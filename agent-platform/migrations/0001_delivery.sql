SET ROLE alpheus_agent_migrator;

CREATE TABLE agent_control.delivery_policy (
    singleton BOOLEAN PRIMARY KEY DEFAULT true CHECK (singleton),
    max_attempts INTEGER NOT NULL CHECK (max_attempts BETWEEN 1 AND 10000),
    max_quarantine_rows INTEGER NOT NULL CHECK (max_quarantine_rows BETWEEN 1 AND 1000000),
    max_claim_batch INTEGER NOT NULL CHECK (max_claim_batch BETWEEN 1 AND 10000),
    max_lease_seconds INTEGER NOT NULL CHECK (max_lease_seconds BETWEEN 1 AND 86400),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_by TEXT NOT NULL CHECK (updated_by <> '' AND updated_by !~ '[[:space:][:cntrl:]]')
);

INSERT INTO agent_control.delivery_policy (
    singleton, max_attempts, max_quarantine_rows, max_claim_batch, max_lease_seconds, updated_by
) VALUES (true, 50, 10000, 100, 300, 'bootstrap');

CREATE TABLE agent_control.delivery_policy_event (
    event_id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    previous_policy JSONB,
    new_policy JSONB NOT NULL,
    changed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    changed_by TEXT NOT NULL CHECK (changed_by <> '' AND changed_by !~ '[[:space:][:cntrl:]]')
);

INSERT INTO agent_control.delivery_policy_event (previous_policy, new_policy, changed_by)
SELECT NULL, to_jsonb(policy), 'bootstrap'
FROM agent_control.delivery_policy AS policy
WHERE singleton;

CREATE TABLE agent_control.delivery_outbox (
    event_id TEXT NOT NULL CHECK (event_id <> '' AND event_id !~ '[[:space:][:cntrl:]]'),
    destination TEXT NOT NULL CHECK (destination <> '' AND destination !~ '[[:space:][:cntrl:]]'),
    schema_revision SMALLINT NOT NULL CHECK (schema_revision = 1),
    source_owner TEXT NOT NULL CHECK (source_owner IN (
        'agent_control', 'blob', 'delegation', 'grace', 'kernel',
        'platform_governance', 'research_gateway', 'worker'
    )),
    owner_sequence BIGINT NOT NULL CHECK (owner_sequence > 0),
    event_type TEXT NOT NULL CHECK (event_type ~ '^[a-z][a-z0-9_]{0,63}$'),
    event_digest CHAR(64) NOT NULL CHECK (event_digest ~ '^[0-9a-f]{64}$'),
    causation_id TEXT NOT NULL CHECK (causation_id <> '' AND causation_id !~ '[[:space:][:cntrl:]]'),
    correlation_id TEXT NOT NULL CHECK (correlation_id <> '' AND correlation_id !~ '[[:space:][:cntrl:]]'),
    event_payload JSONB NOT NULL CHECK (
        jsonb_typeof(event_payload) = 'object' AND octet_length(event_payload::text) <= 1048576
    ),
    committed_at TIMESTAMPTZ NOT NULL,
    available_at TIMESTAMPTZ NOT NULL,
    state TEXT NOT NULL DEFAULT 'available' CHECK (state IN ('available', 'leased', 'delivered', 'quarantined')),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    replay_generation INTEGER NOT NULL DEFAULT 0 CHECK (replay_generation >= 0),
    lease_dispatcher_id TEXT,
    lease_token UUID,
    lease_claimed_at TIMESTAMPTZ,
    lease_expires_at TIMESTAMPTZ,
    delivered_at TIMESTAMPTZ,
    quarantined_at TIMESTAMPTZ,
    PRIMARY KEY (event_id, destination),
    UNIQUE (source_owner, owner_sequence, destination),
    CHECK (available_at >= committed_at),
    CHECK (
        (state = 'available' AND attempt_count = 0 AND lease_token IS NULL AND lease_dispatcher_id IS NULL AND
            lease_claimed_at IS NULL AND lease_expires_at IS NULL AND delivered_at IS NULL AND quarantined_at IS NULL)
        OR
        (state = 'leased' AND attempt_count > 0 AND lease_token IS NOT NULL AND lease_dispatcher_id IS NOT NULL AND
            lease_claimed_at IS NOT NULL AND lease_expires_at > lease_claimed_at AND delivered_at IS NULL AND quarantined_at IS NULL)
        OR
        (state = 'delivered' AND attempt_count > 0 AND lease_token IS NULL AND lease_dispatcher_id IS NULL AND
            lease_claimed_at IS NULL AND lease_expires_at IS NULL AND delivered_at >= available_at AND quarantined_at IS NULL)
        OR
        (state = 'quarantined' AND attempt_count > 0 AND lease_token IS NULL AND lease_dispatcher_id IS NULL AND
            lease_claimed_at IS NULL AND lease_expires_at IS NULL AND delivered_at IS NULL AND quarantined_at >= available_at)
    )
);

CREATE INDEX delivery_outbox_claim_idx ON agent_control.delivery_outbox (
    destination, state, available_at, lease_expires_at, committed_at
) WHERE state IN ('available', 'leased');

CREATE TABLE agent_control.delivery_inbox (
    consumer_id TEXT NOT NULL CHECK (consumer_id <> '' AND consumer_id !~ '[[:space:][:cntrl:]]'),
    event_id TEXT NOT NULL CHECK (event_id <> '' AND event_id !~ '[[:space:][:cntrl:]]'),
    event_digest CHAR(64) NOT NULL CHECK (event_digest ~ '^[0-9a-f]{64}$'),
    source_owner TEXT NOT NULL CHECK (source_owner IN (
        'agent_control', 'blob', 'delegation', 'grace', 'kernel',
        'platform_governance', 'research_gateway', 'worker'
    )),
    owner_sequence BIGINT NOT NULL CHECK (owner_sequence > 0),
    effect_digest CHAR(64) NOT NULL CHECK (effect_digest ~ '^[0-9a-f]{64}$'),
    applied_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (consumer_id, event_id)
);

CREATE TABLE agent_control.delivery_quarantine (
    consumer_id TEXT NOT NULL CHECK (consumer_id <> '' AND consumer_id !~ '[[:space:][:cntrl:]]'),
    event_id TEXT NOT NULL CHECK (event_id <> '' AND event_id !~ '[[:space:][:cntrl:]]'),
    event_digest CHAR(64) NOT NULL CHECK (event_digest ~ '^[0-9a-f]{64}$'),
    source_owner TEXT NOT NULL CHECK (source_owner IN (
        'agent_control', 'blob', 'delegation', 'grace', 'kernel',
        'platform_governance', 'research_gateway', 'worker'
    )),
    owner_sequence BIGINT NOT NULL CHECK (owner_sequence > 0),
    reason_code TEXT NOT NULL CHECK (reason_code ~ '^[a-z][a-z0-9_]{0,63}$'),
    attempt_count INTEGER NOT NULL CHECK (attempt_count > 0),
    state TEXT NOT NULL DEFAULT 'active' CHECK (state IN ('active', 'replay_requested', 'resolved')),
    first_failed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    last_failed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    replay_generation INTEGER NOT NULL DEFAULT 0 CHECK (replay_generation >= 0),
    replay_reason TEXT,
    replay_requested_at TIMESTAMPTZ,
    resolved_at TIMESTAMPTZ,
    PRIMARY KEY (consumer_id, event_id),
    CHECK (last_failed_at >= first_failed_at),
    CHECK (
        (state = 'active' AND replay_reason IS NULL AND replay_requested_at IS NULL AND resolved_at IS NULL)
        OR
        (state = 'replay_requested' AND replay_generation > 0 AND replay_reason IS NOT NULL AND replay_requested_at IS NOT NULL AND resolved_at IS NULL)
        OR
        (state = 'resolved' AND replay_generation > 0 AND replay_reason IS NOT NULL AND replay_requested_at IS NOT NULL AND resolved_at IS NOT NULL)
    )
);

CREATE VIEW agent_control.delivery_health AS
SELECT
    destination,
    count(*) FILTER (WHERE state = 'available') AS available_count,
    count(*) FILTER (WHERE state = 'leased') AS leased_count,
    count(*) FILTER (WHERE state = 'delivered') AS delivered_count,
    count(*) FILTER (WHERE state = 'quarantined') AS quarantined_count,
    min(available_at) FILTER (WHERE state IN ('available', 'leased')) AS oldest_pending_at,
    max(attempt_count) FILTER (WHERE state IN ('available', 'leased', 'quarantined')) AS max_attempt_count
FROM agent_control.delivery_outbox
GROUP BY destination;

CREATE FUNCTION agent_control.update_delivery_policy(
    p_expected_updated_at TIMESTAMPTZ,
    p_max_attempts INTEGER,
    p_max_quarantine_rows INTEGER,
    p_max_claim_batch INTEGER,
    p_max_lease_seconds INTEGER,
    p_actor TEXT
) RETURNS TIMESTAMPTZ
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, agent_control
AS $$
DECLARE
    previous agent_control.delivery_policy%ROWTYPE;
    changed_at TIMESTAMPTZ;
BEGIN
    IF p_actor IS NULL OR p_actor = '' OR p_actor ~ '[[:space:][:cntrl:]]' THEN
        RAISE EXCEPTION USING ERRCODE = '22023', MESSAGE = 'invalid policy actor';
    END IF;
    SELECT * INTO STRICT previous
    FROM agent_control.delivery_policy
    WHERE singleton
    FOR UPDATE;
    IF previous.updated_at <> p_expected_updated_at THEN
        RAISE EXCEPTION USING ERRCODE = '40001', MESSAGE = 'stale delivery policy';
    END IF;
    changed_at := greatest(clock_timestamp(), previous.updated_at + interval '1 microsecond');
    UPDATE agent_control.delivery_policy
    SET max_attempts = p_max_attempts,
        max_quarantine_rows = p_max_quarantine_rows,
        max_claim_batch = p_max_claim_batch,
        max_lease_seconds = p_max_lease_seconds,
        updated_at = changed_at,
        updated_by = p_actor
    WHERE singleton;
    INSERT INTO agent_control.delivery_policy_event (previous_policy, new_policy, changed_at, changed_by)
    SELECT to_jsonb(previous), to_jsonb(policy), changed_at, p_actor
    FROM agent_control.delivery_policy AS policy
    WHERE singleton;
    RETURN changed_at;
END
$$;

CREATE FUNCTION agent_control.enqueue_outbox(
    p_event_id TEXT,
    p_destination TEXT,
    p_source_owner TEXT,
    p_owner_sequence BIGINT,
    p_event_type TEXT,
    p_event_digest TEXT,
    p_causation_id TEXT,
    p_correlation_id TEXT,
    p_event_payload JSONB,
    p_committed_at TIMESTAMPTZ,
    p_available_at TIMESTAMPTZ
) RETURNS BOOLEAN
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, agent_control
AS $$
DECLARE
    inserted BOOLEAN;
    existing agent_control.delivery_outbox%ROWTYPE;
BEGIN
    -- This app-facing function is owned by agent_control. Other owners must
    -- expose their own owner-pinned wrapper instead of borrowing this grant.
    IF p_source_owner <> 'agent_control' THEN
        RAISE EXCEPTION USING ERRCODE = '22023', MESSAGE = 'invalid outbox source owner';
    END IF;
    IF p_committed_at > clock_timestamp() THEN
        RAISE EXCEPTION USING ERRCODE = '22007', MESSAGE = 'future outbox commit time';
    END IF;
    INSERT INTO agent_control.delivery_outbox (
        event_id, destination, schema_revision, source_owner, owner_sequence,
        event_type, event_digest, causation_id, correlation_id, event_payload,
        committed_at, available_at
    ) VALUES (
        p_event_id, p_destination, 1, p_source_owner, p_owner_sequence,
        p_event_type, p_event_digest, p_causation_id, p_correlation_id, p_event_payload,
        p_committed_at, p_available_at
    )
    ON CONFLICT (event_id, destination) DO NOTHING
    RETURNING true INTO inserted;

    IF inserted THEN
        RETURN true;
    END IF;

    SELECT * INTO STRICT existing
    FROM agent_control.delivery_outbox
    WHERE event_id = p_event_id AND destination = p_destination;

    IF existing.source_owner = p_source_owner
       AND existing.owner_sequence = p_owner_sequence
       AND existing.event_type = p_event_type
       AND existing.event_digest = p_event_digest
       AND existing.causation_id = p_causation_id
       AND existing.correlation_id = p_correlation_id
       AND existing.event_payload = p_event_payload
       AND existing.committed_at = p_committed_at
       AND existing.available_at = p_available_at THEN
        RETURN false;
    END IF;
    RAISE EXCEPTION USING ERRCODE = '23505', MESSAGE = 'outbox identity conflict';
END
$$;

CREATE FUNCTION agent_control.claim_outbox(
    p_dispatcher_id TEXT,
    p_destination TEXT,
    p_limit INTEGER,
    p_lease_seconds INTEGER
) RETURNS TABLE (
    event_id TEXT,
    destination TEXT,
    event_digest TEXT,
    event_payload JSONB,
    lease_token UUID,
    attempt_count INTEGER,
    replay_generation INTEGER
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, agent_control
AS $$
DECLARE
    policy agent_control.delivery_policy%ROWTYPE;
    exhausted agent_control.delivery_outbox%ROWTYPE;
    quarantine_changed INTEGER;
BEGIN
    SELECT * INTO STRICT policy FROM agent_control.delivery_policy WHERE singleton;
    IF p_dispatcher_id IS NULL OR p_dispatcher_id = '' OR p_dispatcher_id ~ '[[:space:][:cntrl:]]'
       OR p_destination IS NULL OR p_destination = '' OR p_destination ~ '[[:space:][:cntrl:]]'
       OR p_limit < 1 OR p_limit > policy.max_claim_batch
       OR p_lease_seconds < 1 OR p_lease_seconds > policy.max_lease_seconds THEN
        RAISE EXCEPTION USING ERRCODE = '22023', MESSAGE = 'invalid delivery claim';
    END IF;

    FOR exhausted IN
        SELECT queued.*
        FROM agent_control.delivery_outbox AS queued
        WHERE queued.destination = p_destination
          AND queued.state = 'leased'
          AND queued.lease_expires_at <= clock_timestamp()
          AND queued.attempt_count >= policy.max_attempts
        ORDER BY queued.committed_at, queued.event_id
        FOR UPDATE SKIP LOCKED
        LIMIT p_limit
    LOOP
        PERFORM 1 FROM agent_control.delivery_policy WHERE singleton FOR UPDATE;
        IF NOT EXISTS (
            SELECT 1 FROM agent_control.delivery_quarantine AS quarantine
            WHERE quarantine.consumer_id = exhausted.destination
              AND quarantine.event_id = exhausted.event_id
              AND quarantine.state <> 'resolved'
        ) AND (
            SELECT count(*) FROM agent_control.delivery_quarantine AS quarantine
            WHERE quarantine.state <> 'resolved'
        ) >= policy.max_quarantine_rows THEN
            RAISE EXCEPTION USING ERRCODE = '54000', MESSAGE = 'delivery quarantine capacity reached';
        END IF;
        UPDATE agent_control.delivery_outbox AS target
        SET state = 'quarantined', quarantined_at = clock_timestamp(),
            lease_dispatcher_id = NULL, lease_token = NULL,
            lease_claimed_at = NULL, lease_expires_at = NULL
        WHERE target.event_id = exhausted.event_id AND target.destination = exhausted.destination;
        INSERT INTO agent_control.delivery_quarantine (
            consumer_id, event_id, event_digest, source_owner, owner_sequence,
            reason_code, attempt_count, replay_generation
        ) VALUES (
            exhausted.destination, exhausted.event_id, exhausted.event_digest,
            exhausted.source_owner, exhausted.owner_sequence,
            'delivery_attempts_exhausted', exhausted.attempt_count, exhausted.replay_generation
        )
        ON CONFLICT ON CONSTRAINT delivery_quarantine_pkey DO UPDATE
        SET state = 'active',
            last_failed_at = clock_timestamp(),
            reason_code = EXCLUDED.reason_code,
            attempt_count = EXCLUDED.attempt_count,
            replay_generation = EXCLUDED.replay_generation,
            replay_reason = NULL,
            replay_requested_at = NULL,
            resolved_at = NULL
        WHERE agent_control.delivery_quarantine.event_digest = EXCLUDED.event_digest
          AND agent_control.delivery_quarantine.source_owner = EXCLUDED.source_owner
          AND agent_control.delivery_quarantine.owner_sequence = EXCLUDED.owner_sequence;
        GET DIAGNOSTICS quarantine_changed = ROW_COUNT;
        IF quarantine_changed = 0 THEN
            RAISE EXCEPTION USING ERRCODE = '23505', MESSAGE = 'quarantine identity conflict';
        END IF;
    END LOOP;

    RETURN QUERY
    WITH candidates AS (
        SELECT queued.event_id, queued.destination
        FROM agent_control.delivery_outbox AS queued
        WHERE queued.destination = p_destination
          AND queued.available_at <= clock_timestamp()
          AND queued.attempt_count < policy.max_attempts
          AND (
              queued.state = 'available'
              OR (queued.state = 'leased' AND queued.lease_expires_at <= clock_timestamp())
          )
        ORDER BY queued.committed_at, queued.event_id
        FOR UPDATE SKIP LOCKED
        LIMIT p_limit
    )
    UPDATE agent_control.delivery_outbox AS queued
    SET state = 'leased',
        attempt_count = queued.attempt_count + 1,
        lease_dispatcher_id = p_dispatcher_id,
        lease_token = gen_random_uuid(),
        lease_claimed_at = clock_timestamp(),
        lease_expires_at = clock_timestamp() + make_interval(secs => p_lease_seconds),
        delivered_at = NULL,
        quarantined_at = NULL
    FROM candidates
    WHERE queued.event_id = candidates.event_id AND queued.destination = candidates.destination
    RETURNING queued.event_id, queued.destination, queued.event_digest::TEXT,
        queued.event_payload, queued.lease_token, queued.attempt_count, queued.replay_generation;
END
$$;

CREATE FUNCTION agent_control.complete_outbox(
    p_event_id TEXT,
    p_destination TEXT,
    p_lease_token UUID
) RETURNS BOOLEAN
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, agent_control
AS $$
DECLARE
    changed INTEGER;
    generation INTEGER;
BEGIN
    UPDATE agent_control.delivery_outbox
    SET state = 'delivered', delivered_at = clock_timestamp(),
        lease_dispatcher_id = NULL, lease_token = NULL, lease_claimed_at = NULL, lease_expires_at = NULL
    WHERE event_id = p_event_id AND destination = p_destination AND state = 'leased'
      AND lease_token = p_lease_token AND lease_expires_at > clock_timestamp()
    RETURNING replay_generation INTO generation;
    GET DIAGNOSTICS changed = ROW_COUNT;
    IF changed = 0 THEN
        RETURN false;
    END IF;
    IF generation > 0 THEN
        UPDATE agent_control.delivery_quarantine
        SET state = 'resolved', resolved_at = clock_timestamp()
        WHERE consumer_id = p_destination AND event_id = p_event_id
          AND state = 'replay_requested' AND replay_generation = generation;
    END IF;
    RETURN true;
END
$$;

CREATE FUNCTION agent_control.quarantine_outbox(
    p_event_id TEXT,
    p_destination TEXT,
    p_lease_token UUID,
    p_reason_code TEXT
) RETURNS BOOLEAN
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, agent_control
AS $$
DECLARE
    policy agent_control.delivery_policy%ROWTYPE;
    queued agent_control.delivery_outbox%ROWTYPE;
    quarantine_changed INTEGER;
BEGIN
    IF p_reason_code IS NULL OR p_reason_code !~ '^[a-z][a-z0-9_]{0,63}$' THEN
        RAISE EXCEPTION USING ERRCODE = '22023', MESSAGE = 'invalid quarantine reason';
    END IF;
    SELECT * INTO queued
    FROM agent_control.delivery_outbox
    WHERE event_id = p_event_id AND destination = p_destination AND state = 'leased'
      AND lease_token = p_lease_token AND lease_expires_at > clock_timestamp()
    FOR UPDATE;
    IF NOT FOUND THEN
        RETURN false;
    END IF;
    SELECT * INTO STRICT policy FROM agent_control.delivery_policy WHERE singleton FOR UPDATE;
    IF NOT EXISTS (
        SELECT 1 FROM agent_control.delivery_quarantine
        WHERE consumer_id = queued.destination AND event_id = queued.event_id AND state <> 'resolved'
    ) AND (SELECT count(*) FROM agent_control.delivery_quarantine WHERE state <> 'resolved') >= policy.max_quarantine_rows THEN
        RAISE EXCEPTION USING ERRCODE = '54000', MESSAGE = 'delivery quarantine capacity reached';
    END IF;
    UPDATE agent_control.delivery_outbox
    SET state = 'quarantined', quarantined_at = clock_timestamp(),
        lease_dispatcher_id = NULL, lease_token = NULL, lease_claimed_at = NULL, lease_expires_at = NULL
    WHERE event_id = p_event_id AND destination = p_destination;
    INSERT INTO agent_control.delivery_quarantine (
        consumer_id, event_id, event_digest, source_owner, owner_sequence,
        reason_code, attempt_count
    ) VALUES (
        queued.destination, queued.event_id, queued.event_digest, queued.source_owner,
        queued.owner_sequence, p_reason_code, queued.attempt_count
    )
    ON CONFLICT ON CONSTRAINT delivery_quarantine_pkey DO UPDATE
    SET state = 'active',
        last_failed_at = clock_timestamp(),
        reason_code = EXCLUDED.reason_code,
        attempt_count = EXCLUDED.attempt_count,
        replay_reason = NULL,
        replay_requested_at = NULL,
        resolved_at = NULL
    WHERE agent_control.delivery_quarantine.event_digest = EXCLUDED.event_digest
      AND agent_control.delivery_quarantine.source_owner = EXCLUDED.source_owner
      AND agent_control.delivery_quarantine.owner_sequence = EXCLUDED.owner_sequence;
    GET DIAGNOSTICS quarantine_changed = ROW_COUNT;
    IF quarantine_changed = 0 THEN
        RAISE EXCEPTION USING ERRCODE = '23505', MESSAGE = 'quarantine identity conflict';
    END IF;
    RETURN true;
END
$$;

CREATE FUNCTION agent_control.record_inbox(
    p_consumer_id TEXT,
    p_event_id TEXT,
    p_event_digest TEXT,
    p_source_owner TEXT,
    p_owner_sequence BIGINT,
    p_effect_digest TEXT
) RETURNS BOOLEAN
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, agent_control
AS $$
DECLARE
    inserted BOOLEAN;
    existing agent_control.delivery_inbox%ROWTYPE;
BEGIN
    INSERT INTO agent_control.delivery_inbox (
        consumer_id, event_id, event_digest, source_owner, owner_sequence, effect_digest
    ) VALUES (
        p_consumer_id, p_event_id, p_event_digest, p_source_owner, p_owner_sequence, p_effect_digest
    )
    ON CONFLICT ON CONSTRAINT delivery_inbox_pkey DO NOTHING
    RETURNING true INTO inserted;
    IF inserted THEN
        RETURN true;
    END IF;
    SELECT * INTO STRICT existing
    FROM agent_control.delivery_inbox
    WHERE consumer_id = p_consumer_id AND event_id = p_event_id;
    IF existing.event_digest = p_event_digest
       AND existing.source_owner = p_source_owner
       AND existing.owner_sequence = p_owner_sequence
       AND existing.effect_digest = p_effect_digest THEN
        RETURN false;
    END IF;
    RAISE EXCEPTION USING ERRCODE = '23505', MESSAGE = 'inbox identity conflict';
END
$$;

CREATE FUNCTION agent_control.request_outbox_replay(
    p_event_id TEXT,
    p_destination TEXT,
    p_expected_generation INTEGER,
    p_reason TEXT
) RETURNS INTEGER
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, agent_control
AS $$
DECLARE
    next_generation INTEGER;
BEGIN
    IF p_reason IS NULL OR length(btrim(p_reason)) = 0 OR length(p_reason) > 500 THEN
        RAISE EXCEPTION USING ERRCODE = '22023', MESSAGE = 'invalid replay reason';
    END IF;
    PERFORM 1
    FROM agent_control.delivery_outbox
    WHERE event_id = p_event_id AND destination = p_destination AND state = 'quarantined'
    FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION USING ERRCODE = 'P0002', MESSAGE = 'quarantined outbox record missing';
    END IF;
    UPDATE agent_control.delivery_quarantine
    SET state = 'replay_requested', replay_generation = replay_generation + 1,
        replay_reason = p_reason, replay_requested_at = clock_timestamp()
    WHERE consumer_id = p_destination AND event_id = p_event_id AND state = 'active'
      AND replay_generation = p_expected_generation
    RETURNING replay_generation INTO next_generation;
    IF next_generation IS NULL THEN
        RAISE EXCEPTION USING ERRCODE = '40001', MESSAGE = 'stale quarantine generation';
    END IF;
    UPDATE agent_control.delivery_outbox
    SET state = 'available', attempt_count = 0, replay_generation = next_generation,
        available_at = clock_timestamp(), quarantined_at = NULL
    WHERE event_id = p_event_id AND destination = p_destination AND state = 'quarantined';
    RETURN next_generation;
END
$$;

RESET ROLE;
