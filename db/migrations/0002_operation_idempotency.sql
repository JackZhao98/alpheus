ALTER TABLE operations
  ADD COLUMN authenticated_subject TEXT,
  ADD COLUMN idempotency_key TEXT,
  ADD COLUMN request_hash BYTEA;

ALTER TABLE operations
  ADD CONSTRAINT operations_idempotency_fields_complete CHECK (
    (authenticated_subject IS NULL AND idempotency_key IS NULL AND request_hash IS NULL)
    OR
    (authenticated_subject IS NOT NULL AND idempotency_key IS NOT NULL AND request_hash IS NOT NULL)
  ),
  ADD CONSTRAINT operations_request_hash_sha256 CHECK (
    request_hash IS NULL OR octet_length(request_hash) = 32
  );

CREATE UNIQUE INDEX operations_subject_idempotency
  ON operations (authenticated_subject, idempotency_key)
  WHERE authenticated_subject IS NOT NULL;
