package session

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Store holds runtime session metadata. It never stores cookies or tokens.
type Store struct {
	mu    sync.RWMutex
	items map[string]Session
}

func NewStore() *Store { return &Store{items: make(map[string]Session)} }

func (s *Store) Create(item Session) (Session, error) {
	item.ID = strings.TrimSpace(item.ID)
	item.AccountID = strings.TrimSpace(item.AccountID)
	if item.AccountID == "" {
		return Session{}, fmt.Errorf("account_id is required")
	}
	if item.ID == "" {
		id, err := newID()
		if err != nil {
			return Session{}, err
		}
		item.ID = id
	}
	if item.Status == "" {
		item.Status = StatusUnknown
	}
	if !validStatus(item.Status) {
		return Session{}, fmt.Errorf("invalid session status %q", item.Status)
	}
	now := time.Now().UTC()
	item.CreatedAt, item.UpdatedAt = now, now
	if item.ExpiresAt != nil {
		expiry := item.ExpiresAt.UTC()
		item.ExpiresAt = &expiry
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.items[item.ID]; exists {
		return Session{}, fmt.Errorf("session %q already exists", item.ID)
	}
	s.items[item.ID] = item
	return item, nil
}

func (s *Store) Get(id string) (Session, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.items[id]
	if ok && item.Status == StatusActive && item.ExpiresAt != nil && !item.ExpiresAt.After(time.Now()) {
		item.Status = StatusExpired
	}
	return item, ok
}

func (s *Store) List(accountID string) []Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	now := time.Now()
	items := make([]Session, 0, len(s.items))
	for _, item := range s.items {
		if accountID != "" && item.AccountID != accountID {
			continue
		}
		if item.Status == StatusActive && item.ExpiresAt != nil && !item.ExpiresAt.After(now) {
			item.Status = StatusExpired
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	return items
}

func (s *Store) Transition(id string, next Status) (Session, bool, error) {
	if !validStatus(next) {
		return Session{}, false, fmt.Errorf("invalid session status %q", next)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.items[id]
	if !ok {
		return Session{}, false, nil
	}
	if item.Status == StatusActive && item.ExpiresAt != nil && !item.ExpiresAt.After(time.Now()) {
		item.Status = StatusExpired
	}
	if !allowed(item.Status, next) {
		return Session{}, true, fmt.Errorf("invalid session transition: %s -> %s", item.Status, next)
	}
	item.Status, item.UpdatedAt = next, time.Now().UTC()
	s.items[id] = item
	return item, true, nil
}

func (s *Store) Delete(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[id]; !ok {
		return false
	}
	delete(s.items, id)
	return true
}

func validStatus(status Status) bool {
	return status == StatusUnknown || status == StatusActive || status == StatusExpired || status == StatusRevoked || status == StatusError
}

func allowed(current, next Status) bool {
	if current == next {
		return true
	}
	switch current {
	case StatusUnknown:
		return next == StatusActive || next == StatusError || next == StatusRevoked || next == StatusExpired
	case StatusActive:
		return next == StatusExpired || next == StatusRevoked || next == StatusError
	case StatusError:
		return next == StatusUnknown || next == StatusActive || next == StatusRevoked
	default:
		return false // expired and revoked sessions are terminal
	}
}

func newID() (string, error) {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate session id: %w", err)
	}
	return "sess_" + hex.EncodeToString(raw[:]), nil
}

// ListByAccount is the account-scoped listing used by management endpoints.
func (s *Store) ListByAccount(accountID string) []Session { return s.List(accountID) }
