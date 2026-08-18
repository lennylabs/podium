---
title: Getting Started
nav_order: 1
description: One catalog, every harness. Start here for the quickstart, the vocabulary, and the architecture.
---

# Getting Started

Podium holds reusable AI agent artifacts in one catalog and translates them into
the file layouts Claude Code, Cursor, Codex, OpenCode, and other harnesses read.
An artifact is a directory, so the scripts and references it bundles travel with
it into every layout.

Start with the quickstart. It uses a local directory and `podium sync`, so the
authoring and materialization loop is visible before any server concept appears.

---

## Reading order

| Page | What it covers | Time |
|:--|:--|:--|
| [Why Podium](why-podium) | What "one catalog, every harness" means feature by feature, when Podium applies, when a simpler alternative is enough, and how it compares to adjacent tools. Read first when evaluating. | ~10 minutes |
| [Quickstart](quickstart) | Install the CLI, write one skill, materialize it into Claude Code, and see it load. The local tier needs no daemon and no setup beyond the CLI. | ~5 minutes |
| [Concepts](concepts) | The definitions the rest of the docs link to: artifact, type, domain, registry, layer, visibility, harness, profile, materialization, and meta-tools. | ~15 minutes |
| [How it works](how-it-works) | Component overview, where each feature runs, the deployment tiers, and where state lives. | ~15 minutes |

---

## Features and where they are covered

| Feature | Covered in |
|:--|:--|
| Cross-harness delivery | [Configure your harness](../consuming/configure-your-harness) |
| Domains and subdomains | [Domains](../authoring/domains) |
| Selective materialization | [Selective materialization](../consuming/selective-materialization) |
| Progressive discovery | [Browsing the catalog](../consuming/browsing-the-catalog) |
| Layered composition | [Layered composition](../deployment/layers) |
| Access control | [Access control](../deployment/access-control) |

---

## Where to go next

After the quickstart, choose the guide that fits the task. Many workflows
involve both authoring artifacts and consuming them.

| Goal | Next | Why |
|:--|:--|:--|
| **Write artifacts** | [Authoring guide](../authoring/) | Artifact types, frontmatter, bundled resources, how `DOMAIN.md` works, and when to use rule modes and hooks. |
| **Use artifacts in a harness** | [Consuming guide](../consuming/) | Configure Claude Code, Claude Desktop, Claude Cowork, Cursor, Codex, Gemini CLI, OpenCode, Pi, or Hermes, narrow what a workspace receives, browse the catalog from the agent, and work through the SDKs. |
| **Run Podium for a team or organization** | [Deployment guide](../deployment/) | Pick a tier (local, single node, or clustered), run it, and handle day-two operations, governance, and OIDC. |
| **Build against the API** | [Reference](../reference/) | CLI, HTTP API, frontmatter schema, error codes, and glossary. |
