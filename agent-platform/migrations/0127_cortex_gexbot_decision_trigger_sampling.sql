-- Activate the already registered research_gexbot Decision Trigger source.
-- Cortex Control consumes only recent Moody Blues archive observations; the
-- deterministic sampler remains effect-free and keeps the existing threshold,
-- crossing, cooldown and occurrence semantics.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

DO $$
DECLARE
  definition TEXT;
  old_filter TEXT:=$match$      AND revision.data_source='kernel_quote'$match$;
  new_filter TEXT:=$match$      AND revision.data_source IN(
        'kernel_quote','research_gexbot'
      )$match$;
BEGIN
  SELECT pg_get_functiondef(
    'agent_control.record_cortex_decision_trigger_sample(text,numeric,timestamp with time zone)'::regprocedure
  ) INTO definition;
  IF position(old_filter IN definition)=0 THEN
    RAISE EXCEPTION
      'expected Cortex Decision Trigger source filter';
  END IF;
  EXECUTE replace(definition,old_filter,new_filter);
END
$$;

REVOKE ALL ON FUNCTION
agent_control.record_cortex_decision_trigger_sample(
  TEXT,NUMERIC,TIMESTAMPTZ
)
FROM PUBLIC;
GRANT EXECUTE ON FUNCTION
agent_control.record_cortex_decision_trigger_sample(
  TEXT,NUMERIC,TIMESTAMPTZ
)
TO alpheus_agent_control_api;

RESET ROLE;
