# Reference registry

Multi-layer fixture exercising every first-class artifact type, every
visibility mode, and every adapter target. Phase 19's example artifact
registry per spec §11. Used by adapter conformance, end-to-end
scenarios, and integration tests that need realistic data.

Layers:

- `org-defaults/` — public organization-wide content.
- `team-finance/` — visibility scoped to OIDC group `acme-finance`.
- `personal/` — joan's user-defined layer (visibility: `users:[joan]`).

Each layer contains a representative cross-section of the seven
first-class types: `skill`, `agent`, `context`, `command`, `rule`,
`hook`, `mcp-server`. Bundled resources span Python scripts, Markdown
references, JSON schemas, and a binary blob.
