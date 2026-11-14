[![test](https://github.com/lennylabs/podium/actions/workflows/test.yml/badge.svg)](https://github.com/lennylabs/podium/actions/workflows/test.yml)
[![nightly](https://github.com/lennylabs/podium/actions/workflows/nightly.yml/badge.svg)](https://github.com/lennylabs/podium/actions/workflows/nightly.yml)
[![codeql](https://github.com/lennylabs/podium/actions/workflows/codeql.yml/badge.svg)](https://github.com/lennylabs/podium/actions/workflows/codeql.yml)
[![codecov](https://codecov.io/gh/lennylabs/podium/branch/main/graph/badge.svg)](https://codecov.io/gh/lennylabs/podium)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Discord](https://img.shields.io/discord/1507235632966275085?logo=discord&logoColor=white&label=Discord&color=5865F2)](https://discord.gg/2kcteA8Y64)

# Podium

**The skill registry for AI agents.**

An enterprise-grade registry for reusable agent skills and other artifacts.

Podium stores skills, agents, commands, rules, hooks, contexts, and MCP server
registrations as canonical artifacts. A Podium server signs each artifact at
ingest, decides which callers can see it, and records every load and search in
an audit log. Podium delivers each artifact into the native format of every
harness that uses it.

A harness adapter performs that delivery. An author writes an artifact once,
and the adapter translates it into the layout the target runtime expects. The
same `finance/rollback` directory becomes `.claude/skills/rollback/SKILL.md`
for Claude Code and `.cursor/skills/rollback/SKILL.md` for Cursor, with no
per-harness copy in the catalog. Adapters cover Claude Code, Claude Desktop,
Claude Cowork, Cursor, Codex, Gemini CLI, OpenCode, Pi, Hermes, and custom
runtimes. `podium sync` writes a workspace tree the harness reads directly, and
a `kind: marketplace` target renders the plugin marketplace, extension,
package, or tap repository a harness imports. See
[Configure your harness](https://lennylabs.github.io/podium/consuming/configure-your-harness#supported-harnesses)
and [Publishing marketplace repositories](https://lennylabs.github.io/podium/deployment/content/publishing).

An artifact is a directory, and the directory carries its dependencies. A skill
that ships `scripts/verify_revision.py` beside its `SKILL.md` delivers that
script into every layout it reaches, workspace trees and published marketplace
repositories alike. In a workspace tree, a `rule` is a single file and an
`mcp-server` registration is a config-file merge, so bundled files on those two
types are not written.

[Documentation](https://lennylabs.github.io/podium) •
[Install](#install) •
[Features](#features) •
[Hello world](#hello-world-example) •
[Contributing](#contributing)

> **Status: 0.6.x, early release.** The CLI, server, MCP bridge, and SDKs are
> all published, but the surface and behavior may still shift before 1.0.
> Open an [issue](https://github.com/lennylabs/podium/issues) or
> [discussion](https://github.com/lennylabs/podium/discussions) for bug
> reports, missing use cases, or design feedback.

## Install

Podium releases ship the `podium`, `podium-server`, and `podium-mcp` binaries
on every supported platform. Choose the channel that fits the platform.

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

**Direct binary download:** download `podium-<os>-<arch>`, or the `.tar.gz` or `.zip` bundle that includes every binary, from the [latest release](https://github.com/lennylabs/podium/releases/latest).

**Container** (for the registry server): `docker pull ghcr.io/lennylabs/podium-server:latest`.

**SDKs** for programmatic consumers:

```bash
pip install 'podium-sdk[verify]'  # Python; imports as `from podium import ...`
npm install @lennylabs/podium-sdk  # TypeScript
```

**From source** (Go 1.26+ required):

```bash
git clone https://github.com/lennylabs/podium.git
cd podium && go build -o ~/.local/bin/podium ./cmd/podium
```

---

## Features

- **Domains and subdomains.** The directory layout defines the domain
  hierarchy. `finance` is a domain, `finance/ap` is a subdomain, and
  `finance/ap/pay-invoice` is the canonical ID of an artifact under it. A
  domain folder can carry a `DOMAIN.md` that adds a description, keywords, and
  featured artifacts. See
  [Domains](https://lennylabs.github.io/podium/authoring/domains).
- **Selective materialization.** `podium sync` writes a chosen subset of the
  catalog into a workspace ahead of time. Include globs, exclude globs, and
  artifact types select the subset. A named profile stores that selection in
  `sync.yaml`, so one command switches between scopes. See
  [Selective materialization](https://lennylabs.github.io/podium/consuming/selective-materialization).
- **Progressive discovery.** The `podium-mcp` stdio MCP server gives an agent
  tools to find artifacts and load them on demand. The agent walks domains with
  `load_domain`, finds candidates with `search_domains` and `search_artifacts`,
  and calls `load_artifact` on the one it picks. Only that last call writes
  anything, and it writes the artifact's bundled files at the same time, so a
  catalog larger than any system prompt stays usable.
  Progressive discovery requires a Podium server, reached through the MCP
  server or an SDK. See
  [Browsing the catalog](https://lennylabs.github.io/podium/consuming/browsing-the-catalog).
- **Layered composition.** A catalog is composed from several independent
  sources in a declared order, with deterministic merge and explicit
  precedence. Two layers that contribute the same artifact ID are a collision,
  which Podium rejects. A catalog on disk composes its subdirectories as
  ordered layers through `.registry-config`. A server adds registered layers
  and visibility, and a layer's source can be a Git repository, including a
  private one, or a local path. See
  [Connecting layer sources](https://lennylabs.github.io/podium/deployment/content/layer-sources)
  and [Layered composition](https://lennylabs.github.io/podium/deployment/content/layers).
- **Security and access control.** A Podium server signs each artifact
  version at ingest. The MCP server and `podium sync` against a server verify
  the signature before they write the artifact. Each layer declares who can see it:
  everyone, every authenticated user in the organization, the members of named
  OIDC groups, or named users. The registry evaluates visibility on every call,
  which requires an identity provider. Loads, searches, ingests, and admin
  changes are recorded in a hash-chained audit log. See
  [Access control](https://lennylabs.github.io/podium/deployment/content/access-control).
- **Observability.** Audit events stream to a file, stdout, HTTP receivers
  that accept NDJSON, or an OpenTelemetry Collector over OTLP. The registry
  serves Prometheus metrics at `/metrics` and exports traces over OTLP when
  tracing is enabled. A configured notification provider reports a failed
  layer ingest by webhook, email, or log. See
  [Audit and observability](https://lennylabs.github.io/podium/deployment/operations/observability).

[Why Podium](https://lennylabs.github.io/podium/getting-started/why-podium)
covers each feature in more detail, when Podium applies, when a simpler
alternative is enough, and how it compares to adjacent products.

---

## Deployment modes

Podium runs in the deployment modes below. Each mode keeps everything the mode
before it does and adds server-side capability.

| Mode | Server-side deployment | Catalog source | Materialization | What the mode adds |
|:--|:--|:--|:--|:--|
| [Local](https://lennylabs.github.io/podium/deployment/modes/local) | None | A folder, read from disk | User-driven sync | Authoring, lint, sync, domains, profiles, and ordered layers from disk |
| [Single node](https://lennylabs.github.io/podium/deployment/modes/single-node) | One binary | One or more folders or remote Git repos | User-driven sync, or agent-driven on demand | Everything in local, plus discovery through MCP or the SDKs, hybrid search, registered and remote layers with visibility, and one audit log |
| [Clustered](https://lennylabs.github.io/podium/deployment/modes/clustered) | Replicas, Postgres, and object storage | One or more folders or remote Git repos | User-driven sync, or agent-driven on demand | Everything in single node, plus Postgres schema-per-org tenant isolation and high availability across registry replicas |

The artifacts are the same in every mode. The catalog on disk does not change
when the deployment changes. The same shared Go library parses, composes, and
materializes it everywhere, so a given target and profile produce
bit-identical output.
[Moving between modes](https://lennylabs.github.io/podium/deployment/modes/migrating#skills-that-declare-no-version)
lists the exceptions for skills that declare no version.
[Deployment modes](https://lennylabs.github.io/podium/deployment/modes/) covers
choosing a mode.

---

## Server-side integrations

A registry process uses several backing services. Each one has a default that
a single-node deployment runs without extra infrastructure, and each one is
selectable per deployment.

| Integration | Out of the box | Compatible alternatives |
|:--|:--|:--|
| Metadata store | SQLite | Postgres |
| Object storage | Local filesystem | S3 or any S3-compatible service |
| Vector index | `sqlite-vec` | `pgvector`, Pinecone, Weaviate Cloud, and Qdrant Cloud |
| Embeddings | `ollama`, falling back to BM25 when unreachable | OpenAI, Voyage, Cohere, and Ollama, or a self-embedding vector backend |
| Identity | None | `oidc-jwt`, `trusted-headers`, and `injected-session-token` |
| Layer sources | Git and local paths | Custom sources through the `LayerSourceProvider` SPI |
| Audit log | Hash-chained file | stdout, HTTP receivers, and an OpenTelemetry Collector |
| Tracing | None | OTLP/HTTP and stdout |
| Notifications | None | Webhook, email over SMTP, and log |

Nothing in the right column is required to start. At cluster scale, Postgres
and object storage become requirements, because registry replicas need shared
state. The following details apply to specific rows:

- **Vector index.** `pgvector` becomes the default once the metadata store is
  Postgres.
- **Embeddings.** A single node defaults the provider to `ollama`, and a
  Postgres-backed deployment defaults it to `openai`. Neither ships in the
  binary. Hybrid search needs that provider reachable from the registry, or a
  managed vector backend that embeds on ingest. With neither,
  `search_artifacts` runs BM25 keyword search over manifest text.
- **Identity.** `oidc-jwt` verifies tokens an external IdP issued, so it needs
  that IdP's issuer URL and an audience the IdP mints tokens for.
  `trusted-headers` reads the identity headers a fronting gateway injects and
  contacts no IdP. `injected-session-token` verifies tokens signed by a
  runtime key, against the audience configured for the registry. The registry
  reads its trusted runtime keys at startup from `PODIUM_RUNTIME_KEYS_PATH`,
  which `podium admin runtime register --keys-file` writes. SCIM provisions
  groups alongside a provider rather than replacing one.
- **Audit log.** Listing several destinations writes one hash-chained stream
  to each.

See [Backing services](https://lennylabs.github.io/podium/deployment/services/)
and [Audit and observability](https://lennylabs.github.io/podium/deployment/operations/observability).

---

## 'Hello world' example

After installing the `podium` CLI, create a skill directory with a
`SKILL.md` file from the [agentskills.io](https://agentskills.io/specification)
standard. Its frontmatter carries the version in `metadata.version`, and its
body holds the agent-facing instructions:

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
[frontmatter reference](https://lennylabs.github.io/podium/authoring/frontmatter-reference)
lists them.

Anything else in the directory is a bundled resource that travels with the
artifact. Give the skill a script to call:

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

Open Claude Code in the project. Claude Code discovers the skill in its native
location. Point the same catalog at another harness, and the adapter decides
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
whole target against the lock file, so the second run removed the `.claude/`
files the first run wrote. The bracket names the filesystem layer the artifact
came from, which is the basename of the registry directory.

[Full quickstart](https://lennylabs.github.io/podium/getting-started/quickstart)

---

## Documentation

- **[Documentation site](https://lennylabs.github.io/podium)**: the docs home
  is [Overview](https://lennylabs.github.io/podium/overview). The sections are
  Getting Started, Authoring, Consuming, Deployment, Reference, and About.
  Deployment groups its pages into deployment modes, identity, catalog
  content, operations, and backing services.
- **Start here**:
  [Why Podium](https://lennylabs.github.io/podium/getting-started/why-podium) for
  the features and the comparisons,
  [Quickstart](https://lennylabs.github.io/podium/getting-started/quickstart) for
  the first artifact,
  [Concepts](https://lennylabs.github.io/podium/getting-started/concepts) for the
  vocabulary, and
  [How it works](https://lennylabs.github.io/podium/getting-started/how-it-works)
  for the architecture.
- **Reference**:
  [CLI](https://lennylabs.github.io/podium/reference/cli),
  [Configuration](https://lennylabs.github.io/podium/reference/configuration),
  [Error codes](https://lennylabs.github.io/podium/reference/error-codes), and
  [Glossary](https://lennylabs.github.io/podium/reference/glossary).
- **Project**: [Contributing](CONTRIBUTING.md),
  [Governance](GOVERNANCE.md), [Security](SECURITY.md), and
  [Implementation status](https://lennylabs.github.io/podium/about/status).

## Build and test

Building from source requires:

- Go 1.26 or later for the registry, CLI, and MCP server.
- Python 3.10 or later for the `podium-py` SDK.
- Node.js 20 or later for the `@lennylabs/podium-sdk` TypeScript SDK.

Clone the repository, then run the targets below:

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
pip install -e '.[verify]'
pytest

cd sdks/podium-ts
npm install
npm test
```

The complete Go suite runs in one to two minutes on a recent laptop.
The full development setup is in
[Contributing](https://lennylabs.github.io/podium/about/contributing), and
[Live vector backend tests](https://lennylabs.github.io/podium/about/live-vector-backends)
covers the accounts and credentials the `make test-live-external` lane needs.

## Contributing

The following contributions are the most useful today:

- **Open issues or discussions** for questions, missing use cases, and bug
  reports.
- **Run the test suite from source** and report failures or
  environment-specific issues.
- **Sketch a harness adapter.** A prototype adapter for a new harness
  validates the `HarnessAdapter` SPI against a runtime nobody has targeted yet.
- **Sketch a `LayerSourceProvider` plugin.** A custom source backend, such as
  S3, OCI, or an internal CMS, validates that SPI surface.
- **Fix typos and broken links.** Small documentation PRs are welcome at any
  time.

See [`CONTRIBUTING.md`](CONTRIBUTING.md) and [`GOVERNANCE.md`](GOVERNANCE.md).

## License

[MIT](LICENSE)
