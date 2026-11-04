// Package apikey manages hashed gateway credentials. Raw keys are returned
// exactly once at creation and are never persisted.
package apikey

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"omnirouter/internal/security"
	"omnirouter/internal/storage"
)

type Key struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Scopes     []string   `json:"scopes"`
	Enabled    bool       `json:"enabled"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

const (
	ScopeGatewayRead  = "gateway:read"
	ScopeGatewayWrite = "gateway:write"
	ScopeAdminRead    = "admin:read"
	ScopeAdminWrite   = "admin:write"
)

var allowedScopes = map[string]struct{}{
	ScopeGatewayRead: {}, ScopeGatewayWrite: {}, ScopeAdminRead: {}, ScopeAdminWrite: {},
}

func ValidateScopes(scopes []string) error {
	seen := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		if _, ok := allowedScopes[scope]; !ok {
			return fmt.Errorf("unsupported API key scope %q", scope)
		}
		if _, duplicate := seen[scope]; duplicate {
			return fmt.Errorf("duplicate API key scope %q", scope)
		}
		seen[scope] = struct{}{}
	}
	if len(seen) == 0 {
		return fmt.Errorf("at least one API key scope is required")
	}
	return nil
}

func HasScope(scopes []string, required string) bool {
	for _, scope := range scopes {
		if scope == required {
			return true
		}
	}
	return false
}

type Repository struct{ db storage.DBTX }

func NewRepository(db storage.DBTX) (*Repository, error) {
	if db == nil {
		return nil, fmt.Errorf("API key repository database is required")
	}
	return &Repository{db: db}, nil
}
func (r *Repository) Create(ctx context.Context, id, name string, scopes []string) (Key, string, error) {
	if id == "" || name == "" {
		return Key{}, "", fmt.Errorf("key id and name are required")
	}
	if err := ValidateScopes(scopes); err != nil {
		return Key{}, "", err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return Key{}, "", err
	}
	secret := "omni_" + base64.RawURLEncoding.EncodeToString(raw)
	encoded, err := json.Marshal(scopes)
	if err != nil {
		return Key{}, "", err
	}
	now := time.Now().UTC()
	_, err = r.db.ExecContext(ctx, `INSERT INTO api_keys(id,name,key_hash,scopes,enabled,created_at) VALUES(?,?,?,?,1,?)`, id, name, security.HashAPIKey(secret), string(encoded), now.Format(time.RFC3339Nano))
	if err != nil {
		return Key{}, "", err
	}
	return Key{ID: id, Name: name, Scopes: scopes, Enabled: true, CreatedAt: now}, secret, nil
}
func (r *Repository) Authenticate(ctx context.Context, secret string) (Key, error) {
	if secret == "" {
		return Key{}, sql.ErrNoRows
	}
	var k Key
	var scopes, created string
	var last sql.NullString
	err := r.db.QueryRowContext(ctx, `SELECT id,name,scopes,created_at,last_used_at FROM api_keys WHERE key_hash=? AND enabled=1`, security.HashAPIKey(secret)).Scan(&k.ID, &k.Name, &scopes, &created, &last)
	if err != nil {
		return Key{}, err
	}
	if err := json.Unmarshal([]byte(scopes), &k.Scopes); err != nil {
		return Key{}, err
	}
	k.Enabled = true
	if k.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return Key{}, err
	}
	if last.Valid {
		t, parseErr := time.Parse(time.RFC3339Nano, last.String)
		if parseErr != nil {
			return Key{}, parseErr
		}
		k.LastUsedAt = &t
	}
	nowTime := time.Now().UTC()
	now := nowTime.Format(time.RFC3339Nano)
	_, _ = r.db.ExecContext(ctx, `UPDATE api_keys SET last_used_at=? WHERE id=?`, now, k.ID)
	k.LastUsedAt = &nowTime
	return k, nil
}

func (r *Repository) List(ctx context.Context) ([]Key, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,name,scopes,enabled,created_at,last_used_at FROM api_keys ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Key, 0)
	for rows.Next() {
		var k Key
		var scopes, created string
		var enabled int
		var last sql.NullString
		if err := rows.Scan(&k.ID, &k.Name, &scopes, &enabled, &created, &last); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(scopes), &k.Scopes); err != nil {
			return nil, err
		}
		k.Enabled = enabled != 0
		if k.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
			return nil, err
		}
		if last.Valid {
			t, e := time.Parse(time.RFC3339Nano, last.String)
			if e != nil {
				return nil, e
			}
			k.LastUsedAt = &t
		}
		out = append(out, k)
	}
	return out, rows.Err()
}
func (r *Repository) SetEnabled(ctx context.Context, id string, enabled bool) error {
	v := 0
	if enabled {
		v = 1
	}
	res, err := r.db.ExecContext(ctx, `UPDATE api_keys SET enabled=? WHERE id=?`, v, id)
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
	res, err := r.db.ExecContext(ctx, `DELETE FROM api_keys WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return sql.ErrNoRows
	}
	return err
}
