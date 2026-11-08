package deepseekweb

import (
	"context"
	"errors"
	"testing"

	"omnirouter/internal/provider"
)

func TestRegistrationProviderFailsClosedUntilInferenceTransportExists(t *testing.T) {
	p := NewRegistrationProvider("deepseek-web-primary", "")

	if p.ID() != "deepseek-web-primary" || p.Name() != "deepseek-web-primary" {
		t.Fatalf("unexpected provider identity: id=%q name=%q", p.ID(), p.Name())
	}
	models, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 0 {
		t.Fatalf("registration-only provider advertised routable models: %+v", models)
	}

	if _, err := p.Generate(context.Background(), &provider.Request{Model: "deepseek"}); !errors.Is(err, ErrAuthorizationRequired) {
		t.Fatalf("Generate error = %v, want ErrAuthorizationRequired", err)
	}
	if err := p.Stream(context.Background(), &provider.Request{Model: "deepseek", Stream: true}, func(provider.Event) error { return nil }); !errors.Is(err, ErrAuthorizationRequired) {
		t.Fatalf("Stream error = %v, want ErrAuthorizationRequired", err)
	}
	if err := p.HealthCheck(context.Background()); !errors.Is(err, ErrAuthorizationRequired) {
		t.Fatalf("HealthCheck error = %v, want ErrAuthorizationRequired", err)
	}
}
