ALTER TABLE agent_query_job
  ADD COLUMN workflow TEXT NOT NULL DEFAULT 'scout'
    CHECK (workflow IN ('scout','team'));

CREATE INDEX agent_query_job_workflow_recent
  ON agent_query_job (workflow, created_at DESC);
