# DeepSeek official API adapter

OmniRouter uses a dedicated `kind: deepseek` adapter for the official DeepSeek
API. This is separate from the generic OpenAI-compatible adapter and from the
future DeepSeek consumer-web adapter.

## Official endpoint and current models

- API root: `https://api.deepseek.com` (do not append `/v1`).
- Authentication: `Authorization: Bearer <API_KEY>`.
- Chat endpoint: `POST /chat/completions`.
- Model catalogue: `GET /models`.
- Current model IDs documented by DeepSeek: `deepseek-flash` and
  `deepseek-v4-pro`.
- `deepseek-chat` and `deepseek-reasoner` are legacy IDs and were scheduled
  for discontinuation on 2026-07-24. New configurations should not use them.

The adapter reads the authenticated model catalogue and maps model ID, display
name, context window, maximum output tokens, input/output modalities, reasoning
effort levels, and protocol capability declarations into OmniRouter model
metadata. The catalogue is cached for five minutes. The default/offline fallback
contains the two current documented model IDs; it is used only when discovery
cannot reach the API. A health check still reports discovery/authentication
failure rather than treating fallback metadata as proof of availability.

## Configuration

Environment variables:

```sh
export OMNI_PROVIDER_KIND=deepseek
export OMNI_PROVIDER_ID=deepseek
export OMNI_PROVIDER_NAME=DeepSeek
export OMNI_PROVIDER_BASE_URL=https://api.deepseek.com
export OMNI_PROVIDER_API_KEY='your-deepseek-api-key'
export OMNI_PROVIDER_MODELS=deepseek-flash,deepseek-v4-pro
```

`OMNI_CREDENTIAL_ENCRYPTION_KEY` must also be configured when an API key is
persisted. Provider secrets are stored through the existing AES-GCM credential
vault and are not written to the Provider metadata row.

Equivalent `OMNI_PROVIDERS` entry:

```json
{
  "kind": "deepseek",
  "id": "deepseek",
  "name": "DeepSeek",
  "base_url": "https://api.deepseek.com",
  "api_key": "REDACTED",
  "models": ["deepseek-flash", "deepseek-v4-pro"],
  "priority": 100,
  "weight": 1
}
```

## Supported in this implementation batch

- OpenAI Chat Completions request and response transport.
- Native OpenAI Responses API ingress at `POST /v1/responses`, forwarded to DeepSeek `/responses` with semantic SSE terminal-event handling.
- Non-streaming and SSE forwarding with explicit `data: [DONE]` validation.
- DeepSeek `thinking` and `reasoning_effort` request fields pass through
  unchanged; `reasoning_content` response and stream deltas are not rewritten.
- Function tool declarations/calls pass through the OpenAI-compatible transport.
- Authenticated model catalogue discovery and model metadata mapping.
- Provider kind persistence and adapter restoration after restart.
- Admin model refresh at `POST /admin/providers/{id}/models/refresh`; this
  refreshes the remote catalogue, persists model names/capabilities, and updates
  the scheduler's model routes.

The gateway's token accounting currently records the common prompt/completion
token counts. DeepSeek's additional reasoning-token and cache-token breakdowns
are preserved in the upstream response but are not yet separately persisted in
usage analytics.

## Explicitly not claimed yet

- Direct native Anthropic Messages upstream transport through the DeepSeek
  `/anthropic` endpoint.
- Consumer website login, browser sessions, or cookie-based access. Those belong
  to a separate web adapter and must not be conflated with the official API.
- Live production acceptance. A valid key and a real upstream smoke run are
  still required before the adapter is release-certified.

## Model refresh

After creating a DeepSeek Provider, call the authenticated admin route:

```http
POST /admin/providers/deepseek/models/refresh
Authorization: Bearer <OMNI_GATEWAY_API_KEY>
```

A successful response returns the discovered model records and count. The route
fails closed on upstream discovery errors, persists the new catalogue, removes
stale model rows for that Provider, and replaces its scheduler routes.

## Live acceptance

The repository includes `scripts/deepseek-smoke.sh` and the manual GitHub Actions
workflow `.github/workflows/deepseek-live-smoke.yml`. Configure the repository
secrets `OMNI_PROVIDER_API_KEY`, `OMNI_CREDENTIAL_ENCRYPTION_KEY`, and
`OMNI_GATEWAY_API_KEY`, then dispatch the workflow with the model to test.
It checks catalogue discovery, Chat Completions (including thinking), native
Responses API, both SSE terminal formats, and usage endpoint availability.
No secret value is printed by the script.
