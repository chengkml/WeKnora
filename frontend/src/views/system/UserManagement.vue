<template>
  <div class="user-management">
    <div class="section-header">
      <h2>{{ t('system.globalSettings.sections.userManagement.title') }}</h2>
      <p class="section-description">
        {{ t('system.globalSettings.sections.userManagement.description') }}
      </p>
    </div>

    <div v-if="loading && users.length === 0" class="loading-state">
      <t-loading :text="t('system.globalSettings.sections.userManagement.table.loading')" />
    </div>

    <div v-else-if="error" class="error-inline">
      <t-alert theme="error" :message="error">
        <template #operation>
          <t-button size="small" @click="loadUsers">{{ t('system.globalSettings.runtime.retry') }}</t-button>
        </template>
      </t-alert>
    </div>

    <div v-else-if="users.length === 0" class="empty-state">
      <t-empty :description="t('system.globalSettings.sections.userManagement.table.empty')" />
    </div>

    <div v-else class="data-table-shell">
      <div class="data-table-shell__scroll">
        <t-table
          row-key="id"
          :data="users"
          :columns="columns"
          size="medium"
          hover
          stripe
          :loading="loading"
          :expanded-row-keys="expandedRowKeys"
          @expand-change="onExpandChange"
        >
          <template #username="{ row }">
            <div class="user-cell">
              <span class="user-name">{{ row.username || '—' }}</span>
            </div>
          </template>
          <template #email="{ row }">
            <span class="user-email">{{ row.email || '—' }}</span>
          </template>
          <template #isSystemAdmin="{ row }">
            <t-tag v-if="row.is_system_admin" theme="warning" variant="light" size="small">
              {{ t('system.globalSettings.sections.userManagement.tenantRole.admin') }}
            </t-tag>
            <span v-else class="no-tag">—</span>
          </template>
          <template #createdAt="{ row }">
            <span class="date-cell">{{ formatDate(row.created_at) }}</span>
          </template>
          <template #tenants="{ row }">
            <div class="tenants-cell">
              <span class="tenants-count">{{ row.tenants?.length || 0 }}</span>
            </div>
          </template>
          <template #actions="{ row }">
            <div class="actions-cell">
              <t-popconfirm
                :content="t('system.globalSettings.sections.userManagement.deleteUser.confirmBody', {
                  username: row.username || row.email,
                  email: row.email,
                })"
                :confirm-btn="{
                  content: t('system.globalSettings.sections.userManagement.deleteUser.confirmBtn'),
                  theme: 'danger',
                }"
                :cancel-btn="t('common.cancel')"
                placement="left"
                @confirm="handleDeleteUser(row)"
              >
                <t-button
                  v-if="row.id !== currentUserId"
                  theme="danger"
                  variant="text"
                  size="small"
                  :loading="deletingUserId === row.id"
                >
                  <template #icon><t-icon name="delete" /></template>
                  {{ t('system.globalSettings.sections.userManagement.deleteUser.confirmBtn') }}
                </t-button>
              </t-popconfirm>
            </div>
          </template>
          <template #expandedRow="{ row }">
            <div class="expanded-tenant-list">
              <div v-if="!row.tenants || row.tenants.length === 0" class="no-tenants">
                {{ t('system.globalSettings.sections.userManagement.table.noTenants') }}
              </div>
              <div v-else class="tenant-grid-header">
                <span class="tenant-grid-cell tenant-grid-cell--name">{{ t('settings.tenantName') }}</span>
                <span class="tenant-grid-cell tenant-grid-cell--role">{{ t('tenantMember.columns.role') }}</span>
                <span class="tenant-grid-cell tenant-grid-cell--actions">{{ t('tenantMember.columns.operations') }}</span>
              </div>
              <div v-for="tenant in row.tenants" :key="tenant.tenant_id" class="tenant-grid-row">
                <span class="tenant-grid-cell tenant-grid-cell--name">{{ tenant.tenant_name || `#${tenant.tenant_id}` }}</span>
                <span class="tenant-grid-cell tenant-grid-cell--role">
                  <t-tag :theme="roleTagTheme(tenant.role)" size="small">
                    {{ roleLabel(tenant.role) }}
                  </t-tag>
                </span>
                <span class="tenant-grid-cell tenant-grid-cell--actions">
                  <t-popconfirm
                    :content="t('system.globalSettings.sections.userManagement.removeFromTenant.confirmBody', {
                      username: row.username || row.email,
                      workspace: tenant.tenant_name || `#${tenant.tenant_id}`,
                    })"
                    :confirm-btn="{
                      content: t('system.globalSettings.sections.userManagement.removeFromTenant.confirmBtn'),
                      theme: 'danger',
                    }"
                    :cancel-btn="t('common.cancel')"
                    placement="left"
                    @confirm="handleRemoveFromTenant(row.id, tenant)"
                  >
                    <t-button
                      theme="danger"
                      variant="text"
                      size="small"
                      :loading="removingKey === `${row.id}-${tenant.tenant_id}`"
                    >
                      <template #icon><t-icon name="user-clear" /></template>
                      {{ t('system.globalSettings.sections.userManagement.removeFromTenant.confirmBtn') }}
                    </t-button>
                  </t-popconfirm>
                </span>
              </div>
            </div>
          </template>
        </t-table>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import { useAuthStore } from '@/stores/auth'
import {
  listUsersWithTenants,
  removeUserFromTenant,
  adminDeleteUser,
  type UserWithTenantInfo,
  type UserTenantBrief,
} from '@/api/system'

const { t, locale } = useI18n()
const authStore = useAuthStore()

const users = ref<UserWithTenantInfo[]>([])
const loading = ref(false)
const error = ref('')
const expandedRowKeys = ref<string[]>([])
const removingKey = ref<string | null>(null)
const deletingUserId = ref<string | null>(null)

const currentUserId = computed(() => authStore.currentUserId)

const columns = computed(() => [
  { colKey: 'username', title: t('system.globalSettings.sections.userManagement.table.username'), ellipsis: true, minWidth: 120 },
  { colKey: 'email', title: t('system.globalSettings.sections.userManagement.table.email'), ellipsis: true, minWidth: 180 },
  {
    colKey: 'isSystemAdmin',
    title: t('system.globalSettings.sections.userManagement.table.isSystemAdmin'),
    width: 120,
    align: 'center' as const,
  },
  {
    colKey: 'createdAt',
    title: t('system.globalSettings.sections.userManagement.table.createdAt'),
    width: 160,
  },
  {
    colKey: 'tenants',
    title: t('system.globalSettings.sections.userManagement.table.tenants', { count: '' }),
    width: 100,
    align: 'center' as const,
  },
  {
    colKey: 'actions',
    title: t('system.globalSettings.sections.userManagement.table.actions'),
    width: 100,
    align: 'left' as const,
  },
])

function roleTagTheme(role: string): 'primary' | 'warning' | 'success' | 'default' | 'danger' {
  switch (role) {
    case 'owner':
      return 'primary'
    case 'admin':
      return 'warning'
    case 'contributor':
      return 'success'
    case 'viewer':
      return 'default'
    default:
      return 'default'
  }
}

function roleLabel(role: string): string {
  const path = `system.globalSettings.sections.userManagement.tenantRole.${role}`
  return t(path) as string
}

function formatDate(s: string | undefined): string {
  if (!s) return '-'
  try {
    const d = new Date(s)
    return new Intl.DateTimeFormat(locale.value || 'zh-CN', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
    }).format(d)
  } catch {
    return s
  }
}

function onExpandChange(keys: (string | number)[]) {
  expandedRowKeys.value = keys.map((k) => String(k))
}

async function loadUsers() {
  loading.value = true
  error.value = ''
  try {
    const resp = await listUsersWithTenants()
    if (resp.success && resp.data) {
      users.value = resp.data
    } else {
      error.value = t('system.globalSettings.sections.userManagement.table.loadFailed')
    }
  } catch (err: any) {
    error.value = err?.message || t('system.globalSettings.sections.userManagement.table.loadFailed')
  } finally {
    loading.value = false
  }
}

async function handleRemoveFromTenant(userId: string, tenant: UserTenantBrief) {
  const key = `${userId}-${tenant.tenant_id}`
  removingKey.value = key
  try {
    const resp = await removeUserFromTenant(userId, tenant.tenant_id)
    if (resp.success) {
      MessagePlugin.success(t('system.globalSettings.sections.userManagement.removeFromTenant.success'))
      // Reload to reflect changes
      await loadUsers()
    } else {
      MessagePlugin.error(resp.message || t('system.globalSettings.sections.userManagement.removeFromTenant.failed'))
    }
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('system.globalSettings.sections.userManagement.removeFromTenant.failed'))
  } finally {
    removingKey.value = null
  }
}

async function handleDeleteUser(row: UserWithTenantInfo) {
  if (row.id === currentUserId.value) {
    MessagePlugin.error(t('system.globalSettings.sections.userManagement.deleteUser.cannotDeleteSelf'))
    return
  }
  deletingUserId.value = row.id
  try {
    const resp = await adminDeleteUser(row.id)
    if (resp.success) {
      MessagePlugin.success(t('system.globalSettings.sections.userManagement.deleteUser.success'))
      // Reload to reflect changes
      await loadUsers()
    } else {
      MessagePlugin.error(resp.message || t('system.globalSettings.sections.userManagement.deleteUser.failed'))
    }
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('system.globalSettings.sections.userManagement.deleteUser.failed'))
  } finally {
    deletingUserId.value = null
  }
}

onMounted(() => {
  loadUsers()
})
</script>

<style lang="less" scoped>
.user-management {
  width: 100%;

  .section-header {
    margin-bottom: 24px;

    h2 {
      font-size: 20px;
      font-weight: 600;
      color: var(--td-text-color-primary);
      margin: 0 0 8px 0;
    }

    .section-description {
      font-size: 14px;
      color: var(--td-text-color-secondary);
      margin: 0;
      line-height: 1.5;
    }
  }
}

.user-cell {
  padding: 2px 0;

  .user-name {
    font-weight: 500;
    font-size: 14px;
    color: var(--td-text-color-primary);
  }
}

.user-email {
  font-size: 13px;
  color: var(--td-text-color-secondary);
}

.date-cell {
  font-size: 13px;
  color: var(--td-text-color-primary);
  font-variant-numeric: tabular-nums;
}

.tenants-cell {
  display: flex;
  justify-content: center;

  .tenants-count {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    min-width: 22px;
    height: 20px;
    padding: 0 7px;
    border-radius: 10px;
    background-color: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-primary);
    font-size: 12px;
    font-weight: 600;
  }
}

.actions-cell {
  display: flex;
  align-items: center;
  gap: 4px;
}

.no-tag {
  color: var(--td-text-color-disabled);
}

.data-table-shell {
  overflow-x: auto;
  border-radius: 10px;
  border: 1px solid var(--td-component-stroke);
  background-color: var(--td-bg-color-container);

  &:deep(thead th) {
    font-weight: 600;
    font-size: 13px;
  }

  &:deep(.t-table td),
  &:deep(.t-table th) {
    padding-top: 12px;
    padding-bottom: 12px;
  }

  &:deep(.t-table__expandable-icon-cell) {
    width: 36px;
  }
}

.expanded-tenant-list {
  padding: 8px 16px 8px 48px;
  background: var(--td-bg-color-container-hover);
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.tenant-grid-header {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 100px 120px;
  gap: 12px;
  padding: 6px 0;
  font-size: 12px;
  font-weight: 600;
  color: var(--td-text-color-secondary);
  border-bottom: 1px solid var(--td-component-stroke);
}

.tenant-grid-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 100px 120px;
  gap: 12px;
  padding: 8px 0;
  align-items: center;
  border-bottom: 1px solid var(--td-component-stroke);

  &:last-child {
    border-bottom: none;
  }
}

.tenant-grid-cell {
  font-size: 13px;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;

  &--name {
    font-weight: 500;
  }

  &--actions {
    display: flex;
    align-items: center;
  }
}

.no-tenants {
  padding: 12px 0;
  font-size: 13px;
  color: var(--td-text-color-placeholder);
  text-align: center;
}

.loading-state {
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 60px 0;
  color: var(--td-text-color-placeholder);
  font-size: 13px;
}

.empty-state {
  padding: 60px 0;
  display: flex;
  justify-content: center;
}

.error-inline {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 20px 0 8px;
}
</style>
