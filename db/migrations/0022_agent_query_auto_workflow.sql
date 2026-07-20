ALTER TABLE agent_query_job
  DROP CONSTRAINT agent_query_job_workflow_check,
  ADD CONSTRAINT agent_query_job_workflow_check
    CHECK (workflow IN ('auto','scout','team'));
