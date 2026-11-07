package adapter

import (
	"testing"

	"omnirouter/internal/provider/deepseek"
	"omnirouter/internal/provider"
	anthropicprovider "omnirouter/internal/provider/anthropic"
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
	upstream, err := New(Config{Kind: KindAnthropic, ID: "anthropic", Models: []string{"claude-test"}})
	if err != nil { t.Fatal(err) }
	if upstream.ID() != "anthropic" { t.Fatalf("unexpected provider identity: %q", upstream.ID()) }
	if DefaultBaseURL(KindAnthropic) != anthropicprovider.DefaultBaseURL { t.Fatalf("unexpected Anthropic default URL: %q", DefaultBaseURL(KindAnthropic)) }
	if _, ok := upstream.(provider.AnthropicProvider); !ok { t.Fatal("Anthropic adapter must implement native Messages capability") }
}
