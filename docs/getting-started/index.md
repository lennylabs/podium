---
layout: default
title: Getting Started
nav_order: 1
has_children: true
description: Quickstart, concepts, and a high-level look at how Podium works. The right entry point regardless of role.
---

# Getting Started with Podium

Podium is a registry for the artifacts an AI agent uses while it
works — skills, commands, rules, agents, contexts, hooks, MCP server
registrations. You author them in markdown once; Podium delivers
them into whatever harness or runtime you use.

This section is the right entry point whether you're going to author,
consume, or operate Podium. After it, follow the role-specific guide
that fits.

---

## Start here

Three pages, in order. Together they take well under an hour.

| Page | What you'll do | Time |
|:--|:--|:--|
| [Quickstart](quickstart) | Install the CLI, write one skill, materialize it into Claude Code, see it load. Filesystem mode — no daemon, no setup beyond a CLI. | ~5 minutes |
| [Concepts](concepts) | The vocabulary you'll see everywhere: artifact, domain, layer, harness, materialization, visibility, the four meta-tools. | ~15 minutes |
| [How it works](how-it-works) | Component overview, the three deployment shapes, where state lives, what runs in your process versus on a server. | ~15 minutes |

---

## Where to go next

After the quickstart, pick the role-specific guide. Most people land
in one and stay; many will be both authors and consumers.

| If you want to… | Next | Why |
|:--|:--|:--|
| **Write artifacts** | [Authoring guide](../authoring/) | The seven first-class types, the frontmatter you'll actually use, how `DOMAIN.md` works, when to use rule modes and hooks. |
| **Use artifacts in your harness** | [Consuming guide](../consuming/) | Configure Claude Code / Cursor / OpenCode / Pi / Hermes / Codex / Gemini / Claude Desktop, browse the catalog from the agent, work via the SDK. |
| **Set up Podium for a team or org** | [Deployment guide](../deployment/) | Pick your shape (filesystem / standalone / standard), then run it. Day-2 operations, progressive adoption of governance, OIDC cookbooks. |
| **Build against the API** | [Reference](../reference/) | CLI, HTTP API, frontmatter schema, error codes, glossary. |
