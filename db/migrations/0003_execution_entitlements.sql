CREATE TABLE trade_grant (
  operation_id UUID PRIMARY KEY REFERENCES operations(id),
  ledger TEXT NOT NULL CHECK (ledger IN ('live', 'shadow')),
  market_day DATE NOT NULL,
  authorized_risk_micros BIGINT,
  risk_source TEXT NOT NULL CHECK (risk_source IN ('computed', 'legacy_unknown')),
  granted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (
    (risk_source = 'computed' AND authorized_risk_micros > 0)
    OR
    (risk_source = 'legacy_unknown' AND authorized_risk_micros IS NULL)
  )
);

CREATE INDEX trade_grant_ledger_day ON trade_grant (ledger, market_day);

INSERT INTO trade_grant (
  operation_id, ledger, market_day, authorized_risk_micros, risk_source, granted_at
)
SELECT
  id,
  CASE WHEN COALESCE((payload->>'shadow')::boolean, false)
       THEN 'shadow' ELSE 'live' END,
  (ts AT TIME ZONE current_setting('alpheus.tz_market'))::date,
  CASE WHEN payload ? 'derived_max_risk'
             AND (payload->>'derived_max_risk')::numeric > 0
       THEN ((payload->>'derived_max_risk')::numeric * 1000000)::bigint
       ELSE NULL END,
  CASE WHEN payload ? 'derived_max_risk'
             AND (payload->>'derived_max_risk')::numeric > 0
       THEN 'computed' ELSE 'legacy_unknown' END,
  ts
FROM operations
WHERE class = 'B' AND payload->>'action' = 'open';

CREATE TABLE close_reservation (
  id UUID PRIMARY KEY,
  operation_id UUID UNIQUE NOT NULL REFERENCES operations(id),
  ledger TEXT NOT NULL CHECK (ledger IN ('live', 'shadow')),
  symbol TEXT NOT NULL,
  original_qty BIGINT NOT NULL,
  remaining_qty BIGINT NOT NULL,
  state TEXT NOT NULL CHECK (state IN ('held', 'released')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  released_at TIMESTAMPTZ,
  CHECK (original_qty > 0 AND remaining_qty >= 0 AND remaining_qty <= original_qty),
  CHECK (
    (state = 'held' AND released_at IS NULL)
    OR
    (state = 'released' AND released_at IS NOT NULL)
  )
);

CREATE INDEX close_reservation_held_symbol
  ON close_reservation (ledger, symbol)
  WHERE state = 'held';

CREATE TABLE execution_attempt (
  id UUID PRIMARY KEY,
  operation_id UUID NOT NULL REFERENCES operations(id),
  seq INTEGER NOT NULL,
  close_reservation_id UUID REFERENCES close_reservation(id),
  intent TEXT NOT NULL,
  client_order_id TEXT UNIQUE,
  target_broker_order_id TEXT,
  state TEXT NOT NULL,
  broker_order_id TEXT,
  qty BIGINT,
  limit_micros BIGINT,
  attempt INTEGER NOT NULL DEFAULT 0,
  claimed_by TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  claimed_at TIMESTAMPTZ,
  resolved_at TIMESTAMPTZ,
  last_error TEXT,
  UNIQUE (operation_id, seq),
  CHECK (seq > 0 AND attempt >= 0),
  CHECK (state IN ('pending', 'claimed', 'placed', 'settled', 'failed', 'unknown')),
  CHECK (
    (intent = 'place' AND client_order_id IS NOT NULL
                      AND target_broker_order_id IS NULL
                      AND qty > 0 AND limit_micros > 0)
    OR
    (intent = 'cancel' AND client_order_id IS NULL
                       AND target_broker_order_id IS NOT NULL
                       AND qty IS NULL AND limit_micros IS NULL)
  )
);

CREATE INDEX execution_attempt_recovery
  ON execution_attempt (state, created_at, claimed_at);
