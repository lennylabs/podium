---
title: How it works
nav_order: 4
description: "How one catalog reaches every harness: the component overview, where each feature runs, the deployment tiers, and where state lives."
---

# How it works

One catalog serves every harness because the catalog and the delivery are
separate. The catalog holds canonical artifacts. A harness adapter translates a
canonical artifact into the layout one runtime expects, at the moment the
artifact is written to disk. This page covers the components that perform the
translation, where each feature runs, and what state each deployment tier keeps.

Podium has two main parts:

- A **registry**: the system of record for artifacts.
- **Consumers**: components that read from the registry. Built-in
  consumers include language SDKs, the MCP server, and `podium sync`.
  `podium sync` renders a target as a workspace tree the harness reads
  directly or, for a `kind: marketplace` target, as a git-repo
  distribution a harness imports. Custom consumers can build against
  the HTTP API.

The registry can be reached as a Podium server (one binary or a replicated
deployment) or as a local directory. Most consumers work against a server, and
`podium sync` also works against a local directory.

---

## Where each feature runs

| Feature | What implements it | Where it runs | Covered in |
|:--|:--|:--|:--|
| Cross-harness delivery | The `HarnessAdapter` step of the materialization pipeline, shared by `podium sync`, the MCP server, and the SDKs. | On the consumer, in every tier. | [Configure your harness](../consuming/configure-your-harness) |
| Domains and subdomains | The catalog's directory layout, plus the `DOMAIN.md` metadata read from it. | In the catalog, in every tier. | [Domains](../authoring/domains) |
| Selective materialization | Scope resolution in `podium sync`, from the merged `sync.yaml` and the CLI flags. | On the consumer, in every tier. | [Selective materialization](../consuming/selective-materialization) |
| Progressive discovery | The meta-tools, backed by the domain renderer and hybrid retrieval. | On the registry, reached through the MCP server or an SDK. Requires single node or clustered. | [Browsing the catalog](../consuming/browsing-the-catalog) |
| Layered composition | The layer composer and the `extends:` resolver. | On the registry. A local directory composes ordered layers from one root; identity-aware composition across independent sources requires a server. | [Layered composition](../deployment/layers) |
| Access control | The visibility evaluator, run against the caller's attested identity. | On the registry. Requires single node or clustered with an identity provider configured. | [Access control](../deployment/access-control) |

---

## High-level architecture

The registry stores the catalog. Consumers retrieve artifacts and translate
them into the format expected by the harness.

A registry server is what a team runs once the catalog outgrows the local tier.
The server holds the catalog, consumers reach it over HTTP, and identity-aware
composition runs server-side:

![Registry-server architecture: Git and local sources flow into the Podium server, which serves language SDKs, the MCP server, and podium sync over an OAuth-attested HTTP API.](../assets/diagrams/architecture-server-mode.svg)

<!--
ASCII fallback for the diagram above (registry-server architecture):

  sources:
    Git repo            Git repo                 Local path
    team-shared @ main  company-glossary @ v3    /opt/podium/personal
         |                  |                       |
         +------------------+-----------------------+
                                  |
                                  v
                  +-----------------------------------+
                  | podium-server                     |
                  |   HTTP / JSON API                 |   OAuth identity
                  |   stateless front-end + Postgres  | --- on every call
                  |                                   |
                  |   [SQLite or Postgres]            |
                  |   [Layer composition]             |
                  |   [Visibility filter]             |
                  |   [Dependency graph]              |
                  |   [Audit + hash chain]            |
                  |   [Hybrid retrieval]              |
                  +-----------------+-----------------+
                                    |
            +-----------------------+-----------------------+
            v                       v                       v
       +-----------+           +-----------+           +-------------+
       | Language  |           | MCP       |           | podium sync |
       | SDKs      |           | server    |           | CLI library |
       | py / ts   |           | in-proc   |           |             |
       +-----------+           +-----------+           +-------------+
            |                       |                       |
            v                       v                       v
       targets:
       LangChain, Claude        Claude Code, OpenCode,   Filesystem harnesses
       Agent SDK, etc.          etc. (coding harnesses)  (writes to .claude/,
       (programmatic runtimes)                            .cursor/, etc.)
-->

The local tier fits any team or individual whose catalog does not
require access control or progressive discovery, and it fits prototypes and CI. The catalog is a folder and `podium sync` reads it directly. Configuration, diagrams, and error messages call this a filesystem-source registry. The diagram below shows a filesystem source and a server source side by side:

![Two catalog sources side by side: a filesystem source has podium sync reading a directory directly, and a server source places the registry behind an HTTP API with a metadata store and object storage.](../assets/diagrams/modes-filesystem-vs-server.svg)

<!--
ASCII fallback for the diagram above (filesystem source vs server source):

  Filesystem catalog                    |  Registry server
                                        |
  catalog:                              |   Git repo            Local path
    ~/podium-artifacts/                 |   team-shared @ main  /opt/podium/personal
      personal/hello/greet/ARTIFACT.md  |        |                   |
      finance/run-variance/ARTIFACT.md  |        +---------+---------+
              |                         |                  |
              v                         |                  v
       +----------------+               |     +-------------------------+
       | podium sync    |               |     | podium-server           |
       | reads directly |               |     |   HTTP / JSON API       |
       +-------+--------+               |     |   SQLite or Postgres    |
               |                        |     |   Layer composition     |
               v                        |     |   Visibility filter     |
  workspace:                            |     |   Audit + dependency    |
    project/.claude/skills/greet/       |     +------------+------------+
    .cursor/rules/, AGENTS.md, etc.     |             +----+----+
                                        |             v    v    v
                                        |          SDKs  MCP  podium sync
                                        |               server

  Fits any team or individual without   |  Required for runtime discovery,
  access control or progressive         |  access control, or centralized
  disclosure needs. MCP server and      |  audit. Catalog usually lives in
  language SDKs require a server;       |  Git; the server ingests tracked
  not available from a folder.          |  refs.
-->

The artifacts, the file formats (`ARTIFACT.md`, `SKILL.md`, and
`DOMAIN.md`), and the harness adapter behavior are identical across
tiers. `podium sync` either reaches the registry over HTTP or reads it
directly from disk. The MCP server and language SDKs require a server.

---

## What runs where

For server-source deployments:

| Component        | Role                                                                                                                           | Where it runs                                                                                                                      |
| :--------------- | :----------------------------------------------------------------------------------------------------------------------------- | :--------------------------------------------------------------------------------------------------------------------------------- |
| Registry service | HTTP API; layer composition; visibility filtering; manifest indexing; hybrid retrieval; dependency graph; signing; audit       | A server (one binary in the single-node tier, replicas behind a load balancer in the clustered tier)                               |
| Postgres         | Manifest metadata, layer config, admin grants, dependency edges, audit log, embeddings (when `pgvector` is the vector backend) | Alongside the registry (or managed RDS / Cloud SQL / Aurora)                                                                       |
| Object storage   | Bundled resource bytes, content-addressed                                                                                      | S3 / GCS / MinIO / R2 (the local filesystem in the single-node tier)                                                               |
| Vector backend   | Hybrid retrieval                                                                                                               | `pgvector` and `sqlite-vec` collocate with the metadata store; managed alternatives include Pinecone, Weaviate Cloud, Qdrant Cloud |
| MCP server       | In-process bridge for MCP-speaking hosts; runs the harness adapter at materialization time                                     | Spawned as a stdio subprocess by the host (Claude Code, Cursor, etc.), one per workspace                                           |
| `podium sync`    | Up-front filesystem materialization of a scope; one-shot or watcher. A `kind: marketplace` target renders a git-repo distribution and runs an operator-configured workflow to push it | Developer machines, CI runners, build pipelines                                                                                    |
| Language SDKs    | Programmatic HTTP clients                                                                                                      | Wherever your code runs: LangChain, Bedrock, OpenAI Assistants, custom orchestrators, eval harnesses                               |

The MCP server, `podium sync`, and the language SDKs share the same
registry HTTP API. They also share identity providers, the content
cache, layer composition, and the harness adapter. The MCP server
and `podium sync` are thin clients that delegate composition and
visibility to the registry, then run the adapter and write to disk
locally.

For filesystem-source deployments, only `podium sync` and the
filesystem-aware shared library are involved. There is no Postgres,
no object storage, no authentication, and no registry process.

---

## Deployment tiers

Podium runs in three tiers. Each tier keeps what the tier below it does and adds
server-side capability. Pick the one that fits today and move up when the
catalog outgrows it. [Deployment](../deployment/) has the setup for each.

### Local

A directory of files, with no daemon and no authentication.
`podium sync` reads the directory directly, applies layer
composition and the harness adapter, and writes to the harness's
destination.

- **Who it is for.** Any team or individual whose catalog does not
  require access control or progressive discovery. Solo workflows
  keep the directory local, and teams commit it to Git so every
  developer runs `podium sync` against their clone.
- **What runs.** The `podium` CLI.
- **What it covers.** Authoring, lint, sync, domains, and profiles. The
  harness's own filesystem discovery does the loading at runtime.
- **What it does not cover.** Progressive discovery through MCP or an SDK,
  centralized audit, and identity-based visibility filtering.
- **Multi-user.** Share the directory the way any folder is shared.
  Committing to Git is the typical choice, the Git history doubles as the
  audit trail, and `git pull` is each developer's ingest. A network share
  or a file-sync service also works.
- **Setup.** [Local](../deployment/local).

### Single node

A single binary running on one machine. SQLite, sqlite-vec, and
filesystem object storage are embedded. Semantic search uses the
embedding provider selected by `PODIUM_EMBEDDING_PROVIDER`:
`ollama` pointed at a local model server for offline or air-gapped
use, or a cloud provider (`openai`, `voyage`, or `cohere`). When
neither the variable nor `registry.yaml`'s `embedding_provider.type`
is set, the provider defaults to `ollama` at `http://localhost:11434`,
and an unreachable provider degrades search to BM25 at runtime. Pass
`--no-embeddings` to `podium serve` (or set `PODIUM_NO_EMBEDDINGS=true`)
to disable embeddings and run BM25-only search. Bind it to localhost
or to a private network.

- **Who it is for.** A team of any size that wants progressive discovery
  (agents calling the meta-tools mid-session) or a single audit log
  without running the clustered stack. A team can stay local until
  runtime discovery or centralized audit becomes necessary.
- **What runs.** `podium serve --standalone --layer-path /path/to/dir`
  plus the CLI.
- **What it adds.** Progressive discovery through the MCP server and the
  SDKs, hybrid search, layers and visibility, and one audit log covering
  every load.
- **Migration path.** Point `podium serve --standalone` at the same
  directory the local catalog uses, then change `defaults.registry` from a
  path to a URL. The authoring loop is unchanged.
- **Setup.** [Single node](../deployment/single-node).

### Clustered

Registry replicas behind a load balancer, with Postgres, pgvector, S3,
OIDC, and multi-tenancy. A Helm chart ships with the registry, and the
supporting services are managed or self-run alongside.

- **Who it is for.** Larger teams and organizations, multi-tenant
  deployments, governed environments, and anything with compliance
  constraints or identity-based visibility requirements.
- **What runs.** Registry replicas behind a load balancer,
  Postgres (managed or self-run), object storage, and an OIDC IdP.
- **What it adds.** Multi-tenancy, SCIM group sync, signing with a
  transparency log, freeze windows, hash-chained audit, and high
  availability.
- **Migration path.** `podium admin migrate-to-standard` exports
  a single-node deployment into the clustered stack. The same artifact
  directory becomes a `local`-source layer until the cut-over to
  Git-source layers.
- **Setup.** [Clustered](../deployment/clustered) and the
  [Operator guide](../deployment/operator-guide).

---

## Where state lives

State lives in the locations below. Each tier uses a different
combination.

| State                                  | Local                                                                              | Single node                                  | Clustered                                                                             |
| :------------------------------------- | :--------------------------------------------------------------------------------- | :------------------------------------------- | :------------------------------------------------------------------------------------ |
| Manifest metadata, layer config, audit | (none; directory is canonical)                                                     | SQLite (`~/.podium/standalone/podium.db`)    | Postgres                                                                              |
| Embeddings                             | (none)                                                                             | sqlite-vec collocated in SQLite              | pgvector collocated in Postgres (or external: Pinecone, Weaviate Cloud, Qdrant Cloud) |
| Bundled resource bytes                 | The directory itself                                                               | Filesystem (`~/.podium/standalone/objects/`) | S3-compatible object storage                                                          |
| Workspace local overlay                | `<workspace>/.podium/overlay/` (highest precedence in the caller's effective view) | `<workspace>/.podium/overlay/`                     | `<workspace>/.podium/overlay/`                                                        |
| Content cache                          | (none; `podium sync` keeps no content cache)                                       | `~/.podium/cache/` (MCP server; content-addressed) | `~/.podium/cache/` (MCP server; content-addressed)                                    |
| Sync state                             | `<target>/.podium/sync.lock` (per-target)                                          | `<target>/.podium/sync.lock`                       | `<target>/.podium/sync.lock`                                                          |

The workspace overlay, the content cache, and the sync state live on
the consumer's machine. The MCP server holds the content cache, and it
requires a server-source registry.

---

## Materialization

Materialization is the step that makes one canonical artifact land as
harness-native files, so it is where cross-harness delivery happens.

A consumer calls `load_artifact(id, harness=...)`. The pipeline
fetches bundled bytes from the registry, verifies them against the
manifest's signature and content hash, runs the configured
`HarnessAdapter` to translate the canonical layout into the
harness-native one, applies any `MaterializationHook` plugins, and
writes atomically to the destination. The artifact's bundled files travel
through the same pipeline as its manifest, so a script the author shipped
beside `SKILL.md` reaches every destination the manifest reaches. Errors at
any step abort with a structured code so the caller can decide whether to
retry, fall back, or surface a diagnostic.

![Materialization pipeline: load_artifact runs Fetch, Verify, Adapt, Hook, and Write in order; errors abort with a structured code; success writes into the destination tree.](../assets/diagrams/materialization-pipeline.svg)

<!--
ASCII fallback for the diagram above (materialization pipeline: load_artifact()):

  [1. Fetch]   ==> [2. Verify]   ==> [3. Adapt]    ==> [4. Hook]    ==> [5. Write]   ==> destination tree
   bytes from       signature         Harness            plugins           atomic .tmp      .claude/, .cursor/, ..
   registry         policy            Adapter ->         rewrite           + rename
   + presigned      + content         native             per-file
   URLs             hash              layout             in order

  Steps 3 (Adapt) and 4 (Hook) are pluggable; the rest are fixed. Hooks share the
  adapter sandbox: no network calls, no subprocesses, and no writes outside the
  destination. Errors at any step abort with a structured code (see surrounding prose).
-->

`HarnessAdapter` and `MaterializationHook` are the pluggable steps.
The remaining steps are fixed. Hooks share the adapter sandbox:
they cannot make network calls, spawn subprocesses, or write
outside the destination.

The registry returns metadata and the resource bytes below the inline
cutoff. A resource above the cutoff, and a manifest above it, live in
content-addressed object storage and reach the consumer as a URL the
consumer fetches. With the S3 backend the URL is presigned, it expires
after the configured TTL, and the fetch goes to object storage without
passing through the registry. With the filesystem backend, which a single
node uses by default, the URL is the registry's own `/objects/<content-hash>`
route: it carries no signature and no expiry, the consumer sends the session
token it used for `load_artifact`, and the registry re-checks visibility
before streaming the bytes. The MCP server resolves those URLs and writes
every resource to disk, so the agent's result holds the manifest body and the
file paths. The SDKs return the manifest in memory and resolve the references
on a later `materialize()` call.

---

## Marketplace publishing

A `podium sync` target of `kind: marketplace` is a further output
path. It reads the publishing identity's effective view over the same
HTTP API, renders each harness's git-repo distribution (a plugin
marketplace, extension, package, or tap), and runs an
operator-configured workflow that clones, commits, and pushes the
result to a git remote. A harness then imports the published
repository through its own install path. The CI trigger is the
`layer.ingested` webhook event, so one source commit yields one
publish.

![Publish flow: a source change ingests into the registry, which emits layer.ingested; a CI job (scheduled, or relayed through a receiver and repository_dispatch) runs podium sync --config, which renders the marketplace tree and pushes it to a git remote that the harness imports.](../assets/diagrams/publish-flow.svg)

<!--
ASCII fallback for the diagram above (publish flow):

  source change                registry
    git push to a    ====>      ingest cycle      ===(layer.ingested)==>  trigger
    layer source                emits one event                           (one of two)
                                                                              |
       +----------------------------------------------------------------------+
       |                                                                      |
  Pattern A (scheduled)                                Pattern B (event relay)
  +---------------------+                              +---------------------------+
  | GitHub Actions cron |                              | receiver (layer.ingested) |
  +----------+----------+                              +-------------+-------------+
             |                                                       |
             |                                          verify HMAC, POST dispatches
             |                                                       v
             |                                          +---------------------------+
             |                                          | relay ===> repository_    |
             |                                          | dispatch ===> CI job      |
             |                                          +-------------+-------------+
             |                                                       |
             +-----------------------------+-------------------------+
                                           v
                                +---------------------+
                                | podium sync (config)|
                                |  prepare (clone)    |
                                |  render (Podium)    |
                                |  publish (push)     |
                                +----------+----------+
                                           v
                                +---------------------+        +-------------------+
                                | git remote          | =====> | harness imports   |
                                | marketplace repo    | import | the marketplace   |
                                +---------------------+        +-------------------+

  The receiver cannot call GitHub's dispatch endpoint directly because the
  HMAC-signed receiver body differs, so a relay verifies the HMAC and issues
  POST /repos/<owner>/<repo>/dispatches.
-->

See [Consuming → Marketplace publishing](../consuming/publishing) for
the model, the marketplace target schema, and the worked GitHub
Actions patterns.

---

## Shared library code

The manifest parsers, glob resolver, layer composer, `extends:`
resolver, visibility evaluator, materialization writer, and harness
adapters all live in a single Go module. The registry binary embeds it
behind the HTTP API. `podium sync` against a filesystem-source registry calls
the same module functions directly, skipping HTTP. The MCP server and
`podium sync` against a server-source registry are thin HTTP clients that invoke
the same module's materialization writer locally.

There is a single canonical implementation per concern. Migrating
between tiers (local, then single node, then clustered)
preserves behavior because the same composer, parsers, merge
semantics, and harness adapter output run in every tier.

The language SDKs are the exception: they're independent HTTP
clients in Python and TypeScript, and they only work against a
Podium server.

---

## Identity and trust

The registry attaches an OAuth-attested identity to every call.
Built-in identity providers:

- **`oauth-device-code`**: a client-side provider. The consumer runs the
  interactive device-code flow (`podium login`) and caches the tokens in the
  OS keychain; the registry ships no request-time verifier for it. Setting
  `PODIUM_IDENTITY_PROVIDER=oauth-device-code` on the registry aborts startup
  with `config.identity_provider_unverified`. The registry verifies
  `injected-session-token`, `oidc-jwt`, and `trusted-headers`.
- **`injected-session-token`**: runtime-issued signed JWT for
  managed agent runtimes (Bedrock Agents, OpenAI Assistants, custom
  orchestrators). The runtime registers its signing key once with
  the registry; the registry verifies signatures on every call.
- **`oidc-jwt`** and **`trusted-headers`**: registry-process providers for a
  deployment that runs the registry behind a gateway that already authenticated
  the caller. `oidc-jwt` verifies the forwarded IdP-signed token on every
  request. `trusted-headers` reads the identity the gateway injects as request
  headers. See
  [Gateway-delegated identity](../deployment/gateway-delegated-identity).

A filesystem-source registry has no identity by definition:
the visibility evaluator short-circuits to `true` for every layer.
A single-node deployment runs with or without auth, and with auth
it uses the same OIDC machinery as a clustered deployment.

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

After the vocabulary and the architecture, continue with the guide that fits
the task:

- **Write artifacts**: [Authoring guide](../authoring/)
- **Deliver them into a harness**: [Configure your harness](../consuming/configure-your-harness)
- **Narrow what a workspace receives**: [Selective materialization](../consuming/selective-materialization)
- **Let an agent find them at runtime**: [Browsing the catalog](../consuming/browsing-the-catalog)
- **Run Podium for a team or organization**: [Deployment guide](../deployment/)
- **Call the API directly**: [Reference](../reference/)
