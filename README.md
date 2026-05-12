[![test](https://github.com/lennylabs/podium/actions/workflows/test.yml/badge.svg)](https://github.com/lennylabs/podium/actions/workflows/test.yml)
[![nightly](https://github.com/lennylabs/podium/actions/workflows/nightly.yml/badge.svg)](https://github.com/lennylabs/podium/actions/workflows/nightly.yml)
[![codeql](https://github.com/lennylabs/podium/actions/workflows/codeql.yml/badge.svg)](https://github.com/lennylabs/podium/actions/workflows/codeql.yml)
[![codecov](https://codecov.io/gh/lennylabs/podium/branch/main/graph/badge.svg)](https://codecov.io/gh/lennylabs/podium)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

# Podium

**A catalog for reusable AI agent artifacts, with tools that translate
those artifacts into harness-specific formats and help you share them with others.**

Podium stores skills, agents, commands, rules, hooks, contexts, and MCP
server registrations as portable artifacts. A developer can keep a local
filesystem catalog and run `podium sync` to write harness-native files into
a workspace. A team can put the same artifacts behind a registry server for
runtime discovery, identity-aware visibility, audit, and shared governance.
In server mode, teams usually keep the catalog in one or more Git
repositories; the registry ingests those tracked refs and builds the
effective catalog it serves.

[Documentation](https://lennylabs.github.io/podium) •
[Install](#install) •
[Hello world](#hello-world-example) •
[Contributing](#contributing)

> **Status: 0.1.x, early release.** The CLI, server, MCP bridge, and SDKs are
> all published, but the surface and behavior may still shift before 1.0.
> Open an [issue](https://github.com/lennylabs/podium/issues) or
> [discussion](https://github.com/lennylabs/podium/discussions) for bug
> reports, missing use cases, or design feedback.

## Install

The Podium CLI ships three binaries (`podium`, `podium-server`, `podium-mcp`) on every supported platform. Pick whichever channel matches your setup.

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

**Direct binary download:** grab `podium-<os>-<arch>` (or the `.tar.gz` / `.zip` bundle that includes all three binaries) from the [latest release](https://github.com/lennylabs/podium/releases/latest).

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

## Setups

Podium can run from a filesystem catalog or from a registry server:

- **Filesystem catalog**: file-based artifacts plus the Podium CLI. This
  mode fits individual use, prototypes, CI, and small shared repositories.
- **Registry server**: artifacts in one or more Git repositories, plus the
  Podium server, CLI, MCP server, and SDKs. Git stores catalog history and
  review flow; the registry ingests the configured refs and composes the
  effective catalog. This mode adds runtime discovery, identity-aware
  visibility, audit, and server-side composition.

[Concepts](https://lennylabs.github.io/podium/getting-started/concepts)
[Compare deployment setups](https://lennylabs.github.io/podium/deployment/)

---

## Highlights

- **Cross-harness delivery.** Pluggable harness adapters translate canonical
  artifacts into Claude Code, Claude Desktop, Claude Cowork, Cursor, Codex,
  Gemini CLI, OpenCode, Pi, Hermes, or a custom runtime. The adapter roster
  with documentation links is in
  [Configure your harness](https://lennylabs.github.io/podium/consuming/configure-your-harness/#supported-harnesses).
- **Artifact organization based on domains and subdomains.** Keep artifacts
  organized in folders and subfolders, where each folder defines a domain.
- **Selective materialization.** Sync a subset of the catalog into a
  workspace. Define profiles to quickly switch between scopes.
- **Layered composition.** Compose the catalog from multiple sources
  with deterministic merge and explicit
  precedence. (Requires the Podium registry server.)
- **Per-layer visibility.** Declare who can see what: each layer can be
  `public`, organization-wide, scoped to OIDC `groups`, or restricted to
  specific `users`. (Requires the Podium registry server.)
- **Agent-driven progressive discovery.** Discovery tools for traversing
  domains and searching artifacts. (Requires the Podium MCP server or
  SDK.)
- **Lazy artifact loading.** Materialize artifact files into the workspace
  as they are loaded. (Requires the Podium MCP server or SDK.)

Every capability is covered by the integration test suite.

---

## 'Hello world' example

The commands below describe the target v1 CLI flow.

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

Point Podium at the directory and set the harness:

```bash
cd workspace
podium init --registry ~/podium-artifacts/ --harness claude-code
podium sync
```

Open Claude Code in the project. Claude Code can discover the materialized
skill in its native location.

[Full quickstart](https://lennylabs.github.io/podium/getting-started/quickstart)

---

## How it works

Podium consists of:

- A **registry**: the catalog of artifacts. Backed either by a folder
  on disk (filesystem mode) or by a Podium server (standalone or
  standard mode). Built-in source types are `git` (a remote Git repo
  at a tracked ref) and `local` (a filesystem path); the
  `LayerSourceProvider` SPI lets deployments add custom sources
  (S3 buckets, OCI registries, HTTP archives).
- **Consumers**: built-in consumers are `podium sync`, the MCP
  server, and the language SDKs. Custom consumers can build against
  the HTTP API directly.

In server mode, the server holds the catalog; consumers reach it
over HTTP and identity-aware composition runs server-side:

```
   Git repos / local paths ──────────┐
   (one or more layer sources)       │
                                     ▼
                       ┌─────────────────────────┐
                       │ Podium server           │
                       │  HTTP/JSON API          │
                       │  Postgres + pgvector    │
                       │  layer composition      │
                       │  visibility filtering   │
                       │  dependency graph       │
                       └────────────▲────────────┘
                                    │
                  OAuth-attested identity (every call)
                                    │
       ┌────────────────────────────┼────────────────────────────┐
       │                            │                            │
┌──────┴───────┐          ┌─────────┴──────┐          ┌──────────┴─────┐
│ Language SDKs│          │ MCP server     │          │ podium sync    │
│ (py, ts)     │          │ (in-process)   │          │ (CLI)          │
└──────────────┘          └────────────────┘          └────────────────┘
LangChain, Bedrock,       Claude Code, Cursor,        File-based
custom orchestrators      Cowork, OpenCode, Pi,       harnesses
                          Hermes
```

In filesystem mode, the catalog is a folder. `podium sync` reads
it directly, with no server, HTTP, or auth, and writes harness-native
files to a project. The MCP server and language SDKs require a
server.

| Component         | Role                                                                                                        |
| :---------------- | :---------------------------------------------------------------------------------------------------------- |
| **Podium server** | HTTP API; layer composition; visibility filtering; manifest indexing; hybrid retrieval; signing; audit.     |
| **MCP server**    | In-process bridge for MCP-speaking hosts. Exposes the discovery and load meta-tools. Requires a server.     |
| **`podium sync`** | CLI (and library) that materializes the user's effective view to disk via the harness adapter. Either mode. |
| **Language SDKs** | Thin HTTP clients for programmatic runtimes (LangChain, Bedrock, custom orchestrators). Requires a server.  |

Layer composition, visibility filtering, and harness adaptation run
through the same shared Go library regardless of mode: embedded
behind the server's HTTP API in server mode; invoked directly by
`podium sync` in filesystem mode. Migrating between modes is
mechanical and produces equivalent output for the same artifact
directory.

---

## Documentation

- **[Documentation site](https://lennylabs.github.io/podium)**:
  organized by role (author / consume / deploy). Start with
  [Getting Started](https://lennylabs.github.io/podium/getting-started/)
  for the quickstart, concepts, and architecture.
- **[Contributing](CONTRIBUTING.md)**,
  **[Governance](GOVERNANCE.md)**, **[Security](SECURITY.md)**.

## Build and test

Building from source requires:

- Go 1.26 or later for the registry, CLI, and MCP server.
- Python 3.10 or later for the `podium-py` SDK.
- Node.js 20 or later for the `@lennylabs/podium-sdk` TypeScript SDK.

Clone the repository, then:

```bash
go build ./...          # Build every Go binary in the module.
make test               # Run the full Go test suite.
make test-live          # Run Tier 2 tests against real Postgres, S3,
                        # Sigstore, and embedding providers
                        # (configured via PODIUM_LIVE_* env vars).
make coverage           # Run with -coverprofile and print a summary.
make help               # List every make target.
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

The complete Go suite runs in about 10 seconds on a recent laptop.
The full development setup is in
[`docs/about/contributing.md`](https://lennylabs.github.io/podium/about/contributing).

## Contributing

Today's most useful contributions:

- **Open issues or discussions**: questions, missing use cases, bug reports.
- **Run the test suite from source** and report failures or environment-specific issues.
- **Sketch a harness adapter**: prototyping an adapter for a new harness
  helps validate the adapter SPI shape.
- **Sketch a `LayerSourceProvider` plugin**: a custom source backend
  (S3, OCI, internal CMS) helps validate that SPI surface.
- **Fix typos and broken links**: small documentation PRs are welcome
  any time.

See [`CONTRIBUTING.md`](CONTRIBUTING.md) and [`GOVERNANCE.md`](GOVERNANCE.md).

## License

[MIT](LICENSE)
