# Implementation status

Authoritative phase-by-phase classification of what's implemented
vs the spec. Updated to reflect the current state of
`initial-implementation`.

## Legend

- **REAL** — matches the spec; ready for production wiring with
  the relevant infrastructure backing it.
- **PARTIAL** — happy path works; specific corners (called out)
  remain.
- **OUT-OF-SCOPE** — explicitly carved out of the registry's
  responsibility, with rationale.

## Phase-by-phase

### Phase 0 — Filesystem-source `podium sync` (§13.11) — REAL

`pkg/sync.Run` walks a filesystem registry, applies layer
composition, runs the configured `HarnessAdapter`, and writes the
target atomically. `--watch` polls the registry + workspace
overlay and reruns on change. `--overlay` merges workspace overlay
records as the highest-precedence layer.

**Known gap.** A sync that drops an artifact does not delete the
files the previous sync wrote for it. See REMAINING_GAPS.md A4.

### Phase 1 — Manifest schema + `podium lint` + signing — REAL

`pkg/lint` ships seven rules covering required fields, SKILL.md
compliance, name syntax, semver, hook generic vs subtype, hint
type applicability. `pkg/sign` ships:

- `Noop` for default standalone deployments.
- `SigstoreKeyless`: Fulcio v2 cert mint via OIDC token, Rekor
  hashedrekord upload + presence check, x509 chain validation
  against a configurable trust root. Tier 1 tests use an
  in-process CA harness; Tier 2 live smoke gates on
  `PODIUM_SIGSTORE_*` env vars.
- `RegistryManagedKey`: Ed25519 with `KeyID`-aware rotation
  rejection.

`podium sign` / `podium verify` CLIs operate over any provider.

### Phase 2 — Registry HTTP API (§5) — REAL

`/v1/load_domain`, `/v1/search_domains`, `/v1/search_artifacts`,
`/v1/load_artifact`, `/v1/dependents`, `/v1/scope/preview`. Plus
`/v1/quota`, `/v1/admin/grants`, `/v1/admin/show-effective`,
`/v1/admin/reembed`, `/v1/events`, `/v1/webhooks`,
`/v1/domain/analyze`, `/objects/{key}`.

§4.1 large-resource path: resources above the 256 KB cutoff are
uploaded to the configured `pkg/objectstore` provider (Memory /
Filesystem / S3 via minio-go) and surfaced as URLs in
`large_resources`. Filesystem backend serves via authenticated
`/objects/{content_hash}`; S3 backend uses Signature V4 presigning.

Visibility filtering, `latest` resolution with session_id
consistency, hybrid BM25+vector ranking via RRF, structured §6.10
errors, public-mode + IdP guard. Read-only mode probe is
REMAINING_GAPS.md D3.

### Phase 3 — Sync upgrades + claude-code / codex — REAL

Lock file, scope filter (`*`, `**`, `{a,b}`), per-target lock,
override / save-as / profile-edit subcommands, `--watch`
(poll-based, configurable period and debounce), `--overlay`,
multi-type reference catalog, `podium init`.

### Phase 4 — MCP server + read CLI — REAL

`cmd/podium-mcp` runs the §6.6 materialization pipeline: fetch +
content cache (§6.5) + adapter + hook chain + atomic write.
Per-call `harness:` override, identity passthrough, protocol
version negotiation, workspace overlay short-circuit on
load_artifact, signature enforcement at materialize time
(`PODIUM_VERIFY_SIGNATURES`).

`podium search`, `domain show`, `domain search`, `domain analyze`,
`artifact show`, `init`, `status`, `login`, `logout`.

**Gap.** `podium-py` SDK is missing — REMAINING_GAPS.md C3.

### Phase 5 — Multi-tenant data model — REAL

Memory + SQLite + Postgres `pkg/store` backends share a
conformance suite (`pkg/store/storetest`). Postgres tests gate on
`PODIUM_POSTGRES_DSN`. Object storage SPI ships with Memory,
Filesystem, S3 backends. pgvector + sqlite-vec collocate with the
metadata store; Pinecone / Weaviate / Qdrant ship as alternatives.
`store.LayerConfig` carries `LastIngestedRef` + `ForcePushPolicy`.
`Quota` field is honored by `core.Quota` and surfaced via
`/v1/quota`. Schema migrations run on first open (idempotent
`CREATE TABLE IF NOT EXISTS` for SQLite; same shape for Postgres).

### Phase 6 — LayerSourceProvider — REAL

`pkg/layer/source` ships `local` (filesystem) and `git` (real
go-git fetch with full-history clone when `PriorRef` is set so
the §7.3.1 force-push detector can walk ancestry). Webhook
verification via `pkg/layer/webhook` covers GitHub / GitLab /
Bitbucket HMAC. The ingest pipeline runs lint, immutability check,
content-addressed storage, and §4.7.6 extends:-pin resolution.
Force-push tolerance: `LastIngestedRef` tracking, ancestry walk,
strict + tolerant policies, `layer.history_rewritten` event +
audit emission.

`podium layer reingest` triggers ingest; `podium layer watch`
polling is REMAINING_GAPS.md B3.

### Phase 7 — LayerComposer + visibility + OIDC + SCIM — REAL

`pkg/layer.Visible` + `EffectiveLayers` enforce §4.6 visibility on
every meta-tool call. The HTTP server filters per identity. OIDC
identity comes from the JWT (see Phase 11). SCIM 2.0 receiver at
`/scim/v2/` ships full Users + Groups CRUD + filter parser
(eq / sw / co / pr) + bearer-token auth.

**Known gap.** SCIM-pushed group memberships are stored but not
yet consulted by `layer.Visible` for group resolution. The
`groups: [engineering]` filter resolves only against JWT-supplied
groups today. REMAINING_GAPS.md A1.

`podium admin grant` / `revoke` / `show-effective` ship; admin
auth is enforced via `core.AdminAuthorize`.

### Phase 8 — Domain composition — REAL

`pkg/domain.MergeAcrossLayers` ships §4.5.4 merge rules. The glob
resolver inside `core.LoadDomain` honors `DOMAIN.md include:` /
`exclude:`. `extends:` resolution does cycle detection,
parent-version pinning at child ingest, and hidden-parent merging
at load_artifact. Discovery rendering: `max_depth`,
`fold_below_artifacts`, `fold_passthrough_chains`,
`target_response_tokens`, `notable_count`, `featured` ordering,
`deprioritize` ranking, rendering note.

`podium domain analyze` walks the visible subtree and reports
artifact counts, passthrough chain depth, Shannon tag entropy,
fold candidates, split candidates.

### Phase 9 — Versioning — REAL

`pkg/version` ships ParsePin, Resolve, ContentHash. Wired into
ingest (immutability invariant fires from `pkg/store.PutManifest`
on content-hash mismatch). `<id>@sha256:<hash>` resolution at
`load_artifact`. `session_id`-tagged latest-resolution caching.
Force-push tolerance ships per Phase 6.

### Phase 10 — Layer CLI — REAL

`podium layer register / list / reorder / unregister / reingest`.
Server-side `layer_configs` table + HTTP endpoints. Admin auth via
`core.AdminAuthorize`. Auto-generated 32-byte HMAC webhook secret
on register for git sources. User-defined layers receive implicit
`Users:[<owner>]` visibility. `freeze.break_glass` audit event.

`podium layer update` and `podium layer watch` are
REMAINING_GAPS.md B3.

### Phase 11 — IdentityProvider — REAL

`pkg/identity.OAuthDeviceCode` runs the full RFC 8628 flow against
a configured IdP (HTTP, polling with backoff, token exchange).
`InjectedSessionToken` does real JWT parse + RSA / ECDSA / Ed25519
signature verification via `RuntimeKeyRegistry`. OS keychain
integration via `KeychainStore` (zalando/go-keyring →
macOS Keychain / Windows Credential Manager / libsecret).
`PODIUM_SESSION_TOKEN_FILE` and `PODIUM_SESSION_TOKEN_ENV`
indirection for secret-handling flexibility. `podium login` /
`logout` drive the device-code flow end-to-end.

### Phase 12 — Workspace local overlay — REAL

`pkg/overlay.Filesystem` walks the workspace overlay; the MCP
bridge consults it on `load_artifact` (highest-precedence
short-circuit) and `pkg/sync` merges it as the top layer.
`ResolveWorkspaceOverlay` follows §6.4 path resolution rules
(env → `<workspace>/.podium/overlay/` → disabled).

### Phase 13 — Adapters + MaterializationHook — REAL

10 built-in `HarnessAdapter` implementations: `none`, `claude-
code`, `claude-desktop`, `claude-cowork`, `cursor`, `codex`,
`opencode`, `gemini`, `pi`, `hermes`. Every cell of the §6.7.1 /
§4.3.5 / §4.3 capability matrices is exercised. `pkg/hook` ships
the SPI + chain runner; `pkg/materialize.Materialize` invokes
hooks before atomic write per §6.6 step 4.

### Phase 14 — TS SDK + sync override / save-as / profile edit — REAL

`sdks/podium-ts` ships HTTP client with `subscribe()` (NDJSON
streaming), `dependentsOf`, `previewScope`. `podium sync override`
(batch flags), `save-as`, `profile edit` (comment-preserving
YAML round-trip).

`sdks/podium-py` is missing — REMAINING_GAPS.md C3.

### Phase 15 — Cross-type dependency graph — REAL

`pkg/dependency.Graph` + reverse index in `pkg/store`. Population
from manifest parse (`extends:`, `delegates_to:`, `mcpServers:`
→ edges). `core.DependentsOf` walks reverse-dependencies with
visibility filtering. `core.PreviewScope` returns aggregated
scope metadata. `podium impact` CLI + `/v1/dependents` +
`/v1/scope/preview`.

### Phase 16 — Audit log + hash chain — REAL

`pkg/audit` ships in-memory + file-backed (`FileSink`)
implementations with hash-chain integrity (`Verify`). PII redaction
via `PIIScrubber` + `RedactFields`. Retention enforcement via
`Enforce`; GDPR erasure via `EraseUser` (chain rebuild on rewrite).
Transparency anchoring via `Anchor` records the chain head into
Rekor through the Sigstore-keyless signer. `podium admin
retention` / `erase` CLIs.

**Known gap.** `Anchor` works on demand; the periodic scheduler
goroutine in `cmd/podium-server` is REMAINING_GAPS.md A2.

### Phase 17 — Vulnerability tracking — OUT-OF-SCOPE

Vulnerability scanning is not a registry responsibility. The
natural place for CVE checks is the CI pipeline that authored the
artifact (pre-merge) and the CD pipeline that deploys agents
using it (continuous). Bundle contents are opaque to Podium per
§1.1 / §4.7.7; the registry stores bytes, hashes them, and hands
them to consumers.

The `sbom:` frontmatter field stays as an informational
passthrough so consumers can find an SBOM bundled alongside
`ARTIFACT.md`, but Podium does not parse it.

### Phase 18 — Deployment — REAL

Helm chart at `deploy/helm/podium/` with values.yaml, deployment
+ service templates, _helpers.tpl. Multi-stage Dockerfile
(`deploy/Dockerfile`) on alpine:3.20 with non-root user.
Operator runbook (`deploy/runbook.md`) covers read-only mode,
public mode, object-storage outage, IdP outage, full-disk, audit
backpressure, runaway QPS, signature failure storm. Reference
Grafana dashboard (`deploy/grafana-dashboard.json`).

### Phase 19 — Example artifact registry — REAL

`testdata/registries/reference/` carries every first-class type
(skill, agent, context, command, rule, hook, mcp-server) plus an
unlisted helpers domain. Skills include bundled scripts,
sensitivity:medium with SBOM passthrough, runtime requirements,
sandbox profile. Rules cover all four `rule_mode` values. Hooks
cover representative canonical events. Commands include
`expose_as_mcp_prompt: true`.

## Summary

| Phase | Status | Notes |
| --- | --- | --- |
| 0 | REAL | gap: stale-file cleanup on sync (REMAINING_GAPS.md A4) |
| 1 | REAL | — |
| 2 | REAL | gap: read-only mode probe (D3) |
| 3 | REAL | — |
| 4 | REAL | gap: podium-py (C3) |
| 5 | REAL | — |
| 6 | REAL | gap: layer watch CLI (B3) |
| 7 | REAL | gap: SCIM → visibility integration (A1) |
| 8 | REAL | — |
| 9 | REAL | — |
| 10 | REAL | gap: layer update CLI (B3) |
| 11 | REAL | — |
| 12 | REAL | — |
| 13 | REAL | — |
| 14 | REAL | gap: podium-py (C3) |
| 15 | REAL | — |
| 16 | REAL | gap: anchoring scheduler (A2) |
| 17 | OUT-OF-SCOPE | vulnerability scanning lives in CI/CD |
| 18 | REAL | — |
| 19 | REAL | — |

## Cross-cutting gaps

These don't map to one phase; see REMAINING_GAPS.md for the
detailed plan with effort estimates and test strategies.

- **Plumbing (Batch A): DONE.** SCIM → visibility resolver
  hook, sandbox profile enforcement on the MCP path, sync
  stale-file cleanup driven by the sync lock.
- **Configuration surface (Batch D): DONE.**
  `~/.podium/registry.yaml` parser,
  `PODIUM_DEFAULT_LAYER_VISIBILITY`, read-only-mode probe
  goroutine in `cmd/podium-server` that flips the
  `ModeTracker` after consecutive store failures.
- **Anchor scheduler / outbound webhook worker bootstrap.**
  `pkg/audit.Scheduler` and `pkg/webhook.Worker` ship as
  ready-to-mount components, but `cmd/podium-server` does not
  yet construct them. Both need a persistence-backed signer
  and receiver store wired first.
- **CLI surface (Batch B, ~half day).** `podium serve` /
  `config show` / `layer update` / `layer watch` / `cache prune`
  / `import` / `admin migrate-to-standard` /
  `admin runtime register`.
- **Real new features (Batch C, ~2 days).**
  `NotificationProvider` SPI, `TypeProvider` SPI,
  `podium-py` SDK.
- **Web UI (Batch E, ~2 days).** `--web-ui` SPA at `/ui/`.
- **Verification (Batch F, ~half day).** p99 latency benchmark,
  CI workflow for live integration tests.

## Test discipline

- Every behavior shipped follows the Tier 1 + Tier 2 pattern:
  Tier 1 (always-on, in-process httptest fixtures), Tier 2
  (env-gated against real upstream services). No Docker
  dependency for the default suite.
- Conformance suites for `pkg/store` (Memory + SQLite + Postgres),
  `pkg/objectstore` (Memory + Filesystem; S3 covered via Tier 2),
  `pkg/vector` (all six backends).
- §6.7.1 / §6.10 / §4.6 / §4.3.5 / §4.3 matrices exercised by
  test cells (199 / 199 covered).
