package deepseekweb

import (
	"errors"
	"testing"
	"time"
)

func TestPairingTicketIsSingleUse(t *testing.T) {
	manager := NewPairingManager()
	ticket, err := manager.Create("deepseek-web", "personal")
	if err != nil {
		t.Fatal(err)
	}
	target, err := manager.Consume(ticket.Nonce)
	if err != nil {
		t.Fatal(err)
	}
	if target.ProviderID != "deepseek-web" || target.AccountName != "personal" {
		t.Fatalf("unexpected pairing target: %+v", target)
	}
	if _, err := manager.Consume(ticket.Nonce); !errors.Is(err, ErrPairingInvalid) {
		t.Fatalf("expected consumed ticket to be invalid, got %v", err)
	}
}

func TestPairingTicketExpires(t *testing.T) {
	manager := NewPairingManager()
	now := time.Date(2026, 11, 5, 4, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return now }
	ticket, err := manager.Create("deepseek-web", "personal")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(PairingLifetime)
	if _, err := manager.Consume(ticket.Nonce); !errors.Is(err, ErrPairingInvalid) {
		t.Fatalf("expected expired ticket to be rejected, got %v", err)
	}
}

func TestPairingManagerCapsPendingTickets(t *testing.T) {
	manager := NewPairingManager()
	for i := 0; i < MaxPendingPairings; i++ {
		if _, err := manager.Create("deepseek-web", "account"); err != nil {
			t.Fatalf("ticket %d: %v", i, err)
		}
	}
	if _, err := manager.Create("deepseek-web", "overflow"); !errors.Is(err, ErrPairingLimit) {
		t.Fatalf("expected pending-ticket limit, got %v", err)
	}
}

func TestPairingOriginValidation(t *testing.T) {
	for _, origin := range []string{
		"https://chat.deepseek.com",
	} {
		if !IsAllowedPageOrigin(origin) {
			t.Errorf("expected page origin %q to be accepted", origin)
		}
	}
	for _, origin := range []string{
		"http://chat.deepseek.com",
		"https://evil.example",
		"https://chat.deepseek.com.evil.example",
		"https://sub.chat.deepseek.com",
		"https://chat.deepseek.com/",
		"https://user@chat.deepseek.com",
	} {
		if IsAllowedPageOrigin(origin) {
			t.Errorf("expected page origin %q to be rejected", origin)
		}
	}
	if !IsAllowedExtensionOrigin("chrome-extension://abcdefghijklmnop", "chrome-extension://abcdefghijklmnop") {
		t.Fatal("expected exact configured Chrome extension origin")
	}
	if IsAllowedExtensionOrigin("chrome-extension://attacker", "chrome-extension://trusted") {
		t.Fatal("unexpected acceptance of a different extension origin")
	}
	if IsAllowedExtensionOrigin("https://example.com", "https://example.com") {
		t.Fatal("ordinary web origin must not be accepted as extension origin")
	}
}

func TestLoopbackPeerValidation(t *testing.T) {
	for _, peer := range []string{"127.0.0.1:8080", "[::1]:8080", "::1"} {
		if !IsLoopbackPeer(peer) {
			t.Errorf("expected loopback peer %q", peer)
		}
	}
	for _, peer := range []string{"192.168.1.20:8080", "8.8.8.8:8080", "localhost:8080"} {
		if IsLoopbackPeer(peer) {
			t.Errorf("expected non-loopback peer %q", peer)
		}
	}
}
