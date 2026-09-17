<template>
  <div class="agent-tasks">
    <header class="at-header">
      <div class="at-title-block">
        <h2>{{ t('agentTasks.title') }}</h2>
        <p class="at-subtitle">{{ t('agentTasks.subtitle') }}</p>
      </div>
      <div class="at-header-actions">
        <label class="at-auto-refresh">
          <span class="at-live-dot" :class="{ 'at-live-dot--active': autoRefresh }" />
          <span>{{ t('agentTasks.autoRefresh') }}</span>
          <t-switch
            v-model="autoRefresh"
            size="small"
            :aria-label="t('agentTasks.autoRefresh')"
          />
        </label>
        <t-button variant="outline" size="medium" :loading="loading" @click="reload">
          <template #icon><t-icon name="refresh" /></template>
          {{ t('agentTasks.refresh') }}
        </t-button>
      </div>
    </header>

    <!-- 顶部统计：GET /api/v1/system/admin/agent-tasks/summary -->
    <section class="at-overview" :aria-label="t('agentTasks.cardsTitle')">
      <div class="at-metric">
        <span class="at-metric-label">{{ t('agentTasks.cards.running') }}</span>
        <strong class="at-metric-value">{{ summary.running }}</strong>
      </div>
      <div class="at-metric">
        <span class="at-metric-label">{{ t('agentTasks.cards.queued') }}</span>
        <strong class="at-metric-value">{{ summary.queued }}</strong>
      </div>
      <div class="at-metric" :class="{ 'at-metric--danger': summary.failed_today > 0 }">
        <span class="at-metric-label">{{ t('agentTasks.cards.failedToday') }}</span>
        <strong class="at-metric-value">{{ summary.failed_today }}</strong>
      </div>
      <div class="at-metric" :class="{ 'at-metric--success': summary.succeeded_today > 0 }">
        <span class="at-metric-label">{{ t('agentTasks.cards.succeededToday') }}</span>
        <strong class="at-metric-value">{{ summary.succeeded_today }}</strong>
      </div>
    </section>

    <!-- 工具栏：状态筛选 / doc_name 关键词 / 总数 -->
    <div class="at-toolbar">
      <t-select
        v-model="statusFilter"
        class="at-status-select"
        :options="statusOptions"
        :placeholder="t('agentTasks.filterStatus')"
        @change="handleFilterChange"
      />
      <t-input
        v-model="keyword"
        class="at-keyword-input"
        :placeholder="t('agentTasks.filterKeywordPlaceholder')"
        clearable
        @enter="handleFilterChange"
        @clear="handleFilterChange"
        @blur="handleFilterChange"
      >
        <template #prefix-icon><t-icon name="search" /></template>
      </t-input>
      <span class="at-total-chip">{{ t('agentTasks.totalChip', { total }) }}</span>
    </div>

    <div v-if="error" class="at-error-banner" role="alert">
      <t-icon name="error-circle" size="16px" />
      <span class="at-error-text-inline">{{ error }}</span>
      <t-button size="small" variant="outline" @click="reload">
        {{ t('agentTasks.refresh') }}
      </t-button>
    </div>

    <div class="at-table-shell data-table-shell">
      <t-table
        row-key="id"
        :data="rows"
        :columns="columns"
        :loading="loading && rows.length === 0"
        size="medium"
        hover
      >
        <template #doc_name="{ row }">
          <div class="at-doc-cell">
            <span class="at-doc-name" :title="row.doc_name">{{ row.doc_name || '-' }}</span>
            <span v-if="row.knowledge_id" class="at-doc-meta">{{ row.knowledge_id }}</span>
          </div>
        </template>

        <template #knowledge_base_name="{ row }">
          <span :title="row.knowledge_base_name">{{ row.knowledge_base_name || '-' }}</span>
        </template>

        <template #skill="{ row }">
          <span :title="row.skill">{{ row.skill || '-' }}</span>
        </template>

        <template #status="{ row }">
          <t-tag :theme="statusTheme(row.status)" variant="light" size="small">
            {{ statusLabel(row.status) }}
          </t-tag>
        </template>

        <template #attempts="{ row }">
          <span class="at-number">{{ row.attempts }}/{{ row.max_attempts }}</span>
        </template>

        <template #wait_seconds="{ row }">
          <span class="at-number">{{ formatDuration(row.wait_seconds) }}</span>
        </template>

        <template #run_seconds="{ row }">
          <span class="at-number">{{ formatDuration(row.run_seconds) }}</span>
        </template>

        <template #last_error="{ row }">
          <span v-if="row.last_error" class="at-error-text" :title="row.last_error">
            {{ row.last_error }}
          </span>
          <span v-else class="at-muted">-</span>
        </template>

        <template #operations="{ row }">
          <t-space :size="4">
            <t-button
              variant="text"
              theme="primary"
              size="small"
              :disabled="!canRetry(row) || Boolean(actionId)"
              :loading="actionId === row.id && action === 'retry'"
              @click="handleRetry(row)"
            >
              {{ t('agentTasks.retry') }}
            </t-button>
            <t-popconfirm
              theme="danger"
              :content="t('agentTasks.cancelConfirm')"
              @confirm="handleCancel(row)"
            >
              <t-button
                variant="text"
                theme="danger"
                size="small"
                :disabled="!canCancel(row) || Boolean(actionId)"
                :loading="actionId === row.id && action === 'cancel'"
              >
                {{ t('agentTasks.cancel') }}
              </t-button>
            </t-popconfirm>
          </t-space>
        </template>

        <template #empty>
          <div class="at-empty">
            <t-icon name="queue" size="28px" />
            <span>{{ t('agentTasks.empty') }}</span>
          </div>
        </template>
      </t-table>
    </div>

    <div v-if="total > 0" class="at-pagination">
      <t-pagination
        v-model="page"
        v-model:page-size="pageSize"
        :total="total"
        :page-size-options="[10, 20, 50]"
        :show-jumper="true"
        @change="handlePageChange"
      />
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import {
  listAgentTasks,
  getAgentTaskSummary,
  retryAgentTask,
  cancelAgentTask,
  type AgentTaskItem,
  type AgentTaskStatus,
  type AgentTaskSummary,
} from '@/api/agent-task'
import { useAuthStore } from '@/stores/auth'

const { t, te } = useI18n()

// 读接口对本工作区成员开放（按租户限域），但 retry / cancel 打的是共享的网关队列，
// 后端走 AdminOrSystemAdmin 闸门：非管理员点按钮只会拿到 403，所以这里直接禁用。
const authStore = useAuthStore()
const canOperate = computed(() => authStore.isSystemAdmin || authStore.hasRole('admin'))

const POLL_INTERVAL_MS = 10000
const DEFAULT_PAGE_SIZE = 20
const STATUS_VALUES: AgentTaskStatus[] = ['queued', 'running', 'succeeded', 'failed', 'cancelled']

const rows = ref<AgentTaskItem[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(DEFAULT_PAGE_SIZE)
const statusFilter = ref<string>('all')
const keyword = ref('')
const loading = ref(false)
const error = ref('')
const autoRefresh = ref(true)
const actionId = ref('')
const action = ref<'retry' | 'cancel' | ''>('')
const summary = ref<AgentTaskSummary>({
  running: 0,
  queued: 0,
  failed_today: 0,
  succeeded_today: 0,
  total: 0,
  concurrency: 0,
  oldest_queued_seconds: 0,
  gateway_configured: false,
  gateway_url: '',
})

let pollTimer: ReturnType<typeof setInterval> | null = null
let requestId = 0

const statusOptions = computed(() => [
  { label: t('agentTasks.statusAll'), value: 'all' },
  ...STATUS_VALUES.map(value => ({ label: statusLabel(value), value })),
])

const columns = computed(() => [
  { colKey: 'doc_name', title: t('agentTasks.columns.docName'), minWidth: 240 },
  { colKey: 'knowledge_base_name', title: t('agentTasks.columns.knowledgeBase'), minWidth: 160, ellipsis: true },
  { colKey: 'skill', title: t('agentTasks.columns.skill'), width: 150, ellipsis: true },
  { colKey: 'status', title: t('agentTasks.columns.status'), width: 110 },
  { colKey: 'attempts', title: t('agentTasks.columns.attempts'), width: 100, align: 'center' as const },
  { colKey: 'wait_seconds', title: t('agentTasks.columns.wait'), width: 110, align: 'center' as const },
  { colKey: 'run_seconds', title: t('agentTasks.columns.run'), width: 110, align: 'center' as const },
  { colKey: 'last_error', title: t('agentTasks.columns.lastError'), minWidth: 200, ellipsis: true },
  { colKey: 'operations', title: t('agentTasks.columns.operations'), width: 168, align: 'center' as const },
])

// 状态文案走 i18n；后端若新增状态则回退为原始值，避免前端渲染空白
function statusLabel(status: string): string {
  const path = `agentTasks.status.${status}`
  return te(path) ? (t(path) as string) : status
}

function statusTheme(status: string): 'default' | 'primary' | 'success' | 'warning' | 'danger' {
  switch (status) {
    case 'queued':
      return 'warning'
    case 'running':
      return 'primary'
    case 'succeeded':
      return 'success'
    case 'failed':
      return 'danger'
    default:
      return 'default'
  }
}

// 0 / null 视为无耗时，其余按秒人性化展示（45s / 3m20s / 1h05m）
function formatDuration(seconds: number | null | undefined): string {
  const value = Number(seconds)
  if (!value || value <= 0) return t('agentTasks.duration.empty')
  if (value < 60) return t('agentTasks.duration.seconds', { n: Math.floor(value) })
  if (value < 3600) {
    return t('agentTasks.duration.minutes', {
      m: Math.floor(value / 60),
      s: Math.floor(value % 60),
    })
  }
  return t('agentTasks.duration.hours', {
    h: Math.floor(value / 3600),
    m: String(Math.floor((value % 3600) / 60)).padStart(2, '0'),
  })
}

function canRetry(row: AgentTaskItem): boolean {
  return canOperate.value && (row.status === 'failed' || row.status === 'cancelled')
}

function canCancel(row: AgentTaskItem): boolean {
  return canOperate.value && (row.status === 'queued' || row.status === 'running')
}

async function load() {
  const current = ++requestId
  loading.value = true
  try {
    const [list, stats] = await Promise.all([
      listAgentTasks({
        status: statusFilter.value === 'all' ? '' : statusFilter.value,
        keyword: keyword.value.trim(),
        page: page.value,
        page_size: pageSize.value,
      }),
      getAgentTaskSummary(),
    ])
    if (current !== requestId) return
    rows.value = list?.items || []
    total.value = list?.total || 0
    if (list?.page) page.value = list.page
    if (list?.page_size) pageSize.value = list.page_size
    if (stats) summary.value = stats
    error.value = ''
  } catch (err: any) {
    if (current !== requestId) return
    rows.value = []
    total.value = 0
    error.value = err?.message || t('agentTasks.loadFailed')
  } finally {
    if (current === requestId) loading.value = false
  }
}

function reload() {
  load()
}

function handleFilterChange() {
  page.value = 1
  load()
}

function handlePageChange() {
  // v-model 已同步 page / pageSize，这里只需重新拉取
  load()
}

async function runAction(row: AgentTaskItem, kind: 'retry' | 'cancel') {
  if (actionId.value) return
  actionId.value = row.id
  action.value = kind
  try {
    if (kind === 'retry') await retryAgentTask(row.id)
    else await cancelAgentTask(row.id)
    MessagePlugin.success(t('agentTasks.actionSucceeded', { action: t(`agentTasks.${kind}`) }))
    await load()
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('agentTasks.actionFailed', { action: t(`agentTasks.${kind}`) }))
  } finally {
    actionId.value = ''
    action.value = ''
  }
}

function handleRetry(row: AgentTaskItem) {
  if (!canRetry(row)) return
  runAction(row, 'retry')
}

function handleCancel(row: AgentTaskItem) {
  if (!canCancel(row)) return
  runAction(row, 'cancel')
}

function startPolling() {
  stopPolling()
  if (!autoRefresh.value) return
  pollTimer = setInterval(() => {
    // 静默后台刷新：不打断正在进行的操作
    if (!loading.value && !actionId.value) load()
  }, POLL_INTERVAL_MS)
}

function stopPolling() {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

watch(autoRefresh, (on) => {
  if (on) startPolling()
  else stopPolling()
})

onMounted(() => {
  load()
  startPolling()
})

onUnmounted(() => {
  stopPolling()
})
</script>

<style lang="less" scoped>
.agent-tasks {
  height: 100%;
  display: flex;
  flex-direction: column;
  padding: 20px 24px;
  box-sizing: border-box;
  overflow: auto;
  color: var(--td-text-color-primary);
}

.at-header {
  flex-shrink: 0;
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 24px;
  padding-bottom: 16px;
  border-bottom: 1px solid var(--td-component-stroke, #e5e6eb);

  h2 {
    margin: 0;
    font-size: 20px;
    font-weight: 600;
    line-height: 1.3;
  }
}

.at-subtitle {
  margin: 6px 0 0;
  font-size: 13px;
  color: var(--td-text-color-secondary, #6b7280);
}

.at-header-actions {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-shrink: 0;
}

.at-auto-refresh {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  color: var(--td-text-color-secondary, #6b7280);
  cursor: pointer;
  user-select: none;
}

.at-live-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--td-text-color-placeholder, #c5c8ce);
  transition: background 0.2s ease;
}

.at-live-dot--active {
  background: var(--td-success-color, #2ba471);
}

.at-overview {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 16px;
  margin: 20px 0;
}

.at-metric {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 16px 18px;
  border: 1px solid var(--td-component-stroke, #e5e6eb);
  border-radius: 10px;
  background: var(--td-bg-color-container, #fff);
}

.at-metric--danger {
  border-color: var(--td-error-color, #d54941);
}

.at-metric--success {
  border-color: var(--td-success-color, #2ba471);
}

.at-metric-label {
  font-size: 13px;
  color: var(--td-text-color-secondary, #6b7280);
}

.at-metric-value {
  font-size: 26px;
  font-weight: 600;
  line-height: 1.2;
  font-variant-numeric: tabular-nums;
}

.at-toolbar {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
  margin-bottom: 12px;
}

.at-status-select {
  width: 160px;
}

.at-keyword-input {
  width: 260px;
}

.at-total-chip {
  font-size: 13px;
  color: var(--td-text-color-secondary, #6b7280);
}

.at-error-banner {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 12px;
  padding: 10px 14px;
  border: 1px solid var(--td-error-color, #d54941);
  border-radius: 6px;
  color: var(--td-error-color, #d54941);
  font-size: 13px;
}

.at-error-text-inline {
  flex: 1;
  min-width: 0;
}

.data-table-shell {
  overflow-x: auto;
  border-radius: 10px;
  border: 1px solid var(--td-component-stroke, #e5e6eb);
  background-color: var(--td-bg-color-container, #fff);

  &:deep(thead th) {
    font-weight: 600;
    font-size: 13px;
    background-color: var(--td-bg-color-secondarycontainer) !important;
  }

  &:deep(.t-table td),
  &:deep(.t-table th) {
    padding-top: 14px;
    padding-bottom: 14px;
    vertical-align: middle;
  }
}

.at-doc-cell {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.at-doc-name {
  font-weight: 500;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.at-doc-meta {
  font-size: 12px;
  color: var(--td-text-color-placeholder, #c5c8ce);
  font-family: var(--td-font-family-mono, ui-monospace, SFMono-Regular, Menlo, Consolas, monospace);
}

.at-number {
  font-variant-numeric: tabular-nums;
}

.at-error-text {
  display: block;
  color: var(--td-error-color, #d54941);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.at-muted {
  color: var(--td-text-color-placeholder, #c5c8ce);
}

.at-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
  padding: 32px 0;
  color: var(--td-text-color-secondary, #6b7280);
}

.at-pagination {
  display: flex;
  justify-content: flex-end;
  padding: 16px 0 0;
}
</style>
