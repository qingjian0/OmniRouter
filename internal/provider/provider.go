package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
)

type Model struct {
	ID           string   `json:"id"`
	Provider     string   `json:"provider"`
	Capabilities []string `json:"capabilities,omitempty"`
	ContextLimit int      `json:"context_limit,omitempty"`
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
