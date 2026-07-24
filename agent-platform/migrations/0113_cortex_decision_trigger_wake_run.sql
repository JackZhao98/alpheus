-- Admit an effect=none Cortex Run from one materialized external-event
-- TriggerOccurrence. The wake owns a separate raw input, objective, budget,
-- Session and immutable admission record; it never masquerades as a UserRequest.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

CREATE TABLE agent_control.cortex_decision_trigger_wake_admission(
    occurrence_id TEXT PRIMARY KEY,
    occurrence_digest CHAR(64) NOT NULL,
    body_fingerprint CHAR(64) NOT NULL,
    raw_input JSONB NOT NULL CHECK(
        agent_control.runtime_blob_ref_valid(raw_input,'input_raw','')
    ),
    objective JSONB NOT NULL CHECK(
        agent_control.runtime_blob_ref_valid(objective,'task_objective','')
    ),
    run_id TEXT NOT NULL UNIQUE,
    root_task_id TEXT NOT NULL UNIQUE,
    response JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY(occurrence_id,occurrence_digest)
        REFERENCES agent_control.trigger_occurrence(
            occurrence_id,record_digest
        ),
    FOREIGN KEY(run_id) REFERENCES agent_control.runtime_run(run_id),
    FOREIGN KEY(root_task_id) REFERENCES agent_control.runtime_task(task_id)
);

CREATE TRIGGER cortex_decision_trigger_wake_admission_immutable
BEFORE UPDATE OR DELETE ON agent_control.cortex_decision_trigger_wake_admission
FOR EACH ROW EXECUTE FUNCTION
agent_control.reject_immutable_runtime_definition_mutation();

CREATE FUNCTION agent_control.admit_cortex_decision_trigger_wake(
    p_command JSONB
) RETURNS JSONB
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_governance,platform_security,blob
SET timezone='UTC'
AS $$
DECLARE
    invoker RECORD;
    at_time TIMESTAMPTZ:=clock_timestamp();
    deadline_at TIMESTAMPTZ;
    occurrence agent_control.trigger_occurrence%ROWTYPE;
    occurrence_link agent_control.cortex_decision_trigger_occurrence%ROWTYPE;
    registration agent_control.trigger_registration_revision%ROWTYPE;
    policy agent_control.runtime_policy_revision%ROWTYPE;
    owner_policy platform_governance.owner_policy_revision%ROWTYPE;
    output_contract agent_control.output_contract_revision%ROWTYPE;
    existing agent_control.cortex_decision_trigger_wake_admission%ROWTYPE;
    fingerprint CHAR(64);
    run_id_value TEXT:=gen_random_uuid()::TEXT;
    task_id_value TEXT:=gen_random_uuid()::TEXT;
    run_ledger_id TEXT:=gen_random_uuid()::TEXT;
    task_ledger_id TEXT:=gen_random_uuid()::TEXT;
    event_body JSONB;
    response_value JSONB;
BEGIN
    SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
    IF invoker.group_role::TEXT<>'alpheus_agent_control_api'
       OR invoker.profile_id<>'control-api'
       OR invoker.owner_id<>'agent_control'
       OR jsonb_typeof(p_command)<>'object'
       OR NOT(p_command ?& ARRAY[
            'occurrence_id','deadline','raw_input','objective'
       ])
       OR p_command-ARRAY[
            'occurrence_id','deadline','raw_input','objective'
       ]<>'{}'::JSONB
       OR NOT agent_control.runtime_identifier_valid(
            p_command->>'occurrence_id'
       )
       OR NOT agent_control.runtime_utc_instant_json(p_command->'deadline')
       OR NOT agent_control.runtime_blob_ref_valid(
            p_command->'raw_input','input_raw',''
       )
       OR NOT agent_control.runtime_blob_ref_valid(
            p_command->'objective','task_objective',''
       )
       OR p_command#>>'{raw_input,origin,record_id}'
            <>p_command->>'occurrence_id'
       OR p_command#>>'{objective,origin,record_id}'
            <>p_command->>'occurrence_id' THEN
        RAISE EXCEPTION USING ERRCODE='22023',
            MESSAGE='invalid Cortex decision Trigger wake';
    END IF;
    deadline_at:=(p_command->>'deadline')::TIMESTAMPTZ;
    IF deadline_at<=at_time+interval '90 seconds'
       OR deadline_at>at_time+interval '15 minutes' THEN
        RAISE EXCEPTION USING ERRCODE='57014',
            MESSAGE='Cortex decision Trigger wake deadline invalid';
    END IF;
    fingerprint:=agent_control.runtime_sha256_json(p_command);
    SELECT * INTO existing
    FROM agent_control.cortex_decision_trigger_wake_admission
    WHERE occurrence_id=p_command->>'occurrence_id';
    IF FOUND THEN
        IF existing.body_fingerprint<>fingerprint
           OR existing.raw_input<>p_command->'raw_input'
           OR existing.objective<>p_command->'objective' THEN
            RAISE EXCEPTION USING ERRCODE='23505',
                MESSAGE='Cortex decision Trigger wake identity conflict';
        END IF;
        RETURN existing.response;
    END IF;
    SELECT * INTO STRICT occurrence_link
    FROM agent_control.cortex_decision_trigger_occurrence
    WHERE occurrence_id=p_command->>'occurrence_id'
    FOR SHARE;
    SELECT * INTO STRICT occurrence
    FROM agent_control.trigger_occurrence
    WHERE occurrence_id=occurrence_link.occurrence_id
      AND record_digest=occurrence_link.occurrence_digest
      AND kind='external_event'
    FOR SHARE;
    SELECT registration_row.* INTO STRICT registration
    FROM agent_control.trigger_registration_head AS head
    JOIN agent_control.trigger_registration_revision AS registration_row
      ON registration_row.registration_id=head.registration_id
     AND registration_row.generation=head.generation
     AND registration_row.record_digest=head.record_digest
    WHERE head.registration_id=occurrence.registration_id
      AND head.generation=occurrence.registration_generation
      AND head.record_digest=occurrence.registration_digest
      AND registration_row.enabled
    FOR SHARE OF head;
    SELECT policy_row.* INTO STRICT policy
    FROM agent_control.runtime_policy_head AS head
    JOIN agent_control.runtime_policy_revision AS policy_row
      ON policy_row.policy_id=head.policy_id
     AND policy_row.generation=head.generation
     AND policy_row.record_digest=head.record_digest
    WHERE head.policy_id=registration.runtime_policy_record_id
      AND head.generation=registration.runtime_policy_generation
      AND head.record_digest=registration.runtime_policy_record_digest
    FOR SHARE OF head;
    SELECT owner_row.* INTO STRICT owner_policy
    FROM platform_governance.owner_policy_head AS head
    JOIN platform_governance.owner_policy_revision AS owner_row
      ON owner_row.policy_id=head.head_id
     AND owner_row.generation=head.generation
     AND owner_row.revision_id=head.revision_id
     AND owner_row.record_digest=head.revision_digest
    WHERE owner_row.revision_id=occurrence.owner_policy_record_id
      AND owner_row.generation=occurrence.owner_policy_generation
      AND owner_row.record_digest=occurrence.owner_policy_record_digest
      AND owner_row.effect_ceiling='none'
    FOR SHARE OF head;
    SELECT * INTO STRICT output_contract
    FROM agent_control.output_contract_revision
    WHERE revision_id='cortex-workflow-output-v8'
      AND effect_class='none';
    IF NOT EXISTS(
        SELECT 1 FROM blob.blob_object AS object
        WHERE object.blob_id=(p_command#>>'{raw_input,blob_id}')::UUID
          AND object.state='committed'
          AND object.origin_owner='agent_control'
          AND object.origin_record_type='input_raw'
          AND object.origin_record_id=p_command->>'occurrence_id'
          AND object.origin_record_digest=
                p_command#>>'{raw_input,origin,record_digest}'
    ) OR NOT EXISTS(
        SELECT 1 FROM blob.blob_object AS object
        WHERE object.blob_id=(p_command#>>'{objective,blob_id}')::UUID
          AND object.state='committed'
          AND object.origin_owner='agent_control'
          AND object.origin_record_type='task_objective'
          AND object.origin_record_id=p_command->>'occurrence_id'
          AND object.origin_record_digest=
                p_command#>>'{objective,origin,record_digest}'
    ) THEN
        RAISE EXCEPTION USING ERRCODE='23503',
            MESSAGE='Cortex decision Trigger wake Blob missing';
    END IF;

    INSERT INTO agent_control.runtime_run(
        run_id,schema_revision,occurrence_owner,occurrence_record_type,
        occurrence_id,occurrence_schema_revision,occurrence_digest,
        origin_kind,origin_source_owner,origin_source_record_type,
        origin_source_record_id,origin_source_schema_revision,
        origin_source_record_digest,origin_initiating_principal_id,
        origin_initiating_kind,origin_initiating_audience,
        origin_owner_policy_owner,origin_owner_policy_record_type,
        origin_owner_policy_record_id,origin_owner_policy_schema_revision,
        origin_owner_policy_record_digest,origin_owner_policy_generation,
        origin_occurred_at,origin_observed_at,origin_committed_at,
        runtime_policy_owner,runtime_policy_record_type,runtime_policy_id,
        runtime_policy_schema_revision,runtime_policy_generation,
        runtime_policy_digest,budget_ledger_id,root_task_id,state,
        state_generation,created_at,updated_at,deadline_at
    ) VALUES(
        run_id_value,1,'agent_control','trigger_occurrence',
        occurrence.occurrence_id,1,occurrence.record_digest,
        occurrence.kind,occurrence.source_owner,occurrence.source_record_type,
        occurrence.source_record_id,1,occurrence.source_record_digest,
        occurrence.initiating_principal_id,occurrence.initiating_kind,
        occurrence.initiating_audience,occurrence.owner_policy_owner,
        occurrence.owner_policy_record_type,occurrence.owner_policy_record_id,
        1,occurrence.owner_policy_record_digest,
        occurrence.owner_policy_generation,occurrence.occurred_at,
        occurrence.observed_at,occurrence.committed_at,'agent_control',
        'runtime_policy',policy.policy_id,1,policy.generation,
        policy.record_digest,run_ledger_id,task_id_value,'queued',1,
        at_time,at_time,deadline_at
    );
    INSERT INTO agent_control.runtime_budget_ledger(
        ledger_id,schema_revision,scope,scope_id,parent_ledger_id,
        runtime_policy_owner,runtime_policy_record_type,runtime_policy_id,
        runtime_policy_schema_revision,runtime_policy_generation,
        runtime_policy_digest,limit_model_calls,limit_input_tokens,
        limit_output_tokens,limit_tool_calls,
        limit_external_cost_micro_usd,limit_wall_time_ms,
        limit_idle_time_ms,limit_tasks,limit_depth,limit_fanout,
        limit_parallelism,limit_invalid_output_retries,
        limit_infrastructure_retries,consumed_tasks,generation,state,
        updated_at
    ) VALUES
    (
        run_ledger_id,1,'run',run_id_value,NULL,'agent_control',
        'runtime_policy',policy.policy_id,1,policy.generation,
        policy.record_digest,policy.max_model_calls,policy.max_input_tokens,
        policy.max_output_tokens,policy.max_tool_calls,
        policy.max_external_cost_micro_usd,policy.max_wall_time_ms,
        policy.max_idle_time_ms,policy.max_tasks,policy.max_depth,
        policy.max_fanout,policy.max_parallelism,
        policy.max_invalid_output_retries,
        policy.max_infrastructure_retries,1,1,'open',at_time
    ),
    (
        task_ledger_id,1,'task',task_id_value,run_ledger_id,'agent_control',
        'runtime_policy',policy.policy_id,1,policy.generation,
        policy.record_digest,policy.max_model_calls,policy.max_input_tokens,
        policy.max_output_tokens,policy.max_tool_calls,
        policy.max_external_cost_micro_usd,policy.max_wall_time_ms,
        policy.max_idle_time_ms,policy.max_tasks,policy.max_depth,
        policy.max_fanout,policy.max_parallelism,
        policy.max_invalid_output_retries,
        policy.max_infrastructure_retries,1,1,'open',at_time
    );
    INSERT INTO agent_control.runtime_task(
        task_id,schema_revision,run_id,depth,objective,
        output_contract_owner,output_contract_record_type,
        output_contract_revision_id,output_contract_schema_revision,
        output_contract_generation,output_contract_digest,budget_ledger_id,
        state,state_generation,budget_slot_held,created_at,updated_at,deadline_at
    ) VALUES(
        task_id_value,1,run_id_value,0,p_command->'objective',
        'agent_control','output_contract_revision',
        output_contract.revision_id,1,output_contract.generation,
        output_contract.record_digest,task_ledger_id,'ready',1,false,
        at_time,at_time,deadline_at
    );
    INSERT INTO agent_control.runtime_task_input_ref(
        task_id,ordinal,reference
    ) VALUES(
        task_id_value,1,jsonb_build_object(
            'owner','agent_control',
            'record_type','trigger_occurrence',
            'record_id',occurrence.occurrence_id,
            'schema_revision',1,
            'record_digest',occurrence.record_digest
        )
    );
    event_body:=jsonb_build_object(
        'schema_revision',1,'event_id',gen_random_uuid()::TEXT,
        'subject','run','subject_id',run_id_value,'to_state','queued',
        'generation',1,
        'actor',jsonb_build_object(
            'principal_id',invoker.principal_id,
            'kind','workload','audience','control_api'
        ),
        'causation_id',occurrence.occurrence_id,
        'correlation_id',occurrence.registration_id,
        'reason_code','external_event_admitted',
        'occurred_at',agent_control.runtime_utc_text(at_time)
    );
    INSERT INTO agent_control.runtime_event(
        event_id,schema_revision,record_digest,subject,subject_id,
        to_state,generation,actor,causation_id,correlation_id,
        reason_code,occurred_at
    ) VALUES(
        event_body->>'event_id',1,agent_control.runtime_contract_digest(
            'agent-platform.contract.runtime_event.v1',event_body
        ),'run',run_id_value,'queued',1,event_body->'actor',
        occurrence.occurrence_id,occurrence.registration_id,
        'external_event_admitted',at_time
    );
    event_body:=jsonb_build_object(
        'schema_revision',1,'event_id',gen_random_uuid()::TEXT,
        'subject','task','subject_id',task_id_value,'to_state','ready',
        'generation',1,
        'actor',jsonb_build_object(
            'principal_id',invoker.principal_id,
            'kind','workload','audience','control_api'
        ),
        'causation_id',occurrence.occurrence_id,
        'correlation_id',occurrence.registration_id,
        'reason_code','trigger_root_task_ready',
        'occurred_at',agent_control.runtime_utc_text(at_time)
    );
    INSERT INTO agent_control.runtime_event(
        event_id,schema_revision,record_digest,subject,subject_id,
        to_state,generation,actor,causation_id,correlation_id,
        reason_code,occurred_at
    ) VALUES(
        event_body->>'event_id',1,agent_control.runtime_contract_digest(
            'agent-platform.contract.runtime_event.v1',event_body
        ),'task',task_id_value,'ready',1,event_body->'actor',
        occurrence.occurrence_id,occurrence.registration_id,
        'trigger_root_task_ready',at_time
    );
    response_value:=jsonb_build_object(
        'status','admitted',
        'occurrence_id',occurrence.occurrence_id,
        'run_id',run_id_value,
        'root_task_id',task_id_value,
        'run_state','queued',
        'task_state','ready'
    );
    INSERT INTO agent_control.cortex_decision_trigger_wake_admission(
        occurrence_id,occurrence_digest,body_fingerprint,raw_input,
        objective,run_id,root_task_id,response,created_at
    ) VALUES(
        occurrence.occurrence_id,occurrence.record_digest,fingerprint,
        p_command->'raw_input',p_command->'objective',run_id_value,
        task_id_value,response_value,at_time
    );
    RETURN response_value;
END
$$;

-- Worker discovery originally admitted only UserRequest origins. Select the
-- immutable wake raw input for external-event Runs while leaving every
-- existing user, graph, continuation and recovery branch unchanged.
DO $$
DECLARE
    definition TEXT;
    updated TEXT;
    raw_old CONSTANT TEXT := '    request.raw_input,';
    raw_new CONSTANT TEXT :=
        '    COALESCE(request.raw_input,wake.raw_input) AS raw_input,';
    join_old CONSTANT TEXT :=
'  JOIN agent_input.user_request AS request
    ON request.request_id=run.origin_source_record_id
   AND request.record_digest=run.origin_source_record_digest';
    join_new CONSTANT TEXT :=
'  LEFT JOIN agent_input.user_request AS request
    ON run.origin_kind=''user_request''
   AND request.request_id=run.origin_source_record_id
   AND request.record_digest=run.origin_source_record_digest
  LEFT JOIN agent_control.cortex_decision_trigger_wake_admission AS wake
    ON run.origin_kind=''external_event''
   AND wake.run_id=run.run_id';
    state_old CONSTANT TEXT :=
        '    AND run.state IN (''queued'',''running'',''waiting'')';
    state_new CONSTANT TEXT :=
'    AND run.state IN (''queued'',''running'',''waiting'')
    AND (
      (run.origin_kind=''user_request'' AND request.request_id IS NOT NULL)
      OR
      (run.origin_kind=''external_event'' AND wake.occurrence_id IS NOT NULL)
    )';
BEGIN
    SELECT pg_get_functiondef(
        'agent_control.next_cortex_task()'::regprocedure
    ) INTO STRICT definition;
    IF length(definition)-length(replace(definition,raw_old,''))
            <>length(raw_old)
       OR length(definition)-length(replace(definition,join_old,''))
            <>length(join_old)
       OR length(definition)-length(replace(definition,state_old,''))
            <>length(state_old) THEN
        RAISE EXCEPTION USING ERRCODE='55000',
            MESSAGE='Cortex Worker wake discovery source mismatch';
    END IF;
    updated:=replace(
        replace(replace(definition,raw_old,raw_new),join_old,join_new),
        state_old,state_new
    );
    EXECUTE updated;
END
$$;

REVOKE ALL ON TABLE
agent_control.cortex_decision_trigger_wake_admission
FROM PUBLIC;
REVOKE ALL ON FUNCTION
agent_control.admit_cortex_decision_trigger_wake(JSONB)
FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.next_cortex_task() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION
agent_control.admit_cortex_decision_trigger_wake(JSONB)
TO alpheus_agent_control_api;
GRANT EXECUTE ON FUNCTION agent_control.next_cortex_task()
TO alpheus_agent_worker;

RESET ROLE;
