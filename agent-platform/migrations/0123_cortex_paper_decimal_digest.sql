-- Runtime contract canonical JSON deliberately rejects fractional numbers.
-- Paper proposals and fills are decimal-valued, so immutable record bodies bind
-- their normalized JSONB bytes by SHA-256 instead of embedding those decimals
-- directly in the Runtime canonical body.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

DO $$
DECLARE
  definition TEXT;
  old_fragment TEXT;
  new_fragment TEXT;
BEGIN
  SELECT pg_get_functiondef(
    'agent_control.admit_cortex_paper_trade_candidate(text,text,bigint,uuid,jsonb)'::regprocedure
  ) INTO definition;
  old_fragment:=
    '    ''attempt_id'',source_row.attempt_id,''proposal'',p_proposal,' ||
    chr(10);
  new_fragment:=
    '    ''attempt_id'',source_row.attempt_id,''proposal_digest'',' ||
    chr(10) ||
    '      encode(digest(convert_to(p_proposal::TEXT,''UTF8''),' ||
    chr(10) ||
    '        ''sha256''),''hex''),' || chr(10);
  IF position(old_fragment IN definition)=0 THEN
    RAISE EXCEPTION 'expected Paper Candidate proposal digest fragment';
  END IF;
  EXECUTE replace(definition,old_fragment,new_fragment);

  SELECT pg_get_functiondef(
    'agent_control.authorize_cortex_paper_effect(text,text,text,bigint)'::regprocedure
  ) INTO definition;
  old_fragment:='    ''proposal'',source.proposal,' || chr(10);
  new_fragment:=
    '    ''proposal_digest'',' || chr(10) ||
    '      encode(digest(convert_to(source.proposal::TEXT,''UTF8''),' ||
    chr(10) ||
    '        ''sha256''),''hex''),' || chr(10);
  IF position(old_fragment IN definition)=0 THEN
    RAISE EXCEPTION 'expected Paper authorization proposal digest fragment';
  END IF;
  EXECUTE replace(definition,old_fragment,new_fragment);

  SELECT pg_get_functiondef(
    'agent_control.record_cortex_paper_effect_receipt(text,text,integer,jsonb,text)'::regprocedure
  ) INTO definition;
  old_fragment:=
    '    ''http_status'',p_http_status,''kernel_response'',' ||
    'p_kernel_response,' || chr(10);
  new_fragment:=
    '    ''http_status'',p_http_status,''kernel_response_digest'',' ||
    chr(10) ||
    '      CASE WHEN p_kernel_response IS NULL THEN NULL ELSE' ||
    chr(10) ||
    '        encode(digest(convert_to(p_kernel_response::TEXT,''UTF8''),' ||
    chr(10) ||
    '          ''sha256''),''hex'') END,' || chr(10);
  IF position(old_fragment IN definition)=0 THEN
    RAISE EXCEPTION 'expected Paper receipt response digest fragment';
  END IF;
  EXECUTE replace(definition,old_fragment,new_fragment);
END $$;

RESET ROLE;
