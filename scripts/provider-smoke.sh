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

make_body() {
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

echo "[1/2] OpenAI-compatible non-streaming request"
make_body false > "${tmp}/nonstream.json"
curl -fsS --max-time "${TIMEOUT}" "${BASE_URL%/}/v1/chat/completions" \
  "${headers[@]}" --data-binary @"${tmp}/nonstream.json" -o "${tmp}/nonstream.out"
python3 - "${tmp}/nonstream.out" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as f:
    payload = json.load(f)
if not isinstance(payload.get("choices"), list) or not payload["choices"]:
    raise SystemExit("response is valid JSON but contains no choices")
print("PASS: valid completion response")
PY

echo "[2/2] OpenAI-compatible SSE streaming request"
make_body true > "${tmp}/stream.json"
curl -fsS -N --max-time "${TIMEOUT}" "${BASE_URL%/}/v1/chat/completions" \
  "${headers[@]}" --data-binary @"${tmp}/stream.json" -o "${tmp}/stream.out"
if ! grep -Fq 'data: [DONE]' "${tmp}/stream.out"; then
  echo "FAIL: stream did not contain the explicit data: [DONE] terminal marker" >&2
  exit 1
fi
if ! grep -Eq '^data: ' "${tmp}/stream.out"; then
  echo "FAIL: stream contained no SSE data events" >&2
  exit 1
fi
echo "PASS: SSE events and explicit terminal marker received"
echo "Provider smoke test passed for model: ${MODEL}"
