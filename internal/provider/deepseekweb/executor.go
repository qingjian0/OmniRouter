package deepseekweb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"omnirouter/internal/provider"
	"omnirouter/internal/websession"
)

var ErrIncompleteCompletion = errors.New("deepseek web: upstream completion did not reach a verified terminal state")

// AuthorizationLoader exposes only the secret-loading operation needed by the
// executor. Implementations must return caller-owned byte slices.
type AuthorizationLoader interface {
	LoadAuthorization(context.Context, string) (AuthorizationMaterial, error)
}

// ConversationLease is the minimal lineage transaction used by one turn.
type ConversationLease interface {
	Lineage() websession.Lineage
	Commit(context.Context, string, string) (websession.Lineage, error)
	Close()
}

// CompletionLeaseManager is the provider-facing adapter for the concrete
// coordinator. It keeps the coordinator API narrow and mockable in tests.
type CompletionLeaseManager interface {
	BeginLease(context.Context, string, string, string) (ConversationLease, error)
}

type coordinatorLeaseManager struct {
	coordinator *websession.ConversationCoordinator
}

func NewCoordinatorLeaseManager(coordinator *websession.ConversationCoordinator) (*coordinatorLeaseManager, error) {
	if coordinator == nil {
		return nil, fmt.Errorf("conversation coordinator is required")
	}
	return &coordinatorLeaseManager{coordinator: coordinator}, nil
}
func (m *coordinatorLeaseManager) BeginLease(ctx context.Context, providerID, accountID, conversationID string) (ConversationLease, error) {
	return m.coordinator.Begin(ctx, providerID, accountID, conversationID)
}

// UpstreamCompletionTransport is the protocol-specific seam. A real
// implementation must use an authorized, evidence-backed request format; this
// package intentionally does not synthesize private DeepSeek request fields.
type UpstreamCompletionTransport interface {
	Stream(context.Context, AuthorizationMaterial, ChatCompletionInput, websession.Lineage, func(PatchUpdate) error) (CompletionResult, error)
}

// CompletionResult is returned only after the transport has observed its
// protocol's verified successful terminal condition.
type CompletionResult struct {
	UpstreamConversationID string
	ResponseMessageID      string
	Completed              bool
}

type ExecutorConfig struct {
	ID            string
	Name          string
	AccountID     string
	Models        []string
	Authorization AuthorizationLoader
	Leases        CompletionLeaseManager
	Transport     UpstreamCompletionTransport
}

type ExecutorProvider struct {
	id, name, accountID string
	models              []string
	authorization       AuthorizationLoader
	leases              CompletionLeaseManager
	transport           UpstreamCompletionTransport
}

func NewExecutorProvider(cfg ExecutorConfig) (*ExecutorProvider, error) {
	cfg.ID = strings.TrimSpace(cfg.ID)
	cfg.Name = strings.TrimSpace(cfg.Name)
	cfg.AccountID = strings.TrimSpace(cfg.AccountID)
	if cfg.ID == "" || cfg.AccountID == "" || cfg.Authorization == nil || cfg.Leases == nil || cfg.Transport == nil {
		return nil, fmt.Errorf("provider ID, account ID, authorization loader, conversation lease manager, and upstream transport are required")
	}
	models := make([]string, 0, len(cfg.Models))
	seen := make(map[string]struct{}, len(cfg.Models))
	for _, model := range cfg.Models {
		model = strings.TrimSpace(model)
		if model == "" {
			return nil, fmt.Errorf("model IDs must not be empty")
		}
		if _, ok := seen[model]; ok {
			return nil, fmt.Errorf("duplicate model ID %q", model)
		}
		seen[model] = struct{}{}
		models = append(models, model)
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("at least one explicitly enabled model is required")
	}
	if cfg.Name == "" {
		cfg.Name = cfg.ID
	}
	return &ExecutorProvider{id: cfg.ID, name: cfg.Name, accountID: cfg.AccountID, models: models, authorization: cfg.Authorization, leases: cfg.Leases, transport: cfg.Transport}, nil
}

func (p *ExecutorProvider) ID() string   { return p.id }
func (p *ExecutorProvider) Name() string { return p.name }

func (p *ExecutorProvider) ListModels(context.Context) ([]provider.Model, error) {
	models := make([]provider.Model, 0, len(p.models))
	for _, id := range p.models {
		models = append(models, provider.Model{ID: id, Name: modelDisplayName(id), Provider: p.id, Capabilities: []string{"chat", "streaming"}})
	}
	return models, nil
}

func (p *ExecutorProvider) Generate(ctx context.Context, req *provider.Request) (*provider.Response, error) {
	if req == nil {
		return nil, fmt.Errorf("request is required")
	}
	var content, reasoning strings.Builder
	err := p.execute(ctx, req, func(update PatchUpdate) error {
		content.WriteString(update.ContentDelta)
		reasoning.WriteString(update.ReasoningDelta)
		return nil
	})
	if err != nil {
		return nil, err
	}
	message := map[string]any{"role": "assistant", "content": content.String()}
	if reasoning.Len() > 0 {
		message["reasoning_content"] = reasoning.String()
	}
	body, err := json.Marshal(map[string]any{
		"id":      "deepseek-web-" + fmt.Sprint(time.Now().UnixNano()),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   req.Model,
		"choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": "stop"}},
	})
	if err != nil {
		return nil, fmt.Errorf("encode DeepSeek Web completion: %w", err)
	}
	return &provider.Response{Model: req.Model, Output: body}, nil
}

func (p *ExecutorProvider) Stream(ctx context.Context, req *provider.Request, emit func(provider.Event) error) error {
	if emit == nil {
		return fmt.Errorf("event handler is required")
	}
	if req == nil {
		return fmt.Errorf("request is required")
	}
	encoder, err := NewOpenAIStreamEncoder(req.Model)
	if err != nil {
		return err
	}
	return p.execute(ctx, req, func(update PatchUpdate) error {
		if update.Done {
			return nil // terminal chunk is emitted only after lineage commit
		}
		payload, err := encoder.Encode(update)
		if err != nil || len(payload) == 0 {
			return err
		}
		return emit(provider.Event{Data: append(payload, '\n', '\n')})
	}, func() error {
		payload, err := encoder.Encode(PatchUpdate{Done: true})
		if err != nil {
			return err
		}
		if err := emit(provider.Event{Data: append(payload, '\n', '\n')}); err != nil {
			return err
		}
		return emit(provider.Event{Done: true, ExplicitDone: true})
	})
}

func (p *ExecutorProvider) execute(ctx context.Context, req *provider.Request, consume func(PatchUpdate) error, afterCommit ...func() error) error {
	if req == nil {
		return fmt.Errorf("request is required")
	}
	// The gateway conversation header is optional. Without it, use a unique
	// one-turn lease key and deliberately do not persist lineage: callers that
	// want continuity must supply the same local ID on every request.
	localConversationID := strings.TrimSpace(req.ConversationID)
	persistLineage := localConversationID != ""
	if !persistLineage {
		var raw [16]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return fmt.Errorf("generate one-turn conversation ID: %w", err)
		}
		localConversationID = "ephemeral_" + hex.EncodeToString(raw[:])
	}
	input, err := MapChatCompletionInput(req.Input)
	if err != nil {
		return err
	}
	if input.Model != req.Model {
		return fmt.Errorf("request model %q does not match payload model %q", req.Model, input.Model)
	}
	enabled := false
	for _, model := range p.models {
		if model == req.Model {
			enabled = true
			break
		}
	}
	if !enabled {
		return fmt.Errorf("model %q is not enabled for this provider", req.Model)
	}

	material, err := p.authorization.LoadAuthorization(ctx, p.accountID)
	if err != nil {
		return err
	}
	defer zeroBytes(material.Token)
	defer zeroBytes(material.CookieHeader)

	lease, err := p.leases.BeginLease(ctx, p.id, p.accountID, localConversationID)
	if err != nil {
		return err
	}
	defer lease.Close()

	sawTerminal := false
	result, err := p.transport.Stream(ctx, material, input, lease.Lineage(), func(update PatchUpdate) error {
		if sawTerminal {
			return fmt.Errorf("%w: update received after terminal event", ErrIncompleteCompletion)
		}
		if update.Done {
			sawTerminal = true
			return nil
		}
		return consume(update)
	})
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !result.Completed || !sawTerminal || result.UpstreamConversationID == "" || result.ResponseMessageID == "" {
		return ErrIncompleteCompletion
	}
	if persistLineage {
		if _, err := lease.Commit(ctx, result.UpstreamConversationID, result.ResponseMessageID); err != nil {
			return fmt.Errorf("commit successful DeepSeek Web conversation lineage: %w", err)
		}
	}
	for _, finish := range afterCommit {
		if err := finish(); err != nil {
			return err
		}
	}
	return nil
}

func (p *ExecutorProvider) HealthCheck(ctx context.Context) error {
	material, err := p.authorization.LoadAuthorization(ctx, p.accountID)
	if err != nil {
		return err
	}
	zeroBytes(material.Token)
	zeroBytes(material.CookieHeader)
	return nil
}
