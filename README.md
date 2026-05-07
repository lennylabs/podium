[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

# Podium

**A registry for generic agentic AI artifacts, and tools for getting them
into any harness.**

Podium lets you:

- Define generic skills, agents, commands, rules, and other artifacts, and
  use them across any harness.
- Share artifacts with your team and organization.
- Build and organize large catalogs of artifacts and use them efficiently
  with the help of tools for progressive disclosure and lazy loading.

[Documentation](https://lennylabs.github.io/podium) •
[Hello world](#hello-world-example) •
[Specification](spec/) •
[Contributing](#contributing)

> **Status: design phase.** The technical specification drives a spec- and
> test-driven implementation. There is no shipped binary yet. Design feedback
> is the most useful contribution today. Open an
> [issue](https://github.com/lennylabs/podium/issues) or
> [discussion](https://github.com/lennylabs/podium/discussions).

---

## Setups

Podium supports multiple setups to meet the needs of single developers and
large organizations alike:

- Individual users: file-based artifacts + Podium CLI
- Small teams: artifacts in repos + Podium CLI
- Large teams/organizations: artifacts in repos + Podium registry server +
  Podium CLI/MCP server/SDK

[Concepts](https://lennylabs.github.io/podium/getting-started/concepts)

Podium supports multiple setups to meet the needs of single developers and large organizations alike:

- Individual users: file-based artifacts + Podium CLI
- Small teams: artifacts in repos + Podium CLI
- Large teams/organizations: artifacts in repos + Podium registry server + Podium CLI/MCP server/SDK

[Compare deployment setups](https://lennylabs.github.io/podium/deployment/)

---

## Highlights

- **Author once, deliver anywhere.** Pluggable harness adapters translate
  canonical artifacts into Claude Code, Claude Desktop, Cursor, OpenCode,
  Gemini, Codex, Pi, Hermes, or your own runtime.
- **Artifact organization based on domains and subdomains.** Keep artifacts
  organized in folders and subfolders, where each folder defines a domain.
- **Selective materialization.** Sync only a subset of the catalog into
  your workspace. Define profiles to quickly switch between scopes.
- **Layered composition.** Compose your catalog from multiple sources
  with deterministic merge and explicit
  precedence. (Requires the Podium registry server.)
- **Per-layer visibility.** Declare who can see what: each layer can be
  `public`, organization-wide, scoped to OIDC `groups`, or restricted to
  specific `users`. (Requires the Podium registry server.)
- **Agent-driven progressive discovery.** Discovery tools for traversing
  domains and searching artifacts. (Requires the Podium MCP server or
  SDK.)
- **Lazy artifact loading.** Materialize artifact files into your
  workspace as they are loaded. (Requires the Podium MCP server or SDK.)

Every capability is specified in [`spec/`](spec/) and covered by the
integration test suite.

---

## 'Hello world' example

After installing the `podium` CLI, write a skill: one file in a directory:

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
cd your_workspace
podium init --registry ~/podium-artifacts/ --harness claude-code
podium sync
```

Open Claude Code in your project. The skill is there.

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
custom orchestrators      OpenCode, Pi, Hermes        harnesses
```

In filesystem mode, the catalog is just a folder. `podium sync` reads
it directly, with no server, HTTP, or auth, and writes harness-native
files to your project. The MCP server and language SDKs require a
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
