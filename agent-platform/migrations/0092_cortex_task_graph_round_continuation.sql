-- A Decision Desk refinement does not finish the Run. Control atomically
-- closes the current graph and reactivates its parked root Task as a bounded
-- round planner. The immutable decision result remains the only motivation
-- for that continuation.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

CREATE TABLE agent_control.cortex_task_graph_round_continuation (
    source_call_id TEXT PRIMARY KEY,
    schema_revision SMALLINT NOT NULL CHECK (schema_revision=1),
    record_digest CHAR(64) NOT NULL UNIQUE CHECK (
        agent_control.runtime_digest_valid(record_digest::TEXT)
    ),
    run_id TEXT NOT NULL REFERENCES agent_control.runtime_run(run_id),
    graph_id TEXT NOT NULL UNIQUE REFERENCES agent_control.cortex_task_graph(graph_id),
    completed_round BIGINT NOT NULL CHECK (completed_round BETWEEN 1 AND 7),
    max_rounds BIGINT NOT NULL CHECK (
        max_rounds BETWEEN completed_round+1 AND 8
    ),
    parent_task_id TEXT NOT NULL REFERENCES agent_control.runtime_task(task_id),
    decision_task_id TEXT NOT NULL UNIQUE REFERENCES agent_control.runtime_task(task_id),
    source_result_id TEXT NOT NULL UNIQUE REFERENCES agent_control.runtime_model_call_result(result_id),
    decision_output JSONB NOT NULL CHECK (
        agent_control.runtime_blob_ref_valid(
            decision_output,'model_call_manifest','')
    ),
    parent_session_id TEXT NOT NULL UNIQUE REFERENCES agent_control.runtime_session(session_id),
    execution_binding JSONB NOT NULL CHECK (
        agent_control.runtime_blob_ref_valid(
            execution_binding,'execution_binding','')
    ),
    context_manifest JSONB NOT NULL CHECK (
        agent_control.runtime_blob_ref_valid(
            context_manifest,'context_manifest','')
    ),
    state TEXT NOT NULL CHECK (state='ready'),
    response JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE(run_id,parent_task_id,completed_round)
);

CREATE TRIGGER cortex_task_graph_round_continuation_immutable
BEFORE UPDATE OR DELETE ON agent_control.cortex_task_graph_round_continuation
FOR EACH ROW EXECUTE FUNCTION
agent_control.reject_runtime_immutable_mutation();

CREATE FUNCTION agent_control.get_cortex_task_graph_round_seed(
    p_source_call_id TEXT,
    p_attempt_id TEXT,
    p_lease_generation BIGINT,
    p_lease_token UUID
) RETURNS JSONB
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path=pg_catalog,agent_control,agent_input,platform_security
SET timezone='UTC'
AS $$
DECLARE
    invoker RECORD;
    selected RECORD;
    at_time TIMESTAMPTZ:=clock_timestamp();
BEGIN
    SELECT * INTO STRICT invoker
    FROM platform_security.invoker_identity();
    IF invoker.group_role::TEXT<>'alpheus_agent_control_api'
       OR invoker.profile_id<>'control-api'
       OR invoker.owner_id<>'agent_control'
       OR NOT agent_control.runtime_identifier_valid(p_source_call_id)
       OR NOT agent_control.runtime_identifier_valid(p_attempt_id)
       OR p_lease_generation<1 OR p_lease_token IS NULL THEN
        RAISE EXCEPTION USING ERRCODE='42501',
            MESSAGE='TaskGraph round seed denied';
    END IF;

    SELECT
        run.run_id,run.deadline_at,graph.graph_id,graph.round,
        graph.max_rounds,graph.parent_task_id,
        desk.task_id AS decision_task_id,
        result.result_id,result.record_digest::TEXT AS result_digest,
        result.output AS decision_output,
        request.request_id,request.conversation_id,
        request.subject_principal_id,request.raw_input
    INTO STRICT selected
    FROM agent_control.runtime_model_call_manifest AS manifest
    JOIN agent_control.runtime_model_call_result AS result
      ON result.call_id=manifest.call_id
     AND result.attempt_id=manifest.attempt_id
     AND result.turn_id=manifest.turn_id
    JOIN agent_control.runtime_cortex_output_validation AS validation
      ON validation.call_id=manifest.call_id
     AND validation.manifest_digest=manifest.record_digest
     AND validation.output_blob_id=(result.output->>'blob_id')::UUID
     AND validation.output_content_digest=result.output->>'content_digest'
    JOIN agent_control.runtime_turn AS turn
      ON turn.turn_id=result.turn_id
     AND turn.attempt_id=result.attempt_id
     AND turn.state='result_committed'
    JOIN agent_control.runtime_attempt AS attempt
      ON attempt.attempt_id=result.attempt_id
     AND attempt.attempt_id=p_attempt_id
     AND attempt.state='executing'
     AND attempt.lease_generation=p_lease_generation
     AND attempt.lease_token=p_lease_token
     AND attempt.lease_expires_at>at_time
     AND attempt.lease_worker->>'principal_id'='cortex-worker-1'
    JOIN agent_control.runtime_task AS desk
      ON desk.task_id=attempt.task_id
     AND desk.run_id=attempt.run_id
     AND desk.state='running'
    JOIN agent_control.cortex_task_graph_node AS node
      ON node.task_id=desk.task_id
     AND node.role_id='decision_desk'
    JOIN agent_control.cortex_task_graph AS graph
      ON graph.graph_id=node.graph_id
     AND graph.round<graph.max_rounds
    JOIN agent_control.runtime_task AS parent
      ON parent.task_id=graph.parent_task_id
     AND parent.run_id=graph.run_id
     AND parent.state='waiting'
    JOIN agent_control.runtime_run AS run
      ON run.run_id=graph.run_id
     AND run.state='running'
     AND run.deadline_at>at_time
    JOIN agent_input.user_request AS request
      ON request.request_id=run.origin_source_record_id
     AND request.record_digest=run.origin_source_record_digest
    JOIN agent_control.output_contract_revision AS contract
      ON contract.revision_id='cortex-task-graph-round-output-v1'
     AND contract.generation=1
     AND contract.record_digest=manifest.output_contract_digest
    WHERE manifest.call_id=p_source_call_id;

    RETURN jsonb_build_object(
        'status','ready',
        'source_call_id',p_source_call_id,
        'run_id',selected.run_id,
        'graph_id',selected.graph_id,
        'completed_round',selected.round,
        'max_rounds',selected.max_rounds,
        'parent_task_id',selected.parent_task_id,
        'decision_task_id',selected.decision_task_id,
        'source_result',jsonb_build_object(
            'owner','agent_control',
            'record_type','model_call_result',
            'record_id',selected.result_id,
            'schema_revision',1,
            'record_digest',selected.result_digest
        ),
        'decision_output',selected.decision_output,
        'decision_output_binding_id',
          'cortex-model-output:'||p_source_call_id,
        'request_id',selected.request_id,
        'conversation_id',selected.conversation_id,
        'subject_principal_id',selected.subject_principal_id,
        'raw_input',selected.raw_input,
        'deadline_at',agent_control.runtime_utc_text(selected.deadline_at)
    );
END
$$;

CREATE FUNCTION agent_control.prepare_cortex_task_graph_next_round(
    p_source_call_id TEXT,
    p_attempt_id TEXT,
    p_lease_generation BIGINT,
    p_lease_token UUID,
    p_execution_binding JSONB,
    p_context_manifest JSONB,
    p_worker_principal TEXT
) RETURNS JSONB
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path=pg_catalog,agent_control,agent_input,platform_security,blob
SET timezone='UTC'
AS $$
DECLARE
    invoker RECORD;
    existing agent_control.cortex_task_graph_round_continuation%ROWTYPE;
    manifest_row agent_control.runtime_model_call_manifest%ROWTYPE;
    result_row agent_control.runtime_model_call_result%ROWTYPE;
    turn_row agent_control.runtime_turn%ROWTYPE;
    attempt_row agent_control.runtime_attempt%ROWTYPE;
    desk_row agent_control.runtime_task%ROWTYPE;
    desk_session agent_control.runtime_session%ROWTYPE;
    graph_row agent_control.cortex_task_graph%ROWTYPE;
    schedule_row agent_control.cortex_task_graph_schedule%ROWTYPE;
    parent_row agent_control.runtime_task%ROWTYPE;
    parent_session agent_control.runtime_session%ROWTYPE;
    run_row agent_control.runtime_run%ROWTYPE;
    raw_input JSONB;
    session_id_value TEXT;
    response_value JSONB;
    digest_value CHAR(64);
    at_time TIMESTAMPTZ:=clock_timestamp();
BEGIN
    SELECT * INTO STRICT invoker
    FROM platform_security.invoker_identity();
    IF invoker.group_role::TEXT<>'alpheus_agent_control_api'
       OR invoker.profile_id<>'control-api'
       OR invoker.owner_id<>'agent_control'
       OR NOT agent_control.runtime_identifier_valid(p_source_call_id)
       OR NOT agent_control.runtime_identifier_valid(p_attempt_id)
       OR p_lease_generation<1 OR p_lease_token IS NULL
       OR p_worker_principal<>'cortex-worker-1'
       OR NOT agent_control.runtime_blob_ref_valid(
         p_execution_binding,'execution_binding','')
       OR NOT agent_control.runtime_blob_ref_valid(
         p_context_manifest,'context_manifest','') THEN
        RAISE EXCEPTION USING ERRCODE='42501',
            MESSAGE='TaskGraph next round preparation denied';
    END IF;

    SELECT * INTO existing
    FROM agent_control.cortex_task_graph_round_continuation
    WHERE source_call_id=p_source_call_id;
    IF FOUND THEN
        IF existing.execution_binding<>p_execution_binding
           OR existing.context_manifest<>p_context_manifest THEN
            RAISE EXCEPTION USING ERRCODE='23505',
                MESSAGE='TaskGraph round continuation identity conflict';
        END IF;
        RETURN existing.response;
    END IF;

    SELECT * INTO STRICT manifest_row
    FROM agent_control.runtime_model_call_manifest
    WHERE call_id=p_source_call_id;
    SELECT * INTO STRICT result_row
    FROM agent_control.runtime_model_call_result
    WHERE call_id=p_source_call_id
      AND attempt_id=manifest_row.attempt_id
      AND turn_id=manifest_row.turn_id;
    SELECT * INTO STRICT turn_row
    FROM agent_control.runtime_turn
    WHERE turn_id=result_row.turn_id
      AND state='result_committed';
    SELECT * INTO STRICT attempt_row
    FROM agent_control.runtime_attempt
    WHERE attempt_id=result_row.attempt_id
    FOR UPDATE;
    SELECT * INTO STRICT desk_row
    FROM agent_control.runtime_task
    WHERE task_id=attempt_row.task_id
    FOR UPDATE;
    SELECT * INTO STRICT desk_session
    FROM agent_control.runtime_session
    WHERE session_id=attempt_row.session_id
    FOR UPDATE;
    SELECT graph.* INTO STRICT graph_row
    FROM agent_control.cortex_task_graph_node AS node
    JOIN agent_control.cortex_task_graph AS graph
      ON graph.graph_id=node.graph_id
    WHERE node.task_id=desk_row.task_id
      AND node.role_id='decision_desk'
    FOR SHARE OF graph;
    SELECT * INTO STRICT schedule_row
    FROM agent_control.cortex_task_graph_schedule
    WHERE graph_id=graph_row.graph_id
    FOR UPDATE;
    SELECT * INTO STRICT parent_row
    FROM agent_control.runtime_task
    WHERE task_id=graph_row.parent_task_id
    FOR UPDATE;
    SELECT * INTO STRICT run_row
    FROM agent_control.runtime_run
    WHERE run_id=graph_row.run_id
    FOR UPDATE;
    SELECT * INTO STRICT parent_session
    FROM agent_control.runtime_session
    WHERE task_id=parent_row.task_id
    ORDER BY generation DESC
    LIMIT 1
    FOR UPDATE;

    IF attempt_row.attempt_id<>p_attempt_id
       OR attempt_row.state<>'executing'
       OR attempt_row.lease_generation<>p_lease_generation
       OR attempt_row.lease_token<>p_lease_token
       OR attempt_row.lease_expires_at<=at_time
       OR attempt_row.lease_worker->>'principal_id'<>p_worker_principal
       OR desk_row.state<>'running'
       OR desk_session.state<>'open'
       OR graph_row.round>=graph_row.max_rounds
       OR schedule_row.state<>'open'
       OR schedule_row.active_tasks<>1
       OR parent_row.state<>'waiting'
       OR parent_row.budget_slot_held
       OR parent_session.state<>'closed'
       OR run_row.state<>'running'
       OR run_row.deadline_at<=at_time
       OR NOT EXISTS (
         SELECT 1
         FROM agent_control.runtime_cortex_output_validation AS validation
         JOIN agent_control.output_contract_revision AS contract
           ON contract.revision_id='cortex-task-graph-round-output-v1'
          AND contract.generation=1
          AND contract.record_digest=manifest_row.output_contract_digest
         WHERE validation.call_id=manifest_row.call_id
           AND validation.manifest_digest=manifest_row.record_digest
           AND validation.output_blob_id=
             (result_row.output->>'blob_id')::UUID
           AND validation.output_content_digest=
             result_row.output->>'content_digest'
       )
       OR p_execution_binding#>>'{origin,record_id}'<>
          'cortex-round-planner-'||p_source_call_id
       OR p_context_manifest#>>'{origin,record_id}'<>
          'cortex-round-context-'||p_source_call_id THEN
        RAISE EXCEPTION USING ERRCODE='55000',
            MESSAGE='TaskGraph next round state denied';
    END IF;

    SELECT request.raw_input INTO STRICT raw_input
    FROM agent_input.user_request AS request
    WHERE request.request_id=run_row.origin_source_record_id
      AND request.record_digest=run_row.origin_source_record_digest;
    session_id_value:='cortex-round-session-'||p_source_call_id;

    UPDATE agent_control.runtime_attempt
    SET state='superseded',
        state_generation=attempt_row.state_generation+1,
        updated_at=greatest(updated_at,at_time),
        terminal_at=at_time
    WHERE attempt_id=attempt_row.attempt_id;
    PERFORM agent_control.runtime_insert_attempt_release_event(
        attempt_row.attempt_id,attempt_row.lease_generation,
        p_worker_principal,attempt_row.lease_token,
        attempt_row.lease_expires_at,p_source_call_id,
        run_row.run_id,at_time
    );
    PERFORM agent_control.runtime_insert_event(
        'attempt',attempt_row.attempt_id,'executing','superseded',
        attempt_row.state_generation+1,p_worker_principal,
        p_source_call_id,run_row.run_id,
        'task_graph_next_round_requested',at_time
    );
    UPDATE agent_control.runtime_session
    SET state='closed',generation=desk_session.generation+1,
        closed_at=at_time
    WHERE session_id=desk_session.session_id;
    PERFORM agent_control.runtime_insert_event(
        'session',desk_session.session_id,'open','closed',
        desk_session.generation+1,p_worker_principal,
        p_source_call_id,run_row.run_id,
        'task_graph_next_round_requested',at_time
    );
    UPDATE agent_control.runtime_task
    SET state='superseded',
        state_generation=desk_row.state_generation+1,
        budget_slot_held=false,
        updated_at=greatest(updated_at,at_time),
        terminal_at=at_time
    WHERE task_id=desk_row.task_id;
    IF NOT agent_control.runtime_release_active_slot_ancestors(
      run_row.run_id,desk_row.budget_ledger_id,at_time) THEN
        RAISE EXCEPTION USING ERRCODE='40001',
            MESSAGE='TaskGraph Desk active slot changed';
    END IF;
    PERFORM agent_control.runtime_insert_event(
        'task',desk_row.task_id,'running','superseded',
        desk_row.state_generation+1,p_worker_principal,
        p_source_call_id,run_row.run_id,
        'task_graph_next_round_requested',at_time
    );
    UPDATE agent_control.cortex_task_graph_schedule
    SET state='closed',generation=generation+1,
        updated_at=greatest(updated_at,at_time),closed_at=at_time
    WHERE graph_id=graph_row.graph_id AND active_tasks=0;

    PERFORM blob.bind_reference_internal(
      'agent_control','cortex-session:'||session_id_value||':execution',
      (p_execution_binding->>'blob_id')::UUID,
      p_execution_binding#>>'{origin,record_type}',
      p_execution_binding#>>'{origin,record_id}',
      p_execution_binding#>>'{origin,record_digest}',
      invoker.principal_id,'explicit',run_row.deadline_at,
      invoker.principal_id);
    PERFORM blob.change_acl_internal(
      'agent_control','cortex-session:'||session_id_value||':execution',
      invoker.principal_id,p_worker_principal,0,'grant',
      'cortex_worker_round_session',invoker.principal_id);
    PERFORM blob.bind_reference_internal(
      'agent_control','cortex-session:'||session_id_value||':context',
      (p_context_manifest->>'blob_id')::UUID,
      p_context_manifest#>>'{origin,record_type}',
      p_context_manifest#>>'{origin,record_id}',
      p_context_manifest#>>'{origin,record_digest}',
      invoker.principal_id,'explicit',run_row.deadline_at,
      invoker.principal_id);
    PERFORM blob.change_acl_internal(
      'agent_control','cortex-session:'||session_id_value||':context',
      invoker.principal_id,p_worker_principal,0,'grant',
      'cortex_worker_round_session',invoker.principal_id);
    PERFORM blob.bind_reference_internal(
      'agent_control','cortex-session:'||session_id_value||':raw-input',
      (raw_input->>'blob_id')::UUID,
      raw_input#>>'{origin,record_type}',raw_input#>>'{origin,record_id}',
      raw_input#>>'{origin,record_digest}',invoker.principal_id,
      'explicit',run_row.deadline_at,invoker.principal_id);
    PERFORM blob.change_acl_internal(
      'agent_control','cortex-session:'||session_id_value||':raw-input',
      invoker.principal_id,p_worker_principal,0,'grant',
      'cortex_worker_round_session',invoker.principal_id);
    PERFORM blob.bind_reference_internal(
      'agent_control','cortex-session:'||session_id_value||':round-decision',
      (result_row.output->>'blob_id')::UUID,
      'agent_control','model_call_result',result_row.result_id,
      result_row.record_digest::TEXT,invoker.principal_id,
      'explicit',run_row.deadline_at,invoker.principal_id);
    PERFORM blob.change_acl_internal(
      'agent_control',
      'cortex-session:'||session_id_value||':round-decision',
      invoker.principal_id,p_worker_principal,0,'grant',
      'cortex_worker_round_session',invoker.principal_id);

    INSERT INTO agent_control.runtime_session(
      session_id,schema_revision,run_id,task_id,generation,
      execution_binding,context_manifest,state,created_at
    ) VALUES(
      session_id_value,1,run_row.run_id,parent_row.task_id,
      parent_session.generation+1,p_execution_binding,
      p_context_manifest,'open',at_time
    );
    UPDATE agent_control.runtime_task
    SET session_id=session_id_value,state='ready',
        state_generation=parent_row.state_generation+1,
        updated_at=greatest(updated_at,at_time)
    WHERE task_id=parent_row.task_id;
    PERFORM agent_control.runtime_insert_event(
      'task',parent_row.task_id,'waiting','ready',
      parent_row.state_generation+1,p_worker_principal,
      p_source_call_id,run_row.run_id,
      'task_graph_round_planner_ready',at_time
    );

    response_value:=jsonb_build_object(
      'status','ready','source_call_id',p_source_call_id,
      'run_id',run_row.run_id,'graph_id',graph_row.graph_id,
      'completed_round',graph_row.round,
      'next_round',graph_row.round+1,
      'max_rounds',graph_row.max_rounds,
      'parent_task_id',parent_row.task_id,
      'parent_session_id',session_id_value
    );
    digest_value:=agent_control.runtime_contract_digest(
      'agent_control.cortex_task_graph_round_continuation.v1',
      jsonb_build_object(
        'source_call_id',p_source_call_id,'run_id',run_row.run_id,
        'graph_id',graph_row.graph_id,'completed_round',graph_row.round,
        'max_rounds',graph_row.max_rounds,
        'parent_task_id',parent_row.task_id,
        'decision_task_id',desk_row.task_id,
        'source_result_id',result_row.result_id,
        'decision_output',result_row.output,
        'parent_session_id',session_id_value,
        'execution_binding',p_execution_binding,
        'context_manifest',p_context_manifest,
        'state','ready','response',response_value
      )
    );
    INSERT INTO agent_control.cortex_task_graph_round_continuation(
      source_call_id,schema_revision,record_digest,run_id,graph_id,
      completed_round,max_rounds,parent_task_id,decision_task_id,
      source_result_id,decision_output,parent_session_id,
      execution_binding,context_manifest,state,response,created_at
    ) VALUES(
      p_source_call_id,1,digest_value,run_row.run_id,graph_row.graph_id,
      graph_row.round,graph_row.max_rounds,parent_row.task_id,
      desk_row.task_id,result_row.result_id,result_row.output,
      session_id_value,p_execution_binding,p_context_manifest,
      'ready',response_value,at_time
    );
    RETURN response_value;
END
$$;

-- Add round identity and the reactivated root's decision input to Worker
-- discovery without granting that data to ordinary Intent Tasks.
DO $migration$
DECLARE
    definition TEXT;
    original_definition TEXT;
BEGIN
    definition:=pg_get_functiondef(
      'agent_control.next_cortex_task()'::REGPROCEDURE);
    original_definition:=definition;
    definition:=replace(
      definition,
      $old$      WHEN graph_node.task_id IS NOT NULL THEN graph_node.role_id
      WHEN scout_admission.request_id IS NOT NULL THEN 'scout'$old$,
      $new$      WHEN graph_node.task_id IS NOT NULL THEN graph_node.role_id
      WHEN round_continuation.source_call_id IS NOT NULL
        THEN 'round_planner'
      WHEN scout_admission.request_id IS NOT NULL THEN 'scout'$new$
    );
    definition:=replace(
      definition,
      $old$    graph.graph_id AS task_graph_id,
    graph_node.role_revision AS task_graph_role_revision,$old$,
      $new$    graph.graph_id AS task_graph_id,
    COALESCE(graph.round,round_continuation.completed_round+1)
      AS task_graph_round,
    COALESCE(graph.max_rounds,round_continuation.max_rounds)
      AS task_graph_max_rounds,
    graph_node.role_revision AS task_graph_role_revision,$new$
    );
    definition:=replace(
      definition,
      $old$  LEFT JOIN agent_control.cortex_scout_child_admission AS scout_admission$old$,
      $new$  LEFT JOIN agent_control.cortex_task_graph_round_continuation
    AS round_continuation
    ON round_continuation.parent_task_id=task.task_id
   AND round_continuation.parent_session_id=session.session_id
   AND round_continuation.state='ready'
  LEFT JOIN agent_control.cortex_scout_child_admission AS scout_admission$new$
    );
    definition:=replace(
      definition,
      $old$      AND continuation.admission_request_id IS NULL
      AND task.output_contract_revision_id='cortex-workflow-output-v8'$old$,
      $new$      AND continuation.admission_request_id IS NULL
      AND task.output_contract_revision_id='cortex-workflow-output-v8'$new$
    );
    definition:=replace(
      definition,
      $old$    'task_graph_id',selected.task_graph_id,
    'task_graph_role_revision',selected.task_graph_role_revision,$old$,
      $new$    'task_graph_id',selected.task_graph_id,
    'task_graph_round',selected.task_graph_round,
    'task_graph_max_rounds',selected.task_graph_max_rounds,
    'task_graph_role_revision',selected.task_graph_role_revision,
    'task_graph_round_decision',
      CASE WHEN selected.role='round_planner'
        THEN round_continuation.decision_output ELSE NULL END,
    'task_graph_round_decision_binding_id',
      CASE WHEN selected.role='round_planner'
        THEN 'cortex-session:'||selected.session_id||':round-decision'
        ELSE NULL END,$new$
    );
    -- The return expression above references the joined continuation, so make
    -- it part of the selected record rather than an outer query reference.
    definition:=replace(
      definition,
      $old$    join_resolution.join_id AS task_graph_join_id,$old$,
      $new$    round_continuation.decision_output
      AS task_graph_round_decision,
    join_resolution.join_id AS task_graph_join_id,$new$
    );
    definition:=replace(
      definition,
      $old$      CASE WHEN selected.role='round_planner'
        THEN round_continuation.decision_output ELSE NULL END,$old$,
      $new$      CASE WHEN selected.role='round_planner'
        THEN selected.task_graph_round_decision ELSE NULL END,$new$
    );
    IF definition=original_definition
       OR position('round_planner' IN definition)=0
       OR position('task_graph_round_decision_binding_id' IN definition)=0
       OR position('task_graph_max_rounds' IN definition)=0 THEN
        RAISE EXCEPTION 'Unexpected Cortex Worker discovery definition';
    END IF;
    EXECUTE definition;
END
$migration$;

REVOKE ALL ON TABLE
agent_control.cortex_task_graph_round_continuation FROM PUBLIC;
REVOKE ALL ON FUNCTION
agent_control.get_cortex_task_graph_round_seed(TEXT,TEXT,BIGINT,UUID)
FROM PUBLIC;
REVOKE ALL ON FUNCTION
agent_control.prepare_cortex_task_graph_next_round(
  TEXT,TEXT,BIGINT,UUID,JSONB,JSONB,TEXT)
FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.next_cortex_task() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION
agent_control.get_cortex_task_graph_round_seed(TEXT,TEXT,BIGINT,UUID)
TO alpheus_agent_control_api;
GRANT EXECUTE ON FUNCTION
agent_control.prepare_cortex_task_graph_next_round(
  TEXT,TEXT,BIGINT,UUID,JSONB,JSONB,TEXT)
TO alpheus_agent_control_api;
GRANT EXECUTE ON FUNCTION agent_control.next_cortex_task()
TO alpheus_agent_worker;

RESET ROLE;
