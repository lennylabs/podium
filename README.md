# Podium

**A registry for generic agentic AI artifacts, and tools for getting them
into any harness.**

Podium lets you:

- Define generic skills, agents, commands, rules, and other artifacts, and
  use them across any harness.
- Share artifacts with your team and organization.
- Build and organize large catalogs of artifacts and use them efficiently
  with the help of tools for progressive disclosure and lazy loading.

[Documentation](https://OWNER.github.io/podium) •
[Quickstart](#quickstart) •
[Specification](spec/) •
[Contributing](#contributing)

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

> **Status: design phase.** The technical specification drives a spec- and
> test-driven implementation. There is no shipped binary yet. Design feedback
> is the most useful contribution today — open an
> [issue](https://github.com/OWNER/podium/issues) or
> [discussion](https://github.com/OWNER/podium/discussions).

---

## Setups

Podium supports multiple setups to meet the needs of single developers and
large organizations alike:

- **Individual users:** file-based artifacts + Podium CLI.
- **Small teams:** artifacts in repos + Podium CLI.
- **Large teams / organizations:** artifacts in repos + Podium registry
  server + Podium CLI / MCP server / SDK.

Same artifacts. Same author flow. Different operational shape. Migration
between shapes is mechanical.

[Compare deployment setups](https://OWNER.github.io/podium/deployment/)

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
- **Per-layer visibility.** Declare who can see what — each layer can be
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

## Quickstart

Filesystem mode — no daemon, no setup beyond a CLI:

```bash
# Install (target shape; not yet packaged)
brew install OWNER/tap/podium

# In your project, point Podium at a folder of artifacts
mkdir -p ~/podium-artifacts/personal
cd ~/projects/your-project
podium init --registry ~/podium-artifacts/ --harness claude-code

# Author one skill
mkdir -p ~/podium-artifacts/personal/hello/greet
cat > ~/podium-artifacts/personal/hello/greet/ARTIFACT.md <<'EOF'
---
type: skill
name: greet
version: 1.0.0
description: Greet the user by name and tell them today's date.
---

Greet the user by their first name. Tell them today's date.
EOF

# Materialize into the project
podium sync
```

Open Claude Code in the project. The skill is available.

[Full quickstart](https://OWNER.github.io/podium/getting-started/quickstart)

---

## How it works

```
   Git repos / S3 / OCI / local paths ──┐
   (one source per layer)               │
                                        ▼
                       ┌──────────────────────────┐
                       │ PODIUM REGISTRY          │
                       │  HTTP/JSON API           │
                       │  Postgres + pgvector     │
                       │  layer composition       │
                       │  visibility filtering    │
                       │  dependency graph        │
                       └─────────────▲────────────┘
                                     │
                  OAuth-attested identity (every call)
                                     │
       ┌─────────────────────────────┼─────────────────────────────┐
       │                             │                             │
┌──────┴───────┐           ┌─────────┴──────┐         ┌───────────┴─────┐
│ Language SDKs│           │ MCP server     │         │ podium sync     │
│ (py, ts)     │           │ (in-process)   │         │ (filesystem)    │
└──────────────┘           └────────────────┘         └─────────────────┘
LangChain, Bedrock,        Claude Code, Cursor,        File-based
custom orchestrators       OpenCode, Pi, Hermes        harnesses
```

| Component         | Role                                                                                                                                                                                                                                              |
| :---------------- | :------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **Registry**      | System of record. Composes the caller's effective view from the layer list, applies per-layer visibility, indexes manifests, runs hybrid search, signs URLs, maintains the cross-type dependency graph, emits change events.                      |
| **MCP server**    | In-process bridge for MCP-speaking hosts. Exposes the four meta-tools. Holds no per-session server-side state — only a content-addressed disk cache, OS-keychain credentials, an in-memory local-overlay index, and the materialized working set. |
| **`podium sync`** | CLI (and library) that reads the user's effective view and writes it to a host-configured layout via the configured `HarnessAdapter`. Works against either an HTTP registry or a filesystem-source registry.                                      |
| **Language SDKs** | Thin HTTP clients. Used by LangChain, Bedrock, OpenAI Assistants, custom orchestrators, eval harnesses, build pipelines, notebooks.                                                                                                               |

---

## When Podium helps

Podium is overkill for a small catalog in a single harness with one
author — a flat directory plus the harness's native conventions handles
that. It becomes valuable as any of these dimensions grow:

- **Catalog size.** Lazy discovery and per-domain navigation help once
  the working set no longer fits comfortably in a system prompt.
- **Cross-harness delivery.** "Author once, deliver anywhere" pays off
  even at small scale once you target more than one harness.
- **Multiple artifact types.** A single dependency graph across skills,
  agents, contexts, commands, rules, hooks, and MCP server registrations
  beats N type-specific stores.
- **Multiple contributors.** Per-layer visibility, classification, and
  audit start to pay off as the number of contributors and the diversity
  of audiences grow.

---

## Documentation

- [Documentation site](https://OWNER.github.io/podium) — Jekyll +
  Just The Docs theme.
- [Specification](spec/) — comprehensive technical reference, one file
  per top-level section. Start at [`spec/README.md`](spec/README.md).
- [Roadmap](ROADMAP.md) — short-horizon priorities.
- [Contributing](CONTRIBUTING.md) — how to contribute today.
- [Governance](GOVERNANCE.md) — how decisions are made.
- [Security](SECURITY.md) — reporting vulnerabilities.

## Contributing

The specification is the source of truth and the most useful place to
push back. Today's highest-leverage contributions:

- **Open issues or discussions** — questions, disagreements with the
  spec, missing use cases.
- **Read the spec and stress-test it** — if a section is unclear or
  contradicts another, file an issue.
- **Sketch a harness adapter** — prototyping an adapter against the
  [adapter contract](spec/06-mcp-server.md#67-harness-adapters) helps
  surface gaps before they're expensive to fix.
- **Sketch a `LayerSourceProvider` plugin** — a custom source backend
  (S3, OCI, internal CMS) against the
  [SPI](spec/09-extensibility.md) helps validate that surface.
- **Fix typos and broken links** — small documentation PRs are welcome
  any time.

See [`CONTRIBUTING.md`](CONTRIBUTING.md) and [`GOVERNANCE.md`](GOVERNANCE.md).

## License

[MIT](LICENSE)
