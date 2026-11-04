# V1 implementation batches

## Batch A — Core gateway and provider routing

Implemented in source (not yet build/test verified):
- HTTP application lifecycle and environment configuration.
- OpenAI-compatible provider adapter.
- `/v1/models` and `/v1/chat/completions` including SSE forwarding.
- Multi-provider configuration via `OMNI_PROVIDERS`.
- Model-based provider selection with priority, weighted round-robin, health cache, and fallback.
- Optional bearer-key protection for model and provider-admin APIs.
- Embedded lightweight admin page and provider metadata endpoint.
- AES-256-GCM credential encryption primitive and API-key hashing/verification helpers.

## Batch B — Persistence foundation

Implemented in source (not yet wired into application startup):
- Initial SQLite schema for providers, accounts, credentials, sessions, models, capabilities, API keys, conversations, messages, request logs, scheduler logs, and settings.
- Generic `database/sql` store wrapper and numbered migration runner.

Pending: complete business repository runtime switching and transactional CRUD; validate the selected SQLite driver on each target; finalize backup/recovery behavior.

## Remaining V1 batches

- Credential encryption key configuration/rotation and persistent API-key management.
- Persistent Provider / Account / Session / Model repositories and management APIs.
- Anthropic Messages and Gemini protocol adapters, with explicit capability mapping.
- Provider health, retries, circuit breaking, request deadlines, and observable scheduler decisions.
- Request logs, usage accounting, and admin dashboard.
- Additional first-party provider presets and compatibility documentation.
- Final build, tests, cross-platform checks, CI, and release packaging (deferred until V1 feature work is complete; the starter CI workflow has been removed for now).

## Batch C — Domain repositories and gateway hardening

Implemented in source (not yet build/test verified):
- SQL repositories for Provider metadata, Account CRUD/status, Session save/read/revoke, Model metadata/capabilities, encrypted Credentials, hashed API keys, and recent request logs.
- Credential repository binds AES-GCM associated data to account ID + credential ID + credential type, so ciphertext cannot be moved between records without authentication failure.
- API-key repository generates high-entropy `omni_` secrets, persists only SHA-256 digests, returns the raw secret only at creation, and supports enable/disable and last-used tracking.
- Request body size is rejected explicitly above 8 MiB rather than silently forwarding a truncated payload.
- OpenAI-compatible SSE forwarding preserves complete SSE event blocks instead of inserting a blank line after every individual upstream line.
- Streaming upstream failures mark the selected provider unhealthy for subsequent scheduler decisions.

Known integration gap: SQL repositories use `database/sql`, and the pure-Go SQLite driver is registered and opened during App startup. The application applies embedded migrations and closes the database at shutdown. Provider metadata and encrypted credential records are now wired to SQL. Account, Session, Model, API Key, request-log runtime integration and Provider-to-Credential restoration are still incomplete. The driver dependency has not yet been downloaded or build-verified in this environment.

## Batch D — Cross-protocol streaming adapters

Implemented in source (not yet build/test verified):
- Anthropic Messages `stream: true` conversion from OpenAI-compatible SSE chunks into the core Anthropic message lifecycle events (`message_start`, text block events, `message_delta`, `message_stop`).
- Gemini `:streamGenerateContent` route conversion from OpenAI-compatible SSE chunks into Gemini candidate response SSE payloads.
- Stream translation retains usage metadata when the upstream emits it and normalizes `[DONE]` handling at the gateway boundary.

Known limits:
- Text-only streams; tool-call deltas, multimodal parts, refusal annotations, provider-specific chunk extensions, and every edge-case event variant are not yet mapped.
- Once HTTP streaming headers are committed, upstream failures can only affect provider health; they cannot be converted into a normal JSON HTTP error response.
- These adapters are source-complete for the documented subset but have not been exercised against live provider streams.

## Runtime Provider Administration (current batch)

The gateway now exposes authenticated runtime provider management endpoints:

- `GET /admin/providers` lists registered providers without exposing credentials.
- `GET /admin/models` lists registered model metadata, declared capabilities and context limits.
- `POST /admin/providers` registers or replaces an OpenAI-compatible provider and its model routes.
- `DELETE /admin/providers/{id}` removes a provider from the registry and scheduler routes only when no runtime account metadata references it; otherwise it returns HTTP 409.

Example request body:

```json
{
  "id": "deepseek-primary",
  "name": "DeepSeek Primary",
  "base_url": "https://api.deepseek.com/v1",
  "api_key": "replace-with-secret",
  "models": ["deepseek-chat", "deepseek-reasoner"],
  "model_capabilities": {"deepseek-chat": ["chat", "streaming", "tools", "json"]},
  "model_context_limits": {"deepseek-chat": 65536},
  "priority": 100,
  "weight": 2,
  "enabled": true
}
```

[Superseded by the persistent credential batches below.] Provider metadata mutations are written to SQLite. Current provider API keys are stored through the persistent AES-GCM Credential Vault when configured; metadata responses never include secret values.


## Batch E — Capability-aware provider routing

Implemented in source (not yet build/test verified):
- Scheduler candidates now carry per-model declared capabilities and context-window metadata.
- Startup route construction and runtime Provider replacement preserve model capability declarations.
- Gateway detects declared request requirements for streaming, tools, JSON response mode, and vision payloads, then filters candidate Providers before priority/weighted selection and health checks.
- Gemini streaming endpoints explicitly require the `streaming` capability.
- Capability declarations remain operator-supplied metadata; they are not live-tested against upstreams. Context limits are exposed but are not yet enforced against estimated token usage.

Known limits:
- Capability detection is conservative structural inspection of the request JSON, not a complete semantic parser for every vendor extension.
- Audio and video capability routing is not yet enabled.
- Non-streaming and streaming tool calls are translated for Anthropic and Gemini in the batches below. Capability filtering still requires declared tool support.
- This batch has only been formatted with `gofmt`; build, tests, and live-provider validation remain deferred until the V1 feature set is complete.


## Batch: non-streaming tool calling interoperability

- Anthropic Messages now maps `tools`/`tool_choice`, assistant `tool_use`, and user `tool_result` to OpenAI Chat Completions tools and tool messages. OpenAI tool-call responses map back to Anthropic `tool_use` content blocks.
- Gemini `functionDeclarations`, `functionCall`, and `functionResponse` map to the corresponding OpenAI tools representation; OpenAI tool-call responses map back to Gemini `functionCall` parts.
- Anthropic streaming translates OpenAI tool-call deltas into `tool_use` content blocks and `input_json_delta` events.
- Gemini streaming buffers function-call argument fragments by OpenAI tool-call index and emits complete Gemini `functionCall` parts when the assistant turn finishes, because Gemini parts require structured JSON arguments.
- Gemini historical `functionResponse` messages are correlated to preceding same-name calls in FIFO order; Gemini's wire format does not carry the original OpenAI tool-call ID.
- SQLite driver/runtime integration remains blocked: the environment has no cached SQLite Go driver and DNS access to `proxy.golang.org` fails. No database persistence is claimed.
- No build, test suite, CI, or live provider integration was run in this batch, per the agreed sequencing.


## Batch: streaming tool-call interoperability

- Removed the previous blanket rejection of Anthropic/Gemini streaming requests that include tool declarations.
- Anthropic stream translation now emits indexed `tool_use` blocks and incremental `input_json_delta` events, and closes text/tool blocks before the message stop events.
- Gemini stream translation now retains tool-call fragments across upstream SSE chunks, validates completed arguments as JSON objects, and emits completed `functionCall` parts on the finish chunk (or stream completion fallback).
- Gemini function responses are correlated by function name using FIFO call order in request history; arbitrary call-ID correlation is impossible because Gemini `functionResponse` does not expose that identifier.
- Tightened Gemini non-streaming tool-call and declaration validation to require JSON object arguments/schemas, and replaced the previous constant same-name call ID with unique per-request call IDs plus FIFO response matching.
- SQLite driver/runtime integration remains blocked by the environment's unavailable DNS/network and missing cached Go SQLite driver. No persistent database is claimed.
- Only `gofmt` and archive integrity checks are performed in this phase; Go build, automated tests, CI, and live provider integration remain deferred until V1 feature completion.


## Batch: image input interoperability and scheduler circuit breaker

Implemented in source (not build/test verified):
- Anthropic user image blocks (`url` and `base64`) now convert to OpenAI `image_url` content parts. Base64 media types must be `image/*`.
- Gemini user `inlineData` images convert to data-URL image parts; HTTP(S) `fileData` is accepted only with an `image/*` MIME type. Non-HTTP(S) provider file URIs such as `gs://` are rejected rather than assumed accessible to an unrelated OpenAI-compatible upstream.
- Mixed text/image order is retained. Image-only and image-plus-tool-call messages do not have their image content overwritten by a null tool-call content field.
- Gateway capability detection recognizes Anthropic image blocks and Gemini `inlineData`/`fileData`; model routing requires the configured `vision` capability. The upstream must independently support the image format.
- Scheduler weighted round-robin now selects among healthy candidates in a priority group, preventing unhealthy candidates from skewing selection to the first healthy provider.
- Provider failures now trigger exponential cooldown with a half-open health probe after cooldown; successful probes reset the failure count. Cooldown is capped at five minutes.

Known limits: audio/video/document inputs, generated image outputs, multimodal tool results, account-level concurrency controls, and token-budget enforcement remain unimplemented. SQLite remains blocked by the absence of a cached Go SQLite driver and unavailable DNS/network access to fetch one. This batch was formatted with `gofmt`; no build, tests, CI, or live provider requests were run, consistent with the current V1 execution sequence.


## Batch: bounded request observability and upstream transport hardening

Implemented in source (not build/test verified):
- Added request ID response headers (`X-Request-ID`) and bounded in-memory request metadata ring buffer (maximum 1000 records).
- Added authenticated `GET /admin/stats` with request totals, in-flight count, status-class counts, average latency and approximate P95 latency.
- Added authenticated `GET /admin/requests?limit=1..1000` with recent method/path/status/latency metadata. Request bodies, query strings, authorization headers and provider credentials are not retained.
- Request telemetry records panic paths before re-panicking, and the response writer preserves `http.Flusher` for SSE.
- Hardened the default OpenAI-compatible transport with dial, TLS handshake, idle-connection and response-header timeouts; total request timeout remains disabled so long-running SSE streams are not cut off.

Known limits: telemetry is process-local and bounded; it is lost on restart. It does not yet record token usage, provider/model attribution, cost, scheduler decisions or durable request logs. Those require request lifecycle instrumentation and SQLite runtime integration.

SQLite remains blocked in this execution environment: no driver is cached and DNS access to `proxy.golang.org` fails. No persistence claim is made. Per the requested sequencing, no build, tests, CI or live provider calls were run in this batch.

### Scheduler probe coordination and provider URL validation (current batch)

- Provider health checks now use a per-provider probe mutex. After a cooldown expires, concurrent requests wait for one half-open probe instead of concurrently flooding the upstream model-list endpoint.
- Health state is rechecked after acquiring the probe lock, so waiters reuse the first probe result.
- OpenAI-compatible provider construction now rejects non-HTTP(S), relative, user-info-bearing, query-bearing, and fragment-bearing base URLs. Localhost and private-network endpoints remain allowed for self-hosted model servers.
- Empty model IDs and negative context limits are rejected at provider construction.
- This is source-level hardening only; full build, race, unit, and integration validation remains deferred until the V1 feature-complete checkpoint as requested.

### Batch D — non-streaming request failover

The OpenAI-compatible, Anthropic Messages, and Gemini `generateContent` non-streaming paths now share a bounded `generateWithFallback` execution path. A provider that fails a generation attempt is reported to the scheduler, then the gateway asks the scheduler for another eligible provider while preserving the model capability requirements. A provider ID is attempted at most once per incoming request, and a nil response without an error is treated as an upstream failure.

Streaming requests are intentionally not transparently retried after response headers or body bytes have been sent. A future streaming retry design would need an explicit pre-commit boundary and replay-safe upstream semantics. This change does not add durable provider state: provider configuration remains runtime-only until a database driver and repository wiring are implemented.


### Batch E — runtime account metadata management

Implemented in source (not build/test verified):
- Added a concurrency-safe in-memory account store with generated opaque IDs, duplicate-ID rejection, provider association, status validation, timestamps, filtered listing, status changes, and deletion.
- Added authenticated `GET/POST /admin/accounts`, `GET /admin/accounts/{id}`, `PATCH /admin/accounts/{id}/status`, and `DELETE /admin/accounts/{id}` endpoints.
- Account creation requires an existing Provider. Provider deletion is rejected with HTTP 409 while account metadata still references it, preventing dangling runtime associations.
- The API accepts metadata only. It does not accept or expose credentials, cookies, tokens, or browser sessions; account status is not yet wired into Provider scheduling.
- All account mutations are process-local and explicitly return `persisted: false`; restart recovery awaits SQLite driver and runtime repository integration.

No build, automated tests, CI, or live provider calls were run, following the agreed V1 sequencing. `gofmt` and archive integrity checks are not substitutes for those validations.

### Batch E — runtime session lifecycle

Added an in-memory session metadata store and protected admin endpoints for listing, creating, reading, transitioning, and deleting sessions. State transitions are validated; expired/revoked sessions are terminal, and elapsed active-session expiry is surfaced as `expired`. Account deletion is blocked while attached sessions exist. This is metadata lifecycle management only: it does not authenticate or refresh provider sessions, store cookies/tokens, or persist state across restart (`persisted:false`).

### Batch F — encrypted credential vault foundation

Implemented in source (not build/test verified):
- Added an in-memory `credential.Vault` backed by the existing AES-256-GCM encryptor. Supported credential kinds: API key, access token, refresh token, cookie, and username/password.
- Plaintext is encrypted before storage; metadata listing does not expose secret bytes. The only plaintext read path is the explicit internal `Reveal` method.
- AES-GCM associated data binds ciphertext to the immutable `(account_id, credential_id, kind)` tuple. Reusing an ID with a different account or kind is rejected.
- Secret replacement increments a version while retaining the original creation timestamp. Local credential status metadata supports active, disabled, revoked, and expired.
- Key rotation decrypts and re-encrypts all entries under a replacement encryptor and swaps the in-memory map only after all entries succeed.

Known limits:
- The vault is not yet wired into application construction, management APIs, Account/Session lifecycle, or SQLite persistence.
- No credential-encryption key configuration or deployment secret-manager integration exists yet. Vault data is process-local and lost on restart.
- Key rotation is currently an in-memory operation, not a durable multi-key migration protocol. Production use requires key provisioning, durable transaction semantics, backup/recovery, access-control review, and build/test validation.
- No build, tests, CI, or live Provider calls were run, per the agreed V1 sequencing; `gofmt` is formatting only.


### Batch G — runtime credential vault integration

- Added `OMNI_CREDENTIAL_ENCRYPTION_KEY` configuration and application startup initialization. The value must be Base64 encoding of exactly 32 random bytes; invalid configured values fail initialization.
- Added credential metadata/list/create/status/delete management endpoints. Secret values are accepted only on create and never returned by the API. These endpoints are disabled unless `OMNI_GATEWAY_API_KEY` is configured. Account deletion is blocked while sessions or credentials remain.
- Hardened in-memory vault locking around encryption/decryption and zeroed replaced/deleted ciphertext buffers.
- Persistence remains disabled: vault contents are lost at process exit. No SQLite driver or durable key management has been integrated.
- No build, tests, CI, or provider integration tests were run in this batch.


### Batch I — SQLite connection initialization boundary

Added `storage.ConfigureSQLite` to apply and verify connection-level settings
(foreign keys, busy timeout, optional WAL). It currently pins the DB pool to a
single connection so PRAGMA state is deterministic. Removed the ineffective
foreign-key PRAGMA from the transactional schema migration. This is not a
driver integration: no SQLite driver is registered and App does not yet open a
database.

### Transaction composition update

Repository-level multi-row writes now use `storage.InTransaction`. It starts a
transaction for a `*sql.DB`, and reuses an existing `*sql.Tx` when a repository
is invoked inside an outer `SQLStore.WithTx` callback. This avoids unsupported
nested transactions and keeps model capability replacement atomic. The helper
requires a transaction-capable database implementation; the SQLite driver and
runtime wiring remain pending.

## Embedded database migrations

The canonical SQL migration files live under `internal/storage/migrations/` and
are compiled into the binary with `embed`. Production startup can call
`SQLStore.MigrateEmbedded`; callers that need a controlled migration filesystem
can continue using `SQLStore.Migrate`.

## Database runtime boundary

`storage.NewSQLiteRuntime` owns an already-open `database/sql` handle, configures SQLite
connection behavior, applies embedded migrations, and verifies the connection. It closes
the handle if initialization fails and provides idempotent shutdown. Driver registration
and opening the configured database path remain pending; `OMNI_DATABASE_PATH`,
`OMNI_DATABASE_BUSY_TIMEOUT`, and `OMNI_DATABASE_WAL` are parsed configuration only and
do not yet activate persistence in `app.App`.


### Batch M — Pure-Go SQLite driver and App lifecycle connection

Selected `modernc.org/sqlite v1.34.5` for the Go 1.23 baseline and pinned
`modernc.org/libc v1.55.3` to the driver generation's matching ABI dependency;
`mathutil v1.6.0` and `memory v1.8.0` are pinned to the same dependency family.
Added a blank-import driver registration and `storage.OpenSQLiteRuntime`, which
creates the database parent directory, opens the `sqlite` database/sql driver,
configures PRAGMAs, applies embedded migrations, and transfers connection ownership
to Runtime. `config.Default()` now selects `data/omnirouter.db`. `app.New` opens
the database before constructing the HTTP server, so initialization/migration errors
prevent server startup. `App.Run` closes the database on every return path.

Verification limitation: module proxy DNS is unavailable in this environment. The
driver and ABI pins are declared, but module archives/checksums could not be fetched;
no `go.sum` was generated and no build, tests, CI, or runtime DB verification was run.
Android `GOOS=android` is absent from the upstream supported-platform matrix and is
not claimed as supported. Business repositories still need runtime wiring; this batch
activates SQLite schema persistence, not yet all business-record persistence.


### Batch N — SQLite-backed encrypted credentials

- Added migration `002_credential_status.sql` to persist credential lifecycle status while preserving existing rows as active.
- Extended Credential Repository to persist AES-GCM ciphertext, immutable account/kind identity, expiry, status, creation/update timestamps, and key version. Secret replacement increments the stored version; revoked credentials are terminal and cannot be overwritten.
- Added a persistent Vault mode and wired App to construct it when `OMNI_CREDENTIAL_ENCRYPTION_KEY` is configured. Existing gateway credential endpoints now use the repository-backed Vault and report `persisted:true`; list database errors return an explicit storage error rather than silently returning an empty list.
- Credential ciphertext is bound with AES-GCM associated data to `(account_id, credential_id, kind)`. The encryption key is never stored in SQLite. Losing or changing the key makes existing credentials unreadable. Durable key rotation is not yet implemented.
- Account runtime metadata is still in-memory, and Provider creation does not yet bind its API key to a Credential record. Thus this batch makes the standalone credential-management records durable, but does not yet complete automatic Provider restoration.
- No build, tests, CI, or live Provider calls were run, per the agreed V1 sequencing.


### Batch P — SQLite-backed Account and Session lifecycle

- Added Account Repository list, delete, provider-count, and dependent-credential-count operations. Account create/list/get/status/delete management routes now use the SQL Repository when App supplies it; memory Store remains as a compatibility path for standalone Handler constructors.
- Added Session Repository create/list/transition/delete operations. Session status transitions execute transactionally, expiration is persisted, and duplicate session IDs are rejected rather than silently overwritten by an upsert.
- App constructs and injects both repositories. Account and Session metadata now survive restarts when the SQLite database is available. Credential management checks durable Account records rather than the old process-local account map.
- Account deletion is guarded by a conditional SQL delete that refuses to remove accounts with dependent sessions or credentials, including when a credential vault is not configured. Provider deletion checks the durable Account count.
- This batch changes source wiring only. The SQLite module download remains blocked by module-proxy DNS in this environment; no go.sum, build, tests, CI, or live database verification is claimed.

## Batch Q — Model and API key persistence

- Model metadata and capability rows are synchronized into SQLite during configured-provider startup.
- `/admin/models` reads persisted model metadata when the SQL repository is available; model enable/disable and deletion endpoints are available.
- API keys can be created, listed, disabled, and deleted through `/admin/api-keys`. Only a SHA-256 digest is stored; the generated secret is returned once at creation.
- API-key management remains bootstrapped by the configured `OMNI_GATEWAY_API_KEY`. Persisted API keys are accepted by the gateway middleware when that master key is configured, with exact route-scope checks; see Batch R1.
- Runtime build and database integration verification remain deferred to the final validation phase, as requested.


## Batch R1 — scoped API key authentication

- Added the `gateway:read`, `gateway:write`, `admin:read`, and `admin:write` scope vocabulary.
- Persisted API keys are now authenticated by indexed hash lookup and checked against the exact route scope when the master key is configured.
- The configured `OMNI_GATEWAY_API_KEY` remains the master credential.
- API keys with empty, duplicate, or unsupported scopes are rejected.
- Authentication updates `last_used_at` after the lookup; failed telemetry updates do not turn a valid key into an authentication failure.
- The open development mode remains when no master key is configured; it must not be exposed to untrusted networks.

## Batch R2 — Provider-to-Credential binding and restart restoration

- Added migration `003_provider_credential_binding.sql`; Provider rows can reference their owning Account and encrypted Credential without storing API key material in the Provider table.
- Added stable provider-owned Account/Credential identifiers. Configured Provider API keys are encrypted through the persistent AES-GCM Vault and bound to Provider metadata. Supplying a provider API key without persistent credential encryption now fails startup rather than silently retaining a memory-only secret.
- App startup synchronizes configured Provider metadata and model declarations, then reconstructs enabled OpenAI-compatible adapters from persisted Provider, Model/Capability, Account, and Credential records. Disabled Accounts prevent adapter restoration; missing or unavailable credential bindings fail closed.
- Dynamic Provider creation persists API keys through the Vault and persists model metadata/capabilities. Updating Provider metadata without a replacement API key retains and reuses the existing bound credential.
- Provider deletion checks non-owned Account, Session, and Credential dependencies, then removes its provider-owned Credential and Account before deleting metadata. Disabled persisted Providers can be deleted even though they are absent from the live registry.
- The encryption key remains deployment-managed through `OMNI_CREDENTIAL_ENCRYPTION_KEY`; durable key rotation and key backup/restore automation are not implemented. These are source-level changes only. No build, tests, CI, or live SQLite verification were run; validation remains deferred to the V1 checkpoint.


## Current implementation boundary (Batch R2)

Provider, Account, Session, Model/Capability, Credential, and API Key repositories are connected to the App's SQLite runtime in source. Provider API keys are stored only as AES-GCM ciphertext and restored from their provider-owned credential binding. Request telemetry remains process-local; token/cost accounting, full admin UI, additional native provider adapters, media beyond image input, durable encryption-key rotation, and final build/runtime/platform validation remain open.


## Batch R2 — provider policy and runtime binding completion

- Provider metadata, owning Account, encrypted Credential, and model capability records are wired through startup and dynamic provider creation. Startup treats SQLite provider records as the runtime source of truth after importing environment configuration.
- Added `PATCH /admin/providers/{id}/policy` to persist enabled/priority/weight and refresh the scheduler candidate set without replacing credentials.
- Disabled providers remain stored and restorable but are excluded from scheduler selection.
- Provider creation requires persistent encrypted credential storage whenever an upstream API key is supplied.
- This is source-level integration only; no build, tests, CI, or cross-platform runtime verification is claimed.


## Batch R3.1 — durable HTTP request lifecycle

- Connected the existing bounded observability recorder to the SQLite `request_logs` repository through a small sink interface.
- App constructs and injects the repository; request records are appended after the response completes, including panic paths.
- `/admin/requests` reads persisted history when the repository is configured; memory-only constructors retain the in-memory fallback.
- Added `/admin/usage` with durable request/error totals, known token totals, and average latency.
- Privacy boundary: request bodies, authorization headers, and query strings are not persisted. Current middleware records HTTP-level lifecycle only; provider/model attribution and protocol-reported token usage extraction remain incomplete. Unknown usage is not estimated.
- Build, tests, CI, and runtime DB verification remain deferred to the final validation batch.

## R3.2 — provider token usage and stream lifecycle

- Added a shared token usage parser for OpenAI-compatible, Anthropic Messages, and Gemini usage envelopes.
- Added provider/model attribution, lifecycle status, usage source, and finality to persisted request records.
- Added migration `004_request_usage_lifecycle.sql` for the three request-log metadata fields and a model/time index.
- Added provider/model aggregation to `GET /admin/usage`.
- OpenAI-compatible streams now require an explicit `[DONE]` marker to complete; premature EOF is an upstream stream error. Any usage already observed is retained as non-final. Downstream write failures and request-context cancellation are classified as client disconnects.
- Estimates are never substituted for missing provider usage.

Validation note: token-usage parser, recorder, Provider SSE, and Gateway integration tests pass. All packages and tests compile, `go vet ./...`, and executable build pass in an isolated compile-check copy with the SQLite driver import replaced by a local compile-only stub. Targeted race tests pass for TokenUsage, Observability, Provider SSE, and Gateway. These checks validate Go types, package wiring, and the non-database request paths, but do not validate the real SQLite driver. The migration and aggregation SQL were separately smoke-tested with Python's SQLite engine. A real-driver storage integration test now covers migrations 001–005, durable request usage, provider/model aggregation, and reopen behavior; it is pending execution because this environment cannot resolve `proxy.golang.org` and the source archive has no `go.sum`. A GitHub Actions workflow has been added to run module verification, formatting, all tests, race tests, vet, and build when dependencies are reachable. No remote CI run or live upstream Provider call is claimed.

### R3.2 lifecycle correction

Provider-selection failures are recorded with `error_code=provider_unavailable` and
`lifecycle_status=failed` for OpenAI-compatible, Anthropic, and Gemini entrypoints.
This prevents unavailable-provider requests from being counted as successfully
completed in lifecycle analytics.

### R3.2 lifecycle consistency correction

The request recorder now normalizes unclassified HTTP 4xx/5xx responses to
`lifecycle_status=failed`, preserving explicit `interrupted` and `timeout` states.
Recovered handler panics are recorded as failed with `error_code=handler_panic`,
even if the handler had already committed a successful HTTP status. Regression
tests cover HTTP error fallback and panic-after-write behavior. Validation remains
subject to the real SQLite driver and remote CI release gates.
R3.2 usage accounting distinguishes inference traffic from administrative and health traffic. All HTTP requests remain in request history, while `/admin/usage` and provider/model breakdowns include only `POST /v1/chat/completions`, `POST /v1/messages`, and Gemini generate/streamGenerate endpoints. Migration `005_usage_eligibility.sql` marks pre-existing rows as unknown/ineligible rather than guessing their route.


### R3.2 inference-only usage accounting

Request history continues to retain administrative and health traffic, but usage
aggregates now include only model inference endpoints. Migration 005 adds
`usage_eligible`; historical rows default to ineligible because their original route
was not persisted and must not be guessed. Python SQLite smoke validation confirms
migrations 001–005 and inference-only Summary/Breakdown SQL. The real Go SQLite
integration tests remain pending the external module dependency.

### R3.2 follow-up: response-writer and migration concurrency

The Recorder wrapper preserves conditional `http.Flusher` support, implements
`io.ReaderFrom` while retaining response status accounting, and exposes
`Unwrap()` so `http.ResponseController` can discover optional capabilities on
the original server writer. It intentionally does not claim direct
`http.Hijacker`/`http.Pusher` support when the wrapped writer lacks those
interfaces. SQLite migrations increment a guaranteed-existing singleton lock
row inside each transaction before rechecking the migration marker, preventing
two processes from both applying the same numbered migration after a stale
pre-transaction check. A concurrent startup integration test covers two
independent database handles; it still requires the real `modernc.org/sqlite`
driver to execute.
