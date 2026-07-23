-- Prepare every admitted TaskGraph node with immutable execution, context,
-- request and objective inputs. Preparation alone does not unblock a node;
-- dependency/Join transitions remain a separate Control responsibility.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

CREATE FUNCTION agent_control.prepare_cortex_task_graph_node_session(
    p_task_id TEXT,
    p_execution_binding JSONB,
    p_context_manifest JSONB,
    p_raw_input JSONB,
    p_objective JSONB,
    p_worker_principal TEXT
) RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,agent_input,platform_security,blob
SET timezone='UTC' AS $$
DECLARE
    invoker RECORD;
    task_row agent_control.runtime_task%ROWTYPE;
    node_row agent_control.cortex_task_graph_node%ROWTYPE;
    graph_row agent_control.cortex_task_graph%ROWTYPE;
    run_row agent_control.runtime_run%ROWTYPE;
    request_row agent_input.user_request%ROWTYPE;
    parent_row agent_control.runtime_task%ROWTYPE;
    session_row agent_control.runtime_session%ROWTYPE;
    session_id_value TEXT:=gen_random_uuid()::TEXT;
    execution_binding_id TEXT;
    context_binding_id TEXT;
    raw_binding_id TEXT;
    objective_binding_id TEXT;
    retention_until TIMESTAMPTZ;
    at_time TIMESTAMPTZ:=clock_timestamp();
BEGIN
    SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
    IF invoker.group_role::TEXT<>'alpheus_agent_control_api'
       OR invoker.profile_id<>'control-api'
       OR invoker.owner_id<>'agent_control'
       OR NOT agent_control.runtime_identifier_valid(p_task_id)
       OR NOT agent_control.runtime_identifier_valid(p_worker_principal)
       OR NOT agent_control.runtime_blob_ref_valid(
           p_execution_binding,'execution_binding','')
       OR p_execution_binding#>>'{origin,record_id}'<>p_task_id
       OR NOT agent_control.runtime_blob_ref_valid(
           p_context_manifest,'context_manifest','')
       OR p_context_manifest#>>'{origin,record_id}'<>p_task_id
       OR NOT agent_control.runtime_blob_ref_valid(p_raw_input,'input_raw','')
       OR NOT agent_control.runtime_blob_ref_valid(p_objective,'task_objective','') THEN
        RAISE EXCEPTION USING ERRCODE='42501',
            MESSAGE='TaskGraph node Session preparation denied';
    END IF;

    SELECT task.* INTO STRICT task_row
    FROM agent_control.runtime_task AS task
    WHERE task.task_id=p_task_id
    FOR UPDATE;
    SELECT node.* INTO STRICT node_row
    FROM agent_control.cortex_task_graph_node AS node
    WHERE node.task_id=task_row.task_id
    FOR SHARE;
    SELECT graph.* INTO STRICT graph_row
    FROM agent_control.cortex_task_graph AS graph
    WHERE graph.graph_id=node_row.graph_id
    FOR SHARE;
    SELECT run.* INTO STRICT run_row
    FROM agent_control.runtime_run AS run
    WHERE run.run_id=graph_row.run_id AND run.run_id=task_row.run_id
    FOR SHARE;
    SELECT parent.* INTO STRICT parent_row
    FROM agent_control.runtime_task AS parent
    WHERE parent.task_id=graph_row.parent_task_id
      AND parent.run_id=run_row.run_id
    FOR SHARE;
    SELECT request.* INTO STRICT request_row
    FROM agent_input.user_request AS request
    WHERE request.request_id=run_row.origin_source_record_id
      AND request.record_digest=run_row.origin_source_record_digest
    FOR SHARE;

    IF task_row.state NOT IN ('ready','blocked')
       OR parent_row.state<>'waiting'
       OR run_row.state NOT IN ('running','waiting')
       OR at_time>=task_row.deadline_at
       OR at_time>=run_row.deadline_at
       OR p_objective<>node_row.objective
       OR p_raw_input<>request_row.raw_input THEN
        RAISE EXCEPTION USING ERRCODE='55000',
            MESSAGE='TaskGraph node is not session-preparable';
    END IF;

    IF task_row.session_id IS NOT NULL THEN
        SELECT * INTO STRICT session_row
        FROM agent_control.runtime_session
        WHERE session_id=task_row.session_id;
        IF session_row.state<>'open'
           OR session_row.execution_binding<>p_execution_binding
           OR session_row.context_manifest<>p_context_manifest THEN
            RAISE EXCEPTION USING ERRCODE='23505',
                MESSAGE='TaskGraph node Session identity conflict';
        END IF;
        RETURN jsonb_build_object(
            'status','ready',
            'graph_id',graph_row.graph_id,
            'task_id',task_row.task_id,
            'task_state',task_row.state,
            'session_id',session_row.session_id,
            'replayed',true,
            'context_binding_id',
                'cortex-session:'||session_row.session_id||':context',
            'raw_input_binding_id',
                'cortex-session:'||session_row.session_id||':raw-input',
            'objective_binding_id',
                'cortex-session:'||session_row.session_id||':objective'
        );
    END IF;

    retention_until:=least(run_row.deadline_at,task_row.deadline_at);
    execution_binding_id:=
        'cortex-session:'||session_id_value||':execution';
    context_binding_id:=
        'cortex-session:'||session_id_value||':context';
    raw_binding_id:=
        'cortex-session:'||session_id_value||':raw-input';
    objective_binding_id:=
        'cortex-session:'||session_id_value||':objective';

    PERFORM blob.bind_reference_internal(
        'agent_control',execution_binding_id,
        (p_execution_binding->>'blob_id')::UUID,
        p_execution_binding#>>'{origin,record_type}',
        p_execution_binding#>>'{origin,record_id}',
        p_execution_binding#>>'{origin,record_digest}',
        invoker.principal_id,'explicit',retention_until,invoker.principal_id);
    PERFORM blob.change_acl_internal(
        'agent_control',execution_binding_id,invoker.principal_id,
        p_worker_principal,0,'grant','cortex_worker_task_graph_session',
        invoker.principal_id);

    PERFORM blob.bind_reference_internal(
        'agent_control',context_binding_id,
        (p_context_manifest->>'blob_id')::UUID,
        p_context_manifest#>>'{origin,record_type}',
        p_context_manifest#>>'{origin,record_id}',
        p_context_manifest#>>'{origin,record_digest}',
        invoker.principal_id,'explicit',retention_until,invoker.principal_id);
    PERFORM blob.change_acl_internal(
        'agent_control',context_binding_id,invoker.principal_id,
        p_worker_principal,0,'grant','cortex_worker_task_graph_session',
        invoker.principal_id);

    PERFORM blob.bind_reference_internal(
        'agent_control',raw_binding_id,(p_raw_input->>'blob_id')::UUID,
        p_raw_input#>>'{origin,record_type}',
        p_raw_input#>>'{origin,record_id}',
        p_raw_input#>>'{origin,record_digest}',
        invoker.principal_id,'explicit',retention_until,invoker.principal_id);
    PERFORM blob.change_acl_internal(
        'agent_control',raw_binding_id,invoker.principal_id,
        p_worker_principal,0,'grant','cortex_worker_task_graph_session',
        invoker.principal_id);

    PERFORM blob.bind_reference_internal(
        'agent_control',objective_binding_id,(p_objective->>'blob_id')::UUID,
        p_objective#>>'{origin,record_type}',
        p_objective#>>'{origin,record_id}',
        p_objective#>>'{origin,record_digest}',
        invoker.principal_id,'explicit',retention_until,invoker.principal_id);
    PERFORM blob.change_acl_internal(
        'agent_control',objective_binding_id,invoker.principal_id,
        p_worker_principal,0,'grant','cortex_worker_task_graph_session',
        invoker.principal_id);

    INSERT INTO agent_control.runtime_session(
        session_id,schema_revision,run_id,task_id,generation,
        execution_binding,context_manifest,state,created_at
    ) VALUES(
        session_id_value,1,run_row.run_id,task_row.task_id,1,
        p_execution_binding,p_context_manifest,'open',at_time
    );
    UPDATE agent_control.runtime_task
    SET session_id=session_id_value
    WHERE task_id=task_row.task_id;

    RETURN jsonb_build_object(
        'status','ready',
        'graph_id',graph_row.graph_id,
        'task_id',task_row.task_id,
        'task_state',task_row.state,
        'session_id',session_id_value,
        'replayed',false,
        'context_binding_id',context_binding_id,
        'raw_input_binding_id',raw_binding_id,
        'objective_binding_id',objective_binding_id
    );
END
$$;

REVOKE ALL ON FUNCTION agent_control.prepare_cortex_task_graph_node_session(
    TEXT,JSONB,JSONB,JSONB,JSONB,TEXT
) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agent_control.prepare_cortex_task_graph_node_session(
    TEXT,JSONB,JSONB,JSONB,JSONB,TEXT
) TO alpheus_agent_control_api;

RESET ROLE;
