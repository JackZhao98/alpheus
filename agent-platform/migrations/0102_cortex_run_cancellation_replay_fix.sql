-- Repair cancellation replay lookup after 0101: the local variable named
-- "found" shadowed PL/pgSQL's FOUND status. Also bind an existing immutable
-- request to its original authenticated subject before reconciling it.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

CREATE OR REPLACE FUNCTION agent_control.get_cortex_run_cancellation_result(
    p_request_id TEXT,
    p_subject_principal_id TEXT
) RETURNS JSONB
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security
SET timezone='UTC'
AS $$
DECLARE
    invoker RECORD;
    response_value JSONB;
BEGIN
    SELECT * INTO STRICT invoker
    FROM platform_security.invoker_identity();
    IF invoker.group_role::TEXT<>'alpheus_agent_control_api'
       OR invoker.profile_id<>'control-api'
       OR invoker.owner_id<>'agent_control'
       OR NOT agent_control.runtime_identifier_valid(p_request_id)
       OR NOT agent_control.runtime_identifier_valid(
            p_subject_principal_id
       ) THEN
        RAISE EXCEPTION USING ERRCODE='42501',
            MESSAGE='Cortex Run cancellation result read denied';
    END IF;
    SELECT cancellation.response INTO response_value
    FROM agent_control.cortex_run_cancellation AS cancellation
    WHERE cancellation.request_id=p_request_id
      AND cancellation.subject_principal_id=p_subject_principal_id;
    IF NOT FOUND THEN RETURN NULL; END IF;
    RETURN response_value;
END
$$;

CREATE OR REPLACE FUNCTION agent_control.cancel_cortex_run(
    p_subject_principal_id TEXT,
    p_command TEXT
) RETURNS JSONB
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security
SET timezone='UTC'
AS $$
DECLARE
    invoker RECORD;
    parsed JSONB:=agent_control.runtime_parse_worker_command(p_command);
    run_row agent_control.runtime_run%ROWTYPE;
    existing_subject TEXT;
    submit_response JSONB;
    initial_response JSONB;
    request_id_value TEXT;
BEGIN
    SELECT * INTO STRICT invoker
    FROM platform_security.invoker_identity();
    IF invoker.group_role::TEXT<>'alpheus_agent_control_api'
       OR invoker.profile_id<>'control-api'
       OR invoker.owner_id<>'agent_control'
       OR parsed IS NULL
       OR parsed #>> '{request,target}'<>'run'
       OR parsed #>> '{request,mode}'<>'cancel'
       OR parsed #>> '{request,reason_code}'<>'user_cancel'
       OR NOT agent_control.runtime_identifier_valid(
            p_subject_principal_id
       ) THEN
        RAISE EXCEPTION USING ERRCODE='42501',
            MESSAGE='Cortex Run cancellation denied';
    END IF;
    request_id_value:=parsed #>> '{request,request_id}';
    SELECT subject_principal_id INTO existing_subject
    FROM agent_control.cortex_run_cancellation
    WHERE request_id=request_id_value;
    IF FOUND THEN
        IF existing_subject<>p_subject_principal_id THEN
            RETURN jsonb_build_object(
                'status','denied',
                'reason_code','cancellation_target_not_found'
            );
        END IF;
        RETURN agent_control.reconcile_one_cortex_run_cancellation(
            request_id_value
        );
    END IF;

    SELECT * INTO run_row
    FROM agent_control.runtime_run
    WHERE run_id=parsed #>> '{request,target_id}'
    FOR UPDATE;
    IF NOT FOUND
       OR run_row.origin_initiating_principal_id<>p_subject_principal_id THEN
        RETURN jsonb_build_object(
            'status','denied',
            'reason_code','cancellation_target_not_found'
        );
    END IF;
    IF agent_control.runtime_terminal_state('run',run_row.state) THEN
        RETURN jsonb_build_object(
            'status','terminal',
            'run_id',run_row.run_id,
            'run_state',run_row.state,
            'reason_code','run_already_terminal'
        );
    END IF;

    submit_response:=agent_control.runtime_submit_cancellation_request(
        parsed
    );
    IF submit_response->>'status'<>'committed' THEN
        RETURN submit_response;
    END IF;
    initial_response:=jsonb_build_object(
        'status','canceling',
        'run_id',run_row.run_id,
        'run_state',run_row.state,
        'request_id',request_id_value,
        'reason_code','cancellation_pending_reconciliation'
    );
    INSERT INTO agent_control.cortex_run_cancellation(
        request_id,run_id,schema_revision,subject_principal_id,
        expected_run_generation,state,response,requested_at,updated_at
    ) VALUES(
        request_id_value,run_row.run_id,1,p_subject_principal_id,
        (parsed #>> '{request,expected_state_generation}')::BIGINT,
        'pending',initial_response,
        (parsed #>> '{request,requested_at}')::TIMESTAMPTZ,
        clock_timestamp()
    );
    RETURN agent_control.reconcile_one_cortex_run_cancellation(
        request_id_value
    );
END
$$;

REVOKE ALL ON FUNCTION
agent_control.get_cortex_run_cancellation_result(TEXT,TEXT),
agent_control.cancel_cortex_run(TEXT,TEXT)
FROM PUBLIC;
GRANT EXECUTE ON FUNCTION
agent_control.get_cortex_run_cancellation_result(TEXT,TEXT),
agent_control.cancel_cortex_run(TEXT,TEXT)
TO alpheus_agent_control_api;

RESET ROLE;
