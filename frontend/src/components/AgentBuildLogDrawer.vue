<template>
  <t-drawer
    :visible="visible"
    :header="t('agentTasks.detail.title')"
    size="760px"
    :footer="false"
    destroy-on-close
    @close="close"
    @after-enter="onOpened"
  >
    <div v-if="loading" class="at-detail-loading">{{ t('agentTasks.detail.loading') }}</div>
    <div v-else-if="loadError" class="at-detail-error">{{ loadError }}</div>
    <div v-else-if="detail" class="at-detail">
      <!-- 摘要：台账行 + 网关任务快照 -->
      <section class="at-detail-block">
        <h4>{{ t('agentTasks.detail.overview') }}</h4>
        <div class="at-detail-grid">
          <span class="at-detail-key">{{ t('agentTasks.detail.docName') }}</span>
          <span class="at-detail-val" :title="detail.task.doc_name">{{ detail.task.doc_name || '-' }}</span>
          <span class="at-detail-key">{{ t('agentTasks.detail.knowledgeBase') }}</span>
          <span class="at-detail-val">
            {{ detail.task.knowledge_base_name || detail.task.knowledge_base_id || '-' }}
          </span>
          <span class="at-detail-key">{{ t('agentTasks.detail.skill') }}</span>
          <span class="at-detail-val">{{ detail.task.skill || '-' }}</span>
          <span class="at-detail-key">{{ t('agentTasks.detail.status') }}</span>
          <span class="at-detail-val">
            <t-tag :theme="statusTheme(detail.task.status)" variant="light" size="small">
              {{ taskStatusLabel(detail.task.status) }}
            </t-tag>
          </span>
          <span class="at-detail-key">{{ t('agentTasks.detail.attempts') }}</span>
          <span class="at-detail-val">{{ detail.task.attempts }}/{{ detail.task.max_attempts }}</span>
          <span class="at-detail-key">{{ t('agentTasks.detail.duration') }}</span>
          <span class="at-detail-val">
            {{ detail.gateway?.runs_ms != null ? t('agentTasks.detail.durationShort', { n: detail.gateway.runs_ms }) : formatRunSeconds(detail.task.run_seconds) }}
          </span>
          <span class="at-detail-key">{{ t('agentTasks.detail.gatewayTaskId') }}</span>
          <span class="at-detail-val at-detail-mono">{{ detail.gateway?.task_id || detail.task.gateway_task_id || '-' }}</span>
          <span class="at-detail-key">{{ t('agentTasks.detail.traceId') }}</span>
          <span class="at-detail-val at-detail-mono">
            {{ detail.gateway?.sdk_trace_id || detail.trace?.trace_id || detail.gateway?.trace_id || '-' }}
          </span>
          <span class="at-detail-key">{{ t('agentTasks.detail.agent') }}</span>
          <span class="at-detail-val">{{ detail.gateway?.agent_name || '-' }}</span>
        </div>
      </section>

      <section v-if="detail.notes && detail.notes.length" class="at-detail-block">
        <h4>{{ t('agentTasks.detail.notes') }}</h4>
        <ul class="at-detail-notes">
          <li v-for="(note, index) in detail.notes" :key="index">{{ note }}</li>
        </ul>
      </section>

      <section v-if="detail.gateway?.output_text" class="at-detail-block">
        <h4>{{ t('agentTasks.detail.output') }}</h4>
        <pre class="at-detail-pre">{{ detail.gateway.output_text }}</pre>
      </section>

      <section v-if="errorText" class="at-detail-block">
        <h4>{{ t('agentTasks.detail.error') }}</h4>
        <pre class="at-detail-pre at-detail-pre--error">{{ errorText }}</pre>
      </section>

      <section class="at-detail-block">
        <h4>
          {{ t('agentTasks.detail.trace') }}
          <span class="at-detail-chip">
            {{ t('agentTasks.detail.spanCount', { n: detail.trace?.span_count || 0 }) }}
          </span>
          <t-tag v-if="detail.task.status === 'running'" size="small" theme="warning" variant="light">
            {{ t('agentTasks.detail.liveTag') }}
          </t-tag>
        </h4>
        <div v-if="!detailSpans.length" class="at-detail-muted">{{ t('agentTasks.detail.traceEmpty') }}</div>
        <div v-else class="at-trace">
          <div
            v-for="span in detailSpans"
            :key="span.id"
            class="at-trace-row"
            :style="{ paddingLeft: 8 + span.depth * 18 + 'px' }"
          >
            <div class="at-trace-head" @click="toggleSpan(span.id)">
              <t-icon :name="expandedSpans.has(span.id) ? 'chevron-down' : 'chevron-right'" size="16px" />
              <t-tag size="small" variant="light" :theme="span.status === 'error' ? 'danger' : 'default'">
                {{ span.type }}
              </t-tag>
              <span class="at-trace-name" :title="span.name">{{ span.name || span.type }}</span>
              <span class="at-trace-dur">
                {{ span.duration_ms != null ? t('agentTasks.detail.durationShort', { n: span.duration_ms }) : '' }}
              </span>
            </div>
            <div v-if="span.summary" class="at-trace-summary">{{ span.summary }}</div>
            <div v-if="expandedSpans.has(span.id)" class="at-trace-body">
              <div v-if="span.error" class="at-trace-part">
                <strong>{{ t('agentTasks.detail.error') }}</strong>
                <pre class="at-detail-pre at-detail-pre--error">{{ span.error }}</pre>
              </div>
              <div v-if="span.input" class="at-trace-part">
                <strong>{{ t('agentTasks.detail.input') }}</strong>
                <pre class="at-detail-pre">{{ span.input }}</pre>
              </div>
              <div v-if="span.output" class="at-trace-part">
                <strong>{{ t('agentTasks.detail.spanOutput') }}</strong>
                <pre class="at-detail-pre">{{ span.output }}</pre>
              </div>
              <div v-if="span.detail" class="at-trace-part">
                <strong>{{ t('agentTasks.detail.raw') }}</strong>
                <pre class="at-detail-pre at-detail-pre--raw">{{ span.detail }}</pre>
              </div>
            </div>
          </div>
        </div>
      </section>
    </div>
  </t-drawer>
</template>

<script setup lang="ts">
import { ref, computed, watch, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  getAgentTaskDetail,
  type AgentTaskDetail,
  type AgentTaskTraceSpan,
} from '@/api/agent-task'

const props = defineProps<{
  visible: boolean
  /** agent_build_tasks 台账行 id（spans 接口 agent.task_id） */
  taskId: string | null
}>()

const emit = defineEmits<{ (e: 'update:visible', v: boolean): void }>()

const { t, te } = useI18n()

const loading = ref(false)
const loadError = ref('')
const detail = ref<AgentTaskDetail | null>(null)
const expandedSpans = ref<Set<string>>(new Set())

const DETAIL_POLL_INTERVAL_MS = 5000
let pollTimer: ReturnType<typeof setInterval> | null = null

/** 按 parent_id 还原 span 树再拍平，模板逐行渲染 */
interface DetailSpanNode extends AgentTaskTraceSpan {
  depth: number
}
type DetailSpanTreeNode = DetailSpanNode & { children: DetailSpanTreeNode[] }

const detailSpans = computed<DetailSpanNode[]>(() => {
  const spans = detail.value?.trace?.spans || []
  const nodes = new Map<string, DetailSpanTreeNode>()
  spans.forEach((span) => {
    nodes.set(span.id, { ...span, depth: 0, children: [] })
  })
  const roots: DetailSpanTreeNode[] = []
  nodes.forEach((node) => {
    const parent = node.parent_id ? nodes.get(node.parent_id) : undefined
    if (parent) parent.children.push(node)
    else roots.push(node)
  })
  const byStart = (a: DetailSpanNode, b: DetailSpanNode) =>
    String(a.started_at || '').localeCompare(String(b.started_at || ''))
  const flat: DetailSpanNode[] = []
  const walk = (list: DetailSpanTreeNode[], depth: number) => {
    list.sort(byStart)
    list.forEach((node) => {
      node.depth = depth
      flat.push(node)
      walk(node.children, depth + 1)
    })
  }
  walk(roots, 0)
  return flat
})

const errorText = computed(
  () => detail.value?.gateway?.error_detail || detail.value?.task?.last_error || '',
)

function formatRunSeconds(seconds?: number): string {
  const value = Number(seconds || 0)
  if (value <= 0) return '-'
  if (value < 60) return `${Math.floor(value)}s`
  if (value < 3600) return `${Math.floor(value / 60)}m${Math.floor(value % 60)}s`
  return `${Math.floor(value / 3600)}h${String(Math.floor((value % 3600) / 60)).padStart(2, '0')}m`
}

function taskStatusLabel(status: string): string {
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

function toggleSpan(id: string) {
  const next = new Set(expandedSpans.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  expandedSpans.value = next
}

async function load() {
  if (!props.taskId) return
  loading.value = true
  loadError.value = ''
  try {
    detail.value = await getAgentTaskDetail(props.taskId)
    // 默认展开根 span，一眼看到链路主干
    const first = detail.value?.trace?.spans?.find((span) => !span.parent_id)
    expandedSpans.value = new Set(first ? [first.id] : [])
    stopPolling()
    // 任务运行中，抽屉每 5s 静默刷新（span 实时增长）
    if (detail.value?.task?.status === 'running') startPolling()
  } catch (err: any) {
    loadError.value = err?.message || t('agentTasks.detail.loadFailed')
  } finally {
    loading.value = false
  }
}

function startPolling() {
  stopPolling()
  pollTimer = setInterval(refreshSilently, DETAIL_POLL_INTERVAL_MS)
}

function stopPolling() {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

async function refreshSilently() {
  if (!props.taskId) return
  try {
    const fresh = await getAgentTaskDetail(props.taskId)
    detail.value = fresh
    if (fresh.task.status !== 'running') {
      stopPolling()
      const first = fresh?.trace?.spans?.find((span) => !span.parent_id)
      expandedSpans.value = new Set(first ? [first.id] : [])
    } else {
      const first = fresh?.trace?.spans?.find((span) => !span.parent_id)
      const next = new Set(expandedSpans.value)
      if (first) next.add(first.id)
      expandedSpans.value = next
    }
  } catch {
    // 静默失败：下一轮轮询再试
  }
}

function close() {
  emit('update:visible', false)
}

function onOpened() {
  if (props.taskId) load()
}

watch(
  () => props.visible,
  (v) => {
    if (v && props.taskId) load()
  },
)

onUnmounted(() => stopPolling())
</script>

<style lang="less" scoped>
.at-detail-loading,
.at-detail-error,
.at-detail-muted {
  padding: 12px 0;
  color: var(--td-text-color-secondary, #6b7280);
}

.at-detail-error {
  color: var(--td-error-color, #d54941);
}

.at-detail-block {
  margin-bottom: 20px;
}

.at-detail-block h4 {
  margin: 0 0 8px;
  font-size: 14px;
  font-weight: 600;
  color: var(--td-text-color-primary, #1f2329);
}

.at-detail-grid {
  display: grid;
  grid-template-columns: 110px 1fr;
  gap: 6px 12px;
  font-size: 13px;
}

.at-detail-key {
  color: var(--td-text-color-secondary, #6b7280);
}

.at-detail-val {
  word-break: break-all;
}

.at-detail-mono,
.at-detail-chip {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 12px;
}

.at-detail-chip {
  margin-left: 8px;
  padding: 0 6px;
  border-radius: 4px;
  background: var(--td-bg-color-secondarycontainer, #f3f3f3);
  color: var(--td-text-color-secondary, #6b7280);
  font-weight: 400;
}

.at-detail-notes {
  margin: 0;
  padding-left: 18px;
  color: var(--td-warning-color, #e37318);
  font-size: 13px;
}

.at-detail-pre {
  margin: 0;
  max-height: 260px;
  overflow: auto;
  padding: 8px 10px;
  border-radius: 4px;
  background: var(--td-bg-color-secondarycontainer, #f7f7f7);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 12px;
  line-height: 1.5;
  white-space: pre-wrap;
  word-break: break-all;
}

.at-detail-pre--raw {
  max-height: 200px;
  color: var(--td-text-color-secondary, #6b7280);
}

.at-detail-pre--error {
  background: var(--td-error-color-1, #fdecee);
  color: var(--td-error-color, #d54941);
}

.at-trace-row {
  padding: 4px 0;
  border-bottom: 1px dashed var(--td-component-stroke, #e7e7e7);
}

.at-trace-head {
  display: flex;
  align-items: center;
  gap: 6px;
  cursor: pointer;
  font-size: 13px;
}

.at-trace-name {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.at-trace-dur {
  color: var(--td-text-color-secondary, #6b7280);
  font-size: 12px;
}

.at-trace-summary {
  margin: 2px 0 0 22px;
  color: var(--td-text-color-secondary, #6b7280);
  font-size: 12px;
  word-break: break-all;
}

.at-trace-body {
  margin: 6px 0 4px 22px;
}

.at-trace-part {
  margin-bottom: 6px;
  font-size: 12px;
}

.at-trace-part strong {
  display: block;
  margin-bottom: 2px;
  color: var(--td-text-color-secondary, #6b7280);
  font-weight: 500;
}
</style>