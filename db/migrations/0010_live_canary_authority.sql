-- Rows created before K0 were descriptive audit records, not runtime
-- authority.  authority_version is deliberately nullable so normal startup
-- cannot silently promote those rows; the deployment CLI must create the
-- first authoritative revision explicitly.
ALTER TABLE live_canary_revision
  ADD COLUMN authority_version SMALLINT,
  ADD COLUMN recorded_by TEXT,
  ADD COLUMN reason TEXT,
  ADD COLUMN change_class TEXT,
  ADD CONSTRAINT live_canary_authority_shape CHECK (
    (authority_version IS NULL AND recorded_by IS NULL AND reason IS NULL AND change_class IS NULL)
    OR
    (authority_version = 1
      AND char_length(btrim(recorded_by)) BETWEEN 1 AND 200
      AND char_length(btrim(reason)) BETWEEN 1 AND 1000
      AND change_class IN ('initial','tighten'))
  );

-- An authoritative row is an append-only authorization fact. A mutable row
-- would let a cap change retain its old revision ID and audit event, defeating
-- both CAS activation and trade-grant binding.
CREATE FUNCTION reject_live_canary_authority_mutation() RETURNS trigger AS $$
BEGIN
  IF OLD.authority_version IS NOT NULL THEN
    RAISE EXCEPTION 'authoritative live canary revisions are immutable';
  END IF;
  IF TG_OP = 'UPDATE' AND NEW.authority_version IS NOT NULL THEN
    RAISE EXCEPTION 'legacy live canary revisions cannot be promoted in place';
  END IF;
  IF TG_OP = 'DELETE' THEN
    RETURN OLD;
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER live_canary_authority_immutable
  BEFORE UPDATE OR DELETE ON live_canary_revision
  FOR EACH ROW EXECUTE FUNCTION reject_live_canary_authority_mutation();

ALTER TABLE trade_grant
  ADD COLUMN live_canary_revision_id BIGINT REFERENCES live_canary_revision(id);

CREATE INDEX trade_grant_live_canary_revision
  ON trade_grant (live_canary_revision_id)
  WHERE live_canary_revision_id IS NOT NULL;

CREATE INDEX live_canary_revision_event_lookup
  ON events ((payload->>'revision_id'))
  WHERE kind='live_canary_revision_recorded';
