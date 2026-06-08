# Test coverage

Project-wide rules for testing new and changed code in Podium. These rules apply to every change to Go code under `pkg/`, `internal/`, and `cmd/`, and to the SDKs under `sdks/` through their own test suites.

## Top-level principle

New and changed code carries tests. The new code reaches at least 85% line coverage, and a test exercises it at the highest level its behavior reaches, up to end-to-end.

## Coverage threshold

- New or changed code reaches at least 85% line coverage.
- Measure coverage across packages, because a package's code is often exercised by another package's tests. Use the cross-package profile that CI uses: `go test -coverpkg=./... -coverprofile=cover.out ./...`, or the subset of packages the change touches. A per-package run such as `go test ./pkg/foo/` reports only that package's own tests and undercounts code reached from another package. A server middleware exercised by a `serverboot` integration test is one example of code that a per-package run scores as uncovered.
- Inspect per-function coverage with `go tool cover -func=cover.out` and add tests for the functions below the threshold.
- A function below 85% needs a test for the uncovered branch, or a comment that names why the branch cannot run.

## Test level

- Unit tests cover pure functions, branch logic, and error mapping.
- Integration tests cover a component wired to its real collaborators in one process. A registry behind the meta-tool server, driven over HTTP with an in-memory store, is an integration test.
- End-to-end tests cover behavior that appears only when the compiled binary runs: the boot sequence, configuration validation, the command-line interface, and signal handling.
- Add a test at the highest level the change reaches. A change to request handling gets an integration test. A change to the boot path or the CLI gets an end-to-end test. A pure helper gets a unit test.

## Code that runs in a spawned process

- The end-to-end tests start the `podium` binary as a subprocess. A plain `go test` coverage profile records only the test process, so lines that run inside the spawned binary read as uncovered even when an end-to-end test drives them. The boot path, configuration validation, and the CLI fall in this category.
- Cover this code with an end-to-end test that drives the behavior through the binary and asserts the observable result: the exit code, the error envelope, or the response body.
- Measure the subprocess coverage when you need to confirm it. The command harness in `internal/testharness/cmdharness` builds the binary with `-cover` when `GOCOVERDIR` is set, so `GOCOVERDIR=$(mktemp -d) go test ./test/e2e/...` records the subprocess execution into that directory. Convert it with `go tool covdata textfmt -i=$GOCOVERDIR -o=e2e.txt` and read it with `go tool cover -func=e2e.txt`.
- Keep the end-to-end test even when the default `go test` profile does not move. Removing it to simplify a coverage report removes the only check on that code.

## Where these rules apply

- Go code under `pkg/`, `internal/`, and `cmd/`.
- The SDKs under `sdks/` meet the same bar through their own suites: pytest for `podium-py`, and the Node test runner for `podium-ts`.

## How to apply when editing

1. Identify the level the change reaches and add or extend a test there.
2. Run the cross-package coverage profile over the packages you changed and confirm the new lines reach 85%.
3. For a boot-path or CLI change, add or extend an end-to-end test under `test/e2e/` and assert the observable result.
4. For a branch that needs external infrastructure such as a live database or a real identity provider, cover it in the live or nightly suite and make the default run skip it cleanly.

## Escape hatches

- A branch that cannot run without external infrastructure may be covered by a live or nightly test instead of the default suite.
- Generated code and vendored code are exempt.
- An unreachable branch, such as a default arm on an exhaustive switch or a guard against a condition the type system already prevents, carries a comment that names why instead of a contrived test.

## Maintenance

When a new coverage gap pattern surfaces in review, add a specific, actionable rule above. Keep each rule actionable and specific.
