package types

import "encoding/json"

// AgentBuildTaskDetail is what the monitor page's "执行日志 / trace" drawer
// renders: the ledger row itself, plus a best-effort snapshot of the matching
// task and trace pulled from the OpenAI-Agents gateway.
//
// Every gateway field is optional: the gateway may be unreachable, the row may
// never have been submitted, or the trace may already have been rotated out of
// traces.jsonl. In those cases the request still succeeds and explains itself
// through Notes instead of failing the whole page.
type AgentBuildTaskDetail struct {
	// Task is the ledger row (same shape as a list item).
	Task *AgentBuildTaskView `json:"task"`
	// Gateway is the live gateway task snapshot (status, output, error, trace id).
	Gateway *AgentBuildGatewayTask `json:"gateway,omitempty"`
	// Trace is the replayable execution trace of the gateway run.
	Trace *AgentBuildTrace `json:"trace,omitempty"`
	// Notes carries human readable explanations for whatever is missing.
	Notes []string `json:"notes,omitempty"`
}

// AgentBuildGatewayTask mirrors GET <gateway>/tasks/{task_id}.
type AgentBuildGatewayTask struct {
	TaskID      string `json:"task_id"`
	Status      string `json:"status"`
	AgentName   string `json:"agent_name"`
	RunsMs      int64  `json:"runs_ms"`
	OutputText  string `json:"output_text"`
	ErrorDetail string `json:"error_detail"`
	// TraceID is the business run id (gateway-run-...). It is NOT the key of the
	// gateway trace store, so it cannot be used against GET /traces/{id}.
	TraceID     string `json:"trace_id"`
	// SDKTraceID is the OpenAI-Agents trace id (trace_...) that indexes the
	// gateway trace store; empty for tasks that ran before the gateway started
	// returning it.
	SDKTraceID  string `json:"sdk_trace_id"`
}

// AgentBuildTrace mirrors GET <gateway>/traces/{trace_id} (span tree flattened
// into a parent referenced list so the UI can rebuild the tree).
type AgentBuildTrace struct {
	TraceID   string                `json:"trace_id"`
	Name      string                `json:"name"`
	SpanCount int                   `json:"span_count"`
	Spans     []*AgentBuildTraceSpan `json:"spans"`
}

// AgentBuildTraceSpan is one exported OpenAI-Agents span, flattened for display.
// Input/Output/Detail are pre-truncated strings (JSON or plain text) so a single
// LLM prompt cannot blow up the response.
type AgentBuildTraceSpan struct {
	ID         string          `json:"id"`
	ParentID   string          `json:"parent_id,omitempty"`
	Type       string          `json:"type"`
	Name       string          `json:"name,omitempty"`
	StartedAt  string          `json:"started_at,omitempty"`
	EndedAt    string          `json:"ended_at,omitempty"`
	DurationMs *int64          `json:"duration_ms"`
	Status     string          `json:"status"`
	Error      string          `json:"error,omitempty"`
	Model      string          `json:"model,omitempty"`
	Summary    string          `json:"summary,omitempty"`
	Input      string          `json:"input,omitempty"`
	Output     string          `json:"output,omitempty"`
	Detail     string          `json:"detail,omitempty"`
	Raw        json.RawMessage `json:"-"`
}
