-- Read-only intraday GEXBot Classic collection. Configuration is database
-- state, not deployment configuration, so the owner can change symbols and
-- cadence without rebuilding the Kernel.
CREATE TABLE gexbot_collection_config (
  singleton BOOLEAN PRIMARY KEY DEFAULT true CHECK (singleton),
  enabled BOOLEAN NOT NULL DEFAULT false,
  symbols TEXT[] NOT NULL DEFAULT ARRAY['SPX']::TEXT[],
  interval_minutes INTEGER NOT NULL DEFAULT 1 CHECK (interval_minutes IN (1,5,10,15)),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

INSERT INTO gexbot_collection_config(singleton) VALUES (true)
ON CONFLICT (singleton) DO NOTHING;

CREATE TABLE gexbot_observation (
  id UUID PRIMARY KEY,
  symbol TEXT NOT NULL CHECK (symbol ~ '^[A-Z0-9._-]{1,16}$'),
  category TEXT NOT NULL CHECK (category = 'gex_full'),
  observed_at TIMESTAMPTZ NOT NULL,
  source_timestamp TIMESTAMPTZ NOT NULL,
  payload_digest BYTEA NOT NULL CHECK (octet_length(payload_digest)=32),
  spot NUMERIC,
  zero_gamma NUMERIC,
  payload JSONB NOT NULL CHECK (jsonb_typeof(payload)='object'),
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  UNIQUE(symbol,category,observed_at)
);

CREATE INDEX gexbot_observation_symbol_time
  ON gexbot_observation(symbol,observed_at DESC);
