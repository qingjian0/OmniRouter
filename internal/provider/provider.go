package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
)

type Model struct {
	ID               string   `json:"id"`
	Name             string   `json:"name,omitempty"`
	Provider         string   `json:"provider"`
	Capabilities     []string `json:"capabilities,omitempty"`
	ContextLimit     int      `json:"context_limit,omitempty"`
	MaxOutputTokens  int      `json:"max_output_tokens,omitempty"`
	InputModalities  []string `json:"input_modalities,omitempty"`
	OutputModalities []string `json:"output_modalities,omitempty"`
	ReasoningEffort  []string `json:"reasoning_effort,omitempty"`
	APICapabilities  []string `json:"api_capabilities,omitempty"`
}

type Request struct {
	Model  string
	Input  []byte
	Stream bool
}
type Response struct {
	Model  string
	Output []byte
}
type Event struct {
	Data         []byte
	Done         bool
	ExplicitDone bool
}

// Provider is the boundary between the gateway and an AI service adapter.
type Provider interface {
	ID() string
	Name() string
	ListModels(context.Context) ([]Model, error)
	Generate(context.Context, *Request) (*Response, error)
	Stream(context.Context, *Request, func(Event) error) error
	HealthCheck(context.Context) error
}

// BindingIDs returns stable, non-secret database identifiers for the account
// and credential owned by a provider. The digest is only an identifier, not
// a credential hash or a security boundary.
func BindingIDs(providerID string) (accountID, credentialID string) {
	sum := sha256.Sum256([]byte(providerID))
	suffix := hex.EncodeToString(sum[:12])
	return "provider_account_" + suffix, "provider_credential_" + suffix
}

// ResponsesProvider is an optional capability for adapters that implement the
// OpenAI Responses API natively. It is separate from Chat Completions support.
type ResponsesProvider interface {
	GenerateResponses(context.Context, *Request) (*Response, error)
	StreamResponses(context.Context, *Request, func(Event) error) error
}
