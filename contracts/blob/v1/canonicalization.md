# Blob v1 canonicalization

Blob contracts use `alpheus-c14n-v1` and the blob contract type as the digest
domain. `BlobRef.content_digest` is the lowercase SHA-256 of raw bytes; the
contract digest is separately computed over canonical JSON.

A content digest is identity, not authority. Reads bind the current principal,
exact owning `RecordRef`, binding, BlobRef, current ACL, and retention decision.
Staging grants snapshot database policy; deployment configuration cannot widen
them. After streaming, computed facts are persisted before physical
materialization; only then may committed metadata make the bytes referenceable.
Media types use their lowercase canonical representation.
