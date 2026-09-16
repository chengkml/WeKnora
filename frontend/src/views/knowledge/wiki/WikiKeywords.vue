<template>
  <div class="wiki-keywords">
    <!-- Header -->
    <div class="wiki-keywords-header">
      <div class="wiki-keywords-title">
        <t-icon name="chart-bubble" size="20px" class="wk-title-icon" />
        <span>{{ $t('knowledgeEditor.wikiKeywords.title') }}</span>
      </div>
      <div class="wiki-keywords-summary">
        <span class="wk-summary-chip">{{ $t('knowledgeEditor.wikiKeywords.totalChip', { total }) }}</span>
      </div>
    </div>

    <!-- Toolbar: search + sort -->
    <div class="wiki-keywords-toolbar">
      <t-input v-model.trim="search" class="wk-search" size="small" clearable
        :placeholder="$t('knowledgeEditor.wikiKeywords.searchPlaceholder')"
        @enter="reload(1)" @clear="reload(1)">
        <template #prefixIcon><t-icon name="search" /></template>
      </t-input>
      <t-select v-model="sortMode" class="wk-sort-select" size="small" :options="sortOptions" @change="reload(1)" />
    </div>

    <!-- Ranked list -->
    <div class="wiki-keywords-body">
      <div v-if="loading && items.length === 0" class="wk-loading"><t-loading /></div>

      <div v-else-if="!loading && items.length === 0" class="wk-empty">
        <div class="wk-empty-icon"><t-icon name="chart-bubble" size="36px" /></div>
        <p class="wk-empty-title">{{ $t('knowledgeEditor.wikiKeywords.emptyTitle') }}</p>
        <p class="wk-empty-desc">{{ $t('knowledgeEditor.wikiKeywords.emptyDesc') }}</p>
      </div>

      <template v-else>
        <table class="wk-table">
          <thead>
            <tr>
              <th class="wk-col-rank">{{ $t('knowledgeEditor.wikiKeywords.rank') }}</th>
              <th class="wk-col-keyword">{{ $t('knowledgeEditor.wikiKeywords.keyword') }}</th>
              <th class="wk-col-meaning">{{ $t('knowledgeEditor.wikiKeywords.meaning') }}</th>
              <th class="wk-col-num">{{ $t('knowledgeEditor.wikiKeywords.freq') }}</th>
              <th class="wk-col-num">{{ $t('knowledgeEditor.wikiKeywords.docCount') }}</th>
              <th class="wk-col-mark">{{ $t('knowledgeEditor.wikiKeywords.markColumn') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="(item, idx) in items" :key="item.slug" class="wk-row" @click="openPage(item.slug)">
              <td class="wk-col-rank">
                <span class="wk-rank-badge" :class="{ 'wk-rank-top': idx < 3 }">{{ (currentPage - 1) * pageSize + idx + 1 }}</span>
              </td>
              <td class="wk-col-keyword">
                <span class="wk-keyword-name">{{ item.keyword }}</span>
              </td>
              <td class="wk-col-meaning">
                <span v-if="item.meaning" class="wk-meaning" :title="item.meaning">{{ item.meaning }}</span>
                <span v-else class="wk-meaning-empty">—</span>
              </td>
              <td class="wk-col-num">
                <button type="button" class="wk-freq-link" :title="$t('knowledgeEditor.wikiKeywords.freqViewTip')"
                  @click.stop="openDrawer(item)">
                  <span class="wk-freq">{{ item.total_freq }}</span>
                </button>
              </td>
              <td class="wk-col-num">{{ item.doc_count }}</td>
              <td class="wk-col-mark">
                <div class="wk-mark-cell" @click.stop>
                  <button type="button" class="wk-mark-btn" @click="openMarkDialog(item)">
                    <t-icon name="chat-bubble-add" size="14px" />
                    <span>{{ $t('knowledgeEditor.wikiKeywords.markAction') }}</span>
                  </button>
                  <div v-if="hasMarks(item)" class="wk-mark-counts">
                    <span v-if="item.feedback_counts?.kw_meaningful" class="wk-mark-count wk-mark-ok"
                      :title="$t('knowledgeEditor.wikiKeywords.presetMeaningful')">
                      ✅ {{ item.feedback_counts.kw_meaningful }}
                    </span>
                    <span v-if="item.feedback_counts?.kw_noise" class="wk-mark-count wk-mark-no"
                      :title="$t('knowledgeEditor.wikiKeywords.presetNoise')">
                      ❌ {{ item.feedback_counts.kw_noise }}
                    </span>
                    <span v-if="item.feedback_counts?.kw_mis_extracted" class="wk-mark-count wk-mark-warn"
                      :title="$t('knowledgeEditor.wikiKeywords.presetMisExtracted')">
                      ⚠️ {{ item.feedback_counts.kw_mis_extracted }}
                    </span>
                  </div>
                </div>
              </td>
            </tr>
          </tbody>
        </table>

        <!-- Pagination -->
        <div class="wk-pagination">
          <t-pagination v-model="currentPage" :total="total" :page-size="pageSize" :show-jumper="true"
            :show-page-size="false" @change="onPageChange" />
        </div>
      </template>
    </div>

    <!-- Keyword detail drawer: shows the keyword wiki page content instead of navigating away -->
    <t-drawer v-model:visible="drawerVisible" :header="drawerTitle" size="580px" :footer="false">
      <div v-if="drawerLoading" class="wk-drawer-loading"><t-loading /></div>
      <div v-else-if="drawerContent" class="wk-drawer-content" v-html="drawerContent"></div>
      <div v-else class="wk-drawer-empty">{{ $t('knowledgeEditor.wikiKeywords.drawerEmpty') }}</div>
    </t-drawer>

    <!-- Mark keyword meaning dialog: annotate whether the keyword has real business
         meaning / was correctly extracted. Reuses the manual-feedback pipeline
         (preset keys kw_meaningful / kw_noise / kw_mis_extracted). -->
    <t-dialog v-model:visible="markDialog.visible" :header="$t('knowledgeEditor.wikiKeywords.markTitle')" :width="480"
      destroy-on-close
      :confirm-btn="{ theme: 'primary', content: $t('common.confirm'), loading: markDialog.submitting }"
      :cancel-btn="$t('common.cancel')" @confirm="submitMark" @close="resetMarkDialog">
      <div class="wk-mark-form">
        <div class="wk-mark-row">
          <span class="wk-mark-label">{{ $t('knowledgeEditor.wikiKeywords.markWordLabel') }}</span>
          <span class="wk-mark-word">{{ markDialog.keyword }}</span>
        </div>
        <div class="wk-mark-row">
          <span class="wk-mark-label">{{ $t('knowledgeEditor.wikiKeywords.markTypeLabel') }}</span>
          <t-select v-model="markDialog.preset" :options="markOptions"
            :placeholder="$t('knowledgeEditor.wikiKeywords.markPlaceholder')" />
        </div>
        <div v-if="markDialog.preset === FEEDBACK_PRESET_CUSTOM" class="wk-mark-custom">
          <span class="wk-mark-label">{{ $t('knowledgeEditor.wikiKeywords.markContentLabel') }}</span>
          <t-textarea v-model="markDialog.content"
            :placeholder="$t('knowledgeEditor.wikiKeywords.markContentPlaceholder')"
            :maxlength="1000" :autosize="{ minRows: 3, maxRows: 6 }" />
        </div>
        <div class="wk-mark-hint">{{ $t('knowledgeEditor.wikiKeywords.markHint') }}</div>
      </div>
    </t-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, watch } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { marked } from 'marked'
import { listWikiKeywords, getWikiPage, createWikiFeedback, type WikiKeywordStat } from '@/api/wiki'
import { sanitizeMarkdownHTML } from '@/utils/security'

const props = defineProps<{
  knowledgeBaseId: string
}>()

const emit = defineEmits<{
  (e: 'open-page', slug: string): void
}>()

const { t } = useI18n()

const items = ref<WikiKeywordStat[]>([])
const total = ref(0)
const loading = ref(false)
const currentPage = ref(1)
const pageSize = 50
const search = ref('')
const sortMode = ref('freq_desc')

const sortOptions = computed(() => [
  { label: t('knowledgeEditor.wikiKeywords.sortFreqDesc'), value: 'freq_desc' },
  { label: t('knowledgeEditor.wikiKeywords.sortFreqAsc'), value: 'freq_asc' },
  { label: t('knowledgeEditor.wikiKeywords.sortKeywordAsc'), value: 'keyword_asc' },
  { label: t('knowledgeEditor.wikiKeywords.sortKeywordDesc'), value: 'keyword_desc' },
])

async function reload(page: number) {
  loading.value = true
  try {
    const [sort, order] = sortMode.value.split('_') as ['freq' | 'keyword', 'desc' | 'asc']
    const res: any = await listWikiKeywords(props.knowledgeBaseId, {
      search: search.value || undefined,
      sort,
      order,
      page,
      page_size: pageSize,
    })
    const data = res?.data || res || {}
    items.value = data.items || []
    total.value = data.total || 0
    currentPage.value = data.page || page
  } catch (e) {
    console.error('Failed to load wiki keywords:', e)
    items.value = []
    total.value = 0
    MessagePlugin.error(t('knowledgeEditor.wikiKeywords.loadFailed'))
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

// --- Keyword meaning mark (reuses the manual-feedback pipeline) ---

const FEEDBACK_PRESET_CUSTOM = '__custom__'
const markOptions = computed(() => [
  { label: t('knowledgeEditor.wikiKeywords.presetMeaningful'), value: 'kw_meaningful' },
  { label: t('knowledgeEditor.wikiKeywords.presetNoise'), value: 'kw_noise' },
  { label: t('knowledgeEditor.wikiKeywords.presetMisExtracted'), value: 'kw_mis_extracted' },
  { label: t('knowledgeEditor.wikiKeywords.feedbackPresetCustom'), value: FEEDBACK_PRESET_CUSTOM },
])
const markDialog = reactive({
  visible: false,
  submitting: false,
  slug: '',
  keyword: '',
  preset: '',
  content: '',
})

function hasMarks(item: WikiKeywordStat): boolean {
  const c = item.feedback_counts
  return !!c && ((c.kw_meaningful || 0) + (c.kw_noise || 0) + (c.kw_mis_extracted || 0)) > 0
}

function openMarkDialog(item: WikiKeywordStat) {
  markDialog.slug = item.slug
  markDialog.keyword = item.keyword
  markDialog.preset = ''
  markDialog.content = ''
  markDialog.visible = true
}

function resetMarkDialog() {
  markDialog.content = ''
  markDialog.submitting = false
}

async function submitMark() {
  const slug = markDialog.slug
  if (!slug) return
  markDialog.submitting = true
  try {
    if (markDialog.preset === FEEDBACK_PRESET_CUSTOM) {
      const content = markDialog.content.trim()
      if (!content) {
        MessagePlugin.warning(t('knowledgeEditor.wikiKeywords.markContentRequired'))
        markDialog.submitting = false
        return
      }
      await createWikiFeedback(props.knowledgeBaseId, { slug, feedback_type: 'comment', content })
    } else if (markDialog.preset) {
      await createWikiFeedback(props.knowledgeBaseId, { slug, feedback_type: 'question', preset: markDialog.preset })
    } else {
      MessagePlugin.warning(t('knowledgeEditor.wikiKeywords.markRequired'))
      markDialog.submitting = false
      return
    }
    MessagePlugin.success(t('knowledgeEditor.wikiKeywords.markSubmitted'))
    markDialog.visible = false
    resetMarkDialog()
    // 刷新行内标注计数（保持当前页）
    reload(currentPage.value)
  } catch (e) {
    console.error('Failed to submit keyword mark:', e)
    MessagePlugin.error(t('knowledgeEditor.wikiKeywords.markSubmitFailed'))
  } finally {
    markDialog.submitting = false
  }
}

// --- Detail drawer (clicking the total frequency opens the keyword page here) ---

const drawerVisible = ref(false)
const drawerLoading = ref(false)
const drawerTitle = ref('')
const drawerContent = ref('')

// renderDrawerMarkdown renders the keyword page content inside the drawer.
// [[wikilink|display]] links are flattened to their display text — the drawer
// is for reading, navigation keeps happening through the overview rows.
function renderDrawerMarkdown(md: string): string {
  const plain = md
    .replace(/\[\[[^\]\n]*\|([^\]]*)\]\]/g, '$1')
    .replace(/\[\[[^\]\n]*\]\]/g, '$1')
  return sanitizeMarkdownHTML(marked.parse(plain) as string)
}

async function openDrawer(item: WikiKeywordStat) {
  drawerTitle.value = item.keyword
  drawerContent.value = ''
  drawerLoading.value = true
  drawerVisible.value = true
  try {
    const res: any = await getWikiPage(props.knowledgeBaseId, item.slug)
    const page = res?.data || res || {}
    drawerContent.value = renderDrawerMarkdown(page.content || '')
  } catch (e) {
    console.error('Failed to load keyword page:', e)
    drawerContent.value = ''
  } finally {
    drawerLoading.value = false
  }
}

// Reset to page 1 and reload whenever the KB changes.
watch(() => props.knowledgeBaseId, () => {
  search.value = ''
  sortMode.value = 'freq_desc'
  reload(1)
})

onMounted(() => reload(1))
</script>

<style scoped lang="less">
.wiki-keywords {
  display: flex;
  flex-direction: column;
  height: 100%;
  overflow: hidden;
  padding: 16px 20px;
  box-sizing: border-box;
}

.wiki-keywords-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 0 12px 0;

  .wiki-keywords-title {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 16px;
    font-weight: 600;
    color: var(--td-text-color-primary);

    .wk-title-icon {
      color: var(--td-brand-color);
    }
  }

  .wk-summary-chip {
    font-size: 12px;
    color: var(--td-text-color-placeholder);
    background: var(--td-bg-color-container);
    border: 1px solid var(--td-component-border);
    border-radius: 12px;
    padding: 2px 10px;
  }
}

.wiki-keywords-toolbar {
  display: flex;
  align-items: center;
  gap: 12px;
  padding-bottom: 12px;

  .wk-search {
    width: 280px;
  }

  .wk-sort-select {
    width: 160px;
  }
}

.wiki-keywords-body {
  flex: 1;
  overflow-y: auto;
  min-height: 0;
}

.wk-loading {
  display: flex;
  justify-content: center;
  padding: 48px 0;
}

.wk-empty {
  text-align: center;
  padding: 64px 0;
  color: var(--td-text-color-placeholder);

  .wk-empty-icon {
    font-size: 0;
    margin-bottom: 12px;
  }

  .wk-empty-title {
    font-size: 14px;
    color: var(--td-text-color-secondary);
    margin-bottom: 4px;
  }

  .wk-empty-desc {
    font-size: 12px;
  }
}

.wk-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;

  thead th {
    position: sticky;
    top: 0;
    z-index: 1;
    background: var(--td-bg-color-container);
    text-align: left;
    font-weight: 500;
    color: var(--td-text-color-secondary);
    padding: 8px 12px;
    border-bottom: 1px solid var(--td-component-border);
    white-space: nowrap;
  }

  tbody td {
    padding: 10px 12px;
    border-bottom: 1px solid var(--td-component-stroke);
    vertical-align: middle;
  }

  .wk-row {
    cursor: pointer;
    transition: background-color 0.15s ease;

    &:hover {
      background: var(--td-bg-color-container-hover);
    }
  }

  .wk-col-rank {
    width: 80px;
    white-space: nowrap;
  }

  .wk-rank-badge {
    display: inline-block;
    min-width: 22px;
    text-align: center;
    padding: 1px 6px;
    border-radius: 10px;
    background: var(--td-bg-color-component);
    color: var(--td-text-color-secondary);
    font-size: 12px;

    &.wk-rank-top {
      background: var(--td-brand-color-light);
      color: var(--td-brand-color);
      font-weight: 600;
    }
  }

  .wk-col-keyword {
    width: 22%;

    .wk-keyword-name {
      font-weight: 500;
      color: var(--td-text-color-primary);
    }
  }

  .wk-col-meaning {
    width: 38%;

    .wk-meaning {
      display: block;
      color: var(--td-text-color-secondary);
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }

    .wk-meaning-empty {
      color: var(--td-text-color-placeholder);
    }
  }

  .wk-col-num {
    width: 110px;
    white-space: nowrap;
    color: var(--td-text-color-secondary);
  }

  .wk-col-mark {
    width: 200px;
    white-space: nowrap;

    .wk-mark-cell {
      display: flex;
      align-items: center;
      gap: 10px;
    }

    .wk-mark-btn {
      display: inline-flex;
      align-items: center;
      gap: 4px;
      border: 1px solid var(--td-component-border);
      border-radius: 6px;
      background: var(--td-bg-color-container);
      color: var(--td-text-color-secondary);
      padding: 3px 8px;
      font-size: 12px;
      cursor: pointer;
      transition: all 0.2s ease;

      &:hover {
        color: var(--td-brand-color);
        border-color: var(--td-brand-color);
      }
    }

    .wk-mark-counts {
      display: inline-flex;
      align-items: center;
      gap: 6px;
    }

    .wk-mark-count {
      font-size: 12px;
      padding: 1px 6px;
      border-radius: 10px;
      background: var(--td-bg-color-component);
      color: var(--td-text-color-secondary);
    }

    .wk-mark-ok {
      background: var(--td-success-color-light);
      color: var(--td-success-color);
    }

    .wk-mark-no {
      background: var(--td-error-color-light);
      color: var(--td-error-color);
    }

    .wk-mark-warn {
      background: var(--td-warning-color-light);
      color: var(--td-warning-color);
    }
  }

  .wk-freq-link {
    border: none;
    background: none;
    padding: 0;
    cursor: pointer;
    line-height: inherit;

    &:hover .wk-freq {
      text-decoration: underline;
    }
  }

  .wk-freq {
    font-weight: 600;
    color: var(--td-brand-color);
  }
}

.wk-pagination {
  display: flex;
  justify-content: flex-end;
  padding-top: 12px;
}

.wk-drawer-loading {
  display: flex;
  justify-content: center;
  padding: 48px 0;
}

.wk-drawer-empty {
  text-align: center;
  padding: 48px 0;
  color: var(--td-text-color-placeholder);
}

.wk-drawer-content {
  font-size: 13px;
  line-height: 1.7;
  color: var(--td-text-color-primary);
  word-break: break-word;

  :deep(table) {
    border-collapse: collapse;
    width: 100%;

    th, td {
      border: 1px solid var(--td-component-border);
      padding: 6px 10px;
      font-size: 12px;
    }

    th {
      background: var(--td-bg-color-component);
    }
  }
}

.wk-mark-form {
  display: flex;
  flex-direction: column;
  gap: 14px;

  .wk-mark-row {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  .wk-mark-label {
    font-size: 12px;
    color: var(--td-text-color-secondary);
  }

  .wk-mark-word {
    font-size: 14px;
    font-weight: 600;
    color: var(--td-text-color-primary);
  }

  .wk-mark-custom {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  .wk-mark-hint {
    font-size: 12px;
    color: var(--td-text-color-placeholder);
  }
}
</style>