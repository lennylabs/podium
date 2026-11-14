---
title: Getting Started
nav_order: 1
description: Start here for the positioning, the quickstart, the vocabulary, and the architecture of Podium, the skill registry for AI agents.
---

# Getting Started

Podium is a skill registry for AI agents. It stores reusable agent skills and
other artifacts, controls who can see them, and delivers them into the file
layouts Claude Code, Cursor, Codex, OpenCode, and other harnesses read. This
section introduces Podium to a new reader and points to the guide for each
task.

Start with the [quickstart](quickstart). It uses a local directory and
`podium sync`, so the authoring and materialization loop is visible before any
server concept appears.

---

## Reading order

| Page | What it covers | Time |
|:--|:--|:--|
| [Why Podium](why-podium) | The features, when Podium applies, when a simpler alternative is enough, and how Podium compares to adjacent products. Read it first when evaluating Podium. | ~10 minutes |
| [Quickstart](quickstart) | Installing the CLI, writing one skill, materializing it into Claude Code, and loading it. The local deployment mode needs no daemon and no setup beyond the CLI. | ~5 minutes |
| [Concepts](concepts) | The definitions the rest of the docs link to: registry, catalog, artifact, type, domain, layer, visibility, harness, profile, materialization, and meta-tools. | ~15 minutes |
| [How it works](how-it-works) | The components, where each feature runs, the deployment modes, and where state lives. | ~15 minutes |

---

## Features and where they are covered

| Feature | Covered in |
|:--|:--|
| Domains and subdomains | [Domains](../authoring/domains) |
| Selective materialization | [Selective materialization](../consuming/selective-materialization) |
| Progressive discovery | [Browsing the catalog](../consuming/browsing-the-catalog) |
| Layered composition | [Connecting layer sources](../deployment/layer-sources), [Layered composition](../deployment/layers) |
| Security and access control | [Access control](../deployment/access-control) |
| Observability | [Audit and observability](../deployment/observability) |
| Cross-harness delivery | [Configure your harness](../consuming/configure-your-harness), [Publishing marketplace repositories](../consuming/publishing) |

---

## Where to go next

After the quickstart, choose the guide that fits the task. Many workflows
involve both authoring artifacts and consuming them.

| Goal | Next | What the guide covers |
|:--|:--|:--|
| **Write artifacts** | [Authoring guide](../authoring/) | Artifact types, frontmatter, bundled resources, `DOMAIN.md`, and when to use rule modes and hooks. |
| **Use artifacts in a harness** | [Consuming guide](../consuming/) | Configuring Claude Code, Claude Desktop, Claude Cowork, Cursor, Codex, Gemini CLI, OpenCode, Pi, or Hermes, narrowing what a workspace receives, browsing the catalog from the agent, and working through the SDKs. |
| **Run Podium for a team or organization** | [Deployment guide](../deployment/) | Choosing a deployment mode (local, single node, or clustered), connecting layer sources, and handling day-two operations, governance, and OIDC. |
| **Build against the API** | [Reference](../reference/) | CLI, HTTP API, frontmatter schema, error codes, and glossary. |
