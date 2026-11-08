# Native Gemini Adapter

OmniRouter's `gemini` Provider kind calls Google's native Gemini REST API directly. It is distinct from the Gemini-compatible gateway surface, which can translate Gemini requests to an OpenAI-compatible upstream.

## Configuration

- Provider kind: `gemini`
- Default Base URL: `https://generativelanguage.googleapis.com/v1beta`
- Authentication: `x-goog-api-key` request header
- Model IDs: explicitly configured model names such as `gemini-2.5-flash`
- Non-streaming endpoint: `POST /v1beta/models/{model}:generateContent`
- Streaming endpoint: `POST /v1beta/models/{model}:streamGenerateContent?alt=sse`

The adapter preserves the Gemini request and response JSON envelopes. It does not translate them through OpenAI Chat Completions and does not claim the `chat` capability.

Example `OMNI_PROVIDERS` entry:

```json
[
  {
    "kind": "gemini",
    "id": "google-gemini",
    "name": "Google Gemini",
    "api_key": "YOUR_GEMINI_API_KEY",
    "models": ["gemini-2.5-flash", "gemini-2.5-pro"]
  }
]
```

Provider API keys continue to use the existing encrypted Credential Vault. Configure `OMNI_CREDENTIAL_ENCRYPTION_KEY` before storing an upstream key.

## Capabilities and routing

The adapter declares `gemini_generate_content` and `streaming` for each configured model. Additional capabilities such as `tools`, `vision`, `reasoning`, or `json` must be explicitly declared in `model_capabilities` for the relevant model. These declarations are routing metadata, not a claim that every model/version supports every Gemini feature.

The gateway's Gemini route prefers a provider implementing the native `GeminiProvider` interface. If none is eligible, it retains the existing Gemini-to-OpenAI-compatible translation path.

## Streaming completion

Gemini `streamGenerateContent` uses SSE and returns a sequence of native `GenerateContentResponse` objects. The adapter forwards complete SSE frames unchanged. A clean HTTP/SSE EOF is treated as the protocol's explicit completion; scanner errors, malformed JSON frames, and provider error envelopes fail the stream. It does not synthesize an OpenAI `[DONE]` frame.

## Boundaries

- This adapter implements `generateContent` and `streamGenerateContent` only.
- It does not implement Gemini Live API, batch generation, embeddings, file upload, or the Interactions API.
- It does not auto-discover model IDs; configure the exact model IDs to be routed.
- Live API smoke tests and real-device acceptance remain separate opt-in gates.
