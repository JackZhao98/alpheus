-- Replay observations are an explicit simulated Trigger source. They keep
-- virtual market time and replay identity separate from recent live GEX
-- samples, while reusing the immutable occurrence and Cortex wake chain.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

ALTER TABLE agent_control.cortex_decision_trigger_revision
DROP CONSTRAINT cortex_decision_trigger_revision_data_source_check;

ALTER TABLE agent_control.cortex_decision_trigger_revision
ADD CONSTRAINT cortex_decision_trigger_revision_data_source_check
CHECK(data_source IN(
  'kernel_quote','research_gexbot','moody_blues_replay'
));

DO $migration$
DECLARE
  definition TEXT;
  original TEXT;
BEGIN
  SELECT pg_get_functiondef(
    'agent_control.register_cortex_decision_trigger(text,jsonb)'::regprocedure
  ) INTO definition;
  original:=definition;
  definition:=replace(
    definition,
    $old$       OR coalesce(p_command->>'data_source','') NOT IN(
            'kernel_quote','research_gexbot'
       )$old$,
    $new$       OR coalesce(p_command->>'data_source','') NOT IN(
            'kernel_quote','research_gexbot','moody_blues_replay'
       )$new$
  );
  definition:=replace(
    definition,
    $old$            p_command->>'data_source'='research_gexbot'
            AND p_command->>'metric' NOT IN($old$,
    $new$            p_command->>'data_source' IN(
                'research_gexbot','moody_blues_replay'
            )
            AND p_command->>'metric' NOT IN($new$
  );
  IF definition=original
     OR position('moody_blues_replay' IN definition)=0 THEN
    RAISE EXCEPTION
      'expected Cortex Decision Trigger replay source boundary';
  END IF;
  EXECUTE definition;
END
$migration$;

CREATE TABLE agent_control.cortex_moody_blues_replay_trigger_sample(
  sample_id TEXT PRIMARY KEY REFERENCES
    agent_control.cortex_decision_trigger_sample(sample_id),
  trigger_id TEXT NOT NULL,
  trigger_generation BIGINT NOT NULL,
  replay_id UUID NOT NULL,
  replay_generation BIGINT NOT NULL CHECK(replay_generation>=2),
  observation_id TEXT NOT NULL CHECK(
    observation_id=btrim(observation_id)
    AND observation_id<>''
    AND octet_length(observation_id)<=200
    AND observation_id!~'[[:cntrl:]]'
  ),
  observation_record_digest CHAR(64) NOT NULL CHECK(
    agent_control.runtime_digest_valid(
      observation_record_digest::TEXT
    )
  ),
  normalized_digest CHAR(64) NOT NULL CHECK(
    agent_control.runtime_digest_valid(normalized_digest::TEXT)
  ),
  normalized JSONB NOT NULL,
  committed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  FOREIGN KEY(trigger_id,trigger_generation)
    REFERENCES agent_control.cortex_decision_trigger_revision(
      trigger_id,generation
    ),
  UNIQUE(
    replay_id,replay_generation,trigger_id,trigger_generation
  )
);

CREATE INDEX cortex_moody_blues_replay_trigger_sample_previous_idx
ON agent_control.cortex_moody_blues_replay_trigger_sample(
  replay_id,trigger_id,replay_generation DESC
);

CREATE TRIGGER cortex_moody_blues_replay_trigger_sample_immutable
BEFORE UPDATE OR DELETE
ON agent_control.cortex_moody_blues_replay_trigger_sample
FOR EACH ROW EXECUTE FUNCTION
agent_control.reject_immutable_runtime_definition_mutation();

CREATE FUNCTION
agent_control.record_cortex_moody_blues_replay_trigger_sample(
  p_trigger_id TEXT,
  p_value NUMERIC,
  p_virtual_observed_at TIMESTAMPTZ,
  p_replay_id UUID,
  p_replay_generation BIGINT,
  p_observation_id TEXT,
  p_observation_record_digest TEXT,
  p_normalized JSONB
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
  previous_binding
    agent_control.cortex_moody_blues_replay_trigger_sample%ROWTYPE;
  existing_binding
    agent_control.cortex_moody_blues_replay_trigger_sample%ROWTYPE;
  existing_sample agent_control.cortex_decision_trigger_sample%ROWTYPE;
  last_fired_at TIMESTAMPTZ;
  condition_met_value BOOLEAN:=false;
  fired_value BOOLEAN:=false;
  reason_code_value TEXT:='threshold_not_met';
  sample_id_value TEXT:=gen_random_uuid()::TEXT;
  normalized_digest_value CHAR(64);
  expected_value NUMERIC;
BEGIN
  SELECT * INTO STRICT invoker
  FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api'
     OR invoker.profile_id<>'control-api'
     OR invoker.owner_id<>'agent_control'
     OR NOT agent_control.runtime_identifier_valid(p_trigger_id)
     OR p_value IS NULL
     OR p_value NOT BETWEEN -1000000000 AND 1000000000
     OR p_virtual_observed_at IS NULL
     OR p_virtual_observed_at>at_time
     OR p_replay_id IS NULL
     OR p_replay_generation<2
     OR coalesce(p_observation_id,'')<>btrim(
       coalesce(p_observation_id,'')
     )
     OR coalesce(p_observation_id,'')=''
     OR octet_length(p_observation_id)>200
     OR p_observation_id~'[[:cntrl:]]'
     OR NOT agent_control.runtime_digest_valid(
       coalesce(p_observation_record_digest,'')
     )
     OR jsonb_typeof(p_normalized)<>'object'
     OR p_normalized-ARRAY[
       'schema_revision','transform_id','replay_id',
       'replay_generation','observation_id',
       'observation_record_digest','symbol','category',
       'virtual_observed_at','metrics'
     ]<>'{}'::JSONB
     OR p_normalized->>'schema_revision'<>'1'
     OR p_normalized->>'transform_id'<>'gex_compact_v1'
     OR p_normalized->>'replay_id'<>p_replay_id::TEXT
     OR (p_normalized->>'replay_generation')::BIGINT
       <>p_replay_generation
     OR p_normalized->>'observation_id'<>p_observation_id
     OR p_normalized->>'observation_record_digest'
       <>p_observation_record_digest
     OR p_normalized->>'symbol'<>'SPX'
     OR p_normalized->>'category'<>'gex_full'
     OR (p_normalized->>'virtual_observed_at')::TIMESTAMPTZ
       <>p_virtual_observed_at
     OR jsonb_typeof(p_normalized->'metrics')<>'object'
     OR p_normalized->'metrics'-ARRAY[
       'spot','zero_gamma','major_pos_oi','major_neg_oi'
     ]<>'{}'::JSONB
     OR jsonb_typeof(p_normalized->'metrics'->'spot')<>'number'
     OR jsonb_typeof(
       p_normalized->'metrics'->'zero_gamma'
     )<>'number'
     OR jsonb_typeof(
       p_normalized->'metrics'->'major_pos_oi'
     )<>'number'
     OR jsonb_typeof(
       p_normalized->'metrics'->'major_neg_oi'
     )<>'number' THEN
    RAISE EXCEPTION USING ERRCODE='22023',
      MESSAGE='invalid Moody Blues replay Trigger sample';
  END IF;
  normalized_digest_value:=agent_control.runtime_contract_digest(
    'agent_control.moody_blues_gex_compact.v1',p_normalized
  );
  PERFORM pg_advisory_xact_lock(hashtextextended(p_trigger_id,0));
  SELECT revision.* INTO trigger_row
  FROM agent_control.cortex_decision_trigger_head AS head
  JOIN agent_control.cortex_decision_trigger_revision AS revision
    ON revision.trigger_id=head.trigger_id
   AND revision.generation=head.generation
   AND revision.record_digest=head.record_digest
  WHERE head.trigger_id=p_trigger_id
    AND revision.enabled
    AND revision.data_source='moody_blues_replay'
    AND revision.symbol='SPX'
  FOR SHARE OF head;
  IF NOT FOUND THEN
    RAISE EXCEPTION USING ERRCODE='55000',
      MESSAGE='Moody Blues replay Trigger is not evaluable';
  END IF;
  expected_value:=CASE trigger_row.metric
    WHEN 'gex_call_wall' THEN
      (p_normalized->'metrics'->>'major_pos_oi')::NUMERIC
    WHEN 'gex_put_wall' THEN
      (p_normalized->'metrics'->>'major_neg_oi')::NUMERIC
    WHEN 'gex_zero_gamma' THEN
      (p_normalized->'metrics'->>'zero_gamma')::NUMERIC
    ELSE NULL
  END;
  IF expected_value IS NULL OR expected_value<>p_value THEN
    RAISE EXCEPTION USING ERRCODE='22023',
      MESSAGE='replay Trigger metric does not match normalized frame';
  END IF;
  SELECT * INTO existing_binding
  FROM agent_control.cortex_moody_blues_replay_trigger_sample
  WHERE replay_id=p_replay_id
    AND replay_generation=p_replay_generation
    AND trigger_id=trigger_row.trigger_id
    AND trigger_generation=trigger_row.generation;
  IF FOUND THEN
    SELECT * INTO STRICT existing_sample
    FROM agent_control.cortex_decision_trigger_sample
    WHERE sample_id=existing_binding.sample_id;
    IF existing_binding.observation_id<>p_observation_id
       OR existing_binding.observation_record_digest::TEXT
         <>p_observation_record_digest
       OR existing_binding.normalized_digest
         <>normalized_digest_value
       OR existing_sample.value<>p_value
       OR existing_sample.observed_at<>p_virtual_observed_at THEN
      RAISE EXCEPTION USING ERRCODE='23505',
        MESSAGE='replay Trigger generation conflict';
    END IF;
    RETURN jsonb_strip_nulls(jsonb_build_object(
      'sample_id',existing_sample.sample_id,
      'trigger_id',existing_sample.trigger_id,
      'generation',existing_sample.trigger_generation,
      'value',existing_sample.value,
      'prior_value',existing_sample.prior_value,
      'condition_met',existing_sample.condition_met,
      'fired',existing_sample.fired,
      'reason_code',existing_sample.reason_code,
      'observed_at',agent_control.runtime_utc_text(
        existing_sample.observed_at
      ),
      'committed_at',agent_control.runtime_utc_text(
        existing_sample.committed_at
      )
    ));
  END IF;
  SELECT binding.*
  INTO previous_binding
  FROM agent_control.cortex_moody_blues_replay_trigger_sample AS binding
  WHERE binding.replay_id=p_replay_id
    AND binding.trigger_id=trigger_row.trigger_id
    AND binding.trigger_generation=trigger_row.generation
  ORDER BY binding.replay_generation DESC
  LIMIT 1;
  IF previous_binding.sample_id IS NOT NULL THEN
    SELECT * INTO STRICT previous_sample
    FROM agent_control.cortex_decision_trigger_sample
    WHERE sample_id=previous_binding.sample_id;
  END IF;
  IF previous_binding.sample_id IS NOT NULL
     AND (
       previous_binding.replay_generation>=p_replay_generation
       OR previous_sample.observed_at>p_virtual_observed_at
     ) THEN
    RAISE EXCEPTION USING ERRCODE='22023',
      MESSAGE='replay Trigger frame is not monotonic';
  END IF;
  SELECT max(sample.observed_at) INTO last_fired_at
  FROM agent_control.cortex_moody_blues_replay_trigger_sample AS binding
  JOIN agent_control.cortex_decision_trigger_sample AS sample
    ON sample.sample_id=binding.sample_id
  WHERE binding.replay_id=p_replay_id
    AND binding.trigger_id=trigger_row.trigger_id
    AND binding.trigger_generation=trigger_row.generation
    AND sample.fired;

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
    IF previous_binding.sample_id IS NULL THEN
      reason_code_value:='no_prior_sample';
    ELSE
      condition_met_value:=
        previous_sample.value<=trigger_row.threshold
        AND p_value>trigger_row.threshold;
      reason_code_value:=CASE WHEN condition_met_value
        THEN 'crossed' ELSE 'threshold_not_met' END;
    END IF;
  WHEN 'crosses_below' THEN
    IF previous_binding.sample_id IS NULL THEN
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
     )>p_virtual_observed_at THEN
    fired_value:=false;
    reason_code_value:='cooldown_suppressed';
  END IF;
  INSERT INTO agent_control.cortex_decision_trigger_sample(
    sample_id,trigger_id,trigger_generation,trigger_digest,value,
    prior_value,condition_met,fired,reason_code,observed_at,
    committed_at
  ) VALUES(
    sample_id_value,trigger_row.trigger_id,trigger_row.generation,
    trigger_row.record_digest,p_value,
    CASE WHEN previous_binding.sample_id IS NULL
      THEN NULL ELSE previous_sample.value END,
    condition_met_value,fired_value,reason_code_value,
    p_virtual_observed_at,at_time
  );
  INSERT INTO agent_control.cortex_moody_blues_replay_trigger_sample(
    sample_id,trigger_id,trigger_generation,replay_id,
    replay_generation,observation_id,observation_record_digest,
    normalized_digest,normalized,committed_at
  ) VALUES(
    sample_id_value,trigger_row.trigger_id,trigger_row.generation,
    p_replay_id,p_replay_generation,p_observation_id,
    p_observation_record_digest,normalized_digest_value,p_normalized,
    at_time
  );
  RETURN jsonb_strip_nulls(jsonb_build_object(
    'sample_id',sample_id_value,
    'trigger_id',trigger_row.trigger_id,
    'generation',trigger_row.generation,
    'value',p_value,
    'prior_value',CASE WHEN previous_binding.sample_id IS NULL
      THEN NULL ELSE previous_sample.value END,
    'condition_met',condition_met_value,
    'fired',fired_value,
    'reason_code',reason_code_value,
    'observed_at',agent_control.runtime_utc_text(
      p_virtual_observed_at
    ),
    'committed_at',agent_control.runtime_utc_text(at_time)
  ));
END
$$;

REVOKE ALL ON TABLE
agent_control.cortex_moody_blues_replay_trigger_sample
FROM PUBLIC;
REVOKE ALL ON FUNCTION
agent_control.register_cortex_decision_trigger(TEXT,JSONB),
agent_control.record_cortex_moody_blues_replay_trigger_sample(
  TEXT,NUMERIC,TIMESTAMPTZ,UUID,BIGINT,TEXT,TEXT,JSONB
)
FROM PUBLIC;
GRANT EXECUTE ON FUNCTION
agent_control.register_cortex_decision_trigger(TEXT,JSONB),
agent_control.record_cortex_moody_blues_replay_trigger_sample(
  TEXT,NUMERIC,TIMESTAMPTZ,UUID,BIGINT,TEXT,TEXT,JSONB
)
TO alpheus_agent_control_api;

RESET ROLE;
