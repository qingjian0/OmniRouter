#!/usr/bin/env bash
set -euo pipefail

DB="${1:-${OMNI_DATABASE_PATH:-data/omnirouter.db}}"
DEST="${2:-${DB}.backup.$(date -u +%Y%m%dT%H%M%SZ)}"

for value in "${DB}" "${DEST}"; do
  if [[ "${value}" == *"'"* ]]; then
    echo "Database paths containing a single quote are not supported by this script." >&2
    exit 2
  fi
done
if ! command -v sha256sum >/dev/null 2>&1 && ! command -v shasum >/dev/null 2>&1; then
  echo "sha256sum or shasum is required." >&2
  exit 2
fi
if [[ ! -f "${DB}" ]]; then
  echo "Database file not found: ${DB}" >&2
  exit 2
fi
if [[ -e "${DEST}" || -e "${DEST}.sha256" ]]; then
  echo "Backup destination already exists: ${DEST}" >&2
  exit 2
fi
if ! command -v sqlite3 >/dev/null 2>&1; then
  echo "sqlite3 CLI is required (Linux: install sqlite3; Termux: pkg install sqlite)." >&2
  exit 2
fi

mkdir -p "$(dirname "${DEST}")"
tmpdir="$(mktemp -d "$(dirname "${DEST}")/.omnirouter-backup.XXXXXX")"
trap 'rm -rf "${tmpdir}"' EXIT
tmp="${tmpdir}/backup.db"

sqlite3 "${DB}" ".backup '${tmp}'"
integrity="$(sqlite3 "${tmp}" 'PRAGMA integrity_check;')"
if [[ "${integrity}" != "ok" ]]; then
  echo "SQLite integrity check failed; backup was not published." >&2
  exit 1
fi
chmod 600 "${tmp}"
mv "${tmp}" "${DEST}"

if command -v sha256sum >/dev/null 2>&1; then
  sha256sum "${DEST}" > "${DEST}.sha256"
elif command -v shasum >/dev/null 2>&1; then
  shasum -a 256 "${DEST}" > "${DEST}.sha256"
else
  echo "Backup created, but no SHA-256 utility was found." >&2
  exit 1
fi
chmod 600 "${DEST}" "${DEST}.sha256"
echo "Backup verified: ${DEST}"
