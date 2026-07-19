\set ON_ERROR_STOP on

SET ROLE alpheus_platform_owner;
SELECT platform_governance.create_revision(
    'platform_mode', 'global', '10000000-0000-4000-8000-000000000001', 1,
    'read_only', NULL, repeat('a', 64), 'owner-1', 'initial_read_only'
);
SELECT platform_governance.issue_activation_receipt(
    '20000000-0000-4000-8000-000000000001', repeat('b', 64),
    'platform_mode', 'global', '10000000-0000-4000-8000-000000000001', 1,
    repeat('a', 64), 0, 'raise', 'owner-1', 'read_only', 'external_read',
    repeat('c', 64), 'initial_read_only', clock_timestamp() - interval '1 second',
    clock_timestamp() + interval '10 minutes'
);
SELECT platform_governance.create_revision(
    'kill_switch', 'strategy_activation', '40000000-0000-4000-8000-000000000301', 1,
    'strategy_activation', 'enabled', repeat('a', 64), 'owner-1', 'concurrent_bootstrap'
);
SELECT platform_governance.issue_activation_receipt(
    '20000000-0000-4000-8000-000000000301', repeat('d', 64),
    'kill_switch', 'strategy_activation', '40000000-0000-4000-8000-000000000301', 1,
    repeat('a', 64), 0, 'resume', 'owner-1', 'read_only', 'external_read',
    repeat('c', 64), 'concurrent_bootstrap', clock_timestamp() - interval '1 second',
    clock_timestamp() + interval '10 minutes'
);
RESET ROLE;

SET ROLE alpheus_agent_activator;
SELECT * FROM platform_governance.activate_head(
    '20000000-0000-4000-8000-000000000001', 0, 'activator-1'
);
-- Exact retry is idempotent while the receipt is current.
SELECT * FROM platform_governance.activate_head(
    '20000000-0000-4000-8000-000000000001', 0, 'activator-1'
);
RESET ROLE;

DO $$
BEGIN
    BEGIN
        UPDATE platform_governance.platform_mode_revision
        SET reason_code = 'mutated'
        WHERE revision_id = '10000000-0000-4000-8000-000000000001';
        RAISE EXCEPTION 'immutable revision update unexpectedly allowed';
    EXCEPTION WHEN object_not_in_prerequisite_state THEN NULL;
    END;
    BEGIN
        DELETE FROM platform_governance.activation_receipt_consumption
        WHERE receipt_id = '20000000-0000-4000-8000-000000000001';
        RAISE EXCEPTION 'immutable receipt consumption delete unexpectedly allowed';
    EXCEPTION WHEN object_not_in_prerequisite_state THEN NULL;
    END;
END
$$;

SET ROLE alpheus_agent_control_api;
DO $$
BEGIN
    BEGIN
        PERFORM 1 FROM platform_governance.platform_mode_revision;
        RAISE EXCEPTION 'control-api read base table unexpectedly allowed';
    EXCEPTION WHEN insufficient_privilege THEN NULL;
    END;
    BEGIN
        PERFORM platform_governance.create_revision(
            'platform_mode', 'global', gen_random_uuid(), 2,
            'shadow', NULL, repeat('d', 64), 'control-1', 'forbidden_write'
        );
        RAISE EXCEPTION 'control-api candidate write unexpectedly allowed';
    EXCEPTION WHEN insufficient_privilege THEN NULL;
    END;
    IF (SELECT count(*) FROM platform_governance.current_head) <> 1 THEN
        RAISE EXCEPTION 'control-api current-head projection unavailable';
    END IF;
END
$$;
RESET ROLE;

SET ROLE alpheus_platform_owner;
DO $$
BEGIN
    BEGIN
        PERFORM * FROM platform_governance.activate_head(
            '20000000-0000-4000-8000-000000000001', 0, 'owner-1'
        );
        RAISE EXCEPTION 'owner activation unexpectedly allowed';
    EXCEPTION WHEN insufficient_privilege THEN NULL;
    END;
END
$$;
RESET ROLE;

SET ROLE alpheus_agent_activator;
DO $$
BEGIN
    BEGIN
        PERFORM platform_governance.create_revision(
            'platform_mode', 'global', gen_random_uuid(), 2,
            'shadow', NULL, repeat('d', 64), 'activator-1', 'forbidden_author'
        );
        RAISE EXCEPTION 'activator authoring unexpectedly allowed';
    EXCEPTION WHEN insufficient_privilege THEN NULL;
    END;
END
$$;
RESET ROLE;

-- The least-privilege path can only create a more restrictive revision.
SET ROLE alpheus_platform_halt;
SELECT platform_governance.emergency_halt(
    'platform_mode', 'global', 1, '10000000-0000-4000-8000-000000000002',
    repeat('d', 64), 'halt-1', 'emergency_halt'
);
DO $$
BEGIN
    BEGIN
        PERFORM * FROM platform_governance.activate_head(
            '20000000-0000-4000-8000-000000000001', 0, 'halt-1'
        );
        RAISE EXCEPTION 'halt role activation unexpectedly allowed';
    EXCEPTION WHEN insufficient_privilege THEN NULL;
    END;
END
$$;
RESET ROLE;

-- The old receipt cannot overwrite the emergency generation.
SET ROLE alpheus_agent_activator;
DO $$
BEGIN
    BEGIN
        PERFORM * FROM platform_governance.activate_head(
            '20000000-0000-4000-8000-000000000001', 0, 'activator-1'
        );
        RAISE EXCEPTION 'stale receipt unexpectedly activated';
    EXCEPTION WHEN serialization_failure THEN NULL;
    END;
END
$$;
RESET ROLE;

-- Exercise the two other typed head families. Missing heads are halted; only
-- the emergency role may bootstrap that least-permissive state without a
-- receipt, and only the Activator may resume an exact successor.
SET ROLE alpheus_platform_halt;
SELECT platform_governance.emergency_halt(
    'effect_class', 'external_read', 0, '30000000-0000-4000-8000-000000000001',
    repeat('2', 64), 'halt-1', 'bootstrap_halted'
);
SELECT platform_governance.emergency_halt(
    'kill_switch', 'capability_external_execution', 0, '40000000-0000-4000-8000-000000000001',
    repeat('3', 64), 'halt-1', 'bootstrap_halted'
);
RESET ROLE;

SET ROLE alpheus_platform_owner;
SELECT platform_governance.create_revision(
    'effect_class', 'external_read', '30000000-0000-4000-8000-000000000002', 2,
    'external_read', 'enabled', repeat('4', 64), 'owner-1', 'enable_external_read'
);
SELECT platform_governance.issue_activation_receipt(
    '20000000-0000-4000-8000-000000000102', repeat('5', 64),
    'effect_class', 'external_read', '30000000-0000-4000-8000-000000000002', 2,
    repeat('4', 64), 1, 'resume', 'owner-1', 'read_only', 'external_read',
    repeat('6', 64), 'enable_external_read', clock_timestamp() - interval '1 second',
    clock_timestamp() + interval '10 minutes'
);
SELECT platform_governance.create_revision(
    'kill_switch', 'capability_external_execution', '40000000-0000-4000-8000-000000000002', 2,
    'capability_external_execution', 'enabled', repeat('7', 64), 'owner-1', 'enable_external_tools'
);
SELECT platform_governance.issue_activation_receipt(
    '20000000-0000-4000-8000-000000000202', repeat('8', 64),
    'kill_switch', 'capability_external_execution', '40000000-0000-4000-8000-000000000002', 2,
    repeat('7', 64), 1, 'resume', 'owner-1', 'read_only', 'external_read',
    repeat('9', 64), 'enable_external_tools', clock_timestamp() - interval '1 second',
    clock_timestamp() + interval '10 minutes'
);
RESET ROLE;

SET ROLE alpheus_agent_activator;
SELECT * FROM platform_governance.activate_head(
    '20000000-0000-4000-8000-000000000102', 1, 'activator-1'
);
SELECT * FROM platform_governance.activate_head(
    '20000000-0000-4000-8000-000000000202', 1, 'activator-1'
);
RESET ROLE;

-- Prepare one exact successor for the shell concurrency barrier.
SET ROLE alpheus_platform_owner;
SELECT platform_governance.create_revision(
    'platform_mode', 'global', '10000000-0000-4000-8000-000000000003', 3,
    'read_only', NULL, repeat('e', 64), 'owner-1', 'concurrent_read_only'
);
SELECT platform_governance.issue_activation_receipt(
    '20000000-0000-4000-8000-000000000003', repeat('f', 64),
    'platform_mode', 'global', '10000000-0000-4000-8000-000000000003', 3,
    repeat('e', 64), 2, 'raise', 'owner-1', 'read_only', 'external_read',
    repeat('1', 64), 'concurrent_read_only', clock_timestamp() - interval '1 second',
    clock_timestamp() + interval '10 minutes'
);
RESET ROLE;

SELECT 'ap0-governance-base-pass' WHERE
   (SELECT generation = 2 AND mode = 'disabled' AND activation_receipt_id IS NULL
        FROM platform_governance.platform_mode_head WHERE head_id = 'global')
   AND (SELECT generation = 2 AND state = 'enabled' FROM platform_governance.effect_class_head
        WHERE effect_class = 'external_read')
   AND (SELECT generation = 2 AND state = 'enabled' FROM platform_governance.kill_switch_head
        WHERE switch_id = 'capability_external_execution')
   AND (SELECT count(*) = 6 FROM platform_governance.governance_event)
   AND (SELECT count(*) = 3 FROM platform_governance.activation_receipt_consumption);
