-- Add durable user-cancellation boundaries to the existing Cortex Run trace
-- without reconstructing them from process logs.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

ALTER FUNCTION agent_control.get_cortex_run_trace(TEXT)
  RENAME TO get_cortex_run_trace_pre_cancellation_v1;

CREATE FUNCTION agent_control.get_cortex_run_trace(p_run_id TEXT)
RETURNS JSONB
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security
SET timezone='UTC'
AS $$
DECLARE
  invoker RECORD;
  base_trace JSONB;
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api'
     OR invoker.owner_id<>'agent_control'
     OR NOT agent_control.runtime_identifier_valid(p_run_id) THEN
    RAISE EXCEPTION USING ERRCODE='42501',
      MESSAGE='cortex trace read denied';
  END IF;

  base_trace:=
    agent_control.get_cortex_run_trace_pre_cancellation_v1(p_run_id);
  RETURN COALESCE((
    SELECT jsonb_agg(
      event.payload||jsonb_build_object('sequence',event.sequence)
      ORDER BY event.occurred_at,event.order_key,event.event_id
    )
    FROM (
      SELECT raw.occurred_at,raw.order_key,raw.event_id,raw.payload,
        row_number() OVER (
          ORDER BY raw.occurred_at,raw.order_key,raw.event_id
        ) AS sequence
      FROM (
        SELECT
          (item.payload->>'created_at')::TIMESTAMPTZ AS occurred_at,
          10 AS order_key,
          'base:'||(item.payload->>'sequence') AS event_id,
          item.payload-'sequence' AS payload
        FROM jsonb_array_elements(base_trace) AS item(payload)

        UNION ALL

        SELECT
          cancellation.requested_at,
          90,
          'cancel-requested:'||cancellation.request_id,
          jsonb_build_object(
            'created_at',
              agent_control.runtime_utc_text(cancellation.requested_at),
            'stage','run_cancel_requested',
            'state',CASE WHEN cancellation.state='pending'
              THEN 'canceling' ELSE cancellation.state END,
            'reason_code','user_cancel',
            'request_id',cancellation.request_id
          )
        FROM agent_control.cortex_run_cancellation AS cancellation
        WHERE cancellation.run_id=p_run_id

        UNION ALL

        SELECT
          cancellation.terminal_at,
          95,
          'cancel-terminal:'||cancellation.request_id,
          jsonb_build_object(
            'created_at',
              agent_control.runtime_utc_text(cancellation.terminal_at),
            'stage',CASE cancellation.state
              WHEN 'canceled' THEN 'run_canceled'
              ELSE 'run_cancellation_denied'
            END,
            'state',cancellation.state,
            'reason_code',cancellation.response->>'reason_code',
            'request_id',cancellation.request_id
          )
        FROM agent_control.cortex_run_cancellation AS cancellation
        WHERE cancellation.run_id=p_run_id
          AND cancellation.terminal_at IS NOT NULL
      ) AS raw
    ) AS event
  ),'[]'::JSONB);
END
$$;

REVOKE ALL ON FUNCTION
agent_control.get_cortex_run_trace_pre_cancellation_v1(TEXT),
agent_control.get_cortex_run_trace(TEXT)
FROM PUBLIC;
GRANT EXECUTE ON FUNCTION
agent_control.get_cortex_run_trace(TEXT)
TO alpheus_agent_control_api;

RESET ROLE;
