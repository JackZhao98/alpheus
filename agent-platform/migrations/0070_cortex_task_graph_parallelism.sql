-- Enforce each admitted graph's own max_parallelism independently from the
-- broader RuntimePolicy Run ledger. State transitions, not process-local
-- counters, own slots so crashes and concurrent Worker lanes cannot overbook.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

CREATE TABLE agent_control.cortex_task_graph_schedule (
    graph_id TEXT PRIMARY KEY REFERENCES agent_control.cortex_task_graph(graph_id)
        DEFERRABLE INITIALLY DEFERRED,
    limit_parallelism BIGINT NOT NULL CHECK (
        limit_parallelism BETWEEN 1 AND 16
    ),
    active_tasks BIGINT NOT NULL DEFAULT 0 CHECK (
        active_tasks BETWEEN 0 AND limit_parallelism
    ),
    generation BIGINT NOT NULL DEFAULT 1 CHECK (generation>0),
    state TEXT NOT NULL DEFAULT 'open' CHECK (state IN ('open','closed')),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    closed_at TIMESTAMPTZ,
    CHECK (
        (state='open' AND closed_at IS NULL)
        OR
        (state='closed' AND active_tasks=0 AND closed_at IS NOT NULL)
    )
);

CREATE FUNCTION agent_control.guard_cortex_task_graph_schedule()
RETURNS TRIGGER LANGUAGE plpgsql
SET search_path=pg_catalog,agent_control SET timezone='UTC' AS $$
BEGIN
    IF TG_OP='DELETE'
       OR NEW.graph_id<>OLD.graph_id
       OR NEW.limit_parallelism<>OLD.limit_parallelism
       OR NEW.created_at<>OLD.created_at
       OR NEW.generation<>OLD.generation+1
       OR NEW.updated_at<OLD.updated_at
       OR (
         OLD.state='closed'
         AND ROW(NEW.active_tasks,NEW.state,NEW.closed_at)
             IS DISTINCT FROM
             ROW(OLD.active_tasks,OLD.state,OLD.closed_at)
       )
       OR (OLD.state='open' AND NEW.state NOT IN ('open','closed')) THEN
        RAISE EXCEPTION USING ERRCODE='55000',
            MESSAGE='invalid TaskGraph schedule mutation';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER cortex_task_graph_schedule_guard
BEFORE UPDATE OR DELETE ON agent_control.cortex_task_graph_schedule
FOR EACH ROW EXECUTE FUNCTION
agent_control.guard_cortex_task_graph_schedule();

CREATE FUNCTION agent_control.initialize_cortex_task_graph_schedule()
RETURNS TRIGGER LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control SET timezone='UTC' AS $$
BEGIN
    INSERT INTO agent_control.cortex_task_graph_schedule(
        graph_id,limit_parallelism,active_tasks,generation,state,
        created_at,updated_at
    ) VALUES(
        NEW.graph_id,
        (NEW.authorized_limit->>'max_parallelism')::BIGINT,
        0,1,'open',NEW.created_at,NEW.created_at
    );
    RETURN NULL;
END
$$;

CREATE TRIGGER cortex_task_graph_schedule_initialize
AFTER INSERT ON agent_control.cortex_task_graph
FOR EACH ROW EXECUTE FUNCTION
agent_control.initialize_cortex_task_graph_schedule();

CREATE FUNCTION agent_control.account_cortex_task_graph_parallelism()
RETURNS TRIGGER LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control SET timezone='UTC' AS $$
DECLARE
    graph_id_value TEXT;
    schedule_row agent_control.cortex_task_graph_schedule%ROWTYPE;
    at_time TIMESTAMPTZ:=clock_timestamp();
BEGIN
    IF OLD.state=NEW.state THEN RETURN NEW; END IF;
    SELECT node.graph_id INTO graph_id_value
    FROM agent_control.cortex_task_graph_node AS node
    WHERE node.task_id=NEW.task_id;
    IF NOT FOUND THEN RETURN NEW; END IF;

    SELECT * INTO STRICT schedule_row
    FROM agent_control.cortex_task_graph_schedule
    WHERE graph_id=graph_id_value
    FOR UPDATE;

    IF OLD.state<>'running' AND NEW.state='running' THEN
        IF schedule_row.state<>'open'
           OR schedule_row.active_tasks>=schedule_row.limit_parallelism THEN
            RAISE EXCEPTION USING ERRCODE='55000',
                MESSAGE='TaskGraph parallelism exhausted';
        END IF;
        UPDATE agent_control.cortex_task_graph_schedule
        SET active_tasks=active_tasks+1,
            generation=generation+1,
            updated_at=greatest(updated_at,at_time)
        WHERE graph_id=graph_id_value;
    ELSIF OLD.state='running' AND NEW.state<>'running' THEN
        IF schedule_row.active_tasks<1 THEN
            RAISE EXCEPTION USING ERRCODE='55000',
                MESSAGE='TaskGraph parallelism accounting underflow';
        END IF;
        UPDATE agent_control.cortex_task_graph_schedule
        SET active_tasks=active_tasks-1,
            generation=generation+1,
            updated_at=greatest(updated_at,at_time)
        WHERE graph_id=graph_id_value;
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER runtime_task_graph_parallelism
BEFORE UPDATE OF state ON agent_control.runtime_task
FOR EACH ROW EXECUTE FUNCTION
agent_control.account_cortex_task_graph_parallelism();

-- Discovery avoids work that is already known to have no graph slot. The
-- state-transition trigger above remains the atomic race winner.
DO $migration$
DECLARE
    definition TEXT;
    old_join TEXT:=E'  LEFT JOIN agent_control.cortex_task_graph AS graph\n    ON graph.graph_id=graph_node.graph_id\n  LEFT JOIN LATERAL (';
    new_join TEXT:=E'  LEFT JOIN agent_control.cortex_task_graph AS graph\n    ON graph.graph_id=graph_node.graph_id\n  LEFT JOIN agent_control.cortex_task_graph_schedule AS graph_schedule\n    ON graph_schedule.graph_id=graph.graph_id\n  LEFT JOIN LATERAL (';
    old_filter TEXT:=E'        graph_node.role_id<>''decision_desk''\n        AND graph_grant.tool_id IS NULL\n      )';
    new_filter TEXT:=E'        graph_node.role_id<>''decision_desk''\n        AND graph_grant.tool_id IS NULL\n        AND graph_schedule.state=''open''\n        AND graph_schedule.active_tasks<graph_schedule.limit_parallelism\n      )';
BEGIN
    SELECT pg_get_functiondef(
        'agent_control.next_cortex_task()'::regprocedure
    ) INTO definition;
    IF position(old_join IN definition)=0
       OR position(old_filter IN definition)=0 THEN
        RAISE EXCEPTION 'unexpected TaskGraph discovery definition';
    END IF;
    definition:=replace(definition,old_join,new_join);
    definition:=replace(definition,old_filter,new_filter);
    EXECUTE definition;
END
$migration$;

REVOKE ALL ON TABLE agent_control.cortex_task_graph_schedule FROM PUBLIC;
REVOKE ALL ON FUNCTION
    agent_control.guard_cortex_task_graph_schedule(),
    agent_control.initialize_cortex_task_graph_schedule(),
    agent_control.account_cortex_task_graph_parallelism()
FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.next_cortex_task() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agent_control.next_cortex_task()
TO alpheus_agent_worker;

RESET ROLE;
