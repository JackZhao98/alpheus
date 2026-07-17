-- A rolling M2.8 database may already contain durable place attempts. Give
-- every one a canonical M2.9 order before the reconciler starts. Reuse the
-- attempt UUID as the stable order UUID; no extension or random SQL function
-- is required. Broker-observed rows restart at submitted and are reconciled
-- from the provider, while attempts that never recorded a broker id stay new.
INSERT INTO orders (
  id, operation_id, execution_attempt_id, broker_order_id, client_order_id,
  ledger, symbol, side, kind, multiplier, qty, limit_micros, state,
  created_at, updated_at
)
SELECT
  a.id,
  a.operation_id,
  a.id,
  a.broker_order_id,
  a.client_order_id,
  CASE WHEN COALESCE((o.payload->>'shadow')::boolean, false)
       THEN 'shadow' ELSE 'live' END,
  COALESCE(NULLIF(o.payload->>'symbol', ''), o.payload->>'underlying'),
  o.payload->>'side',
  o.payload->>'kind',
  (o.payload->>'multiplier')::bigint,
  a.qty,
  a.limit_micros,
  CASE WHEN a.broker_order_id IS NULL THEN 'new' ELSE 'submitted' END,
  a.created_at,
  COALESCE(a.resolved_at, a.claimed_at, a.created_at)
FROM execution_attempt a
JOIN operations o ON o.id = a.operation_id
WHERE a.intent = 'place';
