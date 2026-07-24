-- Expose only recent materialized occurrences that have not crossed the Run
-- admission boundary. Cortex Control can replay their deterministic Blob and
-- admission work after a process restart without re-firing the condition.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

CREATE FUNCTION agent_control.list_pending_cortex_decision_trigger_wakes(
    p_subject_principal_id TEXT,
    p_limit INTEGER
) RETURNS JSONB
LANGUAGE plpgsql
STABLE
SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security
SET timezone='UTC'
AS $$
DECLARE
    invoker RECORD;
BEGIN
    SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
    IF invoker.group_role::TEXT<>'alpheus_agent_control_api'
       OR invoker.profile_id<>'control-api'
       OR invoker.owner_id<>'agent_control'
       OR NOT agent_control.runtime_identifier_valid(
            p_subject_principal_id
       )
       OR p_limit NOT BETWEEN 1 AND 100 THEN
        RAISE EXCEPTION USING ERRCODE='42501',
            MESSAGE='Cortex decision Trigger wake recovery denied';
    END IF;
    RETURN COALESCE((
        SELECT jsonb_agg(item.payload ORDER BY item.created_at,item.sample_id)
        FROM (
            SELECT
                occurrence_link.created_at,
                occurrence_link.sample_id,
                jsonb_build_object(
                    'trigger',
                        agent_control.cortex_decision_trigger_json(trigger_row),
                    'sample',jsonb_strip_nulls(jsonb_build_object(
                        'sample_id',sample.sample_id,
                        'trigger_id',sample.trigger_id,
                        'generation',sample.trigger_generation,
                        'value',sample.value,
                        'prior_value',sample.prior_value,
                        'condition_met',sample.condition_met,
                        'fired',sample.fired,
                        'reason_code',sample.reason_code,
                        'observed_at',agent_control.runtime_utc_text(
                            sample.observed_at
                        ),
                        'committed_at',agent_control.runtime_utc_text(
                            sample.committed_at
                        )
                    )),
                    'occurrence',occurrence_link.response
                ) AS payload
            FROM agent_control.cortex_decision_trigger_occurrence
                AS occurrence_link
            JOIN agent_control.cortex_decision_trigger_sample AS sample
              ON sample.sample_id=occurrence_link.sample_id
             AND sample.fired
            JOIN agent_control.cortex_decision_trigger_revision AS trigger_row
              ON trigger_row.trigger_id=occurrence_link.trigger_id
             AND trigger_row.generation=occurrence_link.trigger_generation
             AND trigger_row.record_digest=occurrence_link.trigger_digest
            LEFT JOIN agent_control.cortex_decision_trigger_wake_admission
                AS wake
              ON wake.occurrence_id=occurrence_link.occurrence_id
            WHERE wake.occurrence_id IS NULL
              AND trigger_row.subject_principal_id=p_subject_principal_id
              AND occurrence_link.created_at>
                    clock_timestamp()-interval '12 minutes'
            ORDER BY occurrence_link.created_at,occurrence_link.sample_id
            LIMIT p_limit
        ) AS item
    ),'[]'::JSONB);
END
$$;

REVOKE ALL ON FUNCTION
agent_control.list_pending_cortex_decision_trigger_wakes(TEXT,INTEGER)
FROM PUBLIC;
GRANT EXECUTE ON FUNCTION
agent_control.list_pending_cortex_decision_trigger_wakes(TEXT,INTEGER)
TO alpheus_agent_control_api;

RESET ROLE;
