# 5. Meta-Tools

Podium exposes three meta-tools through the Podium MCP server. These are the only tools Podium contributes; hosts add their own runtime tools alongside.

| Tool               | Description                                                                                                                                                                                                                                                                                                                               |
| ------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `load_domain`      | Returns the map for a path: `load_domain()` (root), `load_domain("finance")` (domain), `load_domain("finance/close-reporting")` (subdomain). Output groups artifacts by type, lists notable entries, includes vocabulary hints. Optional `session_id` arg.                                                                                |
| `search_artifacts` | Hybrid retrieval (BM25 + embeddings, RRF) over artifact frontmatter. Filters by `type`, `tags`, `scope`. Returns top N results with frontmatter and retrieval scores; bodies stay at the registry until `load_artifact`. Optional `session_id` arg.                                                                                       |
| `load_artifact`    | Loads a specific artifact by ID and version. Returns the manifest content as the tool result; **materializes** any bundled resources to a host-configured path on the filesystem (atomic write via `.tmp` + rename; presigned URLs for large blobs). Args: `id`, optional `version`, optional `session_id`, optional `harness:` override. |

`load_domain` and `search_artifacts` round-trip through the registry on every call (no snapshot caching at session startup). Only `load_artifact` writes to the host filesystem, and only for the specific artifact requested. Programmatic consumers (SDK) can also call a non-MCP bulk variant of `load_artifact` — see §7.6.2.

The MCP server declares its capabilities in the MCP `initialize` response: `{tools: true, prompts: <conditional on prompt artifacts with expose_as_mcp_prompt: true>, sessionCorrelation: true}`.

**`mcp-server` artifacts are filtered out of the MCP bridge's results.** Hosts that consume Podium through the MCP bridge cannot connect to a discovered MCP server mid-session — Claude Desktop, Claude Code, Cursor, and similar harnesses fix their MCP server list at startup. Surfacing `mcp-server` registrations through `search_artifacts` or `load_artifact` from the bridge would only add planning noise. They remain visible through the SDK (which owns its MCP client and can connect dynamically) and through `podium sync` (which materializes them into the harness's on-disk config for the next launch).

## 5.0 Why Tools, Not Resources

MCP resources fit static lists and host-driven enumeration. Podium's catalog needs parameterized navigation (`load_domain` takes a path; `search_artifacts` takes a query) and lazy materialization with side effects. Tools fit better.

Artifact bodies are also exposed as MCP resources for hosts that prefer that pattern (read-only mirror of `load_artifact`); the canonical interface remains the three meta-tools.

## 5.1 Meta-Tool Descriptions and Prompting Guidance

The strings below are the canonical tool descriptions exposed to the LLM via MCP. Hosts SHOULD use them verbatim unless customizing for a specific runtime.

### `load_domain`

> Browse the artifact catalog hierarchically. Call with no path to see top-level domains. Call with a path (e.g., "finance") to see that domain's subdomains and notable artifacts. Use this when you don't know what's available and need to explore. Returns a map; doesn't load any artifact's content. To use an artifact you find here, call `load_artifact`.

### `search_artifacts`

> Search the artifact catalog by query. Use this when you know roughly what you're looking for but not the exact artifact ID. Filters: `type` (skill, agent, context, prompt), `tags`, `scope` (a domain path to constrain the search). Returns ranked descriptors only — no manifest bodies. To use a result, call `load_artifact` with its id.

### `load_artifact`

> Load a specific artifact by ID. Returns the manifest body and materializes any bundled resources (scripts, templates, schemas, etc.) onto the local filesystem at a configured path. Use this only when you've decided to actually use the artifact — loading is the expensive operation. The returned `materialized_at` paths are absolute and ready to use.

### Example system-prompt fragment

```
You have access to a catalog of authored skills and agents through the Podium meta-tools:
  - load_domain: explore the catalog hierarchically.
  - search_artifacts: find an artifact by query.
  - load_artifact: actually load and materialize an artifact for use.

Sessions start empty. Call load_domain or search_artifacts when you need
capability you don't already have. Call load_artifact only when you're ready
to use the artifact — it's the operation that puts content in your context.
```

## 5.2 Prompt Projection

When a `type: prompt` artifact is loaded with `expose_as_mcp_prompt: true` in frontmatter, the MCP server also exposes it via MCP's `prompts/get` so harnesses with slash-menu support can surface it directly to users. Opt-in.

The MCP tools declared in a loaded artifact's manifest (`mcpServers:`) are stored by Podium but registered by the host's runtime. Podium stores the declarations and exposes them via `load_artifact`; hosts decide whether and how to wire them up.
