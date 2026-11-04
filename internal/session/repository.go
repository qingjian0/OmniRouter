package session

import (
	"context"
	"database/sql"
	"fmt"
	"omnirouter/internal/storage"
	"time"
)

type Repository struct{ db storage.DBTX }

func NewRepository(db storage.DBTX) (*Repository, error) {
	if db == nil {
		return nil, fmt.Errorf("session repository database is required")
	}
	return &Repository{db: db}, nil
}

func (r *Repository) Create(ctx context.Context, x Session) error {
	if x.ID == "" || x.AccountID == "" {
		return fmt.Errorf("session id and account_id are required")
	}
	if x.Status == "" {
		x.Status = StatusUnknown
	}
	if !validStatus(x.Status) {
		return fmt.Errorf("invalid session status %q", x.Status)
	}
	now := time.Now().UTC()
	if x.CreatedAt.IsZero() {
		x.CreatedAt = now
	}
	if x.UpdatedAt.IsZero() {
		x.UpdatedAt = now
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO sessions(id,account_id,status,expires_at,last_checked_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, x.ID, x.AccountID, x.Status, formatTime(x.ExpiresAt), formatTime(x.LastCheckedAt), x.CreatedAt.UTC().Format(time.RFC3339Nano), x.UpdatedAt.UTC().Format(time.RFC3339Nano))
	return err
}

func (r *Repository) Save(ctx context.Context, x Session) error {
	if x.ID == "" || x.AccountID == "" {
		return fmt.Errorf("session id and account_id are required")
	}
	now := time.Now().UTC()
	if x.Status == "" {
		x.Status = StatusUnknown
	}
	if x.CreatedAt.IsZero() {
		x.CreatedAt = now
	}
	x.UpdatedAt = now
	_, err := r.db.ExecContext(ctx, `INSERT INTO sessions(id,account_id,status,expires_at,last_checked_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET account_id=excluded.account_id,status=excluded.status,expires_at=excluded.expires_at,last_checked_at=excluded.last_checked_at,updated_at=excluded.updated_at`, x.ID, x.AccountID, x.Status, formatTime(x.ExpiresAt), formatTime(x.LastCheckedAt), x.CreatedAt.Format(time.RFC3339Nano), x.UpdatedAt.Format(time.RFC3339Nano))
	return err
}
func (r *Repository) Get(ctx context.Context, id string) (Session, error) {
	var x Session
	var expires, checked, created, updated sql.NullString
	err := r.db.QueryRowContext(ctx, `SELECT id,account_id,status,expires_at,last_checked_at,created_at,updated_at FROM sessions WHERE id=?`, id).Scan(&x.ID, &x.AccountID, &x.Status, &expires, &checked, &created, &updated)
	if err != nil {
		return Session{}, err
	}
	if x.ExpiresAt, err = parseNullable(expires); err != nil {
		return Session{}, err
	}
	if x.LastCheckedAt, err = parseNullable(checked); err != nil {
		return Session{}, err
	}
	if x.CreatedAt, err = time.Parse(time.RFC3339Nano, created.String); err != nil {
		return Session{}, err
	}
	if x.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated.String); err != nil {
		return Session{}, err
	}
	return x, nil
}
func (r *Repository) Revoke(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `UPDATE sessions SET status=?,updated_at=? WHERE id=?`, StatusRevoked, time.Now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return sql.ErrNoRows
	}
	return err
}
func formatTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}
func parseNullable(v sql.NullString) (*time.Time, error) {
	if !v.Valid {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339Nano, v.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (r *Repository) List(ctx context.Context, accountID string) ([]Session, error) {
	query := `SELECT id,account_id,status,expires_at,last_checked_at,created_at,updated_at FROM sessions`
	args := []any{}
	if accountID != "" {
		query += ` WHERE account_id=?`
		args = append(args, accountID)
	}
	query += ` ORDER BY created_at DESC,id`
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Session, 0)
	expiredIDs := make([]string, 0)
	now := time.Now()
	for rows.Next() {
		var x Session
		var expires, checked, created, updated sql.NullString
		if err := rows.Scan(&x.ID, &x.AccountID, &x.Status, &expires, &checked, &created, &updated); err != nil {
			return nil, err
		}
		if x.ExpiresAt, err = parseNullable(expires); err != nil {
			return nil, err
		}
		if x.LastCheckedAt, err = parseNullable(checked); err != nil {
			return nil, err
		}
		if x.CreatedAt, err = time.Parse(time.RFC3339Nano, created.String); err != nil {
			return nil, err
		}
		if x.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated.String); err != nil {
			return nil, err
		}
		if x.Status == StatusActive && x.ExpiresAt != nil && !x.ExpiresAt.After(now) {
			x.Status = StatusExpired
			expiredIDs = append(expiredIDs, x.ID)
		}
		items = append(items, x)
	}
	rowsErr := rows.Err()
	if err := rows.Close(); err != nil && rowsErr == nil {
		rowsErr = err
	}
	if rowsErr != nil {
		return nil, rowsErr
	}
	for _, id := range expiredIDs {
		if err := r.updateStatus(ctx, id, StatusExpired); err != nil {
			return nil, err
		}
	}
	return items, nil
}

func (r *Repository) Transition(ctx context.Context, id string, next Status) (Session, error) {
	if !validStatus(next) {
		return Session{}, fmt.Errorf("invalid session status %q", next)
	}
	returnValue := Session{}
	err := storage.InTransaction(ctx, r.db, func(tx storage.DBTX) error {
		repo, _ := NewRepository(tx)
		current, err := repo.Get(ctx, id)
		if err != nil {
			return err
		}
		if current.Status == StatusActive && current.ExpiresAt != nil && !current.ExpiresAt.After(time.Now()) {
			current.Status = StatusExpired
			if err := repo.updateStatus(ctx, id, current.Status); err != nil {
				return err
			}
		}
		if !allowed(current.Status, next) {
			return fmt.Errorf("invalid session transition: %s -> %s", current.Status, next)
		}
		if err := repo.updateStatus(ctx, id, next); err != nil {
			return err
		}
		current.Status = next
		current.UpdatedAt = time.Now().UTC()
		returnValue = current
		return nil
	})
	return returnValue, err
}

func (r *Repository) updateStatus(ctx context.Context, id string, status Status) error {
	res, err := r.db.ExecContext(ctx, `UPDATE sessions SET status=?,updated_at=? WHERE id=?`, status, time.Now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return sql.ErrNoRows
	}
	return err
}

func (r *Repository) Delete(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM sessions WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return sql.ErrNoRows
	}
	return err
}
