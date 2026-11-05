#!/usr/bin/env bash
set -euo pipefail

: "${OMNI_BASE_URL:=http://127.0.0.1:8080}"
: "${OMNI_SMOKE_MODEL:=deepseek-flash}"
: "${OMNI_GATEWAY_API_KEY:?OMNI_GATEWAY_API_KEY is required}"
: "${OMNI_PROVIDER_API_KEY:?OMNI_PROVIDER_API_KEY is required}"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
auth=(-H "Authorization: Bearer ${OMNI_GATEWAY_API_KEY}" -H "Content-Type: application/json")

echo "[1/6] Check gateway and discovered model catalogue"
curl -fsS --max-time 10 "${OMNI_BASE_URL}/healthz" >/dev/null
curl -fsS --max-time 30 "${OMNI_BASE_URL}/v1/models" "${auth[@]}" >"$tmp/models.json"
python3 - "$tmp/models.json" "$OMNI_SMOKE_MODEL" <<'PY'
import json, sys
data = json.load(open(sys.argv[1], encoding="utf-8"))
models = {item.get("id") for item in data.get("data", [])}
if sys.argv[2] not in models:
    raise SystemExit(f"configured model {sys.argv[2]!r} missing from DeepSeek model catalogue: {sorted(models)}")
PY

echo "[2/6] Chat Completions non-streaming with thinking enabled"
python3 - "$OMNI_SMOKE_MODEL" >"$tmp/chat.json" <<'PY'
import json, sys
print(json.dumps({
  "model": sys.argv[1],
  "messages": [{"role":"user","content":"Reply with the single word READY."}],
  "thinking": {"type":"enabled"},
  "reasoning_effort": "low",
  "stream": False
}))
PY
curl -fsS --max-time 180 -X POST "${OMNI_BASE_URL}/v1/chat/completions" "${auth[@]}" --data-binary @"$tmp/chat.json" >"$tmp/chat-response.json"
python3 - "$tmp/chat-response.json" <<'PY'
import json, sys
obj=json.load(open(sys.argv[1],encoding="utf-8"))
if not obj.get("choices"):
    raise SystemExit("Chat Completions response has no choices")
PY

echo "[3/6] Native Responses API non-streaming"
python3 - "$OMNI_SMOKE_MODEL" >"$tmp/responses.json" <<'PY'
import json, sys
print(json.dumps({
  "model": sys.argv[1],
  "input": "Reply with the single word READY.",
  "reasoning": {"effort":"low"},
  "stream": False
}))
PY
curl -fsS --max-time 180 -X POST "${OMNI_BASE_URL}/v1/responses" "${auth[@]}" --data-binary @"$tmp/responses.json" >"$tmp/responses-output.json"
python3 - "$tmp/responses-output.json" <<'PY'
import json, sys
obj=json.load(open(sys.argv[1],encoding="utf-8"))
if obj.get("status") != "completed":
    raise SystemExit(f"Responses API status is not completed: {obj.get('status')!r}")
PY

echo "[4/9] Chat Completions forced function tool call"
python3 - "$OMNI_SMOKE_MODEL" >"$tmp/tool.json" <<'PY'
import json, sys
print(json.dumps({
  "model": sys.argv[1],
  "messages": [{"role":"user","content":"Call the lookup function with id 42."}],
  "thinking": {"type":"disabled"},
  "tools": [{"type":"function","function":{"name":"lookup","description":"Look up a record","parameters":{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}}}],
  "tool_choice": {"type":"function","function":{"name":"lookup"}}
}))
PY
curl -fsS --max-time 180 -X POST "${OMNI_BASE_URL}/v1/chat/completions" "${auth[@]}" --data-binary @"$tmp/tool.json" >"$tmp/tool-output.json"
python3 - "$tmp/tool-output.json" <<'PY'
import json, sys
obj=json.load(open(sys.argv[1],encoding="utf-8"))
calls=obj.get("choices",[{}])[0].get("message",{}).get("tool_calls",[])
if not calls or calls[0].get("function",{}).get("name") != "lookup":
    raise SystemExit("forced function tool call was not returned")
PY

echo "[5/9] Chat Completions SSE terminal marker"
python3 - "$OMNI_SMOKE_MODEL" >"$tmp/chat-stream.json" <<'PY'
import json, sys
print(json.dumps({
  "model": sys.argv[1],
  "messages": [{"role":"user","content":"Count from one to three."}],
  "stream": True,
  "stream_options": {"include_usage": True}
}))
PY
curl -fsSN --max-time 180 -X POST "${OMNI_BASE_URL}/v1/chat/completions" "${auth[@]}" --data-binary @"$tmp/chat-stream.json" >"$tmp/chat-stream.out"
grep -Fq "data: [DONE]" "$tmp/chat-stream.out"

echo "[6/9] Responses API semantic SSE completion event"
python3 - "$OMNI_SMOKE_MODEL" >"$tmp/responses-stream.json" <<'PY'
import json, sys
print(json.dumps({
  "model": sys.argv[1],
  "input": "Count from one to three.",
  "reasoning": {"effort":"low"},
  "stream": True
}))
PY
curl -fsSN --max-time 180 -X POST "${OMNI_BASE_URL}/v1/responses" "${auth[@]}" --data-binary @"$tmp/responses-stream.json" >"$tmp/responses-stream.out"
grep -Fq "event: response.completed" "$tmp/responses-stream.out"

echo "[7/9] Native Anthropic Messages non-streaming"
python3 - "$OMNI_SMOKE_MODEL" >"$tmp/anthropic.json" <<'PY'
import json, sys
print(json.dumps({
  "model": sys.argv[1],
  "max_tokens": 128,
  "messages": [{"role":"user","content":"Reply with the single word READY."}],
  "thinking": {"type":"enabled"},
  "stream": False
}))
PY
curl -fsS --max-time 180 -X POST "${OMNI_BASE_URL}/v1/messages" "${auth[@]}" --data-binary @"$tmp/anthropic.json" >"$tmp/anthropic-output.json"
python3 - "$tmp/anthropic-output.json" <<'PY'
import json, sys
obj=json.load(open(sys.argv[1],encoding="utf-8"))
if obj.get("type") != "message" or not obj.get("content"):
    raise SystemExit("native Anthropic Messages response is malformed")
PY

echo "[8/9] Native Anthropic Messages SSE message_stop"
python3 - "$OMNI_SMOKE_MODEL" >"$tmp/anthropic-stream.json" <<'PY'
import json, sys
print(json.dumps({
  "model": sys.argv[1],
  "max_tokens": 128,
  "messages": [{"role":"user","content":"Count from one to three."}],
  "stream": True
}))
PY
curl -fsSN --max-time 180 -X POST "${OMNI_BASE_URL}/v1/messages" "${auth[@]}" --data-binary @"$tmp/anthropic-stream.json" >"$tmp/anthropic-stream.out"
grep -Fq "event: message_stop" "$tmp/anthropic-stream.out"

echo "[9/9] Verify persisted usage endpoint is readable"
curl -fsS --max-time 20 "${OMNI_BASE_URL}/admin/usage" "${auth[@]}" >"$tmp/usage.json"
python3 - "$tmp/usage.json" <<'PY'
import json, sys
obj=json.load(open(sys.argv[1],encoding="utf-8"))
if "data" not in obj:
    raise SystemExit("usage endpoint returned no data field")
PY

echo "DeepSeek live smoke passed for model ${OMNI_SMOKE_MODEL}. No API key or prompt data was printed."
