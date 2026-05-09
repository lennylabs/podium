# Test infrastructure plan

This branch builds the test infrastructure that lets Claude implement Podium end-to-end in a single autonomous session, spec-driven and test-driven. The spec in `spec/` is frozen as executable tests; the test runner reports the next failing test; Claude implements until the test passes; the runner advances. Phases gate progress so Phase 0 ships before Phase 1 begins.

The §11 verification list and the §10 MVP build sequence are the inputs. This plan converts both into concrete test artifacts, scaffolding, and an operating loop.

## 1. Goals

- **Coverage is exhaustive.** Every spec sentence with observable behavior has at least one executable test that cites it. Every error code, every CLI flag, every config precedence level, every cell of the adapter capability matrix, every visibility combination, every audit event, every wire payload, every failure mode in §6.9 has a dedicated test. The bar is "no observable behavior in the spec is unverified," not "the major flows are tested."
- **Coverage is enforced.** A coverage-budget gate in CI rejects any drop in line coverage, branch coverage, or spec-section citation count. Mutation testing surfaces logic that tests do not actually exercise. New spec sentences without corresponding tests fail the spec-coverage check.
- A single command reports what to build next. Claude does not have to scan the repo to find the next failing test.
- Phases gate progress. Tests for unbuilt phases skip rather than fail; the active phase fails loudly until done.
- Tests are deterministic. No flakes from clocks, networks, or randomness. A failing test means the implementation is wrong.
- Tests are fast. A unit run completes in under a minute on a developer laptop; the medium lane completes in under five minutes; the slow lane completes in under thirty.
- Conformance suites consolidate cross-implementation behavior. A new `HarnessAdapter` or `LayerSourceProvider` runs the same suite as the built-ins.
- The infrastructure supports three languages: Go for the registry, MCP server, CLI, and `podium sync`; Python for `podium-py`; TypeScript for `podium-ts`. Coverage commitments apply to all three.

## 2. Operating model

How Claude uses the infrastructure to build Podium:

1. `make status` prints the current phase, the count of passing and failing tests in that phase, and the next failing test by spec citation.
2. `make next` outputs the next failing test as a single record: name, spec citation, and one-line failure summary.
3. Claude reads the cited spec section, reads the test, implements the minimum required to pass.
4. `make test-phase PHASE=N` runs every test tagged for phase ≤ N. Tests tagged for higher phases skip with a clear marker.
5. When phase N is green, `make advance` moves the active phase pointer to N+1 and re-runs the suite to confirm no regressions.
6. Repeat from step 1 until phase 19.

The active phase is tracked in a single file (`.phase`) at the repo root. CI reads it to choose which test set to require.

## 3. Principles

- **Spec citation per test.** Each test carries a structured comment naming the spec section it verifies. A linter rejects new tests without citations.
- **Failure pinpoints the assertion.** A test exercises one behavior and names the field, error code, or output it expects. A failure reports the spec section and the diff.
- **Fixtures over fakes.** Behavior under realistic data outranks behavior under hand-crafted mocks. The reference registry covers every type, every visibility mode, every adapter, every error path.
- **Hermetic environment.** Every test owns its filesystem, database, and identity. Parallelism is the default; tests that share state are quarantined and tagged.
- **Golden files for exact outputs.** Adapter outputs, lock files, error envelopes, audit events, search responses are checked against golden files. `make update-golden` regenerates them after intentional changes.
- **Property tests for invariants.** Immutability, idempotent ingest, deterministic content hashing, layer-composition stability use property-based generators.
- **Conformance suites for SPIs.** Each SPI ships a generic suite. Every built-in implementation imports it; community implementations run the same suite as a quality gate.

## 4. Architecture

Three layers stack from data to execution:

1. **Fixtures** (`testdata/`): static inputs. Sample registries, sample webhook payloads, sample OIDC tokens, golden outputs.
2. **Harnesses** (`internal/testharness/`): reusable runtime helpers. In-process registry, in-process MCP client, simulated webhook emitter, identity faker, frozen clock.
3. **Tests** (`*_test.go` next to each package, plus `test/integration/`, `test/e2e/`, `test/conformance/`): the actual assertions.

Three execution lanes match the latency budgets:

- **Fast lane** (`make test-fast`): unit tests across all Go packages. Target: < 60 s on a laptop. Runs on every commit.
- **Medium lane** (`make test-medium`): integration tests with real Postgres, MinIO, and Dex via `testcontainers-go`. Target: < 5 min. Runs on PR.
- **Slow lane** (`make test-slow`): full end-to-end scenarios, harness-adapter conformance, performance baselines, soak tests. Target: < 30 min. Runs on merge to main and nightly.

Three languages are tested in parallel:

- **Go** (Phases 0–13): primary. The shared library, registry, MCP server, CLIs.
- **Python** (Phase 4 onward): `podium-py`. `pytest` + `tox` + a thin shared HTTP fixture.
- **TypeScript** (Phase 14 onward): `podium-ts`. `vitest` + `tsup` + the same shared HTTP fixture.

## 5. Project layout

```
podium/
├── .phase                          # current active phase number (0–19)
├── Makefile                        # phase-aware test orchestration
├── go.mod                          # single Go module
├── cmd/                            # binaries
│   ├── podium/                     # podium CLI (sync, lint, layer, init, etc.)
│   ├── podium-mcp/                 # MCP server binary
│   └── podium-server/              # registry server binary
├── pkg/                            # exported library code
│   ├── manifest/                   # ARTIFACT.md / SKILL.md / DOMAIN.md parsers
│   ├── domain/                     # glob resolver, DOMAIN.md merge
│   ├── layer/                      # LayerComposer, extends:, visibility evaluator
│   ├── adapter/                    # HarnessAdapter built-ins + interface
│   ├── lint/                       # IngestLinter rules
│   ├── materialize/                # atomic write, hook chain
│   ├── identity/                   # IdentityProvider implementations
│   ├── audit/                      # hash-chained audit, sinks
│   ├── search/                     # hybrid retrieval (BM25 + vectors + RRF)
│   └── ...                         # one package per spec concern
├── internal/                       # registry-internal
│   ├── registry/                   # HTTP handlers, ingest pipeline
│   ├── store/                      # RegistryStore implementations
│   ├── objectstore/                # RegistryObjectStore implementations
│   ├── webhook/                    # GitProvider implementations
│   └── testharness/                # reusable test helpers (see §7)
├── sdks/
│   ├── podium-py/                  # Python SDK + tests
│   └── podium-ts/                  # TypeScript SDK + tests
├── test/
│   ├── integration/                # cross-package integration tests
│   ├── conformance/                # SPI conformance suites (generic)
│   └── e2e/                        # full-binary end-to-end scenarios
├── testdata/
│   ├── registries/                 # sample artifact registries
│   │   ├── reference/              # the kitchen-sink reference registry
│   │   ├── visibility/             # one registry per visibility scenario
│   │   ├── extends/                # parent + child layered scenarios
│   │   └── lint/                   # one registry per lint rule (good + bad)
│   ├── manifests/                  # individual ARTIFACT.md / SKILL.md fixtures
│   ├── webhooks/                   # signed webhook payloads (GitHub, GitLab, Bitbucket)
│   ├── oidc/                       # OIDC token fixtures, signing keys
│   ├── golden/                     # golden outputs (adapter, lock file, audit, error)
│   │   ├── adapter/                # one golden tree per adapter × fixture combination
│   │   ├── lock/                   # sync.lock golden files
│   │   ├── audit/                  # audit-event golden files
│   │   └── responses/              # API response golden files
│   └── README.md                   # fixture index and conventions
├── tools/
│   ├── speccov/                    # spec-coverage reporter
│   ├── phasegate/                  # phase advancement and validation tool
│   └── golden/                     # golden-file diff and regeneration tool
├── spec/                           # frozen; tests cite into here
└── .github/workflows/              # CI lanes (fast / medium / slow)
```

The single Go module avoids the friction of multi-module setups. SDKs in `sdks/` have their own toolchains and CI jobs; each is independently buildable.

## 6. Test categories

### 6.1 Unit tests

- One test file per source file; placed next to the source.
- Pure functions and small components: parsers, glob resolver, hash chain, content addressing, error envelope formatting, schema validation.
- Run as part of `make test-fast`.
- No external services. No filesystem outside `t.TempDir()`. No real time (use `internal/clock`).

### 6.2 Integration tests

- Cross-package scenarios that exercise composition: layer merge under realistic data, ingest pipeline with a real `GitProvider`, materialization with an adapter and a hook chain.
- Located under `test/integration/`. Use `testcontainers-go` for Postgres, MinIO, and Dex when the test exercises those backends.
- Each test owns its container set; `t.Parallel()` is the default.
- Run as part of `make test-medium`.

### 6.3 Conformance tests

- Generic suites under `test/conformance/`. Each SPI has one. Examples: `harnessadapter.Suite(adapter)`, `layersource.Suite(provider)`, `identityprovider.Suite(provider)`.
- Each built-in implementation imports the suite and runs it under its own test file.
- The suite covers every contract in the SPI documentation: required calls, structured errors, idempotency, sandbox constraints, payload size limits, restartability.
- Wire-compatibility is enforced via static analysis under `tools/speccov` against the §9.3 forward-compatibility constraints.

### 6.4 End-to-end tests

- Spawn the actual binaries (`podium`, `podium-mcp`, `podium-server`) and exercise full scenarios from §14.
- Located under `test/e2e/`. Each scenario owns a temp directory tree, a fresh database, and (where needed) a containerized Dex.
- Two flavors: filesystem-source registry tests (no daemon), and server-source tests (`podium serve --standalone` or full stack).
- Run as part of `make test-slow`.

### 6.5 Property tests

- Located alongside unit tests, gated behind `-tags=property`.
- Properties cover invariants: content-hash determinism over canonicalized manifests, ingest idempotency, layer-composition associativity within precedence order, frontmatter round-trip through parser and serializer, error-envelope shape.
- Use `gopter` or stdlib `testing/quick` with seeded RNGs.

### 6.6 Golden-file tests

- Located alongside the feature they verify; the golden tree lives under `testdata/golden/`.
- `make update-golden` regenerates after intentional changes; the diff is reviewed before commit.
- Categories: adapter outputs (one tree per adapter × reference-fixture pair), lock-file shapes, audit-event records, API response payloads, error envelopes, lint reports.

### 6.7 Soak and chaos tests

- Located under `test/soak/` and `test/chaos/`. Run on a schedule rather than on PR.
- Soak: 24 h continuous load via a load generator that exercises the meta-tools. Asserts no memory growth, no descriptor leaks, audit-chain integrity preserved across registry restarts.
- Chaos: induced failures (Postgres failover, S3 stalls, IdP outages, full disk) with deterministic injection points.

## 7. Test harnesses

Each harness is a Go package under `internal/testharness/`. The harnesses below cover what tests need to share without duplicating setup.

### 7.1 `registryharness`

Spins up a complete in-process registry suitable for fast tests. The harness composes the same Go shared library the production registry uses, with backends selectable at construction time:

- `WithSQLite()` (default): in-memory SQLite, in-memory object store. No containers; sub-second startup.
- `WithPostgres()`: starts a `pgvector` Postgres container via `testcontainers-go`. Used by integration tests that exercise Postgres-specific code (RLS, schema migrations, pgvector).
- `WithLayers(...)`: registers admin layers programmatically. Each layer is either a synthetic `local` source (a temp directory the harness manages) or a synthetic `git` source backed by `go-git` against a temp bare repo.
- `WithVisibility(map)`: pre-populates the OIDC group / user mapping the visibility evaluator reads.
- `WithIdentity(faker)`: routes calls through the identity faker (§7.5).
- `WithClock(clock)`: injects a frozen or controllable clock.

Returns a `*RegistryHarness` exposing the HTTP test handler (suitable for `httptest.Server`), direct shared-library entry points (for tests that bypass HTTP), and convenience methods for ingesting fixture artifacts.

### 7.2 `mcpclientharness`

Simulated MCP client. Connects to the MCP server over an in-process pipe pair (no real stdio) and offers typed methods for `load_domain`, `search_domains`, `search_artifacts`, and `load_artifact`. Records every call and response for assertion. Supports MCP elicitation (for the device-code flow tests) by feeding scripted responses.

### 7.3 `webhookemitter`

Synthesizes signed webhook payloads from each `GitProvider` (GitHub, GitLab, Bitbucket). Tests obtain a payload, optionally tamper with it, and post it to the registry under test to exercise signature verification, force-push handling, and ingest paths.

### 7.4 `clockfreezer`

Implements `internal/clock.Clock` with controllable time. Used for resolution-cache TTLs, freeze windows, presigned-URL expiry, audit timestamps, learn-from-usage decay.

### 7.5 `identityfaker`

Issues OIDC tokens against a fake IdP managed by the harness. Pre-registers users, groups, and runtime signing keys. Tokens carry the same claims a real IdP would. Tests acquire a token by user identity and pass it through the registry's auth pipeline.

### 7.6 `fsregistryfixture`

Builds a filesystem registry on disk in a temp directory: `.registry-config`, layer subdirectories, manifests, bundled resources. Used by every filesystem-source test (Phase 0).

### 7.7 `goldenfile`

Diff-aware golden-file helper. Reads the expected file, compares against actual output, prints a unified diff on mismatch, regenerates on `UPDATE_GOLDEN=1`. Sorted, canonicalized output for stability across platforms.

### 7.8 `harnessbridge`

For harness-adapter conformance: spins up an actual instance of each target harness (Claude Code, Cursor, etc.) in a container, materializes a fixture into it, and verifies the harness loads the artifact end-to-end. Built incrementally; the first three adapters (`none`, `claude-code`, `codex` per Phase 3) come first.

## 8. Test fixtures

Fixtures are static, version-controlled, and indexed by `testdata/README.md`. Each fixture has a small comment header naming the spec section it exercises.

### 8.1 Reference registry

A single comprehensive registry covering every first-class type, every visibility mode, every `extends:` configuration, every adapter, every error mode. Located at `testdata/registries/reference/`. Used as the default fixture for adapter conformance, end-to-end scenarios, and integration tests that need realistic data.

Contents:

- One artifact of each first-class type: `skill`, `agent`, `context`, `command`, `rule`, `hook`, `mcp-server`.
- Multi-layer setup with `org-defaults`, `team-finance` (with `groups: [finance]`), `team-platform` (`groups: [engineering]`), `public-marketing` (`public: true`), `joan-personal` (`users: [joan]`).
- Cross-layer `extends:` chains.
- `DOMAIN.md` files at the appropriate hierarchy levels, including `unlisted: true`, `include:`, `exclude:`, glob imports.
- Bundled resources spanning every relevant file type (Python script, Jinja template, JSON schema, binary blob, large external resource).
- Signed artifacts at multiple sensitivities.
- Deprecated artifacts with `replaced_by:`.
- One `command` artifact with `expose_as_mcp_prompt: true`.
- One `rule` artifact for each `rule_mode` value (`always`, `glob`, `auto`, `explicit`).
- One `hook` artifact for each common canonical event.

### 8.2 Visibility scenarios

One small registry per scenario under `testdata/registries/visibility/`. Each scenario asserts a specific row of the §11 visibility tests. Scenarios include: `public-only`, `org-only`, `groups-disjoint`, `groups-overlap`, `users-explicit`, `multi-condition-union`, `user-defined-cap`, `mcp-server-filtered-from-bridge`, `admin-override-audited`.

### 8.3 Webhook payloads

`testdata/webhooks/{github,gitlab,bitbucket}/{push,merge,tag,force-push}.json`, each with a matching `*.sig` file produced under a known HMAC secret. Negative payloads (`*.invalid-sig.json`) for signature failure tests.

### 8.4 OIDC fixtures

`testdata/oidc/`: signing key pair, JWKS document, sample tokens for representative users (`joan`, `admin`, `finance-lead`, `unknown`). Each token is regenerated from the source by `tools/oidc/regen` when expirations slip; the regenerator uses the frozen clock so tokens stay reproducible.

### 8.5 Golden outputs

Under `testdata/golden/`:

- `adapter/<adapter>/<fixture>/...`: the expected file tree per adapter × fixture combination.
- `lock/<scenario>.yaml`: expected `sync.lock` after each scenario.
- `audit/<scenario>.jsonl`: expected audit events.
- `responses/<endpoint>/<scenario>.json`: expected HTTP response payloads.
- `errors/<code>.json`: expected error envelopes for each namespaced error code.

### 8.6 Lint scenarios

`testdata/registries/lint/`: paired good/bad registries for each lint rule. The bad registries have a single intentional violation; the test asserts the lint rule fires with the expected diagnostic and the good registries pass clean.

## 9. Spec traceability and coverage enforcement

### 9.1 Annotation format

Every test names the spec section it verifies in a structured comment immediately above the function:

```go
// Spec: §4.6 Layer ordering — admin-defined layers appear before user-defined layers in the composition order.
// Phase: 7
func TestLayerComposer_AdminBeforeUser(t *testing.T) { ... }
```

The annotation has three parts: section number, short title, and the specific assertion. Sentence-level granularity is preferred for sections with many discrete claims; the `assertion` part lets multiple tests cite the same section without collision. The `tools/speccov` reporter parses the annotation, indexes tests by section and assertion, and reports orphan claims.

### 9.2 Coverage tooling

Three tools compose the coverage signal. Each runs in CI; each can be invoked locally for fast feedback.

#### `tools/speccov`

Reads every `*_test.go` (Go), `test_*.py` / `*_test.py` (Python), and `*.test.ts` (TypeScript), parses citations, and produces:

- `speccov report`: a table of every spec section with the count of tests citing it and the per-section assertion list each test covers.
- `speccov uncovered`: spec sections with zero citing tests. Broken into "no tests" and "tests exist but cite a different sub-claim."
- `speccov drift`: tests citing sections that no longer exist in the spec.
- `speccov sentences`: a sentence-level matrix that breaks each spec paragraph into its observable claims and reports whether each claim has a citing test. Generated by a deterministic spec parser that splits on sentence boundaries inside spec sections that contain normative behavior.

`speccov` is required to pass on every PR. The fast lane runs `speccov drift`; the medium lane runs `speccov uncovered` and `speccov sentences`. Both fail on regressions.

#### `tools/coverage`

Wraps language-native coverage tools and enforces budgets:

- **Line coverage**: `go test -coverprofile`, `pytest --cov`, `vitest --coverage`. Aggregated into a single report per language. Hard floor: 95% line coverage in `pkg/`, `internal/registry/`, and the SDK packages. Cmd binaries are exempt only for `main()` entry-point glue.
- **Branch coverage**: `go test -covermode=atomic` plus a custom branch analyzer (Go's stdlib coverage is line-only). Hard floor: 90% branch coverage in the same packages.
- **Per-file regression gate**: a coverage decrease in any file of more than 0.1 percentage points fails CI even if the global budget is held.

The tool emits `coverage/summary.md` with a per-package and per-file breakdown, surfaced as a PR comment.

#### `tools/mutation`

Mutation testing via `gremlins` (Go), `mutmut` (Python), `stryker` (TypeScript). Configured to mutate every package under `pkg/`, `internal/registry/`, and the SDKs. Each release-candidate run must achieve a mutation score ≥ 85%. Mutations that survive the test suite are surfaced in `coverage/mutations.md` as TODOs.

Mutation testing is too slow for every PR; it runs nightly and on release branches. PR-time mutation testing is scoped to changed files only.

#### `tools/matrix`

Generates the expected-test list from spec tables. The spec contains explicit matrices that translate one-to-one into test cases:

- **§6.7.1 capability matrix**: 11 fields × 9 first-class adapters = 99 cells, each requiring a (field, adapter, expected behavior) test. The tool reads the matrix from the spec, generates the expected test names, and verifies each exists in `test/conformance/adapter/`.
- **§6.10 error codes**: every namespaced error code (`auth.*`, `config.*`, `ingest.*`, `materialize.*`, `quota.*`, `mcp.*`, `network.*`, `registry.*`, `domain.*`) requires a test that produces and asserts the envelope.
- **§6.9 failure modes**: every row of the failure-modes table requires an integration test that triggers the failure and asserts the documented behavior.
- **§4.6 visibility combinations**: every union of `public`, `organization`, `groups`, `users` requires a test under `test/integration/visibility/`.
- **§4.6 merge semantics table**: every row of the field-semantics table requires a test that exercises the merge rule (scalar wins, list append, deep-merge, most-restrictive).
- **§7.5.2 precedence levels**: every level of the six-level precedence chain requires a test that confirms the level wins where it should.
- **§4.3.5 canonical hook events**: every canonical event requires a `hook` artifact fixture and a materialization test per ✓-marked adapter.

`matrix audit` reports cells without a test. `matrix scaffold` generates skeletons for missing cells. CI runs `matrix audit` and fails on uncovered cells.

### 9.3 Lint

A `golangci-lint` custom rule (and Python / TypeScript equivalents) rejects new test functions without a `// Spec:` and `// Phase:` annotation. The rule is bypassable for genuinely spec-orthogonal helpers, with an explicit `// Spec: n/a — <reason>` form. The lint also rejects tests with no assertions (a common Go anti-pattern), tests that only check `err == nil`, and tests longer than 100 lines without a clear comment block explaining the structure.

### 9.4 Coverage gate

A single command, `make coverage-gate`, runs `speccov`, `coverage`, `matrix audit`, and (on release branches) `mutation`. CI invokes it on every PR. Failure on any sub-check blocks the merge.

## 10. Phase tagging and gates

### 10.1 Tags

Each test carries a phase tag in its annotation:

```go
// Spec: §13.11 Filesystem registry — multi-layer dispatch via .registry-config.
// Phase: 0
func TestFilesystemRegistry_MultiLayerDispatch(t *testing.T) { ... }
```

The phase tag governs whether the test runs under the active phase. A `Phase: N` tagged test runs when the active phase is ≥ N and skips with a clear message otherwise.

### 10.2 Phase-tagged skip helper

```go
// internal/testharness/phase.go
func RequirePhase(t *testing.T, phase int) {
    if ActivePhase() < phase {
        t.Skipf("phase %d (active: %d)", phase, ActivePhase())
    }
}
```

Tests that depend on machinery from a higher phase invoke `RequirePhase(t, N)` at the top.

### 10.3 Phase advancement

`tools/phasegate advance` runs the full suite for the current phase, and only on green updates `.phase` to the next number. A regression in a previously-green phase blocks advancement; the failing tests are reported with their citations.

### 10.4 Phase-to-§11 mapping

The verification tests in §11 map onto MVP phases (§10). The mapping is maintained in `tools/phasegate/mapping.yaml` and used by `make status` to show what's left in the current phase.

| Phase | Tests gated to this phase (selection)                                                                                   |
| ----- | ----------------------------------------------------------------------------------------------------------------------- |
| 0     | Filesystem-registry dispatch, `podium sync` against filesystem source, `podium serve --standalone`, sqlite + sqlite-vec |
| 1     | Manifest schema validation, `podium lint`, signing, agentskills.io compliance                                           |
| 2     | Registry HTTP API: meta-tools against `--standalone`                                                                    |
| 3     | `podium sync` for `none` / `claude-code` / `codex`, lock file, `--watch`, `podium init`, `podium config show`           |
| 4     | MCP server core, `podium-py` SDK, read CLI                                                                              |
| 5     | Multi-tenant data model in Postgres + pgvector + S3                                                                     |
| 6     | `LayerSourceProvider` SPI, `git` and `local` source built-ins, webhook ingest                                           |
| 7     | `LayerComposer`, visibility filtering, OIDC + SCIM                                                                      |
| 8     | Domain composition, `DOMAIN.md` parser, `extends:` resolver, discovery rendering                                        |
| 9     | Versioning, immutability, content-hash cache keys, `latest` resolution                                                  |
| 10    | Layer CLI: register, list, reorder, unregister, reingest, watch                                                         |
| 11    | `IdentityProvider` implementations: `oauth-device-code`, `injected-session-token`                                       |
| 12    | Workspace `LocalOverlayProvider`, local BM25 search                                                                     |
| 13    | Remaining `HarnessAdapter` built-ins, `MaterializationHook` SPI, full conformance suite                                 |
| 14    | `podium-ts` SDK, `podium sync override` / `save-as` / `profile edit`                                                    |
| 15    | Cross-type dependency graph, reverse index, impact analysis CLI                                                         |
| 16    | Registry audit log, `LocalAuditSink`, hash-chain integrity                                                              |
| 17    | Vulnerability tracking, SBOM ingestion, `NotificationProvider`                                                          |
| 18    | Helm chart, reference Grafana dashboard, runbook                                                                        |
| 19    | Example artifact registry verifying the full multi-layer, multi-type catalog end-to-end                                 |

## 11. Build order for the test infrastructure itself

The infrastructure is built in five stages. Each stage produces something usable; no stage waits on later stages.

### Stage 1: Foundations

- `go.mod` + initial directory layout per §5.
- `Makefile` with `test-fast`, `test-medium`, `test-slow`, `lint`, `update-golden`, `status`, `next`, `advance`.
- `golangci-lint` config including the spec-citation linter.
- CI workflows for fast / medium / slow lanes.
- `internal/clock` interface with the production and test implementations.
- `internal/testharness/` skeleton: `phase.go`, `goldenfile.go`, `tempdir.go`.
- `tools/speccov` minimum viable reporter: parses annotations, produces `report` and `uncovered`.
- `.phase` initialized to `0`.

Exit criteria: `make test-fast` runs, the empty suite passes, `make status` reports phase 0 with zero tests.

### Stage 2: Phase 0 fully tested

- `testdata/registries/reference/` skeleton at the level Phase 0 needs (filesystem-registry layout, two layers, a few skills).
- `internal/testharness/fsregistryfixture` complete.
- All Phase 0 tests written and failing: filesystem-registry dispatch, single-layer vs multi-layer, `.registry-config` parsing, layer ordering, basic `podium sync` against filesystem source, lock-file write, idempotency, `--dry-run`.
- Golden files for Phase 0 outputs.
- `internal/testharness/registryharness` with the `WithSQLite()` and filesystem-source paths complete.

Exit criteria: `make test-phase PHASE=0` reports the full Phase 0 test count, all failing for known-implementation reasons. Claude can begin implementation against this signal.

### Stage 3: Phases 1–4 tests

- Lint test scenarios across Phase 1 (one fixture per lint rule).
- HTTP API test harness for Phase 2 (`httptest.Server`-backed registry).
- `podium sync` adapter scenarios for Phase 3 (`none`, `claude-code`, `codex` golden trees).
- MCP client harness for Phase 4 (in-process MCP wire).
- Python SDK test harness skeleton for Phase 4 (shared HTTP fixture from Go).

Exit criteria: every test through Phase 4 is written and tagged. `make test-phase PHASE=4` reports the count and failures.

### Stage 4: Phases 5–13 tests

- Postgres-backed registry harness (`WithPostgres()`).
- Webhook emitter (`webhookemitter`) for GitHub, GitLab, Bitbucket.
- Identity faker (`identityfaker`) for OIDC / SCIM.
- Conformance suites for `LayerSourceProvider`, `HarnessAdapter`, `IdentityProvider`, `TypeProvider`, `MaterializationHook`.
- Adapter golden trees for every built-in adapter.
- Multi-region and air-gapped scenarios.

Exit criteria: every test through Phase 13 is written and tagged.

### Stage 5: Phases 14–19 tests

- TypeScript SDK test harness (`vitest` against the same shared HTTP fixture).
- Cross-type dependency graph fixtures.
- Audit-stream and hash-chain test scenarios.
- Vulnerability and SBOM fixtures.
- Helm-chart smoke tests.
- Soak and chaos suites under `test/soak/` and `test/chaos/`.

Exit criteria: every test through Phase 19 is written and tagged. `make test-phase PHASE=19` is the full conformance run.

## 12. CI integration

Three workflows under `.github/workflows/`:

- `test-fast.yml`: triggered on every commit. Runs `make test-fast` + `make lint` + `make speccov-drift`. Required check.
- `test-medium.yml`: triggered on PR. Runs `make test-medium` against the active phase. Required check.
- `test-slow.yml`: triggered on merge to `main` and nightly. Runs `make test-slow` plus the conformance suite. Failures open an issue rather than blocking merges.

The active phase determines which test set is required. CI reads `.phase` and runs `make test-phase PHASE=$(cat .phase)`. Pull requests that introduce regressions in the active phase are blocked.

A separate `.github/workflows/spec-coverage.yml` runs the speccov reporter on PR and posts a comment showing newly uncovered sections.

## 13. Observability for autonomous runs

Claude needs feedback that compresses into the conversation context. Three signals matter:

- **`make next`**: a single record (test name + spec citation + one-line failure summary) that fits in one screenful. The default scope is the active phase.
- **`make status`**: phase pointer, count of passing / failing / skipped tests in the active phase, count of tests deferred to higher phases. One screenful.
- **`make speccov`**: the spec-coverage table by section. Used to confirm a phase's tests cover its spec sections before declaring completion.

A failing test prints in a structured envelope `{name, file:line, spec_section, expected, actual, diff}` so a single read gives Claude everything it needs.

## 14. Coverage commitments

Coverage is the primary signal for "Podium is built correctly." This section enumerates what must be covered. Items below are gated by `tools/matrix` and `tools/speccov`; CI rejects merges that leave any item uncovered.

### 14.1 Behavior coverage

Every observable behavior in the spec has a dedicated test. Observable means: a value the API returns, a file written to disk, a record written to a log, a status code on an HTTP response, a stderr message, an exit code, a side effect on the database. Internal helper functions without externally visible behavior are covered transitively through the behaviors that use them.

### 14.2 Structural coverage

| Kind | Source | Test count target |
| --- | --- | --- |
| Spec sections | `spec/*.md` headings | ≥ 1 test per section, ≥ 1 test per normative sub-claim |
| Error codes | §6.10 namespaced codes | exactly 1 envelope test per code; integration test per failure path that produces it |
| Failure modes | §6.9 table | 1 integration test per row |
| Capability matrix cells | §6.7.1 | 1 test per (field, adapter) pair, three classes (`✓`, `⚠`, `✗`) |
| Visibility unions | §4.6 | 1 test per `{public, organization, groups, users}` subset |
| Field-merge rules | §4.6 merge table | 1 test per row, plus 1 test per "default for unlisted fields" entry |
| Config precedence levels | §7.5.2 | 1 test per level showing it wins over every lower level |
| Canonical hook events | §4.3.5 | 1 test per (event, ✓-adapter) pair |
| `rule_mode` values | §4.3 | 1 test per (mode, adapter) pair from §6.7.1 |
| First-class types | §4.1 | 1 lint suite per type, 1 adapter golden per type per adapter, 1 search-by-type test, 1 load test |
| Adapter outputs | §6.7 | 1 golden tree per (adapter, fixture) pair; ≥ 5 fixtures per adapter |
| Webhook providers | §9 `GitProvider` | 1 signature-verify test per (provider, valid/invalid) pair |
| Identity providers | §6.3 | every provider passes the §11 "Identity provider switching test"; 1 token-rotation test per provider |
| `LayerSourceProvider` triggers | §7.3.1 | 1 test per (built-in source, trigger model) pair: webhook, polling, manual reingest |
| Audit events | §8.1 | 1 test per event type; format check via golden file |
| `podium` CLI subcommands | §7 | 1 test per subcommand, 1 test per documented flag, 1 test per documented flag combination, 1 `--help` snapshot per subcommand |
| `podium-mcp` env vars | §6.2 | 1 startup test per env var, 1 test per documented combination |
| MCP meta-tools | §5 | 1 happy-path test per tool; 1 test per documented argument; 1 test per documented response field |
| `sync.lock` schema | §7.5.3 | 1 round-trip test per field; 1 conflict-resolution test per `toggles` operation |
| Standalone defaults | §13.10 | 1 test per "sensible default" override |
| Public mode | §13.10 | 1 test per safety constraint (sensitivity ceiling, loopback bind, mutual exclusion with IdP) |
| Read-only mode | §13.2.1 | 1 test per state transition, 1 test per affected endpoint |

### 14.3 Quality coverage

- **Line coverage** ≥ 95% on `pkg/`, `internal/registry/`, `sdks/podium-py/podium/`, `sdks/podium-ts/src/`. Files below 95% block merges.
- **Branch coverage** ≥ 90% on the same set.
- **Mutation score** ≥ 85% on release-candidate runs. Surviving mutations are tracked and either killed (new test added) or annotated as deliberately unverified with a justification.
- **Spec sentence coverage** ≥ 95% on normative sentences (those describing observable behavior, as identified by the `speccov sentences` parser). The remaining ≤ 5% must be explicitly annotated as non-normative.

### 14.4 Negative coverage

For every "the registry rejects X" claim in the spec, the test suite includes:

- A positive test confirming the valid case is accepted.
- A negative test confirming the invalid case is rejected with the documented error code.
- A boundary test confirming the rejection threshold (e.g., the 1 MB per-file soft cap fires at 1 MB + 1 byte and not at 1 MB - 1 byte).

### 14.5 Adversarial coverage

The §11 security tests are exhaustive against the documented threat model:

- Visibility bypass attempts: every layer × identity combination where the layer should be invisible asserts that nothing leaks.
- Token forgery: unsigned, expired, wrong-issuer, wrong-audience, wrong-runtime, replayed JWTs each have a rejection test.
- Webhook signature attacks: replay, body tampering, header tampering, bit-flip in signature each have a rejection test.
- Path traversal: every path-accepting input has a `..` and absolute-path test.
- Manifest injection: prompt-injection markers in `description`, `when_to_use`, `keywords`, and bundled file contents each have a propagation test asserting the harness sees the documented trust regions.
- Sandbox escape: the adapter sandbox contract has a positive test per built-in adapter and a violation test per disallowed operation (network, subprocess, out-of-destination write).

### 14.6 Compatibility coverage

Cross-mode equivalence tests ensure migrations are mechanical:

- Filesystem source ↔ standalone server pointed at the same directory: byte-identical materialized output.
- Standalone server ↔ standard server with the same artifact set: equivalent API responses (modulo identity).
- Standard backend variants (`pgvector` ↔ `sqlite-vec` ↔ external vector backends): equivalent search ranking on a fixed corpus.
- Embedding-provider switching: query-result equivalence where the model's outputs are bitwise identical.

### 14.7 What is not tested

A small list of explicit non-goals keeps the bar honest:

- Performance numbers under unrealistic hardware (tests verify the latency contracts in §7.1 hold on a reference rig; absolute numbers on developer laptops are advisory).
- Behavior of third-party harnesses themselves. Tests verify Podium's adapter output is correct; they do not regress when a harness ships a breaking change. A separate "harness-tracking" job runs against current harness releases nightly.
- Behavior of external services (OpenAI, Pinecone, Sigstore) at the network layer. Their interactions are stubbed with documented contract fixtures; a separate "live integration" job runs against the real services on a schedule.

## 15. Determinism mechanics

The full set of determinism guarantees:

- **Time**: `internal/clock.Clock` is the only time source. Production code calls it; tests inject `clockfreezer`. No `time.Now()` in non-test code outside `internal/clock`.
- **Randomness**: every random source takes a `*rand.Rand` parameter. Tests pass a seeded RNG.
- **Network**: no test makes outbound network calls. The webhook emitter, identity faker, and HTTP harness are in-process. Container-backed tests bind only to loopback ports allocated by `testcontainers-go`.
- **Filesystem**: every test uses `t.TempDir()`. The registry-as-disk fixtures are constructed under the temp dir.
- **Concurrency**: parallelism is the default. `t.Parallel()` is invoked at the top of every test that doesn't share global state. Tests that mutate shared state are marked and serialized.
- **Goroutine leaks**: `goleak.VerifyTestMain` runs in every package's `TestMain`.

## 16. Open decisions

The plan above commits to several choices. The decisions below are deliberately deferred and want sign-off before Stage 1 begins:

1. **Assertion library**. Standard library plus a small custom helper, or `testify`. Standard library produces lower-friction stack traces; `testify` reduces boilerplate. Recommendation: standard library plus a small `internal/testharness/assert` for the patterns we hit repeatedly.
2. **Mocking strategy**. Hand-written fakes implementing the SPI interfaces, or `gomock`-generated mocks. Recommendation: hand-written fakes; the SPIs are small enough that generated mocks add more friction than they remove.
3. **Container library**. `testcontainers-go` (mature, broad backend support) or `dockertest` (lighter). Recommendation: `testcontainers-go`.
4. **Property-test framework**. `gopter` (opinionated) or stdlib `testing/quick` (minimal). Recommendation: `gopter`; the shrinking and generators justify the dependency for invariant tests.
5. **Spec-citation enforcement**. Custom `golangci-lint` rule (effort but integrates with existing CI lint), or a standalone `tools/speccov` check. Recommendation: standalone; keeps the lint config simple and lets the citation rule evolve independently.
6. **Reference registry size**. A single kitchen-sink registry covers every type and every visibility mode but bloats fast-lane runtime. Alternative: a small "default" registry plus per-feature focused registries. Recommendation: default to the small registry for fast-lane tests and let the kitchen-sink fixture ride the medium and slow lanes only.
7. **Harness-adapter end-to-end strategy**. Spinning up real harnesses in containers is slow and fragile. Alternative: each adapter ships a minimal "harness simulator" that loads the adapter's output and asserts the harness's discovery succeeds. Recommendation: the simulator path for the conformance suite, with one real-harness end-to-end per adapter run nightly.
