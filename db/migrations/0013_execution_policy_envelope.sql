-- K1B-2: downstream execution facts carry the exact immutable policy binding
-- of their operation. Existing pre-K1 work remains explicitly unbound.
ALTER TABLE trade_grant
  ADD COLUMN kernel_policy_revision_id BIGINT REFERENCES kernel_policy_revision(id),
  ADD COLUMN kernel_policy_generation BIGINT,
  ADD COLUMN kernel_policy_digest BYTEA;

ALTER TABLE open_reservation
  ADD COLUMN kernel_policy_revision_id BIGINT REFERENCES kernel_policy_revision(id),
  ADD COLUMN kernel_policy_generation BIGINT,
  ADD COLUMN kernel_policy_digest BYTEA;

ALTER TABLE close_reservation
  ADD COLUMN kernel_policy_revision_id BIGINT REFERENCES kernel_policy_revision(id),
  ADD COLUMN kernel_policy_generation BIGINT,
  ADD COLUMN kernel_policy_digest BYTEA;

ALTER TABLE execution_attempt
  ADD COLUMN kernel_policy_revision_id BIGINT REFERENCES kernel_policy_revision(id),
  ADD COLUMN kernel_policy_generation BIGINT,
  ADD COLUMN kernel_policy_digest BYTEA,
  ADD COLUMN authorization_expires_at TIMESTAMPTZ,
  ADD COLUMN max_reprices INTEGER,
  ADD COLUMN reprice_interval_sec INTEGER,
  ADD COLUMN quote_max_age_sec INTEGER,
  ADD COLUMN lease_expires_at TIMESTAMPTZ;

ALTER TABLE orders
  ADD COLUMN kernel_policy_revision_id BIGINT REFERENCES kernel_policy_revision(id),
  ADD COLUMN kernel_policy_generation BIGINT,
  ADD COLUMN kernel_policy_digest BYTEA,
  ADD COLUMN authorization_expires_at TIMESTAMPTZ,
  ADD COLUMN approved_price_bound_micros BIGINT,
  ADD COLUMN max_reprices INTEGER,
  ADD COLUMN reprice_interval_sec INTEGER,
  ADD COLUMN quote_max_age_sec INTEGER,
  ADD COLUMN working_since TIMESTAMPTZ;

-- Bind all already-staged post-K1 facts. Rows whose operation predates K1
-- deliberately keep an all-NULL binding and envelope.
UPDATE trade_grant d SET
  kernel_policy_revision_id=o.kernel_policy_revision_id,
  kernel_policy_generation=o.kernel_policy_generation,
  kernel_policy_digest=o.kernel_policy_digest
FROM operations o WHERE o.id=d.operation_id;

UPDATE open_reservation d SET
  kernel_policy_revision_id=o.kernel_policy_revision_id,
  kernel_policy_generation=o.kernel_policy_generation,
  kernel_policy_digest=o.kernel_policy_digest
FROM operations o WHERE o.id=d.operation_id;

UPDATE close_reservation d SET
  kernel_policy_revision_id=o.kernel_policy_revision_id,
  kernel_policy_generation=o.kernel_policy_generation,
  kernel_policy_digest=o.kernel_policy_digest
FROM operations o WHERE o.id=d.operation_id;

UPDATE execution_attempt d SET
  kernel_policy_revision_id=o.kernel_policy_revision_id,
  kernel_policy_generation=o.kernel_policy_generation,
  kernel_policy_digest=o.kernel_policy_digest,
  authorization_expires_at=o.expires_at,
  max_reprices=(r.policy #>> '{execution_policy,max_reprices}')::integer,
  reprice_interval_sec=(r.policy #>> '{execution_policy,reprice_interval_sec}')::integer,
  quote_max_age_sec=(r.policy ->> 'quote_max_age_sec')::integer
FROM operations o
LEFT JOIN kernel_policy_revision r ON r.id=o.kernel_policy_revision_id
WHERE o.id=d.operation_id;

UPDATE orders d SET
  kernel_policy_revision_id=a.kernel_policy_revision_id,
  kernel_policy_generation=a.kernel_policy_generation,
  kernel_policy_digest=a.kernel_policy_digest,
  authorization_expires_at=a.authorization_expires_at,
  approved_price_bound_micros=CASE
    WHEN o.payload->>'action'='open'
      THEN ((o.payload->>'approved_price_cap')::numeric * 1000000)::bigint
    WHEN o.payload->>'action'='close' AND o.payload ? 'limit'
      THEN ((o.payload->>'limit')::numeric * 1000000)::bigint
    ELSE d.limit_micros
  END,
  max_reprices=a.max_reprices,
  reprice_interval_sec=a.reprice_interval_sec,
  quote_max_age_sec=a.quote_max_age_sec
FROM execution_attempt a
JOIN operations o ON o.id=a.operation_id
WHERE a.id=d.execution_attempt_id;

UPDATE orders SET working_since=updated_at
WHERE state IN ('submitted','partially_filled') AND working_since IS NULL;

-- A claim existing during migration is immediately recoverable. This is
-- safer than inventing a future lease from a process-local timeout.
UPDATE execution_attempt
SET lease_expires_at=clock_timestamp()
WHERE state='claimed' AND lease_expires_at IS NULL;

ALTER TABLE trade_grant ADD CONSTRAINT trade_grant_kernel_policy_complete CHECK (
  (kernel_policy_revision_id IS NULL AND kernel_policy_generation IS NULL AND kernel_policy_digest IS NULL)
  OR (kernel_policy_revision_id IS NOT NULL AND kernel_policy_generation > 0 AND octet_length(kernel_policy_digest)=32)
);
ALTER TABLE open_reservation ADD CONSTRAINT open_reservation_kernel_policy_complete CHECK (
  (kernel_policy_revision_id IS NULL AND kernel_policy_generation IS NULL AND kernel_policy_digest IS NULL)
  OR (kernel_policy_revision_id IS NOT NULL AND kernel_policy_generation > 0 AND octet_length(kernel_policy_digest)=32)
);
ALTER TABLE close_reservation ADD CONSTRAINT close_reservation_kernel_policy_complete CHECK (
  (kernel_policy_revision_id IS NULL AND kernel_policy_generation IS NULL AND kernel_policy_digest IS NULL)
  OR (kernel_policy_revision_id IS NOT NULL AND kernel_policy_generation > 0 AND octet_length(kernel_policy_digest)=32)
);
ALTER TABLE execution_attempt ADD CONSTRAINT execution_attempt_kernel_policy_complete CHECK (
  (kernel_policy_revision_id IS NULL AND kernel_policy_generation IS NULL AND kernel_policy_digest IS NULL
    AND authorization_expires_at IS NULL AND max_reprices IS NULL
    AND reprice_interval_sec IS NULL AND quote_max_age_sec IS NULL)
  OR (kernel_policy_revision_id IS NOT NULL AND kernel_policy_generation > 0
    AND octet_length(kernel_policy_digest)=32 AND authorization_expires_at IS NOT NULL
    AND max_reprices BETWEEN 0 AND 100 AND reprice_interval_sec BETWEEN 1 AND 86400
    AND quote_max_age_sec BETWEEN 1 AND 86400)
);
ALTER TABLE execution_attempt ADD CONSTRAINT execution_attempt_lease_shape CHECK (
  (lease_expires_at IS NULL OR claimed_at IS NOT NULL)
  AND (kernel_policy_revision_id IS NULL OR state <> 'claimed' OR lease_expires_at IS NOT NULL)
);
ALTER TABLE orders ADD CONSTRAINT orders_kernel_policy_complete CHECK (
  (kernel_policy_revision_id IS NULL AND kernel_policy_generation IS NULL AND kernel_policy_digest IS NULL
    AND authorization_expires_at IS NULL AND approved_price_bound_micros IS NULL
    AND max_reprices IS NULL AND reprice_interval_sec IS NULL AND quote_max_age_sec IS NULL)
  OR (kernel_policy_revision_id IS NOT NULL AND kernel_policy_generation > 0
    AND octet_length(kernel_policy_digest)=32 AND authorization_expires_at IS NOT NULL
    AND approved_price_bound_micros > 0 AND max_reprices BETWEEN 0 AND 100
    AND reprice_interval_sec BETWEEN 1 AND 86400 AND quote_max_age_sec BETWEEN 1 AND 86400)
);
ALTER TABLE orders ADD CONSTRAINT orders_working_since_shape CHECK (
  kernel_policy_revision_id IS NULL
  OR state NOT IN ('submitted','partially_filled')
  OR working_since IS NOT NULL
);

-- Once an operation is bound, its authorization evidence is append-only.
-- Lifecycle code may still advance status/verdict, but cannot rewrite what was
-- proposed, which policy authorized it, or its absolute deadline.
CREATE FUNCTION reject_bound_operation_authority_mutation() RETURNS trigger AS $$
BEGIN
  IF OLD.kernel_policy_revision_id IS NOT NULL AND (
    NEW.id IS DISTINCT FROM OLD.id
    OR NEW.ts IS DISTINCT FROM OLD.ts
    OR NEW.proposer IS DISTINCT FROM OLD.proposer
    OR NEW.class IS DISTINCT FROM OLD.class
    OR NEW.payload IS DISTINCT FROM OLD.payload
    OR NEW.kernel_policy_revision_id IS DISTINCT FROM OLD.kernel_policy_revision_id
    OR NEW.kernel_policy_generation IS DISTINCT FROM OLD.kernel_policy_generation
    OR NEW.kernel_policy_digest IS DISTINCT FROM OLD.kernel_policy_digest
    OR NEW.expires_at IS DISTINCT FROM OLD.expires_at
  ) THEN
    RAISE EXCEPTION 'bound operation authority is immutable';
  END IF;
  IF OLD.kernel_policy_revision_id IS NULL AND NEW.kernel_policy_revision_id IS NOT NULL THEN
    RAISE EXCEPTION 'legacy operation cannot be adopted into policy authority';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER bound_operation_authority_immutable
  BEFORE UPDATE ON operations
  FOR EACH ROW EXECUTE FUNCTION reject_bound_operation_authority_mutation();

CREATE FUNCTION require_operation_policy_binding() RETURNS trigger AS $$
DECLARE
  op_revision BIGINT;
  op_generation BIGINT;
  op_digest BYTEA;
BEGIN
  SELECT kernel_policy_revision_id,kernel_policy_generation,kernel_policy_digest
    INTO op_revision,op_generation,op_digest
    FROM operations WHERE id=NEW.operation_id;
  IF NOT FOUND THEN
    RETURN NEW;
  END IF;
  IF NEW.kernel_policy_revision_id IS DISTINCT FROM op_revision
     OR NEW.kernel_policy_generation IS DISTINCT FROM op_generation
     OR NEW.kernel_policy_digest IS DISTINCT FROM op_digest THEN
    RAISE EXCEPTION 'downstream policy binding differs from operation';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trade_grant_require_operation_policy
  BEFORE INSERT OR UPDATE OF kernel_policy_revision_id,kernel_policy_generation,kernel_policy_digest
  ON trade_grant FOR EACH ROW EXECUTE FUNCTION require_operation_policy_binding();
CREATE TRIGGER open_reservation_require_operation_policy
  BEFORE INSERT OR UPDATE OF kernel_policy_revision_id,kernel_policy_generation,kernel_policy_digest
  ON open_reservation FOR EACH ROW EXECUTE FUNCTION require_operation_policy_binding();
CREATE TRIGGER close_reservation_require_operation_policy
  BEFORE INSERT OR UPDATE OF kernel_policy_revision_id,kernel_policy_generation,kernel_policy_digest
  ON close_reservation FOR EACH ROW EXECUTE FUNCTION require_operation_policy_binding();
CREATE TRIGGER execution_attempt_require_operation_policy
  BEFORE INSERT OR UPDATE OF kernel_policy_revision_id,kernel_policy_generation,kernel_policy_digest
  ON execution_attempt FOR EACH ROW EXECUTE FUNCTION require_operation_policy_binding();
CREATE TRIGGER orders_require_operation_policy
  BEFORE INSERT OR UPDATE OF kernel_policy_revision_id,kernel_policy_generation,kernel_policy_digest
  ON orders FOR EACH ROW EXECUTE FUNCTION require_operation_policy_binding();

CREATE FUNCTION reject_execution_envelope_mutation() RETURNS trigger AS $$
BEGIN
  IF OLD.kernel_policy_revision_id IS NOT NULL AND (
    NEW.kernel_policy_revision_id IS DISTINCT FROM OLD.kernel_policy_revision_id
    OR NEW.kernel_policy_generation IS DISTINCT FROM OLD.kernel_policy_generation
    OR NEW.kernel_policy_digest IS DISTINCT FROM OLD.kernel_policy_digest
    OR NEW.authorization_expires_at IS DISTINCT FROM OLD.authorization_expires_at
    OR NEW.max_reprices IS DISTINCT FROM OLD.max_reprices
    OR NEW.reprice_interval_sec IS DISTINCT FROM OLD.reprice_interval_sec
    OR NEW.quote_max_age_sec IS DISTINCT FROM OLD.quote_max_age_sec
  ) THEN
    RAISE EXCEPTION 'execution attempt policy envelope is immutable';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER execution_attempt_envelope_immutable
  BEFORE UPDATE ON execution_attempt
  FOR EACH ROW EXECUTE FUNCTION reject_execution_envelope_mutation();

CREATE FUNCTION reject_order_envelope_mutation() RETURNS trigger AS $$
BEGIN
  IF OLD.kernel_policy_revision_id IS NOT NULL AND (
    NEW.kernel_policy_revision_id IS DISTINCT FROM OLD.kernel_policy_revision_id
    OR NEW.kernel_policy_generation IS DISTINCT FROM OLD.kernel_policy_generation
    OR NEW.kernel_policy_digest IS DISTINCT FROM OLD.kernel_policy_digest
    OR NEW.authorization_expires_at IS DISTINCT FROM OLD.authorization_expires_at
    OR NEW.approved_price_bound_micros IS DISTINCT FROM OLD.approved_price_bound_micros
    OR NEW.max_reprices IS DISTINCT FROM OLD.max_reprices
    OR NEW.reprice_interval_sec IS DISTINCT FROM OLD.reprice_interval_sec
    OR NEW.quote_max_age_sec IS DISTINCT FROM OLD.quote_max_age_sec
  ) THEN
    RAISE EXCEPTION 'order policy envelope is immutable';
  END IF;
  IF OLD.working_since IS NOT NULL AND NEW.working_since IS DISTINCT FROM OLD.working_since THEN
    RAISE EXCEPTION 'order working start is immutable';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER order_envelope_immutable
  BEFORE UPDATE ON orders
  FOR EACH ROW EXECUTE FUNCTION reject_order_envelope_mutation();

CREATE INDEX execution_attempt_expired_lease
  ON execution_attempt (lease_expires_at,id) WHERE state='claimed';
