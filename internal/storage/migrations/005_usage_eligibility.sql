-- Older request rows predate the inference/admin distinction. Keep them
-- visible in request history, but exclude them from usage aggregates because
-- their original route cannot be reconstructed reliably.
ALTER TABLE request_logs ADD COLUMN usage_eligible INTEGER NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_request_logs_usage_eligible_created ON request_logs(usage_eligible, created_at);
