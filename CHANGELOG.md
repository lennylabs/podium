# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

[Unreleased]: https://github.com/lennylabs/podium/compare/v0.2.0...HEAD

## [0.2.0] - 2026-06-29

Marketplace publishing: a `podium sync` target of `kind: marketplace` renders the catalog into a harness-native git-repo marketplace and runs an operator-configured workflow to push it to a remote.

### Added

- **Marketplace publishing** (§7.5.2, §7.8): a `podium sync` target of `kind: marketplace` renders the effective view into a harness-native git-repo distribution and runs an operator-configured `workflow` of shell commands to clone, commit, and push it to a git remote. One repository carries the Claude (Code, Desktop, Cowork), Codex, and Cursor plugin-marketplace manifests at their fixed locations, while Gemini (extension), Pi (package), and Hermes (tap) take their own repository. The `plugins:` list groups artifacts by scope filter, and the publishing `identity:` governs the visibility-filtered effective view that reaches the marketplace. Podium renders to a folder and never holds a git push credential. `podium sync --config` runs the prepare, render, and publish pipeline per target, and `--check` and `--dry-run` write nothing.
- The `HarnessAdapter` `Source` carries a plugin descriptor, so an adapter can render an artifact into a named plugin (§6.7, §9.1).

### Changed

- **`claude-cowork` is publish-only** (§6.7, §6.7.1): the cowork adapter no longer materializes the plugin-layout artifact types (skill, agent, command, rule, hook, and mcp-server) through `podium sync`; they reach Claude Cowork through a `kind: marketplace` marketplace instead. A `type: context` artifact still materializes to `.podium/context/` under `podium sync`. The §6.7.1 capability cells for `claude-cowork` are regraded to unsupported for the affected rows.
- `podium sync` enforces the §6.9 untranslatable rule: a target whose harness cannot represent a selected artifact fails rather than silently skipping it, matching `load_artifact`.

### Documentation

- Added a marketplace-publishing guide and a publish-flow diagram, and reframed the harness, CLI, and error-code references onto the `kind: marketplace` sync target.

[0.2.0]: https://github.com/lennylabs/podium/releases/tag/v0.2.0

## [0.1.6] - 2026-06-17

The `podium-mcp` stdio server returns a spec-compliant MCP `CallToolResult`, so hosts that render `result.content` show meta-tool output instead of an empty result.

### Fixed

- **MCP `tools/call` result format** (§6.1.1): `podium-mcp` returns each meta-tool result as an MCP `CallToolResult`. The domain object is carried in `structuredContent` and mirrored as a `content[]` text block, and a §6.10 error envelope sets `isError`. Hosts that render `result.content` (Claude Code, Claude Desktop, Cursor, and VS Code) previously received no content and showed an empty result for `search_artifacts`; they now display the output. The meta-tool fields move from the result top level to `structuredContent`.

### Documentation

- Documented the `tools/call` result format in §6.1.1, and corrected the §5 `load_artifact` description so it states materialization writes the adapted body as a harness-native file in addition to any bundled resources.

[0.1.6]: https://github.com/lennylabs/podium/releases/tag/v0.1.6

## [0.1.5] - 2026-06-10

A standalone server pointed at a filesystem registry now honors per-layer `.layer-config` visibility at boot, instead of stamping one deployment default on every layer.

### Fixed

- **Standalone bootstrap** (§4.6, §13.11.1): a `PODIUM_LAYER_PATH` filesystem registry served by a standalone server applies each layer's declared `.layer-config` visibility. A layer that declares a non-empty visibility boots with it; a layer with no `.layer-config`, or one whose `visibility:` block is empty, falls back to the deployment default (`PODIUM_DEFAULT_LAYER_VISIBILITY`), matching how a declarative `layers:` entry resolves an empty block.

### Documentation

- Documented the optional per-layer `.layer-config` file and its `visibility:` schema in the filesystem-registry directory layout (§13.11.1) and the solo/filesystem deployment guide.

[0.1.5]: https://github.com/lennylabs/podium/releases/tag/v0.1.5

## [0.1.4] - 2026-06-08

Multi-tenancy and gateway-delegated authentication. Two design proposals land: server-side request authentication for a registry behind an identity-aware gateway (proposal 0001), and runtime tenant provisioning through an operator-authorized API and CLI (proposal 0002). The boot-time `PODIUM_TENANTS` environment variable is replaced by the runtime provisioning path.

### Added

- **Server-side request authentication** (§6.3.3, proposal 0001): the `oidc-jwt` and `trusted-headers` identity providers authenticate each caller from a gateway-forwarded token or trusted request headers, selected by `PODIUM_IDENTITY_PROVIDER`. The caller's organization comes from the verified `org_id` claim or the `X-Podium-User-Org` header.
- **Per-request multi-tenant routing** (§6.3.1): a registry started with `PODIUM_MULTI_TENANT` resolves each request to the tenant its organization names, and rejects an organization that names no provisioned tenant with `auth.tenant_unknown`. A single-tenant registry binds every request to its sole tenant and does not consult the organization value.
- **Runtime tenant provisioning** (§7.3.3, proposal 0002): the operator-authorized `/v1/admin/tenants` API and the `podium admin tenant` CLI create, list, update, and deactivate tenants on a live multi-tenant registry. The instance-operator role is seeded with `PODIUM_OPERATOR_ADMINS` and is distinct from the per-tenant `admin` role. Per-tenant quotas and the §3.5 scope-preview gate are set at create or update, and create is idempotent. Deactivation is soft: a deactivated tenant stops resolving while its data persists, and reactivation restores it.

### Changed

- `podium domain analyze` takes the path as a positional argument (`podium domain analyze <path>`), matching `podium domain show` and `podium domain search`.

### Removed

- The boot-time `PODIUM_TENANTS` environment variable and the boot-time tenant-provisioning path. A multi-tenant deployment seeds its first operator with `PODIUM_OPERATOR_ADMINS` and provisions tenants at runtime through the API or CLI.
- The `lint.hook_generic_and_subtype` lint rule, which rejected a hook that declared both a generic tool-call event and a subtype event. The rule could not be enforced across independently authored layers, and declaring both events is a legitimate pattern.

### Fixed

- **SDKs** (§7.2): `load_artifact` content above the 256 KB inline cutoff on a single load is fetched from the presigned manifest-body URL instead of failing (`podium-py`, `podium-ts`).
- **Store** (§4.7.1): `Memory.CreateTenant` is idempotent, matching the SQLite and Postgres backends, so re-creating an existing tenant leaves the stored row unchanged.
- **Registry**: graceful shutdown runs through a single server lifecycle context.

### Documentation

- Clarified what `load_artifact` returns inline versus what materializes to disk, for the MCP server and the SDKs (§6.6, §6.7).
- Corrected the CLI, HTTP API, error-code, and authoring references against the implementation.

[0.1.4]: https://github.com/lennylabs/podium/releases/tag/v0.1.4

## [0.1.3] - 2026-06-04

Spec-conformance and reliability release. The bulk of the work reconciles the implementation with the specification across the registry, CLI, MCP bridge, and SDKs, and builds out the test infrastructure that verifies it (live integration lanes for Postgres, S3, and the managed vector backends; spec, doc, and matrix coverage gates; and a hand- and agent-runnable end-to-end validation suite). The user-facing changes are grouped below by area; the internal test and CI work is omitted.

### Added

- **Managed vector backends**: Pinecone, Weaviate Cloud, and Qdrant Cloud, alongside the existing `sqlite-vec` and `pgvector`, with both externally-computed embeddings and backend-side integrated inference.
- **Observability** (§13.8): an opt-in Prometheus `/metrics` endpoint on the registry and the MCP bridge, and OpenTelemetry trace export with W3C context propagation.
- **Per-tenant daily audit-volume quota** (§4.7.8) and **reverse-dependency in-degree ranking** in search (§4.7.3).
- **Transactional vector outbox** with a drain worker, and **per-row embedding-model versioning** with a mixed-model query restriction (§4.7, §4.7.2).
- **Consumer-side `verify_signatures` default** read from `sync.yaml` for standalone deployments (§13.10), and **config-merge / managed-marker materialization ops** (§6.7).

### Changed

- `podium status` and `podium config show` resolve the registry and harness from the merged `sync.yaml` (the flag, then the environment, then the config), not only from environment variables; `config show` hints when no configuration is in scope and surfaces effective server settings under `--server`.
- The MCP bridge negotiates down to an older MCP protocol version, rejects a filesystem-source registry, and refuses an incompatible client version (§6.1, §6.9).

### Fixed

- **Artifact model, ingest, and lint** (§4.1–§4.4): the type system and sizing lint, canonical IDs and the resource boundary, manifest schema parsing, skill and hook ingest lint, prose artifact-reference resolution, document-source provenance, URL status checks, the seccomp baseline, DOMAIN.md body-size lint, and configurable bundled-resource caps; binary inline resources are base64-encoded and served without an object store.
- **Domains** (§4.5): `DOMAIN.md` composition is ingested and applied at `load_domain`, with discovery rendering, tenant config, and imports.
- **Layers, visibility, and versioning** (§4.6, §4.7): extends-merge / collision / visibility composition, the per-identity user-defined layer cap, runtime layer resolution, embedding projection and version resolution, `replaced_by` recovery on load for the SQL backends, and extends-pinned-parent protection from deprecated-version purge. A same-ID `extends` overlay from a lower-precedence layer is no longer rejected as a self-extends cycle.
- **Meta-tools and MCP bridge** (§5, §6): verbatim §5.1 tool descriptions and input schemas, the §6.6 materialization pipeline (content-hash verification, hook script path, rule fidelity), the §6.5 resolution cache (TTL, HEAD revalidation, prune safety), the §6.4 workspace overlay (watch / re-index, fused `total_matched`), per-harness materialization targets (§6.7 — codex hooks into `config.toml`, cowork buckets, config-merge ownership so gemini accepts `mcpServers`), the §6.2 server config env vars, and the §6.10 structured error envelope. The content cache now persists `skill_raw` and the sensitivity/signature envelope, fixing a `content_hash_mismatch` and a skipped signature check on cache hits. `search_artifacts` `total_matched` counts vector-only hits, and the hybrid BM25 half indexes only the §4.7 searchable projection (name, description, when_to_use, tags) with stopword filtering, so a paraphrased query ranks by vector similarity.
- **External integration and sync** (§7): §7.2 bundled-resource delivery and the presigned manifest-body channel above the inline cutoff, §7.3 inbound webhook and reingest pipeline (`last_ingested_at`, `force_push_policy`, break-glass, webhook-secret rotation and redaction), §7.4 degraded-network cache-mode fallback across the bridge / sync / SDKs, §7.5.2 sync honoring `PODIUM_HARNESS` with profile / scope and lock provenance, §7.6 read CLI and SDK `--json` schemas and caller-credential propagation, and §7.7 onboarding (`init` walk-up / wizard / hints, login resolution). `cache prune --days 0` is accepted as the "older than now" boundary.
- **Identity and scope preview** (§6.3, §3.5): injected-session-token verification, device-code, scope and group mapping, `aud` enforcement, and token watch; scope-preview endpoint correctness and the tenant gate, surfaced in `status` / `sync` / MCP.
- **Audit and observability** (§8, §12, §13.7, §13.9): registry audit events under dotted `caller.*` keys, §8.2 PII redaction, §8.4 sampling / retention / re-anchor, §8.5 right-to-be-forgotten erasure (purge, redaction, tombstone, salt guard), §8.6 gap-detection scheduling, immutable `Cache-Control` on content-addressed reads, §13.9 health and readiness probes, and §12 offline status / ETag revalidation / learn-from-usage rerank.
- **Deployment and config** (§13, §14): the §13.1.1 evaluation compose stack (registry, Dex, bootstrap-admin seeding), §13.2 read-only write rejection / public-mode bind guard / sensitivity ceiling / read-only probe and recovery, §13.4 `migrate-to-standard` short-form flags and standalone-tenant resolution, §13.10 standalone zero-flag and first-run `~/.podium/sync.yaml` auto-bootstrap, §13.11 fsnotify watch and filesystem `extends`, and §14.9 / §14.10 enterprise-layer register-class inference and `layer watch --interval`.
- **Retrieval and SPIs** (§3.2, §3.3, §9): hybrid domain search with vector-only fusion, description-quality advisories with MCP session correlation, the §9.1 operational notification on ingest failure, context-first SPI signatures, and a structured SPI error envelope.

### Security

- The `/objects/{content_hash}` data-plane route was exempt from identity verification and served restricted bytes to any caller. Visibility is now enforced on that route, and S3 presigned URLs no longer embed credentials.

[0.1.3]: https://github.com/lennylabs/podium/releases/tag/v0.1.3

## [0.1.2] - 2026-05-11

Distribution-channel additions. No changes to the CLI surface; the binaries themselves are bit-identical to v0.1.1 (modulo the embedded version string).

### Added

- **Per-platform archives** alongside the individual binaries on each GitHub Release: `podium-<os>-<arch>.tar.gz` (Linux + macOS) and `podium-windows-amd64.zip`, each containing `podium`, `podium-server`, and `podium-mcp` with their canonical un-suffixed names. The individual binaries are still attached; the archives are additive for package-manager consumers.
- **Homebrew tap and Scoop bucket update job** in `release.yml`. On each tag push, the workflow patches `Formula/podium.rb` in `lennylabs/homebrew-tap` and `bucket/podium.json` in `lennylabs/scoop-bucket` so `brew install podium` / `scoop install podium` track the latest release. Both auxiliary repos are org-wide — one repo per package manager, one file per Lenny Labs project.
- **`TAP_BUCKET_TOKEN`** repo secret requirement, documented in OPERATIONS.md.

[0.1.2]: https://github.com/lennylabs/podium/releases/tag/v0.1.2

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
