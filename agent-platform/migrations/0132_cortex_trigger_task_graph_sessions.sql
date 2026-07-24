-- Prepare TaskGraph node Sessions from the exact origin-specific raw input.
-- User Runs remain bound to UserRequest.raw_input; external-event Runs remain
-- bound to the immutable Trigger wake admission and cannot substitute one
-- origin for the other.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

DO $$
DECLARE
  definition TEXT;
  original TEXT;
BEGIN
  SELECT pg_get_functiondef(
    'agent_control.prepare_cortex_task_graph_node_session(text,jsonb,jsonb,jsonb,jsonb,text)'::regprocedure
  ) INTO definition;
  original:=definition;
  definition:=replace(
    definition,
    '    request_row agent_input.user_request%ROWTYPE;'||chr(10),
    '    origin_raw_input JSONB;'||chr(10)
  );
  definition:=replace(
    definition,
    '    SELECT request.* INTO STRICT request_row'||chr(10)||
    '    FROM agent_input.user_request AS request'||chr(10)||
    '    WHERE request.request_id=run_row.origin_source_record_id'||chr(10)||
    '      AND request.record_digest=run_row.origin_source_record_digest'||
    chr(10)||
    '    FOR SHARE;',
    '    SELECT request.raw_input INTO origin_raw_input'||chr(10)||
    '    FROM agent_input.user_request AS request'||chr(10)||
    '    WHERE run_row.origin_kind=''user_request'''||chr(10)||
    '      AND request.request_id=run_row.origin_source_record_id'||chr(10)||
    '      AND request.record_digest=run_row.origin_source_record_digest'||
    chr(10)||
    '    FOR SHARE;'||chr(10)||
    '    IF origin_raw_input IS NULL THEN'||chr(10)||
    '        SELECT wake.raw_input INTO origin_raw_input'||chr(10)||
    '        FROM agent_control.cortex_decision_trigger_wake_admission AS wake'||
    chr(10)||
    '        WHERE run_row.origin_kind=''external_event'''||chr(10)||
    '          AND wake.run_id=run_row.run_id'||chr(10)||
    '          AND wake.occurrence_id=run_row.occurrence_id'||chr(10)||
    '        FOR SHARE;'||chr(10)||
    '    END IF;'
  );
  definition:=replace(
    definition,
    '       OR p_raw_input<>request_row.raw_input THEN',
    '       OR origin_raw_input IS NULL'||chr(10)||
    '       OR p_raw_input<>origin_raw_input THEN'
  );
  IF definition=original
     OR position('origin_raw_input JSONB' IN definition)=0
     OR position(
       'cortex_decision_trigger_wake_admission AS wake' IN definition
     )=0
     OR position('p_raw_input<>origin_raw_input' IN definition)=0 THEN
    RAISE EXCEPTION
      'expected origin-aware Cortex TaskGraph node Session definition';
  END IF;
  EXECUTE definition;
END
$$;

REVOKE ALL ON FUNCTION
agent_control.prepare_cortex_task_graph_node_session(
  TEXT,JSONB,JSONB,JSONB,JSONB,TEXT
)
FROM PUBLIC;
GRANT EXECUTE ON FUNCTION
agent_control.prepare_cortex_task_graph_node_session(
  TEXT,JSONB,JSONB,JSONB,JSONB,TEXT
)
TO alpheus_agent_control_api;

RESET ROLE;
