package deepseekweb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"omnirouter/internal/websession"
)

func TestHTTPCompletionTransportAuthorizedMultiTurnFlow(t *testing.T) {
	var completionPayload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "ds_session_id=authorized-session" {
			t.Errorf("unexpected cookie header")
		}
		switch r.URL.Path {
		case usersCurrentPath:
			if r.Header.Get("Authorization") != "Bearer user-token" {
				t.Errorf("users/current did not receive user token")
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{"biz_data":{"token":"access-token"}}}`))
		case createSessionPath:
			if r.Header.Get("Authorization") != "Bearer access-token" {
				t.Errorf("session create did not receive access token")
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{"biz_data":{"chat_session":{"id":"upstream-session"}}}}`))
		case completionPath:
			if err := json.NewDecoder(r.Body).Decode(&completionPayload); err != nil {
				t.Errorf("decode completion request: %v", err)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: ready\ndata: {\"request_message_id\":\"request-1\",\"response_message_id\":\"response-1\"}\n\n"))
			_, _ = w.Write([]byte("data: {\"p\":\"response/fragments\",\"o\":\"APPEND\",\"v\":[{\"type\":\"RESPONSE\",\"content\":\"hello\"}]}\n\n"))
			_, _ = w.Write([]byte("data: {\"p\":\"response/status\",\"v\":\"FINISHED\"}\n\n"))
			_, _ = w.Write([]byte("data: {\"p\":\"response/search_results\",\"o\":\"APPEND\",\"v\":[{\"title\":\"source\"}]}\n\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := websession.NewHTTPClient(server.URL, "/api/v0")
	if err != nil {
		t.Fatal(err)
	}
	transport := &HTTPCompletionTransport{client: client}
	var updates []PatchUpdate
	result, err := transport.Stream(context.Background(), AuthorizationMaterial{
		Token: []byte("user-token"), CookieHeader: []byte("ds_session_id=authorized-session"),
	}, ChatCompletionInput{
		Model: "deepseek-chat", ModelType: "default", Prompt: "hello", RefFileIDs: []string{},
	}, websession.Lineage{}, func(update PatchUpdate) error {
		updates = append(updates, update)
		return nil
	})
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	if !result.Completed || result.UpstreamConversationID != "upstream-session" || result.ResponseMessageID != "response-1" {
		t.Fatalf("unexpected completion result: %#v", result)
	}
	if len(updates) != 3 || updates[1].ContentDelta != "hello" || !updates[2].Done {
		t.Fatalf("unexpected updates: %#v", updates)
	}
	if parent, exists := completionPayload["parent_message_id"]; !exists || parent != nil {
		t.Fatalf("new conversation parent_message_id must be null, got %#v", parent)
	}
	if completionPayload["chat_session_id"] != "upstream-session" {
		t.Fatalf("unexpected upstream session ID: %#v", completionPayload["chat_session_id"])
	}
}

func TestHTTPCompletionTransportFailsClosedOnPolicyBlock(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "blocked", http.StatusForbidden)
	}))
	defer server.Close()
	client, err := websession.NewHTTPClient(server.URL, "/api/v0")
	if err != nil {
		t.Fatal(err)
	}
	transport := &HTTPCompletionTransport{client: client}
	_, err = transport.acquireAccessToken(context.Background(), AuthorizationMaterial{
		Token: []byte("user-token"), CookieHeader: []byte("ds_session_id=authorized-session"),
	})
	if err != ErrPolicyBlocked {
		t.Fatalf("expected policy-blocked error, got %v", err)
	}
}
