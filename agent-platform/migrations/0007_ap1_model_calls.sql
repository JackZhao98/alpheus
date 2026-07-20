SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

-- AP1 model calls are a three-command protocol.  Dispatch commits the exact
-- immutable call identity and worst-case reservation before any network call.
-- Resolve or mark-unknown can only act through the same live Attempt fence.
-- This migration performs no model, Tool, Kernel, Provider, broker, GRACE,
-- Delegation, operation, or other external effect.

-- Like the unresolved-Turn guard fixed in 0006, this deferred 0005 trigger
-- fires after the public SECURITY DEFINER statement has returned to the
-- default-deny Worker.  Let the migrator-owned trigger read only the invariant
-- rows it validates; it remains non-callable by every application role.
ALTER FUNCTION agent_control.validate_runtime_manifest_contract()
    SECURITY DEFINER;
ALTER FUNCTION agent_control.validate_runtime_manifest_contract()
    SET search_path = pg_catalog, agent_control;
REVOKE ALL ON FUNCTION agent_control.validate_runtime_manifest_contract()
    FROM PUBLIC;

-- One provider response may identify only one logical call.  The frozen
-- ModelCallResult shape remains unchanged; this private immutable registry
-- closes cross-call provider-request aliasing under concurrent resolution.
CREATE TABLE agent_control.runtime_model_provider_request (
    provider TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(provider)
    ),
    provider_request_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(provider_request_id)
    ),
    call_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(call_id)
    ),
    result_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(result_id)
    ),
    committed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (provider, provider_request_id),
    UNIQUE (call_id),
    UNIQUE (result_id),
    FOREIGN KEY (call_id)
        REFERENCES agent_control.runtime_model_call_manifest(call_id)
        DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (result_id)
        REFERENCES agent_control.runtime_model_call_result(result_id)
        DEFERRABLE INITIALLY DEFERRED
);

CREATE TRIGGER runtime_model_provider_request_immutable
BEFORE UPDATE OR DELETE ON agent_control.runtime_model_provider_request
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();

-- Arbitrary user-chosen unique identities need a lockable row before their
-- durable record exists. Keeping this private registry in PostgreSQL avoids
-- granting any Agent role access to the database-wide advisory-lock keyspace.
CREATE TABLE agent_control.runtime_model_identity_lock (
    identity_key TEXT PRIMARY KEY CHECK (
        octet_length(identity_key) BETWEEN 1 AND 512
        AND identity_key !~ '[[:cntrl:]]'
    ),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

CREATE TRIGGER runtime_model_identity_lock_immutable
BEFORE UPDATE OR DELETE ON agent_control.runtime_model_identity_lock
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();

CREATE FUNCTION agent_control.runtime_nonnegative_bigint_json(p_value JSONB)
RETURNS BOOLEAN
LANGUAGE plpgsql
IMMUTABLE
STRICT
AS $$
DECLARE
    parsed NUMERIC;
BEGIN
    IF jsonb_typeof(p_value) <> 'number'
       OR p_value::TEXT !~ '^(0|[1-9][0-9]*)$' THEN
        RETURN false;
    END IF;
    parsed := p_value::TEXT::NUMERIC;
    RETURN parsed <= 9223372036854775807;
EXCEPTION WHEN OTHERS THEN
    RETURN false;
END
$$;

CREATE FUNCTION agent_control.runtime_model_manifest_candidate_valid(
    p_value JSONB
) RETURNS BOOLEAN
LANGUAGE plpgsql
IMMUTABLE
STRICT
AS $$
BEGIN
    RETURN COALESCE(
        jsonb_typeof(p_value) = 'object'
        AND p_value ?& ARRAY[
            'call_id', 'idempotency_key', 'provider', 'model',
            'prompt_digest', 'context_manifest', 'output_contract_digest',
            'request_digest', 'max_output_tokens', 'reserved_input_tokens',
            'reserved_external_cost_micro_usd', 'timeout_ms',
            'temperature_micros'
        ]
        AND p_value - ARRAY[
            'call_id', 'idempotency_key', 'provider', 'model',
            'prompt_digest', 'context_manifest', 'output_contract_digest',
            'request_digest', 'max_output_tokens', 'reserved_input_tokens',
            'reserved_external_cost_micro_usd', 'timeout_ms',
            'temperature_micros'
        ] = '{}'::JSONB
        AND jsonb_typeof(p_value->'call_id') = 'string'
        AND agent_control.runtime_identifier_valid(p_value->>'call_id')
        AND jsonb_typeof(p_value->'idempotency_key') = 'string'
        AND agent_control.runtime_identifier_valid(p_value->>'idempotency_key')
        AND jsonb_typeof(p_value->'provider') = 'string'
        AND agent_control.runtime_identifier_valid(p_value->>'provider')
        AND jsonb_typeof(p_value->'model') = 'string'
        AND agent_control.runtime_identifier_valid(p_value->>'model')
        AND jsonb_typeof(p_value->'prompt_digest') = 'string'
        AND agent_control.runtime_digest_valid(p_value->>'prompt_digest')
        AND agent_control.runtime_blob_ref_valid(
            p_value->'context_manifest', 'context_manifest', ''
        )
        AND agent_control.runtime_utc_instant_json(
            p_value #> '{context_manifest,committed_at}'
        )
        AND jsonb_typeof(p_value->'output_contract_digest') = 'string'
        AND agent_control.runtime_digest_valid(
            p_value->>'output_contract_digest'
        )
        AND jsonb_typeof(p_value->'request_digest') = 'string'
        AND agent_control.runtime_digest_valid(p_value->>'request_digest')
        AND agent_control.runtime_positive_bigint_json(
            p_value->'max_output_tokens'
        )
        AND agent_control.runtime_nonnegative_bigint_json(
            p_value->'reserved_input_tokens'
        )
        AND agent_control.runtime_nonnegative_bigint_json(
            p_value->'reserved_external_cost_micro_usd'
        )
        AND agent_control.runtime_positive_bigint_json(p_value->'timeout_ms')
        AND agent_control.runtime_nonnegative_bigint_json(
            p_value->'temperature_micros'
        ), false
    );
EXCEPTION WHEN OTHERS THEN
    RETURN false;
END
$$;

CREATE FUNCTION agent_control.runtime_model_result_candidate_valid(
    p_value JSONB
) RETURNS BOOLEAN
LANGUAGE plpgsql
IMMUTABLE
STRICT
AS $$
BEGIN
    RETURN COALESCE(
        jsonb_typeof(p_value) = 'object'
        AND p_value ?& ARRAY[
            'call_id', 'request_digest', 'provider_request_id', 'output',
            'input_tokens', 'output_tokens', 'external_cost_micro_usd',
            'wall_time_ms', 'finish_reason'
        ]
        AND p_value - ARRAY[
            'call_id', 'request_digest', 'provider_request_id', 'output',
            'input_tokens', 'output_tokens', 'external_cost_micro_usd',
            'wall_time_ms', 'finish_reason'
        ] = '{}'::JSONB
        AND jsonb_typeof(p_value->'call_id') = 'string'
        AND agent_control.runtime_identifier_valid(p_value->>'call_id')
        AND jsonb_typeof(p_value->'request_digest') = 'string'
        AND agent_control.runtime_digest_valid(p_value->>'request_digest')
        AND jsonb_typeof(p_value->'provider_request_id') = 'string'
        AND agent_control.runtime_identifier_valid(
            p_value->>'provider_request_id'
        )
        AND agent_control.runtime_blob_ref_valid(
            p_value->'output', 'model_call_manifest', ''
        )
        AND agent_control.runtime_utc_instant_json(
            p_value #> '{output,committed_at}'
        )
        AND agent_control.runtime_nonnegative_bigint_json(
            p_value->'input_tokens'
        )
        AND agent_control.runtime_nonnegative_bigint_json(
            p_value->'output_tokens'
        )
        AND agent_control.runtime_nonnegative_bigint_json(
            p_value->'external_cost_micro_usd'
        )
        AND agent_control.runtime_nonnegative_bigint_json(
            p_value->'wall_time_ms'
        )
        AND jsonb_typeof(p_value->'finish_reason') = 'string'
        AND p_value->>'finish_reason' IN (
            'stop', 'tool_use', 'length', 'content_filter'
        ), false
    );
EXCEPTION WHEN OTHERS THEN
    RETURN false;
END
$$;

CREATE FUNCTION agent_control.runtime_dispatch_model_call_command_valid(
    p_command JSONB
) RETURNS BOOLEAN
LANGUAGE sql
STABLE
STRICT
AS $$
    SELECT agent_control.runtime_worker_command_valid(
               p_command,
               'dispatch_model_call',
               ARRAY[
                   'schema_revision', 'envelope', 'attempt_id',
                   'expected_attempt_state_generation', 'lease_generation',
                   'lease_token', 'turn_id', 'manifest'
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
       AND jsonb_typeof(p_command->'turn_id') = 'string'
       AND agent_control.runtime_identifier_valid(p_command->>'turn_id')
       AND agent_control.runtime_model_manifest_candidate_valid(
               p_command->'manifest'
           )
$$;

CREATE FUNCTION agent_control.runtime_resolve_model_call_command_valid(
    p_command JSONB
) RETURNS BOOLEAN
LANGUAGE plpgsql
STABLE
STRICT
AS $$
DECLARE
    outcome TEXT;
BEGIN
    IF jsonb_typeof(p_command->'outcome') <> 'string' THEN
        RETURN false;
    END IF;
    outcome := p_command->>'outcome';
    IF outcome = 'result_committed' THEN
        IF NOT agent_control.runtime_worker_command_valid(
            p_command,
            'resolve_model_call',
            ARRAY[
                'schema_revision', 'envelope', 'attempt_id',
                'expected_attempt_state_generation', 'lease_generation',
                'lease_token', 'turn_id', 'expected_turn_state_generation',
                'outcome', 'result'
            ]
        ) THEN
            RETURN false;
        END IF;
    ELSIF outcome = 'failed' THEN
        IF NOT agent_control.runtime_worker_command_valid(
            p_command,
            'resolve_model_call',
            ARRAY[
                'schema_revision', 'envelope', 'attempt_id',
                'expected_attempt_state_generation', 'lease_generation',
                'lease_token', 'turn_id', 'expected_turn_state_generation',
                'outcome', 'failure'
            ]
        ) THEN
            RETURN false;
        END IF;
    ELSE
        RETURN false;
    END IF;
    IF jsonb_typeof(p_command->'attempt_id') <> 'string'
       OR NOT agent_control.runtime_identifier_valid(p_command->>'attempt_id')
       OR NOT agent_control.runtime_positive_bigint_json(
           p_command->'expected_attempt_state_generation'
       )
       OR NOT agent_control.runtime_positive_bigint_json(
           p_command->'lease_generation'
       )
       OR jsonb_typeof(p_command->'lease_token') <> 'string'
       OR NOT agent_control.runtime_identifier_valid(p_command->>'lease_token')
       OR jsonb_typeof(p_command->'turn_id') <> 'string'
       OR NOT agent_control.runtime_identifier_valid(p_command->>'turn_id')
       OR NOT agent_control.runtime_positive_bigint_json(
           p_command->'expected_turn_state_generation'
       ) THEN
        RETURN false;
    END IF;
    IF outcome = 'result_committed' THEN
        RETURN agent_control.runtime_model_result_candidate_valid(
               p_command->'result'
           );
    ELSIF outcome = 'failed' THEN
        RETURN agent_control.runtime_failure_valid(p_command->'failure');
    END IF;
    RETURN false;
EXCEPTION WHEN OTHERS THEN
    RETURN false;
END
$$;

CREATE FUNCTION agent_control.runtime_mark_model_call_unknown_command_valid(
    p_command JSONB
) RETURNS BOOLEAN
LANGUAGE sql
STABLE
STRICT
AS $$
    SELECT agent_control.runtime_worker_command_valid(
               p_command,
               'mark_model_call_unknown',
               ARRAY[
                   'schema_revision', 'envelope', 'attempt_id',
                   'expected_attempt_state_generation', 'lease_generation',
                   'lease_token', 'turn_id',
                   'expected_turn_state_generation', 'failure'
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
       AND jsonb_typeof(p_command->'turn_id') = 'string'
       AND agent_control.runtime_identifier_valid(p_command->>'turn_id')
       AND agent_control.runtime_positive_bigint_json(
               p_command->'expected_turn_state_generation'
           )
       AND agent_control.runtime_failure_valid(p_command->'failure')
$$;

-- Validate the exact immutable BlobRef against committed bytes and one current
-- reference readable by the authenticated Worker.  The caller already proved
-- the JSON shape; this closes the shape-only gap without granting table reads.
CREATE FUNCTION agent_control.runtime_worker_blob_ref_current(
    p_blob JSONB,
    p_principal_id TEXT,
    p_reference_record_type TEXT,
    p_reference_record_id TEXT,
    p_reference_record_digest TEXT
) RETURNS BOOLEAN
LANGUAGE plpgsql
VOLATILE
STRICT
SET search_path = pg_catalog, agent_control, blob
AS $$
DECLARE
    selected_binding TEXT;
    selected_owner TEXT;
    selected_access TEXT;
BEGIN
    SELECT reference.binding_id, reference.owner_principal,
           reference.access_class
      INTO selected_binding, selected_owner, selected_access
      FROM blob.blob_object AS object
      JOIN blob.blob_content AS content
        ON content.content_digest = object.content_digest
      JOIN blob.blob_reference AS reference
        ON reference.blob_id = object.blob_id
     WHERE object.blob_id = (p_blob->>'blob_id')::UUID
       AND object.content_digest::TEXT = p_blob->>'content_digest'
       AND object.media_type = p_blob->>'media_type'
       AND object.size_bytes = (p_blob->>'size_bytes')::BIGINT
       AND object.origin_owner = p_blob #>> '{origin,owner}'
       AND object.origin_record_type = p_blob #>> '{origin,record_type}'
       AND object.origin_record_id = p_blob #>> '{origin,record_id}'
       AND object.origin_record_digest::TEXT
           = p_blob #>> '{origin,record_digest}'
       AND object.committed_at
           = (p_blob->>'committed_at')::TIMESTAMPTZ
       AND object.state = 'committed'
       AND content.state = 'committed'
       AND reference.state = 'active'
       AND reference.retention_until > clock_timestamp()
       AND (
           p_reference_record_type = ''
           OR (
               reference.reference_owner = 'agent_control'
               AND reference.reference_record_type = p_reference_record_type
               AND reference.reference_record_id = p_reference_record_id
               AND reference.reference_record_digest::TEXT
                   = p_reference_record_digest
           )
       )
       AND (
           reference.owner_principal = p_principal_id
           OR (
               reference.access_class = 'explicit'
               AND EXISTS (
                   SELECT 1
                     FROM blob.blob_acl AS acl
                    WHERE acl.binding_id = reference.binding_id
                      AND acl.principal_id = p_principal_id
                      AND acl.state = 'active'
               )
           )
       )
     ORDER BY (reference.owner_principal = p_principal_id) DESC,
              reference.binding_id
     LIMIT 1
     FOR SHARE OF object, content, reference;
    IF NOT FOUND THEN
        RETURN false;
    END IF;
    IF selected_owner <> p_principal_id THEN
        IF selected_access <> 'explicit' THEN
            RETURN false;
        END IF;
        PERFORM 1
          FROM blob.blob_acl AS acl
         WHERE acl.binding_id = selected_binding
           AND acl.principal_id = p_principal_id
           AND acl.state = 'active'
         FOR SHARE;
        IF NOT FOUND THEN
            RETURN false;
        END IF;
    END IF;
    RETURN true;
EXCEPTION WHEN OTHERS THEN
    RETURN false;
END
$$;

-- A Task ledger is a child-work allocation owned by that Task.  Work performed
-- by the Task itself is charged to its parent ledger and every ancestor.  This
-- is the same ancestry boundary used by 0006 claim/start accounting.
CREATE FUNCTION agent_control.runtime_lock_model_budget_ancestors(
    p_run_id TEXT,
    p_task_ledger_id TEXT,
    p_model_calls BIGINT,
    p_input_tokens BIGINT,
    p_output_tokens BIGINT,
    p_external_cost_micro_usd BIGINT,
    p_wall_time_ms BIGINT
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
        IF ledger_row.state <> 'open'
           OR p_model_calls > ledger_row.limit_model_calls
                - ledger_row.consumed_model_calls
                - ledger_row.reserved_model_calls
           OR p_input_tokens > ledger_row.limit_input_tokens
                - ledger_row.consumed_input_tokens
                - ledger_row.reserved_input_tokens
           OR p_output_tokens > ledger_row.limit_output_tokens
                - ledger_row.consumed_output_tokens
                - ledger_row.reserved_output_tokens
           OR p_external_cost_micro_usd
                > ledger_row.limit_external_cost_micro_usd
                  - ledger_row.consumed_external_cost_micro_usd
                  - ledger_row.reserved_external_cost_micro_usd
           OR p_wall_time_ms > ledger_row.limit_wall_time_ms
                - ledger_row.consumed_wall_time_ms
                - ledger_row.reserved_wall_time_ms THEN
            RETURN false;
        END IF;
    END LOOP;
    RETURN root_seen;
EXCEPTION WHEN NO_DATA_FOUND THEN
    RETURN false;
END
$$;

CREATE FUNCTION agent_control.runtime_reserve_model_budget_ancestors(
    p_run_id TEXT,
    p_task_ledger_id TEXT,
    p_model_calls BIGINT,
    p_input_tokens BIGINT,
    p_output_tokens BIGINT,
    p_external_cost_micro_usd BIGINT,
    p_wall_time_ms BIGINT,
    p_updated_at TIMESTAMPTZ
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
               OR p_model_calls > ledger.limit_model_calls
                    - ledger.consumed_model_calls
                    - ledger.reserved_model_calls
               OR p_input_tokens > ledger.limit_input_tokens
                    - ledger.consumed_input_tokens
                    - ledger.reserved_input_tokens
               OR p_output_tokens > ledger.limit_output_tokens
                    - ledger.consumed_output_tokens
                    - ledger.reserved_output_tokens
               OR p_external_cost_micro_usd
                    > ledger.limit_external_cost_micro_usd
                      - ledger.consumed_external_cost_micro_usd
                      - ledger.reserved_external_cost_micro_usd
               OR p_wall_time_ms > ledger.limit_wall_time_ms
                    - ledger.consumed_wall_time_ms
                    - ledger.reserved_wall_time_ms
           )
    ) THEN
        RETURN false;
    END IF;

    FOREACH current_ledger_id IN ARRAY ledger_ids LOOP
        UPDATE agent_control.runtime_budget_ledger AS ledger
           SET reserved_model_calls = reserved_model_calls + p_model_calls,
               reserved_input_tokens = reserved_input_tokens + p_input_tokens,
               reserved_output_tokens = reserved_output_tokens + p_output_tokens,
               reserved_external_cost_micro_usd
                   = reserved_external_cost_micro_usd
                     + p_external_cost_micro_usd,
               reserved_wall_time_ms = reserved_wall_time_ms + p_wall_time_ms,
               generation = generation + 1,
               updated_at = greatest(p_updated_at, updated_at)
         WHERE ledger.ledger_id = current_ledger_id;
        GET DIAGNOSTICS changed_count = ROW_COUNT;
        IF changed_count <> 1 THEN
            RAISE EXCEPTION USING ERRCODE = '40001',
                MESSAGE = 'locked model budget ancestry changed during reservation';
        END IF;
    END LOOP;
    RETURN true;
END
$$;

CREATE FUNCTION agent_control.runtime_settle_model_budget_ancestors(
    p_run_id TEXT,
    p_task_ledger_id TEXT,
    p_reserved_input_tokens BIGINT,
    p_reserved_output_tokens BIGINT,
    p_reserved_external_cost_micro_usd BIGINT,
    p_reserved_wall_time_ms BIGINT,
    p_consumed_input_tokens BIGINT,
    p_consumed_output_tokens BIGINT,
    p_consumed_external_cost_micro_usd BIGINT,
    p_consumed_wall_time_ms BIGINT,
    p_principal_id TEXT,
    p_causation_id TEXT,
    p_correlation_id TEXT,
    p_updated_at TIMESTAMPTZ
) RETURNS BOOLEAN
LANGUAGE plpgsql
VOLATILE
STRICT
AS $$
DECLARE
    ledger_ids TEXT[];
    current_ledger_id TEXT;
    ledger_row agent_control.runtime_budget_ledger%ROWTYPE;
    next_state TEXT;
    root_seen BOOLEAN := false;
BEGIN
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
        IF ledger_row.state = 'closed'
           OR ledger_row.reserved_model_calls < 1
           OR ledger_row.reserved_input_tokens < p_reserved_input_tokens
           OR ledger_row.reserved_output_tokens < p_reserved_output_tokens
           OR ledger_row.reserved_external_cost_micro_usd
                < p_reserved_external_cost_micro_usd
           OR ledger_row.reserved_wall_time_ms < p_reserved_wall_time_ms
           OR p_consumed_input_tokens
                > ledger_row.limit_input_tokens
                  - ledger_row.consumed_input_tokens
           OR p_consumed_output_tokens
                > ledger_row.limit_output_tokens
                  - ledger_row.consumed_output_tokens
           OR p_consumed_external_cost_micro_usd
                > ledger_row.limit_external_cost_micro_usd
                  - ledger_row.consumed_external_cost_micro_usd
           OR p_consumed_wall_time_ms
                > ledger_row.limit_wall_time_ms
                  - ledger_row.consumed_wall_time_ms
           OR ledger_row.consumed_model_calls
                >= ledger_row.limit_model_calls THEN
            RETURN false;
        END IF;
    END LOOP;
    IF NOT root_seen THEN
        RETURN false;
    END IF;

    FOREACH current_ledger_id IN ARRAY ledger_ids LOOP
        SELECT * INTO STRICT ledger_row
          FROM agent_control.runtime_budget_ledger AS ledger
         WHERE ledger.ledger_id = current_ledger_id;
        next_state := ledger_row.state;
        IF ledger_row.state = 'open' AND (
            (ledger_row.limit_model_calls > 0
             AND ledger_row.consumed_model_calls + 1
                 = ledger_row.limit_model_calls)
            OR (ledger_row.limit_input_tokens > 0
                AND ledger_row.consumed_input_tokens
                    + p_consumed_input_tokens
                    = ledger_row.limit_input_tokens)
            OR (ledger_row.limit_output_tokens > 0
                AND ledger_row.consumed_output_tokens
                    + p_consumed_output_tokens
                    = ledger_row.limit_output_tokens)
            OR (ledger_row.limit_external_cost_micro_usd > 0
                AND ledger_row.consumed_external_cost_micro_usd
                    + p_consumed_external_cost_micro_usd
                    = ledger_row.limit_external_cost_micro_usd)
            OR (ledger_row.limit_wall_time_ms > 0
                AND ledger_row.consumed_wall_time_ms
                    + p_consumed_wall_time_ms
                    = ledger_row.limit_wall_time_ms)
        ) THEN
            next_state := 'exhausted';
        END IF;

        UPDATE agent_control.runtime_budget_ledger AS ledger
           SET reserved_model_calls = reserved_model_calls - 1,
               reserved_input_tokens
                   = reserved_input_tokens - p_reserved_input_tokens,
               reserved_output_tokens
                   = reserved_output_tokens - p_reserved_output_tokens,
               reserved_external_cost_micro_usd
                   = reserved_external_cost_micro_usd
                     - p_reserved_external_cost_micro_usd,
               reserved_wall_time_ms
                   = reserved_wall_time_ms - p_reserved_wall_time_ms,
               consumed_model_calls = consumed_model_calls + 1,
               consumed_input_tokens
                   = consumed_input_tokens + p_consumed_input_tokens,
               consumed_output_tokens
                   = consumed_output_tokens + p_consumed_output_tokens,
               consumed_external_cost_micro_usd
                   = consumed_external_cost_micro_usd
                     + p_consumed_external_cost_micro_usd,
               consumed_wall_time_ms
                   = consumed_wall_time_ms + p_consumed_wall_time_ms,
               generation = generation + 1,
               state = next_state,
               updated_at = greatest(p_updated_at, updated_at)
         WHERE ledger.ledger_id = current_ledger_id;

        IF next_state <> ledger_row.state THEN
            PERFORM agent_control.runtime_insert_event(
                'budget', ledger_row.ledger_id, ledger_row.state, next_state,
                ledger_row.generation + 1, p_principal_id,
                p_causation_id, p_correlation_id,
                'budget_exhausted', p_updated_at
            );
        END IF;
    END LOOP;
    RETURN true;
EXCEPTION WHEN NO_DATA_FOUND THEN
    RETURN false;
END
$$;

-- Outcome commands must lock the already-held reservation before their final
-- fence check. This is separate from settlement so a lock wait cannot let an
-- expired Worker commit with a stale clock reading.
CREATE FUNCTION agent_control.runtime_lock_existing_model_reservation_ancestors(
    p_run_id TEXT,
    p_task_ledger_id TEXT,
    p_reserved_input_tokens BIGINT,
    p_reserved_output_tokens BIGINT,
    p_reserved_external_cost_micro_usd BIGINT,
    p_reserved_wall_time_ms BIGINT
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
        IF ledger_row.state = 'closed'
           OR ledger_row.reserved_model_calls < 1
           OR ledger_row.reserved_input_tokens < p_reserved_input_tokens
           OR ledger_row.reserved_output_tokens < p_reserved_output_tokens
           OR ledger_row.reserved_external_cost_micro_usd
                < p_reserved_external_cost_micro_usd
           OR ledger_row.reserved_wall_time_ms < p_reserved_wall_time_ms THEN
            RETURN false;
        END IF;
    END LOOP;
    RETURN root_seen;
EXCEPTION WHEN NO_DATA_FOUND THEN
    RETURN false;
END
$$;

-- User-chosen identities participate in global unique indexes. Acquiring each
-- private registry row in deterministic order moves any cross-Run wait before
-- the final lease/deadline fence. The insert race itself is intentional: it
-- waits here, before the fence, and leaves one reusable row for later calls.
CREATE FUNCTION agent_control.runtime_lock_model_identity_keys(p_keys TEXT[])
RETURNS VOID
LANGUAGE plpgsql
VOLATILE
STRICT
AS $$
DECLARE
    current_key TEXT;
BEGIN
    IF cardinality(p_keys) < 1 OR array_position(p_keys, NULL) IS NOT NULL THEN
        RAISE EXCEPTION USING ERRCODE = '22023',
            MESSAGE = 'invalid model identity lock set';
    END IF;
    FOR current_key IN
        SELECT DISTINCT identity_key
          FROM unnest(p_keys) AS identity(identity_key)
         ORDER BY identity_key
    LOOP
        INSERT INTO agent_control.runtime_model_identity_lock (identity_key)
        VALUES (current_key)
        ON CONFLICT (identity_key) DO NOTHING;
        PERFORM 1
          FROM agent_control.runtime_model_identity_lock AS identity
         WHERE identity.identity_key = current_key
         FOR UPDATE;
    END LOOP;
END
$$;

CREATE FUNCTION agent_control.runtime_dispatch_model_call(p_command JSONB)
RETURNS JSONB
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, agent_control, platform_security, blob
SET timezone = 'UTC'
AS $$
DECLARE
    command_row agent_control.runtime_command%ROWTYPE;
    run_row agent_control.runtime_run%ROWTYPE;
    task_row agent_control.runtime_task%ROWTYPE;
    session_row agent_control.runtime_session%ROWTYPE;
    attempt_row agent_control.runtime_attempt%ROWTYPE;
    envelope JSONB;
    manifest JSONB;
    principal TEXT;
    lookup_run_id TEXT;
    lookup_task_id TEXT;
    lookup_session_id TEXT;
    expected_attempt_generation BIGINT;
    expected_lease_generation BIGINT;
    max_output_tokens BIGINT;
    reserved_input_tokens BIGINT;
    reserved_external_cost BIGINT;
    timeout_ms BIGINT;
    next_ordinal BIGINT;
    now_at TIMESTAMPTZ;
    manifest_body JSONB;
    manifest_digest CHAR(64);
    response JSONB;
BEGIN
    IF p_command IS NULL
       OR NOT agent_control.runtime_dispatch_model_call_command_valid(
           p_command
       ) THEN
        RAISE EXCEPTION USING ERRCODE = '22023',
            MESSAGE = 'invalid dispatch_model_call command';
    END IF;
    envelope := p_command->'envelope';
    manifest := p_command->'manifest';
    principal := envelope #>> '{actor,principal_id}';
    expected_attempt_generation :=
        (p_command->>'expected_attempt_state_generation')::BIGINT;
    expected_lease_generation :=
        (p_command->>'lease_generation')::BIGINT;
    max_output_tokens := (manifest->>'max_output_tokens')::BIGINT;
    reserved_input_tokens := (manifest->>'reserved_input_tokens')::BIGINT;
    reserved_external_cost :=
        (manifest->>'reserved_external_cost_micro_usd')::BIGINT;
    timeout_ms := (manifest->>'timeout_ms')::BIGINT;

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
    IF attempt_row.state <> 'executing' THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'attempt_not_executing'
        );
    END IF;
    IF attempt_row.state_generation <> expected_attempt_generation THEN
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
    IF timeout_ms::NUMERIC
       > extract(epoch FROM (
           least(
               attempt_row.lease_expires_at,
               run_row.deadline_at,
               task_row.deadline_at
           ) - now_at
       ))::NUMERIC * 1000 THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'model_call_window_unavailable'
        );
    END IF;
    IF manifest->>'output_contract_digest'
       <> task_row.output_contract_digest::TEXT THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'output_contract_mismatch'
        );
    END IF;
    IF manifest->'context_manifest' IS DISTINCT FROM session_row.context_manifest
       OR NOT agent_control.runtime_worker_blob_ref_current(
           manifest->'context_manifest', principal,
           manifest #>> '{context_manifest,origin,record_type}',
           manifest #>> '{context_manifest,origin,record_id}',
           manifest #>> '{context_manifest,origin,record_digest}'
       ) THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'context_manifest_unavailable'
        );
    END IF;
    IF EXISTS (
        SELECT 1
          FROM agent_control.runtime_turn AS turn
         WHERE turn.attempt_id = attempt_row.attempt_id
           AND turn.state IN ('planned', 'dispatched', 'unknown')
         FOR UPDATE
    ) THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'unresolved_turn_exists'
        );
    END IF;

    IF NOT agent_control.runtime_lock_model_budget_ancestors(
        run_row.run_id, task_row.budget_ledger_id,
        1, reserved_input_tokens, max_output_tokens,
        reserved_external_cost, timeout_ms
    ) THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'model_budget_unavailable'
        );
    END IF;
    IF NOT agent_control.runtime_run_admission_current(run_row.run_id) THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'runtime_authority_not_current'
        );
    END IF;
    PERFORM agent_control.runtime_lock_model_identity_keys(ARRAY[
        'call:' || (manifest->>'call_id'),
        'provider-idempotency:' || (manifest->>'provider') || ':'
            || (manifest->>'idempotency_key'),
        'turn:' || (p_command->>'turn_id')
    ]::TEXT[]);

    -- Every potentially blocking admission lock is now held.  Re-read
    -- database time, fence, deadlines, and Blob reference freshness before
    -- creating any Turn or reservation.
    SELECT * INTO attempt_row
      FROM agent_control.runtime_attempt AS attempt
     WHERE attempt.attempt_id = p_command->>'attempt_id'
     FOR UPDATE;
    now_at := clock_timestamp();
    IF now_at >= command_row.deadline_at
       OR now_at >= run_row.deadline_at OR now_at >= task_row.deadline_at
       OR now_at >= attempt_row.lease_expires_at
       OR attempt_row.state <> 'executing'
       OR attempt_row.state_generation <> expected_attempt_generation
       OR attempt_row.lease_generation <> expected_lease_generation
       OR attempt_row.lease_token::TEXT <> p_command->>'lease_token'
       OR attempt_row.lease_worker #>> '{principal_id}' <> principal THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'dispatch_fence_expired'
        );
    END IF;
    IF timeout_ms::NUMERIC
       > extract(epoch FROM (
           least(
               attempt_row.lease_expires_at,
               run_row.deadline_at,
               task_row.deadline_at
           ) - now_at
       ))::NUMERIC * 1000 THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'dispatch_window_unavailable'
        );
    END IF;
    IF NOT agent_control.runtime_worker_blob_ref_current(
        manifest->'context_manifest', principal,
        manifest #>> '{context_manifest,origin,record_type}',
        manifest #>> '{context_manifest,origin,record_id}',
        manifest #>> '{context_manifest,origin,record_digest}'
    ) THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'dispatch_window_unavailable'
        );
    END IF;

    -- Blob validation may itself wait on a concurrent metadata transaction.
    -- Never reuse the clock sampled before that wait to authorize dispatch.
    now_at := clock_timestamp();
    IF now_at >= command_row.deadline_at
       OR now_at >= run_row.deadline_at OR now_at >= task_row.deadline_at
       OR now_at >= attempt_row.lease_expires_at
       OR attempt_row.state <> 'executing'
       OR attempt_row.state_generation <> expected_attempt_generation
       OR attempt_row.lease_generation <> expected_lease_generation
       OR attempt_row.lease_token::TEXT <> p_command->>'lease_token'
       OR attempt_row.lease_worker #>> '{principal_id}' <> principal
       OR attempt_row.lease_worker #>> '{kind}' <> 'workload'
       OR attempt_row.lease_worker #>> '{audience}' <> 'worker'
       OR timeout_ms::NUMERIC
            > extract(epoch FROM (
                least(
                    attempt_row.lease_expires_at,
                    run_row.deadline_at,
                    task_row.deadline_at
                ) - now_at
            ))::NUMERIC * 1000 THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'dispatch_fence_expired'
        );
    END IF;

    SELECT coalesce(max(turn.ordinal), 0) + 1
      INTO next_ordinal
      FROM agent_control.runtime_turn AS turn
     WHERE turn.attempt_id = attempt_row.attempt_id;

    BEGIN
        INSERT INTO agent_control.runtime_turn (
            turn_id, schema_revision, run_id, task_id, session_id, attempt_id,
            ordinal, kind, state, state_generation, request_digest,
            reservation_held, created_at, updated_at
        ) VALUES (
            p_command->>'turn_id', 1, run_row.run_id, task_row.task_id,
            session_row.session_id, attempt_row.attempt_id, next_ordinal,
            'model_call', 'planned', 1, manifest->>'request_digest', false,
            now_at, now_at
        );
        PERFORM agent_control.runtime_insert_event(
            'turn', p_command->>'turn_id', NULL, 'planned', 1, principal,
            envelope->>'causation_id', envelope->>'correlation_id',
            'model_call_planned', now_at
        );

        manifest_body := jsonb_build_object(
            'schema_revision', 1,
            'call_id', manifest->>'call_id',
            'turn_id', p_command->>'turn_id',
            'attempt_id', attempt_row.attempt_id,
            'idempotency_key', manifest->>'idempotency_key',
            'provider', manifest->>'provider',
            'model', manifest->>'model',
            'prompt_digest', manifest->>'prompt_digest',
            'context_manifest', manifest->'context_manifest',
            'output_contract_digest', manifest->>'output_contract_digest',
            'request_digest', manifest->>'request_digest',
            'max_output_tokens', max_output_tokens,
            'reserved_input_tokens', reserved_input_tokens,
            'reserved_external_cost_micro_usd', reserved_external_cost,
            'timeout_ms', timeout_ms,
            'temperature_micros',
                (manifest->>'temperature_micros')::BIGINT,
            'created_at', agent_control.runtime_utc_text(now_at)
        );
        manifest_digest := agent_control.runtime_contract_digest(
            'agent-platform.contract.model_call_manifest.v1', manifest_body
        );
        INSERT INTO agent_control.runtime_model_call_manifest (
            call_id, schema_revision, record_digest, turn_id, attempt_id,
            idempotency_key, provider, model, prompt_digest, context_manifest,
            output_contract_digest, request_digest, max_output_tokens,
            reserved_input_tokens, reserved_external_cost_micro_usd,
            timeout_ms, temperature_micros, created_at
        ) VALUES (
            manifest->>'call_id', 1, manifest_digest,
            p_command->>'turn_id', attempt_row.attempt_id,
            manifest->>'idempotency_key', manifest->>'provider',
            manifest->>'model', manifest->>'prompt_digest',
            manifest->'context_manifest', manifest->>'output_contract_digest',
            manifest->>'request_digest', max_output_tokens,
            reserved_input_tokens, reserved_external_cost, timeout_ms,
            (manifest->>'temperature_micros')::BIGINT, now_at
        );

        IF NOT agent_control.runtime_reserve_model_budget_ancestors(
            run_row.run_id, task_row.budget_ledger_id,
            1, reserved_input_tokens, max_output_tokens,
            reserved_external_cost, timeout_ms, now_at
        ) THEN
            RAISE EXCEPTION USING ERRCODE = '40001',
                MESSAGE = 'model budget changed during reservation';
        END IF;

        UPDATE agent_control.runtime_turn
           SET state = 'dispatched', state_generation = 2,
               reservation_held = true, dispatched_at = now_at,
               updated_at = now_at
         WHERE turn_id = p_command->>'turn_id';
        PERFORM agent_control.runtime_insert_event(
            'turn', p_command->>'turn_id', 'planned', 'dispatched', 2,
            principal, envelope->>'causation_id',
            envelope->>'correlation_id', 'model_call_dispatched', now_at
        );
    EXCEPTION
        WHEN unique_violation THEN
            RETURN agent_control.runtime_deny_command(
                command_row, 'model_call_identity_conflict'
            );
        WHEN serialization_failure THEN
            RETURN agent_control.runtime_deny_command(
                command_row, 'model_budget_changed'
            );
    END;

    response := jsonb_build_object(
        'schema_revision', 1,
        'status', 'committed',
        'command_id', command_row.command_id,
        'attempt_id', attempt_row.attempt_id,
        'turn_id', p_command->>'turn_id',
        'turn_state', 'dispatched',
        'turn_state_generation', 2,
        'call_id', manifest->>'call_id',
        'manifest_digest', manifest_digest::TEXT,
        'reservation', jsonb_build_object(
            'model_calls', 1,
            'input_tokens', reserved_input_tokens,
            'output_tokens', max_output_tokens,
            'external_cost_micro_usd', reserved_external_cost,
            'wall_time_ms', timeout_ms
        )
    );
    RETURN agent_control.runtime_finish_command(
        command_row, 'committed', response
    );
END
$$;

CREATE FUNCTION agent_control.runtime_resolve_model_call(p_command JSONB)
RETURNS JSONB
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, agent_control, platform_security, blob
SET timezone = 'UTC'
AS $$
DECLARE
    command_row agent_control.runtime_command%ROWTYPE;
    run_row agent_control.runtime_run%ROWTYPE;
    task_row agent_control.runtime_task%ROWTYPE;
    session_row agent_control.runtime_session%ROWTYPE;
    attempt_row agent_control.runtime_attempt%ROWTYPE;
    turn_row agent_control.runtime_turn%ROWTYPE;
    manifest_row agent_control.runtime_model_call_manifest%ROWTYPE;
    envelope JSONB;
    result_candidate JSONB;
    principal TEXT;
    lookup_run_id TEXT;
    lookup_task_id TEXT;
    lookup_session_id TEXT;
    expected_attempt_generation BIGINT;
    expected_lease_generation BIGINT;
    expected_turn_generation BIGINT;
    outcome TEXT;
    now_at TIMESTAMPTZ;
    consumed_input BIGINT;
    consumed_output BIGINT;
    consumed_cost BIGINT;
    consumed_wall BIGINT;
    new_result_id TEXT;
    result_body JSONB;
    new_result_digest CHAR(64);
    response JSONB;
BEGIN
    IF p_command IS NULL
       OR NOT agent_control.runtime_resolve_model_call_command_valid(
           p_command
       ) THEN
        RAISE EXCEPTION USING ERRCODE = '22023',
            MESSAGE = 'invalid resolve_model_call command';
    END IF;
    envelope := p_command->'envelope';
    principal := envelope #>> '{actor,principal_id}';
    expected_attempt_generation :=
        (p_command->>'expected_attempt_state_generation')::BIGINT;
    expected_lease_generation :=
        (p_command->>'lease_generation')::BIGINT;
    expected_turn_generation :=
        (p_command->>'expected_turn_state_generation')::BIGINT;
    outcome := p_command->>'outcome';

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
    SELECT * INTO turn_row
      FROM agent_control.runtime_turn AS turn
     WHERE turn.turn_id = p_command->>'turn_id'
       AND turn.attempt_id = attempt_row.attempt_id
       AND turn.run_id = run_row.run_id
       AND turn.task_id = task_row.task_id
       AND turn.session_id = session_row.session_id
     FOR UPDATE;
    IF NOT FOUND THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'turn_not_found'
        );
    END IF;

    now_at := clock_timestamp();
    IF run_row.state <> 'running' OR task_row.state <> 'running'
       OR session_row.state <> 'open' OR attempt_row.state <> 'executing' THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'runtime_not_executable'
        );
    END IF;
    IF now_at >= command_row.deadline_at THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'command_deadline_expired'
        );
    END IF;
    IF attempt_row.state_generation <> expected_attempt_generation THEN
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
    IF turn_row.state NOT IN ('dispatched', 'unknown')
       OR turn_row.state_generation <> expected_turn_generation
       OR NOT turn_row.reservation_held THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'stale_turn_generation'
        );
    END IF;

    SELECT * INTO manifest_row
      FROM agent_control.runtime_model_call_manifest AS manifest
     WHERE manifest.turn_id = turn_row.turn_id
       AND manifest.attempt_id = attempt_row.attempt_id
       AND manifest.request_digest = turn_row.request_digest
     FOR SHARE;
    IF NOT FOUND THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'model_manifest_not_found'
        );
    END IF;

    IF outcome = 'result_committed' THEN
        result_candidate := p_command->'result';
        IF result_candidate->>'call_id' <> manifest_row.call_id
           OR result_candidate->>'request_digest'
                <> manifest_row.request_digest::TEXT
           OR result_candidate #>> '{output,origin,owner}'
                <> 'agent_control'
           OR result_candidate #>> '{output,origin,record_type}'
                <> 'model_call_manifest'
           OR result_candidate #>> '{output,origin,record_id}'
                <> manifest_row.call_id
           OR result_candidate #>> '{output,origin,record_digest}'
                <> manifest_row.record_digest::TEXT THEN
            RETURN agent_control.runtime_deny_command(
                command_row, 'model_result_lineage_mismatch'
            );
        END IF;
        consumed_input := (result_candidate->>'input_tokens')::BIGINT;
        consumed_output := (result_candidate->>'output_tokens')::BIGINT;
        consumed_cost :=
            (result_candidate->>'external_cost_micro_usd')::BIGINT;
        consumed_wall := (result_candidate->>'wall_time_ms')::BIGINT;
        IF consumed_input > manifest_row.reserved_input_tokens
           OR consumed_output > manifest_row.max_output_tokens
           OR consumed_cost
                > manifest_row.reserved_external_cost_micro_usd
           OR consumed_wall > manifest_row.timeout_ms
           OR (result_candidate #>> '{output,committed_at}')::TIMESTAMPTZ
                > now_at THEN
            RETURN agent_control.runtime_deny_command(
                command_row, 'model_usage_exceeds_reservation'
            );
        END IF;
        IF NOT agent_control.runtime_worker_blob_ref_current(
            result_candidate->'output', principal,
            'model_call_manifest', manifest_row.call_id,
            manifest_row.record_digest::TEXT
        ) THEN
            RETURN agent_control.runtime_deny_command(
                command_row, 'model_output_unavailable'
            );
        END IF;
    ELSE
        consumed_input := manifest_row.reserved_input_tokens;
        consumed_output := manifest_row.max_output_tokens;
        consumed_cost := manifest_row.reserved_external_cost_micro_usd;
        consumed_wall := manifest_row.timeout_ms;
    END IF;

    -- Settlement must first own the complete reservation ancestry.  A lock
    -- wait can outlive the Worker's lease, so all fences are checked again
    -- after the ancestry (and, for success, the output Blob) is stable.
    IF NOT agent_control.runtime_lock_existing_model_reservation_ancestors(
        run_row.run_id, task_row.budget_ledger_id,
        manifest_row.reserved_input_tokens,
        manifest_row.max_output_tokens,
        manifest_row.reserved_external_cost_micro_usd,
        manifest_row.timeout_ms
    ) THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'model_reservation_unavailable'
        );
    END IF;
    IF outcome = 'result_committed' THEN
        PERFORM agent_control.runtime_lock_model_identity_keys(ARRAY[
            'provider-request:' || manifest_row.provider || ':'
                || (result_candidate->>'provider_request_id')
        ]::TEXT[]);
    END IF;
    IF outcome = 'result_committed'
       AND NOT agent_control.runtime_worker_blob_ref_current(
           result_candidate->'output', principal,
           'model_call_manifest', manifest_row.call_id,
           manifest_row.record_digest::TEXT
       ) THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'model_output_unavailable'
        );
    END IF;

    SELECT * INTO attempt_row
      FROM agent_control.runtime_attempt AS attempt
     WHERE attempt.attempt_id = p_command->>'attempt_id'
       AND attempt.run_id = run_row.run_id
       AND attempt.task_id = task_row.task_id
       AND attempt.session_id = session_row.session_id
     FOR UPDATE;
    SELECT * INTO turn_row
      FROM agent_control.runtime_turn AS turn
     WHERE turn.turn_id = p_command->>'turn_id'
       AND turn.attempt_id = attempt_row.attempt_id
       AND turn.run_id = run_row.run_id
       AND turn.task_id = task_row.task_id
       AND turn.session_id = session_row.session_id
     FOR UPDATE;
    now_at := clock_timestamp();
    IF now_at >= command_row.deadline_at
       OR attempt_row.state <> 'executing'
       OR attempt_row.state_generation <> expected_attempt_generation
       OR attempt_row.lease_generation <> expected_lease_generation
       OR attempt_row.lease_token::TEXT <> p_command->>'lease_token'
       OR attempt_row.lease_worker #>> '{principal_id}' <> principal
       OR attempt_row.lease_worker #>> '{kind}' <> 'workload'
       OR attempt_row.lease_worker #>> '{audience}' <> 'worker'
       OR now_at >= attempt_row.lease_expires_at THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'resolve_fence_expired'
        );
    END IF;
    IF turn_row.state NOT IN ('dispatched', 'unknown')
       OR turn_row.state_generation <> expected_turn_generation
       OR NOT turn_row.reservation_held THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'stale_turn_generation'
        );
    END IF;

    BEGIN
        IF outcome = 'result_committed' THEN
            new_result_id := gen_random_uuid()::TEXT;
            result_body := jsonb_build_object(
                'schema_revision', 1,
                'result_id', new_result_id,
                'call_id', manifest_row.call_id,
                'attempt_id', attempt_row.attempt_id,
                'turn_id', turn_row.turn_id,
                'idempotency_key', manifest_row.idempotency_key,
                'request_digest', manifest_row.request_digest::TEXT,
                'provider_request_id',
                    result_candidate->>'provider_request_id',
                'output', result_candidate->'output',
                'input_tokens', consumed_input,
                'output_tokens', consumed_output,
                'external_cost_micro_usd', consumed_cost,
                'wall_time_ms', consumed_wall,
                'finish_reason', result_candidate->>'finish_reason',
                'committed_at', agent_control.runtime_utc_text(now_at)
            );
            new_result_digest := agent_control.runtime_contract_digest(
                'agent-platform.contract.model_call_result.v1', result_body
            );
            INSERT INTO agent_control.runtime_model_call_result (
                result_id, schema_revision, record_digest, call_id,
                attempt_id, turn_id, idempotency_key, request_digest,
                provider_request_id, output_origin_owner,
                output_origin_record_type, output_origin_record_id,
                output_origin_schema_revision, output_origin_record_digest,
                output, input_tokens, output_tokens,
                external_cost_micro_usd, wall_time_ms, finish_reason,
                committed_at
            ) VALUES (
                new_result_id, 1, new_result_digest, manifest_row.call_id,
                attempt_row.attempt_id, turn_row.turn_id,
                manifest_row.idempotency_key,
                manifest_row.request_digest,
                result_candidate->>'provider_request_id',
                'agent_control', 'model_call_manifest', manifest_row.call_id,
                1, manifest_row.record_digest, result_candidate->'output',
                consumed_input, consumed_output, consumed_cost, consumed_wall,
                result_candidate->>'finish_reason', now_at
            );
            INSERT INTO agent_control.runtime_model_provider_request (
                provider, provider_request_id, call_id, result_id, committed_at
            ) VALUES (
                manifest_row.provider,
                result_candidate->>'provider_request_id',
                manifest_row.call_id, new_result_id, now_at
            );

            UPDATE agent_control.runtime_turn
               SET state = 'result_committed',
                   state_generation = turn_row.state_generation + 1,
                   result_owner = 'agent_control',
                   result_record_type = 'model_call_result',
                   result_id = new_result_id,
                   result_schema_revision = 1,
                   result_digest = new_result_digest,
                   failure = NULL,
                   reservation_held = false,
                   finished_at = now_at,
                   updated_at = greatest(now_at, turn_row.updated_at)
             WHERE turn_id = turn_row.turn_id;
        ELSE
            UPDATE agent_control.runtime_turn
               SET state = 'failed',
                   state_generation = turn_row.state_generation + 1,
                   failure = p_command->'failure',
                   reservation_held = false,
                   finished_at = now_at,
                   updated_at = greatest(now_at, turn_row.updated_at)
             WHERE turn_id = turn_row.turn_id;
        END IF;

        IF NOT agent_control.runtime_settle_model_budget_ancestors(
            run_row.run_id, task_row.budget_ledger_id,
            manifest_row.reserved_input_tokens,
            manifest_row.max_output_tokens,
            manifest_row.reserved_external_cost_micro_usd,
            manifest_row.timeout_ms,
            consumed_input, consumed_output, consumed_cost, consumed_wall,
            principal, envelope->>'causation_id',
            envelope->>'correlation_id', now_at
        ) THEN
            RAISE EXCEPTION USING ERRCODE = '40001',
                MESSAGE = 'model reservation unavailable during settlement';
        END IF;
        IF outcome = 'result_committed'
           AND NOT agent_control.runtime_worker_blob_ref_current(
               result_candidate->'output', principal,
               'model_call_manifest', manifest_row.call_id,
               manifest_row.record_digest::TEXT
           ) THEN
            RAISE EXCEPTION USING ERRCODE = '40001',
                MESSAGE = 'model output reference expired during settlement';
        END IF;

        PERFORM agent_control.runtime_insert_event(
            'turn', turn_row.turn_id, turn_row.state, outcome,
            turn_row.state_generation + 1, principal,
            envelope->>'causation_id', envelope->>'correlation_id',
            CASE WHEN outcome = 'result_committed'
                 THEN 'model_call_resolved'
                 ELSE 'model_call_failed' END,
            now_at
        );
    EXCEPTION
        WHEN unique_violation THEN
            RETURN agent_control.runtime_deny_command(
                command_row, 'model_result_identity_conflict'
            );
        WHEN serialization_failure THEN
            RETURN agent_control.runtime_deny_command(
                command_row, 'model_settlement_changed'
            );
    END;

    response := jsonb_build_object(
        'schema_revision', 1,
        'status', 'committed',
        'command_id', command_row.command_id,
        'attempt_id', attempt_row.attempt_id,
        'turn_id', turn_row.turn_id,
        'turn_state', outcome,
        'turn_state_generation', turn_row.state_generation + 1,
        'call_id', manifest_row.call_id,
        'outcome', outcome
    );
    IF outcome = 'result_committed' THEN
        response := response || jsonb_build_object(
            'result_id', new_result_id,
            'result_digest', new_result_digest::TEXT
        );
    END IF;
    RETURN agent_control.runtime_finish_command(
        command_row, 'committed', response
    );
END
$$;

CREATE FUNCTION agent_control.runtime_mark_model_call_unknown(p_command JSONB)
RETURNS JSONB
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, agent_control, platform_security, blob
SET timezone = 'UTC'
AS $$
DECLARE
    command_row agent_control.runtime_command%ROWTYPE;
    run_row agent_control.runtime_run%ROWTYPE;
    task_row agent_control.runtime_task%ROWTYPE;
    session_row agent_control.runtime_session%ROWTYPE;
    attempt_row agent_control.runtime_attempt%ROWTYPE;
    turn_row agent_control.runtime_turn%ROWTYPE;
    manifest_row agent_control.runtime_model_call_manifest%ROWTYPE;
    envelope JSONB;
    principal TEXT;
    lookup_run_id TEXT;
    lookup_task_id TEXT;
    lookup_session_id TEXT;
    expected_attempt_generation BIGINT;
    expected_lease_generation BIGINT;
    expected_turn_generation BIGINT;
    now_at TIMESTAMPTZ;
    response JSONB;
BEGIN
    IF p_command IS NULL
       OR NOT agent_control.runtime_mark_model_call_unknown_command_valid(
           p_command
       ) THEN
        RAISE EXCEPTION USING ERRCODE = '22023',
            MESSAGE = 'invalid mark_model_call_unknown command';
    END IF;
    envelope := p_command->'envelope';
    principal := envelope #>> '{actor,principal_id}';
    expected_attempt_generation :=
        (p_command->>'expected_attempt_state_generation')::BIGINT;
    expected_lease_generation :=
        (p_command->>'lease_generation')::BIGINT;
    expected_turn_generation :=
        (p_command->>'expected_turn_state_generation')::BIGINT;

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
    SELECT * INTO turn_row
      FROM agent_control.runtime_turn AS turn
     WHERE turn.turn_id = p_command->>'turn_id'
       AND turn.attempt_id = attempt_row.attempt_id
       AND turn.run_id = run_row.run_id
       AND turn.task_id = task_row.task_id
       AND turn.session_id = session_row.session_id
     FOR UPDATE;
    IF NOT FOUND THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'turn_not_found'
        );
    END IF;

    now_at := clock_timestamp();
    IF run_row.state <> 'running' OR task_row.state <> 'running'
       OR session_row.state <> 'open' OR attempt_row.state <> 'executing' THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'runtime_not_executable'
        );
    END IF;
    IF now_at >= command_row.deadline_at THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'command_deadline_expired'
        );
    END IF;
    IF attempt_row.state_generation <> expected_attempt_generation THEN
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
    IF turn_row.state <> 'dispatched'
       OR turn_row.state_generation <> expected_turn_generation
       OR NOT turn_row.reservation_held THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'stale_turn_generation'
        );
    END IF;
    SELECT * INTO manifest_row
      FROM agent_control.runtime_model_call_manifest AS manifest
     WHERE manifest.turn_id = turn_row.turn_id
       AND manifest.attempt_id = attempt_row.attempt_id
       AND manifest.request_digest = turn_row.request_digest
     FOR SHARE;
    IF NOT FOUND THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'model_manifest_not_found'
        );
    END IF;

    -- Manifest locking can wait.  Re-sample database time and the exact lease
    -- fence before making the irreversible dispatched -> unknown transition.
    SELECT * INTO attempt_row
      FROM agent_control.runtime_attempt AS attempt
     WHERE attempt.attempt_id = p_command->>'attempt_id'
       AND attempt.run_id = run_row.run_id
       AND attempt.task_id = task_row.task_id
       AND attempt.session_id = session_row.session_id
     FOR UPDATE;
    SELECT * INTO turn_row
      FROM agent_control.runtime_turn AS turn
     WHERE turn.turn_id = p_command->>'turn_id'
       AND turn.attempt_id = attempt_row.attempt_id
       AND turn.run_id = run_row.run_id
       AND turn.task_id = task_row.task_id
       AND turn.session_id = session_row.session_id
     FOR UPDATE;
    now_at := clock_timestamp();
    IF now_at >= command_row.deadline_at
       OR attempt_row.state <> 'executing'
       OR attempt_row.state_generation <> expected_attempt_generation
       OR attempt_row.lease_generation <> expected_lease_generation
       OR attempt_row.lease_token::TEXT <> p_command->>'lease_token'
       OR attempt_row.lease_worker #>> '{principal_id}' <> principal
       OR attempt_row.lease_worker #>> '{kind}' <> 'workload'
       OR attempt_row.lease_worker #>> '{audience}' <> 'worker'
       OR now_at >= attempt_row.lease_expires_at THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'unknown_fence_expired'
        );
    END IF;
    IF turn_row.state <> 'dispatched'
       OR turn_row.state_generation <> expected_turn_generation
       OR NOT turn_row.reservation_held THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'stale_turn_generation'
        );
    END IF;

    UPDATE agent_control.runtime_turn
       SET state = 'unknown',
           state_generation = turn_row.state_generation + 1,
           failure = p_command->'failure',
           reservation_held = true,
           updated_at = greatest(now_at, turn_row.updated_at)
     WHERE turn_id = turn_row.turn_id;
    PERFORM agent_control.runtime_insert_event(
        'turn', turn_row.turn_id, 'dispatched', 'unknown',
        turn_row.state_generation + 1, principal,
        envelope->>'causation_id', envelope->>'correlation_id',
        'model_call_outcome_unknown', now_at
    );

    response := jsonb_build_object(
        'schema_revision', 1,
        'status', 'committed',
        'command_id', command_row.command_id,
        'attempt_id', attempt_row.attempt_id,
        'turn_id', turn_row.turn_id,
        'turn_state', 'unknown',
        'turn_state_generation', turn_row.state_generation + 1,
        'call_id', manifest_row.call_id,
        'reservation_held', true
    );
    RETURN agent_control.runtime_finish_command(
        command_row, 'committed', response
    );
END
$$;

-- A Worker can die after dispatch is durable but before it records an unknown
-- provider outcome.  The old claim path can reclaim only an already-unknown
-- Turn, so this narrow pre-claim path first makes the ambiguity durable and
-- then rotates the lease on the same Attempt.  It never creates another Turn,
-- Manifest, call id, or provider idempotency identity.
CREATE FUNCTION agent_control.runtime_reclaim_dispatched_attempt(
    p_command JSONB
) RETURNS JSONB
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
    turn_row agent_control.runtime_turn%ROWTYPE;
    manifest_row agent_control.runtime_model_call_manifest%ROWTYPE;
    policy_row agent_control.runtime_policy_revision%ROWTYPE;
    envelope JSONB;
    principal TEXT;
    task_run_id TEXT;
    target_attempt_id TEXT;
    target_turn_id TEXT;
    requested_seconds BIGINT;
    expected_task_generation BIGINT;
    now_at TIMESTAMPTZ;
    new_expires_at TIMESTAMPTZ;
    new_lease_token UUID;
    lease_event_generation BIGINT;
    ambiguity_failure JSONB := jsonb_build_object(
        'code', 'provider_outcome_ambiguous',
        'message', 'worker lease expired after model dispatch',
        'retryable', true
    );
    response JSONB;
BEGIN
    IF p_command IS NULL
       OR NOT agent_control.runtime_claim_task_command_valid(p_command) THEN
        RAISE EXCEPTION USING ERRCODE = '22023',
            MESSAGE = 'invalid claim_task command';
    END IF;

    -- Do not consume the command identity unless this is specifically the
    -- dispatched crash window.  NULL delegates ordinary/new and already-
    -- unknown claims to the frozen 0006 implementation.
    IF NOT EXISTS (
        SELECT 1
          FROM agent_control.runtime_task AS task
          JOIN agent_control.runtime_attempt AS attempt
            ON attempt.task_id = task.task_id
           AND attempt.run_id = task.run_id
          JOIN agent_control.runtime_turn AS turn
            ON turn.attempt_id = attempt.attempt_id
           AND turn.task_id = task.task_id
           AND turn.run_id = task.run_id
         WHERE task.task_id = p_command->>'task_id'
           AND attempt.state = 'executing'
           AND attempt.lease_expires_at <= clock_timestamp()
           AND turn.state = 'dispatched'
           AND turn.reservation_held
    ) THEN
        RETURN NULL;
    END IF;

    envelope := p_command->'envelope';
    principal := envelope #>> '{actor,principal_id}';
    requested_seconds := (p_command->>'requested_lease_seconds')::BIGINT;
    expected_task_generation :=
        (p_command->>'expected_task_state_generation')::BIGINT;

    command_row := agent_control.runtime_begin_command(p_command);
    IF command_row.state IN ('committed', 'denied') THEN
        RETURN command_row.response;
    END IF;

    SELECT task.run_id INTO task_run_id
      FROM agent_control.runtime_task AS task
     WHERE task.task_id = p_command->>'task_id';
    IF NOT FOUND THEN
        RETURN agent_control.runtime_deny_command(command_row, 'task_not_found');
    END IF;
    SELECT * INTO run_row
      FROM agent_control.runtime_run AS run
     WHERE run.run_id = task_run_id
     FOR UPDATE;
    IF NOT FOUND THEN
        RETURN agent_control.runtime_deny_command(command_row, 'run_not_found');
    END IF;
    SELECT * INTO task_row
      FROM agent_control.runtime_task AS task
     WHERE task.task_id = p_command->>'task_id'
       AND task.run_id = run_row.run_id
     FOR UPDATE;
    IF NOT FOUND THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'task_run_mismatch'
        );
    END IF;
    IF task_row.session_id IS NULL THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'session_not_bound'
        );
    END IF;
    SELECT * INTO session_row
      FROM agent_control.runtime_session AS session
     WHERE session.session_id = task_row.session_id
       AND session.run_id = run_row.run_id
       AND session.task_id = task_row.task_id
     FOR UPDATE;
    IF NOT FOUND THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'session_not_found'
        );
    END IF;

    -- Task locking prevents new Attempt allocation.  Lock all existing rows
    -- in their canonical order before choosing the single active Attempt.
    PERFORM attempt.attempt_id
      FROM agent_control.runtime_attempt AS attempt
     WHERE attempt.task_id = task_row.task_id
     ORDER BY attempt.ordinal, attempt.attempt_id
     FOR UPDATE;
    SELECT * INTO attempt_row
      FROM agent_control.runtime_attempt AS attempt
     WHERE attempt.task_id = task_row.task_id
       AND attempt.run_id = run_row.run_id
       AND attempt.session_id = session_row.session_id
       AND attempt.state = 'executing'
     ORDER BY attempt.ordinal DESC, attempt.attempt_id DESC
     LIMIT 1;
    IF NOT FOUND THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'dispatched_reclaim_changed'
        );
    END IF;
    target_attempt_id := attempt_row.attempt_id;
    SELECT * INTO turn_row
      FROM agent_control.runtime_turn AS turn
     WHERE turn.attempt_id = attempt_row.attempt_id
       AND turn.run_id = run_row.run_id
       AND turn.task_id = task_row.task_id
       AND turn.session_id = session_row.session_id
       AND turn.state = 'dispatched'
       AND turn.reservation_held
     ORDER BY turn.ordinal, turn.turn_id
     LIMIT 1
     FOR UPDATE;
    IF NOT FOUND THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'dispatched_reclaim_changed'
        );
    END IF;
    target_turn_id := turn_row.turn_id;
    SELECT * INTO manifest_row
      FROM agent_control.runtime_model_call_manifest AS manifest
     WHERE manifest.turn_id = turn_row.turn_id
       AND manifest.attempt_id = attempt_row.attempt_id
       AND manifest.request_digest = turn_row.request_digest
     FOR SHARE;
    IF NOT FOUND THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'model_manifest_not_found'
        );
    END IF;

    SELECT * INTO STRICT policy_row
      FROM agent_control.runtime_policy_revision AS policy
     WHERE policy.policy_id = run_row.runtime_policy_id
       AND policy.generation = run_row.runtime_policy_generation
       AND policy.record_digest = run_row.runtime_policy_digest;
    IF requested_seconds > policy_row.max_lease_seconds THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'lease_limit_exceeded'
        );
    END IF;
    IF NOT agent_control.runtime_lock_existing_model_reservation_ancestors(
        run_row.run_id, task_row.budget_ledger_id,
        manifest_row.reserved_input_tokens,
        manifest_row.max_output_tokens,
        manifest_row.reserved_external_cost_micro_usd,
        manifest_row.timeout_ms
    ) THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'model_reservation_unavailable'
        );
    END IF;

    -- The ancestry lock can wait.  Refresh both fenced rows and database time
    -- before recording ambiguity or issuing the replacement lease.
    SELECT * INTO attempt_row
      FROM agent_control.runtime_attempt AS attempt
     WHERE attempt.attempt_id = target_attempt_id
       AND attempt.run_id = run_row.run_id
       AND attempt.task_id = task_row.task_id
       AND attempt.session_id = session_row.session_id
     FOR UPDATE;
    SELECT * INTO turn_row
      FROM agent_control.runtime_turn AS turn
     WHERE turn.turn_id = target_turn_id
       AND turn.attempt_id = attempt_row.attempt_id
     FOR UPDATE;
    now_at := clock_timestamp();
    IF run_row.state <> 'running' OR task_row.state <> 'running'
       OR session_row.state <> 'open' OR NOT task_row.budget_slot_held
       OR task_row.state_generation <> expected_task_generation
       OR attempt_row.state <> 'executing'
       OR now_at < attempt_row.lease_expires_at
       OR turn_row.state <> 'dispatched'
       OR NOT turn_row.reservation_held THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'dispatched_reclaim_changed'
        );
    END IF;
    IF now_at >= command_row.deadline_at
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
    IF new_expires_at <= now_at
       OR new_expires_at <= attempt_row.lease_expires_at THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'lease_window_unavailable'
        );
    END IF;

    UPDATE agent_control.runtime_turn
       SET state = 'unknown',
           state_generation = turn_row.state_generation + 1,
           failure = ambiguity_failure,
           reservation_held = true,
           updated_at = greatest(now_at, turn_row.updated_at)
     WHERE turn_id = turn_row.turn_id;
    PERFORM agent_control.runtime_insert_event(
        'turn', turn_row.turn_id, 'dispatched', 'unknown',
        turn_row.state_generation + 1, principal,
        envelope->>'causation_id', envelope->>'correlation_id',
        'model_call_lease_expired', now_at
    );

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
    INSERT INTO agent_control.runtime_attempt_lease_event (
        event_id, schema_revision, attempt_id, event_generation,
        lease_generation, transition, worker_principal_id, lease_token,
        previous_expires_at, new_expires_at, actor, causation_id,
        correlation_id, occurred_at
    ) VALUES (
        gen_random_uuid()::TEXT, 1, attempt_row.attempt_id,
        lease_event_generation, attempt_row.lease_generation + 1,
        'reclaimed', principal, new_lease_token,
        attempt_row.lease_expires_at, new_expires_at,
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
        'lease_expires_at', agent_control.runtime_utc_text(new_expires_at),
        'reclaimed', true,
        'unresolved_turn_id', turn_row.turn_id,
        'unresolved_turn_state', 'unknown'
    );
    RETURN agent_control.runtime_finish_command(
        command_row, 'committed', response
    );
EXCEPTION WHEN NO_DATA_FOUND THEN
    IF command_row.command_id IS NOT NULL THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'dispatched_reclaim_changed'
        );
    END IF;
    RAISE;
END
$$;

-- Public Worker commands accept raw TEXT so lexical duplicate keys and other
-- non-canonical JSON cannot disappear before validation.
CREATE OR REPLACE FUNCTION agent_control.claim_task(p_command TEXT)
RETURNS JSONB
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, agent_control, platform_security
SET timezone = 'UTC'
AS $$
DECLARE
    parsed JSONB := agent_control.runtime_parse_worker_command(p_command);
    reclaimed_response JSONB;
    overflow_command_row agent_control.runtime_command%ROWTYPE;
BEGIN
    IF parsed IS NULL THEN
        RAISE EXCEPTION USING ERRCODE = '22023',
            MESSAGE = 'invalid raw claim_task command';
    END IF;
    BEGIN
        reclaimed_response :=
            agent_control.runtime_reclaim_dispatched_attempt(parsed);
        IF reclaimed_response IS NOT NULL THEN
            RETURN reclaimed_response;
        END IF;
        RETURN agent_control.runtime_claim_task(parsed);
    EXCEPTION
        WHEN datetime_field_overflow OR interval_field_overflow THEN
            -- The failed nested statement is rolled back to this block's
            -- savepoint, including any processing command row. Recreate the
            -- exact command identity and fail durably instead of leaking a
            -- representation error that can be retried forever.
            overflow_command_row :=
                agent_control.runtime_begin_command(parsed);
            IF overflow_command_row.state IN ('committed', 'denied') THEN
                RETURN overflow_command_row.response;
            END IF;
            RETURN agent_control.runtime_deny_command(
                overflow_command_row, 'lease_duration_out_of_range'
            );
    END;
END
$$;

CREATE FUNCTION agent_control.dispatch_model_call(p_command TEXT)
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
            MESSAGE = 'invalid raw dispatch_model_call command';
    END IF;
    RETURN agent_control.runtime_dispatch_model_call(parsed);
END
$$;

CREATE FUNCTION agent_control.resolve_model_call(p_command TEXT)
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
            MESSAGE = 'invalid raw resolve_model_call command';
    END IF;
    RETURN agent_control.runtime_resolve_model_call(parsed);
END
$$;

CREATE FUNCTION agent_control.mark_model_call_unknown(p_command TEXT)
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
            MESSAGE = 'invalid raw mark_model_call_unknown command';
    END IF;
    RETURN agent_control.runtime_mark_model_call_unknown(parsed);
END
$$;

-- The new immutable registry and every implementation helper remain private.
-- The Worker receives only the four raw command functions.
REVOKE ALL ON TABLE agent_control.runtime_model_provider_request FROM PUBLIC;
REVOKE ALL ON TABLE agent_control.runtime_model_identity_lock FROM PUBLIC;

REVOKE ALL ON FUNCTION agent_control.runtime_nonnegative_bigint_json(JSONB) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.runtime_model_manifest_candidate_valid(JSONB) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.runtime_model_result_candidate_valid(JSONB) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.runtime_dispatch_model_call_command_valid(JSONB) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.runtime_resolve_model_call_command_valid(JSONB) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.runtime_mark_model_call_unknown_command_valid(JSONB) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.runtime_worker_blob_ref_current(JSONB, TEXT, TEXT, TEXT, TEXT) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.runtime_lock_model_budget_ancestors(TEXT, TEXT, BIGINT, BIGINT, BIGINT, BIGINT, BIGINT) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.runtime_reserve_model_budget_ancestors(TEXT, TEXT, BIGINT, BIGINT, BIGINT, BIGINT, BIGINT, TIMESTAMPTZ) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.runtime_settle_model_budget_ancestors(TEXT, TEXT, BIGINT, BIGINT, BIGINT, BIGINT, BIGINT, BIGINT, BIGINT, BIGINT, TEXT, TEXT, TEXT, TIMESTAMPTZ) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.runtime_lock_existing_model_reservation_ancestors(TEXT, TEXT, BIGINT, BIGINT, BIGINT, BIGINT) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.runtime_lock_model_identity_keys(TEXT[]) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.runtime_dispatch_model_call(JSONB) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.runtime_resolve_model_call(JSONB) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.runtime_mark_model_call_unknown(JSONB) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.runtime_reclaim_dispatched_attempt(JSONB) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.claim_task(TEXT) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.dispatch_model_call(TEXT) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.resolve_model_call(TEXT) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.mark_model_call_unknown(TEXT) FROM PUBLIC;

GRANT EXECUTE ON FUNCTION agent_control.claim_task(TEXT)
    TO alpheus_agent_worker;
GRANT EXECUTE ON FUNCTION agent_control.dispatch_model_call(TEXT)
    TO alpheus_agent_worker;
GRANT EXECUTE ON FUNCTION agent_control.resolve_model_call(TEXT)
    TO alpheus_agent_worker;
GRANT EXECUTE ON FUNCTION agent_control.mark_model_call_unknown(TEXT)
    TO alpheus_agent_worker;

RESET ROLE;
