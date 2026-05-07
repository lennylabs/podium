# 5. Meta-Tools

Podium exposes four meta-tools through the Podium MCP server. These are the only tools Podium contributes; hosts add their own runtime tools alongside.

| Tool               | Description                                                                                                                                                                                                                                                                                                                               |
| ------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `load_domain`      | Returns the map for a path: `load_domain()` (root), `load_domain("finance")` (domain), `load_domain("finance/close-reporting")` (subdomain). For the requested domain: resolved description (long-form prose body when present, else frontmatter summary), author-supplied keywords, and notable artifacts (`featured` + learn-from-usage signal; empty when neither applies). For each subdomain listed: short description only. Output shape governed by §4.5.5; optional `depth` arg overrides the configured default. Optional `session_id` arg. |
| `search_domains`   | Hybrid retrieval (BM25 + embeddings, RRF) over each domain's projection (frontmatter `description` + `keywords` + truncated body). Filters by `scope` (a path prefix to constrain). `top_k` defaults to 10, max 50. Returns ranked domain descriptors only — `path`, `name`, short `description`, `keywords`, score; no body, no subdomain list, no notable. Response includes `total_matched` so the caller knows when more results exist. To drill into a result, call `load_domain` with its path. Optional `session_id`. |
| `search_artifacts` | Hybrid retrieval (BM25 + embeddings, RRF) over artifact frontmatter. All args optional: `query` (free text), `type`, `tags`, `scope` (path prefix), `top_k` (default 10, max 50). When `query` is omitted, returns artifacts matching the filters in default order — useful for browsing all artifacts within a `scope`. Response includes `total_matched` so the caller knows when more results exist. Bodies stay at the registry until `load_artifact`. Optional `session_id`. |
| `load_artifact`    | Loads a specific artifact by ID and version. Returns the manifest content as the tool result; **materializes** any bundled resources to a host-configured path on the filesystem (atomic write via `.tmp` + rename; presigned URLs for large blobs). Args: `id`, optional `version`, optional `session_id`, optional `harness:` override. |

`load_domain`, `search_domains`, and `search_artifacts` round-trip through the registry on every call (no snapshot caching at session startup). Only `load_artifact` writes to the host filesystem, and only for the specific artifact requested. Programmatic consumers (SDK) can also call a non-MCP bulk variant of `load_artifact` — see §7.6.2.

The MCP server declares its capabilities in the MCP `initialize` response: `{tools: true, prompts: <conditional on prompt artifacts with expose_as_mcp_prompt: true>, sessionCorrelation: true}`.

**`mcp-server` artifacts are filtered out of the MCP bridge's results.** Hosts that consume Podium through the MCP bridge cannot connect to a discovered MCP server mid-session — Claude Desktop, Claude Code, Cursor, and similar harnesses fix their MCP server list at startup. Surfacing `mcp-server` registrations through `search_artifacts` or `load_artifact` from the bridge would only add planning noise. They remain visible through the SDK (which owns its MCP client and can connect dynamically) and through `podium sync` (which materializes them into the harness's on-disk config for the next launch).

## 5.0 Why Tools, Not Resources

MCP resources fit static lists and host-driven enumeration. Podium's catalog needs parameterized navigation (`load_domain` takes a path; `search_domains` and `search_artifacts` take queries and filters) and lazy materialization with side effects. Tools fit better.

Artifact bodies are also exposed as MCP resources for hosts that prefer that pattern (read-only mirror of `load_artifact`); the canonical interface remains the four meta-tools.

## 5.1 Meta-Tool Descriptions and Prompting Guidance

The strings below are the canonical tool descriptions exposed to the LLM via MCP. Hosts SHOULD use them verbatim unless customizing for a specific runtime.

### `load_domain`

> Browse the artifact catalog hierarchically. Call with no path to see top-level domains. Call with a path (e.g., "finance") to see that domain's subdomains and notable artifacts. Use this when you don't know what's available and need to explore. Returns a map; doesn't load any artifact's content. Optional `depth` arg requests a deeper map than the configured default. If the response had to be tightened to fit a token budget, it includes a short `note` describing what was reduced — pass an explicit `depth` to override, or call `search_artifacts(scope=<path>)` for more notable artifacts than what fit. To use an artifact you find here, call `load_artifact`.

### `search_domains`

> Search the catalog for relevant domains by query. Use this when you don't know the right domain path and want to find candidate neighborhoods to drill into — e.g., "vendor payments," "observability," "release management." Filters: `scope` (a domain path to constrain the search). Returns ranked domain descriptors only — `path`, `name`, short `description`, `keywords`, score. To explore one of the results, call `load_domain` with its path. Use `search_artifacts` instead when you're already in the right neighborhood and want a specific artifact.

### `search_artifacts`

> Search or browse the artifact catalog. Pass a `query` for relevance ranking when you know the topic but not the exact ID. Omit `query` and pass `scope` alone to browse all artifacts in a domain — useful when a domain's notable list and description don't tell you enough and you want to list everything. Filters: `type` (skill, agent, context, prompt), `tags`, `scope` (a domain path). Optional `top_k` (default 10, max 50). The response includes `total_matched`; when more results exist than were returned, narrow with filters, drill into a subdomain, or run a more specific query. Returns ranked descriptors only — no manifest bodies. To use a result, call `load_artifact` with its id.

### `load_artifact`

> Load a specific artifact by ID. Returns the manifest body and materializes any bundled resources (scripts, templates, schemas, etc.) onto the local filesystem at a configured path. Use this only when you've decided to actually use the artifact — loading is the expensive operation. The returned `materialized_at` paths are absolute and ready to use.

### Example system-prompt fragment

```
You have access to a catalog of authored skills and agents through the Podium meta-tools:
  - load_domain: explore the catalog hierarchically (browse).
  - search_domains: find candidate domain neighborhoods by query.
  - search_artifacts: find an artifact by query.
  - load_artifact: actually load and materialize an artifact for use.

Sessions start empty. Use search_domains when you don't know the right
neighborhood; load_domain to navigate within one; search_artifacts when you
know the neighborhood but not the exact artifact. Call load_artifact only when
you're ready to use the artifact — it's the operation that puts content in
your context.
```

## 5.2 Prompt Projection

When a `type: prompt` artifact is loaded with `expose_as_mcp_prompt: true` in frontmatter, the MCP server also exposes it via MCP's `prompts/get` so harnesses with slash-menu support can surface it directly to users. Opt-in.

The MCP tools declared in a loaded artifact's manifest (`mcpServers:`) are stored by Podium but registered by the host's runtime. Podium stores the declarations and exposes them via `load_artifact`; hosts decide whether and how to wire them up.
