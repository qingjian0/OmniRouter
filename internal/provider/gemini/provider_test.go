package gemini

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"omnirouter/internal/provider"
)

func TestGenerateGeminiUsesNativeEndpointAndHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1beta/models/gemini-test:generateContent" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		if r.Header.Get("x-goog-api-key") != "secret" || r.Header.Get("Content-Type") != "application/json" {
			t.Fatal("Gemini native headers missing")
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"contents"`) || !strings.Contains(string(body), `"generationConfig"`) {
			t.Fatalf("native request fields were not preserved: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"hello"}]}}]}`)
	}))
	defer server.Close()

	p, err := New(Config{ID: "gemini", BaseURL: server.URL + "/v1beta", APIKey: "secret", Models: []string{"gemini-test"}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.GenerateGemini(context.Background(), &provider.Request{
		Model: "gemini-test",
		Input: []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"maxOutputTokens":32}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got.Output), `"text":"hello"`) {
		t.Fatalf("unexpected response: %s", got.Output)
	}
}

func TestStreamGeminiForwardsNativeSSEAndCompletesOnCleanEOF(t *testing.T) {
	const frame = "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"hello\"}]}}]}\n\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/models/gemini-test:streamGenerateContent" || r.URL.Query().Get("alt") != "sse" {
			t.Fatalf("unexpected streaming endpoint: %s", r.URL.String())
		}
		if r.Header.Get("Accept") != "text/event-stream" {
			t.Fatal("streaming Accept header missing")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, frame)
	}))
	defer server.Close()

	p, err := New(Config{ID: "gemini", BaseURL: server.URL + "/v1beta"})
	if err != nil {
		t.Fatal(err)
	}
	var frames []string
	var terminal bool
	err = p.StreamGemini(context.Background(), &provider.Request{
		Model: "gemini-test", Input: []byte(`{"contents":[{"parts":[{"text":"hi"}]}]}`), Stream: true,
	}, func(event provider.Event) error {
		if len(event.Data) > 0 {
			frames = append(frames, string(event.Data))
		}
		if event.Done {
			terminal = event.ExplicitDone
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 1 || frames[0] != frame || !terminal {
		t.Fatalf("unexpected stream frames=%q terminal=%v", frames, terminal)
	}
}

func TestStreamGeminiRejectsErrorEnvelope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"error\":{\"message\":\"bad request\"}}\n\n")
	}))
	defer server.Close()

	p, err := New(Config{ID: "gemini", BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	err = p.StreamGemini(context.Background(), &provider.Request{
		Model: "m", Input: []byte(`{"contents":[{"parts":[{"text":"hi"}]}]}`), Stream: true,
	}, func(provider.Event) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "Gemini API returned an error response") {
		t.Fatalf("expected Gemini stream error, got %v", err)
	}
}

func TestListModelsDeclaresOnlyNativeGeminiCapability(t *testing.T) {
	p, err := New(Config{ID: "gemini", Models: []string{"gemini-test"}})
	if err != nil {
		t.Fatal(err)
	}
	models, err := p.ListModels(context.Background())
	if err != nil || len(models) != 1 {
		t.Fatalf("models=%v err=%v", models, err)
	}
	for _, capability := range models[0].Capabilities {
		if capability == "chat" {
			t.Fatal("native-only Gemini adapter must not claim OpenAI chat")
		}
	}
	if len(models[0].APICapabilities) != 1 || models[0].APICapabilities[0] != "gemini_generate_content" {
		t.Fatalf("unexpected API capabilities: %v", models[0].APICapabilities)
	}
}

func TestNewRejectsInvalidModelID(t *testing.T) {
	if _, err := New(Config{ID: "gemini", Models: []string{"models/gemini-test"}}); err == nil {
		t.Fatal("expected invalid model ID error")
	}
}

func TestStreamGeminiRejectsEmptySuccessfulResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
	}))
	defer server.Close()

	p, err := New(Config{ID: "gemini", BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	err = p.StreamGemini(context.Background(), &provider.Request{
		Model: "m", Input: []byte(`{"contents":[{"parts":[{"text":"hi"}]}]}`), Stream: true,
	}, func(provider.Event) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "without any response data") {
		t.Fatalf("expected empty stream error, got %v", err)
	}
}

func TestStreamGeminiRejectsNonSSEContentType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"candidates":[]}`)
	}))
	defer server.Close()

	p, err := New(Config{ID: "gemini", BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	err = p.StreamGemini(context.Background(), &provider.Request{
		Model: "m", Input: []byte(`{"contents":[{"parts":[{"text":"hi"}]}]}`), Stream: true,
	}, func(provider.Event) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "unexpected content type") {
		t.Fatalf("expected non-SSE content type error, got %v", err)
	}
}

func TestHealthCheckUsesGeminiModelsEndpointAndAPIKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1beta/models" || r.URL.Query().Get("pageSize") != "1" {
			t.Fatalf("unexpected health request: %s %s", r.Method, r.URL.String())
		}
		if r.Header.Get("x-goog-api-key") != "secret" {
			t.Fatal("Gemini API key header missing from health check")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	p, err := New(Config{ID: "gemini", BaseURL: server.URL + "/v1beta", APIKey: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.HealthCheck(context.Background()); err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
}

func TestHealthCheckRejectsUnauthorizedUpstream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	p, err := New(Config{ID: "gemini", BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.HealthCheck(context.Background()); err == nil {
		t.Fatal("expected unauthorized health check error")
	}
}

func TestNewRejectsUnsafeBaseURL(t *testing.T) {
	for _, baseURL := range []string{
		"ftp://example.com/v1beta",
		"https://user:password@example.com/v1beta",
		"https://example.com/v1beta?key=secret",
		"https://example.com/v1beta#fragment",
	} {
		if _, err := New(Config{ID: "gemini", BaseURL: baseURL}); err == nil {
			t.Errorf("expected invalid base URL %q to be rejected", baseURL)
		}
	}
}

func TestGenerateGeminiRejectsNonJSONContentType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, `{"candidates":[]}`)
	}))
	defer server.Close()

	p, err := New(Config{ID: "gemini", BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.GenerateGemini(context.Background(), &provider.Request{
		Model: "m", Input: []byte(`{"contents":[{"parts":[{"text":"hi"}]}]}`),
	})
	if err == nil || !strings.Contains(err.Error(), "unexpected content type") {
		t.Fatalf("expected non-JSON content type error, got %v", err)
	}
}

func TestGenerateGeminiDoesNotExposeUpstreamErrorBody(t *testing.T) {
	const privateDetail = "upstream-private-prompt-or-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, privateDetail)
	}))
	defer server.Close()
	p, err := New(Config{ID: "gemini", BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.GenerateGemini(context.Background(), &provider.Request{
		Model: "m", Input: []byte(`{"contents":[{"parts":[{"text":"hi"}]}]}`),
	})
	if err == nil || !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("expected sanitized HTTP status error, got %v", err)
	}
	if strings.Contains(err.Error(), privateDetail) {
		t.Fatal("upstream error body leaked through provider error")
	}
}
