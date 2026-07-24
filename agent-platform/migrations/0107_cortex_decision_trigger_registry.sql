-- Durable, effect=none mathematical wake conditions for the Agent Console.
-- The typed condition is Control-owned; every revision is also bound to the
-- frozen AP1 TriggerRegistration, RuntimePolicy, and an independently
-- activated external-event OwnerPolicy.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

CREATE FUNCTION platform_governance.activate_cortex_external_event_policy()
RETURNS JSONB
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path=pg_catalog,platform_governance,platform_security,agent_control
SET timezone='UTC'
AS $$
DECLARE
    invoker RECORD;
    at_time TIMESTAMPTZ:=clock_timestamp();
    policy_body JSONB;
    policy_digest CHAR(64);
BEGIN
    SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
    IF invoker.group_role::TEXT<>'alpheus_agent_activator'
       OR invoker.profile_id<>'activator'
       OR invoker.owner_id<>'platform_governance' THEN
        RAISE EXCEPTION USING ERRCODE='42501',
            MESSAGE='Cortex external-event policy activation denied';
    END IF;
    SELECT record_digest INTO policy_digest
    FROM platform_governance.owner_policy_revision
    WHERE revision_id='cortex-external-event-v1';
    IF NOT FOUND THEN
        policy_body:=jsonb_build_object(
            'schema_revision',1,
            'policy_id','cortex-external-event',
            'revision_id','cortex-external-event-v1',
            'generation',1,
            'origin_kind','external_event',
            'source_owner','agent_control',
            'source_record_type','external_event',
            'initiating_kind','workload',
            'initiating_audience','control_api',
            'effect_ceiling','none',
            'author',jsonb_build_object(
                'principal_id',invoker.principal_id,
                'kind','workload','audience','activator'
            ),
            'reason_code','cortex_external_event_enabled',
            'created_at',agent_control.runtime_utc_text(at_time)
        );
        policy_digest:=agent_control.runtime_contract_digest(
            'agent-platform.contract.owner_policy_revision.v1',policy_body
        );
        INSERT INTO platform_governance.owner_policy_revision(
            revision_id,schema_revision,policy_id,generation,record_digest,
            origin_kind,source_owner,source_record_type,initiating_kind,
            initiating_audience,initiating_principal_id,effect_ceiling,
            author_principal_id,author_kind,author_audience,reason_code,created_at
        ) VALUES(
            'cortex-external-event-v1',1,'cortex-external-event',1,
            policy_digest,'external_event','agent_control','external_event',
            'workload','control_api',NULL,'none',invoker.principal_id,
            'workload','activator','cortex_external_event_enabled',at_time
        );
        INSERT INTO platform_governance.owner_policy_head(
            head_id,schema_revision,generation,revision_id,revision_digest,
            activated_by_principal_id,activated_by_kind,
            activated_by_audience,activated_at
        ) VALUES(
            'cortex-external-event',1,1,'cortex-external-event-v1',
            policy_digest,invoker.principal_id,'workload','activator',at_time
        );
        INSERT INTO platform_governance.owner_policy_event(
            event_id,schema_revision,policy_id,generation,current_revision_id,
            current_revision_digest,actor_principal_id,actor_kind,
            actor_audience,reason_code,occurred_at
        ) VALUES(
            gen_random_uuid()::TEXT,1,'cortex-external-event',1,
            'cortex-external-event-v1',policy_digest,invoker.principal_id,
            'workload','activator','cortex_external_event_enabled',at_time
        );
    END IF;
    IF NOT EXISTS(
        SELECT 1 FROM platform_governance.owner_policy_head
        WHERE head_id='cortex-external-event' AND generation=1
          AND revision_id='cortex-external-event-v1'
          AND revision_digest=policy_digest
    ) THEN
        RAISE EXCEPTION USING ERRCODE='23505',
            MESSAGE='Cortex external-event policy identity conflict';
    END IF;
    RETURN jsonb_build_object(
        'status','active','policy_id','cortex-external-event',
        'revision_id','cortex-external-event-v1',
        'record_digest',policy_digest
    );
END
$$;

CREATE TABLE agent_control.cortex_decision_trigger_revision(
    trigger_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(trigger_id)
    ),
    schema_revision SMALLINT NOT NULL CHECK(schema_revision=1),
    generation BIGINT NOT NULL CHECK(generation>0),
    record_digest CHAR(64) NOT NULL UNIQUE CHECK(
        agent_control.runtime_digest_valid(record_digest::TEXT)
    ),
    registration_digest CHAR(64) NOT NULL CHECK(
        agent_control.runtime_digest_valid(registration_digest::TEXT)
    ),
    subject_principal_id TEXT NOT NULL CHECK(
        agent_control.runtime_identifier_valid(subject_principal_id)
    ),
    title TEXT NOT NULL CHECK(
        title=btrim(title) AND title<>'' AND octet_length(title)<=160
        AND title!~'[[:cntrl:]]'
    ),
    strategy_id TEXT NOT NULL CHECK(
        strategy_id~'^[a-z][a-z0-9_]{0,63}$'
    ),
    data_source TEXT NOT NULL CHECK(
        data_source IN ('kernel_quote','research_gexbot')
    ),
    symbol TEXT NOT NULL CHECK(
        symbol~'^[A-Z][A-Z0-9._^-]{0,15}$'
    ),
    metric TEXT NOT NULL CHECK(metric IN(
        'mid_price','bid_price','ask_price',
        'gex_call_wall','gex_put_wall','gex_zero_gamma'
    )),
    comparator TEXT NOT NULL CHECK(comparator IN(
        'gte','lte','crosses_above','crosses_below'
    )),
    threshold NUMERIC(24,8) NOT NULL CHECK(
        threshold BETWEEN -1000000000 AND 1000000000
    ),
    cooldown_seconds INTEGER NOT NULL CHECK(
        cooldown_seconds BETWEEN 5 AND 86400
    ),
    objective TEXT NOT NULL CHECK(
        objective=btrim(objective) AND objective<>''
        AND octet_length(objective)<=4000
    ),
    enabled BOOLEAN NOT NULL,
    created_by_principal_id TEXT NOT NULL CHECK(
        agent_control.runtime_identifier_valid(created_by_principal_id)
    ),
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY(trigger_id,generation),
    UNIQUE(trigger_id,generation,record_digest),
    FOREIGN KEY(trigger_id,generation,registration_digest)
        REFERENCES agent_control.trigger_registration_revision(
            registration_id,generation,record_digest
        )
);

CREATE TABLE agent_control.cortex_decision_trigger_head(
    trigger_id TEXT PRIMARY KEY,
    generation BIGINT NOT NULL,
    record_digest CHAR(64) NOT NULL,
    selected_at TIMESTAMPTZ NOT NULL,
    FOREIGN KEY(trigger_id,generation,record_digest)
        REFERENCES agent_control.cortex_decision_trigger_revision(
            trigger_id,generation,record_digest
        )
);

CREATE TABLE agent_control.cortex_decision_trigger_event(
    event_id TEXT PRIMARY KEY CHECK(
        agent_control.runtime_identifier_valid(event_id)
    ),
    trigger_id TEXT NOT NULL,
    generation BIGINT NOT NULL,
    previous_generation BIGINT,
    previous_record_digest CHAR(64),
    current_record_digest CHAR(64) NOT NULL,
    reason_code TEXT NOT NULL CHECK(
        reason_code~'^[a-z][a-z0-9_]{0,63}$'
    ),
    occurred_at TIMESTAMPTZ NOT NULL,
    UNIQUE(trigger_id,generation),
    FOREIGN KEY(trigger_id,generation,current_record_digest)
        REFERENCES agent_control.cortex_decision_trigger_revision(
            trigger_id,generation,record_digest
        ),
    FOREIGN KEY(trigger_id,previous_generation,previous_record_digest)
        REFERENCES agent_control.cortex_decision_trigger_revision(
            trigger_id,generation,record_digest
        ),
    CHECK(
        (generation=1 AND previous_generation IS NULL
            AND previous_record_digest IS NULL)
        OR
        (generation>1 AND previous_generation=generation-1
            AND previous_record_digest IS NOT NULL)
    )
);

CREATE TRIGGER cortex_decision_trigger_revision_immutable
BEFORE UPDATE OR DELETE ON agent_control.cortex_decision_trigger_revision
FOR EACH ROW EXECUTE FUNCTION
agent_control.reject_immutable_runtime_definition_mutation();

CREATE TRIGGER cortex_decision_trigger_event_immutable
BEFORE UPDATE OR DELETE ON agent_control.cortex_decision_trigger_event
FOR EACH ROW EXECUTE FUNCTION
agent_control.reject_immutable_runtime_definition_mutation();

CREATE FUNCTION agent_control.cortex_decision_trigger_json(
    p_trigger agent_control.cortex_decision_trigger_revision
) RETURNS JSONB
LANGUAGE sql
STABLE
SET search_path=pg_catalog,agent_control
SET timezone='UTC'
AS $$
    SELECT jsonb_build_object(
        'trigger_id',p_trigger.trigger_id,
        'generation',p_trigger.generation,
        'title',p_trigger.title,
        'strategy_id',p_trigger.strategy_id,
        'data_source',p_trigger.data_source,
        'symbol',p_trigger.symbol,
        'metric',p_trigger.metric,
        'comparator',p_trigger.comparator,
        'threshold',p_trigger.threshold,
        'cooldown_seconds',p_trigger.cooldown_seconds,
        'objective',p_trigger.objective,
        'enabled',p_trigger.enabled,
        'state',CASE WHEN p_trigger.enabled THEN 'armed' ELSE 'paused' END,
        'updated_at',agent_control.runtime_utc_text(p_trigger.created_at)
    )
$$;

CREATE FUNCTION agent_control.register_cortex_decision_trigger(
    p_subject_principal_id TEXT,
    p_command JSONB
) RETURNS JSONB
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_governance,platform_security
SET timezone='UTC'
AS $$
DECLARE
    invoker RECORD;
    at_time TIMESTAMPTZ:=clock_timestamp();
    current_head agent_control.cortex_decision_trigger_head%ROWTYPE;
    previous_generation BIGINT;
    previous_digest CHAR(64);
    next_generation BIGINT;
    owner_policy platform_governance.owner_policy_revision%ROWTYPE;
    runtime_policy agent_control.runtime_policy_revision%ROWTYPE;
    registration_body JSONB;
    registration_digest CHAR(64);
    trigger_body JSONB;
    trigger_digest CHAR(64);
    trigger_row agent_control.cortex_decision_trigger_revision%ROWTYPE;
    trigger_id_value TEXT:=p_command->>'trigger_id';
    expected_generation BIGINT;
BEGIN
    SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
    IF invoker.group_role::TEXT<>'alpheus_agent_control_api'
       OR invoker.profile_id<>'control-api'
       OR invoker.owner_id<>'agent_control'
       OR jsonb_typeof(p_command)<>'object'
       OR p_command-ARRAY[
            'trigger_id','expected_generation','title','strategy_id',
            'data_source','symbol','metric','comparator','threshold',
            'cooldown_seconds','objective','enabled'
       ]<>'{}'::JSONB
       OR NOT agent_control.runtime_identifier_valid(p_subject_principal_id)
       OR NOT agent_control.runtime_identifier_valid(trigger_id_value) THEN
        RAISE EXCEPTION USING ERRCODE='42501',
            MESSAGE='Cortex decision Trigger registration denied';
    END IF;
    BEGIN
        expected_generation:=(p_command->>'expected_generation')::BIGINT;
    EXCEPTION WHEN OTHERS THEN
        RAISE EXCEPTION USING ERRCODE='22023',
            MESSAGE='invalid expected Trigger generation';
    END;
    IF expected_generation<0
       OR coalesce(p_command->>'title','')<>btrim(coalesce(p_command->>'title',''))
       OR coalesce(p_command->>'title','')=''
       OR octet_length(p_command->>'title')>160
       OR (p_command->>'title')~'[[:cntrl:]]'
       OR coalesce(p_command->>'strategy_id','')!~'^[a-z][a-z0-9_]{0,63}$'
       OR coalesce(p_command->>'data_source','') NOT IN(
            'kernel_quote','research_gexbot'
       )
       OR coalesce(p_command->>'symbol','')!~'^[A-Z][A-Z0-9._^-]{0,15}$'
       OR coalesce(p_command->>'metric','') NOT IN(
            'mid_price','bid_price','ask_price',
            'gex_call_wall','gex_put_wall','gex_zero_gamma'
       )
       OR (
            p_command->>'data_source'='kernel_quote'
            AND p_command->>'metric' NOT IN(
                'mid_price','bid_price','ask_price'
            )
       )
       OR (
            p_command->>'data_source'='research_gexbot'
            AND p_command->>'metric' NOT IN(
                'gex_call_wall','gex_put_wall','gex_zero_gamma'
            )
       )
       OR coalesce(p_command->>'comparator','') NOT IN(
            'gte','lte','crosses_above','crosses_below'
       )
       OR coalesce(p_command->>'objective','')<>btrim(coalesce(p_command->>'objective',''))
       OR coalesce(p_command->>'objective','')=''
       OR octet_length(p_command->>'objective')>4000
       OR jsonb_typeof(p_command->'threshold')<>'number'
       OR (p_command->>'threshold')::NUMERIC NOT BETWEEN -1000000000 AND 1000000000
       OR jsonb_typeof(p_command->'cooldown_seconds')<>'number'
       OR (p_command->>'cooldown_seconds')::INTEGER NOT BETWEEN 5 AND 86400
       OR jsonb_typeof(p_command->'enabled')<>'boolean' THEN
        RAISE EXCEPTION USING ERRCODE='22023',
            MESSAGE='invalid Cortex decision Trigger';
    END IF;

    SELECT * INTO current_head
    FROM agent_control.cortex_decision_trigger_head
    WHERE trigger_id=trigger_id_value
    FOR UPDATE;
    IF FOUND THEN
        IF current_head.generation<>expected_generation THEN
            SELECT * INTO trigger_row
            FROM agent_control.cortex_decision_trigger_revision
            WHERE trigger_id=current_head.trigger_id
              AND generation=current_head.generation
              AND record_digest=current_head.record_digest;
            RETURN jsonb_build_object(
                'status','conflict',
                'reason_code','trigger_generation_changed',
                'trigger',agent_control.cortex_decision_trigger_json(trigger_row)
            );
        END IF;
        previous_generation:=current_head.generation;
        previous_digest:=current_head.record_digest;
        next_generation:=current_head.generation+1;
    ELSE
        IF expected_generation<>0 THEN
            RETURN jsonb_build_object(
                'status','conflict',
                'reason_code','trigger_generation_changed'
            );
        END IF;
        next_generation:=1;
    END IF;

    SELECT revision.* INTO STRICT owner_policy
    FROM platform_governance.owner_policy_head AS head
    JOIN platform_governance.owner_policy_revision AS revision
      ON revision.policy_id=head.head_id
     AND revision.generation=head.generation
     AND revision.revision_id=head.revision_id
     AND revision.record_digest=head.revision_digest
    WHERE head.head_id='cortex-external-event'
      AND revision.origin_kind='external_event'
      AND revision.effect_ceiling='none';

    SELECT revision.* INTO STRICT runtime_policy
    FROM agent_control.runtime_policy_head AS head
    JOIN agent_control.runtime_policy_revision AS revision
      ON revision.policy_id=head.policy_id
     AND revision.generation=head.generation
     AND revision.record_digest=head.record_digest
    WHERE head.policy_id='cortex-mvp';

    registration_body:=jsonb_build_object(
        'schema_revision',1,
        'registration_id',trigger_id_value,
        'generation',next_generation,
        'kind','external_event',
        'source_key',trigger_id_value,
        'owner_policy',jsonb_build_object(
            'owner','platform_governance',
            'record_type','owner_policy_revision',
            'record_id',owner_policy.revision_id,
            'schema_revision',1,
            'record_digest',owner_policy.record_digest,
            'generation',owner_policy.generation
        ),
        'runtime_policy',jsonb_build_object(
            'owner','agent_control',
            'record_type','runtime_policy',
            'record_id',runtime_policy.policy_id,
            'schema_revision',1,
            'record_digest',runtime_policy.record_digest,
            'generation',runtime_policy.generation
        ),
        'enabled',(p_command->>'enabled')::BOOLEAN,
        'updated_by',jsonb_build_object(
            'principal_id',invoker.principal_id,
            'kind','workload','audience','control_api'
        ),
        'updated_at',agent_control.runtime_utc_text(at_time)
    );
    registration_digest:=agent_control.runtime_contract_digest(
        'agent-platform.contract.trigger_registration.v1',
        registration_body
    );
    trigger_body:=jsonb_build_object(
        'schema_revision',1,
        'trigger_id',trigger_id_value,
        'generation',next_generation,
        'subject_principal_id',p_subject_principal_id,
        'title',p_command->>'title',
        'strategy_id',p_command->>'strategy_id',
        'data_source',p_command->>'data_source',
        'symbol',p_command->>'symbol',
        'metric',p_command->>'metric',
        'comparator',p_command->>'comparator',
        'threshold',(p_command->>'threshold')::NUMERIC,
        'cooldown_seconds',(p_command->>'cooldown_seconds')::INTEGER,
        'objective',p_command->>'objective',
        'enabled',(p_command->>'enabled')::BOOLEAN,
        'created_by_principal_id',invoker.principal_id,
        'created_at',agent_control.runtime_utc_text(at_time)
    );
    trigger_digest:=agent_control.runtime_contract_digest(
        'alpheus.cortex.decision_trigger.v1',trigger_body
    );

    INSERT INTO agent_control.trigger_registration_revision(
        registration_id,schema_revision,generation,record_digest,kind,
        source_key,owner_policy_owner,owner_policy_record_type,
        owner_policy_record_id,owner_policy_schema_revision,
        owner_policy_record_digest,owner_policy_generation,
        runtime_policy_owner,runtime_policy_record_type,
        runtime_policy_record_id,runtime_policy_schema_revision,
        runtime_policy_record_digest,runtime_policy_generation,enabled,
        updated_by_principal_id,updated_by_kind,updated_by_audience,updated_at
    ) VALUES(
        trigger_id_value,1,next_generation,registration_digest,
        'external_event',trigger_id_value,'platform_governance',
        'owner_policy_revision',owner_policy.revision_id,1,
        owner_policy.record_digest,owner_policy.generation,'agent_control',
        'runtime_policy',runtime_policy.policy_id,1,runtime_policy.record_digest,
        runtime_policy.generation,(p_command->>'enabled')::BOOLEAN,
        invoker.principal_id,'workload','control_api',at_time
    );
    INSERT INTO agent_control.trigger_registration_event(
        event_id,registration_id,generation,previous_generation,
        previous_record_digest,current_record_digest,actor_principal_id,
        actor_kind,actor_audience,reason_code,occurred_at
    ) VALUES(
        gen_random_uuid()::TEXT,trigger_id_value,next_generation,
        previous_generation,
        CASE WHEN previous_generation IS NULL THEN NULL ELSE (
            SELECT registration_digest
            FROM agent_control.cortex_decision_trigger_revision
            WHERE trigger_id=trigger_id_value
              AND generation=previous_generation
        ) END,
        registration_digest,invoker.principal_id,'workload','control_api',
        CASE WHEN next_generation=1 THEN 'decision_trigger_created'
             ELSE 'decision_trigger_revised' END,at_time
    );
    INSERT INTO agent_control.trigger_registration_head(
        registration_id,generation,record_digest,selected_by_principal_id,
        selected_by_kind,selected_by_audience,selected_at
    ) VALUES(
        trigger_id_value,next_generation,registration_digest,
        invoker.principal_id,'workload','control_api',at_time
    )
    ON CONFLICT(registration_id) DO UPDATE SET
        generation=excluded.generation,
        record_digest=excluded.record_digest,
        selected_by_principal_id=excluded.selected_by_principal_id,
        selected_by_kind=excluded.selected_by_kind,
        selected_by_audience=excluded.selected_by_audience,
        selected_at=excluded.selected_at;

    INSERT INTO agent_control.cortex_decision_trigger_revision(
        trigger_id,schema_revision,generation,record_digest,
        registration_digest,subject_principal_id,title,strategy_id,
        data_source,symbol,metric,comparator,threshold,cooldown_seconds,
        objective,enabled,created_by_principal_id,created_at
    ) VALUES(
        trigger_id_value,1,next_generation,trigger_digest,
        registration_digest,p_subject_principal_id,p_command->>'title',
        p_command->>'strategy_id',p_command->>'data_source',
        p_command->>'symbol',p_command->>'metric',p_command->>'comparator',
        (p_command->>'threshold')::NUMERIC,
        (p_command->>'cooldown_seconds')::INTEGER,p_command->>'objective',
        (p_command->>'enabled')::BOOLEAN,invoker.principal_id,at_time
    ) RETURNING * INTO trigger_row;
    INSERT INTO agent_control.cortex_decision_trigger_event(
        event_id,trigger_id,generation,previous_generation,
        previous_record_digest,current_record_digest,reason_code,occurred_at
    ) VALUES(
        gen_random_uuid()::TEXT,trigger_id_value,next_generation,
        previous_generation,previous_digest,trigger_digest,
        CASE WHEN next_generation=1 THEN 'decision_trigger_created'
             ELSE 'decision_trigger_revised' END,at_time
    );
    INSERT INTO agent_control.cortex_decision_trigger_head(
        trigger_id,generation,record_digest,selected_at
    ) VALUES(
        trigger_id_value,next_generation,trigger_digest,at_time
    )
    ON CONFLICT(trigger_id) DO UPDATE SET
        generation=excluded.generation,
        record_digest=excluded.record_digest,
        selected_at=excluded.selected_at;
    RETURN jsonb_build_object(
        'status','registered',
        'trigger',agent_control.cortex_decision_trigger_json(trigger_row)
    );
END
$$;

CREATE FUNCTION agent_control.list_cortex_decision_triggers(
    p_subject_principal_id TEXT,
    p_limit INTEGER
) RETURNS JSONB
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security
SET timezone='UTC'
AS $$
DECLARE
    invoker RECORD;
BEGIN
    SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
    IF invoker.group_role::TEXT<>'alpheus_agent_control_api'
       OR invoker.profile_id<>'control-api'
       OR invoker.owner_id<>'agent_control'
       OR NOT agent_control.runtime_identifier_valid(p_subject_principal_id)
       OR p_limit NOT BETWEEN 1 AND 100 THEN
        RAISE EXCEPTION USING ERRCODE='42501',
            MESSAGE='Cortex decision Trigger list denied';
    END IF;
    RETURN COALESCE((
        SELECT jsonb_agg(
            agent_control.cortex_decision_trigger_json(revision)
            ORDER BY head.selected_at DESC,revision.trigger_id
        )
        FROM agent_control.cortex_decision_trigger_head AS head
        JOIN agent_control.cortex_decision_trigger_revision AS revision
          ON revision.trigger_id=head.trigger_id
         AND revision.generation=head.generation
         AND revision.record_digest=head.record_digest
        WHERE revision.subject_principal_id=p_subject_principal_id
    ),'[]'::JSONB);
END
$$;

REVOKE ALL ON TABLE
agent_control.cortex_decision_trigger_revision,
agent_control.cortex_decision_trigger_head,
agent_control.cortex_decision_trigger_event
FROM PUBLIC;
REVOKE ALL ON FUNCTION
platform_governance.activate_cortex_external_event_policy(),
agent_control.cortex_decision_trigger_json(
    agent_control.cortex_decision_trigger_revision
),
agent_control.register_cortex_decision_trigger(TEXT,JSONB),
agent_control.list_cortex_decision_triggers(TEXT,INTEGER)
FROM PUBLIC;
GRANT EXECUTE ON FUNCTION
platform_governance.activate_cortex_external_event_policy()
TO alpheus_agent_activator;
GRANT EXECUTE ON FUNCTION
agent_control.register_cortex_decision_trigger(TEXT,JSONB),
agent_control.list_cortex_decision_triggers(TEXT,INTEGER)
TO alpheus_agent_control_api;

RESET ROLE;
