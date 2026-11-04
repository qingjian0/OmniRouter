-- Bind a provider to its owning account and encrypted credential without
-- storing any secret material in the provider metadata row.
ALTER TABLE providers ADD COLUMN account_id TEXT REFERENCES accounts(id) ON DELETE SET NULL;
ALTER TABLE providers ADD COLUMN credential_id TEXT REFERENCES credentials(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_providers_account ON providers(account_id);
CREATE INDEX IF NOT EXISTS idx_providers_credential ON providers(credential_id);
