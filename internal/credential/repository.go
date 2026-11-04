// Package credential stores encrypted provider credentials. Plaintext values
// are accepted only at the write boundary and are never returned by List.
package credential

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"omnirouter/internal/security"
	"omnirouter/internal/storage"
)

type Metadata struct {
	ID        string     `json:"id"`
	AccountID string     `json:"account_id"`
	Type      string     `json:"type"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

type Repository struct {
	db        storage.DBTX
	encryptor *security.Encryptor
}

func NewRepository(db storage.DBTX, e *security.Encryptor) (*Repository, error) {
	if db == nil || e == nil {
		return nil, fmt.Errorf("database and credential encryptor are required")
	}
	return &Repository{db: db, encryptor: e}, nil
}

func (r *Repository) Put(ctx context.Context, m Metadata, kind Kind, plaintext []byte) (VaultMetadata, error) {
	if m.ID == "" || m.AccountID == "" || !validKind(kind) || len(plaintext) == 0 {
		return VaultMetadata{}, fmt.Errorf("credential id, account_id, supported kind and value are required")
	}
	ad := associatedData(m.AccountID, m.ID, string(kind))
	ciphertext, err := r.encryptor.Encrypt(plaintext, ad)
	if err != nil {
		return VaultMetadata{}, err
	}
	defer zero(ciphertext)
	now := time.Now().UTC()
	if m.CreatedAt.IsZero() {
		m.CreatedAt = now
	}
	m.UpdatedAt = now
	status := StatusActive
	if m.ExpiresAt != nil && !m.ExpiresAt.After(now) {
		status = StatusExpired
	}
	// Identity is immutable because it is part of AES-GCM associated data.
	err = storage.InTransaction(ctx, r.db, func(tx storage.DBTX) error {
		var accountID, existingKind, existingStatus string
		err := tx.QueryRowContext(ctx, `SELECT account_id,credential_type,status FROM credentials WHERE id=?`, m.ID).Scan(&accountID, &existingKind, &existingStatus)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if err == nil {
			if accountID != m.AccountID || existingKind != string(kind) {
				return fmt.Errorf("credential identity is immutable; create a new id to change account or kind")
			}
			if existingStatus == string(StatusRevoked) {
				return fmt.Errorf("revoked credentials are terminal; create a new credential")
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO credentials(id,account_id,credential_type,encrypted_value,key_version,expires_at,status,created_at,updated_at) VALUES(?,?,?,?,1,?,?,?,?) ON CONFLICT(id) DO UPDATE SET encrypted_value=excluded.encrypted_value,key_version=credentials.key_version+1,expires_at=excluded.expires_at,status=excluded.status,updated_at=excluded.updated_at`, m.ID, m.AccountID, string(kind), ciphertext, nullableTime(m.ExpiresAt), string(status), m.CreatedAt.Format(time.RFC3339Nano), m.UpdatedAt.Format(time.RFC3339Nano))
		return err
	})
	if err != nil {
		return VaultMetadata{}, err
	}
	return r.metadata(ctx, m.ID)
}

func (r *Repository) Get(ctx context.Context, id string) (VaultMetadata, []byte, error) {
	var m Metadata
	var kind, status, created, updated string
	var encrypted []byte
	var expires sql.NullString
	var version uint64
	err := r.db.QueryRowContext(ctx, `SELECT id,account_id,credential_type,encrypted_value,key_version,expires_at,status,created_at,updated_at FROM credentials WHERE id=?`, id).Scan(&m.ID, &m.AccountID, &kind, &encrypted, &version, &expires, &status, &created, &updated)
	if err != nil {
		return VaultMetadata{}, nil, err
	}
	plain, err := r.encryptor.Decrypt(encrypted, associatedData(m.AccountID, m.ID, kind))
	if err != nil {
		return VaultMetadata{}, nil, err
	}
	if expires.Valid {
		t, parseErr := time.Parse(time.RFC3339Nano, expires.String)
		if parseErr != nil {
			zero(plain)
			return VaultMetadata{}, nil, parseErr
		}
		m.ExpiresAt = &t
	}
	if m.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		zero(plain)
		return VaultMetadata{}, nil, err
	}
	if m.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
		zero(plain)
		return VaultMetadata{}, nil, err
	}
	meta := VaultMetadata{Metadata: m, Kind: Kind(kind), Status: Status(status), Version: version}
	if meta.Status == StatusActive && m.ExpiresAt != nil && !m.ExpiresAt.After(time.Now()) {
		meta.Status = StatusExpired
	}
	if meta.Status != StatusActive {
		zero(plain)
		return meta, nil, fmt.Errorf("credential is %s", meta.Status)
	}
	return meta, plain, nil
}

func (r *Repository) List(ctx context.Context, accountID string) ([]VaultMetadata, error) {
	query := `SELECT id,account_id,credential_type,key_version,expires_at,status,created_at,updated_at FROM credentials`
	args := []any{}
	if accountID != "" {
		query += ` WHERE account_id=?`
		args = append(args, accountID)
	}
	query += ` ORDER BY created_at DESC`
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]VaultMetadata, 0)
	for rows.Next() {
		var m Metadata
		var kind, status, created, updated string
		var expires sql.NullString
		var version uint64
		if err := rows.Scan(&m.ID, &m.AccountID, &kind, &version, &expires, &status, &created, &updated); err != nil {
			return nil, err
		}
		if expires.Valid {
			t, e := time.Parse(time.RFC3339Nano, expires.String)
			if e != nil {
				return nil, e
			}
			m.ExpiresAt = &t
		}
		if m.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
			return nil, err
		}
		if m.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
			return nil, err
		}
		meta := VaultMetadata{Metadata: m, Kind: Kind(kind), Status: Status(status), Version: version}
		if meta.Status == StatusActive && m.ExpiresAt != nil && !m.ExpiresAt.After(time.Now()) {
			meta.Status = StatusExpired
		}
		items = append(items, meta)
	}
	return items, rows.Err()
}

func (r *Repository) SetStatus(ctx context.Context, id string, status Status) (VaultMetadata, bool, error) {
	if status != StatusActive && status != StatusDisabled && status != StatusRevoked && status != StatusExpired {
		return VaultMetadata{}, false, fmt.Errorf("invalid credential status %q", status)
	}
	current, err := r.metadata(ctx, id)
	if err == sql.ErrNoRows {
		return VaultMetadata{}, false, nil
	}
	if err != nil {
		return VaultMetadata{}, false, err
	}
	if current.Status == StatusRevoked && status != StatusRevoked {
		return VaultMetadata{}, true, fmt.Errorf("revoked credentials are terminal")
	}
	if current.ExpiresAt != nil && !current.ExpiresAt.After(time.Now()) && status == StatusActive {
		return VaultMetadata{}, true, fmt.Errorf("expired credentials cannot be reactivated; replace the secret")
	}
	res, err := r.db.ExecContext(ctx, `UPDATE credentials SET status=?,updated_at=? WHERE id=?`, string(status), time.Now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return VaultMetadata{}, false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return VaultMetadata{}, false, err
	}
	if n == 0 {
		return VaultMetadata{}, false, nil
	}
	item, err := r.metadata(ctx, id)
	return item, true, err
}

func (r *Repository) Delete(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM credentials WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return sql.ErrNoRows
	}
	return err
}

func (r *Repository) metadata(ctx context.Context, id string) (VaultMetadata, error) {
	var m Metadata
	var kind, status, created, updated string
	var expires sql.NullString
	var version uint64
	err := r.db.QueryRowContext(ctx, `SELECT id,account_id,credential_type,key_version,expires_at,status,created_at,updated_at FROM credentials WHERE id=?`, id).Scan(&m.ID, &m.AccountID, &kind, &version, &expires, &status, &created, &updated)
	if err != nil {
		return VaultMetadata{}, err
	}
	if expires.Valid {
		t, e := time.Parse(time.RFC3339Nano, expires.String)
		if e != nil {
			return VaultMetadata{}, e
		}
		m.ExpiresAt = &t
	}
	if m.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return VaultMetadata{}, err
	}
	if m.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
		return VaultMetadata{}, err
	}
	meta := VaultMetadata{Metadata: m, Kind: Kind(kind), Status: Status(status), Version: version}
	if meta.Status == StatusActive && m.ExpiresAt != nil && !m.ExpiresAt.After(time.Now()) {
		meta.Status = StatusExpired
	}
	return meta, nil
}

func nullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}
