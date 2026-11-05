<script setup lang="ts">
import { computed, onMounted, reactive, ref } from "vue";

type Row = Record<string, any>;
type ListRef = { value: Row[] };

const tabs = [
  { id: "overview", label: "总览", icon: "◫" },
  { id: "providers", label: "Provider", icon: "⇄" },
  { id: "models", label: "模型", icon: "◇" },
  { id: "accounts", label: "账户", icon: "◎" },
  { id: "sessions", label: "会话", icon: "◷" },
  { id: "credentials", label: "凭据", icon: "▣" },
  { id: "keys", label: "API Keys", icon: "⌘" },
  { id: "requests", label: "请求记录", icon: "≋" },
  { id: "usage", label: "用量统计", icon: "▥" }
] as const;

const activeTab = ref("overview");
const activeLabel = computed(() => tabs.find((item) => item.id === activeTab.value)?.label ?? "总览");
const gatewayKey = ref("");
const health = ref("检查中");
const loading = ref(false);
const error = ref("");
const notice = ref("");
const stats = ref<Row>({});
const usage = ref<Row>({});
const providers = ref<Row[]>([]);
const models = ref<Row[]>([]);
const accounts = ref<Row[]>([]);
const sessions = ref<Row[]>([]);
const credentials = ref<Row[]>([]);
const apiKeys = ref<Row[]>([]);
const requests = ref<Row[]>([]);
const oneTimeSecret = ref("");

const providerForm = reactive({
  id: "", name: "", base_url: "", api_key: "", models: "",
  priority: 100, weight: 1, enabled: true,
  capabilities: "{}"
});
const accountForm = reactive({ provider_id: "", name: "" });
const sessionForm = reactive({ account_id: "", status: "unknown", expires_at: "" });
const credentialForm = reactive({ account_id: "", kind: "api_key", value: "", expires_at: "" });
const apiKeyForm = reactive({ name: "" });
const scopes = ref(["gateway:read", "gateway:write"]);
const availableScopes = ["gateway:read", "gateway:write", "admin:read", "admin:write"];

async function api(path: string, method = "GET", payload?: unknown): Promise<Row> {
  const headers: Record<string, string> = {};
  if (gatewayKey.value.trim()) headers.Authorization = `Bearer ${gatewayKey.value.trim()}`;
  if (payload !== undefined) headers["Content-Type"] = "application/json";
  const response = await fetch(path, {
    method,
    headers,
    body: payload === undefined ? undefined : JSON.stringify(payload)
  });
  const raw = await response.text();
  let result: Row = {};
  try { result = raw ? JSON.parse(raw) as Row : {}; } catch { result = { message: raw }; }
  if (!response.ok) {
    throw new Error(result?.error?.message || result?.message || `HTTP ${response.status}`);
  }
  return result;
}

function messageOf(value: unknown): string {
  return value instanceof Error ? value.message : String(value);
}

async function loadList(path: string, target: ListRef, failures: string[]) {
  try {
    const result = await api(path);
    target.value = Array.isArray(result.data) ? result.data : [];
  } catch (e) {
    failures.push(`${path}: ${messageOf(e)}`);
  }
}

async function refresh() {
  loading.value = true;
  error.value = "";
  const failures: string[] = [];
  try {
    try {
      const result = await api("/healthz");
      health.value = result.status === "ok" ? "运行正常" : "异常";
    } catch (e) {
      health.value = "无法连接";
      failures.push(`/healthz: ${messageOf(e)}`);
    }
    await Promise.all([
      loadList("/admin/providers", providers, failures),
      loadList("/admin/models", models, failures),
      loadList("/admin/accounts", accounts, failures),
      loadList("/admin/sessions", sessions, failures),
      loadList("/admin/credentials", credentials, failures),
      loadList("/admin/api-keys", apiKeys, failures),
      loadList("/admin/requests?limit=100", requests, failures)
    ]);
    try { stats.value = await api("/admin/stats"); } catch (e) { failures.push(`/admin/stats: ${messageOf(e)}`); }
    try { usage.value = await api("/admin/usage"); } catch (e) { failures.push(`/admin/usage: ${messageOf(e)}`); }
    if (failures.length) error.value = failures.join("；");
    else notice.value = "数据已刷新";
  } finally {
    loading.value = false;
  }
}

async function mutate(path: string, method: string, payload: unknown, success: string) {
  error.value = "";
  notice.value = "";
  try {
    const result = await api(path, method, payload);
    if (result.secret) oneTimeSecret.value = result.secret;
    notice.value = success;
    await refresh();
  } catch (e) {
    error.value = messageOf(e);
  }
}

async function createProvider() {
  try {
    const capabilities = JSON.parse(providerForm.capabilities || "{}");
    await mutate("/admin/providers", "POST", {
      id: providerForm.id.trim(),
      name: providerForm.name.trim(),
      base_url: providerForm.base_url.trim(),
      api_key: providerForm.api_key,
      models: providerForm.models.split(",").map((v) => v.trim()).filter(Boolean),
      model_capabilities: capabilities,
      priority: providerForm.priority,
      weight: providerForm.weight,
      enabled: providerForm.enabled
    }, "Provider 已保存");
    Object.assign(providerForm, { id: "", name: "", base_url: "", api_key: "", models: "", priority: 100, weight: 1, enabled: true, capabilities: "{}" });
  } catch (e) {
    error.value = `Provider 能力 JSON 无效：${messageOf(e)}`;
  }
}

function saveProviderPolicy(p: Row) {
  return mutate(`/admin/providers/${encodeURIComponent(p.id)}/policy`, "PATCH", {
    enabled: Boolean(p.enabled),
    priority: Number(p.priority),
    weight: Number(p.weight)
  }, "Provider 调度策略已更新");
}
function removeProvider(p: Row) {
  if (window.confirm(`确定删除 Provider ${p.name || p.id}？`)) {
    return mutate(`/admin/providers/${encodeURIComponent(p.id)}`, "DELETE", undefined, "Provider 已删除");
  }
}
async function createAccount() {
  await mutate("/admin/accounts", "POST", accountForm, "账户已创建");
  Object.assign(accountForm, { provider_id: "", name: "" });
}
function setAccountStatus(a: Row, status: string) {
  return mutate(`/admin/accounts/${encodeURIComponent(a.id)}/status`, "PATCH", { status }, "账户状态已更新");
}
function removeAccount(a: Row) {
  if (window.confirm(`确定删除账户 ${a.name || a.id}？请先处理关联会话和凭据。`)) {
    return mutate(`/admin/accounts/${encodeURIComponent(a.id)}`, "DELETE", undefined, "账户已删除");
  }
}
function isoDate(value: string): string | undefined {
  return value ? new Date(value).toISOString() : undefined;
}
async function createSession() {
  await mutate("/admin/sessions", "POST", {
    account_id: sessionForm.account_id,
    status: sessionForm.status,
    ...(isoDate(sessionForm.expires_at) ? { expires_at: isoDate(sessionForm.expires_at) } : {})
  }, "会话已创建");
  Object.assign(sessionForm, { account_id: "", status: "unknown", expires_at: "" });
}
function setSessionStatus(s: Row, status: string) {
  return mutate(`/admin/sessions/${encodeURIComponent(s.id)}/status`, "PATCH", { status }, "会话状态已更新");
}
function removeSession(s: Row) {
  if (window.confirm(`确定删除会话 ${s.id}？`)) {
    return mutate(`/admin/sessions/${encodeURIComponent(s.id)}`, "DELETE", undefined, "会话已删除");
  }
}
async function createCredential() {
  await mutate("/admin/credentials", "POST", {
    account_id: credentialForm.account_id,
    kind: credentialForm.kind,
    value: credentialForm.value,
    ...(isoDate(credentialForm.expires_at) ? { expires_at: isoDate(credentialForm.expires_at) } : {})
  }, "凭据已加密保存；列表不会显示明文");
  Object.assign(credentialForm, { account_id: "", kind: "api_key", value: "", expires_at: "" });
}
function setCredentialStatus(c: Row, status: string) {
  return mutate(`/admin/credentials/${encodeURIComponent(c.id)}/status`, "PATCH", { status }, "凭据状态已更新");
}
function removeCredential(c: Row) {
  if (window.confirm("确定删除该凭据？此操作不可撤销。")) {
    return mutate(`/admin/credentials/${encodeURIComponent(c.id)}`, "DELETE", undefined, "凭据已删除");
  }
}
async function createAPIKey() {
  await mutate("/admin/api-keys", "POST", { name: apiKeyForm.name.trim(), scopes: scopes.value }, "API Key 已创建。密钥只显示一次，请立即保存。");
  apiKeyForm.name = "";
}
function setAPIKeyEnabled(k: Row, enabled: boolean) {
  return mutate(`/admin/api-keys/${encodeURIComponent(k.id)}/status`, "PATCH", { enabled }, "API Key 状态已更新");
}
function removeAPIKey(k: Row) {
  if (window.confirm(`确定删除 API Key ${k.name || k.id}？`)) {
    return mutate(`/admin/api-keys/${encodeURIComponent(k.id)}`, "DELETE", undefined, "API Key 已删除");
  }
}
function setModelEnabled(m: Row, enabled: boolean) {
  return mutate(`/admin/models/${encodeURIComponent(m.id)}/status`, "PATCH", {
    provider_id: m.provider_id,
    enabled
  }, "模型状态已更新");
}
function removeModel(m: Row) {
  if (window.confirm(`确定删除模型 ${m.id} 在 Provider ${m.provider_id} 下的路由？`)) {
    return mutate(`/admin/models/${encodeURIComponent(m.id)}?provider_id=${encodeURIComponent(m.provider_id)}`, "DELETE", undefined, "模型路由已删除");
  }
}
function eventValue(event: Event): string {\n  return event.target instanceof HTMLSelectElement ? event.target.value : \"\";\n}\nasync function copySecret() {
  try {
    await navigator.clipboard.writeText(oneTimeSecret.value);
    notice.value = "密钥已复制";
  } catch {
    error.value = "浏览器未授予剪贴板权限，请手动复制密钥";
  }
}
function json(value: unknown) {
  return JSON.stringify(value ?? {}, null, 2);
}
function shortTime(value: string | undefined) {
  if (!value) return "—";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}
onMounted(refresh);
</script>

<template>
  <div class="app-shell">
    <header class="topbar">
      <div class="brand">
        <div class="brand-mark">O</div>
        <div><strong>OmniRouter</strong><small>统一 AI 接入与调度网关</small></div>
      </div>
      <div class="connection">
        <span class="health" :class="{ down: health !== '运行正常' }">{{ health }}</span>
        <input v-model="gatewayKey" type="password" autocomplete="off" placeholder="Gateway API Key" aria-label="Gateway API Key" />
        <button class="primary" :disabled="loading" @click="refresh">{{ loading ? "刷新中…" : "连接 / 刷新" }}</button>
      </div>
    </header>

    <div class="workspace">
      <aside class="sidebar">
        <div class="side-label">控制台</div>
        <button v-for="tab in tabs" :key="tab.id" class="nav-item" :class="{ active: activeTab === tab.id }" @click="activeTab = tab.id">
          <span>{{ tab.icon }}</span>{{ tab.label }}
        </button>
        <div class="side-foot">V1 管理控制台<br /><small>凭据仅写入加密 Vault</small></div>
      </aside>

      <main class="main">
        <div class="page-heading">
          <div><p class="eyebrow">OMNIROUTER / ADMIN</p><h1>{{ activeLabel }}</h1><p class="subheading">管理 Provider、模型路由、凭据与网关运行情况。</p></div>
          <button class="secondary" :disabled="loading" @click="refresh">刷新数据</button>
        </div>

        <div v-if="error" class="alert error">{{ error }}</div>
        <div v-if="notice" class="alert success">{{ notice }}</div>

        <section v-if="oneTimeSecret" class="secret-panel">
          <div><strong>新 API Key（仅显示一次）</strong><p>请立即复制并安全保存。离开页面后不会再次返回明文。</p></div>
          <code>{{ oneTimeSecret }}</code>
          <div class="secret-actions"><button class="primary" @click="copySecret">复制密钥</button><button class="secondary" @click="oneTimeSecret = ''">我已保存</button></div>
        </section>

        <section v-if="activeTab === 'overview'" class="stack">
          <div class="metric-grid">
            <article class="metric"><span>Provider</span><strong>{{ providers.length }}</strong><small>已登记上游</small></article>
            <article class="metric"><span>模型路由</span><strong>{{ models.length }}</strong><small>已登记模型</small></article>
            <article class="metric"><span>请求总数</span><strong>{{ stats.total_requests ?? stats.total ?? "—" }}</strong><small>当前统计窗口 / 生命周期</small></article>
            <article class="metric"><span>进行中</span><strong>{{ stats.in_flight ?? "—" }}</strong><small>当前在途请求</small></article>
          </div>
          <div class="panel-grid">
            <article class="panel"><div class="panel-title"><h2>运行统计</h2><span class="muted">/admin/stats</span></div><pre>{{ json(stats) }}</pre></article>
            <article class="panel"><div class="panel-title"><h2>Token 用量</h2><span class="muted">/admin/usage</span></div><pre>{{ json(usage) }}</pre></article>
          </div>
          <article class="panel"><div class="panel-title"><h2>最近请求</h2><button class="text-button" @click="activeTab = 'requests'">查看全部 →</button></div>
            <div class="table-wrap"><table><thead><tr><th>时间</th><th>路径</th><th>状态</th><th>Provider / Model</th><th>耗时</th><th>生命周期</th></tr></thead>
              <tbody><tr v-for="r in requests.slice(0, 8)" :key="r.request_id"><td>{{ shortTime(r.started_at || r.created_at) }}</td><td>{{ r.path }}</td><td>{{ r.status_code }}</td><td>{{ r.provider_id || "—" }} / {{ r.model_id || "—" }}</td><td>{{ r.latency_ms ?? "—" }} ms</td><td>{{ r.lifecycle_status || "—" }}</td></tr><tr v-if="!requests.length"><td colspan="6" class="empty">暂无请求记录</td></tr></tbody>
            </table></div>
          </article>
        </section>

        <section v-else-if="activeTab === 'providers'" class="stack">
          <article class="panel">
            <div class="panel-title"><h2>添加 Provider</h2><span class="muted">OpenAI-compatible 上游</span></div>
            <form class="form-grid" @submit.prevent="createProvider">
              <label>ID<input v-model="providerForm.id" required placeholder="deepseek-primary" /></label>
              <label>名称<input v-model="providerForm.name" placeholder="DeepSeek Primary" /></label>
              <label class="wide">Base URL<input v-model="providerForm.base_url" required placeholder="https://api.example.com/v1" /></label>
              <label class="wide">上游 API Key<input v-model="providerForm.api_key" type="password" autocomplete="new-password" placeholder="仅在提交时发送，服务端加密保存" /></label>
              <label class="wide">模型 ID（逗号分隔）<input v-model="providerForm.models" required placeholder="model-a,model-b" /></label>
              <label>优先级<input v-model.number="providerForm.priority" type="number" min="0" /></label>
              <label>权重<input v-model.number="providerForm.weight" type="number" min="0" max="1000000" /></label>
              <label class="wide">模型能力 JSON（可选）<textarea v-model="providerForm.capabilities" rows="2" spellcheck="false"></textarea></label>
              <label class="check"><input v-model="providerForm.enabled" type="checkbox" />启用</label>
              <div class="form-actions"><button class="primary" type="submit">保存 Provider</button></div>
            </form>
            <p class="hint">配置上游密钥前，必须设置 OMNI_CREDENTIAL_ENCRYPTION_KEY。列表不会返回密钥明文。</p>
          </article>
          <article class="panel">
            <div class="panel-title"><h2>Provider 列表</h2><span class="muted">{{ providers.length }} 项</span></div>
            <div class="table-wrap"><table><thead><tr><th>Provider</th><th>Base URL</th><th>启用</th><th>优先级</th><th>权重</th><th>操作</th></tr></thead>
              <tbody><tr v-for="p in providers" :key="p.id"><td><b>{{ p.name || p.id }}</b><small>{{ p.id }}</small></td><td class="url">{{ p.base_url || "—" }}</td><td><input v-model="p.enabled" type="checkbox" /></td><td><input v-model.number="p.priority" class="small-input" type="number" min="0" /></td><td><input v-model.number="p.weight" class="small-input" type="number" min="0" /></td><td class="actions"><button class="secondary" @click="saveProviderPolicy(p)">保存策略</button><button class="danger" @click="removeProvider(p)">删除</button></td></tr><tr v-if="!providers.length"><td colspan="6" class="empty">暂无 Provider</td></tr></tbody>
            </table></div>
          </article>
        </section>

        <section v-else-if="activeTab === 'models'" class="stack">
          <article class="panel"><div class="panel-title"><h2>模型路由</h2><span class="muted">{{ models.length }} 项</span></div>
            <div class="table-wrap"><table><thead><tr><th>模型</th><th>Provider</th><th>能力</th><th>上下文</th><th>启用</th><th>操作</th></tr></thead>
              <tbody><tr v-for="m in models" :key="m.provider_id + ':' + m.id"><td><b>{{ m.display_name || m.id }}</b><small>{{ m.id }}</small></td><td>{{ m.provider_id }}</td><td>{{ (m.capabilities || []).join(", ") || "—" }}</td><td>{{ m.context_limit || "—" }}</td><td>{{ m.enabled ? "是" : "否" }}</td><td class="actions"><button class="secondary" @click="setModelEnabled(m, !m.enabled)">{{ m.enabled ? "停用" : "启用" }}</button><button class="danger" @click="removeModel(m)">删除路由</button></td></tr><tr v-if="!models.length"><td colspan="6" class="empty">暂无模型</td></tr></tbody>
            </table></div>
          </article>
        </section>

        <section v-else-if="activeTab === 'accounts'" class="stack">
          <article class="panel"><div class="panel-title"><h2>创建账户</h2></div>
            <form class="form-grid" @submit.prevent="createAccount">
              <label>Provider<select v-model="accountForm.provider_id" required><option value="" disabled>选择 Provider</option><option v-for="p in providers" :key="p.id" :value="p.id">{{ p.name || p.id }}</option></select></label>
              <label>账户名称<input v-model="accountForm.name" required placeholder="Primary account" /></label>
              <div class="form-actions"><button class="primary" type="submit">创建账户</button></div>
            </form>
          </article>
          <article class="panel"><div class="panel-title"><h2>账户列表</h2></div>
            <div class="table-wrap"><table><thead><tr><th>名称 / ID</th><th>Provider</th><th>状态</th><th>创建时间</th><th>操作</th></tr></thead>
              <tbody><tr v-for="a in accounts" :key="a.id"><td><b>{{ a.name }}</b><small>{{ a.id }}</small></td><td>{{ a.provider_id }}</td><td><select :value="a.status" @change="setAccountStatus(a, eventValue($event))"><option>active</option><option>disabled</option><option>error</option></select></td><td>{{ shortTime(a.created_at) }}</td><td><button class="danger" @click="removeAccount(a)">删除</button></td></tr><tr v-if="!accounts.length"><td colspan="5" class="empty">暂无账户</td></tr></tbody>
            </table></div>
          </article>
        </section>

        <section v-else-if="activeTab === 'sessions'" class="stack">
          <article class="panel"><div class="panel-title"><h2>创建会话元数据</h2></div>
            <form class="form-grid" @submit.prevent="createSession">
              <label>账户<select v-model="sessionForm.account_id" required><option value="" disabled>选择账户</option><option v-for="a in accounts" :key="a.id" :value="a.id">{{ a.name }} · {{ a.id }}</option></select></label>
              <label>初始状态<select v-model="sessionForm.status"><option>unknown</option><option>active</option><option>error</option></select></label>
              <label>过期时间<input v-model="sessionForm.expires_at" type="datetime-local" /></label>
              <div class="form-actions"><button class="primary" type="submit">创建会话</button></div>
            </form>
            <p class="hint">此处管理本地会话生命周期元数据，不执行上游登录、Cookie 验证或 Token 刷新。</p>
          </article>
          <article class="panel"><div class="panel-title"><h2>会话列表</h2></div>
            <div class="table-wrap"><table><thead><tr><th>ID</th><th>账户</th><th>状态</th><th>过期时间</th><th>操作</th></tr></thead>
              <tbody><tr v-for="s in sessions" :key="s.id"><td>{{ s.id }}</td><td>{{ s.account_id }}</td><td><select :value="s.status" @change="setSessionStatus(s, eventValue($event))"><option>unknown</option><option>active</option><option>error</option><option>expired</option><option>revoked</option></select></td><td>{{ shortTime(s.expires_at) }}</td><td><button class="danger" @click="removeSession(s)">删除</button></td></tr><tr v-if="!sessions.length"><td colspan="5" class="empty">暂无会话</td></tr></tbody>
            </table></div>
          </article>
        </section>

        <section v-else-if="activeTab === 'credentials'" class="stack">
          <article class="panel"><div class="panel-title"><h2>添加加密凭据</h2></div>
            <form class="form-grid" @submit.prevent="createCredential">
              <label>所属账户<select v-model="credentialForm.account_id" required><option value="" disabled>选择账户</option><option v-for="a in accounts" :key="a.id" :value="a.id">{{ a.name }} · {{ a.id }}</option></select></label>
              <label>类型<select v-model="credentialForm.kind"><option>api_key</option><option>access_token</option><option>refresh_token</option><option>cookie</option><option>username_password</option></select></label>
              <label class="wide">凭据值<input v-model="credentialForm.value" type="password" autocomplete="new-password" required /></label>
              <label>过期时间<input v-model="credentialForm.expires_at" type="datetime-local" /></label>
              <div class="form-actions"><button class="primary" type="submit">加密保存</button></div>
            </form>
            <p class="hint">凭据值只在提交时发送。服务端使用 AES-256-GCM 加密；读取接口只返回元数据。</p>
          </article>
          <article class="panel"><div class="panel-title"><h2>凭据列表</h2></div>
            <div class="table-wrap"><table><thead><tr><th>ID</th><th>账户</th><th>类型</th><th>状态</th><th>版本</th><th>操作</th></tr></thead>
              <tbody><tr v-for="c in credentials" :key="c.id"><td>{{ c.id }}</td><td>{{ c.account_id }}</td><td>{{ c.kind }}</td><td><select :value="c.status" @change="setCredentialStatus(c, eventValue($event))"><option>active</option><option>disabled</option><option>revoked</option><option>expired</option></select></td><td>{{ c.version }}</td><td><button class="danger" @click="removeCredential(c)">删除</button></td></tr><tr v-if="!credentials.length"><td colspan="6" class="empty">暂无凭据或凭据 Vault 尚未启用</td></tr></tbody>
            </table></div>
          </article>
        </section>

        <section v-else-if="activeTab === 'keys'" class="stack">
          <article class="panel"><div class="panel-title"><h2>创建 Gateway API Key</h2></div>
            <form class="form-grid" @submit.prevent="createAPIKey">
              <label>名称<input v-model="apiKeyForm.name" required placeholder="Read-only dashboard" /></label>
              <fieldset class="scope-box"><legend>权限范围</legend><label v-for="scope in availableScopes" :key="scope" class="check"><input v-model="scopes" type="checkbox" :value="scope" />{{ scope }}</label></fieldset>
              <div class="form-actions"><button class="primary" type="submit">创建 API Key</button></div>
            </form>
            <p class="hint">新密钥只在创建成功时返回一次。请安全保存，服务端仅持久化 SHA-256 摘要。</p>
          </article>
          <article class="panel"><div class="panel-title"><h2>API Key 列表</h2></div>
            <div class="table-wrap"><table><thead><tr><th>名称 / ID</th><th>Scopes</th><th>状态</th><th>最近使用</th><th>操作</th></tr></thead>
              <tbody><tr v-for="k in apiKeys" :key="k.id"><td><b>{{ k.name }}</b><small>{{ k.id }}</small></td><td>{{ (k.scopes || []).join(", ") }}</td><td>{{ k.enabled ? "启用" : "停用" }}</td><td>{{ shortTime(k.last_used_at) }}</td><td class="actions"><button class="secondary" @click="setAPIKeyEnabled(k, !k.enabled)">{{ k.enabled ? "停用" : "启用" }}</button><button class="danger" @click="removeAPIKey(k)">删除</button></td></tr><tr v-if="!apiKeys.length"><td colspan="5" class="empty">暂无 API Key</td></tr></tbody>
            </table></div>
          </article>
        </section>

        <section v-else-if="activeTab === 'requests'" class="stack">
          <article class="panel"><div class="panel-title"><h2>最近 100 条请求</h2><span class="muted">请求正文和凭据不会被展示</span></div>
            <div class="table-wrap"><table><thead><tr><th>时间</th><th>请求 ID</th><th>方法 / 路径</th><th>HTTP</th><th>Provider / Model</th><th>Token</th><th>生命周期 / 错误</th></tr></thead>
              <tbody><tr v-for="r in requests" :key="r.request_id"><td>{{ shortTime(r.started_at || r.created_at) }}</td><td>{{ r.request_id }}</td><td>{{ r.method }} {{ r.path }}</td><td>{{ r.status_code }}</td><td>{{ r.provider_id || "—" }} / {{ r.model_id || "—" }}</td><td>{{ r.input_tokens ?? "—" }} / {{ r.output_tokens ?? "—" }}</td><td>{{ r.lifecycle_status || "—" }} / {{ r.error_code || "—" }}</td></tr><tr v-if="!requests.length"><td colspan="7" class="empty">暂无请求记录</td></tr></tbody>
            </table></div>
          </article>
        </section>

        <section v-else-if="activeTab === 'usage'" class="stack">
          <article class="panel"><div class="panel-title"><h2>用量汇总</h2><span class="muted">只统计上游明确报告的 Token</span></div><pre>{{ json(usage.data || usage) }}</pre></article>
          <article class="panel"><div class="panel-title"><h2>Provider / Model 分布</h2></div><pre>{{ json(usage.by_provider_model || []) }}</pre></article>
          <p class="hint">{{ usage.token_usage_note || "缺少上游 usage 时不会推算 Token 用量。" }}</p>
        </section>

        <footer class="footer">OmniRouter · V1 Admin · 管理操作由同源 /admin API 提供</footer>
      </main>
    </div>
  </div>
</template>
