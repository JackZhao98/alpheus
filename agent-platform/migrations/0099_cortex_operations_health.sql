-- Provide one bounded, read-only operational projection for Cortex. The
-- Control API remains the only caller; the browser never receives table
-- privileges or raw failures, prompts, credentials, or provider payloads.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

CREATE FUNCTION agent_control.get_cortex_operations_health()
RETURNS JSONB
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security
SET timezone='UTC'
AS $$
DECLARE
    invoker RECORD;
    now_at TIMESTAMPTZ:=clock_timestamp();
    active_runs BIGINT;
    succeeded_runs BIGINT;
    failed_runs BIGINT;
    canceled_runs BIGINT;
    dead_lettered_runs BIGINT;
    active_tasks BIGINT;
    failed_tasks BIGINT;
    dead_lettered_tasks BIGINT;
    stalled_runs BIGINT;
    expired_runs BIGINT;
    expired_attempt_leases BIGINT;
    terminal_open_sessions BIGINT;
    terminal_slot_leaks BIGINT;
    authorized_tools BIGINT;
    acknowledged_tools BIGINT;
    overdue_tools BIGINT;
    active_run_rows JSONB;
    recent_failure_rows JSONB;
BEGIN
    SELECT * INTO STRICT invoker
    FROM platform_security.invoker_identity();
    IF invoker.group_role::TEXT<>'alpheus_agent_control_api'
       OR invoker.profile_id<>'control-api'
       OR invoker.owner_id<>'agent_control' THEN
        RAISE EXCEPTION USING ERRCODE='42501',
            MESSAGE='Cortex operations health read denied';
    END IF;

    SELECT
        count(*) FILTER (
            WHERE NOT agent_control.runtime_terminal_state('run',state)
        ),
        count(*) FILTER (
            WHERE state='succeeded' AND terminal_at>=now_at-interval '24 hours'
        ),
        count(*) FILTER (
            WHERE state='failed' AND terminal_at>=now_at-interval '24 hours'
        ),
        count(*) FILTER (
            WHERE state='canceled' AND terminal_at>=now_at-interval '24 hours'
        ),
        count(*) FILTER (
            WHERE state='dead_lettered'
              AND terminal_at>=now_at-interval '24 hours'
        ),
        count(*) FILTER (
            WHERE NOT agent_control.runtime_terminal_state('run',state)
              AND updated_at<now_at-interval '150 seconds'
              AND deadline_at>now_at
        ),
        count(*) FILTER (
            WHERE NOT agent_control.runtime_terminal_state('run',state)
              AND deadline_at<=now_at
        )
    INTO active_runs,succeeded_runs,failed_runs,canceled_runs,
         dead_lettered_runs,stalled_runs,expired_runs
    FROM agent_control.runtime_run;

    SELECT
        count(*) FILTER (
            WHERE NOT agent_control.runtime_terminal_state('task',state)
        ),
        count(*) FILTER (
            WHERE state='failed' AND terminal_at>=now_at-interval '24 hours'
        ),
        count(*) FILTER (
            WHERE state='dead_lettered'
              AND terminal_at>=now_at-interval '24 hours'
        ),
        count(*) FILTER (
            WHERE agent_control.runtime_terminal_state('task',state)
              AND budget_slot_held
        )
    INTO active_tasks,failed_tasks,dead_lettered_tasks,terminal_slot_leaks
    FROM agent_control.runtime_task;

    SELECT count(*) INTO expired_attempt_leases
    FROM agent_control.runtime_attempt
    WHERE state IN ('leased','executing')
      AND lease_expires_at<=now_at;

    SELECT count(*) INTO terminal_open_sessions
    FROM agent_control.runtime_session AS session
    JOIN agent_control.runtime_run AS run ON run.run_id=session.run_id
    WHERE session.state='open'
      AND agent_control.runtime_terminal_state('run',run.state);

    WITH calls AS (
        SELECT intent.tool_call_id,intent.authorized_at,
               ack.acknowledged_at
        FROM agent_control.cortex_tool_call_intent AS intent
        LEFT JOIN agent_control.cortex_tool_receipt_ack AS ack
          ON ack.tool_call_id=intent.tool_call_id
        UNION ALL
        SELECT intent.tool_call_id,intent.authorized_at,
               ack.acknowledged_at
        FROM agent_control.cortex_gexbot_tool_call_intent AS intent
        LEFT JOIN agent_control.cortex_gexbot_tool_receipt_ack AS ack
          ON ack.tool_call_id=intent.tool_call_id
        UNION ALL
        SELECT intent.tool_call_id,intent.authorized_at,
               ack.acknowledged_at
        FROM agent_control.cortex_gexbot_live_tool_call_intent AS intent
        LEFT JOIN agent_control.cortex_gexbot_live_tool_receipt_ack AS ack
          ON ack.tool_call_id=intent.tool_call_id
        UNION ALL
        SELECT intent.tool_call_id,intent.authorized_at,
               ack.acknowledged_at
        FROM agent_control.cortex_kernel_earnings_tool_call_intent AS intent
        LEFT JOIN agent_control.cortex_kernel_earnings_tool_receipt_ack AS ack
          ON ack.tool_call_id=intent.tool_call_id
        UNION ALL
        SELECT intent.tool_call_id,intent.authorized_at,
               ack.acknowledged_at
        FROM agent_control.cortex_kernel_read_tool_call_intent AS intent
        LEFT JOIN agent_control.cortex_kernel_read_tool_receipt_ack AS ack
          ON ack.tool_call_id=intent.tool_call_id
    )
    SELECT
        count(*) FILTER (
            WHERE authorized_at>=now_at-interval '24 hours'
        ),
        count(*) FILTER (
            WHERE acknowledged_at>=now_at-interval '24 hours'
        ),
        count(*) FILTER (
            WHERE acknowledged_at IS NULL
              AND authorized_at<now_at-interval '45 seconds'
        )
    INTO authorized_tools,acknowledged_tools,overdue_tools
    FROM calls;

    SELECT coalesce(jsonb_agg(item ORDER BY item->>'updated_at'), '[]'::JSONB)
    INTO active_run_rows
    FROM (
        SELECT jsonb_build_object(
            'run_id',run.run_id,
            'state',run.state,
            'updated_at',agent_control.runtime_utc_text(run.updated_at),
            'deadline_at',agent_control.runtime_utc_text(run.deadline_at),
            'stale',run.updated_at<now_at-interval '150 seconds'
        ) AS item
        FROM agent_control.runtime_run AS run
        WHERE NOT agent_control.runtime_terminal_state('run',run.state)
        ORDER BY run.updated_at,run.run_id
        LIMIT 20
    ) AS bounded;

    SELECT coalesce(jsonb_agg(item ORDER BY item->>'terminal_at' DESC),
                    '[]'::JSONB)
    INTO recent_failure_rows
    FROM (
        SELECT jsonb_build_object(
            'run_id',run.run_id,
            'state',run.state,
            'terminal_at',agent_control.runtime_utc_text(run.terminal_at),
            'reason_code',coalesce(
                run.failure->>'code',
                run.failure->>'reason_code',
                'run_terminal_failure'
            )
        ) AS item
        FROM agent_control.runtime_run AS run
        WHERE run.state IN ('failed','dead_lettered')
          AND run.terminal_at>=now_at-interval '24 hours'
        ORDER BY run.terminal_at DESC,run.run_id
        LIMIT 10
    ) AS bounded;

    RETURN jsonb_build_object(
        'generated_at',agent_control.runtime_utc_text(now_at),
        'status',CASE
            WHEN stalled_runs>0 OR expired_runs>0
              OR expired_attempt_leases>0 OR overdue_tools>0
              OR terminal_open_sessions>0 OR terminal_slot_leaks>0
            THEN 'degraded' ELSE 'healthy' END,
        'window_hours',24,
        'runs',jsonb_build_object(
            'active',active_runs,
            'succeeded',succeeded_runs,
            'failed',failed_runs,
            'canceled',canceled_runs,
            'dead_lettered',dead_lettered_runs
        ),
        'tasks',jsonb_build_object(
            'active',active_tasks,
            'failed',failed_tasks,
            'dead_lettered',dead_lettered_tasks
        ),
        'risks',jsonb_build_object(
            'stalled_runs',stalled_runs,
            'expired_runs',expired_runs,
            'expired_attempt_leases',expired_attempt_leases,
            'unacknowledged_tool_calls',overdue_tools,
            'terminal_open_sessions',terminal_open_sessions,
            'terminal_slot_leaks',terminal_slot_leaks
        ),
        'tools',jsonb_build_object(
            'authorized',authorized_tools,
            'acknowledged',acknowledged_tools,
            'overdue_unacknowledged',overdue_tools
        ),
        'active_runs',active_run_rows,
        'recent_failures',recent_failure_rows
    );
END
$$;

REVOKE ALL ON FUNCTION
agent_control.get_cortex_operations_health() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION
agent_control.get_cortex_operations_health()
TO alpheus_agent_control_api;

RESET ROLE;
