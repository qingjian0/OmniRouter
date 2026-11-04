-- Persist credential lifecycle state. Existing rows remain active by default.
ALTER TABLE credentials ADD COLUMN status TEXT NOT NULL DEFAULT 'active';
