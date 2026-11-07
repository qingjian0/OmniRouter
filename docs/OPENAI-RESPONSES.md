# OpenAI Responses Provider

OmniRouter's `openai-responses` adapter connects to an upstream that implements the OpenAI Responses API natively. It is a separate provider kind from `openai-compatible`, which targets Chat Completions.

## Configuration

Use a Base URL that includes the API version prefix when required by the upstream. The adapter appends `/responses` and uses Bearer authentication.

Example provider record:

```json
{
  "kind": "openai-responses",
  "id": "openai-responses-primary",
  "name": "OpenAI Responses",
  "base_url": "https://api.openai.com/v1",
  "api_key": "REDACTED",
  "models": ["gpt-4.1"],
  "model_capabilities": {
    "gpt-4.1": ["tools", "vision", "reasoning", "json"]
  },
  "priority": 100,
  "weight": 1
}
```

The API key is supplied through the normal encrypted provider-credential flow. Do not commit real credentials to configuration files or source control.

## Supported behavior

- Native non-streaming `POST /responses` JSON requests.
- Native streaming `POST /responses` SSE requests.
- Bearer token authentication.
- Explicit model declarations from provider configuration.
- Model capability metadata supplied by the operator, plus adapter-owned `responses` and `streaming` declarations.
- Health check through `GET /models`.

The adapter advertises `openai_responses` as its API capability. It does not advertise Chat Completions support and does not translate Responses payloads into `/chat/completions`.

## Stream completion and failure semantics

A stream is considered successfully complete only after the upstream emits `response.completed`. The adapter treats `response.failed`, `response.incomplete`, and `error` events as failures. An empty stream, malformed event payload, non-SSE content type, or EOF without `response.completed` is not reported as a successful completion.

Streaming requests are not automatically retried after downstream response headers or event data have been sent.

## Base URL examples

- OpenAI: `https://api.openai.com/v1`
- OpenAI-compatible Responses service: use the service's documented API root, including any version prefix.

This adapter does not imply that every OpenAI-compatible service supports the Responses API. Select this provider kind only when the upstream explicitly implements `POST /responses`.
