-- Workflow v9 inherits every v8 read-only Tool family. Migration 0116 added
-- v9 to flags whose final array item shared a line with v8; this closes the
-- two line-wrapped arrays without changing any already-admitted Run.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

DO $$
DECLARE
  definition TEXT;
  old_gexbot TEXT;
  new_gexbot TEXT;
  old_kernel TEXT;
  new_kernel TEXT;
BEGIN
  SELECT pg_get_functiondef(
    'agent_control.next_cortex_task()'::regprocedure
  ) INTO definition;
  old_gexbot:=$match$'cortex-workflow-output-v6','cortex-workflow-output-v7',
      'cortex-workflow-output-v8'
    ) THEN true ELSE false END AS gexbot_enabled,$match$;
  new_gexbot:=$match$'cortex-workflow-output-v6','cortex-workflow-output-v7',
      'cortex-workflow-output-v8','cortex-workflow-output-v9'
    ) THEN true ELSE false END AS gexbot_enabled,$match$;
  old_kernel:=$match$'cortex-workflow-output-v6','cortex-workflow-output-v7',
      'cortex-workflow-output-v8'
    ) THEN true ELSE false END AS kernel_tools_enabled,$match$;
  new_kernel:=$match$'cortex-workflow-output-v6','cortex-workflow-output-v7',
      'cortex-workflow-output-v8','cortex-workflow-output-v9'
    ) THEN true ELSE false END AS kernel_tools_enabled,$match$;
  IF position(old_gexbot IN definition)=0
    OR position(old_kernel IN definition)=0
    OR position('paper_candidate_enabled' IN definition)=0 THEN
    RAISE EXCEPTION 'expected Cortex Worker v9 feature discovery definition';
  END IF;
  definition:=replace(definition,old_gexbot,new_gexbot);
  definition:=replace(definition,old_kernel,new_kernel);
  EXECUTE definition;
END $$;

REVOKE ALL ON FUNCTION agent_control.next_cortex_task() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agent_control.next_cortex_task()
  TO alpheus_agent_worker;

RESET ROLE;
