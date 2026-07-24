-- Kernel-owned Console projection that permanently binds a user-visible
-- intraday session to its canonical Moody Blues replay. Source observations,
-- Cortex Runs, Candidates and Paper orders remain owned by their respective
-- systems; this table stores only the cross-system navigation boundary.
CREATE TABLE agent_intraday_session (
  session_id UUID PRIMARY KEY,
  subject TEXT NOT NULL CHECK (length(subject) BETWEEN 1 AND 200),
  environment TEXT NOT NULL CHECK (environment IN ('paper','live')),
  request_id TEXT NOT NULL CHECK (length(request_id) BETWEEN 1 AND 200),
  replay_id UUID NOT NULL UNIQUE,
  provider_id TEXT NOT NULL CHECK (provider_id='gexbot-classic'),
  symbol TEXT NOT NULL CHECK (symbol='SPX'),
  category TEXT NOT NULL CHECK (
    category IN ('gex_full','gex_zero','gex_one')
  ),
  start_available_at TIMESTAMPTZ NOT NULL,
  end_available_at TIMESTAMPTZ NOT NULL,
  as_of TIMESTAMPTZ NOT NULL,
  state TEXT NOT NULL CHECK (state IN ('active','complete','failed')),
  replay_generation BIGINT NOT NULL CHECK (replay_generation > 0),
  last_source_timestamp TIMESTAMPTZ,
  last_available_at TIMESTAMPTZ,
  latest_wake_run_id UUID,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  CHECK (start_available_at <= end_available_at),
  CHECK (end_available_at <= as_of),
  CHECK (created_at <= updated_at),
  UNIQUE (subject,request_id)
);

CREATE INDEX agent_intraday_session_subject_recent_idx
ON agent_intraday_session(subject,created_at DESC);

CREATE TABLE agent_intraday_session_event (
  event_id UUID PRIMARY KEY,
  session_id UUID NOT NULL REFERENCES agent_intraday_session(session_id),
  kind TEXT NOT NULL CHECK (kind IN ('created','frame')),
  replay_generation BIGINT NOT NULL CHECK (replay_generation > 0),
  run_id UUID,
  source_timestamp TIMESTAMPTZ,
  available_at TIMESTAMPTZ,
  payload JSONB NOT NULL CHECK (
    jsonb_typeof(payload)='object'
    AND octet_length(payload::TEXT) <= 65536
  ),
  occurred_at TIMESTAMPTZ NOT NULL,
  UNIQUE (session_id,kind,replay_generation)
);

CREATE OR REPLACE FUNCTION reject_agent_intraday_session_event_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'agent intraday session events are immutable';
END
$$;

CREATE TRIGGER agent_intraday_session_event_immutable
BEFORE UPDATE OR DELETE ON agent_intraday_session_event
FOR EACH ROW EXECUTE FUNCTION reject_agent_intraday_session_event_mutation();
