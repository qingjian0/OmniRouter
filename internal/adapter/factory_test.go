package adapter

import (
	"context"
	"testing"

	"omnirouter/internal/provider/deepseek"
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

func TestNewRejectsUnknownKind(t *testing.T) {
	if _, err := New(Config{Kind: "unknown", ID: "x"}); err == nil {
		t.Fatal("expected unsupported provider kind error")
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
