# P0 system acceptance and security audit

Status: implementation and deterministic acceptance coverage added on the work/p0-system-acceptance branch. The live official-provider matrix is prepared but remains unexecuted until repository Actions secrets are configured and the manual workflow is available on the repository default branch.

## Scope and evidence boundaries

This batch covers four gates:
1. Management API authentication, credential handling, redaction, and input validation.
2. Multi-provider routing, non-stream failover, stream cancellation, and incomplete terminal handling.
3. SQLite process restart, encrypted credential recovery, and backup/restore round-trip.
4. Official OpenAI, Anthropic, Gemini, and DeepSeek upstream acceptance.

A local unit test, a mock server, a successful build, or a green general CI run must never be described as a successful live upstream test.

## 1. Management API security

### Implemented in this batch

- Unauthenticated/open mode is permitted only when the HTTP listener is bound to loopback (127.0.0.0/8, ::1, or localhost). Wildcard, LAN, public IP, and hostname binds require OMNI_GATEWAY_API_KEY.
- With a master key configured, protected routes require the master key or a persisted API key with the exact gateway/admin read/write scope.
- Admin JSON mutations use a bounded decoder, reject unknown fields, and reject trailing JSON values.
- Provider creation trims and validates provider/model identifiers, rejects duplicate/empty model IDs, rejects negative priority, and caps weight at 1,000,000.
- Upstream HTTP error bodies are not copied into public gateway error responses; clients receive stable generic errors rather than upstream response text that could contain credentials or sensitive diagnostics.
- Credential write buffers are cleared after vault persistence; list and metadata APIs never return credential plaintext.

### Security tests added

- Loopback and wildcard listener classification.
- Non-loopback startup rejected without a gateway key; allowed with a key.
- Master-key authentication for management routes.
- Scoped API key route isolation (admin read cannot access gateway read or admin write).
- Secret-like upstream error text is not reflected to clients.
- Unknown JSON fields, multiple JSON values, and oversized bodies are rejected.

### Operational constraint

Loopback binding is not a substitute for a gateway key when exposing the service through a reverse proxy, tunnel, container port publish, or other forwarding layer. Set a strong OMNI_GATEWAY_API_KEY before exposing any management or inference route beyond the local machine.

## 2. Routing, failover, and streaming

Existing adaptive scheduler behavior remains the basis of route selection: enabled/healthy/circuit/capability/model filtering precedes weighted, priority-aware adaptive ranking. Capability declarations are eligibility metadata, not proof that a provider supports every edge case.

### Added/retained acceptance coverage

- Non-streaming request retries a distinct eligible provider after an upstream failure; a provider is not retried twice in one attempt sequence.
- Existing native Gemini fallback coverage verifies capability-compatible failover.
- Stream request context cancellation is propagated to the upstream Provider.
- Existing stream tests verify explicit terminal markers, incomplete EOF, partial usage retention, and downstream write failure classification.
- Once downstream streaming has begun, the gateway does not silently switch providers. A missing/invalid terminal event remains an incomplete request.

Live outage failover, long-duration stream behavior, and cancellation under real network load remain separate deployment acceptance cases.

## 3. SQLite restart and backup/restore

### Deterministic tests

- Existing gateway SQLite integration test closes and reopens the database and verifies request lifecycle and token usage persistence.
- This batch adds a persistent credential-vault integration test: encrypt a provider secret, close SQLite, reopen it with the same deployment key, and reveal the exact secret. The same test verifies metadata listing never serializes plaintext and a different encryption key cannot decrypt it.
- The CI workflow already executes scripts/backup-sqlite.sh and scripts/restore-sqlite.sh against a temporary database, verifies restored content, and confirms the pre-restore snapshot exists.

### Recovery rules

- Back up the SQLite database and separately preserve the exact OMNI_CREDENTIAL_ENCRYPTION_KEY.
- Restore only after stopping OmniRouter.
- A database backup without the matching encryption key is not a complete credential recovery set.
- A checksum and SQLite integrity check establish file integrity, not application-level correctness. After restore, verify readiness, provider recovery, and a controlled model request.

## 4. Official Provider live acceptance

The native provider workflow and script cover:

- OpenAI Responses: non-streaming completion and response.completed SSE.
- Anthropic Messages: non-streaming message envelope and message_stop SSE.
- Gemini generateContent: non-streaming candidates and streaming SSE data.
- DeepSeek official API: existing dedicated smoke suite, including Chat Completions, Responses, Anthropic Messages, tools, and explicit stream terminal checks.

See LIVE-PROVIDER-MATRIX.md for secret names, execution instructions, model selection, and exact proof boundaries.

### External dependency / not yet certified

Actual official API calls require valid provider credentials, billing/quota, network access, and a supported model ID. Required repository Actions secrets:

- OPENAI_API_KEY
- ANTHROPIC_API_KEY
- GEMINI_API_KEY
- DEEPSEEK_API_KEY
- OMNI_CREDENTIAL_ENCRYPTION_KEY
- OMNI_GATEWAY_API_KEY

The GitHub connector used for repository changes cannot read or set repository secrets and cannot dispatch Actions workflows. GitHub manual dispatch requires the workflow file to exist on the repository default branch. Therefore this branch can prepare the executable acceptance workflow but cannot truthfully claim live official-provider success before the user configures secrets and runs each provider acceptance job.

## 5. CI and merge gates

Required before merging this branch to dev:

- go test ./...
- go test -race ./...
- go vet ./...
- gofmt clean
- Shell syntax checks for provider, backup, and restore scripts
- SQLite backup/restore round-trip test
- Native provider workflow YAML syntax check

Required before V1 release:

- Each official provider live workflow passes independently, with run URL, provider kind, model ID, adapter commit, and UTC run timestamp recorded.
- Real multi-provider failover and network cancellation acceptance passes.
- Restore drill passes with the exact deployment encryption key.
- Android/Termux device acceptance and security review are complete.
- No release gate is inferred from a mock test or from the generic CI result.

## 6. Current change boundary

This batch targets dev through a reviewable work branch. It does not merge to main, create a release tag, alter repository secrets, or make a live API call.