package gateway

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"omnirouter/internal/provider"
)

type failingNativeAnthropicTestProvider struct {
	*nativeAnthropicTestProvider
}

func (p *failingNativeAnthropicTestProvider) GenerateAnthropic(context.Context, *provider.Request) (*provider.Response, error) {
	return nil, fmt.Errorf("simulated Anthropic upstream failure")
}

func TestNativeAnthropicNonStreamingRetriesCapabilityCompatibleProvider(t *testing.T) {
	first := &failingNativeAnthropicTestProvider{
		nativeAnthropicTestProvider: &nativeAnthropicTestProvider{
			usageTestProvider: &usageTestProvider{id: "anthropic-failing", capabilities: []string{"anthropic_messages"}},
		},
	}
	second := &nativeAnthropicTestProvider{
		usageTestProvider: &usageTestProvider{id: "anthropic-backup", capabilities: []string{"anthropic_messages"}},
	}
	registry := provider.NewRegistry()
	if err := registry.Register(first); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(second); err != nil {
		t.Fatal(err)
	}
	selector := &nativeFallbackSelectorTest{providers: []provider.Provider{first, second}}
	h := &Handler{registry: registry, selector: selector}

	response, selected, err := h.generateNativeWithFallback(
		context.Background(),
		first,
		"model-test",
		[]string{"anthropic_messages"},
		func(candidate provider.Provider) (*provider.Response, error) {
			anthropicProvider, ok := candidate.(provider.AnthropicProvider)
			if !ok {
				return nil, fmt.Errorf("provider %q does not implement AnthropicProvider", candidate.ID())
			}
			return anthropicProvider.GenerateAnthropic(context.Background(), &provider.Request{Model: "model-test"})
		},
	)
	if err != nil {
		t.Fatalf("native Anthropic fallback failed: %v", err)
	}
	if selected.ID() != second.ID() || !strings.Contains(string(response.Output), "\"text\":\"native\"") {
		t.Fatalf("selected=%q response=%s, want backup Anthropic provider", selected.ID(), response.Output)
	}
	if !selector.failed[first.ID()] {
		t.Fatal("failed Anthropic provider was not reported to selector")
	}
}

type failingNativeAnthropicStreamProvider struct {
	*nativeAnthropicTestProvider
}

func (p *failingNativeAnthropicStreamProvider) StreamAnthropic(context.Context, *provider.Request, func(provider.Event) error) error {
	return fmt.Errorf("simulated Anthropic stream interruption")
}

type countedNativeAnthropicStreamProvider struct {
	*nativeAnthropicTestProvider
	streamCalls int
}

func (p *countedNativeAnthropicStreamProvider) StreamAnthropic(ctx context.Context, req *provider.Request, emit func(provider.Event) error) error {
	p.streamCalls++
	return p.nativeAnthropicTestProvider.StreamAnthropic(ctx, req, emit)
}

func TestNativeAnthropicStreamingFailureDoesNotRetryAfterHeaders(t *testing.T) {
	first := &failingNativeAnthropicStreamProvider{
		nativeAnthropicTestProvider: &nativeAnthropicTestProvider{
			usageTestProvider: &usageTestProvider{id: "anthropic-stream-failing", capabilities: []string{"anthropic_messages", "streaming"}},
		},
	}
	second := &countedNativeAnthropicStreamProvider{
		nativeAnthropicTestProvider: &nativeAnthropicTestProvider{
			usageTestProvider: &usageTestProvider{id: "anthropic-stream-backup", capabilities: []string{"anthropic_messages", "streaming"}},
		},
	}
	registry := provider.NewRegistry()
	if err := registry.Register(first); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(second); err != nil {
		t.Fatal(err)
	}
	selector := &nativeFallbackSelectorTest{providers: []provider.Provider{first, second}}
	handler := NewHandlerWithFullRepositories(registry, "", selector, nil, nil, nil, nil, nil, nil)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(
		`{"model":"model-test","max_tokens":32,"stream":true,"messages":[{"role":"user","content":"hello"}]}`,
	))
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK || !strings.Contains(response.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("expected an established SSE response, status=%d headers=%v", response.Code, response.Header())
	}
	if second.streamCalls != 0 {
		t.Fatalf("streaming request was retried on backup after response headers: calls=%d", second.streamCalls)
	}
	if !selector.failed[first.ID()] {
		t.Fatal("failed streaming provider was not reported to selector")
	}
}
