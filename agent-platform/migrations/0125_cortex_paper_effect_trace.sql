-- Project the durable Candidate, Control authorization, and Kernel receipt
-- records into the same ordered Run trace used by Agent Chat and Agent Lab.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

ALTER FUNCTION agent_control.get_cortex_run_trace(TEXT)
  RENAME TO get_cortex_run_trace_pre_paper_effect_v1;

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
    agent_control.get_cortex_run_trace_pre_paper_effect_v1(p_run_id);
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
          candidate.proposed_at,
          80,
          'paper-candidate:'||candidate.candidate_id,
          jsonb_build_object(
            'created_at',
              agent_control.runtime_utc_text(candidate.proposed_at),
            'stage','paper_candidate_proposed',
            'state','proposed',
            'candidate_id',candidate.candidate_id,
            'task_id',candidate.task_id,
            'symbol',candidate.symbol,
            'side',candidate.side
          )
        FROM agent_control.cortex_paper_trade_candidate AS candidate
        WHERE candidate.run_id=p_run_id

        UNION ALL

        SELECT
          effect_auth.authorized_at,
          85,
          'paper-authorization:'||effect_auth.authorization_id,
          jsonb_build_object(
            'created_at',
              agent_control.runtime_utc_text(
                effect_auth.authorized_at
              ),
            'stage','paper_effect_authorized',
            'state','authorized',
            'candidate_id',effect_auth.candidate_id,
            'authorization_id',effect_auth.authorization_id,
            'effect_id',effect_auth.effect_id,
            'authorization_kind',effect_auth.authorization_kind,
            'kernel_mode_generation',
              effect_auth.kernel_mode_generation
          )
        FROM agent_control.cortex_paper_effect_authorization AS effect_auth
        JOIN agent_control.cortex_paper_trade_candidate AS candidate
          ON candidate.candidate_id=effect_auth.candidate_id
        WHERE candidate.run_id=p_run_id

        UNION ALL

        SELECT
          receipt.recorded_at,
          90,
          'paper-receipt:'||receipt.receipt_id,
          jsonb_strip_nulls(jsonb_build_object(
            'created_at',
              agent_control.runtime_utc_text(receipt.recorded_at),
            'stage',CASE receipt.outcome
              WHEN 'succeeded' THEN 'paper_effect_succeeded'
              ELSE 'paper_effect_failed'
            END,
            'state',receipt.outcome,
            'candidate_id',receipt.candidate_id,
            'authorization_id',receipt.authorization_id,
            'receipt_id',receipt.receipt_id,
            'effect_id',receipt.effect_id,
            'http_status',receipt.http_status,
            'error_code',receipt.failure_code
          ))
        FROM agent_control.cortex_paper_effect_receipt AS receipt
        JOIN agent_control.cortex_paper_trade_candidate AS candidate
          ON candidate.candidate_id=receipt.candidate_id
        WHERE candidate.run_id=p_run_id
      ) AS raw
    ) AS event
  ),'[]'::JSONB);
END
$$;

REVOKE ALL ON FUNCTION
agent_control.get_cortex_run_trace_pre_paper_effect_v1(TEXT),
agent_control.get_cortex_run_trace(TEXT)
FROM PUBLIC;
GRANT EXECUTE ON FUNCTION
agent_control.get_cortex_run_trace(TEXT)
TO alpheus_agent_control_api;

RESET ROLE;
