---
title: Implementation status
nav_order: 1
description: What Podium implements today, where each release is published, and how to track progress.
---

# Implementation status

This page records what Podium implements today, where each release is published, and how to follow the work toward 1.0.

Podium is at **0.6.x**, an early release. The v1 surface is implemented and published. The surface and its behavior may still change before 1.0, so pin a specific version in production and watch the [changelog](changelog) for breaking changes.

Install the CLI through Homebrew, Scoop, a direct binary download, or a build from source. [Quickstart](../getting-started/quickstart#1-install-the-cli) has the commands. The published container image carries only the `podium-server` registry binary.

---

## What's built

The implementation covers the following surfaces:

- **The local deployment mode**, end to end. `podium sync` runs against a catalog directory on disk, composes that directory's ordered layers, and materializes through the built-in harness adapters.
- **The single-node and clustered deployment modes**, end to end. The registry server, started with `podium serve` or the `podium-server` binary, runs a single node on embedded SQLite and `sqlite-vec`, and a clustered deployment on Postgres, `pgvector`, and S3-compatible object storage. Both modes include the registry HTTP API, `LayerComposer`, visibility filtering, OIDC and SCIM, domain composition, immutability and versioning, and the layer CLI. They also include signing, the workspace overlay with local BM25 search, the dependency graph and impact analysis, the registry audit log with hash-chain integrity, and the meta-tools (`search_domains`, `search_artifacts`, `load_domain`, and `load_artifact`).
- **The MCP server.** `podium-mcp` serves the meta-tools, materializes through the configured harness adapter, and loads artifacts according to the caller's identity.
- **Marketplace publishing.** A `podium sync` target of `kind: marketplace` renders the catalog into harness-native git-repo distributions: the Claude, Codex, and Cursor plugin marketplaces, the Gemini extension, the Pi package, and the Hermes tap. It then runs an operator-configured git workflow to push them. See [Publishing marketplace repositories](../deployment/content/publishing).
- **SDKs.** `podium-py` and `@lennylabs/podium-sdk` (TypeScript) are thin HTTP clients for programmatic runtimes.
- **The plugin surface.** Every SPI documented in [Extending](../deployment/services/extending) is implemented, including `LayerSourceProvider`, `GitProvider`, `IdentityProvider`, `HarnessAdapter`, `MaterializationHook`, `SignatureProvider`, `NotificationProvider`, and the search and embedding providers.

---

## What's shipped

| Artifact | Where |
|:--|:--|
| Binaries for Linux amd64/arm64, macOS arm64, Windows amd64 | [GitHub Releases](https://github.com/lennylabs/podium/releases/latest) |
| Homebrew formula (`brew tap lennylabs/tap && brew install podium`) | [github.com/lennylabs/homebrew-tap](https://github.com/lennylabs/homebrew-tap) |
| Scoop manifest (`scoop bucket add lennylabs https://github.com/lennylabs/scoop-bucket`, then `scoop install podium`) | [github.com/lennylabs/scoop-bucket](https://github.com/lennylabs/scoop-bucket) |
| Container image | `ghcr.io/lennylabs/podium-server` |
| Python SDK | [`podium-sdk` on PyPI](https://pypi.org/project/podium-sdk/), imported as `from podium import …` |
| TypeScript SDK | [`@lennylabs/podium-sdk` on npm](https://www.npmjs.com/package/@lennylabs/podium-sdk) |

## On the roadmap toward 1.0

Behavior between 0.6.x and 1.0 may break, and the [changelog](changelog) records each change. These topics are under active discussion:

- Tightening the spec where field semantics are still under-specified.
- Adapters for harnesses outside the current built-in roster, with conformance coverage for each.
- SBOM and SLSA attestations on every release.
- Code signing for macOS and Windows binaries.

---

## What contributions help most today

- **Run the suite.** Build from source, run `make test`, and report failures or environment-specific issues.
- **Sketch a harness adapter.** A prototype adapter for a new harness tests the `HarnessAdapter` SPI.
- **Sketch a `LayerSourceProvider` plugin.** A custom source backend (S3, OCI, or an internal CMS) tests that SPI.
- **Comparisons and use cases.** Report where Podium does or does not fit a workflow.
- **Security review.** Threat-model the design, and report findings related to identity, audit, signing, and visibility.
- **Documentation fixes.** Small PRs for typos, broken links, or unclear passages are welcome at any time.

---

## How to track progress

- **Commits on `main`** show the current work at [github.com/lennylabs/podium/commits/main](https://github.com/lennylabs/podium/commits/main). Each tagged release has an entry in [`CHANGELOG.md`](https://github.com/lennylabs/podium/blob/main/CHANGELOG.md).
- **Open issues and discussions** at [github.com/lennylabs/podium](https://github.com/lennylabs/podium) record the current conversation about design and direction.
- **The test suite** shows precisely what is wired up. Run `make test`, and inspect the reporters under `tools/` for coverage breakdowns.
