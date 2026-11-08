package deepseekweb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"omnirouter/internal/websession"
)

const (
	usersCurrentPath = "/api/v0/users/current"
	createSessionPath = "/api/v0/chat_session/create"
	completionPath = "/api/v0/chat/completion"
	maxJSONResponseBytes = 1 << 20
)

// HTTPCompletionTransport performs the ordinary, user-authorized DeepSeek Web
// request flow. It does not synthesize browser fingerprints, cookies, or
// anti-abuse challenge responses. If the upstream requires such a challenge,
// the request fails closed with ErrPolicyBlocked.
type HTTPCompletionTransport struct {
	client *websession.HTTPClient
}

// NewHTTPCompletionTransport creates an origin-pinned transport for the
// DeepSeek consumer web API.
func NewHTTPCompletionTransport() (*HTTPCompletionTransport, error) {
	client, err := websession.NewHTTPClient(DefaultOrigin, "/api/v0")
	if err != nil {
		return nil, fmt.Errorf("initialize DeepSeek Web HTTP client: %w", err)
	}
	return &HTTPCompletionTransport{client: client}, nil
}

type usersCurrentResponse struct {
	Code int `json:"code"`
	Msg string `json:"msg"`
	Data struct {
		BizData struct {
			Token string `json:"token"`
		} `json:"biz_data"`
	} `json:"data"`
	BizData struct {
		Token string `json:"token"`
	} `json:"biz_data"`
}

type createSessionResponse struct {
	Code int `json:"code"`
	Data struct {
		BizData struct {
			ChatSession struct {
				ID string `json:"id"`
			} `json:"chat_session"`
		} `json:"biz_data"`
	} `json:"data"`
	BizData struct {
		ChatSession struct {
			ID string `json:"id"`
		} `json:"chat_session"`
	} `json:"biz_data"`
}

// Stream validates the supplied browser session, creates an upstream chat
// session when needed, and converts DeepSeek SSE patches into normalized
// updates. Conversation lineage is committed by ExecutorProvider only after
// this method confirms FINISHED and a response message ID.
func (t *HTTPCompletionTransport) Stream(
	ctx context.Context,
	material AuthorizationMaterial,
	input ChatCompletionInput,
	lineage websession.Lineage,
	emit func(PatchUpdate) error,
) (CompletionResult, error) {
	if t == nil || t.client == nil {
		return CompletionResult{}, errors.New("DeepSeek Web transport is not initialized")
	}
	if len(material.Token) == 0 || len(material.CookieHeader) == 0 {
		return CompletionResult{}, ErrAuthorizationRequired
	}
	if emit == nil {
		return CompletionResult{}, errors.New("DeepSeek Web update handler is required")
	}

	accessToken, err := t.acquireAccessToken(ctx, material)
	if err != nil {
		return CompletionResult{}, err
	}
	defer zeroBytes(accessToken)

	sessionID := strings.TrimSpace(lineage.UpstreamConversationID)
	if sessionID == "" {
		sessionID, err = t.createSession(ctx, material, accessToken)
		if err != nil {
			return CompletionResult{}, err
		}
	}

	payload := struct {
		ChatSessionID string   `json:"chat_session_id"`
		ParentMessageID *string `json:"parent_message_id"`
		ModelType string         `json:"model_type"`
		Prompt string            `json:"prompt"`
		RefFileIDs []string      `json:"ref_file_ids"`
		ThinkingEnabled bool     `json:"thinking_enabled"`
		SearchEnabled bool       `json:"search_enabled"`
		Preempt bool             `json:"preempt"`
	}{
		ChatSessionID: sessionID,
		ParentMessageID: optionalString(lineage.ParentMessageID),
		ModelType: input.ModelType,
		Prompt: input.Prompt,
		RefFileIDs: input.RefFileIDs,
		ThinkingEnabled: input.ThinkingEnabled,
		SearchEnabled: input.SearchEnabled,
		Preempt: false,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return CompletionResult{}, fmt.Errorf("encode DeepSeek Web completion request: %w", err)
	}
	headers := t.headers(accessToken, material.CookieHeader)
	headers.Set("Content-Type", "application/json")
	headers.Set("Accept", "text/event-stream")
	response, err := t.client.Do(ctx, http.MethodPost, completionPath, headers, bytes.NewReader(body))
	if err != nil {
		return CompletionResult{}, fmt.Errorf("send DeepSeek Web completion request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return CompletionResult{}, SafeFailure(response.StatusCode)
	}
	contentType := strings.ToLower(response.Header.Get("Content-Type"))
	if !strings.Contains(contentType, "text/event-stream") {
		return CompletionResult{}, fmt.Errorf("%w: expected SSE response, received %q", ErrUpstreamChanged, contentType)
	}

	decoder := &PatchDecoder{}
	result := CompletionResult{UpstreamConversationID: sessionID}
	terminal := false
	err = ParseSSE(response.Body, 1<<20, func(frame SSEFrame) error {
		update, decodeErr := decoder.DecodeFrame(frame)
		if decodeErr != nil {
			return decodeErr
		}
		if update.ResponseMessageID != "" {
			result.ResponseMessageID = update.ResponseMessageID
		}
		if update.Done {
			// DeepSeek may emit search_results after FINISHED. Delay the
			// normalized terminal signal until the complete SSE body drains.
			terminal = true
			return nil
		}
		return emit(update)
	})
	if err != nil {
		return CompletionResult{}, fmt.Errorf("read DeepSeek Web completion stream: %w", err)
	}
	if !terminal || result.ResponseMessageID == "" {
		return CompletionResult{}, ErrIncompleteCompletion
	}
	if err := emit(PatchUpdate{Done: true}); err != nil {
		return CompletionResult{}, err
	}
	result.Completed = true
	return result, nil
}

func (t *HTTPCompletionTransport) acquireAccessToken(ctx context.Context, material AuthorizationMaterial) ([]byte, error) {
	headers := t.headers(material.Token, material.CookieHeader)
	headers.Set("Accept", "application/json")
	response, err := t.client.Do(ctx, http.MethodGet, usersCurrentPath, headers, nil)
	if err != nil {
		return nil, fmt.Errorf("validate DeepSeek Web authorization: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, SafeFailure(response.StatusCode)
	}
	raw, err := websession.ReadBoundedBody(response.Body, maxJSONResponseBytes)
	if err != nil {
		return nil, err
	}
	var decoded usersCurrentResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("%w: invalid users/current JSON", ErrUpstreamChanged)
	}
	if decoded.Code != 0 {
		return nil, ErrAuthorizationRequired
	}
	token := decoded.Data.BizData.Token
	if token == "" {
		token = decoded.BizData.Token
	}
	if strings.TrimSpace(token) == "" {
		return nil, ErrAuthorizationRequired
	}
	return []byte(token), nil
}

func (t *HTTPCompletionTransport) createSession(ctx context.Context, material AuthorizationMaterial, accessToken []byte) (string, error) {
	headers := t.headers(accessToken, material.CookieHeader)
	headers.Set("Content-Type", "application/json")
	headers.Set("Accept", "application/json")
	response, err := t.client.Do(ctx, http.MethodPost, createSessionPath, headers, bytes.NewReader([]byte("{}")))
	if err != nil {
		return "", fmt.Errorf("create DeepSeek Web chat session: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", SafeFailure(response.StatusCode)
	}
	raw, err := websession.ReadBoundedBody(response.Body, maxJSONResponseBytes)
	if err != nil {
		return "", err
	}
	var decoded createSessionResponse
	if err := json.Unmarshal(raw, &decoded); err != nil || decoded.Code != 0 {
		return "", fmt.Errorf("%w: invalid chat_session/create response", ErrUpstreamChanged)
	}
	id := decoded.Data.BizData.ChatSession.ID
	if id == "" {
		id = decoded.BizData.ChatSession.ID
	}
	if strings.TrimSpace(id) == "" {
		return "", fmt.Errorf("%w: chat session ID missing", ErrUpstreamChanged)
	}
	return id, nil
}

func (t *HTTPCompletionTransport) headers(token, cookie []byte) http.Header {
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+string(token))
	headers.Set("Cookie", string(cookie))
	headers.Set("Origin", DefaultOrigin)
	headers.Set("Referer", DefaultOrigin+"/")
	headers.Set("Accept-Language", "en-US,en;q=0.9")
	headers.Set("User-Agent", "OmniRouter/DeepSeek-Web")
	return headers
}

func optionalString(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	trimmed := strings.TrimSpace(value)
	return &trimmed
}

// drainAndClose is retained as a small helper for transports that may later\n// add bounded response-body diagnostics; it never returns upstream body text.
func drainAndClose(body io.ReadCloser) {
	if body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(body, 4<<10))
	_ = body.Close()
}
