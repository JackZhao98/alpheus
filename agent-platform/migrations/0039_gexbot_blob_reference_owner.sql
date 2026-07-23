-- Permit the Provider's immutable Observation record to retain its own raw
-- Blob. This is intentionally a new owner value, not a grant to the existing
-- Research Gateway writer role.
SET ROLE alpheus_agent_migrator;
ALTER TABLE blob.blob_reference DROP CONSTRAINT IF EXISTS blob_reference_reference_owner_check;
ALTER TABLE blob.blob_reference ADD CONSTRAINT blob_reference_reference_owner_check
  CHECK (reference_owner IN ('agent_control','research_gateway','gexbot_provider'));
RESET ROLE;
