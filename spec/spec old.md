# podium — Technical Specification

## 1. Overview

### 1.1 What Podium Is

Podium is an **enterprise registry for inference-time agentic AI artifacts**:

- **Author once, load anywhere.**
  - Skills, agents, context bundles, prompts, and similar artifacts are authored in a single canonical format and translated at load time to your harness's native conventions.
  - Bundled resources can be any file type or combination: scripts, templates, schemas, JSON / YAML / Markdown, model files, datasets, binaries.
- **Designed for progressive disclosure at scale.**
  - Agents start with the top-level domains and drill in for more detail — a domain's subdomains, then the artifacts inside, going deeper as the task demands.
  - Search runs in parallel: when the agent already knows what it's looking for, it can query across the registry directly without browsing.
  - Only the artifact the agent decides to use is fully materialized, so the agent's context stays focused even when the registry holds thousands of skills and agent definitions.
- **Built for the enterprise.**
  - RBAC, layered authoring (org / team / user / local), classification, lifecycle, and audit baked in.
  - Domain owners curate what's in their domain — they can pull in artifacts from elsewhere in the registry, or from shared libraries meant for reuse across domains.
  - Workspace-local overlays let developers iterate on artifacts in their own session before publishing.
- **Server-side registry + client-side MCP server.**
  - Artifacts live in and are served from Podium's server. Callers run a thin MCP server alongside their own runtime that bridges to the registry, exposes discovery and artifact materialization meta-tools, and adapts the result to the caller's harness on the way out.
  - One MCP server binary works everywhere — pluggable identity for both developer hosts and managed agent runtimes.

#### Two podium components

1. **Registry service** — the system of record for artifacts. A control-plane MCP/HTTP API for manifests, search, and signed URLs; an object-storage data plane for resource bytes; a Postgres+pgvector store for metadata, dependency edges, embeddings, RBAC bindings, and audit. Resolves overlays per OAuth identity. Centralized and multi-tenant.
2. **Podium MCP server** — a single-shape binary the caller runs alongside its own runtime. Exposes the three meta-tools over MCP, forwards calls to the registry under the caller's OAuth-attested identity, materializes bundled resources onto the caller's filesystem at load time, and runs the configured `HarnessAdapter` to translate the canonical artifact into the caller's harness-native format. Stateless. Holds no credentials. Pluggable interfaces let the same binary serve every deployment context: an `IdentityProvider` decides where the OAuth identity comes from (interactive device-code flow with OS keychain, or a token injected by a managed runtime), an optional `LocalOverlayProvider` adds a workspace `local` overlay layer when configured, and the `HarnessAdapter` does load-time format translation per harness.

Sessions, runtime execution, policy compilation, and downstream tool wiring are concerns that belong to callers.

### 1.2 Problem Statement

Organizations adopting AI accumulate large libraries of authored content. As the catalogue grows past a few hundred items, several problems emerge together:

1. **Capability saturation.** Exposing thousands of skills, prompts, or tool definitions to a model degrades planning quality. Callers need to see only what's relevant.
2. **Discoverability at scale.** A multi-domain catalogue with thousands of items shared across many teams needs a structured discovery model. A flat list does not work.
3. **Role-based access control.** Different users have different rights: who can read an artifact, who can publish or update one, who can deprecate, who can manage memberships and overlays. The registry is the central enforcement point.
4. **Layered authoring.** Org / team / user / workspace contributions need to compose deterministically with clear precedence and no silent shadowing.
5. **Governance, classification, lifecycle.** Sensitivity labels, review status, ownership, deprecation paths, and reverse-dependency impact analysis need to be first-class.
6. **Type heterogeneity.** Skills, agents, context bundles, prompts, tool registrations, workflows, eval datasets, model files — every AI artifact type fits in one registry, with one storage and discovery model.
7. **Heterogeneous callers.** Agent runtimes, build pipelines, terminal CLIs, evaluation harnesses, and other AI systems all read from the same catalogue; none should need its own copy.

Podium addresses these together: a centralized registry service plus a thin MCP server callers load alongside their runtime.

### 1.3 Design Principles

- **Progressive disclosure.** Sessions start empty. The caller sees only a high-level map; navigation, search, and load surface what's needed when it's needed (§3).
- **RBAC at the registry.** Scope claims (org, team, user) and roles (reader, publisher, reviewer, owner, admin) live in the registry and travel on every OAuth-attested call. The registry is the only enforcement point for visibility and authoring rights.
- **The registry is a deployable service.** Authoring lives in Git; the runtime is a service (control plane MCP API + object store + Postgres+pgvector). Updates take effect on the next call.
- **Type-agnostic discovery.** The registry defines an artifact type system (`skill` / `agent` / `context` / `prompt` / `tool` / `workflow`, extensible) and treats every type uniformly for discovery, search, and load. Type-specific runtime behaviour lives in callers.
- **Any file type or combination of file types.** Manifests are markdown with YAML frontmatter; bundled resources alongside are arbitrary files. The registry stores them as opaque versioned blobs and serves them via presigned URLs.
- **One MCP server, pluggable identity.** A single binary serves every deployment context. Identity is selected by configuration (OAuth device-code on developer hosts; injected session token in managed runtimes; additional providers register through the interface).
- **Materialization on the caller's filesystem.** `load_artifact` lazily downloads bundled resources to a caller-configured destination path on the host, atomically. The catalogue lives at the registry; the working set lives on the host.

### 1.4 Constraints and Decisions

| Decision | Rationale |
| --- | --- |
| Two podium components: registry service, MCP server | The full surface area: a centralized registry with persistence and a thin loopback bridge callers run alongside their runtime. |
| MCP server is a single shape with pluggable identity, overlay, and harness adapter | One binary serves every deployment context. Identity providers (OAuth device-code + OS keychain for developer hosts; injected session token for managed runtimes), the workspace `local` overlay, and the harness adapter that translates canonical artifacts to the caller's harness-native format on the way out are all selected via configuration. |
| Author once, load anywhere | Artifacts have one canonical authored form. At materialization time, the configured `HarnessAdapter` translates them into the harness's native shape (Claude Code, Claude Desktop, Cursor, Gemini, OpenCode, Codex, or `none` for raw). Authors don't fork artifacts per harness. |
| Sessions start empty; discovery via meta-tools | Each session begins with zero artifact content. The caller calls `load_domain` / `search_artifacts` / `load_artifact` to assemble its working set on demand. |
| Multiple overlays per identity (org / any number of teams / optional user / optional local) | Layers compose at the registry from the caller's OAuth identity. The MCP server adds the workspace `local` layer when its `LocalOverlayProvider` is configured. Any number of `team:<name>` claims compose. Collisions: most-restrictive-wins for security fields, last-overlay-wins for descriptions, explicit `extends:` to inherit. |
| RBAC enforced at the registry on every call | Roles bind to identities per scope (org, team). Permissions cover read, publish, review, deprecate, manage members, manage overlays, admin. Every API call is checked. |
| Registry as a deployable service | Authoring lives in Git; runtime is a service (control plane MCP API + object store + Postgres+pgvector). |
| PostgreSQL + pgvector for the registry | Manifest metadata, dependency edges, embeddings for `search_artifacts`, RBAC bindings, registry-side audit. Pluggable interface for alternatives. |
| Per-workspace MCP server lifecycle on developer hosts | When the MCP server runs as a developer-side subprocess, the caller spawns one per workspace, over stdio. Local overlay is workspace-scoped (`.podium/overlay/`). Cache lives in `~/.podium/cache/` and is content-addressed across workspaces. |
| Artifacts can be any file type | Manifest is markdown + YAML frontmatter; bundled resources alongside are arbitrary files. Resource size limits apply (~10 MB per package, ~1 MB per file as a soft cap; presigned-URL data plane handles delivery). |

---

## 2. Architecture

### 2.1 High-Level Component Map

A single centralized registry service serves every caller. The podium MCP server is a thin binary the caller runs alongside its own runtime, wherever that runtime lives. The same MCP server binary serves every deployment context; differences are configuration only.

```
                          ┌───────────────────────────┐
                          │ PODIUM REGISTRY (service) │
                          │  control plane (MCP/HTTP) │
                          │  data plane (object store)│
                          │  Postgres + pgvector      │
                          │  RBAC + overlays          │
                          │  (centralized,            │
                          │   multi-tenant)           │
                          └───────────▲───────────────┘
                                      │
                       OAuth-attested │ identity
                                      │
                          ┌───────────┴───────────────┐
                          │ Podium MCP server         │
                          │   load_domain ·           │
                          │   search_artifacts ·      │
                          │   load_artifact           │
                          │ + IdentityProvider        │
                          │ + LocalOverlayProvider    │
                          │ + HarnessAdapter          │
                          │   (translate canonical    │
                          │   → harness-native)       │
                          │ + content-addressed cache │
                          │ + materialization         │
                          │   (atomic write to        │
                          │   caller-configured path) │
                          └───────────▲───────────────┘
                                      │ MCP (stdio)
                                      │
                          ┌───────────┴───────────────┐
                          │ Caller's runtime          │
                          │ (any AI system loading    │
                          │  artifacts on demand)     │
                          └───────────────────────────┘
```

Two deployment scenarios use the same MCP server binary:

- **Managed agent runtime.** The runtime spawns the MCP server as a co-located process. Identity is supplied by the runtime via an injected session token; the registry endpoint is configured the same way. The `local` overlay is unset.
- **Developer's host.** The caller spawns one MCP server per workspace as a stdio subprocess. The MCP server uses an OAuth device-code flow on first use to obtain a registry token, stored in the OS keychain. The workspace `local` overlay reads from `${WORKSPACE}/.podium/overlay/`.

The registry is the same service in both scenarios.

### 2.2 Component Responsibilities

**Podium Registry** _(centralized service)_. The system of record for artifacts. Resolves overlays per OAuth identity, enforces RBAC, indexes manifests, runs hybrid search, signs URLs for resource bytes. Three persistent stores: Postgres + pgvector (metadata, dependency edges, embeddings, RBAC bindings, registry-side audit), object storage (resource bytes), MCP/HTTP API (stateless front door). The same service backs every caller.

**Podium MCP server** _(thin loopback bridge)_. Single binary. Exposes the three meta-tools (`load_domain`, `search_artifacts`, `load_artifact`). Stateless. Holds no credentials. Materializes bundled resources atomically on the caller's filesystem when `load_artifact` is invoked, translating canonical artifacts to the caller's harness-native format on the way out. Pluggable interfaces:

- **IdentityProvider** — supplies the OAuth-attested identity attached to every registry call. Built-in implementations: `oauth-device-code` (interactive flow on first use; tokens cached in OS keychain) and `injected-session-token` (token supplied by the runtime via env var or file path; refreshed by the runtime). Additional implementations register through the interface.
- **LocalOverlayProvider** — optional. When configured, reads `ARTIFACT.md` packages from a workspace filesystem path and merges them as the `local` overlay layer (§4.6).
- **HarnessAdapter** — translates canonical artifacts into harness-native format at materialization time. Built-in implementations cover Claude Code, Claude Desktop, Cursor, Gemini, OpenCode, and Codex; `none` (the default) writes the canonical layout as-is for callers that want raw artifacts. See §6.7.

Configuration: env vars, command-line flags, or a config file the caller supplies. See §6.

**Callers** _(not podium components)_. Any AI system that needs the catalogue. Callers spawn the podium MCP server alongside their own runtime tools, configure its identity provider and (optionally) its local overlay, and use the meta-tools.

---

## 3. Progressive Disclosure

### 3.1 The Problem

Capability saturation: if a model sees 500 tools or skills at once, planning quality degrades. The classical answer — "show only what you need" — is harder than it sounds when the catalog is large, multi-domain, and shared across teams. Pre-loading everything fails at the model. Pre-loading nothing fails at the user. Discovery has to be staged.

### 3.2 Five Layers of Disclosure

Podium uses five layers, each one narrowing what the caller must consider:

**Layer 0 — Scope filtering.** Every request to the registry carries the caller's identity (via OAuth) and the resolved scope claims for that identity. The registry filters its catalog to only those artifacts the scope claims and RBAC bindings grant. The caller sees only the filtered catalog; the filtering happens at the registry on every call.

**Layer 1 — Hierarchical map.** Within the visible catalog, the caller calls `load_domain(path)` to get a map of what exists. With no path, the map describes top-level domains. With a path like `finance`, it describes that domain's subdomains and key artifacts. With a deeper path, it returns the leaf set. The hierarchy is two levels deep by default — a third level kicks in only when a domain crosses ~1000 artifacts. The directory layout drives the domain hierarchy (§4.2); a domain's children may be augmented or curated by an optional `DOMAIN.md` config that imports artifacts from elsewhere (§4.5). Multi-membership is allowed: one artifact can show up under more than one domain via imports, and unlisted folders host shared artifacts that exist to be imported.

**Layer 2 — Search.** When the caller has the right neighborhood but doesn't know which artifact, it calls `search_artifacts(query, scope?)`. The registry runs a hybrid retriever (BM25 + embeddings, fused via reciprocal rank) over manifest text, returning a ranked list of `(artifact_id, summary, score)` tuples. Search returns descriptors only.

**Layer 3 — Load on demand.** When the caller has chosen an artifact, it calls `load_artifact(artifact_id)`. The registry returns the manifest body inline; bundled resources are materialized lazily on the caller's filesystem and large blobs are delivered via presigned URLs.

**Layer 4 — Description quality.** Layers 1 and 2 only work if manifests describe themselves well. Manifest authoring is a first-class concern: each artifact's `description` field must answer "when should I use this?" in one or two sentences. The registry lints for thin descriptions and flags clusters of artifacts whose summaries collide.

**Layer 5 — Learn from usage.** The registry observes which artifacts actually get loaded after which queries, and uses that signal to (a) rerank search results, (b) suggest import candidates to domain owners ("this artifact is repeatedly loaded after queries in the `finance` domain — consider importing it"), and (c) flag artifacts whose authored descriptions underperform retrieval expectations.

### 3.3 Discovery Flow

A typical caller session begins empty. The caller calls `load_domain()` to get the top-level map. It either picks a domain and calls `load_domain("<domain>")` for the next level, or — if the request is specific enough — jumps straight to `search_artifacts`. When it has an artifact ID, it calls `load_artifact`, which materializes the package on the host (§6.6).

The capability surface grows with the task. Only `load_artifact` writes to the host filesystem. The catalog lives at the registry; the working set lives on the host.

---

## 4. Artifact Model

### 4.1 Artifacts Are Packages of Arbitrary Files

An artifact is a directory with a manifest at its root. The manifest — `ARTIFACT.md` — is a markdown file with YAML frontmatter and prose. Frontmatter is what the registry indexes; prose is what the caller reads when the artifact is loaded.

**Bundled resources alongside the manifest are arbitrary files.** Python scripts, shell scripts, templates (Jinja, Handlebars, …), JSON / YAML schemas, evaluation datasets, model weights, binary blobs — anything the caller needs at runtime. The registry treats these as opaque versioned blobs and serves them via presigned URLs. There is no per-file-type handling at the registry level; the caller (or the artifact's intended runtime) interprets them.

Every artifact's frontmatter declares its `type`. The first-class types are:

- `skill` — instructions (+ optional scripts) loaded into the host agent's context on demand.
- `agent` — a complete agent definition meant to run in isolation as a delegated child.
- `context` — pure reference material (style guides, glossaries, API references, large knowledge bases).
- `prompt` — parameterized prompt templates the agent or a human can instantiate.
- `tool` — an MCP tool/server registration (name, endpoint, auth profile, description).
- `workflow` — ordered, multi-step procedures that orchestrate skills or agents.

The type system is extensible: registry deployments can register additional types (e.g., `dataset`, `model`, `eval`, `policy`) with their own lint rules. Podium treats every type uniformly for discovery, search, and load; type-specific runtime behaviour lives in callers.

The type determines indexing, loading semantics, governance requirements, and search ranking. A `context` artifact does not need the same safety review as a `skill` because instructions are more dangerous than reference data.

**Manifest size lint.** A reasonable cap is ~20K tokens of manifest content. Larger reference content should be factored out as a separate `type: context` artifact; the lint rule is "if your manifest is large, the reviewer will ask whether some of it should be a context artifact instead."

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

The manifest is what gets indexed for discovery. The other files are resources that travel with it.

**Resource size limits.** Reasonable defaults: ~10 MB total per package, individual files capped at ~1 MB as a soft cap. Larger than that and the registry is being misused as a data store; reference large data via URL from the manifest instead, or use a higher-cap deployment configuration. Resources are immutable per version: bundling them with the manifest avoids drift between what the manifest documents and what the script does.

### 4.2 Registry Layout on Disk

The registry's authoring layout is a domain hierarchy. Directories are domain paths and the leaves are artifact packages. Optional `DOMAIN.md` files configure how a domain composes (imports, exclusions, listed/unlisted state, description); they are described in §4.5.

```
registry/
├── registry.yaml
├── company-glossary/
│   └── ARTIFACT.md
├── helpdesk-router/
│   └── ARTIFACT.md
├── finance/
│   ├── DOMAIN.md
│   ├── ap/
│   │   ├── DOMAIN.md
│   │   ├── pay-invoice/
│   │   │   └── ARTIFACT.md
│   │   ├── invoice-lookup/
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
├── compliance/
│   └── audit-trails/
│       └── DOMAIN.md             # imports cross-cutting artifacts from elsewhere
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

Org/team/user overlays are layers in the registry service indexed by scope claim (§4.6). At request time, the registry composes the effective view from all layers the caller's identity entitles them to.

### 4.3 Artifact Manifest Schema

Frontmatter fields the registry recognizes (any artifact type may use any subset):

```yaml
---
type: skill | agent | context | prompt | tool | workflow | <extension type>
name: run-variance-analysis
description: One-line "when should I use this?"
when_to_use:
  - "After month-end close, to flag unusual variance vs. forecast"
tags: [finance, close, variance]
sensitivity: low | medium | high

# Caller-interpreted fields (stored verbatim; consumed by the caller's runtime)
mcpServers:
  - name: finance-warehouse
    transport: stdio
    command: npx
    args: ["-y", "@company/finance-warehouse-mcp"]

requiresApproval:
  - tool: payment-submit
    reason: irreversible

# For type: agent — declared input/output schemas
input: { $ref: ./schemas/input.json }
output: { $ref: ./schemas/output.json }

# For type: agent — well-known delegation targets (drives the registry's reverse dep index)
delegates_to:
  - finance/procurement/vendor-compliance-check

# Inheritance — explicitly extend another artifact's manifest (overlay merge)
extends: finance/ap/pay-invoice
---
<prose body>
```

Frontmatter is YAML; the prose body is markdown. The registry indexes frontmatter for `search_artifacts` and `load_domain`. The prose body is returned inline by `load_artifact`.

Some fields (e.g., `mcpServers`, `requiresApproval`, `input/output` schemas) are stored verbatim in the manifest and interpreted by the caller's runtime. Podium lints them at publish time and indexes them for the reverse-dependency index, leaving runtime interpretation to the caller.

### 4.4 Bundled Resources

Bundled resources ship with the artifact package and are discovered implicitly from the directory: every file under the artifact's root other than `ARTIFACT.md` is a bundled resource. There is no `resources:` list in frontmatter — what's in the folder ships, and the manifest references files inline in prose.

Resources can be any file type or combination: scripts, templates, schemas, JSON, YAML, CSV, Markdown, binary blobs, model files, datasets. The registry treats them as opaque versioned blobs and stores them in object storage; presigned URLs deliver them at load time.

At materialization (§6.6), resources land at a caller-supplied path on the host. The podium MCP server downloads each resource and writes it atomically (`.tmp` + rename) so partial downloads cannot corrupt a working set.

A publish-time linter validates that prose references in `ARTIFACT.md` resolve to real files in the package. Drift between manifest text and bundled files is a publish error.

**Trust model.** Bundled scripts inherit the artifact's sensitivity label. A high-sensitivity skill that bundles a Python script is effectively shipping code into the caller; publish-time CI (secret scanning, static analysis, optional sandbox policy review) takes bundled scripts seriously.

### 4.5 Domain Organization

A domain is a directory in the registry. Its members at discovery time are: every artifact directly under that directory, every subdirectory that itself qualifies as a domain, and (optionally) anything brought in by an explicit import. Domain composition is configured by an optional `DOMAIN.md` at the directory root.

#### 4.5.1 DOMAIN.md

`DOMAIN.md` is a markdown file with YAML frontmatter, parallel to `ARTIFACT.md`. Frontmatter holds the domain's configuration; the prose body is a human-readable description surfaced in `load_domain()` output (and lints for description quality the same way artifacts do).

```markdown
---
unlisted: false                     # default false; see §4.5.3
description: "AP-related operations" # optional one-liner; defaults to first prose paragraph

include:                            # optional; see §4.5.2
  - finance/ap/pay-invoice          # explicit artifact path
  - finance/ap/payments/*           # one-level glob (immediate children)
  - finance/refunds/**              # recursive glob
  - _shared/payment-helpers/*       # imports from an unlisted folder
  - _shared/regex/{ssn,iban,routing-number}  # brace expansion

exclude:                            # optional; applied after include
  - finance/ap/internal/**          # keep these out of the imported set
---

# Accounts Payable

Artifacts for vendor-payment workflows: invoice intake, payment drafting,
approval, reconciliation. Vendor compliance lives one domain over (see
finance/procurement).
```

A domain folder without a `DOMAIN.md` is a regular navigable domain by default — its direct artifact children and subdomain folders surface in `load_domain()`. The file is only needed to import from elsewhere, exclude paths, set the description, or mark the folder as unlisted.

#### 4.5.2 Imports and Globs

The `include:` list adds members to the domain in addition to whatever lives directly in the folder. Each entry is either an artifact path or a glob pattern:

- `*` matches one path segment (immediate children).
- `**` matches any number of path segments (recursive).
- `{a,b,c}` expands to alternatives.

`exclude:` is applied after `include:` and removes paths from the resolved set.

**Resolution at `load_domain()` time.** Globs are evaluated against the caller's effective view (org + team overlays + user + local — see §4.6). Imports are dynamic: an artifact added at `finance/ap/payments/new-thing/` is automatically picked up by any domain whose `DOMAIN.md` includes `finance/ap/payments/*` — no `DOMAIN.md` re-publish needed.

**Imports do not change canonical paths.** An artifact has exactly one canonical home (the directory where its `ARTIFACT.md` lives). Imports add additional appearances under other domains. `search_artifacts` returns the artifact once, with its canonical path and (optionally) the list of domains that import it.

**Cross-overlay imports.** Imports are paths resolved against the caller's effective view, not the publisher's view. A `DOMAIN.md` in the org overlay can import an artifact that lives in a team overlay — for callers with both scopes, the import resolves; for callers without the team scope, it silently produces nothing. Globs that match zero artifacts in a particular caller's view are a natural no-op. This handles "expected to be defined in another overlay" without coordination.

**RBAC for imports.** Editing a domain's `include:`/`exclude:` requires publisher rights in the destination domain. **Importing does not require rights in the source path** — it is a declarative pointer, not a privilege grant. RBAC at read time still applies: a reader who lacks visibility into the source doesn't see the imported artifact, even though it appears in the destination's `DOMAIN.md`.

**Cycle detection.** Two domains importing each other is allowed (it's not actually broken — each just contributes its own contents to the other), but the publish lint warns so authors can confirm intent.

**Self-imports.** A domain importing its own subtree (`finance/ap/**` from `finance/ap/DOMAIN.md`) is redundant; lint warns.

**Validation.** Imports that don't currently resolve in any view the registry knows about produce a publish-time **warning**, not an error. This lets a `DOMAIN.md` declare imports that are "expected to be defined" in another overlay later, without forcing coordinated publishes.

#### 4.5.3 Unlisted Folders

Setting `unlisted: true` in a folder's `DOMAIN.md` removes that folder and its entire subtree from `load_domain()` enumeration. Artifacts inside still:

- Are reachable via `load_artifact(<id>)` if the caller has visibility.
- Appear in `search_artifacts` results normally (search is over the visible catalog regardless of listing state).
- Can be imported into other domains via `include:`.

```markdown
---
unlisted: true
description: "Reusable payment helpers — import these into payment-related domains"
---

# _shared / payment-helpers

Routing-number validators, SWIFT/BIC parsers, payment-rail enums. Imported by
finance/ap, finance/ar, treasury/wires.
```

`unlisted: true` propagates to the whole subtree; subdirectories cannot reverse it. Conventionally these folders are prefixed with `_` (e.g., `_shared/`, `_lib/`) for human readability, but the underscore is documentation, not enforcement.

**Search as the fallback.** Hierarchical browsing (`load_domain`) is the curated view: callers see what domain owners decided to surface where. Search (`search_artifacts`) is over the entire visible catalog, regardless of listing state. Useful unlisted artifacts get imported into domains and become hierarchically discoverable; less-frequented ones stay search-only until someone bothers to import them. This rewards good curation without penalizing utility.

#### 4.5.4 DOMAIN.md Across Overlays

`DOMAIN.md` is just a file in an overlay; the same composition rules as artifacts apply. If multiple overlays contribute a `DOMAIN.md` for the same path, the registry merges them:

- `description` and prose body — last-overlay-wins.
- `include:` — additive across overlays (union of all imports).
- `exclude:` — additive across overlays (union of all exclusions; applied after the merged include set).
- `unlisted` — most-restrictive-wins (any overlay setting `unlisted: true` makes the folder unlisted).

This means a developer's `local` overlay can carry its own `DOMAIN.md` for a path that exists in remote overlays — useful for "I want to see X surfaced under this domain in my own view while iterating," without affecting anyone else.

### 4.6 Overlays and Scope Claims

Overlays are **layers in the registry indexed by scope claim**, plus — when the MCP server's `LocalOverlayProvider` is configured — one additional layer sourced from the developer's workspace. Each registry-side artifact is owned by exactly one scope: `org`, `team:<name>`, or `user:<id>`. A request's effective view is the union of layers the caller's OAuth identity entitles them to:

1. The org layer (always visible to org members).
2. The team layers for every `team:<name>` claim on the caller's token. **Any number of team claims is supported.**
3. The personal layer for the caller's `user:<id>` claim, if any.
4. **The `local` layer** (when configured): artifacts read from the workspace filesystem at `${PODIUM_OVERLAY_PATH}` (default `${WORKSPACE}/.podium/overlay/`). Resolved by the MCP server's `LocalOverlayProvider`. Composed as the most-specific tier, after `user:<id>`.

Within the team tier, layers compose alphabetically by team name as the deterministic tiebreaker for collisions. Org → all team layers (alphabetical) → user → local is the full precedence order. Resolution of layers 1–3 happens at the registry on every `load_domain`, `search_artifacts`, and `load_artifact` call; layer 4 is merged in by the MCP server before returning results.

The `local` layer uses the same `ARTIFACT.md` + frontmatter format and the same merge semantics (below) as remote layers. Its content hash is exposed alongside the remote layer hashes in `domain.load` responses so audit and cache invalidation can distinguish which version of which layer the caller saw. To promote a local artifact, copy it into the registry and run `podium publish`.

**Merge semantics for collisions.** If two layers contribute artifacts with the same fully-qualified ID:

- A collision is a publish error **unless** the higher-precedence artifact declares `extends: <lower-precedence-id>` in frontmatter.
- When `extends:` is declared:
  - Description, prose body, and tags use last-layer-wins.
  - Security-sensitive fields (`sensitivity`, `requiresApproval`, sandbox constraints, `mcpServers` allowlists) use most-restrictive-wins.
  - Allowlists (e.g., `allowed_tools`) are intersected; denylists are unioned.

To intentionally replace an artifact rather than extend it, the lower-precedence layer must remove it first or rename the higher-precedence one. Silent shadowing is never permitted.

**Typical layering.**

- **Org.** Organization-wide artifacts, policies, glossaries.
- **Team.** Team-specific skills, agents, and extensions.
- **User.** Individual customizations and personal helpers.
- **Local.** A developer's in-progress work in a specific workspace.

The IdP resolves a caller's scope claims from their OAuth token; podium grants only what the token carries.

### 4.7 Registry as a Service

The registry is a deployable service. The on-disk layout described above (§4.2–§4.5) is the **authoring** model; overlays (§4.6), RBAC (§4.7.2), and the runtime model below are how the service serves requests. The service has three persistent stores:

- **Postgres + pgvector.** Primary store for manifest metadata, descriptors, scope claims, overlay rules, RBAC bindings, dependency edges, deprecation status, audit log, and embeddings used by `search_artifacts`.
- **Object storage.** Holds bundled resource bytes per artifact version, fronted by presigned URL generation. Versioned: each artifact version is immutable.
- **MCP / HTTP API.** Stateless front door. Accepts OAuth-attested identity, evaluates RBAC, resolves scope and overlays, queries Postgres, signs URLs, returns responses.

#### 4.7.1 Tenancy

The service supports three management tiers:

- **Org.** A tenant boundary. Org admins manage teams, users, the org-wide overlay, and org-level RBAC bindings.
- **Team.** A grouping inside an org. Team admins manage the team's overlay, team membership, and team-level RBAC bindings.
- **User.** An identity inside zero or more teams, optionally with a personal overlay.

A request's effective view is the union of org overlay → team overlays the user belongs to → optional personal overlay → optional local overlay (when the MCP server's `LocalOverlayProvider` is configured), with collisions handled per §4.6, and filtered by RBAC (§4.7.2).

#### 4.7.2 Role-Based Access Control

RBAC is enforced at the registry on every API call. Roles bind to identities per scope (org or team); permissions are the actions a role grants.

**Built-in roles:**

| Role | Permissions |
| ---- | ----------- |
| `reader` | List, search, and load artifacts visible under the binding's scope. |
| `publisher` | Reader + create / update artifacts (subject to review status). |
| `reviewer` | Reader + approve, reject, or request changes on draft artifacts. |
| `owner` | Publisher + reviewer + deprecate artifacts within the binding's scope. |
| `admin` | Owner + manage RBAC bindings, team membership, and overlay configuration within the binding's scope. |

Bindings are stored as `(identity, scope, role)` triples in Postgres. Multiple bindings per identity compose additively. The registry evaluates the union of permissions across all bindings the caller's OAuth identity entitles them to.

**Scope hierarchy.** Org-level bindings apply to every artifact in the org; team-level bindings apply to artifacts in that team's scope. A user with `team:finance/publisher` and `team:engineering/reader` can publish to finance and read engineering, with no rights elsewhere.

**Role extensibility.** Deployments can register additional roles with custom permission bundles via the `RBACProvider` interface (§9). The defaults cover most workflows; custom roles serve domain-specific needs (e.g., a `secrets-officer` role with permission to publish high-sensitivity tool artifacts).

**Publish workflow with RBAC.** A publisher creates or updates an artifact in `draft` status; one or more reviewers (per the artifact's sensitivity-driven review policy) move it to `approved`. Owners can deprecate. Admins can override review policy for emergencies, with the override recorded in audit.

**Visibility vs. authoring.** Scope claims (§4.6) determine what artifacts an identity can see. RBAC determines what an identity can do with what they see. Both are enforced at the registry on every call.

**RBAC for domain composition.** Editing a `DOMAIN.md` (§4.5) — including its `include:`, `exclude:`, `unlisted`, and description fields — requires publisher rights in the destination domain. Importing does not require any rights in the source path: imports are declarative pointers, not privilege grants. RBAC at read time still applies to the imported artifact itself — readers see it only if their own scope claims and RBAC bindings entitle them to.

#### 4.7.3 Reverse Dependency Index

The registry indexes "X depends on Y" edges across artifacts: `extends:` chains, `mcpServers:` references to `tool` artifacts, delegation targets discovered from `type: agent` manifests (the `delegates_to:` field), and tag-based associations. The index drives:

- **Impact analysis.** Before deprecating an artifact, list everything that depends on it.
- **Cascading review.** When a high-sensitivity dependency changes, flag downstream artifacts for re-review.
- **Search ranking signals.** Frequently-depended-on artifacts surface higher.

#### 4.7.4 Classification and Lifecycle

Each artifact carries:

- **Sensitivity label.** Low / medium / high. Drives review requirements (e.g., high-sensitivity skills require two reviewers and a security sign-off).
- **Ownership.** Team or individual ID. The registry routes review and deprecation requests to owners.
- **Review status.** Draft / under review / approved / deprecated. Only approved artifacts appear in `search_artifacts` and `load_domain` results by default; deprecated artifacts return a warning when loaded and are excluded from default search results.

Deprecation is a soft state: the artifact remains loadable until its sunset date, but the registry surfaces upgrade paths if `replaced_by:` is set.

#### 4.7.5 Audit

Every `load_domain`, `search_artifacts`, and `load_artifact` call is logged with caller identity, scope claims, RBAC evaluation outcome, requested artifact (or query), timestamp, resolved overlay, and result size. Publish, review, deprecate, and admin actions are also logged. Callers keep their own audit streams for runtime events; podium's audit stream stays focused on the catalogue.

---

## 5. Meta-Tools

Podium exposes three meta-tools through the podium MCP server. These are the only tools podium contributes; callers add their own runtime tools alongside.

| Tool             | Description                                                                                                                                                                                                                                                |
| ---------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| load_domain      | Returns the map for a path: `load_domain()` (root), `load_domain("finance")` (domain), `load_domain("finance/close-reporting")` (subdomain). Output groups artifacts by type, lists notable entries, includes vocabulary hints.                            |
| search_artifacts | Hybrid retrieval (BM25 + embeddings, RRF) over artifact frontmatter. Filters by `type`, `tags`, `scope`. Returns top N results with frontmatter and retrieval scores; bodies stay at the registry until `load_artifact`.                                   |
| load_artifact    | Loads a specific artifact by ID and version. Returns the manifest content as the tool result; **materializes** any bundled resources to a caller-configured path on the host filesystem (atomic write via `.tmp` + rename; presigned URLs for large blobs). |

`load_domain` and `search_artifacts` round-trip through the registry on every call (no snapshot caching at session startup; see §7.1). Only `load_artifact` writes to the host filesystem, and only for the specific artifact requested. This keeps discovery and the working set strictly separated: the catalog lives at the registry; the working set lives on the host.

The MCP tools declared in a loaded artifact's manifest (`mcpServers:`) are stored by podium but registered by the caller's runtime. Podium stores the declarations and exposes them via `load_artifact`; callers decide whether and how to wire them up.

Each caller session loads the three podium meta-tools plus whatever the caller adds (its own runtime tools, harness UI hooks, etc.); the universe of artifacts is reachable on demand through `load_domain` / `search_artifacts` / `load_artifact`.

---

## 6. MCP Server

### 6.1 The Bridge

The podium MCP server is a thin loopback process. It exposes the three meta-tools to the caller's runtime over MCP and forwards calls to the registry. It is stateless. It holds no credentials of its own. It materializes bundled resources atomically on the caller's filesystem when `load_artifact` is invoked.

A single Go binary serves every deployment context. The caller configures it via env vars, command-line flags, or a config file.

### 6.2 Configuration

Top-level configuration parameters (env-var form shown; `--flag` and config-file equivalents are accepted):

| Parameter                     | Description                                                                                  | Default                                |
| ----------------------------- | -------------------------------------------------------------------------------------------- | -------------------------------------- |
| `PODIUM_REGISTRY_ENDPOINT`    | Registry MCP/HTTP API endpoint                                                               | (required)                             |
| `PODIUM_IDENTITY_PROVIDER`    | Selected identity provider implementation                                                    | `oauth-device-code`                    |
| `PODIUM_HARNESS`              | Selected harness adapter (translates canonical artifacts to harness-native format on load)   | `none` (write canonical layout as-is)  |
| `PODIUM_OVERLAY_PATH`         | Workspace path for the `local` overlay                                                       | (unset → layer disabled)               |
| `PODIUM_CACHE_DIR`            | Content-addressed cache directory                                                            | `~/.podium/cache/`                     |
| `PODIUM_AUDIT_SINK`           | Local audit destination (path or external endpoint)                                          | (unset → registry audit only)          |
| `PODIUM_MATERIALIZE_ROOT`     | Default destination root for `load_artifact`                                                 | (caller specifies per call)            |

Provider-specific options are passed as additional env vars (e.g., `PODIUM_OAUTH_AUDIENCE`, `PODIUM_SESSION_TOKEN_ENV`). See §6.3 and §6.7.

### 6.3 Identity Providers

Identity providers attach the caller's OAuth-attested identity to every registry call. The identity provider is the only piece of the MCP server that varies meaningfully across deployment contexts.

- **`oauth-device-code`** _(default)_. Interactive device-code flow on first use; tokens cached in the OS keychain (macOS Keychain, Windows Credential Manager, libsecret on Linux). Refreshes transparently. Suitable for developer hosts where a human is present at first launch. Options: `PODIUM_OAUTH_AUDIENCE`, `PODIUM_OAUTH_AUTHORIZATION_ENDPOINT`, `PODIUM_TOKEN_KEYCHAIN_NAME`.
- **`injected-session-token`**. The MCP server reads a short-lived token from an env var (or a file path) configured by the runtime. The runtime is responsible for token issuance and refresh; podium attaches the token to outgoing calls. Suitable for managed agent runtimes where the runtime brokers identity centrally. Options: `PODIUM_SESSION_TOKEN_ENV` (env var name carrying the token), `PODIUM_SESSION_TOKEN_FILE` (path to a file the runtime updates on rotation), `PODIUM_SESSION_TOKEN_REFRESH_URL` (optional callback the runtime exposes).
- **(Extensible.)** Additional implementations register through the `IdentityProvider` interface (§9).

### 6.4 Local Overlay Provider

Optional. When `PODIUM_OVERLAY_PATH` is set, the MCP server watches the configured path for `ARTIFACT.md` and `DOMAIN.md` files and merges them as the `local` overlay layer (§4.6). fsnotify watcher re-indexes on change. Hashes and ETags are exposed alongside remote layer hashes in `domain.load` responses.

Format: same `ARTIFACT.md` + frontmatter as the registry; resolution and merge semantics identical to remote layers. To promote a local artifact, copy it into the registry and run `podium publish`.

### 6.5 Cache

Disk cache at `${PODIUM_CACHE_DIR}/<sha256>/`. Content-addressed; one entry per artifact version. Index DB (BoltDB or SQLite). Cache modes: `always-revalidate` (default; HEAD with `If-None-Match`), `offline-first`, `offline-only`. `podium cache prune` for cleanup.

In contexts where the home directory is ephemeral, the caller points `PODIUM_CACHE_DIR` at an ephemeral or shared volume.

### 6.6 Materialization

On `load_artifact(<id>)`, the registry returns the canonical manifest body inline and presigned URLs for bundled resources above the inline threshold (~256 KB per resource, ~1 MB per file as a soft cap). Materialization on the MCP server runs in three steps:

1. **Fetch.** The MCP server downloads each resource (or reads it from the cache) into a temporary staging area.
2. **Adapt.** The configured `HarnessAdapter` (§6.7) translates the canonical artifact into the harness's native layout — file names, frontmatter conventions, directory shape — without changing the underlying bytes of bundled resources unless the adapter declares it needs to.
3. **Write.** The MCP server writes the adapted output atomically to a caller-configured destination path (`.tmp` + rename), ensuring the destination either contains a complete copy or nothing.

Below the inline threshold, small resources are returned inline in the `load_artifact` response and written by step 3 alongside the rest.

The destination path comes from the caller — either via `PODIUM_MATERIALIZE_ROOT` or per-call in the `load_artifact` arguments. Common conventions: `/workspace/current/<artifact-id>/<path>` for sandboxed runtimes, `${WORKSPACE}/.<caller>/runtime/<artifact-id>/<path>` for developer-host setups.

When `PODIUM_HARNESS=none` (the default), step 2 is a no-op: the canonical layout is written directly. Callers that want raw artifacts — build pipelines, evaluation harnesses, custom scripts — leave the adapter unset.

### 6.7 Harness Adapters

The `HarnessAdapter` translates a canonical artifact into the format a specific harness expects. It runs at materialization time on the MCP server, between fetch and write.

**Built-in adapters** (selected via `PODIUM_HARNESS`):

| Value             | Target                                                                                                                  |
| ----------------- | ----------------------------------------------------------------------------------------------------------------------- |
| `none`            | _(default)_ Writes the canonical layout as-is: `ARTIFACT.md` + bundled resources alongside.                              |
| `claude-code`     | Writes `.claude/agents/<name>.md` (frontmatter + composed prompt) and places bundled resources under `.claude/podium/<artifact-id>/`. |
| `claude-desktop`  | Writes a Claude Desktop extension layout (`manifest.json` derived from the canonical frontmatter; resources alongside).  |
| `cursor`          | Writes Cursor's native agent / extension format with the prompt and bundled resources placed where Cursor expects them. |
| `gemini`          | Writes Gemini's native agent / extension package layout.                                                                |
| `opencode`        | Writes OpenCode's native package layout.                                                                                |
| `codex`           | Writes Codex's native package layout.                                                                                   |

**What an adapter does.** Each adapter implements a translation from the canonical `ARTIFACT.md` + bundled resources into the harness's native shape:

- Frontmatter mapping: canonical fields like `name`, `description`, `mcpServers:`, `requiresApproval:` map to the harness's equivalent fields.
- Prose body composition: the canonical prose body becomes the harness's system-prompt section in whatever file the harness reads.
- Resource layout: bundled resources move to the path the harness expects (e.g., scripts referenced by the prompt land where the harness can `cd` into them).
- Type-specific behavior: a `type: skill` artifact becomes a skill in the harness's terms; a `type: agent` artifact becomes an agent definition.

**What an adapter does not do.** Adapters do not invent semantics. Fields the harness has no equivalent for are left out (or carried in an `x-podium-*` extension namespace if the harness tolerates one). The adapter's job is mechanical translation, not interpretation.

**Configuration per call.** Callers can override the harness for a single `load_artifact` call by passing `harness: <value>` in the call arguments. This is useful when one MCP server is configured for the developer's primary harness but the caller needs to materialize for a different target (e.g., a build script that produces artifacts for multiple harnesses).

**Cache behavior.** The cache stores canonical artifact bytes (§6.5). Adapter output is regenerated on each materialization — adapter cost is small relative to network fetch, and this avoids per-(artifact, harness) cache duplication.

**Conformance test suite.** Every built-in adapter passes the same set of tests (§11): load a canonical fixture, produce the harness-native output, verify the harness can spawn an agent that uses the materialized artifact end-to-end. New adapters register through the `HarnessAdapter` interface (§9) and are expected to pass the suite.

**Versioning.** Adapter behavior is versioned alongside the MCP server binary. Profile and harness combinations that need a newer adapter behavior pin a minimum MCP server version; older binaries refuse to start.

### 6.8 Process Model

The MCP server is a stdio subprocess spawned by its caller. The caller is responsible for lifecycle (spawn, signal handling, shutdown).

- **Developer hosts.** The convention is one subprocess per workspace, spawned when the workspace opens and torn down when the workspace closes.
- **Managed agent runtimes.** One subprocess per session, spawned by the runtime's bootstrap glue alongside the agent.

### 6.9 Failure Modes

| Failure                                         | Behavior                                                                                              |
| ----------------------------------------------- | ----------------------------------------------------------------------------------------------------- |
| Registry offline                                | Serve from cache; return explicit "offline" status on fresh `domain.load` / `search`.                |
| Overlay path missing                            | Skip overlay layer; warn once.                                                                       |
| Auth token expired (`oauth-device-code`)         | Trigger refresh; if interactive refresh required, surface in tool response with reauth instructions. |
| Auth token expired (`injected-session-token`)    | Surface "token expired"; the caller's runtime is responsible for refresh.                            |
| RBAC denial on a call                           | Return a structured error naming the missing role; log to the registry audit stream.                  |
| Materialization destination unwritable          | Fail the `load_artifact` call with a structured error; nothing partial is left on disk.               |
| Unknown `PODIUM_HARNESS` value                  | Refuse to start; CLI lists the available adapter values.                                              |
| Adapter cannot translate an artifact            | Fail the `load_artifact` call with a structured error naming the missing translation; suggest `harness: none` for raw output. |
| Binary version mismatch with caller             | Refuse to start; caller's CLI prompts an update.                                                     |

---

## 7. External Integration

### 7.1 The Registry Is an External System

From the caller's perspective, the registry is an external system reached on demand. Every discovery, search, and load call round-trips to the registry's MCP API. The system prompt carries the meta-tool descriptions only; the working set assembles call by call as the caller invokes `load_artifact`.

This separation is deliberate:

- The registry can be self-hosted, multi-tenant, or fully managed without changing the caller's behavior.
- Multi-scope overlay resolution and RBAC enforcement live at the registry, where the OAuth identity is the authoritative input.
- Artifact updates take effect on the next call.

### 7.2 Control Plane / Data Plane Split

The registry exposes two surfaces:

**Control plane (MCP API).** Returns metadata: manifest bodies, descriptors, search results, domain maps. Synchronous. Audited. Every call carries the caller's OAuth identity and is RBAC-checked.

**Data plane (object storage).** Holds bundled resources. The control plane never streams bytes for resources above the inline threshold (~256 KB per resource, ~1 MB per file as a soft cap; anything larger **must** use presigned URLs). Instead, `load_artifact` returns presigned URLs that the podium MCP server fetches directly from object storage.

Below the inline threshold, resources are returned inline in the `load_artifact` response. This avoids round-trips for small fixtures.

### 7.3 Caller Integration

Podium does not expose its own end-user client API. Callers spawn the podium MCP server alongside their own runtime tools. The podium CLI (`podium publish`, `podium cache prune`, `podium rbac …`, etc.) is for authoring and operator tasks against the registry; agents do not consume it at runtime.

Any AI system that speaks MCP can consume podium: agent runtimes, build pipelines, terminal CLIs, evaluation harnesses, custom scripts. The contract is the three meta-tools plus the materialization semantics described in §6.6.

---

## 8. Audit and Observability

### 8.1 What Gets Logged

Every significant event, each carrying a trace ID:

| Event                | When                                                              | Source           |
| -------------------- | ----------------------------------------------------------------- | ---------------- |
| domain.loaded        | Caller invoked `load_domain`                                      | Registry         |
| artifacts.searched   | Caller invoked `search_artifacts`                                 | Registry         |
| artifact.loaded      | Caller invoked `load_artifact` (manifest + resources materialized) | Registry         |
| artifact.published   | Publisher created or updated an artifact                          | Registry         |
| artifact.reviewed    | Reviewer approved, rejected, or requested changes                 | Registry         |
| artifact.deprecated  | Owner deprecated an artifact                                      | Registry         |
| domain.published     | Publisher created or updated a `DOMAIN.md`                        | Registry         |
| rbac.binding.changed | Admin added or removed an RBAC binding                            | Registry         |
| overlay.changed     | Admin updated an overlay configuration                            | Registry         |
| rbac.denied          | A call was rejected for missing permissions                       | Registry         |

Audit lives in two streams. The registry owns the events above. The MCP server can also write a local audit log for the meta-tool events through a `LocalAuditSink` interface (§9) when configured. Both streams share trace IDs for cross-stream correlation.

### 8.2 PII Redaction

Artifact manifests can specify fields that should be redacted in audit logs (e.g., `bank_account`, `ssn`). The registry honors redaction directives carried in stored content; the MCP server applies the same directives before writing to its local audit sink.

### 8.3 Audit Sinks

The registry has its own sink for catalogue events. The local file log, when enabled via `PODIUM_AUDIT_SINK`, is written by the MCP server through the `LocalAuditSink` interface. Both default to local storage (the registry's Postgres; a file under the workspace) and can be redirected to external SIEM / log aggregation independently.

---

## 9. Pluggable Interfaces

| Interface              | Default                                              | Purpose                                                                                          |
| ---------------------- | ---------------------------------------------------- | ------------------------------------------------------------------------------------------------ |
| RegistryStore          | Postgres + pgvector                                  | Manifest metadata, dependency edges, embeddings, RBAC bindings, registry-side audit              |
| RegistryObjectStore    | S3-compatible                                        | Bundled resource bytes, presigned URLs                                                           |
| RegistrySearchProvider | BM25 + pgvector (RRF)                                | Hybrid retrieval for `search_artifacts`                                                          |
| RegistryAuditSink      | RegistryStore                                        | Stream for catalogue events                                                                      |
| OverlayResolver        | OAuth claim → layer composition                      | Org / team / user layer composition; collision/extends merge (registry-side, layers 1–3)         |
| RBACProvider           | Built-in roles (reader, publisher, reviewer, owner, admin) | Role definitions and permission evaluation                                                |
| PublishLinter          | Built-in rules                                       | Manifest validation, resource-reference checks, sensitivity sign-off, type-specific rules        |
| IdentityProvider       | `oauth-device-code` (built-in alternative: `injected-session-token`) | Attaches OAuth-attested identity to every registry call from the MCP server      |
| LocalOverlayProvider   | Workspace filesystem (`.podium/overlay/`)            | Source for the `local` overlay layer (when configured)                                           |
| LocalAuditSink         | JSON Lines file at `${WORKSPACE}/.podium/audit.log`  | Local audit log for meta-tool calls (when configured)                                            |
| HarnessAdapter         | `none` (built-ins: `claude-code`, `claude-desktop`, `cursor`, `gemini`, `opencode`, `codex`) | Translates canonical artifacts to the harness's native format at materialization time (§6.7) |

---

## 10. MVP Build Sequence

| Phase | What                                                                                              | Why                                                            |
| ----- | ------------------------------------------------------------------------------------------------- | -------------------------------------------------------------- |
| 1     | Registry data model (Postgres + pgvector + object storage layout)                                 | The catalog is the foundation                                  |
| 2     | Publish pipeline + lint for `ARTIFACT.md` and `DOMAIN.md` + per-type lint rules                   | Authors need a way to add artifacts and configure domains before callers can load them |
| 3     | Registry MCP/HTTP API: `load_domain`, `search_artifacts`, `load_artifact`                         | The wire surface the MCP server talks to                       |
| 4     | Overlay resolver + scope-claim filtering (org / teams / user)                                     | Multi-tenant correctness from day one                          |
| 5     | Domain composition: `DOMAIN.md` parser, glob resolver, `unlisted` enforcement, cross-overlay merge | Multi-membership without duplication                           |
| 6     | RBAC: built-in roles, bindings table, evaluation on every call (including `DOMAIN.md` edits)      | Authoring rights and visibility enforced centrally             |
| 7     | Podium MCP server core (single-shape binary: registry client, resolver, MCP handlers, cache, materialization) | The bridge callers load alongside their runtime |
| 8     | IdentityProvider implementations: `oauth-device-code` (with OS keychain) and `injected-session-token` | One binary across deployment contexts                      |
| 9     | LocalOverlayProvider implementation (filesystem watcher; reads `ARTIFACT.md` + `DOMAIN.md`)       | Workspace `local` layer                                        |
| 10    | HarnessAdapter implementations: `none`, `claude-code`, `claude-desktop`, `cursor`, `gemini`, `opencode`, `codex` + conformance test suite | Author once, load anywhere     |
| 11    | Registry audit log + LocalAuditSink + cross-stream correlation                                    | Observability                                                  |
| 12    | `podium` CLI (`publish`, `cache prune`, `rbac …`, `whoami`)                                       | Operator surface                                               |
| 13    | Example artifact registry (multi-domain, with `DOMAIN.md` imports, unlisted folders, overlays, RBAC bindings) | Prove end-to-end                                |

---

## 11. Verification

- **Unit tests**: registry MCP handlers, overlay resolver, RBAC evaluator, `DOMAIN.md` parser and glob resolver, publish lint, manifest schema validator, MCP server forwarder, local overlay watcher and merge, content-addressed cache, atomic materialization, OAuth keychain integration, identity provider implementations.
- **Managed-runtime integration test**: spawn the MCP server with `PODIUM_IDENTITY_PROVIDER=injected-session-token`, supply a stub session token, exercise the meta-tool round-trip against a real registry, verify identity flows through and overlays resolve correctly per scope claims.
- **Developer-host integration test**: spawn the MCP server with `PODIUM_IDENTITY_PROVIDER=oauth-device-code` and `PODIUM_OVERLAY_PATH=${WORKSPACE}/.podium/overlay/`, complete the device-code flow, exercise the meta-tool round-trip, verify the `local` overlay overrides registry-side artifacts and that hashes are exposed in `domain.load`.
- **Local overlay precedence test**: confirm the `local` layer overrides `user:<id>` (and earlier tiers) for a synthetic conflicting artifact, and that removing the overlay file restores the registry-side artifact.
- **Domain composition tests**: a `DOMAIN.md` with `include:` patterns surfaces matching artifacts in `load_domain()`; recursive `**` and brace `{a,b}` patterns resolve correctly; `exclude:` removes paths from the included set; `unlisted: true` removes a folder and its subtree from `load_domain()` enumeration but leaves search and `load_artifact` working; `DOMAIN.md` from multiple overlays merges per §4.5.4.
- **Cross-overlay import tests**: a `DOMAIN.md` published in one overlay imports an artifact published in another; a caller with both scopes sees the imported artifact; a caller with only the destination overlay sees nothing for that import (silent no-op); imports that don't currently resolve produce a publish-time warning, not an error.
- **Unlisted folder test**: artifacts in an `unlisted: true` folder do not appear in `load_domain()` for any path leading to that folder; they appear in `search_artifacts` results; they are reachable via direct `load_artifact(<id>)` and via imports from other domains.
- **Materialization test**: exercise `load_artifact` against artifacts with diverse bundled file types (Python script, Jinja template, JSON schema, binary blob); verify atomic write semantics; verify partial-download recovery (kill mid-write, retry, check for stray `.tmp` files).
- **Harness adapter conformance suite**: for each built-in adapter (`none`, `claude-code`, `claude-desktop`, `cursor`, `gemini`, `opencode`, `codex`) — load a canonical fixture, produce harness-native output, install into a fresh harness instance, verify the harness can spawn an agent that uses the materialized artifact end-to-end. The same fixture set runs against every adapter.
- **Adapter switching test**: the same MCP server binary, started with each `PODIUM_HARNESS` value, passes the conformance suite without recompilation. Per-call `harness:` overrides materialize a single artifact in a different format than the server's default.
- **Identity provider switching test**: the same MCP server binary, started with each identity provider, passes both integration tests above without recompilation.
- **RBAC tests**: a reader can list and load but not publish; a publisher cannot deprecate; an admin can change bindings; org-level bindings apply to every team; bindings outside an identity's scope are ignored; denials produce structured errors and audit events. Editing a `DOMAIN.md` requires publisher rights in the destination domain; importing into a `DOMAIN.md` does not require any rights in the source path.
- **Publish + review workflow test**: a publisher creates a draft; a reviewer approves; the artifact appears in `search_artifacts` for downstream callers; the audit trail contains both events.
- **Failure-mode tests**: registry offline (cache serves; explicit "offline" status on miss), overlay path missing (skip with warning), token expired under each identity provider, materialization destination unwritable.
- **Security tests**: a caller without a scope claim or RBAC role sees nothing in the corresponding layer; the MCP server requires OAuth-attested identity to reach the registry; redaction directives propagate to the registry audit stream and the local audit log; tokens stay in the OS keychain (or in the runtime-managed location for injected tokens).
- **Example artifact registry**: multi-domain demo with diverse types (skill, agent, context, prompt, tool, workflow), diverse bundled file types, layered overlays (org / multiple teams / optional user / local), RBAC bindings exercising every built-in role.

---

## 12. Key Risks and Mitigations

| Risk                                                   | Mitigation                                                                                                                                                                                                             |
| ------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Catalog grows too large for `load_domain` to be useful | Two-level hierarchy default; the directory layout drives subdomain structure (§4.2, §4.5); domain owners curate cross-cutting views via `DOMAIN.md include:` rather than expanding any single domain unboundedly; rerank with usage signal (Layer 5 in §3.2).                                                                                                   |
| Prompt injection via artifact manifests                | Manifests are authored by reviewed contributors. RBAC restricts who can publish; classification + review status gate visibility.                                                                                       |
| Registry latency on every meta-tool call               | HTTP/2 keep-alive between MCP server and registry; ETag caching of immutable artifact versions; manifest body inline; content-addressed disk cache shared across workspaces.                                            |
| Manifest description quality                           | Publish-time lint flags thin descriptions and clusters of artifacts with colliding summaries. Layer 5 reranking surfaces underperforming descriptions.                                                                  |
| Local overlay tampering                                | The local overlay is intended for the developer's own workspace iteration. Callers that need tamper-evident behavior pin to registry-side versions and leave `PODIUM_OVERLAY_PATH` unset.                               |
| Registry as a single point of failure for callers      | The cache and `offline-first` mode let cached artifacts continue to work during transient outages. Fresh `domain.load` / `search` returns an explicit "offline" status that callers can surface.                       |
| Type system extensibility / per-type lint rule drift   | Type definitions are themselves versioned in the registry. New types ship with their lint rules; deployments can pin a registry version.                                                                                |
| RBAC misconfiguration                                  | Built-in roles cover most cases; admin actions are audited; the registry refuses to start with structurally invalid binding tables; `podium rbac …` CLI surfaces effective permissions for any identity.                |
| Identity provider misconfiguration                     | The MCP server validates its identity-provider configuration at startup and refuses to start with an obviously broken combination. Each provider documents the env vars it requires.                                   |
| Bundled resource bloat                                 | Per-package and per-file size lints at publish time; soft cap is configurable; deployments running large model files use a higher cap and stricter classification review.                                               |
| Recursive globs in `DOMAIN.md` are expensive           | Glob expansion is cached server-side per artifact-version snapshot; cache invalidation is keyed on publish events. Lint warns on overly broad recursive globs (e.g., `**` at the registry root).                       |
| `DOMAIN.md` imports go stale silently                  | Publish-time lint warns on imports that don't currently resolve in any visible view. Layer 5 (usage signal) surfaces domains whose imports return empty results frequently.                                            |
| `unlisted: true` accidentally hides artifacts authors expected to be discoverable | The flag is opt-in (default `false`); the publish lint flags newly-set `unlisted: true` for review. `search_artifacts` continues to surface unlisted artifacts, so they remain reachable. |
| Harness adapter drift (a harness's native format changes; the adapter falls behind) | Adapters are versioned with the MCP server binary; profiles can pin a minimum version. Conformance suite runs against every adapter on every release. Authors who hit drift can fall back to `harness: none` for raw output and adapt manually. |
| Canonical artifact uses a feature an adapter cannot translate | Adapter returns a structured error from `load_artifact` naming the missing translation; suggests `harness: none` or a different harness. Authors prefer canonical fields with broad adapter coverage; deployments can lint for adapter compatibility at publish time. |
| Adapter sprawl across many harnesses                  | Adapters carry no agent or registry logic; they are mechanical translators with a shared core. Conformance suite gates merges. New harness support is additive; existing adapters do not change.                       |
