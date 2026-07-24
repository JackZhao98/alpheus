-- Subject-scoped, read-only projection for the Agent Console. A Candidate
-- becomes eligible for later authorization only after its source Run commits.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

CREATE FUNCTION agent_control.list_cortex_paper_trade_candidates(
  p_subject_principal_id TEXT,
  p_limit INTEGER
) RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security
SET timezone='UTC' AS $$
DECLARE
  invoker RECORD;
  result JSONB;
BEGIN
  SELECT * INTO STRICT invoker
  FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api'
    OR invoker.profile_id<>'control-api'
    OR invoker.owner_id<>'agent_control'
    OR NOT agent_control.runtime_identifier_valid(p_subject_principal_id)
    OR p_limit<1 OR p_limit>100 THEN
    RAISE EXCEPTION USING ERRCODE='42501',
      MESSAGE='Cortex Paper Candidate projection denied';
  END IF;
  SELECT COALESCE(jsonb_agg(item ORDER BY proposed_at DESC,candidate_id DESC),
    '[]'::JSONB) INTO result
  FROM (
    SELECT
      candidate.proposed_at,
      candidate.candidate_id,
      jsonb_build_object(
        'schema_revision',1,
        'candidate_id',candidate.candidate_id,
        'run_id',candidate.run_id,
        'task_id',candidate.task_id,
        'status',CASE WHEN run.state='succeeded'
          THEN 'proposed' ELSE 'source_not_committed' END,
        'source_run_state',run.state,
        'eligible',run.state='succeeded',
        'proposal',candidate.proposal,
        'record_digest',candidate.record_digest::TEXT,
        'proposed_at',
          agent_control.runtime_utc_text(candidate.proposed_at)
      ) AS item
    FROM agent_control.cortex_paper_trade_candidate AS candidate
    JOIN agent_control.runtime_run AS run
      ON run.run_id=candidate.run_id
    WHERE run.origin_initiating_principal_id=p_subject_principal_id
    ORDER BY candidate.proposed_at DESC,candidate.candidate_id DESC
    LIMIT p_limit
  ) AS selected;
  RETURN result;
END $$;

REVOKE ALL ON FUNCTION
  agent_control.list_cortex_paper_trade_candidates(TEXT,INTEGER)
  FROM PUBLIC;
GRANT EXECUTE ON FUNCTION
  agent_control.list_cortex_paper_trade_candidates(TEXT,INTEGER)
  TO alpheus_agent_control_api;

RESET ROLE;
