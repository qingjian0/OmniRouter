# DeepSeek Web Reverse Proxy

Status: DeepSeek-only implementation plan and acceptance contract. No GLM or Kimi work is included in this delivery.

## Product boundary

OmniRouter must expose the existing local OpenAI-compatible Gateway backed by a user-authorized session from the DeepSeek consumer website (`https://chat.deepseek.com`). This is a separate provider kind (`deepseek-web`) from the official DeepSeek API provider (`deepseek`).

The first supported capability target is text-only Chat Completions:
- `GET /v1/models`
- `POST /v1/chat/completions`, non-streaming and streaming
- durable multi-turn conversation continuity
- per-account authorization, re-check, and revoke

Responses API, tool-call emulation, search, image/file input, and reasoning-channel parity remain explicitly out of scope until Chat Completions passes its own acceptance suite.

## Current branch implementation status

- Provider registration accepts an explicit model allowlist; an unpaired account remains an inert registration shell.
- The existing authenticated pairing flow stores the user-authorized `userToken` and `ds_session_id` cookie in the encrypted credential vault.
- A concrete origin-pinned HTTP transport now implements `/api/v0/users/current`, `/api/v0/chat_session/create`, and `/api/v0/chat/completion`, including bounded JSON reads, SSE parsing, typed HTTP failures, and context cancellation.
- The application restores a paired account into an `ExecutorProvider` at startup. A successful browser pairing also attempts to activate that provider in the live registry and scheduler without restarting the process.
- The executor is wired to the existing prompt mapper, SSE patch decoder, OpenAI stream encoder, credential service, and SQLite conversation coordinator.
- The current transport follows OmniRoute's MIT persistent-session + rolling-prompt strategy: reuse the upstream session ID, send the bounded transcript in `prompt`, and keep `parent_message_id: null`. It does not combine this with explicit parent-message continuation.
- Local HTTP simulation tests cover authorization exchange, session creation, streaming completion, post-FINISHED metadata draining, and persistent-session reuse. These tests validate OmniRouter's transport behavior against fixtures, not DeepSeek's live endpoint.
- **Live compatibility is not yet certified.** The upstream may require current client attestation or anti-abuse challenge material not supplied by the ordinary paired token/cookie. The transport does not synthesize or solve those challenges and returns a typed fail-closed error if blocked.
- Generic upstream API providers and official DeepSeek API remain separate and must be unaffected by web-session failures.

## Reference-first implementation policy

This workstream is reference-driven. Before implementing a protocol or adapter feature, inspect mature projects and existing libraries, identify the closest implementation, check its license and maintenance status, and reuse compatible code or port only the minimum required behavior. New code should be limited to OmniRouter-specific integration, security boundaries, and compatibility glue. Every adopted behavior must have a source link and regression test.

Reference projects are not automatically trusted: compare behavior across implementations, reject stale or contradictory assumptions, and do not import unsafe credential handling, browser automation, challenge bypass, or unrelated application code.

### Audited references

| Project | License / stack | Reuse value | Restrictions / decision |
| --- | --- | --- | --- |
| [JuanCMPDev/deep-proxy](https://github.com/JuanCMPDev/deep-proxy) | MIT / Go | Closest language match; OpenAI Chat handler, upstream request types, stream translation, usage accounting, and Go test layout | Reuse protocol concepts and compatible isolated code only. Exclude PoW solver, challenge acquisition/retry, Chrome credential interception, and embedded DeepSeek-owned WASM. |
| [kittors/deepseek-web-api](https://github.com/kittors/deepseek-web-api) | MIT / TypeScript | Strongest architecture reference for SSE event parsing, `ready/title/close`, `THINK/RESPONSE` channel separation, patch append semantics, session lineage, and parent-message persistence | Port behavior to Go behind OmniRouter's existing transport/vault/SQLite abstractions. Exclude PoW/browser worker and login automation. |
| [xiaoY233/Chat2API](https://github.com/xiaoY233/Chat2API) | GPL-3.0 / TypeScript + Koa/Electron | Broad, actively maintained provider reference; DeepSeek request adapter, model options, `ANSWER/RESPONSE/THINK`, search metadata, non-stream aggregation, and OpenAI-compatible stream conversion | GPL-3.0 code is not copied into this project unless license compatibility is explicitly established. Use as behavioral research and compare semantics. Exclude its challenge solver and any credential practices that log secrets or synthesize browser cookies. |
| [sums001/Deepseek-API](https://github.com/sums001/Deepseek-API) | License must be re-checked | Additional protocol research lead | No code reuse until license and provenance are verified. Challenge-solving behavior excluded. |

### Additional current implementation audit

A further implementation was inspected after the initial source audit:
- [cheaper-inference/OmniRoute](https://github.com/cheaper-inference/OmniRoute) — MIT / TypeScript. Audited `open-sse/executors/deepseek-web.ts` at `release/v3.8.54` and verified its MIT license. It provides a more recent, concrete implementation reference for session creation response parsing, model-option selection, request payload shape, SSE-to-OpenAI conversion, and session reuse.
- Its implementation currently creates sessions through `/api/v0/chat_session/create`, extracts `data.biz_data.chat_session.id` (with a `biz_data` fallback), and posts completion payloads containing `chat_session_id`, `parent_message_id`, `model_type`, `prompt`, `ref_file_ids`, `thinking_enabled`, `search_enabled`, and `preempt`.
- Important divergence: this implementation uses `parent_message_id: null` and a persistent-session/rolling-prompt strategy in the inspected path, while other references describe explicit parent-message continuation. These behaviors are not interchangeable; current sanitized two-turn fixtures must decide which lineage strategy the upstream accepts.
- **No code is copied from this project in this slice.** Its inspected executor also contains generated synthetic cookies, browser-fingerprint impersonation, and a PoW challenge/solver path. Those mechanisms are explicitly excluded from OmniRouter. The MIT license does not override the project's security/policy boundary.
- This reference is research evidence only. Its comments mention a captured client build, but that does not prove the same headers, auth behavior, session semantics, or completion schema are valid for the user's current authorized session.

### Cross-reference findings

- SSE framing must be parsed independently of network chunk boundaries and preserve event names; `ready`, `title`, and `close` are distinct event types in the kittors reference.
- Patch decoding must track the active fragment channel. Content-only append operations inherit the active fragment type; THINK text must never be appended to normal assistant content.
- Public implementations differ on fragment names: `RESPONSE` and `ANSWER` both appear. Treat them as answer-channel aliases only when covered by tests.
- Batch updates may carry `accumulated_token_usage` and terminal status. Search fragments/results are preserved on a separate metadata channel; they must not be blindly concatenated into assistant text.
- Two incompatible continuity strategies appear in public references: explicit parent-message continuation, or persistent upstream session + rolling prompt with `parent_message_id: null`. The current branch selects the latter based on the inspected MIT OmniRoute executor; it reuses the saved chat-session ID and sends the bounded message transcript. This is a source-derived choice, not a live compatibility certification, and must be revisited if current sanitized fixtures contradict it.
- `update_session` is session metadata, not a completion terminal. The decoder must not commit lineage or signal completion from this event alone.
- Some references treat an explicit SSE `close` event as terminal. Other dated references report `response/status=FINISHED` or batch quasi-status. EOF or a generic `[DONE]` sentinel alone is not accepted as proof of successful completion until confirmed by current sanitized fixtures.
- Chat2API is GPL-3.0. Its code is not copied into OmniRouter by default; its current adapter and stream handler are behavioral references only.
- Reference projects contain implementation-specific tradeoffs and potential unsafe patterns. In particular, do not copy fake/synthetic cookie generation, raw credential logging, weak random identifiers, or unbounded session assumptions.

## Observed protocol evidence (not a stable upstream contract)

Public reference implementations currently document these observed details:
- Origin: `https://chat.deepseek.com`
- Completion endpoint: `POST /api/v0/chat/completion`
- Session creation endpoint reported by references: `POST /api/v0/chat_session/create`
- Request JSON commonly includes `chat_session_id`, `parent_message_id`, `model_type`, `prompt`, `ref_file_ids`, `thinking_enabled`, `search_enabled`, `action`, and `preempt`.
- Stream frames may use SSE events such as `ready`, `title`, and `close`; patch payloads may carry response fragments including `THINK`, `RESPONSE`, or `ANSWER`, plus request/response message IDs and token usage.

These observations came from third-party source and can become stale. They are research leads only, not sufficient to claim current live compatibility. Exact session creation, auth acceptance, payload fields, model mapping, stream terminal semantics, and account policy responses must be verified against sanitized fixtures from a current user-authorized browser session.

## Challenge and policy boundary

Some reference implementations include proof-of-work (PoW) challenge generation/solving. OmniRouter will not implement or embed PoW solvers, CAPTCHA/MFA automation, WAF bypass, challenge replay, identity rotation, or rate-limit evasion.

The adapter must not fabricate a proof header or retry through a challenge. If the current DeepSeek web endpoint requires a challenge response that cannot be obtained through an ordinary, user-initiated browser flow, the adapter must fail closed with a typed `policy_blocked` / `reauthorization_required` outcome. No challenge-solving workaround is part of this project.

## Runtime architecture

```text
OpenAI-compatible client
  -> Gateway API-key authentication + request validation
  -> deepseek-web provider selection (separate from official DeepSeek API)
  -> account/session authorization check
  -> per-account and per-conversation serialization
  -> encrypted credential vault (userToken + cookie)
  -> DeepSeek Web origin-pinned HTTP transport
  -> DeepSeek SSE parser and versioned event decoder
  -> Chat Completions response / SSE encoder
  -> SQLite conversation lineage
```

Shared infrastructure in `internal/websession` is transport/storage machinery only; no second vendor adapter is being developed.

## DeepSeek account and credential lifecycle

- Tokens and cookies are write-only through authenticated local admin actions and encrypted in the existing AES-GCM vault.
- Never store browser profile directories, passwords, MFA seeds, or raw HAR exports.
- Keep credentials scoped to `chat.deepseek.com`; redirects are not followed and request paths are restricted to explicit DeepSeek API prefixes.
- Re-authentication creates a new credential generation; revocation is terminal.
- Refresh or rotation must be persisted atomically before an old credential is discarded.
- Redact Authorization, Cookie, Set-Cookie, access tokens, user identifiers, conversation IDs, message IDs, and pairing nonces from logs, traces, metrics, errors, and tests.
- Never rotate accounts to evade upstream rate limits.

## Required sanitized fixtures

Before enabling inference, capture current protocol evidence from an account the user owns or is authorized to test. Use browser DevTools during an ordinary manual chat; do not automate login, challenges, or PoW.

Required fixtures:
1. One ordinary successful text completion request body and response status.
2. Sanitized request headers showing header names only; redact every credential and proof value.
3. A short streaming transcript containing each event name and sanitized JSON structure, with all user text, IDs, tokens, cookies, and proof values replaced by placeholders.
4. Session creation/continuation fields and a two-turn example proving the actual parent-message relationship.
5. Normal expired-session response.
6. Rate-limit and policy-block response categories (status and safe error class only).

Record browser version, capture date, visible DeepSeek web build/version if available, and locale. Never commit raw HAR, cURL exports, auth.json, token/cookie/proof values, or user conversation content.

## Implementation slices

1. Complete DeepSeek-only shared runtime foundation: durable lineage, conversation locks, origin-pinned HTTP transport, cancellation and body-size tests.
2. Compare reference implementations for session creation, request mapping, SSE parsing, terminal events, and OpenAI conversion; reuse compatible MIT code or port behavior with attribution.
3. Verify current session creation and completion schema from sanitized fixtures.
4. Implement DeepSeek-specific request mapping and session lifecycle without PoW/challenge solving.
5. Implement DeepSeek SSE event decoding with explicit terminal marker recognition; unknown event/schema must fail closed.
6. Replace `RegistrationProvider` only when the transport is verified; advertise only text capabilities actually proven by fixtures.
7. Wire the provider to encrypted authorization state and durable SQLite lineage.
8. Add authenticated admin UI for session status, re-check, re-authentication, and revoke without ever displaying secrets.
9. Run mock-server acceptance, SQLite restart, cancellation, malformed/unknown event, expiry, revoke, and a user-operated live smoke test.

## Acceptance gates

- The DeepSeek Web provider is independently selectable and never aliases the official DeepSeek API provider.
- A paired account can be restored after SQLite restart without exposing credentials.
- One non-streaming text completion succeeds with a current user-authorized session.
- One streaming completion reaches a verified terminal event; EOF without terminal marker is an error.
- Two sequential turns reuse the same upstream chat-session ID and rolling prompt. The current implementation keeps `parent_message_id: null`; this strategy remains subject to live compatibility verification.
- Concurrent turns for one conversation are serialized; separate conversations can proceed independently.
- Downstream cancellation cancels upstream I/O and releases locks.
- Expired sessions fail closed and request re-authentication.
- Policy blocks and rate limits are not retried or bypassed.
- Logs, errors, metrics, request history, and backups contain no credentials.
- The provider remains unadvertised/unroutable until pairing, persistent credential storage, and an explicit model allowlist are present. Live upstream compatibility remains a separate acceptance gate; a locally passing simulation must never be reported as live acceptance.

## Authoritative implementation status (2026-11-08)

The historical implementation-slice notes below describe earlier checkpoints and are superseded where they conflict with the current branch status above and here.

- Concrete HTTP transport, request mapper, SSE patch decoder, OpenAI stream encoder, executor, credential vault integration, SQLite lineage coordinator, pairing-time activation, and startup restoration are implemented and wired on `feature/web-session-proxy`.
- Local tests exercise the ordinary authorized request sequence (`users/current` → session creation when needed → completion SSE), session reuse, rolling prompt, terminal handling, cancellation, and policy-error handling.
- HTTP 401 maps to expired authorization; HTTP 403 maps to a typed policy-blocked error; HTTP 429 maps to rate limiting. An SSE `hint` error maps to a typed policy/rate-limit error. The current adapter does not call a challenge endpoint or generate proof material.
- Challenge-specific online compatibility has not been verified. If the current upstream requires an attestation/PoW response outside the ordinary user-initiated authorization flow, the request remains blocked and is surfaced as an upstream policy error. No fabricated challenge values, copied proof values, synthetic browser identity, or solver are part of this implementation.
- The current supported scope is text Chat Completions only. Search metadata is drained rather than exposed; active tools, image input, and file upload are not implemented.
- **Not yet proven:** live DeepSeek acceptance, exact current request schema, the selected rolling-prompt session strategy, and successful two-turn continuity against the live service.

## Current code status

The isolated Go patch decoder now follows cross-checked public reference behavior for:
- SSE `ready`, `title`, and `close` event decoding.
- Initial fragments and append patches.
- `THINK` separated from answer text.
- `RESPONSE` and `ANSWER` answer aliases.
- Search fragment/result metadata preserved separately from answer and reasoning channels.
- Batch token usage and explicit terminal status.
- Unknown event/patch fail-closed behavior.

This decoder is now wired into the concrete HTTP transport and `ExecutorProvider`. Runtime activation still requires a paired account, persistent encrypted credentials, and at least one enabled model. Local fixtures validate the adapter's behavior, not current DeepSeek live compatibility.


### OpenAI request projection and bounded conversation history

A protocol-neutral mapper now exists in `internal/provider/deepseekweb/request.go`. It is based on the concrete MIT implementations in [deep-proxy translate_request.go](https://github.com/JuanCMPDev/deep-proxy/blob/main/internal/proxy/translate_request.go) and [OmniRoute deepseek-web.ts](https://github.com/cheaper-inference/OmniRoute/blob/release/v3.8.54/open-sse/executors/deepseek-web.ts):
- Maps system/developer, user, and assistant text to a single role-tagged prompt string.
- Preserves prior assistant tool-call blocks inside their original assistant turn and tool-result messages as transcript context, including the matching function name where a prior `tool_call_id` is available; historical JSON-string arguments are decoded and validated before prompt serialization.
- Keeps a bounded rolling window of the latest 20 non-system conversation entries; system/developer instructions remain at the front.
- Maps model-name hints and explicit thinking/search request aliases into neutral option fields.
- Rejects a current `tools[]` definition because translating model output back into OpenAI `tool_calls` is not implemented yet. It also rejects unsupported content blocks and malformed historical tool-call payloads.
- Preserves message whitespace, including trailing newlines.

The mapper is called by `ExecutorProvider` for incoming Chat Completions requests. It does not implement active tool execution, and its rolling-prompt convention remains unverified against the live upstream. The runtime advertises only explicitly enabled text Chat Completions models after account pairing.

The OmniRoute reference's persistent-session design also demonstrates that session reuse and rolling prompt history are separate controls: its implementation keeps an upstream session cache and separately limits the prompt transcript. OmniRouter will keep its durable per-provider-account lineage model and conversation lock, but will not key sessions by raw tokens or copy the reference's generated-cookie/browser-fingerprint/challenge logic. A verified two-turn fixture must determine whether upstream parent IDs are required and how stale-session responses are represented.


Request mapper regression coverage now includes:
- canonical and alias model option mapping;
- explicit request-option overrides;
- historical assistant tool-call + matching tool-result reconstruction;
- bounded 20-entry transcript behavior;
- malformed JSON, trailing JSON, unsupported roles/content, malformed tool arguments, and rejection of active `tools[]`;
- preservation of leading/trailing message whitespace.

The tests exercise only the local normalization layer. They do not assert upstream acceptance or tool-call execution.

### Source-level audit notes

The audit is based on concrete implementation files, not README claims alone:
- deep-proxy: [deepseek_chunk.go](https://github.com/JuanCMPDev/deep-proxy/blob/main/internal/proxy/deepseek_chunk.go), [translate_stream.go](https://github.com/JuanCMPDev/deep-proxy/blob/main/internal/proxy/translate_stream.go), [translate_request.go](https://github.com/JuanCMPDev/deep-proxy/blob/main/internal/proxy/translate_request.go), and [translate_stream_test.go](https://github.com/JuanCMPDev/deep-proxy/blob/main/internal/proxy/translate_stream_test.go).
- deepseek-web-api: [updates.ts](https://github.com/kittors/deepseek-web-api/blob/main/src/deepseek/updates.ts), [sse.ts](https://github.com/kittors/deepseek-web-api/blob/main/src/deepseek/sse.ts), [completion.ts](https://github.com/kittors/deepseek-web-api/blob/main/src/deepseek/completion.ts), and [sessionStore.ts](https://github.com/kittors/deepseek-web-api/blob/main/src/deepseek/sessionStore.ts).
- Chat2API: [deepseek.ts](https://github.com/xiaoY233/Chat2API/blob/main/src/main/proxy/adapters/deepseek.ts), [deepseek-stream.ts](https://github.com/xiaoY233/Chat2API/blob/main/src/main/proxy/adapters/deepseek-stream.ts), and [DeepSeek provider guide](https://github.com/xiaoY233/Chat2API/blob/main/docs/providers/deepseek.md).

The Go deep-proxy parser is useful as a compact baseline, but its current implementation only consumes the first fragment in some initial/append cases and treats malformed or unknown operations as empty updates. OmniRouter's provisional decoder intentionally processes all recognized fragments, keeps reasoning separate, and fails closed on unknown structural operations. This is a deliberate correctness/security improvement rather than a literal copy.

The kittors update decoder explicitly tracks the current fragment channel for content-only APPEND patches, defaults the initial channel to reasoning, and recognizes event-level `ready`, `title`, and `close` while continuing to decode patch payloads carried by other named SSE events. OmniRouter now mirrors those channel semantics, but exposes SEARCH/SEARCH_REF on a distinct `SearchDelta` field rather than mixing search metadata into reasoning text. Its session store also documents explicit-ID, previous-response-ID, and normalized-history matching. These are the primary behavioral references for future lineage integration.

Chat2API's DeepSeek adapter currently contains browser/protocol-specific assumptions that must not be imported blindly. In particular, any generated cookie, secret-bearing diagnostic output, or challenge-solving dependency is outside OmniRouter's trust boundary. Its GPL-3.0 license also means its source code is not directly copied into this repository by default.

### OpenAI streaming response projection

A per-completion encoder now exists in `internal/provider/deepseekweb/openai_stream.go`:
- Converts normalized answer deltas to OpenAI Chat Completions SSE data fields (`data: {...}`).
- Keeps reasoning in the separate `reasoning_content` delta instead of leaking it into `content`.
- Emits a final `finish_reason: "stop"` chunk only when the normalized patch carries an explicit terminal signal.
- Does not emit the downstream `[DONE]` marker itself. The existing Gateway owns that marker and must receive `provider.Event{Done: true, ExplicitDone: true}` only after a verified upstream terminal event.
- Rejects search metadata until a documented OpenAI-compatible metadata mapping exists; it does not silently append citations or search results to assistant text.
- Rejects writes after completion and uses a distinct completion ID per encoder instance.

The encoder is connected to `ExecutorProvider`, which consumes normalized transport updates and preserves the Gateway's explicit terminal-event contract. Completion-token usage is not currently projected into downstream chunks. Local serialization tests do not prove live upstream compatibility.

### Local conversation identity contract

The Chat Completions gateway now accepts an optional `X-OmniRouter-Conversation-ID` header and carries the validated value through `provider.Request.ConversationID` for both streaming and non-streaming requests, including provider fallback attempts.

- The value is an opaque **local** conversation key, 1–128 ASCII characters from `A-Z a-z 0-9 . _ : -`.
- It is not a DeepSeek `chat_session_id`, parent message ID, cookie, or credential.
- Callers that want cross-request DeepSeek Web continuity must send the same header value on every turn. If omitted, the executor creates a cryptographically random one-turn lease key, does not persist lineage, and never guesses identity from message text or API key.
- The adapter scopes lineage by provider and authorized account in addition to this local key, and serializes turns with `ConversationLocks`.
- The header is opt-in and does not change the behavior of other providers. The runtime provider is activated after successful pairing and restored on startup when persistent credentials are available.

### Conversation coordinator boundary

`internal/websession/coordinator.go` now provides a lease-based coordinator over the existing SQLite lineage repository and per-conversation locks:
- `Begin` acquires the (provider, account, local conversation) lock before reading lineage. Missing lineage is represented as a new conversation; storage errors release the lock and fail.
- `Lineage` exposes the snapshot loaded under the lock.
- `Commit` persists upstream conversation and parent-message IDs and advances the generation. Empty upstream conversation IDs fail through repository validation.
- `Close` releases the lock idempotently. The caller must hold the lease for the entire upstream turn and close it on every path.
- Tests cover first-turn creation, second-turn restoration/advance, incomplete identity rejection, idempotent close, and context cancellation.

This is now a reusable orchestration primitive, but it is not yet instantiated by the DeepSeek provider. The provider must call `Begin` only after selecting a concrete authorized account, use the returned lineage to construct a fixture-verified request, and call `Commit` only after parsing a verified successful response. The coordinator deliberately does not define private DeepSeek wire fields.


## Expanded reference audit (2026-11-08)

A broader source-level comparison covering NIyueE/ds-free-api, deep-proxy, kittors/deepseek-web-api, nv-hoai/deepseek-rp, and additional projects is documented in [DEEPSEEK-WEB-REFERENCE-AUDIT.md](./DEEPSEEK-WEB-REFERENCE-AUDIT.md). It records the newer observed completion field set (including source), SSE event semantics, incremental-prompt versus rolling-history differences, model availability caveats, account concurrency considerations, and license/security exclusions. The audit is research evidence only; it does not enable live inference or replace current sanitized fixtures.

### Provider execution orchestration (mockable; transport still pending)

`internal/provider/deepseekweb/executor.go` now provides an injectable `ExecutorProvider` that composes the already-tested local layers without inventing the private upstream HTTP protocol:
- Validates and projects the incoming OpenAI Chat Completions request through `MapChatCompletionInput`.
- Requires an explicitly enabled model and a stable local `ConversationID`.
- Loads the user-authorized token/cookie material through the encrypted authorization service boundary and zeroes the returned byte buffers after the turn.
- Acquires a per-conversation lease and passes its durable lineage snapshot to the transport adapter.
- Accepts normalized `PatchUpdate` values from an `UpstreamCompletionTransport`; answer and reasoning deltas are projected to OpenAI SSE separately.
- Buffers the terminal chunk until the transport reports verified completion and supplies both upstream conversation and response-message IDs.
- Commits lineage only after that terminal contract succeeds; transport errors, cancellation, missing terminal evidence, or missing IDs do not commit.
- Non-streaming generation aggregates answer/reasoning deltas into a Chat Completions response.

The transport interface is intentionally injectable so mock HTTP/protocol fixtures can exercise the orchestration deterministically. **There is not yet a concrete DeepSeek Web HTTP transport, and `ExecutorProvider` is not yet wired into the runtime provider factory/registry.** The existing `RegistrationProvider` remains inert. The `HealthCheck` method currently validates that local authorization material can be restored; it does not make a network request or prove that DeepSeek accepts the session.

The new executor tests cover successful lineage commit, missing terminal evidence, upstream failure, missing local conversation identity, and non-streaming aggregation. These are mock orchestration tests, not live protocol tests. Current sanitized request/response fixtures are still required before a concrete HTTP transport can be implemented or models can be advertised in production.


## Engineering progress (2026-11-08)

The feature branch now contains the HTTP completion transport and runtime provider wiring. The provider is activated after browser pairing when persistent credential encryption and at least one enabled model are configured. On restart, the active account is restored and re-registered. The first implementation deliberately supports text-only Chat Completions; search metadata is drained but not mapped into OpenAI output, and tools, images, and file upload remain unsupported.

The acceptance gate remains a successful live request against a normally authorized DeepSeek Web session, including a streamed answer and a second turn reusing the same upstream chat session. A passing local simulation or CI run is not evidence that DeepSeek currently accepts the request schema or session.

---

## Current integrated status — 2026-11-09

PR #21 has been merged into the dev branch. Merge commit:
6bed98272b565e0079373917ac801bde40bd2469.

The post-merge CI run [42511399078](https://github.com/qingjian0/OmniRouter/actions/runs/42511399078) completed successfully with all 7 jobs passing, including Go validation, Chromium extension validation, and Linux / Windows / macOS cross-builds.

The implementation is integrated, but the live acceptance gate remains open: normal user-initiated Chromium pairing, one real streamed completion, a second turn proving continuity, restart restoration, and authorization revocation. The live-provider smoke workflow run after merge did not provide job records, so it is not evidence of either success or a specific adapter failure.

For device-specific setup, see [docs/INSTALL.md](INSTALL.md). Supported pairing requires desktop Chromium on the same computer as the loopback OmniRouter gateway. Android/Termux is an experimental gateway runtime path; Android browser extension pairing is not supported.