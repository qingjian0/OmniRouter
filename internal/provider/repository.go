package provider

import (
	"context"
	"database/sql"
	"fmt"
	"omnirouter/internal/storage"
	"time"
)

type ProviderConfig struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Kind         string    `json:"kind"`
	BaseURL      string    `json:"base_url"`
	Enabled      bool      `json:"enabled"`
	Priority     int       `json:"priority"`
	Weight       uint32    `json:"weight"`
	AccountID    string    `json:"account_id,omitempty"`
	CredentialID string    `json:"credential_id,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}
type Repository struct{ db storage.DBTX }

func NewRepository(db storage.DBTX) (*Repository, error) {
	if db == nil {
		return nil, fmt.Errorf("provider repository database is required")
	}
	return &Repository{db: db}, nil
}
func (r *Repository) Upsert(ctx context.Context, p ProviderConfig) error {
	if p.ID == "" || p.Name == "" || p.Kind == "" || p.BaseURL == "" {
		return fmt.Errorf("provider id, name, kind and base_url are required")
	}
	now := time.Now().UTC()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	if p.Priority == 0 {
		p.Priority = 100
	}
	if p.Weight == 0 {
		p.Weight = 1
	}
	enabled := 0
	if p.Enabled {
		enabled = 1
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO providers(id,name,kind,base_url,enabled,priority,weight,account_id,credential_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,kind=excluded.kind,base_url=excluded.base_url,enabled=excluded.enabled,priority=excluded.priority,weight=excluded.weight,account_id=COALESCE(excluded.account_id,providers.account_id),credential_id=COALESCE(excluded.credential_id,providers.credential_id),updated_at=excluded.updated_at`, p.ID, p.Name, p.Kind, p.BaseURL, enabled, p.Priority, p.Weight, nullableID(p.AccountID), nullableID(p.CredentialID), p.CreatedAt.Format(time.RFC3339Nano), p.UpdatedAt.Format(time.RFC3339Nano))
	return err
}
func (r *Repository) List(ctx context.Context) ([]ProviderConfig, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,name,kind,base_url,enabled,priority,weight,COALESCE(account_id,''),COALESCE(credential_id,''),created_at,updated_at FROM providers ORDER BY priority DESC,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ProviderConfig, 0)
	for rows.Next() {
		var p ProviderConfig
		var enabled int
		var created, updated string
		if err := rows.Scan(&p.ID, &p.Name, &p.Kind, &p.BaseURL, &enabled, &p.Priority, &p.Weight, &p.AccountID, &p.CredentialID, &created, &updated); err != nil {
			return nil, err
		}
		p.Enabled = enabled != 0
		if p.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
			return nil, err
		}
		if p.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (r *Repository) SetPolicy(ctx context.Context, id string, enabled bool, priority int, weight uint32) error {
	if priority == 0 {
		priority = 100
	}
	if weight == 0 {
		weight = 1
	}
	flag := 0
	if enabled {
		flag = 1
	}
	res, err := r.db.ExecContext(ctx, `UPDATE providers SET enabled=?,priority=?,weight=?,updated_at=? WHERE id=?`, flag, priority, weight, time.Now().UTC().Format(time.RFC3339Nano), id)
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
	res, err := r.db.ExecContext(ctx, `DELETE FROM providers WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return sql.ErrNoRows
	}
	return err
}

func (r *Repository) Get(ctx context.Context, id string) (ProviderConfig, error) {
	var p ProviderConfig
	var enabled int
	var created, updated string
	err := r.db.QueryRowContext(ctx, `SELECT id,name,kind,base_url,enabled,priority,weight,COALESCE(account_id,''),COALESCE(credential_id,''),created_at,updated_at FROM providers WHERE id=?`, id).Scan(&p.ID, &p.Name, &p.Kind, &p.BaseURL, &enabled, &p.Priority, &p.Weight, &p.AccountID, &p.CredentialID, &created, &updated)
	if err != nil {
		return ProviderConfig{}, err
	}
	p.Enabled = enabled != 0
	if p.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return ProviderConfig{}, err
	}
	if p.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
		return ProviderConfig{}, err
	}
	return p, nil
}

func nullableID(value string) any {
	if value == "" {
		return nil
	}
	return value
}
