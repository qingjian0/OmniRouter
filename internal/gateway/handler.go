package gateway

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"omnirouter/internal/account"
	"omnirouter/internal/adapter"
	"omnirouter/internal/admin"
	"omnirouter/internal/apikey"
	"omnirouter/internal/credential"
	domainmodel "omnirouter/internal/model"
	"omnirouter/internal/observability"
	"omnirouter/internal/protocol/anthropic"
	"omnirouter/internal/protocol/gemini"
	"omnirouter/internal/provider"
	"omnirouter/internal/session"
	"omnirouter/internal/storage"
	"omnirouter/internal/tokenusage"
)

type Selector interface {
	Select(context.Context, string) (provider.Provider, error)
	ReportFailure(string)
}

type Handler struct {
	registry     *provider.Registry
	selector     Selector
	apiKey       string
	mux          *http.ServeMux
	metrics      *observability.Recorder
	accounts     *account.Store
	sessions     *session.Store
	credentials  *credential.Vault
	providerRepo *provider.Repository
	accountRepo  *account.Repository
	sessionRepo  *session.Repository
	modelRepo    *domainmodel.Repository
	apiKeyRepo   *apikey.Repository
	requestLogs  *storage.RequestLogRepository
	mutationMu   sync.Mutex
}

func NewHandler(registry *provider.Registry, apiKey string, selector Selector) http.Handler {
	return NewHandlerWithVault(registry, apiKey, selector, nil)
}

func NewHandlerWithVault(registry *provider.Registry, apiKey string, selector Selector, vault *credential.Vault) http.Handler {
	return NewHandlerWithPersistence(registry, apiKey, selector, vault, nil)
}

// NewHandlerWithPersistence adds durable provider metadata storage while keeping
// the original constructor available to embedders that do not use SQL storage.
func NewHandlerWithPersistence(registry *provider.Registry, apiKey string, selector Selector, vault *credential.Vault, providerRepo *provider.Repository) http.Handler {
	return NewHandlerWithRepositories(registry, apiKey, selector, vault, providerRepo, nil, nil)
}

func NewHandlerWithRepositories(registry *provider.Registry, apiKey string, selector Selector, vault *credential.Vault, providerRepo *provider.Repository, accountRepo *account.Repository, sessionRepo *session.Repository) http.Handler {
	return NewHandlerWithFullRepositories(registry, apiKey, selector, vault, providerRepo, accountRepo, sessionRepo, nil, nil)
}

func NewHandlerWithFullRepositories(registry *provider.Registry, apiKey string, selector Selector, vault *credential.Vault, providerRepo *provider.Repository, accountRepo *account.Repository, sessionRepo *session.Repository, modelRepo *domainmodel.Repository, apiKeyRepo *apikey.Repository, requestLogRepos ...*storage.RequestLogRepository) http.Handler {
	h := &Handler{registry: registry, apiKey: apiKey, selector: selector, mux: http.NewServeMux(), metrics: observability.NewRecorder(1000), accounts: account.NewStore(), sessions: session.NewStore(), credentials: vault, providerRepo: providerRepo, accountRepo: accountRepo, sessionRepo: sessionRepo, modelRepo: modelRepo, apiKeyRepo: apiKeyRepo}
	if len(requestLogRepos) > 0 && requestLogRepos[0] != nil {
		h.requestLogs = requestLogRepos[0]
		h.metrics.SetSink(requestLogSink{repo: requestLogRepos[0]})
	}
	h.mux.HandleFunc("GET /healthz", h.health)
	h.mux.HandleFunc("GET /readyz", h.ready)
	h.mux.Handle("GET /", admin.Handler())
	h.mux.Handle("GET /v1/models", h.protect(http.HandlerFunc(h.models)))
	h.mux.Handle("POST /v1/chat/completions", h.protect(http.HandlerFunc(h.chatCompletions)))
	h.mux.Handle("POST /v1/messages", h.protect(http.HandlerFunc(h.anthropicMessages)))
	h.mux.Handle("POST /v1beta/models/", h.protect(http.HandlerFunc(h.geminiGenerateContent)))
	h.mux.Handle("GET /admin/providers", h.protect(http.HandlerFunc(h.adminProviders)))
	h.mux.Handle("GET /admin/accounts", h.protect(http.HandlerFunc(h.adminAccounts)))
	h.mux.Handle("POST /admin/accounts", h.protect(http.HandlerFunc(h.createAccount)))
	h.mux.Handle("GET /admin/accounts/{id}", h.protect(http.HandlerFunc(h.getAccount)))
	h.mux.Handle("PATCH /admin/accounts/{id}/status", h.protect(http.HandlerFunc(h.setAccountStatus)))
	h.mux.Handle("DELETE /admin/accounts/{id}", h.protect(http.HandlerFunc(h.deleteAccount)))
	h.mux.Handle("GET /admin/credentials", h.protect(http.HandlerFunc(h.adminCredentials)))
	h.mux.Handle("POST /admin/credentials", h.protect(http.HandlerFunc(h.createCredential)))
	h.mux.Handle("PATCH /admin/credentials/{id}/status", h.protect(http.HandlerFunc(h.setCredentialStatus)))
	h.mux.Handle("DELETE /admin/credentials/{id}", h.protect(http.HandlerFunc(h.deleteCredential)))
	h.mux.Handle("GET /admin/sessions", h.protect(http.HandlerFunc(h.adminSessions)))
	h.mux.Handle("POST /admin/sessions", h.protect(http.HandlerFunc(h.createSession)))
	h.mux.Handle("GET /admin/sessions/{id}", h.protect(http.HandlerFunc(h.getSession)))
	h.mux.Handle("PATCH /admin/sessions/{id}/status", h.protect(http.HandlerFunc(h.setSessionStatus)))
	h.mux.Handle("DELETE /admin/sessions/{id}", h.protect(http.HandlerFunc(h.deleteSession)))
	h.mux.Handle("GET /admin/models", h.protect(http.HandlerFunc(h.adminModels)))
	h.mux.Handle("PATCH /admin/models/{id}/status", h.protect(http.HandlerFunc(h.setModelStatus)))
	h.mux.Handle("DELETE /admin/models/{id}", h.protect(http.HandlerFunc(h.deleteModel)))
	h.mux.Handle("GET /admin/api-keys", h.protect(http.HandlerFunc(h.adminAPIKeys)))
	h.mux.Handle("POST /admin/api-keys", h.protect(http.HandlerFunc(h.createAPIKey)))
	h.mux.Handle("PATCH /admin/api-keys/{id}/status", h.protect(http.HandlerFunc(h.setAPIKeyStatus)))
	h.mux.Handle("DELETE /admin/api-keys/{id}", h.protect(http.HandlerFunc(h.deleteAPIKey)))
	h.mux.Handle("POST /admin/providers", h.protect(http.HandlerFunc(h.createProvider)))
	h.mux.Handle("PATCH /admin/providers/{id}/policy", h.protect(http.HandlerFunc(h.setProviderPolicy)))
	h.mux.Handle("POST /admin/providers/{id}/models/refresh", h.protect(http.HandlerFunc(h.refreshProviderModels)))
	h.mux.Handle("DELETE /admin/providers/{id}", h.protect(http.HandlerFunc(h.deleteProvider)))
	h.mux.Handle("GET /admin/stats", h.protect(http.HandlerFunc(h.adminStats)))
	h.mux.Handle("GET /admin/requests", h.protect(http.HandlerFunc(h.adminRequests)))
	h.mux.Handle("GET /admin/usage", h.protect(http.HandlerFunc(h.adminUsage)))
	return h.metrics.Middleware(h.mux)
}

func (h *Handler) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "time": time.Now().UTC().Format(time.RFC3339)})
}
func (h *Handler) ready(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ready", "providers": h.registry.Count()})
}

func (h *Handler) models(w http.ResponseWriter, r *http.Request) {
	items := make([]provider.Model, 0)
	for _, p := range h.registry.List() {
		models, err := p.ListModels(r.Context())
		if err != nil {
			writeError(w, http.StatusBadGateway, "provider_error", err.Error())
			return
		}
		for _, model := range models {
			items = append(items, model)
		}
	}
	data := make([]map[string]any, 0, len(items))
	for _, model := range items {
		data = append(data, map[string]any{"id": model.ID, "object": "model", "owned_by": model.Provider, "capabilities": model.Capabilities, "context_limit": model.ContextLimit})
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

func (h *Handler) chatCompletions(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, (8<<20)+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "could not read request body")
		return
	}
	if len(body) > 8<<20 {
		writeError(w, http.StatusRequestEntityTooLarge, "invalid_request_error", "request body exceeds 8 MiB")
		return
	}
	var request struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	if err := json.Unmarshal(body, &request); err != nil || strings.TrimSpace(request.Model) == "" {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "valid JSON with a model is required")
		return
	}
	var p provider.Provider
	if h.selector != nil {
		p, err = h.selectProviderFor(r.Context(), request.Model, requiredCapabilities(body))
	} else {
		var ok bool
		p, ok = h.registry.ResolveModel(request.Model)
		if !ok {
			err = http.ErrNoLocation
		}
	}
	if err != nil || p == nil {
		observability.SetRequestDetails(r, func(d *observability.RequestDetails) { d.ModelID = request.Model })
		setRequestError(r, "provider_unavailable")
		writeError(w, http.StatusServiceUnavailable, "provider_unavailable", "no healthy configured provider serves model "+request.Model)
		return
	}
	setRequestRoute(r, p, request.Model)
	if request.Stream {
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeError(w, http.StatusInternalServerError, "server_error", "streaming is not supported by this server")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)
		beginStreamRequest(r)
		var usage *tokenusage.Usage
		terminal := false
		err := p.Stream(r.Context(), &provider.Request{Model: request.Model, Input: body, Stream: true}, func(event provider.Event) error {
			chunk := "data: [DONE]\n\n"
			if !event.Done {
				usage = mergeStreamUsage(r, usage, event.Data, false)
				chunk = string(event.Data) + "\n\n"
			} else {
				terminal = event.ExplicitDone
				if usage != nil {
					usage.Final = event.ExplicitDone
				}
				storeRequestUsage(r, usage)
				if !event.ExplicitDone {
					// Do not signal a successful downstream end unless the upstream
					// protocol supplied its explicit terminal marker.
					return nil
				}
			}
			if _, writeErr := io.WriteString(w, chunk); writeErr != nil {
				return downstreamWriteError{err: writeErr}
			}
			flusher.Flush()
			return nil
		})
		if err != nil {
			storeRequestUsage(r, usage)
			setRequestError(r, classifyRequestError(r, err, true))
			if h.selector != nil {
				h.selector.ReportFailure(p.ID())
			}
		} else if !terminal {
			storeRequestUsage(r, usage)
			setRequestError(r, "stream_incomplete")
		} else {
			completeStreamRequest(r)
		}
		return
	}
	response, p, err := h.generateWithFallback(r.Context(), p, request.Model, requiredCapabilities(body), body)
	if err != nil {
		setRequestRoute(r, p, request.Model)
		setRequestError(r, classifyRequestError(r, err, false))
		writeError(w, http.StatusBadGateway, "upstream_error", err.Error())
		return
	}
	setRequestRoute(r, p, request.Model)
	captureResponseUsage(r, response.Output)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(response.Output)
}

// anthropicMessages exposes Anthropic's Messages request/response envelope over
// any configured OpenAI-compatible upstream. Unsupported block types fail
// explicitly rather than being silently discarded.
func (h *Handler) anthropicMessages(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, (8<<20)+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "could not read request body")
		return
	}
	if len(body) > 8<<20 {
		writeError(w, http.StatusRequestEntityTooLarge, "invalid_request_error", "request body exceeds 8 MiB")
		return
	}
	openAIRequest, model, stream, err := anthropic.ToOpenAI(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	p, err := h.selectProviderFor(r.Context(), model, requiredCapabilities(body))
	if err != nil {
		observability.SetRequestDetails(r, func(d *observability.RequestDetails) { d.ModelID = model })
		setRequestError(r, "provider_unavailable")
		writeError(w, http.StatusServiceUnavailable, "provider_unavailable", "no healthy configured provider serves model "+model)
		return
	}
	setRequestRoute(r, p, model)
	if stream {
		h.streamAnthropic(w, r, p, model, openAIRequest)
		return
	}
	response, p, err := h.generateWithFallback(r.Context(), p, model, requiredCapabilities(body), openAIRequest)
	if err != nil {
		setRequestRoute(r, p, model)
		setRequestError(r, classifyRequestError(r, err, false))
		writeError(w, http.StatusBadGateway, "upstream_error", err.Error())
		return
	}
	setRequestRoute(r, p, model)
	captureResponseUsage(r, response.Output)
	output, err := anthropic.FromOpenAI(response.Output, model)
	if err != nil {
		setRequestError(r, "upstream_protocol_error")
		writeError(w, http.StatusBadGateway, "upstream_error", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(output)
}

// geminiGenerateContent implements the Gemini generateContent and
// streamGenerateContent envelopes at /v1beta/models/{model}.
func (h *Handler) geminiGenerateContent(w http.ResponseWriter, r *http.Request) {
	const prefix = "/v1beta/models/"
	tail := strings.TrimPrefix(r.URL.Path, prefix)
	stream := strings.HasSuffix(tail, ":streamGenerateContent")
	suffix := ":generateContent"
	if stream {
		suffix = ":streamGenerateContent"
	}
	model, ok := strings.CutSuffix(tail, suffix)
	if !ok || strings.TrimSpace(model) == "" || strings.Contains(model, "/") {
		http.NotFound(w, r)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, (8<<20)+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "could not read request body")
		return
	}
	if len(body) > 8<<20 {
		writeError(w, http.StatusRequestEntityTooLarge, "invalid_request_error", "request body exceeds 8 MiB")
		return
	}
	payload, err := gemini.ToOpenAIStream(body, model, stream)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	required := requiredCapabilities(body)
	if stream {
		required = append(required, "streaming")
	}
	p, err := h.selectProviderFor(r.Context(), model, required)
	if err != nil {
		observability.SetRequestDetails(r, func(d *observability.RequestDetails) { d.ModelID = model })
		setRequestError(r, "provider_unavailable")
		writeError(w, http.StatusServiceUnavailable, "provider_unavailable", "no healthy configured provider serves model "+model)
		return
	}
	setRequestRoute(r, p, model)
	if stream {
		h.streamGemini(w, r, p, model, payload)
		return
	}
	response, p, err := h.generateWithFallback(r.Context(), p, model, required, payload)
	if err != nil {
		setRequestRoute(r, p, model)
		setRequestError(r, classifyRequestError(r, err, false))
		writeError(w, http.StatusBadGateway, "upstream_error", err.Error())
		return
	}
	setRequestRoute(r, p, model)
	captureResponseUsage(r, response.Output)
	output, err := gemini.FromOpenAI(response.Output)
	if err != nil {
		setRequestError(r, "upstream_protocol_error")
		writeError(w, http.StatusBadGateway, "upstream_error", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(output)
}

// generateWithFallback retries a failed non-streaming request on another
// healthy provider for the same model. It never retries a provider twice in
// one request and preserves the original capability requirements.
func (h *Handler) generateWithFallback(ctx context.Context, initial provider.Provider, model string, required []string, payload []byte) (*provider.Response, provider.Provider, error) {
	if initial == nil {
		return nil, nil, fmt.Errorf("no provider selected")
	}
	current := initial
	attempted := make(map[string]struct{}, h.registry.Count())
	var lastErr error
	maxAttempts := h.registry.Count()
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if _, exists := attempted[current.ID()]; exists {
			break
		}
		attempted[current.ID()] = struct{}{}
		response, err := current.Generate(ctx, &provider.Request{Model: model, Input: payload})
		if err == nil && response == nil {
			err = fmt.Errorf("provider %q returned an empty response", current.ID())
		}
		if err == nil {
			return response, current, nil
		}
		lastErr = err
		if h.selector == nil || ctx.Err() != nil {
			break
		}
		h.selector.ReportFailure(current.ID())
		next, selectErr := h.selectProviderFor(ctx, model, required)
		if selectErr != nil {
			break
		}
		if _, exists := attempted[next.ID()]; exists {
			break
		}
		current = next
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no alternate healthy provider is available")
	}
	return nil, current, lastErr
}

func (h *Handler) streamAnthropic(w http.ResponseWriter, r *http.Request, p provider.Provider, model string, payload []byte) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "server_error", "streaming is not supported by this server")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	translator := anthropic.NewStreamTranslator(model)
	beginStreamRequest(r)
	var usage *tokenusage.Usage
	terminal := false
	err := p.Stream(r.Context(), &provider.Request{Model: model, Input: payload, Stream: true}, func(event provider.Event) error {
		if event.Done {
			terminal = event.ExplicitDone
			if usage != nil {
				usage.Final = event.ExplicitDone
			}
			storeRequestUsage(r, usage)
			if !event.ExplicitDone {
				return nil
			}
		} else {
			usage = mergeStreamUsage(r, usage, event.Data, false)
		}
		if event.Done {
			for _, frame := range translator.Finish() {
				if _, err := w.Write(frame); err != nil {
					return downstreamWriteError{err: err}
				}
			}
			flusher.Flush()
			return nil
		}
		data := eventData(event.Data)
		frames, err := translator.Process(data)
		if err != nil {
			return err
		}
		for _, frame := range frames {
			if _, err := w.Write(frame); err != nil {
				return downstreamWriteError{err: err}
			}
		}
		flusher.Flush()
		return nil
	})
	if err != nil {
		storeRequestUsage(r, usage)
		setRequestError(r, classifyRequestError(r, err, true))
		if h.selector != nil {
			h.selector.ReportFailure(p.ID())
		}
	} else if !terminal {
		storeRequestUsage(r, usage)
		setRequestError(r, "stream_incomplete")
	} else {
		completeStreamRequest(r)
	}
}

func (h *Handler) streamGemini(w http.ResponseWriter, r *http.Request, p provider.Provider, model string, payload []byte) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "server_error", "streaming is not supported by this server")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	translator := gemini.NewStreamTranslator()
	beginStreamRequest(r)
	var usage *tokenusage.Usage
	terminal := false
	err := p.Stream(r.Context(), &provider.Request{Model: model, Input: payload, Stream: true}, func(event provider.Event) error {
		if event.Done {
			terminal = event.ExplicitDone
			if usage != nil {
				usage.Final = event.ExplicitDone
			}
			storeRequestUsage(r, usage)
			if !event.ExplicitDone {
				return nil
			}
		} else {
			usage = mergeStreamUsage(r, usage, event.Data, false)
		}
		if event.Done {
			final, finishErr := translator.Finish()
			if finishErr != nil {
				return finishErr
			}
			if len(final) > 0 {
				if _, writeErr := io.WriteString(w, "data: "+string(final)+"\n\n"); writeErr != nil {
					return downstreamWriteError{err: writeErr}
				}
			}
			_, err := io.WriteString(w, "data: [DONE]\n\n")
			flusher.Flush()
			if err != nil {
				return downstreamWriteError{err: err}
			}
			return nil
		}
		converted, err := translator.Process(eventData(event.Data))
		if err != nil {
			return err
		}
		if len(converted) == 0 {
			return nil
		}
		if _, err = io.WriteString(w, "data: "+string(converted)+"\n\n"); err != nil {
			return downstreamWriteError{err: err}
		}
		flusher.Flush()
		return nil
	})
	if err != nil {
		storeRequestUsage(r, usage)
		setRequestError(r, classifyRequestError(r, err, true))
		if h.selector != nil {
			h.selector.ReportFailure(p.ID())
		}
	} else if !terminal {
		storeRequestUsage(r, usage)
		setRequestError(r, "stream_incomplete")
	} else {
		completeStreamRequest(r)
	}
}

func setRequestRoute(r *http.Request, p provider.Provider, model string) {
	observability.SetRequestDetails(r, func(d *observability.RequestDetails) {
		if p != nil {
			d.ProviderID = p.ID()
		}
		d.ModelID = model
	})
}

func beginStreamRequest(r *http.Request) {
	observability.SetRequestDetails(r, func(d *observability.RequestDetails) { d.LifecycleStatus = "streaming" })
}

func completeStreamRequest(r *http.Request) {
	observability.SetRequestDetails(r, func(d *observability.RequestDetails) { d.LifecycleStatus = "completed" })
}

func setRequestError(r *http.Request, code string) {
	observability.SetRequestDetails(r, func(d *observability.RequestDetails) {
		d.ErrorCode = code
		switch code {
		case "client_disconnected":
			d.LifecycleStatus = "interrupted"
		case "timeout":
			d.LifecycleStatus = "timeout"
		case "stream_incomplete", "upstream_stream_error", "upstream_error", "upstream_protocol_error":
			d.LifecycleStatus = "failed"
		case "usage_parse_error":
			// Telemetry parsing failure does not turn a successful inference into a failed request.
		default:
			d.LifecycleStatus = "failed"
		}
	})
}

type downstreamWriteError struct{ err error }

func (e downstreamWriteError) Error() string {
	return "downstream response write failed: " + e.err.Error()
}
func (e downstreamWriteError) Unwrap() error { return e.err }

func classifyRequestError(r *http.Request, err error, streaming bool) string {
	var downstreamErr downstreamWriteError
	if errors.As(err, &downstreamErr) {
		return "client_disconnected"
	}
	if errors.Is(r.Context().Err(), context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(r.Context().Err(), context.Canceled) {
		return "client_disconnected"
	}
	if streaming {
		return "upstream_stream_error"
	}
	return "upstream_error"
}

func mergeStreamUsage(r *http.Request, current *tokenusage.Usage, raw []byte, final bool) *tokenusage.Usage {
	data := eventData(raw)
	next, err := tokenusage.ParseOpenAI(data, final)
	if err != nil {
		setRequestError(r, "usage_parse_error")
		return current
	}
	return tokenusage.Merge(current, next)
}

func storeRequestUsage(r *http.Request, usage *tokenusage.Usage) {
	if usage == nil || usage.Estimated {
		return
	}
	observability.SetRequestDetails(r, func(d *observability.RequestDetails) {
		if usage.InputTokens != nil {
			value := *usage.InputTokens
			d.InputTokens = &value
		}
		if usage.OutputTokens != nil {
			value := *usage.OutputTokens
			d.OutputTokens = &value
		}
		d.UsageSource = usage.Source
		d.UsageFinal = usage.Final
	})
}

func captureResponseUsage(r *http.Request, raw []byte) {
	usage, err := tokenusage.ParseOpenAI(raw, true)
	if err != nil {
		setRequestError(r, "usage_parse_error")
		return
	}
	storeRequestUsage(r, usage)
}

func eventData(raw []byte) []byte {
	var lines []string
	for _, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data:") {
			lines = append(lines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	return []byte(strings.Join(lines, "\n"))
}

func (h *Handler) selectProviderFor(ctx context.Context, model string, required []string) (provider.Provider, error) {
	if selector, ok := h.selector.(interface {
		SelectWithRequirements(context.Context, string, []string) (provider.Provider, error)
	}); ok {
		return selector.SelectWithRequirements(ctx, model, required)
	}
	return h.selectProvider(ctx, model)
}

// requiredCapabilities performs conservative structural detection. It does not
// inspect or transform payload contents; it only prevents routing to adapters
// that have not declared the requested feature.
func requiredCapabilities(body []byte) []string {
	var value any
	if json.Unmarshal(body, &value) != nil {
		return nil
	}
	found := map[string]bool{}
	var walk func(any)
	walk = func(v any) {
		switch item := v.(type) {
		case map[string]any:
			for key, child := range item {
				switch key {
				case "tools", "toolConfig", "functionDeclarations":
					if list, ok := child.([]any); !ok || len(list) > 0 {
						found["tools"] = true
					}
				case "tool_choice":
					found["tools"] = true
				case "response_format":
					if child != nil {
						found["json"] = true
					}
				case "image_url", "inline_data", "inlineData", "fileData":
					found["vision"] = true
				case "type":
					if blockType, ok := child.(string); ok && blockType == "image" {
						found["vision"] = true
					}
				case "stream":
					if enabled, ok := child.(bool); ok && enabled {
						found["streaming"] = true
					}
				}
				walk(child)
			}
		case []any:
			for _, child := range item {
				walk(child)
			}
		}
	}
	walk(value)
	out := make([]string, 0, len(found))
	for _, capability := range []string{"streaming", "tools", "json", "vision"} {
		if found[capability] {
			out = append(out, capability)
		}
	}
	return out
}

func (h *Handler) selectProvider(ctx context.Context, model string) (provider.Provider, error) {
	if h.selector != nil {
		return h.selector.Select(ctx, model)
	}
	p, ok := h.registry.ResolveModel(model)
	if !ok {
		return nil, http.ErrNoLocation
	}
	return p, nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"type": code, "message": message}})
}

func (h *Handler) lookupAccount(ctx context.Context, id string) (account.Account, error) {
	if h.accountRepo != nil {
		return h.accountRepo.Get(ctx, id)
	}
	item, ok := h.accounts.Get(id)
	if !ok {
		return account.Account{}, sql.ErrNoRows
	}
	return item, nil
}

func (h *Handler) protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Preserve the explicitly configured open mode for deployments that have
		// not enabled a master key. Once enabled, every protected route requires
		// either the master key or a persisted key with the route's exact scope.
		if h.apiKey == "" {
			next.ServeHTTP(w, r)
			return
		}
		provided := bearerToken(r.Header.Get("Authorization"))
		if provided == "" {
			writeError(w, http.StatusUnauthorized, "authentication_error", "valid bearer token required")
			return
		}
		if subtle.ConstantTimeCompare([]byte(provided), []byte(h.apiKey)) == 1 {
			next.ServeHTTP(w, r)
			return
		}
		if h.apiKeyRepo == nil {
			writeError(w, http.StatusUnauthorized, "authentication_error", "valid bearer token required")
			return
		}
		required := requiredAPIScope(r)
		if required == "" {
			writeError(w, http.StatusForbidden, "authorization_error", "this route does not accept scoped API keys")
			return
		}
		key, err := h.apiKeyRepo.Authenticate(r.Context(), provided)
		if err != nil || !apikey.HasScope(key.Scopes, required) {
			writeError(w, http.StatusForbidden, "authorization_error", "API key does not have the required scope")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func bearerToken(header string) string {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

func requiredAPIScope(r *http.Request) string {
	if strings.HasPrefix(r.URL.Path, "/v1/") || strings.HasPrefix(r.URL.Path, "/v1beta/") {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			return apikey.ScopeGatewayRead
		}
		return apikey.ScopeGatewayWrite
	}
	if strings.HasPrefix(r.URL.Path, "/admin/") {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			return apikey.ScopeAdminRead
		}
		return apikey.ScopeAdminWrite
	}
	return ""
}

// Account endpoints manage durable metadata when SQL repositories are configured;
// credential values remain isolated in the encrypted vault.
func (h *Handler) adminAccounts(w http.ResponseWriter, r *http.Request) {
	providerID := strings.TrimSpace(r.URL.Query().Get("provider_id"))
	if providerID != "" {
		if _, ok := h.registry.Get(providerID); !ok {
			writeError(w, http.StatusNotFound, "not_found", "provider not found")
			return
		}
	}
	if h.accountRepo != nil {
		items, err := h.accountRepo.List(r.Context(), providerID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "storage_error", "could not list accounts")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": items, "persisted": true})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": h.accounts.List(providerID), "persisted": false})
}

func (h *Handler) createAccount(w http.ResponseWriter, r *http.Request) {
	h.mutationMu.Lock()
	defer h.mutationMu.Unlock()
	var input struct {
		ID         string         `json:"id"`
		ProviderID string         `json:"provider_id"`
		Name       string         `json:"name"`
		Status     account.Status `json:"status"`
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid account JSON: "+err.Error())
		return
	}
	if _, ok := h.registry.Get(strings.TrimSpace(input.ProviderID)); !ok {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "provider_id must reference a configured provider")
		return
	}
	item, err := account.NewStore().Create(account.Account{ID: input.ID, ProviderID: input.ProviderID, Name: input.Name, Status: input.Status})
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	if h.accountRepo != nil {
		if err := h.accountRepo.Create(r.Context(), item); err != nil {
			writeError(w, http.StatusConflict, "storage_error", "could not persist account")
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"data": item, "persisted": true})
		return
	}
	item, err = h.accounts.Create(item)
	if err != nil {
		writeError(w, http.StatusConflict, "conflict", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"data": item, "persisted": false})
}

func (h *Handler) getAccount(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if h.accountRepo != nil {
		item, err := h.accountRepo.Get(r.Context(), id)
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", "account not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "storage_error", "could not load account")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": item, "persisted": true})
		return
	}
	item, ok := h.accounts.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "account not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": item, "persisted": false})
}

func (h *Handler) setAccountStatus(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Status account.Status `json:"status"`
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid status JSON")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if h.accountRepo != nil {
		if err := h.accountRepo.SetStatus(r.Context(), id, input.Status); errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", "account not found")
			return
		} else if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}
		item, err := h.accountRepo.Get(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "storage_error", "could not reload account")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": item, "persisted": true})
		return
	}
	item, ok, err := h.accounts.SetStatus(id, input.Status)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "account not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": item, "persisted": false})
}

func (h *Handler) deleteAccount(w http.ResponseWriter, r *http.Request) {
	h.mutationMu.Lock()
	defer h.mutationMu.Unlock()
	id := strings.TrimSpace(r.PathValue("id"))
	if h.accountRepo != nil {
		var sessions []session.Session
		var err error
		if h.sessionRepo != nil {
			sessions, err = h.sessionRepo.List(r.Context(), id)
		} else {
			sessions = h.sessions.ListByAccount(id)
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "storage_error", "could not inspect account sessions")
			return
		}
		if len(sessions) > 0 {
			writeError(w, http.StatusConflict, "conflict", "delete account sessions before deleting the account")
			return
		}
		credentialCount, err := h.accountRepo.CountCredentials(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "storage_error", "could not inspect account credentials")
			return
		}
		if credentialCount > 0 {
			writeError(w, http.StatusConflict, "conflict", "delete account credentials before deleting the account")
			return
		}
		err = h.accountRepo.Delete(r.Context(), id)
		if errors.Is(err, account.ErrHasDependencies) {
			writeError(w, http.StatusConflict, "conflict", "delete account sessions and credentials before deleting the account")
			return
		}
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", "account not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "storage_error", "could not delete account")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": id, "persisted": true})
		return
	}
	if len(h.sessions.ListByAccount(id)) > 0 {
		writeError(w, http.StatusConflict, "conflict", "delete account sessions before deleting the account")
		return
	}
	if h.credentials != nil && len(h.credentials.List(id)) > 0 {
		writeError(w, http.StatusConflict, "conflict", "delete account credentials before deleting the account")
		return
	}
	if !h.accounts.Delete(id) {
		writeError(w, http.StatusNotFound, "not_found", "account not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": id, "persisted": false})
}

// Credential endpoints accept secrets only on the write path. Responses and
// list operations expose metadata only; plaintext is never returned here.
func (h *Handler) adminCredentials(w http.ResponseWriter, r *http.Request) {
	if h.apiKey == "" {
		writeError(w, http.StatusServiceUnavailable, "admin_auth_required", "set OMNI_GATEWAY_API_KEY before enabling credential management")
		return
	}
	if h.credentials == nil {
		writeError(w, http.StatusServiceUnavailable, "credential_vault_unavailable", "configure OMNI_CREDENTIAL_ENCRYPTION_KEY to enable credential management")
		return
	}
	accountID := strings.TrimSpace(r.URL.Query().Get("account_id"))
	if accountID != "" {
		if _, err := h.lookupAccount(r.Context(), accountID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeError(w, http.StatusNotFound, "not_found", "account not found")
			} else {
				writeError(w, http.StatusInternalServerError, "storage_error", "could not load account")
			}
			return
		}
	}
	items, err := h.credentials.ListContext(r.Context(), accountID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", "could not list credentials")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items, "persisted": h.credentials.Persistent()})
}

func (h *Handler) createCredential(w http.ResponseWriter, r *http.Request) {
	h.mutationMu.Lock()
	defer h.mutationMu.Unlock()
	if h.apiKey == "" {
		writeError(w, http.StatusServiceUnavailable, "admin_auth_required", "set OMNI_GATEWAY_API_KEY before enabling credential management")
		return
	}
	if h.credentials == nil {
		writeError(w, http.StatusServiceUnavailable, "credential_vault_unavailable", "configure OMNI_CREDENTIAL_ENCRYPTION_KEY to enable credential management")
		return
	}
	var input struct {
		ID        string          `json:"id"`
		AccountID string          `json:"account_id"`
		Kind      credential.Kind `json:"kind"`
		Value     string          `json:"value"`
		ExpiresAt *time.Time      `json:"expires_at"`
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid credential JSON")
		return
	}
	if _, err := h.lookupAccount(r.Context(), strings.TrimSpace(input.AccountID)); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusBadRequest, "invalid_request_error", "account_id must reference an existing account")
		} else {
			writeError(w, http.StatusInternalServerError, "storage_error", "could not verify account")
		}
		return
	}
	if input.Value == "" {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "value is required")
		return
	}
	meta, err := h.credentials.Put(credential.Metadata{ID: input.ID, AccountID: input.AccountID, Type: string(input.Kind), ExpiresAt: input.ExpiresAt}, input.Kind, []byte(input.Value))
	input.Value = ""
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"data": meta, "persisted": h.credentials.Persistent()})
}

func (h *Handler) setCredentialStatus(w http.ResponseWriter, r *http.Request) {
	if h.apiKey == "" {
		writeError(w, http.StatusServiceUnavailable, "admin_auth_required", "set OMNI_GATEWAY_API_KEY before enabling credential management")
		return
	}
	if h.credentials == nil {
		writeError(w, http.StatusServiceUnavailable, "credential_vault_unavailable", "credential vault is not configured")
		return
	}
	var input struct {
		Status credential.Status `json:"status"`
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid credential status JSON")
		return
	}
	item, ok, err := h.credentials.SetStatus(strings.TrimSpace(r.PathValue("id")), input.Status)
	if err != nil {
		writeError(w, http.StatusConflict, "invalid_state_transition", err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "credential not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": item, "persisted": h.credentials.Persistent()})
}

func (h *Handler) deleteCredential(w http.ResponseWriter, r *http.Request) {
	if h.apiKey == "" {
		writeError(w, http.StatusServiceUnavailable, "admin_auth_required", "set OMNI_GATEWAY_API_KEY before enabling credential management")
		return
	}
	if h.credentials == nil {
		writeError(w, http.StatusServiceUnavailable, "credential_vault_unavailable", "credential vault is not configured")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	deleted, err := h.credentials.DeleteContext(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", "could not delete credential")
		return
	}
	if !deleted {
		writeError(w, http.StatusNotFound, "not_found", "credential not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": id, "persisted": h.credentials.Persistent()})
}

// Session endpoints persist lifecycle metadata; they do not authenticate upstream.
func (h *Handler) adminSessions(w http.ResponseWriter, r *http.Request) {
	accountID := strings.TrimSpace(r.URL.Query().Get("account_id"))
	if accountID != "" {
		if h.accountRepo != nil {
			if _, err := h.accountRepo.Get(r.Context(), accountID); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					writeError(w, http.StatusNotFound, "not_found", "account not found")
				} else {
					writeError(w, http.StatusInternalServerError, "storage_error", "could not load account")
				}
				return
			}
		} else if _, ok := h.accounts.Get(accountID); !ok {
			writeError(w, http.StatusNotFound, "not_found", "account not found")
			return
		}
	}
	if h.sessionRepo != nil {
		items, err := h.sessionRepo.List(r.Context(), accountID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "storage_error", "could not list sessions")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": items, "persisted": true})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": h.sessions.ListByAccount(accountID), "persisted": false})
}

func (h *Handler) createSession(w http.ResponseWriter, r *http.Request) {
	h.mutationMu.Lock()
	defer h.mutationMu.Unlock()
	var input struct {
		ID        string         `json:"id"`
		AccountID string         `json:"account_id"`
		Status    session.Status `json:"status"`
		ExpiresAt *time.Time     `json:"expires_at"`
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid session JSON: "+err.Error())
		return
	}
	if h.accountRepo != nil {
		if _, err := h.accountRepo.Get(r.Context(), strings.TrimSpace(input.AccountID)); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeError(w, http.StatusBadRequest, "invalid_request_error", "account_id must reference an existing account")
			} else {
				writeError(w, http.StatusInternalServerError, "storage_error", "could not verify account")
			}
			return
		}
	} else if _, ok := h.accounts.Get(strings.TrimSpace(input.AccountID)); !ok {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "account_id must reference an existing account")
		return
	}
	item, err := session.NewStore().Create(session.Session{ID: input.ID, AccountID: input.AccountID, Status: input.Status, ExpiresAt: input.ExpiresAt})
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	if h.sessionRepo != nil {
		if err := h.sessionRepo.Create(r.Context(), item); err != nil {
			writeError(w, http.StatusConflict, "storage_error", "could not persist session")
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"data": item, "persisted": true})
		return
	}
	item, err = h.sessions.Create(item)
	if err != nil {
		writeError(w, http.StatusConflict, "conflict", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"data": item, "persisted": false})
}

func (h *Handler) getSession(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if h.sessionRepo != nil {
		item, err := h.sessionRepo.Get(r.Context(), id)
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", "session not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "storage_error", "could not load session")
			return
		}
		if item.Status == session.StatusActive && item.ExpiresAt != nil && !item.ExpiresAt.After(time.Now()) {
			item, err = h.sessionRepo.Transition(r.Context(), id, session.StatusExpired)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "storage_error", "could not expire session")
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": item, "persisted": true})
		return
	}
	item, ok := h.sessions.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "session not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": item, "persisted": false})
}

func (h *Handler) setSessionStatus(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Status session.Status `json:"status"`
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid session status JSON")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if h.sessionRepo != nil {
		item, err := h.sessionRepo.Transition(r.Context(), id, input.Status)
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", "session not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusConflict, "invalid_state_transition", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": item, "persisted": true})
		return
	}
	item, ok, err := h.sessions.Transition(id, input.Status)
	if err != nil {
		writeError(w, http.StatusConflict, "invalid_state_transition", err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "session not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": item, "persisted": false})
}

func (h *Handler) deleteSession(w http.ResponseWriter, r *http.Request) {
	h.mutationMu.Lock()
	defer h.mutationMu.Unlock()
	id := strings.TrimSpace(r.PathValue("id"))
	if h.sessionRepo != nil {
		err := h.sessionRepo.Delete(r.Context(), id)
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", "session not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "storage_error", "could not delete session")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": id, "persisted": true})
		return
	}
	if !h.sessions.Delete(id) {
		writeError(w, http.StatusNotFound, "not_found", "session not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": id, "persisted": false})
}

func (h *Handler) adminProviders(w http.ResponseWriter, r *http.Request) {
	if h.providerRepo != nil {
		items, err := h.providerRepo.List(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "storage_error", "could not list provider metadata")
			return
		}
		credentialsPersisted := true
		for _, item := range items {
			if item.CredentialID != "" && (h.credentials == nil || !h.credentials.Persistent()) {
				credentialsPersisted = false
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": items, "persisted": true, "credentials_persisted": credentialsPersisted})
		return
	}
	items := make([]map[string]string, 0)
	for _, p := range h.registry.List() {
		items = append(items, map[string]string{"id": p.ID(), "name": p.Name()})
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items, "persisted": false})
}

// createProvider installs a typed upstream adapter and persists its metadata.
// Upstream secrets are stored only through the encrypted credential vault.
func (h *Handler) createProvider(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Kind               string              `json:"kind"`
		ID                 string              `json:"id"`
		Name               string              `json:"name"`
		BaseURL            string              `json:"base_url"`
		APIKey             string              `json:"api_key"`
		Models             []string            `json:"models"`
		ModelCapabilities  map[string][]string `json:"model_capabilities"`
		ModelContextLimits map[string]int      `json:"model_context_limits"`
		Priority           int                 `json:"priority"`
		Weight             uint32              `json:"weight"`
		Enabled            *bool               `json:"enabled"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid provider JSON")
		return
	}
	kind := adapter.NormalizeKind(input.Kind)
	if strings.TrimSpace(input.BaseURL) == "" { input.BaseURL = adapter.DefaultBaseURL(kind) }
	if strings.TrimSpace(input.ID) == "" || strings.TrimSpace(input.BaseURL) == "" || (len(input.Models) == 0 && kind != adapter.KindDeepSeek) {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "id and base_url are required; models are required except for DeepSeek model discovery")
		return
	}
	upstream, err := adapter.New(adapter.Config{Kind: kind, ID: input.ID, Name: input.Name, BaseURL: input.BaseURL, APIKey: input.APIKey, Models: input.Models, ModelCapabilities: input.ModelCapabilities, ModelContextLimits: input.ModelContextLimits})
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	h.mutationMu.Lock()
	defer h.mutationMu.Unlock()
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	providerName := strings.TrimSpace(input.Name)
	if providerName == "" {
		providerName = strings.TrimSpace(input.ID)
	}
	if h.providerRepo != nil {
		if input.APIKey != "" && (h.credentials == nil || !h.credentials.Persistent() || h.accountRepo == nil) {
			writeError(w, http.StatusServiceUnavailable, "credential_persistence_required", "configure persistent credential encryption before storing provider API keys")
			return
		}
		record := provider.ProviderConfig{ID: input.ID, Name: providerName, Kind: kind, BaseURL: input.BaseURL, Enabled: enabled, Priority: input.Priority, Weight: input.Weight}
		if err := h.providerRepo.Upsert(r.Context(), record); err != nil {
			writeError(w, http.StatusInternalServerError, "storage_error", "could not persist provider metadata")
			return
		}
		if input.APIKey != "" {
			accountID, credentialID := provider.BindingIDs(input.ID)
			if _, getErr := h.accountRepo.Get(r.Context(), accountID); errors.Is(getErr, sql.ErrNoRows) {
				if createErr := h.accountRepo.Create(r.Context(), account.Account{ID: accountID, ProviderID: input.ID, Name: providerName + " credentials", Status: account.StatusActive}); createErr != nil {
					writeError(w, http.StatusConflict, "storage_error", "could not create provider account")
					return
				}
			} else if getErr != nil {
				writeError(w, http.StatusInternalServerError, "storage_error", "could not load provider account")
				return
			}
			secret := []byte(input.APIKey)
			_, putErr := h.credentials.Put(credential.Metadata{ID: credentialID, AccountID: accountID, Type: string(credential.KindAPIKey)}, credential.KindAPIKey, secret)
			for i := range secret {
				secret[i] = 0
			}
			if putErr != nil {
				writeError(w, http.StatusInternalServerError, "credential_error", "could not persist provider credential")
				return
			}
			record.AccountID, record.CredentialID = accountID, credentialID
		}
		if err := h.providerRepo.Upsert(r.Context(), record); err != nil {
			writeError(w, http.StatusInternalServerError, "storage_error", "could not persist provider metadata")
			return
		}
	}
	// Updating provider metadata without supplying a replacement API key keeps
	// the previously bound encrypted credential active.
	if h.providerRepo != nil && input.APIKey == "" {
		record, getErr := h.providerRepo.Get(r.Context(), input.ID)
		if getErr != nil {
			writeError(w, http.StatusInternalServerError, "storage_error", "could not reload provider binding")
			return
		}
		if record.AccountID != "" && record.CredentialID == "" {
			writeError(w, http.StatusConflict, "credential_error", "provider account exists but its credential binding is missing")
			return
		}
		if record.CredentialID != "" {
			if h.credentials == nil {
				writeError(w, http.StatusServiceUnavailable, "credential_vault_unavailable", "provider credential encryption is not configured")
				return
			}
			_, secret, revealErr := h.credentials.Reveal(record.CredentialID)
			if revealErr != nil {
				writeError(w, http.StatusConflict, "credential_error", "stored provider credential is unavailable")
				return
			}
			upstream, err = adapter.New(adapter.Config{Kind: kind, ID: input.ID, Name: providerName, BaseURL: input.BaseURL, APIKey: string(secret), Models: input.Models, ModelCapabilities: input.ModelCapabilities, ModelContextLimits: input.ModelContextLimits})
			for i := range secret {
				secret[i] = 0
			}
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
				return
			}
		}
	}
	models, _ := upstream.ListModels(r.Context())
	if h.modelRepo != nil {
		for _, item := range models {
			if err := h.modelRepo.Upsert(r.Context(), domainmodel.Model{ID: item.ID, ProviderID: input.ID, DisplayName: modelDisplayName(item), ContextLimit: item.ContextLimit, Enabled: enabled}); err != nil {
				writeError(w, http.StatusInternalServerError, "storage_error", "could not persist provider model")
				return
			}
			if err := h.modelRepo.SetCapabilities(r.Context(), item.ID, input.ID, item.Capabilities); err != nil {
				writeError(w, http.StatusInternalServerError, "storage_error", "could not persist provider model capabilities")
				return
			}
		}
	}
	if err := h.registry.Replace(upstream); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	if manager, ok := h.selector.(interface {
		AddOrReplaceProviderModels(provider.Provider, []provider.Model, int, uint32, bool)
	}); ok {
		manager.AddOrReplaceProviderModels(upstream, models, input.Priority, input.Weight, enabled)
	} else if manager, ok := h.selector.(interface {
		AddOrReplaceProvider(provider.Provider, []string, int, uint32, bool)
	}); ok {
		manager.AddOrReplaceProvider(upstream, input.Models, input.Priority, input.Weight, enabled)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": upstream.ID(), "name": upstream.Name(), "kind": kind, "models": input.Models, "model_capabilities": input.ModelCapabilities, "model_context_limits": input.ModelContextLimits, "enabled": enabled, "persisted": h.providerRepo != nil, "credentials_persisted": h.providerRepo != nil && (input.APIKey == "" || (h.credentials != nil && h.credentials.Persistent()))})
}

func (h *Handler) refreshProviderModels(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "provider id is required")
		return
	}
	upstream, ok := h.registry.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "provider not found")
		return
	}
	discoverer, ok := upstream.(interface {
		DiscoverModels(context.Context) ([]provider.Model, error)
	})
	if !ok {
		writeError(w, http.StatusBadRequest, "unsupported_operation", "this Provider adapter does not support remote model discovery")
		return
	}
	models, err := discoverer.DiscoverModels(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "provider_discovery_failed", "could not refresh the upstream model catalogue")
		return
	}
	priority, weight, enabled := 100, uint32(1), true
	if h.providerRepo != nil {
		record, getErr := h.providerRepo.Get(r.Context(), id)
		if getErr != nil {
			writeError(w, http.StatusInternalServerError, "storage_error", "could not load provider policy")
			return
		}
		priority, weight, enabled = normalizedPriority(record.Priority), normalizedWeight(record.Weight), record.Enabled
	}
	if h.modelRepo != nil {
		for _, item := range models {
			if err := h.modelRepo.Upsert(r.Context(), domainmodel.Model{ID: item.ID, ProviderID: id, DisplayName: modelDisplayName(item), ContextLimit: item.ContextLimit, Enabled: enabled}); err != nil {
				writeError(w, http.StatusInternalServerError, "storage_error", "could not persist discovered model")
				return
			}
			if err := h.modelRepo.SetCapabilities(r.Context(), item.ID, id, item.Capabilities); err != nil {
				writeError(w, http.StatusInternalServerError, "storage_error", "could not persist discovered model capabilities")
				return
			}
		}
	}
	if manager, ok := h.selector.(interface {
		AddOrReplaceProviderModels(provider.Provider, []provider.Model, int, uint32, bool)
	}); ok {
		manager.AddOrReplaceProviderModels(upstream, models, priority, weight, enabled)
	}
	writeJSON(w, http.StatusOK, map[string]any{"provider_id": id, "data": models, "count": len(models), "persisted": h.modelRepo != nil})
}

func modelDisplayName(item provider.Model) string {
	if name := strings.TrimSpace(item.Name); name != "" { return name }
	return item.ID
}

func (h *Handler) setProviderPolicy(w http.ResponseWriter, r *http.Request) {
	if h.providerRepo == nil {
		writeError(w, http.StatusServiceUnavailable, "storage_unavailable", "provider repository is unavailable")
		return
	}
	var input struct {
		Enabled  bool   `json:"enabled"`
		Priority int    `json:"priority"`
		Weight   uint32 `json:"weight"`
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid provider policy JSON")
		return
	}
	if input.Priority < 0 || input.Weight > 1000000 {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "priority must be non-negative and weight must not exceed 1000000")
		return
	}
	h.mutationMu.Lock()
	defer h.mutationMu.Unlock()
	id := strings.TrimSpace(r.PathValue("id"))
	if _, err := h.providerRepo.Get(r.Context(), id); errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "provider not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", "could not load provider")
		return
	}
	var upstreamModels []provider.Model
	upstream, registered := h.registry.Get(id)
	if registered {
		var listErr error
		upstreamModels, listErr = upstream.ListModels(r.Context())
		if listErr != nil {
			writeError(w, http.StatusBadGateway, "provider_error", "could not refresh provider models; policy was not changed")
			return
		}
	}
	if err := h.providerRepo.SetPolicy(r.Context(), id, input.Enabled, input.Priority, input.Weight); err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", "could not update provider policy")
		return
	}
	if registered {
		if manager, ok := h.selector.(interface {
			AddOrReplaceProviderModels(provider.Provider, []provider.Model, int, uint32, bool)
		}); ok {
			manager.AddOrReplaceProviderModels(upstream, upstreamModels, input.Priority, input.Weight, input.Enabled)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "enabled": input.Enabled, "priority": normalizedPriority(input.Priority), "weight": normalizedWeight(input.Weight), "persisted": true})
}

func normalizedPriority(value int) int {
	if value == 0 {
		return 100
	}
	return value
}
func normalizedWeight(value uint32) uint32 {
	if value == 0 {
		return 1
	}
	return value
}

func (h *Handler) deleteProvider(w http.ResponseWriter, r *http.Request) {
	h.mutationMu.Lock()
	defer h.mutationMu.Unlock()
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "provider id is required")
		return
	}
	_, registered := h.registry.Get(id)
	var providerRecord provider.ProviderConfig
	if h.providerRepo != nil {
		var getErr error
		providerRecord, getErr = h.providerRepo.Get(r.Context(), id)
		if errors.Is(getErr, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", "provider not found")
			return
		}
		if getErr != nil {
			writeError(w, http.StatusInternalServerError, "storage_error", "could not load provider metadata")
			return
		}
	} else if !registered {
		writeError(w, http.StatusNotFound, "not_found", "provider not found")
		return
	}
	if h.accountRepo != nil {
		if h.providerRepo != nil {
			count, countErr := h.accountRepo.CountByProvider(r.Context(), id)
			if countErr != nil {
				writeError(w, http.StatusInternalServerError, "storage_error", "could not inspect provider accounts")
				return
			}
			if providerRecord.AccountID == "" && count > 0 || providerRecord.AccountID != "" && count > 1 {
				writeError(w, http.StatusConflict, "conflict", "delete or reassign non-provider-owned accounts before deleting the provider")
				return
			}
			if providerRecord.AccountID != "" {
				credentialCount, countErr := h.accountRepo.CountCredentials(r.Context(), providerRecord.AccountID)
				if countErr != nil {
					writeError(w, http.StatusInternalServerError, "storage_error", "could not inspect provider credentials")
					return
				}
				if providerRecord.CredentialID != "" && credentialCount > 1 || providerRecord.CredentialID == "" && credentialCount > 0 {
					writeError(w, http.StatusConflict, "conflict", "delete or reassign additional account credentials before deleting the provider")
					return
				}
				if h.sessionRepo != nil {
					sessions, listErr := h.sessionRepo.List(r.Context(), providerRecord.AccountID)
					if listErr != nil {
						writeError(w, http.StatusInternalServerError, "storage_error", "could not inspect provider sessions")
						return
					}
					if len(sessions) > 0 {
						writeError(w, http.StatusConflict, "conflict", "revoke or delete provider sessions before deleting the provider")
						return
					}
				}
				if h.credentials != nil && providerRecord.CredentialID != "" {
					if _, deleteErr := h.credentials.DeleteContext(r.Context(), providerRecord.CredentialID); deleteErr != nil {
						writeError(w, http.StatusInternalServerError, "storage_error", "could not delete provider credential")
						return
					}
				}
				if err := h.accountRepo.Delete(r.Context(), providerRecord.AccountID); err != nil && !errors.Is(err, sql.ErrNoRows) {
					writeError(w, http.StatusConflict, "conflict", "could not remove provider account dependencies")
					return
				}
			}
		} else {
			count, err := h.accountRepo.CountByProvider(r.Context(), id)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "storage_error", "could not inspect provider accounts")
				return
			}
			if count > 0 {
				writeError(w, http.StatusConflict, "conflict", "delete or reassign provider accounts before deleting the provider")
				return
			}
		}
	} else if accounts := h.accounts.List(id); len(accounts) > 0 {
		writeError(w, http.StatusConflict, "conflict", "delete or reassign provider accounts before deleting the provider")
		return
	}
	if h.providerRepo != nil {
		if err := h.providerRepo.Delete(r.Context(), id); err != nil {
			writeError(w, http.StatusInternalServerError, "storage_error", "could not delete provider metadata")
			return
		}
	}
	if registered {
		h.registry.Unregister(id)
	}
	if manager, ok := h.selector.(interface{ RemoveProvider(string) }); ok {
		manager.RemoveProvider(id)
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": id, "persisted": h.providerRepo != nil})
}

func (h *Handler) adminModels(w http.ResponseWriter, r *http.Request) {
	if h.modelRepo != nil {
		items, err := h.modelRepo.List(r.Context(), strings.TrimSpace(r.URL.Query().Get("provider_id")))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "storage_error", "could not list models")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": items, "persisted": true})
		return
	}
	items := make([]provider.Model, 0)
	for _, p := range h.registry.List() {
		models, err := p.ListModels(r.Context())
		if err != nil {
			writeError(w, http.StatusBadGateway, "provider_error", err.Error())
			return
		}
		items = append(items, models...)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items, "persisted": false})
}

func (h *Handler) setModelStatus(w http.ResponseWriter, r *http.Request) {
	if h.modelRepo == nil {
		writeError(w, http.StatusServiceUnavailable, "storage_unavailable", "model repository is unavailable")
		return
	}
	var input struct {
		ProviderID string `json:"provider_id"`
		Enabled    bool   `json:"enabled"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&input); err != nil || strings.TrimSpace(input.ProviderID) == "" {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "provider_id and enabled are required")
		return
	}
	if err := h.modelRepo.SetEnabled(r.Context(), r.PathValue("id"), input.ProviderID, input.Enabled); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", "model not found")
		} else {
			writeError(w, http.StatusInternalServerError, "storage_error", "could not update model")
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": r.PathValue("id"), "provider_id": input.ProviderID, "enabled": input.Enabled, "persisted": true})
}

func (h *Handler) deleteModel(w http.ResponseWriter, r *http.Request) {
	if h.modelRepo == nil {
		writeError(w, http.StatusServiceUnavailable, "storage_unavailable", "model repository is unavailable")
		return
	}
	providerID := strings.TrimSpace(r.URL.Query().Get("provider_id"))
	if providerID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "provider_id query parameter is required")
		return
	}
	if err := h.modelRepo.Delete(r.Context(), r.PathValue("id"), providerID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", "model not found")
		} else {
			writeError(w, http.StatusInternalServerError, "storage_error", "could not delete model")
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": r.PathValue("id"), "provider_id": providerID, "persisted": true})
}

func (h *Handler) adminAPIKeys(w http.ResponseWriter, r *http.Request) {
	if h.apiKey == "" {
		writeError(w, http.StatusServiceUnavailable, "admin_auth_required", "set OMNI_GATEWAY_API_KEY before enabling API key management")
		return
	}
	if h.apiKeyRepo == nil {
		writeError(w, http.StatusServiceUnavailable, "storage_unavailable", "API key repository is unavailable")
		return
	}
	items, err := h.apiKeyRepo.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", "could not list API keys")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items, "persisted": true})
}

func (h *Handler) createAPIKey(w http.ResponseWriter, r *http.Request) {
	if h.apiKey == "" {
		writeError(w, http.StatusServiceUnavailable, "admin_auth_required", "set OMNI_GATEWAY_API_KEY before enabling API key management")
		return
	}
	if h.apiKeyRepo == nil {
		writeError(w, http.StatusServiceUnavailable, "storage_unavailable", "API key repository is unavailable")
		return
	}
	var input struct {
		ID     string   `json:"id"`
		Name   string   `json:"name"`
		Scopes []string `json:"scopes"`
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || strings.TrimSpace(input.Name) == "" {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "valid name is required")
		return
	}
	if input.ID == "" {
		raw := make([]byte, 12)
		if _, err := rand.Read(raw); err != nil {
			writeError(w, http.StatusInternalServerError, "server_error", "could not generate key id")
			return
		}
		input.ID = "key_" + hex.EncodeToString(raw)
	}
	if err := apikey.ValidateScopes(input.Scopes); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	item, secret, err := h.apiKeyRepo.Create(r.Context(), input.ID, strings.TrimSpace(input.Name), input.Scopes)
	if err != nil {
		writeError(w, http.StatusConflict, "conflict", "could not create API key")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"data": item, "secret": secret, "warning": "secret is shown once; store it securely"})
}

func (h *Handler) setAPIKeyStatus(w http.ResponseWriter, r *http.Request) {
	if h.apiKey == "" || h.apiKeyRepo == nil {
		writeError(w, http.StatusServiceUnavailable, "admin_auth_required", "API key management is unavailable")
		return
	}
	var input struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid status JSON")
		return
	}
	if err := h.apiKeyRepo.SetEnabled(r.Context(), r.PathValue("id"), input.Enabled); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", "API key not found")
		} else {
			writeError(w, http.StatusInternalServerError, "storage_error", "could not update API key")
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": r.PathValue("id"), "enabled": input.Enabled, "persisted": true})
}

func (h *Handler) deleteAPIKey(w http.ResponseWriter, r *http.Request) {
	if h.apiKey == "" || h.apiKeyRepo == nil {
		writeError(w, http.StatusServiceUnavailable, "admin_auth_required", "API key management is unavailable")
		return
	}
	if err := h.apiKeyRepo.Delete(r.Context(), r.PathValue("id")); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", "API key not found")
		} else {
			writeError(w, http.StatusInternalServerError, "storage_error", "could not delete API key")
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": r.PathValue("id"), "persisted": true})
}

func (h *Handler) adminStats(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.metrics.Snapshot())
}

func (h *Handler) adminRequests(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > storage.MaxRecentRequestLogs {
			writeError(w, http.StatusBadRequest, "invalid_request_error", "limit must be between 1 and 1000")
			return
		}
		limit = parsed
	}
	if h.requestLogs != nil {
		items, err := h.requestLogs.Recent(r.Context(), limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "storage_error", "could not read persisted request logs")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": items, "persisted": true})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": h.metrics.Recent(limit), "persisted": false})
}

type requestLogSink struct{ repo *storage.RequestLogRepository }

func (s requestLogSink) Append(ctx context.Context, record observability.RequestRecord) error {
	return s.repo.AppendRequestRecord(ctx, record)
}

func (h *Handler) adminUsage(w http.ResponseWriter, r *http.Request) {
	if h.requestLogs == nil {
		writeJSON(w, http.StatusOK, map[string]any{"persisted": false, "message": "persistent request usage is unavailable"})
		return
	}
	summary, breakdown, err := h.requestLogs.UsageReport(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", "could not calculate a consistent usage report")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": summary, "by_provider_model": breakdown, "persisted": true, "token_usage_note": "Only upstream-reported token usage is counted; missing usage is not estimated."})
}
