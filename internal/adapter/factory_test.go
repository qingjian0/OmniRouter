package adapter

import (
	"context"
	"testing"

	"omnirouter/internal/provider/deepseek"
	"omnirouter/internal/provider/deepseekweb"
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

func TestNewDeepSeekWebCreatesInertRegistrationProvider(t *testing.T) {
	upstream, err := New(Config{Kind: KindDeepSeekWeb, ID: "ds-web"})
	if err != nil {
		t.Fatal(err)
	}
	if upstream.ID() != "ds-web" || upstream.Name() != "ds-web" {
		t.Fatalf("unexpected provider identity: %q %q", upstream.ID(), upstream.Name())
	}
	models, err := upstream.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 0 {
		t.Fatalf("registration shell must not advertise routable models: %+v", models)
	}
	if DefaultBaseURL(KindDeepSeekWeb) != deepseekweb.DefaultOrigin {
		t.Fatalf("unexpected DeepSeek Web origin: %q", DefaultBaseURL(KindDeepSeekWeb))
	}
	if _, err := New(Config{Kind: KindDeepSeekWeb, ID: "ds-web", APIKey: "unexpected"}); err == nil {
		t.Fatal("DeepSeek Web registration shell must reject API keys")
	}
	if _, err := New(Config{Kind: KindDeepSeekWeb, ID: "ds-web", BaseURL: "https://example.invalid"}); err == nil {
		t.Fatal("DeepSeek Web registration shell must reject custom upstream origins")
	}
	if err := upstream.HealthCheck(context.Background()); err == nil {
		t.Fatal("inert registration shell must not claim a healthy upstream session")
	}
}

func TestNewRejectsUnknownKind(t *testing.T) {
	if _, err := New(Config{Kind: "unknown", ID: "x"}); err == nil {
		t.Fatal("expected unsupported provider kind error")
	}
}
