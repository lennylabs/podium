# Podium

**A registry for the artifacts AI agents use, and a way to deliver them
into any harness.**

Skills, commands, rules, agents, contexts, hooks, MCP server registrations
— write them once in markdown, serve them from one place, materialize them
into Claude Code, Cursor, OpenCode, Gemini, Codex, Pi, Hermes, or your own
runtime.

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

## What it is

You author artifacts in `ARTIFACT.md` files in a Git repo. Podium
mirrors them into a content-addressed store and serves them. Three
consumers read from that store:

- **MCP server** — your harness (Claude Code, Cursor, OpenCode, etc.)
  calls `load_domain` / `search_domains` / `search_artifacts` /
  `load_artifact` over MCP. Lazy: nothing in the agent's context until
  it's actually needed.
- **`podium sync`** — eager filesystem materialization. Walks your
  effective view, writes harness-native files. One-shot or watcher mode.
- **Language SDKs** (`podium-py`, `podium-ts`) — programmatic access for
  runtimes, eval harnesses, custom orchestrators.

You can run Podium three ways:

| Shape | For | What's running |
|:--|:--|:--|
| **Filesystem** | Solo, prototype, CI | `podium sync` reads a directory; no daemon, no port, no auth |
| **Standalone** | 3–10 person team | `podium serve --standalone` — single binary with embedded SQLite + sqlite-vec + a bundled embedding model |
| **Standard** | 20+ / multi-tenant / governed | Postgres + S3 + OIDC; per-layer visibility, signing, freeze windows, SCIM, hash-chained audit |

Same artifacts. Same author flow. Different operational shape.

---

## Quickstart

Filesystem mode — no daemon, no setup beyond a CLI:

```bash
# Install (target shape; not yet packaged)
brew install OWNER/tap/podium

# Point Podium at a folder; default to Claude Code as the harness
mkdir -p ~/podium-artifacts/personal
podium init --global --registry ~/podium-artifacts/ --harness claude-code

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

# Materialize into Claude Code's directory
cd ~/projects/your-project
podium sync --target .claude/
```

Open Claude Code in the project. The skill is available.

[Full quickstart](https://OWNER.github.io/podium/getting-started/quickstart)

---

## What's included

- **Seven first-class artifact types.** `skill`, `agent`, `context`,
  `command`, `rule`, `hook`, and `mcp-server`. Extension types register
  through a `TypeProvider` SPI.
- **Eight built-in harness adapters.** `claude-code`, `claude-desktop`,
  `cursor`, `gemini`, `opencode`, `codex`, `pi`, `hermes`, plus `none`
  for raw output.
- **Layered composition.** Admin-defined, user-defined, and workspace-
  local overlay layers compose per request with deterministic merge.
  `extends:` lets a higher-precedence artifact inherit and refine a
  lower one without forking.
- **Visibility per layer.** `public`, `organization`, OIDC `groups`, or
  explicit `users`. Authoring rights stay in your Git host's branch
  protection.
- **Hybrid retrieval.** BM25 + vector embeddings via reciprocal rank
  fusion. Vector backends: pgvector, sqlite-vec, Pinecone, Weaviate
  Cloud, Qdrant Cloud. Embedding providers: openai, voyage, cohere,
  ollama, embedded-onnx.
- **17 SPIs.** Storage, identity, composition, signing, audit, layer
  source, delivery — every seam is pluggable. `MaterializationHook`
  for pre-write rewrites; `LayerSourceProvider` for non-Git layer
  sources (S3, OCI, HTTP archives).

Every capability is specified in [`spec/`](spec/) and covered by the
integration test suite.

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

| Component | Role |
|:--|:--|
| **Registry** | System of record. Composes the caller's effective view from the layer list, applies per-layer visibility, indexes manifests, runs hybrid search, signs URLs, maintains the cross-type dependency graph, emits change events. |
| **MCP server** | In-process bridge for MCP-speaking hosts. Exposes the four meta-tools. Holds no per-session server-side state — only a content-addressed disk cache, OS-keychain credentials, an in-memory local-overlay index, and the materialized working set. |
| **`podium sync`** | CLI (and library) that reads the user's effective view and writes it to a host-configured layout via the configured `HarnessAdapter`. Works against either an HTTP registry or a filesystem-source registry. |
| **Language SDKs** | Thin HTTP clients. Used by LangChain, Bedrock, OpenAI Assistants, custom orchestrators, eval harnesses, build pipelines, notebooks. |

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
