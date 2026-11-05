package scheduler

import (
	"context"
	"math"
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
