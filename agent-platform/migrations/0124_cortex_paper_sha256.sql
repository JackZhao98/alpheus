-- PostgreSQL 16 exposes sha256(bytea) directly. Use the built-in function so
-- Paper digest binding does not depend on the optional pgcrypto digest().
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
    '      encode(digest(convert_to(p_proposal::TEXT,''UTF8''),' ||
    chr(10) || '        ''sha256''),''hex''),';
  new_fragment:=
    '      encode(sha256(convert_to(p_proposal::TEXT,''UTF8'')),' ||
    '''hex''),';
  IF position(old_fragment IN definition)=0 THEN
    RAISE EXCEPTION 'expected Paper Candidate pgcrypto digest fragment';
  END IF;
  EXECUTE replace(definition,old_fragment,new_fragment);

  SELECT pg_get_functiondef(
    'agent_control.authorize_cortex_paper_effect(text,text,text,bigint)'::regprocedure
  ) INTO definition;
  old_fragment:=
    '      encode(digest(convert_to(source.proposal::TEXT,''UTF8''),' ||
    chr(10) || '        ''sha256''),''hex''),';
  new_fragment:=
    '      encode(sha256(convert_to(source.proposal::TEXT,''UTF8'')),' ||
    '''hex''),';
  IF position(old_fragment IN definition)=0 THEN
    RAISE EXCEPTION 'expected Paper authorization pgcrypto digest fragment';
  END IF;
  EXECUTE replace(definition,old_fragment,new_fragment);

  SELECT pg_get_functiondef(
    'agent_control.record_cortex_paper_effect_receipt(text,text,integer,jsonb,text)'::regprocedure
  ) INTO definition;
  old_fragment:=
    '        encode(digest(convert_to(p_kernel_response::TEXT,''UTF8''),' ||
    chr(10) || '          ''sha256''),''hex'') END,';
  new_fragment:=
    '        encode(sha256(convert_to(p_kernel_response::TEXT,''UTF8'')),' ||
    '''hex'') END,';
  IF position(old_fragment IN definition)=0 THEN
    RAISE EXCEPTION 'expected Paper receipt pgcrypto digest fragment';
  END IF;
  EXECUTE replace(definition,old_fragment,new_fragment);
END $$;

RESET ROLE;
