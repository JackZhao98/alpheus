-- Extend the existing atomic Run recovery boundary to execution trees whose
-- immutable authority is no longer current.  Revocation must stop more than
-- new admissions: Control closes already-materialized work immediately,
-- releases accounting slots, and records the stable reason in the same
-- immutable recovery journal.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

ALTER TABLE agent_control.cortex_expired_run_recovery
ADD COLUMN reason_code TEXT NOT NULL
DEFAULT 'runtime_deadline_expired'
CHECK (reason_code IN (
    'runtime_deadline_expired',
    'runtime_authority_not_current'
));

DO $migration$
DECLARE
    definition TEXT;
    original_definition TEXT;
    old_fragment TEXT;
    new_fragment TEXT;
BEGIN
    definition:=pg_get_functiondef(
        'agent_control.reconcile_expired_cortex_runs(integer)'::REGPROCEDURE
    );
    original_definition:=definition;

    old_fragment:=$old$
    failure_value JSONB;
    response_value JSONB;
$old$;
    new_fragment:=$new$
    failure_value JSONB;
    failure_message TEXT;
    reason_code_value TEXT;
    response_value JSONB;
$new$;
    definition:=replace(definition,old_fragment,new_fragment);

    old_fragment:=$old$
          AND run.deadline_at<=clock_timestamp()
          AND recovery.run_id IS NULL
$old$;
    new_fragment:=$new$
          AND (
              run.deadline_at<=clock_timestamp()
              OR NOT agent_control.runtime_run_admission_current(run.run_id)
          )
          AND recovery.run_id IS NULL
$new$;
    definition:=replace(definition,old_fragment,new_fragment);

    old_fragment:=$old$
        at_time:=clock_timestamp();
        failure_value:=jsonb_build_object(
            'code','runtime_deadline_expired',
            'message','Cortex Run exceeded its immutable deadline and was terminalized by Control recovery',
            'retryable',false
        );
$old$;
    new_fragment:=$new$
        at_time:=clock_timestamp();
        IF run_row.deadline_at<=at_time THEN
            reason_code_value:='runtime_deadline_expired';
            failure_message:=
                'Cortex Run exceeded its immutable deadline and was terminalized by Control recovery';
        ELSE
            reason_code_value:='runtime_authority_not_current';
            failure_message:=
                'Cortex Run authority is no longer current and was terminalized by Control recovery';
        END IF;
        failure_value:=jsonb_build_object(
            'code',reason_code_value,
            'message',failure_message,
            'retryable',false
        );
$new$;
    definition:=replace(definition,old_fragment,new_fragment);

    definition:=replace(
        definition,
        $old$                    'runtime_deadline_expired',at_time$old$,
        $new$                    reason_code_value,at_time$new$
    );
    definition:=replace(
        definition,
        $old$            'reason_code','runtime_deadline_expired',$old$,
        $new$            'reason_code',reason_code_value,$new$
    );
    definition:=replace(
        definition,
        $old$            run_id,schema_revision,prior_run_state,failure,response,recovered_at
        ) VALUES(
            run_row.run_id,1,run_row.state,failure_value,
            response_value,at_time
$old$,
        $new$            run_id,schema_revision,prior_run_state,failure,response,recovered_at,
            reason_code
        ) VALUES(
            run_row.run_id,1,run_row.state,failure_value,
            response_value,at_time,reason_code_value
$new$
    );
    definition:=replace(
        definition,
        $old$    task_count BIGINT:=0;$old$,
        $new$    task_count BIGINT:=0;
    expired_count BIGINT:=0;
    revoked_count BIGINT:=0;$new$
    );
    definition:=replace(
        definition,
        $old$        recovered_count:=recovered_count+1;$old$,
        $new$        recovered_count:=recovered_count+1;
        IF reason_code_value='runtime_deadline_expired' THEN
            expired_count:=expired_count+1;
        ELSE
            revoked_count:=revoked_count+1;
        END IF;$new$
    );
    definition:=replace(
        definition,
        $old$        'recovered_runs',recovered_count,
        'terminalized_turns',turn_count,$old$,
        $new$        'recovered_runs',recovered_count,
        'expired_runs',expired_count,
        'revoked_runs',revoked_count,
        'terminalized_turns',turn_count,$new$
    );

    IF definition=original_definition
       OR position('runtime_authority_not_current' IN definition)=0
       OR position('revoked_runs' IN definition)=0
       OR position('reason_code_value,at_time' IN definition)=0 THEN
        RAISE EXCEPTION 'Unexpected Cortex Run recovery definition';
    END IF;
    EXECUTE definition;
END
$migration$;

-- The trace projection introduced in 0090 is preserved through later wrapper
-- revisions. Replace its historical hard-coded deadline reason with the
-- durable per-recovery reason.
DO $migration$
DECLARE
    definition TEXT;
    original_definition TEXT;
BEGIN
    definition:=pg_get_functiondef(
        'agent_control.get_cortex_run_trace_pre_cancellation_v1(text)'::REGPROCEDURE
    );
    original_definition:=definition;
    definition:=replace(
        definition,
        $old$            'reason_code','runtime_deadline_expired'$old$,
        $new$            'reason_code',recovery.reason_code$new$
    );
    IF definition=original_definition
       OR position('recovery.reason_code' IN definition)=0 THEN
        RAISE EXCEPTION 'Unexpected Cortex recovery trace definition';
    END IF;
    EXECUTE definition;
END
$migration$;

REVOKE ALL ON TABLE
agent_control.cortex_expired_run_recovery FROM PUBLIC;
REVOKE ALL ON FUNCTION
agent_control.reconcile_expired_cortex_runs(INTEGER) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION
agent_control.reconcile_expired_cortex_runs(INTEGER)
TO alpheus_agent_control_api;

RESET ROLE;
