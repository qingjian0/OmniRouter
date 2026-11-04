package openai

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"omnirouter/internal/provider"
)

// Config describes an OpenAI-compatible HTTP endpoint. APIKey is never exposed
// through JSON or the administrative API.
type Config struct {
	ID                 string
	Name               string
	BaseURL            string
	APIKey             string
	Models             []string
	ModelCapabilities  map[string][]string
	ModelContextLimits map[string]int
	Client             *http.Client
}

type Provider struct {
	id, name, baseURL, apiKey string
	models                    []string
	modelCapabilities         map[string][]string
	modelContextLimits        map[string]int
	client                    *http.Client
}

func New(cfg Config) (*Provider, error) {
	cfg.ID = strings.TrimSpace(cfg.ID)
	cfg.Name = strings.TrimSpace(cfg.Name)
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if cfg.ID == "" || cfg.BaseURL == "" {
		return nil, fmt.Errorf("provider ID and base URL are required")
	}
	parsedURL, err := url.Parse(cfg.BaseURL)
	if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") || parsedURL.Host == "" || parsedURL.User != nil || parsedURL.RawQuery != "" || parsedURL.Fragment != "" {
		return nil, fmt.Errorf("provider base URL must be an absolute HTTP(S) URL without user info, query, or fragment")
	}
	for _, modelID := range cfg.Models {
		if strings.TrimSpace(modelID) == "" {
			return nil, fmt.Errorf("provider model IDs must not be empty")
		}
	}
	for modelID, limit := range cfg.ModelContextLimits {
		if limit < 0 {
			return nil, fmt.Errorf("context limit for model %q must not be negative", modelID)
		}
	}
	if cfg.Client == nil {
		cfg.Client = NewStreamingClient()
	}
	return &Provider{id: cfg.ID, name: cfg.Name, baseURL: cfg.BaseURL, apiKey: cfg.APIKey, models: append([]string(nil), cfg.Models...), modelCapabilities: cloneCapabilities(cfg.ModelCapabilities), modelContextLimits: cloneIntMap(cfg.ModelContextLimits), client: cfg.Client}, nil
}

func (p *Provider) ID() string { return p.id }
func (p *Provider) Name() string {
	if p.name == "" {
		return p.id
	}
	return p.name
}

func (p *Provider) ListModels(ctx context.Context) ([]provider.Model, error) {
	out := make([]provider.Model, 0, len(p.models))
	for _, id := range p.models {
		out = append(out, provider.Model{ID: id, Provider: p.id, Capabilities: append([]string(nil), p.modelCapabilities[id]...), ContextLimit: p.modelContextLimits[id]})
	}
	return out, nil
}

func (p *Provider) Generate(ctx context.Context, req *provider.Request) (*provider.Response, error) {
	if req == nil {
		return nil, fmt.Errorf("request is required")
	}
	body, err := p.do(ctx, req.Input)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	data, err := io.ReadAll(io.LimitReader(body, 32<<20))
	if err != nil {
		return nil, err
	}
	return &provider.Response{Model: req.Model, Output: data}, nil
}

func (p *Provider) Stream(ctx context.Context, req *provider.Request, emit func(provider.Event) error) error {
	if req == nil {
		return fmt.Errorf("request is required")
	}
	body, err := p.do(ctx, req.Input)
	if err != nil {
		return err
	}
	defer body.Close()
	s := bufio.NewScanner(body)
	s.Buffer(make([]byte, 64*1024), 4<<20)
	var eventLines []string
	sawDone := false
	flush := func() error {
		if len(eventLines) == 0 {
			return nil
		}
		data := []byte(strings.Join(eventLines, "\n"))
		eventLines = eventLines[:0]
		if strings.TrimSpace(string(data)) == "data: [DONE]" {
			sawDone = true
			return emit(provider.Event{Done: true, ExplicitDone: true})
		}
		return emit(provider.Event{Data: data})
	}
	for s.Scan() {
		if sawDone {
			break
		}
		line := strings.TrimRight(s.Text(), "\r")
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		eventLines = append(eventLines, line)
	}
	if err := s.Err(); err != nil {
		return err
	}
	if err := flush(); err != nil {
		return err
	}
	if !sawDone {
		return fmt.Errorf("upstream stream ended without explicit [DONE]")
	}
	return nil
}

func (p *Provider) HealthCheck(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/models", nil)
	if err != nil {
		return err
	}
	p.authorize(req)
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("provider health check: HTTP %d", resp.StatusCode)
	}
	return nil
}

func (p *Provider) do(ctx context.Context, input []byte) (io.ReadCloser, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, fmt.Errorf("invalid chat completion payload: %w", err)
	}
	if _, ok := payload["model"]; !ok {
		return nil, fmt.Errorf("model is required")
	}
	if _, ok := payload["stream"]; !ok {
		payload["stream"] = json.RawMessage("false")
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	p.authorize(req)
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, fmt.Errorf("upstream returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(message)))
	}
	return resp.Body, nil
}

func (p *Provider) authorize(req *http.Request) {
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
}

// NewDefaultClient returns a client for bounded non-streaming control calls.
// The transport-level timeouts also apply when a request has no explicit deadline.
func NewDefaultClient(timeout time.Duration) *http.Client {
	client := NewStreamingClient()
	client.Timeout = timeout
	return client
}

// NewStreamingClient deliberately has no total request timeout: SSE streams may
// remain open for a long time. Dial, TLS and response-header phases are bounded
// so a dead endpoint cannot stall connection establishment indefinitely.
func NewStreamingClient() *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: time.Second,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
	return &http.Client{Transport: transport, Timeout: 0}
}

func cloneCapabilities(input map[string][]string) map[string][]string {
	out := make(map[string][]string, len(input))
	for key, values := range input {
		out[key] = append([]string(nil), values...)
	}
	return out
}
func cloneIntMap(input map[string]int) map[string]int {
	out := make(map[string]int, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}
