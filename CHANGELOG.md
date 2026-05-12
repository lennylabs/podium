# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

[Unreleased]: https://github.com/lennylabs/podium/compare/v0.1.1...HEAD

## [0.1.1] - 2026-05-11

Release-pipeline fixes. The v0.1.0 tag was created but never produced
published artifacts (PyPI, npm, GHCR) because of a sequence of CI
configuration failures; v0.1.1 is the first version where the release
workflow runs end-to-end. The behavior of the code itself is unchanged
from what v0.1.0 was supposed to ship — see the [0.1.0] section below
for the feature list.

### Release-pipeline fixes since v0.1.0

- Container builder switched from alpine/musl to debian/glibc;
  sqlite-vec.c uses BSD type names that musl doesn't provide.
- Cross-compile matrix now uses CGO_ENABLED=1 with per-target
  toolchains (gcc on linux/amd64, gcc-aarch64-linux-gnu on
  linux/arm64, mingw on windows/amd64, native clang on darwin/arm64).
- Windows binary build moved to a windows-latest runner with a
  workflow step that fetches sqlite3.h from the SQLite amalgamation.
- npm package gains `repository` / `homepage` / `bugs` / `keywords`
  fields so npm provenance verification accepts the publish.
- Postgres schema gained the `signature` column that the store
  queries already referenced.
- MinIO service swapped from the now-vanished bitnami tag to the
  official `minio/minio` image, with bucket creation via `mc mb` in
  a workflow step.
- A flaky scheduler test that raced with `t.TempDir` cleanup now
  waits for the goroutine to finish on cancel.

[0.1.1]: https://github.com/lennylabs/podium/releases/tag/v0.1.1

## [0.1.0] - 2026-05-11

Initial release. Covers the full v1 surface described in the project specification, across three binaries (`podium`, `podium-server`, `podium-mcp`) and two SDKs (`podium-sdk` on PyPI, `@lennylabs/podium-sdk` on npm).

### What's included

- **Filesystem mode**: `podium sync` materializes an effective view from a local artifact directory through the configured `HarnessAdapter`. Built-in adapters: `none`, `claude-code`, `claude-desktop`, `claude-cowork`, `cursor`, `codex`, `gemini`, `opencode`, `pi`, `hermes`.
- **Server mode**: `podium serve` runs the registry HTTP API. Standalone bootstrap uses embedded SQLite + `sqlite-vec`; standard deployment wires Postgres + `pgvector` + S3-compatible object storage + an OIDC identity provider.
- **`LayerComposer`** with visibility filtering across `public` / `organization` / OIDC `groups` / explicit `users`.
- **Domain composition**: `DOMAIN.md` parsing, glob resolution, cross-layer merge, `extends:` resolution, discovery rendering.
- **Versioning and immutability**: semver, content-hash cache keys, `latest` resolution with `session_id` consistency, tolerant force-push handling.
- **Workspace overlay** with local BM25 search alongside the registry's hybrid retrieval.
- **MCP server**: `podium-mcp` exposes `search_artifacts`, `load_artifact`, `search_domains`, `load_domain` with materialization through the configured adapter.
- **Identity**: OAuth device-code flow with OS keychain storage; injected-session-token flow for service runtimes.
- **SCIM 2.0** + OIDC group claim mapping.
- **Audit log** with hash-chain integrity, retention policies, and GDPR right-to-be-forgotten.
- **Signing**: Sigstore keyless by default; pluggable `SignatureProvider`.
- **Dependency graph**: cross-type reverse index + impact analysis CLI.
- **SDKs**: `podium-sdk` (Python) and `@lennylabs/podium-sdk` (TypeScript) as thin HTTP clients.
- **Plugin surface**: every SPI documented in `docs/deployment/extending.md`, including `LayerSourceProvider`, `GitProvider`, `IdentityProvider`, `HarnessAdapter`, `MaterializationHook`, `SignatureProvider`, `NotificationProvider`, plus search and embedding providers.

[0.1.0]: https://github.com/lennylabs/podium/releases/tag/v0.1.0
