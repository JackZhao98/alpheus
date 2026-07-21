-- GEXBot Classic is a read-only research-data credential. Keep it within the
-- same encrypted secret store and explicit database allowlist as other agent
-- service credentials.
ALTER TABLE agent_secret DROP CONSTRAINT agent_secret_name_check;
ALTER TABLE agent_secret ADD CONSTRAINT agent_secret_name_check
  CHECK (name IN ('openai', 'brave', 'gexbot', 'robinhood_research'));
