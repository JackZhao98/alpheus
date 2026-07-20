-- The private Robinhood research credential is distinct from the production
-- MCP session and is used only by the read-only Research Gateway connector.
ALTER TABLE agent_secret DROP CONSTRAINT agent_secret_name_check;
ALTER TABLE agent_secret ADD CONSTRAINT agent_secret_name_check
  CHECK (name IN ('openai', 'brave', 'robinhood_research'));
