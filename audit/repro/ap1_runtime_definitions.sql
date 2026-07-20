\set ON_ERROR_STOP on

CREATE FUNCTION pg_temp.assert_sqlstate(p_sql TEXT, p_expected_state TEXT)
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
    observed_state TEXT;
BEGIN
    BEGIN
        EXECUTE p_sql;
    EXCEPTION WHEN OTHERS THEN
        observed_state := SQLSTATE;
    END;
    IF observed_state IS NULL THEN
        RAISE EXCEPTION 'statement unexpectedly succeeded: %', p_sql;
    END IF;
    IF observed_state <> p_expected_state THEN
        RAISE EXCEPTION 'statement got SQLSTATE %, expected %: %',
            observed_state, p_expected_state, p_sql;
    END IF;
END
$$;

CREATE FUNCTION pg_temp.assert_role_fails(
    p_role TEXT,
    p_sql TEXT,
    p_expected_state TEXT
) RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
    login_name TEXT;
    observed_state TEXT;
BEGIN
    login_name := CASE p_role
        WHEN 'alpheus_agent_control_api' THEN 'control-1'
        WHEN 'alpheus_agent_worker' THEN 'worker-1'
        WHEN 'alpheus_platform_owner' THEN 'owner-1'
        WHEN 'alpheus_agent_activator' THEN 'activator-1'
        WHEN 'alpheus_agent_diagnostics' THEN 'diagnostics-1'
        ELSE NULL
    END;
    IF login_name IS NULL THEN
        RAISE EXCEPTION 'unknown AP1 definitions probe role %', p_role;
    END IF;

    EXECUTE format('SET LOCAL SESSION AUTHORIZATION %I', login_name);
    EXECUTE format('SET LOCAL ROLE %I', p_role);
    BEGIN
        EXECUTE p_sql;
    EXCEPTION WHEN OTHERS THEN
        observed_state := SQLSTATE;
    END;
    RESET ROLE;
    RESET SESSION AUTHORIZATION;

    IF observed_state IS NULL THEN
        RAISE EXCEPTION 'statement unexpectedly succeeded for role %: %', p_role, p_sql;
    END IF;
    IF observed_state <> p_expected_state THEN
        RAISE EXCEPTION 'role % got SQLSTATE %, expected %: %',
            p_role, observed_state, p_expected_state, p_sql;
    END IF;
END
$$;

-- Structurally valid immutable definitions. Canonical digest recomputation is
-- enforced by the later identity-derived command boundary; this storage probe
-- verifies the exact persisted tuple, relational constraints, and isolation.
SET ROLE alpheus_agent_migrator;

INSERT INTO platform_governance.owner_policy_revision (
    revision_id, schema_revision, policy_id, generation, record_digest,
    origin_kind, source_owner, source_record_type, initiating_kind,
    initiating_audience, initiating_principal_id, effect_ceiling,
    author_principal_id, author_kind, author_audience, reason_code, created_at
) VALUES (
    'owner-policy-revision-1', 1, 'owner-policy-1', 1, repeat('a', 64),
    'schedule', 'agent_control', 'schedule_occurrence', 'workload',
    'control_api', 'scheduler-1', 'none',
    'owner-1', 'user', 'activator', 'initial_policy',
    '2026-07-20 12:00:00+00'
);

INSERT INTO platform_governance.owner_policy_head (
    head_id, schema_revision, generation, revision_id, revision_digest,
    activated_by_principal_id, activated_by_kind, activated_by_audience,
    activated_at
) VALUES (
    'owner-policy-1', 1, 1, 'owner-policy-revision-1', repeat('a', 64),
    'activator-1', 'workload', 'activator', '2026-07-20 12:01:00+00'
);

INSERT INTO platform_governance.owner_policy_event (
    event_id, schema_revision, policy_id, generation, current_revision_id,
    current_revision_digest, actor_principal_id, actor_kind, actor_audience,
    reason_code, occurred_at
) VALUES (
    'owner-policy-event-1', 1, 'owner-policy-1', 1,
    'owner-policy-revision-1', repeat('a', 64),
    'activator-1', 'workload', 'activator', 'activate_policy',
    '2026-07-20 12:01:00+00'
);

INSERT INTO agent_control.runtime_policy_revision (
    policy_id, schema_revision, generation, record_digest,
    max_model_calls, max_input_tokens, max_output_tokens, max_tool_calls,
    max_external_cost_micro_usd, max_wall_time_ms, max_idle_time_ms,
    max_tasks, max_depth, max_fanout, max_parallelism,
    max_invalid_output_retries, max_infrastructure_retries,
    max_lease_seconds, max_heartbeat_extension_seconds, max_claim_batch,
    max_dependencies, max_artifact_sections, dead_letter_retention_seconds,
    updated_by_principal_id, updated_by_kind, updated_by_audience, updated_at
) VALUES (
    'runtime-policy-1', 1, 1, repeat('b', 64),
    10, 10000, 2000, 0, 100000, 3600000, 600000,
    10, 3, 4, 2, 1, 2, 300, 60, 20, 100, 32, 86400,
    'control-1', 'workload', 'control_api', '2026-07-20 12:02:00+00'
);

INSERT INTO agent_control.runtime_policy_head (
    policy_id, generation, record_digest, selected_by_principal_id,
    selected_by_kind, selected_by_audience, selected_at
) VALUES (
    'runtime-policy-1', 1, repeat('b', 64), 'control-1',
    'workload', 'control_api', '2026-07-20 12:03:00+00'
);

INSERT INTO agent_control.runtime_policy_event (
    event_id, policy_id, generation, current_record_digest,
    actor_principal_id, actor_kind, actor_audience, reason_code, occurred_at
) VALUES (
    'runtime-policy-event-1', 'runtime-policy-1', 1, repeat('b', 64),
    'control-1', 'workload', 'control_api', 'install_policy',
    '2026-07-20 12:03:00+00'
);

INSERT INTO agent_control.trigger_registration_revision (
    registration_id, schema_revision, generation, record_digest, kind,
    source_key, owner_policy_owner, owner_policy_record_type,
    owner_policy_record_id, owner_policy_schema_revision,
    owner_policy_record_digest, owner_policy_generation,
    runtime_policy_owner, runtime_policy_record_type, runtime_policy_record_id,
    runtime_policy_schema_revision, runtime_policy_record_digest,
    runtime_policy_generation, enabled, updated_by_principal_id,
    updated_by_kind, updated_by_audience, updated_at
) VALUES (
    'trigger-registration-1', 1, 1, repeat('c', 64), 'schedule',
    'daily-research', 'platform_governance', 'owner_policy_revision',
    'owner-policy-revision-1', 1, repeat('a', 64), 1,
    'agent_control', 'runtime_policy', 'runtime-policy-1', 1,
    repeat('b', 64), 1, true, 'control-1',
    'workload', 'control_api', '2026-07-20 12:04:00+00'
);

INSERT INTO agent_control.trigger_registration_head (
    registration_id, generation, record_digest, selected_by_principal_id,
    selected_by_kind, selected_by_audience, selected_at
) VALUES (
    'trigger-registration-1', 1, repeat('c', 64), 'control-1',
    'workload', 'control_api', '2026-07-20 12:05:00+00'
);

INSERT INTO agent_control.trigger_registration_event (
    event_id, registration_id, generation, current_record_digest,
    actor_principal_id, actor_kind, actor_audience, reason_code, occurred_at
) VALUES (
    'trigger-registration-event-1', 'trigger-registration-1', 1,
    repeat('c', 64), 'control-1', 'workload', 'control_api',
    'install_registration', '2026-07-20 12:05:00+00'
);

INSERT INTO agent_control.output_contract_revision (
    revision_id, schema_revision, generation, record_digest, artifact_type,
    schema_blob_schema_revision, schema_blob_id, schema_blob_content_digest,
    schema_blob_media_type, schema_blob_size_bytes, schema_origin_owner,
    schema_origin_record_type, schema_origin_record_id,
    schema_origin_schema_revision, schema_origin_record_digest,
    schema_blob_committed_at, effect_class, author_principal_id, author_kind,
    author_audience, reason_code, created_at
) VALUES (
    'output-contract-1', 1, 1, repeat('d', 64), 'decision_draft',
    1, '20000000-0000-4000-8000-333333333333', repeat('e', 64),
    'application/json', 256, 'agent_control', 'output_contract_schema',
    'output-contract-schema-1', 1, repeat('f', 64),
    '2026-07-20 12:05:00+00', 'none', 'control-1', 'workload',
    'control_api', 'initial_contract', '2026-07-20 12:06:00+00'
);

RESET ROLE;

DO $$
BEGIN
    IF (SELECT count(*) FROM platform_governance.owner_policy_revision) <> 1
       OR (SELECT count(*) FROM platform_governance.owner_policy_head) <> 1
       OR (SELECT count(*) FROM platform_governance.owner_policy_event) <> 1
       OR (SELECT count(*) FROM agent_control.runtime_policy_revision) <> 1
       OR (SELECT count(*) FROM agent_control.runtime_policy_head) <> 1
       OR (SELECT count(*) FROM agent_control.runtime_policy_event) <> 1
       OR (SELECT count(*) FROM agent_control.trigger_registration_revision) <> 1
       OR (SELECT count(*) FROM agent_control.trigger_registration_head) <> 1
       OR (SELECT count(*) FROM agent_control.trigger_registration_event) <> 1
       OR (SELECT count(*) FROM agent_control.output_contract_revision) <> 1 THEN
        RAISE EXCEPTION 'valid AP1 definition fixtures did not persist exactly once';
    END IF;
END
$$;

-- system_recovery must reuse the original OwnerPolicy revision rather than
-- minting a separately registrable policy.
SELECT pg_temp.assert_sqlstate($sql$
    INSERT INTO platform_governance.owner_policy_revision (
        revision_id, schema_revision, policy_id, generation, record_digest,
        origin_kind, source_owner, source_record_type, initiating_kind,
        initiating_audience, effect_ceiling, author_principal_id, author_kind,
        author_audience, reason_code, created_at
    ) VALUES (
        'recovery-policy-revision', 1, 'recovery-policy', 1, repeat('1', 64),
        'system_recovery', 'agent_control', 'recovery_occurrence', 'workload',
        'control_api', 'none', 'owner-1', 'user', 'activator',
        'invalid_recovery', '2026-07-20 12:07:00+00'
    )
$sql$, '23514');

-- Frozen parser/schema ceilings are the only fixed numeric ceilings in 0004.
SELECT pg_temp.assert_sqlstate($sql$
    INSERT INTO agent_control.runtime_policy_revision (
        policy_id, schema_revision, generation, record_digest,
        max_model_calls, max_input_tokens, max_output_tokens, max_tool_calls,
        max_external_cost_micro_usd, max_wall_time_ms, max_idle_time_ms,
        max_tasks, max_depth, max_fanout, max_parallelism,
        max_invalid_output_retries, max_infrastructure_retries,
        max_lease_seconds, max_heartbeat_extension_seconds, max_claim_batch,
        max_dependencies, max_artifact_sections, dead_letter_retention_seconds,
        updated_by_principal_id, updated_by_kind, updated_by_audience, updated_at
    ) SELECT
        'runtime-policy-too-many-dependencies', schema_revision, 1, repeat('2', 64),
        max_model_calls, max_input_tokens, max_output_tokens, max_tool_calls,
        max_external_cost_micro_usd, max_wall_time_ms, max_idle_time_ms,
        max_tasks, max_depth, max_fanout, max_parallelism,
        max_invalid_output_retries, max_infrastructure_retries,
        max_lease_seconds, max_heartbeat_extension_seconds, max_claim_batch,
        4097, max_artifact_sections, dead_letter_retention_seconds,
        updated_by_principal_id, updated_by_kind, updated_by_audience, updated_at
    FROM agent_control.runtime_policy_revision
    WHERE policy_id = 'runtime-policy-1' AND generation = 1
$sql$, '23514');

SELECT pg_temp.assert_sqlstate($sql$
    INSERT INTO agent_control.runtime_policy_revision (
        policy_id, schema_revision, generation, record_digest,
        max_model_calls, max_input_tokens, max_output_tokens, max_tool_calls,
        max_external_cost_micro_usd, max_wall_time_ms, max_idle_time_ms,
        max_tasks, max_depth, max_fanout, max_parallelism,
        max_invalid_output_retries, max_infrastructure_retries,
        max_lease_seconds, max_heartbeat_extension_seconds, max_claim_batch,
        max_dependencies, max_artifact_sections, dead_letter_retention_seconds,
        updated_by_principal_id, updated_by_kind, updated_by_audience, updated_at
    ) SELECT
        'runtime-policy-too-many-sections', schema_revision, 1, repeat('3', 64),
        max_model_calls, max_input_tokens, max_output_tokens, max_tool_calls,
        max_external_cost_micro_usd, max_wall_time_ms, max_idle_time_ms,
        max_tasks, max_depth, max_fanout, max_parallelism,
        max_invalid_output_retries, max_infrastructure_retries,
        max_lease_seconds, max_heartbeat_extension_seconds, max_claim_batch,
        max_dependencies, 257, dead_letter_retention_seconds,
        updated_by_principal_id, updated_by_kind, updated_by_audience, updated_at
    FROM agent_control.runtime_policy_revision
    WHERE policy_id = 'runtime-policy-1' AND generation = 1
$sql$, '23514');

SELECT pg_temp.assert_sqlstate($sql$
    INSERT INTO agent_control.output_contract_revision (
        revision_id, schema_revision, generation, record_digest, artifact_type,
        schema_blob_schema_revision, schema_blob_id, schema_blob_content_digest,
        schema_blob_media_type, schema_blob_size_bytes, schema_origin_owner,
        schema_origin_record_type, schema_origin_record_id,
        schema_origin_schema_revision, schema_origin_record_digest,
        schema_blob_committed_at, effect_class, author_principal_id, author_kind,
        author_audience, reason_code, created_at
    ) SELECT
        'output-contract-too-large', schema_revision, generation, repeat('4', 64),
        artifact_type, schema_blob_schema_revision, schema_blob_id,
        schema_blob_content_digest, schema_blob_media_type, 1073741825,
        schema_origin_owner, schema_origin_record_type, schema_origin_record_id,
        schema_origin_schema_revision, schema_origin_record_digest,
        schema_blob_committed_at, effect_class, author_principal_id, author_kind,
        author_audience, reason_code, created_at
    FROM agent_control.output_contract_revision
    WHERE revision_id = 'output-contract-1'
$sql$, '23514');

-- Every revision/event body is append-only. Heads are intentionally mutable
-- only through the later fenced command API.
SELECT pg_temp.assert_sqlstate($sql$
    UPDATE platform_governance.owner_policy_revision
    SET reason_code = 'mutated'
    WHERE revision_id = 'owner-policy-revision-1'
$sql$, '55000');
SELECT pg_temp.assert_sqlstate($sql$
    DELETE FROM platform_governance.owner_policy_event
    WHERE event_id = 'owner-policy-event-1'
$sql$, '55000');
SELECT pg_temp.assert_sqlstate($sql$
    UPDATE agent_control.runtime_policy_revision
    SET max_claim_batch = 21
    WHERE policy_id = 'runtime-policy-1' AND generation = 1
$sql$, '55000');
SELECT pg_temp.assert_sqlstate($sql$
    DELETE FROM agent_control.runtime_policy_event
    WHERE event_id = 'runtime-policy-event-1'
$sql$, '55000');
SELECT pg_temp.assert_sqlstate($sql$
    UPDATE agent_control.trigger_registration_revision
    SET enabled = false
    WHERE registration_id = 'trigger-registration-1' AND generation = 1
$sql$, '55000');
SELECT pg_temp.assert_sqlstate($sql$
    DELETE FROM agent_control.trigger_registration_event
    WHERE event_id = 'trigger-registration-event-1'
$sql$, '55000');
SELECT pg_temp.assert_sqlstate($sql$
    UPDATE agent_control.output_contract_revision
    SET reason_code = 'mutated'
    WHERE revision_id = 'output-contract-1'
$sql$, '55000');

-- Same-owner exact refs use the complete id/generation/digest FK. A plausible
-- id and generation with a different digest cannot bind a registration.
SELECT pg_temp.assert_sqlstate($sql$
    INSERT INTO agent_control.trigger_registration_revision (
        registration_id, schema_revision, generation, record_digest, kind,
        source_key, owner_policy_owner, owner_policy_record_type,
        owner_policy_record_id, owner_policy_schema_revision,
        owner_policy_record_digest, owner_policy_generation,
        runtime_policy_owner, runtime_policy_record_type,
        runtime_policy_record_id, runtime_policy_schema_revision,
        runtime_policy_record_digest, runtime_policy_generation, enabled,
        updated_by_principal_id, updated_by_kind, updated_by_audience, updated_at
    ) SELECT
        'trigger-registration-bad-digest', schema_revision, 1, repeat('5', 64),
        kind, 'bad-digest-source', owner_policy_owner,
        owner_policy_record_type, owner_policy_record_id,
        owner_policy_schema_revision, owner_policy_record_digest,
        owner_policy_generation, runtime_policy_owner,
        runtime_policy_record_type, runtime_policy_record_id,
        runtime_policy_schema_revision, repeat('9', 64),
        runtime_policy_generation, enabled, updated_by_principal_id,
        updated_by_kind, updated_by_audience, updated_at
    FROM agent_control.trigger_registration_revision
    WHERE registration_id = 'trigger-registration-1' AND generation = 1
$sql$, '23503');

-- The cross-owner OwnerPolicy reference is narrow and exact: a dangling
-- revision and a real revision registered under the wrong trigger kind both
-- fail before a registration can become selectable.
SELECT pg_temp.assert_sqlstate($sql$
    INSERT INTO agent_control.trigger_registration_revision (
        registration_id, schema_revision, generation, record_digest, kind,
        source_key, owner_policy_owner, owner_policy_record_type,
        owner_policy_record_id, owner_policy_schema_revision,
        owner_policy_record_digest, owner_policy_generation,
        runtime_policy_owner, runtime_policy_record_type,
        runtime_policy_record_id, runtime_policy_schema_revision,
        runtime_policy_record_digest, runtime_policy_generation, enabled,
        updated_by_principal_id, updated_by_kind, updated_by_audience, updated_at
    ) SELECT
        'trigger-registration-dangling-policy', schema_revision, 1,
        repeat('6', 64), kind, 'dangling-policy-source',
        owner_policy_owner, owner_policy_record_type, 'missing-owner-policy',
        owner_policy_schema_revision, owner_policy_record_digest,
        owner_policy_generation, runtime_policy_owner,
        runtime_policy_record_type, runtime_policy_record_id,
        runtime_policy_schema_revision, runtime_policy_record_digest,
        runtime_policy_generation, enabled, updated_by_principal_id,
        updated_by_kind, updated_by_audience, updated_at
    FROM agent_control.trigger_registration_revision
    WHERE registration_id = 'trigger-registration-1' AND generation = 1
$sql$, '23503');

SELECT pg_temp.assert_sqlstate($sql$
    INSERT INTO agent_control.trigger_registration_revision (
        registration_id, schema_revision, generation, record_digest, kind,
        source_key, owner_policy_owner, owner_policy_record_type,
        owner_policy_record_id, owner_policy_schema_revision,
        owner_policy_record_digest, owner_policy_generation,
        runtime_policy_owner, runtime_policy_record_type,
        runtime_policy_record_id, runtime_policy_schema_revision,
        runtime_policy_record_digest, runtime_policy_generation, enabled,
        updated_by_principal_id, updated_by_kind, updated_by_audience, updated_at
    ) SELECT
        'trigger-registration-kind-mismatch', schema_revision, 1,
        repeat('7', 64), 'external_event', 'kind-mismatch-source',
        owner_policy_owner, owner_policy_record_type, owner_policy_record_id,
        owner_policy_schema_revision, owner_policy_record_digest,
        owner_policy_generation, runtime_policy_owner,
        runtime_policy_record_type, runtime_policy_record_id,
        runtime_policy_schema_revision, runtime_policy_record_digest,
        runtime_policy_generation, enabled, updated_by_principal_id,
        updated_by_kind, updated_by_audience, updated_at
    FROM agent_control.trigger_registration_revision
    WHERE registration_id = 'trigger-registration-1' AND generation = 1
$sql$, '23503');

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_catalog.pg_constraint AS constraint_record
        WHERE constraint_record.conrelid =
                'agent_control.trigger_registration_revision'::regclass
          AND constraint_record.contype = 'f'
          AND constraint_record.confrelid =
                'platform_governance.owner_policy_revision'::regclass
    ) THEN
        RAISE EXCEPTION 'exact OwnerPolicy revision/origin-kind foreign key is missing';
    END IF;
END
$$;

-- All application groups remain command-only. Test the full group inventory
-- through catalog ACLs and representative real LOGIN/session_user attempts.
DO $$
DECLARE
    role_name TEXT;
    table_name TEXT;
    privilege_name TEXT;
BEGIN
    FOREACH role_name IN ARRAY ARRAY[
        'alpheus_agent_control_api', 'alpheus_agent_worker',
        'alpheus_agent_delivery_dispatcher', 'alpheus_agent_delivery_repair',
        'alpheus_agent_validator', 'alpheus_agent_activator',
        'alpheus_platform_owner', 'alpheus_platform_halt',
        'alpheus_research_gateway', 'alpheus_grace_intake',
        'alpheus_grace_engine', 'alpheus_delegation_engine',
        'alpheus_agent_web', 'alpheus_agent_diagnostics',
        'alpheus_blob_gc', 'alpheus_blob_diagnostics'
    ] LOOP
        FOREACH table_name IN ARRAY ARRAY[
            'platform_governance.owner_policy_revision',
            'platform_governance.owner_policy_head',
            'platform_governance.owner_policy_event',
            'agent_control.runtime_policy_revision',
            'agent_control.runtime_policy_head',
            'agent_control.runtime_policy_event',
            'agent_control.trigger_registration_revision',
            'agent_control.trigger_registration_head',
            'agent_control.trigger_registration_event',
            'agent_control.output_contract_revision'
        ] LOOP
            FOREACH privilege_name IN ARRAY ARRAY[
                'SELECT', 'INSERT', 'UPDATE', 'DELETE', 'TRUNCATE',
                'REFERENCES', 'TRIGGER'
            ] LOOP
                IF pg_catalog.has_table_privilege(
                    role_name, table_name, privilege_name
                ) THEN
                    RAISE EXCEPTION 'role % unexpectedly has % on %',
                        role_name, privilege_name, table_name;
                END IF;
            END LOOP;
        END LOOP;
    END LOOP;
END
$$;

SELECT pg_temp.assert_role_fails(
    'alpheus_agent_control_api',
    'SELECT * FROM agent_control.runtime_policy_revision', '42501'
);
SELECT pg_temp.assert_role_fails(
    'alpheus_agent_control_api',
    $$INSERT INTO agent_control.runtime_policy_head (
        policy_id, generation, record_digest, selected_by_principal_id,
        selected_by_kind, selected_by_audience, selected_at
    ) VALUES (
        'forbidden', 1, repeat('8', 64), 'control-1',
        'workload', 'control_api', clock_timestamp()
    )$$, '42501'
);
SELECT pg_temp.assert_role_fails(
    'alpheus_agent_worker',
    'SELECT * FROM agent_control.output_contract_revision', '42501'
);
SELECT pg_temp.assert_role_fails(
    'alpheus_platform_owner',
    'SELECT * FROM platform_governance.owner_policy_revision', '42501'
);
SELECT pg_temp.assert_role_fails(
    'alpheus_agent_activator',
    $$UPDATE platform_governance.owner_policy_head
      SET generation = generation
      WHERE head_id = 'owner-policy-1'$$, '42501'
);
SELECT pg_temp.assert_role_fails(
    'alpheus_agent_diagnostics',
    'SELECT * FROM agent_control.runtime_policy_head', '42501'
);

-- 0004 adds only a non-definer trigger guard. It grants no callable Runtime
-- command and creates no Run/Task/Attempt/Turn/model/effect state.
DO $$
DECLARE
    role_name TEXT;
    guard_oid OID;
BEGIN
    SELECT routine.oid INTO STRICT guard_oid
    FROM pg_catalog.pg_proc AS routine
    WHERE routine.pronamespace = 'agent_control'::regnamespace
      AND routine.proname = 'reject_immutable_runtime_definition_mutation'
      AND pg_catalog.pg_get_function_identity_arguments(routine.oid) = ''
      AND routine.prorettype = 'trigger'::regtype
      AND NOT routine.prosecdef;

    IF EXISTS (
        SELECT 1
        FROM pg_catalog.aclexplode(
            coalesce(
                (SELECT routine.proacl FROM pg_catalog.pg_proc AS routine
                 WHERE routine.oid = guard_oid),
                pg_catalog.acldefault(
                    'f',
                    (SELECT routine.proowner FROM pg_catalog.pg_proc AS routine
                     WHERE routine.oid = guard_oid)
                )
            )
        ) AS privilege
        WHERE privilege.grantee = 0 AND privilege.privilege_type = 'EXECUTE'
    ) THEN
        RAISE EXCEPTION 'PUBLIC can execute internal Runtime trigger guard';
    END IF;

    FOREACH role_name IN ARRAY ARRAY[
        'alpheus_agent_control_api', 'alpheus_agent_worker',
        'alpheus_agent_validator', 'alpheus_agent_activator',
        'alpheus_platform_owner', 'alpheus_agent_web',
        'alpheus_agent_diagnostics'
    ] LOOP
        IF pg_catalog.has_function_privilege(role_name, guard_oid, 'EXECUTE') THEN
            RAISE EXCEPTION 'role % can execute internal Runtime trigger guard', role_name;
        END IF;
    END LOOP;

    IF to_regclass('agent_control.runtime_run') IS NOT NULL
       OR to_regclass('agent_control.runtime_task') IS NOT NULL
       OR to_regclass('agent_control.runtime_attempt') IS NOT NULL
       OR to_regclass('agent_control.runtime_turn') IS NOT NULL
       OR to_regclass('agent_control.model_call_manifest') IS NOT NULL
       OR to_regclass('agent_control.artifact') IS NOT NULL
       OR to_regclass('agent_control.artifact_publication_intent') IS NOT NULL THEN
        RAISE EXCEPTION 'definition migration created Runtime behavior/effect state';
    END IF;
END
$$;

SELECT 'ap1-runtime-definitions-pass';
