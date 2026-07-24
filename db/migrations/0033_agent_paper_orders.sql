-- Exactly-once Paper orders and fills for the independent Agent environment.
-- The quote used for each deterministic fill is retained with the order.
CREATE TABLE agent_paper_order (
  order_id UUID PRIMARY KEY,
  account_id TEXT NOT NULL REFERENCES agent_paper_account(account_id),
  idempotency_key TEXT NOT NULL CHECK (
    length(idempotency_key) BETWEEN 1 AND 200
    AND idempotency_key !~ '[[:space:][:cntrl:]]'
  ),
  request_hash BYTEA NOT NULL CHECK (octet_length(request_hash) = 32),
  actor_kind TEXT NOT NULL CHECK (actor_kind IN ('user','agent','trigger')),
  actor_id TEXT NOT NULL CHECK (length(actor_id) BETWEEN 1 AND 200),
  symbol TEXT NOT NULL CHECK (symbol ~ '^[A-Z][A-Z0-9._^-]{0,15}$'),
  kind TEXT NOT NULL CHECK (kind IN ('equity','option')),
  side TEXT NOT NULL CHECK (side IN ('buy','sell')),
  multiplier BIGINT NOT NULL CHECK (multiplier > 0),
  qty BIGINT NOT NULL CHECK (qty > 0),
  fill_price_micros BIGINT NOT NULL CHECK (fill_price_micros > 0),
  notional_micros BIGINT NOT NULL CHECK (notional_micros > 0),
  quote_bid_micros BIGINT NOT NULL CHECK (quote_bid_micros > 0),
  quote_ask_micros BIGINT NOT NULL CHECK (quote_ask_micros > quote_bid_micros),
  quote_source TEXT NOT NULL CHECK (length(quote_source) BETWEEN 1 AND 100),
  quote_observed_at TIMESTAMPTZ NOT NULL,
  state TEXT NOT NULL CHECK (state = 'filled'),
  generation BIGINT NOT NULL CHECK (generation > 1),
  created_at TIMESTAMPTZ NOT NULL,
  filled_at TIMESTAMPTZ NOT NULL,
  UNIQUE (account_id,idempotency_key),
  CHECK (created_at <= filled_at),
  CHECK (
    (side = 'buy' AND fill_price_micros = quote_ask_micros)
    OR (side = 'sell' AND fill_price_micros = quote_bid_micros)
  )
);

CREATE INDEX agent_paper_order_activity_idx
  ON agent_paper_order (account_id,filled_at DESC,order_id DESC);

CREATE OR REPLACE FUNCTION reject_agent_paper_order_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'agent paper orders are immutable';
END
$$;

CREATE TRIGGER agent_paper_order_immutable
BEFORE UPDATE OR DELETE ON agent_paper_order
FOR EACH ROW EXECUTE FUNCTION reject_agent_paper_order_mutation();
