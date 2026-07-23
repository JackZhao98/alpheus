# TaskGraph v1 canonicalization

TaskGraph v1 uses `alpheus-c14n-v1`. Digests are calculated over the complete
JSON value without field removal, default insertion, array sorting, or string
normalization.

`TaskGraphPlan.Digest` uses the domain
`agent-platform.task-graph-plan.v1`. The enclosing generic contract digest uses
`agent-platform.contract.<contract_type>.v1`.

Node, edge, join, input-reference, tool-grant, and source arrays are ordered
data. A producer must emit its intended order and a consumer must not silently
reorder it before verification.
