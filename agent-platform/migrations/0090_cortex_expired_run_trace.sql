-- Surface Control deadline recovery in the same user-visible trace as model,
-- tool and TaskGraph events. The durable recovery record is the source of
-- truth; no process-local log reconstruction is involved.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

DO $migration$
DECLARE
    definition TEXT;
    original_definition TEXT;
    old_fragment TEXT:=$old$
        UNION ALL

        SELECT
          result.recorded_at,
          50,
$old$;
    new_fragment TEXT:=$new$
        UNION ALL

        SELECT
          recovery.recovered_at,
          90,
          'expired-run-recovery:'||recovery.run_id,
          jsonb_build_object(
            'created_at',
              agent_control.runtime_utc_text(recovery.recovered_at),
            'stage','cortex_run_dead_lettered',
            'state','dead_lettered',
            'reason_code','runtime_deadline_expired'
          )
        FROM agent_control.cortex_expired_run_recovery AS recovery
        WHERE recovery.run_id=p_run_id

        UNION ALL

        SELECT
          result.recorded_at,
          50,
$new$;
BEGIN
    definition:=pg_get_functiondef(
        'agent_control.get_cortex_run_trace(text)'::REGPROCEDURE);
    original_definition:=definition;
    definition:=replace(definition,old_fragment,new_fragment);
    IF definition=original_definition
       OR position('expired-run-recovery:' IN definition)=0 THEN
        RAISE EXCEPTION 'Unexpected Cortex trace definition';
    END IF;
    EXECUTE definition;
END
$migration$;

REVOKE ALL ON FUNCTION
agent_control.get_cortex_run_trace(TEXT) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION
agent_control.get_cortex_run_trace(TEXT)
TO alpheus_agent_control_api;

RESET ROLE;
