-- 0026's returned TABLE column shares a name with the recovery table key.
-- Qualify the UPDATE target explicitly so PostgreSQL cannot resolve it as the
-- PL/pgSQL output variable when the live reconciler claims its first row.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

CREATE OR REPLACE FUNCTION agent_control.claim_cortex_tool_recoveries(p_limit INTEGER,p_lease_seconds INTEGER)
RETURNS TABLE(tool_call_id TEXT,lease_generation BIGINT,lease_token UUID,lease_expires_at TIMESTAMPTZ)
LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security SET timezone='UTC' AS $$
DECLARE invoker RECORD; candidate agent_control.cortex_tool_recovery%ROWTYPE; at_time TIMESTAMPTZ:=clock_timestamp();
  token UUID; expiry TIMESTAMPTZ; transition_name TEXT;
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api' OR invoker.profile_id<>'control-api'
     OR invoker.owner_id<>'agent_control' OR p_limit NOT BETWEEN 1 AND 32 OR p_lease_seconds NOT BETWEEN 5 AND 60 THEN
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='cortex tool recovery claim denied';
  END IF;
  FOR candidate IN
    SELECT recovery.* FROM agent_control.cortex_tool_recovery recovery
     WHERE ((recovery.state='pending' AND recovery.next_attempt_at<=at_time)
         OR (recovery.state='leased' AND recovery.lease_expires_at<=at_time))
       AND NOT EXISTS (SELECT 1 FROM agent_control.cortex_tool_receipt_ack ack WHERE ack.tool_call_id=recovery.tool_call_id)
     ORDER BY recovery.next_attempt_at,recovery.tool_call_id
     LIMIT p_limit FOR UPDATE SKIP LOCKED
  LOOP
    token:=gen_random_uuid();
    expiry:=at_time+make_interval(secs=>p_lease_seconds);
    transition_name:=CASE WHEN candidate.state='leased' THEN 'reclaimed' ELSE 'claimed' END;
    UPDATE agent_control.cortex_tool_recovery AS recovery
       SET state='leased',generation=candidate.generation+1,attempt_count=candidate.attempt_count+1,
           lease_token=token,lease_owner=invoker.principal_id,lease_expires_at=expiry
     WHERE recovery.tool_call_id=candidate.tool_call_id;
    INSERT INTO agent_control.cortex_tool_recovery_event(
        tool_call_id,generation,transition,actor_principal_id,reason_code,occurred_at,details
    ) VALUES (
        candidate.tool_call_id,candidate.generation+1,transition_name,invoker.principal_id,
        CASE WHEN transition_name='reclaimed' THEN 'recovery_lease_expired' ELSE 'recovery_claimed' END,
        at_time,jsonb_build_object('lease_expires_at',agent_control.runtime_utc_text(expiry),'attempt_count',candidate.attempt_count+1)
    );
    tool_call_id:=candidate.tool_call_id;
    lease_generation:=candidate.generation+1;
    lease_token:=token;
    lease_expires_at:=expiry;
    RETURN NEXT;
  END LOOP;
END $$;

REVOKE ALL ON FUNCTION agent_control.claim_cortex_tool_recoveries(INTEGER,INTEGER) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agent_control.claim_cortex_tool_recoveries(INTEGER,INTEGER) TO alpheus_agent_control_api;

RESET ROLE;
