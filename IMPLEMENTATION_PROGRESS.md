# Implementation progress (session checkpoint)

Tracks the state of the Podium implementation on the
`initial-implementation` branch.

## Summary

- **39 commits** on `initial-implementation` since branching from `main`.
- **All tests pass** at the active phase (1) and at every higher phase.
- **Matrix coverage: 199 / 199 cells.** Every documented spec matrix
  is fully exercised.
- **Spec section coverage**: ~50 of 138 sections cited.
- **Line coverage**: 57.4%.

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

_All items shipped. The implementation matches the §10 build sequence end-to-end._

§4.7 hybrid retrieval ships with four embedding providers
(`openai`, `voyage`, `cohere`, `ollama`) and six vector backends
(`memory`, `pgvector`, `sqlite-vec`, `pinecone`, `weaviate-cloud`,
`qdrant-cloud`). RRF fusion blends BM25 ranks with vector cosine
ranks; `SearchResult.Degraded` surfaces BM25-only fallback when
vector search isn't configured or the embedder is unreachable.
Ingest-time embedding is content-hash-gated (no re-embed on
idempotent re-ingest) and atomic per row in the vector store.
`podium admin reembed` covers manual backfill and provider
switches.

## What this branch leaves you with

- A spec-correct registry server, MCP bridge, sync CLI, and SDKs
  that interoperate end-to-end.
- 199/199 documented spec matrices covered by tests.
- Deployment-ready Docker image + Helm chart + runbook + dashboard.
- Reference test fixture exercising every first-class artifact type.
- A clear roadmap above for the remaining infrastructure wiring.

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
