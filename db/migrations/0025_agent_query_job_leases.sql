-- Recover the read-only Agent Lab queue after Kernel process loss. Existing
-- pre-lease running rows are returned to queued exactly once during migration.
ALTER TABLE agent_query_job
  ADD COLUMN attempt INTEGER NOT NULL DEFAULT 0 CHECK (attempt >= 0),
  ADD COLUMN claim_token UUID,
  ADD COLUMN lease_expires_at TIMESTAMPTZ;

UPDATE agent_query_job
SET status = 'queued', updated_at = clock_timestamp()
WHERE status = 'running';

ALTER TABLE agent_query_job
  ADD CONSTRAINT agent_query_job_claim_shape_check CHECK (
    (status = 'queued' AND claim_token IS NULL AND lease_expires_at IS NULL)
    OR (status = 'running' AND attempt > 0 AND claim_token IS NOT NULL AND lease_expires_at IS NOT NULL)
    OR (status IN ('succeeded','failed') AND claim_token IS NULL AND lease_expires_at IS NULL)
  );

CREATE INDEX agent_query_job_recovery
  ON agent_query_job (status, lease_expires_at, created_at)
  WHERE status IN ('queued','running');
