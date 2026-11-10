# Proposal 0058: Ingest, sync, materialize, and serve a plain Agent Skills directory as a type: skill artifact with no ARTIFACT.md

- Issue: (to be filed)
- Status: Approved (2026-11-09). Verified on 2026-11-09 after 3 adversarial review rounds (5 findings fixed).
- Date: 2026-11-09

This document stages spec, code, test, and documentation changes. It does not modify any spec, code, test, or doc file. Apply the changes in the staged sections after sign-off. The change is backward-incompatible in four places. `podium import` loses `--type`. `podium artifact scaffold --type skill` refuses the Podium-only flags. A both-files skill whose `SKILL.md` `metadata.version` is a `major.minor.patch` value different from its `ARTIFACT.md` `version` fails ingest lint. A `SKILL.md` outside any package that has no frontmatter, which discovery ignored before, now fails the walk of its layer.

## Summary

**What changes.**

- **Discovery.** §4.2 states the package discovery rule. A directory below the layer root is a package when it directly holds `ARTIFACT.md`, or when it directly holds `SKILL.md` and no directory between it and the layer root is a package. A package with `SKILL.md` and no `ARTIFACT.md` is a `type: skill` artifact. §4.1 and §4.3 make a skill's `ARTIFACT.md` an optional overlay for the Podium-only fields. A `SKILL.md` below a package's root stays a bundled resource. Both Go walkers (`pkg/registry/filesystem` and `pkg/registry/ingest`) share one predicate, and both SDK overlays mirror it (SPEC-1, SPEC-2, SPEC-3, SPEC-5, SPEC-12, CODE-3, CODE-9).
- **Versions.** §4.3.4 states version precedence: the `ARTIFACT.md` `version`, then a `major.minor.patch` `SKILL.md` `metadata.version`. §4.7.6 reserves `0.0.0-auto.<N>` for the registry, and §7.3.1 states how ingest assigns N. `pkg/version` resolves the form in pins and `latest`, and `pkg/registry/ingest` derives N from stored rows with no new store method. Ingest refuses a version-less skill whose bytes a stored or soft-deleted row of its ID already holds under another version, so §4.7.6 can state that no two versions of one artifact share a content hash (SPEC-4, SPEC-6, SPEC-8, CODE-1, CODE-2, CODE-5).
- **Hashing and serving.** The §4.7.6 digest frames a zero-length value for an absent `ARTIFACT.md`. The registry serves a synthesized `ARTIFACT.md` with exact bytes in the `frontmatter` member of `load_artifact` and of the batch entry, and the §4.7.10 delivery record frames it. A search descriptor carries that document's frontmatter block (`---\ntype: skill\nversion: <version>\n---\n`), as it carries every artifact's block without the body. Stored-row admission (§13.4) derives a plain-skill row's served type, body, description, tags, sensitivity, deprecation flag, replacement, and `audit_redact` keys from its admitted `SKILL.md` rather than from stored columns, so an `extends:` child merges none of those columns from a plain-skill parent. The filesystem registry places the synthesized bytes in `ArtifactRecord.ArtifactBytes` after `extends:` resolution, so adapters dispatch a plain skill as a skill in every mode. Server-source sync and the MCP content cache take a load's version from the served `version` member, because an `ARTIFACT.md` overlay may omit `version` (SPEC-6, SPEC-7, SPEC-13, CODE-4, CODE-6, CODE-12).
- **Materialization and lint.** The `none` adapter and the SDK `materialize()` writers omit the synthesized document, so a plain skill materializes as a plain Agent Skills directory (SPEC-10, CODE-8, CODE-9). Lint requires `SKILL.md`, runs the `ARTIFACT.md` rules only when that file exists, warns with `lint.skill_version_assigned`, and errors on an authored reserved version or a version mismatch (SPEC-4, CODE-7).
- **Tooling, verification, and docs.** `podium artifact scaffold --type skill` writes `SKILL.md` alone, and `podium import` writes plain skill directories and drops `--type` (CODE-10, CODE-11). §13.11 and §11 state the filesystem-source version and the equivalence fixture (SPEC-9, SPEC-11). The docs lead with a `SKILL.md`-only skill and present `ARTIFACT.md` as an optional hand-written overlay (DOC-1 through DOC-5). Scenario S96 and edits to existing scenarios cover the operator-visible flows (MV-1).

**Fixed decisions.**

- A `SKILL.md` makes a package only when no directory between it and the layer root is a package. The nested-package boundary of §4.4 and §4.7.6 stays a directory carrying its own `ARTIFACT.md` (D-1).
- The `ARTIFACT.md` `version` wins. A `metadata.version` counts only in `major.minor.patch` form, and any other form is ignored with a warning (D-2).
- The reserved form is exactly `0.0.0-auto.<N>`, a semver prerelease of `0.0.0`. Authors cannot write it, ranges never select it, and `latest` treats it as a candidate (D-3).
- N is derived from the stored rows that one `ListManifestsIncludingDeleted` read per ingest call returns, and a conflict at the pre-check or at commit re-reads the listing and re-derives N. A version-less skill whose bytes a stored or soft-deleted row of its ID already holds, other than its layer's latest non-deleted row, is refused with `ingest.invalid_artifact` and `details.constraint: "auto_version_duplicate_content"`, so no two stored rows of an ID share a content hash under different versions. A purge can release refused bytes under a new N, and an MCP server cache that held them before the purge then reports the purged version for them; §7.3.1 and §6.5 state that accepted outcome. A purge of the row holding the highest N lets that N be issued again (D-4, D-15).
- The stored `Frontmatter` of a plain skill is empty. The synthesized document never enters the §4.7.6 content hash and is never stored, and the §4.7.10 delivery record frames it as the served document. Admission treats a row with an empty manifest and a parsing `SKILL.md` as a skill with no `ARTIFACT.md` and serves its fields from those admitted bytes (D-5).
- The synthesized bytes are fixed by §4.3.4 and defined once per language, in `pkg/manifest` for Go (D-6).
- The `none` adapter and the SDK writers recognize the synthesized document by byte equality and omit it (D-7).
- A filesystem source reports `0.0.0-auto.0` for every skill whose version is assigned (D-8).
- Lint adds one code, the `lint.skill_version_assigned` warning. A version mismatch reuses `lint.skill_artifact_field_mismatch` rather than a new `ingest.invalid_artifact` constraint, and an authored reserved-form version is `lint.invalid_version` (D-2, D-9).
- `podium artifact scaffold --type skill` writes `SKILL.md` alone and refuses `--tags`, `--when-to-use`, `--extends`, and an explicitly passed `--sensitivity` in one message with exit 2 (D-10).
- `podium import` stays as an optional normalizer. It drops `--type`, imports only the immediate subdirectories of `--source`, writes `--version` to `metadata.version`, and never writes `ARTIFACT.md` (D-11).
- Podium-only fields live in an optional hand-written `ARTIFACT.md` overlay and never as top-level or `metadata` keys of `SKILL.md`. An `extends:` child needs the overlay, and its parent may be a plain skill (D-12).
- No new env var, §6.10 code, store SPI method, schema column, HTTP member, `registry.yaml` key, or matrix cell. One `details.constraint` value, `auto_version_duplicate_content`, is added (D-4, D-13).
- Every spec edit lands in one commit before any code step, one pull request carries every step, and the CHANGELOG entry goes under `[Unreleased]` toward 0.6.0 (D-14).

**Watch out for.**

- **The nested `SKILL.md` stays a resource.** Proposal 0026 (`fe2d1118`) made `references/SKILL.md` a bundled resource of its parent on purpose, and Go and SDK tests pin it. Do not move the nested-package boundary, do not invert those tests, and do not test the boundary with a `SKILL.md` predicate. Only the discovery trigger gains a `SKILL.md` arm, with the ancestor check.
- **The spec edits contradict each other until all of them land.** §4.1 line 7, §4.3 line 135, §4.3.4 lines 256 and 287, the glossary, and the §13.11.1 tree each restate the both-files rule, and §13.4 says ingest never stores an empty-manifest row. Every SPEC edit that S1 lists lands in one commit.
- **A same-ID overlay with an assigned version pins as an authored one does.** `resolveExtendsPin` runs before ingest assigns a version (`pkg/registry/ingest/ingest.go:677`), so for an assigned record its own-row exclusion (`ingest.go:1613`) compares against an empty version and excludes nothing. The only row it would exclude is the child's own row at the version `assignVersion` reuses, and reuse requires an equal content hash, so the pre-check classifies that record as unchanged and discards the pin and the fold (`ingest.go:669-675`). A version-less overlay whose bytes change therefore pins the most recently ingested non-deprecated row of its ID from any layer (`ingest.go:1685-1689`), as an authored version bump does. When no other layer has ingested a row of the ID since the overlay's previous row, that row is the pin. It is an earlier version of the overlay, which §4.6 makes a same-layer parent (`spec/04-artifact-model.md:724`), so the §4.6 executable-field rule does not test that edge (`TestIngest_ExecutableInheritanceSameLayerOverlayPinAccepted`). When a lower layer's row is newer, the pin crosses layers and the rule applies. Do not change `resolveExtendsPin`. Restricting a same-ID reference to a lower layer contradicts that §4.6 sentence and proposal 0057's `[Unreleased]` CHANGELOG entry.
- **Never read a version back out of a served `ARTIFACT.md`.** An authored overlay may omit `version`, so a consumer that parses the served document for the version gets an empty string. CODE-12 moves server-source sync and the MCP cache read-back to the served `version` member, and CODE-6 gives a merged `extends:` document the child row's version.
- **Import direction between `pkg/version` and `pkg/manifest`.** `pkg/version/version_test.go` is `package version` and imports `pkg/manifest`, so `pkg/manifest` must never import `pkg/version`. The auto-form helpers live in `pkg/manifest`, and `version.ParsePin` calls `manifest.IsAutoVersion`.
- **`Registry.Walk` returns from inside `if opts.ResolveExtends`.** With the flag set it returns `composeExtends(all, opts)` (`pkg/registry/filesystem/walk.go:69-71`), which re-runs `dedupe` each round and calls `resolveExtends(deduped, kept)` once on the settled set (`pkg/registry/filesystem/extends.go:38-73`). `kept` holds copies, so a fix-up applied to `deduped` before the resolver never reaches a parent. CODE-4 sets the version and description in the loader, which feeds every round, and it synthesizes `ArtifactBytes` on the slice each `Walk` path returns, after `composeExtends`, because the overlay and `podium lint` walks leave the flag false.
- **`PackageManifests` returns its stat error.** Detection moves from the entry name to `fs.Stat`, and under `source.ConfinedFS` an escaping or absolute-target manifest symlink fails `Stat` rather than reading as absent. Discarding that error, as `hasArtifactManifest` and `fsHasArtifactManifest` do today, would silently drop the package instead of failing the layer's ingest with `ingest.source_unreachable` (§7.3.1). Only `fs.ErrNotExist` means absent (CODE-3).
- **Lint runs before ingest assigns the version** (`pkg/registry/ingest/ingest.go:533`). The required-version rule must skip a record whose version is assigned, or every plain skill fails ingest as `lint.required_field_missing`.
- **Soft-deleted rows count as duplicates and never supply a reused version.** A tombstoned row joins the D-4 duplicate set, because `RestoreLayerConfig` serves every tombstoned row of its layer again (`pkg/store/memory.go:545-561`, `pkg/store/sqlite.go:950-951`). It never supplies a reused version: `GetManifest` hides it, and `PutManifest` treats its key as an idempotent no-op that leaves it tombstoned (`pkg/store/memory.go:131-137`), so reusing it would report an accepted skill that is not served. The reused row is the layer's latest non-deleted row at any version. Comparing against any earlier row of the layer would map a revert back to an older row while `latest` serves the newer one.
- **The pre-check is a second conflict point.** The `GetManifest` pre-check at `pkg/registry/ingest/ingest.go:781-800` reports a conflict without reaching `commitManifest`. The re-derivation loop must cover both.
- **`version.less` is shared by `ResolveLatest` and `Compare`.** `Compare` stays core-only because `parseCore` strips the suffix first. A `TestCompare_Ordering` row pins it.
- **The walker parity test hashes `fsRec.ArtifactBytes`** (`pkg/registry/ingest/walk_internal_test.go:192`). After CODE-4 the filesystem side carries the synthesized document there, so the assertion moves to `AuthoredBytes`.
- **`podium artifact show --json` parses `frontmatter` into an object** (`cmd/podium/read_cli_json.go`). A check of the exact synthesized bytes reads `/v1/load_artifact` directly.
- **The SDK frontmatter parsers skip indented lines.** `metadata.version` is nested YAML, so each SDK parser gains a narrow read of that key.
- **`source.git.root` with a trailing slash fails ingest** (go-git `Tree.Tree` returns `directory not found`). Every `root` value this proposal writes omits the trailing slash. Fixing the existing `root: artifacts/` examples is a non-goal.
- **Proposal 0057 is on `main` (merge `a56ae170`).** It added the §4.6 executable-field rule, which ingest applies on the insert path after the idempotency switch (`pkg/registry/ingest/ingest.go:802-817`) and the filesystem source applies in `composeExtends` (`pkg/registry/filesystem/extends.go:38-73`). It also added `RejectedArtifact.Details`, `layer.InvalidArtifactCode`, `layer.Drop`, `WalkOptions.OnDrop`, `core.ExtendsChain`, and `core.ResolvedArtifact`. This change reuses those surfaces (D-13) and leaves `foldExtendsParent`, `manifest.MergeExtends`, `resolveExtendsPin`, both `executableInheritance` functions, `composeExtends`, and the §4.6 field-semantics and executable-field text untouched.
- **Parts of `test/e2e` skip silently on macOS.** Run the end-to-end tests with `-v` and confirm no `SKIP` line before claiming a pass.
- **Line numbers drift.** Anchor each edit by its quoted text, and treat cited line numbers as hints.

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1, SPEC-2, SPEC-3, SPEC-4, SPEC-5, SPEC-6, SPEC-7, SPEC-8, SPEC-9, SPEC-10, SPEC-11, SPEC-12, SPEC-13. Every spec edit and the `artifact-package.svg` edit land in one commit, because each edit contradicts the unedited restatements of the both-files rule until all of them change.
      Levels: unit (`make coverage-gate`). Depends on: —
- [ ] **S2 · code** — CODE-2, TEST-1B. The `pkg/manifest` skill-version, auto-form, and synthesized-document helpers with their unit tests, bundled because both touch `pkg/manifest` only and the reviewer reads them together.
      Levels: unit. Depends on: S1
- [ ] **S3 · code** — CODE-1, TEST-1A. `pkg/version` pins and resolution accept the reserved form, with their unit tests in the same package.
      Levels: unit. Depends on: S2
- [ ] **S4 · code** — CODE-3, TEST-1C. The shared discovery predicate, the `SKILL.md` trigger with the ancestor check, and the cross-walker parity table.
      Levels: unit. Depends on: S2
- [ ] **S5 · code** — CODE-7, TEST-1D. Lint for an assigned version, the reserved form, the version mismatch, and the optional overlay, with the end-to-end lint test that CODE-7 changes in the same commit.
      Levels: unit, e2e. Depends on: S4
- [ ] **S6 · code** — CODE-4. The filesystem registry assigns `0.0.0-auto.0` and synthesizes `ArtifactBytes` after `extends:` resolution.
      Levels: unit, integration. Depends on: S3, S5
- [ ] **S7 · code** — CODE-5, TEST-2A. Ingest assigns N from stored rows, refuses a version-less skill whose bytes a stored or soft-deleted row of its ID already holds under another version, and re-derives N on conflict, and the §4.6 executable-field check runs once per record inside the re-derivation loop.
      Levels: unit, integration. Depends on: S3, S5
- [ ] **S8 · code** — CODE-6, CODE-12, TEST-2B. Core serves the synthesized document, admits a plain-skill row from its `SKILL.md`, and gives a merged `extends:` document the child row's version; server-source sync and the MCP cache take the version from the served `version` member; and the ingest-to-HTTP integration tests run across the stores.
      Levels: unit, integration. Depends on: S7
- [ ] **S9 · code** — CODE-8. The `none` adapter omits the synthesized document.
      Levels: unit. Depends on: S2
- [ ] **S10 · test** — TEST-3. Cross-mode equivalence and the golden materialization case.
      Levels: integration, materialization. Depends on: S6, S8, S9
- [ ] **S11 · code** — CODE-9, TEST-5. SDK overlay discovery and canonical-layout writers, with the SDK suites.
      Levels: unit, integration (SDK suites). Depends on: S1
- [ ] **S12 · code** — CODE-10, TEST-4A. `podium artifact scaffold --type skill` writes `SKILL.md` alone, and the existing scaffold unit tests (CODE-10) and end-to-end tests (TEST-4A) change in the same commit so the suite stays green.
      Levels: unit, e2e. Depends on: S5
- [ ] **S13 · code** — CODE-11, TEST-4B. `podium import` writes plain skill directories, and the existing import end-to-end tests change in the same commit.
      Levels: unit, e2e. Depends on: S5
- [ ] **S14 · test** — TEST-4C. The plain-skills end-to-end test and its fixture.
      Levels: e2e. Depends on: S6, S8, S9, S12
- [ ] **S15 · test** — MV-1. Scenario S96 and the edits to the existing scenarios MV-1 lists.
      Levels: —. Depends on: S12, S13, S14
- [ ] **S16 · docs** — DOC-1. Quickstart, README, overview, and tutorial lead with a `SKILL.md`-only skill, with their end-to-end fixtures.
      Levels: e2e. Depends on: S12, S14
- [ ] **S17 · docs** — DOC-2. Authoring and reference pages.
      Levels: —. Depends on: S1
- [ ] **S18 · docs** — DOC-3. Deployment pages on skills repositories and hidden directories, and the cross-tier exceptions for skills that declare no version on the deployment pages and in `README.md`.
      Levels: —. Depends on: S1
- [ ] **S19 · docs** — DOC-4. CLI reference for import, scaffold, and lint.
      Levels: e2e. Depends on: S12, S13
- [ ] **S20 · docs** — DOC-5. CHANGELOG entry.
      Levels: —. Depends on: S19

**Ordering constraints.** S1 lands first and passes `make coverage-gate` before any code step (D-14). S2 precedes S3, because `version.ParsePin` calls `manifest.IsAutoVersion`. S6 and S7 are independent of each other once S3 and S5 land. S9 and S11 depend on no step after S2 and may proceed in parallel with S3 through S8. Land every step in one pull request (D-14).

## Current state and the gap

### Discovery

A directory that holds a valid Agent Skills `SKILL.md` and no `ARTIFACT.md` is invisible to Podium. Both discovery walkers recognize an artifact only by a file named exactly `ARTIFACT.md`. The filesystem walker returns early for any other name at `pkg/registry/filesystem/walk.go:203-205`, and the ingest walker does the same at `pkg/registry/ingest/ingest.go:1039-1041`. `SKILL.md` is read only as a companion of an `ARTIFACT.md` that was already found (`walk.go:247`, `ingest.go:1188`). No lint rule reports the skipped directory, so the skip is silent. The nested-package boundary predicate exists twice, as `hasArtifactManifest` (`walk.go:323-328`, `os.Stat`) and `fsHasArtifactManifest` (`ingest.go:1742-1747`, `fs.Stat`), and both test for `ARTIFACT.md` alone. The TypeScript and Python SDK overlays repeat that test (`sdks/podium-ts/src/overlay.ts:343`, `:402`; `sdks/podium-py/podium/_overlay.py:284`, `:417-423`).

The spec makes `ARTIFACT.md` mandatory in several places:

- §4.1 says "Every artifact has an `ARTIFACT.md`", which covers every type, and its next paragraph, diagram alt text, and ASCII fallback repeat it.
- §4.3 says a skill's frontmatter lives in `ARTIFACT.md`.
- §4.3.4 says "A skill artifact directory contains both", and its lint list says "error if either is missing".
- The §13.11.1 tree annotates a skill as "SKILL.md + ARTIFACT.md".
- The glossary defines "Manifest" as `ARTIFACT.md` and "Package" as the directory holding `ARTIFACT.md`.

§4.4 and §4.7.6 cut a nested package only at a directory carrying its own `ARTIFACT.md`, and they name a nested `SKILL.md` as a bundled resource. That rule stays.

### Versions

A version can come only from `ARTIFACT.md` today (`pkg/manifest/types.go:102`). Lint errors on an empty one with `lint.required_field_missing` (`pkg/lint/lint.go:247-249`), and it rejects a top-level `version` in `SKILL.md` (`pkg/manifest/skill_fields.go:29`, `pkg/lint/skill_rules.go:33-50`). `SKILL.md` `metadata` already parses as `map[string]string` (`pkg/manifest/types.go:223`), but nothing reads `metadata.version`.

The fixed decision D2.1 (`tmp/research/implementation-plan.md:56`) assigns `0.0.0-auto.<N>` to an unversioned skill, and every version parser rejects that string:

- The §4.7.6 grammar admits only `major.minor.patch`.
- `manifest.ValidateVersion` matches `^\d+\.\d+\.\d+$` (`pkg/manifest/parse.go:208-216`).
- `version.ParsePin` rejects a four-part split (`pkg/version/version.go:54-110`).
- `Resolve` and `ResolveLatest` silently skip unparseable candidates (`version.go:124-180`).

An auto-versioned skill would therefore never resolve as `latest` through `load_artifact` (`pkg/registry/core/core.go:1664-1717`) or `extends:` (`pkg/registry/ingest/ingest.go:1580-1691`).

§7.3.1 is enforced tenant-wide on `(artifact_id, version)` (`pkg/store/memory.go:129-139`, `sqlite.go:303-361`, `postgres.go:680-743`). A fixed default version is therefore accepted on the first ingest and rejected as `ingest.immutable_violation` on the first later ingest with changed bytes. A version derived per content hash has a separate problem: a revert to earlier bytes maps to an older stored row, and `latest` (most recently ingested, `version.go:160-180`) keeps serving the newer content. No store method holds a per-artifact counter, ingest takes no serialization lock, and filesystem-source sync has no store.

### Hashing and delivery

§4.7.6 already frames a zero-length value for an absent `SKILL.md`. `version.CanonicalContentHash` frames nil bytes that way (`pkg/version/version.go:291-294`), and stored-row admission already accepts a row whose `Frontmatter` is empty (`pkg/registry/core/admit.go:72-81`). Delivery verification copies the served `frontmatter` member as-is in every consumer (`pkg/registry/server/delivery.go:36`, `cmd/podium-mcp/main.go:2083-2101`, and the SDKs), so it does not depend on what that member holds.

Every adapter and marketplace emitter dispatches on `frontmatterType(src.ArtifactBytes)` (`pkg/adapter/claudecode.go:32`, `builtins.go`, `codex.go:20`, `emitter.go`). A skill whose `ArtifactBytes` lacks `type: skill` falls into the extension-type default branch and lands at `.claude/podium/<id>/ARTIFACT.md` (`claudecode.go:101-109`) instead of `.claude/skills/<name>/SKILL.md`. Three paths feed adapters: the filesystem walker, server-source sync (`pkg/sync/server.go:210`), and the MCP server (`cmd/podium-mcp/main.go:1968-1974`). §2.2 and the §11 equivalence test require all of them to agree byte for byte. The MCP cache path also reads `version` and `type` back out of the cached `frontmatter` (`cmd/podium-mcp/resolution_cache.go:462-470`).

### Tooling

Podium creates an `ARTIFACT.md` for every skill it generates. `podium import` writes `type` and `version` frontmatter to `ARTIFACT.md` unconditionally (`cmd/podium/import.go:30-31`, `:139-146`). `podium artifact scaffold --type skill` writes `ARTIFACT.md` and then `SKILL.md` (`cmd/podium/artifact_scaffold.go:287-302`, `renderSkillArtifactMD` at `:336-351`). `docs/reference/cli.md:188-203` and `:374-419` document both behaviors.

### The change

A directory with a valid `SKILL.md` and no `ARTIFACT.md` ingests, syncs, materializes, and serves as a `type: skill` artifact. Existing skills with both files keep their content hash and keep working. No Podium tool creates a new `ARTIFACT.md` for a skill. The design follows the maintainer's fixed decisions D2.1 through D2.6 (`tmp/research/implementation-plan.md:56-61`).

## Decisions

- **D-1. Discovery (D2.2).** A directory below the layer root is an artifact package when it directly contains a regular file named `ARTIFACT.md`, or when it directly contains a regular file named `SKILL.md` and no directory between it and the layer root is a package. A package with `SKILL.md` and no `ARTIFACT.md` has type `skill`. A `SKILL.md` below a package's root is a bundled resource of that package and starts no package, which keeps the rule proposal 0026 chose and matches how a harness reads a skill directory. The nested-package boundary of §4.4 and §4.7.6 stays a directory carrying its own `ARTIFACT.md`, so no existing package changes its resource set or its digest. D2.2 says every existing discovery rule applies unchanged, nested-package handling included, and this reading satisfies it. Every other rule is unchanged: dot-prefixed directories below the layer root are skipped (`walk.go:198-200`, `ingest.go:1033-1035`), a manifest at the layer root is refused by `ValidateCanonicalID`, and a top-level `SKILL.md` under `multi_layer: true` still raises `config.layer_path_ambiguous` (`pkg/registry/filesystem/registry.go:98-102`). A directory whose `ARTIFACT.md` declares a non-skill type keeps its root `SKILL.md` as a bundled resource. One predicate in `pkg/registry/filesystem` implements the rule for both Go walkers, and the SDK overlays mirror it.
- **D-2. Version precedence.** When `ARTIFACT.md` declares `version`, that value is the version. Otherwise a `SKILL.md` `metadata.version` in `major.minor.patch` form is the version. A `metadata.version` in any other form is ignored, because `metadata` is a free-form map in the Agent Skills standard, and the skill's version is then assigned under D-3 with a lint warning. When `ARTIFACT.md` declares `version` and `metadata.version` is a `major.minor.patch` value, the two must be equal, and a mismatch is a lint error under the existing `lint.skill_artifact_field_mismatch` code, which blocks ingest as `ingest.lint_failed`. This applies the existing §4.3.4 name and description rule. The plan's suggested `ingest.invalid_artifact` with `details.constraint: "version_mismatch"` is not adopted, because it would add a §6.10 detail value for a check the lint layer already expresses. An authored version in the reserved form, in either file, fails `lint.invalid_version`.
- **D-3. Registry-assigned version form (D2.1).** A skill that declares no usable version under D-2 receives `0.0.0-auto.<N>`, where N is a non-negative decimal integer with no leading zeros, starting at 0. The form is a semver 2.0.0 prerelease of `0.0.0`. It is reserved to the registry: `manifest.ValidateVersion` stays strict. An exact pin `<id>@0.0.0-auto.<N>` and a `sha256:` pin select it. A `<semver>.x` range never matches it, following semver's prerelease convention. `latest` keeps its rule (most recently ingested non-deprecated version), and auto versions are candidates. For the code's semver tie-break, `0.0.0-auto.<N>` orders below every version without the suffix and by N among auto versions, which is semver precedence. `version.Compare` and `AtLeast`, which order binary versions (`pkg/sync/config.go:257`), are unchanged.
- **D-4. Assigning N and refusing duplicate content.** N is derived from the store's existing rows, and no store SPI method is added. One `ListManifestsIncludingDeleted` read per ingest call replaces the existing `ListManifests` read and groups the tenant's rows by artifact ID, soft-deleted rows included. For a record whose version is assigned, ingest decides from that ID's rows, the ingesting layer, and the incoming content hash:
  1. The layer's own row is the most recently ingested non-deleted row the ingesting layer contributed for the ID, at any version, ordered as `latest` orders candidates (`IngestedAt`, with ties going to the higher version). When its content hash equals the incoming hash, ingest reuses its version, and the existing pre-check classifies the record as idempotent (§7.3.1). That version is registry-assigned, because a declared version lies in the hashed bytes.
  2. Otherwise, when any row of the ID, from any layer and soft-deleted or not, carries the incoming hash, ingest rejects the record with `ingest.invalid_artifact` and `details.constraint: "auto_version_duplicate_content"`. The reason names no version and no layer, and it tells the author to set `metadata.version` in `SKILL.md` or change the content. No row is written, and `latest` keeps serving the newer row. The rule covers a revert to earlier bytes, a revert across an interval in which the skill declared a version, an unchanged reingest of a layer that was unregistered and registered again under the same ID, and a second layer that contributes identical bytes past the §4.6 check.
  3. Otherwise the record gets one more than the highest N any auto-versioned row of the ID carries, soft-deleted rows included because their keys still block an insert, or 0 when the ID has none.

  Soft-deleted rows join the step 2 set because `RestoreLayerConfig` serves every tombstoned row of its layer again (`pkg/store/memory.go:545-561`, `pkg/store/sqlite.go:950-951`). They never supply the step 1 version, because `GetManifest` hides them and `PutManifest` leaves a tombstoned key tombstoned (`pkg/store/memory.go:131-137`). As a result, among the rows stored at any one time, soft-deleted rows included, no two rows of one artifact ID carry one content hash under different versions. §4.7.6 states the invariant (SPEC-6), and `sha256:` pin selection needs no change. The invariant does not hold across a purge, and the purge paragraph below states the one consequence that has for the MCP server's content cache, which this change accepts rather than closes. The §7.2 entity tag, the §6.5 content cache and HEAD revalidation, and the per-hash delivery files are unchanged.

  When the pre-check finds a different-hash row at an assigned version, or the commit returns `store.ErrImmutableViolation` for one, ingest re-reads the listing, replaces that ID's rows, runs steps 1 to 3 again, and retries, up to 3 attempts in total, and then reports the existing conflict. A concurrent commit of the same bytes therefore either lands on the same N, which the pre-check classifies as idempotent, or appears in the re-read listing, where step 1 reuses it or step 2 refuses it. The bound is an internal guard against a livelock and has no operator meaning, because each lost attempt means another ingest committed a row for the same artifact.

  A purge (§8.4) that hard-deletes every row carrying refused bytes lets a later ingest accept them at the N step 3 then yields. Those bytes then carry a version other than the purged row's, under the same content hash. The MCP server keys its cached delivery record by content hash and artifact ID (`cmd/podium-mcp/resolution_cache.go:440-442`), and CODE-12 stores the served version beside it. Always-revalidate compares only the HEAD `X-Podium-Content-Hash` (`cmd/podium-mcp/main.go:1380-1393`, `:2686`), and the conditional GET sends a content-hash entity tag that the registry answers with a 304 (`main.go:1404-1416`). Both serve the cached record through `cachedOrRefetch` with `RefreshOnly` set. The record passes the §4.7.10 check, because the registry signed it, and the freshness check does not run (`main.go:1898`). A bridge that cached the record before the purge therefore reports the purged version, and for a plain skill the synthesized document naming it, for the re-accepted content until its content cache no longer holds that record, and every `offline-first` and `offline-only` hit does the same. The body and resources it serves are the bytes the registry serves. This change accepts that outcome, because it needs a purge of every row carrying the bytes followed by a re-ingest of the same bytes, and closing it would change the §7.2 entity tag or the §6.5 revalidation predicate. SPEC-8 states it in §7.3.1 and §6.5. A purge that removes the row holding the highest N lets that N be issued again, on the same terms as an authored version whose row was purged (D-15). An operator who registers a layer again under its ID, instead of running `podium layer restore` within the recovery window, sees each unchanged version-less skill of the layer refused until the retention purge removes the tombstoned rows or the author sets `metadata.version`. A filesystem source keeps no history, applies none of these steps, and reports `0.0.0-auto.0` (D-8).
- **D-5. Content hash (D2.3).** An artifact with no `ARTIFACT.md` frames a zero-length value in the first §4.7.6 position, as an absent `SKILL.md` does in the second. `version.CanonicalContentHash` needs no change. The stored `ManifestRecord.Frontmatter` is the authored `ARTIFACT.md` bytes, which are empty for such a skill, so the §13.4 first-start rewrite and stored-row admission recompute the same digest. Admission today serves a row whose manifest is empty or does not parse from its stored columns (`pkg/registry/core/admit.go:77-80`, `:223-226`; `core.go:1986-2005`), and §13.4 permits that only because ingest never stores such a row (`spec/13-deployment.md:90`). Every plain skill becomes such a row, and its body, type, description, tags, sensitivity, deprecation flag, replacement, and `audit_redact` keys would then come from columns a metadata-store writer can change without the signing key. The `extends:` merge reads a parent row's description and tags through the same column fallback (`pkg/registry/core/core.go:1993-2006`), so an edited column of a plain-skill parent would reach the child's served and attested merged document. Admission therefore treats a row whose stored manifest is empty and whose stored `SKILL.md` parses as a skill with no `ARTIFACT.md`, and derives those fields from the admitted bytes through a `manifest.Artifact` value with type `skill` and the `SKILL.md` description, which is the value the filesystem loader builds for a plain skill (CODE-3, CODE-4, CODE-6, SPEC-13). The synthesized document of D-6 never enters the content hash and is never stored. The §4.7.10 delivery record frames it as the served document (D-6). Every existing package keeps a byte-identical digest.
- **D-6. Synthesized document (D2.3).** For a skill with no `ARTIFACT.md`, every consumer that needs an `ARTIFACT.md` document uses these exact bytes: `---\ntype: skill\nversion: <version>\n---\n<!-- Synthesized by Podium: this skill has no ARTIFACT.md. -->\n`, where `<version>` is the resolved version. The registry produces it at serve time in the `frontmatter` member of `load_artifact` and batch responses. A search descriptor carries its frontmatter block, `---\ntype: skill\nversion: <version>\n---\n`, because `descriptorOf` passes every stored document through `frontmatterBlock`, which drops the body (`pkg/registry/core/core.go:2268`, `:2277-2283`). The filesystem registry produces it in `ArtifactRecord.ArtifactBytes` after `extends:` resolution, so a plain-skill parent contributes an empty chain block on both sides, as the server's stored row does. The SDK overlays produce it as the overlay record's frontmatter. The §4.7.10 delivery record frames the served document, which for such a skill is this one. The body comment is a §4.3.4-permitted pointer, and it makes the document byte-distinguishable from an authored `ARTIFACT.md` that a writer has not copied from it (D-7). Synthesis refuses a version that is neither a strict semver nor the reserved form, so no value can inject YAML keys.
- **D-7. Canonical-layout writers omit the synthesized document (D-15).** The `none` adapter (`pkg/adapter/none.go:21-28`) and the SDK `materialize()` writers (`sdks/podium-ts/src/index.ts:477`, `sdks/podium-py/podium/client.py:392`) skip `ARTIFACT.md` when the served document equals the D-6 bytes for its own `version:` line. A plain skill therefore materializes as a plain Agent Skills directory, the output carries no registry-assigned version, and reading the output back as a layer reproduces the source package. Every other adapter writes no `ARTIFACT.md` for a skill today, so its output is unchanged.
- **D-8. Filesystem source.** With no store, filesystem-source sync, `podium lint`, and the Go and SDK workspace overlays report `0.0.0-auto.0` for every skill whose version is assigned, whether or not it has an `ARTIFACT.md`. A registry that ingests the same directory for the first time assigns the same value to a canonical ID that one layer contributes. It assigns a higher N when more than one layer contributes the ID, as when an `extends:` child declares no version, and once it has ingested changed bytes for the skill. In those cases the lock-file `version` entries of the two sources differ, and §13.11.3 states it. The merged `ARTIFACT.md` of an `extends:` child carries the child's version (`pkg/manifest/merge.go:71`), and the `none` adapter writes it, so for an `extends:` child whose version is assigned the `none` output differs in that `version:` line as well. While the registry serves the directory's current bytes as the skill's latest row, the content hash stays identical, because it frames no version, and the output of every other adapter stays identical when the `extends:` reference resolves to the same parent in both modes. A revert is the exception. The registry rejects a change back to bytes a stored row holds under another version (D-4) and keeps serving its newer row, while the filesystem source serves the reverted bytes as `0.0.0-auto.0`. After a revert, the content hash, the body, and every adapter's output therefore differ between the sources until the author sets `metadata.version` or changes the content. On a server, a version-less same-ID overlay whose bytes change pins the most recently ingested non-deprecated row of its ID (§4.7.6). When no other layer has ingested a row of the ID since the overlay's previous row, that row is the pin, an edge within the overlay's layer that the §4.6 executable-field rule does not test. When a lower layer's row is newer, the pin crosses layers and the rule applies. The filesystem source resolves the same overlay to the next-lower-precedence record and tests that cross-layer edge under §4.6. It merges the overlay over that record when the overlay restates every executable field the record sets, and otherwise drops the overlay with `ingest.invalid_artifact`, serves the lower record, and exits 1. The two modes resolve the same parent only when the server's pin is that record. §13.11.3 already states that the two modes can test the rule on different edges (`spec/13-deployment.md:341`).
- **D-9. Lint (§4.3.4).** "Both present" becomes "`SKILL.md` present". A directory whose `ARTIFACT.md` declares `type: skill` with no `SKILL.md` stays an error. The required-version check and the semver check skip a skill whose version is assigned. A new warning, `lint.skill_version_assigned`, reports that the version is assigned, names any ignored `metadata.version` value, and names `metadata.version` as the fix. An authored reserved-form version in either file is `lint.invalid_version`. The `ARTIFACT.md` field and body rules run only when the skill has an authored `ARTIFACT.md`, which they already do in effect, because a plain skill's bytes carry no `name`, `description`, or `license` key. Lint codes are not §6.10 codes, so §6.10 does not change.
- **D-10. `podium artifact scaffold --type skill` (D2.4, D2.5)** writes `SKILL.md` alone, with `metadata.version` set from `--version` (default `0.1.0`). `--tags`, `--when-to-use`, `--extends`, and an explicitly passed `--sensitivity` are Podium-only fields with no home in `SKILL.md`. For `--type skill` the command refuses every one passed, in one message, with exit 2, and points to the hand-written `ARTIFACT.md` overlay. Other types are unchanged.
- **D-11. `podium import` (D2.6).** The command stays as an optional skills normalizer:
  - `--type` is removed, and passing it exits 2.
  - The output for each skill is `SKILL.md` plus its resources, with `metadata.version` set from `--version` (default `1.0.0`, validated as `major.minor.patch`). An existing `SKILL.md` whose `metadata.version` already equals `--version` is copied byte for byte.
  - The body is the root-level `SKILL.md`, or, when there is none, the single root-level `.md` file. Every other file, a nested `SKILL.md` included, is copied verbatim as a bundled resource.
  - A subdirectory of `--source` with no root-level `.md` file is skipped, as `buildImportPlan` skips a directory that yields no body today (`cmd/podium/import.go:96-98`). A skill nested deeper, such as `skills/greet/SKILL.md` under `--source`, is therefore not imported; the command imports immediate subdirectories only. When no subdirectory yields a skill, the command exits 1 with the existing `source directory contained no skills` message (`import.go:45-48`). Skipping keeps a source that is a Git clone importable, because its `.git/` directory holds no root-level `.md` file.
  - The command exits 1, naming the directory, when an existing `SKILL.md` carries a different `metadata.version`, when a source directory holds an `ARTIFACT.md`, when the chosen file has no frontmatter block, or when the root holds several `.md` files and no `SKILL.md`.
- **D-12. Podium-only fields (D2.5).** `when_to_use`, `tags`, `sensitivity`, `search_visibility`, `deprecated`, `replaced_by`, `release_notes`, `extends`, `mcpServers`, `runtime_requirements`, `sandbox_profile`, `target_harnesses`, `requiresApproval`, `effort_hint`, `model_class_hint`, `sbom`, and `external_resources` live in an optional `ARTIFACT.md` overlay that the author writes by hand. The overlay stays fully supported, no tool generates it, and the docs present it as the advanced path. An `extends:` child needs the overlay, and its parent may be a plain skill.
- **D-13. No new surface beyond what the decisions require.** The change adds no env var, no §6.10 error code, no store SPI method or schema column, no HTTP member, no `registry.yaml` key, no matrix cell, and no harness value. It adds one lint code (`lint.skill_version_assigned`), one `details.constraint` value of `ingest.invalid_artifact` (`auto_version_duplicate_content`, D-4), the reserved version form, and the synthesized document. `podium import` loses `--type`. Podium is pre-1.0, so no compatibility shim is added beyond keeping both-files skills working. The D-4 duplicate-content refusal reuses `RejectedArtifact` and `layer.InvalidArtifactCode` and adds only its `details.constraint` value. Every other ingest refusal on a path this change touches reuses an existing construction. A resolver failure for an `extends:` reference, an assigned-version child included, is the existing `RejectedArtifact` with `Code` `layer.InvalidArtifactCode` and nil `Details` (`pkg/registry/ingest/ingest.go:678-685`). An assigned-version child that leaves an executable field to a parent in another layer receives proposal 0057's rejection unchanged: `layer.InvalidArtifactCode`, the reason `layer.ExecutableInheritance.Reason` builds, and the `Details` that `layer.ExecutableInheritance.Details` returns (`ingest.go:1553-1561`). The reingest response copies those into the entry's `details` member (`pkg/registry/server/layers.go:2007-2018`). A whole-layer refusal, either a root-level `SKILL.md` or a frontmatter-less `SKILL.md` outside any package, wraps `ingest.ErrInvalidArtifact`. That sentinel is `errors.New(layer.InvalidArtifactCode)` (`ingest.go:54`), and the reingest endpoint answers it as HTTP 422 `ingest.lint_failed` (`layers.go:2052-2053`). A filesystem-source sync reports an executable-field drop as the `layer.Drop` that `composeExtends` builds with `layer.InvalidArtifactCode` (`pkg/registry/filesystem/extends.go:190-192`).
- **D-14. Spec first.** Every SPEC edit lands in one spec commit, because the §4.1, §4.2, §4.3, §4.3.4, §4.7.6, §13.4, and glossary edits contradict each other until all of them land. No spec edit follows a code step. The commit passes `make coverage-gate` before any code step. Code follows in dependency order (version helpers, discovery, lint, filesystem registry, ingest, serve, adapters, SDKs, and CLI), then tests, scenarios, and docs. One pull request carries all of it. The CHANGELOG entry goes in `[Unreleased]` under new `### Added` and `### Removed` groups and the existing `### Changed` group, toward 0.6.0. `[Unreleased]` already carries proposal 0057's backward-incompatible `extends:` entry, which lands in a MINOR bump, and this change's removal of `podium import --type` is backward-incompatible as well.
- **D-15. Resolved at sign-off.** Q-1 and Q-4: a version-less skill whose bytes a stored or soft-deleted row of its ID holds, other than its layer's latest row, is refused under D-4. A revert therefore neither receives a fresh N nor maps back to the earlier row, and no validator or cache surface changes. D-4 records the one cache outcome this leaves after a purge. Q-2: N stays derived from stored rows, and reissuing a purged N is acceptable. Q-3: the `none` adapter and the SDK `materialize()` writers omit the synthesized `ARTIFACT.md`, and the D-6 bytes keep the marker comment (D-7).

## Spec amendment: §4.1 Artifacts Are Packages of Arbitrary Files

**SPEC-1.** `spec/04-artifact-model.md`, §4.1, and `docs/assets/diagrams/artifact-package.svg`.

**Rationale.** Line 7 ("Every artifact has an `ARTIFACT.md`.") covers every type, and line 9, the alt text, and the ASCII fallback repeat it. Editing §4.3.4 alone would leave a direct contradiction (D-1). The paragraph also has to say where a plain skill's indexed fields come from.

Line 5 ("An artifact is a directory with one or two manifest files at its root.") is unchanged.

Current text (line 7, first sentence):

> **Every artifact has an `ARTIFACT.md`.**

Replacement (the rest of the paragraph is unchanged):

> **Every non-skill artifact has an `ARTIFACT.md`.**

Current text (line 9):

> **Skill artifacts (`type: skill`) additionally have a `SKILL.md`** alongside `ARTIFACT.md` to comply with the [Agent Skills specification](https://agentskills.io/specification). `SKILL.md` carries the standard's required frontmatter (`name`, `description`) and any of the standard's optional fields (`license`, `compatibility`, `metadata`, `allowed-tools`); its prose body holds the agent-facing skill instructions. `ARTIFACT.md` carries Podium's structured frontmatter and omits the prose body for skills (a one-line pointer comment is allowed). Field allocation across the two files and the lint rules that govern them are described in §4.3.4.

Replacement:

> **Skill artifacts (`type: skill`) have a `SKILL.md`** that complies with the [Agent Skills specification](https://agentskills.io/specification). `SKILL.md` carries the standard's required frontmatter (`name`, `description`) and any of the standard's optional fields (`license`, `compatibility`, `metadata`, `allowed-tools`); its prose body holds the agent-facing skill instructions. A package (§4.2) whose root holds a `SKILL.md` and no `ARTIFACT.md` is a skill artifact, and the registry indexes its `SKILL.md` frontmatter. An optional `ARTIFACT.md` beside `SKILL.md` carries Podium-only structured frontmatter and omits the prose body (a one-line pointer comment is allowed). Field allocation, version precedence, and lint are in §4.3.4.

Current alt text (line 45):

> Artifact package layout: a single directory containing ARTIFACT.md (required) and optionally SKILL.md, scripts/, references/, assets/, or other files.

Replacement:

> Artifact package layout for a skill: a single directory containing SKILL.md (required for a skill), an optional ARTIFACT.md, and optional scripts/, references/, assets/, or other files. An artifact of any other type carries ARTIFACT.md and no SKILL.md.

ASCII fallback (lines 49-50), replace the two manifest rows:

```
    ARTIFACT.md            [OPTIONAL] Podium-only frontmatter
    SKILL.md               [REQUIRED] agentskills.io body
```

`docs/assets/diagrams/artifact-package.svg` changes in the same commit:

- Line 97 (`dg-lead`): "A skill is a directory with a SKILL.md, an optional ARTIFACT.md, and optional bundled resources."
- Line 111 (`dg-label` for `ARTIFACT.md`): "OPTIONAL". The box keeps its class, or moves to `dg-card` when the primary emphasis moves to `SKILL.md`.
- Line 112 (`dg-sub` for `ARTIFACT.md`): "Podium-only frontmatter, no prose body".
- Line 118 (`dg-label` for `SKILL.md`): "REQUIRED", and the `SKILL.md` sub-text at line 119 moves from `x="556"` to `x="446"` to align with the row above.
- Line 149 (first `dg-foot`): "SKILL.md is required for a skill. Resources larger than the per-package cap use external_resources:". Line 150 is unchanged.

The shared `<style>` block stays byte-identical. Apply the text-fit procedure of `.claude/rules/doc-diagram-style.md`, render the page in both `data-theme` values and the file standalone with `preferredColorScheme` 0 and 1, and run the hex, em-dash, `shape`, and `§` sweeps.

## Spec amendment: §4.2 Registry Layout on Disk, package discovery

**SPEC-2.** `spec/04-artifact-model.md`, §4.2, the ASCII fallback (lines 89-122) and a new paragraph after it.

**Rationale.** Every skill in the §4.2 tree carries both files, and no spec text states which directories are packages, that discovery skips dot-prefixed directories, or that a manifest at the layer root is refused. All three rules exist only in code. §4.2 already defines the canonical ID and the leaf-equals-package structure, so the discovery rule belongs here.

In the ASCII fallback, remove the `ARTIFACT.md` line under `routing-validator/` and under `swift-bic-parser/`, so both show a plain skill. Keep it on `pay-invoice`, `reconcile-invoice`, and `run-variance-analysis`. `pay-invoice` keeps its overlay because `docs/authoring/extends.md` uses that ID as its overlay and `extends:` example. `docs/assets/diagrams/registry-layout.svg` needs no edit, because it draws no children for `routing-validator` or `swift-bic-parser`.

Insert after the closing `-->` of the ASCII fallback and before the paragraph that begins "The hierarchy can nest to arbitrary depth":

> A directory below the layer root is an artifact package when it directly contains a file named `ARTIFACT.md`, or when it directly contains a file named `SKILL.md` and no directory between it and the layer root is itself a package (§4.1). A `SKILL.md` below a package's root is a bundled resource of that package (§4.4) and starts no package. Package discovery does not descend into a directory below the layer root whose name begins with a dot, so no package is discovered inside one. Files under a dot-prefixed subdirectory of a package remain that package's bundled resources (§4.4). A layer whose skills sit under `.claude/skills/` or `.agents/skills/` therefore places its root at that directory or at the dot-prefixed directory itself: a `git` source sets `root` (§4.6), and a `local` source, `--layer-path`, or a `podium sync` path names the directory as its path. A manifest file directly at the layer root is an error, because the canonical ID is the non-empty directory path under the root.

No other §4.2 text changes.

## Spec amendment: §4.3 Artifact Manifest Schema, frontmatter location

**SPEC-3.** `spec/04-artifact-model.md`, §4.3, line 135.

**Rationale.** Line 135 says a skill's frontmatter lives in `ARTIFACT.md`, which is false for a plain skill. The `version: 1.0.0 # semver, author-chosen` comment in the universal-fields block (line 143) stays, because an authored `version` remains strict, author-chosen semver; §4.3.4 and §4.7.6 state the skill version sources and the reserved form.

Current text:

> For non-skill types, frontmatter and prose both live in `ARTIFACT.md`. For skills, frontmatter lives in `ARTIFACT.md` while prose lives in `SKILL.md`; a small subset of frontmatter fields is mirrored into `SKILL.md` to satisfy the agentskills.io specification (§4.3.4).

Replacement:

> For non-skill types, frontmatter and prose both live in `ARTIFACT.md`. For skills, the Agent Skills fields and the prose body live in `SKILL.md`, and the Podium-only fields live in an optional `ARTIFACT.md` (§4.3.4).

## Spec amendment: §4.3.4 SKILL.md compliance for type: skill

**SPEC-4.** `spec/04-artifact-model.md`, §4.3.4 (lines 252-294).

**Rationale.** Line 256 ("contains both") and the lint bullet at line 287 ("error if either is missing") make `ARTIFACT.md` mandatory for a skill. §4.3.4 owns field allocation, so it hosts version precedence (D-2), the synthesized document (D-6), the overlay framing (D-12), and lint (D-9).

Current text (the "**Two manifest files.**" paragraph):

> **Two manifest files.** A skill artifact directory contains both `SKILL.md` and `ARTIFACT.md`. `SKILL.md` is the agentskills.io manifest read by every SKILL.md-aware tool (the `npx skills` CLI, the public Vercel skills.sh registry, third-party skill validators, and any harness that consumes SKILL.md directly). `ARTIFACT.md` is Podium's structured manifest read by the registry, the MCP server, the SDKs, and `podium sync`.

Replacement:

> **Manifest files.** A skill artifact directory contains a `SKILL.md` and may contain an `ARTIFACT.md`. `SKILL.md` is the agentskills.io manifest read by every SKILL.md-aware tool (the `npx skills` CLI, the public Vercel skills.sh registry, third-party skill validators, and any harness that consumes SKILL.md directly). `ARTIFACT.md` is an optional overlay that the author writes by hand to carry Podium-only fields, and no Podium tool generates one for a skill. The registry, the MCP server, the SDKs, and `podium sync` read both files when both are present. An `extends:` child carries an `ARTIFACT.md`; the parent it names may be a skill without one.

In the field-allocation table, rename the third column header from `ARTIFACT.md` to `ARTIFACT.md (optional overlay)`. Replace the `type` row and the row that begins `` `version`, `when_to_use` `` with these three rows, in this order:

> | `type` | Not present | Top-level (`type: skill`); implied when `ARTIFACT.md` is absent |
> | `version` | As `metadata.version` (string) | Top-level; equal to `metadata.version` when `metadata.version` is a `major.minor.patch` value |
> | `when_to_use`, `tags`, `sensitivity`, `search_visibility`, `deprecated`, `replaced_by`, `release_notes` | Not present | Top-level |

The `metadata` row and every other row are unchanged.

Insert after the table and before "**Body content.**":

> **Version.** A skill's version is `SKILL.md` `metadata.version`, the `ARTIFACT.md` `version`, or both. When `ARTIFACT.md` declares `version`, that value is the version. Otherwise a `metadata.version` in `major.minor.patch` form is the version. A `metadata.version` in any other form is ignored, because `metadata` is free-form in the Agent Skills standard. When `ARTIFACT.md` declares `version` and `metadata.version` is a `major.minor.patch` value, the two are equal. A skill that declares no usable version carries an assigned version in the reserved form of §4.7.6 (§7.3.1, §13.11.3).
>
> **Synthesized `ARTIFACT.md`.** For a skill with no `ARTIFACT.md`, every response or record that serves an `ARTIFACT.md` document carries exactly these bytes, where `<version>` is the resolved version. Each line ends with a single line feed, and the document ends with the line feed after the comment.
>
> ```
> ---
> type: skill
> version: <version>
> ---
> <!-- Synthesized by Podium: this skill has no ARTIFACT.md. -->
> ```
>
> The document is not part of the authored package. The §4.7.6 content hash frames a zero-length value in its position, the registry does not store it, and no adapter or SDK writer materializes it (§6.7, §7.6). Those writers also omit an authored `ARTIFACT.md` whose bytes equal this document for the version it names.

The "**Body content.**" paragraph is unchanged.

Current lint list (after "**Skill-artifact ingest lint.** For every artifact with `type: skill`:"):

> - Both `SKILL.md` and `ARTIFACT.md` are present (error if either is missing).
> - `SKILL.md` has top-level `name` matching the parent directory and `description` non-empty (error).
> - `SKILL.md` `name` follows the agentskills.io syntax constraints (error).
> - `SKILL.md` does not contain Podium-only fields (`type`, `version`, `when_to_use`, `tags`, etc.); if present, error and recommend moving the field to `ARTIFACT.md`.
> - `ARTIFACT.md` does not contain `name`, `description`, or `license` fields (warning); if present, the values must match `SKILL.md` exactly (error on mismatch).
> - `ARTIFACT.md` body is empty or a single HTML comment (warning otherwise).
> - The `skills-ref validate` reference check from the agentskills.io project passes against `SKILL.md` (warning on failure; lint suppression flag available for cases where the standard's validator is overly strict).

Replacement:

> - `SKILL.md` is present (error if missing).
> - `SKILL.md` has top-level `name` matching the parent directory and `description` non-empty (error).
> - `SKILL.md` `name` follows the agentskills.io syntax constraints (error).
> - `SKILL.md` does not contain Podium-only fields (`type`, `version`, `when_to_use`, `tags`, etc.); if present, error. The diagnostic recommends `metadata.version` for a version and the `ARTIFACT.md` overlay for every other field.
> - When `ARTIFACT.md` is present, it does not contain `name`, `description`, or `license` fields (warning); if present, the values must match `SKILL.md` exactly (error on mismatch). A `version` in `ARTIFACT.md` equals `SKILL.md` `metadata.version` when both are set and `metadata.version` is a `major.minor.patch` value (error on mismatch).
> - When `ARTIFACT.md` is present, its body is empty or a single HTML comment (warning otherwise).
> - A skill that declares no usable version has its version assigned (warning that names any ignored `metadata.version` value and the fix).
> - An authored version in the reserved form of §4.7.6, in either file, is an error (`lint.invalid_version`).
> - The `skills-ref validate` reference check from the agentskills.io project passes against `SKILL.md` (warning on failure; lint suppression flag available for cases where the standard's validator is overly strict).

## Spec amendment: §4.4 Bundled Resources, a nested SKILL.md

**SPEC-5.** `spec/04-artifact-model.md`, §4.4, line 338.

**Rationale.** §4.4 and §4.7.6 already keep a nested `SKILL.md` as a bundled resource, and their root-exclusion wording ("other than the root `ARTIFACT.md`") reads correctly when no `ARTIFACT.md` exists. One sentence ties §4.4 to the new §4.2 discovery rule, so a reader of §4.4 alone does not conclude that a nested `SKILL.md` now starts a package. §4.7.6 line 888 is unchanged.

Current text (inside the first paragraph):

> ... and including a file named `SKILL.md` in a subdirectory of the package. Every file below a nested directory that carries its own `ARTIFACT.md` is excluded from the enclosing artifact's set.

Replacement:

> ... and including a file named `SKILL.md` in a subdirectory of the package. A `SKILL.md` in a subdirectory of a package does not start a package (§4.2). Every file below a nested directory that carries its own `ARTIFACT.md` is excluded from the enclosing artifact's set.

## Spec amendment: §4.7.6 Version Resolution and Consistency

**SPEC-6.** `spec/04-artifact-model.md`, §4.7.6 (lines 880, 882, 895-901).

**Rationale.** The grammar, the pin syntax, and resolution reject `0.0.0-auto.<N>`, so an assigned version would never resolve (D-3). D2.3 extends the absent-file framing to `ARTIFACT.md` (D-5). Stating the form as a semver 2.0.0 prerelease keeps every passage that says `@<semver>` or `(artifact_id, semver)` true (§4.2, the glossary, §1, and §11) with no edit there.

Current text (line 880):

> Versions are semver-named (`major.minor.patch`), author-chosen via the manifest's `version:` field. Internally, the registry stores `(artifact_id, semver, content_hash)` triples. `content_hash` is the SHA-256 digest of the artifact's canonical serialization.

Replacement:

> Versions are semver-named and author-chosen through `ARTIFACT.md` `version:` or, for a skill, `SKILL.md` `metadata.version` (§4.3.4). An authored version is `major.minor.patch`. A skill that declares no usable version carries a registry-assigned version `0.0.0-auto.<N>` (§7.3.1). That form is a semver 2.0.0 prerelease of `0.0.0`, where N is a non-negative decimal integer with no leading zeros, and it orders by semver precedence. The form is reserved to the registry, and an authored version in that form fails lint (§4.3.4). Internally, the registry stores `(artifact_id, semver, content_hash)` triples. `content_hash` is the SHA-256 digest of the artifact's canonical serialization. No two versions of one `artifact_id` share a `content_hash`, soft-deleted versions included: an authored version lies in the hashed bytes, and ingest refuses a registry-assigned version whose content a stored row of the artifact already holds (§7.3.1). A `sha256:` pin therefore selects at most one version of an artifact.

Line 882: after the sentence that ends "so an absent and an empty `SKILL.md` produce the same digest.", append:

> An artifact that carries no `ARTIFACT.md` frames a zero-length value in the first position in the same way. The content hash never frames the synthesized document of §4.3.4, and the §4.7.10 delivery record frames it as the served `ARTIFACT.md` document.

Lines 884 and 886 are unchanged.

Pinning list: insert after the bullet "`<id>@<semver>`: exact version.":

> - `<id>@0.0.0-auto.<N>`: exact registry-assigned version.

Append to the bullet "`<id>@<semver>.x`: minor or patch range (e.g., `1.2.x`, `1.x`).":

> A range never selects a registry-assigned version.

After the paragraph that defines `latest` ("`load_artifact(<id>)` resolves to `latest` = ..."), append to that paragraph:

> Registry-assigned versions are candidates for `latest`.

## Spec amendment: §4.7.10 Delivery Attestation, the served ARTIFACT.md document

**SPEC-7.** `spec/04-artifact-model.md`, §4.7.10, line 972.

**Rationale.** The same paragraph frames "An absent `SKILL.md`" as a zero-length value, and SPEC-6 adds the zero-length frame for an absent `ARTIFACT.md` to the §4.7.6 digest. A reader applying either rule by analogy would frame an empty value in the delivery record's `ARTIFACT.md` position, while the registry frames the synthesized bytes it serves. A third-party consumer would then compute a different `delivery_hash`. §7.2 "Record fields" maps fields to members and takes their content from §4.7.10, so it needs no edit.

Current text:

> the served `ARTIFACT.md` document, which for a child declaring `extends:` is the merged document (§4.6), for every type including a skill;

Replacement:

> the served `ARTIFACT.md` document for every type including a skill, which for a child declaring `extends:` is the merged document (§4.6) and for a skill with no `ARTIFACT.md` is the synthesized document of §4.3.4 rather than a zero-length value;

## Spec amendment: §7.3.1 Layer source ingest, registry-assigned versions

**SPEC-8.** `spec/07-external-integration.md`, §7.3.1, the "**Ingest cases:**" table and the text after it, `spec/11-verification.md`, §11 "Versioning and ingest tests" (line 45), and `spec/06-mcp-server.md`, §6.5 Cache, the "**Content cache.**" bullet (line 241).

**Rationale.** §7.3.1's immutability rule forces a version that changes with content, and the revert and concurrency hazards need explicit rules (D-4). The §4.7 invariant at line 741 is stated per `(artifact_id, version)` pair and needs no edit. The behavior is new, so §11 lists it. A purge that releases refused bytes lets one content hash name a second version over time, while §6.5 calls the content cache immutable by definition, so §6.5 states the outcome an MCP server cache shows for such bytes (D-4).

Insert a row into the "**Ingest cases:**" table after the row "Same version, different content_hash":

> | Skill with no declared version | Assigned `0.0.0-auto.<N>` (§4.7.6). When the most recently ingested non-deleted row this layer contributed for the artifact, at any version, carries an equal content hash, its version is reused and the ingest is a no-op. Otherwise, when any stored or soft-deleted row of the artifact, from any layer, carries an equal content hash, the artifact is rejected as `ingest.invalid_artifact` with `details.constraint: "auto_version_duplicate_content"`, and the reason tells the author to declare `metadata.version` (§4.3.4) or change the content. Otherwise N is one more than the highest N that any stored or soft-deleted row of the artifact carries, or 0 when there is none. |

Insert after the table and before "**Layer CLI.**":

> **Registry-assigned versions.** The table's rules apply to a skill whose version the registry assigns (§4.3.4):
>
> - A revert to earlier bytes, including a revert across an interval in which the skill declared a version, is rejected under the table's rule, and `latest` (§4.7.6) keeps serving the newer row. A soft-deleted row counts, because restoring its layer (§8.4) serves it again, so a layer registered again under its ID rejects its unchanged version-less skills until the purge removes the earlier rows.
> - Two ingests never commit one registry-assigned version with different bytes. The ingest that finds the version taken, whether before or at commit, reads the stored rows again and applies the table's rule to them, so it reuses its layer's matching row, rejects the skill as duplicate content, or assigns a fresh N. An ingest that keeps finding a freshly assigned version taken reports `ingest.immutable_violation` for that artifact, and a later ingest assigns it.
> - A purge (§8.4) that removes the row holding the highest N lets a later ingest assign that N again, and a purge that removes every row carrying rejected bytes lets a later ingest accept them. The accepted bytes carry a registry-assigned version other than the purged row's under the same content hash, and an MCP server content cache that holds the purged row's record reports the purged version for them (§6.5).
> - Removing `ARTIFACT.md` from a skill that keeps its version in `metadata.version` changes the content hash (§4.7.6) and is rejected as `ingest.immutable_violation`. The author changes the version or drops the declaration.

In the "**Ingest outcome.**" paragraph of §7.3.1, after "an unresolved `extends` pin (`ingest.invalid_artifact`),", insert:

> a skill whose registry-assigned version would give stored content a second version (`ingest.invalid_artifact`, the ingest cases above),

In the same paragraph, replace "with a `details` object when the rule that rejected it defines one, as the §4.6 executable-field rule does." with:

> with a `details` object when the rule that rejected it defines one, as the §4.6 executable-field rule and the registry-assigned-version rule do.

`spec/11-verification.md`, the "**Versioning and ingest tests**" bullet: after "same-version-different-content ingests return `ingest.immutable_violation`;", insert:

> a skill with no declared version is assigned `0.0.0-auto.0` on first ingest, reuses it on unchanged bytes, receives the next N on changed bytes, skips an N held by a soft-deleted row and never reuses a soft-deleted row's version, is rejected with `ingest.invalid_artifact` and `details.constraint: "auto_version_duplicate_content"` on a revert to bytes a stored or soft-deleted row holds, is accepted again once a purge removes that row, and two concurrent ingests of different bytes commit distinct N;

`spec/06-mcp-server.md`, §6.5, the "**Content cache.**" bullet. Current text:

> - **Content cache.** Maps `content_hash` to manifest bytes + bundled resources. Forever (immutable by definition).

Replacement:

> - **Content cache.** Maps `content_hash` to manifest bytes + bundled resources. Forever (immutable by definition). A skill whose version the registry assigns is the exception for its version and its served `ARTIFACT.md`: after a purge lets the registry accept content it had rejected under a new registry-assigned version (§7.3.1), a cached record of that content reports the purged version and, for a skill with no `ARTIFACT.md`, the synthesized document that names it (§4.3.4). A HEAD revalidation and a `304` confirm the content hash only, so the record keeps reporting that version until the cache no longer holds it. Its body and bundled resources are the bytes the registry serves.

## Spec amendment: §13.11.1 and §13.11.3 Filesystem source, plain skills

**SPEC-9.** `spec/13-deployment.md`, §13.11.1 (the tree) and §13.11.3.

**Rationale.** A filesystem source has no store, so §13.11 has to state the version it reports for an unversioned skill (D-8), and the tree shows no `SKILL.md`-only package. The existing `run-variance-analysis/` annotation stays accurate, because that package keeps both files.

In the §13.11.1 tree, replace the two lines

```
│   └── platform/
│       └── …
```

with

```
│   └── platform/
│       └── onboarding/
│           └── welcome/                 # type: skill — SKILL.md alone
│               └── SKILL.md
```

No other tree line changes.

§13.11.3: insert after the paragraph that begins "The composer, parsers, glob resolver, `extends:` resolver, and harness adapters" and before "**Layer collisions.**":

> **Skills without a declared version.** A filesystem source keeps no ingest history. It records a skill that declares no usable version (§4.3.4) as `0.0.0-auto.0` in the lock file. A registry ingesting the same directory for the first time assigns the same value to a canonical ID that one layer contributes. It assigns a higher N when more than one layer contributes the ID, as when an `extends:` child declares no version (§4.6, §7.3.1). It also assigns a higher N once it has ingested changed bytes for the skill. In those cases the lock-file `version` entries of the two sources differ. The merged `ARTIFACT.md` of an `extends:` child carries the child's version (§4.6), so for such a child the `none` adapter output differs in that version as well. While the registry serves the directory's current bytes as the skill's latest row, the content hash stays identical, and the output of every other harness adapter stays identical when the `extends:` reference resolves to the same parent in both sources, which the executable-inheritance paragraph below states need not hold for a same-ID overlay. A registry rejects a change of such a skill back to bytes a stored row holds under another version (§7.3.1) and keeps serving its latest row, while a filesystem source, which keeps no history, serves the reverted bytes as `0.0.0-auto.0`. After such a revert, the content hash, the body, and the output of every harness adapter differ between the two sources until the author declares `metadata.version` or changes the content.

## Spec amendment: §6.7 and §7.6, the canonical layout of a plain skill

**SPEC-10.** `spec/06-mcp-server.md`, §6.7 (line 320), and `spec/07-external-integration.md`, §7.6 Language SDKs (line 619, the Python example before §7.6.1 at line 637).

**Rationale.** After D-6 the synthesized document reaches the `none` adapter and the SDK writers, and both texts list `ARTIFACT.md` unconditionally. The writers recognize the document by byte equality (D-7), so the rule covers an authored `ARTIFACT.md` identical to it as well, and §6.7 states it in those terms. §7.6 line 629 and §2 already define `materialize()` output as the `none` adapter's output, so §7.6 needs only its example comment changed.

Current text (§6.7, first sentence of line 320):

> `none` writes the canonical layout (`ARTIFACT.md`, `SKILL.md`, bundled resources) without translation.

Replacement (the rest of the line is unchanged):

> `none` writes the canonical layout (`ARTIFACT.md`, `SKILL.md`, and bundled resources) without translation. It omits the served `ARTIFACT.md` when its bytes equal the synthesized document of §4.3.4 for the version that document names, which covers a skill with no `ARTIFACT.md` and an authored `ARTIFACT.md` identical to that document.

Current text (§7.6, the comment in the Python example):

```
artifact.materialize(to="./artifacts/")  # canonical layout: ARTIFACT.md, SKILL.md for a skill, and bundled resources
```

Replacement:

```
artifact.materialize(to="./artifacts/")  # canonical layout: ARTIFACT.md unless it equals the §4.3.4 synthesized document, SKILL.md for a skill, and bundled resources
```

## Spec amendment: §11 Filesystem ↔ server equivalence test

**SPEC-11.** `spec/11-verification.md`, the "**Filesystem ↔ server equivalence test**" bullet (line 33).

**Rationale.** The two consumers reach a plain skill's version and document through different code paths: the filesystem side fixes the version at `0.0.0-auto.0` and synthesizes after `extends:` resolution, and the server side derives N from stored rows and synthesizes at serve time. The general byte-identical sentence does not pin that path. The test names a fresh store, because a standalone server that already ingested earlier bytes reports a higher N (§13.11.3).

After the sentence that ends "which the filesystem consumer computes and the server consumer records from the registry.", insert:

> The directory also carries a skill with no `ARTIFACT.md` and no declared version, a skill with no `ARTIFACT.md` and a `SKILL.md` `metadata.version`, a skill whose `ARTIFACT.md` overlay declares no `version` while its `SKILL.md` declares `metadata.version`, and `extends:` children of the first two skills, one of which declares its version only in `metadata.version`. No other layer contributes the canonical ID of any of these skills. The comparison therefore covers the zero-length `ARTIFACT.md` frame of the §4.7.6 digest, the synthesized `ARTIFACT.md` (§4.3.4), the version of a skill whose served `ARTIFACT.md` omits it, and the merged document of a child whose overlay omits it. It also covers the registry-assigned version `0.0.0-auto.0`, which the filesystem consumer reports and the server consumer assigns on its first ingest. The server consumer ingests the directory into a store that holds no earlier rows for these artifacts, because a server that has ingested changed bytes for a skill reports a different registry-assigned version (§13.11.3).

The rest of the bullet is unchanged.

## Spec amendment: §0, §2, §4.5.2, §6.4, §7.3.1, §14, and the glossary, package wording

**SPEC-12.** Consistency edits in `spec/00-quickstart.md`, `spec/02-architecture.md`, `spec/04-artifact-model.md` §4.5.2, `spec/06-mcp-server.md` §6.4, `spec/07-external-integration.md` §7.3.1, `spec/14-common-scenarios.md`, and `spec/glossary.md`.

**Rationale.** Each line defines a package or a skill as "`ARTIFACT.md` plus `SKILL.md` for skills". After SPEC-1 they would be inaccurate, the glossary "Package" entry and the "Skills additionally have a `SKILL.md`" clause of the "Manifest" entry in particular. Each restatement of the package definition points to the §4.2 discovery rule rather than carrying a variant of it. The "Manifest" entry keeps the term bound to `ARTIFACT.md` alone. §13.4 uses "manifest" for the `ARTIFACT.md` slot beside a separately listed `SKILL.md`, in the first-start rewrite ("from the stored manifest, `SKILL.md`, and bundled-resource bytes", `spec/13-deployment.md:84`), in admission check (1) (`:90`), in check (2) ("a manifest that is empty or does not parse declares none", `:90`), and in the SPEC-13 predicate. A plain skill's manifest is therefore absent on disk and empty in its stored row, which is the zero-length first frame SPEC-6 adds to §4.7.6. Defining a plain skill's manifest as its `SKILL.md` would frame `SKILL.md` in the first slot of both recomputations and make the SPEC-13 predicate hold for no row.

**Glossary, "Manifest" (line 11).** Current text:

> - **Manifest**: the `ARTIFACT.md` file specifically. Skills additionally have a `SKILL.md` (§4.3.4) carrying the agentskills.io-spec frontmatter and the agent-facing prose body; for skills the term "manifest" without qualification refers to `ARTIFACT.md`, and "skill manifest" refers to `SKILL.md`.

Replacement:

> - **Manifest**: the `ARTIFACT.md` file specifically. Skills also have a `SKILL.md` (§4.3.4) carrying the agentskills.io-spec frontmatter and the agent-facing prose body; for skills the term "manifest" without qualification refers to `ARTIFACT.md`, and "skill manifest" refers to `SKILL.md`. A skill's `ARTIFACT.md` is optional (§4.3.4). A skill without one has no manifest on disk, and its stored manifest is empty (§13.4).

**Glossary, "Package" (line 16).** Replacement:

> - **Package**: the on-disk directory containing an artifact's manifest files (an `ARTIFACT.md`, a `SKILL.md`, or both, per the §4.2 discovery rule) and bundled resources.

**§2, LocalOverlayProvider (line 102).** Replace "(`ARTIFACT.md` for every type, plus `SKILL.md` for skills)" with "(an `ARTIFACT.md`, a `SKILL.md`, or both, per the §4.2 discovery rule)".

**§6.4 (line 211).** Replace "(`ARTIFACT.md` for every type, plus `SKILL.md` for skills)" with "(an `ARTIFACT.md`, a `SKILL.md`, or both, per the §4.2 discovery rule)".

**§6.4 (line 222).** Current text:

> Format: same `ARTIFACT.md` (plus `SKILL.md` for skills) and frontmatter as the registry.

Replacement:

> Format: the same package layout (§4.2) and frontmatter as the registry.

**§14 (line 11).** Replace "(each artifact a subdirectory containing `ARTIFACT.md`, plus `SKILL.md` for skills)" with "(each artifact a subdirectory containing an `ARTIFACT.md`, a `SKILL.md`, or both, per the §4.2 discovery rule)".

**§14 (line 200).** Current text:

> 1. Edit `<workspace>/.podium/overlay/finance/cashflow/forecast/ARTIFACT.md` (and `SKILL.md` if `forecast` is a skill). The MCP server (or sync, or SDK) sees it immediately.

Replacement:

> 1. Edit `<workspace>/.podium/overlay/finance/cashflow/forecast/SKILL.md` if `forecast` is a skill (and its `ARTIFACT.md` overlay when it has one), or `.../forecast/ARTIFACT.md` for any other type. The MCP server (or sync, or SDK) sees it immediately.

**§4.5.2 (line 459).** Replace "(the directory where its `ARTIFACT.md` lives, plus `SKILL.md` for skills)" with "(its package directory, which holds an `ARTIFACT.md`, a `SKILL.md`, or both, per §4.2)".

**§7.3.1, Author flow step 1 (line 71).** Replace "Edit `ARTIFACT.md` (and `SKILL.md` for skills, plus bundled resources) in a checkout of the layer's Git repo." with "Edit `SKILL.md` for a skill or `ARTIFACT.md` for any other type, plus any `ARTIFACT.md` overlay and bundled resources, in a checkout of the layer's Git repo." The rest of the step is unchanged.

**§0 Quickstart (lines 3-44).** Replace the opening sentence:

> A minimal skill artifact has both a `SKILL.md` (per the [agentskills.io specification](https://agentskills.io/specification)) and an `ARTIFACT.md` (per Podium's schema):

with:

> A minimal skill artifact is a directory with a `SKILL.md` (per the [agentskills.io specification](https://agentskills.io/specification)):

Remove the `└── ARTIFACT.md` line from the tree and change `├── SKILL.md` to `└── SKILL.md`. In the `SKILL.md` example, keep `license: MIT` and add, after it:

```yaml
metadata:
  version: "1.0.0"
```

Replace the `ARTIFACT.md` paragraph and its code block (from "`ARTIFACT.md` (Podium structured frontmatter; no body):" through the closing fence) with:

> `metadata.version` sets the version. An optional `ARTIFACT.md` beside `SKILL.md` carries Podium-only fields such as `when_to_use`, `tags`, and `sensitivity` (§4.3.4).

In the ingest block, replace `git add SKILL.md ARTIFACT.md` with `git add SKILL.md`, and keep the commit message `Add run-variance-analysis@1.0.0`.

## Spec amendment: §13.4 stored-row admission, a skill with no ARTIFACT.md

**SPEC-13.** `spec/13-deployment.md`, §13.4, the stored-row admission paragraph that begins "Before the registry serves an artifact's manifest" (line 90).

**Rationale.** The paragraph serves a row whose manifest is empty from its stored values and states that ingest never stores such a row. After this change ingest stores one for every plain skill, so the sentence becomes false, and the stored-value fallback would let a metadata-store writer without the signing key change the body, type, description, tags, sensitivity, deprecation flag, replacement, and `audit_redact` keys of every plain skill while admission still passes on the unchanged `SKILL.md` and resource bytes (D-5). The §4.6 merge over the admitted rows would carry an edited description or tags of a plain-skill parent into its `extends:` child's served `ARTIFACT.md`. The rule keeps the existing guarantee that those fields come from admitted bytes.

Current text:

> A row whose manifest is empty or does not parse is served with its stored values; ingest never stores such a row.

Replacement:

> A row whose manifest is empty and whose `SKILL.md` parses is a skill with no `ARTIFACT.md` (§4.3.4). Its type is `skill`, its body and description are the admitted `SKILL.md` body and description, it declares no tags, sensitivity, deprecation, replacement, or `audit_redact` key, and its served `ARTIFACT.md` is the synthesized document of §4.3.4. The §4.6 merge for an `extends:` child of such a row takes these values for the row. Any other row whose manifest is empty or does not parse is served with its stored values; ingest never stores such a row.

No other §13.4 text changes. Check (2) already says a manifest that is empty declares no `extends:`, which holds for such a row. The first-start rewrite (`spec/13-deployment.md:84`) and check (1) (`:90`) recompute the digest "from the stored manifest, `SKILL.md`, and bundled-resource bytes". Under the glossary "Manifest" entry, which SPEC-12 keeps bound to `ARTIFACT.md`, the stored manifest of such a row is empty, so both recomputations frame the zero-length first value SPEC-6 adds to §4.7.6.

## Proposed solution

### CODE-2. `pkg/manifest`: skill version, auto form, and synthesized document

New file `pkg/manifest/skill_version.go`, beside `ValidateVersion`:

- `var autoVersionRegex = regexp.MustCompile(`^0\.0\.0-auto\.(0|[1-9]\d*)$`)`.
- `func AutoVersion(n int) string` returns `fmt.Sprintf("0.0.0-auto.%d", n)`. Its doc comment states that n is non-negative.
- `func IsAutoVersion(s string) (n int, ok bool)` matches the regex and parses N with `strconv.Atoi`; an overflow returns `false`.
- `func ResolveSkillVersion(a *Artifact, s *Skill) string` returns `a.Version` when `a` is non-nil and the field is set, then `s.Metadata["version"]` when `s` is non-nil and `ValidateVersion` accepts it, and otherwise `""`. Callers derive `VersionAssigned` as `v == ""`.
- `func SynthesizedSkillDocument(v string) ([]byte, error)` returns `fmt.Errorf("%w: %q", ErrInvalidVersion, v)` unless `ValidateVersion(v) == nil` or `IsAutoVersion(v)` holds. Otherwise it returns the D-6 bytes, built from one prefix constant (`"---\ntype: skill\nversion: "`) and one suffix constant (`"\n---\n<!-- Synthesized by Podium: this skill has no ARTIFACT.md. -->\n"`).
- `func IsSynthesizedSkillDocument(doc []byte) bool` checks the prefix and suffix, takes the middle as `v`, and returns true when `SynthesizedSkillDocument(v)` succeeds and equals `doc`. It does no YAML parse.

Replace the `semverRegex` comment at `pkg/manifest/parse.go:206-207` with: "semverRegex matches an authored major.minor.patch version. The registry-reserved 0.0.0-auto.<N> form (§4.7.6) is validated separately by IsAutoVersion and is never accepted from an author." `ValidateVersion` stays strict. Update the doc comment in `pkg/manifest/skill_fields.go` that names `version` as a forbidden top-level key, so it names `metadata.version` as the home of a skill's version.

Annotations: `// Spec: §4.3.4` on `ResolveSkillVersion`, `SynthesizedSkillDocument`, and `IsSynthesizedSkillDocument`, and `// Spec: §4.7.6` on `AutoVersion` and `IsAutoVersion`.

### CODE-1. `pkg/version`: the reserved form in pins and resolution

- Add `Auto bool` and `AutoN int` to `Pin`.
- In `ParsePin`, before the "." split, call `manifest.IsAutoVersion(s)`. On a match, return `Pin{Kind: PinExact, Major: 0, Minor: 0, Patch: 0, Auto: true, AutoN: n}`. Every other suffix, and an `-auto.` suffix on any other core, stays `ErrInvalidPin`. Add the row `"0.0.0-auto.N" -> PinExact (registry-assigned)` to the doc-comment list.
- `Resolve` keeps each parsed candidate paired with its source string and returns that string. This replaces the `fmt.Sprintf` at `version.go:139`, so the result always equals a stored version string. No `Pin.String()` is added.
- `matches`: `PinMajor` and `PinMinor` return false for an `Auto` candidate. `PinExact` also requires equal `Auto` and `AutoN`.
- `less`: compare the core, then rank a non-auto version above an auto one, then compare `AutoN`.
- `ResolveLatest`, `Compare`, `parseCore`, and `AtLeast` are unchanged. `Compare` stays core-only because `parseCore` cuts at "-" or "+" before calling `ParsePin`.
- `ParsePin`, `Resolve`, `matches`, and `less` carry `// Spec: §4.7.6`.

`pkg/version` gains a production import of `pkg/manifest`. `pkg/manifest` imports only `yaml`, so no cycle forms.

### CODE-3. Discovery: one predicate, the SKILL.md trigger, and VersionAssigned

New file `pkg/registry/filesystem/discovery.go`:

```go
// PackageManifests reports whether dir directly holds a regular file named
// ARTIFACT.md and whether it directly holds one named SKILL.md. Both Go
// walkers use it: a directory holding ARTIFACT.md is a package and a
// nested-package boundary (§4.4), and a directory holding SKILL.md alone is a
// package only when no directory between it and the layer root is one (§4.2).
// A stat failure other than fs.ErrNotExist is returned rather than read as an
// absent manifest, so a confined source that refuses a manifest fails the
// layer's ingest instead of silently dropping the package (§7.3.1).
//
// Spec: §4.2, §4.4, §7.3.1
func PackageManifests(fsys fs.FS, dir string) (artifact, skill bool, err error)
```

It uses `fs.Stat` and `Mode().IsRegular()`, so a directory named `SKILL.md` or `ARTIFACT.md` is not a manifest. `fs.Stat` follows a symlink, as the `os.Stat` and `fs.Stat` calls in the predicates it replaces do (`pkg/registry/filesystem/walk.go:325-328`, `pkg/registry/ingest/ingest.go:1744-1746`), so a symlink named `SKILL.md` or `ARTIFACT.md` that resolves to a regular file is a manifest in both walkers, and one that resolves to a directory or to nothing (`fs.ErrNotExist`) is not.

Every other `fs.Stat` error is returned unchanged as `err`, with both booleans false. Those predicates discard it (`err == nil && !info.IsDir()`), which was harmless only because both walkers detect a package by the entry name `ARTIFACT.md` (`walk.go:203-205`, `ingest.go:1039-1041`) and then read the file, so a refused read failed the layer. Under `source.ConfinedFS`, which the bootstrap and local-layer ingests pass (`internal/serverboot/serverboot.go:492`, `:653`), a manifest symlink that leaves the layer directory or has an absolute target makes `Stat` return an `ErrSourceUnreachable`-wrapped error (`pkg/layer/source/confined.go:46-50`, `:82-86`), while an absent path keeps `fs.ErrNotExist`. Discarding that error would skip the package and ingest the layer without it, contrary to §7.3.1 local-source confinement ("A read the ingest requires and cannot satisfy fails that layer's ingest", `spec/07-external-integration.md:139`). Every caller returns a non-nil `err` from its walk callback: both walkers' directory visits and both resource captures' nested-boundary checks (`walk.go:280`, `ingest.go:1220`). The ingest walk then fails the call with an error that `errors.Is` matches to `source.ErrSourceUnreachable`, which the reingest endpoint answers as HTTP 502 `ingest.source_unreachable` (`pkg/registry/server/layers.go:2060-2061`), as an escaping `SKILL.md` companion does today (`pkg/registry/ingest/source_confinement_test.go:126`). The filesystem walker reads through `os.DirFS`, which follows a symlink anywhere, so it returns only a genuine stat failure such as a permission error, and `podium sync` and `podium lint` report it as they report any walk error.

Both walkers (`walkLayer` in `pkg/registry/filesystem/walk.go` and `walkLayer` in `pkg/registry/ingest/ingest.go`) recognize a package on the directory entry rather than on the file name:

- When visiting a directory `d`, including the layer root, keep the dot-directory `fs.SkipDir` for `d != "."`. Then call `PackageManifests(fsys, d)`, and return its `err` when non-nil.
- When `artifact` is true, load the package from `ARTIFACT.md` as today.
- When `artifact` is false and `skill` is true, load the package as a plain skill unless an ancestor of `d` below the root is in the walk's set of package directories. `fs.WalkDir` visits a directory before its contents, so the set is complete for every ancestor.
- Record each loaded package directory in the set.
- A package at the layer root fails through `ValidateCanonicalID` as a root-level `ARTIFACT.md` does today. Change the message at `walk.go:340` from "artifact must live in a subdirectory of the layer (a root-level ARTIFACT.md has no canonical ID)" to exactly "artifact must live in a subdirectory of the layer (a root-level ARTIFACT.md or SKILL.md has no canonical ID)". The edit replaces only the parenthetical, so the existing assertions on the substring `subdirectory` stay unchanged and pass: `pkg/registry/filesystem/walk_test.go:542` and `:576`, `pkg/registry/ingest/ingest_test.go:493`, `pkg/registry/ingest/walk_internal_test.go:34`, `test/e2e/canonical_id_test.go:39`, and `test/integration/canonical_id_test.go:52`. The ingest walker wraps every `ValidateCanonicalID` error, this one and the `@`-segment one, with `invalidArtifact` (`pkg/registry/ingest/ingest.go:1160`), so `podium layer reingest` answers HTTP 422 `ingest.lint_failed` (`pkg/registry/server/layers.go:2052-2053`). Today `loadOne` returns it unwrapped (`ingest.go:1168-1170`), which reaches the endpoint's default arm and reports a retryable `registry.unavailable` (HTTP 500) for a permanent content fault. The filesystem walker returns the error unchanged, and `podium sync` and `podium lint` report it as they report any walk error.

The filesystem walker calls `PackageManifests(os.DirFS(layerRoot), d)` with slash paths. Remove `hasArtifactManifest` (`walk.go:323-328`) and `fsHasArtifactManifest` (`ingest.go:1742-1747`), and make `captureResources` and the ingest resource capture cut at a subdirectory whose `PackageManifests` reports `artifact`, returning its `err` when non-nil. A nested `SKILL.md` therefore stays a resource, as today.

A plain-skill package loads `SKILL.md` with `ParseSkill`, sets `Artifact = &manifest.Artifact{Type: manifest.TypeSkill}`, and leaves `ArtifactBytes` and `AuthoredBytes` nil. A `SKILL.md` that fails `ParseSkill` fails the layer walk as an unparseable `ARTIFACT.md` does: the filesystem walk returns the error, and ingest classifies it through `invalidArtifact` and fails the call.

`filesystem.ArtifactRecord` gains `VersionAssigned bool`, documented as "the skill declares no usable version (§4.3.4), so the version is assigned". For every record whose type is `skill`, both loaders set `rec.Artifact.Version = manifest.ResolveSkillVersion(rec.Artifact, rec.Skill)` and `rec.VersionAssigned = rec.Artifact.Version == ""`. A skill with both files whose `ARTIFACT.md` and `SKILL.md` declare no usable version is therefore `VersionAssigned`.

Update the loop comments at `ingest.go:525` ("find every ARTIFACT.md") and in `walk.go` to name both manifests. Annotate the walkers with `// Spec: §4.2`.

### CODE-7. Lint: assigned version, reserved form, mismatch, and overlay

Targets: `pkg/lint/lint.go` (`ruleRequiredFields`, `ruleVersionSemver`, and `AllRulesWithClient`) and `pkg/lint/skill_rules.go` (`ruleSkillPodiumOnlyFields`, `ruleSkillArtifactFields`, and a new rule).

- `ruleRequiredFields`: skip "version is required" when `rec.VersionAssigned` is set.
- `ruleVersionSemver`: skip a record with `VersionAssigned` set. A record whose `ARTIFACT.md` declares a malformed or reserved-form version still fails here, because `ResolveSkillVersion` returns the authored `ARTIFACT.md` value first.
- New `ruleSkillVersionAssigned`, code `lint.skill_version_assigned`, severity warning. It fires when `rec.VersionAssigned` is set. Its message is "no usable version declared; the registry assigns 0.0.0-auto.<N>. Set metadata.version in SKILL.md to choose one", and when `metadata.version` holds an ignored value it prefixes "metadata.version %q is not major.minor.patch and is ignored; ". Register it after `ruleSkillCompliance`.
- New check in the same file, code `lint.invalid_version`, severity error: when `rec.Skill.Metadata["version"]` satisfies `manifest.IsAutoVersion`, report "metadata.version %q uses the registry-reserved form". This is the only path that reports an authored reserved form in `SKILL.md`, because `ResolveSkillVersion` ignores a non-semver `metadata.version`.
- `ruleSkillArtifactFields`: keep reading `rec.ArtifactBytes`. After the existing loop, when `rec.Skill` is non-nil, let `mv := rec.Skill.Metadata["version"]`. When `manifest.ValidateVersion(mv) == nil` and `rec.Artifact.Version != mv`, emit `lint.skill_artifact_field_mismatch` as an error: "ARTIFACT.md version %q does not match SKILL.md metadata.version %q". `ResolveSkillVersion` makes the two equal whenever `ARTIFACT.md` declares none, so the check fires only on an authored mismatch.
- `ruleSkillPodiumOnlyFields`: for the key `version`, recommend `SKILL.md` `metadata.version`. Every other key keeps recommending the `ARTIFACT.md` overlay.
- `ruleSkillCompliance` and `ruleArtifactBodyForSkill` are unchanged. A plain skill reaches neither diagnostic: it always has a `SKILL.md`, and its body at ingest is empty and under `podium lint` is the synthesized single HTML comment, which `skillArtifactBodyIsAllowed` accepts.
- Annotate each changed rule with `// Spec: §4.3.4`.

### CODE-4. Filesystem registry: assign `0.0.0-auto.0` and synthesize after `extends:` resolution

Targets: `pkg/registry/filesystem/walk.go` (`walkLayer`'s loader and `Registry.Walk`) and `cmd/podium-mcp/local_search.go` (`manifestBodyOf`).

**In the loader (`walkLayer`, filesystem package only).** After CODE-3's version resolution:

- For a skill record with `VersionAssigned` set, set `Artifact.Version = manifest.AutoVersion(0)`. `VersionAssigned` stays true, so lint still skips the required and semver checks.
- For every skill record whose `AuthoredBytes` is empty, whether or not its version is assigned, set `Artifact.Description` from `Skill.Description`. This mirrors the server. CODE-6's admission sets an admitted plain-skill row's description from its `SKILL.md`, the value ingest also stores in the `Description` column (`pkg/registry/ingest/ingest.go:1256-1268`), and `parsedArtifact` (`pkg/registry/core/core.go:1993-2006`) reads that admitted value for every row whose stored manifest is empty. An `extends:` child therefore merges the same parent description in both modes, whether its plain-skill parent declares `metadata.version` or not (`pkg/manifest/merge.go` keeps the parent's description when the child sets none).

The loader runs before `composeExtends`, whose every round re-runs `dedupe` over the records not yet refused (`pkg/registry/filesystem/extends.go:38-73`), so `deduped` and `kept` carry the values in each round, and `validateExtends` and `resolveExtends` both see them on a parent. The loader does not touch `ArtifactBytes`, so a plain-skill parent's chain block stays empty in the §4.6 executable-field comparison (`executableInheritance`, `extends.go:366`) and in `authoredChain` (`extends.go:390`). This matches the server, where `core.ResolvedArtifact` passes the plain-skill row's empty `Frontmatter` as the block (`pkg/registry/core/core.go:1887-1893`) and `manifest.decodeChain` skips an empty block. A plain-skill parent therefore sets no executable field in either mode. On the filesystem side its `Artifact` carries only type, version, and description. On the server side `parsedArtifact`'s column fallback carries no executable field (`core.go:1993-2006`).

**In `Registry.Walk`, on the slice each path returns.** `Walk` (`pkg/registry/filesystem/walk.go:52-78`) returns `composeExtends(all, opts)` from inside `if opts.ResolveExtends`, and otherwise returns `dedupe`'s first result after `reportDrops`. Restructure the tail so both paths assign one `records` slice and one `err`, return on `err`, and then call a new unexported `synthesizeSkillDocuments(records []ArtifactRecord) error` before returning `records`. For each record whose type is `skill` and whose `AuthoredBytes` is empty, the helper sets `ArtifactBytes` from `manifest.SynthesizedSkillDocument(rec.Artifact.Version)`. A synthesis error returns `fmt.Errorf("filesystem: synthesize ARTIFACT.md for %s: %w", rec.ID, err)`. The version is either a validated `metadata.version` or the auto form, so the error marks a broken invariant rather than an authoring fault. The helper runs after `composeExtends` returns, so the §4.6 validation and the rewrite pass read a plain skill's nil `ArtifactBytes`. A plain skill declares no `extends:`, so it is never the child whose `ArtifactBytes` the executable-field comparison parses as authored. Every `Walk` caller receives the document: filesystem-source sync (`pkg/sync/sync.go:579`), `podium lint` (`cmd/podium/main.go:1083`), and the workspace overlay (`pkg/overlay/overlay.go:61`).

**`manifestBodyOf`.** Return `""` when `manifest.IsSynthesizedSkillDocument(rec.ArtifactBytes)` holds, so overlay BM25 does not index the pointer comment's words.

Annotate with `// Spec: §13.11.3, §4.3.4, §4.6`.

Tests (in `pkg/registry/filesystem`, unit and integration):

- A plain skill with no version walks with `Version` `0.0.0-auto.0`, `VersionAssigned` true, nil `AuthoredBytes`, and `ArtifactBytes` equal to the D-6 bytes, with `ResolveExtends` both true and false.
- A both-files skill whose files declare no version walks with `Version` `0.0.0-auto.0` and its authored `ArtifactBytes` unchanged.
- A plain skill with no version is walked, walked again after its `SKILL.md` changes, and walked a third time after the first bytes return. Each walk returns the record with `Version` `0.0.0-auto.0` and the content hash of that walk's bytes, and no error (`// Spec: §13.11.3, §7.3.1`). The filesystem source keeps no history, so the §7.3.1 duplicate-content rejection never applies to it.
- An `extends:` child with an `ARTIFACT.md` overlay receives its plain-skill parent's `SKILL.md` description in its merged document, in one case whose parent declares no version and one whose parent declares `metadata.version: "1.0.0"`. TEST-3 pins the cross-mode byte equality of that document.
- `pkg/registry/filesystem/extends_executable_test.go` `TestWalk_ExecutableInheritancePlainSkillParentSetsNoField` (`// Spec: §4.6, §13.11.3`). Layer `L1` holds `ops/base/SKILL.md` alone, whose frontmatter carries `allowed-tools` and a `metadata` mapping. Layer `L2` holds `ops/child` with a `SKILL.md` and an `ARTIFACT.md` declaring `type: skill`, `version: 1.0.0`, and `extends: ops/base`. A `Walk` with `CollisionPolicyDrop`, an `OnDrop` that records drops, and `ResolveExtends` reports no drop. It returns `ops/child` with merged `ArtifactBytes`, and `ops/base` with `ArtifactBytes` equal to `manifest.SynthesizedSkillDocument("0.0.0-auto.0")`. The first test in this list, which runs with `ResolveExtends` true and false, pins that both `Walk` paths synthesize.
- `cmd/podium-mcp/helpers_extra_test.go` `TestManifestBodyOf_StripsFrontmatter` (`// Spec: §4.3.4`) gains these rows: a record whose `ArtifactBytes` is `SynthesizedSkillDocument("0.0.0-auto.0")` maps to `""`, and an authored `ARTIFACT.md` whose body is the same pointer comment under different frontmatter keeps that body.

### CODE-5. Ingest: assign the registry version

Targets: `pkg/registry/ingest/autoversion.go` (new) and `pkg/registry/ingest/ingest.go` (the ingest loop around lines 560-830).

- Replace the `ListManifests` call at `ingest.go:574` with one `ListManifestsIncludingDeleted` call. From that call, build `crossLayerByID` from the non-deleted rows of other layers, as today, and `rowsByID`, every row of each artifact ID with soft-deleted rows included. A listing error aborts the ingest call with `fmt.Errorf("ingest: list manifests: %w", err)`; today the cross-layer scan ignores the error.
- `func assignVersion(rows []store.ManifestRecord, layerID, hash string) (v string, duplicate bool)` in `autoversion.go` implements the D-4 steps over one ID's rows. It takes the layer's own row as the row whose version `version.ResolveLatest` returns over the rows with `Layer == layerID` and `DeletedAt == nil`. It returns that version when the row's `ContentHash` equals `hash`. It reports `duplicate` when any row carries `hash`. Otherwise it returns `manifest.AutoVersion(maxN+1)`, or `manifest.AutoVersion(0)` when no row satisfies `manifest.IsAutoVersion`. It reads no store and no clock. Its comment states why soft-deleted rows join the duplicate set and never supply the reused version, and why the reused version is always registry-assigned.
- On `duplicate`, append `RejectedArtifact{ArtifactID: rec.ID, Reason: autoVersionDuplicateReason, Code: layer.InvalidArtifactCode, Details: map[string]any{"constraint": autoVersionDuplicateConstraint}}`, log `ingest: tenant=%s layer=%s artifact=%s rejected: auto_version_duplicate_content` with identifiers only, and continue. The unexported constants are `autoVersionDuplicateConstraint = "auto_version_duplicate_content"` and `autoVersionDuplicateReason = "content matches an earlier stored version of this skill, whose version the registry assigns; set metadata.version in SKILL.md or change the content"`. Each carries `// Spec: §7.3.1`.
- No index update follows a commit. Each canonical ID occurs once per call, because `walkLayer` derives it from one package directory (`ingest.go:1021-1053`).
- For a record with `VersionAssigned`, wrap assignment, the `GetManifest` pre-check, and `commitManifest` in one loop of at most 3 attempts with no sleep, which starts after the §4.6 collision check. When the pre-check finds a row with a different hash at the assigned version, or `commitManifest` returns `store.ErrImmutableViolation`, re-read the listing, replace `rowsByID[rec.ID]` with that ID's rows from it, run `assignVersion` again, and retry, so a duplicate that only the re-read listing reveals is rejected rather than committed. After the last attempt, append today's `ConflictReport`. The attempt count is a named unexported constant whose comment states why it is not operator-tunable (D-4). Signing (`Signer(ctx, mr.ContentHash)`) and `persistResources` do not depend on the version, so they run once. The §4.6 executable-field check that proposal 0057 placed between the pre-check and signing (`ingest.go:802-817`) runs at most once per record as well. It runs on the first attempt whose pre-check returns `store.ErrNotFound`, before signing, and never on an attempt whose pre-check classifies the record as unchanged. A retry does not repeat it, because its inputs (`rec.ArtifactBytes`, `mr.ExtendsPin`, and the pinned parent's stored chain) do not include the version. A rejection from it appends its `RejectedArtifact` and ends the record's attempts, with no row committed and no `ConflictReport`.
- `manifestRecordFor` is unchanged. `Frontmatter: rec.ArtifactBytes` already stores empty bytes for a plain skill, because CODE-3 leaves `ArtifactBytes` nil.
- `resolveExtendsPin` (`ingest.go:1575`) and its call (`ingest.go:677`) are unchanged. The call passes `rec.Artifact.Version`, which is empty for an assigned record, as the watch-out bullet on same-ID overlays explains. `TestIngest_ExecutableInheritanceSameLayerOverlayPinAccepted` (`pkg/registry/ingest/executable_inheritance_test.go:533`) and `TestIngest_SameVersionOverlayIsConflictNotSelfCycle` (`pkg/registry/ingest/extends_conformance_test.go:381`) pass unchanged.
- Annotate with `// Spec: §7.3.1, §4.7.6`.

### CODE-6. Core: serve the synthesized document

Targets: `pkg/registry/core/served_frontmatter.go` (new), `pkg/registry/core/core.go` (`assembleResult` at line 1762, `mergeChain` at line 1908, and `descriptorOf` at line 2256), and `pkg/registry/core/admit.go` (`admitBytes` at line 61).

- `func plainSkillRow(rec store.ManifestRecord) bool` reports `len(rec.Frontmatter) == 0` and `manifest.ParseSkill(rec.SkillRaw)` returning no error. It is the one predicate for "a stored row of a skill with no `ARTIFACT.md`" in `pkg/registry/core`. It reads only hashed bytes, never the `Type` column, so a metadata-store writer cannot make a row satisfy it or fail it without changing the bytes admission recomputes the content hash over. Ingest writes a row that satisfies it exactly for a plain skill, because CODE-3 leaves `ArtifactBytes` nil only for a package whose `SKILL.md` passed `ParseSkill`. Annotate with `// Spec: §13.4, §4.3.4`.
- `admitBytes`: after the content-hash and resource checks, when `plainSkillRow(rec)` holds, set `a = &manifest.Artifact{Type: manifest.TypeSkill, Description: s.Description}`, where `s` is the `manifest.ParseSkill(rec.SkillRaw)` result, in place of the nil a failed `ParseArtifact` yields. That is the value the filesystem loader builds for a plain skill (CODE-3, CODE-4). `withAdmittedFields` (`admit.go:223-230`) then takes its existing non-nil path: the body is `admittedBody(a, rec.SkillRaw)`, the admitted `SKILL.md` body, and `setManifestFields` sets type `skill`, empty sensitivity, `Deprecated` false, no replacement, and no `audit_redact` keys. `withAdmittedFields` also gains one branch that runs when `plainSkillRow(rec)` holds: it sets `rec.Description` to `a.Description` and `rec.Tags` to nil. All of these are the values ingest stores for such a row (`pkg/registry/ingest/ingest.go:1254-1289`). The branch stays out of `setManifestFields`, which `mergeChain` also calls on every merged record. `checkExtendsPin` sees no `extends:` and requires an empty pin, as it does for a nil manifest. A row whose manifest is empty and whose `SKILL.md` does not parse keeps today's stored-value path. `parsedArtifact` is unchanged: on an admitted plain-skill row its column fallback (`core.go:1993-2006`) reads the admitted type, description, tags, sensitivity, deprecation, replacement, and `audit_redact` values, and its `Version` is the row key, which §13.4 serves. `mergeChain` folds the admitted chain through `ResolvedArtifact` (`core.go:1912`), which calls `parsedArtifact` on every row (`core.go:1879-1881`), and `manifest.MergeExtends` keeps a parent's description when the child sets none and unions the parent's tags (`pkg/manifest/merge.go:33-34`, `:46`), so this branch is what keeps an edited `Description` or `Tags` column of a plain-skill parent out of its child's served merged document. Annotate with `// Spec: §13.4`.
- `func servedFrontmatter(rec store.ManifestRecord) ([]byte, error)` returns `rec.Frontmatter, nil` unless `plainSkillRow(rec)` holds. In that case it returns `manifest.SynthesizedSkillDocument(rec.Version)`, with an error wrapped as `fmt.Errorf("serve %s@%s: synthesized ARTIFACT.md: %w", rec.ArtifactID, rec.Version, err)`.
- `assembleResult` calls it on the admitted record after `mergeChain` and before `resultFromRecord`, places the result in the record's `Frontmatter`, and fails the read on error, as it does when `mergeChain` fails. `resultFromRecord` is unchanged. A merged chain is never affected, because a merged document is non-empty.
- `mergeChain`: after `ResolvedArtifact` returns (`core.go:1912`) and before `setManifestFields` (`core.go:1953`) and `manifest.SerializeMerged` (`core.go:1961`), when the merged `Version` is empty, set it to `out.Version`, the requested child row's version. Make the change in `mergeChain` and leave `ResolvedArtifact` unchanged, because ingest's §4.6 executable-field check calls `ResolvedArtifact` on raw stored rows (`pkg/registry/ingest/ingest.go:1541`) and compares no version. `manifest.MergeExtends` takes the child's parsed `version` verbatim (`pkg/manifest/merge.go:71`), and `Artifact.Version` carries no `omitempty` (`pkg/manifest/types.go:102`), so a child whose `ARTIFACT.md` overlay omits `version` would otherwise be served a merged document with `version: ""`, while the filesystem resolver merges the loader-resolved version (CODE-3, CODE-4) and serializes it (`pkg/registry/filesystem/extends.go:281`). The child row's version is the version §13.4 says a load returns, and §4.6 makes it the child's own. Annotate with `// Spec: §4.6, §4.3.4`.
- `descriptorOf` sets `Frontmatter` to `frontmatterBlock` of `servedFrontmatter(m)`, so a plain skill's descriptor carries `---\ntype: skill\nversion: <version>\n---\n` and no body, as every descriptor carries its document's frontmatter block (`core.go:2268`, `:2277-2283`). On a synthesis error it logs `log.Printf("search: descriptor of %s: %v", m.ArtifactID, err)` and emits the empty block, following the convention of `descriptorBlockHidingChain` (`core.go:1364`). Search is served without admission (§13.4), and `plainSkillRow` reads no unhashed column, so the descriptor applies the same predicate as a load. The reason is §7.6.1 consistency: a plain skill's descriptor carries the same metadata as a both-files skill's.
- Server-source sync's `AuthoredBytes` becomes the synthesized document. The lock uses the registry's `ContentHash` (`pkg/sync/sync.go:438-440`), and CODE-12 takes the lock version from the served `version` member.

Tests (integration, in `pkg/registry/core` or `test/integration`, `// Spec: §4.7.10, §4.3.4`):

- The single and batch responses for a plain-skill row carry the D-6 bytes in `frontmatter`, and their delivery records verify. The search descriptor for the same row carries exactly `---\ntype: skill\nversion: <version>\n---\n`.
- A plain-skill row whose stored `Version` fails synthesis makes `load_artifact` return an error, and its search descriptor carries an empty `frontmatter`.
- An `extends:` child row whose stored overlay omits `version`, at version `2.0.0`, loads with a merged `frontmatter` that carries `version: 2.0.0`.

Admission test (`pkg/registry/core/admit_test.go`, `// Spec: §13.4`): store a plain-skill row, then overwrite its `Body`, `Type`, `Description`, `Tags`, `Sensitivity`, `Deprecated`, `ReplacedBy`, and `AuditRedact` columns with other values while leaving `Frontmatter`, `SkillRaw`, resources, and the content hash unchanged. A load serves type `skill`, the `SKILL.md` body, and the values ingest stores for those fields, and serves the D-6 bytes in `frontmatter`. A second case stores that tampered row as the parent of an `extends:` child row whose `ARTIFACT.md` overlay sets no `description` and no `tags`, and asserts that the child's served merged `frontmatter` carries the parent's `SKILL.md` description and none of the injected tags. A third case stores a row whose manifest is empty and whose `SKILL.md` has no frontmatter, and asserts it keeps the stored-value path.

### CODE-8. Adapters: `none` omits the synthesized document

- `pkg/adapter/none.go`: write `ARTIFACT.md` only when `len(src.ArtifactBytes) > 0 && !manifest.IsSynthesizedSkillDocument(src.ArtifactBytes)`, with `// Spec: §6.7, §4.3.4`.
- `pkg/adapter/adapter.go:111` comment: "ArtifactBytes is the verbatim bytes of ARTIFACT.md, or the §4.3.4 synthesized document for a skill that has none."
- Every other adapter and emitter dispatches on `frontmatterType`, which reads `type: skill` from the synthesized document, and `deriveSkillCompatibility` finds nothing to derive in it. The Claude marketplace emitter's `ArtifactBytes` fallback (`emitter.go:177-182`) is never reached for a plain skill, because `SkillBytes` is always present.
- Unit test in `pkg/adapter`: `none` writes `SKILL.md` and resources and no `ARTIFACT.md` for the synthesized bytes, writes `ARTIFACT.md` for an authored document, and writes it for a document that differs from the synthesized one by one byte.

### CODE-9. SDKs: overlay discovery and canonical-layout writers

Targets: `sdks/podium-ts/src/overlay.ts` (`load` and `readPackage`), `sdks/podium-ts/src/index.ts` (`materializeCanonical`, near line 477), `sdks/podium-py/podium/_overlay.py` (`load` and `_read_package`), and `sdks/podium-py/podium/client.py` (`_canonical_files`, near line 392).

Overlay walk:

- A directory below the overlay root is a package when it directly contains a regular file named `ARTIFACT.md`, or when it directly contains a regular file named `SKILL.md` and no directory between it and the overlay root is a package. A both-files directory loads once, from `ARTIFACT.md`, as today.
- The nested-boundary checks (`overlay.ts:402`, `_has_artifact_manifest`) keep testing `ARTIFACT.md` alone. Their comments name `filesystem.PackageManifests` in `pkg/registry/filesystem/discovery.go` as the Go function they mirror.
- For a package with `SKILL.md` and no `ARTIFACT.md`, `readPackage` and `_read_package` set `type` to `skill`; set `version` to `SKILL.md` `metadata.version` when it is `major.minor.patch`, and otherwise to `0.0.0-auto.0`; take `description` and the search tokens from the `SKILL.md` frontmatter; take `body` (served as `manifest_body`) from the `SKILL.md` body, as the registry serves a skill (`pkg/registry/ingest/ingest.go:1254-1268`); set `skillRaw` to the `SKILL.md` bytes; and set `frontmatter` to the D-6 bytes. They never derive the description or the body by parsing the synthesized document.
- For a both-files package whose `ARTIFACT.md` declares no `version`, the version is a `major.minor.patch` `metadata.version`, and otherwise `0.0.0-auto.0`.
- Each SDK's hand-written frontmatter parser gains a narrow read of the nested `metadata:` → `version:` key, quoted or unquoted.

`materialize()`: skip the `ARTIFACT.md` entry when `frontmatter` byte-equals `synthesizedSkillDocument(<its own version: line>)`. Everything else is unchanged.

Each SDK gains one `synthesizedSkillDocument(version)` and one `isSynthesizedSkillDocument(doc)` helper, citing §4.3.4 and the Go functions they mirror (`manifest.SynthesizedSkillDocument`, `manifest.IsSynthesizedSkillDocument`). The overlays compute no content hash, so nothing changes there.

### CODE-10. `podium artifact scaffold --type skill` writes `SKILL.md` alone

Target: `cmd/podium/artifact_scaffold.go` (`renderAndWrite` near lines 287-310, `renderSkillArtifactMD` near 336-353, `renderSkillMD` near 355-370, and flag handling in `artifactScaffoldWithIO`).

- For `--type skill`, write `SKILL.md` alone, with `metadata:` holding `version: "<--version>"`. Delete `renderSkillArtifactMD`.
- Collect every explicitly passed Podium-only flag (`tags`, `when-to-use`, `extends`, and `sensitivity`) with `fs.Visit`, and report all of them in one message, then return 2: `error: --tags, --extends set Podium-only fields; write them in a hand-written ARTIFACT.md overlay (see docs/authoring/frontmatter-reference.md)`. The list names the flags passed in flag-definition order (`tags`, `sensitivity`, `when-to-use`, `extends`; `cmd/podium/artifact_scaffold.go:80-85`). `fs.Visit` visits flags in lexicographical order, so the command records the set `fs.Visit` reports and emits the names by walking a fixed list in that definition order.
- The refusal runs immediately after the existing `validateSensitivity` check (`cmd/podium/artifact_scaffold.go:147-150`) and before the existing-directory check, so an invalid `--sensitivity` value still exits 2 with `sensitivity "<v>" must be one of low|medium|high`, and a valid one passed with `--type skill` exits 2 with the overlay message. Neither path creates the target directory.
- Append "not accepted with --type skill" to the help strings of those four flags.
- Update the success message so it names `SKILL.md` alone.
- Annotate with `// Spec: §4.3.4`.
- Unit tests in `cmd/podium/artifact_scaffold_test.go`, edited in the same commit, each with `// Spec: §4.3.4`:
  - `TestScaffold_Skill_LintClean` drops `--tags` and asserts `SKILL.md` alone, carrying `version: "0.1.0"` under `metadata:`, with no `ARTIFACT.md`. Its `ARTIFACT.md` field assertions move to `SKILL.md`. A new case passes `--tags` with `--type skill` and asserts exit 2, a stderr naming `--tags` and the `ARTIFACT.md` overlay, and no target directory; another passes `--extends`, `--when-to-use`, `--sensitivity high`, and `--tags`, in that order on the command line, and asserts one message naming `--tags, --sensitivity, --when-to-use, --extends`, the definition order.
  - `TestScaffold_DomainHierarchyInPath` asserts `SKILL.md` exists and `ARTIFACT.md` does not.
  - `TestScaffold_ExtendsOnSkill` asserts exit 2 with the overlay message for `--extends` with `--type skill`. `TestScaffold_ExtendsOnContext` keeps the non-skill success case.
  - A new case passes `--type skill --sensitivity ultra` and asserts exit 2 with the `low|medium|high` hint, which pins the check order.
  - `TestScaffold_Skill_SkillMDWriteFailsCleanly` keeps its assertion, and its comment no longer says the `ARTIFACT.md` write succeeds first.

### CODE-11. `podium import` becomes a skills normalizer

Target: `cmd/podium/import.go`.

- Remove `--type`. Update the package doc comment to describe the normalizer.
- Validate `--version` with `manifest.ValidateVersion` before planning, and exit 2 with a message on failure. This also rejects the reserved form.
- Rewrite body selection in `collectSkillFiles`: the body is the root-level `SKILL.md`; only when no root `SKILL.md` exists is it the single root-level `.md` file. Every other file, including other `.md` files and any `SKILL.md` in a subdirectory, is a bundled resource copied verbatim. A nested `SKILL.md` stays a resource in the output (§4.4).
- Refuse, with exit 1 and the directory named, before any write and also under `--dry-run`: a source directory with a root `ARTIFACT.md`; a root with several `.md` files and no `SKILL.md`; a chosen file with no frontmatter block; and an existing `metadata.version` different from `--version`.
- `setSkillMetadataVersion` locates the closing `---` delimiter in the raw source, re-marshals only the header through a yaml.v3 encoder with `SetIndent(2)`, and appends the raw bytes after the delimiter unchanged. It returns an error naming the file when `metadata` is present but is not a mapping, or when `metadata.version` is not a string scalar. When `metadata.version` already equals `--version`, the file is copied byte for byte.
- Never write `ARTIFACT.md`.
- A subdirectory with no root-level `.md` file is skipped through the existing nil-body `continue` (`import.go:96-98`), as D-11 states.
- `cmd/podium/import_test.go`: drop `--type`, assert no `ARTIFACT.md` and a `metadata.version`, replace the `Spec: §3` anchor with `Spec: §4.3.4`, and add one unit case per refusal and one for a nested `SKILL.md`, which is copied as a resource while the root body is kept (`Spec: §4.3.4, §4.4`). Add these cases, each with `// Spec: §4.3.4, §4.7.6`:
  - `--version 1.0` exits 2, and `--version 0.0.0-auto.0` exits 2.
  - A source `SKILL.md` with `metadata: [a]` fails with exit 1 naming the file, and one with `metadata: {version: 1}` fails the same way.
  - A source `SKILL.md` whose `metadata.version` already equals `--version` is written byte-identical to the source.
  - A source `SKILL.md` without `metadata` whose body holds HTML comments and blank lines keeps every body byte after the closing delimiter.
  - A source `SKILL.md` whose header carries `license: MIT`, an `allowed-tools` list, and a `metadata` mapping holding `author: alice` and no `version` is written with `metadata.version` equal to `--version`, with `metadata.author` still `alice`, with `license` still `MIT`, with the same `allowed-tools` entries in the same order, and with every body byte after the closing delimiter unchanged. This is the branch where `setSkillMetadataVersion` inserts `version` into an existing mapping and re-marshals a header that holds other keys, which the Agent Skills standard's free-form `metadata` map (`pkg/manifest/types.go:223`) makes the common input. The no-`metadata` case creates a fresh mapping, and the equal-version case copies bytes, so neither reaches it.
  - A subdirectory with no root-level `.md` file and a `SKILL.md` one level down is skipped, and a source whose only subdirectory is such a directory exits 1 with `source directory contained no skills`.
- `cmd/podium/lint_import_more_test.go` `TestImportCmd_HappyPath`: move the fixture's `SKILL.md` from `src/skills/greet/SKILL.md` to `src/greet/SKILL.md`, because the import body is now the root-level file of each immediate subdirectory, and keep the exit-0 assertion (`// Spec: §4.3.4`).

### CODE-12. Consumers take a load's version from the served `version` member

A skill's `ARTIFACT.md` overlay may omit `version` when `SKILL.md` declares `metadata.version` (SPEC-4), and the registry serves that overlay as authored. Server-source sync and `podium-mcp` read the version out of the served document today rather than out of the response's `version` member, and each worked only because lint required `version` in every `ARTIFACT.md`:

- Server-source sync parses `rec.Frontmatter` into `out.Artifact` (`pkg/sync/server.go:211-219`), and the lock records `rec.Artifact.Version` (`pkg/sync/sync.go:316-323`). For such a skill it records `""`, while the filesystem source records the resolved version, and `assertLockArtifactsEqual` (`test/integration/sync_equivalence_test.go:547-569`) fails.
- `podium-mcp` recovers `resp.Version` and `resp.Type` from the cached served document (`cmd/podium-mcp/resolution_cache.go:462-470`) and re-runs the §4.7.10 check over `deliveryRecordOf(resp)`, which frames `resp.Version` (`cmd/podium-mcp/main.go:2058`, `:2089-2091`). An empty version fails with `materialize.content_hash_mismatch`, which is not a signature error, so `cachedOrRefetch` returns the error instead of refetching (`main.go:1574-1585`). The second load after a 304 or a HEAD match in the default `always-revalidate` mode, and every `offline-first` and `offline-only` cache hit, then fail.

Changes:

- `pkg/sync/server.go` `recordFromServed`: when `manifest.ParseArtifact` succeeds, set `out.Artifact.Version = rec.Version`. `rec` is the delivery record the §4.7.10 check verified, and its `Version` is the served `version` member, which §13.4 makes the stored row's version. For every authored document that declares `version`, the two values are already equal, because lint requires the `ARTIFACT.md` version to be the artifact's version, and a merged document carries the child's version (CODE-6). A parse failure leaves `Artifact` nil as today. Annotate with `// Spec: §7.5, §13.4`.
- `cmd/podium-mcp` persists the served version and type with each cached delivery record:
  - `deliveryFiles` (`main.go:2876`) gains `Version` and `Type` string fields, documented as the served `version` and `type` members that the delivery record frames.
  - `cacheVerifiedRecord` (`main.go:1821-1836`), the only caller of `putDelivery`, sets them from `resp.Version` and `resp.Type`. It runs only after verification, so it persists verified values.
  - `putDelivery` (`main.go:2892`) writes them as the `version` and `type` files beside the existing per-ID files, written even when empty, as its other files are.
  - `readDeliveryFiles` (`resolution_cache.go:501`), the only reader, adds `version` and `type` to its required files. A record cached by an earlier release lacks them and is a cache miss, which `always-revalidate` and `offline-first` answer with a full fetch and `offline-only` answers with its cache-miss error, as for a record cached before the `artifact_revision` file existed.
  - `loadArtifactFromCache` (`resolution_cache.go:440`) sets `resp.Version` and `resp.Type` from those files and no longer parses them out of the cached document. A tampered `version` or `type` file still fails the delivery check, because the record frames both.
  - Annotate with `// Spec: §6.5, §6.6, §4.7.10`.
- No HTTP member, spec text, or other consumer changes. The SDKs and the HTTP read path already take the version from the response's `version` member.

Tests:

- `cmd/podium-mcp` (`// Spec: §6.5, §6.6, §4.7.10`): against a registry that serves a both-files skill whose `ARTIFACT.md` overlay omits `version` and whose `SKILL.md` declares `metadata.version: "1.0.0"`, a second `load_artifact` in `always-revalidate` mode answered by a 304, a second one answered by a matching HEAD, and an `offline-only` load after a first full load each succeed and report version `1.0.0`. A bucket whose per-ID directory lacks the `version` file is a miss: `always-revalidate` refetches and succeeds, and `offline-only` reports the cache-miss error.
- `cmd/podium-mcp/cache_mode_test.go` (`// Spec: §6.5, §6.6, §4.7.10`), following `TestOfflineOnly_RewrittenRevisionFails` and the tamper arm of `TestRevalidatedLatest_RefreshesOnlyAfterVerification`: prime the cache with a verified load of a record at `1.0.0`, then rewrite the per-ID `version` file to `9.9.9`, and in a second case rewrite the per-ID `type` file to another type. A cache-served load in `offline-only` and a `latest` load in `always-revalidate` answered by a matching HEAD are each refused with `materialize.content_hash_mismatch`, and no response reports `9.9.9`.
- The existing tests that call `putDelivery` (`cmd/podium-mcp/cache_mode_test.go:282`, `cmd/podium-mcp/resolution_cache_test.go:142`) set `Version` and `Type`, so each keeps failing or passing for the condition it names.
- Server-source sync: TEST-3's `plain-skills/overlay-unversioned` fixture skill, compared by `assertLockArtifactsEqual`, records `1.0.0` in both locks.

## Edge cases and accepted failure modes

| Case | Observable outcome | Governing spec text; docs page |
|:--|:--|:--|
| First ingest of a skill with no usable version | Stored and served as `0.0.0-auto.0` | §7.3.1 "Skill with no declared version" row (SPEC-8); `docs/authoring/frontmatter-reference.md` (DOC-2) |
| Re-ingest of unchanged bytes | No-op at the existing assigned version | §7.3.1 row (SPEC-8); `docs/authoring/frontmatter-reference.md` |
| Changed bytes | Next N, no `ingest.immutable_violation` | §7.3.1 row (SPEC-8); `docs/authoring/frontmatter-reference.md` |
| Revert to earlier bytes, including across a declared-version interval | Rejected as `ingest.invalid_artifact` with `details.constraint: "auto_version_duplicate_content"`; `latest` keeps serving the newer row until the author sets `metadata.version` or changes the content | §7.3.1 "Skill with no declared version" row and "A revert to earlier bytes ... is rejected" (SPEC-8); `docs/authoring/frontmatter-reference.md` and `docs/reference/error-codes.md` (DOC-2) |
| A soft-deleted row holds the highest N | The next N skips it | §7.3.1 row "any stored or soft-deleted row" (SPEC-8) |
| A purge removes the row holding the highest N, or every row carrying rejected bytes | That N can be assigned again to different bytes, and the rejected bytes are accepted at the N the derivation then yields (D-15). An MCP server cache that held the purged row's record for those bytes reports the purged version, and for a plain skill the synthesized document naming it, on every cache-served load until the cache no longer holds the record; the body and resources match the registry's (D-4) | §7.3.1 "A purge (§8.4) ..." and §6.5 "Content cache" (SPEC-8); `docs/authoring/frontmatter-reference.md` (DOC-2) |
| A layer unregistered and registered again under its ID, then reingested unchanged | Each version-less skill is rejected with `auto_version_duplicate_content` until the retention purge removes the tombstoned rows or the author sets `metadata.version`; `podium layer restore` within the recovery window serves the earlier rows instead | §7.3.1 (SPEC-8), §8.4; `docs/authoring/frontmatter-reference.md` (DOC-2) |
| A second layer's version-less package byte-identical to a stored row of the ID, admitted by the §4.6 check | Rejected with `auto_version_duplicate_content` | §7.3.1 (SPEC-8); `docs/reference/error-codes.md` (DOC-2) |
| Two concurrent ingests of different bytes | Distinct N, no conflict. Under sustained contention one ingest reports `ingest.immutable_violation` for that artifact and a later ingest assigns it | §7.3.1 "Two ingests never commit one registry-assigned version with different bytes" (SPEC-8) |
| `ARTIFACT.md` removed while `metadata.version` keeps the version | `ingest.immutable_violation`; `latest` keeps serving the stored row | §7.3.1 "Removing `ARTIFACT.md` ..." (SPEC-8); `docs/authoring/frontmatter-reference.md` (DOC-2), CHANGELOG (DOC-5) |
| `metadata.version: "1.0"` with no `ARTIFACT.md` version | Ignored; `lint.skill_version_assigned` warning; assigned version | §4.3.4 "Version." and lint list (SPEC-4); `docs/authoring/frontmatter-reference.md` (DOC-2) |
| `metadata.version: "1.0"` with `ARTIFACT.md` `version: 1.0.0` | `1.0.0`; no warning, no mismatch | §4.3.4 "Version." (SPEC-4); `docs/reference/frontmatter-schema.md` (DOC-2) |
| `ARTIFACT.md` `version` and a `major.minor.patch` `metadata.version` differ | `lint.skill_artifact_field_mismatch`, ingest refused as `ingest.lint_failed`. Breaking for an existing skill | §4.3.4 lint list (SPEC-4); CHANGELOG (DOC-5) |
| Authored `0.0.0-auto.<N>` in either file | `lint.invalid_version`, ingest refused as `ingest.lint_failed` | §4.7.6 "an authored version in that form fails lint" (SPEC-6), §4.3.4 lint list (SPEC-4); `docs/authoring/frontmatter-reference.md` |
| A range pin such as `<id>@0.0.x` | Never selects an assigned version | §4.7.6 "A range never selects a registry-assigned version" (SPEC-6); `docs/authoring/frontmatter-reference.md` |
| Filesystem source after the registry ingested changed bytes, or an ID that several layers contribute | Lock `version` entries differ between the sources. The `none` output differs in the `version:` line of an `extends:` child's merged `ARTIFACT.md` when that child's version is assigned. While the registry serves the directory's current bytes as its latest row, the content hash agrees, and every other adapter output agrees when the `extends:` reference resolves to the same parent in both sources. After a revert they differ (the next row). A version-less same-ID overlay whose bytes changed can resolve a different parent in each source: a server pins the most recently ingested row of the ID, which is the overlay's own previous row when no other layer has ingested one since, and the filesystem source resolves the next-lower-precedence record. Its merged output can then also differ in tags, sensitivity, and executable fields, or the filesystem source drops it (the version-less same-ID overlay row below) | §13.11.3 "Skills without a declared version" (SPEC-9), §13.11.3 *Executable inheritance*; `docs/deployment/local.md` "Skills that declare no version", with pointers from `single-node.md`, `deployment/index.md`, and `README.md` (DOC-3) |
| Filesystem source after a revert | Reports `0.0.0-auto.0` with the reverted bytes, with no rejection. The registry keeps serving its newer row, so the content hash, the body, and every adapter output differ between the sources until the author sets `metadata.version` or changes the content | §13.11.3 (SPEC-9); `docs/deployment/local.md` "Skills that declare no version" (DOC-3) |
| A `SKILL.md` in a subdirectory of a package | A bundled resource of that package, also under `podium import` | §4.2 (SPEC-2), §4.4 (SPEC-5); `docs/authoring/bundled-resources.md` (DOC-2) |
| A version-less same-ID `extends:` overlay whose bytes change | On a server, assigned the next N and pinned to the most recently ingested non-deprecated row of the ID, as an authored version bump is. When no other layer has ingested a row of the ID since the overlay's previous row, that row is the pin, and the §4.6 executable-field rule does not test that same-layer edge. When a lower layer's row is newer, the pin crosses layers and the rule applies. A filesystem-source sync tests the overlay against the next-lower-precedence record, a cross-layer edge, under §4.6. It merges the overlay over that record when the overlay restates every executable field the record sets, and otherwise drops the overlay with `ingest.invalid_artifact`, serves the lower record, and exits 1 | §4.6 *Executable fields across layers*, §13.11.3 *Executable inheritance*; no docs page |
| A cross-layer `extends:` child of a plain skill | Accepted without restating any field, because a plain skill's stored manifest is empty and sets no executable field (§4.6). A filesystem-source sync keeps the child for the same reason | §4.6 *Executable fields across layers*, §13.4 (SPEC-13); no docs page |
| A frontmatter-less `SKILL.md` in a directory outside any package | The layer walk fails: `podium sync` and `podium lint` error, and `podium layer reingest` fails with `ingest.lint_failed` (HTTP 422), because the walk error wraps `ErrInvalidArtifact` | §4.2 discovery rule (SPEC-2); `docs/deployment/layers.md` (DOC-3), CHANGELOG (DOC-5) |
| A `SKILL.md` at the layer root | Discovery error (it was silently ignored). `podium sync` and `podium lint` error, and `podium layer reingest` fails with `ingest.lint_failed` (HTTP 422) rather than a retryable `registry.unavailable` | §4.2 "A manifest file directly at the layer root is an error" (SPEC-2); `docs/deployment/layers.md` (DOC-3) |
| An `ARTIFACT.md`, or a package's sole `SKILL.md`, that is a symlink leaving a `local` layer's directory or naming an absolute target | The ingest of that layer fails with `ingest.source_unreachable` (HTTP 502 on reingest) and accepts no artifact from the cycle, as an escaping `SKILL.md` companion does today | §7.3.1 "Local-source ingest confinement"; no docs page |
| Skills under `.claude/skills/` with the layer at the repository root | Not discovered; the layer root must name the directory | §4.2 (SPEC-2); `docs/deployment/layers.md` (DOC-3) |
| A repository whose root is a single skill, as a git layer | Reingest fails with `ingest.lint_failed`, because no `root` names a directory above the repository. A copy of the skill without its `.git/` directory registers as a `local` layer | §4.2 (SPEC-2); `docs/deployment/layers.md` (DOC-3) |
| A dot-prefixed subdirectory of a package that carries its own `ARTIFACT.md` | Its files ship nowhere. Unchanged | §4.4; `docs/authoring/bundled-resources.md` |
| An authored `ARTIFACT.md` byte-identical to the synthesized document | `none` and SDK `materialize()` omit it, and the output reads back as a plain skill | §4.3.4 "Synthesized `ARTIFACT.md`" (SPEC-4), §6.7 (SPEC-10); `docs/consuming/configure-your-harness.md` (DOC-2) |
| A stored plain-skill row whose version cannot be synthesized | `load_artifact` fails rather than serving an empty `frontmatter`; the search descriptor carries an empty block. Ingest cannot produce such a row | §4.3.4 "carries exactly these bytes" (SPEC-4); no docs page |
| `podium artifact scaffold --type skill` with a Podium-only flag | Exit 2, naming each flag passed | §4.3.4 "no Podium tool generates one for a skill" (SPEC-4); `docs/reference/cli.md` (DOC-4) |
| A both-files skill whose `ARTIFACT.md` overlay omits `version` and whose `SKILL.md` declares `metadata.version` | Served at the `metadata.version` value with the overlay as authored. Server-source sync records that version in the lock, and `podium-mcp` cache-served loads report it | §4.3.4 "Version." (SPEC-4), §13.4 "The version a load returns is the stored row's"; `docs/authoring/frontmatter-reference.md` (DOC-2) |
| A plain-skill row whose stored body, type, description, tags, sensitivity, deprecation, replacement, or `audit_redact` columns were changed in the metadata store | The load serves the values the admitted `SKILL.md` bytes give, and an `extends:` child's merged `ARTIFACT.md` carries none of the changed description or tags | §13.4 (SPEC-13); no docs page |
| `podium import` over a subdirectory with no root-level `.md` file | The subdirectory is skipped; exit 1 with `source directory contained no skills` when no subdirectory yields a skill | §4.3.4 (SPEC-4); `docs/reference/cli.md` (DOC-4) |
| `podium import` with `--type`, a source `ARTIFACT.md`, a version mismatch, a frontmatter-less file, or several root `.md` files and no `SKILL.md` | Exit 2 for `--type`; exit 1 naming the directory for the rest, before any write | §4.3.4 (SPEC-4); `docs/reference/cli.md` (DOC-4) |
| A `root:` value with a trailing slash | Ingest of that layer fails with `ingest.source_unreachable`. Unchanged, a non-goal | §4.6; `docs/deployment/layers.md` (DOC-3 writes every value without the slash) |

## Testing

Every new behavior has a test at the highest level it reaches. The CODE sections above and the TEST sections below list each assertion.

- **§4.7.6 grammar and resolution** (unit): TEST-1A, `// Spec: §4.7.6`; the `TestCompare_Ordering` row carries `// Spec: §6.7`. Boundary cases: leading zeros, empty N, foreign cores, and `IngestedAt` ties.
- **§4.3.4 helpers** (unit): TEST-1B, `// Spec: §4.3.4, §4.7.6`. Error cases: invalid, newline-bearing, and reserved-with-leading-zero versions.
- **§4.2 and §4.4 discovery** (unit, cross-walker): TEST-1C, `// Spec: §4.2, §4.4, §4.7.6`. Error cases: a root-level manifest, a frontmatter-less `SKILL.md` outside a package, and a stat error from `PackageManifests`. Spec-named failure (§7.3.1 local-source confinement, `// Spec: §7.3.1, §6.10, §4.2`): an `ARTIFACT.md` or a sole `SKILL.md` that escapes the layer or has an absolute target fails the ingest with `source.ErrSourceUnreachable` under `source.ConfinedFS`.
- **§4.3.4 lint** (unit): TEST-1D, `// Spec: §4.3.4`. Spec-named failures: the mismatch and the reserved form.
- **§13.11.3 filesystem version and synthesis** (unit, integration): the CODE-4 tests, `// Spec: §13.11.3, §4.3.4, §4.6`.
- **§4.6 executable fields with a plain-skill parent and an assigned-version child** (unit, both modes): `TestIngest_ExecutableInheritancePlainSkillParentSetsNoField`, `TestWalk_ExecutableInheritancePlainSkillParentSetsNoField`, and TEST-2A (g), `// Spec: §4.6`. `TestIngest_ExecutableInheritanceSameLayerOverlayPinAccepted` passes unchanged.
- **§7.3.1 assignment** (unit, concurrent): TEST-2A, `// Spec: §7.3.1, §4.7.6`. Concurrency is driven deterministically through a store wrapper, and the error path covers a failed listing and an exhausted retry. Spec-named failure: the duplicate-content rejection, including one that only the re-read listing reveals.
- **§7.3.1, §4.7.10, and §13.4 end to end in process** (integration, across stores, `-race`): TEST-2B, including the duplicate-content rejection and its release by a purge, the two-layer assignment, the same-ID overlay's parent pin after a content change, the unregistered-layer and soft-deleted cross-layer cases, the removed-`ARTIFACT.md` conflict, the root-level `SKILL.md` reingest envelope, and the `ingest.source_unreachable` envelope for an escaping sole `SKILL.md`.
- **§4.7.10 served document** (integration): the CODE-6 tests and the TEST-2B delivery-attestation extension.
- **§13.4 plain-skill admission** (unit, security): the CODE-6 admission test, `// Spec: §13.4`. Spec-named failure: tampered columns of a plain-skill row are not served and do not reach an `extends:` child's merged document.
- **§6.5 and §7.5 served version** (unit, integration): the CODE-12 tests and the TEST-3 overlay without `version`. Error paths: a cached record from an earlier release is a miss, and a tampered `version` or `type` cache file is refused.
- **§6.7 `none` layout** (unit, materialization): the CODE-8 test and the TEST-3 golden case.
- **§11 and §2.2 equivalence** (integration): TEST-3.
- **§6.4 and §7.6 SDKs** (SDK suites): TEST-5.
- **CLI and binary paths** (unit, e2e): the CODE-10 and CODE-11 unit tests, and TEST-4A, TEST-4B, and TEST-4C.

Run `go test -coverpkg=./... -coverprofile=cover.out ./...` over the changed packages and confirm the new lines reach 85%. Measure subprocess coverage for the CLI changes with `GOCOVERDIR=$(mktemp -d) go test ./test/e2e/...`. Run `npm test` under `sdks/podium-ts` and `pytest` under `sdks/podium-py`. Run `make coverage-gate` after S1 and again at the end.

### TEST-1A. `pkg/version` unit tests

`pkg/version/version_test.go`, `// Spec: §4.7.6` unless noted:

- `ParsePin` accepts `0.0.0-auto.0` and `0.0.0-auto.12`, and rejects `0.0.0-auto.01`, `0.0.0-auto.`, `0.0.0-rc.1`, `1.2.3-auto.x`, and `1.2.3-auto.0`.
- `Resolve` returns the exact stored string for an auto pin, and `0.0.x` and `0.x` never select an auto candidate. An exact `0.0.0` pin does not select `0.0.0-auto.0`.
- `ResolveLatest` picks an auto candidate by `IngestedAt`, and on an `IngestedAt` tie ranks a non-auto version above an auto one and then orders by N.
- One row in `TestCompare_Ordering` (`// Spec: §6.7`): `{"0.0.0-auto.1", "0.0.0", 0}`, which proves `Compare` stays core-only after the shared `less` changes.

### TEST-1B. `pkg/manifest` unit tests

`pkg/manifest/skill_version_test.go`, `// Spec: §4.3.4` and `§4.7.6`:

- `SynthesizedSkillDocument("1.0.0")` and `SynthesizedSkillDocument("0.0.0-auto.3")` equal golden string literals of the D-6 bytes.
- An invalid version, a version containing a newline, `1.0`, and `0.0.0-auto.01` each return an error wrapping `ErrInvalidVersion`.
- `IsSynthesizedSkillDocument` is true for the synthesized bytes, false for a one-byte difference, false for a document that lacks the comment, false for a document whose version line is invalid, and false for empty input.
- `ResolveSkillVersion` has one row per precedence case: `ARTIFACT.md` only, `metadata.version` only, both equal, both different (returns the `ARTIFACT.md` value), a non-semver `metadata.version` alone (returns `""`), a reserved-form `metadata.version` alone (returns `""`), and nil inputs.
- `IsAutoVersion` rejects an N that overflows `int`.

### TEST-1C. Discovery predicate and cross-walker parity

- `pkg/registry/filesystem/discovery_test.go` (`// Spec: §4.2, §4.4, §7.3.1`): `PackageManifests` over an `fstest.MapFS` with `ARTIFACT.md` only, `SKILL.md` only, both, a directory named `SKILL.md`, and neither, each with a nil `err`. A stub `fs.StatFS` whose `Stat` returns a sentinel error for `x/SKILL.md` returns an `err` that `errors.Is` matches to the sentinel, with both booleans false.
- `pkg/registry/ingest/source_confinement_test.go` (`// Spec: §7.3.1, §6.10, §4.2`), each case driving `ingest.Ingest` with `Files: source.ConfinedFS(root)` over an on-disk tree, as `TestSourceIngest_EscapingSkillFileClassifies` does:
  - `x/ARTIFACT.md` is a symlink to a file outside the layer root. The call fails with an error that `errors.Is` matches to `source.ErrSourceUnreachable`, and the store holds no row for the layer.
  - `y/SKILL.md` is the package's only manifest and is a symlink to a file outside the layer root. The outcome is the same.
  - `z/SKILL.md` is the package's only manifest and is a symlink with an absolute target that names a regular file inside the layer root, which `os.Root` refuses (`pkg/layer/source/confined.go:19-21`). The outcome is the same.
  - `w/SKILL.md` is the package's only manifest and is a dangling relative symlink inside the layer. The ingest succeeds and stores no row for `w`, which pins the `fs.ErrNotExist` arm.
- `pkg/registry/ingest/walk_internal_test.go` (`// Spec: §4.2, §4.4, §4.7.6`): convert `TestLoadOne_ResourceSetMatchesTheFilesystemWalk` into a table over one on-disk tree per case. Each case asserts equal IDs, equal resource sets, and an equal digest computed from `AuthoredBytes` on both sides; the assertion at line 192 moves from `fsRec.ArtifactBytes` to `fsRec.AuthoredBytes`. The cases:
  - `SKILL.md` alone, `ARTIFACT.md` alone, and both files (one record).
  - A plain skill whose subdirectory holds a valid `SKILL.md` with frontmatter: one package, and the nested file is a resource.
  - An `ARTIFACT.md` package whose `references/SKILL.md` has no frontmatter: one package, the file is a resource, and nothing is parsed (the existing case).
  - `group/x/SKILL.md` where `group/` holds no manifest: one package `group/x`.
  - A plain skill containing a subdirectory with its own `ARTIFACT.md`: two packages, and the nested files are excluded from the parent.
  - `.claude/skills/x/SKILL.md`: skipped when the root is the repository, and found as `skills/x` when the root is `.claude`.
  - A root-level `SKILL.md`: both walkers return the canonical-ID error, whose text contains `subdirectory` and `SKILL.md`, and ingest's error wraps `ErrInvalidArtifact`.
  - `x/SKILL.md` as a symlink to a regular file elsewhere inside the layer: both walkers find package `x`. `y/SKILL.md` as a symlink to a directory: neither walker finds a package at `y`.
  - A frontmatter-less `SKILL.md` in a directory outside any package: both walkers fail, and ingest's error wraps `ErrInvalidArtifact`.
  - `type: agent` in `ARTIFACT.md` beside a root `SKILL.md`: `SKILL.md` stays a resource.
- One both-files case asserts that the digest equals `CanonicalContentHash` over the authored `ARTIFACT.md`, `SKILL.md`, and resources, and a plain-skill case asserts the digest frames a zero-length first value.
- Both walkers set `VersionAssigned` identically for each case.

The existing tests that pin a nested `SKILL.md` as a resource (`pkg/registry/filesystem/walk_test.go:713-761`, `pkg/registry/ingest/ingest_test.go:591-640`) stay unchanged and must pass.

### TEST-1D. Lint unit tests

`pkg/lint/skill_rules_test.go` and `pkg/lint/lint_test.go`, `// Spec: §4.3.4`, one row per D-9 rule:

- `ARTIFACT.md` `version: 1.0.0` and `metadata.version: "2.0.0"`: `lint.skill_artifact_field_mismatch` error.
- `ARTIFACT.md` `version: 1.0.0` and `metadata.version: "1.0"`: no mismatch error and no assigned-version warning.
- Plain skill with `metadata.version: "1.0"`: one `lint.skill_version_assigned` warning that names `"1.0"`, and no error.
- `metadata.version: "0.0.0-auto.1"`: `lint.invalid_version` error.
- `ARTIFACT.md` `version: 0.0.0-auto.1`: `lint.invalid_version` error.
- A record with `VersionAssigned` set and `Version` empty (the ingest state) and one with `Version` `0.0.0-auto.0` (the `podium lint` state) each emit the warning and neither `lint.required_field_missing` nor `lint.invalid_version`.
- A plain skill with the synthesized `ArtifactBytes` emits no `lint.skill_artifact_field`, `lint.skill_artifact_field_mismatch`, or `lint.skill_artifact_body`.
- A top-level `version` in `SKILL.md` reports `lint.skill_podium_only_field` with a message that names `metadata.version`.
- A record whose `ARTIFACT.md` declares `type: skill` with no `SKILL.md` keeps the existing `ruleSkillCompliance` error.

End-to-end test edited in the same commit, because CODE-7 changes its outcome: `test/e2e/skill_tutorial_test.go` `TestSkillTutorial_MissingVersion` (line 569) pairs an `ARTIFACT.md` of `type: skill` with no `version` with `greetSkillBody`, which has no `metadata` (`test/e2e/helpers_test.go:700`), so the record is `VersionAssigned`. The test now asserts exit 0, exactly one `lint.skill_version_assigned` warning, and no `lint.required_field_missing`, and its comment cites `// Spec: §4.3.4`. `TestFrontmatter_MissingVersion` (`test/e2e/frontmatter_schema_test.go:173`) keeps the `version is required` error for `type: context`.

### TEST-2A. Ingest auto-versioning unit and race tests

`pkg/registry/ingest/autoversion_test.go`, `// Spec: §7.3.1, §4.7.6`. Drive `ingest.Ingest` with `fstest.MapFS` files against a `store.Store` wrapper that intervenes deterministically:

- (a) The wrapper inserts a competing `0.0.0-auto.0` row with a different hash on the first `GetManifest`. The ingest re-derives N, commits `0.0.0-auto.1`, and reports no conflict.
- (b) The wrapper inserts the competing row on the first `PutManifest`. The outcome is the same.
- (c) The wrapper returns `store.ErrImmutableViolation` on every `PutManifest`. After 3 attempts the result carries the existing `ConflictReport`.
- (d) `ListManifestsIncludingDeleted` returns an error. The call fails with an error whose text begins `ingest: list manifests:`.
- (e) Unit tests of `assignVersion` over hand-built rows (`// Spec: §7.3.1`), one case each:
  - no rows gives `0.0.0-auto.0`;
  - the layer's latest row carries the incoming hash, so its version is reused and the result is not a duplicate;
  - the layer's latest row carries another hash and no row carries the incoming one, so the result is the maximum N plus one;
  - the layer's latest row is `1.0.0` with another hash and an older row of the layer at `0.0.0-auto.0` carries the incoming hash, so the result is a duplicate, which pins the comparison to the latest row rather than any row of the layer;
  - an `IngestedAt` tie between two rows of the layer resolves to the higher version;
  - a soft-deleted row of the layer at `0.0.0-auto.0` carries the incoming hash and the layer has no other row, so the result is a duplicate;
  - a soft-deleted row of another layer holds the maximum N with another hash, so the result is that N plus one;
  - a non-deleted row of another layer carries the incoming hash, so the result is a duplicate;
  - two layers of one ID, where `base` holds `0.0.0-auto.0` (H1) and `team` holds `0.0.0-auto.1` (H2): `(base, H1)` reuses `0.0.0-auto.0`, `(team, H2)` reuses `0.0.0-auto.1`, and `(base, H3)` returns `0.0.0-auto.2`.
- (f) On the first `GetManifest`, the wrapper inserts a `0.0.0-auto.0` row from layer `M` with a different hash and a `0.0.0-auto.1` row from layer `M` carrying the incoming hash. The re-derivation rejects the record with `details.constraint` `auto_version_duplicate_content` and commits no row for the ingesting layer.
- (g) `// Spec: §4.6, §7.3.1`. Layer `base` holds `x` as a both-files skill whose `ARTIFACT.md` declares `version: 1.0.0` and an `mcpServers` entry `jira` with `transport: stdio` and `command: org-jira`. Layer `team` holds a same-ID overlay of `x` whose `ARTIFACT.md` declares `type: skill` and `extends: x`, with no `version` and no `mcpServers`, beside a `SKILL.md` with no `metadata.version`. The wrapper inserts a `team` row at `x@0.0.0-auto.0` with a different hash on the first `GetManifest` for that key. The ingest re-derives N and reaches the insert path at `0.0.0-auto.1`. The result carries one `Rejected` entry with `Code` `ingest.invalid_artifact` and `Details` equal to `{"constraint": "executable_inheritance", "fields": ["mcpServers[jira]"]}`, no `ConflictReport`, and no `team` row at `0.0.0-auto.1`. The case fails if a re-derivation attempt bypasses the check.
- `pkg/registry/ingest/executable_inheritance_test.go` `TestIngest_ExecutableInheritancePlainSkillParentSetsNoField` (`// Spec: §4.6, §13.4`). Layer `L1` ingests `x` as a `SKILL.md` alone whose frontmatter carries `allowed-tools` and a `metadata` mapping. Layer `L2` ingests `y` with an `ARTIFACT.md` declaring `type: skill`, `version: 1.0.0`, and `extends: x`. The result has one accepted artifact and no rejection, and the stored `y@1.0.0` has `ExtendsPin` `x@0.0.0-auto.0`.

### TEST-2B. Ingest-to-HTTP integration tests

`test/integration/plain_skill_ingest_test.go` (new), run over `ingestConvergenceStores`, so memory and SQLite always run and Postgres runs when `PODIUM_POSTGRES_DSN` is set. The test uses one layer, `L`. `// Spec: §7.3.1, §4.7.6, §4.7.10, §13.4`:

- The first ingest assigns `0.0.0-auto.0`, and unchanged bytes are idempotent.
- Changed bytes get `0.0.0-auto.1` with no conflict.
- A revert to the first bytes lands in `Rejected` with code `ingest.invalid_artifact` and `Details` `{"constraint": "auto_version_duplicate_content"}`, appears in no `Ingested` entry, stores no row, and `load_artifact` `latest` still serves `0.0.0-auto.1` and the changed body.
- The sequence `0.0.0-auto.N` (bytes H), then `metadata.version: 1.0.0`, then back to bytes H is rejected with the same constraint, and `latest` serves `1.0.0`.
- A tombstoned `0.0.0-auto.5` row seeded with `store.PutManifest` and a non-nil `DeletedAt` makes the next changed bytes get `0.0.0-auto.6`.
- Unregistered and re-registered layer (`// Spec: §7.3.1, §8.4`): ingest a plain skill into `L` (`0.0.0-auto.0`), unregister `L` with `DeleteLayerConfig`, which tombstones its rows (`pkg/store/store.go:466-470`), register `L` again, and reingest the same bytes. The record lands in `Rejected` with `details.constraint` `auto_version_duplicate_content`. It is reported neither as accepted nor as idempotent, and `ListManifestsIncludingDeleted` holds only the tombstoned row for the ID. Reusing the tombstoned version would make `PutManifest` a no-op on the existing key (`pkg/store/memory.go:131-137`) and report an accepted skill that is not served. Assigning `0.0.0-auto.1` would give the same bytes a second version that a later unregister and restore of `L` would serve beside the first. Changed bytes then ingest at `0.0.0-auto.1`.
- Soft-deleted cross-layer row and purge (`// Spec: §7.3.1, §8.4`): in a fresh store, register `L` and `M`, ingest a plain skill with bytes H into `L` (`0.0.0-auto.0`), and tombstone `L`'s rows with `DeleteLayerConfig`. `M` then ingests a version-less plain skill at the same ID with other bytes. It is not rejected as `ingest.collision`, because `crossLayerByID` holds only non-deleted rows, and it gets `0.0.0-auto.1`, past the tombstoned row. In a second fresh store with the same setup, `M`'s package carries bytes H and lands in `Rejected` with `details.constraint` `auto_version_duplicate_content`. `PurgeExpiredLayerDeletions(ctx, now.Add(time.Hour))` then hard-deletes `L`'s rows, and reingesting `M` accepts the record at `0.0.0-auto.0`, which `load_artifact` `latest` serves, and the purged N is reissued (D-15). The purge runs while `L` stays unregistered, because the memory store purges a tombstoned row only under a tombstoned layer (`pkg/store/memory.go:577-592`), while SQLite and Postgres purge by the row's `deleted_at` (`pkg/store/sqlite.go:989-992`, `pkg/store/postgres.go:1502-1504`).
- `@0.0.0-auto.1` loads, and `@0.0.x` returns not found.
- The stored row's `Frontmatter` is empty.
- A load through the HTTP server runs §13.4 admission and succeeds, and its `frontmatter` equals `manifest.SynthesizedSkillDocument(version)` with `type` `skill`.
- A two-goroutine race on SQLite only, run under `-race`, ingests different bytes for one ID in `L` and commits two distinct N with zero conflicts. The deterministic tests of TEST-2A cover the retry branches.
- Two layers, `base` and `team`, registered with `PutLayerConfig` in ascending `Order`, contribute one ID (`// Spec: §7.3.1, §13.11.3, §4.6`). `base` holds a version-less plain skill. `team` holds a same-ID overlay: a `SKILL.md` with no `metadata.version` and a hand-written `ARTIFACT.md` with `type: skill`, `extends: <id>`, two tags, and no `version`, so the §4.6 collision check at `pkg/registry/ingest/ingest.go:758-776` admits it. The first ingest of `base` assigns `0.0.0-auto.0` and the first ingest of `team` assigns `0.0.0-auto.1` with `ExtendsPin` `<id>@0.0.0-auto.0`. Unchanged reingests of both layers are idempotent and keep those versions. Changed bytes in `team` then get `0.0.0-auto.2`, whose stored `ExtendsPin` is `<id>@0.0.0-auto.1`, `team`'s own previous row, because the reference is unpinned, `latest` selects the most recently ingested row of the ID, `base` has ingested no row since `team`'s first ingest, and §4.6 makes an earlier version stored from the overlay's own layer a same-layer parent (`spec/04-artifact-model.md:724`). The case pins two things. N is derived across both layers' rows. An assigned-version overlay resolves its parent as `TestIngest_ExecutableInheritanceSameLayerOverlayPinAccepted` resolves an authored one.
- A separate case over a fresh store ingests `base` and then a third layer, `other`, whose package at that ID is a version-less plain skill with no `ARTIFACT.md`, and asserts that the record lands in `Rejected` with code `ingest.collision` and that no row is stored for `other`. The store holds no `team` row, because the §4.6 check also admits an incoming record when any existing cross-layer row is an overlay of the ID (`ingest.go:763-770`), so with `team`'s row stored it admits `other`. That admission predates this change, applies to authored versions in the same way, and is outside this proposal. A further case stores `team`'s overlay row and then ingests `other` with a package byte-identical to `base`'s plain skill. The §4.6 check admits it, and the record lands in `Rejected` with `details.constraint` `auto_version_duplicate_content` with no row stored for `other`.
- A both-files skill at `ARTIFACT.md` `version: 1.0.0` and `metadata.version: "1.0.0"` ingests at `1.0.0` (`// Spec: §7.3.1, §4.7.6`). Deleting its `ARTIFACT.md` and reingesting reports a `ConflictReport` with `ingest.immutable_violation` for that ID, and `load_artifact` `latest` still serves the original row and its authored `ARTIFACT.md`.

`test/integration/reingest_pipeline_test.go` (`// Spec: §4.2, §7.3.1`): a layer whose root directory directly holds a `SKILL.md`, reingested through the layer endpoint, answers HTTP 422 with code `ingest.lint_failed`, and the response is not the retryable `registry.unavailable`. In the same file (`// Spec: §7.3.1, §6.10, §4.2`), a `local` layer whose only package is `x/SKILL.md`, a symlink to a file outside the layer directory, reingested through the layer endpoint as `TestReingestPipeline_SQLiteLocalSource` reingests its layer, answers HTTP 502 with code `ingest.source_unreachable`, and no row for `x` is stored. In the same file (`// Spec: §7.3.1`), a `local` layer holding a version-less `y/SKILL.md` is reingested through the layer endpoint, `y/SKILL.md` is changed and reingested, and then the first bytes are restored and reingested. The third response is HTTP 200, with `accepted` and `idempotent` both 0, and a `rejected` array holding one entry with `artifact_id` `y`, `code` `ingest.invalid_artifact`, and `details` equal to `{"constraint": "auto_version_duplicate_content"}`.

`test/integration/delivery_attestation_test.go`: add a `team/plainskill` row to `daRecords` with type `skill`, empty `Frontmatter`, and `SkillRaw` set, and extend `TestLoadArtifact_SingleAndBatchAgreeOnTheDeliveryHash` so the single and batch records for that row agree, verify, and frame the synthesized document. No storetest conformance case is added, because no SPI method changes.

### TEST-3. Cross-mode equivalence and golden materialization

Targets: `testdata/registries/reference/` (new `org-defaults` packages and a `README.md` note), `test/integration/sync_equivalence_test.go`, `test/materialization/golden_test.go`, and `test/materialization/testdata/golden/`.

Equivalence:

- Add these packages. No other layer contributes any of their canonical IDs, so no same-ID overlay shadows them, and each lock's entries for an ID come from one record (`pkg/registry/filesystem/walk.go:89-133` dedupes by canonical ID). The lock holds one entry per materialized file (`pkg/sync/sync.go:383-392`), so a both-files package yields more than one entry for its ID under `none`.
  - `org-defaults/plain-skills/unversioned/SKILL.md` (ID `plain-skills/unversioned`): no `ARTIFACT.md` and no `metadata.version`.
  - `org-defaults/plain-skills/versioned/SKILL.md` (ID `plain-skills/versioned`): no `ARTIFACT.md`, and `metadata.version: "1.0.0"`.
  - `org-defaults/plain-skills/overlay-unversioned/` (ID `plain-skills/overlay-unversioned`): a `SKILL.md` with `metadata.version: "1.0.0"` and a hand-written `ARTIFACT.md` overlay holding `type: skill`, `tags`, and the pointer comment, with no `version`.
  - `personal/notes/versioned-child/` (ID `notes/versioned-child`): a `SKILL.md` and a hand-written overlay holding `type: skill`, `version: 1.0.0`, and `extends: plain-skills/unversioned`.
  - `personal/notes/meta-child/` (ID `notes/meta-child`): a `SKILL.md` with `metadata.version: "2.0.0"` and a hand-written overlay holding `type: skill` and `extends: plain-skills/versioned`, with no `version` and no `description`.
- Update the fixture README to say why these entries exist.
- `TestSyncEquivalence_FilesystemVsServerByteIdentical` keeps its adapter list (`none`, `claude-code`, and `cursor`). Codex cannot run on this fixture because of its §6.9 untranslatable cells (`sync_equivalence_test.go:39-44`).
- The existing `assertTreesEqual` and `assertLockArtifactsEqual` cover byte equality and the lock `id`, `version`, and `content_hash`. Add these assertions:
  - The filesystem lock records `0.0.0-auto.0` for `plain-skills/unversioned`, `1.0.0` for `plain-skills/versioned`, `1.0.0` for `plain-skills/overlay-unversioned`, `1.0.0` for `notes/versioned-child`, and `2.0.0` for `notes/meta-child`. `assertLockArtifactsEqual` then requires the server lock to match, which pins CODE-12 for the overlay that omits `version`.
  - Under `none`, neither `plain-skills/unversioned` nor `plain-skills/versioned` has an `ARTIFACT.md` in the materialized tree, `plain-skills/overlay-unversioned` has its authored overlay byte for byte, and the merged `ARTIFACT.md` of `notes/meta-child` carries `version: 2.0.0` and `plain-skills/versioned`'s `SKILL.md` description. `assertTreesEqual` then pins CODE-6's merged version and admitted parent description, and CODE-4's description fallback, across modes.
  - The `claude-code` tree holds `.claude/skills/<name>/SKILL.md` for each of the added skills.
- The other users of the fixture assert non-empty results only and stay green (`test/conformance/reference_registry_test.go`, `test/integration/sync_server_content_hash_test.go`, `test/integration/sync_manifest_body_test.go`).
- `notes/versioned-child` and `notes/meta-child` live in `personal` and extend skills in `org-defaults`, so both edges cross layers and the §4.6 executable-field rule tests them in both modes. Neither child authors an executable field, and neither plain-skill parent sets one. Neither mode rejects or drops either child, and the lock assertions above fail if either mode does.
- Annotate with `// Spec: §11, §2.2, §4.3.4, §13.11.3`.

Golden:

- Add one plain-skill fixture to `canonicalArtifacts`. Its artifact field is the D-6 bytes for `0.0.0-auto.0`, written as a literal, its skill field is a `SKILL.md`, and it has one bundled resource.
- Regenerate with `UPDATE_GOLDEN=1 go test ./test/materialization/`, and confirm the diff adds only the new case's per-skill files in each harness golden and that `none.golden` gains `SKILL.md` and the resource with no `ARTIFACT.md` for that ID.
- Annotate with `// Spec: §6.7, §4.3.4`.

### TEST-4A. Scaffold end-to-end tests, edited in place

Land with CODE-10 so the suite stays green. Each test keeps or gains `// Spec: §4.3.4`.

- `test/e2e/frontmatter_schema_test.go` `TestFrontmatter_SkillScaffoldSplit` and `test/e2e/artifact_types_test.go` `TestArtifactTypes_ScaffoldSkillSplit`: assert `SKILL.md` alone with `metadata.version` and no `ARTIFACT.md`. Move the `--tags`, `--sensitivity`, and `--when-to-use` cases to an exit-2 refusal assertion that checks the overlay message names each flag passed.
- `test/e2e/artifact_extends_test.go` `TestExtends_ScaffoldExtendsField`: `--extends` with `--type skill` exits 2 with the overlay message, and an `--extends` success case on a non-skill type stays.
- `test/e2e/artifact_hints_test.go` `TestHints_ScaffoldSkillWithAddedHintsLintsClean`: write a hand-authored `ARTIFACT.md` overlay carrying the hints beside the scaffolded `SKILL.md`, and assert the pair lints clean.
- `test/e2e/core_concepts_test.go` `TestCoreConcept_ScaffoldSkill` and `test/e2e/readme_claims_test.go` `TestReadme_ScaffoldSkill`: assert `SKILL.md` carries `metadata` `version` and `ARTIFACT.md` is absent.
- `test/e2e/cli_reference_test.go` `TestCLI_ScaffoldSkill` and `TestCLI_ScaffoldSkillExample`: drop `--tags`, assert `ARTIFACT.md` is absent and `SKILL.md` holds `version: "0.1.0"` under `metadata:`, and keep the lint-clean check. Add a case where `--tags` with `--type skill` exits 2.
- `test/e2e/cli_reference_test.go` `TestCLI_ScaffoldExtends`: `--type skill --extends base/base-skill@1.x` exits 2 with the overlay message, and no `ARTIFACT.md` is read. The test adds a `--type context --extends base/base-skill@1.x` success case that asserts `extends: base/base-skill@1.x` in `ARTIFACT.md`.
- `test/e2e/governance_adoption_test.go` `TestGovernance_ScaffoldSensitivityHigh`: switch to `--type context`, which keeps `sensitivity: high`, `type: context`, and `version: 0.1.0` in `ARTIFACT.md`, and add a `--type skill --sensitivity high` case that asserts exit 2 with the overlay message. `TestGovernance_ScaffoldInvalidSensitivity` stays unchanged, because CODE-10 runs `validateSensitivity` before the refusal, so `--sensitivity critical` still reports `must be one of`.

### TEST-4B. Import end-to-end tests, edited in place

Land with CODE-11. `// Spec: §4.3.4`.

- `test/e2e/bundled_resources_test.go` `TestBundled_ImportCreatesLayer` and `TestBundled_ImportDryRun`, and `test/e2e/readme_claims_test.go` `TestReadme_Import`: drop `--type`, assert `SKILL.md` carries `metadata.version` and no `ARTIFACT.md` is written.
- Add cases: a source subdirectory holding a single non-`SKILL.md` `.md` file imports as a skill; a `metadata.version` mismatch, a source `ARTIFACT.md`, and a frontmatter-less file each exit 1; `--type` exits 2; `--version 1.0` exits 2 and writes nothing; and `--dry-run` with a mismatch exits 1 and writes nothing.

### TEST-4C. Plain-skills end-to-end test

`test/e2e/plain_skills_test.go` (new). The file carries no D-slug header, because no docs page maps to it; its header comment names §4.2, §4.3.4, §4.4, §6.7, and §13.11.3. Fixture `testdata/registries/plain-skills/`: `skills/greet/SKILL.md` (no version) with `scripts/hello.sh` and `references/notes.md`, `skills/summarize/SKILL.md` with `metadata.version: "1.0.0"`, `skills/greet/examples/nested/SKILL.md` with valid frontmatter, a `.github/workflows/ci.yml`, and a top-level `README.md`. No file in it is named `ARTIFACT.md`, and it has no `.registry-config`. Reuse the existing helpers (`startServerArgs`, `bridgeLoad`, `bootSyncServer`, and `srvSyncEnv`).

1. Filesystem-source sync: `podium sync --registry <fixture>/skills --harness claude-code`. `greet` and `summarize` land at `.claude/skills/<name>/SKILL.md`; `.claude/skills/greet/examples/nested/SKILL.md` exists as a resource; no `.claude/skills/nested/` directory exists; and the lock records `0.0.0-auto.0` for `greet` and `1.0.0` for `summarize`.
2. Repository-root sync: `podium sync --registry <fixture>` finds `skills/greet` and `skills/summarize`, and nothing under `.github/` is materialized.
3. MCP delivery: `startServerArgs` with `serve --standalone --sign registry-key --layer-path <fixture>/skills`, then `bridgeLoad` of `greet` with `PODIUM_SIGNATURE_PROVIDER=registry-managed`, `PODIUM_SIGNATURE_VERIFY_KEY=<the server key>`, and `PODIUM_VERIFY_SIGNATURES=always`. The load succeeds, and the `PODIUM_MATERIALIZE_ROOT` output holds `SKILL.md` and the resources with no `ARTIFACT.md`. A GET of `/v1/load_artifact?id=greet` returns `frontmatter` equal to the D-6 bytes for `0.0.0-auto.0`.

Run with `-v` and grep the output for `SKIP` before claiming a pass.

### TEST-5. SDK tests

Targets: `sdks/podium-ts/src/resolution_overlay_login.test.ts`, `sdks/podium-ts/src/index.test.ts` (the `LoadedArtifact.materialize` block), `sdks/podium-py/tests/test_resolution_overlay_login.py`, and `sdks/podium-py/tests/test_client.py` (the materialize tests). Each test cites `§4.3.4, §4.4, §6.4` or `§6.7, §7.6` as applicable.

1. A `SKILL.md`-only overlay package loads as `type: skill`, and its `frontmatter` equals a string literal copied from the §4.3.4 synthesized document rather than the SDK helper's output. One case has no `metadata.version` (`0.0.0-auto.0`), one has `metadata.version: "1.0.0"` quoted, one has it unquoted, and one has `metadata.version: "1.0"`, which reports `0.0.0-auto.0`. In each case, the artifact that `loadArtifact` / `load_artifact` returns for the package has a `manifest_body` equal to the `SKILL.md` body text (for example `Greet the user by name.`), and that `manifest_body` does not contain `Synthesized by Podium`. Both overlays pass the overlay record's `body` through as `manifest_body` (`sdks/podium-ts/src/index.ts:1400`, `sdks/podium-py/podium/client.py:1428`), and today they parse that body out of `ARTIFACT.md` (`sdks/podium-ts/src/overlay.ts:383`, `sdks/podium-py/podium/_overlay.py:308`), so this assertion pins CODE-9's rule that the body never comes from the synthesized document. Items 1 and 6 alone do not pin it, because neither reads the body. These assertions sit beside the existing overlay-body assertions (`sdks/podium-ts/src/resolution_overlay_login.test.ts:133`, `sdks/podium-py/tests/test_resolution_overlay_login.py:213`) and cite `§4.3.4, §6.4`.
2. Version precedence: an `ARTIFACT.md` without `version` plus a `SKILL.md` `metadata.version` reports the metadata version; both declaring the same version reports it; and an authored `ARTIFACT.md` is served as-is. A both-files package whose `ARTIFACT.md` declares `type: skill` and no `version` loads with version `0.0.0-auto.0` in two cases: its `SKILL.md` has no `metadata.version`, and its `SKILL.md` has `metadata.version: "1.0"`. Today both SDKs report the empty string there (`sdks/podium-ts/src/overlay.ts:423`, `version: fields.version`; `sdks/podium-py/podium/_overlay.py:339`, `version=str(fields.get("version", ""))`), so these cases pin CODE-9's both-files bullet and the D-8 overlay value that the Go walker test in CODE-4 pins for the same package. They live in `resolution_overlay_login.test.ts` and `test_resolution_overlay_login.py` and cite `§4.3.4, §6.4`.
3. A plain skill whose subdirectory holds a valid `SKILL.md` loads as one package, and the nested file is in its resources. The existing nested-package tests stay unchanged. A case with an `ARTIFACT.md` declaring a non-skill type beside a root `SKILL.md` keeps that `SKILL.md` as a resource.
4. Round trip: load a `SKILL.md`-only overlay package through `loadArtifact` / `load_artifact`, then call `materialize()`. The output holds `SKILL.md` byte-identical to the source and the bundled resources, and no `ARTIFACT.md`.
5. Writer unit cases on hand-built records: synthesized frontmatter writes no `ARTIFACT.md`; an authored `ARTIFACT.md` is written; and a frontmatter that differs from the synthesized document by one byte, or lacks the pointer comment, is written.
6. The overlay record's description comes from `SKILL.md`, and overlay search finds the skill by that description.

## Manual validation

**MV-1.** Append scenario S96 to `test/manual-validation.md` after S95, add its index row, and edit the existing scenarios that CODE-10 and CODE-11 change, which are every scenario that scaffolds a skill and then reads or edits its `ARTIFACT.md` or edits the stored `frontmatter` column of its row, plus S31 for import. A search of `test/manual-validation.md` for `ARTIFACT.md` inside the scenarios that run `podium artifact scaffold --type skill` finds S01, S10, S18, S26, S64, S66, and S84, plus S23, whose `ARTIFACT.md` files belong to hand-built packages and need no edit. A search for `update manifests set` finds S64 and S69, and S69 tampers the scaffolded skill's stored `frontmatter`. S17 and S19 pass `--sensitivity`, and no scenario passes `--tags`, `--when-to-use`, or `--extends` to a skill scaffold. Port 8198 is unused by other scenarios; S91 through S95 use 8193 through 8197. S91 through S95 hand-build both-files skills whose `ARTIFACT.md` declares `version` and whose `SKILL.md` declares no `metadata.version`, and none runs `podium artifact scaffold` or `podium import`, so none needs an edit.

Index row, after the S95 row:

```
| S96 | An Agent Skills directory with no ARTIFACT.md is a Podium layer | standalone | none | none | none |
```

````markdown
## S96: An Agent Skills directory with no ARTIFACT.md is a Podium layer

**Goal.** Validate that a directory of plain Agent Skills, with no `ARTIFACT.md`
anywhere, lints, syncs through filesystem source, serves from a standalone
registry with a registry-assigned version and the synthesized `ARTIFACT.md`,
assigns a new version on each content change, rejects a revert to bytes it already stored, materializes
with no `ARTIFACT.md`, and leaves the source tree unmodified.

**Covers.** §4.2 package discovery, §4.3.4 version precedence and the
synthesized `ARTIFACT.md`, §4.4 bundled resources, §4.7.6 the reserved version
form, §6.7 the `none` layout, §7.3.1 registry-assigned versions, and §13.11.3,
through the compiled binaries.

**Why by hand.** An operator reads the materialized tree, the lock file, and the
raw `frontmatter` member side by side. A `nested` skill directory beside
`greet`, a lock version other than `0.0.0-auto.0` for `greet`, a `frontmatter`
that differs from the synthesized bytes, an `ARTIFACT.md` in the `none` output,
an `ingest.immutable_violation` after an edit, a revert that is accepted
instead of rejected, or a modified source tree is the defect this scenario catches.

**Steps.**

1. Run the isolation block. Build a plain skills repository.

   ```bash
   mkdir -p "$WORK/repo/skills/greet/examples/nested" "$WORK/repo/skills/summarize" "$WORK/repo/.claude/skills/hidden"
   printf -- '---\nname: greet\ndescription: Greet a user politely\n---\n\nGreet the user by name.\n' > "$WORK/repo/skills/greet/SKILL.md"
   printf -- '---\nname: nested\ndescription: An example skill kept as a resource\n---\n\nExample.\n' > "$WORK/repo/skills/greet/examples/nested/SKILL.md"
   printf -- '---\nname: summarize\ndescription: Summarize a document\nmetadata:\n  version: "1.0.0"\n---\n\nProduce a short summary.\n' > "$WORK/repo/skills/summarize/SKILL.md"
   printf -- '---\nname: hidden\ndescription: A skill under a dot directory\n---\n\nHidden.\n' > "$WORK/repo/.claude/skills/hidden/SKILL.md"
   printf 'Plain skills repository.\n' > "$WORK/repo/README.md"
   git -C "$WORK/repo" init -q && git -C "$WORK/repo" add -A && git -C "$WORK/repo" -c user.email=alice@acme.com -c user.name=alice commit -qm init
   ```

   **Expect.** `git -C "$WORK/repo" log --oneline` prints one commit.

2. Lint the skills directory.

   ```bash
   podium lint --registry "$WORK/repo/skills"; echo "lint exit=$?"
   ```

   **Expect.** `lint exit=0`, and the output carries one
   `lint.skill_version_assigned` warning, for `greet` only.

3. Sync through filesystem source, then inspect the output, the lock, and the
   source tree.

   ```bash
   podium sync --registry "$WORK/repo/skills" --target "$WORK/fs" --harness claude-code
   (cd "$WORK/fs" && find .claude/skills -name SKILL.md | sort)
   grep -A3 'greet' "$WORK/fs/.podium/sync.lock"
   git -C "$WORK/repo" status --porcelain
   ```

   **Expect.** `find` prints `.claude/skills/greet/SKILL.md`,
   `.claude/skills/greet/examples/nested/SKILL.md`, and
   `.claude/skills/summarize/SKILL.md`, and no `.claude/skills/nested/`
   directory exists. The lock records `version: 0.0.0-auto.0` for `greet`.
   `git status` prints nothing.

4. Serve the skills directory from a standalone registry and read the raw
   `load_artifact` response for `greet`.

   ```bash
   podium serve --standalone --no-embeddings --layer-path "$WORK/repo/skills" --bind 127.0.0.1:8198 > "$WORK/srv.log" 2>&1 &
   SRV=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8198/healthz
   server_alive "$SRV" "$WORK/srv.log"
   export PODIUM_REGISTRY=http://127.0.0.1:8198
   show() { curl -s "$PODIUM_REGISTRY/v1/load_artifact?id=greet" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d['version']); print(repr(d['frontmatter'])); print(d['manifest_body'])"; }
   show
   ```

   **Expect.** The first line is `0.0.0-auto.0`. The second line is
   `'---\ntype: skill\nversion: 0.0.0-auto.0\n---\n<!-- Synthesized by Podium: this skill has no ARTIFACT.md. -->\n'`.
   The body reads `Greet the user by name.`

5. Sync from the server with `claude-code` and with `none`, then compare.

   ```bash
   podium sync --registry "$PODIUM_REGISTRY" --target "$WORK/srv" --harness claude-code
   podium sync --registry "$PODIUM_REGISTRY" --target "$WORK/raw" --harness none
   diff -r "$WORK/fs/.claude" "$WORK/srv/.claude" && echo SAME
   find "$WORK/raw" -name ARTIFACT.md | wc -l
   ```

   **Expect.** `diff` prints nothing and the step prints `SAME`. The `find`
   count is `0`.

6. Edit `greet`, reingest, and show it. Revert the edit, reingest, and show it
   again. Then declare a version, reingest, show it, and restore the file.

   ```bash
   printf -- '---\nname: greet\ndescription: Greet a user politely\n---\n\nGreet the user warmly by name.\n' > "$WORK/repo/skills/greet/SKILL.md"
   podium layer reingest --registry "$PODIUM_REGISTRY" skills
   show
   git -C "$WORK/repo" checkout -- skills/greet/SKILL.md
   podium layer reingest --registry "$PODIUM_REGISTRY" skills; echo "exit=$?"
   show
   printf -- '---\nname: greet\ndescription: Greet a user politely\nmetadata:\n  version: "1.0.0"\n---\n\nGreet the user by name.\n' > "$WORK/repo/skills/greet/SKILL.md"
   podium layer reingest --registry "$PODIUM_REGISTRY" skills
   show
   git -C "$WORK/repo" checkout -- skills/greet/SKILL.md
   ```

   **Expect.** The first reingest reports no `ingest.immutable_violation`, and
   the first `show` prints `0.0.0-auto.1` and `Greet the user warmly by name.`
   The second reingest prints `rejected: greet (ingest.invalid_artifact): `
   followed by a reason that names `metadata.version`, and `exit=1`. The
   second `show` still prints `0.0.0-auto.1` and `Greet the user warmly by name.`
   The third `show` prints `1.0.0`, a `frontmatter` of
   `'---\ntype: skill\nversion: 1.0.0\n---\n<!-- Synthesized by Podium: this skill has no ARTIFACT.md. -->\n'`,
   and `Greet the user by name.`

7. Sync the repository root and the dot directory as layers.

   ```bash
   podium sync --registry "$WORK/repo" --target "$WORK/root" --harness claude-code
   podium sync --registry "$WORK/repo/.claude" --target "$WORK/dot" --harness claude-code
   ls "$WORK/root/.claude/skills"; ls "$WORK/dot/.claude/skills"
   grep -c '^ *- id: skills/hidden$' "$WORK/dot/.podium/sync.lock"
   ```

   **Expect.** The first listing names `greet` and `summarize` and no `hidden`.
   The second names `hidden`, and the lock count is `1`. The pattern matches
   the entry's `id` line only, because the `materialized_path` line
   `.claude/skills/hidden/SKILL.md` also contains `skills/hidden`.

**Cleanup.** Stop the server and `rm -rf "$WORK"`.
````

Edits to existing scenarios:

- **S01** (`## S01: Solo filesystem, one skill, Claude Code`). The expected-results bullet for step 2 becomes "Step 2 reports `Scaffolded skill at .../reg/greet/` and the directory holds `SKILL.md` alone, with `version: "0.1.0"` under `metadata:`." Add a step after step 2: run `podium artifact scaffold --type skill --tags x --description "Tagged" "$WORK/reg/tagged"; echo "exit=$?"`, and expect `exit=2` and a message that names `--tags` and the `ARTIFACT.md` overlay.
- **S10** (`## S10: Standalone server, multiple Git-source layers`, lines 760-780). The step text says the `team` copy of `greet` carries a hand-written `ARTIFACT.md` overlay that declares `version: 0.2.0` and `extends: greet`, and that its `SKILL.md` `metadata.version` is set to the same value so the two files agree (§4.3.4). The `python3` block that edits `$WORK/team/greet/ARTIFACT.md` is replaced by:

  ```bash
  printf -- '---\ntype: skill\nversion: 0.2.0\nextends: greet\n---\n<!-- Skill body lives in SKILL.md. -->\n' > "$WORK/team/greet/ARTIFACT.md"
  sed -i.bak 's/^  version: "0.1.0"$/  version: "0.2.0"/' "$WORK/team/greet/SKILL.md" && rm "$WORK/team/greet/SKILL.md.bak"
  ```

  The `rm` keeps the backup out of the commit, where it would become a bundled resource. The Expect blocks are unchanged.
- **S17** (line 1252) and **S19** (lines 1383 and 1429). Replace each `podium artifact scaffold --type skill --sensitivity high ...` with a scaffold without `--sensitivity`, followed by a heredoc that writes a hand-written overlay `ARTIFACT.md` holding `type: skill`, `version: 0.1.0`, `sensitivity: high`, and the pointer comment `<!-- Skill body lives in SKILL.md. -->`. Each Expect block is unchanged.
- **S18** (`## S18: Lifecycle, versioning, and deprecation`, lines 1300-1345). The scaffold writes `SKILL.md` alone with `version: "0.1.0"` under `metadata:`, so each version edit targets that line: step 2 sets it to `1.0.0` before the first commit and step 3 sets it to `2.0.0`, each with `sed -i.bak 's/^  version: ".*"$/  version: "<v>"/' "$WORK/repo/deploy/SKILL.md" && rm "$WORK/repo/deploy/SKILL.md.bak"`. Step 4 sets it to `3.0.0` and writes a hand-written overlay `$WORK/repo/deploy/ARTIFACT.md` holding `type: skill`, `version: 3.0.0`, `deprecated: true`, `replaced_by: deploy@2.0.0`, and the pointer comment, because `deprecated` and `replaced_by` are Podium-only fields. Step 4 commits with `git add -A` before `git commit -qm`, because `commit -a` does not stage the new file. The prose that names `$WORK/repo/deploy/ARTIFACT.md` as the version's home names `SKILL.md` `metadata.version` instead. The Expected block is unchanged.
- **S26** (`## S26: Reverse-dependency impact analysis`, lines 1933-1942). The `python3` block that edits `$WORK/reg/deploy-pro/ARTIFACT.md` is replaced by a `printf` that writes a hand-written overlay holding `type: skill`, `version: 0.2.0`, `extends: deploy-base`, and the pointer comment, and by a `sed -i.bak` that sets `deploy-pro`'s `SKILL.md` `metadata.version` to `0.2.0`, followed by `rm` of the backup, which would otherwise become a bundled resource of the local layer. The comment above the block names both files. The Expect blocks are unchanged.
- **S31** (`## S31: Import an existing skill tree into a layer`). The goal becomes "converts a directory of plain skills into plain Agent Skills directories that lint, serve, and are searchable". Step 3 runs `podium import --source "$WORK/skills" --target "$WORK/reg" --version 1.2.0`, then `find "$WORK/reg" -name ARTIFACT.md | wc -l` and `grep -A1 '^metadata:' "$WORK/reg"/*/SKILL.md`. The Expected block says the count is `0`, each `SKILL.md` carries `version: "1.2.0"` under `metadata:`, lint reports `lint: no issues.`, and search returns `greet`. Add refusal steps with their exit codes: a source `SKILL.md` with `metadata.version: "9.9.9"` (exit 1, naming the directory), a source directory holding an `ARTIFACT.md` (exit 1), and `--type skill` (exit 2).
- **S64** (`## S64: A registry migrates its stored hashes on the first start`, lines 7671-7758). Step 2 says it authors one skill carrying a `SKILL.md` and two bundled resources, and its Expect lists `SKILL.md`, `notes.md`, and `scripts.py`. Step 5's hand computation reads `OLD=$( { cat "$D/SKILL.md"; printf 'notes.md'; cat "$D/notes.md"; printf 'scripts.py'; cat "$D/scripts.py"; } | shasum -a 256 | cut -d' ' -f1 )`, and its prose drops `ARTIFACT.md` from the concatenation. The literal `content_hash` values in the step 3 and step 4 Expect blocks change, because the scaffolded package changes. The implementor replaces them with the values the post-CODE-10 binaries print, after confirming the step 3 value against `version.CanonicalContentHash` over the scaffolded files with a zero-length first value. Every other Expect block is unchanged.
- **S66** (line 8066). The `sed` target becomes the `SKILL.md` `metadata.version` line of `early`: `sed -i.bak 's/^  version: "0.1.0"$/  version: "0.2.0"/' "$WORK/reg/early/SKILL.md"`. The Expect block is unchanged.
- **S69** (`## S69: The registry refuses a row edited in its store`, step 4, lines 8300-8322). The scaffolded `admit` skill has no `ARTIFACT.md`, so ingest stores a nil `frontmatter` (CODE-5). The SQLite driver binds it as NULL (`pkg/store/sqlite.go:336`), and `NULL || ' '` is NULL, so the step's edit would change nothing and both refusals would read `loaded`. Step 4 keeps `frontmatter, signature, skill_raw` in `s69_keep`, appends a byte to the stored `skill_raw` with `update manifests set skill_raw = skill_raw || ' ' where artifact_id='admit';`, and restores `skill_raw` from `s69_keep`. The step prose names `skill_raw`, the admitted `SKILL.md` bytes, in place of `frontmatter`. The stored `content_hash` is unchanged, so the edit still catches a registry that checks the signature and skips the hash, now through the plain-skill admission of SPEC-13. Steps 5 and 6, every Expect block, and the "Why each edit" paragraph are unchanged.
- **S84** (lines 10446 and 10513). After scaffolding `fresh`, write a hand-written overlay `ARTIFACT.md` holding `type: skill`, `version: 1.0.0`, and the pointer comment, so `setver` and the `deprecated: true` insertion keep editing `ARTIFACT.md` with the same meaning. The scaffolded `SKILL.md` `metadata.version` is removed in the same step so the two files never disagree.

## Documentation changes

### DOC-1. Quickstart, README, overview, and the first-skill tutorial

Targets: `docs/getting-started/quickstart.md` §3 in full (lines 97-160), the watch-mode paragraph (237-240), the "Add more artifacts" bullet (253-257), and the troubleshooting entry (294-298); `README.md` (182-205); `docs/overview.md` (99-124); and `docs/authoring/your-first-skill.md`.

Quickstart, README, and overview:

- §3 opens with: "A skill is a directory with a `SKILL.md` from the agentskills.io standard at its root. An optional `ARTIFACT.md` beside it carries Podium-only fields."
- Remove the `ARTIFACT.md` heredoc and add `metadata:\n  version: "1.0.0"` to the `SKILL.md` heredoc.
- "Anything beside the two manifests" becomes "Anything beside the manifest files".
- The role bullets become: "`SKILL.md` carries the agent-facing content and the version in `metadata.version`."; "An optional, hand-written `ARTIFACT.md` carries Podium-only fields such as `when_to_use`, `tags`, and `sensitivity`. See the frontmatter reference."; and the existing canonical-ID bullet.
- The "Add more artifacts" bullet reads "with a `SKILL.md` for a skill or an `ARTIFACT.md` for other types".
- The troubleshooting entry reads "Confirm that the artifact directory holds `SKILL.md` (for a skill) or `ARTIFACT.md` (for other types) at its immediate root."

Tutorial:

- "Starting point" shows `SKILL.md` and `scripts/` only.
- "Add fuller frontmatter" covers `description`, `license`, and `metadata.version` in `SKILL.md`, and states that the minimum is `name` and `description`, with `metadata.version` recommended.
- A new section, "Add Podium-only fields with an ARTIFACT.md overlay", has the author write by hand an `ARTIFACT.md` containing `type: skill`, `when_to_use`, `tags`, and `sensitivity`, with no `version` and the pointer-comment body.
- "Declare runtime requirements" adds `runtime_requirements` to that overlay, and the final tree shows `ARTIFACT.md` as the overlay.
- The lint paragraph replaces "in both files" with "in `SKILL.md` and, when present, `ARTIFACT.md`", and names `lint.skill_version_assigned` as the warning for a skill with no declared version.

End-to-end fixtures that change with these pages:

- `greetRegistry` (`test/e2e/quickstart_flow_test.go:20-24`) and the `readme_claims` fixture become `SKILL.md`-only with `metadata.version`.
- `TestQuickstart_SyncNoneCanonical` (line 261), the watch test (near line 339, which polls `SKILL.md` instead), and `TestQuickstart_IntermediateDirsNotArtifacts` (line 412) assert `SKILL.md` exists and no `ARTIFACT.md` is written in the `none` output.
- `test/e2e/readme_claims_test.go:93-95` asserts `metadata.version` in `SKILL.md` and no `ARTIFACT.md` in the fixture, and line 171 flips to absence.
- `test/e2e/skill_tutorial_test.go`, edited in one commit with the tutorial page:
  - `greetArtifactFuller` and `greetArtifactRuntime` become the overlay contents without `version`.
  - `greetSkillBodyFuller` and `greetSkillWithBody` gain `metadata:\n  version: "1.0.0"` after `license: MIT`, as the "Add fuller frontmatter" section shows. Without it, `TestSkillTutorial_FullerFrontmatterLints`, `TestSkillTutorial_RuntimeRequirementsLints`, and `TestSkillTutorial_EndStateLintsClean` would each receive a `lint.skill_version_assigned` warning and fail their `lint: no issues.` assertion, which `cmd/podium/main.go:1093-1095` prints only for zero diagnostics. `TestSkillTutorial_ProseRefResolves` pairs `greetSkillWithBody` with `greetSkillArtifact`, whose `version: 1.0.0` equals the new `metadata.version`, so it stays clean.
  - `TestSkillTutorial_MinimalTwoFileLints` becomes `TestSkillTutorial_MinimalSkillLints`. Its fixture is `greetSkillBody` as `SKILL.md` alone, matching the "Starting point" section, which declares no version. It asserts that no `ARTIFACT.md` exists, exit 0, and exactly one `lint.skill_version_assigned` warning.
  - `greetSkillArtifact` stays in `test/e2e/helpers_test.go:698`, because other files use it. The remaining tutorial tests that pair it with `greetSkillBody` keep its declared `version: 1.0.0`, so CODE-7 adds no diagnostic to them.
- Run `go test -v ./test/e2e/ -run 'Quickstart|Readme|SkillTutorial'` and grep for `SKIP`.

### DOC-2. Authoring and reference pages

- `docs/authoring/frontmatter-reference.md`:
  - Lines 11-12 and 64 say "ARTIFACT.md (every non-skill type; optional overlay for skills)".
  - "File allocation for skills" (lines 22-39) follows SPEC-4's §4.3.4 table. Line 24 becomes "A skill's `SKILL.md` carries the agentskills.io fields, and an optional `ARTIFACT.md` that the author writes by hand carries the Podium-only fields, so that `SKILL.md` stays strictly within the agentskills.io specification. The allocation is mechanical:". The column header `ARTIFACT.md (skill)` becomes `ARTIFACT.md (skill, optional overlay)`. The `type` row's skill cell (line 34) becomes "Yes (`type: skill`); implied when `ARTIFACT.md` is absent". `version` leaves the row at line 35 and gets its own row above it: `SKILL.md` "As `metadata.version` (string)", `ARTIFACT.md` (skill) "Optional; equal to `metadata.version` when that is a `major.minor.patch` value", and `ARTIFACT.md` (non-skill) "Yes". The row at line 35 keeps `when_to_use` through `lint_suppress`.
  - Universal fields table (lines 79-83): the `type` row's Required cell becomes "Yes (implied for a skill with no `ARTIFACT.md`)". The `version` row's Required cell becomes "Yes, except for a skill", and its description gains "A skill's version is its `ARTIFACT.md` `version` or its `SKILL.md` `metadata.version`, and the registry assigns one when neither declares a usable value. See [Skill versions](#skill-versions)."
  - Add a "Skill versions" subsection stating the D-2 precedence, the reserved `0.0.0-auto.<N>` form, its pin and range rules, the rejection of a revert to stored bytes with `details.constraint: "auto_version_duplicate_content"` and its remedy (`metadata.version` or changed content), the re-registered-layer case and `podium layer restore`, the purge outcome, and the filesystem-source value. The purge outcome includes the D-4 cache case: an MCP server cache that held the purged version's record reports that version for the re-accepted content. The subsection also states that removing `ARTIFACT.md` from a skill changes its content hash, so a reingest at an unchanged `metadata.version` is rejected with `ingest.immutable_violation`, and the author bumps `metadata.version` or removes it to let the registry assign a version. It names `lint.skill_version_assigned` as the warning lint reports for a skill with no usable version. The page has no lint-code list, so no list changes.
- `docs/reference/frontmatter-schema.md`:
  - Line 15 and the lint list at lines 121-128 follow the SPEC-4 lint list.
  - Universal fields table (lines 23 and 25), following SPEC-4's §4.3.4 table. The `type` row's Required cell becomes "yes (implied for a skill with no `ARTIFACT.md`)", and its "For skills" cell becomes "In `ARTIFACT.md` when present; implied otherwise". The `version` row's Required cell becomes "yes, except for a skill", its description becomes "Semver. An authored version is `major.minor.patch` and author-chosen; a skill that declares no usable version carries a registry-assigned `0.0.0-auto.<N>`. Once `(artifact_id, version)` is ingested, it's bit-for-bit immutable.", and its "For skills" cell becomes "`metadata.version` in `SKILL.md`, or `version` in `ARTIFACT.md`; assigned when neither declares a usable one".
  - Line 100 (the second sentence of the `## SKILL.md (for type: skill)` intro) says "When a skill has an `ARTIFACT.md`, its body is empty (a one-line HTML comment pointer is allowed)."
  - After the `SKILL.md` top-level fields table (lines 102-111, before `### Body` at line 113), add the D-2 precedence: "A skill's version is the `ARTIFACT.md` `version` when that file declares one, and otherwise a `SKILL.md` `metadata.version` in `major.minor.patch` form. A `metadata.version` in any other form, such as `1.0`, is ignored, and with no `ARTIFACT.md` `version` the registry assigns the version. When both are set and `metadata.version` is a `major.minor.patch` value, the two are equal (`lint.skill_artifact_field_mismatch` otherwise)." The edge-case row for `metadata.version: "1.0"` with `ARTIFACT.md` `version: 1.0.0` points to this paragraph.
- `docs/authoring/bundled-resources.md`: line 9 says "other than its manifest files (`SKILL.md` for a skill, with an optional `ARTIFACT.md`, and `ARTIFACT.md` for other types)". Line 11 keeps "a file named `SKILL.md` in a subfolder such as `references/` is an ordinary bundled resource" and adds "and starts no package". The dot-directory sentence reads "so a manifest file (`ARTIFACT.md` or `SKILL.md`) anywhere below such a directory produces no artifact".
- `docs/authoring/extends.md`: state that an `extends:` child carries an `ARTIFACT.md` and that its parent may be a plain skill.
- `docs/authoring/index.md` lines 17 and 22: "SKILL.md for skills, with an optional ARTIFACT.md, and ARTIFACT.md for other types".
- `docs/authoring/artifact-types.md` line 41: label the `ARTIFACT.md` block as an optional overlay, and state that a `SKILL.md` alone is a complete skill.
- `docs/getting-started/concepts.md` (28-40) and `docs/reference/glossary.md` (17, 22) mirror SPEC-12's glossary entries.
- `docs/reference/http-api.md` line 176: for a skill without `ARTIFACT.md`, a search result's `frontmatter` is the frontmatter block of the §4.3.4 synthesized document. Line 235: the `load_artifact` `frontmatter` member is the synthesized document for such a skill.
- `docs/reference/error-codes.md` line 113: the `ingest.invalid_artifact` row lists every cause of the code and the `details.constraint` value each cause carries. Change "; or a child left an executable field to a parent in another layer, which carries `details.constraint: "executable_inheritance"` and `details.fields`." to "; a child left an executable field to a parent in another layer, which carries `details.constraint: "executable_inheritance"` and `details.fields`; or a skill with no declared version has content that a stored or soft-deleted row of the artifact already holds under another version (§7.3.1), which carries `details.constraint: "auto_version_duplicate_content"`. Set `metadata.version` in `SKILL.md` or change the content to publish such a skill."
- `docs/consuming/custom-via-sdk.md` (96, 241): `materialize()` omits `ARTIFACT.md` when the served document is the §4.3.4 synthesized document, which covers a skill with no `ARTIFACT.md` and an authored file byte-identical to that document, and writes it otherwise.
- `docs/consuming/configure-your-harness.md:495`: the `none` layout omits `ARTIFACT.md` when the served document is the §4.3.4 synthesized document, which covers a skill with no `ARTIFACT.md` and an authored file byte-identical to that document, and writes it otherwise.
- `docs/deployment/clustered.md:226`: "Edit `SKILL.md` for a skill or `ARTIFACT.md` for other types, plus any overlay and bundled resources".
- `docs/getting-started/how-it-works.md`, the "Versioning" section (lines 437-445), mirrors SPEC-6's §4.7.6 replacement. The first sentence becomes "Versions are semver, author-chosen through the manifest's `version:` field or, for a skill, `SKILL.md` `metadata.version`. A skill that declares no version receives `0.0.0-auto.<N>` from the registry." After the sentence that lists the pin forms, add "`@0.0.0-auto.<N>` pins a registry-assigned version exactly, and a range pin never selects one. See [Skill versions](../authoring/frontmatter-reference#skill-versions)." The rest of the page is unchanged. Lines 105-106 are the ASCII fallback of a both-files skill, which stays valid, and lines 129-131 name the file formats and the adapter behavior, which stay identical across tiers.

### DOC-3. Deployment pages

`docs/deployment/layers.md`: add a subsection "Using an Agent Skills repository as a layer" below the `registry.yaml` example. It states:

- Discovery treats every directory that directly contains `ARTIFACT.md` as a package, and a directory that directly contains `SKILL.md` as a package unless it sits inside another package (§4.2). A repository whose skills sit under `skills/<name>/` is registered as a git layer with `root: skills`, and the canonical IDs are `<name>`.
- Discovery skips every directory below the layer root whose name begins with a dot. A repository whose skills sit under `.claude/skills/` or `.agents/skills/` sets `root: .claude/skills` (IDs `<name>`) or `root: .claude` (IDs `skills/<name>`), and the page names the ID difference.
- `root` names a subtree path inside the repository and is written without a trailing slash.
- A repository whose root directory is itself a single skill cannot be registered as a git layer, because a manifest at the layer root has no canonical ID and `root` cannot name a directory above the repository root; a reingest of such a layer fails with `ingest.lint_failed`. Register it as a `local` layer whose path is a directory that holds a copy of the skill without its `.git/` directory, for example one filled by `git -C <clone> archive --prefix=<name>/ HEAD | tar -x -C <layer-path>`. Every file below a package's root, including files under `.git/`, is a bundled resource of that package (§4.4), so a layer that holds the clone itself ships its Git metadata as resources and changes the content hash with every Git operation.
- A `SKILL.md` without frontmatter in a directory outside any package fails discovery for the whole layer. Rename the file or add the Agent Skills frontmatter.

Every `root` value in the subsection omits the trailing slash. The YAML block is not a runnable example, so no doccov entry changes; `layers.md` already maps to D-cli.

`docs/deployment/local.md`: the line 74 annotation becomes "# type: skill — SKILL.md, with an optional ARTIFACT.md". Remove "(skills have both SKILL.md and ARTIFACT.md)" from the line 129 comment, and mark the line 131 `$EDITOR .../ARTIFACT.md` line as editing the optional overlay. Add one sentence after the tree: "[Skills that declare no version](#skills-that-declare-no-version) states the version each tier records for a skill whose `SKILL.md` declares none."

The pages that say the tiers produce bit-identical output state the exceptions SPEC-9 adds to §13.11.3, which the "Filesystem source after the registry ingested changed bytes" and "Filesystem source after a revert" edge-case rows accept:

- `docs/deployment/local.md`: line 160 gains "Skills that declare no version are the exception that the next subsection describes." after its bit-identical sentence. A new `### Skills that declare no version` subsection closes the "Migration paths" section, after the "**To clustered.**" paragraph and before `## Limits worth knowing`. It reads: "A filesystem source records a skill that declares no version as `0.0.0-auto.0`. A registry assigns a higher N once it has ingested changed bytes for the skill, and when several layers contribute the skill's ID, as a same-ID `extends:` overlay that declares no version does, so the lock-file versions differ between the tiers. When they differ for an `extends:` child, the `version:` line of the merged `ARTIFACT.md` that the `none` harness writes differs as well. A same-ID overlay that declares no version can also resolve a different parent in each tier after its content changes, because a registry pins the most recently ingested non-deprecated version of the ID and a filesystem source merges the overlay over the next-lower-precedence layer's artifact. A registry rejects a change of such a skill back to content it already stored under another version and keeps serving its newer version, while a filesystem source serves the reverted content. After a revert, the content hash, the body, and every harness's output differ between the tiers until the author sets `metadata.version` in `SKILL.md` or changes the content."
- `docs/deployment/single-node.md:138`: append "Skills that declare no version are the exception that [Local](local#skills-that-declare-no-version) describes." to the paragraph, so the text lives on one page.
- `docs/deployment/index.md:17` and `README.md:142-144`: append "[Local](local#skills-that-declare-no-version) lists the exceptions for skills that declare no version." to the bit-identical sentence's paragraph, with the absolute `https://lennylabs.github.io/podium/deployment/local` URL in `README.md`, as its neighboring links use. These are the only `README.md` lines DOC-3 edits; DOC-1 owns the rest.

### DOC-4. CLI reference

`docs/reference/cli.md`:

- Lint (line 186): "validating each `SKILL.md`, each `ARTIFACT.md`, and any `DOMAIN.md` against the type's schema". The rest of the paragraph is unchanged.
- `podium import` (188-203): the intro becomes "Normalizes a directory of skills into Agent Skills directories. Each immediate subdirectory of `--source` becomes a directory under `--target` named for that subdirectory. The directory holds a `SKILL.md`, taken from the source `SKILL.md` or, when there is none, from the single root-level `.md` file, plus every other file as a bundled resource. A subdirectory with no root-level `.md` file is skipped. `--version` is written to `SKILL.md` `metadata.version`. The command writes no `ARTIFACT.md` and never modifies the source. The command is optional, because a directory of skill directories is already a valid layer." Remove `[--type <type>]` from the synopsis and delete the `--type` row. The `--version` row becomes "Version written to `SKILL.md` `metadata.version`. Default: `1.0.0`." Add: "The command exits 1 and names the directory when a source directory holds an `ARTIFACT.md`, when an existing `SKILL.md` carries a `metadata.version` different from `--version`, when the chosen file has no frontmatter block, or when the root holds several `.md` files and no `SKILL.md`. `--dry-run` runs the same checks before reporting the plan."
- `podium artifact scaffold` (374-419): after the synopsis, add "For `--type skill`, the command refuses `--tags`, `--when-to-use`, `--extends`, and an explicitly passed `--sensitivity` with exit 2, because they are Podium-only fields that belong in a hand-written `ARTIFACT.md` overlay (see the frontmatter reference)." Line 391 becomes "`--version` defaults to `0.1.0`. For types other than `skill`, `--sensitivity` defaults to `low`." The skill table row becomes "`skill` | `SKILL.md` (with `metadata.version`) | —". The non-interactive example (404-414) drops the `--tags "release,workflow"` line. Line 417 becomes "This writes `finance/release/release-notes/SKILL.md` and creates the intermediate domain directories. `SKILL.md` carries `name`, `description`, `license`, and `metadata.version`. The command writes no `ARTIFACT.md`."

The D-cli test changes ride with TEST-4A and TEST-4B.

### DOC-5. CHANGELOG

The entries go under `[Unreleased]` in `CHANGELOG.md`. Each bullet is a bold lead sentence with its section citation.

`### Added` (new group, above `### Changed`):

- **A directory with a `SKILL.md` and no `ARTIFACT.md` is a skill artifact** (§4.2, §4.3.4, §4.7.6, §7.3.1, §13.11.3). Discovery, ingest, `podium sync`, the MCP server, and the SDK workspace overlays recognize it. Its version is `SKILL.md` `metadata.version` in `major.minor.patch` form. When no usable version is declared, the registry assigns `0.0.0-auto.<N>`: a filesystem source reports `0.0.0-auto.0`, and the registry issues a new N for each content change. It rejects a change back to bytes it already stored under another assigned version with `ingest.invalid_artifact` and `details.constraint: "auto_version_duplicate_content"`; declare `metadata.version` to publish such a change. `<id>@0.0.0-auto.<N>` pins an assigned version exactly, and a `<semver>.x` range never selects one. The registry serves a synthesized `ARTIFACT.md` (`type: skill` and the resolved version) in `frontmatter`, which the delivery hash covers, and the `none` adapter and SDK `materialize()` write no `ARTIFACT.md` for such a skill. `podium lint` warns with `lint.skill_version_assigned` when a skill declares no usable version.

`### Changed` (append to the existing group):

- **`SKILL.md` `metadata.version` sets a skill's version** (§4.3.4). When `ARTIFACT.md` also declares `version` and `metadata.version` is a `major.minor.patch` value, the two must be equal (`lint.skill_artifact_field_mismatch`), and a reserved `0.0.0-auto.<N>` value in either file is `lint.invalid_version`. A skill that ingests today with a disagreeing or reserved-form value fails ingest with `ingest.lint_failed` until it is corrected. Removing `ARTIFACT.md` from an ingested skill changes its content hash, so a reingest at an unchanged `metadata.version` fails with `ingest.immutable_violation`; bump `metadata.version` or remove it.
- **Discovery treats a directory holding `SKILL.md` as a package** (§4.2). A `SKILL.md` without frontmatter in a directory outside any package now fails the walk of its layer, and a `SKILL.md` directly at the layer root is an error. A `SKILL.md` inside a package stays a bundled resource, and no existing package's content hash changes.
- **`podium artifact scaffold --type skill` writes `SKILL.md` alone** (§4.3.4). The command sets `metadata.version` from `--version`, and it exits 2 when `--tags`, `--when-to-use`, `--extends`, or `--sensitivity` is passed, because those fields belong in a hand-written `ARTIFACT.md`.
- **`podium import` writes plain Agent Skills directories** (§4.3.4). It writes `--version` into `SKILL.md` `metadata.version` and never writes `ARTIFACT.md`. It takes the body from each immediate subdirectory's root-level file and skips a subdirectory with none, so a `SKILL.md` nested deeper is no longer imported. It fails on a source `ARTIFACT.md`, on a `metadata.version` that differs from `--version`, on a source file without frontmatter, and on a root with several `.md` files and no `SKILL.md`.

`### Removed` (new group, after `### Fixed` and before `### Documentation`):

- **`podium import --type`**: import produces skills only, and passing the flag exits 2.

No `### Documentation` bullet is added, because the page changes follow the behavior recorded above.

## Open questions

The proposal has no open questions. D-15 records the answers to Q-1 through Q-4.

## Non-goals

- Folding `command` or `context` artifacts into skill semantics, or inferring any type other than `skill` from a file's presence.
- Accepting Claude Code plugin directories (`.claude-plugin/`) as authoring input.
- The §1.5 comparison table and its closing sentence about upstreaming the format. The competitive-landscape proposal owns them.
- Changing the §4.6 executable-field rule or which stored version a same-ID `extends:` reference selects. Proposal 0057 implemented the rule (merge `a56ae170`), and its §4.6 text makes an overlay's own earlier version a same-layer parent. This change leaves `foldExtendsParent`, `manifest.MergeExtends`, `resolveExtendsPin`, both `executableInheritance` functions, `composeExtends`, and that §4.6 text untouched. A same-ID reference can select a row from a higher-precedence layer under `latest` today, for authored and assigned versions alike. That needs its own proposal with a §4.7.6 edit.
- Accepting Podium-only fields as top-level `SKILL.md` keys or as namespaced `metadata` keys (D2.5 options b and c).
- Changing the dot-directory skip, the root-level manifest refusal, the `multi_layer` ambiguity check, or the §4.4 nested-package boundary.
- A new store SPI method, schema column, or storetest conformance case for an auto-version counter.
- Any new §6.10 error code, env var, flag, `registry.yaml` key, HTTP member, or matrix cell, and any `details.constraint` value other than `auto_version_duplicate_content` (D-4).
- An unchanged reingest of an authored-version artifact after its layer is unregistered and registered again under its ID reports the artifact accepted while `PutManifest` leaves its row tombstoned and unserved (`pkg/store/memory.go:131-137`, `pkg/store/sqlite.go:325-360`). The memory store also never purges the tombstoned rows of a layer registered again (`pkg/store/memory.go:577-592`), while SQLite and Postgres do. Both predate this change and need their own proposal.
- Changing `version.Compare` or `AtLeast`, which order binary versions.
- A migration tool that removes `ARTIFACT.md` from existing skills, or any compatibility path other than keeping both-files skills working.
- Trimming a trailing slash from `source.git.root`, or fixing the existing `root: artifacts/` examples in `docs/deployment/layers.md`, `docs/deployment/clustered.md`, §4.6, and §14. That defect needs its own proposal.

## Resolved in adversarial review

Review rounds populate this section. Each bullet records a finding and where its fix landed.

### Draft reconciliation (2026-11-09)

- The draft's SPEC-5 review replaced the moved nested-package boundary with an ancestor rule, while several other revisions still assumed a nested `SKILL.md` becomes its own package. D-1, D-5, SPEC-2, SPEC-5, CODE-3, CODE-9, CODE-11, TEST-1C, TEST-4C, TEST-5, MV-1, DOC-2, and DOC-5 now follow the ancestor rule, which D2.2's "nested-package handling unchanged" also requires. No existing package changes its digest, and no existing test is inverted.
- The draft's SPEC-4 review treats a non-semver `metadata.version` as undeclared with a warning, while D-2, CODE-7, CODE-9, TEST-1D, and DOC-5 made it an error or a skipped package. All of them now follow SPEC-4. A reserved-form `metadata.version` stays an error through a dedicated lint check, and the version mismatch fires only for a `major.minor.patch` `metadata.version`.
- CODE-1 and CODE-2 each defined the auto-form helpers. `pkg/version/version_test.go` imports `pkg/manifest`, so the helpers live in `pkg/manifest` and `pkg/version` calls them. TEST-1A drops the `Pin.String` round trip, because CODE-1 adds no `Pin.String`.
- CODE-4's first phase ran on `deduped` before `resolveExtends`, which reads the separate `kept` copies. The version and description fix-up moved into the loader, which feeds both slices.
- `manifestBodyOf` in `cmd/podium-mcp/local_search.go` reads `ArtifactBytes`, so overlay BM25 would index the synthesized comment. CODE-4 now returns an empty body for the synthesized document.
- The draft's DOC-5 (diagrams) changed `registry-layout.svg` line 175, the `pay-invoice` row that SPEC-2 keeps as a both-files skill. The SVG draws no children for the skills SPEC-2 changes, so it needs no edit, and the `artifact-package.svg` edits moved into SPEC-1. The CHANGELOG deliverable is now DOC-5.
- The draft's SPEC-8 review dropped the retry bound from the spec while CODE-5 keeps one. SPEC-8 now states the observable outcome of an exhausted re-derivation without a number, and D-4 records why the bound is not operator-tunable.
- TEST-1, TEST-2, and TEST-4 each spanned several packages and checklist steps. They are split into TEST-1A through TEST-1D, TEST-2A and TEST-2B, and TEST-4A through TEST-4C, so each test deliverable lands in the commit of the code it covers.
- The draft's MV-1 step 4 read `podium artifact show --json`, which parses `frontmatter` into an object. S91 reads `/v1/load_artifact` directly to compare the exact bytes.

### Pass 1 (2026-11-09, automated)

- SPEC-10, the SPEC-4 synthesized-document paragraph, TEST-5, and the Testing section cited §7.6.3 (Delivery Verification) for `materialize()`. They now cite §7.6 Language SDKs, which holds the Python example at line 619 and the canonical-layout sentence at line 629.
- The Summary, D-6, and the CODE-6 test said a search descriptor carries the D-6 bytes, while `descriptorOf` passes every document through `frontmatterBlock`, which drops the body. The Summary, D-6, and CODE-6 now say a descriptor carries `---\ntype: skill\nversion: <version>\n---\n`, and the CODE-6 test asserts the D-6 bytes for the single and batch responses and that block for search.
- CODE-10 broke `TestScaffold_Skill_LintClean`, `TestScaffold_DomainHierarchyInPath`, `TestScaffold_ExtendsOnSkill`, `TestCLI_ScaffoldExtends`, and `TestGovernance_ScaffoldSensitivityHigh`, none of which was listed. CODE-10 now lists the unit-test edits and states that the refusal runs after `validateSensitivity`, so `TestGovernance_ScaffoldInvalidSensitivity` stays unchanged; TEST-4A lists the end-to-end edits, and S12 names both.
- MV-1 omitted S10, S18, S26, and S64, which edit or hash a scaffolded skill's `ARTIFACT.md`. MV-1 now edits each, states the search that found the set, and S15 points at MV-1's list.
- A both-files skill whose overlay omits `version` gave server-source sync an empty lock version and failed `podium-mcp` cache-served loads. New CODE-12 takes the lock version from the served `version` member and persists the served version and type in the MCP delivery files; S8 carries it, and TEST-3 and the CODE-12 tests pin both paths.
- TEST-3 placed an `extends:` child at its parent's canonical ID, which shadows the parent, and then asserted the parent's `0.0.0-auto.0` lock entry. TEST-3 now names its added packages at distinct IDs that no other layer contributes, and SPEC-11 describes that fixture.
- The server merged an `extends:` child whose overlay omits `version` into a document with `version: ""`, while the filesystem merged the resolved version. CODE-6's `mergeChain` now fills the merged version from the child row, and D-8, SPEC-9, and the edge-case row state that the `none` output of an `extends:` child whose version is assigned frames that version.
- S91 step 7 counted `skills/hidden` with `grep -c`, which matches both the `id` and the `materialized_path` lines. The step now matches the `id` line only.
- DOC-3's single-skill workaround registered the clone itself, whose `.git/` would ship as bundled resources. DOC-3 and the edge-case row now copy the skill without `.git/` and state why.
- Plain-skill rows took their served body, type, sensitivity, deprecation, replacement, and `audit_redact` keys from unverified columns, and §13.4 said ingest never stores an empty-manifest row. New SPEC-13 amends §13.4, CODE-6 makes `admitBytes` treat an empty manifest with a parsing `SKILL.md` as a skill through the `plainSkillRow` predicate that `servedFrontmatter` also uses, D-5 and the Summary state it, and an admission test tampers each column. S1 and D-14 include SPEC-13.
- A revert or a duplicate same-ID package gives two versions one content hash, which the §7.2 entity tag, MCP revalidation and delivery files, and `sha256:` pins do not distinguish. This needs a decision, recorded as Q-4 with its constraint, and the Summary and D-4 point at it.
- DOC-3 made a nested `ARTIFACT.md` directory a non-package. Its discovery sentence now separates the `ARTIFACT.md` and `SKILL.md` arms as §4.2 does.
- The removed-`ARTIFACT.md` conflict appeared on no docs page. DOC-2's "Skill versions" subsection and the DOC-5 `metadata.version` bullet now state it, and the edge-case row names DOC-2.
- DOC-2 said `materialize()` and `none` write `ARTIFACT.md` only when it is authored, which contradicted the byte-identical edge case. Both bullets now state the byte-equality rule.
- CODE-11 broke `TestImportCmd_HappyPath` and left the zero-root-`.md` outcome unstated. D-11 and CODE-11 now skip such a subdirectory, the test moves its fixture, DOC-4 and DOC-5 state the skip, and a unit case pins it.
- CODE-4's description fallback ran only for an assigned version. It now runs for every skill with no `ARTIFACT.md`, and the CODE-4 and TEST-3 cases cover a versioned plain-skill parent.
- CODE-3 said a symlink named `SKILL.md` is not a manifest while using `fs.Stat`, which follows it. CODE-3 now states that a symlink to a regular file is a manifest in both walkers, as today's predicates treat one, and TEST-1C pins it.
- The new `podium import` error and boundary paths had no tests. CODE-11 and TEST-4B now list cases for an invalid and a reserved `--version`, a non-mapping `metadata`, a non-string `metadata.version`, a byte-identical copy, and body preservation.
- No test covered auto-versioning across two layers of one ID. TEST-2A (e) gains a two-layer `assign` row and TEST-2B a two-layer ingest case.
- The §7.3.1 removed-`ARTIFACT.md` rejection had no test. TEST-2B now ingests a both-files skill, deletes `ARTIFACT.md`, and asserts the conflict and the unchanged `latest`.
- CODE-4's `manifestBodyOf` change had no test. CODE-4's tests now add rows to `TestManifestBodyOf_StripsFrontmatter`.
- A root-level `SKILL.md` reached the reingest endpoint unclassified and answered a retryable `registry.unavailable`. CODE-3 now wraps every `ValidateCanonicalID` error in the ingest walker with `invalidArtifact`, the edge-case rows and DOC-3 state `ingest.lint_failed`, and `test/integration/reingest_pipeline_test.go` asserts the envelope. The neighboring row for a frontmatter-less `SKILL.md` now names the same code, which the reingest endpoint returns for `ErrInvalidArtifact`.
- Correction: the plain-skill admission branch left the `Description` and `Tags` columns unadmitted. `parsedArtifact` falls back to both, `mergeChain` calls it on every admitted chain row, and `manifest.MergeExtends` keeps a parent's description and unions its tags, so an edited column of a plain-skill `extends:` parent reached the child's served and attested merged document. CODE-6 now builds the admitted artifact with the `SKILL.md` description, and `withAdmittedFields` gains a plain-skill branch that sets `Description` and clears `Tags`. The Summary, D-5, SPEC-13, the CODE-4 rationale, TEST-3, the edge-case row, and the Testing bullet state it, and the admission test tampers both columns and adds an `extends:` child of a tampered plain-skill parent.
- Correction: TEST-2B's two-layer case placed two plain skills at one ID, which the §4.6 collision check at `pkg/registry/ingest/ingest.go:746-764` rejects, so `team` never received `0.0.0-auto.1`. `team` now holds a same-ID overlay whose hand-written `ARTIFACT.md` declares `extends: <id>` and no `version`, as D-8 and SPEC-9 describe, and a further case asserts that a third layer's same-ID plain skill is rejected as a collision. Pass 2 moved that case to a store without `team`'s row.
- Correction: CODE-10's refusal said the flag list follows definition order, while its new unit test asserted a different order, and `fs.Visit` reports flags lexicographically. CODE-10 now states the definition order (`tags`, `sensitivity`, `when-to-use`, `extends`) and that the names are emitted from a fixed list, and the test asserts that order for flags passed in another order.
- Correction: TEST-3 said each lock carries one entry per ID, while `pkg/sync/sync.go:383-392` writes one entry per materialized file. TEST-3 now says each ID's entries come from one record and that a both-files package yields several entries under `none`.

### Pass 2 (2026-11-09, automated)

- TEST-2B's third-layer case expected a collision rejection that the §4.6 check does not produce once `team`'s overlay row exists, because the check also admits a record when an existing cross-layer row is an overlay of the ID (`pkg/registry/ingest/ingest.go:751-758`). The case now runs over a fresh store that holds only `base`'s row and asserts `ingest.collision`, and the bullet records that the broader admission predates this change and is out of scope.
- CODE-7 changed the outcome of `TestSkillTutorial_MissingVersion`, and DOC-1's fixture edits left `TestSkillTutorial_FullerFrontmatterLints`, `TestSkillTutorial_RuntimeRequirementsLints`, `TestSkillTutorial_EndStateLintsClean`, and the starting-point lint test with an unexpected `lint.skill_version_assigned` warning. TEST-1D now rewrites `TestSkillTutorial_MissingVersion` in the S5 commit, S5 lists the e2e level, and DOC-1 adds `metadata.version` to `greetSkillBodyFuller` and `greetSkillWithBody` and makes the starting-point test expect the warning.
- A version-less same-ID `extends:` overlay whose bytes changed pinned its own earlier row as parent, because `resolveExtendsPin` excluded only the child's row at an empty version. CODE-5 now excludes every row of the child's own layer for a same-ID reference and drops the `childVersion` parameter, with a resolver unit case and a TEST-2B case that changes `team`'s bytes and asserts the new pin and the served tags. S7 and the Summary's watch-out list name it.
- SPEC-4's field-allocation row required the `ARTIFACT.md` `version` to equal any `metadata.version`, while the Version paragraph and the lint list require it only for a `major.minor.patch` value. The row now states the narrower predicate.
- CODE-3's new root-level message dropped the word `subdirectory`, which existing walker, lint, and sync tests assert. CODE-3 now states the exact message, which keeps the clause and changes only the parenthetical, and lists those assertions as unchanged; TEST-1C asserts both substrings.
- The Q-4 gate sat before S7 and S8, while both options rewrite spec text that S1 commits. The watch-out bullet, Q-4, D-14, and the S1 checklist line now gate S1 on Q-4 and put the chosen option's spec edits in the S1 commit.
- MV-1 omitted S69, whose step 4 appends to the stored `frontmatter`, which is NULL for a scaffolded plain skill. MV-1 now edits S69 to tamper `skill_raw` instead, keeps every Expect block, and widens its search criterion to stored-column edits.
- No test pinned that a tampered `version` or `type` cache file fails the delivery check. CODE-12 now adds a `cache_mode_test.go` case for each file in `offline-only` and in `always-revalidate` with a HEAD match, and the Testing section names the error path.
- CODE-5's non-deleted filters on `latestByLayer` and `crossLayerByID` had no test. TEST-2A (e) gains an `assign` row for a soft-deleted same-hash row, and TEST-2B gains an unregister-and-reregister case and a soft-deleted cross-layer case.
- SPEC-10's §6.7 sentence said `none` writes every authored `ARTIFACT.md`, while CODE-8 and the edge-case table omit one byte-identical to the synthesized document. The §6.7 sentence, the §7.6 example comment, and the SPEC-4 synthesized-document paragraph now state the byte-equality rule.
- SPEC-6 said the synthesized document is never framed, while §4.7.10 frames it in the delivery record. The sentence now scopes the exclusion to the content hash and names the delivery-record framing, and D-5 and the Summary say "never enters the content hash" in place of "never hashed".
- Correction: TEST-2B's two-layer case changed `base`'s bytes before `team`'s, so `base`'s newer row was the `latest` candidate with or without the CODE-5 resolver change, and the case could not fail on a regression. The case now changes `team`'s bytes while `team`'s own `0.0.0-auto.1` row is the most recently ingested candidate, asserts the `<id>@0.0.0-auto.0` pin and the served tags, and names the pin a regression would store. Its layers are registered, and the Testing section lists the resolver unit cases.
- Correction: excluding the child's own layer left a higher-precedence layer's rows as candidates, so in a chain of `base`, `team`, and `top` a change to `team` pinned `top`'s overlay and the server merged a longer chain than `pkg/registry/filesystem/extends.go`. CODE-5 now restricts a same-ID reference's candidates to the rows of the nearest lower-precedence layer that holds the ID, in the order `ListLayerConfigs` returns, keeps the own-layer exclusion for an unregistered child layer, and adds unit cases for a higher layer's newer row, a nearer lower layer's older row, and unregistered layers. The Summary watch-out and S7 state the rule.
- Correction: the CODE-5 resolver change alters the stored pin and the served merged document of an existing same-ID overlay bumped in its own layer, and DOC-5 had no entry for it. DOC-5 now appends a `### Fixed` bullet.

### Pass 3 (2026-11-09, automated)

- SPEC-8's §7.3.1 row let the most recently ingested row of the layer supply a reused version without excluding soft-deleted rows, while D-4, CODE-5, TEST-2A (e), and TEST-2B reuse only the layer's latest non-deleted row. A store that followed the spec text would reuse a tombstoned `0.0.0-auto.0` after an unregister and re-register, and `PutManifest` would treat the existing key as an idempotent no-op (`pkg/store/memory.go:131-137`, `:524-538`), leaving the skill unserved. The row now says "most recently ingested non-deleted row" and adds "A version that only a soft-deleted row holds is never reused", the §11 insert adds "never reuses a soft-deleted row's version", and the Summary watch-out names the non-deleted filter.
- CODE-3's `PackageManifests` returned two booleans and discarded every `fs.Stat` error, so under `source.ConfinedFS` an `ARTIFACT.md` or a sole `SKILL.md` symlink that escapes the layer or has an absolute target would silently drop its package, where today the name-based detection reads the file and fails the layer with `ingest.source_unreachable` (§7.3.1 local-source confinement). `PackageManifests` now returns `(artifact, skill bool, err error)`, treats only `fs.ErrNotExist` as absent, and every caller (both walkers' directory visits and both resource captures' boundary checks) returns the error. TEST-1C adds a stub-`StatFS` predicate row and `source_confinement_test.go` cases for an escaping `ARTIFACT.md`, an escaping sole `SKILL.md`, an absolute-target sole `SKILL.md`, and a dangling sole `SKILL.md`; TEST-2B adds a reingest-endpoint case asserting HTTP 502 `ingest.source_unreachable`. The Testing section, the edge-case table, and the Summary watch-out list state it.

### Pass 4 (2026-11-09, automated)

- SPEC-12 redefined the glossary "Manifest" as a plain skill's `SKILL.md`, while §13.4 uses "manifest" for the `ARTIFACT.md` slot beside a separately listed `SKILL.md` (`spec/13-deployment.md:84`, `:90`). Under that definition a plain-skill row never has an empty manifest, so SPEC-13's "manifest is empty and whose `SKILL.md` parses" predicate held for no row, and both §13.4 digest recomputations framed `SKILL.md` in the first slot against SPEC-6's zero-length first frame. SPEC-12 now keeps the entry's current first sentence ("the `ARTIFACT.md` file specifically"), changes "additionally" to "also", and adds that a skill's `ARTIFACT.md` is optional and that a skill without one has no manifest on disk and an empty stored manifest (§13.4). The SPEC-12 rationale states why the term stays bound to `ARTIFACT.md`, and SPEC-13 states that the unchanged first-start rewrite and check (1) frame an empty stored manifest for such a row.
- TEST-5 item 2 covered no both-files SDK overlay package in which neither file declares a usable version, the branch where CODE-9 changes the reported version from the empty string (`sdks/podium-ts/src/overlay.ts:423`, `sdks/podium-py/podium/_overlay.py:339`) to `0.0.0-auto.0` under D-8. Item 2 now adds two cases in both SDK suites, an `ARTIFACT.md` with `type: skill` and no `version` beside a `SKILL.md` with no `metadata.version` and beside one with `metadata.version: "1.0"`, each loading with version `0.0.0-auto.0`, citing `§4.3.4, §6.4`.

### Pass 5 (2026-11-09, automated)

- No SDK test pinned CODE-9's rule that a `SKILL.md`-only overlay package serves the `SKILL.md` body as `manifest_body`. Both overlays pass the record's `body` through as `manifest_body` (`sdks/podium-ts/src/index.ts:1400`, `sdks/podium-py/podium/client.py:1428`) and parse it from `ARTIFACT.md` today (`sdks/podium-ts/src/overlay.ts:383`, `sdks/podium-py/podium/_overlay.py:308`), so an implementation that parsed the synthesized document would serve the pointer comment while every TEST-5 item still passed. TEST-5 item 1 now asserts, in both `resolution_overlay_login` suites, that the loaded artifact's `manifest_body` equals the `SKILL.md` body text and does not contain `Synthesized by Podium`, citing `§4.3.4, §6.4`.
- No CODE-11 unit case reached the `setSkillMetadataVersion` branch that inserts `version` into an existing `metadata` mapping and re-marshals a header holding other keys. CODE-11's `cmd/podium/import_test.go` list now adds a case whose source header carries `license: MIT`, an `allowed-tools` list, and `metadata` with `author: alice` and no `version`, and asserts the inserted `metadata.version`, the kept `metadata.author`, `license`, and `allowed-tools` values, and the unchanged body bytes. The case carries the list's `// Spec: §4.3.4, §4.7.6` citation.

### Redesign 1 (2026-11-09, automated)

- Proposal 0057 merged to `main` in `a56ae170` after this draft. Every citation into `pkg/registry/ingest/ingest.go`, `pkg/registry/filesystem/walk.go`, `pkg/registry/filesystem/extends.go`, `pkg/registry/core/core.go`, `pkg/registry/server/layers.go`, `cmd/podium/main.go`, `spec/04-artifact-model.md`, `docs/reference/cli.md`, and `test/manual-validation.md` is re-anchored against that commit.
- CODE-5 restricted a same-ID `extends:` reference to the nearest lower-precedence layer's rows. Proposal 0057's §4.6 text makes an earlier version stored from the overlay's own layer a same-layer parent (`spec/04-artifact-model.md:724`), its CHANGELOG entry says the same, and `TestIngest_ExecutableInheritanceSameLayerOverlayPinAccepted` pins it. The restriction therefore contradicted committed spec text, and no spec edit was staged for it. The resolver change, its unit cases, its TEST-2B assertion, and its DOC-5 `### Fixed` bullet are removed. An assigned-version overlay now pins as an authored one does, and higher-precedence selection is a non-goal.
- `Registry.Walk` returns from inside `if opts.ResolveExtends` through `composeExtends`, so CODE-4's synthesis point after that block no longer existed. CODE-4 synthesizes on the slice each path returns, after `composeExtends`. A plain-skill parent contributes an empty block to the §4.6 executable-field comparison in both modes, and two named tests pin it.
- The CODE-5 re-derivation loop runs proposal 0057's executable-field check once per record, before signing. TEST-2A (g) pins that a conflict on the assigned version does not bypass it.
- `mergeChain` folds through `ResolvedArtifact`, which ingest's executable-field check also calls. CODE-6 places the merged-version fix in `mergeChain`.
- Proposal 0057 added scenarios S91 through S95 on ports 8193 through 8197. The new scenario is S96 on port 8198.
- D-13 names the refusal constructions this change reuses: `layer.InvalidArtifactCode`, `RejectedArtifact.Details` from `layer.ExecutableInheritance`, `ingest.ErrInvalidArtifact`, and `layer.Drop`.
- Q-4 recorded that a revert, or a second layer's identical version-less bytes, gave two versions of one ID one content hash. D-4 now refuses a version-less skill whose bytes a stored or soft-deleted row of its ID holds, other than its layer's latest row, with `ingest.invalid_artifact` and `details.constraint: "auto_version_duplicate_content"`, and SPEC-6 states the resulting invariant in §4.7.6. The §7.2 entity tag, the §6.5 cache, the MCP delivery files, and `sha256:` pins therefore need no change. D-15 records the answers to Q-1 through Q-4. The Q-4 watch-out, the S1 entry criterion, and the Q-4 edge-case row are removed, and SPEC-8, CODE-5, TEST-2A, TEST-2B, S96 step 6, DOC-2, and DOC-5 state the refusal.
- CODE-5's `autoIndex`, its `record(mr)` update, and the `IsAutoVersion` reuse guard are replaced by `assignVersion` over one ID's rows. Each canonical ID occurs once per ingest call, and an equal hash already implies an assigned version.
- An unchanged reingest of an authored-version artifact after an unregister and a re-register reports an accepted artifact that stays tombstoned, and the memory store keeps the tombstoned rows of a re-registered layer. Both predate this change and are recorded as non-goals.
- The redesign deleted these mechanisms outright: the Q-4 watch-out, the S1 entry criterion and its `Depends on: Q-4`, the Q-4 clause of D-14, the Q-4 edge-case row, and the Q-1 through Q-4 open-question bodies; CODE-5's `autoIndex`, its `latestByLayer` map, the `record(mr)` update, and the `IsAutoVersion` reuse guard; the nearest-lower-layer resolver rule with its `ListLayerConfigs` read, its unregistered-layer path, the `childVersion` removal, the `extends_deprecated_internal_test.go` call-site edits, its five unit cases, its watch-out bullet, its S7 clause, its Testing bullet, its TEST-2B pin and dropped-tag assertions, and its DOC-5 `### Fixed` bullet; the TEST-2B assertions that a revert gets `0.0.0-auto.2` and that a declared-version interval yields a fresh N; the original S96 step 6 expectation of `0.0.0-auto.2` with the reverted body; and the CODE-4 instruction to synthesize on `deduped` after the `if opts.ResolveExtends` block. No Q-4 option (b) surface (§7.2 entity tag, §6.5 cache, MCP HEAD check, per-version delivery files, or `sha256:` selection) is staged.
- The redesign recorded two open decisions, and the proposal applies the default of each. OD-1 chooses between refusing a version-less revert to stored bytes (the default, D-4 and D-15) and issuing a fresh N with Q-4's option (b) surfaces. The relayed sign-off answer "1 and 2: go with your recommendations" may refer to Q-1 and Q-2 of this proposal, and Q-1's recommendation was the fresh N. The default therefore departs from that recommendation and needs the maintainer's explicit confirmation before S1. Choosing the fresh N reverts every edit that states the refusal and reopens Q-4. OD-2 chooses between leaving `resolveExtendsPin` unchanged, so an assigned-version overlay pins as an authored one does (the default), and excluding every own-layer row for an assigned-version same-ID reference, which adds a SPEC-8 bullet, three resolver unit cases, and a TEST-2B assertion.
- Reconciliation beyond the edit list: the Summary's Versions bullet now states the duplicate-content refusal and the §4.7.6 invariant it supports.
- The edits apply proposal 0057's §4.6 sentence on same-layer parents (`spec/04-artifact-model.md:724`) and the user's choice of 0.6.0 as the release vehicle (D-14).

### Pass 6 (2026-11-09, automated)

- DOC-2 edited `docs/authoring/frontmatter-reference.md` and `docs/reference/frontmatter-schema.md` only at their intro lines and lint list, while both pages carry tables that mirror the §4.3.4 allocation and mark `type` and `version` required in `ARTIFACT.md` for a skill (`frontmatter-reference.md:26-37`, `:79-83`; `frontmatter-schema.md:21-25`). DOC-2 now rewrites the "File allocation for skills" table to follow SPEC-4 (optional-overlay column, implied `type`, a separate `version` row), qualifies the `type` and `version` rows of both universal-field tables for a skill, rewrites `frontmatter-schema.md` line 100, and adds the D-2 precedence paragraph to `frontmatter-schema.md`, which the `metadata.version: "1.0"` edge-case row names. The bullet's reference to a lint-code list on `frontmatter-reference.md` is replaced, because the page has none.
- D-4 claimed that the §6.5 content cache and HEAD revalidation need no change because no two rows share a content hash, while a purge lets refused bytes return under a new N and the MCP server serves its cached `(content hash, ID)` record, with the purged version, on a HEAD match or a `304` (`cmd/podium-mcp/main.go:1380-1393`, `:1404-1416`, `:1898`, `:2686`). The proposal keeps the cache unchanged and accepts the outcome, because closing it would change the §7.2 entity tag or the §6.5 revalidation predicate for a case that needs a purge and a re-ingest of identical bytes. D-4 scopes the invariant to the rows stored at one time and states the cache outcome, D-15 and the Summary point to it, SPEC-8 adds the outcome to its §7.3.1 purge bullet and stages a §6.5 "Content cache" sentence, the purge edge-case row states it, and DOC-2's "Skill versions" subsection names it.
- DOC-2 declared `docs/getting-started/how-it-works.md` unchanged, while its "Versioning" section (lines 437-445) restates the §4.7.6 sentence SPEC-6 replaces. DOC-2 now rewrites that section's first sentence to name `SKILL.md` `metadata.version` and the registry-assigned `0.0.0-auto.<N>`, adds the exact-pin and range rule with a link to the "Skill versions" subsection, and keeps the stated reasons for leaving lines 105-106 and 129-131 unchanged.
- The two filesystem-source edge-case rows named `docs/deployment/local.md` (DOC-3), whose one added sentence covered only a higher N after changed bytes, while `local.md:160`, `single-node.md:138`, `deployment/index.md:17`, and `README.md:142-144` say output is bit-identical across tiers. DOC-3 now adds a `local.md` subsection, "Skills that declare no version", that states the lock-version, `none` version-line, same-ID overlay parent, and revert differences, and adds pointers to it from the bit-identical sentences on the other pages. The sentence after the `local.md` tree points to the subsection, the two edge-case rows name it, and S18 names the added scope.
- Correction to this pass: the DOC-2 `frontmatter-schema.md` sub-bullets and the first bullet of this pass cited line 41 and line 52, which hold the header row of the Caller-interpreted fields table and the blank line after `### Type-specific fields` in the `ARTIFACT.md` section. The sentence DOC-2 rewrites is at line 100, the second sentence of the `## SKILL.md (for type: skill)` intro, and the `SKILL.md` top-level fields table spans lines 102-111, before `### Body` at line 113. DOC-2 and this pass now cite those lines, so the D-2 precedence paragraph that the `metadata.version: "1.0"` edge-case row names lands in the `SKILL.md` section.
