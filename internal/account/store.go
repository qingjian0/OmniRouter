package account

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Store is the first-phase runtime account registry. It deliberately keeps
// metadata only; credentials and durable storage are separate integrations.
type Store struct {
	mu    sync.RWMutex
	items map[string]Account
}

func NewStore() *Store { return &Store{items: make(map[string]Account)} }

func (s *Store) Create(item Account) (Account, error) {
	item.ID = strings.TrimSpace(item.ID)
	item.ProviderID = strings.TrimSpace(item.ProviderID)
	item.Name = strings.TrimSpace(item.Name)
	if item.ID == "" {
		id, err := newID()
		if err != nil {
			return Account{}, err
		}
		item.ID = id
	}
	if item.ProviderID == "" || item.Name == "" {
		return Account{}, fmt.Errorf("provider_id and name are required")
	}
	if item.Status == "" {
		item.Status = StatusActive
	}
	if !validStatus(item.Status) {
		return Account{}, fmt.Errorf("invalid account status %q", item.Status)
	}
	now := time.Now().UTC()
	item.CreatedAt, item.UpdatedAt = now, now

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.items[item.ID]; exists {
		return Account{}, fmt.Errorf("account %q already exists", item.ID)
	}
	s.items[item.ID] = item
	return item, nil
}

func (s *Store) Get(id string) (Account, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.items[id]
	return item, ok
}

func (s *Store) List(providerID string) []Account {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]Account, 0, len(s.items))
	for _, item := range s.items {
		if providerID == "" || item.ProviderID == providerID {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	return items
}

func (s *Store) SetStatus(id string, status Status) (Account, bool, error) {
	if !validStatus(status) {
		return Account{}, false, fmt.Errorf("invalid account status %q", status)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.items[id]
	if !ok {
		return Account{}, false, nil
	}
	item.Status = status
	item.UpdatedAt = time.Now().UTC()
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
	return status == StatusActive || status == StatusDisabled || status == StatusError
}

func newID() (string, error) {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate account id: %w", err)
	}
	return "acct_" + hex.EncodeToString(raw[:]), nil
}
