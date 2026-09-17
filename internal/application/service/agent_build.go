package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// Tunables for the agent build dispatcher. Every one of them is an env
// override so an operator can retune the queue without a rebuild; the defaults
// are deliberately conservative because the OpenAI-Agents gateway only has a
// handful of execution slots and shares them with chat workloads.
const (
	// AgentBuildConcurrencyEnv caps how many documents WeKnora hands to the
	// gateway at the same time. This is the "queue control" the operator asked
	// for: with concurrency=6 and a gateway pool of 8 slots, submissions from
	// WeKnora can never fill the gateway and start bouncing with HTTP 429.
	AgentBuildConcurrencyEnv = "WEKNORA_AGENT_DISPATCH_CONCURRENCY"
	// AgentBuildPollIntervalEnv controls how often in-flight tasks are polled.
	AgentBuildPollIntervalEnv = "WEKNORA_AGENT_POLL_INTERVAL_S"
	// AgentBuildMaxWaitEnv is the ceiling on a single gateway task. Beyond it
	// the row is failed instead of being polled forever.
	AgentBuildMaxWaitEnv = "WEKNORA_AGENT_MAX_WAIT_S"

	// AgentBuildCallbackURLEnv is the gateway's task collection endpoint
	// (e.g. http://10.1.215.50:8080/tasks). The status and cancel endpoints are
	// derived from it: <url>/{task_id} and <url>/{task_id}/cancel.
	AgentBuildCallbackURLEnv = "WIKI_AGENT_CALLBACK_URL"

	defaultAgentBuildConcurrency  = 6
	defaultAgentBuildPollInterval = 3 * time.Second
	defaultAgentBuildMaxWait      = time.Hour

	// agentBuildDispatchTick is how often the dispatcher looks for free slots.
	agentBuildDispatchTick = 2 * time.Second
	// agentBuildHTTPTimeout bounds a single gateway call.
	agentBuildHTTPTimeout = 30 * time.Second
	// agentBuildPollBatch bounds how many in-flight tasks are polled per tick.
	agentBuildPollBatch = 100
	// agentBuildOutputPreview is how much gateway output is kept for the UI.
	agentBuildOutputPreview = 2000
	// agentBuildMaxBackoff caps the retry backoff.
	agentBuildMaxBackoff = 5 * time.Minute
	// agentBuildQueueFullBackoff is the minimum backoff applied on HTTP 429.
	agentBuildQueueFullBackoff = 60 * time.Second
)

// agentBuildTaskService owns the WeKnora side of the wiki-build hand-off:
//
//   - dispatch: claims queued ledger rows (bounded by the concurrency knob),
//     POSTs the pre-rendered payload to the gateway and stores the returned
//     gateway task id. A retryable failure goes back to the queue with
//     backoff; a permanent one terminates the row.
//   - poll: follows every in-flight gateway task to a terminal state and
//     mirrors it onto the ledger, so the monitor page and the gateway never
//     disagree.
//
// Both loops are plain goroutines over the durable ledger, which means the
// queue survives a restart and also works in Lite mode (no Redis needed).
type agentBuildTaskService struct {
	repo         interfaces.AgentBuildTaskRepository
	client       *http.Client
	concurrency  int
	pollInterval time.Duration
	maxWait      time.Duration
	startOnce    sync.Once
}

var _ interfaces.AgentBuildTaskService = (*agentBuildTaskService)(nil)

// NewAgentBuildTaskService wires the dispatcher/poller around the ledger.
func NewAgentBuildTaskService(repo interfaces.AgentBuildTaskRepository) interfaces.AgentBuildTaskService {
	return &agentBuildTaskService{
		repo:         repo,
		client:       &http.Client{Timeout: agentBuildHTTPTimeout},
		concurrency:  envInt(AgentBuildConcurrencyEnv, defaultAgentBuildConcurrency),
		pollInterval: envSeconds(AgentBuildPollIntervalEnv, defaultAgentBuildPollInterval),
		maxWait:      envSeconds(AgentBuildMaxWaitEnv, defaultAgentBuildMaxWait),
	}
}

// Start launches both background loops. It never returns an error: a missing
// migration must not stop the rest of the application from booting.
func (s *agentBuildTaskService) Start(ctx context.Context) {
	s.startOnce.Do(func() {
		if !s.repo.TableExists(ctx) {
			logger.Warnf(ctx, "[agent-build] table agent_build_tasks is missing "+
				"(migration 000078 not applied?); dispatcher stays idle")
			return
		}
		if s.concurrency <= 0 {
			logger.Warnf(ctx, "[agent-build] %s is 0; dispatcher stays idle", AgentBuildConcurrencyEnv)
			return
		}
		if recovered, err := s.repo.RecoverInterrupted(ctx); err != nil {
			logger.Warnf(ctx, "[agent-build] recovering interrupted tasks failed: %v", err)
		} else if recovered > 0 {
			logger.Infof(ctx, "[agent-build] recovered %d interrupted task(s) after restart", recovered)
		}
		logger.Infof(ctx, "[agent-build] dispatcher started (concurrency=%d poll=%s max_wait=%s gateway=%s)",
			s.concurrency, s.pollInterval, s.maxWait, gatewayBaseURL())
		go s.dispatchLoop(ctx)
		go s.pollLoop(ctx)
	})
}

func (s *agentBuildTaskService) dispatchLoop(ctx context.Context) {
	ticker := time.NewTicker(agentBuildDispatchTick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			logger.Infof(ctx, "[agent-build] dispatcher stopped")
			return
		case <-ticker.C:
			s.dispatchOnce(ctx)
		}
	}
}

func (s *agentBuildTaskService) pollLoop(ctx context.Context) {
	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			logger.Infof(ctx, "[agent-build] status poller stopped")
			return
		case <-ticker.C:
			s.pollOnce(ctx)
		}
	}
}

// dispatchOnce fills the free slots: it claims rows atomically (so two app
// instances never double-submit) and POSTs them concurrently.
func (s *agentBuildTaskService) dispatchOnce(ctx context.Context) {
	inFlight, err := s.repo.CountInFlight(ctx)
	if err != nil {
		logger.Warnf(ctx, "[agent-build] counting in-flight tasks failed: %v", err)
		return
	}
	slots := s.concurrency - int(inFlight)
	if slots <= 0 {
		return
	}
	claimed, err := s.repo.ClaimQueued(ctx, slots)
	if err != nil {
		logger.Warnf(ctx, "[agent-build] claiming queued tasks failed: %v", err)
		return
	}
	if len(claimed) == 0 {
		return
	}
	var wg sync.WaitGroup
	for _, task := range claimed {
		wg.Add(1)
		go func(t *types.AgentBuildTask) {
			defer wg.Done()
			s.submit(ctx, t)
		}(task)
	}
	wg.Wait()
}

// submit hands one claimed row to the gateway and records the task id it
// returns. The call is short: completion is tracked by the poller, so a slow
// gateway build never blocks a dispatch slot for its whole duration.
func (s *agentBuildTaskService) submit(ctx context.Context, task *types.AgentBuildTask) {
	if strings.TrimSpace(task.GatewayURL) == "" {
		s.failPermanently(ctx, task, "no gateway endpoint configured ("+AgentBuildCallbackURLEnv+" is empty)")
		return
	}
	reqCtx, cancel := context.WithTimeout(ctx, agentBuildHTTPTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, task.GatewayURL, strings.NewReader(task.Payload))
	if err != nil {
		s.failPermanently(ctx, task, fmt.Sprintf("building the gateway request failed: %v", err))
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		s.retryLater(ctx, task, fmt.Sprintf("posting to the gateway failed: %v", err), 0)
		return
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
	if readErr != nil {
		s.retryLater(ctx, task, fmt.Sprintf("reading the gateway response failed: %v", readErr), 0)
		return
	}

	switch {
	case resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusOK:
		var out struct {
			TaskID string `json:"task_id"`
			Status string `json:"status"`
		}
		if err := json.Unmarshal(body, &out); err != nil || strings.TrimSpace(out.TaskID) == "" {
			s.retryLater(ctx, task, fmt.Sprintf("gateway accepted the task (HTTP %d) but returned no task_id: %s",
				resp.StatusCode, agentBuildPreview(string(body), 200)), 0)
			return
		}
		if err := s.repo.MarkSubmitted(ctx, task.ID, out.TaskID, time.Now()); err != nil {
			logger.Warnf(ctx, "[agent-build] recording the gateway task id failed id=%s: %v", task.ID, err)
			return
		}
		logger.Infof(ctx, "[agent-build] submitted doc=%q kb=%s gateway_task=%s attempt=%d",
			task.DocName, task.KnowledgeBaseID, out.TaskID, task.Attempts)
	case resp.StatusCode == http.StatusTooManyRequests:
		s.retryLater(ctx, task, fmt.Sprintf("the gateway queue is full (HTTP 429): %s",
			agentBuildPreview(string(body), 200)), agentBuildQueueFullBackoff)
	case resp.StatusCode >= 500:
		s.retryLater(ctx, task, fmt.Sprintf("the gateway failed with HTTP %d: %s",
			resp.StatusCode, agentBuildPreview(string(body), 200)), 0)
	default:
		// Any other 4xx means the request itself is unacceptable; retrying the
		// identical body cannot help.
		s.failPermanently(ctx, task, fmt.Sprintf("the gateway rejected the task with HTTP %d: %s",
			resp.StatusCode, agentBuildPreview(string(body), 400)))
	}
}

func (s *agentBuildTaskService) pollOnce(ctx context.Context) {
	tasks, err := s.repo.ListInFlight(ctx, agentBuildPollBatch)
	if err != nil {
		logger.Warnf(ctx, "[agent-build] listing in-flight tasks failed: %v", err)
		return
	}
	now := time.Now()
	for _, task := range tasks {
		if ctx.Err() != nil {
			return
		}
		if task.SubmittedAt != nil && s.maxWait > 0 && now.Sub(*task.SubmittedAt) > s.maxWait {
			s.finish(ctx, task, types.AgentBuildStatusFailed, "",
				fmt.Sprintf("the gateway task exceeded the %s maximum wait", s.maxWait), 0)
			continue
		}
		s.syncTask(ctx, task)
	}
}

// syncTask mirrors one gateway task onto its ledger row.
func (s *agentBuildTaskService) syncTask(ctx context.Context, task *types.AgentBuildTask) {
	url := taskStatusURL(task.GatewayURL, task.GatewayTaskID)
	if url == "" {
		return
	}
	reqCtx, cancel := context.WithTimeout(ctx, agentBuildHTTPTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return
	}
	resp, err := s.client.Do(req)
	if err != nil {
		// Transient: the row stays running and is retried on the next tick.
		logger.Debugf(ctx, "[agent-build] polling gateway task %s failed: %v", task.GatewayTaskID, err)
		return
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	_ = resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		s.finish(ctx, task, types.AgentBuildStatusFailed, "",
			"the gateway has no record of this task (it restarted or dropped it)", 0)
		return
	}
	if readErr != nil || resp.StatusCode != http.StatusOK {
		logger.Debugf(ctx, "[agent-build] polling gateway task %s returned HTTP %d", task.GatewayTaskID, resp.StatusCode)
		return
	}
	var out struct {
		Status      string `json:"status"`
		OutputText  string `json:"output_text"`
		ErrorDetail string `json:"error_detail"`
		RunsMs      int64  `json:"runs_ms"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		logger.Debugf(ctx, "[agent-build] decoding gateway task %s failed: %v", task.GatewayTaskID, err)
		return
	}
	switch strings.ToLower(strings.TrimSpace(out.Status)) {
	case "succeeded":
		s.finish(ctx, task, types.AgentBuildStatusSucceeded, out.OutputText, "", out.RunsMs)
	case "failed":
		msg := strings.TrimSpace(out.ErrorDetail)
		if msg == "" {
			msg = "the gateway reported a failure"
		}
		s.finish(ctx, task, types.AgentBuildStatusFailed, out.OutputText, msg, out.RunsMs)
	case "cancelled", "canceled":
		s.finish(ctx, task, types.AgentBuildStatusCancelled, out.OutputText, "cancelled on the gateway", out.RunsMs)
	default:
		// pending / running / queued: still in flight, poll again next tick.
	}
}

// Retry puts a finished row back on the queue, so an operator can re-run a
// build without touching the knowledge base.
func (s *agentBuildTaskService) Retry(ctx context.Context, id string) error {
	task, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if !types.IsTerminalAgentBuildStatus(task.Status) {
		return types.ErrAgentBuildTaskNotRetryable
	}
	affected, err := s.repo.Requeue(ctx, id)
	if err != nil {
		return err
	}
	if affected == 0 {
		return types.ErrAgentBuildTaskNotRetryable
	}
	logger.Infof(ctx, "[agent-build] doc=%q requeued by the operator (was %s)", task.DocName, task.Status)
	return nil
}

// Cancel stops a queued or in-flight task. The gateway is asked to stop first
// on a best-effort basis; WeKnora always records the cancellation, otherwise a
// unreachable gateway would make a runaway task impossible to kill.
func (s *agentBuildTaskService) Cancel(ctx context.Context, id string) error {
	task, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if types.IsTerminalAgentBuildStatus(task.Status) {
		return types.ErrAgentBuildTaskNotCancellable
	}
	if task.GatewayTaskID != "" {
		s.cancelAtGateway(ctx, task)
	}
	affected, err := s.repo.Cancel(ctx, id)
	if err != nil {
		return err
	}
	if affected == 0 {
		return types.ErrAgentBuildTaskNotCancellable
	}
	logger.Infof(ctx, "[agent-build] doc=%q cancelled by the operator", task.DocName)
	return nil
}

func (s *agentBuildTaskService) cancelAtGateway(ctx context.Context, task *types.AgentBuildTask) {
	url := taskCancelURL(task.GatewayURL, task.GatewayTaskID)
	if url == "" {
		return
	}
	reqCtx, cancel := context.WithTimeout(ctx, agentBuildHTTPTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, nil)
	if err != nil {
		return
	}
	resp, err := s.client.Do(req)
	if err != nil {
		logger.Warnf(ctx, "[agent-build] cancelling gateway task %s failed: %v", task.GatewayTaskID, err)
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 16<<10))
	_ = resp.Body.Close()
	logger.Infof(ctx, "[agent-build] gateway cancel for task %s returned HTTP %d", task.GatewayTaskID, resp.StatusCode)
}

// List returns one page of the ledger with knowledge base names resolved and
// credential-bearing fields stripped.
func (s *agentBuildTaskService) List(ctx context.Context, filter types.AgentBuildTaskFilter) (*types.AgentBuildTaskPage, error) {
	filter.Normalize()
	rows, total, err := s.repo.List(ctx, filter)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(rows))
	seen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if row.KnowledgeBaseID == "" {
			continue
		}
		if _, ok := seen[row.KnowledgeBaseID]; ok {
			continue
		}
		seen[row.KnowledgeBaseID] = struct{}{}
		ids = append(ids, row.KnowledgeBaseID)
	}
	names, err := s.repo.KnowledgeBaseNames(ctx, ids)
	if err != nil {
		logger.Warnf(ctx, "[agent-build] resolving knowledge base names failed: %v", err)
		names = map[string]string{}
	}

	now := time.Now()
	items := make([]*types.AgentBuildTaskView, 0, len(rows))
	for _, row := range rows {
		terminal := types.IsTerminalAgentBuildStatus(row.Status)
		items = append(items, &types.AgentBuildTaskView{
			ID:                row.ID,
			KnowledgeBaseID:   row.KnowledgeBaseID,
			KnowledgeBaseName: names[row.KnowledgeBaseID],
			KnowledgeID:       row.KnowledgeID,
			DocName:           row.DocName,
			Skill:             row.Skill,
			Status:            row.Status,
			Attempts:          row.Attempts,
			MaxAttempts:       row.MaxAttempts,
			GatewayTaskID:     row.GatewayTaskID,
			LastError:         row.LastError,
			OutputPreview:     agentBuildPreview(row.OutputText, agentBuildOutputPreview),
			QueuedAt:          row.QueuedAt,
			SubmittedAt:       row.SubmittedAt,
			StartedAt:         row.StartedAt,
			FinishedAt:        row.FinishedAt,
			WaitSeconds:       row.WaitSeconds(now),
			RunSeconds:        row.RunSeconds(now),
			DurationMs:        row.DurationMs,
			CanRetry:          terminal,
			CanCancel:         !terminal,
		})
	}
	return &types.AgentBuildTaskPage{
		Items:    items,
		Total:    total,
		Page:     filter.Page,
		PageSize: filter.PageSize,
	}, nil
}

// Summary feeds the monitor page's header cards. tenantID scopes the counters
// to the caller's workspace; 0 (system admin) counts every tenant.
func (s *agentBuildTaskService) Summary(
	ctx context.Context, tenantID uint64,
) (*types.AgentBuildTaskSummary, error) {
	counts, err := s.repo.CountByStatus(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	failedToday, err := s.repo.CountFinishedSince(ctx, types.AgentBuildStatusFailed, midnight, tenantID)
	if err != nil {
		return nil, err
	}
	succeededToday, err := s.repo.CountFinishedSince(ctx, types.AgentBuildStatusSucceeded, midnight, tenantID)
	if err != nil {
		return nil, err
	}
	total := int64(0)
	for _, n := range counts {
		total += n
	}
	summary := &types.AgentBuildTaskSummary{
		Total:             total,
		Running:           counts[types.AgentBuildStatusRunning],
		Queued:            counts[types.AgentBuildStatusQueued],
		FailedToday:       failedToday,
		SucceededToday:    succeededToday,
		Concurrency:       s.concurrency,
		GatewayConfigured: gatewayBaseURL() != "",
		GatewayURL:        gatewayBaseURL(),
	}
	if oldest, err := s.repo.OldestQueuedAt(ctx, tenantID); err == nil && oldest != nil {
		if wait := int64(now.Sub(*oldest).Seconds()); wait > 0 {
			summary.OldestQueuedSeconds = wait
		}
	}
	return summary, nil
}

// finish records a terminal outcome for one row.
func (s *agentBuildTaskService) finish(
	ctx context.Context,
	task *types.AgentBuildTask,
	status string,
	output string,
	lastError string,
	durationMs int64,
) {
	if err := s.repo.MarkTerminal(ctx, task.ID, status, output, lastError, durationMs); err != nil {
		logger.Warnf(ctx, "[agent-build] recording the terminal status failed id=%s: %v", task.ID, err)
		return
	}
	logger.Infof(ctx, "[agent-build] doc=%q finished status=%s runs_ms=%d err=%q",
		task.DocName, status, durationMs, lastError)
}

// retryLater puts a row back on the queue with backoff, or terminates it once
// the attempts are exhausted.
func (s *agentBuildTaskService) retryLater(
	ctx context.Context,
	task *types.AgentBuildTask,
	message string,
	minBackoff time.Duration,
) {
	backoff := time.Duration(task.Attempts*task.Attempts) * 30 * time.Second
	if backoff < minBackoff {
		backoff = minBackoff
	}
	if backoff > agentBuildMaxBackoff {
		backoff = agentBuildMaxBackoff
	}
	maxAttempts := task.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = types.DefaultAgentBuildMaxAttempts
	}
	terminal := task.Attempts >= maxAttempts
	if err := s.repo.MarkRetry(ctx, task.ID, message, time.Now().Add(backoff), terminal); err != nil {
		logger.Warnf(ctx, "[agent-build] recording the retry failed id=%s: %v", task.ID, err)
		return
	}
	if terminal {
		logger.Warnf(ctx, "[agent-build] giving up on doc=%q after %d attempt(s): %s",
			task.DocName, task.Attempts, message)
		return
	}
	logger.Warnf(ctx, "[agent-build] retrying doc=%q in %s (attempt %d/%d): %s",
		task.DocName, backoff, task.Attempts, maxAttempts, message)
}

func (s *agentBuildTaskService) failPermanently(ctx context.Context, task *types.AgentBuildTask, message string) {
	if err := s.repo.MarkTerminal(ctx, task.ID, types.AgentBuildStatusFailed, "", message, 0); err != nil {
		logger.Warnf(ctx, "[agent-build] recording the failure failed id=%s: %v", task.ID, err)
		return
	}
	logger.Warnf(ctx, "[agent-build] doc=%q failed permanently: %s", task.DocName, message)
}

// taskStatusURL derives the gateway's task-detail endpoint from the configured
// collection endpoint (WIKI_AGENT_CALLBACK_URL, e.g. http://host:8080/tasks).
func taskStatusURL(collection, taskID string) string {
	base := strings.TrimSpace(collection)
	taskID = strings.TrimSpace(taskID)
	if base == "" || taskID == "" {
		return ""
	}
	return strings.TrimSuffix(base, "/") + "/" + taskID
}

// taskCancelURL derives the gateway's cancel endpoint.
func taskCancelURL(collection, taskID string) string {
	if url := taskStatusURL(collection, taskID); url != "" {
		return url + "/cancel"
	}
	return ""
}

// gatewayBaseURL is the configured hand-off endpoint, used for reporting only.
func gatewayBaseURL() string {
	return strings.TrimSpace(os.Getenv(AgentBuildCallbackURLEnv))
}

// agentBuildPreview truncates text on a rune boundary so Chinese output is never cut
// mid-character.
func agentBuildPreview(text string, limit int) string {
	trimmed := strings.TrimSpace(text)
	if limit <= 0 || len(trimmed) <= limit {
		return trimmed
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(trimmed[cut]) {
		cut--
	}
	return trimmed[:cut] + "..."
}

func envInt(key string, def int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return def
	}
	return value
}

func envSeconds(key string, def time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return def
	}
	return time.Duration(value) * time.Second
}
