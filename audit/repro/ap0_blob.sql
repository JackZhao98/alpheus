\set ON_ERROR_STOP on

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

SELECT pg_temp.assert_fails('alpheus_agent_control_api', 'SELECT * FROM blob.blob_object', '42501');
SELECT pg_temp.assert_fails('alpheus_research_gateway', 'DELETE FROM blob.blob_reference', '42501');
SELECT pg_temp.assert_fails('alpheus_blob_gc', 'INSERT INTO blob.lifecycle_event (
    subject_kind, subject_id, transition, generation, actor, reason_code
) VALUES (''blob'', ''forbidden'', ''deleted'', 1, ''gc'', ''forbidden_write'')', '42501');
SELECT pg_temp.assert_fails('alpheus_blob_diagnostics', 'SELECT * FROM blob.blob_content', '42501');
SELECT pg_temp.assert_allowed('alpheus_blob_diagnostics', 'SELECT * FROM blob.blob_health');
SELECT pg_temp.assert_fails('alpheus_agent_worker', $$SELECT * FROM blob.begin_stage(
    '11111111-1111-4111-8111-111111111111', 'user-1', 'text/plain; charset=utf-8',
    5, NULL, NULL, 60, 'worker'
)$$, '42501');

SELECT pg_temp.assert_fails('alpheus_agent_control_api', $$SELECT * FROM blob.begin_stage(
    '11111111-1111-4111-8111-111111111111', 'user-1', 'text/plain; charset=utf-8',
    20971521, NULL, NULL, 60, 'control-api'
)$$, '22023');

SET ROLE alpheus_agent_control_api;
SELECT * FROM blob.begin_stage(
    '11111111-1111-4111-8111-111111111111', 'user-1', 'text/plain; charset=utf-8',
    5, '2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824',
    5, 60, 'control-api'
) \gset stage_
RESET ROLE;

SET ROLE alpheus_agent_control_api;
SELECT * FROM blob.begin_stage(
    '11111111-1111-4111-8111-111111111111', 'user-1', 'text/plain; charset=utf-8',
    5, '2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824',
    5, 60, 'control-api'
);
RESET ROLE;

SELECT pg_temp.assert_fails('alpheus_agent_control_api', $$SELECT * FROM blob.begin_stage(
    '11111111-1111-4111-8111-111111111111', 'user-1', 'text/plain; charset=utf-8',
    4, NULL, 4, 60, 'control-api'
)$$, '23505');

SELECT pg_temp.assert_fails('alpheus_agent_control_api', $$SELECT * FROM blob.commit_stage(
    '11111111-1111-4111-8111-111111111111', 'user-1', repeat('b', 64), 5,
    'agent_control', 'raw_document', 'raw-1', repeat('a', 64), 'control-api'
)$$, '22023');

SELECT pg_temp.assert_fails('alpheus_agent_control_api', $$SELECT blob.record_stage_facts(
    '11111111-1111-4111-8111-111111111111', 'user-1', repeat('b', 64), 5, 'control-api'
)$$, '22023');

SET ROLE alpheus_agent_control_api;
SELECT blob.record_stage_facts(
    '11111111-1111-4111-8111-111111111111', 'user-1',
    '2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824', 5,
    'control-api'
);
SELECT CASE WHEN NOT blob.record_stage_facts(
    '11111111-1111-4111-8111-111111111111', 'user-1',
    '2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824', 5,
    'control-api'
) THEN 'true' ELSE 'false' END AS exact_stage_facts \gset
RESET ROLE;
\if :exact_stage_facts
\else
    \quit 1
\endif

SELECT pg_temp.assert_fails('alpheus_agent_control_api', $$SELECT blob.bind_agent_control_reference(
    'staged-binding', '99999999-9999-4999-8999-999999999999', 'user_request', 'request-0',
    repeat('a', 64), 'user-1', 'private', clock_timestamp() + interval '1 day', 'control-api'
)$$, '55000');

SET ROLE alpheus_agent_control_api;
SELECT * FROM blob.commit_stage(
    '11111111-1111-4111-8111-111111111111', 'user-1',
    '2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824', 5,
    'agent_control', 'raw_document', 'raw-1', repeat('a', 64), 'control-api'
) \gset committed_
RESET ROLE;

SET ROLE alpheus_agent_control_api;
SELECT * FROM blob.commit_stage(
    '11111111-1111-4111-8111-111111111111', 'user-1',
    '2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824', 5,
    'agent_control', 'raw_document', 'raw-1', repeat('a', 64), 'control-api'
);
RESET ROLE;

SELECT pg_temp.assert_fails('alpheus_agent_control_api', format($sql$SELECT * FROM blob.commit_stage(
    '11111111-1111-4111-8111-111111111111', 'user-1',
    '2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824', 5,
    'agent_control', 'raw_document', 'changed-origin', repeat('a', 64), 'control-api'
)$sql$), '23505');

SET ROLE alpheus_agent_control_api;
SELECT blob.bind_agent_control_reference(
    'attachment-private', :'committed_blob_id'::uuid, 'user_request', 'request-1',
    repeat('c', 64), 'user-1', 'private', clock_timestamp() + interval '1 day', 'control-api'
);
SELECT blob.bind_agent_control_reference(
    'attachment-explicit', :'committed_blob_id'::uuid, 'user_request', 'request-2',
    repeat('d', 64), 'user-1', 'explicit', clock_timestamp() + interval '1 day', 'control-api'
);
RESET ROLE;

SELECT pg_temp.assert_fails('alpheus_research_gateway', format($sql$SELECT blob.bind_agent_control_reference(
    'research-escalation', %L::uuid, 'user_request', 'request-x', repeat('e', 64),
    'user-1', 'private', clock_timestamp() + interval '1 day', 'research-gateway'
)$sql$, :'committed_blob_id'), '42501');

SET ROLE alpheus_agent_worker;
SELECT count(*) AS owner_read_count FROM blob.authorize_read(
    'user-1', 'attachment-private', :'committed_blob_id'::uuid,
    'agent_control', 'user_request', 'request-1', repeat('c', 64)
) \gset
SELECT count(*) AS denied_read_count FROM blob.authorize_read(
    'user-2', 'attachment-private', :'committed_blob_id'::uuid,
    'agent_control', 'user_request', 'request-1', repeat('c', 64)
) \gset
SELECT count(*) AS guessed_digest_count FROM blob.authorize_read(
    'user-1', 'wrong-binding', :'committed_blob_id'::uuid,
    'agent_control', 'user_request', 'request-1', repeat('c', 64)
) \gset
RESET ROLE;
\if :owner_read_count
\else
    \quit 1
\endif
\if :denied_read_count
    \quit 1
\endif
\if :guessed_digest_count
    \quit 1
\endif

SET ROLE alpheus_agent_control_api;
SELECT blob.grant_agent_control_read(
    'attachment-explicit', 'user-1', 'user-2', 0, 'collaboration_grant', 'control-api'
) AS acl_generation \gset
RESET ROLE;
SET ROLE alpheus_agent_worker;
SELECT count(*) AS granted_read_count FROM blob.authorize_read(
    'user-2', 'attachment-explicit', :'committed_blob_id'::uuid,
    'agent_control', 'user_request', 'request-2', repeat('d', 64)
) \gset
RESET ROLE;
\if :granted_read_count
\else
    \quit 1
\endif
SET ROLE alpheus_agent_control_api;
SELECT blob.revoke_agent_control_read(
    'attachment-explicit', 'user-1', 'user-2', :'acl_generation'::bigint,
    'collaboration_revoked', 'control-api'
);
RESET ROLE;
SET ROLE alpheus_agent_worker;
SELECT count(*) AS revoked_read_count FROM blob.authorize_read(
    'user-2', 'attachment-explicit', :'committed_blob_id'::uuid,
    'agent_control', 'user_request', 'request-2', repeat('d', 64)
) \gset
RESET ROLE;
\if :revoked_read_count
    \quit 1
\endif

SELECT updated_at AS original_policy_updated_at FROM blob.storage_policy WHERE singleton \gset
SET ROLE alpheus_blob_gc;
SELECT blob.update_storage_policy(
    :'original_policy_updated_at'::timestamptz, 20971520, 3600, 1, 315576000, 100, 300,
    ARRAY['application/json', 'application/pdf', 'image/jpeg', 'image/png',
          'text/markdown; charset=utf-8', 'text/plain; charset=utf-8'], 'blob-gc'
) AS new_policy_updated_at \gset
RESET ROLE;
SELECT pg_temp.assert_fails('alpheus_blob_gc', format($sql$SELECT blob.update_storage_policy(
    %L::timestamptz, 20971520, 3600, 1, 315576000, 100, 300,
    ARRAY['text/plain; charset=utf-8'], 'blob-gc'
)$sql$, :'original_policy_updated_at'), '40001');

UPDATE blob.blob_content
SET updated_at = clock_timestamp() - interval '10 seconds'
WHERE content_digest = :'committed_content_digest';
SET ROLE alpheus_blob_gc;
SELECT count(*) AS retained_gc_count FROM blob.claim_content_gc('blob-gc', 10, 30) \gset
RESET ROLE;
\if :retained_gc_count
    \quit 1
\endif

SET ROLE alpheus_agent_control_api;
SELECT blob.release_agent_control_reference(
    'attachment-private', 'user-1', 1, 'owner_release', 'control-api'
);
SELECT blob.release_agent_control_reference(
    'attachment-explicit', 'user-1', 1, 'owner_release', 'control-api'
);
RESET ROLE;

SET ROLE alpheus_blob_gc;
SELECT * FROM blob.claim_content_gc('blob-gc', 10, 30) \gset gc_
RESET ROLE;
SET ROLE alpheus_blob_gc;
SELECT CASE WHEN NOT blob.complete_content_gc(
    :'gc_content_digest', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', 'blob-gc'
) THEN 'true' ELSE 'false' END AS stale_gc_completion \gset
SELECT CASE WHEN blob.complete_content_gc(
    :'gc_content_digest', :'gc_claim_token'::uuid, 'blob-gc'
) THEN 'true' ELSE 'false' END AS gc_completed \gset
RESET ROLE;
\if :stale_gc_completion
\else
    \quit 1
\endif
\if :gc_completed
\else
    \quit 1
\endif

SET ROLE alpheus_agent_worker;
SELECT count(*) AS deleted_read_count FROM blob.authorize_read(
    'user-1', 'attachment-private', :'committed_blob_id'::uuid,
    'agent_control', 'user_request', 'request-1', repeat('c', 64)
) \gset
RESET ROLE;
\if :deleted_read_count
    \quit 1
\endif

SET ROLE alpheus_agent_control_api;
SELECT * FROM blob.begin_stage(
    '44444444-4444-4444-8444-444444444444', 'user-1', 'application/json',
    1024, NULL, NULL, 60, 'control-api'
);
RESET ROLE;
UPDATE blob.blob_stage
SET created_at = clock_timestamp() - interval '2 seconds',
    expires_at = clock_timestamp() - interval '1 second'
WHERE stage_id = '44444444-4444-4444-8444-444444444444';
SET ROLE alpheus_blob_gc;
SELECT * FROM blob.claim_stage_gc('blob-gc', 10, 30) \gset stage_gc_
SELECT CASE WHEN blob.complete_stage_gc(
    :'stage_gc_stage_id'::uuid, :'stage_gc_claim_token'::uuid, 'blob-gc'
) THEN 'true' ELSE 'false' END AS stage_gc_completed \gset
RESET ROLE;
\if :stage_gc_completed
\else
    \quit 1
\endif

DO $$
BEGIN
    IF (SELECT state FROM blob.blob_content WHERE content_digest =
        '2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824') <> 'deleted' THEN
        RAISE EXCEPTION 'orphan content did not delete';
    END IF;
    IF (SELECT state FROM blob.blob_stage WHERE stage_id =
        '44444444-4444-4444-8444-444444444444') <> 'aborted' THEN
        RAISE EXCEPTION 'expired stage did not clean';
    END IF;
    IF (SELECT count(*) FROM blob.storage_policy_event) <> 2 THEN
        RAISE EXCEPTION 'blob policy audit event missing';
    END IF;
    IF (SELECT count(*) FROM blob.lifecycle_event) < 10 THEN
        RAISE EXCEPTION 'blob lifecycle audit evidence incomplete';
    END IF;
END
$$;
