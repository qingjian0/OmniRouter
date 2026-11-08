package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"omnirouter/internal/apikey"
	"omnirouter/internal/provider"
	"omnirouter/internal/scheduler"
	"omnirouter/internal/storage"
)

type securityTestProvider struct {
	id       string
	generate func(context.Context, *provider.Request) (*provider.Response, error)
	stream   func(context.Context, *provider.Request, func(provider.Event) error) error
}

func (p *securityTestProvider) ID() string   { return p.id }
func (p *securityTestProvider) Name() string { return p.id }
func (p *securityTestProvider) ListModels(context.Context) ([]provider.Model, error) {
	return []provider.Model{{ID: "model-test", Provider: p.id, Capabilities: []string{"chat", "streaming"}}}, nil
}
func (p *securityTestProvider) Generate(ctx context.Context, req *provider.Request) (*provider.Response, error) {
	if p.generate != nil {
		return p.generate(ctx, req)
	}
	return &provider.Response{Model: req.Model, Output: []byte(`{"choices":[{"message":{"content":"ok"}}]}`)}, nil
}
func (p *securityTestProvider) Stream(ctx context.Context, req *provider.Request, emit func(provider.Event) error) error {
	if p.stream != nil {
		return p.stream(ctx, req, emit)
	}
	if err := emit(provider.Event{Data: []byte(`data: {"choices":[{"delta":{"content":"ok"}}]}`)}); err != nil {
		return err
	}
	return emit(provider.Event{Done: true, ExplicitDone: true})
}
func (p *securityTestProvider) HealthCheck(context.Context) error { return nil }

func TestAdminRoutesRequireConfiguredMasterKey(t *testing.T) {
	registry := provider.NewRegistry()
	handler := NewHandler(registry, "master-secret", nil)
	for _, tc := range []struct {
		name   string
		header string
		want   int
	}{
		{name: "missing", want: http.StatusUnauthorized},
		{name: "wrong", header: "Bearer wrong-secret", want: http.StatusUnauthorized},
		{name: "valid", header: "Bearer master-secret", want: http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/admin/providers", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != tc.want {
				t.Fatalf("status=%d want=%d body=%s", res.Code, tc.want, res.Body.String())
			}
		})
	}
}

func TestUpstreamErrorBodyNeverLeaksCredentialToClient(t *testing.T) {
	const secret = "sk-provider-secret-must-not-leak"
	p := &securityTestProvider{id: "secret-error", generate: func(context.Context, *provider.Request) (*provider.Response, error) {
		return nil, errors.New("upstream HTTP 401: Authorization: Bearer " + secret)
	}}
	registry := provider.NewRegistry()
	if err := registry.Register(p); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(registry, "", nil)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"model-test","messages":[{"role":"user","content":"hi"}]}`))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), secret) || strings.Contains(res.Body.String(), "Authorization") {
		t.Fatalf("upstream credential details leaked in response: %s", res.Body.String())
	}
}

func TestDecodeJSONRequestRejectsUnknownAndTrailingValues(t *testing.T) {
	for _, body := range []string{
		`{"name":"valid","unexpected":true}`,
		`{"name":"valid"} {"name":"second"}`,
	} {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		res := httptest.NewRecorder()
		var input struct {
			Name string `json:"name"`
		}
		if err := decodeJSONRequest(res, req, &input, 1024); err == nil {
			t.Fatalf("expected strict JSON rejection for %q", body)
		}
	}
}

func TestDecodeJSONRequestEnforcesBodyLimit(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"`+strings.Repeat("x", 128)+`"}`))
	res := httptest.NewRecorder()
	var input struct {
		Name string `json:"name"`
	}
	if err := decodeJSONRequest(res, req, &input, 32); err == nil {
		t.Fatal("expected oversized JSON body to be rejected")
	}
}

func TestStreamingRequestCancellationPropagatesToProvider(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	p := &securityTestProvider{id: "cancel-provider", stream: func(ctx context.Context, _ *provider.Request, _ func(provider.Event) error) error {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return ctx.Err()
	}}
	registry := provider.NewRegistry()
	if err := registry.Register(p); err != nil {
		t.Fatal(err)
	}
	h := &Handler{registry: registry, metrics: nil, mux: http.NewServeMux()}
	h.mux.Handle("POST /v1/chat/completions", h.protect(http.HandlerFunc(h.chatCompletions)))
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"model-test","stream":true}`)).WithContext(ctx)
	res := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		h.mux.ServeHTTP(res, req)
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("provider stream did not start")
	}
	cancel()
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("request cancellation was not propagated to provider")
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return after stream cancellation")
	}
}

type failoverAcceptanceSelector struct {
	providers []provider.Provider
	failed    map[string]bool
}

func (s *failoverAcceptanceSelector) Select(ctx context.Context, model string) (provider.Provider, error) {
	return s.SelectWithRequirements(ctx, model, nil)
}
func (s *failoverAcceptanceSelector) SelectWithRequirements(ctx context.Context, model string, required []string) (provider.Provider, error) {
	for _, candidate := range s.providers {
		if s.failed[candidate.ID()] {
			continue
		}
		models, err := candidate.ListModels(ctx)
		if err != nil {
			continue
		}
		for _, item := range models {
			if item.ID == model && hasDeclaredCapabilities(item.Capabilities, required) {
				return candidate, nil
			}
		}
	}
	return nil, fmt.Errorf("no eligible provider")
}
func (s *failoverAcceptanceSelector) ReportFailure(id string) {
	if s.failed == nil {
		s.failed = make(map[string]bool)
	}
	s.failed[id] = true
}

func TestNonStreamingFailureRetriesDistinctEligibleProvider(t *testing.T) {
	first := &securityTestProvider{id: "first", generate: func(context.Context, *provider.Request) (*provider.Response, error) {
		return nil, errors.New("simulated upstream failure")
	}}
	second := &securityTestProvider{id: "second"}
	registry := provider.NewRegistry()
	if err := registry.Register(first); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(second); err != nil {
		t.Fatal(err)
	}
	selector := &failoverAcceptanceSelector{providers: []provider.Provider{first, second}, failed: make(map[string]bool)}
	h := &Handler{registry: registry, selector: selector}
	response, selected, err := h.generateWithFallback(context.Background(), first, "model-test", []string{"chat"}, scheduler.AdaptiveProfile{}, []byte(`{"model":"model-test"}`))
	if err != nil {
		t.Fatalf("fallback failed: %v", err)
	}
	if selected.ID() != "second" || len(response.Output) == 0 {
		t.Fatalf("selected=%q response=%q; want second provider response", selected.ID(), response.Output)
	}
	if !selector.failed["first"] {
		t.Fatal("first provider failure was not reported to selector")
	}
}

func TestScopedAPIKeyCannotCrossRouteScopes(t *testing.T) {
	ctx := context.Background()
	runtime, err := storage.OpenSQLiteRuntime(ctx, t.TempDir()+"/api-keys.db", storage.SQLiteOptions{WAL: true})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	keyRepo, err := apikey.NewRepository(runtime.Store().DB())
	if err != nil {
		t.Fatal(err)
	}
	_, secret, err := keyRepo.Create(ctx, "admin-read", "Admin read only", []string{apikey.ScopeAdminRead})
	if err != nil {
		t.Fatal(err)
	}
	registry := provider.NewRegistry()
	handler := NewHandlerWithFullRepositories(registry, "master-secret", nil, nil, nil, nil, nil, nil, keyRepo)
	for _, tc := range []struct {
		name   string
		method string
		path   string
		want   int
	}{
		{name: "admin read", method: http.MethodGet, path: "/admin/providers", want: http.StatusOK},
		{name: "gateway read denied", method: http.MethodGet, path: "/v1/models", want: http.StatusForbidden},
		{name: "admin write denied", method: http.MethodPost, path: "/admin/providers", want: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{}`))
			req.Header.Set("Authorization", "Bearer "+secret)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != tc.want {
				t.Fatalf("status=%d want=%d body=%s", res.Code, tc.want, res.Body.String())
			}
		})
	}
}
