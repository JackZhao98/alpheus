-- B0-2: every live Provider mutation is bound to an immutable, expiring
-- pre-effect manifest.  The manifest points at the exact aggregate broker
-- observation used for the decision and carries the canonical action-specific
-- facts (quote/instrument or exact cancel target).  A manifest is evidence,
-- never a claim of ownership over an external broker object.
CREATE TABLE execution_pre_effect_manifest (
  id UUID PRIMARY KEY,
  execution_attempt_id UUID NOT NULL REFERENCES execution_attempt(id),
  fencing_token INTEGER NOT NULL CHECK (fencing_token > 0),
  account_id TEXT NOT NULL CHECK (btrim(account_id) <> ''),
  effect TEXT NOT NULL CHECK (effect IN (
    'place_open','place_close','cancel_order','replace_cancel'
  )),
  broker_observation_id UUID NOT NULL REFERENCES broker_observation(id),
  observation_generation BIGINT NOT NULL CHECK (observation_generation > 0),
  observation_manifest_digest BYTEA NOT NULL
    CHECK (octet_length(observation_manifest_digest)=32),
  target_broker_order_id TEXT,
  facts JSONB NOT NULL CHECK (jsonb_typeof(facts)='object'),
  facts_digest BYTEA NOT NULL CHECK (octet_length(facts_digest)=32),
  expires_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  CHECK (
    (effect IN ('place_open','place_close') AND target_broker_order_id IS NULL)
    OR
    (effect IN ('cancel_order','replace_cancel')
      AND btrim(target_broker_order_id) <> '')
  ),
  CHECK (expires_at > created_at),
  UNIQUE (id,execution_attempt_id,fencing_token)
);

CREATE INDEX execution_pre_effect_attempt
  ON execution_pre_effect_manifest (execution_attempt_id,fencing_token,created_at DESC);

-- One evidence binding per possible Provider call.  send_ordinal=0 is the
-- first call; send_ordinal=1 is the single same-reference replay allowed by
-- the live execution gate.
CREATE TABLE execution_pre_effect_binding (
  execution_attempt_id UUID NOT NULL REFERENCES execution_attempt(id),
  send_ordinal SMALLINT NOT NULL CHECK (send_ordinal IN (0,1)),
  fencing_token INTEGER NOT NULL CHECK (fencing_token > 0),
  manifest_id UUID UNIQUE NOT NULL,
  bound_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (execution_attempt_id,send_ordinal),
  FOREIGN KEY (manifest_id,execution_attempt_id,fencing_token)
    REFERENCES execution_pre_effect_manifest(id,execution_attempt_id,fencing_token)
);

CREATE TRIGGER execution_pre_effect_manifest_immutable
  BEFORE UPDATE OR DELETE ON execution_pre_effect_manifest
  FOR EACH ROW EXECUTE FUNCTION reject_broker_observation_mutation();
CREATE TRIGGER execution_pre_effect_binding_immutable
  BEFORE UPDATE OR DELETE ON execution_pre_effect_binding
  FOR EACH ROW EXECUTE FUNCTION reject_broker_observation_mutation();
