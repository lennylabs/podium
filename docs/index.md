---
layout: default
title: Home
nav_order: 0
permalink: /
description: Podium is a registry for the artifacts AI agents use — skills, commands, rules, agents — and a way to deliver them to any harness.
---

# Podium

**A registry for the artifacts AI agents use, and a way to get them
into any harness — Claude Code, Cursor, OpenCode, your own runtime.**

You write skills, commands, rules, agents, and other artifacts in
markdown. Podium serves them. Each consumer — your harness, an SDK,
a `podium sync` command — pulls what it needs.

You can run Podium as a folder of files (no daemon, no setup), as a
single binary for a small team, or as a multi-tenant service for an
organization. Same artifacts, same source repo, same author flow —
different operational shape.

{: .note }

> **Status: design phase.** The technical specification drives a
> spec- and test-driven implementation. There is no shipped binary
> yet. Design feedback is the most useful contribution today — see
> [Status](about/status) and [Contributing](about/contributing).

---

## In 30 seconds

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
podium init --global --registry ~/podium-artifacts/ --harness claude-code
podium sync --target .claude/
```

Open Claude Code in your project. The skill is there.

That's the whole solo workflow. No daemon. No server. Just files.

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

## Three deployment shapes, in order of effort

| Shape | Who it's for | What's running |
|:--|:--|:--|
| **Filesystem** | Solo developer, prototype, CI build step | `podium sync` reads a directory. No daemon, no port, no auth. |
| **Standalone server** | 3–10 person team, one VM | One binary (`podium serve --standalone`). Embedded SQLite, bundled embedding model. Adds runtime discovery and a single audit log. |
| **Standard** | 20+ people, multi-tenant, governed | Postgres + S3 + OIDC. Per-layer visibility, freeze windows, signing, SCIM, hash-chained audit. |

Same artifacts. Same author flow. Pick the shape that fits today;
graduate when you outgrow it. Migration is mechanical — `podium serve
--standalone --layer-path /path/to/dir` against the same directory
turns a filesystem catalog into a server source without touching the
authoring loop.

[Compare deployment shapes](deployment/){: .btn .btn-outline }

---

## What's in the box

- **Author once, deliver anywhere.** Pluggable harness adapters
  translate canonical artifacts into Claude Code, Claude Desktop,
  Cursor, Gemini, Codex, OpenCode, Pi, and Hermes — and you can
  register your own through the `HarnessAdapter` SPI.
- **Seven first-class types.** `skill`, `agent`, `context`,
  `command`, `rule`, `hook`, and `mcp-server`. Extension types
  register through the `TypeProvider` SPI.
- **Layered composition.** An ordered list of layers (admin-defined,
  user-defined, workspace-local) composes per request, with
  deterministic merge and explicit precedence. `extends:` lets a
  higher-precedence artifact inherit and refine a lower one without
  forking.
- **Visibility per layer.** `public`, `organization`, OIDC `groups`,
  or specific `users`. Authoring rights stay in your Git host's
  branch protection — Podium doesn't duplicate them.
- **Lazy materialization.** Sessions can start empty. Agents call
  `load_domain` / `search_domains` / `search_artifacts` /
  `load_artifact` only when they need something.
- **Hybrid retrieval.** BM25 + vector embeddings, fused via reciprocal
  rank. Vector backends include pgvector, sqlite-vec, Pinecone,
  Weaviate Cloud, and Qdrant Cloud. Embedding providers include
  embedded-onnx, openai, voyage, cohere, and ollama.
- **Pluggable everywhere.** 17 SPIs across storage, identity,
  composition, signing, audit, layer source, and delivery. The SPI
  shapes are designed to support out-of-process plugins in a future
  release without source-incompatible changes.

---

## Quick links

- [Quickstart](getting-started/quickstart){: .btn .btn-outline }
- [Concepts](getting-started/concepts){: .btn .btn-outline }
- [How it works](getting-started/how-it-works){: .btn .btn-outline }
- [Specification](https://github.com/OWNER/podium/tree/main/spec){: .btn .btn-outline }
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
