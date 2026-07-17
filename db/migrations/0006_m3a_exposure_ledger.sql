CREATE TABLE feature_activation (
  name TEXT PRIMARY KEY,
  activated_at TIMESTAMPTZ NOT NULL,
  cutoff TIMESTAMPTZ NOT NULL
);

CREATE TABLE open_reservation (
  id UUID PRIMARY KEY,
  operation_id UUID UNIQUE NOT NULL REFERENCES operations(id),
  ledger TEXT NOT NULL CHECK (ledger IN ('live', 'shadow')),
  market_day DATE NOT NULL,
  symbol TEXT NOT NULL CHECK (btrim(symbol) <> ''),
  kind TEXT NOT NULL CHECK (btrim(kind) <> ''),
  original_qty BIGINT NOT NULL,
  remaining_qty BIGINT NOT NULL,
  original_risk_micros BIGINT NOT NULL,
  remaining_risk_micros BIGINT NOT NULL,
  original_cash_micros BIGINT NOT NULL,
  remaining_cash_micros BIGINT NOT NULL,
  resource_state TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  settled_at TIMESTAMPTZ,
  CHECK (original_qty > 0 AND remaining_qty >= 0 AND remaining_qty <= original_qty),
  CHECK (original_risk_micros >= 0 AND remaining_risk_micros >= 0
         AND remaining_risk_micros <= original_risk_micros),
  CHECK (original_cash_micros >= 0 AND remaining_cash_micros >= 0
         AND remaining_cash_micros <= original_cash_micros),
  CHECK (resource_state IN ('held', 'converted', 'released')),
  CHECK (resource_state <> 'held' OR remaining_qty > 0),
  CHECK (resource_state <> 'converted' OR
         (remaining_qty=0 AND remaining_risk_micros=0 AND remaining_cash_micros=0)),
  CHECK (resource_state <> 'released' OR
         (remaining_risk_micros=0 AND remaining_cash_micros=0)),
  CHECK ((resource_state='held' AND settled_at IS NULL)
         OR (resource_state<>'held' AND settled_at IS NOT NULL))
);

CREATE INDEX open_reservation_held_ledger
  ON open_reservation (ledger, market_day, operation_id)
  WHERE resource_state = 'held';

ALTER TABLE execution_attempt
  ADD COLUMN open_reservation_id UUID REFERENCES open_reservation(id);

ALTER TABLE execution_attempt DROP CONSTRAINT execution_attempt_check1;
ALTER TABLE execution_attempt ADD CONSTRAINT execution_attempt_intent_check CHECK (
  (intent='place' AND client_order_id IS NOT NULL
                  AND target_broker_order_id IS NULL
                  AND qty > 0 AND limit_micros > 0)
  OR
  (intent='paper_place' AND client_order_id IS NOT NULL
                        AND target_broker_order_id IS NULL
                        AND qty > 0 AND limit_micros > 0)
  OR
  (intent='cancel' AND client_order_id IS NULL
                   AND target_broker_order_id IS NOT NULL
                   AND qty IS NULL AND limit_micros IS NULL)
);

CREATE TABLE exposure_lot (
  open_fill_id UUID PRIMARY KEY REFERENCES fills(id),
  operation_id UUID NOT NULL REFERENCES operations(id),
  ledger TEXT NOT NULL CHECK (ledger IN ('live', 'shadow')),
  symbol TEXT NOT NULL,
  kind TEXT NOT NULL,
  multiplier BIGINT NOT NULL,
  opened_qty BIGINT NOT NULL,
  closed_qty BIGINT NOT NULL DEFAULT 0,
  entry_cost_micros BIGINT NOT NULL,
  remaining_cost_basis_micros BIGINT NOT NULL,
  remaining_risk_micros BIGINT NOT NULL,
  opened_at TIMESTAMPTZ NOT NULL,
  closed_at TIMESTAMPTZ,
  CHECK (multiplier > 0 AND opened_qty > 0
         AND closed_qty >= 0 AND closed_qty <= opened_qty),
  CHECK (entry_cost_micros > 0
         AND remaining_cost_basis_micros >= 0
         AND remaining_cost_basis_micros <= entry_cost_micros
         AND remaining_risk_micros >= 0
         AND remaining_risk_micros <= entry_cost_micros),
  CHECK ((closed_qty=opened_qty AND closed_at IS NOT NULL)
         OR (closed_qty<opened_qty AND closed_at IS NULL))
);

CREATE INDEX exposure_lot_fifo_open
  ON exposure_lot (ledger, symbol, kind, opened_at, open_fill_id)
  WHERE closed_qty < opened_qty;

CREATE TABLE exposure_close_allocation (
  close_fill_id UUID NOT NULL REFERENCES fills(id),
  open_fill_id UUID NOT NULL REFERENCES exposure_lot(open_fill_id),
  qty BIGINT NOT NULL,
  matched_cost_micros BIGINT NOT NULL,
  released_risk_micros BIGINT NOT NULL,
  PRIMARY KEY (close_fill_id, open_fill_id),
  CHECK (qty > 0 AND matched_cost_micros >= 0 AND released_risk_micros >= 0)
);

CREATE TABLE shadow_account (
  singleton BOOLEAN PRIMARY KEY DEFAULT true CHECK (singleton),
  cash_micros BIGINT NOT NULL CHECK (cash_micros >= 0),
  buying_power_micros BIGINT NOT NULL CHECK (buying_power_micros >= 0),
  activated_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE shadow_positions (
  symbol TEXT PRIMARY KEY CHECK (btrim(symbol) <> ''),
  kind TEXT NOT NULL CHECK (btrim(kind) <> ''),
  multiplier BIGINT NOT NULL CHECK (multiplier > 0),
  qty BIGINT NOT NULL CHECK (qty > 0),
  updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE day_open (
  market_day DATE NOT NULL,
  ledger TEXT NOT NULL CHECK (ledger IN ('live', 'shadow')),
  equity_micros BIGINT NOT NULL,
  PRIMARY KEY (market_day, ledger)
);
