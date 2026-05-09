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
| 1  | PARTIAL | Lint rules + Noop signer + Sigstore-keyless / RegistryManagedKey stubs. Production Fulcio + Rekor integration deferred to deployment-time work. |
| 2  | REAL    | HTTP API including `/v1/dependents` and `/v1/scope/preview`. Visibility, latest, BM25, audit, public-mode + IdP guard, read-only mode. Presigned URLs above the inline cutoff still pending. |
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
| 16 | REAL    | Audit emission per call. File-backed JSON Lines sink with hash-chain integrity. PII redaction via PIIScrubber + RedactFields. Retention enforcement (Enforce) and GDPR erasure (EraseUser) with chain rebuild; `podium admin retention` and `podium admin erase` CLIs. Transparency anchoring still depends on Phase 1. |
| 17 | PARTIAL | PURL parser + structural CVE matching + CycloneDX / SPDX SBOM parsers + ParseSBOM dispatch. Real CVE feed ingestion (NVD / OSV / GHSA) and notification providers still pending. |
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

Each remaining item carries a single dependency or infrastructure
choice the project owner needs to make. `UNBLOCK.md` documents the
recommended approach for each.

1. **Phase 1 real Sigstore integration.** Fulcio + Rekor HTTP clients;
   needs `sigstore/sigstore-go` (recommended) or a hand-rolled client.
2. **Phase 2 presigned URLs** above the §4.1 inline cutoff. Needs an
   `aws-sdk-go-v2` dependency for S3 / MinIO / R2 / GCS.
3. **Phase 17 real CVE feed adapters.** NVD / OSV / GHSA periodic
   ingestion; the OSV adapter is the recommended starting point
   (no API key, broadest coverage). NVD + GHSA need a tenant-config
   shape for keys / cadence.
4. **Phase 16 transparency anchoring.** Periodic Sigstore-style
   anchoring of the audit chain head; depends on Phase 1.
5. **Vector store + embedding pipeline** for §4.7 hybrid retrieval.
   Needs at least one embedding provider (recommended: ship the
   API providers — OpenAI / Voyage / Cohere — first; add sqlite-vec
   + embedded-onnx behind a build tag for air-gapped deployments).

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
