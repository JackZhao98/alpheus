-- A persisted Tool intent must not be abandoned merely because the original
-- Worker or Control HTTP request disappeared.  This migration introduces a
-- small, explicitly scoped reconciler for the only installed Tool: the
-- idempotent public web fetch.  It never creates an intent, changes a Tool
-- request, or resumes an Attempt; it can only re-drive the exact ToolCallID
-- until Research has durably produced the one matching receipt.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

CREATE TABLE agent_control.cortex_tool_recovery (
    tool_call_id TEXT PRIMARY KEY REFERENCES agent_control.cortex_tool_call_intent(tool_call_id),
    state TEXT NOT NULL CHECK (state IN ('pending','leased','succeeded')),
    generation BIGINT NOT NULL CHECK (generation > 0),
    eligible_at TIMESTAMPTZ NOT NULL,
    next_attempt_at TIMESTAMPTZ NOT NULL,
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0 AND attempt_count <= 1000000),
    lease_token UUID,
    lease_owner TEXT CHECK (lease_owner IS NULL OR agent_control.runtime_identifier_valid(lease_owner)),
    lease_expires_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    CHECK (next_attempt_at >= eligible_at),
    CHECK (
        (state='pending' AND lease_token IS NULL AND lease_owner IS NULL AND lease_expires_at IS NULL AND completed_at IS NULL)
        OR (state='leased' AND lease_token IS NOT NULL AND lease_owner IS NOT NULL AND lease_expires_at IS NOT NULL AND completed_at IS NULL)
        OR (state='succeeded' AND lease_token IS NULL AND lease_owner IS NULL AND lease_expires_at IS NULL AND completed_at IS NOT NULL)
    )
);
CREATE INDEX cortex_tool_recovery_ready_idx
    ON agent_control.cortex_tool_recovery(state,next_attempt_at,tool_call_id)
    WHERE state IN ('pending','leased');

CREATE TABLE agent_control.cortex_tool_recovery_event (
    event_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tool_call_id TEXT NOT NULL REFERENCES agent_control.cortex_tool_call_intent(tool_call_id),
    generation BIGINT NOT NULL CHECK (generation > 0),
    transition TEXT NOT NULL CHECK (transition IN ('scheduled','claimed','reclaimed','requeued','receipt_acknowledged')),
    actor_principal_id TEXT NOT NULL CHECK (agent_control.runtime_identifier_valid(actor_principal_id)),
    reason_code TEXT NOT NULL CHECK (reason_code ~ '^[a-z][a-z0-9_]{0,63}$'),
    occurred_at TIMESTAMPTZ NOT NULL,
    details JSONB NOT NULL DEFAULT '{}'::JSONB CHECK (jsonb_typeof(details)='object')
);
CREATE INDEX cortex_tool_recovery_event_lookup_idx
    ON agent_control.cortex_tool_recovery_event(tool_call_id,occurred_at,event_id);

-- Intents receive a short quiet period for the synchronous Control -> Research
-- request.  Only then can a durable background reconciler claim them.  The
-- delay eliminates normal-path races without making recovery depend on a
-- still-valid Worker lease.
CREATE FUNCTION agent_control.schedule_cortex_tool_recovery()
RETURNS TRIGGER LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control SET timezone='UTC' AS $$
DECLARE ready_at TIMESTAMPTZ:=NEW.authorized_at+make_interval(secs=>45);
BEGIN
  INSERT INTO agent_control.cortex_tool_recovery(
      tool_call_id,state,generation,eligible_at,next_attempt_at
  ) VALUES (NEW.tool_call_id,'pending',1,ready_at,ready_at);
  INSERT INTO agent_control.cortex_tool_recovery_event(
      tool_call_id,generation,transition,actor_principal_id,reason_code,occurred_at,details
  ) VALUES (
      NEW.tool_call_id,1,'scheduled',NEW.authorized_by,'tool_intent_authorized',NEW.authorized_at,
      jsonb_build_object('eligible_at',agent_control.runtime_utc_text(ready_at))
  );
  RETURN NEW;
END $$;
CREATE TRIGGER cortex_tool_recovery_schedule
AFTER INSERT ON agent_control.cortex_tool_call_intent
FOR EACH ROW EXECUTE FUNCTION agent_control.schedule_cortex_tool_recovery();

-- A persisted acknowledgement is the single terminal truth for an idempotent
-- Tool call.  Marking the recovery row here covers both the synchronous path
-- and a recovery worker without giving either direct table-write authority.
CREATE FUNCTION agent_control.acknowledge_cortex_tool_recovery()
RETURNS TRIGGER LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security SET timezone='UTC' AS $$
DECLARE recovery agent_control.cortex_tool_recovery%ROWTYPE; invoker RECORD; at_time TIMESTAMPTZ:=clock_timestamp();
BEGIN
  SELECT * INTO recovery FROM agent_control.cortex_tool_recovery WHERE tool_call_id=NEW.tool_call_id FOR UPDATE;
  IF NOT FOUND OR recovery.state='succeeded' THEN RETURN NEW; END IF;
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  UPDATE agent_control.cortex_tool_recovery
     SET state='succeeded',generation=recovery.generation+1,lease_token=NULL,lease_owner=NULL,
         lease_expires_at=NULL,completed_at=at_time
   WHERE tool_call_id=NEW.tool_call_id;
  INSERT INTO agent_control.cortex_tool_recovery_event(
      tool_call_id,generation,transition,actor_principal_id,reason_code,occurred_at,details
  ) VALUES (
      NEW.tool_call_id,recovery.generation+1,'receipt_acknowledged',invoker.principal_id,
      'research_receipt_acknowledged',at_time,jsonb_build_object('receipt_id',NEW.receipt_id)
  );
  RETURN NEW;
END $$;
CREATE TRIGGER cortex_tool_recovery_acknowledge
AFTER INSERT ON agent_control.cortex_tool_receipt_ack
FOR EACH ROW EXECUTE FUNCTION agent_control.acknowledge_cortex_tool_recovery();

-- Existing local data may contain a historical intent from just before this
-- migration.  Seed it deterministically; an already acknowledged intent is
-- terminal and never becomes a recovery candidate.
INSERT INTO agent_control.cortex_tool_recovery(
    tool_call_id,state,generation,eligible_at,next_attempt_at,completed_at
)
SELECT intent.tool_call_id,
       CASE WHEN ack.tool_call_id IS NULL THEN 'pending' ELSE 'succeeded' END,
       1,
       intent.authorized_at+make_interval(secs=>45),
       intent.authorized_at+make_interval(secs=>45),
       CASE WHEN ack.tool_call_id IS NULL THEN NULL ELSE ack.acknowledged_at END
  FROM agent_control.cortex_tool_call_intent intent
  LEFT JOIN agent_control.cortex_tool_receipt_ack ack ON ack.tool_call_id=intent.tool_call_id
ON CONFLICT (tool_call_id) DO NOTHING;

CREATE FUNCTION agent_control.claim_cortex_tool_recoveries(p_limit INTEGER,p_lease_seconds INTEGER)
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
    UPDATE agent_control.cortex_tool_recovery
       SET state='leased',generation=candidate.generation+1,attempt_count=candidate.attempt_count+1,
           lease_token=token,lease_owner=invoker.principal_id,lease_expires_at=expiry
     WHERE tool_call_id=candidate.tool_call_id;
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

-- A failed replay is retryable only while its claim remains current.  The
-- reason is deliberately a bounded code, never an upstream body or secret.
CREATE FUNCTION agent_control.requeue_cortex_tool_recovery(
    p_tool_call_id TEXT,p_lease_generation BIGINT,p_lease_token UUID,p_reason_code TEXT
) RETURNS BOOLEAN LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security SET timezone='UTC' AS $$
DECLARE invoker RECORD; recovery agent_control.cortex_tool_recovery%ROWTYPE; at_time TIMESTAMPTZ:=clock_timestamp();
  delay_seconds INTEGER;
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api' OR invoker.profile_id<>'control-api'
     OR invoker.owner_id<>'agent_control' OR NOT agent_control.runtime_identifier_valid(p_tool_call_id)
     OR p_lease_generation<1 OR p_lease_token IS NULL OR p_reason_code !~ '^[a-z][a-z0-9_]{0,63}$' THEN
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='cortex tool recovery requeue denied';
  END IF;
  SELECT * INTO recovery FROM agent_control.cortex_tool_recovery WHERE tool_call_id=p_tool_call_id FOR UPDATE;
  IF NOT FOUND OR recovery.state<>'leased' OR recovery.generation<>p_lease_generation
     OR recovery.lease_token<>p_lease_token OR recovery.lease_owner<>invoker.principal_id
     OR recovery.lease_expires_at<=at_time THEN
    RETURN false;
  END IF;
  IF EXISTS (SELECT 1 FROM agent_control.cortex_tool_receipt_ack ack WHERE ack.tool_call_id=p_tool_call_id) THEN
    RETURN false;
  END IF;
  delay_seconds:=LEAST(300,15*power(2,LEAST(4,GREATEST(0,recovery.attempt_count-1)))::INTEGER);
  UPDATE agent_control.cortex_tool_recovery
     SET state='pending',generation=recovery.generation+1,lease_token=NULL,lease_owner=NULL,lease_expires_at=NULL,
         next_attempt_at=at_time+make_interval(secs=>delay_seconds)
   WHERE tool_call_id=p_tool_call_id;
  INSERT INTO agent_control.cortex_tool_recovery_event(
      tool_call_id,generation,transition,actor_principal_id,reason_code,occurred_at,details
  ) VALUES (
      p_tool_call_id,recovery.generation+1,'requeued',invoker.principal_id,p_reason_code,at_time,
      jsonb_build_object('next_attempt_at',agent_control.runtime_utc_text(at_time+make_interval(secs=>delay_seconds)))
  );
  RETURN true;
END $$;

-- Research first reads this immutable pair on every retry.  Therefore a lost
-- Control response returns the persisted receipt instead of issuing another
-- public fetch after the original call already succeeded.
CREATE FUNCTION agent_control.get_research_web_fetch_receipt(p_tool_call_id TEXT)
RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security SET timezone='UTC' AS $$
DECLARE invoker RECORD; receipt agent_control.research_tool_receipt%ROWTYPE; evidence agent_control.research_web_fetch_evidence%ROWTYPE;
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_research_gateway' OR invoker.profile_id<>'research-gateway'
     OR invoker.owner_id<>'research_gateway' OR NOT agent_control.runtime_identifier_valid(p_tool_call_id) THEN
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='research tool receipt read denied';
  END IF;
  SELECT * INTO receipt FROM agent_control.research_tool_receipt WHERE tool_call_id=p_tool_call_id FOR SHARE;
  IF NOT FOUND THEN RETURN NULL; END IF;
  SELECT * INTO STRICT evidence FROM agent_control.research_web_fetch_evidence WHERE evidence_id=receipt.evidence_id FOR SHARE;
  RETURN jsonb_build_object('receipt',receipt.body,'evidence',evidence.body);
END $$;

REVOKE ALL ON TABLE agent_control.cortex_tool_recovery,agent_control.cortex_tool_recovery_event FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.schedule_cortex_tool_recovery(),agent_control.acknowledge_cortex_tool_recovery() FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.claim_cortex_tool_recoveries(INTEGER,INTEGER) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.requeue_cortex_tool_recovery(TEXT,BIGINT,UUID,TEXT) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.get_research_web_fetch_receipt(TEXT) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agent_control.claim_cortex_tool_recoveries(INTEGER,INTEGER) TO alpheus_agent_control_api;
GRANT EXECUTE ON FUNCTION agent_control.requeue_cortex_tool_recovery(TEXT,BIGINT,UUID,TEXT) TO alpheus_agent_control_api;
GRANT EXECUTE ON FUNCTION agent_control.get_research_web_fetch_receipt(TEXT) TO alpheus_research_gateway;

RESET ROLE;
