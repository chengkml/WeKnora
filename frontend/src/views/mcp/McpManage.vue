<template>
  <div class="mcp-manage-container">
    <div class="mcp-manage-header" style="--wails-draggable: drag">
      <div class="mcp-manage-title-row" style="--wails-draggable: no-drag">
        <h2>{{ $t('menu.mcp') }}</h2>
        <t-space :size="8">
          <t-button v-if="isAdmin" theme="primary" size="small" @click="handleCreate">
            <template #icon><t-icon name="add" size="16px" /></template>
            {{ $t('mcpManage.newService') }}
          </t-button>
          <t-button v-if="isAdmin" theme="primary" variant="outline" size="small" :loading="syncing" @click="handleSync">
            <template #icon><t-icon name="swap-right" size="16px" /></template>
            {{ syncing ? $t('mcpManage.syncing') : $t('mcpManage.sync') }}
          </t-button>
          <t-button variant="outline" size="small" :loading="loading || statusLoading" @click="refreshAll">
            {{ $t('mcpManage.refresh') }}
          </t-button>
        </t-space>
      </div>
      <p class="mcp-manage-subtitle">{{ $t('mcpManage.subtitle') }}</p>
    </div>

    <div class="mcp-manage-body">
      <!-- Agent 网关状态卡片：GET /api/v1/mcp-gateway/status（drift 检测） -->
      <div class="mcp-gateway-card" :class="{ 'mcp-gateway-card--unreachable': status && !status.reachable }">
        <div class="mcp-gateway-head">
          <div class="mcp-gateway-title">
            <t-icon :name="status && status.reachable ? 'check-circle-filled' : 'error-circle-filled'" size="18px"
              class="mcp-gateway-title-icon" />
            <span>{{ $t('mcpManage.gatewayTitle') }}</span>
          </div>
          <t-button v-if="isAdmin && hasDrift" theme="primary" variant="outline" size="small" :loading="syncing"
            @click="handleSync">
            {{ $t('mcpManage.syncNow') }}
          </t-button>
        </div>

        <div v-if="statusLoading && !status" class="mcp-gateway-loading">
          <t-loading :loading="true" size="small" />
        </div>

        <template v-else-if="status">
          <div class="mcp-gateway-meta">
            <span class="mcp-gateway-meta-item">
              <span class="mcp-gateway-meta-label">{{ $t('mcpManage.gatewayUrl') }}:</span>
              <code class="mcp-gateway-meta-value">{{ status.gateway_url || '-' }}</code>
            </span>
            <span class="mcp-gateway-meta-item">
              <span class="mcp-gateway-meta-label">{{ $t('mcpManage.gatewaySource') }}:</span>
              <span class="mcp-gateway-meta-value">{{ sourceLabel }}</span>
            </span>
            <span class="mcp-gateway-meta-item">
              <span class="mcp-gateway-meta-value">{{ $t('mcpManage.gatewayCount', { n: status.count || 0 }) }}</span>
            </span>
            <t-tag :theme="reachableTheme" variant="light" size="small">{{ reachableText }}</t-tag>
          </div>

          <div v-if="status.error" class="mcp-gateway-error">{{ status.error }}</div>

          <div v-if="hasDrift" class="mcp-gateway-drift">
            <p class="mcp-gateway-drift-title">{{ $t('mcpManage.driftTitle') }}</p>
            <div v-if="missingNames.length" class="mcp-gateway-drift-row">
              <t-tag theme="warning" variant="light" size="small">
                {{ $t('mcpManage.driftMissing', { n: missingNames.length }) }}
              </t-tag>
              <span class="mcp-gateway-drift-names"
                :title="$t('mcpManage.driftMissingTip', { names: missingNames.join(', ') })">{{ missingNames.join(', ') }}</span>
            </div>
            <div v-if="extraNames.length" class="mcp-gateway-drift-row">
              <t-tag theme="danger" variant="light" size="small">
                {{ $t('mcpManage.driftExtra', { n: extraNames.length }) }}
              </t-tag>
              <span class="mcp-gateway-drift-names"
                :title="$t('mcpManage.driftExtraTip', { names: extraNames.join(', ') })">{{ extraNames.join(', ') }}</span>
            </div>
          </div>
        </template>

        <div v-else class="mcp-gateway-error">{{ $t('mcpManage.statusError') }}</div>

        <!-- 上一次「同步到 Agent 网关」的结果明细（含被跳过的原因） -->
        <div v-if="lastSync" class="mcp-gateway-sync-result">
          <p class="mcp-gateway-sync-summary">
            {{ $t('mcpManage.syncSummary', {
              synced: (lastSync.synced || []).length,
              skipped: (lastSync.skipped || []).length,
              warnings: (lastSync.warnings || []).length,
            }) }}
          </p>
          <div v-if="lastSync.skipped && lastSync.skipped.length" class="mcp-gateway-sync-list">
            <span class="mcp-gateway-sync-list-title">{{ $t('mcpManage.skippedTitle') }}</span>
            <ul>
              <li v-for="item in lastSync.skipped" :key="'skipped-' + item.name">{{ item.name }} — {{ item.reason }}</li>
            </ul>
          </div>
          <div v-if="lastSync.warnings && lastSync.warnings.length" class="mcp-gateway-sync-list">
            <span class="mcp-gateway-sync-list-title">{{ $t('mcpManage.warningsTitle') }}</span>
            <ul>
              <li v-for="item in lastSync.warnings" :key="'warning-' + item.name">{{ item.name }} — {{ item.reason }}</li>
            </ul>
          </div>
        </div>
      </div>

      <!-- MCP 服务列表：数据来自既有 listMCPServices() -->
      <div v-if="loading && services.length === 0" class="mcp-manage-loading">
        <t-loading :loading="true" size="large" :text="$t('mcpManage.loading')" />
      </div>

      <div v-else-if="services.length === 0" class="mcp-manage-empty">
        <div class="mcp-manage-empty-icon"><t-icon name="server" size="48px" /></div>
        <p class="mcp-manage-empty-title">{{ $t('mcpManage.emptyTitle') }}</p>
        <p class="mcp-manage-empty-desc">{{ $t('mcpManage.emptyDesc') }}</p>
      </div>

      <t-table v-else :data="services" :columns="columns" row-key="id" :hover="true" :loading="loading">
        <template #name="{ row }">
          <div class="mcp-service-name-cell">
            <t-icon :name="transportIcon(row.transport_type)" size="18px" class="mcp-service-name-icon" />
            <div class="mcp-service-name-main">
              <div class="mcp-service-name-line">
                <span class="mcp-service-name-text">{{ row.name || $t('mcpManage.unnamed') }}</span>
                <t-tag v-if="row.is_builtin" size="small" variant="light" theme="primary">
                  {{ $t('mcpSettings.builtin') }}
                </t-tag>
                <t-tag v-if="credentialState(row)" size="small" variant="light-outline"
                  :theme="credentialState(row)?.configured ? 'success' : 'warning'"
                  :title="$t('mcpManage.credentialTip')">
                  {{ credentialState(row)?.configured ? $t('mcpManage.credentialConfigured') : $t('mcpManage.credentialMissing') }}
                </t-tag>
              </div>
              <div class="mcp-service-desc-text">{{ row.description || '-' }}</div>
            </div>
          </div>
        </template>

        <template #transport_type="{ row }">
          <span class="mcp-service-transport">{{ transportLabel(row.transport_type) }}</span>
        </template>

        <template #target="{ row }">
          <span class="mcp-service-target">{{ targetText(row) }}</span>
        </template>

        <template #enabled="{ row }">
          <t-tag :theme="row.enabled ? 'success' : 'default'" variant="light" size="small">
            {{ row.enabled ? $t('mcpManage.enabled') : $t('mcpManage.disabled') }}
          </t-tag>
        </template>

        <template #gateway="{ row }">
          <t-tooltip :content="gatewayBadge(row).tip" placement="top">
            <t-tag :theme="gatewayBadge(row).theme" variant="light" size="small">{{ gatewayBadge(row).text }}</t-tag>
          </t-tooltip>
        </template>

        <template #actions="{ row }">
          <t-space :size="4">
            <t-button variant="text" theme="primary" size="small" @click="handleTest(row)">
              {{ $t('mcpManage.test') }}
            </t-button>
            <t-button variant="text" theme="primary" size="small" :loading="gatewayTestingId === row.id"
              @click="handleGatewayTest(row)">
              {{ $t('mcpManage.gatewayTest') }}
            </t-button>
            <t-button variant="text" theme="primary" size="small" @click="handleEdit(row)">
              {{ $t('mcpManage.edit') }}
            </t-button>
            <t-button v-if="!row.is_builtin" variant="text" theme="default" size="small"
              :loading="togglingId === row.id" @click="handleToggleEnabled(row)">
              {{ row.enabled ? $t('mcpManage.disable') : $t('mcpManage.enable') }}
            </t-button>
            <t-popconfirm v-if="!row.is_builtin" theme="danger"
              :content="$t('mcpManage.deleteConfirm', { name: row.name || $t('mcpManage.unnamed') })"
              @confirm="handleDelete(row)">
              <t-button variant="text" theme="danger" size="small" :loading="deletingId === row.id">
                {{ $t('mcpManage.delete') }}
              </t-button>
            </t-popconfirm>
          </t-space>
        </template>
      </t-table>
    </div>

    <!-- 复用 Settings 的既有抽屉（新建/编辑），不改其既有行为 -->
    <McpServiceDialog v-model:visible="dialogVisible" :service="currentService" :mode="dialogMode"
      @success="handleDialogSuccess" @created="handleDialogCreated" />

    <!-- 测试连接：WeKnora 侧视角（含工具/资源与人工审批开关） -->
    <t-dialog v-model:visible="testVisible" :header="testDialogHeader" width="720px" :footer="false">
      <div v-if="testLoading" class="mcp-test-loading">
        <t-loading :loading="true" size="small" :text="$t('mcpManage.testing')" />
      </div>
      <McpTestResultBody v-else :result="testResult" :service-id="testService ? testService.id : undefined"
        :active="testVisible" />
    </t-dialog>

    <!-- 网关侧测试：真正执行侧 agent-gateway 的连通性与工具列表 -->
    <t-dialog v-model:visible="gatewayTestVisible" :header="gatewayTestHeader" width="640px" :footer="false">
      <div v-if="gatewayTestingId" class="mcp-test-loading">
        <t-loading :loading="true" size="small" :text="$t('mcpManage.gatewayTesting')" />
      </div>
      <div v-else-if="gatewayTestResult" class="mcp-gateway-test">
        <div class="mcp-gateway-test-status" :class="gatewayTestResult.ok ? 'is-success' : 'is-error'">
          <t-icon :name="gatewayTestResult.ok ? 'check-circle-filled' : 'close-circle-filled'" size="18px" />
          <span>{{ gatewayTestResult.ok ? $t('mcpManage.gatewayOk') : $t('mcpManage.gatewayFailed') }}</span>
          <span v-if="gatewayTestResult.elapsed_ms !== undefined && gatewayTestResult.elapsed_ms !== null"
            class="mcp-gateway-test-elapsed">
            {{ $t('mcpManage.elapsed', { ms: gatewayTestResult.elapsed_ms }) }}
          </span>
        </div>
        <p v-if="gatewayTestResult.error" class="mcp-gateway-test-error">{{ gatewayTestResult.error }}</p>
        <div class="mcp-gateway-test-meta">
          <span>{{ $t('mcpManage.gatewayTransport') }}: {{ gatewayTestResult.transport || '-' }}</span>
          <span>{{ $t('mcpManage.toolsCount', { n: gatewayTestResult.tool_count || 0 }) }}</span>
        </div>
        <div v-if="gatewayTools.length" class="mcp-gateway-test-tools">
          <div v-for="item in gatewayTools" :key="'gw-tool-' + item.name" class="mcp-gateway-test-tool">
            <div class="mcp-gateway-test-tool-head">
              <t-icon name="tools" size="16px" />
              <span class="mcp-gateway-test-tool-name">{{ item.name }}</span>
            </div>
            <div v-if="item.description" class="mcp-gateway-test-tool-desc">{{ item.description }}</div>
            <div v-if="item.params && item.params.length" class="mcp-gateway-test-tool-params">
              <t-tag v-for="p in item.params" :key="'gw-param-' + item.name + '-' + p" size="small"
                variant="light-outline">{{ p }}</t-tag>
            </div>
          </div>
        </div>
        <div v-else class="mcp-gateway-test-empty">{{ $t('mcpManage.noTools') }}</div>
      </div>
    </t-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue';
import { MessagePlugin } from 'tdesign-vue-next';
import { useI18n } from 'vue-i18n';
import { useAuthStore } from '@/stores/auth';
import {
  listMCPServices,
  updateMCPService,
  deleteMCPService,
  testMCPService,
  type MCPService,
  type MCPTestResult,
} from '@/api/mcp-service';
import {
  syncMCPGateway,
  getMCPGatewayStatus,
  testMCPGatewayServer,
  type MCPGatewayStatusResult,
  type MCPGatewaySyncResult,
  type MCPGatewayTestResult,
} from '@/api/mcp-gateway';
// 复用 Settings 里既有的 MCP 组件（仅复用其对外契约，不改它们的既有行为）
import McpServiceDialog from '@/views/settings/components/McpServiceDialog.vue';
import McpTestResultBody from '@/views/settings/components/McpTestResultBody.vue';

const { t } = useI18n();
const authStore = useAuthStore();
const isAdmin = computed(() => authStore.hasRole('admin'));

// ---- 列表 / 网关状态 ----
const services = ref<MCPService[]>([]);
const loading = ref(false);
const status = ref<MCPGatewayStatusResult | null>(null);
const statusLoading = ref(false);
const syncing = ref(false);
const lastSync = ref<MCPGatewaySyncResult | null>(null);
const togglingId = ref('');
const deletingId = ref('');

// ---- 测试弹窗（WeKnora 侧）：复用 McpTestResultBody ----
const testVisible = ref(false);
const testLoading = ref(false);
const testService = ref<MCPService | null>(null);
const testResult = ref<MCPTestResult | null>(null);

// ---- 网关侧测试弹窗 ----
const gatewayTestVisible = ref(false);
const gatewayTestingId = ref('');
const gatewayTestService = ref<MCPService | null>(null);
const gatewayTestResult = ref<MCPGatewayTestResult | null>(null);

// ---- 新建/编辑抽屉（复用 McpServiceDialog）----
const dialogVisible = ref(false);
const dialogMode = ref<'add' | 'edit'>('add');
const currentService = ref<MCPService | null>(null);

interface ColumnItem {
  colKey: string;
  title: string;
  ellipsis?: boolean;
  width?: number;
}

const columns = computed<ColumnItem[]>(() => {
  const base: ColumnItem[] = [
    { colKey: 'name', title: t('mcpManage.colName'), ellipsis: true, width: 260 },
    { colKey: 'transport_type', title: t('mcpManage.colTransport'), width: 150 },
    { colKey: 'target', title: t('mcpManage.colTarget'), ellipsis: true },
    { colKey: 'enabled', title: t('mcpManage.colEnabled'), width: 100 },
    { colKey: 'gateway', title: t('mcpManage.colGateway'), width: 120 },
  ];
  // 操作列里的接口（test / 网关侧 test / update / delete）后端均为 Admin：
  // 非 Admin 直接隐藏操作列，避免点了必然 403。
  if (isAdmin.value) {
    base.push({ colKey: 'actions', title: t('mcpManage.colActions'), width: 330 });
  }
  return base;
});

const missingNames = computed<string[]>(() => status.value?.missing_on_gateway || []);
const extraNames = computed<string[]>(() => status.value?.extra_on_gateway || []);
const hasDrift = computed<boolean>(() => missingNames.value.length > 0 || extraNames.value.length > 0);
const sourceLabel = computed<string>(() => {
  const source = status.value?.source;
  if (source === 'managed') return t('mcpManage.sourceManaged');
  if (source === 'seed') return t('mcpManage.sourceSeed');
  return t('mcpManage.sourceUnknown');
});
const reachableTheme = computed(() => (status.value?.reachable ? 'success' : 'danger'));
const reachableText = computed(() =>
  status.value?.reachable ? t('mcpManage.reachable') : t('mcpManage.unreachable')
);
const gatewayTools = computed(() => gatewayTestResult.value?.tools || []);
const testDialogHeader = computed(() =>
  t('mcpManage.testTitle', { name: testService.value?.name || t('mcpManage.unnamed') })
);
const gatewayTestHeader = computed(() =>
  t('mcpManage.gatewayTestTitle', { name: gatewayTestService.value?.name || t('mcpManage.unnamed') })
);

// 传输类型图标：与 views/settings/McpSettings.vue 的 getTransportTypeIcon 保持一致
function transportIcon(transportType?: string): string {
  switch (transportType) {
    case 'sse':
      return 'cast';
    case 'http-streamable':
      return 'link';
    case 'stdio':
      return 'code';
    default:
      return 'tools';
  }
}

function transportLabel(transportType?: string): string {
  switch (transportType) {
    case 'sse':
      return t('mcpManage.transport.sse');
    case 'http-streamable':
      return t('mcpManage.transport.httpStreamable');
    case 'stdio':
      return t('mcpManage.transport.stdio');
    default:
      return transportType || '-';
  }
}

// 目标：http/sse 显示 url，stdio 显示「command args」
function targetText(row: MCPService): string {
  if (row.transport_type === 'stdio') {
    const command = row.stdio_config?.command || '';
    const args = (row.stdio_config?.args || []).join(' ');
    const text = [command, args].filter(Boolean).join(' ').trim();
    return text || t('mcpManage.emptyTarget');
  }
  return row.url || t('mcpManage.emptyTarget');
}

// 凭证：只读既有 credentials 元数据（configured 布尔），永不回显明文
function credentialState(row: MCPService): { configured: boolean } | null {
  const credentials = row.credentials;
  if (!credentials) return null;
  const fields = Object.values(credentials).filter(Boolean);
  if (fields.length === 0) return null;
  return { configured: fields.some((field) => !!field.configured) };
}

interface GatewayBadge {
  theme: 'success' | 'warning' | 'danger' | 'default';
  text: string;
  tip: string;
}

// 网关同步状态徽标：口径与后端 mapping 规则一致（enabled=false / sse 不参与同步）
function gatewayBadge(row: MCPService): GatewayBadge {
  if (!row.enabled) {
    return { theme: 'default', text: t('mcpManage.badgeDisabled'), tip: t('mcpManage.tipDisabled') };
  }
  if (row.transport_type === 'sse') {
    return { theme: 'warning', text: t('mcpManage.badgeUnsupported'), tip: t('mcpManage.tipSseUnsupported') };
  }
  if (row.auth_config?.auth_type === 'oauth') {
    return { theme: 'warning', text: t('mcpManage.badgeUnsupported'), tip: t('mcpManage.tipOauthUnsupported') };
  }
  if (!status.value) {
    return { theme: 'default', text: t('mcpManage.badgeUnknown'), tip: t('mcpManage.tipStatusUnknown') };
  }
  if ((status.value.gateway_names || []).includes(row.name)) {
    return { theme: 'success', text: t('mcpManage.badgeSynced'), tip: t('mcpManage.tipSynced') };
  }
  return { theme: 'danger', text: t('mcpManage.badgeNotSynced'), tip: t('mcpManage.tipNotSynced') };
}

async function loadServices() {
  loading.value = true;
  try {
    services.value = (await listMCPServices()) || [];
  } catch (e: any) {
    // 复用既有 mcpSettings.toasts.loadFailed（四语言均已存在）
    MessagePlugin.error(e?.message || t('mcpSettings.toasts.loadFailed'));
    services.value = [];
  } finally {
    loading.value = false;
  }
}

async function loadStatus() {
  statusLoading.value = true;
  try {
    status.value = await getMCPGatewayStatus();
  } catch (e: any) {
    // 网关状态是辅助信息：失败只记录，不打断服务列表
    console.error('Failed to load MCP gateway status:', e);
    status.value = null;
  } finally {
    statusLoading.value = false;
  }
}

async function refreshAll() {
  await Promise.all([loadServices(), loadStatus()]);
}

async function handleSync() {
  syncing.value = true;
  try {
    const result = await syncMCPGateway();
    lastSync.value = result;
    MessagePlugin.success(t('mcpManage.syncSuccess', { n: (result?.synced || []).length }));
    await refreshAll();
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('mcpManage.syncFailed'));
  } finally {
    syncing.value = false;
  }
}

async function handleTest(row: MCPService) {
  testService.value = row;
  testResult.value = null;
  testLoading.value = true;
  testVisible.value = true;
  try {
    testResult.value = await testMCPService(row.id);
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('mcpManage.testFailed'));
  } finally {
    testLoading.value = false;
  }
}

async function handleGatewayTest(row: MCPService) {
  gatewayTestService.value = row;
  gatewayTestResult.value = null;
  gatewayTestingId.value = row.id;
  gatewayTestVisible.value = true;
  try {
    // 探测失败后端也返回 200 + ok=false，原因在 error 字段里
    gatewayTestResult.value = await testMCPGatewayServer(row.id);
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('mcpManage.gatewayTestFailed'));
  } finally {
    gatewayTestingId.value = '';
  }
}

function handleCreate() {
  currentService.value = null;
  dialogMode.value = 'add';
  dialogVisible.value = true;
}

function handleEdit(row: MCPService) {
  currentService.value = row;
  dialogMode.value = 'edit';
  dialogVisible.value = true;
}

async function handleDialogSuccess() {
  dialogVisible.value = false;
  await refreshAll();
}

async function handleDialogCreated(created: MCPService) {
  await loadServices();
  // 列表里的完整记录才有 credentials 元数据，用它切到编辑态
  const full = services.value.find((item) => item.id === created.id) || created;
  currentService.value = full;
  dialogMode.value = 'edit';
  // 不关闭抽屉：新建后直接进入编辑态，便于继续配凭证 / 授权 / 测试
}

async function handleToggleEnabled(row: MCPService) {
  const original = row.enabled;
  togglingId.value = row.id;
  row.enabled = !original;
  try {
    await updateMCPService(row.id, { enabled: row.enabled });
    MessagePlugin.success(row.enabled ? t('mcpManage.enabledToast') : t('mcpManage.disabledToast'));
    // 启停会改变应下发到网关的集合，刷新 drift
    await loadStatus();
  } catch (e: any) {
    row.enabled = original;
    MessagePlugin.error(e?.message || t('mcpManage.toggleFailed'));
  } finally {
    togglingId.value = '';
  }
}

async function handleDelete(row: MCPService) {
  deletingId.value = row.id;
  try {
    await deleteMCPService(row.id);
    MessagePlugin.success(t('mcpManage.deleted'));
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('mcpManage.deleteFailed'));
  } finally {
    deletingId.value = '';
    // 无论成功失败都刷新，避免「操作后列表不变、须刷新页面才知道结果」
    await refreshAll();
  }
}

onMounted(refreshAll);
</script>

<style scoped>
.mcp-manage-container {
  height: 100%;
  display: flex;
  flex-direction: column;
  padding: 20px 24px;
  box-sizing: border-box;
  overflow: hidden;
}

.mcp-manage-header {
  flex-shrink: 0;
  padding-bottom: 16px;
  border-bottom: 1px solid var(--td-component-stroke, #e5e6eb);
}

.mcp-manage-title-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.mcp-manage-title-row h2 {
  margin: 0;
  font-size: 20px;
  font-weight: 600;
}

.mcp-manage-subtitle {
  margin: 6px 0 0;
  font-size: 13px;
  color: var(--td-text-color-secondary, #6b7280);
}

.mcp-manage-body {
  flex: 1;
  overflow: auto;
  padding-top: 16px;
}

.mcp-gateway-card {
  border: 1px solid var(--td-component-stroke, #e5e6eb);
  border-radius: 6px;
  padding: 12px 16px;
  margin-bottom: 16px;
  background: var(--td-bg-color-container, #fff);
}

.mcp-gateway-card--unreachable {
  border-color: var(--td-error-color, #d54941);
}

.mcp-gateway-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.mcp-gateway-title {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 15px;
  font-weight: 600;
}

.mcp-gateway-title-icon {
  color: var(--td-brand-color, #0052d9);
}

.mcp-gateway-card--unreachable .mcp-gateway-title-icon {
  color: var(--td-error-color, #d54941);
}

.mcp-gateway-loading {
  padding: 12px 0;
}

.mcp-gateway-meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px 16px;
  margin-top: 10px;
  font-size: 13px;
  color: var(--td-text-color-secondary, #6b7280);
}

.mcp-gateway-meta-item {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.mcp-gateway-meta-label {
  color: var(--td-text-color-secondary, #6b7280);
}

.mcp-gateway-meta-value {
  color: var(--td-text-color-primary, #000);
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  word-break: break-all;
}

.mcp-gateway-error {
  margin-top: 8px;
  font-size: 13px;
  color: var(--td-error-color, #d54941);
}

.mcp-gateway-drift {
  margin-top: 10px;
  padding: 8px 10px;
  border-radius: 4px;
  background: var(--td-warning-color-1, #fff1e9);
}

.mcp-gateway-drift-title {
  margin: 0 0 6px;
  font-size: 13px;
  font-weight: 600;
}

.mcp-gateway-drift-row {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 4px;
}

.mcp-gateway-drift-names {
  font-size: 13px;
  color: var(--td-text-color-primary, #000);
  word-break: break-all;
}

.mcp-gateway-sync-result {
  margin-top: 12px;
  padding-top: 10px;
  border-top: 1px dashed var(--td-component-stroke, #e5e6eb);
  font-size: 13px;
}

.mcp-gateway-sync-summary {
  margin: 0;
  font-weight: 500;
}

.mcp-gateway-sync-list {
  margin-top: 6px;
}

.mcp-gateway-sync-list-title {
  font-weight: 500;
  color: var(--td-text-color-secondary, #6b7280);
}

.mcp-gateway-sync-list ul {
  margin: 4px 0 0;
  padding-left: 18px;
  color: var(--td-text-color-secondary, #6b7280);
}

.mcp-manage-loading {
  display: flex;
  justify-content: center;
  padding-top: 80px;
}

.mcp-manage-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  padding-top: 80px;
  color: var(--td-text-color-secondary, #6b7280);
}

.mcp-manage-empty-icon {
  color: var(--td-brand-color, #0052d9);
}

.mcp-manage-empty-title {
  font-size: 16px;
  font-weight: 500;
  margin: 12px 0 4px;
}

.mcp-manage-empty-desc {
  font-size: 13px;
}

.mcp-service-name-cell {
  display: flex;
  align-items: flex-start;
  gap: 8px;
}

.mcp-service-name-icon {
  color: var(--td-brand-color, #0052d9);
  margin-top: 2px;
  flex-shrink: 0;
}

.mcp-service-name-main {
  min-width: 0;
}

.mcp-service-name-line {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
}

.mcp-service-name-text {
  font-weight: 500;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}

.mcp-service-desc-text {
  margin-top: 2px;
  font-size: 13px;
  color: var(--td-text-color-secondary, #6b7280);
}

.mcp-service-transport {
  font-size: 13px;
}

.mcp-service-target {
  font-size: 13px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  color: var(--td-text-color-secondary, #6b7280);
  word-break: break-all;
}

.mcp-test-loading {
  display: flex;
  justify-content: center;
  padding: 40px 0;
}

.mcp-gateway-test-status {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 15px;
  font-weight: 600;
}

.mcp-gateway-test-status.is-success {
  color: var(--td-success-color, #2ba471);
}

.mcp-gateway-test-status.is-error {
  color: var(--td-error-color, #d54941);
}

.mcp-gateway-test-elapsed {
  font-size: 13px;
  font-weight: 400;
  color: var(--td-text-color-secondary, #6b7280);
}

.mcp-gateway-test-error {
  margin: 8px 0 0;
  font-size: 13px;
  color: var(--td-error-color, #d54941);
  word-break: break-all;
}

.mcp-gateway-test-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 6px 16px;
  margin-top: 8px;
  font-size: 13px;
  color: var(--td-text-color-secondary, #6b7280);
}

.mcp-gateway-test-tools {
  margin-top: 12px;
  max-height: 380px;
  overflow: auto;
  border: 1px solid var(--td-component-stroke, #e5e6eb);
  border-radius: 6px;
}

.mcp-gateway-test-tool {
  padding: 8px 12px;
  border-bottom: 1px solid var(--td-component-stroke, #f0f0f0);
}

.mcp-gateway-test-tool:last-child {
  border-bottom: none;
}

.mcp-gateway-test-tool-head {
  display: flex;
  align-items: center;
  gap: 6px;
  color: var(--td-brand-color, #0052d9);
}

.mcp-gateway-test-tool-name {
  font-weight: 500;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  color: var(--td-text-color-primary, #000);
}

.mcp-gateway-test-tool-desc {
  margin-top: 2px;
  font-size: 13px;
  color: var(--td-text-color-secondary, #6b7280);
}

.mcp-gateway-test-tool-params {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  margin-top: 6px;
}

.mcp-gateway-test-empty {
  padding: 24px;
  text-align: center;
  font-size: 13px;
  color: var(--td-text-color-secondary, #6b7280);
}
</style>
