-- Select the registered round-decision contract for every new Decision Desk
-- and derive the next graph round only from immutable prior graphs plus the
-- exact root Session created by round continuation.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

DO $migration$
DECLARE
    definition TEXT;
    original_definition TEXT;
BEGIN
    definition:=pg_get_functiondef(
      'agent_control.get_cortex_task_graph_proposal_context(text,text,bigint,uuid)'::REGPROCEDURE);
    original_definition:=definition;
    definition:=replace(
      definition,
      $old$  answer_contract agent_control.output_contract_revision%ROWTYPE;
  at_time TIMESTAMPTZ:=clock_timestamp();$old$,
      $new$  answer_contract agent_control.output_contract_revision%ROWTYPE;
  round_value BIGINT;
  max_rounds_value BIGINT;
  at_time TIMESTAMPTZ:=clock_timestamp();$new$
    );
    definition:=replace(
      definition,
      $old$  IF source_row.attempt_id<>p_attempt_id$old$,
      $new$  SELECT COALESCE(max(graph.round)+1,1),
         COALESCE(max(graph.max_rounds),2)
  INTO round_value,max_rounds_value
  FROM agent_control.cortex_task_graph AS graph
  WHERE graph.run_id=source_row.run_id
    AND graph.parent_task_id=source_row.task_id;

  IF source_row.attempt_id<>p_attempt_id$new$
    );
    definition:=replace(
      definition,
      $old$     OR EXISTS (
       SELECT 1
       FROM agent_control.cortex_task_graph AS graph
       WHERE graph.parent_task_id=source_row.task_id
     ) THEN$old$,
      $new$     OR round_value>max_rounds_value
     OR EXISTS (
       SELECT 1
       FROM agent_control.cortex_task_graph AS graph
       WHERE graph.parent_task_id=source_row.task_id
         AND graph.round=round_value
     )
     OR (
       round_value=1
       AND EXISTS (
         SELECT 1
         FROM agent_control.cortex_task_graph_round_continuation AS continuation
         WHERE continuation.parent_task_id=source_row.task_id
       )
     )
     OR (
       round_value>1
       AND NOT EXISTS (
         SELECT 1
         FROM agent_control.cortex_task_graph_round_continuation AS continuation
         JOIN agent_control.runtime_attempt AS current_attempt
           ON current_attempt.attempt_id=p_attempt_id
          AND current_attempt.session_id=continuation.parent_session_id
         WHERE continuation.parent_task_id=source_row.task_id
           AND continuation.run_id=source_row.run_id
           AND continuation.completed_round=round_value-1
           AND continuation.max_rounds=max_rounds_value
           AND continuation.state='ready'
       )
     ) THEN$new$
    );
    definition:=replace(
      definition,
      $old$  WHERE revision_id='cortex-text-output-v1'$old$,
      $new$  WHERE revision_id='cortex-task-graph-round-output-v1'$new$
    );
    definition:=replace(
      definition,
      $old$    'parent_task_state_generation',source_row.task_state_generation,$old$,
      $new$    'parent_task_state_generation',source_row.task_state_generation,
    'round',round_value,
    'max_rounds',max_rounds_value,$new$
    );
    IF definition=original_definition
       OR position('round_value BIGINT' IN definition)=0
       OR position('cortex-task-graph-round-output-v1' IN definition)=0
       OR position('''round'',round_value' IN definition)=0 THEN
        RAISE EXCEPTION 'Unexpected TaskGraph proposal context definition';
    END IF;
    EXECUTE definition;
END
$migration$;

REVOKE ALL ON FUNCTION
agent_control.get_cortex_task_graph_proposal_context(
  TEXT,TEXT,BIGINT,UUID)
FROM PUBLIC;
GRANT EXECUTE ON FUNCTION
agent_control.get_cortex_task_graph_proposal_context(
  TEXT,TEXT,BIGINT,UUID)
TO alpheus_agent_control_api;

RESET ROLE;
