# Live Provider acceptance smoke test

The automated Go test suite uses controlled providers and SQLite integration
tests. This script adds a separate, opt-in check against a configured live
upstream through the running OmniRouter gateway.

## Requirements

- A running OmniRouter instance with at least one configured Provider.
- A model ID returned by `GET /v1/models`.
- `curl` and `python3` on the test machine.
- If gateway authentication is enabled, set `OMNI_GATEWAY_API_KEY`.

Do not paste API keys, cookies, or other credentials into source files or chat.
Configure upstream credentials through the server's protected environment and
credential-encryption setup.

## Run

Linux / macOS:

```sh
export OMNI_BASE_URL=http://127.0.0.1:8080
export OMNI_SMOKE_MODEL=your-model-id
# Set only if OMNI_GATEWAY_API_KEY is enabled on the gateway:
export OMNI_GATEWAY_API_KEY='your-gateway-key'
bash scripts/provider-smoke.sh
```

The script sends one short non-streaming request and one SSE request to
`/v1/chat/completions`. It verifies that the non-streaming response contains
a non-empty `choices` array and that the stream contains SSE data and the
explicit `data: [DONE]` terminal marker. It does not print response bodies.

A successful run proves only that the selected configured model works through
the OpenAI-compatible gateway path at that time. It does not certify Anthropic
or Gemini protocol translation, tool calls, vision, provider failover, long
streams, cancellation, or every model capability. Those require separate
acceptance cases and must be recorded independently.
