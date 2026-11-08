package config

import "testing"

func TestLoadDeepSeekWebExtensionOrigin(t *testing.T) {
	t.Setenv("OMNI_DEEPSEEK_WEB_EXTENSION_ORIGIN", "chrome-extension://abcdefghijklmnopabcdefghijklmnop")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DeepSeekWebExtensionOrigin != "chrome-extension://abcdefghijklmnopabcdefghijklmnop" {
		t.Fatalf("extension origin = %q", cfg.DeepSeekWebExtensionOrigin)
	}
}
