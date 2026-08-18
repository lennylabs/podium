---
title: Podium
nav_title: Overview
nav_order: 0
description: A catalog for reusable AI agent artifacts, with tools that translate those artifacts into harness-specific formats.
actions:
  - label: Quickstart
    href: getting-started/quickstart
  - label: Concepts
    href: getting-started/concepts
  - label: Fit and comparisons
    href: getting-started/why-podium
  - label: Compare deployment setups
    href: deployment/
---

# Podium

**A catalog for reusable AI agent artifacts, with tools that translate those artifacts into harness-specific formats.**

Podium stores skills, agents, commands, rules, hooks, contexts, and MCP
server registrations as portable artifacts. A developer can keep a local
filesystem catalog and run `podium sync` to write harness-native files into
a workspace. A team can put the same artifacts behind a registry server for
runtime discovery, identity-aware visibility, audit, and shared governance.
In server mode, teams usually keep the catalog in one or more Git
repositories; the registry ingests those tracked refs and builds the
effective catalog it serves.

> [!NOTE]
> **Status: 0.1.x, early release.** The CLI, server, MCP bridge, and SDKs
> are published. Install with `brew tap lennylabs/tap && brew install podium`
> (macOS / Linux) or `scoop bucket add lennylabs https://github.com/lennylabs/scoop-bucket && scoop install podium` (Windows).
> See [Implementation status](about/status) for what's shipped and what's
> still on the roadmap to 1.0.

Podium can run from a filesystem catalog or from a registry server:

- **Filesystem catalog**: file-based artifacts plus the Podium CLI. This
  mode fits any team or individual whose catalog does not require access
  control or progressive disclosure: solo work, prototypes, CI, and
  Git-shared catalogs.
- **Registry server**: artifacts in one or more Git repositories, plus the
  Podium server, CLI, MCP server, and SDKs. Git stores catalog history and
  review flow; the registry ingests the configured refs and composes the
  effective catalog. This mode adds runtime discovery, identity-aware
  visibility, audit, and server-side composition.

Highlights:

- **Cross-harness delivery.** Pluggable harness adapters translate canonical artifacts into Claude Code, Claude Desktop, Claude Cowork, Cursor, Codex, Gemini CLI, OpenCode, Pi, Hermes, or a custom runtime. The adapter roster with documentation links is in [Configure your harness](consuming/configure-your-harness#supported-harnesses).
- **Artifact organization based on domains and subdomains.** Keep artifacts organized in folders and subfolders, where each folder defines a domain.
- **Selective materialization.** Sync a subset of the catalog into a workspace. Define profiles to quickly switch between scopes.
- **Layered composition.** Compose the catalog from multiple sources with deterministic merge and explicit precedence. (Requires the Podium registry server.)
- **Per-layer visibility.** Declare who can see what: each layer can be `public`, organization-wide, scoped to OIDC `groups`, or restricted to specific `users`. (Requires the Podium registry server.)
- **Agent-driven progressive discovery.** Discovery tools for traversing domains and searching artifacts. (Requires the Podium MCP server or SDK.)
- **Lazy artifact loading.** Materialize artifact files into the workspace as they are loaded. (Requires the Podium MCP server or SDK.)

---

## 'Hello world' example

The commands below describe the target v1 CLI flow.

Create a skill directory with a `SKILL.md` file for agent-facing
instructions and an `ARTIFACT.md` file for Podium metadata:

```markdown
~/podium-artifacts/personal/hello/greet/SKILL.md

---
name: greet
description: Greet the user by name and tell them today's date.
---

Greet the user by their first name. Tell them today's date.
```

```markdown
~/podium-artifacts/personal/hello/greet/ARTIFACT.md

---
type: skill
version: 1.0.0
tags: [demo, hello-world]
---

<!-- Skill body lives in SKILL.md. -->
```

Point Podium at the directory and set the harness:

```bash
cd workspace
podium init --registry ~/podium-artifacts/ --harness claude-code
podium sync
```

Open Claude Code in the project. Claude Code can discover the materialized
skill in its native location.

The [full quickstart](getting-started/quickstart) covers the same flow with
prerequisites and verification steps.

---

## Pick your entry point

- [Authoring guide](authoring/): author skills, commands, rules, and agents.
- [Consuming guide](consuming/): configure Claude Code, Cursor, OpenCode, or
  another harness to consume Podium artifacts.
- [Deployment guide](deployment/): select a deployment mode and migrate as the
  catalog grows.
- [Reference](reference/): build a runtime, an eval pipeline, or custom tooling
  against Podium directly.

---

## Quick links

- [Quickstart](getting-started/quickstart)
- [Concepts](getting-started/concepts)
- [How it works](getting-started/how-it-works)
- [Podium on GitHub](https://github.com/lennylabs/podium)
