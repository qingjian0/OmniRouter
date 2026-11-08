# DeepSeek Web Reverse-Proxy Reference Audit

Audit date: 2026-11-08  
Scope: DeepSeek consumer Web only (chat.deepseek.com). This is a source-level comparison, not a live compatibility certification.

## Executive conclusion

The strongest implementation reference found in this audit is [NIyueE/ds-free-api](https://github.com/NIyueeE/ds-free-api). At audit time it had 865 GitHub stars, 192 forks, was updated on 2026-11-08, and was licensed GPL-3.0. Its Rust implementation is much more feature-complete than OmniRouter's current DeepSeek Web provider shell. Its separate ds_core crate and raw-api-reference.md provide a detailed, dated protocol reference.

For OmniRouter, use it as a behavioral and protocol research source only. Do not copy its GPL-3.0 implementation into this Go project unless the repository's licensing decision is explicitly changed. Its login, device identity, challenge, WAF, and risk-control machinery are not implementation targets for OmniRouter.

The best language/architecture matches remain:
- [JuanCMPDev/deep-proxy](https://github.com/JuanCMPDev/deep-proxy) — MIT, Go; compact source-level reference for request mapping, stream parsing, and OpenAI SSE.
- [kittors/deepseek-web-api](https://github.com/kittors/deepseek-web-api) — MIT, TypeScript; strong reference for event-aware SSE decoding and multi-turn session bookkeeping.

Additional references:
- [nv-hoai/deepseek-rp](https://github.com/nv-hoai/deepseek-rp) — MIT, Python; clean separation of client orchestration, typed events, SSE parser, transport, and adapters.
- [afterburnerr/FreeDeepSeekAPI](https://github.com/afterburnerr/FreeDeepSeekAPI) — repository metadata did not assert a license; useful for observing legacy and JSON-Patch stream compatibility, session pooling, and operational warnings, but not a code-reuse source.
- [lcx9451/deepseek-web-proxy](https://github.com/lcx9451/deepseek-web-proxy) — 2026 protocol research lead; license was not established in this audit. Its repository tree includes a committed browser profile directory and browser state files. Do not clone, redistribute, or use those profile artifacts; they may contain sensitive session material. Treat the source as untrusted and do not reuse its credential files.
- [lisycotana/ds-free-proxy](https://github.com/lisycotana/ds-free-proxy) — MIT, small Node implementation based on freeseek; its README describes extension-pushed credentials, but the project is small and is not the primary architecture reference.
- [gyatstian/deepseaport](https://github.com/gyatstian/deepseaport) — multi-provider project; not a scope target because OmniRouter's current work is DeepSeek-only.

## Source files inspected

### NIyueeE/ds-free-api (GPL-3.0; research only)
- [README](https://github.com/NIyueeE/ds-free-api)
- [Raw API reference](https://github.com/NIyueeE/ds-free-api/blob/main/ds_core/raw-api-reference.md)
- [Development guide](https://github.com/NIyueeE/ds-free-api/blob/main/docs/development.md)
- [OpenAI request prompt adapter](https://github.com/NIyueeE/ds-free-api/blob/main/src/openai_adapter/request/prompt.rs)
- [OpenAI request resolver](https://github.com/NIyueE/ds-free-api/blob/main/src/openai_adapter/request/resolver.rs)
- [OpenAI response converter](https://github.com/NIyueE/ds-free-api/blob/main/src/openai_adapter/response/converter.rs)
- [OpenAI tool parser](https://github.com/NIyueE/ds-free-api/blob/main/src/openai_adapter/response/tool_parser.rs)
- [Idempotency store](https://github.com/NIyueE/ds-free-api/blob/main/src/server/idempotency.rs)
- [Responses store](https://github.com/NIyueE/ds-free-api/blob/main/src/responses_adapter/store.rs)

### Go / TypeScript / Python references
- [deep-proxy upstream client](https://github.com/JuanCMPDev/deep-proxy/blob/main/internal/upstream/deepseek.go)
- [deep-proxy request mapper](https://github.com/JuanCMPDev/deep-proxy/blob/main/internal/proxy/translate_request.go)
- [deep-proxy Patch decoder](https://github.com/JuanCMPDev/deep-proxy/blob/main/internal/proxy/deepseek_chunk.go)
- [deep-proxy stream converter](https://github.com/JuanCMPDev/deep-proxy/blob/main/internal/proxy/translate_stream.go)
- [kittors completion request](https://github.com/kittors/deepseek-web-api/blob/main/src/deepseek/completion.ts)
- [kittors update decoder](https://github.com/kittors/deepseek-web-api/blob/main/src/deepseek/updates.ts)
- [kittors session store](https://github.com/kittors/deepseek-web-api/blob/main/src/deepseek/sessionStore.ts)
- [kittors session turn matching](https://github.com/kittors/deepseek-web-api/blob/main/src/deepseek/sessionTurns.ts)
- [nv-hoai SSE parser](https://github.com/nv-hoai/deepseek-rp/blob/main/dsk/sse.py)
- [nv-hoai client orchestration](https://github.com/nv-hoai/deepseek-rp/blob/main/dsk/client.py)

## Protocol findings from the dated 2026-10 reference

The NIyueE raw reference reports the following observed shape. These are third-party observations and must still be confirmed against a current, user-authorized, sanitized fixture before OmniRouter sends a request.

### Session and completion
- Session creation is documented as POST /api/v0/chat_session/create; the session ID is extracted from data.biz_data.chat_session.id.
- Completion is documented as POST /api/v0/chat/completion.
- Its observed completion body includes chat_session_id, parent_message_id, model_type, prompt, ref_file_ids, thinking_enabled, search_enabled, source, action, and preempt.
- The 2026-10 reference specifically records source as an additional field that is absent from several older public examples.
- It reports that, for a plain text turn when reusing a session, prompt contains only the latest user message; previous turns are already represented by the upstream session and parent_message_id. It notes that richer tool/file/search flows may still include additional prompt context.
- It reports an upstream session TTL of 259200 seconds (3 days). This must be treated as an observation, not a guaranteed retention contract.

### SSE framing and state
- SSE event names carry semantic information: ready, update_session, title, and terminal events must not be flattened into generic JSON deltas.
- ready may contain both request_message_id and response_message_id; the response ID is a candidate for the next turn's parent only after the stream is confirmed successful.
- Patch frames may use p (path), o (operation), and v (value). The last path and operation can be inherited when omitted.
- Full response snapshots and fragment APPEND operations are distinct from content-only APPEND operations.
- THINK and RESPONSE are separate channels. The current fragment type must be tracked so content-only deltas are not misrouted.
- A BATCH operation can carry accumulated_token_usage and a quasi-terminal status.
- update_session is metadata, not a terminal event. Treating it as stream completion would truncate output.
- Public references differ in whether they recognize close, finish, response/status=FINISHED, or a batch quasi-status as the terminal signal. OmniRouter must not select one by guesswork; require a current sanitized fixture and test all observed terminal forms.

### Model availability
The 2026-10 NIyueE reference reports default as enabled/switchable and expert / vision as disabled in its observed /api/v0/client/settings response. This conflicts with older examples that map deepseek-reasoner to expert. OmniRouter must query/verify actual available models or keep its advertised model list conservative; do not hard-code historical model aliases as current availability.

## Additional 2026-11-08 risk-control finding (research only)

A subsequently inspected development document for the GPL-3.0 `NIyueeE/ds-free-api` reference reports that its current completion path obtains a short-lived HIF-LEIM value from `hif-leim.deepseek.com/query` and sends both `x-hif-leim` and `x-ds-pow-response` on completion requests. The same source describes this as an upstream risk-control requirement and reports that omitting the HIF header causes the request to be rejected.

This is third-party, source-level evidence, not an official DeepSeek contract and not a live OmniRouter test. It materially changes the feasibility assessment: a basic cookie/token POST may not be sufficient for current web completion.

**OmniRouter policy remains unchanged:**
- Do not implement or import proof-of-work solvers, risk-token acquisition intended to satisfy anti-abuse challenges, browser/device impersonation, challenge replay, CAPTCHA/WAF bypass, or rate-limit evasion.
- Do not fabricate `x-hif-leim` or `x-ds-pow-response`, and do not copy values from public repositories or captured profiles.
- The transport may send only ordinary requests supported by the user's explicit authorization and verified fixtures. If the upstream requires an anti-abuse challenge that cannot be completed through the normal official user flow, return a typed fail-closed policy/protocol error.
- Do not advertise DeepSeek Web models or mark live inference as supported until an ordinary authorized request succeeds without bypassing these controls and a sanitized two-turn fixture confirms session continuity.

Reference: [NIyueeE/ds-free-api development notes](https://git.xszz.org/NIyueeE/ds-free-api/blob/main/docs/development.md). GPL-3.0 source remains research-only; no code is copied.

## Cross-project comparison and implications

| Concern | What the references show | OmniRouter decision |
| --- | --- | --- |
| Go implementation | deep-proxy offers a compact Go baseline | Keep OmniRouter-native Go types and tests; port only small, license-compatible, isolated logic if it materially reduces risk |
| SSE event handling | kittors and nv-hoai preserve event names and patch state; deep-proxy is smaller and less strict | Keep the existing stateful Go decoder; add fixtures for ready, update_session, title, close/finish, full snapshots, inherited path/op, APPEND, BATCH, and malformed patches |
| Parent-message lineage | Multiple references persist the latest response message ID and use it on the next turn | The existing SQLite lineage + lease coordinator is the right foundation; commit only after a verified terminal success |
| Prompt history | Some projects resend a rolling transcript; NIyueE documents incremental prompts when upstream session reuse is active | Separate session policy from prompt-window policy. Do not send the same prior turns both through upstream lineage and in the prompt unless a fixture proves that is required |
| Concurrency | References describe per-account request limits and per-session serialization | Conversation lease is necessary but may not be sufficient; add a separate per-account concurrency gate and queue/reject policy based on observed account limits, never account rotation to evade limits |
| Idempotency | NIyueE implements bounded in-flight/completed request idempotency | Consider an OmniRouter idempotency layer to prevent client retries from issuing duplicate upstream turns; do not persist user response bodies by default |
| Tool calls | Several projects emulate tools by prompt tags and parse the output | Keep active tools unsupported until strict parser, output validation, tool-result continuation, and failure tests exist |
| License | NIyueE is GPL-3.0; deep-proxy and kittors are MIT | No GPL code import. MIT references still require attribution and a file-level license/provenance review before copying |

## Recommended implementation order for OmniRouter

1. Fix the current CI first. The 2026-11-08 run failed because internal/websession/coordinator_test.go imported omnirouter/internal/storage without using it. Remove the unused import and rerun the exact branch-head workflow.
2. Keep the current Provider inert while building protocol fixtures and a mock upstream server.
3. Extend the neutral SSE decoder tests to cover named SSE events and inherited patch path/operation semantics. Preserve search metadata separately; never append it blindly to assistant content.
4. Add a versioned wire DTO for the observed completion fields, including the newly reported source field, but do not send it to the real service until the fixture verifies the exact request.
5. Decide between incremental prompt and rolling-history prompt only after a two-turn capture proves how the current DeepSeek session behaves.
6. Integrate the existing lease coordinator with the selected local authorized account. Hold the lease for the entire upstream stream; commit the new parent ID only after explicit successful completion.
7. Add a separate account-level concurrency guard. Do not rotate accounts, impersonate other devices, or evade rate limits.
8. Keep tool calling, file upload, search, vision, Responses API, and model aliases disabled until each has independent fixtures and acceptance tests.
9. Only enable model advertisement after the user-authorized live smoke test passes. A mock server proves adapter behavior, not DeepSeek compatibility.

## Security and licensing exclusions

- Do not use committed browser profiles or credential files from public repositories. The lcx9451 repository tree includes browser-profile state; it is not a safe credential source.
- Do not copy passwords, session cookies, bearer tokens, device identifiers, browser storage, or user data into fixtures.
- Do not implement PoW solvers, WAF/CAPTCHA bypass, challenge replay, device identity spoofing, MFA automation, account rotation, or rate-limit evasion.
- If DeepSeek rejects an ordinary user-authorized request because a challenge or policy action is required, return a typed fail-closed error and require the user to complete the official browser flow.
- GPL-3.0 source from NIyueE/ds-free-api and GPL-3.0 source from Chat2API are research-only for this repository unless a compatible licensing decision is explicitly made.

## Current status at audit time

- OmniRouter PR #21 remains Draft, targets dev, and is not merged.
- The DeepSeek Web Provider is still inert; no real upstream completion has been validated.
- Request mapping, OpenAI SSE encoding, SQLite lineage, and the conversation lease coordinator exist as isolated components, not a complete live provider.
- The latest inspected CI failure was a compile error from the unused import in coordinator_test.go. A cleanup commit was pushed after identifying it; its replacement CI must be checked before reporting a pass.


## Direct GitHub source inspection (2026-11-08 14:15Z)

The following files were fetched from the repositories themselves during this pass; this is not a README-only comparison.

### kittors/deepseek-web-api — MIT, active reference

Inspected:
- `src/deepseek/completion.ts`
- `src/deepseek/sse.ts`
- `src/deepseek/updates.ts`
- `src/deepseek/sessionStore.ts`
- `src/deepseek/client.ts`
- `src/deepseek/types.ts`
- `LICENSE`

Concrete reusable design observations:
- The SSE parser is stateful across arbitrary network chunk boundaries, preserves named event labels, joins multiple data lines, and flushes a final unterminated frame.
- The update decoder separates `ready`, `title`, `close`, reasoning, answer, and token-usage events. It tracks the active fragment channel so content-only APPEND operations do not accidentally mix THINK and RESPONSE.
- Session storage persists the last response message ID and uses it as the next turn's parent; it supports explicit IDs, previous-response IDs, and normalized history matching.
- The completion client keeps request preparation, session resolution, auth refresh, streaming, and persistence in separate layers.
- The completion request currently includes browser-derived auth and a PoW response. Its login/browser and PoW path is not adopted under OmniRouter's fail-closed anti-abuse boundary.

### JuanCMPDev/deep-proxy — MIT, best Go-language reference

Inspected:
- `internal/proxy/translate_request.go`
- `internal/proxy/deepseek_chunk.go`
- `internal/proxy/translate_stream.go`
- `LICENSE`

Concrete observations:
- Its request mapper is compact and directly comparable to OmniRouter's Go mapper.
- Its patch parser demonstrates fragment-type tracking, response-only output, token usage extraction, and status handling.
- Its stream converter demonstrates OpenAI chunk assembly and terminal output.
- Its current upstream request flow explicitly uses PoW/WASM and browser-captured credentials. Those parts are excluded; only the language-level separation and independently re-tested parsing/translation ideas are candidates for reuse.

### nv-hoai/deepseek-rp — Python reference

Inspected:
- `dsk/sse.py`
- `dsk/client.py`

Concrete observations:
- The parser explicitly documents inherited patch path/operation state, full-response snapshots, fragment APPEND, content APPEND, BATCH, and named metadata events.
- The client separates transport, typed models, cookies, device identity, challenge logic, and stream parsing. Its challenge, device-identity, and WAF recovery implementation is not adopted.

### lcx9451/deepseek-web-proxy — protocol lead only

The public README describes a 2026 protocol shape with session creation, a challenge endpoint, completion JSON, and patch-style SSE. This is a lead for cross-checking event/field names, not a basis for implementing challenge handling. License and provenance were not established; no code is copied.

## Implementation decision after direct inspection

1. Do not wait for the user to supply all protocol research. Continue mining public, license-compatible source and dated protocol notes.
2. Reuse OmniRouter's existing Go SSE framing, patch decoder, OpenAI encoder, and SQLite lease abstractions where they already cover the observed behavior. Do not replace working code merely to mirror another repository.
3. Next code-level comparison should be a gap matrix between OmniRouter's `patch.go` and kittors `updates.ts` / nv-hoai `sse.py`: named event handling, inherited `p/o`, fragment index handling, BATCH usage, and explicit terminal variants. Add isolated Go regression fixtures for only the compatible, verified cases.
4. Build and test an HTTP transport against a local mock upstream using the existing abstractions. Do not enable live provider registration until the ordinary authorized request path is verified.
5. No PoW solver, HIF-LEIM acquisition, browser/device impersonation, CAPTCHA/WAF bypass, challenge replay, or rate-limit evasion will be ported. If the current upstream requires such a gate outside a normal user-authorized flow, return a typed fail-closed error rather than simulating success.
