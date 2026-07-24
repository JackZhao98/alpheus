-- AP1 canonical Runtime JSON permits integers but intentionally rejects
-- floating JSON numbers. Preserve exact market decimals as strings in the
-- external-event source record before digesting it.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

DO $$
DECLARE
    original_definition TEXT;
    corrected_definition TEXT;
    numeric_fragment CONSTANT TEXT :=
'        ''value'',sample.value,
        ''prior_value'',sample.prior_value,';
    text_fragment CONSTANT TEXT :=
'        ''value'',sample.value::TEXT,
        ''prior_value'',sample.prior_value::TEXT,';
BEGIN
    SELECT pg_get_functiondef(
        'agent_control.materialize_cortex_decision_trigger_occurrence(text)'::regprocedure
    ) INTO STRICT original_definition;
    IF length(original_definition)-length(
        replace(original_definition,numeric_fragment,'')
    )<>length(numeric_fragment) THEN
        RAISE EXCEPTION USING ERRCODE='55000',
            MESSAGE='Cortex decision Trigger decimal repair source mismatch';
    END IF;
    corrected_definition:=replace(
        original_definition,numeric_fragment,text_fragment
    );
    EXECUTE corrected_definition;
END
$$;

RESET ROLE;
