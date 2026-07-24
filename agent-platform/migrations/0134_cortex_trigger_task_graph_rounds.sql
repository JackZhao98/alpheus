-- Dynamic TaskGraph rounds must preserve the same strict origin split as the
-- first graph. External-event Runs use their immutable Trigger occurrence and
-- wake raw input; they never manufacture or require a UserRequest row.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

DO $migration$
DECLARE
  definition TEXT;
  original TEXT;
BEGIN
  SELECT pg_get_functiondef(
    'agent_control.get_cortex_task_graph_round_seed(text,text,bigint,uuid)'::regprocedure
  ) INTO definition;
  original:=definition;
  definition:=replace(
    definition,
    '        request.request_id,request.conversation_id,'||chr(10)||
    '        request.subject_principal_id,request.raw_input',
    '        COALESCE(request.request_id,wake.occurrence_id) AS request_id,'||chr(10)||
    '        COALESCE(request.conversation_id,''trigger:''||occurrence.registration_id)'||chr(10)||
    '          AS conversation_id,'||chr(10)||
    '        COALESCE(request.subject_principal_id,occurrence.initiating_principal_id)'||chr(10)||
    '          AS subject_principal_id,'||chr(10)||
    '        COALESCE(request.raw_input,wake.raw_input) AS raw_input'
  );
  definition:=replace(
    definition,
    '    JOIN agent_input.user_request AS request'||chr(10)||
    '      ON request.request_id=run.origin_source_record_id'||chr(10)||
    '     AND request.record_digest=run.origin_source_record_digest',
    '    LEFT JOIN agent_input.user_request AS request'||chr(10)||
    '      ON run.origin_kind=''user_request'''||chr(10)||
    '     AND request.request_id=run.origin_source_record_id'||chr(10)||
    '     AND request.record_digest=run.origin_source_record_digest'||chr(10)||
    '    LEFT JOIN agent_control.cortex_decision_trigger_wake_admission AS wake'||chr(10)||
    '      ON run.origin_kind=''external_event'''||chr(10)||
    '     AND wake.run_id=run.run_id'||chr(10)||
    '     AND wake.occurrence_id=run.occurrence_id'||chr(10)||
    '    LEFT JOIN agent_control.trigger_occurrence AS occurrence'||chr(10)||
    '      ON occurrence.occurrence_id=wake.occurrence_id'||chr(10)||
    '     AND occurrence.record_digest=wake.occurrence_digest'
  );
  definition:=replace(
    definition,
    '    WHERE manifest.call_id=p_source_call_id;',
    '    WHERE manifest.call_id=p_source_call_id'||chr(10)||
    '      AND ('||chr(10)||
    '        (request.request_id IS NOT NULL AND wake.occurrence_id IS NULL)'||chr(10)||
    '        OR'||chr(10)||
    '        (request.request_id IS NULL AND wake.occurrence_id IS NOT NULL'||chr(10)||
    '          AND occurrence.occurrence_id IS NOT NULL)'||chr(10)||
    '      );'
  );
  IF definition=original
     OR position('COALESCE(request.raw_input,wake.raw_input)' IN definition)=0
     OR position('trigger:''||occurrence.registration_id' IN definition)=0
     OR position('cortex_decision_trigger_wake_admission AS wake' IN definition)=0 THEN
    RAISE EXCEPTION
      'expected origin-aware Cortex TaskGraph round seed definition';
  END IF;
  EXECUTE definition;
END
$migration$;

DO $migration$
DECLARE
  definition TEXT;
  original TEXT;
BEGIN
  SELECT pg_get_functiondef(
    'agent_control.prepare_cortex_task_graph_next_round(text,text,bigint,uuid,jsonb,jsonb,text)'::regprocedure
  ) INTO definition;
  original:=definition;
  definition:=replace(
    definition,
    '    SELECT request.raw_input INTO STRICT raw_input'||chr(10)||
    '    FROM agent_input.user_request AS request'||chr(10)||
    '    WHERE request.request_id=run_row.origin_source_record_id'||chr(10)||
    '      AND request.record_digest=run_row.origin_source_record_digest;',
    '    SELECT request.raw_input INTO raw_input'||chr(10)||
    '    FROM agent_input.user_request AS request'||chr(10)||
    '    WHERE run_row.origin_kind=''user_request'''||chr(10)||
    '      AND request.request_id=run_row.origin_source_record_id'||chr(10)||
    '      AND request.record_digest=run_row.origin_source_record_digest;'||chr(10)||
    '    IF raw_input IS NULL THEN'||chr(10)||
    '      SELECT wake.raw_input INTO raw_input'||chr(10)||
    '      FROM agent_control.cortex_decision_trigger_wake_admission AS wake'||chr(10)||
    '      WHERE run_row.origin_kind=''external_event'''||chr(10)||
    '        AND wake.run_id=run_row.run_id'||chr(10)||
    '        AND wake.occurrence_id=run_row.occurrence_id;'||chr(10)||
    '    END IF;'||chr(10)||
    '    IF raw_input IS NULL THEN'||chr(10)||
    '      RAISE EXCEPTION USING ERRCODE=''55000'','||chr(10)||
    '        MESSAGE=''TaskGraph next round origin input unavailable'';'||chr(10)||
    '    END IF;'
  );
  IF definition=original
     OR position('run_row.origin_kind=''external_event''' IN definition)=0
     OR position('TaskGraph next round origin input unavailable' IN definition)=0 THEN
    RAISE EXCEPTION
      'expected origin-aware Cortex TaskGraph next round definition';
  END IF;
  EXECUTE definition;
END
$migration$;

REVOKE ALL ON FUNCTION
agent_control.get_cortex_task_graph_round_seed(TEXT,TEXT,BIGINT,UUID),
agent_control.prepare_cortex_task_graph_next_round(
  TEXT,TEXT,BIGINT,UUID,JSONB,JSONB,TEXT
)
FROM PUBLIC;
GRANT EXECUTE ON FUNCTION
agent_control.get_cortex_task_graph_round_seed(TEXT,TEXT,BIGINT,UUID),
agent_control.prepare_cortex_task_graph_next_round(
  TEXT,TEXT,BIGINT,UUID,JSONB,JSONB,TEXT
)
TO alpheus_agent_control_api;

RESET ROLE;
