<template>
  <div class="skill-manage-container">
    <div class="skill-manage-header" style="--wails-draggable: drag">
      <div class="skill-manage-title-row" style="--wails-draggable: no-drag">
        <h2>{{ $t('menu.skills') }}</h2>
        <t-tooltip :content="$t('skillManage.uploadHint')" placement="bottom">
          <t-button theme="primary" variant="outline" size="small" :disabled="uploading" @click="triggerFileInput">
            <template #icon><t-icon name="upload" size="16px" /></template>
            {{ uploading ? $t('skillManage.uploading') : $t('skillManage.upload') }}
          </t-button>
        </t-tooltip>
      </div>
      <p class="skill-manage-subtitle">{{ $t('skillManage.subtitle') }}</p>
      <input ref="fileInputRef" type="file" accept=".zip" style="display:none" @change="handleFileChange" />
    </div>

    <div class="skill-manage-body">
      <div v-if="loading" class="skill-manage-loading">
        <t-loading :loading="true" size="large" :text="$t('skillManage.loading')" />
      </div>

      <div v-else-if="!skillsAvailable" class="skill-manage-empty">
        <div class="skill-manage-empty-icon"><t-icon name="tools" size="48px" /></div>
        <p class="skill-manage-empty-title">{{ $t('skillManage.disabledTitle') }}</p>
        <p class="skill-manage-empty-desc">{{ $t('skillManage.disabledDesc') }}</p>
      </div>

      <div v-else class="skill-list-wrapper">
        <div v-if="skills.length === 0" class="skill-manage-empty">
          <div class="skill-manage-empty-icon"><t-icon name="tools" size="48px" /></div>
          <p class="skill-manage-empty-title">{{ $t('skillManage.emptyTitle') }}</p>
          <p class="skill-manage-empty-desc">{{ $t('skillManage.emptyDesc') }}</p>
        </div>

        <t-table
          v-else
          :data="skills"
          :columns="columns"
          row-key="name"
          :hover="true"
          :pagination="{ pageSize: 20 }"
          :loading="loading"
        >
          <template #name="{ row }">
            <div class="skill-name-cell">
              <t-icon name="file-code" size="18px" class="skill-name-icon" />
              <span class="skill-name-text">{{ row.name }}</span>
            </div>
          </template>
          <template #description="{ row }">
            <span class="skill-desc-text">{{ row.description || '-' }}</span>
          </template>
          <template #actions="{ row }">
            <t-space :size="4">
              <t-button variant="text" theme="primary" size="small" @click="handleViewDetail(row.name)">
                {{ $t('skillManage.viewDetail') }}
              </t-button>
              <t-popconfirm :content="$t('skillManage.deleteConfirm', { name: row.name })" theme="danger" @confirm="handleDelete(row.name)">
                <t-button variant="text" theme="danger" size="small">
                  {{ $t('skillManage.delete') }}
                </t-button>
              </t-popconfirm>
            </t-space>
          </template>
        </t-table>
      </div>
    </div>

    <!-- 技能详情弹窗 -->
    <t-dialog v-model:visible="detailVisible" :header="$t('skillManage.detailTitle')" width="720px" :footer="false">
      <div v-if="detailLoading" class="skill-detail-loading">
        <t-loading :loading="true" size="small" :text="$t('skillManage.loading')" />
      </div>
      <div v-else-if="detail" class="skill-detail">
        <div class="skill-detail-head">
          <div class="skill-detail-name">
            <t-icon name="file-code" size="20px" class="skill-name-icon" />
            <span>{{ detail.name }}</span>
          </div>
          <p class="skill-detail-desc">{{ detail.description || '-' }}</p>
        </div>
        <p class="skill-detail-count">{{ $t('skillManage.fileCount', { n: detail.total_files }) }}</p>
        <div class="skill-detail-files">
          <div v-for="f in detail.files" :key="f.path" class="skill-detail-file">
            <t-icon name="file" size="14px" />
            <span class="skill-detail-file-path">{{ f.path }}</span>
            <span class="skill-detail-file-size">{{ formatSize(f.size) }}</span>
          </div>
          <div v-if="detail.files.length === 0" class="skill-detail-empty">{{ $t('skillManage.noFiles') }}</div>
        </div>
      </div>
    </t-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue';
import { Message } from 'tdesign-vue-next';
import { listSkills, uploadSkill, getSkillDetail, deleteSkill, type SkillInfo, type SkillDetail } from '@/api/skill';

const skills = ref<SkillInfo[]>([]);
const loading = ref(false);
const uploading = ref(false);
const skillsAvailable = ref(true);
const fileInputRef = ref<HTMLInputElement | null>(null);

const detailVisible = ref(false);
const detailLoading = ref(false);
const detail = ref<SkillDetail | null>(null);

const columns = computed(() => [
  { colKey: 'name', title: '名称', ellipsis: true, cell: (h: any, { row }: any) => h('div', { class: 'skill-name-cell' }, [
      h('t-icon', { name: 'file-code', size: '18px', class: 'skill-name-icon' }),
      h('span', { class: 'skill-name-text' }, row.name),
    ]) },
  { colKey: 'description', title: '描述', ellipsis: true },
  { colKey: 'actions', title: '操作', width: 160 },
]);

async function loadSkills() {
  loading.value = true;
  try {
    const resp = await listSkills() as any;
    // 响应结构: { data: SkillInfo[], skills_available: bool, success: bool }
    // data 直接是技能数组（顶层平级），不是两层嵌套
    skillsAvailable.value = resp?.skills_available !== false;
    skills.value = Array.isArray(resp?.data) ? resp.data : [];
  } catch (e: any) {
    Message.error(e?.message || '获取技能列表失败');
    skills.value = [];
  } finally {
    loading.value = false;
  }
}

function triggerFileInput() {
  fileInputRef.value?.click();
}

async function handleFileChange(e: Event) {
  const input = e.target as HTMLInputElement;
  const file = input.files?.[0];
  if (!file) return;

  if (!file.name.toLowerCase().endsWith('.zip')) {
    Message.warning('请选择 ZIP 格式的技能包');
    input.value = '';
    return;
  }

  uploading.value = true;
  try {
    const resp = await uploadSkill(file);
    const ok = resp?.success !== false;
    if (ok) {
      Message.success(`技能「${file.name}」上传安装成功`);
      input.value = '';
      await loadSkills();
    } else {
      Message.error(resp?.message || '上传失败');
    }
  } catch (e: any) {
    Message.error(e?.message || '上传失败，请检查技能包格式（需包含 SKILL.md）');
  } finally {
    uploading.value = false;
    input.value = '';
  }
}

async function handleViewDetail(name: string) {
  detailVisible.value = true;
  detailLoading.value = true;
  detail.value = null;
  try {
    const resp = await getSkillDetail(name) as any;
    detail.value = resp?.data ?? resp ?? null;
  } catch (e: any) {
    Message.error(e?.message || '获取技能详情失败');
    detail.value = null;
  } finally {
    detailLoading.value = false;
  }
}

async function handleDelete(name: string) {
  try {
    const resp = await deleteSkill(name) as any;
    Message.success(`技能「${name}」已删除`);
    await loadSkills();
    if (detail.value?.name === name) {
      detailVisible.value = false;
      detail.value = null;
    }
  } catch (e: any) {
    Message.error(e?.message || '删除失败');
  }
}

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(2)} MB`;
}

onMounted(loadSkills);
</script>

<style scoped>
.skill-manage-container {
  height: 100%;
  display: flex;
  flex-direction: column;
  padding: 20px 24px;
  box-sizing: border-box;
  overflow: hidden;
}

.skill-manage-header {
  flex-shrink: 0;
  padding-bottom: 16px;
  border-bottom: 1px solid var(--td-component-stroke, #e5e6eb);
}

.skill-manage-title-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.skill-manage-title-row h2 {
  margin: 0;
  font-size: 20px;
  font-weight: 600;
}

.skill-manage-subtitle {
  margin: 6px 0 0;
  font-size: 13px;
  color: var(--td-text-color-secondary, #6b7280);
}

.skill-manage-body {
  flex: 1;
  overflow: auto;
  padding-top: 16px;
}

.skill-manage-loading {
  display: flex;
  justify-content: center;
  padding-top: 80px;
}

.skill-manage-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  padding-top: 80px;
  color: var(--td-text-color-secondary, #6b7280);
}

.skill-manage-empty-icon {
  color: var(--td-brand-color, #0052d9);
}

.skill-manage-empty-title {
  font-size: 16px;
  font-weight: 500;
  margin: 12px 0 4px;
}

.skill-manage-empty-desc {
  font-size: 13px;
}

.skill-name-cell {
  display: flex;
  align-items: center;
  gap: 8px;
}

.skill-name-icon {
  color: var(--td-brand-color, #0052d9);
}

.skill-name-text {
  font-weight: 500;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}

.skill-desc-text {
  color: var(--td-text-color-secondary, #6b7280);
  font-size: 13px;
}

.skill-detail-loading {
  display: flex;
  justify-content: center;
  padding: 40px 0;
}

.skill-detail-head {
  border-bottom: 1px solid var(--td-component-stroke, #e5e6eb);
  padding-bottom: 12px;
  margin-bottom: 12px;
}

.skill-detail-name {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 18px;
  font-weight: 600;
}

.skill-detail-desc {
  margin: 8px 0 0;
  font-size: 13px;
  color: var(--td-text-color-secondary, #6b7280);
}

.skill-detail-count {
  font-size: 13px;
  color: var(--td-text-color-secondary, #6b7280);
  margin: 8px 0;
}

.skill-detail-files {
  max-height: 380px;
  overflow: auto;
  border: 1px solid var(--td-component-stroke, #e5e6eb);
  border-radius: 6px;
}

.skill-detail-file {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 12px;
  font-size: 13px;
  border-bottom: 1px solid var(--td-component-stroke, #f0f0f0);
}

.skill-detail-file:last-child {
  border-bottom: none;
}

.skill-detail-file-path {
  flex: 1;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  word-break: break-all;
}

.skill-detail-file-size {
  color: var(--td-text-color-secondary, #6b7280);
  white-space: nowrap;
}

.skill-detail-empty {
  padding: 24px;
  text-align: center;
  color: var(--td-text-color-secondary, #6b7280);
}
</style>