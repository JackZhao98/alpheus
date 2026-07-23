-- Control-only, all-or-nothing TaskGraph v1 admission. Model output is only
-- lineage: every authority, role, Tool grant, budget and graph invariant is
-- independently revalidated against current immutable database records.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

CREATE FUNCTION agent_control.cortex_task_graph_plan_valid(p_plan JSONB)
RETURNS BOOLEAN
LANGUAGE plpgsql
STABLE
STRICT
SET search_path=pg_catalog,agent_control
SET timezone='UTC'
AS $$
DECLARE
    created_value TIMESTAMPTZ;
    deadline_value TIMESTAMPTZ;
    node_count INTEGER;
    edge_count INTEGER;
    join_count INTEGER;
    computed_count INTEGER;
BEGIN
    IF jsonb_typeof(p_plan)<>'object'
       OR NOT (p_plan ?& ARRAY[
           'schema_revision','graph_id','run_id','parent_task_id','source_result',
           'runtime_policy','round','max_rounds','authorized_limit','nodes','edges',
           'joins','created_by','created_at','deadline_at'
       ])
       OR p_plan-ARRAY[
           'schema_revision','graph_id','run_id','parent_task_id','source_result',
           'runtime_policy','round','max_rounds','authorized_limit','nodes','edges',
           'joins','created_by','created_at','deadline_at'
       ]<>'{}'::JSONB
       OR p_plan->>'schema_revision'<>'1'
       OR jsonb_typeof(p_plan->'graph_id')<>'string'
       OR NOT agent_control.runtime_identifier_valid(p_plan->>'graph_id')
       OR jsonb_typeof(p_plan->'run_id')<>'string'
       OR NOT agent_control.runtime_identifier_valid(p_plan->>'run_id')
       OR jsonb_typeof(p_plan->'parent_task_id')<>'string'
       OR NOT agent_control.runtime_identifier_valid(p_plan->>'parent_task_id')
       OR NOT agent_control.runtime_record_ref_valid(
           p_plan->'source_result','agent_control','model_call_result')
       OR NOT agent_control.runtime_child_revision_ref_valid(
           p_plan->'runtime_policy','agent_control','runtime_policy')
       OR NOT agent_control.runtime_positive_bigint_json(p_plan->'round')
       OR NOT agent_control.runtime_positive_bigint_json(p_plan->'max_rounds')
       OR (p_plan->>'round')::BIGINT>(p_plan->>'max_rounds')::BIGINT
       OR (p_plan->>'max_rounds')::BIGINT>8
       OR NOT agent_control.runtime_child_budget_limit_valid(p_plan->'authorized_limit')
       OR (p_plan#>>'{authorized_limit,max_tasks}')::BIGINT NOT BETWEEN 1 AND 64
       OR (p_plan#>>'{authorized_limit,max_depth}')::BIGINT NOT BETWEEN 1 AND 8
       OR (p_plan#>>'{authorized_limit,max_parallelism}')::BIGINT NOT BETWEEN 1 AND 16
       OR jsonb_typeof(p_plan->'nodes')<>'array'
       OR jsonb_typeof(p_plan->'edges')<>'array'
       OR jsonb_typeof(p_plan->'joins')<>'array'
       OR jsonb_array_length(p_plan->'nodes') NOT BETWEEN 1 AND 64
       OR jsonb_array_length(p_plan->'edges')>256
       OR jsonb_array_length(p_plan->'joins')>64
       OR NOT agent_control.runtime_actor_valid(p_plan->'created_by')
       OR p_plan#>>'{created_by,kind}'<>'workload'
       OR p_plan#>>'{created_by,audience}'<>'control_api'
       OR NOT agent_control.runtime_utc_instant_json(p_plan->'created_at')
       OR NOT agent_control.runtime_utc_instant_json(p_plan->'deadline_at') THEN
        RETURN false;
    END IF;
    created_value:=(p_plan->>'created_at')::TIMESTAMPTZ;
    deadline_value:=(p_plan->>'deadline_at')::TIMESTAMPTZ;
    IF created_value>=deadline_value OR created_value>clock_timestamp() THEN
        RETURN false;
    END IF;
    node_count:=jsonb_array_length(p_plan->'nodes');
    edge_count:=jsonb_array_length(p_plan->'edges');
    join_count:=jsonb_array_length(p_plan->'joins');
    IF node_count>(p_plan#>>'{authorized_limit,max_tasks}')::BIGINT THEN
        RETURN false;
    END IF;

    -- Every Node is exact, bounded, role-bound and references one immutable
    -- output contract. A node cannot recursively create unplanned descendants.
    IF EXISTS (
        SELECT 1
        FROM jsonb_array_elements(p_plan->'nodes') WITH ORDINALITY AS item(node,ordinal)
        WHERE jsonb_typeof(node)<>'object'
           OR NOT (node ?& ARRAY[
               'task_id','role_id','role_revision','depth','objective','input_refs',
               'output_contract_name','output_contract','tool_grants','limit','deadline_at'
           ])
           OR node-ARRAY[
               'task_id','role_id','role_revision','depth','objective','input_refs',
               'output_contract_name','output_contract','tool_grants','limit','deadline_at'
           ]<>'{}'::JSONB
           OR jsonb_typeof(node->'task_id')<>'string'
           OR NOT agent_control.runtime_identifier_valid(node->>'task_id')
           OR jsonb_typeof(node->'role_id')<>'string'
           OR NOT agent_control.runtime_name_valid(node->>'role_id')
           OR node->>'role_revision'<>'1'
           OR NOT agent_control.runtime_positive_bigint_json(node->'depth')
           OR (node->>'depth')::BIGINT>8
           OR NOT agent_control.runtime_blob_ref_valid(node->'objective','task_objective','')
           OR (node#>>'{objective,committed_at}')::TIMESTAMPTZ>created_value
           OR NOT agent_control.runtime_child_input_refs_valid(node->'input_refs')
           OR jsonb_typeof(node->'output_contract_name')<>'string'
           OR NOT agent_control.runtime_name_valid(node->>'output_contract_name')
           OR NOT agent_control.runtime_child_revision_ref_valid(
               node->'output_contract','agent_control','output_contract_revision')
           OR jsonb_typeof(node->'tool_grants')<>'array'
           OR jsonb_array_length(node->'tool_grants')>1
           OR NOT agent_control.runtime_child_budget_limit_valid(node->'limit')
           OR (node#>>'{limit,max_model_calls}')::BIGINT<1
           OR (node#>>'{limit,max_tasks}')::BIGINT<>1
           OR (node#>>'{limit,max_depth}')::BIGINT<>0
           OR (node#>>'{limit,max_fanout}')::BIGINT<>0
           OR (node#>>'{limit,max_parallelism}')::BIGINT<>1
           OR (node#>>'{limit,max_tool_calls}')::BIGINT<jsonb_array_length(node->'tool_grants')
           OR NOT agent_control.runtime_utc_instant_json(node->'deadline_at')
           OR (node->>'deadline_at')::TIMESTAMPTZ<=created_value
           OR (node->>'deadline_at')::TIMESTAMPTZ>deadline_value
           OR (
               node->>'role_id'='decision_desk'
               AND (
                   node->>'output_contract_name'<>'answer_v1'
                   OR jsonb_array_length(node->'tool_grants')<>0
                   OR (node#>>'{limit,max_tool_calls}')::BIGINT<>0
               )
           )
           OR (
               node->>'role_id'<>'decision_desk'
               AND (
                   node->>'output_contract_name'<>'specialist_memo_v1'
                   OR NOT EXISTS (
                       SELECT 1 FROM agent_control.cortex_agent_role_registry role
                       WHERE role.role_id=node->>'role_id'
                         AND role.revision=(node->>'role_revision')::INTEGER
                         AND role.output_contract=node->>'output_contract_name'
                         AND role.active
                         AND jsonb_array_length(node->'tool_grants')<=role.max_tool_calls
                         AND (node#>>'{limit,max_tool_calls}')::BIGINT<=role.max_tool_calls
                   )
               )
           )
           OR NOT EXISTS (
               SELECT 1 FROM agent_control.output_contract_revision contract
               WHERE contract.revision_id=node#>>'{output_contract,record_id}'
                 AND contract.generation=(node#>>'{output_contract,generation}')::BIGINT
                 AND contract.record_digest::TEXT=node#>>'{output_contract,record_digest}'
                 AND contract.effect_class='none'
           )
           OR NOT EXISTS (
               SELECT 1 FROM blob.blob_object object
               WHERE object.blob_id=(node#>>'{objective,blob_id}')::UUID
                 AND object.state='committed'
                 AND object.content_digest::TEXT=node#>>'{objective,content_digest}'
                 AND object.origin_owner='agent_control'
                 AND object.origin_record_type='task_objective'
                 AND object.origin_record_id=node#>>'{objective,origin,record_id}'
                 AND object.origin_record_digest::TEXT=node#>>'{objective,origin,record_digest}'
           )
    ) THEN
        RETURN false;
    END IF;
    IF EXISTS (
        SELECT node->>'task_id'
        FROM jsonb_array_elements(p_plan->'nodes') AS item(node)
        GROUP BY node->>'task_id' HAVING count(*)>1
    ) THEN
        RETURN false;
    END IF;

    -- Tool snapshots must be exact v1 read-only grants already installed for
    -- that Specialist. Naming a catalog Tool never creates permission.
    IF EXISTS (
        SELECT 1
        FROM jsonb_array_elements(p_plan->'nodes') AS nodes(node)
        CROSS JOIN LATERAL jsonb_array_elements(node->'tool_grants') AS grants(grant_value)
        WHERE jsonb_typeof(grant_value)<>'object'
           OR NOT (grant_value ?& ARRAY['tool_id','tool_revision','effect'])
           OR grant_value-ARRAY['tool_id','tool_revision','effect']<>'{}'::JSONB
           OR jsonb_typeof(grant_value->'tool_id')<>'string'
           OR NOT agent_control.runtime_name_valid(grant_value->>'tool_id')
           OR grant_value->>'tool_revision'<>'1'
           OR grant_value->>'effect'<>'read_only'
           OR NOT EXISTS (
               SELECT 1 FROM agent_control.cortex_specialist_tool_grant installed
               WHERE installed.role_id=node->>'role_id'
                 AND installed.tool_id=grant_value->>'tool_id'
                 AND installed.effect=grant_value->>'effect'
           )
    ) OR EXISTS (
        SELECT node->>'task_id',grant_value->>'tool_id'
        FROM jsonb_array_elements(p_plan->'nodes') AS nodes(node)
        CROSS JOIN LATERAL jsonb_array_elements(node->'tool_grants') AS grants(grant_value)
        GROUP BY node->>'task_id',grant_value->>'tool_id' HAVING count(*)>1
    ) THEN
        RETURN false;
    END IF;

    -- Aggregate maxima are checked as NUMERIC to avoid BIGINT addition
    -- overflow. They must fit the exact Control-authorized graph limit.
    IF EXISTS (
        SELECT 1 FROM (
            SELECT
                sum((node#>>'{limit,max_model_calls}')::NUMERIC) model_calls,
                sum((node#>>'{limit,max_input_tokens}')::NUMERIC) input_tokens,
                sum((node#>>'{limit,max_output_tokens}')::NUMERIC) output_tokens,
                sum((node#>>'{limit,max_tool_calls}')::NUMERIC) tool_calls,
                sum((node#>>'{limit,max_external_cost_micro_usd}')::NUMERIC) external_cost,
                sum((node#>>'{limit,max_wall_time_ms}')::NUMERIC) wall_time,
                sum((node#>>'{limit,max_idle_time_ms}')::NUMERIC) idle_time,
                sum((node#>>'{limit,max_tasks}')::NUMERIC) tasks,
                sum((node#>>'{limit,max_invalid_output_retries}')::NUMERIC) invalid_retries,
                sum((node#>>'{limit,max_infrastructure_retries}')::NUMERIC) infrastructure_retries
            FROM jsonb_array_elements(p_plan->'nodes') AS item(node)
        ) total
        WHERE total.model_calls>(p_plan#>>'{authorized_limit,max_model_calls}')::NUMERIC
           OR total.input_tokens>(p_plan#>>'{authorized_limit,max_input_tokens}')::NUMERIC
           OR total.output_tokens>(p_plan#>>'{authorized_limit,max_output_tokens}')::NUMERIC
           OR total.tool_calls>(p_plan#>>'{authorized_limit,max_tool_calls}')::NUMERIC
           OR total.external_cost>(p_plan#>>'{authorized_limit,max_external_cost_micro_usd}')::NUMERIC
           OR total.wall_time>(p_plan#>>'{authorized_limit,max_wall_time_ms}')::NUMERIC
           OR total.idle_time>(p_plan#>>'{authorized_limit,max_idle_time_ms}')::NUMERIC
           OR total.tasks>(p_plan#>>'{authorized_limit,max_tasks}')::NUMERIC
           OR total.invalid_retries>(p_plan#>>'{authorized_limit,max_invalid_output_retries}')::NUMERIC
           OR total.infrastructure_retries>(p_plan#>>'{authorized_limit,max_infrastructure_retries}')::NUMERIC
    ) THEN
        RETURN false;
    END IF;

    IF EXISTS (
        SELECT 1 FROM jsonb_array_elements(p_plan->'edges') WITH ORDINALITY AS item(edge,ordinal)
        WHERE jsonb_typeof(edge)<>'object'
           OR NOT (edge ?& ARRAY['from_task_id','to_task_id'])
           OR edge-ARRAY['from_task_id','to_task_id']<>'{}'::JSONB
           OR jsonb_typeof(edge->'from_task_id')<>'string'
           OR jsonb_typeof(edge->'to_task_id')<>'string'
           OR NOT agent_control.runtime_identifier_valid(edge->>'from_task_id')
           OR NOT agent_control.runtime_identifier_valid(edge->>'to_task_id')
           OR edge->>'from_task_id'=edge->>'to_task_id'
           OR NOT EXISTS (
               SELECT 1 FROM jsonb_array_elements(p_plan->'nodes') node(value)
               WHERE value->>'task_id'=edge->>'from_task_id')
           OR NOT EXISTS (
               SELECT 1 FROM jsonb_array_elements(p_plan->'nodes') node(value)
               WHERE value->>'task_id'=edge->>'to_task_id')
    ) OR EXISTS (
        SELECT edge->>'from_task_id',edge->>'to_task_id'
        FROM jsonb_array_elements(p_plan->'edges') item(edge)
        GROUP BY edge->>'from_task_id',edge->>'to_task_id' HAVING count(*)>1
    ) THEN
        RETURN false;
    END IF;

    -- Transitive closure is bounded by the 64-node set and UNION de-duplicates
    -- paths. Any self reachability is a cycle.
    IF EXISTS (
        WITH RECURSIVE edge AS (
            SELECT value->>'from_task_id' source,value->>'to_task_id' target
            FROM jsonb_array_elements(p_plan->'edges') item(value)
        ), reach(source,target) AS (
            SELECT source,target FROM edge
            UNION
            SELECT reach.source,edge.target FROM reach JOIN edge ON edge.source=reach.target
        )
        SELECT 1 FROM reach WHERE source=target
    ) THEN
        RETURN false;
    END IF;

    -- Declared depth is exact longest-path depth (sources are depth 1).
    SELECT count(*) INTO computed_count FROM (
        WITH RECURSIVE edge AS (
            SELECT value->>'from_task_id' source,value->>'to_task_id' target
            FROM jsonb_array_elements(p_plan->'edges') item(value)
        ), source AS (
            SELECT node->>'task_id' task_id
            FROM jsonb_array_elements(p_plan->'nodes') item(node)
            WHERE NOT EXISTS (SELECT 1 FROM edge WHERE target=node->>'task_id')
        ), path(task_id,depth) AS (
            SELECT task_id,1::BIGINT FROM source
            UNION
            SELECT edge.target,path.depth+1 FROM path JOIN edge ON edge.source=path.task_id
        ), exact AS (
            SELECT task_id,max(depth) depth FROM path GROUP BY task_id
        )
        SELECT 1
        FROM jsonb_array_elements(p_plan->'nodes') item(node)
        JOIN exact ON exact.task_id=node->>'task_id'
        WHERE exact.depth=(node->>'depth')::BIGINT
          AND exact.depth<=(p_plan#>>'{authorized_limit,max_depth}')::BIGINT
    ) valid_depths;
    IF computed_count<>node_count THEN
        RETURN false;
    END IF;
    IF EXISTS (
        SELECT 1 FROM (
            SELECT edge->>'from_task_id',count(*) outgoing
            FROM jsonb_array_elements(p_plan->'edges') item(edge)
            GROUP BY edge->>'from_task_id'
        ) fanout
        WHERE fanout.outgoing>(p_plan#>>'{authorized_limit,max_fanout}')::BIGINT
    ) THEN
        RETURN false;
    END IF;

    IF EXISTS (
        SELECT 1 FROM jsonb_array_elements(p_plan->'joins') WITH ORDINALITY AS item(join_value,ordinal)
        WHERE jsonb_typeof(join_value)<>'object'
           OR NOT (join_value ?& ARRAY[
               'join_id','downstream_task_id','upstream_task_ids','policy',
               'minimum_success','failure_policy','deadline_at'
           ])
           OR join_value-ARRAY[
               'join_id','downstream_task_id','upstream_task_ids','policy',
               'minimum_success','failure_policy','deadline_at'
           ]<>'{}'::JSONB
           OR jsonb_typeof(join_value->'join_id')<>'string'
           OR NOT agent_control.runtime_identifier_valid(join_value->>'join_id')
           OR jsonb_typeof(join_value->'downstream_task_id')<>'string'
           OR NOT agent_control.runtime_identifier_valid(join_value->>'downstream_task_id')
           OR jsonb_typeof(join_value->'upstream_task_ids')<>'array'
           OR jsonb_array_length(join_value->'upstream_task_ids') NOT BETWEEN 2 AND 64
           OR join_value->>'policy' NOT IN ('all_required','minimum_succeeded')
           OR NOT agent_control.runtime_positive_bigint_json(join_value->'minimum_success')
           OR join_value->>'failure_policy' NOT IN ('fail_graph','continue_if_threshold_met')
           OR NOT agent_control.runtime_utc_instant_json(join_value->'deadline_at')
           OR (join_value->>'deadline_at')::TIMESTAMPTZ<=created_value
           OR (join_value->>'deadline_at')::TIMESTAMPTZ>deadline_value
           OR (join_value->>'deadline_at')::TIMESTAMPTZ>(
               SELECT (node->>'deadline_at')::TIMESTAMPTZ
               FROM jsonb_array_elements(p_plan->'nodes') item(node)
               WHERE node->>'task_id'=join_value->>'downstream_task_id')
           OR (
               join_value->>'policy'='all_required'
               AND (
                   (join_value->>'minimum_success')::BIGINT<>jsonb_array_length(join_value->'upstream_task_ids')
                   OR join_value->>'failure_policy'<>'fail_graph'
               )
           )
           OR (
               join_value->>'policy'='minimum_succeeded'
               AND (
                   (join_value->>'minimum_success')::BIGINT>=jsonb_array_length(join_value->'upstream_task_ids')
                   OR join_value->>'failure_policy'<>'continue_if_threshold_met'
               )
           )
           OR EXISTS (
               SELECT upstream.value
               FROM jsonb_array_elements_text(join_value->'upstream_task_ids') upstream(value)
               GROUP BY upstream.value HAVING count(*)>1
           )
           OR EXISTS (
               SELECT upstream.value
               FROM jsonb_array_elements_text(join_value->'upstream_task_ids') upstream(value)
               WHERE upstream.value=join_value->>'downstream_task_id'
                  OR NOT EXISTS (
                      SELECT 1 FROM jsonb_array_elements(p_plan->'edges') edge(value)
                      WHERE value->>'from_task_id'=upstream.value
                        AND value->>'to_task_id'=join_value->>'downstream_task_id'
                  )
           )
           OR (
               SELECT count(*) FROM jsonb_array_elements(p_plan->'edges') edge(value)
               WHERE value->>'to_task_id'=join_value->>'downstream_task_id'
           )<>jsonb_array_length(join_value->'upstream_task_ids')
    ) OR EXISTS (
        SELECT join_value->>'join_id'
        FROM jsonb_array_elements(p_plan->'joins') item(join_value)
        GROUP BY join_value->>'join_id' HAVING count(*)>1
    ) OR EXISTS (
        SELECT join_value->>'downstream_task_id'
        FROM jsonb_array_elements(p_plan->'joins') item(join_value)
        GROUP BY join_value->>'downstream_task_id' HAVING count(*)>1
    ) THEN
        RETURN false;
    END IF;

    -- Every node with two or more incoming dependencies has exactly one Join;
    -- nodes with zero or one incoming edge must not invent a Join.
    IF EXISTS (
        SELECT node->>'task_id'
        FROM jsonb_array_elements(p_plan->'nodes') item(node)
        WHERE (
            SELECT count(*) FROM jsonb_array_elements(p_plan->'edges') edge(value)
            WHERE value->>'to_task_id'=node->>'task_id'
        )>1
        AND NOT EXISTS (
            SELECT 1 FROM jsonb_array_elements(p_plan->'joins') join_item(value)
            WHERE value->>'downstream_task_id'=node->>'task_id'
        )
    ) OR EXISTS (
        SELECT join_value->>'downstream_task_id'
        FROM jsonb_array_elements(p_plan->'joins') item(join_value)
        WHERE (
            SELECT count(*) FROM jsonb_array_elements(p_plan->'edges') edge(value)
            WHERE value->>'to_task_id'=join_value->>'downstream_task_id'
        )<2
    ) THEN
        RETURN false;
    END IF;
    RETURN true;
EXCEPTION WHEN OTHERS THEN
    RETURN false;
END
$$;

CREATE TABLE agent_control.cortex_task_graph_admission (
    command_id TEXT PRIMARY KEY CHECK (agent_control.runtime_identifier_valid(command_id)),
    idempotency_key TEXT NOT NULL UNIQUE CHECK (
        agent_control.runtime_identifier_valid(idempotency_key)
    ),
    body_fingerprint CHAR(64) NOT NULL CHECK (
        agent_control.runtime_digest_valid(body_fingerprint::TEXT)
    ),
    graph_id TEXT NOT NULL UNIQUE REFERENCES agent_control.cortex_task_graph(graph_id)
        DEFERRABLE INITIALLY DEFERRED,
    run_id TEXT NOT NULL CHECK (agent_control.runtime_identifier_valid(run_id)),
    parent_task_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(parent_task_id)
    ),
    response JSONB NOT NULL CHECK (jsonb_typeof(response)='object'),
    created_at TIMESTAMPTZ NOT NULL,
    FOREIGN KEY(graph_id,run_id)
        REFERENCES agent_control.cortex_task_graph(graph_id,run_id)
        DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY(parent_task_id,run_id)
        REFERENCES agent_control.runtime_task(task_id,run_id)
        DEFERRABLE INITIALLY DEFERRED
);
CREATE TRIGGER cortex_task_graph_admission_immutable
BEFORE UPDATE OR DELETE ON agent_control.cortex_task_graph_admission
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();

CREATE FUNCTION agent_control.admit_cortex_task_graph(p_command JSONB)
RETURNS JSONB
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security,blob
SET timezone='UTC'
AS $$
DECLARE
    invoker RECORD;
    envelope JSONB;
    plan JSONB;
    existing agent_control.cortex_task_graph_admission%ROWTYPE;
    run_row agent_control.runtime_run%ROWTYPE;
    parent_task agent_control.runtime_task%ROWTYPE;
    parent_attempt agent_control.runtime_attempt%ROWTYPE;
    parent_session agent_control.runtime_session%ROWTYPE;
    run_ledger agent_control.runtime_budget_ledger%ROWTYPE;
    parent_ledger agent_control.runtime_budget_ledger%ROWTYPE;
    node JSONB;
    edge JSONB;
    join_value JSONB;
    grant_value JSONB;
    input_ref JSONB;
    node_ordinal BIGINT;
    edge_ordinal BIGINT;
    join_ordinal BIGINT;
    grant_ordinal BIGINT;
    input_ordinal BIGINT;
    task_ledger_id TEXT;
    task_state TEXT;
    node_count BIGINT;
    plan_digest CHAR(64);
    fingerprint CHAR(64);
    response JSONB;
    task_ids JSONB:='[]'::JSONB;
    at_time TIMESTAMPTZ:=clock_timestamp();
    worker_principal TEXT;
    source_result_committed_at TIMESTAMPTZ;
BEGIN
    SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
    IF invoker.group_role::TEXT<>'alpheus_agent_control_api'
       OR invoker.profile_id<>'control-api' OR invoker.owner_id<>'agent_control'
       OR jsonb_typeof(p_command)<>'object'
       OR NOT (p_command ?& ARRAY[
           'schema_revision','envelope','expected_run_state_generation',
           'expected_parent_state_generation','plan'
       ])
       OR p_command-ARRAY[
           'schema_revision','envelope','expected_run_state_generation',
           'expected_parent_state_generation','plan'
       ]<>'{}'::JSONB
       OR p_command->>'schema_revision'<>'1'
       OR jsonb_typeof(p_command->'envelope')<>'object'
       OR NOT agent_control.runtime_positive_bigint_json(
           p_command->'expected_run_state_generation')
       OR NOT agent_control.runtime_positive_bigint_json(
           p_command->'expected_parent_state_generation')
       OR NOT agent_control.cortex_task_graph_plan_valid(p_command->'plan') THEN
        RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid_cortex_task_graph_admission';
    END IF;
    envelope:=p_command->'envelope';
    plan:=p_command->'plan';
    IF NOT (envelope ?& ARRAY[
           'schema_revision','command_id','actor','audience','command_type',
           'idempotency_key','request_digest','causation_id','correlation_id','deadline'
       ])
       OR envelope-ARRAY[
           'schema_revision','command_id','actor','audience','command_type',
           'idempotency_key','request_digest','causation_id','correlation_id','deadline'
       ]<>'{}'::JSONB
       OR envelope->>'schema_revision'<>'1'
       OR NOT agent_control.runtime_identifier_valid(envelope->>'command_id')
       OR NOT agent_control.runtime_actor_valid(envelope->'actor')
       OR envelope#>>'{actor,kind}'<>'workload'
       OR envelope#>>'{actor,audience}'<>'control_api'
       OR envelope#>>'{actor,principal_id}'<>invoker.principal_id
       OR envelope->>'audience'<>'control_api'
       OR envelope->>'command_type'<>'admit_task_graph'
       OR NOT agent_control.runtime_identifier_valid(envelope->>'idempotency_key')
       OR NOT agent_control.runtime_digest_valid(envelope->>'request_digest')
       OR envelope->>'causation_id'<>plan->>'parent_task_id'
       OR envelope->>'correlation_id'<>plan->>'run_id'
       OR envelope->'actor'<>plan->'created_by'
       OR NOT agent_control.runtime_utc_instant_json(envelope->'deadline')
       OR (envelope->>'deadline')::TIMESTAMPTZ>(plan->>'deadline_at')::TIMESTAMPTZ THEN
        RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid_cortex_task_graph_envelope';
    END IF;
    plan_digest:=agent_control.runtime_contract_digest(
        'agent-platform.task-graph-plan.v1',plan);
    IF envelope->>'request_digest'<>plan_digest::TEXT THEN
        RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='cortex_task_graph_digest_mismatch';
    END IF;
    fingerprint:=agent_control.runtime_sha256_json(p_command);
    SELECT * INTO existing FROM agent_control.cortex_task_graph_admission
    WHERE command_id=envelope->>'command_id'
       OR idempotency_key=envelope->>'idempotency_key'
       OR graph_id=plan->>'graph_id';
    IF FOUND THEN
        IF existing.command_id<>envelope->>'command_id'
           OR existing.idempotency_key<>envelope->>'idempotency_key'
           OR existing.graph_id<>plan->>'graph_id'
           OR existing.body_fingerprint<>fingerprint THEN
            RAISE EXCEPTION USING ERRCODE='23505',MESSAGE='cortex task graph admission identity conflict';
        END IF;
        RETURN existing.response;
    END IF;
    IF at_time>=(envelope->>'deadline')::TIMESTAMPTZ
       OR at_time>=(plan->>'deadline_at')::TIMESTAMPTZ THEN
        RAISE EXCEPTION USING ERRCODE='57014',MESSAGE='cortex task graph admission deadline expired';
    END IF;

    SELECT * INTO run_row FROM agent_control.runtime_run
    WHERE run_id=plan->>'run_id' FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION USING ERRCODE='23503',MESSAGE='cortex task graph run not found';
    END IF;
    -- The Run row serializes same-Run graph identities. Recheck after waiting
    -- so exact concurrent retries return one response instead of conflicting.
    SELECT * INTO existing FROM agent_control.cortex_task_graph_admission
    WHERE command_id=envelope->>'command_id'
       OR idempotency_key=envelope->>'idempotency_key'
       OR graph_id=plan->>'graph_id';
    IF FOUND THEN
        IF existing.command_id<>envelope->>'command_id'
           OR existing.idempotency_key<>envelope->>'idempotency_key'
           OR existing.graph_id<>plan->>'graph_id'
           OR existing.body_fingerprint<>fingerprint THEN
            RAISE EXCEPTION USING ERRCODE='23505',MESSAGE='cortex task graph admission identity conflict';
        END IF;
        RETURN existing.response;
    END IF;
    SELECT * INTO parent_task FROM agent_control.runtime_task
    WHERE task_id=plan->>'parent_task_id' AND run_id=run_row.run_id FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION USING ERRCODE='23503',MESSAGE='cortex task graph parent not found';
    END IF;
    SELECT attempt.* INTO parent_attempt
    FROM agent_control.runtime_model_call_result result
    JOIN agent_control.runtime_turn turn
      ON turn.turn_id=result.turn_id AND turn.attempt_id=result.attempt_id
    JOIN agent_control.runtime_attempt attempt
      ON attempt.attempt_id=result.attempt_id
    WHERE result.result_id=plan#>>'{source_result,record_id}'
      AND result.record_digest::TEXT=plan#>>'{source_result,record_digest}'
      AND attempt.task_id=parent_task.task_id
      AND attempt.run_id=run_row.run_id
      AND turn.state='result_committed'
    FOR UPDATE OF attempt;
    IF NOT FOUND THEN
        RAISE EXCEPTION USING ERRCODE='23503',MESSAGE='cortex task graph source result not found';
    END IF;
    SELECT result.committed_at INTO STRICT source_result_committed_at
    FROM agent_control.runtime_model_call_result result
    WHERE result.result_id=plan#>>'{source_result,record_id}'
      AND result.record_digest::TEXT=plan#>>'{source_result,record_digest}';
    SELECT * INTO parent_session FROM agent_control.runtime_session
    WHERE session_id=parent_attempt.session_id FOR UPDATE;
    SELECT * INTO run_ledger FROM agent_control.runtime_budget_ledger
    WHERE ledger_id=run_row.budget_ledger_id FOR UPDATE;
    SELECT * INTO parent_ledger FROM agent_control.runtime_budget_ledger
    WHERE ledger_id=parent_task.budget_ledger_id FOR UPDATE;
    node_count:=jsonb_array_length(plan->'nodes');
    IF run_row.state<>'running' OR run_row.state_generation<>(
           p_command->>'expected_run_state_generation')::BIGINT
       OR parent_task.state<>'running' OR parent_task.state_generation<>(
           p_command->>'expected_parent_state_generation')::BIGINT
       OR parent_attempt.state<>'executing' OR at_time>=parent_attempt.lease_expires_at
       OR parent_session.state<>'open'
       OR at_time>=run_row.deadline_at OR at_time>=parent_task.deadline_at
       OR (plan->>'created_at')::TIMESTAMPTZ<source_result_committed_at
       OR (plan->>'deadline_at')::TIMESTAMPTZ>run_row.deadline_at
       OR (plan->>'deadline_at')::TIMESTAMPTZ>parent_task.deadline_at
       OR NOT agent_control.runtime_run_admission_current(run_row.run_id)
       OR run_row.runtime_policy_id<>plan#>>'{runtime_policy,record_id}'
       OR run_row.runtime_policy_generation<>(
           plan#>>'{runtime_policy,generation}')::BIGINT
       OR run_row.runtime_policy_digest::TEXT<>plan#>>'{runtime_policy,record_digest}'
       OR EXISTS (
           SELECT 1 FROM agent_control.runtime_turn turn
           WHERE turn.attempt_id=parent_attempt.attempt_id
             AND turn.state IN ('planned','dispatched','unknown')
       )
       OR run_ledger.state<>'open' OR parent_ledger.state<>'open'
       OR node_count>run_ledger.limit_tasks-run_ledger.consumed_tasks-run_ledger.reserved_tasks
       OR node_count>parent_ledger.limit_tasks-parent_ledger.consumed_tasks-parent_ledger.reserved_tasks
       OR node_count>parent_ledger.limit_fanout
       OR NOT agent_control.runtime_child_limit_within_parent(
           plan->'authorized_limit',parent_ledger)
       OR NOT agent_control.runtime_child_limit_within_parent(
           plan->'authorized_limit',run_ledger)
       OR parent_task.depth+(plan#>>'{authorized_limit,max_depth}')::BIGINT>run_ledger.limit_depth THEN
        RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='cortex task graph authority or budget unavailable';
    END IF;

    INSERT INTO agent_control.cortex_task_graph(
        graph_id,schema_revision,record_digest,run_id,parent_task_id,
        source_result_owner,source_result_record_type,source_result_id,
        source_result_schema_revision,source_result_digest,
        runtime_policy_owner,runtime_policy_record_type,runtime_policy_id,
        runtime_policy_schema_revision,runtime_policy_generation,runtime_policy_digest,
        round,max_rounds,authorized_limit,created_by_principal_id,created_by_kind,
        created_by_audience,created_at,deadline_at
    ) VALUES(
        plan->>'graph_id',1,plan_digest,run_row.run_id,parent_task.task_id,
        'agent_control','model_call_result',plan#>>'{source_result,record_id}',1,
        plan#>>'{source_result,record_digest}','agent_control','runtime_policy',
        plan#>>'{runtime_policy,record_id}',1,
        (plan#>>'{runtime_policy,generation}')::BIGINT,
        plan#>>'{runtime_policy,record_digest}',(plan->>'round')::BIGINT,
        (plan->>'max_rounds')::BIGINT,plan->'authorized_limit',
        plan#>>'{created_by,principal_id}','workload','control_api',
        (plan->>'created_at')::TIMESTAMPTZ,(plan->>'deadline_at')::TIMESTAMPTZ
    );

    -- Charge Task creation once to the Run and parent descendant ledgers.
    UPDATE agent_control.runtime_budget_ledger SET
        consumed_tasks=consumed_tasks+node_count,generation=generation+1,
        updated_at=greatest(updated_at,at_time)
    WHERE ledger_id=run_ledger.ledger_id;
    UPDATE agent_control.runtime_budget_ledger SET
        consumed_tasks=consumed_tasks+node_count,generation=generation+1,
        updated_at=greatest(updated_at,at_time)
    WHERE ledger_id=parent_ledger.ledger_id;

    FOR node,node_ordinal IN
        SELECT value,ordinality
        FROM jsonb_array_elements(plan->'nodes') WITH ORDINALITY item(value,ordinality)
        ORDER BY ordinality
    LOOP
        task_ledger_id:=gen_random_uuid()::TEXT;
        task_state:=CASE WHEN EXISTS (
            SELECT 1 FROM jsonb_array_elements(plan->'edges') edge(value)
            WHERE value->>'to_task_id'=node->>'task_id'
        ) THEN 'blocked' ELSE 'ready' END;
        INSERT INTO agent_control.runtime_budget_ledger(
            ledger_id,schema_revision,scope,scope_id,parent_ledger_id,
            runtime_policy_owner,runtime_policy_record_type,runtime_policy_id,
            runtime_policy_schema_revision,runtime_policy_generation,runtime_policy_digest,
            limit_model_calls,limit_input_tokens,limit_output_tokens,limit_tool_calls,
            limit_external_cost_micro_usd,limit_wall_time_ms,limit_idle_time_ms,
            limit_tasks,limit_depth,limit_fanout,limit_parallelism,
            limit_invalid_output_retries,limit_infrastructure_retries,
            consumed_tasks,generation,state,updated_at
        ) VALUES(
            task_ledger_id,1,'task',node->>'task_id',parent_ledger.ledger_id,
            parent_ledger.runtime_policy_owner,parent_ledger.runtime_policy_record_type,
            parent_ledger.runtime_policy_id,parent_ledger.runtime_policy_schema_revision,
            parent_ledger.runtime_policy_generation,parent_ledger.runtime_policy_digest,
            (node#>>'{limit,max_model_calls}')::BIGINT,
            (node#>>'{limit,max_input_tokens}')::BIGINT,
            (node#>>'{limit,max_output_tokens}')::BIGINT,
            (node#>>'{limit,max_tool_calls}')::BIGINT,
            (node#>>'{limit,max_external_cost_micro_usd}')::BIGINT,
            (node#>>'{limit,max_wall_time_ms}')::BIGINT,
            (node#>>'{limit,max_idle_time_ms}')::BIGINT,
            (node#>>'{limit,max_tasks}')::BIGINT,
            (node#>>'{limit,max_depth}')::BIGINT,
            (node#>>'{limit,max_fanout}')::BIGINT,
            (node#>>'{limit,max_parallelism}')::BIGINT,
            (node#>>'{limit,max_invalid_output_retries}')::BIGINT,
            (node#>>'{limit,max_infrastructure_retries}')::BIGINT,
            1,1,'open',at_time
        );
        INSERT INTO agent_control.runtime_task(
            task_id,schema_revision,run_id,parent_task_id,depth,objective,
            output_contract_owner,output_contract_record_type,
            output_contract_revision_id,output_contract_schema_revision,
            output_contract_generation,output_contract_digest,budget_ledger_id,
            state,state_generation,budget_slot_held,created_at,updated_at,deadline_at
        ) VALUES(
            node->>'task_id',1,run_row.run_id,parent_task.task_id,
            parent_task.depth+(node->>'depth')::BIGINT,node->'objective',
            'agent_control','output_contract_revision',
            node#>>'{output_contract,record_id}',1,
            (node#>>'{output_contract,generation}')::BIGINT,
            node#>>'{output_contract,record_digest}',task_ledger_id,
            task_state,1,false,at_time,at_time,
            least((node->>'deadline_at')::TIMESTAMPTZ,run_row.deadline_at,parent_task.deadline_at)
        );
        FOR input_ref,input_ordinal IN
            SELECT value,ordinality
            FROM jsonb_array_elements(node->'input_refs') WITH ORDINALITY item(value,ordinality)
            ORDER BY ordinality
        LOOP
            INSERT INTO agent_control.runtime_task_input_ref(task_id,ordinal,reference)
            VALUES(node->>'task_id',input_ordinal,input_ref);
        END LOOP;
        INSERT INTO agent_control.cortex_task_graph_node(
            graph_id,ordinal,task_id,role_id,role_revision,depth,objective,input_refs,
            output_contract_name,output_contract_owner,output_contract_record_type,
            output_contract_revision_id,output_contract_schema_revision,
            output_contract_generation,output_contract_digest,task_limit,deadline_at
        ) VALUES(
            plan->>'graph_id',node_ordinal,node->>'task_id',node->>'role_id',
            (node->>'role_revision')::INTEGER,(node->>'depth')::BIGINT,
            node->'objective',node->'input_refs',node->>'output_contract_name',
            'agent_control','output_contract_revision',
            node#>>'{output_contract,record_id}',1,
            (node#>>'{output_contract,generation}')::BIGINT,
            node#>>'{output_contract,record_digest}',node->'limit',
            (node->>'deadline_at')::TIMESTAMPTZ
        );
        FOR grant_value,grant_ordinal IN
            SELECT value,ordinality
            FROM jsonb_array_elements(node->'tool_grants') WITH ORDINALITY item(value,ordinality)
            ORDER BY ordinality
        LOOP
            INSERT INTO agent_control.cortex_task_graph_tool_grant(
                graph_id,task_id,ordinal,role_id,tool_id,tool_revision,effect
            ) VALUES(
                plan->>'graph_id',node->>'task_id',grant_ordinal,node->>'role_id',
                grant_value->>'tool_id',(grant_value->>'tool_revision')::INTEGER,grant_value->>'effect'
            );
        END LOOP;
        PERFORM agent_control.runtime_insert_event(
            'task',node->>'task_id',NULL,task_state,1,
            parent_attempt.lease_worker->>'principal_id',
            envelope->>'command_id',run_row.run_id,'task_graph_node_admitted',at_time);
        task_ids:=task_ids||jsonb_build_array(node->>'task_id');
    END LOOP;

    FOR edge,edge_ordinal IN
        SELECT value,ordinality
        FROM jsonb_array_elements(plan->'edges') WITH ORDINALITY item(value,ordinality)
        ORDER BY ordinality
    LOOP
        INSERT INTO agent_control.cortex_task_graph_edge(
            graph_id,ordinal,from_task_id,to_task_id
        ) VALUES(
            plan->>'graph_id',edge_ordinal,edge->>'from_task_id',edge->>'to_task_id'
        );
        INSERT INTO agent_control.runtime_task_dependency(
            task_id,depends_on_task_id,run_id,schema_revision,requires_success,created_at
        ) VALUES(
            edge->>'to_task_id',edge->>'from_task_id',run_row.run_id,1,
            COALESCE((
                SELECT join_item.value->>'policy'='all_required'
                FROM jsonb_array_elements(plan->'joins') join_item(value)
                WHERE join_item.value->>'downstream_task_id'=edge->>'to_task_id'
            ),true),at_time
        );
    END LOOP;

    FOR join_value,join_ordinal IN
        SELECT value,ordinality
        FROM jsonb_array_elements(plan->'joins') WITH ORDINALITY item(value,ordinality)
        ORDER BY ordinality
    LOOP
        INSERT INTO agent_control.cortex_task_graph_join(
            graph_id,join_id,ordinal,downstream_task_id,policy,minimum_success,
            failure_policy,deadline_at
        ) VALUES(
            plan->>'graph_id',join_value->>'join_id',join_ordinal,
            join_value->>'downstream_task_id',join_value->>'policy',
            (join_value->>'minimum_success')::BIGINT,join_value->>'failure_policy',
            (join_value->>'deadline_at')::TIMESTAMPTZ
        );
        INSERT INTO agent_control.cortex_task_graph_join_upstream(
            graph_id,join_id,ordinal,upstream_task_id
        )
        SELECT plan->>'graph_id',join_value->>'join_id',ordinality,value
        FROM jsonb_array_elements_text(join_value->'upstream_task_ids')
             WITH ORDINALITY item(value,ordinality);
    END LOOP;

    worker_principal:=parent_attempt.lease_worker->>'principal_id';
    UPDATE agent_control.runtime_attempt SET
        state='superseded',state_generation=state_generation+1,
        updated_at=greatest(updated_at,at_time),terminal_at=at_time
    WHERE attempt_id=parent_attempt.attempt_id;
    PERFORM agent_control.runtime_insert_attempt_release_event(
        parent_attempt.attempt_id,parent_attempt.lease_generation,worker_principal,
        parent_attempt.lease_token,parent_attempt.lease_expires_at,
        envelope->>'command_id',run_row.run_id,at_time);
    PERFORM agent_control.runtime_insert_event(
        'attempt',parent_attempt.attempt_id,'executing','superseded',
        parent_attempt.state_generation+1,worker_principal,envelope->>'command_id',
        run_row.run_id,'parent_waiting_for_task_graph',at_time);
    UPDATE agent_control.runtime_session SET
        state='closed',generation=generation+1,closed_at=at_time
    WHERE session_id=parent_session.session_id;
    PERFORM agent_control.runtime_insert_event(
        'session',parent_session.session_id,'open','closed',
        parent_session.generation+1,worker_principal,envelope->>'command_id',
        run_row.run_id,'parent_session_parked_for_task_graph',at_time);
    UPDATE agent_control.runtime_task SET
        state='waiting',state_generation=state_generation+1,
        updated_at=greatest(updated_at,at_time)
    WHERE task_id=parent_task.task_id;
    PERFORM agent_control.runtime_insert_event(
        'task',parent_task.task_id,'running','waiting',
        parent_task.state_generation+1,worker_principal,envelope->>'command_id',
        run_row.run_id,'parent_waiting_for_task_graph',at_time);

    response:=jsonb_build_object(
        'status','admitted','graph_id',plan->>'graph_id','run_id',run_row.run_id,
        'parent_task_id',parent_task.task_id,'task_ids',task_ids,
        'task_count',node_count,'parent_task_state','waiting'
    );
    INSERT INTO agent_control.cortex_task_graph_admission(
        command_id,idempotency_key,body_fingerprint,graph_id,run_id,
        parent_task_id,response,created_at
    ) VALUES(
        envelope->>'command_id',envelope->>'idempotency_key',fingerprint,
        plan->>'graph_id',run_row.run_id,parent_task.task_id,response,at_time
    );
    RETURN response;
END
$$;

-- Future root Tasks must carry the RuntimePolicy's bounded descendant budget.
-- The legacy value 2 allowed only the old one-Scout chain and cannot admit a
-- three-or-more-node graph. Existing immutable Runs are not rewritten.
DO $migration$
DECLARE
    definition TEXT;
    old_fragment TEXT:='policy.max_idle_time_ms,2,policy.max_depth';
    new_fragment TEXT:='policy.max_idle_time_ms,policy.max_tasks,policy.max_depth';
BEGIN
    SELECT pg_get_functiondef(
        'agent_control.admit_cortex_user_request_run_v9(jsonb)'::regprocedure
    ) INTO definition;
    IF position(old_fragment IN definition)=0 THEN
        RAISE EXCEPTION 'unexpected Cortex v9 root Task budget definition';
    END IF;
    EXECUTE replace(
        replace(
            definition,
            'admit_cortex_user_request_run_v9',
            'admit_cortex_user_request_run_v10'
        ),
        old_fragment,
        new_fragment
    );
END
$migration$;

REVOKE ALL ON FUNCTION agent_control.cortex_task_graph_plan_valid(JSONB) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.admit_cortex_task_graph(JSONB) FROM PUBLIC;
REVOKE ALL ON TABLE agent_control.cortex_task_graph_admission FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agent_control.admit_cortex_task_graph(JSONB)
TO alpheus_agent_control_api;
REVOKE ALL ON FUNCTION agent_control.admit_cortex_user_request_run_v10(JSONB) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agent_control.admit_cortex_user_request_run_v10(JSONB)
TO alpheus_agent_control_api;

RESET ROLE;
