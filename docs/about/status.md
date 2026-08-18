---
title: Implementation status
nav_order: 1
description: What's built today, what's published, and where to track progress.
---

# Implementation status

Podium is at **0.3.x**, an early release. The v1 surface is implemented and published. The surface and its behavior may still change before 1.0, so pin a specific version in production and watch the [changelog](changelog) for breaking changes.

Install via Homebrew, Scoop, container, or direct binary download. [Quickstart](../getting-started/quickstart#1-install-the-cli) has the commands.

---

## What's built

The implementation covers:

- **The local tier** end-to-end: `podium sync` against a catalog directory on disk, composing that directory's ordered layers, with the built-in harness adapters.
- **The single-node and clustered tiers** end-to-end: `podium serve` (single node on embedded SQLite and `sqlite-vec`, clustered on Postgres, `pgvector`, and S3-compatible object storage), the registry HTTP API, `LayerComposer`, visibility filtering, OIDC and SCIM, domain composition, immutability and versioning, the layer CLI, signing, the workspace overlay with local BM25 search, the dependency graph and impact analysis, the registry audit log with hash-chain integrity, and the meta-tools (`search_domains`, `search_artifacts`, `load_domain`, `load_artifact`).
- **MCP server**: `podium-mcp` with the meta-tool surface, materialization through the configured harness adapter, and identity-aware loading.
- **Marketplace publishing**: a `podium sync` target of `kind: marketplace` renders the catalog into harness-native git-repo distributions (the Claude, Codex, and Cursor plugin marketplaces, the Gemini extension, the Pi package, and the Hermes tap) and runs an operator-configured git workflow to push them. See [Marketplace publishing](../consuming/publishing).
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
| Python SDK | [`podium-sdk` on PyPI](https://pypi.org/project/podium-sdk/), imported as `from podium import …` |
| TypeScript SDK | [`@lennylabs/podium-sdk` on npm](https://www.npmjs.com/package/@lennylabs/podium-sdk) |

## On the roadmap toward 1.0

Behavior between 0.3.x and 1.0 may break. Watch the [changelog](changelog) for specifics. Topics under active discussion:

- Tightening the spec where field semantics are still under-specified.
- Adapters for harnesses outside the current built-in roster, with conformance coverage for each.
- SBOM and SLSA attestations on every release.
- Code signing for macOS and Windows binaries.

---

## What contributions help most today

- **Run the suite.** Build from source, run `make test`, and report failures or environment-specific issues.
- **Sketch a harness adapter.** Prototyping an adapter for a new harness helps validate the `HarnessAdapter` SPI.
- **Sketch a `LayerSourceProvider` plugin.** A custom source backend (S3, OCI, internal CMS) helps validate that SPI surface.
- **Comparisons and use cases.** Report where Podium does or does not fit a workflow.
- **Security review.** Threat-model the design and report findings related to identity, audit, signing, and visibility.
- **Documentation fixes.** Small PRs for typos, broken links, or unclear passages are welcome anytime.

---

## How to track progress

- **Commits on `main`** show the current work: [github.com/lennylabs/podium/commits/main](https://github.com/lennylabs/podium/commits/main). Each tagged release has an entry in [`CHANGELOG.md`](https://github.com/lennylabs/podium/blob/main/CHANGELOG.md).
- **Open issues and discussions** at [github.com/lennylabs/podium](https://github.com/lennylabs/podium) capture the current conversation about design and direction.
- **The test suite** is the most precise picture of what's wired up. Run `make test` and inspect the reporters under `tools/` for coverage breakdowns.
