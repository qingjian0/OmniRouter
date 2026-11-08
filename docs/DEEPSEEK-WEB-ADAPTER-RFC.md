# DeepSeek consumer-web adapter RFC

Status: Slice A security boundary and Slice B pairing path/source implemented / not release-certified  
Target branch: `feature/deepseek-web-adapter` (stacked on the official API adapter branch)

## 1. Scope and trust boundary

The consumer web adapter is a separate Provider kind, `deepseek-web`. It is not
an official DeepSeek API integration and must never share API-key assumptions,
health state, model discovery cache, or request transport with `deepseek`.

It may use only a session that the local account owner has established by
interactive login in a browser they control. The adapter must not collect
account passwords, automate MFA or CAPTCHA, defeat WAF/human checks, implement
or outsource anti-abuse challenge solving, rotate identities, or retry around
rate limits. If the normal browser session is challenged, blocked, or rejected,
the adapter returns a typed re-authentication / upstream-policy error and
waits for the user to complete the normal browser flow.

The web protocol is private, undocumented, and changeable. It is best-effort;
the official `deepseek` adapter remains the recommended and preferred path.

## 2. Evidence and protocol volatility

Current community implementations document the following observed protocol
shape (not a stable contract):

- Browser-owned state includes `localStorage.userToken` and cookies for
  `chat.deepseek.com`, including `ds_session_id`.
- A completion request has been observed at
  `POST /api/v0/chat/completion`.
- The web stream uses update fragments such as `THINK` and `RESPONSE`,
  rather than OpenAI's Chat Completions event schema.
- Web chat conversation continuity uses an upstream chat-session identifier
  and parent response-message identifier.
- Some implementations report a proof-of-work challenge header. OmniRouter
  will not implement a solver or bypass for that challenge. A browser-native
  request may only proceed when the user's ordinary authenticated browser
  session completes the site's own checks.

These observations are discovery notes, not guarantees. Every protocol
assumption must be isolated behind a versioned transport and covered by
fixtures. A changed/unknown response shape must fail closed.

## 3. Credential and account lifecycle

Reuse the existing Account, Credential Vault, and Session repositories. Do not
create a second secret store.

- One Account represents one user-owned DeepSeek web identity.
- Store the browser-derived bearer/access token as a credential of kind
  `access_token`; store the required cookie material as kind `cookie`.
- Both values must be encrypted by the existing AES-GCM vault. Credential
  metadata may expose kind, status, expiry, and version, never plaintext.
- Never store passwords, MFA seeds, browser profile databases, or raw browser
  storage dumps in provider metadata or request logs.
- A Session row records only local session lifecycle metadata: active/expired/
  revoked/error, expiry when known, and last checked time. It does not contain
  token material.
- Session import must be explicit, local-admin authenticated, size-limited,
  origin-scoped to `chat.deepseek.com`, and reject malformed or unrelated
  cookies. Secrets are write-only in the admin API.
- Replacing either credential increments its vault version. Revocation is
  terminal; re-authentication creates/replaces a new active credential pair
  according to repository lifecycle rules.
- Redact `Cookie`, `Set-Cookie`, Authorization, userToken, session IDs, and
  parent-message IDs from all logs, traces, errors, and metrics labels.

## 4. Browser authorization flow

Preferred implementation: a small, user-installed browser extension for
Chrome/Edge (and Firefox only after a separate compatibility test). The user
opens and logs in to `https://chat.deepseek.com` normally, then explicitly
clicks "Connect this tab" in the extension. The extension reads only the
minimum session values from that active DeepSeek origin and sends them to the
local OmniRouter pairing endpoint.

Pairing protocol:
1. The authenticated local admin UI creates a cryptographically random,
   single-use pairing nonce with a short expiry (maximum 2 minutes).
2. The nonce is bound to the selected provider/account creation request and
   consumed atomically by the first valid extension submission.
3. The extension may submit only while the active tab origin is exactly
   `https://chat.deepseek.com`; it must not read unrelated tabs or origins.
4. The local endpoint accepts only loopback connections by default, validates
   the configured extension origin and nonce, enforces a small request-size
   limit, and immediately passes the material to the authorization validator.
5. The extension clears transient values after the response. The admin UI
   receives status and non-secret metadata only.

The authenticated admin endpoint is `POST /admin/deepseek-web/pairings`. The
extension submits to `POST /internal/deepseek-web/pairings`. Submission
requires the exact configured extension `Origin`, a loopback peer, JSON content
type, and a single-use unexpired nonce. The nonce is sent in the `X-Omni-Pairing-Nonce` header, not the URL, so request telemetry cannot persist the bearer nonce. The JSON `token` field is base64-encoded
so the Go boundary can decode it directly into a byte buffer; `cookies` contains
only the currently required `ds_session_id` cookie in `{name,value,domain,path,secure}` form. Do not collect all cookies for the site; expand the allowlist only when a sanitized fixture proves an additional cookie is required. The nonce is consumed before
payload parsing, so malformed attempts cannot be replayed.

The extension must declare the narrowest possible host permissions and show
the user an explicit consent action before reading session material. No
password, MFA seed, browser profile, browsing history, or page content is
collected. Do not attach to or scrape the user's default browser profile, do
not export a whole profile, and do not inject cookies into unrelated domains.

A dedicated visible browser profile may be offered as an alternative desktop
flow only if it uses ordinary interactive login and never automates site
verification. Headless login, challenge solving, and background anti-abuse
automation are out of scope.

If the extension or browser is unavailable, the UI reports that interactive
authorization is unavailable; it must not silently fall back to password
login or unrestricted manual secret scraping. Android/Termux support is
conditional on a browser that supports the extension bridge; otherwise the
official API adapter remains available.

## 5. Provider contract

- Provider kind: `deepseek-web`.
- Default upstream origin: `https://chat.deepseek.com`; origin is fixed by
  the adapter and is not user-overridable.
- Public gateway ingress: reuse the existing OpenAI Chat Completions and
  Responses routes. Do not advertise Anthropic or Gemini support until a
  separately tested mapping exists.
- Initial model mapping is conservative and must be learned from the logged-in
  web client, not inferred from official API model IDs.
- Text and reasoning fragments are normalized into the existing response
  envelope. Preserve reasoning separately; never silently concatenate
  reasoning into user-visible answer text.
- Streaming must validate framing, cap frame size, enforce request deadlines,
  propagate downstream cancellation, and require a recognized terminal event.
- Tool calling is not native to the web protocol. Do not advertise native
  tools until an explicit compatibility mode is separately implemented and
  validated. Prompt-emulated tools must be labelled as best-effort.
- Image upload/vision is out of scope for the first adapter increment.

## 6. Conversation continuity

Keep local gateway conversation IDs distinct from upstream web session IDs.
Persist the mapping in a dedicated session-lineage store keyed by account and
local conversation ID. Never trust a client-supplied upstream session ID or
parent-message ID.

Serialize turns within one conversation. Concurrent turns against the same
upstream parent can fork or corrupt the conversation. A new local conversation
must create a new upstream conversation; a revoked/expired account session must
not silently reuse old lineage.

## 7. Scheduling and failure semantics

The web adapter advertises a separate capability profile and lower default
scheduler trust than the official API adapter.

Typed outcomes:
- `auth_required`: user must complete ordinary browser login.
- `session_expired`: session is no longer accepted; disable the account until
  re-authentication.
- `upstream_changed`: unrecognized endpoint, schema, or SSE event; open a
  compatibility incident and do not retry blindly.
- `policy_blocked`: site challenge, WAF, anti-abuse, or policy rejection;
  stop automated attempts and require user intervention.
- `rate_limited`: honor Retry-After when supplied; do not evade limits.
- `unsupported_capability`: reject before dispatch when model/protocol mapping
  is not explicitly supported.

Health checks must not send completion prompts. They may perform a low-impact
session validation only after the user has enabled the account. A web adapter
failure must not mark the official DeepSeek API provider unhealthy.

## 8. Admin UI

The provider form distinguishes "DeepSeek 官方 API" from "DeepSeek 网页会话". The current increment adds the inert web-provider registration form and authenticated one-time pairing-code creation/copy flow. The pairing UI requires `OMNI_GATEWAY_API_KEY` and the exact extension-origin setting. The accounts table exposes an account-wide revoke action at `POST /admin/deepseek-web/accounts/{id}/revoke`, which disables the account and revokes its stored credentials and sessions. It does not claim that inference is available. Last-validation and re-authentication controls remain pending.

For the completed web adapter show:

- Browser authorization state and an explicit Open Login / Recheck action.
- Account/session status and last validation time.
- Model and capability list discovered from the authorized web session.
- A Re-authenticate action and a Revoke Session action.
- A warning that this is an unofficial, best-effort integration.
- No field that displays or echoes cookie/token plaintext.

All state-changing actions require the existing admin gateway authentication.

## 9. Delivery slices and acceptance gates

### Slice A — domain and storage integration
- [x] Add strict browser authorization material validation for `chat.deepseek.com` cookies and access tokens.
- [x] Add an authorization service that creates a separate Account, stores access token and cookie header as separate encrypted Vault credentials, and records Session metadata.
- [x] Add restart restoration that requires exactly one active token and one active cookie credential for the account.
- [x] Add account-wide revoke semantics for credentials, sessions, and account state.
- [x] Add tests for malformed/foreign cookies, injection, duplicate cookies, missing session ID, input-buffer isolation, redaction header names, and typed upstream status mapping.
- [x] Add SQLite integration coverage for authorize / encrypted restart restore / revoke.
- [x] Execute repository-wide CI: run `41972102296` passed formatting, frontend build, `go test ./...`, race tests, `go vet ./...`, workflow checks, and cross-build/package jobs.
- [x] Add SQLite failure-injection coverage for partial persistence rollback; cleanup uses a detached bounded context so request cancellation cannot strand credentials.
- [x] Add an inert `deepseek-web` registration shell so admins can create the provider and pair a session; it advertises zero models and rejects API keys/model lists, so it cannot receive inference traffic.
- [ ] Replace the inert shell with the real runtime Provider only after the version-isolated browser transport and SSE interfaces are implemented.
- Do not send upstream completion requests yet.

### Slice B — interactive browser authorization
- [x] Add an in-memory, cryptographically random, single-use pairing ticket with a 2-minute lifetime and bounded pending-ticket count.
- [x] Add exact-origin validators for the DeepSeek page and administrator-configured browser extension origin.
- [x] Add loopback-peer validation for the future local pairing endpoint.
- [x] Add tests for ticket replay, expiry, pending-ticket limits, origin spoofing, and non-loopback peers.
- [x] Implement the authenticated admin pairing endpoint and exact extension-origin allowlist configuration (`OMNI_DEEPSEEK_WEB_EXTENSION_ORIGIN`).
- [x] Add Chromium Manifest V3 extension source with explicit active-tab consent and minimal session extraction.
- [x] Connect the loopback-only pairing submission endpoint to the encrypted AuthorizationService.
- [ ] Validate extension pairing in Chromium on Windows, Linux, and macOS; evaluate Android browser support separately.
- [ ] Test extension pairing on Windows, Linux, and macOS; evaluate Android browser support separately.
- No real account credentials in CI.

### Slice C — protocol transport
- Capture sanitized fixtures from a user-authorized session.
- Verify endpoint, request schema, auth, model mapping, SSE event taxonomy.
- Implement only the protocol behavior observed in fixtures.
- No PoW solver, CAPTCHA/MFA automation, WAF bypass, or anti-abuse evasion.
- Local fake-server tests for malformed frames, unknown event types, cancellation,
  timeouts, and upstream errors.

### Slice D — gateway and admin
- Chat Completions and Responses translation.
- Per-account conversation lineage and serialized turns.
- [x] UI for inert Provider registration, one-time pairing-code creation/copy, and account-wide revoke.
- [ ] UI for browser login state, last session validation, session health, and model refresh.
- Usage accounting marked estimated if upstream token usage is unavailable.

### Release acceptance
- Live acceptance with a user-owned account, performed by the user.
- Explicit evidence of text, reasoning, streaming, multi-turn continuity, expiry,
  revoke, rate-limit handling, and graceful protocol-change failure.
- Security review of browser profile and secret handling.
- Cross-platform tests for Windows, Linux, macOS, and Android Termux.
- Keep this adapter opt-in and excluded from default routing until it meets the
  same operational standards as other providers.

## 10. Reference implementations

These projects are useful for protocol research only; neither is an upstream
contract or a dependency:

- https://github.com/kittors/deepseek-web-api — TypeScript/Playwright browser
  session lifecycle, SSE normalization, and session-lineage design.
- https://github.com/sums001/Deepseek-API — Python browser sign-in and
  OpenAI-compatible compatibility layer.

Review their licenses and current source before copying any code. Prefer a
clean Go implementation of the documented interfaces and sanitized fixtures.
