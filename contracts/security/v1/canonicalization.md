# Security Pack Canonicalization

Security profile records use `alpheus-c14n-v1` from the common v1 pack. Only
absolute secret file paths are serializable. Secret bytes, tokens, passwords,
connection strings, and environment dumps are forbidden contract content.

Profile-set identity is order-sensitive. Deployment tooling must sort profiles
by `profile` before digesting a set; duplicate profiles, principals, database
roles, or secret paths fail semantic validation.
