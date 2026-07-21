SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

-- Control prepares the immutable Session inputs before a Worker may claim the
-- root Task. Worker owns neither the Session row nor Blob reference metadata.
CREATE FUNCTION agent_control.prepare_cortex_root_session(
    p_task_id TEXT, p_execution_binding JSONB, p_context_manifest JSONB,
    p_raw_input JSONB, p_worker_principal TEXT
) RETURNS JSONB
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, agent_control, platform_security, blob
SET timezone = 'UTC'
AS $$
DECLARE
    invoker RECORD;
    task_row agent_control.runtime_task%ROWTYPE;
    run_row agent_control.runtime_run%ROWTYPE;
    session_row agent_control.runtime_session%ROWTYPE;
    session_id_value TEXT := gen_random_uuid()::TEXT;
    execution_binding_id TEXT;
    context_binding_id TEXT;
    at_time TIMESTAMPTZ := clock_timestamp();
BEGIN
    SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
    IF invoker.group_role::TEXT <> 'alpheus_agent_control_api'
       OR invoker.owner_id <> 'agent_control'
       OR NOT agent_control.runtime_identifier_valid(p_task_id)
       OR NOT agent_control.runtime_identifier_valid(p_worker_principal)
       OR NOT agent_control.runtime_blob_ref_valid(p_execution_binding, 'execution_binding', '')
       OR NOT agent_control.runtime_blob_ref_valid(p_context_manifest, 'context_manifest', '')
       OR NOT agent_control.runtime_blob_ref_valid(p_raw_input, 'input_raw', '') THEN
        RAISE EXCEPTION USING ERRCODE = '42501', MESSAGE = 'cortex session preparation denied';
    END IF;
    SELECT * INTO STRICT task_row FROM agent_control.runtime_task
    WHERE task_id = p_task_id FOR UPDATE;
    SELECT * INTO STRICT run_row FROM agent_control.runtime_run
    WHERE run_id = task_row.run_id FOR SHARE;
    IF task_row.state <> 'ready' OR run_row.state <> 'queued'
       OR at_time >= task_row.deadline_at OR at_time >= run_row.deadline_at THEN
        RAISE EXCEPTION USING ERRCODE = '55000', MESSAGE = 'cortex root Task is not session-preparable';
    END IF;
    IF task_row.session_id IS NOT NULL THEN
        SELECT * INTO STRICT session_row FROM agent_control.runtime_session
        WHERE session_id = task_row.session_id;
        IF session_row.execution_binding <> p_execution_binding
           OR session_row.context_manifest <> p_context_manifest THEN
            RAISE EXCEPTION USING ERRCODE = '23505', MESSAGE = 'cortex Session identity conflict';
        END IF;
        RETURN jsonb_build_object('status','ready','session_id',session_row.session_id,
            'context_binding_id','cortex-session:'||session_row.session_id||':context',
            'raw_input_binding_id','cortex-session:'||session_row.session_id||':raw-input');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM blob.blob_object object
        WHERE object.blob_id = (p_execution_binding->>'blob_id')::UUID
          AND object.state='committed' AND object.origin_owner='agent_control'
          AND object.origin_record_type='execution_binding'
          AND object.origin_record_digest=p_execution_binding #>> '{origin,record_digest}')
       OR NOT EXISTS (SELECT 1 FROM blob.blob_object object
        WHERE object.blob_id = (p_context_manifest->>'blob_id')::UUID
          AND object.state='committed' AND object.origin_owner='agent_control'
          AND object.origin_record_type='context_manifest'
          AND object.origin_record_digest=p_context_manifest #>> '{origin,record_digest}') THEN
        RAISE EXCEPTION USING ERRCODE = '23503', MESSAGE = 'cortex Session Blob is not committed';
    END IF;
    execution_binding_id := 'cortex-session:'||session_id_value||':execution';
    context_binding_id := 'cortex-session:'||session_id_value||':context';
    PERFORM blob.bind_reference_internal('agent_control', execution_binding_id,
        (p_execution_binding->>'blob_id')::UUID,
        p_execution_binding #>> '{origin,record_type}', p_execution_binding #>> '{origin,record_id}',
        p_execution_binding #>> '{origin,record_digest}', p_worker_principal, 'private',
        run_row.deadline_at, invoker.principal_id);
    PERFORM blob.bind_reference_internal('agent_control', context_binding_id,
        (p_context_manifest->>'blob_id')::UUID,
        p_context_manifest #>> '{origin,record_type}', p_context_manifest #>> '{origin,record_id}',
        p_context_manifest #>> '{origin,record_digest}', p_worker_principal, 'private',
        run_row.deadline_at, invoker.principal_id);
    PERFORM blob.bind_reference_internal('agent_control',
        'cortex-session:'||session_id_value||':raw-input',
        (p_raw_input->>'blob_id')::UUID,
        p_raw_input #>> '{origin,record_type}', p_raw_input #>> '{origin,record_id}',
        p_raw_input #>> '{origin,record_digest}', p_worker_principal, 'private',
        run_row.deadline_at, invoker.principal_id);
    INSERT INTO agent_control.runtime_session(session_id,schema_revision,run_id,task_id,
        generation,execution_binding,context_manifest,state,created_at)
    VALUES(session_id_value,1,run_row.run_id,task_row.task_id,1,
        p_execution_binding,p_context_manifest,'open',at_time);
    UPDATE agent_control.runtime_task SET session_id=session_id_value
    WHERE task_id=task_row.task_id;
    RETURN jsonb_build_object('status','ready','session_id',session_id_value,
        'context_binding_id',context_binding_id,
        'raw_input_binding_id','cortex-session:'||session_id_value||':raw-input');
END
$$;

-- A Worker can discover only one already-prepared ready Task and the exact
-- metadata needed to authorize its context Blob read and submit fenced commands.
CREATE FUNCTION agent_control.next_cortex_task()
RETURNS JSONB
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, agent_control, platform_security
SET timezone = 'UTC'
AS $$
DECLARE
    invoker RECORD;
    selected RECORD;
BEGIN
    SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
    IF invoker.group_role::TEXT <> 'alpheus_agent_worker' OR invoker.profile_id <> 'worker' THEN
        RAISE EXCEPTION USING ERRCODE = '42501', MESSAGE = 'cortex Worker discovery denied';
    END IF;
    SELECT task.task_id, task.state_generation, task.output_contract_digest,
           task.deadline_at, session.session_id, session.context_manifest,
           context_object.origin_record_id AS raw_input_id,
           raw_object.blob_id::TEXT AS raw_blob_id,
           raw_object.content_digest::TEXT AS raw_content_digest,
           raw_object.media_type AS raw_media_type,
           raw_object.size_bytes AS raw_size_bytes,
           raw_object.origin_record_digest::TEXT AS raw_origin_digest,
           raw_object.committed_at AS raw_committed_at
    INTO selected
    FROM agent_control.runtime_task task
    JOIN agent_control.runtime_session session ON session.session_id=task.session_id
    JOIN agent_control.runtime_run run ON run.run_id=task.run_id
    JOIN blob.blob_object context_object
      ON context_object.blob_id=(session.context_manifest->>'blob_id')::UUID
    JOIN blob.blob_object raw_object
      ON raw_object.origin_owner='agent_control'
     AND raw_object.origin_record_type='input_raw'
     AND raw_object.origin_record_id=context_object.origin_record_id
    WHERE task.state='ready' AND session.state='open'
      AND run.state IN ('queued','running','waiting')
      AND task.deadline_at>clock_timestamp()+interval '90 seconds'
      AND run.deadline_at>clock_timestamp()+interval '90 seconds'
    ORDER BY task.created_at, task.task_id LIMIT 1;
    IF NOT FOUND THEN RETURN NULL; END IF;
    RETURN jsonb_build_object('task_id',selected.task_id,
        'task_state_generation',selected.state_generation,
        'output_contract_digest',selected.output_contract_digest::TEXT,
        'deadline',agent_control.runtime_utc_text(selected.deadline_at),
        'session_id',selected.session_id,'context_manifest',selected.context_manifest,
        'context_binding_id','cortex-session:'||selected.session_id||':context',
        'raw_input',jsonb_build_object('schema_revision',1,'blob_id',selected.raw_blob_id,
          'content_digest',selected.raw_content_digest,'media_type',selected.raw_media_type,
          'size_bytes',selected.raw_size_bytes,'origin',jsonb_build_object(
            'owner','agent_control','record_type','input_raw','record_id',selected.raw_input_id,
            'schema_revision',1,'record_digest',selected.raw_origin_digest),
          'committed_at',agent_control.runtime_utc_text(selected.raw_committed_at)),
        'raw_input_binding_id','cortex-session:'||selected.session_id||':raw-input');
END
$$;

-- Model output bytes are staged by the Control service. This command proves
-- the exact dispatched manifest before creating the Worker-readable reference.
CREATE FUNCTION agent_control.publish_cortex_model_output(
    p_call_id TEXT, p_manifest_digest TEXT, p_output JSONB,
    p_worker_principal TEXT
) RETURNS JSONB
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, agent_control, platform_security, blob
SET timezone = 'UTC'
AS $$
DECLARE
    invoker RECORD;
    manifest agent_control.runtime_model_call_manifest%ROWTYPE;
    binding_id_value TEXT;
    retention_until_value TIMESTAMPTZ;
BEGIN
    SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
    IF invoker.group_role::TEXT <> 'alpheus_agent_control_api'
       OR invoker.owner_id <> 'agent_control'
       OR NOT agent_control.runtime_blob_ref_valid(p_output,'model_call_manifest','')
       OR p_output #>> '{origin,record_id}' <> p_call_id
       OR p_output #>> '{origin,record_digest}' <> p_manifest_digest
       OR NOT agent_control.runtime_identifier_valid(p_worker_principal) THEN
        RAISE EXCEPTION USING ERRCODE = '42501', MESSAGE = 'cortex model output publication denied';
    END IF;
    SELECT * INTO STRICT manifest FROM agent_control.runtime_model_call_manifest
    WHERE call_id=p_call_id AND record_digest::TEXT=p_manifest_digest FOR SHARE;
    SELECT least(run.deadline_at, clock_timestamp()+interval '1 day')
    INTO retention_until_value
    FROM agent_control.runtime_attempt attempt
    JOIN agent_control.runtime_run run ON run.run_id=attempt.run_id
    WHERE attempt.attempt_id=manifest.attempt_id;
    binding_id_value := 'cortex-model-output:'||p_call_id;
    PERFORM blob.bind_reference_internal('agent_control',binding_id_value,
        (p_output->>'blob_id')::UUID,'model_call_manifest',p_call_id,p_manifest_digest,
        p_worker_principal,'private',retention_until_value,invoker.principal_id);
    RETURN jsonb_build_object('status','published','binding_id',binding_id_value);
END
$$;

REVOKE ALL ON FUNCTION agent_control.prepare_cortex_root_session(TEXT,JSONB,JSONB,JSONB,TEXT) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.next_cortex_task() FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.publish_cortex_model_output(TEXT,TEXT,JSONB,TEXT) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agent_control.prepare_cortex_root_session(TEXT,JSONB,JSONB,JSONB,TEXT) TO alpheus_agent_control_api;
GRANT EXECUTE ON FUNCTION agent_control.publish_cortex_model_output(TEXT,TEXT,JSONB,TEXT) TO alpheus_agent_control_api;
GRANT EXECUTE ON FUNCTION agent_control.next_cortex_task() TO alpheus_agent_worker;

RESET ROLE;
