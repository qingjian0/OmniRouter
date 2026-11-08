package credential

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"omnirouter/internal/security"
	"omnirouter/internal/storage"
)

func TestPersistentVaultSurvivesSQLiteRestartWithoutExposingPlaintext(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "credentials.db")
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	encryptor, err := security.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := storage.OpenSQLiteRuntime(ctx, path, storage.SQLiteOptions{WAL: true})
	if err != nil {
		t.Fatal(err)
	}
	db := runtime.Store().DB()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.ExecContext(ctx, `INSERT INTO providers(id,name,kind,base_url,enabled,priority,weight,created_at,updated_at) VALUES(?,?,?,?,1,100,1,?,?)`, "provider-test", "Provider", "openai-compatible", "https://example.test/v1", now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO accounts(id,provider_id,name,status,created_at,updated_at) VALUES(?,?,?,'active',?,?)`, "account-test", "provider-test", "Provider account", now, now); err != nil {
		t.Fatal(err)
	}
	repo, err := NewRepository(db, encryptor)
	if err != nil {
		t.Fatal(err)
	}
	vault, err := NewPersistentVault(encryptor, repo)
	if err != nil {
		t.Fatal(err)
	}
	const secret = "provider-secret-never-serialize"
	if _, err := vault.Put(Metadata{ID: "credential-test", AccountID: "account-test", Type: string(KindAPIKey)}, KindAPIKey, []byte(secret)); err != nil {
		t.Fatal(err)
	}
	listed, err := vault.ListContext(ctx, "account-test")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(listed)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("credential list exposed plaintext: %s", encoded)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}

	restorePath := path
	if sqlite3, lookupErr := exec.LookPath("sqlite3"); lookupErr == nil {
		backupPath := filepath.Join(t.TempDir(), "credentials.backup.db")
		backup := exec.Command("bash", "../../scripts/backup-sqlite.sh", path, backupPath)
		if output, err := backup.CombinedOutput(); err != nil {
			t.Fatalf("SQLite backup script failed: %v: %s", err, output)
		}
		restorePath = filepath.Join(t.TempDir(), "credentials-restored.db")
		seed := exec.Command(sqlite3, restorePath, "CREATE TABLE pre_restore_marker(value TEXT); INSERT INTO pre_restore_marker VALUES('old');")
		if output, err := seed.CombinedOutput(); err != nil {
			t.Fatalf("seed restore destination: %v: %s", err, output)
		}
		restore := exec.Command("bash", "../../scripts/restore-sqlite.sh", restorePath, backupPath)
		if output, err := restore.CombinedOutput(); err != nil {
			t.Fatalf("SQLite restore script failed: %v: %s", err, output)
		}
	}

	reopened, err := storage.OpenSQLiteRuntime(ctx, restorePath, storage.SQLiteOptions{WAL: true})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	reopenedRepo, err := NewRepository(reopened.Store().DB(), encryptor)
	if err != nil {
		t.Fatal(err)
	}
	reopenedVault, err := NewPersistentVault(encryptor, reopenedRepo)
	if err != nil {
		t.Fatal(err)
	}
	meta, plaintext, err := reopenedVault.Reveal("credential-test")
	if err != nil {
		t.Fatalf("reveal after restart: %v", err)
	}
	defer zero(plaintext)
	if string(plaintext) != secret || meta.Status != StatusActive {
		t.Fatalf("restored credential mismatch: status=%s", meta.Status)
	}

	wrongEncryptor, err := security.NewEncryptor(base64.StdEncoding.EncodeToString([]byte("abcdef0123456789abcdef0123456789")))
	if err != nil {
		t.Fatal(err)
	}
	wrongRepo, err := NewRepository(reopened.Store().DB(), wrongEncryptor)
	if err != nil {
		t.Fatal(err)
	}
	wrongVault, err := NewPersistentVault(wrongEncryptor, wrongRepo)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := wrongVault.Reveal("credential-test"); err == nil {
		t.Fatal("credential must not decrypt with a different deployment key")
	}
}
