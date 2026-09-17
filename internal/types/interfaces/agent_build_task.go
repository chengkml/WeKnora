package interfaces

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// AgentBuildTaskRepository is the durable ledger/queue store behind the agent
// task monitor page. Every document handed to the OpenAI-Agents gateway for
// wiki building owns exactly one row, which doubles as its queue entry and its
// run history.
type AgentBuildTaskRepository interface {
	// Create inserts a new ledger row (normally status=queued).
	Create(ctx context.Context, task *types.AgentBuildTask) error
	// GetByID loads one row; missing rows yield types.ErrAgentBuildTaskNotFound.
	GetByID(ctx context.Context, id string) (*types.AgentBuildTask, error)
	// List returns a filtered page plus the unpaged total.
	List(ctx context.Context, filter types.AgentBuildTaskFilter) ([]*types.AgentBuildTask, int64, error)
	// CountByStatus groups every row by status (drives the header cards).
	CountByStatus(ctx context.Context) (map[string]int64, error)
	// CountFinishedSince counts rows that reached `status` at or after `since`.
	CountFinishedSince(ctx context.Context, status string, since time.Time) (int64, error)
	// OldestQueuedAt returns the queued_at of the oldest waiting row, if any.
	OldestQueuedAt(ctx context.Context) (*time.Time, error)
	// ClaimQueued atomically flips up to `limit` eligible rows to running and
	// returns them. Concurrent dispatchers never claim the same row twice.
	ClaimQueued(ctx context.Context, limit int) ([]*types.AgentBuildTask, error)
	// CountInFlight counts rows the gateway is currently working on.
	CountInFlight(ctx context.Context) (int64, error)
	// CountActiveForKnowledge counts non-terminal rows for one document.
	CountActiveForKnowledge(ctx context.Context, knowledgeID string) (int64, error)
	// MarkSubmitted records that the gateway accepted the task.
	MarkSubmitted(ctx context.Context, id string, gatewayTaskID string, submittedAt time.Time) error
	// MarkRetry sends a retryable failure back to the queue, or terminates the
	// row as failed when `terminal` is true.
	MarkRetry(ctx context.Context, id string, lastError string, nextAttemptAt time.Time, terminal bool) error
	// MarkTerminal records a terminal outcome.
	MarkTerminal(ctx context.Context, id string, status string, output string, lastError string, durationMs int64) error
	// Requeue resets a terminal row into a fresh queue entry (operator retry).
	// It returns the number of rows affected: 0 means the row was not terminal.
	Requeue(ctx context.Context, id string) (int64, error)
	// Cancel marks a queued/running row cancelled. 0 affected means the row had
	// already finished.
	Cancel(ctx context.Context, id string) (int64, error)
	// ListInFlight returns running rows that carry a gateway task id, oldest
	// submission first, for the status poller.
	ListInFlight(ctx context.Context, limit int) ([]*types.AgentBuildTask, error)
	// RecoverInterrupted requeues (or fails) rows left claimed by a process
	// that died before recording a gateway task id.
	RecoverInterrupted(ctx context.Context) (int64, error)
	// KnowledgeBaseNames resolves display names for the list view.
	KnowledgeBaseNames(ctx context.Context, ids []string) (map[string]string, error)
	// TableExists reports whether the ledger table is present, so the
	// dispatcher can idle instead of spamming errors before a migration runs.
	TableExists(ctx context.Context) bool
}

// AgentBuildTaskService is the application facade the HTTP layer talks to and
// the container starts.
type AgentBuildTaskService interface {
	// List returns one page of the ledger, joined with knowledge base names and
	// stripped of credential-bearing fields.
	List(ctx context.Context, filter types.AgentBuildTaskFilter) (*types.AgentBuildTaskPage, error)
	// Summary returns the header counters for the monitor page.
	Summary(ctx context.Context) (*types.AgentBuildTaskSummary, error)
	// Retry puts a finished row back on the queue.
	Retry(ctx context.Context, id string) error
	// Cancel cancels a queued or in-flight row, best-effort cancelling it on the
	// gateway first.
	Cancel(ctx context.Context, id string) error
	// Start launches the dispatcher and status poller goroutines.
	Start(ctx context.Context)
}
