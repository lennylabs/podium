# Podium — Technical Specification

## 0. Quickstart

A minimal artifact:

```
finance/close-reporting/run-variance-analysis/
└── ARTIFACT.md
```

```markdown
---
type: skill
name: run-variance-analysis
version: 1.0.0
description: Flag unusual variance vs. forecast after month-end close.
when_to_use:
  - "After month-end close, when reviewing financial performance"
tags: [finance, close, variance]
sensitivity: low
---

Compare actuals vs. forecast for the most recent close period. For each line
item, flag variances above the threshold defined in your team's policy doc.
Output a markdown table sorted by absolute variance.
```

Ingest:

```bash
$ git add ARTIFACT.md && git commit -m "Add run-variance-analysis@1.0.0"
$ git push    # opens or updates a PR; CI runs `podium lint`; reviewers approve; merge.
# The Git provider's webhook fires; the registry ingests automatically.
# If the webhook was missed, an admin (or the layer owner) can reingest manually:
$ podium layer reingest org-defaults
artifact: finance/close-reporting/run-variance-analysis@1.0.0   layer: org-defaults
```

In an agent session, the host has the Podium MCP server configured. The agent calls:

```
load_domain("finance/close-reporting")
→ {domains: [...], artifacts: [{id: "finance/close-reporting/run-variance-analysis", ...}]}

load_artifact("finance/close-reporting/run-variance-analysis")
→ {manifest: <prose body>, materialized_at: "/workspace/.podium/runtime/.../ARTIFACT.md"}
```

The agent now has the skill in its working set. Done.

---

## 1. Overview

### 1.1 What Podium Is

**Podium is a control plane for managing and serving AI agent artifacts at scale** — skills, agents, contexts, prompts, MCP server registrations, and any extension type a deployment registers. One catalog, one identity model, one ordered set of layers, one dependency graph, one audit stream, one signing scheme — across every artifact type the catalog holds.

Two pieces:

1. **Registry service** — the served source of truth. Centralized, multi-tenant. Control-plane HTTP/JSON API for manifests, search, layer composition, and signed URLs. Object-storage data plane for resource bytes. Postgres + pgvector for metadata, dependency edges, embeddings, layer config, admin grants, and audit. Mirrors authored content from each layer's source (a Git repo or a local filesystem path) into a content-addressed store; resolves the caller's effective view per OAuth identity on every request.
2. **Three pluggable consumers**, all backed by the same registry HTTP API:
   - **Language SDKs (`podium-py`, `podium-ts`)** — thin clients over the registry HTTP API. The most direct and flexible path; used by programmatic runtimes (LangChain, Bedrock, OpenAI Assistants, custom orchestrators, eval harnesses, build pipelines) wherever a long-running process can host an HTTP client.
   - **Podium MCP server** — a single-binary, in-process bridge for hosts where running an SDK in-process isn't practical (Claude Desktop, Claude Code, Cursor, and similar). Exposes three meta-tools (`load_domain`, `search_artifacts`, `load_artifact`) for lazy catalog navigation; materializes bundled resources atomically on the host's filesystem; runs the configured `HarnessAdapter` to translate the canonical artifact into the host's harness-native format on the way out.
   - **`podium sync`** — eager filesystem materialization of the user's effective view. The deliberate choice when an author wants to drop artifacts onto disk up front and let the harness's native discovery do the loading at runtime, rather than mediate every load through Podium's MCP tools or SDK. One-shot or watcher mode.

Authoring lives in Git (or, for standalone and small-team installations, in a local filesystem path). Authors merge to a tracked Git ref; the registry ingests on webhook (§7.3.1).

What Podium provides:

- **Layered composition with deterministic merge.** An ordered list of layers — admin-defined, user-defined, and the workspace local overlay — composes per request with explicit precedence and no silent shadowing. `extends:` lets a higher-precedence artifact inherit and refine a lower one without forking. Most-restrictive-wins for security fields; last-layer-wins for descriptions.
- **Type heterogeneity as a first principle.** Skills are one of several first-class types. Agents, contexts, prompts, and MCP server registrations sit alongside; extension types register through a `TypeProvider` SPI. Cross-type dependency edges (an agent's `delegates_to:` another agent; a skill's `mcpServers:` references that resolve to `mcp-server`-type artifacts) drive impact analysis.
- **Governance built in.** Per-layer visibility, classification metadata, freeze windows, signing, hash-chained audit, SBOM ingestion, and CVE tracking are first-class. Authoring controls (review requirements, code ownership) live in the Git provider's branch protection — Podium does not duplicate them.
- **Lazy materialization at scale.** Sessions can start empty (MCP path) or with the user's effective view pre-synced (file path). Catalogs of thousands of artifacts don't pollute the system prompt; the agent navigates and loads what it needs.
- **One canonical authoring format, multiple delivery shapes.** Artifacts are authored in a uniform Podium format. The configured `HarnessAdapter` translates at delivery time for harnesses that have native conventions (and is a no-op for runtimes that prefer the canonical layout).
- **Pluggable identity.** One MCP server binary serves every deployment context — interactive OAuth on developer hosts; injected session token in managed runtimes. The same identity providers are available to `podium sync` and the SDKs.

What Podium is not:

- Not an agent runtime. Sessions, agent execution, policy compilation, and downstream tool wiring belong to hosts.
- Not a public artifact marketplace. Public-skill marketplaces (e.g. Vercel skills.sh) fill that role for community content. Podium serves internal catalogs, with optional public mirroring.
- Not a replacement for harness-native conventions. Where a harness has a native skills directory, agent format, or prompt convention, Podium delivers into it via the harness adapter; it doesn't try to displace it.

### 1.2 Problem Statement

Organizations adopting AI accumulate large libraries of authored content of many kinds — skills, agents, contexts, prompts, MCP server registrations, evaluation datasets, and more. As these libraries grow, several problems tend to emerge together:

1. **Capability saturation.** Exposing thousands of skills, prompts, or tool definitions to a model degrades planning quality. Hosts need to see only what's relevant.
2. **Discoverability at scale.** A multi-domain catalogue with thousands of items shared across many teams needs a structured discovery model. A flat list does not work.
3. **Visibility control.** Different users see different subsets of the catalog. The registry is the central enforcement point on every read.
4. **Layered composition.** Organization-wide content, team-specific content, individuals' personal artifacts, and workspace experiments need to compose deterministically with clear precedence and no silent shadowing.
5. **Governance, classification, lifecycle.** Sensitivity labels, ownership, deprecation paths, and reverse-dependency impact analysis need to be first-class.
6. **Type heterogeneity.** Skills, agents, context bundles, prompts, MCP server registrations, eval datasets, model files — every artifact type fits in one registry, with one storage and discovery model, and dependency edges that cross types (e.g., an agent's `delegates_to:` another agent; a skill's `mcpServers:` resolving to `mcp-server`-type artifacts).
7. **Heterogeneous consumers.** Agent runtimes, build pipelines, terminal CLIs, evaluation harnesses, and other AI systems all read from the same catalogue; none should need its own copy. Some speak MCP; many do not.
8. **Cross-harness portability.** The same artifact should be deliverable into Claude Code, Cursor, Codex, Gemini, or a custom runtime without forking per harness. Per-harness convention sprawl is an authoring tax.

Several point solutions partially address subsets of these problems — git monorepos handle versioning and per-repo permissions; per-harness skill marketplaces handle discovery within one vendor's surface; LLM gateways add a thin governance layer over a flat plugin list — but no existing system handles all of them coherently across artifact types. Podium addresses them together: a centralized registry service that ingests from Git (or local) layers, plus three pluggable consumers (language SDKs for direct programmatic access, the MCP server for hosts where running an SDK in-process isn't practical, and `podium sync` for authors who prefer eager materialization with native harness discovery).

### 1.3 Design Principles

- **Git is the authoring source of truth; the registry is the served source of truth.** Authors merge to a tracked Git ref; the registry mirrors what's there into a content-addressed store and serves it. Once `(artifact_id, version)` is ingested, it is bit-for-bit immutable in the registry's store regardless of subsequent Git mutations. `local`-source layers replace Git for standalone and small-team installations; the same immutability invariant applies on ingest.
- **Lazy materialization.** Sessions start empty. The host sees only a high-level map; navigation, search, and load surface what's needed when it's needed (§3).
- **Visibility at the registry.** Per-layer visibility is enforced at the registry on every OAuth-attested call. Authoring rights live in the Git provider's branch protection — Podium does not duplicate them.
- **Type-agnostic discovery.** The registry defines an artifact type system (`skill` / `agent` / `context` / `prompt` / `mcp-server`, extensible) and treats every type uniformly for discovery, search, and load. Type-specific runtime behaviour lives in hosts.
- **Cross-type dependency graph.** Dependency edges (`extends:`, `delegates_to:`, `mcpServers:`) are first-class and span types. Impact analysis and search ranking read from this graph.
- **Any file type or combination.** Manifests are markdown with YAML frontmatter; bundled resources alongside are arbitrary files. The registry stores them as opaque versioned blobs.
- **Three consumer paths, one registry.** Language SDKs (the direct path for runtimes that can call HTTP), the MCP server (the bridge for hosts that can't run an SDK in-process — Claude Desktop, Claude Code, Cursor), and `podium sync` (eager filesystem materialization for authors who prefer native harness discovery over runtime mediation). All three speak the same registry HTTP API and share identity providers, layer composition, content cache, and audit.
- **One MCP server, pluggable identity.** A single binary serves every MCP deployment context. Identity is selected by configuration.
- **Materialization on the host's filesystem.** `load_artifact` lazily downloads bundled resources to a host-configured destination path, atomically. The catalog lives at the registry; the working set lives on the host.
- **Author once, deliver anywhere.** Adapters mechanically translate canonical artifacts into harness-native shapes. No per-harness forks of the source-of-truth manifest.
- **Multi-vendor neutrality.** MIT-licensed; vendor-agnostic; designed for tooling that spans multiple harnesses and runtimes.
- **Immutability and signing.** Every artifact version is bit-for-bit immutable. High-sensitivity artifacts are cryptographically signed.

### 1.3.1 When Podium Helps

Podium is overkill for a small catalog in a single harness with one author — a flat directory plus the harness's native conventions handles that. Becomes valuable as any of these dimensions grow:

- **Catalog size.** Browsing-by-directory and listing artifacts in a system prompt both degrade as the catalog grows. Lazy discovery (§3) and per-domain navigation help once the working set no longer fits comfortably in context.
- **Cross-harness delivery.** "Author once, deliver anywhere" is useful even at small scale once a team is targeting more than one harness — anything beats forking per harness.
- **Multiple artifact types.** A single dependency graph across skills, agents, contexts, prompts, and MCP server registrations beats N type-specific stores once a catalog has more than one type.
- **Multiple contributors.** Per-layer visibility, classification, and audit start to pay off as the number of contributors and the diversity of audiences grow.

A solo developer with a handful of skills in one harness doesn't need Podium. A large team with mixed artifact types across several harnesses, contributing to a catalog used by many audiences, can get substantial value from it.

The minimum viable alternative — a short script that watches a Git repo and copies files into the right harness-specific directories — already gets a single-team, single-type, single-vendor shop most of the way to "author once, deliver anywhere" for a fraction of the engineering effort. Podium addresses the intersection of multiple types, multiple teams, multiple harnesses, and governance requirements; below that intersection, file-copy is often enough.

### 1.4 Constraints and Decisions

| Decision                                                                            | Rationale                                                                                                                                                                                                                                                                                                                                                                                                                |
| ----------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Two component categories: registry service, consumer clients                        | A centralized registry with persistence, plus thin clients that hosts run in-process or call programmatically.                                                                                                                                                                                                                                                                                                           |
| Three consumer shapes (language SDKs, MCP server, `podium sync`)                    | Each fits a different access pattern. SDKs are the direct path when a runtime can call HTTP. The MCP server bridges hosts where running an SDK in-process isn't practical (Claude Desktop, Claude Code, Cursor). `podium sync` is the deliberate choice when an author wants to materialize artifacts up front and let the harness do native discovery, rather than mediate every load through MCP or an SDK at runtime. |
| MCP server is a single binary with pluggable identity, overlay, and harness adapter | One binary serves every MCP deployment context. Identity providers, the workspace local overlay, and the harness adapter are all selected via configuration.                                                                                                                                                                                                                                                             |
| Author once, deliver anywhere                                                       | Artifacts have one canonical authored form. At delivery time (MCP materialization or `podium sync`), the configured `HarnessAdapter` translates into the harness's native shape (Claude Code, Claude Desktop, Cursor, Gemini, OpenCode, Codex, or `none` for raw).                                                                                                                                                       |
| Lazy and eager loading are both first-class                                         | Lazy MCP/SDK mediation keeps the working set small at runtime — most valuable when catalogs grow large. Eager `podium sync` lets the author materialize once and rely on the harness's native discovery — the right call when the author specifically wants that, regardless of catalog size.                                                                                                                            |
| Git as the authoring source of truth                                                | Authors merge to a tracked Git ref; the registry ingests on webhook. The registry's content store mirrors what's been ingested and is the served source of truth — bit-for-bit immutable per `(artifact_id, version)`. `local`-source layers cover standalone and small-team installations using the same ingest semantics.                                                                                              |
| Explicit ordered layer list, not a fixed hierarchy                                  | Each layer is admin-defined or user-defined, has a `git` or `local` source, and declares its own visibility (public / organization / OIDC groups / specific users). The caller's effective view is the composition of every layer they can see, in the configured order, plus the workspace local overlay (when present).                                                                                                |
| Visibility enforced at the registry on every call                                   | The registry composes the caller's effective view from the configured layer list. Git provider permissions are not consulted at request time. Authoring rights live in the Git provider's branch protection, not in Podium.                                                                                                                                                                                              |
| Single `admin` role per tenant                                                      | Admins manage the layer list, freeze windows, and tenant settings. Per-artifact roles do not exist; visibility is per-layer.                                                                                                                                                                                                                                                                                             |
| Cap of 3 user-defined layers per identity by default                                | Configurable per tenant. Keeps personal-layer growth bounded; reordering supported.                                                                                                                                                                                                                                                                                                                                      |
| No registry-side polling                                                            | Ingestion fires from Git provider webhooks or from manual `podium layer reingest` invocations. `local`-source layers re-scan on demand.                                                                                                                                                                                                                                                                                  |
| PostgreSQL + pgvector for the registry (sqlite + sqlite-vec in standalone mode)     | Default backend for manifest metadata, dependency edges, embeddings, layer config, admin grants, and audit. Vector storage is pluggable: managed services (Pinecone, Weaviate Cloud, Qdrant Cloud) can replace pgvector / sqlite-vec, in which case the metadata store stays in Postgres (or SQLite) and embeddings live in the external service. The metadata store itself is also pluggable via `RegistryStore`.       |
| Per-workspace MCP server lifecycle on developer hosts                               | When the MCP server runs as a developer-side subprocess, the host spawns one per workspace, over stdio. The workspace local overlay lives at `.podium/overlay/`. Cache lives in `~/.podium/cache/` and is content-addressed across workspaces.                                                                                                                                                                           |
| Versions are immutable; semver-named                                                | Every `(artifact_id, semver)` pair, once ingested, is bit-for-bit immutable forever. Internal cache keying is by content hash.                                                                                                                                                                                                                                                                                           |
| MIT license; multi-vendor neutrality                                                | Permissive, enterprise-friendly, common for infrastructure projects.                                                                                                                                                                                                                                                                                                                                                     |

### 1.5 Where Podium Fits

Podium overlaps with several existing categories. None of them handle the full set of problems in §1.2 across artifact types.

| Alternative                                                                                | Overlap                                                                       | When it wins                                                                                                                                    | When Podium wins                                                                                                                                                                                       |
| ------------------------------------------------------------------------------------------ | ----------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **Git monorepo + per-harness directory layout**                                            | Versioning, history, repo-permissions on a single repo                        | Single team, single harness, one or two artifact types, no formal governance needs. Zero infrastructure. The right answer for many small teams. | Multi-layer composition with deterministic merge across multiple Git repos; per-layer visibility for cross-team catalogs; cross-type dependency-aware impact analysis; lazy discovery at scale.        |
| **A short script that syncs Git → harness-specific directories**                           | File delivery to multiple harnesses                                           | Single-vendor catalog under a few dozen items where a sync script is good enough.                                                               | Multi-layer composition, per-layer visibility, audit, signing, cross-type dependency graph, lazy MCP-mediated discovery — i.e. the things a sync script would never grow into without becoming Podium. |
| **Per-harness skill marketplaces** (Anthropic Claude marketplace, plugin registries, etc.) | Skill discovery and installation within one harness                           | Single-harness shop; consumption of public/community skills.                                                                                    | Cross-harness delivery; multiple artifact types beyond skills; org-private catalogs; multi-layer composition; richer governance.                                                                       |
| **LLM gateways with plugin marketplaces** (LiteLLM, etc.)                                  | Internal corporate registry with admin enable/disable over a flat plugin list | Already deployed for LLM proxying; adds plugin governance for free.                                                                             | Multi-layer composition with `extends:`; type heterogeneity; dependency tracking; SBOM/CVE pipeline.                                                                                                   |
| **MCP server marketplaces**                                                                | Both register MCP servers                                                     | Discovering pre-built community MCP servers.                                                                                                    | Internal authored content (skills, agents, prompts, contexts) registered alongside MCP server entries under one governance model.                                                                      |
| **LangChain Hub / LangSmith**                                                              | Prompt registry                                                               | Prompt-only flows; LangChain-native runtime; eval-focused workflows.                                                                            | Type heterogeneity; multi-runtime; multi-layer composition; governance.                                                                                                                                |
| **PromptLayer / Langfuse / Helicone**                                                      | Prompt registry + observability                                               | Prompt-only with strong eval focus.                                                                                                             | Broader artifact model; richer governance; not bound to a single LLM provider.                                                                                                                         |
| **HuggingFace Hub**                                                                        | Versioned artifact storage                                                    | Models and datasets at scale.                                                                                                                   | Authored artifacts (skills, agents, contexts, prompts, MCP server registrations) as runtime objects with governance — not models or datasets.                                                          |
| **Single-vendor enterprise governance tiers**                                              | Centralized visibility controls / audit for one vendor's surface              | Single-vendor shop; native integration; managed infrastructure.                                                                                 | Multi-vendor neutrality; open MIT license; one governance plane across heterogeneous tooling.                                                                                                          |

The canonical artifact format is intended for upstream contribution to an MCP-adjacent or AAIF-governed standard once the right venue exists; until then, it's specified here.

### 1.6 Project Model

- **License.** MIT.
- **Governance.** Maintainer model + RFC process for spec changes; see `GOVERNANCE.md`.
- **Distribution.** OSS-first development; optional commercial managed offering by the sponsoring entity (separate doc).
- **Public registry.** A reference registry with curated example artifacts is hosted at the project's public URL.
- **Multi-vendor neutrality.** The project does not adopt contributions, governance changes, or roadmap pressure that would bind it to a single harness vendor's surface.
- **Standards engagement.** Where adjacent open standards (MCP, AAIF-governed standards, etc.) overlap with Podium concerns, the project participates upstream and harmonizes wherever doing so doesn't compromise Podium's broader scope across artifact types.

---

## 2. Architecture

### 2.1 High-Level Component Map

The registry is the system of record for artifacts. It can be reached two ways — as an external HTTP service (§13.10) or as a local filesystem path (§13.11). The diagram below shows the HTTP-service shape. See §7.1 for the dispatch and what each shape supports.

Three consumer shapes read from the registry over HTTP: the Podium MCP server (in-process bridge for MCP-speaking hosts), `podium sync` (filesystem delivery for harnesses that load artifacts directly from disk), and the language SDKs (programmatic access for non-MCP runtimes). All three speak the same registry HTTP API, share identity providers, and apply the same layer composition and visibility filtering.

`podium sync` is also the only consumer that works against a filesystem-source registry (eager materialization, no HTTP).

```
                          ┌───────────────────────────┐
                          │ PODIUM REGISTRY (service) │
                          │  control plane (HTTP/JSON)│
                          │  data plane (object store)│
                          │  Postgres + pgvector      │
                          │  layer composition +      │
                          │    visibility filtering   │
                          │  dependency graph         │
                          │  (centralized,            │
                          │   multi-tenant)           │
                          └───────────▲───────────────┘
                                      │
                       OAuth-attested │ identity (every call)
                                      │
              ┌───────────────────────┼───────────────────────────┐
              │                       │                           │
   ┌──────────┴──────────┐ ┌──────────┴──────────┐ ┌──────────────┴──────────┐
   │ Podium MCP server   │ │ podium sync         │ │ Language SDKs           │
   │   load_domain ·     │ │  one-shot or watch; │ │  podium-py, podium-ts   │
   │   search_artifacts ·│ │  writes effective   │ │  thin client over the   │
   │   load_artifact     │ │  view to disk in    │ │  registry HTTP API      │
   │ + IdentityProvider  │ │  harness-native     │ │ + IdentityProvider      │
   │ + LocalOverlayProv. │ │  layout             │ │                         │
   │ + HarnessAdapter    │ │ + HarnessAdapter    │ │                         │
   │ + cache + materlz.  │ │ + cache             │ │                         │
   └──────────▲──────────┘ └──────────▲──────────┘ └──────────▲──────────────┘
              │                       │                       │
              │ MCP (stdio)           │ filesystem            │ HTTP
              │                       │                       │
   ┌──────────┴──────────┐ ┌──────────┴──────────┐ ┌──────────┴──────────────┐
   │ MCP-speaking host   │ │ File-based harness  │ │ Non-MCP runtime         │
   │ (any agent runtime) │ │ or runtime          │ │ (LangChain, Bedrock,    │
   │                     │ │                     │ │  custom orchestrators,  │
   │                     │ │                     │ │  eval/build pipelines)  │
   └─────────────────────┘ └─────────────────────┘ └─────────────────────────┘
```

Sequence for `load_artifact` (MCP path):

```
host         MCP server          registry        object storage
 │ load_artifact │                  │                    │
 │──────────────▶│ POST /artifacts  │                    │
 │               │ (id, identity)   │                    │
 │               │─────────────────▶│                    │
 │               │                  │ visibility + layer │
 │               │                  │ compose + version  │
 │               │ {manifest,       │                    │
 │               │  presigned URLs} │                    │
 │               │◀─────────────────│                    │
 │               │ GET <presigned>                       │
 │               │──────────────────────────────────────▶│
 │               │ resource bytes                        │
 │               │◀──────────────────────────────────────│
 │               │ verify + adapt + atomic write to host │
 │ {manifest,    │                                       │
 │  materialized}│                                       │
 │◀──────────────│                                       │
```

Two MCP deployment scenarios use the same MCP server binary:

- **Managed agent runtime.** The runtime spawns the MCP server as a co-located process. Identity is supplied via an injected session token (signed JWT); the registry endpoint is configured the same way. The workspace local overlay is unset.
- **Developer's host.** The host spawns one MCP server per workspace as a stdio subprocess. The MCP server uses an OAuth device-code flow on first use (surfaced via MCP elicitation) to obtain a registry token, stored in the OS keychain. The workspace local overlay reads from `${WORKSPACE}/.podium/overlay/`.

### 2.2 Component Responsibilities

**Podium Registry** _(centralized service)_. The system of record for artifacts. Composes the caller's effective view from the configured layer list per OAuth identity, applies per-layer visibility, indexes manifests, runs hybrid search, signs URLs for resource bytes, maintains the cross-type dependency graph, emits change events. Three persistent stores: Postgres + pgvector, object storage, HTTP/JSON API.

The registry's wire protocol is **HTTP/JSON**. All three consumer shapes speak the same HTTP API. Direct MCP access to the registry is not supported; MCP is one of three consumer surfaces that translate HTTP responses into a runtime-appropriate shape.

**Podium MCP server** _(in-process bridge for MCP-speaking hosts)_. Single binary. Exposes the three meta-tools. Holds no per-session server-side state — local state is limited to a content-addressed disk cache, OS-keychain-stored credentials (in `oauth-device-code` mode), an in-memory local-overlay index, and the materialized working set on disk. No state is shared across MCP server processes.

**`podium sync`** _(filesystem delivery for harnesses that read artifacts directly from disk)_. CLI command (and library) that reads the user's effective view from the registry and writes it to a host-configured layout via the configured `HarnessAdapter`. One-shot or `--watch` mode (subscribes to registry change events). Reuses the same identity providers and content cache as the MCP server. See §7.5.

**Language SDKs (`podium-py`, `podium-ts`)** _(programmatic access for non-MCP runtimes)_. Thin clients over the registry HTTP API. Used by LangChain, Bedrock, OpenAI Assistants, custom orchestrators, eval harnesses, build pipelines, notebooks. See §7.6.

Pluggable interfaces shared across all three consumer shapes:

- **IdentityProvider** — supplies the OAuth-attested identity attached to every registry call. Built-ins: `oauth-device-code` and `injected-session-token`. Additional implementations register through the interface.
- **LocalOverlayProvider** — optional. When configured, reads `ARTIFACT.md` packages from a workspace filesystem path and merges them as the workspace local overlay (§6.4). Available across all three consumer shapes.
- **HarnessAdapter** — translates canonical artifacts into harness-native format at delivery time (MCP materialization or `podium sync` write). Built-ins cover Claude Code, Claude Desktop, Cursor, Gemini, OpenCode, Codex; `none` (default) writes the canonical layout as-is. See §6.7. The SDKs accept a harness parameter on `materialize()`.

Configuration: env vars, command-line flags, or a config file the host/user supplies. See §6.

**Hosts** _(not Podium components)_. Any system that consumes the catalog: MCP-speaking agent runtimes, file-based harnesses, programmatic runtimes. Hosts choose the consumer shape that fits their architecture.

---

## 3. Disclosure Surface

### 3.1 The Problem

Capability saturation: tool-call accuracy starts to degrade past ~50–100 tools in a single system prompt and falls off sharply past ~200 (figures vary by model and task). For larger catalogs, discovery has to be staged.

### 3.2 Three Disclosure Layers

The host sees only what it asks for, in stages. The three layers map 1:1 to the three meta-tools.

#### Layer 1 — Hierarchical map (`load_domain`)

The host calls `load_domain(path)` to get a map of what exists. With no path, the map describes top-level domains. With a path like `finance`, it describes that domain's subdomains and key artifacts. The hierarchy is two levels deep by default — a third level kicks in only when a domain crosses ~1000 artifacts. The directory layout drives the domain hierarchy (§4.2); a domain's children may be augmented or curated by an optional `DOMAIN.md` config that imports artifacts from elsewhere (§4.5). Multi-membership is allowed: one artifact can show up under more than one domain via imports.

#### Layer 2 — Search (`search_artifacts`)

When the host has the right neighborhood but doesn't know which artifact, it calls `search_artifacts(query, scope?)`. The registry runs a hybrid retriever (BM25 + embeddings, fused via reciprocal rank) over manifest text, returning a ranked list of `(artifact_id, summary, score)` tuples. Search returns descriptors only.

#### Layer 3 — Load (`load_artifact`)

When the host has chosen an artifact, it calls `load_artifact(artifact_id)`. The registry returns the manifest body inline; bundled resources are materialized lazily on the host's filesystem and large blobs are delivered via presigned URLs.

### 3.3 Three Enabling Concerns

The disclosure surface only works if three other things hold.

**Visibility filtering.** Every request to the registry carries the host's OAuth identity. The registry composes the caller's effective view from the configured layer list (§4.6), filtering by each layer's visibility declaration. This is gatekeeping, not disclosure — it bounds what the disclosure surface can reveal.

**Description quality.** Layers 1 and 2 only work if manifests describe themselves well. Each artifact's `description` field must answer "when should I use this?" in one or two sentences. The registry lints for thin descriptions and flags clusters of artifacts whose summaries collide.

**Learn-from-usage reranking.** The registry observes which artifacts actually get loaded after which queries (correlated within a `session_id` — see §5), and uses that signal to (a) rerank search results, (b) suggest import candidates to domain owners, and (c) flag artifacts whose authored descriptions underperform retrieval expectations.

### 3.4 Discovery Flow

A typical host session begins empty. The host calls `load_domain()` to get the top-level map. It either picks a domain and calls `load_domain("<domain>")` for the next level, or — if the request is specific enough — jumps straight to `search_artifacts`. When it has an artifact ID, it calls `load_artifact`, which materializes the package on the host (§6.6).

Only `load_artifact` writes to the host filesystem. The catalog lives at the registry; the working set lives on the host.

### 3.5 Scope Preview (Pre-Session)

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

---

## 4. Artifact Model

### 4.1 Artifacts Are Packages of Arbitrary Files

An artifact is a directory with a manifest at its root. The manifest — `ARTIFACT.md` — is a markdown file with YAML frontmatter and prose. Frontmatter is what the registry indexes; prose is what the host reads when the artifact is loaded.

**Bundled resources alongside the manifest are arbitrary files.** Python scripts, shell scripts, templates, JSON / YAML schemas, evaluation datasets, model weights, binary blobs — anything the host needs at runtime. The registry treats these as opaque versioned blobs.

#### First-class types

Full lint coverage, conformance suite participation, broad adapter support:

- `skill` — instructions (+ optional scripts) loaded into the host agent's context on demand.
- `agent` — a complete agent definition meant to run in isolation as a delegated child.
- `context` — pure reference material (style guides, glossaries, API references, large knowledge bases).
- `prompt` — parameterized prompt templates the agent or a human can instantiate.

#### Registered extension types

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

### 4.2 Registry Layout on Disk

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

### 4.3 Artifact Manifest Schema

The manifest frontmatter is YAML; the prose body is markdown. The registry indexes frontmatter for `search_artifacts` and `load_domain`. The prose body is returned inline by `load_artifact`.

#### Universal fields (any artifact type)

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

#### Caller-interpreted fields (stored verbatim; consumed by the host)

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

#### Type-specific fields

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

#### External resources

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

### 4.4 Bundled Resources

Bundled resources ship with the artifact package and are discovered implicitly from the directory: every file under the artifact's root other than `ARTIFACT.md` is a bundled resource. There is no `resources:` list in frontmatter — what's in the folder ships, and the manifest references files inline in prose.

The registry stores bundled resources content-addressed by SHA-256 in object storage; bytes are deduplicated across all artifact versions within an org's storage namespace. Presigned URLs deliver them at load time.

At materialization (§6.6), resources land at a host-supplied path. The Podium MCP server downloads each resource and writes it atomically (`.tmp` + rename) so partial downloads cannot corrupt a working set.

The ingest-time linter validates that prose references in `ARTIFACT.md` resolve to:

- Bundled files (existence check)
- URLs (HTTP HEAD returns 200/3xx)
- Other artifacts (registry-side resolution against current visible catalog)

Drift between manifest text and bundled files is an ingest error.

**Trust model.** Bundled scripts inherit the artifact's sensitivity label. A high-sensitivity skill that bundles a Python script is effectively shipping code into the host; pre-merge CI run by the source repository (secret scanning, static analysis, SBOM generation, optional sandbox policy review) takes bundled scripts seriously.

#### 4.4.1 Execution Model Contract

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

#### 4.4.2 Content Provenance

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

### 4.5 Domain Organization

A domain is a directory in the registry. Its members at discovery time are: every artifact directly under that directory, every subdirectory that itself qualifies as a domain, and (optionally) anything brought in by an explicit import. Domain composition is configured by an optional `DOMAIN.md` at the directory root.

#### 4.5.1 DOMAIN.md

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

#### 4.5.2 Imports and Globs

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

#### 4.5.3 Unlisted Folders

Setting `unlisted: true` in a folder's `DOMAIN.md` removes that folder and its entire subtree from `load_domain` enumeration. Artifacts inside still:

- Are reachable via `load_artifact(<id>)` if the host has visibility.
- Appear in `search_artifacts` results normally (subject to per-artifact `search_visibility:`).
- Can be imported into other domains via `include:`.

`unlisted: true` propagates to the whole subtree.

#### 4.5.4 DOMAIN.md Across Layers

If multiple layers contribute a `DOMAIN.md` for the same path, the registry merges them:

- `description` and prose body — last-layer-wins.
- `include:` — additive across layers.
- `exclude:` — additive across layers; applied after the merged include set.
- `unlisted` — most-restrictive-wins.

When a workspace-local-overlay `DOMAIN.md` is involved, the MCP server applies the merge client-side after the registry returns its result for the registry-side layers.

### 4.6 Layers and Visibility

#### Terminology

- **Layer** — a unit of composition. Each layer has a single **source** (a Git repo or a local filesystem path) and a **visibility** declaration.
- **Effective view** — the composition of every layer the caller's identity is entitled to see, in precedence order.

#### The layer list

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

#### Source types

Two source types are supported:

- **`git`** — a remote Git repository at a tracked ref, optionally rooted at a subpath. The registry ingests on webhook (§7.3.1).
- **`local`** — a filesystem path readable by the registry process. Re-scanned on demand via `podium layer reingest <id>`. Intended for standalone and small-team installations where the registry runs alongside the author.

#### Visibility

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

#### Config schema

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

#### Merge semantics for collisions

If two layers contribute artifacts with the same canonical ID:

- A collision is rejected at ingest **unless** the higher-precedence artifact declares `extends: <lower-precedence-id>` in frontmatter.
- When `extends:` is declared, fields merge per the table below.

`extends:` is a single scalar (no multiple inheritance). Cycle detection at ingest time. Parent version is resolved at the child's ingest time and pinned (parent updates do not silently propagate; the child must be re-ingested to pick up changes).

To intentionally replace an artifact rather than extend it, the lower-precedence layer must remove it first or rename the higher-precedence one. Silent shadowing is never permitted.

**Hidden parents.** When a child manifest declares `extends: <parent>` and the requesting identity cannot see the layer that contributes the parent, the registry resolves and merges the parent server-side and serves the merged manifest. The parent's existence and ID are not surfaced to the requester. This preserves layer privacy and keeps the consumer interface uniform regardless of layer membership.

#### Field semantics table

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

### 4.7 Registry as a Service

The registry is a deployable service. The on-disk layout described above (§4.2–§4.5) is the **authoring** model; layers (§4.6), access control (§4.7.2), and the runtime model below are how the service serves requests. The runtime model has four pieces — three persistent stores plus the API front door:

- **Metadata store (Postgres in standard, SQLite in standalone).** Manifest metadata, descriptors, layer config, admin grants, user-defined-layer registrations, dependency edges, deprecation status, and audit log. Pluggable via `RegistryStore` (§9.1).
- **Vector store.** `pgvector` collocated in Postgres (standard default) or `sqlite-vec` collocated in SQLite (standalone default). Pluggable via `RegistrySearchProvider` (§9.1) to a managed service (Pinecone, Weaviate Cloud, Qdrant Cloud); when a managed backend is configured, embeddings move out of the metadata store and the registry assumes responsibility for dual-write consistency.
- **Object storage.** Bundled resource bytes per artifact version, fronted by presigned URL generation. Versioned: each artifact version is immutable.
- **HTTP/JSON API.** Stateless front door. Accepts OAuth-attested identity, composes the caller's effective view from the layer list, applies per-layer visibility, queries the metadata and vector stores, signs URLs, returns responses.

#### Version immutability invariant

A `(artifact_id, version)` pair, once ingested, is bit-for-bit immutable forever in the registry's content store. Subsequent commits in a layer's source that change the same `version:` with different content are rejected at ingest. Readers in flight when a re-ingest occurs continue to see their pinned version.

Force-push or history rewrite at the source does not break the invariant: previously-ingested commits' bytes are preserved in the content-addressed store, and the registry emits a `layer.history_rewritten` event for the operator. Strict mode is configurable per layer (§7.3.1).

#### Embedding generation

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

#### Dual-write semantics for external vector backends

When `RegistrySearchProvider` is configured to a backend outside the metadata store (any managed service or a separate pgvector instance), the registry coordinates writes through a **transactional outbox**:

1. At ingest, the manifest commit and a `vector_pending` row land in the same `RegistryStore` transaction. The outbox row carries either the pre-computed vector (storage-only backends) or the canonical text projection (self-embedding backends).
2. A background worker drains the outbox by writing to the vector backend with exponential-backoff retry, marking each row complete on success.
3. Ingest itself never blocks on the external service. If the vector backend is down, ingest succeeds, the outbox grows, and the metadata store stays the source of truth.

While an outbox row is unresolved, the affected artifact remains discoverable via BM25 and direct `load_artifact` calls; only its semantic-search recall is degraded until the vector lands. Operators monitor outbox depth via a Prometheus gauge; a `vector.outbox_lagging` event fires when depth or oldest-row age exceeds an operator-configured threshold.

Self-embedding backends collapse the embedding step into the same call (text-in instead of vector-in), so they avoid a separate inference round-trip from the registry but the outbox semantics are otherwise identical.

The collocated defaults (pgvector, sqlite-vec) sidestep the outbox entirely — embeddings and metadata commit in a single database transaction.

#### 4.7.1 Tenancy

The tenant boundary is the **org**. Each org has its own layer list (§4.6), its own admins, its own audit stream, and its own quotas. Org IDs are UUIDs; org names are human-readable aliases.

User identity comes from the configured identity provider (§6.3). Group membership comes from OIDC group claims and from SCIM 2.0 push (where the IdP supports it). Layer visibility (§4.6) references those groups and user identifiers directly — there is no Podium-side concept of "team" beyond what OIDC groups provide.

**Postgres isolation.** Each org has its own schema; cross-org tables (e.g., shared infrastructure metadata) use row-level security with org_id checks. Schema-per-org gives clean drop-org semantics, isolates query patterns, and bounds the blast radius of SQL injection.

##### 4.7.1.1 Data Residency

A deployment is single-region. Multi-region deployments run separate registries per region with no cross-region replication.

#### 4.7.2 Access Control

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

#### 4.7.3 Reverse Dependency Index

The registry indexes "X depends on Y" edges across artifacts:

- `extends:` chains
- `delegates_to:` references (constrained to `agent`-type targets)
- `mcpServers:` references that resolve to `mcp-server`-type artifacts via `server_identifier`

Tag co-occurrence is **not** a dependency edge (too noisy for impact analysis).

The index drives:

- **Impact analysis.** Before deprecating an artifact, list everything that depends on it.
- **Cascading review.** When a high-sensitivity dependency changes, flag downstream artifacts for re-review.
- **Search ranking signals.** Frequently-depended-on artifacts surface higher.

#### 4.7.4 Classification and Lifecycle

Each artifact carries:

- **Sensitivity label.** `low` / `medium` / `high`, declared in frontmatter. Informational metadata exposed in `search_artifacts` and `load_artifact` responses for filtering and display. Reviewer requirements based on sensitivity are enforced in the Git provider's branch protection (e.g., path-scoped CODEOWNERS plus required-reviewer counts), not by the registry.
- **Ownership.** Authoring rights flow through the source layer's Git permissions. The artifact's manifest can name owners informationally for routing notifications via the `NotificationProvider` SPI (e.g., for vulnerability alerts and ingest failures).
- **Lifecycle.** An ingested artifact is live until a subsequent ingest sets `deprecated: true`. Deprecated artifacts return a warning when loaded and are excluded from default search results; if `replaced_by:` is set, the registry surfaces the upgrade target alongside the warning.

#### 4.7.5 Audit

Every `load_domain`, `search_artifacts`, and `load_artifact` call is logged with caller identity, visibility outcome, requested artifact (or query), timestamp, resolved layer composition, and result size. Ingest events (success and failure), admin actions (layer-list edits, freeze-window toggles, admin grants), and break-glass invocations are also logged. Hosts keep their own audit streams for runtime events; Podium's audit stream stays focused on the catalogue. Detail in §8.

#### 4.7.6 Version Resolution and Consistency

Versions are semver-named (`major.minor.patch`), author-chosen via the manifest's `version:` field. Internally, the registry stores `(artifact_id, semver, content_hash)` triples; content_hash is the SHA-256 of the canonicalized manifest + bundled resources.

Pinning syntax in references (`extends:`, `delegates_to:`, `mcpServers:`):

- `<id>` — resolves to `latest`.
- `<id>@<semver>` — exact version.
- `<id>@<semver>.x` — minor or patch range (e.g., `1.2.x`, `1.x`).
- `<id>@sha256:<hash>` — content-pinned.

`load_artifact(<id>)` resolves to `latest` = "the most recently ingested non-deprecated version visible under the caller's effective view, at resolution time." Resolution is registry-side.

For session consistency, the meta-tools accept an optional `session_id` argument (UUID generated by the host per agent session). The first `latest` lookup within a session is recorded and reused for all subsequent same-id lookups in that session — so the host sees a consistent snapshot.

**Inheritance and re-ingest.** When a child manifest declares `extends: <parent>` (no version pin), the parent version is resolved at the child's ingest time and stored as a hard pin in the ingested manifest's resolved form. Parent updates do not silently propagate; the child must be re-ingested (typically by bumping its `version:` and merging) to pick up changes.

#### 4.7.7 Vulnerability Tracking

The registry consumes CVE feeds, walks SBOM dependencies declared in artifact frontmatter, and surfaces affected artifacts:

- `podium vuln list [--severity ...]` — list affected artifacts.
- `podium vuln explain <cve> <artifact>` — show the dependency path.
- Owners notified through configured channels (webhook / email / Slack via the `NotificationProvider` SPI).

Lint enforces SBOM presence for sensitivity ≥ medium.

#### 4.7.8 Quotas

Per-org limits, admin-configurable: storage (bytes), search QPS, materialization rate, audit volume.

`podium quota` CLI surfaces current usage and limits. Quota exhaustion returns structured errors (`quota.storage_exceeded`, etc.).

#### 4.7.9 Signing

Each artifact version is signed by the author's key at commit time, or by a registry-managed key at ingest. Two key models:

- **Sigstore-keyless** (preferred). OIDC-attested signature; transparency log entry; no key management.
- **Registry-managed key** (fallback). Per-org key managed by the registry; rotated quarterly.

Signatures are stored alongside content. The MCP server verifies signatures on materialization for sensitivity ≥ medium (configurable per deployment). Signature failure aborts materialization with `materialize.signature_invalid`.

`podium verify <artifact>` for ad-hoc verification. `podium sign <artifact>` for explicit signing outside the ingest flow.

---

## 5. Meta-Tools

Podium exposes three meta-tools through the Podium MCP server. These are the only tools Podium contributes; hosts add their own runtime tools alongside.

| Tool               | Description                                                                                                                                                                                                                                                                                                                               |
| ------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `load_domain`      | Returns the map for a path: `load_domain()` (root), `load_domain("finance")` (domain), `load_domain("finance/close-reporting")` (subdomain). Output groups artifacts by type, lists notable entries, includes vocabulary hints. Optional `session_id` arg.                                                                                |
| `search_artifacts` | Hybrid retrieval (BM25 + embeddings, RRF) over artifact frontmatter. Filters by `type`, `tags`, `scope`. Returns top N results with frontmatter and retrieval scores; bodies stay at the registry until `load_artifact`. Optional `session_id` arg.                                                                                       |
| `load_artifact`    | Loads a specific artifact by ID and version. Returns the manifest content as the tool result; **materializes** any bundled resources to a host-configured path on the filesystem (atomic write via `.tmp` + rename; presigned URLs for large blobs). Args: `id`, optional `version`, optional `session_id`, optional `harness:` override. |

`load_domain` and `search_artifacts` round-trip through the registry on every call (no snapshot caching at session startup). Only `load_artifact` writes to the host filesystem, and only for the specific artifact requested. Programmatic consumers (SDK) can also call a non-MCP bulk variant of `load_artifact` — see §7.6.2.

The MCP server declares its capabilities in the MCP `initialize` response: `{tools: true, prompts: <conditional on prompt artifacts with expose_as_mcp_prompt: true>, sessionCorrelation: true}`.

**`mcp-server` artifacts are filtered out of the MCP bridge's results.** Hosts that consume Podium through the MCP bridge cannot connect to a discovered MCP server mid-session — Claude Desktop, Claude Code, Cursor, and similar harnesses fix their MCP server list at startup. Surfacing `mcp-server` registrations through `search_artifacts` or `load_artifact` from the bridge would only add planning noise. They remain visible through the SDK (which owns its MCP client and can connect dynamically) and through `podium sync` (which materializes them into the harness's on-disk config for the next launch).

### 5.0 Why Tools, Not Resources

MCP resources fit static lists and host-driven enumeration. Podium's catalog needs parameterized navigation (`load_domain` takes a path; `search_artifacts` takes a query) and lazy materialization with side effects. Tools fit better.

Artifact bodies are also exposed as MCP resources for hosts that prefer that pattern (read-only mirror of `load_artifact`); the canonical interface remains the three meta-tools.

### 5.1 Meta-Tool Descriptions and Prompting Guidance

The strings below are the canonical tool descriptions exposed to the LLM via MCP. Hosts SHOULD use them verbatim unless customizing for a specific runtime.

#### `load_domain`

> Browse the artifact catalog hierarchically. Call with no path to see top-level domains. Call with a path (e.g., "finance") to see that domain's subdomains and notable artifacts. Use this when you don't know what's available and need to explore. Returns a map; doesn't load any artifact's content. To use an artifact you find here, call `load_artifact`.

#### `search_artifacts`

> Search the artifact catalog by query. Use this when you know roughly what you're looking for but not the exact artifact ID. Filters: `type` (skill, agent, context, prompt), `tags`, `scope` (a domain path to constrain the search). Returns ranked descriptors only — no manifest bodies. To use a result, call `load_artifact` with its id.

#### `load_artifact`

> Load a specific artifact by ID. Returns the manifest body and materializes any bundled resources (scripts, templates, schemas, etc.) onto the local filesystem at a configured path. Use this only when you've decided to actually use the artifact — loading is the expensive operation. The returned `materialized_at` paths are absolute and ready to use.

#### Example system-prompt fragment

```
You have access to a catalog of authored skills and agents through the Podium meta-tools:
  - load_domain: explore the catalog hierarchically.
  - search_artifacts: find an artifact by query.
  - load_artifact: actually load and materialize an artifact for use.

Sessions start empty. Call load_domain or search_artifacts when you need
capability you don't already have. Call load_artifact only when you're ready
to use the artifact — it's the operation that puts content in your context.
```

### 5.2 Prompt Projection

When a `type: prompt` artifact is loaded with `expose_as_mcp_prompt: true` in frontmatter, the MCP server also exposes it via MCP's `prompts/get` so harnesses with slash-menu support can surface it directly to users. Opt-in.

The MCP tools declared in a loaded artifact's manifest (`mcpServers:`) are stored by Podium but registered by the host's runtime. Podium stores the declarations and exposes them via `load_artifact`; hosts decide whether and how to wire them up.

---

## 6. MCP Server

### 6.1 The Bridge

The Podium MCP server is a thin in-process bridge. It exposes the three meta-tools to the host's runtime over MCP and forwards calls to the registry. It holds no per-session server-side state. Local state is limited to a content-addressed disk cache, OS-keychain-stored credentials (in `oauth-device-code` mode), an in-memory local-overlay index, and the materialized working set on disk. No state is shared across MCP server processes.

A single Go binary serves every deployment context. The host configures it via env vars, command-line flags, or a config file.

**Requires a server-source registry.** The MCP server speaks HTTP and does not work against a filesystem-source registry (§13.11).

### 6.2 Configuration

Top-level configuration parameters (env-var form shown; `--flag` and config-file equivalents are accepted):

| Parameter                    | Description                                                                                                                      | Default                                                                                       |
| ---------------------------- | -------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------- |
| `PODIUM_REGISTRY`            | Registry source: URL (HTTP) or filesystem path. See §7.5.2 for dispatch.                                                         | (read from `sync.yaml`'s `defaults.registry` per §7.5.2 if unset; required if neither is set) |
| `PODIUM_IDENTITY_PROVIDER`   | Selected identity provider implementation                                                                                        | `oauth-device-code`                                                                           |
| `PODIUM_HARNESS`             | Selected harness adapter                                                                                                         | `none` (write canonical layout as-is)                                                         |
| `PODIUM_OVERLAY_PATH`        | Workspace path for the `local` overlay                                                                                           | (unset → layer disabled)                                                                      |
| `PODIUM_CACHE_DIR`           | Content-addressed cache directory                                                                                                | `~/.podium/cache/`                                                                            |
| `PODIUM_CACHE_MODE`          | `always-revalidate` / `offline-first` / `offline-only`                                                                           | `always-revalidate`                                                                           |
| `PODIUM_AUDIT_SINK`          | Local audit destination (path or external endpoint). When set without a value (or set to `default`), uses `~/.podium/audit.log`. | (unset → registry audit only)                                                                 |
| `PODIUM_MATERIALIZE_ROOT`    | Default destination root for `load_artifact`                                                                                     | (host specifies per call)                                                                     |
| `PODIUM_PRESIGN_TTL_SECONDS` | Override for presigned URL TTL                                                                                                   | 3600                                                                                          |
| `PODIUM_VERIFY_SIGNATURES`   | Verify artifact signatures on materialization                                                                                    | `medium-and-above`                                                                            |

Provider-specific options are passed as additional env vars (e.g., `PODIUM_OAUTH_AUDIENCE`, `PODIUM_SESSION_TOKEN_ENV`).

### 6.3 Identity Providers

Identity providers attach the caller's OAuth-attested identity to every registry call.

- **`oauth-device-code`** _(default)_. Interactive device-code flow on first use; tokens cached in the OS keychain (macOS Keychain, Windows Credential Manager, libsecret on Linux). Refreshes transparently. Defaults: access-token TTL 15 min, refresh-token TTL 7 days, revocation propagation ≤60s. Options: `PODIUM_OAUTH_AUDIENCE`, `PODIUM_OAUTH_AUTHORIZATION_ENDPOINT`, `PODIUM_TOKEN_KEYCHAIN_NAME`.

  How the verification URL surfaces depends on the consumer:
  - **MCP server** uses MCP elicitation — the host displays the URL and code in the agent UI.
  - **`podium sync`, `podium login`, and other CLI commands** print the URL and code to stderr, attempt to open the URL in the system browser (via `open` on macOS, `xdg-open` on Linux, `start` on Windows), and poll the IdP's token endpoint until the user completes the flow or a 10-minute timeout elapses. `--no-browser` skips the auto-open. Output is suppressed under `--json`; the prompt is replaced with a structured `auth.device_code_pending` event emitted on stderr.
  - **SDK** raises `DeviceCodeRequired` with the URL and code; calling code is responsible for surfacing it to the user. `Client.login()` performs the same blocking poll-until-completion the CLI uses.

- **`injected-session-token`**. The MCP server reads a signed JWT from an env var or file path configured by the runtime. The runtime is responsible for token issuance and refresh. Options: `PODIUM_SESSION_TOKEN_ENV`, `PODIUM_SESSION_TOKEN_FILE`.
- **(Extensible.)** Additional implementations register through the `IdentityProvider` interface (§9).

#### 6.3.1 Claim Derivation

The IdP returns a JWT with claims `{sub, org_id, email, exp, iss, aud}`. Team membership is resolved registry-side via SCIM 2.0 push from the IdP — the registry maintains a directory of `(user_id → teams)`.

For IdPs without SCIM, the `IdpGroupMapping` adapter reads OIDC group claims from the token and maps them to team names per a registry-side configuration.

Tested IdPs: Okta, Entra ID, Auth0, Google Workspace, Keycloak. SAML supported via OIDC bridge.

Fine-grained narrowing via OAuth scope claims (e.g., `podium:read:finance/*`, `podium:load:finance/ap/pay-invoice@1.x`); narrow scopes intersect with the caller's layer visibility — the smaller surface wins.

#### 6.3.2 Runtime Trust Model (`injected-session-token`)

The injected token is a JWT signed by a runtime-specific signing key registered with the registry one-time at runtime onboarding. The registry verifies the signature on every call. Required claims:

- `iss` — runtime identifier (must match a registered runtime).
- `aud` — registry endpoint.
- `sub` — user id the runtime is acting on behalf of.
- `act` — actor (the runtime itself).
- `exp` — expiry.

Without a registered signing key, the registry rejects with `auth.untrusted_runtime`.

##### 6.3.2.1 Token Rotation Contract

- Env-var change is observed at next registry call (no signal needed — the MCP server reads fresh on every call).
- SIGHUP triggers a forced re-read.
- `PODIUM_SESSION_TOKEN_FILE` is watched via fsnotify and re-read on change.

Token rotation is the runtime's responsibility; the MCP server's only obligation is to read fresh on every call. Recommended TTLs: ≤15 min. Prefer `PODIUM_SESSION_TOKEN_FILE` over env var when the runtime can write to a file with restrictive permissions.

### 6.4 Workspace Local Overlay

The workspace local overlay is a per-developer set of `ARTIFACT.md` and `DOMAIN.md` files that merge as the **highest-precedence layer in the caller's effective view** (§4.6). It's the path most teams use for in-progress work that isn't ready to share.

**Path resolution.** All three consumer shapes (MCP server, `podium sync`, language SDKs) honor the same lookup:

1. `PODIUM_OVERLAY_PATH` if set (`Client(overlay_path=...)` on the SDK takes precedence over the env var).
2. The MCP server falls back to MCP roots when available — the `roots/list` response identifies the workspace, and the overlay defaults to `<workspace>/.podium/overlay/` if that directory exists.
3. `podium sync` and the SDK fall back to `<CWD>/.podium/overlay/` if that directory exists.
4. Otherwise: layer disabled.

The MCP server watches the resolved path via fsnotify and re-indexes on change. `podium sync` reads it once per invocation and again on each watcher event when `--watch` is set. The SDK reads it on each `Client.search_artifacts` and `Client.load_artifact` call (cached for the duration of a `session_id`).

Format: same `ARTIFACT.md` + frontmatter as the registry; merge semantics are identical to registry-side layers.

The workspace local overlay is **orthogonal to the registry-side `local` source type** (§4.6): the workspace overlay is merged in by the consumer (MCP server, sync, or SDK) and is visible only to the developer running it, while a registry-side `local`-source layer is read by the registry process and surfaced to whichever identities the layer's visibility declaration allows.

To promote a workspace artifact to a shared layer, copy it into the appropriate Git repo (or registry-side `local` path), commit, and merge.

#### 6.4.1 Local Search Index

When `LocalOverlayProvider` is configured, the MCP server maintains a local BM25 index over local-overlay manifest text. `search_artifacts` calls fan out to both the registry and the local index; the MCP server fuses results via reciprocal rank fusion before returning.

The default is BM25-only — local artifacts have lower recall on semantic queries than registry artifacts, which is acceptable for the developer iteration loop where the goal is "find my draft," not "outrank everything else." Authors who want better local recall can configure the MCP server with an external embedding provider and a vector store via the `LocalSearchProvider` SPI (§9.1). Backends include `sqlite-vec` (embedded, single-file — matching the standalone registry's default in §13.10), a local pgvector instance, or a managed service (Pinecone, Weaviate Cloud, Qdrant Cloud). Cost and identity for any external service are the operator's to manage.

### 6.5 Cache

Disk cache at `${PODIUM_CACHE_DIR}/<sha256>/`. Two cache layers:

- **Resolution cache.** Maps `(id, "latest")` to `semver`. TTL 30s by default. Revalidated via HEAD on hit when `PODIUM_CACHE_MODE=always-revalidate`.
- **Content cache.** Maps `content_hash` to manifest bytes + bundled resources. Forever (immutable by definition).

Cache modes (set at server startup via `PODIUM_CACHE_MODE`):

- `always-revalidate` (default) — HEAD-revalidate the resolution cache on every call.
- `offline-first` — use cached resolution and content if present; only call the registry on miss.
- `offline-only` — never call the registry; cache only.

Index DB: BoltDB or SQLite. `podium cache prune` for cleanup.

In contexts where the home directory is ephemeral, the host points `PODIUM_CACHE_DIR` at an ephemeral or shared volume.

### 6.6 Materialization

On `load_artifact(<id>)`, the registry returns the canonical manifest body inline (or via presigned URL if above the inline cutoff) and presigned URLs for bundled resources. Materialization on the MCP server runs in four steps:

1. **Fetch.** The MCP server downloads each resource (or reads it from the cache) into a temporary staging area. On 403/expired during fetch, retries with a fresh URL set (max 3 attempts, exponential backoff).
2. **Verify.** Signature verification (per `PODIUM_VERIFY_SIGNATURES`); content_hash match; SBOM walk if vulnerability tracking is enabled.
3. **Adapt.** The configured `HarnessAdapter` (§6.7) translates the canonical artifact into the harness's native layout — file names, frontmatter conventions, directory shape — without changing the underlying bytes of bundled resources unless the adapter declares it needs to.
4. **Write.** The MCP server writes the adapted output atomically to a host-configured destination path (`.tmp` + rename), ensuring the destination either contains a complete copy or nothing.

The destination path comes from the host — either via `PODIUM_MATERIALIZE_ROOT` or per-call in the `load_artifact` arguments.

When `PODIUM_HARNESS=none` (the default), step 3 is a no-op: the canonical layout is written directly. Hosts that want raw artifacts — build pipelines, evaluation harnesses, custom scripts — leave the adapter unset.

### 6.7 Harness Adapters

The `HarnessAdapter` translates a canonical artifact into the format a specific harness expects. It runs at materialization time on the MCP server, between fetch and write.

**Built-in adapters** (selected via `PODIUM_HARNESS`):

| Value            | Target                                                                                                                                |
| ---------------- | ------------------------------------------------------------------------------------------------------------------------------------- |
| `none`           | _(default)_ Writes the canonical layout as-is.                                                                                        |
| `claude-code`    | Writes `.claude/agents/<name>.md` (frontmatter + composed prompt) and places bundled resources under `.claude/podium/<artifact-id>/`. |
| `claude-desktop` | Writes a Claude Desktop extension layout (`manifest.json` derived from canonical frontmatter; resources alongside).                   |
| `cursor`         | Writes Cursor's native agent / extension format.                                                                                      |
| `gemini`         | Writes Gemini's native agent / extension package layout.                                                                              |
| `opencode`       | Writes OpenCode's native package layout.                                                                                              |
| `codex`          | Writes Codex's native package layout.                                                                                                 |

**What an adapter does.** Mechanical translation:

- Frontmatter mapping (canonical fields → harness equivalents)
- Prose body composition (canonical body → harness's system-prompt section)
- Resource layout (bundled resources → paths the harness expects)
- Type-specific behavior (`type: skill` → skill; `type: agent` → agent definition)

**What an adapter does not do.** Adapters do not invent semantics. Fields the harness has no equivalent for are left out (or carried in an `x-podium-*` extension namespace if the harness tolerates one).

**Configuration per call.** Hosts can override the harness for a single `load_artifact` call by passing `harness: <value>` in the call arguments.

**Adapter sandbox contract.** Adapters MUST be no-network, MUST NOT write outside the materialization destination, MUST NOT spawn subprocesses. Enforced where Go runtime restrictions allow; documented as the contract for community adapters; conformance suite includes negative tests.

**Cache behavior.** The cache stores canonical artifact bytes (§6.5). Adapter output is regenerated on each materialization by default. An optional in-memory memo cache keyed on `(content_hash, harness)` with 5-minute TTL is enabled for sessions that load the same artifact repeatedly.

**Conformance test suite.** Every built-in adapter passes the same set of tests (§11): load a canonical fixture, produce the harness-native output, verify the harness can spawn an agent that uses the materialized artifact end-to-end.

**Versioning.** Adapter behavior is versioned alongside the MCP server binary. Profile and harness combinations that need a newer adapter behavior pin a minimum MCP server version; older binaries refuse to start.

#### 6.7.1 The Author's Burden

Adapters can only translate features the target harness supports. Authors who use harness-specific features will get degraded materializations elsewhere.

Two mitigations:

1. **Core feature set.** A documented subset of canonical fields and patterns that all built-in adapters support. Authors writing to the core feature set get true "author once, load anywhere."
2. **Capability matrix.** Per-(field, harness) compatibility table maintained alongside the adapters. Ingest-time lint surfaces capability mismatches: "field `X` is used but adapter `cursor` cannot translate it."

Authors who must use a non-portable feature can declare `target_harnesses:` in frontmatter to opt out of cross-harness materialization for that artifact.

**Capability matrix (excerpt; maintained in sync with adapter implementations):**

| Field                      | claude-code | cursor | codex | opencode | gemini |
| -------------------------- | ----------- | ------ | ----- | -------- | ------ |
| `description`              | ✓           | ✓      | ✓     | ✓        | ✓      |
| `mcpServers`               | ✓           | ✓      | ✓     | ✓        | ✓      |
| `delegates_to` (subagents) | ✓           | ✗      | ✗     | ✓        | ✗      |
| `requiresApproval`         | ✓           | ✗      | ✓     | ✓        | ✗      |
| `sandbox_profile`          | ✓           | ✗      | ✓     | ✓        | ✗      |
| `expose_as_mcp_prompt`     | ✓           | ✓      | ✓     | ✓        | ✓      |

### 6.8 Process Model

The MCP server is a stdio subprocess spawned by its host. The host is responsible for lifecycle (spawn, signal handling, shutdown).

- **Developer hosts.** One subprocess per workspace, spawned when the workspace opens and torn down when the workspace closes.
- **Managed agent runtimes.** One subprocess per session, spawned by the runtime's bootstrap glue alongside the agent.

### 6.9 Failure Modes

| Failure                                       | Behavior                                                                                                                                                    |
| --------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Registry offline                              | Serve from cache; return explicit "offline" status on fresh `load_domain` / `search_artifacts`.                                                             |
| Workspace overlay path missing                | Skip the workspace local overlay; warn once.                                                                                                                |
| Auth token expired (`oauth-device-code`)      | Trigger refresh; if interactive refresh required, surface in tool response with reauth instructions via MCP elicitation.                                    |
| Auth token expired (`injected-session-token`) | Surface "token expired"; the host's runtime is responsible for refresh.                                                                                     |
| Untrusted runtime (`injected-session-token`)  | Reject with `auth.untrusted_runtime`. Runtime must register signing key with registry.                                                                      |
| Visibility denial on a call                   | Return a structured error naming the unreachable resource (without leaking the layer's existence); log to the registry audit stream as `visibility.denied`. |
| Materialization destination unwritable        | Fail the `load_artifact` call with a structured error; nothing partial is left on disk.                                                                     |
| Signature verification failure                | Fail with `materialize.signature_invalid`; do not write to disk.                                                                                            |
| Unknown `PODIUM_HARNESS` value                | Refuse to start; CLI lists the available adapter values.                                                                                                    |
| Adapter cannot translate an artifact          | Fail with structured error naming the missing translation; suggest `harness: none` for raw output.                                                          |
| Binary version mismatch with host caller      | Refuse to start; host's CLI prompts an update.                                                                                                              |
| MCP protocol version mismatch                 | Negotiate down to host's max supported MCP version; if no compatible version, fail with `mcp.unsupported_version`.                                          |
| Quota exhausted                               | Structured error (`quota.storage_exceeded` etc.); operation rejected.                                                                                       |
| Runtime requirement unsatisfiable             | Fail with `materialize.runtime_unavailable`; lists the unsatisfied requirement.                                                                             |

### 6.10 Error Model

All errors use a structured envelope:

```json
{
  "code": "auth.untrusted_runtime",
  "message": "Runtime 'managed-runtime-x' is not registered with the registry.",
  "details": { "runtime_iss": "managed-runtime-x" },
  "retryable": false,
  "suggested_action": "Register the runtime's signing key via 'podium admin runtime register'."
}
```

Codes are namespaced (`auth.*`, `ingest.*`, `materialize.*`, `quota.*`, `mcp.*`, `network.*`, `registry.*`). Mapped to MCP error payloads per the MCP spec.

### 6.11 Host Configuration Recipes

The Podium MCP server is a stdio binary the host spawns alongside its other MCP servers. Each host has its own MCP config format; the snippets below show what to add for the common harnesses. All three reuse the same env-var contract from §6.2.

**Claude Desktop** (`~/Library/Application Support/Claude/claude_desktop_config.json` on macOS; equivalents on Windows/Linux):

```json
{
  "mcpServers": {
    "podium": {
      "command": "podium-mcp",
      "env": {
        "PODIUM_REGISTRY": "https://podium.acme.com",
        "PODIUM_HARNESS": "claude-desktop"
      }
    }
  }
}
```

**Claude Code** (project-level `.claude/mcp.json` or user-level `~/.claude/mcp.json`):

```json
{
  "mcpServers": {
    "podium": {
      "command": "podium-mcp",
      "env": {
        "PODIUM_REGISTRY": "https://podium.acme.com",
        "PODIUM_HARNESS": "claude-code",
        "PODIUM_OVERLAY_PATH": "${WORKSPACE}/.podium/overlay/"
      }
    }
  }
}
```

**Cursor** (Settings → MCP, or `~/.cursor/mcp.json`):

```json
{
  "mcpServers": {
    "podium": {
      "command": "podium-mcp",
      "env": {
        "PODIUM_REGISTRY": "https://podium.acme.com",
        "PODIUM_HARNESS": "cursor"
      }
    }
  }
}
```

**Standalone (no env override).** When `podium serve` has auto-bootstrapped `~/.podium/sync.yaml` with `defaults.registry: http://127.0.0.1:8080` (§13.10), or `podium init --global --standalone` has written it explicitly (§7.7), the MCP server resolves the registry from there and the env var can be omitted.

For other MCP-speaking hosts (custom runtimes, non-major harnesses), the same snippet shape applies; `PODIUM_HARNESS=none` writes the canonical layout when no harness-specific adapter is configured.

---

## 7. External Integration

### 7.1 The Registry: External HTTP or Local Filesystem

The registry is the system of record for artifacts. It can be reached two ways:

- **External HTTP service.** The registry runs as a server (standalone or standard deployment, §13.10) and clients reach it over HTTP.
- **Local filesystem.** The registry is a directory of artifacts on disk (§13.11). The Podium CLI reads it directly via `podium sync` for eager materialization.

Both shapes apply layer composition (§4.6). The dispatch between them is governed by the value of `defaults.registry` in the merged `sync.yaml` (§7.5.2): a URL routes to HTTP, a filesystem path routes to local filesystem.

The MCP server (§6), the language SDKs (§7.6), and identity-based visibility filtering require the external HTTP shape; filesystem source does not provide them. The full list of what each shape supports is in §13.11.

#### Latency budgets (SLO targets — server source)

- `load_domain`: p99 < 200 ms
- `search_artifacts`: p99 < 200 ms
- `load_artifact` (manifest only): p99 < 500 ms
- `load_artifact` (manifest + ≤10 MB resources from cache miss): p99 < 2 s

Server deployments that miss these should investigate.

### 7.2 Control Plane / Data Plane Split

The registry exposes two surfaces:

**Control plane (HTTP API).** Returns metadata: manifest bodies, descriptors, search results, domain maps. Synchronous. Audited. Every call carries the host's OAuth identity and is visibility-filtered.

**Data plane (object storage).** Holds bundled resources. The control plane never streams bytes for resources above the inline cutoff (256 KB). Instead, `load_artifact` returns presigned URLs that the Podium MCP server fetches directly from object storage.

Below the inline cutoff, resources are returned inline. This avoids round-trips for small fixtures.

### 7.3 Host Integration

Hosts and authors choose the consumer shape that fits their context:

- **Programmatic runtimes** use `podium-py` or `podium-ts` to call the registry HTTP API directly. The most flexible path — preferred wherever a long-running process can host an HTTP client. Contract: the registry's HTTP API, with layer composition and visibility filtering applied server-side. See §7.6.
- **Hosts that can't run an SDK in-process** (Claude Desktop, Claude Code, Cursor, and similar) spawn the Podium MCP server alongside their own runtime tools. Contract: the three meta-tools plus the materialization semantics described in §6.6.
- **Authors who prefer eager materialization** run `podium sync` (one-shot or watcher) and let the harness's native discovery take over from there, instead of mediating every load through MCP or the SDK at runtime. Contract: the registry's effective view written to a host-configured directory layout via the harness adapter. See §7.5.

Authoring uses Git as the source of truth (§4.6). The Podium CLI handles layer registration, manual reingests, cache management, and admin tasks; it does not push artifact content to the registry.

#### 7.3.1 Authoring and Ingestion

Artifacts enter the registry by being merged into a tracked Git ref (or, for `local` source layers, by being written to the configured filesystem path). The registry mirrors what's in the source layer into its content-addressed store; once `(artifact_id, version)` is ingested, it is bit-for-bit immutable in the registry's store regardless of subsequent Git mutations.

**Author flow:**

1. Edit `ARTIFACT.md` (and bundled resources) in a checkout of the layer's Git repo.
2. Open a PR against the tracked ref. CI runs `podium lint` as a required check.
3. Reviewers approve per the team's branch protection rules.
4. Merge.
5. The Git provider fires a webhook to the registry. The registry fetches the new commit, walks the diff, runs lint as defense in depth, validates immutability, hashes content, stores manifest + bundled resources, indexes metadata, and emits the corresponding outbound event.

`local` source layers skip the Git steps: the author edits files in place and runs `podium layer reingest <id>`.

**Ingestion triggers.** No polling. Two paths:

| Trigger              | Source                                                                                                            |
| -------------------- | ----------------------------------------------------------------------------------------------------------------- |
| Git provider webhook | Configured at layer-creation time. The registry validates the webhook signature, fetches the new commit, ingests. |
| Manual reingest      | `podium layer reingest <id>` (admin or layer owner). For missed webhooks, initial backfill, disaster recovery.    |

`last_ingested_at` is exposed per layer for staleness monitoring.

**Ingest cases:**

| Case                                 | Behavior                                                                                                                                                                                                                    |
| ------------------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| New `(artifact_id, version)`         | Accepted; content hashed and stored.                                                                                                                                                                                        |
| Same version, identical content_hash | No-op. Handles webhook retries idempotently.                                                                                                                                                                                |
| Same version, different content_hash | Rejected as `ingest.immutable_violation`. The author bumps the version.                                                                                                                                                     |
| Lint failure                         | Rejected as `ingest.lint_failed`. The artifact remains at its previous version (if any).                                                                                                                                    |
| Freeze-window in effect              | Rejected as `ingest.frozen` unless `--break-glass` passed via the manual reingest path.                                                                                                                                     |
| Force-push detected                  | Tolerant by default — previously-ingested commits' bytes are preserved in the content store, and a `layer.history_rewritten` event is emitted. Strict mode is configurable per layer (`force_push_policy: strict` rejects). |
| Source unreachable                   | Ingest fails; existing served artifacts are unaffected.                                                                                                                                                                     |

**Layer CLI.**

```
podium layer register --id <id> --repo <git-url> --ref <ref> [--root <subpath>]
podium layer register --id <id> --local <path>
podium layer list
podium layer reorder <id> [<id> ...]            # user-defined layers only
podium layer unregister <id>
podium layer reingest <id> [--break-glass --justification <text>]
podium layer watch <id>                         # local source only
```

`podium layer register` returns the webhook URL and HMAC secret to register on the source repo. Registering a Git source without configuring the webhook leaves the layer at its initial commit until the first manual reingest.

**User-defined layers.** Authenticated users register their own layers via `podium layer register`. Each user-defined layer has implicit visibility `users: [<registrant>]`. Default cap: 3 user-defined layers per identity, configurable per tenant. Reordering via `podium layer reorder` is supported.

**Errors.** Lint failures (`ingest.lint_failed`), webhook signature failures (`ingest.webhook_invalid`), same-version content conflicts (`ingest.immutable_violation`), freeze-window blocks (`ingest.frozen`), quota exhaustion (`quota.*` — including the user-defined-layer cap), source unreachable (`ingest.source_unreachable`), admin-only operations attempted by a non-admin (`auth.forbidden`).

#### 7.3.2 Outbound Webhooks

The registry emits outbound webhooks for:

- `artifact.published` — a new `(artifact_id, version)` was ingested.
- `artifact.deprecated` — a manifest update flipped `deprecated: true`.
- `domain.published` — a `DOMAIN.md` was added or changed.
- `layer.ingested` — a layer completed an ingest cycle (with summary counts).
- `layer.history_rewritten` — force-push detected in a `git` layer.
- `vulnerability.detected` — a CVE matched an artifact's SBOM.

Schema:

```json
{
  "event": "artifact.published",
  "trace_id": "...",
  "timestamp": "...",
  "actor": {...},
  "data": {...}
}
```

Receivers are configured per org (URL + HMAC secret).

### 7.4 Degraded Network

When the registry is unreachable, the MCP server falls back to its content cache. Behavior depends on `PODIUM_CACHE_MODE`:

- `always-revalidate` — fresh calls return `{status: "offline", served_from_cache: true}` alongside cached results; if no cache, structured error `network.registry_unreachable`.
- `offline-first` — no error; serve cached results silently.
- `offline-only` — never contact the registry; structured error if cache miss.

Hosts can surface the offline status to the agent so it can adjust behavior (e.g., warn the user about staleness).

`podium sync` and the SDKs apply the same cache modes.

### 7.5 `podium sync` (Filesystem Delivery)

`podium sync` is the consumer for authors who want to materialize the user's effective view onto disk and let the harness's native discovery take over from there — instead of mediating every load through the MCP server or an SDK at runtime. It works for any harness with a filesystem-readable layout, including ones that also speak MCP. The choice is about authoring preference, not about whether the harness can talk to Podium.

The **target directory defaults to the current working directory.** Every workspace (target) holds its own state; multiple `podium sync` invocations from different folders run independently and don't interfere.

```bash
# One-shot: write the caller's effective view to the current directory
cd ~/.claude/ && podium sync --harness claude-code

# Explicit target
podium sync --harness claude-code --target ~/.claude/

# Watcher: re-sync on registry change events (long-running)
cd ~/.codex/ && podium sync --harness codex --watch

# Path-scoped: sync only artifacts under certain domain paths
podium sync --harness claude-code \
  --include "finance/invoicing/**" --include "shared/policies/*" \
  --exclude "finance/invoicing/legacy/**"

# Type-scoped: sync only artifacts of certain types (useful for split deployments)
podium sync --harness none --type skill,agent

# Profile-driven: load named scope from .podium/sync.yaml
podium sync --profile finance-team

# Dry run: print what would be synced without writing anything
podium sync --dry-run

# Multi-target: write to all configured destinations from sync.yaml
podium sync --config .podium/sync.yaml
```

The sync command reads the caller's effective view (the composed layer list after visibility filtering and `extends:` resolution), applies the requested scope filters, and writes each artifact through the configured `HarnessAdapter` to the target directory.

`podium sync` works against either kind of registry source — a server (URL) or a local filesystem (path); see §7.5.2 for dispatch and §13.11 for filesystem-specific behavior. Against a server source, sync uses the same identity providers as the MCP server, the same content cache, and the same harness adapters.

The sync model is type-agnostic: skills, agents, contexts, prompts, and `mcp-server` registrations all sync through the same path; the harness adapter decides where each type lands.

**`--dry-run`** resolves the artifact set against the current scope and prints it without writing. Default output is human-readable; `--json` produces a structured envelope (`{profile, target, harness, scope, artifacts: [{id, version, type, layer}, ...]}`) for piping into `jq`.

#### 7.5.1 Scope Filters

Three filters narrow the materialized set:

| Flag                  | Repeated? | Effect                                                                                                                                                                                                                                     |
| --------------------- | --------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `--include <pattern>` | Yes       | Glob matched against canonical artifact IDs (the directory path under each layer's root, e.g., `finance/invoicing/run-variance-analysis`). When any `--include` is given, only artifacts matching at least one include pattern are synced. |
| `--exclude <pattern>` | Yes       | Glob matched against canonical artifact IDs. Applied after the include set; a matching pattern removes the artifact.                                                                                                                       |
| `--type <type>[,...]` | No        | Restricts to a comma-separated list of artifact types.                                                                                                                                                                                     |

Patterns use the same glob syntax as `DOMAIN.md include:` (§4.5.2): `*` matches a single path segment, `**` matches recursively, brace expansion `{a,b}` is supported. A bare ID (`finance/invoicing/run-variance-analysis`) matches that artifact exactly.

Visibility is enforced before scope filtering. An artifact that the caller cannot see is not eligible to match an include pattern; this is symmetric with how `search_artifacts` behaves and prevents include patterns from leaking the existence of artifacts in invisible layers.

When neither `--include` nor `--profile` is given, the full effective view is the implicit scope (current behavior).

Path-scoped sync is the recommended way to keep a harness's working set small enough to avoid context rot. Two patterns that work well in practice:

- **Per-team profile.** Each team defines a profile that includes its domain plus shared utilities. Developers run `podium sync --profile <team>`.
- **Programmatic curation.** A script uses the SDK to pick artifacts based on context (the current task, semantic search, etc.), then invokes `podium sync --include <id> [--include <id> ...]` to materialize the chosen set. See §9.3.

#### 7.5.2 Configuration (`sync.yaml`)

`sync.yaml` configures the registry source, profiles, defaults, and multi-target lists. The same schema is read from up to three file scopes; precedence resolves which value wins per key.

**File scopes.**

| Scope          | Path                                  | Typical content                                                                    |
| -------------- | ------------------------------------- | ---------------------------------------------------------------------------------- |
| User-global    | `~/.podium/sync.yaml`                 | per-developer defaults that follow them across projects (typical: harness, target) |
| Project-shared | `<workspace>/.podium/sync.yaml`       | per-project settings committed to git (typical: registry, profile)                 |
| Project-local  | `<workspace>/.podium/sync.local.yaml` | per-developer overrides on top of the project file; gitignored                     |

The schema is identical at every scope — every field that's valid at user-global is valid at project-shared and project-local. Placement is convention, not enforcement: a project that wants to pin harness + target across teammates does so by putting those fields in the project-shared file.

**Workspace discovery.** Project-shared and project-local files are discovered by walking up from CWD until a `.podium/` directory is found, mirroring how `git` finds `.git`. The discovered `.podium/` directory is also the home for `overlay/` (workspace local overlay, §6.4) and `sync.lock` (§7.5.3).

**Precedence.** Resolved per-key, highest precedence first:

1. CLI flags
2. `PODIUM_*` env vars
3. `<workspace>/.podium/sync.local.yaml`
4. `<workspace>/.podium/sync.yaml`
5. `~/.podium/sync.yaml`
6. Built-in defaults

**Profile merge.** Profiles are additive across files — union by profile name. On name collision, the higher-precedence file's definition wins entirely (whole-profile overwrite, no field-level merge inside a profile). A stderr warning fires only when invoking a profile that has a collision (`podium sync --profile X` with multi-defined `X`); a sync against a non-colliding profile stays quiet. `podium config show` (§7.7) always surfaces collisions for debugging.

**Schema:**

```yaml
defaults:
  registry: https://podium.acme.com # see "Registry source" below — accepts URL or filesystem path
  harness: claude-code
  target: ~/.claude/
  profile: project-default # default profile when --profile is not passed

profiles:
  project-default:
    include:
      - "finance/**"
      - "shared/policies/*"
    exclude:
      - "finance/**/legacy/**"
    type: [skill, agent]
  oncall:
    include: ["platform/oncall/**", "shared/runbooks/*"]
    target: ~/.claude-oncall/ # overrides the default target

# Multi-target list selected via --config (without --profile).
# Each entry runs as a separate sync with its own scope and target.
targets:
  - id: claude-code
    harness: claude-code
    target: ~/.claude/
    profile: project-default
  - id: codex-runbooks
    harness: codex
    target: ~/.codex/
    include: ["shared/runbooks/**"]
```

**Registry source.** `defaults.registry` accepts either a URL or a filesystem path; the client adapts:

- **URL** (`http://` / `https://`) — the client speaks HTTP to that registry server. All consumer paths (MCP server, SDKs, `podium sync`, read CLI) work.
- **Filesystem path** (relative or absolute) — `podium sync` reads the directory directly, applies layer composition (§4.6), and materializes through the configured harness adapter. Each subdirectory of the path is a `local`-source layer; ordering is alphabetical (or governed by `<registry-path>/.layer-order`). **`podium sync` is the only consumer that works in this shape** — the MCP server and the SDKs require a server source. See §13.11 for the full filesystem-registry description.
- **Unset across all scopes** — `config.no_registry` error. The client points the user at `podium init` to configure one. There is no implicit workspace fallback; `podium sync` will not auto-detect `.podium/registry/` without explicit config.

Override an inherited URL with a filesystem path by setting `defaults.registry` explicitly at a higher-precedence scope (typically the project-shared file). Normal precedence applies; no magic-value semantics.

**Resolution rules.**

- **Profile lookup.** `--profile <name>` selects an entry under `profiles:`. The profile's fields are merged on top of `defaults:`.
- **CLI override.** Explicit CLI flags override the resolved profile (and defaults) for the same field. `--include` and `--exclude` on the CLI replace the profile's lists rather than appending; if you need additive composition, define a new profile.
- **Multi-target mode.** `podium sync --config <path>` (without `--profile`) iterates `targets:` and runs one sync per entry. Each entry can name a `profile:` (resolved as above) or specify `include`/`exclude`/`type` inline. Each target writes its own `<target>/.podium/sync.lock`; the multi-target invocation does not introduce shared state across targets.
- **Profile composition.** Profiles do not reference other profiles; nesting is intentionally not supported. A team that wants an "extended" profile defines a new entry with the combined include/exclude lists.
- **Validation.** `podium sync --check` validates the merged config against the schema and reports unresolved profile references, malformed globs, target collisions, and profile-name collisions across scopes (warning, not error).

#### 7.5.3 Lock File (`.podium/sync.lock`)

Every target directory holds its own state in `<target>/.podium/sync.lock`. The lock file is per-target — multiple `podium sync` invocations against different targets run independently and don't share state. The cache (`~/.podium/cache/`) stays shared across targets (content-addressed); only sync state is per-target.

`.podium/sync.lock` is git-ignored by default. Teams that want a deterministic shared materialization commit it explicitly.

Schema:

```yaml
# <target>/.podium/sync.lock
version: 1
profile: finance-team # null when no profile was used
scope:
  include: ["finance/**", "shared/policies/*"]
  exclude: ["finance/**/legacy/**"]
  type: [skill, agent]
harness: claude-code
target: /Users/joan/.claude/
last_synced_at: 2026-05-05T14:30:00Z
last_synced_by: full # full | watch | override

# Currently materialized artifacts.
artifacts:
  - id: finance/ap/pay-invoice
    version: 1.2.0
    content_hash: sha256:abc123…
    layer: team-finance
    materialized_path: agents/pay-invoice.md
  - id: finance/close/run-variance
    version: 1.0.0
    content_hash: sha256:def456…
    layer: team-finance
    materialized_path: agents/run-variance.md

# Ephemeral overrides applied since the last full sync.
toggles:
  add:
    - id: finance/experimental/new-thing
      version: 0.1.0
      added_at: 2026-05-05T14:35:00Z
  remove:
    - id: finance/ap/legacy-vendor
      removed_at: 2026-05-05T14:36:00Z
```

The lock file is written atomically (`.tmp` + rename) on every sync, watch event, and override invocation. The `profile:` field is the **active profile** for that target — `override`, `save-as`, and `profile edit` use it as the default when no `--profile` flag is given. Concurrent writers against the same target's lock file (e.g., two `podium sync --watch` processes pointed at one directory) are undefined; operators are expected to keep a single sync owner per target.

The target directory is created if it doesn't exist. The same is true for `<target>/.podium/`. `podium sync` errors only when the target path exists but is not writable.

#### 7.5.4 Watch Mode and Toggle Persistence

Manual `podium sync` and `podium sync --watch` treat the lock file's `toggles:` section differently:

- **Manual sync** (`podium sync`, no `--watch`) — re-resolves the profile, rewrites the target, **clears `toggles` in the lock file**. A manual sync is the operator's "reset to baseline" gesture.
- **Watch mode** (`podium sync --watch`) — long-running. On startup it materializes `profile + toggles from the lock file`, so any overrides from a previous session survive. On every registry change event (`artifact.published`, `artifact.deprecated`, `layer.config_changed`), the watcher re-resolves the profile, applies toggles on top, and updates the lock file. Toggles persist across events and across watcher restarts.
- **Watch scope.** Watchers honor the active scope filters; events for artifacts outside the scope are ignored.

Each watcher is workspace-local. Two `podium sync --watch` processes in two different folders run independently, each against its own lock file.

#### 7.5.5 Ephemeral Override (`podium sync override`)

`podium sync override` is for on-the-fly toggling without touching `sync.yaml`. Toggles live in the target's `.podium/sync.lock` (`toggles.add` / `toggles.remove`) and are reset by the next manual `podium sync`. They survive watcher events.

Two modes on a single command:

```bash
# TUI: launches a checklist over the resolved set + everything else the caller can see
podium sync override

# Batch: --add and --remove are repeatable, exact IDs only
podium sync override --add finance/experimental/new-thing
podium sync override --remove finance/ap/legacy-vendor
podium sync override --add finance/foo --add finance/bar --remove finance/baz

# Preview without writing
podium sync override --add finance/foo --dry-run

# Clear all toggles in the current target
podium sync override --reset
```

**TUI mode** (no flags). Renders the caller's effective view as an expandable tree (domains as nodes, artifacts as leaf checkboxes). Each entry is annotated with its current state — already materialized, excluded by the profile's `exclude`, etc. — and the layer it comes from. The user toggles items; on quit, the TUI applies the diff to the target directory and updates the lock file.

**Batch mode** (with flags). `--add <id>` fetches and writes the artifact through the active harness adapter, just like a full sync would; the entry lands in `toggles.add`. `--remove <id>` deletes the artifact's materialized files from the target; the entry lands in `toggles.remove` (and is removed from `toggles.add` if it was there). Repeatable. The pair is idempotent — running `--add` on something already materialized is a no-op with a warning.

**Scope.** Override operates on any artifact the caller's identity can see, regardless of the active profile's include/exclude. Visibility filtering still applies — the caller can't `--add` something they can't see. This is the point of override: bring in (or drop) artifacts the profile didn't think of.

**`--reset`** clears `toggles` in the lock file and re-applies the profile's resolved set, dropping artifacts that were `add`ed and re-materializing artifacts that were `remove`d. Equivalent to running a manual `podium sync`.

#### 7.5.6 Saving Toggles as a Profile (`podium sync save-as`)

After working with overrides for a while, an operator can capture the current materialized set as a YAML profile:

```bash
# Save the current materialized set as a new profile in .podium/sync.yaml
podium sync save-as --profile finance-team-v2

# Update an existing profile in place
podium sync save-as --profile finance-team --update

# Print the proposed YAML diff without writing
podium sync save-as --profile finance-team --update --dry-run
```

`save-as` reads the current lock file (`scope` + `toggles`), renders an equivalent `include` / `exclude` / `type` block, and writes it to `sync.yaml`. The mapping:

- Existing scope `include` and `exclude` carry over verbatim.
- Each `toggles.add` entry becomes an `include:` entry pinned to the exact ID.
- Each `toggles.remove` entry becomes an `exclude:` entry pinned to the exact ID.
- Type filter carries over.

After `save-as` succeeds, the target's lock file `toggles:` is cleared (the toggles are now part of the profile's scope). If `.podium/sync.yaml` doesn't exist yet, `save-as` creates it with the new profile and an empty `defaults:` block.

#### 7.5.7 Editing Profiles Permanently (`podium profile edit`)

A separate command for permanent edits to entries in `sync.yaml`. Distinct from `podium sync override`, which is ephemeral.

```bash
# TUI for the active or named profile
podium profile edit
podium profile edit finance-team

# Batch: add/remove patterns to/from include or exclude
podium profile edit finance-team --add-include "finance/new-thing/**"
podium profile edit finance-team --remove-exclude "finance/old-deprecated/**"

# Print proposed YAML diff without writing
podium profile edit finance-team --add-include "finance/foo" --dry-run
```

`podium profile edit` modifies `sync.yaml` in place, preserving formatting and comments around the edited keys (round-trip via a comment-preserving YAML parser). It does not touch the target directory or any lock file — to apply the change to a workspace, run `podium sync` afterwards. If `.podium/sync.yaml` doesn't exist, `podium profile edit <name>` creates it with the named profile and an empty `defaults:` block; `podium profile edit` (no name) errors and asks the user to specify a name.

The flag names are distinct from `podium sync --include` / `--exclude` (ephemeral scope flags applied at sync time) and from `podium sync override --add` / `--remove` (ephemeral toggles on exact IDs). `podium profile edit` writes patterns into the profile YAML; the other two never touch `sync.yaml`.

### 7.6 Language SDKs

Two thin language SDKs are provided, both backed by the registry's HTTP API:

- **`podium-py`** (PyPI) — for Python orchestrators. Used by LangChain consumers, OpenAI Assistants integrations, custom build/eval pipelines, and notebook environments.
- **`podium-ts`** (npm) — for TypeScript / Node orchestrators. Used by Bedrock Agents, custom Node-based agent runtimes, and Edge runtime integrations.

**Require a server-source registry.** Both SDKs speak HTTP and do not work against a filesystem-source registry (§13.11).

Surface area:

```python
from podium import Client

# from_env reads PODIUM_REGISTRY, PODIUM_IDENTITY_PROVIDER,
# PODIUM_OVERLAY_PATH, etc. Constructor params override env values.
client = Client.from_env()

# Or pass explicitly:
client = Client(
    registry="https://podium.acme.com",
    identity_provider="oauth-device-code",
    overlay_path="./.podium/overlay/",   # workspace local overlay (§6.4)
)

# Discovery
domains = client.load_domain("finance/close-reporting")
results = client.search_artifacts("variance analysis", type="skill")

# Full filter surface: type, tags, scope, top_k, session_id
results = client.search_artifacts(
    "variance analysis",
    type="skill",
    tags=["finance", "close"],
    scope="finance/close-reporting",
    top_k=10,
    session_id=session_id,
)

# Type-specific lookups
agents = client.search_artifacts("payment workflow", type="agent")
contexts = client.search_artifacts("style guide", type="context")
mcp_servers = client.search_artifacts(type="mcp-server")

# Load (in-memory or to disk)
artifact = client.load_artifact("finance/close-reporting/run-variance-analysis")
print(artifact.manifest_body)
artifact.materialize(to="./artifacts/", harness="claude-code")  # respects the harness adapter

# Streaming change events for sync use cases
for event in client.subscribe(["artifact.published", "artifact.deprecated"]):
    ...

# Cross-type dependency walks (for impact analysis in custom tooling)
deps = client.dependents_of("finance/ap/pay-invoice@1.2")
```

Identity providers, the cache, visibility filtering, layer composition, and audit are all the same as in the MCP path — the SDK is just a different transport. Identity provider plug-points are exposed; custom providers register through the same interface as the MCP server's.

The SDKs deliberately do not implement the MCP meta-tool semantics (the agent-driven lazy materialization). Programmatic consumers know what they want; they don't need an LLM-mediated browse interface. If a programmatic consumer wants lazy semantics, it can call `load_artifact` lazily in its own code.

#### 7.6.1 Read CLI

For shell pipelines and language-agnostic scripts that don't want to take a Python or Node dependency just to read the catalog, the same read operations are exposed as `podium` subcommands. Each maps 1:1 to the corresponding SDK call and uses the same identity, cache, layer composition, and visibility filtering server-side.

| Command                       | Maps to                                    | Behavior                                                                                                                                                                         |
| ----------------------------- | ------------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `podium search <query>`       | `Client.search_artifacts(...)`             | Hybrid search. Flags `--type`, `--tags`, `--scope`, `--top-k` mirror the SDK args. Returns ranked descriptors.                                                                   |
| `podium domain show [<path>]` | `Client.load_domain(path)`                 | Domain map for `<path>` (or root when no path is given).                                                                                                                         |
| `podium artifact show <id>`   | `Client.load_artifact(id)` (manifest only) | Prints the manifest body and frontmatter to stdout. **Does not materialize bundled resources** — for that, use `podium sync --include <id>`. Flags: `--version`, `--session-id`. |

Output formats:

- **Default** — human-readable rendering. Search results are a ranked table; domain trees are nested bullets; manifests are printed as the markdown body with frontmatter at the top.
- **`--json`** — structured envelope with stable keys, designed to be piped into `jq`. Schemas:

  ```json
  // podium search ... --json
  { "query": "...", "results": [ { "id": "...", "type": "...", "version": "...",
                                   "score": 0.83, "frontmatter": { ... } }, ... ] }

  // podium domain show <path> --json
  { "path": "...", "subdomains": [ { "path": "...", "name": "..." }, ... ],
    "artifacts": [ { "id": "...", "type": "...", "summary": "..." }, ... ] }

  // podium artifact show <id> --json
  { "id": "...", "version": "...", "content_hash": "...",
    "frontmatter": { ... }, "body": "..." }
  ```

The CLI and SDK are intentionally interchangeable for these read operations — pick whichever fits the surrounding code. Both defer to the same `RegistrySearchProvider`, `LayerComposer`, and cache paths server-side; output drift between them is treated as a bug.

Example pipeline — fully scripted curation without an SDK install:

```bash
podium search "month-end close OR variance" --type skill --top-k 15 --json \
  | jq -r '.results[] | select(.score > 0.5) | .id' \
  | xargs -I{} podium sync --harness claude-code --target ~/.claude/ --include {}
```

#### 7.6.2 Bulk Fetch

`load_artifact` works one ID at a time. Programmatic consumers — eval harnesses, batch workflows, custom orchestrators — that need a known set of artifacts up front pay the per-request round-trip N times when iterating. `Client.load_artifacts` is the bulk variant: one HTTP request, one auth check, one visibility composition pass, one transactional snapshot.

```python
artifacts = client.load_artifacts(
    ids=[
        "finance/close-reporting/run-variance-analysis",
        "finance/close-reporting/policy-doc",
        "finance/ap/pay-invoice",
    ],
    session_id=session_id,        # honors the same `latest`-resolution semantics as load_artifact
    harness="claude-code",        # optional per-call adapter override
)

for result in artifacts:
    if result.status == "ok":
        result.materialize(to="./artifacts/")
    else:
        log.warning("skip %s: %s", result.id, result.error.code)
```

**Wire shape.** `POST /v1/artifacts:batchLoad` with body `{ids: [...], session_id?, harness?, version_pins?: {<id>: <semver>}}`. Response is an array of per-item envelopes:

```json
[
  {
    "id": "finance/close-reporting/run-variance-analysis",
    "status": "ok",
    "version": "1.2.0",
    "content_hash": "sha256:...",
    "manifest_body": "...",
    "resources": [
      { "path": "...", "presigned_url": "...", "content_hash": "..." }
    ]
  },
  {
    "id": "finance/restricted/payroll-runner",
    "status": "error",
    "error": { "code": "visibility.denied", "message": "..." }
  }
]
```

**Semantics.**

- **Hard cap:** 50 IDs per batch. The SDK splits larger sets transparently.
- **Visibility:** identical to `load_artifact`. Items the caller cannot see come back as `status: "error"` with `visibility.denied`; no leak about whether the artifact exists in some hidden layer.
- **Session consistency:** with `session_id`, the first occurrence of each `(id, "latest")` in the batch freezes the resolved version for the rest of the batch and session.
- **Partial failure** does not fail the batch — each item carries its own status.
- **Bandwidth:** large bundled resources travel via presigned URLs (§4.4) so the response body stays small; the SDK fetches resources concurrently after the response.

**Not exposed as an MCP meta-tool** (§5). The MCP path is agent-mediated and load-on-demand; bulk loading is a programmatic-runtime concern that doesn't belong in the agent's tool list. The MCP server uses this endpoint internally for cache warm-up when configured to prefetch.

### 7.7 Onboarding: `podium init`, `podium config show`, `podium login`

Three commands cover client-side lifecycle: `podium init` writes a `sync.yaml`; `podium config show` displays the merged result with provenance; `podium login` runs the OAuth flow when the resolved registry is a server. Server-side setup is handled separately by `podium serve` (§13.10), which auto-bootstraps standalone defaults on first run.

#### `podium init`

Writes a `sync.yaml`. Default scope is workspace (`<ws>/.podium/sync.yaml`, committed to git); scope flags `--global` and `--local` target the user-global file or the gitignored project-local override file respectively. Idempotent — refuses to overwrite an existing file without `--force`.

```bash
# Workspace (default) — writes <ws>/.podium/sync.yaml, committed
podium init                                              # interactive wizard
podium init --registry https://podium.acme.com           # set the project's registry
podium init --registry .podium/registry/                 # filesystem source — see §13.11
podium init --standalone                                  # shortcut for --registry http://127.0.0.1:8080
podium init --harness claude-code --target .claude/       # set per-project defaults at init time

# User-global — writes ~/.podium/sync.yaml
podium init --global                                      # interactive
podium init --global --registry https://podium.acme.com
podium init --global --standalone

# Workspace personal override — writes <ws>/.podium/sync.local.yaml (gitignored)
podium init --local
podium init --local --registry https://podium-staging.acme.com
```

Mental model: scope flags (`--global`, `--local`) decide _where the file goes_; value flags (`--registry`, `--standalone`, `--harness`, `--target`) decide _what's in it_. Default scope is workspace because that's the common case; making it implicit keeps the standard onboarding path short.

`--registry <url-or-path>` accepts either a URL (server source) or a filesystem path (filesystem source, §13.11). The flag's value type determines the registry shape; there is no separate `--mode` flag.

`--standalone` is a shortcut for `--registry http://127.0.0.1:8080`. It is purely client-side (this command only writes `sync.yaml`); the server itself is started by `podium serve`. For the all-in-one server-and-client bootstrap, use `podium serve` zero-flag (§13.10), which writes both `registry.yaml` and `sync.yaml` and starts serving.

**Workspace mode behavior:**

1. Walks up from CWD to find an existing `.podium/` directory; if none, creates `.podium/` in CWD.
2. Writes `<workspace>/.podium/sync.yaml` with the chosen value flags as `defaults`.
3. Adds `.podium/sync.local.yaml` and `.podium/overlay/` entries to `.gitignore` (creating it if needed) if they aren't already present.
4. Prints next-step hints (commit `<ws>/.podium/sync.yaml`, run `podium sync` to materialize).

`--global` writes to `~/.podium/sync.yaml` regardless of CWD and does not touch `.gitignore`. `--local` writes to `<ws>/.podium/sync.local.yaml` and assumes the workspace is already initialized.

#### `podium config show`

Prints the merged `sync.yaml` with per-key provenance:

```
$ podium config show
defaults.registry:   https://podium.acme.com         (from <ws>/.podium/sync.yaml)
defaults.harness:    claude-code                      (from ~/.podium/sync.yaml)
defaults.target:     .claude/                         (from <ws>/.podium/sync.yaml)
profiles.project-default:
  include:           ["finance/**", "shared/policies/*"]   (from <ws>/.podium/sync.yaml)
  exclude:           ["finance/**/legacy/**"]              (from <ws>/.podium/sync.yaml)
profiles.staging:                                      (defined in <ws>/.podium/sync.local.yaml)
  registry:          https://podium-staging.acme.com
…

Profile collisions: 1 (profiles.staging defined in both ~/.podium/sync.yaml and <ws>/.podium/sync.local.yaml; project-local wins)
```

`--explain <key>` prints just one key with its full resolution chain — which file each scope had, and which won. Useful when the merged output is large.

#### `podium login`

Explicit OAuth device-code flow against the resolved registry. Useful when sync is being scripted (auth before the script runs), when re-authing after token expiry, or when the user wants to confirm their identity before doing anything destructive.

```bash
podium login                                    # uses the merged config to find the registry
podium login --registry https://podium.acme.com # override; useful for ad-hoc switches
podium login --no-browser                       # don't auto-open the verification URL
podium logout                                   # clears the cached token from the OS keychain
```

Behavior: resolves the registry from the merged config (or `--registry` flag), prints the verification URL and code to stderr, attempts to open the URL in the system browser, polls the IdP's token endpoint until the user completes the flow or a 10-minute timeout elapses, caches the access + refresh tokens in the OS keychain (per `oauth-device-code` in §6.3), and prints the resolved identity (`sub`, `email`, OIDC groups) on success. Exits non-zero on timeout, denial, or `auth.untrusted_runtime`.

**Multi-endpoint behavior.** Tokens cache in the OS keychain keyed by registry URL. A developer logged into both `https://podium.acme.com` and `https://podium-finance.acme.com` keeps both tokens simultaneously; switching projects (or running `podium login` in any context) authenticates against whichever registry the merged config resolves to. No `podium logout` between project switches required.

`podium login` is a no-op when the resolved registry is a filesystem path (no auth) or points at a `--standalone` server (no auth) — in both cases it prints a notice and exits.

---

## 8. Audit and Observability

### 8.1 What Gets Logged

Every significant event, each carrying a trace ID (W3C Trace Context):

| Event                     | When                                                               | Source   |
| ------------------------- | ------------------------------------------------------------------ | -------- |
| `domain.loaded`           | Host invoked `load_domain`                                         | Registry |
| `artifacts.searched`      | Host invoked `search_artifacts`                                    | Registry |
| `artifact.loaded`         | Host invoked `load_artifact`                                       | Registry |
| `artifact.published`      | A new `(artifact_id, version)` was ingested                        | Registry |
| `artifact.deprecated`     | An ingested manifest set `deprecated: true`                        | Registry |
| `artifact.signed`         | Artifact version signed                                            | Registry |
| `domain.published`        | A `DOMAIN.md` was added or changed                                 | Registry |
| `layer.ingested`          | A layer completed an ingest cycle                                  | Registry |
| `layer.history_rewritten` | Force-push or history rewrite detected on a `git`-source layer     | Registry |
| `layer.config_changed`    | Admin added, removed, or reordered admin-defined layers            | Registry |
| `layer.user_registered`   | A user registered or unregistered a personal layer                 | Registry |
| `admin.granted`           | An admin grant was added or revoked                                | Registry |
| `visibility.denied`       | A call was rejected because the requested resource was not visible | Registry |
| `freeze.break_glass`      | An admin used break-glass during a freeze window                   | Registry |
| `vulnerability.detected`  | CVE matched an artifact's SBOM                                     | Registry |
| `user.erased`             | Admin invoked the GDPR erasure command                             | Registry |

Audit lives in two streams. The registry owns the events above. The MCP server can also write a local audit log for the meta-tool events through a `LocalAuditSink` interface (§9) when configured. Both streams share trace IDs.

**Caller identity in audit events.** Read events (`domain.loaded`, `artifacts.searched`, `artifact.loaded`) record the caller's identity from the OAuth token: typically `caller.identity = "<sub-claim>"`, with email and groups attached. In public-mode deployments (§13.10), the OAuth flow is skipped and these events instead record `caller.identity = "system:public"`, with the source IP address and any upstream `X-Forwarded-User` header preserved in `caller.network`. Public-mode events also carry the flag `caller.public_mode: true` so downstream consumers (SIEM, audit dashboards) can filter them without parsing identity strings.

### 8.2 PII Redaction

Two redaction surfaces:

- **Manifest-declared.** Artifact manifests can specify fields that should be redacted in audit logs (e.g., `bank_account`, `ssn`). The registry honors redaction directives; the MCP server applies the same directives before writing to its local audit sink.
- **Query text.** Free-text `search_artifacts` queries are regex-scrubbed for common PII patterns (SSN, credit-card, email, phone) before being written to audit. Patterns configurable via `PIIRedactionConfig`. Default-on.

### 8.3 Audit Sinks

The registry has its own sink for catalogue events. The local file log, when enabled via `PODIUM_AUDIT_SINK`, is written by the MCP server through the `LocalAuditSink` interface. The local sink defaults to `~/.podium/audit.log` (user-wide — one file across all workspaces). Operators who need per-project scoping point `PODIUM_AUDIT_SINK` at a workspace path such as `${WORKSPACE}/.podium/audit.log`. Both the registry and local sinks can be redirected to external SIEM / log aggregation independently.

### 8.4 Retention

Defaults, configurable per deployment:

| Data                                | Retention                                               |
| ----------------------------------- | ------------------------------------------------------- |
| Audit events (metadata)             | 1 year                                                  |
| Query text                          | 30 days (redacted to placeholders after 7 days)         |
| Deprecated artifact versions        | 90 days after the deprecation flag is set               |
| Layers unregistered by their owners | 30 days (artifacts soft-deleted, recoverable via admin) |
| Vulnerability scan history          | 1 year                                                  |

Optional sampling for high-volume low-sensitivity events (e.g., `domain.loaded` at 10% sample) reduces storage cost.

### 8.5 Erasure

```
podium admin erase <user_id>
```

- Unregisters and purges any user-defined layers owned by the user (and the artifacts ingested from them).
- Redacts the user identity in audit records (replaces with `redacted-<sha256(user_id+salt)>`).
- Preserves audit event sequencing for integrity.

GDPR right-to-erasure is supported via this command. Erasure is itself logged as a `user.erased` event.

### 8.6 Audit Integrity

Every audit event carries a hash chain: `event_hash = sha256(event_body || prev_event_hash)`. Detection of gaps is automated and alerted.

Periodic anchoring of the chain head to a public transparency log (Sigstore/CT-style) is recommended for high-assurance deployments. SIEM mirroring is the operational integrity backstop.

---

## 9. Extensibility

Podium is extensible at two layers. **In-process plugins** swap or augment the registry's own behavior — different stores, different identity providers, different lint rules — by implementing a Go interface and being compiled into a registry build (§9.1). **External extensions** build on the registry's HTTP API, SDKs, and CLI without changing the registry itself — programmatic curation scripts, webhook receivers, custom CI checks, layer source bridges (§9.3). Most teams reach for external extensions first; SPI plugins are for cases where the registry's own behavior needs to change.

### 9.1 Pluggable Interfaces

| Interface                | Default                                                                    | Purpose                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                               |
| ------------------------ | -------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `RegistryStore`          | Postgres (standard) / SQLite (standalone)                                  | Manifest metadata, dependency edges, layer config, admin grants, registry-side audit. Embeddings live here too when the default vector backend is in use; see `RegistrySearchProvider`.                                                                                                                                                                                                                                                                                                                                                                                                                                                               |
| `RegistryObjectStore`    | S3-compatible (filesystem in standalone)                                   | Bundled resource bytes, presigned URLs                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                |
| `RegistrySearchProvider` | `pgvector` (standard) / `sqlite-vec` (standalone), with BM25 fused via RRF | Hybrid retrieval for `search_artifacts`. Built-ins shipped in the default binary, selectable via `PODIUM_VECTOR_BACKEND`: `pgvector`, `sqlite-vec`, `pinecone`, `weaviate-cloud`, `qdrant-cloud`. Each implementation declares a `self_embedding` capability — Pinecone Integrated Inference, Weaviate vectorizer, and Qdrant Cloud Inference set this true and don't require an `EmbeddingProvider`; storage-only backends (`pgvector`, `sqlite-vec`, plain `qdrant`, plain `weaviate`) require one. Custom backends register through this SPI as Go-module plugins (§9.2). Dual-write semantics for non-collocated backends are documented in §4.7. |
| `EmbeddingProvider`      | `embedded-onnx` (standalone) / `openai` (standard)                         | Generates embeddings for ingest text and for `search_artifacts` queries. Built-ins shipped in the default binary, selectable via `PODIUM_EMBEDDING_PROVIDER`: `embedded-onnx`, `openai`, `voyage`, `cohere`, `ollama`. Required when `RegistrySearchProvider` is storage-only; optional override when the backend self-embeds. See §4.7 (Embedding generation).                                                                                                                                                                                                                                                                                       |
| `LocalSearchProvider`    | BM25 over local-overlay manifests                                          | Optional semantic backing for the local-overlay index (§6.4.1). Same SPI shape as `RegistrySearchProvider`; backends include `sqlite-vec`, a local pgvector instance, or any managed vector service. Embedding-provider selection follows the same rules as the registry-side path.                                                                                                                                                                                                                                                                                                                                                                   |
| `RegistryAuditSink`      | Separate Postgres table within `RegistryStore`                             | Stream for catalogue events; logically distinct, separately mockable, separately routable                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| `LayerComposer`          | Layer-list composition + visibility filtering                              | Resolves the caller's effective view from the configured layer list (§4.6); applies merge semantics and `extends:` resolution                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                         |
| `GitProvider`            | GitHub                                                                     | Webhook signature verification and Git fetch semantics. Built-in support for GitHub, GitLab, Bitbucket; additional providers register through this interface.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                         |
| `TypeProvider`           | Built-in first-class types                                                 | Type definitions: frontmatter JSON Schema + lint rules + adapter hints + field-merge semantics                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                        |
| `IngestLinter`           | Built-in rule registry                                                     | Manifest validation, resource-reference checks, type-specific rules; runs pre-merge in CI and again at registry ingest                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                |
| `IdentityProvider`       | `oauth-device-code` (alt: `injected-session-token`)                        | Attaches OAuth-attested identity to every registry call from the MCP server                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                           |
| `LocalOverlayProvider`   | Workspace filesystem (`.podium/overlay/`)                                  | Source for the workspace-scoped local overlay layer (§6.4)                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                            |
| `LocalAuditSink`         | JSON Lines file at `~/.podium/audit.log`                                   | Local audit log for meta-tool calls (when configured). User-wide by default — one file across all workspaces. Redirect to a workspace-local path or an external endpoint via `PODIUM_AUDIT_SINK`. Concurrent appends from multiple MCP servers are safe under typical event sizes (POSIX `PIPE_BUF`-bounded atomic writes).                                                                                                                                                                                                                                                                                                                           |
| `HarnessAdapter`         | `none` (built-ins per §6.7)                                                | Translates canonical artifacts to the harness's native format at materialization time                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                 |
| `NotificationProvider`   | Email + webhook                                                            | Delivery for vulnerability alerts and ingest-failure notifications                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                    |
| `SignatureProvider`      | Sigstore-keyless                                                           | Artifact signing and verification                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                     |

### 9.2 Plugin Distribution

Plugins ship as Go modules importable into a registry build. A deployment that needs a custom `IdentityProvider` or `GitProvider` builds a registry binary from source with the plugin imported.

A community plugin registry is hosted at the project's public URL.

### 9.3 Building on Podium from outside the registry

The registry's HTTP API, the SDKs, the CLI, and the outbound webhook stream are designed to be composed into team-specific tooling without touching the registry binary. Common patterns:

#### Programmatic curation (semantic discovery + scoped sync)

A script picks artifacts based on whatever context is meaningful — semantic match against a query, the user's recent work, the active project, an upstream ticket — and then invokes `podium sync` with `--include` flags to materialize the selected set. The script owns the discovery logic; Podium owns the materialization (visibility filtering, `extends:` resolution, harness adaptation, audit). The on-disk result is reproducible from the include list.

Discovery can use either the SDK (when the script is in Python or TypeScript and wants typed results) or the read CLI (§7.6.1, when a shell pipeline is enough or the surrounding code is in another language). Both surface the same `search_artifacts` / `load_domain` / `load_artifact` operations.

```python
from podium import Client
import subprocess

client = Client.from_env()

# Discovery: whatever logic the team wants. Here, semantic match + a score floor.
results = client.search_artifacts(
    "month-end close OR variance analysis",
    type="skill",
    top_k=15,
)
ids = [r.id for r in results if r.score > 0.5]

# Materialization: hand the chosen ids to `podium sync` so the on-disk view is
# auditable and reproducible from the include list.
subprocess.run(
    [
        "podium", "sync",
        "--harness", "claude-code",
        "--target", "/Users/me/.claude/",
        *sum((["--include", artifact_id] for artifact_id in ids), []),
    ],
    check=True,
)
```

The same pattern handles other discovery strategies: the script could read recent files in the workspace and search for related artifacts, follow `dependents_of()` from a starting artifact, or consult an external system (a ticket, a calendar) before deciding what to materialize. Whatever the script decides, `podium sync` performs the write.

This is the recommended answer to "I have a thousand artifacts but my harness only needs ~30 in context for this session." Curate, then sync.

#### Webhook-driven integrations

Receivers for the outbound webhooks (§7.3.2) feed Slack channels, ticket trackers, deployment pipelines, internal dashboards. The registry emits the events; the receiver decides what to do. Common targets: notify owners on `artifact.deprecated`, post to a channel on `vulnerability.detected`, kick off a downstream rebuild on `artifact.published` matching certain paths.

#### Custom pre-merge CI

Each layer's source repo runs whatever CI checks the team wants — naming conventions, sensitivity sign-off, banned dependencies, structural rules — using `podium lint` plus team-specific scripts in the same pipeline. These checks are out of Podium's scope; they're ordinary CI in the layer's source repository, gated by branch protection.

#### Layer source bridges

A script that pulls content from another system (a vendor SaaS, an internal CMS, a documentation generator) and writes it into a `local`-source layer's filesystem path. The registry ingests via `podium layer reingest <id>` (manually or on a schedule the bridge controls). The bridge runs wherever the team wants; Podium just serves what's in the layer's path at the time of ingest.

#### Custom consumer surfaces

A runtime that doesn't fit the three built-in consumer shapes — a specialized agent framework, an internal orchestrator, an evaluation harness — wraps the registry HTTP API directly. Identity attaches via the same OAuth flow used by the SDKs; visibility filtering and layer composition still happen server-side. The custom consumer is responsible for caching and any harness-native translation it needs.

---

## 10. MVP Build Sequence

The build sequence is structured in two parts. Phases 0–4 ship an initial release that exercises the architecture end-to-end against the two lightest local deployment shapes — a filesystem-source registry (no daemon) and a `--standalone` server. Phases 5+ add the enterprise capabilities (multi-tenancy, OIDC/SCIM, full RBAC, audit, vulnerability tracking, deployment).

### Initial phases

| Phase | What                                                                                                                                                                                                                                                                                                                                                                                                  | Why                                                                                        |
| ----- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------ |
| 0     | Filesystem-source `podium sync` (§13.11) — `podium sync` reads `.podium/registry/`, applies layer composition, writes the harness target. + `podium serve --standalone` for the same artifact directory plus search and HTTP API (single binary, embedded SQLite + sqlite-vec, no auth).                                                                                                              | Cover the two lightest deployment shapes. Five-minute install for personal/small-team use. |
| 1     | Manifest schema + `podium lint` for `ARTIFACT.md` and `DOMAIN.md` + per-type lint rules + signing                                                                                                                                                                                                                                                                                                     | Authors need a way to validate artifacts; lint is the early quality bar                    |
| 2     | Registry HTTP API: `load_domain`, `search_artifacts`, `load_artifact` (against `--standalone`)                                                                                                                                                                                                                                                                                                        | The wire surface every server-source consumer talks to                                     |
| 3     | `podium sync` for `none`, `claude-code`, and `codex` adapters (default target = CWD; `--include` / `--exclude` / `--type` / `--profile` / `--dry-run`) + per-target lock file (`.podium/sync.lock`) + `--watch` against both source types + multi-type reference catalog (skills, agents, contexts, prompts, mcp-servers) + `podium init` (workspace / `--global` / `--local`) + `podium config show` | Filesystem delivery end-to-end with the full client config surface.                        |
| 4     | Podium MCP server core (registry client, layer composer, MCP handlers, cache, materialization) + `podium-py` SDK + read CLI (`podium search`, `podium domain show`, `podium artifact show`) against `--standalone`                                                                                                                                                                                    | Exercises MCP-speaking and programmatic runtimes against the catalog (server source only)  |

### Enterprise phases

| Phase | What                                                                                                                                                                                                                   | Why                                                                                  |
| ----- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------ |
| 5     | Multi-tenant registry data model (Postgres + pgvector + object storage layout, layer config table, admin grants)                                                                                                       | The catalog at scale                                                                 |
| 6     | `GitProvider` + webhook-driven ingest pipeline (signature verification, diff walk, lint, immutability check, content-addressed store)                                                                                  | Authoring source-of-truth model in production                                        |
| 7     | `LayerComposer` + visibility filtering (`public` / `organization` / OIDC groups / explicit users) + OIDC + SCIM 2.0                                                                                                    | Multi-tenant correctness                                                             |
| 8     | Domain composition: `DOMAIN.md` parser, glob resolver, `unlisted` enforcement, cross-layer merge, `extends:` with hidden-parent resolution                                                                             | Multi-layer composition without duplication                                          |
| 9     | Versioning: semver, immutability invariant on ingest, content-hash cache keys, `latest` resolution with `session_id` consistency, tolerant force-push handling                                                         | Foundational invariant                                                               |
| 10    | Layer CLI: `podium layer register / list / reorder / unregister / reingest / watch`; user-defined-layer cap; freeze windows; `podium admin grant/revoke`                                                               | Operator and author surface for the layer model                                      |
| 11    | IdentityProvider implementations: `oauth-device-code` (with OS keychain) and `injected-session-token` (signed JWT contract)                                                                                            | One MCP server / sync / SDK binary across deployment contexts                        |
| 12    | Workspace `LocalOverlayProvider` + local BM25 search index                                                                                                                                                             | Workspace iteration loop visible to the MCP-bridge consumer                          |
| 13    | Full `HarnessAdapter` implementations for the remaining built-ins (`claude-desktop`, `cursor`, `gemini`, `opencode`) + conformance test suite                                                                          | Cross-harness coverage for all artifact types                                        |
| 14    | `podium-ts` SDK + remaining SDK surface area (subscriptions, dependency walks); `podium sync override` (TUI + batch flags), `podium sync save-as`, and `podium profile edit` for ephemeral and permanent scope changes | Programmatic-runtime parity with the MCP path; TUI/batch toggles for materialization |
| 15    | Cross-type dependency graph + reverse dependency index + impact analysis CLI                                                                                                                                           | Cross-type analysis: surface what depends on a given artifact, regardless of type    |
| 16    | Registry audit log + `LocalAuditSink` + cross-stream correlation + retention + hash-chain integrity                                                                                                                    | Observability + governance                                                           |
| 17    | Vulnerability tracking + SBOM ingestion + `NotificationProvider`                                                                                                                                                       | Enterprise governance                                                                |
| 18    | Deployment: Helm chart, reference Grafana dashboard, runbook                                                                                                                                                           | Operability                                                                          |
| 19    | Example artifact registry (multi-layer, multi-type, with `DOMAIN.md` imports, unlisted folders, signatures, cross-type delegation)                                                                                     | Prove end-to-end                                                                     |

---

## 11. Verification

- **Unit tests**: registry HTTP handlers, layer composer, visibility evaluator, `DOMAIN.md` parser and glob resolver, ingest linter, manifest schema validator, MCP server forwarder, workspace local-overlay watcher and merge, content-addressed cache, atomic materialization, OAuth keychain integration, identity provider implementations, Git provider webhook signature verification, signature verification, hash-chain audit, freeze-window enforcement.

- **Managed-runtime integration test**: spawn the MCP server with `PODIUM_IDENTITY_PROVIDER=injected-session-token`, supply a stub signed JWT, exercise the meta-tool round-trip against a real registry, verify identity flows through and the layer composition resolves correctly for the caller's identity; verify rejection on unsigned token (`auth.untrusted_runtime`).

- **Developer-host integration test**: spawn the MCP server with `PODIUM_IDENTITY_PROVIDER=oauth-device-code` and `PODIUM_OVERLAY_PATH=${WORKSPACE}/.podium/overlay/`, complete the device-code flow via MCP elicitation, exercise the meta-tool round-trip, verify the workspace local overlay overrides registry-side artifacts and that hashes are exposed in `load_domain`.

- **Local search test**: `search_artifacts` returns workspace-local-overlay artifacts merged with registry results via RRF; removing the local file removes the artifact from search.

- **Workspace local overlay precedence test**: confirm the workspace local overlay overrides every registry-side layer for a synthetic conflicting artifact, and that removing the overlay file restores the registry-side artifact.

- **Domain composition tests**: `DOMAIN.md` `include:` patterns surface matching artifacts; recursive `**` and brace `{a,b}` patterns resolve correctly; `exclude:` removes paths; `unlisted: true` removes a folder and its subtree from `load_domain` enumeration; `DOMAIN.md` from multiple layers merges per §4.5.4; remote-vs-local glob resolution asymmetry is correct.

- **Cross-layer import tests**: a `DOMAIN.md` ingested in one layer imports an artifact ingested in another; a caller who can see both layers sees the imported artifact; a caller who can see only the destination layer sees nothing for that import; imports that don't currently resolve produce an ingest-time warning, not an error.

- **Materialization test**: exercise `load_artifact` against artifacts with diverse bundled file types (Python script, Jinja template, JSON schema, binary blob, external resource); verify atomic write semantics; verify partial-download recovery; verify presigned URL refresh on expiry.

- **`podium sync` lock-file test**: `podium sync` in an empty target writes `.podium/sync.lock` with the resolved profile, scope, and artifact list; re-running `podium sync` is idempotent (no spurious writes); the target dir auto-creates if missing; `--dry-run` prints the resolved set and writes nothing. Two `podium sync` invocations against different targets each maintain independent lock files.

- **Override + watch test**: `podium sync --watch` running against a target keeps `.podium/sync.lock`'s `toggles:` populated across registry change events. `podium sync override --add <id>` materializes the artifact and adds an entry to `toggles.add`; `podium sync override --remove <id>` removes it from disk and adds to `toggles.remove`. Manual `podium sync` (no `--watch`) clears `toggles:`. `podium sync override --reset` is equivalent to manual `podium sync`.

- **Save-as test**: after `podium sync override --add <id-a> --remove <id-b>`, `podium sync save-as --profile <name>` renders a profile in `.podium/sync.yaml` whose `include` / `exclude` reproduce the toggled state; the lock file's `toggles:` is cleared and the new profile becomes the active one. `--update` overwrites an existing profile; `--dry-run` prints the YAML diff and writes nothing.

- **Profile edit test**: `podium profile edit <name> --add-include <pattern>` rewrites `.podium/sync.yaml` preserving formatting and comments around untouched keys. The target directory and lock file are untouched; a subsequent `podium sync` picks up the change.

- **Signing test**: artifact signed at ingest; signature verified on materialization; tampered content rejected with `materialize.signature_invalid`; `podium verify <id>` matches.

- **Versioning and ingest tests**: pinned `<id>@<semver>` resolves exactly; `<id>@<semver>.x` resolves to highest matching; `<id>` resolves to `latest`; `session_id`-tagged calls return consistent `latest` resolution within the session; same-version-different-content ingests return `ingest.immutable_violation`; `extends:` parent version pinned at child ingest time; force-push on a `git`-source layer with the default tolerant policy preserves the previously-ingested bytes and emits `layer.history_rewritten`.

- **Harness adapter conformance suite**: for each built-in adapter, load a canonical fixture, produce harness-native output, install into a fresh harness instance, verify the harness can spawn an agent that uses the materialized artifact end-to-end. Includes negative tests for adapter sandbox contract (no network, no out-of-destination writes, no subprocess).

- **Adapter switching test**: the same MCP server binary, started with each `PODIUM_HARNESS` value, passes the conformance suite without recompilation. Per-call `harness:` overrides materialize a single artifact in a different format than the server's default.

- **Identity provider switching test**: the same MCP server binary, started with each identity provider, passes both integration tests above without recompilation.

- **Visibility tests**: a layer with `public: true` is visible to unauthenticated callers; `organization: true` is visible to any authenticated user in the org and no one else; `groups: [...]` matches OIDC group claims; `users: [...]` matches the listed identifiers; multiple visibility fields combine as a union; user-defined layers are visible only to the registrant; the user-defined-layer cap is enforced; `mcp-server` artifacts are filtered from MCP-bridge results; admins can override visibility for diagnostic purposes (and that override is audited).

- **Layer lifecycle tests**: `podium layer register` returns a webhook URL and HMAC secret; an ingest webhook with an invalid signature is rejected; a manual `podium layer reingest` succeeds and is idempotent; `podium layer reorder` re-sequences user-defined layers; `podium layer unregister` removes the layer immediately and the artifacts disappear from the caller's effective view; freeze windows block ingest; break-glass requires dual-signoff and justification.

- **Ingest workflow test**: an artifact merged into a tracked Git ref is ingested via webhook; manifest body and bundled resources are stored at the resolved content_hash; the artifact appears in `search_artifacts` for downstream callers; `artifact.published` and `layer.ingested` events are written to audit.

- **Failure-mode tests**: registry offline (cache serves; explicit "offline" status on miss), workspace overlay path missing (skip with warning), token expired under each identity provider, materialization destination unwritable, MCP protocol version mismatch, untrusted runtime, signature failure, runtime requirement unsatisfiable, source unreachable during ingest, webhook signature invalid.

- **Security tests**: a caller without matching visibility on a layer sees nothing from that layer; the MCP server requires OAuth-attested identity to reach the registry; redaction directives propagate to the registry audit stream and the local audit log; tokens stay in the OS keychain (or in the runtime-managed location for injected tokens); query text PII is scrubbed before audit; audit hash chain detects gaps; sandbox profile honored or refused per host capability.

- **Performance tests**: 1K QPS sustained for `search_artifacts`; 100 ingests/min; `load_artifact` p99 < SLO targets in §7.1; cold-cache vs warm-cache materialization budgets.

- **Soak tests**: 24h continuous load with mixed workload; no memory growth, no descriptor leaks, audit log integrity preserved across restarts.

- **Chaos tests**: Postgres failover during load, object-storage stalls, network partitions between MCP server and registry, IdP outage during refresh, full-disk on registry node.

- **Example artifact registry**: multi-domain demo with diverse types (skill, agent, context, prompt, mcp-server), diverse bundled file types, multiple layers exercising every visibility mode (`public`, `organization`, `groups`, `users`), at least one user-defined layer, signed artifacts at multiple sensitivities.

---

## 12. Operational Risks and Mitigations

| Risk                                                          | Mitigation                                                                                                                                                                                                                                                                                               |
| ------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Catalog grows too large for `load_domain` to be useful        | Two-level hierarchy default; directory layout drives subdomain structure (§4.2, §4.5); domain owners curate cross-cutting views via `DOMAIN.md include:`; learn-from-usage reranking surfaces signal-based ordering.                                                                                     |
| Prompt injection via artifact manifests                       | Content provenance markers (§4.4.2) enable differential trust; adapters propagate to harness-native trust regions where supported. Authoring rights at the Git provider gate who can ingest a manifest into a layer; the `sensitivity:` field surfaces classification metadata for downstream filtering. |
| Bundled-script supply chain                                   | SBOM at ingest; signature verification on materialization (§4.7.9); sandbox profile (§4.4.1); secret scanning + static analysis in pre-merge CI run by the source repo.                                                                                                                                  |
| Registry latency on every meta-tool call                      | HTTP/2 keep-alive between MCP server and registry; ETag caching of immutable artifact versions; manifest body inline; content-addressed disk cache shared across workspaces; explicit p99 budgets (§7.1).                                                                                                |
| Manifest description quality                                  | Ingest-time lint flags thin descriptions and clusters of artifacts with colliding summaries. Learn-from-usage reranking surfaces underperforming descriptions.                                                                                                                                           |
| Workspace local overlay tampering                             | The workspace overlay is intended for the developer's own iteration. Hosts that need tamper-evident behavior pin to registry-side versions and leave `PODIUM_OVERLAY_PATH` unset.                                                                                                                        |
| Registry as a single point of failure for hosts               | The cache and `offline-first` mode let cached artifacts continue to work during transient outages. Fresh `load_domain` / `search_artifacts` returns an explicit "offline" status that hosts can surface.                                                                                                 |
| Type system extensibility / per-type lint rule drift          | Type definitions are SPI plugins compiled into the registry binary; deployments pin a registry version.                                                                                                                                                                                                  |
| Visibility misconfiguration                                   | Layer config is auditable and version-controlled; admin actions are audited; the registry refuses to start with structurally invalid layer config; `podium admin show-effective <user>` surfaces the effective view for any identity.                                                                    |
| Identity provider misconfiguration                            | The MCP server validates its identity-provider configuration at startup and refuses to start with an obviously broken combination. Each provider documents the env vars it requires. Untrusted runtimes rejected at the registry.                                                                        |
| Bundled resource bloat                                        | Per-package and per-file size lints at ingest time; soft cap is configurable; large data uses `external_resources:` (§4.3) instead of inline bundling.                                                                                                                                                   |
| Recursive globs in `DOMAIN.md` are expensive                  | Glob expansion is cached server-side per artifact-version snapshot; cache invalidation is keyed on ingest events. Lint warns on overly broad recursive globs.                                                                                                                                            |
| `DOMAIN.md` imports go stale silently                         | Ingest-time lint warns on imports that don't currently resolve in any visible view. Learn-from-usage signal surfaces domains whose imports return empty results frequently.                                                                                                                              |
| `unlisted: true` accidentally hides artifacts                 | The flag is opt-in (default `false`); ingest-time lint flags newly-set `unlisted: true` for review. `search_artifacts` continues to surface unlisted artifacts unless `search_visibility: direct-only`.                                                                                                  |
| Harness adapter drift                                         | Adapters are versioned with the MCP server binary; profiles can pin a minimum version. Conformance suite runs against every adapter on every release. Authors who hit drift can fall back to `harness: none`.                                                                                            |
| Canonical artifact uses a feature an adapter cannot translate | Capability matrix (§6.7.1); ingest-time lint surfaces mismatches; `target_harnesses:` opt-out; adapter returns structured error from `load_artifact`.                                                                                                                                                    |
| Adapter sprawl across many harnesses                          | Adapters carry no agent or registry logic; mechanical translators with a shared core. Conformance suite gates merges. Sandbox contract enforced.                                                                                                                                                         |
| Vulnerability in a bundled dependency                         | SBOM at ingest; CVE feed ingested by registry; affected artifacts surfaced via `podium vuln list`; owners notified through configured channels.                                                                                                                                                          |
| Token leakage in `injected-session-token`                     | Runtime owns env-var/file lifecycle; ≤15 min token TTLs recommended; `PODIUM_SESSION_TOKEN_FILE` over env var when possible; runtime trust model rejects unsigned tokens.                                                                                                                                |
| Webhook secret compromise                                     | Per-layer HMAC secret rotated via `podium layer update`; webhook signature verified on every delivery; failed verifications log `ingest.webhook_invalid` and never reach the content store.                                                                                                              |
| Audit tampering                                               | Hash-chained audit (§8.6); periodic transparency-log anchoring recommended; SIEM mirroring as operational backstop.                                                                                                                                                                                      |

---

## 13. Deployment

### 13.1 Reference Topology

- **Stateless front-end:** 3+ replicas behind a load balancer (HTTP).
- **Postgres:** managed (RDS, Cloud SQL, Aurora) or self-run; primary + read replicas. Holds manifest metadata, layer config, admin grants, and audit; also holds embeddings when the default vector backend (pgvector) is in use.
- **Vector backend:** `pgvector` by default — collocated in the Postgres deployment, no separate service to run. The default binary also ships built-ins for `pinecone`, `weaviate-cloud`, and `qdrant-cloud`, selectable via `PODIUM_VECTOR_BACKEND` (each takes its own endpoint + API key env vars). Custom backends register through the `RegistrySearchProvider` SPI (§9.1, §9.2).
- **Embedding provider:** `openai` by default in standard deployments — text projection from manifest frontmatter (§4.7 _Embedding generation_) is sent to OpenAI's embeddings API. The default binary also ships `voyage`, `cohere`, `ollama`, and `embedded-onnx`, selectable via `PODIUM_EMBEDDING_PROVIDER`. Optional when the configured vector backend self-embeds (Pinecone Integrated Inference, Weaviate Cloud vectorizer, Qdrant Cloud Inference).
- **Object storage:** S3-compatible (S3, GCS, MinIO, R2).
- **Helm chart** ships with the registry; bare-metal deployment guide alongside.

For non-prod or standalone use, see §13.10.

#### 13.1.1 Evaluation Deployment (Docker Compose)

For team evaluation, smoke-testing, and local integration testing — anything that wants the standard topology's components without the standalone single-binary shortcut — the repo ships a `docker-compose.yml` that brings up the full stack with one command:

```bash
docker compose up -d
podium init --global --registry http://localhost:8080
podium login    # device-code flow against the bundled Dex IdP
```

The compose file includes:

- **`registry`** — the registry binary, configured against the local services below.
- **`postgres`** — `pgvector/pgvector:pg16` for metadata + embeddings.
- **`minio`** — S3-compatible object storage (path-style URLs, `MINIO_ROOT_USER` / `MINIO_ROOT_PASSWORD` for auth).
- **`dex`** — OIDC IdP for the OAuth device-code flow.
- **`bootstrap`** — one-shot container that creates the MinIO bucket, registers the registry as an OIDC client with Dex, creates the first tenant and admin user (configurable via env vars), then exits.

**Not production-grade.** Single-replica services, default credentials, local volumes — the compose stack is _standard-topology in shape_ so consumers exercise the same code paths as a real deployment, but it is intended only for evaluation pilots, CI integration tests, and adapter / SDK development. For genuine non-prod or solo use, prefer §13.10's standalone mode (one binary instead of four containers).

### 13.2 Runbook

Coverage for: Postgres failover, object-storage outage, IdP outage, full-disk on registry node, audit-stream backpressure, runaway search QPS, signature verification failure storm. Each scenario gets detection signals, impact, and mitigation steps; full runbook ships with the Helm chart.

#### 13.2.1 Read-Only Mode

When the Postgres primary becomes unreachable but a read replica is up, the registry falls back to **read-only mode**: read endpoints (`load_domain`, `search_artifacts`, `load_artifact`, `load_artifacts`) continue to serve from the replica; write endpoints (ingest webhooks, layer admin operations, freeze toggles, admin grants, `podium login`-driven token issuance against the local IdP-mediated session table) are rejected with the structured error `registry.read_only`.

A health-state machine drives the transition. The registry probes the primary every 5 s and flips to read-only after three consecutive failures (tunable via `PODIUM_READONLY_PROBE_INTERVAL` and `PODIUM_READONLY_PROBE_FAILURES`). It flips back automatically after three consecutive probe successes once the primary is reachable again.

Read responses in read-only mode carry two additional headers:

- `X-Podium-Read-Only: true`
- `X-Podium-Read-Only-Lag-Seconds: <n>` — observed replication lag at response time. Clients that need strict freshness can retry once the registry leaves read-only mode (or surface the staleness to a human reviewer via the existing offline/staleness affordance, §7.4).

Audit events for state transitions (`registry.read_only_entered`, `registry.read_only_exited`) are logged like any other admin action and carry the same hash-chain integrity guarantees as ingest and admin events. Ingest events that would have fired during the read-only window are queued by the Git provider's webhook retry policy and replayed on exit; webhooks from receivers that don't retry leave their corresponding ingests pending until the next manual `podium layer reingest`.

The MCP server, SDKs, and `podium sync` propagate the read-only signal: the MCP `health` tool reports `mode: read_only`, SDKs raise `RegistryReadOnly` on attempted writes, and `podium sync` continues to materialize against the cached effective view (the read path is unaffected).

#### 13.2.2 Public Mode

A misconfigured public-mode deployment is the most common security-relevant operational anomaly because the registry serves correctly — it just serves to everyone. The runbook entry exists to make it easy to detect and recover from.

**Detection.** `/healthz` returns `mode: public`. Audit events for read calls show `caller.identity: "system:public"` and the flag `caller.public_mode: true`. The registry's startup banner shows the public-mode warning. Operators investigating a deployment can confirm with `podium status`, which surfaces the same flag.

**Impact.** Authentication is skipped; visibility is bypassed (§4.6). Every artifact is reachable to every caller that can connect to the registry's bind address. Ingest of `sensitivity: medium` and `sensitivity: high` artifacts is rejected; existing artifacts at those levels (ingested before public mode was enabled) continue to be served.

**Mitigation.**

1. Confirm public mode was the intended deployment posture. If it was, no action needed — the audit log already records the intent.
2. If public mode was _not_ intended (a misconfigured environment variable, copy-pasted CLI flag, or accidental container image tag), stop the registry, remove `--public-mode` / unset `PODIUM_PUBLIC_MODE`, restart. The registry refuses mid-run flips, so a restart is mandatory.
3. If public mode was running on an internet-exposed registry (which the safety check should have prevented unless `--allow-public-bind` was set), treat as a security incident: rotate any signing keys that were in scope, audit the access log for unfamiliar IPs, and proceed per the org's incident-response procedure.

**Prevention.** Container-image and Helm-chart consumers should set `PODIUM_NO_AUTOSTANDALONE=1` and use `--strict` to refuse anything but explicitly-configured deployments — public mode requires an explicit flag, so a strict-only deployment cannot accidentally land in it. Production CI templates should fail-fast on the presence of `PODIUM_PUBLIC_MODE` in environment lists.

### 13.3 Backup and Restore

- Postgres: logical + physical backups; point-in-time recovery.
- Object storage: cross-region replication or snapshots.
- Consistent restore via PITR + object-storage version history.
- Default RPO 1h / RTO 4h.

### 13.4 Migrations

Schema migrations bundled in the registry binary; expand-contract pattern for online migrations. Type-system migrations versioned alongside the binary.

### 13.5 Multi-Region

A deployment is single-region. Cross-region read replicas via Postgres logical replication and object-storage replication; writes route to the primary region.

### 13.6 Sizing

Baseline: 10K artifacts, 100 QPS, 1 GB Postgres, 500 GB object storage handles a typical mid-sized org on a 3-replica deployment + db.m5.large equivalent.

Scale guidance:

- 100K artifacts: pgvector scale; consider sharding embeddings.
- 1K QPS: scale front-end replicas; CDN in front of object storage.
- 10K QPS: review search query patterns; consider dedicated Elasticsearch for BM25.

### 13.7 CDN

Presigned URLs are CDN-friendly. Recommend CloudFront / Fastly / Cloudflare in front of object storage for hot artifacts. Cache headers safe because content_hash keys are immutable.

### 13.8 Observability

- **Metrics.** Prometheus endpoint on registry and MCP server. Histograms for latency; counters for cache hit rate, error rate, visibility-denial rate, ingest success/failure rate; gauges for queue depths.
- **Tracing.** OpenTelemetry trace export. W3C Trace Context propagation across all calls. One root span per `load_domain` / `search_artifacts` / `load_artifact`; child spans for registry round-trip, object-storage fetch, adapter translation, materialization.
- **Reference Grafana dashboard** ships with the registry.

### 13.9 Health and Readiness

- Registry: `/healthz` (liveness) and `/readyz` (readiness — Postgres + object-storage reachable).
- `/readyz` reports one of `mode: ready | read_only | not_ready`. `read_only` is healthy from a load-balancer perspective (the registry should stay in rotation to serve reads) but signals upstream tooling that writes are being refused. Response body includes observed replication lag in seconds. See §13.2.1 for the state machine and the corresponding response headers.
- MCP server: `health` MCP tool returning registry connectivity + observed registry mode (`ready` / `read_only` / unreachable) + cache size + last successful call timestamp.

### 13.10 Standalone Deployment

`podium serve --standalone` collapses the full stack into a single binary with no external dependencies. It targets local development, individual contributors, and small-team installations where running Postgres + object storage + an IdP is overkill.

For a lighter shape with no daemon — just the CLI reading the artifact directory directly — see §13.11 (Filesystem Registry).

**Zero-flag default.** Running `podium serve` with no flags is equivalent to `podium serve --standalone` when no server config is found at `~/.podium/registry.yaml` and no `PODIUM_*` server-side environment variables are set. The server emits a clear stderr line on startup ("No config found at `~/.podium/registry.yaml` — starting in standalone mode at `http://127.0.0.1:8080`. Run `podium serve --strict` to require explicit setup."), creates the standalone defaults (`~/.podium/registry.yaml`, `~/.podium/sync.yaml`, `~/podium-artifacts/`) on first run, and proceeds to serve. This collapses the five-minute install path into a single command — no `podium init` step required.

`podium serve --strict` retains the prior behavior of refusing to start without explicit configuration. Setting `PODIUM_NO_AUTOSTANDALONE=1` in the environment has the same effect — useful in CI and image-building contexts where a missing config should always be a hard error rather than an auto-bootstrap. Auto-bootstrap is also suppressed when `--config <path>` is passed and the file does not exist (the user explicitly named a config; not finding it is an error, not a cue to invent one).

```bash
# Zero-flag — auto-enters standalone mode if no config exists at ~/.podium/registry.yaml
podium serve

# Explicit standalone with custom paths
podium serve --standalone \
  --layer-path /var/podium/artifacts \
  --bind 127.0.0.1:8080

# Refuse to start without explicit config (CI, image builds)
podium serve --strict
```

**What changes from the standard topology:**

| Concern                 | Standard                                        | Standalone                                                                                                                                                                                                                                                                                     |
| ----------------------- | ----------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Metadata store          | Postgres                                        | Embedded SQLite (`~/.podium/standalone/podium.db`)                                                                                                                                                                                                                                             |
| Vector store            | pgvector                                        | `sqlite-vec` extension loaded into the same SQLite file                                                                                                                                                                                                                                        |
| Embedding provider      | `openai` (default)                              | `embedded-onnx` — bundled BGE-small ONNX model, in-process, no external service                                                                                                                                                                                                                |
| Object storage          | S3-compatible                                   | Filesystem (`~/.podium/standalone/objects/`)                                                                                                                                                                                                                                                   |
| Identity provider       | OIDC IdP                                        | None — no auth; `127.0.0.1`-only HTTP by default                                                                                                                                                                                                                                               |
| Layers                  | Configured admin layers + user-defined layers   | One default `local`-source layer rooted at `--layer-path`; additional `local` and `git` layers can be registered via `podium layer register`                                                                                                                                                   |
| Git provider / webhooks | Required for `git`-source layers                | `git` source layers are supported; webhooks are optional. Without a webhook (typical for a developer machine without a public ingress), `podium layer reingest <id>` pulls the current state on demand. A local watcher (`podium layer watch <id>`) can poll a configured interval if desired. |
| Signing                 | Sigstore-keyless or registry-managed key        | Disabled by default; opt in via `--sign registry-key`                                                                                                                                                                                                                                          |
| Content cache           | Cross-workspace disk cache (`~/.podium/cache/`) | Disabled — the registry is local, the cache adds nothing                                                                                                                                                                                                                                       |
| Audit                   | Per-tenant Postgres table                       | Same SQLite file (audit table)                                                                                                                                                                                                                                                                 |
| Helm chart / Kubernetes | Required for production deployments             | Not used                                                                                                                                                                                                                                                                                       |

**Hybrid search.** Standalone runs the same BM25 + vector RRF retriever as the standard registry. Vectors live in `sqlite-vec`; embeddings come from the bundled `embedded-onnx` provider — both run in-process, so the binary works offline and air-gapped with no external dependency. Operators who want a remote model instead can switch via `PODIUM_EMBEDDING_PROVIDER=openai|voyage|cohere|ollama` (`ollama` is the obvious choice for self-hosted local models). `--no-embeddings` falls back to BM25-only.

**Upgrade path.** A standalone deployment migrates to standard via `podium admin migrate-to-standard --postgres <dsn> --object-store <url>` (covered in §13.4). Layer config, admin grants, and audit history are preserved; embeddings are re-computed against the target vector backend on first ingest.

**Web UI.** When `podium serve` (standalone or standard) is started with `--web-ui` (or `PODIUM_WEB_UI=true`), the same process exposes a single-page web UI at `http://<bind>/ui/`. The UI is a static SPA bundled into the binary; it talks to the registry's HTTP API as any other consumer would. What it surfaces:

- **Domain browser** — hierarchical navigation matching `load_domain`'s structure.
- **Search** — text input that calls `search_artifacts` with the same `type` / `scope` / `tags` filters as the SDK and CLI.
- **Artifact viewer** — manifest body rendered as markdown, frontmatter as a property table, links to extending or dependent artifacts.
- **Layer panel** — list registered layers with their source, visibility, and `last_ingested_at`. Admins can register, reingest, and unregister layers from the UI; users can manage their own user-defined layers (cap per §7.3.1). The UI is a thin client over the same `podium layer …` HTTP endpoints.

Authentication: in standalone deployments without an identity provider, the UI is open on the bind address (default `127.0.0.1` — not network-exposed). In standard deployments the UI uses the same OAuth device-code flow as the CLI, with the verification URL handoff handled in-browser.

Behind a flag: opt-in via `--web-ui` so headless deployments (CI runners, managed runtimes) don't pay the binary-size or attack-surface cost when they don't need it. The binary refuses to bind the UI to a non-loopback address unless `--web-ui-allow-public-bind` is also passed _and_ an identity provider is configured — preventing accidental exposure of an unauthenticated UI.

The UI is the recommended consumption path for non-developer users (analysts, prompt authors, reviewers) who want to browse the catalog without installing the SDK or learning the CLI.

**Sensible defaults for permissive deployments.** Standalone deployments shift several defaults toward low-friction rather than secure-by-default — appropriate for the solo and small-team contexts standalone targets:

- **Layer visibility.** New layers registered via `podium layer register` default to `visibility: public` (instead of `users: [<registrant>]` as in standard mode for user-defined layers). Override with `PODIUM_DEFAULT_LAYER_VISIBILITY=users` for multi-user standalone deployments that want the standard behavior.
- **Signature verification.** `PODIUM_VERIFY_SIGNATURES` defaults to `never` (instead of `medium-and-above`). Authors who want enforcement set it explicitly to `medium-and-above` or `always`.
- **Sandbox profile.** `sandbox_profile:` is informational in standalone — hosts honor it as in standard mode, but the registry does not refuse to ingest artifacts whose profiles can't be enforced locally. Override with `PODIUM_ENFORCE_SANDBOX_PROFILE=true` in multi-user setups.
- **Sensitivity.** Artifacts without an explicit `sensitivity:` field default to `low`. The lint check that flags missing sensitivity is downgraded from a warning to a hint.

Any of these defaults can be flipped to standard-mode behavior via the named env var without otherwise changing the deployment shape — the same single binary continues to serve.

**Public mode (`--public-mode` / `PODIUM_PUBLIC_MODE`).** A registry-level switch that bypasses both authentication and the visibility model in one step. Replaces "progressively disable each governance feature" with a single explicit decision — appropriate for solo demos, evaluation pilots without team context, and intentionally open internal-knowledge-base deployments.

```bash
# Standalone, fully open
podium serve --public-mode --layer-path ~/podium-artifacts

# Or via env var
PODIUM_PUBLIC_MODE=true podium serve
```

Startup banner:

```
⚠  PUBLIC MODE — all artifacts visible to all callers without authentication.
   Bound to 127.0.0.1 by default; pass --allow-public-bind to bind a non-loopback address.
```

What public mode does:

- **Skips OAuth.** No `podium login`, no JWT verification, no OIDC config required. Callers reach the registry without credentials.
- **Bypasses visibility.** The visibility evaluator (§4.6) short-circuits to `true` for every layer and every caller. Layer `visibility:` declarations are still accepted into config (so artifacts remain portable to non-public deployments) but ignored at request time.
- **Records `system:public`** in audit (§8.1). Source IP and any `X-Forwarded-User` header from an upstream proxy are preserved.
- **Leaves ingest unchanged.** `content_hash` immutability, lint, hash-chained audit, and signing (when configured) all behave normally.

Safety constraints:

- **Mutually exclusive with an identity provider.** Setting `PODIUM_PUBLIC_MODE` and `PODIUM_IDENTITY_PROVIDER` (or the equivalent config keys) at the same time fails at startup with `config.public_mode_with_idp`. Public mode is the absence of authentication, not an alternative provider — pick one.
- **Loopback bind by default.** Public mode binds to `127.0.0.1` unless `--allow-public-bind` is _also_ passed. The escape hatch exists for deployments behind an authenticated reverse proxy that enforces who can reach the registry; without the proxy, the operator is taking explicit responsibility for the security model.
- **Sensitivity ceiling.** Ingest of `sensitivity: medium` or `sensitivity: high` artifacts is rejected with `ingest.public_mode_rejects_sensitive`. Public mode is for low-stakes content only. Artifacts already at those levels (ingested before public mode was enabled) continue to be served — public mode does not retroactively delete content.
- **One-way for the deployment's lifetime.** Toggling public mode requires a config change _and_ a registry restart. The registry refuses to flip the mode mid-run — prevents an admin accidentally toggling away protections through a config-reload signal.
- **Loud at every checkpoint.** The mode is surfaced in `/healthz` (`mode: public`), in the MCP `health` tool, in `podium status`, and as a flag (`caller.public_mode: true`) on every audit event so downstream tooling can detect it without inspecting startup config.

When to use public mode vs sensible-defaults standalone:

- **Use sensible defaults** when you're a single user or small team using standalone for productivity. The visibility model is already trivially permissive; no extra ceremony.
- **Use public mode** when (a) the deployment is intentionally open beyond a single user — e.g., a demo registry, an internal-public catalog, an evaluation pilot — and (b) you want the audit log to record that anonymous-public access was the deployment's intent, not a misconfiguration.

Migration to a governed deployment goes through `podium admin migrate-to-standard --postgres <dsn> --object-store <url>` (§13.4), followed by removing the `--public-mode` flag and reconfiguring layer visibility. Same migration path standalone uses today.

**Out of scope for standalone.** Multi-tenancy, freeze windows, SCIM, SBOM/CVE pipeline, transparency-log anchoring, outbound webhooks. These are present in the binary but inert without the supporting infrastructure (an IdP for SCIM, a CVE feed for vulnerability tracking, etc.). They can be enabled individually when their dependencies are available.

**Client setup.** Clients (CLI, MCP server, SDK) don't read `registry.yaml` — that's server-side config. The registry value clients use to reach the server is configured separately on the client side, via `sync.yaml`'s `defaults.registry`, `PODIUM_REGISTRY`, or an SDK constructor param (§7.5.2 covers the lookup order). `podium serve` zero-flag writes both files in one step on first run: `~/.podium/registry.yaml` for the server (`bind: 127.0.0.1:8080`, store/vector defaults) and `~/.podium/sync.yaml` for the client (`defaults.registry: http://127.0.0.1:8080`). For client-only setup (e.g., when the server runs elsewhere), use `podium init --global --registry <url>` (§7.7).

### 13.11 Filesystem Registry

A filesystem registry is a directory tree treated as the registry. `podium sync` reads it directly — applying layer composition (§4.6) and materializing through the harness adapter — with no server intermediary. No daemon, no port, no PID. Only `podium sync` works in this shape; the MCP server, SDKs, and read CLI require an HTTP server.

The audience is solo developers, small teams committing the catalog to git, CI runs, and restricted environments where running a server isn't possible. The dispatch logic that routes a `defaults.registry` value to either server or filesystem is in §7.5.2.

#### 13.11.1 Directory Layout

A filesystem registry rooted at `<registry-path>` is a directory of layer directories:

```
<registry-path>/
├── team-shared/                # one layer
│   ├── DOMAIN.md
│   ├── finance/
│   │   └── close-reporting/
│   │       └── run-variance-analysis/
│   │           └── ARTIFACT.md
│   └── platform/
│       └── …
├── personal/                   # another layer (purely a name choice)
│   └── …
└── .layer-order                # optional; controls layer ordering
```

Each subdirectory of `<registry-path>` is treated as a `local`-source layer (§4.6). Layer IDs default to the subdirectory name; layer order is alphabetical by name. An optional `<registry-path>/.layer-order` file overrides the order — one layer ID per line, in precedence order from lowest to highest:

```
# <registry-path>/.layer-order
team-shared
personal
```

The workspace local overlay (`<workspace>/.podium/overlay/`, §6.4) sits on top of the filesystem-registry layers, exactly as in server source.

#### 13.11.2 Configuration

The client picks filesystem source when `defaults.registry` resolves to a path:

```yaml
# <workspace>/.podium/sync.yaml
defaults:
  registry: ./.podium/registry/ # relative paths are resolved against the workspace
  harness: claude-code
  target: .claude/
```

Absolute paths work too (`registry: /opt/podium-artifacts/`). There is no implicit workspace fallback — if `defaults.registry` is unset across all scopes, the client errors with `config.no_registry` and points the user at `podium init`. Behavior never depends on whether `<workspace>/.podium/registry/` happens to exist.

To override an inherited URL with a filesystem path, set `defaults.registry: ./.podium/registry/` explicitly at a higher-precedence scope (typically the project-shared file). Normal precedence applies.

#### 13.11.3 What's Available

What `podium sync` does in filesystem source:

- Layer composition (§4.6) across the registry's layer subdirectories plus the workspace overlay (§6.4).
- Materialization through the configured harness adapter.
- Lock-file write at `<target>/.podium/sync.lock`. `podium sync override` and `podium sync save-as` work the same way as in server source.

What's **not available** in filesystem source:

- The MCP server (§6) and progressive disclosure via meta-tools (§5).
- The language SDKs (§7.6).
- The read CLI (`podium search`, `podium domain show`, `podium artifact show`; §7.6.1) — SDK-backed.
- Outbound webhooks (§7.3.2).
- Identity-based visibility filtering. The visibility evaluator short-circuits to `true` for every layer.
- `podium login` (no auth to perform).

Features that require **specifically a remote server** (not just any server):

- Centralized audit independent of clones.
- OIDC identity-based visibility filtering.
- Multi-tenancy, SCIM, SBOM/CVE pipeline, transparency-log anchoring.

#### 13.11.4 Watch Mode

`podium sync --watch` against a filesystem source uses `fsnotify` to watch the registry path and the workspace overlay; when files change, it re-runs composition and materialization for the affected artifacts.

#### 13.11.5 Multi-User via Committed Registry

The registry directory is just files. Commit `<workspace>/.podium/registry/` (or whatever path the project chose) to git, and every developer who clones the project has the same catalog. Each developer runs `podium sync` independently against their local clone; the catalog is read-only from the client's perspective, and mutation goes through git PR + merge. No shared-state coordination, no conflicts.

Any number of developers can share a project this way without running a server. The catalog is the git history; ingest is `git pull`.

#### 13.11.6 Migrating to a Server

The filesystem source covers the small-team eager-only path. Migration to a server happens for two clusters of reasons:

**Migrate to any server (local `podium serve --standalone` or remote standard deployment):**

- Progressive disclosure required (agents call MCP meta-tools at runtime to load capabilities incrementally instead of materializing everything ahead of time).

**Migrate specifically to a remote server:**

- Centralized audit independent of clones.
- OIDC identity-based visibility filtering.

Migration is mechanical:

1. Run `podium serve --standalone --layer-path /path/to/.podium/registry/` (the same directory) on a chosen host. For remote, set up the standard topology (§13.1) and use `podium admin migrate-to-standard` (§13.4).
2. In each developer's `<workspace>/.podium/sync.yaml`, change `defaults.registry: ./.podium/registry/` to the server URL.
3. Done. Authoring loop unchanged; consumer paths gain MCP / SDK availability.

### 13.12 Backend Configuration Reference

This section covers **server-side** configuration — the registry process's storage backends, vector backend, embedding provider, and identity provider, configured in `registry.yaml` (default `/etc/podium/registry.yaml` for standard deployments and `~/.podium/registry.yaml` for standalone; override via `--config <path>`).

For **client-side** configuration (`sync.yaml`, `defaults.registry`, profiles, scope filters, etc.), see §7.5.2. Client and server configs are independent — clients don't read `registry.yaml`, servers don't read `sync.yaml`.

Backend selections and their per-backend config values can be set as environment variables, command-line flags, or entries in `registry.yaml`. **Server-side precedence: CLI flag > env var > config file.** All env vars below are also valid config-file keys (snake-cased under the relevant section); a complete YAML example follows the per-backend tables. (Client-side precedence is similar but adds project and project-local config files between env vars and the user-level file — see §7.5.2.)

The same values apply on the MCP server when it's configured to use `LocalSearchProvider` against an external backend (§6.4.1) — the workspace-side process reads the same env-var names.

The registry refuses to start when a backend is selected but its required values are missing, naming the missing keys in the error.

#### Metadata store

Selected via `PODIUM_REGISTRY_STORE` (`postgres` | `sqlite`).

| Var                   | Description                                  | Default                          |
| --------------------- | -------------------------------------------- | -------------------------------- |
| `PODIUM_POSTGRES_DSN` | Postgres connection string (when `postgres`) | — required                       |
| `PODIUM_SQLITE_PATH`  | SQLite file path (when `sqlite`)             | `~/.podium/standalone/podium.db` |

#### Object storage

Selected via `PODIUM_OBJECT_STORE` (`s3` | `filesystem`).

| Var                                                       | Description                                                            | Default                                      |
| --------------------------------------------------------- | ---------------------------------------------------------------------- | -------------------------------------------- |
| `PODIUM_S3_BUCKET`                                        | Bucket name (when `s3`)                                                | — required                                   |
| `PODIUM_S3_REGION`                                        | AWS / region for the bucket                                            | — required                                   |
| `PODIUM_S3_ENDPOINT`                                      | Override URL for S3-compatible services (MinIO, GCS, R2, Backblaze B2) | (none — uses AWS S3)                         |
| `PODIUM_S3_ACCESS_KEY_ID` / `PODIUM_S3_SECRET_ACCESS_KEY` | Static credentials                                                     | (use IAM role / instance profile when unset) |
| `PODIUM_S3_FORCE_PATH_STYLE`                              | `true` for MinIO and similar                                           | `false`                                      |
| `PODIUM_FILESYSTEM_ROOT`                                  | Root directory (when `filesystem`)                                     | `~/.podium/standalone/objects/`              |

#### Vector backend

Selected via `PODIUM_VECTOR_BACKEND` (`pgvector` | `sqlite-vec` | `pinecone` | `weaviate-cloud` | `qdrant-cloud`).

`pgvector` and `sqlite-vec` reuse the metadata-store connection — no additional config.

`pinecone`:

| Var                               | Description                                                                      | Default                                                   |
| --------------------------------- | -------------------------------------------------------------------------------- | --------------------------------------------------------- |
| `PODIUM_PINECONE_API_KEY`         | Pinecone API key                                                                 | — required                                                |
| `PODIUM_PINECONE_INDEX`           | Index name                                                                       | — required                                                |
| `PODIUM_PINECONE_HOST`            | Index host URL (Pinecone serverless)                                             | (auto-resolved from index name)                           |
| `PODIUM_PINECONE_NAMESPACE`       | Namespace prefix used per tenant                                                 | `default`                                                 |
| `PODIUM_PINECONE_INFERENCE_MODEL` | Hosted model name to enable Integrated Inference (e.g., `multilingual-e5-large`) | (unset → storage-only mode; `EmbeddingProvider` required) |

`weaviate-cloud`:

| Var                          | Description                                                                                          | Default                                                   |
| ---------------------------- | ---------------------------------------------------------------------------------------------------- | --------------------------------------------------------- |
| `PODIUM_WEAVIATE_URL`        | Cluster REST URL                                                                                     | — required                                                |
| `PODIUM_WEAVIATE_API_KEY`    | API key                                                                                              | — required                                                |
| `PODIUM_WEAVIATE_COLLECTION` | Collection name                                                                                      | — required                                                |
| `PODIUM_WEAVIATE_GRPC_URL`   | gRPC endpoint                                                                                        | (derived from REST URL)                                   |
| `PODIUM_WEAVIATE_VECTORIZER` | Vectorizer module name (e.g., `text2vec-openai`, `text2vec-weaviate`) — set to enable self-embedding | (unset → storage-only mode; `EmbeddingProvider` required) |

`qdrant-cloud`:

| Var                             | Description                                                      | Default                                                   |
| ------------------------------- | ---------------------------------------------------------------- | --------------------------------------------------------- |
| `PODIUM_QDRANT_URL`             | Cluster REST URL                                                 | — required                                                |
| `PODIUM_QDRANT_API_KEY`         | API key                                                          | — required                                                |
| `PODIUM_QDRANT_COLLECTION`      | Collection name                                                  | — required                                                |
| `PODIUM_QDRANT_GRPC_PORT`       | gRPC port                                                        | `6334`                                                    |
| `PODIUM_QDRANT_INFERENCE_MODEL` | Hosted Cloud Inference model name — set to enable self-embedding | (unset → storage-only mode; `EmbeddingProvider` required) |

#### Embedding provider

Selected via `PODIUM_EMBEDDING_PROVIDER` (`embedded-onnx` | `openai` | `voyage` | `cohere` | `ollama`). **Optional** when the configured vector backend self-embeds (any of the `*_INFERENCE_MODEL` / `*_VECTORIZER` env vars above is set); **required** otherwise.

`embedded-onnx`:

| Var                      | Description                | Default                       |
| ------------------------ | -------------------------- | ----------------------------- |
| `PODIUM_ONNX_MODEL_PATH` | Path to an ONNX model file | (bundled `bge-small-en-v1.5`) |
| `PODIUM_ONNX_DIMENSIONS` | Output vector dimensions   | `384`                         |
| `PODIUM_ONNX_POOL_SIZE`  | Concurrent inference slots | `runtime.NumCPU()`            |

`openai`:

| Var                      | Description                                         | Default                     |
| ------------------------ | --------------------------------------------------- | --------------------------- |
| `OPENAI_API_KEY`         | OpenAI API key                                      | — required                  |
| `PODIUM_OPENAI_MODEL`    | Model name                                          | `text-embedding-3-small`    |
| `PODIUM_OPENAI_BASE_URL` | API base URL (override for Azure OpenAI or proxies) | `https://api.openai.com/v1` |
| `PODIUM_OPENAI_ORG`      | OpenAI organization ID                              | (unset)                     |

`voyage`:

| Var                   | Description       | Default    |
| --------------------- | ----------------- | ---------- |
| `VOYAGE_API_KEY`      | Voyage AI API key | — required |
| `PODIUM_VOYAGE_MODEL` | Model name        | `voyage-3` |

`cohere`:

| Var                   | Description    | Default    |
| --------------------- | -------------- | ---------- |
| `COHERE_API_KEY`      | Cohere API key | — required |
| `PODIUM_COHERE_MODEL` | Model name     | `embed-v4` |

`ollama`:

| Var                   | Description     | Default                  |
| --------------------- | --------------- | ------------------------ |
| `PODIUM_OLLAMA_URL`   | Ollama endpoint | `http://localhost:11434` |
| `PODIUM_OLLAMA_MODEL` | Model name      | `nomic-embed-text`       |

#### Identity provider

Identity-provider selection and per-provider config are documented in §6.3 (`PODIUM_IDENTITY_PROVIDER`, `PODIUM_OAUTH_AUDIENCE`, `PODIUM_SESSION_TOKEN_*`, etc.). The same values apply on both the registry and the MCP server.

For server deployments that intentionally run without an identity provider, `PODIUM_PUBLIC_MODE=true` (or `--public-mode`) bypasses authentication and the visibility model entirely — see §13.10. Public mode is mutually exclusive with `PODIUM_IDENTITY_PROVIDER`; setting both fails at startup with `config.public_mode_with_idp`.

Filesystem-source registries (§13.11) have no identity provider by definition — there is no server process to authenticate against and no JWT to verify. `podium login` is a no-op when the resolved registry is a filesystem path; the visibility evaluator short-circuits to `true` for every layer.

#### Config file format

```yaml
# /etc/podium/registry.yaml (or ~/.podium/registry.yaml in standalone)
registry:
  endpoint: https://podium.acme.com
  bind: 0.0.0.0:8080

  store:
    type: postgres
    dsn: ${PODIUM_POSTGRES_DSN} # ${ENV_VAR} interpolation supported

  object_store:
    type: s3
    bucket: acme-podium
    region: us-east-1
    endpoint: ${PODIUM_S3_ENDPOINT} # optional — set for MinIO / R2 / GCS

  vector_backend:
    type: pinecone
    api_key: ${PINECONE_API_KEY}
    index: acme-prod
    namespace: ${PODIUM_TENANT_ID}
    inference_model: multilingual-e5-large # enables self-embedding

  # Optional: omitted because the vector backend above self-embeds.
  # embedding_provider:
  #   type: openai
  #   api_key: ${OPENAI_API_KEY}
  #   model: text-embedding-3-large

  identity_provider:
    type: oauth-device-code
    audience: https://podium.acme.com
    authorization_endpoint: https://acme.okta.com/oauth2/default
```

Env vars and CLI flags override file values. Secret values should use `${ENV_VAR}` interpolation rather than being committed in plaintext.

---

## 14. Common Scenarios

End-to-end walkthroughs for common ways Podium gets used. Each scenario links to the relevant detail sections; the steps below show what an operator or developer actually types.

### 14.1 Local registry folder, one-shot, single workspace

A developer keeps artifacts in a local folder and materializes a subset into one project for Claude Code. Filesystem-source registry (§13.11) — no daemon, single CLI invocation.

**One-time:**

1. Lay out artifacts in `~/podium-artifacts/` per §4.2 (each artifact a subdirectory containing `ARTIFACT.md`; subdirectories of `~/podium-artifacts/` are layers, §13.11.1).
2. `podium init --global --registry ~/podium-artifacts/` to write `~/.podium/sync.yaml` with `defaults.registry: ~/podium-artifacts/`. No server needed — the client reads the directory directly.

**Per project:**

3. `cd ~/projects/myapp/`.
4. Optional — `.podium/sync.yaml`:
   ```yaml
   profiles:
     myapp:
       include: ["finance/**", "shared/policies/*"]
       exclude: ["finance/**/legacy/**"]
   ```
5. `podium sync --harness claude-code --profile myapp`. Default target = CWD; lock file at `<cwd>/.podium/sync.lock`.

**When to graduate to a server:** if the developer wants `load_artifact` lazy-loaded by Claude Code (progressive disclosure via MCP), they migrate by running `podium serve --standalone --layer-path ~/podium-artifacts/` and updating `~/.podium/sync.yaml` to point at `http://127.0.0.1:8080`. Same artifact directory; just add a daemon. See §13.11.6.

**Changing the subset:** edit `sync.yaml` (or `podium profile edit myapp ...`), re-run `podium sync`. Ad-hoc adjustments: `podium sync override --add/--remove`.

### 14.2 Local registry folder, one-shot, multiple workspaces

Same setup. Each project workspace runs sync independently.

```bash
cd ~/projects/finance-app/    && podium sync --harness claude-code --profile finance
cd ~/projects/marketing-tool/ && podium sync --harness claude-code --profile marketing
```

Each workspace has its own `<workspace>/.podium/sync.lock`. Both read from the same artifact directory; with filesystem source, no shared cache or registry process is needed (the source files are the cache).

### 14.3 Local registry folder, MCP discovery, multiple workspaces

Same artifact directory, but now the developer wants progressive disclosure (the agent calls `load_domain` / `search_artifacts` / `load_artifact` at runtime, not pre-materialized). MCP requires a server, so this scenario graduates from filesystem source (§13.11) to standalone server (§13.10):

1. `podium serve --standalone --layer-path ~/podium-artifacts/` — starts the server against the same directory; auto-bootstraps `~/.podium/sync.yaml` with `defaults.registry: http://127.0.0.1:8080`.
2. Per project, add a Podium entry to the harness's MCP config (snippets per §6.11). The MCP server picks up the registry from `~/.podium/sync.yaml` (`defaults.registry`), so no extra env var is needed.
3. Optional `.podium/overlay/` per workspace for in-progress artifacts.

### 14.4 Local registry, custom harness via SDK, multiple workspaces

Each workspace runs an app built on `podium-py` (or `podium-ts`) plus an agent SDK like Claude Agent SDK. The SDK speaks HTTP, so this scenario uses a standalone server (graduated from §14.1's filesystem source — same artifact directory, just wrap it in `podium serve --standalone`).

```python
from podium import Client
from claude_agent_sdk import ...

client = Client.from_env()         # picks up registry URL from sync.yaml + overlay path
# ... custom discovery logic, search_artifacts → load_artifact → agent execution
```

### 14.5 Remote registry, one-shot, multiple workspaces

**Operator (centralized):**

1. Deploy the registry per §13.1 with chosen vector backend, embedding provider, OIDC IdP, S3.
2. Configure the tenant's layer list with Git-source layers and visibility rules (§4.6).
3. Set Git webhooks pointing at the ingest endpoint (§7.3.1).

**Per developer:**

4. `podium init --global --registry https://podium.acme.com`.
5. `podium login` — completes the device-code flow once, caches the token.
6. `cd <project>`, write `.podium/sync.yaml` with a profile, `podium sync --harness claude-code --profile <name>`.
7. Repeat per workspace; each has its own lock file.

### 14.6 Remote registry + workspace local overlay, one-shot, multiple workspaces

Operator setup as in §14.5. Per workspace:

1. Drop in-progress artifacts under `<workspace>/.podium/overlay/`.
2. `podium sync --harness claude-code --profile <name>`. The overlay path auto-resolves to `<CWD>/.podium/overlay/` per §6.4 — no env var needed.

### 14.7 Remote registry + local overlay, MCP, multiple workspaces

Operator setup as in §14.5. Per workspace:

1. Configure the harness's MCP server entry (§6.11) with `PODIUM_REGISTRY` and `PODIUM_HARNESS`. `PODIUM_OVERLAY_PATH` is optional — when unset, the MCP server resolves the overlay from MCP roots (§6.4).
2. First call triggers OAuth device-code via MCP elicitation. Token caches in the OS keychain.
3. Drop workspace-local artifacts under `.podium/overlay/`. The MCP server's fsnotify watcher picks up changes.

### 14.8 Remote registry + local overlay, custom harness via SDK, multiple workspaces

Operator setup as in §14.5. Per workspace:

```python
client = Client(
    registry="https://podium.acme.com",
    identity_provider="oauth-device-code",
    overlay_path="./.podium/overlay/",
)
client.login()   # device-code flow before any catalog calls
# ... use search_artifacts / load_artifact in your runtime
```

### 14.9 Enterprise multi-layer setup

**Operator:**

1. Deploy registry with full stack: Postgres + chosen vector backend, S3, OIDC IdP with SCIM push.
2. Configure layers — multiple Git repos with visibility per §4.6:
   ```yaml
   layers:
     - id: org-defaults
       source: { git: { repo: ..., ref: main, root: artifacts/ } }
       visibility: { organization: true }
     - id: team-finance
       source: { git: { repo: ..., ref: main } }
       visibility: { groups: [acme-finance] }
     - id: public-marketing
       source: { git: { repo: ..., ref: main } }
       visibility: { public: true }
   ```
3. Configure freeze windows, admin grants, signing (Sigstore-keyless or registry-managed).
4. `podium lint` runs as a required CI check on each layer's repo.

**Per author:** edit artifacts in the team's Git repo, open PR, merge. Webhook fires; registry ingests.

**Per consumer:** authenticates via OIDC, runs `podium sync` / MCP / SDK as in §14.5–14.8. Effective view composes admin layers (visibility-filtered) + user-defined layers + workspace local overlay.

**Personal user-defined layers:** `podium layer register --id my-experiments --repo git@github.com:joan/podium-experiments.git --ref main`. Capped at 3 per identity by default.

### 14.10 Standalone registry with a Git-source layer

A single-developer setup that mirrors a public Git repo (e.g., a community library) into a local standalone registry.

**One-time:**

1. `podium serve --standalone --layer-path ~/podium-artifacts` (single-binary server with the local layer for personal artifacts; auto-bootstraps `~/.podium/sync.yaml` pointing at the local server).
2. `podium serve --standalone`.
3. Register the Git layer:
   ```bash
   podium layer register --id community-skills \
     --repo https://github.com/podium-community/skills.git --ref main
   ```
4. The CLI prints the webhook URL it would expect; on a developer machine without a public ingress, ignore the webhook and pull manually instead:
   ```bash
   podium layer reingest community-skills
   # or, for periodic sync:
   podium layer watch community-skills --interval 1h
   ```

**Per project:** `podium sync` as in §14.1, scoping to the layers and paths the developer wants.

### 14.11 CI / build pipeline materialization

A build pipeline materializes a deterministic artifact set into a deploy artifact (e.g., a Docker image) without device-code interaction.

**Pipeline setup:**

1. CI obtains a runtime-issued JWT (per `injected-session-token`, §6.3.2). The runtime's signing key is registered with the registry one-time.
2. Pipeline step:
   ```bash
   export PODIUM_REGISTRY=https://podium.acme.com
   export PODIUM_IDENTITY_PROVIDER=injected-session-token
   export PODIUM_SESSION_TOKEN_FILE=/run/secrets/podium-token
   podium sync --harness claude-code --profile production --target ./build/.claude/
   ```
3. The lock file (`./build/.claude/.podium/sync.lock`) captures exactly which `(artifact_id, version, content_hash)` triples landed in the image — committed alongside the build for reproducibility.

`podium sync --dry-run --json` is useful in pre-flight to sanity-check what the build will include.

### 14.12 Air-gapped enterprise

Registry runs entirely on an internal network with no public ingress.

**Operator:**

1. Deploy registry per §13.1 inside the internal network. Identity via the org's internal OIDC IdP. Object storage on internal S3-compatible storage (MinIO or similar).
2. Layer Git repos hosted on internal Git server (GitLab/Gitea/internal GitHub Enterprise). Webhooks reach the registry over the internal network only.
3. Embedding provider: `embedded-onnx` (no external API calls) or `ollama` pointed at a local model server. Vector backend: pgvector (no external service).
4. Sigstore-keyless requires public OIDC infrastructure; air-gapped deployments use the registry-managed signing key path instead.

**Consumers:** internal endpoint only; OIDC flow stays inside the network. CVE feeds either disabled or mirrored from an internal vulnerability database.

### 14.13 Mixed-harness developer

A single developer uses Claude Code on one project and Cursor on another, sharing one OAuth identity and the same content cache.

1. `podium login --registry https://podium.acme.com` once. Token caches in the OS keychain.
2. Project A: Claude Code with the §6.11 Claude Code MCP snippet. Workspace overlay at `<project-a>/.podium/overlay/`.
3. Project B: Cursor with the §6.11 Cursor MCP snippet. Workspace overlay at `<project-b>/.podium/overlay/`.
4. Both share the same OS-keychain token, the same `~/.podium/cache/`, and (if so configured) the same user-wide audit log at `~/.podium/audit.log`.

### 14.14 Promote-to-shared workflow

A developer iterates on a new artifact in their workspace overlay, then promotes it to a shared layer.

1. Edit `<workspace>/.podium/overlay/finance/cashflow/forecast/ARTIFACT.md`. The MCP server (or sync, or SDK) sees it immediately.
2. Test the artifact end-to-end in the workspace.
3. When ready, `git mv` (or copy) the artifact into the team's Git layer repo, commit, open PR.
4. PR runs `podium lint` and any team-specific checks. Reviewers approve, merge.
5. Webhook fires; registry ingests.
6. Remove the now-redundant copy from `.podium/overlay/`.

`podium sync save-as` is the alternative path for capturing a curated set of overrides as a profile in `sync.yaml` rather than promoting individual artifacts to a shared layer.

### 14.15 Read-only viewer / auditor

A user with read-only visibility to several layers wants to browse the catalog without running sync or an MCP server.

```bash
export PODIUM_REGISTRY=https://podium.acme.com
podium login

podium domain show                                          # top-level map
podium domain show finance/close-reporting                  # drill in
podium search "variance analysis" --type skill --json
podium artifact show finance/close-reporting/run-variance-analysis --version 1.2.0
```

`podium sync --dry-run` provides the same view in materialization-shape (resolved profile + scope) without writing anything to disk.

---

## Glossary

- **Artifact** — a packaged authoring unit (skill, agent, prompt, context, MCP-server registration, or extension type). Distinct from "build artifact" or "ML artifact."
- **Canonical artifact ID** — the directory path under the registry root (e.g., `finance/ap/pay-invoice`). All references use this ID, optionally suffixed with `@<semver>` or `@sha256:<hash>`.
- **Domain** — a node in the catalog hierarchy. Distinct from DNS domain or DDD domain.
- **Effective view** — the composition of every layer (admin-defined, user-defined, and the workspace local overlay) visible to the caller's identity, in precedence order.
- **Harness** — the AI runtime hosting an agent (Claude Code, Cursor, Codex, etc.). Used interchangeably with "host" when the runtime context matters.
- **Host** — the MCP-speaking system that runs the Podium MCP server alongside its own runtime.
- **Layer** — a unit of composition with a single source (Git repo or local filesystem path) and a visibility declaration. Admin-defined, user-defined, or the workspace local overlay.
- **Workspace local overlay** — the workspace-scoped layer sourced from `${PODIUM_OVERLAY_PATH}` by the MCP server's `LocalOverlayProvider`. Highest precedence in the caller's effective view.
- **Manifest** — the `ARTIFACT.md` file specifically.
- **Materialization** — atomic write of a loaded artifact's content (manifest + bundled resources, after harness adapter translation) onto the host's filesystem.
- **MCP server (Podium MCP server)** — the in-process bridge binary the host runs.
- **Package** — the on-disk directory containing an artifact's `ARTIFACT.md` and bundled resources.
- **Registry** — the centralized service that ingests artifacts from layer sources and serves the catalog.
- **Visibility** — per-layer access declaration in the registry config (or, for user-defined layers, set by the registrar): `public`, `organization`, OIDC `groups`, or explicit `users`.
- **Session ID** — optional UUID generated by the host per agent session; used by the registry for `latest`-resolution consistency and learn-from-usage reranking.
