// Package anthropic implements Anthropic's native Messages API as an upstream.
package anthropic

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

const DefaultBaseURL = "https://api.anthropic.com/v1"
const DefaultVersion = "2023-06-01"

type Config struct {
	ID      string
	Name    string
	BaseURL string
	APIKey  string
	Models  []string
	Client  *http.Client
}

type Provider struct {
	id, name, baseURL, apiKey string
	models                    []string
	client                    *http.Client
}

func New(cfg Config) (*Provider, error) {
	cfg.ID = strings.TrimSpace(cfg.ID)
	cfg.Name = strings.TrimSpace(cfg.Name)
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if cfg.ID == "" {
		return nil, fmt.Errorf("Anthropic provider ID is required")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	u, err := url.Parse(cfg.BaseURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("Anthropic base URL must be an absolute HTTP(S) URL without user info, query, or fragment")
	}
	for _, model := range cfg.Models {
		if strings.TrimSpace(model) == "" {
			return nil, fmt.Errorf("Anthropic model IDs must not be empty")
		}
	}
	if cfg.Client == nil {
		cfg.Client = openai.NewStreamingClient()
	}
	return &Provider{id: cfg.ID, name: cfg.Name, baseURL: cfg.BaseURL, apiKey: cfg.APIKey, models: append([]string(nil), cfg.Models...), client: cfg.Client}, nil
}

func (p *Provider) ID() string { return p.id }
func (p *Provider) Name() string {
	if p.name == "" {
		return "Anthropic"
	}
	return p.name
}

func (p *Provider) ListModels(context.Context) ([]provider.Model, error) {
	out := make([]provider.Model, 0, len(p.models))
	for _, id := range p.models {
		out = append(out, provider.Model{ID: id, Name: id, Provider: p.id, Capabilities: []string{"anthropic_messages", "streaming", "tools", "vision"}, APICapabilities: []string{"anthropic_messages"}})
	}
	return out, nil
}

// Generate and Stream intentionally reject Chat Completions. This adapter is
// eligible only for the native Anthropic Messages capability.
func (p *Provider) Generate(context.Context, *provider.Request) (*provider.Response, error) {
	return nil, fmt.Errorf("Anthropic native adapter does not implement OpenAI Chat Completions")
}
func (p *Provider) Stream(context.Context, *provider.Request, func(provider.Event) error) error {
	return fmt.Errorf("Anthropic native adapter does not implement OpenAI Chat Completions streaming")
}

func (p *Provider) GenerateAnthropic(ctx context.Context, req *provider.Request) (*provider.Response, error) {
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

func (p *Provider) StreamAnthropic(ctx context.Context, req *provider.Request, emit func(provider.Event) error) error {
	if req == nil {
		return fmt.Errorf("request is required")
	}
	body, err := p.do(ctx, req.Input)
	if err != nil {
		return err
	}
	defer body.Close()
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 4<<20)
	var lines []string
	terminal := false
	flush := func() error {
		if len(lines) == 0 {
			return nil
		}
		frame := strings.Join(lines, "\n") + "\n\n"
		eventType := ""
		var dataLines []string
		for _, line := range lines {
			if strings.HasPrefix(line, "event:") {
				eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			}
			if strings.HasPrefix(line, "data:") {
				dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			}
		}
		lines = lines[:0]
		if eventType == "" && len(dataLines) > 0 {
			var envelope struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal([]byte(strings.Join(dataLines, "\n")), &envelope)
			eventType = envelope.Type
		}
		switch eventType {
		case "message_stop":
			terminal = true
			return emit(provider.Event{Data: []byte(frame), Done: true, ExplicitDone: true})
		case "error":
			terminal = true
			if err := emit(provider.Event{Data: []byte(frame), Done: true}); err != nil {
				return err
			}
			return fmt.Errorf("Anthropic Messages stream emitted an error event")
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
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if err := flush(); err != nil {
		return err
	}
	if !terminal {
		return fmt.Errorf("Anthropic Messages stream ended without message_stop")
	}
	return nil
}

func (p *Provider) do(ctx context.Context, input []byte) (io.ReadCloser, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, fmt.Errorf("invalid Anthropic Messages payload: %w", err)
	}
	if _, ok := payload["model"]; !ok {
		return nil, fmt.Errorf("model is required")
	}
	if _, ok := payload["max_tokens"]; !ok {
		return nil, fmt.Errorf("max_tokens is required")
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/messages", bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("anthropic-version", DefaultVersion)
	if p.apiKey != "" {
		req.Header.Set("x-api-key", p.apiKey)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, fmt.Errorf("Anthropic Messages API returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(message)))
	}
	return resp.Body, nil
}

func (p *Provider) HealthCheck(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/models", nil)
	if err != nil {
		return err
	}
	req.Header.Set("anthropic-version", DefaultVersion)
	if p.apiKey != "" {
		req.Header.Set("x-api-key", p.apiKey)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Anthropic health check returned HTTP %d", resp.StatusCode)
	}
	return nil
}
