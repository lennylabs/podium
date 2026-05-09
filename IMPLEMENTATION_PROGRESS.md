# Implementation progress (session checkpoint)

Tracks the state of the Podium implementation on the
`initial-implementation` branch.

## Summary

- **25 commits** on `initial-implementation` since branching from `main`.
- **All tests pass** at the active phase (1) and at the maximum phase (19).
- **Matrix coverage: 199 / 199 cells** — every documented spec matrix is
  fully exercised. Down from 186 missing at session start.
- **Spec section coverage**: 50 of 138 sections cited (was 19 at start).
- **Line coverage**: 60.1% (was 50.4% at start).

## Phase-by-phase status

The categories REAL / PARTIAL / SCAFFOLDED / STUB / MISSING are the
same as `IMPLEMENTATION_STATUS.md`.

| Phase | Status | Notes |
| ---: | --- | --- |
| 0  | REAL    | Filesystem-source `podium sync` end-to-end. |
| 1  | PARTIAL | Lint rules + Noop sign provider. Sigstore-keyless / registry-managed key still missing. |
| 2  | REAL    | HTTP API through `pkg/registry/core`. Visibility, latest, BM25, audit, public-mode + IdP guard, read-only mode, audit-citation per call. Presigned URLs above the inline cutoff still pending. |
| 3  | PARTIAL | Lock file + scope filter + claude-code/codex adapters. `--watch` and full `--profile` resolution still pending. |
| 4  | REAL    | MCP bridge does fetch + cache + adapter + atomic write per §6.6. Per-call `harness:` override. Identity passthrough via `PODIUM_SESSION_TOKEN[_FILE]`. Protocol version negotiation rejects pre-supportedSince hosts. |
| 5  | REAL    | SQLite backend + conformance suite (Memory + SQLite both pass). Postgres backend still pending. |
| 6  | REAL    | Real go-git source provider + ingest pipeline + GitHub/GitLab/Bitbucket webhook signature verification. Force-push handling + `podium layer watch` polling still pending. |
| 7  | REAL    | LayerComposer wired into HTTP server; visibility filtering applied per call; admin-only ops gated by §4.7.2 grants. OIDC / SCIM / scope claims still pending. |
| 8  | PARTIAL | extends: pinned at child ingest + merged at load_artifact (hidden-parent semantics). max_depth + notable_count + featured rendering. fold_below_artifacts / fold_passthrough_chains / target_response_tokens still pending. |
| 9  | PARTIAL | Content hashing through ingest. session_id-consistent latest. Force-push handling still pending. |
| 10 | MISSING | Layer CLI subcommands (register / list / reorder / unregister / reingest / watch / admin grant). |
| 11 | REAL    | Real JWT verification (RSA / ECDSA / Ed25519). RuntimeKeyRegistry. OS keychain (zalando/go-keyring). Real OAuth device-code HTTP client (RFC 8628) including slow_down / pending / expired / access_denied paths. |
| 12 | PARTIAL | Overlay provider exists; MCP / sync integration and BM25 local index still pending. |
| 13 | REAL    | All 10 built-in adapters; every §6.7.1 / §4.3.5 / §4.3 matrix cell exercised. MaterializationHook chain integrated into materialize still pending. |
| 14 | PARTIAL | TS SDK + `podium sync override` / `save-as` / `profile edit` shipped. TS SDK extras (subscriptions, dependents_of) pending. |
| 15 | PARTIAL | Dependency graph populated by ingest; `podium impact` / `podium dependents-of` CLI pending. |
| 16 | PARTIAL | Audit emission wired into core. File-backed sink, redaction, retention, GDPR erasure, transparency anchoring pending. |
| 17 | PARTIAL | PURL parser + structural CVE matching. CVE feed ingestion (NVD/OSV/GHSA), SBOM parsing (CycloneDX/SPDX), real notification providers pending. |
| 18 | MISSING | Helm chart, runbook, Grafana dashboard. Out of scope for code; ships as deployment artifacts. |
| 19 | PARTIAL | Reference fixture covers 3 layers / 3 types. Broader fixture coverage (rule_mode variants, hook events, signed high-sensitivity artifact) pending. |

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

## Infrastructure flagged this session

The following pieces were deferred deliberately rather than being
shipped this session. Each has a proposed solution in
`AUTONOMOUS_LOOP_AUDIT.md` and the prior conversation:

1. **Vector store + embedding pipeline** (Phase 2 hybrid retrieval, Phase 12).
   Currently BM25-only. Spec calls for BM25 + vectors via RRF.
   Proposal: load `sqlite-vec` extension via mattn driver plus an
   `embedded-onnx` provider behind a build tag; defer until catalogs
   exceed ~1K artifacts.
2. **Postgres backend** (Phase 5 standard deployment). Spec default for
   the standard topology. Proposal: `pgx` + `testcontainers-go`. About
   400 lines.
3. **Phase 18 deployment artifacts** (Helm chart, Dockerfile, Grafana
   dashboard, runbook). Pure ops material; defer until first user.
4. **Real MCP protocol library**. Current bridge handles initialize,
   tools/list, tools/call. Bidirectional MCP (server→client elicitation,
   resource notifications) is not yet wired. Proposal: hand-roll the
   ~300 lines of additional protocol.

## What's next per value-per-effort

1. **Phase 10 layer CLI** (5 subcommands). Needs server-side layer
   config table; small additional schema + endpoints.
2. **Phase 8 fold_below_artifacts / fold_passthrough_chains**. Pure
   function; medium complexity. Important once catalogs grow.
3. **Phase 17 CVE feed ingestion + SBOM parsing**. Real production
   feature: NVD / OSV / GHSA feeds, CycloneDX + SPDX parsing.
4. **Phase 19 broader fixture coverage**. Expand
   `testdata/registries/reference` to cover every artifact type and
   every visibility scenario.
5. **Postgres backend** (Phase 5) once Postgres becomes the active
   target.

## Notes on test discipline this session

- TDD for every new behavior: failing test first, implementation,
  passing test, commit.
- One existing test was modified: `TestIngest_PopulatesDependencyEdges`
  conflicted with §4.7.6's "extends: pinned at child ingest time"
  requirement. The fix pre-ingests the parent before the child.
- No spec content was modified.
