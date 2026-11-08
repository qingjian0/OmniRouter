package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"omnirouter/internal/provider"
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
