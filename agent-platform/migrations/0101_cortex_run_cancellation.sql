-- Reconcile an authenticated user's immutable cancellation request into the
-- complete effect-none Cortex execution tree. Unknown provider outcomes keep
-- the Run in canceling until their existing recovery path reaches a known
-- terminal state; cancellation never invents an outcome.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

CREATE TABLE agent_control.cortex_run_cancellation (
    request_id TEXT PRIMARY KEY REFERENCES
        agent_control.runtime_cancellation_request(request_id),
    run_id TEXT NOT NULL REFERENCES agent_control.runtime_run(run_id),
    schema_revision SMALLINT NOT NULL CHECK (schema_revision=1),
    subject_principal_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(subject_principal_id)
    ),
    expected_run_generation BIGINT NOT NULL CHECK (
        expected_run_generation>0
    ),
    state TEXT NOT NULL CHECK (state IN ('pending','canceled','denied')),
    response JSONB NOT NULL CHECK (
        jsonb_typeof(response)='object'
        AND octet_length(response::TEXT)<=16384
    ),
    requested_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    terminal_at TIMESTAMPTZ,
    CHECK (requested_at<=updated_at),
    CHECK (
        (state='pending' AND terminal_at IS NULL)
        OR (state IN ('canceled','denied') AND terminal_at IS NOT NULL
            AND terminal_at>=requested_at)
    )
);

CREATE FUNCTION agent_control.guard_cortex_run_cancellation()
RETURNS TRIGGER
LANGUAGE plpgsql
SET search_path=pg_catalog,agent_control
SET timezone='UTC'
AS $$
BEGIN
    IF TG_OP='DELETE'
       OR NEW.request_id<>OLD.request_id
       OR NEW.run_id<>OLD.run_id
       OR NEW.schema_revision<>OLD.schema_revision
       OR NEW.subject_principal_id<>OLD.subject_principal_id
       OR NEW.expected_run_generation<>OLD.expected_run_generation
       OR NEW.requested_at<>OLD.requested_at
       OR NEW.updated_at<OLD.updated_at
       OR OLD.state IN ('canceled','denied')
       OR (OLD.state='pending' AND NEW.state NOT IN (
            'pending','canceled','denied'
       )) THEN
        RAISE EXCEPTION USING ERRCODE='55000',
            MESSAGE='invalid Cortex Run cancellation mutation';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER cortex_run_cancellation_guard
BEFORE UPDATE OR DELETE ON agent_control.cortex_run_cancellation
FOR EACH ROW EXECUTE FUNCTION
agent_control.guard_cortex_run_cancellation();

CREATE FUNCTION agent_control.get_cortex_run_cancellation_seed(
    p_run_id TEXT,
    p_subject_principal_id TEXT
) RETURNS JSONB
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security
SET timezone='UTC'
AS $$
DECLARE
    invoker RECORD;
    run_row agent_control.runtime_run%ROWTYPE;
BEGIN
    SELECT * INTO STRICT invoker
    FROM platform_security.invoker_identity();
    IF invoker.group_role::TEXT<>'alpheus_agent_control_api'
       OR invoker.profile_id<>'control-api'
       OR invoker.owner_id<>'agent_control'
       OR NOT agent_control.runtime_identifier_valid(p_run_id)
       OR NOT agent_control.runtime_identifier_valid(
            p_subject_principal_id
       ) THEN
        RAISE EXCEPTION USING ERRCODE='42501',
            MESSAGE='Cortex Run cancellation seed denied';
    END IF;
    SELECT * INTO run_row
    FROM agent_control.runtime_run
    WHERE run_id=p_run_id;
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
            'run_state_generation',run_row.state_generation
        );
    END IF;
    RETURN jsonb_build_object(
        'status','ready',
        'run_id',run_row.run_id,
        'run_state',run_row.state,
        'run_state_generation',run_row.state_generation
    );
END
$$;

CREATE FUNCTION agent_control.reconcile_one_cortex_run_cancellation(
    p_request_id TEXT
) RETURNS JSONB
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security
SET timezone='UTC'
AS $$
DECLARE
    invoker RECORD;
    cancellation_row agent_control.cortex_run_cancellation%ROWTYPE;
    run_row agent_control.runtime_run%ROWTYPE;
    turn_row agent_control.runtime_turn%ROWTYPE;
    manifest_row agent_control.runtime_model_call_manifest%ROWTYPE;
    attempt_row agent_control.runtime_attempt%ROWTYPE;
    session_row agent_control.runtime_session%ROWTYPE;
    task_row agent_control.runtime_task%ROWTYPE;
    schedule_row agent_control.cortex_task_graph_schedule%ROWTYPE;
    at_time TIMESTAMPTZ:=clock_timestamp();
    run_generation BIGINT;
    unknown_turns BIGINT;
    turn_count BIGINT:=0;
    attempt_count BIGINT:=0;
    session_count BIGINT:=0;
    task_count BIGINT:=0;
    response_value JSONB;
BEGIN
    SELECT * INTO STRICT invoker
    FROM platform_security.invoker_identity();
    IF invoker.group_role::TEXT<>'alpheus_agent_control_api'
       OR invoker.profile_id<>'control-api'
       OR invoker.owner_id<>'agent_control'
       OR NOT agent_control.runtime_identifier_valid(p_request_id) THEN
        RAISE EXCEPTION USING ERRCODE='42501',
            MESSAGE='Cortex Run cancellation reconciliation denied';
    END IF;

    SELECT * INTO cancellation_row
    FROM agent_control.cortex_run_cancellation
    WHERE request_id=p_request_id
    FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION USING ERRCODE='22023',
            MESSAGE='Cortex Run cancellation not found';
    END IF;
    IF cancellation_row.state<>'pending' THEN
        RETURN cancellation_row.response;
    END IF;

    SELECT * INTO STRICT run_row
    FROM agent_control.runtime_run
    WHERE run_id=cancellation_row.run_id
    FOR UPDATE;
    IF run_row.origin_initiating_principal_id<>
       cancellation_row.subject_principal_id THEN
        RAISE EXCEPTION USING ERRCODE='42501',
            MESSAGE='Cortex Run cancellation owner changed';
    END IF;

    IF agent_control.runtime_terminal_state('run',run_row.state) THEN
        response_value:=jsonb_build_object(
            'status',CASE WHEN run_row.state='canceled'
                THEN 'canceled' ELSE 'denied' END,
            'run_id',run_row.run_id,
            'run_state',run_row.state,
            'request_id',cancellation_row.request_id,
            'reason_code',CASE WHEN run_row.state='canceled'
                THEN 'user_cancel' ELSE 'run_already_terminal' END,
            'terminal_at',agent_control.runtime_utc_text(
                coalesce(run_row.terminal_at,at_time)
            )
        );
        UPDATE agent_control.cortex_run_cancellation
        SET state=CASE WHEN run_row.state='canceled'
                THEN 'canceled' ELSE 'denied' END,
            response=response_value,updated_at=at_time,terminal_at=at_time
        WHERE request_id=cancellation_row.request_id;
        RETURN response_value;
    END IF;

    run_generation:=run_row.state_generation;
    IF run_row.state IN ('running','waiting') THEN
        UPDATE agent_control.runtime_run
        SET state='canceling',
            state_generation=run_generation+1,
            updated_at=greatest(updated_at,at_time)
        WHERE run_id=run_row.run_id;
        PERFORM agent_control.cortex_insert_control_event(
            'run',run_row.run_id,run_row.state,'canceling',
            run_generation+1,invoker.principal_id,
            cancellation_row.request_id,run_row.run_id,
            'user_cancel_requested',at_time
        );
        run_row.state:='canceling';
        run_generation:=run_generation+1;
    END IF;

    SELECT count(*) INTO unknown_turns
    FROM agent_control.runtime_turn
    WHERE run_id=run_row.run_id AND state='unknown';
    IF unknown_turns>0 THEN
        response_value:=jsonb_build_object(
            'status','canceling',
            'run_id',run_row.run_id,
            'run_state',run_row.state,
            'request_id',cancellation_row.request_id,
            'reason_code','provider_outcome_reconciliation_pending',
            'unknown_turns',unknown_turns
        );
        UPDATE agent_control.cortex_run_cancellation
        SET response=response_value,updated_at=at_time
        WHERE request_id=cancellation_row.request_id;
        RETURN response_value;
    END IF;

    FOR turn_row IN
        SELECT turn.*
        FROM agent_control.runtime_turn AS turn
        WHERE turn.run_id=run_row.run_id
          AND turn.state IN ('planned','dispatched')
        ORDER BY turn.created_at,turn.turn_id
        FOR UPDATE
    LOOP
        IF turn_row.state='dispatched' THEN
            SELECT * INTO STRICT manifest_row
            FROM agent_control.runtime_model_call_manifest
            WHERE turn_id=turn_row.turn_id;
            SELECT * INTO STRICT task_row
            FROM agent_control.runtime_task
            WHERE task_id=turn_row.task_id;
            IF NOT agent_control.runtime_settle_model_budget_ancestors(
                run_row.run_id,task_row.budget_ledger_id,
                manifest_row.reserved_input_tokens,
                manifest_row.max_output_tokens,
                manifest_row.reserved_external_cost_micro_usd,
                manifest_row.timeout_ms,
                0,0,0,0,
                invoker.principal_id,cancellation_row.request_id,
                run_row.run_id,at_time
            ) THEN
                RAISE EXCEPTION USING ERRCODE='40001',
                    MESSAGE='canceled Turn reservation changed';
            END IF;
        END IF;
        UPDATE agent_control.runtime_turn
        SET state='canceled',
            state_generation=turn_row.state_generation+1,
            reservation_held=false,
            updated_at=greatest(updated_at,at_time),
            finished_at=at_time
        WHERE turn_id=turn_row.turn_id;
        PERFORM agent_control.cortex_insert_control_event(
            'turn',turn_row.turn_id,turn_row.state,'canceled',
            turn_row.state_generation+1,invoker.principal_id,
            cancellation_row.request_id,run_row.run_id,
            'user_cancel',at_time
        );
        turn_count:=turn_count+1;
    END LOOP;

    FOR attempt_row IN
        SELECT attempt.*
        FROM agent_control.runtime_attempt AS attempt
        WHERE attempt.run_id=run_row.run_id
          AND attempt.state IN ('leased','executing')
        ORDER BY attempt.created_at,attempt.attempt_id
        FOR UPDATE
    LOOP
        UPDATE agent_control.runtime_attempt
        SET state='canceled',
            state_generation=attempt_row.state_generation+1,
            updated_at=greatest(updated_at,at_time),
            terminal_at=at_time
        WHERE attempt_id=attempt_row.attempt_id;
        INSERT INTO agent_control.runtime_attempt_lease_event(
            event_id,schema_revision,attempt_id,event_generation,
            lease_generation,transition,worker_principal_id,lease_token,
            previous_expires_at,new_expires_at,actor,causation_id,
            correlation_id,occurred_at
        ) VALUES(
            gen_random_uuid()::TEXT,1,attempt_row.attempt_id,
            (SELECT coalesce(max(event.event_generation),0)+1
             FROM agent_control.runtime_attempt_lease_event AS event
             WHERE event.attempt_id=attempt_row.attempt_id),
            attempt_row.lease_generation,'released',
            attempt_row.lease_worker->>'principal_id',
            attempt_row.lease_token,attempt_row.lease_expires_at,NULL,
            attempt_row.lease_worker,cancellation_row.request_id,
            run_row.run_id,at_time
        );
        PERFORM agent_control.cortex_insert_control_event(
            'attempt',attempt_row.attempt_id,attempt_row.state,'canceled',
            attempt_row.state_generation+1,invoker.principal_id,
            cancellation_row.request_id,run_row.run_id,
            'user_cancel',at_time
        );
        attempt_count:=attempt_count+1;
    END LOOP;

    FOR session_row IN
        SELECT session.*
        FROM agent_control.runtime_session AS session
        WHERE session.run_id=run_row.run_id AND session.state='open'
        ORDER BY session.created_at,session.session_id
        FOR UPDATE
    LOOP
        UPDATE agent_control.runtime_session
        SET state='closed',generation=session_row.generation+1,
            closed_at=at_time
        WHERE session_id=session_row.session_id;
        PERFORM agent_control.cortex_insert_control_event(
            'session',session_row.session_id,'open','closed',
            session_row.generation+1,invoker.principal_id,
            cancellation_row.request_id,run_row.run_id,
            'user_cancel',at_time
        );
        session_count:=session_count+1;
    END LOOP;

    FOR task_row IN
        SELECT task.*
        FROM agent_control.runtime_task AS task
        WHERE task.run_id=run_row.run_id
          AND NOT agent_control.runtime_terminal_state('task',task.state)
        ORDER BY task.depth DESC,task.created_at,task.task_id
        FOR UPDATE
    LOOP
        UPDATE agent_control.runtime_task
        SET state='canceled',
            state_generation=task_row.state_generation+1,
            budget_slot_held=false,
            updated_at=greatest(updated_at,at_time),
            terminal_at=at_time
        WHERE task_id=task_row.task_id;
        IF task_row.budget_slot_held
           AND NOT agent_control.runtime_release_active_slot_ancestors(
                run_row.run_id,task_row.budget_ledger_id,at_time
           ) THEN
            RAISE EXCEPTION USING ERRCODE='40001',
                MESSAGE='canceled Task active slot changed';
        END IF;
        PERFORM agent_control.cortex_insert_control_event(
            'task',task_row.task_id,task_row.state,'canceled',
            task_row.state_generation+1,invoker.principal_id,
            cancellation_row.request_id,run_row.run_id,
            'user_cancel',at_time
        );
        task_count:=task_count+1;
    END LOOP;

    FOR schedule_row IN
        SELECT schedule.*
        FROM agent_control.cortex_task_graph_schedule AS schedule
        JOIN agent_control.cortex_task_graph AS graph
          ON graph.graph_id=schedule.graph_id
        WHERE graph.run_id=run_row.run_id AND schedule.state='open'
        ORDER BY schedule.graph_id
        FOR UPDATE OF schedule
    LOOP
        IF schedule_row.active_tasks<>0 THEN
            RAISE EXCEPTION USING ERRCODE='40001',
                MESSAGE='canceled TaskGraph still has active tasks';
        END IF;
        UPDATE agent_control.cortex_task_graph_schedule
        SET state='closed',generation=schedule_row.generation+1,
            updated_at=greatest(updated_at,at_time),closed_at=at_time
        WHERE graph_id=schedule_row.graph_id;
    END LOOP;

    IF run_row.state='queued' THEN
        UPDATE agent_control.runtime_run
        SET state='canceled',
            state_generation=run_generation+1,
            updated_at=greatest(updated_at,at_time),
            terminal_at=at_time
        WHERE run_id=run_row.run_id;
        PERFORM agent_control.cortex_insert_control_event(
            'run',run_row.run_id,'queued','canceled',
            run_generation+1,invoker.principal_id,
            cancellation_row.request_id,run_row.run_id,
            'user_cancel',at_time
        );
    ELSE
        UPDATE agent_control.runtime_run
        SET state='canceled',
            state_generation=run_generation+1,
            updated_at=greatest(updated_at,at_time),
            terminal_at=at_time
        WHERE run_id=run_row.run_id;
        PERFORM agent_control.cortex_insert_control_event(
            'run',run_row.run_id,'canceling','canceled',
            run_generation+1,invoker.principal_id,
            cancellation_row.request_id,run_row.run_id,
            'user_cancel',at_time
        );
    END IF;

    response_value:=jsonb_build_object(
        'status','canceled',
        'run_id',run_row.run_id,
        'run_state','canceled',
        'request_id',cancellation_row.request_id,
        'reason_code','user_cancel',
        'canceled_at',agent_control.runtime_utc_text(at_time),
        'terminalized_turns',turn_count,
        'terminalized_attempts',attempt_count,
        'closed_sessions',session_count,
        'terminalized_tasks',task_count
    );
    UPDATE agent_control.cortex_run_cancellation
    SET state='canceled',response=response_value,
        updated_at=at_time,terminal_at=at_time
    WHERE request_id=cancellation_row.request_id;
    RETURN response_value;
END
$$;

CREATE FUNCTION agent_control.get_cortex_run_cancellation_result(
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
    found RECORD;
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
    SELECT cancellation.response INTO found
    FROM agent_control.cortex_run_cancellation AS cancellation
    WHERE cancellation.request_id=p_request_id
      AND cancellation.subject_principal_id=p_subject_principal_id;
    IF NOT FOUND THEN RETURN NULL; END IF;
    RETURN found.response;
END
$$;

CREATE FUNCTION agent_control.cancel_cortex_run(
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
    IF EXISTS(
        SELECT 1 FROM agent_control.cortex_run_cancellation
        WHERE request_id=request_id_value
    ) THEN
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

CREATE FUNCTION agent_control.reconcile_cortex_run_cancellations(
    p_limit INTEGER
) RETURNS JSONB
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security
SET timezone='UTC'
AS $$
DECLARE
    invoker RECORD;
    candidate RECORD;
    result JSONB;
    processed BIGINT:=0;
    canceled BIGINT:=0;
    pending BIGINT:=0;
BEGIN
    SELECT * INTO STRICT invoker
    FROM platform_security.invoker_identity();
    IF invoker.group_role::TEXT<>'alpheus_agent_control_api'
       OR invoker.profile_id<>'control-api'
       OR invoker.owner_id<>'agent_control'
       OR p_limit IS NULL OR p_limit NOT BETWEEN 1 AND 32 THEN
        RAISE EXCEPTION USING ERRCODE='42501',
            MESSAGE='Cortex Run cancellation batch denied';
    END IF;
    FOR candidate IN
        SELECT request_id
        FROM agent_control.cortex_run_cancellation
        WHERE state='pending'
        ORDER BY requested_at,request_id
        LIMIT p_limit
    LOOP
        result:=agent_control.reconcile_one_cortex_run_cancellation(
            candidate.request_id
        );
        processed:=processed+1;
        IF result->>'status'='canceled' THEN
            canceled:=canceled+1;
        ELSE
            pending:=pending+1;
        END IF;
    END LOOP;
    RETURN jsonb_build_object(
        'status','reconciled',
        'processed',processed,
        'canceled',canceled,
        'pending',pending
    );
END
$$;

REVOKE ALL ON TABLE
agent_control.cortex_run_cancellation FROM PUBLIC;
REVOKE ALL ON FUNCTION
agent_control.guard_cortex_run_cancellation(),
agent_control.get_cortex_run_cancellation_seed(TEXT,TEXT),
agent_control.get_cortex_run_cancellation_result(TEXT,TEXT),
agent_control.reconcile_one_cortex_run_cancellation(TEXT),
agent_control.cancel_cortex_run(TEXT,TEXT),
agent_control.reconcile_cortex_run_cancellations(INTEGER)
FROM PUBLIC;
GRANT EXECUTE ON FUNCTION
agent_control.get_cortex_run_cancellation_seed(TEXT,TEXT),
agent_control.get_cortex_run_cancellation_result(TEXT,TEXT),
agent_control.cancel_cortex_run(TEXT,TEXT),
agent_control.reconcile_cortex_run_cancellations(INTEGER)
TO alpheus_agent_control_api;

RESET ROLE;
