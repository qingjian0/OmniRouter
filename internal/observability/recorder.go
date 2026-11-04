// Package observability provides bounded, privacy-conscious HTTP request
// telemetry for the gateway and administrative endpoints.
package observability

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

const defaultCapacity = 1000

type RequestRecord struct {
	ID              string    `json:"id"`
	ProviderID      string    `json:"provider_id,omitempty"`
	ModelID         string    `json:"model_id,omitempty"`
	InputTokens     *int64    `json:"input_tokens,omitempty"`
	OutputTokens    *int64    `json:"output_tokens,omitempty"`
	ErrorCode       string    `json:"error_code,omitempty"`
	LifecycleStatus string    `json:"lifecycle_status"`
	UsageSource     string    `json:"token_usage_source,omitempty"`
	UsageFinal      bool      `json:"token_usage_final"`
	UsageEligible   bool      `json:"usage_eligible"`
	Method          string    `json:"method"`
	Path            string    `json:"path"`
	Status          int       `json:"status"`
	DurationMS      int64     `json:"duration_ms"`
	StartedAt       time.Time `json:"started_at"`
}

type Stats struct {
	TotalRequests       uint64         `json:"total_requests"`
	InFlight            int64          `json:"in_flight"`
	PersistenceFailures uint64         `json:"persistence_failures"`
	StatusCounts        map[string]int `json:"status_counts"`
	AverageMS           float64        `json:"average_ms"`
	P95MS               int64          `json:"p95_ms"`
	WindowSize          int            `json:"window_size"`
}

type RequestSink interface {
	Append(context.Context, RequestRecord) error
}

type requestDetailsKey struct{}
type RequestDetails struct {
	mu              sync.RWMutex
	ProviderID      string
	ModelID         string
	InputTokens     *int64
	OutputTokens    *int64
	ErrorCode       string
	LifecycleStatus string
	UsageSource     string
	UsageFinal      bool
}

// SetRequestDetails updates metadata for the current request. It stores only
// routing identifiers, provider-reported token counts and a bounded error code.
func SetRequestDetails(req *http.Request, update func(*RequestDetails)) {
	if req == nil || update == nil {
		return
	}
	details, _ := req.Context().Value(requestDetailsKey{}).(*RequestDetails)
	if details == nil {
		return
	}
	details.mu.Lock()
	defer details.mu.Unlock()
	update(details)
}

type Recorder struct {
	sink                RequestSink
	mu                  sync.RWMutex
	capacity            int
	records             []RequestRecord
	next                int
	count               int
	total               uint64
	inFlight            int64
	persistenceFailures uint64
}

func NewRecorder(capacity int) *Recorder {
	if capacity <= 0 {
		capacity = defaultCapacity
	}
	return &Recorder{capacity: capacity, records: make([]RequestRecord, capacity)}
}

func (r *Recorder) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		started := time.Now()
		id := requestID()
		w.Header().Set("X-Request-ID", id)
		r.mu.Lock()
		r.inFlight++
		r.mu.Unlock()
		details := &RequestDetails{LifecycleStatus: "completed"}
		req = req.WithContext(context.WithValue(req.Context(), requestDetailsKey{}, details))

		recorder := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		var wrapped http.ResponseWriter = recorder
		// Preserve the streaming interface only when the underlying writer
		// supports it. statusWriter.Unwrap keeps http.ResponseController able to
		// reach other optional capabilities (Hijacker, Pusher, deadlines) without
		// falsely advertising those interfaces through direct type assertions.
		if _, ok := w.(http.Flusher); ok {
			wrapped = flushStatusWriter{statusWriter: recorder}
		}
		defer func() {
			if recovered := recover(); recovered != nil {
				if !recorder.wrote {
					recorder.WriteHeader(http.StatusInternalServerError)
				}
				setLifecycleFailure(details, "handler_panic")
				record := r.record(id, req, started, recorder.status)
				r.persist(req.Context(), record)
				panic(recovered)
			}
			record := r.record(id, req, started, recorder.status)
			r.persist(req.Context(), record)
		}()
		next.ServeHTTP(wrapped, req)
	})
}

func (r *Recorder) SetSink(sink RequestSink) { r.mu.Lock(); r.sink = sink; r.mu.Unlock() }

func (r *Recorder) persist(ctx context.Context, record RequestRecord) {
	r.mu.RLock()
	sink := r.sink
	r.mu.RUnlock()
	if sink != nil {
		persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		failed := false
		func() {
			// A sink is an observability side effect and must never replace a
			// handler panic or crash the request-serving goroutine. Treat sink
			// panics exactly like persistence errors, without exposing panic data.
			defer func() {
				if recover() != nil {
					failed = true
				}
			}()
			if err := sink.Append(persistCtx, record); err != nil {
				failed = true
			}
		}()
		if failed {
			r.mu.Lock()
			r.persistenceFailures++
			r.mu.Unlock()
		}
	}
}

func (r *Recorder) record(id string, req *http.Request, started time.Time, status int) RequestRecord {
	record := RequestRecord{
		ID: id, Method: req.Method, Path: safePath(req.URL.Path),
		UsageEligible: isInferenceRequest(req.Method, req.URL.Path),
		Status:        status, DurationMS: time.Since(started).Milliseconds(),
		StartedAt: started.UTC(),
	}
	if details, ok := req.Context().Value(requestDetailsKey{}).(*RequestDetails); ok {
		details.mu.RLock()
		record.ProviderID, record.ModelID = details.ProviderID, details.ModelID
		record.InputTokens, record.OutputTokens, record.ErrorCode = details.InputTokens, details.OutputTokens, details.ErrorCode
		record.LifecycleStatus, record.UsageSource, record.UsageFinal = details.LifecycleStatus, details.UsageSource, details.UsageFinal
		details.mu.RUnlock()
	}
	// A handler that returns an HTTP error without explicitly classifying its
	// failure must not be recorded as a successful lifecycle completion.
	if status >= http.StatusBadRequest && record.LifecycleStatus == "completed" {
		record.LifecycleStatus = "failed"
	}
	r.mu.Lock()
	r.inFlight--
	r.total++
	r.records[r.next] = record
	r.next = (r.next + 1) % r.capacity
	if r.count < r.capacity {
		r.count++
	}
	r.mu.Unlock()
	return record
}

func (r *Recorder) Recent(limit int) []RequestRecord {
	if limit <= 0 || limit > r.capacity {
		limit = r.capacity
	}
	r.mu.RLock()
	count := r.count
	if limit < count {
		count = limit
	}
	out := make([]RequestRecord, 0, count)
	for offset := 1; offset <= count; offset++ {
		index := (r.next - offset + r.capacity) % r.capacity
		out = append(out, r.records[index])
	}
	r.mu.RUnlock()
	return out
}

func (r *Recorder) Snapshot() Stats {
	// Capture counters and the bounded record window under one read lock. The
	// previous two-lock implementation could combine a record window from one
	// instant with lifetime counters from a later instant while requests were
	// completing concurrently.
	r.mu.RLock()
	stats := Stats{
		TotalRequests:       r.total,
		InFlight:            r.inFlight,
		PersistenceFailures: r.persistenceFailures,
		StatusCounts:        make(map[string]int),
	}
	count := r.count
	records := make([]RequestRecord, 0, count)
	for offset := 1; offset <= count; offset++ {
		index := (r.next - offset + r.capacity) % r.capacity
		records = append(records, r.records[index])
	}
	r.mu.RUnlock()
	stats.WindowSize = len(records)
	if len(records) == 0 {
		return stats
	}
	latencies := make([]int64, 0, len(records))
	var total int64
	for _, record := range records {
		stats.StatusCounts[statusClass(record.Status)]++
		total += record.DurationMS
		latencies = append(latencies, record.DurationMS)
	}
	stats.AverageMS = float64(total) / float64(len(records))
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	index := (95*len(latencies)+99)/100 - 1
	if index < 0 {
		index = 0
	}
	stats.P95MS = latencies[index]
	return stats
}

// isInferenceRequest separates model inference from health checks, model discovery,
// and administrative traffic. All requests remain visible in request history, but
// only inference endpoints contribute to token usage and provider/model summaries.
func isInferenceRequest(method, path string) bool {
	if method != http.MethodPost {
		return false
	}
	switch path {
	case "/v1/chat/completions", "/v1/messages":
		return true
	}
	if !strings.HasPrefix(path, "/v1beta/models/") {
		return false
	}
	return strings.HasSuffix(path, ":generateContent") || strings.HasSuffix(path, ":streamGenerateContent")
}

func setLifecycleFailure(details *RequestDetails, code string) {
	details.mu.Lock()
	details.LifecycleStatus = "failed"
	if details.ErrorCode == "" {
		details.ErrorCode = code
	}
	details.mu.Unlock()
}

type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *statusWriter) WriteHeader(status int) {
	if w.wrote {
		return
	}
	w.status = status
	w.wrote = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(data []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}

// ReadFrom preserves io.Copy's optimized destination path while ensuring the
// implicit 200 status is recorded before bytes are forwarded. When the
// underlying writer has its own ReaderFrom implementation, use it directly;
// otherwise fall back to io.Copy against the underlying Writer.
func (w *statusWriter) ReadFrom(src io.Reader) (int64, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	if readerFrom, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		return readerFrom.ReadFrom(src)
	}
	return io.Copy(w.ResponseWriter, src)
}

type flushStatusWriter struct{ *statusWriter }

func (w flushStatusWriter) Flush() {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	w.ResponseWriter.(http.Flusher).Flush()
}

// Unwrap exposes the original writer to http.ResponseController. The
// middleware intentionally does not implement optional interfaces such as
// http.Hijacker or http.Pusher unconditionally: doing so would make handlers
// take capability branches that the underlying server writer may not support.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func requestID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err == nil {
		return hex.EncodeToString(bytes[:])
	}
	return hex.EncodeToString([]byte(time.Now().UTC().Format("20060102150405.000000000")))
}

func safePath(path string) string {
	if path == "" {
		return "/"
	}
	// Never persist query strings or arbitrary user-controlled URL fragments.
	if strings.Contains(path, "?") {
		path = strings.SplitN(path, "?", 2)[0]
	}
	return path
}

func statusClass(status int) string {
	return string(rune('0'+status/100)) + "xx"
}
