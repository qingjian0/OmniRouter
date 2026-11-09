package model

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"
	"omnirouter/internal/storage"
	"time"
)

type Repository struct{ db storage.DBTX }

func NewRepository(db storage.DBTX) (*Repository, error) {
	if db == nil {
		return nil, fmt.Errorf("model repository database is required")
	}
	return &Repository{db: db}, nil
}
func (r *Repository) Upsert(ctx context.Context, item Model) error {
	if item.ID == "" || item.ProviderID == "" {
		return fmt.Errorf("model id and provider are required")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := r.db.ExecContext(ctx, `INSERT INTO models(id,provider_id,display_name,context_limit,input_cost_per_million_usd,output_cost_per_million_usd,enabled,updated_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(id,provider_id) DO UPDATE SET display_name=excluded.display_name,context_limit=excluded.context_limit,input_cost_per_million_usd=COALESCE(excluded.input_cost_per_million_usd,models.input_cost_per_million_usd),output_cost_per_million_usd=COALESCE(excluded.output_cost_per_million_usd,models.output_cost_per_million_usd),enabled=excluded.enabled,updated_at=excluded.updated_at`, item.ID, item.ProviderID, item.DisplayName, nullInt(item.ContextLimit), item.InputCostPerMillionUSD, item.OutputCostPerMillionUSD, boolInt(item.Enabled), now)
	return err
}
func (r *Repository) SetCapabilities(ctx context.Context, modelID, providerID string, capabilities []string) error {
	return storage.InTransaction(ctx, r.db, func(tx storage.DBTX) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM model_capabilities WHERE model_id=? AND provider_id=?`, modelID, providerID); err != nil {
			return err
		}
		for _, cap := range capabilities {
			if cap == "" {
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO model_capabilities(model_id,provider_id,capability) VALUES(?,?,?)`, modelID, providerID, cap); err != nil {
				return err
			}
		}
		return nil
	})
}
func (r *Repository) List(ctx context.Context, providerID string) ([]Model, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,provider_id,COALESCE(display_name,''),COALESCE(context_limit,0),input_cost_per_million_usd,output_cost_per_million_usd,enabled FROM models WHERE (?='' OR provider_id=?) ORDER BY provider_id,id`, providerID, providerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Model, 0)
	for rows.Next() {
		var m Model
		var enabled int
		var inputCost, outputCost sql.NullFloat64
		if err := rows.Scan(&m.ID, &m.ProviderID, &m.DisplayName, &m.ContextLimit, &inputCost, &outputCost, &enabled); err != nil {
			return nil, err
		}
		m.Enabled = enabled != 0
		if inputCost.Valid {
			value := inputCost.Float64
			m.InputCostPerMillionUSD = &value
		}
		if outputCost.Valid {
			value := outputCost.Float64
			m.OutputCostPerMillionUSD = &value
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		caps, err := r.Capabilities(ctx, out[i].ID, out[i].ProviderID)
		if err != nil {
			return nil, err
		}
		out[i].Capabilities = caps
	}
	return out, nil
}

func (r *Repository) Get(ctx context.Context, modelID, providerID string) (Model, error) {
	items, err := r.List(ctx, providerID)
	if err != nil {
		return Model{}, err
	}
	for _, item := range items {
		if item.ID == modelID {
			return item, nil
		}
	}
	return Model{}, sql.ErrNoRows
}

func (r *Repository) Capabilities(ctx context.Context, modelID, providerID string) ([]Capability, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT capability FROM model_capabilities WHERE model_id=? AND provider_id=? ORDER BY capability`, modelID, providerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Capability, 0)
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		out = append(out, Capability(value))
	}
	return out, rows.Err()
}

func (r *Repository) SetPricing(ctx context.Context, modelID, providerID string, inputCost, outputCost float64) error {
	if math.IsNaN(inputCost) || math.IsInf(inputCost, 0) || inputCost < 0 ||
		math.IsNaN(outputCost) || math.IsInf(outputCost, 0) || outputCost < 0 {
		return fmt.Errorf("model token prices must be finite non-negative numbers")
	}
	res, err := r.db.ExecContext(ctx, `UPDATE models SET input_cost_per_million_usd=?,output_cost_per_million_usd=?,updated_at=? WHERE id=? AND provider_id=?`, inputCost, outputCost, time.Now().UTC().Format(time.RFC3339Nano), modelID, providerID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return sql.ErrNoRows
	}
	return err
}

func (r *Repository) SetDisplayName(ctx context.Context, modelID, providerID, displayName string) error {
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		return fmt.Errorf("model display name is required")
	}
	res, err := r.db.ExecContext(ctx, `UPDATE models SET display_name=?,updated_at=? WHERE id=? AND provider_id=?`, displayName, time.Now().UTC().Format(time.RFC3339Nano), modelID, providerID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return sql.ErrNoRows
	}
	return err
}

func (r *Repository) SetEnabled(ctx context.Context, modelID, providerID string, enabled bool) error {
	res, err := r.db.ExecContext(ctx, `UPDATE models SET enabled=?,updated_at=? WHERE id=? AND provider_id=?`, boolInt(enabled), time.Now().UTC().Format(time.RFC3339Nano), modelID, providerID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return sql.ErrNoRows
	}
	return err
}

func (r *Repository) Delete(ctx context.Context, modelID, providerID string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM models WHERE id=? AND provider_id=?`, modelID, providerID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return sql.ErrNoRows
	}
	return err
}
func nullInt(v int) any {
	if v <= 0 {
		return nil
	}
	return v
}
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
