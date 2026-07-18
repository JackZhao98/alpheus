ALTER TABLE execution_attempt
  ADD COLUMN intent_fingerprint BYTEA,
  ADD COLUMN provider_account_id TEXT,
  ADD COLUMN provider_intent JSONB,
  ADD COLUMN sent_at TIMESTAMPTZ,
  ADD COLUMN send_window_start TIMESTAMPTZ,
  ADD COLUMN send_window_end TIMESTAMPTZ,
  ADD COLUMN replay_count INTEGER NOT NULL DEFAULT 0,
  ADD COLUMN provider_error_code TEXT,
  ADD COLUMN candidate_broker_order_id UUID,
  ADD COLUMN candidate_observed_at TIMESTAMPTZ;

ALTER TABLE execution_attempt
  ADD CONSTRAINT execution_attempt_fingerprint_check
    CHECK (intent_fingerprint IS NULL OR octet_length(intent_fingerprint)=32),
  ADD CONSTRAINT execution_attempt_provider_intent_check
    CHECK ((intent_fingerprint IS NULL) = (provider_intent IS NULL) AND
           (provider_intent IS NULL OR jsonb_typeof(provider_intent)='object')),
  ADD CONSTRAINT execution_attempt_provider_account_check
    CHECK ((provider_account_id IS NULL) = (provider_intent IS NULL) AND
           (provider_account_id IS NULL OR btrim(provider_account_id)<>'')),
  ADD CONSTRAINT execution_attempt_send_window_check
    CHECK ((sent_at IS NULL) = (send_window_start IS NULL) AND
           (sent_at IS NULL) = (send_window_end IS NULL) AND
           (sent_at IS NULL OR (send_window_start <= sent_at AND sent_at <= send_window_end))),
  ADD CONSTRAINT execution_attempt_replay_count_check
    CHECK (replay_count >= 0 AND replay_count <= 1),
  ADD CONSTRAINT execution_attempt_provider_error_code_check
    CHECK (provider_error_code IS NULL OR
           (btrim(provider_error_code) <> '' AND length(provider_error_code) <= 64)),
  ADD CONSTRAINT execution_attempt_candidate_check
    CHECK ((candidate_broker_order_id IS NULL) = (candidate_observed_at IS NULL));

CREATE TABLE live_execution_gate (
  singleton BOOLEAN PRIMARY KEY DEFAULT true CHECK (singleton),
  active_attempt_id UUID UNIQUE REFERENCES execution_attempt(id),
  unknown_attempt_id UUID UNIQUE REFERENCES execution_attempt(id),
  active_since TIMESTAMPTZ,
  unknown_since TIMESTAMPTZ,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (active_attempt_id IS NULL OR unknown_attempt_id IS NULL),
  CHECK ((active_attempt_id IS NULL) = (active_since IS NULL)),
  CHECK ((unknown_attempt_id IS NULL) = (unknown_since IS NULL))
);

INSERT INTO live_execution_gate(singleton) VALUES (true);
