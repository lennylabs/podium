---
title: Why Podium
nav_order: 1
description: "One catalog, every harness: what that claim means feature by feature, when Podium applies, when a simpler alternative is enough, and how it compares to adjacent products."
---

# Why Podium

## One catalog, every harness

Podium holds reusable AI agent artifacts in one catalog and translates them into
the layout each runtime expects. An author writes an artifact once in the
canonical format. A Claude Code workspace, a Cursor workspace, and a published
plugin marketplace each receive their own translation of that same artifact,
with no per-harness copy in the catalog.

An artifact is a directory, and the directory carries its dependencies. A skill
that ships `scripts/verify_revision.py` alongside its `SKILL.md` delivers that
script into every layout it reaches, workspace and marketplace alike. Selecting
the artifact selects its files. See
[Bundled resources](../authoring/bundled-resources) for what an artifact
directory may hold.

Authored content includes skills (repeatable procedures), agents (delegated
workers), contexts (reference material), commands (parameterized prompts), rules
(policy), hooks (lifecycle observers), and MCP server registrations. Harnesses
include developer-facing tools such as Claude Code, Cursor, Codex, Gemini CLI,
and OpenCode, chat clients such as Claude Desktop, and any host with a published
adapter.

---

## The features behind the claim

Each feature below has one page that covers it in operational detail.

### Cross-harness delivery

Write canonical artifacts once and let Podium translate them into the format the
runtime expects. A harness adapter maps the canonical artifact onto Claude Code,
Claude Desktop, Claude Cowork, Cursor, Codex, Gemini CLI, OpenCode, Pi, Hermes,
or a custom runtime, and decides the on-disk destination for each artifact type.
Adding a harness to a team means adding an adapter to a sync configuration.

Covered in [Configure your harness](../consuming/configure-your-harness#supported-harnesses).

### Domains and subdomains

The directory layout defines the domain hierarchy. `finance` is a domain,
`finance/ap` is a subdomain, and `finance/ap/pay-invoice` is the canonical ID of
an artifact under it. A domain folder can carry a `DOMAIN.md` that adds a
description, keywords, and featured artifacts. Authors get a place to put a new
artifact, and agents get a tree to walk.

Covered in [Domains](../authoring/domains).

### Selective materialization

A workspace rarely needs the whole catalog. `podium sync` materializes the
subset named by include globs, exclude globs, and artifact types, and a named
profile stores that subset in `sync.yaml` so one command switches between
scopes. A narrowed scope also removes the files a wider previous run wrote.

Covered in [Selective materialization](../consuming/selective-materialization).

### Progressive discovery

An agent that speaks MCP starts with an empty context window and a small set of
meta-tools. It traverses domains with `load_domain`, finds candidates with
`search_domains` and `search_artifacts`, and calls `load_artifact` on the one it
picked. Only that last call materializes anything, and it materializes the
artifact's bundled files at the same time. A catalog larger than any system
prompt stays usable because the agent pays per artifact it actually loads. This
path requires a registry server, reached through the MCP server or an SDK.

Covered in [Browsing the catalog](../consuming/browsing-the-catalog).

### Layered composition

One catalog can be assembled from several independent sources. The layers
compose in a declared order with deterministic merge. Two layers that
contribute the same artifact ID are a collision, which Podium rejects. A
catalog on disk composes its
subdirectories as ordered layers through a `.registry-config` file. A registry
server adds registered layers, remote Git sources, custom sources through the
`LayerSourceProvider` SPI, and per-layer visibility.

Covered in [Layered composition](../deployment/layers).

### Access control

Each layer declares who can see it: everyone, every authenticated user in the
organization, the members of named OIDC groups, or named users. The registry
evaluates visibility on every call and composes the caller's effective view from
the layers that pass. This requires a registry server with an identity provider
configured.

Covered in [Access control](../deployment/access-control).

---

## Boundaries

Podium catalogs authored artifacts and delivers them to harnesses. LLM
application development platforms, evaluation frameworks, observability
products, prompt-version-as-program-artifact tools, and agent runtimes solve
different problems.

---

## When Podium applies

Podium becomes valuable as any of these dimensions grow:

- **Catalog size.** Progressive discovery and per-domain navigation handle catalogs that exceed what fits in a system prompt.
- **Cross-harness delivery.** A canonical artifact format pays off as soon as a team targets more than one harness.
- **Multiple artifact types.** A dependency graph across skills, agents, contexts, commands, rules, hooks, and MCP server registrations covers cross-type edges (`delegates_to:` and `mcpServers:`) that type-specific stores do not model.
- **Multiple contributors and audiences.** Per-layer access control, classification, and audit address contributor and audience diversity.
- **Audiences beyond engineering.** The same catalog feeds developers in coding harnesses and non-developers in desktop chat clients.

## When a simpler alternative is enough

A solo author with a handful of skills in one harness does not benefit from Podium. A flat directory with the harness's native conventions handles that case. The minimum viable alternative is a short script that watches a Git repository and copies files into harness-specific directories. A single-team, single-type, single-vendor shop can use that script for a fraction of the engineering effort.

Podium addresses the intersection of multiple types, multiple teams, multiple harnesses, and governance requirements. Below that intersection, file-copy is sufficient.

---

## How Podium compares

The adjacent landscape splits into categories. The comparisons below cover products that catalog or distribute authored AI agent artifacts into the harnesses an organization runs. LLM application development frameworks, evaluation suites, observability and tracing products, prompt-version-as-program-artifact tools, RAG-over-corpora knowledge bases, and agent runtimes solve different problems and are out of scope. Product details are current as of November 2026.

### Self-hosted and managed registries

These products store and govern agent artifacts centrally. They differ in artifact scope, delivery into harnesses, composition, and hosting.

| Product | License and hosting | Artifact types | Delivery into harnesses | Composition across sources | Access control |
|:--|:--|:--|:--|:--|:--|
| **iFlytek SkillHub** ([repo](https://github.com/iflytek/skillhub)) | Apache 2.0; self-hosted | Skills | CLI download and install | None | Team namespaces with roles; review before promotion to global scope |
| **agentregistry** ([repo](https://github.com/agentregistry-dev/agentregistry)) | Apache 2.0; self-hosted | MCP servers, agents, skills, and prompts | Generated client configuration; deployment to Kubernetes; MCP routing through agentgateway | None | Curation and publishing controls |
| **AWS Agent Registry** ([announcement](https://aws.amazon.com/about-aws/whats-new/2026/08/aws-agent-registry-generally-available/)) | Managed (AWS) | MCP servers, A2A agent cards, skills, and custom records | Discovery from MCP-compatible IDEs | None | IAM; approval workflow; approved-only discovery |
| **Google Cloud Agent Registry** ([docs](https://docs.cloud.google.com/agent-registry/concepts)) | Managed (Google Cloud) | Agents, MCP servers, and skills (skill governance in preview) | Discovery through the registry's search API | None | Skill governance (preview); vulnerability scanning of skills |
| **JFrog AI Catalog** ([skills registry](https://jfrog.com/ai-catalog/skills-registry/)) | Commercial; SaaS or self-hosted | Skills, plugins, MCP servers, and models | CLI and MCP | None | Policy-driven approval; scanning; signing of approved skills |
| **Tessl** ([tessl.io](https://tessl.io/registry)) | Commercial; SaaS | Skills, docs, rules, and commands in tiles | Package manager; MCP tools to install | None | Workspace-scoped; evaluation of skill effect |
| **Podium** | MIT; self-hosted in the local, single-node, or clustered deployment mode | Skills, agents, contexts, commands, rules, hooks, MCP server registrations, and extension types | Native project files through the built-in adapters, for each type a harness's project-level surface carries; marketplace repositories; MCP discovery tools; SDKs | Ordered layers | Per-OIDC-group and per-user visibility evaluated per request; SCIM; multi-tenant mode |

**Where each applies.**

- SkillHub fits an organization that wants a self-hosted skill registry with team namespaces and a review step.
- agentregistry fits a Kubernetes platform team that deploys MCP servers and agents and wants a catalog beside them.
- AWS Agent Registry and Google Cloud Agent Registry fit an organization standardized on that cloud provider.
- JFrog AI Catalog fits an organization that already governs packages in Artifactory and wants scanning and approval for skills.
- Tessl fits an organization that accepts SaaS and wants evaluation of each skill's effect on agent tasks.

**Where Podium applies.** Podium applies when the catalog holds agents, contexts, commands, rules, and hooks beside skills and MCP server registrations, when ordered layers compose several sources, when visibility is evaluated per OIDC group at request time, when the built-in adapters write native project files for each type a harness's project-level surface carries, when consumers verify the signature of what they receive, or when the registry is self-hosted under the MIT license in any deployment mode. Podium does not scan artifacts. Scanning runs in the CI/CD pipeline of the Git repository that a layer ingests from.

### Harness vendors' private marketplaces

The coding agents below offer plugin bundles and an admin-managed private marketplace. An organization that sanctions one coding agent gets a shared, governed catalog from that vendor.

| Product | Coverage |
|:--|:--|
| **Claude** private plugin marketplaces with managed settings ([docs](https://code.claude.com/docs/en/plugins/org)) | Claude Code, Claude Desktop, and Claude Cowork; admin allowlists of marketplace sources through managed settings |
| **Cursor** team marketplaces ([docs](https://cursor.com/docs/plugins)) | Cursor; a team marketplace imported from a GitHub repository; per-plugin install modes Default Off, Default On, and Required |
| **OpenAI Codex** workspace plugins ([help article](https://help.openai.com/en/articles/20001256)) | Codex and ChatGPT; a public or private GitHub marketplace imported with daily sync; per-role installation policy |
| **GitHub Copilot** enterprise-managed plugins ([changelog](https://github.blog/changelog/2026-05-06-enterprise-managed-plugins-in-github-copilot-cli-are-now-in-public-preview/)) | Copilot CLI; marketplaces defined in the enterprise's `.github-private` repository; installation on sign-in; plugins that bundle agents, hooks, skills, and MCP servers (public preview) |

**Where they apply.** A vendor marketplace fits an organization that sanctions a single coding agent.

**Where Podium applies.** Podium applies when an organization sanctions more than one coding agent and wants one source and one visibility policy across them, or when the catalog must be deployed on-prem. Podium renders marketplace repositories for Claude, Codex, Cursor, Gemini, Pi, and Hermes from the same catalog (§7.8 in the spec). The Claude, Codex, and Cursor marketplaces coexist in one repository, a Gemini extension occupies a repository of its own, and a Hermes tap defaults to a root `skills/` directory and usually takes its own repository. Podium has no GitHub Copilot adapter, so it writes no Copilot custom agents, hooks, or plugin marketplaces.

### Gateways and MCP server registries

These products overlap with Podium's `mcp-server` type and with the governance around MCP. Some gateways also store skills and hand them to coding agents.

| Product | License | Coverage |
|:--|:--|:--|
| **LiteLLM** Skills Gateway ([docs](https://docs.litellm.ai/docs/skills_gateway)) | Open source with a commercial enterprise edition | Imports skills from Git, grants private skills per key or team, and generates a Claude Code marketplace |
| **agentic-community/mcp-gateway-registry** ([repo](https://github.com/agentic-community/mcp-gateway-registry)) | OSS | Registers MCP servers, agents, and skills, with Keycloak or Entra OAuth, dynamic discovery, and scanning at registration |
| **Kong AI Registry** ([konghq.com](https://konghq.com/products/mcp-registry)) | Commercial | Governed discovery of MCP servers, tools, models, agents, and skills |
| **TrueFoundry MCP Gateway** ([truefoundry.com](https://www.truefoundry.com/mcp-gateway)) | Commercial | MCP catalog, OAuth, federated identity, and request tracing |
| **MACH Alliance MCP Registry** ([machalliance.org](https://machalliance.org/mach-alliance-mcp-registry)) | Industry alliance | A vendor-neutral directory of MCP servers that agents and tools query |
| **Docker MCP Catalog** ([hub.docker.com](https://hub.docker.com/mcp)) | Run by Docker, with community submissions | A catalog of containerized MCP servers |
| **GitHub Copilot internal MCP registry** ([docs](https://docs.github.com/en/copilot/how-tos/administer-copilot/manage-mcp-usage/configure-mcp-server-access)) | Hosted by the organization, configured in Copilot policies | An organization's registry of MCP servers for discovery and allowlisting in Copilot clients |
| **ServiceNow MCP Registry** ([docs](https://www.servicenow.com/docs/r/integrate-applications/mcp-registry.html)) | Commercial (ServiceNow application) | A private registry of approved MCP servers |

**Where they apply.** A gateway or MCP registry fits an organization whose catalog holds MCP server registrations alone, or that already routes coding-agent traffic through a gateway.

**Where Podium applies.** Podium applies when agents, contexts, commands, rules, and hooks need the same governance as MCP server registrations, when `mcpServers:` references from skills and agents must resolve to `mcp-server` artifacts as dependency edges, or when ordered layers compose several sources. A gateway and Podium can run together: the gateway brokers model and tool traffic, and Podium catalogs the authored artifacts.

### Cross-harness installers and generators

These tools copy or convert one source into several coding agents' formats on a developer machine or in CI, without a server.

| Tool | What it does |
|:--|:--|
| **`npx skills`** and the **Vercel skills.sh** registry ([skills.sh](https://skills.sh/), [repo](https://github.com/vercel-labs/skills)) | Installs skills from the public skills.sh registry, a Git repository, or a discovery index into many coding agents |
| **`gh skill`** ([changelog](https://github.blog/changelog/2026-04-16-manage-agent-skills-with-github-cli/)) | Searches, installs, pins, and updates skills from GitHub repositories |
| **rulesync** ([repo](https://github.com/dyoshikawa/rulesync)) | Generates rules, commands, MCP configuration, subagents, and skills for many coding agents from one source |
| **agent-skills-platform** ([repo](https://github.com/FrancyJGLisboa/agent-skills-platform)) | Builds tested skills from workflows and publishes them to a Git-hosted team marketplace |

**Where they apply.** These tools fit a single team or a library author with a modest catalog and no per-group visibility requirement.

**Where Podium applies.** Podium applies when several sources compose into one effective view, when visibility is evaluated per OIDC group at request time, when loads must be audited, or when the catalog is too large to install in full and needs lazy discovery through the MCP server.

### Git repository with harness-native directories

A Git repository that holds each harness's native directories is the baseline. A common variant keeps one `AGENTS.md` or rule file and runs a pre-commit hook or CI script that copies it into each harness's location, such as `CLAUDE.md`, `.cursorrules`, `.windsurfrules`, or `.github/copilot-instructions.md`.

**Where it applies.** This model applies to a single team with one or a few harnesses, one or two artifact types, a modest catalog, and no formal governance requirements. It requires no infrastructure beyond Git.

**Where Podium applies.** Podium applies when several repositories compose into one effective view with deterministic merge, when visibility differs per layer across teams, when each artifact type needs translation into the native format of each harness whose project-level surface carries it, when impact analysis must follow dependency edges across types, or when the catalog is too large to load in full and needs lazy discovery.

### Open specifications

Several open specifications define parts of this space. The table states what Podium implements of each.

| Specification | Status | What it defines | What Podium implements |
|:--|:--|:--|:--|
| **Agent Skills** ([specification](https://agentskills.io/specification)) | Specification governed by the Agentic AI Foundation (AAIF) | The `SKILL.md` skill directory format | A `type: skill` artifact carries a `SKILL.md` that follows the specification. Podium-only fields, such as `when_to_use` and `sensitivity`, live in an `ARTIFACT.md` beside it. |
| **AGENTS.md** ([agents.md](https://agents.md/)) | Open convention | Repository-wide instructions for coding agents | The Codex, OpenCode, and Pi adapters inject rule artifacts into `AGENTS.md` between Podium-managed markers. |
| **Agent Plugins 1.0** ([agent-plugins.org](https://agent-plugins.org/)) | Published specification, version 1.0.0 | A portable package format for skills, tools, and other agent components | Podium writes no Agent Plugins packages. |
| **MCP Skills Extension, SEP-2640** ([SEP](https://modelcontextprotocol.io/seps/2640-skills-extension)) | Final MCP extension (`io.modelcontextprotocol/skills`) | Serving skills from MCP servers | The Podium MCP server mirrors artifact bodies as read-only MCP resources and does not implement the skills extension. |
| **Agent Skills discovery index** ([RFC](https://github.com/cloudflare/agent-skills-discovery-rfc)) | Proposal (RFC) | A public `/.well-known/agent-skills/index.json` listing | The registry publishes no discovery index. |

---

## Project model

- **License.** MIT.
- **Governance.** Maintainer model with an RFC process for spec changes. See [Governance](../about/governance).
- **Distribution.** OSS-first development, with an optional commercial managed offering by the sponsoring entity (separate doc).
- **Multi-vendor neutrality.** The project does not adopt contributions, governance changes, or roadmap pressure that would bind it to a single harness vendor's surface.
- **Standards engagement.** Where adjacent open standards (MCP, AAIF-governed standards) overlap with Podium's concerns, the project participates upstream and harmonizes wherever doing so does not compromise the broader scope across artifact types.
