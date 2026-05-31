package main

// This file carries the canonical §5.1 meta-tool descriptions, their
// MCP inputSchema declarations, and the example system-prompt fragment.
// The strings are copied verbatim from spec/05-meta-tools.md §5.1 so the
// bridge emits them unchanged. §5.1 states the descriptions "are the
// canonical tool descriptions exposed to the LLM via MCP" and that hosts
// "SHOULD use them verbatim unless customizing for a specific runtime."

// spec: §5.1 — canonical `load_domain` description, verbatim.
const descLoadDomain = "Browse the artifact catalog hierarchically. Call with no path to see top-level domains. Call with a path (e.g., \"finance\") to see that domain's subdomains and notable artifacts. Use this when you don't know what's available and need to explore. Returns a map; doesn't load any artifact's content. Optional `depth` arg requests a deeper map than the configured default. If the response had to be tightened to fit a token budget, it includes a short `note` describing what was reduced. Pass an explicit `depth` to override, or call `search_artifacts(scope=<path>)` for more notable artifacts than what fit. To use an artifact you find here, call `load_artifact`."

// spec: §5.1 — canonical `search_domains` description, verbatim.
const descSearchDomains = "Search the catalog for relevant domains by query. Use this when you don't know the right domain path and want to find candidate neighborhoods to drill into, e.g., \"vendor payments,\" \"observability,\" \"release management.\" Filters: `scope` (a domain path to constrain the search). Returns ranked domain descriptors only: `path`, `name`, short `description`, `keywords`, score. To explore one of the results, call `load_domain` with its path. Use `search_artifacts` instead when you're already in the right neighborhood and want a specific artifact."

// spec: §5.1 — canonical `search_artifacts` description, verbatim.
const descSearchArtifacts = "Search or browse the artifact catalog. Pass a `query` for relevance ranking when you know the topic but not the exact ID. Omit `query` and pass `scope` alone to browse all artifacts in a domain; useful when a domain's notable list and description don't tell you enough and you want to list everything. Filters: `type` (skill, agent, context, command, rule, hook), `tags`, `scope` (a domain path). Optional `top_k` (default 10, max 50). The response includes `total_matched`; when more results exist than were returned, narrow with filters, drill into a subdomain, or run a more specific query. Returns ranked descriptors only; no manifest bodies. To use a result, call `load_artifact` with its id."

// spec: §5.1 — canonical `load_artifact` description, verbatim.
const descLoadArtifact = "Load a specific artifact by ID. Returns the manifest body and materializes any bundled resources (scripts, templates, schemas, etc.) onto the local filesystem at a configured path. Use this only when you've decided to actually use the artifact; loading is the expensive operation. The returned `materialized_at` paths are absolute and ready to use."

// systemPromptFragment is the §5.1 "Example system-prompt fragment",
// verbatim. The bridge surfaces it through the MCP `initialize` result's
// optional `instructions` field so a host can add it to the model's
// system prompt without hardcoding the text. F-5.1.3.
const systemPromptFragment = `You have access to a catalog of authored skills and agents through the Podium meta-tools:
  - load_domain: explore the catalog hierarchically (browse).
  - search_domains: find candidate domain neighborhoods by query.
  - search_artifacts: find an artifact by query.
  - load_artifact: actually load and materialize an artifact for use.

Sessions start empty. Use search_domains when you don't know the right
neighborhood; load_domain to navigate within one; search_artifacts when you
know the neighborhood but not the exact artifact. Call load_artifact only when
you're ready to use the artifact; it's the operation that puts content in
your context.`

// metaToolDescriptors returns the `tools/list` entries for the four §5
// meta-tools plus the §13.9 health tool. Each meta-tool carries its
// verbatim §5.1 description (F-5.1.1) and an `inputSchema` declaring the
// documented parameters so a compliant MCP client and the LLM have
// machine-readable parameter information (F-5.1.2). The parameter names
// and required fields mirror the arguments the per-tool handlers parse in
// callTool and §5/§5.1.
func metaToolDescriptors() []map[string]any {
	sessionID := map[string]any{"type": "string", "description": "Correlates this call with the agent session (§3.3/§4.7.6). The bridge supplies one per process; an explicit value overrides it for this call."}
	return []map[string]any{
		{
			"name":        "load_domain",
			"description": descLoadDomain,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":       map[string]any{"type": "string", "description": "Domain path to map (e.g., \"finance\" or \"finance/close-reporting\"). Omit for the top-level map."},
					"depth":      map[string]any{"type": "integer", "description": "Requests a deeper map than the configured default."},
					"session_id": sessionID,
				},
			},
		},
		{
			"name":        "search_domains",
			"description": descSearchDomains,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query":      map[string]any{"type": "string", "description": "Free-text query to rank domains by."},
					"scope":      map[string]any{"type": "string", "description": "Domain path prefix to constrain the search."},
					"top_k":      map[string]any{"type": "integer", "description": "Maximum results (default 10, max 50)."},
					"session_id": sessionID,
				},
			},
		},
		{
			"name":        "search_artifacts",
			"description": descSearchArtifacts,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query":      map[string]any{"type": "string", "description": "Free-text query for relevance ranking. Omit to browse by filter."},
					"type":       map[string]any{"type": "string", "description": "Artifact type filter: skill, agent, context, command, rule, or hook."},
					"tags":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Tag filters."},
					"scope":      map[string]any{"type": "string", "description": "Domain path prefix to constrain the search."},
					"top_k":      map[string]any{"type": "integer", "description": "Maximum results (default 10, max 50)."},
					"session_id": sessionID,
				},
			},
		},
		{
			"name":        "load_artifact",
			"description": descLoadArtifact,
			// §6.2 / §6.6: the host may supply the materialization
			// destination per call via `destination`, overriding
			// PODIUM_MATERIALIZE_ROOT for that call.
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":          map[string]any{"type": "string", "description": "Artifact ID to load."},
					"version":     map[string]any{"type": "string", "description": "Semver or \"latest\" (default)."},
					"harness":     map[string]any{"type": "string", "description": "Harness adapter override for this call."},
					"session_id":  sessionID,
					"destination": map[string]any{"type": "string", "description": "Materialization root for this call (overrides PODIUM_MATERIALIZE_ROOT)."},
				},
				"required": []string{"id"},
			},
		},
		{
			"name":        "health",
			"description": "Report registry connectivity, observed mode, cache size, and last successful call.",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
		},
		{
			// §3.5 transparency affordance. Aggregate counts only (no bodies,
			// no per-artifact metadata) for the caller's effective view, so an
			// operator or reviewer can answer "what could this identity have
			// loaded?" before a session. Agents do not call it during a
			// session; it is not a discovery surface.
			"name":        "scope_preview",
			"description": "Report aggregate counts for the caller's effective view: total artifacts, counts by type, and counts by sensitivity. Transparency affordance for operators and reviewers; not a discovery surface.",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
		},
	}
}
