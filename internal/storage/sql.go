package storage

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type SQLStore struct{ db *sql.DB }

func NewSQL(db *sql.DB) (*SQLStore, error) {
	if db == nil {
		return nil, fmt.Errorf("database connection is required")
	}
	return &SQLStore{db: db}, nil
}

func (s *SQLStore) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }
func (s *SQLStore) Close() error                   { return s.db.Close() }
func (s *SQLStore) DB() *sql.DB                    { return s.db }

// Migrate applies numbered SQL files in numeric version order. Each migration and its
// version marker commit atomically. The caller owns the migration filesystem.
func (s *SQLStore) Migrate(ctx context.Context, migrations fs.FS, directory string) error {
	entries, err := fs.ReadDir(migrations, directory)
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return fmt.Errorf("initialize migration table: %w", err)
	}
	// A dedicated singleton row provides an unambiguous write target for
	// serializing migration transactions across independent SQLite handles.
	// It is infrastructure state, separate from the numbered migration ledger.
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migration_lock (id INTEGER PRIMARY KEY CHECK (id = 1), generation INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("initialize migration lock table: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migration_lock(id, generation) VALUES(1, 0)`); err != nil {
		return fmt.Errorf("initialize migration lock row: %w", err)
	}
	type migrationFile struct {
		name    string
		version int
	}
	files := make([]migrationFile, 0, len(entries))
	seen := make(map[int]string)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		base := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		prefix := strings.SplitN(base, "_", 2)[0]
		version, err := strconv.Atoi(prefix)
		if err != nil || version <= 0 {
			return fmt.Errorf("invalid migration filename %q: expected positive numeric prefix", entry.Name())
		}
		if previous, ok := seen[version]; ok {
			return fmt.Errorf("duplicate migration version %d in %q and %q", version, previous, entry.Name())
		}
		seen[version] = entry.Name()
		files = append(files, migrationFile{name: entry.Name(), version: version})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].version < files[j].version })
	for _, entry := range files {
		version := entry.version
		var exists int
		err = s.db.QueryRowContext(ctx, `SELECT 1 FROM schema_migrations WHERE version = ?`, version).Scan(&exists)
		if err == nil {
			continue
		}
		if err != sql.ErrNoRows {
			return fmt.Errorf("check migration %d: %w", version, err)
		}
		script, err := fs.ReadFile(migrations, filepath.ToSlash(filepath.Join(directory, entry.name)))
		if err != nil {
			return fmt.Errorf("read migration %s: %w", entry.name, err)
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", entry.name, err)
		}
		// Acquire SQLite's writer reservation by changing a guaranteed-existing
		// singleton row before checking the version. A second process waits
		// under busy_timeout, then observes the first process's committed
		// version marker instead of racing to execute the same DDL.
		if _, err = tx.ExecContext(ctx, `UPDATE schema_migration_lock SET generation = generation + 1 WHERE id = 1`); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("lock migration sequence before %s: %w", entry.name, err)
		}
		var applied int
		err = tx.QueryRowContext(ctx, `SELECT 1 FROM schema_migrations WHERE version = ?`, version).Scan(&applied)
		if err == nil {
			if rollbackErr := tx.Rollback(); rollbackErr != nil {
				return fmt.Errorf("finish skipped migration %s: %w", entry.name, rollbackErr)
			}
			continue
		}
		if err != sql.ErrNoRows {
			_ = tx.Rollback()
			return fmt.Errorf("recheck migration %d under write lock: %w", version, err)
		}
		if _, err = tx.ExecContext(ctx, string(script)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", entry.name, err)
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES(?, ?)`, version, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %s: %w", entry.name, err)
		}
		if err = tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", entry.name, err)
		}
	}
	return nil
}
