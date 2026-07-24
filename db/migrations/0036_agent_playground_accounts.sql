-- A replay is a Paper Strategy Playground experiment, not a view over the
-- shared default Paper account. Persist its frozen capital and detector set
-- and provision one isolated ledger account per Paper Session.
ALTER TABLE agent_intraday_session
ADD COLUMN paper_account_id TEXT,
ADD COLUMN initial_cash_micros BIGINT NOT NULL DEFAULT 100000000000
  CHECK (initial_cash_micros BETWEEN 1000000000 AND 10000000000000),
ADD COLUMN detector_ids TEXT[] NOT NULL DEFAULT '{}'::TEXT[]
  CHECK (cardinality(detector_ids) <= 32);

WITH accounts AS (
  SELECT
    'playground-'||replace(session_id::TEXT,'-','') AS account_id,
    initial_cash_micros,
    created_at
  FROM agent_intraday_session
  WHERE environment='paper'
), inserted AS (
  INSERT INTO agent_paper_account (
    account_id,account_type,starting_cash_micros,cash_micros,
    buying_power_micros,generation,created_at,updated_at
  )
  SELECT
    account_id,'paper',initial_cash_micros,initial_cash_micros,
    initial_cash_micros,1,created_at,created_at
  FROM accounts
  ON CONFLICT (account_id) DO NOTHING
  RETURNING account_id,generation,starting_cash_micros,created_at
)
INSERT INTO agent_paper_event (
  event_id,account_id,generation,event_type,payload,occurred_at
)
SELECT gen_random_uuid(),account_id,generation,'account_created',
       jsonb_build_object(
         'schema_revision',1,
         'starting_cash_micros',starting_cash_micros,
         'reason_code','strategy_playground_initialized'
       ),
       created_at
FROM inserted;

UPDATE agent_intraday_session
SET paper_account_id=
  'playground-'||replace(session_id::TEXT,'-','')
WHERE environment='paper' AND paper_account_id IS NULL;

ALTER TABLE agent_intraday_session
ADD CONSTRAINT agent_intraday_session_paper_account_fk
FOREIGN KEY (paper_account_id)
REFERENCES agent_paper_account(account_id),
ADD CONSTRAINT agent_intraday_session_playground_boundary
CHECK (
  (environment='paper' AND paper_account_id IS NOT NULL)
  OR
  (environment='live' AND paper_account_id IS NULL)
);

CREATE UNIQUE INDEX agent_intraday_session_paper_account_idx
ON agent_intraday_session(paper_account_id)
WHERE paper_account_id IS NOT NULL;
