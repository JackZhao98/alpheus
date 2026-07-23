-- A graph's final answer remains the Decision Desk Artifact that produced it.
-- The parked root Task is superseded by that graph result; copying a child
-- Artifact ID into the root Task would violate exact Task/Attempt lineage.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

CREATE TABLE agent_control.cortex_task_graph_result (
    graph_id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL UNIQUE,
    parent_task_id TEXT NOT NULL UNIQUE,
    decision_task_id TEXT NOT NULL UNIQUE,
    artifact_id TEXT NOT NULL UNIQUE,
    artifact_digest CHAR(64) NOT NULL CHECK (
        agent_control.runtime_digest_valid(artifact_digest::TEXT)
    ),
    recorded_at TIMESTAMPTZ NOT NULL,
    FOREIGN KEY(graph_id,run_id)
        REFERENCES agent_control.cortex_task_graph(graph_id,run_id)
        DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY(parent_task_id,run_id)
        REFERENCES agent_control.runtime_task(task_id,run_id)
        DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY(graph_id,decision_task_id)
        REFERENCES agent_control.cortex_task_graph_node(graph_id,task_id)
        DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY(artifact_id,artifact_digest,run_id,decision_task_id)
        REFERENCES agent_control.runtime_artifact(
            artifact_id,record_digest,run_id,task_id
        ) DEFERRABLE INITIALLY DEFERRED
);
CREATE TRIGGER cortex_task_graph_result_immutable
BEFORE UPDATE OR DELETE ON agent_control.cortex_task_graph_result
FOR EACH ROW EXECUTE FUNCTION
agent_control.reject_runtime_immutable_mutation();

DO $migration$
DECLARE
    definition TEXT;
    original_definition TEXT;
    old_block TEXT:=$old$
        IF parent_row.budget_slot_held THEN
            IF NOT agent_control.runtime_release_active_slot_ancestors(
                run_row.run_id,parent_row.budget_ledger_id,at_time) THEN
                RAISE EXCEPTION USING ERRCODE='40001',
                    MESSAGE='TaskGraph parent active slot changed';
            END IF;
        END IF;
        UPDATE agent_control.runtime_task
        SET state='running',state_generation=state_generation+1,
            updated_at=greatest(updated_at,at_time)
        WHERE task_id=parent_row.task_id;
        PERFORM agent_control.runtime_insert_event(
            'task',parent_row.task_id,'waiting','running',
            parent_row.state_generation+1,p_worker_principal,
            candidate.graph_id,run_row.run_id,
            'task_graph_join_completed',at_time);
        UPDATE agent_control.runtime_task
        SET state='result_committed',state_generation=state_generation+1,
            result_artifact_id=desk_row.result_artifact_id,
            updated_at=greatest(updated_at,at_time)
        WHERE task_id=parent_row.task_id;
        PERFORM agent_control.runtime_insert_event(
            'task',parent_row.task_id,'running','result_committed',
            parent_row.state_generation+2,p_worker_principal,
            candidate.graph_id,run_row.run_id,
            'task_graph_result_promoted',at_time);
        UPDATE agent_control.runtime_task
        SET state='succeeded',state_generation=state_generation+1,
            budget_slot_held=false,
            updated_at=greatest(updated_at,at_time),terminal_at=at_time
        WHERE task_id=parent_row.task_id;
        PERFORM agent_control.runtime_insert_event(
            'task',parent_row.task_id,'result_committed','succeeded',
            parent_row.state_generation+3,p_worker_principal,
            candidate.graph_id,run_row.run_id,
            'task_graph_succeeded',at_time);
$old$;
    new_block TEXT:=$new$
        INSERT INTO agent_control.cortex_task_graph_result(
            graph_id,run_id,parent_task_id,decision_task_id,
            artifact_id,artifact_digest,recorded_at
        )
        SELECT candidate.graph_id,run_row.run_id,parent_row.task_id,
               desk_row.task_id,artifact.artifact_id,
               artifact.record_digest,at_time
        FROM agent_control.runtime_artifact AS artifact
        WHERE artifact.artifact_id=desk_row.result_artifact_id
          AND artifact.run_id=run_row.run_id
          AND artifact.task_id=desk_row.task_id;
        IF NOT FOUND THEN
            RAISE EXCEPTION USING ERRCODE='23503',
                MESSAGE='TaskGraph Decision Desk Artifact missing';
        END IF;
        IF parent_row.budget_slot_held THEN
            IF NOT agent_control.runtime_release_active_slot_ancestors(
                run_row.run_id,parent_row.budget_ledger_id,at_time) THEN
                RAISE EXCEPTION USING ERRCODE='40001',
                    MESSAGE='TaskGraph parent active slot changed';
            END IF;
        END IF;
        UPDATE agent_control.runtime_task
        SET state='superseded',state_generation=state_generation+1,
            budget_slot_held=false,
            updated_at=greatest(updated_at,at_time),terminal_at=at_time
        WHERE task_id=parent_row.task_id;
        PERFORM agent_control.runtime_insert_event(
            'task',parent_row.task_id,'waiting','superseded',
            parent_row.state_generation+1,p_worker_principal,
            candidate.graph_id,run_row.run_id,
            'task_graph_result_promoted',at_time);
$new$;
BEGIN
    definition:=pg_get_functiondef(
        'agent_control.reconcile_cortex_task_graph_joins(text)'::REGPROCEDURE);
    original_definition:=definition;
    definition:=replace(definition,old_block,new_block);
    IF definition=original_definition
       OR position(new_block IN definition)=0 THEN
        RAISE EXCEPTION 'Unexpected TaskGraph Join completion definition';
    END IF;
    EXECUTE definition;
END
$migration$;

REVOKE ALL ON TABLE agent_control.cortex_task_graph_result FROM PUBLIC;
REVOKE ALL ON FUNCTION
agent_control.reconcile_cortex_task_graph_joins(TEXT) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION
agent_control.reconcile_cortex_task_graph_joins(TEXT)
TO alpheus_agent_control_api;

RESET ROLE;
