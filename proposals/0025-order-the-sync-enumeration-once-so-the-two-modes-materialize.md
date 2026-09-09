# Proposal 0025: Order the sync enumeration once, so the two modes materialize the same bytes, and finish the content-hash consolidation

- Issue: (to be filed)
- Status: Applied to spec (2026-09-09). Verified after 8 adversarial review rounds (13 findings fixed).
- Date: 2026-09-08

This document stages the proposed spec, code, test, and documentation changes. It does not modify any spec, code, or doc file. Apply the changes in the staged sections after sign-off.

## Summary

**What changes.**

- §7.5 states the materialization order: the resolved set is materialized in ascending canonical artifact ID order under either registry source, so two artifacts writing into one §6.7 config-merge or inject target compose in the same sequence in every deployment mode. `spec/07-external-integration.md` fixes layer order (§13.11.1) and fixes no artifact order today.
- §7.5.3 states that an artifact materializing more than one file contributes one `artifacts:` entry per file, and states the list's order.
- `pkg/sync/sync.go` sorts the materialization set by canonical ID inside `selectRecords`, the single point at which both registry sources converge and the last point at which a record can still be appended. That one sort reaches the merge fold, `Result.Artifacts`, and the lock together.
- `pkg/sync/sync.go` re-keys the change comparison by (artifact id, materialized path) so every artifact writing into a shared materialized path participates in `Result.Changed`.
- `cmd/podium-mcp/main.go` moves the §6.6 step-2 integrity gate onto `version.CanonicalContentHash`, which removes the last hand-rolled copy of the §4.7.6 composition. `pkg/sync/sync.go` and `pkg/sync/lockfile.go` get doc comments that describe what their code now does.
- New tests pin the composition of `version.CanonicalContentHash`, the ascending-ID materialization order through `sync.Run`, the change comparison over a shared materialized path, and the two modes' equivalence over a registry whose artifacts collide on one merge target and one inject target. The existing `contentHashFor` tests are corrected to assert both hash slots and to stop describing the either/or the branch removed.
- `CHANGELOG.md`'s unreleased `### Fixed` bullet is rewritten, because its causal claim about the lock's list order is wrong and its closing claim about the next sync does not hold.
- `test/manual-validation.md` gains S63, which compares the merged and injected files two sync modes write over one directory and reads the change signal through a workspace target's publish command.

**Fixed decisions.**

- The spec lands and is verified before the code. No section fixes an artifact enumeration order for either source, so the order is a specification gap and §7.5 states it before `pkg/sync` implements it.
- The order is ascending canonical artifact ID. It is what the server source already produces, and it is the only order both sources can produce, because the sync manifest carries layer IDs and never the tenant's `layer_order:`.
- The sort lands in `selectRecords`, after scope filtering, after the §6.4 overlay tail, and after the `toggles.add` tail. It does not land in `filesystem.Registry.Walk`, and it does not land at the lock's write point.
- §4.6 precedence, the collision policy, and which record wins are untouched. Ordering applies to the already-resolved set.
- The composition rule is stated for the two kinds of shared target separately, because the code composes them by different mechanisms: `deepMerge` folds the JSON config-merge targets by value kind, and `injectBlock` splices whole marker-delimited blocks on `.codex/config.toml`, `AGENTS.md`, and `GEMINI.md`. SPEC-1 is the only site that states the rule, and every other site points at it.
- `WriteLock` keeps its sort. It is the single point every lock writer passes, and §7.5.3 states the list order as a property of the file.
- The §6.6 step-2 gate delegates to `version.CanonicalContentHash`. `version.CanonicalContentHash` itself is not changed, and no caller list is added to its doc comment.
- Podium is pre-1.0. No flag, environment variable, or `sync.yaml` key selects an enumeration order, and no compatibility path preserves the old one. A lock written by an older build is rewritten by the next sync.

**Watch out for.**

- **The sort changes one observable outcome inside a shared target, and SPEC-1's third and fourth sentences state the rule at the granularity the code has.** The third sentence covers the JSON config-merge targets `deepMerge` folds, and the fourth covers the marker-block targets `injectBlock` splices, which is where `.codex/config.toml` belongs despite §6.7 listing it as a config-merge target. SPEC-1 is the only site that states the rule. The Edge cases table indexes it by sentence, CODE-1's comment points at §7.5, and DOC-1's second item leaves the restatement to the implementor under a constraint. Do not write "the last artifact supplies the key's value" anywhere; it is false for the two collision cases this proposal tests. Do not state a key-level merge rule over `.codex/config.toml`, `AGENTS.md`, or `GEMINI.md`; those targets compose whole blocks.
- **`Result.Changed` cannot see a fold-order change.** `lockChanged` compares recorded artifact content hashes per lock entry and never reads the materialized bytes, so a registry whose only difference is the fold order rewrites the merged file while reporting no change. The Edge cases table records that as an accepted failure mode, and Non-goals records the one-time `skip_if_no_changes` skip it produces. Do not write a test or a manual step that expects a changed result from a reordered fold.
- **Do not remove `WriteLock`'s sort.** An earlier draft did. `lockChanged` compares maps, which are order-insensitive, so the sort explains none of the branch's spurious-change regression, and removing it would leave the override and save-as rewrite paths building a file whose order §7.5.3 states.
- **The spurious-change regression and the missed-change defect are separate.** CODE-1 removes the spurious-change arm by making the two orders equal. CODE-3 closes the pre-existing missed-change arm, where an edit to the artifact whose lock entry does not survive the per-path collapse reports no change. Do not attribute either to the other.
- **`version.CanonicalContentHash` does not length-frame its parts.** `CanonicalContentHash([]byte("x"), nil, {"ab":"c"})` and `CanonicalContentHash([]byte("x"), nil, {"a":"bc"})` yield the same digest. Do not write a test asserting that a resource key is unambiguously separated from its value; the code does not have that property, and asserting it plants a false invariant in a mirrored surface.
- **Do not add `codex` to the existing equivalence loop.** The reference fixture carries a `type: command` artifact, which is an unsupported §6.7.1 cell for codex, so `sync.Run` aborts with `materialize.untranslatable`. Inject-harness equivalence comes from the new test's own fixture, which carries no command.
- **The `none` adapter cannot exercise a shared merge target.** It emits per-artifact `OpWrite` paths only. Any test arm about a shared path needs `claude-code` or `codex`.
- **`contentHashFor`'s existing unit tests pass identically before and after the branch's fix,** because each sets at most one of the two hash slots and a zero-length slot contributes no bytes. Do not read them as evidence that the consolidated hash is pinned; the integration equivalence test is what fails against the old body.

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. §7.5 states the ascending canonical artifact ID materialization order, its scope against §4.6 and §6.4, and its effect inside a shared target of either kind, the JSON config-merge targets and the marker-block targets. Committed alone and verified before any code.
      Levels: —. Depends on: —
- [ ] **S2 · spec** — SPEC-2. §7.5.3 states the one-entry-per-file rule and the `artifacts:` list order.
      Levels: —. Depends on: S1
- [ ] **S3 · code** — CODE-1. `selectRecords` returns the materialization set in ascending canonical ID order.
      Levels: unit, integration. Depends on: S1, S2
- [ ] **S4 · code** — CODE-2. `WriteLock`'s doc comment names §7.5.3 as the invariant's source and states that sync already arrives in that order. The sort and its key are unchanged, so the step reaches no test level.
      Levels: —. Depends on: S2, S3
- [ ] **S5 · code** — CODE-3. The change comparison keys by (artifact id, materialized path), so every contributor to a shared path participates.
      Levels: unit. Depends on: S3
- [ ] **S6 · code** — CODE-4. The §6.6 step-2 integrity gate delegates to `version.CanonicalContentHash`.
      Levels: unit. Depends on: —
- [ ] **S7 · code** — CODE-5. `contentHashFor`'s doc comment describes the composition the code now uses. The function body is unchanged, so the step reaches no test level.
      Levels: —. Depends on: —
- [ ] **S8 · test** — TEST-1. One unit test pinning `version.CanonicalContentHash`'s slot order and sorted-path rule.
      Levels: unit. Depends on: —
- [ ] **S9 · test** — TEST-2. The `contentHashFor` unit tests assert both hash slots and stop describing the removed either/or.
      Levels: unit. Depends on: S7
- [ ] **S10 · test** — TEST-3. `pkg/sync/sync_order_test.go` pins the ascending-ID order through `Run`, including the toggle-add tail, and pins the shared-path change comparison.
      Levels: unit. Depends on: S3, S5
- [ ] **S11 · test** — TEST-4. The §11 equivalence test gains one function over a test-local colliding fixture, run under `claude-code` and `codex`, and its stale `assertLockArtifactsEqual` comment is corrected. The existing `none` / `claude-code` loop is not edited.
      Levels: integration. Depends on: S3, S4
- [ ] **S12 · docs** — DOC-1. `CHANGELOG.md`'s unreleased `### Fixed` bullet is rewritten as the items listed under DOC-1, and `test/manual-validation.md` gains S63.
      Levels: manual. Depends on: S10, S11

**Ordering constraints.** S1 precedes S2 because §7.5.3's sentence states in the lock file what §7.5 fixes for the materialization. Both precede the code per `.claude/rules/spec-driven-development.md`. S6, S7, S8, and S9 form the content-hash lane the branch left half-finished. S6, S7, and S8 depend on nothing, S9 depends on S7 alone, and a reviewer can take the lane without waiting for the ordering work. S12 follows the tests so the CHANGELOG describes what the tested build does.

## Current state and the gap

### The two sync sources enumerate artifacts under different rules

`filesystem.Registry.Walk` returns layer-major order with alphabetical canonical IDs inside each layer (`pkg/registry/filesystem/walk.go`), and `filesystemRecords` copies it unchanged (`pkg/sync/sync.go`). The server source reads `GET /v1/sync/manifest` (`pkg/sync/server.go`), which serves `core.EffectiveView` verbatim (`pkg/registry/server/sync_manifest.go`), and that view is globally ID-sorted by `dedupeLatest` (`pkg/registry/core/effective_view.go`, `pkg/registry/core/domain_load.go`).

That difference reaches the merge fold unpermuted. `resolveRecords`, `applyOverlay`, `selectRecords`, and `ScopeFilter.filterMaterial` (`pkg/sync/sync.go`, `pkg/sync/scope.go`) all preserve input order; the Adapt loop appends each artifact's output into `allFiles` in record order (`pkg/sync/sync.go`), and `materialize.Write` hands the slice straight to `foldOps` (`pkg/materialize/atomic.go`), which folds `OpInject` and `OpMergeJSON` fragments in input order (`pkg/materialize/merge.go`). `deepMerge` concatenates same-key arrays destination-first and assigns a scalar key from the last fragment folded, and `injectBlock` appends an absent key's block at the end after the per-sync strip. Object-member order is canonicalized by `json.MarshalIndent`, so the divergence is confined to same-key JSON arrays, a scalar key two artifacts contend for, `OpInject` block sequences in one text file, and `OpWrite` last-write-wins.

§11 requires the two modes to produce byte-identical harness-adapter output and lock `artifacts:` list, and §2.2 names merge logic among the concerns with one canonical implementation, so this is a specification violation.

### The suite cannot see it

The reference fixture carries exactly one hook and one mcp-server (`testdata/registries/reference/`); every other type materializes to a per-artifact path. The equivalence test loops over `none` and `claude-code` only (`test/integration/sync_equivalence_test.go`), so no inject-based harness is exercised at all, and `pkg/materialize/merge_test.go` builds the colliding case but asserts containment rather than element order.

### The branch treats the symptom

Branch `fix/filesystem-lock-content-hash` (PR 121, commit e33c749) consolidates the content hash correctly and then sorts the lock at its write point (`pkg/sync/lockfile.go`). That makes the recorded list agree while the materialized bytes stay mode-dependent. The write-point sort is a correct property of the lock file, and it is the wrong place to close the equivalence gap, because the bytes the adapter writes never pass through it.

### The change comparison collapses a shared materialized path

`Result.Changed` comes from `lockChanged` (`pkg/sync/sync.go`), which compares path-to-hash maps built by `lockPathHashes`. That helper keys on `MaterializedPath` alone, so a path two artifacts write collapses last-writer-wins. Shared paths are the normal case: `.mcp.json` (`pkg/adapter/claudecode.go`), `.cursor/mcp.json` and `.gemini/settings.json` (`pkg/adapter/builtins.go`), `.claude/settings.json` for hooks, and `AGENTS.md` for codex rules (`pkg/adapter/layout.go`, `pkg/adapter/codex.go`). Editing the artifact whose entry does not survive the collapse leaves `Result.Changed` false though the file was rewritten, and a `skip_if_no_changes` publish command then skips a target that changed. The existing test uses one artifact on a per-artifact path, so the behavior is unpinned.

### The realignment is unconstrained by §4.6

Precedence is carried by the layer iteration and the dedupe map in `pkg/registry/filesystem/walk.go`, both fully resolved before `Walk` returns; on a collision the winner takes the loser's slot, so the emitted slice is not strictly layer-major anyway. §4.6 governs which record survives a canonical-ID collision, and it does not speak to how two distinct artifacts' fragments compose inside one shared merge target. No spec text fixes an order for either source, so the order has to be stated before it can be implemented.

One consequence has to be stated with the order. A merged config entry is keyed by the artifact's `name:` frontmatter rather than by its canonical ID (`mcpName`, `pkg/adapter/layout.go`), so two artifacts with distinct canonical IDs can write one key and the enumeration order decides how `deepMerge` (`pkg/materialize/merge.go`) composes their fragments. SPEC-1's third sentence states that composition.

### The corrections around the branch stand

`contentHashFor`'s doc comment still describes the removed either/or (`pkg/sync/sync.go`), as do the comments and the test name in `pkg/sync/lock_content_hash_test.go`. A hand-rolled copy of the canonical composition survives in the §6.6 step-2 integrity gate (`cmd/podium-mcp/main.go`), which is the only remaining non-test caller of `version.ContentHash`, and its doc comment describes the registry's `contentHashOf` as "contentHashOf over version.ContentHash", a description that is stale now that `contentHashOf` delegates to `version.CanonicalContentHash`. `version.CanonicalContentHash` has no direct pin on its composition: it reports full coverage from its callers, and all three `contentHashFor` tests set at most one of `ArtifactBytes` and `SkillBytes`, so a zero-length slot contributes no bytes and each passes identically before and after the branch's fix. The unreleased CHANGELOG entry carries claims that do not hold.

## Spec amendment: §7.5 the materialization enumeration order

**SPEC-1.** Anchor: `spec/07-external-integration.md`, §7.5, the paragraph beginning "The sync command reads the caller's effective view". The text below is appended to that paragraph. The two paragraphs that follow it, on registry-source dispatch and on type-agnostic sync, are not edited.

> The resolved set is materialized in ascending canonical artifact ID order, whatever the registry source and whatever the layer order that composed it, so two artifacts writing into one §6.7 config-merge or inject target compose in the same sequence in every deployment mode. Ordering is applied after layer composition (§4.6) and after the workspace overlay (§6.4), so it selects no artifact and overrides no precedence, and a lower-precedence artifact's fragment can therefore land later inside a shared target than a higher-precedence one. Where two artifacts contribute to one key of a JSON config-merge target (`.claude/settings.json`, `.mcp.json`, `.cursor/hooks.json`, `.cursor/mcp.json`, `.gemini/settings.json`, and root `opencode.json`), their fragments are folded in that order and composed by value: entries a shared array key receives are concatenated in fold order, an object key is merged member by member, and a scalar value both artifacts set is supplied by the artifact whose canonical ID sorts last. A marker-block target composes whole Podium-managed blocks in that same order and merges no keys, so on `.codex/config.toml`, `AGENTS.md`, and `GEMINI.md` two artifacts naming one destination each contribute their own block and the contents of those blocks do not compose. The ordering therefore governs composition within a shared target and does not change which artifact a canonical-ID collision resolves to.

The composition rule is stated separately for the two kinds of shared target because the code composes them by different mechanisms. `applyOp` routes `OpMergeJSON` to `mergeJSON` and thence to `deepMerge`, and routes `OpInject` to `injectBlock`, which splices a whole marker-delimited block with no key-level merging (`pkg/materialize/merge.go`). §6.7 lists `.codex/config.toml` among the config-merge targets, but the codex adapter emits `OpInject` for both its config-merge types (`pkg/adapter/codex.go`, `codexHookOut` in `pkg/adapter/layout.go`), so a value-kind rule stated over every §6.7 config-merge target would misdescribe the harness this proposal's own TEST-4 arm exercises. The distinction is observable: `mcpName` keys a merged entry by the artifact's `name:` frontmatter (`pkg/adapter/layout.go`), so two mcp-servers sharing a name deep-merge into one object on a JSON target and emit two `[mcp_servers.<name>]` blocks on `.codex/config.toml`.

The order is ascending canonical artifact ID because that is the only order both sources can produce. The sync manifest carries each artifact's layer ID and never the tenant's `layer_order:`, so a server-source consumer cannot reconstruct layer-major order, and ID order is stable under a layer rename or a `layer_order:` edit, which keeps a committed lock (§14.11) diffing on what changed. §11 already states the obligation the order satisfies, so §7.5 states the rule without restating the obligation. §7.5 already carries test citations, so this edit creates no new `speccov-drift` obligation.

## Spec amendment: §7.5.3 the lock file's `artifacts:` list

**SPEC-2.** Anchor: `spec/07-external-integration.md`, §7.5.3, the paragraph beginning "The lock file is written atomically". The two sentences below are inserted after that paragraph's first sentence, before "The `profile:` field is the **active profile** for that target."

> An artifact that materializes more than one file contributes one `artifacts:` entry per file. The list is ordered by `id`, and the entries an artifact contributes are ordered among themselves by `materialized_path`.

The schema block above the paragraph shows one entry per artifact, while a skill and any artifact that writes both a body and a registration occupy several entries, and no spec text says so. §11 requires the list to agree across modes and §14.11 has teams commit it, so its order is observable. SPEC-1 fixes the materialization order; this states what that yields in the file, plus the per-file rule SPEC-1 does not reach. §7.5.3's silence caused no divergence: one lock builder serves both registry sources, and the divergence is upstream in the two enumerations.

## Proposed solution

### CODE-1: order the materialization set by canonical ID in `selectRecords`

`selectRecords` (`pkg/sync/sync.go`) returns the sorted set. It is the single point both sources converge on and the last point at which a record can still be appended: it runs after `ScopeFilter.filterMaterial`, after the `toggles.add` tail, after the `toggles.remove` pass, and after `resolveRecords` has merged the §6.4 overlay tail. Ordering here reaches the merge fold, `Result.Artifacts`, and the lock in one place.

```go
// The materialization set is returned in ascending canonical artifact ID order
// (§7.5), which is the order both registry sources can produce and the order
// the merge fold, Result.Artifacts, and the lock all inherit.
//
// It selects no artifact: a canonical-ID collision is resolved by Walk before
// the records arrive (pkg/registry/filesystem/walk.go), and applyOverlay
// replaces in place. It does decide the fold order inside a shared merge
// target, and there the key is not the canonical ID: mcpName keys the
// mcpServers entry by the artifact's name: frontmatter (pkg/adapter/layout.go),
// so two distinct IDs can write one key. How a shared target composes once
// the fragments arrive in this order is stated in §7.5.
// Filesystem mode previously folded in layer-precedence order
// and server mode in ID order; this makes both fold in ID order, which is the
// only order a server source can produce, since the sync manifest carries
// layer IDs and never the tenant's layer_order:.
sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
return out
```

**IMPLEMENTOR'S CHOICE:** how the comment refers to the composition inside a shared target, subject to the reference pointing at §7.5 rather than restating the rule. The comment must not state a key-level merge rule over an `OpInject` target. The three statements the comment owns are fixed: the sort selects no artifact, `mcpName` keys a merged entry by the artifact's `name:` frontmatter so two canonical IDs can write one key, and filesystem mode previously folded in layer-precedence order.

`sort` and `strings` are already imported by the file, so the edit adds no import. IDs are unique after dedupe, so the stability is decorative.

Sorting inside `filesystem.Registry.Walk` instead is strictly larger and does not work: its layer-major order is documented on the function and pinned by `pkg/registry/filesystem/walk_test.go`, it is read by `cmd/podium/main.go` outside sync, and sorting at either source alone would leave the §6.4 overlay tail and the §7.5.5 `toggles.add` tail unordered in both modes.

### CODE-2: retarget `WriteLock`'s doc comment

`pkg/sync/lockfile.go` keeps its `slices.SortStableFunc` block and its `slices` and `strings` imports. The doc paragraphs above `WriteLock` explain the sort by the two consumers having walked their sources in different orders, which is the reason SPEC-1 and CODE-1 relocate to §7.5. They are replaced with the invariant's new owner:

> The artifacts list is sorted here by id and then materialized path because §7.5.3 states that order as a property of the lock file, and `WriteLock` is the one point every writer passes: `sync.Run`, the §7.5.5 override paths, and the §7.8 publish render. Sync arrives already in this order, because §7.5 materializes in canonical artifact ID order and appends one entry per file an artifact writes with each artifact's files already sorted, so the sort is a no-op on that path; it is what keeps an override rewrite and a lock written by an older build normalized to the same order.

**IMPLEMENTOR'S CHOICE:** the exact wording, subject to the comment naming §7.5.3 as the source of the invariant, naming `WriteLock` as the single point every writer passes, and stating that the sync path arrives already ordered. The sort itself and its key are not changed.

### CODE-3: key the change comparison by (id, materialized path)

`lockPathHashes` is renamed to `lockEntryHashes` and its single map write changes from a path key to an (id, path) key (`pkg/sync/sync.go`):

```go
// lockEntryHashes returns the content hash recorded against each (artifact id,
// materialized path) pair in a lock. The key carries the artifact ID because a
// materialized path is shared whenever two artifacts config-merge or inject into
// one file (two mcp-servers on .mcp.json, two hooks on .claude/settings.json,
// two rules on AGENTS.md), and a path-only key kept just the last entry, so
// editing the artifact whose entry did not survive reported no change though the
// file was rewritten. A nil lock returns an empty map.
// spec: §11 (idempotent re-sync).
out[a.ID+"\x00"+a.MaterializedPath] = a.ContentHash
```

`lockChanged` is unchanged apart from the two call sites and one added sentence in its doc comment. Equal map sizes plus equal values per key is map equality, so the existing `len` short-circuit and the value loop remain correct.

CODE-1 removes the spurious-change arm the branch introduced, by making the two modes' orders equal. CODE-3 closes the pre-existing missed-change arm alone, and it is cleanly separable from the ordering work.

### CODE-4: move the §6.6 step-2 gate onto `version.CanonicalContentHash`

`verifyContentHash` (`cmd/podium-mcp/main.go`) keeps its slot-0 selection between `resp.Frontmatter` and `resp.RawFrontmatter`, and replaces the hand-rolled composition with the shared function:

```go
// resp.Resources is map[string]string on the wire; the canonical
// composition takes bytes.
resources := make(map[string][]byte, len(resp.Resources))
for k, v := range resp.Resources {
    resources[k] = []byte(v)
}
got := "sha256:" + version.CanonicalContentHash(artifactBytes, []byte(resp.SkillRaw), resources)
```

The doc paragraph that begins "The recomputation reproduces the registry's ingest canonicalization (contentHashOf over version.ContentHash)" is rewritten to say the recomputation reproduces the registry's ingest canonicalization through the shared `version.CanonicalContentHash`. The two bullets about `skill_raw` and `raw_frontmatter` and the slot-0 comment are unchanged. The `sort` import is dropped if nothing else in the file uses it.

The refactor is byte-for-byte behavior-preserving: both forms append slot 0, then slot 1 unconditionally, then each resource key and value under a sorted key walk, and the `map[string]string` to `map[string][]byte` conversion is exact. `pkg/version/version.go` is not edited, and no caller list is added to `CanonicalContentHash`'s doc comment; the mirrored surface is the composition, which the delegation itself now guarantees.

### CODE-5: correct `contentHashFor`'s doc comment

```go
// contentHashFor computes the §7.5.3 content_hash for a filesystem-source
// record from its served bytes, through version.CanonicalContentHash so the
// registry's ingest and this consumer cannot compute it differently (§4.6,
// §11). The record supplies all three slots: the manifest bytes, the SKILL.md
// bytes when the artifact carries one, and every bundled resource, inline and
// large alike. The result carries the spec's "sha256:<hex>" prefix, which
// CanonicalContentHash omits. spec: §4.7.6, §7.5.3, §14.11.
func contentHashFor(rec materialRecord) string {
```

This corrects two errors. The comment describes the either/or the branch removed, and it says "each large resource in sorted key order" while `rec.Resources` carries inline and large resources both (`pkg/sync/records.go`). The slot order's rationale and the frontmatter-only-edit consequence stay on `version.CanonicalContentHash`, where they already are.

## Edge cases and accepted failure modes

| Case | Observable outcome | Where it is stated |
|:--|:--|:--|
| Two artifacts write one key of a JSON config-merge target | Their fragments fold in ascending canonical ID order in both modes. A shared array key concatenates its entries in that order, a shared object key merges member by member, and a scalar leaf both artifacts set comes from the artifact whose canonical ID sorts last. In filesystem mode this changes the concatenation order and can change which layer supplies a contended scalar leaf, because that mode folded in layer-precedence order | SPEC-1's third sentence; `CHANGELOG.md` per DOC-1; pinned by TEST-4's claude-code arm |
| Two mcp-servers whose `name:` frontmatter collides on one `mcpServers` key | On a JSON target their entries deep-merge into one object rather than one replacing the other, so a URL-based and a command-based server sharing a name yield an entry carrying `url`, `command`, and `args` together. On `.codex/config.toml` the same pair emits two blocks carrying one `[mcp_servers.<name>]` table header. Pre-existing and unchanged; only the fold order is fixed here | Recorded in Non-goals |
| Two artifacts inject into one marker-block target (`.codex/config.toml`, `AGENTS.md`, or `GEMINI.md`) | Their blocks appear in ascending canonical ID order in both modes, and their contents do not compose | SPEC-1's fourth sentence; pinned by TEST-4's codex arm |
| A workspace overlay artifact (§6.4) | Ordered with the rest by canonical ID rather than appended at the tail. It still replaces in place where it overrides a registry artifact, so the overlay's precedence is unchanged | SPEC-1's second sentence |
| A `toggles.add` entry (§7.5.5) | Ordered with the rest rather than appended at the tail, so a toggled-in artifact whose ID sorts first materializes first | SPEC-1; pinned by TEST-3 |
| A canonical-ID collision across layers | Unchanged. The winner is resolved before the records reach sync, and the ordering applies to the resolved set | SPEC-1's second sentence; §4.6 |
| A lock written by an older build | Rewritten by the next sync, in the stated order. No migration path, and `WriteLock` normalizes any lock that passes through it | Recorded in Non-goals; `CHANGELOG.md` per DOC-1 |
| The first sync after this change, against a skill-carrying target | Reports the target as changed, because the recorded content hash moves. A `skip_if_no_changes` workflow runs once | `CHANGELOG.md` per DOC-1 |
| The first sync after this change, against a target with a shared merge path and no skill | The merged file is rewritten once with the same entries in a different order, while `Result.Changed` and `$PODIUM_CHANGED` stay false, because the comparison reads the recorded artifact content hashes rather than the materialized bytes and no hash moved. A `skip_if_no_changes` command therefore skips that one rewrite | Recorded in Non-goals; `CHANGELOG.md` per DOC-1 |
| An edit to the artifact whose lock entry did not survive the per-path collapse | Now reports changed. Previously reported unchanged, so a `skip_if_no_changes` publish command skipped a target that had been rewritten | CODE-3; pinned by TEST-3's second case |
| Two artifacts contributing the same fragment to one merged array | Both fragments are concatenated, with no deduplication inside a render. Unchanged and accepted; only the concatenation order is fixed here | Recorded in Non-goals |
| A resource key and value whose concatenation is ambiguous | `version.CanonicalContentHash` does not length-frame its parts, so a resource named `ab` with body `c` and one named `a` with body `bc` hash alike. Pre-existing and accepted; changing it is a §4.7.6 question | Recorded in Non-goals; TEST-1 asserts no such property |
| The marketplace render path's lock (§7.8) | Unchanged. It builds from sorted paths with empty IDs, and satisfies the (id, materialized path) rule trivially | Recorded in Non-goals; SPEC-2 |
| Concurrent `podium sync` writers against one target | Undefined, as today. Ordering changes nothing here | §7.5.3, existing text |

## Testing

**TEST-1 · unit, `pkg/version/`.** One test in `pkg/version/version_test.go`.

```go
// Spec: §4.7.6 (canonicalized manifest + bundled resources) — the slot order
// and the sorted-path rule are implementation decisions this test pins so the
// registry's ingest, the filesystem consumer, and the §6.6 gate cannot drift.
// §4.7.6 fixes neither, so nothing here is read out of the spec text.
func TestCanonicalContentHash_ComposesManifestSkillAndSortedResources(t *testing.T)
```

It builds one call with resources supplied in reverse insertion order and asserts equality with `ContentHash(artifact, skill, []byte("a.md"), []byte("A"), []byte("ref.md"), []byte("R"))`, which pins the slot order and the sorted-path rule in one assertion. The function reports full line coverage from its callers today, so this is a direct pin on the composition rather than a coverage fix. No assertion is written about a resource key being unambiguously separated from its value; the code does not have that property.

**TEST-2 · unit, `pkg/sync/`.** Two edits in `pkg/sync/lock_content_hash_test.go`.

`TestContentHashFor_SkillBytesAndResources` also sets `ArtifactBytes` on the record and asserts against `version.ContentHash(art, skill, []byte("a.md"), []byte("A"), []byte("ref.md"), []byte("R"))`. That is the assertion `pkg/sync` owes on its own: both hash slots are wired through, in that order. `version.ContentHash` is variadic `...[]byte`, so the resource key and value need `[]byte` conversions.

`TestContentHashFor_FallsBackToArtifactBytes` is renamed to `TestContentHashFor_NoSkillHashesManifestAlone`, and the file's comments are rewritten to state that an absent SKILL.md contributes no bytes rather than that the manifest is hashed instead. The `// spec: §7.5.3, §14.11` annotations stay.

The regression itself is pinned at the integration level: with the pre-branch `contentHashFor` restored, `TestSyncEquivalence_FilesystemVsServerByteIdentical` fails on the reference fixture's skills. This change corrects the record and adds the one `pkg/sync`-level assertion.

**TEST-3 · unit, `pkg/sync/sync_order_test.go`** (new file, package `sync`). Two cases.

```go
// Spec: §7.5 — the resolved set is materialized in ascending canonical
// artifact ID order whatever the layer order that composed it, so the merge
// fold, Result.Artifacts, and the lock all inherit one order.
func TestRun_MaterializesInCanonicalIDOrder(t *testing.T)
```

A two-layer filesystem fixture whose IDs interleave against layer order: `.registry-config` with `multi_layer: true` and `layer_order: [org-defaults, team-finance]`, `org-defaults/z-tools/lint/ARTIFACT.md`, `org-defaults/a-audit/check/ARTIFACT.md`, and `team-finance/a-finance/close/ARTIFACT.md`. The third artifact carries the lowest canonical ID (`a-audit/check`) and exists so the toggle arm below has an ID a scope can exclude. Run with no `Scope` and `AdapterID: "none"`, assert that `res.Artifacts` IDs ascend and that `ReadLock` returns the same order. The `ReadLock` arm is a §7.5.3 assertion rather than a witness for §7.5, because `WriteLock` normalizes the file; the `res.Artifacts` arm is the §7.5 witness.

The same test covers the toggle tail rather than calling `selectRecords` directly, and it has to reach the tail to be worth writing. `Run` reads the prior lock's toggles only when `Options.PreserveToggles` is true (`pkg/sync/sync.go`), and `selectRecords` skips a toggled ID that the scoped set already carries, so a run with an empty scope and toggles cleared exercises nothing. The arm therefore mirrors `TestRun_PreserveTogglesAppliesAddAndRemove` (`pkg/sync/scope_run_test.go`): seed the target lock with a `toggles.add` entry for `a-audit/check`, re-run with `PreserveToggles: true` and `Scope{Include: []string{"z-tools/**", "a-finance/**"}}`, which carries the other two artifacts and excludes `a-audit/check`, and assert that `a-audit/check` lands at the head of `res.Artifacts` rather than at the tail. The record then reaches `out` only through the `toggles.Add` append, so the assertion fails against a build with no sort.

```go
// Spec: §11 (idempotent re-sync), §7.5.3 — every artifact writing into a shared
// materialized path participates in the change comparison, so an edit to the
// artifact whose lock entry does not survive the per-path collapse still
// reports changed.
func TestRun_ChangedSeesEveryContributorToASharedPath(t *testing.T)
```

Two mcp-server artifacts that land in one `.mcp.json` under `AdapterID: "claude-code"`. Sync twice, edit the artifact whose ID sorts first, sync again, and assert `Changed == true`. The case fails today against the path-only key and passes after CODE-3. The `none` adapter cannot carry this case: it emits per-artifact `OpWrite` paths only. The unchanged direction is asserted by TEST-4's second `Run` and by the existing `TestRun_ChangedTracksOnDiskDelta`.

**TEST-4 · integration, `test/integration/sync_equivalence_test.go`.** One new function.

```go
// Spec: §11 (Filesystem ↔ server equivalence test) / §2.2 — two artifacts
// contending for one §6.7 config-merge target and two contending for one inject
// target compose identically under both registry sources, which the reference
// fixture cannot show because it carries one artifact per merge destination.
func TestSyncEquivalence_SharedMergeTargetsAreByteIdentical(t *testing.T)
```

It builds one test-local registry (`.registry-config` with `multi_layer: true` and `layer_order: [org-defaults, team-finance]`) holding two `hook_event: pre_tool_use` hooks, two rules, and no `type: command` artifact. Each colliding pair straddles the two layers, with canonical IDs that sort opposite to `layer_order`: the hooks are `org-defaults/z-hooks/notify/ARTIFACT.md` and `team-finance/a-hooks/audit/ARTIFACT.md`, and the rules are `org-defaults/z-rules/style/ARTIFACT.md` and `team-finance/a-rules/policy/ARTIFACT.md`. The straddle is what makes the arms non-vacuous. `Walk` emits alphabetically by canonical ID within a single layer (`pkg/registry/filesystem/walk.go`), which is the order `dedupeLatest` produces globally for the server source (`pkg/registry/core/domain_load.go`), so a pair sitting inside one layer folds in the same sequence under both sources with or without CODE-1's sort, `assertTreesEqual` passes either way, and the arm carries no signal about the change it exists to pin. It runs the filesystem source and an in-process `server.NewFromFilesystem` source through the existing `assertTreesEqual` and `assertLockArtifactsEqual` helpers for `claude-code` and `codex`, and asserts that a second `Run` against each target reports `Changed == false` (§11's idempotency bullet).

The two harnesses exercise different collisions over the one fixture. `claude-code` writes rules to per-artifact files and shares `.claude/settings.json` between hook fragments, so its collision is the hook pair; both fragments carry an array under the same native event key, and `deepMerge` concatenates them, so the claude-code arm asserts the concatenated array's element order rather than a surviving value. `codex` injects both rules into one `AGENTS.md` and both hook fragments into one `.codex/config.toml` (`pkg/adapter/codex.go`, `codexHookOut` in `pkg/adapter/layout.go`), so its collisions are marker-block sequences and the assertion in each file is the block order. The fixture may not carry a `type: command` artifact, which is the §6.7.1 cell codex cannot translate.

The existing `TestSyncEquivalence_FilesystemVsServerByteIdentical` and its `none` / `claude-code` loop are not edited. Adding `codex` there would abort on the reference fixture's `type: command` artifact and would carry no ordering signal.

The stale reasoning in `assertLockArtifactsEqual`'s doc comment, which explains the position-for-position comparison by "WriteLock orders them", is corrected to name §7.5.3 as the stated order and §7.5 as the materialization order the two consumers now share.

## Manual validation

**S63 · The two sync modes compose a shared merge target identically.** The surface a human reads is the merged JSON file and the injected text file in two target directories: `diff` output over `.claude/settings.json` from a `claude-code` target and over `AGENTS.md` and `.codex/config.toml` from a `codex` target. It catches a build in which the two modes still fold fragments in different orders, which no per-artifact file can show. It also reads `$PODIUM_CHANGED` after editing the contributor whose lock entry the old per-path collapse discarded, through the one surface that exposes it: a `kind: workspace` target whose `workflow.publish` command echoes the variable, run with `podium sync --config`. A single-target `podium sync` surfaces `Result.Changed` in neither its human output nor its `--json` envelope (`cmd/podium/main.go`), so no flag on that form can carry the step.

The scenario is added after S62 in `test/manual-validation.md`, with an index row reading `solo then standalone` for Deployment (matching S40, the other filesystem-versus-server parity scenario) and no embeddings, no vector backend, and no live infrastructure.

> **Goal.** Validate that a filesystem-source sync and a standalone-server sync
> over the same directory write byte-identical merged and injected files when
> two artifacts contend for one target, and that an edit to either contributor
> is reported as a change.
>
> **Covers.** The §7.5 materialization order, the §7.5.3 `artifacts:` list
> order, the §11 filesystem-to-server equivalence requirement, and the change
> comparison over a shared materialized path (§11's idempotent re-sync bullet
> over the §7.5.3 lock entries).
>
> **Why by hand.** The assertion is over the bytes of a file two artifacts wrote
> into, read side by side from two targets that were materialized through
> different registry sources. A run in which one mode orders by layer and the
> other by ID differs only inside those files, and every per-artifact file
> matches, which is what kept the divergence out of the suite.

**IMPLEMENTOR'S CHOICE:** the whole procedure, which is what S63 leaves open. Write the steps, run them, and record what the commands actually print. The scenario must satisfy these constraints.

- The scenario opens with the file's isolation block as step 1, numbers its steps, and gives each step an `**Expect.**` block naming the observable outcome.
- The fixture is a two-layer registry with `multi_layer: true`, holding two hooks on one event, two rules, and no `type: command` artifact, which is the §6.7.1 cell codex cannot translate. The two hooks live in different layers, and so do the two rules, with each pair's canonical IDs sorting opposite to the layer order. The straddle is what makes the comparison non-vacuous: within a single layer `Walk` already emits alphabetically by canonical ID, which is the order the server source produces globally, so a pair sitting in one layer folds identically under both modes whatever the build does.
- Every scaffold command supplies its type-specific required flag, so no step blocks on an interactive prompt reading stdin. For `--type hook` that is `--hook-event`, which `collectTypeSpecific` prompts for when it is absent (`cmd/podium/artifact_scaffold.go`), and naming one event on both hooks puts the two fragments in one native entry with no frontmatter edit.
- The server step uses the file's idiom: a log redirect, a recorded `SRV=$!`, and a `/healthz` poll before any sync, on a loopback port no other scenario binds. Ports 8126-8131 and 8144-8149 are unclaimed in `test/manual-validation.md`.
- The target comparison is `diff -r -x sync.lock`, preceded by a non-vacuity count in S40's style (`test/manual-validation.md:3518`), because an empty tree compared against an empty tree also reports no differences. The two locks are compared separately, on their `id` and `materialized_path` order, with the volatile `target:` and `last_synced_at:` fields filtered out.
- The change signal is read through a `kind: workspace` target's `workflow.publish` command under `podium sync --config`, because no single-target `podium sync` output carries it. The step edits the contributor whose lock entry the per-path collapse discarded and expects `changed=true`.
- Cleanup is the file's standard `kill "$SRV"; wait "$SRV"` block followed by `rm -rf "$WORK"`.

## Documentation changes

**DOC-1.** `CHANGELOG.md`'s single unreleased `### Fixed` bullet is replaced with the items below. The heading is unchanged.

1. **Content hash.** Keep the manifest-versus-`SKILL.md` description and the shared `version.CanonicalContentHash`. State that the first sync after the change reports a skill-carrying target as changed because the recorded hash moves. The list-order clause and the "No materialized file changes" sentence are deleted.
2. **Enumeration order (§7.5, §11).** Both modes materialize the resolved set in ascending canonical artifact ID order, so config-merge fragments and inject blocks compose identically in every deployment mode and the lock's `artifacts:` list follows. A merged target that previously composed in layer order is rewritten once, with the same entries in a different order; that rewrite is not reported as a change, because the comparison reads the recorded artifact content hashes and none of them moved. In filesystem mode the composition inside a shared target can differ from before, matching what the server mode already did.

   **IMPLEMENTOR'S CHOICE:** the sentence stating what the order does inside a shared target, placed before the filesystem-mode sentence. It restates SPEC-1's third and fourth sentences at their granularity in one sentence, and it names both target kinds, the JSON config-merge targets and the marker-block targets. The phrase "the last artifact supplies the key's value" is forbidden, and no key-level merge rule may be stated over `.codex/config.toml`, `AGENTS.md`, or `GEMINI.md`.
3. **Change reporting (§11).** Every artifact writing into a shared materialized path now participates in the change comparison, so `$PODIUM_CHANGED` is true after an edit to an artifact whose lock entry previously did not survive the per-path collapse, and a `skip_if_no_changes` command that formerly skipped that case now runs.

The manual-validation half of DOC-1 is the S63 scenario above, whose goal, coverage, and constraints are stated there and whose steps the implementor writes and runs.

## Resolved in adversarial review

Review rounds populate this section. Each entry names the defect the round found in this proposal and the amendment that answered it.

### Pass 0 (2026-09-08, drafting)

- **The draft removed `WriteLock`'s sort on reasoning that did not survive.** It claimed the write-point sort made `lockChanged` compare a sorted prior lock against an unsorted new one, that two ordering authorities would drift, and that the in-place sort harmed a caller on a failed write. `lockChanged` compares path-keyed maps, which are order-insensitive; both orderings use the same key, so an ordered slice is a fixed point of the sort; and every production caller passes a locally owned lock it does not re-read. CODE-2 is now a comment retarget, the removal is recorded as a dropped alternative, and the spurious-change regression is attributed solely to the per-path collapse in the change comparison.
- **CODE-1's comment claimed the sort overrides no §4.6 precedence, which is false.** A merged config entry is keyed by the artifact's `name:` frontmatter, so two distinct canonical IDs can write one key and the fold order decides how their fragments compose. Filesystem mode folded in layer-precedence order. The comment now states what the sort decides, SPEC-1 carries the same statement as a normative sentence, and the CHANGELOG records the filesystem-mode consequence.
- **TEST-1 asserted a property `version.CanonicalContentHash` does not have.** A draft case asserted that a resource key is unambiguously separated from its value; the function concatenates its parts with no length framing, so the assertion passes while the comment justifying it states a false invariant. That case and two tautological ones are dropped, one composition pin survives, and the ambiguity is recorded in the Edge cases table and in Non-goals.
- **TEST-4 proposed adding `codex` to the existing equivalence loop.** The reference fixture carries a `type: command` artifact, an unsupported §6.7.1 cell for codex, so the arm aborts before it can assert anything. The existing test is left alone and the inject-harness coverage comes from the new colliding fixture.
- **Two test arms were placed on the `none` adapter, which cannot reach a shared merge target.** It emits per-artifact `OpWrite` paths only. The shared-path arms are on `claude-code` and `codex`.
- **DOC-1 staged an edit to a pull-request description.** A proposal stages changes to files in this tree. The CHANGELOG rewrite stands, the PR-body restatement moved into an open question, and the missing CHANGELOG item for the change-reporting fix was added.

### Pass 1 (2026-09-08, automated)

- **SPEC-1 staged a last-wins merge rule that `deepMerge` does not implement.** The third sentence said the artifact whose canonical ID sorts last supplies a contended key's value, and CODE-1's comment, the Edge cases table, and DOC-1's second item repeated it. `deepMerge` (`pkg/materialize/merge.go`) concatenates same-key arrays destination-first and merges same-key objects recursively, and only a scalar leaf takes the src value, so the rule was false for both collisions this proposal tests: two hooks contend for a JSON array under one native event key (`hookFragmentJSON`, `pkg/adapter/layout.go`), and two mcp-servers sharing a `name:` contend for an object-valued entry (`jsonMapMergeFragment`, same file). Every one of those statements now describes the fold at the granularity the code has, and TEST-4's claude-code arm asserts the concatenated hook array's element order.
- **An Edge cases row claimed a fold-order change reports the target as changed.** `lockChanged` compares the content hash recorded against each lock entry and never reads the materialized bytes (`pkg/sync/sync.go`), so reordering the fold moves no hash and `Result.Changed` stays false. The row now states that the merged file is rewritten once while `Result.Changed` and `$PODIUM_CHANGED` stay false, DOC-1's second item says the same, and the one-time `skip_if_no_changes` skip is recorded as an accepted failure mode in Non-goals alongside what closing it would cost.
- **S63's step 5 compared the two targets with an unfiltered recursive `diff`.** Each lock records its own `target:` and a wall-clock `last_synced_at:` (`pkg/sync/sync.go`, `pkg/sync/lockfile.go`), so the two files differ on every run and the `&& echo` could never fire. The implementor's-choice constraint now requires `diff -r -x sync.lock`, matching S40 and the integration equivalence helper, and a separate comparison over the two locks' `artifacts:` order so the §7.5.3 order S63 claims to cover is still checked.
- **S63's step 6 read the changed result from a `podium sync --json` key that does not exist.** The single-target envelope carries profile, target, harness, scope, and artifacts, `printHuman` prints adapter, target, and artifacts, and `PODIUM_CHANGED` is exported only by `runWorkspaceTarget` inside a workflow phase reachable through `--config` (`cmd/podium/main.go`). The implementor's-choice constraint now names that mechanism, a `kind: workspace` target echoing `$PODIUM_CHANGED` from its `publish` phase under `podium sync --config`, rather than a flag spelling.
- **S63's step 4 backgrounded the server without a PID or a readiness poll.** A sync issued before the port binds fails with `network.registry_unreachable` rather than retrying (`pkg/sync/sync.go`), and the stated cleanup had no handle to stop, which the document's conventions require. The implementor's-choice constraint now requires the log redirect, the recorded `SRV=$!`, a `/healthz` poll before any sync, a loopback port no other scenario claims, and the standard `kill "$SRV"; wait "$SRV"` cleanup block.
- **S63's index row said `solo` although step 4 starts a standalone server.** The row now reads `solo then standalone`, matching S40, the corpus's other filesystem-versus-server parity scenario.
- **TEST-3's toggle arm could not reach the `toggles.add` append tail.** `Run` reads the prior lock's toggles only under `Options.PreserveToggles`, which a manual one-shot sync leaves false, and `selectRecords` skips a toggled ID the scoped set already carries, so the staged run exercised nothing and would pass against a build with no sort. The arm now mirrors `TestRun_PreserveTogglesAppliesAddAndRemove`: `PreserveToggles: true` with a `Scope` that excludes the third artifact, so the record reaches the result only through the append tail.
- **TEST-3's second case cited §7.5.2 for a contract §7.5.2 does not state.** §7.5.2 is `sync.yaml` configuration and never names `$PODIUM_CHANGED`, whose only spec occurrence is §7.8. The annotation, S63's Covers block, and DOC-1's third item now cite §11's idempotency bullet over the §7.5.3 lock entries, which is what OQ-3 already said the proposal does, and OQ-3 records that the existing prose comments in `pkg/sync` and `cmd/podium` carry the same misattribution.
- **TEST-3's rewritten toggle arm named a third artifact and a `Scope` the staged fixture did not contain.** `selectRecords` pulls each `toggles.add` ID out of `byID`, which is built from the full effective view (`pkg/sync/sync.go`), so a toggled ID with no artifact directory is skipped silently and the arm is vacuous. The fixture enumeration now lists `org-defaults/a-audit/check/ARTIFACT.md` as the third artifact and says why it is there, and the arm names the `Include` patterns (`z-tools/**` and `a-finance/**`) that carry the other two while excluding it, mirroring how `scopeRegistry` carries `shared/c` alongside the `finance/**` set in `pkg/sync/scope_run_test.go`.
- **OQ-3's enumeration of the §7.5.2 `$PODIUM_CHANGED` misattribution omitted the export site.** `runWorkspaceTarget` in `cmd/podium/main.go` attributes the variable to §7.5.2 immediately above the `vars["PODIUM_CHANGED"]` assignment, which is where the workspace analog is exported. OQ-3 now names that comment beside the two in `pkg/sync` and drops the count, so the unstaged gap covers every site.

### Pass 2 (2026-09-08, automated)

- **S63's step 2 scaffolded the two hooks with commands that block on an interactive prompt.** `collectTypeSpecific` requires `--hook-event` for `--type hook` and, with `--yes` absent, prompts on stdin rather than failing (`cmd/podium/artifact_scaffold.go`), so the block as staged would hang or consume the following command line as the event value and produce a malformed hook, and step 3's Expect over the two fragments under one event could not hold. The implementor's-choice constraint now requires every scaffold command to supply its type-specific required flag, so no step reads stdin, and records that naming one event on both hooks puts the two fragments in one native entry with no frontmatter edit.

### Pass 3 (2026-09-08, automated)

- **S63 staged a verbatim command script that no reviewer in this loop can run.** The scenario had grown to roughly a third of the document and restated its own mechanism in the intro paragraph, in the numbered steps, and again in the implementor's-choice constraint, so each round spent its budget proofreading shell that only the implementor executes, and one instance of the same class was still unfixed: step 5's recursive `diff` carried no non-vacuity count, which S40 has, so an empty tree compared against an empty tree scored as a pass. The `Steps` blocks, their per-step `**Expect.**` prose, and the intro's restatement of the file's conventions are deleted. `Goal`, `Covers`, and `Why by hand` stay, because they state why the scenario exists and what it catches, and the implementor's-choice constraint is now the only description of the procedure. It carries the fixture, the scaffold-flag rule, the server idiom and the unclaimed ports, the comparison including the non-vacuity count, the workspace change signal, and the cleanup block, and the implementor runs the commands, which is the step that validates them.
- **The `deepMerge` value-kind fold rule was stated at four sites.** The Summary's `Watch out for` bullet and the second paragraph of "The realignment is unconstrained by §4.6" restated what SPEC-1's normative sentence and DOC-1's second item already say, with the Edge cases row as the one-line index, so a correction to the rule had to land in four places at once. Pass 1 already paid that cost. Both restatements now point at SPEC-1's third sentence instead of repeating it, and the warning against writing "the last artifact supplies the key's value" stays in the Summary.

### Pass 4 (2026-09-08, automated)

- **SPEC-1's composition sentence was stated over every §6.7 config-merge target, and it does not hold for codex.** §6.7 lists `.codex/config.toml` among the config-merge targets, while the codex adapter emits `OpInject` for both its config-merge types (`pkg/adapter/codex.go`, `codexHookOut` in `pkg/adapter/layout.go`) and `applyOp` routes `OpInject` to `injectBlock` rather than to `deepMerge` (`pkg/materialize/merge.go`), so no key-level composition happens there. The staged text now states the value-kind rule over the JSON config-merge targets `deepMerge` folds and states separately that a marker-block target composes whole Podium-managed blocks with no key merging. The rationale paragraph under SPEC-1 records the routing evidence, DOC-1's second item and the Edge cases rows carry the same split, CODE-1's comment names the inject targets, and the Summary's warning names both halves. The consequence for a `name:` collision on `.codex/config.toml`, two blocks carrying one table header, is recorded in the Edge cases table and in Non-goals as pre-existing and unchanged.
- **TEST-4's fixture constraint bound the fixture as a whole rather than each colliding pair, so both arms could be built blind to the ordering fix.** `Walk` emits alphabetically by canonical ID within one layer (`pkg/registry/filesystem/walk.go`), which is the order `dedupeLatest` produces globally for the server source (`pkg/registry/core/domain_load.go`), so a pair sitting inside one layer folds identically under both sources with or without CODE-1's sort and `assertTreesEqual` passes against a build with no sort. TEST-4 now enumerates each colliding pair's artifact paths, requires each pair to straddle the two layers with canonical IDs sorting opposite to `layer_order`, and states why the arm is vacuous otherwise. The codex arm now names both of its marker-block collisions, `AGENTS.md` for the rules and `.codex/config.toml` for the hooks. S63's fixture constraint carried the identical gap and now carries the same straddle requirement, and S63's file list is corrected to the files the fixture actually produces.

### Pass 5 (2026-09-08, automated)

- **The merge and inject composition rule was still copied in full at four sites, and correcting it had twice cost a coordinated four-site edit.** SPEC-1's third and fourth sentences state the mechanism whole and correctly, and Pass 1 and Pass 4 each had to repair the same rule in SPEC-1, CODE-1's comment, the Edge cases table, and DOC-1's second item together. Pass 3 collapsed two of the copies and left these two. CODE-1's staged comment no longer restates the value-kind fold or `injectBlock`'s behavior; it points at §7.5 and keeps only the three statements it owns, under an implementor's-choice constraint barring a key-level merge rule over an `OpInject` target. DOC-1's second item no longer enumerates the targets and the per-value-kind outcomes; it carries an implementor's-choice constraint requiring one sentence at SPEC-1's granularity, naming both target kinds, forbidding "the last artifact supplies the key's value", and barring a key-level rule over `.codex/config.toml`, `AGENTS.md`, and `GEMINI.md`. The Summary's warning is reworded to name SPEC-1 as the single site that states the rule. The Edge cases rows are kept as they stand, because they cite SPEC-1's sentences as their source, which is the index role the table serves.

## Open questions

**OQ-1.** CODE-3 closes a defect that predates the branch: an edit to the artifact whose lock entry does not survive the per-path collapse reports `$PODIUM_CHANGED=false`. It is folded in here because the same collapse is what the branch's write-point sort turned into a live regression. Splitting it into its own proposal is defensible if this one should stay confined to the enumeration order and the hash consolidation; in that case CODE-1 alone removes the regression and the missed-change case stays open, and TEST-3's second case moves with it.

**Resolved 2026-09-08 by the reviewer: CODE-3 stays as staged.** The per-path collapse is what the write-point sort turned into a live regression, so the two arms are the same defect seen from either side, and closing one while leaving the other would leave a shared materialized target still under-reported.

**OQ-2.** Whether PR 121 is amended in place or superseded. Its content-hash consolidation is kept as it stands, its `WriteLock` sort is kept with a retargeted comment, and the ordering half of its diff is replaced by CODE-1. Its PR body still describes the ordering and `materialized_path` divergences as filed rather than fixed and describes an assertion the branch no longer makes; that text is restated on whichever pull request carries this work, or the branch is closed. A proposal does not stage edits to an artifact outside the tree.

**Resolved 2026-09-08 by the reviewer: the work continues on the existing branch.** The pull request text is restated once this lands rather than now, and the branch is not superseded.

**OQ-3.** Whether the workspace `$PODIUM_CHANGED` variable should have a spec basis at all. It is named in `spec/07-external-integration.md` for the §7.8 marketplace pipeline alone; the workspace analog exists in code alone, and the prose comments that describe it in `pkg/sync/sync.go`, `pkg/sync/sync_test.go`, and `runWorkspaceTarget` in `cmd/podium/main.go`, which is the site that exports the variable, attribute it to §7.5.2, which covers `sync.yaml` and never names the variable. CODE-3, its test, S63's Covers block, and DOC-1's third item therefore cite §11's idempotency bullet over the §7.5.3 lock entries rather than a §7.5.2 contract the spec does not state. Correcting the existing prose comments and stating the workspace contract in §7.5.2 are separate gaps this proposal does not stage.

**Resolved 2026-09-08 by the reviewer: filed as its own defect rather than staged here.** The citation correction needs no decision, but stating the workspace contract needs one, namely whether the variable promises that the tree changed, which would require reading materialized bytes, or that the recorded set changed, which is today's behavior. This proposal keeps its §11 citation and adds no fourth reference to a §7.5.2 contract the specification does not state.

## Non-goals

- **Changing §4.6 precedence, the collision policy, or which record wins.** `Walk` resolves both before it returns, and the ordering applies to the already-resolved set.
- **Re-ordering `filesystem.Registry.Walk`'s own return value.** Its layer-major order is documented on the function, pinned by its own test, and read by paths beyond sync. Sync's consumption of it is where the §11 obligation lives.
- **Changing the content-hash composition itself.** `version.CanonicalContentHash` keeps the slots and the sorted-path rule the branch landed; only the remaining copy moves onto it.
- **Length-framing the parts of `version.CanonicalContentHash`.** Its parts are concatenated into one digest with no framing, so a resource key and its value are ambiguous across a boundary. Fixing it changes every recorded hash and is a §4.7.6 question with its own migration cost. It is recorded in the Edge cases table and left as it stands, and no test asserts the property the code lacks.
- **Deduplicating fragments inside a merged target.** Same-key arrays are concatenated without deduplication within a render, and that is unchanged; only the concatenation order is fixed.
- **Reconciling two mcp-servers whose `name:` frontmatter collides on one destination.** On a JSON config-merge target `deepMerge` merges the two entry objects member by member, so a URL-based and a command-based server sharing a name yield an entry carrying `url`, `command`, and `args` together. On `.codex/config.toml` the adapter emits `OpInject`, so the pair produces two marker blocks carrying the same `[mcp_servers.<name>]` table header, which Codex's TOML parser rejects. Both behaviors predate this change and the ordering does not alter either. Detecting the collision belongs with the §6.7 adapter output rules.
- **Making the change comparison read the materialized bytes.** `lockChanged` compares the recorded artifact content hashes, so a rewrite whose only difference is the fold order reports no change. The first sync after this change therefore rewrites a shared merge target once while `$PODIUM_CHANGED` stays false on a target carrying no skill, and a `skip_if_no_changes` command skips that one rewrite. That is accepted here: closing it means recording a per-file digest in the lock, which is a §7.5.3 schema change with its own proposal. CODE-3 closes the separate defect in which an artifact's own hash moved and the comparison discarded its entry.
- **The marketplace render path's lock (§7.8).** It builds from sorted paths with empty IDs and is already deterministic.
- **Any migration path for a lock written by an older build.** Podium is pre-1.0 and the next sync rewrites the file.
- **The `.claude/settings.json` and `.mcp.json` merge semantics for an operator's own entries,** which the §6.7 Podium-owned tag already governs.
- **Adding a caller list to `version.CanonicalContentHash`'s doc comment** (dropped alternative, originally part of CODE-4). A hand-maintained list of callers goes stale unchecked, which is the class of defect this proposal fixes elsewhere. The mirrored surface is the composition, which the delegation guarantees.
- **Removing `WriteLock`'s sort** (dropped alternative, originally CODE-2). `lockChanged` compares maps, so the sort explains none of the spurious-change regression; both orderings use the identical key, so they cannot disagree; no caller is harmed by the in-place sort on a failed write; and removal would leave the override and save-as rewrite paths building a file whose order §7.5.3 states. The comment is stale rather than the code.
