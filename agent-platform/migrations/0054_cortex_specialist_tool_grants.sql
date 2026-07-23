-- Bind each effect-free read Tool to one bounded Specialist responsibility.
-- The two order-review preflight Tools intentionally receive no Specialist
-- grant and remain Decision Desk-only.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

CREATE TABLE agent_control.cortex_specialist_tool_grant (
    role_id TEXT NOT NULL REFERENCES agent_control.cortex_agent_role_registry(role_id),
    tool_id TEXT NOT NULL CHECK (agent_control.runtime_identifier_valid(tool_id)),
    effect TEXT NOT NULL CHECK (effect='read_only'),
    granted_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY(role_id,tool_id),
    UNIQUE(tool_id)
);
CREATE TRIGGER cortex_specialist_tool_grant_immutable
BEFORE UPDATE OR DELETE ON agent_control.cortex_specialist_tool_grant
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();

INSERT INTO agent_control.cortex_specialist_tool_grant(role_id,tool_id,effect,granted_at) VALUES
('discovery_scout','research_web_fetch','read_only',clock_timestamp()),
('options_scout','research_gexbot_as_of','read_only',clock_timestamp()),
('position_manager','kernel_accounts','read_only',clock_timestamp()),
('catalyst_scout','kernel_earnings_calendar','read_only',clock_timestamp()),
('catalyst_scout','kernel_earnings_results','read_only',clock_timestamp()),
('fundamental_scout','kernel_equity_fundamentals','read_only',clock_timestamp()),
('fundamental_scout','kernel_financials','read_only',clock_timestamp()),
('market_scout','kernel_equity_historicals','read_only',clock_timestamp()),
('market_scout','kernel_equity_price_book','read_only',clock_timestamp()),
('market_scout','kernel_equity_quotes','read_only',clock_timestamp()),
('market_scout','kernel_equity_technical_indicators','read_only',clock_timestamp()),
('market_scout','kernel_equity_tradability','read_only',clock_timestamp()),
('market_scout','kernel_indexes','read_only',clock_timestamp()),
('market_scout','kernel_index_quotes','read_only',clock_timestamp()),
('options_scout','kernel_option_chains','read_only',clock_timestamp()),
('options_scout','kernel_option_instruments','read_only',clock_timestamp()),
('options_scout','kernel_option_quotes','read_only',clock_timestamp()),
('options_scout','kernel_option_watchlist','read_only',clock_timestamp()),
('position_manager','kernel_option_level_upgrade_info','read_only',clock_timestamp()),
('position_manager','kernel_equity_positions','read_only',clock_timestamp()),
('position_manager','kernel_option_positions','read_only',clock_timestamp()),
('position_manager','kernel_equity_orders','read_only',clock_timestamp()),
('position_manager','kernel_option_orders','read_only',clock_timestamp()),
('position_manager','kernel_equity_tax_lots','read_only',clock_timestamp()),
('position_manager','kernel_portfolio','read_only',clock_timestamp()),
('position_manager','kernel_pnl_trade_history','read_only',clock_timestamp()),
('position_manager','kernel_realized_pnl','read_only',clock_timestamp()),
('discovery_scout','kernel_popular_watchlists','read_only',clock_timestamp()),
('discovery_scout','kernel_watchlists','read_only',clock_timestamp()),
('discovery_scout','kernel_watchlist_items','read_only',clock_timestamp()),
('discovery_scout','kernel_scanner_filter_specs','read_only',clock_timestamp()),
('discovery_scout','kernel_scans','read_only',clock_timestamp()),
('discovery_scout','kernel_run_scan','read_only',clock_timestamp()),
('discovery_scout','kernel_search','read_only',clock_timestamp());

CREATE FUNCTION agent_control.get_cortex_specialist_tool_grants()
RETURNS JSONB LANGUAGE plpgsql STABLE SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security SET timezone='UTC' AS $$
DECLARE invoker RECORD; result JSONB;
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api' OR invoker.owner_id<>'agent_control' THEN
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='Cortex Specialist Tool grants denied';
  END IF;
  SELECT COALESCE(jsonb_agg(jsonb_build_object(
    'role_id',role_id,'tool_id',tool_id,'effect',effect
  ) ORDER BY role_id,tool_id),'[]'::JSONB) INTO result
  FROM agent_control.cortex_specialist_tool_grant;
  RETURN result;
END $$;

REVOKE ALL ON TABLE agent_control.cortex_specialist_tool_grant FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.get_cortex_specialist_tool_grants() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agent_control.get_cortex_specialist_tool_grants() TO alpheus_agent_control_api;

RESET ROLE;
