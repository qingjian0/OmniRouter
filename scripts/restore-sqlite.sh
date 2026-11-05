#!/usr/bin/env bash
set -euo pipefail

DB="${1:-${OMNI_DATABASE_PATH:-data/omnirouter.db}}"
BACKUP="${2:-}"

if [[ -z "${BACKUP}" || ! -f "${BACKUP}" ]]; then
  echo "Usage: $0 <database-path> <backup-file>" >&2
  exit 2
fi
for value in "${DB}" "${BACKUP}"; do
  if [[ "${value}" == *"'"* ]]; then
    echo "Database paths containing a single quote are not supported by this script." >&2
    exit 2
  fi
done
if ! command -v sqlite3 >/dev/null 2>&1; then
  echo "sqlite3 CLI is required (Linux: install sqlite3; Termux: pkg install sqlite)." >&2
  exit 2
fi

if [[ -f "${BACKUP}.sha256" ]]; then
  if ! command -v sha256sum >/dev/null 2>&1 && ! command -v shasum >/dev/null 2>&1; then
    echo "sha256sum or shasum is required to verify the backup." >&2
    exit 2
  fi
  expected="$(awk '{print $1}' "${BACKUP}.sha256")"
  if command -v sha256sum >/dev/null 2>&1; then
    actual="$(sha256sum "${BACKUP}" | awk '{print $1}')"
  else
    actual="$(shasum -a 256 "${BACKUP}" | awk '{print $1}')"
  fi
  if [[ -z "${expected}" || "${actual}" != "${expected}" ]]; then
    echo "Backup SHA-256 verification failed." >&2
    exit 1
  fi
fi

integrity="$(sqlite3 "${BACKUP}" 'PRAGMA integrity_check;')"
if [[ "${integrity}" != "ok" ]]; then
  echo "Backup database failed SQLite integrity_check." >&2
  exit 1
fi

mkdir -p "$(dirname "${DB}")"
tmpdir="$(mktemp -d "$(dirname "${DB}")/.omnirouter-restore.XXXXXX")"
trap 'rm -rf "${tmpdir}"' EXIT
tmp="${tmpdir}/restored.db"
sqlite3 "${BACKUP}" ".backup '${tmp}'"
restored_integrity="$(sqlite3 "${tmp}" 'PRAGMA integrity_check;')"
if [[ "${restored_integrity}" != "ok" ]]; then
  echo "Restored temporary database failed integrity_check." >&2
  exit 1
fi

pre_restore=""
if [[ -f "${DB}" ]]; then
  pre_restore="${DB}.pre-restore.$(date -u +%Y%m%dT%H%M%SZ)"
  if [[ -e "${pre_restore}" ]]; then
    echo "Pre-restore backup already exists: ${pre_restore}" >&2
    exit 2
  fi
  sqlite3 "${DB}" ".backup '${pre_restore}'"
  chmod 600 "${pre_restore}"
fi

# The service must be stopped before replacing the database and its WAL sidecars.
rm -f "${DB}-wal" "${DB}-shm"
chmod 600 "${tmp}"
mv "${tmp}" "${DB}"
final_integrity="$(sqlite3 "${DB}" 'PRAGMA integrity_check;')"
if [[ "${final_integrity}" != "ok" ]]; then
  if [[ -n "${pre_restore}" ]]; then
    cp "${pre_restore}" "${DB}"
  fi
  echo "Restored database failed final integrity_check; previous database was retained where available." >&2
  exit 1
fi
echo "Restore verified: ${DB}"
if [[ -n "${pre_restore}" ]]; then
  echo "Previous database backup: ${pre_restore}"
fi
