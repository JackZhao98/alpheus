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

SET ROLE alpheus_agent_control_api;
SELECT CASE WHEN agent_control.enqueue_outbox(
    'event-1', 'grace-intake', 'agent_control', 1, 'artifact_published',
    repeat('a', 64), 'task-1', 'run-1', '{"probe":1}'::jsonb,
    '2026-07-19T12:00:00Z'::timestamptz, '2026-07-19T12:00:00Z'::timestamptz
) THEN 'true' ELSE 'false' END AS first_enqueue \gset
RESET ROLE;
\if :first_enqueue
\else
    \quit 1
\endif

SET ROLE alpheus_agent_control_api;
SELECT CASE WHEN NOT agent_control.enqueue_outbox(
    'event-1', 'grace-intake', 'agent_control', 1, 'artifact_published',
    repeat('a', 64), 'task-1', 'run-1', '{"probe":1}'::jsonb,
    '2026-07-19T12:00:00Z'::timestamptz, '2026-07-19T12:00:00Z'::timestamptz
) THEN 'true' ELSE 'false' END AS exact_retry \gset
RESET ROLE;
\if :exact_retry
\else
    \quit 1
\endif

SELECT pg_temp.assert_fails(
    'alpheus_agent_control_api',
    $$SELECT agent_control.enqueue_outbox(
        'event-1', 'grace-intake', 'agent_control', 1, 'artifact_published',
        repeat('b', 64), 'task-1', 'run-1', '{"probe":2}'::jsonb,
        '2026-07-19T12:00:00Z'::timestamptz, '2026-07-19T12:00:00Z'::timestamptz
    )$$,
    '23505'
);

SET ROLE alpheus_agent_delivery_dispatcher;
SELECT * FROM agent_control.claim_outbox('dispatcher-1', 'grace-intake', 10, 30);
RESET ROLE;
SELECT lease_token AS event_1_lease
FROM agent_control.delivery_outbox
WHERE event_id = 'event-1' AND destination = 'grace-intake' \gset

SET ROLE alpheus_agent_delivery_dispatcher;
SELECT CASE WHEN agent_control.complete_outbox(
    'event-1', 'grace-intake', :'event_1_lease'::uuid
) THEN 'true' ELSE 'false' END AS completed \gset
RESET ROLE;
\if :completed
\else
    \quit 1
\endif

SET ROLE alpheus_agent_delivery_dispatcher;
SELECT CASE WHEN NOT agent_control.complete_outbox(
    'event-1', 'grace-intake', :'event_1_lease'::uuid
) THEN 'true' ELSE 'false' END AS stale_completion \gset
RESET ROLE;
\if :stale_completion
\else
    \quit 1
\endif

SET ROLE alpheus_agent_control_api;
SELECT CASE WHEN agent_control.record_inbox(
    'control-api', 'event-1', repeat('a', 64), 'agent_control', 1, repeat('c', 64)
) THEN 'true' ELSE 'false' END AS first_inbox \gset
SELECT CASE WHEN NOT agent_control.record_inbox(
    'control-api', 'event-1', repeat('a', 64), 'agent_control', 1, repeat('c', 64)
) THEN 'true' ELSE 'false' END AS inbox_retry \gset
RESET ROLE;
\if :first_inbox
\else
    \quit 1
\endif
\if :inbox_retry
\else
    \quit 1
\endif
SELECT pg_temp.assert_fails(
    'alpheus_agent_control_api',
    $$SELECT agent_control.record_inbox(
        'control-api', 'event-1', repeat('b', 64), 'agent_control', 1, repeat('c', 64)
    )$$,
    '23505'
);

SET ROLE alpheus_agent_control_api;
SELECT agent_control.enqueue_outbox(
    'event-2', 'grace-intake', 'agent_control', 2, 'artifact_published',
    repeat('d', 64), 'task-2', 'run-2', '{"probe":2}'::jsonb,
    '2026-07-19T12:00:01Z'::timestamptz, '2026-07-19T12:00:01Z'::timestamptz
);
RESET ROLE;
SET ROLE alpheus_agent_delivery_dispatcher;
SELECT * FROM agent_control.claim_outbox('dispatcher-1', 'grace-intake', 10, 30);
RESET ROLE;
SELECT lease_token AS event_2_lease
FROM agent_control.delivery_outbox
WHERE event_id = 'event-2' AND destination = 'grace-intake' \gset
SET ROLE alpheus_agent_delivery_dispatcher;
SELECT CASE WHEN agent_control.quarantine_outbox(
    'event-2', 'grace-intake', :'event_2_lease'::uuid, 'unsupported_revision'
) THEN 'true' ELSE 'false' END AS quarantined \gset
RESET ROLE;
\if :quarantined
\else
    \quit 1
\endif

SET ROLE alpheus_agent_delivery_repair;
SELECT agent_control.request_outbox_replay(
    'event-2', 'grace-intake', 0, 'decoder deployed with schema support'
) AS replay_generation \gset
RESET ROLE;
\if :{?replay_generation}
\else
    \quit 1
\endif

SET ROLE alpheus_agent_delivery_dispatcher;
SELECT * FROM agent_control.claim_outbox('dispatcher-2', 'grace-intake', 10, 30);
RESET ROLE;
SELECT lease_token AS event_2_replay_lease
FROM agent_control.delivery_outbox
WHERE event_id = 'event-2' AND destination = 'grace-intake' \gset
SET ROLE alpheus_agent_delivery_dispatcher;
SELECT CASE WHEN agent_control.complete_outbox(
    'event-2', 'grace-intake', :'event_2_replay_lease'::uuid
) THEN 'true' ELSE 'false' END AS replay_completed \gset
RESET ROLE;
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
SET ROLE alpheus_agent_delivery_repair;
SELECT agent_control.update_delivery_policy(
    :'original_policy_updated_at'::timestamptz, 50, 10000, 100, 300, 'delivery-repair-1'
) AS new_policy_updated_at \gset
RESET ROLE;
SELECT pg_temp.assert_fails(
    'alpheus_agent_delivery_repair',
    format(
        'SELECT agent_control.update_delivery_policy(%L::timestamptz, 50, 10000, 100, 300, ''delivery-repair-1'')',
        :'original_policy_updated_at'
    ),
    '40001'
);

SET ROLE alpheus_agent_delivery_repair;
SELECT agent_control.update_delivery_policy(
    :'new_policy_updated_at'::timestamptz, 1, 1, 100, 300, 'delivery-repair-1'
) AS bounded_policy_updated_at \gset
RESET ROLE;

SET ROLE alpheus_agent_control_api;
SELECT agent_control.enqueue_outbox(
    'event-3', 'bounded', 'agent_control', 3, 'artifact_published',
    repeat('e', 64), 'task-3', 'run-3', '{"probe":3}'::jsonb,
    clock_timestamp(), clock_timestamp()
);
RESET ROLE;
SET ROLE alpheus_agent_delivery_dispatcher;
SELECT * FROM agent_control.claim_outbox('dispatcher-bounded', 'bounded', 10, 1);
RESET ROLE;
SELECT pg_sleep(1.1);
SET ROLE alpheus_agent_delivery_dispatcher;
SELECT * FROM agent_control.claim_outbox('dispatcher-bounded', 'bounded', 10, 1);
RESET ROLE;

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

SET ROLE alpheus_agent_control_api;
SELECT agent_control.enqueue_outbox(
    'event-4', 'bounded', 'agent_control', 4, 'artifact_published',
    repeat('f', 64), 'task-4', 'run-4', '{"probe":4}'::jsonb,
    clock_timestamp(), clock_timestamp()
);
RESET ROLE;
SET ROLE alpheus_agent_delivery_dispatcher;
SELECT * FROM agent_control.claim_outbox('dispatcher-bounded', 'bounded', 10, 30);
RESET ROLE;
SELECT lease_token AS event_4_lease
FROM agent_control.delivery_outbox
WHERE event_id = 'event-4' AND destination = 'bounded' \gset
SELECT pg_temp.assert_fails(
    'alpheus_agent_delivery_dispatcher',
    format(
        'SELECT agent_control.quarantine_outbox(''event-4'', ''bounded'', %L::uuid, ''unsupported_revision'')',
        :'event_4_lease'
    ),
    '54000'
);

DO $$
BEGIN
    IF (SELECT count(*) FROM agent_control.delivery_policy_event) <> 3 THEN
        RAISE EXCEPTION 'delivery policy audit event missing';
    END IF;
    IF (SELECT count(*) FROM agent_control.delivery_inbox WHERE consumer_id = 'control-api' AND event_id = 'event-1') <> 1 THEN
        RAISE EXCEPTION 'inbox dedupe failed';
    END IF;
END
$$;
