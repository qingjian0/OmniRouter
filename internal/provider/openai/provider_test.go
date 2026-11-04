package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"omnirouter/internal/provider"
)

func TestStreamRequiresExplicitDone(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[]}\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()
	p, err := New(Config{ID: "test", BaseURL: server.URL, Client: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	var events []provider.Event
	err = p.Stream(context.Background(), &provider.Request{Model: "m", Input: []byte(`{"model":"m","stream":true}`), Stream: true}, func(event provider.Event) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || !events[1].Done || !events[1].ExplicitDone {
		t.Fatalf("expected explicit terminal event, got %#v", events)
	}
}

func TestStreamEOFWithoutDoneIsIncomplete(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[]}\n\n"))
	}))
	defer server.Close()
	p, err := New(Config{ID: "test", BaseURL: server.URL, Client: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	var done bool
	err = p.Stream(context.Background(), &provider.Request{Model: "m", Input: []byte(`{"model":"m","stream":true}`), Stream: true}, func(event provider.Event) error { done = done || event.Done; return nil })
	if err == nil || !strings.Contains(err.Error(), "without explicit [DONE]") {
		t.Fatalf("expected incomplete-stream error, got %v", err)
	}
	if done {
		t.Fatal("incomplete EOF must not emit a successful terminal event")
	}
}
