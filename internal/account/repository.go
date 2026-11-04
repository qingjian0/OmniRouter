package account

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"omnirouter/internal/storage"
	"time"
)

var ErrHasDependencies = errors.New("account has dependent sessions or credentials")

type Repository struct{ db storage.DBTX }

func NewRepository(db storage.DBTX) (*Repository, error) {
	if db == nil {
		return nil, fmt.Errorf("account repository database is required")
	}
	return &Repository{db: db}, nil
}

func (r *Repository) Create(ctx context.Context, item Account) error {
	if item.ID == "" || item.ProviderID == "" || item.Name == "" {
		return fmt.Errorf("account id, provider_id and name are required")
	}
	now := time.Now().UTC()
	if item.Status == "" {
		item.Status = StatusActive
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = now
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO accounts(id,provider_id,name,status,created_at,updated_at) VALUES(?,?,?,?,?,?)`, item.ID, item.ProviderID, item.Name, item.Status, item.CreatedAt.Format(time.RFC3339Nano), item.UpdatedAt.Format(time.RFC3339Nano))
	return err
}

func (r *Repository) Get(ctx context.Context, id string) (Account, error) {
	var item Account
	var created, updated string
	err := r.db.QueryRowContext(ctx, `SELECT id,provider_id,name,status,created_at,updated_at FROM accounts WHERE id=?`, id).Scan(&item.ID, &item.ProviderID, &item.Name, &item.Status, &created, &updated)
	if err != nil {
		return Account{}, err
	}
	item.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return Account{}, err
	}
	item.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	if err != nil {
		return Account{}, err
	}
	return item, nil
}

func (r *Repository) ListByProvider(ctx context.Context, providerID string) ([]Account, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,provider_id,name,status,created_at,updated_at FROM accounts WHERE provider_id=? ORDER BY created_at DESC`, providerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Account, 0)
	for rows.Next() {
		var x Account
		var c, u string
		if err := rows.Scan(&x.ID, &x.ProviderID, &x.Name, &x.Status, &c, &u); err != nil {
			return nil, err
		}
		if x.CreatedAt, err = time.Parse(time.RFC3339Nano, c); err != nil {
			return nil, err
		}
		if x.UpdatedAt, err = time.Parse(time.RFC3339Nano, u); err != nil {
			return nil, err
		}
		items = append(items, x)
	}
	return items, rows.Err()
}

func (r *Repository) SetStatus(ctx context.Context, id string, status Status) error {
	if status != StatusActive && status != StatusDisabled && status != StatusError {
		return fmt.Errorf("invalid account status %q", status)
	}
	result, err := r.db.ExecContext(ctx, `UPDATE accounts SET status=?,updated_at=? WHERE id=?`, status, time.Now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return sql.ErrNoRows
	}
	return err
}

func (r *Repository) List(ctx context.Context, providerID string) ([]Account, error) {
	query := `SELECT id,provider_id,name,status,created_at,updated_at FROM accounts`
	args := []any{}
	if providerID != "" {
		query += ` WHERE provider_id=?`
		args = append(args, providerID)
	}
	query += ` ORDER BY created_at DESC,id`
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Account, 0)
	for rows.Next() {
		var x Account
		var created, updated string
		if err := rows.Scan(&x.ID, &x.ProviderID, &x.Name, &x.Status, &created, &updated); err != nil {
			return nil, err
		}
		if x.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
			return nil, err
		}
		if x.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
			return nil, err
		}
		items = append(items, x)
	}
	return items, rows.Err()
}

func (r *Repository) Delete(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM accounts WHERE id=? AND NOT EXISTS (SELECT 1 FROM sessions WHERE account_id=?) AND NOT EXISTS (SELECT 1 FROM credentials WHERE account_id=?)`, id, id, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil || n > 0 {
		return err
	}
	var exists int
	if err := r.db.QueryRowContext(ctx, `SELECT 1 FROM accounts WHERE id=?`, id).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return sql.ErrNoRows
	} else if err != nil {
		return err
	}
	return ErrHasDependencies
}

func (r *Repository) CountByProvider(ctx context.Context, providerID string) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM accounts WHERE provider_id=?`, providerID).Scan(&n)
	return n, err
}

func (r *Repository) CountCredentials(ctx context.Context, accountID string) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM credentials WHERE account_id=?`, accountID).Scan(&n)
	return n, err
}
