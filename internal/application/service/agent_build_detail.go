package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// Limits for the monitor page's log drawer. The gateway stores whole LLM
// prompts and tool payloads in a trace, so every text field is truncated before
// it reaches the browser.
const (
	agentBuildDetailOutputLimit   = 20000
	agentBuildDetailSpanTextLimit = 6000
	agentBuildDetailMaxSpans      = 400
	// agentBuildDetailTimeout bounds one gateway call from the detail endpoint.
	agentBuildDetailTimeout = 30 * time.Second
)

// buildAgentBuildTaskView renders one ledger row for the API (no credentials,
// truncated output). Shared by List and Detail so both stay in sync.
func buildAgentBuildTaskView(row *types.AgentBuildTask, kbName string, now time.Time) *types.AgentBuildTaskView {
	return &types.AgentBuildTaskView{
		ID:                row.ID,
		KnowledgeBaseID:   row.KnowledgeBaseID,
		KnowledgeBaseName: kbName,
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
		CanRetry:          types.IsRetryableAgentBuildStatus(row.Status),
		CanCancel:         !types.IsTerminalAgentBuildStatus(row.Status),
	}
}

// Detail returns one ledger row plus the gateway task snapshot and execution
// trace for the monitor page's log drawer.
//
// The gateway is consulted server side (WeKnora → gateway), so the browser never
// needs gateway credentials. A missing gateway task, an unreachable gateway or a
// rotated-out trace are reported through Notes instead of failing the request:
// the operator still gets the ledger row they clicked.
func (s *agentBuildTaskService) Detail(
	ctx context.Context, id string, tenantID uint64,
) (*types.AgentBuildTaskDetail, error) {
	row, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	// A member may only read rows of their own workspace (0 = system admin).
	if tenantID != 0 && row.TenantID != tenantID {
		return nil, types.ErrAgentBuildTaskNotFound
	}

	kbName := ""
	if row.KnowledgeBaseID != "" {
		if names, err := s.repo.KnowledgeBaseNames(ctx, []string{row.KnowledgeBaseID}); err == nil {
			kbName = names[row.KnowledgeBaseID]
		}
	}
	detail := &types.AgentBuildTaskDetail{Task: buildAgentBuildTaskView(row, kbName, time.Now())}

	collection := strings.TrimSpace(row.GatewayURL)
	if collection == "" {
		collection = gatewayBaseURL()
	}
	if strings.TrimSpace(row.GatewayTaskID) == "" {
		detail.Notes = append(detail.Notes,
			"该任务尚未提交到网关（无 gateway_task_id），因此没有可回放的执行日志。")
		return detail, nil
	}
	if collection == "" {
		detail.Notes = append(detail.Notes,
			"未配置网关地址（WIKI_AGENT_CALLBACK_URL），无法读取执行日志。")
		return detail, nil
	}

	task, note := s.fetchGatewayTask(ctx, collection, row.GatewayTaskID)
	if task == nil {
		detail.Notes = append(detail.Notes, note)
		return detail, nil
	}
	detail.Gateway = task

	// 网关的 trace 存储按 OpenAI-Agents 的 trace_id 建索引；业务 run id
	// （gateway-run-...）查不到 trace，因此优先用 sdk_trace_id。
	traceKey := strings.TrimSpace(task.SDKTraceID)
	if traceKey == "" {
		traceKey = strings.TrimSpace(task.TraceID)
	}
	if traceKey == "" {
		detail.Notes = append(detail.Notes,
			"网关任务没有 trace_id，暂时没有 trace 日志可看（任务可能仍在排队或该次运行为空）。")
		return detail, nil
	}
	if strings.TrimSpace(task.SDKTraceID) == "" {
		detail.Notes = append(detail.Notes,
			"该任务的 SDK trace id 未回传（网关版本较旧或任务早于本次升级），可能取不到 trace。")
	}
	trace, note := s.fetchGatewayTrace(ctx, collection, traceKey)
	if trace == nil {
		detail.Notes = append(detail.Notes, note)
		return detail, nil
	}
	detail.Trace = trace
	return detail, nil
}

// fetchGatewayTask reads GET <collection>/{task_id} from the gateway.
func (s *agentBuildTaskService) fetchGatewayTask(
	ctx context.Context, collection, taskID string,
) (*types.AgentBuildGatewayTask, string) {
	url := taskStatusURL(collection, taskID)
	if url == "" {
		return nil, "网关任务地址无法推导，未读取执行日志。"
	}
	body, err := s.getGatewayJSON(ctx, url, "任务")
	if err != nil {
		logger.Warnf(ctx, "[agent-build] reading gateway task %s failed: %v", taskID, err)
		return nil, "读取网关任务失败：" + err.Error()
	}
	var out struct {
		TaskID      string `json:"task_id"`
		Status      string `json:"status"`
		AgentName   string `json:"agent_name"`
		RunsMs      int64  `json:"runs_ms"`
		OutputText  string `json:"output_text"`
		ErrorDetail string `json:"error_detail"`
		TraceID     string `json:"trace_id"`
		SDKTraceID  string `json:"sdk_trace_id"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		logger.Warnf(ctx, "[agent-build] decoding gateway task %s failed: %v", taskID, err)
		return nil, "解析网关任务响应失败。"
	}
	return &types.AgentBuildGatewayTask{
		TaskID:      out.TaskID,
		Status:      out.Status,
		AgentName:   out.AgentName,
		RunsMs:      out.RunsMs,
		OutputText:  agentBuildPreview(out.OutputText, agentBuildDetailOutputLimit),
		ErrorDetail: strings.TrimSpace(out.ErrorDetail),
		TraceID:     strings.TrimSpace(out.TraceID),
		SDKTraceID:  strings.TrimSpace(out.SDKTraceID),
	}, ""
}

// fetchGatewayTrace reads GET <gateway>/traces/{trace_id} and flattens the span
// tree into a list the UI can render.
func (s *agentBuildTaskService) fetchGatewayTrace(
	ctx context.Context, collection, traceID string,
) (*types.AgentBuildTrace, string) {
	url := gatewayTraceURL(collection, traceID)
	if url == "" {
		return nil, "网关 trace 地址无法推导，未读取 trace 日志。"
	}
	body, err := s.getGatewayJSON(ctx, url, "trace")
	if err != nil {
		logger.Warnf(ctx, "[agent-build] reading gateway trace %s failed: %v", traceID, err)
		var notFound gatewayNotFoundError
		if errors.As(err, &notFound) {
			return nil, "网关 trace 存储里还没有这条 trace：若任务仍在执行中，trace 会在运行结束后写入，稍后刷新即可。"
		}
		return nil, "读取 trace 失败：" + err.Error()
	}
	var raw struct {
		TraceID string            `json:"trace_id"`
		Name    string            `json:"name"`
		Spans   []json.RawMessage `json:"spans"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		logger.Warnf(ctx, "[agent-build] decoding gateway trace %s failed: %v", traceID, err)
		return nil, "解析 trace 响应失败。"
	}
	trace := &types.AgentBuildTrace{TraceID: raw.TraceID, Name: raw.Name, SpanCount: len(raw.Spans)}
	if trace.TraceID == "" {
		trace.TraceID = traceID
	}
	if len(raw.Spans) == 0 {
		return trace, ""
	}
	limit := len(raw.Spans)
	if limit > agentBuildDetailMaxSpans {
		limit = agentBuildDetailMaxSpans
	}
	spans := make([]*types.AgentBuildTraceSpan, 0, limit)
	for _, rawSpan := range raw.Spans[:limit] {
		if span := flattenGatewaySpan(rawSpan); span != nil {
			spans = append(spans, span)
		}
	}
	trace.Spans = spans
	return trace, ""
}

// getGatewayJSON performs a short-lived GET and returns the response body.
func (s *agentBuildTaskService) getGatewayJSON(ctx context.Context, url, what string) ([]byte, error) {
	reqCtx, cancel := context.WithTimeout(ctx, agentBuildDetailTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, errGatewayStatus(resp.StatusCode, what)
	}
	return body, nil
}

// errGatewayStatus turns a gateway HTTP status into an operator readable note.
func errGatewayStatus(code int, what string) error {
	switch code {
	case http.StatusNotFound:
		return gatewayNotFoundError{what: what}
	case http.StatusTooManyRequests:
		return fmt.Errorf("网关繁忙（HTTP 429），请稍后重试")
	default:
		return fmt.Errorf("网关返回 HTTP %s（读取%s）", strconv.Itoa(code), what)
	}
}

// gatewayNotFoundError marks a 404 from the gateway so callers can tell
// "nothing there (yet)" apart from a transport or server failure.
type gatewayNotFoundError struct {
	what string
}

func (e gatewayNotFoundError) Error() string {
	return fmt.Sprintf("网关已无该%s记录（可能已被清理或轮转）", e.what)
}

// flattenGatewaySpan converts one exported OpenAI-Agents span into the flat
// shape the drawer renders (type / name / duration / truncated payloads).
func flattenGatewaySpan(raw json.RawMessage) *types.AgentBuildTraceSpan {
	var span struct {
		ID        string          `json:"id"`
		ParentID  string          `json:"parent_id"`
		TraceID   string          `json:"trace_id"`
		StartedAt string          `json:"started_at"`
		EndedAt   string          `json:"ended_at"`
		Error     json.RawMessage `json:"error"`
		SpanData  json.RawMessage `json:"span_data"`
	}
	if err := json.Unmarshal(raw, &span); err != nil {
		return nil
	}
	out := &types.AgentBuildTraceSpan{
		ID:        span.ID,
		ParentID:  strings.TrimSpace(span.ParentID),
		StartedAt: span.StartedAt,
		EndedAt:   span.EndedAt,
		Status:    "ok",
		Raw:       raw,
	}
	if !spaceOnly(span.Error) {
		out.Error = agentBuildPreview(strings.TrimSpace(string(span.Error)), 1000)
		out.Status = "error"
	}

	data := map[string]json.RawMessage{}
	if err := json.Unmarshal(span.SpanData, &data); err == nil {
		out.Type = rawJSONString(data["type"])
		out.Name = rawJSONString(data["name"])
		out.Model = rawJSONString(data["model"])
		if out.Name == "" {
			out.Name = out.Type
		}
		out.Summary = gatewaySpanSummary(out.Type, data)
		if v, ok := data["input"]; ok {
			out.Input = agentBuildPreview(rawJSONText(v), agentBuildDetailSpanTextLimit)
		}
		if v, ok := data["output"]; ok {
			out.Output = agentBuildPreview(rawJSONText(v), agentBuildDetailSpanTextLimit)
		}
	}
	if out.Type == "" {
		out.Type = "span"
	}
	if out.Name == "" {
		out.Name = out.Type
	}
	out.Detail = agentBuildPreview(string(raw), agentBuildDetailSpanTextLimit)
	out.DurationMs = spanDurationMs(span.StartedAt, span.EndedAt)
	return out
}

// gatewaySpanSummary produces the one-line description shown next to a span.
func gatewaySpanSummary(kind string, data map[string]json.RawMessage) string {
	switch kind {
	case "generation":
		parts := make([]string, 0, 3)
		if model := rawJSONString(data["model"]); model != "" {
			parts = append(parts, "model="+model)
		}
		if usage := rawJSONText(data["usage"]); usage != "" && usage != "null" {
			parts = append(parts, agentBuildPreview(usage, 160))
		}
		if out := rawJSONText(data["output"]); out != "" && out != "null" {
			parts = append(parts, "output="+agentBuildPreview(out, 200))
		}
		return strings.Join(parts, " · ")
	case "function":
		name := rawJSONString(data["name"])
		args := rawJSONText(data["input"])
		if args != "" && args != "null" {
			return name + "(" + agentBuildPreview(args, 200) + ")"
		}
		return name
	case "mcp_tools", "mcp_tool":
		server := rawJSONString(data["server"])
		return "server=" + server
	case "agent":
		name := rawJSONString(data["name"])
		return "agent=" + name
	default:
		if name := rawJSONString(data["name"]); name != "" {
			return name
		}
		return ""
	}
}

// spanDurationMs derives the span duration from its ISO8601 timestamps.
func spanDurationMs(started, ended string) *int64 {
	if strings.TrimSpace(started) == "" || strings.TrimSpace(ended) == "" {
		return nil
	}
	start, err := time.Parse(time.RFC3339Nano, started)
	if err != nil {
		return nil
	}
	end, err := time.Parse(time.RFC3339Nano, ended)
	if err != nil {
		return nil
	}
	ms := end.Sub(start).Milliseconds()
	if ms < 0 {
		return nil
	}
	return &ms
}

// rawJSONString decodes a JSON string value, returning "" for null/invalid.
func rawJSONString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

// rawJSONText renders any JSON value as text for display.
func rawJSONText(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	return trimmed
}

func spaceOnly(raw json.RawMessage) bool {
	return strings.TrimSpace(string(raw)) == "" || strings.TrimSpace(string(raw)) == "null"
}

// gatewayTraceURL derives the gateway's trace endpoint from the task collection
// endpoint (e.g. http://host:8080/tasks → http://host:8080/traces/{traceID}).
func gatewayTraceURL(collection, traceID string) string {
	base := strings.TrimSpace(collection)
	traceID = strings.TrimSpace(traceID)
	if base == "" || traceID == "" {
		return ""
	}
	base = strings.TrimSuffix(base, "/")
	base = strings.TrimSuffix(base, "/tasks")
	if base == "" {
		return ""
	}
	return base + "/traces/" + traceID
}
