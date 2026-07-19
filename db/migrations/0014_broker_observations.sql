-- B0-1: immutable Provider observations and evidence-backed broker-object
-- origin. These records describe shared broker reality; they never adopt an
-- external object into an Alpheus lifecycle.
CREATE TABLE broker_observation (
  id UUID PRIMARY KEY,
  generation BIGSERIAL UNIQUE NOT NULL,
  account_id TEXT NOT NULL CHECK (btrim(account_id) <> ''),
  source TEXT NOT NULL CHECK (btrim(source) <> ''),
  purpose TEXT NOT NULL CHECK (purpose IN (
    'decision','pre_effect','reconciliation','read_model','manual_refresh'
  )),
  started_at TIMESTAMPTZ NOT NULL,
  completed_at TIMESTAMPTZ NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('complete','partial')),
  manifest_digest BYTEA NOT NULL CHECK (octet_length(manifest_digest)=32),
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  CHECK (completed_at >= started_at)
);

CREATE INDEX broker_observation_account_generation
  ON broker_observation (account_id,generation DESC);

CREATE TABLE broker_observation_family (
  observation_id UUID NOT NULL REFERENCES broker_observation(id),
  family TEXT NOT NULL CHECK (family IN ('account','positions','orders','fills')),
  status TEXT NOT NULL CHECK (status IN ('success','error')),
  error_code TEXT,
  completed_at TIMESTAMPTZ NOT NULL,
  item_count INTEGER NOT NULL CHECK (item_count >= 0),
  family_digest BYTEA NOT NULL CHECK (octet_length(family_digest)=32),
  PRIMARY KEY (observation_id,family),
  CHECK (
    (status='success' AND error_code IS NULL)
    OR (status='error' AND btrim(error_code) <> '' AND length(error_code)<=64
        AND error_code ~ '^[a-z0-9_]+$' AND item_count=0)
  )
);

CREATE TABLE broker_observation_item (
  observation_id UUID NOT NULL,
  family TEXT NOT NULL,
  object_key TEXT NOT NULL CHECK (btrim(object_key) <> ''),
  observed_at TIMESTAMPTZ NOT NULL,
  object_digest BYTEA NOT NULL CHECK (octet_length(object_digest)=32),
  canonical JSONB NOT NULL CHECK (jsonb_typeof(canonical)='object'),
  PRIMARY KEY (observation_id,family,object_key),
  FOREIGN KEY (observation_id,family)
    REFERENCES broker_observation_family(observation_id,family)
);

CREATE TABLE broker_object_origin_event (
  id BIGSERIAL PRIMARY KEY,
  observation_id UUID NOT NULL REFERENCES broker_observation(id),
  family TEXT NOT NULL CHECK (family IN ('positions','orders','fills')),
  object_key TEXT NOT NULL CHECK (btrim(object_key) <> ''),
  origin TEXT NOT NULL CHECK (origin IN ('alpheus','external','ambiguous')),
  evidence TEXT NOT NULL CHECK (evidence IN (
    'exact_broker_order_id','exact_client_reference','exact_broker_fill_id',
    'unmatched','aggregate_overlap','identity_conflict'
  )),
  matched_order_id UUID REFERENCES orders(id),
  matched_attempt_id UUID REFERENCES execution_attempt(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  UNIQUE (observation_id,family,object_key),
  CHECK (
    (origin='alpheus' AND matched_order_id IS NOT NULL AND matched_attempt_id IS NOT NULL)
    OR origin<>'alpheus'
  )
);

-- The head is only advanced by a complete account+positions+orders snapshot.
-- A partial or action-specific read remains durable evidence but cannot become
-- the canonical aggregate account view.
CREATE TABLE broker_observation_head (
  account_id TEXT PRIMARY KEY,
  observation_id UUID UNIQUE NOT NULL REFERENCES broker_observation(id),
  generation BIGINT UNIQUE NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL
);

CREATE FUNCTION reject_broker_observation_mutation() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'broker observation evidence is append-only';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER broker_observation_immutable
  BEFORE UPDATE OR DELETE ON broker_observation
  FOR EACH ROW EXECUTE FUNCTION reject_broker_observation_mutation();
CREATE TRIGGER broker_observation_family_immutable
  BEFORE UPDATE OR DELETE ON broker_observation_family
  FOR EACH ROW EXECUTE FUNCTION reject_broker_observation_mutation();
CREATE TRIGGER broker_observation_item_immutable
  BEFORE UPDATE OR DELETE ON broker_observation_item
  FOR EACH ROW EXECUTE FUNCTION reject_broker_observation_mutation();
CREATE TRIGGER broker_object_origin_event_immutable
  BEFORE UPDATE OR DELETE ON broker_object_origin_event
  FOR EACH ROW EXECUTE FUNCTION reject_broker_observation_mutation();
