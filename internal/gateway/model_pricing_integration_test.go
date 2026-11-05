package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	domainmodel "omnirouter/internal/model"
	"omnirouter/internal/provider"
	"omnirouter/internal/scheduler"
	"omnirouter/internal/storage"
)

type modelPricingTestProvider struct {
	id string
}

func (p *modelPricingTestProvider) ID() string   { return p.id }
func (p *modelPricingTestProvider) Name() string { return p.id }
func (p *modelPricingTestProvider) ListModels(context.Context) ([]provider.Model, error) {
	return []provider.Model{{ID: "model-test", Provider: p.id, Capabilities: []string{"chat"}}}, nil
}
func (p *modelPricingTestProvider) Generate(_ context.Context, req *provider.Request) (*provider.Response, error) {
	return &provider.Response{Model: req.Model, Output: []byte(`{}`)}, nil
}
func (p *modelPricingTestProvider) Stream(context.Context, *provider.Request, func(provider.Event) error) error {
	return nil
}
func (p *modelPricingTestProvider) HealthCheck(context.Context) error { return nil }

func TestAdminModelPricingUpdatesPersistenceAndAdaptiveRouting(t *testing.T) {
	ctx := context.Background()
	runtime, err := storage.OpenSQLiteRuntime(ctx, filepath.Join(t.TempDir(), "pricing-admin.db"), storage.SQLiteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	providerRepo, err := provider.NewRepository(runtime.Store().DB())
	if err != nil {
		t.Fatal(err)
	}
	modelRepo, err := domainmodel.NewRepository(runtime.Store().DB())
	if err != nil {
		t.Fatal(err)
	}

	cheap := &modelPricingTestProvider{id: "provider-cheap"}
	expensive := &modelPricingTestProvider{id: "provider-expensive"}
	registry := provider.NewRegistry()
	for _, p := range []*modelPricingTestProvider{cheap, expensive} {
		if err := providerRepo.Upsert(ctx, provider.ProviderConfig{
			ID: p.ID(), Name: p.Name(), Kind: "openai-compatible",
			BaseURL: "https://example.invalid/v1", Enabled: true,
		}); err != nil {
			t.Fatal(err)
		}
		if err := modelRepo.Upsert(ctx, domainmodel.Model{ID: "model-test", ProviderID: p.ID(), Enabled: true}); err != nil {
			t.Fatal(err)
		}
		if err := registry.Register(p); err != nil {
			t.Fatal(err)
		}
	}

	selector := scheduler.NewAdaptive(time.Minute)
	selector.Replace("model-test", []scheduler.Candidate{
		{Provider: cheap, Enabled: true, Weight: 1, Capabilities: []string{"chat"}, InputCostPerMillionUSD: 10, OutputCostPerMillionUSD: 10, PricingKnown: true},
		{Provider: expensive, Enabled: true, Weight: 1, Capabilities: []string{"chat"}, InputCostPerMillionUSD: 20, OutputCostPerMillionUSD: 20, PricingKnown: true},
	})
	handler := NewHandlerWithFullRepositories(registry, "gateway-admin-key", selector, nil, providerRepo, nil, nil, modelRepo, nil)

	request := httptest.NewRequest(http.MethodPatch, "/admin/models/model-test/pricing", strings.NewReader(`{
		"provider_id":"provider-cheap",
		"input_cost_per_million_usd":0,
		"output_cost_per_million_usd":0.5
	}`))
	request.Header.Set("Authorization", "Bearer gateway-admin-key")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("pricing update status=%d body=%s", response.Code, response.Body.String())
	}
	var result struct {
		RuntimeUpdated bool `json:"runtime_updated"`
		Persisted      bool `json:"persisted"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.RuntimeUpdated || !result.Persisted {
		t.Fatalf("pricing update was not applied to both runtime and storage: %+v", result)
	}

	stored, err := modelRepo.Get(ctx, "model-test", "provider-cheap")
	if err != nil {
		t.Fatal(err)
	}
	if stored.InputCostPerMillionUSD == nil || stored.OutputCostPerMillionUSD == nil ||
		*stored.InputCostPerMillionUSD != 0 || *stored.OutputCostPerMillionUSD != 0.5 {
		t.Fatalf("unexpected stored pricing: %+v", stored)
	}

	selected, err := selector.SelectAdaptive(ctx, "model-test", []string{"chat"}, scheduler.AdaptiveProfile{
		TaskType: "economy", EstimatedInputTokens: 1000, EstimatedOutputTokens: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if selected.ID() != "provider-cheap" {
		t.Fatalf("adaptive scheduler selected %q after pricing update, want provider-cheap", selected.ID())
	}
}
