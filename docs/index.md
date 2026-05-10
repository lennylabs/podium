---
layout: default
title: Home
nav_order: 0
permalink: /
description: A catalog for reusable AI agent artifacts, with tools that translate those artifacts into harness-specific formats.
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

{: .note }

> **Status: pre-release.** The initial v1 implementation is on the
> `initial-implementation` branch. No tagged release exists yet; build
> from source via [Contributing](about/contributing#development-setup).
> See [Implementation status](about/status) for the merge-and-release
> roadmap.

[Quickstart](getting-started/quickstart){: .btn .btn-purple }
[Concepts](getting-started/concepts){: .btn .btn-outline }
[Fit and comparisons](about/why-podium){: .btn .btn-outline }

Podium can run from a filesystem catalog or from a registry server:

- **Filesystem catalog**: file-based artifacts plus the Podium CLI. This
  mode fits individual use, prototypes, CI, and small shared repositories.
- **Registry server**: artifacts in one or more Git repositories, plus the
  Podium server, CLI, MCP server, and SDKs. Git stores catalog history and
  review flow; the registry ingests the configured refs and composes the
  effective catalog. This mode adds runtime discovery, identity-aware
  visibility, audit, and server-side composition.

[Compare deployment setups](deployment/){: .btn .btn-outline }

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

[Full quickstart](getting-started/quickstart){: .btn .btn-purple }

---

## Pick your entry point

<div class="grid-cards" markdown="1">

<div class="card" markdown="1">

### Author artifacts

Author skills, commands, rules, and agents.

[Authoring guide](authoring/){: .btn .btn-purple }

</div>

<div class="card" markdown="1">

### Consume artifacts in a harness

Configure Claude Code, Cursor, OpenCode, or another harness to consume
Podium artifacts.

[Consuming guide](consuming/){: .btn .btn-purple }

</div>

<div class="card" markdown="1">

### Set up Podium for a team

Select a deployment mode and migrate as the catalog grows.

[Deployment guide](deployment/){: .btn .btn-purple }

</div>

<div class="card" markdown="1">

### Call the API

Build a runtime, an eval pipeline, or custom tooling against Podium
directly.

[Reference](reference/){: .btn .btn-purple }

</div>

</div>

---

## Quick links

- [Quickstart](getting-started/quickstart){: .btn .btn-outline }
- [Concepts](getting-started/concepts){: .btn .btn-outline }
- [How it works](getting-started/how-it-works){: .btn .btn-outline }
- [GitHub](https://github.com/lennylabs/podium){: .btn .btn-outline }

<style>
.grid-cards {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  gap: 1rem;
  margin-top: 1rem;
}
.card {
  border: 1px solid var(--border-color, #e1e4e8);
  border-radius: 6px;
  padding: 1.25rem;
}
</style>
