-- Immutable storage for accepted Cortex TaskGraph v1 plans. This migration
-- creates no graph and enables no parallel execution; 0065 owns the sole
-- atomic Control admission command.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

CREATE TABLE agent_control.cortex_task_graph (
    graph_id TEXT PRIMARY KEY CHECK (agent_control.runtime_identifier_valid(graph_id)),
    schema_revision SMALLINT NOT NULL CHECK (schema_revision=1),
    record_digest CHAR(64) NOT NULL UNIQUE CHECK (
        agent_control.runtime_digest_valid(record_digest::TEXT)
    ),
    run_id TEXT NOT NULL CHECK (agent_control.runtime_identifier_valid(run_id)),
    parent_task_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(parent_task_id)
    ),
    source_result_owner TEXT NOT NULL CHECK (source_result_owner='agent_control'),
    source_result_record_type TEXT NOT NULL CHECK (
        source_result_record_type='model_call_result'
    ),
    source_result_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(source_result_id)
    ),
    source_result_schema_revision SMALLINT NOT NULL CHECK (
        source_result_schema_revision=1
    ),
    source_result_digest CHAR(64) NOT NULL CHECK (
        agent_control.runtime_digest_valid(source_result_digest::TEXT)
    ),
    runtime_policy_owner TEXT NOT NULL CHECK (runtime_policy_owner='agent_control'),
    runtime_policy_record_type TEXT NOT NULL CHECK (
        runtime_policy_record_type='runtime_policy'
    ),
    runtime_policy_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(runtime_policy_id)
    ),
    runtime_policy_schema_revision SMALLINT NOT NULL CHECK (
        runtime_policy_schema_revision=1
    ),
    runtime_policy_generation BIGINT NOT NULL CHECK (runtime_policy_generation>0),
    runtime_policy_digest CHAR(64) NOT NULL CHECK (
        agent_control.runtime_digest_valid(runtime_policy_digest::TEXT)
    ),
    round BIGINT NOT NULL CHECK (round BETWEEN 1 AND 8),
    max_rounds BIGINT NOT NULL CHECK (max_rounds BETWEEN round AND 8),
    authorized_limit JSONB NOT NULL CHECK (
        agent_control.runtime_child_budget_limit_valid(authorized_limit)
        AND (authorized_limit->>'max_tasks')::BIGINT BETWEEN 1 AND 64
        AND (authorized_limit->>'max_depth')::BIGINT BETWEEN 1 AND 8
        AND (authorized_limit->>'max_parallelism')::BIGINT BETWEEN 1 AND 16
    ),
    created_by_principal_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(created_by_principal_id)
    ),
    created_by_kind TEXT NOT NULL CHECK (created_by_kind='workload'),
    created_by_audience TEXT NOT NULL CHECK (created_by_audience='control_api'),
    created_at TIMESTAMPTZ NOT NULL,
    deadline_at TIMESTAMPTZ NOT NULL,
    UNIQUE(run_id,parent_task_id,round),
    UNIQUE(graph_id,run_id),
    CHECK(created_at<deadline_at),
    FOREIGN KEY(run_id) REFERENCES agent_control.runtime_run(run_id)
        DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY(parent_task_id,run_id)
        REFERENCES agent_control.runtime_task(task_id,run_id)
        DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY(source_result_id,source_result_digest)
        REFERENCES agent_control.runtime_model_call_result(result_id,record_digest)
        DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY(runtime_policy_id,runtime_policy_generation,runtime_policy_digest)
        REFERENCES agent_control.runtime_policy_revision(policy_id,generation,record_digest)
        DEFERRABLE INITIALLY DEFERRED
);

CREATE TABLE agent_control.cortex_task_graph_node (
    graph_id TEXT NOT NULL REFERENCES agent_control.cortex_task_graph(graph_id)
        DEFERRABLE INITIALLY DEFERRED,
    ordinal INTEGER NOT NULL CHECK (ordinal BETWEEN 1 AND 64),
    task_id TEXT NOT NULL UNIQUE CHECK (agent_control.runtime_identifier_valid(task_id)),
    role_id TEXT NOT NULL CHECK (agent_control.runtime_name_valid(role_id)),
    role_revision INTEGER NOT NULL CHECK (role_revision=1),
    depth BIGINT NOT NULL CHECK (depth BETWEEN 1 AND 8),
    objective JSONB NOT NULL CHECK (
        agent_control.runtime_blob_ref_valid(objective,'task_objective','')
    ),
    input_refs JSONB NOT NULL CHECK (
        agent_control.runtime_child_input_refs_valid(input_refs)
    ),
    output_contract_name TEXT NOT NULL CHECK (
        agent_control.runtime_name_valid(output_contract_name)
        AND output_contract_name IN ('specialist_memo_v1','answer_v1')
    ),
    output_contract_owner TEXT NOT NULL CHECK (output_contract_owner='agent_control'),
    output_contract_record_type TEXT NOT NULL CHECK (
        output_contract_record_type='output_contract_revision'
    ),
    output_contract_revision_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(output_contract_revision_id)
    ),
    output_contract_schema_revision SMALLINT NOT NULL CHECK (
        output_contract_schema_revision=1
    ),
    output_contract_generation BIGINT NOT NULL CHECK (output_contract_generation>0),
    output_contract_digest CHAR(64) NOT NULL CHECK (
        agent_control.runtime_digest_valid(output_contract_digest::TEXT)
    ),
    task_limit JSONB NOT NULL CHECK (
        agent_control.runtime_child_budget_limit_valid(task_limit)
        AND (task_limit->>'max_model_calls')::BIGINT>0
        AND (task_limit->>'max_tasks')::BIGINT=1
        AND (task_limit->>'max_depth')::BIGINT=0
        AND (task_limit->>'max_fanout')::BIGINT=0
        AND (task_limit->>'max_parallelism')::BIGINT=1
    ),
    deadline_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY(graph_id,task_id),
    UNIQUE(graph_id,ordinal),
    CHECK (
        (role_id='decision_desk' AND output_contract_name='answer_v1')
        OR
        (role_id<>'decision_desk' AND output_contract_name='specialist_memo_v1')
    ),
    FOREIGN KEY(task_id) REFERENCES agent_control.runtime_task(task_id)
        DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY(output_contract_revision_id,output_contract_generation,output_contract_digest)
        REFERENCES agent_control.output_contract_revision(revision_id,generation,record_digest)
        DEFERRABLE INITIALLY DEFERRED
);

CREATE TABLE agent_control.cortex_task_graph_tool_grant (
    graph_id TEXT NOT NULL,
    task_id TEXT NOT NULL,
    ordinal INTEGER NOT NULL CHECK (ordinal=1),
    role_id TEXT NOT NULL CHECK (
        agent_control.runtime_name_valid(role_id) AND role_id<>'decision_desk'
    ),
    tool_id TEXT NOT NULL CHECK (agent_control.runtime_name_valid(tool_id)),
    tool_revision INTEGER NOT NULL CHECK (tool_revision=1),
    effect TEXT NOT NULL CHECK (effect='read_only'),
    PRIMARY KEY(graph_id,task_id,tool_id),
    UNIQUE(graph_id,task_id,ordinal),
    FOREIGN KEY(graph_id,task_id)
        REFERENCES agent_control.cortex_task_graph_node(graph_id,task_id)
        DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY(role_id,tool_id)
        REFERENCES agent_control.cortex_specialist_tool_grant(role_id,tool_id)
        DEFERRABLE INITIALLY DEFERRED
);

CREATE TABLE agent_control.cortex_task_graph_edge (
    graph_id TEXT NOT NULL,
    ordinal INTEGER NOT NULL CHECK (ordinal BETWEEN 1 AND 256),
    from_task_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(from_task_id)
    ),
    to_task_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(to_task_id)
    ),
    PRIMARY KEY(graph_id,from_task_id,to_task_id),
    UNIQUE(graph_id,ordinal),
    CHECK(from_task_id<>to_task_id),
    FOREIGN KEY(graph_id,from_task_id)
        REFERENCES agent_control.cortex_task_graph_node(graph_id,task_id)
        DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY(graph_id,to_task_id)
        REFERENCES agent_control.cortex_task_graph_node(graph_id,task_id)
        DEFERRABLE INITIALLY DEFERRED
);

CREATE TABLE agent_control.cortex_task_graph_join (
    graph_id TEXT NOT NULL,
    join_id TEXT NOT NULL CHECK (agent_control.runtime_identifier_valid(join_id)),
    ordinal INTEGER NOT NULL CHECK (ordinal BETWEEN 1 AND 64),
    downstream_task_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(downstream_task_id)
    ),
    policy TEXT NOT NULL CHECK (policy IN ('all_required','minimum_succeeded')),
    minimum_success BIGINT NOT NULL CHECK (minimum_success>0),
    failure_policy TEXT NOT NULL CHECK (
        failure_policy IN ('fail_graph','continue_if_threshold_met')
    ),
    deadline_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY(graph_id,join_id),
    UNIQUE(graph_id,ordinal),
    UNIQUE(graph_id,downstream_task_id),
    CHECK (
        (policy='all_required' AND failure_policy='fail_graph')
        OR
        (policy='minimum_succeeded' AND failure_policy='continue_if_threshold_met')
    ),
    FOREIGN KEY(graph_id,downstream_task_id)
        REFERENCES agent_control.cortex_task_graph_node(graph_id,task_id)
        DEFERRABLE INITIALLY DEFERRED
);

CREATE TABLE agent_control.cortex_task_graph_join_upstream (
    graph_id TEXT NOT NULL,
    join_id TEXT NOT NULL,
    ordinal INTEGER NOT NULL CHECK (ordinal BETWEEN 1 AND 64),
    upstream_task_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(upstream_task_id)
    ),
    PRIMARY KEY(graph_id,join_id,upstream_task_id),
    UNIQUE(graph_id,join_id,ordinal),
    FOREIGN KEY(graph_id,join_id)
        REFERENCES agent_control.cortex_task_graph_join(graph_id,join_id)
        DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY(graph_id,upstream_task_id)
        REFERENCES agent_control.cortex_task_graph_node(graph_id,task_id)
        DEFERRABLE INITIALLY DEFERRED
);

CREATE TRIGGER cortex_task_graph_immutable
BEFORE UPDATE OR DELETE ON agent_control.cortex_task_graph
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();
CREATE TRIGGER cortex_task_graph_node_immutable
BEFORE UPDATE OR DELETE ON agent_control.cortex_task_graph_node
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();
CREATE TRIGGER cortex_task_graph_tool_grant_immutable
BEFORE UPDATE OR DELETE ON agent_control.cortex_task_graph_tool_grant
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();
CREATE TRIGGER cortex_task_graph_edge_immutable
BEFORE UPDATE OR DELETE ON agent_control.cortex_task_graph_edge
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();
CREATE TRIGGER cortex_task_graph_join_immutable
BEFORE UPDATE OR DELETE ON agent_control.cortex_task_graph_join
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();
CREATE TRIGGER cortex_task_graph_join_upstream_immutable
BEFORE UPDATE OR DELETE ON agent_control.cortex_task_graph_join_upstream
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();

REVOKE ALL ON TABLE
    agent_control.cortex_task_graph,
    agent_control.cortex_task_graph_node,
    agent_control.cortex_task_graph_tool_grant,
    agent_control.cortex_task_graph_edge,
    agent_control.cortex_task_graph_join,
    agent_control.cortex_task_graph_join_upstream
FROM PUBLIC;

RESET ROLE;
