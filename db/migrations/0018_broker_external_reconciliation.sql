-- B0-6: deterministic reconciliation of broker-side position changes which
-- happened outside an Alpheus lifecycle.  The mutable projection is only a
-- restart checkpoint; economic evidence and every internal exposure reduction
-- remain append-only.  No row in these tables is an order, fill, or PnL claim.
ALTER TABLE broker_observation
  ADD COLUMN local_state_generation BIGINT NOT NULL DEFAULT 0
  CHECK (local_state_generation >= 0);

CREATE TABLE broker_local_state_revision (
  singleton BOOLEAN PRIMARY KEY DEFAULT true CHECK (singleton),
  generation BIGINT NOT NULL CHECK (generation >= 0),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
INSERT INTO broker_local_state_revision(singleton,generation) VALUES (true,0);

CREATE FUNCTION bump_broker_local_state_revision() RETURNS trigger AS $$
BEGIN
  UPDATE broker_local_state_revision
  SET generation=generation+1,updated_at=clock_timestamp()
  WHERE singleton=true;
  RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER orders_broker_local_state_revision
  AFTER INSERT OR UPDATE OR DELETE ON orders
  FOR EACH STATEMENT EXECUTE FUNCTION bump_broker_local_state_revision();
CREATE TRIGGER fills_broker_local_state_revision
  AFTER INSERT OR UPDATE OR DELETE ON fills
  FOR EACH STATEMENT EXECUTE FUNCTION bump_broker_local_state_revision();

CREATE TABLE broker_reconciliation_head (
  account_id TEXT PRIMARY KEY CHECK (btrim(account_id) <> ''),
  observation_id UUID UNIQUE NOT NULL REFERENCES broker_observation(id),
  generation BIGINT UNIQUE NOT NULL CHECK (generation > 0),
  reconciled_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE broker_position_projection (
  account_id TEXT NOT NULL CHECK (btrim(account_id) <> ''),
  symbol TEXT NOT NULL CHECK (btrim(symbol) <> ''),
  kind TEXT NOT NULL CHECK (btrim(kind) <> ''),
  observation_id UUID NOT NULL REFERENCES broker_observation(id),
  observation_generation BIGINT NOT NULL CHECK (observation_generation > 0),
  provider_qty BIGINT NOT NULL,
  tracked_qty BIGINT NOT NULL CHECK (tracked_qty >= 0),
  position_keys JSONB NOT NULL CHECK (jsonb_typeof(position_keys)='array'),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (account_id,symbol,kind)
);

CREATE TABLE broker_external_change_episode (
  id UUID PRIMARY KEY,
  account_id TEXT NOT NULL CHECK (btrim(account_id) <> ''),
  broker_observation_id UUID NOT NULL REFERENCES broker_observation(id),
  observation_generation BIGINT NOT NULL CHECK (observation_generation > 0),
  symbol TEXT NOT NULL CHECK (btrim(symbol) <> ''),
  kind TEXT NOT NULL CHECK (btrim(kind) <> ''),
  change_kind TEXT NOT NULL CHECK (change_kind IN (
    'baseline','external_add','external_reduce','external_reversal'
  )),
  origin TEXT NOT NULL CHECK (origin IN ('external','ambiguous','mixed')),
  provider_qty_before BIGINT,
  provider_qty_after BIGINT NOT NULL,
  tracked_qty_before BIGINT NOT NULL CHECK (tracked_qty_before >= 0),
  tracked_qty_after BIGINT NOT NULL CHECK (tracked_qty_after >= 0),
  adjusted_tracked_qty BIGINT NOT NULL CHECK (adjusted_tracked_qty >= 0),
  position_keys JSONB NOT NULL CHECK (jsonb_typeof(position_keys)='array'),
  attribution_status TEXT NOT NULL CHECK (attribution_status='uncertain'),
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  UNIQUE (broker_observation_id,symbol,kind),
  CHECK (tracked_qty_after <= tracked_qty_before),
  CHECK (adjusted_tracked_qty = tracked_qty_before-tracked_qty_after)
);

CREATE INDEX broker_external_change_account_generation
  ON broker_external_change_episode (account_id,observation_generation DESC,id);

CREATE TABLE broker_external_exposure_allocation (
  episode_id UUID NOT NULL REFERENCES broker_external_change_episode(id),
  open_fill_id UUID NOT NULL REFERENCES exposure_lot(open_fill_id),
  qty BIGINT NOT NULL CHECK (qty > 0),
  matched_cost_micros BIGINT NOT NULL CHECK (matched_cost_micros >= 0),
  released_risk_micros BIGINT NOT NULL CHECK (released_risk_micros >= 0),
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (episode_id,open_fill_id)
);

CREATE TABLE broker_operation_invalidation (
  operation_id UUID PRIMARY KEY REFERENCES operations(id),
  broker_observation_id UUID NOT NULL REFERENCES broker_observation(id),
  observation_generation BIGINT NOT NULL CHECK (observation_generation > 0),
  reason TEXT NOT NULL CHECK (reason IN ('external_broker_state_changed')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

CREATE TRIGGER broker_external_change_episode_immutable
  BEFORE UPDATE OR DELETE ON broker_external_change_episode
  FOR EACH ROW EXECUTE FUNCTION reject_broker_observation_mutation();
CREATE TRIGGER broker_external_exposure_allocation_immutable
  BEFORE UPDATE OR DELETE ON broker_external_exposure_allocation
  FOR EACH ROW EXECUTE FUNCTION reject_broker_observation_mutation();
CREATE TRIGGER broker_operation_invalidation_immutable
  BEFORE UPDATE OR DELETE ON broker_operation_invalidation
  FOR EACH ROW EXECUTE FUNCTION reject_broker_observation_mutation();
