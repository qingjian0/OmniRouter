// Package gemini implements Google's native Gemini generateContent API.
package gemini

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"omnirouter/internal/provider"
	openai "omnirouter/internal/provider/openai"
)

const DefaultBaseURL = "https://generativelanguage.googleapis.com/v1beta"
const maxResponseBytes = 32 << 20

type Config struct {
	ID                string
	Name              string
	BaseURL           string
	APIKey            string
	Models            []string
	ModelCapabilities map[string][]string
	Client            *http.Client
}

type Provider struct {
	id, name, baseURL, apiKey string
	models                    []string
	modelCapabilities         map[string][]string
	client                    *http.Client
}

func New(cfg Config) (*Provider, error) {
	cfg.ID = strings.TrimSpace(cfg.ID)
	cfg.Name = strings.TrimSpace(cfg.Name)
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if cfg.ID == "" {
		return nil, fmt.Errorf("Gemini provider ID is required")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	u, err := url.Parse(cfg.BaseURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("Gemini base URL must be an absolute HTTP(S) URL without user info, query, or fragment")
	}
	for _, model := range cfg.Models {
		if !validModelID(model) {
			return nil, fmt.Errorf("invalid Gemini model ID %q", model)
		}
	}
	if cfg.Client == nil {
		cfg.Client = openai.NewStreamingClient()
	}
	return &Provider{
		id:                cfg.ID,
		name:              cfg.Name,
		baseURL:           cfg.BaseURL,
		apiKey:            cfg.APIKey,
		models:            append([]string(nil), cfg.Models...),
		modelCapabilities: cloneCapabilities(cfg.ModelCapabilities),
		client:            cfg.Client,
	}, nil
}

func (p *Provider) ID() string { return p.id }
func (p *Provider) Name() string {
	if p.name == "" {
		return "Google Gemini"
	}
	return p.name
}

func (p *Provider) ListModels(context.Context) ([]provider.Model, error) {
	out := make([]provider.Model, 0, len(p.models))
	for _, id := range p.models {
		capabilities := []string{"gemini_generate_content", "streaming"}
		for _, capability := range p.modelCapabilities[id] {
			switch capability {
			case "chat", "gemini_generate_content", "streaming":
				continue
			default:
				capabilities = append(capabilities, capability)
			}
		}
		out = append(out, provider.Model{
			ID: id, Name: id, Provider: p.id, Capabilities: capabilities,
			APICapabilities: []string{"gemini_generate_content"},
		})
	}
	return out, nil
}

// Generate and Stream intentionally reject OpenAI Chat Completions. The
// gateway must use the native Gemini interface for this provider.
func (p *Provider) Generate(context.Context, *provider.Request) (*provider.Response, error) {
	return nil, fmt.Errorf("Gemini native adapter does not implement OpenAI Chat Completions")
}
func (p *Provider) Stream(context.Context, *provider.Request, func(provider.Event) error) error {
	return fmt.Errorf("Gemini native adapter does not implement OpenAI Chat Completions streaming")
}

func (p *Provider) GenerateGemini(ctx context.Context, req *provider.Request) (*provider.Response, error) {
	if req == nil {
		return nil, fmt.Errorf("request is required")
	}
	body, err := p.do(ctx, req.Model, req.Input, false)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	data, err := io.ReadAll(io.LimitReader(body, maxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxResponseBytes {
		return nil, fmt.Errorf("Gemini response exceeds 32 MiB")
	}
	if err := validateResponse(data); err != nil {
		return nil, err
	}
	return &provider.Response{Model: req.Model, Output: data}, nil
}

// StreamGemini forwards native Gemini SSE frames. Gemini's documented
// streamGenerateContent protocol terminates with a clean HTTP/SSE EOF rather
// than a synthetic [DONE] frame; that clean EOF is the explicit completion.
func (p *Provider) StreamGemini(ctx context.Context, req *provider.Request, emit func(provider.Event) error) error {
	if req == nil {
		return fmt.Errorf("request is required")
	}
	body, err := p.do(ctx, req.Model, req.Input, true)
	if err != nil {
		return err
	}
	defer body.Close()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 4<<20)
	var lines []string
	seenData := false
	flush := func() error {
		if len(lines) == 0 {
			return nil
		}
		frame := strings.Join(lines, "\n") + "\n\n"
		lines = lines[:0]
		for _, line := range strings.Split(frame, "\n") {
			if strings.HasPrefix(line, "data:") && strings.TrimSpace(strings.TrimPrefix(line, "data:")) != "" {
				seenData = true
			}
		}
		if err := validateSSEFrame(frame); err != nil {
			if emitErr := emit(provider.Event{Data: []byte(frame), Done: true}); emitErr != nil {
				return emitErr
			}
			return err
		}
		return emit(provider.Event{Data: []byte(frame)})
	}
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if err := flush(); err != nil {
		return err
	}
	if !seenData {
		return fmt.Errorf("Gemini stream ended without any response data")
	}
	return emit(provider.Event{Done: true, ExplicitDone: true})
}

func (p *Provider) do(ctx context.Context, model string, input []byte, stream bool) (io.ReadCloser, error) {
	if !validModelID(model) {
		return nil, fmt.Errorf("valid Gemini model ID is required")
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(input, &payload); err != nil || payload == nil {
		return nil, fmt.Errorf("invalid Gemini generateContent payload")
	}
	var contents []json.RawMessage
	if raw, ok := payload["contents"]; !ok || json.Unmarshal(raw, &contents) != nil || len(contents) == 0 {
		return nil, fmt.Errorf("Gemini contents are required")
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	method := "generateContent"
	query := ""
	if stream {
		method = "streamGenerateContent"
		query = "?alt=sse"
	}
	endpoint := p.baseURL + "/models/" + url.PathEscape(model) + ":" + method + query
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
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
		req.Header.Set("x-goog-api-key", p.apiKey)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, fmt.Errorf("Gemini API returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(message)))
	}
	if stream && !strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		defer resp.Body.Close()
		return nil, fmt.Errorf("Gemini streaming endpoint returned unexpected content type %q", resp.Header.Get("Content-Type"))
	}
	return resp.Body, nil
}

func (p *Provider) HealthCheck(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/models?pageSize=1", nil)
	if err != nil {
		return err
	}
	if p.apiKey != "" {
		req.Header.Set("x-goog-api-key", p.apiKey)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Gemini health check returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func validModelID(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && value == strings.TrimSpace(value) &&
		!strings.ContainsAny(value, "/\\\\?#:") && !strings.ContainsAny(value, " \t\r\n")
}

func validateResponse(data []byte) error {
	var envelope struct {
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("invalid Gemini response JSON: %w", err)
	}
	if len(envelope.Error) > 0 && string(envelope.Error) != "null" {
		return fmt.Errorf("Gemini API returned an error response")
	}
	return nil
}

func validateSSEFrame(frame string) error {
	for _, line := range strings.Split(frame, "\n") {
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		if err := validateResponse([]byte(data)); err != nil {
			return err
		}
	}
	return nil
}

func cloneCapabilities(input map[string][]string) map[string][]string {
	out := make(map[string][]string, len(input))
	for modelID, capabilities := range input {
		out[modelID] = append([]string(nil), capabilities...)
	}
	return out
}
