-- K1A: one typed, immutable Kernel policy document and one active head.
-- This is intentionally not a generic settings/KV service.
CREATE TABLE kernel_policy_revision (
  id BIGSERIAL PRIMARY KEY,
  schema_version SMALLINT NOT NULL CHECK (schema_version = 1),
  policy JSONB NOT NULL CHECK (jsonb_typeof(policy) = 'object'),
  digest BYTEA NOT NULL CHECK (octet_length(digest) = 32),
  recorded_by TEXT NOT NULL CHECK (char_length(btrim(recorded_by)) BETWEEN 1 AND 200),
  reason TEXT NOT NULL CHECK (char_length(btrim(reason)) BETWEEN 1 AND 1000),
  change_class TEXT NOT NULL CHECK (change_class IN ('initial','tighten','widen','mixed')),
  recorded_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),

  -- Application decoding owns the complete typed schema and rejects unknown
  -- fields. These checks independently protect the most critical ranges.
  CHECK ((policy #>> '{hard_limits,max_risk_per_trade_pct}')::numeric > 0
    AND (policy #>> '{hard_limits,max_risk_per_trade_pct}')::numeric <= 100),
  CHECK ((policy #>> '{hard_limits,max_total_open_risk_pct}')::numeric > 0
    AND (policy #>> '{hard_limits,max_total_open_risk_pct}')::numeric <= 100),
  CHECK ((policy #>> '{hard_limits,max_daily_loss_pct}')::numeric > 0
    AND (policy #>> '{hard_limits,max_daily_loss_pct}')::numeric <= 100),
  CHECK ((policy #>> '{hard_limits,max_new_trades_per_day}')::integer > 0),
  CHECK ((policy #>> '{hard_limits,consecutive_loss_days_halt}')::integer > 0),
  CHECK ((policy #>> '{instrument_rules,max_relative_spread}')::numeric BETWEEN 0 AND 1),
  CHECK ((policy #>> '{execution_policy,max_reprices}')::integer BETWEEN 0 AND 100),
  CHECK ((policy #>> '{execution_policy,reprice_interval_sec}')::integer BETWEEN 1 AND 86400),
  CHECK ((policy ->> 'quote_max_age_sec')::integer BETWEEN 1 AND 86400),
  CHECK ((policy ->> 'proposal_ttl_sec')::integer BETWEEN 1 AND 2592000)
);

CREATE TABLE kernel_policy_head (
  singleton BOOLEAN PRIMARY KEY DEFAULT true CHECK (singleton),
  revision_id BIGINT NOT NULL REFERENCES kernel_policy_revision(id),
  generation BIGINT NOT NULL CHECK (generation > 0),
  activated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  activated_by TEXT NOT NULL CHECK (char_length(btrim(activated_by)) BETWEEN 1 AND 200),
  reason TEXT NOT NULL CHECK (char_length(btrim(reason)) BETWEEN 1 AND 1000)
);

CREATE INDEX kernel_policy_revision_recorded_at
  ON kernel_policy_revision (recorded_at DESC, id DESC);

CREATE INDEX kernel_policy_activation_event_lookup
  ON events ((payload->>'revision_id'), (payload->>'generation'))
  WHERE kind='kernel_policy_activated';

CREATE FUNCTION reject_kernel_policy_revision_mutation() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'kernel policy revisions are immutable';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER kernel_policy_revision_immutable
  BEFORE UPDATE OR DELETE ON kernel_policy_revision
  FOR EACH ROW EXECUTE FUNCTION reject_kernel_policy_revision_mutation();
