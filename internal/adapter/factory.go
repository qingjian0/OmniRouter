// Package adapter constructs concrete upstream adapters from persisted Provider kinds.
package adapter

import (
	"fmt"
	"strings"

	"omnirouter/internal/provider"
	"omnirouter/internal/provider/anthropic"
	"omnirouter/internal/provider/deepseek"
	geminiprovider "omnirouter/internal/provider/gemini"
	openai "omnirouter/internal/provider/openai"
	openairesponses "omnirouter/internal/provider/openairesponses"
)

const (
	KindOpenAICompatible = "openai-compatible"
	KindOpenAIResponses  = "openai-responses"
	KindDeepSeek         = "deepseek"
	KindAnthropic        = "anthropic"
	KindGemini           = "gemini"
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
	case KindOpenAIResponses:
		return openairesponses.DefaultBaseURL
	case KindAnthropic:
		return anthropic.DefaultBaseURL
	case KindGemini:
		return geminiprovider.DefaultBaseURL
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
	case KindOpenAIResponses:
		return openairesponses.New(openairesponses.Config{
			ID: cfg.ID, Name: cfg.Name, BaseURL: cfg.BaseURL, APIKey: cfg.APIKey,
			Models: cfg.Models, ModelCapabilities: cfg.ModelCapabilities,
			ModelContextLimits: cfg.ModelContextLimits,
		})
	case KindAnthropic:
		return anthropic.New(anthropic.Config{ID: cfg.ID, Name: cfg.Name, BaseURL: cfg.BaseURL, APIKey: cfg.APIKey, Models: cfg.Models, ModelCapabilities: cfg.ModelCapabilities})
	case KindGemini:
		return geminiprovider.New(geminiprovider.Config{ID: cfg.ID, Name: cfg.Name, BaseURL: cfg.BaseURL, APIKey: cfg.APIKey, Models: cfg.Models, ModelCapabilities: cfg.ModelCapabilities})
	case KindDeepSeek:
		return deepseek.New(deepseek.Config{
			ID: cfg.ID, Name: cfg.Name, BaseURL: cfg.BaseURL, APIKey: cfg.APIKey,
			Models: cfg.Models,
		})
	default:
		return nil, fmt.Errorf("unsupported provider kind %q", kind)
	}
}
