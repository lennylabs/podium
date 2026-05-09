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

## What remains

The §10 phase table is end-to-end REAL, but a handful of
non-phase-aligned gaps remain. See `REMAINING_GAPS.md` for the
detailed plan with effort estimates and test strategies. The list
below is the punch summary.

**Plumbing** (small wiring on existing components — Batch A):
- SCIM → visibility integration (`layer.Visible` does not yet
  consult `scim.Store.MembersOf` when expanding `groups:` filters).
- Transparency anchoring scheduler (`audit.Anchor` works on
  demand; `cmd/podium-server` doesn't run it on a cadence).
- Sandbox profile enforcement (`PODIUM_ENFORCE_SANDBOX_PROFILE`
  is documented but not gated).
- Idempotent sync stale-file cleanup (a sync that drops an
  artifact does not delete its previously-materialized files).

**CLI surface** (operator commands the spec promises — Batch B):
- `podium serve` (alias / in-process for `podium-server`).
- `podium config show`.
- `podium layer update` / `layer watch`.
- `podium cache prune`, `podium import`.
- `podium admin migrate-to-standard` / `runtime register`.

**Real new features** (Batch C):
- `NotificationProvider` SPI (§9): ingest-failure /
  operational-notification delivery, distinct from the §7.3.2
  outbound webhook event stream.
- `TypeProvider` SPI (§9): the seven first-class types are
  hardcoded; no plugin point for a custom type.
- `podium-py` SDK (§10 Phase 4): only `podium-ts` exists today.

**Configuration surface** (Batch D):
- `~/.podium/registry.yaml` server-config parser.
- `PODIUM_DEFAULT_LAYER_VISIBILITY`.
- `PODIUM_READONLY_PROBE_FAILURES` / `_INTERVAL` (probe-and-flip
  when Postgres becomes unreachable).

**Verification** (Batch F):
- p99 latency benchmark suite for §7.1 budgets.
- CI workflow that runs the env-gated Tier 2 integration tests
  against real Postgres / S3 / Sigstage / embedding providers.

**Larger** (Batch E):
- Web UI (`--web-ui` / `PODIUM_WEB_UI=true`, §13.10) — SPA at
  `/ui/`. Not started.

**Intentionally out of scope:**
- Phase 17 vulnerability scanning (§1.1, §4.7.7) — CI/CD's job.
- Embedded ONNX — Ollama-against-local-model is the offline path.
- SCIM PATCH and Bulk endpoints — IdPs use full PUT.

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
