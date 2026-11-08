package deepseekweb

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"
)

var (
	ErrPairingInvalid = errors.New("DeepSeek web pairing ticket is invalid or expired")
	ErrPairingLimit   = errors.New("too many pending DeepSeek web pairing tickets")
)

const (
	PairingLifetime    = 2 * time.Minute
	MaxPendingPairings = 128
)

// PairingTicket is a short-lived, single-use capability. The nonce must be
// returned only to the authenticated local admin UI and never written to logs.
type PairingTicket struct {
	Nonce     string    `json:"nonce"`
	ExpiresAt time.Time `json:"expires_at"`
}

type PairingTarget struct {
	ProviderID  string
	AccountName string
}

type pairingEntry struct {
	target  PairingTarget
	expires time.Time
}

// PairingManager stores only ephemeral pairing metadata in process memory.
// A restart invalidates every outstanding ticket by design.
type PairingManager struct {
	mu      sync.Mutex
	now     func() time.Time
	pending map[string]pairingEntry
}

func NewPairingManager() *PairingManager {
	return &PairingManager{now: time.Now, pending: make(map[string]pairingEntry)}
}

func (m *PairingManager) Create(providerID, accountName string) (PairingTicket, error) {
	providerID, accountName = strings.TrimSpace(providerID), strings.TrimSpace(accountName)
	if providerID == "" || accountName == "" {
		return PairingTicket{}, ErrPairingInvalid
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return PairingTicket{}, err
	}
	nonce := hex.EncodeToString(raw[:])
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, item := range m.pending {
		if !item.expires.After(now) {
			delete(m.pending, key)
		}
	}
	if len(m.pending) >= MaxPendingPairings {
		return PairingTicket{}, ErrPairingLimit
	}
	expires := now.Add(PairingLifetime)
	m.pending[nonce] = pairingEntry{target: PairingTarget{ProviderID: providerID, AccountName: accountName}, expires: expires}
	return PairingTicket{Nonce: nonce, ExpiresAt: expires}, nil
}

// Consume atomically deletes a ticket before returning its target. Every
// ticket can be consumed at most once, including tickets that have expired.
func (m *PairingManager) Consume(nonce string) (PairingTarget, error) {
	nonce = strings.TrimSpace(nonce)
	if nonce == "" {
		return PairingTarget{}, ErrPairingInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	item, ok := m.pending[nonce]
	delete(m.pending, nonce)
	if !ok || !item.expires.After(m.now()) {
		return PairingTarget{}, ErrPairingInvalid
	}
	return item.target, nil
}

// IsAllowedPageOrigin accepts only the exact DeepSeek Web origin, not
// subdomains, alternate schemes, or lookalike hostnames.
func IsAllowedPageOrigin(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u == nil {
		return false
	}
	return u.Scheme == "https" && strings.EqualFold(u.Host, "chat.deepseek.com") && u.User == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == ""
}

// IsAllowedExtensionOrigin requires an exact administrator-configured
// extension origin. It intentionally does not wildcard extension IDs.
func IsAllowedExtensionOrigin(origin, configured string) bool {
	origin, configured = strings.TrimSpace(origin), strings.TrimSpace(configured)
	if origin == "" || configured == "" || origin != configured {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || u == nil || (u.Scheme != "chrome-extension" && u.Scheme != "moz-extension") || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	return true
}

// IsLoopbackPeer prevents a browser bridge from accepting pairing submissions
// through a public or LAN-facing interface. The HTTP listener should still
// bind to loopback as a separate defense.
func IsLoopbackPeer(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = strings.Trim(remoteAddr, "[]")
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
