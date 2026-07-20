# Runtime v1 canonicalization

Runtime v1 uses `alpheus-c14n-v1` from the common contract pack. Contract
digests use the domain `agent-platform.contract.<contract_type>.v1`.

Unknown fields, duplicate keys, non-integer numbers, unknown enum values, and
semantically invalid state combinations fail closed. A retry retains the
original causal, idempotency, lease-generation, and request-digest bindings.
