# Code best practices

Project-wide rules for the Go code in this repository. They apply to every change under `pkg/`, `cmd/`, and `internal/`, and to any agent or workflow that writes or modifies code here. They complement the linter rather than restate it. The SDKs under `sdks/` follow their own language conventions and are covered by their own tooling.

## Top-level principle

New code is modular, small, and reuses existing surfaces, and it reads like the code around it. Match the surrounding package's naming, error handling, and structure. Keep each change minimal and focused, and do not reformat or restructure code the change does not touch.

## What the linter already enforces

`make lint` runs `golangci-lint` with `errcheck`, `govet`, `staticcheck`, `unused`, `ineffassign`, `misspell`, `gofmt`, `goimports` (local prefix `github.com/lennylabs/podium`), and `nolintlint`. Do not hand-fight formatting or import grouping; run `gofmt` and `goimports`. Never ignore a returned error to satisfy the compiler. Those are machine-checked, and the rules below cover what the linter does not.

## Functions and files

- Keep functions small and single-purpose. Extract a helper when a function exceeds roughly 50 lines or mixes levels of abstraction, such as request parsing, business logic, and I/O in one body.
- One responsibility per function, one concern per file. Split a file that has grown to cover several concerns.
- Prefer early returns over deep nesting: guard clauses at the top, the main path unindented.

## Project structure and reuse

- Search for an existing package to reuse or extend before creating a new one. Cross-reference the §2.2 component map and the §9.1 SPI table so a concern lands in its canonical package.
- One package per concern, named for the concern or the spec component it implements. `pkg/` holds the library surface, one directory per concern (`adapter`, `identity`, `layer`, `manifest`, `materialize`, `registry`, `store`, and the rest). `pkg/spi` holds the pluggable interfaces from §9.1. There is deliberately no catch-all utility package: a shared helper goes with the concern it serves.
- Binaries under `cmd/` stay thin and delegate to `pkg/`. The three are `podium`, `podium-server`, and `podium-mcp`. Do not put reusable logic in a `cmd/` main.
- `internal/` holds what must not be imported outside this module: the boot sequence (`serverboot`), build metadata (`buildinfo`), the injectable clock, and the test harnesses. Reach for it when a surface would otherwise become part of the module's public API by accident.
- Reuse over duplication: extract a shared helper rather than copy a block. Two near-identical blocks are a refactor, not a pattern.
- Prefer the standard library and the dependencies already in `go.mod`. A new third-party dependency is a supply-chain and maintenance surface: justify it, and extend an SDK the module already imports rather than adding a second for the same service.

## Errors

- Wrap errors with context and `%w`, prefixed with the operation, in the style the packages already use: `fmt.Errorf("webhook: read %s: %w", path, err)`. The chain stays inspectable with `errors.Is` and `errors.As`.
- Define a sentinel or typed error when callers branch on the failure (`var ErrUnknownProvider = errors.New(...)`), and return that rather than a string a caller has to match.
- A failure that reaches a client carries its §6.10 error code. A new code needs its spec entry and its `// Matrix:` annotated test, per [`spec-driven-development.md`](spec-driven-development.md).
- Do not `panic` in library code; return an error. `panic` is for genuinely unreachable invariants only.
- Fail closed on security-relevant paths. A visibility filter that cannot evaluate, an identity that cannot be verified, a signature that cannot be checked, and a JWKS that cannot be fetched all deny rather than admit.

## Interfaces, dependencies, and testability

- Define small interfaces at the consumer rather than the producer. Accept interfaces, return concrete types. The §9.1 SPIs are the exception by design: they are producer-side contracts, and §9.3 constrains every method to be context-aware, wire-serializable, and free of shared in-process state.
- Inject dependencies (clocks, stores, clients, randomness) so a unit test can substitute them. `internal/clock` exists for exactly this; do not call `time.Now()` directly in logic a test needs to control. Avoid global mutable singletons for anything that does I/O or carries state.
- Take `context.Context` as the first parameter on any function that does I/O, blocks, or spawns work, and propagate it. Honor cancellation and deadlines, and do not bury `context.Background()` deep in a call chain.

## Concurrency and resource cleanup

- Guard shared state with the right primitive, and document the invariant the lock protects.
- Code that runs concurrently must be `-race` clean, and the test that exercises it says so.
- Do not leak goroutines: every goroutine has a clear exit tied to a context or a closed channel.
- Close what you open. `defer` the close of rows, response bodies, files, and connections.
- Put a timeout or a deadline on every outbound call, and bound every retry with jittered backoff. The outbound surfaces are the Git fetch, the identity provider and its JWKS, object storage, the embedding provider, and the outbound webhook worker; an unbounded call on any of them stalls the path on one hung dependency.

## Logging and secrets

- Log through the standard library `log` package, which is what the server and the CLI use (`log.Printf`). Do not introduce a second logging library for consistency's sake alone; if structured logging becomes necessary, it is a change to make deliberately and everywhere rather than package by package.
- Log identifiers and outcomes, never secret values: no tokens, proxy secrets, API keys, signing keys, or the contents of a keychain entry. Query text is scrubbed for PII before it reaches the audit log (§8), and the same care applies to anything written to a log line.

## Naming, comments, and spec ties

- Use idiomatic Go names and avoid stutter (`layer.Composer`, not `layer.LayerComposer`). Document every exported identifier.
- Comments explain why, not what. Delete commented-out code. Include the context a later reader, human or agent, would otherwise have to reconstruct: the constraint that forced a design, the failure a guard prevents, the reason an obvious simplification does not work.
- Cite the spec on spec-derived logic with `// Spec: §X.Y`, by section number and never by line number. A reviewer should be able to trace any behavior to its section.

## Configuration and compatibility

- A default the spec does not fix must be overridable by a flag, a `PODIUM_*` environment variable, or a `registry.yaml` key, and documented as operator-tunable (§13.12). Do not hard-code a non-spec constant with no override.
- Podium is pre-1.0. A backward-incompatible change is acceptable and lands in a MINOR bump, so do not add compatibility shims, dual code paths, legacy flags, or migration paths for external compatibility. Change the interface and update every caller.
- A change must not make the deployment modes diverge. The shared Go library is the single behavioral surface (§2.2), so local, single-node, and clustered deployments materialize identical output, and the §11 equivalence test pins it.

## Where these rules apply

- All Go under `pkg/`, `cmd/`, and `internal/`.
- Inline code comments that constitute prose follow [`doc-style.md`](doc-style.md).
- Tests additionally follow [`test-coverage.md`](test-coverage.md).

## How to apply when editing

1. Read the target package first and match its idioms, error style, and structure.
2. Before adding a package or a helper, search for an existing one to extend.
3. Keep functions small and dependencies injected, and add the `// Spec:` citation on spec-derived logic.
4. Run `gofmt`, `goimports`, and `make lint`, and resolve every finding rather than suppressing it.
5. Keep the diff scoped to the change, and revert incidental reformatting.

## Escape hatches

- Golden files under `test/materialization/testdata/golden/` are regenerated with `UPDATE_GOLDEN=1 go test ./test/materialization/` rather than hand-edited, and the resulting diff is confirmed to be exactly the intended change.
- Vendored or third-party code keeps its upstream style.
- A `nolint` directive is permitted only with an inline reason, and `nolintlint` enforces that. It is the exception, not a routine tool.

## Maintenance

When a review surfaces a recurring code defect this file does not cover, add a specific, actionable rule. Do not restate what the linter already enforces; keep this file to what review catches and tooling does not.
