package deepseek

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"omnirouter/internal/provider"
	openai "omnirouter/internal/provider/openai"
)

const DefaultBaseURL = "https://api.deepseek.com"

type Config struct {
	ID string
	Name string
	BaseURL string
	APIKey string
	Models []string
	Client *http.Client
}

type apiModel struct {
	ID string `json:"id"`
	Name string `json:"name"`
	ContextWindow int `json:"context_window"`
	MaxOutputTokens int `json:"max_output_tokens"`
	InputModalities []string `json:"input_modalities"`
	OutputModalities []string `json:"output_modalities"`
	Effort *struct { SupportedLevels []string `json:"supported_levels"` } `json:"effort"`
	APICapabilities map[string]json.RawMessage `json:"api_capabilities"`
}

type modelEnvelope struct { Data []apiModel `json:"data"` }

type Provider struct {
	id string
	name string
	baseURL string
	apiKey string
	client *http.Client
	chat *openai.Provider
	fallback []provider.Model
	mu sync.RWMutex
	cached []provider.Model
	cachedAt time.Time
}

func New(cfg Config) (*Provider, error) {
	cfg.ID = strings.TrimSpace(cfg.ID)
	cfg.Name = strings.TrimSpace(cfg.Name)
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if cfg.BaseURL == "" { cfg.BaseURL = DefaultBaseURL }
	if cfg.ID == "" { return nil, fmt.Errorf("DeepSeek provider ID is required") }
	u, err := url.Parse(cfg.BaseURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("DeepSeek base URL must be an absolute HTTP(S) URL without user info, query, or fragment")
	}
	if cfg.Client == nil { cfg.Client = openai.NewStreamingClient() }
	chat, err := openai.New(openai.Config{ID: cfg.ID, Name: cfg.Name, BaseURL: cfg.BaseURL, APIKey: cfg.APIKey, Client: cfg.Client})
	if err != nil { return nil, err }
	fallback := defaultModels(cfg.ID)
	if len(cfg.Models) > 0 {
		fallback = make([]provider.Model, 0, len(cfg.Models))
		for _, id := range cfg.Models {
			id = strings.TrimSpace(id)
			if id == "" { return nil, fmt.Errorf("DeepSeek model IDs must not be empty") }
			fallback = append(fallback, modelDefaults(cfg.ID, id))
		}
	}
	return &Provider{id: cfg.ID, name: cfg.Name, baseURL: cfg.BaseURL, apiKey: cfg.APIKey, client: cfg.Client, chat: chat, fallback: fallback}, nil
}

func (p *Provider) ID() string { return p.id }
func (p *Provider) Name() string {
	if p.name == "" { return "DeepSeek" }
	return p.name
}

// ListModels refreshes the official model catalogue. If the API is temporarily
// unreachable, it returns configured/default model declarations so startup and
// local administration remain available; HealthCheck still reports the outage.
func (p *Provider) ListModels(ctx context.Context) ([]provider.Model, error) {
	p.mu.RLock()
	if len(p.cached) > 0 && time.Since(p.cachedAt) < 5*time.Minute {
		out := cloneModels(p.cached); p.mu.RUnlock(); return out, nil
	}
	p.mu.RUnlock()
	models, err := p.DiscoverModels(ctx)
	if err != nil {
		p.mu.RLock(); fallback := cloneModels(p.fallback); p.mu.RUnlock()
		return fallback, nil
	}
	p.mu.Lock()
	p.cached, p.cachedAt = cloneModels(models), time.Now()
	p.mu.Unlock()
	return models, nil
}

// DiscoverModels reads the authenticated official GET /models catalogue and
// maps provider metadata into OmniRouter's provider-neutral model vocabulary.
func (p *Provider) DiscoverModels(ctx context.Context) ([]provider.Model, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/models", nil)
	if err != nil { return nil, err }
	p.authorize(req)
	resp, err := p.client.Do(req)
	if err != nil { return nil, fmt.Errorf("DeepSeek model discovery: %w", err) }
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("DeepSeek model discovery returned HTTP %d", resp.StatusCode)
	}
	var envelope modelEnvelope
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("decode DeepSeek model catalogue: %w", err)
	}
	if len(envelope.Data) == 0 { return nil, fmt.Errorf("DeepSeek model catalogue is empty") }
	out := make([]provider.Model, 0, len(envelope.Data))
	seen := make(map[string]struct{}, len(envelope.Data))
	for _, item := range envelope.Data {
		id := strings.TrimSpace(item.ID)
		if id == "" { return nil, fmt.Errorf("DeepSeek model catalogue contains an empty model ID") }
		if _, ok := seen[id]; ok { continue }
		seen[id] = struct{}{}
		caps := []string{"chat", "streaming", "tools"}
		if contains(item.InputModalities, "image") { caps = append(caps, "vision") }
		if item.Effort != nil && len(item.Effort.SupportedLevels) > 0 { caps = append(caps, "reasoning") }
		if _, ok := item.APICapabilities["anthropic_messages"]; ok { caps = append(caps, "anthropic_messages") }
		if _, ok := item.APICapabilities["openai_responses"]; ok { caps = append(caps, "responses") }
		out = append(out, provider.Model{ID:id, Name:item.Name, Provider:p.id, Capabilities:caps, ContextLimit:item.ContextWindow})
	}
	return out, nil
}

func (p *Provider) Generate(ctx context.Context, req *provider.Request) (*provider.Response, error) {
	return p.chat.Generate(ctx, req)
}
func (p *Provider) Stream(ctx context.Context, req *provider.Request, emit func(provider.Event) error) error {
	return p.chat.Stream(ctx, req, emit)
}
func (p *Provider) HealthCheck(ctx context.Context) error {
	_, err := p.DiscoverModels(ctx)
	return err
}

func (p *Provider) authorize(req *http.Request) {
	if p.apiKey != "" { req.Header.Set("Authorization", "Bearer "+p.apiKey) }
}

func modelDefaults(providerID, id string) provider.Model {
	m := provider.Model{ID:id, Name:id, Provider:providerID, Capabilities:[]string{"chat","streaming","tools","reasoning"}}
	switch id {
	case "deepseek-flash":
		m.Name = "DeepSeek V4.1 Flash"
		m.Capabilities = append(m.Capabilities, "vision", "responses")
		m.ContextLimit = 1048576
	case "deepseek-v4-pro":
		m.Name = "DeepSeek V4 Pro"
		m.Capabilities = append(m.Capabilities, "responses")
		m.ContextLimit = 1048576
	}
	return m
}
func defaultModels(providerID string) []provider.Model {
	return []provider.Model{modelDefaults(providerID,"deepseek-flash"), modelDefaults(providerID,"deepseek-v4-pro")}
}
func contains(values []string, wanted string) bool {
	for _, value := range values { if strings.EqualFold(value,wanted) { return true } }
	return false
}
func cloneModels(values []provider.Model) []provider.Model {
	out := make([]provider.Model,len(values))
	for i, value := range values {
		out[i] = value
		out[i].Capabilities = append([]string(nil),value.Capabilities...)
	}
	return out
}

