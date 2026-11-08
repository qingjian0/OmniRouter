package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"omnirouter/internal/account"
	"omnirouter/internal/adapter"
	"omnirouter/internal/apikey"
	"omnirouter/internal/config"
	"omnirouter/internal/credential"
	"omnirouter/internal/gateway"
	domainmodel "omnirouter/internal/model"
	"omnirouter/internal/provider"
	"omnirouter/internal/provider/deepseekweb"
	"omnirouter/internal/scheduler"
	"omnirouter/internal/security"
	"omnirouter/internal/session"
	"omnirouter/internal/storage"
)

type App struct {
	server  *http.Server
	logger  *slog.Logger
	storage *storage.Runtime
}

func New(cfg config.Config, logger *slog.Logger) (*App, error) {
	dbCtx, cancelDB := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelDB()
	dbRuntime, err := storage.OpenSQLiteRuntime(dbCtx, cfg.Database.Path, storage.SQLiteOptions{BusyTimeout: cfg.Database.BusyTimeout, WAL: cfg.Database.WAL})
	if err != nil {
		return nil, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = dbRuntime.Close()
		}
	}()
	var vault *credential.Vault
	if cfg.CredentialEncryptionKey != "" {
		encryptor, err := security.NewEncryptor(cfg.CredentialEncryptionKey)
		if err != nil {
			return nil, err
		}
		credentialRepo, err := credential.NewRepository(dbRuntime.Store().DB(), encryptor)
		if err != nil {
			return nil, err
		}
		vault, err = credential.NewPersistentVault(encryptor, credentialRepo)
		if err != nil {
			return nil, err
		}
	}
	providerRepo, err := provider.NewRepository(dbRuntime.Store().DB())
	if err != nil {
		return nil, err
	}
	accountRepo, err := account.NewRepository(dbRuntime.Store().DB())
	if err != nil {
		return nil, err
	}
	sessionRepo, err := session.NewRepository(dbRuntime.Store().DB())
	if err != nil {
		return nil, err
	}
	modelRepo, err := domainmodel.NewRepository(dbRuntime.Store().DB())
	if err != nil {
		return nil, err
	}
	apiKeyRepo, err := apikey.NewRepository(dbRuntime.Store().DB())
	if err != nil {
		return nil, err
	}
	requestLogRepo, err := storage.NewRequestLogRepository(dbRuntime.Store().DB())
	if err != nil {
		return nil, err
	}
	registry := provider.NewRegistry()
	router := scheduler.NewAdaptive(20 * time.Second)
	candidates := make(map[string][]scheduler.Candidate)
	for _, providerConfig := range cfg.Providers {
		providerName := providerConfig.Name
		if providerName == "" {
			providerName = providerConfig.ID
		}
		kind := adapter.NormalizeKind(providerConfig.Kind)
		if kind == adapter.KindDeepSeekWeb && (providerConfig.APIKey != "" || len(providerConfig.Models) > 0) {
			return nil, fmt.Errorf("provider %q: DeepSeek Web registration does not accept API keys or models until its session transport is enabled", providerConfig.ID)
		}
		baseURL := providerConfig.BaseURL
		if strings.TrimSpace(baseURL) == "" {
			baseURL = adapter.DefaultBaseURL(kind)
		}
		providerRecord := provider.ProviderConfig{ID: providerConfig.ID, Name: providerName, Kind: kind, BaseURL: baseURL, Enabled: true, Priority: providerConfig.Priority, Weight: providerConfig.Weight}
		if err := providerRepo.Upsert(dbCtx, providerRecord); err != nil {
			return nil, fmt.Errorf("persist provider %q metadata: %w", providerConfig.ID, err)
		}
		if providerConfig.APIKey != "" {
			if vault == nil || !vault.Persistent() {
				return nil, fmt.Errorf("provider %q has an API key but persistent credential encryption is not configured; set OMNI_CREDENTIAL_ENCRYPTION_KEY", providerConfig.ID)
			}
			accountID, credentialID := provider.BindingIDs(providerConfig.ID)
			if _, getErr := accountRepo.Get(dbCtx, accountID); errors.Is(getErr, sql.ErrNoRows) {
				if createErr := accountRepo.Create(dbCtx, account.Account{ID: accountID, ProviderID: providerConfig.ID, Name: providerName + " credentials", Status: account.StatusActive}); createErr != nil {
					return nil, fmt.Errorf("create provider account %q: %w", providerConfig.ID, createErr)
				}
			} else if getErr != nil {
				return nil, fmt.Errorf("load provider account %q: %w", providerConfig.ID, getErr)
			}
			secret := []byte(providerConfig.APIKey)
			_, putErr := vault.Put(credential.Metadata{ID: credentialID, AccountID: accountID, Type: string(credential.KindAPIKey)}, credential.KindAPIKey, secret)
			for i := range secret {
				secret[i] = 0
			}
			if putErr != nil {
				return nil, fmt.Errorf("persist provider credential %q: %w", providerConfig.ID, putErr)
			}
			providerRecord.AccountID, providerRecord.CredentialID = accountID, credentialID
			if err := providerRepo.Upsert(dbCtx, providerRecord); err != nil {
				return nil, fmt.Errorf("bind provider credential %q: %w", providerConfig.ID, err)
			}
		}
		upstream, err := adapter.New(adapter.Config{Kind: kind, ID: providerConfig.ID, Name: providerName, BaseURL: baseURL, APIKey: providerConfig.APIKey, Models: providerConfig.Models, ModelCapabilities: providerConfig.ModelCapabilities, ModelContextLimits: providerConfig.ModelContextLimits})
		if err != nil {
			return nil, err
		}
		if err := registry.Register(upstream); err != nil {
			return nil, err
		}
		priority := providerConfig.Priority
		if priority == 0 {
			priority = 100
		}
		weight := providerConfig.Weight
		if weight == 0 {
			weight = 1
		}
		models, err := upstream.ListModels(context.Background())
		if err != nil {
			return nil, err
		}
		for _, model := range models {
			if err := modelRepo.Upsert(dbCtx, domainModelFromProvider(model, providerConfig.ID)); err != nil {
				return nil, fmt.Errorf("persist model %q: %w", model.ID, err)
			}
			if err := modelRepo.SetCapabilities(dbCtx, model.ID, providerConfig.ID, model.Capabilities); err != nil {
				return nil, fmt.Errorf("persist model capabilities %q: %w", model.ID, err)
			}
			candidates[model.ID] = append(candidates[model.ID], scheduler.Candidate{Provider: upstream, Priority: priority, Weight: weight, Enabled: true, Capabilities: append([]string(nil), model.Capabilities...), ContextLimit: model.ContextLimit})
		}
	}
	// Rebuild the runtime registry from durable provider metadata. Config is an
	// import/update source; SQLite is the source of truth after synchronization.
	persistedProviders, err := providerRepo.List(dbCtx)
	if err != nil {
		return nil, fmt.Errorf("load persisted providers: %w", err)
	}
	candidates = make(map[string][]scheduler.Candidate)
	for _, record := range persistedProviders {
		if !record.Enabled {
			continue
		}
		apiKey := ""
		if record.AccountID != "" {
			owner, ownerErr := accountRepo.Get(dbCtx, record.AccountID)
			if ownerErr != nil {
				return nil, fmt.Errorf("load provider %q account: %w", record.ID, ownerErr)
			}
			if owner.Status != account.StatusActive {
				continue
			}
		}
		if record.AccountID != "" && record.CredentialID == "" {
			return nil, fmt.Errorf("provider %q has a credential account but no bound credential; refusing to restore it without authentication", record.ID)
		}
		if record.CredentialID != "" {
			if vault == nil {
				return nil, fmt.Errorf("provider %q has a bound credential but credential encryption is not configured", record.ID)
			}
			meta, secret, revealErr := vault.Reveal(record.CredentialID)
			if revealErr != nil {
				return nil, fmt.Errorf("restore provider %q credential (%s): %w", record.ID, meta.ID, revealErr)
			}
			apiKey = string(secret)
			for i := range secret {
				secret[i] = 0
			}
		}
		storedModels, listErr := modelRepo.List(dbCtx, record.ID)
		if listErr != nil {
			return nil, fmt.Errorf("load models for provider %q: %w", record.ID, listErr)
		}
		modelIDs := make([]string, 0, len(storedModels))
		capabilities := make(map[string][]string, len(storedModels))
		contextLimits := make(map[string]int, len(storedModels))
		for _, m := range storedModels {
			if !m.Enabled {
				continue
			}
			modelIDs = append(modelIDs, m.ID)
			for _, cap := range m.Capabilities {
				capabilities[m.ID] = append(capabilities[m.ID], string(cap))
			}
			contextLimits[m.ID] = m.ContextLimit
		}
		upstream, buildErr := adapter.New(adapter.Config{Kind: record.Kind, ID: record.ID, Name: record.Name, BaseURL: record.BaseURL, APIKey: apiKey, Models: modelIDs, ModelCapabilities: capabilities, ModelContextLimits: contextLimits})
		if buildErr != nil {
			return nil, fmt.Errorf("restore provider %q adapter: %w", record.ID, buildErr)
		}
		if err := registry.Replace(upstream); err != nil {
			return nil, err
		}
		priority, weight := record.Priority, record.Weight
		if priority == 0 {
			priority = 100
		}
		if weight == 0 {
			weight = 1
		}
		for _, m := range storedModels {
			if m.Enabled {
				caps := make([]string, 0, len(m.Capabilities))
				for _, cap := range m.Capabilities {
					caps = append(caps, string(cap))
				}
				candidates[m.ID] = append(candidates[m.ID], scheduler.Candidate{
					Provider: upstream, Priority: priority, Weight: weight, Enabled: true,
					Capabilities: caps, ContextLimit: m.ContextLimit,
					InputCostPerMillionUSD:  modelPrice(m.InputCostPerMillionUSD),
					OutputCostPerMillionUSD: modelPrice(m.OutputCostPerMillionUSD),
					PricingKnown:            m.InputCostPerMillionUSD != nil || m.OutputCostPerMillionUSD != nil,
				})
			}
		}
	}
	for model, list := range candidates {
		router.Replace(model, list)
	}
	server := &http.Server{
		Addr:         cfg.HTTP.Address,
		Handler:      gateway.NewHandlerWithDeepSeekWeb(registry, cfg.GatewayAPIKey, router, vault, providerRepo, accountRepo, sessionRepo, modelRepo, apiKeyRepo, cfg.DeepSeekWebExtensionOrigin, requestLogRepo),
		ReadTimeout:  cfg.HTTP.ReadTimeout,
		WriteTimeout: cfg.HTTP.WriteTimeout,
		IdleTimeout:  cfg.HTTP.IdleTimeout,
	}
	cleanup = false
	return &App{server: server, logger: logger, storage: dbRuntime}, nil
}

func (a *App) Run(ctx context.Context) error {
	defer func() {
		if a.storage != nil {
			if err := a.storage.Close(); err != nil {
				a.logger.Error("close database", "error", err)
			}
		}
	}()
	errCh := make(chan error, 1)
	go func() {
		a.logger.Info("HTTP server starting", "address", a.server.Addr)
		errCh <- a.server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.server.Shutdown(shutdownCtx); err != nil {
			_ = a.server.Close()
			return err
		}
		err := <-errCh
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func domainModelFromProvider(item provider.Model, providerID string) domainmodel.Model {
	displayName := item.Name
	if strings.TrimSpace(displayName) == "" {
		displayName = item.ID
	}
	return domainmodel.Model{ID: item.ID, ProviderID: providerID, DisplayName: displayName, ContextLimit: item.ContextLimit, Enabled: true}
}

func modelPrice(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}
