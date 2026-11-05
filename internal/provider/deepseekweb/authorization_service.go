package deepseekweb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"

	"omnirouter/internal/account"
	"omnirouter/internal/credential"
	"omnirouter/internal/session"
)

// AuthorizationService coordinates account, encrypted credentials, and
// session metadata. It stores no plaintext secret outside the Vault write
// boundary. Each authorization creates a new account identity so replacement
// and revocation cannot accidentally revive a revoked credential.
type AuthorizationService struct {
	accounts *account.Repository
	sessions *session.Repository
	vault    *credential.Vault
}

type AuthorizationRecord struct {
	AccountID          string
	SessionID          string
	AccessTokenID      string
	CookieCredentialID string
	TokenVersion       uint64
	CookieVersion      uint64
}

// NewAuthorizationService requires a persistent encrypted vault. An in-memory
// vault is intentionally rejected because web sessions must survive restart.
func NewAuthorizationService(accounts *account.Repository, sessions *session.Repository, vault *credential.Vault) (*AuthorizationService, error) {
	if accounts == nil || sessions == nil || vault == nil || !vault.Persistent() {
		return nil, fmt.Errorf("account repository, session repository, and persistent credential vault are required")
	}
	return &AuthorizationService{accounts: accounts, sessions: sessions, vault: vault}, nil
}

// Authorize validates user-supplied browser state, encrypts the token and
// cookie header separately, and records local session metadata. It does not
// claim that DeepSeek has accepted the session; that requires a later,
// user-enabled low-impact validation operation.
func (s *AuthorizationService) Authorize(ctx context.Context, providerID, accountName string, input AuthorizationInput, expiresAt *time.Time) (AuthorizationRecord, error) {
	providerID = stringsTrim(providerID)
	accountName = stringsTrim(accountName)
	if providerID == "" || accountName == "" {
		return AuthorizationRecord{}, fmt.Errorf("provider ID and account name are required")
	}
	material, err := ValidateAuthorization(input)
	if err != nil {
		return AuthorizationRecord{}, err
	}
	defer zeroBytes(material.Token)
	defer zeroBytes(material.CookieHeader)

	accountID, err := randomID("dsweb_account_")
	if err != nil {
		return AuthorizationRecord{}, err
	}
	sessionID, err := randomID("dsweb_session_")
	if err != nil {
		return AuthorizationRecord{}, err
	}
	tokenID, err := randomID("dsweb_token_")
	if err != nil {
		return AuthorizationRecord{}, err
	}
	cookieID, err := randomID("dsweb_cookie_")
	if err != nil {
		return AuthorizationRecord{}, err
	}
	if err := s.accounts.Create(ctx, account.Account{ID: accountID, ProviderID: providerID, Name: accountName, Status: account.StatusActive}); err != nil {
		return AuthorizationRecord{}, fmt.Errorf("create DeepSeek web account: %w", err)
	}
	rollback := func() {
		_, _ = s.vault.DeleteContext(ctx, tokenID)
		_, _ = s.vault.DeleteContext(ctx, cookieID)
		_ = s.sessions.Delete(ctx, sessionID)
		_ = s.accounts.Delete(ctx, accountID)
	}
	tokenMeta, err := s.vault.Put(credential.Metadata{ID: tokenID, AccountID: accountID, Type: string(credential.KindAccessToken), ExpiresAt: expiresAt}, credential.KindAccessToken, material.Token)
	if err != nil {
		_ = s.accounts.Delete(ctx, accountID)
		return AuthorizationRecord{}, fmt.Errorf("store encrypted DeepSeek web access token: %w", err)
	}
	cookieMeta, err := s.vault.Put(credential.Metadata{ID: cookieID, AccountID: accountID, Type: string(credential.KindCookie), ExpiresAt: expiresAt}, credential.KindCookie, material.CookieHeader)
	if err != nil {
		rollback()
		return AuthorizationRecord{}, fmt.Errorf("store encrypted DeepSeek web cookie: %w", err)
	}
	localStatus := session.StatusActive
	if expiresAt != nil && !expiresAt.After(time.Now()) {
		localStatus = session.StatusExpired
	}
	if err := s.sessions.Save(ctx, session.Session{ID: sessionID, AccountID: accountID, Status: localStatus, ExpiresAt: expiresAt}); err != nil {
		rollback()
		return AuthorizationRecord{}, fmt.Errorf("persist DeepSeek web session metadata: %w", err)
	}
	return AuthorizationRecord{
		AccountID: accountID, SessionID: sessionID,
		AccessTokenID: tokenID, CookieCredentialID: cookieID,
		TokenVersion: tokenMeta.Version, CookieVersion: cookieMeta.Version,
	}, nil
}

// Revoke disables the account and irreversibly revokes all credentials and
// sessions owned by it. Re-authentication must create a fresh account.
func (s *AuthorizationService) Revoke(ctx context.Context, accountID string) error {
	items, err := s.vault.ListContext(ctx, accountID)
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.Status == credential.StatusRevoked {
			continue
		}
		if _, found, err := s.vault.SetStatus(item.ID, credential.StatusRevoked); err != nil {
			return err
		} else if !found {
			return fmt.Errorf("credential %q disappeared during revocation", item.ID)
		}
	}
	sessions, err := s.sessions.List(ctx, accountID)
	if err != nil {
		return err
	}
	for _, item := range sessions {
		if item.Status != session.StatusRevoked {
			if err := s.sessions.Revoke(ctx, item.ID); err != nil {
				return err
			}
		}
	}
	return s.accounts.SetStatus(ctx, accountID, account.StatusDisabled)
}


// LoadAuthorization restores the encrypted pair after process restart. It
// requires exactly one active access token and one active cookie credential
// for the account; incomplete or ambiguous pairs fail closed.
func (s *AuthorizationService) LoadAuthorization(ctx context.Context, accountID string) (AuthorizationMaterial, error) {
	owner, err := s.accounts.Get(ctx, accountID)
	if err != nil || owner.Status != account.StatusActive {
		return AuthorizationMaterial{}, ErrAuthorizationRequired
	}
	accountSessions, err := s.sessions.List(ctx, accountID)
	if err != nil {
		return AuthorizationMaterial{}, err
	}
	activeSessions := 0
	for _, item := range accountSessions {
		if item.Status == session.StatusActive {
			activeSessions++
		}
	}
	if activeSessions != 1 {
		return AuthorizationMaterial{}, ErrAuthorizationRequired
	}
	items, err := s.vault.ListContext(ctx, accountID)
	if err != nil {
		return AuthorizationMaterial{}, err
	}
	var tokenID, cookieID string
	for _, item := range items {
		switch item.Kind {
		case credential.KindAccessToken:
			if tokenID != "" {
				return AuthorizationMaterial{}, ErrAuthorizationRequired
			}
			tokenID = item.ID
		case credential.KindCookie:
			if cookieID != "" {
				return AuthorizationMaterial{}, ErrAuthorizationRequired
			}
			cookieID = item.ID
		}
	}
	if tokenID == "" || cookieID == "" {
		return AuthorizationMaterial{}, ErrAuthorizationRequired
	}
	tokenMeta, token, err := s.vault.Reveal(tokenID)
	if err != nil {
		return AuthorizationMaterial{}, ErrAuthorizationRequired
	}
	defer zeroBytes(token)
	cookieMeta, cookieHeader, err := s.vault.Reveal(cookieID)
	if err != nil {
		return AuthorizationMaterial{}, ErrAuthorizationRequired
	}
	defer zeroBytes(cookieHeader)
	if tokenMeta.AccountID != accountID || cookieMeta.AccountID != accountID ||
		tokenMeta.Kind != credential.KindAccessToken || cookieMeta.Kind != credential.KindCookie ||
		len(token) == 0 || len(token) > MaxTokenBytes || len(cookieHeader) == 0 || len(cookieHeader) > MaxCookieBytes {
		return AuthorizationMaterial{}, ErrAuthorizationRequired
	}
	req, _ := http.NewRequest(http.MethodGet, DefaultOrigin, nil)
	req.Header.Set("Cookie", string(cookieHeader))
	hasSession := false
	for _, item := range req.Cookies() {
		if item.Name == "ds_session_id" && item.Value != "" {
			hasSession = true
		}
	}
	if !hasSession {
		return AuthorizationMaterial{}, ErrAuthorizationRequired
	}
	for _, r := range string(token) {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return AuthorizationMaterial{}, ErrAuthorizationRequired
		}
	}
	return AuthorizationMaterial{Token: append([]byte(nil), token...), CookieHeader: append([]byte(nil), cookieHeader...)}, nil
}

func randomID(prefix string) (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate authorization identifier: %w", err)
	}
	return prefix + hex.EncodeToString(raw[:]), nil
}

func zeroBytes(value []byte) {
	for i := range value {
		value[i] = 0
	}
}

func stringsTrim(value string) string {
	return strings.TrimSpace(value)
}
