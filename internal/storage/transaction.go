package storage

import (
	"context"
	"database/sql"
	"fmt"
)

// DBTX is the common query surface implemented by *sql.DB and *sql.Tx.
// Repositories can use the same implementation inside or outside a transaction.
type DBTX interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// WithTx executes fn in a single database transaction. A callback error or
// commit error leaves the transaction rolled back; callers must not retain tx.
func (s *SQLStore) WithTx(ctx context.Context, opts *sql.TxOptions, fn func(*sql.Tx) error) (err error) {
	if fn == nil {
		return fmt.Errorf("transaction callback is required")
	}
	tx, err := s.db.BeginTx(ctx, opts)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	// Rollback is safe after Commit (it returns sql.ErrTxDone). Deferring it
	// unconditionally also releases the transaction if fn panics before err
	// can be assigned.
	defer func() { _ = tx.Rollback() }()
	if err = fn(tx); err != nil {
		return fmt.Errorf("transaction callback: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// InTransaction reuses an existing *sql.Tx or starts a transaction when db is
// backed by *sql.DB. This lets repositories compose safely inside an outer
// SQLStore.WithTx callback without attempting a nested transaction.
func InTransaction(ctx context.Context, db DBTX, fn func(DBTX) error) (err error) {
	if db == nil {
		return fmt.Errorf("database is required")
	}
	if fn == nil {
		return fmt.Errorf("transaction callback is required")
	}
	if tx, ok := db.(*sql.Tx); ok {
		return fn(tx)
	}
	beginner, ok := db.(interface {
		BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
	})
	if !ok {
		return fmt.Errorf("database does not support transactions")
	}
	tx, err := beginner.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	// Always schedule rollback so callback panics cannot strand an open
	// transaction or monopolize a single-connection SQLite pool.
	defer func() { _ = tx.Rollback() }()
	if err = fn(tx); err != nil {
		return fmt.Errorf("transaction callback: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}
