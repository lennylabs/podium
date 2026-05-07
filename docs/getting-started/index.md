---
layout: default
title: Getting Started
nav_order: 1
has_children: true
description: Quickstart, concepts, and a high-level look at how Podium works. The right entry point regardless of role.
---

# Getting Started with Podium

Podium is a registry for generic agentic AI artifacts, and tools for
getting them into any harness. You define skills, agents, commands,
rules, and other artifacts once and use them across any harness;
share them with your team and organization; and build large catalogs
that work efficiently with progressive disclosure and lazy loading.

This section is the right entry point regardless of role. After it,
follow the role-specific guide that fits.

---

## Start here

In order:

| Page | What it covers | Time |
|:--|:--|:--|
| [Quickstart](quickstart) | Install the CLI, write one skill, materialize it into Claude Code, see it load. Filesystem mode, with no daemon and no setup beyond a CLI. | ~5 minutes |
| [Concepts](concepts) | Vocabulary used throughout the docs: artifact, domain, layer, harness, materialization, visibility, the meta-tools. | ~15 minutes |
| [How it works](how-it-works) | Component overview, deployment shapes, where state lives, what runs in your process versus on a server. | ~15 minutes |

---

## Where to go next

After the quickstart, pick the role-specific guide. Most people land
in one and stay; many will be both authors and consumers.

| If you want to… | Next | Why |
|:--|:--|:--|
| **Write artifacts** | [Authoring guide](../authoring/) | The first-class types, the frontmatter, how `DOMAIN.md` works, when to use rule modes and hooks. |
| **Use artifacts in your harness** | [Consuming guide](../consuming/) | Configure Claude Code / Cursor / OpenCode / Pi / Hermes / Codex / Gemini / Claude Desktop, browse the catalog from the agent, work via the SDK. |
| **Set up Podium for a team or org** | [Deployment guide](../deployment/) | Pick your shape (filesystem / standalone / standard), then run it. Day-2 operations, progressive adoption of governance, OIDC cookbooks. |
| **Build against the API** | [Reference](../reference/) | CLI, HTTP API, frontmatter schema, error codes, glossary. |
