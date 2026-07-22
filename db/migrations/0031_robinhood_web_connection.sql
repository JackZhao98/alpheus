-- The Robinhood MCP OAuth token is encrypted by Kernel before it reaches this
-- table. The selected account number lives inside the same ciphertext, so it
-- is never a container environment variable or plaintext database column.
ALTER TABLE agent_secret DROP CONSTRAINT agent_secret_name_check;
ALTER TABLE agent_secret ADD CONSTRAINT agent_secret_name_check
  CHECK (name IN ('openai', 'brave', 'gexbot', 'robinhood_research', 'robinhood_mcp'));

-- PKCE callback records are intentionally short lived. state_digest is a
-- SHA-256 digest of the browser-visible state; verifier_ciphertext is wrapped
-- by Kernel's process-owned secret root before this database ever sees it.
CREATE TABLE robinhood_oauth_flow (
  id UUID PRIMARY KEY,
  state_digest CHAR(64) UNIQUE NOT NULL CHECK (state_digest ~ '^[0-9a-f]{64}$'),
  verifier_ciphertext BYTEA NOT NULL CHECK (octet_length(verifier_ciphertext) BETWEEN 30 AND 2048),
  client_id TEXT NOT NULL CHECK (octet_length(client_id) BETWEEN 1 AND 512),
  redirect_uri TEXT NOT NULL CHECK (octet_length(redirect_uri) BETWEEN 1 AND 2048),
  subject TEXT NOT NULL CHECK (octet_length(subject) BETWEEN 1 AND 256),
  status TEXT NOT NULL CHECK (status IN ('pending', 'consumed', 'failed')) DEFAULT 'pending',
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  expires_at TIMESTAMPTZ NOT NULL,
  consumed_at TIMESTAMPTZ
);

CREATE INDEX robinhood_oauth_flow_expiry_idx ON robinhood_oauth_flow (expires_at)
  WHERE status = 'pending';
