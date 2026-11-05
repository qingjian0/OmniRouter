#!/usr/bin/env bash
set -euo pipefail

BIN="${1:-./omnirouter}"
ADDR="${OMNI_SMOKE_ADDR:-127.0.0.1:18765}"
BASE_URL="http://${ADDR}"
TIMEOUT="${OMNI_SMOKE_START_TIMEOUT:-30}"

if [[ ! -x "${BIN}" ]]; then
  echo "Executable not found or not executable: ${BIN}" >&2
  exit 2
fi
if ! command -v curl >/dev/null 2>&1; then
  echo "curl is required (Termux: pkg install curl)." >&2
  exit 2
fi

tmp="$(mktemp -d "${TMPDIR:-${HOME}}/omnirouter-smoke.XXXXXX")"
pid=""
cleanup() {
  if [[ -n "${pid}" ]] && kill -0 "${pid}" 2>/dev/null; then
    kill -TERM "${pid}" 2>/dev/null || true
    wait "${pid}" 2>/dev/null || true
  fi
  rm -rf "${tmp}"
}
trap cleanup EXIT

db="${tmp}/omnirouter.db"
log="${tmp}/omnirouter.log"

echo "[1/4] Start binary with a temporary writable SQLite database"
OMNI_ADDR="${ADDR}" OMNI_DATABASE_PATH="${db}" OMNI_DATABASE_WAL=true \
  "${BIN}" >"${log}" 2>&1 &
pid=$!

echo "[2/4] Wait for the local health endpoint"
ready=0
for ((i=0; i<TIMEOUT; i++)); do
  if curl -fsS --max-time 2 "${BASE_URL}/healthz" >/dev/null 2>&1; then
    ready=1
    break
  fi
  if ! kill -0 "${pid}" 2>/dev/null; then
    echo "OmniRouter exited during startup; log follows:" >&2
    cat "${log}" >&2
    exit 1
  fi
  sleep 1
done
if [[ "${ready}" -ne 1 ]]; then
  echo "Health endpoint did not become ready within ${TIMEOUT}s; log follows:" >&2
  cat "${log}" >&2
  exit 1
fi

echo "[3/4] Verify readiness endpoint and SQLite file creation"
curl -fsS --max-time 3 "${BASE_URL}/readyz" >/dev/null
if [[ ! -s "${db}" ]]; then
  echo "SQLite database was not created or is empty." >&2
  cat "${log}" >&2
  exit 1
fi

echo "[4/4] Verify graceful SIGTERM shutdown"
kill -TERM "${pid}"
if wait "${pid}"; then
  pid=""
else
  status=$?
  pid=""
  echo "OmniRouter did not exit cleanly (status ${status}); log follows:" >&2
  cat "${log}" >&2
  exit "${status}"
fi

echo "PASS: startup, loopback HTTP, SQLite write access, and graceful shutdown"
