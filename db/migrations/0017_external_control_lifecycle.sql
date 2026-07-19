-- B0-4: auditable management of broker objects which were not created by an
-- Alpheus lifecycle.  An episode records the observed external/mixed object
-- and the accounting split authorized for the new management operation.  It
-- never adopts the earlier broker action or invents an Alpheus open/fill.
CREATE TABLE external_control_episode (
  id UUID PRIMARY KEY,
  operation_id UUID UNIQUE NOT NULL REFERENCES operations(id),
  control_action TEXT NOT NULL CHECK (control_action IN ('cancel_order','close_position')),
  origin TEXT NOT NULL CHECK (origin IN ('external','ambiguous','mixed')),
  broker_observation_id UUID NOT NULL REFERENCES broker_observation(id),
  observation_generation BIGINT NOT NULL CHECK (observation_generation > 0),
  object_key TEXT NOT NULL CHECK (btrim(object_key) <> ''),
  requested_qty BIGINT NOT NULL DEFAULT 0 CHECK (requested_qty >= 0),
  tracked_qty BIGINT NOT NULL DEFAULT 0 CHECK (tracked_qty >= 0),
  external_qty BIGINT NOT NULL DEFAULT 0 CHECK (external_qty >= 0),
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  CHECK (
    (control_action='cancel_order'
      AND requested_qty=0 AND tracked_qty=0 AND external_qty=0)
    OR
    (control_action='close_position'
      AND requested_qty > 0
      AND requested_qty = tracked_qty + external_qty)
  )
);

CREATE TABLE external_control_fill_allocation (
  close_fill_id UUID PRIMARY KEY REFERENCES fills(id),
  episode_id UUID NOT NULL REFERENCES external_control_episode(id),
  qty BIGINT NOT NULL CHECK (qty > 0),
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

CREATE INDEX external_control_fill_episode
  ON external_control_fill_allocation (episode_id,close_fill_id);

CREATE TRIGGER external_control_episode_immutable
  BEFORE UPDATE OR DELETE ON external_control_episode
  FOR EACH ROW EXECUTE FUNCTION reject_broker_observation_mutation();
CREATE TRIGGER external_control_fill_allocation_immutable
  BEFORE UPDATE OR DELETE ON external_control_fill_allocation
  FOR EACH ROW EXECUTE FUNCTION reject_broker_observation_mutation();
