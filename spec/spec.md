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

Publish:

```bash
$ podium publish .
artifact: finance/close-reporting/run-variance-analysis@1.0.0
status: approved (low sensitivity, single-reviewer policy not required)
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

**Author your skills, agents, prompts, and reference contexts once in a canonical format; load them into any harness — Claude Code, Claude Desktop, Cursor, Codex, OpenCode, Gemini — without forking per-harness.**

Podium is a shared catalog for the artifacts agents load at session time. Two pieces:

1. **Registry service** — the system of record. Centralized, multi-tenant. Control-plane HTTP/JSON API for manifests, search, and signed URLs. Object-storage data plane for resource bytes. Postgres + pgvector for metadata, dependency edges, embeddings, RBAC bindings, and audit. Resolves overlays per OAuth identity.
2. **Podium MCP server** — a single-binary, in-process bridge the host runs alongside its own runtime. Exposes three meta-tools (`load_domain`, `search_artifacts`, `load_artifact`) over MCP; forwards calls to the registry under the host's OAuth-attested identity; materializes bundled resources atomically on the host's filesystem; runs the configured `HarnessAdapter` to translate the canonical artifact into the host's harness-native format on the way out.

What Podium gives you:

- **Author once, load anywhere.** One canonical authoring format. The configured `HarnessAdapter` translates at materialization time.
- **Lazy materialization at scale.** Sessions start empty. The agent navigates and searches the catalog; only the artifacts it actually loads are materialized on the host. Catalogs of thousands of artifacts don't pollute the system prompt.
- **Built for the enterprise.** RBAC, layered authoring (org / team / user / local), classification, lifecycle, signing, and audit are first-class.
- **Pluggable identity.** One MCP server binary serves every deployment context — interactive OAuth on developer hosts; injected session token in managed runtimes.

Sessions, agent execution, policy compilation, and downstream tool wiring are concerns that belong to hosts, not Podium.

### 1.2 Problem Statement

Organizations adopting AI accumulate large libraries of authored content. Past a few hundred items, several problems emerge together:

1. **Capability saturation.** Exposing thousands of skills, prompts, or tool definitions to a model degrades planning quality. Hosts need to see only what's relevant.
2. **Discoverability at scale.** A multi-domain catalogue with thousands of items shared across many teams needs a structured discovery model. A flat list does not work.
3. **RBAC.** Different users have different rights: who can read, publish, deprecate, manage memberships and overlays. The registry is the central enforcement point.
4. **Layered authoring.** Org / team / user / workspace contributions need to compose deterministically with clear precedence and no silent shadowing.
5. **Governance, classification, lifecycle.** Sensitivity labels, review status, ownership, deprecation paths, and reverse-dependency impact analysis need to be first-class.
6. **Type heterogeneity.** Skills, agents, context bundles, prompts, MCP server registrations, eval datasets, model files — every artifact type fits in one registry, with one storage and discovery model.
7. **Heterogeneous hosts.** Agent runtimes, build pipelines, terminal CLIs, evaluation harnesses, and other AI systems all read from the same catalogue; none should need its own copy.
8. **Cross-harness portability.** The same skill should work in Claude Code, Cursor, and Codex without forking. Per-harness convention sprawl is an authoring tax.

Podium addresses these together: a centralized registry service plus a thin MCP server hosts load alongside their runtime.

### 1.3 Design Principles

- **Lazy materialization.** Sessions start empty. The host sees only a high-level map; navigation, search, and load surface what's needed when it's needed (§3).
- **RBAC at the registry.** Scope claims (org, team, user) and roles live in the registry and travel on every OAuth-attested call. The registry is the only enforcement point for visibility and authoring rights.
- **The registry is a deployable service.** Authoring lives in Git; the runtime is a service (control plane HTTP API + object store + Postgres+pgvector). Updates take effect on the next call.
- **Type-agnostic discovery.** The registry defines an artifact type system (`skill` / `agent` / `context` / `prompt` / `mcp-server`, extensible) and treats every type uniformly for discovery, search, and load. Type-specific runtime behaviour lives in hosts.
- **Any file type or combination.** Manifests are markdown with YAML frontmatter; bundled resources alongside are arbitrary files. The registry stores them as opaque versioned blobs.
- **One MCP server, pluggable identity.** A single binary serves every deployment context. Identity is selected by configuration.
- **Materialization on the host's filesystem.** `load_artifact` lazily downloads bundled resources to a host-configured destination path, atomically. The catalog lives at the registry; the working set lives on the host.
- **Author once, load anywhere.** Adapters mechanically translate canonical artifacts into harness-native shapes. No per-harness forks.
- **Immutability and signing.** Every artifact version is bit-for-bit immutable. High-sensitivity artifacts are cryptographically signed.

### 1.3.1 When You Need Podium

- **Below ~50 artifacts**, a flat directory plus your harness's native conventions is fine. Podium is overkill.
- **50–500 artifacts**, you start to feel discovery pain (capability saturation in the system prompt; finding the right artifact by browsing). Podium pays off, especially if you're cross-harness or multi-team.
- **Above 500 artifacts**, the registry pays for itself outright on discovery alone.
- **Cross-harness portability** is valuable at any scale — even with five artifacts, "author once" beats forking.
- **Governance** (RBAC, classification, audit) is valuable above ~10 contributors.

A solo developer with a handful of skills in one harness doesn't need Podium yet. A 50-person team with skills in three harnesses does.

### 1.4 Constraints and Decisions

| Decision | Rationale |
| --- | --- |
| Two components: registry service, MCP server | A centralized registry with persistence, plus a thin in-process bridge hosts run alongside their runtime. |
| MCP server is a single binary with pluggable identity, overlay, and harness adapter | One binary serves every deployment context. Identity providers, the workspace `local` overlay, and the harness adapter are all selected via configuration. |
| Author once, load anywhere | Artifacts have one canonical authored form. At materialization time, the configured `HarnessAdapter` translates them into the harness's native shape (Claude Code, Claude Desktop, Cursor, Gemini, OpenCode, Codex, or `none` for raw). |
| Sessions start empty; discovery via meta-tools | Each session begins with zero artifact content. The host calls `load_domain` / `search_artifacts` / `load_artifact` to assemble its working set on demand. |
| Multiple overlay layers per identity (org / any number of teams / optional user / optional local) | Layers compose at the registry from the host's OAuth identity. The MCP server adds the workspace `local` layer when its `LocalOverlayProvider` is configured. Collisions: most-restrictive-wins for security fields, last-layer-wins for descriptions, explicit `extends:` to inherit. |
| RBAC enforced at the registry on every call | Roles bind to identities per scope (org, team). Permissions cover read, publish, review, deprecate, manage members, manage overlays, admin. |
| Registry as a deployable service | Authoring lives in Git; runtime is a service (control plane HTTP API + object store + Postgres+pgvector). |
| PostgreSQL + pgvector for the registry | Manifest metadata, dependency edges, embeddings, RBAC bindings, registry-side audit. Pluggable interface for alternatives. |
| Per-workspace MCP server lifecycle on developer hosts | When the MCP server runs as a developer-side subprocess, the host spawns one per workspace, over stdio. Local overlay is workspace-scoped (`.podium/overlay/`). Cache lives in `~/.podium/cache/` and is content-addressed across workspaces. |
| Versions are immutable; semver-named | Every `(artifact_id, semver)` pair, once published, is bit-for-bit immutable forever. Internal cache keying is by content hash. |
| MCP-only in v1 | Hosts must speak MCP. Non-MCP runtimes (LangGraph, OpenAI Assistants, Bedrock) integrate via thin language SDKs over the same registry HTTP surface — planned for v1.1. |
| Apache 2.0 license | Permissive, enterprise-friendly, common for infrastructure projects. |

### 1.5 Where Podium Fits

Podium overlaps with several existing categories. Quick comparison:

| Alternative | Overlap | When it wins | When Podium wins |
| --- | --- | --- | --- |
| **Anthropic Skills (Claude Code/Desktop)** | Same artifact authoring concept | Single-harness Claude shop; small catalog | Cross-harness portability; large catalog with discovery; RBAC/audit |
| **Cursor Rules / Continue contexts / Cline rules** | Per-harness convention files | Single-harness, single-user | Cross-harness; multi-user with overlays |
| **MCP server marketplaces** | Both register MCP servers | Discovering pre-built community servers | Internal authored content (skills, agents, prompts) alongside MCP servers |
| **LangChain Hub / LangSmith** | Prompt registry | Prompt-only flows; LangChain-native runtime | Type heterogeneity (skills, agents, contexts); cross-runtime |
| **PromptLayer / Langfuse / Helicone** | Prompt registry + observability | Prompt-only with strong eval focus | Broader artifact model; richer governance |
| **HuggingFace Hub** | Versioned artifact storage | Models and datasets at scale | Skills/agents/prompts authored for runtime use |
| **Git monorepo + GitHub** | Versioning, RBAC, search | Small catalog where a flat repo works | Lazy materialization; runtime resolution; harness translation; structured discovery |

The canonical artifact format is intended for upstream contribution to an MCP-adjacent standard once one exists; until then, it's specified here.

### 1.6 Project Model

- **License.** Apache 2.0.
- **Governance.** Maintainer model + RFC process for spec changes; see `GOVERNANCE.md`.
- **Distribution.** OSS-first development; optional commercial managed offering by the sponsoring entity (separate doc).
- **Public registry.** A reference registry with curated example artifacts is hosted at the project's public URL.

---

## 2. Architecture

### 2.1 High-Level Component Map

A single centralized registry service serves every host. The Podium MCP server is a thin binary the host runs alongside its own runtime. The same MCP server binary serves every deployment context; differences are configuration only.

```
                          ┌───────────────────────────┐
                          │ PODIUM REGISTRY (service) │
                          │  control plane (HTTP/JSON)│
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
                          │ + content-addressed cache │
                          │ + atomic materialization  │
                          └───────────▲───────────────┘
                                      │ MCP (stdio)
                                      │
                          ┌───────────┴───────────────┐
                          │ Host runtime              │
                          │ (any MCP-speaking AI      │
                          │  system)                  │
                          └───────────────────────────┘
```

Sequence for `load_artifact`:

```
host         MCP server          registry        object storage
 │ load_artifact │                  │                    │
 │──────────────▶│ POST /artifacts  │                    │
 │               │ (id, identity)   │                    │
 │               │─────────────────▶│                    │
 │               │                  │ RBAC + resolve     │
 │               │                  │ overlays + version │
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

Two deployment scenarios use the same MCP server binary:

- **Managed agent runtime.** The runtime spawns the MCP server as a co-located process. Identity is supplied via an injected session token (signed JWT); the registry endpoint is configured the same way. The `local` overlay is unset.
- **Developer's host.** The host spawns one MCP server per workspace as a stdio subprocess. The MCP server uses an OAuth device-code flow on first use (surfaced via MCP elicitation) to obtain a registry token, stored in the OS keychain. The workspace `local` overlay reads from `${WORKSPACE}/.podium/overlay/`.

### 2.2 Component Responsibilities

**Podium Registry** _(centralized service)_. The system of record for artifacts. Resolves overlays per OAuth identity, enforces RBAC, indexes manifests, runs hybrid search, signs URLs for resource bytes. Three persistent stores: Postgres + pgvector, object storage, HTTP/JSON API.

The registry's wire protocol is **HTTP/JSON**. The MCP surface is what the client-side MCP server exposes after translating registry HTTP responses into MCP tool results. Direct MCP access to the registry is not supported in v1.

**Podium MCP server** _(in-process bridge)_. Single binary. Exposes the three meta-tools. Holds no per-session server-side state — local state is limited to a content-addressed disk cache, OS-keychain-stored credentials (in `oauth-device-code` mode), an in-memory local-overlay index, and the materialized working set on disk. No state is shared across MCP server processes.

Pluggable interfaces:

- **IdentityProvider** — supplies the OAuth-attested identity attached to every registry call. Built-ins: `oauth-device-code` and `injected-session-token`. Additional implementations register through the interface.
- **LocalOverlayProvider** — optional. When configured, reads `ARTIFACT.md` packages from a workspace filesystem path and merges them as the `local` overlay layer (§4.6).
- **HarnessAdapter** — translates canonical artifacts into harness-native format at materialization time. Built-ins cover Claude Code, Claude Desktop, Cursor, Gemini, OpenCode, Codex; `none` (default) writes the canonical layout as-is. See §6.7.

Configuration: env vars, command-line flags, or a config file the host supplies. See §6.

**Hosts** _(not Podium components)_. Any MCP-speaking system that needs the catalog. Hosts spawn the Podium MCP server alongside their own runtime tools, configure its identity provider and (optionally) its local overlay, and use the meta-tools.

---

## 3. Disclosure Surface

### 3.1 The Problem

Capability saturation: tool-call accuracy starts to degrade past ~50–100 tools in a single system prompt and falls off sharply past ~200 (figures vary by model and task). Production catalogs run 1–5K artifacts. The gap is two orders of magnitude. Pre-loading everything fails at the model. Pre-loading nothing fails at the user. Discovery has to be staged.

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

**Scope filtering.** Every request to the registry carries the host's identity (via OAuth) and the resolved scope claims for that identity. The registry filters its catalog to only those artifacts the scope claims and RBAC bindings grant. This is gatekeeping, not disclosure — it bounds what the disclosure surface can reveal.

**Description quality.** Layers 1 and 2 only work if manifests describe themselves well. Each artifact's `description` field must answer "when should I use this?" in one or two sentences. The registry lints for thin descriptions and flags clusters of artifacts whose summaries collide.

**Learn-from-usage reranking.** The registry observes which artifacts actually get loaded after which queries (correlated within a `session_id` — see §5), and uses that signal to (a) rerank search results, (b) suggest import candidates to domain owners, and (c) flag artifacts whose authored descriptions underperform retrieval expectations.

### 3.4 Discovery Flow

A typical host session begins empty. The host calls `load_domain()` to get the top-level map. It either picks a domain and calls `load_domain("<domain>")` for the next level, or — if the request is specific enough — jumps straight to `search_artifacts`. When it has an artifact ID, it calls `load_artifact`, which materializes the package on the host (§6.6).

Only `load_artifact` writes to the host filesystem. The catalog lives at the registry; the working set lives on the host.

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
- `workflow` — reserved but not specified in v1.

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
- **Per-file soft cap (1 MB)** — publish-time warning above this.
- **Per-package soft cap (10 MB)** — publish-time error above this.

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

Org/team/user overlays are layers in the registry service indexed by scope claim (§4.6). At request time, the registry composes the effective view from all layers the host's identity entitles them to.

### 4.3 Artifact Manifest Schema

The manifest frontmatter is YAML; the prose body is markdown. The registry indexes frontmatter for `search_artifacts` and `load_domain`. The prose body is returned inline by `load_artifact`.

#### Universal fields (any artifact type)

```yaml
---
type: skill | agent | context | prompt | mcp-server | <extension type>
name: run-variance-analysis
version: 1.0.0                       # semver, publisher-chosen
description: One-line "when should I use this?"
when_to_use:
  - "After month-end close, to flag unusual variance vs. forecast"
tags: [finance, close, variance]
sensitivity: low | medium | high
license: Apache-2.0                  # SPDX identifier
search_visibility: indexed | direct-only   # default: indexed
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

sbom:                                 # CycloneDX or SPDX inline or referenced
  format: cyclonedx-1.5
  ref: ./sbom.json

approval_policy:                      # optional override of sensitivity defaults
  stages:
    - reviewers: [team:finance/reviewers]
      min_approvals: 1
  timeout: "7d"
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

# Inheritance — explicitly extend another artifact's manifest (overlay merge)
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

A publish-time linter validates that prose references in `ARTIFACT.md` resolve to:

- Bundled files (existence check)
- URLs (HTTP HEAD returns 200/3xx)
- Other artifacts (registry-side resolution against current visible catalog)

Drift between manifest text and bundled files is a publish error.

**Trust model.** Bundled scripts inherit the artifact's sensitivity label. A high-sensitivity skill that bundles a Python script is effectively shipping code into the host; publish-time CI (secret scanning, static analysis, SBOM generation, optional sandbox policy review) takes bundled scripts seriously.

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

| Profile | Meaning |
| --- | --- |
| `unrestricted` | No sandbox constraints. Default for low-sensitivity. |
| `read-only-fs` | Filesystem is read-only outside the materialization destination. |
| `network-isolated` | No outbound network. |
| `seccomp-strict` | Strict syscall allowlist (per a baseline profile shipped with Podium). |

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

This asymmetry exists because the local layer is merged client-side (§6.4); the registry doesn't see it.

Imports are dynamic: an artifact added at `finance/ap/payments/new-thing/` is automatically picked up by any domain whose `DOMAIN.md` includes `finance/ap/payments/*` — no `DOMAIN.md` re-publish needed.

**Imports do not change canonical paths.** An artifact has exactly one canonical home (the directory where its `ARTIFACT.md` lives). Imports add additional appearances under other domains. `search_artifacts` returns the artifact once, with its canonical path and (optionally) the list of domains that import it.

**RBAC for imports.** Editing a domain's `include:`/`exclude:` requires publisher rights in the destination domain. Importing does not require rights in the source path. RBAC at read time still applies.

**Cycle detection.** Two domains importing each other is allowed but lint-warned.

**Validation.** Imports that don't currently resolve in any view the registry knows about produce a publish-time **warning**, not an error. This handles "expected to be defined in another overlay later" without coordinated publishes.

#### 4.5.3 Unlisted Folders

Setting `unlisted: true` in a folder's `DOMAIN.md` removes that folder and its entire subtree from `load_domain` enumeration. Artifacts inside still:

- Are reachable via `load_artifact(<id>)` if the host has visibility.
- Appear in `search_artifacts` results normally (subject to per-artifact `search_visibility:`).
- Can be imported into other domains via `include:`.

`unlisted: true` propagates to the whole subtree.

#### 4.5.4 DOMAIN.md Across Overlays

If multiple overlays contribute a `DOMAIN.md` for the same path, the registry merges them:

- `description` and prose body — last-layer-wins.
- `include:` — additive across layers.
- `exclude:` — additive across layers; applied after the merged include set.
- `unlisted` — most-restrictive-wins.

When a local `DOMAIN.md` is involved, the MCP server applies the merge client-side after the registry returns its result for the remote layers.

### 4.6 Overlays and Scope Claims

#### Terminology

- **Layer** — a unit of composition (org / team / user / local).
- **Overlay** — the contents authored under a layer.
- **Scope** — the OAuth claim that grants access to a layer.

Each registry-side artifact is owned by exactly one scope: `org`, `team:<org_id>/<team_name>`, or `user:<user_id>`. A request's effective view is the union of layers the host's OAuth identity entitles them to:

1. The org layer (always visible to org members).
2. The team layers for every `team:<org_id>/<team_name>` claim on the host's token. Any number of team claims is supported.
3. The personal layer for the host's `user:<user_id>` claim, if any.
4. The `local` layer (when configured): artifacts read from the workspace filesystem at `${PODIUM_OVERLAY_PATH}` (default `${WORKSPACE}/.podium/overlay/`). Resolved by the MCP server's `LocalOverlayProvider`. Composed as the most-specific layer, after `user:<id>`.

Within the team layers, composition is alphabetical by team name as the deterministic tiebreaker for collisions. Org → all team layers (alphabetical) → user → local is the full precedence order. Resolution of layers 1–3 happens at the registry on every `load_domain`, `search_artifacts`, and `load_artifact` call; layer 4 is merged in by the MCP server before returning results.

The `local` layer uses the same `ARTIFACT.md` + frontmatter format and the same merge semantics (below) as remote layers. Its content hash is exposed alongside the remote layer hashes in `load_domain` responses. To promote a local artifact, copy it into the registry and run `podium publish`.

**Shared workspaces caveat.** Two developers in the same workspace with different local overlays effectively see different `DOMAIN.md` resolutions. By design — but worth noting: avoid local `DOMAIN.md` changes for paths the team relies on; promote to user/team overlay before sharing.

#### Merge semantics for collisions

If two layers contribute artifacts with the same canonical ID:

- A collision is a publish error **unless** the higher-precedence artifact declares `extends: <lower-precedence-id>` in frontmatter.
- When `extends:` is declared, fields merge per the table below.

`extends:` is a single scalar in v1 (no multiple inheritance). Cycle detection at publish time. Parent version is resolved at child publish time and pinned (parent updates do not silently propagate; republish the child to pick up changes).

To intentionally replace an artifact rather than extend it, the lower-precedence layer must remove it first or rename the higher-precedence one. Silent shadowing is never permitted.

#### Field semantics table

| Field | Merge semantics |
| --- | --- |
| `description`, `name`, `release_notes` | Scalar; child wins |
| `tags` | List; append unique |
| `when_to_use` | List; append |
| `sensitivity` | Scalar; most-restrictive (high > medium > low) |
| `mcpServers` | List of objects; deep-merge by `name` |
| `requiresApproval` | List; append |
| `runtime_requirements` | Map; deep-merge with child wins |
| `sandbox_profile` | Scalar; most-restrictive |
| `delegates_to` | List; append |
| `external_resources` | List; append |
| `license` | Scalar; child wins (lint warning if changed across layers) |
| `search_visibility` | Scalar; most-restrictive (`direct-only` > `indexed`) |
| `approval_policy` | Object; most-restrictive (more reviewers / longer timeout wins) |

Extension types register their own field semantics via `TypeProvider`.

**Typical layering.**

- **Org.** Organization-wide artifacts, policies, glossaries.
- **Team.** Team-specific skills, agents, and extensions.
- **User.** Individual customizations and personal helpers.
- **Local.** A developer's in-progress work in a specific workspace.

The IdP resolves a host's scope claims from their OAuth token; Podium grants only what the token carries.

### 4.7 Registry as a Service

The registry is a deployable service. The on-disk layout described above (§4.2–§4.5) is the **authoring** model; overlays (§4.6), RBAC (§4.7.2), and the runtime model below are how the service serves requests. Three persistent stores:

- **Postgres + pgvector.** Manifest metadata, descriptors, scope claims, overlay rules, RBAC bindings, dependency edges, deprecation status, audit log, and embeddings used by `search_artifacts`.
- **Object storage.** Bundled resource bytes per artifact version, fronted by presigned URL generation. Versioned: each artifact version is immutable.
- **HTTP/JSON API.** Stateless front door. Accepts OAuth-attested identity, evaluates RBAC, resolves scope and overlays, queries Postgres, signs URLs, returns responses.

#### Version immutability invariant

A `(artifact_id, version)` pair, once published, is bit-for-bit immutable forever. Readers in flight when a republish occurs continue to see their pinned version. This is a load-bearing system invariant.

#### 4.7.1 Tenancy

The service supports three management layers:

- **Org.** A tenant boundary. Org admins manage teams, users, the org-wide overlay, and org-level RBAC bindings. Org IDs are UUIDs; org names are human-readable aliases.
- **Team.** A grouping inside an org. Team names are unique within an org. The canonical scope claim form is `team:<org_id>/<team_name>`.
- **User.** An identity inside zero or more teams, optionally with a personal overlay.

A request's effective view is the union of org overlay → team overlays → optional personal overlay → optional local overlay (when configured), with collisions handled per §4.6, and filtered by RBAC (§4.7.2).

**Postgres isolation.** Each org has its own schema; cross-org tables (e.g., shared infrastructure metadata) use row-level security with org_id checks. Schema-per-org gives clean drop-org semantics, isolates query patterns, and bounds the blast radius of SQL injection.

##### 4.7.1.1 Data Residency

v1 is single-region per deployment. Multi-region deployments run separate registries per region with no cross-region replication. A future cross-region federation (post-v1) will allow signed catalog summaries to flow between region-pinned registries.

#### 4.7.2 Role-Based Access Control

RBAC is enforced at the registry on every API call. Roles bind to identities per scope (org or team).

| Role | Permissions |
| ---- | ----------- |
| `reader` | List, search, and load artifacts visible under the binding's scope. |
| `publisher` | Reader + create / update artifacts (subject to review status). |
| `reviewer` | Reader + approve, reject, or request changes on draft artifacts. |
| `owner` | Publisher + reviewer + deprecate artifacts within the binding's scope. |
| `admin` | Owner + manage RBAC bindings, team membership, and overlay configuration within the binding's scope. |

Bindings are stored as `(identity, scope, role)` triples in Postgres. Multiple bindings per identity compose additively.

**Scope hierarchy.** Org-level bindings apply to every artifact in the org; team-level bindings apply to artifacts in that team's scope.

**Role extensibility.** Deployments can register additional roles with custom permission bundles via the `RBACProvider` interface (§9).

**Approval policies.** Per-artifact `approval_policy:` frontmatter (or inherited from sensitivity defaults) specifies the review workflow:

```yaml
approval_policy:
  stages:
    - reviewers: [team:finance/reviewers]
      min_approvals: 1
    - reviewers: [team:security/reviewers, team:legal/reviewers]
      min_approvals: 2
      parallel: true
  timeout: "7d"
  delegated_approval_allowed: true
```

Defaults per sensitivity:

- `low` — single reviewer.
- `medium` — two reviewers.
- `high` — reviewer + security + manager (parallel).

**Freeze windows.** Org-level config:

```yaml
freeze_windows:
  - name: "year-end-close"
    start: "2026-12-15T00:00:00Z"
    end: "2026-12-31T23:59:59Z"
    blocks: [publish, deprecate, overlay-edit]
    break_glass_role: admin
```

During a freeze, blocked operations are rejected unless `--break-glass` is passed. Break-glass requires dual-signoff (two admins), justification, auto-expires after 24h, and queues for post-hoc security review.

**Visibility vs. authoring.** Scope claims (§4.6) determine what artifacts an identity can see. RBAC determines what an identity can do with what they see.

**RBAC for domain composition.** Editing a `DOMAIN.md` requires publisher rights in the destination domain. Importing does not require any rights in the source path.

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

- **Sensitivity label.** Low / medium / high. Drives review requirements via `approval_policy` defaults.
- **Ownership.** Team or individual ID. The registry routes review and deprecation requests to owners.
- **Review status.** Draft / under review / approved / deprecated. Only approved artifacts appear in `search_artifacts` and `load_domain` results by default; deprecated artifacts return a warning when loaded and are excluded from default search results.

Deprecation is a soft state: the artifact remains loadable until its sunset date, but the registry surfaces upgrade paths if `replaced_by:` is set.

##### 4.7.4.1 Review Workflow Primitives

- `podium publish --draft` — creates a draft (no review starts).
- `podium publish` — submits for review per the artifact's `approval_policy`.
- `podium review queue` — lists pending reviews for the caller's reviewer scope.
- `podium review approve <id> [--comment <text>]`
- `podium review reject <id> --comment <text>`
- `podium review changes-requested <id> --comment <text>`

Comments are stored on the artifact's review record and appear in audit. Clients can build UIs on top.

#### 4.7.5 Audit

Every `load_domain`, `search_artifacts`, and `load_artifact` call is logged with caller identity, scope claims, RBAC outcome, requested artifact (or query), timestamp, resolved overlay, and result size. Publish, review, deprecate, and admin actions are also logged. Hosts keep their own audit streams for runtime events; Podium's audit stream stays focused on the catalogue. Detail in §8.

#### 4.7.6 Version Resolution and Consistency

Versions are semver-named (`major.minor.patch`), publisher-chosen at publish time. Internally, the registry stores `(artifact_id, semver, content_hash)` triples; content_hash is the SHA-256 of the canonicalized manifest + bundled resources.

Pinning syntax in references (`extends:`, `delegates_to:`, `mcpServers:`):

- `<id>` — resolves to `latest`.
- `<id>@<semver>` — exact version.
- `<id>@<semver>.x` — minor or patch range (e.g., `1.2.x`, `1.x`).
- `<id>@sha256:<hash>` — content-pinned.

`load_artifact(<id>)` resolves to `latest` = "the most recently approved version visible under the host's effective view, at resolution time." Resolution is registry-side.

For session consistency, the meta-tools accept an optional `session_id` argument (UUID generated by the host per agent session). The first `latest` lookup within a session is recorded and reused for all subsequent same-id lookups in that session — so the host sees a consistent snapshot.

**Inheritance and republish.** When a child manifest declares `extends: <parent>` (no version pin), the parent version is resolved at the child's publish time and stored as a hard pin in the published manifest's resolved form. Parent updates do not silently propagate; republish the child to pick up changes.

**Concurrency.** Publish uses CAS via `if-match: <previous-content-hash>`; conflicts return HTTP 412 with structured error `publish.conflict`.

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

Each artifact version is signed by the publisher's key. Two key models:

- **Sigstore-keyless** (preferred). OIDC-attested signature; transparency log entry; no key management.
- **Registry-managed key** (fallback). Per-org key managed by the registry; rotated quarterly.

Signatures are stored alongside content. The MCP server verifies signatures on materialization for sensitivity ≥ medium (configurable per deployment). Signature failure aborts materialization with `materialize.signature_invalid`.

`podium verify <artifact>` for ad-hoc verification. `podium sign <artifact>` for explicit signing outside the publish flow.

---

## 5. Meta-Tools

Podium exposes three meta-tools through the Podium MCP server. These are the only tools Podium contributes; hosts add their own runtime tools alongside.

| Tool             | Description                                                                                                                                                                                                                                                |
| ---------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `load_domain`      | Returns the map for a path: `load_domain()` (root), `load_domain("finance")` (domain), `load_domain("finance/close-reporting")` (subdomain). Output groups artifacts by type, lists notable entries, includes vocabulary hints. Optional `session_id` arg. |
| `search_artifacts` | Hybrid retrieval (BM25 + embeddings, RRF) over artifact frontmatter. Filters by `type`, `tags`, `scope`. Returns top N results with frontmatter and retrieval scores; bodies stay at the registry until `load_artifact`. Optional `session_id` arg.        |
| `load_artifact`    | Loads a specific artifact by ID and version. Returns the manifest content as the tool result; **materializes** any bundled resources to a host-configured path on the filesystem (atomic write via `.tmp` + rename; presigned URLs for large blobs). Args: `id`, optional `version`, optional `session_id`, optional `harness:` override. |

`load_domain` and `search_artifacts` round-trip through the registry on every call (no snapshot caching at session startup). Only `load_artifact` writes to the host filesystem, and only for the specific artifact requested.

The MCP server declares its capabilities in the MCP `initialize` response: `{tools: true, prompts: <conditional on prompt artifacts with expose_as_mcp_prompt: true>, sessionCorrelation: true}`.

### 5.0 Why Tools, Not Resources

MCP resources fit static lists and host-driven enumeration. Podium's catalog needs parameterized navigation (`load_domain` takes a path; `search_artifacts` takes a query) and lazy materialization with side effects. Tools fit better.

Artifact bodies are also exposed as MCP resources for hosts that prefer that pattern (read-only mirror of `load_artifact`); the canonical interface remains the three meta-tools.

### 5.1 Meta-Tool Descriptions and Prompting Guidance

The strings below are the canonical tool descriptions exposed to the LLM via MCP. Hosts SHOULD use them verbatim unless customizing for a specific runtime.

#### `load_domain`

> Browse the artifact catalog hierarchically. Call with no path to see top-level domains. Call with a path (e.g., "finance") to see that domain's subdomains and notable artifacts. Use this when you don't know what's available and need to explore. Returns a map; doesn't load any artifact's content. To use an artifact you find here, call `load_artifact`.

#### `search_artifacts`

> Search the artifact catalog by query. Use this when you know roughly what you're looking for but not the exact artifact ID. Filters: `type` (skill, agent, context, prompt, mcp-server), `tags`, `scope` (a domain path to constrain the search). Returns ranked descriptors only — no manifest bodies. To use a result, call `load_artifact` with its id.

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

### 6.2 Configuration

Top-level configuration parameters (env-var form shown; `--flag` and config-file equivalents are accepted):

| Parameter                     | Description                                                                                  | Default                                |
| ----------------------------- | -------------------------------------------------------------------------------------------- | -------------------------------------- |
| `PODIUM_REGISTRY_ENDPOINT`    | Registry HTTP API endpoint                                                                   | (required)                             |
| `PODIUM_IDENTITY_PROVIDER`    | Selected identity provider implementation                                                    | `oauth-device-code`                    |
| `PODIUM_HARNESS`              | Selected harness adapter                                                                     | `none` (write canonical layout as-is)  |
| `PODIUM_OVERLAY_PATH`         | Workspace path for the `local` overlay                                                       | (unset → layer disabled)               |
| `PODIUM_CACHE_DIR`            | Content-addressed cache directory                                                            | `~/.podium/cache/`                     |
| `PODIUM_CACHE_MODE`           | `always-revalidate` / `offline-first` / `offline-only`                                       | `always-revalidate`                    |
| `PODIUM_AUDIT_SINK`           | Local audit destination (path or external endpoint)                                          | (unset → registry audit only)          |
| `PODIUM_MATERIALIZE_ROOT`     | Default destination root for `load_artifact`                                                 | (host specifies per call)              |
| `PODIUM_PRESIGN_TTL_SECONDS`  | Override for presigned URL TTL                                                               | 3600                                   |
| `PODIUM_VERIFY_SIGNATURES`    | Verify artifact signatures on materialization                                                | `medium-and-above`                     |

Provider-specific options are passed as additional env vars (e.g., `PODIUM_OAUTH_AUDIENCE`, `PODIUM_SESSION_TOKEN_ENV`).

### 6.3 Identity Providers

Identity providers attach the caller's OAuth-attested identity to every registry call.

- **`oauth-device-code`** _(default)_. Interactive device-code flow on first use, surfaced via MCP elicitation; tokens cached in the OS keychain (macOS Keychain, Windows Credential Manager, libsecret on Linux). Refreshes transparently. Defaults: access-token TTL 15 min, refresh-token TTL 7 days, revocation propagation ≤60s. Options: `PODIUM_OAUTH_AUDIENCE`, `PODIUM_OAUTH_AUTHORIZATION_ENDPOINT`, `PODIUM_TOKEN_KEYCHAIN_NAME`.
- **`injected-session-token`**. The MCP server reads a signed JWT from an env var or file path configured by the runtime. The runtime is responsible for token issuance and refresh. Options: `PODIUM_SESSION_TOKEN_ENV`, `PODIUM_SESSION_TOKEN_FILE`.
- **(Extensible.)** Additional implementations register through the `IdentityProvider` interface (§9).

#### 6.3.1 Claim Derivation

The IdP returns a JWT with claims `{sub, org_id, email, exp, iss, aud}`. Team membership is resolved registry-side via SCIM 2.0 push from the IdP — the registry maintains a directory of `(user_id → teams)`.

For IdPs without SCIM, the `IdpGroupMapping` adapter reads OIDC group claims from the token and maps them to team names per a registry-side configuration.

Tested IdPs: Okta, Entra ID, Auth0, Google Workspace, Keycloak. SAML supported via OIDC bridge.

Fine-grained scoping via OAuth scope claims (e.g., `podium:read:finance/*`, `podium:load:finance/ap/pay-invoice@1.x`); narrow scopes intersect with RBAC bindings — the smaller surface wins.

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

### 6.4 Local Overlay Provider

Optional. When `PODIUM_OVERLAY_PATH` is set, the MCP server watches the configured path for `ARTIFACT.md` and `DOMAIN.md` files and merges them as the `local` overlay layer (§4.6). fsnotify watcher re-indexes on change.

Default path resolution uses MCP roots when available (the `roots/list` response identifies the workspace).

Format: same `ARTIFACT.md` + frontmatter as the registry; resolution and merge semantics identical to remote layers.

To promote a local artifact, copy it into the registry and run `podium publish`.

#### 6.4.1 Local Search Index

When `LocalOverlayProvider` is configured, the MCP server maintains a local BM25 index over local-overlay manifest text. `search_artifacts` calls fan out to both the registry and the local index; the MCP server fuses results via reciprocal rank fusion before returning.

Embeddings for the local index are out of scope for v1 (BM25-only); local artifacts have lower recall on semantic queries than registry artifacts. Acceptable for the developer iteration loop — the goal is "find my draft," not "outrank everything else."

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

| Value             | Target                                                                                                                  |
| ----------------- | ----------------------------------------------------------------------------------------------------------------------- |
| `none`            | _(default)_ Writes the canonical layout as-is.                                                                           |
| `claude-code`     | Writes `.claude/agents/<name>.md` (frontmatter + composed prompt) and places bundled resources under `.claude/podium/<artifact-id>/`. |
| `claude-desktop`  | Writes a Claude Desktop extension layout (`manifest.json` derived from canonical frontmatter; resources alongside).      |
| `cursor`          | Writes Cursor's native agent / extension format.                                                                         |
| `gemini`          | Writes Gemini's native agent / extension package layout.                                                                 |
| `opencode`        | Writes OpenCode's native package layout.                                                                                 |
| `codex`           | Writes Codex's native package layout.                                                                                   |

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
2. **Capability matrix.** Per-(field, harness) compatibility table maintained alongside the adapters. Publish-time lint surfaces capability mismatches: "field `X` is used but adapter `cursor` cannot translate it."

Authors who must use a non-portable feature can declare `target_harnesses:` in frontmatter to opt out of cross-harness materialization for that artifact.

**Capability matrix (excerpt; maintained in sync with adapter implementations):**

| Field | claude-code | cursor | codex | opencode | gemini |
| --- | --- | --- | --- | --- | --- |
| `description` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `mcpServers` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `delegates_to` (subagents) | ✓ | ✗ | ✗ | ✓ | ✗ |
| `requiresApproval` | ✓ | ✗ | ✓ | ✓ | ✗ |
| `sandbox_profile` | ✓ | ✗ | ✓ | ✓ | ✗ |
| `expose_as_mcp_prompt` | ✓ | ✓ | ✓ | ✓ | ✓ |

### 6.8 Process Model

The MCP server is a stdio subprocess spawned by its host. The host is responsible for lifecycle (spawn, signal handling, shutdown).

- **Developer hosts.** One subprocess per workspace, spawned when the workspace opens and torn down when the workspace closes.
- **Managed agent runtimes.** One subprocess per session, spawned by the runtime's bootstrap glue alongside the agent.

### 6.9 Failure Modes

| Failure                                         | Behavior                                                                                              |
| ----------------------------------------------- | ----------------------------------------------------------------------------------------------------- |
| Registry offline                                | Serve from cache; return explicit "offline" status on fresh `load_domain` / `search_artifacts`.       |
| Overlay path missing                            | Skip overlay layer; warn once.                                                                       |
| Auth token expired (`oauth-device-code`)         | Trigger refresh; if interactive refresh required, surface in tool response with reauth instructions via MCP elicitation. |
| Auth token expired (`injected-session-token`)    | Surface "token expired"; the host's runtime is responsible for refresh.                              |
| Untrusted runtime (`injected-session-token`)     | Reject with `auth.untrusted_runtime`. Runtime must register signing key with registry.                |
| RBAC denial on a call                           | Return a structured error naming the missing role; log to the registry audit stream.                  |
| Materialization destination unwritable          | Fail the `load_artifact` call with a structured error; nothing partial is left on disk.               |
| Signature verification failure                  | Fail with `materialize.signature_invalid`; do not write to disk.                                       |
| Unknown `PODIUM_HARNESS` value                  | Refuse to start; CLI lists the available adapter values.                                              |
| Adapter cannot translate an artifact            | Fail with structured error naming the missing translation; suggest `harness: none` for raw output.   |
| Binary version mismatch with host caller        | Refuse to start; host's CLI prompts an update.                                                       |
| MCP protocol version mismatch                   | Negotiate down to host's max supported MCP version; if no compatible version, fail with `mcp.unsupported_version`. |
| Quota exhausted                                 | Structured error (`quota.storage_exceeded` etc.); operation rejected.                                  |
| Runtime requirement unsatisfiable               | Fail with `materialize.runtime_unavailable`; lists the unsatisfied requirement.                       |

### 6.10 Error Model

All errors use a structured envelope:

```json
{
  "code": "auth.untrusted_runtime",
  "message": "Runtime 'managed-runtime-x' is not registered with the registry.",
  "details": {"runtime_iss": "managed-runtime-x"},
  "retryable": false,
  "suggested_action": "Register the runtime's signing key via 'podium admin runtime register'."
}
```

Codes are namespaced (`auth.*`, `rbac.*`, `publish.*`, `materialize.*`, `quota.*`, `mcp.*`, `network.*`). Mapped to MCP error payloads per the MCP spec.

---

## 7. External Integration

### 7.1 The Registry Is an External System

From the host's perspective, the registry is an external system reached on demand. Every discovery, search, and load call round-trips to the registry's HTTP API. The system prompt carries the meta-tool descriptions only; the working set assembles call by call as the host invokes `load_artifact`.

This separation is deliberate:

- The registry can be self-hosted, multi-tenant, or fully managed without changing the host's behavior.
- Multi-scope overlay resolution and RBAC enforcement live at the registry, where the OAuth identity is the authoritative input.
- Artifact updates take effect on the next call.

#### Latency budgets (SLO targets)

- `load_domain`: p99 < 200 ms
- `search_artifacts`: p99 < 200 ms
- `load_artifact` (manifest only): p99 < 500 ms
- `load_artifact` (manifest + ≤10 MB resources from cache miss): p99 < 2 s

Deployments that miss these should investigate.

### 7.2 Control Plane / Data Plane Split

The registry exposes two surfaces:

**Control plane (HTTP API).** Returns metadata: manifest bodies, descriptors, search results, domain maps. Synchronous. Audited. Every call carries the host's OAuth identity and is RBAC-checked.

**Data plane (object storage).** Holds bundled resources. The control plane never streams bytes for resources above the inline cutoff (256 KB). Instead, `load_artifact` returns presigned URLs that the Podium MCP server fetches directly from object storage.

Below the inline cutoff, resources are returned inline. This avoids round-trips for small fixtures.

### 7.3 Host Integration

Podium does not expose its own end-user client API. Hosts spawn the Podium MCP server alongside their own runtime tools. The Podium CLI (`podium publish`, `podium cache prune`, `podium rbac …`, etc.) is for authoring and operator tasks against the registry; agents do not consume it at runtime.

Any AI system that speaks MCP can consume Podium: agent runtimes, build pipelines, terminal CLIs, evaluation harnesses, custom scripts. The contract is the three meta-tools plus the materialization semantics described in §6.6.

#### 7.3.1 Publish Workflow Specification

```
podium publish [<path>] [--draft] [--break-glass --justification <text>]
```

- **Inputs.** Directory path (default `.`); recursively scans for `ARTIFACT.md` and `DOMAIN.md`. Push model — the CLI uploads to the registry's HTTP API. Pull-from-Git is post-v1 future work.
- **Outputs.** `(artifact_id, version, status)` per artifact; structured errors per the §6.10 error model.
- **Errors.** Lint failures (`publish.lint_failed`), RBAC denials (`rbac.*`), CAS conflicts (`publish.conflict`), freeze-window blocks (`publish.frozen`), quota exhaustion (`quota.*`).

#### 7.3.2 Outbound Webhooks

The registry emits outbound webhooks for:

- `artifact.published`
- `artifact.review_state_changed`
- `artifact.deprecated`
- `domain.published`
- `vulnerability.detected`

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

---

## 8. Audit and Observability

### 8.1 What Gets Logged

Every significant event, each carrying a trace ID (W3C Trace Context):

| Event                | When                                                              | Source           |
| -------------------- | ----------------------------------------------------------------- | ---------------- |
| `domain.loaded`        | Host invoked `load_domain`                                      | Registry         |
| `artifacts.searched`   | Host invoked `search_artifacts`                                 | Registry         |
| `artifact.loaded`      | Host invoked `load_artifact`                                     | Registry         |
| `artifact.published`   | Publisher created or updated an artifact                          | Registry         |
| `artifact.reviewed`    | Reviewer approved, rejected, or requested changes                 | Registry         |
| `artifact.deprecated`  | Owner deprecated an artifact                                      | Registry         |
| `artifact.signed`      | Artifact version signed                                           | Registry         |
| `domain.published`     | Publisher created or updated a `DOMAIN.md`                        | Registry         |
| `rbac.binding.changed` | Admin added or removed an RBAC binding                            | Registry         |
| `overlay.changed`      | Admin updated an overlay configuration                            | Registry         |
| `rbac.denied`          | A call was rejected for missing permissions                       | Registry         |
| `freeze.break_glass`   | Admin used break-glass during a freeze window                     | Registry         |
| `vulnerability.detected` | CVE matched an artifact's SBOM                                  | Registry         |
| `user.erased`          | Admin invoked the GDPR erasure command                            | Registry         |

Audit lives in two streams. The registry owns the events above. The MCP server can also write a local audit log for the meta-tool events through a `LocalAuditSink` interface (§9) when configured. Both streams share trace IDs.

### 8.2 PII Redaction

Two redaction surfaces:

- **Manifest-declared.** Artifact manifests can specify fields that should be redacted in audit logs (e.g., `bank_account`, `ssn`). The registry honors redaction directives; the MCP server applies the same directives before writing to its local audit sink.
- **Query text.** Free-text `search_artifacts` queries are regex-scrubbed for common PII patterns (SSN, credit-card, email, phone) before being written to audit. Patterns configurable via `PIIRedactionConfig`. Default-on.

### 8.3 Audit Sinks

The registry has its own sink for catalogue events. The local file log, when enabled via `PODIUM_AUDIT_SINK`, is written by the MCP server through the `LocalAuditSink` interface. Both default to local storage and can be redirected to external SIEM / log aggregation independently.

### 8.4 Retention

Defaults, configurable per deployment:

| Data | Retention |
| --- | --- |
| Audit events (metadata) | 1 year |
| Query text | 30 days (redacted to placeholders after 7 days) |
| Deprecated artifact versions | 90 days post-sunset |
| Soft-deleted artifacts | 30 days |
| Vulnerability scan history | 1 year |

Optional sampling for high-volume low-sensitivity events (e.g., `domain.loaded` at 10% sample) reduces storage cost.

### 8.5 Erasure

```
podium admin erase <user_id>
```

- Removes user-overlay artifacts.
- Redacts the user identity in audit records (replaces with `redacted-<sha256(user_id+salt)>`).
- Preserves audit event sequencing for integrity.

GDPR right-to-erasure is supported via this command. Erasure is itself logged as a `user.erased` event.

### 8.6 Audit Integrity

Every audit event carries a hash chain: `event_hash = sha256(event_body || prev_event_hash)`. Detection of gaps is automated and alerted.

Periodic anchoring of the chain head to a public transparency log (Sigstore/CT-style) is recommended for high-assurance deployments. SIEM mirroring is the operational integrity backstop.

---

## 9. Pluggable Interfaces

| Interface              | Default                                              | Purpose                                                                                          |
| ---------------------- | ---------------------------------------------------- | ------------------------------------------------------------------------------------------------ |
| `RegistryStore`          | Postgres + pgvector                                  | Manifest metadata, dependency edges, embeddings, RBAC bindings, registry-side audit              |
| `RegistryObjectStore`    | S3-compatible                                        | Bundled resource bytes, presigned URLs                                                           |
| `RegistrySearchProvider` | BM25 + pgvector (RRF)                                | Hybrid retrieval for `search_artifacts`                                                          |
| `RegistryAuditSink`      | Separate Postgres table within `RegistryStore`       | Stream for catalogue events; logically distinct, separately mockable, separately routable        |
| `OverlayResolver`        | OAuth claim → layer composition                      | Org / team / user layer composition; collision/extends merge (registry-side, layers 1–3)         |
| `RBACProvider`           | Built-in roles                                       | Role definitions and permission evaluation                                                       |
| `TypeProvider`           | Built-in first-class types                           | Type definitions: frontmatter JSON Schema + lint rules + adapter hints + field-merge semantics    |
| `PublishLinter`          | Built-in rule registry                               | Manifest validation, resource-reference checks, sensitivity sign-off, type-specific rules        |
| `IdentityProvider`       | `oauth-device-code` (alt: `injected-session-token`)  | Attaches OAuth-attested identity to every registry call from the MCP server                      |
| `LocalOverlayProvider`   | Workspace filesystem (`.podium/overlay/`)            | Source for the `local` overlay layer                                                             |
| `LocalAuditSink`         | JSON Lines file at `${WORKSPACE}/.podium/audit.log`  | Local audit log for meta-tool calls (when configured)                                            |
| `HarnessAdapter`         | `none` (built-ins per §6.7)                          | Translates canonical artifacts to the harness's native format at materialization time            |
| `NotificationProvider`   | Email + webhook                                      | Delivery for review notifications, vulnerability alerts                                          |
| `SignatureProvider`      | Sigstore-keyless                                     | Artifact signing and verification                                                                 |

### 9.1 Plugin Distribution

In v1, plugins ship as Go modules importable into a registry build. A deployment that needs a custom `IdentityProvider` builds a registry binary from source with the plugin imported.

Out-of-process plugins (subprocess + RPC) are post-v1 future work.

A community plugin registry is hosted at the project's public URL.

---

## 10. MVP Build Sequence

| Phase | What                                                                                              | Why                                                            |
| ----- | ------------------------------------------------------------------------------------------------- | -------------------------------------------------------------- |
| 0     | `podium serve --solo` (single binary, embedded SQLite, filesystem object store, no auth)          | Five-minute install for personal use; on-ramp for OSS adoption |
| 1     | Registry data model (Postgres + pgvector + object storage layout)                                 | The catalog is the foundation                                  |
| 2     | Publish pipeline + lint for `ARTIFACT.md` and `DOMAIN.md` + per-type lint rules + signing         | Authors need a way to add artifacts before hosts can load them |
| 3     | Registry HTTP API: `load_domain`, `search_artifacts`, `load_artifact`                             | The wire surface the MCP server talks to                       |
| 4     | Overlay resolver + scope-claim filtering (org / teams / user) + OIDC + SCIM 2.0                   | Multi-tenant correctness from day one                          |
| 5     | Domain composition: `DOMAIN.md` parser, glob resolver, `unlisted` enforcement, cross-overlay merge | Multi-membership without duplication                           |
| 6     | RBAC: built-in roles, bindings table, `approval_policy`, `freeze_windows`, evaluation on every call | Authoring rights and visibility enforced centrally             |
| 7     | Versioning: semver, immutability, CAS publish, content-hash cache keys, `latest` resolution with `session_id` consistency | Foundational invariant |
| 8     | Podium MCP server core (single-shape binary: registry client, resolver, MCP handlers, cache, materialization, signature verification) | The bridge hosts load alongside their runtime |
| 9     | IdentityProvider implementations: `oauth-device-code` (with OS keychain) and `injected-session-token` (signed JWT contract) | One binary across deployment contexts |
| 10    | LocalOverlayProvider + local BM25 search index                                                    | Workspace `local` layer with discoverable iteration loop      |
| 11    | HarnessAdapter implementations: `none`, `claude-code`, `claude-desktop`, `cursor`, `gemini`, `opencode`, `codex` + conformance test suite | Author once, load anywhere     |
| 12    | Authoring CLI: `podium publish`, `podium lint`, `podium materialize`, `podium search-explain`, `podium review *`, `podium cache prune`, `podium rbac …`, `podium whoami`, `podium verify` | Operator + author surface |
| 13    | Registry audit log + LocalAuditSink + cross-stream correlation + retention + hash-chain integrity | Observability + governance                                     |
| 14    | Vulnerability tracking + SBOM ingestion + notification provider                                    | Enterprise governance                                          |
| 15    | Deployment: Helm chart, reference Grafana dashboard, runbook                                      | Operability                                                    |
| 16    | Example artifact registry (multi-domain, with `DOMAIN.md` imports, unlisted folders, overlays, RBAC bindings, approval policies, signatures) | Prove end-to-end |

---

## 11. Verification

- **Unit tests**: registry HTTP handlers, overlay resolver, RBAC evaluator, `DOMAIN.md` parser and glob resolver, publish lint, manifest schema validator, MCP server forwarder, local overlay watcher and merge, content-addressed cache, atomic materialization, OAuth keychain integration, identity provider implementations, signature verification, hash-chain audit, approval-policy state machine, freeze-window enforcement.

- **Managed-runtime integration test**: spawn the MCP server with `PODIUM_IDENTITY_PROVIDER=injected-session-token`, supply a stub signed JWT, exercise the meta-tool round-trip against a real registry, verify identity flows through and overlays resolve correctly per scope claims; verify rejection on unsigned token (`auth.untrusted_runtime`).

- **Developer-host integration test**: spawn the MCP server with `PODIUM_IDENTITY_PROVIDER=oauth-device-code` and `PODIUM_OVERLAY_PATH=${WORKSPACE}/.podium/overlay/`, complete the device-code flow via MCP elicitation, exercise the meta-tool round-trip, verify the `local` overlay overrides registry-side artifacts and that hashes are exposed in `load_domain`.

- **Local search test**: `search_artifacts` returns local-overlay artifacts merged with registry results via RRF; removing the local file removes the artifact from search.

- **Local overlay precedence test**: confirm the `local` layer overrides `user:<id>` (and earlier layers) for a synthetic conflicting artifact, and that removing the overlay file restores the registry-side artifact.

- **Domain composition tests**: `DOMAIN.md` `include:` patterns surface matching artifacts; recursive `**` and brace `{a,b}` patterns resolve correctly; `exclude:` removes paths; `unlisted: true` removes a folder and its subtree from `load_domain` enumeration; `DOMAIN.md` from multiple overlays merges per §4.5.4; remote-vs-local glob resolution asymmetry is correct.

- **Cross-overlay import tests**: a `DOMAIN.md` published in one overlay imports an artifact published in another; a host with both scopes sees the imported artifact; a host with only the destination overlay sees nothing for that import; imports that don't currently resolve produce a publish-time warning, not an error.

- **Materialization test**: exercise `load_artifact` against artifacts with diverse bundled file types (Python script, Jinja template, JSON schema, binary blob, external resource); verify atomic write semantics; verify partial-download recovery; verify presigned URL refresh on expiry.

- **Signing test**: artifact signed at publish; signature verified on materialization; tampered content rejected with `materialize.signature_invalid`; `podium verify <id>` matches.

- **Versioning tests**: pinned `<id>@<semver>` resolves exactly; `<id>@<semver>.x` resolves to highest matching; `<id>` resolves to `latest`; `session_id`-tagged calls return consistent `latest` resolution within the session; CAS publish conflicts return `publish.conflict`; `extends:` parent version pinned at child publish time.

- **Harness adapter conformance suite**: for each built-in adapter, load a canonical fixture, produce harness-native output, install into a fresh harness instance, verify the harness can spawn an agent that uses the materialized artifact end-to-end. Includes negative tests for adapter sandbox contract (no network, no out-of-destination writes, no subprocess).

- **Adapter switching test**: the same MCP server binary, started with each `PODIUM_HARNESS` value, passes the conformance suite without recompilation. Per-call `harness:` overrides materialize a single artifact in a different format than the server's default.

- **Identity provider switching test**: the same MCP server binary, started with each identity provider, passes both integration tests above without recompilation.

- **RBAC tests**: a reader can list and load but not publish; a publisher cannot deprecate; an admin can change bindings; org-level bindings apply to every team; bindings outside an identity's scope are ignored; denials produce structured errors and audit events. Editing a `DOMAIN.md` requires publisher rights in the destination domain; importing into a `DOMAIN.md` does not require any rights in the source path. `approval_policy` parallel and sequential stages enforced; freeze-window blocks publish; break-glass requires dual-signoff and justification.

- **Publish + review workflow test**: a publisher creates a draft; a reviewer approves; the artifact appears in `search_artifacts` for downstream hosts; the audit trail contains both events.

- **Failure-mode tests**: registry offline (cache serves; explicit "offline" status on miss), overlay path missing (skip with warning), token expired under each identity provider, materialization destination unwritable, MCP protocol version mismatch, untrusted runtime, signature failure, runtime requirement unsatisfiable.

- **Security tests**: a host without a scope claim or RBAC role sees nothing in the corresponding layer; the MCP server requires OAuth-attested identity to reach the registry; redaction directives propagate to the registry audit stream and the local audit log; tokens stay in the OS keychain (or in the runtime-managed location for injected tokens); query text PII is scrubbed before audit; audit hash chain detects gaps; sandbox profile honored or refused per host capability.

- **Performance tests**: 1K QPS sustained for `search_artifacts`; 100 publish/min; `load_artifact` p99 < SLO targets in §7.1; cold-cache vs warm-cache materialization budgets.

- **Soak tests**: 24h continuous load with mixed workload; no memory growth, no descriptor leaks, audit log integrity preserved across restarts.

- **Chaos tests**: Postgres failover during load, object-storage stalls, network partitions between MCP server and registry, IdP outage during refresh, full-disk on registry node.

- **Example artifact registry**: multi-domain demo with diverse types (skill, agent, context, prompt, mcp-server), diverse bundled file types, layered overlays (org / multiple teams / optional user / local), RBAC bindings exercising every built-in role, signed artifacts at multiple sensitivities.

---

## 12. Key Risks and Mitigations

| Risk                                                   | Mitigation                                                                                                                                                                                                             |
| ------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Catalog grows too large for `load_domain` to be useful | Two-level hierarchy default; directory layout drives subdomain structure (§4.2, §4.5); domain owners curate cross-cutting views via `DOMAIN.md include:`; learn-from-usage reranking surfaces signal-based ordering. |
| Prompt injection via artifact manifests                | Content provenance markers (§4.4.2) enable differential trust; adapters propagate to harness-native trust regions where supported. RBAC restricts who can publish; classification + review status gate visibility.    |
| Bundled-script supply chain                            | SBOM at publish; signature verification on materialization (§4.7.9); sandbox profile (§4.4.1); secret scanning + static analysis in publish-time CI.                                                                  |
| Registry latency on every meta-tool call               | HTTP/2 keep-alive between MCP server and registry; ETag caching of immutable artifact versions; manifest body inline; content-addressed disk cache shared across workspaces; explicit p99 budgets (§7.1).             |
| Manifest description quality                           | Publish-time lint flags thin descriptions and clusters of artifacts with colliding summaries. Learn-from-usage reranking surfaces underperforming descriptions.                                                       |
| Local overlay tampering                                | The local overlay is intended for the developer's own workspace iteration. Hosts that need tamper-evident behavior pin to registry-side versions and leave `PODIUM_OVERLAY_PATH` unset.                              |
| Registry as a single point of failure for hosts        | The cache and `offline-first` mode let cached artifacts continue to work during transient outages. Fresh `load_domain` / `search_artifacts` returns an explicit "offline" status that hosts can surface.              |
| Type system extensibility / per-type lint rule drift   | Type definitions are SPI plugins compiled into the registry binary; deployments pin a registry version. Future runtime-registered types deferred to post-v1.                                                          |
| RBAC misconfiguration                                  | Built-in roles cover most cases; admin actions are audited; the registry refuses to start with structurally invalid binding tables; `podium rbac …` CLI surfaces effective permissions for any identity.              |
| Identity provider misconfiguration                     | The MCP server validates its identity-provider configuration at startup and refuses to start with an obviously broken combination. Each provider documents the env vars it requires. Untrusted runtimes rejected at the registry. |
| Bundled resource bloat                                 | Per-package and per-file size lints at publish time; soft cap is configurable; large data uses `external_resources:` (§4.3) instead of inline bundling.                                                              |
| Recursive globs in `DOMAIN.md` are expensive           | Glob expansion is cached server-side per artifact-version snapshot; cache invalidation is keyed on publish events. Lint warns on overly broad recursive globs.                                                        |
| `DOMAIN.md` imports go stale silently                  | Publish-time lint warns on imports that don't currently resolve in any visible view. Learn-from-usage signal surfaces domains whose imports return empty results frequently.                                          |
| `unlisted: true` accidentally hides artifacts          | The flag is opt-in (default `false`); the publish lint flags newly-set `unlisted: true` for review. `search_artifacts` continues to surface unlisted artifacts unless `search_visibility: direct-only`.              |
| Harness adapter drift                                  | Adapters are versioned with the MCP server binary; profiles can pin a minimum version. Conformance suite runs against every adapter on every release. Authors who hit drift can fall back to `harness: none`.        |
| Canonical artifact uses a feature an adapter cannot translate | Capability matrix (§6.7.1); publish-time lint surfaces mismatches; `target_harnesses:` opt-out; adapter returns structured error from `load_artifact`.                                                                |
| Adapter sprawl across many harnesses                  | Adapters carry no agent or registry logic; mechanical translators with a shared core. Conformance suite gates merges. Sandbox contract enforced.                                                                      |
| Vulnerability in a bundled dependency                 | SBOM at publish; CVE feed ingested by registry; affected artifacts surfaced via `podium vuln list`; owners notified through configured channels.                                                                      |
| Token leakage in `injected-session-token`             | Runtime owns env-var/file lifecycle; ≤15 min token TTLs recommended; `PODIUM_SESSION_TOKEN_FILE` over env var when possible; runtime trust model rejects unsigned tokens.                                              |
| Audit tampering                                       | Hash-chained audit (§8.6); periodic transparency-log anchoring recommended; SIEM mirroring as operational backstop.                                                                                                   |

---

## 13. Deployment

### 13.1 Reference Topology

- **Stateless front-end:** 3+ replicas behind a load balancer (HTTP).
- **Postgres:** managed (RDS, Cloud SQL, Aurora) or self-run; primary + read replicas.
- **Object storage:** S3-compatible (S3, GCS, MinIO, R2).
- **Helm chart** shipped with v1.0; bare-metal deployment guide alongside.

For non-prod or solo use: `podium serve --solo` ships in v1.0 — single binary, embedded SQLite, filesystem object storage, no auth, no overlays.

### 13.2 Runbook

Coverage for: Postgres failover, object-storage outage, IdP outage, full-disk on registry node, audit-stream backpressure, runaway search QPS, signature verification failure storm. Each scenario gets detection signals, impact, and mitigation steps; full runbook ships with the Helm chart.

### 13.3 Backup and Restore

- Postgres: logical + physical backups; point-in-time recovery.
- Object storage: cross-region replication or snapshots.
- Consistent restore via PITR + object-storage version history.
- Default RPO 1h / RTO 4h.

### 13.4 Migrations

Schema migrations bundled in the registry binary; expand-contract pattern for online migrations. Type-system migrations versioned alongside the binary.

### 13.5 Multi-Region

v1 is single-region per deployment. Cross-region read replicas via Postgres logical replication and object-storage replication; writes route to the primary region. Cross-region federation (signed catalog summaries flowing between region-pinned registries) is post-v1.

### 13.6 Sizing

Baseline: 10K artifacts, 100 QPS, 1 GB Postgres, 500 GB object storage handles a typical mid-sized org on a 3-replica deployment + db.m5.large equivalent.

Scale guidance:

- 100K artifacts: pgvector scale; consider sharding embeddings.
- 1K QPS: scale front-end replicas; CDN in front of object storage.
- 10K QPS: review search query patterns; consider dedicated Elasticsearch for BM25.

### 13.7 CDN

Presigned URLs are CDN-friendly. Recommend CloudFront / Fastly / Cloudflare in front of object storage for hot artifacts. Cache headers safe because content_hash keys are immutable.

### 13.8 Observability

- **Metrics.** Prometheus endpoint on registry and MCP server. Histograms for latency; counters for cache hit rate, error rate, RBAC denial rate; gauges for queue depths.
- **Tracing.** OpenTelemetry trace export. W3C Trace Context propagation across all calls. One root span per `load_domain` / `search_artifacts` / `load_artifact`; child spans for registry round-trip, object-storage fetch, adapter translation, materialization.
- **Reference Grafana dashboard** shipped with v1.0.

### 13.9 Health and Readiness

- Registry: `/healthz` (liveness) and `/readyz` (readiness — Postgres + object-storage reachable).
- MCP server: `health` MCP tool returning registry connectivity + cache size + last successful call timestamp.

---

## Glossary

- **Artifact** — a packaged authoring unit (skill, agent, prompt, context, MCP-server registration, or extension type). Distinct from "build artifact" or "ML artifact."
- **Canonical artifact ID** — the directory path under the registry root (e.g., `finance/ap/pay-invoice`). All references use this ID, optionally suffixed with `@<semver>` or `@sha256:<hash>`.
- **Domain** — a node in the catalog hierarchy. Distinct from DNS domain or DDD domain.
- **Effective view** — the union of layers a host's identity entitles them to, after RBAC filtering and overlay composition.
- **Harness** — the AI runtime hosting an agent (Claude Code, Cursor, Codex, etc.). Used interchangeably with "host" when the runtime context matters.
- **Host** — the MCP-speaking system that runs the Podium MCP server alongside its own runtime.
- **Layer** — a unit of composition (org / team / user / local).
- **Local overlay** — the workspace-scoped layer sourced from `${PODIUM_OVERLAY_PATH}` by the MCP server's `LocalOverlayProvider`.
- **Manifest** — the `ARTIFACT.md` file specifically.
- **Materialization** — atomic write of a loaded artifact's content (manifest + bundled resources, after harness adapter translation) onto the host's filesystem.
- **MCP server (Podium MCP server)** — the in-process bridge binary the host runs.
- **Overlay** — the contents authored under a layer.
- **Package** — the on-disk directory containing an artifact's `ARTIFACT.md` and bundled resources.
- **Registry** — the centralized service that stores and serves the catalog.
- **Scope** — the OAuth claim that grants access to a layer (`org`, `team:<org_id>/<team_name>`, `user:<user_id>`).
- **Session ID** — optional UUID generated by the host per agent session; used by the registry for `latest`-resolution consistency and learn-from-usage reranking.
