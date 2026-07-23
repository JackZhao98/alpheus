-- Resolve admitted Join barriers only from terminal, committed Task state.
-- Successful memo Blobs receive downstream-session-scoped Worker ACLs; failed
-- all-required/threshold joins terminalize the parked parent Run.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

CREATE TABLE agent_control.cortex_task_graph_join_resolution (
    graph_id TEXT NOT NULL,
    join_id TEXT NOT NULL,
    downstream_task_id TEXT NOT NULL,
    outcome TEXT NOT NULL CHECK (outcome IN ('ready','failed')),
    successful_upstream_task_ids JSONB NOT NULL CHECK (
        jsonb_typeof(successful_upstream_task_ids)='array'
        AND jsonb_array_length(successful_upstream_task_ids)<=64
    ),
    failed_upstream_task_ids JSONB NOT NULL CHECK (
        jsonb_typeof(failed_upstream_task_ids)='array'
        AND jsonb_array_length(failed_upstream_task_ids)<=64
    ),
    inputs JSONB NOT NULL CHECK (
        jsonb_typeof(inputs)='array'
        AND jsonb_array_length(inputs)<=64
    ),
    record_digest CHAR(64) NOT NULL CHECK (
        agent_control.runtime_digest_valid(record_digest::TEXT)
    ),
    resolved_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY(graph_id,join_id),
    UNIQUE(graph_id,downstream_task_id),
    FOREIGN KEY(graph_id,join_id)
        REFERENCES agent_control.cortex_task_graph_join(graph_id,join_id)
        DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY(graph_id,downstream_task_id)
        REFERENCES agent_control.cortex_task_graph_node(graph_id,task_id)
        DEFERRABLE INITIALLY DEFERRED
);
CREATE TRIGGER cortex_task_graph_join_resolution_immutable
BEFORE UPDATE OR DELETE ON agent_control.cortex_task_graph_join_resolution
FOR EACH ROW EXECUTE FUNCTION
agent_control.reject_runtime_immutable_mutation();

CREATE FUNCTION agent_control.reconcile_cortex_task_graph_joins(
    p_worker_principal TEXT
) RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security,blob
SET timezone='UTC' AS $$
DECLARE
    invoker RECORD;
    candidate RECORD;
    graph_row agent_control.cortex_task_graph%ROWTYPE;
    run_row agent_control.runtime_run%ROWTYPE;
    parent_row agent_control.runtime_task%ROWTYPE;
    downstream_row agent_control.runtime_task%ROWTYPE;
    downstream_session agent_control.runtime_session%ROWTYPE;
    upstream RECORD;
    desk_row agent_control.runtime_task%ROWTYPE;
    successful_ids JSONB;
    failed_ids JSONB;
    inputs_value JSONB;
    outcome_value TEXT;
    resolution_body JSONB;
    resolution_digest CHAR(64);
    binding_id_value TEXT;
    at_time TIMESTAMPTZ;
    resolved_count BIGINT:=0;
    ready_count BIGINT:=0;
    failed_count BIGINT:=0;
    completed_count BIGINT:=0;
BEGIN
    SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
    IF invoker.group_role::TEXT<>'alpheus_agent_control_api'
       OR invoker.profile_id<>'control-api'
       OR invoker.owner_id<>'agent_control'
       OR NOT agent_control.runtime_identifier_valid(p_worker_principal) THEN
        RAISE EXCEPTION USING ERRCODE='42501',
            MESSAGE='TaskGraph Join reconciliation denied';
    END IF;

    FOR candidate IN
        SELECT join_row.*
        FROM agent_control.cortex_task_graph_join AS join_row
        WHERE NOT EXISTS (
            SELECT 1
            FROM agent_control.cortex_task_graph_join_resolution AS resolution
            WHERE resolution.graph_id=join_row.graph_id
              AND resolution.join_id=join_row.join_id
        )
        ORDER BY join_row.graph_id,join_row.ordinal
        LIMIT 32
    LOOP
        SELECT * INTO STRICT graph_row
        FROM agent_control.cortex_task_graph
        WHERE graph_id=candidate.graph_id
        FOR SHARE;
        SELECT * INTO STRICT run_row
        FROM agent_control.runtime_run
        WHERE run_id=graph_row.run_id
        FOR UPDATE;
        SELECT * INTO STRICT parent_row
        FROM agent_control.runtime_task
        WHERE task_id=graph_row.parent_task_id
        FOR UPDATE;

        PERFORM task.task_id
        FROM agent_control.cortex_task_graph_join_upstream AS member
        JOIN agent_control.runtime_task AS task
          ON task.task_id=member.upstream_task_id
        WHERE member.graph_id=candidate.graph_id
          AND member.join_id=candidate.join_id
        ORDER BY task.task_id
        FOR UPDATE OF task;
        SELECT * INTO STRICT downstream_row
        FROM agent_control.runtime_task
        WHERE task_id=candidate.downstream_task_id
        FOR UPDATE;
        SELECT * INTO STRICT downstream_session
        FROM agent_control.runtime_session
        WHERE session_id=downstream_row.session_id
        FOR UPDATE;

        IF downstream_row.state<>'blocked'
           OR downstream_session.state<>'open'
           OR parent_row.state<>'waiting'
           OR run_row.state NOT IN ('running','waiting') THEN
            CONTINUE;
        END IF;
        IF EXISTS (
            SELECT 1
            FROM agent_control.cortex_task_graph_join_upstream AS member
            JOIN agent_control.runtime_task AS task
              ON task.task_id=member.upstream_task_id
            WHERE member.graph_id=candidate.graph_id
              AND member.join_id=candidate.join_id
              AND NOT agent_control.runtime_terminal_state('task',task.state)
        ) THEN
            CONTINUE;
        END IF;

        SELECT
          COALESCE(jsonb_agg(member.upstream_task_id ORDER BY member.ordinal)
            FILTER(WHERE task.state='succeeded'),'[]'::JSONB),
          COALESCE(jsonb_agg(member.upstream_task_id ORDER BY member.ordinal)
            FILTER(WHERE task.state<>'succeeded'),'[]'::JSONB)
        INTO successful_ids,failed_ids
        FROM agent_control.cortex_task_graph_join_upstream AS member
        JOIN agent_control.runtime_task AS task
          ON task.task_id=member.upstream_task_id
        WHERE member.graph_id=candidate.graph_id
          AND member.join_id=candidate.join_id;

        outcome_value:=CASE
          WHEN jsonb_array_length(successful_ids)>=candidate.minimum_success
            THEN 'ready'
          ELSE 'failed'
        END;
        inputs_value:='[]'::JSONB;
        at_time:=clock_timestamp();

        IF outcome_value='ready' THEN
            FOR upstream IN
                SELECT
                    member.upstream_task_id,
                    node.role_id,
                    artifact.artifact_id,
                    artifact.record_digest::TEXT AS artifact_digest,
                    section.content
                FROM agent_control.cortex_task_graph_join_upstream AS member
                JOIN agent_control.cortex_task_graph_node AS node
                  ON node.graph_id=member.graph_id
                 AND node.task_id=member.upstream_task_id
                JOIN agent_control.runtime_task AS task
                  ON task.task_id=member.upstream_task_id
                 AND task.state='succeeded'
                JOIN agent_control.runtime_artifact AS artifact
                  ON artifact.artifact_id=task.result_artifact_id
                 AND artifact.task_id=task.task_id
                JOIN agent_control.runtime_artifact_section AS section
                  ON section.artifact_id=artifact.artifact_id
                 AND section.name='memo' AND section.required
                WHERE member.graph_id=candidate.graph_id
                  AND member.join_id=candidate.join_id
                ORDER BY member.ordinal
            LOOP
                binding_id_value:=
                  'cortex-session:'||downstream_session.session_id||
                  ':join:'||upstream.upstream_task_id;
                PERFORM blob.bind_reference_internal(
                    'agent_control',binding_id_value,
                    (upstream.content->>'blob_id')::UUID,
                    'artifact',upstream.artifact_id,
                    upstream.artifact_digest,invoker.principal_id,
                    'explicit',least(run_row.deadline_at,
                      downstream_row.deadline_at),invoker.principal_id);
                PERFORM blob.change_acl_internal(
                    'agent_control',binding_id_value,invoker.principal_id,
                    p_worker_principal,0,'grant',
                    'cortex_worker_task_graph_join',invoker.principal_id);
                inputs_value:=inputs_value||jsonb_build_array(
                    jsonb_build_object(
                        'task_id',upstream.upstream_task_id,
                        'role_id',upstream.role_id,
                        'artifact',jsonb_build_object(
                            'owner','agent_control',
                            'record_type','artifact',
                            'record_id',upstream.artifact_id,
                            'schema_revision',1,
                            'record_digest',upstream.artifact_digest
                        ),
                        'content',jsonb_set(
                            upstream.content,'{origin}',
                            jsonb_build_object(
                                'owner','agent_control',
                                'record_type','artifact',
                                'record_id',upstream.artifact_id,
                                'schema_revision',1,
                                'record_digest',upstream.artifact_digest
                            )
                        ),
                        'binding_id',binding_id_value
                    )
                );
            END LOOP;
        END IF;

        resolution_body:=jsonb_build_object(
            'schema_revision',1,
            'graph_id',candidate.graph_id,
            'join_id',candidate.join_id,
            'downstream_task_id',candidate.downstream_task_id,
            'outcome',outcome_value,
            'successful_upstream_task_ids',successful_ids,
            'failed_upstream_task_ids',failed_ids,
            'inputs',inputs_value,
            'resolved_at',agent_control.runtime_utc_text(at_time)
        );
        resolution_digest:=agent_control.runtime_contract_digest(
            'agent-platform.task-graph-join-resolution.v1',
            resolution_body);
        INSERT INTO agent_control.cortex_task_graph_join_resolution(
            graph_id,join_id,downstream_task_id,outcome,
            successful_upstream_task_ids,failed_upstream_task_ids,
            inputs,record_digest,resolved_at
        ) VALUES(
            candidate.graph_id,candidate.join_id,
            candidate.downstream_task_id,outcome_value,
            successful_ids,failed_ids,inputs_value,resolution_digest,at_time
        );
        resolved_count:=resolved_count+1;

        IF outcome_value='ready' THEN
            UPDATE agent_control.runtime_task
            SET state='ready',state_generation=state_generation+1,
                updated_at=greatest(updated_at,at_time)
            WHERE task_id=downstream_row.task_id;
            PERFORM agent_control.runtime_insert_event(
                'task',downstream_row.task_id,'blocked','ready',
                downstream_row.state_generation+1,p_worker_principal,
                candidate.join_id,run_row.run_id,
                'task_graph_join_ready',at_time);
            ready_count:=ready_count+1;
        ELSE
            UPDATE agent_control.runtime_task
            SET state='dead_lettered',
                state_generation=state_generation+1,
                updated_at=greatest(updated_at,at_time),
                terminal_at=at_time
            WHERE task_id=downstream_row.task_id;
            PERFORM agent_control.runtime_insert_event(
                'task',downstream_row.task_id,'blocked','dead_lettered',
                downstream_row.state_generation+1,p_worker_principal,
                candidate.join_id,run_row.run_id,
                'task_graph_join_failed',at_time);
            UPDATE agent_control.runtime_session
            SET state='closed',generation=generation+1,closed_at=at_time
            WHERE session_id=downstream_session.session_id;
            PERFORM agent_control.runtime_insert_event(
                'session',downstream_session.session_id,'open','closed',
                downstream_session.generation+1,p_worker_principal,
                candidate.join_id,run_row.run_id,
                'task_graph_join_failed',at_time);
            IF parent_row.budget_slot_held THEN
                IF NOT agent_control.runtime_release_active_slot_ancestors(
                    run_row.run_id,parent_row.budget_ledger_id,at_time) THEN
                    RAISE EXCEPTION USING ERRCODE='40001',
                        MESSAGE='TaskGraph parent active slot changed';
                END IF;
            END IF;
            UPDATE agent_control.runtime_task
            SET state='dead_lettered',
                state_generation=state_generation+1,
                budget_slot_held=false,
                updated_at=greatest(updated_at,at_time),
                terminal_at=at_time
            WHERE task_id=parent_row.task_id;
            PERFORM agent_control.runtime_insert_event(
                'task',parent_row.task_id,'waiting','dead_lettered',
                parent_row.state_generation+1,p_worker_principal,
                candidate.join_id,run_row.run_id,
                'task_graph_join_failed',at_time);
            UPDATE agent_control.runtime_run
            SET state='dead_lettered',
                state_generation=state_generation+1,
                updated_at=greatest(updated_at,at_time),
                terminal_at=at_time
            WHERE run_id=run_row.run_id;
            PERFORM agent_control.runtime_insert_event(
                'run',run_row.run_id,run_row.state,'dead_lettered',
                run_row.state_generation+1,p_worker_principal,
                candidate.join_id,run_row.run_id,
                'task_graph_join_failed',at_time);
            UPDATE agent_control.cortex_task_graph_schedule
            SET state='closed',generation=generation+1,
                updated_at=greatest(updated_at,at_time),closed_at=at_time
            WHERE graph_id=candidate.graph_id AND active_tasks=0;
            failed_count:=failed_count+1;
        END IF;
    END LOOP;

    -- A succeeded Decision Desk node is the graph's user-facing Artifact.
    FOR candidate IN
        SELECT graph.graph_id,graph.run_id,graph.parent_task_id,
               node.task_id AS desk_task_id
        FROM agent_control.cortex_task_graph AS graph
        JOIN agent_control.cortex_task_graph_node AS node
          ON node.graph_id=graph.graph_id
         AND node.role_id='decision_desk'
        JOIN agent_control.runtime_task AS desk
          ON desk.task_id=node.task_id AND desk.state='succeeded'
        JOIN agent_control.runtime_task AS parent
          ON parent.task_id=graph.parent_task_id AND parent.state='waiting'
        JOIN agent_control.runtime_run AS run
          ON run.run_id=graph.run_id AND run.state IN ('running','waiting')
        ORDER BY graph.graph_id
        LIMIT 32
    LOOP
        at_time:=clock_timestamp();
        SELECT * INTO STRICT run_row FROM agent_control.runtime_run
        WHERE run_id=candidate.run_id FOR UPDATE;
        SELECT * INTO STRICT parent_row FROM agent_control.runtime_task
        WHERE task_id=candidate.parent_task_id FOR UPDATE;
        SELECT * INTO STRICT desk_row FROM agent_control.runtime_task
        WHERE task_id=candidate.desk_task_id FOR SHARE;
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
        UPDATE agent_control.runtime_run
        SET state='succeeded',state_generation=state_generation+1,
            updated_at=greatest(updated_at,at_time),terminal_at=at_time
        WHERE run_id=run_row.run_id;
        PERFORM agent_control.runtime_insert_event(
            'run',run_row.run_id,run_row.state,'succeeded',
            run_row.state_generation+1,p_worker_principal,
            candidate.graph_id,run_row.run_id,
            'task_graph_succeeded',at_time);
        UPDATE agent_control.cortex_task_graph_schedule
        SET state='closed',generation=generation+1,
            updated_at=greatest(updated_at,at_time),closed_at=at_time
        WHERE graph_id=candidate.graph_id AND active_tasks=0;
        completed_count:=completed_count+1;
    END LOOP;

    RETURN jsonb_build_object(
        'status','reconciled',
        'resolved_joins',resolved_count,
        'ready_joins',ready_count,
        'failed_joins',failed_count,
        'completed_graphs',completed_count
    );
END
$$;

REVOKE ALL ON TABLE
agent_control.cortex_task_graph_join_resolution FROM PUBLIC;
REVOKE ALL ON FUNCTION
agent_control.reconcile_cortex_task_graph_joins(TEXT) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION
agent_control.reconcile_cortex_task_graph_joins(TEXT)
TO alpheus_agent_control_api;

RESET ROLE;
