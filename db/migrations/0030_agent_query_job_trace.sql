-- Durable, secret-free diagnostics for the read-only Agent Lab.  A trace row
-- records dispatcher state only: no prompt, model token, or runtime payload is
-- ever stored here.
CREATE TABLE agent_query_job_trace (
  sequence BIGSERIAL PRIMARY KEY,
  job_id UUID NOT NULL REFERENCES agent_query_job(id) ON DELETE CASCADE,
  attempt INTEGER NOT NULL CHECK (attempt >= 0),
  stage TEXT NOT NULL CHECK (stage IN (
    'submitted', 'claimed', 'credential_loaded', 'runtime_request_started',
    'runtime_response_received', 'runtime_request_failed', 'completed', 'failed'
  )),
  error_code TEXT CHECK (
    error_code IS NULL OR error_code ~ '^[a-z][a-z0-9_]{0,127}$'
  ),
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

CREATE INDEX agent_query_job_trace_recent
  ON agent_query_job_trace (job_id, sequence);
