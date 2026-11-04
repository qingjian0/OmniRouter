package observability

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type noFlushWriter struct{ header http.Header }

func (w *noFlushWriter) Header() http.Header         { return w.header }
func (w *noFlushWriter) WriteHeader(int)             {}
func (w *noFlushWriter) Write(p []byte) (int, error) { return len(p), nil }

type flushWriter struct {
	noFlushWriter
	flushed bool
}

func (w *flushWriter) Flush() { w.flushed = true }

func TestMiddlewareDoesNotAdvertiseUnsupportedFlusher(t *testing.T) {
	handler := NewRecorder(4).Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, ok := w.(http.Flusher); ok {
			t.Fatal("middleware advertised http.Flusher unsupported by underlying writer")
		}
	}))
	writer := &noFlushWriter{header: make(http.Header)}
	handler.ServeHTTP(writer, httptest.NewRequest(http.MethodGet, "/", nil))
}

func TestMiddlewarePreservesSupportedFlusher(t *testing.T) {
	underlying := &flushWriter{noFlushWriter: noFlushWriter{header: make(http.Header)}}
	handler := NewRecorder(4).Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("middleware hid supported http.Flusher")
		}
		flusher.Flush()
	}))
	handler.ServeHTTP(underlying, httptest.NewRequest(http.MethodGet, "/", nil))
	if !underlying.flushed {
		t.Fatal("Flush did not reach underlying writer")
	}
}

func TestMiddlewareUnwrapsForResponseControllerWithoutAdvertisingHijacker(t *testing.T) {
	handler := NewRecorder(4).Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, ok := w.(http.Hijacker); ok {
			t.Fatal("middleware advertised http.Hijacker unsupported by underlying writer")
		}
		if _, _, err := http.NewResponseController(w).Hijack(); err == nil {
			t.Fatal("ResponseController.Hijack unexpectedly succeeded on a writer without Hijacker")
		} else if !errors.Is(err, http.ErrNotSupported) {
			t.Fatalf("Hijack error = %v, want http.ErrNotSupported", err)
		}
	}))
	writer := httptest.NewRecorder()
	handler.ServeHTTP(writer, httptest.NewRequest(http.MethodGet, "/", nil))
}

func TestMiddlewareResponseControllerFlushRespectsUnderlyingCapability(t *testing.T) {
	for _, tc := range []struct {
		name      string
		writer    http.ResponseWriter
		wantErr   bool
		wantFlush bool
	}{
		{name: "unsupported", writer: &noFlushWriter{header: make(http.Header)}, wantErr: true},
		{name: "supported", writer: &flushWriter{noFlushWriter: noFlushWriter{header: make(http.Header)}}, wantFlush: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := NewRecorder(4).Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				err := http.NewResponseController(w).Flush()
				if tc.wantErr {
					if !errors.Is(err, http.ErrNotSupported) {
						t.Fatalf("Flush error = %v, want http.ErrNotSupported", err)
					}
				} else if err != nil {
					t.Fatalf("Flush returned error: %v", err)
				}
			}))
			handler.ServeHTTP(tc.writer, httptest.NewRequest(http.MethodGet, "/", nil))
			if tc.wantFlush {
				if writer, ok := tc.writer.(*flushWriter); !ok || !writer.flushed {
					t.Fatal("ResponseController.Flush did not reach underlying writer")
				}
			}
		})
	}
}

type readerFromWriter struct {
	noFlushWriter
	readFromCalled bool
	body           bytes.Buffer
}

func (w *readerFromWriter) Write(p []byte) (int, error) { return w.body.Write(p) }
func (w *readerFromWriter) ReadFrom(src io.Reader) (int64, error) {
	w.readFromCalled = true
	return w.body.ReadFrom(src)
}

type plainReader struct{ reader *strings.Reader }

func (r plainReader) Read(p []byte) (int, error) { return r.reader.Read(p) }

func TestMiddlewarePreservesReaderFromAndTracksImplicitStatus(t *testing.T) {
	underlying := &readerFromWriter{noFlushWriter: noFlushWriter{header: make(http.Header)}}
	recorder := NewRecorder(4)
	handler := recorder.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, ok := w.(io.ReaderFrom); !ok {
			t.Fatal("middleware did not expose safe io.ReaderFrom implementation")
		}
		if _, err := io.Copy(w, plainReader{reader: strings.NewReader("reader-from-payload")}); err != nil {
			t.Fatalf("io.Copy: %v", err)
		}
	}))
	handler.ServeHTTP(underlying, httptest.NewRequest(http.MethodGet, "/", nil))
	if !underlying.readFromCalled {
		t.Fatal("io.Copy did not use the underlying ReaderFrom fast path")
	}
	if got := underlying.body.String(); got != "reader-from-payload" {
		t.Fatalf("body = %q", got)
	}
	records := recorder.Recent(1)
	if len(records) != 1 || records[0].Status != http.StatusOK {
		t.Fatalf("implicit response status was not tracked: %+v", records)
	}
}

type hijackWriter struct{ noFlushWriter }

func (w *hijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, errors.New("test hijack reached")
}

func TestMiddlewareResponseControllerReachesSupportedHijacker(t *testing.T) {
	underlying := &hijackWriter{noFlushWriter: noFlushWriter{header: make(http.Header)}}
	handler := NewRecorder(4).Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, ok := w.(http.Hijacker); ok {
			t.Fatal("middleware directly advertised Hijacker instead of relying on Unwrap")
		}
		_, _, err := http.NewResponseController(w).Hijack()
		if err == nil || err.Error() != "test hijack reached" {
			t.Fatalf("ResponseController.Hijack error = %v, want underlying sentinel", err)
		}
	}))
	handler.ServeHTTP(underlying, httptest.NewRequest(http.MethodGet, "/", nil))
}
