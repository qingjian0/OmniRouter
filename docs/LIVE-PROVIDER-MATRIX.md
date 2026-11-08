# Official Provider live acceptance matrix

This document distinguishes local adapter tests from calls to official upstream
services. A green unit or integration suite is not a live-provider certification.

## Supported live acceptance paths

| Provider kind | Official endpoint | Gateway ingress tested | Non-streaming | Streaming terminal evidence |
|---|---|---|---|---|
| `openai-responses` | OpenAI Responses API | `POST /v1/responses` | response status is `completed` | `event: response.completed` |
| `anthropic` | Anthropic Messages API | `POST /v1/messages` | message envelope and content exist | `event: message_stop` |
| `gemini` | Gemini generateContent API | `POST /v1beta/models/{model}:generateContent` | at least one candidate | SSE data contains candidate payload |
| `deepseek` | DeepSeek official API | Chat Completions, Responses, Anthropic Messages | response envelope checks | explicit protocol terminal markers |

OpenAI, Anthropic, and Gemini use their native outbound adapters. DeepSeek uses
its dedicated adapter and the expanded DeepSeek smoke suite. Do not substitute
an OpenAI-compatible proxy URL when claiming official-provider acceptance.

## GitHub Actions secrets

Configure these repository Actions secrets. Never enter secret values as
workflow inputs, source files, issue comments, or chat messages.

- `OPENAI_API_KEY`
- `ANTHROPIC_API_KEY`
- `GEMINI_API_KEY`
- `DEEPSEEK_API_KEY`
- `OMNI_CREDENTIAL_ENCRYPTION_KEY` — Base64-encoded 32-byte key.
- `OMNI_GATEWAY_API_KEY` — gateway master key used by the smoke client.

The workflow selects the provider-specific upstream key based on the provider
choice. The encryption and gateway keys are required for every run.

## Running acceptance

1. Ensure `.github/workflows/native-provider-live.yml` is present on the
   repository's default branch; GitHub only exposes manual dispatch for a
   workflow file on the default branch.
2. Open **Actions → Native Provider Live Acceptance → Run workflow**.
3. Select the provider kind and enter the exact model ID enabled for that
   provider account.
4. Run one provider at a time and retain the workflow URL and conclusion as
   acceptance evidence.

The workflow builds OmniRouter with a temporary SQLite database, starts the
gateway on loopback, verifies the selected model is present, and performs a
short non-streaming and streaming request. It does not print API keys or
response bodies.

## Local execution

For OpenAI Responses, Anthropic, or Gemini, configure the corresponding native
provider using `OMNI_PROVIDER_KIND`, `OMNI_PROVIDER_BASE_URL`,
`OMNI_PROVIDER_API_KEY`, `OMNI_PROVIDER_MODELS`,
`OMNI_CREDENTIAL_ENCRYPTION_KEY`, and `OMNI_GATEWAY_API_KEY`, start the
gateway, then run:

```sh
OMNI_PROVIDER_KIND=anthropic \
OMNI_SMOKE_MODEL=your-model-id \
OMNI_BASE_URL=http://127.0.0.1:8080 \
OMNI_GATEWAY_API_KEY=your-gateway-key \
bash scripts/native-provider-smoke.sh
```

For DeepSeek use `bash scripts/deepseek-smoke.sh`.

## What a pass does not prove

A basic text smoke does not certify tool calling, structured outputs, vision,
audio/video, long-running streams, cancellation under load, rate-limit
recovery, quota behavior, account billing, failover under live outage, or every
model variant. Those remain separate capability-specific acceptance cases.
A successful API call validates only the selected account, model, region,
adapter version, and time of the run.
