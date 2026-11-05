package scheduler

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"omnirouter/internal/provider"
)

// AdaptiveProfile describes the request's routing objective. Token counts are
// estimates used only for relative cost ranking; they are not billing records.
type AdaptiveProfile struct {
	TaskType               string
	EstimatedInputTokens   int64
	EstimatedOutputTokens  int64
}

type providerPerformance struct {
	samples    uint64
	successes  uint64
	latencyEMA time.Duration
}

// AdaptiveScheduler adds outcome-driven latency/reliability ranking and
// optional operator-supplied model pricing to the existing health-aware router.
// The embedded Scheduler retains its administration and circuit-breaker API.
type AdaptiveScheduler struct {
	*Scheduler
	metricsMu sync.RWMutex
	metrics   map[string]providerPerformance
}

func NewAdaptive(healthTTL time.Duration) *AdaptiveScheduler {
	return &AdaptiveScheduler{
		Scheduler: New(healthTTL),
		metrics:   make(map[string]providerPerformance),
	}
}

func (s *AdaptiveScheduler) Select(ctx context.Context, modelID string) (provider.Provider, error) {
	return s.SelectAdaptive(ctx, modelID, nil, AdaptiveProfile{TaskType: "balanced"})
}

func (s *AdaptiveScheduler) SelectWithRequirements(ctx context.Context, modelID string, required []string) (provider.Provider, error) {
	return s.SelectAdaptive(ctx, modelID, required, AdaptiveProfile{TaskType: classifyTask(required)})
}

// SelectAdaptive first enforces capability and health constraints, then ranks
// the remaining providers by the request objective, observed latency,
// reliability, optional model pricing, operator priority, and configured weight.
func (s *AdaptiveScheduler) SelectAdaptive(ctx context.Context, modelID string, required []string, profile AdaptiveProfile) (provider.Provider, error) {
	s.mu.RLock()
	candidates := append([]Candidate(nil), s.candidates[modelID]...)
	cursor := s.cursor[modelID]
	s.mu.RUnlock()
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no enabled provider for model %q", modelID)
	}

	eligible := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Provider == nil || !candidate.Enabled || !hasCapabilities(candidate.Capabilities, required) {
			continue
		}
		if s.Scheduler.isHealthy(ctx, candidate.Provider) {
			eligible = append(eligible, candidate)
		}
	}
	if len(eligible) == 0 {
		return nil, fmt.Errorf("no healthy provider for model %q satisfies required capabilities %v", modelID, required)
	}
	if cursor == nil {
		s.mu.Lock()
		cursor = s.cursor[modelID]
		if cursor == nil {
			s.cursor[modelID] = newAtomicCursor()
			cursor = s.cursor[modelID]
		}
		s.mu.Unlock()
	}

	profile = normalizeProfile(profile)
	stats := make(map[string]providerPerformance, len(eligible))
	s.metricsMu.RLock()
	for _, candidate := range eligible {
		stats[candidate.Provider.ID()] = s.metrics[candidate.Provider.ID()]
	}
	s.metricsMu.RUnlock()

	minLatency, maxLatency := time.Duration(math.MaxInt64), time.Duration(0)
	minCost, maxCost := math.MaxFloat64, 0.0
	hasPriced := false
	minPriority, maxPriority := eligible[0].Priority, eligible[0].Priority
	maxWeight := uint32(1)
	for _, candidate := range eligible {
		stat := stats[candidate.Provider.ID()]
		if stat.latencyEMA > 0 {
			if stat.latencyEMA < minLatency { minLatency = stat.latencyEMA }
			if stat.latencyEMA > maxLatency { maxLatency = stat.latencyEMA }
		}
		cost, known := estimatedCost(candidate, profile)
		if known {
			hasPriced = true
			if cost < minCost { minCost = cost }
			if cost > maxCost { maxCost = cost }
		}
		if candidate.Priority < minPriority { minPriority = candidate.Priority }
		if candidate.Priority > maxPriority { maxPriority = candidate.Priority }
		if candidate.Weight > maxWeight { maxWeight = candidate.Weight }
	}

	weights := objectiveWeights(profile.TaskType)
	type scored struct { candidate Candidate; score float64 }
	ranked := make([]scored, 0, len(eligible))
	best := -1.0
	for _, candidate := range eligible {
		stat := stats[candidate.Provider.ID()]
		latencyScore := 0.5
		if stat.latencyEMA > 0 {
			if minLatency == maxLatency { latencyScore = 1 } else {
				latencyScore = 1 - float64(stat.latencyEMA-minLatency)/float64(maxLatency-minLatency)
			}
		}
		reliabilityScore := float64(stat.successes+4*95/100) / float64(stat.samples+4)
		if stat.samples == 0 { reliabilityScore = 0.95 }
		costScore := 0.5
		if cost, known := estimatedCost(candidate, profile); known && hasPriced {
			if maxCost == minCost { costScore = 1 } else { costScore = 1 - (cost-minCost)/(maxCost-minCost) }
		}
		priorityScore := 0.5
		if maxPriority != minPriority {
			priorityScore = float64(candidate.Priority-minPriority) / float64(maxPriority-minPriority)
		}
		weightScore := math.Log1p(float64(candidate.Weight)) / math.Log1p(float64(maxWeight))
		score := weights.cost*costScore + weights.latency*latencyScore +
			weights.reliability*reliabilityScore + weights.priority*priorityScore +
			weights.weight*weightScore
		ranked = append(ranked, scored{candidate: candidate, score: score})
		if score > best { best = score }
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })

	// Rotate among near-equal top candidates so a single provider does not
	// monopolize traffic when measured scores are effectively tied.
	tied := make([]Candidate, 0, len(ranked))
	for _, item := range ranked {
		if best-item.score > 0.015 { break }
		tied = append(tied, item.candidate)
	}
	if len(tied) == 1 { return tied[0].Provider, nil }
	totalWeight := uint64(0)
	for _, candidate := range tied { totalWeight += uint64(candidate.Weight) }
	if totalWeight == 0 { return tied[0].Provider, nil }
	ticket := cursor.Add(1)-1
	ticket %= totalWeight
	for _, candidate := range tied {
		if ticket < uint64(candidate.Weight) { return candidate.Provider, nil }
		ticket -= uint64(candidate.Weight)
	}
	return tied[0].Provider, nil
}

// ReportOutcome feeds real request results back into future routing decisions.
// Successful requests refresh the positive health cache; failures enter the
// existing exponential circuit-breaker cooldown.
func (s *AdaptiveScheduler) ReportOutcome(providerID string, latency time.Duration, success bool) {
	if providerID == "" { return }
	s.metricsMu.Lock()
	stat := s.metrics[providerID]
	stat.samples++
	if success { stat.successes++ }
	if latency > 0 {
		if stat.latencyEMA == 0 { stat.latencyEMA = latency } else {
			stat.latencyEMA = time.Duration(0.2*float64(latency) + 0.8*float64(stat.latencyEMA))
		}
	}
	s.metrics[providerID] = stat
	s.metricsMu.Unlock()

	if success {
		s.mu.Lock()
		s.health[providerID] = healthRecord{checked: time.Now(), healthy: true}
		s.mu.Unlock()
	} else {
		s.Scheduler.ReportFailure(providerID)
	}
}

type objectiveWeight struct { cost, latency, reliability, priority, weight float64 }

func objectiveWeights(task string) objectiveWeight {
	switch task {
	case "economy":
		return objectiveWeight{cost: .50, latency: .15, reliability: .20, priority: .10, weight: .05}
	case "speed":
		return objectiveWeight{cost: .10, latency: .50, reliability: .25, priority: .10, weight: .05}
	case "reasoning":
		return objectiveWeight{cost: .10, latency: .20, reliability: .40, priority: .25, weight: .05}
	case "vision", "tools":
		return objectiveWeight{cost: .15, latency: .20, reliability: .35, priority: .25, weight: .05}
	default:
		return objectiveWeight{cost: .25, latency: .25, reliability: .30, priority: .15, weight: .05}
	}
}

func classifyTask(required []string) string {
	for _, item := range required { if item == "reasoning" { return "reasoning" } }
	for _, item := range required { if item == "vision" { return "vision" } }
	for _, item := range required { if item == "tools" { return "tools" } }
	for _, item := range required { if item == "streaming" { return "speed" } }
	return "balanced"
}

func normalizeProfile(profile AdaptiveProfile) AdaptiveProfile {
	if profile.TaskType == "" { profile.TaskType = "balanced" }
	if profile.EstimatedInputTokens <= 0 { profile.EstimatedInputTokens = 1024 }
	if profile.EstimatedOutputTokens <= 0 { profile.EstimatedOutputTokens = 512 }
	return profile
}

func estimatedCost(candidate Candidate, profile AdaptiveProfile) (float64, bool) {
	if candidate.InputCostPerMillionUSD < 0 || candidate.OutputCostPerMillionUSD < 0 { return 0, false }
	if candidate.InputCostPerMillionUSD == 0 && candidate.OutputCostPerMillionUSD == 0 { return 0, false }
	input := float64(profile.EstimatedInputTokens) * candidate.InputCostPerMillionUSD / 1_000_000
	output := float64(profile.EstimatedOutputTokens) * candidate.OutputCostPerMillionUSD / 1_000_000
	return input + output, true
}
