package adapter

import (
	"testing"

	"omnirouter/internal/provider"
	anthropicprovider "omnirouter/internal/provider/anthropic"
	"omnirouter/internal/provider/deepseek"
)

func TestNewDeepSeekUsesOfficialDefaults(t *testing.T) {
	upstream, err := New(Config{Kind: KindDeepSeek, ID: "deepseek"})
	if err != nil {
		t.Fatal(err)
	}
	if upstream.ID() != "deepseek" || upstream.Name() != "DeepSeek" {
		t.Fatalf("unexpected provider identity: %q %q", upstream.ID(), upstream.Name())
	}
	if DefaultBaseURL(KindDeepSeek) != deepseek.DefaultBaseURL {
		t.Fatalf("unexpected DeepSeek default URL: %q", DefaultBaseURL(KindDeepSeek))
	}
}

func TestNewRejectsUnknownKind(t *testing.T) {
	if _, err := New(Config{Kind: "unknown", ID: "x"}); err == nil {
		t.Fatal("expected unsupported provider kind error")
	}
}

func TestNewAnthropicUsesNativeDefaults(t *testing.T) {
	upstream, err := New(Config{Kind: KindAnthropic, ID: "anthropic", Models: []string{"claude-test"}, ModelCapabilities: map[string][]string{"claude-test": {"tools", "vision"}}})
	if err != nil {
		t.Fatal(err)
	}
	if upstream.ID() != "anthropic" {
		t.Fatalf("unexpected provider identity: %q", upstream.ID())
	}
	if DefaultBaseURL(KindAnthropic) != anthropicprovider.DefaultBaseURL {
		t.Fatalf("unexpected Anthropic default URL: %q", DefaultBaseURL(KindAnthropic))
	}
	if _, ok := upstream.(provider.AnthropicProvider); !ok {
		t.Fatal("Anthropic adapter must implement native Messages capability")
	}
	models, err := upstream.ListModels(t.Context())
	if err != nil || len(models) != 1 {
		t.Fatalf("models=%v err=%v", models, err)
	}
	if !containsCapability(models[0].Capabilities, "tools") || !containsCapability(models[0].Capabilities, "vision") || containsCapability(models[0].Capabilities, "chat") {
		t.Fatalf("unexpected native capability declarations: %v", models[0].Capabilities)
	}
}

func containsCapability(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
