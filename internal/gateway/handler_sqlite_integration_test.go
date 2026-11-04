package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"omnirouter/internal/provider"
	"omnirouter/internal/storage"
)

// TestGatewayRecorderSQLiteAdminEndToEnd exercises the production HTTP handler,
// recorder sink, SQLite repository, and administrative read APIs as one path.
// It intentionally uses a deterministic in-process provider; no live upstream
// credentials or network access are required.
func TestGatewayRecorderSQLiteAdminEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dbPath := filepath.Join(t.TempDir(), "gateway-usage.db")
	runtime, err := storage.OpenSQLiteRuntime(ctx, dbPath, storage.SQLiteOptions{BusyTimeout: time.Second, WAL: true})
	if err != nil {
		t.Fatalf("open SQLite runtime: %v", err)
	}
	repo, err := storage.NewRequestLogRepository(runtime.Store().DB())
	if err != nil {
		_ = runtime.Close()
		t.Fatalf("create request log repository: %v", err)
	}
	registry := provider.NewRegistry()
	testProvider := &usageTestProvider{stream: func(_ context.Context, req *provider.Request, emit func(provider.Event) error) error {
		if bytes.Contains(req.Input, []byte("simulate_eof")) {
			if err := emit(provider.Event{Data: []byte(`data: {"choices":[],"usage":{"prompt_tokens":11,"completion_tokens":2,"total_tokens":13}}`)}); err != nil {
				return err
			}
			return io.ErrUnexpectedEOF
		}
		if err := emit(provider.Event{Data: []byte(`data: {"choices":[],"usage":{"prompt_tokens":9,"completion_tokens":4,"total_tokens":13}}`)}); err != nil {
			return err
		}
		return emit(provider.Event{Done: true, ExplicitDone: true})
	}}
	if err := registry.Register(testProvider); err != nil {
		_ = runtime.Close()
		t.Fatalf("register test provider: %v", err)
	}
	handler := NewHandlerWithFullRepositories(registry, "", nil, nil, nil, nil, nil, nil, nil, repo)

	completion := httptest.NewRecorder()
	completionRequest := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"model-test"}`))
	handler.ServeHTTP(completion, completionRequest)
	if completion.Code != http.StatusOK {
		t.Fatalf("completion status=%d body=%s", completion.Code, completion.Body.String())
	}

	// Exercise the same durable path for a completed SSE response.
	streamResponse := httptest.NewRecorder()
	streamRequest := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"model-test","stream":true}`))
	handler.ServeHTTP(streamResponse, streamRequest)
	if streamResponse.Code != http.StatusOK || !strings.Contains(streamResponse.Body.String(), "[DONE]") {
		t.Fatalf("stream status=%d body=%s", streamResponse.Code, streamResponse.Body.String())
	}

	// An upstream EOF after partial usage must retain the partial token counts
	// but must not mark those counts as final or the request as completed.
	interruptedResponse := httptest.NewRecorder()
	interruptedRequest := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"model-test","stream":true,"simulate_eof":true}`))
	handler.ServeHTTP(interruptedResponse, interruptedRequest)
	if interruptedResponse.Code != http.StatusOK {
		t.Fatalf("interrupted stream status=%d body=%s", interruptedResponse.Code, interruptedResponse.Body.String())
	}

	usageResponse := httptest.NewRecorder()
	handler.ServeHTTP(usageResponse, httptest.NewRequest(http.MethodGet, "/admin/usage", nil))
	if usageResponse.Code != http.StatusOK {
		t.Fatalf("usage status=%d body=%s", usageResponse.Code, usageResponse.Body.String())
	}
	var usage struct {
		Persisted bool `json:"persisted"`
		Data      struct {
			Requests           int64 `json:"requests"`
			Errors             int64 `json:"errors"`
			InputTokens        int64 `json:"input_tokens"`
			OutputTokens       int64 `json:"output_tokens"`
			KnownTokenRequests int64 `json:"known_token_requests"`
			FinalTokenRequests int64 `json:"final_token_requests"`
		} `json:"data"`
		Breakdown []storage.UsageBreakdown `json:"by_provider_model"`
	}
	if err := json.Unmarshal(usageResponse.Body.Bytes(), &usage); err != nil {
		t.Fatalf("decode usage response: %v", err)
	}
	if !usage.Persisted || usage.Data.Requests != 3 || usage.Data.Errors != 1 || usage.Data.InputTokens != 27 || usage.Data.OutputTokens != 11 || usage.Data.KnownTokenRequests != 3 || usage.Data.FinalTokenRequests != 2 {
		t.Fatalf("unexpected persisted usage response: %+v", usage)
	}
	if len(usage.Breakdown) != 1 || usage.Breakdown[0].ProviderID != "provider-test" || usage.Breakdown[0].ModelID != "model-test" || usage.Breakdown[0].Requests != 3 || usage.Breakdown[0].InputTokens != 27 || usage.Breakdown[0].OutputTokens != 11 || usage.Breakdown[0].FinalTokenRequests != 2 {
		t.Fatalf("unexpected provider/model breakdown: %+v", usage.Breakdown)
	}

	requestsResponse := httptest.NewRecorder()
	handler.ServeHTTP(requestsResponse, httptest.NewRequest(http.MethodGet, "/admin/requests?limit=10", nil))
	if requestsResponse.Code != http.StatusOK {
		t.Fatalf("requests status=%d body=%s", requestsResponse.Code, requestsResponse.Body.String())
	}
	var requests struct {
		Persisted bool                 `json:"persisted"`
		Data      []storage.RequestLog `json:"data"`
	}
	if err := json.Unmarshal(requestsResponse.Body.Bytes(), &requests); err != nil {
		t.Fatalf("decode requests response: %v", err)
	}
	if !requests.Persisted || len(requests.Data) < 4 {
		t.Fatalf("expected persisted completion and admin usage records, got %+v", requests)
	}
	foundCompletion, foundStream, foundInterrupted := false, false, false
	for _, item := range requests.Data {
		if item.ModelID != "model-test" {
			if item.UsageEligible {
				t.Errorf("non-inference request %q was marked usage eligible", item.ID)
			}
			continue
		}
		if !item.UsageEligible {
			t.Errorf("inference request %q was not marked usage eligible", item.ID)
		}
		switch {
		case item.LifecycleStatus == "completed" && item.UsageFinal && item.InputTokens != nil && *item.InputTokens == 7:
			foundCompletion = item.ProviderID == "provider-test" && item.StatusCode == http.StatusOK && item.OutputTokens != nil && *item.OutputTokens == 5
		case item.LifecycleStatus == "completed" && item.UsageFinal && item.InputTokens != nil && *item.InputTokens == 9:
			foundStream = item.ProviderID == "provider-test" && item.OutputTokens != nil && *item.OutputTokens == 4
		case item.LifecycleStatus == "failed" && item.ErrorCode == "upstream_stream_error" && !item.UsageFinal && item.InputTokens != nil && *item.InputTokens == 11:
			foundInterrupted = item.ProviderID == "provider-test" && item.OutputTokens != nil && *item.OutputTokens == 2
		}
	}
	if !foundCompletion || !foundStream || !foundInterrupted {
		t.Fatalf("missing or incorrect completion/SSE/interrupted records: completion=%t stream=%t interrupted=%t records=%+v", foundCompletion, foundStream, foundInterrupted, requests.Data)
	}

	if err := runtime.Close(); err != nil {
		t.Fatalf("close runtime: %v", err)
	}
	reopened, err := storage.OpenSQLiteRuntime(ctx, dbPath, storage.SQLiteOptions{BusyTimeout: time.Second, WAL: true})
	if err != nil {
		t.Fatalf("reopen SQLite runtime: %v", err)
	}
	defer reopened.Close()
	reopenedRepo, err := storage.NewRequestLogRepository(reopened.Store().DB())
	if err != nil {
		t.Fatalf("create repository after restart: %v", err)
	}
	summary, err := reopenedRepo.Summary(ctx)
	if err != nil {
		t.Fatalf("read summary after restart: %v", err)
	}
	if summary.Requests != 3 || summary.Errors != 1 || summary.InputTokens != 27 || summary.OutputTokens != 11 || summary.FinalTokenRequests != 2 {
		t.Fatalf("persisted summary did not survive restart: %+v", summary)
	}
}
