package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// SQLiteOptions controls connection-level SQLite behavior. The initial
// database/sql integration intentionally pins one connection so PRAGMA
// settings cannot silently differ between pooled connections. A future driver
// integration may replace this with connector-level initialization.
type SQLiteOptions struct {
	BusyTimeout time.Duration
	WAL         bool
}

// ConfigureSQLite configures and verifies the SQLite connection used by db.
// It does not open a driver: the caller must register and open a SQLite driver.
func ConfigureSQLite(ctx context.Context, db *sql.DB, opts SQLiteOptions) error {
	if db == nil {
		return fmt.Errorf("database connection is required")
	}
	if opts.BusyTimeout == 0 {
		opts.BusyTimeout = 5 * time.Second
	}
	if opts.BusyTimeout < 0 {
		return fmt.Errorf("SQLite busy timeout must not be negative")
	}
	busyMS := opts.BusyTimeout.Milliseconds()
	if busyMS > int64(^uint32(0)>>1) {
		return fmt.Errorf("SQLite busy timeout is too large")
	}

	// PRAGMA values cannot be supplied as bind parameters. busyMS is an
	// internally computed non-negative integer, not user-provided SQL.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err := db.ExecContext(ctx, fmt.Sprintf("PRAGMA busy_timeout = %d", busyMS)); err != nil {
		return fmt.Errorf("set SQLite busy_timeout: %w", err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		return fmt.Errorf("enable SQLite foreign_keys: %w", err)
	}
	var foreignKeys int
	if err := db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		return fmt.Errorf("verify SQLite foreign_keys: %w", err)
	}
	if foreignKeys != 1 {
		return fmt.Errorf("SQLite foreign key enforcement is not enabled")
	}
	if opts.WAL {
		var mode string
		if err := db.QueryRowContext(ctx, "PRAGMA journal_mode = WAL").Scan(&mode); err != nil {
			return fmt.Errorf("enable SQLite WAL: %w", err)
		}
		if !strings.EqualFold(mode, "wal") {
			return fmt.Errorf("SQLite refused WAL mode; active journal mode is %q", mode)
		}
	}
	return nil
}
