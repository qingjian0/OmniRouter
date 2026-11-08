package websession

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestConversationCoordinatorLoadsAndCommitsLineageUnderLease(t *testing.T) {
	runtime := openLineageDB(t, filepath.Join(t.TempDir(), "coordinator.db"))
	defer runtime.Close()
	seedLineageOwner(t, runtime.Store().DB())
	repository, err := NewLineageRepository(runtime.Store().DB())
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := NewConversationCoordinator(repository, NewConversationLocks())
	if err != nil {
		t.Fatal(err)
	}

	first, err := coordinator.Begin(context.Background(), "web-provider", "web-account", "local-1")
	if err != nil {
		t.Fatal(err)
	}
	if got := first.Lineage(); got.UpstreamConversationID != "" || got.ParentMessageID != "" {
		t.Fatalf("new lease should have no remote lineage: %+v", got)
	}
	saved, err := first.Commit(context.Background(), "remote-chat-1", "parent-1")
	if err != nil {
		t.Fatal(err)
	}
	if saved.Generation != 1 || saved.UpstreamConversationID != "remote-chat-1" || saved.ParentMessageID != "parent-1" {
		t.Fatalf("unexpected committed lineage: %+v", saved)
	}
	first.Close()
	first.Close()
	if _, err := first.Commit(context.Background(), "remote-chat-1", "parent-late"); err == nil {
		t.Fatal("commit after lease close should fail")
	}

	second, err := coordinator.Begin(context.Background(), "web-provider", "web-account", "local-1")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	loaded := second.Lineage()
	if loaded.Generation != 1 || loaded.UpstreamConversationID != "remote-chat-1" || loaded.ParentMessageID != "parent-1" {
		t.Fatalf("next lease did not load persisted lineage: %+v", loaded)
	}
	advanced, err := second.Commit(context.Background(), "remote-chat-1", "parent-2")
	if err != nil {
		t.Fatal(err)
	}
	if advanced.Generation != 2 || advanced.ParentMessageID != "parent-2" {
		t.Fatalf("lineage did not advance: %+v", advanced)
	}
}

func TestConversationCoordinatorDoesNotCommitIncompleteRemoteIdentity(t *testing.T) {
	runtime := openLineageDB(t, filepath.Join(t.TempDir(), "coordinator-incomplete.db"))
	defer runtime.Close()
	seedLineageOwner(t, runtime.Store().DB())
	repository, err := NewLineageRepository(runtime.Store().DB())
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := NewConversationCoordinator(repository, NewConversationLocks())
	if err != nil {
		t.Fatal(err)
	}
	lease, err := coordinator.Begin(context.Background(), "web-provider", "web-account", "local-2")
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	if _, err := lease.Commit(context.Background(), "", "parent"); err == nil {
		t.Fatal("empty upstream conversation ID must fail closed")
	}
	if _, err := repository.Get(context.Background(), "web-provider", "web-account", "local-2"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("incomplete commit unexpectedly persisted: %v", err)
	}
}

func TestConversationCoordinatorHonorsBeginCancellation(t *testing.T) {
	runtime := openLineageDB(t, filepath.Join(t.TempDir(), "coordinator-cancel.db"))
	defer runtime.Close()
	seedLineageOwner(t, runtime.Store().DB())
	repository, err := NewLineageRepository(runtime.Store().DB())
	if err != nil {
		t.Fatal(err)
	}
	locks := NewConversationLocks()
	coordinator, err := NewConversationCoordinator(repository, locks)
	if err != nil {
		t.Fatal(err)
	}
	first, err := coordinator.Begin(context.Background(), "web-provider", "web-account", "local-3")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := coordinator.Begin(ctx, "web-provider", "web-account", "local-3"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("begin cancellation error=%v, want deadline exceeded", err)
	}
}
