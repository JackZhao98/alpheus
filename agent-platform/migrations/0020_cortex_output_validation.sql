SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

-- Control records the exact local-only validator, schema bytes and output bytes
-- that passed validation before a model output can become Worker-readable.
CREATE TABLE agent_control.runtime_cortex_output_validation (
    call_id TEXT PRIMARY KEY REFERENCES agent_control.runtime_model_call_manifest(call_id),
    manifest_digest CHAR(64) NOT NULL CHECK (agent_control.runtime_digest_valid(manifest_digest::TEXT)),
    output_blob_id UUID NOT NULL,
    output_content_digest CHAR(64) NOT NULL CHECK (agent_control.runtime_digest_valid(output_content_digest::TEXT)),
    schema_content_digest CHAR(64) NOT NULL CHECK (agent_control.runtime_digest_valid(schema_content_digest::TEXT)),
    profile TEXT NOT NULL CHECK (profile = 'alpheus-json-schema-2020-12-local-v1'),
    dialect TEXT NOT NULL CHECK (dialect = 'https://json-schema.org/draft/2020-12/schema'),
    validator_implementation TEXT NOT NULL CHECK (validator_implementation = 'github.com/santhosh-tekuri/jsonschema/v6'),
    validator_version TEXT NOT NULL CHECK (validator_version = 'v6.0.2'),
    record_digest CHAR(64) NOT NULL CHECK (agent_control.runtime_digest_valid(record_digest::TEXT)),
    validated_at TIMESTAMPTZ NOT NULL,
    UNIQUE (call_id, manifest_digest, record_digest),
    FOREIGN KEY (call_id, manifest_digest)
        REFERENCES agent_control.runtime_model_call_manifest(call_id, record_digest)
        DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (output_blob_id) REFERENCES blob.blob_object(blob_id)
        DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (output_content_digest) REFERENCES blob.blob_content(content_digest)
        DEFERRABLE INITIALLY DEFERRED
);

CREATE TRIGGER runtime_cortex_output_validation_immutable
BEFORE UPDATE OR DELETE ON agent_control.runtime_cortex_output_validation
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();

CREATE FUNCTION agent_control.publish_cortex_model_output_v3(
    p_call_id TEXT, p_manifest_digest TEXT, p_output JSONB,
    p_worker_principal TEXT, p_validation JSONB
) RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security,blob SET timezone='UTC' AS $$
DECLARE
    invoker RECORD;
    manifest agent_control.runtime_model_call_manifest%ROWTYPE;
    existing agent_control.runtime_cortex_output_validation%ROWTYPE;
    schema_digest TEXT;
    validation_digest TEXT;
    at_time TIMESTAMPTZ := clock_timestamp();
    publication JSONB;
BEGIN
    SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
    IF invoker.group_role::TEXT <> 'alpheus_agent_control_api'
       OR invoker.owner_id <> 'agent_control'
       OR NOT agent_control.runtime_blob_ref_valid(p_output,'model_call_manifest','')
       OR p_output#>>'{origin,record_id}' <> p_call_id
       OR p_output#>>'{origin,record_digest}' <> p_manifest_digest
       OR jsonb_typeof(p_validation) <> 'object'
       OR p_validation->>'profile' <> 'alpheus-json-schema-2020-12-local-v1'
       OR p_validation->>'dialect' <> 'https://json-schema.org/draft/2020-12/schema'
       OR p_validation->>'implementation' <> 'github.com/santhosh-tekuri/jsonschema/v6'
       OR p_validation->>'implementation_version' <> 'v6.0.2'
       OR NOT agent_control.runtime_digest_valid(p_validation->>'schema_sha256')
       OR NOT agent_control.runtime_digest_valid(p_validation->>'instance_sha256')
       OR p_validation->>'instance_sha256' <> p_output->>'content_digest' THEN
        RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='validated cortex model output publication denied';
    END IF;

    SELECT * INTO STRICT manifest
      FROM agent_control.runtime_model_call_manifest
     WHERE call_id=p_call_id AND record_digest::TEXT=p_manifest_digest
     FOR SHARE;
    SELECT schema_blob_content_digest::TEXT INTO STRICT schema_digest
      FROM agent_control.output_contract_revision
     WHERE record_digest::TEXT=manifest.output_contract_digest::TEXT
     ORDER BY generation DESC LIMIT 1;
    IF schema_digest <> p_validation->>'schema_sha256' THEN
        RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='cortex output schema evidence mismatch';
    END IF;

    SELECT * INTO existing FROM agent_control.runtime_cortex_output_validation WHERE call_id=p_call_id;
    IF FOUND THEN
        IF existing.manifest_digest::TEXT <> p_manifest_digest
           OR existing.output_blob_id::TEXT <> p_output->>'blob_id'
           OR existing.output_content_digest::TEXT <> p_output->>'content_digest'
           OR existing.schema_content_digest::TEXT <> schema_digest THEN
            RAISE EXCEPTION USING ERRCODE='23505',MESSAGE='cortex output validation identity conflict';
        END IF;
        RETURN jsonb_build_object('status','published','binding_id','cortex-model-output:'||p_call_id,
            'validation_digest',existing.record_digest);
    END IF;

    publication := agent_control.publish_cortex_model_output_v2(
        p_call_id,p_manifest_digest,p_output,p_worker_principal);
    validation_digest := agent_control.runtime_contract_digest(
        'agent-platform.contract.cortex_output_validation.v1',
        jsonb_build_object('call_id',p_call_id,'manifest_digest',p_manifest_digest,
            'output_blob_id',p_output->>'blob_id','output_content_digest',p_output->>'content_digest',
            'schema_content_digest',schema_digest,'profile',p_validation->>'profile',
            'dialect',p_validation->>'dialect','implementation',p_validation->>'implementation',
            'implementation_version',p_validation->>'implementation_version','validated_at',at_time));
    INSERT INTO agent_control.runtime_cortex_output_validation(
        call_id,manifest_digest,output_blob_id,output_content_digest,schema_content_digest,
        profile,dialect,validator_implementation,validator_version,record_digest,validated_at)
    VALUES (p_call_id,p_manifest_digest,(p_output->>'blob_id')::UUID,p_output->>'content_digest',schema_digest,
        p_validation->>'profile',p_validation->>'dialect',p_validation->>'implementation',
        p_validation->>'implementation_version',validation_digest,at_time);
    RETURN publication || jsonb_build_object('validation_digest',validation_digest);
END $$;

REVOKE ALL ON TABLE agent_control.runtime_cortex_output_validation FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.publish_cortex_model_output_v3(TEXT,TEXT,JSONB,TEXT,JSONB) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agent_control.publish_cortex_model_output_v3(TEXT,TEXT,JSONB,TEXT,JSONB) TO alpheus_agent_control_api;

RESET ROLE;
