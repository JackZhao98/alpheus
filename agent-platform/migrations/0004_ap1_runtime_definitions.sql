SET ROLE alpheus_agent_migrator;

-- AP1 stores definitions before it enables any Runtime behavior. These rows
-- are inert data: no trigger claim, model call, Tool call, Provider call,
-- Kernel write, broker mutation, or publication can be caused by this
-- migration. Application command functions and their grants land separately.

CREATE FUNCTION agent_control.reject_immutable_runtime_definition_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION USING ERRCODE = '55000', MESSAGE = 'immutable runtime definition';
END
$$;

REVOKE ALL ON FUNCTION agent_control.reject_immutable_runtime_definition_mutation()
FROM PUBLIC;

-- OwnerPolicyRevision is the platform-owned, effect=none authority source for
-- one authenticated non-recovery Run origin. Recovery reuses the original
-- exact revision and therefore cannot register a system_recovery policy.
CREATE TABLE platform_governance.owner_policy_revision (
    revision_id TEXT PRIMARY KEY CHECK (
        revision_id <> '' AND revision_id = btrim(revision_id)
        AND octet_length(revision_id) <= 200
        AND revision_id !~ '[[:space:][:cntrl:]]'
    ),
    schema_revision SMALLINT NOT NULL CHECK (schema_revision = 1),
    policy_id TEXT NOT NULL CHECK (
        policy_id <> '' AND policy_id = btrim(policy_id)
        AND octet_length(policy_id) <= 200
        AND policy_id !~ '[[:space:][:cntrl:]]'
    ),
    generation BIGINT NOT NULL CHECK (generation > 0),
    record_digest CHAR(64) NOT NULL UNIQUE CHECK (record_digest ~ '^[0-9a-f]{64}$'),
    origin_kind TEXT NOT NULL CHECK (origin_kind IN (
        'user_request', 'schedule', 'kernel_event', 'external_event',
        'system_maintenance'
    )),
    source_owner TEXT NOT NULL CHECK (source_owner IN (
        'agent_control', 'blob', 'delegation', 'grace', 'kernel',
        'platform_governance', 'research_gateway', 'worker'
    )),
    source_record_type TEXT NOT NULL CHECK (
        source_record_type ~ '^[a-z][a-z0-9_]{0,63}$'
    ),
    initiating_kind TEXT NOT NULL CHECK (initiating_kind IN ('kernel', 'user', 'workload')),
    initiating_audience TEXT NOT NULL CHECK (initiating_audience IN (
        'activator', 'control_api', 'delegation_engine', 'grace_engine',
        'grace_intake', 'kernel', 'kernel_admin', 'research_gateway',
        'validator', 'worker'
    )),
    initiating_principal_id TEXT CHECK (
        initiating_principal_id IS NULL OR (
            initiating_principal_id <> ''
            AND initiating_principal_id = btrim(initiating_principal_id)
            AND octet_length(initiating_principal_id) <= 200
            AND initiating_principal_id !~ '[[:space:][:cntrl:]]'
        )
    ),
    effect_ceiling TEXT NOT NULL CHECK (effect_ceiling = 'none'),
    author_principal_id TEXT NOT NULL CHECK (
        author_principal_id <> '' AND author_principal_id = btrim(author_principal_id)
        AND octet_length(author_principal_id) <= 200
        AND author_principal_id !~ '[[:space:][:cntrl:]]'
    ),
    author_kind TEXT NOT NULL CHECK (author_kind IN ('user', 'workload')),
    author_audience TEXT NOT NULL CHECK (author_audience = 'activator'),
    reason_code TEXT NOT NULL CHECK (reason_code ~ '^[a-z][a-z0-9_]{0,63}$'),
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (policy_id, generation),
    UNIQUE (policy_id, generation, revision_id, record_digest),
    UNIQUE (revision_id, generation, record_digest, origin_kind),
    CHECK (
        (origin_kind = 'user_request'
            AND source_owner = 'agent_control'
            AND source_record_type = 'user_request'
            AND initiating_kind = 'user'
            AND initiating_audience = 'control_api')
        OR
        (origin_kind = 'schedule'
            AND source_owner = 'agent_control'
            AND source_record_type = 'schedule_occurrence'
            AND initiating_kind = 'workload'
            AND initiating_audience = 'control_api')
        OR
        (origin_kind = 'kernel_event'
            AND source_owner = 'kernel'
            AND source_record_type = 'kernel_event'
            AND initiating_kind = 'kernel'
            AND initiating_audience = 'kernel')
        OR
        (origin_kind = 'external_event'
            AND source_owner = 'agent_control'
            AND source_record_type = 'external_event'
            AND initiating_kind = 'workload'
            AND initiating_audience = 'control_api')
        OR
        (origin_kind = 'system_maintenance'
            AND source_owner = 'agent_control'
            AND source_record_type = 'maintenance_occurrence'
            AND initiating_kind = 'workload'
            AND initiating_audience = 'control_api')
    )
);

CREATE TABLE platform_governance.owner_policy_head (
    head_id TEXT PRIMARY KEY CHECK (
        head_id <> '' AND head_id = btrim(head_id) AND octet_length(head_id) <= 200
        AND head_id !~ '[[:space:][:cntrl:]]'
    ),
    schema_revision SMALLINT NOT NULL CHECK (schema_revision = 1),
    generation BIGINT NOT NULL CHECK (generation > 0),
    revision_id TEXT NOT NULL,
    revision_digest CHAR(64) NOT NULL CHECK (revision_digest ~ '^[0-9a-f]{64}$'),
    activated_by_principal_id TEXT NOT NULL CHECK (
        activated_by_principal_id <> ''
        AND activated_by_principal_id = btrim(activated_by_principal_id)
        AND octet_length(activated_by_principal_id) <= 200
        AND activated_by_principal_id !~ '[[:space:][:cntrl:]]'
    ),
    activated_by_kind TEXT NOT NULL CHECK (activated_by_kind IN ('user', 'workload')),
    activated_by_audience TEXT NOT NULL CHECK (activated_by_audience = 'activator'),
    activated_at TIMESTAMPTZ NOT NULL,
    FOREIGN KEY (head_id, generation, revision_id, revision_digest)
        REFERENCES platform_governance.owner_policy_revision (
            policy_id, generation, revision_id, record_digest
        )
);

CREATE TABLE platform_governance.owner_policy_event (
    event_id TEXT PRIMARY KEY CHECK (
        event_id <> '' AND event_id = btrim(event_id) AND octet_length(event_id) <= 200
        AND event_id !~ '[[:space:][:cntrl:]]'
    ),
    schema_revision SMALLINT NOT NULL CHECK (schema_revision = 1),
    policy_id TEXT NOT NULL CHECK (
        policy_id <> '' AND policy_id = btrim(policy_id) AND octet_length(policy_id) <= 200
        AND policy_id !~ '[[:space:][:cntrl:]]'
    ),
    generation BIGINT NOT NULL CHECK (generation > 0),
    previous_revision_id TEXT,
    previous_revision_generation BIGINT,
    previous_revision_digest CHAR(64),
    current_revision_id TEXT NOT NULL,
    current_revision_digest CHAR(64) NOT NULL CHECK (current_revision_digest ~ '^[0-9a-f]{64}$'),
    actor_principal_id TEXT NOT NULL CHECK (
        actor_principal_id <> '' AND actor_principal_id = btrim(actor_principal_id)
        AND octet_length(actor_principal_id) <= 200
        AND actor_principal_id !~ '[[:space:][:cntrl:]]'
    ),
    actor_kind TEXT NOT NULL CHECK (actor_kind IN ('user', 'workload')),
    actor_audience TEXT NOT NULL CHECK (actor_audience = 'activator'),
    reason_code TEXT NOT NULL CHECK (reason_code ~ '^[a-z][a-z0-9_]{0,63}$'),
    occurred_at TIMESTAMPTZ NOT NULL,
    UNIQUE (policy_id, generation),
    FOREIGN KEY (policy_id, generation, current_revision_id, current_revision_digest)
        REFERENCES platform_governance.owner_policy_revision (
            policy_id, generation, revision_id, record_digest
        ),
    FOREIGN KEY (
        policy_id, previous_revision_generation, previous_revision_id,
        previous_revision_digest
    ) REFERENCES platform_governance.owner_policy_revision (
        policy_id, generation, revision_id, record_digest
    ),
    CHECK (
        (generation = 1
            AND previous_revision_id IS NULL
            AND previous_revision_generation IS NULL
            AND previous_revision_digest IS NULL)
        OR
        (generation > 1
            AND previous_revision_id IS NOT NULL
            AND previous_revision_generation = generation - 1
            AND previous_revision_digest IS NOT NULL
            AND previous_revision_digest ~ '^[0-9a-f]{64}$')
    )
);

CREATE TRIGGER owner_policy_revision_immutable
BEFORE UPDATE OR DELETE ON platform_governance.owner_policy_revision
FOR EACH ROW EXECUTE FUNCTION platform_governance.reject_immutable_mutation();

CREATE TRIGGER owner_policy_event_immutable
BEFORE UPDATE OR DELETE ON platform_governance.owner_policy_event
FOR EACH ROW EXECUTE FUNCTION platform_governance.reject_immutable_mutation();

-- RuntimePolicy operational limits are versioned database rows. Apart from
-- the parser/schema ceilings for collection sizes, this migration does not
-- compile deployment-specific limits into SQL or application configuration.
CREATE TABLE agent_control.runtime_policy_revision (
    policy_id TEXT NOT NULL CHECK (
        policy_id <> '' AND policy_id = btrim(policy_id) AND octet_length(policy_id) <= 200
        AND policy_id !~ '[[:space:][:cntrl:]]'
    ),
    schema_revision SMALLINT NOT NULL CHECK (schema_revision = 1),
    generation BIGINT NOT NULL CHECK (generation > 0),
    record_digest CHAR(64) NOT NULL UNIQUE CHECK (record_digest ~ '^[0-9a-f]{64}$'),
    max_model_calls BIGINT NOT NULL CHECK (max_model_calls >= 0),
    max_input_tokens BIGINT NOT NULL CHECK (max_input_tokens >= 0),
    max_output_tokens BIGINT NOT NULL CHECK (max_output_tokens >= 0),
    max_tool_calls BIGINT NOT NULL CHECK (max_tool_calls >= 0),
    max_external_cost_micro_usd BIGINT NOT NULL CHECK (max_external_cost_micro_usd >= 0),
    max_wall_time_ms BIGINT NOT NULL CHECK (max_wall_time_ms > 0),
    max_idle_time_ms BIGINT NOT NULL CHECK (max_idle_time_ms >= 0),
    max_tasks BIGINT NOT NULL CHECK (max_tasks > 0),
    max_depth BIGINT NOT NULL CHECK (max_depth >= 0),
    max_fanout BIGINT NOT NULL CHECK (max_fanout >= 0),
    max_parallelism BIGINT NOT NULL CHECK (max_parallelism > 0),
    max_invalid_output_retries BIGINT NOT NULL CHECK (max_invalid_output_retries >= 0),
    max_infrastructure_retries BIGINT NOT NULL CHECK (max_infrastructure_retries >= 0),
    max_lease_seconds BIGINT NOT NULL CHECK (max_lease_seconds > 0),
    max_heartbeat_extension_seconds BIGINT NOT NULL CHECK (
        max_heartbeat_extension_seconds > 0
        AND max_heartbeat_extension_seconds <= max_lease_seconds
    ),
    max_claim_batch BIGINT NOT NULL CHECK (max_claim_batch > 0),
    max_dependencies BIGINT NOT NULL CHECK (max_dependencies BETWEEN 1 AND 4096),
    max_artifact_sections BIGINT NOT NULL CHECK (max_artifact_sections BETWEEN 1 AND 256),
    dead_letter_retention_seconds BIGINT NOT NULL CHECK (dead_letter_retention_seconds > 0),
    updated_by_principal_id TEXT NOT NULL CHECK (
        updated_by_principal_id <> ''
        AND updated_by_principal_id = btrim(updated_by_principal_id)
        AND octet_length(updated_by_principal_id) <= 200
        AND updated_by_principal_id !~ '[[:space:][:cntrl:]]'
    ),
    updated_by_kind TEXT NOT NULL CHECK (updated_by_kind IN ('kernel', 'user', 'workload')),
    updated_by_audience TEXT NOT NULL CHECK (updated_by_audience IN (
        'activator', 'control_api', 'delegation_engine', 'grace_engine',
        'grace_intake', 'kernel', 'kernel_admin', 'research_gateway',
        'validator', 'worker'
    )),
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (policy_id, generation),
    UNIQUE (policy_id, generation, record_digest)
);

CREATE TABLE agent_control.runtime_policy_head (
    policy_id TEXT PRIMARY KEY CHECK (
        policy_id <> '' AND policy_id = btrim(policy_id) AND octet_length(policy_id) <= 200
        AND policy_id !~ '[[:space:][:cntrl:]]'
    ),
    generation BIGINT NOT NULL CHECK (generation > 0),
    record_digest CHAR(64) NOT NULL CHECK (record_digest ~ '^[0-9a-f]{64}$'),
    selected_by_principal_id TEXT NOT NULL CHECK (
        selected_by_principal_id <> ''
        AND selected_by_principal_id = btrim(selected_by_principal_id)
        AND octet_length(selected_by_principal_id) <= 200
        AND selected_by_principal_id !~ '[[:space:][:cntrl:]]'
    ),
    selected_by_kind TEXT NOT NULL CHECK (selected_by_kind = 'workload'),
    selected_by_audience TEXT NOT NULL CHECK (selected_by_audience = 'control_api'),
    selected_at TIMESTAMPTZ NOT NULL,
    FOREIGN KEY (policy_id, generation, record_digest)
        REFERENCES agent_control.runtime_policy_revision (policy_id, generation, record_digest)
);

CREATE TABLE agent_control.runtime_policy_event (
    event_id TEXT PRIMARY KEY CHECK (
        event_id <> '' AND event_id = btrim(event_id) AND octet_length(event_id) <= 200
        AND event_id !~ '[[:space:][:cntrl:]]'
    ),
    policy_id TEXT NOT NULL CHECK (
        policy_id <> '' AND policy_id = btrim(policy_id) AND octet_length(policy_id) <= 200
        AND policy_id !~ '[[:space:][:cntrl:]]'
    ),
    generation BIGINT NOT NULL CHECK (generation > 0),
    previous_generation BIGINT,
    previous_record_digest CHAR(64),
    current_record_digest CHAR(64) NOT NULL CHECK (current_record_digest ~ '^[0-9a-f]{64}$'),
    actor_principal_id TEXT NOT NULL CHECK (
        actor_principal_id <> '' AND actor_principal_id = btrim(actor_principal_id)
        AND octet_length(actor_principal_id) <= 200
        AND actor_principal_id !~ '[[:space:][:cntrl:]]'
    ),
    actor_kind TEXT NOT NULL CHECK (actor_kind = 'workload'),
    actor_audience TEXT NOT NULL CHECK (actor_audience = 'control_api'),
    reason_code TEXT NOT NULL CHECK (reason_code ~ '^[a-z][a-z0-9_]{0,63}$'),
    occurred_at TIMESTAMPTZ NOT NULL,
    UNIQUE (policy_id, generation),
    FOREIGN KEY (policy_id, generation, current_record_digest)
        REFERENCES agent_control.runtime_policy_revision (policy_id, generation, record_digest),
    FOREIGN KEY (policy_id, previous_generation, previous_record_digest)
        REFERENCES agent_control.runtime_policy_revision (policy_id, generation, record_digest),
    CHECK (
        (generation = 1 AND previous_generation IS NULL AND previous_record_digest IS NULL)
        OR
        (generation > 1 AND previous_generation IS NOT NULL
            AND previous_generation = generation - 1
            AND previous_record_digest IS NOT NULL
            AND previous_record_digest ~ '^[0-9a-f]{64}$')
    )
);

CREATE TRIGGER runtime_policy_revision_immutable
BEFORE UPDATE OR DELETE ON agent_control.runtime_policy_revision
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_immutable_runtime_definition_mutation();

CREATE TRIGGER runtime_policy_event_immutable
BEFORE UPDATE OR DELETE ON agent_control.runtime_policy_event
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_immutable_runtime_definition_mutation();

-- TriggerRegistration stores both referenced revisions exactly. The narrow
-- composite OwnerPolicy FK verifies existence and origin-kind compatibility;
-- the later command boundary additionally requires the exact active head.
CREATE TABLE agent_control.trigger_registration_revision (
    registration_id TEXT NOT NULL CHECK (
        registration_id <> '' AND registration_id = btrim(registration_id)
        AND octet_length(registration_id) <= 200
        AND registration_id !~ '[[:space:][:cntrl:]]'
    ),
    schema_revision SMALLINT NOT NULL CHECK (schema_revision = 1),
    generation BIGINT NOT NULL CHECK (generation > 0),
    record_digest CHAR(64) NOT NULL UNIQUE CHECK (record_digest ~ '^[0-9a-f]{64}$'),
    kind TEXT NOT NULL CHECK (kind IN (
        'schedule', 'kernel_event', 'external_event', 'system_maintenance'
    )),
    source_key TEXT NOT NULL CHECK (
        source_key <> '' AND source_key = btrim(source_key) AND octet_length(source_key) <= 200
        AND source_key !~ '[[:space:][:cntrl:]]'
    ),
    owner_policy_owner TEXT NOT NULL CHECK (owner_policy_owner = 'platform_governance'),
    owner_policy_record_type TEXT NOT NULL CHECK (
        owner_policy_record_type = 'owner_policy_revision'
    ),
    owner_policy_record_id TEXT NOT NULL CHECK (
        owner_policy_record_id <> ''
        AND owner_policy_record_id = btrim(owner_policy_record_id)
        AND octet_length(owner_policy_record_id) <= 200
        AND owner_policy_record_id !~ '[[:space:][:cntrl:]]'
    ),
    owner_policy_schema_revision SMALLINT NOT NULL CHECK (owner_policy_schema_revision = 1),
    owner_policy_record_digest CHAR(64) NOT NULL CHECK (
        owner_policy_record_digest ~ '^[0-9a-f]{64}$'
    ),
    owner_policy_generation BIGINT NOT NULL CHECK (owner_policy_generation > 0),
    runtime_policy_owner TEXT NOT NULL CHECK (runtime_policy_owner = 'agent_control'),
    runtime_policy_record_type TEXT NOT NULL CHECK (runtime_policy_record_type = 'runtime_policy'),
    runtime_policy_record_id TEXT NOT NULL CHECK (
        runtime_policy_record_id <> ''
        AND runtime_policy_record_id = btrim(runtime_policy_record_id)
        AND octet_length(runtime_policy_record_id) <= 200
        AND runtime_policy_record_id !~ '[[:space:][:cntrl:]]'
    ),
    runtime_policy_schema_revision SMALLINT NOT NULL CHECK (runtime_policy_schema_revision = 1),
    runtime_policy_record_digest CHAR(64) NOT NULL CHECK (
        runtime_policy_record_digest ~ '^[0-9a-f]{64}$'
    ),
    runtime_policy_generation BIGINT NOT NULL CHECK (runtime_policy_generation > 0),
    enabled BOOLEAN NOT NULL,
    updated_by_principal_id TEXT NOT NULL CHECK (
        updated_by_principal_id <> ''
        AND updated_by_principal_id = btrim(updated_by_principal_id)
        AND octet_length(updated_by_principal_id) <= 200
        AND updated_by_principal_id !~ '[[:space:][:cntrl:]]'
    ),
    updated_by_kind TEXT NOT NULL CHECK (updated_by_kind IN ('kernel', 'user', 'workload')),
    updated_by_audience TEXT NOT NULL CHECK (updated_by_audience IN (
        'activator', 'control_api', 'delegation_engine', 'grace_engine',
        'grace_intake', 'kernel', 'kernel_admin', 'research_gateway',
        'validator', 'worker'
    )),
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (registration_id, generation),
    UNIQUE (registration_id, generation, record_digest),
    FOREIGN KEY (
        owner_policy_record_id, owner_policy_generation,
        owner_policy_record_digest, kind
    ) REFERENCES platform_governance.owner_policy_revision (
        revision_id, generation, record_digest, origin_kind
    ),
    FOREIGN KEY (
        runtime_policy_record_id, runtime_policy_generation,
        runtime_policy_record_digest
    ) REFERENCES agent_control.runtime_policy_revision (
        policy_id, generation, record_digest
    )
);

CREATE INDEX trigger_registration_source_idx
ON agent_control.trigger_registration_revision (kind, source_key, registration_id, generation);

CREATE TABLE agent_control.trigger_registration_head (
    registration_id TEXT PRIMARY KEY CHECK (
        registration_id <> '' AND registration_id = btrim(registration_id)
        AND octet_length(registration_id) <= 200
        AND registration_id !~ '[[:space:][:cntrl:]]'
    ),
    generation BIGINT NOT NULL CHECK (generation > 0),
    record_digest CHAR(64) NOT NULL CHECK (record_digest ~ '^[0-9a-f]{64}$'),
    selected_by_principal_id TEXT NOT NULL CHECK (
        selected_by_principal_id <> ''
        AND selected_by_principal_id = btrim(selected_by_principal_id)
        AND octet_length(selected_by_principal_id) <= 200
        AND selected_by_principal_id !~ '[[:space:][:cntrl:]]'
    ),
    selected_by_kind TEXT NOT NULL CHECK (selected_by_kind = 'workload'),
    selected_by_audience TEXT NOT NULL CHECK (selected_by_audience = 'control_api'),
    selected_at TIMESTAMPTZ NOT NULL,
    FOREIGN KEY (registration_id, generation, record_digest)
        REFERENCES agent_control.trigger_registration_revision (
            registration_id, generation, record_digest
        )
);

CREATE TABLE agent_control.trigger_registration_event (
    event_id TEXT PRIMARY KEY CHECK (
        event_id <> '' AND event_id = btrim(event_id) AND octet_length(event_id) <= 200
        AND event_id !~ '[[:space:][:cntrl:]]'
    ),
    registration_id TEXT NOT NULL CHECK (
        registration_id <> '' AND registration_id = btrim(registration_id)
        AND octet_length(registration_id) <= 200
        AND registration_id !~ '[[:space:][:cntrl:]]'
    ),
    generation BIGINT NOT NULL CHECK (generation > 0),
    previous_generation BIGINT,
    previous_record_digest CHAR(64),
    current_record_digest CHAR(64) NOT NULL CHECK (current_record_digest ~ '^[0-9a-f]{64}$'),
    actor_principal_id TEXT NOT NULL CHECK (
        actor_principal_id <> '' AND actor_principal_id = btrim(actor_principal_id)
        AND octet_length(actor_principal_id) <= 200
        AND actor_principal_id !~ '[[:space:][:cntrl:]]'
    ),
    actor_kind TEXT NOT NULL CHECK (actor_kind = 'workload'),
    actor_audience TEXT NOT NULL CHECK (actor_audience = 'control_api'),
    reason_code TEXT NOT NULL CHECK (reason_code ~ '^[a-z][a-z0-9_]{0,63}$'),
    occurred_at TIMESTAMPTZ NOT NULL,
    UNIQUE (registration_id, generation),
    FOREIGN KEY (registration_id, generation, current_record_digest)
        REFERENCES agent_control.trigger_registration_revision (
            registration_id, generation, record_digest
        ),
    FOREIGN KEY (registration_id, previous_generation, previous_record_digest)
        REFERENCES agent_control.trigger_registration_revision (
            registration_id, generation, record_digest
        ),
    CHECK (
        (generation = 1 AND previous_generation IS NULL AND previous_record_digest IS NULL)
        OR
        (generation > 1 AND previous_generation IS NOT NULL
            AND previous_generation = generation - 1
            AND previous_record_digest IS NOT NULL
            AND previous_record_digest ~ '^[0-9a-f]{64}$')
    )
);

CREATE TRIGGER trigger_registration_revision_immutable
BEFORE UPDATE OR DELETE ON agent_control.trigger_registration_revision
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_immutable_runtime_definition_mutation();

CREATE TRIGGER trigger_registration_event_immutable
BEFORE UPDATE OR DELETE ON agent_control.trigger_registration_event
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_immutable_runtime_definition_mutation();

-- OutputContractRevision binds one exact JSON schema Blob. It deliberately has
-- no activation head in AP1: a Task binds the immutable revision directly.
CREATE TABLE agent_control.output_contract_revision (
    revision_id TEXT PRIMARY KEY CHECK (
        revision_id <> '' AND revision_id = btrim(revision_id)
        AND octet_length(revision_id) <= 200
        AND revision_id !~ '[[:space:][:cntrl:]]'
    ),
    schema_revision SMALLINT NOT NULL CHECK (schema_revision = 1),
    generation BIGINT NOT NULL CHECK (generation > 0),
    record_digest CHAR(64) NOT NULL UNIQUE CHECK (record_digest ~ '^[0-9a-f]{64}$'),
    artifact_type TEXT NOT NULL CHECK (artifact_type ~ '^[a-z][a-z0-9_]{0,63}$'),
    schema_blob_schema_revision SMALLINT NOT NULL CHECK (schema_blob_schema_revision = 1),
    schema_blob_id UUID NOT NULL CHECK (
        schema_blob_id::TEXT ~
            '^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
    ),
    schema_blob_content_digest CHAR(64) NOT NULL CHECK (
        schema_blob_content_digest ~ '^[0-9a-f]{64}$'
    ),
    schema_blob_media_type TEXT NOT NULL CHECK (schema_blob_media_type = 'application/json'),
    schema_blob_size_bytes BIGINT NOT NULL CHECK (
        schema_blob_size_bytes BETWEEN 1 AND 1073741824
    ),
    schema_origin_owner TEXT NOT NULL CHECK (schema_origin_owner = 'agent_control'),
    schema_origin_record_type TEXT NOT NULL CHECK (
        schema_origin_record_type = 'output_contract_schema'
    ),
    schema_origin_record_id TEXT NOT NULL CHECK (
        schema_origin_record_id <> ''
        AND schema_origin_record_id = btrim(schema_origin_record_id)
        AND octet_length(schema_origin_record_id) <= 200
        AND schema_origin_record_id !~ '[[:space:][:cntrl:]]'
    ),
    schema_origin_schema_revision SMALLINT NOT NULL CHECK (schema_origin_schema_revision = 1),
    schema_origin_record_digest CHAR(64) NOT NULL CHECK (
        schema_origin_record_digest ~ '^[0-9a-f]{64}$'
    ),
    schema_blob_committed_at TIMESTAMPTZ NOT NULL,
    effect_class TEXT NOT NULL CHECK (effect_class = 'none'),
    author_principal_id TEXT NOT NULL CHECK (
        author_principal_id <> '' AND author_principal_id = btrim(author_principal_id)
        AND octet_length(author_principal_id) <= 200
        AND author_principal_id !~ '[[:space:][:cntrl:]]'
    ),
    author_kind TEXT NOT NULL CHECK (author_kind = 'workload'),
    author_audience TEXT NOT NULL CHECK (author_audience = 'control_api'),
    reason_code TEXT NOT NULL CHECK (reason_code ~ '^[a-z][a-z0-9_]{0,63}$'),
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (revision_id, generation, record_digest),
    CHECK (schema_blob_committed_at <= created_at)
);

CREATE TRIGGER output_contract_revision_immutable
BEFORE UPDATE OR DELETE ON agent_control.output_contract_revision
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_immutable_runtime_definition_mutation();

-- Default privileges already deny PUBLIC. Repeat the boundary here so this
-- migration remains safe if it is inspected or replayed independently. No
-- application role receives direct definition writes; 0006 owns narrow,
-- identity-derived command APIs.
REVOKE ALL ON
    platform_governance.owner_policy_revision,
    platform_governance.owner_policy_head,
    platform_governance.owner_policy_event,
    agent_control.runtime_policy_revision,
    agent_control.runtime_policy_head,
    agent_control.runtime_policy_event,
    agent_control.trigger_registration_revision,
    agent_control.trigger_registration_head,
    agent_control.trigger_registration_event,
    agent_control.output_contract_revision
FROM PUBLIC;

RESET ROLE;
