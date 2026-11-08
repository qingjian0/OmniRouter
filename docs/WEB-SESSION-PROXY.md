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

## Existing state on dev

- Provider registration, admin pairing tickets, Chromium extension pairing, encrypted token/cookie persistence, restart restore, and account-wide revoke are implemented.
- `RegistrationProvider` is deliberately inert: it advertises zero models and rejects Generate/Stream. It is not a working web reverse proxy.
- The pairing boundary currently accepts a user-authorized `userToken` and the `ds_session_id` cookie.
- Generic upstream API providers and official DeepSeek API are separate and must remain unaffected by web-session failures.

## Observed protocol evidence (not a stable upstream contract)

A public reference implementation currently documents these observed details:
- Origin: `https://chat.deepseek.com`
- Completion endpoint: `POST /api/v0/chat/completion`
- Request headers include Bearer authorization, Cookie, JSON content type, SSE accept, and DeepSeek web client metadata.
- Request JSON includes `chat_session_id`, `parent_message_id`, `model_type`, `prompt`, `ref_file_ids`, `thinking_enabled`, `search_enabled`, `action`, and `preempt`.
- Stream frames use SSE events such as `ready`, `title`, and `close`; patch payloads carry response fragments including `THINK` and `RESPONSE`, plus request/response message IDs and token usage.

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

1. Finish DeepSeek-only shared runtime foundation: durable lineage, conversation locks, origin-pinned HTTP transport, cancellation and body-size tests.
2. Verify current session creation and completion schema from sanitized fixtures.
3. Implement DeepSeek-specific request mapping and session lifecycle without PoW/challenge solving.
4. Implement DeepSeek SSE event decoding with explicit terminal marker recognition; unknown event/schema must fail closed.
5. Replace `RegistrationProvider` only when the transport is verified; advertise only text capabilities actually proven by fixtures.
6. Wire the provider to encrypted authorization state and durable SQLite lineage.
7. Add authenticated admin UI for session status, re-check, re-authentication, and revoke without ever displaying secrets.
8. Run mock-server acceptance, SQLite restart, cancellation, malformed/unknown event, expiry, revoke, and a user-operated live smoke test.

## Acceptance gates

- The DeepSeek Web provider is independently selectable and never aliases the official DeepSeek API provider.
- A paired account can be restored after SQLite restart without exposing credentials.
- One non-streaming text completion succeeds with a current user-authorized session.
- One streaming completion reaches a verified terminal event; EOF without terminal marker is an error.
- Two sequential turns continue the same upstream conversation using the verified parent message ID.
- Concurrent turns for one conversation are serialized; separate conversations can proceed independently.
- Downstream cancellation cancels upstream I/O and releases locks.
- Expired sessions fail closed and request re-authentication.
- Policy blocks and rate limits are not retried or bypassed.
- Logs, errors, metrics, request history, and backups contain no credentials.
- The provider remains unadvertised/unroutable until these gates pass.

## Reference research

- `kittors/deepseek-web-api` (MIT): useful protocol research for observed completion payloads, SSE patch events, session lineage, and OpenAI-compatible mapping. Its source also includes PoW handling; that part is explicitly excluded.
- `sums001/Deepseek-API`: research lead only; its challenge-solving implementation is excluded. Review license before any reuse.

Do not copy protocol assumptions blindly. Re-verify current behavior from sanitized, user-authorized traffic and implement independently in Go.
