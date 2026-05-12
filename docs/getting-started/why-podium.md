---
layout: default
title: Why Podium
parent: Getting Started
nav_order: 1
description: "What Podium does, when it applies, when a simpler alternative is enough, and how it compares to adjacent products."
---

# Why Podium

## What Podium does

Podium catalogs authored AI agent know-how and delivers it into the
harnesses people use. Authored content includes skills (repeatable
procedures), agents (delegated workers), contexts (reference material),
commands (parameterized prompts), rules (policy), hooks (lifecycle
observers), and MCP server registrations. Harnesses include
developer-facing tools such as Claude Code, Cursor, Codex, Gemini CLI,
and OpenCode; chat clients such as Claude Desktop; and any host with a
published adapter.

Podium differs from adjacent products in these areas:

- **Cross-harness delivery.** A pluggable harness adapter translates the canonical artifact format into Claude Code, Claude Desktop, Claude Cowork, Cursor, Codex, Gemini CLI, OpenCode, Pi, Hermes, or a custom runtime. The full roster with documentation links is in [Configure your harness](../consuming/configure-your-harness#supported-harnesses).
- **Domain-organized catalog.** Folders and subfolders define the domain hierarchy under one canonical tree.
- **Selective materialization.** `podium sync` materializes the subset of the catalog declared by a profile, instead of the whole tree.
- **Layered composition.** Multiple sources compose into one effective view with deterministic merge, explicit precedence, and `extends:`-based inheritance. Requires the registry server.
- **Per-layer visibility.** Each layer is `public`, organization-wide, scoped to OIDC `groups`, or restricted to specific `users`. Requires the registry server.
- **Progressive discovery.** Meta-tools traverse domains, search artifacts, and load artifacts on demand. Requires the MCP server or SDK.
- **Lazy materialization.** Bundled resource bytes land on disk when the artifact is loaded, instead of at session start. Requires the MCP server or SDK.

## Boundaries

Podium catalogs authored artifacts and delivers them to harnesses. LLM
application development platforms, evaluation frameworks, observability
products, prompt-version-as-program-artifact tools, and agent runtimes solve
different problems.

---

## When Podium applies

Podium becomes valuable as any of these dimensions grow:

- **Catalog size.** Lazy discovery and per-domain navigation handle catalogs that exceed what fits in a system prompt.
- **Cross-harness delivery.** A canonical artifact format pays off as soon as a team targets more than one harness.
- **Multiple artifact types.** A dependency graph across skills, agents, contexts, commands, rules, hooks, and MCP server registrations covers cross-type edges (`extends:`, `delegates_to:`, `mcpServers:`) that type-specific stores do not model.
- **Multiple contributors and audiences.** Per-layer visibility, classification, and audit address contributor and audience diversity.
- **Audiences beyond engineering.** The same catalog feeds developers in coding harnesses and non-developers in desktop chat clients.

## When a simpler alternative is enough

A solo author with a handful of skills in one harness does not benefit from Podium. A flat directory with the harness's native conventions handles that case. The minimum viable alternative is a short script that watches a Git repository and copies files into harness-specific directories. A single-team, single-type, single-vendor shop can use that script for a fraction of the engineering effort.

Podium addresses the intersection of multiple types, multiple teams, multiple harnesses, and governance requirements. Below that intersection, file-copy is sufficient.

---

## How Podium compares

The adjacent landscape splits into categories. The comparisons below cover products that catalog or distribute authored AI agent artifacts into the harnesses an organization runs. LLM application development frameworks, evaluation suites, observability and tracing products, prompt-version-as-program-artifact tools, RAG-over-corpora knowledge bases, and agent runtimes solve different problems and are out of scope.

### Catalogs of AI agent artifacts

The products in this section are the closest direct comparisons. All ship registries for SKILL.md or related authored content; the differences are scope, deployment, governance, and license.

| Product | License | Hosting | Artifact types | Layered composition | Per-layer visibility |
|:--|:--|:--|:--|:--|:--|
| **Vercel skills.sh** ([skills.sh](https://skills.sh/)) | OSS Apache; registry is SaaS | Public registry; `npx skills add <pkg>` | SKILL.md packages | No | Public-only |
| **iFlytek SkillHub** ([repo](https://github.com/iflytek/skillhub)) | OSS | Self-hosted (Docker / Kubernetes) | SKILL.md packages | No | Namespace + RBAC; visibility flags per package |
| **SkillReg** ([skillreg.dev](https://skillreg.dev/)) | Closed commercial; SaaS-only | SaaS | SKILL.md packages | No | Org-scoped roles |
| **Tessl** ([tessl.io](https://tessl.io/registry)) | Closed commercial; SaaS-only | SaaS | Skills, docs, rules, and commands bundled in tiles | No | Workspace-scoped private or public |
| **Continue Hub** ([continue.dev/hub](https://www.continue.dev/hub)) | Closed SaaS hub; Continue extension is Apache 2.0 | SaaS hub plus `.continue/` in-repo | Assistants, agents, rules, MCP servers, prompts, models | No | Account or org sharing |

**Where each applies.** skills.sh fits public-content discovery. SkillHub fits an on-prem-only shop wanting a SKILL.md-only registry. SkillReg fits a SaaS-acceptable shop wanting registry-side approval workflows. Tessl fits a shop that wants registry-side evaluation of skills against agent task outcomes. Continue Hub fits a shop standardized on the Continue extension.

**Where Podium applies.** Podium applies when the catalog needs type heterogeneity beyond what each product holds, ordered-layer composition with `extends:`, per-OIDC-group visibility composed across multiple sources at request time, cross-type dependency edges, or MIT-licensed self-hosted deployment alongside filesystem and standalone modes.

### Single-vendor private marketplaces and team-rule systems

Single-harness organizations get a shared catalog from the harness vendor's own enterprise tier. Podium delivers into these vendors via harness adapters. The comparison is the boundary at which a multi-harness organization stops fitting in any single one of them.

| Product | Coverage | Where it applies | Where Podium applies |
|:--|:--|:--|:--|
| **Anthropic Cowork private plugin marketplaces** | Claude Code and Claude Cowork; org-private GitHub-hosted marketplace; per-user provisioning, auto-install, and audit | Claude-only shop; managed by Anthropic | Multi-harness shop; on-prem deployment; per-OIDC-group visibility composed across multiple sources |
| **Cursor Team Rules** | Cursor only; rules only; recommend / require flags from a cloud dashboard | Cursor-only shop; rules-only catalog | Multi-harness; multi-type catalog; on-prem deployment |
| **AWS Bedrock Registry with AgentCore** | AWS-managed; Kiro resources; AgentCore harness; cross-harness skill rollout announced | AWS-tied shop; managed runtime | Multi-vendor; MIT-licensed; on-prem deployment |
| **Per-harness skill systems** (OpenAI Codex Skills, GitHub Copilot custom instructions, Goose, Roo Code, Windsurf, Trae, Amp, Factory) | Single harness; each surface manages its own catalog | One-harness shop | Cross-harness delivery via the adapter set |

### Cross-harness skill installers and conventions

These tools translate one source SKILL.md into many harness-native locations on a developer's machine without a server. They operate per-machine; they do not compose multiple sources or apply per-OIDC-group visibility at request time.

| Tool or pattern | What it does |
|:--|:--|
| **agent-skill-creator** ([repo](https://github.com/FrancyJGLisboa/agent-skill-creator)) | Generates a SKILL.md plus an `install.sh` that targets multiple harnesses. |
| **AGENTS.md plus sync hook** ([AGENTS.md spec](https://www.harness.io/blog/the-agent-native-repo-why-agents-md-is-the-new-standard)) | A single repo-rooted file plus a pre-commit hook copies it into `.cursorrules`, `CLAUDE.md`, `.windsurfrules`, `.github/copilot-instructions.md`, and similar. |
| **Curated GitHub-hosted skill libraries** (VoltAgent awesome-agent-skills, alirezarezvani/claude-skills, tech-leads-club/agent-skills, harness/harness-skills) | `git clone` distribution. |

**Where they apply.** These tools apply to a single team, a single type, and modest scale. The overhead of a registry server is not justified.

**Where Podium applies.** Podium applies when multiple sources compose into one effective view, per-OIDC-group visibility is required, cross-type dependency edges matter, or the catalog spans multiple types.

### MCP server registries and gateways

These overlap with Podium's `mcp-server` registered extension type and with the governance plane around MCP. Their scope is restricted to MCP server registrations.

| Product | License | Coverage |
|:--|:--|:--|
| **Kong MCP Registry / Gateway** ([konghq.com](https://konghq.com/products/mcp-registry)) | Commercial | Discovery, OAuth, observability, per-tool governance |
| **TrueFoundry MCP Gateway** ([truefoundry.com](https://www.truefoundry.com/mcp-gateway)) | Commercial | MCP catalog, OAuth, federated identity, request tracing |
| **agentic-community/mcp-gateway-registry** ([repo](https://github.com/agentic-community/mcp-gateway-registry)) | OSS | Keycloak / Entra OAuth, dynamic discovery, allowlist |
| **MACH Alliance MCP Registry, Docker MCP Catalog, GitHub VS Code internal MCP registry, ServiceNow MCP Registry** | Industry- or vendor-curated | Curated catalogs scoped to a platform |

These coexist with Podium in a deployment. An MCP gateway brokers transport and runtime auth for MCP servers; Podium catalogs the registrations and the rest of the artifact graph under one governance plane.

### Git monorepo with per-harness directory layout

A Git monorepo with per-harness directories is the original baseline.

**Where it applies.** This model applies to a single team, a single harness, one or two artifact types, and no formal governance requirements. It requires no additional infrastructure beyond an existing Git provider.

**Where Podium applies.** Podium applies when multi-source composition with deterministic merge across multiple Git repositories, per-layer visibility for cross-team catalogs, cross-type dependency-aware impact analysis, or lazy discovery at scale is required.

---

## Project model

- **License.** MIT.
- **Governance.** Maintainer model with an RFC process for spec changes. See [Governance](governance).
- **Distribution.** OSS-first development, with an optional commercial managed offering by the sponsoring entity (separate doc).
- **Public registry.** A reference registry with curated example artifacts is hosted at the project's public URL.
- **Multi-vendor neutrality.** The project does not adopt contributions, governance changes, or roadmap pressure that would bind it to a single harness vendor's surface.
- **Standards engagement.** Where adjacent open standards (MCP, AAIF-governed standards) overlap with Podium's concerns, the project participates upstream and harmonizes wherever doing so does not compromise the broader scope across artifact types.
