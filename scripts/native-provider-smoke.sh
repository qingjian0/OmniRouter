#!/usr/bin/env bash
set -euo pipefail

: "${OMNI_PROVIDER_KIND:?OMNI_PROVIDER_KIND is required}"
: "${OMNI_SMOKE_MODEL:?OMNI_SMOKE_MODEL is required}"
: "${OMNI_GATEWAY_API_KEY:?OMNI_GATEWAY_API_KEY is required}"
: "${OMNI_BASE_URL:=http://127.0.0.1:8765}"

tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT
headers=(-H "Authorization: Bearer ${OMNI_GATEWAY_API_KEY}" -H "Content-Type: application/json")
python3 - "${tmp}/models.json" "${OMNI_BASE_URL}" "${OMNI_GATEWAY_API_KEY}" <<'PY'
import json, os, sys, urllib.request
base, key = sys.argv[2], sys.argv[3]
req = urllib.request.Request(base + "/v1/models", headers={"Authorization": "Bearer " + key})
with urllib.request.urlopen(req, timeout=30) as response:
    data = json.load(response)
models = {item.get("id") for item in data.get("data", [])}
if sys.argv[1]:
    with open(sys.argv[1], "w", encoding="utf-8") as out:
        json.dump(data, out)
if os.environ["OMNI_SMOKE_MODEL"] not in models:
    raise SystemExit("configured model is absent from the gateway model catalogue")
PY

case "${OMNI_PROVIDER_KIND}" in
  openai-responses)
    echo "[1/2] OpenAI Responses API non-streaming"
    python3 - "${OMNI_SMOKE_MODEL}" >"${tmp}/request.json" <<'PY'
import json,sys
print(json.dumps({"model":sys.argv[1],"input":"Reply with the single word READY.","stream":False}))
PY
    curl -fsS --max-time 180 "${OMNI_BASE_URL}/v1/responses" "${headers[@]}" --data-binary @"${tmp}/request.json" >"${tmp}/response.json"
    python3 - "${tmp}/response.json" <<'PY'
import json,sys
obj=json.load(open(sys.argv[1],encoding="utf-8"))
if obj.get("status") != "completed":
    raise SystemExit("OpenAI Responses did not complete")
PY
    echo "[2/2] OpenAI Responses SSE terminal event"
    python3 - "${OMNI_SMOKE_MODEL}" >"${tmp}/request-stream.json" <<'PY'
import json,sys
print(json.dumps({"model":sys.argv[1],"input":"Count from one to three.","stream":True}))
PY
    curl -fsSN --max-time 180 "${OMNI_BASE_URL}/v1/responses" "${headers[@]}" --data-binary @"${tmp}/request-stream.json" >"${tmp}/stream.out"
    grep -Fq 'event: response.completed' "${tmp}/stream.out"
    ;;
  anthropic)
    echo "[1/2] Anthropic Messages non-streaming"
    python3 - "${OMNI_SMOKE_MODEL}" >"${tmp}/request.json" <<'PY'
import json,sys
print(json.dumps({"model":sys.argv[1],"max_tokens":64,"messages":[{"role":"user","content":"Reply with the single word READY."}],"stream":False}))
PY
    curl -fsS --max-time 180 "${OMNI_BASE_URL}/v1/messages" "${headers[@]}" -H "anthropic-version: 2023-06-01" --data-binary @"${tmp}/request.json" >"${tmp}/response.json"
    python3 - "${tmp}/response.json" <<'PY'
import json,sys
obj=json.load(open(sys.argv[1],encoding="utf-8"))
if obj.get("type") != "message" or not obj.get("content"):
    raise SystemExit("Anthropic Messages response is malformed")
PY
    echo "[2/2] Anthropic Messages SSE terminal event"
    python3 - "${OMNI_SMOKE_MODEL}" >"${tmp}/request-stream.json" <<'PY'
import json,sys
print(json.dumps({"model":sys.argv[1],"max_tokens":64,"messages":[{"role":"user","content":"Count from one to three."}],"stream":True}))
PY
    curl -fsSN --max-time 180 "${OMNI_BASE_URL}/v1/messages" "${headers[@]}" -H "anthropic-version: 2023-06-01" --data-binary @"${tmp}/request-stream.json" >"${tmp}/stream.out"
    grep -Fq 'event: message_stop' "${tmp}/stream.out"
    ;;
  gemini)
    echo "[1/2] Gemini generateContent non-streaming"
    python3 - >"${tmp}/request.json" <<'PY'
import json
print(json.dumps({"contents":[{"role":"user","parts":[{"text":"Reply with the single word READY."}]}],"generationConfig":{"maxOutputTokens":64}}))
PY
    curl -fsS --max-time 180 "${OMNI_BASE_URL}/v1beta/models/${OMNI_SMOKE_MODEL}:generateContent" "${headers[@]}" --data-binary @"${tmp}/request.json" >"${tmp}/response.json"
    python3 - "${tmp}/response.json" <<'PY'
import json,sys
obj=json.load(open(sys.argv[1],encoding="utf-8"))
if not obj.get("candidates"):
    raise SystemExit("Gemini generateContent returned no candidates")
PY
    echo "[2/2] Gemini streamGenerateContent SSE data"
    curl -fsSN --max-time 180 "${OMNI_BASE_URL}/v1beta/models/${OMNI_SMOKE_MODEL}:streamGenerateContent" "${headers[@]}" --data-binary @"${tmp}/request.json" >"${tmp}/stream.out"
    grep -Fq 'data:' "${tmp}/stream.out"
    grep -Fq 'candidates' "${tmp}/stream.out"
    ;;
  *)
    echo "Unsupported native provider kind: ${OMNI_PROVIDER_KIND}" >&2
    exit 2
    ;;
esac

echo "PASS: ${OMNI_PROVIDER_KIND} native API non-streaming and streaming acceptance for configured model."
echo "No API key or response body was printed."
