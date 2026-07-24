-- A fired deterministic sample becomes exactly one frozen AP1
-- TriggerOccurrence. Materialization is idempotent and carries the exact
-- TriggerRegistration, RuntimePolicy and effect=none OwnerPolicy bindings.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

CREATE TABLE agent_control.cortex_decision_trigger_occurrence(
    sample_id TEXT PRIMARY KEY REFERENCES
        agent_control.cortex_decision_trigger_sample(sample_id),
    trigger_id TEXT NOT NULL,
    trigger_generation BIGINT NOT NULL,
    trigger_digest CHAR(64) NOT NULL,
    occurrence_id TEXT NOT NULL,
    occurrence_digest CHAR(64) NOT NULL,
    source_record_digest CHAR(64) NOT NULL,
    response JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(occurrence_id,occurrence_digest),
    FOREIGN KEY(trigger_id,trigger_generation,trigger_digest)
        REFERENCES agent_control.cortex_decision_trigger_revision(
            trigger_id,generation,record_digest
        ),
    FOREIGN KEY(occurrence_id,occurrence_digest)
        REFERENCES agent_control.trigger_occurrence(
            occurrence_id,record_digest
        )
);

CREATE TRIGGER cortex_decision_trigger_occurrence_immutable
BEFORE UPDATE OR DELETE ON agent_control.cortex_decision_trigger_occurrence
FOR EACH ROW EXECUTE FUNCTION
agent_control.reject_immutable_runtime_definition_mutation();

CREATE FUNCTION agent_control.materialize_cortex_decision_trigger_occurrence(
    p_sample_id TEXT
) RETURNS JSONB
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_governance,platform_security
SET timezone='UTC'
AS $$
DECLARE
    invoker RECORD;
    at_time TIMESTAMPTZ:=clock_timestamp();
    sample agent_control.cortex_decision_trigger_sample%ROWTYPE;
    trigger_row agent_control.cortex_decision_trigger_revision%ROWTYPE;
    registration agent_control.trigger_registration_revision%ROWTYPE;
    owner_policy platform_governance.owner_policy_revision%ROWTYPE;
    existing agent_control.cortex_decision_trigger_occurrence%ROWTYPE;
    occurrence_id_value TEXT:=gen_random_uuid()::TEXT;
    source_body JSONB;
    source_digest CHAR(64);
    occurrence_body JSONB;
    occurrence_digest CHAR(64);
    response_value JSONB;
BEGIN
    SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
    IF invoker.group_role::TEXT<>'alpheus_agent_control_api'
       OR invoker.profile_id<>'control-api'
       OR invoker.owner_id<>'agent_control'
       OR NOT agent_control.runtime_identifier_valid(p_sample_id) THEN
        RAISE EXCEPTION USING ERRCODE='42501',
            MESSAGE='Cortex decision Trigger occurrence denied';
    END IF;
    SELECT * INTO existing
    FROM agent_control.cortex_decision_trigger_occurrence
    WHERE sample_id=p_sample_id;
    IF FOUND THEN
        RETURN existing.response;
    END IF;
    SELECT * INTO STRICT sample
    FROM agent_control.cortex_decision_trigger_sample
    WHERE sample_id=p_sample_id AND fired
    FOR SHARE;
    SELECT * INTO STRICT trigger_row
    FROM agent_control.cortex_decision_trigger_revision
    WHERE trigger_id=sample.trigger_id
      AND generation=sample.trigger_generation
      AND record_digest=sample.trigger_digest
    FOR SHARE;
    SELECT * INTO STRICT registration
    FROM agent_control.trigger_registration_revision
    WHERE registration_id=trigger_row.trigger_id
      AND generation=trigger_row.generation
      AND record_digest=trigger_row.registration_digest
      AND enabled AND kind='external_event'
    FOR SHARE;
    SELECT policy.* INTO STRICT owner_policy
    FROM platform_governance.owner_policy_head AS head
    JOIN platform_governance.owner_policy_revision AS policy
      ON policy.policy_id=head.head_id
     AND policy.generation=head.generation
     AND policy.revision_id=head.revision_id
     AND policy.record_digest=head.revision_digest
    WHERE policy.revision_id=registration.owner_policy_record_id
      AND policy.generation=registration.owner_policy_generation
      AND policy.record_digest=registration.owner_policy_record_digest
      AND policy.origin_kind='external_event'
      AND policy.source_owner='agent_control'
      AND policy.source_record_type='external_event'
      AND policy.initiating_kind='workload'
      AND policy.initiating_audience='control_api'
      AND policy.effect_ceiling='none'
    FOR SHARE OF head;

    source_body:=jsonb_build_object(
        'schema_revision',1,
        'event_id',sample.sample_id,
        'trigger_id',trigger_row.trigger_id,
        'trigger_generation',trigger_row.generation,
        'trigger_digest',trigger_row.record_digest,
        'value',sample.value,
        'prior_value',sample.prior_value,
        'reason_code',sample.reason_code,
        'occurred_at',agent_control.runtime_utc_text(sample.observed_at),
        'committed_at',agent_control.runtime_utc_text(sample.committed_at)
    );
    source_digest:=agent_control.runtime_contract_digest(
        'agent_control.cortex_decision_trigger_external_event.v1',
        source_body
    );
    occurrence_body:=jsonb_build_object(
        'schema_revision',1,
        'occurrence_id',occurrence_id_value,
        'registration',jsonb_build_object(
            'owner','agent_control',
            'record_type','trigger_registration',
            'record_id',registration.registration_id,
            'schema_revision',1,
            'generation',registration.generation,
            'record_digest',registration.record_digest
        ),
        'kind','external_event',
        'source',jsonb_build_object(
            'owner','agent_control',
            'record_type','external_event',
            'record_id',sample.sample_id,
            'schema_revision',1,
            'record_digest',source_digest
        ),
        'initiating_principal_id',invoker.principal_id,
        'initiating_kind','workload',
        'initiating_audience','control_api',
        'owner_policy',jsonb_build_object(
            'owner','platform_governance',
            'record_type','owner_policy_revision',
            'record_id',owner_policy.revision_id,
            'schema_revision',1,
            'generation',owner_policy.generation,
            'record_digest',owner_policy.record_digest
        ),
        'occurrence_key',sample.sample_id,
        'occurred_at',agent_control.runtime_utc_text(sample.observed_at),
        'observed_at',agent_control.runtime_utc_text(sample.committed_at),
        'committed_at',agent_control.runtime_utc_text(at_time)
    );
    occurrence_digest:=agent_control.runtime_contract_digest(
        'agent-platform.contract.trigger_occurrence.v1',occurrence_body
    );
    INSERT INTO agent_control.trigger_occurrence(
        occurrence_id,schema_revision,record_digest,
        registration_owner,registration_record_type,registration_id,
        registration_schema_revision,registration_generation,
        registration_digest,kind,source_owner,source_record_type,
        source_record_id,source_schema_revision,source_record_digest,
        initiating_principal_id,initiating_kind,initiating_audience,
        owner_policy_owner,owner_policy_record_type,owner_policy_record_id,
        owner_policy_schema_revision,owner_policy_record_digest,
        owner_policy_generation,occurrence_key,payload,occurred_at,
        observed_at,committed_at
    ) VALUES(
        occurrence_id_value,1,occurrence_digest,
        'agent_control','trigger_registration',registration.registration_id,
        1,registration.generation,registration.record_digest,
        'external_event','agent_control','external_event',sample.sample_id,
        1,source_digest,invoker.principal_id,'workload','control_api',
        'platform_governance','owner_policy_revision',
        owner_policy.revision_id,1,owner_policy.record_digest,
        owner_policy.generation,sample.sample_id,NULL,sample.observed_at,
        sample.committed_at,at_time
    );
    response_value:=jsonb_build_object(
        'status','materialized',
        'sample_id',sample.sample_id,
        'trigger_id',trigger_row.trigger_id,
        'occurrence_id',occurrence_id_value,
        'occurrence_digest',occurrence_digest,
        'source_record_digest',source_digest,
        'occurred_at',agent_control.runtime_utc_text(sample.observed_at)
    );
    INSERT INTO agent_control.cortex_decision_trigger_occurrence(
        sample_id,trigger_id,trigger_generation,trigger_digest,
        occurrence_id,occurrence_digest,source_record_digest,response,created_at
    ) VALUES(
        sample.sample_id,trigger_row.trigger_id,trigger_row.generation,
        trigger_row.record_digest,occurrence_id_value,occurrence_digest,
        source_digest,response_value,at_time
    );
    RETURN response_value;
END
$$;

REVOKE ALL ON TABLE
agent_control.cortex_decision_trigger_occurrence
FROM PUBLIC;
REVOKE ALL ON FUNCTION
agent_control.materialize_cortex_decision_trigger_occurrence(TEXT)
FROM PUBLIC;
GRANT EXECUTE ON FUNCTION
agent_control.materialize_cortex_decision_trigger_occurrence(TEXT)
TO alpheus_agent_control_api;

RESET ROLE;
