-- System-scoped Agent service credentials. Values are encrypted by the
-- Kernel before they cross the database boundary; PostgreSQL never receives
-- plaintext credentials or the process-owned wrapping-key material.
CREATE TABLE agent_secret (
  name TEXT PRIMARY KEY CHECK (name IN ('openai', 'brave')),
  ciphertext BYTEA NOT NULL CHECK (octet_length(ciphertext) BETWEEN 30 AND 4096),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
