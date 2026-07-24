-- Install a candidate-aware Decision Desk round contract for v9 parent Runs.
-- Ordinary research graphs remain pinned to the immutable v1 answer contract.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

DO $$
DECLARE
  definition TEXT;
BEGIN
  SELECT pg_get_functiondef(
    'agent_control.ensure_cortex_task_graph_round_output_contract_v1(jsonb)'::regprocedure
  ) INTO definition;
  definition:=replace(
    definition,
    'ensure_cortex_task_graph_round_output_contract_v1',
    'ensure_cortex_task_graph_round_output_contract_v2'
  );
  definition:=replace(
    definition,
    'cortex-task-graph-round-output-v1',
    'cortex-task-graph-round-output-v2'
  );
  IF position(
    'ensure_cortex_task_graph_round_output_contract_v2' IN definition
  )=0 OR position(
    'cortex-task-graph-round-output-v2' IN definition
  )=0 THEN
    RAISE EXCEPTION 'expected Candidate TaskGraph round contract definition';
  END IF;
  EXECUTE definition;
END $$;

DO $$
DECLARE
  definition TEXT;
  original TEXT;
BEGIN
  SELECT pg_get_functiondef(
    'agent_control.get_cortex_task_graph_proposal_context(text,text,bigint,uuid)'::regprocedure
  ) INTO definition;
  original:=definition;
  definition:=replace(
    definition,
    '    task.task_id,'||chr(10),
    '    task.task_id,'||chr(10)||
    '    task.output_contract_revision_id AS parent_output_contract_revision_id,'||
    chr(10)
  );
  definition:=replace(
    definition,
    '   AND task.output_contract_revision_id=''cortex-workflow-output-v8''',
    '   AND task.output_contract_revision_id IN ('||chr(10)||
    '     ''cortex-workflow-output-v8'',''cortex-workflow-output-v9'''||
    chr(10)||'   )'
  );
  definition:=replace(
    definition,
    '  WHERE revision_id=''cortex-task-graph-round-output-v1''',
    '  WHERE revision_id=CASE'||chr(10)||
    '    WHEN source_row.parent_output_contract_revision_id='||
    '''cortex-workflow-output-v9'''||chr(10)||
    '      THEN ''cortex-task-graph-round-output-v2'''||chr(10)||
    '    ELSE ''cortex-task-graph-round-output-v1'' END'
  );
  IF definition=original OR position(
    'parent_output_contract_revision_id' IN definition
  )=0 OR position(
    'cortex-task-graph-round-output-v2' IN definition
  )=0 THEN
    RAISE EXCEPTION 'expected Candidate TaskGraph proposal context definition';
  END IF;
  EXECUTE definition;
END $$;

DO $$
DECLARE
  definition TEXT;
  old_flag TEXT;
  new_flag TEXT;
BEGIN
  SELECT pg_get_functiondef(
    'agent_control.next_cortex_task()'::regprocedure
  ) INTO definition;
  old_flag:=$match$CASE WHEN task.output_contract_revision_id='cortex-workflow-output-v9'
      THEN true ELSE false END AS paper_candidate_enabled,$match$;
  new_flag:=$match$CASE WHEN task.output_contract_revision_id IN (
      'cortex-workflow-output-v9','cortex-task-graph-round-output-v2'
    ) THEN true ELSE false END AS paper_candidate_enabled,$match$;
  IF position(old_flag IN definition)=0 THEN
    RAISE EXCEPTION 'expected Candidate Worker discovery flag';
  END IF;
  EXECUTE replace(definition,old_flag,new_flag);
END $$;

DO $$
DECLARE
  definition TEXT;
  old_contract TEXT;
  new_contract TEXT;
BEGIN
  SELECT pg_get_functiondef(
    'agent_control.get_cortex_task_graph_round_seed(text,text,bigint,uuid)'::regprocedure
  ) INTO definition;
  old_contract:=
    '      ON contract.revision_id='||
    '''cortex-task-graph-round-output-v1''';
  new_contract:=
    '      ON contract.revision_id IN ('||chr(10)||
    '        ''cortex-task-graph-round-output-v1'','||
    chr(10)||'        ''cortex-task-graph-round-output-v2'''||
    chr(10)||'      )';
  IF position(old_contract IN definition)=0 THEN
    RAISE EXCEPTION 'expected TaskGraph round seed contract';
  END IF;
  EXECUTE replace(definition,old_contract,new_contract);
END $$;

REVOKE ALL ON FUNCTION
agent_control.ensure_cortex_task_graph_round_output_contract_v2(JSONB),
agent_control.get_cortex_task_graph_proposal_context(
  TEXT,TEXT,BIGINT,UUID),
agent_control.get_cortex_task_graph_round_seed(
  TEXT,TEXT,BIGINT,UUID),
agent_control.next_cortex_task()
FROM PUBLIC;
GRANT EXECUTE ON FUNCTION
agent_control.ensure_cortex_task_graph_round_output_contract_v2(JSONB),
agent_control.get_cortex_task_graph_proposal_context(
  TEXT,TEXT,BIGINT,UUID)
TO alpheus_agent_control_api;
GRANT EXECUTE ON FUNCTION
agent_control.next_cortex_task()
TO alpheus_agent_worker;

RESET ROLE;
