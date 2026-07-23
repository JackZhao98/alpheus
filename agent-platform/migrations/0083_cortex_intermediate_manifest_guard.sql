-- The deferred manifest/Task consistency guard must recognize the same exact
-- intermediate-contract whitelist as model dispatch. Keeping this guard
-- stricter than dispatch would roll back an otherwise authorized Turn.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

CREATE OR REPLACE FUNCTION agent_control.validate_runtime_manifest_contract()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  matched_task_id TEXT;
BEGIN
  SELECT task.task_id INTO matched_task_id
  FROM agent_control.runtime_turn AS turn
  JOIN agent_control.runtime_task AS task
    ON task.task_id=turn.task_id
   AND task.run_id=turn.run_id
  WHERE turn.turn_id=NEW.turn_id
    AND turn.attempt_id=NEW.attempt_id
    AND (
      task.output_contract_digest=NEW.output_contract_digest
      OR agent_control.cortex_intermediate_output_contract_allowed(
        task.task_id,NEW.attempt_id,
        NEW.output_contract_digest::TEXT)
    );
  IF matched_task_id IS NULL THEN
    RAISE EXCEPTION USING ERRCODE='23514',
      MESSAGE='model manifest output contract does not match turn task';
  END IF;
  RETURN NULL;
END
$$;

REVOKE ALL ON FUNCTION
  agent_control.validate_runtime_manifest_contract()
  FROM PUBLIC;

RESET ROLE;
