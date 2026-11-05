package gateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"omnirouter/internal/account"
	"omnirouter/internal/credential"
	providerrepo "omnirouter/internal/provider"
	"omnirouter/internal/provider/deepseekweb"
	"omnirouter/internal/security"
	"omnirouter/internal/session"
	"omnirouter/internal/storage"
)

const testDeepSeekExtensionOrigin = "chrome-extension://abcdefghijklmnopabcdefghijklmnop"

func newDeepSeekWebPairingTestHandler(t *testing.T) *Handler {
	t.Helper()
	ctx := context.Background()
	runtime, err := storage.OpenSQLiteRuntime(ctx, filepath.Join(t.TempDir(), "pairing.db"), storage.SQLiteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	encryptor, err := security.NewEncryptor(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	credentialRepo, err := credential.NewRepository(runtime.Store().DB(), encryptor)
	if err != nil {
		t.Fatal(err)
	}
	vault, err := credential.NewPersistentVault(encryptor, credentialRepo)
	if err != nil {
		t.Fatal(err)
	}
	providers, err := providerrepo.NewRepository(runtime.Store().DB())
	if err != nil {
		t.Fatal(err)
	}
	if err := providers.Upsert(ctx, providerrepo.ProviderConfig{
		ID: "ds-web", Name: "DeepSeek Web", Kind: deepseekweb.ProviderKind,
		BaseURL: deepseekweb.DefaultOrigin, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	accounts, err := account.NewRepository(runtime.Store().DB())
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := session.NewRepository(runtime.Store().DB())
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := deepseekweb.NewAuthorizationService(accounts, sessions, vault)
	if err != nil {
		t.Fatal(err)
	}
	return &Handler{
		providerRepo:               providers,
		deepSeekWebPairings:        deepseekweb.NewPairingManager(),
		deepSeekWebAuthorization:   authorization,
		deepSeekWebExtensionOrigin: testDeepSeekExtensionOrigin,
	}
}

func TestDeepSeekWebPairingSubmissionPersistsAndConsumesTicket(t *testing.T) {
	h := newDeepSeekWebPairingTestHandler(t)
	ticket, err := h.deepSeekWebPairings.Create("ds-web", "test account")
	if err != nil {
		t.Fatal(err)
	}
	body := `{"token":"` + base64.StdEncoding.EncodeToString([]byte("test-access-token")) + `","cookies":[{"name":"ds_session_id","value":"session-value","domain":"chat.deepseek.com","path":"/","secure":true}]}`
	request := httptest.NewRequest(http.MethodPost, "/internal/deepseek-web/pairings", strings.NewReader(body))
	request.RemoteAddr = "127.0.0.1:43122"
	request.Header.Set("Origin", testDeepSeekExtensionOrigin)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Omni-Pairing-Nonce", ticket.Nonce)
	response := httptest.NewRecorder()

	h.submitDeepSeekWebPairing(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if response.Header().Get("Access-Control-Allow-Origin") != testDeepSeekExtensionOrigin {
		t.Fatal("exact extension origin was not echoed in CORS response")
	}
	var result struct {
		AccountID string `json:"account_id"`
		SessionID string `json:"session_id"`
		Status    string `json:"status"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.AccountID == "" || result.SessionID == "" || result.Status != "stored" {
		t.Fatalf("unexpected non-secret authorization result: %+v", result)
	}
	material, err := h.deepSeekWebAuthorization.LoadAuthorization(context.Background(), result.AccountID)
	if err != nil {
		t.Fatalf("stored authorization cannot be restored: %v", err)
	}
	if string(material.Token) != "test-access-token" {
		t.Fatal("stored access token does not match submitted token")
	}
	for i := range material.Token {
		material.Token[i] = 0
	}
	for i := range material.CookieHeader {
		material.CookieHeader[i] = 0
	}

	replay := httptest.NewRecorder()
	replayRequest := httptest.NewRequest(http.MethodPost, "/internal/deepseek-web/pairings/"+ticket.Nonce, strings.NewReader(body))
	replayRequest.RemoteAddr = "127.0.0.1:43122"
	replayRequest.Header.Set("Origin", testDeepSeekExtensionOrigin)
	replayRequest.Header.Set("Content-Type", "application/json")
	replayRequest.Header.Set("X-Omni-Pairing-Nonce", ticket.Nonce)
	h.submitDeepSeekWebPairing(replay, replayRequest)
	if replay.Code != http.StatusGone {
		t.Fatalf("replayed ticket status=%d, want %d; body=%s", replay.Code, http.StatusGone, replay.Body.String())
	}
}

func TestDeepSeekWebPairingRejectsNonLoopbackAndForeignOrigin(t *testing.T) {
	h := newDeepSeekWebPairingTestHandler(t)
	for _, test := range []struct {
		name, remote, origin string
	}{
		{name: "non-loopback", remote: "192.0.2.10:43122", origin: testDeepSeekExtensionOrigin},
		{name: "foreign-origin", remote: "127.0.0.1:43122", origin: "chrome-extension://attacker"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ticket, err := h.deepSeekWebPairings.Create("ds-web", "test account")
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/internal/deepseek-web/pairings/"+ticket.Nonce, strings.NewReader("{}"))
			request.RemoteAddr = test.remote
			request.Header.Set("Origin", test.origin)
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-Omni-Pairing-Nonce", ticket.Nonce)
			response := httptest.NewRecorder()
			h.submitDeepSeekWebPairing(response, request)
			if response.Code != http.StatusForbidden {
				t.Fatalf("status=%d, want %d", response.Code, http.StatusForbidden)
			}
		})
	}
}

func TestDeepSeekWebPairingPreflightAllowsOnlyConfiguredExtension(t *testing.T) {
	h := newDeepSeekWebPairingTestHandler(t)
	request := httptest.NewRequest(http.MethodOptions, "/internal/deepseek-web/pairings", nil)
	request.RemoteAddr = "127.0.0.1:43122"
	request.Header.Set("Origin", testDeepSeekExtensionOrigin)
	request.Header.Set("Access-Control-Request-Method", "POST")
	request.Header.Set("Access-Control-Request-Headers", "content-type, x-omni-pairing-nonce")
	response := httptest.NewRecorder()
	h.deepSeekWebPairingPreflight(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if response.Header().Get("Access-Control-Allow-Origin") != testDeepSeekExtensionOrigin {
		t.Fatal("preflight did not return the configured extension origin")
	}
}

func TestDeepSeekWebPairingCreationRequiresConfiguredGatewayKey(t *testing.T) {
	h := newDeepSeekWebPairingTestHandler(t)
	request := httptest.NewRequest(http.MethodPost, "/admin/deepseek-web/pairings", strings.NewReader(`{"provider_id":"ds-web","account_name":"test"}`))
	response := httptest.NewRecorder()
	h.createDeepSeekWebPairing(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}
