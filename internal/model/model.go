// Package model defines provider-neutral model metadata and capabilities.
package model

import "time"

type Capability string

const (
	CapabilityChat        Capability = "chat"
	CapabilityStreaming   Capability = "streaming"
	CapabilityTools       Capability = "tools"
	CapabilityJSON        Capability = "json"
	CapabilityVision      Capability = "vision"
	CapabilityAudioInput  Capability = "audio_input"
	CapabilityAudioOutput Capability = "audio_output"
	CapabilityEmbedding   Capability = "embedding"
)

type Model struct {
	ID           string       `json:"id"`
	ProviderID   string       `json:"provider_id"`
	DisplayName  string       `json:"display_name,omitempty"`
	Capabilities []Capability `json:"capabilities,omitempty"`
	ContextLimit            int          `json:"context_limit,omitempty"`
	InputCostPerMillionUSD  *float64     `json:"input_cost_per_million_usd,omitempty"`
	OutputCostPerMillionUSD *float64     `json:"output_cost_per_million_usd,omitempty"`
	Enabled                 bool         `json:"enabled"`
	UpdatedAt               time.Time    `json:"updated_at"`
}

func (m Model) Supports(required ...Capability) bool {
	set := make(map[Capability]struct{}, len(m.Capabilities))
	for _, capability := range m.Capabilities {
		set[capability] = struct{}{}
	}
	for _, capability := range required {
		if _, ok := set[capability]; !ok {
			return false
		}
	}
	return true
}
