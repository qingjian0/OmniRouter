-- Durable mapping between a local API conversation and a vendor web conversation.
-- Upstream identifiers are sensitive correlation metadata and must never be logged.
CREATE TABLE IF NOT EXISTS web_conversation_lineage (
    provider_id TEXT NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    local_conversation_id TEXT NOT NULL,
    upstream_conversation_id TEXT NOT NULL,
    parent_message_id TEXT,
    generation INTEGER NOT NULL DEFAULT 1 CHECK (generation > 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (provider_id, account_id, local_conversation_id)
);
CREATE INDEX IF NOT EXISTS idx_web_lineage_account_updated
    ON web_conversation_lineage(account_id, updated_at);
