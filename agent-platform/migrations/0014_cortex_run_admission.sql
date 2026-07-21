SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

-- OwnerPolicy activation remains credential-isolated from Cortex Control.
-- This command can only install the fixed effect=none user-request policy.
CREATE FUNCTION platform_governance.activate_cortex_user_request_policy()
RETURNS JSONB
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, platform_governance, platform_security, agent_control
SET timezone = 'UTC'
AS $$
DECLARE
    invoker RECORD;
    at_time TIMESTAMPTZ := clock_timestamp();
    policy_body JSONB;
    policy_digest CHAR(64);
BEGIN
    SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
    IF invoker.group_role::TEXT <> 'alpheus_agent_activator'
       OR invoker.profile_id <> 'activator'
       OR invoker.owner_id <> 'platform_governance' THEN
        RAISE EXCEPTION USING ERRCODE = '42501', MESSAGE = 'cortex owner policy activation denied';
    END IF;
    SELECT record_digest INTO policy_digest
    FROM platform_governance.owner_policy_revision
    WHERE revision_id = 'cortex-user-request-v1';
    IF NOT FOUND THEN
        policy_body := jsonb_build_object(
            'schema_revision', 1, 'policy_id', 'cortex-user-request',
            'revision_id', 'cortex-user-request-v1', 'generation', 1,
            'origin_kind', 'user_request', 'source_owner', 'agent_control',
            'source_record_type', 'user_request', 'initiating_kind', 'user',
            'initiating_audience', 'control_api', 'effect_ceiling', 'none',
            'author', jsonb_build_object('principal_id', invoker.principal_id,
                'kind', 'workload', 'audience', 'activator'),
            'reason_code', 'cortex_user_request_enabled',
            'created_at', agent_control.runtime_utc_text(at_time)
        );
        policy_digest := agent_control.runtime_contract_digest(
            'agent-platform.contract.owner_policy_revision.v1', policy_body);
        INSERT INTO platform_governance.owner_policy_revision (
            revision_id, schema_revision, policy_id, generation, record_digest,
            origin_kind, source_owner, source_record_type, initiating_kind,
            initiating_audience, initiating_principal_id, effect_ceiling,
            author_principal_id, author_kind, author_audience, reason_code, created_at
        ) VALUES (
            'cortex-user-request-v1', 1, 'cortex-user-request', 1, policy_digest,
            'user_request', 'agent_control', 'user_request', 'user', 'control_api',
            NULL, 'none', invoker.principal_id, 'workload', 'activator',
            'cortex_user_request_enabled', at_time
        );
        INSERT INTO platform_governance.owner_policy_head (
            head_id, schema_revision, generation, revision_id, revision_digest,
            activated_by_principal_id, activated_by_kind, activated_by_audience, activated_at
        ) VALUES ('cortex-user-request', 1, 1, 'cortex-user-request-v1', policy_digest,
            invoker.principal_id, 'workload', 'activator', at_time);
        INSERT INTO platform_governance.owner_policy_event (
            event_id, schema_revision, policy_id, generation,
            current_revision_id, current_revision_digest, actor_principal_id,
            actor_kind, actor_audience, reason_code, occurred_at
        ) VALUES (gen_random_uuid()::TEXT, 1, 'cortex-user-request', 1,
            'cortex-user-request-v1', policy_digest, invoker.principal_id,
            'workload', 'activator', 'cortex_user_request_enabled', at_time);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM platform_governance.owner_policy_head
        WHERE head_id = 'cortex-user-request' AND generation = 1
          AND revision_id = 'cortex-user-request-v1' AND revision_digest = policy_digest) THEN
        RAISE EXCEPTION USING ERRCODE = '23505', MESSAGE = 'cortex owner policy identity conflict';
    END IF;
    RETURN jsonb_build_object('status', 'active', 'policy_id', 'cortex-user-request',
        'revision_id', 'cortex-user-request-v1', 'record_digest', policy_digest);
END
$$;

-- Control owns the immutable operational policy and exact output schema, but
-- receives no direct definition-table privilege.
CREATE FUNCTION agent_control.ensure_cortex_mvp_runtime(p_schema JSONB)
RETURNS JSONB
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, agent_control, platform_security, blob
SET timezone = 'UTC'
AS $$
DECLARE
    invoker RECORD;
    at_time TIMESTAMPTZ := clock_timestamp();
    policy_body JSONB;
    output_body JSONB;
    policy_digest CHAR(64);
    output_digest CHAR(64);
BEGIN
    SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
    IF invoker.group_role::TEXT <> 'alpheus_agent_control_api'
       OR invoker.profile_id <> 'control-api' OR invoker.owner_id <> 'agent_control'
       OR NOT agent_control.runtime_blob_ref_valid(p_schema, 'output_contract_schema', '')
       OR p_schema->>'media_type' <> 'application/json'
       OR NOT EXISTS (SELECT 1 FROM blob.blob_object AS object
            WHERE object.blob_id = (p_schema->>'blob_id')::UUID
              AND object.state = 'committed' AND object.content_digest = p_schema->>'content_digest'
              AND object.origin_owner = 'agent_control'
              AND object.origin_record_type = 'output_contract_schema'
              AND object.origin_record_id = p_schema #>> '{origin,record_id}'
              AND object.origin_record_digest = p_schema #>> '{origin,record_digest}') THEN
        RAISE EXCEPTION USING ERRCODE = '42501', MESSAGE = 'cortex runtime definition denied';
    END IF;

    policy_body := jsonb_build_object(
        'schema_revision', 1, 'policy_id', 'cortex-mvp', 'generation', 1,
        'default_run_limit', jsonb_build_object(
            'max_model_calls', 32, 'max_input_tokens', 1000000,
            'max_output_tokens', 128000, 'max_tool_calls', 64,
            'max_external_cost_micro_usd', 10000000, 'max_wall_time_ms', 3600000,
            'max_idle_time_ms', 600000, 'max_tasks', 64, 'max_depth', 4,
            'max_fanout', 8, 'max_parallelism', 4,
            'max_invalid_output_retries', 2, 'max_infrastructure_retries', 3),
        'max_lease_seconds', 120, 'max_heartbeat_extension_seconds', 60,
        'max_claim_batch', 8, 'max_dependencies', 64, 'max_artifact_sections', 32,
        'dead_letter_retention_seconds', 604800,
        'updated_by', jsonb_build_object('principal_id', invoker.principal_id,
            'kind', 'workload', 'audience', 'control_api'),
        'updated_at', agent_control.runtime_utc_text(at_time));
    policy_digest := agent_control.runtime_contract_digest(
        'agent-platform.contract.runtime_policy.v1', policy_body);

    INSERT INTO agent_control.runtime_policy_revision (
        policy_id, schema_revision, generation, record_digest,
        max_model_calls, max_input_tokens, max_output_tokens, max_tool_calls,
        max_external_cost_micro_usd, max_wall_time_ms, max_idle_time_ms,
        max_tasks, max_depth, max_fanout, max_parallelism,
        max_invalid_output_retries, max_infrastructure_retries,
        max_lease_seconds, max_heartbeat_extension_seconds, max_claim_batch,
        max_dependencies, max_artifact_sections, dead_letter_retention_seconds,
        updated_by_principal_id, updated_by_kind, updated_by_audience, updated_at
    ) VALUES ('cortex-mvp', 1, 1, policy_digest, 32, 1000000, 128000, 64,
        10000000, 3600000, 600000, 64, 4, 8, 4, 2, 3, 120, 60, 8, 64, 32,
        604800, invoker.principal_id, 'workload', 'control_api', at_time)
    ON CONFLICT (policy_id, generation) DO NOTHING;
    IF NOT EXISTS (SELECT 1 FROM agent_control.runtime_policy_revision
        WHERE policy_id = 'cortex-mvp' AND generation = 1 AND record_digest = policy_digest) THEN
        -- An existing immutable definition is acceptable across restarts even
        -- though its original timestamp makes this invocation's digest differ.
        SELECT record_digest INTO policy_digest FROM agent_control.runtime_policy_revision
        WHERE policy_id = 'cortex-mvp' AND generation = 1;
    END IF;
    INSERT INTO agent_control.runtime_policy_head (
        policy_id, generation, record_digest, selected_by_principal_id,
        selected_by_kind, selected_by_audience, selected_at
    ) VALUES ('cortex-mvp', 1, policy_digest, invoker.principal_id,
        'workload', 'control_api', at_time) ON CONFLICT (policy_id) DO NOTHING;
    INSERT INTO agent_control.runtime_policy_event (
        event_id, policy_id, generation, current_record_digest,
        actor_principal_id, actor_kind, actor_audience, reason_code, occurred_at
    ) VALUES (gen_random_uuid()::TEXT, 'cortex-mvp', 1, policy_digest,
        invoker.principal_id, 'workload', 'control_api', 'cortex_mvp_selected', at_time)
    ON CONFLICT (policy_id, generation) DO NOTHING;

    output_body := jsonb_build_object(
        'schema_revision', 1, 'revision_id', 'cortex-text-output-v1', 'generation', 1,
        'artifact_type', 'assistant_response', 'schema', p_schema, 'effect_class', 'none',
        'author', jsonb_build_object('principal_id', invoker.principal_id,
            'kind', 'workload', 'audience', 'control_api'),
        'reason_code', 'cortex_mvp_output', 'created_at', agent_control.runtime_utc_text(at_time));
    output_digest := agent_control.runtime_contract_digest(
        'agent-platform.contract.output_contract_revision.v1', output_body);
    INSERT INTO agent_control.output_contract_revision (
        revision_id, schema_revision, generation, record_digest, artifact_type,
        schema_blob_schema_revision, schema_blob_id, schema_blob_content_digest,
        schema_blob_media_type, schema_blob_size_bytes, schema_origin_owner,
        schema_origin_record_type, schema_origin_record_id,
        schema_origin_schema_revision, schema_origin_record_digest,
        schema_blob_committed_at, effect_class, author_principal_id, author_kind,
        author_audience, reason_code, created_at
    ) VALUES ('cortex-text-output-v1', 1, 1, output_digest, 'assistant_response',
        1, (p_schema->>'blob_id')::UUID, p_schema->>'content_digest', p_schema->>'media_type',
        (p_schema->>'size_bytes')::BIGINT, 'agent_control', 'output_contract_schema',
        p_schema #>> '{origin,record_id}', 1, p_schema #>> '{origin,record_digest}',
        (p_schema->>'committed_at')::TIMESTAMPTZ, 'none', invoker.principal_id,
        'workload', 'control_api', 'cortex_mvp_output', at_time)
    ON CONFLICT (revision_id) DO NOTHING;
    IF NOT EXISTS (SELECT 1 FROM agent_control.output_contract_revision
        WHERE revision_id = 'cortex-text-output-v1' AND schema_blob_id = (p_schema->>'blob_id')::UUID) THEN
        RAISE EXCEPTION USING ERRCODE = '23505', MESSAGE = 'cortex output contract identity conflict';
    END IF;
    SELECT record_digest INTO output_digest FROM agent_control.output_contract_revision
    WHERE revision_id = 'cortex-text-output-v1';
    RETURN jsonb_build_object('status', 'ready', 'runtime_policy_digest', policy_digest,
        'output_contract_digest', output_digest);
END
$$;

CREATE TABLE agent_control.cortex_run_admission (
    request_id TEXT PRIMARY KEY CHECK (agent_control.runtime_identifier_valid(request_id)),
    idempotency_key TEXT NOT NULL UNIQUE CHECK (agent_control.runtime_identifier_valid(idempotency_key)),
    body_fingerprint CHAR(64) NOT NULL CHECK (agent_control.runtime_digest_valid(body_fingerprint::TEXT)),
    response JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE TRIGGER cortex_run_admission_immutable
BEFORE UPDATE OR DELETE ON agent_control.cortex_run_admission
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();

CREATE FUNCTION agent_control.admit_cortex_user_request_run(p_command JSONB)
RETURNS JSONB
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, agent_control, agent_input, platform_governance, platform_security, blob
SET timezone = 'UTC'
AS $$
DECLARE
    invoker RECORD;
    request_row agent_input.user_request%ROWTYPE;
    conversation_row agent_input.conversation%ROWTYPE;
    policy agent_control.runtime_policy_revision%ROWTYPE;
    owner_policy platform_governance.owner_policy_revision%ROWTYPE;
    output_contract agent_control.output_contract_revision%ROWTYPE;
    existing agent_control.cortex_run_admission%ROWTYPE;
    objective JSONB := p_command->'objective';
    fingerprint CHAR(64);
    now_at TIMESTAMPTZ := clock_timestamp();
    deadline_at TIMESTAMPTZ;
    run_id_value TEXT := gen_random_uuid()::TEXT;
    task_id_value TEXT := gen_random_uuid()::TEXT;
    run_ledger_id TEXT := gen_random_uuid()::TEXT;
    task_ledger_id TEXT := gen_random_uuid()::TEXT;
    response JSONB;
    event_body JSONB;
BEGIN
    SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
    IF invoker.group_role::TEXT <> 'alpheus_agent_control_api' OR invoker.profile_id <> 'control-api'
       OR jsonb_typeof(p_command) <> 'object'
       OR NOT (p_command ?& ARRAY['request_id','idempotency_key','causation_id','correlation_id','deadline','objective'])
       OR p_command - ARRAY['request_id','idempotency_key','causation_id','correlation_id','deadline','objective'] <> '{}'::JSONB
       OR NOT agent_control.runtime_identifier_valid(p_command->>'request_id')
       OR NOT agent_control.runtime_identifier_valid(p_command->>'idempotency_key')
       OR NOT agent_control.runtime_identifier_valid(p_command->>'causation_id')
       OR NOT agent_control.runtime_identifier_valid(p_command->>'correlation_id')
       OR NOT agent_control.runtime_utc_instant_json(p_command->'deadline')
       OR NOT agent_control.runtime_blob_ref_valid(objective, 'task_objective', '')
       OR objective #>> '{origin,record_id}' <> p_command->>'request_id' THEN
        RAISE EXCEPTION USING ERRCODE = '22023', MESSAGE = 'invalid_cortex_run_admission';
    END IF;
    deadline_at := (p_command->>'deadline')::TIMESTAMPTZ;
    fingerprint := agent_control.runtime_sha256_json(p_command);
    SELECT * INTO existing FROM agent_control.cortex_run_admission
    WHERE request_id = p_command->>'request_id' OR idempotency_key = p_command->>'idempotency_key';
    IF FOUND THEN
        IF existing.request_id <> p_command->>'request_id'
           OR existing.idempotency_key <> p_command->>'idempotency_key'
           OR existing.body_fingerprint <> fingerprint THEN
            RAISE EXCEPTION USING ERRCODE = '23505', MESSAGE = 'cortex run admission identity conflict';
        END IF;
        RETURN existing.response;
    END IF;
    IF now_at >= deadline_at THEN
        RAISE EXCEPTION USING ERRCODE = '57014', MESSAGE = 'cortex run admission deadline expired';
    END IF;
    SELECT * INTO STRICT request_row FROM agent_input.user_request
    WHERE request_id = p_command->>'request_id' FOR SHARE;
    IF request_row.request_kind NOT IN ('new_request','continuation','additional_context','clarification_answer','correction') THEN
        RAISE EXCEPTION USING ERRCODE = '22023', MESSAGE = 'user request kind does not create a run';
    END IF;
    SELECT * INTO STRICT conversation_row FROM agent_input.conversation
    WHERE conversation_id = request_row.conversation_id AND record_digest = request_row.conversation_digest FOR SHARE;
    IF NOT EXISTS (SELECT 1 FROM blob.blob_object AS object
        WHERE object.blob_id = (objective->>'blob_id')::UUID AND object.state = 'committed'
          AND object.content_digest = objective->>'content_digest'
          AND object.origin_record_digest = objective #>> '{origin,record_digest}') THEN
        RAISE EXCEPTION USING ERRCODE = '23503', MESSAGE = 'task objective blob not committed';
    END IF;
    SELECT revision.* INTO STRICT owner_policy
    FROM platform_governance.owner_policy_head AS head
    JOIN platform_governance.owner_policy_revision AS revision
      ON revision.policy_id = head.head_id AND revision.generation = head.generation
     AND revision.revision_id = head.revision_id AND revision.record_digest = head.revision_digest
    WHERE revision.origin_kind = 'user_request' AND revision.effect_ceiling = 'none'
      AND (revision.initiating_principal_id IS NULL OR revision.initiating_principal_id = request_row.subject_principal_id)
    ORDER BY (revision.initiating_principal_id IS NOT NULL) DESC, revision.policy_id LIMIT 1 FOR SHARE OF head;
    SELECT revision.* INTO STRICT policy FROM agent_control.runtime_policy_head AS head
    JOIN agent_control.runtime_policy_revision AS revision
      ON revision.policy_id = head.policy_id AND revision.generation = head.generation
     AND revision.record_digest = head.record_digest
    WHERE head.policy_id = 'cortex-mvp' FOR SHARE OF head;
    SELECT * INTO STRICT output_contract FROM agent_control.output_contract_revision
    WHERE revision_id = 'cortex-text-output-v1';

    INSERT INTO agent_control.runtime_run (
        run_id, schema_revision, origin_kind, origin_source_owner,
        origin_source_record_type, origin_source_record_id, origin_source_schema_revision,
        origin_source_record_digest, origin_conversation_owner,
        origin_conversation_record_type, origin_conversation_record_id,
        origin_conversation_schema_revision, origin_conversation_record_digest,
        origin_initiating_principal_id, origin_initiating_kind, origin_initiating_audience,
        origin_owner_policy_owner, origin_owner_policy_record_type,
        origin_owner_policy_record_id, origin_owner_policy_schema_revision,
        origin_owner_policy_record_digest, origin_owner_policy_generation,
        origin_occurred_at, origin_observed_at, origin_committed_at,
        runtime_policy_owner, runtime_policy_record_type, runtime_policy_id,
        runtime_policy_schema_revision, runtime_policy_generation, runtime_policy_digest,
        budget_ledger_id, root_task_id, state, state_generation, created_at, updated_at, deadline_at
    ) VALUES (run_id_value, 1, 'user_request', 'agent_control', 'user_request',
        request_row.request_id, 1, request_row.record_digest, 'agent_control', 'conversation',
        conversation_row.conversation_id, 1, conversation_row.record_digest,
        request_row.subject_principal_id, 'user', 'control_api', 'platform_governance',
        'owner_policy_revision', owner_policy.revision_id, 1, owner_policy.record_digest,
        owner_policy.generation, request_row.created_at, request_row.created_at, request_row.created_at,
        'agent_control', 'runtime_policy', policy.policy_id, 1, policy.generation,
        policy.record_digest, run_ledger_id, task_id_value, 'queued', 1, now_at, now_at, deadline_at);

    INSERT INTO agent_control.runtime_budget_ledger (
        ledger_id, schema_revision, scope, scope_id, parent_ledger_id,
        runtime_policy_owner, runtime_policy_record_type, runtime_policy_id,
        runtime_policy_schema_revision, runtime_policy_generation, runtime_policy_digest,
        limit_model_calls, limit_input_tokens, limit_output_tokens, limit_tool_calls,
        limit_external_cost_micro_usd, limit_wall_time_ms, limit_idle_time_ms,
        limit_tasks, limit_depth, limit_fanout, limit_parallelism,
        limit_invalid_output_retries, limit_infrastructure_retries,
        consumed_tasks, generation, state, updated_at
    ) VALUES
      (run_ledger_id,1,'run',run_id_value,NULL,'agent_control','runtime_policy',policy.policy_id,1,policy.generation,policy.record_digest,
       policy.max_model_calls,policy.max_input_tokens,policy.max_output_tokens,policy.max_tool_calls,
       policy.max_external_cost_micro_usd,policy.max_wall_time_ms,policy.max_idle_time_ms,
       policy.max_tasks,policy.max_depth,policy.max_fanout,policy.max_parallelism,
       policy.max_invalid_output_retries,policy.max_infrastructure_retries,1,1,'open',now_at),
      (task_ledger_id,1,'task',task_id_value,run_ledger_id,'agent_control','runtime_policy',policy.policy_id,1,policy.generation,policy.record_digest,
       policy.max_model_calls,policy.max_input_tokens,policy.max_output_tokens,policy.max_tool_calls,
       policy.max_external_cost_micro_usd,policy.max_wall_time_ms,policy.max_idle_time_ms,
       1,policy.max_depth,policy.max_fanout,policy.max_parallelism,
       policy.max_invalid_output_retries,policy.max_infrastructure_retries,1,1,'open',now_at);

    INSERT INTO agent_control.runtime_task (
        task_id, schema_revision, run_id, depth, objective,
        output_contract_owner, output_contract_record_type,
        output_contract_revision_id, output_contract_schema_revision,
        output_contract_generation, output_contract_digest, budget_ledger_id,
        state, state_generation, budget_slot_held, created_at, updated_at, deadline_at
    ) VALUES (task_id_value,1,run_id_value,0,objective,'agent_control',
        'output_contract_revision',output_contract.revision_id,1,
        output_contract.generation,output_contract.record_digest,task_ledger_id,
        'ready',1,false,now_at,now_at,deadline_at);
    INSERT INTO agent_control.runtime_task_input_ref(task_id, ordinal, reference)
    VALUES (task_id_value, 1, jsonb_build_object('owner','agent_control',
        'record_type','user_request','record_id',request_row.request_id,
        'schema_revision',1,'record_digest',request_row.record_digest));

    event_body := jsonb_build_object('schema_revision',1,'event_id',gen_random_uuid()::TEXT,
        'subject','run','subject_id',run_id_value,'to_state','queued','generation',1,
        'actor',jsonb_build_object('principal_id',invoker.principal_id,'kind','workload','audience','control_api'),
        'causation_id',p_command->>'causation_id','correlation_id',p_command->>'correlation_id',
        'reason_code','user_request_admitted','occurred_at',agent_control.runtime_utc_text(now_at));
    INSERT INTO agent_control.runtime_event(event_id,schema_revision,record_digest,subject,subject_id,to_state,generation,actor,causation_id,correlation_id,reason_code,occurred_at)
    VALUES(event_body->>'event_id',1,agent_control.runtime_contract_digest('agent-platform.contract.runtime_event.v1',event_body),
        'run',run_id_value,'queued',1,event_body->'actor',p_command->>'causation_id',p_command->>'correlation_id','user_request_admitted',now_at);
    event_body := jsonb_build_object('schema_revision',1,'event_id',gen_random_uuid()::TEXT,
        'subject','task','subject_id',task_id_value,'to_state','ready','generation',1,
        'actor',jsonb_build_object('principal_id',invoker.principal_id,'kind','workload','audience','control_api'),
        'causation_id',p_command->>'causation_id','correlation_id',p_command->>'correlation_id',
        'reason_code','root_task_ready','occurred_at',agent_control.runtime_utc_text(now_at));
    INSERT INTO agent_control.runtime_event(event_id,schema_revision,record_digest,subject,subject_id,to_state,generation,actor,causation_id,correlation_id,reason_code,occurred_at)
    VALUES(event_body->>'event_id',1,agent_control.runtime_contract_digest('agent-platform.contract.runtime_event.v1',event_body),
        'task',task_id_value,'ready',1,event_body->'actor',p_command->>'causation_id',p_command->>'correlation_id','root_task_ready',now_at);

    response := jsonb_build_object('status','admitted','request_id',request_row.request_id,
        'run_id',run_id_value,'root_task_id',task_id_value,'run_state','queued','task_state','ready');
    INSERT INTO agent_control.cortex_run_admission(request_id,idempotency_key,body_fingerprint,response)
    VALUES(request_row.request_id,p_command->>'idempotency_key',fingerprint,response);
    RETURN response;
END
$$;

REVOKE ALL ON FUNCTION platform_governance.activate_cortex_user_request_policy() FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.ensure_cortex_mvp_runtime(JSONB) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.admit_cortex_user_request_run(JSONB) FROM PUBLIC;
REVOKE ALL ON TABLE agent_control.cortex_run_admission FROM PUBLIC;
GRANT USAGE ON SCHEMA platform_governance TO alpheus_agent_activator;
GRANT EXECUTE ON FUNCTION platform_governance.activate_cortex_user_request_policy() TO alpheus_agent_activator;
GRANT EXECUTE ON FUNCTION agent_control.ensure_cortex_mvp_runtime(JSONB) TO alpheus_agent_control_api;
GRANT EXECUTE ON FUNCTION agent_control.admit_cortex_user_request_run(JSONB) TO alpheus_agent_control_api;

RESET ROLE;
