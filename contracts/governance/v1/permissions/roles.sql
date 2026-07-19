REVOKE ALL ON SCHEMA platform_governance FROM PUBLIC;
REVOKE ALL ON ALL TABLES IN SCHEMA platform_governance FROM PUBLIC;
REVOKE ALL ON ALL FUNCTIONS IN SCHEMA platform_governance FROM PUBLIC;
REVOKE ALL ON ALL SEQUENCES IN SCHEMA platform_governance FROM PUBLIC;

GRANT USAGE ON SCHEMA platform_governance TO
    alpheus_platform_owner,
    alpheus_platform_halt,
    alpheus_agent_activator,
    alpheus_agent_control_api,
    alpheus_agent_worker,
    alpheus_research_gateway,
    alpheus_grace_intake,
    alpheus_grace_engine,
    alpheus_delegation_engine,
    alpheus_agent_validator,
    alpheus_agent_web,
    alpheus_agent_diagnostics;

GRANT EXECUTE ON FUNCTION platform_governance.create_revision(
    TEXT, TEXT, UUID, BIGINT, TEXT, TEXT, TEXT, TEXT, TEXT
) TO alpheus_platform_owner;
GRANT EXECUTE ON FUNCTION platform_governance.issue_activation_receipt(
    UUID, TEXT, TEXT, TEXT, UUID, BIGINT, TEXT, BIGINT, TEXT, TEXT,
    TEXT, TEXT, TEXT, TEXT, TIMESTAMPTZ, TIMESTAMPTZ
) TO alpheus_platform_owner;

GRANT EXECUTE ON FUNCTION platform_governance.activate_head(UUID, BIGINT, TEXT)
    TO alpheus_agent_activator;

GRANT EXECUTE ON FUNCTION platform_governance.emergency_halt(
    TEXT, TEXT, BIGINT, UUID, TEXT, TEXT, TEXT
) TO alpheus_platform_halt;

GRANT SELECT ON platform_governance.current_head TO
    alpheus_agent_control_api,
    alpheus_agent_worker,
    alpheus_research_gateway,
    alpheus_grace_intake,
    alpheus_grace_engine,
    alpheus_delegation_engine,
    alpheus_agent_validator,
    alpheus_agent_activator;
GRANT SELECT ON platform_governance.governance_health
    TO alpheus_agent_web, alpheus_agent_diagnostics;

REVOKE INSERT, UPDATE, DELETE, TRUNCATE, REFERENCES, TRIGGER
    ON ALL TABLES IN SCHEMA platform_governance
    FROM alpheus_platform_owner, alpheus_platform_halt, alpheus_agent_activator,
         alpheus_agent_control_api, alpheus_agent_worker, alpheus_research_gateway,
         alpheus_grace_intake, alpheus_grace_engine, alpheus_delegation_engine,
         alpheus_agent_validator, alpheus_agent_web, alpheus_agent_diagnostics;
