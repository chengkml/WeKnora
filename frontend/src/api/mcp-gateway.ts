import { get, post } from '@/utils/request'

// ---------------------------------------------------------------------------
// MCP → agent-gateway 同步（私有化定制）
//
// WeKnora 是 MCP 配置的唯一事实源（source of truth），这里只把一份派生副本
// 全量覆盖下发到真正执行 agent 的 agent-gateway。
//
// 后端契约（internal/handler/mcp_gateway.go + internal/application/service/mcp_gateway_sync.go）：
//   POST /api/v1/mcp-gateway/sync                  → {success:true, data:MCPGatewaySyncResult}
//   GET  /api/v1/mcp-gateway/status                 → {success:true, data:MCPGatewayStatusResult}
//   POST /api/v1/mcp-gateway/servers/:id/test       → {success:true, data:MCPGatewayTestResult}
//
// 响应取值方式与 api/mcp-service.ts 保持一致：utils/request.ts 的响应拦截器
// 已经把整个 HTTP body 作为 resolve 值返回，所以这里拿到的是
// { success, data, msg }；统一用 response.data ?? response 读出业务数据。
// ---------------------------------------------------------------------------

/** 网关侧跳过 / 警告的单项说明 */
export interface MCPGatewaySkipItem {
  name: string
  reason: string
}

/** POST /api/v1/mcp-gateway/sync 的 data */
export interface MCPGatewaySyncResult {
  gateway_url: string
  /** 已成功下发到网关的服务名 */
  synced: string[]
  /** 被跳过的服务及原因（未启用 / SSE 不支持 / 配置缺失等） */
  skipped: MCPGatewaySkipItem[]
  /** 下发成功但需注意的服务（如 OAuth 无法复用 token） */
  warnings: MCPGatewaySkipItem[]
  /** 网关侧配置文件来源：managed（托管文件）| seed（种子文件） */
  source: string
  /** 本次下发时间 */
  pushed_at: string
}

/** GET /api/v1/mcp-gateway/status 的 data */
export interface MCPGatewayStatusResult {
  gateway_url: string
  reachable: boolean
  /** 网关侧配置文件来源：managed（托管文件）| seed（种子文件） */
  source: string
  /** 网关当前生效的服务数量 */
  count: number
  /** 网关侧的服务名列表 */
  gateway_names: string[]
  /** WeKnora 已启用但网关缺少（drift） */
  missing_on_gateway: string[]
  /** 网关多出、WeKnora 侧已删除或停用（drift） */
  extra_on_gateway: string[]
  /** 网关不可达等原因 */
  error?: string
}

/** 网关视角的工具描述 */
export interface MCPGatewayToolInfo {
  name: string
  description?: string
  /** 入参名列表（网关侧只回显参数名，不回显 schema） */
  params?: string[]
}

/** POST /api/v1/mcp-gateway/servers/:id/test 的 data */
export interface MCPGatewayTestResult {
  ok: boolean
  name?: string
  /** 网关侧传输类型，如 streamable_http / stdio */
  transport?: string
  tool_count?: number
  tools?: MCPGatewayToolInfo[]
  error?: string | null
  elapsed_ms?: number
}

/**
 * 全量下发当前空间的 MCP 服务到 agent-gateway（幂等、覆盖式）。
 * 仅在网关不可达 / 配置非法时抛错；被跳过的服务通过 skipped/warnings 返回。
 * force=true 才允许下发空配置（否则后端会以 400 拦截，避免误清空网关配置）。
 */
export async function syncMCPGateway(force = false): Promise<MCPGatewaySyncResult> {
  const response: any = await post(`/api/v1/mcp-gateway/sync${force ? '?force=true' : ''}`, {})
  return (response?.data ?? response) as MCPGatewaySyncResult
}

/** 查询 agent-gateway 当前生效的 MCP 配置，并与 WeKnora 侧对比出 drift。 */
export async function getMCPGatewayStatus(): Promise<MCPGatewayStatusResult> {
  const response: any = await get('/api/v1/mcp-gateway/status')
  return (response?.data ?? response) as MCPGatewayStatusResult
}

/**
 * 以 agent-gateway 的视角测试某个已配置服务的连通性，并回显网关侧真实工具列表。
 * 探测失败也返回 200 + ok=false（error 里带原因），不抛错。
 */
export async function testMCPGatewayServer(serviceId: string): Promise<MCPGatewayTestResult> {
  const response: any = await post(`/api/v1/mcp-gateway/servers/${encodeURIComponent(serviceId)}/test`, {})
  return (response?.data ?? response) as MCPGatewayTestResult
}
