package credential

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"omnirouter/internal/security"
)

// Status describes the local lifecycle of a credential. It does not assert
// that the remote provider has accepted or validated the secret.
type Status string

const (
	StatusActive   Status = "active"
	StatusExpired  Status = "expired"
	StatusRevoked  Status = "revoked"
	StatusDisabled Status = "disabled"
)

type Kind string

const (
	KindAPIKey       Kind = "api_key"
	KindAccessToken  Kind = "access_token"
	KindRefreshToken Kind = "refresh_token"
	KindCookie       Kind = "cookie"
	KindUsernamePass Kind = "username_password"
)

// Vault keeps ciphertext only. The encryption key is supplied by the caller
// and is never copied into metadata or returned from this package.
type Vault struct {
	mu         sync.RWMutex
	items      map[string]vaultItem
	encryptor  *security.Encryptor
	repository *Repository
}

type vaultItem struct {
	metadata Metadata
	kind     Kind
	status   Status
	version  uint64
	cipher   []byte
}

type VaultMetadata struct {
	Metadata
	Kind    Kind   `json:"kind"`
	Status  Status `json:"status"`
	Version uint64 `json:"version"`
}

func NewVault(encryptor *security.Encryptor) (*Vault, error) {
	if encryptor == nil {
		return nil, fmt.Errorf("credential encryptor is required")
	}
	return &Vault{items: make(map[string]vaultItem), encryptor: encryptor}, nil
}

// NewPersistentVault uses SQLite as the source of truth. The same public Vault
// operations are retained so gateway handlers do not need separate code paths.
func NewPersistentVault(encryptor *security.Encryptor, repository *Repository) (*Vault, error) {
	if encryptor == nil || repository == nil {
		return nil, fmt.Errorf("credential encryptor and repository are required")
	}
	return &Vault{items: make(map[string]vaultItem), encryptor: encryptor, repository: repository}, nil
}

// Put encrypts a new secret or replaces an existing one. Replacements retain
// the original creation time and increment the credential version.
func (v *Vault) Put(m Metadata, kind Kind, plaintext []byte) (VaultMetadata, error) {
	m.ID, m.AccountID = strings.TrimSpace(m.ID), strings.TrimSpace(m.AccountID)
	if m.ID == "" {
		id, err := credentialID()
		if err != nil {
			return VaultMetadata{}, err
		}
		m.ID = id
	}
	if m.AccountID == "" || !validKind(kind) || len(plaintext) == 0 {
		return VaultMetadata{}, fmt.Errorf("account_id, supported credential kind and non-empty value are required")
	}
	if v.repository != nil {
		return v.repository.Put(context.Background(), m, kind, plaintext)
	}
	if m.ExpiresAt != nil {
		expires := m.ExpiresAt.UTC()
		m.ExpiresAt = &expires
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	ad := associatedData(m.AccountID, m.ID, string(kind))
	ciphertext, err := v.encryptor.Encrypt(plaintext, ad)
	if err != nil {
		return VaultMetadata{}, err
	}
	now := time.Now().UTC()
	version := uint64(1)
	if old, ok := v.items[m.ID]; ok {
		if old.metadata.AccountID != m.AccountID || old.kind != kind {
			return VaultMetadata{}, fmt.Errorf("credential identity is immutable; create a new id to change account or kind")
		}
		m.CreatedAt = old.metadata.CreatedAt
		version = old.version + 1
	} else {
		m.CreatedAt = now
	}
	m.UpdatedAt = now
	status := StatusActive
	if m.ExpiresAt != nil && !m.ExpiresAt.After(now) {
		status = StatusExpired
	}
	if old, ok := v.items[m.ID]; ok {
		zero(old.cipher)
	}
	v.items[m.ID] = vaultItem{metadata: m, kind: kind, status: status, version: version, cipher: ciphertext}
	return toVaultMetadata(v.items[m.ID]), nil
}

// Reveal decrypts a secret only for an explicit internal caller. Callers must
// not serialize or log the returned bytes.
func (v *Vault) Reveal(id string) (VaultMetadata, []byte, error) {
	if v.repository != nil {
		return v.repository.Get(context.Background(), id)
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	item, ok := v.items[id]
	if !ok {
		return VaultMetadata{}, nil, fmt.Errorf("credential not found")
	}
	if item.status != StatusActive {
		return toVaultMetadata(item), nil, fmt.Errorf("credential is %s", item.status)
	}
	if item.metadata.ExpiresAt != nil && !item.metadata.ExpiresAt.After(time.Now()) {
		return toVaultMetadata(item), nil, fmt.Errorf("credential is expired")
	}
	plain, err := v.encryptor.Decrypt(item.cipher, associatedData(item.metadata.AccountID, item.metadata.ID, string(item.kind)))
	if err != nil {
		return VaultMetadata{}, nil, err
	}
	return toVaultMetadata(item), plain, nil
}

func (v *Vault) List(accountID string) []VaultMetadata {
	items, _ := v.ListContext(context.Background(), accountID)
	return items
}

func (v *Vault) ListContext(ctx context.Context, accountID string) ([]VaultMetadata, error) {
	if v.repository != nil {
		return v.repository.List(ctx, accountID)
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	items := make([]VaultMetadata, 0, len(v.items))
	for _, item := range v.items {
		if accountID == "" || item.metadata.AccountID == accountID {
			items = append(items, toVaultMetadata(item))
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Metadata.CreatedAt.After(items[j].Metadata.CreatedAt) })
	return items, nil
}

func (v *Vault) SetStatus(id string, status Status) (VaultMetadata, bool, error) {
	if v.repository != nil {
		return v.repository.SetStatus(context.Background(), id, status)
	}
	if status != StatusActive && status != StatusDisabled && status != StatusRevoked && status != StatusExpired {
		return VaultMetadata{}, false, fmt.Errorf("invalid credential status %q", status)
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	item, ok := v.items[id]
	if !ok {
		return VaultMetadata{}, false, nil
	}
	if item.status == StatusRevoked && status != StatusRevoked {
		return VaultMetadata{}, true, fmt.Errorf("revoked credentials are terminal")
	}
	if item.metadata.ExpiresAt != nil && !item.metadata.ExpiresAt.After(time.Now()) && status == StatusActive {
		return VaultMetadata{}, true, fmt.Errorf("expired credentials cannot be reactivated; replace the secret")
	}
	item.status, item.metadata.UpdatedAt = status, time.Now().UTC()
	v.items[id] = item
	return toVaultMetadata(item), true, nil
}

func (v *Vault) Delete(id string) bool {
	deleted, _ := v.DeleteContext(context.Background(), id)
	return deleted
}

func (v *Vault) DeleteContext(ctx context.Context, id string) (bool, error) {
	if v.repository != nil {
		err := v.repository.Delete(ctx, id)
		if err == sql.ErrNoRows {
			return false, nil
		}
		return err == nil, err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	item, ok := v.items[id]
	if !ok {
		return false, nil
	}
	zero(item.cipher)
	delete(v.items, id)
	return true, nil
}

// Rotate re-encrypts every item with a replacement deployment key. The swap
// is atomic in memory: if any decrypt/encrypt operation fails, nothing changes.
func (v *Vault) Rotate(next *security.Encryptor) error {
	if v.repository != nil {
		return fmt.Errorf("persistent credential key rotation requires an explicit database rotation operation")
	}
	if next == nil {
		return fmt.Errorf("replacement encryptor is required")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	replacement := make(map[string]vaultItem, len(v.items))
	for id, item := range v.items {
		ad := associatedData(item.metadata.AccountID, item.metadata.ID, string(item.kind))
		plain, err := v.encryptor.Decrypt(item.cipher, ad)
		if err != nil {
			return fmt.Errorf("decrypt credential %s during rotation: %w", id, err)
		}
		ciphertext, err := next.Encrypt(plain, ad)
		for i := range plain {
			plain[i] = 0
		}
		if err != nil {
			return fmt.Errorf("encrypt credential %s during rotation: %w", id, err)
		}
		item.cipher = ciphertext
		item.version++
		item.metadata.UpdatedAt = time.Now().UTC()
		replacement[id] = item
	}
	for _, old := range v.items {
		zero(old.cipher)
	}
	v.items, v.encryptor = replacement, next
	return nil
}

func validKind(kind Kind) bool {
	switch kind {
	case KindAPIKey, KindAccessToken, KindRefreshToken, KindCookie, KindUsernamePass:
		return true
	default:
		return false
	}
}

func associatedData(accountID, id, kind string) []byte {
	return []byte(accountID + ":" + id + ":" + kind)
}
func toVaultMetadata(item vaultItem) VaultMetadata {
	if item.status == StatusActive && item.metadata.ExpiresAt != nil && !item.metadata.ExpiresAt.After(time.Now()) {
		item.status = StatusExpired
	}
	return VaultMetadata{Metadata: item.metadata, Kind: item.kind, Status: item.status, Version: item.version}
}
func credentialID() (string, error) {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate credential id: %w", err)
	}
	return "cred_" + hex.EncodeToString(raw[:]), nil
}

func zero(value []byte) {
	for i := range value {
		value[i] = 0
	}
}

// Persistent reports whether credential operations use the durable repository.
func (v *Vault) Persistent() bool { return v != nil && v.repository != nil }
