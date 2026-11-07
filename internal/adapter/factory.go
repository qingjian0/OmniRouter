// Package adapter constructs concrete upstream adapters from persisted Provider kinds.
package adapter

import (
	"fmt"
	"strings"

	"omnirouter/internal/provider"
	anthropicprovider "omnirouter/internal/provider/anthropic"
	"omnirouter/internal/provider/deepseek"
	openai "omnirouter/internal/provider/openai"
)

const (
	KindOpenAICompatible = "openai-compatible"
	KindDeepSeek         = "deepseek"
	KindAnthropic        = "anthropic"
)

type Config struct {
	Kind               string
	ID                 string
	Name               string
	BaseURL            string
	APIKey             string
	Models             []string
	ModelCapabilities  map[string][]string
	ModelContextLimits map[string]int
}

func NormalizeKind(kind string) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		return KindOpenAICompatible
	}
	return kind
}

func DefaultBaseURL(kind string) string {
	switch NormalizeKind(kind) {
	case KindDeepSeek:
		return deepseek.DefaultBaseURL
	case KindAnthropic:
		return anthropicprovider.DefaultBaseURL
	default:
		return ""
	}
}

func New(cfg Config) (provider.Provider, error) {
	kind := NormalizeKind(cfg.Kind)
	switch kind {
	case KindOpenAICompatible:
		return openai.New(openai.Config{
			ID: cfg.ID, Name: cfg.Name, BaseURL: cfg.BaseURL, APIKey: cfg.APIKey,
			Models: cfg.Models, ModelCapabilities: cfg.ModelCapabilities,
			ModelContextLimits: cfg.ModelContextLimits,
		})
	case KindDeepSeek:
		return deepseek.New(deepseek.Config{
			ID: cfg.ID, Name: cfg.Name, BaseURL: cfg.BaseURL, APIKey: cfg.APIKey,
			Models: cfg.Models,
		})
	case KindAnthropic:
		return anthropicprovider.New(anthropicprovider.Config{
			ID: cfg.ID, Name: cfg.Name, BaseURL: cfg.BaseURL, APIKey: cfg.APIKey, Models: cfg.Models, ModelCapabilities: cfg.ModelCapabilities,
		})
	default:
		return nil, fmt.Errorf("unsupported provider kind %q", kind)
	}
}
