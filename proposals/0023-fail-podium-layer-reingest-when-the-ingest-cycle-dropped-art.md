# Proposal 0023: Fail `podium layer reingest` when the ingest cycle dropped artifacts

- Issue: (to be filed)
- Status: Implemented (2026-09-08). Verified after 7 adversarial review rounds (12 findings fixed).
- Date: 2026-09-08

This document stages the proposed spec, code, test, and documentation changes. It does not modify any spec, code, or doc file. Apply the changes in the staged sections after sign-off.

## Summary

**What changes.**

- §7.3.1 gains one paragraph fixing the ingest outcome classification and the exit status of `podium layer reingest`. The section owns the manual reingest command, its CLI synopsis, the ingest cases table, and the errors paragraph, and it states nothing today about what the command reports or returns.
- `cmd/podium/layer.go` gains one unexported report printer that takes injected `io.Writer` streams, decodes the reingest response, prints the accepted and unchanged artifacts and the advisories on one stream and each conflicted artifact, each rejected artifact, the lint diagnostic count, and each embedding failure on the other, and returns the drop count together with whether the body was a cycle report at all.
- `layerReingest` (`cmd/podium/layer.go:386-491`) drops the `len(parsed.Artifacts) > 0` gate at `:459` and returns 1 when the report reports a drop. Today it returns 0 on every response below HTTP 400 (`:487`, `:489-490`), so a mixed snapshot and a snapshot whose every artifact was rejected both report success.
- A unit table in `cmd/podium/layer_reingest_test.go` pins the classification and the rendered stderr lines, including the rejected-only body the shipped gate skips, the mixed-conflict body the handler answers 200, the runner-less acknowledgement, and a non-JSON body. A second unit test drives `layerReingest` against a stub reingest runner and pins the exit status.
- An end-to-end test in `test/e2e/cli_reference_test.go` drives a public-mode standalone registry whose §13.10 sensitivity floor rejects an artifact per cycle, and asserts the exit status through the built binary on a mixed, a rejected-only, and an empty-layer arm.
- Three shipped end-to-end assertions that pass today through the raw-body fallback are restated: `test/e2e/cli_reference_test.go:1497-1499`, `TestStandardDeploy_LayerReingestLocal` (`test/e2e/standard_deployment_test.go:519-536`), and `TestServerOps_LayerReingest` (`test/e2e/server_operations_test.go:800-821`).
- `docs/reference/cli.md` states the exit status and the output contract for the command, and `CHANGELOG.md` records the behavior change.
- `test/manual-validation.md` gains the S62 scenario and its index row, and S42's step 5 loses the note stating that a mixed snapshot exits 0.

**Fixed decisions.**

- The exit status is the signal. No `--json` mode, no new §6.10 error code, and no new response field are added.
- The HTTP status is unchanged: 409 for a pure-conflict snapshot, 200 for every mixed and rejected-only snapshot. The same handler serves the inbound webhook trigger, and a Git host retries a non-2xx delivery.
- The exit codes are the ones the binary already uses: 2 for a usage error, 1 for a failed request or a cycle that dropped an artifact, 0 otherwise.
- A non-blocking advisory (§3.3) and an artifact whose embedding call failed (§4.7) are not drops. Both are reported and neither changes the exit status.
- A lint failure is a drop, and the command reports it as the diagnostic count the response carries rather than per artifact.
- The printer writes the per-artifact lines alone. No summary line is added, because §7.3.1 as amended fixes the per-artifact lines and a new always-on line is unpinned output on a shipped command's success path.
- The 409 and 422 envelopes keep what they carry today. Itemising `res.Rejected` on either is a registry change with its own tests, and it lands as its own proposal.
- `podium layer watch` is not changed. It prints the raw response body per tick as it does today, and it carries no exit status.
- The `layer.ingested` event payload is not changed.
- Podium is pre-1.0. No flag, environment variable, or `registry.yaml` key restores the exit-0-on-drop behavior.

**Watch out for.**

- **Shipped end-to-end assertions break the moment the code lands, and they all pass today through the raw-body fallback.** `test/e2e/cli_reference_test.go:1497-1499` registers an empty `t.TempDir()` layer and asserts that stdout contains `queued`. It passes today only because `artifacts` is empty and the command falls back to printing the raw body. `test/e2e/standard_deployment_test.go:519-536` (`TestStandardDeploy_LayerReingestLocal`) and `test/e2e/server_operations_test.go:800-821` (`TestServerOps_LayerReingest`) each boot a standalone server with `--layer-path <reg>` (`test/e2e/helpers_test.go:431-437`) and then register that same directory again as a second layer. The bootstrap layer's id is the directory basename (`pkg/registry/filesystem/registry.go:157-168`), the artifact id derives from the domain directory alone, and the second layer's every artifact is therefore rejected with `ingest.collision` (`pkg/registry/ingest/ingest.go:736-753`), so `artifacts` is empty and the raw body is printed with exit 0. Under the change each of those bodies decodes as a cycle report, nothing prints on stdout, and the drop count returns 1. All three assertions are restated in the same commit as the code, which is why S2 carries them.
- **The nested response fields need explicit JSON tags.** `encoding/json` matches an untagged field by exact or case-insensitive name, so `artifact_id` does not match `ArtifactID`. A tagless struct decodes the slice lengths correctly and every identifier and reason as an empty string, which leaves the exit status right and the stderr lines rendered as `rejected:  (): `. The shipped decode carries the tags (`cmd/podium/layer.go:445-457`); keep them, and assert the rendered text rather than the counts alone.
- **`queued` does not discriminate a cycle report from the runner-less acknowledgement.** Both bodies carry `queued` and `queued_at` (`pkg/registry/server/layers.go:1652-1656`, `:1751-1752`). The presence of `accepted` is the only key that separates them, which is the discrimination the web UI already makes (`web/ui/src/surfaces/ReingestControl.tsx:314`).
- **The runner-less arm is unreachable end to end.** `internal/serverboot/serverboot.go:1496` always wires a reingest runner, so no booted registry produces `{queued, queued_at}` alone. The unit table is the only place that arm is exercised.
- **`lint_failures` counts diagnostics rather than artifacts.** `res.LintFailures` accumulates every diagnostic for each rejected artifact (`pkg/registry/ingest/ingest.go:572-574`) and the handler emits its length (`pkg/registry/server/layers.go:1758`). One artifact with two diagnostics reports 2. Do not assert an artifact count against it, and do not document one.
- **The lint-only snapshot already exits 1.** `pkg/registry/ingest/ingest.go:918-921` returns `ErrLintFailed` and the handler answers 422 (`pkg/registry/server/layers.go:1774-1775`), which the `status >= 400` arm already catches. Only the mixed lint snapshot changes.
- **The pure-conflict snapshot already exits 1** through the same transport arm, because the handler answers 409 (`pkg/registry/server/layers.go:1717`). The new drop check adds no second signal there. That guard fires on any snapshot with a conflict and nothing accepted or unchanged, including one that also rejected artifacts for other reasons.
- **The two envelope arms report less than the 200 body does.** Rows 2 and 3 of the drop table in Current-state state what each arm carries and what it loses. Both already exit 1 and continue to, so this proposal changes neither, and the limit is stated once in that table.
- **The suite passes with and without the fix today.** `grep -rn "Rejected:" pkg/registry/server/*_test.go` returns nothing, so no test constructs an `ingest.Result` carrying rejections and the 200-on-drops path is unpinned. A green run before the new tests land is evidence for neither direction.
- **The whole `cmd/podium` test package runs serially.** `captureStdout` and `captureStderr` swap the process-wide `os.Stdout` and `os.Stderr` (`cmd/podium/config_test.go:15`, `cmd/podium/admin_migrate_test.go:16-28`), and `cmd/podium/no_parallel_guard_test.go:11-26` fails on a `t.Parallel()` anywhere in the package. The direct `layerReingest` call runs inside the capture helpers and adds no `t.Parallel()`.
- **The printer has one caller.** `podium layer watch` is out of scope, so the extraction is not justified by reuse. It is justified by the unit table, which drives the classification through injected writers without extending the process-global swap to new code.
- **Parts of `test/e2e` skip silently on macOS.** Check the output for `SKIP` before treating a local run as evidence for the new arm.

## Implementation checklist

- [x] **S1 · spec** — SPEC-1. §7.3.1 gains the ingest outcome paragraph fixing the classification and the reingest exit status. Committed alone and verified before any code.
      Levels: —. Depends on: —
- [x] **S2 · code** — CODE-1, CODE-2. The report printer, the removed printing gate, the exit status, and the restated assertions at `test/e2e/cli_reference_test.go:1497-1499`, `test/e2e/standard_deployment_test.go:519-536`, and `test/e2e/server_operations_test.go:800-821`. **Indivisible**: the printer has one caller, and the shipped end-to-end assertions go red the moment the gate is removed.
      Levels: e2e. Depends on: S1
- [x] **S3 · test** — TEST-1. The classification table over the printer and the exit-status test over `layerReingest`.
      Levels: unit. Depends on: S2
- [x] **S4 · test** — TEST-2. The end-to-end arms through the built binary on a public-mode standalone registry.
      Levels: e2e. Depends on: S2
- [x] **S5 · docs** — DOC-1, DOC-2, DOC-3. `docs/reference/cli.md`, `test/manual-validation.md` (the S62 scenario, its Scenario index row, and the replaced exit-status note in S42's step 5), and `CHANGELOG.md`.
      Levels: —. Depends on: S3, S4

**Ordering constraints.** S1 precedes the code, per `.claude/rules/spec-driven-development.md`, because the printer's doc comment cites the paragraph it implements. S2 carries the three restated end-to-end assertions rather than deferring them to S4, so no step leaves the suite red. S3 and S4 are independent of each other and both depend on S2, because both assert the new exit status and would land red before it. S5 follows the tests so the page describes what the tested build does. `docs/reference/cli.md` maps to the `D-cli` slug (`tools/doccov/manifest.yaml:29-30`) and the staged edit adds no fenced block, so no `doccov-check` obligation is created.

## Current state and the gap

### The command reports success on a cycle that lost work

After the request is made, `layerReingest` returns non-zero only when the HTTP status is at or above 400 (`cmd/podium/layer.go:427-431`). Both structured tails return 0 (`:487`, `:489-490`). The pre-request usage errors return 2 (`:400`, `:404`, `:409`, `:413`, `:419`, and `parseExit` at `cmd/podium/main.go:129-134`) and are unaffected.

`ingest.Ingest` accumulates the classification on one `*Result` allocated at `pkg/registry/ingest/ingest.go:517`. No site clears a slot: `:525` assigns the `Advisories` slot its initial contents and every other site appends. Every artifact lands in exactly one of the mutually exclusive outcome slots `Accepted`, `Idempotent`, `LintFailures`, `Conflicts`, and `Rejected`, because each lint, conflict, and rejection site continues to the next record; `Advisories` and `EmbeddingFailures` are recorded beside those outcomes rather than instead of them, so an artifact accepted at `:822` can also carry an embedding failure appended at `:910` in the same iteration. The endpoint selects one of three response arms from the slot counts together with the returned error, beneath a fourth arm that answers a runner-less registry before any cycle runs. The table below is this proposal's single statement of the mapping from cycle state to response arm; it covers the states a cycle reaches, and the runner-less acknowledgement, which runs no cycle, is stated in the Edge cases row for a registry with no ingest runner wired and in CODE-2. The Edge cases rows and the Watch-out-for entries state what they own and refer to the table rather than restating it. SPEC-1, DOC-1, and DOC-3 are staged text for `spec/07-external-integration.md`, `docs/reference/cli.md`, and `CHANGELOG.md`, so each states its own scope without referring to a table internal to this proposal.

The slots that are not drops are `Accepted` (`:822-823`), `Idempotent` (`:763-764`), `Advisories` (`:525`, which assigns the §3.3 description-quality advisories the rest of the slot is appended to, `:530` for a newly unlisted domain, and `:677` for a cross-layer license change), and `EmbeddingFailures` (`:540` for a domain vector and `:910` for an artifact vector). The slots that are drops are `LintFailures`, which has one site (`:573`) and appends every diagnostic raised against an artifact; `Conflicts`, which has two (`:767` for a stored hash that differs and `:813` for `store.ErrImmutableViolation` on commit); and `Rejected`, which has twelve carrying eight codes: `:582` and `:726` `ingest.public_mode_rejects_sensitive`, both through `sensitivityRejection` (`:1633-1642`), which the loop takes twice for an `extends` child; `:593` `ingest.sandbox_profile_unenforceable`; `:604`, `:653`, `:663`, and `:708` `ingest.invalid_artifact`; `:617` `quota.storage_exceeded`; `:634` `quota.artifact_count_exceeded`; `:747` `ingest.collision`; `:788` `ingest.sign_failed`; and `:803` `ingest.resource_store_failed`. Five of those code strings appear nowhere under `spec/`, which is pre-existing and outside this proposal. SPEC-1 therefore names drop classes rather than codes and creates no §6.10 matrix obligation.

The cycle returns `nil, err` for a whole-cycle failure at `:415`, `:418`, `:433` (an active freeze window), `:477`, `:510`, `:701`, `:777`, `:820`, and `:897`, and at `pkg/registry/ingest/orchestrator.go:116` and `:122`. It returns a result beside an error at one site inside `Ingest`, `:920`, guarded at `:919` by `len(res.LintFailures) > 0 && res.Accepted == 0 && res.Idempotent == 0 && len(res.Conflicts) == 0`; `res.Rejected` is absent from that guard. `SourceIngestWithOptions` also returns the result beside an error when the post-commit `PutLayerConfig` fails (`orchestrator.go:200`), and the runner passes the pair through unchanged (`internal/serverboot/reingest.go:84-86`).

`runIngestAndRespond` (`pkg/registry/server/layers.go:1650`) has four response arms. Arm 0 fires when `e.reingestRunner == nil`, writes an early 200 `{queued, queued_at}` acknowledgement (`:1651-1657`), and returns before the runner is called at `:1658`, so it runs no cycle; CODE-1 discriminates it from a cycle report by the absence of the `accepted` key. Arm A fires on a non-nil error (`:1659`), calls `notifyIngestFailure` and `writeReingestError` (`:1768-1789`), and reads no field of the result. Arm B fires on `len(res.Conflicts) > 0 && res.Accepted == 0 && res.Idempotent == 0` (`:1717`), writes 409 `ingest.immutable_violation` with `details.conflicts`, and returns above the `rejected` array built at `:1733`. Arm C writes the 200 body (`:1751-1763`).

| # | Cycle state | Ingest return | Arm | What the response carries | The command today |
|:--|:--|:--|:--|:--|:--|
| 1 | A pre-loop guard, an active freeze window, or a source, store, or provider failure | `nil, err`, or `res, err` at `orchestrator.go:200` after a committed cycle | A | The mapped §6.10 envelope: `code`, `message`, `retryable`, and the per-code `suggested_action`, with no `details` because `writeError` passes nil details (`pkg/registry/server/server.go:1446-1447`, `:1454-1457`, `pkg/registry/server/error_envelope.go:112-123`), and no field of the ingest result | Exit 1 |
| 2 | Lint failures, nothing accepted, nothing unchanged, and no conflicts. `Rejected` may be non-empty | `res, ErrLintFailed` (`ingest.go:920`), replaced by `nil` at `orchestrator.go:164-166` | A | 422 `ingest.lint_failed`, with the diagnostic count in the envelope's `message` string. The rejections are reported nowhere | Exit 1 |
| 3 | Conflicts, nothing accepted, and nothing unchanged. `LintFailures` and `Rejected` may be non-empty | `res, nil`, because `:919` requires no conflicts | B | 409 `ingest.immutable_violation` with `details.conflicts`. Neither the lint diagnostic count nor the rejections are reported | Exit 1 |
| 4 | Rejections alone, with nothing accepted, nothing unchanged, no conflicts, and no lint failures | `res, nil` | C | 200 with `rejected` populated and `artifacts` empty | The raw body on standard output, exit 0 |
| 5 | Anything accepted or unchanged beside any drop | `res, nil` | C | 200 with the full body | The labelled lines, exit 0 |
| 6 | A clean cycle carrying at least one artifact | `res, nil` | C | 200 | The labelled lines, exit 0 |
| 7 | A clean cycle over a layer with no artifacts | `res, nil` | C | 200 with `artifacts` empty | The raw body on standard output, exit 0, because the structured branch is gated on `len(parsed.Artifacts) > 0` (`cmd/podium/layer.go:459`, `:489`) |

Rows 4 and 5 are what this proposal changes to exit 1. Rows 1, 2, and 3 already exit 1 through the transport arm at `cmd/podium/layer.go:427-430`, which prints the envelope on standard error, and they are unchanged. Rows 2 and 3 are the reporting limit: on both, the artifacts the cycle recorded in `res.Rejected` are named on no stream, and on row 3 the lint diagnostic count is lost as well. On row 2 the pipeline does produce the result beside `ErrLintFailed` at `ingest.go:920`; `SourceIngestWithOptions` replaces it with `nil` at `orchestrator.go:164-166` before the handler sees it. SPEC-1 states the drop set as an open list because the governing sentence is that the cycle rejected the artifact rather than storing it, and the named classes illustrate it. SPEC-1's "A completed ingest cycle" wording keeps out of the per-artifact naming requirement the row 1 states that end the cycle before it classifies anything: a pre-loop guard, a freeze window, and a source, store, or provider failure. It does not reach rows 2 and 3, nor row 1's post-commit `PutLayerConfig` failure at `orchestrator.go:200`: each of those completes a cycle, row 3 returning `res, nil` because `:919` requires no conflicts. What displaces the per-artifact naming requirement on those is the other sentence SPEC-1 states, that a cycle the registry answers with a §6.10 error reports that error envelope.

Two cases follow. A mixed snapshot answers 200, the command prints its `conflict:` and `rejected:` lines to stderr, and it returns 0. A CI pipeline gating on the exit status accepts that reingest.

### A snapshot that accepted nothing prints no labelled reason

The structured branch is gated on `len(parsed.Artifacts) > 0` (`cmd/podium/layer.go:459`), and `artifacts` is built from `res.Ingested`, which holds accepted and unchanged pairs alone (`pkg/registry/server/layers.go:1672-1679`, `pkg/registry/ingest/ingest.go:346-350`). A snapshot whose every artifact was rejected therefore skips the branch and prints the raw JSON body to stdout with exit 0. The body is server-side pretty-printed (`pkg/registry/server/server.go:1438-1444`) and does carry the `rejected` array with its per-artifact `artifact_id`, `code`, and `reason` (`pkg/registry/server/layers.go:1733-1740`), so a human at the terminal can read it. What is lost is the exit status, the labelled line, and the stream.

### The wire format already carries the classification

The 200 body carries `accepted`, `idempotent`, `conflicts`, `lint_failures`, `rejected`, `embedding_failures`, `artifacts`, and `advisories` (`pkg/registry/server/layers.go:1751-1762`). The web UI reads it that way and reports the drop counts (`web/ui/src/surfaces/ReingestControl.tsx:304-356`). The CLI's reading of the same response is the defect.

### No spec text fixes the exit status in either direction

`spec/` fixes an exit status for `podium login` (§7.7) and states one fail-fast rule for the publish pipeline (§7.8). §7.6.1 is the Read CLI section and maps each command to an SDK read call, so it does not reach a layer write. §7.3.1 covers the reingest command's synopsis, its authorization, its freeze windows, and its errors, and says nothing about its exit status. `docs/reference/cli.md:471-479` documents its flags and its freeze-window behavior alone. Under `.claude/rules/spec-driven-development.md` the behavior has to trace to a section before the code changes, which is what SPEC-1 provides; §7.3.1 already carries test citations, so the paragraph creates no new mechanical obligation on `speccov-drift`.

## Spec amendment: §7.3.1 the ingest outcome and the reingest exit status

**SPEC-1.** Anchor: `spec/07-external-integration.md`, §7.3.1. One paragraph is appended at the end of the section, after the paragraph beginning "**Errors.**" and before the `### 7.3.2` heading. It is appended at the end of its level and inserted between no existing siblings.

> **Ingest outcome.** A completed ingest cycle classifies each artifact in the snapshot as accepted, unchanged, or dropped. An artifact is dropped when the cycle rejects it rather than storing it. The rejections include a same-version content change and a lint failure (the ingest cases above), a quota (the errors rule above), the public-mode sensitivity floor and an unenforceable sandbox profile (§13.10), a cross-layer collision and an `extends:` chain that crosses types (§4.6), an unresolved `extends` pin, a manifest record the cycle cannot build, a signing failure, and a resource-store failure. A non-blocking advisory (§3.3) and an artifact whose embedding call failed (§4.7) are not drops: the artifact is stored and served. `podium layer reingest <id>` exits non-zero when the cycle dropped at least one artifact. On a completed cycle it names each conflicted and each rejected artifact on standard error with that artifact's identifier, its §6.10 code, and its reason, and it reports a lint drop as the number of lint diagnostics the cycle raised; `podium lint` against the source names the artifacts those diagnostics came from. A cycle the registry answers with a §6.10 error reports that error envelope instead and exits non-zero. `podium layer watch <id>` runs until it is interrupted and carries no exit status of its own.

§7.3.1 is the section that owns the manual reingest command, its synopsis, the ingest cases table, and the errors paragraph, so the outcome classification and the command's exit status belong beside them. §7.6.1 is not amended: it is the Read CLI section and maps each command to an SDK read call, so it does not reach a layer write. The numeric exit codes are left to `docs/reference/cli.md`, which is where the sibling commands' numeric contracts already live (`docs/reference/cli.md:184` for `podium lint`, `docs/consuming/publishing.md:169` for the publish pipeline); the spec's one precedent is a single clause for `podium login` (§7.7). The paragraph fixes no response body and no HTTP status, both of which this proposal leaves as they are.

This adds no §6.10 error code and no matrix cell. §7.3.1 keeps its existing test citations, and TEST-1 and TEST-2 cite the section as well.

## Proposed solution

### CODE-1: one ingest-report printer with injected streams

A helper lands beside `layerReingest` in `cmd/podium/layer.go`. It decodes the reingest response, prints the report, and returns the number of dropped artifacts together with whether the body was a cycle report at all.

```go
type ingestConflict struct {
	ArtifactID string `json:"artifact_id"`
	Version    string `json:"version"`
	Code       string `json:"code"`
}

type ingestRejection struct {
	ArtifactID string `json:"artifact_id"`
	Code       string `json:"code"`
	Reason     string `json:"reason"`
}

type ingestAdvisory struct {
	ArtifactID string `json:"artifact_id"`
	Code       string `json:"code"`
	Severity   string `json:"severity"`
	Message    string `json:"message"`
}

type ingestEmbeddingFailure struct {
	ArtifactID string `json:"artifact_id"`
	Version    string `json:"version"`
	Reason     string `json:"reason"`
}

type ingestReport struct {
	// Accepted discriminates a completed cycle from the acknowledgement a
	// registry with no ingest runner returns. Both bodies carry `queued` and
	// `queued_at`, so the presence of `accepted` is the only key that separates
	// them.
	Accepted *int   `json:"accepted"`
	Layer    string `json:"layer"`
	LintFailures int `json:"lint_failures"`
	Artifacts []struct {
		ID      string `json:"id"`
		Version string `json:"version"`
	} `json:"artifacts"`
	Advisories        []ingestAdvisory         `json:"advisories"`
	Conflicts         []ingestConflict         `json:"conflicts"`
	Rejected          []ingestRejection        `json:"rejected"`
	EmbeddingFailures []ingestEmbeddingFailure `json:"embedding_failures"`
}
```

The printer signature is `writeIngestReport(stdout, stderr io.Writer, layerID string, body []byte) (dropped int, ok bool)`. It reports `ok == false` when the body does not decode or carries no `accepted` key, and in that case it writes nothing to either stream, so the caller's fallback print is the only output on that arm. When it reports `ok == true` it writes, in order:

- one `artifact: <id>@<version>   layer: <layer>` line per entry in `artifacts`, on `stdout`, which is the line §0 already pins (`spec/00-quickstart.md:47-48`) and which covers accepted and unchanged pairs alike;
- one `advisory: <id> [<severity>] <message> (<code>)` line per advisory, on `stdout`;
- one `conflict: <id>@<version> rejected (<code>); bump the version` line per conflict, on `stderr`;
- one `rejected: <id> (<code>): <reason>` line per rejection, on `stderr`;
- one `lint failures: <n>` line when `lint_failures` is non-zero, on `stderr`;
- and one `embedding failure: <id>@<version>: <reason>` line per embedding failure, on `stderr`.

`dropped` is `len(Conflicts) + len(Rejected) + LintFailures`. It is a gate rather than an artifact count, because `lint_failures` counts diagnostics: `res.LintFailures` accumulates every diagnostic for each rejected artifact (`pkg/registry/ingest/ingest.go:572-574`) and the handler emits its length (`pkg/registry/server/layers.go:1758`). The doc comment says so, and no caller reads the sum as a number of artifacts.

No summary line is printed. §7.3.1 as amended fixes the per-artifact lines and nothing else, and a new always-on line on the clean path is unpinned output on the success path of a shipped command. Dropping it also removes the only reason to decode `idempotent` and the artifacts' `status` field, so neither is in the struct. `Layer` stays, because the `artifact:` line already uses it and falls back to the requested layer id when the response omits it.

**Testability.** `captureStderr` exists in this package (`cmd/podium/admin_migrate_test.go:28`) and is used (`:430`). The argument for injected writers is not that stderr cannot be captured: it is that the helper swaps the process-wide `os.Stderr` because the commands write straight to the variable rather than to an injected writer (`admin_migrate_test.go:16-27`), and that swap is what forces the whole package to run serially (`cmd/podium/no_parallel_guard_test.go:11-25`). Injected writers keep the new classification out of that constraint.

**One caller.** With `podium layer watch` out of scope the extraction is not justified by reuse. It is justified by TEST-1's table, which drives every classification arm directly. An implementor who prefers the inline form must still make the classification unit-testable without adding to the process-global swap.

### CODE-2: the exit status

In `layerReingest` (`cmd/podium/layer.go:427-491`), the `len(parsed.Artifacts) > 0` gate at `:459` is removed and the tail becomes:

```go
	dropped, ok := writeIngestReport(os.Stdout, os.Stderr, fs.Arg(0), out)
	if !ok {
		fmt.Println(string(out))
		return 0
	}
	if dropped > 0 {
		return 1
	}
	return 0
```

The `!ok` arm covers a body that is not a cycle report, which today is the runner-less acknowledgement `{queued, queued_at}` (`pkg/registry/server/layers.go:1651-1657`) and a body that fails to decode. No end-to-end test reaches it, because `internal/serverboot/serverboot.go:1496` always wires a runner; `cmd/podium/layer_reingest_test.go:56-80` wires a stub runner and so stays green through the `ok == true` path with `dropped == 0`. The precondition on CODE-1 is that the printer writes nothing on that arm, so the acknowledgement is not printed twice.

The transport arm at `:427-430` is unchanged. A pure-conflict snapshot is answered 409 and already exits 1 there, and a lint-only snapshot is answered 422 and already exits 1 there, so the new check adds no second signal for either.

**Blast radius in the shipped suite.** Three shipped end-to-end assertions reach the raw-body fallback and are restated in the same commit.

- `test/e2e/cli_reference_test.go:1497-1499` reingests an empty local layer and asserts that stdout contains `queued`. Under the change that body decodes as a cycle report, the raw body is no longer printed, and nothing is written at all. The restated arm asserts exit 0 and the absence of a `rejected:` or `conflict:` line, which is what TEST-2's empty-layer arm also asserts.
- `TestStandardDeploy_LayerReingestLocal` (`test/e2e/standard_deployment_test.go:519-536`) boots `startServer(t, reg)`, which passes `--layer-path reg` (`test/e2e/helpers_test.go:431-437`), and then registers `reg` again as `local-test`. The bootstrap layer takes the directory basename as its id (`pkg/registry/filesystem/registry.go:157-168`) and the artifact id `hello` derives from the domain directory alone, so `local-test`'s reingest rejects it with `ingest.collision` (`pkg/registry/ingest/ingest.go:736-753`) and the command prints the raw body with exit 0, which is the only reason the `orgDecodeBody` stdout-is-JSON assertion holds today. Under the change the exit guard and the JSON assertion both fail. The restatement boots `startServer(t, "")` so the registered layer is the registry's only source, keeps the exit-0 guard, and asserts the `artifact: hello@1.0.0   layer: local-test` line on stdout in place of decoding stdout as JSON.
- `TestServerOps_LayerReingest` (`test/e2e/server_operations_test.go:800-821`) has the same structure with the artifact id `ctx`, and its exit-0 guard fails for the same reason. The restatement boots `startServer(t, "")` and keeps the exit-0 guard.

Booting each of those servers without `--layer-path` is what keeps the exit-0 assertion meaning what it says, because the tests exist to assert that a reingest of a registered local layer succeeds rather than to pin the cross-layer collision rule.

`test/e2e/cli_reference_test.go:1561` polls the watch log for `queued` and is unaffected, because `podium layer watch` still prints the raw response body per tick (`cmd/podium/layer.go:186-194`).

## Edge cases and accepted failure modes

| Case | Observable outcome | Where it is stated |
|:--|:--|:--|
| Mixed snapshot: one artifact accepted, one rejected by the §13.10 floor | 200; the `artifact:` line on stdout, the `rejected:` line on stderr, exit 1. Exit 0 today | SPEC-1; CODE-2; TEST-2 |
| Rejected-only snapshot, `artifacts` empty | 200; the `rejected:` line on stderr, no raw body on stdout, exit 1. Raw body on stdout and exit 0 today | SPEC-1; CODE-1; CODE-2; TEST-1; TEST-2 |
| Mixed conflict snapshot: one artifact accepted or unchanged, one rejected by the same-version content rule | 200, because the 409 guard requires nothing accepted and nothing unchanged; the `artifact:` line on stdout, the `conflict:` line on stderr, exit 1. Exit 0 today | SPEC-1; CODE-2; TEST-1 |
| Conflicts with nothing accepted and nothing unchanged, including a snapshot that also rejected artifacts for other reasons or raised lint diagnostics | 409 and exit 1, unchanged, through the transport arm. The envelope names the conflicts and reports neither the rejections nor the lint diagnostic count | Drop table, row 3; `cmd/podium/layer.go:427-430`; SPEC-1; Non-goals |
| Snapshot rejected entirely by lint, including one that also rejected artifacts for other reasons | 422 `ingest.lint_failed` and exit 1, unchanged, through the transport arm. The envelope carries the diagnostic count in its message and does not itemise the rejections | Drop table, row 2; SPEC-1; DOC-1; Non-goals |
| Mixed lint snapshot: one artifact accepted, one lint-rejected | 200; `lint failures: <n>` on stderr and exit 1. Exit 0 today. The line reports diagnostics rather than artifacts, and names no artifact | SPEC-1; OQ-1; DOC-1 |
| A non-blocking advisory on an accepted artifact | Reported on stdout, exit 0. The artifact is stored and served | SPEC-1; TEST-1; `test/e2e/description_quality_advisory_test.go:49` |
| An artifact whose embedding call failed | Reported on stderr, exit 0. The artifact is stored and searchable by BM25 until `podium admin reembed` | SPEC-1; OQ-3; TEST-1 |
| Clean cycle, every artifact unchanged | The `artifact:` lines on stdout, exit 0 | SPEC-1; TEST-2 |
| Reingest of a layer with no artifacts | Nothing printed, exit 0. The raw body carrying `queued` is printed today | CODE-2; TEST-2 |
| A registry with no ingest runner wired | The raw body on stdout and exit 0, unchanged. Unreachable from a booted registry | CODE-2; TEST-1 |
| A response body that does not decode | The raw body on stdout and exit 0, unchanged | CODE-2; TEST-1 |
| Usage error, missing registry, `--justification` without `--break-glass` | Exit 2, unchanged | `cmd/podium/layer.go:400-419`; DOC-1 |
| `podium layer watch` on a cycle that dropped artifacts | The raw response body per tick, and no exit status. Unchanged, and the drop classes are already itemised in that body | Non-goals |
| The `layer.ingested` event on a cycle that dropped artifacts | Carries `accepted`, `idempotent`, `conflicts`, `lint_failures`, and `embed_failures`, and no rejected count. Unchanged | Non-goals |
| The §13.8 ingest metric on a rejected-only cycle | Counted as a success, because it derives from the returned error alone (`internal/serverboot/reingest.go:93-101`). Unchanged and accepted | Non-goals |
| A pipeline that gates on this command's exit status and tolerates drops today | Starts failing on a reingest that lost work. Pre-1.0, no flag restores the previous behavior | DOC-3 |
| An operator reading `docs/deployment/layers.md` who schedules a reingest without following the reference link | Surprised by the new non-zero exit. Accepted: the page already defers command detail to the CLI reference (`docs/deployment/layers.md:155`), and the changelog is the notice channel | Non-goals; DOC-3 |

**Accepted.** The lint class is reported as a count and not per artifact. The operator reads the per-artifact diagnostics with `podium lint --registry <path>` against the source. OQ-1 puts the alternative to the reviewer.

**Accepted.** Exit 1 does not distinguish a cycle that ran and dropped work from a failed request. A CI gate reads one non-zero value, matching `podium lint` and the publish pipeline. OQ-2 puts the alternative to the reviewer.

## Testing

**TEST-1 · unit, `cmd/podium/layer_reingest_test.go`.** Two tests, both carrying `// Spec: §7.3.1`.

`TestWriteIngestReport_Classification` tables the arms no other level reaches, calling the printer with two `bytes.Buffer` streams:

- a rejected-only body with `"accepted": 0` and `"artifacts": []` carrying one `rejected` entry: `dropped == 1`, `ok == true`, stderr contains `rejected: finance/ap/pay-invoice (ingest.collision): ` and the entry's reason, stdout empty. This is the case the shipped gate at `cmd/podium/layer.go:459` skips, and asserting the rendered text rather than the count alone is what fails a decode whose nested JSON tags were dropped;
- the runner-less acknowledgement `{"queued": "my-layer", "queued_at": "..."}` with no `accepted` key: `ok == false`, and both buffers empty;
- a body that is not JSON: `ok == false`, and both buffers empty;
- a mixed-conflict body carrying one `artifacts` entry plus one `conflicts` entry (`artifact_id`, `version`, `code: ingest.immutable_violation`): `dropped == 1`, `ok == true`, stdout carries the `artifact:` line, and stderr carries `conflict: <id>@<version> rejected (ingest.immutable_violation); bump the version`. This is the arm the handler answers 200 rather than 409, because the 409 guard requires `res.Accepted == 0 && res.Idempotent == 0` (`pkg/registry/server/layers.go:1717`), and it is the arm that flips from exit 0 to exit 1;
- one accepted artifact plus one advisory and one embedding failure: `dropped == 0`, the `artifact:` and `advisory:` lines on stdout, the `embedding failure:` line on stderr;
- and one accepted artifact with `"lint_failures": 2`: `dropped > 0`, and stderr carries `lint failures: 2`. The case carries a comment naming `res.LintFailures` in `pkg/registry/ingest/ingest.go`, which accumulates every diagnostic for each rejected artifact, and the handler's `lint_failures` field in `pkg/registry/server/layers.go`, which emits that slice's length, as the reason the number is a diagnostic count rather than an artifact count, so no later reader turns it into one. The comment names the fields rather than line numbers, which drift.

`TestLayerReingestCmd_ExitsOneOnDroppedArtifacts` drives `layerReingest` against an `httptest` server carrying `server.NewLayerEndpoint(...).WithReingestRunner(runner)`, the harness the file already uses (`cmd/podium/layer_reingest_test.go:55-80`), with the stub runner returning `ingest.Result` values directly: a rejected-only result, a mixed accepted-plus-rejected result, a mixed result carrying `Accepted: 1` and one `ConflictReport` on a same-version hash mismatch, and a clean result. The first three assert 1 and the last asserts 0. The conflict case is the one that reaches the 200 fall-through rather than the 409 arm, so the transport arm does not cover it. The call is wrapped in `captureStdout` and `captureStderr` and carries no `t.Parallel()`, which `cmd/podium/no_parallel_guard_test.go:26` fails on.

**TEST-2 · end-to-end, `test/e2e/cli_reference_test.go`.** One `t.Parallel()` test against a single `serve --standalone --layer-path <empty>` server started with `PODIUM_PUBLIC_MODE=true`, reusing `startServerArgs`, `writeRegistry`, `smallteamLowArtifact` (`test/e2e/standalone_server_test.go:36`), and `smallteamMediumArtifact` (`:41`). Public mode wires the §13.10 sensitivity floor into the runtime reingest runner (`internal/serverboot/reingest.go:56-61`) and authenticates no caller, so §7.3.1's local-source rule admits `podium layer register --local` and `podium layer reingest` there. Each arm registers its own `--local` layer against that one server. The test carries `// Spec: §7.3.1, §13.10`.

- **mixed.** One low and one medium artifact. Exit 1; stdout carries the `artifact:` line for the low artifact; stderr carries a `rejected:` line naming the medium artifact and its §6.10 code.
- **rejected-only.** The medium artifact alone. Exit 1; stderr carries the `rejected:` line; stdout carries no raw JSON body. This is the arm the shipped build fails.
- **empty layer.** An empty `--local` directory. Exit 0, and stdout carries no `rejected:` or `conflict:` line.

The added value is that a real registry produces the rejected-only 200 through the runtime reingest runner and that the command's exit status is observed from the spawned process. The clean-cycle arm is not repeated: `test/e2e/plugin_spi_test.go:1342-1345` already asserts exit 0 on an initial reingest, and the `artifact:` lines are asserted by the readme and quickstart end-to-end tests. No arm asserts a summary line, because the proposal fixes none.

**Non-regression.** `test/e2e/plugin_spi_test.go:1329-1364` asserts a non-zero exit on the 409 pure-conflict snapshot and stays green through the transport arm. `test/e2e/description_quality_advisory_test.go:49` asserts that a description-quality advisory does not gate ingest and stays green, because an advisory is not a drop. `test/e2e/readme_claims_test.go:1071` and `test/e2e/standard_deployment_test.go:1622`, `:1632` assert the `[reingest <id>]` prefix in the watch log and are unaffected, because `podium layer watch` is unchanged. `cmd/podium/layer_reingest_test.go:44` (the rc-2 argument error) and `:56` (the break-glass body) stay green. The shipped assertions the change invalidates are `test/e2e/cli_reference_test.go:1497-1499`, `TestStandardDeploy_LayerReingestLocal` (`test/e2e/standard_deployment_test.go:519-536`), and `TestServerOps_LayerReingest` (`test/e2e/server_operations_test.go:800-821`), and CODE-2's blast-radius list restates each of them in the same commit.

**Mutation checks.** Restore the `len(parsed.Artifacts) > 0` gate and confirm TEST-1's rejected-only row and TEST-2's rejected-only arm both fail. Return 0 in place of 1 on `dropped > 0` and confirm TEST-1's exit-status test and TEST-2's first two arms fail. Drop the JSON tags from the nested rows and confirm TEST-1's rendered-text assertion fails while its count assertions still pass, which is why the text is asserted. Make `writeIngestReport` print on the `!ok` arm and confirm TEST-1's acknowledgement row fails on a non-empty buffer. Discriminate on `queued` in place of `accepted` and confirm TEST-1's acknowledgement row and TEST-2's empty-layer arm both fail. Remove `len(Conflicts)` from the `dropped` sum and confirm TEST-1's mixed-conflict row and its exit-status conflict case both fail.

**Coverage.** The printer and the classification are unit-covered in process. The exit status inside the spawned binary is scored as uncovered by the default profile even when TEST-2 drives it, so confirm the execution with `GOCOVERDIR=$(mktemp -d) go test ./test/e2e/...` and `go tool covdata textfmt` per `.claude/rules/test-coverage.md`. Measure the in-process half with `go test -coverpkg=./... -coverprofile=cover.out ./cmd/podium/`, and read `codecov/patch` on the pull request rather than the local figure. Check the end-to-end output for `SKIP` before treating a local run as evidence.

## Manual validation

`test/manual-validation.md` gains **S62**, appended after S61, on a public-mode standalone stack that needs no identity provider, container, or certificate. The Scenario index table gains a row after S61's (`test/manual-validation.md:164`):

```
| S62 | A reingest that drops an artifact fails | standalone | none | none | none |
```

The same file's **S42** carries a note the change makes false. Step 5 of the scenario "S42: a deprecated parent in an `extends:` chain" (heading at `test/manual-validation.md:3662`) states at `:3799-3801` that "`podium layer reingest` exits `0` here, because other artifacts in the layer were accepted. Assert on the output rather than on the exit status." That cycle rejects `team/pinned` as `ingest.invalid_artifact` while sibling artifacts land (`test/manual-validation.md:3790`), which is a mixed snapshot and therefore exits 1 after CODE-2. The note is replaced with one stating that the reingest exits 1 because the cycle dropped `team/pinned`, and keeping the instruction to read the printed reason rather than inferring it from the exit status.

The S62 scenario is a hand-run rather than a suite duplicate because what it reads is the operator's view of the two streams and the shell's exit status together: the accepted artifact on standard output, the dropped one on standard error with its §6.10 code, and `$?` reporting 1 on a command that reports success today.

> ## S62: A reingest that drops an artifact fails
>
> **Goal.** Validate that `podium layer reingest` exits 1 when the ingest cycle
> dropped an artifact, that each dropped artifact is named on standard error
> with its identifier, its error code, and its reason, that this holds on a
> cycle that accepted nothing, and that a clean cycle still exits 0.
>
> **Covers.** The §7.3.1 ingest outcome and the reingest exit status, and the
> §13.10 public-mode sensitivity floor as the rejection this scenario uses.
>
> **Why by hand.** The end-to-end suite reads the exit code and the two streams
> through the harness. What it does not read is the operator's terminal: that
> the accepted artifact and the dropped one arrive on different streams, that
> redirecting standard output still leaves the rejection visible, and that the
> shell's `$?` is what a cron job or a CI step would gate on.
>
> **Prerequisites.** A built `podium` binary on `PATH`. No identity provider,
> container, or certificate is required.
>
> **Steps.**
>
> 1. Create the working directory and the three layer directories this scenario
>    registers, then start a public-mode standalone registry that ingests none
>    of them at boot. Every artifact sits under its own domain path, because a
>    canonical artifact identifier derives from its domain directory and two
>    layers contributing the same identifier is a cross-layer collision.
>
>    ```bash
>    export WORK="$(mktemp -d)"
>    export REG="http://127.0.0.1:8080"
>    mkdir -p "$WORK/mixed/ops/runbook" "$WORK/mixed/ops/payroll" \
>      "$WORK/rejected/finance/ledger" "$WORK/clean/ops/oncall"
>    write_artifact() {
>      cat > "$1" <<EOF
>    ---
>    type: context
>    version: 1.0.0
>    description: $2
>    sensitivity: $3
>    ---
>
>    $2
>    EOF
>    }
>    write_artifact "$WORK/mixed/ops/runbook/ARTIFACT.md" \
>      "Restarting the ingest worker after a failed deploy." low
>    write_artifact "$WORK/mixed/ops/payroll/ARTIFACT.md" \
>      "Reconciling the monthly payroll export." medium
>    write_artifact "$WORK/rejected/finance/ledger/ARTIFACT.md" \
>      "Closing the monthly ledger." medium
>    write_artifact "$WORK/clean/ops/oncall/ARTIFACT.md" \
>      "Handing over the on-call pager." low
>    PODIUM_PUBLIC_MODE=true podium serve --standalone \
>      > "$WORK/server.log" 2>&1 &
>    echo "$!" > "$WORK/server.pid"
>    sleep 2
>    ```
>
>    **Expect.** The server is listening on `$REG` and `$WORK/server.log`
>    reports the registry started in public mode. No `--layer-path` is passed,
>    so the registry ingests nothing at boot and the only ingests are the ones
>    the steps below trigger. A refusal to start means the address is in use;
>    restart with `PODIUM_BIND=127.0.0.1:8099`, set
>    `export REG="http://127.0.0.1:8099"`, and repeat.
>
> 2. Register the mixed layer as a local source and reingest it, keeping the two
>    streams apart.
>
>    ```bash
>    podium layer register --registry "$REG" --id s62-mixed \
>      --local "$WORK/mixed" > /dev/null
>    podium layer reingest --registry "$REG" s62-mixed \
>      > "$WORK/out.txt" 2> "$WORK/err.txt"
>    echo "exit=$?"
>    cat "$WORK/out.txt"
>    cat "$WORK/err.txt"
>    ```
>
>    **Expect.** `exit=1`. `$WORK/out.txt` carries one
>    `artifact: ...   layer: s62-mixed` line for the runbook artifact and no
>    line for the payroll artifact. `$WORK/err.txt` carries one `rejected:`
>    line naming the payroll artifact, its error code, and the sensitivity
>    reason, and carries no raw JSON body. `exit=0` is the shipped behavior
>    this scenario exists to catch.
>
> 3. Register the layer holding a medium artifact alone and reingest it, so the
>    cycle accepts nothing.
>
>    ```bash
>    podium layer register --registry "$REG" --id s62-rejected \
>      --local "$WORK/rejected" > /dev/null
>    podium layer reingest --registry "$REG" s62-rejected \
>      > "$WORK/out2.txt" 2> "$WORK/err2.txt"
>    echo "exit=$?"
>    cat "$WORK/out2.txt"
>    cat "$WORK/err2.txt"
>    ```
>
>    **Expect.** `exit=1`. `$WORK/err2.txt` carries the `rejected:` line naming
>    the ledger artifact and its error code. `$WORK/out2.txt` is empty, and in
>    particular carries no pretty-printed JSON body. A JSON body on standard
>    output with `exit=0` is the shipped behavior on a cycle that accepted
>    nothing.
>
> 4. Register the layer holding a low-sensitivity artifact alone and reingest it
>    twice, so the second cycle changes nothing.
>
>    ```bash
>    podium layer register --registry "$REG" --id s62-clean \
>      --local "$WORK/clean" > /dev/null
>    podium layer reingest --registry "$REG" s62-clean; echo "exit=$?"
>    podium layer reingest --registry "$REG" s62-clean; echo "exit=$?"
>    ```
>
>    **Expect.** Both report `exit=0` and both print the
>    `artifact: ...   layer: s62-clean` line for the on-call artifact, because
>    an unchanged artifact is reported on the same line as an accepted one and
>    is not a drop.
>
> **Cleanup.** `kill "$(cat "$WORK/server.pid")"` and `rm -rf "$WORK"`.

**IMPLEMENTOR'S CHOICE:** the artifact bodies, the domain paths, and the bind address, subject to the constraints that follow. The mixed layer holds exactly one artifact the public-mode floor accepts and one it rejects, so step 2's cycle is mixed and step 3's accepts nothing. The rejected artifacts' sensitivity is above the public-mode floor at the registry's default configuration, and the accepted ones are at or below it. No two layers on the registry contribute the same artifact identifier: an identifier derives from the artifact's domain directory and is independent of the layer, and ingest rejects a second layer's same-identifier record as `ingest.collision` before it classifies anything, which would empty step 2's accepted set and make step 4's clean cycle exit 1 (`pkg/registry/ingest/ingest.go:552-563`, `:736-753`). The registry is therefore started without `--layer-path`, so the boot ingests no directory the steps also register (`internal/serverboot/serverboot.go:463-466`, `:954`). Every step reads the exit status through `echo "exit=$?"` immediately after the command, because a later command overwrites it. The bind address is `PODIUM_BIND`, defaulting to `127.0.0.1:8080` (`internal/serverboot/serverboot.go:2076`, `cmd/podium/serve.go:22`); every step reads the base URL from `$REG`, which step 1 sets.

## Documentation changes

**DOC-1.** `docs/reference/cli.md`, appended to the `podium layer reingest` section (`:471-479`) before the `### podium layer update` heading:

> The command prints one line per accepted and unchanged artifact on standard output. It prints one line per conflicted or rejected artifact on standard error, carrying that artifact's identifier, its error code, and its reason, and a `lint failures: <n>` line reporting the number of lint diagnostics the cycle raised. On a request the registry answers with an error it prints that error response on standard error instead of the per-artifact lines. It exits 1 when the cycle dropped at least one artifact, when a lint diagnostic rejected one, or when the request failed. It exits 2 on a usage error and 0 otherwise. A non-blocking advisory and an artifact whose embedding call failed are reported without changing the exit status: the artifact is stored and served. Run `podium lint --registry <path>` against the source to read the per-artifact lint diagnostics, which the reingest response reports only as a count.

Two constraints on the final text. It does not promise a per-artifact code and reason for a lint-rejected artifact, because the response carries `lint_failures` as a diagnostic count (`pkg/registry/server/layers.go:1758`, `pkg/registry/ingest/ingest.go:573`) and the command prints that count. It does not describe the lint case as wholly new, because a snapshot rejected entirely by lint is already answered 422 and already exits 1 (`pkg/registry/ingest/ingest.go:918-921`, `pkg/registry/server/layers.go:1774-1775`); the mixed lint snapshot is the one that changes. If OQ-1 resolves toward itemising lint diagnostics on the wire, this paragraph is rewritten in that change rather than written speculatively now.

**DOC-2** is the manual-validation edit above: the new S62 scenario, its Scenario index row, and the replaced exit-status note in S42's step 5.

**DOC-3.** `CHANGELOG.md` gains one bullet under `## [Unreleased]` / `### Changed`:

> `podium layer reingest` reports the ingest outcome in its exit status. It exits 1 when the cycle dropped at least one artifact, which covers a same-version content conflict, a lint failure, and a rejection such as the public-mode sensitivity floor or a cross-layer collision, and it exits 0 otherwise. On a cycle the registry answers 200, each conflicted and each rejected artifact is printed on standard error with its identifier, its error code, and its reason, including on a cycle that accepted nothing, where the command previously printed the response body to standard output and exited 0. A lint drop is reported as the number of diagnostics the cycle raised, and `podium lint` against the source names the artifacts behind them. A non-blocking advisory and a failed embedding call are reported without changing the exit status. A pipeline that gates on this command's exit status will now fail a reingest that lost work; pre-1.0, no flag restores the previous behavior.

The entry lands in the same commit as CODE-2, matching `fa9eedf`. No `### Fixed` bullet is added: the accurate half of one, that the command previously printed the response body and exited 0, belongs with the change that supersedes it, and the rest of that framing would misstate the shipped behavior, because the pretty-printed body does carry each rejection's identifier, code, and reason (`pkg/registry/server/server.go:1438-1444`, `pkg/registry/server/layers.go:1733-1740`).

`docs/deployment/layers.md` is not edited. Its "Keeping layers current" section already defers command detail to the CLI reference (`:155`), it maps to the same `D-cli` slug (`tools/doccov/manifest.yaml:51-52`), and nothing on it becomes false: `grep -n "exit" docs/deployment/layers.md` returns nothing.

## Resolved in adversarial review

Review rounds populate this section. Each entry names the defect the round found in this proposal and the amendment that answered it.

### Pass 1 (2026-09-08, automated)

- **S62 registered layers over the directory `--layer-path` had already bootstrapped, so steps 2 and 4 were unattainable.** The boot ingest stores the low-sensitivity artifact under a layer named for the directory basename, and a second layer contributing the same identifier is rejected as `ingest.collision` before the accepted, unchanged, and conflict classification. The scenario now starts the registry without `--layer-path`, registers three directories whose domain paths are all distinct, and the IMPLEMENTOR'S CHOICE block carries the constraint that no two layers contribute the same artifact identifier, with the collision rule as the reason.
- **S62 told the operator to set `PODIUM_PORT`, which nothing in the product reads.** The recovery instruction names `PODIUM_BIND` and its `127.0.0.1:8080` default, and every step reads the base URL from the `$REG` variable step 1 sets rather than repeating a literal address.
- **SPEC-1 required each dropped artifact to be reported on standard error, which the staged code cannot do for a lint drop.** The staged paragraph now states that the command names each conflicted and each rejected artifact with its identifier, its §6.10 code, and its reason, and reports a lint drop as the number of diagnostics the cycle raised, pointing at `podium lint` for the artifacts behind them. The lint failure stays in the drop set, so the exit status is unchanged, and per-artifact lint itemisation stays with OQ-1.
- **More than one shipped end-to-end assertion breaks.** `TestStandardDeploy_LayerReingestLocal` and `TestServerOps_LayerReingest` each register the server's own `--layer-path` directory as a second layer and pass today only through the raw-body fallback. Both are named in the Watch-out-for entry, in CODE-2's blast-radius list with the restatement each takes, in the Non-regression paragraph, and in S2's indivisible edit set.
- **No test covered the mixed-conflict snapshot, which the change flips from exit 0 to exit 1.** TEST-1 gains a classification row carrying one `artifacts` entry beside one `conflicts` entry and a fourth stub-runner case returning `Accepted: 1` with one `ConflictReport`, the Edge cases table gains the matching row, and the mutation checks gain the removal of `len(Conflicts)` from the `dropped` sum.
- **"The ingest pipeline returns a non-nil error only for the lint-only case" was false.** The Current-state paragraph now names the whole-cycle failures that return an error, including the active freeze window and the source, store, and provider failures, separates them from the per-artifact rejections the loop records with `err == nil`, and states that SPEC-1's "A completed ingest cycle" wording is what keeps the freeze-window and source-unreachable rows out of the drop definition.
- **S42's "reingest exits 0 here" note becomes false and was unstaged.** The manual-validation edit list, DOC-2, S5 of the checklist, and the Summary now carry the replacement of that note with one stating that the reingest exits 1 because the cycle dropped `team/pinned`.
- **TEST-1's lint row instructed a comment citing the `idempotent` field.** The row now names `res.LintFailures` and the handler's `lint_failures` field rather than line numbers.
- **DOC-3's staged CHANGELOG bullet and the Summary's printer bullet still promised a per-artifact code and reason for every drop.** Both now match the narrowed SPEC-1 and DOC-1: the changelog entry states that each conflicted and each rejected artifact is printed with its identifier, its error code, and its reason, and that a lint drop is reported as the diagnostic count with `podium lint` naming the artifacts behind it. The response carries `lint_failures` as a count alone (`pkg/registry/server/layers.go:1758`), and a lint-failing record never reaches `res.Rejected` (`pkg/registry/ingest/ingest.go:572-574`).
- **The staged §7.3.1 paragraph attributed drop reasons to text that section does not state.** The sentence now defines the dropped set and attributes each reason to the place that states it: the ingest cases, the errors rule, §13.10, and §4.6.

### Pass 2 (2026-09-08, automated)

- **SPEC-1 stated the drop set as a closed list that omitted rejection classes the pipeline records.** An `extends:` chain that crosses types, which §4.6 mandates ingest reject (`pkg/registry/ingest/ingest.go:662-670`), and a record whose manifest cannot be built (`:602-609`) both land in `res.Rejected`, reach the 200 body's `rejected` array, and therefore flip the new exit status, while neither is an unresolved `extends` pin. The staged paragraph now opens the list with "include", names both classes, and the Current-state paragraph enumerates the rejection sites with their line numbers and records that the governing sentence is the classification rather than the list.
- **SPEC-1 promised a per-artifact stderr line for every rejected artifact on two arms that discard `res.Rejected`.** The 409 pure-conflict arm writes the conflicts map and returns above the `rejected` array (`pkg/registry/server/layers.go:1717-1726`, `:1728-1740`), and the lint arm discards the result at `pkg/registry/ingest/orchestrator.go:164-166` after `Ingest` returned it beside `ErrLintFailed` (`pkg/registry/ingest/ingest.go:920`), so the CLI has no rejections to name on either. SPEC-1, DOC-1, and DOC-3 now scope the per-artifact naming to a completed cycle and state that a cycle answered with a §6.10 error envelope reports that envelope instead. The two Edge-cases rows carry the same limit, a new Watch-out-for entry names the reachable snapshot, and OQ-4 puts itemising the envelopes to the reviewer with the constraints any answer must satisfy.

### Pass 3 (2026-09-08, automated)

- **Pass 2's amendment stated the drop contract a fourth time and carried a false code fact, and the contract had never been written down once.** OQ-4 stated that itemising the lint arm needs the pipeline to return the result beside `ErrLintFailed`, which `pkg/registry/ingest/ingest.go:920` already does; `pkg/registry/ingest/orchestrator.go:164-166` discards it one frame up. The classification and the response arms are now derived once, from the twelve `res.Rejected` sites, the two `res.Conflicts` sites, the single `res.LintFailures` site, the whole-cycle returns, and the single return of a result beside an error, into the drop table in the Current-state section. The two Edge-cases rows and the Watch-out-for entry dropped their prose copies of the envelope limitation and refer to the table. SPEC-1, DOC-1, and DOC-3 dropped theirs with no replacement pointer, because all three are staged text for `spec/07-external-integration.md`, `docs/reference/cli.md`, and `CHANGELOG.md`, which cannot refer to a table internal to this proposal; SPEC-1 keeps one sentence stating that a cycle the registry answers with a §6.10 error reports that envelope and exits non-zero, and DOC-1 keeps the same predicate as a clause scoping its per-artifact stderr lines to the 200 path.
- **No statement covered a snapshot carrying both conflicts and lint diagnostics with nothing accepted.** The guard at `pkg/registry/ingest/ingest.go:919` requires `len(res.Conflicts) == 0`, so `ErrLintFailed` does not fire and the handler's 409 guard (`pkg/registry/server/layers.go:1717`) does. The envelope names the conflicts and reports neither the lint diagnostic count nor the rejections. It is row 3 of the drop table and one Edge-cases row, and it changes no behavior.
- **OQ-4 was deleted and its content moved to a Non-goal.** Its staged position was already a Non-goal, and its constraint list carried the false claim above. The Non-goal bullet keeps the constraints any future itemisation has to satisfy and names `pkg/registry/ingest/orchestrator.go:164-166` as the site such a change has to touch.

### Redesign 1 (2026-09-08, automated)

Pass 3 above is the record of this redesign's substance. This entry records its scope.

**Areas redesigned.** The Summary's envelope watch-out, the Current-state classification paragraph, SPEC-1's envelope sentence, the two Edge-cases envelope rows, DOC-1's envelope and exit sentences, DOC-3's envelope sentence, the pass 2 record's code citation, OQ-4, and the Non-goals list.

**Why.** The proposal stated the ingest drop classification and the two error-envelope arms in seven places. Each review round corrected a different copy and introduced a new wrong sentence, and the contract itself had never been written down. The redesign states the mapping from cycle state to response arm once, as the drop table in Current-state, and reduces every other statement to a reference to that table or to the scope each deliverable owns. It adds no behavior, no test obligation, and no spec citation, and the rows it describes are already pinned by `test/e2e/plugin_spi_test.go:1329-1364`, TEST-1, and TEST-2.

**What it deleted.** The Watch-out-for bullet enumerating what each envelope carries, replaced by a pointer to the table. The Current-state paragraph on the 409 guard and the whole-cycle errors, replaced by the table and its derivation. SPEC-1's sentence enumerating which cycles get an envelope and what each envelope names, replaced by one sentence stating that a cycle the registry answers with a §6.10 error reports that envelope and exits non-zero. The explanatory clauses in both Edge-cases envelope rows, including the false clause "because the guard returns before the result is serialized". DOC-1's envelope sentence, whose scoping role is carried by the reworded exit sentence. DOC-3's changelog sentence about the two envelope arms, with no replacement, because both arms exit 1 before and after. The false clause about the lint guard in the pass 2 record, replaced by the site that discards the result. OQ-4 in full, with its constraint list preserved in a Non-goals bullet. No staged spec, code, test, or documentation edit gained or lost a requirement, so the implementation checklist, its dependencies, the files each step touches, and the testing section are unchanged.

**Open decisions the redesign recorded.**

- **OD-1. Delete OQ-4, or keep it as a pointer to the drop table.** Applied as deletion. Deleting moves a scope decision whose position is already fixed into the section that holds scope decisions and removes the standing invitation that produced three rounds of re-litigation. The cost is that a reviewer who wants the envelopes itemised has to overturn a Non-goal rather than answer a question. Both options carry the same constraint list.
- **OD-2. How much of SPEC-1's staged paragraph is rewritten.** Applied as the one-sentence replacement, keeping the open-ended rejection-class list that passes 1 and 2 settled. The alternative replaces the class list with section pointers, which removes a list that drifts as the pipeline gains rejection classes, at the cost of a spec paragraph that names no class. One of the alternative's pointers, §4.7.8 for the quota rejections, is unverified against §7.3.1's own errors rule. Revisit if a third rejection class lands.
- **OD-3. Whether the consolidation also covers the "`lint_failures` is a diagnostic count" duplication.** Applied as no. That fact is stated in the Watch-out-for entry, CODE-1, the Edge cases table, OQ-1, DOC-1's constraints paragraph, and TEST-1's lint row, which is the same churn pattern with a different subject. Folding it in would require the drop table to carry its derivation and would widen the edit set. It is a candidate for a separate pass if a future round corrects one of those copies.

## Open questions

**OQ-1.** Does a lint failure gate the exit status given that the response carries only a count? The staged position is yes: a lint-rejected artifact did not land, so the cycle dropped work, and the command prints `lint failures: <n>` without a per-artifact reason. The alternative is to itemise `res.LintFailures` on the wire the way `conflicts` and `rejected` are itemised (`pkg/registry/server/layers.go:1733-1749`), which gives the operator the reason at the cost of a wider response body, a matching web UI change, and a rewrite of DOC-1's paragraph. The cost of the staged position is that `dropped` sums a diagnostic count into an artifact count, which the printer's doc comment and TEST-1's lint row both record.

**OQ-2.** Is exit 1 the right code for a cycle that ran and dropped work, or should it differ from the code a failed request returns? The staged position reuses 1, matching `podium lint` and the publish pipeline, so a CI gate needs no new value. A distinct code would let a pipeline retry a transport failure and fail hard on a drop, at the cost of a value no shipped command uses and no documentation states.

**OQ-3.** Is an artifact whose embedding call failed a drop? The staged position is no: the manifest is stored and the artifact is searchable by BM25 until `podium admin reembed`, so the command reports it and exits 0. Treating it as a drop would fail a reingest for a degraded but served artifact, and would make the exit status depend on an external embedding provider's availability.

## Non-goals

- **Changing the HTTP status of a mixed or rejected-only reingest, or adding a §6.10 error code for a non-conflict drop.** The 409 pure-conflict arm and the 200 fall-through stay as they are. The same handler serves the inbound webhook trigger (`pkg/registry/server/webhook_ingest.go:89`), and a Git host retries a non-2xx delivery, so promoting an artifact-level rejection to a 4xx would produce a retry loop over a rejection every retry reproduces.
- **Itemising `res.Rejected` on the 409 and 422 envelopes**, which are rows 2 and 3 of the drop table. Both arms already exit 1, so the exit contract this proposal fixes is correct on both, and no deliverable here describes what those envelopes carry. Repairing the reporting is a registry change with its own tests: the 409 arm holds the result and returns above the `rejected` array (`pkg/registry/server/layers.go:1717-1726`, `:1733-1740`), and the 422 arm has no result to write because `SourceIngestWithOptions` replaces it with `nil` at `pkg/registry/ingest/orchestrator.go:164-166` after `ingest.Ingest` returned it at `:920`. Any resolution has to keep the response to the §7.3.1 body plus the §6.10 envelope with no new field name outside those two, keep the statuses 409 and 422 so the webhook trigger's retry behavior is unchanged, carry the same `artifact_id`, `code`, and `reason` keys the 200 body's `rejected` array carries (`pkg/registry/server/layers.go:1733-1740`), and change the whole-cycle error propagation at `orchestrator.go:164-166`, which every caller of `SourceIngestWithOptions` observes. It lands as its own proposal.
- **Changing `podium layer register` or `podium layer restore`.** `register` returns `LayerRegisterResponse` (`pkg/registry/server/layers.go:1364-1369`) and `restore` returns `{"restored": id}` (`:1458`). Neither runs an ingest and neither carries the report.
- **Changing the web UI.** `web/ui/src/surfaces/ReingestControl.tsx` already reads `rejected`, `conflicts`, `lint_failures`, and `artifacts` from the same response and reports the drop counts.
- **Itemising lint diagnostics per artifact on the reingest response.** The body carries `lint_failures` as a count and the CLI reports that count. The diagnostics stay where `podium lint` and the boot-path log already carry them, subject to OQ-1.
- **Adding a `--json` or other structured-output mode to `podium layer reingest`.** The exit status plus the itemised stderr lines are the contract this proposal fixes; a machine consumer that wants the full report calls the endpoint.
- **Giving `podium layer watch` an exit status.** It runs until it is interrupted.
- **Changing what `podium layer watch` prints per tick** (dropped alternative CODE-3). The information-loss premise does not hold: the tick prints the entire response body (`cmd/podium/layer.go:191`), which is server-side pretty-printed (`pkg/registry/server/server.go:1440-1442`) and already itemises `conflicts`, `lint_failures`, `rejected` with per-entry `artifact_id`, `code`, and `reason`, `embedding_failures`, and `advisories`. The labelled line conveys the same fields in a shorter form, and the watch loop has no exit status, so only the cosmetic half of the defect applies there. The change would also remove the `[reingest <id>]` prefix that `test/e2e/readme_claims_test.go:1071` and `test/e2e/standard_deployment_test.go:1622`, `:1632` assert on, and it would split a long-running poller's per-tick output across two streams for a command whose only channel is the operator's terminal. Nothing is duplicated by leaving `layerWatch` alone, because watch prints a raw body and reingest prints a classified report. If the labelled report is wanted for watch later, the minimal form retains the prefix line and appends the report, landing with its own updates to those two assertions.
- **Adding a rejected count to the `layer.ingested` event payload** (dropped alternative CODE-4). The payload's counts are an undocumented, code-only key set: §7.3.2 says only that the event reports a completed cycle with summary counts, its worked example carries `data: { "layer": "team-shared" }` alone, and `docs/reference/http-api.md:668` enumerates no `data` field. The spec's described consumer subscribes the event to trigger `podium sync --config` (§7.8) and does not branch on the counts, so a fifth undocumented key gives a receiver author nothing to read. No Go test asserts the payload keys today, so the change would ship a wire-format change the suite cannot observe. A consumer that wants the classification has the §7.3.1 response body, and the operator boot log already prints `rejected=` per layer (`internal/serverboot/serverboot.go:550`, `:674`). If the payload is to become a gate input, that needs a documented `data` schema in §7.3.2 and `docs/reference/http-api.md`, a test pinning the keys, and a decision about the §13.8 ingest metric, which derives success from the returned error alone (`internal/serverboot/reingest.go:93-101`) and has the same gap.
- **Naming the exit status in `docs/deployment/layers.md`** (dropped alternative DOC-2). The section already defers to the CLI reference (`:155`), and the repository states an exit contract once, on the page that owns the command: `docs/consuming/publishing.md:167-169` carries the publish-pipeline codes and `docs/reference/cli.md` does not restate them. The watch clause DOC-2 would have added is already written at `docs/reference/cli.md:505`. Nothing on the page becomes false.
- **Widening the §8.1 audit record for `layer.ingested`** (`pkg/registry/ingest/orchestrator.go:182-186`), which keeps its `accepted` field alone.
- **Any SDK change.** Neither `sdks/podium-py` nor `sdks/podium-ts` exposes reingest.
- **A migration flag, environment variable, or dual code path preserving the exit-0-on-drop behavior.**

