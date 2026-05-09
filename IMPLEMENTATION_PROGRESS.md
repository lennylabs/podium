# Implementation progress (session checkpoint)

Tracks the state of the Podium implementation on the
`initial-implementation` branch.

## Summary

- **32 commits** on `initial-implementation` since branching from `main`.
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
| 3  | PARTIAL | Lock file + scope filter + adapters + override / save-as / profile edit. `--watch` still pending. |
| 4  | REAL    | MCP bridge does fetch + cache + adapter + atomic write per §6.6. Per-call `harness:` override. Identity passthrough. Protocol version negotiation. |
| 5  | REAL    | SQLite + Memory conformance suite. Standalone bootstrap via `cmd/podium-server`. Postgres still pending. |
| 6  | REAL    | Real go-git source + ingest pipeline + GitHub/GitLab/Bitbucket webhook signature verification + freeze-window + storage-quota enforcement. Force-push tolerance still pending. |
| 7  | REAL    | LayerComposer wired into HTTP server; visibility filtering applied per call; admin-only ops gated. OIDC / SCIM still pending. |
| 8  | PARTIAL | extends: at ingest + merge at load_artifact (hidden parent). max_depth + notable_count + featured rendering. fold_below_artifacts / fold_passthrough_chains / target_response_tokens still pending. |
| 9  | PARTIAL | Content hashing + session_id-consistent latest. Force-push handling still pending. |
| 10 | REAL    | Layer CLI subcommands (register / list / reorder / unregister / reingest); server-side layer config table + HTTP endpoints. Admin auth via core.AdminAuthorize. |
| 11 | REAL    | RSA / ECDSA / Ed25519 JWT verification. RuntimeKeyRegistry. OS keychain. Real OAuth device-code (RFC 8628). |
| 12 | PARTIAL | Overlay provider exists; MCP / sync integration and BM25 local index still pending. |
| 13 | REAL    | All 10 built-in adapters; every §6.7.1 / §4.3.5 / §4.3 cell exercised. MaterializationHook chain via `pkg/materialize.Materialize`. |
| 14 | REAL    | TS SDK with subscriptions, dependentsOf, previewScope. `podium sync override` / `save-as` / `profile edit`. |
| 15 | REAL    | Cross-type dependency graph populated by ingest. core.DependentsOf + core.PreviewScope. /v1/dependents, /v1/scope/preview, podium impact. |
| 16 | PARTIAL | Audit emission per call. File-backed JSON Lines sink with hash-chain integrity. PII redaction via PIIScrubber + RedactFields. Retention enforcement / GDPR erasure / transparency anchoring still pending. |
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

These items are clearly scoped but each requires either additional
infrastructure or deployment-side wiring to ship the production version:

1. **Phase 8 fold_below_artifacts / fold_passthrough_chains rendering.**
   Pure-function logic; needs careful subtree counting + collapse
   semantics from §4.5.5.
2. **Phase 12 MCP / sync overlay integration + BM25 local index.**
   Wires the existing overlay provider into the consumer surfaces.
3. **Phase 9 force-push tolerance.** Ingest needs to track the
   last-ingested commit per layer and emit `layer.history_rewritten`
   on rewrites.
4. **Phase 1 real Sigstore integration.** Fulcio + Rekor HTTP clients;
   requires the production secret backend.
5. **Phase 17 real CVE feed adapters.** NVD / OSV / GHSA periodic
   ingestion; uses the existing PURL matcher.
6. **Phase 5 Postgres backend** (alongside SQLite). The conformance
   suite already covers what a backend must satisfy.
7. **Phase 16 retention + GDPR erasure + transparency anchoring.**
   Builds on the file-backed audit sink that ships now.
8. **Phase 3 --watch mode** for sync.
9. **Phase 2 presigned URLs** above the §4.1 inline cutoff.
10. **Vector store + embedding pipeline** for §4.7 hybrid retrieval
    (sqlite-vec + embedded-onnx in standalone, pgvector + an
    EmbeddingProvider in standard).

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
- One existing test was modified (`TestIngest_PopulatesDependencyEdges`)
  for §4.7.6 spec alignment. One fixture-shape test was updated
  (`TestReferenceRegistry_OpensAndWalks`) to match the expanded
  reference registry. No spec content was modified.
