-- An account owner may explicitly widen the canary without fabricating clean-
-- day attestations. Authority version 3 is reserved for this audited override;
-- normal version-2 widening continues to require completed-day evidence.
ALTER TABLE live_canary_revision
  DROP CONSTRAINT live_canary_authority_shape,
  ADD CONSTRAINT live_canary_authority_shape CHECK (
    (authority_version IS NULL
      AND recorded_by IS NULL AND reason IS NULL AND change_class IS NULL
      AND required_attestations = 0)
    OR
    (authority_version = 1
      AND char_length(btrim(recorded_by)) BETWEEN 1 AND 200
      AND char_length(btrim(reason)) BETWEEN 1 AND 1000
      AND change_class IN ('initial','tighten')
      AND required_attestations = 0)
    OR
    (authority_version = 2
      AND char_length(btrim(recorded_by)) BETWEEN 1 AND 200
      AND char_length(btrim(reason)) BETWEEN 1 AND 1000
      AND (
        (change_class IN ('initial','tighten') AND required_attestations = 0)
        OR (change_class = 'widen' AND required_attestations > 0)
      ))
    OR
    (authority_version = 3
      AND char_length(btrim(recorded_by)) BETWEEN 1 AND 200
      AND char_length(btrim(reason)) BETWEEN 1 AND 1000
      AND change_class = 'widen'
      AND required_attestations = 0)
  );
