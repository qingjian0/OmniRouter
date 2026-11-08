package deepseekweb

import (
	"errors"
	"sync"
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

func TestPairingTicketConcurrentConsumeSucceedsExactlyOnce(t *testing.T) {
	manager := NewPairingManager()
	ticket, err := manager.Create("deepseek-web", "personal")
	if err != nil {
		t.Fatal(err)
	}

	const consumers = 32
	var wg sync.WaitGroup
	results := make(chan error, consumers)
	for i := 0; i < consumers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, consumeErr := manager.Consume(ticket.Nonce)
			results <- consumeErr
		}()
	}
	wg.Wait()
	close(results)

	successes, rejected := 0, 0
	for consumeErr := range results {
		switch {
		case consumeErr == nil:
			successes++
		case errors.Is(consumeErr, ErrPairingInvalid):
			rejected++
		default:
			t.Fatalf("unexpected consume error: %v", consumeErr)
		}
	}
	if successes != 1 || rejected != consumers-1 {
		t.Fatalf("concurrent consumes: successes=%d rejected=%d, want 1 and %d", successes, rejected, consumers-1)
	}
}

