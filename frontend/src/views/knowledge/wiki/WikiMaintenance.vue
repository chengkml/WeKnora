<template>
  <div class="wiki-maintenance">
    <!-- Empty / not-wiki guard is handled by the parent; here we render the view -->
    <div class="wiki-maintenance-header">
      <div class="wiki-maintenance-title">
        <t-icon name="comment" class="wm-title-icon" />
        <span>{{ $t('knowledgeEditor.wikiMaintenance.title') }}</span>
      </div>
      <div class="wiki-maintenance-summary">
        <span class="wm-summary-chip wm-summary-total">{{ $t('knowledgeEditor.wikiMaintenance.totalChip', { total }) }}</span>
        <span v-if="pendingCount" class="wm-summary-chip wm-summary-pending">
          {{ $t('knowledgeEditor.wikiMaintenance.pendingChip', { count: pendingCount }) }}
        </span>
      </div>
    </div>

    <!-- Filter bar -->
    <div class="wiki-maintenance-toolbar">
      <div class="wm-filter-row">
        <div class="wm-filter-group">
          <span class="wm-filter-label">{{ $t('knowledgeEditor.wikiMaintenance.typeLabel') }}</span>
          <t-radio-group v-model="filterType" variant="default-filled" size="small">
            <t-radio-button value="">{{ $t('knowledgeEditor.wikiMaintenance.typeAll') }}</t-radio-button>
            <t-radio-button value="question">{{ $t('knowledgeEditor.wikiMaintenance.typeQuestion') }}</t-radio-button>
            <t-radio-button value="comment">{{ $t('knowledgeEditor.wikiMaintenance.typeComment') }}</t-radio-button>
          </t-radio-group>
        </div>
        <div class="wm-filter-group">
          <span class="wm-filter-label">{{ $t('knowledgeEditor.wikiMaintenance.statusLabel') }}</span>
          <t-select v-model="filterStatus" class="wm-status-select" size="small"
            :placeholder="$t('knowledgeEditor.wikiMaintenance.statusAll')"
            :options="statusOptions" clearable />
        </div>
        <t-input v-model="slugFilter" class="wm-slug-search" size="small" clearable
          :placeholder="$t('knowledgeEditor.wikiMaintenance.searchPlaceholder')"
          @enter="reload(1)" @clear="reload(1)">
          <template #prefixIcon><t-icon name="search" /></template>
        </t-input>
      </div>
    </div>

    <!-- List -->
    <div class="wiki-maintenance-body">
      <div v-if="loading && items.length === 0" class="wm-loading"><t-loading /></div>

      <div v-else-if="!loading && items.length === 0" class="wm-empty">
        <div class="wm-empty-icon"><t-icon name="comment" size="36px" /></div>
        <p class="wm-empty-title">{{ $t('knowledgeEditor.wikiMaintenance.emptyTitle') }}</p>
        <p class="wm-empty-desc">{{ $t('knowledgeEditor.wikiMaintenance.emptyDesc') }}</p>
      </div>

      <template v-else>
        <div v-for="item in items" :key="item.id" class="wm-item" :class="{ 'wm-item--muted': item.status !== 'pending' }">
          <div class="wm-item-main">
            <div class="wm-item-head">
              <t-tag :theme="item.feedback_type === 'question' ? 'warning' : 'primary'" variant="light" size="small">
                {{ item.feedback_type === 'question'
                  ? $t('knowledgeEditor.wikiMaintenance.typeQuestion')
                  : $t('knowledgeEditor.wikiMaintenance.typeComment') }}
              </t-tag>
              <t-tag :theme="statusTheme(item.status)" variant="light-outline" size="small">
                {{ statusText(item.status) }}
              </t-tag>
              <span class="wm-item-time">{{ formatDate(item.created_at) }}</span>
            </div>

            <a href="#" class="wm-item-page" @click.prevent="openPage(item.slug)">
              <t-icon name="link" size="12px" />
              {{ item.page_title || slugDisplayName(item.slug) }}
              <span class="wm-item-slug">{{ item.slug }}</span>
            </a>

            <div class="wm-item-content">{{ item.content }}</div>

            <div class="wm-item-meta">
              <span class="wm-item-reporter">
                <t-icon name="user" size="13px" />
                {{ item.reported_by_name || item.reported_by_id || '—' }}
              </span>
            </div>
          </div>

          <div v-if="canEdit" class="wm-item-actions">
            <t-button v-if="item.status === 'pending'" size="small" theme="success" variant="text" @click="setStatus(item, 'resolved')">
              <template #icon><t-icon name="check" /></template>
              {{ $t('knowledgeEditor.wikiMaintenance.actionResolve') }}
            </t-button>
            <t-button v-if="item.status !== 'pending'" size="small" theme="default" variant="text" @click="setStatus(item, 'pending')">
              <template #icon><t-icon name="rollback" /></template>
              {{ $t('knowledgeEditor.wikiMaintenance.actionReopen') }}
            </t-button>
            <t-button size="small" theme="default" variant="text" @click="setStatus(item, 'ignored')">
              <template #icon><t-icon name="close" /></template>
              {{ $t('knowledgeEditor.wikiMaintenance.actionIgnore') }}
            </t-button>
            <t-popconfirm :content="$t('knowledgeEditor.wikiMaintenance.deleteConfirm')" theme="danger"
              confirm-btn="删除" cancel-btn="取消" @confirm="removeItem(item)">
              <t-button size="small" theme="danger" variant="text">
                <template #icon><t-icon name="delete" /></template>
                {{ $t('knowledgeEditor.wikiMaintenance.actionDelete') }}
              </t-button>
            </t-popconfirm>
          </div>
        </div>

        <!-- Pagination -->
        <div class="wm-pagination">
          <t-pagination v-model="currentPage" :total="total" :page-size="pageSize" :show-jumper="true"
            :show-page-size="false" @change="onPageChange" />
        </div>
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted } from 'vue';
import { MessagePlugin } from 'tdesign-vue-next';
import { useI18n } from 'vue-i18n';
import {
  listWikiFeedback,
  updateWikiFeedbackStatus,
  deleteWikiFeedback,
  type WikiPageFeedback,
  type WikiFeedbackStatus,
} from '@/api/wiki';

const props = defineProps<{
  knowledgeBaseId: string;
  canEdit?: boolean;
}>();

const emit = defineEmits<{
  (e: 'open-page', slug: string): void;
}>();

const { t } = useI18n();

const items = ref<WikiPageFeedback[]>([]);
const total = ref(0);
const currentPage = ref(1);
const pageSize = 50;
const loading = ref(false);

const filterType = ref(''); // '' | 'question' | 'comment'
const filterStatus = ref<WikiFeedbackStatus | ''>(''); // '' | 'pending' | ...
const slugFilter = ref('');

const statusOptions = computed(() => [
  { label: t('knowledgeEditor.wikiMaintenance.statusPending'), value: 'pending' },
  { label: t('knowledgeEditor.wikiMaintenance.statusResolved'), value: 'resolved' },
  { label: t('knowledgeEditor.wikiMaintenance.statusIgnored'), value: 'ignored' },
]);

const pendingCount = computed(() => items.value.filter((i) => i.status === 'pending').length);

function statusText(status: WikiFeedbackStatus): string {
  const map: Record<WikiFeedbackStatus, string> = {
    pending: t('knowledgeEditor.wikiMaintenance.statusPending'),
    resolved: t('knowledgeEditor.wikiMaintenance.statusResolved'),
    ignored: t('knowledgeEditor.wikiMaintenance.statusIgnored'),
  };
  return map[status] || status;
}

function statusTheme(status: WikiFeedbackStatus): string {
  const map: Record<WikiFeedbackStatus, 'warning' | 'success' | 'default'> = {
    pending: 'warning',
    resolved: 'success',
    ignored: 'default',
  };
  return map[status] || 'default';
}

function slugDisplayName(slug: string): string {
  if (!slug) return ''
  const segs = slug.split('/').filter(Boolean)
  return segs.length ? segs[segs.length - 1] : slug
}

function formatDate(iso: string): string {
  if (!iso) return '—'
  const d = new Date(iso)
  if (isNaN(d.getTime())) return iso
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

async function reload(page = currentPage.value) {
  loading.value = true
  try {
    const res: any = await listWikiFeedback(props.knowledgeBaseId, {
      page,
      page_size: pageSize,
      feedback_type: filterType.value as any || undefined,
      status: filterStatus.value || undefined,
      slug: slugFilter.value || undefined,
    })
    const body = res?.data || res || {}
    items.value = body.items || []
    total.value = body.total || 0
    currentPage.value = body.page || page
  } catch (e) {
    console.error('List wiki feedback failed:', e)
    MessagePlugin.error('加载反馈失败')
    items.value = []
  } finally {
    loading.value = false
  }
}

function onPageChange() {
  reload(currentPage.value)
}

function openPage(slug: string) {
  emit('open-page', slug)
}

async function setStatus(item: WikiPageFeedback, status: WikiFeedbackStatus) {
  try {
    await updateWikiFeedbackStatus(props.knowledgeBaseId, item.id, status)
    item.status = status
  } catch (e) {
    console.error('Update feedback status failed:', e)
    MessagePlugin.error('状态更新失败')
  }
}

async function removeItem(item: WikiPageFeedback) {
  try {
    await deleteWikiFeedback(props.knowledgeBaseId, item.id)
    MessagePlugin.success('已删除')
    await reload(currentPage.value)
  } catch (e) {
    console.error('Delete feedback failed:', e)
    MessagePlugin.error('删除失败')
  }
}

watch([filterType, filterStatus], () => { reload(1) })

onMounted(() => { reload(1) })
</script>

<style scoped lang="less">
.wiki-maintenance {
  height: 100%;
  display: flex;
  flex-direction: column;
  padding: 16px 20px;
  box-sizing: border-box;
  overflow: hidden;
}

.wiki-maintenance-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 12px;
}

.wiki-maintenance-title {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 16px;
  font-weight: 600;
  color: var(--td-text-color-primary);

  .wm-title-icon {
    color: var(--td-brand-color);
  }
}

.wiki-maintenance-summary {
  display: flex;
  gap: 8px;
}

.wm-summary-chip {
  display: inline-flex;
  align-items: center;
  padding: 2px 10px;
  border-radius: 999px;
  font-size: 12px;

  &.wm-summary-total { background: var(--td-brand-color-1); color: var(--td-brand-color); }
  &.wm-summary-pending { background: var(--td-warning-color-1); color: var(--td-warning-color); }
}

.wiki-maintenance-toolbar {
  margin-bottom: 12px;
}

.wm-filter-row {
  display: flex;
  align-items: center;
  gap: 20px;
  flex-wrap: wrap;
}

.wm-filter-group {
  display: flex;
  align-items: center;
  gap: 8px;
}

.wm-filter-label {
  font-size: 12px;
  color: var(--td-text-color-secondary);
  white-space: nowrap;
}

.wm-status-select {
  width: 130px;
}

.wm-slug-search {
  width: 220px;
  margin-left: auto;
}

.wiki-maintenance-body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  padding: 8px;
}

.wm-loading {
  display: flex;
  justify-content: center;
  padding: 60px 0;
}

.wm-empty {
  padding: 60px 20px;
  text-align: center;

  .wm-empty-icon { color: var(--td-text-color-placeholder); }
  .wm-empty-title { margin: 12px 0 4px; font-size: 14px; color: var(--td-text-color-primary); }
  .wm-empty-desc { font-size: 12px; color: var(--td-text-color-placeholder); }
}

.wm-item {
  display: flex;
  align-items: flex-start;
  padding: 14px 16px;
  border-bottom: 1px solid var(--td-component-stroke);
  transition: background 0.2s;

  &:last-child { border-bottom: none; }
  &:hover { background: var(--td-bg-color-container-hover); }

  &.wm-item--muted { opacity: 0.72; }
}

.wm-item-main {
  flex: 1;
  min-width: 0;
}

.wm-item-head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 6px;
}

.wm-item-time {
  font-size: 12px;
  color: var(--td-text-color-placeholder);
  margin-left: 4px;
}

.wm-item-page {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 14px;
  font-weight: 600;
  color: var(--td-brand-color);
  text-decoration: none;

  &:hover { text-decoration: underline; }

  .wm-item-slug {
    font-size: 12px;
    font-weight: 400;
    color: var(--td-text-color-placeholder);
  }
}

.wm-item-content {
  margin-top: 6px;
  font-size: 13px;
  line-height: 1.6;
  color: var(--td-text-color-primary);
  white-space: pre-wrap;
  word-break: break-word;
}

.wm-item-meta {
  margin-top: 8px;
  display: flex;
  align-items: center;
}

.wm-item-reporter {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 12px;
  color: var(--td-text-color-secondary);
}

.wm-item-actions {
  display: flex;
  align-items: center;
  gap: 4px;
  flex-shrink: 0;
  padding-left: 12px;
  margin-top: 2px;
}

.wm-pagination {
  display: flex;
  justify-content: flex-end;
  padding: 12px 8px 4px;
}
</style>