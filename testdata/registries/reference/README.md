# Reference registry

Multi-layer fixture exercising the first-class artifact types, every
visibility mode, and every adapter target. Phase 19's example artifact
registry per spec §11. Used by adapter conformance, end-to-end
scenarios, and integration tests that need realistic data.

Layers and their `.layer-config` visibility (§4.6):

- `org-defaults/` — public organization-wide content (`public`).
- `_shared/` — shared helpers visible to authenticated org members (`organization`).
- `team-finance/` — scoped to the `acme-finance` OIDC group (`groups`).
- `personal/` — alice's user-defined layer (`users: [alice]`).

A server pointed at this directory through `--layer-path` (§13.10) reads
each layer's `.layer-config` and applies the declared visibility. The
filesystem-source client (`podium sync`, §13.11.3) short-circuits
visibility to `true` and ignores the file.

Each layer contains a representative cross-section of the first-class
types: `skill`, `agent`, `context`, `command`, `rule`, `hook`, and
`mcp-server`. Bundled resources span Python scripts, Markdown
references, JSON schemas, and a binary blob.
