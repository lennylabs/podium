[![test](https://github.com/lennylabs/podium/actions/workflows/test.yml/badge.svg)](https://github.com/lennylabs/podium/actions/workflows/test.yml)
[![nightly](https://github.com/lennylabs/podium/actions/workflows/nightly.yml/badge.svg)](https://github.com/lennylabs/podium/actions/workflows/nightly.yml)
[![codeql](https://github.com/lennylabs/podium/actions/workflows/codeql.yml/badge.svg)](https://github.com/lennylabs/podium/actions/workflows/codeql.yml)
[![codecov](https://codecov.io/gh/lennylabs/podium/branch/main/graph/badge.svg)](https://codecov.io/gh/lennylabs/podium)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Discord](https://img.shields.io/discord/1507235632966275085?logo=discord&logoColor=white&label=Discord&color=5865F2)](https://discord.gg/2kcteA8Y64)

# Podium

**One catalog. Every harness.**

A catalog for reusable AI skills and other agent artifacts, with tools that
translate them into harness-specific formats.

Podium holds skills, agents, commands, rules, hooks, contexts, and MCP server
registrations as canonical artifacts. An author writes an artifact once, and a
harness adapter translates it into the layout the target runtime expects. The
same `finance/rollback` directory becomes `.claude/skills/rollback/SKILL.md`
for Claude Code and `.cursor/skills/rollback/SKILL.md` for Cursor, with no
per-harness copy in the catalog.

An artifact is a directory, and the directory carries its dependencies. A skill
that ships `scripts/verify_revision.py` beside its `SKILL.md` delivers that
script into every layout it reaches, workspace trees and published marketplace
repositories alike. Selecting the artifact selects its files.

[Documentation](https://lennylabs.github.io/podium) •
[Install](#install) •
[Features](#features) •
[Hello world](#hello-world-example) •
[Contributing](#contributing)

> **Status: 0.3.x, early release.** The CLI, server, MCP bridge, and SDKs are
> all published, but the surface and behavior may still shift before 1.0.
> Open an [issue](https://github.com/lennylabs/podium/issues) or
> [discussion](https://github.com/lennylabs/podium/discussions) for bug
> reports, missing use cases, or design feedback.

## Install

The Podium CLI ships the `podium`, `podium-server`, and `podium-mcp` binaries on every supported platform. Pick whichever channel matches your setup.

**macOS / Linux (Homebrew):**

```bash
brew tap lennylabs/tap
brew install podium
```

**Windows (Scoop):**

```powershell
scoop bucket add lennylabs https://github.com/lennylabs/scoop-bucket
scoop install podium
```

**Direct binary download:** grab `podium-<os>-<arch>` (or the `.tar.gz` / `.zip` bundle that includes every binary) from the [latest release](https://github.com/lennylabs/podium/releases/latest).

**Container** (for the registry server): `docker pull ghcr.io/lennylabs/podium-server:latest`.

**SDKs** for programmatic consumers:

```bash
pip install podium-sdk             # Python; imports as `from podium import ...`
npm install @lennylabs/podium-sdk  # TypeScript
```

**From source** (Go 1.26+ required):

```bash
git clone https://github.com/lennylabs/podium.git
cd podium && go build -o ~/.local/bin/podium ./cmd/podium
```

---

## Features

- **Cross-harness delivery.** A harness adapter maps a canonical artifact onto
  Claude Code, Claude Desktop, Claude Cowork, Cursor, Codex, Gemini CLI,
  OpenCode, Pi, Hermes, or a custom runtime, and decides the on-disk
  destination for each artifact type. `podium sync` writes a workspace tree the
  harness reads directly. An entry of `kind: marketplace` under `targets:`
  renders the same catalog into the git-repo distribution a harness imports
  (a plugin marketplace, extension, package, or tap) and runs an
  operator-configured workflow to push it. Bundled files ride along into both
  wherever the destination layout has a place for them. In the workspace tree a
  harness-native `rule` is a single file and an `mcp-server` registration is a
  config-file merge, so bundled files on those two types are not written.
  See [Configure your harness](https://lennylabs.github.io/podium/consuming/configure-your-harness#supported-harnesses)
  and [Marketplace publishing](https://lennylabs.github.io/podium/consuming/publishing).
- **Domains and subdomains.** The directory layout defines the domain
  hierarchy. `finance` is a domain, `finance/ap` is a subdomain, and
  `finance/ap/pay-invoice` is the canonical ID of an artifact under it. A
  domain folder can carry a `DOMAIN.md` that adds a description, keywords, and
  featured artifacts. See
  [Domains](https://lennylabs.github.io/podium/authoring/domains).
- **Selective materialization.** A workspace rarely needs the whole catalog.
  `podium sync` materializes the subset named by include globs, exclude globs,
  and artifact types, and a named profile stores that subset in `sync.yaml` so
  one command switches between scopes. See
  [Selective materialization](https://lennylabs.github.io/podium/consuming/selective-materialization).
- **Progressive discovery.** An agent that speaks MCP traverses domains with
  `load_domain`, finds candidates with `search_domains` and
  `search_artifacts`, and calls `load_artifact` on the one it picks. Only that
  last call materializes anything, and it writes the artifact's bundled files
  at the same time, so a catalog larger than any system prompt stays usable.
  Requires a Podium server, reached through the MCP server or an SDK. See
  [Browsing the catalog](https://lennylabs.github.io/podium/consuming/browsing-the-catalog).
- **Layered composition.** One catalog assembles from several independent
  sources in a declared order, with deterministic merge, explicit precedence,
  and `extends:` for an artifact that inherits and refines a lower one. A
  catalog on disk composes its subdirectories as ordered layers through
  `.registry-config`. A server adds registered layers, remote Git sources, and
  visibility. See
  [Layered composition](https://lennylabs.github.io/podium/deployment/layers).
- **Access control.** Each layer declares who can see it: everyone, every
  authenticated user in the organization, the members of named OIDC groups, or
  named users. The registry evaluates visibility on every call and composes the
  caller's effective view from the layers that pass. Requires a Podium server
  with an identity provider configured. See
  [Access control](https://lennylabs.github.io/podium/deployment/access-control).

[Why Podium](https://lennylabs.github.io/podium/getting-started/why-podium)
covers each feature in more detail, when Podium applies, when a simpler
alternative is enough, and how it compares to adjacent products.

---

## Deployment tiers

Podium runs in tiers. Each tier keeps everything the tier below it does and
adds server-side capability.

| Tier | Server-side deployment | Catalog source | Materialization | What the tier adds |
|:--|:--|:--|:--|:--|
| [Local](https://lennylabs.github.io/podium/deployment/local) | None | A folder, read from disk | User-driven sync | Authoring, lint, sync, domains, profiles, and ordered layers from disk |
| [Single node](https://lennylabs.github.io/podium/deployment/single-node) | One binary | One or more folders or remote Git repos | User-driven sync, or agent-driven on demand | Everything in local, plus discovery through MCP or the SDKs, hybrid search, registered and remote layers with visibility, and one audit log |
| [Clustered](https://lennylabs.github.io/podium/deployment/clustered) | Replicas, Postgres, and object storage | One or more folders or remote Git repos | User-driven sync, or agent-driven on demand | Everything in single node, plus multi-tenancy, SCIM group sync, signing with a transparency log, and high availability |

The artifacts are the same in every tier. The catalog on disk does not change
when the deployment changes, and the same shared Go library parses, composes,
and materializes it everywhere, so a given target and profile produce
bit-identical output. [Deployment](https://lennylabs.github.io/podium/deployment/)
covers picking a tier and moving between them.

---

## Server-side integrations

A registry process reaches out to several backing services. Each one has a
default that a single-node deployment runs without extra infrastructure, and
each one is selectable per deployment.

| Integration | Out of the box | Compatible alternatives |
|:--|:--|:--|
| Metadata store | SQLite | Postgres |
| Object storage | Local filesystem | S3 or any S3-compatible service |
| Vector index | `sqlite-vec` | `pgvector`, Pinecone, Weaviate Cloud, and Qdrant Cloud |
| Embeddings | `ollama`, falling back to BM25 when unreachable | OpenAI, Voyage, Cohere, and Ollama, or a self-embedding vector backend |
| Identity | None | `oidc-jwt`, `trusted-headers`, and `injected-session-token` |
| Layer sources | Git and local paths | Custom sources through the `LayerSourceProvider` SPI |

Nothing in the right column is required to start. `pgvector` becomes the vector
default once the metadata store is Postgres. A single node defaults the provider to `ollama` and a Postgres-backed
deployment to `openai`, but neither ships in the binary. Hybrid search needs
that provider reachable from the registry, or a managed vector backend that
embeds on ingest; with neither, `search_artifacts` runs BM25 keyword search
over manifest text. Enabling identity means registering a client with an external
IdP first, because every provider points at one. SCIM provisions groups
alongside a provider rather than replacing one. At cluster scale,
Postgres and object storage become requirements, because registry replicas need
shared state. See
[Server-side integrations](https://lennylabs.github.io/podium/deployment/integrations).

---

## 'Hello world' example

After installing the `podium` CLI, create a skill directory with a
`SKILL.md` file for agent-facing instructions and an `ARTIFACT.md` file for
Podium metadata:

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

Anything else in the directory is a bundled resource that travels with the
artifact, so give the skill a script to call:

```python
~/podium-artifacts/personal/hello/greet/scripts/today.py

from datetime import date

print(date.today().strftime("%A, %-d %B %Y"))
```

Point Podium at the directory and set the harness:

```bash
cd ~/projects/your-project
podium init --registry ~/podium-artifacts/ --harness claude-code
podium sync
```

```
adapter: claude-code
target:  /Users/alice/projects/your-project
artifacts:
  - personal/hello/greet  [podium-artifacts]
      .claude/skills/greet/SKILL.md
      .claude/skills/greet/scripts/today.py
```

Open Claude Code in the project and it discovers the skill in its native
location. Point the same catalog at another harness and the adapter decides
where everything lands:

```bash
podium sync --harness cursor
```

```
adapter: cursor
target:  /Users/alice/projects/your-project
artifacts:
  - personal/hello/greet  [podium-artifacts]
      .cursor/skills/greet/SKILL.md
      .cursor/skills/greet/scripts/today.py
```

Nothing in the catalog changed between those two runs. Each run reconciles the
whole target against the lock file, so the second one removed the `.claude/`
files the first wrote. The layer bracket names the filesystem layer the artifact
came from, which is the basename of the registry directory.

[Full quickstart](https://lennylabs.github.io/podium/getting-started/quickstart)

---

## Documentation

- **[Documentation site](https://lennylabs.github.io/podium)**: the docs home
  is [Overview](https://lennylabs.github.io/podium/overview), and the sections
  are organized by task (author, consume, deploy, and reference).
- **Start here**:
  [Why Podium](https://lennylabs.github.io/podium/getting-started/why-podium) for
  the feature-by-feature claim and the comparisons,
  [Quickstart](https://lennylabs.github.io/podium/getting-started/quickstart) for
  the first artifact,
  [Concepts](https://lennylabs.github.io/podium/getting-started/concepts) for the
  vocabulary, and
  [How it works](https://lennylabs.github.io/podium/getting-started/how-it-works)
  for the architecture.
- **Project**: [Contributing](CONTRIBUTING.md),
  [Governance](GOVERNANCE.md), [Security](SECURITY.md), and
  [Implementation status](https://lennylabs.github.io/podium/about/status).

## Build and test

Building from source requires:

- Go 1.26 or later for the registry, CLI, and MCP server.
- Python 3.10 or later for the `podium-py` SDK.
- Node.js 20 or later for the `@lennylabs/podium-sdk` TypeScript SDK.

Clone the repository, then:

```bash
go build ./...           # Build every Go binary in the module.
make test                # Run the full Go test suite.
make test-live           # Run the suite against the local Postgres and
                         # MinIO services started by `make services-up`.
make test-live-external  # Run the suite against the managed vector and
                         # embedding services (PODIUM_LIVE_EXTERNAL=1).
make coverage            # Run with -coverprofile and print a summary.
make help                # List every make target.
```

The SDK suites run independently:

```bash
cd sdks/podium-py
pip install -e .
pytest

cd sdks/podium-ts
npm install
npm test
```

The complete Go suite runs in one to two minutes on a recent laptop.
The full development setup is in
[`docs/about/contributing.md`](https://lennylabs.github.io/podium/about/contributing).

## Contributing

Today's most useful contributions:

- **Open issues or discussions**: questions, missing use cases, bug reports.
- **Run the test suite from source** and report failures or environment-specific issues.
- **Sketch a harness adapter**: prototyping an adapter for a new harness
  validates the `HarnessAdapter` SPI against a runtime nobody has targeted yet.
- **Sketch a `LayerSourceProvider` plugin**: a custom source backend
  (S3, OCI, internal CMS) validates that SPI surface.
- **Fix typos and broken links**: small documentation PRs are welcome
  any time.

See [`CONTRIBUTING.md`](CONTRIBUTING.md) and [`GOVERNANCE.md`](GOVERNANCE.md).

## License

[MIT](LICENSE)
