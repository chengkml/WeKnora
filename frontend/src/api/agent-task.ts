import { get, post } from '@/utils/request'

// ---------------------------------------------------------------------------
// WeKnora → AgentGateway「wiki 构建」任务监控（私有化定制）
//
// 后端契约（读接口对本工作区成员开放、按租户限域；retry/cancel 需工作区管理员）：
//   GET  /api/v1/agent-tasks            → { items, total, page, page_size }
//   GET  /api/v1/agent-tasks/summary    → { total, running, queued, failed_today,
//                                                       succeeded_today, concurrency,
//                                                       oldest_queued_seconds, gateway_configured, gateway_url }
//   POST /api/v1/agent-tasks/{id}/retry  → { success: true }
//   POST /api/v1/agent-tasks/{id}/cancel → { success: true }
//
// 响应取值方式与 api/mcp-gateway.ts 保持一致：utils/request.ts 的响应拦截器已经把
// 整个 HTTP body 作为 resolve 值返回，故这里用 response.data ?? response 兜底读出业务数据。
// ---------------------------------------------------------------------------

/** 任务状态：排队中 / 执行中 / 成功 / 失败 / 已取消 */
export type AgentTaskStatus = 'queued' | 'running' | 'succeeded' | 'failed' | 'cancelled'

/** 列表筛选：'' 表示不限状态 */
export type AgentTaskStatusFilter = AgentTaskStatus | ''

/** GET /api/v1/agent-tasks 列表项（后端 JSON 为 snake_case） */
export interface AgentTaskItem {
  id: string
  knowledge_base_id: string
  knowledge_base_name: string
  knowledge_id: string
  doc_name: string
  skill: string
  status: AgentTaskStatus | string
  attempts: number
  max_attempts: number
  last_error: string
  gateway_task_id: string
  output_preview: string
  duration_ms: number | null
  can_retry: boolean
  can_cancel: boolean
  wait_seconds: number
  run_seconds: number
  /** 以下时间字段均为 ISO8601，可能为空串或 null */
  queued_at: string | null
  submitted_at: string | null
  started_at: string | null
  finished_at: string | null
}

/** 列表查询参数 */
export interface AgentTaskListParams {
  status?: AgentTaskStatusFilter | string
  knowledge_base_id?: string
  keyword?: string
  page?: number
  page_size?: number
}

/** GET /api/v1/agent-tasks 的业务数据 */
export interface AgentTaskListResult {
  items: AgentTaskItem[]
  total: number
  page: number
  page_size: number
}

/** GET /api/v1/agent-tasks/summary 的业务数据 */
export interface AgentTaskSummary {
  /** 全部台账行数（含已完成 / 已取消） */
  total: number
  running: number
  queued: number
  failed_today: number
  succeeded_today: number
  /** 投递并发上限：WeKnora 同时交给网关的最大任务数 */
  concurrency: number
  /** 队首任务已排队秒数 */
  oldest_queued_seconds: number
  /** 是否已配置网关回调地址（WIKI_AGENT_CALLBACK_URL） */
  gateway_configured: boolean
  gateway_url: string
}

/** retry / cancel 的业务数据 */
export interface AgentTaskActionResponse {
  success: boolean
}

/** 去掉空值，避免把 status='' / keyword='' 作为过滤条件传给后端 */
function buildQuery(params: AgentTaskListParams): Record<string, string | number> {
  const query: Record<string, string | number> = {}
  if (params.status) query.status = params.status
  if (params.knowledge_base_id) query.knowledge_base_id = params.knowledge_base_id
  if (params.keyword) query.keyword = params.keyword
  if (params.page) query.page = params.page
  if (params.page_size) query.page_size = params.page_size
  return query
}

/** 分页查询任务列表（支持按状态 / 知识库 / 关键词 doc_name 过滤）。 */
export async function listAgentTasks(params: AgentTaskListParams = {}): Promise<AgentTaskListResult> {
  const response: any = await get('/api/v1/agent-tasks', { params: buildQuery(params) })
  return (response?.data ?? response) as AgentTaskListResult
}

/** 顶部统计卡片（运行中 / 排队中 / 今日失败 / 今日成功 / 总数）。 */
export async function getAgentTaskSummary(): Promise<AgentTaskSummary> {
  const response: any = await get('/api/v1/agent-tasks/summary')
  return (response?.data ?? response) as AgentTaskSummary
}

/** 重试失败或已取消的任务。 */
export async function retryAgentTask(id: string): Promise<AgentTaskActionResponse> {
  const response: any = await post(`/api/v1/agent-tasks/${encodeURIComponent(id)}/retry`, {})
  return (response?.data ?? response) as AgentTaskActionResponse
}

/** 取消排队中或执行中的任务。 */
export async function cancelAgentTask(id: string): Promise<AgentTaskActionResponse> {
  const response: any = await post(`/api/v1/agent-tasks/${encodeURIComponent(id)}/cancel`, {})
  return (response?.data ?? response) as AgentTaskActionResponse
}

// ---------------------------------------------------------------------------
// 任务执行日志（详情抽屉）
//
//   GET /api/v1/agent-tasks/{id}/detail
//     → { task, gateway?, trace?, notes? }
//
// 网关侧的 task/trace 由 WeKnora 服务端代理读取（浏览器不需要网关凭据）；
// gateway / trace 字段可能缺失，缺失原因写在 notes 里。
// ---------------------------------------------------------------------------

/** 一条 trace span（后端已按类型拍平并把 input/output/detail 截断） */
export interface AgentTaskTraceSpan {
  id: string
  parent_id?: string
  type: string
  name?: string
  started_at?: string
  ended_at?: string
  duration_ms: number | null
  /** ok | error */
  status: string
  error?: string
  model?: string
  summary?: string
  input?: string
  output?: string
  detail?: string
}

/** 网关任务快照（GET <gateway>/tasks/{task_id}） */
export interface AgentTaskGatewayInfo {
  task_id: string
  status: string
  agent_name: string
  runs_ms: number
  output_text: string
  error_detail: string
  trace_id: string
}

/** 一次 agent run 的完整 trace */
export interface AgentTaskTrace {
  trace_id: string
  name: string
  span_count: number
  spans: AgentTaskTraceSpan[]
}

/** GET /api/v1/agent-tasks/{id}/detail 的业务数据 */
export interface AgentTaskDetail {
  task: AgentTaskItem
  gateway?: AgentTaskGatewayInfo
  trace?: AgentTaskTrace
  /** 缺失项说明（未提交网关 / 网关不可达 / trace 已轮转等） */
  notes?: string[]
}

/** 读取单个任务的执行日志（网关任务快照 + trace span 列表）。 */
export async function getAgentTaskDetail(id: string): Promise<AgentTaskDetail> {
  const response: any = await get(`/api/v1/agent-tasks/${encodeURIComponent(id)}/detail`)
  return (response?.data ?? response) as AgentTaskDetail
}
