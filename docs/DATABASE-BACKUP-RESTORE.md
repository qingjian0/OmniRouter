# SQLite backup and restore

OmniRouter uses SQLite with WAL enabled by default. The credential-encryption
key is deployment-managed and is **not** stored in the database. A recoverable
backup therefore requires both a verified database backup and a separately
protected copy of the exact `OMNI_CREDENTIAL_ENCRYPTION_KEY`.

## Requirements

- Install the SQLite command-line tool (`sqlite3`).
- Keep backups outside the live database directory where practical.
- Never upload database backups or encryption keys to a public repository.

## Backup

The backup script uses SQLite's online backup API, so it creates a consistent
snapshot while the service is running:

```sh
bash scripts/backup-sqlite.sh data/omnirouter.db backups/omnirouter.db
```

The script runs `PRAGMA integrity_check`, writes the backup atomically, and
creates a sibling `.sha256` file. It refuses to overwrite an existing backup.
Protect the resulting database and checksum files as sensitive operational data.

## Restore

1. Stop OmniRouter cleanly.
2. Verify that the backup and its checksum file are available.
3. Preserve the current database and the exact credential-encryption key.
4. Restore the snapshot:

```sh
bash scripts/restore-sqlite.sh data/omnirouter.db backups/omnirouter.db
```

The script verifies the backup checksum when a sibling `.sha256` file exists,
checks SQLite integrity before and after restore, preserves a pre-restore
database snapshot, and removes stale WAL/SHM sidecars. It refuses to proceed if
the backup is corrupt.

## Recovery constraints

- Supply the exact same `OMNI_CREDENTIAL_ENCRYPTION_KEY` used when the
  credentials were encrypted. A valid database with a different key cannot
  decrypt the stored Provider credentials.
- Restore only while the OmniRouter process is stopped; otherwise an active
  connection may continue writing to the old database inode.
- Preserve the previous database snapshot until the restored instance has
  passed health, readiness, Provider recovery, and a controlled model request.
- This procedure does not migrate between incompatible future schema versions.
  Review release notes before downgrading; downgrade migrations are not implied.
