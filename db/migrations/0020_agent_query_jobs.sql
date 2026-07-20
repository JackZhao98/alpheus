-- Read-only Agent Lab MVP queue. Credentials are intentionally absent: the
-- browser-supplied model token exists only for the lifetime of the dispatcher.
CREATE TABLE agent_query_job (
  id UUID PRIMARY KEY,
  authenticated_subject TEXT NOT NULL
    CHECK (char_length(btrim(authenticated_subject)) BETWEEN 1 AND 200),
  role TEXT NOT NULL DEFAULT 'scout' CHECK (role = 'scout'),
  symbol TEXT NOT NULL CHECK (symbol ~ '^[A-Z0-9.-]{1,16}$'),
  query TEXT NOT NULL CHECK (char_length(btrim(query)) BETWEEN 1 AND 4000),
  status TEXT NOT NULL CHECK (status IN ('queued','running','succeeded','failed')),
  result JSONB,
  error_code TEXT CHECK (error_code IS NULL OR error_code ~ '^[a-z0-9_]{1,80}$'),
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  CHECK (
    (status IN ('queued','running') AND result IS NULL AND error_code IS NULL)
    OR (status = 'succeeded' AND result IS NOT NULL AND error_code IS NULL)
    OR (status = 'failed' AND result IS NULL AND error_code IS NOT NULL)
  )
);

CREATE INDEX agent_query_job_subject_recent
  ON agent_query_job (authenticated_subject, created_at DESC);
