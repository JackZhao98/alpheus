REVOKE ALL ON SCHEMA blob FROM PUBLIC;
REVOKE ALL ON ALL TABLES IN SCHEMA blob FROM PUBLIC;
REVOKE ALL ON ALL FUNCTIONS IN SCHEMA blob FROM PUBLIC;
REVOKE ALL ON ALL SEQUENCES IN SCHEMA blob FROM PUBLIC;

GRANT USAGE ON SCHEMA blob TO
    alpheus_agent_control_api,
    alpheus_agent_worker,
    alpheus_research_gateway,
    alpheus_blob_gc,
    alpheus_blob_diagnostics;

GRANT EXECUTE ON FUNCTION blob.begin_stage(
    UUID, TEXT, TEXT, BIGINT, TEXT, BIGINT, INTEGER, TEXT
) TO alpheus_agent_control_api, alpheus_research_gateway;
GRANT EXECUTE ON FUNCTION blob.record_stage_facts(
    UUID, TEXT, TEXT, BIGINT, TEXT
) TO alpheus_agent_control_api, alpheus_research_gateway;
GRANT EXECUTE ON FUNCTION blob.commit_stage(
    UUID, TEXT, TEXT, BIGINT, TEXT, TEXT, TEXT, TEXT, TEXT
) TO alpheus_agent_control_api, alpheus_research_gateway;

GRANT EXECUTE ON FUNCTION blob.bind_agent_control_reference(
    TEXT, UUID, TEXT, TEXT, TEXT, TEXT, TEXT, TIMESTAMPTZ, TEXT
) TO alpheus_agent_control_api;
GRANT EXECUTE ON FUNCTION blob.grant_agent_control_read(
    TEXT, TEXT, TEXT, BIGINT, TEXT, TEXT
) TO alpheus_agent_control_api;
GRANT EXECUTE ON FUNCTION blob.revoke_agent_control_read(
    TEXT, TEXT, TEXT, BIGINT, TEXT, TEXT
) TO alpheus_agent_control_api;
GRANT EXECUTE ON FUNCTION blob.release_agent_control_reference(
    TEXT, TEXT, BIGINT, TEXT, TEXT
) TO alpheus_agent_control_api;

GRANT EXECUTE ON FUNCTION blob.bind_research_gateway_reference(
    TEXT, UUID, TEXT, TEXT, TEXT, TEXT, TEXT, TIMESTAMPTZ, TEXT
) TO alpheus_research_gateway;
GRANT EXECUTE ON FUNCTION blob.grant_research_gateway_read(
    TEXT, TEXT, TEXT, BIGINT, TEXT, TEXT
) TO alpheus_research_gateway;
GRANT EXECUTE ON FUNCTION blob.revoke_research_gateway_read(
    TEXT, TEXT, TEXT, BIGINT, TEXT, TEXT
) TO alpheus_research_gateway;
GRANT EXECUTE ON FUNCTION blob.release_research_gateway_reference(
    TEXT, TEXT, BIGINT, TEXT, TEXT
) TO alpheus_research_gateway;

GRANT EXECUTE ON FUNCTION blob.authorize_read(
    TEXT, TEXT, UUID, TEXT, TEXT, TEXT, TEXT
) TO alpheus_agent_control_api, alpheus_agent_worker, alpheus_research_gateway;

GRANT EXECUTE ON FUNCTION blob.claim_stage_gc(TEXT, INTEGER, INTEGER) TO alpheus_blob_gc;
GRANT EXECUTE ON FUNCTION blob.complete_stage_gc(UUID, UUID, TEXT) TO alpheus_blob_gc;
GRANT EXECUTE ON FUNCTION blob.claim_content_gc(TEXT, INTEGER, INTEGER) TO alpheus_blob_gc;
GRANT EXECUTE ON FUNCTION blob.complete_content_gc(TEXT, UUID, TEXT) TO alpheus_blob_gc;
GRANT EXECUTE ON FUNCTION blob.quarantine_content(TEXT, TEXT, TEXT) TO alpheus_blob_gc;
GRANT EXECUTE ON FUNCTION blob.update_storage_policy(
    TIMESTAMPTZ, BIGINT, INTEGER, INTEGER, BIGINT, INTEGER, INTEGER, TEXT[], TEXT
) TO alpheus_blob_gc;

GRANT SELECT ON blob.blob_health TO alpheus_blob_diagnostics;

REVOKE INSERT, UPDATE, DELETE, TRUNCATE, REFERENCES, TRIGGER
    ON ALL TABLES IN SCHEMA blob
    FROM alpheus_agent_control_api, alpheus_agent_worker, alpheus_research_gateway,
         alpheus_grace_intake, alpheus_grace_engine, alpheus_delegation_engine,
         alpheus_agent_validator, alpheus_agent_activator, alpheus_agent_web,
         alpheus_agent_diagnostics, alpheus_blob_gc, alpheus_blob_diagnostics;
