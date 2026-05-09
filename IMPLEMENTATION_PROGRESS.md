# Implementation progress (session checkpoint)

This doc supersedes `IMPLEMENTATION_STATUS.md` for the current state.
That doc described the framework-only baseline before the
implementation pass; this one documents where the code stands now.

## Summary

- **16 commits** on `initial-implementation` since branching from `main`.
- **All tests pass** at the active phase (1) and at the maximum phase (19).
- **Matrix coverage**: 4 of 5 spec matrices fully covered; 177 of 199
  cells now have citing tests (was 13 at start of session). The 12
  remaining cells are §6.10 error codes whose production mechanisms
  are still scaffolded.
- **Spec section coverage**: 42 of 138 sections cited (was 19).
- **Line coverage**: 62.0% (was 50.4%).

## Phase-by-phase status

The categories REAL / PARTIAL / SCAFFOLDED / STUB / MISSING are the
same as `IMPLEMENTATION_STATUS.md`. Δ shows the change in this session.

| Phase | Status | Δ | Notes |
| ---: | --- | :-: | --- |
| 0  | REAL       |     | Filesystem-source `podium sync` end-to-end. |
| 1  | PARTIAL    |     | Lint rules + Noop sign provider; Sigstore-keyless / registry-managed key still missing. |
| 2  | REAL       | ↑↑↑ | HTTP API now goes through `pkg/registry/core`; visibility, latest, BM25 ranking, audit emission all wired. Presigned URLs above the inline cutoff still pending. |
| 3  | PARTIAL    |     | Lock file + scope filter + claude-code/codex adapters. `--watch` / `--profile` resolution / `podium config show` still missing. |
| 4  | SPLIT      |     | Python SDK + read CLI real; MCP server bridge is still a thin proxy (cache, materialize, identity attachment pending). |
| 5  | REAL       | ↑↑↑ | SQLite backend + conformance suite. Memory + SQLite both pass the same 11-test contract. Postgres backend still missing. |
| 6  | PARTIAL→REAL | ↑↑↑ | Real go-git source provider + ingest pipeline + GitHub/GitLab/Bitbucket webhook signature verification. Force-push handling and the §7.3.1 layer-watch poll mode still pending. |
| 7  | REAL       | ↑↑↑ | LayerComposer wired into the HTTP server; visibility filtering applied per call. OIDC / SCIM / scope claims still missing. |
| 8  | PARTIAL    | ↑↑  | extends: pinned at child ingest + merged at load_artifact (hidden-parent semantics). discovery rendering: max_depth, notable_count, featured shipped; fold_below_artifacts / fold_passthrough_chains / target_response_tokens pending. DOMAIN.md merge in core still pending. |
| 9  | PARTIAL    | ↑↑  | Content hashing wired through ingest. session_id consistency for latest resolution shipped. Force-push handling still pending. |
| 10 | MISSING    |     | Layer CLI subcommands not yet implemented (register / list / reorder / unregister / reingest / watch / admin grant). |
| 11 | PARTIAL    | ↑↑↑ | Real JWT verification for injected-session-token, RuntimeKeyRegistry, OS keychain integration via go-keyring. OAuth device-code HTTP client and MCP-elicitation flow still pending. |
| 12 | PARTIAL    |     | Overlay provider exists; MCP / sync integration and BM25 local index pending. |
| 13 | REAL       | ↑↑↑ | All 10 built-in adapters, every §6.7.1 capability matrix cell tested (99/99), every canonical hook event covered (20/20), every rule_mode × harness pair (36/36). MaterializationHook chain integrated into materialize still pending; per-cell ⚠/✗ enforcement at lint pending. |
| 14 | PARTIAL    |     | TS SDK shipped. `podium sync override` / `save-as` / `profile edit` and TS subscription / dependents_of pending. |
| 15 | PARTIAL    | ↑   | Dependency graph populated by ingest; `podium impact` / `podium dependents-of` CLI pending. |
| 16 | PARTIAL    | ↑↑  | Audit emission wired into core. File-backed sink, redaction, retention, GDPR erasure, transparency anchoring pending. |
| 17 | PARTIAL    | ↑↑  | PURL-based vuln matching shipped. CVE feed ingestion (NVD/OSV/GHSA), SBOM parsing (CycloneDX/SPDX), notification providers (email/Slack) pending. |
| 18 | MISSING    |     | Helm chart, runbook, Grafana dashboard. Out of scope for code; ships as deployment artifacts. |
| 19 | PARTIAL    |     | Reference fixture covers 3 layers / 3 types. Broader fixture coverage (rule_mode variants, hook events, signed high-sensitivity artifact) pending. |

## Matrix audit (final)

```
§6.7.1   total cells: 99   missing: 0    Capability matrix
§6.10    total cells: 29   missing: 12   Error codes
§4.6     total cells: 15   missing: 0    Visibility unions
§4.3.5   total cells: 20   missing: 0    Canonical hook events
§4.3     total cells: 36   missing: 0    rule_mode × harness
                          ───────────
                          Total missing: 12 / 199
```

The 12 remaining §6.10 error codes need production mechanisms before
their envelope tests can be meaningful:

- `auth.device_code_pending` — OAuth device-code HTTP client.
- `auth.forbidden` — RBAC checks (admin actions / forbidden routes).
- `config.no_registry` — pkg/sync currently surfaces a different
  error variant; needs the explicit code.
- `config.public_mode_with_idp` — public-mode + IdP startup guard.
- `config.read_only` — read-only-mode operator surface.
- `ingest.frozen` — freeze window enforcement.
- `materialize.runtime_unavailable` — host runtime requirement
  enforcement at materialize time.
- `mcp.unsupported_version` — MCP protocol negotiation.
- `network.registry_unreachable` — cache-fallback behavior on
  registry outage.
- `quota.storage_exceeded` — per-tenant quota enforcement.
- `registry.read_only` — read-only mode HTTP behavior.
- `registry.unavailable` — already exists; needs the dedicated test.

## What's most useful next

In rough order of value-per-effort for a follow-up session:

1. **Phase 4 MCP server materialization**. Cache + atomic write +
   identity threading. The Python SDK already exercises the registry
   end-to-end; getting the MCP bridge to feature-parity unlocks the
   full developer-host workflow.
2. **Phase 10 layer CLI**. Five subcommands (register / list /
   reorder / unregister / reingest), all small, all blocked on a
   registry-side layer config table.
3. **Phase 14 sync override / save-as / profile edit**. CLI work
   that's clearly specified and bounded.
4. **Phase 17 CVE feed + SBOM parser**. Most-impactful production
   feature still missing.
5. **§6.10 dozen** — knock out the remaining error-code envelopes
   alongside their production triggers as items 1–4 land.

## Notes on test discipline this session

- TDD was followed for new behaviors: write the test, run it failing,
  implement, run it passing.
- One existing test was modified (`TestIngest_PopulatesDependencyEdges`)
  because it conflicted with the §4.7.6 "extends: pinned at child
  ingest time" requirement: the test had assumed unbound parents were
  acceptable, which the spec does not permit.
- No spec content was modified.
