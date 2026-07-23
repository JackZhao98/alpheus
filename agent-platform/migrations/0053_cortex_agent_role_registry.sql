-- Register the first bounded Cortex Specialist responsibilities. This is a
-- responsibility/capability catalog, not a Worker permission grant and not a
-- permanent process topology.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

CREATE TABLE agent_control.cortex_agent_role_registry (
    role_id TEXT PRIMARY KEY CHECK (agent_control.runtime_identifier_valid(role_id)),
    revision INTEGER NOT NULL CHECK (revision=1),
    purpose TEXT NOT NULL CHECK (purpose<>'' AND octet_length(purpose)<=1000),
    tool_categories JSONB NOT NULL CHECK (jsonb_typeof(tool_categories)='array' AND jsonb_array_length(tool_categories)>0),
    output_contract TEXT NOT NULL CHECK (output_contract='specialist_memo_v1'),
    allowed_handoff_targets JSONB NOT NULL CHECK (allowed_handoff_targets='["decision_desk"]'::JSONB),
    max_tool_calls INTEGER NOT NULL CHECK (max_tool_calls=1),
    effect TEXT NOT NULL CHECK (effect='none'),
    active BOOLEAN NOT NULL CHECK (active),
    UNIQUE(role_id,revision)
);
CREATE TRIGGER cortex_agent_role_registry_immutable
BEFORE UPDATE OR DELETE ON agent_control.cortex_agent_role_registry
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();

INSERT INTO agent_control.cortex_agent_role_registry(
  role_id,revision,purpose,tool_categories,output_contract,allowed_handoff_targets,max_tool_calls,effect,active
) VALUES
('market_scout',1,'Interpret bounded market, index, price, liquidity, and technical evidence.','["market"]','specialist_memo_v1','["decision_desk"]',1,'none',true),
('fundamental_scout',1,'Interpret bounded company fundamentals, valuation, and financial-statement evidence.','["fundamentals"]','specialist_memo_v1','["decision_desk"]',1,'none',true),
('options_scout',1,'Interpret bounded option-chain, contract, quote, volatility, and GEX evidence.','["options","market_options"]','specialist_memo_v1','["decision_desk"]',1,'none',true),
('position_manager',1,'Interpret canonical facts for existing positions, orders, lots, eligibility, and realized P&L.','["portfolio"]','specialist_memo_v1','["decision_desk"]',1,'none',true),
('catalyst_scout',1,'Interpret bounded earnings and catalyst timing evidence.','["catalyst"]','specialist_memo_v1','["decision_desk"]',1,'none',true),
('discovery_scout',1,'Resolve bounded discovery, scanner, watchlist, search, and explicit public-web evidence.','["discovery","web"]','specialist_memo_v1','["decision_desk"]',1,'none',true);

CREATE FUNCTION agent_control.get_cortex_agent_role_registry()
RETURNS JSONB LANGUAGE plpgsql STABLE SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security SET timezone='UTC' AS $$
DECLARE invoker RECORD; result JSONB;
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api' OR invoker.owner_id<>'agent_control' THEN
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='Cortex Agent role registry denied';
  END IF;
  SELECT COALESCE(jsonb_agg(jsonb_build_object(
    'role_id',role_id,'revision',revision,'purpose',purpose,'tool_categories',tool_categories,
    'output_contract',output_contract,'allowed_handoff_targets',allowed_handoff_targets,
    'max_tool_calls',max_tool_calls,'effect',effect
  ) ORDER BY role_id),'[]'::JSONB) INTO result
  FROM agent_control.cortex_agent_role_registry WHERE active;
  RETURN result;
END $$;

REVOKE ALL ON TABLE agent_control.cortex_agent_role_registry FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.get_cortex_agent_role_registry() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agent_control.get_cortex_agent_role_registry() TO alpheus_agent_control_api;

RESET ROLE;
