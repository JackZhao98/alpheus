-- Closing a graph must also close the parked root Task's Session. Node
-- Sessions were already terminalized; leaving the parent Session open after a
-- succeeded or failed Run would preserve stale Worker authority.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

DO $migration$
DECLARE
    definition TEXT;
    original_definition TEXT;
    old_declaration TEXT :=
        '    downstream_session agent_control.runtime_session%ROWTYPE;' ||
        chr(10) || '    upstream RECORD;';
    new_declaration TEXT :=
        '    downstream_session agent_control.runtime_session%ROWTYPE;' ||
        chr(10) ||
        '    parent_session agent_control.runtime_session%ROWTYPE;' ||
        chr(10) || '    upstream RECORD;';
    first_parent_select TEXT:=$first_old$
        SELECT * INTO STRICT parent_row
        FROM agent_control.runtime_task
        WHERE task_id=graph_row.parent_task_id
        FOR UPDATE;

        PERFORM task.task_id
$first_old$;
    first_parent_replacement TEXT:=$first_new$
        SELECT * INTO STRICT parent_row
        FROM agent_control.runtime_task
        WHERE task_id=graph_row.parent_task_id
        FOR UPDATE;
        SELECT * INTO STRICT parent_session
        FROM agent_control.runtime_session
        WHERE session_id=parent_row.session_id
        FOR UPDATE;

        PERFORM task.task_id
$first_new$;
    completion_parent_select TEXT:=$completion_old$
        SELECT * INTO STRICT parent_row FROM agent_control.runtime_task
        WHERE task_id=candidate.parent_task_id FOR UPDATE;
        SELECT * INTO STRICT desk_row FROM agent_control.runtime_task
$completion_old$;
    completion_parent_replacement TEXT:=$completion_new$
        SELECT * INTO STRICT parent_row FROM agent_control.runtime_task
        WHERE task_id=candidate.parent_task_id FOR UPDATE;
        SELECT * INTO STRICT parent_session FROM agent_control.runtime_session
        WHERE session_id=parent_row.session_id FOR UPDATE;
        SELECT * INTO STRICT desk_row FROM agent_control.runtime_task
$completion_new$;
    failed_session_point TEXT:=$failed_old$
            PERFORM agent_control.runtime_insert_event(
                'session',downstream_session.session_id,'open','closed',
                downstream_session.generation+1,p_worker_principal,
                candidate.join_id,run_row.run_id,
                'task_graph_join_failed',at_time);
            IF parent_row.budget_slot_held THEN
$failed_old$;
    failed_session_replacement TEXT:=$failed_new$
            PERFORM agent_control.runtime_insert_event(
                'session',downstream_session.session_id,'open','closed',
                downstream_session.generation+1,p_worker_principal,
                candidate.join_id,run_row.run_id,
                'task_graph_join_failed',at_time);
            IF parent_session.state='open' THEN
                UPDATE agent_control.runtime_session
                SET state='closed',generation=generation+1,
                    closed_at=at_time
                WHERE session_id=parent_session.session_id;
                PERFORM agent_control.runtime_insert_event(
                    'session',parent_session.session_id,'open','closed',
                    parent_session.generation+1,p_worker_principal,
                    candidate.join_id,run_row.run_id,
                    'task_graph_join_failed',at_time);
            END IF;
            IF parent_row.budget_slot_held THEN
$failed_new$;
    success_session_point TEXT:=$success_old$
        PERFORM agent_control.runtime_insert_event(
            'task',parent_row.task_id,'waiting','superseded',
            parent_row.state_generation+1,p_worker_principal,
            candidate.graph_id,run_row.run_id,
            'task_graph_result_promoted',at_time);
        UPDATE agent_control.runtime_run
$success_old$;
    success_session_replacement TEXT:=$success_new$
        PERFORM agent_control.runtime_insert_event(
            'task',parent_row.task_id,'waiting','superseded',
            parent_row.state_generation+1,p_worker_principal,
            candidate.graph_id,run_row.run_id,
            'task_graph_result_promoted',at_time);
        IF parent_session.state='open' THEN
            UPDATE agent_control.runtime_session
            SET state='closed',generation=generation+1,closed_at=at_time
            WHERE session_id=parent_session.session_id;
            PERFORM agent_control.runtime_insert_event(
                'session',parent_session.session_id,'open','closed',
                parent_session.generation+1,p_worker_principal,
                candidate.graph_id,run_row.run_id,
                'task_graph_result_promoted',at_time);
        END IF;
        UPDATE agent_control.runtime_run
$success_new$;
BEGIN
    definition:=pg_get_functiondef(
        'agent_control.reconcile_cortex_task_graph_joins(text)'::REGPROCEDURE);
    original_definition:=definition;
    definition:=replace(definition,old_declaration,new_declaration);
    definition:=replace(
        definition,first_parent_select,first_parent_replacement);
    definition:=replace(
        definition,completion_parent_select,completion_parent_replacement);
    definition:=replace(
        definition,failed_session_point,failed_session_replacement);
    definition:=replace(
        definition,success_session_point,success_session_replacement);
    IF definition=original_definition
       OR position(new_declaration IN definition)=0
       OR position(first_parent_replacement IN definition)=0
       OR position(completion_parent_replacement IN definition)=0
       OR position(failed_session_replacement IN definition)=0
       OR position(success_session_replacement IN definition)=0 THEN
        RAISE EXCEPTION 'Unexpected TaskGraph parent Session definition';
    END IF;
    EXECUTE definition;
END
$migration$;

REVOKE ALL ON FUNCTION
agent_control.reconcile_cortex_task_graph_joins(TEXT) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION
agent_control.reconcile_cortex_task_graph_joins(TEXT)
TO alpheus_agent_control_api;

RESET ROLE;
