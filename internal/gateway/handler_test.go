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
	stream       func(context.Context, *provider.Request, func(provider.Event) error) error
	capabilities []string
}

func (p *usageTestProvider) ID() string   { return "provider-test" }
func (p *usageTestProvider) Name() string { return "test" }
func (p *usageTestProvider) ListModels(context.Context) ([]provider.Model, error) {
	return []provider.Model{{ID: "model-test", Provider: p.ID(), Capabilities: append([]string(nil), p.capabilities...)}}, nil
}
func (p *usageTestProvider) Generate(context.Context, *provider.Request) (*provider.Response, error) {
	return &provider.Response{Model: "model-test", Output: []byte(`{"id":"x","choices":[],"usage":{"prompt_tokens":7,"completion_tokens":5,"total_tokens":12}}`)}, nil
}
func (p *usageTestProvider) Stream(ctx context.Context, req *provider.Request, emit func(provider.Event) error) error {
	return p.stream(ctx, req, emit)
}

func (p *usageTestProvider) GenerateResponses(context.Context, *provider.Request) (*provider.Response, error) {
	return &provider.Response{Model: "model-test", Output: []byte(`{"id":"resp_1","status":"completed","usage":{"input_tokens":4,"output_tokens":6,"total_tokens":10}}`)}, nil
}

func (p *usageTestProvider) StreamResponses(_ context.Context, _ *provider.Request, emit func(provider.Event) error) error {
	return emit(provider.Event{Data: []byte("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":2,\"output_tokens\":3,\"total_tokens\":5}}}\n\n"), Done: true, ExplicitDone: true})
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

func TestGatewayResponsesAPIRecordsUsage(t *testing.T) {
	p := &usageTestProvider{capabilities: []string{"responses"}}
	registry := provider.NewRegistry()
	if err := registry.Register(p); err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithFullRepositories(registry, "", nil, nil, nil, nil, nil, nil, nil)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"model-test","input":"hello"}`))
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"status":"completed"`) {
		t.Fatalf("unexpected body: %s", response.Body.String())
	}
}

func TestGatewayResponsesStreamingPreservesSemanticTerminal(t *testing.T) {
	p := &usageTestProvider{capabilities: []string{"responses"}}
	registry := provider.NewRegistry()
	if err := registry.Register(p); err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithFullRepositories(registry, "", nil, nil, nil, nil, nil, nil, nil)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"model-test","input":"hello","stream":true}`))
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "event: response.completed") {
		t.Fatalf("semantic terminal event was not forwarded: %s", response.Body.String())
	}
}

func TestRequiredCapabilitiesIncludesDeepSeekReasoningMode(t *testing.T) {
	got := requiredCapabilities([]byte(`{"model":"deepseek-flash","thinking":{"type":"enabled"},"reasoning_effort":"high","stream":true}`))
	found := map[string]bool{}
	for _, capability := range got {
		found[capability] = true
	}
	for _, capability := range []string{"reasoning", "streaming"} {
		if !found[capability] {
			t.Errorf("missing required capability %q in %v", capability, got)
		}
	}
}

type nativeAnthropicTestProvider struct {
	*usageTestProvider
}

func (p *nativeAnthropicTestProvider) GenerateAnthropic(context.Context, *provider.Request) (*provider.Response, error) {
	return &provider.Response{Model: "model-test", Output: []byte(`{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"native"}],"usage":{"input_tokens":3,"output_tokens":4}}`)}, nil
}

func (p *nativeAnthropicTestProvider) StreamAnthropic(_ context.Context, _ *provider.Request, emit func(provider.Event) error) error {
	if err := emit(provider.Event{Data: []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":2}}}\n\n")}); err != nil {
		return err
	}
	return emit(provider.Event{Data: []byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"), Done: true, ExplicitDone: true})
}

func TestGatewayUsesNativeAnthropicProviderWhenAvailable(t *testing.T) {
	p := &nativeAnthropicTestProvider{usageTestProvider: &usageTestProvider{capabilities: []string{"anthropic_messages"}}}
	registry := provider.NewRegistry()
	if err := registry.Register(p); err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithFullRepositories(registry, "", nil, nil, nil, nil, nil, nil, nil)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"model-test","max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`))
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"text":"native"`) {
		t.Fatalf("native Anthropic response was not forwarded: %s", response.Body.String())
	}
}

func TestGatewayStreamsNativeAnthropicEvents(t *testing.T) {
	p := &nativeAnthropicTestProvider{usageTestProvider: &usageTestProvider{capabilities: []string{"anthropic_messages", "streaming"}}}
	registry := provider.NewRegistry()
	if err := registry.Register(p); err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithFullRepositories(registry, "", nil, nil, nil, nil, nil, nil, nil)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"model-test","max_tokens":32,"stream":true,"messages":[{"role":"user","content":"hello"}]}`))
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "event: message_stop") {
		t.Fatalf("native message_stop was not forwarded: %s", response.Body.String())
	}
}

type nativeGeminiTestProvider struct {
	*usageTestProvider
}

func (p *nativeGeminiTestProvider) GenerateGemini(context.Context, *provider.Request) (*provider.Response, error) {
	return &provider.Response{Model: "model-test", Output: []byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"native gemini"}]}}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":4,"totalTokenCount":7}}`)}, nil
}

func (p *nativeGeminiTestProvider) StreamGemini(_ context.Context, _ *provider.Request, emit func(provider.Event) error) error {
	if err := emit(provider.Event{Data: []byte("data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"native gemini\"}]}}]}\n\n")}); err != nil {
		return err
	}
	return emit(provider.Event{Done: true, ExplicitDone: true})
}

func TestGatewayUsesNativeGeminiProviderWhenAvailable(t *testing.T) {
	p := &nativeGeminiTestProvider{usageTestProvider: &usageTestProvider{capabilities: []string{"gemini_generate_content"}}}
	registry := provider.NewRegistry()
	if err := registry.Register(p); err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithFullRepositories(registry, "", nil, nil, nil, nil, nil, nil, nil)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1beta/models/model-test:generateContent", strings.NewReader(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`))
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "native gemini") {
		t.Fatalf("native Gemini response was not forwarded: %s", response.Body.String())
	}
}

func TestGatewayStreamsNativeGeminiEvents(t *testing.T) {
	p := &nativeGeminiTestProvider{usageTestProvider: &usageTestProvider{capabilities: []string{"gemini_generate_content", "streaming"}}}
	registry := provider.NewRegistry()
	if err := registry.Register(p); err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithFullRepositories(registry, "", nil, nil, nil, nil, nil, nil, nil)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1beta/models/model-test:streamGenerateContent", strings.NewReader(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`))
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "native gemini") || !strings.Contains(response.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("native Gemini SSE was not forwarded: headers=%v body=%s", response.Header(), response.Body.String())
	}
}

func TestRequiredCapabilitiesIncludesGeminiSpecificFeatures(t *testing.T) {
	got := requiredCapabilities([]byte(`{
		"generationConfig": {
			"responseMimeType": "application/json",
			"thinkingConfig": {"thinkingBudget": 1024}
		},
		"tools": [{"functionDeclarations": [{"name": "lookup"}]}],
		"contents": [{"parts": [{"inlineData": {"mimeType": "image/png", "data": "AA=="}}]}]
	}`))
	found := map[string]bool{}
	for _, capability := range got {
		found[capability] = true
	}
	for _, capability := range []string{"json", "reasoning", "tools", "vision"} {
		if !found[capability] {
			t.Errorf("missing Gemini required capability %q in %v", capability, got)
		}
	}
}

func TestRequiredCapabilitiesDoesNotTreatAudioOrVideoAsVision(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{name: "inline image", body: `{"contents":[{"parts":[{"inlineData":{"mimeType":"image/png","data":"AA=="}}]}]}`, want: true},
		{name: "uploaded image", body: `{"contents":[{"parts":[{"fileData":{"mimeType":"image/jpeg","fileUri":"gs://bucket/image.jpg"}}]}]}`, want: true},
		{name: "inline audio", body: `{"contents":[{"parts":[{"inlineData":{"mimeType":"audio/ogg","data":"AA=="}}]}]}`},
		{name: "uploaded video", body: `{"contents":[{"parts":[{"fileData":{"mimeType":"video/mp4","fileUri":"gs://bucket/video.mp4"}}]}]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := requiredCapabilities([]byte(test.body))
			found := false
			for _, capability := range got {
				if capability == "vision" {
					found = true
				}
			}
			if found != test.want {
				t.Fatalf("vision capability = %v, want %v; all capabilities: %v", found, test.want, got)
			}
		})
	}
}

type legacySelectorTest struct {
	selected provider.Provider
}

func (s *legacySelectorTest) Select(context.Context, string) (provider.Provider, error) {
	return s.selected, nil
}
func (s *legacySelectorTest) ReportFailure(string) {}

func TestSelectProviderForWithoutSelectorRequiresDeclaredCapabilities(t *testing.T) {
	p := &usageTestProvider{capabilities: []string{"streaming"}}
	registry := provider.NewRegistry()
	if err := registry.Register(p); err != nil {
		t.Fatal(err)
	}
	h := &Handler{registry: registry}
	if _, err := h.selectProviderFor(context.Background(), "model-test", []string{"gemini_generate_content"}); err == nil {
		t.Fatal("provider without required capability was selected")
	}
	selected, err := h.selectProviderFor(context.Background(), "model-test", []string{"streaming"})
	if err != nil {
		t.Fatalf("provider declaring required capability was rejected: %v", err)
	}
	if selected.ID() != p.ID() {
		t.Fatalf("selected provider %q, want %q", selected.ID(), p.ID())
	}
}

func TestSelectProviderForRejectsLegacySelectorForCapabilityConstrainedRequest(t *testing.T) {
	p := &usageTestProvider{capabilities: []string{"streaming"}}
	h := &Handler{selector: &legacySelectorTest{selected: p}}
	if _, err := h.selectProviderFor(context.Background(), "model-test", []string{"vision"}); err == nil {
		t.Fatal("legacy selector silently bypassed capability requirements")
	}
	selected, err := h.selectProviderFor(context.Background(), "model-test", nil)
	if err != nil {
		t.Fatalf("legacy selector should remain usable without capability requirements: %v", err)
	}
	if selected.ID() != p.ID() {
		t.Fatalf("selected provider %q, want %q", selected.ID(), p.ID())
	}
}

type nativeFallbackSelectorTest struct {
	providers []provider.Provider
	failed    map[string]bool
}

func (s *nativeFallbackSelectorTest) Select(_ context.Context, model string) (provider.Provider, error) {
	return s.SelectWithRequirements(context.Background(), model, nil)
}
func (s *nativeFallbackSelectorTest) SelectWithRequirements(_ context.Context, _ string, required []string) (provider.Provider, error) {
	for _, candidate := range s.providers {
		if s.failed[candidate.ID()] {
			continue
		}
		models, err := candidate.ListModels(context.Background())
		if err != nil {
			continue
		}
		for _, model := range models {
			if hasDeclaredCapabilities(model.Capabilities, required) {
				return candidate, nil
			}
		}
	}
	return nil, fmt.Errorf("no eligible provider")
}
func (s *nativeFallbackSelectorTest) ReportFailure(id string) {
	if s.failed == nil {
		s.failed = make(map[string]bool)
	}
	s.failed[id] = true
}

type failingNativeGeminiTestProvider struct {
	*nativeGeminiTestProvider
}

func (p *failingNativeGeminiTestProvider) GenerateGemini(context.Context, *provider.Request) (*provider.Response, error) {
	return nil, fmt.Errorf("simulated upstream failure")
}

func TestNativeGeminiNonStreamingRetriesAnotherCapabilityCompatibleProvider(t *testing.T) {
	first := &failingNativeGeminiTestProvider{nativeGeminiTestProvider: &nativeGeminiTestProvider{usageTestProvider: &usageTestProvider{capabilities: []string{"gemini_generate_content"}}}}
	second := &nativeGeminiTestProvider{usageTestProvider: &usageTestProvider{capabilities: []string{"gemini_generate_content"}}}
	registry := provider.NewRegistry()
	if err := registry.Register(first); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(second); err != nil {
		t.Fatal(err)
	}
	selector := &nativeFallbackSelectorTest{providers: []provider.Provider{first, second}}
	h := &Handler{registry: registry, selector: selector}
	response, selected, err := h.generateNativeWithFallback(context.Background(), first, "model-test", []string{"gemini_generate_content"}, func(candidate provider.Provider) (*provider.Response, error) {
		geminiProvider, ok := candidate.(provider.GeminiProvider)
		if !ok {
			return nil, fmt.Errorf("not a Gemini provider")
		}
		return geminiProvider.GenerateGemini(context.Background(), &provider.Request{Model: "model-test"})
	})
	if err != nil {
		t.Fatalf("native fallback failed: %v", err)
	}
	if selected.ID() != second.ID() || !strings.Contains(string(response.Output), "native gemini") {
		t.Fatalf("selected=%q response=%s, want second native provider", selected.ID(), response.Output)
	}
	if !selector.failed[first.ID()] {
		t.Fatal("failed native provider was not reported to selector")
	}
}
