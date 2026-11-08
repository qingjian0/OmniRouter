-- Persist operator-configured model pricing in USD per one million tokens.
-- NULL means unknown; zero is an explicit zero-price value.
ALTER TABLE models ADD COLUMN input_cost_per_million_usd REAL;
ALTER TABLE models ADD COLUMN output_cost_per_million_usd REAL;
