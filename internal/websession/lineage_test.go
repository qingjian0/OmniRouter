package websession

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"omnirouter/internal/storage"
)

func openLineageDB(t *testing.T, path string) *storage.Runtime {
	t.Helper()
	runtime, err := storage.OpenSQLiteRuntime(context.Background(), path, storage.SQLiteOptions{WAL: true})
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}

func seedLineageOwner(t *testing.T, db *sql.DB) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO providers(id,name,kind,base_url,enabled,priority,weight,created_at,updated_at) VALUES('web-provider','Web','deepseek-web','https://chat.deepseek.com',1,100,1,?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO accounts(id,provider_id,name,status,created_at,updated_at) VALUES('web-account','web-provider','Account','active',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
}

func TestLineagePersistsAcrossSQLiteRestartAndAdvancesGeneration(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "lineage.db")
	runtime := openLineageDB(t, path)
	seedLineageOwner(t, runtime.Store().DB())
	repo, err := NewLineageRepository(runtime.Store().DB())
	if err != nil {
		t.Fatal(err)
	}
	first, err := repo.Save(ctx, Lineage{
		ProviderID: "web-provider", AccountID: "web-account",
		LocalConversationID: "local-conversation-1",
		UpstreamConversationID: "upstream-chat-1",
		ParentMessageID: "parent-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Generation != 1 {
		t.Fatalf("first generation=%d, want 1", first.Generation)
	}
	second, err := repo.Save(ctx, Lineage{
		ProviderID: "web-provider", AccountID: "web-account",
		LocalConversationID: "local-conversation-1",
		UpstreamConversationID: "upstream-chat-1",
		ParentMessageID: "parent-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Generation != 2 || second.ParentMessageID != "parent-2" {
		t.Fatalf("updated lineage=%+v, want generation 2 and parent-2", second)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}

	reopened := openLineageDB(t, path)
	defer reopened.Close()
	reopenedRepo, err := NewLineageRepository(reopened.Store().DB())
	if err != nil {
		t.Fatal(err)
	}
	restored, err := reopenedRepo.Get(ctx, "web-provider", "web-account", "local-conversation-1")
	if err != nil {
		t.Fatal(err)
	}
	if restored.Generation != 2 || restored.UpstreamConversationID != "upstream-chat-1" || restored.ParentMessageID != "parent-2" {
		t.Fatalf("restored lineage mismatch: %+v", restored)
	}
}

func TestLineageIsolatedByProviderAndAccountAndRejectsOversizeIDs(t *testing.T) {
	runtime := openLineageDB(t, filepath.Join(t.TempDir(), "isolation.db"))
	defer runtime.Close()
	seedLineageOwner(t, runtime.Store().DB())
	repo, err := NewLineageRepository(runtime.Store().DB())
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.Save(context.Background(), Lineage{
		ProviderID: "web-provider", AccountID: "web-account",
		LocalConversationID: "conversation", UpstreamConversationID: "remote-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Get(context.Background(), "web-provider", "other-account", "conversation"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cross-account lookup error=%v, want sql.ErrNoRows", err)
	}
	if _, err := repo.Save(context.Background(), Lineage{
		ProviderID: "web-provider", AccountID: "web-account",
		LocalConversationID: string(make([]byte, maxLineageIDBytes+1)),
		UpstreamConversationID: "remote",
	}); err == nil {
		t.Fatal("oversized local conversation ID accepted")
	}
}

func TestConversationLocksSerializeSameConversationButAllowDifferentOnes(t *testing.T) {
	locks := NewConversationLocks()
	unlock, err := locks.Lock(context.Background(), "provider", "account", "conversation-a")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := locks.Lock(ctx, "provider", "account", "conversation-a"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("same-conversation lock error=%v, want deadline exceeded", err)
	}
	otherUnlock, err := locks.Lock(context.Background(), "provider", "account", "conversation-b")
	if err != nil {
		t.Fatalf("different conversation should not block: %v", err)
	}
	otherUnlock()
	unlock()

	release, err := locks.Lock(context.Background(), "provider", "account", "conversation-a")
	if err != nil {
		t.Fatalf("lock was not released after cancellation: %v", err)
	}
	release()
}

func TestConversationLockAllowsOnlyOneConcurrentOwner(t *testing.T) {
	locks := NewConversationLocks()
	var active atomic.Int32
	var maximum atomic.Int32
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() {
			unlock, err := locks.Lock(context.Background(), "provider", "account", "same")
			if err != nil {
				done <- struct{}{}
				return
			}
			current := active.Add(1)
			for {
				old := maximum.Load()
				if current <= old || maximum.CompareAndSwap(old, current) {
					break
				}
			}
			time.Sleep(time.Millisecond)
			active.Add(-1)
			unlock()
			done <- struct{}{}
		}()
	}
	for i := 0; i < 8; i++ {
		<-done
	}
	if got := maximum.Load(); got != 1 {
		t.Fatalf("maximum concurrent owners=%d, want 1", got)
	}
}
