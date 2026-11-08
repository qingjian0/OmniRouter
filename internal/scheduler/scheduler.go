// Package scheduler selects an eligible provider for a requested model.
package scheduler

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"omnirouter/internal/provider"
)

type Candidate struct {
	Provider                provider.Provider
	Priority                int
	Weight                  uint32
	Enabled                 bool
	Capabilities            []string
	ContextLimit            int
	InputCostPerMillionUSD  float64
	OutputCostPerMillionUSD float64
	PricingKnown            bool
}

type Scheduler struct {
	mu         sync.RWMutex
	candidates map[string][]Candidate
	cursor     map[string]*atomic.Uint64
	healthTTL  time.Duration
	health     map[string]healthRecord
	probeLocks map[string]*sync.Mutex
}

type healthRecord struct {
	checked        time.Time
	healthy        bool
	failures       uint32
	unhealthyUntil time.Time
}

func New(healthTTL time.Duration) *Scheduler {
	if healthTTL <= 0 {
		healthTTL = 30 * time.Second
	}
	return &Scheduler{
		candidates: make(map[string][]Candidate),
		cursor:     make(map[string]*atomic.Uint64),
		healthTTL:  healthTTL,
		health:     make(map[string]healthRecord),
		probeLocks: make(map[string]*sync.Mutex),
	}
}

func (s *Scheduler) Replace(modelID string, candidates []Candidate) {
	filtered := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Provider != nil && candidate.Enabled {
			if candidate.Weight == 0 {
				candidate.Weight = 1
			}
			filtered = append(filtered, candidate)
		}
	}
	sort.SliceStable(filtered, func(i, j int) bool { return filtered[i].Priority > filtered[j].Priority })
	s.mu.Lock()
	defer s.mu.Unlock()
	s.candidates[modelID] = filtered
	if s.cursor[modelID] == nil {
		s.cursor[modelID] = &atomic.Uint64{}
	}
}

func (s *Scheduler) Select(ctx context.Context, modelID string) (provider.Provider, error) {
	return s.SelectWithRequirements(ctx, modelID, nil)
}

// SelectWithRequirements filters candidates by declared model capabilities before
// applying priority, weighted selection, and health checks. Capabilities are
// declarations supplied by the adapter/configuration, not runtime certification.
func (s *Scheduler) SelectWithRequirements(ctx context.Context, modelID string, required []string) (provider.Provider, error) {
	s.mu.RLock()
	candidates := append([]Candidate(nil), s.candidates[modelID]...)
	cursor := s.cursor[modelID]
	s.mu.RUnlock()
	eligibleCandidates := candidates[:0]
	for _, candidate := range candidates {
		if candidate.Enabled && candidate.Provider != nil {
			eligibleCandidates = append(eligibleCandidates, candidate)
		}
	}
	candidates = eligibleCandidates
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no enabled provider for model %q", modelID)
	}
	if len(required) > 0 {
		eligible := candidates[:0]
		for _, candidate := range candidates {
			if hasCapabilities(candidate.Capabilities, required) {
				eligible = append(eligible, candidate)
			}
		}
		candidates = eligible
		if len(candidates) == 0 {
			return nil, fmt.Errorf("no provider for model %q declares required capabilities: %v", modelID, required)
		}
	}
	// Priority groups are strict: a lower-priority provider is considered only
	// when every provider in the higher-priority group is unhealthy. Weighted
	// round-robin is applied only to healthy candidates, avoiding traffic skew
	// toward the first healthy entry after an unhealthy weighted selection.
	for start := 0; start < len(candidates); {
		end := start + 1
		for end < len(candidates) && candidates[end].Priority == candidates[start].Priority {
			end++
		}
		group := candidates[start:end]
		healthy := make([]Candidate, 0, len(group))
		total := uint64(0)
		for _, candidate := range group {
			if s.isHealthy(ctx, candidate.Provider) {
				healthy = append(healthy, candidate)
				total += uint64(candidate.Weight)
			}
		}
		if len(healthy) > 0 && total > 0 {
			n := cursor.Add(1) - 1
			ticket := n % total
			for _, candidate := range healthy {
				if ticket < uint64(candidate.Weight) {
					return candidate.Provider, nil
				}
				ticket -= uint64(candidate.Weight)
			}
		}
		start = end
	}
	return nil, fmt.Errorf("all providers for model %q are unavailable", modelID)
}

func (s *Scheduler) isHealthy(ctx context.Context, p provider.Provider) bool {
	id := p.ID()
	if healthy, decided := s.cachedHealth(id, time.Now()); decided {
		return healthy
	}

	// Only one half-open health probe may run per provider. Without this guard,
	// a burst of requests after cooldown can stampede the upstream /models API.
	s.mu.Lock()
	probeLock := s.probeLocks[id]
	if probeLock == nil {
		probeLock = &sync.Mutex{}
		s.probeLocks[id] = probeLock
	}
	s.mu.Unlock()
	probeLock.Lock()
	defer probeLock.Unlock()

	// Another request may have completed the probe while this request waited.
	if healthy, decided := s.cachedHealth(id, time.Now()); decided {
		return healthy
	}
	if err := ctx.Err(); err != nil {
		return false
	}

	checkedAt := time.Now()
	err := p.HealthCheck(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := s.health[id]
	if err == nil {
		s.health[id] = healthRecord{checked: checkedAt, healthy: true}
		return true
	}
	failures := saturatingFailureIncrement(previous.failures)
	s.health[id] = healthRecord{checked: checkedAt, healthy: false, failures: failures, unhealthyUntil: checkedAt.Add(failureCooldown(failures))}
	return false
}

// cachedHealth reports a decision only while the positive TTL or negative
// cooldown is active. An expired record requires a serialized half-open probe.
func (s *Scheduler) cachedHealth(providerID string, now time.Time) (healthy, decided bool) {
	s.mu.RLock()
	record, ok := s.health[providerID]
	ttl := s.healthTTL
	s.mu.RUnlock()
	if !ok {
		return false, false
	}
	if record.healthy && now.Sub(record.checked) < ttl {
		return true, true
	}
	if !record.healthy && now.Before(record.unhealthyUntil) {
		return false, true
	}
	return false, false
}

func saturatingFailureIncrement(value uint32) uint32 {
	if value == ^uint32(0) {
		return value
	}
	return value + 1
}

func failureCooldown(failures uint32) time.Duration {
	// 1s, 2s, 4s ... capped at five minutes. This is a circuit-breaker
	// cooldown, not a retry loop; a single half-open health probe follows it.
	if failures > 10 {
		failures = 10
	}
	d := time.Second * time.Duration(1<<(failures-1))
	if d > 5*time.Minute {
		return 5 * time.Minute
	}
	return d
}

func (s *Scheduler) InvalidateHealth(providerID string) {
	s.mu.Lock()
	delete(s.health, providerID)
	s.mu.Unlock()
}

// ReportFailure temporarily removes a provider from eligibility until the
// health cache expires or an explicit invalidation is requested.
func (s *Scheduler) ReportFailure(providerID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	previous := s.health[providerID]
	failures := saturatingFailureIncrement(previous.failures)
	s.health[providerID] = healthRecord{checked: now, healthy: false, failures: failures, unhealthyUntil: now.Add(failureCooldown(failures))}
}

// RemoveProvider removes a provider from every model route and invalidates its health state.
func (s *Scheduler) RemoveProvider(providerID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.health, providerID)
	for modelID, candidates := range s.candidates {
		kept := candidates[:0]
		for _, candidate := range candidates {
			if candidate.Provider != nil && candidate.Provider.ID() != providerID {
				kept = append(kept, candidate)
			}
		}
		s.candidates[modelID] = kept
	}
}

// AddOrReplaceProvider adds the provider to the configured model routes.
func (s *Scheduler) AddOrReplaceProvider(p provider.Provider, models []string, priority int, weight uint32, enabled bool) {
	if p == nil { return }
	if priority == 0 { priority = 100 }
	if weight == 0 { weight = 1 }
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.health, p.ID())
	previous := make(map[string]Candidate)
	for modelID, candidates := range s.candidates {
		kept := candidates[:0]
		for _, candidate := range candidates {
			if candidate.Provider != nil && candidate.Provider.ID() == p.ID() {
				previous[modelID] = candidate
				continue
			}
			kept = append(kept, candidate)
		}
		s.candidates[modelID] = kept
	}
	for _, modelID := range models {
		old, found := previous[modelID]
		if !enabled && !found { continue }
		candidate := Candidate{Provider: p, Priority: priority, Weight: weight, Enabled: enabled}
		if found {
			candidate.Capabilities = append([]string(nil), old.Capabilities...)
			candidate.ContextLimit = old.ContextLimit
			candidate.InputCostPerMillionUSD = old.InputCostPerMillionUSD
			candidate.OutputCostPerMillionUSD = old.OutputCostPerMillionUSD
			candidate.PricingKnown = old.PricingKnown
		}
		s.candidates[modelID] = append(s.candidates[modelID], candidate)
		sort.SliceStable(s.candidates[modelID], func(i, j int) bool { return s.candidates[modelID][i].Priority > s.candidates[modelID][j].Priority })
		if s.cursor[modelID] == nil { s.cursor[modelID] = &atomic.Uint64{} }
	}
}
func hasCapabilities(available, required []string) bool {
	set := make(map[string]struct{}, len(available))
	for _, capability := range available {
		set[capability] = struct{}{}
	}
	for _, capability := range required {
		if _, ok := set[capability]; !ok {
			return false
		}
	}
	return true
}

// AddOrReplaceProviderModels keeps the scheduler's model capability metadata
// synchronized with the provider registry during runtime administration.
func (s *Scheduler) AddOrReplaceProviderModels(p provider.Provider, models []provider.Model, priority int, weight uint32, enabled bool) {
	if p == nil { return }
	if priority == 0 { priority = 100 }
	if weight == 0 { weight = 1 }
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.health, p.ID())
	previousPricing := make(map[string]Candidate)
	for modelID, candidates := range s.candidates {
		kept := candidates[:0]
		for _, candidate := range candidates {
			if candidate.Provider != nil && candidate.Provider.ID() == p.ID() {
				previousPricing[modelID] = candidate
			} else {
				kept = append(kept, candidate)
			}
		}
		s.candidates[modelID] = kept
	}
	for _, model := range models {
		previous, found := previousPricing[model.ID]
		if !enabled && !found { continue }
		candidate := Candidate{
			Provider: p, Priority: priority, Weight: weight, Enabled: enabled,
			Capabilities: append([]string(nil), model.Capabilities...), ContextLimit: model.ContextLimit,
		}
		if found {
			candidate.InputCostPerMillionUSD = previous.InputCostPerMillionUSD
			candidate.OutputCostPerMillionUSD = previous.OutputCostPerMillionUSD
			candidate.PricingKnown = previous.PricingKnown
		}
		s.candidates[model.ID] = append(s.candidates[model.ID], candidate)
		sort.SliceStable(s.candidates[model.ID], func(i, j int) bool { return s.candidates[model.ID][i].Priority > s.candidates[model.ID][j].Priority })
		if s.cursor[model.ID] == nil { s.cursor[model.ID] = &atomic.Uint64{} }
	}
}
