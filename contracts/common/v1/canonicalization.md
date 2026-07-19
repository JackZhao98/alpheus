# Alpheus Canonical JSON Profile v1

Profile identity: `alpheus-c14n-v1`.

The input is exactly one valid UTF-8 JSON value. Accepted values are `null`,
booleans, strings, arrays, objects, and base-10 integers. Duplicate keys,
floating-point syntax, exponent syntax, negative zero, invalid UTF-8, and
trailing values are invalid.

Object keys are ordered by their UTF-8 byte strings. Arrays retain input order.
Strings use UTF-8 directly except for JSON-required quotation/backslash escapes,
the short escapes `\b`, `\f`, `\n`, `\r`, and `\t`, and lowercase `\u00xx` for
the remaining control characters. Integers have no leading zero and retain no
positive sign.

The SHA-256 preimage is:

```text
alpheus-c14n-v1 + LF + digest_domain + LF + canonical_json
```

The digest is lowercase hexadecimal. Domains match
`^[a-z][a-z0-9._-]{0,127}$`. A profile or algorithm change is an identity
migration requiring explicit human review; it cannot silently replace v1.

The normative golden is also checked in at
`agent-platform/canonical/testdata/`. Its digest domain is
`agent-platform.contract.golden`.
