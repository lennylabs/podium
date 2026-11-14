---
title: How it works
nav_order: 4
description: "The architecture of the Podium registry: the components, where each feature runs, the deployment modes, where state lives, and how an artifact reaches a harness."
---

# How it works

This page describes the architecture of Podium: the components, where each
feature runs, what state each deployment mode keeps, and how an artifact
travels from the registry to a harness. It is written for a reader who knows
the [concepts](concepts) and wants to see how the pieces fit together.

## Components

Podium separates the catalog from its delivery. The registry holds canonical
artifacts. A harness adapter translates a canonical artifact into the layout
one runtime expects at the moment the artifact is written to disk.

Podium has two main parts:

- The **registry** is the system of record for artifacts.
- **Consumers** read from the registry. The built-in consumers are the language
  SDKs, the MCP server, and `podium sync`. Custom consumers can build against
  the HTTP API.

`podium sync` renders a target as a workspace tree the harness reads directly.
For a `kind: marketplace` target, it renders a git-repo distribution that a
harness imports.

The registry can be reached as a Podium server (one binary or a replicated
deployment) or as a local directory. Most consumers work against a server, and
`podium sync` also works against a local directory.

---

## Where each feature runs

The table names the component that implements each feature and the side it
runs on. [Why Podium](why-podium#features) describes the features themselves.

| Feature | What implements it | Where it runs |
|:--|:--|:--|
| Domains and subdomains | The catalog's directory layout, plus the `DOMAIN.md` metadata read from it. | In the catalog, in every deployment mode. |
| Selective materialization | Scope resolution in `podium sync`, from the merged `sync.yaml` and the CLI flags. | On the consumer, in every deployment mode. |
| Progressive discovery | The meta-tools, backed by the domain renderer and hybrid retrieval. | On the registry server, reached through the MCP server or an SDK. |
| Layered composition | The layer composer, fed by the layer source providers. | On the registry. A filesystem source composes ordered layers from one root, and identity-aware composition across independent sources runs on a server. |
| Security and access control | Signing at ingest, the visibility evaluator run against the caller's verified identity, and the audit log. | On the registry server, with signature verification on the consumer. |
| Observability | The audit destinations, the Prometheus metrics endpoint, the OpenTelemetry tracer, and the notification provider. | On the registry server. The MCP server can also write a local audit stream, serve metrics, and export traces. |
| Cross-harness delivery | The `HarnessAdapter` step of the materialization pipeline, shared by `podium sync`, the MCP server, and the SDKs. | On the consumer, in every deployment mode. |

---

## High-level architecture

The registry stores the catalog. Consumers retrieve artifacts and translate
them into the format the harness expects.

A team runs a registry server once the catalog outgrows the local deployment
mode. The server holds the catalog, consumers reach it over HTTP, and
identity-aware composition runs on the server:

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

The local deployment mode fits any team or individual whose catalog does not
require access control or progressive discovery. It also fits prototypes and
CI. The catalog is a folder, and `podium sync` reads it directly.
Configuration, diagrams, and error messages call this a filesystem-source
registry. The diagram below shows a filesystem source and a server source side
by side:

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

The artifacts, the file formats (`ARTIFACT.md`, `SKILL.md`, and `DOMAIN.md`),
and the harness adapter behavior are identical across deployment modes.
`podium sync` either reaches the registry over HTTP or reads it directly from
disk. The MCP server and language SDKs require a server.

---

## What runs where

The table lists the components of a server-source deployment:

| Component        | Role                                                                                                                           | Where it runs                                                                                                                      |
| :--------------- | :----------------------------------------------------------------------------------------------------------------------------- | :--------------------------------------------------------------------------------------------------------------------------------- |
| Registry service | HTTP API; layer composition; visibility filtering; manifest indexing; hybrid retrieval; dependency graph; signing; audit       | A server (one binary in the single-node mode, replicas behind a load balancer in the clustered mode)                               |
| Postgres         | Manifest metadata, layer config, admin grants, dependency edges, embeddings (when `pgvector` is the vector backend)            | Alongside the registry (or managed RDS / Cloud SQL / Aurora)                                                                       |
| Object storage   | Bundled resource bytes, content-addressed                                                                                      | S3 / GCS / MinIO / R2 (the local filesystem in the single-node mode)                                                               |
| Vector backend   | Hybrid retrieval                                                                                                               | `pgvector` and `sqlite-vec` collocate with the metadata store; managed alternatives include Pinecone, Weaviate Cloud, Qdrant Cloud |
| MCP server       | In-process bridge for MCP-speaking hosts; runs the harness adapter at materialization time                                     | Spawned as a stdio subprocess by the host (Claude Code, Cursor, etc.), one per workspace                                           |
| `podium sync`    | Up-front filesystem materialization of a scope; one-shot or watcher. A `kind: marketplace` target renders a git-repo distribution and runs an operator-configured workflow to push it | Developer machines, CI runners, build pipelines                                                                                    |
| Language SDKs    | Programmatic HTTP clients                                                                                                      | Wherever the calling code runs: LangChain, Bedrock, OpenAI Assistants, custom orchestrators, eval harnesses                        |

The MCP server, `podium sync`, and the language SDKs share the same registry
HTTP API. They also share identity providers, the content cache, layer
composition, and the harness adapter. The MCP server and `podium sync` are thin
clients. They delegate composition and visibility to the registry, then run the
adapter and write to disk locally.

A filesystem-source deployment involves only `podium sync` and the
filesystem-aware shared library. It has no Postgres, no object storage, no
authentication, and no registry process.

---

## Deployment modes

The registry runs in the local, single-node, or clustered deployment mode.
Each mode keeps what the mode before it does and adds server-side capability.

| Mode | What runs |
|:--|:--|
| Local | The `podium` CLI, reading a catalog directory. There is no server process, database, or identity provider. |
| Single node | One registry process, started with `podium serve --standalone`, with SQLite, `sqlite-vec`, and filesystem object storage embedded. |
| Clustered | Registry replicas behind a load balancer, with Postgres, S3-compatible object storage, and an OIDC identity provider. A Helm chart ships for Kubernetes. |

Multi-tenancy, SCIM group push, freeze windows, outbound webhooks, and the
hash-chained audit log run on a single node as well. The clustered mode adds
high availability across registry replicas and Postgres schema-per-org tenant
isolation. [Deployment modes](../deployment/modes/) covers choosing a mode, and
[Moving between modes](../deployment/modes/migrating) covers the migration
from one mode to the next.

---

## Where state lives

Each deployment mode keeps state in a different combination of the locations
below.

| State                                  | Local                                                                              | Single node                                  | Clustered                                                                             |
| :------------------------------------- | :--------------------------------------------------------------------------------- | :------------------------------------------- | :------------------------------------------------------------------------------------ |
| Manifest metadata, layer config        | (none; directory is canonical)                                                     | SQLite (`~/.podium/standalone/podium.db`)    | Postgres                                                                              |
| Audit                                  | (none)                                                                             | The `PODIUM_AUDIT_LOG_PATH` destinations (default `~/.podium/audit.log`) | The `PODIUM_AUDIT_LOG_PATH` destinations (default `~/.podium/audit.log`)    |
| Embeddings                             | (none)                                                                             | sqlite-vec collocated in SQLite              | pgvector collocated in Postgres (or external: Pinecone, Weaviate Cloud, Qdrant Cloud) |
| Bundled resource bytes                 | The directory itself                                                               | Filesystem (`~/.podium/standalone/objects/`) | S3-compatible object storage                                                          |
| Workspace local overlay                | `<workspace>/.podium/overlay/` (highest precedence in the caller's effective view) | `<workspace>/.podium/overlay/`                     | `<workspace>/.podium/overlay/`                                                        |
| Content cache                          | (none; `podium sync` keeps no content cache)                                       | `~/.podium/cache/` (MCP server; content-addressed) | `~/.podium/cache/` (MCP server; content-addressed)                                    |
| Sync state                             | `<target>/.podium/sync.lock` (per-target)                                          | `<target>/.podium/sync.lock`                       | `<target>/.podium/sync.lock`                                                          |

The workspace overlay, the content cache, and the sync state live on the
consumer's machine. The MCP server holds the content cache, and it requires a
server-source registry.

---

## Materialization

Materialization is the step that lands one canonical artifact as
harness-native files, so it is where cross-harness delivery happens.

A consumer calls `load_artifact(id, harness=...)`, and the pipeline runs these
steps in order:

1. Fetch the bundled bytes from the registry.
2. Verify them against the served delivery hash and the registry's signature
   over it.
3. Run the configured `HarnessAdapter` to translate the canonical layout into
   the harness-native one.
4. Apply any `MaterializationHook` plugins.
5. Write atomically to the destination.

The artifact's bundled files travel through the same pipeline as its manifest,
so a script the author shipped beside `SKILL.md` reaches every destination the
manifest reaches. An error at any step aborts with a structured code, so the
caller can decide whether to retry, fall back, or surface a diagnostic.

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

`HarnessAdapter` and `MaterializationHook` are the pluggable steps, and the
remaining steps are fixed. Hooks share the adapter sandbox. They cannot make
network calls, spawn subprocesses, or write outside the destination.

### How resource bytes reach the consumer

The registry returns the manifest body and every bundled resource at or below
the inline cutoff inside the `load_artifact` response. On a registry with an
object store, each larger resource, and a manifest document above the cutoff,
arrives as a URL into content-addressed object storage that the consumer
fetches. The MCP server fetches those URLs and writes every resource to disk
before it returns, while the SDKs return the manifest in memory and fetch the
URLs on a later `materialize()` call.
[Handling artifact responses](../consuming/handling-artifact-responses) covers
the cutoff, the URL that each object-storage backend serves, and the resources
that arrive inline at any size.

---

## Marketplace publishing

A `podium sync` target of `kind: marketplace` is a further output path. It
works in these stages:

1. It reads the publishing identity's effective view over the same HTTP API.
2. It renders each harness's git-repo distribution: a plugin marketplace,
   extension, package, or tap.
3. It runs an operator-configured workflow that clones, commits, and pushes
   the result to a git remote.

A harness then imports the published repository through its own install path.
The CI trigger is the `layer.ingested` webhook event, so one source commit
yields one publish.

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

[Publishing marketplace repositories](../deployment/content/publishing) covers the model, the
marketplace target schema, and the worked GitHub Actions patterns.

---

## Shared library code

The manifest parsers, glob resolver, layer composer, visibility evaluator,
materialization writer, and harness adapters all live in a single Go module.
The module is used in these ways:

- The registry binary embeds it behind the HTTP API.
- `podium sync` against a filesystem-source registry calls the same module
  functions directly, skipping HTTP.
- The MCP server and `podium sync` against a server-source registry are thin
  HTTP clients that invoke the same module's materialization writer locally.

There is a single canonical implementation per concern. Migrating between
deployment modes (local, then single node, then clustered) preserves behavior,
because the same composer, parsers, merge semantics, and harness adapter
output run in every mode.

The language SDKs are the exception. They are independent HTTP clients in
Python and TypeScript, and they work only against a Podium server.

---

## Identity and trust

With an identity provider configured, the registry verifies the caller's
identity on every call. The registry-side identity providers are `oidc-jwt`,
`trusted-headers`, and `injected-session-token`:

- **`oidc-jwt`** verifies an IdP-signed token on every request. A CLI or an SDK
  obtains that token through the device-code flow (`podium login`), a gateway
  may forward it, or the registry obtains it for a browser through its own
  authorization-code exchange.
- **`trusted-headers`** reads the identity that a fronting gateway injects as
  request headers, for a registry behind a gateway that already authenticated
  the caller.
- **`injected-session-token`** verifies a JWT that a managed agent runtime
  signs, against the runtime keys the registry trusts.

The registry composes each caller's effective view from the layers whose
visibility matches that identity. A filesystem source has no identity, so the
visibility evaluator admits every layer. A single-node deployment runs with or
without an identity provider, and with one it uses the same machinery as a
clustered deployment. [Identity](../deployment/identity/) covers each provider
and its configuration.

### Signing

The registry signs each artifact version at ingest with a registry-managed
Ed25519 key, and signing is on by default. Each response carries a delivery
signature over the served record. The MCP server and server-source
`podium sync` verify it before they write anything, as step 2 of the
materialization pipeline. [Verifying deliveries](../consuming/verifying-deliveries)
covers the verification policy a consumer applies, and
[Signing keys](../deployment/operations/signing-keys) covers the key itself.

### Audit

The registry records significant events, including every meta-tool call,
ingest, layer change, and admin grant, in a hash-chained audit log. The stream
goes to the destinations `PODIUM_AUDIT_LOG_PATH` lists: a file, stdout, HTTP
receivers, or an OpenTelemetry Collector. The MCP server can write its own
local audit stream as well.
[Audit and observability](../deployment/operations/observability#audit-log) covers the
destinations, metrics, tracing, and notifications.

---

## Versioning

Versions are semver, chosen by the author through the manifest's `version:`
field or, for a skill, `SKILL.md` `metadata.version`. A skill that declares no
version receives `0.0.0-auto.<N>` from the registry.

Once `(artifact_id, version)` is ingested, it is bit-for-bit immutable in the
registry's content store. Later commits to the same version with different
content are rejected at ingest.

References can pin versions in these forms:

- An exact version, such as `@1.2.3`.
- A minor or patch range, such as `@1.2.x` or `@1.x`.
- A content hash, such as `@sha256:abc...`.
- `@0.0.0-auto.<N>`, which pins a registry-assigned version exactly. A range
  pin never selects a registry-assigned version.

See [Skill versions](../authoring/frontmatter-reference#skill-versions).

`load_artifact(id)` without a version pin resolves to the most recently
ingested non-deprecated version visible to the caller. For session
consistency, the meta-tools accept a `session_id` argument. The first `latest`
lookup within a session is recorded and reused for every later lookup of the
same ID, so the host sees a consistent snapshot.

---

## What's next

Continue with the guide that fits the task:

- **Write artifacts**: [Authoring guide](../authoring/)
- **Deliver them into a harness**: [Configure your harness](../consuming/configure-your-harness)
- **Narrow what a workspace receives**: [Selective materialization](../consuming/selective-materialization)
- **Let an agent find them at runtime**: [Browsing the catalog](../consuming/browsing-the-catalog)
- **Connect Git repositories as layer sources**: [Connecting layer sources](../deployment/content/layer-sources)
- **Run Podium for a team or organization**: [Deployment guide](../deployment/)
- **Call the API directly**: [Reference](../reference/)
