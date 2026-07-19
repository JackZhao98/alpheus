-- K1B: every post-activation operation binds the exact Kernel policy that
-- classified it and freezes proposal expiry at database time.
ALTER TABLE operations
  ADD COLUMN kernel_policy_revision_id BIGINT REFERENCES kernel_policy_revision(id),
  ADD COLUMN kernel_policy_generation BIGINT,
  ADD COLUMN kernel_policy_digest BYTEA,
  ADD COLUMN expires_at TIMESTAMPTZ,
  ADD CONSTRAINT operations_kernel_policy_binding_complete CHECK (
    (kernel_policy_revision_id IS NULL AND kernel_policy_generation IS NULL
      AND kernel_policy_digest IS NULL AND expires_at IS NULL)
    OR
    (kernel_policy_revision_id IS NOT NULL AND kernel_policy_generation > 0
      AND octet_length(kernel_policy_digest) = 32 AND expires_at >= ts)
  );

CREATE INDEX operations_kernel_policy_revision
  ON operations (kernel_policy_revision_id)
  WHERE kernel_policy_revision_id IS NOT NULL;

-- Once a head exists, no newly inserted operation may bypass it. Historical
-- pre-K1 rows stay readable with NULL bindings and are never silently adopted.
CREATE FUNCTION require_current_operation_policy_binding() RETURNS trigger AS $$
DECLARE
  active_revision BIGINT;
  active_generation BIGINT;
  active_digest BYTEA;
BEGIN
  SELECT h.revision_id,h.generation,r.digest
    INTO active_revision,active_generation,active_digest
    FROM kernel_policy_head h
    JOIN kernel_policy_revision r ON r.id=h.revision_id
    WHERE h.singleton=true;

  IF NOT FOUND THEN
    IF NEW.kernel_policy_revision_id IS NOT NULL
       OR NEW.kernel_policy_generation IS NOT NULL
       OR NEW.kernel_policy_digest IS NOT NULL
       OR NEW.expires_at IS NOT NULL THEN
      RAISE EXCEPTION 'operation policy binding has no active authority';
    END IF;
    RETURN NEW;
  END IF;

  IF NEW.kernel_policy_revision_id IS NULL
     OR NEW.kernel_policy_generation IS NULL
     OR NEW.kernel_policy_digest IS NULL
     OR NEW.expires_at IS NULL THEN
    RAISE EXCEPTION 'active kernel policy requires an operation binding';
  END IF;
  IF NEW.kernel_policy_revision_id <> active_revision
     OR NEW.kernel_policy_generation <> active_generation
     OR NEW.kernel_policy_digest <> active_digest THEN
    RAISE EXCEPTION 'operation policy binding is not the active generation';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER operations_require_current_policy
  BEFORE INSERT ON operations
  FOR EACH ROW EXECUTE FUNCTION require_current_operation_policy_binding();
