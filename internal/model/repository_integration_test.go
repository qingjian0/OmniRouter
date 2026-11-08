package model

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"omnirouter/internal/storage"
)

func TestModelPricingPersistsAndSurvivesProviderRefresh(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "model-pricing.db")
	runtime, err := storage.OpenSQLiteRuntime(ctx, path, storage.SQLiteOptions{BusyTimeout: time.Second, WAL: true})
	if err != nil {
		t.Fatalf("open SQLite runtime: %v", err)
	}
	defer runtime.Close()

	if _, err := runtime.Store().DB().ExecContext(ctx, `INSERT INTO providers(id,name,kind,base_url,created_at,updated_at) VALUES(?,?,?,?,?,?)`,
		"provider-a", "Provider A", "openai-compatible", "https://example.test/v1", time.Now().UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert provider fixture: %v", err)
	}
	repo, err := NewRepository(runtime.Store().DB())
	if err != nil {
		t.Fatalf("create model repository: %v", err)
	}

	zero, output := 0.0, 1.25
	item := Model{ID: "model-a", ProviderID: "provider-a", Enabled: true, InputCostPerMillionUSD: &zero, OutputCostPerMillionUSD: &output}
	if err := repo.Upsert(ctx, item); err != nil {
		t.Fatalf("upsert model pricing: %v", err)
	}
	if err := repo.SetPricing(ctx, "model-a", "provider-a", 0, 1.25); err != nil {
		t.Fatalf("set model pricing: %v", err)
	}

	// Provider model discovery must not overwrite manually configured prices
	// when the adapter does not publish pricing metadata.
	if err := repo.Upsert(ctx, Model{ID: "model-a", ProviderID: "provider-a", Enabled: true}); err != nil {
		t.Fatalf("refresh model metadata: %v", err)
	}
	items, err := repo.List(ctx, "provider-a")
	if err != nil {
		t.Fatalf("list models: %v", err)
	}
	if len(items) != 1 || items[0].InputCostPerMillionUSD == nil || items[0].OutputCostPerMillionUSD == nil {
		t.Fatalf("pricing was not persisted: %+v", items)
	}
	if *items[0].InputCostPerMillionUSD != 0 || *items[0].OutputCostPerMillionUSD != 1.25 {
		t.Fatalf("unexpected persisted pricing: input=%v output=%v", *items[0].InputCostPerMillionUSD, *items[0].OutputCostPerMillionUSD)
	}

	if err := repo.SetPricing(ctx, "model-a", "provider-a", -1, 1); err == nil {
		t.Fatal("expected negative pricing to be rejected")
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("close SQLite runtime: %v", err)
	}

	reopened, err := storage.OpenSQLiteRuntime(ctx, path, storage.SQLiteOptions{BusyTimeout: time.Second})
	if err != nil {
		t.Fatalf("reopen SQLite runtime: %v", err)
	}
	defer reopened.Close()
	reopenedRepo, err := NewRepository(reopened.Store().DB())
	if err != nil {
		t.Fatalf("create reopened model repository: %v", err)
	}
	persisted, err := reopenedRepo.Get(ctx, "model-a", "provider-a")
	if err != nil {
		t.Fatalf("read persisted model: %v", err)
	}
	if persisted.InputCostPerMillionUSD == nil || persisted.OutputCostPerMillionUSD == nil ||
		*persisted.InputCostPerMillionUSD != 0 || *persisted.OutputCostPerMillionUSD != 1.25 {
		t.Fatalf("pricing did not survive restart: %+v", persisted)
	}
}
