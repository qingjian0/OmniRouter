ALTER TABLE request_logs ADD COLUMN lifecycle_status TEXT NOT NULL DEFAULT 'unknown';
ALTER TABLE request_logs ADD COLUMN token_usage_source TEXT NOT NULL DEFAULT '';
ALTER TABLE request_logs ADD COLUMN token_usage_final INTEGER NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_request_logs_model_created ON request_logs(model_id, created_at);
