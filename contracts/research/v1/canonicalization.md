# Research v1 canonicalization

All externally supplied times are parsed as RFC3339 instants and converted to
UTC. `available_at` and `ingested_at` are assigned by the Research Provider;
callers cannot backdate availability. Symbols are uppercased, categories are
from the fixed Classic set, and raw content is hashed before it is staged.

The persisted observation body contains a BlobRef summary, not raw payload
bytes. The digest is the SHA-256 hex digest of PostgreSQL's canonical `jsonb`
body under the `research.gexbot_observation.v1` record shape. A retry with the
same observation identity must produce the same immutable body or fail.

An `as_of` lookup orders only by `available_at`, then `observed_at`, then the
immutable observation identifier. A replay cursor uses the same tuple and can
only move forward.
