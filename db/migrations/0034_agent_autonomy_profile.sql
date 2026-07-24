-- Durable autonomy controls for each Agent Console environment. Live starts
-- fail-closed in observe; policy code decides which transitions are exposed.
CREATE TABLE agent_autonomy_profile (
  environment TEXT PRIMARY KEY CHECK (environment IN ('paper','live')),
  mode TEXT NOT NULL CHECK (mode IN ('observe','copilot','agentic')),
  generation BIGINT NOT NULL CHECK (generation > 0),
  updated_by TEXT NOT NULL CHECK (length(updated_by) BETWEEN 1 AND 200),
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  CHECK (created_at <= updated_at)
);

CREATE TABLE agent_autonomy_event (
  event_id UUID PRIMARY KEY,
  environment TEXT NOT NULL,
  generation BIGINT NOT NULL CHECK (generation > 0),
  from_mode TEXT CHECK (from_mode IN ('observe','copilot','agentic')),
  to_mode TEXT NOT NULL CHECK (to_mode IN ('observe','copilot','agentic')),
  updated_by TEXT NOT NULL CHECK (length(updated_by) BETWEEN 1 AND 200),
  occurred_at TIMESTAMPTZ NOT NULL,
  FOREIGN KEY (environment) REFERENCES agent_autonomy_profile(environment),
  UNIQUE (environment,generation)
);

CREATE OR REPLACE FUNCTION reject_agent_autonomy_event_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'agent autonomy events are immutable';
END
$$;

CREATE TRIGGER agent_autonomy_event_immutable
BEFORE UPDATE OR DELETE ON agent_autonomy_event
FOR EACH ROW EXECUTE FUNCTION reject_agent_autonomy_event_mutation();

WITH inserted AS (
  INSERT INTO agent_autonomy_profile (
    environment,mode,generation,updated_by,created_at,updated_at
  ) VALUES
    ('paper','observe',1,'system-bootstrap',clock_timestamp(),clock_timestamp()),
    ('live','observe',1,'system-bootstrap',clock_timestamp(),clock_timestamp())
  RETURNING environment,mode,generation,updated_by,created_at
)
INSERT INTO agent_autonomy_event (
  event_id,environment,generation,from_mode,to_mode,updated_by,occurred_at
)
SELECT gen_random_uuid(),environment,generation,NULL,mode,updated_by,created_at
FROM inserted;
