---
layout: default
title: Implementation status
parent: About
nav_order: 2
description: What's built today, what's pending release, and where to track progress.
---

# Implementation status

Podium is at **0.1.x**, an early release. The full v1 surface is implemented and published, but surface and behavior may still shift before 1.0 — pin to specific versions in production environments and watch the [changelog](changelog) for breaking changes.

Install via Homebrew, Scoop, container, or direct binary download — see [Quickstart](../getting-started/quickstart#1-install-the-cli) for the commands.

---

## What's built

The initial implementation covers:

- **Filesystem mode** end-to-end: `podium sync` against a local artifact directory, with the full set of built-in harness adapters.
- **Server mode** end-to-end: `podium serve` (standalone via embedded SQLite + `sqlite-vec`, or standard via Postgres + `pgvector` + S3-compatible object storage), the registry HTTP API, `LayerComposer`, visibility filtering, OIDC + SCIM, domain composition, immutability and versioning, layer CLI, signing, the workspace overlay with local BM25 search, the dependency graph and impact analysis, the registry audit log with hash-chain integrity, and the meta-tools (`search_domains`, `search_artifacts`, `load_domain`, `load_artifact`).
- **MCP server**: `podium-mcp` with the meta-tool surface, materialization through the configured harness adapter, and identity-aware loading.
- **SDKs**: `podium-py` and `@lennylabs/podium-sdk` (TypeScript) as thin HTTP clients for programmatic runtimes.
- **Plugin surface**: every SPI documented in [Extending](../deployment/extending), including the `LayerSourceProvider`, `GitProvider`, `IdentityProvider`, `HarnessAdapter`, `MaterializationHook`, `SignatureProvider`, `NotificationProvider`, and search/embedding providers.

---

## What's shipped

| Artifact | Where |
|:--|:--|
| Binaries for Linux amd64/arm64, macOS arm64, Windows amd64 | [GitHub Releases](https://github.com/lennylabs/podium/releases/latest) |
| Homebrew formula (`brew tap lennylabs/tap && brew install podium`) | [github.com/lennylabs/homebrew-tap](https://github.com/lennylabs/homebrew-tap) |
| Scoop manifest (`scoop install podium`) | [github.com/lennylabs/scoop-bucket](https://github.com/lennylabs/scoop-bucket) |
| Container image | `ghcr.io/lennylabs/podium-server` |
| Python SDK | [`podium-sdk` on PyPI](https://pypi.org/project/podium-sdk/) — imports as `from podium import …` |
| TypeScript SDK | [`@lennylabs/podium-sdk` on npm](https://www.npmjs.com/package/@lennylabs/podium-sdk) |

## On the roadmap toward 1.0

Behavior between 0.1.x and 1.0 may break. Watch the [changelog](changelog) for specifics. Topics under active discussion:

- Tightening the spec where field semantics are still under-specified.
- Filling in remaining harness adapters and verifying conformance.
- SBOM and SLSA attestations on every release.
- Code signing for macOS and Windows binaries.

---

## What contributions help most today

- **Run the suite.** Build from source, run `make test`, and report failures or environment-specific issues.
- **Sketch a harness adapter.** Prototyping an adapter for a new harness helps validate the adapter SPI shape.
- **Sketch a `LayerSourceProvider` plugin.** A custom source backend (S3, OCI, internal CMS) helps validate that SPI surface.
- **Comparisons and use cases.** Report where Podium does or does not fit a workflow.
- **Security review.** Threat-model the design and report findings related to identity, audit, signing, and visibility.
- **Documentation fixes.** Small PRs for typos, broken links, or unclear passages are welcome anytime.

---

## How to track progress

- **Commits on `initial-implementation`** show the current work: [github.com/lennylabs/podium/commits/initial-implementation](https://github.com/lennylabs/podium/commits/initial-implementation).
- **Open issues and discussions** at [github.com/lennylabs/podium](https://github.com/lennylabs/podium) capture the current conversation about design and direction.
- **The test suite** is the most precise picture of what's wired up. Run `make test` and inspect the reporters under `tools/` for coverage breakdowns.
