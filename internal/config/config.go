package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

type Config struct {
	HTTP                    HTTPConfig
	Database                DatabaseConfig
	Provider                ProviderConfig
	Providers               []ProviderConfig
	GatewayAPIKey           string
	CredentialEncryptionKey string
}

type ProviderConfig struct {
	Kind               string              `json:"kind"`
	ID                 string              `json:"id"`
	Name               string              `json:"name"`
	BaseURL            string              `json:"base_url"`
	APIKey             string              `json:"api_key"`
	Models             []string            `json:"models"`
	ModelCapabilities  map[string][]string `json:"model_capabilities"`
	ModelContextLimits map[string]int      `json:"model_context_limits"`
	Priority           int                 `json:"priority"`
	Weight             uint32              `json:"weight"`
}

type DatabaseConfig struct {
	Path        string
	BusyTimeout time.Duration
	WAL         bool
}

type HTTPConfig struct {
	Address      string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration
}

func Default() Config {
	return Config{Database: DatabaseConfig{Path: "data/omnirouter.db", BusyTimeout: 5 * time.Second, WAL: true}, HTTP: HTTPConfig{
		Address:      "127.0.0.1:8080",
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 0, // Keep streaming responses unrestricted by a global write timeout.
		IdleTimeout:  60 * time.Second,
	}}
}

func Load() (Config, error) {
	cfg := Default()
	if v := os.Getenv("OMNI_ADDR"); v != "" {
		cfg.HTTP.Address = v
	}
	for _, item := range []struct {
		name string
		dst  *time.Duration
	}{
		{"OMNI_READ_TIMEOUT", &cfg.HTTP.ReadTimeout},
		{"OMNI_WRITE_TIMEOUT", &cfg.HTTP.WriteTimeout},
		{"OMNI_IDLE_TIMEOUT", &cfg.HTTP.IdleTimeout},
	} {
		if v := os.Getenv(item.name); v != "" {
			d, err := time.ParseDuration(v)
			if err != nil {
				return Config{}, fmt.Errorf("%s: %w", item.name, err)
			}
			if d < 0 {
				return Config{}, fmt.Errorf("%s must not be negative", item.name)
			}
			*item.dst = d
		}
	}
	if v := strings.TrimSpace(os.Getenv("OMNI_DATABASE_PATH")); v != "" {
		cfg.Database.Path = v
	}
	if v := strings.TrimSpace(os.Getenv("OMNI_DATABASE_BUSY_TIMEOUT")); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("OMNI_DATABASE_BUSY_TIMEOUT: %w", err)
		}
		if d < 0 {
			return Config{}, fmt.Errorf("OMNI_DATABASE_BUSY_TIMEOUT must not be negative")
		}
		cfg.Database.BusyTimeout = d
	}
	if v := strings.TrimSpace(os.Getenv("OMNI_DATABASE_WAL")); v != "" {
		switch strings.ToLower(v) {
		case "1", "true", "yes", "on":
			cfg.Database.WAL = true
		case "0", "false", "no", "off":
			cfg.Database.WAL = false
		default:
			return Config{}, fmt.Errorf("OMNI_DATABASE_WAL must be a boolean")
		}
	}
	cfg.GatewayAPIKey = os.Getenv("OMNI_GATEWAY_API_KEY")
	cfg.CredentialEncryptionKey = strings.TrimSpace(os.Getenv("OMNI_CREDENTIAL_ENCRYPTION_KEY"))
	if raw := strings.TrimSpace(os.Getenv("OMNI_PROVIDERS")); raw != "" {
		if err := json.Unmarshal([]byte(raw), &cfg.Providers); err != nil {
			return Config{}, fmt.Errorf("OMNI_PROVIDERS: %w", err)
		}
	} else {
		cfg.Provider.Kind = env("OMNI_PROVIDER_KIND", "openai-compatible")
		defaultID, defaultName := "openai-compatible", "OpenAI-compatible"
		if strings.EqualFold(cfg.Provider.Kind, "deepseek") {
			defaultID, defaultName = "deepseek", "DeepSeek"
		} else if strings.EqualFold(cfg.Provider.Kind, "openai-responses") {
			defaultID, defaultName = "openai-responses", "OpenAI Responses"
		}
		cfg.Provider.ID = env("OMNI_PROVIDER_ID", defaultID)
		cfg.Provider.Name = env("OMNI_PROVIDER_NAME", defaultName)
		cfg.Provider.BaseURL = strings.TrimSpace(os.Getenv("OMNI_PROVIDER_BASE_URL"))
		if cfg.Provider.BaseURL == "" {
			switch strings.ToLower(strings.TrimSpace(cfg.Provider.Kind)) {
			case "deepseek":
				cfg.Provider.BaseURL = "https://api.deepseek.com"
			case "openai-responses":
				cfg.Provider.BaseURL = "https://api.openai.com/v1"
			}
		}
		cfg.Provider.APIKey = os.Getenv("OMNI_PROVIDER_API_KEY")
		if model := strings.TrimSpace(os.Getenv("OMNI_PROVIDER_MODELS")); model != "" {
			for _, item := range strings.Split(model, ",") {
				if item = strings.TrimSpace(item); item != "" {
					cfg.Provider.Models = append(cfg.Provider.Models, item)
				}
			}
		}
		if raw := strings.TrimSpace(os.Getenv("OMNI_PROVIDER_MODEL_CAPABILITIES")); raw != "" {
			if err := json.Unmarshal([]byte(raw), &cfg.Provider.ModelCapabilities); err != nil {
				return Config{}, fmt.Errorf("OMNI_PROVIDER_MODEL_CAPABILITIES: %w", err)
			}
		}
		if raw := strings.TrimSpace(os.Getenv("OMNI_PROVIDER_MODEL_CONTEXT_LIMITS")); raw != "" {
			if err := json.Unmarshal([]byte(raw), &cfg.Provider.ModelContextLimits); err != nil {
				return Config{}, fmt.Errorf("OMNI_PROVIDER_MODEL_CONTEXT_LIMITS: %w", err)
			}
		}
		if cfg.Provider.BaseURL != "" {
			cfg.Providers = append(cfg.Providers, cfg.Provider)
		}
	}
	if cfg.HTTP.Address == "" {
		return Config{}, fmt.Errorf("HTTP address must not be empty")
	}
	return cfg, nil
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
