CREATE TABLE daily_pnl (
  market_day DATE NOT NULL,
  ledger TEXT NOT NULL CHECK (ledger IN ('live', 'shadow')),
  local_realized_pnl_micros BIGINT NOT NULL,
  provider_realized_pnl_micros BIGINT,
  effective_realized_pnl_micros BIGINT NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (market_day, ledger),
  CHECK (provider_realized_pnl_micros IS NULL
         OR effective_realized_pnl_micros <= provider_realized_pnl_micros),
  CHECK (effective_realized_pnl_micros <= local_realized_pnl_micros)
);

CREATE TABLE breaker_state (
  ledger TEXT PRIMARY KEY CHECK (ledger IN ('live', 'shadow')),
  halted BOOLEAN NOT NULL DEFAULT false,
  reason TEXT,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK ((halted AND reason IS NOT NULL AND btrim(reason) <> '')
         OR (NOT halted AND reason IS NULL))
);

INSERT INTO breaker_state (ledger, halted) VALUES ('live', false), ('shadow', false);

CREATE TABLE breaker_override (
  ledger TEXT NOT NULL CHECK (ledger IN ('live', 'shadow')),
  reason TEXT NOT NULL CHECK (reason IN ('daily_loss', 'loss_streak', 'pnl_divergence')),
  market_day DATE NOT NULL,
  subject TEXT NOT NULL CHECK (btrim(subject) <> ''),
  ts TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (ledger, reason, market_day)
);

CREATE INDEX fills_m3c_ledger_ts ON fills (ledger, ts, id);
