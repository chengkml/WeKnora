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
        </t-table>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue';
import { Message, DialogPlugin } from 'tdesign-vue-next';
import { listSkills, uploadSkill, type SkillInfo } from '@/api/skill';

const skills = ref<SkillInfo[]>([]);
const loading = ref(false);
const uploading = ref(false);
const skillsAvailable = ref(true);
const fileInputRef = ref<HTMLInputElement | null>(null);

const columns = computed(() => [
  { colKey: 'name', title: '名称', ellipsis: true, cell: (h: any, { row }: any) => h('div', { class: 'skill-name-cell' }, [
      h('t-icon', { name: 'file-code', size: '18px', class: 'skill-name-icon' }),
      h('span', { class: 'skill-name-text' }, row.name),
    ]) },
  { colKey: 'description', title: '描述', ellipsis: true },
]);

async function loadSkills() {
  loading.value = true;
  try {
    const resp = await listSkills() as any;
    const data = resp?.data ?? resp;
    skillsAvailable.value = data?.skills_available !== false;
    skills.value = Array.isArray(data?.data) ? data.data : [];
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
</style>