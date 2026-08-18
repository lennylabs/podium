---
title: Podium
nav_title: Overview
nav_order: 0
description: The documentation home. One catalog, every harness, and where each feature, tier, and guide is documented.
actions:
  - label: Quickstart
    href: getting-started/quickstart
  - label: Concepts
    href: getting-started/concepts
  - label: Why Podium
    href: getting-started/why-podium
  - label: Deployment tiers
    href: deployment/
---

# Podium

Podium holds reusable AI agent artifacts in one catalog and translates them into
the file layout each runtime expects. An author writes an artifact once in the
canonical format, and a harness adapter produces the Claude Code layout, the
Cursor layout, or a published marketplace repository from it.

An artifact is a directory, and the directory carries its dependencies. A script
bundled beside a `SKILL.md` reaches every layout the manifest reaches.

This page is the documentation home. It names the pages that cover each feature,
each deployment tier, and each task.

> [!NOTE]
> **Status: 0.3.x, early release.** The CLI, server, MCP bridge, and SDKs
> are published. Install with `brew tap lennylabs/tap && brew install podium`
> (macOS / Linux) or `scoop bucket add lennylabs https://github.com/lennylabs/scoop-bucket && scoop install podium` (Windows).
> See [Implementation status](about/status) for what's shipped and what's
> still on the roadmap to 1.0.

---

## Start here

| Page | What it covers |
|:--|:--|
| [Why Podium](getting-started/why-podium) | What "one catalog, every harness" means feature by feature, when Podium applies, when a simpler alternative is enough, and how it compares to adjacent products. |
| [Quickstart](getting-started/quickstart) | Install the CLI, write one skill, materialize it into Claude Code, and see it load. |
| [Concepts](getting-started/concepts) | The vocabulary the rest of the docs link to: artifact, type, domain, registry, layer, visibility, harness, profile, materialization, and meta-tools. |
| [How it works](getting-started/how-it-works) | The components, where each feature runs, the deployment tiers, and where state lives. |

---

## Features

| Feature | What it does | Covered in |
|:--|:--|:--|
| Cross-harness delivery | A harness adapter translates a canonical artifact into the layout one runtime expects, for a workspace tree or a published marketplace repository. | [Configure your harness](consuming/configure-your-harness), [Marketplace publishing](consuming/publishing) |
| Domains and subdomains | The directory layout defines the domain hierarchy, and `DOMAIN.md` adds descriptions, keywords, and featured artifacts. | [Domains](authoring/domains) |
| Selective materialization | `podium sync` materializes the subset named by include globs, exclude globs, and types, and a named profile replays that subset. | [Selective materialization](consuming/selective-materialization) |
| Progressive discovery | An agent traverses domains and searches the catalog through the meta-tools, and materializes an artifact and its bundled files only when it loads one. | [Browsing the catalog](consuming/browsing-the-catalog) |
| Layered composition | Several independent sources compose into one catalog with deterministic merge, explicit precedence, and `extends:`. | [Layered composition](deployment/layers) |
| Access control | Each layer declares who can see it, and the registry composes the caller's effective view from the layers that pass. | [Access control](deployment/access-control) |

---

## Deployment tiers

Each tier keeps everything the tier below it does and adds server-side
capability. The artifacts are the same in every tier.

| Tier | Server-side deployment | What the tier adds |
|:--|:--|:--|
| [Local](deployment/local) | None | Authoring, lint, sync, domains, profiles, and ordered layers from disk |
| [Single node](deployment/single-node) | One binary | Discovery through MCP or the SDKs, hybrid search, registered and remote layers with visibility, and one audit log |
| [Clustered](deployment/clustered) | Replicas, Postgres, and object storage | Multi-tenancy, SCIM group sync, signing with a transparency log, and high availability |

[Server-side integrations](deployment/integrations) names the backing service
behind each server-side concern: the metadata store, object storage, the vector
index, embeddings, identity, and layer sources. Each row states what ships by
default and what can replace it.

---

## Guides

| Guide | What it covers |
|:--|:--|
| [Getting Started](getting-started/) | Positioning, the quickstart, the vocabulary, and the architecture. |
| [Authoring](authoring/) | Writing artifacts: types, frontmatter, domains, bundled resources, `extends:`, rule modes, hooks, and hints. |
| [Consuming](consuming/) | Delivering artifacts into a harness, narrowing what a workspace receives, runtime discovery, the SDKs, and marketplace publishing. |
| [Deployment](deployment/) | Running each tier, the server-side integrations, layers, access control, day-two operations, and the OIDC cookbooks. |
| [Reference](reference/) | CLI, HTTP API, frontmatter schema, error codes, and glossary. |
| [Testing](testing/) | Setting up and running the integration and live-backend suites. |
| [About](about/) | Implementation status, contributing, governance, and the changelog. |

---

## 'Hello world' example

The [full quickstart](getting-started/quickstart) covers the same flow with
prerequisites and verification steps.

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
cd ~/projects/your-project
podium init --registry ~/podium-artifacts/ --harness claude-code
podium sync
```

Open Claude Code in the project. Claude Code can discover the materialized
skill in its native location. Changing the harness and running `podium sync`
again writes the same artifact into another runtime's layout, and it removes the
files the previous harness's run wrote, because each run reconciles the whole
target against the lock file.

The source, the issue tracker, and the release history live at
[lennylabs/podium](https://github.com/lennylabs/podium).
