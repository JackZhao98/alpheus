SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

-- AP1's public database boundary is intentionally three JSON commands.  The
-- caller cannot select or mutate Runtime tables, choose its audit identity, or
-- call the private helpers below.  Every public function resolves session_user
-- through the AP0 identity root before it acquires the idempotency row lock.

-- This 0005 constraint trigger is deferred until the outer SQL statement has
-- left a SECURITY DEFINER command body. Without a definer owner it would run
-- as the default-deny Worker and fail while reading its own invariant tables.
-- It remains non-callable; only PostgreSQL's trigger invokes it.
ALTER FUNCTION agent_control.validate_runtime_unresolved_turn_attempt()
    SECURITY DEFINER;
ALTER FUNCTION agent_control.validate_runtime_unresolved_turn_attempt()
    SET search_path = pg_catalog, agent_control;
REVOKE ALL ON FUNCTION
    agent_control.validate_runtime_unresolved_turn_attempt()
FROM PUBLIC;

CREATE FUNCTION agent_control.runtime_sha256_json(p_value JSONB)
RETURNS CHAR(64)
LANGUAGE sql
IMMUTABLE
STRICT
AS $$
    SELECT encode(sha256(convert_to(p_value::TEXT, 'UTF8')), 'hex')::CHAR(64)
$$;

-- Public commands enter as raw TEXT, not JSONB. PostgreSQL JSONB would erase
-- duplicate object keys and normalize exponent-form numbers before validation,
-- making two contract-invalid byte streams indistinguishable from valid input.
-- JSON preserves both; this recursive pass rejects duplicates and every number
-- outside the common canonical profile before the command becomes JSONB.
CREATE FUNCTION agent_control.runtime_strict_json_valid(p_value JSON)
RETURNS BOOLEAN
LANGUAGE plpgsql
IMMUTABLE
STRICT
AS $$
DECLARE
    value_type TEXT := json_typeof(p_value);
    member RECORD;
BEGIN
    CASE value_type
        WHEN 'object' THEN
            IF EXISTS (
                SELECT 1
                FROM json_each(p_value) AS duplicate_member
                GROUP BY duplicate_member.key
                HAVING count(*) > 1
            ) THEN
                RETURN false;
            END IF;
            FOR member IN SELECT value FROM json_each(p_value) LOOP
                IF NOT agent_control.runtime_strict_json_valid(member.value) THEN
                    RETURN false;
                END IF;
            END LOOP;
        WHEN 'array' THEN
            FOR member IN SELECT value FROM json_array_elements(p_value) LOOP
                IF NOT agent_control.runtime_strict_json_valid(member.value) THEN
                    RETURN false;
                END IF;
            END LOOP;
        WHEN 'number' THEN
            IF p_value::TEXT !~ '^-?(0|[1-9][0-9]*)$'
               OR p_value::TEXT = '-0' THEN
                RETURN false;
            END IF;
        WHEN 'string', 'boolean', 'null' THEN
            NULL;
        ELSE
            RETURN false;
    END CASE;
    RETURN true;
EXCEPTION WHEN OTHERS THEN
    RETURN false;
END
$$;

CREATE FUNCTION agent_control.runtime_parse_worker_command(p_value TEXT)
RETURNS JSONB
LANGUAGE plpgsql
IMMUTABLE
AS $$
DECLARE
    parsed JSON;
BEGIN
    IF p_value IS NULL OR octet_length(p_value) > 1048576 THEN
        RETURN NULL;
    END IF;
    BEGIN
        parsed := p_value::JSON;
    EXCEPTION WHEN OTHERS THEN
        RETURN NULL;
    END;
    IF NOT agent_control.runtime_strict_json_valid(parsed) THEN
        RETURN NULL;
    END IF;
    BEGIN
        RETURN parsed::JSONB;
    EXCEPTION WHEN OTHERS THEN
        -- JSON accepts numeric lexemes wider than JSONB's NUMERIC storage.
        -- Keep that parser detail behind the stable invalid-input boundary.
        RETURN NULL;
    END;
END
$$;

CREATE FUNCTION agent_control.runtime_canonical_json(p_value JSONB)
RETURNS TEXT
LANGUAGE plpgsql
IMMUTABLE
STRICT
AS $$
DECLARE
    value_type TEXT := jsonb_typeof(p_value);
    canonical TEXT;
BEGIN
    CASE value_type
        WHEN 'object' THEN
            SELECT '{' || coalesce(string_agg(
                to_jsonb(member.key)::TEXT || ':'
                    || agent_control.runtime_canonical_json(member.value),
                ',' ORDER BY member.key COLLATE "C"
            ), '') || '}'
            INTO canonical
            FROM jsonb_each(p_value) AS member;
        WHEN 'array' THEN
            SELECT '[' || coalesce(string_agg(
                agent_control.runtime_canonical_json(member.value),
                ',' ORDER BY member.ordinal
            ), '') || ']'
            INTO canonical
            FROM jsonb_array_elements(p_value)
                 WITH ORDINALITY AS member(value, ordinal);
        WHEN 'string' THEN
            canonical := to_jsonb(p_value #>> '{}')::TEXT;
        WHEN 'number' THEN
            canonical := p_value::TEXT;
            IF canonical !~ '^-?(0|[1-9][0-9]*)$' OR canonical = '-0' THEN
                RAISE EXCEPTION USING ERRCODE = '22023',
                    MESSAGE = 'canonical Runtime JSON requires integers';
            END IF;
        WHEN 'boolean', 'null' THEN
            canonical := p_value::TEXT;
        ELSE
            RAISE EXCEPTION USING ERRCODE = '22023',
                MESSAGE = 'unsupported canonical Runtime JSON value';
    END CASE;
    RETURN canonical;
END
$$;

CREATE FUNCTION agent_control.runtime_contract_digest(
    p_domain TEXT,
    p_value JSONB
) RETURNS CHAR(64)
LANGUAGE plpgsql
IMMUTABLE
STRICT
AS $$
DECLARE
    preimage TEXT;
BEGIN
    IF p_domain !~ '^[a-z][a-z0-9._-]{0,127}$' THEN
        RAISE EXCEPTION USING ERRCODE = '22023',
            MESSAGE = 'invalid canonical Runtime digest domain';
    END IF;
    preimage := 'alpheus-c14n-v1' || chr(10) || p_domain || chr(10)
        || agent_control.runtime_canonical_json(p_value);
    RETURN encode(sha256(convert_to(preimage, 'UTF8')), 'hex')::CHAR(64);
END
$$;

CREATE FUNCTION agent_control.runtime_utc_text(p_value TIMESTAMPTZ)
RETURNS TEXT
LANGUAGE plpgsql
IMMUTABLE
STRICT
AS $$
DECLARE
    whole TEXT;
    fraction TEXT;
BEGIN
    whole := to_char(
        p_value AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS'
    );
    fraction := rtrim(
        to_char(p_value AT TIME ZONE 'UTC', 'US'), '0'
    );
    RETURN whole || CASE WHEN fraction = '' THEN '' ELSE '.' || fraction END
        || 'Z';
END
$$;

CREATE FUNCTION agent_control.runtime_positive_bigint_json(p_value JSONB)
RETURNS BOOLEAN
LANGUAGE plpgsql
IMMUTABLE
STRICT
AS $$
DECLARE
    parsed NUMERIC;
BEGIN
    IF jsonb_typeof(p_value) <> 'number'
       OR p_value::TEXT !~ '^[1-9][0-9]*$' THEN
        RETURN false;
    END IF;
    parsed := p_value::TEXT::NUMERIC;
    RETURN parsed <= 9223372036854775807;
EXCEPTION WHEN OTHERS THEN
    RETURN false;
END
$$;

CREATE FUNCTION agent_control.runtime_utc_instant_json(p_value JSONB)
RETURNS BOOLEAN
LANGUAGE plpgsql
IMMUTABLE
STRICT
AS $$
DECLARE
    parsed TIMESTAMPTZ;
    raw_value TEXT;
BEGIN
    IF jsonb_typeof(p_value) <> 'string'
       OR p_value #>> '{}' !~
          '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}([.][0-9]{1,9})?Z$' THEN
        RETURN false;
    END IF;
    raw_value := p_value #>> '{}';
    -- PostgreSQL normalizes 24:00:00 and leap-second 23:59:60; Go's frozen
    -- RFC3339 boundary rejects both, so enforce the component ranges first.
    IF substring(raw_value FROM 12 FOR 2)::INTEGER > 23
       OR substring(raw_value FROM 15 FOR 2)::INTEGER > 59
       OR substring(raw_value FROM 18 FOR 2)::INTEGER > 59 THEN
        RETURN false;
    END IF;
    parsed := raw_value::TIMESTAMPTZ;
    RETURN parsed IS NOT NULL;
EXCEPTION WHEN OTHERS THEN
    RETURN false;
END
$$;

CREATE FUNCTION agent_control.runtime_worker_command_valid(
    p_command JSONB,
    p_expected_command_type TEXT,
    p_expected_top_level_keys TEXT[]
) RETURNS BOOLEAN
LANGUAGE plpgsql
STABLE
STRICT
AS $$
DECLARE
    envelope JSONB;
    actor JSONB;
    invoker RECORD;
BEGIN
    IF jsonb_typeof(p_command) <> 'object'
       OR NOT (p_command ?& p_expected_top_level_keys)
       OR p_command - p_expected_top_level_keys <> '{}'::JSONB
       OR jsonb_typeof(p_command->'schema_revision') <> 'number'
       OR p_command->>'schema_revision' <> '1'
       OR jsonb_typeof(p_command->'envelope') <> 'object' THEN
        RETURN false;
    END IF;

    envelope := p_command->'envelope';
    IF NOT (envelope ?& ARRAY[
            'schema_revision', 'command_id', 'actor', 'audience',
            'command_type', 'idempotency_key', 'request_digest',
            'causation_id', 'correlation_id', 'deadline'
        ])
       OR envelope - ARRAY[
            'schema_revision', 'command_id', 'actor', 'audience',
            'command_type', 'idempotency_key', 'request_digest',
            'causation_id', 'correlation_id', 'deadline'
        ] <> '{}'::JSONB
       OR jsonb_typeof(envelope->'schema_revision') <> 'number'
       OR envelope->>'schema_revision' <> '1'
       OR jsonb_typeof(envelope->'command_id') <> 'string'
       OR NOT agent_control.runtime_identifier_valid(envelope->>'command_id')
       OR jsonb_typeof(envelope->'actor') <> 'object'
       OR jsonb_typeof(envelope->'audience') <> 'string'
       OR envelope->>'audience' <> 'control_api'
       OR jsonb_typeof(envelope->'command_type') <> 'string'
       OR envelope->>'command_type' <> p_expected_command_type
       OR jsonb_typeof(envelope->'idempotency_key') <> 'string'
       OR NOT agent_control.runtime_identifier_valid(envelope->>'idempotency_key')
       OR jsonb_typeof(envelope->'request_digest') <> 'string'
       OR NOT agent_control.runtime_digest_valid(envelope->>'request_digest')
       OR jsonb_typeof(envelope->'causation_id') <> 'string'
       OR NOT agent_control.runtime_identifier_valid(envelope->>'causation_id')
       OR jsonb_typeof(envelope->'correlation_id') <> 'string'
       OR NOT agent_control.runtime_identifier_valid(envelope->>'correlation_id')
       OR NOT agent_control.runtime_utc_instant_json(envelope->'deadline') THEN
        RETURN false;
    END IF;

    actor := envelope->'actor';
    IF NOT agent_control.runtime_actor_valid(actor)
       OR actor->>'kind' <> 'workload'
       OR actor->>'audience' <> 'worker' THEN
        RETURN false;
    END IF;

    SELECT * INTO STRICT invoker
    FROM platform_security.invoker_identity();
    RETURN invoker.group_role = 'alpheus_agent_worker'::NAME
       AND invoker.profile_id = 'worker'
       AND invoker.owner_id = 'worker'
       AND actor->>'principal_id' = invoker.principal_id;
EXCEPTION
    WHEN insufficient_privilege THEN
        RAISE;
    WHEN OTHERS THEN
        RETURN false;
END
$$;

CREATE FUNCTION agent_control.runtime_claim_task_command_valid(p_command JSONB)
RETURNS BOOLEAN
LANGUAGE sql
STABLE
STRICT
AS $$
    SELECT agent_control.runtime_worker_command_valid(
               p_command,
               'claim_task',
               ARRAY[
                   'schema_revision', 'envelope', 'task_id',
                   'expected_task_state_generation', 'requested_lease_seconds'
               ]
           )
       AND jsonb_typeof(p_command->'task_id') = 'string'
       AND agent_control.runtime_identifier_valid(p_command->>'task_id')
       AND agent_control.runtime_positive_bigint_json(
               p_command->'expected_task_state_generation'
           )
       AND agent_control.runtime_positive_bigint_json(
               p_command->'requested_lease_seconds'
           )
$$;

CREATE FUNCTION agent_control.runtime_start_attempt_command_valid(p_command JSONB)
RETURNS BOOLEAN
LANGUAGE sql
STABLE
STRICT
AS $$
    SELECT agent_control.runtime_worker_command_valid(
               p_command,
               'start_attempt',
               ARRAY[
                   'schema_revision', 'envelope', 'attempt_id',
                   'expected_attempt_state_generation', 'lease_generation',
                   'lease_token'
               ]
           )
       AND jsonb_typeof(p_command->'attempt_id') = 'string'
       AND agent_control.runtime_identifier_valid(p_command->>'attempt_id')
       AND agent_control.runtime_positive_bigint_json(
               p_command->'expected_attempt_state_generation'
           )
       AND agent_control.runtime_positive_bigint_json(
               p_command->'lease_generation'
           )
       AND jsonb_typeof(p_command->'lease_token') = 'string'
       AND agent_control.runtime_identifier_valid(p_command->>'lease_token')
$$;

CREATE FUNCTION agent_control.runtime_heartbeat_attempt_command_valid(p_command JSONB)
RETURNS BOOLEAN
LANGUAGE sql
STABLE
STRICT
AS $$
    SELECT agent_control.runtime_worker_command_valid(
               p_command,
               'heartbeat_attempt',
               ARRAY[
                   'schema_revision', 'envelope', 'attempt_id',
                   'expected_attempt_state_generation', 'lease_generation',
                   'lease_token', 'requested_extension_seconds'
               ]
           )
       AND jsonb_typeof(p_command->'attempt_id') = 'string'
       AND agent_control.runtime_identifier_valid(p_command->>'attempt_id')
       AND agent_control.runtime_positive_bigint_json(
               p_command->'expected_attempt_state_generation'
           )
       AND agent_control.runtime_positive_bigint_json(
               p_command->'lease_generation'
           )
       AND jsonb_typeof(p_command->'lease_token') = 'string'
       AND agent_control.runtime_identifier_valid(p_command->>'lease_token')
       AND agent_control.runtime_positive_bigint_json(
               p_command->'requested_extension_seconds'
           )
$$;

-- Insert-first locking linearizes an absent idempotency identity without an
-- advisory-lock namespace.  ON CONFLICT waits for a concurrent owner, after
-- which the exact stored response is returned or the changed body conflicts.
CREATE FUNCTION agent_control.runtime_begin_command(p_command JSONB)
RETURNS agent_control.runtime_command
LANGUAGE plpgsql
VOLATILE
STRICT
AS $$
DECLARE
    command_row agent_control.runtime_command%ROWTYPE;
    envelope JSONB := p_command->'envelope';
    principal TEXT := envelope #>> '{actor,principal_id}';
    command_kind TEXT := envelope->>'command_type';
    idem_key TEXT := envelope->>'idempotency_key';
    -- CommandID identifies one delivery attempt, not the idempotent logical
    -- request. CompareReplay permits a fresh CommandID on an exact retry; the
    -- first durable command row and its response remain the replay result.
    fingerprint CHAR(64) := agent_control.runtime_sha256_json(
        jsonb_set(
            p_command,
            '{envelope}',
            (p_command->'envelope') - 'command_id',
            false
        )
    );
    deadline TIMESTAMPTZ := (envelope->>'deadline')::TIMESTAMPTZ;
    now_at TIMESTAMPTZ := clock_timestamp();
    denied JSONB;
BEGIN
    INSERT INTO agent_control.runtime_command (
        principal_id, command_type, idempotency_key, command_id,
        schema_revision, actor_kind, actor_audience, command_audience,
        request_digest, body_fingerprint, causation_id, correlation_id,
        deadline_at, state, created_at
    ) VALUES (
        principal, command_kind, idem_key, envelope->>'command_id',
        1, 'workload', 'worker', 'control_api',
        envelope->>'request_digest', fingerprint,
        envelope->>'causation_id', envelope->>'correlation_id',
        deadline, 'processing', now_at
    ) ON CONFLICT DO NOTHING;

    SELECT * INTO command_row
    FROM agent_control.runtime_command
    WHERE principal_id = principal
      AND command_type = command_kind
      AND idempotency_key = idem_key
    FOR UPDATE;

    IF NOT FOUND THEN
        RAISE EXCEPTION USING ERRCODE = '23505',
            MESSAGE = 'runtime command identity conflicts with an existing command id';
    END IF;
    IF command_row.command_id <> envelope->>'command_id'
       AND EXISTS (
           SELECT 1
           FROM agent_control.runtime_command AS conflicting_command
           WHERE conflicting_command.command_id = envelope->>'command_id'
             AND (
                 conflicting_command.principal_id,
                 conflicting_command.command_type,
                 conflicting_command.idempotency_key
             ) IS DISTINCT FROM (
                 command_row.principal_id,
                 command_row.command_type,
                 command_row.idempotency_key
             )
       ) THEN
        RAISE EXCEPTION USING ERRCODE = '23505',
            MESSAGE = 'retry command id conflicts with another durable command';
    END IF;
    IF command_row.request_digest::TEXT <> envelope->>'request_digest'
       OR command_row.body_fingerprint <> fingerprint
       OR command_row.causation_id <> envelope->>'causation_id'
       OR command_row.correlation_id <> envelope->>'correlation_id'
       OR command_row.deadline_at <> deadline THEN
        RAISE EXCEPTION USING ERRCODE = '23505',
            MESSAGE = 'runtime command idempotency identity reused with a different body';
    END IF;
    IF command_row.state IN ('committed', 'denied') THEN
        RETURN command_row;
    END IF;

    IF deadline <= clock_timestamp() THEN
        denied := jsonb_build_object(
            'schema_revision', 1,
            'status', 'denied',
            'command_id', command_row.command_id,
            'command_type', command_row.command_type,
            'reason_code', 'command_deadline_expired'
        );
        UPDATE agent_control.runtime_command
        SET state = 'denied', response = denied,
            response_digest = agent_control.runtime_sha256_json(denied),
            committed_at = greatest(clock_timestamp(), command_row.created_at)
        WHERE principal_id = principal
          AND command_type = command_kind
          AND idempotency_key = idem_key
        RETURNING * INTO command_row;
    END IF;
    RETURN command_row;
END
$$;

CREATE FUNCTION agent_control.runtime_finish_command(
    p_command agent_control.runtime_command,
    p_state TEXT,
    p_response JSONB
) RETURNS JSONB
LANGUAGE plpgsql
VOLATILE
STRICT
AS $$
DECLARE
    returned_response JSONB;
BEGIN
    IF p_state NOT IN ('committed', 'denied')
       OR jsonb_typeof(p_response) <> 'object' THEN
        RAISE EXCEPTION USING ERRCODE = '22023',
            MESSAGE = 'invalid runtime command completion';
    END IF;
    UPDATE agent_control.runtime_command
    SET state = p_state,
        response = p_response,
        response_digest = agent_control.runtime_sha256_json(p_response),
        committed_at = greatest(clock_timestamp(), created_at)
    WHERE principal_id = p_command.principal_id
      AND command_type = p_command.command_type
      AND idempotency_key = p_command.idempotency_key
      AND state = 'processing'
    RETURNING response INTO returned_response;
    IF NOT FOUND THEN
        RAISE EXCEPTION USING ERRCODE = '40001',
            MESSAGE = 'runtime command was not processing at completion';
    END IF;
    RETURN returned_response;
END
$$;

CREATE FUNCTION agent_control.runtime_deny_command(
    p_command agent_control.runtime_command,
    p_reason_code TEXT
) RETURNS JSONB
LANGUAGE plpgsql
VOLATILE
STRICT
AS $$
DECLARE
    response JSONB;
BEGIN
    IF NOT agent_control.runtime_name_valid(p_reason_code) THEN
        RAISE EXCEPTION USING ERRCODE = '22023', MESSAGE = 'invalid denial reason';
    END IF;
    response := jsonb_build_object(
        'schema_revision', 1,
        'status', 'denied',
        'command_id', p_command.command_id,
        'command_type', p_command.command_type,
        'reason_code', p_reason_code
    );
    RETURN agent_control.runtime_finish_command(p_command, 'denied', response);
END
$$;

-- The recursive list is resolved before any mutation, then every ledger is
-- locked root-to-leaf. This helper only locks and checks: admission can still
-- wait on active-definition locks, so charging here would leak a slot if a
-- later freshness recheck durably denied the command.
CREATE FUNCTION agent_control.runtime_lock_budget_ancestors(
    p_run_id TEXT,
    p_task_ledger_id TEXT,
    p_require_active_capacity BOOLEAN
) RETURNS BOOLEAN
LANGUAGE plpgsql
VOLATILE
STRICT
AS $$
DECLARE
    ledger_ids TEXT[];
    current_ledger_id TEXT;
    ledger_row agent_control.runtime_budget_ledger%ROWTYPE;
    root_seen BOOLEAN := false;
BEGIN
    WITH RECURSIVE chain AS (
        -- The Task's own ledger budgets only its descendants. Its active slot
        -- is owned by, and charged to, the parent ancestry.
        SELECT ledger.ledger_id, ledger.parent_ledger_id, 0 AS depth,
               ARRAY[ledger.ledger_id]::TEXT[] AS path
        FROM agent_control.runtime_budget_ledger AS ledger
        WHERE ledger.ledger_id = (
            SELECT task_ledger.parent_ledger_id
            FROM agent_control.runtime_budget_ledger AS task_ledger
            WHERE task_ledger.ledger_id = p_task_ledger_id
              AND task_ledger.scope = 'task'
        )
        UNION ALL
        SELECT parent.ledger_id, parent.parent_ledger_id, child.depth + 1,
               child.path || parent.ledger_id
        FROM chain AS child
        JOIN agent_control.runtime_budget_ledger AS parent
          ON parent.ledger_id = child.parent_ledger_id
        WHERE child.depth < 4096
          AND NOT parent.ledger_id = ANY(child.path)
    )
    SELECT array_agg(ledger_id ORDER BY depth DESC, ledger_id)
    INTO ledger_ids
    FROM chain;

    IF ledger_ids IS NULL OR cardinality(ledger_ids) < 1 THEN
        RETURN false;
    END IF;

    FOREACH current_ledger_id IN ARRAY ledger_ids LOOP
        SELECT * INTO STRICT ledger_row
        FROM agent_control.runtime_budget_ledger AS ledger
        WHERE ledger.ledger_id = current_ledger_id
        FOR UPDATE;
        IF ledger_row.scope = 'run'
           AND ledger_row.scope_id = p_run_id
           AND ledger_row.parent_ledger_id IS NULL THEN
            root_seen := true;
        END IF;
        IF (p_require_active_capacity AND (
                ledger_row.state <> 'open'
                OR ledger_row.consumed_active_tasks
                    >= ledger_row.limit_parallelism
                       - ledger_row.reserved_active_tasks
                OR ledger_row.consumed_active_tasks >= ledger_row.consumed_tasks
            ))
           OR (NOT p_require_active_capacity AND ledger_row.state = 'closed') THEN
            RETURN false;
        END IF;
    END LOOP;
    IF NOT root_seen THEN
        RETURN false;
    END IF;

    RETURN true;
END
$$;

CREATE FUNCTION agent_control.runtime_charge_budget_ancestors(
    p_run_id TEXT,
    p_task_ledger_id TEXT
) RETURNS BOOLEAN
LANGUAGE plpgsql
VOLATILE
STRICT
AS $$
DECLARE
    ledger_ids TEXT[];
    current_ledger_id TEXT;
    changed_count INTEGER;
BEGIN
    -- The caller already holds these exact rows in root-to-leaf order. Repeat
    -- the set derivation and the overflow-safe capacity predicate, but acquire
    -- no new kind of lock and perform no partial charge on failure.
    WITH RECURSIVE chain AS (
        SELECT ledger.ledger_id, ledger.parent_ledger_id, 0 AS depth,
               ARRAY[ledger.ledger_id]::TEXT[] AS path
        FROM agent_control.runtime_budget_ledger AS ledger
        WHERE ledger.ledger_id = (
            SELECT task_ledger.parent_ledger_id
            FROM agent_control.runtime_budget_ledger AS task_ledger
            WHERE task_ledger.ledger_id = p_task_ledger_id
              AND task_ledger.scope = 'task'
        )
        UNION ALL
        SELECT parent.ledger_id, parent.parent_ledger_id, child.depth + 1,
               child.path || parent.ledger_id
        FROM chain AS child
        JOIN agent_control.runtime_budget_ledger AS parent
          ON parent.ledger_id = child.parent_ledger_id
        WHERE child.depth < 4096
          AND NOT parent.ledger_id = ANY(child.path)
    )
    SELECT array_agg(ledger_id ORDER BY depth DESC, ledger_id)
    INTO ledger_ids
    FROM chain;

    IF ledger_ids IS NULL OR NOT EXISTS (
        SELECT 1
        FROM agent_control.runtime_budget_ledger AS ledger
        WHERE ledger.ledger_id = ANY(ledger_ids)
          AND ledger.scope = 'run'
          AND ledger.scope_id = p_run_id
          AND ledger.parent_ledger_id IS NULL
    ) OR EXISTS (
        SELECT 1
        FROM agent_control.runtime_budget_ledger AS ledger
        WHERE ledger.ledger_id = ANY(ledger_ids)
          AND (
              ledger.state <> 'open'
              OR ledger.consumed_active_tasks
                 >= ledger.limit_parallelism - ledger.reserved_active_tasks
              OR ledger.consumed_active_tasks >= ledger.consumed_tasks
          )
    ) THEN
        RETURN false;
    END IF;

    FOREACH current_ledger_id IN ARRAY ledger_ids LOOP
        UPDATE agent_control.runtime_budget_ledger AS ledger
        SET consumed_active_tasks = consumed_active_tasks + 1,
            generation = generation + 1,
            updated_at = greatest(clock_timestamp(), updated_at)
        WHERE ledger.ledger_id = current_ledger_id;
        GET DIAGNOSTICS changed_count = ROW_COUNT;
        IF changed_count <> 1 THEN
            RAISE EXCEPTION USING ERRCODE = '40001',
                MESSAGE = 'locked budget ancestry changed during charge';
        END IF;
    END LOOP;
    RETURN true;
END
$$;

-- New Task admission requires the exact still-active RuntimePolicy and
-- OwnerPolicy heads. Registered non-recovery triggers additionally require
-- the exact enabled TriggerRegistration head. The immutable Run/Occurrence
-- bindings were proven by 0005; this helper closes their currentness race with
-- row-share locks held through command commit.
CREATE FUNCTION agent_control.runtime_run_admission_current(p_run_id TEXT)
RETURNS BOOLEAN
LANGUAGE plpgsql
VOLATILE
STRICT
AS $$
DECLARE
    run_row agent_control.runtime_run%ROWTYPE;
    owner_policy_id TEXT;
    registration_id_value TEXT;
    registration_generation_value BIGINT;
    registration_digest_value CHAR(64);
    registration_enabled BOOLEAN;
    matches BOOLEAN;
BEGIN
    SELECT * INTO run_row
    FROM agent_control.runtime_run AS run
    WHERE run.run_id = p_run_id;
    IF NOT FOUND THEN
        RETURN false;
    END IF;

    SELECT EXISTS (
        SELECT 1
        FROM agent_control.runtime_policy_head AS head
        WHERE head.policy_id = run_row.runtime_policy_id
          AND head.generation = run_row.runtime_policy_generation
          AND head.record_digest = run_row.runtime_policy_digest
        FOR SHARE
    ) INTO matches;
    IF NOT matches THEN
        RETURN false;
    END IF;

    SELECT policy.policy_id INTO owner_policy_id
    FROM platform_governance.owner_policy_revision AS policy
    WHERE policy.revision_id = run_row.origin_owner_policy_record_id
      AND policy.generation = run_row.origin_owner_policy_generation
      AND policy.record_digest = run_row.origin_owner_policy_record_digest;
    IF NOT FOUND THEN
        RETURN false;
    END IF;
    SELECT EXISTS (
        SELECT 1
        FROM platform_governance.owner_policy_head AS head
        WHERE head.head_id = owner_policy_id
          AND head.generation = run_row.origin_owner_policy_generation
          AND head.revision_id = run_row.origin_owner_policy_record_id
          AND head.revision_digest = run_row.origin_owner_policy_record_digest
        FOR SHARE
    ) INTO matches;
    IF NOT matches THEN
        RETURN false;
    END IF;

    IF run_row.origin_kind IN (
        'schedule', 'kernel_event', 'external_event', 'system_maintenance'
    ) THEN
        SELECT occurrence.registration_id,
               occurrence.registration_generation,
               occurrence.registration_digest
        INTO registration_id_value, registration_generation_value,
             registration_digest_value
        FROM agent_control.trigger_occurrence AS occurrence
        WHERE occurrence.occurrence_id = run_row.occurrence_id
          AND occurrence.record_digest = run_row.occurrence_digest;
        IF NOT FOUND THEN
            RETURN false;
        END IF;

        SELECT registration.enabled INTO registration_enabled
        FROM agent_control.trigger_registration_head AS head
        JOIN agent_control.trigger_registration_revision AS registration
          ON registration.registration_id = head.registration_id
         AND registration.generation = head.generation
         AND registration.record_digest = head.record_digest
        WHERE head.registration_id = registration_id_value
          AND head.generation = registration_generation_value
          AND head.record_digest = registration_digest_value
        FOR SHARE OF head;
        IF NOT FOUND OR NOT registration_enabled THEN
            RETURN false;
        END IF;
    END IF;
    RETURN true;
END
$$;

CREATE FUNCTION agent_control.runtime_insert_event(
    p_subject TEXT,
    p_subject_id TEXT,
    p_from_state TEXT,
    p_to_state TEXT,
    p_generation BIGINT,
    p_principal_id TEXT,
    p_causation_id TEXT,
    p_correlation_id TEXT,
    p_reason_code TEXT,
    p_occurred_at TIMESTAMPTZ
) RETURNS TEXT
LANGUAGE plpgsql
VOLATILE
AS $$
DECLARE
    new_event_id TEXT := gen_random_uuid()::TEXT;
    event_body JSONB;
BEGIN
    event_body := jsonb_build_object(
        'schema_revision', 1,
        'event_id', new_event_id, 'subject', p_subject,
        'subject_id', p_subject_id, 'from_state', p_from_state,
        'to_state', p_to_state, 'generation', p_generation,
        'actor', jsonb_build_object(
            'principal_id', p_principal_id,
            'kind', 'workload',
            'audience', 'worker'
        ),
        'causation_id', p_causation_id,
        'correlation_id', p_correlation_id, 'reason_code', p_reason_code,
        'occurred_at', agent_control.runtime_utc_text(p_occurred_at)
    );
    IF p_from_state IS NULL THEN
        event_body := event_body - 'from_state';
    END IF;
    INSERT INTO agent_control.runtime_event (
        event_id, schema_revision, record_digest, subject, subject_id,
        from_state, to_state, generation, actor, causation_id,
        correlation_id, reason_code, occurred_at
    ) VALUES (
        new_event_id, 1, agent_control.runtime_contract_digest(
            'agent-platform.contract.runtime_event.v1', event_body
        ),
        p_subject, p_subject_id, p_from_state, p_to_state, p_generation,
        jsonb_build_object(
            'principal_id', p_principal_id,
            'kind', 'workload',
            'audience', 'worker'
        ),
        p_causation_id, p_correlation_id, p_reason_code, p_occurred_at
    );
    RETURN new_event_id;
END
$$;

CREATE FUNCTION agent_control.runtime_claim_task(p_command JSONB)
RETURNS JSONB
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, agent_control, platform_security
SET timezone = 'UTC'
AS $$
DECLARE
    command_row agent_control.runtime_command%ROWTYPE;
    run_row agent_control.runtime_run%ROWTYPE;
    task_row agent_control.runtime_task%ROWTYPE;
    session_row agent_control.runtime_session%ROWTYPE;
    attempt_row agent_control.runtime_attempt%ROWTYPE;
    policy_row agent_control.runtime_policy_revision%ROWTYPE;
    envelope JSONB;
    principal TEXT;
    task_run_id TEXT;
    requested_seconds BIGINT;
    expected_generation BIGINT;
    now_at TIMESTAMPTZ;
    new_expires_at TIMESTAMPTZ;
    new_attempt_id TEXT;
    new_lease_token UUID;
    new_ordinal BIGINT;
    lease_event_generation BIGINT;
    unknown_turn_id TEXT;
    response JSONB;
    charge_active_slot BOOLEAN;
    advance_run BOOLEAN := false;
    reclaim_candidate BOOLEAN := false;
    active_attempt_found BOOLEAN := false;
BEGIN
    IF p_command IS NULL
       OR NOT agent_control.runtime_claim_task_command_valid(p_command) THEN
        RAISE EXCEPTION USING ERRCODE = '22023',
            MESSAGE = 'invalid claim_task command';
    END IF;

    envelope := p_command->'envelope';
    principal := envelope #>> '{actor,principal_id}';
    requested_seconds := (p_command->>'requested_lease_seconds')::BIGINT;
    expected_generation :=
        (p_command->>'expected_task_state_generation')::BIGINT;

    -- Lock 1: durable command identity. Exact completed retries stop here.
    command_row := agent_control.runtime_begin_command(p_command);
    IF command_row.state IN ('committed', 'denied') THEN
        RETURN command_row.response;
    END IF;

    -- Reading the immutable Task id is not a lock acquisition. It lets every
    -- contender acquire the mutable rows in the canonical Run -> Task order.
    SELECT task.run_id INTO task_run_id
    FROM agent_control.runtime_task AS task
    WHERE task.task_id = p_command->>'task_id';
    IF NOT FOUND THEN
        RETURN agent_control.runtime_deny_command(command_row, 'task_not_found');
    END IF;

    -- Locks 2 and 3: Run, then Task. Deletion is forbidden by 0005, but the
    -- second lookup still binds Task to the Run selected above.
    SELECT * INTO run_row
    FROM agent_control.runtime_run AS run
    WHERE run.run_id = task_run_id
    FOR UPDATE;
    IF NOT FOUND THEN
        RETURN agent_control.runtime_deny_command(command_row, 'run_not_found');
    END IF;

    -- Lock the target and every prerequisite Task in stable task_id order.
    -- Opposite dependency graphs therefore cannot invert Task row locks.
    PERFORM task.task_id
    FROM agent_control.runtime_task AS task
    WHERE task.run_id = run_row.run_id
      AND (
          task.task_id = p_command->>'task_id'
          OR task.task_id IN (
              SELECT dependency.depends_on_task_id
              FROM agent_control.runtime_task_dependency AS dependency
              WHERE dependency.task_id = p_command->>'task_id'
                AND dependency.run_id = run_row.run_id
          )
      )
    ORDER BY task.task_id
    FOR UPDATE;

    SELECT * INTO task_row
    FROM agent_control.runtime_task AS task
    WHERE task.task_id = p_command->>'task_id'
      AND task.run_id = run_row.run_id;
    IF NOT FOUND THEN
        RETURN agent_control.runtime_deny_command(command_row, 'task_run_mismatch');
    END IF;

    now_at := clock_timestamp();
    IF clock_timestamp() >= command_row.deadline_at THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'command_deadline_expired'
        );
    END IF;
    -- 0005 deliberately leaves OriginalAuthority/OriginalEffect owner
    -- resolution to the command boundary. This first lease slice has no
    -- cross-owner resolver yet, so recovery admission fails closed instead of
    -- pretending ordinary head currentness proves recovery authority.
    IF run_row.origin_kind = 'system_recovery' THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'recovery_admission_unavailable'
        );
    END IF;
    IF run_row.state = 'queued' THEN
        IF task_row.task_id <> run_row.root_task_id OR task_row.depth <> 0 THEN
            RETURN agent_control.runtime_deny_command(
                command_row, 'run_root_not_started'
            );
        END IF;
        advance_run := true;
    ELSIF run_row.state = 'waiting' THEN
        advance_run := true;
    ELSIF run_row.state <> 'running' THEN
        RETURN agent_control.runtime_deny_command(command_row, 'run_not_running');
    END IF;
    IF now_at >= run_row.deadline_at OR now_at >= task_row.deadline_at THEN
        RETURN agent_control.runtime_deny_command(command_row, 'runtime_deadline_expired');
    END IF;
    IF task_row.state_generation <> expected_generation THEN
        RETURN agent_control.runtime_deny_command(command_row, 'stale_task_generation');
    END IF;
    IF task_row.session_id IS NULL THEN
        RETURN agent_control.runtime_deny_command(command_row, 'session_not_bound');
    END IF;

    -- Lock 4: Session.
    SELECT * INTO session_row
    FROM agent_control.runtime_session AS session
    WHERE session.session_id = task_row.session_id
      AND session.run_id = run_row.run_id
      AND session.task_id = task_row.task_id
    FOR UPDATE;
    IF NOT FOUND OR session_row.state <> 'open' THEN
        RETURN agent_control.runtime_deny_command(command_row, 'session_not_open');
    END IF;

    SELECT * INTO STRICT policy_row
    FROM agent_control.runtime_policy_revision AS policy
    WHERE policy.policy_id = run_row.runtime_policy_id
      AND policy.generation = run_row.runtime_policy_generation
      AND policy.record_digest = run_row.runtime_policy_digest;
    IF requested_seconds > policy_row.max_lease_seconds THEN
        RETURN agent_control.runtime_deny_command(command_row, 'lease_limit_exceeded');
    END IF;
    IF (
        SELECT count(*)
        FROM agent_control.runtime_task_dependency AS dependency
        WHERE dependency.task_id = task_row.task_id
          AND dependency.run_id = run_row.run_id
    ) > policy_row.max_dependencies THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'dependency_limit_exceeded'
        );
    END IF;
    IF EXISTS (
        SELECT 1
        FROM agent_control.runtime_task_dependency AS dependency
        JOIN agent_control.runtime_task AS prerequisite
          ON prerequisite.task_id = dependency.depends_on_task_id
         AND prerequisite.run_id = dependency.run_id
        WHERE dependency.task_id = task_row.task_id
          AND dependency.run_id = run_row.run_id
          AND (
              (dependency.requires_success
                  AND prerequisite.state <> 'succeeded')
              OR
              (NOT dependency.requires_success
                  AND NOT agent_control.runtime_terminal_state(
                      'task', prerequisite.state
                  ))
          )
    ) THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'dependencies_not_ready'
        );
    END IF;

    -- Lock 5: every prior Attempt in stable ordinal order. Task's row lock
    -- prevents another command function from allocating the same next ordinal.
    PERFORM attempt.attempt_id
    FROM agent_control.runtime_attempt AS attempt
    WHERE attempt.task_id = task_row.task_id
    ORDER BY attempt.ordinal, attempt.attempt_id
    FOR UPDATE;

    SELECT * INTO attempt_row
    FROM agent_control.runtime_attempt AS attempt
    WHERE attempt.task_id = task_row.task_id
      AND attempt.state IN ('leased', 'executing')
    ORDER BY attempt.ordinal DESC, attempt.attempt_id DESC
    LIMIT 1;
    active_attempt_found := FOUND;

    IF active_attempt_found
       AND attempt_row.lease_expires_at <= clock_timestamp() THEN
        SELECT turn.turn_id INTO unknown_turn_id
        FROM agent_control.runtime_turn AS turn
        WHERE turn.attempt_id = attempt_row.attempt_id
          AND turn.state = 'unknown'
          AND turn.reservation_held
        FOR UPDATE;
        reclaim_candidate := FOUND;
    END IF;

    IF task_row.state IN ('ready', 'waiting') AND NOT reclaim_candidate THEN
        IF active_attempt_found THEN
            RETURN agent_control.runtime_deny_command(
                command_row, 'task_already_leased'
            );
        END IF;

        charge_active_slot := NOT task_row.budget_slot_held;
        -- Lock 6+: the frozen budget ancestry, root first. A ready retry whose
        -- slot is already held does not pay the active-task charge twice.
        IF NOT agent_control.runtime_lock_budget_ancestors(
            run_row.run_id, task_row.budget_ledger_id, charge_active_slot
        ) THEN
            RETURN agent_control.runtime_deny_command(
                command_row, 'budget_unavailable'
            );
        END IF;

        IF NOT agent_control.runtime_run_admission_current(run_row.run_id) THEN
            RETURN agent_control.runtime_deny_command(
                command_row, 'runtime_authority_not_current'
            );
        END IF;

        now_at := clock_timestamp();
        IF now_at >= command_row.deadline_at
           OR now_at >= run_row.deadline_at OR now_at >= task_row.deadline_at THEN
            RETURN agent_control.runtime_deny_command(
                command_row, 'runtime_deadline_expired'
            );
        END IF;
        new_expires_at := least(
            now_at + requested_seconds * interval '1 second',
            run_row.deadline_at,
            task_row.deadline_at
        );
        IF new_expires_at <= now_at THEN
            RETURN agent_control.runtime_deny_command(
                command_row, 'lease_window_unavailable'
            );
        END IF;

        IF charge_active_slot AND NOT agent_control.runtime_charge_budget_ancestors(
            run_row.run_id, task_row.budget_ledger_id
        ) THEN
            RETURN agent_control.runtime_deny_command(
                command_row, 'budget_unavailable'
            );
        END IF;

        SELECT coalesce(max(attempt.ordinal), 0) + 1 INTO new_ordinal
        FROM agent_control.runtime_attempt AS attempt
        WHERE attempt.task_id = task_row.task_id;
        new_attempt_id := gen_random_uuid()::TEXT;
        new_lease_token := gen_random_uuid();

        INSERT INTO agent_control.runtime_attempt (
            attempt_id, schema_revision, run_id, task_id, session_id, ordinal,
            state, state_generation, lease_generation, lease_token,
            lease_worker, lease_claimed_at, lease_heartbeat_at,
            lease_expires_at, created_at, updated_at
        ) VALUES (
            new_attempt_id, 1, run_row.run_id, task_row.task_id,
            session_row.session_id, new_ordinal, 'leased', 1, 1,
            new_lease_token,
            jsonb_build_object(
                'principal_id', principal,
                'kind', 'workload',
                'audience', 'worker'
            ),
            now_at, now_at, new_expires_at, now_at, now_at
        );

        INSERT INTO agent_control.runtime_attempt_lease_event (
            event_id, schema_revision, attempt_id, event_generation,
            lease_generation, transition, worker_principal_id, lease_token,
            previous_expires_at, new_expires_at, actor, causation_id,
            correlation_id, occurred_at
        ) VALUES (
            gen_random_uuid()::TEXT, 1, new_attempt_id, 1, 1, 'claimed',
            principal, new_lease_token, NULL, new_expires_at,
            jsonb_build_object(
                'principal_id', principal,
                'kind', 'workload',
                'audience', 'worker'
            ),
            envelope->>'causation_id', envelope->>'correlation_id', now_at
        );

        IF advance_run THEN
            UPDATE agent_control.runtime_run
            SET state = 'running',
                state_generation = run_row.state_generation + 1,
                updated_at = greatest(now_at, run_row.updated_at)
            WHERE run_id = run_row.run_id;
            PERFORM agent_control.runtime_insert_event(
                'run', run_row.run_id, run_row.state, 'running',
                run_row.state_generation + 1, principal,
                envelope->>'causation_id', envelope->>'correlation_id',
                'run_started', now_at
            );
        END IF;

        UPDATE agent_control.runtime_task
        SET state = 'running',
            state_generation = task_row.state_generation + 1,
            budget_slot_held = true,
            updated_at = greatest(now_at, task_row.updated_at)
        WHERE task_id = task_row.task_id;

        PERFORM agent_control.runtime_insert_event(
            'attempt', new_attempt_id, NULL, 'leased', 1, principal,
            envelope->>'causation_id', envelope->>'correlation_id',
            'attempt_claimed', now_at
        );
        PERFORM agent_control.runtime_insert_event(
            'task', task_row.task_id, task_row.state, 'running',
            task_row.state_generation + 1, principal,
            envelope->>'causation_id', envelope->>'correlation_id',
            'task_claimed', now_at
        );

        response := jsonb_build_object(
            'schema_revision', 1,
            'status', 'committed',
            'command_id', command_row.command_id,
            'task_id', task_row.task_id,
            'attempt_id', new_attempt_id,
            'attempt_state', 'leased',
            'attempt_state_generation', 1,
            'lease_generation', 1,
            'lease_token', new_lease_token::TEXT,
            'lease_expires_at', to_char(
                new_expires_at AT TIME ZONE 'UTC',
                'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'
            ),
            'reclaimed', false
        );
        RETURN agent_control.runtime_finish_command(
            command_row, 'committed', response
        );
    END IF;

    -- A same-Attempt lease is the only legal exception to normal new-Attempt
    -- retry. It exists solely to reconcile one unresolved unknown model Turn;
    -- it never creates a new provider-call or idempotency identity.
    IF task_row.state NOT IN ('ready', 'waiting', 'running')
       OR NOT task_row.budget_slot_held
       OR attempt_row.attempt_id IS NULL OR NOT reclaim_candidate THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'task_not_claimable'
        );
    END IF;

    IF NOT agent_control.runtime_lock_budget_ancestors(
        run_row.run_id, task_row.budget_ledger_id, false
    ) THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'budget_unavailable'
        );
    END IF;

    now_at := clock_timestamp();
    IF now_at < attempt_row.lease_expires_at
       OR now_at >= command_row.deadline_at
       OR now_at >= run_row.deadline_at OR now_at >= task_row.deadline_at THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'reclaim_window_unavailable'
        );
    END IF;
    new_expires_at := least(
        now_at + requested_seconds * interval '1 second',
        run_row.deadline_at,
        task_row.deadline_at
    );
    IF new_expires_at <= now_at THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'lease_window_unavailable'
        );
    END IF;
    new_lease_token := gen_random_uuid();

    SELECT coalesce(max(event.event_generation), 0) + 1
    INTO lease_event_generation
    FROM agent_control.runtime_attempt_lease_event AS event
    WHERE event.attempt_id = attempt_row.attempt_id;

    UPDATE agent_control.runtime_attempt
    SET lease_generation = attempt_row.lease_generation + 1,
        lease_token = new_lease_token,
        lease_worker = jsonb_build_object(
            'principal_id', principal,
            'kind', 'workload',
            'audience', 'worker'
        ),
        lease_claimed_at = now_at,
        lease_heartbeat_at = now_at,
        lease_expires_at = new_expires_at,
        updated_at = greatest(now_at, attempt_row.updated_at)
    WHERE attempt_id = attempt_row.attempt_id;

    IF advance_run THEN
        UPDATE agent_control.runtime_run
        SET state = 'running',
            state_generation = run_row.state_generation + 1,
            updated_at = greatest(now_at, run_row.updated_at)
        WHERE run_id = run_row.run_id;
        PERFORM agent_control.runtime_insert_event(
            'run', run_row.run_id, run_row.state, 'running',
            run_row.state_generation + 1, principal,
            envelope->>'causation_id', envelope->>'correlation_id',
            'run_resumed', now_at
        );
    END IF;

    IF task_row.state <> 'running' THEN
        UPDATE agent_control.runtime_task
        SET state = 'running',
            state_generation = task_row.state_generation + 1,
            budget_slot_held = true,
            updated_at = greatest(now_at, task_row.updated_at)
        WHERE task_id = task_row.task_id;
        PERFORM agent_control.runtime_insert_event(
            'task', task_row.task_id, task_row.state, 'running',
            task_row.state_generation + 1, principal,
            envelope->>'causation_id', envelope->>'correlation_id',
            'task_reclaimed', now_at
        );
    END IF;

    INSERT INTO agent_control.runtime_attempt_lease_event (
        event_id, schema_revision, attempt_id, event_generation,
        lease_generation, transition, worker_principal_id, lease_token,
        previous_expires_at, new_expires_at, actor, causation_id,
        correlation_id, occurred_at
    ) VALUES (
        gen_random_uuid()::TEXT, 1, attempt_row.attempt_id,
        lease_event_generation, attempt_row.lease_generation + 1,
        'reclaimed', principal, new_lease_token, attempt_row.lease_expires_at,
        new_expires_at,
        jsonb_build_object(
            'principal_id', principal,
            'kind', 'workload',
            'audience', 'worker'
        ),
        envelope->>'causation_id', envelope->>'correlation_id', now_at
    );

    response := jsonb_build_object(
        'schema_revision', 1,
        'status', 'committed',
        'command_id', command_row.command_id,
        'task_id', task_row.task_id,
        'attempt_id', attempt_row.attempt_id,
        'attempt_state', attempt_row.state,
        'attempt_state_generation', attempt_row.state_generation,
        'lease_generation', attempt_row.lease_generation + 1,
        'lease_token', new_lease_token::TEXT,
        'lease_expires_at', to_char(
            new_expires_at AT TIME ZONE 'UTC',
            'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'
        ),
        'reclaimed', true,
        'unresolved_turn_id', unknown_turn_id
    );
    RETURN agent_control.runtime_finish_command(
        command_row, 'committed', response
    );
END
$$;

CREATE FUNCTION agent_control.runtime_start_attempt(p_command JSONB)
RETURNS JSONB
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, agent_control, platform_security
SET timezone = 'UTC'
AS $$
DECLARE
    command_row agent_control.runtime_command%ROWTYPE;
    run_row agent_control.runtime_run%ROWTYPE;
    task_row agent_control.runtime_task%ROWTYPE;
    session_row agent_control.runtime_session%ROWTYPE;
    attempt_row agent_control.runtime_attempt%ROWTYPE;
    envelope JSONB;
    principal TEXT;
    lookup_run_id TEXT;
    lookup_task_id TEXT;
    lookup_session_id TEXT;
    expected_generation BIGINT;
    expected_lease_generation BIGINT;
    now_at TIMESTAMPTZ;
    response JSONB;
BEGIN
    IF p_command IS NULL
       OR NOT agent_control.runtime_start_attempt_command_valid(p_command) THEN
        RAISE EXCEPTION USING ERRCODE = '22023',
            MESSAGE = 'invalid start_attempt command';
    END IF;
    envelope := p_command->'envelope';
    principal := envelope #>> '{actor,principal_id}';
    expected_generation :=
        (p_command->>'expected_attempt_state_generation')::BIGINT;
    expected_lease_generation := (p_command->>'lease_generation')::BIGINT;

    command_row := agent_control.runtime_begin_command(p_command);
    IF command_row.state IN ('committed', 'denied') THEN
        RETURN command_row.response;
    END IF;

    SELECT attempt.run_id, attempt.task_id, attempt.session_id
    INTO lookup_run_id, lookup_task_id, lookup_session_id
    FROM agent_control.runtime_attempt AS attempt
    WHERE attempt.attempt_id = p_command->>'attempt_id';
    IF NOT FOUND THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'attempt_not_found'
        );
    END IF;

    -- Canonical mutable-row order: Run -> Task -> Session -> Attempt ->
    -- budget ancestry. The first identity lookup above acquires no row lock.
    SELECT * INTO run_row
    FROM agent_control.runtime_run AS run
    WHERE run.run_id = lookup_run_id
    FOR UPDATE;
    IF NOT FOUND THEN
        RETURN agent_control.runtime_deny_command(command_row, 'run_not_found');
    END IF;

    SELECT * INTO task_row
    FROM agent_control.runtime_task AS task
    WHERE task.task_id = lookup_task_id
      AND task.run_id = run_row.run_id
    FOR UPDATE;
    IF NOT FOUND THEN
        RETURN agent_control.runtime_deny_command(command_row, 'task_not_found');
    END IF;

    SELECT * INTO session_row
    FROM agent_control.runtime_session AS session
    WHERE session.session_id = lookup_session_id
      AND session.run_id = run_row.run_id
      AND session.task_id = task_row.task_id
    FOR UPDATE;
    IF NOT FOUND THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'session_not_found'
        );
    END IF;

    SELECT * INTO attempt_row
    FROM agent_control.runtime_attempt AS attempt
    WHERE attempt.attempt_id = p_command->>'attempt_id'
      AND attempt.run_id = run_row.run_id
      AND attempt.task_id = task_row.task_id
      AND attempt.session_id = session_row.session_id
    FOR UPDATE;
    IF NOT FOUND THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'attempt_lineage_mismatch'
        );
    END IF;

    now_at := clock_timestamp();
    IF run_row.state <> 'running' OR task_row.state <> 'running'
       OR session_row.state <> 'open' THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'runtime_not_executable'
        );
    END IF;
    IF now_at >= command_row.deadline_at
       OR now_at >= run_row.deadline_at OR now_at >= task_row.deadline_at THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'runtime_deadline_expired'
        );
    END IF;
    IF attempt_row.state <> 'leased' THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'attempt_not_leased'
        );
    END IF;
    IF attempt_row.state_generation <> expected_generation THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'stale_attempt_generation'
        );
    END IF;
    IF attempt_row.lease_generation <> expected_lease_generation
       OR attempt_row.lease_token::TEXT <> p_command->>'lease_token'
       OR attempt_row.lease_worker #>> '{principal_id}' <> principal
       OR attempt_row.lease_worker #>> '{kind}' <> 'workload'
       OR attempt_row.lease_worker #>> '{audience}' <> 'worker' THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'stale_lease_fence'
        );
    END IF;
    IF now_at >= attempt_row.lease_expires_at THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'attempt_lease_expired'
        );
    END IF;

    UPDATE agent_control.runtime_attempt
    SET state = 'executing',
        state_generation = attempt_row.state_generation + 1,
        updated_at = greatest(now_at, attempt_row.updated_at)
    WHERE attempt_id = attempt_row.attempt_id;

    PERFORM agent_control.runtime_insert_event(
        'attempt', attempt_row.attempt_id, 'leased', 'executing',
        attempt_row.state_generation + 1, principal,
        envelope->>'causation_id', envelope->>'correlation_id',
        'attempt_started', now_at
    );

    response := jsonb_build_object(
        'schema_revision', 1,
        'status', 'committed',
        'command_id', command_row.command_id,
        'attempt_id', attempt_row.attempt_id,
        'attempt_state', 'executing',
        'attempt_state_generation', attempt_row.state_generation + 1,
        'lease_generation', attempt_row.lease_generation,
        'lease_token', attempt_row.lease_token::TEXT,
        'lease_expires_at', to_char(
            attempt_row.lease_expires_at AT TIME ZONE 'UTC',
            'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'
        )
    );
    RETURN agent_control.runtime_finish_command(
        command_row, 'committed', response
    );
END
$$;

CREATE FUNCTION agent_control.runtime_heartbeat_attempt(p_command JSONB)
RETURNS JSONB
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, agent_control, platform_security
SET timezone = 'UTC'
AS $$
DECLARE
    command_row agent_control.runtime_command%ROWTYPE;
    run_row agent_control.runtime_run%ROWTYPE;
    task_row agent_control.runtime_task%ROWTYPE;
    session_row agent_control.runtime_session%ROWTYPE;
    attempt_row agent_control.runtime_attempt%ROWTYPE;
    policy_row agent_control.runtime_policy_revision%ROWTYPE;
    envelope JSONB;
    principal TEXT;
    lookup_run_id TEXT;
    lookup_task_id TEXT;
    lookup_session_id TEXT;
    expected_generation BIGINT;
    expected_lease_generation BIGINT;
    requested_seconds BIGINT;
    now_at TIMESTAMPTZ;
    new_expires_at TIMESTAMPTZ;
    lease_event_generation BIGINT;
    response JSONB;
BEGIN
    IF p_command IS NULL
       OR NOT agent_control.runtime_heartbeat_attempt_command_valid(p_command) THEN
        RAISE EXCEPTION USING ERRCODE = '22023',
            MESSAGE = 'invalid heartbeat_attempt command';
    END IF;
    envelope := p_command->'envelope';
    principal := envelope #>> '{actor,principal_id}';
    expected_generation :=
        (p_command->>'expected_attempt_state_generation')::BIGINT;
    expected_lease_generation := (p_command->>'lease_generation')::BIGINT;
    requested_seconds := (p_command->>'requested_extension_seconds')::BIGINT;

    command_row := agent_control.runtime_begin_command(p_command);
    IF command_row.state IN ('committed', 'denied') THEN
        RETURN command_row.response;
    END IF;

    SELECT attempt.run_id, attempt.task_id, attempt.session_id
    INTO lookup_run_id, lookup_task_id, lookup_session_id
    FROM agent_control.runtime_attempt AS attempt
    WHERE attempt.attempt_id = p_command->>'attempt_id';
    IF NOT FOUND THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'attempt_not_found'
        );
    END IF;

    SELECT * INTO run_row
    FROM agent_control.runtime_run AS run
    WHERE run.run_id = lookup_run_id
    FOR UPDATE;
    IF NOT FOUND THEN
        RETURN agent_control.runtime_deny_command(command_row, 'run_not_found');
    END IF;

    SELECT * INTO task_row
    FROM agent_control.runtime_task AS task
    WHERE task.task_id = lookup_task_id
      AND task.run_id = run_row.run_id
    FOR UPDATE;
    IF NOT FOUND THEN
        RETURN agent_control.runtime_deny_command(command_row, 'task_not_found');
    END IF;

    SELECT * INTO session_row
    FROM agent_control.runtime_session AS session
    WHERE session.session_id = lookup_session_id
      AND session.run_id = run_row.run_id
      AND session.task_id = task_row.task_id
    FOR UPDATE;
    IF NOT FOUND THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'session_not_found'
        );
    END IF;

    SELECT * INTO attempt_row
    FROM agent_control.runtime_attempt AS attempt
    WHERE attempt.attempt_id = p_command->>'attempt_id'
      AND attempt.run_id = run_row.run_id
      AND attempt.task_id = task_row.task_id
      AND attempt.session_id = session_row.session_id
    FOR UPDATE;
    IF NOT FOUND THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'attempt_lineage_mismatch'
        );
    END IF;

    SELECT * INTO STRICT policy_row
    FROM agent_control.runtime_policy_revision AS policy
    WHERE policy.policy_id = run_row.runtime_policy_id
      AND policy.generation = run_row.runtime_policy_generation
      AND policy.record_digest = run_row.runtime_policy_digest;
    IF requested_seconds > policy_row.max_heartbeat_extension_seconds THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'heartbeat_limit_exceeded'
        );
    END IF;

    now_at := clock_timestamp();
    IF run_row.state <> 'running' OR task_row.state <> 'running'
       OR session_row.state <> 'open'
       OR attempt_row.state NOT IN ('leased', 'executing') THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'runtime_not_executable'
        );
    END IF;
    IF now_at >= command_row.deadline_at
       OR now_at >= run_row.deadline_at OR now_at >= task_row.deadline_at THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'runtime_deadline_expired'
        );
    END IF;
    IF attempt_row.state_generation <> expected_generation THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'stale_attempt_generation'
        );
    END IF;
    IF attempt_row.lease_generation <> expected_lease_generation
       OR attempt_row.lease_token::TEXT <> p_command->>'lease_token'
       OR attempt_row.lease_worker #>> '{principal_id}' <> principal
       OR attempt_row.lease_worker #>> '{kind}' <> 'workload'
       OR attempt_row.lease_worker #>> '{audience}' <> 'worker' THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'stale_lease_fence'
        );
    END IF;
    IF now_at >= attempt_row.lease_expires_at THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'attempt_lease_expired'
        );
    END IF;

    new_expires_at := least(
        now_at + requested_seconds * interval '1 second',
        run_row.deadline_at,
        task_row.deadline_at
    );
    IF new_expires_at <= attempt_row.lease_expires_at THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'heartbeat_window_unavailable'
        );
    END IF;

    SELECT coalesce(max(event.event_generation), 0) + 1
    INTO lease_event_generation
    FROM agent_control.runtime_attempt_lease_event AS event
    WHERE event.attempt_id = attempt_row.attempt_id;

    UPDATE agent_control.runtime_attempt
    SET lease_heartbeat_at = now_at,
        lease_expires_at = new_expires_at,
        updated_at = greatest(now_at, attempt_row.updated_at)
    WHERE attempt_id = attempt_row.attempt_id;

    INSERT INTO agent_control.runtime_attempt_lease_event (
        event_id, schema_revision, attempt_id, event_generation,
        lease_generation, transition, worker_principal_id, lease_token,
        previous_expires_at, new_expires_at, actor, causation_id,
        correlation_id, occurred_at
    ) VALUES (
        gen_random_uuid()::TEXT, 1, attempt_row.attempt_id,
        lease_event_generation, attempt_row.lease_generation, 'heartbeat',
        principal, attempt_row.lease_token, attempt_row.lease_expires_at,
        new_expires_at,
        jsonb_build_object(
            'principal_id', principal,
            'kind', 'workload',
            'audience', 'worker'
        ),
        envelope->>'causation_id', envelope->>'correlation_id', now_at
    );

    response := jsonb_build_object(
        'schema_revision', 1,
        'status', 'committed',
        'command_id', command_row.command_id,
        'attempt_id', attempt_row.attempt_id,
        'attempt_state', attempt_row.state,
        'attempt_state_generation', attempt_row.state_generation,
        'lease_generation', attempt_row.lease_generation,
        'lease_token', attempt_row.lease_token::TEXT,
        'lease_heartbeat_at', to_char(
            now_at AT TIME ZONE 'UTC',
            'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'
        ),
        'lease_expires_at', to_char(
            new_expires_at AT TIME ZONE 'UTC',
            'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'
        )
    );
    RETURN agent_control.runtime_finish_command(
        command_row, 'committed', response
    );
END
$$;

-- The only callable Worker surface accepts raw JSON text so lexical contract
-- violations cannot disappear in a client- or database-side JSONB conversion.
CREATE FUNCTION agent_control.claim_task(p_command TEXT)
RETURNS JSONB
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, agent_control, platform_security
SET timezone = 'UTC'
AS $$
DECLARE
    parsed JSONB := agent_control.runtime_parse_worker_command(p_command);
BEGIN
    IF parsed IS NULL THEN
        RAISE EXCEPTION USING ERRCODE = '22023',
            MESSAGE = 'invalid raw claim_task command';
    END IF;
    RETURN agent_control.runtime_claim_task(parsed);
END
$$;

CREATE FUNCTION agent_control.start_attempt(p_command TEXT)
RETURNS JSONB
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, agent_control, platform_security
SET timezone = 'UTC'
AS $$
DECLARE
    parsed JSONB := agent_control.runtime_parse_worker_command(p_command);
BEGIN
    IF parsed IS NULL THEN
        RAISE EXCEPTION USING ERRCODE = '22023',
            MESSAGE = 'invalid raw start_attempt command';
    END IF;
    RETURN agent_control.runtime_start_attempt(parsed);
END
$$;

CREATE FUNCTION agent_control.heartbeat_attempt(p_command TEXT)
RETURNS JSONB
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, agent_control, platform_security
SET timezone = 'UTC'
AS $$
DECLARE
    parsed JSONB := agent_control.runtime_parse_worker_command(p_command);
BEGIN
    IF parsed IS NULL THEN
        RAISE EXCEPTION USING ERRCODE = '22023',
            MESSAGE = 'invalid raw heartbeat_attempt command';
    END IF;
    RETURN agent_control.runtime_heartbeat_attempt(parsed);
END
$$;

-- All helper functions remain private to the migrator-owned definer body.
-- PUBLIC and every application role receive only the exact command grants
-- below; there is no table, Provider, Kernel, GRACE, Delegation, or effect
-- capability in this migration.
REVOKE ALL ON FUNCTION agent_control.runtime_sha256_json(JSONB) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.runtime_strict_json_valid(JSON) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.runtime_parse_worker_command(TEXT) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.runtime_canonical_json(JSONB) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.runtime_contract_digest(TEXT, JSONB) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.runtime_utc_text(TIMESTAMPTZ) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.runtime_positive_bigint_json(JSONB) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.runtime_utc_instant_json(JSONB) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.runtime_worker_command_valid(JSONB, TEXT, TEXT[]) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.runtime_claim_task_command_valid(JSONB) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.runtime_start_attempt_command_valid(JSONB) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.runtime_heartbeat_attempt_command_valid(JSONB) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.runtime_begin_command(JSONB) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.runtime_finish_command(agent_control.runtime_command, TEXT, JSONB) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.runtime_deny_command(agent_control.runtime_command, TEXT) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.runtime_lock_budget_ancestors(TEXT, TEXT, BOOLEAN) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.runtime_charge_budget_ancestors(TEXT, TEXT) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.runtime_run_admission_current(TEXT) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.runtime_insert_event(TEXT, TEXT, TEXT, TEXT, BIGINT, TEXT, TEXT, TEXT, TEXT, TIMESTAMPTZ) FROM PUBLIC;

REVOKE ALL ON FUNCTION agent_control.runtime_claim_task(JSONB) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.runtime_start_attempt(JSONB) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.runtime_heartbeat_attempt(JSONB) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.claim_task(TEXT) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.start_attempt(TEXT) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.heartbeat_attempt(TEXT) FROM PUBLIC;

GRANT USAGE ON SCHEMA agent_control TO alpheus_agent_worker;
GRANT EXECUTE ON FUNCTION agent_control.claim_task(TEXT)
    TO alpheus_agent_worker;
GRANT EXECUTE ON FUNCTION agent_control.start_attempt(TEXT)
    TO alpheus_agent_worker;
GRANT EXECUTE ON FUNCTION agent_control.heartbeat_attempt(TEXT)
    TO alpheus_agent_worker;

RESET ROLE;
