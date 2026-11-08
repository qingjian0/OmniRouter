package config

import "testing"

func TestIsLoopbackListenAddress(t *testing.T) {
	for _, address := range []string{
		"127.0.0.1:8080",
		"127.0.0.2:8080",
		"localhost:8080",
		"[::1]:8080",
		"[::1%lo]:8080",
	} {
		if !isLoopbackListenAddress(address) {
			t.Errorf("%q should be treated as loopback", address)
		}
	}
	for _, address := range []string{
		":8080",
		"0.0.0.0:8080",
		"[::]:8080",
		"192.168.1.10:8080",
		"example.com:8080",
		"invalid",
	} {
		if isLoopbackListenAddress(address) {
			t.Errorf("%q must not be treated as loopback", address)
		}
	}
}

func TestLoadRequiresGatewayKeyForNonLoopbackBind(t *testing.T) {
	t.Setenv("OMNI_ADDR", "0.0.0.0:8080")
	t.Setenv("OMNI_GATEWAY_API_KEY", "")
	t.Setenv("OMNI_PROVIDERS", "")
	t.Setenv("OMNI_PROVIDER_BASE_URL", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected non-loopback bind without gateway key to be rejected")
	}
}

func TestLoadAllowsNonLoopbackBindWithGatewayKey(t *testing.T) {
	t.Setenv("OMNI_ADDR", "0.0.0.0:8080")
	t.Setenv("OMNI_GATEWAY_API_KEY", "test-master-key")
	t.Setenv("OMNI_PROVIDERS", "")
	t.Setenv("OMNI_PROVIDER_BASE_URL", "")
	if _, err := Load(); err != nil {
		t.Fatalf("Load with gateway key: %v", err)
	}
}

func TestLoadAllowsOpenModeOnLoopback(t *testing.T) {
	t.Setenv("OMNI_ADDR", "127.0.0.1:8080")
	t.Setenv("OMNI_GATEWAY_API_KEY", "")
	t.Setenv("OMNI_PROVIDERS", "")
	t.Setenv("OMNI_PROVIDER_BASE_URL", "")
	if _, err := Load(); err != nil {
		t.Fatalf("loopback open mode should remain available: %v", err)
	}
}
