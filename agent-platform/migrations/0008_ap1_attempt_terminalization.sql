SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

-- AP1 Attempt terminalization is a two-command protocol.  A successful
-- command converts one already-committed ModelCallResult into an immutable,
-- non-effect Artifact.  A failed command either consumes one frozen retry
-- allowance or terminalizes the Task.  Neither command performs a model,
-- Tool, Kernel, Provider, broker, GRACE, Delegation, publication, operation,
-- or other external effect.

-- These deferred 0005 triggers run after the public SECURITY DEFINER
-- statement has returned to the default-deny Worker.  Keep their invariant
-- reads owned by the migrator and keep the functions non-callable.
ALTER FUNCTION agent_control.validate_runtime_artifact_sections()
    SECURITY DEFINER;
ALTER FUNCTION agent_control.validate_runtime_artifact_sections()
    SET search_path = pg_catalog, agent_control;
REVOKE ALL ON FUNCTION agent_control.validate_runtime_artifact_sections()
    FROM PUBLIC;

ALTER FUNCTION agent_control.validate_runtime_artifact_section_time()
    SECURITY DEFINER;
ALTER FUNCTION agent_control.validate_runtime_artifact_section_time()
    SET search_path = pg_catalog, agent_control;
REVOKE ALL ON FUNCTION agent_control.validate_runtime_artifact_section_time()
    FROM PUBLIC;

CREATE FUNCTION agent_control.runtime_artifact_section_candidate_valid(
    p_value JSONB
) RETURNS BOOLEAN
LANGUAGE plpgsql
IMMUTABLE
STRICT
AS $$
BEGIN
    RETURN COALESCE(
        jsonb_typeof(p_value) = 'object'
        AND p_value ?& ARRAY['name', 'required', 'content']
        AND p_value - ARRAY['name', 'required', 'content'] = '{}'::JSONB
        AND jsonb_typeof(p_value->'name') = 'string'
        AND agent_control.runtime_name_valid(p_value->>'name')
        AND jsonb_typeof(p_value->'required') = 'boolean'
        AND agent_control.runtime_blob_ref_valid(p_value->'content', '', '')
        AND agent_control.runtime_utc_instant_json(
            p_value #> '{content,committed_at}'
        )
        AND p_value #>> '{content,origin,owner}' = 'agent_control',
        false
    );
EXCEPTION WHEN OTHERS THEN
    RETURN false;
END
$$;

CREATE FUNCTION agent_control.runtime_artifact_candidate_valid(
    p_value JSONB
) RETURNS BOOLEAN
LANGUAGE plpgsql
IMMUTABLE
STRICT
AS $$
DECLARE
    section_count BIGINT;
BEGIN
    IF NOT COALESCE(
        jsonb_typeof(p_value) = 'object'
        AND p_value ?& ARRAY[
            'artifact_type', 'output_contract_digest', 'effect_class',
            'sections'
        ]
        AND p_value - ARRAY[
            'artifact_type', 'output_contract_digest', 'effect_class',
            'sections'
        ] = '{}'::JSONB
        AND jsonb_typeof(p_value->'artifact_type') = 'string'
        AND agent_control.runtime_name_valid(p_value->>'artifact_type')
        AND jsonb_typeof(p_value->'output_contract_digest') = 'string'
        AND agent_control.runtime_digest_valid(
            p_value->>'output_contract_digest'
        )
        AND jsonb_typeof(p_value->'effect_class') = 'string'
        AND p_value->>'effect_class' = 'none'
        AND jsonb_typeof(p_value->'sections') = 'array',
        false
    ) THEN
        RETURN false;
    END IF;

    section_count := jsonb_array_length(p_value->'sections');
    IF section_count NOT BETWEEN 1 AND 256
       OR EXISTS (
           SELECT 1
             FROM jsonb_array_elements(p_value->'sections') AS section(value)
            WHERE NOT agent_control.runtime_artifact_section_candidate_valid(
                section.value
            )
       )
       OR EXISTS (
           SELECT 1
             FROM jsonb_array_elements(p_value->'sections') AS section(value)
            GROUP BY section.value->>'name'
           HAVING count(*) <> 1
       )
       OR EXISTS (
           SELECT 1
             FROM jsonb_array_elements(p_value->'sections') AS section(value)
            GROUP BY section.value #>> '{content,blob_id}'
           HAVING count(DISTINCT section.value->'content') <> 1
       )
       OR NOT EXISTS (
           SELECT 1
             FROM jsonb_array_elements(p_value->'sections') AS section(value)
            WHERE (section.value->>'required')::BOOLEAN
       ) THEN
        RETURN false;
    END IF;
    RETURN true;
EXCEPTION WHEN OTHERS THEN
    RETURN false;
END
$$;

CREATE FUNCTION agent_control.runtime_commit_attempt_command_valid(
    p_command JSONB
) RETURNS BOOLEAN
LANGUAGE sql
STABLE
STRICT
AS $$
    SELECT agent_control.runtime_worker_command_valid(
               p_command,
               'commit_attempt',
               ARRAY[
                   'schema_revision', 'envelope', 'attempt_id',
                   'expected_attempt_state_generation', 'lease_generation',
                   'lease_token', 'result', 'artifact'
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
       AND agent_control.runtime_record_ref_valid(
               p_command->'result', 'agent_control', 'model_call_result'
           )
       AND agent_control.runtime_artifact_candidate_valid(
               p_command->'artifact'
           )
$$;

CREATE FUNCTION agent_control.runtime_fail_attempt_command_valid(
    p_command JSONB
) RETURNS BOOLEAN
LANGUAGE sql
STABLE
STRICT
AS $$
    SELECT agent_control.runtime_worker_command_valid(
               p_command,
               'fail_attempt',
               ARRAY[
                   'schema_revision', 'envelope', 'attempt_id',
                   'expected_attempt_state_generation', 'lease_generation',
                   'lease_token', 'retry_class', 'failure'
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
       AND jsonb_typeof(p_command->'retry_class') = 'string'
       AND p_command->>'retry_class' IN (
               'none', 'invalid_output', 'infrastructure'
           )
       AND agent_control.runtime_failure_valid(p_command->'failure')
       AND (
           (p_command->>'retry_class' = 'none'
                AND p_command #>> '{failure,retryable}' = 'false')
           OR
           (p_command->>'retry_class' IN (
                    'invalid_output', 'infrastructure'
                )
                AND p_command #>> '{failure,retryable}' = 'true')
       )
$$;

-- The caller holds the exact parent ancestry root-to-leaf through
-- runtime_lock_budget_ancestors().  A retry is charged only to that ancestry;
-- the Task's own ledger continues to budget descendants, not its own work.
-- All rows are checked before the first update, so a denied retry never leaves
-- a partial charge.
CREATE FUNCTION agent_control.runtime_charge_retry_budget_ancestors(
    p_run_id TEXT,
    p_task_ledger_id TEXT,
    p_retry_class TEXT,
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
    root_seen BOOLEAN := false;
BEGIN
    IF p_retry_class NOT IN ('invalid_output', 'infrastructure') THEN
        RAISE EXCEPTION USING ERRCODE = '22023',
            MESSAGE = 'invalid retry budget class';
    END IF;

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
         WHERE ledger.ledger_id = current_ledger_id;
        IF ledger_row.scope = 'run'
           AND ledger_row.scope_id = p_run_id
           AND ledger_row.parent_ledger_id IS NULL THEN
            root_seen := true;
        END IF;
        IF ledger_row.state <> 'open'
           OR (
               p_retry_class = 'invalid_output'
               AND ledger_row.consumed_invalid_output_retries
                   >= ledger_row.limit_invalid_output_retries
                      - ledger_row.reserved_invalid_output_retries
           )
           OR (
               p_retry_class = 'infrastructure'
               AND ledger_row.consumed_infrastructure_retries
                   >= ledger_row.limit_infrastructure_retries
                      - ledger_row.reserved_infrastructure_retries
           ) THEN
            RETURN false;
        END IF;
    END LOOP;
    IF NOT root_seen THEN
        RETURN false;
    END IF;

    FOREACH current_ledger_id IN ARRAY ledger_ids LOOP
        UPDATE agent_control.runtime_budget_ledger AS ledger
           SET consumed_invalid_output_retries
                   = consumed_invalid_output_retries
                     + CASE WHEN p_retry_class = 'invalid_output'
                            THEN 1 ELSE 0 END,
               consumed_infrastructure_retries
                   = consumed_infrastructure_retries
                     + CASE WHEN p_retry_class = 'infrastructure'
                            THEN 1 ELSE 0 END,
               generation = generation + 1,
               updated_at = greatest(p_updated_at, updated_at)
         WHERE ledger.ledger_id = current_ledger_id;
    END LOOP;
    RETURN true;
EXCEPTION WHEN NO_DATA_FOUND THEN
    RETURN false;
END
$$;

-- Terminal Task completion releases the one historical active slot charged
-- at ready -> running.  The Task row fence guarantees this helper is called at
-- most once, and the complete ancestry is validated before any decrement.
CREATE FUNCTION agent_control.runtime_release_active_slot_ancestors(
    p_run_id TEXT,
    p_task_ledger_id TEXT,
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
         WHERE ledger.ledger_id = current_ledger_id;
        IF ledger_row.scope = 'run'
           AND ledger_row.scope_id = p_run_id
           AND ledger_row.parent_ledger_id IS NULL THEN
            root_seen := true;
        END IF;
        IF ledger_row.state = 'closed'
           OR ledger_row.consumed_active_tasks < 1 THEN
            RETURN false;
        END IF;
    END LOOP;
    IF NOT root_seen THEN
        RETURN false;
    END IF;

    FOREACH current_ledger_id IN ARRAY ledger_ids LOOP
        UPDATE agent_control.runtime_budget_ledger AS ledger
           SET consumed_active_tasks = consumed_active_tasks - 1,
               generation = generation + 1,
               updated_at = greatest(p_updated_at, updated_at)
         WHERE ledger.ledger_id = current_ledger_id;
    END LOOP;
    RETURN true;
EXCEPTION WHEN NO_DATA_FOUND THEN
    RETURN false;
END
$$;

CREATE FUNCTION agent_control.runtime_insert_attempt_release_event(
    p_attempt_id TEXT,
    p_lease_generation BIGINT,
    p_worker_principal_id TEXT,
    p_lease_token UUID,
    p_previous_expires_at TIMESTAMPTZ,
    p_causation_id TEXT,
    p_correlation_id TEXT,
    p_occurred_at TIMESTAMPTZ
) RETURNS TEXT
LANGUAGE plpgsql
VOLATILE
STRICT
AS $$
DECLARE
    new_event_id TEXT := gen_random_uuid()::TEXT;
    next_generation BIGINT;
BEGIN
    SELECT coalesce(max(event.event_generation), 0) + 1
      INTO next_generation
      FROM agent_control.runtime_attempt_lease_event AS event
     WHERE event.attempt_id = p_attempt_id;

    INSERT INTO agent_control.runtime_attempt_lease_event (
        event_id, schema_revision, attempt_id, event_generation,
        lease_generation, transition, worker_principal_id, lease_token,
        previous_expires_at, new_expires_at, actor, causation_id,
        correlation_id, occurred_at
    ) VALUES (
        new_event_id, 1, p_attempt_id, next_generation,
        p_lease_generation, 'released', p_worker_principal_id, p_lease_token,
        p_previous_expires_at, NULL,
        jsonb_build_object(
            'principal_id', p_worker_principal_id,
            'kind', 'workload',
            'audience', 'worker'
        ),
        p_causation_id, p_correlation_id, p_occurred_at
    );
    RETURN new_event_id;
END
$$;

-- Lock one exact committed Blob and the deterministic source binding through
-- which the authenticated Worker may read it.  Content is locked FOR UPDATE
-- here because blob.bind_reference_internal() will take that same lock when
-- creating the Artifact-owned retention binding. Callers visit unique Blob
-- content digests, then Blob ids, in sorted order; content is deduplicated by
-- digest, so ordering by Blob id alone would not prevent a cross-Artifact
-- deadlock cycle.
CREATE FUNCTION agent_control.runtime_lock_worker_blob_source_binding(
    p_blob JSONB,
    p_principal_id TEXT
) RETURNS TABLE (
    binding_id TEXT,
    owner_principal TEXT
)
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
       AND object.committed_at = (p_blob->>'committed_at')::TIMESTAMPTZ
       AND object.state = 'committed'
       AND content.state = 'committed'
       AND reference.reference_owner = 'agent_control'
       AND reference.reference_record_type
           = p_blob #>> '{origin,record_type}'
       AND reference.reference_record_id = p_blob #>> '{origin,record_id}'
       AND reference.reference_record_digest::TEXT
           = p_blob #>> '{origin,record_digest}'
       AND reference.state = 'active'
       AND reference.retention_until > clock_timestamp()
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
     FOR UPDATE OF content;
    IF NOT FOUND THEN
        RETURN;
    END IF;

    -- The content lock above precedes this reference/ACL lock.  Reference
    -- release and ACL revocation must wait until terminalization commits.
    PERFORM 1
      FROM blob.blob_reference AS reference
     WHERE reference.binding_id = selected_binding
       AND reference.state = 'active'
       AND reference.retention_until > clock_timestamp()
     FOR SHARE;
    IF NOT FOUND THEN
        RETURN;
    END IF;
    IF selected_owner <> p_principal_id THEN
        IF selected_access <> 'explicit' THEN
            RETURN;
        END IF;
        PERFORM 1
          FROM blob.blob_acl AS acl
         WHERE acl.binding_id = selected_binding
           AND acl.principal_id = p_principal_id
           AND acl.state = 'active'
         FOR SHARE;
        IF NOT FOUND THEN
            RETURN;
        END IF;
    END IF;

    binding_id := selected_binding;
    owner_principal := selected_owner;
    RETURN NEXT;
END
$$;

CREATE FUNCTION agent_control.runtime_commit_attempt(p_command JSONB)
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
    result_row agent_control.runtime_model_call_result%ROWTYPE;
    contract_row agent_control.output_contract_revision%ROWTYPE;
    runtime_policy_row agent_control.runtime_policy_revision%ROWTYPE;
    policy_row blob.storage_policy%ROWTYPE;
    envelope JSONB;
    artifact_candidate JSONB;
    principal TEXT;
    lookup_run_id TEXT;
    lookup_task_id TEXT;
    lookup_session_id TEXT;
    expected_attempt_generation BIGINT;
    expected_lease_generation BIGINT;
    now_at TIMESTAMPTZ;
    artifact_id_value TEXT := gen_random_uuid()::TEXT;
    artifact_body JSONB;
    artifact_digest CHAR(64);
    intent_id_value TEXT := gen_random_uuid()::TEXT;
    intent_body JSONB;
    intent_digest CHAR(64);
    retention_until_value TIMESTAMPTZ;
    section_record RECORD;
    source_binding RECORD;
    source_bindings JSONB := '{}'::JSONB;
    response JSONB;
BEGIN
    IF p_command IS NULL
       OR NOT agent_control.runtime_commit_attempt_command_valid(p_command) THEN
        RAISE EXCEPTION USING ERRCODE = '22023',
            MESSAGE = 'invalid commit_attempt command';
    END IF;
    envelope := p_command->'envelope';
    artifact_candidate := p_command->'artifact';
    principal := envelope #>> '{actor,principal_id}';
    expected_attempt_generation :=
        (p_command->>'expected_attempt_state_generation')::BIGINT;
    expected_lease_generation :=
        (p_command->>'lease_generation')::BIGINT;

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

    -- Canonical mutable-row order: Run -> Task -> Session -> Attempt -> all
    -- Turns -> budget ancestry -> Blob content/source bindings.
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
       OR NOT task_row.budget_slot_held OR session_row.state <> 'open'
       OR attempt_row.state <> 'executing' THEN
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

    PERFORM turn.turn_id
      FROM agent_control.runtime_turn AS turn
     WHERE turn.attempt_id = attempt_row.attempt_id
     ORDER BY turn.ordinal, turn.turn_id
     FOR UPDATE;
    IF EXISTS (
        SELECT 1
          FROM agent_control.runtime_turn AS turn
         WHERE turn.attempt_id = attempt_row.attempt_id
           AND (
               turn.state IN ('planned', 'dispatched', 'unknown')
               OR turn.reservation_held
           )
    ) THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'unresolved_turn_exists'
        );
    END IF;

    -- Result identity is never inferred from "the latest" Turn.  The exact
    -- immutable RecordRef supplied by the Worker must resolve to this Attempt.
    SELECT * INTO result_row
      FROM agent_control.runtime_model_call_result AS result
     WHERE result.result_id = p_command #>> '{result,record_id}'
       AND result.record_digest::TEXT
           = p_command #>> '{result,record_digest}'
       AND result.attempt_id = attempt_row.attempt_id
     FOR SHARE;
    IF NOT FOUND THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'result_lineage_mismatch'
        );
    END IF;
    IF NOT EXISTS (
        SELECT 1
          FROM agent_control.runtime_turn AS turn
         WHERE turn.turn_id = result_row.turn_id
           AND turn.attempt_id = attempt_row.attempt_id
           AND turn.state = 'result_committed'
           AND turn.result_id = result_row.result_id
           AND turn.result_digest = result_row.record_digest
    ) THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'result_turn_mismatch'
        );
    END IF;

    SELECT * INTO contract_row
      FROM agent_control.output_contract_revision AS contract
     WHERE contract.revision_id = task_row.output_contract_revision_id
       AND contract.generation = task_row.output_contract_generation
       AND contract.record_digest = task_row.output_contract_digest
     FOR SHARE;
    IF NOT FOUND
       OR artifact_candidate->>'output_contract_digest'
            <> task_row.output_contract_digest::TEXT
       OR artifact_candidate->>'artifact_type' <> contract_row.artifact_type
       OR artifact_candidate->>'effect_class' <> contract_row.effect_class
       OR contract_row.effect_class <> 'none' THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'artifact_contract_mismatch'
        );
    END IF;
    SELECT * INTO STRICT runtime_policy_row
      FROM agent_control.runtime_policy_revision AS policy
     WHERE policy.policy_id = run_row.runtime_policy_id
       AND policy.generation = run_row.runtime_policy_generation
       AND policy.record_digest = run_row.runtime_policy_digest
     FOR SHARE;
    IF jsonb_array_length(artifact_candidate->'sections')
       > runtime_policy_row.max_artifact_sections THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'artifact_section_limit_exceeded'
        );
    END IF;

    -- The normalized section envelope is exact and at least one required
    -- Section must be the source Result output byte-for-byte as a BlobRef.
    -- OutputContract's custom JSON-schema bytes are not stored here (only
    -- their immutable Blob metadata is), and CommitAttemptCommand carries no
    -- validator receipt.  AP1 therefore accepts only this structural,
    -- effect=none envelope; a later Control-service validation receipt must be
    -- installed before downstream AP2 consumption.  This SQL transaction does
    -- not pretend that it inspected the custom schema bytes.
    IF NOT EXISTS (
        SELECT 1
          FROM jsonb_array_elements(
              artifact_candidate->'sections'
          ) AS section(value)
         WHERE (section.value->>'required')::BOOLEAN
           AND section.value->'content' = result_row.output
    ) THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'artifact_source_output_missing'
        );
    END IF;

    -- Own the full budget ancestry before acquiring any Blob lock.  This is
    -- the same ordering used by outcome settlement and leaves the final fence
    -- check after every potentially blocking Result/budget/Blob operation.
    IF NOT agent_control.runtime_lock_budget_ancestors(
        run_row.run_id, task_row.budget_ledger_id, false
    ) THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'budget_unavailable'
        );
    END IF;
    SELECT * INTO STRICT policy_row
      FROM blob.storage_policy AS policy
     WHERE policy.singleton
     FOR SHARE;

    -- One durable Artifact reference is created per unique Section Blob.  The
    -- source binding selected here proves owner or explicit-ACL Worker access;
    -- its real owner_principal is retained rather than forged as the Worker.
    FOR section_record IN
        WITH unique_blobs AS (
            SELECT DISTINCT ON (section.value #>> '{content,blob_id}')
                   section.value->'content' AS content,
                   section.value #>> '{content,blob_id}' AS blob_id
              FROM jsonb_array_elements(artifact_candidate->'sections')
                   WITH ORDINALITY AS section(value, ordinal)
             ORDER BY section.value #>> '{content,blob_id}', section.ordinal
        )
        SELECT content, blob_id
          FROM unique_blobs
         ORDER BY content->>'content_digest', blob_id
    LOOP
        SELECT * INTO source_binding
          FROM agent_control.runtime_lock_worker_blob_source_binding(
              section_record.content, principal
          );
        IF NOT FOUND THEN
            RETURN agent_control.runtime_deny_command(
                command_row, 'artifact_blob_unavailable'
            );
        END IF;
        source_bindings := source_bindings || jsonb_build_object(
            section_record.blob_id,
            jsonb_build_object(
                'binding_id', source_binding.binding_id,
                'owner_principal', source_binding.owner_principal
            )
        );
    END LOOP;

    -- Re-read the exact Attempt only after Result, budget, storage policy,
    -- Blob content, source binding, and ACL locks are held.  Natural expiry is
    -- checked again even though the underlying rows cannot now be mutated.
    SELECT * INTO attempt_row
      FROM agent_control.runtime_attempt AS attempt
     WHERE attempt.attempt_id = p_command->>'attempt_id'
       AND attempt.run_id = run_row.run_id
       AND attempt.task_id = task_row.task_id
       AND attempt.session_id = session_row.session_id
     FOR UPDATE;
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
       OR result_row.committed_at > now_at
       OR EXISTS (
           SELECT 1
             FROM jsonb_array_elements(
                 artifact_candidate->'sections'
             ) AS section(value)
            WHERE (section.value #>> '{content,committed_at}')::TIMESTAMPTZ
                    > now_at
       ) THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'commit_fence_expired'
        );
    END IF;
    IF EXISTS (
        SELECT 1
          FROM jsonb_each(source_bindings) AS source(blob_id, facts)
          LEFT JOIN blob.blob_reference AS reference
            ON reference.binding_id = source.facts->>'binding_id'
           AND reference.blob_id = source.blob_id::UUID
           AND reference.owner_principal
               = source.facts->>'owner_principal'
         WHERE reference.binding_id IS NULL
            OR reference.state <> 'active'
            OR reference.retention_until <= now_at
            OR NOT (
                reference.owner_principal = principal
                OR (
                    reference.access_class = 'explicit'
                    AND EXISTS (
                        SELECT 1
                          FROM blob.blob_acl AS acl
                         WHERE acl.binding_id = reference.binding_id
                           AND acl.principal_id = principal
                           AND acl.state = 'active'
                    )
                )
            )
    ) THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'artifact_blob_access_expired'
        );
    END IF;
    IF task_row.task_id = run_row.root_task_id
       AND EXISTS (
           SELECT 1
             FROM agent_control.runtime_task AS other_task
            WHERE other_task.run_id = run_row.run_id
              AND other_task.task_id <> task_row.task_id
              AND NOT agent_control.runtime_terminal_state(
                  'task', other_task.state
              )
       ) THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'root_tasks_not_terminal'
        );
    END IF;

    artifact_body := jsonb_build_object(
        'schema_revision', 1,
        'artifact_id', artifact_id_value,
        'run_id', run_row.run_id,
        'task_id', task_row.task_id,
        'session_id', session_row.session_id,
        'attempt_id', attempt_row.attempt_id,
        'source_result', jsonb_build_object(
            'owner', 'agent_control',
            'record_type', 'model_call_result',
            'record_id', result_row.result_id,
            'schema_revision', 1,
            'record_digest', result_row.record_digest::TEXT
        ),
        'artifact_type', contract_row.artifact_type,
        'output_contract_digest', task_row.output_contract_digest::TEXT,
        'effect_class', 'none',
        'sections', artifact_candidate->'sections',
        'created_at', agent_control.runtime_utc_text(now_at)
    );
    artifact_digest := agent_control.runtime_contract_digest(
        'agent-platform.contract.artifact.v1', artifact_body
    );
    retention_until_value := clock_timestamp() + make_interval(
        secs => policy_row.max_retention_seconds::DOUBLE PRECISION
    );

    -- Artifact bindings are private to the source owner.  This preserves the
    -- bytes without silently widening ACLs; later readers must cross their own
    -- owner/ACL boundary. bind_reference_internal emits lifecycle events.
    FOR section_record IN
        SELECT source.blob_id,
               source.facts->>'owner_principal' AS owner_principal
          FROM jsonb_each(source_bindings) AS source(blob_id, facts)
         ORDER BY source.blob_id
    LOOP
        IF NOT blob.bind_reference_internal(
            'agent_control',
            'artifact:' || artifact_id_value || ':blob:'
                || section_record.blob_id,
            section_record.blob_id::UUID,
            'artifact', artifact_id_value, artifact_digest::TEXT,
            section_record.owner_principal, 'private',
            retention_until_value, principal
        ) THEN
            RAISE EXCEPTION USING ERRCODE = '40001',
                MESSAGE = 'artifact blob binding unexpectedly existed';
        END IF;
    END LOOP;

    INSERT INTO agent_control.runtime_artifact (
        artifact_id, schema_revision, record_digest, run_id, task_id,
        session_id, attempt_id, source_result_owner,
        source_result_record_type, source_result_id,
        source_result_schema_revision, source_result_digest,
        artifact_type, output_contract_digest, effect_class, created_at
    ) VALUES (
        artifact_id_value, 1, artifact_digest, run_row.run_id,
        task_row.task_id, session_row.session_id, attempt_row.attempt_id,
        'agent_control', 'model_call_result', result_row.result_id, 1,
        result_row.record_digest, contract_row.artifact_type,
        task_row.output_contract_digest, 'none', now_at
    );
    INSERT INTO agent_control.runtime_artifact_section (
        artifact_id, ordinal, name, required, content
    )
    SELECT artifact_id_value, section.ordinal::INTEGER,
           section.value->>'name',
           (section.value->>'required')::BOOLEAN,
           section.value->'content'
      FROM jsonb_array_elements(artifact_candidate->'sections')
           WITH ORDINALITY AS section(value, ordinal)
     ORDER BY section.ordinal;

    intent_body := jsonb_build_object(
        'schema_revision', 1,
        'intent_id', intent_id_value,
        'artifact', jsonb_build_object(
            'owner', 'agent_control',
            'record_type', 'artifact',
            'record_id', artifact_id_value,
            'schema_revision', 1,
            'record_digest', artifact_digest::TEXT
        ),
        'state', 'disabled',
        'reason_code', 'ap8_not_installed',
        'created_at', agent_control.runtime_utc_text(now_at)
    );
    intent_digest := agent_control.runtime_contract_digest(
        'agent-platform.contract.artifact_publication_intent.v1',
        intent_body
    );
    INSERT INTO agent_control.runtime_artifact_publication_intent (
        intent_id, schema_revision, record_digest, artifact_owner,
        artifact_record_type, artifact_id, artifact_schema_revision,
        artifact_digest, state, reason_code, created_at
    ) VALUES (
        intent_id_value, 1, intent_digest, 'agent_control', 'artifact',
        artifact_id_value, 1, artifact_digest, 'disabled',
        'ap8_not_installed', now_at
    );
    PERFORM agent_control.runtime_insert_event(
        'publication_intent', intent_id_value, NULL, 'disabled', 1,
        principal, envelope->>'causation_id', envelope->>'correlation_id',
        'publication_disabled', now_at
    );

    UPDATE agent_control.runtime_attempt
       SET state = 'result_committed',
           state_generation = attempt_row.state_generation + 1,
           result_artifact_owner = 'agent_control',
           result_artifact_record_type = 'artifact',
           result_artifact_id = artifact_id_value,
           result_artifact_schema_revision = 1,
           result_artifact_digest = artifact_digest,
           failure = NULL,
           updated_at = greatest(now_at, attempt_row.updated_at),
           terminal_at = now_at
     WHERE attempt_id = attempt_row.attempt_id;
    PERFORM agent_control.runtime_insert_attempt_release_event(
        attempt_row.attempt_id, attempt_row.lease_generation, principal,
        attempt_row.lease_token, attempt_row.lease_expires_at,
        envelope->>'causation_id', envelope->>'correlation_id', now_at
    );
    PERFORM agent_control.runtime_insert_event(
        'attempt', attempt_row.attempt_id, 'executing', 'result_committed',
        attempt_row.state_generation + 1, principal,
        envelope->>'causation_id', envelope->>'correlation_id',
        'attempt_result_committed', now_at
    );

    UPDATE agent_control.runtime_task
       SET state = 'result_committed',
           state_generation = task_row.state_generation + 1,
           result_artifact_id = artifact_id_value,
           updated_at = greatest(now_at, task_row.updated_at)
     WHERE task_id = task_row.task_id;
    PERFORM agent_control.runtime_insert_event(
        'task', task_row.task_id, 'running', 'result_committed',
        task_row.state_generation + 1, principal,
        envelope->>'causation_id', envelope->>'correlation_id',
        'task_result_committed', now_at
    );
    UPDATE agent_control.runtime_task
       SET state = 'succeeded',
           state_generation = task_row.state_generation + 2,
           budget_slot_held = false,
           updated_at = greatest(now_at, task_row.updated_at),
           terminal_at = now_at
     WHERE task_id = task_row.task_id;
    PERFORM agent_control.runtime_insert_event(
        'task', task_row.task_id, 'result_committed', 'succeeded',
        task_row.state_generation + 2, principal,
        envelope->>'causation_id', envelope->>'correlation_id',
        'task_succeeded', now_at
    );

    UPDATE agent_control.runtime_session
       SET state = 'closed', generation = session_row.generation + 1,
           closed_at = now_at
     WHERE session_id = session_row.session_id;
    PERFORM agent_control.runtime_insert_event(
        'session', session_row.session_id, 'open', 'closed',
        session_row.generation + 1, principal,
        envelope->>'causation_id', envelope->>'correlation_id',
        'session_closed', now_at
    );

    IF NOT agent_control.runtime_release_active_slot_ancestors(
        run_row.run_id, task_row.budget_ledger_id, now_at
    ) THEN
        RAISE EXCEPTION USING ERRCODE = '40001',
            MESSAGE = 'active Task slot changed during commit';
    END IF;

    IF task_row.task_id = run_row.root_task_id THEN
        UPDATE agent_control.runtime_run
           SET state = 'succeeded',
               state_generation = run_row.state_generation + 1,
               updated_at = greatest(now_at, run_row.updated_at),
               terminal_at = now_at
         WHERE run_id = run_row.run_id;
        PERFORM agent_control.runtime_insert_event(
            'run', run_row.run_id, 'running', 'succeeded',
            run_row.state_generation + 1, principal,
            envelope->>'causation_id', envelope->>'correlation_id',
            'run_succeeded', now_at
        );
    END IF;

    response := jsonb_build_object(
        'schema_revision', 1,
        'status', 'committed',
        'command_id', command_row.command_id,
        'attempt_id', attempt_row.attempt_id,
        'attempt_state', 'result_committed',
        'attempt_state_generation', attempt_row.state_generation + 1,
        'task_id', task_row.task_id,
        'task_state', 'succeeded',
        'task_state_generation', task_row.state_generation + 2,
        'session_id', session_row.session_id,
        'session_state', 'closed',
        'artifact_id', artifact_id_value,
        'artifact_digest', artifact_digest::TEXT,
        'publication_intent_id', intent_id_value,
        'publication_state', 'disabled'
    );
    IF task_row.task_id = run_row.root_task_id THEN
        response := response || jsonb_build_object(
            'run_id', run_row.run_id,
            'run_state', 'succeeded',
            'run_state_generation', run_row.state_generation + 1
        );
    END IF;
    RETURN agent_control.runtime_finish_command(
        command_row, 'committed', response
    );
END
$$;

CREATE FUNCTION agent_control.runtime_fail_attempt(p_command JSONB)
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
    expected_attempt_generation BIGINT;
    expected_lease_generation BIGINT;
    retry_class_value TEXT;
    retry_granted BOOLEAN := false;
    task_terminal_state TEXT;
    other_nonterminal_tasks BOOLEAN := false;
    run_transitioned BOOLEAN := false;
    now_at TIMESTAMPTZ;
    response JSONB;
BEGIN
    IF p_command IS NULL
       OR NOT agent_control.runtime_fail_attempt_command_valid(p_command) THEN
        RAISE EXCEPTION USING ERRCODE = '22023',
            MESSAGE = 'invalid fail_attempt command';
    END IF;
    envelope := p_command->'envelope';
    principal := envelope #>> '{actor,principal_id}';
    expected_attempt_generation :=
        (p_command->>'expected_attempt_state_generation')::BIGINT;
    expected_lease_generation :=
        (p_command->>'lease_generation')::BIGINT;
    retry_class_value := p_command->>'retry_class';

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
       OR NOT task_row.budget_slot_held OR session_row.state <> 'open'
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

    PERFORM turn.turn_id
      FROM agent_control.runtime_turn AS turn
     WHERE turn.attempt_id = attempt_row.attempt_id
     ORDER BY turn.ordinal, turn.turn_id
     FOR UPDATE;
    IF EXISTS (
        SELECT 1
          FROM agent_control.runtime_turn AS turn
         WHERE turn.attempt_id = attempt_row.attempt_id
           AND (
               turn.state IN ('planned', 'dispatched', 'unknown')
               OR turn.reservation_held
           )
    ) THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'unresolved_turn_exists'
        );
    END IF;

    IF NOT agent_control.runtime_lock_budget_ancestors(
        run_row.run_id, task_row.budget_ledger_id, false
    ) THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'budget_unavailable'
        );
    END IF;

    -- The budget ancestry lock may outlive both command and Attempt leases.
    -- No failure, retry charge, slot release, or state transition precedes this
    -- final database-time fence.
    SELECT * INTO attempt_row
      FROM agent_control.runtime_attempt AS attempt
     WHERE attempt.attempt_id = p_command->>'attempt_id'
       AND attempt.run_id = run_row.run_id
       AND attempt.task_id = task_row.task_id
       AND attempt.session_id = session_row.session_id
     FOR UPDATE;
    now_at := clock_timestamp();
    IF now_at >= command_row.deadline_at
       OR now_at >= run_row.deadline_at OR now_at >= task_row.deadline_at
       OR now_at >= attempt_row.lease_expires_at
       OR attempt_row.state NOT IN ('leased', 'executing')
       OR attempt_row.state_generation <> expected_attempt_generation
       OR attempt_row.lease_generation <> expected_lease_generation
       OR attempt_row.lease_token::TEXT <> p_command->>'lease_token'
       OR attempt_row.lease_worker #>> '{principal_id}' <> principal
       OR attempt_row.lease_worker #>> '{kind}' <> 'workload'
       OR attempt_row.lease_worker #>> '{audience}' <> 'worker' THEN
        RETURN agent_control.runtime_deny_command(
            command_row, 'fail_fence_expired'
        );
    END IF;

    IF retry_class_value <> 'none' THEN
        retry_granted :=
            agent_control.runtime_charge_retry_budget_ancestors(
                run_row.run_id, task_row.budget_ledger_id,
                retry_class_value, principal,
                envelope->>'causation_id', envelope->>'correlation_id',
                now_at
            );
    END IF;
    IF retry_class_value = 'none' THEN
        task_terminal_state := 'failed';
    ELSIF NOT retry_granted THEN
        task_terminal_state := 'dead_lettered';
    END IF;
    IF task_row.task_id = run_row.root_task_id THEN
        SELECT EXISTS (
            SELECT 1
              FROM agent_control.runtime_task AS other_task
             WHERE other_task.run_id = run_row.run_id
               AND other_task.task_id <> task_row.task_id
               AND NOT agent_control.runtime_terminal_state(
                   'task', other_task.state
               )
        ) INTO other_nonterminal_tasks;
    END IF;

    UPDATE agent_control.runtime_attempt
       SET state = 'failed',
           state_generation = attempt_row.state_generation + 1,
           failure = p_command->'failure',
           updated_at = greatest(now_at, attempt_row.updated_at),
           terminal_at = now_at
     WHERE attempt_id = attempt_row.attempt_id;
    PERFORM agent_control.runtime_insert_attempt_release_event(
        attempt_row.attempt_id, attempt_row.lease_generation, principal,
        attempt_row.lease_token, attempt_row.lease_expires_at,
        envelope->>'causation_id', envelope->>'correlation_id', now_at
    );
    PERFORM agent_control.runtime_insert_event(
        'attempt', attempt_row.attempt_id, attempt_row.state, 'failed',
        attempt_row.state_generation + 1, principal,
        envelope->>'causation_id', envelope->>'correlation_id',
        'attempt_failed', now_at
    );

    IF retry_granted THEN
        UPDATE agent_control.runtime_task
           SET state = 'waiting',
               state_generation = task_row.state_generation + 1,
               updated_at = greatest(now_at, task_row.updated_at)
         WHERE task_id = task_row.task_id;
        PERFORM agent_control.runtime_insert_event(
            'task', task_row.task_id, 'running', 'waiting',
            task_row.state_generation + 1, principal,
            envelope->>'causation_id', envelope->>'correlation_id',
            'task_retry_waiting', now_at
        );
        IF task_row.task_id = run_row.root_task_id
           AND NOT other_nonterminal_tasks THEN
            UPDATE agent_control.runtime_run
               SET state = 'waiting',
                   state_generation = run_row.state_generation + 1,
                   updated_at = greatest(now_at, run_row.updated_at)
             WHERE run_id = run_row.run_id;
            PERFORM agent_control.runtime_insert_event(
                'run', run_row.run_id, 'running', 'waiting',
                run_row.state_generation + 1, principal,
                envelope->>'causation_id', envelope->>'correlation_id',
                'run_retry_waiting', now_at
            );
            run_transitioned := true;
        END IF;
    ELSE
        UPDATE agent_control.runtime_task
           SET state = task_terminal_state,
               state_generation = task_row.state_generation + 1,
               budget_slot_held = false,
               failure = p_command->'failure',
               updated_at = greatest(now_at, task_row.updated_at),
               terminal_at = now_at
         WHERE task_id = task_row.task_id;
        PERFORM agent_control.runtime_insert_event(
            'task', task_row.task_id, 'running', task_terminal_state,
            task_row.state_generation + 1, principal,
            envelope->>'causation_id', envelope->>'correlation_id',
            CASE WHEN task_terminal_state = 'failed'
                 THEN 'task_failed' ELSE 'task_dead_lettered' END,
            now_at
        );

        UPDATE agent_control.runtime_session
           SET state = 'closed', generation = session_row.generation + 1,
               closed_at = now_at
         WHERE session_id = session_row.session_id;
        PERFORM agent_control.runtime_insert_event(
            'session', session_row.session_id, 'open', 'closed',
            session_row.generation + 1, principal,
            envelope->>'causation_id', envelope->>'correlation_id',
            'session_closed', now_at
        );
        IF NOT agent_control.runtime_release_active_slot_ancestors(
            run_row.run_id, task_row.budget_ledger_id, now_at
        ) THEN
            RAISE EXCEPTION USING ERRCODE = '40001',
                MESSAGE = 'active Task slot changed during failure';
        END IF;

        IF task_row.task_id = run_row.root_task_id
           AND NOT other_nonterminal_tasks THEN
            UPDATE agent_control.runtime_run
               SET state = task_terminal_state,
                   state_generation = run_row.state_generation + 1,
                   failure = p_command->'failure',
                   updated_at = greatest(now_at, run_row.updated_at),
                   terminal_at = now_at
             WHERE run_id = run_row.run_id;
            PERFORM agent_control.runtime_insert_event(
                'run', run_row.run_id, 'running', task_terminal_state,
                run_row.state_generation + 1, principal,
                envelope->>'causation_id', envelope->>'correlation_id',
                CASE WHEN task_terminal_state = 'failed'
                     THEN 'run_failed' ELSE 'run_dead_lettered' END,
                now_at
            );
            run_transitioned := true;
        END IF;
    END IF;

    response := jsonb_build_object(
        'schema_revision', 1,
        'status', 'committed',
        'command_id', command_row.command_id,
        'attempt_id', attempt_row.attempt_id,
        'attempt_state', 'failed',
        'attempt_state_generation', attempt_row.state_generation + 1,
        'task_id', task_row.task_id,
        'task_state', CASE WHEN retry_granted
                           THEN 'waiting' ELSE task_terminal_state END,
        'task_state_generation', task_row.state_generation + 1,
        'retry_class', retry_class_value,
        'retry_scheduled', retry_granted
    );
    IF task_row.task_id = run_row.root_task_id THEN
        response := response || jsonb_build_object(
            'run_id', run_row.run_id,
            'run_state', CASE
                WHEN run_transitioned AND retry_granted THEN 'waiting'
                WHEN run_transitioned THEN task_terminal_state
                ELSE run_row.state
            END,
            'run_state_generation', run_row.state_generation
                + CASE WHEN run_transitioned THEN 1 ELSE 0 END
        );
    END IF;
    RETURN agent_control.runtime_finish_command(
        command_row, 'committed', response
    );
END
$$;

-- The only new Worker surface accepts raw TEXT so duplicate keys, exponent
-- numbers, and other lexical ambiguity cannot disappear before validation.
CREATE FUNCTION agent_control.commit_attempt(p_command TEXT)
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
            MESSAGE = 'invalid raw commit_attempt command';
    END IF;
    RETURN agent_control.runtime_commit_attempt(parsed);
END
$$;

CREATE FUNCTION agent_control.fail_attempt(p_command TEXT)
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
            MESSAGE = 'invalid raw fail_attempt command';
    END IF;
    RETURN agent_control.runtime_fail_attempt(parsed);
END
$$;

-- Every implementation helper stays migrator-private.  The Worker receives
-- only the two raw command functions and no direct Runtime/Blob table access,
-- publication capability, or effect path.
REVOKE ALL ON FUNCTION
    agent_control.runtime_artifact_section_candidate_valid(JSONB)
FROM PUBLIC;
REVOKE ALL ON FUNCTION
    agent_control.runtime_artifact_candidate_valid(JSONB)
FROM PUBLIC;
REVOKE ALL ON FUNCTION
    agent_control.runtime_commit_attempt_command_valid(JSONB)
FROM PUBLIC;
REVOKE ALL ON FUNCTION
    agent_control.runtime_fail_attempt_command_valid(JSONB)
FROM PUBLIC;
REVOKE ALL ON FUNCTION
    agent_control.runtime_charge_retry_budget_ancestors(
        TEXT, TEXT, TEXT, TEXT, TEXT, TEXT, TIMESTAMPTZ
    )
FROM PUBLIC;
REVOKE ALL ON FUNCTION
    agent_control.runtime_release_active_slot_ancestors(
        TEXT, TEXT, TIMESTAMPTZ
    )
FROM PUBLIC;
REVOKE ALL ON FUNCTION
    agent_control.runtime_insert_attempt_release_event(
        TEXT, BIGINT, TEXT, UUID, TIMESTAMPTZ, TEXT, TEXT, TIMESTAMPTZ
    )
FROM PUBLIC;
REVOKE ALL ON FUNCTION
    agent_control.runtime_lock_worker_blob_source_binding(JSONB, TEXT)
FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.runtime_commit_attempt(JSONB)
FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.runtime_fail_attempt(JSONB)
FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.commit_attempt(TEXT) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.fail_attempt(TEXT) FROM PUBLIC;

GRANT EXECUTE ON FUNCTION agent_control.commit_attempt(TEXT)
    TO alpheus_agent_worker;
GRANT EXECUTE ON FUNCTION agent_control.fail_attempt(TEXT)
    TO alpheus_agent_worker;

RESET ROLE;
