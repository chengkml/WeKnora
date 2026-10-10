<script setup lang="ts">
/**
 * DocFolderMoveDialog — 把一个或多个文档移动到指定目录。
 *
 * 单归属语义：选中目录即替换文档的 folder_id；选「根层级」= 移出所有目录。
 * 复用列表页的目录树（只读），提交走 moveDocumentsToFolder。
 */
import { computed, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { listDocFolders, moveDocumentsToFolder, type DocFolderNode } from '@/api/knowledge-base';

const props = defineProps<{
  visible: boolean;
  kbId: string;
  /** 待移动的文档 id 列表（单文档 = 1 个，批量 = 多个） */
  knowledgeIds: string[];
}>();

const emit = defineEmits<{
  (e: 'update:visible', value: boolean): void;
  (e: 'moved'): void;
  (e: 'error', message: string): void;
}>();

const { t } = useI18n();

const tree = ref<DocFolderNode[]>([]);
const expanded = ref<Set<string>>(new Set());
const loading = ref(false);
const submitting = ref(false);
const selectedId = ref(''); // "" = 根层级

const visible = computed({
  get: () => props.visible,
  set: (v) => emit('update:visible', v),
});

const rows = computed(() => {
  const out: DocFolderNode[] = [];
  const walk = (nodes: DocFolderNode[]) => {
    for (const n of nodes) {
      out.push(n);
      if (n.folders?.length && expanded.value.has(n.id)) walk(n.folders);
    }
  };
  walk(tree.value);
  return out;
});

const load = async () => {
  if (!props.kbId) return;
  loading.value = true;
  try {
    const res: any = await listDocFolders(props.kbId);
    const data = (res?.data ?? res ?? {}) as { folders?: DocFolderNode[] };
    tree.value = data.folders || [];
  } catch (e) {
    emit('error', String((e as any)?.message ?? e));
  } finally {
    loading.value = false;
  }
};

watch(() => props.visible, (v) => {
  if (v) {
    selectedId.value = '';
    load();
  }
});

const toggle = (id: string) => {
  const next = new Set(expanded.value);
  if (next.has(id)) next.delete(id);
  else next.add(id);
  expanded.value = next;
};

const confirm = async () => {
  if (!props.knowledgeIds.length) return;
  submitting.value = true;
  try {
    await moveDocumentsToFolder(props.kbId, props.knowledgeIds, selectedId.value);
    visible.value = false;
    emit('moved');
  } catch (e: any) {
    emit('error', e?.message || String(e));
  } finally {
    submitting.value = false;
  }
};
</script>

<template>
  <t-dialog
    v-model:visible="visible"
    :header="t('knowledgeBase.folderMoveTitle')"
    :confirm-btn="{ content: t('common.confirm'), theme: 'primary', loading: submitting }"
    :cancel-btn="{ content: t('common.cancel') }"
    width="420px"
    @confirm="confirm"
  >
    <div class="doc-folder-move">
      <div v-if="loading" class="doc-folder-move__placeholder">
        <t-skeleton animation="gradient" :row-col="[{ width: '100%', height: '24px' }]" />
      </div>
      <div class="doc-folder-move__tree">
        <!-- root option: move out of any folder -->
        <div class="doc-folder-move-row"
          :class="{ 'is-selected': selectedId === '' }" @click="selectedId = ''">
          <t-icon name="folder-open" size="16px" />
          <span class="doc-folder-move-row__name">{{ $t('knowledgeBase.folderMoveToRoot') }}</span>
        </div>
        <div v-for="row in rows" :key="row.id" class="doc-folder-move-row"
          :class="{ 'is-selected': selectedId === row.id }"
          @click="selectedId = row.id">
          <span class="doc-folder-move-row__depth" :style="{ width: `${row.depth * 14}px` }" />
          <span v-if="row.folders?.length" class="doc-folder-move-row__caret" @click.stop="toggle(row.id)">
            <t-icon :name="expanded.has(row.id) ? 'chevron-down' : 'chevron-right'" size="14px" />
          </span>
          <span v-else class="doc-folder-move-row__caret doc-folder-move-row__caret--leaf" />
          <t-icon name="folder" size="16px" />
          <span class="doc-folder-move-row__name">{{ row.name }}</span>
          <span class="doc-folder-move-row__count">{{ row.doc_count || 0 }}</span>
        </div>
        <div v-if="!tree.length && !loading" class="doc-folder-move__empty">
          {{ $t('knowledgeBase.folderEmpty') }}
        </div>
      </div>
    </div>
  </t-dialog>
</template>

<style scoped>
.doc-folder-move {
  min-height: 180px;
  max-height: 320px;
  overflow-y: auto;
}
.doc-folder-move-row {
  display: flex;
  align-items: center;
  gap: 6px;
  height: 30px;
  padding: 0 6px;
  border-radius: 4px;
  cursor: pointer;
}
.doc-folder-move-row:hover {
  background: var(--td-bg-color-container-hover);
}
.doc-folder-move-row.is-selected {
  background: var(--td-brand-color-light);
  color: var(--td-brand-color);
}
.doc-folder-move-row__depth {
  flex: 0 0 auto;
}
.doc-folder-move-row__caret {
  display: inline-flex;
  width: 16px;
  justify-content: center;
  color: var(--td-text-color-secondary);
}
.doc-folder-move-row__caret--leaf {
  color: transparent;
}
.doc-folder-move-row__name {
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 13px;
}
.doc-folder-move-row__count {
  flex: 0 0 auto;
  font-size: 11px;
  color: var(--td-text-color-placeholder);
}
.doc-folder-move__empty,
.doc-folder-move__placeholder {
  padding: 16px;
  text-align: center;
  font-size: 12px;
  color: var(--td-text-color-placeholder);
}
</style>