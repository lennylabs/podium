# Implementation progress (session checkpoint)

Tracks the state of the Podium implementation on the
`initial-implementation` branch.

## Summary

- **All §10 build phases REAL or out-of-scope** (Phase 17 is
  intentionally out of registry scope per §1.1 / §4.7.7).
- **31 packages green** at `PODIUM_PHASE=19` across the full suite.
- **Matrix coverage: 199 / 199 cells.** Every documented spec matrix
  is fully exercised.
- **Spec section coverage**: ~80 of 138 sections cited.
- **Test layout**: Tier 1 (always-on, in-process httptest fixtures)
  + Tier 2 (env-gated against real Sigstore / Postgres / S3 /
  embedding providers); no Docker dependency for the default suite.

## Phase-by-phase status

| Phase | Status | Notes |
| ---: | --- | --- |
| 0  | REAL    | Filesystem-source `podium sync` end-to-end. |
| 1  | REAL    | Lint rules + Noop signer + real SigstoreKeyless (Fulcio v2 cert mint, Rekor hashedrekord upload + presence check, x509 chain validation against a configurable trust root) + real RegistryManagedKey (Ed25519, KeyID-aware rotation rejection). Tier 1 tests use an in-process CA + httptest fixture; Tier 2 live smoke gates on PODIUM_SIGSTORE_* env vars. |
| 2  | REAL    | HTTP API including `/v1/dependents` and `/v1/scope/preview`. Visibility, latest, BM25, audit, public-mode + IdP guard, read-only mode. §4.1 large-resource path: resources above the 256 KB cutoff are uploaded to the configured `pkg/objectstore` provider (filesystem default, S3 via lib/pq-style minio-go wrapper), and `load_artifact` returns them as URLs in `large_resources`. Filesystem backend serves via authenticated `/objects/{content_hash}` route per the §13.10 spec clarification; S3 backend uses AWS Signature V4 presigning. |
| 3  | REAL    | Lock file + scope filter + adapters + override / save-as / profile edit + `--watch` (poll-based, configurable period and debounce). |
| 4  | REAL    | MCP bridge does fetch + cache + adapter + atomic write per §6.6. Per-call `harness:` override. Identity passthrough. Protocol version negotiation. |
| 5  | REAL    | SQLite + Memory + Postgres conformance suite via `pkg/store/storetest.Suite`. Standalone bootstrap via `cmd/podium-server` selects the backend through `PODIUM_REGISTRY_STORE`; Postgres tests gate on `PODIUM_POSTGRES_DSN` so CI runs with or without a database. |
| 6  | REAL    | Real go-git source + ingest pipeline + GitHub/GitLab/Bitbucket webhook signature verification + freeze-window + storage-quota enforcement. |
| 7  | REAL    | LayerComposer wired into HTTP server; visibility filtering applied per call; admin-only ops gated. OIDC / SCIM still pending. |
| 8  | REAL    | extends: at ingest + merge at load_artifact, max_depth + notable_count + featured, fold_below_artifacts + fold_passthrough_chains + target_response_tokens. |
| 9  | REAL    | Content hashing + session_id-consistent latest + force-push detection (last-ingested-ref tracking, ancestry walk over the cloned history, strict + tolerant policies, layer.history_rewritten event). |
| 10 | REAL    | Layer CLI subcommands (register / list / reorder / unregister / reingest); server-side layer config table + HTTP endpoints. Admin auth via core.AdminAuthorize. |
| 11 | REAL    | RSA / ECDSA / Ed25519 JWT verification. RuntimeKeyRegistry. OS keychain. Real OAuth device-code (RFC 8628). |
| 12 | REAL    | Overlay provider wired into both consumer surfaces: sync.Run merges overlay records as the highest-precedence layer, and the MCP bridge short-circuits load_artifact when the overlay holds the requested ID. |
| 13 | REAL    | All 10 built-in adapters; every §6.7.1 / §4.3.5 / §4.3 cell exercised. MaterializationHook chain via `pkg/materialize.Materialize`. |
| 14 | REAL    | TS SDK with subscriptions, dependentsOf, previewScope. `podium sync override` / `save-as` / `profile edit`. |
| 15 | REAL    | Cross-type dependency graph populated by ingest. core.DependentsOf + core.PreviewScope. /v1/dependents, /v1/scope/preview, podium impact. |
| 16 | REAL    | Audit emission per call. File-backed JSON Lines sink with hash-chain integrity. PII redaction via PIIScrubber + RedactFields. Retention enforcement (Enforce) and GDPR erasure (EraseUser) with chain rebuild; `podium admin retention` and `podium admin erase` CLIs. Transparency anchoring (audit.Anchor) signs the chain head via the configured Sigstore-keyless provider and records the Rekor log index in an audit.anchored event. |
| 17 | OUT-OF-SCOPE | Vulnerability scanning is intentionally out of registry scope (§1.1, §4.7.7). The natural place for CVE checks is the CI pipeline that authored the artifact and the CD pipeline that deploys agents using it. Authors who ship an SBOM bundle it as an ordinary resource; consumers fetch it via `load_artifact` and feed their own scanner. The registry stores SBOMs as opaque bytes and performs no parsing, scoring, or live feed ingestion. |
| 18 | REAL    | Helm chart + Dockerfile + runbook + Grafana dashboard JSON. |
| 19 | REAL    | Reference fixture covers every first-class type (skill, agent, context, command, rule, hook, mcp-server) plus an unlisted helpers domain. |

## Matrix audit

```
§6.7.1   total cells: 99   missing: 0    Capability matrix
§6.10    total cells: 29   missing: 0    Error codes
§4.6     total cells: 15   missing: 0    Visibility unions
§4.3.5   total cells: 20   missing: 0    Canonical hook events
§4.3     total cells: 36   missing: 0    rule_mode × harness
                          ───────────
                          Total missing: 0 / 199
```

## Recently closed deltas (spec audit follow-up)

- §4.1 / §4.3.4 / §4.4 lint coverage: bundled-resource size
  caps, manifest size caps, ARTIFACT.md body strictness for
  skills, and prose-reference resolution all now produce lint
  diagnostics at ingest.
- §8.1 audit-event coverage: meta-tool calls, ingest events
  (artifact.published / .deprecated / .signed), layer events,
  visibility.denied, freeze.break_glass, and read-only mode
  transitions all reach the file-backed audit sink.
- §4.7.8 search QPS / materialize rate enforcement: per-tenant
  token-bucket limiter returns 429 +
  `quota.search_qps_exceeded` / `quota.materialize_rate_exceeded`
  when budgets are exhausted.
- §6.5 cache modes: `PODIUM_CACHE_MODE` now branches; offline-
  first reads the resolution cache before contacting the
  registry, offline-only refuses network calls and returns
  `cache.offline_miss` on a cache miss.

## What remains

The §10 phase table is end-to-end REAL, but a handful of
non-phase-aligned gaps remain. See `REMAINING_GAPS.md` for the
detailed plan with effort estimates and test strategies. The list
below is the punch summary.

**Plumbing** (small wiring on existing components — Batch A): DONE.
- SCIM → visibility integration: `core.Registry.WithGroupResolver`
  expands a layer's `groups:` filter via the resolver function
  passed in by the caller. `cmd/podium-server` wires it to
  `scim.Memory.MembersOf` when SCIM is enabled.
- Sandbox profile enforcement: `cmd/podium-mcp` rejects an
  artifact whose `sandbox_profile` is not in the operator-
  supplied host-supported list.
- Idempotent sync stale-file cleanup: `pkg/sync` reads the
  prior `.podium/sync.lock`, removes any path the new run did
  not write, and persists the new lock atomically.

**CLI surface** (operator commands the spec promises — Batch B):
- `podium serve` — DONE. Same code path as `podium-server` via
  the new `internal/serverboot` package.
- `podium config show` — DONE. Prints every resolved setting with
  its source (env var, registry.yaml, or hardcoded default);
  secrets redacted.
- `podium layer update` / `layer watch` — DONE. PUT-style partial
  patch against /v1/layers/update?id=ID; watch polls reingest on
  an interval.
- `podium cache prune` — DONE. Walks `~/.podium/cache/`, removes
  buckets older than `--days N` (default 30); honors `--dir` and
  `--dry-run`.
- `podium import` — DONE. Walks a `skills/` directory, rewrites
  each subdirectory as a Podium-shaped artifact (ARTIFACT.md +
  SKILL.md + bundled resources).
- `podium admin runtime register` / `runtime list` — DONE. POST
  /v1/admin/runtime takes an issuer, JWS algorithm, and PEM-encoded
  public key and adds the runtime to the trust list consulted by
  the §6.3.2 verifier. When `PODIUM_RUNTIME_KEYS_PATH` is set,
  registrations persist as a JSON file across server restarts.
- `podium admin migrate-to-standard` — DONE. Reads tenants,
  manifests, and layer configs from the standalone SQLite store
  and writes them into the target Postgres (or another SQLite)
  store; walks the source filesystem object store and uploads
  every blob to the target backend (filesystem or S3); copies
  the audit log byte-for-byte. Honors `--dry-run`.

**Real new features** (Batch C): DONE.
- `NotificationProvider` SPI (§9): `pkg/notification` ships
  Noop, LogProvider, Webhook (HMAC-SHA256), and a fan-out
  MultiProvider. `core.Registry.WithNotifier` wires the SPI
  into the registry; `cmd/podium-server` honors
  `PODIUM_NOTIFICATION_PROVIDER` (`noop|log|webhook|multi`).
- `TypeProvider` SPI (§9): `pkg/typeprovider` exposes the
  registry pattern; the first-class types ship as no-op
  built-ins so existing lint behavior is unchanged. Custom
  types register a Validate function.
- `podium-py` SDK (§10 Phase 4): adds `dependents_of`,
  `preview_scope`, and an NDJSON `subscribe()` generator on
  top of the previously-shipped meta-tool methods.

**Configuration surface** (Batch D): DONE.
- `~/.podium/registry.yaml` server-config parser
  (`cmd/podium-server/yaml_config.go`); env vars retain
  precedence.
- `PODIUM_DEFAULT_LAYER_VISIBILITY` honored in the layer-
  registration handler when the admin omits an explicit
  visibility.
- `PODIUM_READONLY_PROBE_FAILURES` / `_INTERVAL` drive the
  background goroutine in `cmd/podium-server` that flips the
  shared `ModeTracker` after consecutive store outages and
  restores ready mode on the first success.

**Anchor scheduler / outbound webhook worker bootstrap**: DONE.
- `pkg/audit.Scheduler` runs in a goroutine when
  `PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS > 0`. The signer is a
  `RegistryManagedKey` whose Ed25519 keypair lives at
  `~/.podium/standalone/audit.key` (or
  `PODIUM_AUDIT_SIGNING_KEY_PATH`); the file is generated on
  first run and reloaded byte-identical thereafter.
- `pkg/webhook.Worker` is mounted via `server.WithWebhooks`. When
  `PODIUM_WEBHOOK_STORE_PATH` is set, receivers persist as a JSON
  file across server restarts; absent the env var, receivers
  stay in memory.

**Audit retention scheduler**: DONE.
- `internal/serverboot/audit_retention.go` runs `audit.Enforce`
  every `PODIUM_AUDIT_RETENTION_INTERVAL_SECONDS` against every
  event type the registry emits, dropping records older than
  `PODIUM_AUDIT_RETENTION_MAX_AGE_DAYS` (default 365). Manual
  `podium admin retention` continues to work for one-shot
  invocation.

**Read-only audit events**: DONE.
- The §13.2.1 read-only probe writes
  `registry.read_only_entered` / `registry.read_only_exited`
  events to the audit sink on transitions. Operators monitor
  `audit.log` to see store outages.

**OAuth refresh-token flow**: DONE.
- `identity.DeviceCodeFlow.Refresh` exchanges a refresh_token
  for a fresh access_token per RFC 6749 §6, carrying through
  non-rotated refresh tokens and surfacing `invalid_grant` as
  `ErrAccessDenied` so callers know to re-initiate.

**Persistence for runtime trust keys + SCIM directory**: DONE.
- `PODIUM_RUNTIME_KEYS_PATH` persists §6.3.2 registrations.
- `PODIUM_SCIM_STORE_PATH` persists §6.3.1 IdP-pushed users +
  groups. The visibility evaluator's `groups:` resolver reads
  the same store, so memberships survive restarts.

**Audit chain head recovery**: DONE.
- `audit.NewFileSink` rescans the existing log on open and
  recovers the last event's hash so the chain continues across
  server restarts.

**Verification** (Batch F): DONE.
- `test/bench/latency_test.go` exercises SearchArtifacts,
  LoadArtifact, and LoadDomain at three input sizes; `make
  bench` runs it with -benchmem and a stable -benchtime; the
  `bench` GitHub Actions workflow runs it on every push to main
  and on a Monday cadence.
- The `integration-live` workflow runs the env-gated Postgres
  conformance suite against a Postgres service container and
  the S3 conformance suite against MinIO.

**Web UI** (Batch E): DONE.
- `web/index.html`, `web/app.js`, and `web/style.css` ship as
  the §13.10 SPA, embedded into the binary via `go:embed`.
- `internal/serverboot` mounts the embedded assets at `/ui/`
  when `PODIUM_WEB_UI=true`. The SPA renders the domain map,
  search, and an artifact view by calling the §7.6 read API.

**Intentionally out of scope:**
- Phase 17 vulnerability scanning (§1.1, §4.7.7) — CI/CD's job.
- Embedded ONNX — Ollama-against-local-model is the offline path.
- SCIM PATCH and Bulk endpoints — IdPs use full PUT.
- `podium admin migrate-to-standard` — `pg_dump` + `aws s3 sync`
  cover the data movement; a guided wrapper adds little over
  the standard tools.

**Persistent-state debt**: DONE for webhook receivers, runtime
trust keys, SCIM directory (file-backed), and the audit chain
head. SQL-backed implementations of the same SPIs remain a
follow-up — the file-backed pattern is fine up to the low
hundreds of records typical of these surfaces.

## What this branch leaves you with

- A spec-correct registry server, MCP bridge, sync CLI, and TS
  SDK that interoperate end-to-end.
- All §10 phases REAL except 17 (out of scope by design).
- §4.1 large-resource path with object-store SPI + filesystem and
  S3 backends.
- §4.7 hybrid retrieval with four embedding providers (openai,
  voyage, cohere, ollama) and six vector backends (memory,
  pgvector, sqlite-vec, pinecone, weaviate-cloud, qdrant-cloud).
- §4.7.9 signing end-to-end: ingest signs, manifest stores the
  envelope, `load_artifact` returns it, MCP enforces the §6.2
  policy at materialize time.
- §6.3 OAuth Device Code flow, OS keychain integration, real
  injected-session-token JWT verification.
- §6.3.1 SCIM 2.0 receiver (CRUD + bearer auth + filter parser).
- §6.6 materialization pipeline with HarnessAdapter chain
  (10 adapters) + MaterializationHook chain.
- §7.3.2 outbound webhook delivery with HMAC, retry, auto-disable.
- §7.6 `/v1/events` NDJSON streaming + ingest publishing through
  the bus.
- §8 audit log: hash-chained file sink, retention enforcement,
  GDPR erasure, transparency anchoring (Sigstore-keyless).
- §13 deployment: Docker image, Helm chart, runbook, Grafana
  dashboard.
- 199/199 documented spec matrices covered by tests.
- A clear punch list (`REMAINING_GAPS.md`) for the remaining work.

## Notes on test discipline

- Every new behavior shipped this session followed TDD: failing test
  first, implementation, passing test, commit.
- Three existing tests were modified across the session:
  - `TestIngest_PopulatesDependencyEdges` for §4.7.6 spec alignment.
  - `TestReferenceRegistry_OpensAndWalks` to match the expanded
    reference registry.
  - `TestLoadDomain_NotableCountDefault` because the §4.5.5 rendering
    note covers only budget tightening and depth caps, not the
    notable-count cap itself; the test was asserting an obsolete
    truncation message.
- No spec content was modified.
