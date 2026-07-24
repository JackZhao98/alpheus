-- TaskGraph proposal admission originally required a UserRequest row. External
-- Trigger Runs instead own an immutable wake-admission raw input. Select
-- exactly one origin-specific input so an automatic Candidate Run can use the
-- same bounded parallel graph without masquerading as a user request.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

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
    '    request.raw_input,'||chr(10),
    '    COALESCE(request.raw_input,wake.raw_input) AS raw_input,'||chr(10)
  );
  definition:=replace(
    definition,
    '  JOIN agent_input.user_request AS request'||chr(10)||
    '    ON request.request_id=run.origin_source_record_id'||chr(10)||
    '   AND request.record_digest=run.origin_source_record_digest',
    '  LEFT JOIN agent_input.user_request AS request'||chr(10)||
    '    ON run.origin_kind=''user_request'''||chr(10)||
    '   AND request.request_id=run.origin_source_record_id'||chr(10)||
    '   AND request.record_digest=run.origin_source_record_digest'||chr(10)||
    '  LEFT JOIN agent_control.cortex_decision_trigger_wake_admission AS wake'||
    chr(10)||
    '    ON run.origin_kind=''external_event'''||chr(10)||
    '   AND wake.run_id=run.run_id'||chr(10)||
    '   AND wake.occurrence_id=run.occurrence_id'
  );
  definition:=replace(
    definition,
    '  WHERE manifest.call_id=p_source_call_id'||chr(10)||
    '  FOR UPDATE OF attempt,task,run;',
    '  WHERE manifest.call_id=p_source_call_id'||chr(10)||
    '    AND ('||chr(10)||
    '      (request.request_id IS NOT NULL AND wake.occurrence_id IS NULL)'||
    chr(10)||
    '      OR'||chr(10)||
    '      (request.request_id IS NULL AND wake.occurrence_id IS NOT NULL)'||
    chr(10)||
    '    )'||chr(10)||
    '  FOR UPDATE OF attempt,task,run;'
  );
  IF definition=original
     OR position(
       'COALESCE(request.raw_input,wake.raw_input)' IN definition
     )=0
     OR position(
       'cortex_decision_trigger_wake_admission AS wake' IN definition
     )=0
     OR position(
       'request.request_id IS NULL AND wake.occurrence_id IS NOT NULL'
       IN definition
     )=0 THEN
    RAISE EXCEPTION
      'expected origin-aware Cortex TaskGraph proposal context';
  END IF;
  EXECUTE definition;
END
$$;

REVOKE ALL ON FUNCTION
agent_control.get_cortex_task_graph_proposal_context(
  TEXT,TEXT,BIGINT,UUID
)
FROM PUBLIC;
GRANT EXECUTE ON FUNCTION
agent_control.get_cortex_task_graph_proposal_context(
  TEXT,TEXT,BIGINT,UUID
)
TO alpheus_agent_control_api;

RESET ROLE;
