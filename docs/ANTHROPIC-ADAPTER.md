# Native Anthropic Messages Adapter

OmniRouter supports Anthropic as a native upstream provider kind:

- `kind: anthropic`
- Default base URL: `https://api.anthropic.com/v1`
- Request endpoint: `POST /messages`
- Authentication: `x-api-key`
- Protocol version: `anthropic-version: 2023-06-01`

The adapter forwards the native Messages JSON envelope without translating it
through OpenAI Chat Completions. This preserves Anthropic-specific request
fields and content blocks supported by the upstream API.

## Configuration

Environment-variable mode:

```sh
OMNI_PROVIDER_KIND=anthropic
OMNI_PROVIDER_API_KEY=...
OMNI_PROVIDER_MODELS=claude-model-id
OMNI_CREDENTIAL_ENCRYPTION_KEY=<base64-encoded-32-byte-key>
OMNI_GATEWAY_API_KEY=<gateway-master-key>
```

The base URL defaults to `https://api.anthropic.com/v1`. Override it with
`OMNI_PROVIDER_BASE_URL` for a compatible endpoint. The configured provider
model IDs are the routing catalogue; this adapter does not currently discover
models automatically.

For `OMNI_PROVIDERS` JSON configuration, use the same `kind`, `id`,
`base_url`, `api_key`, and `models` fields as other providers. Optional
per-model features can be declared with `model_capabilities`, for example
`{"claude-model-id":["tools","vision"]}` after verifying the selected model supports them. Provider
credentials are persisted through the encrypted credential vault when an API
key is configured; do not put secrets in source-controlled configuration.

## Capability boundary

The adapter declares native `anthropic_messages` and streaming capabilities by
default. Optional model features such as `tools`, `vision`, `reasoning`, and
`json` must be declared per model through `model_capabilities`; the adapter
filters out `chat`, `anthropic_messages`, and `streaming` from that optional
list so a configuration cannot accidentally claim OpenAI Chat Completions.
It deliberately does **not** declare OpenAI Chat Completions support: its `Generate` and `Stream` methods
return explicit unsupported-operation errors. The gateway's native
`POST /v1/messages` route selects it through the optional
`provider.AnthropicProvider` interface.

Non-streaming responses are returned as the upstream JSON envelope. Streaming
responses preserve upstream SSE frames. A stream is successful only after an
explicit `message_stop` event; EOF without that terminal event is reported as
an incomplete upstream stream.

## Current limitations

- Model catalogue discovery and automatic capability enrichment are not yet implemented.
- Anthropic-specific extended-thinking semantics are passed through only where
  the request reaches the native route; the adapter does not infer support for
  additional capabilities not explicitly declared.
- Live upstream authentication and account-level quota behavior require a
  separate integration smoke test with user-supplied credentials.
