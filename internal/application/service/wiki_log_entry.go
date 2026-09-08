package service

import (
	"context"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
)

// wikiLogEntryService is a thin wrapper over WikiLogEntryRepository. The
// event layer carries no business rules today, so the service just
// forwards to the repo — the indirection exists so handlers / ingest
// batch code depend on an interface rather than a concrete repo type.
type wikiLogEntryService struct {
	repo     interfaces.WikiLogEntryRepository
	spanRepo repository.KnowledgeSpanRepository
}

// NewWikiLogEntryService constructs a WikiLogEntryService backed by the
// given repository. spanRepo is optional (may be nil); when present it is
// used to project external agent progress (agent_* wiki log events that
// carry a knowledge_id) into knowledge_processing_spans, so the document
// "查看 Trace" timeline shows the skill's processing flow.
func NewWikiLogEntryService(repo interfaces.WikiLogEntryRepository, spanRepo repository.KnowledgeSpanRepository) interfaces.WikiLogEntryService {
	return &wikiLogEntryService{repo: repo, spanRepo: spanRepo}
}

// AppendBatch records the given events in one database round trip. Empty
// batches are a no-op (the repo handles that).
func (s *wikiLogEntryService) AppendBatch(ctx context.Context, entries []*types.WikiLogEntry) error {
	if err := s.repo.AppendBatch(ctx, entries); err != nil {
		return err
	}
	// Best-effort projection into the processing-span trace. The wiki log
	// entry remains the source of truth; a span-write failure must never
	// fail the log write itself.
	for _, e := range entries {
		if e == nil || e.KnowledgeID == "" || !strings.HasPrefix(e.Action, "agent_") {
			continue
		}
		s.appendAgentProgressSpan(ctx, e)
	}
	return nil
}

// appendAgentProgressSpan mirrors an external agent progress event as a
// "done" subspan of the document's latest processing attempt, so the
// per-document Trace drawer renders the skill's build steps (找文件→血缘→
// 建目录→摘要→实体→关键词→索引) alongside WeKnora's own pipeline stages.
//
// Attachment point: the postprocess stage span when present (the same
// place postprocess.summary / postprocess.question subspans live),
// otherwise the attempt root. If the document has no spans at all we
// silently skip — there is no trace to attach to.
func (s *wikiLogEntryService) appendAgentProgressSpan(ctx context.Context, e *types.WikiLogEntry) {
	if s.spanRepo == nil {
		return
	}
	attempt, err := s.spanRepo.LatestAttempt(ctx, e.KnowledgeID)
	if err != nil || attempt <= 0 {
		logger.Warnf(ctx, "[wiki-log] skip agent span: latest attempt lookup failed kid=%s: %v", e.KnowledgeID, err)
		return
	}
	rows, err := s.spanRepo.ListByAttempt(ctx, e.KnowledgeID, attempt)
	if err != nil {
		logger.Warnf(ctx, "[wiki-log] skip agent span: list spans failed kid=%s attempt=%d: %v", e.KnowledgeID, attempt, err)
		return
	}
	var parent *types.KnowledgeProcessingSpan
	for i := range rows {
		r := &rows[i]
		if r.Kind == types.SpanKindStage && r.Name == types.StagePostProcess {
			parent = r
			break
		}
	}
	if parent == nil {
		for i := range rows {
			r := &rows[i]
			if r.Kind == types.SpanKindRoot {
				parent = r
				break
			}
		}
	}
	if parent == nil {
		return
	}
	now := time.Now()
	row := &types.KnowledgeProcessingSpan{
		KnowledgeID:  e.KnowledgeID,
		Attempt:      attempt,
		SpanID:       strings.ReplaceAll(uuid.NewString(), "-", ""),
		ParentSpanID: parent.SpanID,
		Name:         e.Action,
		Kind:         types.SpanKindSubSpan,
		Status:       types.SpanStatusDone,
		StartedAt:    &now,
		FinishedAt:   &now,
	}
	if e.DocTitle != "" || e.Summary != "" {
		row.Output = types.JSONMap{}
		if e.DocTitle != "" {
			row.Output["doc_title"] = e.DocTitle
		}
		if e.Summary != "" {
			row.Output["summary"] = e.Summary
		}
	}
	if err := s.spanRepo.Upsert(ctx, row); err != nil {
		logger.Warnf(ctx, "[wiki-log] append agent span failed kid=%s action=%s: %v", e.KnowledgeID, e.Action, err)
	}
}

// List paginates the per-KB event feed. See repo.List for cursor semantics.
func (s *wikiLogEntryService) List(ctx context.Context, kbID string, cursor string, limit int) (*types.WikiLogEntryListResponse, error) {
	entries, nextCursor, err := s.repo.List(ctx, kbID, cursor, limit)
	if err != nil {
		return nil, err
	}
	if entries == nil {
		// Normalise to an empty slice so clients don't need to
		// distinguish `null` from `[]`.
		entries = []*types.WikiLogEntry{}
	}
	return &types.WikiLogEntryListResponse{
		Entries:    entries,
		NextCursor: nextCursor,
	}, nil
}

// DeleteByKB removes the log feed for a KB. Called when the KB itself is
// being deleted, so no further reads happen.
func (s *wikiLogEntryService) DeleteByKB(ctx context.Context, kbID string) error {
	return s.repo.DeleteByKB(ctx, kbID)
}
