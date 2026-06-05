# Tests

This directory holds the cross-package and full-binary tests for Podium. Unit
tests live next to the code they cover under `pkg/`, `cmd/`, and `internal/`.
This tree holds the tests that span packages or run the built binaries.

## Layout

- `integration/` holds cross-package tests that exercise composition against
  real backends, such as layer merge, the ingest pipeline, and materialization
  with an adapter and a hook chain. A test that needs Postgres, S3, or Dex reads
  the connection from the environment and skips when it is absent.
- `conformance/` holds generic SPI conformance suites. Each built-in
  implementation runs the shared suite for its interface.
- `e2e/` holds full-binary end-to-end scenarios. Each test spawns `podium`,
  `podium-mcp`, or a standalone `podium serve`, and drives it through the CLI or
  the MCP stdio protocol against a temporary registry.
- `materialization/` holds golden-file conformance for the harness adapters.
  Each adapter's output is pinned under `testdata/`, and the golden helper
  regenerates it after an intentional change.
- `verification/` holds the §11 performance, soak, and chaos categories, written
  to run in CI within a bounded budget.
- `harness_integration/` holds opt-in tests that drive the real agent harnesses
  such as Claude Code and Cursor. The package is build-tagged
  `harness_integration`, so `go test ./...` skips it, and it needs the harness
  binaries installed. See `harness_integration/README.md`.
- `bench/` holds Go benchmarks.
- `manual-validation.md` documents the hand-run end-to-end scenarios and the
  agentic workflow that executes them.

## Running

```bash
make test                 # go test ./...: the hermetic suite plus any env-gated live paths
make test-live            # also runs the live Postgres and S3 paths
make test-live-external   # also runs the managed vector backends and paid embedding providers
make test-auth-dex        # also runs the bundled-Dex device-code login path
```

`make test` runs the whole Go suite. The live paths self-skip when their backend
environment is unset, so the command works with or without the services. Run
`make services-up` to start a local Postgres and MinIO through Docker, and
`make dex-up` to start the bundled Dex IdP.

### Live-backend configuration

The live paths read their configuration from the environment. The `make` targets
source an optional `test.env` at the repository root, copied from
`test.env.example` and gitignored, and the `e2e` and `integration` suites also
load it through `internal/testenv`, with existing environment values taking
precedence. To run a suite hermetically while a `test.env` is present, set
`PODIUM_TEST_ENV_FILE` to a path that does not exist; the live paths then skip.
The managed vector backends, their collections, and the CI secrets that supply
them are documented in
[`../docs/testing/live-vector-backends.md`](../docs/testing/live-vector-backends.md).

## Reusable harnesses

- `internal/testharness/cmdharness` builds a Podium binary once per test process
  and runs it as a subprocess. It points `HOME` and the working directory at a
  per-test temporary directory so a developer's real `~/.podium` cannot leak into
  a test.
- `internal/testenv` loads `test.env` so a live suite picks up credentials
  without a per-test flag.
- `pkg/store/storetest` holds the `RegistryStore` conformance suite and a
  fault-injectable store decorator for the read-only-fallback tests.
- `internal/testharness/goldenfile`, `tempdir`, and `registryharness` provide
  golden-file diffing, temporary-directory helpers, and an in-process filesystem
  registry behind an `httptest.Server`.

The `e2e` package carries its own helpers for the scenarios that need an
authenticated server. `authserver_harness_test.go` boots a standalone registry
in injected-session-token mode with per-layer visibility, and
`injected_token_helpers_test.go` mints the RS256 runtime-signed tokens it
verifies. The `tools/minttoken` command mints the same tokens for the manual
scenarios.

## Spec traceability and the coverage gate

A test cites the spec section it verifies in a comment above the function, for
example `// Spec: §4.6`. A cell in a spec table is cited as
`// Matrix: §6.10 (auth.untrusted_runtime)`. The tools under `tools/` read these
citations and enforce them:

- `speccov` reports per-section coverage and fails on drift. `make speccov-drift`
  fails when a test cites a spec section that no longer exists, and
  `speccov report` and `speccov uncovered` show what is covered and what is not.
- `matrix` audits the `// Matrix:` annotations against the matrices in
  `tools/matrix/matrices.go`. `make matrix-audit` reports cells without a test,
  and `matrix scaffold` generates skeletons for them.
- `doccov` checks that every runnable documentation page maps to a covering test
  or a recorded waiver. `make doccov-check` enforces it.
- `coverage` enforces an overall line-coverage floor through
  `make coverage-budget`.

`make coverage-gate` runs these together. The per-PR lanes run them across
`test.yml` and `spec-coverage.yml`. `nightly.yml` adds the deeper live Postgres,
S3, and Dex lanes, and `live-external.yml` runs the managed-backend suite on a
release or a manual dispatch.
