# Autonomous loop audit (Stage A)

Pressure test of the framework: bump `.phase` from 1 to 19 and watch the
signals. Goal is to identify framework bugs before they cost time during
real implementation.

## Method

```sh
for p in 2 3 4 ... 19; do
  echo $p > .phase
  ./bin/phasegate status
done
./bin/phasegate next         # at every phase
./bin/phasegate advance      # at the cap
```

## Findings

### 1. Every phase is GREEN out of the box (intentional + problem)

The autonomous loop reports every phase from 1 through 19 as GREEN with
no implementation work. The reason: the implementations shipped in
Stages 1–5 satisfy the tests written in the same stages. There is no
external signal to drive Podium implementation forward.

Implication for E (real Podium implementation): the existing tests do
not surface the gap between "spec-correct implementation" and
"placeholder that passes the test." A few examples:

- `pkg/store` ships a memory backend; the §4.7.1 Postgres / SQLite
  tenancy contract is not exercised.
- `pkg/layer/source.Git.Snapshot` returns a "phase pending" sentinel;
  no test forces a real git fetch.
- `pkg/identity.OAuthDeviceCode` exposes the surface but never runs
  the device-code flow against a real IdP.
- `pkg/audit.Memory` provides the hash chain but is not wired into the
  registry server's request path.

Mitigation: B (implementation audit) documents which phases are real
vs scaffolded; D (matrix tool) generates the missing tests that would
surface these gaps.

### 2. `phasegate advance` walked past phase 19 to 20, 21, ... infinitely

Bug: the original code had no upper bound. `Advanced from phase 19 to
20.` was indistinguishable from a real advancement.

Fix shipped: `MaxPhase = 19` constant; advance refuses past it with the
message `active phase is N; phase 19 is the last MVP phase (spec §10).
Nothing to advance to.` Status at phase 19 reports `Final MVP phase
reached.`

### 3. `make next` returned `(no failing tests in the active phase)`
   at every phase

When a phase has tests and they all pass, "no failing tests" is the
correct answer. When a phase has zero tests, `make next` reports the
same string, which is misleading: a green phase with no tests is not
a green phase.

Fix shipped: `phasegate advance` now refuses to bump `.phase` when the
active phase has zero spec-citing tests, with the message:
`phase N has no spec-citing tests. Refusing to advance: a green phase
with no tests is not a green phase.`

`status` reports a similar distinction.

### 4. Phase 10 (layer CLI) ships zero tests

Spec §10 calls out Phase 10 as "Layer CLI: register / list / reorder /
unregister / reingest / watch; user-defined-layer cap; freeze windows;
admin grant/revoke." Stage 4 elided this phase in the scaffolding pass.

Now that advance enforces "must have spec-citing tests," bumping to
phase 10 will block until tests exist. Captured as a TODO in
`IMPLEMENTATION_STATUS.md` (Stage B output).

### 5. Phase 18 (deployment) ships zero tests, by design

Spec §10 Phase 18: "Helm chart, reference Grafana dashboard, runbook."
This is configuration and ops material rather than testable code. Stage
4 elided tests deliberately.

The "no spec-citing tests" gate now blocks advance from phase 18 → 19.
Either Phase 18 needs tests (Helm template lint, dashboard JSON schema
check) or the gate needs an explicit allowlist for testless phases.

Captured as a TODO; resolution belongs in C/D.

### 6. Python and TypeScript tests are invisible to phasegate

`go test ./...` walks Go packages only. The Python tests under
`sdks/podium-py/tests/` and the TypeScript tests under
`sdks/podium-ts/src/` never surface in `phasegate status`.

That means:
- Phase 4 (Python SDK): the Go MCP integration tests pass; the Python
  tests do not contribute to the phase counts.
- Phase 14 (TS SDK): same situation; phase advance from 13 → 14 does
  not require any test to actually run.

Captured as a TODO; resolution belongs in C (richer test report) or D
(language-aware coverage gate).

### 7. `make next` failure record is thin

When a test does fail, the record is just `name`, `file` (package, not
file path), and `summary: "see go test ./... output for details"`.

Plan §13 calls for `{name, file:line, spec_section, expected, actual,
diff}`. This is the highest-leverage individual fix because every
implementation cycle reads `make next`.

This is the work in C.

## Fixes shipped in this commit

- `phasegate` caps advance at `MaxPhase = 19`.
- `phasegate advance` refuses phases with zero spec-citing tests.
- `phasegate status` distinguishes "no tests defined" from "tests pass."

## TODOs surfaced by this stage

These belong to subsequent stages, not A:

- Document which phases are spec-correct vs scaffolded (B).
- Make `make next` carry spec citation, file:line, and a useful failure
  diff (C).
- Wire Python and TypeScript test runners into the phase report (C/D).
- Generate expected-test lists from the spec's matrices (§6.7.1, §6.10
  error codes, §6.9 failure modes) so missing coverage is visible (D).
- Coverage budget per `pkg/`, `internal/registry/`, `sdks/` (D).
- Mutation testing on a release-candidate cadence (D).
- Phase 10 layer CLI tests do not exist; advancement is blocked.
- Phase 18 deployment artifacts have no test surface; either add Helm
  template lint / dashboard JSON tests, or add an explicit testless
  phase allowlist.
