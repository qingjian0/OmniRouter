package websession

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"
)

const maxLineageIDBytes = 512

// Lineage binds a local gateway conversation to the upstream conversation and
// parent message owned by one provider account. Upstream identifiers are
// sensitive correlation metadata: callers must not include them in logs.
type Lineage struct {
	ProviderID             string
	AccountID              string
	LocalConversationID    string
	UpstreamConversationID string
	ParentMessageID        string
	Generation             int64
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

type LineageRepository struct{ db *sql.DB }

func NewLineageRepository(db *sql.DB) (*LineageRepository, error) {
	if db == nil {
		return nil, fmt.Errorf("web session lineage database is required")
	}
	return &LineageRepository{db: db}, nil
}

// Save atomically creates or advances one lineage record. Generation is
// monotonic and lets a caller detect whether another request advanced the
// conversation between turns. Same-conversation requests must additionally be
// serialized with ConversationLocks before reading and saving.
func (r *LineageRepository) Save(ctx context.Context, item Lineage) (Lineage, error) {
	item.ProviderID = strings.TrimSpace(item.ProviderID)
	item.AccountID = strings.TrimSpace(item.AccountID)
	item.LocalConversationID = strings.TrimSpace(item.LocalConversationID)
	item.UpstreamConversationID = strings.TrimSpace(item.UpstreamConversationID)
	item.ParentMessageID = strings.TrimSpace(item.ParentMessageID)
	if item.ProviderID == "" || item.AccountID == "" || item.LocalConversationID == "" || item.UpstreamConversationID == "" {
		return Lineage{}, fmt.Errorf("provider, account, local conversation, and upstream conversation IDs are required")
	}
	if len(item.ProviderID) > maxLineageIDBytes {
		return Lineage{}, fmt.Errorf("provider_id exceeds %d bytes", maxLineageIDBytes)
	}
	if len(item.AccountID) > maxLineageIDBytes {
		return Lineage{}, fmt.Errorf("account_id exceeds %d bytes", maxLineageIDBytes)
	}
	if len(item.LocalConversationID) > maxLineageIDBytes {
		return Lineage{}, fmt.Errorf("local_conversation_id exceeds %d bytes", maxLineageIDBytes)
	}
	if len(item.UpstreamConversationID) > maxLineageIDBytes {
		return Lineage{}, fmt.Errorf("upstream_conversation_id exceeds %d bytes", maxLineageIDBytes)
	}
	if len(item.ParentMessageID) > maxLineageIDBytes {
		return Lineage{}, fmt.Errorf("parent_message_id exceeds %d bytes", maxLineageIDBytes)
	}
	now := time.Now().UTC()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	item.UpdatedAt = now
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO web_conversation_lineage(
			provider_id, account_id, local_conversation_id,
			upstream_conversation_id, parent_message_id, generation,
			created_at, updated_at
		) VALUES(?,?,?,?,?,1,?,?)
		ON CONFLICT(provider_id, account_id, local_conversation_id) DO UPDATE SET
			upstream_conversation_id=excluded.upstream_conversation_id,
			parent_message_id=excluded.parent_message_id,
			generation=web_conversation_lineage.generation+1,
			updated_at=excluded.updated_at
	`, item.ProviderID, item.AccountID, item.LocalConversationID,
		item.UpstreamConversationID, nullableID(item.ParentMessageID),
		item.CreatedAt.Format(time.RFC3339Nano), item.UpdatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return Lineage{}, fmt.Errorf("save web conversation lineage: %w", err)
	}
	return r.Get(ctx, item.ProviderID, item.AccountID, item.LocalConversationID)
}

func (r *LineageRepository) Get(ctx context.Context, providerID, accountID, localConversationID string) (Lineage, error) {
	var item Lineage
	var parent sql.NullString
	var created, updated string
	err := r.db.QueryRowContext(ctx, `
		SELECT provider_id, account_id, local_conversation_id,
			upstream_conversation_id, parent_message_id, generation,
			created_at, updated_at
		FROM web_conversation_lineage
		WHERE provider_id=? AND account_id=? AND local_conversation_id=?
	`, strings.TrimSpace(providerID), strings.TrimSpace(accountID), strings.TrimSpace(localConversationID)).
		Scan(&item.ProviderID, &item.AccountID, &item.LocalConversationID,
			&item.UpstreamConversationID, &parent, &item.Generation, &created, &updated)
	if err != nil {
		return Lineage{}, err
	}
	if parent.Valid {
		item.ParentMessageID = parent.String
	}
	item.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return Lineage{}, fmt.Errorf("parse web lineage created_at: %w", err)
	}
	item.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	if err != nil {
		return Lineage{}, fmt.Errorf("parse web lineage updated_at: %w", err)
	}
	return item, nil
}

func (r *LineageRepository) Delete(ctx context.Context, providerID, accountID, localConversationID string) error {
	result, err := r.db.ExecContext(ctx, `
		DELETE FROM web_conversation_lineage
		WHERE provider_id=? AND account_id=? AND local_conversation_id=?
	`, strings.TrimSpace(providerID), strings.TrimSpace(accountID), strings.TrimSpace(localConversationID))
	if err != nil {
		return fmt.Errorf("delete web conversation lineage: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func nullableID(value string) any {
	if value == "" {
		return nil
	}
	return value
}

type conversationLockEntry struct {
	mu   sync.Mutex
	refs int
}

// ConversationLocks serializes turns for one (provider, account,
// local-conversation) tuple. It prevents concurrent requests from racing the
// same upstream parent_message_id and forking the remote conversation.
type ConversationLocks struct {
	mu      sync.Mutex
	entries map[string]*conversationLockEntry
}

func NewConversationLocks() *ConversationLocks {
	return &ConversationLocks{entries: make(map[string]*conversationLockEntry)}
}

func (l *ConversationLocks) Lock(ctx context.Context, providerID, accountID, localConversationID string) (func(), error) {
	if l == nil {
		return nil, fmt.Errorf("conversation lock registry is required")
	}
	providerID = strings.TrimSpace(providerID)
	accountID = strings.TrimSpace(accountID)
	localConversationID = strings.TrimSpace(localConversationID)
	for _, value := range []string{providerID, accountID, localConversationID} {
		if value == "" || strings.ContainsRune(value, '\x00') {
			return nil, fmt.Errorf("provider, account, and local conversation IDs must be non-empty and contain no NUL bytes")
		}
	}
	key := strings.Join([]string{providerID, accountID, localConversationID}, "\x00")
	l.mu.Lock()
	entry := l.entries[key]
	if entry == nil {
		entry = &conversationLockEntry{}
		l.entries[key] = entry
	}
	entry.refs++
	l.mu.Unlock()

	acquired := make(chan struct{})
	go func() {
		entry.mu.Lock()
		close(acquired)
	}()
	select {
	case <-ctx.Done():
		// The waiter still owns one reference. Release it after the queued
		// mutex acquisition so a concurrent request cannot orphan the entry.
		go func() {
			<-acquired
			entry.mu.Unlock()
			l.releaseRef(key, entry)
		}()
		return nil, ctx.Err()
	case <-acquired:
		var once sync.Once
		return func() {
			once.Do(func() {
				entry.mu.Unlock()
				l.releaseRef(key, entry)
			})
		}, nil
	}
}

func (l *ConversationLocks) releaseRef(key string, entry *conversationLockEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry.refs--
	if entry.refs == 0 && l.entries[key] == entry {
		delete(l.entries, key)
	}
}
