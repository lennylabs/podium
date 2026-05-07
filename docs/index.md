---
layout: default
title: Home
nav_order: 0
permalink: /
description: Podium is a registry for the artifacts AI agents use — skills, commands, rules, agents — and a way to deliver them to any harness.
---

# Podium

**A registry for generic agentic AI artifacts, and tools for getting them into any harness.**

Podium lets you:

- Define generic skills, agents, commands, rules, and other artifacts, and use them across any harness.
- Share artifacts with your team and organization.
- Build and organize large catalogs of artifacts and use them efficiently with the help of tools for progressive disclosure and lazy loading.

[Concepts](getting-started/concepts){: .btn .btn-outline }

Podium supports multiple setups to meet the needs of single developers and large organizations alike:

- Individual users: file-based artifacts + Podium CLI
- Small teams: artifacts in repos + Podium CLI
- Large teams/organizations: artifacts in repos + Podium registry server + Podium CLI/MCP server/SDK

[Compare deployment setups](deployment/){: .btn .btn-outline }

Highlights:

- **Author once, deliver anywhere.** Pluggable harness adapters translate canonical artifacts into Claude Code, Claude Desktop, Cursor, OpenCode, Gemini, Codex, Pi, Hermes, or your own runtime.
- **Artifact organization based on domains and subdomains.** Keep artifacts organized in folders and subfolders, where each folder defines a domain.
- **Selective materialization.** Sync only a subset of the catalog into your workspace. Define profiles to quickly switch between scopes.
- **Layered composition.** Compose your catalog from multiple sources with deterministic merge and explicit precedence. (Requires the Podium registry server.)
- **Per-layer visibility.** Declare who can see what — each layer can be `public`, organization-wide, scoped to OIDC `groups`, or restricted to specific `users`. (Requires the Podium registry server.)
- **Agent-driven progressive discovery.** Discovery tools for traversing domains and searching artifacts. (Requires the Podium MCP server or SDK.)
- **Lazy artifact loading.** Materialize artifact files into your workspace as they are loaded. (Requires the Podium MCP server or SDK.)

{: .note }

> **Status: design phase.** The technical specification drives a
> spec- and test-driven implementation. There is no shipped binary
> yet. Design feedback is the most useful contribution today — see
> [Status](about/status) and [Contributing](about/contributing).

---

## 'Hello world' example

Write a skill (one file in a directory):

```markdown
---
type: skill
name: greet
version: 1.0.0
description: Greet the user by name and tell them today's date.
---

Greet the user by their first name. Tell them today's date.
```

Point Podium at the directory and tell it which harness you use:

```bash
cd your_workspace
podium init --registry ~/podium-artifacts/ --harness claude-code
podium sync
```

Open Claude Code in your project. The skill is there.

[Full quickstart](getting-started/quickstart){: .btn .btn-purple }

---

## Pick your entry point

<div class="grid-cards" markdown="1">

<div class="card" markdown="1">

### I want to write artifacts

You're authoring skills, commands, rules, agents.

[Authoring guide](authoring/){: .btn .btn-purple }

</div>

<div class="card" markdown="1">

### I want to use them in my harness

You have Claude Code, Cursor, OpenCode, or another harness, and want
Podium to feed it.

[Consuming guide](consuming/){: .btn .btn-purple }

</div>

<div class="card" markdown="1">

### I'm setting up Podium for a team

Pick your deployment shape; scale up as the team grows.

[Deployment guide](deployment/){: .btn .btn-purple }

</div>

<div class="card" markdown="1">

### I'm calling the API

Building a runtime, an eval pipeline, or custom tooling against
Podium directly.

[Reference](reference/){: .btn .btn-purple }

</div>

</div>

---

## Quick links

- [Quickstart](getting-started/quickstart){: .btn .btn-outline }
- [Concepts](getting-started/concepts){: .btn .btn-outline }
- [How it works](getting-started/how-it-works){: .btn .btn-outline }
- [GitHub](https://github.com/OWNER/podium){: .btn .btn-outline }

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
