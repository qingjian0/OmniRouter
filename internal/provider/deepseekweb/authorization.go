// Package deepseekweb contains the security boundary for user-authorized
// DeepSeek consumer-web sessions. It deliberately does not implement private
// protocol challenge solving or login automation.
package deepseekweb

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"unicode"
)

const (
	ProviderKind   = "deepseek-web"
	DefaultOrigin  = "https://chat.deepseek.com"
	MaxTokenBytes  = 16 << 10
	MaxCookieBytes = 32 << 10
	MaxCookieCount  = 64
)

var (
	ErrAuthorizationRequired = errors.New("DeepSeek web authorization is required")
	ErrSessionExpired        = errors.New("DeepSeek web session has expired")
	ErrPolicyBlocked         = errors.New("DeepSeek web request was blocked by an upstream policy")
	ErrUpstreamChanged       = errors.New("DeepSeek web protocol changed")
	ErrRateLimited           = errors.New("DeepSeek web request was rate limited")
)

// BrowserCookie is the minimum browser-cookie data accepted at the explicit
// local authorization boundary. It intentionally excludes browser metadata.
type BrowserCookie struct {
	Name   string
	Value  string
	Domain string
	Path   string
	Secure bool
}

// AuthorizationInput is transient input from a user-authorized browser flow.
// Callers must persist Token and CookieHeader only through the encrypted vault
// and must zero their byte buffers after the write completes.
type AuthorizationInput struct {
	Token   []byte
	Cookies []BrowserCookie
}

// AuthorizationMaterial is validated secret material. It must never be logged,
// marshalled to JSON, or returned by an admin API.
type AuthorizationMaterial struct {
	Token        []byte
	CookieHeader []byte
}

// ValidateAuthorization rejects malformed or unrelated browser state. It
// accepts cookies only for chat.deepseek.com and requires ds_session_id.
func ValidateAuthorization(input AuthorizationInput) (AuthorizationMaterial, error) {
	if len(input.Token) == 0 || len(input.Token) > MaxTokenBytes {
		return AuthorizationMaterial{}, ErrAuthorizationRequired
	}
	for _, r := range string(input.Token) {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return AuthorizationMaterial{}, ErrAuthorizationRequired
		}
	}
	if len(input.Cookies) == 0 || len(input.Cookies) > MaxCookieCount {
		return AuthorizationMaterial{}, ErrAuthorizationRequired
	}

	cookies := make([]string, 0, len(input.Cookies))
	total := 0
	hasSession := false
	seen := make(map[string]struct{}, len(input.Cookies))
	for _, item := range input.Cookies {
		domain := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(item.Domain)), ".")
		if domain != "chat.deepseek.com" || (item.Path != "" && item.Path != "/") || !item.Secure {
			return AuthorizationMaterial{}, ErrAuthorizationRequired
		}
		cookie := &http.Cookie{Name: item.Name, Value: item.Value, Domain: "chat.deepseek.com", Path: "/", Secure: true}
		if err := cookie.Valid(); err != nil || strings.ContainsAny(item.Value, ";,\r\n") {
			return AuthorizationMaterial{}, ErrAuthorizationRequired
		}
		if _, exists := seen[item.Name]; exists {
			return AuthorizationMaterial{}, ErrAuthorizationRequired
		}
		seen[item.Name] = struct{}{}
		if item.Name == "ds_session_id" {
			hasSession = item.Value != ""
		}
		pair := item.Name + "=" + item.Value
		total += len(pair) + 2
		if total > MaxCookieBytes {
			return AuthorizationMaterial{}, ErrAuthorizationRequired
		}
		cookies = append(cookies, pair)
	}
	if !hasSession {
		return AuthorizationMaterial{}, ErrAuthorizationRequired
	}
	sort.Strings(cookies)
	header := strings.Join(cookies, "; ")
	return AuthorizationMaterial{
		Token:        append([]byte(nil), input.Token...),
		CookieHeader: []byte(header),
	}, nil
}

// RedactHeaderName identifies credential-bearing HTTP headers. Call this from
// logging middleware before recording request/response metadata.
func RedactHeaderName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "authorization", "cookie", "set-cookie", "x-auth-token", "x-ds-auth-token":
		return true
	default:
		return false
	}
}

// SafeFailure maps upstream status codes to stable public error categories.
// Response bodies are intentionally not included because they may echo secrets.
func SafeFailure(status int) error {
	switch status {
	case http.StatusUnauthorized:
		return ErrSessionExpired
	case http.StatusForbidden:
		return ErrPolicyBlocked
	case http.StatusTooManyRequests:
		return ErrRateLimited
	case http.StatusNotFound, http.StatusBadGateway, http.StatusNotImplemented, http.StatusServiceUnavailable:
		return ErrUpstreamChanged
	default:
		if status >= 400 {
			return fmt.Errorf("DeepSeek web upstream returned HTTP %d", status)
		}
		return nil
	}
}
