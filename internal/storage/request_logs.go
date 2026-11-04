package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"omnirouter/internal/observability"
)

type RequestLog struct {
	ID              string
	RequestID       string
	ProviderID      string
	ModelID         string
	StatusCode      int
	Latency         time.Duration
	InputTokens     *int64
	OutputTokens    *int64
	ErrorCode       string
	LifecycleStatus string
	UsageSource     string
	UsageFinal      bool
	UsageEligible   bool
	CreatedAt       time.Time
}

// MaxRecentRequestLogs is the maximum number of persisted request records
// exposed by the repository and administrative API.
const MaxRecentRequestLogs = 1000

type RequestLogRepository struct{ db DBTX }

func NewRequestLogRepository(db DBTX) (*RequestLogRepository, error) {
	if db == nil {
		return nil, fmt.Errorf("request log database is required")
	}
	return &RequestLogRepository{db: db}, nil
}
func (r *RequestLogRepository) Append(ctx context.Context, x RequestLog) error {
	if x.ID == "" || x.RequestID == "" {
		return fmt.Errorf("request log id and request_id are required")
	}
	if x.InputTokens != nil && *x.InputTokens < 0 {
		return fmt.Errorf("request log input_tokens must not be negative")
	}
	if x.OutputTokens != nil && *x.OutputTokens < 0 {
		return fmt.Errorf("request log output_tokens must not be negative")
	}
	if x.CreatedAt.IsZero() {
		x.CreatedAt = time.Now().UTC()
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO request_logs(id,request_id,provider_id,model_id,status_code,latency_ms,input_tokens,output_tokens,error_code,created_at,lifecycle_status,token_usage_source,token_usage_final,usage_eligible) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, x.ID, x.RequestID, nullText(x.ProviderID), nullText(x.ModelID), x.StatusCode, x.Latency.Milliseconds(), x.InputTokens, x.OutputTokens, nullText(x.ErrorCode), x.CreatedAt.Format(time.RFC3339Nano), valueOr(x.LifecycleStatus, "unknown"), x.UsageSource, boolInt(x.UsageFinal), boolInt(x.UsageEligible))
	return err
}
func (r *RequestLogRepository) Recent(ctx context.Context, limit int) ([]RequestLog, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > MaxRecentRequestLogs {
		limit = MaxRecentRequestLogs
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id,request_id,COALESCE(provider_id,''),COALESCE(model_id,''),status_code,latency_ms,input_tokens,output_tokens,COALESCE(error_code,''),created_at,lifecycle_status,token_usage_source,token_usage_final,usage_eligible FROM request_logs ORDER BY created_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]RequestLog, 0)
	for rows.Next() {
		var x RequestLog
		var latency int64
		var input, output sql.NullInt64
		var created string
		var usageFinal, usageEligible int
		if err := rows.Scan(&x.ID, &x.RequestID, &x.ProviderID, &x.ModelID, &x.StatusCode, &latency, &input, &output, &x.ErrorCode, &created, &x.LifecycleStatus, &x.UsageSource, &usageFinal, &usageEligible); err != nil {
			return nil, err
		}
		x.Latency = time.Duration(latency) * time.Millisecond
		x.UsageFinal = usageFinal != 0
		x.UsageEligible = usageEligible != 0
		if input.Valid {
			v := input.Int64
			x.InputTokens = &v
		}
		if output.Valid {
			v := output.Int64
			x.OutputTokens = &v
		}
		if x.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
func valueOr(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func nullText(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// AppendRequestRecord persists the HTTP lifecycle record without request bodies,
// authorization headers, query strings, or other user content.
func (r *RequestLogRepository) AppendRequestRecord(ctx context.Context, record observability.RequestRecord) error {
	input, output := record.InputTokens, record.OutputTokens
	return r.Append(ctx, RequestLog{ID: record.ID, RequestID: record.ID, ProviderID: record.ProviderID, ModelID: record.ModelID, StatusCode: record.Status, Latency: time.Duration(record.DurationMS) * time.Millisecond, InputTokens: input, OutputTokens: output, ErrorCode: record.ErrorCode, LifecycleStatus: record.LifecycleStatus, UsageSource: record.UsageSource, UsageFinal: record.UsageFinal, UsageEligible: record.UsageEligible, CreatedAt: record.StartedAt})
}

type RequestUsageSummary struct {
	Requests           int64   `json:"requests"`
	Errors             int64   `json:"errors"`
	InputTokens        int64   `json:"input_tokens"`
	OutputTokens       int64   `json:"output_tokens"`
	KnownTokenRequests int64   `json:"known_token_requests"`
	FinalTokenRequests int64   `json:"final_token_requests"`
	AverageLatencyMS   float64 `json:"average_latency_ms"`
}

func (r *RequestLogRepository) Summary(ctx context.Context) (RequestUsageSummary, error) {
	return summaryWithDB(ctx, r.db)
}

func summaryWithDB(ctx context.Context, db DBTX) (RequestUsageSummary, error) {
	var out RequestUsageSummary
	err := db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(CASE WHEN status_code >= 400 OR lifecycle_status IN ('failed','interrupted','timeout') THEN 1 ELSE 0 END),0), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0), COALESCE(SUM(CASE WHEN input_tokens IS NOT NULL OR output_tokens IS NOT NULL THEN 1 ELSE 0 END),0), COALESCE(SUM(CASE WHEN token_usage_final = 1 THEN 1 ELSE 0 END),0), COALESCE(AVG(latency_ms),0) FROM request_logs WHERE usage_eligible = 1`).Scan(&out.Requests, &out.Errors, &out.InputTokens, &out.OutputTokens, &out.KnownTokenRequests, &out.FinalTokenRequests, &out.AverageLatencyMS)
	if err != nil {
		return RequestUsageSummary{}, fmt.Errorf("aggregate request token usage (possible int64 overflow): %w", err)
	}
	return out, nil
}

type UsageBreakdown struct {
	ProviderID         string `json:"provider_id"`
	ModelID            string `json:"model_id"`
	Requests           int64  `json:"requests"`
	InputTokens        int64  `json:"input_tokens"`
	OutputTokens       int64  `json:"output_tokens"`
	KnownTokenRequests int64  `json:"known_token_requests"`
	FinalTokenRequests int64  `json:"final_token_requests"`
}

func (r *RequestLogRepository) Breakdown(ctx context.Context) ([]UsageBreakdown, error) {
	return breakdownWithDB(ctx, r.db)
}

func breakdownWithDB(ctx context.Context, db DBTX) ([]UsageBreakdown, error) {
	rows, err := db.QueryContext(ctx, `SELECT COALESCE(provider_id,''), COALESCE(model_id,''), COUNT(*), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0), COALESCE(SUM(CASE WHEN input_tokens IS NOT NULL OR output_tokens IS NOT NULL THEN 1 ELSE 0 END),0), COALESCE(SUM(CASE WHEN token_usage_final = 1 THEN 1 ELSE 0 END),0) FROM request_logs WHERE usage_eligible = 1 GROUP BY COALESCE(provider_id,''), COALESCE(model_id,'') ORDER BY COUNT(*) DESC, provider_id, model_id`)
	if err != nil {
		return nil, fmt.Errorf("aggregate provider/model token usage (possible int64 overflow): %w", err)
	}
	defer rows.Close()
	out := make([]UsageBreakdown, 0)
	for rows.Next() {
		var item UsageBreakdown
		if err := rows.Scan(&item.ProviderID, &item.ModelID, &item.Requests, &item.InputTokens, &item.OutputTokens, &item.KnownTokenRequests, &item.FinalTokenRequests); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// UsageReport reads the summary and provider/model breakdown from one read
// transaction so concurrent appends cannot make the two views disagree.
// SQLite integer SUM overflow is returned as an error; totals are never wrapped.
func (r *RequestLogRepository) UsageReport(ctx context.Context) (RequestUsageSummary, []UsageBreakdown, error) {
	var summary RequestUsageSummary
	var breakdown []UsageBreakdown
	err := InTransaction(ctx, r.db, func(tx DBTX) error {
		var err error
		summary, err = summaryWithDB(ctx, tx)
		if err != nil {
			return err
		}
		breakdown, err = breakdownWithDB(ctx, tx)
		return err
	})
	if err != nil {
		return RequestUsageSummary{}, nil, err
	}
	return summary, breakdown, nil
}
