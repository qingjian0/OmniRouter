# Adaptive scheduling

OmniRouter's adaptive scheduler keeps the existing health/circuit-breaker gate,
then ranks only enabled, healthy providers that declare every required
capability. It does not bypass capability declarations or health checks.

## Request objectives

Clients may send these optional gateway-only headers:

| Header | Accepted values / meaning |
|---|---|
| `X-OmniRouter-Objective` | `balanced`, `economy`, `speed`, `reasoning`, `vision`, `tools` |
| `X-OmniRouter-Input-Tokens` | Estimated input tokens, 1–2,000,000 |
| `X-OmniRouter-Output-Tokens` | Estimated output tokens, 1–1,000,000 |

These headers are consumed by OmniRouter and are not forwarded to upstream
providers. Invalid objective values fall back to structural capability
classification. Invalid or out-of-range token estimates are ignored.

When explicit token estimates are absent, input tokens are approximated from
request-body size (bytes / 4). Output tokens are inferred from declared
`max_tokens`, `max_completion_tokens`, `max_output_tokens`, or
`maxOutputTokens`; the fallback is 512. These are routing estimates, not
tokenizer measurements or billing records.

Example:

```http
POST /v1/chat/completions
Authorization: Bearer <gateway-key>
Content-Type: application/json
X-OmniRouter-Objective: economy
X-OmniRouter-Input-Tokens: 1200
X-OmniRouter-Output-Tokens: 300
```

The request body remains a normal OpenAI-compatible request.

## Model pricing

Prices are configured per model and provider, in USD per one million tokens.
The Vue admin Models page exposes input and output price fields. The
authenticated API is:

```http
PATCH /admin/models/{model-id}/pricing
Authorization: Bearer <gateway-key>
Content-Type: application/json

{
  "provider_id": "deepseek-primary",
  "input_cost_per_million_usd": 0.27,
  "output_cost_per_million_usd": 1.10
}
```

Both prices must be finite, non-negative numbers. A value of `0` is an
explicit zero price. A missing database value means unknown pricing and is
never treated as free. Prices persist in SQLite and survive model catalogue
refreshes and process restarts.

## Scoring and feedback

The scheduler combines objective-specific weights for estimated cost, observed
latency, reliability, operator priority, and configured weight. Unknown prices
are penalized when comparable explicit prices exist; if every candidate has
unknown prices, the cost dimension is neutral.

Latency uses an exponential moving average. Reliability uses a 95% prior with
four virtual observations, then incorporates reported successes and failures.
Both streaming and non-streaming outcomes feed these statistics. Circuit-breaker
cooldown remains owned by the existing scheduler failure path.

## Limitations

- Task classification is structural and header-driven, not a semantic model
  classifier.
- Token estimates are approximate and can differ materially from actual
  tokenization and provider billing.
- Prices are operator-maintained metadata, not discovered or verified by the
  gateway.
- Deterministic CI does not replace live multi-provider acceptance with
  operator-owned credentials and representative traffic.
