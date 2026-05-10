---
layout: default
title: Implementation status
parent: About
nav_order: 2
description: What's built today, what's pending release, and where to track progress.
---

# Implementation status

Podium is **pre-release**. The initial implementation covers the full v1 surface and lives on the `initial-implementation` branch. The branch has not been merged to `main` and no tagged release has been published; binaries are not yet distributed through package managers.

Run Podium today by [building from source](../about/contributing#development-setup). The full Go test suite runs in roughly 10 seconds.

---

## What's built

The initial implementation covers:

- **Filesystem mode** end-to-end: `podium sync` against a local artifact directory, with the full set of built-in harness adapters.
- **Server mode** end-to-end: `podium serve` (standalone via embedded SQLite + `sqlite-vec`, or standard via Postgres + `pgvector` + S3-compatible object storage), the registry HTTP API, `LayerComposer`, visibility filtering, OIDC + SCIM, domain composition, immutability and versioning, layer CLI, signing, the workspace overlay with local BM25 search, the dependency graph and impact analysis, the registry audit log with hash-chain integrity, and the meta-tools (`search_domains`, `search_artifacts`, `load_domain`, `load_artifact`).
- **MCP server**: `podium-mcp` with the meta-tool surface, materialization through the configured harness adapter, and identity-aware loading.
- **SDKs**: `podium-py` and `@podium/sdk` (TypeScript) as thin HTTP clients for programmatic runtimes.
- **Plugin surface**: every SPI documented in [Extending](../deployment/extending), including the `LayerSourceProvider`, `GitProvider`, `IdentityProvider`, `HarnessAdapter`, `MaterializationHook`, `SignatureProvider`, `NotificationProvider`, and search/embedding providers.

---

## What's pending release

- **Merge to `main`.** The `initial-implementation` branch lands on `main` once review settles.
- **Tagged release and packaging.** No version is tagged. Released binaries (Linux, macOS, Windows) and container images publish at the first tag.
- **PyPI and npm.** The SDKs install today via `pip install -e .` and `npm install` from a checkout. Public package publishing follows the first tag.
- **Hosted documentation refresh.** This site rebuilds from `main`; the published pages lag the branch.

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
