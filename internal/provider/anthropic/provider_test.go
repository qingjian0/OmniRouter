package anthropic

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"omnirouter/internal/provider"
)

func TestGenerateAnthropicUsesNativeHeadersAndPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "secret" || r.Header.Get("anthropic-version") != DefaultVersion {
			t.Fatalf("native auth/version headers missing")
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"max_tokens":32`) {
			t.Fatalf("payload was not preserved: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"type":"message","content":[]}`)
	}))
	defer server.Close()
	p, err := New(Config{ID: "anthropic", BaseURL: server.URL + "/v1", APIKey: "secret", Models: []string{"claude-test"}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.GenerateAnthropic(context.Background(), &provider.Request{Model: "claude-test", Input: []byte(`{"model":"claude-test","max_tokens":32,"messages":[{"role":"user","content":"hi"}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Output) != `{"type":"message","content":[]}` {
		t.Fatalf("unexpected response: %s", got.Output)
	}
}

func TestGenerateAnthropicRejectsInvalidResponseEnvelope(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "malformed JSON", body: "{not-json"},
		{name: "unexpected type", body: `{"type":"error","error":{"type":"api_error"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			p, err := New(Config{ID: "anthropic", BaseURL: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			_, err = p.GenerateAnthropic(context.Background(), &provider.Request{
				Model: "claude-test",
				Input: []byte(`{"model":"claude-test","max_tokens":8}`),
			})
			if err == nil {
				t.Fatal("expected invalid response envelope to be rejected")
			}
		})
	}
}

func TestStreamAnthropicRequiresMessageStop(t *testing.T) {
	for _, tc := range []struct {
		name    string
		body    string
		wantErr bool
	}{
		{"complete", "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", false},
		{"truncated", "event: content_block_delta\ndata: {\"type\":\"content_block_delta\"}\n\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Accept") != "application/json, text/event-stream" {
					t.Fatalf("unexpected Accept header: %q", r.Header.Get("Accept"))
				}
				requestBody, _ := io.ReadAll(r.Body)
				if !strings.Contains(string(requestBody), `"stream":true`) {
					t.Fatalf("streaming request must set stream=true: %s", requestBody)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			p, err := New(Config{ID: "anthropic", BaseURL: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			err = p.StreamAnthropic(context.Background(), &provider.Request{Model: "m", Input: []byte(`{"model":"m","max_tokens":8}`)}, func(provider.Event) error { return nil })
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestStreamAnthropicRejectsNonSSEContentType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"type":"message","content":[]}`)
	}))
	defer server.Close()
	p, err := New(Config{ID: "anthropic", BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	err = p.StreamAnthropic(context.Background(), &provider.Request{
		Model: "m", Input: []byte(`{"model":"m","max_tokens":8}`),
	}, func(provider.Event) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "unexpected content type") {
		t.Fatalf("expected non-SSE content type error, got %v", err)
	}
}

func TestListModelsDeclaresOnlyNativeAnthropicCapability(t *testing.T) {
	p, err := New(Config{ID: "a", Models: []string{"claude-test"}})
	if err != nil {
		t.Fatal(err)
	}
	models, err := p.ListModels(context.Background())
	if err != nil || len(models) != 1 {
		t.Fatalf("models=%v err=%v", models, err)
	}
	for _, capability := range models[0].Capabilities {
		if capability == "chat" {
			t.Fatal("native-only adapter must not claim OpenAI chat")
		}
	}
}

func TestNewRejectsInvalidBaseURL(t *testing.T) {
	if _, err := New(Config{ID: "a", BaseURL: "file:///tmp"}); err == nil {
		t.Fatal("expected invalid URL error")
	}
}

func TestGenerateAnthropicRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, strings.NewReader(strings.Repeat("x", (32<<20)+1)))
	}))
	defer server.Close()

	p, err := New(Config{ID: "anthropic", BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.GenerateAnthropic(context.Background(), &provider.Request{
		Model: "m",
		Input: []byte(`{"model":"m","max_tokens":8}`),
	})
	if err == nil || !strings.Contains(err.Error(), "exceeds 32 MiB") {
		t.Fatalf("expected oversized response error, got %v", err)
	}
}

func TestGenerateAnthropicDoesNotExposeUpstreamErrorBody(t *testing.T) {
	const privateDetail = "upstream-private-prompt-or-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, privateDetail)
	}))
	defer server.Close()
	p, err := New(Config{ID: "anthropic", BaseURL: server.URL})
	if err != nil { t.Fatal(err) }
	_, err = p.GenerateAnthropic(context.Background(), &provider.Request{
		Model: "m", Input: []byte(`{"model":"m","max_tokens":8}`),
	})
	if err == nil || !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("expected sanitized HTTP status error, got %v", err)
	}
	if strings.Contains(err.Error(), privateDetail) {
		t.Fatal("upstream error body leaked through provider error")
	}
}
