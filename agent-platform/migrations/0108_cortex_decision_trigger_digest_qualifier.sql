-- Qualify the previous revision's registration digest. PL/pgSQL otherwise
-- treats the identically named local variable and table column as ambiguous.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

DO $$
DECLARE
    original_definition TEXT;
    corrected_definition TEXT;
    ambiguous_fragment CONSTANT TEXT :=
'            SELECT registration_digest
            FROM agent_control.cortex_decision_trigger_revision
            WHERE trigger_id=trigger_id_value
              AND generation=previous_generation';
    qualified_fragment CONSTANT TEXT :=
'            SELECT previous_revision.registration_digest
            FROM agent_control.cortex_decision_trigger_revision
                AS previous_revision
            WHERE previous_revision.trigger_id=trigger_id_value
              AND previous_revision.generation=previous_generation';
BEGIN
    SELECT pg_get_functiondef(
        'agent_control.register_cortex_decision_trigger(text,jsonb)'::regprocedure
    ) INTO STRICT original_definition;
    corrected_definition:=replace(
        original_definition,ambiguous_fragment,qualified_fragment
    );
    IF corrected_definition=original_definition
       OR length(original_definition)-length(
            replace(original_definition,ambiguous_fragment,'')
       )<>length(ambiguous_fragment) THEN
        RAISE EXCEPTION USING ERRCODE='55000',
            MESSAGE='Cortex decision Trigger repair source mismatch';
    END IF;
    EXECUTE corrected_definition;
END
$$;

RESET ROLE;
