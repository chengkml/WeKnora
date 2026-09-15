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
              <th class="wk-col-num">{{ $t('knowledgeEditor.wikiKeywords.freq') }}</th>
              <th class="wk-col-num">{{ $t('knowledgeEditor.wikiKeywords.docCount') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="(item, idx) in items" :key="item.slug" class="wk-row" @click="openPage(item.slug)">
              <td class="wk-col-rank">
                <span class="wk-rank-badge" :class="{ 'wk-rank-top': idx < 3 }">{{ (currentPage - 1) * pageSize + idx + 1 }}</span>
              </td>
              <td class="wk-col-keyword">
                <span class="wk-keyword-name">{{ item.keyword }}</span>
                <span class="wk-keyword-slug">{{ item.slug }}</span>
              </td>
              <td class="wk-col-num"><span class="wk-freq">{{ item.total_freq }}</span></td>
              <td class="wk-col-num">{{ item.doc_count }}</td>
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
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { listWikiKeywords, type WikiKeywordStat } from '@/api/wiki'

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
    .wk-keyword-name {
      font-weight: 500;
      color: var(--td-text-color-primary);
      margin-right: 8px;
    }

    .wk-keyword-slug {
      font-size: 12px;
      color: var(--td-text-color-placeholder);
    }
  }

  .wk-col-num {
    width: 120px;
    white-space: nowrap;
    color: var(--td-text-color-secondary);
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
</style>