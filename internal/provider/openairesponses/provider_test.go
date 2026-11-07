package openairesponses

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"omnirouter/internal/provider"
)

func TestGenerateResponsesUsesNativeEndpointAndBearerAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" { t.Fatalf("path=%q", r.URL.Path) }
		if r.Header.Get("Authorization") != "Bearer secret" { t.Fatalf("missing bearer auth") }
		if r.Header.Get("Accept") != "application/json" { t.Fatalf("accept=%q", r.Header.Get("Accept")) }
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_1","status":"completed"}`))
	}))
	defer server.Close()
	p, err := New(Config{ID:"responses", BaseURL:server.URL+"/v1", APIKey:"secret", Models:[]string{"gpt-x"}, Client:server.Client()})
	if err != nil { t.Fatal(err) }
	got, err := p.GenerateResponses(context.Background(), &provider.Request{Model:"gpt-x", Input:[]byte(`{"model":"gpt-x","input":"hello"}`)})
	if err != nil { t.Fatal(err) }
	if !strings.Contains(string(got.Output), "resp_1") { t.Fatalf("unexpected output: %s", got.Output) }
}

func TestListModelsDeclaresOnlyNativeResponsesCapabilities(t *testing.T) {
	p, err := New(Config{ID:"responses", BaseURL:"https://example.test/v1", Models:[]string{"gpt-x"}, ModelCapabilities:map[string][]string{"gpt-x":{"tools","vision"}}})
	if err != nil { t.Fatal(err) }
	models, err := p.ListModels(context.Background())
	if err != nil { t.Fatal(err) }
	if len(models) != 1 { t.Fatalf("models=%+v", models) }
	for _, cap := range []string{"responses","streaming","tools","vision"} {
		if !contains(models[0].Capabilities, cap) { t.Fatalf("missing capability %q: %+v", cap, models[0]) }
	}
	if contains(models[0].Capabilities, "chat") { t.Fatalf("native Responses adapter must not claim chat: %+v", models[0]) }
	if len(models[0].APICapabilities) != 1 || models[0].APICapabilities[0] != "openai_responses" { t.Fatalf("api capabilities=%v", models[0].APICapabilities) }
}

func TestStreamRequiresResponseCompleted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" { t.Fatalf("path=%q", r.URL.Path) }
		if r.Header.Get("Accept") != "text/event-stream" { t.Fatalf("accept=%q", r.Header.Get("Accept")) }
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		_, _ = w.Write([]byte("event: response.created\ndata: {\"type\":\"response.created\"}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\"}\n\n"))
	}))
	defer server.Close()
	p, err := New(Config{ID:"responses", BaseURL:server.URL, Client:server.Client()})
	if err != nil { t.Fatal(err) }
	var events []provider.Event
	err = p.StreamResponses(context.Background(), &provider.Request{Model:"gpt-x", Input:[]byte(`{"model":"gpt-x"}`)}, func(e provider.Event) error { events=append(events,e); return nil })
	if err != nil { t.Fatal(err) }
	if len(events) != 2 || !events[1].Done || !events[1].ExplicitDone { t.Fatalf("events=%+v", events) }
}

func TestStreamEOFWithoutTerminalFailsClosed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: response.created\ndata: {\"type\":\"response.created\"}\n\n"))
	}))
	defer server.Close()
	p, err := New(Config{ID:"responses", BaseURL:server.URL, Client:server.Client()})
	if err != nil { t.Fatal(err) }
	err = p.StreamResponses(context.Background(), &provider.Request{Model:"gpt-x", Input:[]byte(`{"model":"gpt-x"}`)}, func(provider.Event) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "without response.completed") { t.Fatalf("err=%v", err) }
}

func TestStreamRejectsNonSSEContentType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"type":"response.completed"}`))
	}))
	defer server.Close()
	p, err := New(Config{ID:"responses", BaseURL:server.URL, Client:server.Client()})
	if err != nil { t.Fatal(err) }
	err = p.StreamResponses(context.Background(), &provider.Request{Model:"gpt-x", Input:[]byte(`{"model":"gpt-x"}`)}, func(provider.Event) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "non-SSE content type") { t.Fatalf("err=%v", err) }
}

func TestChatCompletionsAreExplicitlyUnsupported(t *testing.T) {
	p, err := New(Config{ID:"responses", BaseURL:"https://example.test"})
	if err != nil { t.Fatal(err) }
	if _, err := p.Generate(context.Background(), &provider.Request{}); err == nil { t.Fatal("expected unsupported operation") }
}

func contains(values []string, wanted string) bool {
	for _, value := range values { if value == wanted { return true } }
	return false
}
