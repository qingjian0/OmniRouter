# OmniRouter

轻量化、跨平台的统一 AI 接入、反向代理与调度网关。核心采用 Go 模块化单体，优先保持低依赖、低资源占用；管理界面采用 Vue 3 + TypeScript，构建后通过 Go embed 嵌入单一可执行文件；Node.js 仅用于前端构建，运行时不需要 Node.js。

## 当前实现状态

当前代码已从纯骨架推进到第一批可运行功能模块：

- Go HTTP 服务、优雅关闭、环境变量配置。
- `/healthz`、`/readyz` 健康端点。
- OpenAI-compatible Provider 适配器（非流式与 SSE 流式转发）。
- OpenAI-compatible `/v1/models`、`/v1/chat/completions` 网关入口。
- Anthropic `POST /v1/messages` 文本、图片输入、非流式及流式 tool_use/tool_result 转换；支持基础文本与工具调用 SSE 生命周期。
- Gemini `generateContent` / `streamGenerateContent` 基础协议转换，覆盖文本、图片输入、functionDeclarations/functionCall/functionResponse 与流式函数调用。
- 多 Provider JSON 环境配置、模型路由、优先级与健康候选加权轮询、健康检查缓存及指数退避故障冷却。
- 可选 Gateway Bearer API Key 认证。
- Provider、Model、Account、Session、Credential、API Key、请求历史和 Token 用量管理 API。
- Vue 3 + TypeScript 管理控制台，构建后嵌入 Go 二进制；运行时无 Node.js 依赖。
- 初始 SQLite Schema、纯 Go SQLite driver 声明及启动迁移接线；Provider 元数据、模型能力、Account/Session、Credential 密文及 API Key 元数据已接入 SQL Repository；Provider API Key 可通过 AES-GCM Credential Vault 绑定并在启动时恢复。
- 有界内存 HTTP 观测：请求 ID、状态码、延迟、在途数、状态分布、近似 P95 与最近请求列表。
- OpenAI-compatible 上游 HTTP transport：连接、TLS 握手、响应头阶段超时；不设置总请求超时以保留长连接 SSE。

****当前已完成：** Go 网关核心、SQLite 持久化、Provider/Model/Account/Session/Credential/API Key 管理 API、请求与 Token 用量记录、Vue 3 + TypeScript 管理控制台（已嵌入 Go 二进制）、SQLite 备份恢复脚本、跨平台发行包工作流和 `--version` 构建元数据。

**仍未完成的能力：** 原生 Anthropic/Gemini 出站 Provider 适配器、独立网页版登录会话适配器、完整任务/成本/延迟自适应调度、持久化凭据密钥轮换、音频/视频转换、完整模型能力自动发现、真实上游验收和 Android/Termux 真机验收。

**协议边界：** 当前出站 Provider 适配器是 OpenAI-compatible。Anthropic Messages 与 Gemini 是入站客户端协议，转换后通过 OpenAI-compatible 上游执行；这不等于直接支持 Anthropic/Gemini 官方上游 API。发布前必须按 [V1 发布清单](docs/V1-RELEASE-CHECKLIST.md) 对这一范围作出明确决定。

**验证状态（2026-11-05）：** GitHub Actions `OmniRouter CI` Run [41963844575](https://github.com/qingjian0/OmniRouter/actions/runs/41963844575) 全部通过：Vue 类型检查与构建、嵌入式静态资源服务测试、Go 格式检查、`go test ./...`、`go test -race ./...`、`go vet ./...`、SQLite 备份/恢复往返测试、版本链接参数验证，以及 Linux amd64/arm64、Windows amd64、macOS amd64/arm64 五个平台的二进制和发行包构建。

**发布状态：尚未发布 V1.0.0。** CI 和交叉编译通过不代表真实上游验收、Termux 真机验收或上述产品范围缺口已经解决。
## 运行

从源码运行完整 Vue 管理控制台前，先构建前端静态资源；预编译发行版不需要 Node.js：

```sh
cd web && npm install --no-audit --no-fund && npm run build
cd .. && go run ./cmd/omnirouter
```

默认监听 `127.0.0.1:8080`。不配置 Provider 时，健康端点和管理页面仍可访问，但模型请求不会有可用上游。

### 单 Provider 配置

```sh
export OMNI_ADDR=127.0.0.1:8080
export OMNI_GATEWAY_API_KEY='replace-with-a-long-random-key'
# Generate once with: openssl rand -base64 32; persist the same value across restarts.
export OMNI_CREDENTIAL_ENCRYPTION_KEY='replace-with-base64-encoded-32-byte-key'
export OMNI_PROVIDER_KIND=deepseek
export OMNI_PROVIDER_ID=deepseek
export OMNI_PROVIDER_NAME=DeepSeek
export OMNI_PROVIDER_BASE_URL=https://api.deepseek.com
export OMNI_PROVIDER_API_KEY='your-provider-api-key'
export OMNI_PROVIDER_MODELS=deepseek-flash,deepseek-v4-pro
# DeepSeek adapter discovers the live catalogue from GET /models.
```

### 多 Provider 配置

设置 `OMNI_PROVIDERS` 为 JSON 数组。每个 Provider 可通过 `kind` 选择适配器（`deepseek` 或 `openai-compatible`）；DeepSeek 官方 API 根地址为 `https://api.deepseek.com`，不带 `/v1`。其他 OpenAI-compatible 服务按其实际 API 根路径配置。相同模型 ID 可配置在多个 Provider 上，以便调度回退。

```sh
# Set OMNI_CREDENTIAL_ENCRYPTION_KEY as above before configuring upstream API keys.
export OMNI_PROVIDERS='[
  {"kind":"deepseek","id":"deepseek","name":"DeepSeek","base_url":"https://api.deepseek.com","api_key":"KEY_A","models":["deepseek-flash","deepseek-v4-pro"],"priority":100,"weight":1},
  {"id":"compatible-backup","name":"Compatible Backup","base_url":"https://example.invalid/v1","api_key":"KEY_B","models":["deepseek-flash"],"priority":90,"weight":1}
]'
```

生产环境请通过安全的环境变量注入机制提供密钥，不要把真实密钥提交到配置文件或仓库。请备份并长期保留 `OMNI_CREDENTIAL_ENCRYPTION_KEY`；更换该密钥会使既有密文无法解密。Provider 和 Credential 也可通过管理控制台写入受保护的持久化存储。

### Provider adapters

- DeepSeek official API: dedicated `kind: deepseek` adapter, official model discovery, current V4 model metadata, thinking/reasoning field pass-through, and SSE handling. See [DeepSeek adapter guide](docs/DEEPSEEK-ADAPTER.md) and the manual [DeepSeek live acceptance workflow](.github/workflows/deepseek-live-smoke.yml).
- Generic OpenAI-compatible endpoints remain available as `kind: openai-compatible`.
- DeepSeek consumer-web login/session support is a separate future adapter and is not part of the official API adapter.

### API

- `GET /healthz`
- `GET /readyz`
- `GET /v1/models`
- `POST /v1/chat/completions`
- `POST /v1/responses` (native DeepSeek Responses API)
- `POST /v1/messages`（Anthropic 文本及非流式工具调用子集；文本 SSE；流式工具调用暂不支持）
- `POST /v1beta/models/{model}:generateContent`（Gemini 文本及非流式函数调用子集）
- `POST /v1beta/models/{model}:streamGenerateContent`（Gemini 文本 SSE 子集；流式函数调用暂不支持）
- `GET /admin/providers`
- `GET /admin/models` — list models with declared capabilities and context limits
- `GET /admin/stats` — bounded in-memory request/latency/status summary
- `GET /admin/requests?limit=100` — recent sanitized HTTP request records (1–1000)
- `GET /` 管理页

设置 `OMNI_GATEWAY_API_KEY` 后，`/v1/*` 和 `/admin/*` 必须携带 `Authorization: Bearer <key>`。健康端点与管理静态页不要求该 Key。

## 构建

源码构建需要 Go 1.23+ 与 Node.js 22+：

```sh
cd web && npm install --no-audit --no-fund && npm run build
cd .. && go test ./... && go build ./cmd/omnirouter
```

Node.js 只用于 Vue 管理端构建；发布后的 Go 二进制已内嵌静态资源，运行时不需要 Node.js。当前 CI 会先构建 Vue 资产，再执行 Go 测试、交叉编译和发行包打包。Android/Termux 仍须真机验收。

## 设计原则

- 模块化单体，不拆微服务。
- Go 服务运行时不依赖 Node.js、Python 或 Java Runtime；Vue 管理端只在构建期需要 Node.js。
- Provider 适配与协议入口解耦。
- 按请求所需能力过滤模型候选，再结合优先级、权重和 Provider 健康状态调度，不依赖随机 map 遍历。
- 凭据不通过管理 API 返回。
- 流式请求不设置全局 WriteTimeout。
- CI 保持宽松：第一版功能完成后再集中构建、测试和跨平台验证。

### Historical implementation notes

The batch notes below preserve the evolution of the implementation. Statements that say SQL integration, request history, or provider recovery are still pending are historical snapshots and are superseded by the current implementation status and CI evidence at the top of this README.

### 协议兼容范围（当前）

Anthropic 与 Gemini 入口支持文本、图片输入、常用采样参数及工具调用映射。Anthropic `tool_use`/`tool_result` 与 Gemini `functionDeclarations`/`functionCall`/`functionResponse` 可映射到 OpenAI Chat Completions tools。Anthropic SSE 可增量输出 `tool_use` 与 `input_json_delta`；Gemini SSE 会按 OpenAI tool-call index 缓冲参数片段，并在工具参数完整后输出 `functionCall` JSON 对象。Gemini `functionResponse` 不含调用 ID，因此请求历史中的响应按函数名 FIFO 关联；同名并发调用依靠顺序关联，不能表达供应商协议本身未提供的任意调用 ID。音频/视频、多模态工具结果和供应商扩展字段仍未实现；遇到不支持的内容块会明确返回错误。流式响应一旦开始发送，后续上游错误无法再转换为常规 HTTP JSON 错误，只会影响 Provider 健康状态。这些入口复用已配置的 OpenAI-compatible Provider，因此上游模型 ID 必须在 Provider 的 `models` 列表中声明。

### Runtime provider administration

When `OMNI_GATEWAY_API_KEY` is configured, use its Bearer token for the admin endpoints:

- `GET /admin/providers`
- `GET /admin/models` — list models with declared capabilities and context limits
- `GET /admin/stats` — bounded in-memory request/latency/status summary
- `GET /admin/requests?limit=100` — recent sanitized HTTP request records (1–1000)
- `POST /admin/providers` — register/replace an OpenAI-compatible provider and its model routes
- `DELETE /admin/providers/{id}` — remove a provider from the running process
- `GET /admin/stats` — inspect bounded in-memory request telemetry
- `GET /admin/requests?limit=100` — inspect recent request metadata without bodies, query strings, or credentials

Model capabilities and context limits can be declared in provider configuration using `model_capabilities` and `model_context_limits`. Requests requiring streaming, tools, JSON output, or vision are filtered against declared capabilities before provider selection. Gemini streaming routes also require `streaming`. These are operator-supplied declarations, not automatically verified feature claims; context limits are currently exposed as metadata and are not yet used for token-budget enforcement. Provider metadata and model declarations are stored in SQLite. When `OMNI_CREDENTIAL_ENCRYPTION_KEY` is configured, provider API keys are encrypted in the Credential Vault and rebound during startup; the encryption key itself remains deployment-managed and must be backed up separately. Provider secrets are never returned by the admin listing endpoint.


### Image input interoperability (current batch)

- Anthropic user `image` blocks with `source.type=url` or `source.type=base64` are converted to OpenAI `image_url` content parts. Base64 sources must declare an `image/*` media type.
- Gemini user `inlineData` image parts are converted to data-URL `image_url` parts. Gemini `fileData` is accepted only for `image/*` MIME types and HTTP(S) URIs; provider-specific `gs://` URIs are rejected rather than forwarded to an upstream that may not be able to access them.
- Mixed text/image order is retained in the converted OpenAI content array. Image-only and image-plus-tool-call user/assistant message shapes are kept distinct.
- The gateway's capability filter recognizes Anthropic image blocks and Gemini `inlineData`/`fileData`; the selected model must declare `vision`. This declaration is metadata, not an upstream capability test.
- Audio, video, generated images, image tool results, and non-image file parts remain unsupported.

### Scheduler circuit-breaker update (current batch)

- Weighted round-robin now runs over healthy candidates only within the highest available priority group. An unhealthy candidate no longer causes traffic to collapse onto the first healthy entry in that group.
- Failed health checks and reported request failures enter an exponential cooldown (1, 2, 4, 8, 16, 32, 64, then 128 seconds; capped at five minutes). After cooldown, a single health probe can restore eligibility.
- Successful health probes reset the failure counter. This is a lightweight provider-level breaker; account-level concurrency limits and per-model token-budget enforcement remain pending.


### Runtime observability (new batch)

- Every HTTP request receives an `X-Request-ID` response header.
- A bounded ring buffer retains at most 1000 recent request metadata records; it does not store request/response bodies, query strings, authorization headers, or provider secrets.
- `GET /admin/stats` returns total requests, in-flight requests, status-class counts, average latency and an approximate P95 over the retained window.
- `GET /admin/requests?limit=N` returns recent records (1–1000).
- This telemetry is process-local and is lost on restart; durable request logs and token/cost accounting still require SQLite integration.
- The OpenAI-compatible HTTP transport bounds dial, TLS handshake and response-header waits, while intentionally leaving total request duration unbounded for SSE. Request cancellation remains controlled by the inbound context.

### Scheduler and provider validation update

Health checks are serialized per provider after cooldown expiry to prevent half-open probe stampedes. Provider base URLs must be absolute HTTP(S) URLs and may not embed user information, query strings, or fragments. Local and private-network model endpoints remain supported. Empty model IDs and negative context limits are rejected during provider construction. Build and runtime validation remain deferred until the V1 feature-complete checkpoint.

### Non-streaming provider failover

OpenAI-compatible chat completions, Anthropic Messages, and Gemini `generateContent` share bounded non-streaming failover. When an upstream generation fails, the gateway reports that provider unhealthy and asks the scheduler for another eligible provider, retaining the original model capability requirements and avoiding duplicate attempts within the same request. Streaming responses are not retried after the response has started because doing so could duplicate or corrupt already-delivered output.


### Runtime account metadata API

- `GET /admin/accounts?provider_id={id}` lists in-memory account metadata, optionally filtered by Provider.
- `POST /admin/accounts` creates an account metadata record; `id` is optional and generated when omitted. Required fields are `provider_id` and `name`.
- `GET /admin/accounts/{id}` reads one account.
- `PATCH /admin/accounts/{id}/status` changes status to `active`, `disabled`, or `error`.
- `DELETE /admin/accounts/{id}` deletes the runtime metadata record.
- A Provider cannot be deleted while runtime account records still reference it; remove those records first.

Example account creation (the Provider must already exist):

```bash
curl -X POST http://127.0.0.1:8765/admin/accounts \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer YOUR_GATEWAY_KEY' \
  -d '{"provider_id":"deepseek-primary","name":"Primary account"}'
```

This is deliberately metadata-only. It does not perform provider login, token refresh, cookie validation, or browser automation, and it does not yet affect account-level concurrency. With App SQL repositories configured, Account metadata survives restarts. Secrets are stored separately in the encrypted Credential Vault.

### Runtime account and session metadata

The admin API now exposes in-memory account and session metadata endpoints:

- `GET|POST /admin/accounts`, `GET|PATCH /admin/accounts/{id}/status`, and `DELETE /admin/accounts/{id}`
- `GET|POST /admin/sessions`, `GET /admin/sessions/{id}`, `PATCH /admin/sessions/{id}/status`, and `DELETE /admin/sessions/{id}`

Session status transitions are constrained: `unknown` may become `active`, `error`, `revoked`, or `expired`; `active` may become `expired`, `revoked`, or `error`; `error` may recover to `unknown` or `active`, or become `revoked`. `expired` and `revoked` are terminal. An active session with an elapsed `expires_at` is reported as expired. Account deletion is rejected while sessions remain attached.

These endpoints manage Account/Session metadata only. They do not perform provider login, token refresh, cookie validation, or upstream authorization checks. With App SQL repositories configured, Account/Session metadata is persisted across restarts. Credentials remain in the separate encrypted Credential Vault.

### Encrypted credential vault (SQLite-backed when configured)

- Added `internal/credential.Vault` with an in-memory compatibility mode and a SQLite-backed mode using the AES-256-GCM `security.Encryptor`. Supported credential kinds are `api_key`, `access_token`, `refresh_token`, `cookie`, and `username_password`.
- Secrets are encrypted before entering the vault map. Listing returns metadata only; plaintext is available only through the explicit internal `Reveal` method and is never part of a JSON response type.
- Associated authenticated data binds each ciphertext to its account ID, credential ID, and immutable credential kind. Changing account or kind requires creating a new credential ID.
- Replacing a secret increments its version. The vault supports local status changes (`active`, `disabled`, `revoked`, `expired`) and all-or-nothing in-memory key rotation to a replacement encryptor.
- When `OMNI_CREDENTIAL_ENCRYPTION_KEY` is configured, App constructs a persistent vault backed by the SQLite Credential Repository. Ciphertext, lifecycle status, expiry, and key version are stored; list operations return metadata only. Without the key, credential management is disabled. Account and Session metadata use SQLite repositories. Provider-owned credentials are linked to deterministic provider-owned Account records; disabling an Account prevents that Provider from being restored into the runtime registry.


## Credential management (SQLite-backed)

Set `OMNI_CREDENTIAL_ENCRYPTION_KEY` to a Base64-encoded 32-byte key to enable the runtime credential vault. Generate a key with `openssl rand -base64 32` or an equivalent cryptographically secure generator. If the variable is unset, credential management endpoints return `503`; if it is set but invalid, application initialization fails. Keep this key in a deployment secret store and do not commit it to source control. Provider configurations containing a non-empty `api_key` require this key; startup refuses to persist an upstream secret in plaintext or silently keep it only in memory.

Credential endpoints: `GET /admin/credentials?account_id=...`, `POST /admin/credentials`, `PATCH /admin/credentials/{id}/status`, and `DELETE /admin/credentials/{id}`. The create body accepts `account_id`, `kind`, `value`, and optional `expires_at`. The API returns metadata only and never provides a reveal endpoint. Credential endpoints are disabled unless `OMNI_GATEWAY_API_KEY` is set, and then require its Bearer token. Keep the service bound to loopback or place it behind TLS when exposed to a network.

With a valid encryption key, credential ciphertext and metadata survive process restarts. The same key must be supplied on every restart; losing or changing it makes existing ciphertext unreadable. Key rotation is not yet implemented for the persistent repository. Protect the management API with TLS or a trusted local reverse proxy; request bodies contain plaintext at the HTTP boundary before immediate encryption.

### SQL transaction and migration foundation (current batch)

- Added `storage.DBTX`, the common `database/sql` query surface implemented by both `*sql.DB` and `*sql.Tx`, for future repository reuse inside atomic workflows.
- Added `SQLStore.WithTx` to provide a single explicit transaction boundary with rollback on callback/commit failure.
- Migration filenames are now validated for positive, unique numeric versions and sorted numerically rather than lexically (so `2_...` precedes `10_...`).
- The pure-Go SQLite driver is registered and App startup opens the database, applies embedded migrations, and closes it during shutdown. SQL Repository runtime switching remains incomplete; business records must not yet be assumed durable.

### SQLite connection policy

`internal/storage.ConfigureSQLite` sets a bounded busy timeout, enables and verifies
foreign-key enforcement, and optionally enables WAL. The current implementation
pins `database/sql` to one connection so connection-scoped PRAGMAs cannot vary
across a pool. This serializes database access and is an interim correctness-first
policy. The migration SQL deliberately does not rely on setting `foreign_keys`
inside a migration transaction. The driver is `modernc.org/sqlite` (pure Go, no CGO).

## Database migration packaging

Database migrations are embedded in the Go binary from `internal/storage/migrations/`; deployments do not need a separate `migrations/` directory.

### Database runtime configuration

The configuration loader recognizes `OMNI_DATABASE_PATH` (default `data/omnirouter.db`),
`OMNI_DATABASE_BUSY_TIMEOUT` (default `5s`), and `OMNI_DATABASE_WAL` (default `true`).
App startup creates the parent directory, opens the pure-Go `modernc.org/sqlite` driver,
configures SQLite, applies embedded migrations, and closes the database during shutdown.
`storage.NewSQLiteRuntime` remains available for callers with an existing `database/sql`
handle; `storage.OpenSQLiteRuntime` owns path creation and driver opening.

The selected compatibility baseline is `modernc.org/sqlite v1.34.5` with matching
`modernc.org/libc v1.55.3`, for the Go 1.23 baseline. Dependency downloads and checksum
generation could not be completed because module-proxy DNS is unavailable in the current
execution environment. Android `GOOS=android` is not in the upstream supported-platform
matrix and is not claimed as supported. Business repositories still require runtime
switching before business data survives restarts.


### Provider 元数据持久化边界

Provider 的基础元数据（ID、名称、类型、Base URL、启用状态、优先级和权重）会写入 SQLite，并通过 `/admin/providers` 从数据库读取。Provider API Key 不写入 `providers` 表。独立 Credential 管理接口已可将凭据密文、状态、过期时间和版本持久化到 SQLite；但 Provider 创建流程会将 API Key 加密写入 Credential Vault，并在 Provider 元数据中保存 Account/Credential 关联 ID。启动时从 SQLite 恢复 Provider、模型能力和凭据；不会将明文密钥写入 Provider 表或管理列表。


### Account and Session persistence (current source state)

When the App is started with the configured SQLite runtime, `/admin/accounts` and `/admin/sessions` use SQL repositories rather than process-local stores. Account and Session metadata therefore survive process restarts once the selected SQLite driver dependency is available and the application can be built and run. Session creation rejects duplicate IDs; status transitions and expiry updates are persisted. Account deletion is blocked while sessions or credentials reference it. The in-memory Store path remains available to embedders using the legacy Handler constructors.

This source integration has not yet been build- or runtime-verified because the pure-Go SQLite module archive could not be downloaded in the current network environment.

### Persisted models and API keys

Configured provider models and their capability declarations are synchronized to SQLite during startup. The admin API exposes persisted model listing, enable/disable, and deletion (`/admin/models`). API keys can be created, listed, disabled, and deleted under `/admin/api-keys`; the raw generated secret is returned only once, while SQLite stores only its SHA-256 digest. These management endpoints require the bootstrap `OMNI_GATEWAY_API_KEY`. Persisted API keys are also accepted as bearer credentials when the master key is configured, subject to their exact route scopes (`gateway:read`, `gateway:write`, `admin:read`, `admin:write`).


### API Key scopes

Persisted gateway API keys support the following explicit scopes:

- `gateway:read`: read-only gateway endpoints such as `GET /v1/models`.
- `gateway:write`: generation endpoints under `/v1/` and `/v1beta/`.
- `admin:read`: read-only `/admin/` endpoints.
- `admin:write`: mutating `/admin/` endpoints.

When `OMNI_GATEWAY_API_KEY` is configured, it remains the master credential and bypasses scoped checks. Other persisted keys must carry the exact scope required by the route. Keys with no scopes, unknown scopes, or duplicate scopes are rejected. If the master key is unset, the existing explicitly open development mode remains enabled; configure the master key before exposing the service to a network.


Provider runtime policy can be changed without replacing its credential: `PATCH /admin/providers/{id}/policy` persists `enabled`, `priority`, and `weight`, then refreshes the scheduler's candidate set. A disabled provider remains configured but is excluded from routing; it can be re-enabled later.


### Persistent request history and usage summary

When SQLite is configured, the gateway records request ID, HTTP method, sanitized path, status code, latency, timestamp, provider/model attribution, and provider-reported token usage in `request_logs`. Request bodies, query strings, and authorization headers are not persisted. `GET /admin/requests` reads the durable history and `GET /admin/usage` returns request/error totals, known and final token totals, average latency, and provider/model breakdowns. Missing usage remains unknown; no token estimates are written as actual usage.

### R3.2 token usage accounting

R3.2 records only token counts explicitly reported by the upstream provider. OpenAI-compatible Chat Completions `usage.prompt_tokens`, `usage.completion_tokens`, and `usage.total_tokens` are parsed directly; native Anthropic Messages `usage.input_tokens` / `usage.output_tokens` and Gemini `usageMetadata` are supported by the shared parser for protocol adapters. Missing values remain unknown (SQL `NULL`), and an explicitly reported zero remains zero. No tokenizer-based estimate is written as provider usage.

Streaming requests are considered complete only after an explicit OpenAI-compatible `data: [DONE]` terminal event. Generic provider terminal events without that explicit marker do not produce a successful downstream terminal frame (including translated Anthropic/Gemini streams). Usage received before an interrupted or incomplete stream may be retained with `token_usage_final=false`; request lifecycle and provider/model attribution are persisted separately. Downstream response-write failures and request-context cancellation are classified as client disconnects rather than generic upstream failures. `/admin/usage` includes aggregate totals and a provider/model breakdown. The `token_usage_source` and `token_usage_final` fields distinguish provider-reported accounting from estimates and partial stream observations. The current gateway uses OpenAI-compatible upstream adapters; the Anthropic and Gemini parsers provide the shared protocol-specific usage shape for future native adapters, not evidence of native upstream adapter support today.

### R3.2 validation commands

The repository CI workflow runs module download/verification, formatting, unit and
SQLite integration tests, race tests, `go vet`, and the executable build. Run the same
checks locally with:

```bash
go mod download
go mod verify
test -z "$(gofmt -l $(find . -name '*.go' -type f))"
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/omnirouter
```

`internal/storage/request_logs_integration_test.go` uses the actual registered
`modernc.org/sqlite` driver and exercises embedded migrations 001–005, request usage
persistence, provider/model aggregation, and database reopen. It is intentionally
not replaced by a SQL mock. A successful compile-only stub check is not a substitute
for running this integration test with the real driver. Migration 005 separates inference
usage from administrative/health traffic: request history retains all routes, while usage
aggregates include only the three model-inference API families.


### Request-log persistence health (R3.2)

`GET /admin/stats` includes the process-lifetime `persistence_failures` counter. It increments when a completed request cannot be appended to the durable request-log sink. The counter intentionally exposes no raw database error text or request content. Request lifecycle records also fall back to `failed` for unclassified HTTP 4xx/5xx responses; recovered handler panics are recorded as `failed` with `handler_panic`, even if a 200 response was already committed.

The Recorder preserves `http.Flusher` only when supported by the underlying response writer, implements `io.ReaderFrom` without bypassing status accounting, and exposes `Unwrap()` for `http.ResponseController` capability discovery. It does not falsely advertise `http.Hijacker` or `http.Pusher`. SQLite migration execution increments a guaranteed-existing singleton lock row and rechecks each migration version inside its transaction, serializing concurrent process startup under the configured busy timeout.


R3.2 request-history queries use a shared 1,000-record maximum. Persisted records with identical timestamps are ordered deterministically by descending record ID, and oversized repository limits are clamped to the documented maximum rather than silently reset to the default page size.

R3.2 storage validation also includes a real-driver integration test for
transient SQLite write-lock recovery: an append blocked by an external writer
must fail within the configured busy timeout, and a later append must succeed
after the lock is released. This test remains pending execution where the
`modernc.org/sqlite` dependency can be downloaded.

The Gateway acceptance suite also contains
`internal/gateway/handler_sqlite_integration_test.go`, which drives a deterministic
completion through the production HTTP Handler → Recorder → SQLite Repository
path, checks `/admin/requests` and `/admin/usage`, and verifies persisted usage
after closing and reopening the database. It requires the real SQLite driver and
must be executed by the full CI run; a stub compile does not execute it.


R3.2 validation also includes a Gateway → Recorder → SQLite → Admin API end-to-end
test for non-streaming usage, completed SSE usage, and partial usage retained
after upstream SSE EOF. The real SQLite driver execution remains a release gate;
see `docs/R3.2-VALIDATION.md`.

Request token counts are stored as `int64` end-to-end, matching provider parsing
and SQLite INTEGER semantics; the persistence adapter does not narrow counts to
platform-sized `int`. A real-driver round-trip test covers values above the
signed 32-bit range and remains pending execution with the SQLite integration suite.
R3.2 usage accounting distinguishes inference traffic from administrative and health traffic. All HTTP requests remain in request history, while `/admin/usage` and provider/model breakdowns include only `POST /v1/chat/completions`, `POST /v1/messages`, and Gemini generate/streamGenerate endpoints. Migration `005_usage_eligibility.sql` marks pre-existing rows as unknown/ineligible rather than guessing their route.

R3.2 persistence validation also rejects negative input/output token counts at
the `RequestLogRepository` boundary, so invalid usage cannot be persisted by a
future caller that bypasses the provider usage parsers. See
[`docs/R3.2-VALIDATION.md`](docs/R3.2-VALIDATION.md) for executed checks and the
remaining real-SQLite/remote-CI release gates.

R3.2 usage reporting reads summary and provider/model breakdown from one read
transaction. SQLite aggregate integer overflow is treated as a storage error;
usage totals are never silently wrapped. Real-driver overflow and snapshot
integration tests are included in the release gate.

`/admin/stats` snapshots now copy recorder counters and the bounded status/latency
window under one read lock, preventing a mixed-time in-memory telemetry snapshot
when requests complete concurrently. This is distinct from `/admin/usage`, whose
persistent summary and provider/model breakdown are read in one SQLite transaction.

R3.2 transaction cleanup also defers rollback unconditionally in both
`SQLStore.WithTx` and `InTransaction`, ensuring callback panics cannot strand a
transaction on the single-connection SQLite runtime. The regression test is
included in the real-driver SQLite validation suite; see
[`docs/R3.2-VALIDATION.md`](docs/R3.2-VALIDATION.md).

The Recorder also treats request-log sink panics as persistence failures rather
than allowing a telemetry side effect to escape after the response. Sink panic
details are not exposed; if the handler itself panics, that original panic remains
the one propagated after the request record is attempted.


## V1 release and acceptance documents

- [Installation, configuration, first run and upgrade](docs/INSTALL.md)
- [V1.0.0 release checklist](docs/V1-RELEASE-CHECKLIST.md)
- [Live Provider acceptance](docs/PROVIDER-ACCEPTANCE.md)
- [Android / Termux device acceptance](docs/ANDROID-TERMUX-ACCEPTANCE.md)
- [SQLite backup and restore](docs/DATABASE-BACKUP-RESTORE.md)

The release workflow packages Linux amd64/arm64, Windows amd64, and macOS
amd64/arm64 binaries when a `v*` tag is pushed. It generates SHA-256 checksums
and publishes a GitHub Release only after all build jobs succeed. A passing
cross-build does not replace live Provider or Android/Termux device acceptance.
