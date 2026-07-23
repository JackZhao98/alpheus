-- A model-selected role is not an authorization decision. Every Tool
-- authorization now checks the immutable handoff against the reviewed
-- Specialist grant matrix. The two non-executing order preflights remain
-- Decision Desk-only.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

CREATE FUNCTION agent_control.enforce_cortex_specialist_tool_grant(
    p_source_call_id TEXT,p_tool_id TEXT
) RETURNS VOID LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security SET timezone='UTC' AS $$
DECLARE invoker RECORD; target TEXT;
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api' OR invoker.owner_id<>'agent_control'
    OR NOT agent_control.runtime_identifier_valid(p_source_call_id)
    OR NOT agent_control.runtime_identifier_valid(p_tool_id) THEN
    RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid Cortex Specialist Tool grant check';
  END IF;
  SELECT handoff.target_role INTO target
    FROM agent_control.cortex_handoff handoff
    WHERE handoff.source_call_id=p_source_call_id
    FOR SHARE;
  IF NOT FOUND THEN
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='Cortex Tool handoff is missing';
  END IF;
  IF p_tool_id IN ('kernel_review_equity_order','kernel_review_option_order') THEN
    IF target<>'desk' THEN
      RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='Cortex preflight is Decision Desk-only';
    END IF;
  ELSIF NOT EXISTS (
    SELECT 1 FROM agent_control.cortex_specialist_tool_grant grant_row
    WHERE grant_row.role_id=target AND grant_row.tool_id=p_tool_id
  ) THEN
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='Cortex Specialist Tool grant denied';
  END IF;
END $$;

DO $$
DECLARE function_row RECORD; definition TEXT; marker TEXT;
  tool_expression TEXT; injection TEXT;
BEGIN
  marker:=E'  SELECT manifest.call_id,result.result_id';
  FOR function_row IN
    SELECT p.oid,p.proname
      FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
      WHERE n.nspname='agent_control' AND p.proname IN (
        'authorize_cortex_web_fetch','authorize_cortex_gexbot_as_of',
        'authorize_cortex_kernel_earnings_results','authorize_cortex_kernel_read'
      )
  LOOP
    definition:=pg_get_functiondef(function_row.oid);
    IF position('enforce_cortex_specialist_tool_grant' IN definition)>0 THEN
      CONTINUE;
    END IF;
    IF position(marker IN definition)=0 THEN
      RAISE EXCEPTION 'Specialist grant injection point missing for %',function_row.proname;
    END IF;
    tool_expression:=CASE function_row.proname
      WHEN 'authorize_cortex_web_fetch' THEN quote_literal('research_web_fetch')
      WHEN 'authorize_cortex_gexbot_as_of' THEN quote_literal('research_gexbot_as_of')
      WHEN 'authorize_cortex_kernel_earnings_results' THEN quote_literal('kernel_earnings_results')
      WHEN 'authorize_cortex_kernel_read' THEN 'p_tool_id'
      ELSE NULL
    END;
    injection:='  PERFORM agent_control.enforce_cortex_specialist_tool_grant(p_source_call_id,'||
      tool_expression||');'||E'\n'||marker;
    definition:=replace(definition,marker,injection);
    EXECUTE definition;
  END LOOP;
END $$;

REVOKE ALL ON FUNCTION agent_control.enforce_cortex_specialist_tool_grant(TEXT,TEXT) FROM PUBLIC;

RESET ROLE;
