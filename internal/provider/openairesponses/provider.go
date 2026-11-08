package openairesponses

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"omnirouter/internal/provider"
	openai "omnirouter/internal/provider/openai"
)

const DefaultBaseURL = "https://api.openai.com/v1"

const maxResponseBytes = 32 << 20
const maxErrorBytes = 1 << 20
const maxSSELineBytes = 4 << 20

// Config describes an upstream that implements the OpenAI Responses API.
// It is deliberately separate from the Chat Completions adapter: no protocol
// translation or endpoint fallback is performed.
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
	capabilities              map[string][]string
	contextLimits             map[string]int
	client                    *http.Client
}

func New(cfg Config) (*Provider, error) {
	cfg.ID = strings.TrimSpace(cfg.ID)
	cfg.Name = strings.TrimSpace(cfg.Name)
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if cfg.ID == "" {
		return nil, fmt.Errorf("OpenAI Responses provider ID and base URL are required")
	}
	u, err := url.Parse(cfg.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("OpenAI Responses base URL must be an absolute HTTP(S) URL without user info, query, or fragment")
	}
	for _, model := range cfg.Models {
		if strings.TrimSpace(model) == "" {
			return nil, fmt.Errorf("OpenAI Responses model IDs must not be empty")
		}
	}
	for model, limit := range cfg.ModelContextLimits {
		if limit < 0 {
			return nil, fmt.Errorf("context limit for model %q must not be negative", model)
		}
	}
	if cfg.Client == nil {
		cfg.Client = openai.NewStreamingClient()
	}
	return &Provider{
		id: cfg.ID, name: cfg.Name, baseURL: cfg.BaseURL, apiKey: cfg.APIKey,
		models:        append([]string(nil), cfg.Models...),
		capabilities:  cloneCapabilities(cfg.ModelCapabilities),
		contextLimits: cloneIntMap(cfg.ModelContextLimits), client: cfg.Client,
	}, nil
}

func (p *Provider) ID() string { return p.id }
func (p *Provider) Name() string {
	if p.name == "" {
		return p.id
	}
	return p.name
}

func (p *Provider) ListModels(context.Context) ([]provider.Model, error) {
	out := make([]provider.Model, 0, len(p.models))
	for _, id := range p.models {
		caps := withoutCapability(p.capabilities[id], "chat")
		caps = appendUnique(caps, "responses", "streaming")
		out = append(out, provider.Model{
			ID: id, Name: id, Provider: p.id, Capabilities: caps,
			ContextLimit:    p.contextLimits[id],
			APICapabilities: []string{"openai_responses"},
		})
	}
	return out, nil
}

// Generate and Stream intentionally reject Chat Completions calls. This
// adapter advertises only the native Responses capability.
func (p *Provider) Generate(context.Context, *provider.Request) (*provider.Response, error) {
	return nil, fmt.Errorf("OpenAI Responses provider does not implement Chat Completions")
}
func (p *Provider) Stream(context.Context, *provider.Request, func(provider.Event) error) error {
	return fmt.Errorf("OpenAI Responses provider does not implement Chat Completions")
}

func (p *Provider) GenerateResponses(ctx context.Context, req *provider.Request) (*provider.Response, error) {
	if req == nil {
		return nil, fmt.Errorf("request is required")
	}
	body, err := p.do(ctx, req.Input, false)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	data, err := io.ReadAll(io.LimitReader(body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read Responses API response: %w", err)
	}
	if len(data) > maxResponseBytes {
		return nil, fmt.Errorf("Responses API response exceeds %d bytes", maxResponseBytes)
	}
	if !json.Valid(data) {
		return nil, fmt.Errorf("Responses API returned invalid JSON")
	}
	var envelope struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("decode Responses API response: %w", err)
	}
	if envelope.Status != "completed" {
		return nil, fmt.Errorf("Responses API returned non-completed status %q", envelope.Status)
	}
	return &provider.Response{Model: req.Model, Output: data}, nil
}

func (p *Provider) StreamResponses(ctx context.Context, req *provider.Request, emit func(provider.Event) error) error {
	if req == nil {
		return fmt.Errorf("request is required")
	}
	body, err := p.do(ctx, req.Input, true)
	if err != nil {
		return err
	}
	defer body.Close()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), maxSSELineBytes)
	var lines []string
	terminal := false
	sawData := false
	flush := func() error {
		if len(lines) == 0 {
			return nil
		}
		frame := strings.Join(lines, "\n") + "\n\n"
		eventType := ""
		dataLines := make([]string, 0, 1)
		for _, line := range lines {
			if strings.HasPrefix(line, "event:") {
				eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			}
			if strings.HasPrefix(line, "data:") {
				dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			}
		}
		lines = lines[:0]
		if len(dataLines) == 0 {
			return nil
		}
		data := strings.Join(dataLines, "\n")
		if !json.Valid([]byte(data)) {
			return fmt.Errorf("Responses API emitted malformed SSE JSON")
		}
		sawData = true
		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(data), &envelope); err != nil {
			return fmt.Errorf("Responses API emitted malformed SSE JSON: %w", err)
		}
		if eventType != "" && envelope.Type != "" && eventType != envelope.Type {
			return fmt.Errorf("Responses API SSE event type %q conflicts with payload type %q", eventType, envelope.Type)
		}
		if eventType == "" {
			eventType = envelope.Type
		}
		switch eventType {
		case "response.completed":
			terminal = true
			return emit(provider.Event{Data: []byte(frame), Done: true, ExplicitDone: true})
		case "response.failed", "response.incomplete", "error":
			terminal = true
			if err := emit(provider.Event{Data: []byte(frame), Done: true, ExplicitDone: false}); err != nil {
				return err
			}
			return fmt.Errorf("OpenAI Responses API terminated with %s", eventType)
		default:
			return emit(provider.Event{Data: []byte(frame)})
		}
	}
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			if terminal {
				return nil
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read Responses API SSE stream: %w", err)
	}
	if err := flush(); err != nil {
		return err
	}
	if !sawData {
		return fmt.Errorf("Responses API stream contained no data events")
	}
	if !terminal {
		return fmt.Errorf("Responses API stream ended without response.completed")
	}
	return nil
}

func (p *Provider) do(ctx context.Context, input []byte, stream bool) (io.ReadCloser, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, fmt.Errorf("invalid Responses API payload: %w", err)
	}
	if _, ok := payload["model"]; !ok {
		return nil, fmt.Errorf("model is required")
	}
	streamValue, _ := json.Marshal(stream)
	payload["stream"] = streamValue
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/responses", bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	} else {
		req.Header.Set("Accept", "application/json")
	}
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		message, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBytes))
		return nil, fmt.Errorf("OpenAI Responses API returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(message)))
	}
	mediaType, _, parseErr := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if parseErr != nil {
		resp.Body.Close()
		return nil, fmt.Errorf("OpenAI Responses endpoint returned unexpected content type %q", resp.Header.Get("Content-Type"))
	}
	if stream {
		if !strings.EqualFold(mediaType, "text/event-stream") {
			resp.Body.Close()
			return nil, fmt.Errorf("OpenAI Responses streaming endpoint returned non-SSE content type %q", resp.Header.Get("Content-Type"))
		}
	} else if !strings.EqualFold(mediaType, "application/json") && !strings.HasSuffix(strings.ToLower(mediaType), "+json") {
		resp.Body.Close()
		return nil, fmt.Errorf("OpenAI Responses endpoint returned unexpected content type %q", resp.Header.Get("Content-Type"))
	}
	return resp.Body, nil
}

func (p *Provider) HealthCheck(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/models", nil)
	if err != nil {
		return err
	}
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("OpenAI Responses provider health check returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func appendUnique(values []string, additions ...string) []string {
	for _, addition := range additions {
		found := false
		for _, value := range values {
			if value == addition {
				found = true
				break
			}
		}
		if !found {
			values = append(values, addition)
		}
	}
	return values
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

func withoutCapability(values []string, excluded string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value != excluded {
			out = appendUnique(out, value)
		}
	}
	return out
}
