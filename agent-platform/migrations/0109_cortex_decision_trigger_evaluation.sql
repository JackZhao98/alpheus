-- Deterministic, append-only evaluation samples. The database serializes one
-- Trigger, applies its exact revision, crossing semantics and cooldown, and
-- records whether the observation should emit an occurrence in the next slice.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

CREATE TABLE agent_control.cortex_decision_trigger_sample(
    sample_id TEXT PRIMARY KEY CHECK(
        agent_control.runtime_identifier_valid(sample_id)
    ),
    trigger_id TEXT NOT NULL,
    trigger_generation BIGINT NOT NULL,
    trigger_digest CHAR(64) NOT NULL,
    value NUMERIC(24,8) NOT NULL CHECK(
        value BETWEEN -1000000000 AND 1000000000
    ),
    prior_value NUMERIC(24,8),
    condition_met BOOLEAN NOT NULL,
    fired BOOLEAN NOT NULL,
    reason_code TEXT NOT NULL CHECK(
        reason_code IN(
            'threshold_not_met','threshold_met','crossed',
            'no_prior_sample','cooldown_suppressed'
        )
    ),
    observed_at TIMESTAMPTZ NOT NULL,
    committed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY(trigger_id,trigger_generation,trigger_digest)
        REFERENCES agent_control.cortex_decision_trigger_revision(
            trigger_id,generation,record_digest
        ),
    CHECK(observed_at<=committed_at),
    CHECK(fired=false OR condition_met=true),
    CHECK(
        (reason_code='cooldown_suppressed'
            AND condition_met=true AND fired=false)
        OR reason_code<>'cooldown_suppressed'
    )
);

CREATE INDEX cortex_decision_trigger_sample_latest_idx
ON agent_control.cortex_decision_trigger_sample(
    trigger_id,observed_at DESC,sample_id DESC
);

CREATE TRIGGER cortex_decision_trigger_sample_immutable
BEFORE UPDATE OR DELETE ON agent_control.cortex_decision_trigger_sample
FOR EACH ROW EXECUTE FUNCTION
agent_control.reject_immutable_runtime_definition_mutation();

CREATE FUNCTION agent_control.record_cortex_decision_trigger_sample(
    p_trigger_id TEXT,
    p_value NUMERIC,
    p_observed_at TIMESTAMPTZ
) RETURNS JSONB
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security
SET timezone='UTC'
AS $$
DECLARE
    invoker RECORD;
    at_time TIMESTAMPTZ:=clock_timestamp();
    trigger_row agent_control.cortex_decision_trigger_revision%ROWTYPE;
    previous_sample agent_control.cortex_decision_trigger_sample%ROWTYPE;
    last_fired_at TIMESTAMPTZ;
    condition_met_value BOOLEAN:=false;
    fired_value BOOLEAN:=false;
    reason_code_value TEXT:='threshold_not_met';
    sample_id_value TEXT:=gen_random_uuid()::TEXT;
BEGIN
    SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
    IF invoker.group_role::TEXT<>'alpheus_agent_control_api'
       OR invoker.profile_id<>'control-api'
       OR invoker.owner_id<>'agent_control'
       OR NOT agent_control.runtime_identifier_valid(p_trigger_id)
       OR p_value IS NULL
       OR p_value NOT BETWEEN -1000000000 AND 1000000000
       OR p_observed_at IS NULL
       OR p_observed_at>at_time
       OR p_observed_at<at_time-interval '5 minutes' THEN
        RAISE EXCEPTION USING ERRCODE='22023',
            MESSAGE='invalid Cortex decision Trigger sample';
    END IF;
    PERFORM pg_advisory_xact_lock(hashtextextended(p_trigger_id,0));
    SELECT revision.* INTO trigger_row
    FROM agent_control.cortex_decision_trigger_head AS head
    JOIN agent_control.cortex_decision_trigger_revision AS revision
      ON revision.trigger_id=head.trigger_id
     AND revision.generation=head.generation
     AND revision.record_digest=head.record_digest
    WHERE head.trigger_id=p_trigger_id
      AND revision.enabled
      AND revision.data_source='kernel_quote'
    FOR SHARE OF head;
    IF NOT FOUND THEN
        RAISE EXCEPTION USING ERRCODE='55000',
            MESSAGE='Cortex decision Trigger is not evaluable';
    END IF;
    SELECT * INTO previous_sample
    FROM agent_control.cortex_decision_trigger_sample
    WHERE trigger_id=p_trigger_id
    ORDER BY observed_at DESC,sample_id DESC
    LIMIT 1;
    SELECT max(observed_at) INTO last_fired_at
    FROM agent_control.cortex_decision_trigger_sample
    WHERE trigger_id=p_trigger_id AND fired;

    CASE trigger_row.comparator
    WHEN 'gte' THEN
        condition_met_value:=p_value>=trigger_row.threshold;
        reason_code_value:=CASE WHEN condition_met_value
            THEN 'threshold_met' ELSE 'threshold_not_met' END;
    WHEN 'lte' THEN
        condition_met_value:=p_value<=trigger_row.threshold;
        reason_code_value:=CASE WHEN condition_met_value
            THEN 'threshold_met' ELSE 'threshold_not_met' END;
    WHEN 'crosses_above' THEN
        IF previous_sample.sample_id IS NULL THEN
            reason_code_value:='no_prior_sample';
        ELSE
            condition_met_value:=
                previous_sample.value<=trigger_row.threshold
                AND p_value>trigger_row.threshold;
            reason_code_value:=CASE WHEN condition_met_value
                THEN 'crossed' ELSE 'threshold_not_met' END;
        END IF;
    WHEN 'crosses_below' THEN
        IF previous_sample.sample_id IS NULL THEN
            reason_code_value:='no_prior_sample';
        ELSE
            condition_met_value:=
                previous_sample.value>=trigger_row.threshold
                AND p_value<trigger_row.threshold;
            reason_code_value:=CASE WHEN condition_met_value
                THEN 'crossed' ELSE 'threshold_not_met' END;
        END IF;
    END CASE;

    fired_value:=condition_met_value;
    IF fired_value AND last_fired_at IS NOT NULL
       AND last_fired_at+make_interval(
            secs=>trigger_row.cooldown_seconds
       )>p_observed_at THEN
        fired_value:=false;
        reason_code_value:='cooldown_suppressed';
    END IF;
    INSERT INTO agent_control.cortex_decision_trigger_sample(
        sample_id,trigger_id,trigger_generation,trigger_digest,value,
        prior_value,condition_met,fired,reason_code,observed_at,committed_at
    ) VALUES(
        sample_id_value,trigger_row.trigger_id,trigger_row.generation,
        trigger_row.record_digest,p_value,
        CASE WHEN previous_sample.sample_id IS NULL
            THEN NULL ELSE previous_sample.value END,
        condition_met_value,fired_value,reason_code_value,
        p_observed_at,at_time
    );
    RETURN jsonb_strip_nulls(jsonb_build_object(
        'sample_id',sample_id_value,
        'trigger_id',trigger_row.trigger_id,
        'generation',trigger_row.generation,
        'value',p_value,
        'prior_value',CASE WHEN previous_sample.sample_id IS NULL
            THEN NULL ELSE previous_sample.value END,
        'condition_met',condition_met_value,
        'fired',fired_value,
        'reason_code',reason_code_value,
        'observed_at',agent_control.runtime_utc_text(p_observed_at),
        'committed_at',agent_control.runtime_utc_text(at_time)
    ));
END
$$;

CREATE OR REPLACE FUNCTION agent_control.cortex_decision_trigger_json(
    p_trigger agent_control.cortex_decision_trigger_revision
) RETURNS JSONB
LANGUAGE sql
STABLE
SET search_path=pg_catalog,agent_control
SET timezone='UTC'
AS $$
    SELECT jsonb_strip_nulls(jsonb_build_object(
        'trigger_id',p_trigger.trigger_id,
        'generation',p_trigger.generation,
        'title',p_trigger.title,
        'strategy_id',p_trigger.strategy_id,
        'data_source',p_trigger.data_source,
        'symbol',p_trigger.symbol,
        'metric',p_trigger.metric,
        'comparator',p_trigger.comparator,
        'threshold',p_trigger.threshold,
        'cooldown_seconds',p_trigger.cooldown_seconds,
        'objective',p_trigger.objective,
        'enabled',p_trigger.enabled,
        'state',CASE WHEN p_trigger.enabled THEN 'armed' ELSE 'paused' END,
        'updated_at',agent_control.runtime_utc_text(p_trigger.created_at),
        'last_value',latest.value,
        'last_observed_at',CASE WHEN latest.observed_at IS NULL THEN NULL
            ELSE agent_control.runtime_utc_text(latest.observed_at) END,
        'last_reason_code',latest.reason_code,
        'last_fired_at',CASE WHEN fired.observed_at IS NULL THEN NULL
            ELSE agent_control.runtime_utc_text(fired.observed_at) END
    ))
    FROM (SELECT 1) AS present
    LEFT JOIN LATERAL(
        SELECT sample.value,sample.observed_at,sample.reason_code
        FROM agent_control.cortex_decision_trigger_sample AS sample
        WHERE sample.trigger_id=p_trigger.trigger_id
        ORDER BY sample.observed_at DESC,sample.sample_id DESC
        LIMIT 1
    ) AS latest ON true
    LEFT JOIN LATERAL(
        SELECT sample.observed_at
        FROM agent_control.cortex_decision_trigger_sample AS sample
        WHERE sample.trigger_id=p_trigger.trigger_id AND sample.fired
        ORDER BY sample.observed_at DESC,sample.sample_id DESC
        LIMIT 1
    ) AS fired ON true
$$;

REVOKE ALL ON TABLE
agent_control.cortex_decision_trigger_sample
FROM PUBLIC;
REVOKE ALL ON FUNCTION
agent_control.record_cortex_decision_trigger_sample(TEXT,NUMERIC,TIMESTAMPTZ)
FROM PUBLIC;
GRANT EXECUTE ON FUNCTION
agent_control.record_cortex_decision_trigger_sample(TEXT,NUMERIC,TIMESTAMPTZ)
TO alpheus_agent_control_api;

RESET ROLE;
