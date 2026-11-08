package scheduler

import (
	"context"
	"testing"

	"omnirouter/internal/provider"
)

type disabledProviderRegression struct{ id string }

func (p *disabledProviderRegression) ID() string   { return p.id }
func (p *disabledProviderRegression) Name() string { return p.id }
func (p *disabledProviderRegression) ListModels(context.Context) ([]provider.Model, error) {
	return []provider.Model{{ID: "model-test", Provider: p.id, Capabilities: []string{"chat", "streaming"}}}, nil
}
func (p *disabledProviderRegression) Generate(context.Context, *provider.Request) (*provider.Response, error) {
	return &provider.Response{Model: "model-test"}, nil
}
func (p *disabledProviderRegression) Stream(context.Context, *provider.Request, func(provider.Event) error) error {
	return nil
}
func (p *disabledProviderRegression) HealthCheck(context.Context) error { return nil }

func TestAddOrReplaceProviderModelsRemovesDisabledProvider(t *testing.T) {
	s := New(0)
	p := &disabledProviderRegression{id: "provider-test"}
	models, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	s.AddOrReplaceProviderModels(p, models, 100, 1, true)
	if selected, err := s.SelectWithRequirements(context.Background(), "model-test", []string{"streaming"}); err != nil || selected.ID() != p.ID() {
		t.Fatalf("enabled provider was not selectable: selected=%v err=%v", selected, err)
	}

	s.AddOrReplaceProviderModels(p, models, 100, 1, false)
	if selected, err := s.SelectWithRequirements(context.Background(), "model-test", []string{"streaming"}); err == nil {
		t.Fatalf("disabled provider remained selectable: %v", selected.ID())
	}
}

func TestAddOrReplaceProviderDoesNotInsertDisabledProvider(t *testing.T) {
	s := New(0)
	p := &disabledProviderRegression{id: "provider-test"}
	s.AddOrReplaceProvider(p, []string{"model-test"}, 100, 1, false)
	if selected, err := s.Select(context.Background(), "model-test"); err == nil {
		t.Fatalf("disabled provider was selectable: %v", selected.ID())
	}
}
