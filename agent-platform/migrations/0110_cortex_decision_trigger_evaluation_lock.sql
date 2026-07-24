-- Serialize evaluations on the Trigger head row. This preserves per-Trigger
-- ordering without granting the control API access to PostgreSQL advisory-lock
-- primitives.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

DO $$
DECLARE
    original_definition TEXT;
    corrected_definition TEXT;
    advisory_fragment CONSTANT TEXT :=
'    PERFORM pg_advisory_xact_lock(hashtextextended(p_trigger_id,0));
';
    share_fragment CONSTANT TEXT := '    FOR SHARE OF head;';
    update_fragment CONSTANT TEXT := '    FOR UPDATE OF head;';
BEGIN
    SELECT pg_get_functiondef(
        'agent_control.record_cortex_decision_trigger_sample(text,numeric,timestamp with time zone)'::regprocedure
    ) INTO STRICT original_definition;
    IF length(original_definition)-length(
        replace(original_definition,advisory_fragment,'')
    )<>length(advisory_fragment)
       OR length(original_definition)-length(
            replace(original_definition,share_fragment,'')
       )<>length(share_fragment) THEN
        RAISE EXCEPTION USING ERRCODE='55000',
            MESSAGE='Cortex decision Trigger evaluation lock repair source mismatch';
    END IF;
    corrected_definition:=replace(
        replace(original_definition,advisory_fragment,''),
        share_fragment,update_fragment
    );
    EXECUTE corrected_definition;
END
$$;

RESET ROLE;
