-- Control-owned recovery for complete Cortex execution trees whose immutable
-- Run deadline has passed.  Worker commands cannot safely close a parked root
-- after a crash in planning, graph synthesis, or continuation, so the Control
-- plane performs one bounded, atomic and auditable terminalization.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

CREATE TABLE agent_control.cortex_expired_run_recovery (
    run_id TEXT PRIMARY KEY REFERENCES agent_control.runtime_run(run_id),
    schema_revision SMALLINT NOT NULL CHECK (schema_revision=1),
    prior_run_state TEXT NOT NULL CHECK (prior_run_state IN (
        'queued','running','waiting','canceling'
    )),
    failure JSONB NOT NULL CHECK (
        agent_control.runtime_failure_valid(failure)
    ),
    response JSONB NOT NULL,
    recovered_at TIMESTAMPTZ NOT NULL
);

CREATE TRIGGER cortex_expired_run_recovery_immutable
BEFORE UPDATE OR DELETE ON agent_control.cortex_expired_run_recovery
FOR EACH ROW EXECUTE FUNCTION
agent_control.reject_runtime_immutable_mutation();

CREATE FUNCTION agent_control.reconcile_expired_cortex_runs(
    p_limit INTEGER
) RETURNS JSONB
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security
SET timezone='UTC'
AS $$
DECLARE
    invoker RECORD;
    run_row agent_control.runtime_run%ROWTYPE;
    turn_row agent_control.runtime_turn%ROWTYPE;
    manifest_row agent_control.runtime_model_call_manifest%ROWTYPE;
    attempt_row agent_control.runtime_attempt%ROWTYPE;
    session_row agent_control.runtime_session%ROWTYPE;
    task_row agent_control.runtime_task%ROWTYPE;
    schedule_row agent_control.cortex_task_graph_schedule%ROWTYPE;
    failure_value JSONB;
    response_value JSONB;
    at_time TIMESTAMPTZ;
    recovered_count BIGINT:=0;
    turn_count BIGINT:=0;
    attempt_count BIGINT:=0;
    session_count BIGINT:=0;
    task_count BIGINT:=0;
BEGIN
    SELECT * INTO STRICT invoker
    FROM platform_security.invoker_identity();
    IF invoker.group_role::TEXT<>'alpheus_agent_control_api'
       OR invoker.profile_id<>'control-api'
       OR invoker.owner_id<>'agent_control'
       OR p_limit IS NULL OR p_limit NOT BETWEEN 1 AND 32 THEN
        RAISE EXCEPTION USING ERRCODE='42501',
            MESSAGE='expired Cortex Run reconciliation denied';
    END IF;

    FOR run_row IN
        SELECT run.*
        FROM agent_control.runtime_run AS run
        LEFT JOIN agent_control.cortex_expired_run_recovery AS recovery
          ON recovery.run_id=run.run_id
        WHERE NOT agent_control.runtime_terminal_state('run',run.state)
          AND run.deadline_at<=clock_timestamp()
          AND recovery.run_id IS NULL
        ORDER BY run.deadline_at,run.run_id
        LIMIT p_limit
        FOR UPDATE OF run SKIP LOCKED
    LOOP
        at_time:=clock_timestamp();
        failure_value:=jsonb_build_object(
            'code','runtime_deadline_expired',
            'message','Cortex Run exceeded its immutable deadline and was terminalized by Control recovery',
            'retryable',false
        );

        -- A dispatched/unknown model call has already crossed the provider
        -- boundary. Settle its reservation as one consumed call with no
        -- unverified token/cost usage, then close the Turn. Planned work holds
        -- no reservation and may be canceled directly.
        FOR turn_row IN
            SELECT turn.*
            FROM agent_control.runtime_turn AS turn
            WHERE turn.run_id=run_row.run_id
              AND turn.state IN ('planned','dispatched','unknown')
            ORDER BY turn.created_at,turn.turn_id
            FOR UPDATE
        LOOP
            IF turn_row.state='planned' THEN
                UPDATE agent_control.runtime_turn
                SET state='canceled',
                    state_generation=turn_row.state_generation+1,
                    updated_at=greatest(updated_at,at_time),
                    finished_at=at_time
                WHERE turn_id=turn_row.turn_id;
                PERFORM agent_control.cortex_insert_control_event(
                    'turn',turn_row.turn_id,'planned','canceled',
                    turn_row.state_generation+1,invoker.principal_id,
                    run_row.run_id,run_row.run_id,
                    'runtime_deadline_expired',at_time
                );
            ELSE
                SELECT * INTO STRICT manifest_row
                FROM agent_control.runtime_model_call_manifest AS manifest
                WHERE manifest.turn_id=turn_row.turn_id;
                SELECT * INTO STRICT task_row
                FROM agent_control.runtime_task AS task
                WHERE task.task_id=turn_row.task_id;
                IF NOT agent_control.runtime_settle_model_budget_ancestors(
                    run_row.run_id,task_row.budget_ledger_id,
                    manifest_row.reserved_input_tokens,
                    manifest_row.max_output_tokens,
                    manifest_row.reserved_external_cost_micro_usd,
                    manifest_row.timeout_ms,
                    0,0,0,0,
                    invoker.principal_id,run_row.run_id,run_row.run_id,
                    at_time
                ) THEN
                    RAISE EXCEPTION USING ERRCODE='40001',
                        MESSAGE='expired Turn reservation changed';
                END IF;
                UPDATE agent_control.runtime_turn
                SET state='failed',
                    state_generation=turn_row.state_generation+1,
                    failure=failure_value,
                    reservation_held=false,
                    updated_at=greatest(updated_at,at_time),
                    finished_at=at_time
                WHERE turn_id=turn_row.turn_id;
                PERFORM agent_control.cortex_insert_control_event(
                    'turn',turn_row.turn_id,turn_row.state,'failed',
                    turn_row.state_generation+1,invoker.principal_id,
                    run_row.run_id,run_row.run_id,
                    'runtime_deadline_expired',at_time
                );
            END IF;
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
            SET state='timed_out',
                state_generation=attempt_row.state_generation+1,
                failure=failure_value,
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
                attempt_row.lease_generation,'expired',
                attempt_row.lease_worker->>'principal_id',
                attempt_row.lease_token,attempt_row.lease_expires_at,NULL,
                attempt_row.lease_worker,run_row.run_id,run_row.run_id,at_time
            );
            PERFORM agent_control.cortex_insert_control_event(
                'attempt',attempt_row.attempt_id,attempt_row.state,'timed_out',
                attempt_row.state_generation+1,invoker.principal_id,
                run_row.run_id,run_row.run_id,
                'runtime_deadline_expired',at_time
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
                run_row.run_id,run_row.run_id,
                'runtime_deadline_expired',at_time
            );
            session_count:=session_count+1;
        END LOOP;

        -- Children are visited before their parent so graph parallelism and
        -- active-slot counters reach zero before the graph schedule closes.
        FOR task_row IN
            SELECT task.*
            FROM agent_control.runtime_task AS task
            WHERE task.run_id=run_row.run_id
              AND NOT agent_control.runtime_terminal_state('task',task.state)
            ORDER BY task.depth DESC,task.created_at,task.task_id
            FOR UPDATE
        LOOP
            UPDATE agent_control.runtime_task
            SET state='dead_lettered',
                state_generation=task_row.state_generation+1,
                budget_slot_held=false,
                failure=failure_value,
                updated_at=greatest(updated_at,at_time),
                terminal_at=at_time
            WHERE task_id=task_row.task_id;
            IF task_row.budget_slot_held
               AND NOT agent_control.runtime_release_active_slot_ancestors(
                    run_row.run_id,task_row.budget_ledger_id,at_time
               ) THEN
                RAISE EXCEPTION USING ERRCODE='40001',
                    MESSAGE='expired Task active slot changed';
            END IF;
            PERFORM agent_control.cortex_insert_control_event(
                'task',task_row.task_id,task_row.state,'dead_lettered',
                task_row.state_generation+1,invoker.principal_id,
                run_row.run_id,run_row.run_id,
                'runtime_deadline_expired',at_time
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
                    MESSAGE='expired TaskGraph still has active tasks';
            END IF;
            UPDATE agent_control.cortex_task_graph_schedule
            SET state='closed',generation=schedule_row.generation+1,
                updated_at=greatest(updated_at,at_time),closed_at=at_time
            WHERE graph_id=schedule_row.graph_id;
        END LOOP;

        UPDATE agent_control.runtime_run
        SET state='dead_lettered',
            state_generation=run_row.state_generation+1,
            failure=failure_value,
            updated_at=greatest(updated_at,at_time),
            terminal_at=at_time
        WHERE run_id=run_row.run_id;
        PERFORM agent_control.cortex_insert_control_event(
            'run',run_row.run_id,run_row.state,'dead_lettered',
            run_row.state_generation+1,invoker.principal_id,
            run_row.run_id,run_row.run_id,
            'runtime_deadline_expired',at_time
        );

        response_value:=jsonb_build_object(
            'status','dead_lettered',
            'run_id',run_row.run_id,
            'reason_code','runtime_deadline_expired',
            'recovered_at',agent_control.runtime_utc_text(at_time)
        );
        INSERT INTO agent_control.cortex_expired_run_recovery(
            run_id,schema_revision,prior_run_state,failure,response,recovered_at
        ) VALUES(
            run_row.run_id,1,run_row.state,failure_value,
            response_value,at_time
        );
        recovered_count:=recovered_count+1;
    END LOOP;

    RETURN jsonb_build_object(
        'status','reconciled',
        'recovered_runs',recovered_count,
        'terminalized_turns',turn_count,
        'terminalized_attempts',attempt_count,
        'closed_sessions',session_count,
        'terminalized_tasks',task_count
    );
END
$$;

REVOKE ALL ON TABLE
agent_control.cortex_expired_run_recovery FROM PUBLIC;
REVOKE ALL ON FUNCTION
agent_control.reconcile_expired_cortex_runs(INTEGER) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION
agent_control.reconcile_expired_cortex_runs(INTEGER)
TO alpheus_agent_control_api;

RESET ROLE;
