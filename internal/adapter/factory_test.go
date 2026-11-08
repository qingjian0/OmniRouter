package adapter

import (
	"context"
	"testing"

	"omnirouter/internal/provider"
	anthropicprovider "omnirouter/internal/provider/anthropic"
	"omnirouter/internal/provider/deepseek"
	"omnirouter/internal/provider/deepseekweb"
	geminiprovider "omnirouter/internal/provider/gemini"
	openairesponses "omnirouter/internal/provider/openairesponses"
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

func TestNewOpenAIResponsesUsesDefaultBaseURL(t *testing.T) {
	upstream, err := New(Config{Kind: KindOpenAIResponses, ID: "responses", Models: []string{"gpt-4.1"}})
	if err != nil {
		t.Fatal(err)
	}
	if DefaultBaseURL(KindOpenAIResponses) != openairesponses.DefaultBaseURL {
		t.Fatalf("unexpected Responses default URL: %q", DefaultBaseURL(KindOpenAIResponses))
	}
	if _, ok := upstream.(*openairesponses.Provider); !ok {
		t.Fatalf("unexpected adapter type %T", upstream)
	}
}

func TestNewOpenAIResponsesUsesNativeAdapter(t *testing.T) {
	upstream, err := New(Config{
		Kind: KindOpenAIResponses, ID: "responses", Name: "Responses",
		BaseURL: "https://api.openai.com/v1", Models: []string{"gpt-4.1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := upstream.(*openairesponses.Provider); !ok {
		t.Fatalf("unexpected adapter type %T", upstream)
	}
	models, err := upstream.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || !containsCapability(models[0].Capabilities, "responses") {
		t.Fatalf("native Responses capability not declared: %+v", models)
	}
}

func TestNewDeepSeekWebCreatesInertRegistrationProvider(t *testing.T) {
	upstream, err := New(Config{Kind: KindDeepSeekWeb, ID: "ds-web"})
	if err != nil { t.Fatal(err) }
	if upstream.ID() != "ds-web" || upstream.Name() != "ds-web" { t.Fatalf("unexpected provider identity: %q %q", upstream.ID(), upstream.Name()) }
	models, err := upstream.ListModels(context.Background())
	if err != nil { t.Fatal(err) }
	if len(models) != 0 { t.Fatalf("registration shell must not advertise routable models: %+v", models) }
	if DefaultBaseURL(KindDeepSeekWeb) != deepseekweb.DefaultOrigin { t.Fatalf("unexpected DeepSeek Web origin: %q", DefaultBaseURL(KindDeepSeekWeb)) }
	if _, err := New(Config{Kind: KindDeepSeekWeb, ID: "ds-web", APIKey: "unexpected"}); err == nil { t.Fatal("DeepSeek Web registration shell must reject API keys") }
	if _, err := New(Config{Kind: KindDeepSeekWeb, ID: "ds-web", BaseURL: "https://example.invalid"}); err == nil { t.Fatal("DeepSeek Web registration shell must reject custom upstream origins") }
	if err := upstream.HealthCheck(context.Background()); err == nil { t.Fatal("inert registration shell must not claim a healthy upstream session") }
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
	models, err := upstream.ListModels(context.Background())
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

func TestNewGeminiUsesNativeDefaults(t *testing.T) {
	upstream, err := New(Config{Kind: KindGemini, ID: "gemini", Models: []string{"gemini-test"}})
	if err != nil {
		t.Fatal(err)
	}
	if upstream.ID() != "gemini" || upstream.Name() != "Google Gemini" {
		t.Fatalf("unexpected provider identity: %q %q", upstream.ID(), upstream.Name())
	}
	if DefaultBaseURL(KindGemini) != geminiprovider.DefaultBaseURL {
		t.Fatalf("unexpected Gemini default URL: %q", DefaultBaseURL(KindGemini))
	}
}
