package websession

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
)

// ConversationCoordinator combines durable lineage lookup with an in-process
// per-conversation lock. A lease must span the complete upstream turn so the
// next request cannot read a stale parent message ID.
type ConversationCoordinator struct {
	repository *LineageRepository
	locks      *ConversationLocks
}

func NewConversationCoordinator(repository *LineageRepository, locks *ConversationLocks) (*ConversationCoordinator, error) {
	if repository == nil || locks == nil {
		return nil, fmt.Errorf("lineage repository and conversation locks are required")
	}
	return &ConversationCoordinator{repository: repository, locks: locks}, nil
}

// ConversationLease owns the conversation lock until Close. Its lineage is a
// snapshot read while holding that lock. Commit advances the durable upstream
// IDs only after the caller has verified a successful upstream response.
type ConversationLease struct {
	repository *LineageRepository
	lineage    Lineage
	unlock     func()
	mu         sync.Mutex
	closed     bool
}

func (c *ConversationCoordinator) Begin(ctx context.Context, providerID, accountID, localConversationID string) (*ConversationLease, error) {
	if c == nil || c.repository == nil || c.locks == nil {
		return nil, fmt.Errorf("conversation coordinator is required")
	}
	unlock, err := c.locks.Lock(ctx, providerID, accountID, localConversationID)
	if err != nil {
		return nil, err
	}
	item, err := c.repository.Get(ctx, providerID, accountID, localConversationID)
	if errors.Is(err, sql.ErrNoRows) {
		item = Lineage{
			ProviderID:          providerID,
			AccountID:           accountID,
			LocalConversationID: localConversationID,
		}
		err = nil
	}
	if err != nil {
		unlock()
		return nil, fmt.Errorf("load conversation lineage: %w", err)
	}
	return &ConversationLease{repository: c.repository, lineage: item, unlock: unlock}, nil
}

func (l *ConversationLease) Lineage() Lineage {
	if l == nil {
		return Lineage{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lineage
}

// Commit stores the remote conversation and parent message identifiers. An
// empty parent ID is allowed for a newly created remote conversation; the
// upstream conversation ID is mandatory. Callers must not commit provisional
// IDs from an unverified or failed response.
func (l *ConversationLease) Commit(ctx context.Context, upstreamConversationID, parentMessageID string) (Lineage, error) {
	if l == nil {
		return Lineage{}, fmt.Errorf("conversation lease is required")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return Lineage{}, fmt.Errorf("conversation lease is closed")
	}
	item := l.lineage
	item.UpstreamConversationID = upstreamConversationID
	item.ParentMessageID = parentMessageID
	saved, err := l.repository.Save(ctx, item)
	if err != nil {
		return Lineage{}, err
	}
	l.lineage = saved
	return saved, nil
}

func (l *ConversationLease) Close() {
	if l == nil {
		return
	}
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return
	}
	l.closed = true
	unlock := l.unlock
	l.unlock = nil
	l.mu.Unlock()
	if unlock != nil {
		unlock()
	}
}
