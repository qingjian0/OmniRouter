package app

import (
    "context"
    "fmt"

    "omnirouter/internal/provider/deepseekweb"
)

// AuthorizeDeepSeekWebAccount is the native-host entry point for an explicit,
// user-initiated Android WebView pairing flow. Callers must scope extraction to
// chat.deepseek.com and must never automate login or anti-abuse challenges.
func (a *App) AuthorizeDeepSeekWebAccount(
    ctx context.Context,
    providerID string,
    accountName string,
    input deepseekweb.AuthorizationInput,
) (deepseekweb.AuthorizationRecord, error) {
    if a == nil || a.webAuthorization == nil {
        return deepseekweb.AuthorizationRecord{}, fmt.Errorf("persistent DeepSeek Web authorization is unavailable")
    }
    record, err := a.webAuthorization.Authorize(ctx, providerID, accountName, input, nil)
    if err != nil {
        return deepseekweb.AuthorizationRecord{}, err
    }
    if a.webRegistrar != nil {
        if err := a.webRegistrar.RegisterDeepSeekWebAccount(ctx, providerID, record.AccountID, accountName); err != nil {
            _ = a.webAuthorization.Revoke(ctx, record.AccountID)
            return deepseekweb.AuthorizationRecord{}, fmt.Errorf("activate paired DeepSeek Web account: %w", err)
        }
    }
    return record, nil
}
