-- Independent Agent Paper environment. This is not the legacy shadow ledger
-- and does not share balances, positions, or execution state with Robinhood.
CREATE TABLE agent_paper_account (
  account_id TEXT PRIMARY KEY CHECK (account_id ~ '^[a-z][a-z0-9_-]{0,63}$'),
  account_type TEXT NOT NULL CHECK (account_type = 'paper'),
  starting_cash_micros BIGINT NOT NULL CHECK (starting_cash_micros > 0),
  cash_micros BIGINT NOT NULL CHECK (cash_micros >= 0),
  buying_power_micros BIGINT NOT NULL CHECK (buying_power_micros >= 0),
  generation BIGINT NOT NULL CHECK (generation > 0),
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  CHECK (created_at <= updated_at)
);

CREATE TABLE agent_paper_position (
  account_id TEXT NOT NULL REFERENCES agent_paper_account(account_id),
  symbol TEXT NOT NULL CHECK (symbol ~ '^[A-Z][A-Z0-9._^-]{0,15}$'),
  kind TEXT NOT NULL CHECK (kind IN ('equity','option')),
  multiplier BIGINT NOT NULL CHECK (multiplier > 0),
  qty BIGINT NOT NULL CHECK (qty > 0),
  avg_price_micros BIGINT NOT NULL CHECK (avg_price_micros > 0),
  generation BIGINT NOT NULL CHECK (generation > 0),
  opened_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (account_id,symbol),
  CHECK (opened_at <= updated_at)
);

CREATE TABLE agent_paper_event (
  event_id UUID PRIMARY KEY,
  account_id TEXT NOT NULL REFERENCES agent_paper_account(account_id),
  generation BIGINT NOT NULL CHECK (generation > 0),
  event_type TEXT NOT NULL CHECK (
    event_type IN ('account_created','order_filled','position_closed','cash_adjusted')
  ),
  payload JSONB NOT NULL,
  occurred_at TIMESTAMPTZ NOT NULL,
  UNIQUE (account_id,generation)
);

CREATE OR REPLACE FUNCTION reject_agent_paper_event_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'agent paper events are immutable';
END
$$;

CREATE TRIGGER agent_paper_event_immutable
BEFORE UPDATE OR DELETE ON agent_paper_event
FOR EACH ROW EXECUTE FUNCTION reject_agent_paper_event_mutation();

WITH created AS (
  INSERT INTO agent_paper_account (
    account_id,account_type,starting_cash_micros,cash_micros,
    buying_power_micros,generation,created_at,updated_at
  ) VALUES (
    'agent-default','paper',100000000000,100000000000,
    100000000000,1,clock_timestamp(),clock_timestamp()
  )
  RETURNING account_id,generation,starting_cash_micros,created_at
)
INSERT INTO agent_paper_event (
  event_id,account_id,generation,event_type,payload,occurred_at
)
SELECT gen_random_uuid(),account_id,generation,'account_created',
       jsonb_build_object(
         'schema_revision',1,
         'starting_cash_micros',starting_cash_micros,
         'reason_code','agent_paper_environment_initialized'
       ),
       created_at
FROM created;
