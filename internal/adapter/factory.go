// Package adapter constructs concrete upstream adapters from persisted Provider kinds.
package adapter

import (
	"fmt"
	"strings"

	"omnirouter/internal/provider"
	"omnirouter/internal/provider/deepseek"
	"omnirouter/internal/provider/deepseekweb"
	openai "omnirouter/internal/provider/openai"
)

const (
	KindOpenAICompatible = "openai-compatible"
	KindDeepSeek         = "deepseek"
	KindDeepSeekWeb      = "deepseek-web"
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
	if NormalizeKind(kind) == KindDeepSeek {
		return deepseek.DefaultBaseURL
	}
	if NormalizeKind(kind) == KindDeepSeekWeb {
		return deepseekweb.DefaultOrigin
	}
	return ""
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
	case KindDeepSeekWeb:
		if strings.TrimSpace(cfg.APIKey) != "" || len(cfg.Models) > 0 {
			return nil, fmt.Errorf("DeepSeek Web registration does not accept API keys or models before its session transport is enabled")
		}
		return deepseekweb.NewRegistrationProvider(cfg.ID, cfg.Name), nil
	default:
		return nil, fmt.Errorf("unsupported provider kind %q", kind)
	}
}
