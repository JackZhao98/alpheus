SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

-- A Task can now own a new Desk continuation Session after its closed Intent
-- Session.  Session generation is therefore per Task lineage, not always one:
-- the first Session is 1 and each replacement starts immediately after the
-- latest closed Session generation.  State transitions inside each Session
-- remain guarded by the existing +1 transition trigger.
CREATE FUNCTION agent_control.guard_runtime_session_initial_insert()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE prior_generation BIGINT;
BEGIN
  IF NEW.state<>'open' OR NEW.generation<1 THEN
    RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='invalid initial runtime state';
  END IF;
  SELECT max(session.generation) INTO prior_generation
    FROM agent_control.runtime_session session WHERE session.task_id=NEW.task_id;
  IF prior_generation IS NULL THEN
    IF NEW.generation<>1 THEN
      RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='initial Session generation must be one';
    END IF;
  ELSIF NEW.generation<>prior_generation+1 THEN
    RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='continuation Session generation is not next';
  END IF;
  RETURN NEW;
END $$;

DROP TRIGGER runtime_session_initial_guard ON agent_control.runtime_session;
CREATE TRIGGER runtime_session_initial_guard
BEFORE INSERT ON agent_control.runtime_session
FOR EACH ROW EXECUTE FUNCTION agent_control.guard_runtime_session_initial_insert();

REVOKE ALL ON FUNCTION agent_control.guard_runtime_session_initial_insert() FROM PUBLIC;
RESET ROLE;
