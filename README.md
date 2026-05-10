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
[Hello world](#hello-world-example) •
[Specification](spec/) •
[Contributing](#contributing)

> **Status: pre-release.** The initial v1 implementation lives on the
> `initial-implementation` branch. No tagged release has been published;
> install by [building from source](#build-and-test). Open an
> [issue](https://github.com/lennylabs/podium/issues) or
> [discussion](https://github.com/lennylabs/podium/discussions) for
> design feedback or bug reports.

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

Every capability is specified in [`spec/`](spec/) and covered by the
integration test suite.

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
- **[Specification](spec/)**: the technical source of truth, one file
  per top-level section.
- **[Roadmap](ROADMAP.md)**, **[Contributing](CONTRIBUTING.md)**,
  **[Governance](GOVERNANCE.md)**, **[Security](SECURITY.md)**.

## Build and test

Building from source requires:

- Go 1.26 or later for the registry, CLI, and MCP server.
- Python 3.10 or later for the `podium-py` SDK.
- Node.js 20 or later for the `@podium/sdk` TypeScript SDK.

Clone the repository, then:

```bash
go build ./...          # Build every Go binary in the module.
make test               # Run the full Go test suite.
make test-live          # Run Tier 2 tests against real Postgres, S3,
                        # Sigstore, and embedding providers
                        # (configured via PODIUM_LIVE_* env vars).
make coverage           # Run with -coverprofile and print a summary.
make speccov            # Print spec-section coverage from test annotations.
make matrix-audit       # Audit spec-table coverage (§6.7.1, §6.10, etc.).
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
Detailed development setup, including the spec-citation conventions
that test annotations follow, is in
[`docs/about/contributing.md`](https://lennylabs.github.io/podium/about/contributing).

## Contributing

The specification is the source of truth and the most useful place to
push back. Today's most useful contributions:

- **Open issues or discussions**: questions, disagreements with the
  spec, missing use cases.
- **Read the spec and stress-test it**: if a section is unclear or
  contradicts another, file an issue.
- **Sketch a harness adapter**: prototyping an adapter against the
  [adapter contract](spec/06-mcp-server.md#67-harness-adapters) helps
  surface gaps before they're expensive to fix.
- **Sketch a `LayerSourceProvider` plugin**: a custom source backend
  (S3, OCI, internal CMS) against the
  [SPI](spec/09-extensibility.md) helps validate that surface.
- **Fix typos and broken links**: small documentation PRs are welcome
  any time.

See [`CONTRIBUTING.md`](CONTRIBUTING.md) and [`GOVERNANCE.md`](GOVERNANCE.md).

## License

[MIT](LICENSE)
