REVOKE ALL ON ALL TABLES IN SCHEMA agent_control FROM PUBLIC;
REVOKE ALL ON ALL FUNCTIONS IN SCHEMA agent_control FROM PUBLIC;
REVOKE ALL ON ALL SEQUENCES IN SCHEMA agent_control FROM PUBLIC;

GRANT EXECUTE ON FUNCTION agent_control.enqueue_outbox(
    TEXT, TEXT, TEXT, BIGINT, TEXT, TEXT, TEXT, TEXT, JSONB, TIMESTAMPTZ, TIMESTAMPTZ
) TO alpheus_agent_control_api;
GRANT EXECUTE ON FUNCTION agent_control.record_inbox(
    TEXT, TEXT, TEXT, TEXT, BIGINT, TEXT
) TO alpheus_agent_control_api;

GRANT EXECUTE ON FUNCTION agent_control.claim_outbox(TEXT, TEXT, INTEGER, INTEGER)
    TO alpheus_agent_delivery_dispatcher;
GRANT EXECUTE ON FUNCTION agent_control.complete_outbox(TEXT, TEXT, UUID)
    TO alpheus_agent_delivery_dispatcher;
GRANT EXECUTE ON FUNCTION agent_control.quarantine_outbox(TEXT, TEXT, UUID, TEXT)
    TO alpheus_agent_delivery_dispatcher;

GRANT EXECUTE ON FUNCTION agent_control.request_outbox_replay(TEXT, TEXT, INTEGER, TEXT)
    TO alpheus_agent_delivery_repair;
GRANT EXECUTE ON FUNCTION agent_control.update_delivery_policy(
    TIMESTAMPTZ, INTEGER, INTEGER, INTEGER, INTEGER, TEXT
) TO alpheus_agent_delivery_repair;

GRANT SELECT ON agent_control.delivery_health TO alpheus_agent_diagnostics;

REVOKE INSERT, UPDATE, DELETE, TRUNCATE, REFERENCES, TRIGGER
    ON ALL TABLES IN SCHEMA agent_control
    FROM alpheus_agent_worker, alpheus_research_gateway, alpheus_grace_intake,
         alpheus_grace_engine, alpheus_delegation_engine, alpheus_agent_validator,
         alpheus_agent_activator, alpheus_agent_web, alpheus_agent_diagnostics,
         alpheus_agent_delivery_dispatcher;
