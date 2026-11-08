package deepseekweb

import (
	"context"

	"omnirouter/internal/provider"
)

// RegistrationProvider is an intentionally inert registration shell. It allows
// an administrator to create a DeepSeek Web provider record and pair a browser
// session before the private request transport is implemented. It advertises
// no models, so the scheduler cannot route inference to this provider.
type RegistrationProvider struct {
	id   string
	name string
}

func NewRegistrationProvider(id, name string) *RegistrationProvider {
	if name == "" {
		name = id
	}
	return &RegistrationProvider{id: id, name: name}
}

func (p *RegistrationProvider) ID() string   { return p.id }
func (p *RegistrationProvider) Name() string { return p.name }

func (p *RegistrationProvider) ListModels(context.Context) ([]provider.Model, error) {
	return []provider.Model{}, nil
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
