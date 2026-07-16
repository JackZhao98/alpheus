-- Append-only audit trail. Everything that happens lands here first.
CREATE TABLE events (
  id BIGSERIAL PRIMARY KEY,
  ts TIMESTAMPTZ NOT NULL DEFAULT now(),
  kind TEXT NOT NULL,              -- operation_proposed | verdict | order_update | fill | breaker | ...
  payload JSONB NOT NULL
);

-- Immutable trade operations (the only way risk enters or leaves the account).
CREATE TABLE operations (
  id UUID PRIMARY KEY,
  ts TIMESTAMPTZ NOT NULL DEFAULT now(),
  proposer TEXT NOT NULL,          -- role id
  class CHAR(1) NOT NULL,          -- A reduce-risk | B checklist-pass | C exception | D rule-change
  status TEXT NOT NULL,            -- auto_approved | pending_review | approved | rejected | executed | failed
  payload JSONB NOT NULL,          -- ProposedOperation schema
  verdict JSONB                    -- checklist results / reviewer rationale
);

CREATE TABLE orders (
  id UUID PRIMARY KEY,
  operation_id UUID REFERENCES operations(id),
  broker_order_id TEXT,
  state TEXT NOT NULL,             -- new | submitted | partially_filled | filled | cancelled | rejected | expired
  payload JSONB NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE fills (
  id BIGSERIAL PRIMARY KEY,
  order_id UUID REFERENCES orders(id),
  qty NUMERIC NOT NULL,
  price NUMERIC NOT NULL,
  ts TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Hypothesis is written at open; outcome at close. Prompt version ties trades to prompts.
CREATE TABLE journal (
  id BIGSERIAL PRIMARY KEY,
  operation_id UUID REFERENCES operations(id),
  hypothesis JSONB NOT NULL,       -- setup, thesis, invalidation, planned exits
  outcome JSONB,                   -- pnl, slippage, rule_compliance, error_tag
  prompt_versions JSONB,           -- {role: version} snapshot at decision time
  shadow BOOLEAN NOT NULL DEFAULT false,
  ts_open TIMESTAMPTZ NOT NULL DEFAULT now(),
  ts_close TIMESTAMPTZ
);

CREATE TABLE lessons (
  id BIGSERIAL PRIMARY KEY,
  ts TIMESTAMPTZ NOT NULL DEFAULT now(),
  text TEXT NOT NULL,
  confidence NUMERIC NOT NULL,     -- 0..1
  applicable_when TEXT,            -- injection filter
  source_journal_id BIGINT REFERENCES journal(id),
  expires_at TIMESTAMPTZ
);

-- One shared operating picture per market day, schema'd JSON.
CREATE TABLE blackboard (
  day DATE PRIMARY KEY,
  doc JSONB NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
