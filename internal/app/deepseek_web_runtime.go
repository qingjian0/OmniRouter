package app

import (
	"context"
	"fmt"

	"omnirouter/internal/model"
	"omnirouter/internal/provider"
	"omnirouter/internal/provider/deepseekweb"
	"omnirouter/internal/scheduler"
)

// deepSeekWebRuntimeRegistrar activates a just-paired browser account without
// requiring a process restart. The provider's model allowlist remains the
// operator-configured model catalogue stored in SQLite.
type deepSeekWebRuntimeRegistrar struct {
	registry      *provider.Registry
	router        *scheduler.AdaptiveScheduler
	models        *model.Repository
	providers     *provider.Repository
	authorization *deepseekweb.AuthorizationService
	leases        deepseekweb.CompletionLeaseManager
	transport     deepseekweb.UpstreamCompletionTransport
}

func (r *deepSeekWebRuntimeRegistrar) RegisterDeepSeekWebAccount(ctx context.Context, providerID, accountID, accountName string) error {
	if r == nil || r.registry == nil || r.router == nil || r.models == nil ||
		r.providers == nil || r.authorization == nil || r.leases == nil || r.transport == nil {
		return fmt.Errorf("DeepSeek Web runtime registration is not configured")
	}
	configured, err := r.models.List(ctx, providerID)
	if err != nil {
		return fmt.Errorf("load configured DeepSeek Web models: %w", err)
	}
	modelIDs := make([]string, 0, len(configured))
	for _, item := range configured {
		if item.Enabled {
			modelIDs = append(modelIDs, item.ID)
		}
	}
	if len(modelIDs) == 0 {
		return fmt.Errorf("DeepSeek Web provider has no enabled models")
	}
	record, err := r.providers.Get(ctx, providerID)
	if err != nil {
		return fmt.Errorf("load DeepSeek Web provider policy: %w", err)
	}
	executor, err := deepseekweb.NewExecutorProvider(deepseekweb.ExecutorConfig{
		ID: providerID, Name: record.Name + " (" + accountName + ")", AccountID: accountID,
		Models: modelIDs, Authorization: r.authorization, Leases: r.leases, Transport: r.transport,
	})
	if err != nil {
		return fmt.Errorf("construct DeepSeek Web executor: %w", err)
	}
	if err := r.registry.Replace(executor); err != nil {
		return fmt.Errorf("register DeepSeek Web executor: %w", err)
	}
	models, err := executor.ListModels(ctx)
	if err != nil {
		return err
	}
	priority, weight := record.Priority, record.Weight
	if priority == 0 {
		priority = 100
	}
	if weight == 0 {
		weight = 1
	}
	r.router.AddOrReplaceProviderModels(executor, models, priority, weight, record.Enabled)
	return nil
}
