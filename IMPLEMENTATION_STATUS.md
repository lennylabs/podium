# Implementation status (Stage B)

Walkthrough of every phase, classifying what's actually implemented
against the spec versus what's a placeholder that satisfies a placeholder
test. Goal: set accurate expectations for E (real Podium implementation).

## Legend

- **REAL** — matches the spec; an autonomous run will not need to revisit this.
- **PARTIAL** — happy path works; spec corners and integration with other
  packages are missing.
- **SCAFFOLDED** — interface is correct; the production mechanism is a
  placeholder that the existing tests do not exercise.
- **STUB** — function exists but returns a "phase pending" sentinel.
- **MISSING** — no code at all.

## Phase-by-phase

### Phase 0 — Filesystem-source `podium sync` (§13.11) — REAL

Everything material:

- `pkg/manifest`: ARTIFACT.md / SKILL.md / DOMAIN.md parsers parse arbitrary
  spec-conforming input.
- `pkg/registry/filesystem`: single-layer / multi-layer / ambiguous dispatch;
  walker captures every artifact and bundled resource; collision policies
  match §4.6.
- `pkg/adapter/none`: canonical-layout pass-through is byte-correct.
- `pkg/materialize`: atomic write rejects parent-escape and absolute paths
  before any write.
- `pkg/sync`: end-to-end orchestrator.
- `cmd/podium sync`: CLI.

**Gap**: idempotent sync clears stale files. Currently a sync that drops
an artifact does not delete its previously-materialized files.

### Phase 1 — Manifest schema + `podium lint` + signing — PARTIAL

- `pkg/lint`: 7 rules, covering required fields, SKILL.md compliance, name
  syntax, semver, SBOM, hook generic vs subtype, hint type applicability.
- `pkg/sign`: SPI + Noop verifier.

**Missing**:

- Lint rules for: §4.1 manifest size cap (20K tokens), §4.5.2 glob
  validation in DOMAIN.md, §4.4 manifest-body reference resolution,
  agentskills.io `skills-ref validate` integration, §4.4.2 content
  provenance markers, §4.3 generic + subtype hook combination on the
  same artifact, ingest collision detection by content hash.
- Sigstore-keyless signing.
- Registry-managed signing key.
- `podium verify` CLI command.

### Phase 2 — Registry HTTP API (§5) — SCAFFOLDED

The four meta-tools have routes, response shapes, and structured error
envelopes. The semantics underneath are placeholders.

**Missing**:

- Hybrid retrieval (BM25 + embeddings via RRF). Currently substring match.
- Layer composition with visibility filtering on every call. Currently the
  server walks the filesystem registry without consulting `pkg/layer`.
- Identity attribution from OAuth-attested calls. Currently anonymous.
- Presigned URLs above the 256 KB inline cutoff. Currently inline-only.
- `total_matched` accuracy, `top_k` accuracy, scope filter (only artifact
  search supports scope; load_domain doesn't honor depth).
- Discovery rendering rules from §4.5.5: `max_depth`, `fold_below_artifacts`,
  `fold_passthrough_chains`, `notable_count`, `target_response_tokens`,
  `featured`, `deprioritize`, `keywords`.
- Latency budgets in §7.1.
- Read-only mode handoff per §13.2.1.
- Public mode bypass per §13.10.

### Phase 3 — Sync upgrades + claude-code / codex — PARTIAL

- `pkg/sync/lockfile`: round-trip and atomic write are real.
- `pkg/sync/scope`: glob matcher (`*`, `**`, `{a,b}`) is real.
- `pkg/adapter/claudecode`: skill / rule / agent placement is real.
- `pkg/adapter/codex`: package layout is real.

**Missing**:

- Frontmatter rewriting (canonical fields → harness-native equivalents) for
  every adapter.
- §6.7.1 capability-matrix enforcement: ingest-time lint that rejects
  unsupported (field, harness) intersections unless `target_harnesses:`
  excludes the harness.
- `--watch` mode (fsnotify on registry + workspace overlay).
- `--profile` resolution from sync.yaml.
- `podium init` writes `~/.podium/sync.yaml` but does not validate the
  resulting precedence per §7.5.2.
- `podium config show` is missing.
- Multi-target sync (`--config` without `--profile`).

### Phase 4 — MCP server + podium-py + read CLI — SPLIT

- `sdks/podium-py`: real HTTP client matching §7.6 surface.
- `cmd/podium {search,domain,artifact,init}`: real CLI dispatchers.
- `cmd/podium-mcp`: thin JSON-RPC stdio proxy. Forwards every tool call
  to the registry HTTP API.

**Missing on the MCP server**:

- Materialization at `load_artifact`. Currently the proxy returns the
  HTTP body verbatim; no atomic write to disk, no harness adapter, no
  hook chain.
- Cache (resolution + content) per §6.5.
- Identity attachment (the proxy makes anonymous HTTP calls).
- Workspace local overlay merge (§6.4.1) before returning results.
- MCP elicitation for the device-code flow.
- Health tool reporting cache size and last-successful-call timestamp.
- Per-call `harness:` override.

### Phase 5 — Multi-tenant data model — SCAFFOLDED

- `pkg/store`: SPI is correct; in-memory backend exists.

**Missing**:

- SQLite backend (standalone default per §13.10).
- Postgres backend with schema-per-tenant and row-level security per §4.7.1.
- Object storage backend (S3 / filesystem) per §4.7.
- pgvector / sqlite-vec collocation.
- Schema migrations.
- Quotas (§4.7.8).
- Tenant lifecycle commands.

### Phase 6 — LayerSourceProvider — PARTIAL

- `pkg/layer/source/local.go`: real.
- `pkg/layer/source/source.go`: SPI is correct.
- `pkg/layer/source/git.go`: STUB; returns "phase 6 implementation pending."

**Missing**:

- Real git fetch (go-git or shell-out).
- `GitProvider` SPI implementations for GitHub / GitLab / Bitbucket
  webhook signature verification.
- Webhook ingest pipeline: HMAC verify → diff walk → lint → immutability
  check → content-addressed store → outbound event.
- Force-push handling (`layer.history_rewritten` event, tolerant
  default policy).
- `podium layer watch` polling.
- `podium layer reingest` integration with backends.

### Phase 7 — LayerComposer + visibility + OIDC + SCIM — PARTIAL

- `pkg/layer/composer.go`: visibility evaluator, EffectiveLayers, Compose,
  most-restrictive-sensitivity, append-unique-tags are real pure functions.

**Missing**:

- Composer wired into `pkg/registry/server` so HTTP requests filter by
  identity. Currently the server bypasses the composer.
- OIDC client (provider validation, JWKS rotation).
- SCIM 2.0 push (group / user sync).
- `IdpGroupMapping` adapter for IdPs without SCIM.
- Public-mode bypass at the server level (§13.10).
- Scope claims narrowing (`podium:read:finance/*` etc).
- `podium admin show-effective <user>`.

### Phase 8 — Domain composition — PARTIAL

- `pkg/domain.MergeAcrossLayers`: §4.5.4 merge rules are real.

**Missing**:

- Glob resolver applied to `DOMAIN.md include:` / `exclude:` against the
  walked artifact set. The matcher in `pkg/sync/scope.go` is reusable but
  not wired into `pkg/domain`.
- `extends:` resolver (cycle detection, parent-version pinning at child
  ingest, hidden-parent merging).
- Discovery rendering: `max_depth` cap, `fold_below_artifacts`,
  `fold_passthrough_chains`, `target_response_tokens`, `notable_count`,
  `featured` ordering, `deprioritize` ranking, learn-from-usage signal,
  rendering note.
- `podium domain analyze` CLI.

### Phase 9 — Versioning — PARTIAL

- `pkg/version`: ParsePin / Resolve / ContentHash are real pure functions.

**Missing**:

- Wired into ingest (the immutability invariant fires from `pkg/store`
  PutManifest by content-hash check, but ingest never calls it).
- `<id>@sha256:<hash>` resolution at the HTTP API.
- `session_id`-tagged latest-resolution caching at the registry.
- Tolerant force-push: preserving previously-ingested bytes when the
  layer's git history is rewritten.
- Strict-mode policy per layer (`force_push_policy: strict`).

### Phase 10 — Layer CLI — MISSING

Not started. Spec §10 calls out:

- `podium layer register` / `list` / `reorder` / `unregister` /
  `reingest` / `watch`.
- User-defined-layer cap (default 3 per identity).
- Freeze windows.
- `podium admin grant` / `revoke`.

`phasegate advance` blocks at phase 10 → 11 (see Stage A).

### Phase 11 — IdentityProvider — SCAFFOLDED

- `pkg/identity`: SPI is correct; both built-ins exist as wrappers around
  caller-supplied functions.

**Missing**:

- Real OAuth device-code client (HTTP, polling, token refresh).
- Real JWT parser and signature verification for `injected-session-token`.
- Runtime trust model: registered signing keys, registry-side verification.
- OS keychain integration (macOS Keychain, Windows Credential Manager,
  libsecret on Linux).
- `PODIUM_SESSION_TOKEN_FILE` fsnotify rotation.
- SIGHUP-triggered re-read.
- Tested IdP integrations: Okta, Entra ID, Auth0, Google Workspace, Keycloak.

### Phase 12 — Workspace local overlay — PARTIAL

- `pkg/overlay/Filesystem`: real.
- `ResolveWorkspaceOverlay`: real.

**Missing**:

- Integration into `cmd/podium-mcp` so the overlay merges into the
  effective view before returning meta-tool results.
- Integration into `pkg/sync` so filesystem-source sync honors the overlay.
- `LocalSearchProvider` (BM25 over local-overlay manifest text + RRF
  with the registry's hybrid retrieval results).
- fsnotify watcher to re-index on overlay change.
- MCP roots discovery (`<workspace>/.podium/overlay/` fallback when env
  unset).

### Phase 13 — Remaining adapters + MaterializationHook — PARTIAL

- `pkg/hook`: SPI + chain runner are real.

**Missing**:

- `claude-desktop`, `claude-cowork`, `cursor`, `gemini`, `opencode`, `pi`,
  `hermes` adapters. None exist.
- Per-adapter frontmatter mapping per the §6.7.1 capability matrix.
- Adapter conformance suite under `test/conformance/adapter/`.
- Hook chain integration into materialize (currently `pkg/materialize`
  doesn't call hooks).
- Adapter sandbox enforcement (adapter implementations are not isolated
  from network or subprocess access).

### Phase 14 — TS SDK + sync override/save-as/profile edit — PARTIAL

- `sdks/podium-ts`: HTTP client matching the Python surface — real.

**Missing**:

- `podium sync override` (TUI + batch flags).
- `podium sync save-as`.
- `podium profile edit` (comment-preserving YAML round-trip).
- TS SDK extras: streaming subscriptions, `dependents_of`,
  `preview_scope`, materialize-to-disk.
- TS SDK actually published under a setup that runs `npm install` and
  `npm test` cleanly.

### Phase 15 — Cross-type dependency graph — PARTIAL

- `pkg/dependency`: Graph + ImpactSet are real pure functions.

**Missing**:

- Reverse index inside `pkg/store` so persistence works.
- Population from manifest parse (`extends:`, `delegates_to:`,
  `mcpServers:` → edges).
- `podium impact` / `podium dependents-of` CLI.
- Search-ranking signal from frequently-depended-on artifacts.

### Phase 16 — Audit log + hash chain — PARTIAL

- `pkg/audit/Memory`: real, with hash chain integrity check.

**Missing**:

- Wired into the registry server's request path (every meta-tool call
  emits an event today; only the in-memory test sink demonstrates it).
- `LocalAuditSink` writing to `~/.podium/audit.log` (JSON Lines, atomic
  appends under `PIPE_BUF`).
- PII redaction (manifest-declared fields + query-text scrubbing).
- Retention enforcement (§8.4).
- GDPR erasure (`podium admin erase <user_id>`).
- Transparency log anchoring (Sigstore / CT-style).
- SIEM export.

### Phase 17 — Vulnerability tracking + SBOM + NotificationProvider — SCAFFOLDED

- `pkg/vuln`: CVE / SBOM / NotificationProvider types — real surface.
- Match function — naive substring placeholder.

**Missing**:

- Real CVE feed ingestion (NVD, OSV, GHSA).
- SBOM parsing (CycloneDX, SPDX).
- PURL / version-range matching.
- `podium vuln list` / `vuln explain` CLIs.
- NotificationProvider implementations: email, Slack, generic webhook.
- SBOM enforcement at ingest for sensitivity ≥ medium.

### Phase 18 — Deployment — MISSING

Not started. Spec §10 Phase 18: Helm chart, reference Grafana dashboard,
runbook.

`phasegate advance` blocks at phase 18 → 19 (see Stage A).

### Phase 19 — Example artifact registry — PARTIAL

- `testdata/registries/reference`: 4 artifacts across 3 layers, two
  types (skill, agent, context).

**Missing**:

- A `command` artifact (with `expose_as_mcp_prompt: true` for §5.2).
- A `rule` artifact for each `rule_mode` value (always, glob, auto, explicit).
- A `hook` artifact for representative canonical events.
- An `mcp-server` artifact.
- A `DOMAIN.md` exercising `unlisted: true` (§4.5.3).
- A `DOMAIN.md` exercising `include:` / `exclude:` cross-domain imports.
- An `extends:` chain across two layers.
- A signed artifact at `sensitivity: high`.
- A bundled Python script + Jinja template + JSON schema + binary blob.
- An external resource declaration.

## Summary

| Phase | Status | Critical-path gaps |
| --- | --- | --- |
| 0 | REAL | minor: stale-file cleanup on sync |
| 1 | PARTIAL | manifest size lint, glob lint, real signing |
| 2 | SCAFFOLDED | hybrid retrieval, composer integration, identity, presigned URLs |
| 3 | PARTIAL | adapter frontmatter mapping, --watch, --profile |
| 4 | SPLIT | MCP server is a thin proxy; needs cache, materialize, identity, overlay |
| 5 | SCAFFOLDED | SQLite, Postgres, S3 backends; pgvector |
| 6 | PARTIAL | git source, webhook pipeline |
| 7 | PARTIAL | OIDC, SCIM, composer ↔ server wiring |
| 8 | PARTIAL | extends:, discovery rendering, glob resolver in domain |
| 9 | PARTIAL | wired into ingest, session_id, force-push |
| 10 | MISSING | every layer CLI subcommand |
| 11 | SCAFFOLDED | real OAuth, real JWT, OS keychain |
| 12 | PARTIAL | MCP / sync integration, BM25 local index, fsnotify |
| 13 | PARTIAL | 7 adapters missing, conformance suite, hook → materialize |
| 14 | PARTIAL | sync override / save-as / profile edit, TS SDK extras |
| 15 | PARTIAL | persistence, ingest population, CLIs |
| 16 | PARTIAL | server integration, file sink, redaction, retention, erasure |
| 17 | SCAFFOLDED | feed ingestion, SBOM parsing, real notifiers |
| 18 | MISSING | Helm, Grafana, runbook |
| 19 | PARTIAL | broader fixture coverage |

## Implications for E

The autonomous-build narrative ("flip phase, watch tests fail, implement,
go green") will not fire as currently wired. Most phases are GREEN
because their tests are simplified to match their simplified
implementations.

Three workable paths forward for E:

**Path 1 — Tighten tests phase by phase.** For each phase, write the
spec-correct tests (visibility filtering at the HTTP layer, capability
matrix enforcement, real OAuth flow, etc.) before implementing. The
matrix tool from Stage D generates many of these mechanically.

**Path 2 — Implement phases bottom-up regardless of tests.** Skip the
red/green dance and just write spec-correct production code, accepting
that the test signal is leading rather than driving. Faster, but loses
the TDD discipline the user wanted.

**Path 3 — Hybrid.** For phases marked SCAFFOLDED or MISSING, write
spec-correct tests first (they will fail). For phases marked PARTIAL,
identify the specific missing piece and write a test for it before the
implementation. Phase 0 and parts of 1, 3, 7, 8, 9, 12, 15, 16 can stay
as-is.

Stage C (richer make next signal) and Stage D (matrix tool, coverage
gate) are infrastructure for Path 1 and Path 3.
