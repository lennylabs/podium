---
layout: default
title: How it works
parent: Getting Started
nav_order: 3
description: Component overview, deployment shapes, where state lives, and what runs on your machine versus on a server.
---

# How it works

Podium consists of:

- A **registry**: the system of record for artifacts.
- **Consumers**: the components that read from the registry.
  Built-in consumers include language SDKs, the MCP server, and
  `podium sync`. Custom consumers can build against the HTTP API.

The registry can be reached as an HTTP service (single binary or
multi-tenant deployment) or as a local filesystem path. Most
consumers work against the HTTP shape; `podium sync` also works
against the filesystem shape directly.

---

## High-level architecture

Podium consists of:

- A **registry**: the catalog of artifacts. Backed by either a
  folder on disk (filesystem mode) or a Podium server (standalone or
  standard mode). Built-in source types are `git` and `local`; the
  `LayerSourceProvider` SPI lets deployments add custom sources
  (S3, OCI, HTTP archives).
- **Consumers**: built-in consumers are `podium sync`, the MCP
  server, and the language SDKs. Custom consumers can build against
  the HTTP API directly.

Server mode is what most teams run once they're past the filesystem
stage. The server holds the catalog; consumers reach it over HTTP
and identity-aware composition runs server-side:

```
   Git repos / local paths ──────────┐
   (one or more layer sources)       │
                                     ▼
                       ┌─────────────────────────┐
                       │ Podium server           │
                       │  HTTP/JSON API          │
                       │  Postgres + pgvector    │
                       │  Object storage         │
                       │  Layer composition      │
                       │  Visibility filtering   │
                       │  Dependency graph       │
                       └────────────▲────────────┘
                                    │
                  OAuth-attested identity (every call)
                                    │
       ┌────────────────────────────┼────────────────────────────┐
       │                            │                            │
┌──────┴───────┐          ┌─────────┴──────┐          ┌──────────┴─────┐
│ Language SDKs│          │ MCP server     │          │ podium sync    │
│ (py, ts)     │          │ (in-process)   │          │ (CLI)          │
└──────────────┘          └────────────────┘          └────────────────┘
LangChain, Bedrock,       Claude Code, Cursor,        File-based
custom orchestrators      OpenCode, Pi, Hermes        harnesses
```

Filesystem mode is for solo work, prototypes, and CI. The catalog is
a folder; `podium sync` reads it directly:

```
   ~/podium-artifacts/             ┌─────────────┐
   ├── personal/                ──→│ podium sync │──→ harness directory
   │   └── greet/ARTIFACT.md       └─────────────┘    (.claude/, .cursor/, ...)
   ├── team-shared/                       ▲
   │   └── ...                            │
   └── .layer-order                       │
                              No daemon. No port. No auth.
                              The catalog is the directory.
```

Same artifacts, same `ARTIFACT.md` and `DOMAIN.md` formats, same
adapter behavior. The only thing that changes is whether `podium
sync` reaches the registry over HTTP or reads it directly from disk.
The MCP server and language SDKs require a server.

---

## What runs where

For server-source deployments:

| Component | Role | Where it runs |
|:--|:--|:--|
| Registry service | HTTP API; layer composition; visibility filtering; manifest indexing; hybrid retrieval; dependency graph; signing; audit | A server (single binary in standalone mode; replicated behind a load balancer in standard mode) |
| Postgres | Manifest metadata, layer config, admin grants, dependency edges, audit log, embeddings (when `pgvector` is the vector backend) | Alongside the registry (or managed RDS / Cloud SQL / Aurora) |
| Object storage | Bundled resource bytes, content-addressed | S3 / GCS / MinIO / R2 (filesystem in standalone mode) |
| Vector backend | Hybrid retrieval | `pgvector` and `sqlite-vec` collocate with the metadata store; managed alternatives include Pinecone, Weaviate Cloud, Qdrant Cloud |
| MCP server | In-process bridge for MCP-speaking hosts; runs the harness adapter at materialization time | Spawned as a stdio subprocess by the host (Claude Code, Cursor, etc.), one per workspace |
| `podium sync` | Eager filesystem materialization; one-shot or watcher | Developer machines, CI runners, build pipelines |
| Language SDKs | Programmatic HTTP clients | Wherever your code runs: LangChain, Bedrock, OpenAI Assistants, custom orchestrators, eval harnesses |

The MCP server, `podium sync`, and the language SDKs share the same
registry HTTP API. They also share identity providers, the content
cache, layer composition, and the harness adapter. The MCP server
and `podium sync` are thin clients that delegate composition and
visibility to the registry, then run the adapter and write to disk
locally.

For filesystem-source deployments, only `podium sync` and the
filesystem-aware shared library are involved. No Postgres, no
object storage, no auth, no registry process.

---

## Deployment shapes

These labels appear throughout the docs. Pick the one that fits
today; graduate when you outgrow it.

### Filesystem

A directory of files; no daemon, no port, no auth. `podium sync`
reads the directory directly, applies layer composition and the
harness adapter, and writes to your harness's destination.

- **Who it's for.** Individual developers and small teams. Solo
  workflows keep the directory local; small teams commit it to git
  and every developer runs `podium sync` against their clone.
- **What you run.** Just the `podium` CLI.
- **What you get.** Eager materialization. The harness's own
  filesystem discovery does the loading at runtime.
- **What you don't get.** Lazy discovery (no MCP, no SDK). No
  centralized audit. No identity-based visibility filtering.
- **Multi-user.** Share the directory however you'd share any
  folder. Committing to git is the typical choice — the git history
  doubles as the audit trail and `git pull` is each developer's
  ingest. A network share or a sync service (Dropbox, iCloud, etc.)
  also works.

### Standalone server

A single binary running on one machine. SQLite + sqlite-vec +
filesystem object storage + a bundled embedding model, all
embedded. Bind to localhost or behind your VPN.

- **Who it's for.** Anyone of any team size who specifically wants
  runtime discovery (agents calling MCP meta-tools mid-session) or
  a single audit log without standing up the full standard stack.
  Most small teams don't need this; reach for it when filesystem
  mode stops fitting.
- **What you run.** `podium serve --standalone --layer-path
  /path/to/dir` plus the CLI.
- **What you get.** Runtime discovery via the MCP server. A single
  audit log capturing every load. Semantic search.
- **Migration path.** Point `podium serve --standalone` at the same
  directory your filesystem catalog uses; flip
  `defaults.registry` from a path to a URL. Authoring loop
  unchanged.

### Standard

The full deployment: Postgres + pgvector + S3 + OIDC + multi-tenancy.
Helm chart ships with the registry; supporting services are managed
or self-run alongside.

- **Who it's for.** Larger teams and organizations. Multi-tenant
  deployments, governed environments, anything with compliance
  constraints or identity-based visibility requirements.
- **What you run.** Registry replicas behind a load balancer,
  Postgres (managed or self-run), object storage, an OIDC IdP. See
  [Deployment → Operator guide](../deployment/operator-guide) and
  the spec's [§13](https://github.com/lennylabs/podium/blob/main/spec/13-deployment.md).
- **What you get.** Per-layer visibility, freeze windows, signing,
  hash-chained audit, SCIM, SBOM/CVE pipeline, multi-tenancy.
- **Migration path.** `podium admin migrate-to-standard` exports
  from a standalone deployment to a standard one; the same artifact
  directory becomes a `local`-source layer until you cut over to
  Git-source layers.

---

## Where state lives

Three places. Each shape uses a different combination.

| State | Filesystem | Standalone | Standard |
|:--|:--|:--|:--|
| Manifest metadata, layer config, audit | (none; directory is canonical) | SQLite (`~/.podium/standalone/podium.db`) | Postgres |
| Embeddings | (none) | sqlite-vec collocated in SQLite | pgvector collocated in Postgres (or external: Pinecone, Weaviate Cloud, Qdrant Cloud) |
| Bundled resource bytes | The directory itself | Filesystem (`~/.podium/standalone/objects/`) | S3-compatible object storage |
| Workspace local overlay | `<workspace>/.podium/overlay/` (highest precedence in the caller's effective view) |
| Content cache | `~/.podium/cache/` (content-addressed; shared across workspaces) |
| Sync state | `<target>/.podium/sync.lock` (per-target) |

The workspace overlay, content cache, and sync state are
client-side; they exist regardless of which deployment shape the
registry uses.

---

## Shared library code

Worth saying explicitly: the manifest parsers, glob resolver, layer
composer, `extends:` resolver, visibility evaluator, materialization
writer, and harness adapters all live in a single Go module. The
registry binary embeds it behind the HTTP API; `podium sync` in
filesystem mode calls the same module functions directly, skipping
HTTP. The MCP server and `podium sync` in server-source mode are
thin HTTP clients that invoke the same module's materialization
writer locally.

There is one canonical implementation per concern, not three. That's
why migrating between deployment shapes (filesystem → standalone →
standard) preserves behavior: same composer, same parsers, same
merge semantics, same harness adapter output. See [§2.2 of the
spec](https://github.com/lennylabs/podium/blob/main/spec/02-architecture.md)
for the load-bearing rationale.

The language SDKs are the exception: they're independent HTTP
clients in Python and TypeScript, and they only work against an
HTTP server.

---

## Identity and trust

The registry attaches an OAuth-attested identity to every call.
Built-in identity providers:

- **`oauth-device-code`**: interactive device-code flow for
  developer hosts; tokens cached in the OS keychain.
- **`injected-session-token`**: runtime-issued signed JWT for
  managed agent runtimes (Bedrock Agents, OpenAI Assistants, custom
  orchestrators). The runtime registers its signing key once with
  the registry; the registry verifies signatures on every call.

Filesystem-source registries don't have identity by definition:
the visibility evaluator short-circuits to `true` for every layer.
Standalone deployments can run with or without auth; with auth,
they use the same OIDC machinery as standard deployments.

`tenant.expose_scope_preview` lets operators decide whether
aggregate visibility counts (artifact count, by-type, by-sensitivity)
are exposed to callers, which is useful for tenants where even those
aggregates would leak signal.

---

## Versioning

Versions are semver, author-chosen via the manifest's `version:`
field. Once `(artifact_id, version)` is ingested, it's bit-for-bit
immutable forever in the registry's content store. Subsequent
commits to the same version with different content are rejected at
ingest. References can pin exact versions
(`@1.2.3`), minor / patch ranges (`@1.2.x`, `@1.x`), or content
hashes (`@sha256:abc...`).

`load_artifact(id)` without a version pin resolves to the most
recently ingested non-deprecated version visible to the caller. For
session consistency, the meta-tools accept a `session_id` argument:
the first `latest` lookup within a session is recorded and reused
for every subsequent same-id lookup, so the host sees a consistent
snapshot.

---

## What's next

You've got the vocabulary and the architecture. From here, follow
the role-specific guide that fits your goal:

- **Authoring artifacts**: [Authoring guide](../authoring/)
- **Using them in a harness**: [Consuming guide](../consuming/)
- **Setting up Podium for a team or org**: [Deployment guide](../deployment/)
- **Calling the API directly**: [Reference](../reference/)

The full technical specification, one file per top-level section,
lives in the [`spec/`](https://github.com/lennylabs/podium/tree/main/spec)
directory of the repository.
