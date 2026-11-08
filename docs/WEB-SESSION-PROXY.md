# Multi-vendor Web Session Reverse Proxy

Status: scope correction and implementation contract. This is the primary Web-to-API workstream; it is distinct from the official API adapters.

## Product requirement

OmniRouter must expose a local OpenAI-compatible gateway backed by user-authorized sessions from the vendors' consumer web products. The first target set is:

| Web product | Consumer origin | Credential lifecycle observed in reference implementations | Current OmniRouter state |
|---|---|---|---|
| DeepSeek Web | https://chat.deepseek.com | Browser-owned user token and session cookie; exact accepted cookie set and request flow must be fixture-verified | Pairing and encrypted credential storage exist; Provider is inert, zero models, no inference transport |
| Zhipu Qingyan / GLM Web | https://chatglm.cn | Reference implementations use refresh-token based renewal, then short-lived access token; signed request metadata is reported | No provider, OAuth/session model, or transport exists |
| Kimi Web | https://www.kimi.com | Reference implementations use web JWT/access token and Connect-RPC JSON framing; session/device metadata may be required | No provider, OAuth/session model, or transport exists |

These are web-session adapters, not official API adapters. Keep official OpenAI, Anthropic, Gemini, and DeepSeek API providers separate and never mix credentials, health, model discovery, or transport.

## Public gateway contract

- Initial common ingress: GET /v1/models and POST /v1/chat/completions (stream and non-stream).
- Responses API is enabled per adapter only after request/response mapping tests pass.
- Client supplies full messages each request unless a provider-specific lineage mapping is explicitly supported.
- Tool calling, vision, files, search, reasoning, and structured outputs are independent capabilities. Never infer support from a text-only success.
- Provider/model identifiers must be namespaced so official API models and web-session models cannot collide.
- Keep account selection and health isolated per vendor and account.

## Shared runtime architecture

```text
OpenAI-compatible client
  -> Gateway authentication / request validation
  -> Model + vendor-web provider selection
  -> Account scheduler (status, concurrency, min interval, cooldown)
  -> Encrypted credential vault + session lifecycle
  -> Vendor protocol adapter
       DeepSeek Web protocol
       GLM Web protocol
       Kimi Web Connect-RPC protocol
  -> bounded HTTP transport / SSE or Connect stream decoder
  -> normalized provider events
  -> Chat Completions response / SSE encoder
  -> SQLite conversation lineage + usage lifecycle
```

Shared components: account pool, encrypted credential references, session refresh lifecycle, HTTP timeout/cancellation, redaction, retry classification, SSE event normalization, per-conversation serialization, usage accounting, and fixture harness.

Vendor-specific components: credential extraction/refresh, origin and headers, request schema, model catalogue, stream framing, terminal event recognition, conversation/session IDs, and capability mapping.

Do not force all vendors into one generic wire protocol. Reuse lifecycle and transport primitives while keeping protocol adapters isolated and versioned.

## Protocol evidence inventory

### DeepSeek Web

- Existing RFC records browser state at chat.deepseek.com and an observed completion endpoint at /api/v0/chat/completion. Treat this as historical evidence, not a stable contract.
- Public projects report additional session creation and user-token exchange flows. Some implementations solve proof-of-work challenges. OmniRouter must not implement challenge solvers, CAPTCHA/MFA automation, WAF bypass, or anti-abuse evasion.
- Only normal user-authorized browser sessions are accepted. If the site blocks or challenges a request, return a typed policy/authentication error and stop.
- Existing `internal/provider/deepseekweb` contains validation, encrypted persistence, pairing, and a generic SSE frame parser only. `RegistrationProvider.Generate/Stream` still fails closed; there is no live completion transport.

### Zhipu Qingyan / GLM Web

- Reference projects report `chatglm.cn` refresh-token exchange followed by short-lived access-token use, and a streaming assistant endpoint.
- A reference DSH plugin documents `POST /chatglm/user-api/user/refresh` and `POST /chatglm/backend-api/assistant/stream`; treat exact headers, signatures, request fields, and response part semantics as version-sensitive observations.
- Any request signature must be implemented only if confirmed as ordinary protocol authentication from current user-authorized traffic and permitted for the intended use. Do not implement anti-abuse challenge solving or identity rotation.
- No OmniRouter GLM Web implementation exists yet.

### Kimi Web

- Reference projects report the consumer origin `www.kimi.com`, JWT/access-token authentication, and a Connect-RPC JSON chat protocol under `/apiv2`.
- The observed stream is not assumed to be SSE merely because the public gateway uses SSE. Implement the actual upstream framing separately and normalize only after terminal/error semantics are verified.
- Device/session metadata and scenario/model mapping are version-sensitive. Do not hard-code a model catalogue from an old README.
- No OmniRouter Kimi Web implementation exists yet.

## Credential and account security

- All tokens, refresh tokens, and cookies are write-only through authenticated local admin actions and encrypted in the existing AES-GCM vault.
- Never store browser profile directories, passwords, MFA seeds, or raw HAR exports.
- Prefer an explicit browser extension or user-supplied narrowly scoped credential import. Never scrape unrelated tabs or origins.
- Redact Authorization, Cookie, Set-Cookie, refresh/access tokens, user identifiers, conversation IDs, message IDs, device IDs, and pairing nonces from logs, traces, metrics, errors, and test output.
- Refresh-token rotation must be persisted atomically before the old credential is discarded.
- Revocation is terminal. Re-authentication creates a new session/credential generation.
- Per-account rate limits and concurrency caps are mandatory; do not rotate accounts to evade upstream rate limits.

## Required protocol fixtures

Before enabling any adapter for inference, capture fixtures from an account the user owns or is authorized to test. Capture manually in browser DevTools; do not export raw HAR or copy raw cURL commands into the repository.

Each vendor needs sanitized fixtures for:

1. Credential/session validation or refresh success, including expiry/rotation metadata but no secret values.
2. One successful non-streaming request/response if the web client exposes it.
3. One streaming request with all event/frame types and an explicit terminal marker.
4. A multi-turn exchange proving conversation lineage fields and parent-message behavior.
5. Ordinary expired-session failure.
6. Rate-limited or policy-blocked response, recorded only as status/category; never solve or replay a challenge.

Fixture metadata must record vendor, capture date, browser version, locale, observed site build/version if visible, and source account type. Replace all identity and content fields with placeholders; do not preserve original token lengths/prefixes.

Fixtures are tests, not credentials. They must be reviewed for leakage before commit.

## Delivery order

1. Preserve the already integrated DeepSeek pairing/security foundation; do not merge the stale, diverged PR #7 branch wholesale.
2. Implement shared web-session runtime contracts and SQLite conversation-lineage storage with restart and per-conversation serialization tests.
3. Implement DeepSeek Web transport against current sanitized fixtures, without challenge bypass. Enable only verified text/stream capabilities.
4. Implement Kimi Web as a separate Connect-RPC adapter after its fixtures are captured and reviewed.
5. Implement GLM Web refresh-token lifecycle and stream adapter after its fixtures are captured and reviewed.
6. Add per-vendor live acceptance, expiry/re-auth, revoke, multi-turn, cancellation, rate-limit, and account-pool tests.
7. Keep all web-session providers opt-in and excluded from default routing until each provider passes its own acceptance suite.

## Reference projects reviewed

- PruhaNLP/web-to-api (MIT): useful architecture reference for DeepSeek/Kimi web adapters, browser-session import, and stream normalization. Its public README currently lists DeepSeek, Kimi, and Qwen; it does not establish GLM support.
- Lequait/dsh-glm-web (MIT): focused GLM Web reference for refresh-token lifecycle and stream handling.
- xiaoY233/Chat2API: broad multi-vendor reference including DeepSeek, GLM, and Kimi. Repository metadata reports no recognized open-source license; do not copy code. Treat protocol notes as unverified research leads only.
- Maicon501a/kimi-proxy-web: focused Kimi Web Connect-RPC reference. Repository metadata does not declare a license; do not copy code.

Reference code is not an upstream contract. Implement independently in Go, preserve attribution in research notes, and re-verify every protocol against current user-authorized browser traffic.

## Acceptance gates

- The three consumer-web providers appear separately from official API providers in the admin UI and model catalogue.
- Each provider can independently import/refresh/revoke credentials and survives SQLite restart.
- A valid session supports one text completion, one streamed completion with explicit terminal event, and a two-turn conversation.
- Expired sessions fail closed and expose a re-authentication status; policy blocks do not trigger automatic retries or account rotation.
- Downstream cancellation cancels upstream I/O and releases account concurrency.
- Logs, errors, metrics, request history, and backups contain no credentials.
- No adapter is called production-ready based only on mock tests or a protocol copied from a third-party repository.