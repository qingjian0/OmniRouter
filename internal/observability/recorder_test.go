package observability

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

type testSink struct {
	record RequestRecord
	called bool
	ctxErr error
}

func (s *testSink) Append(ctx context.Context, record RequestRecord) error {
	s.record = record
	s.called = true
	s.ctxErr = ctx.Err()
	return nil
}

func TestMiddlewarePersistsProviderUsageAndLifecycle(t *testing.T) {
	recorder := NewRecorder(8)
	sink := &testSink{}
	recorder.SetSink(sink)
	handler := recorder.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		input, output := int64(0), int64(12)
		SetRequestDetails(r, func(d *RequestDetails) {
			d.ProviderID, d.ModelID = "provider-a", "model-a"
			d.InputTokens, d.OutputTokens = &input, &output
			d.UsageSource, d.UsageFinal = "provider", true
			d.LifecycleStatus = "completed"
		})
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions?secret=ignored", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if !sink.called {
		t.Fatal("request record was not persisted")
	}
	if sink.record.ProviderID != "provider-a" || sink.record.ModelID != "model-a" {
		t.Fatalf("missing attribution: %#v", sink.record)
	}
	if sink.record.InputTokens == nil || *sink.record.InputTokens != 0 || sink.record.OutputTokens == nil || *sink.record.OutputTokens != 12 {
		t.Fatalf("missing usage: %#v", sink.record)
	}
	if !sink.record.UsageFinal || sink.record.UsageSource != "provider" || sink.record.LifecycleStatus != "completed" {
		t.Fatalf("missing lifecycle/usage metadata: %#v", sink.record)
	}
	if sink.record.Path != "/v1/chat/completions" {
		t.Fatalf("query string leaked into path: %q", sink.record.Path)
	}
}

type failingSink struct{}

func (failingSink) Append(context.Context, RequestRecord) error {
	return context.DeadlineExceeded
}

type panickingSink struct{}

func (panickingSink) Append(context.Context, RequestRecord) error {
	panic("sensitive sink panic detail")
}

func TestMiddlewareContainsPersistenceSinkPanic(t *testing.T) {
	recorder := NewRecorder(4)
	recorder.SetSink(panickingSink{})
	handler := recorder.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	// Persistence is best-effort telemetry: a sink panic must not escape after
	// the response handler has completed.
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Fatalf("persistence sink panic escaped middleware: %v", recovered)
			}
		}()
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz", nil))
	}()

	stats := recorder.Snapshot()
	if stats.TotalRequests != 1 || stats.PersistenceFailures != 1 || stats.InFlight != 0 {
		t.Fatalf("unexpected stats after sink panic: %+v", stats)
	}
}

func TestHandlerPanicIsPreservedWhenPersistenceSinkAlsoPanics(t *testing.T) {
	recorder := NewRecorder(4)
	recorder.SetSink(panickingSink{})
	handler := recorder.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("original handler panic")
	}))

	var recovered any
	func() {
		defer func() { recovered = recover() }()
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/panic", nil))
	}()
	if recovered != "original handler panic" {
		t.Fatalf("recovered panic = %v, want original handler panic", recovered)
	}
	stats := recorder.Snapshot()
	if stats.TotalRequests != 1 || stats.PersistenceFailures != 1 || stats.InFlight != 0 {
		t.Fatalf("unexpected stats after handler and sink panic: %+v", stats)
	}
}

func TestMiddlewareCountsPersistenceFailures(t *testing.T) {
	recorder := NewRecorder(4)
	recorder.SetSink(failingSink{})
	handler := recorder.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/health", nil))

	stats := recorder.Snapshot()
	if stats.TotalRequests != 1 {
		t.Fatalf("total requests = %d, want 1", stats.TotalRequests)
	}
	if stats.PersistenceFailures != 1 {
		t.Fatalf("persistence failures = %d, want 1", stats.PersistenceFailures)
	}
	if stats.InFlight != 0 {
		t.Fatalf("in-flight = %d, want 0", stats.InFlight)
	}
}

func TestMiddlewareClassifiesUnannotatedHTTPErrorAsFailed(t *testing.T) {
	recorder := NewRecorder(4)
	handler := recorder.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/test", nil))

	records := recorder.Recent(1)
	if len(records) != 1 || records[0].LifecycleStatus != "failed" {
		t.Fatalf("HTTP 400 should have failed lifecycle: %+v", records)
	}
}

func TestMiddlewareClassifiesPanicAfterResponseWriteAsFailed(t *testing.T) {
	recorder := NewRecorder(4)
	handler := recorder.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		panic("test panic")
	}))
	func() {
		defer func() { _ = recover() }()
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/panic", nil))
	}()

	records := recorder.Recent(1)
	if len(records) != 1 || records[0].LifecycleStatus != "failed" || records[0].ErrorCode != "handler_panic" {
		t.Fatalf("panic should have failed lifecycle even after 200 was written: %+v", records)
	}
}

func TestInferenceRequestClassificationExcludesAdminAndHealthTraffic(t *testing.T) {
	cases := []struct {
		method, path string
		want         bool
	}{
		{http.MethodPost, "/v1/chat/completions", true},
		{http.MethodPost, "/v1/messages", true},
		{http.MethodPost, "/v1beta/models/gemini-pro:generateContent", true},
		{http.MethodPost, "/v1beta/models/gemini-pro:streamGenerateContent", true},
		{http.MethodGet, "/v1/models", false},
		{http.MethodGet, "/admin/usage", false},
		{http.MethodGet, "/admin/requests", false},
		{http.MethodGet, "/healthz", false},
		{http.MethodPost, "/admin/api-keys", false},
	}
	for _, tc := range cases {
		if got := isInferenceRequest(tc.method, tc.path); got != tc.want {
			t.Errorf("isInferenceRequest(%q, %q) = %v, want %v", tc.method, tc.path, got, tc.want)
		}
	}
}

func TestSnapshotUsesOneConsistentRecorderSnapshot(t *testing.T) {
	recorder := NewRecorder(2)
	handler := recorder.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	for i := 0; i < 3; i++ {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz", nil))
	}

	stats := recorder.Snapshot()
	if stats.TotalRequests != 3 {
		t.Fatalf("total requests = %d, want 3", stats.TotalRequests)
	}
	if stats.WindowSize != 2 {
		t.Fatalf("window size = %d, want capacity 2", stats.WindowSize)
	}
	if stats.StatusCounts["2xx"] != 2 {
		t.Fatalf("2xx window count = %d, want 2", stats.StatusCounts["2xx"])
	}
	if stats.InFlight != 0 {
		t.Fatalf("in-flight = %d, want 0", stats.InFlight)
	}
}
