# Governance v1 canonicalization

Governance records use `alpheus-c14n-v1` and the domain
`agent-platform.contract.<record_type>.v1`. A head binds the exact immutable
revision digest and generation. Receipt and revision digests are lowercase
SHA-256. Identity, canonicalization, or enum changes require an explicit human
review and a new schema revision.
