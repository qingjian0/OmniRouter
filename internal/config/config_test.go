package config

import "testing"

func TestLoadSingleProviderModelCapabilitiesFromEnvironment(t *testing.T) {
	t.Setenv("OMNI_PROVIDERS", "")
	t.Setenv("OMNI_PROVIDER_ID", "test-provider")
	t.Setenv("OMNI_PROVIDER_NAME", "Test Provider")
	t.Setenv("OMNI_PROVIDER_BASE_URL", "https://api.example.test/v1")
	t.Setenv("OMNI_PROVIDER_API_KEY", "")
	t.Setenv("OMNI_PROVIDER_MODELS", "model-a, model-b")
	t.Setenv("OMNI_PROVIDER_MODEL_CAPABILITIES", `{"model-a":["chat","streaming","tools"],"model-b":["chat"]}`)
	t.Setenv("OMNI_PROVIDER_MODEL_CONTEXT_LIMITS", `{"model-a":64000,"model-b":32000}`)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Providers) != 1 {
		t.Fatalf("providers = %d, want 1", len(cfg.Providers))
	}
	got := cfg.Providers[0]
	if got.ID != "test-provider" || len(got.Models) != 2 {
		t.Fatalf("unexpected provider configuration: %+v", got)
	}
	if len(got.ModelCapabilities["model-a"]) != 3 || got.ModelCapabilities["model-a"][1] != "streaming" {
		t.Fatalf("model capabilities not parsed: %#v", got.ModelCapabilities)
	}
	if got.ModelContextLimits["model-a"] != 64000 || got.ModelContextLimits["model-b"] != 32000 {
		t.Fatalf("context limits not parsed: %#v", got.ModelContextLimits)
	}
}

func TestLoadRejectsInvalidModelCapabilityJSON(t *testing.T) {
	t.Setenv("OMNI_PROVIDERS", "")
	t.Setenv("OMNI_PROVIDER_BASE_URL", "https://api.example.test/v1")
	t.Setenv("OMNI_PROVIDER_MODELS", "model-a")
	t.Setenv("OMNI_PROVIDER_MODEL_CAPABILITIES", `{"model-a":["chat"]`)
	t.Setenv("OMNI_PROVIDER_MODEL_CONTEXT_LIMITS", "")

	if _, err := Load(); err == nil {
		t.Fatal("expected invalid capability JSON to fail")
	}
}

func TestLoadDeepSeekProviderDefaults(t *testing.T) {
	t.Setenv("OMNI_PROVIDERS", "")
	t.Setenv("OMNI_PROVIDER_KIND", "deepseek")
	t.Setenv("OMNI_PROVIDER_ID", "")
	t.Setenv("OMNI_PROVIDER_NAME", "")
	t.Setenv("OMNI_PROVIDER_BASE_URL", "")
	t.Setenv("OMNI_PROVIDER_API_KEY", "test-key")
	t.Setenv("OMNI_PROVIDER_MODELS", "")
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
	if got.Kind != "deepseek" || got.ID != "deepseek" || got.Name != "DeepSeek" {
		t.Fatalf("unexpected DeepSeek identity: %+v", got)
	}
	if got.BaseURL != "https://api.deepseek.com" {
		t.Fatalf("base URL = %q", got.BaseURL)
	}
}


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
