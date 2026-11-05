package gateway

import (
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"omnirouter/internal/provider/deepseekweb"
)

// createDeepSeekWebPairing is an authenticated admin operation. The returned
// nonce is a short-lived capability and must not be logged or persisted.
func (h *Handler) createDeepSeekWebPairing(w http.ResponseWriter, r *http.Request) {
	if h.apiKey == "" {
		writeError(w, http.StatusServiceUnavailable, "admin_auth_required", "set OMNI_GATEWAY_API_KEY before creating DeepSeek web pairing tickets")
		return
	}
	if h.deepSeekWebAuthorization == nil || h.providerRepo == nil || h.deepSeekWebPairings == nil {
		writeError(w, http.StatusServiceUnavailable, "storage_unavailable", "persistent DeepSeek web authorization is not configured")
		return
	}
	var input struct {
		ProviderID  string `json:"provider_id"`
		AccountName string `json:"account_name"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "valid provider_id and account_name JSON are required")
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "pairing request must contain exactly one JSON value")
		return
	}
	if strings.TrimSpace(input.ProviderID) == "" || strings.TrimSpace(input.AccountName) == "" ||
		len(input.ProviderID) > 128 || len(input.AccountName) > 80 {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "provider_id and account_name are required and must be within their length limits")
		return
	}
	record, err := h.providerRepo.Get(r.Context(), strings.TrimSpace(input.ProviderID))
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "provider not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", "could not load provider metadata")
		return
	}
	if !strings.EqualFold(strings.TrimSpace(record.Kind), deepseekweb.ProviderKind) {
		writeError(w, http.StatusBadRequest, "invalid_provider", "the selected provider is not a DeepSeek web provider")
		return
	}
	ticket, err := h.deepSeekWebPairings.Create(input.ProviderID, input.AccountName)
	if err != nil {
		writeError(w, http.StatusTooManyRequests, "pairing_unavailable", "could not create a pairing ticket")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]any{
		"nonce": ticket.Nonce, "expires_at": ticket.ExpiresAt,
		"provider_id": strings.TrimSpace(input.ProviderID),
		"account_name": strings.TrimSpace(input.AccountName),
	})
}

// deepSeekWebPairingPreflight exposes only the exact configured extension
// origin. It does not enable general CORS for the gateway.
func (h *Handler) deepSeekWebPairingPreflight(w http.ResponseWriter, r *http.Request) {
	if h.deepSeekWebAuthorization == nil || h.deepSeekWebExtensionOrigin == "" {
		writeError(w, http.StatusServiceUnavailable, "pairing_unavailable", "DeepSeek web browser pairing is not configured")
		return
	}
	origin := r.Header.Get("Origin")
	if !deepseekweb.IsAllowedExtensionOrigin(origin, h.deepSeekWebExtensionOrigin) {
		writeError(w, http.StatusForbidden, "origin_rejected", "browser extension origin is not allowed")
		return
	}
	if !deepseekweb.IsLoopbackPeer(r.RemoteAddr) {
		writeError(w, http.StatusForbidden, "peer_rejected", "pairing submissions are accepted only from loopback clients")
		return
	}
	if requested := strings.ToUpper(strings.TrimSpace(r.Header.Get("Access-Control-Request-Method"))); requested != http.MethodPost {
		writeError(w, http.StatusForbidden, "preflight_rejected", "only POST is allowed")
		return
	}
	if requestedHeaders := strings.TrimSpace(r.Header.Get("Access-Control-Request-Headers")); requestedHeaders != "" {
		for _, header := range strings.Split(requestedHeaders, ",") {
			if !strings.EqualFold(strings.TrimSpace(header), "content-type") && !strings.EqualFold(strings.TrimSpace(header), "x-omni-pairing-nonce") {
				writeError(w, http.StatusForbidden, "preflight_rejected", "requested CORS header is not allowed")
				return
			}
		}
	}
	h.setDeepSeekWebPairingCORS(w, origin)
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Omni-Pairing-Nonce")
	w.Header().Set("Access-Control-Max-Age", "120")
	if strings.EqualFold(r.Header.Get("Access-Control-Request-Private-Network"), "true") {
		w.Header().Set("Access-Control-Allow-Private-Network", "true")
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) submitDeepSeekWebPairing(w http.ResponseWriter, r *http.Request) {
	if h.deepSeekWebAuthorization == nil || h.deepSeekWebExtensionOrigin == "" || h.deepSeekWebPairings == nil {
		writeError(w, http.StatusServiceUnavailable, "pairing_unavailable", "DeepSeek web browser pairing is not configured")
		return
	}
	origin := r.Header.Get("Origin")
	if !deepseekweb.IsAllowedExtensionOrigin(origin, h.deepSeekWebExtensionOrigin) {
		writeError(w, http.StatusForbidden, "origin_rejected", "browser extension origin is not allowed")
		return
	}
	if !deepseekweb.IsLoopbackPeer(r.RemoteAddr) {
		writeError(w, http.StatusForbidden, "peer_rejected", "pairing submissions are accepted only from loopback clients")
		return
	}
	h.setDeepSeekWebPairingCORS(w, origin)
	w.Header().Set("Cache-Control", "no-store")

	nonce := strings.TrimSpace(r.Header.Get("X-Omni-Pairing-Nonce"))
	if len(nonce) != 64 {
		writeError(w, http.StatusBadRequest, "invalid_pairing", "pairing ticket is invalid or expired")
		return
	}
	if _, err := hex.DecodeString(nonce); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_pairing", "pairing ticket is invalid or expired")
		return
	}
	target, err := h.deepSeekWebPairings.Consume(nonce)
	if err != nil {
		writeError(w, http.StatusGone, "pairing_expired", "pairing ticket is invalid, expired, or already used")
		return
	}
	if h.providerRepo == nil {
		writeError(w, http.StatusServiceUnavailable, "storage_unavailable", "provider repository is unavailable")
		return
	}
	record, err := h.providerRepo.Get(r.Context(), target.ProviderID)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "paired provider no longer exists")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", "could not load paired provider")
		return
	}
	if !strings.EqualFold(strings.TrimSpace(record.Kind), deepseekweb.ProviderKind) {
		writeError(w, http.StatusConflict, "invalid_provider", "paired provider is no longer a DeepSeek web provider")
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "application/json is required")
		return
	}

	var payload struct {
		Token   []byte `json:"token"`
		Cookies []struct {
			Name   string `json:"name"`
			Value  string `json:"value"`
			Domain string `json:"domain"`
			Path   string `json:"path"`
			Secure bool   `json:"secure"`
		} `json:"cookies"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_authorization", "authorization payload is invalid or exceeds 64 KiB")
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_authorization", "authorization payload must contain exactly one JSON value")
		return
	}
	cookies := make([]deepseekweb.BrowserCookie, 0, len(payload.Cookies))
	for _, item := range payload.Cookies {
		cookies = append(cookies, deepseekweb.BrowserCookie{Name: item.Name, Value: item.Value, Domain: item.Domain, Path: item.Path, Secure: item.Secure})
	}
	defer func() {
		for i := range payload.Token {
			payload.Token[i] = 0
		}
		for i := range payload.Cookies {
			payload.Cookies[i].Value = ""
		}
		for i := range cookies {
			cookies[i].Value = ""
		}
	}()
	authorization, err := h.deepSeekWebAuthorization.Authorize(r.Context(), target.ProviderID, target.AccountName, deepseekweb.AuthorizationInput{Token: payload.Token, Cookies: cookies}, nil)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_authorization", "DeepSeek web authorization material failed validation")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"provider_id": target.ProviderID,
		"account_id": authorization.AccountID,
		"session_id": authorization.SessionID,
		"status": "stored",
		"message": "authorization stored",
	})
}

func (h *Handler) setDeepSeekWebPairingCORS(w http.ResponseWriter, origin string) {
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Add("Vary", "Origin")
}

func (h *Handler) revokeDeepSeekWebAccount(w http.ResponseWriter, r *http.Request) {
	if h.apiKey == "" {
		writeError(w, http.StatusServiceUnavailable, "admin_auth_required", "set OMNI_GATEWAY_API_KEY before revoking DeepSeek web sessions")
		return
	}
	if h.deepSeekWebAuthorization == nil || h.accountRepo == nil || h.providerRepo == nil {
		writeError(w, http.StatusServiceUnavailable, "storage_unavailable", "persistent DeepSeek web authorization is not configured")
		return
	}
	accountID := strings.TrimSpace(r.PathValue("id"))
	if accountID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "account ID is required")
		return
	}
	owner, err := h.accountRepo.Get(r.Context(), accountID)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "account not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", "could not load account metadata")
		return
	}
	providerRecord, err := h.providerRepo.Get(r.Context(), owner.ProviderID)
	if err != nil || !strings.EqualFold(strings.TrimSpace(providerRecord.Kind), deepseekweb.ProviderKind) {
		writeError(w, http.StatusBadRequest, "invalid_account", "account is not linked to a DeepSeek web provider")
		return
	}
	if err := h.deepSeekWebAuthorization.Revoke(r.Context(), accountID); err != nil {
		writeError(w, http.StatusInternalServerError, "revoke_failed", "could not revoke all DeepSeek web credentials and sessions")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"account_id": accountID, "status": "revoked"})
}
