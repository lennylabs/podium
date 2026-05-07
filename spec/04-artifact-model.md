# 4. Artifact Model

## 4.1 Artifacts Are Packages of Arbitrary Files

An artifact is a directory with a manifest at its root. The manifest — `ARTIFACT.md` — is a markdown file with YAML frontmatter and prose. Frontmatter is what the registry indexes; prose is what the host reads when the artifact is loaded.

**Bundled resources alongside the manifest are arbitrary files.** Python scripts, shell scripts, templates, JSON / YAML schemas, evaluation datasets, model weights, binary blobs — anything the host needs at runtime. The registry treats these as opaque versioned blobs.

### First-class types

Full lint coverage, conformance suite participation, broad adapter support:

- `skill` — instructions (+ optional scripts) loaded into the host agent's context on demand.
- `agent` — a complete agent definition meant to run in isolation as a delegated child.
- `context` — pure reference material (style guides, glossaries, API references, large knowledge bases).
- `prompt` — parameterized prompt templates the agent or a human can instantiate.

### Registered extension types

Schemas and lint rules but no conformance commitment beyond what the type owner specifies:

- `mcp-server` — an MCP server registration (name, endpoint, auth profile, description). Renamed from `tool` to avoid collision with MCP's "tool" callable concept.
- `dataset`, `model`, `eval`, `policy` — register additional types via the `TypeProvider` SPI (§9).
- `workflow` — reserved.

The type system is extensible: deployments register additional types with their own lint rules. Podium treats every type uniformly for discovery, search, and load; type-specific runtime behaviour lives in hosts.

The type determines indexing, loading semantics, governance requirements, and search ranking. A `context` artifact does not need the same safety review as a `skill` because instructions are more dangerous than reference data.

**Manifest size lint.** A reasonable cap is ~20K tokens of manifest content. Larger reference content should be factored out as a separate `type: context` artifact.

**Package layout example.** A skill that ships with a Python script and a Jinja template:

```
finance/close-reporting/run-variance-analysis/
  ARTIFACT.md
  scripts/
    variance.py
    helpers.py
  templates/
    variance-report.md.j2
  schemas/
    output.json
```

**Three size thresholds with distinct roles:**

- **Inline cutoff (256 KB)** — below this, resource bytes are returned in the `load_artifact` response body; above, presigned URL.
- **Per-file soft cap (1 MB)** — ingest-time warning above this.
- **Per-package soft cap (10 MB)** — ingest-time error above this.

For resources larger than the per-package cap (model files, datasets), use the `external_resources:` mechanism (§4.3): the manifest references pre-uploaded object-storage URLs with content hashes and signatures; bytes don't transit the registry. Caps don't apply to external resources.

## 4.2 Registry Layout on Disk

The registry's authoring layout is a domain hierarchy. Directories are domain paths and the leaves are artifact packages. The **canonical artifact ID** is the directory path under the registry root (e.g., `finance/ap/pay-invoice`). All references — `extends:`, `delegates_to:`, glob patterns — use this ID, optionally suffixed with `@<semver>` or `@sha256:<hash>`.

```
registry/
├── registry.yaml
├── company-glossary/
│   └── ARTIFACT.md
├── finance/
│   ├── DOMAIN.md
│   ├── ap/
│   │   ├── DOMAIN.md
│   │   ├── pay-invoice/
│   │   │   └── ARTIFACT.md
│   │   └── reconcile-invoice/
│   │       ├── ARTIFACT.md
│   │       └── scripts/
│   │           └── reconcile.py
│   └── close-reporting/
│       └── run-variance-analysis/
│           ├── ARTIFACT.md
│           ├── scripts/
│           └── templates/
├── _shared/
│   └── payment-helpers/
│       ├── DOMAIN.md             # unlisted: true — exists for imports + search only
│       ├── routing-validator/
│       │   └── ARTIFACT.md
│       └── swift-bic-parser/
│           └── ARTIFACT.md
└── engineering/
    └── platform/
        └── code-change-pr/
            └── ARTIFACT.md
```

The hierarchy can nest to arbitrary depth for organization. For discovery, a two-level cap (domain → subdomain) is the default; deeper nesting collapses into the leaf set returned by `load_domain`.

Each layer (§4.6) is rooted at a Git repo or local filesystem path; the directory hierarchy under that root is the domain hierarchy for the layer's contribution to the catalog. At request time, the registry composes the caller's effective view across every visible layer.

## 4.3 Artifact Manifest Schema

The manifest frontmatter is YAML; the prose body is markdown. The registry indexes frontmatter for `search_artifacts` and `load_domain`. The prose body is returned inline by `load_artifact`.

### Universal fields (any artifact type)

```yaml
---
type: skill | agent | context | prompt | mcp-server | <extension type>
name: run-variance-analysis
version: 1.0.0 # semver, author-chosen
description: One-line "when should I use this?"
when_to_use:
  - "After month-end close, to flag unusual variance vs. forecast"
tags: [finance, close, variance]
sensitivity: low | medium | high # informational; not enforced by the registry
license: MIT # SPDX identifier
search_visibility: indexed | direct-only # default: indexed
deprecated: false # set to true to mark this version deprecated
replaced_by: finance/close-reporting/run-variance-analysis-v2 # optional, suggested upgrade target
release_notes: "Initial release."
---
```

### Caller-interpreted fields (stored verbatim; consumed by the host)

```yaml
mcpServers:
  - name: finance-warehouse
    transport: stdio
    command: npx
    args: ["-y", "@company/finance-warehouse-mcp"]

requiresApproval:
  - tool: payment-submit
    reason: irreversible

runtime_requirements:
  python: ">=3.10"
  node: ">=20"
  system_packages: []

sandbox_profile: unrestricted | read-only-fs | network-isolated | seccomp-strict

sbom: # CycloneDX or SPDX inline or referenced
  format: cyclonedx-1.5
  ref: ./sbom.json
```

### Type-specific fields

```yaml
# For type: agent — declared input/output schemas
input: { $ref: ./schemas/input.json }
output: { $ref: ./schemas/output.json }

# For type: agent — well-known delegation targets (constrained to agent-type)
delegates_to:
  - finance/procurement/vendor-compliance-check@1.x

# For type: prompt — opt-in projection as MCP prompt (see §5.2)
expose_as_mcp_prompt: true

# For type: mcp-server — canonical server identifier (drives reverse index)
server_identifier: npx:@company/finance-warehouse-mcp

# Inheritance — explicitly extend another artifact's manifest (cross-layer merge)
extends: finance/ap/pay-invoice@1.2

# Adapter targeting — opt out of cross-harness materialization for this artifact
target_harnesses: [claude-code, opencode]
```

### External resources

For artifacts that need bytes too large to bundle:

```yaml
external_resources:
  - path: ./model.onnx
    url: s3://company-models/variance/v1/model.onnx
    sha256: 9f2c...
    size: 145000000
    signature: "sigstore:..."
```

The registry stores the URL + hash + size + signature, not the bytes.

## 4.4 Bundled Resources

Bundled resources ship with the artifact package and are discovered implicitly from the directory: every file under the artifact's root other than `ARTIFACT.md` is a bundled resource. There is no `resources:` list in frontmatter — what's in the folder ships, and the manifest references files inline in prose.

The registry stores bundled resources content-addressed by SHA-256 in object storage; bytes are deduplicated across all artifact versions within an org's storage namespace. Presigned URLs deliver them at load time.

At materialization (§6.6), resources land at a host-supplied path. The Podium MCP server downloads each resource and writes it atomically (`.tmp` + rename) so partial downloads cannot corrupt a working set.

The ingest-time linter validates that prose references in `ARTIFACT.md` resolve to:

- Bundled files (existence check)
- URLs (HTTP HEAD returns 200/3xx)
- Other artifacts (registry-side resolution against current visible catalog)

Drift between manifest text and bundled files is an ingest error.

**Trust model.** Bundled scripts inherit the artifact's sensitivity label. A high-sensitivity skill that bundles a Python script is effectively shipping code into the host; pre-merge CI run by the source repository (secret scanning, static analysis, SBOM generation, optional sandbox policy review) takes bundled scripts seriously.

### 4.4.1 Execution Model Contract

The MCP server materializes scripts; the host's runtime executes them. Authors declare runtime expectations in `runtime_requirements:`:

```yaml
runtime_requirements:
  python: ">=3.10"
  node: ">=20"
  system_packages: ["jq", "curl"]
```

Adapters surface these requirements to the host where supported. Hosts that cannot satisfy a requirement reject the artifact at load time with `materialize.runtime_unavailable`.

The `sandbox_profile:` field declares execution constraints:

| Profile            | Meaning                                                                |
| ------------------ | ---------------------------------------------------------------------- |
| `unrestricted`     | No sandbox constraints. Default for low-sensitivity.                   |
| `read-only-fs`     | Filesystem is read-only outside the materialization destination.       |
| `network-isolated` | No outbound network.                                                   |
| `seccomp-strict`   | Strict syscall allowlist (per a baseline profile shipped with Podium). |

Hosts with sandbox capability honor the profile; hosts without it MUST refuse to materialize an artifact whose `sandbox_profile != unrestricted` unless explicitly configured to ignore (with a loud warning logged).

### 4.4.2 Content Provenance

Prose in artifact manifests can declare its provenance to enable differential trust at the host:

```markdown
---
source: authored
---

<authored prose>

<!-- begin imported source="https://wiki.example.com/policy/payments" -->
<imported text>
<!-- end imported -->
```

Adapters propagate provenance markers to harnesses that support trust regions (e.g., Claude's `<untrusted-data>` convention). Hosts can apply differential trust — e.g., quote imported content as data rather than treating it as instruction. This is the primary defense against prompt injection from manifests that aggregate external content.

## 4.5 Domain Organization

A domain is a directory in the registry. Its members at discovery time are: every artifact directly under that directory, every subdirectory that itself qualifies as a domain, and (optionally) anything brought in by an explicit import. Domain composition is configured by an optional `DOMAIN.md` at the directory root.

### 4.5.1 DOMAIN.md

```markdown
---
unlisted: false
description: "AP-related operations"

include:
  - finance/ap/pay-invoice
  - finance/ap/payments/*
  - finance/refunds/**
  - _shared/payment-helpers/*
  - _shared/regex/{ssn,iban,routing-number}

exclude:
  - finance/ap/internal/**
---

# Accounts Payable

...
```

A domain folder without a `DOMAIN.md` is a regular navigable domain by default. The file is only needed to import from elsewhere, exclude paths, set the description, or mark the folder as unlisted.

### 4.5.2 Imports and Globs

Glob syntax: `*` (one segment), `**` (recursive), `{a,b,c}` (alternatives). `exclude:` is applied after `include:`.

**Resolution at `load_domain` time.** "Effective view" is context-dependent:

- A remote `DOMAIN.md`'s globs resolve over the registry view (layers org + team + user).
- A local `DOMAIN.md`'s globs resolve over the merged view (layers org + team + user + local).

This asymmetry exists because the workspace local overlay is merged client-side (§6.4); the registry doesn't see it.

Imports are dynamic: an artifact added at `finance/ap/payments/new-thing/` is automatically picked up by any domain whose `DOMAIN.md` includes `finance/ap/payments/*` — no `DOMAIN.md` re-ingest needed.

**Imports do not change canonical paths.** An artifact has exactly one canonical home (the directory where its `ARTIFACT.md` lives). Imports add additional appearances under other domains. `search_artifacts` returns the artifact once, with its canonical path and (optionally) the list of domains that import it.

**Authoring rights for imports.** Editing a domain's `include:`/`exclude:` requires write access to the layer that contains the destination `DOMAIN.md` (a Git merge or a `local`-source filesystem write). Importing does not require any rights in the source path — only that the artifact resolves under some layer the registry has ingested. Visibility at read time is enforced per layer.

**Cycle detection.** Two domains importing each other is allowed but lint-warned.

**Validation.** Imports that don't currently resolve in any view the registry knows about produce an ingest-time **warning**, not an error. This handles "expected to be defined in another layer later" without coordinated ingests.

### 4.5.3 Unlisted Folders

Setting `unlisted: true` in a folder's `DOMAIN.md` removes that folder and its entire subtree from `load_domain` enumeration. Artifacts inside still:

- Are reachable via `load_artifact(<id>)` if the host has visibility.
- Appear in `search_artifacts` results normally (subject to per-artifact `search_visibility:`).
- Can be imported into other domains via `include:`.

`unlisted: true` propagates to the whole subtree.

### 4.5.4 DOMAIN.md Across Layers

If multiple layers contribute a `DOMAIN.md` for the same path, the registry merges them:

- `description` and prose body — last-layer-wins.
- `include:` — additive across layers.
- `exclude:` — additive across layers; applied after the merged include set.
- `unlisted` — most-restrictive-wins.

When a workspace-local-overlay `DOMAIN.md` is involved, the MCP server applies the merge client-side after the registry returns its result for the registry-side layers.

## 4.6 Layers and Visibility

### Terminology

- **Layer** — a unit of composition. Each layer has a single **source** (a Git repo or a local filesystem path) and a **visibility** declaration.
- **Effective view** — the composition of every layer the caller's identity is entitled to see, in precedence order.

### The layer list

Layers are an explicit, ordered list configured per tenant. There is no fixed `org / team / user` hierarchy: the ordering is whatever the registry config says, and a deployment can have any number of layers.

Three classes of layers exist:

1. **Admin-defined layers** — declared in the registry config by tenant admins.
2. **User-defined layers** — registered at runtime by individual users via the CLI/API (§7.3.1). Each user-defined layer is visible only to the user who registered it.
3. **Workspace local overlay** — the per-workspace `.podium/overlay/` directory read by the MCP server's `LocalOverlayProvider` (§6.4). Always highest precedence in the user's effective view.

Composition order (lowest to highest precedence):

1. Admin-defined layers, in the order they appear in the registry config.
2. User-defined layers belonging to the caller, in the user-controlled order returned by `podium layer list`.
3. The workspace local overlay (when configured).

Higher-precedence layers override lower on collisions. Resolution of layers 1 and 2 happens at the registry on every `load_domain`, `search_artifacts`, and `load_artifact` call; layer 3 is merged in by the MCP server before returning results.

### Source types

Two source types are supported:

- **`git`** — a remote Git repository at a tracked ref, optionally rooted at a subpath. The registry ingests on webhook (§7.3.1).
- **`local`** — a filesystem path readable by the registry process. Re-scanned on demand via `podium layer reingest <id>`. Intended for standalone and small-team installations where the registry runs alongside the author.

### Visibility

Each layer declares one or more of the following:

| Field                         | Effect                                     |
| ----------------------------- | ------------------------------------------ |
| `public: true`                | Anyone, including unauthenticated callers. |
| `organization: true`          | Any authenticated user in the tenant org.  |
| `groups: [<oidc-group>, ...]` | Members of the listed OIDC groups.         |
| `users: [<user-id>, ...]`     | Listed user identifiers (OIDC subject).    |

Multiple fields combine as a union — a caller sees the layer if any condition matches. User-defined layers (§7.3.1) have implicit visibility `users: [<registrant>]`; the field is set automatically and cannot be widened.

Read-side enforcement happens at the registry on every call. Git provider permissions are not consulted at request time — visibility is governed entirely by the registry config (or, for user-defined layers, by the registration record).

**Public-mode bypass.** When the registry is started with `--public-mode` (§13.10), the visibility evaluator short-circuits to `true` for every layer and every caller. `visibility:` declarations stay in config (so artifacts remain portable to non-public deployments) but are not enforced at request time. Public mode is mutually exclusive with an identity provider — see §13.10 for the safety constraints.

**Filesystem-registry bypass.** With a filesystem-source registry (§13.11) there is no identity, so the visibility evaluator short-circuits to `true` for every layer. `visibility:` declarations stay in the layer config (artifacts remain portable to server-source deployments) but are not enforced.

Authoring rights are out of Podium's scope. Whoever can merge to the tracked Git ref publishes; whoever can write to the `local` filesystem path publishes there. Teams configure branch protection, required reviewers, and signing requirements in their Git provider as they see fit. Podium reads no in-repo permission files.

### Config schema

```yaml
# Registry config (per tenant)
layers:
  - id: org-defaults
    source:
      git:
        repo: git@github.com:acme/podium-org-defaults.git
        ref: main
        root: artifacts/
    visibility:
      organization: true

  - id: team-finance
    source:
      git:
        repo: git@github.com:acme/podium-finance.git
        ref: main
    visibility:
      groups: [acme-finance, acme-finance-leads]

  - id: platform-shared
    source:
      git:
        repo: git@github.com:acme/podium-platform.git
        ref: main
    visibility:
      groups: [acme-engineering]
      users: [security-lead@acme.com]

  - id: public-marketing
    source:
      git:
        repo: git@github.com:acme/podium-public.git
        ref: main
    visibility:
      public: true

  - id: dev-finance
    source:
      local:
        path: /var/podium/dev/podium-finance
    visibility:
      users: [joan@acme.com]
```

### Merge semantics for collisions

If two layers contribute artifacts with the same canonical ID:

- A collision is rejected at ingest **unless** the higher-precedence artifact declares `extends: <lower-precedence-id>` in frontmatter.
- When `extends:` is declared, fields merge per the table below.

`extends:` is a single scalar (no multiple inheritance). Cycle detection at ingest time. Parent version is resolved at the child's ingest time and pinned (parent updates do not silently propagate; the child must be re-ingested to pick up changes).

To intentionally replace an artifact rather than extend it, the lower-precedence layer must remove it first or rename the higher-precedence one. Silent shadowing is never permitted.

**Hidden parents.** When a child manifest declares `extends: <parent>` and the requesting identity cannot see the layer that contributes the parent, the registry resolves and merges the parent server-side and serves the merged manifest. The parent's existence and ID are not surfaced to the requester. This preserves layer privacy and keeps the consumer interface uniform regardless of layer membership.

### Field semantics table

| Field                                  | Merge semantics                                            |
| -------------------------------------- | ---------------------------------------------------------- |
| `description`, `name`, `release_notes` | Scalar; child wins                                         |
| `tags`                                 | List; append unique                                        |
| `when_to_use`                          | List; append                                               |
| `sensitivity`                          | Scalar; most-restrictive (high > medium > low)             |
| `mcpServers`                           | List of objects; deep-merge by `name`                      |
| `requiresApproval`                     | List; append                                               |
| `runtime_requirements`                 | Map; deep-merge with child wins                            |
| `sandbox_profile`                      | Scalar; most-restrictive                                   |
| `delegates_to`                         | List; append                                               |
| `external_resources`                   | List; append                                               |
| `license`                              | Scalar; child wins (lint warning if changed across layers) |
| `search_visibility`                    | Scalar; most-restrictive (`direct-only` > `indexed`)       |

Extension types register their own field semantics via `TypeProvider`.

## 4.7 Registry as a Service

The registry is a deployable service. The on-disk layout described above (§4.2–§4.5) is the **authoring** model; layers (§4.6), access control (§4.7.2), and the runtime model below are how the service serves requests. The runtime model has four pieces — three persistent stores plus the API front door:

- **Metadata store (Postgres in standard, SQLite in standalone).** Manifest metadata, descriptors, layer config, admin grants, user-defined-layer registrations, dependency edges, deprecation status, and audit log. Pluggable via `RegistryStore` (§9.1).
- **Vector store.** `pgvector` collocated in Postgres (standard default) or `sqlite-vec` collocated in SQLite (standalone default). Pluggable via `RegistrySearchProvider` (§9.1) to a managed service (Pinecone, Weaviate Cloud, Qdrant Cloud); when a managed backend is configured, embeddings move out of the metadata store and the registry assumes responsibility for dual-write consistency.
- **Object storage.** Bundled resource bytes per artifact version, fronted by presigned URL generation. Versioned: each artifact version is immutable.
- **HTTP/JSON API.** Stateless front door. Accepts OAuth-attested identity, composes the caller's effective view from the layer list, applies per-layer visibility, queries the metadata and vector stores, signs URLs, returns responses.

### Version immutability invariant

A `(artifact_id, version)` pair, once ingested, is bit-for-bit immutable forever in the registry's content store. Subsequent commits in a layer's source that change the same `version:` with different content are rejected at ingest. Readers in flight when a re-ingest occurs continue to see their pinned version.

Force-push or history rewrite at the source does not break the invariant: previously-ingested commits' bytes are preserved in the content-addressed store, and the registry emits a `layer.history_rewritten` event for the operator. Strict mode is configurable per layer (§7.3.1).

### Embedding generation

Hybrid retrieval (BM25 + vectors via RRF) needs an embedding for every artifact and for each `search_artifacts` query. The registry computes both.

**What gets embedded.** A canonical text projection per artifact, built from frontmatter only:

- `name`
- `description`
- `when_to_use` (joined with newlines)
- `tags` (joined)

The prose body of `ARTIFACT.md` is **not** embedded. It's noisy for retrieval and risks busting embedding-model context limits at the long-tail end. Authors who want richer search recall put discoverability content in `description` and `when_to_use`. The same projection is applied to `search_artifacts` queries when the caller passes a text `query` (the `query` is treated as a free-text search target, not concatenated with the projection).

**Where embeddings come from.** Two cases, determined by the configured `RegistrySearchProvider`:

1. **Self-embedding backend** — Pinecone Integrated Inference, Weaviate Cloud with a vectorizer, Qdrant Cloud Inference, and similar. The registry passes the text projection to the backend; the backend computes and stores the embedding inline. No external `EmbeddingProvider` required.
2. **Storage-only backend** — pgvector, sqlite-vec, plain Qdrant, plain Weaviate without a vectorizer. The registry calls a configured `EmbeddingProvider` to compute the vector, then writes the vector to the backend.

In either case, an `EmbeddingProvider` can be **explicitly configured** to override the backend's hosted model — useful when an existing corpus is already embedded with a specific model and you want continuity, or when you want a model the backend doesn't host.

**Built-in `EmbeddingProvider` implementations** (selected via `PODIUM_EMBEDDING_PROVIDER`):

| Value                                  | Model defaults                               | Notes                                                                                                           |
| -------------------------------------- | -------------------------------------------- | --------------------------------------------------------------------------------------------------------------- |
| `embedded-onnx` _(standalone default)_ | `bge-small-en-v1.5` (384 dimensions, ~30 MB) | Bundled ONNX model running in-process. No external service.                                                     |
| `openai` _(standard default)_          | `text-embedding-3-small` (1536 dim)          | Requires `OPENAI_API_KEY`.                                                                                      |
| `voyage`                               | `voyage-3`                                   | Requires `VOYAGE_API_KEY`.                                                                                      |
| `cohere`                               | `embed-v4`                                   | Requires `COHERE_API_KEY`.                                                                                      |
| `ollama`                               | configurable                                 | Points at any Ollama endpoint (default `http://localhost:11434`). Useful for standalone + offline + air-gapped. |

Custom embedding providers register through the SPI as Go-module plugins.

**Model versioning and re-embedding.** The vector store records `(model_id, dimensions)` per artifact. When the configured embedding model changes — operator switches `EmbeddingProvider`, switches the self-embedding backend's hosted model, or upgrades to a new version of the same model — the registry triggers a background re-embed via `podium admin reembed` (`--all` or `--since <timestamp>`). During re-embedding, the vector store may transiently contain mixed dimensions; query-time the registry restricts results to vectors matching the currently-configured model and emits `embedding.reembed_in_progress` events for progress monitoring. Once re-embedding completes, stale-dimension rows are purged.

### Dual-write semantics for external vector backends

When `RegistrySearchProvider` is configured to a backend outside the metadata store (any managed service or a separate pgvector instance), the registry coordinates writes through a **transactional outbox**:

1. At ingest, the manifest commit and a `vector_pending` row land in the same `RegistryStore` transaction. The outbox row carries either the pre-computed vector (storage-only backends) or the canonical text projection (self-embedding backends).
2. A background worker drains the outbox by writing to the vector backend with exponential-backoff retry, marking each row complete on success.
3. Ingest itself never blocks on the external service. If the vector backend is down, ingest succeeds, the outbox grows, and the metadata store stays the source of truth.

While an outbox row is unresolved, the affected artifact remains discoverable via BM25 and direct `load_artifact` calls; only its semantic-search recall is degraded until the vector lands. Operators monitor outbox depth via a Prometheus gauge; a `vector.outbox_lagging` event fires when depth or oldest-row age exceeds an operator-configured threshold.

Self-embedding backends collapse the embedding step into the same call (text-in instead of vector-in), so they avoid a separate inference round-trip from the registry but the outbox semantics are otherwise identical.

The collocated defaults (pgvector, sqlite-vec) sidestep the outbox entirely — embeddings and metadata commit in a single database transaction.

### 4.7.1 Tenancy

The tenant boundary is the **org**. Each org has its own layer list (§4.6), its own admins, its own audit stream, and its own quotas. Org IDs are UUIDs; org names are human-readable aliases.

User identity comes from the configured identity provider (§6.3). Group membership comes from OIDC group claims and from SCIM 2.0 push (where the IdP supports it). Layer visibility (§4.6) references those groups and user identifiers directly — there is no Podium-side concept of "team" beyond what OIDC groups provide.

**Postgres isolation.** Each org has its own schema; cross-org tables (e.g., shared infrastructure metadata) use row-level security with org_id checks. Schema-per-org gives clean drop-org semantics, isolates query patterns, and bounds the blast radius of SQL injection.

#### 4.7.1.1 Data Residency

A deployment is single-region. Multi-region deployments run separate registries per region with no cross-region replication.

### 4.7.2 Access Control

Read access is governed by per-layer visibility (§4.6), enforced at the registry on every API call. There are no per-artifact roles. A caller sees a layer if its visibility declaration matches their identity (`public`, `organization`, an OIDC group claim, or an explicit user listing); the caller's effective view is the composition of every visible layer.

**Authoring rights are out of Podium's scope.** Whoever can merge to a layer's tracked Git ref publishes; whoever can write to a `local` source's filesystem path publishes there. Branch protection, required reviewers, signing requirements, and code ownership are configured by the team in their Git provider as they see fit. Podium reads no in-repo permission files.

**The `admin` role.** A single Podium-side role exists per tenant. Admins can:

- Add, remove, and reorder admin-defined layers in the registry config.
- Manage tenant-level settings (freeze windows, default user-layer cap, audit retention).
- Trigger manual reingests across any layer in the tenant.
- View any layer's contents for diagnostic purposes (override visibility — the override is itself audited).

Admin grants are stored as `(identity, org_id, "admin")` rows in Postgres and are managed via `podium admin grant` / `podium admin revoke`.

**Freeze windows.** Org-level config:

```yaml
freeze_windows:
  - name: "year-end-close"
    start: "2026-12-15T00:00:00Z"
    end: "2026-12-31T23:59:59Z"
    blocks: [ingest, layer-config]
    break_glass_role: admin
```

During a freeze, blocked operations are rejected unless `--break-glass` is passed. Break-glass requires dual-signoff (two admins), justification, auto-expires after 24h, and queues for post-hoc security review. `ingest` covers webhook-driven and manual reingests; `layer-config` covers admin layer-list edits.

### 4.7.3 Reverse Dependency Index

The registry indexes "X depends on Y" edges across artifacts:

- `extends:` chains
- `delegates_to:` references (constrained to `agent`-type targets)
- `mcpServers:` references that resolve to `mcp-server`-type artifacts via `server_identifier`

Tag co-occurrence is **not** a dependency edge (too noisy for impact analysis).

The index drives:

- **Impact analysis.** Before deprecating an artifact, list everything that depends on it.
- **Cascading review.** When a high-sensitivity dependency changes, flag downstream artifacts for re-review.
- **Search ranking signals.** Frequently-depended-on artifacts surface higher.

### 4.7.4 Classification and Lifecycle

Each artifact carries:

- **Sensitivity label.** `low` / `medium` / `high`, declared in frontmatter. Informational metadata exposed in `search_artifacts` and `load_artifact` responses for filtering and display. Reviewer requirements based on sensitivity are enforced in the Git provider's branch protection (e.g., path-scoped CODEOWNERS plus required-reviewer counts), not by the registry.
- **Ownership.** Authoring rights flow through the source layer's Git permissions. The artifact's manifest can name owners informationally for routing notifications via the `NotificationProvider` SPI (e.g., for vulnerability alerts and ingest failures).
- **Lifecycle.** An ingested artifact is live until a subsequent ingest sets `deprecated: true`. Deprecated artifacts return a warning when loaded and are excluded from default search results; if `replaced_by:` is set, the registry surfaces the upgrade target alongside the warning.

### 4.7.5 Audit

Every `load_domain`, `search_artifacts`, and `load_artifact` call is logged with caller identity, visibility outcome, requested artifact (or query), timestamp, resolved layer composition, and result size. Ingest events (success and failure), admin actions (layer-list edits, freeze-window toggles, admin grants), and break-glass invocations are also logged. Hosts keep their own audit streams for runtime events; Podium's audit stream stays focused on the catalogue. Detail in §8.

### 4.7.6 Version Resolution and Consistency

Versions are semver-named (`major.minor.patch`), author-chosen via the manifest's `version:` field. Internally, the registry stores `(artifact_id, semver, content_hash)` triples; content_hash is the SHA-256 of the canonicalized manifest + bundled resources.

Pinning syntax in references (`extends:`, `delegates_to:`, `mcpServers:`):

- `<id>` — resolves to `latest`.
- `<id>@<semver>` — exact version.
- `<id>@<semver>.x` — minor or patch range (e.g., `1.2.x`, `1.x`).
- `<id>@sha256:<hash>` — content-pinned.

`load_artifact(<id>)` resolves to `latest` = "the most recently ingested non-deprecated version visible under the caller's effective view, at resolution time." Resolution is registry-side.

For session consistency, the meta-tools accept an optional `session_id` argument (UUID generated by the host per agent session). The first `latest` lookup within a session is recorded and reused for all subsequent same-id lookups in that session — so the host sees a consistent snapshot.

**Inheritance and re-ingest.** When a child manifest declares `extends: <parent>` (no version pin), the parent version is resolved at the child's ingest time and stored as a hard pin in the ingested manifest's resolved form. Parent updates do not silently propagate; the child must be re-ingested (typically by bumping its `version:` and merging) to pick up changes.

### 4.7.7 Vulnerability Tracking

The registry consumes CVE feeds, walks SBOM dependencies declared in artifact frontmatter, and surfaces affected artifacts:

- `podium vuln list [--severity ...]` — list affected artifacts.
- `podium vuln explain <cve> <artifact>` — show the dependency path.
- Owners notified through configured channels (webhook / email / Slack via the `NotificationProvider` SPI).

Lint enforces SBOM presence for sensitivity ≥ medium.

### 4.7.8 Quotas

Per-org limits, admin-configurable: storage (bytes), search QPS, materialization rate, audit volume.

`podium quota` CLI surfaces current usage and limits. Quota exhaustion returns structured errors (`quota.storage_exceeded`, etc.).

### 4.7.9 Signing

Each artifact version is signed by the author's key at commit time, or by a registry-managed key at ingest. Two key models:

- **Sigstore-keyless** (preferred). OIDC-attested signature; transparency log entry; no key management.
- **Registry-managed key** (fallback). Per-org key managed by the registry; rotated quarterly.

Signatures are stored alongside content. The MCP server verifies signatures on materialization for sensitivity ≥ medium (configurable per deployment). Signature failure aborts materialization with `materialize.signature_invalid`.

`podium verify <artifact>` for ad-hoc verification. `podium sign <artifact>` for explicit signing outside the ingest flow.
