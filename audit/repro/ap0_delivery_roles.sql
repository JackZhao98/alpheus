\set ON_ERROR_STOP on

CREATE TABLE public.kernel_guard (id INTEGER PRIMARY KEY);

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_roles
        WHERE rolname LIKE 'alpheus_%'
          AND (rolcanlogin OR rolsuper OR rolcreatedb OR rolcreaterole OR rolreplication OR rolbypassrls)
    ) THEN
        RAISE EXCEPTION 'agent group role has elevated cluster authority';
    END IF;
    IF pg_has_role('alpheus_agent_worker', 'alpheus_agent_activator', 'MEMBER')
       OR pg_has_role('alpheus_agent_control_api', 'alpheus_agent_delivery_repair', 'MEMBER') THEN
        RAISE EXCEPTION 'agent group roles overlap';
    END IF;
END
$$;

CREATE FUNCTION pg_temp.assert_fails(p_role TEXT, p_sql TEXT, p_expected_state TEXT)
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
    observed_state TEXT;
BEGIN
    EXECUTE format('SET LOCAL ROLE %I', p_role);
    BEGIN
        EXECUTE p_sql;
    EXCEPTION WHEN OTHERS THEN
        observed_state := SQLSTATE;
    END;
    RESET ROLE;
    IF observed_state IS NULL THEN
        RAISE EXCEPTION 'statement unexpectedly succeeded for role %', p_role;
    END IF;
    IF observed_state <> p_expected_state THEN
        RAISE EXCEPTION 'role % got SQLSTATE %, expected %', p_role, observed_state, p_expected_state;
    END IF;
END
$$;

CREATE FUNCTION pg_temp.assert_allowed(p_role TEXT, p_sql TEXT)
RETURNS void
LANGUAGE plpgsql
AS $$
BEGIN
    EXECUTE format('SET LOCAL ROLE %I', p_role);
    BEGIN
        EXECUTE p_sql;
    EXCEPTION WHEN OTHERS THEN
        RESET ROLE;
        RAISE;
    END;
    RESET ROLE;
END
$$;

SET ROLE alpheus_agent_migrator;
CREATE FUNCTION agent_control.ap0_assert_current_fails(p_sql TEXT, p_expected_state TEXT)
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
        RAISE EXCEPTION 'statement unexpectedly succeeded for session user %', session_user;
    END IF;
    IF observed_state <> p_expected_state THEN
        RAISE EXCEPTION 'session user % got SQLSTATE %, expected %', session_user, observed_state, p_expected_state;
    END IF;
END
$$;
REVOKE ALL ON FUNCTION agent_control.ap0_assert_current_fails(TEXT, TEXT) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agent_control.ap0_assert_current_fails(TEXT, TEXT) TO
    alpheus_agent_control_api, alpheus_agent_delivery_dispatcher,
    alpheus_agent_delivery_repair;
RESET ROLE;

SELECT pg_temp.assert_fails(
    'alpheus_agent_worker',
    'INSERT INTO agent_control.delivery_outbox (
        event_id, destination, schema_revision, source_owner, owner_sequence,
        event_type, event_digest, causation_id, correlation_id, event_payload,
        committed_at, available_at
     ) VALUES (
        ''forbidden'', ''grace-intake'', 1, ''agent_control'', 1,
        ''probe'', repeat(''a'', 64), ''cause'', ''correlation'', ''{}'', now(), now()
     )',
    '42501'
);
SELECT pg_temp.assert_fails(
    'alpheus_agent_control_api',
    'DELETE FROM agent_control.delivery_outbox',
    '42501'
);
SELECT pg_temp.assert_fails(
    'alpheus_agent_delivery_dispatcher',
    'SELECT * FROM agent_control.delivery_outbox',
    '42501'
);
SELECT pg_temp.assert_fails(
    'alpheus_agent_diagnostics',
    'SELECT * FROM agent_control.delivery_outbox',
    '42501'
);
SELECT pg_temp.assert_allowed(
    'alpheus_agent_diagnostics',
    'SELECT * FROM agent_control.delivery_health'
);
SELECT pg_temp.assert_fails(
    'alpheus_agent_worker',
    'INSERT INTO public.kernel_guard VALUES (1)',
    '42501'
);
SELECT pg_temp.assert_fails(
    'alpheus_agent_activator',
    'INSERT INTO public.kernel_guard VALUES (2)',
    '42501'
);
SELECT pg_temp.assert_fails(
    'alpheus_research_gateway',
    'SELECT agent_control.claim_outbox(''gateway'', ''grace-intake'', 1, 10)',
    '42501'
);

SET SESSION AUTHORIZATION "control-1";
SET ROLE alpheus_agent_control_api;
SELECT CASE WHEN agent_control.enqueue_outbox(
    'event-1', 'grace-intake', 'agent_control', 1, 'artifact_published',
    repeat('a', 64), 'task-1', 'run-1', '{"probe":1}'::jsonb,
    '2026-07-19T12:00:00Z'::timestamptz, '2026-07-19T12:00:00Z'::timestamptz
) THEN 'true' ELSE 'false' END AS first_enqueue \gset
\if :first_enqueue
\else
    \quit 1
\endif

SELECT CASE WHEN NOT agent_control.enqueue_outbox(
    'event-1', 'grace-intake', 'agent_control', 1, 'artifact_published',
    repeat('a', 64), 'task-1', 'run-1', '{"probe":1}'::jsonb,
    '2026-07-19T12:00:00Z'::timestamptz, '2026-07-19T12:00:00Z'::timestamptz
) THEN 'true' ELSE 'false' END AS exact_retry \gset
\if :exact_retry
\else
    \quit 1
\endif

SELECT agent_control.ap0_assert_current_fails(
    $$SELECT agent_control.enqueue_outbox(
        'event-1', 'grace-intake', 'agent_control', 1, 'artifact_published',
        repeat('b', 64), 'task-1', 'run-1', '{"probe":2}'::jsonb,
        '2026-07-19T12:00:00Z'::timestamptz, '2026-07-19T12:00:00Z'::timestamptz
    )$$,
    '23505'
);
SELECT agent_control.ap0_assert_current_fails(
    $$SELECT agent_control.enqueue_outbox(
        'spoofed-owner', 'grace-intake', 'kernel', 99, 'artifact_published',
        repeat('a', 64), 'task-spoof', 'run-spoof', '{}'::jsonb,
        '2026-07-19T12:00:00Z'::timestamptz, '2026-07-19T12:00:00Z'::timestamptz
    )$$,
    '22023'
);
RESET ROLE;
RESET SESSION AUTHORIZATION;

DO $$
DECLARE
    claim_definition text;
    complete_definition text;
    quarantine_definition text;
    inbox_definition text;
BEGIN
    SELECT pg_get_functiondef('agent_control.claim_outbox(text,text,integer,integer)'::regprocedure)
    INTO claim_definition;
    SELECT pg_get_functiondef('agent_control.complete_outbox(text,text,uuid)'::regprocedure)
    INTO complete_definition;
    SELECT pg_get_functiondef('agent_control.quarantine_outbox(text,text,uuid,text)'::regprocedure)
    INTO quarantine_definition;
    SELECT pg_get_functiondef('agent_control.record_inbox(text,text,text,text,bigint,text)'::regprocedure)
    INTO inbox_definition;
    IF claim_definition NOT LIKE '%lease_expires_at <= clock_timestamp()%' THEN
        RAISE EXCEPTION 'claim lease boundary is not half-open';
    END IF;
    IF complete_definition NOT LIKE '%lease_expires_at > clock_timestamp()%' THEN
        RAISE EXCEPTION 'complete lease boundary is not half-open';
    END IF;
    IF quarantine_definition NOT LIKE '%lease_expires_at > clock_timestamp()%' THEN
        RAISE EXCEPTION 'quarantine lease boundary is not half-open';
    END IF;
    IF inbox_definition NOT LIKE '%state = ''leased''%'
       OR inbox_definition NOT LIKE '%lease_expires_at > clock_timestamp()%'
       OR inbox_definition NOT LIKE '%FOR SHARE%' THEN
        RAISE EXCEPTION 'first inbox receipt is not fenced by an active leased envelope';
    END IF;
END
$$;

SET SESSION AUTHORIZATION "dispatcher-1";
SET ROLE alpheus_agent_delivery_dispatcher;
SELECT * FROM agent_control.claim_outbox('delivery-node-a', 'grace-intake', 10, 30);
RESET ROLE;
RESET SESSION AUTHORIZATION;
SELECT lease_token AS event_1_lease
FROM agent_control.delivery_outbox
WHERE event_id = 'event-1' AND destination = 'grace-intake' \gset

DO $$
BEGIN
    IF (SELECT lease_dispatcher_id FROM agent_control.delivery_outbox
        WHERE event_id = 'event-1' AND destination = 'grace-intake') <> 'delivery-node-a' THEN
        RAISE EXCEPTION 'dispatcher node label was rewritten as principal identity';
    END IF;
END
$$;

SET SESSION AUTHORIZATION "dispatcher-1";
SET ROLE alpheus_agent_delivery_dispatcher;
SELECT CASE WHEN agent_control.complete_outbox(
    'event-1', 'grace-intake', :'event_1_lease'::uuid
) THEN 'true' ELSE 'false' END AS completed \gset
\if :completed
\else
    \quit 1
\endif

SELECT CASE WHEN NOT agent_control.complete_outbox(
    'event-1', 'grace-intake', :'event_1_lease'::uuid
) THEN 'true' ELSE 'false' END AS stale_completion \gset
RESET ROLE;
RESET SESSION AUTHORIZATION;
\if :stale_completion
\else
    \quit 1
\endif

SET SESSION AUTHORIZATION "control-1";
SET ROLE alpheus_agent_control_api;
SELECT agent_control.enqueue_outbox(
    'event-inbox', 'control-api', 'agent_control', 1, 'artifact_published',
    repeat('9', 64), 'task-inbox', 'run-inbox', '{"probe":"inbox"}'::jsonb,
    '2026-07-19T12:00:02Z'::timestamptz, '2026-07-19T12:00:02Z'::timestamptz
);
SELECT agent_control.enqueue_outbox(
    'event-cross-destination', 'grace-intake', 'agent_control', 90, 'artifact_published',
    repeat('7', 64), 'task-cross', 'run-cross', '{"probe":"cross-destination"}'::jsonb,
    '2026-07-19T12:00:03Z'::timestamptz, '2026-07-19T12:00:03Z'::timestamptz
);
SELECT agent_control.enqueue_outbox(
    'event-unclaimed', 'control-api', 'agent_control', 91, 'artifact_published',
    repeat('6', 64), 'task-unclaimed', 'run-unclaimed', '{"probe":"unclaimed"}'::jsonb,
    '2026-07-19T12:00:04Z'::timestamptz, '2026-07-19T12:00:04Z'::timestamptz
);
RESET ROLE;
RESET SESSION AUTHORIZATION;
SET SESSION AUTHORIZATION "dispatcher-1";
SET ROLE alpheus_agent_delivery_dispatcher;
SELECT count(*) AS inbox_claim_count
FROM agent_control.claim_outbox('delivery-node-inbox', 'control-api', 1, 30) \gset
SELECT count(*) AS cross_claim_count
FROM agent_control.claim_outbox('delivery-node-cross', 'grace-intake', 1, 30) \gset
RESET ROLE;
RESET SESSION AUTHORIZATION;
\if :inbox_claim_count
\else
    \quit 1
\endif
\if :cross_claim_count
\else
    \quit 1
\endif

SET SESSION AUTHORIZATION "control-1";
SET ROLE alpheus_agent_control_api;
SELECT CASE WHEN agent_control.record_inbox(
    'control-api', 'event-inbox', repeat('9', 64), 'agent_control', 1, repeat('c', 64)
) THEN 'true' ELSE 'false' END AS first_inbox \gset
SELECT CASE WHEN NOT agent_control.record_inbox(
    'control-api', 'event-inbox', repeat('9', 64), 'agent_control', 1, repeat('c', 64)
) THEN 'true' ELSE 'false' END AS inbox_retry \gset
SELECT agent_control.ap0_assert_current_fails(
    $$SELECT agent_control.record_inbox(
        'control-api', 'event-inbox', repeat('b', 64), 'agent_control', 1, repeat('c', 64)
    )$$,
    '23505'
);
SELECT agent_control.ap0_assert_current_fails(
    $$SELECT agent_control.record_inbox(
        'grace-intake', 'event-1', repeat('a', 64), 'agent_control', 1, repeat('c', 64)
    )$$,
    '42501'
);
SELECT agent_control.ap0_assert_current_fails(
    $$SELECT agent_control.record_inbox(
        'control-api', 'event-cross-destination', repeat('7', 64), 'agent_control', 90, repeat('c', 64)
    )$$,
    '23503'
);
SELECT agent_control.ap0_assert_current_fails(
    $$SELECT agent_control.record_inbox(
        'control-api', 'event-unclaimed', repeat('6', 64), 'agent_control', 91, repeat('c', 64)
    )$$,
    '23503'
);
RESET ROLE;
RESET SESSION AUTHORIZATION;
\if :first_inbox
\else
    \quit 1
\endif
\if :inbox_retry
\else
    \quit 1
\endif

SET SESSION AUTHORIZATION "multi-role-attacker";
SET ROLE alpheus_agent_control_api;
SELECT agent_control.ap0_assert_current_fails(
    $$SELECT agent_control.record_inbox(
        'control-api', 'event-inbox', repeat('9', 64), 'agent_control', 1, repeat('c', 64)
    )$$,
    '42501'
);
SELECT agent_control.ap0_assert_current_fails(
    $$SELECT agent_control.enqueue_outbox(
        'event-ambiguous', 'grace-intake', 'agent_control', 98, 'probe_event',
        repeat('8', 64), 'ambiguous-cause', 'ambiguous-run', '{}'::jsonb,
        clock_timestamp(), clock_timestamp()
    )$$,
    '42501'
);
RESET ROLE;
RESET SESSION AUTHORIZATION;

SET SESSION AUTHORIZATION "control-1";
SET ROLE alpheus_agent_control_api;
SELECT agent_control.enqueue_outbox(
    'event-2', 'grace-intake', 'agent_control', 2, 'artifact_published',
    repeat('d', 64), 'task-2', 'run-2', '{"probe":2}'::jsonb,
    '2026-07-19T12:00:01Z'::timestamptz, '2026-07-19T12:00:01Z'::timestamptz
);
RESET ROLE;
RESET SESSION AUTHORIZATION;
SET SESSION AUTHORIZATION "dispatcher-1";
SET ROLE alpheus_agent_delivery_dispatcher;
SELECT * FROM agent_control.claim_outbox('dispatcher-1', 'grace-intake', 10, 30);
RESET ROLE;
RESET SESSION AUTHORIZATION;
SELECT lease_token AS event_2_lease
FROM agent_control.delivery_outbox
WHERE event_id = 'event-2' AND destination = 'grace-intake' \gset
SET SESSION AUTHORIZATION "dispatcher-1";
SET ROLE alpheus_agent_delivery_dispatcher;
SELECT CASE WHEN agent_control.quarantine_outbox(
    'event-2', 'grace-intake', :'event_2_lease'::uuid, 'unsupported_revision'
) THEN 'true' ELSE 'false' END AS quarantined \gset
RESET ROLE;
RESET SESSION AUTHORIZATION;
\if :quarantined
\else
    \quit 1
\endif

SET SESSION AUTHORIZATION "repair-1";
SET ROLE alpheus_agent_delivery_repair;
SELECT agent_control.request_outbox_replay(
    'event-2', 'grace-intake', 0, 'decoder deployed with schema support'
) AS replay_generation \gset
RESET ROLE;
RESET SESSION AUTHORIZATION;
\if :{?replay_generation}
\else
    \quit 1
\endif

SET SESSION AUTHORIZATION "dispatcher-1";
SET ROLE alpheus_agent_delivery_dispatcher;
SELECT * FROM agent_control.claim_outbox('dispatcher-2', 'grace-intake', 10, 30);
RESET ROLE;
RESET SESSION AUTHORIZATION;
SELECT lease_token AS event_2_replay_lease
FROM agent_control.delivery_outbox
WHERE event_id = 'event-2' AND destination = 'grace-intake' \gset
SET SESSION AUTHORIZATION "dispatcher-1";
SET ROLE alpheus_agent_delivery_dispatcher;
SELECT CASE WHEN agent_control.complete_outbox(
    'event-2', 'grace-intake', :'event_2_replay_lease'::uuid
) THEN 'true' ELSE 'false' END AS replay_completed \gset
RESET ROLE;
RESET SESSION AUTHORIZATION;
\if :replay_completed
\else
    \quit 1
\endif

DO $$
BEGIN
    IF (SELECT state FROM agent_control.delivery_quarantine WHERE event_id = 'event-2') <> 'resolved' THEN
        RAISE EXCEPTION 'replayed quarantine did not resolve';
    END IF;
END
$$;

SELECT updated_at AS original_policy_updated_at
FROM agent_control.delivery_policy WHERE singleton \gset
SET SESSION AUTHORIZATION "repair-1";
SET ROLE alpheus_agent_delivery_repair;
SELECT agent_control.ap0_assert_current_fails(
    format(
        'SELECT agent_control.update_delivery_policy(%L::timestamptz, 50, 10000, 100, 300, ''spoofed-repair'')',
        :'original_policy_updated_at'
    ),
    '42501'
);
SELECT agent_control.update_delivery_policy(
    :'original_policy_updated_at'::timestamptz, 50, 10000, 100, 300, 'repair-1'
) AS new_policy_updated_at \gset
SELECT agent_control.ap0_assert_current_fails(
    format(
        'SELECT agent_control.update_delivery_policy(%L::timestamptz, 50, 10000, 100, 300, ''repair-1'')',
        :'original_policy_updated_at'
    ),
    '40001'
);

SELECT agent_control.update_delivery_policy(
    :'new_policy_updated_at'::timestamptz, 1, 1, 100, 300, 'repair-1'
) AS bounded_policy_updated_at \gset
RESET ROLE;
RESET SESSION AUTHORIZATION;

SET SESSION AUTHORIZATION "control-1";
SET ROLE alpheus_agent_control_api;
SELECT agent_control.enqueue_outbox(
    'event-3', 'bounded', 'agent_control', 3, 'artifact_published',
    repeat('e', 64), 'task-3', 'run-3', '{"probe":3}'::jsonb,
    clock_timestamp(), clock_timestamp()
);
RESET ROLE;
RESET SESSION AUTHORIZATION;
SET SESSION AUTHORIZATION "dispatcher-1";
SET ROLE alpheus_agent_delivery_dispatcher;
SELECT * FROM agent_control.claim_outbox('dispatcher-bounded', 'bounded', 10, 1);
SELECT pg_sleep(1.1);
SELECT * FROM agent_control.claim_outbox('dispatcher-bounded', 'bounded', 10, 1);
RESET ROLE;
RESET SESSION AUTHORIZATION;

DO $$
BEGIN
    IF (SELECT state FROM agent_control.delivery_outbox WHERE event_id = 'event-3') <> 'quarantined' THEN
        RAISE EXCEPTION 'max-attempt event was not quarantined';
    END IF;
    IF (SELECT reason_code FROM agent_control.delivery_quarantine WHERE event_id = 'event-3') <> 'delivery_attempts_exhausted' THEN
        RAISE EXCEPTION 'max-attempt quarantine reason missing';
    END IF;
END
$$;

SET SESSION AUTHORIZATION "control-1";
SET ROLE alpheus_agent_control_api;
SELECT agent_control.enqueue_outbox(
    'event-4', 'bounded', 'agent_control', 4, 'artifact_published',
    repeat('f', 64), 'task-4', 'run-4', '{"probe":4}'::jsonb,
    clock_timestamp(), clock_timestamp()
);
RESET ROLE;
RESET SESSION AUTHORIZATION;
SET SESSION AUTHORIZATION "dispatcher-1";
SET ROLE alpheus_agent_delivery_dispatcher;
SELECT * FROM agent_control.claim_outbox('dispatcher-bounded', 'bounded', 10, 30);
RESET ROLE;
RESET SESSION AUTHORIZATION;
SELECT lease_token AS event_4_lease
FROM agent_control.delivery_outbox
WHERE event_id = 'event-4' AND destination = 'bounded' \gset
SET SESSION AUTHORIZATION "dispatcher-1";
SET ROLE alpheus_agent_delivery_dispatcher;
SELECT agent_control.ap0_assert_current_fails(
    format(
        'SELECT agent_control.quarantine_outbox(''event-4'', ''bounded'', %L::uuid, ''unsupported_revision'')',
        :'event_4_lease'
    ),
    '54000'
);
RESET ROLE;
RESET SESSION AUTHORIZATION;

SET ROLE alpheus_agent_migrator;
DROP FUNCTION agent_control.ap0_assert_current_fails(TEXT, TEXT);
RESET ROLE;

DO $$
BEGIN
    IF (SELECT count(*) FROM agent_control.delivery_policy_event) <> 3 THEN
        RAISE EXCEPTION 'delivery policy audit event missing';
    END IF;
    IF (SELECT updated_by FROM agent_control.delivery_policy WHERE singleton) <> 'repair-1'
       OR (SELECT changed_by FROM agent_control.delivery_policy_event ORDER BY event_id DESC LIMIT 1) <> 'repair-1' THEN
        RAISE EXCEPTION 'delivery policy actor was not bound to authenticated principal';
    END IF;
    IF (SELECT count(*) FROM agent_control.delivery_inbox WHERE consumer_id = 'control-api' AND event_id = 'event-inbox') <> 1 THEN
        RAISE EXCEPTION 'inbox dedupe failed';
    END IF;
END
$$;
