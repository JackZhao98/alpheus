-- M2.9 replaces the pre-execution placeholder tables with typed, durable
-- execution facts. Preserve any legacy rows for audit/recovery instead of
-- coercing payload JSON or NUMERIC money into authoritative live facts.
ALTER TABLE fills RENAME TO fills_legacy_m1;
ALTER TABLE orders RENAME TO orders_legacy_m1;

CREATE TABLE orders_m29 (
  id UUID PRIMARY KEY,
  operation_id UUID NOT NULL REFERENCES operations(id),
  execution_attempt_id UUID UNIQUE NOT NULL REFERENCES execution_attempt(id),
  broker_order_id TEXT UNIQUE,
  client_order_id TEXT UNIQUE NOT NULL,
  ledger TEXT NOT NULL CHECK (ledger IN ('live', 'shadow')),
  symbol TEXT NOT NULL CHECK (btrim(symbol) <> ''),
  side TEXT NOT NULL CHECK (side IN ('buy', 'sell')),
  kind TEXT NOT NULL CHECK (btrim(kind) <> ''),
  multiplier BIGINT NOT NULL,
  qty BIGINT NOT NULL,
  limit_micros BIGINT NOT NULL,
  state TEXT NOT NULL CHECK (state IN (
    'new', 'submitted', 'partially_filled', 'filled',
    'cancelled', 'rejected', 'expired'
  )),
  reprices INTEGER NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (multiplier > 0 AND qty > 0 AND limit_micros > 0 AND reprices >= 0)
);

CREATE INDEX orders_m29_working
  ON orders_m29 (state, updated_at)
  WHERE state IN ('submitted', 'partially_filled');

CREATE TABLE fills_m29 (
  id UUID PRIMARY KEY,
  order_id UUID NOT NULL REFERENCES orders_m29(id),
  broker_fill_id TEXT UNIQUE NOT NULL CHECK (btrim(broker_fill_id) <> ''),
  ledger TEXT NOT NULL CHECK (ledger IN ('live', 'shadow')),
  qty BIGINT NOT NULL,
  price_micros BIGINT NOT NULL,
  fees_micros BIGINT NOT NULL,
  ts TIMESTAMPTZ NOT NULL,
  CHECK (qty > 0 AND price_micros > 0 AND fees_micros >= 0)
);

CREATE INDEX fills_m29_order_ts ON fills_m29 (order_id, ts, id);

ALTER TABLE orders_m29 RENAME TO orders;
ALTER TABLE fills_m29 RENAME TO fills;

COMMENT ON TABLE orders_legacy_m1 IS
  'Pre-M2.9 placeholder rows retained for audit; never read as execution facts.';
COMMENT ON TABLE fills_legacy_m1 IS
  'Pre-M2.9 placeholder rows retained for audit; never read as execution facts.';
