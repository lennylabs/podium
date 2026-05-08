---
layout: default
title: Getting Started
nav_order: 1
has_children: true
description: Quickstart, concepts, and a high-level look at how Podium works.
---

# Getting Started

Podium stores reusable AI agent artifacts in a catalog and translates them
into the file formats used by Claude Code, Cursor, Codex, OpenCode, and
other harnesses.

Start with the filesystem quickstart. It uses a local directory and
`podium sync`, so the basic authoring and materialization loop is visible
before server concepts appear.

---

## Reading Order

In order:

| Page | What it covers | Time |
|:--|:--|:--|
| [Quickstart](quickstart) | Install the CLI, write one skill, materialize it into Claude Code, and see it load. Filesystem mode has no daemon and no setup beyond a CLI. | ~5 minutes |
| [Concepts](concepts) | Vocabulary used throughout the docs: artifact, domain, layer, harness, materialization, visibility, and meta-tools. | ~15 minutes |
| [How it works](how-it-works) | Component overview, deployment modes, where state lives, what runs in your process versus on a server. | ~15 minutes |

---

## Where to go next

After the quickstart, choose the role-specific guide that fits the task.
Many workflows involve both authoring artifacts and consuming them.

| Goal | Next | Why |
|:--|:--|:--|
| **Write artifacts** | [Authoring guide](../authoring/) | Artifact types, frontmatter, how `DOMAIN.md` works, and when to use rule modes and hooks. |
| **Use artifacts in your harness** | [Consuming guide](../consuming/) | Configure Claude Code / Cursor / OpenCode / Pi / Hermes / Codex / Gemini / Claude Desktop, browse the catalog from the agent, work via the SDK. |
| **Set up Podium for a team or org** | [Deployment guide](../deployment/) | Pick your mode (filesystem / standalone / standard), then run it. Day-2 operations, progressive adoption of governance, OIDC cookbooks. |
| **Build against the API** | [Reference](../reference/) | CLI, HTTP API, frontmatter schema, error codes, glossary. |
