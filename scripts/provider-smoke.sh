#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${OMNI_BASE_URL:-http://127.0.0.1:8080}"
MODEL="${OMNI_SMOKE_MODEL:-}"
GATEWAY_KEY="${OMNI_GATEWAY_API_KEY:-}"
TIMEOUT="${OMNI_SMOKE_TIMEOUT:-120}"

if [[ -z "${MODEL}" ]]; then
  echo "OMNI_SMOKE_MODEL is required (use a model ID returned by /v1/models)." >&2
  exit 2
fi
if ! command -v curl >/dev/null 2>&1 || ! command -v python3 >/dev/null 2>&1; then
  echo "curl and python3 are required." >&2
  exit 2
fi

tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT
headers=(-H "Content-Type: application/json")
if [[ -n "${GATEWAY_KEY}" ]]; then
  headers+=(-H "Authorization: Bearer ${GATEWAY_KEY}")
fi
MODEL_PATH="$(python3 -c 'import sys, urllib.parse; print(urllib.parse.quote(sys.argv[1], safe=""))' "${MODEL}")"

make_openai_body() {
  local stream="$1"
  python3 - "${MODEL}" "${stream}" <<'PY'
import json
import sys
print(json.dumps({
    "model": sys.argv[1],
    "messages": [{"role": "user", "content": "Reply with a short V1 smoke-test confirmation."}],
    "stream": sys.argv[2] == "true",
    "max_tokens": 64,
}))
PY
}

make_anthropic_body() {
  local stream="$1"
  python3 - "${MODEL}" "${stream}" <<'PY'
import json
import sys
print(json.dumps({
    "model": sys.argv[1],
    "max_tokens": 64,
    "messages": [{"role": "user", "content": "Reply with a short V1 smoke-test confirmation."}],
    "stream": sys.argv[2] == "true",
}))
PY
}

make_gemini_body() {
  python3 <<'PY'
import json
print(json.dumps({
    "contents": [{"role": "user", "parts": [{"text": "Reply with a short V1 smoke-test confirmation."}]}],
    "generationConfig": {"maxOutputTokens": 64},
}))
PY
}

check_json_list() {
  local file="$1"
  local field="$2"
  python3 - "${file}" "${field}" <<'PY'
import json
import sys
with open(sys.argv[1], encoding="utf-8") as f:
    payload = json.load(f)
value = payload.get(sys.argv[2])
if not isinstance(value, list) or not value:
    raise SystemExit(f"response is valid JSON but contains no non-empty {sys.argv[2]} array")
PY
}

check_sse() {
  local file="$1"
  local label="$2"
  if ! grep -Eq '^data: ' "${file}"; then
    echo "FAIL: ${label} response contained no SSE data events" >&2
    exit 1
  fi
}

echo "[1/6] OpenAI-compatible non-streaming request"
make_openai_body false > "${tmp}/openai.json"
curl -fsS --max-time "${TIMEOUT}" "${BASE_URL%/}/v1/chat/completions" \
  "${headers[@]}" --data-binary @"${tmp}/openai.json" -o "${tmp}/openai.out"
check_json_list "${tmp}/openai.out" "choices"

echo "[2/6] OpenAI-compatible SSE streaming request"
make_openai_body true > "${tmp}/openai-stream.json"
curl -fsS -N --max-time "${TIMEOUT}" "${BASE_URL%/}/v1/chat/completions" \
  "${headers[@]}" --data-binary @"${tmp}/openai-stream.json" -o "${tmp}/openai-stream.out"
check_sse "${tmp}/openai-stream.out" "OpenAI"
grep -Fq 'data: [DONE]' "${tmp}/openai-stream.out"

echo "[3/6] Anthropic Messages non-streaming translation"
make_anthropic_body false > "${tmp}/anthropic.json"
curl -fsS --max-time "${TIMEOUT}" "${BASE_URL%/}/v1/messages" \
  "${headers[@]}" -H "anthropic-version: 2023-06-01" \
  --data-binary @"${tmp}/anthropic.json" -o "${tmp}/anthropic.out"
check_json_list "${tmp}/anthropic.out" "content"

echo "[4/6] Anthropic Messages SSE translation"
make_anthropic_body true > "${tmp}/anthropic-stream.json"
curl -fsS -N --max-time "${TIMEOUT}" "${BASE_URL%/}/v1/messages" \
  "${headers[@]}" -H "anthropic-version: 2023-06-01" \
  --data-binary @"${tmp}/anthropic-stream.json" -o "${tmp}/anthropic-stream.out"
check_sse "${tmp}/anthropic-stream.out" "Anthropic"

echo "[5/6] Gemini generateContent translation"
make_gemini_body > "${tmp}/gemini.json"
curl -fsS --max-time "${TIMEOUT}" "${BASE_URL%/}/v1beta/models/${MODEL_PATH}:generateContent" \
  "${headers[@]}" --data-binary @"${tmp}/gemini.json" -o "${tmp}/gemini.out"
check_json_list "${tmp}/gemini.out" "candidates"

echo "[6/6] Gemini streamGenerateContent SSE translation"
curl -fsS -N --max-time "${TIMEOUT}" "${BASE_URL%/}/v1beta/models/${MODEL_PATH}:streamGenerateContent?alt=sse" \
  "${headers[@]}" --data-binary @"${tmp}/gemini.json" -o "${tmp}/gemini-stream.out"
check_sse "${tmp}/gemini-stream.out" "Gemini"

echo "PASS: OpenAI, Anthropic Messages, and Gemini text routes (non-streaming and SSE) for model ${MODEL}"
