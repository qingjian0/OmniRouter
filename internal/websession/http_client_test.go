package websession

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPClientPinsOriginAndRejectsOutOfScopePaths(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/api/v0/chat" {
			t.Errorf("path=%q", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client, err := NewHTTPClient(server.URL, "/api/v0")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Do(context.Background(), http.MethodPost, "/api/v0/chat", nil, strings.NewReader("{}")); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{
		"https://evil.example/api/v0/chat",
		"//evil.example/api/v0/chat",
		"/api/v0/../admin",
		"/api-evil/chat",
		"/outside",
	} {
		if _, err := client.Do(context.Background(), http.MethodGet, invalid, nil, nil); err == nil {
			t.Errorf("invalid path %q was accepted", invalid)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("upstream calls=%d, want 1", got)
	}
}

func TestHTTPClientDoesNotFollowCrossOriginRedirect(t *testing.T) {
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		targetCalls.Add(1)
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/stolen", http.StatusFound)
	}))
	defer source.Close()
	client, err := NewHTTPClient(source.URL, "/api")
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(context.Background(), http.MethodGet, "/api/test", http.Header{"Authorization": []string{"Bearer test-secret"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusFound {
		t.Fatalf("status=%d, want 302", response.StatusCode)
	}
	if got := targetCalls.Load(); got != 0 {
		t.Fatalf("redirect target calls=%d, want 0", got)
	}
}

func TestHTTPClientRejectsRoutingHeaders(t *testing.T) {
	client, err := NewHTTPClient("https://example.com", "/api")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Host", "Connection", "Proxy-Authorization", "Transfer-Encoding", "Upgrade"} {
		if _, err := client.Do(context.Background(), http.MethodGet, "/api/test", http.Header{name: []string{"bad"}}, nil); err == nil {
			t.Errorf("header %q was accepted", name)
		}
	}
}

func TestHTTPClientPropagatesCancellation(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	client, err := NewHTTPClient(server.URL, "/api")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		response, err := client.Do(ctx, http.MethodGet, "/api/stream", nil, nil)
		if response != nil {
			response.Body.Close()
		}
		done <- err
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled request returned no error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled upstream request did not return")
	}
}

func TestReadBoundedBody(t *testing.T) {
	value, err := ReadBoundedBody(strings.NewReader("small"), 5)
	if err != nil || string(value) != "small" {
		t.Fatalf("value=%q err=%v", value, err)
	}
	if _, err := ReadBoundedBody(strings.NewReader("too large"), 4); err == nil {
		t.Fatal("oversized body accepted")
	}
	if _, err := ReadBoundedBody(errorReader{}, 10); err == nil || errors.Is(err, io.EOF) {
		t.Fatalf("reader error was not preserved: %v", err)
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }
