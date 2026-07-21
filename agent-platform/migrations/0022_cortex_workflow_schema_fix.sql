SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

-- OpenAI strict response schemas do not accept a root oneOf. Keep one closed
-- object shape and enforce the target/kind relationship again in Worker and
-- Control. The old v1 contract remains immutable for historical Runs.
CREATE FUNCTION agent_control.ensure_cortex_workflow_output_contract_v2(p_schema JSONB)
RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security,blob SET timezone='UTC' AS $$
DECLARE invoker RECORD; at_time TIMESTAMPTZ:=clock_timestamp(); body JSONB; contract_digest CHAR(64);
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api' OR invoker.profile_id<>'control-api' OR invoker.owner_id<>'agent_control'
     OR NOT agent_control.runtime_blob_ref_valid(p_schema,'output_contract_schema','') OR p_schema->>'media_type'<>'application/json'
     OR NOT EXISTS (SELECT 1 FROM blob.blob_object object WHERE object.blob_id=(p_schema->>'blob_id')::UUID AND object.state='committed'
       AND object.content_digest=p_schema->>'content_digest' AND object.origin_owner='agent_control' AND object.origin_record_type='output_contract_schema'
       AND object.origin_record_id=p_schema#>>'{origin,record_id}' AND object.origin_record_digest=p_schema#>>'{origin,record_digest}') THEN
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='cortex workflow output contract denied';
  END IF;
  body:=jsonb_build_object('schema_revision',1,'revision_id','cortex-workflow-output-v2','generation',1,'artifact_type','assistant_response','schema',p_schema,
    'effect_class','none','author',jsonb_build_object('principal_id',invoker.principal_id,'kind','workload','audience','control_api'),
    'reason_code','cortex_workflow_output','created_at',agent_control.runtime_utc_text(at_time));
  contract_digest:=agent_control.runtime_contract_digest('agent-platform.contract.output_contract_revision.v1',body);
  INSERT INTO agent_control.output_contract_revision(revision_id,schema_revision,generation,record_digest,artifact_type,schema_blob_schema_revision,schema_blob_id,
    schema_blob_content_digest,schema_blob_media_type,schema_blob_size_bytes,schema_origin_owner,schema_origin_record_type,schema_origin_record_id,schema_origin_schema_revision,
    schema_origin_record_digest,schema_blob_committed_at,effect_class,author_principal_id,author_kind,author_audience,reason_code,created_at)
  VALUES('cortex-workflow-output-v2',1,1,contract_digest,'assistant_response',1,(p_schema->>'blob_id')::UUID,p_schema->>'content_digest',p_schema->>'media_type',
    (p_schema->>'size_bytes')::BIGINT,'agent_control','output_contract_schema',p_schema#>>'{origin,record_id}',1,p_schema#>>'{origin,record_digest}',
    (p_schema->>'committed_at')::TIMESTAMPTZ,'none',invoker.principal_id,'workload','control_api','cortex_workflow_output',at_time)
  ON CONFLICT(revision_id) DO NOTHING;
  IF NOT EXISTS (SELECT 1 FROM agent_control.output_contract_revision WHERE revision_id='cortex-workflow-output-v2' AND schema_blob_id=(p_schema->>'blob_id')::UUID) THEN
    RAISE EXCEPTION USING ERRCODE='23505',MESSAGE='cortex workflow output contract identity conflict';
  END IF;
  SELECT record_digest INTO contract_digest FROM agent_control.output_contract_revision WHERE revision_id='cortex-workflow-output-v2';
  RETURN jsonb_build_object('status','ready','output_contract_digest',contract_digest);
END $$;

DO $$
DECLARE definition TEXT;
BEGIN
  SELECT pg_get_functiondef('agent_control.admit_cortex_user_request_run_v2(jsonb)'::regprocedure) INTO definition;
  IF position('cortex-workflow-output-v1' IN definition)=0 THEN
    RAISE EXCEPTION 'expected Cortex workflow admission v1 definition';
  END IF;
  EXECUTE replace(definition,'cortex-workflow-output-v1','cortex-workflow-output-v2');
END $$;

REVOKE ALL ON FUNCTION agent_control.ensure_cortex_workflow_output_contract_v2(JSONB) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agent_control.ensure_cortex_workflow_output_contract_v2(JSONB) TO alpheus_agent_control_api;
RESET ROLE;
