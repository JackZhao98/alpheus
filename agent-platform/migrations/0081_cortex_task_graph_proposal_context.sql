-- Control reads one lease-bound, schema-validated proposal result and the exact
-- immutable records needed to expand it. The Worker cannot supply Run state,
-- policy, output contracts, raw input, or graph authority.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

CREATE FUNCTION agent_control.get_cortex_task_graph_proposal_context(
  p_source_call_id TEXT,
  p_attempt_id TEXT,
  p_lease_generation BIGINT,
  p_lease_token UUID
)
RETURNS JSONB
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path=pg_catalog,agent_control,agent_input,platform_security,blob
SET timezone='UTC'
AS $$
DECLARE
  invoker RECORD;
  source_row RECORD;
  specialist_contract agent_control.output_contract_revision%ROWTYPE;
  answer_contract agent_control.output_contract_revision%ROWTYPE;
  at_time TIMESTAMPTZ:=clock_timestamp();
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api'
     OR invoker.profile_id<>'control-api'
     OR invoker.owner_id<>'agent_control'
     OR NOT agent_control.runtime_identifier_valid(p_source_call_id)
     OR NOT agent_control.runtime_identifier_valid(p_attempt_id)
     OR p_lease_generation<1 OR p_lease_token IS NULL THEN
    RAISE EXCEPTION USING ERRCODE='22023',
      MESSAGE='invalid Cortex TaskGraph proposal context';
  END IF;

  SELECT
    manifest.call_id,
    manifest.output_contract_digest,
    result.result_id,
    result.record_digest AS result_digest,
    result.output AS proposal_output,
    turn.turn_id,
    attempt.attempt_id,
    task.task_id,
    task.state_generation AS task_state_generation,
    run.run_id,
    run.state_generation AS run_state_generation,
    run.runtime_policy_id,
    run.runtime_policy_generation,
    run.runtime_policy_digest,
    request.raw_input,
    least(run.deadline_at,task.deadline_at) AS deadline_at
  INTO STRICT source_row
  FROM agent_control.runtime_model_call_manifest AS manifest
  JOIN agent_control.runtime_model_call_result AS result
    ON result.call_id=manifest.call_id
   AND result.attempt_id=manifest.attempt_id
   AND result.turn_id=manifest.turn_id
  JOIN agent_control.runtime_cortex_output_validation AS validation
    ON validation.call_id=manifest.call_id
   AND validation.manifest_digest=manifest.record_digest
   AND validation.output_blob_id=(result.output->>'blob_id')::UUID
   AND validation.output_content_digest=result.output->>'content_digest'
  JOIN agent_control.runtime_turn AS turn
    ON turn.turn_id=result.turn_id
   AND turn.attempt_id=result.attempt_id
   AND turn.state='result_committed'
  JOIN agent_control.runtime_attempt AS attempt
    ON attempt.attempt_id=result.attempt_id
  JOIN agent_control.runtime_task AS task
    ON task.task_id=turn.task_id
   AND task.run_id=turn.run_id
   AND task.parent_task_id IS NULL
   AND task.output_contract_revision_id='cortex-workflow-output-v8'
  JOIN agent_control.runtime_run AS run
    ON run.run_id=task.run_id
  JOIN agent_input.user_request AS request
    ON request.request_id=run.origin_source_record_id
   AND request.record_digest=run.origin_source_record_digest
  JOIN agent_control.output_contract_revision AS proposal_contract
    ON proposal_contract.revision_id=
      'cortex-task-graph-proposal-output-v1'
   AND proposal_contract.generation=1
   AND proposal_contract.record_digest=manifest.output_contract_digest
   AND proposal_contract.effect_class='none'
  WHERE manifest.call_id=p_source_call_id
  FOR UPDATE OF attempt,task,run;

  IF source_row.attempt_id<>p_attempt_id
     OR NOT EXISTS (
       SELECT 1
       FROM agent_control.runtime_attempt AS attempt
       WHERE attempt.attempt_id=p_attempt_id
         AND attempt.state='executing'
         AND attempt.lease_generation=p_lease_generation
         AND attempt.lease_token=p_lease_token
         AND attempt.lease_expires_at>at_time
         AND attempt.lease_worker->>'principal_id'='cortex-worker-1'
         AND attempt.lease_worker->>'kind'='workload'
         AND attempt.lease_worker->>'audience'='worker'
     )
     OR NOT EXISTS (
       SELECT 1
       FROM agent_control.runtime_task AS task
       JOIN agent_control.runtime_run AS run ON run.run_id=task.run_id
       JOIN agent_control.runtime_session AS session
         ON session.session_id=task.session_id
       WHERE task.task_id=source_row.task_id
         AND task.state='running'
         AND run.state='running'
         AND session.state='open'
         AND at_time<least(task.deadline_at,run.deadline_at)
     )
     OR EXISTS (
       SELECT 1
       FROM agent_control.cortex_task_graph AS graph
       WHERE graph.parent_task_id=source_row.task_id
     ) THEN
    RAISE EXCEPTION USING ERRCODE='42501',
      MESSAGE='Cortex TaskGraph proposal lease or state denied';
  END IF;

  SELECT * INTO STRICT specialist_contract
  FROM agent_control.output_contract_revision
  WHERE revision_id='cortex-scout-research-memo-v1'
    AND generation=1 AND effect_class='none';
  SELECT * INTO STRICT answer_contract
  FROM agent_control.output_contract_revision
  WHERE revision_id='cortex-text-output-v1'
    AND generation=1 AND effect_class='none';

  RETURN jsonb_build_object(
    'status','ready',
    'run_id',source_row.run_id,
    'parent_task_id',source_row.task_id,
    'run_state_generation',source_row.run_state_generation,
    'parent_task_state_generation',source_row.task_state_generation,
    'source_result',jsonb_build_object(
      'owner','agent_control',
      'record_type','model_call_result',
      'record_id',source_row.result_id,
      'schema_revision',1,
      'record_digest',source_row.result_digest::TEXT),
    'runtime_policy',jsonb_build_object(
      'owner','agent_control',
      'record_type','runtime_policy',
      'record_id',source_row.runtime_policy_id,
      'schema_revision',1,
      'record_digest',source_row.runtime_policy_digest::TEXT,
      'generation',source_row.runtime_policy_generation),
    'specialist_output_contract',jsonb_build_object(
      'owner','agent_control',
      'record_type','output_contract_revision',
      'record_id',specialist_contract.revision_id,
      'schema_revision',1,
      'record_digest',specialist_contract.record_digest::TEXT,
      'generation',specialist_contract.generation),
    'answer_output_contract',jsonb_build_object(
      'owner','agent_control',
      'record_type','output_contract_revision',
      'record_id',answer_contract.revision_id,
      'schema_revision',1,
      'record_digest',answer_contract.record_digest::TEXT,
      'generation',answer_contract.generation),
    'proposal_output',source_row.proposal_output,
    'proposal_output_binding_id',
      'cortex-model-output:'||source_row.call_id,
    'raw_input',source_row.raw_input,
    'deadline_at',agent_control.runtime_utc_text(source_row.deadline_at)
  );
END
$$;

REVOKE ALL ON FUNCTION
  agent_control.get_cortex_task_graph_proposal_context(
    TEXT,TEXT,BIGINT,UUID)
  FROM PUBLIC;
GRANT EXECUTE ON FUNCTION
  agent_control.get_cortex_task_graph_proposal_context(
    TEXT,TEXT,BIGINT,UUID)
  TO alpheus_agent_control_api;

RESET ROLE;
