-- K1C: immutable, typed evidence that a Live market day reached a clean,
-- broker-reconciled terminal observation. day_open alone is never evidence.
ALTER TABLE live_canary_revision
  DROP CONSTRAINT live_canary_authority_shape,
  ADD COLUMN required_attestations SMALLINT NOT NULL DEFAULT 0
    CHECK (required_attestations BETWEEN 0 AND 366),
  ADD CONSTRAINT live_canary_authority_shape CHECK (
    (authority_version IS NULL
      AND recorded_by IS NULL AND reason IS NULL AND change_class IS NULL
      AND required_attestations = 0)
    OR
    (authority_version = 1
      AND char_length(btrim(recorded_by)) BETWEEN 1 AND 200
      AND char_length(btrim(reason)) BETWEEN 1 AND 1000
      AND change_class IN ('initial','tighten')
      AND required_attestations = 0)
    OR
    (authority_version = 2
      AND char_length(btrim(recorded_by)) BETWEEN 1 AND 200
      AND char_length(btrim(reason)) BETWEEN 1 AND 1000
      AND (
        (change_class IN ('initial','tighten') AND required_attestations = 0)
        OR (change_class = 'widen' AND required_attestations > 0)
      ))
  );

CREATE TABLE live_canary_day_attestation (
  id BIGSERIAL PRIMARY KEY,
  account_id TEXT NOT NULL CHECK (char_length(btrim(account_id)) BETWEEN 1 AND 200),
  market_day DATE NOT NULL,
  live_canary_revision_id BIGINT NOT NULL REFERENCES live_canary_revision(id),
  kernel_policy_revision_id BIGINT NOT NULL REFERENCES kernel_policy_revision(id),
  kernel_policy_generation BIGINT NOT NULL CHECK (kernel_policy_generation > 0),
  kernel_policy_digest BYTEA NOT NULL CHECK (octet_length(kernel_policy_digest) = 32),
  day_open_equity_micros BIGINT NOT NULL,
  live_grant_count INTEGER NOT NULL CHECK (live_grant_count > 0),
  authorized_risk_micros BIGINT NOT NULL CHECK (authorized_risk_micros > 0),
  local_realized_pnl_micros BIGINT NOT NULL,
  provider_realized_pnl_micros BIGINT NOT NULL,
  pnl_difference_micros BIGINT NOT NULL CHECK (pnl_difference_micros >= 0),
  pnl_tolerance_micros BIGINT NOT NULL CHECK (pnl_tolerance_micros >= 0),
  pnl_observed_at TIMESTAMPTZ NOT NULL,
  broker_observation_id UUID NOT NULL REFERENCES broker_observation(id),
  broker_observation_generation BIGINT NOT NULL CHECK (broker_observation_generation > 0),
  broker_observation_completed_at TIMESTAMPTZ NOT NULL,
  broker_reconciled_at TIMESTAMPTZ NOT NULL,
  broker_local_state_generation BIGINT NOT NULL CHECK (broker_local_state_generation >= 0),
  attested_by TEXT NOT NULL CHECK (char_length(btrim(attested_by)) BETWEEN 1 AND 200),
  reason TEXT NOT NULL CHECK (char_length(btrim(reason)) BETWEEN 1 AND 1000),
  attested_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  UNIQUE (account_id, market_day),
  CHECK (pnl_difference_micros <= pnl_tolerance_micros),
  CHECK (pnl_observed_at <= attested_at),
  CHECK (broker_observation_completed_at <= broker_reconciled_at),
  CHECK (broker_reconciled_at <= attested_at)
);

CREATE INDEX live_canary_day_attestation_recent
  ON live_canary_day_attestation (account_id, market_day DESC, id DESC);
CREATE INDEX live_canary_day_attestation_event_lookup
  ON events ((payload->>'attestation_id'))
  WHERE kind='live_canary_day_attested';

CREATE TABLE live_canary_widening_evidence (
  revision_id BIGINT NOT NULL REFERENCES live_canary_revision(id),
  ordinal SMALLINT NOT NULL CHECK (ordinal > 0),
  attestation_id BIGINT NOT NULL REFERENCES live_canary_day_attestation(id),
  PRIMARY KEY (revision_id, ordinal),
  UNIQUE (revision_id, attestation_id)
);

CREATE FUNCTION reject_live_canary_evidence_mutation() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'live canary evidence is immutable';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER live_canary_day_attestation_immutable
  BEFORE UPDATE OR DELETE ON live_canary_day_attestation
  FOR EACH ROW EXECUTE FUNCTION reject_live_canary_evidence_mutation();
CREATE TRIGGER live_canary_widening_evidence_immutable
  BEFORE UPDATE OR DELETE ON live_canary_widening_evidence
  FOR EACH ROW EXECUTE FUNCTION reject_live_canary_evidence_mutation();
