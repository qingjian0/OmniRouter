package scheduler

import (
	"context"
	"math"
	"sync"
	"testing"
	"time"

	"omnirouter/internal/provider"
)

type adaptiveTestProvider struct {
	id string
}

func (p adaptiveTestProvider) ID() string   { return p.id }
func (p adaptiveTestProvider) Name() string { return p.id }

func (p adaptiveTestProvider) ListModels(context.Context) ([]provider.Model, error) {
	return nil, nil
}

func (p adaptiveTestProvider) Generate(context.Context, *provider.Request) (*provider.Response, error) {
	return &provider.Response{}, nil
}

func (p adaptiveTestProvider) Stream(context.Context, *provider.Request, func(provider.Event) error) error {
	return nil
}

func (p adaptiveTestProvider) HealthCheck(context.Context) error {
	return nil
}

type blockingHealthProvider struct {
	adaptiveTestProvider
	started chan struct{}
	release chan struct{}
	once sync.Once
}

func (p *blockingHealthProvider) HealthCheck(context.Context) error {
	p.once.Do(func() { close(p.started) })
	<-p.release
	return nil
}

func TestAdaptiveSchedulerPrefersLowerCostForEconomy(t *testing.T) {
	s := NewAdaptive(time.Minute)
	cheap := adaptiveTestProvider{id: "cheap"}
	expensive := adaptiveTestProvider{id: "expensive"}
	s.Replace("m", []Candidate{
		{Provider: cheap, Enabled: true, Weight: 1, InputCostPerMillionUSD: 1, OutputCostPerMillionUSD: 2},
		{Provider: expensive, Enabled: true, Weight: 1, InputCostPerMillionUSD: 8, OutputCostPerMillionUSD: 12},
	})

	got, err := s.SelectAdaptive(context.Background(), "m", nil, AdaptiveProfile{TaskType: "economy"})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID() != "cheap" {
		t.Fatalf("selected %q, want cheap", got.ID())
	}
}

func TestAdaptiveSchedulerLearnsLatency(t *testing.T) {
	s := NewAdaptive(time.Minute)
	slow := adaptiveTestProvider{id: "slow"}
	fast := adaptiveTestProvider{id: "fast"}
	s.Replace("m", []Candidate{
		{Provider: slow, Enabled: true, Weight: 1},
		{Provider: fast, Enabled: true, Weight: 1},
	})
	s.ReportOutcome("slow", 2*time.Second, true)
	s.ReportOutcome("fast", 100*time.Millisecond, true)

	got, err := s.SelectAdaptive(context.Background(), "m", nil, AdaptiveProfile{TaskType: "speed"})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID() != "fast" {
		t.Fatalf("selected %q, want fast", got.ID())
	}
}

func TestAdaptiveSchedulerFiltersCapabilitiesBeforeScoring(t *testing.T) {
	s := NewAdaptive(time.Minute)
	textProvider := adaptiveTestProvider{id: "text"}
	visionProvider := adaptiveTestProvider{id: "vision"}
	s.Replace("m", []Candidate{
		{Provider: textProvider, Enabled: true, Weight: 1, Capabilities: []string{"chat"}},
		{Provider: visionProvider, Enabled: true, Weight: 1, Capabilities: []string{"chat", "vision"}},
	})

	got, err := s.SelectWithRequirements(context.Background(), "m", []string{"vision"})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID() != "vision" {
		t.Fatalf("selected %q, want vision", got.ID())
	}
}

func TestAdaptiveSchedulerFailureEntersCooldown(t *testing.T) {
	s := NewAdaptive(time.Minute)
	p := adaptiveTestProvider{id: "p"}
	s.Replace("m", []Candidate{{Provider: p, Enabled: true, Weight: 1}})
	s.ReportOutcome("p", 100*time.Millisecond, false)
	s.ReportFailure("p")

	if _, err := s.Select(context.Background(), "m"); err == nil {
		t.Fatal("expected failed provider to be excluded during cooldown")
	}
}

func TestAdaptiveSchedulerDoesNotTreatUnknownPricingAsFree(t *testing.T) {
	s := NewAdaptive(time.Minute)
	unknown := adaptiveTestProvider{id: "unknown"}
	cheap := adaptiveTestProvider{id: "cheap"}
	expensive := adaptiveTestProvider{id: "expensive"}
	s.Replace("m", []Candidate{
		{Provider: unknown, Enabled: true, Weight: 1},
		{Provider: cheap, Enabled: true, Weight: 1, InputCostPerMillionUSD: 0.1, OutputCostPerMillionUSD: 0.2},
		{Provider: expensive, Enabled: true, Weight: 1, InputCostPerMillionUSD: 10, OutputCostPerMillionUSD: 20},
	})

	got, err := s.SelectAdaptive(context.Background(), "m", nil, AdaptiveProfile{TaskType: "economy"})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID() != "cheap" {
		t.Fatalf("selected %q, want explicitly priced cheap provider", got.ID())
	}
}

func TestEstimatedCostRejectsNonFiniteAndNegativePrices(t *testing.T) {
	profile := normalizeProfile(AdaptiveProfile{TaskType: "economy"})
	for _, candidate := range []Candidate{
		{InputCostPerMillionUSD: -1, OutputCostPerMillionUSD: 1},
		{InputCostPerMillionUSD: math.NaN(), OutputCostPerMillionUSD: 1},
		{InputCostPerMillionUSD: math.Inf(1), OutputCostPerMillionUSD: 1},
	} {
		if _, known := estimatedCost(candidate, profile); known {
			t.Fatalf("expected invalid price to be treated as unknown: %+v", candidate)
		}
	}
}

func TestReliabilityPriorRemainsNinetyFivePercent(t *testing.T) {
	got := reliabilityScore(providerPerformance{})
	if got != 0.95 {
		t.Fatalf("empty-history reliability = %v, want 0.95", got)
	}
	got = reliabilityScore(providerPerformance{samples: 1, successes: 0})
	if got != 0.76 {
		t.Fatalf("one-failure reliability = %v, want 0.76", got)
	}
}

func TestSetModelPricingUpdatesRuntimeAndKeepsExplicitZero(t *testing.T) {
	s := NewAdaptive(time.Minute)
	p := adaptiveTestProvider{id: "p"}
	s.Replace("m", []Candidate{{Provider: p, Enabled: true, Weight: 1}})
	if !s.SetModelPricing("m", "p", 0, 0) {
		t.Fatal("expected runtime pricing update")
	}
	candidate := s.candidates["m"][0]
	if !candidate.PricingKnown || candidate.InputCostPerMillionUSD != 0 || candidate.OutputCostPerMillionUSD != 0 {
		t.Fatalf("explicit zero pricing was not retained: %+v", candidate)
	}
	if cost, known := estimatedCost(candidate, normalizeProfile(AdaptiveProfile{})); !known || cost != 0 {
		t.Fatalf("explicit zero price should be known and free: cost=%v known=%v", cost, known)
	}
}

func TestSetModelPricingRejectsInvalidValues(t *testing.T) {
	s := NewAdaptive(time.Minute)
	p := adaptiveTestProvider{id: "p"}
	s.Replace("m", []Candidate{{Provider: p, Enabled: true, Weight: 1}})
	for _, tc := range []struct {
		name string
		in   float64
		out  float64
	}{
		{name: "negative input", in: -1, out: 1},
		{name: "negative output", in: 1, out: -1},
		{name: "nan input", in: math.NaN(), out: 1},
		{name: "infinite output", in: 1, out: math.Inf(1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if s.SetModelPricing("m", "p", tc.in, tc.out) {
				t.Fatal("invalid pricing must be rejected")
			}
		})
	}
	candidate := s.candidates["m"][0]
	if candidate.PricingKnown {
		t.Fatalf("rejected pricing must not mutate candidate: %+v", candidate)
	}
}

func TestModelRefreshPreservesConfiguredPricing(t *testing.T) {
	s := NewAdaptive(time.Minute)
	p := adaptiveTestProvider{id: "p"}
	s.Replace("m", []Candidate{{
		Provider: p, Enabled: true, Weight: 1,
		InputCostPerMillionUSD: 0.25, OutputCostPerMillionUSD: 0.5, PricingKnown: true,
	}})
	s.AddOrReplaceProviderModels(p, []provider.Model{{ID: "m", Capabilities: []string{"chat"}}}, 100, 1, true)
	candidate := s.candidates["m"][0]
	if !candidate.PricingKnown || candidate.InputCostPerMillionUSD != 0.25 || candidate.OutputCostPerMillionUSD != 0.5 {
		t.Fatalf("model refresh lost operator pricing: %+v", candidate)
	}
}

func TestEstimatedCostRejectsOverflowingTokenCost(t *testing.T) {
	profile := AdaptiveProfile{
		TaskType: "economy",
		EstimatedInputTokens: int64(^uint64(0) >> 1),
		EstimatedOutputTokens: int64(^uint64(0) >> 1),
	}
	candidate := Candidate{
		PricingKnown: true,
		InputCostPerMillionUSD: math.MaxFloat64,
		OutputCostPerMillionUSD: math.MaxFloat64,
	}
	if _, known := estimatedCost(candidate, profile); known {
		t.Fatal("overflowing estimated cost must not enter adaptive ranking")
	}
}

func TestAdaptiveSchedulerHandlesExtremePriorityRange(t *testing.T) {
	s := NewAdaptive(time.Minute)
	minInt := -int(^uint(0)>>1) - 1
	maxInt := int(^uint(0) >> 1)
	lowest := adaptiveTestProvider{id: "lowest"}
	middle := adaptiveTestProvider{id: "middle"}
	highest := adaptiveTestProvider{id: "highest"}
	s.Replace("m", []Candidate{
		{Provider: lowest, Enabled: true, Weight: 1, Priority: minInt},
		{Provider: middle, Enabled: true, Weight: 1, Priority: 0},
		{Provider: highest, Enabled: true, Weight: 1, Priority: maxInt},
	})
	got, err := s.SelectAdaptive(context.Background(), "m", nil, AdaptiveProfile{TaskType: "balanced"})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID() != "highest" {
		t.Fatalf("selected %q, want highest-priority provider", got.ID())
	}
}

func TestAddOrReplaceProviderModelsPreservesPricingAcrossDisableAndEnable(t *testing.T) {
	s := NewAdaptive(time.Minute)
	p := adaptiveTestProvider{id: "priced-provider"}
	models := []provider.Model{{ID: "model-test", Provider: p.ID(), Capabilities: []string{"chat", "streaming"}}}
	s.AddOrReplaceProviderModels(p, models, 100, 1, true)
	if !s.SetModelPricing("model-test", p.ID(), 0.25, 0.75) {
		t.Fatal("expected initial pricing update")
	}
	s.AddOrReplaceProviderModels(p, models, 100, 1, false)
	if _, err := s.SelectWithRequirements(context.Background(), "model-test", []string{"chat"}); err == nil {
		t.Fatal("disabled provider remained selectable")
	}
	s.AddOrReplaceProviderModels(p, models, 100, 1, true)
	candidates := s.candidates["model-test"]
	if len(candidates) != 1 {
		t.Fatalf("candidate count = %d, want 1", len(candidates))
	}
	got := candidates[0]
	if !got.PricingKnown || got.InputCostPerMillionUSD != 0.25 || got.OutputCostPerMillionUSD != 0.75 {
		t.Fatalf("pricing was lost across disable/enable: %+v", got)
	}
}

func TestRemoveProviderClearsAdaptiveMetrics(t *testing.T) {
	s := NewAdaptive(time.Minute)
	p := adaptiveTestProvider{id: "remove-me"}
	s.Replace("m", []Candidate{{Provider: p, Enabled: true, Weight: 1}})
	s.ReportOutcome(p.ID(), 250*time.Millisecond, true)

	s.metricsMu.RLock()
	_, recorded := s.metrics[p.ID()]
	s.metricsMu.RUnlock()
	if !recorded {
		t.Fatal("expected outcome metrics before removal")
	}

	s.RemoveProvider(p.ID())

	s.metricsMu.RLock()
	_, retained := s.metrics[p.ID()]
	s.metricsMu.RUnlock()
	if retained {
		t.Fatal("removed provider metrics must be cleared")
	}
	if _, err := s.Select(context.Background(), "m"); err == nil {
		t.Fatal("removed provider must no longer be selectable")
	}
}

func TestDisablingProviderRetainsAdaptiveMetrics(t *testing.T) {
	s := NewAdaptive(time.Minute)
	p := adaptiveTestProvider{id: "disable-me"}
	models := []provider.Model{{ID: "m", Provider: p.ID(), Capabilities: []string{"chat"}}}
	s.AddOrReplaceProviderModels(p, models, 100, 1, true)
	s.ReportOutcome(p.ID(), 250*time.Millisecond, true)

	s.AddOrReplaceProviderModels(p, models, 100, 1, false)
	s.metricsMu.RLock()
	_, retained := s.metrics[p.ID()]
	s.metricsMu.RUnlock()
	if !retained {
		t.Fatal("disabling a provider must preserve learned metrics")
	}
}

func TestBlendLatencyEMAHandlesMaximumDuration(t *testing.T) {
	maxDuration := time.Duration(1<<63 - 1)
	got := blendLatencyEMA(maxDuration, maxDuration)
	if got != maxDuration {
		t.Fatalf("blend(max,max) = %d, want %d", got, maxDuration)
	}
	got = blendLatencyEMA(5*time.Second, 10*time.Second)
	if got != 6*time.Second {
		t.Fatalf("blend(5s,10s) = %s, want 6s", got)
	}
	if got := blendLatencyEMA(0, 7*time.Second); got != 7*time.Second {
		t.Fatalf("zero previous latency = %s, want 7s", got)
	}
}

func TestAdaptiveMetricCountersSaturateAtUint64Maximum(t *testing.T) {
	max := ^uint64(0)
	if got := saturatingIncrement(max); got != max {
		t.Fatalf("saturatingIncrement(MaxUint64) = %d, want %d", got, max)
	}
	if got := saturatingIncrement(max - 1); got != max {
		t.Fatalf("saturatingIncrement(MaxUint64-1) = %d, want %d", got, max)
	}

	s := NewAdaptive(time.Minute)
	s.metrics["counter-boundary"] = providerPerformance{samples: max, successes: max}
	s.ReportOutcome("counter-boundary", time.Millisecond, true)
	s.metricsMu.RLock()
	got := s.metrics["counter-boundary"]
	s.metricsMu.RUnlock()
	if got.samples != max || got.successes != max {
		t.Fatalf("counters wrapped at boundary: %#v", got)
	}
}



func TestSaturatingFailureIncrementAtUint32Maximum(t *testing.T) {
	if got := saturatingFailureIncrement(^uint32(0)); got != ^uint32(0) {
		t.Fatalf("failure counter wrapped at uint32 maximum: got %d", got)
	}
	if got := saturatingFailureIncrement(^uint32(0) - 1); got != ^uint32(0) {
		t.Fatalf("failure counter did not reach uint32 maximum: got %d", got)
	}
	if got := saturatingFailureIncrement(0); got != 1 {
		t.Fatalf("first failure count = %d, want 1", got)
	}
}


func TestRemovedProviderCannotPublishStaleHealthAndProbeLockIsRetired(t *testing.T) {
	s := New(time.Minute)
	oldProvider := &blockingHealthProvider{
		adaptiveTestProvider: adaptiveTestProvider{id: "shared-id"},
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	newProvider := adaptiveTestProvider{id: "shared-id"}
	s.Replace("m", []Candidate{{Provider: oldProvider, Enabled: true, Weight: 1}})

	selection := make(chan error, 1)
	go func() {
		_, err := s.Select(context.Background(), "m")
		selection <- err
	}()
	<-oldProvider.started

	// Replace the route while the old instance's health request is in flight.
	s.Replace("m", []Candidate{{Provider: newProvider, Enabled: true, Weight: 1}})
	close(oldProvider.release)
	if err := <-selection; err == nil {
		t.Fatal("selection based on the removed provider instance must fail")
	}
	if _, exists := s.health["shared-id"]; exists {
		t.Fatal("removed provider's in-flight health result was published")
	}
	if len(s.probeLocks) != 0 {
		t.Fatalf("idle probe locks were retained: %d", len(s.probeLocks))
	}

	got, err := s.Select(context.Background(), "m")
	if err != nil {
		t.Fatal(err)
	}
	if got != newProvider {
		t.Fatalf("selected stale provider instance %T, want replacement", got)
	}
	if len(s.probeLocks) != 0 {
		t.Fatalf("completed probe lock was not retired: %d", len(s.probeLocks))
	}
}
