package deepseekweb

import (
	"errors"
	"net/http"
	"testing"
)

func validInput() AuthorizationInput {
	return AuthorizationInput{
		Token: []byte("user-token-value"),
		Cookies: []BrowserCookie{
			{Name: "ds_session_id", Value: "session-123", Domain: ".chat.deepseek.com", Path: "/", Secure: true},
		},
	}
}

func TestValidateAuthorizationBuildsDeterministicCookieHeader(t *testing.T) {
	got, err := ValidateAuthorization(validInput())
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Token) != "user-token-value" {
		t.Fatalf("unexpected token copy: %q", got.Token)
	}
	if string(got.CookieHeader) != "ds_session_id=session-123" {
		t.Fatalf("unexpected cookie header: %q", got.CookieHeader)
	}
}

func TestValidateAuthorizationRejectsUntrustedInput(t *testing.T) {
	tests := map[string]func(*AuthorizationInput){
		"missing token":          func(in *AuthorizationInput) { in.Token = nil },
		"token newline":          func(in *AuthorizationInput) { in.Token = []byte("token\r\nX-Leak: yes") },
		"missing session cookie": func(in *AuthorizationInput) { in.Cookies = in.Cookies[1:] },
		"foreign domain":         func(in *AuthorizationInput) { in.Cookies[0].Domain = "evil.example" },
		"insecure cookie":        func(in *AuthorizationInput) { in.Cookies[0].Secure = false },
		"cookie injection":       func(in *AuthorizationInput) { in.Cookies[0].Value = "x; admin=true" },
		"duplicate cookie":       func(in *AuthorizationInput) { in.Cookies = append(in.Cookies, in.Cookies[0]) },
		"unapproved cookie": func(in *AuthorizationInput) {
			in.Cookies = append(in.Cookies, BrowserCookie{Name: "analytics", Value: "tracking", Domain: "chat.deepseek.com", Path: "/", Secure: true})
		},
		"wrong path": func(in *AuthorizationInput) { in.Cookies[0].Path = "/other" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			input := validInput()
			mutate(&input)
			if _, err := ValidateAuthorization(input); !errors.Is(err, ErrAuthorizationRequired) {
				t.Fatalf("expected ErrAuthorizationRequired, got %v", err)
			}
		})
	}
}

func TestValidateAuthorizationDoesNotAliasInputBuffers(t *testing.T) {
	input := validInput()
	got, err := ValidateAuthorization(input)
	if err != nil {
		t.Fatal(err)
	}
	input.Token[0] = 'X'
	if string(got.Token) != "user-token-value" {
		t.Fatal("validated token aliases caller-owned input")
	}
}

func TestRedactHeaderName(t *testing.T) {
	for _, name := range []string{"Authorization", "cookie", "Set-Cookie", "X-Auth-Token", "X-Ds-Auth-Token"} {
		if !RedactHeaderName(name) {
			t.Errorf("expected %q to be redacted", name)
		}
	}
	for _, name := range []string{"Content-Type", "Accept", "User-Agent"} {
		if RedactHeaderName(name) {
			t.Errorf("did not expect %q to be redacted", name)
		}
	}
}

func TestSafeFailure(t *testing.T) {
	tests := []struct {
		status int
		want   error
	}{
		{http.StatusUnauthorized, ErrSessionExpired},
		{http.StatusForbidden, ErrPolicyBlocked},
		{http.StatusTooManyRequests, ErrRateLimited},
		{http.StatusServiceUnavailable, ErrUpstreamChanged},
		{http.StatusNotFound, ErrUpstreamChanged},
		{http.StatusBadRequest, nil},
	}
	for _, test := range tests {
		err := SafeFailure(test.status)
		if test.want != nil && !errors.Is(err, test.want) {
			t.Errorf("status %d: got %v, want %v", test.status, err, test.want)
		}
		if test.want == nil && err == nil {
			t.Errorf("status %d: expected safe generic error", test.status)
		}
	}
}
