package service

import (
	"context"
	"os"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// fakeMCPServiceRepo 只实现本测试用到的读方法，其余方法由嵌入的（nil）接口占位。
type fakeMCPServiceRepo struct {
	interfaces.MCPServiceRepository
	services []*types.MCPService
}

func (f *fakeMCPServiceRepo) List(_ context.Context, _ uint64) ([]*types.MCPService, error) {
	return f.services, nil
}

func (f *fakeMCPServiceRepo) GetByID(_ context.Context, _ uint64, id string) (*types.MCPService, error) {
	for _, svc := range f.services {
		if svc != nil && svc.ID == id {
			return svc, nil
		}
	}
	return nil, nil
}

// TestMCPGatewayLiveSync 是对真实 agent-gateway 的可选集成测试，默认跳过。
//
// 启用方式：
//
//	MCP_GATEWAY_LIVE_URL=http://127.0.0.1:8080 \
//	WIKI_AGENT_GATEWAY_TOKEN=<网关 MCP_ADMIN_TOKEN 同值> \
//	MCP_LIVE_API_KEY=<被测 MCP server 的密钥> \
//	go test ./internal/application/service/ -run TestMCPGatewayLiveSync -v
//
// 注意：Sync 是全量覆盖语义，会改写网关当前的 MCP 配置，跑完需要恢复。
func TestMCPGatewayLiveSync(t *testing.T) {
	gw := os.Getenv("MCP_GATEWAY_LIVE_URL")
	if gw == "" {
		t.Skip("MCP_GATEWAY_LIVE_URL 未设置，跳过 live 同步测试")
	}
	if os.Getenv("WIKI_AGENT_GATEWAY_TOKEN") == "" {
		t.Fatal("缺少 WIKI_AGENT_GATEWAY_TOKEN")
	}
	t.Setenv("WIKI_AGENT_GATEWAY_URL", gw)

	mcpURL := "http://10.1.215.50:8001/mcp/"
	svc := &types.MCPService{
		ID:            "go-live-test",
		Name:          "weknora-golive",
		Enabled:       true,
		TransportType: types.MCPTransportHTTPStreamable,
		URL:           &mcpURL,
		AuthConfig: &types.MCPAuthConfig{
			AuthType:     types.MCPAuthAPIKey,
			APIKey:       os.Getenv("MCP_LIVE_API_KEY"),
			APIKeyHeader: "X-API-Key",
		},
	}

	// 1) 空配置保护：空间内没有启用中的服务时，必须被拦截而不是清空网关
	blocked := NewMCPGatewaySyncService(&fakeMCPServiceRepo{})
	if _, err := blocked.Sync(context.Background(), 1, false); err == nil {
		t.Fatal("期望空配置同步被拦截，实际却成功了")
	} else {
		t.Logf("空配置保护 OK: %v", err)
	}

	live := NewMCPGatewaySyncService(&fakeMCPServiceRepo{services: []*types.MCPService{svc}})

	// 2) 真实全量下发
	res, err := live.Sync(context.Background(), 1, false)
	if err != nil {
		t.Fatalf("下发失败: %v", err)
	}
	t.Logf("下发结果: gateway=%s synced=%v skipped=%v warnings=%v",
		res.GatewayURL, res.Synced, res.Skipped, res.Warnings)
	if len(res.Synced) != 1 || res.Synced[0] != "weknora-golive" {
		t.Fatalf("下发名单不符合预期: %v", res.Synced)
	}

	// 3) drift 状态
	st, err := live.Status(context.Background(), 1)
	if err != nil {
		t.Fatalf("状态查询失败: %v", err)
	}
	t.Logf("网关状态: reachable=%v source=%s count=%d names=%v missing=%v extra=%v err=%q",
		st.Reachable, st.Source, st.Count, st.GatewayNames, st.MissingOnGateway, st.ExtraOnGateway, st.Error)
	if !st.Reachable || len(st.MissingOnGateway) != 0 {
		t.Fatalf("drift 不符合预期: %+v", st)
	}

	// 4) 借网关视角测连通性并回显工具列表
	out, err := live.TestServer(context.Background(), 1, "go-live-test")
	if err != nil {
		t.Fatalf("网关侧测试失败: %v", err)
	}
	ok, _ := out["ok"].(bool)
	tools, _ := out["tools"].([]any)
	t.Logf("网关侧测试: ok=%v tool_count=%v elapsed_ms=%v", ok, out["tool_count"], out["elapsed_ms"])
	for i, item := range tools {
		if i >= 3 {
			break
		}
		if m, isMap := item.(map[string]any); isMap {
			t.Logf("  工具样例[%d]: %v", i, m["name"])
		}
	}
	if !ok || len(tools) == 0 {
		t.Fatalf("网关侧未返回工具列表: %+v", out)
	}
}
