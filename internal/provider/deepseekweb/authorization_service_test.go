package deepseekweb

import (
	"context"
	"encoding/base64"
	"errors"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"omnirouter/internal/account"
	"omnirouter/internal/credential"
	providerrepo "omnirouter/internal/provider"
	"omnirouter/internal/security"
	"omnirouter/internal/session"
	"omnirouter/internal/storage"
)

func TestAuthorizationServicePersistsRestoresAndRevokes(t *testing.T) {
	ctx := context.Background()
	runtime, err := storage.OpenSQLiteRuntime(ctx, filepath.Join(t.TempDir(), "omni.db"), storage.SQLiteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	encryptor, err := security.NewEncryptor(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	credentialRepo, err := credential.NewRepository(runtime.Store().DB(), encryptor)
	if err != nil {
		t.Fatal(err)
	}
	vault, err := credential.NewPersistentVault(encryptor, credentialRepo)
	if err != nil {
		t.Fatal(err)
	}
	providerRepo, err := providerrepo.NewRepository(runtime.Store().DB())
	if err != nil {
		t.Fatal(err)
	}
	if err := providerRepo.Upsert(ctx, providerrepo.ProviderConfig{
		ID: "deepseek-web", Name: "DeepSeek Web", Kind: ProviderKind,
		BaseURL: DefaultOrigin, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	accountRepo, err := account.NewRepository(runtime.Store().DB())
	if err != nil {
		t.Fatal(err)
	}
	sessionRepo, err := session.NewRepository(runtime.Store().DB())
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewAuthorizationService(accountRepo, sessionRepo, vault)
	if err != nil {
		t.Fatal(err)
	}

	expires := time.Now().Add(time.Hour).UTC()
	record, err := service.Authorize(ctx, "deepseek-web", "personal", validInput(), &expires)
	if err != nil {
		t.Fatal(err)
	}
	if record.AccountID == "" || record.SessionID == "" || record.AccessTokenID == "" || record.CookieCredentialID == "" {
		t.Fatalf("authorization record is incomplete: %+v", record)
	}
	if record.TokenVersion != 1 || record.CookieVersion != 1 {
		t.Fatalf("new credentials must start at version 1: %+v", record)
	}

	restored, err := service.LoadAuthorization(ctx, record.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	if string(restored.Token) != "user-token-value" {
		t.Fatal("restored token does not match imported token")
	}
	if string(restored.CookieHeader) != "ds_session_id=session-123; other=value" {
		t.Fatal("restored cookie header does not match validated cookie set")
	}
	zeroBytes(restored.Token)
	zeroBytes(restored.CookieHeader)

	metadata, err := vault.ListContext(ctx, record.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata) != 2 {
		t.Fatalf("expected two encrypted credentials, got %d", len(metadata))
	}
	for _, item := range metadata {
		if item.Status != credential.StatusActive || item.Version != 1 {
			t.Fatalf("unexpected credential metadata: %+v", item)
		}
	}

	// Simulate a credential-store failure. Revocation must still disable the
	// account first so that no partially retained credential pair can restore.
	if _, err := runtime.Store().DB().ExecContext(ctx, `CREATE TRIGGER reject_test_credential_revoke BEFORE UPDATE OF status ON credentials WHEN NEW.status='revoked' BEGIN SELECT RAISE(ABORT, 'injected credential revoke failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := service.Revoke(ctx, record.AccountID); err == nil {
		t.Fatal("expected injected credential revocation failure")
	}
	if _, err := service.LoadAuthorization(ctx, record.AccountID); !errors.Is(err, ErrAuthorizationRequired) {
		t.Fatalf("partially failed revocation must remain fail-closed, got %v", err)
	}
	owner, err := accountRepo.Get(ctx, record.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	if owner.Status != account.StatusDisabled {
		t.Fatalf("account must be disabled before credential cleanup, got %s", owner.Status)
	}
	if _, err := runtime.Store().DB().ExecContext(ctx, "DROP TRIGGER reject_test_credential_revoke"); err != nil {
		t.Fatal(err)
	}
	if err := service.Revoke(ctx, record.AccountID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.LoadAuthorization(ctx, record.AccountID); !errors.Is(err, ErrAuthorizationRequired) {
		t.Fatalf("revoked authorization must not restore, got %v", err)
	}
	owner, err := accountRepo.Get(ctx, record.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	if owner.Status != account.StatusDisabled {
		t.Fatalf("expected disabled account, got %s", owner.Status)
	}
	sessionRecord, err := sessionRepo.Get(ctx, record.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if sessionRecord.Status != session.StatusRevoked {
		t.Fatalf("expected revoked session, got %s", sessionRecord.Status)
	}
	metadata, err = vault.ListContext(ctx, record.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range metadata {
		if item.Status != credential.StatusRevoked {
			t.Fatalf("expected revoked credential, got %s", item.Status)
		}
	}
}

func TestAuthorizationServiceRollsBackPartialPersistence(t *testing.T) {
	ctx := context.Background()
	runtime, err := storage.OpenSQLiteRuntime(ctx, filepath.Join(t.TempDir(), "rollback.db"), storage.SQLiteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	encryptor, err := security.NewEncryptor(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	credentialRepo, err := credential.NewRepository(runtime.Store().DB(), encryptor)
	if err != nil {
		t.Fatal(err)
	}
	vault, err := credential.NewPersistentVault(encryptor, credentialRepo)
	if err != nil {
		t.Fatal(err)
	}
	providerRepo, err := providerrepo.NewRepository(runtime.Store().DB())
	if err != nil {
		t.Fatal(err)
	}
	if err := providerRepo.Upsert(ctx, providerrepo.ProviderConfig{
		ID: "deepseek-web", Name: "DeepSeek Web", Kind: ProviderKind,
		BaseURL: DefaultOrigin, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	accountRepo, err := account.NewRepository(runtime.Store().DB())
	if err != nil {
		t.Fatal(err)
	}
	sessionRepo, err := session.NewRepository(runtime.Store().DB())
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewAuthorizationService(accountRepo, sessionRepo, vault)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := runtime.Store().DB().ExecContext(ctx, `CREATE TRIGGER reject_test_session BEFORE INSERT ON sessions BEGIN SELECT RAISE(ABORT, 'injected session persistence failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authorize(ctx, "deepseek-web", "rollback test", validInput(), nil); err == nil {
		t.Fatal("expected injected session persistence failure")
	}

	for _, table := range []string{"accounts", "credentials", "sessions"} {
		var count int
		if err := runtime.Store().DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
			t.Fatalf("count %s after rollback: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("partial authorization left %d rows in %s", count, table)
		}
	}
}

func TestAccountOperationLocksSerializeSameAccountAndReleaseEntries(t *testing.T) {
	var locks accountOperationLocks
	first := locks.lock("account-a")
	release := make(chan func(), 1)
	go func() { release <- locks.lock("account-a") }()

	deadline := time.After(time.Second)
	for {
		locks.mu.Lock()
		entry := locks.entries["account-a"]
		refs := 0
		if entry != nil {
			refs = entry.refs
		}
		locks.mu.Unlock()
		if refs == 2 {
			break
		}
		select {
		case <-deadline:
			first()
			t.Fatal("second operation did not register its account lock reference")
		default:
			runtime.Gosched()
		}
	}

	first()
	second := <-release
	second()

	locks.mu.Lock()
	defer locks.mu.Unlock()
	if len(locks.entries) != 0 {
		t.Fatalf("account lock entries leaked after release: %d", len(locks.entries))
	}
}

func TestAccountOperationLocksDoNotBlockDifferentAccounts(t *testing.T) {
	var locks accountOperationLocks
	first := locks.lock("account-a")
	defer first()

	done := make(chan func(), 1)
	go func() { done <- locks.lock("account-b") }()
	second := <-done
	second()
}
