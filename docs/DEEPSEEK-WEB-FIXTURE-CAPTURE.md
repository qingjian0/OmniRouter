# DeepSeek Web protocol fixture capture

The web-session adapter must be based on behavior observed in a user-authorized
browser session. Do not infer private request fields from guessed schemas and
do not copy raw HAR files into the repository.

## Capture scope

Use only an account you own or are explicitly authorized to test. In Chromium:

1. Open `https://chat.deepseek.com` and sign in normally.
2. Open DevTools → Network, enable Preserve log, and filter Fetch/XHR.
3. Start one new conversation and send a short, non-sensitive test prompt.
4. Identify the request that creates the assistant response and the associated
   response stream. Record method, URL path (not query values), request
   content-type, non-secret header names, response status, response content-type,
   and the observed SSE event names/order.
5. Manually create a sanitized fixture following the template below. Do not use
   Copy as cURL, export a raw HAR, or paste raw request headers into an issue.

## Required redaction

Remove or replace all of the following before sharing or committing a fixture:

- `userToken`, Authorization values, cookies, `ds_session_id`, CSRF values,
  pairing nonces, account IDs, conversation IDs, message IDs, device IDs, and
  any other bearer or identity material.
- Prompt text, response text, uploaded content, file names, and personal data.
- Query parameter values and opaque challenge material.

Keep only structural information needed to understand field names, data types,
message ordering, event framing, and terminal markers. Use placeholders such as
`<REDACTED_TOKEN>` and `<CONVERSATION_ID>`; do not preserve original lengths
or prefixes if those could identify a credential.

If the site responds with a challenge, proof-of-work requirement, CAPTCHA, WAF
block, or other anti-abuse gate, stop. Record only the HTTP status and a
sanitized error category. Do not solve, replay, automate, or bypass the
challenge.

## Fixture template

Store one fixture per observed protocol behavior under
`internal/provider/deepseekweb/testdata/`:

```json
{
  "fixture_version": 1,
  "observation": "single-turn-stream",
  "request": {
    "method": "POST",
    "path": "/<observed-path>",
    "content_type": "application/json",
    "header_names": ["content-type", "<non-secret-header-name>"],
    "body": {
      "conversation_id": "<CONVERSATION_ID>",
      "messages": [
        {"role": "user", "content": "<REDACTED_PROMPT>"}
      ]
    }
  },
  "response": {
    "status": 200,
    "content_type": "text/event-stream",
    "events": [
      {"event": "<observed-event>", "data": {"text": "<REDACTED_TEXT>"}},
      {"event": "<observed-terminal-event>", "data": {}}
    ]
  }
}
```

The values above are placeholders, not claims about DeepSeek's actual protocol.
Replace them only with sanitized fields observed in the browser.

## Acceptance requirements

Before implementing a versioned transport, collect at least:

- One successful non-streaming request/response fixture, if the browser uses
  that mode.
- One successful streaming sequence including the explicit terminal marker.
- One multi-turn request showing how conversation lineage is represented.
- One sanitized failure response showing ordinary authentication expiry or
  unsupported-session behavior.

Fixtures must be reviewed for secret leakage before they are committed. A
challenge response is not a successful fixture and must never be used to build
a challenge solver.
