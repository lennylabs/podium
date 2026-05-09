# Remaining-gap fix plan

Plan to close the remaining gaps in the Podium implementation,
grouped into shippable batches with concrete effort estimates and
test strategies. Each gap entry has the same format:

- **What's missing.** Specific behavior or surface that's incomplete.
- **Where it lands.** File paths and packages.
- **Test strategy.** Tier 1 (always-on) and, where relevant, Tier 2
  (env-gated live).
- **Effort.** Lines of code + estimated time.

## Batch A — Plumbing (~half a day)

Small wiring changes to existing components. No new packages.

### A1. SCIM → visibility integration

**What's missing.** SCIM CRUD ships and `Store.MembersOf` returns a
group's userNames, but `layer.Visible` still consults
`Identity.Groups` from the JWT directly. A `groups: [engineering]`
filter doesn't resolve `engineering` against the SCIM store.

**Where it lands.** `pkg/layer/composer.go`: `Visible` accepts a
`GroupResolver func(group string) []string`. The visibility
evaluator calls it to expand SCIM group memberships.
`pkg/registry/server` wires `scim.Store.MembersOf` as the resolver
when `WithSCIM` is set.

**Test strategy.** Tier 1: compose layers with a SCIM-resolved
group; verify a user is visible iff SCIM puts them in the group.
Cover the case where the JWT carries the group directly (no SCIM
lookup needed).

**Effort.** ~80 LOC + ~120 LOC tests. ~1 hour.

### A2. Transparency anchoring scheduler

**What's missing.** `audit.Anchor` works on demand; nothing in
`cmd/podium-server` calls it on a cadence.

**Where it lands.** `cmd/podium-server/main.go`: a goroutine that
calls `audit.Anchor` every `PODIUM_AUDIT_ANCHOR_INTERVAL` (default
1 h) when a Sigstore-keyless signer is configured. Cleanly
no-op when unconfigured.

**Test strategy.** Tier 1: drive the scheduler with a fake clock,
verify Anchor is called the expected number of times.

**Effort.** ~60 LOC + ~80 LOC tests. ~30 minutes.

### A3. Sandbox profile enforcement

**What's missing.** `sandbox_profile` parses correctly, but
`PODIUM_ENFORCE_SANDBOX_PROFILE=true` doesn't gate materialization.

**Where it lands.** `cmd/podium-mcp/main.go`'s `loadArtifact`:
when `PODIUM_ENFORCE_SANDBOX_PROFILE=true` and the artifact's
sandbox_profile is not the host's declared capability set, refuse
to materialize with `materialize.sandbox_unsupported`.

**Test strategy.** Tier 1: stub the host capability set, request
a `sandbox_profile: seccomp-strict` skill against a host that
declares only `unrestricted`, verify rejection.

**Effort.** ~80 LOC + ~100 LOC tests. ~1 hour.

### A4. Idempotent sync clears stale files

**What's missing.** A sync that drops an artifact does not delete
the file it materialized last time. The target directory
accumulates removed artifacts.

**Where it lands.** `pkg/sync.Run`: track "files this run wrote"
and diff against the lock file's previous list; remove anything
the previous sync wrote that this sync didn't.

**Test strategy.** Tier 1: sync a registry with artifact A, sync
again with the registry mutated to drop A, verify A's files are
gone from the target.

**Effort.** ~120 LOC + ~150 LOC tests. ~2 hours.

## Batch B — CLI surface (~half a day)

Operator-facing commands the spec promises but cmd/podium doesn't
yet handle.

### B1. `podium serve` (alias for podium-server)

**What's missing.** Users expect `podium serve --standalone` per
spec; today they invoke a separate `podium-server` binary.

**Where it lands.** New `cmd/podium/serve.go`: re-exec
`podium-server` with the supplied flags, or in-process boot via
`pkg/registry/server`. Recommend in-process so a single binary
distribution suffices.

**Test strategy.** Tier 1: invoke `podium serve --bind=:0` in a
goroutine, hit `/healthz`, expect 200. Cancel context cleanly.

**Effort.** ~150 LOC + ~80 LOC tests.

### B2. `podium config show`

**What's missing.** No way to print the resolved client + server
configuration for diagnostics.

**Where it lands.** New `cmd/podium/config.go`: walks the
precedence chain (env → sync.yaml → defaults), prints each
resolved value with its source.

**Test strategy.** Tier 1: set various env vars, run config show,
assert each appears with the right source label.

**Effort.** ~100 LOC + ~80 LOC tests.

### B3. `podium layer update / watch`

**What's missing.** `register` / `unregister` exist; modifying an
existing layer's config (visibility, ref, etc.) requires two
round-trips. `watch` polls a layer source.

**Where it lands.** `cmd/podium/layer.go`: add two subcommands.
HTTP: `PUT /v1/layers/{id}` for update; the existing reingest
endpoint covers watch under-the-hood.

**Test strategy.** Tier 1: register a layer, update its
visibility, list and verify the new visibility took effect.

**Effort.** ~200 LOC + ~150 LOC tests.

### B4. `podium cache prune` + `podium import`

**What's missing.** Two operator conveniences. `cache prune`
walks `~/.podium/cache/` and removes entries whose content_hash
no longer appears in any reachable manifest. `import` migrates an
existing skills directory into a Podium-shaped layer.

**Where it lands.** New `cmd/podium/cache.go` and
`cmd/podium/import.go`.

**Test strategy.** Tier 1: populate cache + manifest store,
prune, verify only unreferenced entries removed. For import: feed
a skills directory, verify the resulting layer structure.

**Effort.** ~250 LOC + ~200 LOC tests.

### B5. `podium admin migrate-to-standard / runtime register`

**What's missing.** Standalone → standard migration tool; runtime
trust-key registration for §6.3.2 injected-session-token.

**Where it lands.** `cmd/podium/admin.go` extensions.

**Test strategy.** Tier 1: migrate-to-standard exports a
standalone state to a fresh standard deployment; assert layer
config + admin grants + audit chain transfer cleanly. runtime
register: verify trust key persists.

**Effort.** ~300 LOC + ~200 LOC tests.

## Batch C — Real new features (~2 days)

Each item is a new SPI or distribution unit.

### C1. NotificationProvider SPI

**What's missing.** §9 promises `NotificationProvider` for ingest-
failure and operational notifications, separate from §7.3.2
outbound webhooks (which carry change events).

**Where it lands.** New `pkg/notification`:

```go
type Provider interface {
    ID() string
    Notify(ctx, n Notification) error
}
type Notification struct {
    Severity   string  // "info" | "warning" | "error"
    Title      string
    Body       string
    Recipients []string
}
```

Built-ins: `email` (SMTP), `webhook` (POST + HMAC), `noop`.
Operators wire one via `PODIUM_NOTIFICATION_PROVIDER`. Triggers:
ingest failure, transparency-anchor failure, embedding-provider
outage past N minutes, layer auto-disable on force-push (strict).

**Test strategy.** Tier 1: each provider has a httptest-backed
mock receiver. Tier 2: SMTP test against a local smtpd.

**Effort.** ~600 LOC + ~400 LOC tests.

### C2. TypeProvider SPI

**What's missing.** §9 says "extension types register through a
TypeProvider SPI." The seven first-class types are hardcoded in
`pkg/manifest`; no plugin point to register an eighth.

**Where it lands.** New `pkg/typeprovider`:

```go
type TypeProvider interface {
    ID() string
    Type() manifest.Type
    Validate(*manifest.Artifact) []lint.Diagnostic
    LintRules() []lint.Rule
    Adapt(adapter.Source) ([]adapter.File, error)
}
```

`pkg/manifest` reads from a registry of providers; the seven
first-class types ship as TypeProvider implementations. New
types register through the SPI as Go-module plugins.

**Test strategy.** Tier 1: register a fake "macro" type with a
custom validator, ingest a `type: macro` manifest, verify the
provider's validator runs.

**Effort.** ~500 LOC + ~300 LOC tests + the ~100-LOC refactor of
`pkg/manifest` to read from a registry. ~1 day total.

### C3. `podium-py` SDK

**What's missing.** §10 Phase 4 lists `podium-py` alongside the
read CLI. Only `sdks/podium-ts` exists.

**Where it lands.** New `sdks/podium-py/`:

- `podium/__init__.py`: Client class with `load_domain`,
  `search_domains`, `search_artifacts`, `load_artifact`,
  `dependents_of`, `preview_scope`, `subscribe()` (async iterator).
- `setup.py` + `pyproject.toml`.
- pytest test suite.

**Test strategy.** Tier 1: pytest against a `httptest`-style mock
server (use `responses` or `pytest-httpserver`).

**Effort.** ~600 LOC Python + ~400 LOC tests.

## Batch D — Configuration surface (~half a day)

Three small spec features that close env-var promises.

### D1. `registry.yaml` parsing

**What's missing.** §13.10 references `~/.podium/registry.yaml`
for server config; current `cmd/podium-server` reads env vars
only.

**Where it lands.** `cmd/podium-server/config.go`: read the YAML,
overlay onto env defaults.

**Test strategy.** Tier 1: write a YAML, boot the server, assert
config matches.

**Effort.** ~150 LOC + ~100 LOC tests.

### D2. `PODIUM_DEFAULT_LAYER_VISIBILITY`

**What's missing.** Spec says this controls the default
visibility for newly-registered layers. Not honored.

**Where it lands.** `pkg/registry/server/layers.go`'s register
endpoint: when no visibility is supplied, fall back to the
configured default.

**Test strategy.** Tier 1: set the env, register a layer without
visibility, verify the configured default takes effect.

**Effort.** ~50 LOC + ~50 LOC tests.

### D3. Read-only mode probe

**What's missing.** `PODIUM_READONLY_PROBE_FAILURES` /
`_INTERVAL` are spec'd but the registry doesn't probe-and-flip
when Postgres becomes unreachable.

**Where it lands.** `cmd/podium-server`: a goroutine that pings
the configured store every `_INTERVAL` and flips the server into
read-only mode after `_FAILURES` consecutive failures.

**Test strategy.** Tier 1: drive a fake store that fails N times,
verify the server enters read-only mode.

**Effort.** ~120 LOC + ~150 LOC tests.

## Batch E — Web UI (~2 days)

### E1. `--web-ui` SPA

**What's missing.** §13.10 promises a SPA at `/ui/`. Not started.

**Where it lands.** New `web/ui/` (TypeScript + a small
framework — recommend Preact or Lit for binary-size reasons),
embedded into the Go binary via `//go:embed`. Pages: domain
browser, search, artifact viewer.

**Test strategy.** Tier 1: hit `/ui/` and verify the index loads.
Frontend tests via Vitest. Visual regressions deferred.

**Effort.** ~1500 LOC frontend + ~200 LOC server wiring + ~300
LOC tests. ~2 days.

## Batch F — Verification gaps (~half a day)

### F1. p99 latency benchmark suite

**What's missing.** §7.1 documents budgets; no benchmark enforces
them.

**Where it lands.** New `test/bench/`: Go benchmarks for
`load_domain`, `search_artifacts`, `load_artifact` against the
reference fixture. Asserts p99 < the spec's budget.

**Effort.** ~300 LOC + budget table.

### F2. Live integration test job

**What's missing.** Sigstore staging, Postgres+pgvector, S3
(MinIO), embedding providers — each has Tier 2 tests, but CI
doesn't run them by default.

**Where it lands.** New GitHub Actions workflow
`.github/workflows/integration.yml`: spins up Postgres + MinIO
service containers, sets `PODIUM_*_LIVE` env vars, runs the
integration test set.

**Effort.** ~150 lines of YAML + secrets management.

## Suggested execution order

```
Batch A (plumbing)              ~half day
Batch D (config surface)        ~half day
Batch B (CLI surface)           ~half day
Batch C (real new features)     ~2 days
Batch F (verification)          ~half day
Batch E (web UI)                ~2 days
```

A→D→B finishes operator-facing surface in a day. C is the biggest
chunk (NotificationProvider SPI + TypeProvider SPI + podium-py).
E is biggest single item; defer if a faster delivery is preferred.
F can run in parallel with anything since it's CI-only.

Total: ~6 days of focused work for the full set; ~3 days to ship
A+B+C+D (everything except web UI and benchmarks).
