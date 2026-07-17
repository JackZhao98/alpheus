CREATE TABLE live_canary_revision (
  id BIGSERIAL PRIMARY KEY,
  daily_authorized_risk_micros BIGINT NOT NULL CHECK (daily_authorized_risk_micros > 0),
  clean_days_before_raise INTEGER NOT NULL CHECK (clean_days_before_raise > 0),
  effective_market_day DATE NOT NULL,
  recorded_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX live_canary_revision_latest ON live_canary_revision (id DESC);
