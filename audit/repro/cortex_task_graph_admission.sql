\set ON_ERROR_STOP on
BEGIN;
SET CONSTRAINTS ALL DEFERRED;

-- Build one complete but rollback-only parent Run/Task/Session/Attempt/Turn/
-- Result. It reuses immutable policy, schema and Blob references but creates
-- no external call and commits no fixture data.
DO $fixture$
DECLARE
    at_time TIMESTAMPTZ:=clock_timestamp();
    deadline_value TIMESTAMPTZ:=at_time+interval '30 minutes';
    source_run agent_control.runtime_run%ROWTYPE;
    source_task agent_control.runtime_task%ROWTYPE;
    source_session agent_control.runtime_session%ROWTYPE;
    policy agent_control.runtime_policy_revision%ROWTYPE;
    root_contract agent_control.output_contract_revision%ROWTYPE;
    request_digest TEXT:=agent_control.runtime_sha256_json('{"probe":"task-graph-request"}'::JSONB);
    manifest_digest TEXT:=agent_control.runtime_sha256_json('{"probe":"task-graph-manifest"}'::JSONB);
    result_digest_value TEXT:=agent_control.runtime_sha256_json('{"probe":"task-graph-result"}'::JSONB);
    output_ref JSONB;
BEGIN
    SELECT run.* INTO STRICT source_run
    FROM agent_control.runtime_run run
    WHERE run.origin_kind='user_request'
      AND agent_control.runtime_run_admission_current(run.run_id)
    ORDER BY run.created_at DESC LIMIT 1;
    SELECT task.* INTO STRICT source_task
    FROM agent_control.runtime_task task
    WHERE task.run_id=source_run.run_id AND task.depth=0;
    SELECT session.* INTO STRICT source_session
    FROM agent_control.runtime_session session
    WHERE session.task_id=source_task.task_id
    ORDER BY session.created_at DESC LIMIT 1;
    SELECT * INTO STRICT policy
    FROM agent_control.runtime_policy_revision
    WHERE policy_id=source_run.runtime_policy_id
      AND generation=source_run.runtime_policy_generation
      AND record_digest=source_run.runtime_policy_digest;
    SELECT * INTO STRICT root_contract
    FROM agent_control.output_contract_revision
    WHERE revision_id=source_task.output_contract_revision_id
      AND generation=source_task.output_contract_generation
      AND record_digest=source_task.output_contract_digest;

    INSERT INTO agent_control.runtime_run(
        run_id,schema_revision,occurrence_owner,occurrence_record_type,occurrence_id,
        occurrence_schema_revision,occurrence_digest,origin_kind,origin_source_owner,
        origin_source_record_type,origin_source_record_id,origin_source_schema_revision,
        origin_source_record_digest,origin_conversation_owner,origin_conversation_record_type,
        origin_conversation_record_id,origin_conversation_schema_revision,
        origin_conversation_record_digest,origin_initiating_principal_id,
        origin_initiating_kind,origin_initiating_audience,origin_owner_policy_owner,
        origin_owner_policy_record_type,origin_owner_policy_record_id,
        origin_owner_policy_schema_revision,origin_owner_policy_record_digest,
        origin_owner_policy_generation,origin_occurred_at,origin_observed_at,
        origin_committed_at,runtime_policy_owner,runtime_policy_record_type,
        runtime_policy_id,runtime_policy_schema_revision,runtime_policy_generation,
        runtime_policy_digest,budget_ledger_id,root_task_id,state,state_generation,
        created_at,updated_at,deadline_at
    ) VALUES(
        'tg-probe-run',1,source_run.occurrence_owner,source_run.occurrence_record_type,
        source_run.occurrence_id,source_run.occurrence_schema_revision,
        source_run.occurrence_digest,source_run.origin_kind,source_run.origin_source_owner,
        source_run.origin_source_record_type,source_run.origin_source_record_id,
        source_run.origin_source_schema_revision,source_run.origin_source_record_digest,
        source_run.origin_conversation_owner,source_run.origin_conversation_record_type,
        source_run.origin_conversation_record_id,source_run.origin_conversation_schema_revision,
        source_run.origin_conversation_record_digest,source_run.origin_initiating_principal_id,
        source_run.origin_initiating_kind,source_run.origin_initiating_audience,
        source_run.origin_owner_policy_owner,source_run.origin_owner_policy_record_type,
        source_run.origin_owner_policy_record_id,source_run.origin_owner_policy_schema_revision,
        source_run.origin_owner_policy_record_digest,source_run.origin_owner_policy_generation,
        source_run.origin_occurred_at,source_run.origin_observed_at,
        source_run.origin_committed_at,source_run.runtime_policy_owner,
        source_run.runtime_policy_record_type,source_run.runtime_policy_id,
        source_run.runtime_policy_schema_revision,source_run.runtime_policy_generation,
        source_run.runtime_policy_digest,'tg-probe-run-ledger','tg-probe-root',
        'queued',1,at_time,at_time,deadline_value
    );
    INSERT INTO agent_control.runtime_budget_ledger(
        ledger_id,schema_revision,scope,scope_id,parent_ledger_id,
        runtime_policy_owner,runtime_policy_record_type,runtime_policy_id,
        runtime_policy_schema_revision,runtime_policy_generation,runtime_policy_digest,
        limit_model_calls,limit_input_tokens,limit_output_tokens,limit_tool_calls,
        limit_external_cost_micro_usd,limit_wall_time_ms,limit_idle_time_ms,
        limit_tasks,limit_depth,limit_fanout,limit_parallelism,
        limit_invalid_output_retries,limit_infrastructure_retries,
        consumed_tasks,generation,state,updated_at
    ) VALUES
    ('tg-probe-run-ledger',1,'run','tg-probe-run',NULL,'agent_control',
        'runtime_policy',policy.policy_id,1,policy.generation,policy.record_digest,
        policy.max_model_calls,policy.max_input_tokens,policy.max_output_tokens,
        policy.max_tool_calls,policy.max_external_cost_micro_usd,policy.max_wall_time_ms,
        policy.max_idle_time_ms,policy.max_tasks,policy.max_depth,policy.max_fanout,
        policy.max_parallelism,policy.max_invalid_output_retries,
        policy.max_infrastructure_retries,1,1,'open',at_time),
    ('tg-probe-root-ledger',1,'task','tg-probe-root','tg-probe-run-ledger',
        'agent_control','runtime_policy',policy.policy_id,1,policy.generation,
        policy.record_digest,policy.max_model_calls,policy.max_input_tokens,
        policy.max_output_tokens,policy.max_tool_calls,
        policy.max_external_cost_micro_usd,policy.max_wall_time_ms,
        policy.max_idle_time_ms,policy.max_tasks,policy.max_depth,
        policy.max_fanout,policy.max_parallelism,policy.max_invalid_output_retries,
        policy.max_infrastructure_retries,1,1,'open',at_time);
    INSERT INTO agent_control.runtime_task(
        task_id,schema_revision,run_id,depth,objective,output_contract_owner,
        output_contract_record_type,output_contract_revision_id,
        output_contract_schema_revision,output_contract_generation,
        output_contract_digest,budget_ledger_id,state,state_generation,
        budget_slot_held,created_at,updated_at,deadline_at
    ) VALUES(
        'tg-probe-root',1,'tg-probe-run',0,source_task.objective,'agent_control',
        'output_contract_revision',root_contract.revision_id,1,
        root_contract.generation,root_contract.record_digest,'tg-probe-root-ledger',
        'ready',1,false,at_time,at_time,deadline_value
    );
    INSERT INTO agent_control.runtime_session(
        session_id,schema_revision,run_id,task_id,generation,execution_binding,
        context_manifest,state,created_at
    ) VALUES(
        'tg-probe-session',1,'tg-probe-run','tg-probe-root',1,
        source_session.execution_binding,source_session.context_manifest,'open',at_time
    );
    UPDATE agent_control.runtime_task SET session_id='tg-probe-session'
    WHERE task_id='tg-probe-root';
    UPDATE agent_control.runtime_run SET state='running',state_generation=2,
        updated_at=at_time WHERE run_id='tg-probe-run';
    UPDATE agent_control.runtime_task SET state='running',state_generation=2,
        budget_slot_held=true,updated_at=at_time WHERE task_id='tg-probe-root';
    UPDATE agent_control.runtime_budget_ledger SET consumed_active_tasks=1,
        generation=2,updated_at=at_time WHERE ledger_id='tg-probe-run-ledger';

    INSERT INTO agent_control.runtime_attempt(
        attempt_id,schema_revision,run_id,task_id,session_id,ordinal,state,
        state_generation,lease_generation,lease_token,lease_worker,
        lease_claimed_at,lease_heartbeat_at,lease_expires_at,created_at,updated_at
    ) VALUES(
        'tg-probe-attempt',1,'tg-probe-run','tg-probe-root','tg-probe-session',
        1,'leased',1,1,'00000000-0000-4000-8000-000000000099',
        '{"principal_id":"cortex-worker-1","kind":"workload","audience":"worker"}',
        at_time,at_time,deadline_value,at_time,at_time
    );
    UPDATE agent_control.runtime_attempt SET state='executing',
        state_generation=2,updated_at=at_time WHERE attempt_id='tg-probe-attempt';
    INSERT INTO agent_control.runtime_turn(
        turn_id,schema_revision,run_id,task_id,session_id,attempt_id,ordinal,
        kind,state,state_generation,request_digest,reservation_held,created_at,updated_at
    ) VALUES(
        'tg-probe-turn',1,'tg-probe-run','tg-probe-root','tg-probe-session',
        'tg-probe-attempt',1,'model_call','planned',1,request_digest,false,at_time,at_time
    );
    UPDATE agent_control.runtime_turn SET state='dispatched',state_generation=2,
        reservation_held=true,dispatched_at=at_time,updated_at=at_time
    WHERE turn_id='tg-probe-turn';
    INSERT INTO agent_control.runtime_model_call_manifest(
        call_id,schema_revision,record_digest,turn_id,attempt_id,idempotency_key,
        provider,model,prompt_digest,context_manifest,output_contract_digest,
        request_digest,max_output_tokens,reserved_input_tokens,
        reserved_external_cost_micro_usd,timeout_ms,temperature_micros,created_at
    ) VALUES(
        'tg-probe-call',1,manifest_digest,'tg-probe-turn','tg-probe-attempt',
        'tg-probe-model-idempotency','openai','gpt-5-6-sol',
        agent_control.runtime_sha256_json('{"probe":"prompt"}'::JSONB),
        source_session.context_manifest,root_contract.record_digest,request_digest,
        1000,1000,0,30000,0,at_time
    );
    output_ref:=jsonb_build_object(
        'schema_revision',1,
        'blob_id',source_session.context_manifest->>'blob_id',
        'content_digest',source_session.context_manifest->>'content_digest',
        'media_type','application/json',
        'size_bytes',(source_session.context_manifest->>'size_bytes')::BIGINT,
        'origin',jsonb_build_object(
            'owner','agent_control','record_type','model_call_manifest',
            'record_id','tg-probe-call','schema_revision',1,
            'record_digest',manifest_digest
        ),
        'committed_at',agent_control.runtime_utc_text(at_time)
    );
    INSERT INTO agent_control.runtime_model_call_result(
        result_id,schema_revision,record_digest,call_id,attempt_id,turn_id,
        idempotency_key,request_digest,provider_request_id,output_origin_owner,
        output_origin_record_type,output_origin_record_id,
        output_origin_schema_revision,output_origin_record_digest,output,
        input_tokens,output_tokens,external_cost_micro_usd,wall_time_ms,
        finish_reason,committed_at
    ) VALUES(
        'tg-probe-result',1,result_digest_value,'tg-probe-call','tg-probe-attempt',
        'tg-probe-turn','tg-probe-model-idempotency',request_digest,
        'tg-probe-provider-request','agent_control','model_call_manifest',
        'tg-probe-call',1,manifest_digest,output_ref,100,100,0,1000,'stop',at_time
    );
    UPDATE agent_control.runtime_turn SET state='result_committed',
        state_generation=3,result_owner='agent_control',
        result_record_type='model_call_result',result_id='tg-probe-result',
        result_schema_revision=1,result_digest=result_digest_value,reservation_held=false,
        finished_at=at_time,updated_at=at_time WHERE turn_id='tg-probe-turn';
END
$fixture$;

CREATE TEMP TABLE task_graph_probe_command(command JSONB);
INSERT INTO task_graph_probe_command(command)
WITH fixture AS (
    SELECT
        task.objective,
        jsonb_build_object(
            'owner','agent_control','record_type','model_call_result',
            'record_id',result.result_id,'schema_revision',1,
            'record_digest',result.record_digest::TEXT
        ) source_result,
        jsonb_build_object(
            'owner','agent_control','record_type','runtime_policy',
            'record_id',run.runtime_policy_id,'schema_revision',1,
            'record_digest',run.runtime_policy_digest::TEXT,
            'generation',run.runtime_policy_generation
        ) runtime_policy,
        jsonb_build_object(
            'owner','agent_control','record_type','output_contract_revision',
            'record_id',memo.revision_id,'schema_revision',1,
            'record_digest',memo.record_digest::TEXT,'generation',memo.generation
        ) memo_contract,
        jsonb_build_object(
            'owner','agent_control','record_type','output_contract_revision',
            'record_id',answer.revision_id,'schema_revision',1,
            'record_digest',answer.record_digest::TEXT,'generation',answer.generation
        ) answer_contract,
        result.committed_at+interval '1 microsecond' created_at,
        run.deadline_at
    FROM agent_control.runtime_run run
    JOIN agent_control.runtime_task task ON task.task_id=run.root_task_id
    JOIN agent_control.runtime_model_call_result result
      ON result.result_id='tg-probe-result'
    JOIN agent_control.output_contract_revision memo
      ON memo.revision_id='cortex-scout-research-memo-v1'
    JOIN agent_control.output_contract_revision answer
      ON answer.revision_id='cortex-text-output-v1'
    WHERE run.run_id='tg-probe-run'
), plan AS (
    SELECT jsonb_build_object(
        'schema_revision',1,'graph_id','tg-probe-graph','run_id','tg-probe-run',
        'parent_task_id','tg-probe-root','source_result',source_result,
        'runtime_policy',runtime_policy,'round',1,'max_rounds',2,
        'authorized_limit',jsonb_build_object(
            'max_model_calls',5,'max_input_tokens',8000,'max_output_tokens',4000,
            'max_tool_calls',1,'max_external_cost_micro_usd',0,
            'max_wall_time_ms',120000,'max_idle_time_ms',20000,'max_tasks',4,
            'max_depth',2,'max_fanout',1,'max_parallelism',2,
            'max_invalid_output_retries',0,'max_infrastructure_retries',0
        ),
        'nodes',jsonb_build_array(
            jsonb_build_object(
                'task_id','tg-probe-market','role_id','market_scout',
                'role_revision',1,'depth',1,'objective',objective,'input_refs','[]'::JSONB,
                'output_contract_name','specialist_memo_v1','output_contract',memo_contract,
                'tool_grants',jsonb_build_array(jsonb_build_object(
                    'tool_id','kernel_equity_quotes','tool_revision',1,'effect','read_only')),
                'limit',jsonb_build_object(
                    'max_model_calls',2,'max_input_tokens',2000,'max_output_tokens',1000,
                    'max_tool_calls',1,'max_external_cost_micro_usd',0,
                    'max_wall_time_ms',30000,'max_idle_time_ms',5000,'max_tasks',1,
                    'max_depth',0,'max_fanout',0,'max_parallelism',1,
                    'max_invalid_output_retries',0,'max_infrastructure_retries',0),
                'deadline_at',agent_control.runtime_utc_text(deadline_at-interval '2 minutes')),
            jsonb_build_object(
                'task_id','tg-probe-fundamental','role_id','fundamental_scout',
                'role_revision',1,'depth',1,'objective',objective,'input_refs','[]'::JSONB,
                'output_contract_name','specialist_memo_v1','output_contract',memo_contract,
                'tool_grants','[]'::JSONB,
                'limit',jsonb_build_object(
                    'max_model_calls',1,'max_input_tokens',2000,'max_output_tokens',1000,
                    'max_tool_calls',0,'max_external_cost_micro_usd',0,
                    'max_wall_time_ms',30000,'max_idle_time_ms',5000,'max_tasks',1,
                    'max_depth',0,'max_fanout',0,'max_parallelism',1,
                    'max_invalid_output_retries',0,'max_infrastructure_retries',0),
                'deadline_at',agent_control.runtime_utc_text(deadline_at-interval '2 minutes')),
            jsonb_build_object(
                'task_id','tg-probe-options','role_id','options_scout',
                'role_revision',1,'depth',1,'objective',objective,'input_refs','[]'::JSONB,
                'output_contract_name','specialist_memo_v1','output_contract',memo_contract,
                'tool_grants','[]'::JSONB,
                'limit',jsonb_build_object(
                    'max_model_calls',1,'max_input_tokens',2000,'max_output_tokens',1000,
                    'max_tool_calls',0,'max_external_cost_micro_usd',0,
                    'max_wall_time_ms',30000,'max_idle_time_ms',5000,'max_tasks',1,
                    'max_depth',0,'max_fanout',0,'max_parallelism',1,
                    'max_invalid_output_retries',0,'max_infrastructure_retries',0),
                'deadline_at',agent_control.runtime_utc_text(deadline_at-interval '2 minutes')),
            jsonb_build_object(
                'task_id','tg-probe-desk','role_id','decision_desk',
                'role_revision',1,'depth',2,'objective',objective,'input_refs','[]'::JSONB,
                'output_contract_name','answer_v1','output_contract',answer_contract,
                'tool_grants','[]'::JSONB,
                'limit',jsonb_build_object(
                    'max_model_calls',1,'max_input_tokens',2000,'max_output_tokens',1000,
                    'max_tool_calls',0,'max_external_cost_micro_usd',0,
                    'max_wall_time_ms',30000,'max_idle_time_ms',5000,'max_tasks',1,
                    'max_depth',0,'max_fanout',0,'max_parallelism',1,
                    'max_invalid_output_retries',0,'max_infrastructure_retries',0),
                'deadline_at',agent_control.runtime_utc_text(deadline_at-interval '1 minute'))
        ),
        'edges',jsonb_build_array(
            jsonb_build_object('from_task_id','tg-probe-market','to_task_id','tg-probe-desk'),
            jsonb_build_object('from_task_id','tg-probe-fundamental','to_task_id','tg-probe-desk'),
            jsonb_build_object('from_task_id','tg-probe-options','to_task_id','tg-probe-desk')),
        'joins',jsonb_build_array(jsonb_build_object(
            'join_id','tg-probe-join','downstream_task_id','tg-probe-desk',
            'upstream_task_ids',jsonb_build_array(
                'tg-probe-market','tg-probe-fundamental','tg-probe-options'),
            'policy','all_required','minimum_success',3,'failure_policy','fail_graph',
            'deadline_at',agent_control.runtime_utc_text(deadline_at-interval '3 minutes'))),
        'created_by',jsonb_build_object(
            'principal_id','cortex-control-1','kind','workload','audience','control_api'),
        'created_at',agent_control.runtime_utc_text(created_at),
        'deadline_at',agent_control.runtime_utc_text(deadline_at-interval '30 seconds')
    ) value FROM fixture
), command AS (
    SELECT jsonb_build_object(
        'schema_revision',1,
        'envelope',jsonb_build_object(
            'schema_revision',1,'command_id','tg-probe-command',
            'actor',jsonb_build_object(
                'principal_id','cortex-control-1','kind','workload','audience','control_api'),
            'audience','control_api','command_type','admit_task_graph',
            'idempotency_key','tg-probe-idempotency',
            'request_digest',agent_control.runtime_contract_digest(
                'agent-platform.task-graph-plan.v1',value),
            'causation_id','tg-probe-root','correlation_id','tg-probe-run',
            'deadline',value->>'deadline_at'),
        'expected_run_state_generation',2,
        'expected_parent_state_generation',2,
        'plan',value
    ) value FROM plan
)
SELECT value FROM command;
GRANT SELECT ON task_graph_probe_command TO alpheus_agent_control_api;
CREATE TEMP TABLE task_graph_invalid_command AS
WITH changed_plan AS (
    SELECT jsonb_set(
        jsonb_set(command->'plan','{graph_id}','"tg-invalid-graph"'::JSONB),
        '{edges}',
        (command#>'{plan,edges}')||jsonb_build_array(jsonb_build_object(
            'from_task_id','tg-probe-desk','to_task_id','tg-probe-market'))
    ) value
    FROM task_graph_probe_command
)
SELECT jsonb_build_object(
    'schema_revision',1,
    'envelope',jsonb_build_object(
        'schema_revision',1,'command_id','tg-invalid-command',
        'actor',jsonb_build_object(
            'principal_id','cortex-control-1','kind','workload','audience','control_api'),
        'audience','control_api','command_type','admit_task_graph',
        'idempotency_key','tg-invalid-idempotency',
        'request_digest',agent_control.runtime_contract_digest(
            'agent-platform.task-graph-plan.v1',value),
        'causation_id','tg-probe-root','correlation_id','tg-probe-run',
        'deadline',value->>'deadline_at'),
    'expected_run_state_generation',2,
    'expected_parent_state_generation',2,
    'plan',value
) command
FROM changed_plan;
CREATE TEMP TABLE task_graph_conflict_command AS
SELECT jsonb_set(command,'{envelope,command_id}','"tg-conflict-command"'::JSONB) command
FROM task_graph_probe_command;
GRANT SELECT ON task_graph_invalid_command,task_graph_conflict_command
TO alpheus_agent_control_api;

SET SESSION AUTHORIZATION "cortex-control-1";
SET ROLE alpheus_agent_control_api;
SELECT agent_control.admit_cortex_task_graph(command) AS first_response
FROM task_graph_probe_command;
SELECT agent_control.admit_cortex_task_graph(command) AS exact_replay_response
FROM task_graph_probe_command;
DO $negative$
BEGIN
    BEGIN
        PERFORM agent_control.admit_cortex_task_graph(command)
        FROM task_graph_invalid_command;
        RAISE EXCEPTION 'cyclic graph unexpectedly admitted';
    EXCEPTION WHEN invalid_parameter_value THEN
        NULL;
    END;
    BEGIN
        PERFORM agent_control.admit_cortex_task_graph(command)
        FROM task_graph_conflict_command;
        RAISE EXCEPTION 'changed-body replay unexpectedly admitted';
    EXCEPTION WHEN unique_violation THEN
        NULL;
    END;
END
$negative$;
RESET ROLE;
RESET SESSION AUTHORIZATION;

CREATE TEMP TABLE task_graph_session_probe AS
SELECT
    node.task_id,
    jsonb_set(
        source_session.execution_binding,
        '{origin,record_id}',
        to_jsonb(node.task_id)
    ) AS execution_binding,
    jsonb_set(
        source_session.context_manifest,
        '{origin,record_id}',
        to_jsonb(node.task_id)
    ) AS context_manifest,
    request.raw_input,
    node.objective
FROM agent_control.cortex_task_graph_node AS node
JOIN agent_control.cortex_task_graph AS graph
  ON graph.graph_id=node.graph_id
JOIN agent_control.runtime_run AS run
  ON run.run_id=graph.run_id
JOIN agent_input.user_request AS request
  ON request.request_id=run.origin_source_record_id
 AND request.record_digest=run.origin_source_record_digest
JOIN agent_control.runtime_session AS source_session
  ON source_session.session_id='tg-probe-session'
WHERE node.graph_id='tg-probe-graph';
GRANT SELECT ON task_graph_session_probe TO alpheus_agent_control_api;

SET SESSION AUTHORIZATION "cortex-control-1";
SET ROLE alpheus_agent_control_api;
SELECT agent_control.prepare_cortex_task_graph_node_session(
    task_id,execution_binding,context_manifest,raw_input,objective,
    'cortex-worker-1'
) AS prepared
FROM task_graph_session_probe
ORDER BY task_id;
SELECT agent_control.prepare_cortex_task_graph_node_session(
    task_id,execution_binding,context_manifest,raw_input,objective,
    'cortex-worker-1'
) AS replayed
FROM task_graph_session_probe
WHERE task_id='tg-probe-market';
RESET ROLE;
RESET SESSION AUTHORIZATION;

SAVEPOINT task_graph_desk_discovery;
DO $desk_discovery_fixture$
DECLARE
    objective_ref JSONB;
    artifact_ref JSONB:=jsonb_build_object(
        'owner','agent_control','record_type','artifact',
        'record_id','tg-probe-fundamental-artifact',
        'schema_revision',1,'record_digest',repeat('d',64)
    );
BEGIN
    SELECT objective INTO STRICT objective_ref
    FROM agent_control.cortex_task_graph_node
    WHERE graph_id='tg-probe-graph'
      AND task_id='tg-probe-fundamental';
    UPDATE agent_control.runtime_task
    SET state='ready',state_generation=state_generation+1,
        updated_at=clock_timestamp()
    WHERE task_id='tg-probe-desk';
    INSERT INTO agent_control.cortex_task_graph_join_resolution(
        graph_id,join_id,downstream_task_id,outcome,
        successful_upstream_task_ids,failed_upstream_task_ids,
        inputs,record_digest,resolved_at
    ) VALUES(
        'tg-probe-graph','tg-probe-join','tg-probe-desk','ready',
        '["tg-probe-fundamental"]'::JSONB,'[]'::JSONB,
        jsonb_build_array(jsonb_build_object(
            'task_id','tg-probe-fundamental',
            'role_id','fundamental_scout',
            'artifact',artifact_ref,
            'content',jsonb_set(objective_ref,'{origin}',artifact_ref),
            'binding_id','cortex-session:probe:join:fundamental'
        )),
        agent_control.runtime_sha256_json(
            '{"probe":"decision-desk-discovery"}'::JSONB),
        clock_timestamp()
    );
END
$desk_discovery_fixture$;
SET SESSION AUTHORIZATION "cortex-worker-1";
SET ROLE alpheus_agent_worker;
DO $desk_discovery$
DECLARE item JSONB;
BEGIN
    item:=agent_control.next_cortex_task();
    IF item->>'task_id'<>'tg-probe-desk'
       OR item->>'role'<>'decision_desk'
       OR item->>'task_graph_join_id'<>'tg-probe-join'
       OR jsonb_array_length(item->'task_graph_join_inputs')<>1 THEN
        RAISE EXCEPTION 'TaskGraph Desk discovery assertion failed: %',item;
    END IF;
END
$desk_discovery$;
RESET ROLE;
RESET SESSION AUTHORIZATION;
ROLLBACK TO SAVEPOINT task_graph_desk_discovery;
RELEASE SAVEPOINT task_graph_desk_discovery;

SET SESSION AUTHORIZATION "cortex-worker-1";
SET ROLE alpheus_agent_worker;
DO $discovery$
DECLARE item JSONB;
BEGIN
    item:=agent_control.next_cortex_task();
    IF item->>'task_id'<>'tg-probe-fundamental'
       OR item->>'task_graph_id'<>'tg-probe-graph'
       OR item->>'role'<>'fundamental_scout'
       OR item->>'task_graph_tool_id' IS NOT NULL
       OR item->>'task_graph_objective_binding_id' IS NULL THEN
        RAISE EXCEPTION 'TaskGraph Worker discovery assertion failed: %',item;
    END IF;
END
$discovery$;

DO $parallel_claims$
DECLARE
    command JSONB;
    response JSONB;
    task_id_value TEXT;
    command_suffix TEXT;
BEGIN
    FOREACH task_id_value IN ARRAY ARRAY[
        'tg-probe-fundamental','tg-probe-options'
    ] LOOP
        command_suffix:=replace(task_id_value,'tg-probe-','');
        command:=jsonb_build_object(
            'schema_revision',1,
            'envelope',jsonb_build_object(
                'schema_revision',1,
                'command_id','tg-claim-'||command_suffix,
                'actor',jsonb_build_object(
                    'principal_id','cortex-worker-1',
                    'kind','workload','audience','worker'),
                'audience','control_api','command_type','claim_task',
                'idempotency_key','tg-claim-idem-'||command_suffix,
                'request_digest',repeat('a',64),
                'causation_id',task_id_value,
                'correlation_id','tg-probe-run',
                'deadline',to_char(
                    clock_timestamp()+interval '2 minutes',
                    'YYYY-MM-DD"T"HH24:MI:SS.US"Z"')
            ),
            'task_id',task_id_value,
            'expected_task_state_generation',1,
            'requested_lease_seconds',60
        );
        response:=agent_control.claim_task(command::TEXT);
        IF response->>'status'<>'committed' THEN
            RAISE EXCEPTION 'parallel TaskGraph claim failed: %',response;
        END IF;
    END LOOP;

    command:=jsonb_build_object(
        'schema_revision',1,
        'envelope',jsonb_build_object(
            'schema_revision',1,'command_id','tg-claim-market',
            'actor',jsonb_build_object(
                'principal_id','cortex-worker-1',
                'kind','workload','audience','worker'),
            'audience','control_api','command_type','claim_task',
            'idempotency_key','tg-claim-idem-market',
            'request_digest',repeat('b',64),
            'causation_id','tg-probe-market',
            'correlation_id','tg-probe-run',
            'deadline',to_char(
                clock_timestamp()+interval '2 minutes',
                'YYYY-MM-DD"T"HH24:MI:SS.US"Z"')
        ),
        'task_id','tg-probe-market',
        'expected_task_state_generation',1,
        'requested_lease_seconds',60
    );
    BEGIN
        PERFORM agent_control.claim_task(command::TEXT);
        RAISE EXCEPTION 'TaskGraph parallelism limit was not enforced';
    EXCEPTION WHEN object_not_in_prerequisite_state THEN
        NULL;
    END;
END
$parallel_claims$;
RESET ROLE;
RESET SESSION AUTHORIZATION;

DO $slot_release$
DECLARE active_count BIGINT;
BEGIN
    UPDATE agent_control.runtime_task
    SET state='waiting',state_generation=state_generation+1,
        updated_at=clock_timestamp()
    WHERE task_id='tg-probe-fundamental';
    SELECT active_tasks INTO active_count
    FROM agent_control.cortex_task_graph_schedule
    WHERE graph_id='tg-probe-graph';
    IF active_count<>1 THEN
        RAISE EXCEPTION 'TaskGraph slot was not released';
    END IF;
    UPDATE agent_control.runtime_task
    SET state='running',state_generation=state_generation+1,
        updated_at=clock_timestamp()
    WHERE task_id='tg-probe-fundamental';
    SELECT active_tasks INTO active_count
    FROM agent_control.cortex_task_graph_schedule
    WHERE graph_id='tg-probe-graph';
    IF active_count<>2 THEN
        RAISE EXCEPTION 'TaskGraph slot was not reacquired';
    END IF;
END
$slot_release$;

SAVEPOINT task_graph_success_completion;
DO $success_release_sources$
DECLARE
    task_row agent_control.runtime_task%ROWTYPE;
    attempt_row agent_control.runtime_attempt%ROWTYPE;
    at_time TIMESTAMPTZ:=clock_timestamp();
BEGIN
    FOR task_row IN
        SELECT * FROM agent_control.runtime_task
        WHERE task_id IN ('tg-probe-fundamental','tg-probe-options')
        ORDER BY task_id
    LOOP
        SELECT * INTO STRICT attempt_row
        FROM agent_control.runtime_attempt
        WHERE task_id=task_row.task_id AND state='leased';
        UPDATE agent_control.runtime_attempt
        SET state='failed',state_generation=state_generation+1,
            failure=jsonb_build_object(
                'code','probe_branch_closed',
                'message','success-path fixture closes branch',
                'retryable',false),
            updated_at=at_time,terminal_at=at_time
        WHERE attempt_id=attempt_row.attempt_id;
        IF NOT agent_control.runtime_release_active_slot_ancestors(
            task_row.run_id,task_row.budget_ledger_id,at_time
        ) THEN
            RAISE EXCEPTION 'success fixture slot release failed';
        END IF;
        UPDATE agent_control.runtime_task
        SET state='failed',state_generation=state_generation+1,
            budget_slot_held=false,
            failure=jsonb_build_object(
                'code','probe_branch_closed',
                'message','success-path fixture closes branch',
                'retryable',false),
            updated_at=at_time,terminal_at=at_time
        WHERE task_id=task_row.task_id;
        UPDATE agent_control.runtime_session
        SET state='closed',generation=generation+1,closed_at=at_time
        WHERE session_id=task_row.session_id;
    END LOOP;
    UPDATE agent_control.runtime_task
    SET state='dead_lettered',state_generation=state_generation+1,
        failure=jsonb_build_object(
            'code','probe_branch_closed',
            'message','success-path fixture closes branch',
            'retryable',false),
        updated_at=at_time,terminal_at=at_time
    WHERE task_id='tg-probe-market';
    UPDATE agent_control.runtime_session
    SET state='closed',generation=generation+1,closed_at=at_time
    WHERE task_id='tg-probe-market';
    UPDATE agent_control.runtime_task
    SET state='ready',state_generation=state_generation+1,
        updated_at=at_time
    WHERE task_id='tg-probe-desk';
    WITH RECURSIVE ledger_chain AS (
        SELECT ledger.*
        FROM agent_control.runtime_budget_ledger AS ledger
        JOIN agent_control.runtime_task AS task
          ON task.budget_ledger_id=ledger.ledger_id
        WHERE task.task_id='tg-probe-desk'
        UNION ALL
        SELECT parent.*
        FROM agent_control.runtime_budget_ledger AS parent
        JOIN ledger_chain AS child
          ON child.parent_ledger_id=parent.ledger_id
    )
    UPDATE agent_control.runtime_budget_ledger AS ledger
    SET consumed_active_tasks=ledger.consumed_active_tasks+1,
        generation=ledger.generation+1,updated_at=at_time
    FROM ledger_chain AS chain
    WHERE ledger.ledger_id=chain.ledger_id;
    UPDATE agent_control.runtime_task
    SET state='running',state_generation=state_generation+1,
        budget_slot_held=true,updated_at=at_time
    WHERE task_id='tg-probe-desk';
    INSERT INTO agent_control.runtime_attempt(
        attempt_id,schema_revision,run_id,task_id,session_id,ordinal,
        state,state_generation,lease_generation,lease_token,lease_worker,
        lease_claimed_at,lease_heartbeat_at,lease_expires_at,
        created_at,updated_at
    )
    SELECT
        'tg-success-desk-attempt',1,task.run_id,task.task_id,
        task.session_id,1,'leased',1,1,
        '00000000-0000-4000-8000-000000000177',
        jsonb_build_object(
            'principal_id','cortex-worker-1',
            'kind','workload','audience','worker'),
        at_time,at_time,at_time+interval '60 seconds',at_time,at_time
    FROM agent_control.runtime_task AS task
    WHERE task.task_id='tg-probe-desk';
    UPDATE agent_control.runtime_attempt
    SET state='executing',state_generation=2,updated_at=at_time
    WHERE attempt_id='tg-success-desk-attempt';
END
$success_release_sources$;

DO $success_desk_artifact$
DECLARE
    task_row agent_control.runtime_task%ROWTYPE;
    session_row agent_control.runtime_session%ROWTYPE;
    attempt_row agent_control.runtime_attempt%ROWTYPE;
    request_digest CHAR(64):=
        agent_control.runtime_sha256_json(
            '{"probe":"desk-request"}'::JSONB);
    manifest_digest CHAR(64):=
        agent_control.runtime_sha256_json(
            '{"probe":"desk-manifest"}'::JSONB);
    result_digest_value CHAR(64):=
        agent_control.runtime_sha256_json(
            '{"probe":"desk-result"}'::JSONB);
    artifact_digest_value CHAR(64):=
        agent_control.runtime_sha256_json(
            '{"probe":"desk-artifact"}'::JSONB);
    output_ref JSONB;
    at_time TIMESTAMPTZ:=clock_timestamp();
    artifact_time TIMESTAMPTZ;
BEGIN
    artifact_time:=at_time+interval '1 microsecond';
    SELECT * INTO STRICT task_row
    FROM agent_control.runtime_task WHERE task_id='tg-probe-desk';
    SELECT * INTO STRICT session_row
    FROM agent_control.runtime_session WHERE session_id=task_row.session_id;
    SELECT * INTO STRICT attempt_row
    FROM agent_control.runtime_attempt
    WHERE attempt_id='tg-success-desk-attempt';
    INSERT INTO agent_control.runtime_turn(
        turn_id,schema_revision,run_id,task_id,session_id,attempt_id,
        ordinal,kind,state,state_generation,request_digest,
        reservation_held,created_at,updated_at
    ) VALUES(
        'tg-success-desk-turn',1,task_row.run_id,task_row.task_id,
        session_row.session_id,attempt_row.attempt_id,1,'model_call',
        'planned',1,request_digest,false,at_time,at_time
    );
    UPDATE agent_control.runtime_turn
    SET state='dispatched',state_generation=2,reservation_held=true,
        dispatched_at=at_time,updated_at=at_time
    WHERE turn_id='tg-success-desk-turn';
    INSERT INTO agent_control.runtime_model_call_manifest(
        call_id,schema_revision,record_digest,turn_id,attempt_id,
        idempotency_key,provider,model,prompt_digest,context_manifest,
        output_contract_digest,request_digest,max_output_tokens,
        reserved_input_tokens,reserved_external_cost_micro_usd,
        timeout_ms,temperature_micros,created_at
    ) VALUES(
        'tg-success-desk-call',1,manifest_digest,
        'tg-success-desk-turn',attempt_row.attempt_id,
        'tg-success-desk-model-idem','openai','gpt-5-6-sol',
        agent_control.runtime_sha256_json(
            '{"probe":"desk-prompt"}'::JSONB),
        session_row.context_manifest,task_row.output_contract_digest,
        request_digest,1000,1000,0,30000,0,at_time
    );
    output_ref:=jsonb_set(
        session_row.context_manifest,'{origin}',
        jsonb_build_object(
            'owner','agent_control',
            'record_type','model_call_manifest',
            'record_id','tg-success-desk-call',
            'schema_revision',1,
            'record_digest',manifest_digest
        )
    );
    INSERT INTO agent_control.runtime_model_call_result(
        result_id,schema_revision,record_digest,call_id,attempt_id,
        turn_id,idempotency_key,request_digest,provider_request_id,
        output_origin_owner,output_origin_record_type,
        output_origin_record_id,output_origin_schema_revision,
        output_origin_record_digest,output,input_tokens,output_tokens,
        external_cost_micro_usd,wall_time_ms,finish_reason,committed_at
    ) VALUES(
        'tg-success-desk-result',1,result_digest_value,
        'tg-success-desk-call',attempt_row.attempt_id,
        'tg-success-desk-turn','tg-success-desk-model-idem',
        request_digest,'tg-success-provider-request',
        'agent_control','model_call_manifest','tg-success-desk-call',
        1,manifest_digest,output_ref,100,100,0,1000,'stop',at_time
    );
    UPDATE agent_control.runtime_turn
    SET state='result_committed',state_generation=3,
        result_owner='agent_control',
        result_record_type='model_call_result',
        result_id='tg-success-desk-result',
        result_schema_revision=1,result_digest=result_digest_value,
        reservation_held=false,finished_at=at_time,updated_at=at_time
    WHERE turn_id='tg-success-desk-turn';
    INSERT INTO agent_control.runtime_artifact(
        artifact_id,schema_revision,record_digest,run_id,task_id,
        session_id,attempt_id,source_result_owner,
        source_result_record_type,source_result_id,
        source_result_schema_revision,source_result_digest,
        artifact_type,output_contract_digest,effect_class,created_at
    ) VALUES(
        'tg-success-desk-artifact',1,artifact_digest_value,task_row.run_id,
        task_row.task_id,session_row.session_id,attempt_row.attempt_id,
        'agent_control','model_call_result','tg-success-desk-result',
        1,result_digest_value,'assistant_response',
        task_row.output_contract_digest,'none',artifact_time
    );
    INSERT INTO agent_control.runtime_artifact_section(
        artifact_id,ordinal,name,required,content
    ) VALUES(
        'tg-success-desk-artifact',1,'response',true,output_ref
    );
    UPDATE agent_control.runtime_attempt
    SET state='result_committed',state_generation=state_generation+1,
        result_artifact_owner='agent_control',
        result_artifact_record_type='artifact',
        result_artifact_id='tg-success-desk-artifact',
        result_artifact_schema_revision=1,
        result_artifact_digest=artifact_digest_value,
        updated_at=artifact_time,terminal_at=artifact_time
    WHERE attempt_id=attempt_row.attempt_id;
    IF NOT agent_control.runtime_release_active_slot_ancestors(
        task_row.run_id,task_row.budget_ledger_id,artifact_time
    ) THEN
        RAISE EXCEPTION 'Desk active slot release failed';
    END IF;
    UPDATE agent_control.runtime_task
    SET state='result_committed',state_generation=state_generation+1,
        result_artifact_id='tg-success-desk-artifact',
        updated_at=artifact_time
    WHERE task_id=task_row.task_id;
    UPDATE agent_control.runtime_task
    SET state='succeeded',state_generation=state_generation+1,
        budget_slot_held=false,updated_at=artifact_time,
        terminal_at=artifact_time
    WHERE task_id=task_row.task_id;
    UPDATE agent_control.runtime_session
    SET state='closed',generation=generation+1,closed_at=artifact_time
    WHERE session_id=session_row.session_id;
END
$success_desk_artifact$;

SET SESSION AUTHORIZATION "cortex-control-1";
SET ROLE alpheus_agent_control_api;
DO $success_completion$
DECLARE
    reconciled JSONB;
    result JSONB;
BEGIN
    reconciled:=agent_control.reconcile_cortex_task_graph_joins(
        'cortex-worker-1');
    result:=agent_control.get_cortex_run_result('tg-probe-run');
    IF (reconciled->>'completed_graphs')::BIGINT<>1
       OR result->>'state'<>'succeeded'
       OR result #>> '{owning_reference,record_id}'
            <>'tg-success-desk-artifact' THEN
        RAISE EXCEPTION
          'TaskGraph success completion failed: % result=%',
          reconciled,result;
    END IF;
END
$success_completion$;
RESET ROLE;
RESET SESSION AUTHORIZATION;
SET CONSTRAINTS ALL IMMEDIATE;
DO $success_assertions$
DECLARE
    parent_state TEXT;
    run_state TEXT;
    result_count BIGINT;
    active_count BIGINT;
    parent_session_state TEXT;
BEGIN
    SELECT state INTO parent_state FROM agent_control.runtime_task
    WHERE task_id='tg-probe-root';
    SELECT state INTO run_state FROM agent_control.runtime_run
    WHERE run_id='tg-probe-run';
    SELECT count(*) INTO result_count
    FROM agent_control.cortex_task_graph_result
    WHERE graph_id='tg-probe-graph'
      AND artifact_id='tg-success-desk-artifact';
    SELECT active_tasks INTO active_count
    FROM agent_control.cortex_task_graph_schedule
    WHERE graph_id='tg-probe-graph' AND state='closed';
    SELECT state INTO parent_session_state
    FROM agent_control.runtime_session
    WHERE task_id='tg-probe-root';
    IF parent_state<>'superseded' OR run_state<>'succeeded'
       OR result_count<>1 OR active_count<>0
       OR parent_session_state<>'closed' THEN
        RAISE EXCEPTION 'TaskGraph result promotion assertion failed';
    END IF;
END
$success_assertions$;
SET CONSTRAINTS ALL DEFERRED;
ROLLBACK TO SAVEPOINT task_graph_success_completion;
RELEASE SAVEPOINT task_graph_success_completion;

DO $terminal_failures$
DECLARE
    task_row agent_control.runtime_task%ROWTYPE;
    attempt_row agent_control.runtime_attempt%ROWTYPE;
    session_row agent_control.runtime_session%ROWTYPE;
    at_time TIMESTAMPTZ:=clock_timestamp();
BEGIN
    FOR task_row IN
        SELECT task.*
        FROM agent_control.runtime_task AS task
        WHERE task.task_id IN (
            'tg-probe-fundamental','tg-probe-options'
        )
        ORDER BY task.task_id
    LOOP
        SELECT * INTO STRICT attempt_row
        FROM agent_control.runtime_attempt
        WHERE task_id=task_row.task_id AND state='leased';
        SELECT * INTO STRICT session_row
        FROM agent_control.runtime_session
        WHERE session_id=task_row.session_id;
        UPDATE agent_control.runtime_attempt
        SET state='failed',state_generation=state_generation+1,
            failure=jsonb_build_object(
                'code','probe_failed','message','rollback probe failure',
                'retryable',false),
            updated_at=at_time,terminal_at=at_time
        WHERE attempt_id=attempt_row.attempt_id;
        UPDATE agent_control.runtime_task
        SET state='failed',state_generation=state_generation+1,
            failure=jsonb_build_object(
                'code','probe_failed','message','rollback probe failure',
                'retryable',false),
            budget_slot_held=false,updated_at=at_time,terminal_at=at_time
        WHERE task_id=task_row.task_id;
        UPDATE agent_control.runtime_session
        SET state='closed',generation=generation+1,closed_at=at_time
        WHERE session_id=session_row.session_id;
        IF NOT agent_control.runtime_release_active_slot_ancestors(
            task_row.run_id,task_row.budget_ledger_id,at_time
        ) THEN
            RAISE EXCEPTION 'probe active slot release failed';
        END IF;
    END LOOP;

    SELECT * INTO STRICT task_row
    FROM agent_control.runtime_task
    WHERE task_id='tg-probe-market';
    UPDATE agent_control.runtime_task
    SET state='dead_lettered',state_generation=state_generation+1,
        failure=jsonb_build_object(
            'code','probe_dead_lettered',
            'message','rollback probe dead letter',
            'retryable',false),
        updated_at=at_time,terminal_at=at_time
    WHERE task_id=task_row.task_id;
    UPDATE agent_control.runtime_session
    SET state='closed',generation=generation+1,closed_at=at_time
    WHERE session_id=task_row.session_id;
END
$terminal_failures$;

SET SESSION AUTHORIZATION "cortex-control-1";
SET ROLE alpheus_agent_control_api;
DO $join_failure$
DECLARE result JSONB;
BEGIN
    result:=agent_control.reconcile_cortex_task_graph_joins(
        'cortex-worker-1');
    IF result->>'status'<>'reconciled'
       OR (result->>'resolved_joins')::BIGINT<>1
       OR (result->>'failed_joins')::BIGINT<>1
       OR (result->>'ready_joins')::BIGINT<>0 THEN
        RAISE EXCEPTION 'TaskGraph failed Join assertion failed: %',result;
    END IF;
END
$join_failure$;
RESET ROLE;
RESET SESSION AUTHORIZATION;

DO $assertions$
DECLARE
    graph_count INTEGER;
    node_count INTEGER;
    ready_count INTEGER;
    running_count INTEGER;
    blocked_count INTEGER;
    failed_task_count INTEGER;
    join_count INTEGER;
    admission_count INTEGER;
    session_count INTEGER;
    closed_session_count INTEGER;
    binding_count INTEGER;
    worker_acl_count INTEGER;
    schedule_active INTEGER;
    schedule_limit INTEGER;
    schedule_state TEXT;
    resolution_outcome TEXT;
    resolution_inputs INTEGER;
    parent_state TEXT;
    attempt_state TEXT;
    run_state TEXT;
    parent_session_state TEXT;
BEGIN
    SELECT count(*) INTO graph_count FROM agent_control.cortex_task_graph
    WHERE graph_id='tg-probe-graph';
    SELECT count(*),count(*) FILTER(WHERE task.state='ready'),
        count(*) FILTER(WHERE task.state='running'),
        count(*) FILTER(WHERE task.state='blocked'),
        count(*) FILTER(WHERE task.state IN (
            'failed','dead_lettered'
        ))
    INTO node_count,ready_count,running_count,blocked_count,
        failed_task_count
    FROM agent_control.cortex_task_graph_node node
    JOIN agent_control.runtime_task task ON task.task_id=node.task_id
    WHERE node.graph_id='tg-probe-graph';
    SELECT count(*) INTO join_count FROM agent_control.cortex_task_graph_join
    WHERE graph_id='tg-probe-graph';
    SELECT count(*) INTO admission_count
    FROM agent_control.cortex_task_graph_admission
    WHERE graph_id='tg-probe-graph';
    SELECT count(*),count(*) FILTER(WHERE session.state='closed')
    INTO session_count,closed_session_count
    FROM agent_control.cortex_task_graph_node AS node
    JOIN agent_control.runtime_task AS task ON task.task_id=node.task_id
    JOIN agent_control.runtime_session AS session
      ON session.session_id=task.session_id
     AND session.task_id=task.task_id
    WHERE node.graph_id='tg-probe-graph';
    SELECT count(*) INTO binding_count
    FROM blob.blob_reference AS binding
    JOIN agent_control.runtime_session AS session
      ON binding.binding_id LIKE
         'cortex-session:'||session.session_id||':%'
    JOIN agent_control.cortex_task_graph_node AS node
      ON node.task_id=session.task_id
    WHERE node.graph_id='tg-probe-graph'
      AND binding.state='active';
    SELECT count(*) INTO worker_acl_count
    FROM blob.blob_acl AS acl
    JOIN agent_control.runtime_session AS session
      ON acl.binding_id LIKE
         'cortex-session:'||session.session_id||':%'
    JOIN agent_control.cortex_task_graph_node AS node
      ON node.task_id=session.task_id
    WHERE node.graph_id='tg-probe-graph'
      AND acl.principal_id='cortex-worker-1'
      AND acl.state='active';
    SELECT active_tasks,limit_parallelism,state
    INTO schedule_active,schedule_limit,schedule_state
    FROM agent_control.cortex_task_graph_schedule
    WHERE graph_id='tg-probe-graph';
    SELECT state INTO parent_state FROM agent_control.runtime_task
    WHERE task_id='tg-probe-root';
    SELECT state INTO attempt_state FROM agent_control.runtime_attempt
    WHERE attempt_id='tg-probe-attempt';
    SELECT state INTO run_state FROM agent_control.runtime_run
    WHERE run_id='tg-probe-run';
    SELECT state INTO parent_session_state
    FROM agent_control.runtime_session
    WHERE task_id='tg-probe-root';
    SELECT outcome,jsonb_array_length(inputs)
    INTO resolution_outcome,resolution_inputs
    FROM agent_control.cortex_task_graph_join_resolution
    WHERE graph_id='tg-probe-graph' AND join_id='tg-probe-join';
    IF graph_count<>1 OR node_count<>4 OR ready_count<>0
       OR running_count<>0 OR blocked_count<>0
       OR failed_task_count<>4
       OR join_count<>1 OR admission_count<>1
       OR session_count<>4 OR closed_session_count<>4
       OR binding_count<>16 OR worker_acl_count<>16
       OR schedule_active<>0 OR schedule_limit<>2
       OR schedule_state<>'closed'
       OR resolution_outcome<>'failed' OR resolution_inputs<>0
       OR parent_state<>'dead_lettered'
       OR attempt_state<>'superseded'
       OR run_state<>'dead_lettered'
       OR parent_session_state<>'closed'
       OR EXISTS (
           SELECT 1 FROM agent_control.cortex_task_graph
           WHERE graph_id='tg-invalid-graph'
       ) THEN
        RAISE EXCEPTION 'TaskGraph atomic admission assertion failed';
    END IF;
END
$assertions$;

SELECT 'task-graph-admission-pass' AS result;
ROLLBACK;
