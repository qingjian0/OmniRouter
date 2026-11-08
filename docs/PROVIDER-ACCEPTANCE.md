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

The script sends six short requests through the same configured OpenAI-compatible
upstream:

- OpenAI Chat Completions: non-streaming and SSE.
- Anthropic Messages: non-streaming and SSE.
- Gemini `generateContent` / `streamGenerateContent`: non-streaming and SSE.

It checks the expected response arrays for each non-streaming protocol and SSE
data events for each streaming protocol. The OpenAI stream must also include the
explicit `data: [DONE]` terminal marker. Response bodies are never printed.

A successful run proves that the selected model can serve basic text requests
through the three inbound protocol adapters at that time. It does not certify
native Anthropic or Gemini upstream APIs, tool calls, vision, provider failover,
long streams, cancellation, or every model capability. Those require separate
acceptance cases and must be recorded independently.


## Optional GitHub Actions live test

The repository also provides `.github/workflows/live-provider-smoke.yml`.
After this workflow is merged to the default branch, configure these repository
Actions secrets:

- `OMNI_PROVIDER_BASE_URL` — OpenAI-compatible upstream root URL, usually ending
  in `/v1`.
- `OMNI_PROVIDER_API_KEY` — upstream credential.
- `OMNI_CREDENTIAL_ENCRYPTION_KEY` — Base64-encoded 32-byte key used by the
  gateway's persistent credential vault.
- `OMNI_GATEWAY_API_KEY` — gateway access key.

Then run **Actions → Live Provider Smoke → Run workflow** and enter one model ID
that the upstream supports. The workflow builds OmniRouter, starts it with a
temporary SQLite database, and executes all six non-streaming and SSE protocol smoke requests.
It does not reveal the secret values or print response bodies.

This workflow validates one real OpenAI-compatible upstream through all three inbound protocol adapters for basic text requests. It does not certify native Anthropic/Gemini upstream APIs, tools, vision, failover, cancellation, long-stream behavior, or Android/Termux runtime.
Do not add credentials to workflow inputs, source files, or commit messages.


## Official native provider acceptance

The generic smoke test above is intentionally limited to one OpenAI-compatible
upstream. It does not validate the official native OpenAI Responses, Anthropic
Messages, or Gemini APIs.

Use [LIVE-PROVIDER-MATRIX.md](LIVE-PROVIDER-MATRIX.md) and the
[Native Provider Live Acceptance workflow](../.github/workflows/native-provider-live.yml)
for separate official-upstream checks. OpenAI Responses, Anthropic Messages,
and Gemini use their native outbound adapters; DeepSeek uses its dedicated
official API smoke suite. A successful basic-text run is not a certification
of tools, vision, long streams, cancellation under load, rate-limit recovery,
or all model capabilities.
