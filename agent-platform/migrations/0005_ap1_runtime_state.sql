SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

-- AP1 runtime persistence contains no scheduler, model adapter, operation
-- emission, or other effect path.  Application roles receive no direct DML;
-- 0006 exposes the fenced SECURITY DEFINER command transactions.

CREATE FUNCTION agent_control.runtime_identifier_valid(p_value TEXT)
RETURNS BOOLEAN
LANGUAGE sql
IMMUTABLE
AS $$
    SELECT p_value IS NOT NULL
       AND octet_length(p_value) BETWEEN 1 AND 200
       AND p_value = btrim(p_value)
       AND p_value !~ '[[:space:][:cntrl:]]'
$$;

CREATE FUNCTION agent_control.runtime_name_valid(p_value TEXT)
RETURNS BOOLEAN
LANGUAGE sql
IMMUTABLE
AS $$
    SELECT p_value IS NOT NULL AND p_value ~ '^[a-z][a-z0-9_]{0,63}$'
$$;

CREATE FUNCTION agent_control.runtime_digest_valid(p_value TEXT)
RETURNS BOOLEAN
LANGUAGE sql
IMMUTABLE
AS $$
    SELECT p_value IS NOT NULL AND p_value ~ '^[0-9a-f]{64}$'
$$;

CREATE FUNCTION agent_control.runtime_actor_valid(p_value JSONB)
RETURNS BOOLEAN
LANGUAGE plpgsql
IMMUTABLE
STRICT
AS $$
BEGIN
    RETURN COALESCE(
       jsonb_typeof(p_value) = 'object'
       AND p_value ?& ARRAY['principal_id', 'kind', 'audience']
       AND p_value - ARRAY['principal_id', 'kind', 'audience'] = '{}'::JSONB
       AND jsonb_typeof(p_value->'principal_id') = 'string'
       AND jsonb_typeof(p_value->'kind') = 'string'
       AND jsonb_typeof(p_value->'audience') = 'string'
       AND agent_control.runtime_identifier_valid(p_value->>'principal_id')
       AND p_value->>'kind' IN ('user', 'workload', 'kernel')
       AND p_value->>'audience' IN (
           'control_api', 'worker', 'research_gateway', 'grace_intake',
           'grace_engine', 'delegation_engine', 'validator', 'activator',
           'kernel', 'kernel_admin'
       ), false);
EXCEPTION WHEN OTHERS THEN
    RETURN false;
END
$$;

CREATE FUNCTION agent_control.runtime_record_ref_valid(
    p_value JSONB,
    p_expected_owner TEXT,
    p_expected_record_type TEXT
) RETURNS BOOLEAN
LANGUAGE plpgsql
IMMUTABLE
STRICT
AS $$
BEGIN
    RETURN COALESCE(
       jsonb_typeof(p_value) = 'object'
       AND p_value ?& ARRAY['owner', 'record_type', 'record_id', 'schema_revision', 'record_digest']
       AND p_value - ARRAY['owner', 'record_type', 'record_id', 'schema_revision', 'record_digest'] = '{}'::JSONB
       AND jsonb_typeof(p_value->'owner') = 'string'
       AND jsonb_typeof(p_value->'record_type') = 'string'
       AND jsonb_typeof(p_value->'record_id') = 'string'
       AND jsonb_typeof(p_value->'record_digest') = 'string'
       AND p_value->>'owner' IN (
           'agent_control', 'worker', 'platform_governance', 'blob',
           'research_gateway', 'grace', 'delegation', 'kernel'
       )
       AND (p_expected_owner = '' OR p_value->>'owner' = p_expected_owner)
       AND agent_control.runtime_name_valid(p_value->>'record_type')
       AND (p_expected_record_type = '' OR p_value->>'record_type' = p_expected_record_type)
       AND agent_control.runtime_identifier_valid(p_value->>'record_id')
       AND jsonb_typeof(p_value->'schema_revision') = 'number'
       AND p_value->>'schema_revision' = '1'
       AND agent_control.runtime_digest_valid(p_value->>'record_digest'), false);
EXCEPTION WHEN OTHERS THEN
    RETURN false;
END
$$;

CREATE FUNCTION agent_control.runtime_failure_valid(p_value JSONB)
RETURNS BOOLEAN
LANGUAGE plpgsql
IMMUTABLE
STRICT
AS $$
BEGIN
    RETURN COALESCE(
       jsonb_typeof(p_value) = 'object'
       AND p_value ?& ARRAY['code', 'message', 'retryable']
       AND p_value - ARRAY['code', 'message', 'retryable'] = '{}'::JSONB
       AND jsonb_typeof(p_value->'code') = 'string'
       AND jsonb_typeof(p_value->'message') = 'string'
       AND agent_control.runtime_name_valid(p_value->>'code')
       AND p_value->>'message' ~ '[^[:space:]]'
       AND octet_length(p_value->>'message') <= 1000
       AND jsonb_typeof(p_value->'retryable') = 'boolean', false);
EXCEPTION WHEN OTHERS THEN
    RETURN false;
END
$$;

CREATE FUNCTION agent_control.runtime_blob_ref_valid(
    p_value JSONB,
    p_expected_origin_type TEXT,
    p_expected_media_type TEXT
) RETURNS BOOLEAN
LANGUAGE plpgsql
IMMUTABLE
STRICT
AS $$
DECLARE
    size_value NUMERIC;
    committed_value TIMESTAMPTZ;
BEGIN
    IF NOT COALESCE(
       jsonb_typeof(p_value) = 'object'
       AND p_value ?& ARRAY[
           'schema_revision', 'blob_id', 'content_digest', 'media_type',
           'size_bytes', 'origin', 'committed_at'
       ]
       AND p_value - ARRAY[
           'schema_revision', 'blob_id', 'content_digest', 'media_type',
           'size_bytes', 'origin', 'committed_at'
       ] = '{}'::JSONB
       AND jsonb_typeof(p_value->'schema_revision') = 'number'
       AND p_value->>'schema_revision' = '1'
       AND jsonb_typeof(p_value->'blob_id') = 'string'
       AND p_value->>'blob_id' ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
       AND jsonb_typeof(p_value->'content_digest') = 'string'
       AND agent_control.runtime_digest_valid(p_value->>'content_digest')
       AND jsonb_typeof(p_value->'media_type') = 'string'
       AND octet_length(p_value->>'media_type') BETWEEN 1 AND 200
       AND lower(p_value->>'media_type') = p_value->>'media_type'
       AND (p_expected_media_type = '' OR p_value->>'media_type' = p_expected_media_type)
       AND jsonb_typeof(p_value->'size_bytes') = 'number'
       AND p_value->>'size_bytes' ~ '^[1-9][0-9]*$'
       AND jsonb_typeof(p_value->'origin') = 'object'
       AND agent_control.runtime_record_ref_valid(
           p_value->'origin', 'agent_control', p_expected_origin_type
       )
       AND jsonb_typeof(p_value->'committed_at') = 'string'
       AND p_value->>'committed_at' ~ 'Z$', false) THEN
        RETURN false;
    END IF;
    size_value := (p_value->>'size_bytes')::NUMERIC;
    committed_value := (p_value->>'committed_at')::TIMESTAMPTZ;
    RETURN COALESCE(
        size_value BETWEEN 1 AND 1073741824
        AND committed_value IS NOT NULL,
        false
    );
EXCEPTION WHEN OTHERS THEN
    RETURN false;
END
$$;

CREATE FUNCTION agent_control.reject_runtime_immutable_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION USING ERRCODE = '55000', MESSAGE = 'immutable runtime record';
END
$$;

-- Mutable projections may change only the columns named by their trigger.
-- Identity, lineage, deadlines, frozen limits, and canonical references remain
-- immutable even to a future command function.
CREATE FUNCTION agent_control.guard_runtime_mutable_columns()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION USING ERRCODE = '55000', MESSAGE = 'runtime state deletion is forbidden';
    END IF;
    IF (to_jsonb(NEW) - TG_ARGV) IS DISTINCT FROM (to_jsonb(OLD) - TG_ARGV) THEN
        RAISE EXCEPTION USING ERRCODE = '55000', MESSAGE = 'immutable runtime columns changed';
    END IF;
    RETURN NEW;
END
$$;

CREATE FUNCTION agent_control.runtime_transition_allowed(
    p_subject TEXT,
    p_from_state TEXT,
    p_to_state TEXT
) RETURNS BOOLEAN
LANGUAGE sql
IMMUTABLE
AS $$
    SELECT COALESCE(CASE p_subject
        WHEN 'run' THEN CASE p_from_state
            WHEN 'queued' THEN p_to_state IN ('running', 'canceled', 'superseded', 'dead_lettered')
            WHEN 'running' THEN p_to_state IN ('waiting', 'canceling', 'succeeded', 'failed', 'dead_lettered')
            WHEN 'waiting' THEN p_to_state IN ('running', 'canceling', 'failed', 'canceled', 'superseded', 'dead_lettered')
            WHEN 'canceling' THEN p_to_state IN ('canceled', 'failed', 'dead_lettered')
            ELSE false END
        WHEN 'task' THEN CASE p_from_state
            WHEN 'blocked' THEN p_to_state IN ('ready', 'canceled', 'superseded', 'dead_lettered')
            WHEN 'ready' THEN p_to_state IN ('running', 'canceled', 'superseded', 'dead_lettered')
            WHEN 'running' THEN p_to_state IN ('waiting', 'result_committed', 'failed', 'canceled', 'superseded', 'dead_lettered')
            WHEN 'waiting' THEN p_to_state IN ('ready', 'running', 'failed', 'canceled', 'superseded', 'dead_lettered')
            WHEN 'result_committed' THEN p_to_state IN ('succeeded', 'canceled', 'superseded', 'dead_lettered')
            ELSE false END
        WHEN 'session' THEN p_from_state = 'open' AND p_to_state = 'closed'
        WHEN 'attempt' THEN CASE p_from_state
            WHEN 'leased' THEN p_to_state IN ('executing', 'failed', 'timed_out', 'canceled', 'superseded')
            WHEN 'executing' THEN p_to_state IN ('result_committed', 'failed', 'timed_out', 'canceled', 'superseded')
            ELSE false END
        WHEN 'turn' THEN CASE p_from_state
            WHEN 'planned' THEN p_to_state IN ('dispatched', 'canceled')
            WHEN 'dispatched' THEN p_to_state IN ('result_committed', 'failed', 'unknown', 'canceled')
            WHEN 'unknown' THEN p_to_state IN ('result_committed', 'failed')
            ELSE false END
        WHEN 'budget' THEN CASE p_from_state
            WHEN 'open' THEN p_to_state IN ('exhausted', 'closed')
            WHEN 'exhausted' THEN p_to_state = 'closed'
            ELSE false END
        ELSE false
    END, false)
$$;

CREATE FUNCTION agent_control.runtime_terminal_state(
    p_subject TEXT,
    p_state TEXT
) RETURNS BOOLEAN
LANGUAGE sql
IMMUTABLE
AS $$
    SELECT COALESCE(CASE p_subject
        WHEN 'run' THEN p_state IN (
            'succeeded', 'failed', 'canceled', 'superseded', 'dead_lettered'
        )
        WHEN 'task' THEN p_state IN (
            'succeeded', 'failed', 'canceled', 'superseded', 'dead_lettered'
        )
        WHEN 'session' THEN p_state = 'closed'
        WHEN 'attempt' THEN p_state IN (
            'result_committed', 'failed', 'timed_out', 'canceled', 'superseded'
        )
        WHEN 'turn' THEN p_state IN ('result_committed', 'failed', 'canceled')
        WHEN 'budget' THEN p_state = 'closed'
        ELSE false
    END, false)
$$;

CREATE FUNCTION agent_control.guard_runtime_initial_insert()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    subject_name TEXT := TG_ARGV[0];
    generation_column TEXT := TG_ARGV[1];
    initial_generation BIGINT;
    valid_initial BOOLEAN;
BEGIN
    initial_generation := (to_jsonb(NEW)->>generation_column)::BIGINT;
    valid_initial := CASE subject_name
        WHEN 'run' THEN NEW.state::TEXT = 'queued'
        WHEN 'task' THEN NEW.state::TEXT IN ('blocked', 'ready')
        WHEN 'session' THEN NEW.state::TEXT = 'open'
        WHEN 'attempt' THEN NEW.state::TEXT = 'leased'
        WHEN 'turn' THEN NEW.state::TEXT = 'planned'
        WHEN 'budget' THEN NEW.state::TEXT = 'open'
        ELSE false
    END;
    IF initial_generation <> 1 OR NOT valid_initial THEN
        RAISE EXCEPTION USING ERRCODE = '23514',
            MESSAGE = 'invalid initial runtime state';
    END IF;
    RETURN NEW;
END
$$;

CREATE FUNCTION agent_control.guard_runtime_state_transition()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    subject_name TEXT := TG_ARGV[0];
    generation_column TEXT := TG_ARGV[1];
    old_generation BIGINT;
    new_generation BIGINT;
BEGIN
    old_generation := (to_jsonb(OLD)->>generation_column)::BIGINT;
    new_generation := (to_jsonb(NEW)->>generation_column)::BIGINT;
    IF to_jsonb(NEW) ? 'updated_at'
       AND (to_jsonb(NEW)->>'updated_at')::TIMESTAMPTZ
           < (to_jsonb(OLD)->>'updated_at')::TIMESTAMPTZ THEN
        RAISE EXCEPTION USING ERRCODE = '40001',
            MESSAGE = 'runtime updated_at moved backwards';
    END IF;
    IF agent_control.runtime_terminal_state(subject_name, OLD.state::TEXT) THEN
        RAISE EXCEPTION USING ERRCODE = '55000',
            MESSAGE = 'terminal runtime state is immutable';
    END IF;
    IF NEW.state IS DISTINCT FROM OLD.state THEN
        IF new_generation <> old_generation + 1
           OR NOT agent_control.runtime_transition_allowed(
               subject_name, OLD.state::TEXT, NEW.state::TEXT
           ) THEN
            RAISE EXCEPTION USING ERRCODE = '40001', MESSAGE = 'invalid runtime state transition';
        END IF;
    ELSIF new_generation <> old_generation THEN
        RAISE EXCEPTION USING ERRCODE = '40001', MESSAGE = 'state generation changed without transition';
    END IF;
    RETURN NEW;
END
$$;

CREATE FUNCTION agent_control.guard_runtime_turn_update()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.state = OLD.state THEN
        RAISE EXCEPTION USING ERRCODE = '55000',
            MESSAGE = 'turn same-state mutation is forbidden';
    END IF;
    IF OLD.dispatched_at IS NOT NULL
       AND NEW.dispatched_at IS DISTINCT FROM OLD.dispatched_at THEN
        RAISE EXCEPTION USING ERRCODE = '55000',
            MESSAGE = 'turn dispatched_at is immutable once set';
    END IF;
    IF OLD.finished_at IS NOT NULL
       AND NEW.finished_at IS DISTINCT FROM OLD.finished_at THEN
        RAISE EXCEPTION USING ERRCODE = '55000',
            MESSAGE = 'turn finished_at is immutable once set';
    END IF;
    RETURN NEW;
END
$$;

CREATE TABLE agent_control.runtime_command (
    principal_id TEXT NOT NULL CHECK (agent_control.runtime_identifier_valid(principal_id)),
    command_type TEXT NOT NULL CHECK (command_type IN (
        'claim_task', 'start_attempt', 'heartbeat_attempt',
        'dispatch_model_call', 'resolve_model_call', 'mark_model_call_unknown',
        'commit_attempt', 'fail_attempt', 'request_child_task'
    )),
    idempotency_key TEXT NOT NULL CHECK (agent_control.runtime_identifier_valid(idempotency_key)),
    command_id TEXT NOT NULL UNIQUE CHECK (agent_control.runtime_identifier_valid(command_id)),
    schema_revision SMALLINT NOT NULL CHECK (schema_revision = 1),
    actor_kind TEXT NOT NULL CHECK (actor_kind = 'workload'),
    actor_audience TEXT NOT NULL CHECK (actor_audience = 'worker'),
    command_audience TEXT NOT NULL CHECK (command_audience = 'control_api'),
    request_digest CHAR(64) NOT NULL CHECK (agent_control.runtime_digest_valid(request_digest::TEXT)),
    body_fingerprint CHAR(64) NOT NULL CHECK (
        agent_control.runtime_digest_valid(body_fingerprint::TEXT)
    ),
    causation_id TEXT NOT NULL CHECK (agent_control.runtime_identifier_valid(causation_id)),
    correlation_id TEXT NOT NULL CHECK (agent_control.runtime_identifier_valid(correlation_id)),
    deadline_at TIMESTAMPTZ NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('processing', 'committed', 'denied')),
    response JSONB,
    response_digest CHAR(64),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    committed_at TIMESTAMPTZ,
    PRIMARY KEY (principal_id, command_type, idempotency_key),
    CHECK (response IS NULL OR (
        jsonb_typeof(response) = 'object' AND octet_length(response::TEXT) <= 1048576
    )),
    CHECK (response_digest IS NULL OR agent_control.runtime_digest_valid(response_digest::TEXT)),
    -- Deadline freshness is evaluated with database time by 0006. Keeping an
    -- already-expired envelope durable is required for exact denied replay.
    CHECK (
        (state = 'processing' AND response IS NULL AND response_digest IS NULL AND committed_at IS NULL)
        OR
        (state IN ('committed', 'denied') AND response IS NOT NULL
            AND response_digest IS NOT NULL AND committed_at IS NOT NULL
            AND committed_at >= created_at)
    )
);

CREATE TRIGGER runtime_command_guard
BEFORE UPDATE OR DELETE ON agent_control.runtime_command
FOR EACH ROW EXECUTE FUNCTION agent_control.guard_runtime_mutable_columns(
    'state', 'response', 'response_digest', 'committed_at'
);

CREATE FUNCTION agent_control.guard_runtime_command_update()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF OLD.state <> 'processing' THEN
        RAISE EXCEPTION USING ERRCODE = '55000',
            MESSAGE = 'completed runtime command is immutable';
    END IF;
    IF NEW.state NOT IN ('committed', 'denied') THEN
        RAISE EXCEPTION USING ERRCODE = '40001',
            MESSAGE = 'runtime command must complete exactly once';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER runtime_command_state_guard
BEFORE UPDATE ON agent_control.runtime_command
FOR EACH ROW EXECUTE FUNCTION agent_control.guard_runtime_command_update();

-- RevisionRef carries revision_id/generation/digest but not the policy family
-- id. This exact key supports durable references without weakening identity.
ALTER TABLE platform_governance.owner_policy_revision
ADD CONSTRAINT owner_policy_revision_exact_ref_key
UNIQUE (revision_id, generation, record_digest);

CREATE TABLE agent_control.trigger_occurrence (
    occurrence_id TEXT PRIMARY KEY CHECK (agent_control.runtime_identifier_valid(occurrence_id)),
    schema_revision SMALLINT NOT NULL CHECK (schema_revision = 1),
    record_digest CHAR(64) NOT NULL UNIQUE CHECK (agent_control.runtime_digest_valid(record_digest::TEXT)),
    registration_owner TEXT,
    registration_record_type TEXT,
    registration_id TEXT,
    registration_schema_revision SMALLINT,
    registration_generation BIGINT,
    registration_digest CHAR(64),
    kind TEXT NOT NULL CHECK (kind IN (
        'schedule', 'kernel_event', 'external_event',
        'system_maintenance', 'system_recovery'
    )),
    source_owner TEXT NOT NULL CHECK (source_owner IN (
        'agent_control', 'worker', 'platform_governance', 'blob',
        'research_gateway', 'grace', 'delegation', 'kernel'
    )),
    source_record_type TEXT NOT NULL CHECK (
        agent_control.runtime_name_valid(source_record_type)
    ),
    source_record_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(source_record_id)
    ),
    source_schema_revision SMALLINT NOT NULL CHECK (source_schema_revision = 1),
    source_record_digest CHAR(64) NOT NULL CHECK (
        agent_control.runtime_digest_valid(source_record_digest::TEXT)
    ),
    initiating_principal_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(initiating_principal_id)
    ),
    initiating_kind TEXT NOT NULL CHECK (initiating_kind IN ('user', 'workload', 'kernel')),
    initiating_audience TEXT NOT NULL CHECK (initiating_audience IN (
        'control_api', 'worker', 'research_gateway', 'grace_intake',
        'grace_engine', 'delegation_engine', 'validator', 'activator',
        'kernel', 'kernel_admin'
    )),
    owner_policy_owner TEXT NOT NULL CHECK (
        owner_policy_owner = 'platform_governance'
    ),
    owner_policy_record_type TEXT NOT NULL CHECK (
        owner_policy_record_type = 'owner_policy_revision'
    ),
    owner_policy_record_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(owner_policy_record_id)
    ),
    owner_policy_schema_revision SMALLINT NOT NULL CHECK (
        owner_policy_schema_revision = 1
    ),
    owner_policy_record_digest CHAR(64) NOT NULL CHECK (
        agent_control.runtime_digest_valid(owner_policy_record_digest::TEXT)
    ),
    owner_policy_generation BIGINT NOT NULL CHECK (owner_policy_generation > 0),
    occurrence_key TEXT NOT NULL CHECK (agent_control.runtime_identifier_valid(occurrence_key)),
    payload JSONB CHECK (
        payload IS NULL OR agent_control.runtime_blob_ref_valid(
            payload, 'trigger_payload', ''
        )
    ),
    occurred_at TIMESTAMPTZ NOT NULL,
    observed_at TIMESTAMPTZ NOT NULL,
    committed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (occurrence_id, record_digest),
    CHECK (occurred_at <= observed_at AND observed_at <= committed_at),
    CHECK (payload IS NULL
        OR (payload->>'committed_at')::TIMESTAMPTZ <= committed_at),
    CHECK (COALESCE(
        (kind = 'system_recovery'
            AND registration_owner IS NULL
            AND registration_record_type IS NULL
            AND registration_id IS NULL
            AND registration_schema_revision IS NULL
            AND registration_generation IS NULL
            AND registration_digest IS NULL)
        OR
        (kind <> 'system_recovery'
            AND registration_owner IS NOT NULL
            AND registration_owner = 'agent_control'
            AND registration_record_type IS NOT NULL
            AND registration_record_type = 'trigger_registration'
            AND registration_id IS NOT NULL
            AND agent_control.runtime_identifier_valid(registration_id)
            AND registration_schema_revision IS NOT NULL
            AND registration_schema_revision = 1
            AND registration_generation IS NOT NULL
            AND registration_generation > 0
            AND registration_digest IS NOT NULL
            AND agent_control.runtime_digest_valid(registration_digest::TEXT))
    , false)),
    CHECK (CASE kind
        WHEN 'schedule' THEN source_owner = 'agent_control'
            AND source_record_type = 'schedule_occurrence'
            AND initiating_kind = 'workload'
            AND initiating_audience = 'control_api'
        WHEN 'kernel_event' THEN source_owner = 'kernel'
            AND source_record_type = 'kernel_event'
            AND initiating_kind = 'kernel'
            AND initiating_audience = 'kernel'
        WHEN 'external_event' THEN source_owner = 'agent_control'
            AND source_record_type = 'external_event'
            AND initiating_kind = 'workload'
            AND initiating_audience = 'control_api'
        WHEN 'system_maintenance' THEN source_owner = 'agent_control'
            AND source_record_type = 'maintenance_occurrence'
            AND initiating_kind = 'workload'
            AND initiating_audience = 'control_api'
        WHEN 'system_recovery' THEN source_owner = 'agent_control'
            AND source_record_type = 'recovery_occurrence'
            AND initiating_kind = 'workload'
            AND initiating_audience = 'control_api'
        ELSE false END),
    FOREIGN KEY (registration_id, registration_generation, registration_digest)
        REFERENCES agent_control.trigger_registration_revision(
            registration_id, generation, record_digest
        ) DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (
        owner_policy_record_id, owner_policy_generation,
        owner_policy_record_digest
    ) REFERENCES platform_governance.owner_policy_revision(
        revision_id, generation, record_digest
    ) DEFERRABLE INITIALLY DEFERRED
);

CREATE UNIQUE INDEX trigger_occurrence_registered_dedupe_idx
ON agent_control.trigger_occurrence (
    registration_id, registration_generation, occurrence_key
) WHERE kind <> 'system_recovery';

CREATE UNIQUE INDEX trigger_occurrence_recovery_dedupe_idx
ON agent_control.trigger_occurrence (
    source_record_id, occurrence_key
) WHERE kind = 'system_recovery';

CREATE TABLE agent_control.runtime_run (
    run_id TEXT PRIMARY KEY CHECK (agent_control.runtime_identifier_valid(run_id)),
    schema_revision SMALLINT NOT NULL CHECK (schema_revision = 1),
    occurrence_owner TEXT,
    occurrence_record_type TEXT,
    occurrence_id TEXT,
    occurrence_schema_revision SMALLINT,
    occurrence_digest CHAR(64),
    origin_kind TEXT NOT NULL CHECK (origin_kind IN (
        'user_request', 'schedule', 'kernel_event', 'external_event',
        'system_maintenance', 'system_recovery'
    )),
    origin_source_owner TEXT NOT NULL CHECK (origin_source_owner IN (
        'agent_control', 'worker', 'platform_governance', 'blob',
        'research_gateway', 'grace', 'delegation', 'kernel'
    )),
    origin_source_record_type TEXT NOT NULL CHECK (
        agent_control.runtime_name_valid(origin_source_record_type)
    ),
    origin_source_record_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(origin_source_record_id)
    ),
    origin_source_schema_revision SMALLINT NOT NULL CHECK (
        origin_source_schema_revision = 1
    ),
    origin_source_record_digest CHAR(64) NOT NULL CHECK (
        agent_control.runtime_digest_valid(origin_source_record_digest::TEXT)
    ),
    origin_conversation_owner TEXT,
    origin_conversation_record_type TEXT,
    origin_conversation_record_id TEXT,
    origin_conversation_schema_revision SMALLINT,
    origin_conversation_record_digest CHAR(64),
    origin_initiating_principal_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(origin_initiating_principal_id)
    ),
    origin_initiating_kind TEXT NOT NULL CHECK (
        origin_initiating_kind IN ('user', 'workload', 'kernel')
    ),
    origin_initiating_audience TEXT NOT NULL CHECK (
        origin_initiating_audience IN (
            'control_api', 'worker', 'research_gateway', 'grace_intake',
            'grace_engine', 'delegation_engine', 'validator', 'activator',
            'kernel', 'kernel_admin'
        )
    ),
    origin_owner_policy_owner TEXT NOT NULL CHECK (
        origin_owner_policy_owner = 'platform_governance'
    ),
    origin_owner_policy_record_type TEXT NOT NULL CHECK (
        origin_owner_policy_record_type = 'owner_policy_revision'
    ),
    origin_owner_policy_record_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(origin_owner_policy_record_id)
    ),
    origin_owner_policy_schema_revision SMALLINT NOT NULL CHECK (
        origin_owner_policy_schema_revision = 1
    ),
    origin_owner_policy_record_digest CHAR(64) NOT NULL CHECK (
        agent_control.runtime_digest_valid(origin_owner_policy_record_digest::TEXT)
    ),
    origin_owner_policy_generation BIGINT NOT NULL CHECK (
        origin_owner_policy_generation > 0
    ),
    origin_occurred_at TIMESTAMPTZ NOT NULL,
    origin_observed_at TIMESTAMPTZ NOT NULL,
    origin_committed_at TIMESTAMPTZ NOT NULL,
    recovery_original_causation_id TEXT,
    recovery_original_idempotency_key TEXT,
    recovery_authority_owner TEXT,
    recovery_authority_record_type TEXT,
    recovery_authority_record_id TEXT,
    recovery_authority_schema_revision SMALLINT,
    recovery_authority_record_digest CHAR(64),
    recovery_effect_owner TEXT,
    recovery_effect_record_type TEXT,
    recovery_effect_record_id TEXT,
    recovery_effect_schema_revision SMALLINT,
    recovery_effect_record_digest CHAR(64),
    runtime_policy_owner TEXT NOT NULL CHECK (runtime_policy_owner = 'agent_control'),
    runtime_policy_record_type TEXT NOT NULL CHECK (
        runtime_policy_record_type = 'runtime_policy'
    ),
    runtime_policy_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(runtime_policy_id)
    ),
    runtime_policy_schema_revision SMALLINT NOT NULL CHECK (
        runtime_policy_schema_revision = 1
    ),
    runtime_policy_generation BIGINT NOT NULL CHECK (runtime_policy_generation > 0),
    runtime_policy_digest CHAR(64) NOT NULL CHECK (
        agent_control.runtime_digest_valid(runtime_policy_digest::TEXT)
    ),
    budget_ledger_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(budget_ledger_id)
    ),
    root_task_id TEXT NOT NULL CHECK (agent_control.runtime_identifier_valid(root_task_id)),
    state TEXT NOT NULL CHECK (state IN (
        'queued', 'running', 'waiting', 'canceling', 'succeeded', 'failed',
        'canceled', 'superseded', 'dead_lettered'
    )),
    state_generation BIGINT NOT NULL CHECK (state_generation > 0),
    superseded_by TEXT,
    failure JSONB CHECK (
        failure IS NULL OR agent_control.runtime_failure_valid(failure)
    ),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    deadline_at TIMESTAMPTZ NOT NULL,
    terminal_at TIMESTAMPTZ,
    CHECK (origin_occurred_at <= origin_observed_at
        AND origin_observed_at <= origin_committed_at
        AND origin_committed_at <= created_at),
    CHECK (created_at <= updated_at AND created_at < deadline_at),
    CHECK (COALESCE(
        (origin_kind = 'user_request'
            AND occurrence_owner IS NULL
            AND occurrence_record_type IS NULL
            AND occurrence_id IS NULL
            AND occurrence_schema_revision IS NULL
            AND occurrence_digest IS NULL
            AND origin_conversation_owner IS NOT NULL
            AND origin_conversation_owner = 'agent_control'
            AND origin_conversation_record_type IS NOT NULL
            AND origin_conversation_record_type = 'conversation'
            AND origin_conversation_record_id IS NOT NULL
            AND agent_control.runtime_identifier_valid(origin_conversation_record_id)
            AND origin_conversation_schema_revision IS NOT NULL
            AND origin_conversation_schema_revision = 1
            AND origin_conversation_record_digest IS NOT NULL
            AND agent_control.runtime_digest_valid(
                origin_conversation_record_digest::TEXT
            ))
        OR
        (origin_kind <> 'user_request'
            AND occurrence_owner IS NOT NULL
            AND occurrence_owner = 'agent_control'
            AND occurrence_record_type IS NOT NULL
            AND occurrence_record_type = 'trigger_occurrence'
            AND occurrence_id IS NOT NULL
            AND agent_control.runtime_identifier_valid(occurrence_id)
            AND occurrence_schema_revision IS NOT NULL
            AND occurrence_schema_revision = 1
            AND occurrence_digest IS NOT NULL
            AND agent_control.runtime_digest_valid(occurrence_digest::TEXT)
            AND origin_conversation_owner IS NULL
            AND origin_conversation_record_type IS NULL
            AND origin_conversation_record_id IS NULL
            AND origin_conversation_schema_revision IS NULL
            AND origin_conversation_record_digest IS NULL)
    , false)),
    CHECK (CASE origin_kind
        WHEN 'user_request' THEN origin_source_owner = 'agent_control'
            AND origin_source_record_type = 'user_request'
            AND origin_initiating_kind = 'user'
            AND origin_initiating_audience = 'control_api'
        WHEN 'schedule' THEN origin_source_owner = 'agent_control'
            AND origin_source_record_type = 'schedule_occurrence'
            AND origin_initiating_kind = 'workload'
            AND origin_initiating_audience = 'control_api'
        WHEN 'kernel_event' THEN origin_source_owner = 'kernel'
            AND origin_source_record_type = 'kernel_event'
            AND origin_initiating_kind = 'kernel'
            AND origin_initiating_audience = 'kernel'
        WHEN 'external_event' THEN origin_source_owner = 'agent_control'
            AND origin_source_record_type = 'external_event'
            AND origin_initiating_kind = 'workload'
            AND origin_initiating_audience = 'control_api'
        WHEN 'system_maintenance' THEN origin_source_owner = 'agent_control'
            AND origin_source_record_type = 'maintenance_occurrence'
            AND origin_initiating_kind = 'workload'
            AND origin_initiating_audience = 'control_api'
        WHEN 'system_recovery' THEN origin_source_owner = 'agent_control'
            AND origin_source_record_type = 'recovery_occurrence'
            AND origin_initiating_kind = 'workload'
            AND origin_initiating_audience = 'control_api'
        ELSE false END),
    CHECK (COALESCE(
        (origin_kind = 'system_recovery'
            AND recovery_original_causation_id IS NOT NULL
            AND agent_control.runtime_identifier_valid(recovery_original_causation_id)
            AND recovery_original_idempotency_key IS NOT NULL
            AND agent_control.runtime_identifier_valid(recovery_original_idempotency_key)
            AND recovery_authority_owner IS NOT NULL
            AND recovery_authority_owner IN (
                'agent_control', 'worker', 'platform_governance', 'blob',
                'research_gateway', 'grace', 'delegation', 'kernel'
            )
            AND recovery_authority_record_type IS NOT NULL
            AND agent_control.runtime_name_valid(recovery_authority_record_type)
            AND recovery_authority_record_id IS NOT NULL
            AND agent_control.runtime_identifier_valid(recovery_authority_record_id)
            AND recovery_authority_schema_revision IS NOT NULL
            AND recovery_authority_schema_revision = 1
            AND recovery_authority_record_digest IS NOT NULL
            AND agent_control.runtime_digest_valid(recovery_authority_record_digest::TEXT)
            AND recovery_effect_owner IS NOT NULL
            AND recovery_effect_owner IN (
                'agent_control', 'worker', 'platform_governance', 'blob',
                'research_gateway', 'grace', 'delegation', 'kernel'
            )
            AND recovery_effect_record_type IS NOT NULL
            AND agent_control.runtime_name_valid(recovery_effect_record_type)
            AND recovery_effect_record_id IS NOT NULL
            AND agent_control.runtime_identifier_valid(recovery_effect_record_id)
            AND recovery_effect_schema_revision IS NOT NULL
            AND recovery_effect_schema_revision = 1
            AND recovery_effect_record_digest IS NOT NULL
            AND agent_control.runtime_digest_valid(recovery_effect_record_digest::TEXT))
        OR
        (origin_kind <> 'system_recovery'
            AND recovery_original_causation_id IS NULL
            AND recovery_original_idempotency_key IS NULL
            AND recovery_authority_owner IS NULL
            AND recovery_authority_record_type IS NULL
            AND recovery_authority_record_id IS NULL
            AND recovery_authority_schema_revision IS NULL
            AND recovery_authority_record_digest IS NULL
            AND recovery_effect_owner IS NULL
            AND recovery_effect_record_type IS NULL
            AND recovery_effect_record_id IS NULL
            AND recovery_effect_schema_revision IS NULL
            AND recovery_effect_record_digest IS NULL)
    , false)),
    CHECK (
        (state = 'superseded' AND agent_control.runtime_identifier_valid(superseded_by)
            AND superseded_by <> run_id)
        OR (state <> 'superseded' AND superseded_by IS NULL)
    ),
    CHECK (
        (state IN ('failed', 'dead_lettered') AND failure IS NOT NULL)
        OR (state NOT IN ('failed', 'dead_lettered') AND failure IS NULL)
    ),
    CHECK (
        (state IN ('succeeded', 'failed', 'canceled', 'superseded', 'dead_lettered')
            AND terminal_at IS NOT NULL
            AND terminal_at BETWEEN created_at AND updated_at)
        OR
        (state NOT IN ('succeeded', 'failed', 'canceled', 'superseded', 'dead_lettered')
            AND terminal_at IS NULL)
    ),
    FOREIGN KEY (occurrence_id, occurrence_digest)
        REFERENCES agent_control.trigger_occurrence(occurrence_id, record_digest)
        DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (runtime_policy_id, runtime_policy_generation, runtime_policy_digest)
        REFERENCES agent_control.runtime_policy_revision(policy_id, generation, record_digest)
        DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (
        origin_owner_policy_record_id, origin_owner_policy_generation,
        origin_owner_policy_record_digest
    ) REFERENCES platform_governance.owner_policy_revision(
        revision_id, generation, record_digest
    )
        DEFERRABLE INITIALLY DEFERRED
);

CREATE TRIGGER runtime_run_guard
BEFORE UPDATE OR DELETE ON agent_control.runtime_run
FOR EACH ROW EXECUTE FUNCTION agent_control.guard_runtime_mutable_columns(
    'state', 'state_generation', 'superseded_by', 'failure', 'updated_at', 'terminal_at'
);

CREATE TRIGGER runtime_run_initial_guard
BEFORE INSERT ON agent_control.runtime_run
FOR EACH ROW EXECUTE FUNCTION agent_control.guard_runtime_initial_insert(
    'run', 'state_generation'
);

CREATE TRIGGER runtime_run_state_guard
BEFORE UPDATE ON agent_control.runtime_run
FOR EACH ROW EXECUTE FUNCTION agent_control.guard_runtime_state_transition(
    'run', 'state_generation'
);

CREATE UNIQUE INDEX runtime_run_one_per_occurrence_idx
ON agent_control.runtime_run (occurrence_id)
WHERE occurrence_id IS NOT NULL;

CREATE FUNCTION agent_control.validate_trigger_occurrence_binding()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
          FROM platform_governance.owner_policy_revision AS policy
         WHERE policy.revision_id = NEW.owner_policy_record_id
           AND policy.generation = NEW.owner_policy_generation
           AND policy.record_digest = NEW.owner_policy_record_digest
    ) THEN
        RAISE EXCEPTION USING ERRCODE = '23503',
            MESSAGE = 'trigger occurrence owner policy does not exist';
    END IF;

    IF NEW.kind <> 'system_recovery' AND NOT EXISTS (
        SELECT 1
          FROM platform_governance.owner_policy_revision AS policy
          JOIN agent_control.trigger_registration_revision AS registration
            ON registration.registration_id = NEW.registration_id
           AND registration.generation = NEW.registration_generation
           AND registration.record_digest = NEW.registration_digest
         WHERE policy.revision_id = NEW.owner_policy_record_id
           AND policy.generation = NEW.owner_policy_generation
           AND policy.record_digest = NEW.owner_policy_record_digest
           AND policy.origin_kind = NEW.kind
           AND policy.source_owner = NEW.source_owner
           AND policy.source_record_type = NEW.source_record_type
           AND policy.initiating_kind = NEW.initiating_kind
           AND policy.initiating_audience = NEW.initiating_audience
           AND (policy.initiating_principal_id IS NULL
                OR policy.initiating_principal_id = NEW.initiating_principal_id)
           AND registration.kind = NEW.kind
           AND registration.owner_policy_owner = NEW.owner_policy_owner
           AND registration.owner_policy_record_type = NEW.owner_policy_record_type
           AND registration.owner_policy_record_id = NEW.owner_policy_record_id
           AND registration.owner_policy_schema_revision = NEW.owner_policy_schema_revision
           AND registration.owner_policy_record_digest = NEW.owner_policy_record_digest
           AND registration.owner_policy_generation = NEW.owner_policy_generation
    ) THEN
        RAISE EXCEPTION USING ERRCODE = '23514',
            MESSAGE = 'trigger occurrence does not exactly match registration and owner policy';
    END IF;
    RETURN NULL;
END
$$;

CREATE CONSTRAINT TRIGGER trigger_occurrence_binding_guard
AFTER INSERT ON agent_control.trigger_occurrence
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION agent_control.validate_trigger_occurrence_binding();

CREATE FUNCTION agent_control.validate_runtime_run_binding()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
          FROM agent_control.runtime_policy_revision AS runtime_policy
         WHERE runtime_policy.policy_id = NEW.runtime_policy_id
           AND runtime_policy.generation = NEW.runtime_policy_generation
           AND runtime_policy.record_digest = NEW.runtime_policy_digest
    ) THEN
        RAISE EXCEPTION USING ERRCODE = '23503',
            MESSAGE = 'run runtime policy does not exist';
    END IF;

    IF NEW.origin_kind <> 'system_recovery' AND NOT EXISTS (
        SELECT 1
          FROM platform_governance.owner_policy_revision AS policy
         WHERE policy.revision_id = NEW.origin_owner_policy_record_id
           AND policy.generation = NEW.origin_owner_policy_generation
           AND policy.record_digest = NEW.origin_owner_policy_record_digest
           AND policy.origin_kind = NEW.origin_kind
           AND policy.source_owner = NEW.origin_source_owner
           AND policy.source_record_type = NEW.origin_source_record_type
           AND policy.initiating_kind = NEW.origin_initiating_kind
           AND policy.initiating_audience = NEW.origin_initiating_audience
           AND (policy.initiating_principal_id IS NULL
                OR policy.initiating_principal_id = NEW.origin_initiating_principal_id)
    ) THEN
        RAISE EXCEPTION USING ERRCODE = '23514',
            MESSAGE = 'run origin does not match owner policy';
    END IF;

    IF NEW.origin_kind <> 'user_request' AND NOT EXISTS (
        SELECT 1
          FROM agent_control.trigger_occurrence AS occurrence
         WHERE occurrence.occurrence_id = NEW.occurrence_id
           AND occurrence.record_digest = NEW.occurrence_digest
           AND occurrence.kind = NEW.origin_kind
           AND occurrence.source_owner = NEW.origin_source_owner
           AND occurrence.source_record_type = NEW.origin_source_record_type
           AND occurrence.source_record_id = NEW.origin_source_record_id
           AND occurrence.source_schema_revision = NEW.origin_source_schema_revision
           AND occurrence.source_record_digest = NEW.origin_source_record_digest
           AND occurrence.initiating_principal_id = NEW.origin_initiating_principal_id
           AND occurrence.initiating_kind = NEW.origin_initiating_kind
           AND occurrence.initiating_audience = NEW.origin_initiating_audience
           AND occurrence.owner_policy_owner = NEW.origin_owner_policy_owner
           AND occurrence.owner_policy_record_type = NEW.origin_owner_policy_record_type
           AND occurrence.owner_policy_record_id = NEW.origin_owner_policy_record_id
           AND occurrence.owner_policy_schema_revision = NEW.origin_owner_policy_schema_revision
           AND occurrence.owner_policy_record_digest = NEW.origin_owner_policy_record_digest
           AND occurrence.owner_policy_generation = NEW.origin_owner_policy_generation
           AND occurrence.occurred_at = NEW.origin_occurred_at
           AND occurrence.observed_at = NEW.origin_observed_at
           AND occurrence.committed_at = NEW.origin_committed_at
    ) THEN
        RAISE EXCEPTION USING ERRCODE = '23514',
            MESSAGE = 'run origin does not exactly match trigger occurrence';
    END IF;

    IF NEW.origin_kind NOT IN ('user_request', 'system_recovery') AND NOT EXISTS (
        SELECT 1
          FROM agent_control.trigger_occurrence AS occurrence
          JOIN agent_control.trigger_registration_revision AS registration
            ON registration.registration_id = occurrence.registration_id
           AND registration.generation = occurrence.registration_generation
           AND registration.record_digest = occurrence.registration_digest
         WHERE occurrence.occurrence_id = NEW.occurrence_id
           AND registration.runtime_policy_owner = NEW.runtime_policy_owner
           AND registration.runtime_policy_record_type = NEW.runtime_policy_record_type
           AND registration.runtime_policy_record_id = NEW.runtime_policy_id
           AND registration.runtime_policy_schema_revision = NEW.runtime_policy_schema_revision
           AND registration.runtime_policy_generation = NEW.runtime_policy_generation
           AND registration.runtime_policy_record_digest = NEW.runtime_policy_digest
    ) THEN
        RAISE EXCEPTION USING ERRCODE = '23514',
            MESSAGE = 'run runtime policy does not match trigger registration';
    END IF;
    RETURN NULL;
END
$$;

CREATE CONSTRAINT TRIGGER runtime_run_binding_guard
AFTER INSERT ON agent_control.runtime_run
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION agent_control.validate_runtime_run_binding();

CREATE TABLE agent_control.runtime_budget_ledger (
    ledger_id TEXT PRIMARY KEY CHECK (agent_control.runtime_identifier_valid(ledger_id)),
    schema_revision SMALLINT NOT NULL CHECK (schema_revision = 1),
    scope TEXT NOT NULL CHECK (scope IN ('run', 'task')),
    scope_id TEXT NOT NULL CHECK (agent_control.runtime_identifier_valid(scope_id)),
    parent_ledger_id TEXT,
    runtime_policy_owner TEXT NOT NULL CHECK (runtime_policy_owner = 'agent_control'),
    runtime_policy_record_type TEXT NOT NULL CHECK (
        runtime_policy_record_type = 'runtime_policy'
    ),
    runtime_policy_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(runtime_policy_id)
    ),
    runtime_policy_schema_revision SMALLINT NOT NULL CHECK (
        runtime_policy_schema_revision = 1
    ),
    runtime_policy_generation BIGINT NOT NULL CHECK (runtime_policy_generation > 0),
    runtime_policy_digest CHAR(64) NOT NULL CHECK (
        agent_control.runtime_digest_valid(runtime_policy_digest::TEXT)
    ),

    limit_model_calls BIGINT NOT NULL CHECK (limit_model_calls >= 0),
    limit_input_tokens BIGINT NOT NULL CHECK (limit_input_tokens >= 0),
    limit_output_tokens BIGINT NOT NULL CHECK (limit_output_tokens >= 0),
    limit_tool_calls BIGINT NOT NULL CHECK (limit_tool_calls >= 0),
    limit_external_cost_micro_usd BIGINT NOT NULL CHECK (limit_external_cost_micro_usd >= 0),
    limit_wall_time_ms BIGINT NOT NULL CHECK (limit_wall_time_ms > 0),
    limit_idle_time_ms BIGINT NOT NULL CHECK (limit_idle_time_ms >= 0),
    limit_tasks BIGINT NOT NULL CHECK (limit_tasks >= 0),
    limit_depth BIGINT NOT NULL CHECK (limit_depth >= 0),
    limit_fanout BIGINT NOT NULL CHECK (limit_fanout >= 0),
    limit_parallelism BIGINT NOT NULL CHECK (limit_parallelism > 0),
    limit_invalid_output_retries BIGINT NOT NULL CHECK (limit_invalid_output_retries >= 0),
    limit_infrastructure_retries BIGINT NOT NULL CHECK (limit_infrastructure_retries >= 0),

    consumed_model_calls BIGINT NOT NULL DEFAULT 0 CHECK (consumed_model_calls >= 0),
    consumed_input_tokens BIGINT NOT NULL DEFAULT 0 CHECK (consumed_input_tokens >= 0),
    consumed_output_tokens BIGINT NOT NULL DEFAULT 0 CHECK (consumed_output_tokens >= 0),
    consumed_tool_calls BIGINT NOT NULL DEFAULT 0 CHECK (consumed_tool_calls >= 0),
    consumed_external_cost_micro_usd BIGINT NOT NULL DEFAULT 0 CHECK (consumed_external_cost_micro_usd >= 0),
    consumed_wall_time_ms BIGINT NOT NULL DEFAULT 0 CHECK (consumed_wall_time_ms >= 0),
    consumed_tasks BIGINT NOT NULL DEFAULT 0 CHECK (consumed_tasks >= 0),
    consumed_active_tasks BIGINT NOT NULL DEFAULT 0 CHECK (consumed_active_tasks >= 0),
    consumed_invalid_output_retries BIGINT NOT NULL DEFAULT 0 CHECK (consumed_invalid_output_retries >= 0),
    consumed_infrastructure_retries BIGINT NOT NULL DEFAULT 0 CHECK (consumed_infrastructure_retries >= 0),

    reserved_model_calls BIGINT NOT NULL DEFAULT 0 CHECK (reserved_model_calls >= 0),
    reserved_input_tokens BIGINT NOT NULL DEFAULT 0 CHECK (reserved_input_tokens >= 0),
    reserved_output_tokens BIGINT NOT NULL DEFAULT 0 CHECK (reserved_output_tokens >= 0),
    reserved_tool_calls BIGINT NOT NULL DEFAULT 0 CHECK (reserved_tool_calls >= 0),
    reserved_external_cost_micro_usd BIGINT NOT NULL DEFAULT 0 CHECK (reserved_external_cost_micro_usd >= 0),
    reserved_wall_time_ms BIGINT NOT NULL DEFAULT 0 CHECK (reserved_wall_time_ms >= 0),
    reserved_tasks BIGINT NOT NULL DEFAULT 0 CHECK (reserved_tasks >= 0),
    reserved_active_tasks BIGINT NOT NULL DEFAULT 0 CHECK (reserved_active_tasks >= 0),
    reserved_invalid_output_retries BIGINT NOT NULL DEFAULT 0 CHECK (reserved_invalid_output_retries >= 0),
    reserved_infrastructure_retries BIGINT NOT NULL DEFAULT 0 CHECK (reserved_infrastructure_retries >= 0),

    generation BIGINT NOT NULL CHECK (generation > 0),
    state TEXT NOT NULL CHECK (state IN ('open', 'exhausted', 'closed')),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (scope, scope_id),
    CHECK (
        (scope = 'run' AND parent_ledger_id IS NULL)
        OR (scope = 'task' AND agent_control.runtime_identifier_valid(parent_ledger_id))
    ),
    CHECK (consumed_active_tasks <= consumed_tasks),
    CHECK (reserved_active_tasks <= reserved_tasks),
    -- Every capacity check uses subtraction after nonnegative bounds.  This is
    -- both fail-closed and immune to BIGINT addition overflow.
    CHECK (consumed_model_calls <= limit_model_calls
        AND reserved_model_calls <= limit_model_calls - consumed_model_calls),
    CHECK (consumed_input_tokens <= limit_input_tokens
        AND reserved_input_tokens <= limit_input_tokens - consumed_input_tokens),
    CHECK (consumed_output_tokens <= limit_output_tokens
        AND reserved_output_tokens <= limit_output_tokens - consumed_output_tokens),
    CHECK (consumed_tool_calls <= limit_tool_calls
        AND reserved_tool_calls <= limit_tool_calls - consumed_tool_calls),
    CHECK (consumed_external_cost_micro_usd <= limit_external_cost_micro_usd
        AND reserved_external_cost_micro_usd <= limit_external_cost_micro_usd - consumed_external_cost_micro_usd),
    CHECK (consumed_wall_time_ms <= limit_wall_time_ms
        AND reserved_wall_time_ms <= limit_wall_time_ms - consumed_wall_time_ms),
    CHECK (consumed_tasks <= limit_tasks
        AND reserved_tasks <= limit_tasks - consumed_tasks),
    CHECK (consumed_active_tasks <= limit_parallelism
        AND reserved_active_tasks <= limit_parallelism - consumed_active_tasks),
    CHECK (consumed_invalid_output_retries <= limit_invalid_output_retries
        AND reserved_invalid_output_retries <= limit_invalid_output_retries - consumed_invalid_output_retries),
    CHECK (consumed_infrastructure_retries <= limit_infrastructure_retries
        AND reserved_infrastructure_retries <= limit_infrastructure_retries - consumed_infrastructure_retries),
    FOREIGN KEY (parent_ledger_id)
        REFERENCES agent_control.runtime_budget_ledger(ledger_id)
        DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (runtime_policy_id, runtime_policy_generation, runtime_policy_digest)
        REFERENCES agent_control.runtime_policy_revision(policy_id, generation, record_digest)
        DEFERRABLE INITIALLY DEFERRED
);

CREATE TRIGGER runtime_budget_guard
BEFORE UPDATE OR DELETE ON agent_control.runtime_budget_ledger
FOR EACH ROW EXECUTE FUNCTION agent_control.guard_runtime_mutable_columns(
    'consumed_model_calls', 'consumed_input_tokens', 'consumed_output_tokens',
    'consumed_tool_calls', 'consumed_external_cost_micro_usd', 'consumed_wall_time_ms',
    'consumed_tasks', 'consumed_active_tasks', 'consumed_invalid_output_retries',
    'consumed_infrastructure_retries', 'reserved_model_calls', 'reserved_input_tokens',
    'reserved_output_tokens', 'reserved_tool_calls', 'reserved_external_cost_micro_usd',
    'reserved_wall_time_ms', 'reserved_tasks', 'reserved_active_tasks',
    'reserved_invalid_output_retries', 'reserved_infrastructure_retries',
    'generation', 'state', 'updated_at'
);

CREATE TRIGGER runtime_budget_initial_guard
BEFORE INSERT ON agent_control.runtime_budget_ledger
FOR EACH ROW EXECUTE FUNCTION agent_control.guard_runtime_initial_insert(
    'budget', 'generation'
);

CREATE FUNCTION agent_control.guard_runtime_budget_update()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.generation <> OLD.generation + 1 OR NEW.updated_at < OLD.updated_at THEN
        RAISE EXCEPTION USING ERRCODE = '40001', MESSAGE = 'invalid budget generation';
    END IF;
    IF NEW.consumed_model_calls < OLD.consumed_model_calls
       OR NEW.consumed_input_tokens < OLD.consumed_input_tokens
       OR NEW.consumed_output_tokens < OLD.consumed_output_tokens
       OR NEW.consumed_tool_calls < OLD.consumed_tool_calls
       OR NEW.consumed_external_cost_micro_usd < OLD.consumed_external_cost_micro_usd
       OR NEW.consumed_wall_time_ms < OLD.consumed_wall_time_ms
       OR NEW.consumed_tasks < OLD.consumed_tasks
       OR NEW.consumed_invalid_output_retries < OLD.consumed_invalid_output_retries
       OR NEW.consumed_infrastructure_retries < OLD.consumed_infrastructure_retries THEN
        RAISE EXCEPTION USING ERRCODE = '23514',
            MESSAGE = 'cumulative budget consumption cannot decrease';
    END IF;
    IF NEW.state IS DISTINCT FROM OLD.state
       AND NOT agent_control.runtime_transition_allowed('budget', OLD.state, NEW.state) THEN
        RAISE EXCEPTION USING ERRCODE = '40001', MESSAGE = 'invalid budget state transition';
    END IF;
    IF OLD.state = 'closed' THEN
        RAISE EXCEPTION USING ERRCODE = '55000', MESSAGE = 'closed budget is immutable';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER runtime_budget_generation_guard
BEFORE UPDATE ON agent_control.runtime_budget_ledger
FOR EACH ROW EXECUTE FUNCTION agent_control.guard_runtime_budget_update();

CREATE TABLE agent_control.runtime_task (
    task_id TEXT PRIMARY KEY CHECK (agent_control.runtime_identifier_valid(task_id)),
    schema_revision SMALLINT NOT NULL CHECK (schema_revision = 1),
    run_id TEXT NOT NULL CHECK (agent_control.runtime_identifier_valid(run_id)),
    parent_task_id TEXT,
    depth BIGINT NOT NULL CHECK (depth >= 0),
    objective JSONB NOT NULL CHECK (
        agent_control.runtime_blob_ref_valid(objective, 'task_objective', '')
    ),
    output_contract_owner TEXT NOT NULL CHECK (
        output_contract_owner = 'agent_control'
    ),
    output_contract_record_type TEXT NOT NULL CHECK (
        output_contract_record_type = 'output_contract_revision'
    ),
    output_contract_revision_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(output_contract_revision_id)
    ),
    output_contract_schema_revision SMALLINT NOT NULL CHECK (
        output_contract_schema_revision = 1
    ),
    output_contract_generation BIGINT NOT NULL CHECK (output_contract_generation > 0),
    output_contract_digest CHAR(64) NOT NULL CHECK (
        agent_control.runtime_digest_valid(output_contract_digest::TEXT)
    ),
    budget_ledger_id TEXT NOT NULL UNIQUE CHECK (
        agent_control.runtime_identifier_valid(budget_ledger_id)
    ),
    session_id TEXT,
    result_artifact_id TEXT,
    state TEXT NOT NULL CHECK (state IN (
        'blocked', 'ready', 'running', 'waiting', 'result_committed',
        'succeeded', 'failed', 'canceled', 'superseded', 'dead_lettered'
    )),
    state_generation BIGINT NOT NULL CHECK (state_generation > 0),
    budget_slot_held BOOLEAN NOT NULL DEFAULT false,
    failure JSONB CHECK (
        failure IS NULL OR agent_control.runtime_failure_valid(failure)
    ),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    deadline_at TIMESTAMPTZ NOT NULL,
    terminal_at TIMESTAMPTZ,
    UNIQUE (task_id, run_id),
    CHECK (created_at <= updated_at AND created_at < deadline_at),
    CHECK ((objective->>'committed_at')::TIMESTAMPTZ <= created_at),
    CHECK (
        (depth = 0 AND parent_task_id IS NULL)
        OR (depth > 0 AND agent_control.runtime_identifier_valid(parent_task_id)
            AND parent_task_id <> task_id)
    ),
    CHECK (session_id IS NULL OR agent_control.runtime_identifier_valid(session_id)),
    CHECK (result_artifact_id IS NULL OR agent_control.runtime_identifier_valid(result_artifact_id)),
    CHECK (
        (state IN ('running', 'result_committed', 'succeeded')
            AND session_id IS NOT NULL)
        OR state NOT IN ('running', 'result_committed', 'succeeded')
    ),
    CHECK (
        (state IN ('running', 'waiting', 'result_committed') AND budget_slot_held)
        OR state = 'ready'
        OR (state IN (
                'blocked', 'succeeded', 'failed', 'canceled', 'superseded',
                'dead_lettered'
            ) AND NOT budget_slot_held)
    ),
    CHECK (
        (state IN ('result_committed', 'succeeded')
            AND result_artifact_id IS NOT NULL
            AND agent_control.runtime_identifier_valid(result_artifact_id))
        OR state NOT IN ('result_committed', 'succeeded')
    ),
    CHECK (
        (state IN ('failed', 'dead_lettered') AND failure IS NOT NULL)
        OR (state NOT IN ('failed', 'dead_lettered') AND failure IS NULL)
    ),
    CHECK (
        (state IN ('succeeded', 'failed', 'canceled', 'superseded', 'dead_lettered')
            AND terminal_at IS NOT NULL
            AND terminal_at BETWEEN created_at AND updated_at)
        OR
        (state NOT IN ('succeeded', 'failed', 'canceled', 'superseded', 'dead_lettered')
            AND terminal_at IS NULL)
    ),
    FOREIGN KEY (run_id) REFERENCES agent_control.runtime_run(run_id)
        DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (parent_task_id, run_id)
        REFERENCES agent_control.runtime_task(task_id, run_id)
        DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (budget_ledger_id)
        REFERENCES agent_control.runtime_budget_ledger(ledger_id)
        DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (
        output_contract_revision_id, output_contract_generation, output_contract_digest
    ) REFERENCES agent_control.output_contract_revision(
        revision_id, generation, record_digest
    ) DEFERRABLE INITIALLY DEFERRED
);

CREATE INDEX runtime_task_claim_idx
ON agent_control.runtime_task (state, deadline_at, created_at, task_id)
WHERE state IN ('ready', 'waiting');

CREATE TRIGGER runtime_task_guard
BEFORE UPDATE OR DELETE ON agent_control.runtime_task
FOR EACH ROW EXECUTE FUNCTION agent_control.guard_runtime_mutable_columns(
    'session_id', 'result_artifact_id', 'state', 'state_generation',
    'budget_slot_held', 'failure', 'updated_at', 'terminal_at'
);

CREATE TRIGGER runtime_task_initial_guard
BEFORE INSERT ON agent_control.runtime_task
FOR EACH ROW EXECUTE FUNCTION agent_control.guard_runtime_initial_insert(
    'task', 'state_generation'
);

-- A Task's active-slot accounting is historical, not merely a projection of
-- its current state.  New work never arrives holding a slot.  The sole acquire
-- edge is ready -> running; after acquisition, waiting -> ready retries keep
-- the slot until a terminal transition releases it.
CREATE FUNCTION agent_control.guard_runtime_task_budget_slot()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.budget_slot_held THEN
            RAISE EXCEPTION USING ERRCODE = '23514',
                MESSAGE = 'initial Task cannot hold a budget slot';
        END IF;
        RETURN NEW;
    END IF;

    IF NOT OLD.budget_slot_held AND NEW.budget_slot_held
       AND NOT (OLD.state = 'ready' AND NEW.state = 'running') THEN
        RAISE EXCEPTION USING ERRCODE = '40001',
            MESSAGE = 'Task budget slot may only be acquired on ready to running';
    END IF;
    IF OLD.budget_slot_held AND NOT NEW.budget_slot_held
       AND NOT agent_control.runtime_terminal_state('task', NEW.state) THEN
        RAISE EXCEPTION USING ERRCODE = '40001',
            MESSAGE = 'Task budget slot must remain held until terminal';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER runtime_task_budget_slot_guard
BEFORE INSERT OR UPDATE ON agent_control.runtime_task
FOR EACH ROW EXECUTE FUNCTION agent_control.guard_runtime_task_budget_slot();

CREATE TRIGGER runtime_task_state_guard
BEFORE UPDATE ON agent_control.runtime_task
FOR EACH ROW EXECUTE FUNCTION agent_control.guard_runtime_state_transition(
    'task', 'state_generation'
);

CREATE FUNCTION agent_control.validate_runtime_budget_structure()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    target_id TEXT;
BEGIN
    IF TG_TABLE_NAME = 'runtime_run' THEN
        target_id := to_jsonb(NEW)->>'run_id';
        IF NOT EXISTS (
            SELECT 1
              FROM agent_control.runtime_run AS run
              JOIN agent_control.runtime_budget_ledger AS ledger
                ON ledger.ledger_id = run.budget_ledger_id
             WHERE run.run_id = target_id
               AND ledger.scope = 'run'
               AND ledger.scope_id = run.run_id
               AND ledger.parent_ledger_id IS NULL
               AND ledger.runtime_policy_owner = run.runtime_policy_owner
               AND ledger.runtime_policy_record_type = run.runtime_policy_record_type
               AND ledger.runtime_policy_id = run.runtime_policy_id
               AND ledger.runtime_policy_schema_revision = run.runtime_policy_schema_revision
               AND ledger.runtime_policy_generation = run.runtime_policy_generation
               AND ledger.runtime_policy_digest = run.runtime_policy_digest
        ) THEN
            RAISE EXCEPTION USING ERRCODE = '23514',
                MESSAGE = 'run budget ledger structure is invalid';
        END IF;
    ELSIF TG_TABLE_NAME = 'runtime_task' THEN
        target_id := to_jsonb(NEW)->>'task_id';
        IF NOT EXISTS (
            SELECT 1
              FROM agent_control.runtime_task AS task
              JOIN agent_control.runtime_run AS run ON run.run_id = task.run_id
              JOIN agent_control.runtime_budget_ledger AS ledger
                ON ledger.ledger_id = task.budget_ledger_id
              LEFT JOIN agent_control.runtime_task AS parent_task
                ON parent_task.task_id = task.parent_task_id
               AND parent_task.run_id = task.run_id
             WHERE task.task_id = target_id
               AND ledger.scope = 'task'
               AND ledger.scope_id = task.task_id
               AND (
                   (task.depth = 0
                       AND ledger.parent_ledger_id = run.budget_ledger_id)
                   OR
                   (task.depth > 0
                       AND parent_task.task_id IS NOT NULL
                       AND ledger.parent_ledger_id = parent_task.budget_ledger_id)
               )
               AND ledger.runtime_policy_owner = run.runtime_policy_owner
               AND ledger.runtime_policy_record_type = run.runtime_policy_record_type
               AND ledger.runtime_policy_id = run.runtime_policy_id
               AND ledger.runtime_policy_schema_revision = run.runtime_policy_schema_revision
               AND ledger.runtime_policy_generation = run.runtime_policy_generation
               AND ledger.runtime_policy_digest = run.runtime_policy_digest
        ) THEN
            RAISE EXCEPTION USING ERRCODE = '23514',
                MESSAGE = 'task budget ledger structure is invalid';
        END IF;
    ELSIF TG_TABLE_NAME = 'runtime_budget_ledger' THEN
        target_id := to_jsonb(NEW)->>'ledger_id';
        IF (to_jsonb(NEW)->>'scope') = 'run' THEN
            IF NOT EXISTS (
                SELECT 1
                  FROM agent_control.runtime_budget_ledger AS ledger
                  JOIN agent_control.runtime_run AS run
                    ON run.budget_ledger_id = ledger.ledger_id
                 WHERE ledger.ledger_id = target_id
                   AND run.run_id = ledger.scope_id
                   AND ledger.parent_ledger_id IS NULL
                   AND ledger.runtime_policy_owner = run.runtime_policy_owner
                   AND ledger.runtime_policy_record_type = run.runtime_policy_record_type
                   AND ledger.runtime_policy_id = run.runtime_policy_id
                   AND ledger.runtime_policy_schema_revision = run.runtime_policy_schema_revision
                   AND ledger.runtime_policy_generation = run.runtime_policy_generation
                   AND ledger.runtime_policy_digest = run.runtime_policy_digest
            ) THEN
                RAISE EXCEPTION USING ERRCODE = '23514',
                    MESSAGE = 'orphan or mismatched run budget ledger';
            END IF;
        ELSE
            IF NOT EXISTS (
                SELECT 1
                  FROM agent_control.runtime_budget_ledger AS ledger
                  JOIN agent_control.runtime_task AS task
                    ON task.budget_ledger_id = ledger.ledger_id
                  JOIN agent_control.runtime_run AS run ON run.run_id = task.run_id
                  LEFT JOIN agent_control.runtime_task AS parent_task
                    ON parent_task.task_id = task.parent_task_id
                   AND parent_task.run_id = task.run_id
                 WHERE ledger.ledger_id = target_id
                   AND task.task_id = ledger.scope_id
                   AND (
                       (task.depth = 0
                           AND ledger.parent_ledger_id = run.budget_ledger_id)
                       OR
                       (task.depth > 0
                           AND parent_task.task_id IS NOT NULL
                           AND ledger.parent_ledger_id = parent_task.budget_ledger_id)
                   )
                   AND ledger.runtime_policy_owner = run.runtime_policy_owner
                   AND ledger.runtime_policy_record_type = run.runtime_policy_record_type
                   AND ledger.runtime_policy_id = run.runtime_policy_id
                   AND ledger.runtime_policy_schema_revision = run.runtime_policy_schema_revision
                   AND ledger.runtime_policy_generation = run.runtime_policy_generation
                   AND ledger.runtime_policy_digest = run.runtime_policy_digest
            ) THEN
                RAISE EXCEPTION USING ERRCODE = '23514',
                    MESSAGE = 'orphan or mismatched task budget ledger';
            END IF;
        END IF;
    ELSE
        RAISE EXCEPTION USING ERRCODE = '55000',
            MESSAGE = 'budget structure trigger attached to unexpected table';
    END IF;
    RETURN NULL;
END
$$;

CREATE CONSTRAINT TRIGGER runtime_run_budget_structure_guard
AFTER INSERT ON agent_control.runtime_run
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION agent_control.validate_runtime_budget_structure();

CREATE CONSTRAINT TRIGGER runtime_task_budget_structure_guard
AFTER INSERT ON agent_control.runtime_task
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION agent_control.validate_runtime_budget_structure();

CREATE CONSTRAINT TRIGGER runtime_ledger_structure_guard
AFTER INSERT ON agent_control.runtime_budget_ledger
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION agent_control.validate_runtime_budget_structure();

CREATE TABLE agent_control.runtime_task_input_ref (
    task_id TEXT NOT NULL,
    ordinal INTEGER NOT NULL CHECK (ordinal BETWEEN 1 AND 4096),
    reference JSONB NOT NULL CHECK (
        agent_control.runtime_record_ref_valid(reference, '', '')
    ),
    PRIMARY KEY (task_id, ordinal),
    FOREIGN KEY (task_id) REFERENCES agent_control.runtime_task(task_id)
        DEFERRABLE INITIALLY DEFERRED
);

CREATE UNIQUE INDEX runtime_task_input_ref_identity_idx
ON agent_control.runtime_task_input_ref (
    task_id,
    (reference->>'owner'),
    (reference->>'record_type'),
    (reference->>'record_id'),
    ((reference->>'schema_revision')::SMALLINT)
);

CREATE TABLE agent_control.runtime_task_dependency (
    task_id TEXT NOT NULL,
    depends_on_task_id TEXT NOT NULL,
    run_id TEXT NOT NULL CHECK (agent_control.runtime_identifier_valid(run_id)),
    schema_revision SMALLINT NOT NULL CHECK (schema_revision = 1),
    requires_success BOOLEAN NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (task_id, depends_on_task_id),
    CHECK (task_id <> depends_on_task_id),
    FOREIGN KEY (task_id, run_id)
        REFERENCES agent_control.runtime_task(task_id, run_id)
        DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (depends_on_task_id, run_id)
        REFERENCES agent_control.runtime_task(task_id, run_id)
        DEFERRABLE INITIALLY DEFERRED
);

CREATE TABLE agent_control.runtime_session (
    session_id TEXT PRIMARY KEY CHECK (agent_control.runtime_identifier_valid(session_id)),
    schema_revision SMALLINT NOT NULL CHECK (schema_revision = 1),
    run_id TEXT NOT NULL,
    task_id TEXT NOT NULL,
    generation BIGINT NOT NULL CHECK (generation > 0),
    execution_binding JSONB NOT NULL CHECK (
        agent_control.runtime_blob_ref_valid(execution_binding, 'execution_binding', '')
    ),
    context_manifest JSONB NOT NULL CHECK (
        agent_control.runtime_blob_ref_valid(context_manifest, 'context_manifest', '')
    ),
    latest_checkpoint_id TEXT,
    state TEXT NOT NULL CHECK (state IN ('open', 'closed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    closed_at TIMESTAMPTZ,
    UNIQUE (session_id, run_id, task_id),
    UNIQUE (task_id, generation),
    CHECK ((execution_binding->>'committed_at')::TIMESTAMPTZ <= created_at),
    CHECK ((context_manifest->>'committed_at')::TIMESTAMPTZ <= created_at),
    CHECK (latest_checkpoint_id IS NULL
        OR agent_control.runtime_identifier_valid(latest_checkpoint_id)),
    CHECK (
        (state = 'open' AND closed_at IS NULL)
        OR (state = 'closed' AND closed_at IS NOT NULL AND closed_at >= created_at)
    ),
    FOREIGN KEY (task_id, run_id)
        REFERENCES agent_control.runtime_task(task_id, run_id)
        DEFERRABLE INITIALLY DEFERRED
);

CREATE UNIQUE INDEX runtime_session_one_open_per_task_idx
ON agent_control.runtime_session (task_id)
WHERE state = 'open';

CREATE TRIGGER runtime_session_guard
BEFORE UPDATE OR DELETE ON agent_control.runtime_session
FOR EACH ROW EXECUTE FUNCTION agent_control.guard_runtime_mutable_columns(
    'generation', 'latest_checkpoint_id', 'state', 'closed_at'
);

CREATE TRIGGER runtime_session_initial_guard
BEFORE INSERT ON agent_control.runtime_session
FOR EACH ROW EXECUTE FUNCTION agent_control.guard_runtime_initial_insert(
    'session', 'generation'
);

CREATE TRIGGER runtime_session_state_guard
BEFORE UPDATE ON agent_control.runtime_session
FOR EACH ROW EXECUTE FUNCTION agent_control.guard_runtime_state_transition(
    'session', 'generation'
);

CREATE TRIGGER runtime_task_input_ref_immutable
BEFORE UPDATE OR DELETE ON agent_control.runtime_task_input_ref
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();

CREATE TRIGGER runtime_task_dependency_immutable
BEFORE UPDATE OR DELETE ON agent_control.runtime_task_dependency
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();

CREATE TRIGGER trigger_occurrence_immutable
BEFORE UPDATE OR DELETE ON agent_control.trigger_occurrence
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();

-- Lease identity is a generation plus a database-generated UUID token. Direct
-- table reads are denied; 0006 compares the exact token only inside fenced
-- SECURITY DEFINER transactions, matching AP0's delivery-lease convention.
CREATE TABLE agent_control.runtime_attempt (
    attempt_id TEXT PRIMARY KEY CHECK (
        agent_control.runtime_identifier_valid(attempt_id)
    ),
    schema_revision SMALLINT NOT NULL CHECK (schema_revision = 1),
    run_id TEXT NOT NULL CHECK (agent_control.runtime_identifier_valid(run_id)),
    task_id TEXT NOT NULL CHECK (agent_control.runtime_identifier_valid(task_id)),
    session_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(session_id)
    ),
    ordinal BIGINT NOT NULL CHECK (ordinal > 0),
    state TEXT NOT NULL CHECK (state IN (
        'leased', 'executing', 'result_committed', 'failed', 'timed_out',
        'canceled', 'superseded'
    )),
    state_generation BIGINT NOT NULL CHECK (state_generation > 0),
    lease_generation BIGINT NOT NULL CHECK (lease_generation > 0),
    lease_token UUID NOT NULL,
    lease_worker JSONB NOT NULL CHECK (
        agent_control.runtime_actor_valid(lease_worker)
        AND lease_worker->>'kind' = 'workload'
        AND lease_worker->>'audience' = 'worker'
    ),
    lease_claimed_at TIMESTAMPTZ NOT NULL,
    lease_heartbeat_at TIMESTAMPTZ NOT NULL,
    lease_expires_at TIMESTAMPTZ NOT NULL,
    result_artifact_owner TEXT,
    result_artifact_record_type TEXT,
    result_artifact_id TEXT,
    result_artifact_schema_revision SMALLINT,
    result_artifact_digest CHAR(64),
    failure JSONB CHECK (
        failure IS NULL OR agent_control.runtime_failure_valid(failure)
    ),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    terminal_at TIMESTAMPTZ,
    UNIQUE (task_id, ordinal),
    UNIQUE (attempt_id, run_id, task_id),
    UNIQUE (attempt_id, run_id, task_id, session_id),
    CHECK (created_at <= lease_claimed_at
        AND lease_heartbeat_at <= updated_at
        AND created_at <= updated_at),
    CHECK (lease_claimed_at <= lease_heartbeat_at
        AND lease_heartbeat_at < lease_expires_at),
    CHECK (COALESCE(
        (result_artifact_owner IS NULL
            AND result_artifact_record_type IS NULL
            AND result_artifact_id IS NULL
            AND result_artifact_schema_revision IS NULL
            AND result_artifact_digest IS NULL)
        OR
        (result_artifact_owner IS NOT NULL
            AND result_artifact_owner = 'agent_control'
            AND result_artifact_record_type IS NOT NULL
            AND result_artifact_record_type = 'artifact'
            AND result_artifact_id IS NOT NULL
            AND agent_control.runtime_identifier_valid(result_artifact_id)
            AND result_artifact_schema_revision IS NOT NULL
            AND result_artifact_schema_revision = 1
            AND result_artifact_digest IS NOT NULL
            AND agent_control.runtime_digest_valid(result_artifact_digest::TEXT))
    , false)),
    CHECK (
        (state = 'result_committed'
            AND result_artifact_id IS NOT NULL
            AND result_artifact_digest IS NOT NULL)
        OR
        (state <> 'result_committed'
            AND result_artifact_owner IS NULL
            AND result_artifact_record_type IS NULL
            AND result_artifact_id IS NULL
            AND result_artifact_schema_revision IS NULL
            AND result_artifact_digest IS NULL)
    ),
    CHECK (
        (state IN ('failed', 'timed_out') AND failure IS NOT NULL)
        OR (state NOT IN ('failed', 'timed_out') AND failure IS NULL)
    ),
    CHECK (
        (state IN ('result_committed', 'failed', 'timed_out', 'canceled', 'superseded')
            AND terminal_at IS NOT NULL
            AND terminal_at BETWEEN created_at AND updated_at)
        OR
        (state IN ('leased', 'executing') AND terminal_at IS NULL)
    ),
    FOREIGN KEY (session_id, run_id, task_id)
        REFERENCES agent_control.runtime_session(session_id, run_id, task_id)
        DEFERRABLE INITIALLY DEFERRED
);

CREATE UNIQUE INDEX runtime_attempt_one_nonterminal_per_task_idx
ON agent_control.runtime_attempt (task_id)
WHERE state IN ('leased', 'executing');

CREATE INDEX runtime_attempt_expired_lease_idx
ON agent_control.runtime_attempt (lease_expires_at, task_id, attempt_id)
WHERE state IN ('leased', 'executing');

CREATE TRIGGER runtime_attempt_guard
BEFORE UPDATE OR DELETE ON agent_control.runtime_attempt
FOR EACH ROW EXECUTE FUNCTION agent_control.guard_runtime_mutable_columns(
    'state', 'state_generation', 'lease_generation', 'lease_token',
    'lease_worker', 'lease_claimed_at', 'lease_heartbeat_at', 'lease_expires_at',
    'result_artifact_owner', 'result_artifact_record_type', 'result_artifact_id',
    'result_artifact_schema_revision', 'result_artifact_digest', 'failure', 'updated_at',
    'terminal_at'
);

CREATE TRIGGER runtime_attempt_initial_guard
BEFORE INSERT ON agent_control.runtime_attempt
FOR EACH ROW EXECUTE FUNCTION agent_control.guard_runtime_initial_insert(
    'attempt', 'state_generation'
);

CREATE FUNCTION agent_control.guard_runtime_attempt_lease_update()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    reclaim BOOLEAN := NEW.lease_generation IS DISTINCT FROM OLD.lease_generation
        OR NEW.lease_token IS DISTINCT FROM OLD.lease_token
        OR NEW.lease_worker IS DISTINCT FROM OLD.lease_worker
        OR NEW.lease_claimed_at IS DISTINCT FROM OLD.lease_claimed_at;
BEGIN
    IF reclaim THEN
        IF NEW.state IS DISTINCT FROM OLD.state
           OR NEW.lease_generation <> OLD.lease_generation + 1
           OR NEW.lease_token = OLD.lease_token
           OR NEW.lease_claimed_at < OLD.lease_expires_at THEN
            RAISE EXCEPTION USING ERRCODE = '40001',
                MESSAGE = 'invalid same-Attempt lease reclaim';
        END IF;
    ELSIF NEW.lease_heartbeat_at < OLD.lease_heartbeat_at
       OR NEW.lease_expires_at < OLD.lease_expires_at THEN
        RAISE EXCEPTION USING ERRCODE = '40001',
            MESSAGE = 'Attempt lease heartbeat moved backwards';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER runtime_attempt_lease_guard
BEFORE UPDATE ON agent_control.runtime_attempt
FOR EACH ROW EXECUTE FUNCTION agent_control.guard_runtime_attempt_lease_update();

CREATE TRIGGER runtime_attempt_state_guard
BEFORE UPDATE ON agent_control.runtime_attempt
FOR EACH ROW EXECUTE FUNCTION agent_control.guard_runtime_state_transition(
    'attempt', 'state_generation'
);

-- Every lease acquisition, extension, reclaim, and release is append-only.
-- This separate history makes split-brain and stale-token probes auditable.
CREATE TABLE agent_control.runtime_attempt_lease_event (
    event_id TEXT PRIMARY KEY CHECK (agent_control.runtime_identifier_valid(event_id)),
    schema_revision SMALLINT NOT NULL CHECK (schema_revision = 1),
    attempt_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(attempt_id)
    ),
    event_generation BIGINT NOT NULL CHECK (event_generation > 0),
    lease_generation BIGINT NOT NULL CHECK (lease_generation > 0),
    transition TEXT NOT NULL CHECK (transition IN (
        'claimed', 'heartbeat', 'reclaimed', 'released', 'expired'
    )),
    worker_principal_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(worker_principal_id)
    ),
    lease_token UUID NOT NULL,
    previous_expires_at TIMESTAMPTZ,
    new_expires_at TIMESTAMPTZ,
    actor JSONB NOT NULL CHECK (
        agent_control.runtime_actor_valid(actor)
        AND actor->>'kind' = 'workload'
        AND actor->>'audience' IN ('control_api', 'worker')
    ),
    causation_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(causation_id)
    ),
    correlation_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(correlation_id)
    ),
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (attempt_id, event_generation),
    CHECK (
        (transition = 'claimed'
            AND previous_expires_at IS NULL AND new_expires_at IS NOT NULL)
        OR
        (transition IN ('heartbeat', 'reclaimed')
            AND previous_expires_at IS NOT NULL AND new_expires_at IS NOT NULL
            AND new_expires_at > previous_expires_at)
        OR
        (transition IN ('released', 'expired')
            AND previous_expires_at IS NOT NULL AND new_expires_at IS NULL)
    ),
    FOREIGN KEY (attempt_id) REFERENCES agent_control.runtime_attempt(attempt_id)
        DEFERRABLE INITIALLY DEFERRED
);

CREATE TRIGGER runtime_attempt_lease_event_immutable
BEFORE UPDATE OR DELETE ON agent_control.runtime_attempt_lease_event
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();

CREATE TABLE agent_control.runtime_turn (
    turn_id TEXT PRIMARY KEY CHECK (agent_control.runtime_identifier_valid(turn_id)),
    schema_revision SMALLINT NOT NULL CHECK (schema_revision = 1),
    run_id TEXT NOT NULL CHECK (agent_control.runtime_identifier_valid(run_id)),
    task_id TEXT NOT NULL CHECK (agent_control.runtime_identifier_valid(task_id)),
    session_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(session_id)
    ),
    attempt_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(attempt_id)
    ),
    ordinal BIGINT NOT NULL CHECK (ordinal > 0),
    kind TEXT NOT NULL CHECK (kind = 'model_call'),
    state TEXT NOT NULL CHECK (state IN (
        'planned', 'dispatched', 'result_committed', 'failed', 'unknown', 'canceled'
    )),
    state_generation BIGINT NOT NULL CHECK (state_generation > 0),
    request_digest CHAR(64) NOT NULL CHECK (
        agent_control.runtime_digest_valid(request_digest::TEXT)
    ),
    result_owner TEXT,
    result_record_type TEXT,
    result_id TEXT,
    result_schema_revision SMALLINT,
    result_digest CHAR(64),
    failure JSONB CHECK (
        failure IS NULL OR agent_control.runtime_failure_valid(failure)
    ),
    reservation_held BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    dispatched_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    UNIQUE (attempt_id, ordinal),
    UNIQUE (turn_id, attempt_id),
    UNIQUE (turn_id, attempt_id, request_digest),
    CHECK (created_at <= updated_at),
    CHECK (dispatched_at IS NULL OR dispatched_at >= created_at),
    CHECK (dispatched_at IS NULL OR dispatched_at <= updated_at),
    CHECK (finished_at IS NULL OR (
        finished_at >= created_at
        AND (dispatched_at IS NULL OR finished_at >= dispatched_at)
        AND finished_at <= updated_at
    )),
    CHECK (COALESCE(
        (result_owner IS NULL AND result_record_type IS NULL
            AND result_id IS NULL AND result_schema_revision IS NULL
            AND result_digest IS NULL)
        OR
        (result_owner IS NOT NULL
            AND result_owner = 'agent_control'
            AND result_record_type IS NOT NULL
            AND result_record_type = 'model_call_result'
            AND result_id IS NOT NULL
            AND agent_control.runtime_identifier_valid(result_id)
            AND result_schema_revision IS NOT NULL
            AND result_schema_revision = 1
            AND result_digest IS NOT NULL
            AND agent_control.runtime_digest_valid(result_digest::TEXT))
    , false)),
    CHECK (CASE state
        WHEN 'planned' THEN updated_at = created_at
            AND dispatched_at IS NULL AND finished_at IS NULL
            AND result_id IS NULL AND failure IS NULL AND NOT reservation_held
        WHEN 'dispatched' THEN dispatched_at IS NOT NULL AND finished_at IS NULL
            AND result_id IS NULL AND failure IS NULL AND reservation_held
        WHEN 'result_committed' THEN dispatched_at IS NOT NULL
            AND finished_at IS NOT NULL AND result_id IS NOT NULL
            AND result_digest IS NOT NULL AND failure IS NULL
            AND NOT reservation_held
        WHEN 'failed' THEN dispatched_at IS NOT NULL AND finished_at IS NOT NULL
            AND result_id IS NULL AND failure IS NOT NULL
            AND NOT reservation_held
        WHEN 'unknown' THEN dispatched_at IS NOT NULL AND finished_at IS NULL
            AND result_id IS NULL AND failure IS NOT NULL AND reservation_held
        WHEN 'canceled' THEN finished_at IS NOT NULL
            AND result_id IS NULL AND failure IS NULL AND NOT reservation_held
        ELSE false END),
    FOREIGN KEY (attempt_id, run_id, task_id, session_id)
        REFERENCES agent_control.runtime_attempt(attempt_id, run_id, task_id, session_id)
        DEFERRABLE INITIALLY DEFERRED
);

-- An unknown dispatch remains the one active Turn for its Attempt. It keeps
-- the immutable manifest and ledger reservation, preventing a blind resend.
CREATE UNIQUE INDEX runtime_turn_one_unresolved_per_attempt_idx
ON agent_control.runtime_turn (attempt_id)
WHERE state IN ('planned', 'dispatched', 'unknown');

CREATE TRIGGER runtime_turn_guard
BEFORE UPDATE OR DELETE ON agent_control.runtime_turn
FOR EACH ROW EXECUTE FUNCTION agent_control.guard_runtime_mutable_columns(
    'state', 'state_generation', 'result_owner', 'result_record_type',
    'result_id', 'result_schema_revision', 'result_digest', 'failure',
    'reservation_held', 'updated_at', 'dispatched_at', 'finished_at'
);

CREATE TRIGGER runtime_turn_initial_guard
BEFORE INSERT ON agent_control.runtime_turn
FOR EACH ROW EXECUTE FUNCTION agent_control.guard_runtime_initial_insert(
    'turn', 'state_generation'
);

CREATE TRIGGER runtime_turn_body_guard
BEFORE UPDATE ON agent_control.runtime_turn
FOR EACH ROW EXECUTE FUNCTION agent_control.guard_runtime_turn_update();

CREATE TRIGGER runtime_turn_state_guard
BEFORE UPDATE ON agent_control.runtime_turn
FOR EACH ROW EXECUTE FUNCTION agent_control.guard_runtime_state_transition(
    'turn', 'state_generation'
);

CREATE FUNCTION agent_control.validate_runtime_unresolved_turn_attempt()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    target_attempt_id TEXT := to_jsonb(NEW)->>'attempt_id';
BEGIN
    IF EXISTS (
        SELECT 1
          FROM agent_control.runtime_turn AS turn
          LEFT JOIN agent_control.runtime_attempt AS attempt
            ON attempt.attempt_id = turn.attempt_id
         WHERE turn.attempt_id = target_attempt_id
           AND turn.state IN ('planned', 'dispatched', 'unknown')
           AND (attempt.attempt_id IS NULL
                OR attempt.state NOT IN ('leased', 'executing'))
    ) THEN
        RAISE EXCEPTION USING ERRCODE = '23514',
            MESSAGE = 'unresolved turn requires its nonterminal attempt';
    END IF;
    RETURN NULL;
END
$$;

CREATE CONSTRAINT TRIGGER runtime_attempt_unresolved_turn_guard
AFTER INSERT OR UPDATE ON agent_control.runtime_attempt
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION agent_control.validate_runtime_unresolved_turn_attempt();

CREATE CONSTRAINT TRIGGER runtime_turn_unresolved_attempt_guard
AFTER INSERT OR UPDATE ON agent_control.runtime_turn
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION agent_control.validate_runtime_unresolved_turn_attempt();

CREATE TABLE agent_control.runtime_model_call_manifest (
    call_id TEXT PRIMARY KEY CHECK (agent_control.runtime_identifier_valid(call_id)),
    schema_revision SMALLINT NOT NULL CHECK (schema_revision = 1),
    record_digest CHAR(64) NOT NULL UNIQUE CHECK (
        agent_control.runtime_digest_valid(record_digest::TEXT)
    ),
    turn_id TEXT NOT NULL CHECK (agent_control.runtime_identifier_valid(turn_id)),
    attempt_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(attempt_id)
    ),
    idempotency_key TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(idempotency_key)
    ),
    provider TEXT NOT NULL CHECK (agent_control.runtime_identifier_valid(provider)),
    model TEXT NOT NULL CHECK (agent_control.runtime_identifier_valid(model)),
    prompt_digest CHAR(64) NOT NULL CHECK (
        agent_control.runtime_digest_valid(prompt_digest::TEXT)
    ),
    context_manifest JSONB NOT NULL CHECK (
        agent_control.runtime_blob_ref_valid(context_manifest, 'context_manifest', '')
    ),
    output_contract_digest CHAR(64) NOT NULL CHECK (
        agent_control.runtime_digest_valid(output_contract_digest::TEXT)
    ),
    request_digest CHAR(64) NOT NULL CHECK (
        agent_control.runtime_digest_valid(request_digest::TEXT)
    ),
    max_output_tokens BIGINT NOT NULL CHECK (max_output_tokens > 0),
    reserved_input_tokens BIGINT NOT NULL CHECK (reserved_input_tokens >= 0),
    reserved_external_cost_micro_usd BIGINT NOT NULL CHECK (
        reserved_external_cost_micro_usd >= 0
    ),
    timeout_ms BIGINT NOT NULL CHECK (timeout_ms > 0),
    temperature_micros BIGINT NOT NULL CHECK (temperature_micros >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (turn_id),
    UNIQUE (provider, idempotency_key),
    UNIQUE (call_id, record_digest),
    UNIQUE (call_id, attempt_id, turn_id, idempotency_key, request_digest),
    CHECK ((context_manifest->>'committed_at')::TIMESTAMPTZ <= created_at),
    FOREIGN KEY (turn_id, attempt_id, request_digest)
        REFERENCES agent_control.runtime_turn(turn_id, attempt_id, request_digest)
        DEFERRABLE INITIALLY DEFERRED
);

CREATE TRIGGER runtime_model_call_manifest_immutable
BEFORE UPDATE OR DELETE ON agent_control.runtime_model_call_manifest
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();

CREATE FUNCTION agent_control.validate_runtime_manifest_contract()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
          FROM agent_control.runtime_turn AS turn
          JOIN agent_control.runtime_task AS task
            ON task.task_id = turn.task_id
           AND task.run_id = turn.run_id
         WHERE turn.turn_id = NEW.turn_id
           AND turn.attempt_id = NEW.attempt_id
           AND task.output_contract_digest = NEW.output_contract_digest
    ) THEN
        RAISE EXCEPTION USING ERRCODE = '23514',
            MESSAGE = 'model manifest output contract does not match turn task';
    END IF;
    RETURN NULL;
END
$$;

CREATE CONSTRAINT TRIGGER runtime_model_manifest_contract_guard
AFTER INSERT ON agent_control.runtime_model_call_manifest
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION agent_control.validate_runtime_manifest_contract();

CREATE TABLE agent_control.runtime_model_call_result (
    result_id TEXT PRIMARY KEY CHECK (agent_control.runtime_identifier_valid(result_id)),
    schema_revision SMALLINT NOT NULL CHECK (schema_revision = 1),
    record_digest CHAR(64) NOT NULL UNIQUE CHECK (
        agent_control.runtime_digest_valid(record_digest::TEXT)
    ),
    call_id TEXT NOT NULL CHECK (agent_control.runtime_identifier_valid(call_id)),
    attempt_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(attempt_id)
    ),
    turn_id TEXT NOT NULL CHECK (agent_control.runtime_identifier_valid(turn_id)),
    idempotency_key TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(idempotency_key)
    ),
    request_digest CHAR(64) NOT NULL CHECK (
        agent_control.runtime_digest_valid(request_digest::TEXT)
    ),
    provider_request_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(provider_request_id)
    ),
    output_origin_owner TEXT NOT NULL CHECK (
        output_origin_owner = 'agent_control'
    ),
    output_origin_record_type TEXT NOT NULL CHECK (
        output_origin_record_type = 'model_call_manifest'
    ),
    output_origin_record_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(output_origin_record_id)
    ),
    output_origin_schema_revision SMALLINT NOT NULL CHECK (
        output_origin_schema_revision = 1
    ),
    output_origin_record_digest CHAR(64) NOT NULL CHECK (
        agent_control.runtime_digest_valid(output_origin_record_digest::TEXT)
    ),
    output JSONB NOT NULL CHECK (
        agent_control.runtime_blob_ref_valid(output, 'model_call_manifest', '')
    ),
    input_tokens BIGINT NOT NULL CHECK (input_tokens >= 0),
    output_tokens BIGINT NOT NULL CHECK (output_tokens >= 0),
    external_cost_micro_usd BIGINT NOT NULL CHECK (external_cost_micro_usd >= 0),
    wall_time_ms BIGINT NOT NULL CHECK (wall_time_ms >= 0),
    finish_reason TEXT NOT NULL CHECK (finish_reason IN (
        'stop', 'tool_use', 'length', 'content_filter'
    )),
    committed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (call_id),
    UNIQUE (turn_id),
    UNIQUE (result_id, record_digest),
    UNIQUE (result_id, record_digest, attempt_id),
    CHECK (output_origin_record_id = call_id),
    CHECK (output #>> '{origin,owner}' = output_origin_owner),
    CHECK (output #>> '{origin,record_type}' = output_origin_record_type),
    CHECK (output #>> '{origin,record_id}' = output_origin_record_id),
    CHECK ((output #>> '{origin,schema_revision}')::SMALLINT
        = output_origin_schema_revision),
    CHECK (output #>> '{origin,record_digest}' = output_origin_record_digest::TEXT),
    CHECK ((output->>'committed_at')::TIMESTAMPTZ <= committed_at),
    FOREIGN KEY (call_id, attempt_id, turn_id, idempotency_key, request_digest)
        REFERENCES agent_control.runtime_model_call_manifest(
            call_id, attempt_id, turn_id, idempotency_key, request_digest
        ) DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (call_id, output_origin_record_digest)
        REFERENCES agent_control.runtime_model_call_manifest(call_id, record_digest)
        DEFERRABLE INITIALLY DEFERRED
);

CREATE TRIGGER runtime_model_call_result_immutable
BEFORE UPDATE OR DELETE ON agent_control.runtime_model_call_result
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();

-- These composite keys let Artifact acceptance prove the exact frozen output
-- Contract and Task binding without trusting Worker-supplied JSON.
ALTER TABLE agent_control.output_contract_revision
ADD CONSTRAINT output_contract_revision_digest_type_key
UNIQUE (record_digest, artifact_type);

ALTER TABLE agent_control.runtime_task
ADD CONSTRAINT runtime_task_output_contract_key
UNIQUE (task_id, output_contract_digest);

CREATE TABLE agent_control.runtime_artifact (
    artifact_id TEXT PRIMARY KEY CHECK (
        agent_control.runtime_identifier_valid(artifact_id)
    ),
    schema_revision SMALLINT NOT NULL CHECK (schema_revision = 1),
    record_digest CHAR(64) NOT NULL UNIQUE CHECK (
        agent_control.runtime_digest_valid(record_digest::TEXT)
    ),
    run_id TEXT NOT NULL CHECK (agent_control.runtime_identifier_valid(run_id)),
    task_id TEXT NOT NULL CHECK (agent_control.runtime_identifier_valid(task_id)),
    session_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(session_id)
    ),
    attempt_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(attempt_id)
    ),
    source_result_owner TEXT NOT NULL CHECK (
        source_result_owner = 'agent_control'
    ),
    source_result_record_type TEXT NOT NULL CHECK (
        source_result_record_type = 'model_call_result'
    ),
    source_result_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(source_result_id)
    ),
    source_result_schema_revision SMALLINT NOT NULL CHECK (
        source_result_schema_revision = 1
    ),
    source_result_digest CHAR(64) NOT NULL CHECK (
        agent_control.runtime_digest_valid(source_result_digest::TEXT)
    ),
    artifact_type TEXT NOT NULL CHECK (
        agent_control.runtime_name_valid(artifact_type)
    ),
    output_contract_digest CHAR(64) NOT NULL CHECK (
        agent_control.runtime_digest_valid(output_contract_digest::TEXT)
    ),
    effect_class TEXT NOT NULL CHECK (effect_class = 'none'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (artifact_id, record_digest),
    UNIQUE (attempt_id),
    UNIQUE (artifact_id, run_id, task_id, session_id, attempt_id),
    FOREIGN KEY (attempt_id, run_id, task_id, session_id)
        REFERENCES agent_control.runtime_attempt(attempt_id, run_id, task_id, session_id)
        DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (source_result_id, source_result_digest, attempt_id)
        REFERENCES agent_control.runtime_model_call_result(
            result_id, record_digest, attempt_id
        ) DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (task_id, output_contract_digest)
        REFERENCES agent_control.runtime_task(task_id, output_contract_digest)
        DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (output_contract_digest, artifact_type)
        REFERENCES agent_control.output_contract_revision(record_digest, artifact_type)
        DEFERRABLE INITIALLY DEFERRED
);

CREATE TABLE agent_control.runtime_artifact_section (
    artifact_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(artifact_id)
    ),
    ordinal INTEGER NOT NULL CHECK (ordinal BETWEEN 1 AND 256),
    name TEXT NOT NULL CHECK (agent_control.runtime_name_valid(name)),
    required BOOLEAN NOT NULL,
    content JSONB NOT NULL CHECK (
        agent_control.runtime_blob_ref_valid(content, '', '')
    ),
    PRIMARY KEY (artifact_id, ordinal),
    UNIQUE (artifact_id, name),
    FOREIGN KEY (artifact_id) REFERENCES agent_control.runtime_artifact(artifact_id)
        DEFERRABLE INITIALLY DEFERRED
);

CREATE FUNCTION agent_control.validate_runtime_artifact_sections()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    target_artifact_id TEXT := COALESCE(NEW.artifact_id, OLD.artifact_id);
    section_count BIGINT;
    has_required BOOLEAN;
    has_required_source_output BOOLEAN;
    section_time_valid BOOLEAN;
    result_time_valid BOOLEAN;
BEGIN
    SELECT count(section.artifact_id), COALESCE(bool_or(section.required), false),
           COALESCE(bool_or(
               section.required AND section.content = source_result.output
           ), false),
           COALESCE(bool_and(
               (section.content->>'committed_at')::TIMESTAMPTZ <= artifact.created_at
           ), false),
           source_result.committed_at <= artifact.created_at
      INTO section_count, has_required, has_required_source_output,
           section_time_valid, result_time_valid
      FROM agent_control.runtime_artifact AS artifact
      JOIN agent_control.runtime_model_call_result AS source_result
        ON source_result.result_id = artifact.source_result_id
       AND source_result.record_digest = artifact.source_result_digest
      LEFT JOIN agent_control.runtime_artifact_section AS section
        ON section.artifact_id = artifact.artifact_id
     WHERE artifact.artifact_id = target_artifact_id
     GROUP BY artifact.artifact_id, source_result.committed_at;

    IF section_count IS NULL OR section_count NOT BETWEEN 1 AND 256
       OR NOT has_required OR NOT has_required_source_output
       OR NOT section_time_valid OR NOT result_time_valid THEN
        RAISE EXCEPTION USING ERRCODE = '23514',
            MESSAGE = 'artifact sections violate the frozen contract';
    END IF;
    RETURN NULL;
END
$$;

CREATE CONSTRAINT TRIGGER runtime_artifact_sections_from_artifact
AFTER INSERT ON agent_control.runtime_artifact
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION agent_control.validate_runtime_artifact_sections();

-- The aggregate validator above runs once for the new Artifact and observes
-- every deferred Section at commit.  A per-Section aggregate would turn N
-- Section inserts into O(N^2) work.  Later Section additions preserve the
-- already-established required/source-output invariant; they only need this
-- O(1) timestamp check against their Artifact.
CREATE FUNCTION agent_control.validate_runtime_artifact_section_time()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
          FROM agent_control.runtime_artifact AS artifact
         WHERE artifact.artifact_id = NEW.artifact_id
           AND (NEW.content->>'committed_at')::TIMESTAMPTZ <= artifact.created_at
    ) THEN
        RAISE EXCEPTION USING ERRCODE = '23514',
            MESSAGE = 'artifact Section postdates its Artifact';
    END IF;
    RETURN NULL;
END
$$;

CREATE CONSTRAINT TRIGGER runtime_artifact_section_time_guard
AFTER INSERT ON agent_control.runtime_artifact_section
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION agent_control.validate_runtime_artifact_section_time();

CREATE TRIGGER runtime_artifact_immutable
BEFORE UPDATE OR DELETE ON agent_control.runtime_artifact
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();

CREATE TRIGGER runtime_artifact_section_immutable
BEFORE UPDATE OR DELETE ON agent_control.runtime_artifact_section
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();

-- AP1 publication cannot advance: this record only makes the disabled
-- extension point typed and auditable for the future AP8 contract.
CREATE TABLE agent_control.runtime_artifact_publication_intent (
    intent_id TEXT PRIMARY KEY CHECK (agent_control.runtime_identifier_valid(intent_id)),
    schema_revision SMALLINT NOT NULL CHECK (schema_revision = 1),
    record_digest CHAR(64) NOT NULL UNIQUE CHECK (
        agent_control.runtime_digest_valid(record_digest::TEXT)
    ),
    artifact_owner TEXT NOT NULL CHECK (artifact_owner = 'agent_control'),
    artifact_record_type TEXT NOT NULL CHECK (artifact_record_type = 'artifact'),
    artifact_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(artifact_id)
    ),
    artifact_schema_revision SMALLINT NOT NULL CHECK (
        artifact_schema_revision = 1
    ),
    artifact_digest CHAR(64) NOT NULL CHECK (
        agent_control.runtime_digest_valid(artifact_digest::TEXT)
    ),
    state TEXT NOT NULL CHECK (state = 'disabled'),
    reason_code TEXT NOT NULL CHECK (agent_control.runtime_name_valid(reason_code)),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (artifact_id),
    FOREIGN KEY (artifact_id, artifact_digest)
        REFERENCES agent_control.runtime_artifact(artifact_id, record_digest)
        DEFERRABLE INITIALLY DEFERRED
);

CREATE TRIGGER runtime_artifact_publication_intent_immutable
BEFORE UPDATE OR DELETE ON agent_control.runtime_artifact_publication_intent
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();

CREATE TABLE agent_control.runtime_checkpoint (
    checkpoint_id TEXT PRIMARY KEY CHECK (
        agent_control.runtime_identifier_valid(checkpoint_id)
    ),
    schema_revision SMALLINT NOT NULL CHECK (schema_revision = 1),
    record_digest CHAR(64) NOT NULL UNIQUE CHECK (
        agent_control.runtime_digest_valid(record_digest::TEXT)
    ),
    run_id TEXT NOT NULL CHECK (agent_control.runtime_identifier_valid(run_id)),
    task_id TEXT NOT NULL CHECK (agent_control.runtime_identifier_valid(task_id)),
    session_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(session_id)
    ),
    generation BIGINT NOT NULL CHECK (generation > 0),
    previous_checkpoint_id TEXT,
    previous_checkpoint_generation BIGINT GENERATED ALWAYS AS (
        generation - 1
    ) STORED,
    manifest JSONB NOT NULL CHECK (
        agent_control.runtime_blob_ref_valid(manifest, 'checkpoint_manifest', '')
    ),
    narrative JSONB CHECK (
        narrative IS NULL OR agent_control.runtime_blob_ref_valid(
            narrative, 'checkpoint_narrative', ''
        )
    ),
    created_by_attempt_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(created_by_attempt_id)
    ),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (session_id, generation),
    UNIQUE (checkpoint_id, session_id, run_id, task_id),
    UNIQUE (checkpoint_id, session_id, generation),
    CHECK (
        (generation = 1 AND previous_checkpoint_id IS NULL)
        OR
        (generation > 1
            AND agent_control.runtime_identifier_valid(previous_checkpoint_id)
            AND previous_checkpoint_id <> checkpoint_id)
    ),
    CHECK ((manifest->>'committed_at')::TIMESTAMPTZ <= created_at),
    CHECK (narrative IS NULL
        OR (narrative->>'committed_at')::TIMESTAMPTZ <= created_at),
    FOREIGN KEY (session_id, run_id, task_id)
        REFERENCES agent_control.runtime_session(session_id, run_id, task_id)
        DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (created_by_attempt_id, run_id, task_id, session_id)
        REFERENCES agent_control.runtime_attempt(attempt_id, run_id, task_id, session_id)
        DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (
        previous_checkpoint_id, session_id, previous_checkpoint_generation
    ) REFERENCES agent_control.runtime_checkpoint(
        checkpoint_id, session_id, generation
    )
        DEFERRABLE INITIALLY DEFERRED
);

CREATE TABLE agent_control.runtime_checkpoint_preserve_ref (
    checkpoint_id TEXT NOT NULL,
    ordinal INTEGER NOT NULL CHECK (ordinal BETWEEN 1 AND 4096),
    reference JSONB NOT NULL CHECK (
        agent_control.runtime_record_ref_valid(reference, '', '')
    ),
    PRIMARY KEY (checkpoint_id, ordinal),
    FOREIGN KEY (checkpoint_id) REFERENCES agent_control.runtime_checkpoint(checkpoint_id)
        DEFERRABLE INITIALLY DEFERRED
);

CREATE UNIQUE INDEX runtime_checkpoint_preserve_ref_identity_idx
ON agent_control.runtime_checkpoint_preserve_ref (
    checkpoint_id,
    (reference->>'owner'),
    (reference->>'record_type'),
    (reference->>'record_id'),
    ((reference->>'schema_revision')::SMALLINT)
);

CREATE TRIGGER runtime_checkpoint_immutable
BEFORE UPDATE OR DELETE ON agent_control.runtime_checkpoint
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();

CREATE TRIGGER runtime_checkpoint_preserve_ref_immutable
BEFORE UPDATE OR DELETE ON agent_control.runtime_checkpoint_preserve_ref
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();

CREATE FUNCTION agent_control.guard_runtime_session_checkpoint_cas()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    old_checkpoint_generation BIGINT;
    new_checkpoint_generation BIGINT;
    new_previous_checkpoint_id TEXT;
BEGIN
    IF OLD.state = 'closed' THEN
        RAISE EXCEPTION USING ERRCODE = '55000',
            MESSAGE = 'closed session is immutable';
    END IF;

    IF NEW.state <> OLD.state THEN
        IF NEW.latest_checkpoint_id IS DISTINCT FROM OLD.latest_checkpoint_id THEN
            RAISE EXCEPTION USING ERRCODE = '40001',
                MESSAGE = 'checkpoint CAS cannot be combined with session close';
        END IF;
        RETURN NEW;
    END IF;

    IF NEW.latest_checkpoint_id IS NOT DISTINCT FROM OLD.latest_checkpoint_id
       OR NEW.latest_checkpoint_id IS NULL THEN
        RAISE EXCEPTION USING ERRCODE = '40001',
            MESSAGE = 'session same-state update requires a forward checkpoint CAS';
    END IF;

    SELECT checkpoint.generation, checkpoint.previous_checkpoint_id
      INTO new_checkpoint_generation, new_previous_checkpoint_id
      FROM agent_control.runtime_checkpoint AS checkpoint
     WHERE checkpoint.checkpoint_id = NEW.latest_checkpoint_id
       AND checkpoint.session_id = OLD.session_id
       AND checkpoint.run_id = OLD.run_id
       AND checkpoint.task_id = OLD.task_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION USING ERRCODE = '23503',
            MESSAGE = 'checkpoint CAS target does not exist in this session';
    END IF;

    IF OLD.latest_checkpoint_id IS NULL THEN
        IF new_checkpoint_generation <> 1 OR new_previous_checkpoint_id IS NOT NULL THEN
            RAISE EXCEPTION USING ERRCODE = '40001',
                MESSAGE = 'first session checkpoint must be generation one';
        END IF;
    ELSE
        SELECT checkpoint.generation
          INTO old_checkpoint_generation
          FROM agent_control.runtime_checkpoint AS checkpoint
         WHERE checkpoint.checkpoint_id = OLD.latest_checkpoint_id
           AND checkpoint.session_id = OLD.session_id;
        IF NOT FOUND
           OR new_checkpoint_generation <> old_checkpoint_generation + 1
           OR new_previous_checkpoint_id <> OLD.latest_checkpoint_id THEN
            RAISE EXCEPTION USING ERRCODE = '40001',
                MESSAGE = 'checkpoint CAS must advance exactly one linked generation';
        END IF;
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER runtime_session_checkpoint_cas_guard
BEFORE UPDATE ON agent_control.runtime_session
FOR EACH ROW EXECUTE FUNCTION agent_control.guard_runtime_session_checkpoint_cas();

CREATE TABLE agent_control.runtime_cancellation_request (
    request_id TEXT PRIMARY KEY CHECK (
        agent_control.runtime_identifier_valid(request_id)
    ),
    schema_revision SMALLINT NOT NULL CHECK (schema_revision = 1),
    record_digest CHAR(64) NOT NULL UNIQUE CHECK (
        agent_control.runtime_digest_valid(record_digest::TEXT)
    ),
    target TEXT NOT NULL CHECK (target IN ('run', 'task')),
    target_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(target_id)
    ),
    expected_state_generation BIGINT NOT NULL CHECK (
        expected_state_generation > 0
    ),
    mode TEXT NOT NULL CHECK (mode IN ('cancel', 'supersede')),
    superseded_by_run_id TEXT,
    actor JSONB NOT NULL CHECK (agent_control.runtime_actor_valid(actor)),
    reason_code TEXT NOT NULL CHECK (agent_control.runtime_name_valid(reason_code)),
    requested_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CHECK (
        (mode = 'supersede' AND target = 'run'
            AND agent_control.runtime_identifier_valid(superseded_by_run_id)
            AND superseded_by_run_id <> target_id)
        OR
        (mode = 'cancel' AND superseded_by_run_id IS NULL)
    )
);

CREATE TRIGGER runtime_cancellation_request_immutable
BEFORE UPDATE OR DELETE ON agent_control.runtime_cancellation_request
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();

CREATE TABLE agent_control.runtime_recovery_record (
    recovery_id TEXT PRIMARY KEY CHECK (
        agent_control.runtime_identifier_valid(recovery_id)
    ),
    schema_revision SMALLINT NOT NULL CHECK (schema_revision = 1),
    record_digest CHAR(64) NOT NULL UNIQUE CHECK (
        agent_control.runtime_digest_valid(record_digest::TEXT)
    ),
    run_id TEXT NOT NULL CHECK (agent_control.runtime_identifier_valid(run_id)),
    task_id TEXT NOT NULL CHECK (agent_control.runtime_identifier_valid(task_id)),
    previous_attempt_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(previous_attempt_id)
    ),
    original_causation_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(original_causation_id)
    ),
    original_idempotency_key TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(original_idempotency_key)
    ),
    decision TEXT NOT NULL CHECK (decision IN (
        'reuse_committed_result', 'retry_same_task', 'dead_letter', 'canceled'
    )),
    committed_artifact_owner TEXT,
    committed_artifact_record_type TEXT,
    committed_artifact_id TEXT,
    committed_artifact_schema_revision SMALLINT,
    committed_artifact_digest CHAR(64),
    next_attempt_id TEXT,
    reason_code TEXT NOT NULL CHECK (agent_control.runtime_name_valid(reason_code)),
    decided_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (previous_attempt_id),
    CHECK (COALESCE(
        (committed_artifact_owner IS NULL
            AND committed_artifact_record_type IS NULL
            AND committed_artifact_id IS NULL
            AND committed_artifact_schema_revision IS NULL
            AND committed_artifact_digest IS NULL)
        OR
        (committed_artifact_owner IS NOT NULL
            AND committed_artifact_owner = 'agent_control'
            AND committed_artifact_record_type IS NOT NULL
            AND committed_artifact_record_type = 'artifact'
            AND committed_artifact_id IS NOT NULL
            AND agent_control.runtime_identifier_valid(committed_artifact_id)
            AND committed_artifact_schema_revision IS NOT NULL
            AND committed_artifact_schema_revision = 1
            AND committed_artifact_digest IS NOT NULL
            AND agent_control.runtime_digest_valid(committed_artifact_digest::TEXT))
    , false)),
    CHECK (CASE decision
        WHEN 'reuse_committed_result' THEN committed_artifact_id IS NOT NULL
            AND next_attempt_id IS NULL
        WHEN 'retry_same_task' THEN committed_artifact_id IS NULL
            AND agent_control.runtime_identifier_valid(next_attempt_id)
            AND next_attempt_id <> previous_attempt_id
        ELSE committed_artifact_id IS NULL AND next_attempt_id IS NULL
        END),
    FOREIGN KEY (previous_attempt_id, run_id, task_id)
        REFERENCES agent_control.runtime_attempt(attempt_id, run_id, task_id)
        DEFERRABLE INITIALLY DEFERRED
);

CREATE TRIGGER runtime_recovery_record_immutable
BEFORE UPDATE OR DELETE ON agent_control.runtime_recovery_record
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();

CREATE FUNCTION agent_control.runtime_subject_state_valid(
    p_subject TEXT,
    p_state TEXT
) RETURNS BOOLEAN
LANGUAGE sql
IMMUTABLE
AS $$
    SELECT COALESCE(CASE p_subject
        WHEN 'run' THEN p_state IN (
            'queued', 'running', 'waiting', 'canceling', 'succeeded', 'failed',
            'canceled', 'superseded', 'dead_lettered'
        )
        WHEN 'task' THEN p_state IN (
            'blocked', 'ready', 'running', 'waiting', 'result_committed',
            'succeeded', 'failed', 'canceled', 'superseded', 'dead_lettered'
        )
        WHEN 'session' THEN p_state IN ('open', 'closed')
        WHEN 'attempt' THEN p_state IN (
            'leased', 'executing', 'result_committed', 'failed', 'timed_out',
            'canceled', 'superseded'
        )
        WHEN 'turn' THEN p_state IN (
            'planned', 'dispatched', 'result_committed', 'failed', 'unknown', 'canceled'
        )
        WHEN 'budget' THEN p_state IN ('open', 'exhausted', 'closed')
        WHEN 'publication_intent' THEN p_state = 'disabled'
        ELSE false
    END, false)
$$;

CREATE FUNCTION agent_control.runtime_initial_state_valid(
    p_subject TEXT,
    p_state TEXT
) RETURNS BOOLEAN
LANGUAGE sql
IMMUTABLE
AS $$
    SELECT COALESCE(CASE p_subject
        WHEN 'run' THEN p_state = 'queued'
        WHEN 'task' THEN p_state IN ('blocked', 'ready')
        WHEN 'session' THEN p_state = 'open'
        WHEN 'attempt' THEN p_state = 'leased'
        WHEN 'turn' THEN p_state = 'planned'
        WHEN 'budget' THEN p_state = 'open'
        WHEN 'publication_intent' THEN p_state = 'disabled'
        ELSE false
    END, false)
$$;

CREATE TABLE agent_control.runtime_event (
    event_id TEXT PRIMARY KEY CHECK (agent_control.runtime_identifier_valid(event_id)),
    schema_revision SMALLINT NOT NULL CHECK (schema_revision = 1),
    record_digest CHAR(64) NOT NULL UNIQUE CHECK (
        agent_control.runtime_digest_valid(record_digest::TEXT)
    ),
    owner_sequence BIGINT GENERATED ALWAYS AS IDENTITY UNIQUE,
    subject TEXT NOT NULL CHECK (subject IN (
        'run', 'task', 'session', 'attempt', 'turn', 'budget',
        'publication_intent'
    )),
    subject_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(subject_id)
    ),
    from_state TEXT,
    to_state TEXT NOT NULL,
    generation BIGINT NOT NULL CHECK (generation > 0),
    actor JSONB NOT NULL CHECK (agent_control.runtime_actor_valid(actor)),
    causation_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(causation_id)
    ),
    correlation_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(correlation_id)
    ),
    reason_code TEXT NOT NULL CHECK (agent_control.runtime_name_valid(reason_code)),
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (subject, subject_id, generation),
    CHECK (agent_control.runtime_subject_state_valid(subject, to_state)),
    CHECK (
        (from_state IS NULL AND generation = 1
            AND agent_control.runtime_initial_state_valid(subject, to_state))
        OR
        (from_state IS NOT NULL AND generation > 1
            AND agent_control.runtime_subject_state_valid(subject, from_state)
            AND from_state <> to_state
            AND agent_control.runtime_transition_allowed(
                subject, from_state, to_state
            ))
    )
);

CREATE TRIGGER runtime_event_immutable
BEFORE UPDATE OR DELETE ON agent_control.runtime_event
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();

-- Cyclic lineage is installed only after all participating tables exist. All
-- constraints are deferred so one 0006 command transaction can create a Run,
-- its frozen ledgers, root Task, Session, and initial audit records atomically.
ALTER TABLE agent_control.runtime_run
ADD CONSTRAINT runtime_run_budget_ledger_fk
FOREIGN KEY (budget_ledger_id)
REFERENCES agent_control.runtime_budget_ledger(ledger_id)
DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE agent_control.runtime_run
ADD CONSTRAINT runtime_run_root_task_fk
FOREIGN KEY (root_task_id, run_id)
REFERENCES agent_control.runtime_task(task_id, run_id)
DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE agent_control.runtime_run
ADD CONSTRAINT runtime_run_superseded_by_fk
FOREIGN KEY (superseded_by)
REFERENCES agent_control.runtime_run(run_id)
DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE agent_control.runtime_artifact
ADD CONSTRAINT runtime_artifact_id_run_task_key
UNIQUE (artifact_id, run_id, task_id);

ALTER TABLE agent_control.runtime_artifact
ADD CONSTRAINT runtime_artifact_digest_run_task_key
UNIQUE (artifact_id, record_digest, run_id, task_id);

ALTER TABLE agent_control.runtime_artifact
ADD CONSTRAINT runtime_artifact_exact_lineage_key
UNIQUE (artifact_id, record_digest, run_id, task_id, session_id, attempt_id);

ALTER TABLE agent_control.runtime_artifact
ADD CONSTRAINT runtime_artifact_recovery_lineage_key
UNIQUE (artifact_id, record_digest, run_id, task_id, attempt_id);

ALTER TABLE agent_control.runtime_model_call_result
ADD CONSTRAINT runtime_model_call_result_turn_lineage_key
UNIQUE (result_id, record_digest, turn_id, attempt_id);

ALTER TABLE agent_control.runtime_task
ADD CONSTRAINT runtime_task_session_fk
FOREIGN KEY (session_id, run_id, task_id)
REFERENCES agent_control.runtime_session(session_id, run_id, task_id)
DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE agent_control.runtime_task
ADD CONSTRAINT runtime_task_result_artifact_fk
FOREIGN KEY (result_artifact_id, run_id, task_id)
REFERENCES agent_control.runtime_artifact(artifact_id, run_id, task_id)
DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE agent_control.runtime_session
ADD CONSTRAINT runtime_session_latest_checkpoint_fk
FOREIGN KEY (latest_checkpoint_id, session_id, run_id, task_id)
REFERENCES agent_control.runtime_checkpoint(checkpoint_id, session_id, run_id, task_id)
DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE agent_control.runtime_attempt
ADD CONSTRAINT runtime_attempt_result_artifact_fk
FOREIGN KEY (
    result_artifact_id, result_artifact_digest,
    run_id, task_id, session_id, attempt_id
)
REFERENCES agent_control.runtime_artifact(
    artifact_id, record_digest, run_id, task_id, session_id, attempt_id
)
DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE agent_control.runtime_turn
ADD CONSTRAINT runtime_turn_result_fk
FOREIGN KEY (result_id, result_digest, turn_id, attempt_id)
REFERENCES agent_control.runtime_model_call_result(
    result_id, record_digest, turn_id, attempt_id
)
DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE agent_control.runtime_recovery_record
ADD CONSTRAINT runtime_recovery_committed_artifact_fk
FOREIGN KEY (
    committed_artifact_id, committed_artifact_digest,
    run_id, task_id, previous_attempt_id
)
REFERENCES agent_control.runtime_artifact(
    artifact_id, record_digest, run_id, task_id, attempt_id
)
DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE agent_control.runtime_recovery_record
ADD CONSTRAINT runtime_recovery_next_attempt_fk
FOREIGN KEY (next_attempt_id, run_id, task_id)
REFERENCES agent_control.runtime_attempt(attempt_id, run_id, task_id)
DEFERRABLE INITIALLY DEFERRED;

-- 0006 must still lock and validate current active heads/enabled registration,
-- recovery OriginalAuthority/OriginalEffect ownership, child late-insert
-- closure, dependency counts/readiness, and dynamic depth/fanout/section limits.
-- Those are command-time policy/currentness decisions, not immutable row shape.
-- The frozen v1 Turn graph admits dispatched->canceled, but 0006 must expose
-- cancellation only for planned Turns. A dispatched/unknown model call must
-- retain its exact reservation and reconcile to result_committed or failed;
-- changing that graph here alone would silently diverge SQL from Go/YAML.
-- Session.generation is state generation: checkpoint CAS advances the linked
-- Checkpoint.generation while Session.generation stays fixed; close alone
-- advances Session.generation by one.

REVOKE ALL ON ALL TABLES IN SCHEMA agent_control FROM PUBLIC;
REVOKE ALL ON ALL FUNCTIONS IN SCHEMA agent_control FROM PUBLIC;
REVOKE ALL ON ALL SEQUENCES IN SCHEMA agent_control FROM PUBLIC;

RESET ROLE;
