package storage

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Runtime owns a configured SQL database and its migration state.
// The caller must provide a database/sql handle opened by the selected driver.
type Runtime struct {
	store *SQLStore
	once  sync.Once
	err   error
}

// NewSQLiteRuntime configures SQLite, applies embedded migrations, and verifies
// connectivity. It closes db if initialization fails; on success Runtime owns it.
func NewSQLiteRuntime(ctx context.Context, db *sql.DB, opts SQLiteOptions) (*Runtime, error) {
	if db == nil {
		return nil, fmt.Errorf("database connection is required")
	}
	fail := func(err error) (*Runtime, error) {
		_ = db.Close()
		return nil, err
	}
	if err := ConfigureSQLite(ctx, db, opts); err != nil {
		return fail(err)
	}
	store, err := NewSQL(db)
	if err != nil {
		return fail(err)
	}
	if err := store.MigrateEmbedded(ctx); err != nil {
		return fail(fmt.Errorf("apply embedded database migrations: %w", err))
	}
	if err := store.Ping(ctx); err != nil {
		return fail(fmt.Errorf("verify database connection: %w", err))
	}
	return &Runtime{store: store}, nil
}

func (r *Runtime) Store() *SQLStore {
	if r == nil {
		return nil
	}
	return r.store
}

func (r *Runtime) Close() error {
	if r == nil || r.store == nil {
		return nil
	}
	r.once.Do(func() { r.err = r.store.Close() })
	return r.err
}

// OpenSQLiteRuntime creates the database directory, opens the registered pure-Go
// SQLite driver, configures it, applies embedded migrations, and transfers
// connection ownership to Runtime. File URIs are accepted as driver DSNs; for
// ordinary paths the parent directory is created automatically.
func OpenSQLiteRuntime(ctx context.Context, path string, opts SQLiteOptions) (*Runtime, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("SQLite database path is required")
	}
	if path != ":memory:" && !strings.HasPrefix(path, "file:") {
		dir := filepath.Dir(path)
		if dir != "." && dir != "" {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return nil, fmt.Errorf("create SQLite database directory: %w", err)
			}
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open SQLite database: %w", err)
	}
	return NewSQLiteRuntime(ctx, db, opts)
}
