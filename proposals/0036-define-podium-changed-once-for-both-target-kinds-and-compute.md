# Proposal 0036: Define $PODIUM_CHANGED once for both target kinds and compute the workspace value from the bytes on disk

- Issue: (to be filed)
- Status: Approved (2026-10-02). Signed off as staged. OQ-1: the other workspace workflow variables ($PODIUM_WORKDIR, $PODIUM_TARGET_ID, $PODIUM_REGISTRY) are left to a follow-up proposal.
- Date: 2026-10-02

This document stages the proposed spec, code, test, and documentation changes. It does not modify any spec, code, or doc file. Apply the changes in the staged sections after sign-off.

## Summary

**What changes.**

- §7.5.2 gains the single definition of `$PODIUM_CHANGED` for both target kinds: the variable is `true` when materialization altered the bytes on disk of a compared path, measured from the start of materialization (after the `prepare` phase) to the end of the stale-file cleanup, and the compared paths are the paths the run materializes plus the prior lock file's materialized paths (SPEC-1).
- §7.8 keeps its injected-variable entry for `$PODIUM_CHANGED` as a pointer to §7.5.2 (SPEC-2).
- `pkg/sync/render.go`: the doc comments of `onDiskDigests`, `unionPaths`, and `changeSet` name both callers, and the helpers stay in place (CODE-1).
- `pkg/sync/sync.go`: `sync.Run` computes `Result.Changed` through a new `writeTarget` helper from on-disk SHA-256 digests taken before and after materialization with those helpers. `lockChanged`, `lockEntryHashes`, and `pkg/sync/lock_changed_test.go` are deleted (CODE-2).
- `cmd/podium/main.go`: the `runWorkspaceTarget` comments cite §7.5.2 and describe the comparison scope (CODE-3).
- `pkg/sync/marketplace_run.go`: a marketplace `--dry-run` prints the `prepare` phase with the variables the live `prepare` phase receives, so the preview neither substitutes `$PODIUM_CHANGED` nor marks a `prepare` command skipped (CODE-4).
- Tests: unit tests in `pkg/sync` for the cases the lock comparison got wrong, a rewrite of `TestRun_ChangedSeesEveryContributorToASharedPath` that lands with CODE-2, a marketplace dry-run preview test, and an extended end-to-end `skip_if_no_changes` test (TEST-1, TEST-3, TEST-2).
- Docs, changelog, and manual validation follow (DOC-1, CL-1, MV-1).

**Fixed decisions.**

- Option (b) is settled. Both kinds compute `$PODIUM_CHANGED` by comparing the bytes on disk at the start of materialization, after the `prepare` phase, with the bytes after materialization and the stale-file cleanup. A file the `prepare` phase clones, pulls, or rewrites is part of the starting state. The lock-hash comparison is removed without a replacement mode.
- The compared set is the materialized paths of this run plus the prior lock's `materialized_path` entries. The whole target directory is never compared, and the lock file is never a compared path.
- A path counts as altered when its bytes differ, when it appears, or when it disappears. A prior-lock path that was absent before and stays absent is unaltered.
- A config-merge or inject path is compared as the whole merged file on disk.
- A read error before the write, on any compared path, does not fail the sync; it makes `Result.Changed` true, and `materialize.Write` then succeeds or fails exactly as it does today. After the write, a read error on a current path fails the sync closed with `sync: read target after materialize: %w`, and a read error on a prior-only path makes `Result.Changed` true.
- The helpers stay in `pkg/sync/render.go`. No file is moved and no `changeset.go` is created.
- The before-snapshot is taken in `sync.Run` after the `DryRun` return, and the after-snapshot after `removeStalePaths`. A `DryRun` or `--check` run and an offline-first no-op leave `Changed` false, and an offline-first no-op still runs the publish phase with `$PODIUM_CHANGED=false`, as it does today.
- No option gates the comparison. Watch mode and `podium sync override` pay the two reads per compared path although they never read `Result.Changed`.
- The variable is publish-phase only for both kinds, including in the `prepare` commands a marketplace `--dry-run` prints (CODE-4).
- The workspace path gains no `$PODIUM_CHANGE_SUMMARY`. `OverrideResult.Changed`, `printJSON`, and the watch output are unchanged.
- The proposal adds no environment variable, flag, error code, SPI, or lock field.

**Watch out for.**

- **Spec line numbers are anchors at the time of writing.** Locate each edit by its quoted current text.
- **Order matters.** CODE-2 and CODE-4 cite §7.5.2 for the definition, so each lands only after SPEC-1 is committed (spec-driven-development.md).
- **A prior-only path is shared with the operator.** `removeStalePaths` treats it as best-effort cleanup (`pkg/sync/sync.go`, "a partial cleanup is better than a hard failure"). Reading it with the marketplace's fail-closed `onDiskDigests` would abort every sync of a target whose stale path the operator replaced with a directory, including watch mode and `podium sync override`, which never read `Changed`. CODE-2 reads prior-only paths tolerantly for that reason. It also reads current paths tolerantly before the write, because `materialize.Write` never reads a standalone (`OpWrite`) target: it stages `<path>.tmp` and renames it over the old file (`pkg/materialize/atomic.go`), so a mode-000 materialized file is replaced today, and a fail-closed before-read would turn that successful sync into a failure. The marketplace keeps its fail-closed read, because its checkout is fully Podium-owned.
- **`currentPaths` and `fileMerge` are built after `materialize.Write` today.** CODE-2 needs `currentPaths` before the write to take the before-snapshot, so the loop that builds them moves above the write.
- **An operator entry in `.mcp.json` written in non-canonical formatting is rewritten on every sync.** `mergeJSON` re-serializes the whole file with `json.MarshalIndent`, sorted keys, two-space indent, and a trailing newline (`pkg/materialize/merge.go`). A test that adds an operator entry and expects `Changed=false` must write it in that form, or it fails for a correct reason.
- **Do not test `last_synced_at`.** It comes from a direct `time.Now()` call (`pkg/sync/sync.go`) with no injected clock. `Changed=false` over an unchanged re-sync already proves that rewriting the lock does not count.
- **`TestRun_ChangedSeesEveryContributorToASharedPath` fails under CODE-2 unless its body changes.** Its comment (`pkg/sync/sync_order_test.go:102-107`) explains the (artifact id, materialized path) lock key, which CODE-2 deletes. Its edit changes only the mcp-server's `description:` (`pkg/sync/sync_order_test.go:139`), and the claude-code `.mcp.json` fragment carries only the server name, the config derived from `server_identifier`, and the ownership index (`pkg/adapter/claudecode.go:99`, `pkg/adapter/layout.go:185-187`). The merged file stays byte-identical, so `Changed` reads `false` and the test's `!res.Changed` assertion fails. The rewrite in the Testing section keeps the description-only edit with a `Changed=false` assertion, adds a `server_identifier` edit that asserts `Changed=true`, and rewrites the comment. It lands in S3 with CODE-2, so the S3 commit leaves `go test ./pkg/sync/` green.
- **End-to-end tests skip silently on macOS in parts of `test/e2e`.** Grep the output for `SKIP` before treating TEST-2 as verified.
- **A prior attempt claimed an on-disk diff and never read the disk.** Commit `dd988930` introduced `lockChanged` as "the on-disk diff against the prior lock". Its comparison reads only lock content hashes. A review of CODE-2 checks that every compared value comes from `os.ReadFile`.

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. §7.5.2 defines `$PODIUM_CHANGED` once for both target kinds.
      Levels: —. Depends on: —
- [ ] **S2 · spec** — SPEC-2. The §7.8 injected-variable entry for `$PODIUM_CHANGED` becomes a pointer to §7.5.2.
      Levels: —. Depends on: S1
- [ ] **S3 · code** — CODE-1, CODE-2. `sync.Run` computes `Result.Changed` from on-disk digests through `writeTarget`, the render.go helper comments name both callers, and `lockChanged`, `lockEntryHashes`, and `pkg/sync/lock_changed_test.go` are deleted. The comment and body of `TestRun_ChangedSeesEveryContributorToASharedPath` in `sync_order_test.go` are rewritten in the same step, because CODE-2 makes its existing assertion fail. Bundled because CODE-1 is only the comment edit on the helpers CODE-2 starts calling, and one reviewer reads both.
      Levels: unit, integration, materialization. Depends on: S1
- [ ] **S4 · code** — CODE-3. `runWorkspaceTarget` comments cite §7.5.2 and describe the comparison scope.
      Levels: e2e. Depends on: S1, S3
- [ ] **S5 · test** — TEST-1. Unit tests for the workspace on-disk comparison and the rewritten comment in `sync_test.go`.
      Levels: unit. Depends on: S3
- [ ] **S6 · test** — TEST-2. The workspace `skip_if_no_changes` end-to-end test covers a restored hand edit and a missing lock.
      Levels: e2e. Depends on: S3, S4
- [ ] **S7 · docs** — DOC-1. `docs/consuming/publishing.md` defines the variable once and points the workflow and skip-flag rows at it.
      Levels: —. Depends on: S1, S3
- [ ] **S8 · docs** — CL-1. The `## [Unreleased]` `### Fixed` entry replaces the lock-keyed change-reporting entry, and the content-hash upgrade note under `### Changed` drops its every-target-changed clause.
      Levels: —. Depends on: S3
- [ ] **S9 · docs** — MV-1. Manual-validation scenario S78 in `test/manual-validation.md`.
      Levels: —. Depends on: S3, S4
- [ ] **S10 · code** — CODE-4, TEST-3. A marketplace `--dry-run` prints the `prepare` phase with `baseVars`, and a unit test pins the preview. Bundled because the test is the only observer of the change.
      Levels: unit. Depends on: S1
      Placed after the docs steps deliberately: no other step consumes CODE-4, so it can land at any point after S1.

## Current state and the gap

### One definition, for one kind

The spec defines `$PODIUM_CHANGED` in the §7.8 injected-variable list for the marketplace pipeline only: "whether the render produced a diff against the checkout". §7.5.2 says both target kinds may carry a `workflow:` and says when a workflow runs. It never names `$PODIUM_CHANGED` and never says what `skip_if_no_changes` means for a `kind: workspace` target. The only `skip_if_no_changes` in §7.5.2 is the YAML example on the marketplace entry. The workspace variable therefore has no spec basis. The code still cites §7.5.2 for it, together with a proposal-internal "Decision 3", at `pkg/sync/sync.go` (the `Result.Changed` field doc and the `lockChanged` call site), `cmd/podium/main.go` (`runWorkspaceTarget`), `pkg/sync/sync_test.go` (`TestRun_ChangedTracksOnDiskDelta`), and `test/e2e/publishing_test.go` (`TestPublishing_WorkspaceTargetSkipIfNoChanges`).

### Two computations

The marketplace path sets the variable from `RenderResult.Changed` (`pkg/sync/marketplace_run.go`). `reconcile` (`pkg/sync/render.go`) reads the on-disk SHA-256 of every path in `unionPaths(rendered paths, prior-lock materialized paths)` before `materialize.Write` and again after `Reconcile` and `writeLock`, then compares the two with `changeSet`. A differing digest, a newly present path, and a vanished path each count as a change.

The workspace path sets the variable from `sync.Result.Changed` (`cmd/podium/main.go`). `sync.Run` assigns that field from `lockChanged(priorLock, lock)` (`pkg/sync/sync.go`). `lockChanged` compares (artifact id, materialized path) to `ContentHash` maps taken from the two locks. `ContentHash` is the artifact's canonical source hash (`lockContentHash`), so `lockChanged` never reads a materialized byte, while `materialize.Write` and `removeStalePaths` rewrite the disk on every run.

Both values reach the same consumer. `WorkflowRunner.Phase` (`pkg/sync/workflow_phase.go`) skips a command whose `skip_if_no_changes` is set when `vars["PODIUM_CHANGED"] == "false"`. The workspace target reaches it from `runWorkspaceTarget`, and the marketplace target through `runPhase` in `pkg/sync/marketplace_run.go`. The variable is set after materialization, so the skip applies to publish-phase commands.

### The lock comparison is wrong in both directions

It reports `false` while files changed on disk in three cases, so a `skip_if_no_changes` commit is skipped and the rewritten files stay uncommitted:

- An adapter output-format change across a Podium upgrade rewrites files and moves no artifact hash.
- A change in the fold order of two artifacts in one shared config-merge or inject file moves no hash either. Filesystem mode once folded in layer-precedence order and now folds in canonical-ID order (`pkg/sync/sync.go`), and a hand reorder of Podium's blocks is restored by sync.
- A hand-edited or hand-deleted materialized file is restored by sync without moving a hash.

It reports `true` while no materialized byte changed in two cases:

- An authored-bytes change the adapter does not emit, for example a `tags:` edit in `ARTIFACT.md` on a skill whose Claude Code output is only `SKILL.md` and its resources (`pkg/adapter/claudecode.go`).
- A sync with a missing prior lock over a target that is already current. `lockEntryHashes(nil)` is empty, so the map lengths differ.

### Readers of `Result.Changed`

`Result.Changed` has one reader in production code, `runWorkspaceTarget`. `printJSON` emits no changed field. Watch mode only prints results. `podium sync override` prints `OverrideResult.Changed`, a separate toggle flag computed in `pkg/sync/override.go`.

### The docs repeat the gap

`docs/consuming/publishing.md` names `$PODIUM_CHANGED` for a workspace publish phase in the `workflow` row without defining it, and the `skip_if_no_changes` row and the `$PODIUM_CHANGED` row give only the marketplace meaning.

## Decisions

- **Option (b) is settled and not reopened.** Both kinds compute `$PODIUM_CHANGED` the same way, by comparing the bytes on disk at the start of materialization with the bytes after materialization and the stale-file cleanup. The spec states this meaning once, and both kinds point to it.
- **The definition lives in §7.5.2.** It sits next to the existing sentence that both kinds may carry a `workflow:`, because §7.5.2 is the section covering both kinds. The §7.8 list item points to it rather than restating it. Once the definition is in §7.5.2, the code's `§7.5.2` citations for the variable become accurate. They are rewritten to the `// Spec: §7.5.2` form, and the "Decision 3" and lock-based wording are dropped.
- **Compared set.** The compared set is the paths the run materializes (`allFiles`) plus the materialized paths recorded in the target's prior lock file. Those prior paths are the ones the stale-file cleanup removes or reconciles. This is the marketplace `diffPaths = unionPaths(currentPaths, priorMerge)`. The whole target directory is not compared, so operator files that Podium never writes cannot change the variable.
- **The lock file is outside the compared set by construction.** `.podium/sync.lock` is never a materialized path or a prior-lock materialized path, so the `last_synced_at` stamp that `sync.Run` writes on every run never counts as a change. No explicit exclusion is added. The spec text says the lock file is not compared.
- **Appearance and disappearance.** A path counts as changed when it is present in the starting state and absent in the final state (stale cleanup removed it). It also counts when it is absent in the starting state and present in the final state: a first write, or the restoration of a hand-deleted file. Both rules are `changeSet`'s existing ones. A prior-lock path that was already absent in the starting state and stays absent is not a change.
- **Merged files.** For a config-merge or inject path the comparison covers the whole merged file as it sits on disk. A re-sync that leaves the file byte-identical reports `false`, and operator-owned entries that the fold preserves byte-for-byte never register. Restoring Podium's entry order or block order registers as `true`.
- **Cost.** The comparison reads each path in the compared set once before the write and once after it, and hashes it with SHA-256. The cost scales with the materialized set and the bytes it holds, and it does not scale with the size of the target directory. No option gates it. Watch mode and override pay the same cost although they never read `Result.Changed`, because the alternative is a second code path. The extra cost is two reads of files the run already writes.
- **One consumer, one computation.** `Result.Changed` has one production reader, `runWorkspaceTarget`. It switches to the on-disk comparison, and no consumer keeps `lockChanged`. `lockChanged` and `lockEntryHashes` become dead code and are deleted with `pkg/sync/lock_changed_test.go`. `OverrideResult.Changed` is a different field, a toggle-change flag, and is unchanged. `printJSON` and the watch output carry no change field and are unchanged.
- **Reuse in place.** `onDiskDigests`, `digest`, `unionPaths`, and `changeSet` stay in `pkg/sync/render.go`. `sync.go` is in the same package and calls them where they are, so nothing is duplicated and no file moves. Their doc comments are edited to name both callers. `sync.Run` passes a nil owner map to `changeSet` and discards the artifact IDs. The workspace path adds no `$PODIUM_CHANGE_SUMMARY`.
- **Read errors.** The before-snapshot reads every compared path tolerantly. A read error other than not-present marks the path unobservable and does not fail the sync, because the sync's success is decided by `materialize.Write` today. `materialize.Write` never reads a standalone (`OpWrite`) target: it writes `<path>.tmp` with the adapter's mode and renames it over the old file (`pkg/materialize/atomic.go:267-280`), so an unreadable regular file at a standalone path, such as a mode-000 file, is replaced and the sync succeeds. It reads a config-merge or inject target and fails on a read error (`pkg/materialize/merge.go:35-40`), and the rename fails on a directory at the path, so those runs fail with the `materialize.Write` error as they do today. A read error on a prior-only path (in the prior lock and not materialized by this run) does not fail the sync either, because that path is best-effort cleanup today. The after-snapshot reads the current paths fail-closed with `sync: read target after materialize: %w`. That error occurs only on a concurrent filesystem mutation, because `materialize.Write` left each current path a regular file written with the adapter's mode. The after-snapshot reads prior-only paths tolerantly. Any compared path unobservable in either snapshot makes `Result.Changed` true. A variable that cannot be observed therefore never reads `false`.
- **Snapshot placement.** The before-snapshot is taken inside `sync.Run` after the `DryRun` return and before `materialize.Write`. The after-snapshot is taken after `removeStalePaths`. A `DryRun` or `--check` run and an offline-first no-op (`offlineFirstNoop`, `pkg/sync/sync.go:243-245`, `:452-457`) write nothing and leave `Changed` false, as they do today. `runWorkspaceTarget` skips the workflow only under `--check`, `--dry-run`, or an empty workflow (`cmd/podium/main.go:554`), so an offline-first no-op still runs the publish phase with `$PODIUM_CHANGED=false`: its `skip_if_no_changes` commands are skipped and its other publish commands run. That behavior is unchanged. The §13.11.3 publish-then-fail ordering in `runWorkspaceTarget` is unchanged, because `Changed` is still produced inside `sync.Run` before the CLI reads it.
- **Publish phase only.** The variable stays publish-phase only for both kinds. The workspace prepare phase runs before materialization (`cmd/podium/main.go:560-563`), and the marketplace sets the variable after render (`pkg/sync/marketplace_run.go:170`). The spec says so. The marketplace `--dry-run` preview prints `prepare` after that assignment (`pkg/sync/marketplace_run.go:174-176`), so it substitutes `$PODIUM_CHANGED`, `$PODIUM_CHANGE_SUMMARY`, and `$PODIUM_COMMIT_MESSAGE` into `prepare` commands, and `printPhase` marks a `skip_if_no_changes` `prepare` command skipped when the value is `false` (`pkg/sync/marketplace_run.go:296`). The live `prepare` phase receives only `baseVars` (`pkg/sync/marketplace_run.go:132`, `:141`), and `WorkflowRunner.Phase` runs such a command because the value is absent (`pkg/sync/workflow_phase.go:31`). CODE-4 prints `prepare` with `baseVars`, so the preview matches the live run and §7.8's "prints each command with variables substituted" (`spec/07-external-integration.md:882`).
- **Comparison anchor.** The starting state is taken when materialization starts, after the `prepare` phase, and the final state after materialization and the stale-file cleanup. §7.8 has `prepare` clone into a Podium-allocated working directory by default (`spec/07-external-integration.md:870`), so an anchor at the start of the run would make every rendered path absent at the start and the variable always `true`, which contradicts the unchanged §7.8 text on suppressing an empty commit (`spec/07-external-integration.md:892`, `:898`, `:908`). The code already takes both snapshots inside materialization: `reconcile` reads after the `prepare` clone (`pkg/sync/render.go:313-315`), and CODE-2's `writeTarget` reads inside `sync.Run`, which `runWorkspaceTarget` calls after its `prepare` phase (`cmd/podium/main.go:560-564`). A workspace `prepare` that rewrites a materialized file the sync then leaves as is reads `false`.
- **No new surface.** The proposal adds no environment variable, flag, error code, SPI, or lock field. Podium is pre-1.0, so the changed workspace semantics land without a compatibility path.

## Spec amendment: §7.5.2 the `$PODIUM_CHANGED` variable

**SPEC-1.** Lands in S1.

**Anchor.** `spec/07-external-integration.md`, §7.5.2 "Configuration (`sync.yaml`)". Insert a new paragraph between the paragraph that begins "A `targets:` entry carries a `kind:` field that selects its output format." (it contains "Both kinds may carry a `workflow:` of `prepare` and `publish` command lists.") and the paragraph that begins "A `workflow:` runs only when `podium sync` materializes a target."

**Added text.**

> A workflow's `publish` phase receives `$PODIUM_CHANGED` for both target kinds, with one meaning. The variable is `true` when materialization altered at least one compared path in the target directory, and `false` otherwise. The compared paths are the paths the run materializes and the `materialized_path` entries of the target's prior lock file (§7.5.3). The comparison reads the compared paths in a starting state, taken when materialization starts after the `prepare` phase, and in a final state, taken after materialization and the stale-file cleanup that follows it. A file the `prepare` phase clones, pulls, or rewrites is therefore part of the starting state. A compared path is altered when its bytes on disk differ between the two states, when it is absent from the starting state and present in the final state, or when it is present in the starting state and absent from the final state. A compared path whose bytes cannot be read in the starting state, or a compared path from the prior lock file that the run does not materialize and whose bytes cannot be read in the final state, counts as altered. For a config-merge or inject file (§6.7), the comparison covers the whole merged file. The lock file is not a compared path. A command that sets `skip_if_no_changes` is skipped when `$PODIUM_CHANGED` is `false`. The `prepare` phase runs before materialization and does not receive the variable, and the `prepare` commands that `--dry-run` prints do not substitute it. For a `kind: marketplace` target, the target directory is the working checkout (§7.8).

The sentence on an unreadable compared path states the CODE-2 read-error rule, so the behavior an operator observes on a shared target directory has a spec basis. The starting-state sentence anchors the comparison after `prepare`, which is where `reconcile` (`pkg/sync/render.go:313-315`) and CODE-2's `writeTarget` take their snapshots. The dry-run clause states the CODE-4 behavior.

## Spec amendment: §7.8 injected variables

**SPEC-2.** Lands in S2.

**Anchor.** `spec/07-external-integration.md`, §7.8 "Marketplace Publishing", the "`podium sync` and the configurable workflow" subsection, the **Injected variables** list.

**Current text.**

> - `$PODIUM_CHANGED`: whether the render produced a diff against the checkout.

**Replacement text.**

> - `$PODIUM_CHANGED`: whether the render altered the bytes of the checkout, as §7.5.2 defines for both target kinds.

Leave the Reconciliation paragraph ("The git diff after a render is the catalog delta, so `skip_if_no_changes` suppresses an empty commit when the delta is empty."), the Triggers paragraph on an unrelated `layer.ingested` event, and the Pattern A paragraph unchanged. Each matches a byte comparison of the checkout.

## Proposed solution

### CODE-1. Name both callers in the change-detection helper comments

Lands in S3 with CODE-2. Target: `pkg/sync/render.go`.

The helpers stay where they are. Edit the doc comments of `onDiskDigests`, `unionPaths`, and `changeSet` in place so they name both callers: the marketplace render's checkout and a `kind: workspace` target directory.

- `onDiskDigests`: replace "did not exist before the render" with "did not exist before materialization", and "the checkout state could not be observed" with "the state of the checkout or target directory could not be observed".
- `unionPaths`: replace "the paths this render wrote, plus the prior-render paths the cleanup may remove" with "the paths the run materializes, plus the prior-lock paths the cleanup may remove", and "a pure removal is observed as a change against the checkout" with "a pure removal is observed as a change".
- `changeSet`: replace "diffs the checkout digests captured before and after the render" with "diffs the digests of a marketplace checkout or a `kind: workspace` target directory captured before and after materialization", and add: "A nil owner map is valid for a caller that reads only the boolean; the returned IDs are then meaningless."

`removedOwner`, `digest`, and `sortedSet` stay where they are and keep their comments. The `// Spec: §7.5.2` citation goes on `writeTarget` in `sync.go` (CODE-2).

### CODE-2. Compute `Result.Changed` in `sync.Run` from on-disk digests and delete `lockChanged`

Lands in S3. Target: `pkg/sync/sync.go`.

**`writeTarget`.** Add an unexported helper in `sync.go`:

```go
// writeTarget materializes files into target, reconciles the prior lock's
// stale paths, and reports whether any compared path changed on disk.
// Spec: §7.5.2 — $PODIUM_CHANGED compares the bytes on disk of the paths
// the run materializes and the prior lock's materialized paths, at the start
// of materialization (after the prepare phase) and after the stale-file
// cleanup. ...
func writeTarget(target string, files []adapter.File, current map[string]bool, priorMerge map[string]string) (bool, error)
```

Its body, in order:

1. Read the current paths tolerantly. A not-present path contributes no entry. Any other read error marks the path unobservable and does not return an error, so `materialize.Write` decides whether the run fails, as it does today.
2. Read the prior-only paths (in `priorMerge` and not in `current`) tolerantly, the same way.
3. `materialize.Write(target, files)`. The existing error return is unchanged.
4. `removeStalePaths(target, priorMerge, current)`. Unchanged, including its stderr warnings.
5. Read the current paths again with `onDiskDigests`. On error return `fmt.Errorf("sync: read target after materialize: %w", err)`.
6. Read the prior-only paths tolerantly again.
7. When any path was unobservable in step 1, step 2, or step 6, return `true`. Otherwise merge the current and prior-only digests of each snapshot and return the boolean from `changeSet(before, after, nil)`.

**IMPLEMENTOR'S CHOICE:** how the tolerant read of steps 1, 2, and 6 is written. Constraint: it hashes with `digest`, treats `os.ErrNotExist` exactly as `onDiskDigests` does, reports every other read error as unobservable, and adds no second SHA-256 routine.

**`Run`.** In the materialize section after the `DryRun` return:

- Move the loop that builds `currentPaths` and `fileMerge` from `allFiles` above the write.
- Replace the `materialize.Write` and `removeStalePaths` calls with `changed, err := writeTarget(opts.Target, allFiles, currentPaths, priorMerge)`, returning `nil, err` on error.
- Replace `res.Changed = lockChanged(priorLock, lock)` and its comment with `res.Changed = changed`. The lock is still written after, and a lock write failure stays non-fatal.

**`Result.Changed` field doc.** Replace it with:

```go
// Changed reports whether this run altered the bytes on disk of a compared
// path: a path the run materialized or a materialized path recorded in the
// prior lock. A path whose bytes differ, a path that appeared, and a path the
// stale-file cleanup removed each count; the lock file is never compared, and
// a compared path unreadable before the write, or a prior-lock path unreadable
// after it, counts as changed. It feeds the
// $PODIUM_CHANGED variable of a kind: workspace target's publish phase, with
// the meaning a marketplace target's RenderResult.Changed has. A DryRun run
// writes nothing and leaves Changed false.
// Spec: §7.5.2
```

**Deletions.** Delete `lockChanged` and `lockEntryHashes`, and delete `pkg/sync/lock_changed_test.go` in the same commit. The same commit rewrites `TestRun_ChangedSeesEveryContributorToASharedPath` as the Testing section specifies, because its description-only edit no longer changes the bytes on disk (`pkg/sync/sync_order_test.go:139`, `:146-147`).

Nothing in `pkg/sync/render.go` changes behavior. `RenderResult.Changed`, `ChangedArtifacts`, and the marketplace's fail-closed reads stay as they are. The one marketplace behavior change is the dry-run `prepare` preview (CODE-4).

### CODE-3. Correct the `runWorkspaceTarget` comment and citation

Lands in S4. Target: `cmd/podium/main.go`. The change is comment-only.

- Function doc: change "wrapped by the operator prepare/publish workflow phases when the plan carries one (Decision 3)." to "wrapped by the operator prepare/publish workflow phases when the plan carries one (§7.5.2)."
- The comment above `vars["PODIUM_CHANGED"]`: replace it with:

```go
// Spec: §7.5.2 — $PODIUM_CHANGED reports whether the sync altered the bytes
// on disk of a compared path (a materialized path or a prior-lock
// materialized path; the lock file is excluded), the same meaning a
// marketplace target's render reports, so a skip_if_no_changes publish
// command is skipped when no compared path changed.
```

The `Decision 3` citation in `pkg/sync/sync.go` is removed by CODE-2, and the one in `TestPublishing_WorkspaceTargetSkipIfNoChanges` by TEST-2. The citations in `pkg/sync/resolve_marketplace_test.go` and the other `test/e2e/publishing_test.go` tests concern workflow wrapping and are out of scope.

### CODE-4. Print the dry-run `prepare` phase with the variables the live `prepare` phase receives

Lands in S10. Target: `pkg/sync/marketplace_run.go`, `runPipeline`.

`runPipeline` assigns `$PODIUM_CHANGED`, `$PODIUM_CHANGE_SUMMARY`, and `$PODIUM_COMMIT_MESSAGE` into `vars` after the render (`:170-172`) and then, under `opts.DryRun`, prints both phases with that map (`:174-176`). The live `prepare` phase runs at `:141` with `vars` as `baseVars` built it at `:132`, before the assignment.

- Replace `printPhase(opts.Stdout, "prepare", out.Workflow.Prepare, vars)` with `printPhase(opts.Stdout, "prepare", out.Workflow.Prepare, baseVars(out, workdir))`. `baseVars` returns a fresh map with the content the live `prepare` phase receives, so no copy of `vars` is kept.
- Add above the call: `// Spec: §7.5.2 — the prepare phase does not receive $PODIUM_CHANGED or the other render-derived variables, so the preview prints it with the variables the live prepare phase receives (§7.8 "prints each command with variables substituted").`
- The `publish` preview keeps `vars`.

With the key absent, `substitute` leaves `$PODIUM_CHANGED` as its literal `$PODIUM_CHANGED` (`pkg/sync/marketplace_run.go:311-317`), and `printPhase` never marks a `prepare` command skipped, because an absent key never equals `"false"` (`:296`). That matches the live run, where `WorkflowRunner.Phase` runs the command (`pkg/sync/workflow_phase.go:31`). A workspace target runs no workflow under `--dry-run` (`cmd/podium/main.go:554`), so it has no preview to change.

## Edge cases and accepted failure modes

| Case | Outcome | Governing text; documented in |
|:--|:--|:--|
| First sync into an empty target | `true`; publish commands run | §7.5.2 "absent from the starting state and present in the final state" (SPEC-1); `docs/consuming/publishing.md` `$PODIUM_CHANGED` row (DOC-1) |
| Re-sync of an unchanged catalog | `false`; `skip_if_no_changes` commands print `skipped (no changes)` | §7.5.2 (SPEC-1); `docs/consuming/publishing.md` (DOC-1) |
| Re-sync after deleting `.podium/sync.lock`, target already current | `false`. The compared set is the current paths only, and their bytes do not change | §7.5.2 "The compared paths are the paths the run materializes and the `materialized_path` entries of the target's prior lock file" (SPEC-1); `CHANGELOG.md` (CL-1) |
| A hand edit or hand deletion of a materialized file, restored by the sync | `true` | §7.5.2 altered-path rules (SPEC-1); `docs/consuming/publishing.md` "including a file it restores after a hand edit or deletion" (DOC-1) |
| An adapter output-format change after a Podium upgrade | `true` on the first sync on the new version | §7.5.2 (SPEC-1); `CHANGELOG.md` (CL-1) |
| Podium's blocks in `AGENTS.md` reordered by hand | `true`; the sync restores ascending canonical-ID order | §7.5.2 "the comparison covers the whole merged file" (SPEC-1); `CHANGELOG.md` (CL-1) |
| An `ARTIFACT.md` edit that the adapter does not emit (a skill's `tags:` under Claude Code) | `false`; the lock's `content_hash` still moves | §7.5.2 (SPEC-1); `docs/consuming/publishing.md` "changed the bytes on disk of a file Podium writes or removes" (DOC-1) |
| An operator entry in `.mcp.json` written in canonical `MarshalIndent` form | `false`; the entry is preserved | §7.5.2 whole-file comparison (SPEC-1); `docs/consuming/publishing.md` (DOC-1) |
| An operator entry in `.mcp.json` written in another formatting | `true` on the next sync, because the merge rewrites the file in canonical form; `false` on later syncs (accepted) | §7.5.2 whole-file comparison (SPEC-1); `docs/consuming/publishing.md` (DOC-1) |
| An operator file elsewhere in the target directory changes | No effect on the variable (accepted) | §7.5.2 compared-path definition (SPEC-1); `docs/consuming/publishing.md` "a file Podium writes or removes" (DOC-1) |
| Only `last_synced_at` changes in the lock | `false` | §7.5.2 "The lock file is not a compared path." (SPEC-1); `docs/consuming/publishing.md` "The sync lock file is not compared." (DOC-1) |
| A prior-lock path, no longer materialized, replaced by the operator with a non-empty directory | The sync succeeds; the cleanup warning `sync: stale-file cleanup:` appears on standard error; `true`. The next sync no longer lists the path in its prior lock, so the effect lasts one run (accepted) | §7.5.2 "A compared path whose bytes cannot be read in the starting state, or a compared path from the prior lock file that the run does not materialize and whose bytes cannot be read in the final state, counts as altered." (SPEC-1); `docs/consuming/publishing.md` `$PODIUM_CHANGED` row (DOC-1) |
| A current path unreadable before the write where `materialize.Write` also fails (a directory at a materialized file path, or an unreadable config-merge or inject file) | The sync fails with the `materialize.Write` error it returns today; no lock is written | §7.5.2 (SPEC-1); no docs sentence is staged, because the failure is unchanged |
| A standalone materialized file that exists and cannot be read (a mode-000 file, or a root-owned 0600 file in a user-owned directory) | The sync succeeds and replaces the file, as it does today; `true` | §7.5.2 "A compared path whose bytes cannot be read in the starting state ... counts as altered." (SPEC-1); `docs/consuming/publishing.md` `$PODIUM_CHANGED` row (DOC-1) |
| A concurrent mutation makes a current path unreadable after the write | The sync fails with `sync: read target after materialize: ...` (accepted) | Fail-closed rule from code-best-practices; no spec or docs sentence is staged, because the case needs a concurrent writer |
| A concurrent writer edits a compared path between the two snapshots | Attributed to this run; can read `true` without a Podium change (accepted) | §7.5.2 defines the comparison over the starting and final states of materialization (SPEC-1) |
| The `prepare` phase clones, pulls, or rewrites a compared path | Part of the starting state; only what materialization and the stale-file cleanup then alter counts. A default marketplace run that clones into a fresh working directory reads `false` when the render matches the clone | §7.5.2 "A file the `prepare` phase clones, pulls, or rewrites is therefore part of the starting state." (SPEC-1); §7.8 Reconciliation and Pattern A paragraphs, unchanged |
| `--dry-run` or `--check` | No publish phase runs for a workspace target; `Result.Changed` is `false` | §7.5, unchanged; `docs/consuming/publishing.md` |
| An offline-first no-op (an unreachable server-source registry under `offline-first`) | The publish phase runs with `$PODIUM_CHANGED=false`: `skip_if_no_changes` commands are skipped and other publish commands run, as they do today (`cmd/podium/main.go:554`) | §7.4 and §7.5.2 (SPEC-1), unchanged |
| A marketplace `--dry-run` | The render goes to a temporary directory, as today. The printed `publish` commands substitute the variable computed against it. The printed `prepare` commands leave `$PODIUM_CHANGED` literal and carry no `skipped (no changes)` mark (CODE-4) | §7.5.2 "the `prepare` commands that `--dry-run` prints do not substitute it" (SPEC-1); §7.8 (unchanged) |
| The `prepare` phase reads `$PODIUM_CHANGED` | Unset for both kinds in a live run; left literal in the marketplace `--dry-run` preview (CODE-4) | §7.5.2 "The `prepare` phase runs before materialization and does not receive the variable, and the `prepare` commands that `--dry-run` prints do not substitute it." (SPEC-1) |
| Watch mode and `podium sync override` | Pay the two reads per compared path; output unchanged | No user-visible text; the "Cost" decision |

## Testing

**TEST-1 · unit, `pkg/sync/workspace_changed_test.go` (new).** Lands in S5. Every test carries `// Spec: §7.5.2`, and each case asserts `Result.Changed` from `sync.Run` over a `t.TempDir()` registry and target.

- (a) **Adapter output change.** Register a stub adapter through `Options.AdapterRegistry` (`adapter.NewRegistry`, `Register`) whose output for one artifact is switched by a test variable. Sync, switch the output format, re-sync with an unchanged registry and lock. Assert `Changed=true`. A third sync asserts `false`.
- (b) **Fold-order restore.** Use the codex adapter and two rules, `a-rules/policy` and `z-rules/style`, that inject into `AGENTS.md`. After the first sync, swap the two `podium:begin` and `podium:end` blocks by hand, leave the lock untouched, and re-sync. Assert `Changed=true` and that the blocks are back in ascending-ID order.
- (c) **Hand edit restored.** Overwrite one materialized file with other bytes, re-sync, and assert `Changed=true` and the original bytes on disk.
- (d) **Hand deletion restored.** Delete one materialized file, re-sync, and assert `Changed=true` and the file present again. Cases (c) and (d) run through different `changeSet` branches (differing digest and newly present).
- (e) **Lock-only rewrite.** Re-sync an unchanged target and assert `Changed=false`. No `last_synced_at` assertion.
- (f) **Missing prior lock.** After a first sync, delete `.podium/sync.lock` and re-sync. Assert `Changed=false`.
- (g) **Stale removal.** Remove an artifact from the registry and re-sync. Assert `Changed=true` and the file gone. A further re-sync asserts `false`.
- (h) **Operator entry in a merged file.** After a first claude-code sync of an `mcp-server` artifact, read `.mcp.json`, add an untagged `mcpServers` entry, and write it back with `json.MarshalIndent(v, "", "  ")` plus a trailing newline. Re-sync and assert `Changed=false` and the entry preserved. A sibling subtest writes the same entry with non-canonical formatting and asserts `Changed=true`, because the sync rewrites the bytes.
- (i) **Unreadable current path where the write fails.** Create a non-empty directory at a standalone path the run materializes before the first sync. Assert `Run` returns an error, that the error does not contain `sync: read target`, and that the target holds no lock.
- (m) **Unreadable standalone current path.** After a first sync, `chmod 000` one standalone materialized file and re-sync. Assert `Run` returns no error, `Changed=true`, and the file readable again with the adapter's bytes. A further re-sync asserts `Changed=false`. Skip the case when `os.Geteuid() == 0`, because root reads a mode-000 file.
- (j) **Unreadable prior-only path.** After a first sync, remove the artifact from the registry and replace its materialized file with a non-empty directory. Re-sync with standard error captured. Assert `Run` returns no error, `Changed=true`, and the stderr output contains `sync: stale-file cleanup:`. A further re-sync asserts `Changed=false`, because the path left the prior lock.
- (k) **Unemitted metadata.** A claude-code skill: edit only `tags:` in `ARTIFACT.md` and re-sync. Assert `Changed=false` and that the lock's `content_hash` for the artifact moved.
- (l) **Concurrency.** Two `Run` calls into distinct targets from one registry, under `t.Parallel`, assert independent `Changed` values. Run the package with `-race`.

Also in S5:

- `pkg/sync/sync_test.go`, `TestRun_ChangedTracksOnDiskDelta`: replace the comment with `// Spec: §7.5.2 — Result.Changed reports whether the sync altered the bytes on disk of a compared path. A first sync into an empty target alters them, an unchanged re-sync does not, a DryRun writes nothing, and an edit to an emitted artifact alters them again.` The body is unchanged.

In S3, with CODE-2, because CODE-2 makes the test's existing assertion fail and S3 must leave `pkg/sync` green:

- `pkg/sync/sync_order_test.go`, `TestRun_ChangedSeesEveryContributorToASharedPath`: replace the comment (`:102-107`) with `// Spec: §11 (idempotent re-sync), §7.5.2 — an edit to one of two artifacts that share .mcp.json reports Result.Changed true when it changes an emitted field and the merged file's bytes, and false when it changes only a field the adapter does not emit.` Remove the (artifact id, materialized path) key rationale. Change the body: after the unchanged re-sync, first write `mcpServerSrc("alpha", "Alpha server, revised.")` (the existing `:139` edit), re-sync, and assert `Changed=false`, because the claude-code fragment omits `description:` (`pkg/adapter/layout.go:185-187`). Then write the `a-alpha/server` source with `server_identifier: npx:@acme/alpha` replaced by `npx:@acme/alpha2`, re-sync, and keep the existing `!res.Changed` assertion and message. Replace the inline comment about the entry "a path-only key discards" with one stating that the `server_identifier` edit changes the merged `.mcp.json` bytes.

Run `go test -race ./pkg/sync/` and the cross-package profile `go test -coverpkg=./... -coverprofile=cover.out ./pkg/sync/ ./cmd/... ./test/integration/`, and confirm `writeTarget` and the tolerant read reach 85% with `go tool cover -func=cover.out`.

**TEST-3 · unit, `pkg/sync/marketplace_run_test.go`, `TestRunMarketplace_DryRunPrepareOmitsChanged` (new).** Lands in S10. Cite `// Spec: §7.5.2, §7.8`. Build an output with `runOutput(renderFixtureRegistry(t), []string{"claude-code"}, ...)` whose `prepare` holds ``{Sh: `echo "$PODIUM_CHANGED"`, SkipIfNoChanges: true}`` and whose `publish` holds `{Run: []string{"echo", "$PODIUM_CHANGED"}}`. Run `RunMarketplace` with `DryRun: true` and capture standard output. Assert that the `# prepare` section contains the literal `$PODIUM_CHANGED` and no `skipped (no changes)`, and that the `# publish` section contains `echo true`, because the dry run renders into an empty temporary directory (`TestRunMarketplace_DryRunReportsRenderedTreeChanged`, `pkg/sync/marketplace_run_test.go:463`). `TestRunMarketplace_DryRun` (`:396`) keeps passing, because `baseVars` carries `$PODIUM_GIT_REMOTE`.

**TEST-2 · e2e, `test/e2e/publishing_test.go`, `TestPublishing_WorkspaceTargetSkipIfNoChanges`.** Lands in S6.

Keep the two existing steps. Add:

3. Hand-edit one materialized file under the target and re-sync. Assert publish-count is 2 and standard error has no `skipped (no changes)` line for this run.
4. Re-sync unchanged. Assert publish-count stays 2 and `skipped (no changes)` appears.
5. Delete `<target>/.podium/sync.lock` and re-sync. Assert publish-count stays 2: the tree is already current, so the command is skipped.

Rewrite the doc comment to cite `// Spec: §7.5.2` and drop "Decision 3". Run `go test ./test/e2e/ -run TestPublishing_WorkspaceTargetSkipIfNoChanges -v` on Linux, or grep the output for `SKIP` before claiming verification, because parts of `test/e2e` skip silently on macOS.

Run `make coverage-gate` after S6. `speccov-drift` must still find a `// Spec: §11` citation for idempotent re-sync after `lock_changed_test.go` is deleted; `sync_order_test.go` and `pkg/registry/core/hybrid_test.go` carry it.

## Manual validation

**MV-1.** Add scenario S78 to `test/manual-validation.md` after S77, following the existing conventions. Lands in S9.

~~~~markdown
## S78: A workspace target's `$PODIUM_CHANGED` follows the bytes on disk

**Goal.** Validate that a `kind: workspace` target's `skip_if_no_changes`
publish command runs when a sync rewrites a materialized file, including one
restored after a hand edit, and is skipped when the sync leaves every
materialized file byte-identical, including after the lock file is deleted
and after an `ARTIFACT.md` edit the harness output does not carry.

**Covers.** The §7.5.2 definition of `$PODIUM_CHANGED` for both target kinds.

**Why by hand.** The end-to-end suite counts publish runs. What it does not
read is the operator's terminal: the `skipped (no changes)` line a CI log
shows, and whether a commit step would have run after a sync restored a file
a teammate edited by hand.

**Prerequisites.** A built `podium` binary on `PATH`.

**Steps.**

1. Run the isolation block from "Per-scenario isolation" above, then build a
   single-layer registry with one skill and a `sync.yaml` whose workspace
   target appends a line to a counter file on every publish run.

   ```bash
   mkdir -p "$WORK/reg/team/hello" "$WORK/ws/.podium"
   printf -- '---\ntype: skill\nversion: 1.0.0\ndescription: hello\ntags: [a]\n---\n' \
     > "$WORK/reg/team/hello/ARTIFACT.md"
   printf -- '---\nname: hello\ndescription: hello\n---\n\nSay hello.\n' \
     > "$WORK/reg/team/hello/SKILL.md"
   printf 'defaults:\n  registry: %s\ntargets:\n  - id: claude-workspace\n    kind: workspace\n    harness: claude-code\n    target: %s\n    workflow:\n      publish:\n        - sh: "echo run >> %s"\n          skip_if_no_changes: true\n' \
     "$WORK/reg" "$WORK/out" "$WORK/count" > "$WORK/ws/.podium/sync.yaml"
   ```

   **Expect.** `which podium` prints `$PODIUM_BIN/podium`, and both files
   under `$WORK/reg/team/hello` exist.

2. Sync twice.

   ```bash
   cd "$WORK/ws"
   podium sync --config .podium/sync.yaml; echo "exit=$?"
   podium sync --config .podium/sync.yaml 2> "$WORK/err2.txt"; echo "exit=$?"
   wc -l < "$WORK/count"; cat "$WORK/err2.txt"
   ```

   **Expect.** Both runs print `exit=0`. The counter holds 1 line, and
   `$WORK/err2.txt` contains `skipped (no changes)`.

3. Edit the materialized `SKILL.md` by hand and sync.

   ```bash
   echo "local edit" >> "$WORK/out/.claude/skills/hello/SKILL.md"
   podium sync --config .podium/sync.yaml 2> "$WORK/err3.txt"; echo "exit=$?"
   wc -l < "$WORK/count"; cat "$WORK/err3.txt"
   grep -c "local edit" "$WORK/out/.claude/skills/hello/SKILL.md"
   ```

   **Expect.** `exit=0`, the counter holds 2 lines, `$WORK/err3.txt` has no
   `skipped (no changes)` line, and `grep -c` prints `0` because the sync
   restored the file. A counter of 1 is the shipped behavior this step
   exists to catch: the restored file would stay uncommitted.

4. Delete the lock file and sync.

   ```bash
   rm "$WORK/out/.podium/sync.lock"
   podium sync --config .podium/sync.yaml 2> "$WORK/err4.txt"; echo "exit=$?"
   wc -l < "$WORK/count"; cat "$WORK/err4.txt"
   ```

   **Expect.** `exit=0`, the counter still holds 2 lines, and
   `$WORK/err4.txt` contains `skipped (no changes)`. A counter of 3 means the
   variable still follows the lock.

5. Change only `tags:` in the authored `ARTIFACT.md` and sync.

   ```bash
   printf -- '---\ntype: skill\nversion: 1.0.0\ndescription: hello\ntags: [a, b]\n---\n' \
     > "$WORK/reg/team/hello/ARTIFACT.md"
   podium sync --config .podium/sync.yaml 2> "$WORK/err5.txt"; echo "exit=$?"
   wc -l < "$WORK/count"; cat "$WORK/err5.txt"
   ```

   **Expect.** `exit=0`, the counter still holds 2 lines, and
   `$WORK/err5.txt` contains `skipped (no changes)`, because Claude Code's
   output carries `SKILL.md` and no `tags:` field. A counter of 3 means the
   variable still follows the source content hash.

**Cleanup.** `cd /` and `rm -rf "$WORK"`.
~~~~

The surfaces a human reads directly are the counter file, standard error, and the restored `SKILL.md`. Step 3 catches a missed restore, step 4 catches a missing-lock false positive, and step 5 catches an unemitted-metadata false positive.

## Documentation changes

**DOC-1.** Lands in S7. Target: `docs/consuming/publishing.md`. Define the variable once, in the injected-variables table, and point the other rows to that definition. Leave the paragraphs on reconciliation, triggers, and the scheduled pattern unchanged, because they describe marketplace git-diff behavior and remain accurate.

(a) The `$PODIUM_CHANGED` row of the injected-variables table. Replace "Whether the render produced a diff against the checkout." with:

> `true` when materialization changed the bytes on disk of a file Podium writes or removes, including a file it restores after a hand edit or deletion, and `false` otherwise. The comparison starts after the `prepare` phase, so a file that `prepare` clones or pulls is part of the starting state. A file Podium writes or removes that the run cannot read also reads `true`, such as a materialized file without read permission or a stale path from an earlier run that the operator replaced with a directory. The sync lock file is not compared. For a marketplace target the value equals whether the render produced a diff against the checkout. A `kind: workspace` target receives the variable with the same meaning in its `publish` phase.

(b) The `skip_if_no_changes` row of the per-command flag table. Replace "Skip the command when the render produced no diff against the checkout." with:

> Skip the command when `$PODIUM_CHANGED` is `false`. See [Injected variables](#injected-variables).

(c) The `workflow` row of the target-field table. Keep the existing text and change its last clause from "plus `$PODIUM_CHANGED` for the `publish` phase." to:

> plus `$PODIUM_CHANGED` for the `publish` phase, as defined under [Injected variables](#injected-variables).

No `tools/doccov/manifest.yaml` entry changes, because DOC-1 adds no runnable example.

**CL-1.** Lands in S8. Target: `CHANGELOG.md`, `## [Unreleased]`. Two edits.

(a) `### Fixed`. Replace the existing entry "**Change reporting over a shared materialized path** (§11)", whose lock-keyed mechanism CODE-2 deletes before it ships. Do not add a second entry. Replacement text:

> - **`$PODIUM_CHANGED` for a `kind: workspace` target reports a change to the files on disk** (§7.5.2, §7.8): a workspace target's `publish` phase now computes the variable as a marketplace target does, by comparing the bytes of every file the sync writes or removes before and after materialization. An edit to an artifact that changes the bytes Podium writes into a shared materialized path reads `true`, whichever artifact's lock entry survived before. A re-sync that rewrites files with a new adapter output format, restores Podium's entry order in a shared config file, or restores a hand-edited or deleted file also reads `true`, so a `skip_if_no_changes` command runs. A re-sync that leaves every file byte-identical reads `false`, including one without a prior `.podium/sync.lock`. The lock file is not compared.

(b) `### Changed`, the entry "**The §4.7.6 content hash length-frames its parts**" (`CHANGELOG.md:155`). Its upgrade note (`CHANGELOG.md:513-515`) ends "A `podium sync` rewrites the `content_hash` of every entry in its lock file on its first run against the new registry, and that run reports every target as changed." That clause describes `lockChanged`, which CODE-2 deletes in the same release, and contradicts entry (a) and TEST-1 case (k). Replace the sentence with:

> A `podium sync` rewrites the `content_hash` of every entry in its lock file on its first run against the new registry. That run reports a target as changed only when the bytes of its materialized files change, so a `skip_if_no_changes` command is otherwise skipped.

## Open questions

**OQ-1. The other workspace workflow variables.** `docs/consuming/publishing.md` lists `$PODIUM_WORKDIR`, `$PODIUM_TARGET_ID`, and `$PODIUM_REGISTRY` for a workspace workflow, and no spec section defines them. This proposal leaves them out. Should SPEC-1 also list them in §7.5.2, or should a follow-up proposal do it?

## Non-goals

- Changing the marketplace computation or its `RenderResult.Changed` and `ChangedArtifacts` values. Only the doc comments of the shared helpers change (CODE-1).
- Moving the change-detection helpers into a new file. They stay in `pkg/sync/render.go`, and `sync.go` calls them in place.
- Adding `$PODIUM_CHANGE_SUMMARY` or a change count to a workspace target.
- Adding a change field to the `podium sync --json` envelope or to the watch-mode output, or changing `OverrideResult.Changed`.
- Comparing the whole target directory, or adding a flag or option to choose the comparison scope or to turn digesting off in watch mode.
- Recording per-file digests in the lock file.
- Specifying the other variables a workspace workflow receives (`$PODIUM_WORKDIR`, `$PODIUM_TARGET_ID`, and `$PODIUM_REGISTRY`). This proposal defines only `$PODIUM_CHANGED` (OQ-1).
- Revising the remaining §7.5.2 citations in `pkg/sync/workflow_phase.go` and `pkg/sync/marketplace_run.go`. They cite the workflow runner and the execution boundary, which §7.5.2 covers.

## Resolved in adversarial review

Review rounds populate this section. The draft already applies these revisions from the challenge pass:

- SPEC-1 dropped the `--dry-run` and `--check` sentence, which contradicted §7.8 for a marketplace target, and dropped the implementation narration and the examples list, which now live in the decisions, CL-1, and TEST-1.
- SPEC-2 makes the §7.8 entry a pointer, and SPEC-1's last sentence names the checkout without restating the marketplace meaning, so §7.5.2 holds the only definition.
- SPEC-1 gained the sentence on an unreadable prior-lock path so the CODE-2 read-error rule has a spec basis.
- CODE-1 no longer moves the helpers to `pkg/sync/changeset.go`, because they are already shared at package scope. The CODE-2 revision note that `removedOwner` moves with `changeSet` is therefore void: `removedOwner` stays in `render.go`.
- CODE-2 reads prior-only paths tolerantly so a stale path the operator replaced does not abort watch mode or override, and reports `Changed=true` when such a path is unobservable.
- CODE-3 names the function-doc edit and scopes the comment to the compared paths.
- TEST-1 pins case (b) to codex inject into `AGENTS.md`, writes the case (h) operator entry in canonical form, drops the `last_synced_at` assertion, and rewrites the `sync_order_test.go` comment.
- DOC-1 targets the `skip_if_no_changes` row of the flag table and defines the variable in one row.
- CL-1 replaces the lock-keyed `### Fixed` entry and cites §7.5.2 and §7.8.

### Pass 1 (2026-10-02, automated)

- The edge-case row for `--dry-run`, `--check`, or an offline-first no-op is split. `--dry-run` and `--check` run no publish phase. An offline-first no-op runs the publish phase with `$PODIUM_CHANGED=false`, because `runWorkspaceTarget` gates the workflow only on `--check`, `--dry-run`, and an empty workflow (`cmd/podium/main.go:554`) and `offlineFirstNoop` returns a nil error (`pkg/sync/sync.go:243-245`). The "Snapshot placement" decision states the same behavior.
- DOC-1 (a) states that a file Podium writes or removes that the run cannot read reads `true`, so the docs row agrees with SPEC-1's unreadable-path rule and the edge-case rows that cite it.
- CL-1 gains edit (b): the content-hash upgrade note under `### Changed` (`CHANGELOG.md:513-515`) no longer says the first sync reports every target as changed, because CODE-2 deletes the lock comparison that produced that outcome. The S8 checklist step names both edits.
- MV-1 step 5 rewrites `ARTIFACT.md` with `printf` instead of `sed -i.bak`, which left `ARTIFACT.md.bak` in the skill directory. The filesystem registry captures that file as a bundled resource (`pkg/registry/filesystem/walk.go:272-287`) and the Claude Code adapter materializes it (`pkg/adapter/claudecode.go:54-55`), which would make a correct implementation read `true`.
- The before-snapshot now reads current paths tolerantly, as it already read prior-only paths. `materialize.Write` never reads a standalone target and replaces it by rename (`pkg/materialize/atomic.go:267-280`), so a fail-closed before-read would have turned a sync that succeeds today on a mode-000 file into a failure for watch mode and `podium sync override`. An unreadable path is unobservable and makes `Changed` true, and `materialize.Write` alone decides whether the run fails. The `sync: read target before materialize` error is removed. The Summary, the Watch-out entry on prior-only paths, the "Read errors" decision, SPEC-1, CODE-2 steps 1, 2, and 7, the IMPLEMENTOR'S CHOICE, the `Result.Changed` doc comment, the edge-case table, and TEST-1 cases (i) and the new (m) carry the rule.

### Pass 2 (2026-10-02, automated)

- TEST-1 changes the body of `TestRun_ChangedSeesEveryContributorToASharedPath` as well as its comment. Its existing edit changes only `description:` (`pkg/sync/sync_order_test.go:139`), which the claude-code `.mcp.json` fragment does not emit (`pkg/adapter/layout.go:185-187`), so the test would fail under CODE-2. The description-only edit now asserts `Changed=false`, and a `server_identifier` edit asserts `Changed=true`. The Watch-out entry and checklist step S5 say so.
- CL-1 (a) no longer says that any edit to an artifact in a shared materialized path reads `true`. It says an edit that changes the bytes Podium writes into the shared path reads `true`, which agrees with SPEC-1, the edge-case table, TEST-1 case (k), and MV-1 step 5.
- SPEC-1 anchors the comparison to a starting state taken when materialization starts, after the `prepare` phase, and a final state taken after materialization and the stale-file cleanup. The earlier "before and after the run" wording placed a default marketplace `prepare` clone inside the comparison (`spec/07-external-integration.md:870`), which would make the variable always `true`. The new "Comparison anchor" decision records the reasoning against `pkg/sync/render.go:313-315` and `cmd/podium/main.go:560-564`. The Summary, the option (b) decision, the `writeTarget` comment, the edge-case quotes, a new edge-case row for a `prepare` that writes a compared path, DOC-1 (a), and CL-1 (a) carry the same anchor.
- CODE-4 and TEST-3 make the marketplace `--dry-run` preview print the `prepare` phase with `baseVars`, the variables the live `prepare` phase receives. `runPipeline` printed `prepare` after assigning `$PODIUM_CHANGED` (`pkg/sync/marketplace_run.go:170`, `:174-176`), so the preview substituted the variable and could mark a `skip_if_no_changes` `prepare` command skipped (`:296`), which the live run never does. SPEC-1's `prepare` sentence names the preview, the two dry-run edge-case rows describe it, the "Publish phase only" decision cites the code, the Summary lists CODE-4 and TEST-3, and checklist step S10 carries both.
- The "Appearance and disappearance" decision now states its rules against the starting state and the final state that the "Comparison anchor" decision defines, rather than "before the run" and "after it". CODE-1's replacement comments for `onDiskDigests` and `changeSet` say "before materialization" and "before and after materialization", so they no longer write the rejected run anchor into `pkg/sync/render.go` (whose snapshots are taken after the `prepare` clone, `pkg/sync/render.go:313-315`).
- The Watch-out entry for `TestRun_ChangedSeesEveryContributorToASharedPath` now agrees with TEST-1 and checklist step S5: the description-only edit stays and asserts `Changed=false`, and a `server_identifier` edit is added that asserts `Changed=true`.

### Pass 3 (2026-10-02, automated)

- The rewrite of `TestRun_ChangedSeesEveryContributorToASharedPath` moves from checklist step S5 to step S3, which lands CODE-2. CODE-2 makes the test's existing `!res.Changed` assertion fail (`pkg/sync/sync_order_test.go:139`, `:146-147`), because the claude-code `.mcp.json` fragment omits `description:` (`pkg/adapter/layout.go:185-187`), so leaving the rewrite in S5 left `go test ./pkg/sync/` red at the end of S3. Steps S3 and S5, the CODE-2 deletions paragraph, the Watch-out entry, and the Testing section (a new "In S3, with CODE-2" list beside "Also in S5") name the step that now carries the edit. The test's content is unchanged.
