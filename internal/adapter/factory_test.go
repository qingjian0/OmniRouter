package adapter

import (
	"testing"

	"omnirouter/internal/provider/deepseek"
	geminiprovider "omnirouter/internal/provider/gemini"
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
