<script setup lang="ts">
/**
 * DocFolderTree — 知识库文档的多级目录树（单归属 + 递归子树浏览）。
 *
 * 能力（与后端 doc_folders，迁移 000079 对齐）：
 *  - 树形浏览，任意层级。先把嵌套树按展开状态"拍平"成缩进行，天然支持
 *    无限深度，避免递归组件/多层嵌套模板。
 *  - 每目录显示递归文档数（doc_count 为整棵子树，后端已算好）
 *  - 新建根目录 / 新建子目录（行内输入框）
 *  - 重命名（行内输入）、删除（后端拒绝非空目录）
 *  - 点击目录 → emit('select', node)，父级据此递归筛选文档列表
 *
 * props.modelValue = 当前选中目录 id（"" = 根/全部），由父级驱动。
 */
import { computed, onMounted, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import {
  listDocFolders,
  createDocFolder,
  updateDocFolder,
  deleteDocFolder,
  type DocFolderNode,
} from '@/api/knowledge-base';

const props = defineProps<{
  kbId: string;
  canEdit: boolean;
  /** 当前选中的目录 id（"" = 根/全部），由父级驱动 */
  modelValue: string;
}>();

const emit = defineEmits<{
  (e: 'update:modelValue', value: string): void;
  (e: 'select', folder: DocFolderNode | null): void;
  (e: 'error', message: string): void;
  (e: 'changed'): void;
}>();

const { t } = useI18n();

const loading = ref(false);
const tree = ref<DocFolderNode[]>([]);
const expanded = ref<Set<string>>(new Set());
const creatingAt = ref<string | null>(null);
const newName = ref('');
const editingId = ref('');
const editingName = ref('');
const busy = ref(false);
const pendingDelete = ref<DocFolderNode | null>(null);

const load = async () => {
  if (!props.kbId) return;
  loading.value = true;
  try {
    const res: any = await listDocFolders(props.kbId);
    const data = (res?.data ?? res ?? {}) as { folders?: DocFolderNode[] };
    tree.value = data.folders || [];
    if (props.modelValue) {
      expanded.value.add(props.modelValue);
    }
  } catch (e) {
    console.error('Failed to load doc folders', e);
    emit('error', String((e as any)?.message ?? e));
  } finally {
    loading.value = false;
  }
};

watch(() => props.kbId, () => {
  tree.value = [];
  expanded.value = new Set();
  load();
}, { immediate: true });

onMounted(load);

// --- flatten: tree -> visible rows respecting expansion ---
interface FolderRow extends DocFolderNode {
  visible: boolean;
}

const rows = computed<FolderRow[]>(() => {
  const out: FolderRow[] = [];
  const walk = (nodes: DocFolderNode[], visible: boolean) => {
    for (const n of nodes) {
      out.push({ ...n, visible });
      if (n.folders?.length && expanded.value.has(n.id)) {
        walk(n.folders, visible && expanded.value.has(n.id));
      }
    }
  };
  walk(tree.value, true);
  return out;
});

const toggle = (id: string) => {
  const next = new Set(expanded.value);
  if (next.has(id)) next.delete(id);
  else next.add(id);
  expanded.value = next;
};

const selectFolder = (node: DocFolderNode) => {
  emit('update:modelValue', node.id);
  emit('select', node);
};

const selectRoot = () => {
  emit('update:modelValue', '');
  emit('select', null);
};

// --- creation ---
const openCreate = (parentId: string) => {
  creatingAt.value = parentId;
  newName.value = '';
};

const commitCreate = async (parentId: string) => {
  const name = newName.value.trim();
  if (!name) return;
  busy.value = true;
  try {
    await createDocFolder(props.kbId, { parent_id: parentId || undefined, name });
    if (parentId) expanded.value.add(parentId);
    creatingAt.value = null;
    await load();
    emit('changed');
  } catch (e: any) {
    emit('error', e?.message || String(e));
  } finally {
    busy.value = false;
  }
};

// --- rename ---
const startRename = (node: DocFolderNode) => {
  editingId.value = node.id;
  editingName.value = node.name;
};

const commitRename = async () => {
  const id = editingId.value;
  const name = editingName.value.trim();
  editingId.value = '';
  if (!id || !name) return;
  busy.value = true;
  try {
    await updateDocFolder(props.kbId, id, { name });
    await load();
    emit('changed');
  } catch (e: any) {
    emit('error', e?.message || String(e));
  } finally {
    busy.value = false;
  }
};

// --- delete ---
const confirmDelete = async () => {
  const node = pendingDelete.value;
  pendingDelete.value = null;
  if (!node) return;
  busy.value = true;
  try {
    await deleteDocFolder(props.kbId, node.id);
    if (props.modelValue === node.id) selectRoot();
    await load();
    emit('changed');
  } catch (e: any) {
    emit('error', e?.message || String(e));
  } finally {
    busy.value = false;
  }
};

const onNodeAction = (action: string, node: DocFolderNode) => {
  if (action === 'add') openCreate(node.id);
  else if (action === 'rename') startRename(node);
  else if (action === 'delete') pendingDelete.value = node;
};
</script>

<template>
  <div class="doc-folder-tree">
    <div class="doc-folder-tree__toolbar">
      <button v-if="canEdit" type="button" class="doc-folder-tree__new-root" :disabled="busy" @click="openCreate('')">
        <t-icon name="folder-add" size="14px" />
        <span>{{ $t('knowledgeBase.folderCreateRoot') }}</span>
      </button>
    </div>

    <div class="doc-folder-tree__body">
      <div v-if="loading && !rows.length" class="doc-folder-tree__placeholder">
        <t-skeleton animation="gradient" :row-col="[{ width: '100%', height: '22px' }]" />
      </div>

      <div v-else-if="!rows.length && !creatingAt" class="doc-folder-tree__empty">
        {{ $t('knowledgeBase.folderEmpty') }}
      </div>

      <!-- root create row -->
      <div v-if="creatingAt === ''" class="doc-folder-row doc-folder-row--create">
        <t-input v-model="newName" size="small" autofocus
          :placeholder="$t('knowledgeBase.folderNewPlaceholder')"
          @keydown.enter="commitCreate('')" @blur="commitCreate('')" />
      </div>

      <template v-for="row in rows" :key="row.id">
        <div v-if="row.visible" class="doc-folder-row"
          :style="{ '--indent': `${(row.depth - 1) * 14}px` }"
          :class="{ 'is-selected': modelValue === row.id }">
          <span class="doc-folder-row__depth" :style="{ width: `${(row.depth - 1) * 14}px` }" />
          <span v-if="row.folders?.length" class="doc-folder-row__caret" @click="toggle(row.id)">
            <t-icon :name="expanded.has(row.id) ? 'chevron-down' : 'chevron-right'" size="14px" />
          </span>
          <span v-else class="doc-folder-row__caret doc-folder-row__caret--leaf" />

          <template v-if="editingId !== row.id">
            <t-popconfirm v-if="pendingDelete?.id === row.id" theme="warning"
              :content="$t('knowledgeBase.folderDeleteDesc', { name: row.name })"
              :visible="true"
              :confirm-btn="{ content: $t('common.confirm'), theme: 'danger' }"
              :cancel-btn="{ content: $t('common.cancel') }"
              @confirm="confirmDelete" @cancel="pendingDelete = null">
              <div class="doc-folder-row__main" @click.stop>
                <t-icon name="folder" size="16px" />
                <span class="doc-folder-row__name" @click="selectFolder(row)">{{ row.name }}</span>
                <span class="doc-folder-row__count">{{ row.doc_count || 0 }}</span>
              </div>
            </t-popconfirm>
            <div v-else class="doc-folder-row__main" @click.stop>
              <t-icon name="folder" size="16px" />
              <span class="doc-folder-row__name" @click="selectFolder(row)">{{ row.name }}</span>
              <span class="doc-folder-row__count">{{ row.doc_count || 0 }}</span>
              <div v-if="canEdit" class="doc-folder-row__actions" @click.stop>
                <t-dropdown :options="[
                  { content: $t('knowledgeBase.folderCreateChild'), value: 'add' },
                  { content: $t('common.rename'), value: 'rename' },
                  { content: $t('common.delete'), value: 'delete' },
                ]" placement="bottom-right" @click="(opt: any) => onNodeAction(opt.value, row)">
                  <button type="button" class="doc-folder-row__more"><t-icon name="more" size="14px" /></button>
                </t-dropdown>
              </div>
            </div>
          </template>
          <t-input v-else v-model="editingName" size="small" autofocus class="doc-folder-row__rename"
            :placeholder="$t('knowledgeBase.folderNamePlaceholder')"
            @keydown.enter="commitRename" @blur="commitRename" @click.stop />
        </div>

        <div v-if="creatingAt === row.id" class="doc-folder-row doc-folder-row--create">
          <span class="doc-folder-row__depth" :style="{ width: `${row.depth * 14}px` }" />
          <t-input v-model="newName" size="small" autofocus
            :placeholder="$t('knowledgeBase.folderNewPlaceholder')"
            @keydown.enter="commitCreate(row.id)" @blur="commitCreate(row.id)" />
        </div>
      </template>
    </div>
  </div>
</template>
<style scoped>
.doc-folder-tree {
  display: flex;
  flex-direction: column;
  min-height: 0;
  width: 100%;
}
.doc-folder-tree__toolbar {
  display: flex;
  justify-content: flex-start;
  padding-bottom: 4px;
}
.doc-folder-tree__new-root {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 12px;
  color: var(--td-brand-color);
  background: transparent;
  border: none;
  cursor: pointer;
  padding: 2px 4px;
}
.doc-folder-tree__empty,
.doc-folder-tree__placeholder {
  padding: 16px 4px;
  font-size: 12px;
  color: var(--td-text-color-placeholder);
  text-align: center;
}
.doc-folder-tree__body {
  max-height: 320px;
  overflow-y: auto;
}
.doc-folder-row {
  display: flex;
  align-items: center;
  padding: 0 2px;
  border-radius: 4px;
  height: 28px;
}
.doc-folder-row:hover {
  background: var(--td-bg-color-container-hover);
}
.doc-folder-row.is-selected {
  background: var(--td-brand-color-light);
}
.doc-folder-row__depth {
  flex: 0 0 auto;
}
.doc-folder-row__caret {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 18px;
  height: 100%;
  cursor: pointer;
  color: var(--td-text-color-secondary);
  flex: 0 0 auto;
}
.doc-folder-row__caret--leaf {
  cursor: default;
  color: transparent;
}
.doc-folder-row__main {
  display: flex;
  align-items: center;
  gap: 4px;
  flex: 1 1 auto;
  min-width: 0;
  color: var(--td-text-color-primary);
  cursor: pointer;
}
.doc-folder-row__main > .t-icon {
  flex: 0 0 auto;
  color: var(--td-brand-color);
}
.doc-folder-row__name {
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 13px;
}
.doc-folder-row__count {
  flex: 0 0 auto;
  font-size: 11px;
  color: var(--td-text-color-placeholder);
}
.doc-folder-row__actions {
  flex: 0 0 auto;
  opacity: 0;
}
.doc-folder-row:hover .doc-folder-row__actions {
  opacity: 1;
}
.doc-folder-row__more {
  background: transparent;
  border: none;
  cursor: pointer;
  color: var(--td-text-color-secondary);
  padding: 0 2px;
  display: inline-flex;
  align-items: center;
}
.doc-folder-row__rename {
  flex: 1 1 auto;
}
.doc-folder-row--create {
  padding: 2px 2px 2px 14px;
}
</style>
