SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

-- 0029's candidate subquery owns the timestamp alias; refer to that projected
-- value in the outer aggregation so an idle reconciler can poll safely.
CREATE OR REPLACE FUNCTION agent_control.list_cortex_scout_continuation_candidates(p_limit INTEGER)
RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security SET timezone='UTC' AS $$
DECLARE invoker RECORD;
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api' OR invoker.profile_id<>'control-api'
     OR invoker.owner_id<>'agent_control' OR p_limit IS NULL OR p_limit NOT BETWEEN 1 AND 32 THEN
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='cortex scout continuation list denied';
  END IF;
  RETURN COALESCE((SELECT jsonb_agg(jsonb_build_object('request_id',candidate.request_id)
      ORDER BY candidate.created_at,candidate.request_id)
    FROM (
      SELECT admission.request_id,artifact.created_at
      FROM agent_control.cortex_scout_child_admission admission
      JOIN agent_control.runtime_task child ON child.task_id=admission.child_task_id
      JOIN agent_control.runtime_artifact artifact ON artifact.task_id=child.task_id
      WHERE admission.state='admitted' AND child.state='succeeded'
        AND artifact.artifact_type='scout_research_memo'
        AND NOT EXISTS (SELECT 1 FROM agent_control.cortex_parent_continuation continuation
          WHERE continuation.admission_request_id=admission.request_id)
      ORDER BY artifact.created_at,admission.request_id LIMIT p_limit
    ) AS candidate),'[]'::JSONB);
END $$;

REVOKE ALL ON FUNCTION agent_control.list_cortex_scout_continuation_candidates(INTEGER) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agent_control.list_cortex_scout_continuation_candidates(INTEGER) TO alpheus_agent_control_api;
RESET ROLE;
