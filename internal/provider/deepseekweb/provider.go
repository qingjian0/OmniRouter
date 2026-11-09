package deepseekweb

import (
	"context"

	"omnirouter/internal/provider"
)

// RegistrationProvider is the pre-pairing registration shell. It advertises
// only the explicitly configured model allowlist; inference remains unavailable
// until a browser account is paired and the runtime replaces this shell with an
// ExecutorProvider.
type RegistrationProvider struct {
	id     string
	name   string
	models []string
}

func NewRegistrationProvider(id, name string, models ...string) *RegistrationProvider {
	if name == "" {
		name = id
	}
	return &RegistrationProvider{id: id, name: name, models: append([]string(nil), models...)}
}

func (p *RegistrationProvider) ID() string   { return p.id }
func (p *RegistrationProvider) Name() string { return p.name }

func modelDisplayName(id string) string {
	switch id {
	case "deepseek-web":
		return "DeepSeek Web · 默认"
	case "deepseek-web-expert":
		return "DeepSeek Web · 专家"
	default:
		return id
	}
}

func (p *RegistrationProvider) ListModels(context.Context) ([]provider.Model, error) {
	models := make([]provider.Model, 0, len(p.models))
	for _, id := range p.models {
		if id == "" {
			continue
		}
		models = append(models, provider.Model{
			ID: id, Name: modelDisplayName(id), Provider: p.id,
			Capabilities: []string{"chat", "streaming"},
		})
	}
	return models, nil
}

func (p *RegistrationProvider) Generate(context.Context, *provider.Request) (*provider.Response, error) {
	return nil, ErrAuthorizationRequired
}

func (p *RegistrationProvider) Stream(context.Context, *provider.Request, func(provider.Event) error) error {
	return ErrAuthorizationRequired
}

func (p *RegistrationProvider) HealthCheck(context.Context) error {
	return ErrAuthorizationRequired
}
