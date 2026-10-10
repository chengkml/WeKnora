package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
)

// docFolderService implements multi-level document directory trees. One
// document belongs to exactly one folder (knowledges.folder_id); folders form
// an adjacency-list tree. Design decision (2026-10-10): single placement,
// recursive listing (a parent folder's view includes its whole subtree),
// coexisting with the flat tag system, non-empty folders cannot be deleted,
// and all document types (including FAQ) participate.
type docFolderService struct {
	repo         interfaces.DocFolderRepository
	knowledgeSvc interfaces.KnowledgeService
}

// NewDocFolderService creates a doc folder service.
func NewDocFolderService(
	repo interfaces.DocFolderRepository,
	knowledgeSvc interfaces.KnowledgeService,
) *docFolderService {
	return &docFolderService{repo: repo, knowledgeSvc: knowledgeSvc}
}

// GetFolder retrieves a single folder by id.
func (s *docFolderService) GetFolder(ctx context.Context, kbID string, id string) (*types.DocFolder, error) {
	return s.repo.GetFolderByID(ctx, kbID, id)
}

// ListFolders returns the whole folder tree of a knowledge base as nested
// DocFolderNodes under parentID ("" = root), each enriched with a recursive
// document count and whether it has children. One call to the DB for folders,
// one for document placements.
func (s *docFolderService) ListFolders(
	ctx context.Context, kbID string, parentID string,
) ([]types.DocFolderNode, error) {
	all, err := s.repo.ListAllFolders(ctx, kbID)
	if err != nil {
		return nil, err
	}
	directDocs, err := s.repo.ListDocumentsGroupedByFolder(ctx, kbID)
	if err != nil {
		return nil, err
	}
	recCounts := recursiveDocFolderCounts(all, directDocs)

	childrenOf := make(map[string][]*types.DocFolder)
	for _, f := range all {
		p := f.ParentID
		if p == "" {
			p = types.DocFolderRootID
		}
		childrenOf[p] = append(childrenOf[p], f)
	}

	var build func(pid string) []types.DocFolderNode
	build = func(pid string) []types.DocFolderNode {
		kids := childrenOf[pid]
		nodes := make([]types.DocFolderNode, 0, len(kids))
		for _, f := range kids {
			sub := build(f.ID)
			nodes = append(nodes, types.DocFolderNode{
				DocFolder:   *f,
				DocCount:    recCounts[f.ID],
				HasChildren: len(sub) > 0,
				Folders:     sub,
			})
		}
		return nodes
	}
	return build(parentID), nil
}

// CreateFolder creates a new empty folder under parentID.
func (s *docFolderService) CreateFolder(
	ctx context.Context, kbID string, tenantID uint64, parentID string, name string,
) (*types.DocFolder, error) {
	name, err := validateDocFolderName(name)
	if err != nil {
		return nil, err
	}

	parentPath := ""
	depth := 1
	if parentID != types.DocFolderRootID {
		parent, err := s.repo.GetFolderByID(ctx, kbID, parentID)
		if err != nil {
			return nil, err
		}
		parentPath = parent.Path
		depth = parent.Depth + 1
	}

	if _, err := s.repo.GetChildFolderByName(ctx, kbID, parentID, name); err == nil {
		return nil, repository.ErrDocFolderConflict
	} else if !errors.Is(err, repository.ErrDocFolderNotFound) {
		return nil, err
	}

	path := name
	if parentPath != "" {
		path = parentPath + "/" + name
	}
	now := time.Now()
	folder := &types.DocFolder{
		ID:              uuid.New().String(),
		TenantID:        tenantID,
		KnowledgeBaseID: kbID,
		ParentID:        parentID,
		Name:            name,
		Path:            path,
		Depth:           depth,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := s.repo.CreateFolder(ctx, folder); err != nil {
		return nil, fmt.Errorf("create doc folder: %w", err)
	}
	return folder, nil
}

// RenameOrMoveFolder renames and/or reparents a folder, recomputing the
// materialized path/depth of its whole subtree. Documents need no position
// updates (they reference the folder id, and the subtree is resolved by the
// path prefix on read) — unlike wiki pages, there is no denormalized
// category cache on documents to refresh.
func (s *docFolderService) RenameOrMoveFolder(
	ctx context.Context, kbID string, id string, newName string, newParentID string, moveParent bool,
) (*types.DocFolder, error) {
	folder, err := s.repo.GetFolderByID(ctx, kbID, id)
	if err != nil {
		return nil, err
	}

	name := folder.Name
	if strings.TrimSpace(newName) != "" {
		if name, err = validateDocFolderName(newName); err != nil {
			return nil, err
		}
	}

	targetParent := folder.ParentID
	if moveParent {
		targetParent = newParentID
	}

	parentPath := ""
	depthBase := 0
	if targetParent != types.DocFolderRootID {
		if targetParent == folder.ID {
			return nil, errors.New("cannot move a folder into itself")
		}
		parent, err := s.repo.GetFolderByID(ctx, kbID, targetParent)
		if err != nil {
			return nil, err
		}
		if parent.Path == folder.Path || strings.HasPrefix(parent.Path, folder.Path+"/") {
			return nil, errors.New("cannot move a folder into its own descendant")
		}
		parentPath = parent.Path
		depthBase = parent.Depth
	}

	if existing, err := s.repo.GetChildFolderByName(ctx, kbID, targetParent, name); err == nil {
		if existing.ID != folder.ID {
			return nil, repository.ErrDocFolderConflict
		}
	} else if !errors.Is(err, repository.ErrDocFolderNotFound) {
		return nil, err
	}

	oldPath := folder.Path
	newPath := name
	if parentPath != "" {
		newPath = parentPath + "/" + name
	}
	if newPath == oldPath && targetParent == folder.ParentID {
		return folder, nil // no-op
	}

	all, err := s.repo.ListAllFolders(ctx, kbID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	var updated *types.DocFolder
	for _, f := range all {
		switch {
		case f.ID == folder.ID:
			f.ParentID = targetParent
			f.Name = name
			f.Path = newPath
			f.Depth = depthBase + 1
		case strings.HasPrefix(f.Path, oldPath+"/"):
			f.Path = newPath + f.Path[len(oldPath):]
			f.Depth = len(docFolderSegments(f.Path))
		default:
			continue
		}
		f.UpdatedAt = now
		if err := s.repo.UpdateFolder(ctx, f); err != nil {
			return nil, err
		}
		if f.ID == folder.ID {
			updated = f
		}
	}
	if updated == nil {
		updated = folder
	}
	return updated, nil
}

// DeleteFolder removes a folder that has no documents and no child folders.
// Non-destructive by design — the UI must relocate contents first.
func (s *docFolderService) DeleteFolder(ctx context.Context, kbID string, id string) error {
	if _, err := s.repo.GetFolderByID(ctx, kbID, id); err != nil {
		return err
	}
	children, err := s.repo.ListChildFolders(ctx, kbID, id)
	if err != nil {
		return err
	}
	if len(children) > 0 {
		return errors.New("folder is not empty: it still has sub-folders")
	}
	count, err := s.repo.CountDocumentsInFolder(ctx, kbID, id)
	if err != nil {
		return err
	}
	if count > 0 {
		return errors.New("folder is not empty: it still contains documents")
	}
	return s.repo.DeleteFolder(ctx, kbID, id)
}

// ExpandSubtree returns id plus every descendant folder id. The folder must
// exist under kbID ("" expands to nothing). Used by the list filter for
// recursive folder views.
func (s *docFolderService) ExpandSubtree(ctx context.Context, kbID string, id string) ([]string, error) {
	if id == "" {
		return nil, nil
	}
	// Validate the folder exists (errors for cross-KB or bogus ids).
	if _, err := s.repo.GetFolderByID(ctx, kbID, id); err != nil {
		return nil, err
	}
	all, err := s.repo.ListAllFolders(ctx, kbID)
	if err != nil {
		return nil, err
	}
	parentOf := make(map[string]string, len(all))
	for _, f := range all {
		if f != nil {
			parentOf[f.ID] = f.ParentID
		}
	}
	ids := []string{id}
	for _, f := range all {
		if f.ID == id {
			continue
		}
		// walk up the ancestor chain; if we reach id, f is a descendant
		for cur := f.ParentID; cur != ""; cur = parentOf[cur] {
			if cur == id {
				ids = append(ids, f.ID)
				break
			}
		}
	}
	return ids, nil
}

// MoveDocument relocates a single document into folderID ("" = KB root, i.e.
// moved out of any folder). The folder must belong to the same KB; the
// document must exist and belong to the KB.
func (s *docFolderService) MoveDocument(ctx context.Context, kbID string, knowledgeID string, folderID string) error {
	return s.BatchMoveDocuments(ctx, kbID, []string{knowledgeID}, folderID)
}

// BatchMoveDocuments relocates one or more documents into folderID ("" = KB
// root). All knowledge ids must be live and belong to kbID (any miss aborts
// the whole batch, no partial moves); folderID, when non-empty, must exist in
// kbID's folder tree. Single-placement semantics: the folder_id replaces the
// document's current folder.
func (s *docFolderService) BatchMoveDocuments(ctx context.Context, kbID string, knowledgeIDs []string, folderID string) error {
	knowledgeIDs = dedupeDocIDs(knowledgeIDs)
	if len(knowledgeIDs) == 0 {
		return nil
	}
	if folderID != types.DocFolderRootID {
		if _, err := s.repo.GetFolderByID(ctx, kbID, folderID); err != nil {
			return fmt.Errorf("move documents: %w", err)
		}
	}
	count, err := s.repo.CountDocumentsByIDs(ctx, kbID, knowledgeIDs)
	if err != nil {
		return err
	}
	if count != int64(len(knowledgeIDs)) {
		return errors.New("some documents do not exist or do not belong to this knowledge base")
	}
	return s.repo.SetKnowledgeFolder(ctx, kbID, folderID, knowledgeIDs)
}

// dedupeDocIDs removes duplicate knowledge ids preserving order.
func dedupeDocIDs(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// recursiveDocFolderCounts computes for every folder the number of distinct
// documents in its whole subtree (itself included). direct maps folder_id ->
// set of document ids directly filed under it.
func recursiveDocFolderCounts(all []*types.DocFolder, direct map[string]map[string]struct{}) map[string]int64 {
	if len(all) == 0 {
		return map[string]int64{}
	}
	parentOf := make(map[string]string, len(all))
	for _, f := range all {
		if f != nil {
			parentOf[f.ID] = f.ParentID
		}
	}
	ancestorSet := make(map[string]map[string]struct{}, len(all))
	var buildAncestors func(id string) map[string]struct{}
	buildAncestors = func(id string) map[string]struct{} {
		if set, ok := ancestorSet[id]; ok {
			return set
		}
		set := map[string]struct{}{}
		if id != "" {
			set[id] = struct{}{}
			if parent := parentOf[id]; parent != "" && parent != types.DocFolderRootID {
				for a := range buildAncestors(parent) {
					set[a] = struct{}{}
				}
			}
		}
		ancestorSet[id] = set
		return set
	}
	res := make(map[string]int64, len(all))
	for folderID, docSet := range direct {
		ancs := buildAncestors(folderID)
		for docID := range docSet {
			for a := range ancs {
				res[a]++
			}
			_ = docID
		}
	}
	return res
}

// docFolderSegments splits a materialized path into its name chain.
func docFolderSegments(path string) []string {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	return types.CleanWikiCategoryPath(strings.Split(path, "/"))
}

// validateDocFolderName trims and rejects blank names or names carrying
// directory separators (a folder name is a single tree level).
func validateDocFolderName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("folder name is required")
	}
	if strings.ContainsAny(name, "/｜|／") {
		return "", fmt.Errorf("folder name %q must not contain a path separator", name)
	}
	return name, nil
}
