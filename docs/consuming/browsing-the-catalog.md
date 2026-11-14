---
title: Browsing the catalog
nav_order: 3
description: Progressive discovery. How an agent traverses domains and finds artifacts at runtime via load_domain, search_domains, search_artifacts, and load_artifact, and what each call costs.
---

# Browsing the catalog

This page describes progressive discovery: how an agent finds and loads artifacts
from the registry during a session. It is for developers who connect a harness
through the Podium MCP server, and for authors who want their artifacts to be
found.

The Podium MCP server exposes a set of meta-tools to harnesses that speak MCP.
The agent calls them mid-session to traverse domains, find artifacts, and load
them one at a time. The session therefore starts with an empty context window
instead of the full catalog.

---

## How progressive discovery works

An artifact is a directory, and loading one materializes its bundled files along
with its manifest. Discovery is therefore lazy in both directions. The agent
pays for a domain map or a search before it pays for an artifact, and it pays for
an artifact's scripts and references only when it loads that artifact.

| Tool | Purpose |
|:--|:--|
| `load_domain(path?)` | Browse the catalog hierarchically. Returns a map of subdomains and notable artifacts. |
| `search_domains(query)` | Find candidate domain neighborhoods by query. |
| `search_artifacts(query?, scope?, type?, tags?)` | Find or browse artifacts by query and filters. |
| `load_artifact(id)` | Load a specific artifact's manifest and materialize bundled resources to disk. |

Discovery is staged. `load_domain` and the search tools are cheap, because they
return descriptors only. `load_artifact` is the expensive operation and the only
one that writes to the host filesystem.

![Discovery layers: each staged call returns less material than the one before. Layer 1 (map) lists domains and notable artifacts; layer 2 (search) returns ranked candidates; layer 3 (load) returns one artifact's manifest and materialized resources.](../assets/diagrams/disclosure-layers.svg)

<!--
ASCII fallback for the diagram above (discovery layers):

  layer 1 — map
    load_domain · search_domains
    Returns the catalog map: domains and notable artifacts.
    No artifact bodies, no resource bytes.
                  |
                  v  (host picks a domain)
  layer 2 — search
    search_artifacts
    Returns ranked (id, summary, score) within the chosen scope.
                  |
                  v  (host picks an artifact)
  layer 3 — load
    load_artifact
    Returns the manifest inline and materializes resources on the host.

  Visibility filtering bounds what each layer can reveal. The map
  and search layers are useful only when artifact descriptions and
  domain metadata are well written.
-->

The same operations are available to programmatic consumers through the SDK,
and SDK callers see the same wire format. This page covers the agent-mediated
MCP path. [Building custom consumers with the SDK](custom-via-sdk) covers the SDK.

The eager counterpart is `podium sync`, which declares a subset of the catalog up
front and writes it to disk before the session starts.
[Selective materialization](selective-materialization) covers that path.

---

## Discovery flow

A typical session begins empty and proceeds as follows:

1. **Cold start.** The agent calls `load_domain()` to get the top-level map, or
   `search_domains(query)` when it knows the topic but not the right
   neighborhood.
2. **Drill in.** From a domain, the agent drills further with
   `load_domain("<sub>")`, or jumps to
   `search_artifacts(query, scope="<domain>")` when the request is specific
   enough.
3. **Browse if unclear.** When the domain's description and notable list do not
   tell the agent enough, `search_artifacts(scope="<path>")` with no query lists
   everything in the domain.
4. **Load.** Once the agent has chosen an artifact, `load_artifact(id)`
   materializes it.

Only `load_artifact` writes to the host filesystem. The catalog lives at the
registry, and the working set lives on the host.

---

## `load_domain`

`load_domain` returns the map of one domain, or of the top level when called
with no path.

```
load_domain()                              → top-level domains
load_domain("finance")                     → finance's subdomains and notable artifacts
load_domain("finance/close-reporting")     → that subdomain's children
```

The response contains the following:

- The requested domain's resolved description. This is the long-form prose body
  when the domain has one, otherwise the frontmatter summary, otherwise a
  synthesized fallback.
- Author-supplied keywords for the domain.
- Notable artifacts, drawn from the author's `featured` list plus the
  learn-from-usage signal. The list is empty when neither applies.
- Subdomains, each with its short description.

Configurable rules per domain and per tenant govern the output rendering: depth,
folding, notable count, and response budget. The agent can pass `depth` to
override the configured default for one call.

When the renderer reduces the response, through token-budget tightening or a
depth cap, the response includes a `note` field describing what was reduced. The
agent then has these options:

- Pass an explicit `depth` to push past the reduction.
- Drill into a specific subdomain.
- Fall back to `search_artifacts(scope=<path>)` for more notable artifacts than
  fit.

The [Domains page](../authoring/domains) covers the `DOMAIN.md` schema for
authors: descriptions, keywords, featured artifacts, and discovery rendering
settings.

---

## `search_domains`

`search_domains` finds domains by query. An agent uses it when it does not know
which domain to start in. The agent picks a result and calls `load_domain` on its
path to drill in.

```
search_domains("vendor payments")
search_domains("observability", scope="platform")
```

The tool runs hybrid retrieval (BM25 and embeddings, fused through reciprocal
rank) over each domain's projection. The projection is the frontmatter
`description`, the `keywords`, and the truncated body. The response holds ranked
domain descriptors only: `path`, `name`, short `description`, `keywords`, and
score.

| Option | Effect |
|:--|:--|
| `scope` | Constrains results to a path prefix, for example only under `platform/`. |
| `top_k` | Result count cap. Default 10, max 50. |

The response includes `total_matched`, so the caller knows when more results
exist.

Two cases keep a domain out of the results:

- A domain without a `DOMAIN.md` is not embedded and does not appear in
  `search_domains` results. It is reachable through `load_domain` enumeration
  only.
- Visibility filtering applies. A domain whose `DOMAIN.md` lives only in a layer
  the caller cannot see does not surface.

---

## `search_artifacts`

`search_artifacts` finds or browses artifacts. It runs hybrid retrieval over
artifact frontmatter, and every argument is optional.

```
search_artifacts("variance analysis", type="skill")
search_artifacts(scope="finance/ap")                         # browse mode
search_artifacts(query="invoice approval", scope="finance/ap", type="skill")
```

![Hybrid retrieval: a query runs through BM25 and embedding similarity in parallel; results merge via reciprocal rank fusion; an optional learn-from-usage rerank produces the final ranking.](../assets/diagrams/hybrid-retrieval.svg)

<!--
ASCII fallback for the diagram above (hybrid retrieval):

  query (free text + filters)
       |
       +===> BM25 (lexical scoring over manifest text)        ===+
       |                                                         |==> RRF fusion ==> Rerank (optional) ==> ranked results
       +===> Embedding similarity (vector cosine over            +     (reciprocal       (learn-from-        (artifact_id,
              manifest projection)                                      rank across       usage; off          summary,
                                                                        the two lists)    by default)         score) tuples

  The same retriever runs over manifest text (search_artifacts)
  or domain metadata (search_domains). Visibility filtering
  applies to the candidate set before retrieval; hidden layers
  contribute nothing to the ranking.
-->


| Arg | Use |
|:--|:--|
| `query` | Free-text search target. Embedded and matched against the artifact projection (name, description, when_to_use, tags). |
| `type` | Filter by artifact type. |
| `tags` | Filter by tag. |
| `scope` | Constrain to a domain path prefix. |
| `top_k` | Result count cap. Default 10, max 50. |
| `session_id` | Optional UUID for `latest`-resolution consistency and learn-from-usage reranking. |

When `query` is omitted, the tool returns artifacts matching the filters in
default order. This is how an agent lists everything in a domain, when the
domain's notable list and description do not tell it enough.

The response holds ranked descriptors only. To use a result, call
`load_artifact` with its id.

The response includes `total_matched`, so the caller knows when more results
exist than were returned. When more exist, the agent can narrow with filters,
drill into a subdomain, or run a more specific query.

> [!NOTE]
> The SQL metadata stores persist neither the `name` nor the `when_to_use`
> column. Lexical matching therefore sees neither field, and a later
> `podium admin reembed` drops both from the vector as well.

---

## `load_artifact`

`load_artifact` loads a specific artifact by ID. It is the expensive operation in
the discovery flow, and the agent calls it only when it has decided to use the
artifact.

```
load_artifact("finance/close-reporting/run-variance-analysis")
load_artifact("finance/ap/pay-invoice", version="1.2.0")
```

The tool result carries the manifest body inline and the paths of the
materialized files. Every [bundled resource](../authoring/bundled-resources) in
the artifact's directory is written atomically to the host's filesystem at a
host-configured destination path. The agent reads a resource from its file
rather than from the response. Materialization runs through the configured
harness adapter, so the on-disk layout matches what the harness expects.

| Arg | Use |
|:--|:--|
| `id` | Required. The artifact ID. |
| `version` | Optional. Default is `latest`, resolved to the most recently ingested non-deprecated version visible to the caller. |
| `session_id` | Optional. The first `latest` lookup within a session is recorded and reused for all subsequent same-id lookups, so the host sees a consistent snapshot across the session. |
| `harness` | Optional. Per-call adapter override. Pass `none` to get the canonical layout regardless of the server's default `PODIUM_HARNESS`. |
| `destination` | Optional. Materialization root for this call, overriding the server's `PODIUM_MATERIALIZE_ROOT`. |

Manifest fields beyond the prose body determine what the consumer does with the
artifact next. These fields include hints, the sandbox profile, runtime
requirements, MCP server registrations, and dependency edges.
[Handling artifact responses](handling-artifact-responses) covers the
consumer-side action for each field.

### Resource transport

The MCP server fetches each resource from the registry. A resource at or below
the 256 KB cutoff arrives inline, and a larger one arrives through a presigned
URL. The cutoff is a transport detail the agent does not see.

A resource the registry holds inline arrives inline at any size. This includes
every resource of a row ingested while no object store was configured.
[Inline content and materialized files](handling-artifact-responses#inline-content-and-materialized-files)
covers the inline cutoff and the consumer-side model.

### `mcp-server` artifacts

`mcp-server` artifacts are excluded from the MCP server's read-only
`resources/list` mirror. A harness that consumes Podium through the MCP bridge
fixes its MCP server list at startup, so a registration discovered mid-session
would only add planning noise. These artifacts still appear in
`search_artifacts` results and still load through `load_artifact`. They reach a
harness's own configuration through `podium sync`.

---

## Latency and cost

The discovery layers are cheap, and load is expensive. The SLO targets are the
following:

| Call | p99 target |
|:--|:--|
| `load_domain` | < 200 ms |
| `search_domains` | < 200 ms |
| `search_artifacts` | < 200 ms |
| `load_artifact` (manifest only) | < 500 ms |
| `load_artifact` (manifest + bundled resources up to 10 MB, on a cache miss) | < 2 s |

Every call emits an audit event: `domain.loaded`, `domains.searched`,
`artifacts.searched`, or `artifact.loaded`. Free-text queries are PII-scrubbed
before audit. [Audit and observability](../deployment/observability#audit-log)
covers the audit log.

---

## Authoring affects discovery quality

What an agent finds depends largely on how authors describe their artifacts and
domains:

- **Artifact `description`** decides whether the harness reaches for the
  artifact at all. A specific description matches against actual user prompts,
  and a vague one gets ignored.
- **Artifact `when_to_use`** is additional retrieval signal. Concrete
  situational triggers rank better than abstract framings.
- **Domain `description` and `keywords`** are the text `search_domains`
  retrieves over. Without keywords, a query that uses no words from the
  description can miss the domain.
- **`featured` lists** in `DOMAIN.md` give domain owners explicit control over
  which artifacts surface first in `load_domain`'s notable list.

For authoring guidance, see [Your first skill](../authoring/your-first-skill),
[Your first command](../authoring/your-first-command),
[Your first agent](../authoring/your-first-agent), and
[Domains](../authoring/domains).
