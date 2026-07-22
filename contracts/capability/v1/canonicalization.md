# Capability canonicalization v1

All boundary records use `alpheus-c14n-v1`: strict JSON, no duplicate or
unknown fields, UTF-8, UTC timestamps, lower-case SHA-256 hexadecimal digests,
and a domain-separated digest. Evidence text is untrusted source material.
