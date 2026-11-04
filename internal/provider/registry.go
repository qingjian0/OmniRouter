package provider

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

func NewRegistry() *Registry { return &Registry{providers: make(map[string]Provider)} }
func (r *Registry) Register(p Provider) error {
	if p == nil || strings.TrimSpace(p.ID()) == "" {
		return fmt.Errorf("provider and provider ID are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.providers[p.ID()]; ok {
		return fmt.Errorf("provider %q already registered", p.ID())
	}
	r.providers[p.ID()] = p
	return nil
}
func (r *Registry) Get(id string) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[id]
	return p, ok
}
func (r *Registry) Count() int { r.mu.RLock(); defer r.mu.RUnlock(); return len(r.providers) }
func (r *Registry) List() []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Provider, 0, len(r.providers))
	for _, p := range r.providers {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out
}
func (r *Registry) ResolveModel(model string) (Provider, bool) {
	for _, p := range r.List() {
		models, err := p.ListModels(context.Background())
		if err != nil {
			continue
		}
		for _, m := range models {
			if m.ID == model {
				return p, true
			}
		}
	}
	return nil, false
}

// Replace atomically installs a provider with the same ID or registers a new one.
func (r *Registry) Replace(p Provider) error {
	if p == nil || strings.TrimSpace(p.ID()) == "" {
		return fmt.Errorf("provider and provider ID are required")
	}
	r.mu.Lock()
	r.providers[p.ID()] = p
	r.mu.Unlock()
	return nil
}

// Unregister removes a provider from the runtime registry.
func (r *Registry) Unregister(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.providers[id]; !ok {
		return false
	}
	delete(r.providers, id)
	return true
}
