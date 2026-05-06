# Podium

> A control plane for managing and serving AI agent artifacts at scale.

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

Podium is a registry and discovery layer for the artifacts AI agents actually use — skills, agents, contexts, prompts, and MCP server registrations — across multiple harnesses, multiple teams, and multiple types.

Authoring lives in Git. The registry mirrors what's been merged into a content-addressed store and serves it to three consumer paths: an SDK, an MCP bridge, and `podium sync`. Visibility is enforced per layer; lazy materialization keeps the working set small even when the catalog has thousands of entries.

## Status

Podium is in the **design phase**. The technical specification is the source of truth: [`spec/spec.md`](spec/spec.md). Implementation work is sequenced in the [build sequence](spec/spec.md#10-mvp-build-sequence). There is no shipped code yet.

The most useful contributions today are **design feedback on the spec** — questions, disagreements, missing use cases, edge cases that aren't handled.

## What Podium does

- **Author once, deliver anywhere.** Artifacts are written in a single canonical format. A pluggable `HarnessAdapter` translates them into Claude Code, Claude Desktop, Cursor, Gemini, Codex, OpenCode, and similar harness-native formats at delivery time.
- **Type-heterogeneous.** Skills, agents, contexts, prompts, and MCP server registrations are first-class. Cross-type dependencies (`extends:`, `delegates_to:`, `mcpServers:`) are tracked in a single graph.
- **Multi-layer composition.** An ordered list of layers (admin-defined, user-defined, workspace local) composes per-request, with deterministic merge and explicit precedence. `extends:` lets a higher-precedence artifact inherit and refine a lower one without forking.
- **Visibility per layer.** Each layer declares its own visibility (`public`, `organization`, OIDC `groups`, explicit `users`). Authoring rights live in the Git provider's branch protection — Podium does not duplicate them.
- **Lazy materialization.** Sessions can start empty and load only what the agent decides to use. Catalogs of thousands of artifacts don't pollute the system prompt.
- **Hybrid retrieval.** BM25 + vector search fused via reciprocal rank fusion. Built-in vector backends: `pgvector`, `sqlite-vec`, Pinecone, Weaviate Cloud, Qdrant Cloud. Built-in embedding providers: `embedded-onnx`, `openai`, `voyage`, `cohere`, `ollama`.
- **Solo or standard.** `podium serve --solo` runs as a single binary with embedded SQLite + sqlite-vec + a bundled embedding model — works offline. Standard deployments use Postgres, S3-compatible object storage, an OIDC IdP, and webhook-driven Git ingest.

## Quick example

A minimal artifact:

```
finance/close-reporting/run-variance-analysis/
└── ARTIFACT.md
```

```markdown
---
type: skill
name: run-variance-analysis
version: 1.0.0
description: Flag unusual variance vs. forecast after month-end close.
when_to_use:
  - "After month-end close, when reviewing financial performance"
tags: [finance, close, variance]
sensitivity: low
---

Compare actuals vs. forecast for the most recent close period. For each line
item, flag variances above the threshold defined in your team's policy doc.
Output a markdown table sorted by absolute variance.
```

The author commits this to a tracked Git ref. The registry ingests on webhook. In an agent session:

```
load_domain("finance/close-reporting")
→ {domains: [...], artifacts: [{id: "finance/close-reporting/run-variance-analysis", ...}]}

load_artifact("finance/close-reporting/run-variance-analysis")
→ {manifest: <prose body>, materialized_at: "/workspace/.podium/runtime/.../ARTIFACT.md"}
```

The agent now has the skill in its working set. Done.

## Architecture

```
   Git repos / local paths ──────────┐
   (one per layer)                   │
                                     ▼
                       ┌───────────────────────────┐
                       │ PODIUM REGISTRY (service) │
                       │  HTTP/JSON API            │
                       │  Postgres + pgvector      │
                       │  layer composition +      │
                       │    visibility filtering   │
                       │  dependency graph         │
                       └─────────────▲─────────────┘
                                     │
                  OAuth-attested identity (every call)
                                     │
        ┌────────────────────────────┼────────────────────────────┐
        │                            │                            │
┌───────┴────────┐          ┌────────┴────────┐         ┌─────────┴────────┐
│ Language SDKs  │          │ MCP server      │         │ podium sync      │
│ (py, ts)       │          │ (in-process)    │         │ (filesystem)     │
└────────────────┘          └─────────────────┘         └──────────────────┘
LangChain, Bedrock,         Claude Desktop,             File-based harnesses,
custom orchestrators        Claude Code, Cursor         eager materialization
```

## When Podium helps

Podium is overkill for a small catalog in a single harness with one author — a flat directory plus the harness's native conventions handles that. Becomes valuable as any of these dimensions grow:

- **Catalog size.** Lazy discovery and per-domain navigation help once the working set no longer fits comfortably in a system prompt.
- **Cross-harness delivery.** "Author once, deliver anywhere" is useful even at small scale once a team is targeting more than one harness.
- **Multiple artifact types.** A single dependency graph across skills, agents, contexts, prompts, and MCP server registrations beats N type-specific stores.
- **Multiple contributors.** Per-layer visibility, classification, and audit start to pay off as the number of contributors and the diversity of audiences grow.

## Documentation

- **Specification** — [`spec/spec.md`](spec/spec.md). Complete technical reference. Read this if you want to understand how Podium works in detail.
- **Quickstart** — [`spec/spec.md#0-quickstart`](spec/spec.md#0-quickstart).
- **Architecture** — [`spec/spec.md#2-architecture`](spec/spec.md#2-architecture).
- **Layer model** — [`spec/spec.md#46-layers-and-visibility`](spec/spec.md#46-layers-and-visibility).
- **Build sequence** — [`spec/spec.md#10-mvp-build-sequence`](spec/spec.md#10-mvp-build-sequence).
- **Backend configuration** — [`spec/spec.md#1311-backend-configuration-reference`](spec/spec.md#1311-backend-configuration-reference).

## Contributing

The specification is the source of truth and the most useful place to push back. Ways to contribute today:

- **Open issues or discussions** — questions, disagreements with the spec, missing use cases. Issues and discussions are at the project's GitHub.
- **Read the spec and stress-test it** — if a section is unclear or contradicts another, file an issue.
- **Sketch a runtime adapter** — prototyping an adapter against the [adapter contract](spec/spec.md#67-harness-adapters) helps surface gaps before they're expensive to fix.
- **Fix typos and broken links** — small documentation PRs are welcome any time.

See [`CONTRIBUTING.md`](CONTRIBUTING.md) for the full guide and [`GOVERNANCE.md`](GOVERNANCE.md) for how decisions are made. Security issues: see [`SECURITY.md`](SECURITY.md).

## License

[MIT](LICENSE).
