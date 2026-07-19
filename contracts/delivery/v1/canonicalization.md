# Delivery Pack Canonicalization

Delivery records use `alpheus-c14n-v1`. Digest domains are
`agent-platform.contract.outbox_record.v1`,
`agent-platform.contract.inbox_receipt.v1`, and
`agent-platform.contract.quarantine_record.v1`.

Retries preserve event id, event digest, causation, correlation, owner, owner
sequence, destination/consumer identity, and schema revision. Lease tokens,
attempt count, delivery time, quarantine state, and replay generation are
transport state and never create a new logical event.
