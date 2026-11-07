package config

import "testing"

func TestLoadAnthropicProviderDefaults(t *testing.T) {
	t.Setenv("OMNI_PROVIDERS", "")
	t.Setenv("OMNI_PROVIDER_KIND", "anthropic")
	t.Setenv("OMNI_PROVIDER_ID", "")
	t.Setenv("OMNI_PROVIDER_NAME", "")
	t.Setenv("OMNI_PROVIDER_BASE_URL", "")
	t.Setenv("OMNI_PROVIDER_API_KEY", "test-key")
	t.Setenv("OMNI_PROVIDER_MODELS", "claude-test")
	t.Setenv("OMNI_PROVIDER_MODEL_CAPABILITIES", "")
	t.Setenv("OMNI_PROVIDER_MODEL_CONTEXT_LIMITS", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Providers) != 1 {
		t.Fatalf("providers = %d, want 1", len(cfg.Providers))
	}
	got := cfg.Providers[0]
	if got.Kind != "anthropic" || got.ID != "anthropic" || got.Name != "Anthropic" {
		t.Fatalf("unexpected Anthropic identity: %+v", got)
	}
	if got.BaseURL != "https://api.anthropic.com/v1" {
		t.Fatalf("base URL = %q", got.BaseURL)
	}
}
