-- B0-3: bind each pre-effect manifest to the active DB policy and the exact
-- local resource snapshot used for aggregate-capacity evaluation.  Columns
-- are nullable only so an already-applied 0015 can be upgraded safely; the
-- live send transition accepts only fully evaluated manifests.
ALTER TABLE execution_pre_effect_manifest
  ADD COLUMN ledger TEXT CHECK (ledger IN ('live')),
  ADD COLUMN active_kernel_policy_revision_id BIGINT REFERENCES kernel_policy_revision(id),
  ADD COLUMN active_kernel_policy_generation BIGINT CHECK (active_kernel_policy_generation > 0),
  ADD COLUMN active_kernel_policy_digest BYTEA
    CHECK (active_kernel_policy_digest IS NULL OR octet_length(active_kernel_policy_digest)=32),
  ADD COLUMN expected_local_open_risk_micros BIGINT CHECK (expected_local_open_risk_micros >= 0),
  ADD COLUMN expected_local_held_cash_micros BIGINT CHECK (expected_local_held_cash_micros >= 0),
  ADD COLUMN expected_other_close_qty BIGINT CHECK (expected_other_close_qty >= 0);

ALTER TABLE execution_pre_effect_manifest
  ADD CONSTRAINT execution_pre_effect_evaluation_complete CHECK (
    (ledger IS NULL
      AND active_kernel_policy_revision_id IS NULL
      AND active_kernel_policy_generation IS NULL
      AND active_kernel_policy_digest IS NULL
      AND expected_local_open_risk_micros IS NULL
      AND expected_local_held_cash_micros IS NULL
      AND expected_other_close_qty IS NULL)
    OR
    (ledger='live'
      AND active_kernel_policy_revision_id IS NOT NULL
      AND active_kernel_policy_generation IS NOT NULL
      AND active_kernel_policy_digest IS NOT NULL
      AND expected_local_open_risk_micros IS NOT NULL
      AND expected_local_held_cash_micros IS NOT NULL
      AND expected_other_close_qty IS NOT NULL)
  );
