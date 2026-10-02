# Proposal 0033: Apply the §4.6 collision rule to filesystem-source sync and state the workspace overlay exception

- Issue: (to be filed)
- Status: Implemented (2026-10-01). Signed off as staged, including the wholesale overlay replacement for an overlay artifact that declares `extends:`. OQ-1: keep the staged default (the workflow publishes the materialized output, then the target counts as failed).
- Date: 2026-10-01

This document stages the proposed spec, code, test, and documentation changes. It does not modify any spec, code, or doc file. Apply the changes in the staged sections after sign-off.

## Summary

**What changes.**

- §1, §4.6, and §6.4: the workspace local overlay becomes an explicit exception to the §4.6 collision rule. An overlay artifact replaces the registry-side artifact with the same canonical ID on every consumer, whether or not it declares `extends:`, and the consumer resolves no `extends:` chain for it (SPEC-1, SPEC-2, SPEC-3). No overlay code changes, because every consumer already behaves this way for an overlay directory without a `multi_layer: true` `.registry-config`. SPEC-3 states no rule for a multi-layer overlay directory, on which the Go consumers and the SDKs already disagree (a non-goal).
- §4.6 and §7.3.1: the collision rule is stated for registry-side layers on every composition path, and `ingest.collision` is defined in the §7.3.1 Errors list (SPEC-2, SPEC-4).
- §13.11.3 and §13.11.4: a filesystem-source `podium sync` drops the higher-precedence artifact of an unsanctioned collision, names it on standard error, materializes the rest, and exits 1. Watch mode reports each cycle's drops and exits 1 on interrupt when any cycle it reported dropped one, and `podium sync override` reports the drops of its re-materialization and exits 1 (SPEC-5).
- Shared library: `pkg/layer` gains the collision code, the reason text, and the `extends:` sanction predicate, and `pkg/version` gains the single pin-stripping helper (CODE-1). `filesystem.Walk` gains `CollisionPolicyDrop` with an `OnCollision` callback (CODE-2). `pkg/sync` carries the drops on `Result.Dropped`, `RenderResult.Dropped`, and `OverrideResult.Dropped` (CODE-3). The `podium sync` and `podium sync override` CLI paths report them and set the exit status (CODE-4). The `layer.Compose` comments stop claiming highest-wins is the §4.6 effective view (CODE-5).
- Tests: unit, integration, and end-to-end tests pin the drop policy, the report, and the exit status, and the tests that pin highest-wins on an unsanctioned collision are rewritten (TEST-1, TEST-2, TEST-3).
- §11: the overlay precedence test covers an overlay artifact that declares `extends:`, and the filesystem-to-server equivalence test gains a collision case (SPEC-6, TEST-4, TEST-5).
- Docs, changelog, and manual validation follow (DOC-1, CL-1, MV-1).

**Fixed decisions.**

- Option (b) is settled, and for the overlay it supersedes the proposal 0032 OQ-1 sign-off (see "Current state and the gap"). The overlay is an explicit exception, and registry-side layers composed by a filesystem-source sync follow extends-or-reject with a drop, a report, and a non-zero exit.
- An overlay artifact that declares `extends:` replaces the same-ID registry-side artifact wholesale. No consumer merges it with a parent, and an unresolvable overlay `extends:` is not an error.
- Filesystem-source sync drops the higher-precedence artifact and keeps the lower-precedence one. A dropped artifact is also removed from the `extends:` resolver's input, so it cannot serve as a parent.
- The report line is `rejected: <id> (ingest.collision): <reason>` on standard error, the format `podium layer reingest` prints, with the reason ingest already produces. The reason names the artifact and the `extends:` remedy and never the layer that already contributes the ID.
- A drop does not stop the sync. Every other artifact is materialized, stale-file cleanup runs, and the lock file omits the dropped artifact. A drop does not roll back a `podium sync override` toggle.
- `podium sync` and `podium sync override` exit 1 on a drop. Every other exit status of both commands is unchanged.
- `podium sync --watch` counts only the cycles it reported. A cycle still running when the interrupt arrives can finish without a report, and the next `podium sync` reports its drops.
- `Walk` refuses `CollisionPolicyDrop` without an `OnCollision` callback. `CollisionPolicyHighestWins` stays, because the overlay walk uses it.
- `podium sync --check` without `--config` composes nothing and never reports a drop. A `kind: workspace` target under `--config --check` does compose, and counts as failed on a drop. A `kind: marketplace` target under `--check` renders nothing.
- A server-source `podium sync` is unchanged and exits 0, because the server never stored the dropped artifact.
- Ingest keeps its arrival-order rejection and its bidirectional admission. Only its helpers move to the shared library.
- `ingest.collision` is defined in §7.3.1. §6.10, §6.9, and the matrix are not edited.
- The proposal adds no environment variable, flag, error code, endpoint, or SPI.

**Watch out for.**

- **Spec line numbers are anchors at the time of writing.** Locate each edit by its quoted current text. Proposal 0032 already rewrote the first sentence of the §4.6 paragraph after the composition-order list, so SPEC-2 quotes the post-0032 wording.
- **CODE-3 breaks existing tests the moment it lands.** `TestFilesystemSync_CollisionHighestWinsLaterLayerWins` (`test/e2e/filesystem_sync_test.go`), `TestCoreConcept_LayerPrecedenceOverride`, `TestCoreConcept_LayerOrderAlphabeticalDefault` (`test/e2e/core_concepts_test.go`), and the unit test `TestRun_HigherLayerWinsOnCollision` (`pkg/sync/sync_test.go:265-298`) pin highest-wins on a collision without `extends:`. The CODE-3 signature changes to `filesystemRecords` and `FetchRecords` also break the compile of `pkg/sync/lock_content_hash_test.go:209`, `pkg/sync/render_test.go:186`, and `pkg/sync/records_test.go:86`. Step S10 lands CODE-3, CODE-4, TEST-3, and those test updates together for that reason.
- **The `--check` paths differ.** `runSyncCheck` (`cmd/podium/main.go`) loads only `sync.yaml`. `runWorkspaceTarget` under `--check` calls `sync.Run` with `DryRun` set and returns before printing, so the drop report must run before that early return. `RunMarketplace` with `Check` set returns before rendering (`pkg/sync/marketplace_run.go`).
- **`Walk` compares each later record against the kept record.** With layers L1, L2, and L3 all contributing X without `extends:`, both drops report `ExistingLayer` as L1. A test expecting L2 for the second drop is wrong.
- **The resolver input must be pruned.** `resolveExtends(deduped, all)` builds the same-ID chain from `all` (`pkg/registry/filesystem/walk.go`, `pkg/registry/filesystem/extends.go`). Passing `all` unchanged lets a dropped record act as a same-ID parent, and the three-layer test in TEST-1 catches it.
- **The server can still admit a collision the filesystem sync drops.** Ingest admits a collision when an existing cross-layer record declares `extends:` on the ID (`pkg/registry/ingest/ingest.go`). Layers L1:X, L2:X with `extends: X`, and L3:X without `extends:` diverge. That is a non-goal, and no test may assert parity on that input.
- **`TestLoadArtifactFromOverlay_ExtendsChildServesTheAuthoredContentHash` stays.** It pins the overlay exception (an overlay `extends:` resolves no chain). Do not rewrite it to expect a merge or an error.
- **The workflow publish phase runs before the target is marked failed.** A `--config` target with a workflow pushes the output that omits the dropped artifact, and then counts as failed. OQ-1 asks whether that is the intended default.

## Implementation checklist

- [x] **S1 · spec** — SPEC-4. §7.3.1 defines `ingest.collision` in the Errors list and names it in the Ingest outcome.
      Levels: —. Depends on: —
- [x] **S2 · spec** — SPEC-3. §6.4 replaces "merge semantics are identical to registry-side layers" with the overlay exception.
      Levels: —. Depends on: —
- [x] **S3 · spec** — SPEC-5. §13.11.3 states the filesystem-source collision outcome, report, continuation, and exit status, and §13.11.4 states the watch-mode report and exit status.
      Levels: —. Depends on: S1, S2
- [x] **S4 · spec** — SPEC-2. §4.6 states the collision rule for registry-side layers on every composition path, names `ingest.collision`, and narrows "Silent shadowing is never permitted".
      Levels: —. Depends on: S1, S2, S3
- [x] **S5 · spec** — SPEC-1. §1 restricts "no silent shadowing" to registry-side layers and names the §6.4 exception.
      Levels: —. Depends on: S2
- [x] **S6 · spec** — SPEC-6. §11 overlay precedence test covers an overlay `extends:`, and the equivalence test gains the collision case.
      Levels: —. Depends on: S2, S3, S4
- [x] **S7 · code** — CODE-1. `pkg/layer/collision.go` and `version.StripPin` land, and ingest, the filesystem walk, and lint call them.
      Levels: unit, integration. Depends on: S1, S4
- [x] **S8 · code** — CODE-2. `filesystem.Walk` gains `CollisionPolicyDrop` and `WalkOptions.OnCollision`.
      Levels: unit. Depends on: S7
- [x] **S9 · test** — TEST-1. Unit tests for the shared collision helpers and the drop policy.
      Levels: unit. Depends on: S7, S8
- [x] **S10 · code** — CODE-3, CODE-4, TEST-3. `pkg/sync` switches filesystem sync to the drop policy and carries the drops on `Result`, `RenderResult`, and `OverrideResult`, the CLI reports them and exits 1, the existing `pkg/sync` tests named in CODE-3 are updated, and the end-to-end tests that pin highest-wins are replaced. Bundled because the existing unit and end-to-end tests fail as soon as CODE-3 changes the kept copy and the signatures, and their replacements assert the CODE-4 exit status.
      Levels: unit, integration, e2e. Depends on: S3, S8
- [x] **S11 · test** — TEST-2. `pkg/sync` collision tests for materialization, the lock, dry-run, and recovery.
      Levels: integration. Depends on: S10
- [x] **S12 · test** — TEST-4. The §11 equivalence test gains the collision case.
      Levels: integration, materialization. Depends on: S6, S10
- [x] **S13 · code** — CODE-5. The `layer.Compose` and companion test comments stop claiming highest-wins is the §4.6 effective view.
      Levels: unit. Depends on: S4
- [x] **S14 · test** — TEST-5. Overlay exception tests: a sync unit case and an MCP end-to-end case for an overlay artifact declaring `extends:`, and exception comments on the existing overlay tests.
      Levels: unit, e2e. Depends on: S2, S10
- [x] **S15 · docs** — DOC-1. Layers, extends, why-podium, local deployment, error codes, and the `podium sync` and `podium sync override` CLI reference follow the spec.
      Levels: —. Depends on: S10
- [x] **S16 · docs** — CL-1. The `## [Unreleased]` `### Changed` entry for the filesystem-sync break.
      Levels: —. Depends on: S10
- [x] **S17 · docs** — MV-1. Manual-validation scenario S77 for the filesystem-sync drop and the overlay exception.
      Levels: manual. Depends on: S10

**Ordering constraints.** The spec steps are ordered so that no edit cites text a later step adds. S1 defines `ingest.collision`, which S3, S4, and S6 name. S2 states the §6.4 exception, which S3, S4, S5, and S6 cite. S3 states the §13.11.3 filesystem-source drop, which the S4 rejection bullet cites. S6 restates S2, S3, and S4. Each code, test, and docs step follows the spec step whose text it implements or cites.

## Current state and the gap

§4.6 "Merge semantics for collisions" frames the extends-or-reject rule as an ingest event: "A collision is rejected at ingest **unless** the higher-precedence artifact declares `extends: <lower-precedence-id>` in frontmatter." It then states without qualification that "Silent shadowing is never permitted." The paragraph after the §4.6 composition-order list defers to that rule and says "layer 3 is merged in by the MCP server before returning results", with no collision outcome for that merge. §1 places the workspace overlay under "no silent shadowing" by name, in "What Podium provides" and in problem-list item 4. §6.4 says the overlay's "merge semantics are identical to registry-side layers". Two consumer-side composition paths never received a specified outcome, and both shadow silently today.

### The workspace overlay

`applyOverlay` (`pkg/sync/sync.go`) replaces the same-ID base record wholesale and never reads the overlay record's `extends:`. `overlay.Filesystem.Resolve` (`pkg/overlay/overlay.go`) walks the overlay with `CollisionPolicyHighestWins` and no `ResolveExtends`. The MCP server's `loadArtifact` (`cmd/podium-mcp/main.go`) returns an overlay match without consulting the registry. The Python and TypeScript SDK overlays (`sdks/podium-py/podium/_overlay.py`, `sdks/podium-ts/src/overlay.ts`) contain no `extends` handling. Every consumer therefore replaces the registry-side artifact with the overlay artifact, whether or not the overlay artifact declares `extends:`.

The §11 developer-host and overlay precedence tests do not say whether the conflicting artifact declares `extends:`. The tests that implement them use an overlay with no `extends:` and assert replacement (`TestSyncServerSource_WorkspaceOverlayWins` in `test/integration/sync_equivalence_test.go`, `TestRun_OverlayOverridesRegistry` in `pkg/sync/overlay_test.go`, `TestHarness_MCPOverlayOverridesRegistry` in `test/e2e/harness_materialization_test.go`). `TestLoadArtifactFromOverlay_ExtendsChildServesTheAuthoredContentHash` (`cmd/podium-mcp/overlay_load_test.go`) already pins that an overlay child's `extends:` resolves no chain. Under §1 and §4.6 as written, that replacement is a silent shadow.

### Filesystem-source `podium sync`

`filesystemRecords` (`pkg/sync/sync.go`) walks the registry layers with `CollisionPolicyHighestWins`. Under that policy `Walk` skips the `extends:` check and keeps the higher record (`pkg/registry/filesystem/walk.go`). The command reports nothing and exits 0 (`cmd/podium/main.go`).

Server ingest drops the colliding incoming record, stores the rest, and reports `ingest.collision` with a reason that names the `extends:` remedy (`collisionRejection` in `pkg/registry/ingest/ingest.go`). `podium layer reingest` then exits 1 (§7.3.1 "Ingest outcome", `cmd/podium/layer.go`). A standalone server started with `--layer-path` ingests the layers lowest-precedence first (`internal/serverboot/serverboot.go`). On the same colliding directory it serves the lower copy, while filesystem sync serves the higher one. That breaks the parity §2.2, §13.11.3, and the §11 filesystem-to-server equivalence test require. The equivalence fixture carries no collision, so no test detects the divergence.

### Two implementations of one rule

Ingest decides by arrival order and admits a collision when either the incoming record or an existing cross-layer record declares `extends:` on the ID. `Walk` decides by precedence and checks only the higher record (`declaresExtendsTo`). The pin-stripping helper exists three times: `stripPin` in `pkg/registry/ingest/ingest.go`, `stripPin` in `pkg/registry/filesystem/extends.go`, and `stripVersionPin` in `pkg/lint/body_rules.go`. Neither `Walk` policy implements drop-the-higher, keep-the-lower, and continue: `CollisionPolicyError` aborts the whole walk and `CollisionPolicyHighestWins` shadows.

### `ingest.collision` has no spec definition

The code appears nowhere in `spec/`. The other ingest codes are defined in the §7.3.1 Errors list, and §6.10 lists only namespaces. The §6.10 matrix cell (`tools/matrix/matrices.go`) and an annotated test (`TestWalk_CollisionWithoutExtendsFails` in `pkg/registry/filesystem/walk_test.go`) already exist, and `docs/reference/error-codes.md` documents the code.

The overlay text and the §11 overlay tests were introduced together in commit 5b591588 (2026-05-04) and were never reconciled with §4.6. Proposal 0032 recorded the divergence as its OQ-1, and its sign-off directed both the overlay and filesystem sync to follow extends-or-reject in follow-up work (`proposals/0032-correct-seven-spec-statements-that-contradict-the-spec-the-c.md:4`). The follow-up decision that commissioned this proposal (2026-10-01) selected option (b) instead: filesystem sync follows extends-or-reject, and the per-developer overlay is an explicit exception. For the overlay, signing off this proposal supersedes the 0032 OQ-1 answer.

## Decisions

- **The overlay is an exception, and it replaces.** An overlay artifact that collides with an artifact in the caller's composed registry-side view replaces it on every consumer that merges the overlay: the MCP server, `podium sync` against both sources, and the SDKs. The consumer emits no report and no warning. The overlay is per-developer, is never ingested or shared, and exists for the local iteration loop, so a replacement affects only the developer who wrote it. Promoting an overlay artifact to a shared layer subjects it to the §4.6 rule.
- **An overlay `extends:` is not resolved.** The exception covers an overlay artifact that declares `extends:` as well. Resolving it would need a second chain resolver in the MCP server, a fetch-only registry path that skips materialization and audit, a version-pin rule for an artifact that is never ingested, a new error code for an unresolvable parent, and a field-by-field port of the §4.6 merge into both SDKs, which carry no YAML parser. Landing it on fewer than all consumers would make the MCP server and an SDK return different bytes for one overlay artifact. Every consumer replaces wholesale today, so stating that behavior needs no overlay code change.
- **Which artifact is dropped.** A filesystem-source sync composes every layer in one pass, so it drops the higher-precedence colliding artifact and keeps the lower-precedence one, which matches `Walk`'s precedence model. For a directory a standalone server ingests on a fresh start, lowest-precedence layer first, this is the artifact server ingest drops. The spec text records that ingest rejects the artifact of the cycle being ingested, so it does not claim the server always drops by precedence.
- **A dropped artifact is not a parent.** With three or more layers, the dropped record is removed from the record set the `extends:` resolver receives. Layers L1:X, L2:X with no `extends:`, and L3:X with `extends: X` drop L2, and L3 merges over L1. This matches the server, which never stored L2.
- **The rule is reused through the shared library.** `pkg/layer` is the §9.1 `LayerComposer` concern and imports only `pkg/manifest`, so `pkg/registry/ingest` and `pkg/registry/filesystem` can both import it without a cycle. It holds the code constant, the `Collision` type with its reason, and the sanction predicate. Pin stripping is a §4.7.6 pin concern, so `version.StripPin` goes in `pkg/version` beside `ParsePin`, and the three copies collapse into it. Ingest keeps its bidirectional admission, which exists for out-of-order reingest.
- **`Walk` gains a policy and a callback.** `CollisionPolicyDrop` with `WalkOptions.OnCollision` keeps `Walk`'s signature and avoids touching its other call sites. `Walk` returns an error when `CollisionPolicyDrop` is set and `OnCollision` is nil, so no caller can drop an artifact silently at the walk. Above the walk, every `pkg/sync` path that composes for a write surfaces the drops: `Run` on `Result.Dropped`, `Override` on `OverrideResult.Dropped`, and `Render` on `RenderResult.Dropped`. `ResolveEffectiveView` discards them because it writes nothing. `CollisionPolicyHighestWins` stays, because the overlay walk uses it for layers inside the overlay directory.
- **The report reuses ingest's reason and reingest's format.** The line is `rejected: <id> (ingest.collision): <reason>`, one per dropped artifact on standard error, with the reason `cross-layer collision: "<id>" is already contributed by another layer; declare extends: <id> to overlay it`. The formatter is extracted once and shared by both commands.
- **Exit status 1.** `podium sync` already uses 1 for a sync failure (`cmd/podium/main.go:394-397`), and `podium layer reingest` uses it for a drop. The command's other exit statuses are unchanged: 2 for a flag error or an invalid resolved configuration value (`cmd/podium/main.go:298-301`, `:309-312`, `:328-329`), and 1 for a configuration file that cannot be loaded, a sync failure, a watch that fails to start or has a failed cycle, and a failed `--config` target (`cmd/podium/main.go:281-285`, `:432-434`, `:448-450`, `:474-477`, `:521-522`; `docs/consuming/publishing.md:169`). 0 still means no drop and no failure.
- **The rest of the sync proceeds.** Mirroring the ingest cycle, which stores the rest, every non-dropped artifact is materialized, stale-file cleanup runs, and the lock file is written without the dropped artifact. A `--dry-run` reports the drop and exits 1. A `--config` target that drops counts toward the multi-target failure tally. Each `--watch` cycle reports its drops, and a cycle whose drops the watch loop reported counts toward the exit status it returns on interrupt. A cycle still running when the interrupt arrives can finish its writes while the watcher discards its event (`pkg/sync/watch.go:90-95`), so that cycle goes unreported and does not count; the next `podium sync` reports the drop. The same window already applies to a failed cycle, and closing it would need a watcher delivery change that this proposal does not make. §7.3.1's `podium layer watch` carries no exit status, but `podium sync --watch` already carries one, and leaving drops out of it would make one-shot and watch disagree.
- **`--check` reports only where it composes.** `podium sync --check` without `--config` validates `sync.yaml` and composes no layers (§7.5.2), so it never reports a drop. A `kind: workspace` target under `--config --check` resolves its artifact set through a dry run, so it reports the drop and counts as failed. A `kind: marketplace` target under `--check` renders nothing and reports nothing.
- **Server-source sync is unchanged.** The server dropped the artifact at ingest and serves only the lower copy, so the sync has nothing to report and exits 0. The §11 equivalence test therefore compares materialized bytes and leaves exit status to the end-to-end test.
- **`ingest.collision` lives in §7.3.1.** The other `ingest.*` codes are defined there. §6.10 enumerates namespaces and is not edited. The §6.10 matrix cell and its annotated test already exist, and the new drop-policy tests carry the same `// Matrix: §6.10 (ingest.collision)` annotation.
- **The reason names no layer.** The rejection reason already omits the contributing layer, because a reingest response reaches a non-admin layer owner who may not be able to read that layer. SPEC-2 states this beside the §4.6 rejection bullet, which `collisionRejection` already cites.
- **§6.9 gains no row.** §6.9 lists MCP-server failure modes. An overlay replacement is not a failure, and a filesystem-sync drop does not occur in the MCP server.
- **No new configuration surface.** The new exported identifiers are `layer.CollisionCode`, `layer.Collision`, `layer.ExtendsOverlays`, `version.StripPin`, `filesystem.CollisionPolicyDrop`, `filesystem.WalkOptions.OnCollision`, `sync.Result.Dropped`, `sync.RenderResult.Dropped`, and `sync.OverrideResult.Dropped`. Each exists so ingest, `Walk`, sync, and the CLI share one implementation.

## Spec amendment: §1 layered composition

**SPEC-1.** Two anchors in `spec/01-overview.md`, §1.

(a) "What Podium provides", the bullet "**Layered composition with deterministic merge.**" (line 19 at the time of writing). Replace:

> An ordered list of layers (admin-defined, user-defined, and the workspace local overlay) composes per request with explicit precedence and no silent shadowing.

with:

> An ordered list of layers (admin-defined, user-defined, and the workspace local overlay) composes per request with explicit precedence. Registry-side layers admit no silent shadowing, and the per-developer workspace overlay is an exception (§6.4).

The rest of the bullet, from "`extends:` lets a higher-precedence artifact" onward, is unchanged.

(b) The problem list, item 4 "**Layered composition.**" (line 40 at the time of writing). Replace:

> need to compose deterministically with clear precedence and no silent shadowing.

with:

> need to compose deterministically with clear precedence and no silent shadowing between registry-side layers.

## Spec amendment: §4.6 collision rule for registry-side layers

**SPEC-2.** Three anchors in `spec/04-artifact-model.md`, §4.6.

(a) The paragraph after the "Composition order (lowest to highest precedence)" list (line 589 at the time of writing). Replace the first sentence:

> A canonical-ID collision between layers follows the merge semantics for collisions below: the collision is rejected unless the higher-precedence artifact declares `extends:` on the lower-precedence one.

with:

> A canonical-ID collision between registry-side layers (the admin-defined and user-defined layers above) follows the merge semantics for collisions below. The workspace local overlay follows §6.4.

In the same paragraph, replace the closing clause:

> layer 3 is merged in by the MCP server before returning results.

with:

> layer 3 is merged in by the consumer that reads it (§6.4).

The sentence "Resolution of layers 1 and 2 happens at the registry on every ..." up to the semicolon is unchanged.

(b) "Merge semantics for collisions", the first bullet (line 674 at the time of writing). Replace:

> - A collision is rejected at ingest **unless** the higher-precedence artifact declares `extends: <lower-precedence-id>` in frontmatter.

with:

> - Between registry-side layers, the collision is rejected **unless** the higher-precedence artifact declares `extends: <lower-precedence-id>` in frontmatter. The rejected artifact is dropped with `ingest.collision` (§7.3.1), and the other artifact stays in the effective view. At ingest, the dropped artifact is the one the cycle is ingesting (§7.3.1). A filesystem-source `podium sync` drops the higher-precedence artifact (§13.11.3). The rejection reason names the artifact and the `extends:` remedy and does not name the layer that already contributes the ID, because that layer may be one the caller cannot read (§4.7.2).

The second bullet, "When `extends:` is declared, fields merge per the table below.", is unchanged.

(c) The paragraph beginning "To intentionally replace an artifact rather than extend it" (line 681 at the time of writing). Replace:

> Silent shadowing is never permitted.

with:

> Silent shadowing between registry-side layers is never permitted. The workspace local overlay is an exception, and §6.4 states its collision rule.

The first sentence of that paragraph is unchanged. The overlay mechanics are stated only in §6.4, and the sync report, continuation, and exit status only in §13.11.3.

## Spec amendment: §6.4 overlay collision exception

**SPEC-3.** Anchor: `spec/06-mcp-server.md`, §6.4, the paragraph beginning "Format:" (line 209 at the time of writing). Replace:

> Format: same `ARTIFACT.md` (plus `SKILL.md` for skills) and frontmatter as the registry; merge semantics are identical to registry-side layers.

with:

> Format: same `ARTIFACT.md` (plus `SKILL.md` for skills) and frontmatter as the registry.
>
> **Collisions with registry-side artifacts.** The workspace local overlay is an exception to the §4.6 collision rule. An overlay artifact whose canonical ID matches an artifact in the caller's composed registry-side view replaces that artifact in the consumer's view on every consumer (the MCP server, `podium sync`, and the SDKs). This holds whether or not the overlay artifact declares `extends:`: the consumer resolves no `extends:` chain for an overlay artifact and serves it as authored. The collision is neither rejected nor reported. The overlay is per-developer, is never ingested or shared, and exists for the local iteration loop, so a replacement affects only the developer who wrote it. Promoting the artifact to a shared layer subjects it to the §4.6 rule, including `extends:` resolution.

SPEC-3 states no rule for an overlay directory split into layers with a `.registry-config` `multi_layer: true`. §6.4 defines the overlay as one directory, and the consumers disagree on such a directory today: the Go consumers open it through `filesystem.Open` and walk its layers with `CollisionPolicyHighestWins` (`pkg/overlay/overlay.go:57-63`), while the Python and TypeScript SDK overlays walk the overlay root as one tree and key each artifact by its path from that root (`sdks/podium-py/podium/_overlay.py:271-289`, `sdks/podium-ts/src/overlay.ts:336-345`), and neither SDK reads `.registry-config`. Stating a multi-layer rule would assign the SDKs a behavior they do not perform, so it is a non-goal.

The paragraph that follows, "The workspace local overlay is **orthogonal to the registry-side `local` source type**", is unchanged.

## Spec amendment: §7.3.1 ingest.collision

**SPEC-4.** Two anchors in `spec/07-external-integration.md`, §7.3.1.

(a) The **Errors.** paragraph (line 138 at the time of writing). After:

> same-version content conflicts (`ingest.immutable_violation`),

insert:

> cross-layer collisions without a sanctioning `extends:` (`ingest.collision`, §4.6),

The rest of the list is unchanged.

(b) The **Ingest outcome.** paragraph (line 140 at the time of writing). Replace:

> a cross-layer collision and an `extends:` chain that crosses types (§4.6),

with:

> a cross-layer collision (`ingest.collision`) and an `extends:` chain that crosses types (§4.6),

## Spec amendment: §13.11.3 and §13.11.4 filesystem-source collisions

**SPEC-5.** Two anchors in `spec/13-deployment.md`.

(a) §13.11.3 "What's Available", after the paragraph beginning "The composer, parsers, glob resolver, `extends:` resolver, and harness adapters used here" and before "What's **not available** in filesystem source:" (lines 337 and 339 at the time of writing). Insert:

> **Layer collisions.** Two registry layer subdirectories that contribute the same canonical ID follow the §4.6 collision rule. When the higher-precedence artifact declares no `extends:` on that ID, sync drops it and keeps the lower-precedence artifact, which is the artifact a server started fresh with `--layer-path` on the same directory serves. A dropped artifact takes no part in `extends:` resolution. Sync names each dropped artifact on standard error with its identifier, the code `ingest.collision`, and the reason. It still materializes every other artifact, runs the stale-file cleanup, and writes the lock file without the dropped artifact. `podium sync` exits 1 when it dropped at least one artifact, and so does `podium sync --dry-run`. Under `--config`, a target that dropped an artifact counts as a failed target; this includes a `kind: workspace` target under `--check`, which resolves the target's artifact set without writing it. A target's workflow runs on the materialized output as it does on a sync that dropped nothing, and the target then counts as failed. `podium sync --check` without `--config` validates the config only and composes no layers (§7.5.2), and a `kind: marketplace` target under `--check` renders nothing, so neither reports a drop. `podium sync override` re-materializes the target through the same composition when a registry is configured, so it names each dropped artifact the same way and exits 1 when it dropped at least one; `podium sync override --dry-run` writes nothing, runs no re-materialization, and reports no dropped artifact. The workspace overlay is merged after this composition and follows §6.4.

(b) §13.11.4 "Watch Mode" (line 357 at the time of writing). Append to the paragraph:

> Each cycle reports the artifacts it dropped under §13.11.3. When the process is interrupted, it exits 1 if any cycle it reported failed or dropped an artifact, and 0 otherwise. A cycle still running when the interrupt arrives can finish its writes without a report, and the next `podium sync` reports its drops.

§7.5 is not edited, because its composition paragraph already defers filesystem-specific behavior to §13.11.

## Spec amendment: §11 overlay and equivalence tests

**SPEC-6.** Two anchors in `spec/11-verification.md`. The developer-host integration test bullet (line 7) is unchanged, because "the workspace local overlay overrides registry-side artifacts" stays true under the §6.4 exception.

(a) The "Workspace local overlay precedence test" bullet (line 15 at the time of writing). Replace:

> confirm the workspace local overlay overrides every registry-side layer for a synthetic conflicting artifact, and that removing the overlay file restores the registry-side artifact.

with:

> confirm the workspace local overlay overrides every registry-side layer for a synthetic conflicting artifact, both when the overlay artifact declares no `extends:` and when it declares `extends:` on the registry-side ID, with the overlay artifact served as authored in both cases (the §6.4 exception), and that removing the overlay file restores the registry-side artifact.

(b) The "Filesystem ↔ server equivalence test" bullet (line 33 at the time of writing). After the sentence ending "which the filesystem consumer computes and the server consumer records from the registry." and before "Confirms that the shared Go library", insert:

> A second directory carries two layers that contribute one canonical ID with no `extends:`, together with an artifact that collides with nothing. Both consumers materialize the lower-precedence copy and the non-colliding artifact, and the filesystem consumer reports `ingest.collision` for the higher-precedence copy (§4.6, §13.11.3).

The exit status is pinned by the end-to-end test for §13.11.3, because the equivalence test compares materialized output.

## Proposed solution

### CODE-1. One shared implementation of the collision rule

**`pkg/version/version.go`.** Add `StripPin(ref string) string`, documented as returning the canonical ID portion of a §4.7.6 reference by dropping any `@<semver>`, `@<semver>.x`, or `@sha256:<hash>` suffix. Replace the three copies with it: `stripPin` in `pkg/registry/ingest/ingest.go`, `stripPin` in `pkg/registry/filesystem/extends.go`, and `stripVersionPin` in `pkg/lint/body_rules.go`. Move `TestStripPin` from `pkg/registry/ingest/helpers_internal_test.go` to `pkg/version/version_test.go` as `TestStripPin`, annotated `// Spec: §4.7.6`, covering a bare ID, `@1.0.0`, `@1.2.x`, `@sha256:abc`, and an empty reference. The move lands in this step, because deleting ingest's `stripPin` breaks the original test's compile.

**`pkg/layer/collision.go` (new).**

```go
// CollisionCode is the §7.3.1 code for a §4.6 cross-layer collision that no
// extends: declaration sanctions.
const CollisionCode = "ingest.collision"

// Collision records one artifact dropped under the §4.6 collision rule.
type Collision struct {
	// ArtifactID is the canonical ID two registry-side layers contribute.
	ArtifactID string
}

// Reason returns the rejection reason shared by ingest and filesystem-source
// sync. It names the artifact and the extends: remedy and never the
// contributing layer, because that layer may be one the caller cannot read.
//
// Spec: §4.6
func (c Collision) Reason() string { ... }

// ExtendsOverlays reports whether an extends: reference sanctions an overlay
// of id, comparing the pin-stripped reference.
//
// Spec: §4.6
func ExtendsOverlays(extendsRef, id string) bool {
	return extendsRef != "" && version.StripPin(extendsRef) == id
}
```

`Reason()` returns exactly the text `collisionRejection` produces today: `cross-layer collision: "<id>" is already contributed by another layer; declare extends: <id> to overlay it`.

**`pkg/registry/ingest/ingest.go`.** The overlay check uses `layer.ExtendsOverlays(rec.Artifact.Extends, mr.ArtifactID)` and `layer.ExtendsOverlays(ex.ExtendsPin, mr.ArtifactID)`. `collisionRejection` builds its `RejectedArtifact` from `layer.CollisionCode` and `layer.Collision{ArtifactID: id}.Reason()`, keeps its server-side `log.Printf` line naming the existing layer, and keeps its `// Spec: §4.6` citation. Ingest behavior is unchanged, and `test/integration/reingest_collision_reason_test.go` and `TestIngest_CrossLayerCollisionRejected` must still pass unchanged.

**`pkg/registry/filesystem/walk.go`.** Replace `declaresExtendsTo` with `layer.ExtendsOverlays`, and build the `CollisionPolicyError` message with `layer.CollisionCode`. The message text is otherwise unchanged.

The requirement this step serves is that ingest and filesystem-source sync emit the same code and reason (§7.3.1, §13.11.3), together with removing the duplicated pin helper.

### CODE-2. `CollisionPolicyDrop` in `filesystem.Walk`

**`pkg/layer/collision.go`.** Add two fields to `Collision`, consumed by the `OnCollision` callback and deliberately omitted from `Reason()`:

```go
	// Layer is the layer whose contribution was dropped.
	Layer string
	// ExistingLayer is the layer whose contribution was kept.
	ExistingLayer string
```

**`pkg/registry/filesystem/walk.go`.**

- Add `CollisionPolicyDrop` to the `CollisionPolicy` enum, documented as: "keeps the lower-precedence record, drops a higher-precedence one that declares no `extends:` on the ID, and reports it through `OnCollision` (§4.6, §13.11.3). Used by filesystem-source sync."
- Add `OnCollision func(layer.Collision)` to `WalkOptions`.
- At the top of `Walk`, return `errors.New("filesystem: CollisionPolicyDrop requires OnCollision")` when the policy is `CollisionPolicyDrop` and `OnCollision` is nil.
- Extract the dedup loop into a helper so `Walk` stays under the 50-line guideline. The helper returns the deduped records and the `kept` records in layer order:

  ```go
  if !layer.ExtendsOverlays(extendsOf(rec), rec.ID) {
  	switch {
  	case collisionError:
  		return nil, nil, fmt.Errorf("%s: artifact %q present in layers %q and %q",
  			layer.CollisionCode, rec.ID, deduped[idx].Layer.ID, rec.Layer.ID)
  	case opts.CollisionPolicy == CollisionPolicyDrop:
  		opts.OnCollision(layer.Collision{ArtifactID: rec.ID, Layer: rec.Layer.ID, ExistingLayer: deduped[idx].Layer.ID})
  		continue // the dropped record joins neither deduped nor kept
  	}
  }
  deduped[idx] = rec
  kept = append(kept, rec)
  ```

  `extendsOf` returns `rec.Artifact.Extends`, or the empty string for a nil `Artifact`. Every record that is not dropped is appended to `kept`, including the first contribution of each ID.
- Pass `kept` to `resolveExtends` in place of `all`, so a dropped record is never a same-ID or different-ID parent. Under `CollisionPolicyHighestWins` and `CollisionPolicyError`, `kept` equals `all`.
- Reword the `Walk` doc comment and the `CollisionPolicyHighestWins` comment to say that `CollisionPolicyHighestWins` is used by the workspace overlay's walk of its own directory (`pkg/overlay/overlay.go`, §6.4) and `CollisionPolicyDrop` by filesystem-source sync.

### CODE-3. Drop, report, and continue in `pkg/sync`

- **`pkg/sync/sync.go`, `filesystemRecords`.** Walk with `CollisionPolicyDrop` and an `OnCollision` callback that appends to a local `[]layer.Collision`, and return those drops with the records. Update the `// spec: §13.11.3` comment to cite §4.6 for the drop.
- **`resolveRecords`.** Return `([]materialRecord, []layer.Collision, error)`. `fetchServerRecords` contributes no drops. `applyOverlay` is unchanged, and its doc comment changes "matching the highest-precedence semantics of §4.6" to "the §6.4 overlay exception: an overlay record replaces the base record whether or not it declares `extends:`".
- **`Result`.** Add `Dropped []layer.Collision`, documented in the style of `Skipped`: "Dropped lists the artifacts a filesystem-source composition dropped under the §4.6 collision rule (§13.11.3). Callers render each entry with `layer.CollisionCode` and `Reason()`; a server source never reports one." `Run` copies the drops into `Result.Dropped` and continues with materialization, stale-file cleanup, and the lock write. The `DryRun` return carries them as well.
- **`Run` doc comment.** Replace "applies layer composition with CollisionPolicyHighestWins (per §4.6)" with "applies layer composition with CollisionPolicyDrop (§4.6, §13.11.3), recording each dropped artifact on Result.Dropped and materializing the rest".
- **`pkg/sync/records.go`, `FetchRecords`.** Return the drops alongside the records.
- **`pkg/sync/render.go`.** Add `Dropped []layer.Collision` to `RenderResult`, filled from `FetchRecords`. `RunMarketplace` adds no field, because callers read `res.Render.Dropped`.
- **`pkg/sync/effective_view.go`, `ResolveEffectiveView`.** Discard the drops, with a comment saying a dropped artifact is outside the effective view, as it is on the server, and that the override TUI's apply step reports them through `OverrideResult.Dropped`.
- **`pkg/sync/override.go`, `OverrideResult`.** Add `Dropped []layer.Collision`, documented as "Dropped lists the artifacts the re-materialization dropped under the §4.6 collision rule (§13.11.3); empty under DryRun, without a RegistryPath, and for a server source." `Override` is the only `Run` caller in `pkg/sync` that discards the `*Result` (`pkg/sync/override.go:146-165`). It keeps that `*Result` and sets `Dropped` from `Result.Dropped`. That is the one site that sets the field. It is never cleared, because each `Override` call returns a new `OverrideResult`. When `opts.DryRun` is set or `opts.RegistryPath` is empty, `Override` calls no `Run`, composes no layers, and leaves `Dropped` nil. The toggles and the lock are written before and by the inner `Run` as today, so a drop does not roll back the override.
- `pkg/sync/watch.go` is unchanged, because `WatchEvent` carries `*Result`.
- **Existing `pkg/sync` tests, in the same step.** Update the call sites the signature changes break: `pkg/sync/lock_content_hash_test.go:209` (`filesystemRecords`), `pkg/sync/render_test.go:186`, and `pkg/sync/records_test.go:86` (`FetchRecords`) take the added return value and discard it. Rewrite `TestRun_HigherLayerWinsOnCollision` (`pkg/sync/sync_test.go:265-298`) so the `personal/x` fixture declares `extends: x`; assert the materialized `x/ARTIFACT.md` carries the `personal` description and body and `Result.Dropped` is empty. Replace its comment with `// Spec: §4.6, §13.11.3 — a higher-precedence layer that declares extends: on the colliding ID merges over the lower one, and sync drops nothing.` The unsanctioned case is covered by `TestRun_FilesystemCollisionDropsAndContinues` (TEST-2).

### CODE-4. Report and exit in the `podium sync` CLI

- **Shared formatter.** Extract `printRejected(w io.Writer, id, code, reason string)` writing `rejected: <id> (<code>): <reason>\n`, and use it at the `report.Rejected` loop in `cmd/podium/layer.go`. Add `reportDropped(w io.Writer, dropped []layer.Collision) bool`, which calls `printRejected` with `layer.CollisionCode` and `c.Reason()` for each entry and returns whether it printed anything.
- **One-shot (`cmd/podium/main.go`).** After `printJSON` or `printHuman`, `if reportDropped(os.Stderr, res.Dropped) { return 1 }`. This covers `--dry-run`. Under `--json` the drops stay on standard error, so the standard-output envelope stays valid, which matches the `marketplaceStdout` convention.
- **`runWatchLoop`.** After printing each event's result, `if reportDropped(os.Stderr, ev.Result.Dropped) { failures++ }`. The existing `failures > 0` return then covers drops. The loop counts only the events it receives (`cmd/podium/main.go:425-452`). The watcher's `emit` selects between the send and `ctx.Done()` (`pkg/sync/watch.go:90-95`), and `Run` has already written the files and the lock before `emit` runs (`pkg/sync/watch.go:116-118`, `pkg/sync/watch_fsnotify.go:83-84`), so an interrupt during a dropping cycle can discard that cycle's event. SPEC-5 (b) therefore counts only reported cycles, and `pkg/sync/watch.go` keeps its `emit`.
- **`runWorkspaceTarget`.** Call `reportDropped` right after `sync.Run` returns, before the `if check { return nil }` early return, and remember the result. Return `errDropped` at both exits (the check path and the end of the function, after the workflow phases) when a drop was reported. `TestPublishing_WorkspaceCollisionPublishesThenFails` (TEST-3) pins that the publish phase runs before the `errDropped` return. Declare `var errDropped = errors.New("dropped at least one artifact (ingest.collision)")` in `cmd/podium/main.go`. `runMultiTargetSync` already prints `target <id>: <err>` and counts the failure.
- **`runMarketplaceTarget`.** After `RunMarketplace` returns, report `res.Render.Dropped` when `res.Render` is non-nil and return `errDropped` when anything printed. Under `--check` `res.Render` is nil, so nothing is reported. The publish phase has already run inside `RunMarketplace` by then; changing that is OQ-1 and would need a `pkg/sync` change.
- **`podium sync override`.** In `syncOverrideCmd` (`cmd/podium/main.go:727-782`) and `runSyncOverrideInteractive` (`cmd/podium/interactive.go:297-340`), after the toggle lines print, `if reportDropped(os.Stderr, res.Dropped) { return 1 }` (`ovr.Dropped` in the interactive function). These are the only two callers of `sync.Override` outside `pkg/sync`. If either call is missing, that path exits 0 with no report. The TEST-3 override subtest observes the batch path through the binary, and `TestRunSyncOverrideInteractive_CollisionExits1` (TEST-3) observes the interactive path.
- **`runSyncCheck`.** The function is unchanged, because it composes no layers.
- **Lint comment.** In the `podium lint` handler, replace "sync keeps CollisionPolicyHighestWins because it materializes the caller's composed effective view rather than validating it." with "sync uses CollisionPolicyDrop, which drops and reports an unsanctioned collision and materializes the rest (§13.11.3)."

### CODE-5. Correct the `layer.Compose` comments

`pkg/layer/composer.go`. Replace the `Compose` doc comment with:

```go
// Compose returns, for each canonical ID, the candidate from the
// highest-precedence layer in layers. It applies neither the §4.6 collision
// rule nor extends: resolution. Ingest and filesystem.Walk enforce the
// collision rule on their own inputs, and the consumer applies the workspace
// overlay's exception (§6.4). No production path calls Compose.
```

The function body is unchanged. In `pkg/layer/composer_test.go`, replace the comment above `TestCompose_HighestPrecedenceWins` with `// Spec: §4.6 — Compose keeps the highest-precedence candidate per canonical ID, following the §4.6 composition order.`

Run `gofmt`, `goimports`, `go build ./...`, and `make lint` after each code step.

## Edge cases and accepted failure modes

| Case | Outcome | Governing text; documented in |
|:--|:--|:--|
| Filesystem-source sync, two registry layers contribute X, the higher declares no `extends:` | The lower copy is materialized; `rejected: X (ingest.collision): <reason>` on standard error; the rest materialized; lock omits the dropped copy; exit 1 | §4.6 (SPEC-2), §13.11.3 (SPEC-5); `docs/deployment/layers.md`, `docs/reference/cli.md` (DOC-1) |
| The higher copy declares `extends: X` | Merged per the §4.6 field table; no drop; exit 0 | §4.6; `docs/authoring/extends.md` |
| The higher copy declares `extends:` on a different ID | Dropped as an unsanctioned collision; exit 1 | §4.6 (SPEC-2); `docs/authoring/extends.md` (DOC-1) |
| Three layers: L1:X, L2:X without `extends:`, L3:X with `extends: X` | L2 dropped; L3 merged over L1; exit 1 | §13.11.3 "A dropped artifact takes no part in `extends:` resolution" (SPEC-5); `docs/deployment/layers.md` (DOC-1) |
| Three layers: L1:X, L2:X with `extends: X`, L3:X without `extends:` | Filesystem sync drops L3 and serves L2 merged over L1. A server ingesting the layers admits L3, because an existing record declares `extends:` on X (accepted and deferred) | Filesystem side: §13.11.3 (SPEC-5). The server side contradicts §4.6 as amended and is a non-goal; no sentence is staged, because documenting it would sanction it |
| A later reingest or reorder on a server changes which copy the server keeps | The server may serve a different copy than filesystem sync (accepted and deferred) | §4.6 "At ingest, the dropped artifact is the one the cycle is ingesting" (SPEC-2); §13.11.3 limits parity to a server "started fresh" (SPEC-5) |
| `podium sync --dry-run` on a colliding registry | Report and exit 1; nothing written | §13.11.3 (SPEC-5); `docs/reference/cli.md` (DOC-1) |
| `podium sync --check` without `--config` | Validates `sync.yaml` only; no drop reported; exit 0 on a valid config | §7.5.2, §13.11.3 (SPEC-5); `docs/reference/cli.md` (DOC-1) |
| `--config --check` with a colliding `kind: workspace` target | Drop reported; target counts as failed; exit 1; nothing written | §13.11.3 (SPEC-5); `docs/reference/cli.md` (DOC-1) |
| `--config --check` with a `kind: marketplace` target | Renders nothing; no drop reported | §7.8 execution semantics ("`--check` validates the config only"), §13.11.3 (SPEC-5); `docs/reference/cli.md` (DOC-1) |
| `--config` target with a workflow and a drop | The publish phase runs on the output without the dropped artifact; the target then counts as failed; exit 1 (default pending OQ-1) | §13.11.3 (SPEC-5); `docs/reference/cli.md` (DOC-1) |
| `podium sync --watch`, one cycle drops and a later cycle is clean | Each dropping cycle prints its report; exit 1 on interrupt | §13.11.4 (SPEC-5); `docs/reference/cli.md` (DOC-1) |
| `podium sync --watch` interrupted while a dropping cycle runs | The cycle can finish its writes while the watcher discards its event: no report, and that cycle does not count toward the exit status; the next `podium sync` reports the drop (accepted) | §13.11.4 "A cycle still running when the interrupt arrives" (SPEC-5); `docs/reference/cli.md` (DOC-1) |
| `podium sync --json` with a drop | Standard-output envelope unchanged; drops on standard error; exit 1 | §13.11.3 "names each dropped artifact on standard error" (SPEC-5); `docs/reference/cli.md` (DOC-1) |
| Server-source `podium sync` against a server holding the lower copy | Lower copy materialized; no report; exit 0 | §4.6, §7.3.1 (the drop happened at ingest); `docs/deployment/layers.md` (DOC-1) |
| Upgrade: a target previously materialized the higher copy | The next sync overwrites the path with the lower copy and exits 1 | §13.11.3 (SPEC-5); `CHANGELOG.md` (CL-1) |
| `podium sync override --add <id>` (or `--remove`, `--reset`) over a colliding filesystem registry | Toggles recorded, except that an `--add` of an already materialized ID stays the §7.5.5 no-op with a warning; when a registry resolves, the re-materialization keeps the lower copy; `rejected:` line on standard error; exit 1 | §13.11.3 "`podium sync override` re-materializes the target through the same composition" (SPEC-5); `docs/reference/cli.md` `podium sync override` (DOC-1) |
| `podium sync override --dry-run` over a colliding filesystem registry | No re-materialization and no report; exit status as today. The TUI form still composes the layers to build its checklist and discards the drops (`ResolveEffectiveView`, CODE-3) | §13.11.3 (SPEC-5); `docs/reference/cli.md` (DOC-1) |
| The sync override TUI over a colliding filesystem registry | The dropped artifact is absent from the checklist, which lists the effective view (`ResolveEffectiveView` discards the drops). Applying a toggle re-materializes, prints the `rejected:` line, and exits 1; quitting or applying no change prints no report and exits 0 as today | §13.11.3 (SPEC-5); `docs/reference/cli.md` `podium sync override` (DOC-1) |
| Overlay artifact without `extends:` collides with a registry-side artifact | Overlay copy served or materialized; no report; exit unaffected | §6.4 (SPEC-3); `docs/deployment/layers.md`, `docs/authoring/extends.md` (DOC-1) |
| Overlay artifact declares `extends:` on the registry-side ID, or on an ID that does not resolve | Overlay copy served as authored, with its `extends:` key unresolved; no error | §6.4 (SPEC-3); `docs/authoring/extends.md` "Workspace overlay" (DOC-1) |
| An overlay directory with `multi_layer: true` holds a collision among its own layers | Unchanged and unspecified: the Go consumers keep the highest-precedence overlay copy, and the SDKs key each copy by its path from the overlay root (accepted and deferred) | No spec text, because §6.4 defines a single-directory overlay (SPEC-3 note; Non-goals); not documented |
| An overlay artifact is promoted to a shared layer while the lower layer still holds the ID | Ingest rejects it with `ingest.collision` unless it declares `extends:` | §4.6, §6.4 (SPEC-3); `docs/authoring/extends.md` (DOC-1) |
| `podium lint` on a colliding registry | Unchanged: the walk aborts with `ingest.collision`; exit 1 | §4.6; `docs/deployment/layers.md` |

## Testing

**TEST-1 · unit, `pkg/layer/collision_test.go` (new) and `pkg/registry/filesystem/walk_test.go`.** Lands in S9.

- `pkg/layer/collision_test.go`, annotated `// Spec: §4.6`: `TestExtendsOverlays` as a table over a matching reference, a pinned matching reference (`X@1.2.x`), a different-ID reference, and an empty reference. `TestCollisionReason` asserts the text contains the artifact ID and `declare extends: <id>`, and contains neither the `Layer` nor the `ExistingLayer` value set on the struct.
- `walk_test.go`, each test annotated `// Spec: §4.6` and `// Matrix: §6.10 (ingest.collision)`:
  - `TestWalk_DropPolicy`, table-driven, with a non-colliding artifact in every fixture. Cases: two layers with no `extends:` (lower kept, one `OnCollision` call with `ArtifactID`, `Layer`, and `ExistingLayer`, the other artifact present); L1:X, L2:X, and L3:X with no `extends:` (two drops, both with `ExistingLayer` L1, L1 kept); a higher copy with `extends: X` (no drop, higher kept); a higher copy with `extends: other/id` (dropped, mirroring `TestWalk_CollisionExtendsOtherIDStillFails`).
  - `TestWalk_DropPolicyDroppedRecordIsNotAParent`: L1:X and L2:X with no `extends:`, L3:X with `extends: X`, and `ResolveExtends` set. The result carries L3's description and the union of L1's and L3's tags, and none of L2's distinct tags or description. This fails if the resolver receives the unpruned record set.
  - `TestWalk_DropPolicyRequiresCallback`: a nil `OnCollision` returns an error and no records.
- Keep `TestWalk_HighestWinsKeepsTopLayer`. Replace its comment with `// CollisionPolicyHighestWins keeps the highest-precedence record for each ID without checking extends:. The workspace overlay's walk of its own directory (pkg/overlay) uses it; §6.4 defines no multi-layer overlay, so this pins the policy rather than a spec rule.` The test keeps no `// Spec:` line, because no spec section states this behavior after SPEC-2 and SPEC-3.
- The `version.StripPin` test lands with CODE-1 in S7.

**TEST-2 · integration, `pkg/sync/sync_collision_test.go` (new).** Lands in S11. Each test is annotated `// Spec: §4.6`, `// Spec: §13.11.3`, and `// Matrix: §6.10 (ingest.collision)`.

- `TestRun_FilesystemCollisionDropsAndContinues`: a two-layer colliding registry plus one non-colliding artifact. `Run` returns no error. The lower copy and the extra artifact are on disk. `.podium/sync.lock` lists those two and not the dropped copy. `len(Result.Dropped) == 1`.
- `TestRun_FilesystemCollisionDryRunWritesNothing`: the same fixture with `DryRun` returns the same `Dropped` entry, and neither the target directory nor the lock is created.
- `TestRun_FilesystemCollisionResolvedByExtends`: a first run over the colliding fixture, then `extends:` added to the higher copy and a second run. The second run reports no `Dropped`, the materialized artifact is the merge, and stale-file cleanup leaves no leftover file from the first run.
- `TestRender_FilesystemCollisionReportsDropped`: a marketplace `Render` over the same fixture fills `RenderResult.Dropped` with the one collision. This covers the `FetchRecords` path.
- `TestOverride_FilesystemCollisionReportsDropped`: a first `Run` over the colliding fixture with `Scope.Include` set to the colliding ID, so the non-colliding artifact is outside the materialized set and the add is a real toggle rather than the §7.5.5 redundant-add no-op (`pkg/sync/override.go`). `Override` with `Add` naming the non-colliding artifact and `RegistryPath` set returns `OverrideResult.Dropped` with the one collision and no redundant-add warning; the lock's `toggles.add` lists the artifact, the artifact is on disk, and the lower copy stays on disk. The same call with `DryRun` returns a nil `Dropped`. The drop is reported under the narrowed scope because `filesystemRecords` walks every layer before the scope filter applies.
- No watch-cycle unit test is added: `WatchEvent.Result` is `Run`'s `*Result`, and TEST-3 covers the watch exit status through the binary.

**TEST-3 · e2e and unit, `test/e2e/filesystem_sync_test.go`, `test/e2e/core_concepts_test.go`, `test/e2e/publishing_test.go`, and `cmd/podium/interactive_test.go`.** Lands in S10 with CODE-3 and CODE-4.

- Replace `TestFilesystemSync_CollisionHighestWinsLaterLayerWins` with `TestFilesystemSync_CollisionDropsHigherLayer`, annotated `// Spec: §13.11.3` and `// Matrix: §6.10 (ingest.collision)`. The fixture holds `base-layer/shared/note` and `override-layer/shared/note`, both with no `extends:`, plus `base-layer/shared/other`. Assert exit 1; standard error contains `rejected: shared/note (ingest.collision): cross-layer collision` and `declare extends: shared/note`; `target/shared/note` holds `from-base`; `target/shared/other` is materialized; `sync.lock` lists `shared/note` and `shared/other`.
- Subtests on the same fixture:
  - `--dry-run`: exit 1, the same standard-error line, and neither files nor `sync.lock` in the target.
  - `--json`: exit 1, standard output parses as the envelope, and the `rejected:` line is on standard error only.
  - `--config` with one `kind: workspace` target: exit 1, `target <id>:` failure line, and the target is synced.
  - `--config --check` with the same target: exit 1 and nothing written.
  - `--check` without `--config` and a valid `sync.yaml`: exit 0 and no `rejected:` line.
  - `--watch`: reuse `startWatch`, poll `w.log()` (which captures standard error, `test/e2e/helpers_test.go:475-500`) until it contains `rejected: shared/note (ingest.collision):`, then call `w.stop(t)` and assert exit 1. Do not wait on the materialized file with `pollFile`: the file exists once `Run` writes it, before the watcher emits the event (`pkg/sync/sync.go`, `pkg/sync/watch.go:90-95`), so a SIGINT sent then can discard the event and the watcher exits 0. `runWatchLoop` prints the `rejected:` line only after it has received and counted the event, so waiting on it removes the race and also pins the SPEC-5 (b) per-cycle report. Honor the platform skips of `TestFilesystemSync_WatchExits0OnSIGINT`.
  - `sync override --add shared/other --registry <fixture>` after a first sync into the target scoped with `--include shared/note`: exit 1, the same `rejected:` line, `target/shared/note` holds `from-base`, `target/shared/other` is materialized, and the lock's `toggles.add` lists `shared/other`. The `--include` scope keeps `shared/other` out of the first sync's lock, because `--add` of an already materialized ID is a §7.5.5 no-op that records no toggle, as `TestFilesystemSync_OverrideAddRecordsInLock` notes. The `--registry` flag is required because override re-materializes only when a registry resolves (`resolveOverrideRegistry` in `cmd/podium/main.go`, and the `opts.RegistryPath != ""` guard in `pkg/sync/override.go`). `sync override --add shared/other --registry <fixture> --dry-run` on a fresh target with the same scoped first sync: no `rejected:` line and the existing exit status.
- `cmd/podium/interactive_test.go`: add `TestRunSyncOverrideInteractive_CollisionExits1`, annotated `// Spec: §13.11.3` and `// Matrix: §6.10 (ingest.collision)`, modeled on `TestRunSyncOverrideInteractive_AddsAndMaterializes`. Over the colliding fixture, a scripted add-and-save makes `runSyncOverrideInteractive` return 1.
- `test/e2e/publishing_test.go`, each test annotated `// Spec: §13.11.3` and `// Matrix: §6.10 (ingest.collision)`, using the file's existing marketplace config, workspace workflow config, and local bare-remote helpers over a colliding filesystem registry:
  - `TestPublishing_MarketplaceTargetCollisionFails`: `podium sync --config` with one `kind: marketplace` target exits 1; standard error carries the `rejected: <id> (ingest.collision):` line and the `target <id>:` failure line; the rendered output holds the lower copy and no content from the dropped copy.
  - `TestPublishing_CheckConfigMarketplaceCollisionSilent`: the same config under `--config --check` prints no `rejected:` line and exits 0.
  - `TestPublishing_MarketplaceCollisionPublishesThenFails`: a target whose publish command writes a marker file runs over the colliding registry; the marker exists, and the run exits 1. This pins the staged OQ-1 default.
  - `TestPublishing_WorkspaceCollisionPublishesThenFails`: `podium sync --config` over a config written by `writeSyncConfigWorkspaceWorkflow` (`test/e2e/publishing_test.go:937-963`, target ID `claude-workspace`) whose registry is the colliding filesystem registry. Assert that the `publish-ran` marker exists, that standard error carries the `rejected: <id> (ingest.collision):` line and the `target claude-workspace:` failure line, that the target holds the lower copy, and that the command exits 1. `runWorkspaceTarget` is a separate code path from the marketplace pipeline, and its publish phase runs at `cmd/podium/main.go:571-580` after `sync.Run`, so this test fails an implementation that returns `errDropped` before the publish phase. It is modeled on `TestPublishing_WorkspaceTargetRunsWorkflow` (`test/e2e/publishing_test.go:534`) and pins the staged OQ-1 default for `kind: workspace`.
  - **IMPLEMENTOR'S CHOICE:** the plugin scope layout of the colliding fixture. Constraint: the colliding canonical ID is inside a plugin scope the target renders, so the render composes it.
  - Extend the file's header comment to name the collision behavior.
- Update the comment on `TestFilesystemSync_CollisionRaisedByLint` to `(lint uses CollisionPolicyDefault; sync uses CollisionPolicyDrop)`.
- Rewrite `TestCoreConcept_LayerPrecedenceOverride` so the high-layer artifact uses an inline frontmatter string with `extends: shared/glossary`, because `contextArtifact` takes no `extends` argument. Assert `res.Exit == 0` and that `high-description` wins through the merge. Keep the test name.
- Rewrite `TestCoreConcept_LayerOrderAlphabeticalDefault` the same way, with `b-layer` declaring `extends: shared/item`. Assert `res.Exit == 0` and `from-b`.
- No `tools/doccov/manifest.yaml` entry changes. The manifest maps pages to slugs (`docs/consuming/publishing.md` to `D-marketplace-sync` at `tools/doccov/manifest.yaml:41-42`), and DOC-1 adds no runnable example.

**TEST-4 · integration, `test/integration/sync_equivalence_test.go`.** Lands in S12.

- Leave `TestSyncEquivalence_FilesystemVsServerByteIdentical` and `testdata/registries/reference` unchanged. The reference registry is shared with the conformance suite, and a deliberate collision there would make every sync against it report a drop and fail `podium lint`.
- Add `TestSyncEquivalence_LayerCollisionIsByteIdentical`, annotated `// Spec: §11`, `// Spec: §2.2`, `// Spec: §13.11.3`, and `// Matrix: §6.10 (ingest.collision)`, with a helper `crossLayerCollisionRegistry(t)` modeled on `collidingRegistry`. The helper writes a `t.TempDir()` with `.registry-config` set to `multi_layer: true` and `layer_order:` `org-defaults`, `team-finance`, a `visibility: public: true` `.layer-config` in each layer, `org-defaults/shared/clash/ARTIFACT.md` (`from-org`), `team-finance/shared/clash/ARTIFACT.md` (`from-team`, no `extends:`), and one non-colliding artifact.
- For adapters `none` and `claude-code`, run filesystem `sync.Run` and server `sync.Run` (`server.NewFromFilesystem` behind `httptest`). Assert `assertTreesEqual`, `artifactKeys` equality, and `assertLockArtifactsEqual`; `shared/clash` holds `from-org` in both outputs; the non-colliding artifact is materialized; the filesystem `Result.Dropped` holds exactly one `layer.Collision` with `ArtifactID` `shared/clash`, `Layer` `team-finance`, and `ExistingLayer` `org-defaults`; the server `Result.Dropped` is empty.

**TEST-5 · unit and e2e, overlay exception.** Lands in S14.

- `pkg/sync/overlay_test.go`: add `TestRun_OverlayWithExtendsReplacesWholesale`, annotated `// Spec: §6.4`. A filesystem registry holds X with description `base` and tag `a`; the overlay holds X with `extends: X`, description `overlay`, and tag `b`. Assert `Run` succeeds, the materialized `ARTIFACT.md` equals the overlay's authored bytes (including the `extends:` line), tag `a` is absent, and `Result.Dropped` is empty. Add a second case in which the overlay declares `extends: missing/parent`, and assert the same wholesale replacement and no error.
- `test/e2e/harness_materialization_test.go`: add `TestHarness_MCPOverlayWithExtendsReplaces`, annotated `// Spec: §6.4` and `// Spec: §11`, next to `TestHarness_MCPOverlayOverridesRegistry`. Drive the `podium-mcp` binary with `PODIUM_OVERLAY_PATH` over an overlay rule that declares `extends: my-rule`, and assert `load_artifact` returns the overlay body and frontmatter and no error envelope.
- Add a comment naming the §6.4 exception to `TestRun_OverlayOverridesRegistry`, `TestHarness_MCPOverlayOverridesRegistry`, and `TestLoadArtifactFromOverlay_ReturnsLayerOverlay`. Add `§6.4` to the `// Spec:` line of `TestLoadArtifactFromOverlay_ExtendsChildServesTheAuthoredContentHash`, whose body is unchanged. In `pkg/sync/server_auth_overlay_test.go`, normalize the lowercase `spec:` tag on `TestRun_ServerSource_OverlayOverridesServer` to `Spec:` and add the same comment.
- The SDK overlay tests are unchanged, because the SDKs already replace wholesale for a single-directory overlay and SPEC-3 states that behavior. SPEC-3 states no multi-layer overlay rule, so no SDK test is added for one.

Run `go test ./pkg/version/ ./pkg/layer/ ./pkg/registry/... ./pkg/sync/ ./cmd/... ./test/integration/`, `go test ./test/e2e/ -run 'FilesystemSync_Collision|CoreConcept_Layer|MCPOverlay|Publishing_.*Collision'`, the cross-package coverage profile from `.claude/rules/test-coverage.md`, and `make coverage-gate`. Check for `SKIP` in the end-to-end output before treating the watch subtest as verified on macOS.

## Manual validation

**MV-1.** Add scenario S77 to `test/manual-validation.md` after S76, following the existing conventions. Lands in S17.

~~~~markdown
## S77: A filesystem sync that drops a colliding artifact fails

**Goal.** Validate that `podium sync` against a filesystem registry drops the
higher-precedence copy of an unsanctioned cross-layer collision, names it on
standard error, materializes the rest, and exits 1; that a standalone server
over the same directory serves the same copy; and that a workspace overlay
artifact replaces the registry-side artifact silently even when it declares
`extends:`.

**Covers.** The §4.6 collision rule for registry-side layers, the §13.11.3
report and exit status, the §11 filesystem-to-server equivalence requirement
on a colliding directory, and the §6.4 overlay exception.

**Why by hand.** The end-to-end suite reads the exit code and the streams
through the harness. What it does not read is the operator's terminal: that
the materialized file a developer opens holds the lower layer's text, that the
rejection names the remedy in words an author can act on, and that `$?` is
what a CI step gates on.

**Prerequisites.** A built `podium` binary on `PATH`.

**Steps.**

1. Run the isolation block from "Per-scenario isolation" above, then build a
   two-layer registry in which both layers contribute `shared/clash` with no
   `extends:`, and the lower layer also holds `shared/other`.

   ```bash
   mkdir -p "$WORK/reg/org-defaults/shared/clash" "$WORK/reg/org-defaults/shared/other" \
     "$WORK/reg/team-finance/shared/clash"
   printf 'multi_layer: true\nlayer_order:\n  - org-defaults\n  - team-finance\n' \
     > "$WORK/reg/.registry-config"
   printf 'visibility:\n  public: true\n' > "$WORK/reg/org-defaults/.layer-config"
   printf 'visibility:\n  public: true\n' > "$WORK/reg/team-finance/.layer-config"
   printf -- '---\ntype: context\nversion: 1.0.0\ndescription: from-org\n---\n\nfrom-org\n' \
     > "$WORK/reg/org-defaults/shared/clash/ARTIFACT.md"
   printf -- '---\ntype: context\nversion: 1.0.0\ndescription: other\n---\n\nother\n' \
     > "$WORK/reg/org-defaults/shared/other/ARTIFACT.md"
   printf -- '---\ntype: context\nversion: 1.1.0\ndescription: from-team\n---\n\nfrom-team\n' \
     > "$WORK/reg/team-finance/shared/clash/ARTIFACT.md"
   ```

   **Expect.** `which podium` prints `$PODIUM_BIN/podium`, and the three
   `ARTIFACT.md` files exist.

2. Sync through the filesystem source, keeping the two streams apart.

   ```bash
   podium sync --registry "$WORK/reg" --target "$WORK/fs" --harness none \
     > "$WORK/out.txt" 2> "$WORK/err.txt"
   echo "exit=$?"
   cat "$WORK/err.txt"
   cat "$WORK/fs/shared/clash/ARTIFACT.md"
   ls "$WORK/fs/shared/other"
   ```

   **Expect.** `exit=1`. `$WORK/err.txt` carries one line beginning
   `rejected: shared/clash (ingest.collision): cross-layer collision` that
   ends with `declare extends: shared/clash to overlay it`. The materialized
   `shared/clash` holds `from-org`, and `shared/other` is present. `exit=0`
   or `from-team` is the shipped behavior this scenario exists to catch.

3. Repeat with `--dry-run` into a new target.

   ```bash
   podium sync --registry "$WORK/reg" --target "$WORK/dry" --harness none --dry-run; echo "exit=$?"
   ls "$WORK/dry" 2>&1
   ```

   **Expect.** `exit=1`, the same `rejected:` line, and `ls` reports that
   `$WORK/dry` does not exist.

4. Start a standalone server over the same directory and sync through it.

   ```bash
   podium serve --standalone --no-embeddings --layer-path "$WORK/reg" \
     --bind 127.0.0.1:8131 > "$WORK/srv.log" 2>&1 &
   SRV=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8131/healthz
   server_alive "$SRV" "$WORK/srv.log"
   podium sync --registry http://127.0.0.1:8131 --target "$WORK/srv" --harness none; echo "exit=$?"
   diff "$WORK/fs/shared/clash/ARTIFACT.md" "$WORK/srv/shared/clash/ARTIFACT.md" && echo same
   ```

   **Expect.** The server sync prints `exit=0` and no `rejected:` line, and
   `diff` prints `same`. A difference means the two deployment modes kept
   different copies.

5. Sanction the collision and sync the filesystem target again.

   ```bash
   printf -- '---\ntype: context\nversion: 1.1.0\ndescription: from-team\nextends: shared/clash\n---\n\nfrom-team\n' \
     > "$WORK/reg/team-finance/shared/clash/ARTIFACT.md"
   podium sync --registry "$WORK/reg" --target "$WORK/fs" --harness none; echo "exit=$?"
   cat "$WORK/fs/shared/clash/ARTIFACT.md"
   ```

   **Expect.** `exit=0`, no `rejected:` line, and the materialized artifact
   carries `description: from-team` with the `from-team` body.

6. Add a workspace overlay artifact that declares `extends:` on the same ID
   and sync with it.

   ```bash
   mkdir -p "$WORK/overlay/shared/clash"
   printf -- '---\ntype: context\nversion: 9.0.0\ndescription: from-overlay\nextends: shared/clash\n---\n\nfrom-overlay\n' \
     > "$WORK/overlay/shared/clash/ARTIFACT.md"
   podium sync --registry "$WORK/reg" --target "$WORK/fs" --harness none \
     --overlay "$WORK/overlay"; echo "exit=$?"
   cat "$WORK/fs/shared/clash/ARTIFACT.md"
   ```

   **Expect.** `exit=0`, no `rejected:` line, and the materialized file is
   byte-for-byte the overlay's authored `ARTIFACT.md`, including its
   `extends: shared/clash` line. Merged registry fields in the file, a
   `rejected:` line, or a non-zero exit means the §6.4 exception is not
   honored.

**Cleanup.** `kill "$SRV"` and `rm -rf "$WORK"`.
~~~~

The surfaces a human reads directly are the materialized `ARTIFACT.md`, the standard-error report, and `$?`. Step 2 catches the shipped highest-wins behavior, step 4 catches a deployment-mode divergence, and step 6 catches an overlay that starts merging or reporting.

## Documentation changes

**DOC-1.** Lands in S15. Rewrap each edited paragraph to its page's line width.

(a) `docs/reference/error-codes.md`, the `ingest.collision` row (line 110). Replace the description with:

> Another layer already contributes this canonical artifact ID and neither the incoming manifest nor the existing contribution declares `extends:` on it, so the overlay is not sanctioned. Ingest drops the incoming artifact. A filesystem-source `podium sync` drops the higher-precedence copy, materializes the rest, and exits 1.

(b) `docs/authoring/extends.md`, "Replacing instead of extending" (line 147). Replace "Silent shadowing is not permitted: ingest rejects same-ID collisions across layers when neither declares `extends:`." with:

> Silent shadowing between registry-side layers is not permitted. Ingest rejects a same-ID collision across layers when neither artifact declares `extends:` on the ID. A filesystem-source `podium sync` drops the higher-precedence copy in the same case and exits 1.

(c) `docs/authoring/extends.md`: add a subsection `## Workspace overlay` after "Replacing instead of extending":

> The workspace local overlay (`<workspace>/.podium/overlay/`) is an exception to the collision rule. An overlay artifact replaces the registry-side artifact with the same ID in the MCP server, in `podium sync`, and in the SDKs, and no consumer reports the replacement. The replacement is the same whether or not the overlay artifact declares `extends:`: a consumer resolves no `extends:` chain for an overlay artifact and serves it as authored. Copying the artifact into a shared layer subjects it to the collision rule and to `extends:` resolution.

Link the subsection from `docs/deployment/layers.md` (d) and `docs/deployment/local.md` (f).

(d) `docs/deployment/layers.md`.

- Line 17. Replace "The workspace local overlay is merged on the client and replaces a base artifact that carries the same ID." with "The workspace local overlay is merged on the client and replaces a base artifact that carries the same ID, whether or not it declares `extends:` (see [Authoring → extends](../authoring/extends#workspace-overlay))."
- Line 167, the first "Merge behavior worth knowing" bullet. Replace "`podium lint` reports the same collision on a local catalog, and `podium sync` materializing one keeps the highest-precedence copy." with "`podium lint` reports the same collision on a local catalog. `podium sync` against a local catalog drops the higher-precedence copy, names it on standard error, materializes the rest, and exits 1, so it materializes the copy a server started on the same directory serves. A dropped copy cannot serve as an `extends:` parent."

(e) `docs/getting-started/why-podium.md`, "Layered composition" (lines 80-83). Replace "A higher layer overrides a lower one on a collision, and `extends:` lets an artifact inherit and refine a lower one without forking it." with "Two layers that contribute the same artifact ID are a collision, which Podium rejects unless the higher artifact declares `extends:`. `extends:` lets that artifact inherit and refine the lower one without forking it."

(f) `docs/deployment/local.md` (line 105). After "The workspace local overlay (`<workspace>/.podium/overlay/`) sits on top of the filesystem-registry layers, at the same precedence it has against a server.", add: "An overlay artifact replaces a registry artifact with the same ID without a collision error; see [Authoring → extends](../authoring/extends#workspace-overlay). A collision between two registry layers fails `podium sync` instead; see [Layers](layers#merge-behavior-worth-knowing)."

(g) `docs/reference/cli.md`, `podium sync`, after "Lock file at `<target>/.podium/sync.lock`." Add:

> Against a local catalog, two registry layers that contribute the same ID, where the higher-precedence copy declares no `extends:`, are a collision. The command drops the higher-precedence copy and prints `rejected: <id> (ingest.collision): <reason>` on standard error for each dropped artifact, in the format `podium layer reingest` uses. It materializes every other artifact, writes the lock file without the dropped artifact, and exits 1. `--dry-run` reports the drop and exits 1 without writing. Under `--config`, a target that dropped an artifact counts as a failed target, and the command exits 1; this includes a `kind: workspace` target under `--check`. A target's workflow still runs before the target is marked failed. `--check` without `--config` validates `sync.yaml` only and reports no drop, and a `kind: marketplace` target under `--check` renders nothing. Under `--json` the report stays on standard error. Under `--watch` each cycle prints its report, and the command exits 1 on interrupt when any cycle it reported failed or dropped an artifact. A cycle still running when the interrupt arrives can finish without a report, and the next `podium sync` reports its drops. A sync against a server reports no drop, because the server rejected the artifact at ingest. A drop adds a cause for exit status 1 and leaves the command's other exit statuses unchanged.

(h) `docs/reference/cli.md`, `podium sync override`, after the paragraph beginning "`--target <path>` selects the materialized directory and defaults to the current directory. `--registry <url-or-path>` and `--harness <name>` override" (line 253 at the time of writing). The `podium sync override save-as` paragraph at line 263 also contains "`--target <path>` selects the materialized directory", so match the longer phrase. Add:

> When a registry is configured, override re-materializes the target through the same composition as `podium sync`. Against a local catalog with a collision between two registry layers, it prints the same `rejected: <id> (ingest.collision): <reason>` line for each dropped artifact, keeps the recorded toggles, and exits 1. `--dry-run` writes nothing, runs no re-materialization, and reports no drop.

**CL-1.** `CHANGELOG.md`, `## [Unreleased]`, `### Changed` (add the subsection if absent). Lands in S16.

> - **Filesystem-source `podium sync` applies the §4.6 collision rule** (§4.6, §7.3.1, §13.11.3): when two registry layer directories contribute the same artifact ID and the higher-precedence copy declares no `extends:` on that ID, sync drops the higher-precedence copy and materializes the lower-precedence one, which is the copy `podium serve --standalone --layer-path` serves on the same directory. Sync prints `rejected: <id> (ingest.collision): <reason>` on standard error for each dropped artifact, materializes every other artifact, writes the lock file without the dropped artifact, and exits 1. `--dry-run` reports the drop and exits 1. A `--config` target that dropped an artifact counts as a failed target, including a `kind: workspace` target under `--check`. `podium sync --watch` exits 1 on interrupt when any cycle it reported dropped an artifact, and `podium sync override` reports the drops of its re-materialization and exits 1. A sync that previously kept the higher-precedence copy and exited 0 now keeps the lower-precedence copy and exits 1. To keep overriding the lower copy, declare `extends: <id>` on the higher copy. A server-source `podium sync` and the workspace overlay are unchanged. This is a backward-incompatible change and lands in a MINOR bump. No flag restores the previous behavior.

Follow `.claude/rules/doc-style.md` in the final text.

## Open questions

**OQ-1. Publish after a drop.** A `--config` target that runs an operator workflow and dropped an artifact: should the publish phase still run on the materialized output, with the target then counting as failed? That is the staged default, and it mirrors the ingest cycle storing the rest. The alternative skips the publish phase on a drop, so a collision is never pushed to a shared marketplace or workspace remote. The alternative needs `pkg/sync` to expose the drops before `runPipeline` reaches publish, a change to the SPEC-5 sentence "A target's workflow runs on the materialized output as it does on a sync that dropped nothing", and the matching DOC-1 (g) sentence.

## Non-goals

- Resolving `extends:` on workspace overlay artifacts. It was considered and withdrawn while this proposal was drafted. It needs a second chain resolver in the MCP server, a fetch-only registry path that skips materialization, audit, and the cache, a pin rule for an artifact that is never ingested, a new §6.10 code for an unresolvable parent, and a native port of the §4.6 field table into both SDKs. Landing it on fewer than all consumers would make them serve different bytes for one overlay artifact. A separate proposal that lands it on every consumer together is the path if it is wanted.
- Changing server ingest's collision behavior: its arrival-order choice of which artifact to reject and its bidirectional admission, under which an existing cross-layer record that declares `extends:` on the ID admits a new contribution without `extends:`. A later reingest or reorder can make the server keep a different copy than filesystem sync would. Reconciling that belongs in a separate proposal.
- Defining a multi-layer workspace overlay. The Go consumers walk an overlay directory carrying `multi_layer: true` as layers with `CollisionPolicyHighestWins` (`pkg/overlay/overlay.go:57-63`), and the Python and TypeScript SDK overlays read no `.registry-config` and key each artifact by its path from the overlay root (`sdks/podium-py/podium/_overlay.py:271-289`, `sdks/podium-ts/src/overlay.ts:336-345`). Reconciling that pre-existing divergence needs SDK changes and a §6.4 rule, and belongs in a separate proposal.
- Reporting or warning on an overlay replacement. SPEC-3 sanctions it, so it stays silent.
- Removing `CollisionPolicyHighestWins` or `layer.Compose`. The overlay walk still uses the policy, and `Compose` only has its comment corrected.
- Editing §6.10 or adding a matrix cell. The `ingest.collision` cell and its annotated test already exist.
- Adding a §6.9 row.
- Making `podium sync --check` without `--config` compose layers.
- The analogous spec-text gap for `ingest.sandbox_profile_unenforceable`, which is on the §6.10 matrix axis but absent from `spec/`.
- Changing `podium lint`, which already rejects collisions with `CollisionPolicyDefault` and exits 1.

## Resolved in adversarial review

Review rounds populate this section. Each bullet records a finding and where its fix landed.

### Pass 1 (2026-10-01, automated)

- **The 0032 OQ-1 provenance was misstated.** "Current state and the gap" now quotes the 0032 sign-off, which directed both the overlay and filesystem sync to extends-or-reject, and records that the follow-up decision commissioning this proposal selected option (b) and supersedes that answer for the overlay. The first Fixed decisions bullet says the same.
- **DOC-1 (g) and the Fixed decisions stated a wrong `podium sync` exit-status set.** DOC-1 (g) now states only that a drop adds a cause for exit status 1. The Fixed decisions bullet and the "Exit status 1" decision now cite the code's existing 1 and 2 paths (`cmd/podium/main.go`, `docs/consuming/publishing.md:169`) and say they are unchanged.
- **SPEC-3 assigned a `multi_layer` overlay rule the SDKs do not perform.** The `multi_layer` sentence is removed from SPEC-3 and DOC-1 (c), a note under SPEC-3 and a new Non-goals bullet record the pre-existing Go-versus-SDK divergence, the edge-case row marks it unspecified, the Summary scopes "no overlay code changes" to a single-directory overlay, TEST-5 states that no SDK test is added, and the TEST-1 comment for `TestWalk_HighestWinsKeepsTopLayer` no longer cites §6.4 for the policy.
- **`TestRun_HigherLayerWinsOnCollision` and the `filesystemRecords` and `FetchRecords` test call sites were unstaged.** CODE-3 gains an "Existing `pkg/sync` tests" bullet that updates the three call sites and rewrites the test to the `extends:`-sanctioned case, and the "Watch out for" bullet and S10 name them.
- **No test pinned the `kind: marketplace` target's drop behavior.** TEST-3 adds the `test/e2e/publishing_test.go` tests for the report and exit status, the `--check` silence, and publish-then-fail ordering, and the e2e run pattern includes them.
- **`podium sync override` discarded `Result.Dropped` and exited 0.** CODE-3 adds `OverrideResult.Dropped`, CODE-4 reports it in `syncOverrideCmd` and `runSyncOverrideInteractive` and exits 1, SPEC-5 (a) states the override outcome, DOC-1 (h) documents it in `docs/reference/cli.md`, CL-1 mentions it, the edge-case rows cover `--add`, `--dry-run`, and the TUI, and TEST-2 and TEST-3 add the unit, interactive, and end-to-end tests. The Summary, the "`Walk` gains a policy and a callback" decision, and the new-identifier list name the field.
- **S14 did not depend on S10.** S14 now depends on S3 and S10.
- **S77 bound port 8127, which S64 binds.** S77 binds 127.0.0.1:8131, which no other scenario uses.
- **The override tests exercised the §7.5.5 redundant-add path and the e2e subtest passed no registry.** The first sync in the fixture materialized `shared/other`, so `--add shared/other` recorded no toggle, and without `--registry` override ran no re-materialization. TEST-3 now scopes the first sync with `--include shared/note` and passes `--registry` to `sync override`, TEST-2 sets `Scope.Include` on the first `Run`, and the `podium sync override --add <id>` edge-case row states the no-op exception and the registry condition.
- **DOC-1 (h) cited line 252 and a phrase that also appears in the `save-as` paragraph.** DOC-1 (h) now cites line 253 and anchors on the longer phrase unique to the override paragraph.

### Pass 2 (2026-10-01, automated)

- **The watch-mode drop subtest synchronized on the materialized file, so SIGINT could race the dropped cycle's event and the watcher exit 0.** The TEST-3 `--watch` subtest now polls `w.log()` for the `rejected: shared/note (ingest.collision):` line before `w.stop(t)`, which removes the race and pins the per-cycle report. SPEC-5 (b), DOC-1 (g), CL-1, the Summary, and the "The rest of the sync proceeds" decision now count only cycles the watch loop reported, and state that a cycle interrupted mid-run can finish without a report and that the next `podium sync` reports its drops. The CODE-4 `runWatchLoop` bullet records why (`pkg/sync/watch.go:90-95` discards an event once the context is done), and a new edge-case row records the accepted window. The watcher's `emit` is unchanged, because narrowing the spec closes the finding without a new delivery mechanism and the same window already applies to a failed cycle.
- **No test pinned workflow-then-fail ordering for a `kind: workspace` target that drops.** TEST-3 adds `TestPublishing_WorkspaceCollisionPublishesThenFails`, built on `writeSyncConfigWorkspaceWorkflow`, which asserts the publish marker, the `rejected:` and `target claude-workspace:` lines, the lower copy, and exit 1. The CODE-4 `runWorkspaceTarget` bullet names the test, and the existing `Publishing_.*Collision` run pattern selects it.

### Pass 3 (2026-10-01, automated)

- **SPEC-5 (a) said `podium sync override --dry-run` composes no layers, but the TUI form composes them.** With no `--add`, `--remove`, or `--reset`, the command takes the TUI path (`cmd/podium/main.go:750-752`), which calls `sync.ResolveEffectiveView` before it reads `dryRun` (`cmd/podium/interactive.go:296-300`), and that walks the layers through `filesystemRecords`. The dry-run outcome rests on `pkg/sync/override.go:146`, which skips the re-materializing `Run` when `DryRun` is set. SPEC-5 (a) and DOC-1 (h) now say that `--dry-run` writes nothing, runs no re-materialization, and reports no drop, and the edge-case row says the same and notes that the TUI form composes the layers for its checklist and discards the drops.
- **The SPEC-5 (a) bullet above cited `cmd/podium/main.go:760-762` for the TUI branch.** Those lines hold the `sync.Override` call on the batch path. The guard `if len(add) == 0 && len(remove) == 0 && !*reset` that returns `runSyncOverrideInteractive` is at `cmd/podium/main.go:750-752`, and the bullet now cites that range.
