package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// MCP → agent-gateway 同步。
//
// WeKnora 是 MCP 服务配置的唯一事实来源（单租户为主，配置存在 mcp_services
// 表里）；agent-gateway 才是真正执行 agent run 的进程，它只拿到一份派生出来
// 的、覆盖式写入的副本。网关侧契约已经实现并固定，不要再改：
//
//	PUT  {gw}/mcp/servers       全量覆盖下发（幂等）
//	GET  {gw}/mcp/servers       查询网关当前生效配置（密钥已掩码）
//	POST {gw}/mcp/servers/test  从网关视角做连通性测试并回显工具列表
//
// 网关基址统一由 resolveAgentGatewayURL() 解析（与 skill 下发同一套环境变量）。

const (
	// mcpGatewayTokenEnv 是 WeKnora 侧的共享内部令牌环境变量。网关侧同一
	// 个值暴露为 MCP_ADMIN_TOKEN，配置后网关会校验每一个 /mcp/servers 请求；
	// 未配置则网关不校验，此时这里也不发这个头。
	mcpGatewayTokenEnv = "WIKI_AGENT_GATEWAY_TOKEN"
	// mcpGatewayTokenHeader 是承载上述令牌的请求头。
	mcpGatewayTokenHeader = "X-Internal-Token"
	// mcpGatewayRequestTimeout 是每次访问 agent-gateway 的超时上限。
	mcpGatewayRequestTimeout = 10 * time.Second
	// mcpGatewayDefaultAPIKeyHeader 与 MCPAuthConfig.APIKeyHeader 的默认值一致：
	// 该字段为空时 api_key 落到 X-API-Key。
	mcpGatewayDefaultAPIKeyHeader = "X-API-Key"
	// 网关侧支持的传输类型标识（固定契约）。
	mcpGatewayTypeStdio          = "stdio"
	mcpGatewayTypeStreamableHTTP = "streamable_http"
)

// MCPGatewaySkip 记录一个没被下发到 agent-gateway 的服务以及可读原因。
// Warnings 复用同一结构：同样是「没做的事 + 原因」，只是语义上属于提醒而非跳过。
type MCPGatewaySkip struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// MCPGatewaySyncResult 是一次全量覆盖下发的执行结果。
type MCPGatewaySyncResult struct {
	GatewayURL string           `json:"gateway_url"`
	Synced     []string         `json:"synced"`
	Skipped    []MCPGatewaySkip `json:"skipped"`
	Warnings   []MCPGatewaySkip `json:"warnings"`
	Source     string           `json:"source"`
	PushedAt   time.Time        `json:"pushed_at"`
}

// MCPGatewayStatus 把「WeKnora 想让网关持有的名字集合」与「网关实际生效的名字
// 集合」做对比（drift 检测）。网关不可达时 Reachable=false 且 Error 非空，
// 这种情况不算调用失败，前端仍要能展示状态。
type MCPGatewayStatus struct {
	GatewayURL       string   `json:"gateway_url"`
	Reachable        bool     `json:"reachable"`
	Source           string   `json:"source"`
	Count            int      `json:"count"`
	GatewayNames     []string `json:"gateway_names"`
	MissingOnGateway []string `json:"missing_on_gateway"`
	ExtraOnGateway   []string `json:"extra_on_gateway"`
	Error            string   `json:"error"`
}

// mcpGatewayServer 是 agent-gateway 侧 /mcp/servers 接收的单项配置结构。
// 字段在两个方向上共用：PUT 下发与 POST /mcp/servers/test 的表单回显。
type mcpGatewayServer struct {
	Type    string            `json:"type"`
	Name    string            `json:"name"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// MCPGatewaySyncService 负责把 MCP 服务配置下发给 agent-gateway，
// 以及查询网关当前状态 / 借网关之手测试连通性。
type MCPGatewaySyncService struct {
	mcpServiceRepo interfaces.MCPServiceRepository
}

// NewMCPGatewaySyncService creates a new MCP gateway sync service.
func NewMCPGatewaySyncService(mcpServiceRepo interfaces.MCPServiceRepository) *MCPGatewaySyncService {
	return &MCPGatewaySyncService{
		mcpServiceRepo: mcpServiceRepo,
	}
}

// Sync 把当前空间（含对所有租户可见的内建服务）的 MCP 配置全量覆盖式下发到
// agent-gateway。幂等：网关侧以 name 为键整体替换，Enabled=false 的服务不在
// 请求体里，因此也会同时从网关配置中剔除。
func (s *MCPGatewaySyncService) Sync(ctx context.Context, tenantID uint64) (*MCPGatewaySyncResult, error) {
	gwURL := resolveAgentGatewayURL()
	if gwURL == "" {
		return nil, fmt.Errorf("WIKI_AGENT_GATEWAY_URL/WIKI_AGENT_CALLBACK_URL 未配置，无法同步 MCP 服务到 agent-gateway")
	}

	services, err := s.mcpServiceRepo.List(ctx, tenantID)
	if err != nil {
		logger.Errorf(ctx, "[mcp-gateway] 读取 MCP 服务失败: %v", err)
		return nil, fmt.Errorf("failed to list MCP services: %w", err)
	}

	result := &MCPGatewaySyncResult{
		GatewayURL: gwURL,
		Synced:     []string{},
		Skipped:    []MCPGatewaySkip{},
		Warnings:   []MCPGatewaySkip{},
	}

	// servers 始终是非 nil 切片：网关会对 servers 逐项校验，JSON null 会被拒。
	servers := make([]mcpGatewayServer, 0, len(services))
	// name -> WeKnora 服务 ID。网关侧配置是以 name 为键的一份 map，即 name 全局
	// 唯一；跨租户/内建服务重名必须在这里挡下来，否则网关对整单请求返回 400，
	// 一个坏项会连累全部服务。
	seenNames := make(map[string]string, len(services))

	for _, svc := range services {
		if svc == nil {
			continue
		}
		name := strings.TrimSpace(svc.Name)

		// 只同步启用中的服务；停用的服务既不下发，也从网关配置里消失。
		if !svc.Enabled {
			result.Skipped = append(result.Skipped, MCPGatewaySkip{Name: name, Reason: "未启用"})
			continue
		}

		server, warnings, skipReason := mapMCPServiceToGatewayServer(svc)
		result.Warnings = append(result.Warnings, warnings...)
		if skipReason != "" {
			logger.Warnf(ctx, "[mcp-gateway] 跳过 MCP 服务 %s: %s", name, skipReason)
			result.Skipped = append(result.Skipped, MCPGatewaySkip{Name: name, Reason: skipReason})
			continue
		}

		if prevID, ok := seenNames[server.Name]; ok {
			logger.Warnf(ctx, "[mcp-gateway] MCP 服务名冲突: %s (id=%s / id=%s)", server.Name, prevID, svc.ID)
			return nil, fmt.Errorf(
				"MCP 服务名冲突（网关侧 name 全局唯一）: %q 同时被服务 %s 与 %s 使用，请先重命名",
				server.Name, prevID, svc.ID)
		}
		seenNames[server.Name] = svc.ID
		servers = append(servers, *server)
		result.Synced = append(result.Synced, server.Name)
	}

	body, err := json.Marshal(map[string][]mcpGatewayServer{"servers": servers})
	if err != nil {
		return nil, fmt.Errorf("failed to encode MCP gateway payload: %w", err)
	}

	req, err := newMCPGatewayRequest(ctx, http.MethodPut, gwURL+"/mcp/servers", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to build MCP gateway request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := mcpGatewayHTTPClient().Do(req)
	if err != nil {
		logger.Warnf(ctx, "[mcp-gateway] 下发失败: %v", err)
		return nil, fmt.Errorf("请求 agent-gateway 失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取 agent-gateway 响应失败: %w", err)
	}
	if resp.StatusCode >= 300 {
		detail := mcpGatewayErrorDetail(respBody)
		logger.Warnf(ctx, "[mcp-gateway] 下发被拒 (%d): %s", resp.StatusCode, detail)
		return nil, fmt.Errorf("agent-gateway 返回 %d: %s", resp.StatusCode, detail)
	}

	// source 表示网关这一次实际生效的配置来自哪个文件（managed=托管文件，
	// seed=种子文件）；解析失败不影响下发已经成功这个事实。
	var parsed struct {
		Reload struct {
			Source string   `json:"source"`
			Count  int      `json:"count"`
			Names  []string `json:"names"`
		} `json:"reload"`
	}
	if err := json.Unmarshal(respBody, &parsed); err == nil {
		result.Source = parsed.Reload.Source
	}
	result.PushedAt = time.Now()

	logger.Infof(ctx,
		"[mcp-gateway] 同步完成: url=%s synced=%d skipped=%d warnings=%d source=%s",
		gwURL, len(result.Synced), len(result.Skipped), len(result.Warnings), result.Source)
	return result, nil
}

// Status 对比 WeKnora 期望下发的名字集合与网关实际生效的集合。
// 网关 URL 未配置属于配置错误（返回 error）；网关不可达 / 返回错误码则写进
// MCPGatewayStatus（Reachable=false + Error），因为「网关挂了」正是这个接口
// 要汇报的状态之一。
func (s *MCPGatewaySyncService) Status(ctx context.Context, tenantID uint64) (*MCPGatewayStatus, error) {
	gwURL := resolveAgentGatewayURL()
	if gwURL == "" {
		return nil, fmt.Errorf("WIKI_AGENT_GATEWAY_URL/WIKI_AGENT_CALLBACK_URL 未配置，无法查询 agent-gateway MCP 配置")
	}

	status := &MCPGatewayStatus{
		GatewayURL:       gwURL,
		GatewayNames:     []string{},
		MissingOnGateway: []string{},
		ExtraOnGateway:   []string{},
	}

	// 期望集合必须与 Sync 用完全相同的映射规则，否则 drift 会误报。
	desired, err := s.desiredGatewayNames(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	req, err := newMCPGatewayRequest(ctx, http.MethodGet, gwURL+"/mcp/servers", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build MCP gateway request: %w", err)
	}

	resp, err := mcpGatewayHTTPClient().Do(req)
	if err != nil {
		logger.Warnf(ctx, "[mcp-gateway] 查询网关配置失败: %v", err)
		status.Error = err.Error()
		return status, nil
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		status.Error = "读取 agent-gateway 响应失败: " + err.Error()
		return status, nil
	}
	if resp.StatusCode >= 300 {
		status.Error = fmt.Sprintf("agent-gateway 返回 %d: %s", resp.StatusCode, mcpGatewayErrorDetail(respBody))
		logger.Warnf(ctx, "[mcp-gateway] 查询网关配置失败: %s", status.Error)
		return status, nil
	}

	var payload struct {
		Data   []map[string]any `json:"data"`
		Source string           `json:"source"`
		Count  int              `json:"count"`
	}
	if err := json.Unmarshal(respBody, &payload); err != nil {
		status.Error = "解析 agent-gateway 响应失败: " + err.Error()
		return status, nil
	}

	status.Reachable = true
	status.Source = payload.Source

	gatewayNames := make([]string, 0, len(payload.Data))
	for _, item := range payload.Data {
		if name, ok := item["name"].(string); ok && name != "" {
			gatewayNames = append(gatewayNames, name)
		}
	}
	slices.Sort(gatewayNames)
	status.GatewayNames = gatewayNames
	status.Count = len(gatewayNames)

	desiredSet := make(map[string]struct{}, len(desired))
	for _, name := range desired {
		desiredSet[name] = struct{}{}
	}
	gatewaySet := make(map[string]struct{}, len(gatewayNames))
	for _, name := range gatewayNames {
		gatewaySet[name] = struct{}{}
	}
	for _, name := range desired {
		if _, ok := gatewaySet[name]; !ok {
			status.MissingOnGateway = append(status.MissingOnGateway, name)
		}
	}
	for _, name := range gatewayNames {
		if _, ok := desiredSet[name]; !ok {
			status.ExtraOnGateway = append(status.ExtraOnGateway, name)
		}
	}
	slices.Sort(status.MissingOnGateway)
	slices.Sort(status.ExtraOnGateway)

	logger.Infof(ctx,
		"[mcp-gateway] 状态查询完成: reachable=%v count=%d missing=%d extra=%d",
		status.Reachable, status.Count, len(status.MissingOnGateway), len(status.ExtraOnGateway))
	return status, nil
}

// TestServer 用指定 MCP 服务的当前配置，让 agent-gateway 从它自己的网络视角
// 做一次连通性测试（不落盘、不修改网关配置），原样返回网关的 JSON。
// 网关的约定是：探活失败也是 200 + {"ok":false,...,"error":"..."}，只有配置
// 非法才 400，所以这里不翻译状态码，直接把网关响应交给上层。
func (s *MCPGatewaySyncService) TestServer(ctx context.Context, tenantID uint64, id string) (map[string]any, error) {
	gwURL := resolveAgentGatewayURL()
	if gwURL == "" {
		return nil, fmt.Errorf("WIKI_AGENT_GATEWAY_URL/WIKI_AGENT_CALLBACK_URL 未配置，无法通过 agent-gateway 测试")
	}

	svc, err := s.mcpServiceRepo.GetByID(ctx, tenantID, id)
	if err != nil {
		logger.Errorf(ctx, "[mcp-gateway] 读取 MCP 服务失败: %v", err)
		return nil, fmt.Errorf("failed to get MCP service: %w", err)
	}
	if svc == nil {
		return nil, fmt.Errorf("MCP service not found")
	}

	server, warnings, skipReason := mapMCPServiceToGatewayServer(svc)
	if skipReason != "" {
		return nil, fmt.Errorf("无法通过 agent-gateway 测试该 MCP 服务: %s", skipReason)
	}
	for _, warning := range warnings {
		logger.Warnf(ctx, "[mcp-gateway] %s: %s", warning.Name, warning.Reason)
	}

	body, err := json.Marshal(map[string]mcpGatewayServer{"server": *server})
	if err != nil {
		return nil, fmt.Errorf("failed to encode MCP gateway payload: %w", err)
	}

	req, err := newMCPGatewayRequest(ctx, http.MethodPost, gwURL+"/mcp/servers/test", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to build MCP gateway request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := mcpGatewayHTTPClient().Do(req)
	if err != nil {
		logger.Warnf(ctx, "[mcp-gateway] 测试请求失败: %v", err)
		return nil, fmt.Errorf("请求 agent-gateway 失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取 agent-gateway 响应失败: %w", err)
	}
	if resp.StatusCode >= 300 {
		detail := mcpGatewayErrorDetail(respBody)
		logger.Warnf(ctx, "[mcp-gateway] 测试请求被拒 (%d): %s", resp.StatusCode, detail)
		return nil, fmt.Errorf("agent-gateway 返回 %d: %s", resp.StatusCode, detail)
	}

	var out map[string]any
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("解析 agent-gateway 响应失败: %w", err)
	}
	return out, nil
}

// desiredGatewayNames 返回 Sync 会下发的名字集合：启用中的、且配置能被网关
// 表达的服务（与 Sync 共用同一个映射函数，保证 drift 判断和真实同步结果一致）。
func (s *MCPGatewaySyncService) desiredGatewayNames(ctx context.Context, tenantID uint64) ([]string, error) {
	services, err := s.mcpServiceRepo.List(ctx, tenantID)
	if err != nil {
		logger.Errorf(ctx, "[mcp-gateway] 读取 MCP 服务失败: %v", err)
		return nil, fmt.Errorf("failed to list MCP services: %w", err)
	}

	names := make([]string, 0, len(services))
	seen := make(map[string]struct{}, len(services))
	for _, svc := range services {
		if svc == nil || !svc.Enabled {
			continue
		}
		server, _, skipReason := mapMCPServiceToGatewayServer(svc)
		if skipReason != "" || server == nil {
			continue
		}
		if _, ok := seen[server.Name]; ok {
			continue
		}
		seen[server.Name] = struct{}{}
		names = append(names, server.Name)
	}
	slices.Sort(names)
	return names, nil
}

// mapMCPServiceToGatewayServer 把 WeKnora 的 MCP 服务实体转换成 agent-gateway
// 的 wire 格式。返回 (nil, nil, reason) 表示这项无法下发，reason 会原样出现在
// skipped 里 —— 不允许下发非法项，因为网关会对整单请求返回 400。
//
// 映射规则（网关契约，勿改）：
//   - stdio            → {"type":"stdio","name":Name,"command":Command,"args":Args,"env":EnvVars}
//     （env 为空则省略该键）
//   - http-streamable  → {"type":"streamable_http","name":Name,"url":URL}
//   - sse              → 跳过：网关仅支持 stdio / streamable_http
//   - 其他/未知 transport_type → 跳过
//   - URL 为空 / stdio 缺 command → 跳过并记 reason
//   - 请求头合并顺序：Headers → AuthConfig.CustomHeaders → AuthConfig.APIKey
//     落到 AuthConfig.APIKeyHeader（默认 X-API-Key）→ AuthConfig.Token 落到
//     `Authorization: Bearer <token>`
//   - AuthType=oauth   → 不下发 api_key / token 鉴权头（OAuth 是按用户授权的，
//     网关侧无法复用），记入 warnings；管理员显式配置的静态 Headers /
//     CustomHeaders 仍然下发，因为它们是配置而非令牌
func mapMCPServiceToGatewayServer(svc *types.MCPService) (*mcpGatewayServer, []MCPGatewaySkip, string) {
	if svc == nil {
		return nil, nil, "服务配置为空"
	}
	name := strings.TrimSpace(svc.Name)
	if name == "" {
		return nil, nil, "服务名为空"
	}

	switch svc.TransportType {
	case types.MCPTransportStdio:
		command := ""
		var args []string
		if svc.StdioConfig != nil {
			command = strings.TrimSpace(svc.StdioConfig.Command)
			args = svc.StdioConfig.Args
		}
		if command == "" {
			return nil, nil, "stdio 缺少 command"
		}
		server := &mcpGatewayServer{
			Type:    mcpGatewayTypeStdio,
			Name:    name,
			Command: command,
			Args:    args,
		}
		// env 为空时省略该键（网关契约要求）。
		if len(svc.EnvVars) > 0 {
			env := make(map[string]string, len(svc.EnvVars))
			for k, v := range svc.EnvVars {
				env[k] = v
			}
			server.Env = env
		}
		// stdio 没有 HTTP 头，Headers/AuthConfig 一概不下发。
		return server, nil, ""

	case types.MCPTransportHTTPStreamable:
		if svc.URL == nil || strings.TrimSpace(*svc.URL) == "" {
			return nil, nil, "http-streamable 缺少 URL"
		}
		server := &mcpGatewayServer{
			Type: mcpGatewayTypeStreamableHTTP,
			Name: name,
			URL:  strings.TrimSpace(*svc.URL),
		}
		headers, warnings := buildGatewayHeaders(svc)
		if len(headers) > 0 {
			server.Headers = headers
		}
		return server, warnings, ""

	case types.MCPTransportSSE:
		return nil, nil, "网关仅支持 stdio / streamable_http（sse 暂不支持）"

	default:
		return nil, nil, fmt.Sprintf("不支持的 transport_type: %s", svc.TransportType)
	}
}

// buildGatewayHeaders 按固定顺序合并出网关侧要用的请求头：
// Headers → AuthConfig.CustomHeaders → AuthConfig.APIKey → AuthConfig.Token。
// 后者覆盖前者（越靠后越具体）。
func buildGatewayHeaders(svc *types.MCPService) (map[string]string, []MCPGatewaySkip) {
	headers := make(map[string]string)
	for k, v := range svc.Headers {
		headers[k] = v
	}

	auth := svc.AuthConfig
	if auth == nil {
		return headers, nil
	}

	var warnings []MCPGatewaySkip
	// OAuth 的 token 是 per (tenant, user, service) 的，网关侧没有用户上下文，
	// 复用不了；因此不下发 api_key / token 鉴权头，只提示管理员。
	if auth.IsOAuth() {
		warnings = append(warnings, MCPGatewaySkip{
			Name:   strings.TrimSpace(svc.Name),
			Reason: "OAuth 为按用户授权，网关侧无法复用该 token（未下发鉴权头）",
		})
		for k, v := range auth.CustomHeaders {
			headers[k] = v
		}
		if len(headers) == 0 {
			return nil, warnings
		}
		return headers, warnings
	}

	for k, v := range auth.CustomHeaders {
		headers[k] = v
	}
	if auth.APIKey != "" {
		header := strings.TrimSpace(auth.APIKeyHeader)
		if header == "" {
			header = mcpGatewayDefaultAPIKeyHeader
		}
		headers[header] = auth.APIKey
	}
	if auth.Token != "" {
		headers["Authorization"] = "Bearer " + auth.Token
	}
	if len(headers) == 0 {
		return nil, warnings
	}
	return headers, warnings
}

// newMCPGatewayRequest 构造指向 agent-gateway 的请求并带上内部令牌。
// 令牌未配置时不带该头（网关侧同样未配置时不做校验）。
func newMCPGatewayRequest(ctx context.Context, method, url string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	if token := os.Getenv(mcpGatewayTokenEnv); token != "" {
		req.Header.Set(mcpGatewayTokenHeader, token)
	}
	return req, nil
}

// mcpGatewayHTTPClient 返回一个带超时的客户端（默认客户端没有超时，会把请求
// 挂到 TCP 超时为止）。
func mcpGatewayHTTPClient() *http.Client {
	return &http.Client{Timeout: mcpGatewayRequestTimeout}
}

// mcpGatewayErrorDetail 从网关的错误响应里取出可读信息：优先 {"detail":"..."}，
// 否则退化为原始 body（截断），再退化为占位文案。
func mcpGatewayErrorDetail(respBody []byte) string {
	var payload struct {
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(respBody, &payload); err == nil && payload.Detail != "" {
		return payload.Detail
	}
	text := strings.TrimSpace(string(respBody))
	if text == "" {
		return "(empty response body)"
	}
	if len(text) > 512 {
		text = text[:512] + "..."
	}
	return text
}
