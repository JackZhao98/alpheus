SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

-- Discovery returns two exact references for a Desk continuation: the
-- original memo BlobRef (for immutable context comparison) and an Artifact-
-- owned read reference (matching the session ACL binding).  The Blob bytes and
-- digest are identical; only the owning reference used for authorization is
-- different.
CREATE OR REPLACE FUNCTION agent_control.next_cortex_task()
RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,agent_input,platform_security,blob SET timezone='UTC' AS $$
DECLARE invoker RECORD; selected RECORD;
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_worker' OR invoker.profile_id<>'worker' THEN
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='cortex Worker discovery denied';
  END IF;
  SELECT task.task_id,task.state_generation,task.output_contract_digest,task.output_contract_revision_id,task.deadline_at,
    session.session_id,session.context_manifest,request.raw_input,
    CASE WHEN scout_admission.request_id IS NOT NULL THEN 'scout'
         WHEN continuation.admission_request_id IS NOT NULL THEN 'desk' ELSE 'intent' END AS role,
    CASE WHEN task.output_contract_revision_id='cortex-workflow-output-v3' THEN true ELSE false END AS scout_enabled,
    handoff.objective,handoff.rationale,
    memo.content AS scout_memo,artifact.artifact_id AS scout_artifact_id,artifact.record_digest::TEXT AS scout_artifact_digest
  INTO selected FROM agent_control.runtime_task task
    JOIN agent_control.runtime_session session ON session.session_id=task.session_id
    JOIN agent_control.runtime_run run ON run.run_id=task.run_id
    JOIN agent_input.user_request request ON request.request_id=run.origin_source_record_id
      AND request.record_digest=run.origin_source_record_digest
    LEFT JOIN agent_control.cortex_scout_child_admission scout_admission
      ON scout_admission.child_task_id=task.task_id AND scout_admission.state='admitted'
    LEFT JOIN agent_control.cortex_parent_continuation continuation ON continuation.parent_task_id=task.task_id
    LEFT JOIN agent_control.cortex_scout_child_admission continued_admission
      ON continued_admission.request_id=continuation.admission_request_id
    LEFT JOIN agent_control.cortex_handoff handoff ON handoff.handoff_id=COALESCE(scout_admission.handoff_id,continued_admission.handoff_id)
    LEFT JOIN agent_control.runtime_artifact artifact ON artifact.artifact_id=continuation.scout_artifact_id
    LEFT JOIN agent_control.runtime_artifact_section memo ON memo.artifact_id=artifact.artifact_id
      AND memo.name='memo' AND memo.required
  -- A retryable Desk failure deliberately returns the parent to waiting while
  -- retaining its active-budget slot. runtime_claim_task admits both ready and
  -- waiting Tasks, so discovery must surface either state for recovery.
  WHERE task.state IN ('ready','waiting') AND session.state='open' AND run.state IN ('queued','running','waiting')
    AND task.deadline_at>clock_timestamp()+interval '90 seconds'
    AND run.deadline_at>clock_timestamp()+interval '90 seconds'
  ORDER BY task.created_at,task.task_id LIMIT 1;
  IF NOT FOUND THEN RETURN NULL; END IF;
  RETURN jsonb_build_object(
    'task_id',selected.task_id,'task_state_generation',selected.state_generation,
    'output_contract_digest',selected.output_contract_digest::TEXT,'deadline',agent_control.runtime_utc_text(selected.deadline_at),
    'session_id',selected.session_id,'context_manifest',selected.context_manifest,
    'context_binding_id','cortex-session:'||selected.session_id||':context','raw_input',selected.raw_input,
    'raw_input_binding_id','cortex-session:'||selected.session_id||':raw-input','role',selected.role,
    'scout_enabled',selected.scout_enabled,'objective',selected.objective,'rationale',selected.rationale,
    'scout_memo',selected.scout_memo,
    'scout_memo_read',CASE WHEN selected.scout_memo IS NULL THEN NULL ELSE jsonb_set(selected.scout_memo,'{origin}',
      jsonb_build_object('owner','agent_control','record_type','artifact','record_id',selected.scout_artifact_id,
        'schema_revision',1,'record_digest',selected.scout_artifact_digest)) END,
    'scout_memo_binding_id',CASE WHEN selected.scout_memo IS NULL THEN NULL ELSE 'cortex-session:'||selected.session_id||':scout-memo' END,
    'scout_artifact_id',selected.scout_artifact_id,'scout_artifact_digest',selected.scout_artifact_digest
  );
END $$;

REVOKE ALL ON FUNCTION agent_control.next_cortex_task() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agent_control.next_cortex_task() TO alpheus_agent_worker;
RESET ROLE;
