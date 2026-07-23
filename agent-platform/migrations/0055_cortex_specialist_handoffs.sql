-- Admit only active, versioned Specialist roles as additional Intent handoff
-- targets. The handoff remains an immutable Control record; it does not grant
-- a Tool by itself.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

ALTER TABLE agent_control.cortex_handoff
  DROP CONSTRAINT cortex_handoff_target_role_check;
ALTER TABLE agent_control.cortex_handoff
  ADD CONSTRAINT cortex_handoff_target_role_check CHECK (
    target_role IN (
      'desk','scout','market_scout','fundamental_scout','options_scout',
      'position_manager','catalyst_scout','discovery_scout'
    )
  );

CREATE OR REPLACE FUNCTION agent_control.record_cortex_handoff(
    p_call_id TEXT,p_target_role TEXT,p_objective TEXT,p_rationale TEXT
) RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security SET timezone='UTC' AS $$
DECLARE
  invoker RECORD; source_row RECORD; existing agent_control.cortex_handoff%ROWTYPE;
  handoff_id_value TEXT:=gen_random_uuid()::TEXT; at_time TIMESTAMPTZ:=clock_timestamp();
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api' OR invoker.owner_id<>'agent_control'
    OR NOT agent_control.runtime_identifier_valid(p_call_id)
    OR (
      p_target_role NOT IN ('desk','scout')
      AND NOT EXISTS (
        SELECT 1 FROM agent_control.cortex_agent_role_registry role
        WHERE role.role_id=p_target_role AND role.active
      )
    )
    OR p_objective IS NULL OR p_objective<>btrim(p_objective) OR octet_length(p_objective) NOT BETWEEN 1 AND 4000
    OR p_rationale IS NULL OR p_rationale<>btrim(p_rationale) OR octet_length(p_rationale) NOT BETWEEN 1 AND 4000 THEN
    RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid cortex handoff';
  END IF;
  SELECT manifest.call_id,result.result_id,result.attempt_id,result.turn_id,turn.run_id,turn.task_id INTO STRICT source_row
    FROM agent_control.runtime_model_call_manifest manifest
    JOIN agent_control.runtime_model_call_result result ON result.call_id=manifest.call_id
    JOIN agent_control.runtime_turn turn ON turn.turn_id=result.turn_id
    WHERE manifest.call_id=p_call_id FOR SHARE;
  SELECT * INTO existing FROM agent_control.cortex_handoff WHERE source_call_id=p_call_id;
  IF FOUND THEN
    IF existing.target_role<>p_target_role OR existing.objective<>p_objective OR existing.rationale<>p_rationale THEN
      RAISE EXCEPTION USING ERRCODE='23505',MESSAGE='cortex handoff identity conflict';
    END IF;
    RETURN jsonb_build_object('status','recorded','handoff_id',existing.handoff_id);
  END IF;
  INSERT INTO agent_control.cortex_handoff(
    handoff_id,source_call_id,source_result_id,run_id,task_id,attempt_id,turn_id,
    target_role,objective,rationale,created_at
  ) VALUES(
    handoff_id_value,source_row.call_id,source_row.result_id,source_row.run_id,
    source_row.task_id,source_row.attempt_id,source_row.turn_id,p_target_role,
    p_objective,p_rationale,at_time
  );
  RETURN jsonb_build_object('status','recorded','handoff_id',handoff_id_value);
END $$;

REVOKE ALL ON FUNCTION agent_control.record_cortex_handoff(TEXT,TEXT,TEXT,TEXT) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agent_control.record_cortex_handoff(TEXT,TEXT,TEXT,TEXT) TO alpheus_agent_control_api;

RESET ROLE;
