---
title: Podium
nav_title: Overview
nav_order: 0
description: The documentation home for Podium, the skill registry for AI agents, with the page that covers each feature, deployment mode, and task.
actions:
  - label: Quickstart
    href: getting-started/quickstart
  - label: Concepts
    href: getting-started/concepts
  - label: Why Podium
    href: getting-started/why-podium
  - label: Deployment modes
    href: deployment/
---

# Podium

Podium is a skill registry for AI agents. It stores reusable agent skills and
other artifacts (agents, commands, rules, hooks, contexts, and MCP server
registrations), signs them, controls who can see them, audits their use, and
delivers them into the native file layout of each harness. This page is the
documentation home and names the page that covers each feature, deployment
mode, and task.

An author writes an artifact once in the canonical format. A harness adapter
produces the Claude Code layout, the Cursor layout, or a published marketplace
repository from it. An artifact is a directory, so a script bundled beside a
`SKILL.md` reaches every layout the manifest reaches.

> [!NOTE]
> **Status: early release.** The CLI, server, MCP bridge, and SDKs
> are published. Install with `brew tap lennylabs/tap && brew install podium`
> (macOS / Linux) or `scoop bucket add lennylabs https://github.com/lennylabs/scoop-bucket && scoop install podium` (Windows).
> See [Implementation status](about/status) for what's shipped and what's
> still on the roadmap to 1.0.

---

## Start here

| Page | What it covers |
|:--|:--|
| [Why Podium](getting-started/why-podium) | The features, when Podium applies, when a simpler alternative is enough, and how Podium compares to adjacent products. |
| [Quickstart](getting-started/quickstart) | Installing the CLI, writing one skill, materializing it into Claude Code, and loading it. |
| [Concepts](getting-started/concepts) | The vocabulary the rest of the docs link to: registry, catalog, artifact, type, domain, layer, visibility, harness, profile, materialization, and meta-tools. |
| [How it works](getting-started/how-it-works) | The components, where each feature runs, the deployment modes, and where state lives. |

---

## Features

| Feature | What it does | Covered in |
|:--|:--|:--|
| Domains and subdomains | The directory layout defines the domain hierarchy, and `DOMAIN.md` adds descriptions, keywords, and featured artifacts. | [Domains](authoring/domains) |
| Selective materialization | `podium sync` writes the subset named by include globs, exclude globs, and types ahead of time, and a named profile replays that subset. | [Selective materialization](consuming/selective-materialization) |
| Progressive discovery | An agent walks domains and searches the catalog through the MCP server's meta-tools, and materializes an artifact and its bundled files only when it loads one. | [Browsing the catalog](consuming/browsing-the-catalog) |
| Layered composition | Independent sources, including Git repositories, compose into one catalog with deterministic merge and explicit precedence. | [Connecting layer sources](deployment/layer-sources), [Layered composition](deployment/layers) |
| Security and access control | The registry signs artifacts at ingest and consumers verify them on sync. Each layer declares who can see it, and every call is audited. | [Access control](deployment/access-control) |
| Observability | Audit events stream to a file, stdout, HTTP receivers, or an OpenTelemetry Collector. The registry also serves Prometheus metrics, exports OTLP traces, and sends ingest-failure notifications. | [Audit and observability](deployment/observability) |
| Cross-harness delivery | A harness adapter translates a canonical artifact into the layout one runtime expects, for a workspace tree or a published marketplace repository. | [Configure your harness](consuming/configure-your-harness), [Publishing marketplace repositories](consuming/publishing) |

---

## Deployment modes

Each deployment mode keeps everything the mode before it does and adds
server-side capability. The artifacts are the same in every mode.

| Mode | Server-side deployment | What the mode adds |
|:--|:--|:--|
| [Local](deployment/local) | None | Authoring, lint, sync, domains, profiles, and ordered layers from disk |
| [Single node](deployment/single-node) | One binary | Discovery through MCP or the SDKs, hybrid search, registered and remote layers with visibility, and one audit log |
| [Clustered](deployment/clustered) | Replicas, Postgres, and object storage | Postgres schema-per-org tenant isolation and high availability across registry replicas |

[Server-side integrations](deployment/integrations) names the backing service
behind each server-side concern, such as the metadata store, object storage,
the vector index, embeddings, identity, and layer sources. Each row states what
ships by default and what can replace it.

---

## Guides

| Guide | What it covers |
|:--|:--|
| [Getting Started](getting-started/) | Positioning, the quickstart, the vocabulary, and the architecture. |
| [Authoring](authoring/) | Writing artifacts: types, frontmatter, domains, bundled resources, rule modes, hooks, and hints. |
| [Consuming](consuming/) | Delivering artifacts into a harness, narrowing what a workspace receives, runtime discovery, the SDKs, and marketplace publishing. |
| [Deployment](deployment/) | Running each deployment mode, connecting layer sources, access control, audit and observability, the server-side integrations, day-two operations, and the OIDC cookbooks. |
| [Reference](reference/) | CLI, HTTP API, frontmatter schema, error codes, and glossary. |
| [Testing](testing/) | Setting up and running the integration and live-backend suites. |
| [About](about/) | Implementation status, contributing, governance, and the changelog. |

---

## 'Hello world' example

The [full quickstart](getting-started/quickstart) covers the same flow with
prerequisites and verification steps.

Create a skill directory with a `SKILL.md` file from the
[agentskills.io](https://agentskills.io/specification) standard. Its
frontmatter carries the version in `metadata.version`, and its body holds the
agent-facing instructions:

```markdown
~/podium-artifacts/personal/hello/greet/SKILL.md

---
name: greet
description: Greet the user by name and tell them today's date.
metadata:
  version: "1.0.0"
---

Greet the user by their first name. Tell them today's date.
```

A hand-written `ARTIFACT.md` beside `SKILL.md` is optional. It carries
Podium-only fields such as `tags` and `sensitivity`, and the
[frontmatter reference](authoring/frontmatter-reference) lists them.

Point Podium at the directory and set the harness:

```bash
cd ~/projects/your-project
podium init --registry ~/podium-artifacts/ --harness claude-code
podium sync
```

Open Claude Code in the project. Claude Code discovers the materialized skill
in its native location. Changing the harness and running `podium sync` again
writes the same artifact into another runtime's layout. That run also removes
the files the previous harness's run wrote, because each run reconciles the
whole target against the lock file.

The source, the issue tracker, and the release history live at
[lennylabs/podium](https://github.com/lennylabs/podium).
