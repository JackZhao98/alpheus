SET ROLE alpheus_agent_migrator;

CREATE FUNCTION platform_governance.reject_immutable_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION USING ERRCODE = '55000', MESSAGE = 'immutable governance record';
END
$$;

CREATE TABLE platform_governance.platform_mode_revision (
    revision_id UUID PRIMARY KEY,
    schema_revision SMALLINT NOT NULL CHECK (schema_revision = 1),
    generation BIGINT NOT NULL UNIQUE CHECK (generation > 0),
    mode TEXT NOT NULL CHECK (mode IN ('disabled', 'read_only', 'shadow', 'live_confirmed', 'live_autonomous')),
    revision_digest CHAR(64) NOT NULL UNIQUE CHECK (revision_digest ~ '^[0-9a-f]{64}$'),
    author_principal TEXT NOT NULL CHECK (author_principal <> '' AND author_principal !~ '[[:space:][:cntrl:]]'),
    author_kind TEXT NOT NULL CHECK (author_kind IN ('user', 'workload')),
    author_audience TEXT NOT NULL CHECK (author_audience = 'activator'),
    reason_code TEXT NOT NULL CHECK (reason_code ~ '^[a-z][a-z0-9_]{0,63}$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE platform_governance.effect_class_revision (
    revision_id UUID PRIMARY KEY,
    schema_revision SMALLINT NOT NULL CHECK (schema_revision = 1),
    generation BIGINT NOT NULL CHECK (generation > 0),
    effect_class TEXT NOT NULL CHECK (effect_class IN ('external_read', 'operation_intent', 'exact_confirmation', 'broker_mutation')),
    state TEXT NOT NULL CHECK (state IN ('enabled', 'halted')),
    revision_digest CHAR(64) NOT NULL UNIQUE CHECK (revision_digest ~ '^[0-9a-f]{64}$'),
    author_principal TEXT NOT NULL CHECK (author_principal <> '' AND author_principal !~ '[[:space:][:cntrl:]]'),
    author_kind TEXT NOT NULL CHECK (author_kind IN ('user', 'workload')),
    author_audience TEXT NOT NULL CHECK (author_audience = 'activator'),
    reason_code TEXT NOT NULL CHECK (reason_code ~ '^[a-z][a-z0-9_]{0,63}$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (effect_class, generation),
    UNIQUE (effect_class, generation, revision_id)
);

CREATE TABLE platform_governance.kill_switch_revision (
    revision_id UUID PRIMARY KEY,
    schema_revision SMALLINT NOT NULL CHECK (schema_revision = 1),
    generation BIGINT NOT NULL CHECK (generation > 0),
    switch_id TEXT NOT NULL CHECK (switch_id IN (
        'agent_operation_emission', 'agent_release_activation', 'autonomous_live',
        'capability_external_execution', 'delegation_activation', 'exact_confirmation_live',
        'grace_publication', 'product_crypto', 'product_equity', 'product_option',
        'shadow_integration', 'strategy_activation'
    )),
    state TEXT NOT NULL CHECK (state IN ('enabled', 'halted')),
    revision_digest CHAR(64) NOT NULL UNIQUE CHECK (revision_digest ~ '^[0-9a-f]{64}$'),
    author_principal TEXT NOT NULL CHECK (author_principal <> '' AND author_principal !~ '[[:space:][:cntrl:]]'),
    author_kind TEXT NOT NULL CHECK (author_kind IN ('user', 'workload')),
    author_audience TEXT NOT NULL CHECK (author_audience = 'activator'),
    reason_code TEXT NOT NULL CHECK (reason_code ~ '^[a-z][a-z0-9_]{0,63}$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (switch_id, generation),
    UNIQUE (switch_id, generation, revision_id)
);

CREATE TABLE platform_governance.activation_receipt (
    receipt_id UUID PRIMARY KEY,
    schema_revision SMALLINT NOT NULL CHECK (schema_revision = 1),
    receipt_digest CHAR(64) NOT NULL UNIQUE CHECK (receipt_digest ~ '^[0-9a-f]{64}$'),
    target_kind TEXT NOT NULL CHECK (target_kind IN ('platform_mode', 'effect_class', 'kill_switch')),
    target_id TEXT NOT NULL CHECK (target_id <> '' AND target_id !~ '[[:space:][:cntrl:]]'),
    target_revision_id UUID NOT NULL,
    target_generation BIGINT NOT NULL CHECK (target_generation > 0),
    target_revision_digest CHAR(64) NOT NULL CHECK (target_revision_digest ~ '^[0-9a-f]{64}$'),
    expected_head_generation BIGINT NOT NULL CHECK (expected_head_generation >= 0),
    transition TEXT NOT NULL CHECK (transition IN ('raise', 'lower', 'resume', 'halt')),
    actor_principal TEXT NOT NULL CHECK (actor_principal <> '' AND actor_principal !~ '[[:space:][:cntrl:]]'),
    actor_kind TEXT NOT NULL CHECK (actor_kind = 'user'),
    actor_audience TEXT NOT NULL CHECK (actor_audience = 'activator'),
    deployment_mode_ceiling TEXT NOT NULL CHECK (deployment_mode_ceiling IN ('disabled', 'read_only', 'shadow', 'live_confirmed', 'live_autonomous')),
    deployment_effect_ceiling TEXT NOT NULL CHECK (deployment_effect_ceiling IN ('none', 'external_read', 'operation_intent', 'exact_confirmation', 'broker_mutation')),
    request_digest CHAR(64) NOT NULL CHECK (request_digest ~ '^[0-9a-f]{64}$'),
    reason_code TEXT NOT NULL CHECK (reason_code ~ '^[a-z][a-z0-9_]{0,63}$'),
    issued_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    CHECK (target_generation = expected_head_generation + 1),
    CHECK (expires_at > issued_at AND expires_at <= issued_at + interval '1 hour')
);

CREATE TABLE platform_governance.platform_mode_head (
    head_id TEXT PRIMARY KEY CHECK (head_id = 'global'),
    schema_revision SMALLINT NOT NULL CHECK (schema_revision = 1),
    generation BIGINT NOT NULL CHECK (generation > 0),
    revision_id UUID NOT NULL REFERENCES platform_governance.platform_mode_revision(revision_id),
    mode TEXT NOT NULL CHECK (mode IN ('disabled', 'read_only', 'shadow', 'live_confirmed', 'live_autonomous')),
    activation_receipt_id UUID REFERENCES platform_governance.activation_receipt(receipt_id),
    activated_by TEXT NOT NULL CHECK (activated_by <> '' AND activated_by !~ '[[:space:][:cntrl:]]'),
    activated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE platform_governance.effect_class_head (
    effect_class TEXT PRIMARY KEY CHECK (effect_class IN ('external_read', 'operation_intent', 'exact_confirmation', 'broker_mutation')),
    schema_revision SMALLINT NOT NULL CHECK (schema_revision = 1),
    generation BIGINT NOT NULL CHECK (generation > 0),
    revision_id UUID NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('enabled', 'halted')),
    activation_receipt_id UUID REFERENCES platform_governance.activation_receipt(receipt_id),
    activated_by TEXT NOT NULL CHECK (activated_by <> '' AND activated_by !~ '[[:space:][:cntrl:]]'),
    activated_at TIMESTAMPTZ NOT NULL,
    FOREIGN KEY (effect_class, generation, revision_id)
        REFERENCES platform_governance.effect_class_revision(effect_class, generation, revision_id)
);

CREATE TABLE platform_governance.kill_switch_head (
    switch_id TEXT PRIMARY KEY CHECK (switch_id IN (
        'agent_operation_emission', 'agent_release_activation', 'autonomous_live',
        'capability_external_execution', 'delegation_activation', 'exact_confirmation_live',
        'grace_publication', 'product_crypto', 'product_equity', 'product_option',
        'shadow_integration', 'strategy_activation'
    )),
    schema_revision SMALLINT NOT NULL CHECK (schema_revision = 1),
    generation BIGINT NOT NULL CHECK (generation > 0),
    revision_id UUID NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('enabled', 'halted')),
    activation_receipt_id UUID REFERENCES platform_governance.activation_receipt(receipt_id),
    activated_by TEXT NOT NULL CHECK (activated_by <> '' AND activated_by !~ '[[:space:][:cntrl:]]'),
    activated_at TIMESTAMPTZ NOT NULL,
    FOREIGN KEY (switch_id, generation, revision_id)
        REFERENCES platform_governance.kill_switch_revision(switch_id, generation, revision_id)
);

CREATE TABLE platform_governance.activation_receipt_consumption (
    receipt_id UUID PRIMARY KEY REFERENCES platform_governance.activation_receipt(receipt_id),
    subject_kind TEXT NOT NULL CHECK (subject_kind IN ('platform_mode', 'effect_class', 'kill_switch')),
    subject_id TEXT NOT NULL,
    head_generation BIGINT NOT NULL CHECK (head_generation > 0),
    event_id UUID NOT NULL UNIQUE,
    consumed_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE platform_governance.governance_event (
    event_id UUID PRIMARY KEY,
    schema_revision SMALLINT NOT NULL CHECK (schema_revision = 1),
    subject_kind TEXT NOT NULL CHECK (subject_kind IN ('platform_mode', 'effect_class', 'kill_switch')),
    subject_id TEXT NOT NULL,
    generation BIGINT NOT NULL CHECK (generation > 0),
    transition TEXT NOT NULL CHECK (transition IN ('raise', 'lower', 'resume', 'halt')),
    previous_revision_id UUID,
    current_revision_id UUID NOT NULL,
    activation_receipt_id UUID REFERENCES platform_governance.activation_receipt(receipt_id),
    actor_principal TEXT NOT NULL CHECK (actor_principal <> '' AND actor_principal !~ '[[:space:][:cntrl:]]'),
    actor_kind TEXT NOT NULL CHECK (actor_kind IN ('user', 'workload')),
    actor_audience TEXT NOT NULL CHECK (actor_audience = 'activator'),
    occurred_at TIMESTAMPTZ NOT NULL,
    reason_code TEXT NOT NULL CHECK (reason_code ~ '^[a-z][a-z0-9_]{0,63}$'),
    UNIQUE (subject_kind, subject_id, generation)
);

CREATE TRIGGER platform_mode_revision_immutable
BEFORE UPDATE OR DELETE ON platform_governance.platform_mode_revision
FOR EACH ROW EXECUTE FUNCTION platform_governance.reject_immutable_mutation();
CREATE TRIGGER effect_class_revision_immutable
BEFORE UPDATE OR DELETE ON platform_governance.effect_class_revision
FOR EACH ROW EXECUTE FUNCTION platform_governance.reject_immutable_mutation();
CREATE TRIGGER kill_switch_revision_immutable
BEFORE UPDATE OR DELETE ON platform_governance.kill_switch_revision
FOR EACH ROW EXECUTE FUNCTION platform_governance.reject_immutable_mutation();
CREATE TRIGGER activation_receipt_immutable
BEFORE UPDATE OR DELETE ON platform_governance.activation_receipt
FOR EACH ROW EXECUTE FUNCTION platform_governance.reject_immutable_mutation();
CREATE TRIGGER activation_receipt_consumption_immutable
BEFORE UPDATE OR DELETE ON platform_governance.activation_receipt_consumption
FOR EACH ROW EXECUTE FUNCTION platform_governance.reject_immutable_mutation();
CREATE TRIGGER governance_event_immutable
BEFORE UPDATE OR DELETE ON platform_governance.governance_event
FOR EACH ROW EXECUTE FUNCTION platform_governance.reject_immutable_mutation();

CREATE FUNCTION platform_governance.mode_rank(p_mode TEXT)
RETURNS INTEGER
LANGUAGE sql IMMUTABLE STRICT
AS $$
    SELECT CASE p_mode
        WHEN 'disabled' THEN 0 WHEN 'read_only' THEN 1 WHEN 'shadow' THEN 2
        WHEN 'live_confirmed' THEN 3 WHEN 'live_autonomous' THEN 4 ELSE -1 END
$$;

CREATE FUNCTION platform_governance.effect_rank(p_effect TEXT)
RETURNS INTEGER
LANGUAGE sql IMMUTABLE STRICT
AS $$
    SELECT CASE p_effect
        WHEN 'none' THEN 0 WHEN 'external_read' THEN 1 WHEN 'operation_intent' THEN 2
        WHEN 'exact_confirmation' THEN 3 WHEN 'broker_mutation' THEN 4 ELSE 100 END
$$;

CREATE FUNCTION platform_governance.create_revision(
    p_target_kind TEXT,
    p_target_id TEXT,
    p_revision_id UUID,
    p_generation BIGINT,
    p_value TEXT,
    p_state TEXT,
    p_revision_digest TEXT,
    p_actor TEXT,
    p_reason_code TEXT
) RETURNS TIMESTAMPTZ
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, platform_governance
AS $$
DECLARE
    created TIMESTAMPTZ := clock_timestamp();
BEGIN
    IF p_actor IS NULL OR p_actor = '' OR p_actor ~ '[[:space:][:cntrl:]]'
       OR p_reason_code !~ '^[a-z][a-z0-9_]{0,63}$'
       OR p_revision_digest !~ '^[0-9a-f]{64}$' OR p_generation <= 0 THEN
        RAISE EXCEPTION USING ERRCODE = '22023', MESSAGE = 'invalid governance revision';
    END IF;
    IF p_target_kind = 'platform_mode' AND p_target_id = 'global' AND p_state IS NULL THEN
        INSERT INTO platform_governance.platform_mode_revision (
            revision_id, schema_revision, generation, mode, revision_digest,
            author_principal, author_kind, author_audience, reason_code, created_at
        ) VALUES (p_revision_id, 1, p_generation, p_value, p_revision_digest,
            p_actor, 'user', 'activator', p_reason_code, created);
    ELSIF p_target_kind = 'effect_class' AND p_target_id = p_value THEN
        INSERT INTO platform_governance.effect_class_revision (
            revision_id, schema_revision, generation, effect_class, state, revision_digest,
            author_principal, author_kind, author_audience, reason_code, created_at
        ) VALUES (p_revision_id, 1, p_generation, p_value, p_state, p_revision_digest,
            p_actor, 'user', 'activator', p_reason_code, created);
    ELSIF p_target_kind = 'kill_switch' AND p_target_id = p_value THEN
        INSERT INTO platform_governance.kill_switch_revision (
            revision_id, schema_revision, generation, switch_id, state, revision_digest,
            author_principal, author_kind, author_audience, reason_code, created_at
        ) VALUES (p_revision_id, 1, p_generation, p_value, p_state, p_revision_digest,
            p_actor, 'user', 'activator', p_reason_code, created);
    ELSE
        RAISE EXCEPTION USING ERRCODE = '22023', MESSAGE = 'invalid governance revision target';
    END IF;
    RETURN created;
END
$$;

CREATE FUNCTION platform_governance.issue_activation_receipt(
    p_receipt_id UUID,
    p_receipt_digest TEXT,
    p_target_kind TEXT,
    p_target_id TEXT,
    p_target_revision_id UUID,
    p_target_generation BIGINT,
    p_target_revision_digest TEXT,
    p_expected_head_generation BIGINT,
    p_transition TEXT,
    p_actor TEXT,
    p_deployment_mode_ceiling TEXT,
    p_deployment_effect_ceiling TEXT,
    p_request_digest TEXT,
    p_reason_code TEXT,
    p_issued_at TIMESTAMPTZ,
    p_expires_at TIMESTAMPTZ
) RETURNS UUID
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, platform_governance
AS $$
DECLARE
    candidate_matches BOOLEAN := false;
BEGIN
    IF p_actor IS NULL OR p_actor = '' OR p_actor ~ '[[:space:][:cntrl:]]'
       OR p_receipt_digest !~ '^[0-9a-f]{64}$' OR p_request_digest !~ '^[0-9a-f]{64}$'
       OR p_reason_code !~ '^[a-z][a-z0-9_]{0,63}$'
       OR p_target_generation <> p_expected_head_generation + 1
       OR p_issued_at > clock_timestamp() OR p_expires_at <= clock_timestamp()
       OR p_expires_at > p_issued_at + interval '1 hour' THEN
        RAISE EXCEPTION USING ERRCODE = '22023', MESSAGE = 'invalid activation receipt';
    END IF;
    IF p_target_kind = 'platform_mode' AND p_target_id = 'global' THEN
        SELECT EXISTS (SELECT 1 FROM platform_governance.platform_mode_revision
            WHERE revision_id = p_target_revision_id AND generation = p_target_generation
              AND revision_digest = p_target_revision_digest) INTO candidate_matches;
    ELSIF p_target_kind = 'effect_class' THEN
        SELECT EXISTS (SELECT 1 FROM platform_governance.effect_class_revision
            WHERE revision_id = p_target_revision_id AND generation = p_target_generation
              AND effect_class = p_target_id AND revision_digest = p_target_revision_digest) INTO candidate_matches;
    ELSIF p_target_kind = 'kill_switch' THEN
        SELECT EXISTS (SELECT 1 FROM platform_governance.kill_switch_revision
            WHERE revision_id = p_target_revision_id AND generation = p_target_generation
              AND switch_id = p_target_id AND revision_digest = p_target_revision_digest) INTO candidate_matches;
    END IF;
    IF NOT candidate_matches THEN
        RAISE EXCEPTION USING ERRCODE = '23503', MESSAGE = 'activation target revision mismatch';
    END IF;
    INSERT INTO platform_governance.activation_receipt (
        receipt_id, schema_revision, receipt_digest, target_kind, target_id,
        target_revision_id, target_generation, target_revision_digest,
        expected_head_generation, transition, actor_principal, actor_kind, actor_audience,
        deployment_mode_ceiling, deployment_effect_ceiling, request_digest,
        reason_code, issued_at, expires_at
    ) VALUES (
        p_receipt_id, 1, p_receipt_digest, p_target_kind, p_target_id,
        p_target_revision_id, p_target_generation, p_target_revision_digest,
        p_expected_head_generation, p_transition, p_actor, 'user', 'activator',
        p_deployment_mode_ceiling, p_deployment_effect_ceiling, p_request_digest,
        p_reason_code, p_issued_at, p_expires_at
    );
    RETURN p_receipt_id;
END
$$;

CREATE FUNCTION platform_governance.activate_head(
    p_receipt_id UUID,
    p_expected_generation BIGINT,
    p_activator TEXT
) RETURNS TABLE (subject_kind TEXT, subject_id TEXT, generation BIGINT)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, platform_governance
AS $$
DECLARE
    receipt platform_governance.activation_receipt%ROWTYPE;
    current_generation BIGINT := 0;
    previous_revision UUID;
    current_value TEXT;
    target_value TEXT;
    target_state TEXT;
    event UUID := gen_random_uuid();
    transition_ok BOOLEAN := false;
BEGIN
    IF p_activator IS NULL OR p_activator = '' OR p_activator ~ '[[:space:][:cntrl:]]' THEN
        RAISE EXCEPTION USING ERRCODE = '22023', MESSAGE = 'invalid activator';
    END IF;
    SELECT * INTO STRICT receipt FROM platform_governance.activation_receipt
    WHERE receipt_id = p_receipt_id FOR UPDATE;
    IF clock_timestamp() < receipt.issued_at OR clock_timestamp() >= receipt.expires_at
       OR receipt.expected_head_generation <> p_expected_generation THEN
        RAISE EXCEPTION USING ERRCODE = '40001', MESSAGE = 'stale or expired activation receipt';
    END IF;
    -- A stable subject lock also serializes bootstrap while no head row exists.
    PERFORM pg_advisory_xact_lock(hashtextextended(
        'alpheus.platform_governance.' || receipt.target_kind || '.' || receipt.target_id, 0
    ));

    IF receipt.target_kind = 'platform_mode' THEN
        SELECT head.generation, head.revision_id, head.mode
        INTO current_generation, previous_revision, current_value
        FROM platform_governance.platform_mode_head AS head WHERE head.head_id = 'global' FOR UPDATE;
        current_generation := coalesce(current_generation, 0);
        IF current_generation = receipt.target_generation AND EXISTS (
            SELECT 1 FROM platform_governance.platform_mode_head WHERE activation_receipt_id = receipt.receipt_id
        ) THEN
            RETURN QUERY SELECT receipt.target_kind, receipt.target_id, current_generation;
            RETURN;
        END IF;
        SELECT candidate.mode INTO STRICT target_value FROM platform_governance.platform_mode_revision AS candidate
        WHERE candidate.revision_id = receipt.target_revision_id AND candidate.generation = receipt.target_generation
          AND candidate.revision_digest = receipt.target_revision_digest;
        transition_ok := CASE
            WHEN platform_governance.mode_rank(target_value) > platform_governance.mode_rank(coalesce(current_value, 'disabled'))
                THEN receipt.transition IN ('raise', 'resume')
            WHEN platform_governance.mode_rank(target_value) < platform_governance.mode_rank(coalesce(current_value, 'disabled'))
                THEN receipt.transition IN ('lower', 'halt')
            ELSE false
        END;
        IF platform_governance.mode_rank(target_value) > platform_governance.mode_rank(receipt.deployment_mode_ceiling) THEN
            RAISE EXCEPTION USING ERRCODE = '42501', MESSAGE = 'mode exceeds deployment ceiling';
        END IF;
    ELSIF receipt.target_kind = 'effect_class' THEN
        SELECT head.generation, head.revision_id, head.state
        INTO current_generation, previous_revision, current_value
        FROM platform_governance.effect_class_head AS head WHERE head.effect_class = receipt.target_id FOR UPDATE;
        current_generation := coalesce(current_generation, 0);
        IF current_generation = receipt.target_generation AND EXISTS (
            SELECT 1 FROM platform_governance.effect_class_head WHERE effect_class = receipt.target_id AND activation_receipt_id = receipt.receipt_id
        ) THEN
            RETURN QUERY SELECT receipt.target_kind, receipt.target_id, current_generation;
            RETURN;
        END IF;
        SELECT candidate.state INTO STRICT target_state FROM platform_governance.effect_class_revision AS candidate
        WHERE candidate.revision_id = receipt.target_revision_id AND candidate.generation = receipt.target_generation
          AND candidate.effect_class = receipt.target_id AND candidate.revision_digest = receipt.target_revision_digest;
        transition_ok := CASE
            WHEN target_state = 'enabled' AND coalesce(current_value, 'halted') = 'halted'
                THEN receipt.transition IN ('raise', 'resume')
            WHEN target_state = 'halted' AND current_value = 'enabled'
                THEN receipt.transition IN ('lower', 'halt')
            ELSE false
        END;
        IF target_state = 'enabled' AND platform_governance.effect_rank(receipt.target_id) >
            platform_governance.effect_rank(receipt.deployment_effect_ceiling) THEN
            RAISE EXCEPTION USING ERRCODE = '42501', MESSAGE = 'effect exceeds deployment ceiling';
        END IF;
    ELSIF receipt.target_kind = 'kill_switch' THEN
        SELECT head.generation, head.revision_id, head.state
        INTO current_generation, previous_revision, current_value
        FROM platform_governance.kill_switch_head AS head WHERE head.switch_id = receipt.target_id FOR UPDATE;
        current_generation := coalesce(current_generation, 0);
        IF current_generation = receipt.target_generation AND EXISTS (
            SELECT 1 FROM platform_governance.kill_switch_head WHERE switch_id = receipt.target_id AND activation_receipt_id = receipt.receipt_id
        ) THEN
            RETURN QUERY SELECT receipt.target_kind, receipt.target_id, current_generation;
            RETURN;
        END IF;
        SELECT candidate.state INTO STRICT target_state FROM platform_governance.kill_switch_revision AS candidate
        WHERE candidate.revision_id = receipt.target_revision_id AND candidate.generation = receipt.target_generation
          AND candidate.switch_id = receipt.target_id AND candidate.revision_digest = receipt.target_revision_digest;
        transition_ok := CASE
            WHEN target_state = 'enabled' AND coalesce(current_value, 'halted') = 'halted'
                THEN receipt.transition IN ('raise', 'resume')
            WHEN target_state = 'halted' AND current_value = 'enabled'
                THEN receipt.transition IN ('lower', 'halt')
            ELSE false
        END;
    ELSE
        RAISE EXCEPTION USING ERRCODE = '22023', MESSAGE = 'unknown activation target';
    END IF;

    IF current_generation <> p_expected_generation OR current_generation <> receipt.expected_head_generation THEN
        RAISE EXCEPTION USING ERRCODE = '40001', MESSAGE = 'stale governance head generation';
    END IF;
    IF NOT transition_ok THEN
        RAISE EXCEPTION USING ERRCODE = '22023', MESSAGE = 'activation transition direction mismatch';
    END IF;
    INSERT INTO platform_governance.activation_receipt_consumption (
        receipt_id, subject_kind, subject_id, head_generation, event_id, consumed_at
    ) VALUES (receipt.receipt_id, receipt.target_kind, receipt.target_id, receipt.target_generation, event, clock_timestamp());

    IF receipt.target_kind = 'platform_mode' THEN
        INSERT INTO platform_governance.platform_mode_head (
            head_id, schema_revision, generation, revision_id, mode,
            activation_receipt_id, activated_by, activated_at
        ) VALUES ('global', 1, receipt.target_generation, receipt.target_revision_id, target_value,
            receipt.receipt_id, p_activator, clock_timestamp())
        ON CONFLICT (head_id) DO UPDATE SET generation = EXCLUDED.generation,
            revision_id = EXCLUDED.revision_id, mode = EXCLUDED.mode,
            activation_receipt_id = EXCLUDED.activation_receipt_id,
            activated_by = EXCLUDED.activated_by, activated_at = EXCLUDED.activated_at;
    ELSIF receipt.target_kind = 'effect_class' THEN
        INSERT INTO platform_governance.effect_class_head (
            effect_class, schema_revision, generation, revision_id, state,
            activation_receipt_id, activated_by, activated_at
        ) VALUES (receipt.target_id, 1, receipt.target_generation, receipt.target_revision_id, target_state,
            receipt.receipt_id, p_activator, clock_timestamp())
        ON CONFLICT (effect_class) DO UPDATE SET generation = EXCLUDED.generation,
            revision_id = EXCLUDED.revision_id, state = EXCLUDED.state,
            activation_receipt_id = EXCLUDED.activation_receipt_id,
            activated_by = EXCLUDED.activated_by, activated_at = EXCLUDED.activated_at;
    ELSE
        INSERT INTO platform_governance.kill_switch_head (
            switch_id, schema_revision, generation, revision_id, state,
            activation_receipt_id, activated_by, activated_at
        ) VALUES (receipt.target_id, 1, receipt.target_generation, receipt.target_revision_id, target_state,
            receipt.receipt_id, p_activator, clock_timestamp())
        ON CONFLICT (switch_id) DO UPDATE SET generation = EXCLUDED.generation,
            revision_id = EXCLUDED.revision_id, state = EXCLUDED.state,
            activation_receipt_id = EXCLUDED.activation_receipt_id,
            activated_by = EXCLUDED.activated_by, activated_at = EXCLUDED.activated_at;
    END IF;
    INSERT INTO platform_governance.governance_event (
        event_id, schema_revision, subject_kind, subject_id, generation, transition,
        previous_revision_id, current_revision_id, activation_receipt_id,
        actor_principal, actor_kind, actor_audience, occurred_at, reason_code
    ) VALUES (event, 1, receipt.target_kind, receipt.target_id, receipt.target_generation, receipt.transition,
        previous_revision, receipt.target_revision_id, receipt.receipt_id,
        p_activator, 'workload', 'activator', clock_timestamp(), receipt.reason_code);
    RETURN QUERY SELECT receipt.target_kind, receipt.target_id, receipt.target_generation;
END
$$;

CREATE FUNCTION platform_governance.emergency_halt(
    p_target_kind TEXT,
    p_target_id TEXT,
    p_expected_generation BIGINT,
    p_revision_id UUID,
    p_revision_digest TEXT,
    p_actor TEXT,
    p_reason_code TEXT
) RETURNS BIGINT
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, platform_governance
AS $$
DECLARE
    current_generation BIGINT := 0;
    previous_revision UUID;
    next_generation BIGINT := p_expected_generation + 1;
    event UUID := gen_random_uuid();
    now_at TIMESTAMPTZ := clock_timestamp();
BEGIN
    IF p_actor IS NULL OR p_actor = '' OR p_actor ~ '[[:space:][:cntrl:]]'
       OR p_revision_digest !~ '^[0-9a-f]{64}$'
       OR p_reason_code !~ '^[a-z][a-z0-9_]{0,63}$' OR p_expected_generation < 0 THEN
        RAISE EXCEPTION USING ERRCODE = '22023', MESSAGE = 'invalid emergency halt';
    END IF;
    PERFORM pg_advisory_xact_lock(hashtextextended(
        'alpheus.platform_governance.' || p_target_kind || '.' || p_target_id, 0
    ));
    IF p_target_kind = 'platform_mode' AND p_target_id = 'global' THEN
        SELECT generation, revision_id INTO current_generation, previous_revision
        FROM platform_governance.platform_mode_head WHERE head_id = 'global' FOR UPDATE;
        current_generation := coalesce(current_generation, 0);
        IF current_generation <> p_expected_generation THEN
            RAISE EXCEPTION USING ERRCODE = '40001', MESSAGE = 'stale governance head generation';
        END IF;
        INSERT INTO platform_governance.platform_mode_revision (
            revision_id, schema_revision, generation, mode, revision_digest,
            author_principal, author_kind, author_audience, reason_code, created_at
        ) VALUES (p_revision_id, 1, next_generation, 'disabled', p_revision_digest,
            p_actor, 'workload', 'activator', p_reason_code, now_at);
        INSERT INTO platform_governance.platform_mode_head (
            head_id, schema_revision, generation, revision_id, mode, activation_receipt_id, activated_by, activated_at
        ) VALUES ('global', 1, next_generation, p_revision_id, 'disabled', NULL, p_actor, now_at)
        ON CONFLICT (head_id) DO UPDATE SET generation = EXCLUDED.generation,
            revision_id = EXCLUDED.revision_id, mode = EXCLUDED.mode, activation_receipt_id = NULL,
            activated_by = EXCLUDED.activated_by, activated_at = EXCLUDED.activated_at;
    ELSIF p_target_kind = 'effect_class' THEN
        SELECT generation, revision_id INTO current_generation, previous_revision
        FROM platform_governance.effect_class_head WHERE effect_class = p_target_id FOR UPDATE;
        current_generation := coalesce(current_generation, 0);
        IF current_generation <> p_expected_generation THEN
            RAISE EXCEPTION USING ERRCODE = '40001', MESSAGE = 'stale governance head generation';
        END IF;
        INSERT INTO platform_governance.effect_class_revision (
            revision_id, schema_revision, generation, effect_class, state, revision_digest,
            author_principal, author_kind, author_audience, reason_code, created_at
        ) VALUES (p_revision_id, 1, next_generation, p_target_id, 'halted', p_revision_digest,
            p_actor, 'workload', 'activator', p_reason_code, now_at);
        INSERT INTO platform_governance.effect_class_head (
            effect_class, schema_revision, generation, revision_id, state, activation_receipt_id, activated_by, activated_at
        ) VALUES (p_target_id, 1, next_generation, p_revision_id, 'halted', NULL, p_actor, now_at)
        ON CONFLICT (effect_class) DO UPDATE SET generation = EXCLUDED.generation,
            revision_id = EXCLUDED.revision_id, state = EXCLUDED.state, activation_receipt_id = NULL,
            activated_by = EXCLUDED.activated_by, activated_at = EXCLUDED.activated_at;
    ELSIF p_target_kind = 'kill_switch' THEN
        SELECT generation, revision_id INTO current_generation, previous_revision
        FROM platform_governance.kill_switch_head WHERE switch_id = p_target_id FOR UPDATE;
        current_generation := coalesce(current_generation, 0);
        IF current_generation <> p_expected_generation THEN
            RAISE EXCEPTION USING ERRCODE = '40001', MESSAGE = 'stale governance head generation';
        END IF;
        INSERT INTO platform_governance.kill_switch_revision (
            revision_id, schema_revision, generation, switch_id, state, revision_digest,
            author_principal, author_kind, author_audience, reason_code, created_at
        ) VALUES (p_revision_id, 1, next_generation, p_target_id, 'halted', p_revision_digest,
            p_actor, 'workload', 'activator', p_reason_code, now_at);
        INSERT INTO platform_governance.kill_switch_head (
            switch_id, schema_revision, generation, revision_id, state, activation_receipt_id, activated_by, activated_at
        ) VALUES (p_target_id, 1, next_generation, p_revision_id, 'halted', NULL, p_actor, now_at)
        ON CONFLICT (switch_id) DO UPDATE SET generation = EXCLUDED.generation,
            revision_id = EXCLUDED.revision_id, state = EXCLUDED.state, activation_receipt_id = NULL,
            activated_by = EXCLUDED.activated_by, activated_at = EXCLUDED.activated_at;
    ELSE
        RAISE EXCEPTION USING ERRCODE = '22023', MESSAGE = 'invalid emergency halt target';
    END IF;
    INSERT INTO platform_governance.governance_event (
        event_id, schema_revision, subject_kind, subject_id, generation, transition,
        previous_revision_id, current_revision_id, activation_receipt_id,
        actor_principal, actor_kind, actor_audience, occurred_at, reason_code
    ) VALUES (event, 1, p_target_kind, p_target_id, next_generation, 'halt',
        previous_revision, p_revision_id, NULL, p_actor, 'workload', 'activator', now_at, p_reason_code);
    RETURN next_generation;
END
$$;

CREATE VIEW platform_governance.current_head AS
SELECT 'platform_mode'::TEXT AS subject_kind, head.head_id AS subject_id, head.generation, head.revision_id,
       revision.revision_digest::TEXT, head.mode AS value, NULL::TEXT AS state,
       revision.author_principal, revision.author_kind, revision.author_audience,
       revision.reason_code, revision.created_at AS revision_created_at,
       head.activation_receipt_id, receipt.receipt_digest::TEXT AS activation_receipt_digest,
       head.activated_by, head.activated_at
FROM platform_governance.platform_mode_head AS head
JOIN platform_governance.platform_mode_revision AS revision ON revision.revision_id = head.revision_id
LEFT JOIN platform_governance.activation_receipt AS receipt ON receipt.receipt_id = head.activation_receipt_id
UNION ALL
SELECT 'effect_class', head.effect_class, head.generation, head.revision_id,
       revision.revision_digest::TEXT, head.effect_class, head.state,
       revision.author_principal, revision.author_kind, revision.author_audience,
       revision.reason_code, revision.created_at,
       head.activation_receipt_id, receipt.receipt_digest::TEXT,
       head.activated_by, head.activated_at
FROM platform_governance.effect_class_head AS head
JOIN platform_governance.effect_class_revision AS revision ON revision.revision_id = head.revision_id
LEFT JOIN platform_governance.activation_receipt AS receipt ON receipt.receipt_id = head.activation_receipt_id
UNION ALL
SELECT 'kill_switch', head.switch_id, head.generation, head.revision_id,
       revision.revision_digest::TEXT, head.switch_id, head.state,
       revision.author_principal, revision.author_kind, revision.author_audience,
       revision.reason_code, revision.created_at,
       head.activation_receipt_id, receipt.receipt_digest::TEXT,
       head.activated_by, head.activated_at
FROM platform_governance.kill_switch_head AS head
JOIN platform_governance.kill_switch_revision AS revision ON revision.revision_id = head.revision_id
LEFT JOIN platform_governance.activation_receipt AS receipt ON receipt.receipt_id = head.activation_receipt_id;

CREATE VIEW platform_governance.governance_health AS
SELECT
    (SELECT count(*) FROM platform_governance.platform_mode_head) AS platform_mode_heads,
    (SELECT count(*) FROM platform_governance.effect_class_head) AS effect_class_heads,
    (SELECT count(*) FROM platform_governance.kill_switch_head) AS kill_switch_heads,
    (SELECT count(*) FROM platform_governance.activation_receipt WHERE expires_at > clock_timestamp()) AS current_receipts,
    (SELECT count(*) FROM platform_governance.governance_event) AS event_count;

RESET ROLE;
