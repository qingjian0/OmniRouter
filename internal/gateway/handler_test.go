package gateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	"omnirouter/internal/observability"
	"omnirouter/internal/provider"
	"testing"
	"time"
)

func TestClassifyRequestError(t *testing.T) {
	t.Run("downstream write failure is client disconnect", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
		got := classifyRequestError(req, downstreamWriteError{err: io.ErrClosedPipe}, true)
		if got != "client_disconnected" {
			t.Fatalf("got %q, want client_disconnected", got)
		}
	})
	t.Run("request cancellation is client disconnect", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		req := httptest.NewRequest("POST", "/v1/chat/completions", nil).WithContext(ctx)
		if got := classifyRequestError(req, errors.New("stream stopped"), true); got != "client_disconnected" {
			t.Fatalf("got %q, want client_disconnected", got)
		}
	})
	t.Run("deadline is timeout", func(t *testing.T) {
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()
		req := httptest.NewRequest("POST", "/v1/chat/completions", nil).WithContext(ctx)
		if got := classifyRequestError(req, errors.New("stream stopped"), true); got != "timeout" {
			t.Fatalf("got %q, want timeout", got)
		}
	})
}

type usageTestProvider struct {
	stream func(context.Context, *provider.Request, func(provider.Event) error) error
}

func (p *usageTestProvider) ID() string   { return "provider-test" }
func (p *usageTestProvider) Name() string { return "test" }
func (p *usageTestProvider) ListModels(context.Context) ([]provider.Model, error) {
	return []provider.Model{{ID: "model-test", Provider: p.ID()}}, nil
}
func (p *usageTestProvider) Generate(context.Context, *provider.Request) (*provider.Response, error) {
	return &provider.Response{Model: "model-test", Output: []byte(`{"id":"x","choices":[],"usage":{"prompt_tokens":7,"completion_tokens":5,"total_tokens":12}}`)}, nil
}
func (p *usageTestProvider) Stream(ctx context.Context, req *provider.Request, emit func(provider.Event) error) error {
	return p.stream(ctx, req, emit)
}
func (p *usageTestProvider) HealthCheck(context.Context) error { return nil }

func newUsageTestHandler(p provider.Provider) (*Handler, http.Handler) {
	registry := provider.NewRegistry()
	_ = registry.Register(p)
	recorder := observability.NewRecorder(10)
	h := &Handler{registry: registry, metrics: recorder, mux: http.NewServeMux()}
	h.mux.HandleFunc("POST /v1/chat/completions", h.chatCompletions)
	return h, recorder.Middleware(h.mux)
}

func TestGatewayPersistsProviderUsageFromNonStreamingResponse(t *testing.T) {
	p := &usageTestProvider{}
	h, handler := newUsageTestHandler(p)
	response := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"model-test"}`))
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	records := h.metrics.Recent(1)
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
	record := records[0]
	if record.ProviderID != "provider-test" || record.ModelID != "model-test" {
		t.Fatalf("route attribution missing: %+v", record)
	}
	if record.InputTokens == nil || *record.InputTokens != 7 || record.OutputTokens == nil || *record.OutputTokens != 5 || !record.UsageFinal {
		t.Fatalf("usage attribution incorrect: %+v", record)
	}
}

func TestGatewayDoesNotEmitSuccessfulTerminalMarkerForNonExplicitDone(t *testing.T) {
	p := &usageTestProvider{stream: func(_ context.Context, _ *provider.Request, emit func(provider.Event) error) error {
		if err := emit(provider.Event{Data: []byte(`data: {"choices":[{"delta":{"content":"partial"}}]}`)}); err != nil {
			return err
		}
		return emit(provider.Event{Done: true, ExplicitDone: false})
	}}
	h, handler := newUsageTestHandler(p)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"model-test","stream":true}`))
	handler.ServeHTTP(response, request)
	if strings.Contains(response.Body.String(), "[DONE]") {
		t.Fatalf("non-explicit terminal event was exposed as successful [DONE]: %s", response.Body.String())
	}
	record := h.metrics.Recent(1)[0]
	if record.LifecycleStatus != "failed" || record.ErrorCode != "stream_incomplete" || record.UsageFinal {
		t.Fatalf("non-explicit terminal event should remain incomplete: %+v", record)
	}
}

func TestGatewayPersistsFinalStreamingUsage(t *testing.T) {
	p := &usageTestProvider{stream: func(_ context.Context, _ *provider.Request, emit func(provider.Event) error) error {
		if err := emit(provider.Event{Data: []byte(`data: {"choices":[],"usage":{"prompt_tokens":9,"completion_tokens":4,"total_tokens":13}}`)}); err != nil {
			return err
		}
		return emit(provider.Event{Done: true, ExplicitDone: true})
	}}
	h, handler := newUsageTestHandler(p)
	response := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"model-test","stream":true}`))
	handler.ServeHTTP(response, request)
	record := h.metrics.Recent(1)[0]
	if record.LifecycleStatus != "completed" || !record.UsageFinal || record.InputTokens == nil || *record.InputTokens != 9 || record.OutputTokens == nil || *record.OutputTokens != 4 {
		t.Fatalf("final stream record incorrect: %+v", record)
	}
}

type brokenResponseWriter struct{ header http.Header }

func (w *brokenResponseWriter) Header() http.Header       { return w.header }
func (w *brokenResponseWriter) WriteHeader(int)           {}
func (w *brokenResponseWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func (w *brokenResponseWriter) Flush()                    {}

func TestGatewayClassifiesDownstreamStreamWriteFailure(t *testing.T) {
	p := &usageTestProvider{stream: func(_ context.Context, _ *provider.Request, emit func(provider.Event) error) error {
		return emit(provider.Event{Data: []byte(`data: {"choices":[{"delta":{"content":"hello"}}]}`)})
	}}
	h, handler := newUsageTestHandler(p)
	request := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"model-test","stream":true}`))
	handler.ServeHTTP(&brokenResponseWriter{header: make(http.Header)}, request)
	record := h.metrics.Recent(1)[0]
	if record.ErrorCode != "client_disconnected" || record.LifecycleStatus != "interrupted" {
		t.Fatalf("downstream disconnect not classified: %+v", record)
	}
}

func TestGatewayRetainsPartialUsageWhenUpstreamStreamIsInterrupted(t *testing.T) {
	p := &usageTestProvider{stream: func(_ context.Context, _ *provider.Request, emit func(provider.Event) error) error {
		if err := emit(provider.Event{Data: []byte(`data: {"choices":[],"usage":{"prompt_tokens":11,"completion_tokens":2,"total_tokens":13}}`)}); err != nil {
			return err
		}
		return io.ErrUnexpectedEOF
	}}
	h, handler := newUsageTestHandler(p)
	response := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"model-test","stream":true}`))
	handler.ServeHTTP(response, request)
	record := h.metrics.Recent(1)[0]
	if record.LifecycleStatus != "failed" || record.ErrorCode != "upstream_stream_error" || record.UsageFinal {
		t.Fatalf("interrupted stream lifecycle incorrect: %+v", record)
	}
	if record.InputTokens == nil || *record.InputTokens != 11 || record.OutputTokens == nil || *record.OutputTokens != 2 {
		t.Fatalf("partial provider usage was not retained: %+v", record)
	}
}

func TestGatewayMarksProviderUnavailableAsFailedLifecycle(t *testing.T) {
	registry := provider.NewRegistry()
	h := &Handler{registry: registry, metrics: observability.NewRecorder(10), mux: http.NewServeMux()}
	h.mux.HandleFunc("POST /v1/chat/completions", h.chatCompletions)
	handler := h.metrics.Middleware(h.mux)
	response := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"missing-model"}`))
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	records := h.metrics.Recent(1)
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
	if records[0].ErrorCode != "provider_unavailable" || records[0].LifecycleStatus != "failed" {
		t.Fatalf("provider-unavailable lifecycle incorrect: %+v", records[0])
	}
}

func TestStreamingUsageParseFailureDoesNotPersistInvalidCounts(t *testing.T) {
	p := &usageTestProvider{stream: func(_ context.Context, _ *provider.Request, emit func(provider.Event) error) error {
		if err := emit(provider.Event{Data: []byte(`data: {"choices":[],"usage":{"prompt_tokens":-9,"completion_tokens":2,"total_tokens":-7}}`)}); err != nil {
			return err
		}
		return emit(provider.Event{Done: true, ExplicitDone: true})
	}}
	h, handler := newUsageTestHandler(p)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"model-test","stream":true}`))
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	records := h.metrics.Recent(1)
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
	record := records[0]
	if record.InputTokens != nil || record.OutputTokens != nil {
		t.Fatalf("invalid token counts were retained: %+v", record)
	}
	if record.ErrorCode != "usage_parse_error" || record.LifecycleStatus != "completed" {
		t.Fatalf("usage parse error should be visible without failing inference: %+v", record)
	}
}
