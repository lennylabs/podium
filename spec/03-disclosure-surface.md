# 3. Disclosure Surface

## 3.1 The Problem

Capability saturation: tool-call accuracy starts to degrade past ~50–100 tools in a single system prompt and falls off sharply past ~200 (figures vary by model and task). For larger catalogs, discovery has to be staged.

## 3.2 Three Disclosure Layers

The host sees only what it asks for, in stages. The three layers map 1:1 to the three meta-tools.

### Layer 1 — Hierarchical map (`load_domain`)

The host calls `load_domain(path)` to get a map of what exists. With no path, the map describes top-level domains. With a path like `finance`, it describes that domain's subdomains and key artifacts. The hierarchy is two levels deep by default — a third level kicks in only when a domain crosses ~1000 artifacts. The directory layout drives the domain hierarchy (§4.2); a domain's children may be augmented or curated by an optional `DOMAIN.md` config that imports artifacts from elsewhere (§4.5). Multi-membership is allowed: one artifact can show up under more than one domain via imports.

### Layer 2 — Search (`search_artifacts`)

When the host has the right neighborhood but doesn't know which artifact, it calls `search_artifacts(query, scope?)`. The registry runs a hybrid retriever (BM25 + embeddings, fused via reciprocal rank) over manifest text, returning a ranked list of `(artifact_id, summary, score)` tuples. Search returns descriptors only.

### Layer 3 — Load (`load_artifact`)

When the host has chosen an artifact, it calls `load_artifact(artifact_id)`. The registry returns the manifest body inline; bundled resources are materialized lazily on the host's filesystem and large blobs are delivered via presigned URLs.

## 3.3 Three Enabling Concerns

The disclosure surface only works if three other things hold.

**Visibility filtering.** Every request to the registry carries the host's OAuth identity. The registry composes the caller's effective view from the configured layer list (§4.6), filtering by each layer's visibility declaration. This is gatekeeping, not disclosure — it bounds what the disclosure surface can reveal.

**Description quality.** Layers 1 and 2 only work if manifests describe themselves well. Each artifact's `description` field must answer "when should I use this?" in one or two sentences. The registry lints for thin descriptions and flags clusters of artifacts whose summaries collide.

**Learn-from-usage reranking.** The registry observes which artifacts actually get loaded after which queries (correlated within a `session_id` — see §5), and uses that signal to (a) rerank search results, (b) suggest import candidates to domain owners, and (c) flag artifacts whose authored descriptions underperform retrieval expectations.

## 3.4 Discovery Flow

A typical host session begins empty. The host calls `load_domain()` to get the top-level map. It either picks a domain and calls `load_domain("<domain>")` for the next level, or — if the request is specific enough — jumps straight to `search_artifacts`. When it has an artifact ID, it calls `load_artifact`, which materializes the package on the host (§6.6).

Only `load_artifact` writes to the host filesystem. The catalog lives at the registry; the working set lives on the host.

## 3.5 Scope Preview (Pre-Session)

The disclosure layers above describe what an agent can see _during_ a session. Reviewers (security, compliance, the agent's user themself) sometimes need a summary of what's visible _before_ a session starts — both to set expectations and to satisfy audit asks of the form "what could this agent have loaded?"

`Client.preview_scope()` (and the corresponding `GET /v1/scope/preview` HTTP endpoint) returns aggregated metadata for the calling identity's effective view, with no manifest bodies and no resource transfers:

```python
preview = client.preview_scope()
# {
#   "layers": ["admin-finance", "joan-personal", "workspace-overlay"],
#   "artifact_count": 1234,
#   "by_type": {"skill": 800, "agent": 200, "context": 200, "prompt": 30, "mcp-server": 4},
#   "by_sensitivity": {"low": 1100, "medium": 100, "high": 34}
# }
```

The caller's OAuth identity drives layer composition exactly as for a real session; the preview is a read-only projection of that composition with counts only.

**Tenant flag.** Aggregate counts can hint at the existence of restricted content even when no individual artifact is leaked. The endpoint is gated by tenant config:

```yaml
tenant:
  expose_scope_preview: true # default
```

When `false`, the endpoint returns `403 scope_preview_disabled`. When `true`, the endpoint always returns aggregate counts only — never identifiers, descriptions, or any per-artifact metadata.

**Honored by all consumer paths.** The MCP server, SDK, and `podium sync` all expose this preview. The `podium status` CLI surfaces the same data for human inspection.

The preview is a transparency surface, not a discovery surface. Agents do not call it during a session — they use the disclosure layers in §3.2 — and it does not contribute to ranking, history, or any session-level state.
