package types

import (
	"errors"
	"strings"
	"time"
)

// Agent build task status values. A row moves
//
//	queued -> running -> succeeded | failed | cancelled
//
// where a *retryable* hand-off failure sends it back to queued (bounded by
// MaxAttempts) instead of terminating it.
const (
	// AgentBuildStatusQueued means the row is waiting for a dispatch slot.
	AgentBuildStatusQueued = "queued"
	// AgentBuildStatusRunning means the row has been claimed by the dispatcher
	// and is either being POSTed to the gateway or already executing there.
	AgentBuildStatusRunning = "running"
	// AgentBuildStatusSucceeded means the gateway reported success.
	AgentBuildStatusSucceeded = "succeeded"
	// AgentBuildStatusFailed is a terminal failure.
	AgentBuildStatusFailed = "failed"
	// AgentBuildStatusCancelled was cancelled by an operator.
	AgentBuildStatusCancelled = "cancelled"
)

// DefaultAgentBuildMaxAttempts bounds retryable hand-off failures.
const DefaultAgentBuildMaxAttempts = 3

// ErrAgentBuildTaskNotFound is returned when a ledger row does not exist. The
// repository translates gorm.ErrRecordNotFound into this sentinel so upper
// layers do not need to import the driver's error type.
var ErrAgentBuildTaskNotFound = errors.New("agent build task not found")

// ErrAgentBuildTaskNotRetryable is returned when a retry is requested for a row
// the operator cannot re-run: a task still queued/running, or one that already
// succeeded (re-running a successful build would silently invalidate the wiki
// pages an operator just reviewed, which read as "a finished task turned into
// queued/cancelled" on the monitor page).
var ErrAgentBuildTaskNotRetryable = errors.New("only failed or cancelled tasks can be retried")

// ErrAgentBuildTaskNotCancellable is returned when a cancel is requested for a
// row that already reached a terminal state.
var ErrAgentBuildTaskNotCancellable = errors.New("agent build task already finished")

// IsTerminalAgentBuildStatus reports whether the status is final, i.e. the task
// will never move again without an explicit operator action.
func IsTerminalAgentBuildStatus(status string) bool {
	switch status {
	case AgentBuildStatusSucceeded, AgentBuildStatusFailed, AgentBuildStatusCancelled:
		return true
	default:
		return false
	}
}

// IsRetryableAgentBuildStatus reports whether an operator may re-queue the row.
// Succeeded is deliberately excluded: the build output (wiki pages) is already
// published, and re-running it from the monitor page used to look like the task
// had "turned into" queued/cancelled after finishing.
func IsRetryableAgentBuildStatus(status string) bool {
	switch status {
	case AgentBuildStatusFailed, AgentBuildStatusCancelled:
		return true
	default:
		return false
	}
}

// AgentBuildTask is one document handed to the OpenAI-Agents gateway for wiki
// building. It doubles as the queue row (status=queued) and the run history, so
// the monitor page can show "what is running", "what is waiting", and let an
// operator retry or cancel a specific build.
//
// Payload is the rendered POST body for the gateway and contains the knowledge
// base's bound-model credentials: it is never serialised to clients.
type AgentBuildTask struct {
	ID              string     `json:"id" gorm:"column:id;primaryKey;type:varchar(64)"`
	TenantID        uint64     `json:"tenant_id" gorm:"column:tenant_id"`
	KnowledgeBaseID string     `json:"knowledge_base_id" gorm:"column:knowledge_base_id"`
	KnowledgeID     string     `json:"knowledge_id" gorm:"column:knowledge_id"`
	DocName         string     `json:"doc_name" gorm:"column:doc_name"`
	Skill           string     `json:"skill" gorm:"column:skill"`
	Status          string     `json:"status" gorm:"column:status"`
	Attempts        int        `json:"attempts" gorm:"column:attempts"`
	MaxAttempts     int        `json:"max_attempts" gorm:"column:max_attempts"`
	GatewayURL      string     `json:"-" gorm:"column:gateway_url"`
	GatewayTaskID   string     `json:"gateway_task_id" gorm:"column:gateway_task_id"`
	Payload         string     `json:"-" gorm:"column:payload"`
	OutputText      string     `json:"-" gorm:"column:output_text"`
	LastError       string     `json:"last_error" gorm:"column:last_error"`
	QueuedAt        time.Time  `json:"queued_at" gorm:"column:queued_at"`
	NextAttemptAt   time.Time  `json:"next_attempt_at" gorm:"column:next_attempt_at"`
	SubmittedAt     *time.Time `json:"submitted_at" gorm:"column:submitted_at"`
	StartedAt       *time.Time `json:"started_at" gorm:"column:started_at"`
	FinishedAt      *time.Time `json:"finished_at" gorm:"column:finished_at"`
	DurationMs      *int64     `json:"duration_ms" gorm:"column:duration_ms"`
	CreatedAt       time.Time  `json:"created_at" gorm:"column:created_at"`
	UpdatedAt       time.Time  `json:"updated_at" gorm:"column:updated_at"`
}

// TableName pins the table so the ledger cannot be renamed by GORM convention.
func (AgentBuildTask) TableName() string { return "agent_build_tasks" }

// AgentBuildTaskFilter drives the monitor page's list query.
//
// Status accepts either a single status or a comma separated list (e.g.
// "queued,running" for "everything still in flight"). Empty means "any".
type AgentBuildTaskFilter struct {
	TenantID        uint64
	Status          string
	KnowledgeBaseID string
	KnowledgeID     string
	Keyword         string
	Page            int
	PageSize        int
}

// StatusList splits the filter's status field into a slice, tolerating spaces
// and duplicate commas.
func (f AgentBuildTaskFilter) StatusList() []string {
	if strings.TrimSpace(f.Status) == "" {
		return nil
	}
	parts := strings.Split(f.Status, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// Normalize clamps paging into sane bounds so a hostile query string cannot ask
// for a million rows.
func (f *AgentBuildTaskFilter) Normalize() {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 {
		f.PageSize = 20
	}
	if f.PageSize > 200 {
		f.PageSize = 200
	}
}

// AgentBuildTaskView is the API representation of a ledger row. It deliberately
// omits Payload (credentials) and truncates the gateway output.
type AgentBuildTaskView struct {
	ID                string     `json:"id"`
	KnowledgeBaseID   string     `json:"knowledge_base_id"`
	KnowledgeBaseName string     `json:"knowledge_base_name"`
	KnowledgeID       string     `json:"knowledge_id"`
	DocName           string     `json:"doc_name"`
	Skill             string     `json:"skill"`
	Status            string     `json:"status"`
	Attempts          int        `json:"attempts"`
	MaxAttempts       int        `json:"max_attempts"`
	GatewayTaskID     string     `json:"gateway_task_id"`
	LastError         string     `json:"last_error"`
	OutputPreview     string     `json:"output_preview"`
	QueuedAt          time.Time  `json:"queued_at"`
	SubmittedAt       *time.Time `json:"submitted_at"`
	StartedAt         *time.Time `json:"started_at"`
	FinishedAt        *time.Time `json:"finished_at"`
	WaitSeconds       int64      `json:"wait_seconds"`
	RunSeconds        int64      `json:"run_seconds"`
	DurationMs        *int64     `json:"duration_ms"`
	CanRetry          bool       `json:"can_retry"`
	CanCancel         bool       `json:"can_cancel"`
}

// AgentBuildTaskPage is the paginated list response.
type AgentBuildTaskPage struct {
	Items    []*AgentBuildTaskView `json:"items"`
	Total    int64                 `json:"total"`
	Page     int                   `json:"page"`
	PageSize int                   `json:"page_size"`
}

// AgentBuildTaskSummary feeds the monitor page's header cards. The four
// headline numbers answer the operator's first three questions: what is
// running, what is waiting, and what is failing.
type AgentBuildTaskSummary struct {
	// Running is the number of documents currently executing on the gateway.
	Running int64 `json:"running"`
	// Queued is the number of documents waiting for a dispatch slot.
	Queued int64 `json:"queued"`
	// FailedToday / SucceededToday count terminal rows finished since local
	// midnight.
	FailedToday    int64 `json:"failed_today"`
	SucceededToday int64 `json:"succeeded_today"`
	// Total is every row ever recorded in the ledger, across all statuses.
	Total int64 `json:"total"`
	// Concurrency is the configured in-flight cap: the maximum number of
	// documents WeKnora will hand to the gateway at the same time.
	Concurrency int `json:"concurrency"`
	// OldestQueuedSeconds is how long the head of the queue has been waiting,
	// so a stalled dispatcher is obvious.
	OldestQueuedSeconds int64 `json:"oldest_queued_seconds"`
	// GatewayConfigured / GatewayURL expose whether the hand-off target is set.
	GatewayConfigured bool   `json:"gateway_configured"`
	GatewayURL        string `json:"gateway_url"`
}

// WaitSeconds reports how long the row waited in the WeKnora queue before the
// gateway accepted it (and keeps counting while it is still waiting).
func (t *AgentBuildTask) WaitSeconds(now time.Time) int64 {
	end := now
	if t.SubmittedAt != nil {
		end = *t.SubmittedAt
	}
	if t.QueuedAt.IsZero() || end.Before(t.QueuedAt) {
		return 0
	}
	return int64(end.Sub(t.QueuedAt).Seconds())
}

// RunSeconds reports how long the gateway has been working on the row (and
// keeps counting while it is still running).
func (t *AgentBuildTask) RunSeconds(now time.Time) int64 {
	if t.SubmittedAt == nil {
		return 0
	}
	end := now
	if t.FinishedAt != nil {
		end = *t.FinishedAt
	}
	if end.Before(*t.SubmittedAt) {
		return 0
	}
	return int64(end.Sub(*t.SubmittedAt).Seconds())
}
