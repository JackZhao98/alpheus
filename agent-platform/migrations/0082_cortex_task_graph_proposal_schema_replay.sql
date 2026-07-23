-- Schema Blobs are immutable content objects, but a restarted Control process
-- may stage the same bytes under a new Blob ID. Replay identity is therefore
-- the exact content plus immutable schema-origin digest, not allocation ID.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

CREATE OR REPLACE FUNCTION
agent_control.ensure_cortex_task_graph_proposal_output_contract_v1(
  p_schema JSONB
)
RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security,blob
SET timezone='UTC' AS $$
DECLARE
  invoker RECORD;
  existing agent_control.output_contract_revision%ROWTYPE;
  at_time TIMESTAMPTZ:=clock_timestamp();
  body JSONB;
  contract_digest CHAR(64);
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api'
     OR invoker.profile_id<>'control-api'
     OR invoker.owner_id<>'agent_control'
     OR NOT agent_control.runtime_blob_ref_valid(
       p_schema,'output_contract_schema','')
     OR p_schema->>'media_type'<>'application/json'
     OR NOT EXISTS (
       SELECT 1
       FROM blob.blob_object AS object
       WHERE object.blob_id=(p_schema->>'blob_id')::UUID
         AND object.state='committed'
         AND object.content_digest=p_schema->>'content_digest'
         AND object.origin_owner='agent_control'
         AND object.origin_record_type='output_contract_schema'
         AND object.origin_record_id=p_schema#>>'{origin,record_id}'
         AND object.origin_record_digest=p_schema#>>'{origin,record_digest}'
     ) THEN
    RAISE EXCEPTION USING ERRCODE='42501',
      MESSAGE='cortex TaskGraph proposal output contract denied';
  END IF;

  SELECT * INTO existing
  FROM agent_control.output_contract_revision
  WHERE revision_id='cortex-task-graph-proposal-output-v1'
    AND generation=1;
  IF FOUND THEN
    IF existing.schema_blob_content_digest::TEXT<>
         p_schema->>'content_digest'
       OR existing.schema_origin_record_id<>
         p_schema#>>'{origin,record_id}'
       OR existing.schema_origin_record_digest::TEXT<>
         p_schema#>>'{origin,record_digest}' THEN
      RAISE EXCEPTION USING ERRCODE='23505',
        MESSAGE='cortex TaskGraph proposal output contract identity conflict';
    END IF;
    RETURN jsonb_build_object(
      'status','ready',
      'output_contract_digest',existing.record_digest::TEXT);
  END IF;

  body:=jsonb_build_object(
    'schema_revision',1,
    'revision_id','cortex-task-graph-proposal-output-v1',
    'generation',1,
    'artifact_type','task_graph_proposal',
    'schema',p_schema,
    'effect_class','none',
    'author',jsonb_build_object(
      'principal_id',invoker.principal_id,
      'kind','workload',
      'audience','control_api'),
    'reason_code','cortex_task_graph_proposal_output',
    'created_at',agent_control.runtime_utc_text(at_time));
  contract_digest:=agent_control.runtime_contract_digest(
    'agent-platform.contract.output_contract_revision.v1',body);
  INSERT INTO agent_control.output_contract_revision(
    revision_id,schema_revision,generation,record_digest,artifact_type,
    schema_blob_schema_revision,schema_blob_id,schema_blob_content_digest,
    schema_blob_media_type,schema_blob_size_bytes,schema_origin_owner,
    schema_origin_record_type,schema_origin_record_id,
    schema_origin_schema_revision,schema_origin_record_digest,
    schema_blob_committed_at,effect_class,author_principal_id,author_kind,
    author_audience,reason_code,created_at
  ) VALUES(
    'cortex-task-graph-proposal-output-v1',1,1,contract_digest,
    'task_graph_proposal',1,(p_schema->>'blob_id')::UUID,
    p_schema->>'content_digest',p_schema->>'media_type',
    (p_schema->>'size_bytes')::BIGINT,'agent_control',
    'output_contract_schema',p_schema#>>'{origin,record_id}',1,
    p_schema#>>'{origin,record_digest}',
    (p_schema->>'committed_at')::TIMESTAMPTZ,'none',
    invoker.principal_id,'workload','control_api',
    'cortex_task_graph_proposal_output',at_time);
  RETURN jsonb_build_object(
    'status','ready','output_contract_digest',contract_digest);
END
$$;

REVOKE ALL ON FUNCTION
  agent_control.ensure_cortex_task_graph_proposal_output_contract_v1(JSONB)
  FROM PUBLIC;
GRANT EXECUTE ON FUNCTION
  agent_control.ensure_cortex_task_graph_proposal_output_contract_v1(JSONB)
  TO alpheus_agent_control_api;

RESET ROLE;
