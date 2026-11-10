# Proposal 0064: Remove extends: and make every same-ID collision between registry-side layers a rejection

- Issue: (to be filed)
- Status: Approved (2026-11-10). Verified on 2026-11-10 after 21 adversarial review rounds (50 findings fixed).
- Date: 2026-11-10

This document stages spec, code, test, and documentation changes. It does not modify any spec, code, test, or doc file. Apply the changes in the staged sections after sign-off. The change is backward-incompatible: ingest, a filesystem-source `podium sync`, and `podium lint` refuse an `ARTIFACT.md` that declares `extends:`, a same-ID overlay between registry-side layers is rejected, and a row an earlier release stored from such an `ARTIFACT.md` stops serving. It ships in 0.6.0, a MINOR release.

## Summary

**What changes.**

- **Spec.** §4.6 makes every same-ID collision between registry-side layers a rejection. It deletes inheritance together with the field-semantics table, the hidden-parent rules, the executable-field rule, and the sandbox-profile rule. A new no-inheritance rule refuses an `ARTIFACT.md` that declares `extends:` and retires a stored row whose `ARTIFACT.md` declares it. §4.2, §4.3, §4.7, §4.7.3, §4.7.6, and §4.7.10 lose their inheritance text, and the content-hash and delivery-record text names only the §4.3.4 synthesized document. §6.4 keeps serving an overlay artifact as authored, and §6.5 adds the retirement of an artifact's newest rows to the `podium cache reset-revisions` triggers, as §4.7.10 adds it to its list of honest `latest` decreases. §7.2 drops `extends_pin` and makes the entity tag the content hash. §7.3.1, including its registry-assigned-version reuse rule, and §7.5 follow, §13.4 admission loses the pin check and the chain walk, and §13.11.3 gains a removed-inheritance drop. The §1, §2.2, §9.1, §9.4, §10, §11, §13.10, and §13.12 statements follow as well (SPEC-1 to SPEC-6).
- **Go library and both deployment modes.** `pkg/manifest` deletes the merge, serialization, and executable-field code and adds the predicate `DeclaresExtends`. `pkg/layer` deletes the executable and sandbox rule types and `ExtendsOverlays`, adds `ExtendsRemovedConstraint`, `ExtendsRemovedReason`, and `ExtendsRemovedDrop`, rewrites the collision reason, and deletes the unused field-merge helpers `MostRestrictiveSensitivity` and `AppendUniqueTags`. `pkg/registry/ingest` refuses a declared `extends:` before lint, makes collisions unconditional, and deletes the extends pipeline. `pkg/registry/filesystem.Walk` refuses it before dedupe and loses its resolver, and `podium lint` collects the refusals through `OnDrop` so that it names every refused artifact and lints the rest. `pkg/registry/core` retires stored rows at its listing choke points and in the search vector ranker and deletes the chain walk, merge, and hidden-parent code, `pkg/registry/server` serves the bare content-hash ETag, `pkg/store` drops the pin from records and new schemas, removes the purge protection, and reads only the `delegates_to` and `mcpServers` edge kinds, and `pkg/sync` drops extends resolution (CODE-1 to CODE-8). The comments and the edge constant in `pkg/version`, `pkg/sign`, `pkg/dependency`, and `cmd/` follow (CODE-9).
- **CLI, SDKs, and web UI.** `podium artifact scaffold --extends` becomes a usage error, the SDK edge-kind type and docstring drop `extends`, and the web viewer drops its extends rail, `extends_pin`, the frontmatter panel's served note, and the extends collision copy, with `web/bundle` rebuilt (CODE-10 to CODE-12).
- **Fixtures, tests, and manual validation.** The reference registry loses its `extends:` children (FIX-1). Unit, integration, end-to-end, and conformance tests pin the refusal, the unconditional collision, the retired-row read paths, and the bare ETag, and the existing `extends:` tests are deleted or rewritten (TEST-1 to TEST-8). `test/manual-validation.md` deletes the extends-only scenarios, rewrites the scenarios that use `extends:` incidentally, and gains S102 (MV-1).
- **Docs, CHANGELOG, and proposals.** `docs/authoring/extends.md` and its diagram are deleted after their surviving content moves to `docs/deployment/layers.md`, and every page that mentions `extends:` is rewritten (DOC-1, DOC-2). The `[Unreleased]` CHANGELOG section is reconciled (DOC-3), and draft proposal 0062 is committed as withdrawn (PROP-1).

**Fixed decisions.**

- D-1: `extends:` is removed everywhere, with no flag, shim, dual path, or migration command.
- D-2: every same-ID collision between registry-side layers is rejected with `ingest.collision`; 0062 is withdrawn.
- D-3: one predicate, `manifest.DeclaresExtends`, over the top-level frontmatter key with YAML merge keys applied, and one constant set in `pkg/layer`.
- D-4: ingest refuses before lint with `ingest.invalid_artifact` and `details: {"constraint": "extends_removed"}`; no new §6.10 code, lint code, or matrix cell.
- D-5: `filesystem.Walk` refuses before the collision rule under every policy except `CollisionPolicyHighestWins`; `podium lint` collects the refusals through `OnDrop`, names every refused artifact, and lints the rest.
- D-6: the workspace overlay applies no ingest rule and serves an overlay artifact as authored.
- D-7: a stored row whose `ARTIFACT.md` declares `extends:` is retired; see D-7 for its treatment, D-7 *Exceptions* for where it differs from a soft-deleted row, and D-7 *Two-layer IDs* for an ID whose surviving rows two registry-side layers contributed.
- D-8: content hashes, delivery hashes of served rows, and signatures are unchanged; the ETag is the bare content hash; the `extends_pin` delivery vector stays.
- D-9: the `podium-mcp` cache gets no invalidation mechanism; see D-9 for the revision marks and the remedy.
- D-10: existing databases keep the unused `extends_pin` column; new databases omit it; reads filter edges by kind.
- D-11: `delegates_to:`, `mcpServers:`, `DOMAIN.md` composition, `replaced_by`, and overlay replacement stay.
- D-12: every artifact that exists only for `extends:` is removed.
- D-13: the content-hash and served-record split stays, rewritten around the §4.3.4 synthesized document.
- D-14: the CHANGELOG drops the unshipped 0057 and 0063 entries and keeps the surviving `OnDrop` API break.
- D-15: spec first, then the fixture step, then the Go code for both modes in one step, then the remaining code, tests, docs, and the CHANGELOG last.

The list above is an index. Each rule has exactly one normative statement, in its D-n, and one staged statement, in its SPEC or DOC deliverable. Every other mention cites the D-n without paraphrasing it.

**Watch out for.** These bullets name implementation traps that no decision states, and they point at the decision for every trap that one does.

- **The overlay walk must stay exempt.** `pkg/overlay` walks the workspace overlay with `CollisionPolicyHighestWins` and no `OnDrop` (`pkg/overlay/overlay.go:61-63`). A refusal that runs under every policy fails every overlay walk that meets an `extends` key, and `podium-mcp` then disables the whole overlay (`cmd/podium-mcp/main.go:843-847`). CODE-4 skips the refusal under `CollisionPolicyHighestWins` alone, which only the overlay uses outside tests, and TEST-3 pins both sides.
- See D-7 for the retired-row key, which reads the bytes and never the pin.
- **Decode the frontmatter block only.** `ManifestRecord.Frontmatter` holds the whole `ARTIFACT.md`, body included (`pkg/registry/ingest/ingest.go:1470`), and `servableRows` runs on every row of every listing, so CODE-1 splits before it decodes and CODE-5 memoizes the verdict per content hash (D-3).
- See D-7 and CODE-5 (d) for the vector ranker.
- See D-7 *Two-layer IDs* for an ID the wider 0.5.x server sanction left in two layers.
- See D-7 and SPEC-4 Edit 9 for the reuse candidate.
- See D-4 and CODE-3 for the refusal's place before lint and before `serverIdentifierIndex`.
- See D-5 and TEST-3 for the walk's refusal before dedupe.
- See D-7 for a retired row's edges.
- See D-10 and CODE-7 for the edge-kind filter.
- **Drop the purge protection with the column.** Its SQL names `child.extends_pin` (`pkg/store/sqlite.go:641-656`, `pkg/store/postgres.go:1061-1071`), so a fresh database without the column fails the purge until the protection is gone. Remove the column from `CREATE TABLE` and from the additive-column list in the same commit, or `TestSQLite_AdditiveColumnsMatchSchema` fails.
- See D-8 and TEST-5 for `test/vectors/`.
- See D-14 and DOC-3 for the surviving `OnDrop` API break in the 0057 entry.
- **Rebuild the web bundle.** `web/web.go` embeds `web/bundle`, and CI fails on a stale bundle (CODE-12).
- See PROP-1 for the untracked proposal 0062.
- **Test packages do not compile between S3 and S8.** See the ordering constraints under the checklist. CODE-1, CODE-3, CODE-6, and CODE-7 carry the compile-only test edits they list at S3, and TEST-8 carries every other compile-only test removal, including the `ExtendsPin` rows of `delivery_attestation_test.go`, `stored_row_admission_test.go`, `dependents_visibility_test.go`, and `internal/serverboot/store_retention_test.go`, so a later step never waits on another to compile. Deleted test files define helpers that surviving tests use (`exLoadResp`, `lcaWriteArtifact`, `lcaReingest`, `epFile`, `parentArtifact`, and `childArtifact`), and TEST-8 moves or replaces them in the same commit as the deletions. The TEST-8 grep gate finds the extends tests no deliverable names.
- See D-9 for the revision-mark refusal after a retirement.
- See the TEST-8 rationale for speccov-drift.
- **Parts of `test/e2e` skip silently on macOS.** Run the end-to-end steps with `-v` and grep the output for `SKIP` before claiming they passed.
- **Line numbers drift.** Anchor each edit by the quoted text, and treat cited line numbers as hints.
- See MV-1 S102 for the scenario and port numbers.

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1, SPEC-2, SPEC-3, SPEC-4, SPEC-5, SPEC-6. The §4.6 collision and no-inheritance rules and every dependent statement land in one commit (D-15), because each later edit cites the §4.6 rule and deleting §4.6 text alone leaves dangling references.
      Levels: unit. Depends on: —
- [ ] **S2 · test** — FIX-1. The reference registry drops its `extends:` children, and the equivalence and manifest-body tests that read them follow.
      Levels: integration, conformance, materialization. Depends on: S1
      Interleave: this test step precedes the code because each edit passes against the code before and after CODE-4. The S2 suites run against the old code at S2, and again at S8, the first step after S3 at which `test/integration` compiles.
- [ ] **S3 · code** — CODE-1, CODE-2, CODE-3, CODE-4, CODE-5, CODE-6, CODE-7, CODE-8. The Go removal for both deployment modes. Bundled because deleting `Artifact.Extends`, `ManifestRecord.ExtendsPin`, `layer.ExtendsOverlays`, and `WalkOptions.ResolveExtends` breaks compilation in every package these deliverables touch, and D-15 keeps the modes from diverging at a commit. CODE-1, CODE-3, CODE-6, and CODE-7 carry test edits, and those tests run from S8, the first step at which every test package compiles.
      Levels: unit, integration, conformance, e2e. Depends on: S2
- [ ] **S4 · code** — CODE-9. Comment cleanup in `pkg/version`, `pkg/sign`, and `cmd/`, and the deletion of `dependency.EdgeExtends`, with the `graph_test.go` edges that use it moved to the surviving kinds.
      Levels: unit. Depends on: S3
- [ ] **S5 · code** — CODE-10. `podium artifact scaffold` drops `--extends`.
      Levels: unit, e2e. Depends on: S3
- [ ] **S6 · code** — CODE-11. The SDK edge-kind type and docstring.
      Levels: unit. Depends on: S3
- [ ] **S7 · code** — CODE-12. The web viewer, the layer panel copy, the UI tests, and the rebuilt `web/bundle`.
      Levels: unit. Depends on: S3
- [ ] **S8 · test** — TEST-8. Delete or rewrite the existing `extends:` tests that S3 leaves uncompiled or wrong, including every compile-only removal that CODE-1, CODE-3, CODE-6, and CODE-7 do not carry at S3 and the doc-claim end-to-end tests, and move the shared helpers the deleted files define. The TEST-8 grep gate then lists only allowlisted files. Every test package compiles from this step.
      Levels: unit, integration, e2e. Depends on: S3, S4
- [ ] **S9 · test** — TEST-1. The predicate, constant, and collision-reason unit tests.
      Levels: unit. Depends on: S8
- [ ] **S10 · test** — TEST-3. The walk, sync, equivalence, and filesystem-sync end-to-end tests.
      Levels: unit, integration, materialization, e2e. Depends on: S8
- [ ] **S11 · test** — TEST-4. The retired-row read-path, dependents, and upgrade tests.
      Levels: unit, integration, e2e. Depends on: S8
- [ ] **S12 · test** — TEST-5. The response, ETag, and delivery tests.
      Levels: unit, integration. Depends on: S8
- [ ] **S13 · test** — TEST-2. The server ingest and reingest wire tests.
      Levels: integration. Depends on: S8
- [ ] **S14 · test** — TEST-6. The consumer overlay and `podium-mcp` fixture tests.
      Levels: unit. Depends on: S6, S8
- [ ] **S15 · test** — TEST-7. The end-to-end lint, scaffold, standalone, and impact-analysis tests.
      Levels: unit, e2e. Depends on: S5, S8 to S14
- [ ] **S16 · test** — MV-1. Scenario deletions and rewrites, and S102, in `test/manual-validation.md`.
      Levels: e2e. Depends on: S3 to S15
- [ ] **S17 · docs** — DOC-1, DOC-2. The extends page and diagram are deleted after their content moves, and every page and diagram that mentions `extends:` is rewritten. Bundled because DOC-1 re-homes content into `docs/deployment/layers.md`, which DOC-2 also edits, and one site build checks both.
      Levels: e2e. Depends on: S1, S15
- [ ] **S18 · docs** — PROP-1. Proposal 0062 is committed with a withdrawn status.
      Levels: —. Depends on: S1
- [ ] **S19 · docs** — DOC-3. The `[Unreleased]` CHANGELOG reconciliation.
      Levels: —. Depends on: S1 to S18

**Ordering constraints.**

- S1 lands first and passes `make coverage-gate` before any later step (D-15).
- S2 lands before S3, and it passes against the code before S3.
- S3 is one compile unit for the library code. After S3, `go build ./...` passes, but test packages that name a removed identifier do not compile. They compile again when S8 (TEST-8) lands, because CODE-1, CODE-3, CODE-6, and CODE-7 carry the compile-only test edits they list at S3 and TEST-8 carries every other compile-only test removal, and from S8 onward `go vet ./...` passes. S8 also reruns the S2 suites against the new code. A package that S9 to S13 edit also holds tests a later one of those steps rewrites, so each of S9 to S13 runs its levels over the tests it adds or rewrites, selected with `-run`. Whole-package runs of `pkg/registry/...`, `pkg/sync`, `internal/serverboot`, and `test/integration` pass once S13 lands, the combined coverage run executes in the closing step, and from S15 onward the full suite passes, because S15 depends on every step from S8 to S14. Push S3 to S15 together, so that no pushed head of the branch runs CI in a state that does not compile.
- S4 to S7 are independent of one another once S3 lands. S9 to S13 are independent of one another once S8 lands, because none of them waits on another to compile and each runs only its own tests.
- S17 follows S15 so that its doc-claim end-to-end run executes against the finished code and tests. TEST-8 rewrites those doc-claim tests at S8, so the full suite passes from S15, and S17 changes no test file.
- S19 describes the whole change and lands last. The closing step of the commands list runs after S19, over the finished branch. Land every step in one pull request.

## Current state and the gap

The maintainer has decided to remove `extends:` in 0.6.0, a MINOR release. Podium is pre-1.0, so a backward-incompatible change with no shim is acceptable (`.claude/rules/code-best-practices.md`, "Configuration and compatibility"). The proposal removes the feature from the spec, the Go code, both SDKs, the web UI, the docs, the fixtures, the tests, and `test/manual-validation.md`, and it leaves no dead surface. It also removes any other relationship that complicates content hashes, delivery hashes, entity tags, signing, or served bytes in the same way.

### extends: is the only cross-artifact relationship that changes served bytes

A child's frontmatter is folded with a parent that ingest resolves and hard-pins (§4.6, §4.7.6; `pkg/manifest/merge.go:52-64`; `pkg/registry/core/core.go:1874-1990`; `pkg/registry/filesystem/extends.go`). That one mechanism forces a long list of special cases:

- The content hash is defined over pre-merge bytes (§4.7.6).
- The delivery record frames the merged document (§4.7.10; `pkg/version/version.go:366-370`).
- The attestation disclaims merge fidelity (§4.7.10).
- A hidden parent is withheld on load, on search (`pkg/registry/core/core.go:1345-1374`, `FrontmatterHidingParent`), and on the object route (`pkg/registry/core/core.go:2095-2160`; §13.12).
- The per-caller `extends_pin` member exists, and it enters the ETag (§7.2; `pkg/registry/server/server.go:718-721`, `:1226-1243`).
- Stored-row admission has a pin check (2) and a chain walk (4) (§13.4; `pkg/registry/core/admit.go:181-219`, `:282-346`).
- Supporting rules exist for it alone: the executable-field rule (`executable_inheritance`), the sandbox-profile rule (`sandbox_profile_conflict`), and the license advisory `lint.license_changed_across_layers` (§4.6; `pkg/registry/ingest/ingest.go:705-712`).
- Ingest carries its edge cases: deprecated-candidate filtering (§4.6, §4.7.6), cycle and missing-parent rejections, and the public-mode floor re-run on the folded value (`pkg/registry/ingest/ingest.go:599-760`).
- Supporting code and data exist for it alone: the filesystem resolver and its recompose-and-cascade drops (§13.11.3; `pkg/registry/filesystem/walk.go:240-273`), the extends edge and its hidden-parent filter (§4.7.3; `pkg/registry/core/dependents.go:62-105`), the `extends_pin` store column and the purge protection for pinned parents (`pkg/store/sqlite.go:95`, `:641-656`; `pkg/store/postgres.go:86`, `:1061-1071`; `pkg/store/memory.go:304-311`; `pkg/store/schema_migrate.go:66`), the `--extends` scaffold flag (`cmd/podium/artifact_scaffold.go:48`, `:62`, `:97`, `:357`), the web UI extends rail (`web/ui/src/api.ts:294-298`; `web/ui/src/surfaces/ArtifactViewer.tsx:901-1035`), and draft proposal 0062.

### Same-ID shadowing exists only through extends:

§4.6 accepts a same-ID collision between registry-side layers only when the higher-precedence artifact declares `extends: <same id>` (`spec/04-artifact-model.md:690`). The filesystem walk implements that form: it accepts the higher-precedence copy when that copy's `extends:` names the ID (`pkg/registry/filesystem/walk.go:200-217`), and filesystem sync keys its drop on the same condition (§13.11.3). Server ingest is wider. It accepts the incoming artifact when the incoming record's `extends:` names the ID, or when any stored non-deleted row of the ID from another layer carries an `extends_pin` that names the ID, and it never compares layer precedence (`pkg/registry/ingest/ingest.go:761-779`; `pkg/layer/collision.go:40-48`; `pkg/registry/ingest/autoversion.go:53-68`). A plain copy from any further layer is therefore admitted once one overlay row of the ID is stored (`test/integration/plain_skill_ingest_test.go:439-443`), and so is a plain copy from a second layer once a layer's self-overlay row pins that layer's own earlier row (`spec/04-artifact-model.md:740`; `test/integration/plain_skill_ingest_test.go:396-400`). After the overlay rows retire (D-7), such a store holds servable rows of one ID from two registry-side layers. D-7 defines that outcome. Removing `extends:` makes `ingest.collision` unconditional. It deletes the same-ID overlay that proposal 0062 addresses and invalidates the remedy text "declare extends: X to overlay it" (`pkg/layer/collision.go:37`; §4.6; `CHANGELOG.md:865`). The workspace overlay is not a registry-side layer, and its consumer-side replacement (§6.4) does not depend on `extends:`.

### The other relationships stay

- `delegates_to:` and `mcpServers:` are stored as authored bytes. Their edges are stored pin-stripped, with no resolved version (`pkg/registry/ingest/ingest.go:1544-1581`; `pkg/store/store.go:250-254`). They enter the content hash only as authored text (`pkg/registry/ingest/ingest.go:1533-1541`), and they are absent from the ETag (`pkg/registry/server/server.go:1226-1243`) and from the delivery record (`pkg/version/version.go:419-433`). They change served bytes only through the extends fold, where `delegates_to` appends and `mcpServers` deep-merges (`pkg/manifest/merge.go:52-64`).
- §4.5.2 `DOMAIN.md` imports change domain listings only. No `DOMAIN.md` response carries an ETag (`pkg/registry/server/server.go:1156-1164` is the only one).
- `replaced_by` is a lifecycle field, excluded from the delivery record (§4.7.10).

### Served bytes still diverge from authored bytes without extends:

A skill with no `ARTIFACT.md` is served the §4.3.4 synthesized document, and its version may be `0.0.0-auto.<N>`, while its content hash frames a zero-length `ARTIFACT.md` (§4.3.4, §4.7.6, §4.7.10; `pkg/registry/core/served_frontmatter.go:43-53`; `pkg/registry/filesystem/walk.go:143-166`). The spec sentences that separate the content hash from the served record therefore stay, rewritten to name only that case. The `AuthoredBytes` field and the server-source lock `ContentHash` copy (`pkg/sync/sync.go:136-148`, `:430-448`) stay too.

### Stored rows need a defined outcome

A row stored by 0.5.x whose authored `ARTIFACT.md` declares `extends:` keeps its content hash and signature, because neither covers the merge or the pin (§4.7.6; `pkg/registry/core/admit.go:74-83`). Two facts make a defined outcome necessary:

- If the `Extends` field is deleted and nothing else changes, the lenient parser (`pkg/manifest/skill_fields.go:110`) drops the key. The row would then be admitted and served unmerged, and its raw frontmatter, which names the parent ID, would reach load responses, search descriptors, and the object route.
- Rows with an empty pin also exist, because of the column default (`pkg/store/schema_migrate.go:66`). The outcome therefore cannot key on the pin column.

### The podium-mcp cache gets no signal from the ETag change

The `podium-mcp` content cache is keyed by content hash alone and holds the merged frontmatter (`cmd/podium-mcp/main.go:1821-1836`, `:2826-2947`). Its revalidation compares only the content hash (`cmd/podium-mcp/main.go:1382-1393`, `:1405-1418`), so the ETag change alone gives it no invalidation signal. A 0.6.0 bridge already treats every record cached by an earlier release as a miss (`CHANGELOG.md:24`). The bridge also keeps a persisted revision mark per registry and artifact ID, the greatest ingest time it accepted for a `latest` load, and refuses a fresh `latest` answer below it with `materialize.stale_resolution` (§6.5; `cmd/podium-mcp/main.go:1775-1797`, `:1898-1906`). The marks have shipped since 0.5.0 (`CHANGELOG.md:180-187`), live in the `.resolutions` directory, and survive `podium cache prune` (`cmd/podium/cache.go:89-97`; `internal/revmark/revmark.go:29`). The SDKs keep no content cache.

### No §6.10 code exists only for extends:

The extends rejections reuse `ingest.invalid_artifact` (§4.6), which auto-versioning shares (§7.3.1). `ingest.collision` stays. Neither SDK reads `extends_pin`. Each uses `extends` only as an edge kind (`sdks/podium-ts/src/index.ts:788-792`, `:1523`; `sdks/podium-py/podium/client.py:1535-1540`; `sdks/podium-py/tests/test_client.py:323`, `:332`).

### CHANGELOG

Only the `[Unreleased]` section (`CHANGELOG.md:7-43`) has changed since v0.5.2. It carries the 0057 entry (`:16`) and the 0063 entry (`:17`), neither of which shipped, and the scaffold entry naming `--extends` (`:21`).

## Decisions

- **D-1. Removal.** `extends:` is removed everywhere: spec, `pkg/`, `cmd/`, `internal/`, both SDKs, `web/ui` (with `web/bundle` rebuilt), `docs/`, `testdata/`, the tests, and `test/manual-validation.md`. There is no compatibility flag, shim, dual code path, or migration command. The change ships in 0.6.0 as a MINOR bump.
- **D-2. Unconditional collision.** A same-ID collision between registry-side layers is rejected unconditionally with `ingest.collision`. At ingest the dropped artifact is the one being ingested. A filesystem-source sync drops the higher-precedence copy. The reason names the artifact and tells the author to rename it or to remove the other copy first, and it still names no layer. Workspace-overlay replacement (§6.4) is unchanged. Draft proposal 0062 is withdrawn as moot.
- **D-3. One predicate.** `manifest.DeclaresExtends(src []byte) bool` decides whether a manifest declares `extends:`. It is true when the `ARTIFACT.md` frontmatter, decoded as a generic YAML mapping with merge keys applied, carries the top-level key `extends`, whatever its value, for every type including extension types. **IMPLEMENTOR'S CHOICE:** whether `DeclaresExtends` takes any fast path before the `gopkg.in/yaml.v3` map decode — for every input, the predicate returns exactly what `SplitFrontmatter`, then `yaml.Unmarshal` of the frontmatter block into `map[string]yaml.Node`, then a lookup of the top-level key `extends` returns, and TEST-1's escaped, continuation, `!!binary`-tagged, explicit-key, UTF-16LE, and UTF-16BE true cases pass. The recommended default is no fast path, because CODE-5 (b) memoizes the verdict per content hash and `ParseArtifact` already decodes each record at ingest and in the walk. An implementor who adds a fast path also adds a property test that the fast path and the decode agree on every input. A nested `extends` key and a `SKILL.md` key do not match. One constant set in `pkg/layer` (`ExtendsRemovedConstraint = "extends_removed"`, `ExtendsRemovedReason`, and `ExtendsRemovedDrop`) serves both deployment modes.
- **D-4. Server refusal.** Server ingest refuses a declared `extends:` with `ingest.invalid_artifact` and `details: {"constraint": "extends_removed"}`, with no other `details` member. The check runs before lint and before every other per-artifact ingest rule, and the refused record is removed from the set that lint and the description advisories see. Two reasons drive the placement: a child that relied on inheritance often omits a required field and would otherwise be reported only as a lint count, and `podium lint` (D-5) refuses it before its rules run. An unchanged legacy child presented again is reported as dropped rather than unchanged. No new §6.10 code is added. `ingest.invalid_artifact` is not a §6.10 matrix cell (`tools/matrix/matrices.go:79-125`), so the change creates a `// Spec:` obligation and no `// Matrix:` obligation. No new lint code is added.
- **D-5. Filesystem refusal.** `filesystem.Walk` refuses a declared `extends:` under `CollisionPolicyDefault`, `CollisionPolicyError`, and `CollisionPolicyDrop`, before it applies the collision rule. Under every refusing policy it reports a refusal through `OnDrop` when `OnDrop` is set, and filesystem sync counts the drop like a collision drop for exit 1, `--dry-run`, `--config`, and `podium sync override`. A walk under `CollisionPolicyDefault` or `CollisionPolicyError` with `OnDrop` nil fails, naming the artifact, the code, and the reason, as a collision does today. `podium lint` walks under `CollisionPolicyDefault` with an `OnDrop` that collects the refusals, so it behaves like server ingest: it prints a `rejected: <id> (ingest.invalid_artifact): <reason>` line on standard error for every refused artifact, lints the remaining records, and exits 1 (CODE-4 step 8). That keeps the unchanged §7.3.1 sentence true, under which `podium lint` against the source names the artifacts behind a reingest's lint count (`spec/07-external-integration.md:159`), when the source also holds an `ARTIFACT.md` that declares `extends:`. A dropped record collides with nothing. That matches a server, which never stores it, so the two modes stay equivalent (§2.2, §11). Under `CollisionPolicyHighestWins`, the workspace overlay's policy, the walk applies no refusal (D-6).
- **D-6. Workspace overlay.** The workspace overlay applies no ingest rule. `podium-mcp` and `podium sync` (through `overlay.Filesystem`), the Python SDK, and the TypeScript SDK keep serving an overlay artifact as authored, including an `ARTIFACT.md` that carries an `extends` key, which no consumer reads. `filesystem.Walk` therefore skips the D-5 refusal under `CollisionPolicyHighestWins`, which only `pkg/overlay` uses outside tests. `podium lint` reports an `extends` key before the author promotes the artifact to a shared layer, and promotion subjects it to the §4.6 rules.
- **D-7. Retired rows.** A row stored by an earlier release whose stored `ARTIFACT.md` satisfies the D-3 predicate is a retired row. The check reads the bytes and never the pin, so empty-pin rows are covered. A retired row is treated as a soft-deleted row (§8.4; `pkg/store/store.go:141-147`; `pkg/registry/ingest/autoversion.go:53-112`) by the ingest collision check, by the duplicate-content rule, the N derivation, and the reuse candidate of the registry-assigned version (SPEC-4 Edit 9), and by every read path, including the vector ranker (CODE-5 (d)). Its `(id, version)` stays immutable. Edges carry no version (`pkg/registry/ingest/ingest.go:866-871`), so the reverse index and the ranking in-degree treat a retired row's edges as they treat a soft-deleted row's: `dependents_of` admits an edge while its source ID has a servable row (CODE-5 (c)), and the tenant-wide in-degree counts every stored edge of the §4.7.3 kinds (`pkg/store/store.go:446-451`). The registry rewrites, deletes, and re-signs no row, and no §13.4 first-start rewrite is added. The core listing choke points apply the read-path rule (CODE-5), so every read path that excludes a soft-deleted row also excludes a retired row, and the predicate's verdict is memoized per content hash (CODE-5 (b)).

  **Exceptions.** This list is the one statement of where a retired row differs from a soft-deleted row. Its three items are the three that SPEC-1 Edit 4 stages, and every other section cites this list. The readers in items 2 and 3 serve no artifact and call `ListManifests` directly, which excludes soft-deleted rows and returns retired ones (`pkg/store/sqlite.go:610-611`).
  1. No layer restore revives a retired row, because the rule keys on its bytes. A row copied by `migrate-to-standard` or restored with its layer stays retired.
  2. The §4.7.8 quota read and reembedding count it (`pkg/registry/core/quota.go:32-38`; `pkg/registry/core/reembed.go:68`). A retired row counts toward the storage usage the quota read reports (`pkg/registry/server/quota.go:34-39`), and reembedding keeps a vector for it, which the vector ranker never surfaces (CODE-5 (d)).
  3. Ingest's deprecation-flip test and artifact-count check read it, through `tenantHasNonDeprecatedVersion` and `tenantHasArtifact` (`pkg/registry/ingest/ingest.go:284-311`). A new deprecated version of an ID whose only earlier non-deprecated row is retired reports the §7.3.2 deprecation flip (`ingest.go:811`, `:826-829`), and a new version of an ID whose only row is retired does not count against `ArtifactCountQuota` as a new artifact (`ingest.go:652`).

  Because every read path treats a retired row as absent, `latest` resolves to the most recent other row of its ID, and an ID with no other row answers `registry.not_found` (Q-1).

  **Two-layer IDs.** The 0.5.x server sanction also admitted a plain copy from a further layer while an overlay row of the ID was stored (current state, "Same-ID shadowing exists only through extends:"). After retirement, the non-retired rows of such an ID come from two or more registry-side layers, and no rule links them. The registry keeps serving those rows, rewrites none of them, and adds no precedence rule for them:
  - Version resolution treats them as versions of one artifact, so `latest` is the most recently ingested of them whatever layer contributed it (`spec/04-artifact-model.md:924`; `pkg/registry/core/dependents.go:258-275`; `pkg/registry/core/core.go:1700-1715`).
  - Every later ingest of the ID from any of those layers is rejected with `ingest.collision`, including a re-ingest of that layer's unchanged bytes, because the collision check runs before idempotency (`pkg/registry/ingest/ingest.go:766-779`, `:784`). `podium layer reingest` of any of those layers therefore exits non-zero on every run (`cmd/podium/layer.go:589-601`), and the standalone boot reingest logs the rejection count (`internal/serverboot/serverboot.go:553`).
  - The remedy is the existing layer lifecycle. Unregistering all but one of those layers soft-deletes their rows (§8.4; `pkg/store/store.go:466-470`), the collision check skips a soft-deleted row (`pkg/registry/ingest/autoversion.go:58-68`), and read paths exclude it (`pkg/store/store.go:141-147`). Unregistering soft-deletes every row of the layer, so the layer's other artifacts stop serving too (`pkg/store/sqlite.go:920-923`). The soft-deleted rows keep their `(tenant, artifact_id, version)` keys, which name no layer (`pkg/store/sqlite.go:101`), until the §8.4 purge hard-deletes them at the end of the recovery window (`pkg/store/store.go:478-481`; `pkg/store/sqlite.go:980-997`). The operator therefore registers that source again, without the artifact, only after the purge. A registration before the purge, under any layer ID, does not serve the source's other artifacts. An artifact at an unchanged version is not stored, because `GetManifest` hides the tombstoned row (`pkg/store/sqlite.go:509-515`) and `PutManifest` leaves the tombstoned key in place and returns no error on an equal hash (`pkg/store/sqlite.go:323-363`), although ingest counts it accepted (`pkg/registry/ingest/ingest.go:959-993`, `:801-802`). A skill with no declared version is rejected with `auto_version_duplicate_content`, because the duplicate set includes soft-deleted rows (`pkg/registry/ingest/autoversion.go:75-87`, `:103-105`; `spec/07-external-integration.md:106`). `RestoreLayerConfig` revives the rows of the old layer ID only, the colliding row among them (`pkg/store/sqlite.go:948-951`), so it is no remedy. A layer the registry configuration or the standalone layer list declares is re-seeded at each start (`spec/07-external-integration.md:155`; `internal/serverboot/serverboot.go:524-546`), so the operator removes that declaration before unregistering the layer and declares the source again only after the purge.

  The filesystem walk holds no stored rows, so a filesystem-source sync of the same sources applies the D-2 collision rule and drops the higher-precedence copy. SPEC-1 Edit 4 states the outcome, and TEST-2 (l) pins it.
- **D-8. Hashes and validators.**
  - The content hash is unchanged for every stored row.
  - The delivery hash is unchanged for every row that is still served, because only extends children were merged and they are now retired.
  - The `load_artifact` ETag becomes the bare content-hash ETag on a full response, a HEAD, and a 304. `loadArtifactETag` and the `extends_pin` response member are deleted.
  - Signing is untouched: the signature covers the content hash alone.
  - The delivery-record vector "mistyped extends_pin is ignored" stays. It pins that a 0.6.0 consumer ignores the member a 0.5.x registry serves, under the unchanged §4.7.10 step-2 rule.
- **D-9. podium-mcp cache.** The `podium-mcp` cache gets no invalidation mechanism. A 0.6.0 bridge already misses every record cached by an earlier release (`CHANGELOG.md:24`). A bridge upgraded before its registry and running `offline-first` or `offline-only` keeps serving a merged body it cached from the 0.5.x registry. Retiring a row also lowers the ingest time an honest registry serves for `latest` whenever the retired row was the newest one, and D-7 and D-7 *Two-layer IDs* fix the row `latest` falls back to. A bridge of any release since 0.5.0 that accepted the retired row as `latest` holds a revision mark at that row's ingest time (§6.5), so it refuses every fresh `latest` answer for that ID with `materialize.stale_resolution`. That holds in every cache mode, including `always-revalidate`, where the HEAD hash mismatch leads to a full fetch that is then refused, and whatever the roll order, because the mark is persisted. The refusal is not retryable and survives restarts until `podium cache reset-revisions` deletes the mark, and `podium cache prune` deliberately leaves the marks in place (`cmd/podium/cache.go:89-97`). When every row of an ID is retired, the bridge reports `registry.not_found`. The remedy is the one §6.5 already prescribes for a lowered `latest`: stop every MCP server that uses the cache directory, run `podium cache reset-revisions <id>...` (or with no ID to clear every mark), and, for `offline-first` and `offline-only`, also run `podium cache prune --days 0` and refill an `offline-only` cache online once. SPEC-3 Edit 3 adds the retirement to the §6.5 reset-revisions triggers, and SPEC-2 Edit 18 adds it to the §4.7.10 list of honest decreases that defers to §6.5. The CHANGELOG states the roll order (registry first) and the remedy. This follows the 0063 D-7 precedent. The SDKs keep no content cache.
- **D-10. Store schema.** Existing databases keep the `extends_pin` column, unused. New databases do not create it, and the ADD COLUMN entry is removed, so the §13.4 additive-schema guarantee needs no spec change (Q-2). `ManifestRecord.ExtendsPin`, every INSERT and SELECT reference to the column, and the purge protection for pinned parents are deleted. `DependentsOf` and `DependencyInDegree` read only the `delegates_to` and `mcpServers` kinds, so a stored legacy `extends` edge is never returned or counted. No edge is deleted.
- **D-11. Relationships that stay.** `delegates_to:` and `mcpServers:` references keep their pin grammar. `DOMAIN.md` include, exclude, and imports stay, and so do `replaced_by` and workspace-overlay replacement. None of them enters a hash, ETag, signature, or delivery record except as authored bytes, and none rewrites served bytes. Only their field-semantics-table rows and the executable restatement rules are removed.
- **D-12. Artifacts that exist only for extends:.** These are removed:
  - The `executable_inheritance` constraint (with `details.fields`) and the `sandbox_profile_conflict` constraint.
  - The advisory `lint.license_changed_across_layers`.
  - `extends` in the `SKILL.md` Podium-only field list, so a `SKILL.md` `extends` key becomes an ordinary non-Podium key.
  - The `--extends` scaffold flag, whose use becomes a usage error with exit 2.
  - The `extends` edge kind, in the store comment, `pkg/dependency`, the TypeScript union, and the Python docstring.
  - The web UI's outbound extends chip, its "extended by" group, and the frontmatter panel's served note about a withheld `extends` key.
  - The §9.1 `TypeProvider` "field-merge semantics".
  No §6.10 code and no matrix cell exist only for `extends:`.
- **D-13. Synthesized-skill divergence.** The divergence stays. The §4.7.6, §4.7.10, §13.4, and §13.12 sentences that separate the content hash from the served record are rewritten to name only the §4.3.4 synthesized document and the registry-assigned version. The merge-fidelity disclaimer and the uniformity justification in §4.7.10 are deleted. The delivery signature is still served on every response.
- **D-14. CHANGELOG.** The `[Unreleased]` 0057 and 0063 entries (`CHANGELOG.md:16-17`) are deleted, and the scaffold entry (`:21`) drops `--extends`. A Removed entry and Changed entries describe the break relative to 0.5.2, and the `OnDrop` library change the 0057 entry recorded survives as its own entry. Released sections and implemented proposals 0057, 0058, and 0063 are left as history.
- **D-15. Order of work.** The spec edits land first, in one commit, and pass `make coverage-gate`. The fixture edits (FIX-1) follow, because they pass before and after the code. The Go code for both deployment modes (CODE-1 to CODE-8) lands as one step, so the modes never diverge at a commit. The comment cleanup, the scaffold flag, the SDKs, and the web UI follow as their own steps, because none of them affects the equivalence of the modes. Tests, manual validation, and docs follow, and the CHANGELOG lands last, all in one pull request. Run end-to-end steps with `-v` and grep for `SKIP`, because parts of `test/e2e` skip silently on macOS.

## Spec amendment: §4.6 Layers and Visibility, collisions and the removal of inheritance

**SPEC-1.** `spec/04-artifact-model.md`, §4.6. Four edits, all in one commit with SPEC-2 to SPEC-6 (D-15).

**Rationale.** `extends:` is the only thing that sanctions a same-ID collision between registry-side layers, and the merge table, the hidden-parent rules, the executable-field rule, and the sandbox-profile rule exist only for it (D-2, D-12). The rule must also define a fail-closed outcome for a declared `extends:` and for stored rows. The pin column cannot carry that outcome, because 0.5.x stored empty-pin rows too (D-7). The 0.5.x server also admitted plain same-ID copies from further layers under a stored overlay row, so Edit 4 states the outcome for an ID whose surviving rows two layers contributed (D-7 *Two-layer IDs*). The ordering guarantee is stated against per-artifact rules, because cycle-level failures such as `ingest.frozen`, quota exhaustion, and `ingest.source_unreachable` come first (§7.3.1).

**Edit 1, layer resolution paragraph.** Anchor: the paragraph that begins "A canonical-ID collision between registry-side layers (the admin-defined and user-defined layers above)".

Current text:

> A canonical-ID collision between registry-side layers (the admin-defined and user-defined layers above) follows the merge semantics for collisions below. The workspace local overlay follows §6.4. Resolution of layers 1 and 2 happens at the registry on every `load_domain`, `search_domains`, `search_artifacts`, and `load_artifact` call, and includes `extends:` resolution per the §9.1 `LayerComposer` row; layer 3 is merged in by the consumer that reads it (§6.4).

Replacement:

> A canonical-ID collision between registry-side layers (the admin-defined and user-defined layers above) follows the collision rule below. The workspace local overlay follows §6.4. Resolution of layers 1 and 2 happens at the registry on every `load_domain`, `search_domains`, `search_artifacts`, and `load_artifact` call; layer 3 is merged in by the consumer that reads it (§6.4).

**Edit 2, collision rule.** Anchor: the heading `### Merge semantics for collisions` and the text from it through the paragraph that ends "The workspace local overlay is an exception, and §6.4 states its collision rule." Replace the heading and that text, which includes the bullet list, the single-scalar paragraph, and the deprecated-parent paragraph, with:

> ### Collisions
>
> If two registry-side layers contribute artifacts with the same canonical ID, the collision is rejected. The rejected artifact is dropped with `ingest.collision` (§7.3.1), and the other artifact stays in the effective view. At ingest, the dropped artifact is the one the cycle is ingesting (§7.3.1). A filesystem-source `podium sync` drops the higher-precedence artifact (§13.11.3). The rejection reason names the artifact and directs the author to rename it or to remove the other copy first. It does not name the layer that already contributes the ID, because that layer may be one the caller cannot read (§4.7.2).
>
> To replace an artifact, the lower-precedence layer removes it first, or the higher-precedence one is renamed. Neither ingest nor a filesystem-source `podium sync` admits a silent shadow between registry-side layers, and "Manifests do not inherit" below states the outcome for rows an earlier release admitted. The workspace local overlay is an exception, and §6.4 states its collision rule.

**Edit 3, deletion.** Delete everything after the replaced text and before the heading `## 4.7 Registry as a Service`. The deletion covers the **Hidden parents** paragraph with its *Withheld*, *Served*, and *Observable* paragraphs, the resolution-disclosure paragraph, the `### Field semantics table` heading with its table, the **Omitted fields** paragraph, the sentence "Extension types register their own field semantics via `TypeProvider`.", the **Executable fields across layers** paragraphs, and the **Sandbox profile** paragraphs.

**Edit 4, new subsection.** Insert in the place of the deleted text, directly before `## 4.7 Registry as a Service`:

> ### Manifests do not inherit
>
> No artifact inherits frontmatter, prose, or resources from another artifact. An `ARTIFACT.md` whose frontmatter carries the top-level key `extends`, directly or through a YAML merge key and whatever its value, is refused. Ingest rejects it with `ingest.invalid_artifact` (§6.10) before lint and before every other per-artifact ingest rule (§7.3.1), so the rejection reports it whatever other rule it also fails. The rejection carries `details.constraint: "extends_removed"` and no other `details` member, and its reason names no layer. The rejected artifact is not stored, so it collides with no other artifact under the rule above. A filesystem-source `podium sync` drops it before it applies the collision rule (§13.11.3), and `podium lint` reports it as an error. An `extends` key in a skill's `SKILL.md` is not a Podium field (§4.3.4), and this rule does not refuse it. The workspace local overlay applies no ingest rule (§6.4).
>
> A stored row whose `ARTIFACT.md` carries that key is served by no read path. The ingest collision check, the registry-assigned-version rules (§7.3.1), and every read path treat it as a soft-deleted row (§8.4). Unlike a soft-deleted row, it is not served again when its layer is restored, the §4.7.8 quota read counts it toward the tenant's storage usage, and ingest counts it as a stored earlier version of its ID when it decides whether a new version flipped `deprecated: true` (§7.3.2). Such a row is not a collision under the rule above. Its `(artifact_id, version)` stays immutable, and §7.3.1 states its place in the registry-assigned version. Its `delegates_to:` and `mcpServers:` edges stay recorded by artifact ID. §4.7.3 states when the reverse index admits them, and the §4.7.3 search ranking signal, which counts dependents across the tenant, counts them. This rule rewrites and deletes no stored row.
>
> An earlier release admitted a same-ID artifact from another registry-side layer while a stored row of that ID declared `extends:` on it. Once this rule retires those rows, the rows of such an ID that it does not retire can come from two or more registry-side layers. Those rows are served. Version resolution treats them as versions of one artifact (§4.7.6), so `latest` is the most recently ingested of them, whichever layer contributed it. Each later ingest of that ID from any of those layers is rejected with `ingest.collision` under the rule above, including a re-ingest of that layer's unchanged artifact. Unregistering all but one of those layers soft-deletes their rows (§7.3.1, §8.4), after which the remaining layer is the only contributor of the ID.

No other §4.6 text changes.

## Spec amendment: §4.2, §4.3, and §4.7 subsections, references to inheritance

**SPEC-2.** `spec/04-artifact-model.md`. Each sentence below exists only for the extends fold or the pin, except the second replacement of Edit 18, which adds the upgrade retirement to the §4.7.10 list of honest `latest` decreases to match SPEC-3 Edit 3. The divergence the §4.3.4 synthesized document causes remains, so the split between the content hash and the served record survives with `extends:` removed from its wording (D-13).

**Edit 1, §4.2 canonical ID.** Anchor: the paragraph that begins "The registry's authoring layout is a domain hierarchy."

Current text:

> All references (`extends:`, `delegates_to:`, glob patterns) use this ID, optionally suffixed with `@<semver>` or `@sha256:<hash>`.

Replacement:

> All references (`delegates_to:`, glob patterns) use this ID, optionally suffixed with `@<semver>` or `@sha256:<hash>`.

**Edit 2, §4.3 frontmatter example.** Anchor: the YAML example block of §4.3. Delete these two lines and the blank line before them:

> ```yaml
> # Inheritance — explicitly extend another artifact's manifest (cross-layer merge)
> extends: finance/ap/pay-invoice@1.2.x
> ```

**Edit 3, §4.3.4 manifest files.** Anchor: the paragraph that begins "**Manifest files.**". Delete its last sentence:

> An `extends:` child carries an `ARTIFACT.md`; the parent it names may be a skill without one.

**Edit 4, §4.3.4 field placement table.** Anchor: the row that begins `| `mcpServers`, `requiresApproval`, `runtime_requirements`, `sandbox_profile``. Remove `` `extends`, `` from the field list, so the row reads:

> | `mcpServers`, `requiresApproval`, `runtime_requirements`, `sandbox_profile`, `effort_hint`, `model_class_hint`, `sbom`, `target_harnesses`, `external_resources` | Not present | Top-level |

**Edit 5, §4.7 projection.** Anchor: the paragraph that begins "The projection is built from the artifact's §4.6-resolved frontmatter."

Current text:

> The projection is built from the artifact's §4.6-resolved frontmatter. When a manifest declares `extends:`, the chain is folded per the §4.6 field-semantics table before the projection is taken, so an artifact is indexed under the same `description` and `tags` that `load_artifact` and `search_artifacts` serve for it.

Replacement:

> The projection is built from the frontmatter the registry serves for the artifact, so an artifact is indexed under the same `description` and `tags` that `load_artifact` and `search_artifacts` serve for it.

The sentence that begins "The requirement is over `description` and `tags`;" is unchanged.

**Edit 6, §4.7.3 edge list.** Delete the bullet:

> - `extends:` chains

**Edit 7, §4.7.3 visibility.** Anchor: the paragraph that begins "**Visibility.** Edge queries against the reverse index are filtered per caller." Delete the sentence:

> For an `extends:` edge the endpoint tested is the parent record the child pinned at ingest (§4.7.6), so a child that overlays a lower-precedence layer's artifact under the same canonical ID (§4.6) yields no edge to a caller who cannot see that layer.

In its place, insert:

> The index admits no edge whose source ID has no row other than rows the §4.6 no-inheritance rule retires.

The sentence on the search ranking signal is unchanged.

**Edit 8, §4.7.6 first framed value.** Anchor: the paragraph that begins "The first framed value is the artifact's own `ARTIFACT.md` as ingested".

Current text:

> The first framed value is the artifact's own `ARTIFACT.md` as ingested, before any `extends:` merge (§4.6). A merged serialization does not reproduce the digest, so any party that recomputes the digest for an artifact declaring `extends:` recomputes over the pre-merge bytes.

Replacement:

> The first framed value is the artifact's own `ARTIFACT.md` as ingested.

**Edit 9, §4.7.6 recomputation.** Anchor: the paragraph that begins "The registry computes the digest at ingest".

Current text:

> and a filesystem-source sync computes it over the same pre-merge bytes.

Replacement:

> and a filesystem-source sync computes it over the same authored bytes.

**Edit 10, §4.7.6 pinning syntax.**

Current text:

> Pinning syntax in references (`extends:`, `delegates_to:`, `mcpServers:`):

Replacement:

> Pinning syntax in references (`delegates_to:`, `mcpServers:`):

**Edit 11, §4.7.6 latest.** Anchor: the paragraph that begins "`load_artifact(<id>)` resolves to `latest`". Append:

> §4.6 states which stored rows are not candidates for resolution.

**Edit 12, §4.7.6 inheritance paragraphs.** Delete the paragraph that begins "**Inheritance and re-ingest.**" and the paragraph that begins "**Deprecated candidates in an `extends:` reference.**".

**Edit 13, §4.7.10 opening.** Anchor: the paragraph that begins "The `content_hash` (§4.7.6) names the stored artifact."

Current text:

> The `content_hash` (§4.7.6) names the stored artifact. For a child declaring `extends:` the registry serves a merged re-serialization (§4.6) whose bytes do not reproduce that digest, so the record a consumer materializes is not the record the content hash names. The delivery attestation binds the served record instead.

Replacement:

> The `content_hash` (§4.7.6) names the stored artifact. For a skill with no `ARTIFACT.md`, the registry serves the synthesized document of §4.3.4, which names the skill's version, including a registry-assigned one, and which the content hash does not frame, so the record a consumer materializes is not the record the content hash names. The delivery attestation binds the served record instead.

**Edit 14, §4.7.10 delivery record.** Anchor: the paragraph that begins "The **delivery record** is".

Current text:

> the served `ARTIFACT.md` document for every type including a skill, which for a child declaring `extends:` is the merged document (§4.6) and for a skill with no `ARTIFACT.md` is the synthesized document of §4.3.4 rather than a zero-length value; the served manifest body; the served `SKILL.md`, which for a skill is the child's own; and,

Replacement:

> the served `ARTIFACT.md` document for every type including a skill, which for a skill with no `ARTIFACT.md` is the synthesized document of §4.3.4 rather than a zero-length value; the served manifest body; the served `SKILL.md`; and,

**Edit 15, §4.7.10 exclusions.** Anchor: the paragraph that begins "The serialization carries a resource's content hash rather than its body".

Current text:

> The lifecycle fields (§4.7.4), the contributing layer, and the per-caller `extends_pin` reference are excluded: the first change after ingest, and the last is served per requesting identity, so including either would make the digest depend on when or by whom the record was read.

Replacement:

> The lifecycle fields (§4.7.4) and the contributing layer are excluded: the first change after ingest, and a §7.6.2 batch entry does not carry the second, so including either would make the digest depend on when the record was read or on which path served it.

**Edit 16, §4.7.10 uniformity.** Anchor: the paragraph that begins "The registry computes the delivery hash per response".

Current text:

> It does so for every `load_artifact` response and every batch entry, whether or not the artifact declares `extends:`. The uniformity is required: an attestation served only for a merged record would make its presence a disclosure of the merge, which §4.6 forbids.

Replacement:

> It does so for every `load_artifact` response and every batch entry.

**Edit 17, §4.7.10 step 2.** Anchor: step 2, *Members*, of the verification procedure.

Current text:

> and it ignores every other member, including `extends_pin`, `layer`, and a link object's `size` and `content_type`.

Replacement:

> and it ignores every other member, including `layer` and a link object's `size` and `content_type`.

**Edit 18, §4.7.10 scope of the attestation.** Anchor: the paragraph that begins "The attestation states that the registry composed and served these bytes."

Current text:

> The attestation states that the registry composed and served these bytes. It does not state that the merge is faithful. A consumer that cannot see a hidden parent cannot obtain it and therefore cannot check that the served manifest is the §4.6 fold of the child and its pinned ancestors, so a registry that folds the wrong parent, folds a parent the child never named, or fabricates the merged document produces a delivery record that verifies. Between the registry's response and the consumer's disk,

Replacement:

> The attestation states that the registry composed and served these bytes. Between the registry's response and the consumer's disk,

In the same paragraph, the sentence that begins "An honest registry serves a `latest` ingest time below one it served earlier" lists the honest decreases, and the sentence after it says "§6.5 states which of these the MCP server refuses and the remedy for each." SPEC-3 Edit 3 adds the upgrade retirement to the §6.5 trigger list, so this list gains the same case and the two lists stay equal.

Current text:

> after a purge or erasure removes the newest versions (§8.4, §8.5), when the caller's view loses them,

Replacement:

> after a purge or erasure removes the newest versions (§8.4, §8.5), after an upgrade retires them under the §4.6 no-inheritance rule, when the caller's view loses them,

No other sentence of that paragraph changes.

## Spec amendment: §6.4 Workspace Local Overlay collisions and §6.5 revision-mark resets

**SPEC-3.** `spec/06-mcp-server.md`. Sentence replacements in the §6.4 paragraph that begins "**Collisions with registry-side artifacts.**" (Edits 1 and 2) and in the §6.5 paragraph that begins "`podium cache reset-revisions` deletes the marks" (Edit 3).

**Rationale.** The §6.4 sentence names `extends:` twice, so it has to change. The overlay keeps serving every artifact as authored. No consumer reads the key, the overlay is never ingested or shared, and an omission rule would need the SDK hand parsers to match the Go predicate, which they cannot do without a YAML library (D-6).

**Edit 1, §6.4 overlay artifacts.** Current text:

> This holds whether or not the overlay artifact declares `extends:`: the consumer resolves no `extends:` chain for an overlay artifact and serves it as authored.

Replacement:

> The consumer applies no ingest rule to an overlay artifact and serves it as authored.

**Edit 2, §6.4 promotion.** Current text:

> Promoting the artifact to a shared layer subjects it to the §4.6 rule, including `extends:` resolution.

Replacement:

> Promoting the artifact to a shared layer subjects it to the §4.6 rules, and `podium lint` reports an `ARTIFACT.md` that the §4.6 no-inheritance rule refuses.

`podium lint` walks one local directory under `CollisionPolicyDefault` and contacts no registry (`cmd/podium/main.go:1054-1089`), so it cannot report a collision with another layer's stored rows, the public-mode sensitivity floor, an immutability violation, or a quota (§7.3.1). The replacement claims only the no-inheritance refusal, which the walk performs (CODE-4).

**Edit 3, §6.5 reset-revisions triggers.** Retiring the newest rows of an artifact lowers the ingest time an honest registry serves for `latest`, which is the condition the trigger list names (D-9). SPEC-2 Edit 18 adds the same case to the §4.7.10 list of honest decreases, which defers to this paragraph for the remedy. Current text:

> after a purge or erasure removes the newest versions of an artifact, after the caller's view loses them,

Replacement:

> after a purge or erasure removes the newest versions of an artifact, after an upgrade retires them under the §4.6 no-inheritance rule, after the caller's view loses them,

The rest of the paragraph is unchanged.

## Spec amendment: §7.2 response members and entity tag, §7.3.1 errors, ingest outcome, and registry-assigned version, and §7.5 sync

**SPEC-4.** `spec/07-external-integration.md`. `extends_pin` and its ETag folding exist only for `extends:`, and the ingest drop list names the removed rules. The registry-assigned-version reuse rule must also leave a retired row out of its reuse candidate (Edit 9). The new drop is listed with its `details` member, and the lint-ordering consequence is stated without naming a JSON member the spec does not define.

**Edit 1, §7.2 other response members.** Anchor: the paragraph that begins "**Other response members.**".

Current text:

> among them `layer`, a string naming the layer the served version comes from and absent when the registry records none, the `extends_pin` string described below, and a link object's `size`,

Replacement:

> among them `layer`, a string naming the layer the served version comes from and absent when the registry records none, and a link object's `size`,

**Edit 2, §7.2 heading paragraph.** Current text:

> **Integrity and reference fields.** The registry's HTTP `load_artifact` response carries these fields beside the manifest and the resources:

Replacement:

> **Integrity fields.** The registry's HTTP `load_artifact` response carries these fields beside the manifest and the resources:

**Edit 3, §7.2 `extends_pin` bullet.** Delete the bullet that begins "- `extends_pin`: the pinned `<id>@<version>` this artifact extends".

**Edit 4, §7.2 entity tag.** Anchor: the paragraph that begins "These are fields of the HTTP response."

Current text:

> The entity tag the registry publishes for a `load_artifact` of an `(id, version)` is computed from the content hash and the `extends_pin` value the requesting identity is served, on a full response, on a HEAD, and on a 304 alike, so a response that carries `extends_pin` publishes an entity tag that differs from the one the same `(id, version)` publishes without it, and a conditional request cannot revalidate a cached body whose `extends_pin` value differs from the one the requesting identity is served. The artifact revision does not enter the entity tag.

Replacement:

> The entity tag the registry publishes for a `load_artifact` of an `(id, version)` is the strong entity tag whose opaque value is the content hash (`"<content_hash>"`), on a full response, on a HEAD, and on a 304 alike. The artifact revision does not enter the entity tag.

**Edit 5, §7.3.1 errors.** Anchor: the paragraph that begins "**Errors.**".

Current text:

> cross-layer collisions without a sanctioning `extends:` (`ingest.collision`, §4.6),

Replacement:

> cross-layer collisions (`ingest.collision`, §4.6),

**Edit 6, §7.3.1 drop list.** Anchor: the paragraph that begins "**Ingest outcome.**".

Current text:

> a cross-layer collision (`ingest.collision`) and an `extends:` chain that crosses types (§4.6), a child that does not restate an executable field its parent in another layer sets (`ingest.invalid_artifact`, §4.6), a child whose `sandbox_profile` the §4.6 sandbox-profile rule refuses (`ingest.invalid_artifact`, §4.6), an unresolved `extends` pin (`ingest.invalid_artifact`), a skill whose registry-assigned version

Replacement:

> a cross-layer collision (`ingest.collision`, §4.6), an `ARTIFACT.md` that declares `extends:` (`ingest.invalid_artifact`, §4.6), a skill whose registry-assigned version

**Edit 7, §7.3.1 details sentence.** In the same paragraph.

Current text:

> and with a `details` object when the rule that rejected it defines one, as the §4.6 executable-field and sandbox-profile rules and the registry-assigned-version rule do.

Replacement:

> and with a `details` object when the rule that rejected it defines one, as the §4.6 no-inheritance rule and the registry-assigned-version rule do. The §4.6 no-inheritance rule is applied before lint, so the response reports an artifact that declares `extends:` among the cycle's rejections, with its code, reason, and `details`, and never counts it as a lint failure. An artifact that rule refuses is withheld from the cycle's lint, so the §4.4 prose-reference check does not resolve a reference to it from another artifact of the same cycle.

The rest of the paragraph is unchanged.

**Edit 8, §7.5 effective view.** Anchor: the paragraph that begins "The sync command reads the caller's effective view".

Current text:

> (the composed layer list after visibility filtering and `extends:` resolution)

Replacement:

> (the composed layer list after visibility filtering)

**Edit 9, §7.3.1 registry-assigned version.** Anchor: the version-table row that begins "| Skill with no declared version".

The unchanged row reuses the version of the most recently ingested non-deleted own-layer row. A retired row is not deleted, so without this edit a retired `0.0.0-auto.1` that adds `extends:` would be the reuse candidate when an author reverts to the served `auto.0` bytes, and the row would then reject the revert as `auto_version_duplicate_content`. The edit makes the spec state the D-7 split that CODE-3 (a) and TEST-2 (h) implement: a retired row never supplies a reused version, and it stays in the duplicate and N clauses.

Current text:

> When the most recently ingested non-deleted row this layer contributed for the artifact, at any version, carries an equal content hash,

Replacement:

> When the most recently ingested non-deleted row this layer contributed for the artifact, at any version, other than a row the §4.6 no-inheritance rule retires, carries an equal content hash,

The rest of the row is unchanged.

## Spec amendment: §13.4 stored-row admission, §13.10 artifact viewer, §13.11.3 filesystem source, and §13.12 object route

**SPEC-5.** `spec/13-deployment.md`.

**Rationale.** Admission check (2), the chain walk in check (4), and the per-identity validator exist only for `extends:`. The sync executable-inheritance and sandbox paragraphs, including the recompose-and-cascade loop, have nothing left to govern. The object route's ownerless pre-merge document disappears, and the viewer has no extending artifact to link. On a first ingest, the only way two layers store rows under one ID was the same-ID overlay, so the §13.11.3 clause about a higher N for several contributing layers no longer describes a reachable case.

**Edit 1, §13.4 admitted rows.** Anchor: the paragraph that begins "Before the registry serves an artifact's manifest, `SKILL.md`, or bundled resources".

Current text:

> it admits every stored row the served record is built from: the requested row and, for an artifact that declares `extends:`, each row of its chain.

Replacement:

> it admits the stored row the served record is built from.

**Edit 2, §13.4 check (2).** In the same paragraph, delete the text from "(2) It checks the row's stored parent pin" through "A failed pin is refused with `materialize.content_hash_mismatch`." Renumber the signature check from "(3)" to "(2)", and change "(1) through (3)" nowhere else, because the sentence that carries it is deleted by Edit 3.

**Edit 3, §13.4 check (4) and the chain walk.** In the same paragraph, replace the text from "(4) Admission follows a row's pin" through "A load whose chain holds a refused row is refused with that row's code, and the error names only the requested artifact." with:

> A row is refused with the code of the first check it fails.

**Edit 4, §13.4 served values.** Current text:

> The registry serves the body, type, sensitivity, deprecation flag, replacement, and `audit_redact` key set a load returns from the admitted bytes, and for an `extends:` child from the §4.6 merge over the admitted rows.

Replacement:

> The registry serves the body, type, sensitivity, deprecation flag, replacement, and `audit_redact` key set a load returns from the admitted bytes.

**Edit 5, §13.4 plain skill.** Delete the sentence:

> The §4.6 merge for an `extends:` child of such a row takes these values for the row.

**Edit 6, §13.4 validator.** Current text:

> and a conditional `load_artifact` whose validator matches the one the registry publishes to the requesting identity for the resolved row (§7.2),

Replacement:

> and a conditional `load_artifact` whose validator matches the one the registry publishes for the resolved row (§7.2),

**Edit 7, §13.4 pin choice.** Delete the last two sentences of the paragraph:

> A pin moved to another stored version of the declared parent that still satisfies an unpinned, `latest`, minor, or major reference is a choice of the same kind, because ingest resolves such a reference against the versions stored at ingest time and the bytes do not record which version it chose. An author who needs the parent fixed pins it by exact version or by content hash.

**Edit 8, §13.10 artifact viewer.** Current text:

> - **Artifact viewer**: manifest body rendered as markdown, frontmatter as a property table, links to extending or dependent artifacts.

Replacement:

> - **Artifact viewer**: manifest body rendered as markdown, frontmatter as a property table, links to dependent artifacts (§4.7.3).

**Edit 9, §13.11.3 shared library.** Anchor: the paragraph that begins "The composer, parsers, glob resolver,".

Current text:

> The composer, parsers, glob resolver, `extends:` resolver, and harness adapters used here

Replacement:

> The composer, parsers, glob resolver, and harness adapters used here

**Edit 10, §13.11.3 skills without a declared version.** Replace the paragraph that begins "**Skills without a declared version.**" with:

> **Skills without a declared version.** A filesystem source keeps no ingest history. It records a skill that declares no usable version (§4.3.4) as `0.0.0-auto.0` in the lock file. A registry ingesting the same directory for the first time assigns the same value. It assigns a higher N once it has ingested changed bytes for the skill, and the lock-file `version` entries of the two sources then differ. While the registry serves the directory's current bytes as the skill's latest row, the content hash and the output of every harness adapter stay identical. A registry rejects a change of such a skill back to bytes a stored row holds under another version (§7.3.1) and keeps serving its latest row, while a filesystem source, which keeps no history, serves the reverted bytes as `0.0.0-auto.0`. After such a revert, the content hash, the body, and the output of every harness adapter differ between the two sources until the author declares `metadata.version` or changes the content.

**Edit 11, §13.11.3 layer collisions.** Anchor: the paragraph that begins "**Layer collisions.**".

Current text:

> When the higher-precedence artifact declares no `extends:` on that ID, sync drops it and keeps the lower-precedence artifact, which is the artifact a server started fresh with `--layer-path` on the same directory serves. A dropped artifact takes no part in `extends:` resolution.

Replacement:

> Sync drops the higher-precedence artifact and keeps the lower-precedence artifact, which is the artifact a server started fresh with `--layer-path` on the same directory serves.

**Edit 12, §13.11.3 inheritance paragraphs.** Replace the paragraph that begins "**Executable inheritance.**" and the paragraph that begins "**Sandbox profile conflicts.**" with:

> **Removed inheritance.** Sync applies the §4.6 no-inheritance rule on every run. It drops each artifact whose `ARTIFACT.md` declares `extends:` before it applies the collision rule, so a dropped artifact collides with no other artifact, which matches a server that never stores it. Sync names each such artifact on standard error with its identifier, the code `ingest.invalid_artifact`, and the reason. Exit status, `--dry-run`, `--config` targets, and `podium sync override` treat these drops as they treat a collision drop. The workspace overlay applies no ingest rule (§6.4).

**Edit 13, §13.12 filesystem backend.** Anchor: the bullet that begins "- **Filesystem backend.**".

Current text:

> For a skill that document is its `SKILL.md`, which a child declaring `extends:` serves as its own stored `SKILL.md`. For every other type it is the served `ARTIFACT.md`, which for a child declaring `extends:` is the merged document (§4.6) rather than the stored one, so the stored pre-merge document of such a child has no owner.

Replacement:

> For a skill that document is its `SKILL.md`, and for every other type it is its stored `ARTIFACT.md`.

## Spec amendment: §1, §2.2, §9.1, §9.4, §10, and §11, overview and verification

**SPEC-6.** The overview and verification statements describe `extends:` merge, `extends:` resolution, or field-merge semantics. §11 instead pins the refusal and the equivalence of both deployment modes. Deterministic cross-layer merge survives in the §4.5.4 `DOMAIN.md` merge, so the §1 heading stays.

**Edit 1, §1.1.** `spec/01-overview.md`, the bullet that begins "- **Layered composition with deterministic merge.**".

Current text:

> `extends:` lets a higher-precedence artifact inherit and refine a lower one without forking. Most-restrictive-wins for security fields; last-layer-wins for descriptions.

Replacement:

> `DOMAIN.md` files merge across layers (§4.5.4): most-restrictive-wins for `unlisted` and the discovery bounds, last-layer-wins for descriptions.

**Edit 2, §1.3.** `spec/01-overview.md`, the bullet that begins "- **Cross-type dependency graph.**".

Current text:

> Dependency edges (`extends:`, `delegates_to:`, `mcpServers:`) are first-class and span types.

Replacement:

> Dependency edges (`delegates_to:`, `mcpServers:`) are first-class and span types.

**Edit 3, §2.2.** `spec/02-architecture.md`, the paragraph that begins "**Shared library code (Go).**".

Current text:

> the glob resolver, `LayerComposer`, the `extends:` resolver, the visibility evaluator,

Replacement:

> the glob resolver, `LayerComposer`, the visibility evaluator,

Current text:

> the same composer, parsers, merge logic, `extends:` resolution, and harness output run in every deployment mode.

Replacement:

> the same composer, parsers, merge logic, and harness output run in every deployment mode.

"Merge logic" now names the §4.5.4 `DOMAIN.md` merge.

**Edit 4, §9.1.** `spec/09-extensibility.md`, the SPI table.

- `LayerComposer` row: change "applies merge semantics and `extends:` resolution" to "applies the §4.6 collision rule". Pad to the table's column width. The row does not name the §4.5.4 `DOMAIN.md` merge, because `LayerComposer` does not perform it. `pkg/layer`, which implements the SPI (`pkg/layer/composer.go:1`), holds no `DOMAIN.md` code. `pkg/domain.MergeAcrossLayers` implements the merge (`pkg/domain/domain.go:1`, `:11`), and its callers invoke it directly (`pkg/registry/core/domain_load.go:144`, `pkg/overlay/overlay.go:105`). §10 builds the merge in phase 8, after the phase-7 `LayerComposer` (`spec/10-mvp-build-sequence.md:21-22`), and the language SDKs apply the overlay part of the merge client-side (`spec/04-artifact-model.md:506`) without the Go module (`spec/02-architecture.md:105`). Edit 3 leaves the merge under the §2.2 "merge logic" of the shared library.
- `TypeProvider` row: change "Type definitions: frontmatter JSON Schema + lint rules + adapter hints + field-merge semantics" to "Type definitions: frontmatter JSON Schema + lint rules + adapter hints". Pad to the table's column width.

**Edit 5, §9.4.** `spec/09-extensibility.md`, the paragraph under `### Programmatic curation (semantic discovery + scoped sync)` that begins "A script picks artifacts".

Current text:

> Podium owns the materialization (visibility filtering, `extends:` resolution, harness adaptation, audit).

Replacement:

> Podium owns the materialization (visibility filtering, harness adaptation, and audit).

**Edit 6, §10.** `spec/10-mvp-build-sequence.md`, phase 8. Delete "`extends:` with hidden-parent resolution, " from the phase description.

**Edit 7, §11 overlay precedence.** `spec/11-verification.md`, the bullet that begins "- **Workspace local overlay precedence test**". Replace the bullet with:

> - **Workspace local overlay precedence test**: confirm the workspace local overlay overrides every registry-side layer for a synthetic conflicting artifact, with the overlay artifact served as authored, and that removing the overlay file restores the registry-side artifact.

**Edit 8, §11 equivalence.** `spec/11-verification.md`, the bullet that begins "- **Filesystem ↔ server equivalence test**".

- Delete the sentence that begins "The directory also carries at least one artifact declaring `extends:` whose parent is another artifact in the directory" and ends "records from the registry."
- In the plain-skill sentence, delete ", and `extends:` children of the first two skills, one of which declares its version only in `metadata.version`".
- In the coverage sentence, delete ", and the merged document of a child whose overlay omits it", so it ends "the synthesized `ARTIFACT.md` (§4.3.4), and the version of a skill whose served `ARTIFACT.md` omits it."
- Change "A second directory carries two layers that contribute one canonical ID with no `extends:`, together with an artifact that collides with nothing." to "A second directory carries two layers that contribute one canonical ID, together with an artifact that collides with nothing."
- After the sentence that ends "and the filesystem consumer reports `ingest.collision` for the higher-precedence copy (§4.6, §13.11.3).", insert: "The second directory also carries an `ARTIFACT.md` declaring `extends:` in the lower-precedence layer, under a canonical ID that the higher-precedence layer contributes without the key. Both consumers omit it and materialize the higher-precedence copy, and the filesystem consumer reports `ingest.invalid_artifact` for it."
- The rest of the bullet is unchanged.

**Edit 9, §11 versioning and ingest.** `spec/11-verification.md`, the bullet that begins "- **Versioning and ingest tests**".

- Delete "`extends:` parent version pinned at child ingest time; ".
- Change both "including one that declares `extends:`" clauses to "including a skill with no `ARTIFACT.md`".
- After "and two concurrent ingests of different bytes commit distinct N;", insert: "an `ARTIFACT.md` declaring `extends:` is rejected at ingest with `ingest.invalid_artifact` and `details.constraint: \"extends_removed\"` before lint, fails `podium lint`, and is dropped by a filesystem-source sync; a stored row whose `ARTIFACT.md` declares `extends:` is absent from every read path named in §4.6 and does not take part in the collision rule;"

**Edit 10, §1.5.** `spec/01-overview.md`, the §1.5 comparison table, the row that begins "| **Self-hosted and managed registries**", in its "When Podium wins" cell. `docs/getting-started/why-podium.md` keeps its comparison table in step with this row (DOC-2 (f)).

Current text:

> ordered-layer composition with `extends:` across several sources;

Replacement:

> ordered-layer composition across several sources;

## Proposed solution

### CODE-1. `pkg/manifest`: delete the merge, serialization, and executable-field code; add DeclaresExtends

Targets: `pkg/manifest/types.go:155`, `pkg/manifest/merge.go`, `pkg/manifest/merge_serialize.go`, `pkg/manifest/executable.go`, `pkg/manifest/skill_fields.go:57`, `pkg/manifest/parse.go`, `pkg/manifest/artifact_test.go:211-229`, `pkg/manifest/coverage_gaps_test.go`, and `pkg/manifest/skill_fields_test.go:54-82`.

`MergeExtends`, `SerializeArtifact` (whose only caller is `SerializeMerged`), `SerializeMerged`, `FrontmatterHidingParent`, `InheritedExecutableFields`, `SandboxProfilesConflict`, and `MergedBlock` serve only `extends:`. Their callers are all on extends paths that CODE-3 to CODE-5 delete (`pkg/registry/core/core.go:1374`, `:1881`, `:1887`, `:1975`; `pkg/registry/filesystem/extends.go:285`, `:357`, `:381`; `pkg/registry/ingest/ingest.go:1738`, `:1753`).

- Delete `merge.go`, `merge_serialize.go`, and `executable.go` whole.
- Delete the `Extends` field of `Artifact` (`types.go:155`).
- Delete the `"extends"` entry of `podiumOnlySkillFields` (`skill_fields.go:57`).
- Delete `TestParseArtifact_Extends` (`artifact_test.go:212-229`), delete the three `TestMergeMCPServer_*` tests in `coverage_gaps_test.go` while keeping its podiumOnly tests. In `TestPodiumOnlySkillFieldClassification` (`skill_fields_test.go:54-82`), remove `"extends"` from the `podiumOnly` list at `:60`, because the old assertion fails once the entry is deleted, and add, citing `// Spec: §4.3.4, §4.6`, an assertion that `IsPodiumOnlySkillField("extends")` is false and that `PodiumOnlySkillFields()` does not contain `extends`. Removing the entry from the list alone stops asserting the old behavior without asserting the new one.

A key-presence predicate on raw bytes is required because the struct field goes away, the parser is lenient (`skill_fields.go:106-112`), `extends:` and `extends: ""` both decode to an empty string, and `TopLevelFrontmatterKeys` (`skill_fields.go:113-135`) reports `<<` rather than the merged key. A scratch check against `gopkg.in/yaml.v3` v3.0.1 confirms that decoding into `map[string]yaml.Node` finds the key in block form, flow form, quoted form, with an empty value, through `<<: *anchor` and `<<: {extends: a}`, and beside a `description: [x]` that the typed decode refuses, and that a nested key is not found. It also confirms that the map decode and the typed decode both find the key through a `!!binary ZXh0ZW5kcw==` key, in implicit, explicit `?`, and verbose `!<tag:yaml.org,2002:binary>` form, and through a double-quoted continuation in an explicit `?` key, and that both refuse an implicit double-quoted key split across two lines with "could not find expected ':'". A second scratch check confirms that `SplitFrontmatter`'s regex captures a block that starts with `\xff\xfe` or `\xfe\xff` followed by UTF-16LE or UTF-16BE text for `type: context` and `extends: a`, and that the map decode finds the key and the typed decode fills `Extends` with `a` for both byte orders. A UTF-16LE block with no byte order mark and a UTF-8 block that holds a NUL byte are both refused with "control characters are not allowed".

Add, in `skill_fields.go` next to `TopLevelFrontmatterKeys`:

```go
// DeclaresExtends reports whether an ARTIFACT.md frontmatter carries the
// top-level key extends, whatever its value, which §4.6 refuses. Merge keys
// are applied, because the 0.5.x typed decoder applied them when it folded a
// child. It decodes the frontmatter block only, because stored rows hold
// the full document and this predicate runs on every listed row.
//
// Spec: §4.6
func DeclaresExtends(src []byte) bool
```

Its steps:

1. `SplitFrontmatter(src)`. Return false on error.
2. `yaml.Unmarshal` the block into `map[string]yaml.Node`, matching `FrontmatterFields` at `parse.go:77`. Return false on error, because `ParseArtifact` refuses the same bytes, and any input the struct decode accepts the map decode also accepts.
3. Report whether the key `extends` is present.

The D-3 IMPLEMENTOR'S CHOICE on a fast path applies to these steps, under the constraint D-3 states.

### CODE-2. `pkg/layer`: remove the executable and sandbox rule types, add the removal constants, and rewrite the collision reason

Targets: `pkg/layer/executable.go` (delete), `pkg/layer/sandbox.go` (delete), `pkg/layer/collision.go:9-63`, `pkg/layer/composer.go:123`, `:170`, `:178-225`, and `pkg/layer/invalid.go` (new).

Both modes must report the same code, constraint, and reason, as the shared constants do today. `ExtendsOverlays` is the only sanction of a same-ID collision (callers at `pkg/registry/ingest/ingest.go:767`, `:770`, and `pkg/registry/filesystem/walk.go:206`) and goes with `extends:`.

- Delete `executable.go` and `sandbox.go`, keeping only `InvalidArtifactCode`, which `pkg/registry/ingest/ingest.go:54` and `pkg/registry/ingest/autoversion.go:128` use.
- Delete `ExtendsOverlays` and the now-unused `pkg/version` import from `collision.go`. Update the `composer.go:123` comment that names `extends:`.
- Delete `MostRestrictiveSensitivity` (`composer.go:178-205`) and `AppendUniqueTags` (`composer.go:207-225`). They implement the §4.6 field-semantics rows for sensitivity and tags that SPEC-1 Edit 3 deletes, their doc comments cite "§4.6 merge semantics", and no production path calls either one: the only callers are `TestMostRestrictiveSensitivity` and `TestAppendUniqueTags` (`pkg/layer/composer_test.go:256-287`), which TEST-8 deletes. Rewrite the `Composed` doc comment (`composer.go:170`) from "the result of merging candidates" to "the candidate `Compose` selects for a canonical artifact ID".
- Rewrite the `CollisionCode` comment (`collision.go:9-12`) to remove "that no extends: declaration sanctions".
- Rewrite the `Drop` doc comment (`collision.go:50-53`): "Drop is one artifact a filesystem-source composition dropped, with the §6.10 code and reason it reports. A collision and a §4.6 no-inheritance refusal both report through it, so a caller prints every drop the same way."
- Rewrite `Collision.Reason()` and its comment:

```go
// Reason returns the human-readable rejection text. It names the artifact
// and the remedy and omits the layer that already contributes the ID,
// because that layer can be one the caller is not entitled to see.
//
// Spec: §4.6, §13.11.3
func (c Collision) Reason() string {
	return fmt.Sprintf("cross-layer collision: %q is already contributed by another layer; rename this artifact or remove the other copy first",
		c.ArtifactID)
}
```

New file `pkg/layer/invalid.go`, named for the concern it holds because `InvalidArtifactCode` also serves the registry-assigned-version rule:

```go
// InvalidArtifactCode is the §6.10 code ingest returns for an artifact it
// cannot accept as authored (including the §7.3.1 registry-assigned-version
// rule), and the code both modes report for an ARTIFACT.md the §4.6
// no-inheritance rule refuses.
//
// Spec: §4.6, §7.3.1, §13.11.3
const InvalidArtifactCode = "ingest.invalid_artifact"

// ExtendsRemovedConstraint is the details.constraint value of a §4.6
// no-inheritance rejection.
//
// Spec: §4.6, §13.11.3
const ExtendsRemovedConstraint = "extends_removed"

// ExtendsRemovedReason is the reason both modes give an ARTIFACT.md that
// declares extends:. It names no layer.
//
// Spec: §4.6, §13.11.3
const ExtendsRemovedReason = "extends: is not supported; remove the extends: key and declare every field this artifact needs in its own frontmatter"

// ExtendsRemovedDrop returns the drop a filesystem-source composition
// reports for an ARTIFACT.md the §4.6 no-inheritance rule refuses.
//
// Spec: §4.6, §13.11.3
func ExtendsRemovedDrop(id string) Drop {
	return Drop{ArtifactID: id, Code: InvalidArtifactCode, Reason: ExtendsRemovedReason}
}
```

### CODE-3. `pkg/registry/ingest`: refuse before lint, make collisions unconditional, and delete the extends pipeline

Targets: `pkg/registry/ingest/ingest.go:418-420`, `:527-544`, `:566-583`, `:589`, `:591-760`, `:761-779`, `:784`, `:1009-1010`, `:1231-1248`, `:1533-1581`, `:1630-1930`, `:1955`, `:2042`; `pkg/registry/ingest/autoversion.go:53-112`; and `pkg/registry/ingest/helpers_internal_test.go:11-15`.

Server ingest is one of the two places a new `extends:` declaration enters. Making `ParseArtifact` strict would abort the whole layer walk on one artifact (`ingest.go:1307-1310`, `:1136-1138`), and a lint rule would add a lint code and miss filesystem sync, so the refusal is a partition of the walked records.

- **Refusal.** After `walkLayer` (`ingest.go:527`) and before lint, partition the records. Each record with `manifest.DeclaresExtends(rec.AuthoredBytes)` goes to `Rejected` with `Code: layer.InvalidArtifactCode`, `Reason: layer.ExtendsRemovedReason`, and `Details: map[string]any{"constraint": layer.ExtendsRemovedConstraint}`. The remaining records go to lint and the description advisories. At ingest `AuthoredBytes` equals `ArtifactBytes` (`ingest.go:1315-1316`), and a plain skill's is empty (`ingest.go:1367-1374`), so the predicate never fires on a synthesized document; `AuthoredBytes` is used for parity with CODE-4. Reassign `records` before `serverIdentifierIndex` (`ingest.go:589`), so a refused `mcp-server` never resolves an `mcpServers` edge.
- **Lint cascade.** Keep the partition. A same-layer artifact whose prose links to a refused artifact fails `lint.prose_reference`, because the refused artifact is withheld from the catalog the rule builds (`pkg/lint/body_rules.go:110-118`, `:153-161`, `:203-210`). SPEC-4 Edit 7 states this, and TEST-2 (j) pins it. Filesystem sync runs no lint and keeps such a sibling.
- **Unconditional collision.** At `ingest.go:761-779`, delete the sanction on the incoming `Extends` and the stored `ExtendsPin`, so every cross-layer row of the same ID rejects the incoming artifact with `ingest.collision`.
- **Retired rows.** Add one helper shared by `crossLayerRows` and `assignVersion`:

```go
// retired reports whether a stored row's ARTIFACT.md declares extends:, so
// the §4.6 no-inheritance rule serves it on no read path.
//
// Spec: §4.6, §7.3.1
func retired(m store.ManifestRecord) bool { return manifest.DeclaresExtends(m.Frontmatter) }
```

  - (a) In `assignVersion` (`autoversion.go:92-112`), leave a retired row out of the own-layer reuse candidates (`own` and `ownHash`), as soft-deleted rows are left out, which implements SPEC-4 Edit 9. Keep it in the duplicate set and in the N derivation, because it still occupies its `(id, version)` and content hash. Extend the doc comment with the reason: a retired row is served by no read path, so reusing it, or letting it outrank the served row as `latest`, would misreport a revert.
  - In `crossLayerRows` (`autoversion.go:58-68`), skip a retired row as a soft-deleted row is skipped, and extend its comment to say so.
- **Deletions.** Delete the extends block, the fold, the license advisory, the public-mode floor re-run on the folded value, `extendsEdgeRejection`, `resolveExtendsPin` (about `ingest.go:1786-1923`), `splitRef` (`ingest.go:1925-1930`) with its test (`helpers_internal_test.go:11-15`), the extends dependency edge (`ingest.go:1544-1581` keeps the `delegates_to` and `mcpServers` edges), and the `chainReadError` comment and type (`ingest.go:1231-1248`).
- Rewrite the `RejectedArtifact.Details` comment (`ingest.go:418-420`) to cite the `extends_removed` and `auto_version_duplicate_content` constraints.

### CODE-4. `pkg/registry/filesystem`: one refusal in Walk, unconditional collision, and no resolver

Targets: `pkg/registry/filesystem/extends.go` (delete), `pkg/registry/filesystem/walk.go:15-35`, `:62-113`, `:116-150`, `:176-232`, `:234-273`.

`filesystem.Walk` is the one walk behind filesystem-source sync (`pkg/sync/sync.go:580-584`, `CollisionPolicyDrop` with `OnDrop`), `podium lint` (`cmd/podium/main.go:1083-1085`, `CollisionPolicyDefault`, with no `OnDrop` today and a collecting `OnDrop` after step 8), and the Go workspace overlay (`pkg/overlay/overlay.go:61-63`, `CollisionPolicyHighestWins` with no `OnDrop`). Server ingest has its own `walkLayer`, so the two refusal points share only the predicate and the constants.

1. After the layers are gathered and before `dedupe`, add `refuseExtends(all []ArtifactRecord, opts WalkOptions) ([]ArtifactRecord, []layer.Drop, error)`, citing `// Spec: §4.6, §13.11.3`. Under `CollisionPolicyHighestWins` it returns `all` unchanged and no drop, because §6.4 serves an overlay artifact as authored (D-6). Under every other policy it removes each record with `manifest.DeclaresExtends(rec.AuthoredBytes)`. `AuthoredBytes` equals `ArtifactBytes` at walk time and is nil for a plain skill (`walk.go:362-368`, `:410-417`), and synthesis runs after composition (`walk.go:95`), so the predicate reads authored bytes. `ParseArtifact` checks no required field (`pkg/manifest/parse.go:110-121`), so a child that relied on inheritance reaches the refusal.
2. With `OnDrop` set, report each refusal through `OnDrop` as `layer.ExtendsRemovedDrop(rec.ID)` and continue with the remaining records, under `CollisionPolicyDefault` and `CollisionPolicyError` as under `CollisionPolicyDrop`. With `OnDrop` nil, fail the walk in the package's error style: `fmt.Errorf("%s: %q in layer %q: %s", layer.InvalidArtifactCode, rec.ID, rec.Layer.ID, layer.ExtendsRemovedReason)`. Naming the layer is acceptable here, because the local caller owns every layer, as the collision error at `walk.go:209-210` already does. Rewrite the `reportDrops` comment (`walk.go:169-171`): a non-empty slice arises under `CollisionPolicyDrop`, and for a refusal under any refusing policy with `OnDrop` set, because a refusal with `OnDrop` nil fails the walk before `reportDrops` runs.
3. Report refusals in layer order, then by canonical ID, before collision drops, so sync output is deterministic.
4. In `dedupe`, delete the `layer.ExtendsOverlays` sanction, so every same-ID pair collides. Delete `extendsOf`. Remove `dedupe`'s now-unread `kept` return and the `composeExtends` sentence in its comment. Collapse `resolveWalk` into `refuseExtends`, then `dedupe`, then `reportDrops`, or inline it in `Walk`.
5. Delete `extends.go` whole, which removes `composeExtends`, `validateExtends`, `refusalOf`, `unresolvedParentError`, `indexRecords`, `resolveExtends`, `executableInheritance`, `mergeRecord`, `blockOf`, `sortedDrops`, `recordKey`, `keyOf`, and `refusal`. Delete `WalkOptions.ResolveExtends`.
6. Rewrite the `AuthoredBytes` comment (`walk.go:27-34`) around the §4.3.4 synthesized document: `ArtifactBytes` and `AuthoredBytes` differ only for a skill with no `ARTIFACT.md`, where synthesis sets `ArtifactBytes` and `AuthoredBytes` stays nil.
7. Rewrite the `Walk` doc comment (`walk.go:64-70`), the comment at `:84-94`, the `CollisionPolicyError`, `CollisionPolicyHighestWins`, and `CollisionPolicyDrop` comments, and the `OnDrop` comment. They state that every same-ID pair collides; that `OnDrop` receives an `extends_removed` drop under `CollisionPolicyDefault`, `CollisionPolicyError`, and `CollisionPolicyDrop`, and a collision drop only under `CollisionPolicyDrop`; that a walk under those policies with `OnDrop` nil fails on an `ARTIFACT.md` that declares `extends:`; and that `CollisionPolicyHighestWins` applies no ingest rule.
8. Wire `podium lint` (`lintCmd`, `cmd/podium/main.go:1054-1105`) to the refusal, so it behaves like server ingest. Today it walks with no `OnDrop` and returns 1 on a walk error before `linter.Lint` runs (`main.go:1083-1089`), so a walk that fails on the first refused artifact would name no lint diagnostic of the rest and break the unchanged §7.3.1 sentence that `podium lint` against the source names the artifacts behind a reingest's lint count (`spec/07-external-integration.md:159`). Change the call to `reg.Walk(filesystem.WalkOptions{CollisionPolicy: filesystem.CollisionPolicyDefault, OnDrop: func(d layer.Drop) { drops = append(drops, d) }})`, where `drops` is a local `[]layer.Drop`. The walk calls `OnDrop` synchronously in the calling goroutine (`reportDrops`, `walk.go:169-176`), so the slice needs no lock. After the walk, call `reportDropped(os.Stderr, drops)` (`cmd/podium/layer.go:617-622`), which prints `rejected: <id> (ingest.invalid_artifact): <reason>` per drop in the step 3 order, the same line `podium sync` prints. Then run `linter.Lint` over the returned records and print each diagnostic on standard output as today. Exit 1 when `drops` is non-empty or any diagnostic is an error, and print `lint: no issues.` only when there is no drop and no diagnostic. A same-ID cross-layer collision still fails the walk under `CollisionPolicyDefault` and exits 1 as today, because step 7 sends a collision to `OnDrop` only under `CollisionPolicyDrop`. The refused records are absent from the linted set, which is the catalog `lint.prose_reference` resolves against (`pkg/lint/body_rules.go:110-118`), so a sibling whose prose links to a refused artifact fails that rule here as it does at ingest (CODE-3 *Lint cascade*). After this step no non-test caller walks under `CollisionPolicyDefault` or `CollisionPolicyError` with `OnDrop` nil (`cmd/podium/main.go:1083`, `pkg/sync/sync.go:580`, and `pkg/overlay/overlay.go:61` are the non-test callers), and the nil-`OnDrop` failure stays for library callers. Rewrite the `reportDropped` doc comment (`cmd/podium/layer.go:610-616`) to name the §4.6 collision rule and the §4.6 no-inheritance rule in place of the sandbox-profile and executable-field rules, and to name `podium lint` beside the sync commands. If the wiring is missing, `podium lint` exits 1 on the first refused artifact with no diagnostic of the rest, which the TEST-7 mixed lint case observes.

The ingest and filesystem walk-parity test (`pkg/registry/ingest/walk_internal_test.go:340`) covers package discovery only and gets no `extends:` case, because the refusal sits outside both `walkLayer` functions by design.

### CODE-5. `pkg/registry/core`: retire stored rows at the listing choke points and delete the chain walk, merge, and hidden-parent code

Targets: `pkg/registry/core/core.go:528`, `:1272-1294`, `:1345-1374`, `:1377-1405`, `:1490-1498`, `:1536`, `:1620-1650`, `:1753-1830`, `:1842-1990`, `:2095-2160`, `:2185-2253`; `pkg/registry/core/admit.go:36-62`, `:74-83`, `:181-219`, `:240`, `:282-347`, `:377-384`; and `pkg/registry/core/dependents.go:25-105`.

Every read path in core draws its rows from `visibleManifests`, `adminVisibleManifests`, or `artifactExistsAnywhere` (`core.go:2185-2253`; callers at `analyze.go:61`, `core.go:616`, `:1222`, `:1568`, `:2108`, `dependents.go:42`, `:164`, `domain_load.go:266`, `effective_view.go:35`). The load path chooses candidates from that set before content-hash pins, session pins, `latest`, exact versions, and the `Revalidate` HEAD and 304 bypass run (`core.go:1566-1720`). Filtering there serves the retired-row rule on every path that reads stored rows, and no retired row reaches admission or the bypass. Hybrid search also reads a second source the listings do not filter: the vector index, which keys one vector per `(id, version)` row (`reembed.go:93-96`) and still holds a retired row's vector, and (d) filters it. `quota.go:32` and `reembed.go:68`, `:210` keep reading every row, so retired rows are still counted and reembedded (non-goals).

- **Choke point.** Add `servableRows(ctx, rows []store.ManifestRecord) []store.ManifestRecord`, citing `// Spec: §4.6`, which drops each row for which `manifest.DeclaresExtends(m.Frontmatter)` is true. Call it on the `ListManifests` result inside `visibleManifests`, `adminVisibleManifests`, and `artifactExistsAnywhere`.
- **(b) Memo.** Memoize the predicate per `(tenant, content hash)` in a map guarded by a `sync.RWMutex` on `Registry`. The key includes the content hash, because a purge followed by a re-ingest can store other bytes under a reused `(id, version)` (§7.3.1), and the content hash is injective over the `ARTIFACT.md` bytes (§4.7.6). Entries are never evicted, so the map holds one boolean per distinct stored content hash. Document that invariant on the field. A row whose stored bytes no longer reproduce its hash is refused at admission whatever the memo says, so a stale verdict fails closed.
- **(a) Admission.** Delete `checkExtendsPin` and `checkContentHashReference` (`admit.go:191-219`, `:332-347`), collapse `walkAdmitted` and `admitChain` into a single admit call, and rewrite the `admit` and `admitBytes` doc comments (`admit.go:36-45`, `:58-62`) for the two checks that remain.
- **Merge and hidden parents.** Delete `mergeChain`, `ExtendsChain`, `ResolvedArtifact`, the `FrontmatterHidingParent` search-descriptor path (`core.go:1345-1374`), the merged-frontmatter serving (`core.go:1842-1990`), and `LoadArtifactResult.ExtendsPin`. Deleting `ExtendsChain` depends on CODE-3 removing its caller at `pkg/registry/ingest/ingest.go:1728` in the same step.
- **Object route.** In `core.go:2095-2160`, delete the ownerless pre-merge branch, so a non-skill artifact owns its stored `ARTIFACT.md` (SPEC-5 Edit 13).
- **(c) Dependents.** Delete `childPins` and the extends branch in `dependents.go`. State the remaining rule in the doc comment: an edge is admitted when both endpoint IDs have a servable visible row, and edges are ID-level, so an edge a retired version recorded under `delegates_to` or `mcpServers` stays visible while its ID has another servable row, as a superseded version's edges do today. This implements the SPEC-2 Edit 7 sentence.
- **(d) Vector ranker.** `vectorRanks` (`core.go:1381-1405`) keeps a vector match whenever its `ArtifactID` is a candidate ID and never compares `m.Version`, and `reorderScored` (`core.go:1435-1450`) then adds each vector-only hit at the candidate's served version and counts it in `total_matched` (`core.go:1280-1294`). A retired row's vector, built at 0.5.x from the merged projection, would therefore keep returning and ranking its ID. Change the signature to `vectorRanks(ctx context.Context, query string, candidates, servable []store.ManifestRecord, topK int)`, citing `// Spec: §4.6`. Its one caller, `SearchArtifacts` (`core.go:1280`), passes `filtered` as `candidates` and `visible`, the `visibleManifests` or `adminVisibleManifests` result that `servableRows` already filtered, as `servable`. The function builds a set of `(ArtifactID, Version)` pairs from `servable` per call and keeps a match only when its `ArtifactID` is a candidate ID and its `(ArtifactID, Version)` is in that set. The domain-sentinel skip stays. The set covers every servable visible version, so a vector of an older non-retired version still surfaces its ID as it does today, and only a retired row's vector is dropped. The change reads no stored state beyond the listing the caller already holds, adds no field, and changes no interface. `domainVectorRanks` (`domain_search.go:175-195`) is unchanged, because it reads only the `DomainVectorVersion` partition. If the filter does not run, a query that only a retired row's vector matches returns that row's ID, which the TEST-4 vector case observes.

### CODE-6. `pkg/registry/server`: the bare content-hash ETag and no `extends_pin` member

Targets: `pkg/registry/server/server.go:638-640`, `:718-721`, `:1194`, `:1222-1243`; `pkg/registry/server/delivery.go:25`; `pkg/registry/server/layers.go:2000-2003`; and `pkg/registry/server/delivery_internal_test.go:10-27`.

- Delete the `ExtendsPin` field of the `load_artifact` response struct (`server.go:718-721`) and its assignment (`:1194`).
- Make `validatorFor` return `contentHashETag(res.ContentHash)`, and keep it as the single validator that both the `Revalidate` predicate (`:1141`) and the ETag branch (`:1163`) read. Add `// Spec: §7.2, §13.4`, and state in its comment that the validator is the content-hash ETag on full, HEAD, and 304 responses.
- Delete `loadArtifactETag` and the `crypto/sha256` and `encoding/hex` imports, which have no other use in `server.go`.
- Rewrite these comments:
  - (a) `server.go:638-640`: the descriptor's `Sensitivity` is the row's own declared value (§4.7.4). Drop "resolved most-restrictive across an extends chain (§4.6)".
  - (b) `delivery.go:25`: delete "The per-caller ExtendsPin is excluded."
  - (c) `layers.go:2000-2003`: list the drop reasons that remain (sensitivity floor, cross-layer collision, `extends_removed`, the registry-assigned-version rule, and quota), and drop "sandbox profile", "extends mismatch", and "the executable-field rule".
- In the same step, delete `TestLoadArtifactETag_FoldsTheServedExtendsPin` (`delivery_internal_test.go:14-27`), which calls the deleted function. Replace it with a `// Spec: §7.2, §13.4` unit test asserting that `validatorFor` equals `contentHashETag(res.ContentHash)` and is empty for an empty hash, so the package compiles at S3.

### CODE-7. `pkg/store`: drop the pin from records and new schemas, remove the purge protection, and read only the §4.7.3 edge kinds

Targets: `pkg/store/store.go:163-167`, `:253`, `:446-450`; `pkg/store/sqlite.go:95`, `:327-337`, `:392-402`, `:512`, `:618`, `:641-656`, `:698-745`, `:1060`; `pkg/store/postgres.go:86`, `:710-719`, `:780-789`, `:902`, `:1014`, `:1061-1071`, `:1536`, and its `DependentsOf` and `DependencyInDegree`; `pkg/store/memory.go:304-311`, `:359-390`; `pkg/store/schema_migrate.go:66`; `pkg/store/storetest/storetest.go:537-596`; `pkg/store/store_test.go:55-71`.

- Delete `ManifestRecord.ExtendsPin`. Remove `extends_pin` from both `CREATE TABLE` statements and from every INSERT, SELECT, and Scan. Delete the `schema_migrate.go:66` entry in the same commit, which keeps `TestSQLite_AdditiveColumnsMatchSchema` consistent (`schema_migrate_test.go:264-283`). Existing databases keep the unused `NOT NULL DEFAULT ''` column, which accepts inserts that omit it, and every INSERT uses `ON CONFLICT DO NOTHING` with no SET clause naming the pin.
- Delete the pinned-parent purge protection in SQLite, Postgres, and Memory. This is mandatory, because the SQL names `child.extends_pin`.
- In all three stores, add `kind IN ('delegates_to','mcpServers')` (Memory: the same predicate) to `DependentsOf` and `DependencyInDegree`. The filter lives in the store, because `DependencyInDegree` aggregates across kinds before core sees an edge.
- Rewrite the interface doc at `store.go:446-450` ("counts distinct sources across the §4.7.3 kinds `delegates_to` and `mcpServers`; a stored edge of any other kind is not read"), the comment at `memory.go:385`, and the conformance comment at `storetest.go:537-538`. Delete `extends` from the edge-kind comment at `store.go:253`.
- Tests: in each existing `dependencyEdges` and `dependencyInDegree` conformance case, keep exactly one `Kind: "extends"` edge to `parent` as the legacy edge, switch the other extends fixtures (including `dependentsScopedToTenant` at `storetest.go:595-596`) to `delegates_to`, and change the expectations (`DependentsOf(parent)` returns 2 edges, and the parent's in-degree is 1). Add no new conformance case.
- `TestMemory_DependencyIndex` (`pkg/store/store_test.go:55-71`) has this step as its one owner, because the kind filter lands here. Keep its `Kind: "extends"` edge from `child` as the legacy edge and its `delegates_to` edge from `agent1`, change the expectation to exactly 1 edge whose `Kind` is `delegates_to`, and rewrite its comment to say that `DependentsOf` returns every §4.7.3 edge ending at the artifact and no stored edge of another kind (`// Spec: §4.7.3`).
- Compile-affected files outside `pkg/store` change in the same step: `test/integration/store_retention_objects_test.go`, `test/integration/plain_skill_ingest_test.go`, `test/e2e/lifecycle_journeys_test.go`, and `pkg/registry/core/served_frontmatter_test.go` drop every `ExtendsPin` reference. Rewrite the column-list comment at `test/e2e/server_ops_rolling_upgrade_test.go:119` to drop `extends_pin`.
- `test/integration/auth_cross_tenant_quota_test.go`, `TestAuthCrossTenantQuota_NonInterferenceOverSharedPostgres`, has this step as its one owner, because its dependency-edge assertions read through the kind filter that lands here. Its `mustPutEdge` (`:150-155`) stores `Kind: "extends"` edges, and the `xtqHasEdge` assertions at `:216` and `:225` require each org's `/v1/dependents` answer to contain its own edge, so under the D-10 filter both fail. Change `mustPutEdge` to write `Kind: "delegates_to"`, drop the `extendsPin` parameter of `mustPut` (`:140-149`) and the pin arguments at `:156-160`, and rewrite the fixture comment at `:135-139` to say that each org owns a depended-on artifact and a dependent with a `delegates_to` edge to it, with no claim about pins. The test skips when `PODIUM_POSTGRES_DSN` is unset (`:104-106`), so a local run without a Postgres does not show the failure, while CI sets the variable for the whole suite (`.github/workflows/test.yml:108`). The S8 command runs it against a Postgres.

### CODE-8. `pkg/sync`: drop extends resolution and the removed-rule comments

Targets: `pkg/sync/sync.go:136-148`, `:168`, `:430-448`, `:485`, `:521-526`, `:565`, `:571-583`; `pkg/sync/records.go:87`; `pkg/sync/override.go:64`; `pkg/sync/render.go:64`; and `pkg/sync/effective_view.go:46`.

- Delete `ResolveExtends: true` at `sync.go:583`. Sync's filesystem source already reports drops through `OnDrop` into `Result.Dropped`, and `reportDropped` prints them and exits 1 (`cmd/podium/main.go:414`, `:472`, `:618`), so the CODE-4 drop needs no new wiring.
- `pkg/overlay/overlay.go` is unchanged. Its walk uses `CollisionPolicyHighestWins`, which CODE-4 exempts (D-6).
- Rewrite these comments to "the §4.6 collision rule or the §4.6 no-inheritance rule (§13.11.3)": `Result.Dropped` (`sync.go:168`), the `resolveRecords` doc comment (`sync.go:485`), the `filesystemRecords` doc comment (`sync.go:565`), the `FetchRecords` comment (`records.go:87`), `OverrideResult.Dropped` (`override.go:64`), the render `Dropped` comment (`render.go:64`), and the effective-view comment (`effective_view.go:46`).
- Rewrite the in-function comment at `sync.go:571-578`: the filesystem source runs the same §4.6 collision rule as the server and drops each artifact declaring `extends:` before the collision rule, as a server never stores it. Delete the recompose sentence.
- Rewrite the `AuthoredBytes` and `ContentHash` comments at `sync.go:136-148` and the comments at `:430-448` around the §4.3.4 synthesized document, the only case where the served `ARTIFACT.md` differs from the hashed one.
- Rewrite the `applyOverlay` comment (`sync.go:521-526`) to drop "whether or not it declares extends:": an overlay record replaces the same-ID base record per the §6.4 overlay exception, and the overlay applies no ingest rule.

### CODE-9. Comment and constant cleanup in `pkg/version`, `pkg/sign`, `pkg/dependency`, and `cmd/`

Targets: `pkg/version/version.go:366-370`, `:419-421`; `pkg/sign/sign.go:34-36`, `:104-110`; `pkg/dependency/graph.go:3`, `:13`; `pkg/dependency/graph_test.go:8`, `:12`, `:15`, `:39`, `:40`; `cmd/podium/main.go:1076-1082`; `cmd/podium/layer.go:586-588`; and `cmd/podium-mcp/main.go:2078-2080`.

- `version.go:366-370`: the `DeliveryRecord` comment says that every field is the served value, so for a skill with no `ARTIFACT.md` the `Frontmatter` is the §4.3.4 synthesized document rather than the stored zero-length `ARTIFACT.md`.
- `version.go:419-421`: the `DeliveryHash` comment says that the lifecycle fields and the contributing layer are absent from the record, because the digest must not depend on when the record is read or on which path served it (§4.7.10). Delete the `extends_pin` mention.
- `sign.go:34-36` and `:104-110`: delete the "for a child declaring extends:" clauses and the `merge.go` and pre-merge sentence. Keep "the value is reported by the same party that supplies the signature", which matches §4.7.9.
- `graph.go`: delete `EdgeExtends`. The package comment reads "Edges span types: delegates_to and mcpServers."
- `graph_test.go`: replace each `EdgeExtends` edge with `EdgeDelegatesTo` or `EdgeMCPServer`, keeping the counts and the transitive `ImpactSet` expectation unchanged. The `:8` comment names `delegates_to` and `mcpServers` edges.
- `cmd/podium/main.go:1076-1082`: the comment states that `podium lint` fails on a same-canonical-ID cross-layer collision (§4.6), that it collects every `ARTIFACT.md` that declares `extends:` through `OnDrop`, reports each as a `rejected:` error line, and lints the remaining artifacts, as ingest does (§4.6, §7.3.1; CODE-4 step 8), and that sync uses `CollisionPolicyDrop`, which drops and reports both (§13.11.3). CODE-4 step 8 changes the call this comment sits on at S3, and this step rewrites only the comment.
- `cmd/podium/layer.go:586-588`: the rejection list reads "sensitivity floor, cross-layer collision, a declared extends:, quota, or a data-plane resource-store failure". It drops "sandbox profile" and "unresolved extends".
- `cmd/podium-mcp/main.go:2078-2080`: the parenthetical reads "(the synthesized document for a skill with no ARTIFACT.md)".

Run `go test ./pkg/dependency/ ./pkg/version/ ./pkg/sign/` after the edit.

### CODE-10. `cmd/podium` artifact scaffold: remove `--extends`

Targets: `cmd/podium/artifact_scaffold.go:48`, `:62`, `:97`, `:357-358`.

Delete `opts.extends`, the flag registration, the `"extends"` entry in `skillRefusedFlags`, and the frontmatter line writer. Passing `--extends` then fails flag parsing with the command's existing usage error and exit 2 for every `--type` (`cmd/podium/main.go:130-135`; `cmd/podium/artifact_scaffold.go:112-115`). Confirm the exit code against the scaffold's `flag.ContinueOnError` handling. TEST-7 rewrites the tests that pass `--extends`.

### CODE-11. SDKs: edge kinds

Targets: `sdks/podium-ts/src/index.ts:788-792`, `:1523-1524`, and `sdks/podium-py/podium/client.py:1535-1540`.

- `index.ts:791` becomes `kind: "delegates_to" | "mcpServers";`.
- The `index.ts:1524` comment drops `extends`.
- The `client.py:1540` docstring lists `delegates_to` and `mcpServers`.

The SDK overlays are unchanged (D-6): they keep serving an overlay `ARTIFACT.md` as authored, and neither parser reads an `extends` key today.

### CODE-12. Web UI: remove the extends rail, `extends_pin`, and the extends collision copy; keep one always-drawn dependents group; rebuild the bundle

Targets: `web/ui/src/api.ts:294-298`, `:303-308`, `:572`; `web/ui/src/surfaces/ArtifactViewer.tsx:1-4`, `:895-1055`; `web/ui/src/surfaces/LayerPanel.tsx:150-161`; `web/ui/src/route.ts:74-77`; `web/ui/src/components/PropertyTable.tsx:100-104`, `:131-137`, `:141-157`; `web/ui/src/surfaces.test.tsx` (every extends case, including `:4479`, `:4671-4760`, `:4767-4912`, `:6685-6712`, `:7188`, the served-note assertions at `:4843-4853`, and the Layer panel lead case at `:8492` whose lead assertions sit at `:8501-8513`); `web/ui/src/propertytable.test.tsx:107-119`; and `web/bundle/`. `PropertyTable.tsx` contains NUL bytes, so `grep` treats it as binary and a `grep -I` sweep skips it; search it with `grep -a`.

The spec tie is the SPEC-5 Edit 8 change to §13.10, "links to dependent artifacts (§4.7.3)".

- `api.ts`: delete `extends_pin` from `LoadArtifactResponse`. Rewrite the lifecycle comment (`:303-308`) to say the registry reports the §4.7.4 state for the served row. Rewrite the `reorderLayers` comment (`:572`) to say that composition order decides precedence for the collision rule and for `DOMAIN.md` merging.
- `ArtifactViewer.tsx`:
  - Delete `servedExtends`, `referenceID` when it has no other caller, the outbound `extends` `RailRelationGroup`, the `extendedBy` constant, and the `'extends'` case in `inboundLabel`.
  - Rewrite the file header and the `RailRelationGroup` and `Relations` comments so they cite §13.10 for dependent artifacts only, with no extends wording.
  - Keep one always-drawn group: when the reverse index returns no edges, render "Nothing depends on this artifact." under a "dependents" label. When the read fails, label the error band "dependents". Draw the "delegated to by" and "referenced by" groups when they have members.
- `LayerPanel.tsx`: rewrite the comment, citing `// Spec: §4.6 ("Collisions")` in place of `("Merge semantics for collisions")`, and the lead copy. A same-ID artifact in a second registry-side layer is rejected at ingest with `ingest.collision` and must be renamed or the other copy removed first. The order below sets precedence.
- `route.ts`: describe the separator as the `<id>@<version>` pin syntax of §4.7.6 for `delegates_to:` and `mcpServers:` references, without mentioning extends.
- `components/PropertyTable.tsx`: delete `ServedNote`, its doc comment (`:141-157`), and its renders (`:103` in the raw view and `:134` under the table). The note says that a withheld key is absent and that an artifact "can carry no `extends` row here and still name a parent under Relations". CODE-5 deletes the hidden-parent and merged-frontmatter serving, so no key is withheld, and this deliverable removes the extends rail, so no parent appears under Relations. Keep the "Values are shown verbatim" note.
- Tests: remove the extends rail cases. Delete the `propertytable.test.tsx` case "draws the served note key as a code chip" (`:107-119`) and the `(key withheld)` served-note assertions in `surfaces.test.tsx` (`:4843-4853`). Add a `propertytable.test.tsx` case asserting that no `frontmatter-served-note` element renders in the table view or the raw view. Add or adjust cases asserting that no extends group or `extends_pin` chip renders, that a `delegates_to` edge renders under "delegated to by", that an artifact with no edges shows "Nothing depends on this artifact.", and that a failed read keeps the "dependents" label. Each case cites `// Spec: §13.10`. Rewrite, and do not delete, the Layer panel case "states its description, the winning end of the order, and the cap on the caller layers" (`surfaces.test.tsx:8492`). Its lead assertions (`:8501-8513`) assert the new lead copy: a same-ID collision is rejected at ingest, and the remedy is to rename the artifact or to remove the other copy first. Keep `expect(lead).not.toContain("the higher precedence wins")`, add an assertion that the lead does not contain "extends", change its citation to `// Spec: §4.6 ("Collisions")`, and leave the case's other assertions unchanged.
- Run `npm run build` in `web/ui` and commit `web/bundle` in the same commit.

### FIX-1. Reference registry fixture: drop the extends: children

Targets: `testdata/registries/reference/personal/notes/glossary-notes/ARTIFACT.md`, `testdata/registries/reference/personal/notes/versioned-child/ARTIFACT.md`, `testdata/registries/reference/personal/notes/meta-child/` (delete the package), `testdata/registries/reference/README.md:25-40`, `testdata/README.md:18`, `test/integration/sync_equivalence_test.go:47-51`, `:143-152`, `:201-235`, and `test/integration/sync_manifest_body_test.go:140-177`.

Under D-5, a walk refuses a declared `extends:`. That breaks every suite that walks or syncs the reference registry: `TestReferenceRegistry_OpensAndWalks`, the §11 equivalence test, and `TestSyncServerSource_LockPinsRegistryContentHash`.

- `glossary-notes`: delete only the `extends: company-glossary` line. The artifact already declares type, version, and description.
- `versioned-child`: delete only the `extends:` line. It becomes an `ARTIFACT.md` overlay that declares `version: 1.0.0` over a `SKILL.md` with no `metadata.version`. No field is added, because §4.3.4 keeps a skill's description in `SKILL.md`.
- `meta-child`: delete the whole package. Without `extends:` it duplicates `org-defaults/plain-skills/overlay-unversioned`, which is also an overlay that omits version over a `SKILL.md` `metadata.version`.
- `sync_equivalence_test.go`: remove `"notes/meta-child"` from `plainSkillVersions`, delete the meta-child merged-document assertion in `assertNonePlainSkillTree` (`:226-235`), and rewrite the comments at `:47-51`, `:143-146`, and `:203-206` to name the plain skills, the overlay that omits version, and the overlay that declares it.
- `sync_manifest_body_test.go`: rewrite `TestSyncServerSource_OverlayExtendsChildAgreesWithIngest` (`:140-177`) in place as `TestSyncServerSource_OverlayPlainSkillAgreesWithIngest` (`// Spec: §4.7.6, §6.4, §11`). It is the only test of the §11 overlay leg of the content-hash agreement against the digest ingest stored, and SPEC-6 Edit 9 makes §11 name a skill with no `ARTIFACT.md` in that leg. The other overlay hash tests compare only against the formula for packages that carry an `ARTIFACT.md` (`pkg/sync/overlay_test.go:173-215`; `cmd/podium-mcp/overlay_load_test.go:31-58`, `:156-204`). The rewritten test copies the reference fixture's `org-defaults/plain-skills/unversioned/SKILL.md`, the package's only file, byte for byte into the overlay directory at `plain-skills/unversioned/SKILL.md`, with no `ARTIFACT.md`, and runs a server-source `sync.Run` with `OverlayPath` against `server.NewFromFilesystem` over the reference registry, as the current test does. It asserts that the lock's `content_hash` for `plain-skills/unversioned` equals the digest the registry serves for that ID (`registryContentHash`, `test/integration/sync_server_content_hash_test.go:69`), equals `"sha256:" + version.CanonicalContentHash(nil, skillBytes, nil)`, and differs from `"sha256:" + version.CanonicalContentHash(synth, skillBytes, nil)`, where `synth` is `manifest.SynthesizedSkillDocument("0.0.0-auto.0")` (`pkg/manifest/skill_version.go:78`). The last assertion fails if the overlay leg hashes the synthesized `ArtifactBytes` in place of the nil `AuthoredBytes` (`pkg/sync/sync.go:546-547`, `:453-454`). Rewrite the doc comment to name the overlay leg for a skill with no `ARTIFACT.md`. The S2 and S8 `-run` pattern already matches it through `SyncServerSource`, and it passes before and after CODE-4, because the 0.5.x overlay walk already hashes a plain skill's nil `AuthoredBytes`.
- `testdata/registries/reference/README.md:25-40`: delete the glossary-notes paragraph, and rewrite the plain-skills paragraph without the `extends:` children and the merged-document sentence. In `testdata/README.md`, delete the stale `extends/` line, because that directory does not exist.
- `test/conformance/reference_registry_test.go` needs no edit.
- Re-run `test/conformance`, the equivalence test, and `test/integration/sync_server_content_hash_test.go` before S3, and again at S8, the first step after S3 at which `test/integration` compiles (the S2 and S8 commands).

## Edge cases and accepted failure modes

The rows cite the governing spec text only. Every row that records a user-visible change from 0.5.2 appears in the DOC-3 Removed or Changed entries or in DOC-2 (i), and the DOC-2 (h) step checks that once. Rows do not cite doc sentences individually.

| Case | Observable outcome | Governing spec text |
|:--|:--|:--|
| A new `ARTIFACT.md` carries `extends:` in block, flow, quoted, escaped, `!!binary`-tagged, or UTF-16-encoded form, with any value including empty, or through a YAML merge key | Ingest rejects it with `ingest.invalid_artifact`, `details` `{"constraint": "extends_removed"}`, and `ExtendsRemovedReason`; it is not stored | §4.6 "An `ARTIFACT.md` whose frontmatter carries the top-level key `extends`, directly or through a YAML merge key and whatever its value, is refused." (SPEC-1) |
| That artifact also fails lint, for example a hook that omits `hook_action` | Reported once as `extends_removed`; the lint count excludes it | §7.3.1 "never counts it as a lint failure" (SPEC-4 Edit 7) |
| The cycle accepts nothing and has a lint failure beside the refused artifact | 422 `ingest.lint_failed` envelope with no `rejected` member; the refused artifact is still not stored | §7.3.1 "Neither envelope reports the cycle's other rejections or their `details`." (unchanged) |
| Another artifact of the same cycle links to the refused artifact in its prose | That artifact fails `lint.prose_reference`; a filesystem sync, which runs no lint, keeps it | §7.3.1 "the §4.4 prose-reference check does not resolve a reference to it from another artifact of the same cycle" (SPEC-4 Edit 7) |
| A `SKILL.md` carries an `extends` key | Not refused; an ordinary non-Podium `SKILL.md` key. It draws no `lint.skill_podium_only_field` error, which 0.5.x raised (`pkg/lint/skill_rules.go:36-50`), so `podium lint` exits 0, ingest stores and serves the skill with no lint failure, and a filesystem sync materializes it, with or without an `ARTIFACT.md` overlay (TEST-1, TEST-2 (k), TEST-3, TEST-7) | §4.6 "An `extends` key in a skill's `SKILL.md` is not a Podium field (§4.3.4), and this rule does not refuse it." (SPEC-1); the §4.3.4 field placement table (SPEC-2 Edit 4) |
| `extends` nested under another key, or only in body prose | Not refused | §4.6 "the top-level key `extends`" (SPEC-1) |
| A filesystem sync, `--dry-run`, `--config`, or `podium sync override` meets a declared `extends:` | Drop line `rejected: <id> (ingest.invalid_artifact): <reason>`, exit 1, failed target | §13.11.3 *Removed inheritance* (SPEC-5 Edit 12) |
| A lower layer's `extends:` copy of an ID a higher layer contributes plainly | Both modes keep the higher copy; no collision is reported | §13.11.3 "so a dropped artifact collides with no other artifact" (SPEC-5 Edit 12); §11 (SPEC-6 Edit 8) |
| `podium lint` meets a declared `extends:`, alone or beside an artifact that fails a lint rule | Exit 1; a `rejected: <id> (ingest.invalid_artifact): <reason>` line on standard error for every refused artifact, and the lint diagnostics of the remaining artifacts on standard output (CODE-4 step 8, TEST-7) | §4.6 "`podium lint` reports it as an error" (SPEC-1); §7.3.1 "`podium lint` against the source names the artifacts those diagnostics came from" (unchanged) |
| A workspace overlay `ARTIFACT.md` carries an `extends` key | Served as authored in `podium-mcp`, `podium sync`, and both SDKs; the key has no effect; no warning | §6.4 "The consumer applies no ingest rule to an overlay artifact and serves it as authored." (SPEC-3) |
| That overlay artifact is promoted to a shared layer | Ingest refuses it; `podium lint` reports it first | §6.4 "Promoting the artifact to a shared layer subjects it to the §4.6 rules" (SPEC-3) |
| Two registry-side layers contribute one ID, neither with `extends:` | Rejected as today, with the new reason `rename this artifact or remove the other copy first` | §4.6 *Collisions* (SPEC-1) |
| A stored different-ID child C@2.0.0 declares `extends:` beside a plain C@1.0.0 | `latest` serves 1.0.0; an exact or `@sha256:` load of 2.0.0, a HEAD, and a conditional load answer `registry.not_found`, never 304. Accepted (Q-1) | §4.6 "A stored row whose `ARTIFACT.md` carries that key is served by no read path." (SPEC-1) |
| A `latest` load of C with `If-None-Match` carrying the retired row's ETag | Full 200 for C@1.0.0 | §7.2 entity tag (SPEC-4 Edit 4); §4.7.6 (SPEC-2 Edit 11) |
| A stored same-ID overlay X@2.0.0 declares `extends: X` over a lower X@1.0.0, and the lower layer is the only layer that holds a row of X that is not retired | `latest` serves the lower layer's X@1.0.0; the lower layer ingests a new X version with no `ingest.collision` | §4.6 "Such a row is not a collision under the rule above." (SPEC-1) |
| After retirement, rows of X that are not retired remain in two registry-side layers | As D-7 *Two-layer IDs* states. Accepted | §4.6 "Manifests do not inherit", third paragraph (SPEC-1 Edit 4) |
| Every row of an ID declares `extends:`, including one with an empty pin | Absent from load, HEAD, `batchLoad`, search, domain listings, `dependents_of`, the admin diagnostic view, and the object route; no `visibility.denied` event | §4.6 (SPEC-1) |
| The unchanged legacy child is presented again | Dropped as `extends_removed`; `podium layer reingest` exits non-zero | §4.6 (SPEC-1); §7.3.1 (SPEC-4 Edit 6) |
| A retired row's `(id, version)` is presented with other bytes | `ingest.immutable_violation` | §4.6 "Its `(artifact_id, version)` stays immutable" (SPEC-1) |
| A no-version skill whose ID holds a retired `0.0.0-auto.1` | Receives `0.0.0-auto.2`; a revert to the served `auto.0` bytes is unchanged at `auto.0` | §7.3.1 "other than a row the §4.6 no-inheritance rule retires" (SPEC-4 Edit 9) and the unchanged duplicate-content and N clauses |
| A retired version declared `delegates_to:` and its ID has another served row | Its edges stay in `dependents_of`, and the ranking in-degree counts them tenant-wide. Accepted | §4.6 "Its `delegates_to:` and `mcpServers:` edges stay recorded by artifact ID." (SPEC-1 Edit 4); §4.7.3 "The index admits no edge whose source ID has no row other than rows the §4.6 no-inheritance rule retires." (SPEC-2 Edit 7) and the ranking exemption (unchanged) |
| A stored legacy `extends` dependency edge | Never returned or counted | §4.7.3 edge list (SPEC-2 Edit 6) |
| A row copied by `migrate-to-standard` or restored with its layer | Stays retired | §4.6 "Unlike a soft-deleted row, it is not served again when its layer is restored" (SPEC-1 Edit 4), keyed on the bytes |
| A 0.6.0 `podium-mcp` in `offline-first` or `offline-only` that cached a merged body from a 0.5.x registry | Keeps serving it after the registry upgrade until `podium cache prune --days 0`. After the prune, a `latest` load fetches from the registry, and when the bridge accepted the retired row as `latest`, the older answer is refused with `materialize.stale_resolution` until `podium cache reset-revisions`; an `offline-only` cache then needs an online refill. Accepted (D-9) | §6.5 "Content cache. Maps `content_hash` to manifest bytes + bundled resources. Forever (immutable by definition)." (unchanged) and the §6.5 reset-revisions triggers (SPEC-3 Edit 3) |
| A `podium-mcp` in `always-revalidate` with a cached retired record | HEAD answers 404 or another hash. On another hash the bridge refetches, and when it accepted the retired row as `latest` it refuses the older answer with `materialize.stale_resolution` until `podium cache reset-revisions`; otherwise it loads the older version. On a 404 it reports `registry.not_found` | §6.5 revision marks (unchanged) and the reset-revisions triggers (SPEC-3 Edit 3) |
| A 0.5.x consumer against a 0.6.0 registry | `extends_pin` is absent; a cached ETag that folded a pin no longer matches, and the consumer refetches. A 0.5.x `podium-mcp` that accepted a retired row as `latest` refuses the older answer with `materialize.stale_resolution` until `podium cache reset-revisions`, as a 0.6.0 one does | §7.2 entity tag (SPEC-4 Edit 4); §4.7.10 step 2 (SPEC-2 Edit 17); §6.5 reset-revisions triggers (SPEC-3 Edit 3) |
| An existing database keeps the `extends_pin` column | No observable effect; a 0.5.2 binary rolled back onto the database reads `''` for rows 0.6.0 wrote. Q-2 | §13.4 additive guarantee (unchanged) |
| Storage quota, reembedding, the deprecation flip, and the artifact-count quota | As D-7 *Exceptions* items 2 and 3 state. Accepted (non-goal) | §4.6 "Unlike a soft-deleted row" sentence (SPEC-1 Edit 4); §4.7.8 (unchanged) |
| `podium artifact scaffold --extends x` | Exit 2 with the undefined-flag usage error | §4.6 (SPEC-1) |

## Testing

Each test below cites its spec section. A step runs its levels once the packages it edits compile again, as the ordering constraints under the checklist describe.

### TEST-1. Unit tests for the predicate, the constants, and the collision reason

Targets: `pkg/manifest/parse_test.go`, `pkg/layer/invalid_test.go` (new), `pkg/layer/collision_test.go`, and `pkg/lint/skill_rules_test.go`.

- `pkg/manifest/parse_test.go`, `TestDeclaresExtends` (`// Spec: §4.6`). True for:
  - block `extends: a`;
  - flow-mapping frontmatter `{type: skill, extends: a}`;
  - quoted key `"extends": a`;
  - escaped double-quoted keys that do not contain the substring: `"\x65xtends": a` and `"ext\u0065nds": a`;
  - a double-quoted key split by a backslash line continuation in explicit-key form (a `? "ext\` line, an indented `ends"` line, and a `: a` line);
  - a `!!binary`-tagged key that decodes to `extends`, in implicit form (`!!binary ZXh0ZW5kcw==: a`), in explicit-key form (a `? !!binary ZXh0ZW5kcw==` line and a `: a` line), and in verbose form (`!<tag:yaml.org,2002:binary> ZXh0ZW5kcw==: a`);
  - a frontmatter block that starts with the byte order mark `\xff\xfe` followed by the UTF-16LE encoding of `type: context` and `extends: a` on two lines, and the same text as `\xfe\xff` followed by UTF-16BE, between ASCII `---` lines;
  - empty value `extends:` and `extends: ""`;
  - a key that arrives through `<<: {extends: a}`;
  - `extends: a` beside a field that fails the typed decode (`description: [x]`), which pins that the predicate does not depend on `ParseArtifact`.
- False for:
  - nil and empty `src`;
  - an implicit double-quoted key split across two lines (`"ext\` on one line and `ends": a` on the next, indented), which the YAML decoder refuses;
  - `extends` nested under another key;
  - the substring only in a value (`description: never extends anything`);
  - the substring only in the body after the closing `---`;
  - a document with no frontmatter block whose text contains `extends: a`;
  - malformed frontmatter that contains `extends:`, such as an unclosed flow mapping.
- The escaped, continuation, tagged, and UTF-16 true cases pin that the predicate is true for every key the 0.5.x typed decoder resolved to `Extends`. An implementor who adds a fast path under the D-3 IMPLEMENTOR'S CHOICE also adds a property test that the fast path and the decode agree on every input.
- `pkg/layer/collision_test.go`: rewrite `TestCollision_Reason` to pin the exact string `cross-layer collision: "finance/close" is already contributed by another layer; rename this artifact or remove the other copy first`, and keep the loop that asserts neither `Layer` nor `ExistingLayer` appears in the reason. Keep `TestCollision_Drop`. TEST-8 deletes `TestExtendsOverlays` at S8, because it calls `ExtendsOverlays`, which S3 deletes.
- `pkg/layer/invalid_test.go`, `TestExtendsRemovedDrop` (`// Spec: §4.6, §13.11.3`). It pins:
  - `ExtendsRemovedDrop("a/b") == Drop{ArtifactID: "a/b", Code: "ingest.invalid_artifact", Reason: ExtendsRemovedReason}`;
  - the exact `ExtendsRemovedReason` text;
  - `ExtendsRemovedConstraint == "extends_removed"`;
  - `InvalidArtifactCode == "ingest.invalid_artifact"`, moved from `pkg/layer/executable_test.go:59`, which TEST-8 deletes.
- `pkg/lint/skill_rules_test.go`, `TestLint_SkillMDExtendsKeyIsNotPodiumOnly` (`// Spec: §4.3.4, §4.6`). A skill whose `SKILL.md` carries a top-level `extends: other/skill` draws no `lint.skill_podium_only_field` diagnostic. Build it once with `skillTree` (`skill_rules_test.go:14-19`) and an `ARTIFACT.md` of `type: skill` and `version: 1.0.0`, and once with `plainSkillTree` (`:23-27`). The rule reports every key `IsPodiumOnlySkillField` matches as an error (`pkg/lint/skill_rules.go:36-50`), so this case fails while `extends` stays in `podiumOnlySkillFields`. CODE-1 carries the `skill_fields_test.go` assertion at S3.

### TEST-2. Server ingest integration tests

Targets: `pkg/registry/ingest/extends_removed_test.go` (new), `pkg/registry/ingest/ingest_test.go`, and `test/integration/reingest_collision_reason_test.go`. Every test cites `// Spec: §4.6, §7.3.1`.

Ingest-level cases in `extends_removed_test.go`:

- (a) An artifact that declares `extends:` lands in `Rejected` with `Code` `ingest.invalid_artifact`, `Details` exactly `{constraint: extends_removed}`, and `Reason` equal to `layer.ExtendsRemovedReason`. It is not stored (`GetManifest` returns `ErrNotFound`), and it writes no dependency edge. In the same test, a later ingest of the same ID from another layer, without `extends:`, is accepted with no `ingest.collision`.
- (b) A `type: hook` artifact declares `extends:` and omits `hook_action`, which `lint.required_field_missing` refuses (`pkg/lint/required_fields.go:46-50`). It is reported in `Rejected` as `extends_removed`, `LintFailures` is empty, and `Ingest` returns no `ErrLintFailed`. Do not use a missing `description`, which is no lint error for an `ARTIFACT.md`.
- (c) A refused `type: mcp-server` artifact with a `server_identifier`, beside an agent whose `mcpServers` names that identifier, yields no `mcpServers` edge (the `serverIdentifierIndex` reassignment).
- (e) A retired row in layer B, seeded through the store with `extends:` bytes in `Frontmatter`, does not block a new version of the same ID ingested in layer A.
- (f) Ingesting bytes identical to a stored retired row's bytes gives `Idempotent == 0`. The artifact has no `Ingested` entry and is in `Rejected` as `extends_removed`.
- (g) Ingesting a retired row's `(id, version)` with different bytes yields a `ConflictReport`, which surfaces as `ingest.immutable_violation`.
- (h) An auto-versioned skill stored at `0.0.0-auto.0`, with a retired `0.0.0-auto.1` row that adds `extends:` in the same layer: re-ingesting the `auto.0` bytes is classified unchanged at `auto.0`.
- (i) A no-version skill whose ID has a retired row at `0.0.0-auto.1` receives `0.0.0-auto.2`.
- (j) A same-layer context artifact whose prose links to the refused artifact's ID fails `lint.prose_reference`.
- (k) This case also cites §4.3.4. A skill whose `SKILL.md` carries a top-level `extends: other/skill` is ingested once with no `ARTIFACT.md` and once, under another ID, with an `ARTIFACT.md` overlay that omits the key. Each is accepted with an `Ingested` entry, is absent from `Rejected`, and is stored (`GetManifest` succeeds), and `LintFailures` is empty. A `core.Registry` over the same store serves each through `LoadArtifact`, which pins that the CODE-3 refusal reads `AuthoredBytes` and the CODE-5 predicate reads the stored `Frontmatter`, and that neither reads `SkillRaw`.
- (l) This case also cites §4.7.6 and §8.4, and it pins D-7 *Two-layer IDs*. Seed through the store, in three registered layers `base`, `team`, and `other` with distinct `IngestedAt` values in that order: a plain `ops/y@1.0.0` in `base`, a retired `ops/y@2.0.0` in `team` whose `ARTIFACT.md` declares `extends: ops/y`, and a plain `ops/y@3.0.0` in `other`, the row 0.5.x admitted under team's stored pin. Before that seeding, ingest into `other` a plain context `ops/z@1.0.0` and a skill `ops/s` with no `ARTIFACT.md` and no `metadata.version`, which receives `0.0.0-auto.0`. A `core.Registry` over the same store serves `ops/y@3.0.0` for a `latest` load. Re-ingesting `base` with its unchanged `ops/y@1.0.0` bytes, and `other` with a source that holds only its unchanged `ops/y@3.0.0` bytes, each lands `ops/y` in `Rejected` with `ingest.collision`, gives `Idempotent == 0`, and stores nothing. After `DeleteLayerConfig` soft-deletes `other`, the `base` re-ingest is idempotent with no rejection, the `latest` load serves `ops/y@1.0.0`, and `LoadArtifact` of `ops/z` and of `ops/s` answers `registry.not_found`. The case then pins the D-7 *Two-layer IDs* re-registration order:
  - Before any purge, register a layer `other2` with `PutLayerConfig` and ingest into it a source holding the unchanged `ops/z` and `ops/s` files and no `ops/y`. `ops/s` lands in `Rejected` with `ingest.invalid_artifact` and `details` `{constraint: auto_version_duplicate_content}`, and `LoadArtifact` of `ops/z` and of `ops/s` still answers `registry.not_found`. Do not assert the `Accepted` count, because ingest counts the unchanged `ops/z` accepted although `PutManifest` writes no row for it.
  - Call `PurgeExpiredLayerDeletions` with a cutoff after the `DeleteLayerConfig` call, then ingest the same source into `other2` again. Both artifacts are accepted, `LoadArtifact` serves `ops/z@1.0.0` and `ops/s@0.0.0-auto.0`, and the `latest` load of `ops/y` still serves `ops/y@1.0.0`.

Existing-file edit in `ingest_test.go`: in `TestIngest_PopulatesDependencyEdges` (`:327-400`), delete the `extends: shared/parent@1.0.0` line of the dependent, the parent ingest and its comment, and the `{"shared/parent", "extends"}` case. Its dependent is otherwise refused with `extends_removed`, which would fail the surviving `delegates_to` and `mcpServers` edge assertions too. Rewrite its doc comment to name the `delegates_to` and `mcpServers` edges, and keep `// Spec: §4.7.3`.

Wire cases in `test/integration/reingest_collision_reason_test.go`, reusing `rcReingest` and `rcReingestStatus` with the extends parents dropped from the seed:

- Rewrite `TestReingest_CollisionReasonNamesNoLayer`: the reason contains "rename" and the quoted ID, it does not contain "extends", and the body names no hidden layer.
- Replace `TestReingest_ExecutableInheritanceDetails` with `TestReingest_ExtendsRemovedDetails`: a 200 response's `rejected` entry carries code `ingest.invalid_artifact` and `details` exactly `{"constraint":"extends_removed"}`.
- Replace `TestReingest_ExecutableInheritanceUnderLintEnvelope` with an `extends:` artifact beside a lint-failing hook: the response is a 422 `ingest.lint_failed` envelope with no `rejected` member and no `extends_removed` substring, and the `extends:` artifact is not stored.

TEST-7 covers the `podium layer reingest` exit status end to end, and `cmd/podium/layer.go:589-600` already counts every rejection.

### TEST-3. Filesystem walk, sync, and mode-equivalence tests

Targets: `pkg/registry/filesystem/walk_test.go`, `pkg/sync/sync_collision_test.go`, `pkg/sync/overlay_test.go`, `test/integration/sync_equivalence_test.go`, `test/integration/sync_filesystem_test.go`, and `test/e2e/filesystem_sync_test.go`.

- **Walk** (`walk_test.go`, `// Spec: §4.6, §6.4, §13.11.3`). A table test of the refusal:
  - Under `CollisionPolicyDrop`, `OnDrop` receives `layer.ExtendsRemovedDrop(id)`.
  - Under `CollisionPolicyDefault`, the walk errors naming the ID, the layer, and `ingest.invalid_artifact`.
  - Under `CollisionPolicyHighestWins`, with and without `OnDrop`, the record is kept, its `ArtifactBytes` equal the authored bytes, and `OnDrop` receives nothing.
  - A lower-layer X that declares `extends:` is refused, and the plain higher-layer X is kept under `CollisionPolicyDrop` and `CollisionPolicyDefault` with no collision.
  - Two refusals in two layers and a collision are reported in layer order, then by canonical ID, with the refusals before the collision.
  - A skill whose `SKILL.md` carries a top-level `extends:`, once with no `ARTIFACT.md` and once with an `ARTIFACT.md` overlay that omits the key, is kept under `CollisionPolicyDrop` and `CollisionPolicyDefault`, the walk returns no error, and `OnDrop` receives nothing (`// Spec: §4.3.4, §4.6`).
- Rewrite `TestWalk_CollisionWithExtendsAllowed` as "a same-ID higher copy declaring `extends:` on the ID is refused with `extends_removed`, and the lower copy is kept". Delete `TestWalk_CollisionExtendsOtherIDStillFails` and the two extends cases of `TestWalk_DropPolicy`. TEST-8 deletes `TestExtendsOf` and `TestWalk_DropPolicyDroppedRecordIsNotAParent` at S8, because they name `extendsOf` and `WalkOptions.ResolveExtends`, which S3 deletes.
- **pkg/sync** (`sync_collision_test.go`): change `assertOneCollision` to require the new reason (it contains "rename" and does not contain "extends"). Replace `TestRun_FilesystemCollisionResolvedByExtends` with a `Run`-level case: `Result.Dropped` holds the `ExtendsRemovedDrop`, the target and lock omit the artifact, and every other artifact materializes. Make no stderr or exit assertions here, because `Run` sets neither.
- **Overlay through sync** (`overlay_test.go`): keep `TestRun_OverlayWithExtendsReplacesWholesale` and its two subcases, which pin D-6, and rewrite its comment to cite §6.4 "applies no ingest rule to an overlay artifact and serves it as authored".
- **CLI** (`test/e2e/filesystem_sync_test.go`, `// Spec: §4.6, §13.11.3`): rewrite `TestFilesystemSync_ExecutableInheritanceDropsChild` in place as `TestFilesystemSync_ExtendsDeclarationDropsArtifact`. Its fixture `inheritingSyncRegistry` becomes a team-layer `team/notify` that declares `extends: core/notify`, beside the base-layer `core/notify` and `shared/other`. Keep its one-shot, `--dry-run`, and `--config` arms. Each arm asserts exit 1 and the standard-error line `rejected: team/notify (ingest.invalid_artifact): ` followed by `layer.ExtendsRemovedReason`. The one-shot arm also asserts that the lock and the target omit `team/notify` while `core/notify` and `shared/other` are materialized.
- `test/integration/sync_filesystem_test.go`: delete `TestPodiumSync_FilesystemSourceResolvesExtends` and `TestPodiumSync_FilesystemSourceRejectsCrossTypeExtends`.
- **Equivalence** (`sync_equivalence_test.go`, `// Spec: §11, §2.2, §4.6, §13.11.3`):
  - In `crossLayerCollisionRegistry`, add an `org-defaults` (lower layer) copy of a new ID, `shared/legacy`, whose `ARTIFACT.md` declares `extends:`, and a plain `team-finance` copy of `shared/legacy`.
  - Both consumers must materialize the `team-finance` copy. Pin it with `assertTreeHolds`, because a walk that dedupes before refusing would keep neither copy, while the server keeps the higher one.
  - `fsRes.Dropped` equals `[ExtendsRemovedDrop("shared/legacy"), the existing shared/clash collision]`, in that order, and `srvRes.Dropped` is empty.
  - Update the comments at `:47-52`, `:205`, `:384`, and `:459` to drop the extends wording.
  - Keep the existing `// Matrix: §6.10 (ingest.collision)` and add no `// Matrix:` line for `ingest.invalid_artifact`, which is not a matrix cell.

### TEST-4. Core read-path retirement and upgrade tests

Targets: `pkg/registry/core/retired_rows_test.go` (new), `test/integration/dependents_visibility_test.go`, `internal/serverboot/rehash_test.go`, and `test/e2e/boot_rehash_test.go` (or a new `test/e2e/retired_rows_upgrade_test.go` that reuses its standalone SQLite helpers). Cite `// Spec: §4.6, §13.4`, and §11 on the end-to-end test.

Fixtures, seeded through the store:

- (a) A different-ID child C@2.0.0 whose `ARTIFACT.md` declares `extends: P`, beside a plain C@1.0.0 and P.
- (b) A same-ID overlay X@2.0.0 that declares `extends: X` in a higher layer, over a plain X@1.0.0 in a lower layer.
- (c) An all-retired ID R whose only row declares `extends:` and carries an empty pin.
- (d) An ID D with a plain D@1.0.0 and a retired D@2.0.0 that also declares `delegates_to: [T]`.
- (e) An all-retired ID E whose only row spells the key with an escape, `"\x65xtends": P`, so its stored bytes do not contain the substring `extends`.
- (f) An all-retired ID F whose only row spells the key with a binary tag, `!!binary ZXh0ZW5kcw==: P`, so its stored bytes contain neither the substring `extends` nor a backslash.
- (g) An all-retired ID U whose only row's `ARTIFACT.md` frontmatter block starts with `\xff\xfe` and carries `type: context`, `version: 1.0.0`, and a top-level `extends: P` in UTF-16LE between ASCII `---` lines, so its stored bytes contain none of the substring `extends`, a backslash, and `!`.

Assertions:

- A `latest` load of C serves 1.0.0. An exact load of C@2.0.0 gets `registry.not_found`, and so does a load by `@sha256:` of its hash. HEAD and `If-None-Match` on C@2.0.0, by exact version and by hash, get 404, never 304.
- A `latest` load of C carrying `If-None-Match` with the retired row's ETag gets a full 200 for C@1.0.0.
- A `latest` load of X serves X@1.0.0.
- R is absent from load, HEAD, `artifacts:batchLoad`, `search_artifacts`, the `load_domain` listings, `dependents_of` results, an admin diagnostic load, and the object route, which proves D-7 keys on the bytes and not the pin. A load of R emits no `visibility.denied` audit event; assert on the event rather than on the unexported `artifactExistsAnywhere`.
- `dependents_of(T)` returns the D→T `delegates_to` edge, because D has a servable row (CODE-5 (c)).
- `Quota` reports a `StorageBytes` that includes the stored `ARTIFACT.md` and body bytes of R, which pins the SPEC-1 Edit 4 statement that the §4.7.8 quota read counts a retired row.
- E, F, and U are absent from `load_artifact` and from `search_artifacts`, which pins that the predicate finds an escaped, a tagged, and a UTF-16-encoded key on a stored row.
- The memo returns the same verdict on a second listing and recomputes for a row stored under a reused `(id, version)` with other bytes.
- `TestSearchArtifacts_RetiredRowVectorsDoNotSurface` (`// Spec: §4.6, §4.7`) pins CODE-5 (d). It lives in package `core_test` beside `hybrid_test.go`, so it reuses `basis` and `scriptedEmbedder` (`pkg/registry/core/domain_search_test.go:76-95`). The registry holds fixtures (a) and (b) and a plain G with G@1.0.0 and G@1.1.0, and it is built with `WithVectorSearch(vector.NewMemory(dim), scriptedEmbedder{...})`, where the embedder maps the query `xyzzy`, which no row's text contains, to one basis vector. Put that same vector, through `vector.Provider.Put`, for C@2.0.0, X@2.0.0, and G@1.0.0 only. `SearchArtifacts` with query `xyzzy` reports `Degraded` false, returns G at 1.1.0, which shows that the hybrid path ran and that a non-retired older version's vector still surfaces its ID, returns neither C nor X, and reports `TotalMatched` 1.
- `TestServableRows_ConcurrentListingsAndIngest` (`// Spec: §4.6`) runs parallel goroutines that issue `SearchArtifacts`, `LoadArtifact`, and `DependentsOf` through the `core.Registry` API against a store holding fixtures (a) to (g), while another goroutine stores new plain and retired rows with distinct content hashes, including one retired row stored under a purged and reused `(id, version)`. The writes cover first-time memo population and the reused-key recompute. Each goroutine asserts that no retired row appears in any answer and that every plain row it expects appears. The test comment states that it must pass under `-race`, because the memo map is written on the read path of concurrent requests (`.claude/rules/code-best-practices.md`, "Concurrency and resource cleanup"), and the S11 command runs it with `-race`.

Existing-file edits:

- `dependents_visibility_test.go`: add one case where the stored edge of an all-retired source ID is not returned. TEST-8 replaces the hidden-parent extends case at S8.
- `rehash_test.go`: comment-only change at `:356` and `:368`. The seed is already plain.

End to end: start the standalone binary once on a SQLite store at `PODIUM_SQLITE_PATH` and stop it. With `database/sql`, run `ALTER TABLE manifests ADD COLUMN extends_pin TEXT NOT NULL DEFAULT ''` and insert a child row whose frontmatter declares `extends: <parent>` with a non-empty pin, in a layer the boot reingest does not rewrite. Restart, then confirm the start succeeds, the child returns `registry.not_found`, and the parent serves. Run it with `-v` and grep for `SKIP`.

### TEST-5. Server response, ETag, and delivery tests

Targets: `pkg/registry/server/delivery_test.go` and `pkg/registry/server/load_artifact_head_test.go`. Leave `test/vectors/` untouched. TEST-8 rewrites `test/integration/delivery_attestation_test.go` at S8, because its `daFixture` rows set `ExtendsPin`, which S3 deletes.

1. CODE-6 deletes `TestLoadArtifactETag_FoldsTheServedExtendsPin` at S3. Confirm it is gone.
2. In `TestLoadArtifact_HeadReturnsContentHash` (`load_artifact_head_test.go`), add one assertion, `resp.Header.Get("ETag") == "\"" + hash + "\""`, citing `// Spec: §7.2`. The full-GET and 304 assertions already exist in `load_artifact_etag_test.go:37-72` and `delivery_test.go:162`. Do not duplicate them.
3. In `delivery_test.go`, drop `team/child` from `deliveryTree` (`:29`). Reduce `TestLoadArtifact_UniformAttestationAcrossMergedAndPlain` to a check that `team/plain` carries a `sha256` `delivery_hash` whose signature verifies, keeping its §4.7.10 citation, and rewrite the `deliveryTree` comment.
4. Leave the "mistyped extends_pin is ignored" vector (`test/vectors/response_cases_test.go:81-82`, `test/vectors/delivery-record.json:145`) and `pkg/version/served_test.go:138` unchanged. They pin the surviving §4.7.10 step-2 rule that every other member is ignored.

Add no test asserting that `extends_pin` is absent: the struct field no longer exists, and the spec names no such requirement.

### TEST-6. Consumer overlay and podium-mcp fixture tests

Targets: `cmd/podium-mcp/overlay_load_test.go:227-264`, `cmd/podium-mcp/cache_mode_test.go:451-485`, `cmd/podium-mcp/head_revalidate_test.go:76-83`, `cmd/podium-mcp/materialize_pipeline_test.go:328-365`, `sdks/podium-py/tests/test_resolution_overlay_login.py`, `sdks/podium-ts/src/resolution_overlay_login.test.ts`, and `sdks/podium-py/tests/test_client.py:323`, `:332`.

1. `podium-mcp`: rewrite `TestLoadArtifactFromOverlay_ExtendsChildServesTheAuthoredContentHash` as `TestLoadArtifactFromOverlay_ExtendsKeyIsServedAsAuthored` (`// Spec: §6.4`). An overlay `ARTIFACT.md` carrying `extends: acme/base@1.0.0` is served byte-for-byte as authored, its content hash frames the authored bytes, and the key has no effect on the served record.
2. Python and TypeScript (`// Spec: §6.4`): one case each in the overlay test file. An overlay package whose `ARTIFACT.md` carries a top-level `extends:` key is served by `load_artifact` as authored and replaces the registry artifact of that ID, and no warning is raised.
3. Remove the extends dependence from `podium-mcp` fixtures:
   - Delete `TestOfflineOnly_DifferingPinsServeEachChildsMergedDocument`.
   - Drop `extends: ops/base` from the `head_revalidate_test.go:83` fixture, and rewrite the `:76-78` comment so it no longer cites the extends validator.
   - Rewrite the two `TestDeliver_MergedManifest*` tests (`materialize_pipeline_test.go:328-365`) as the §4.3.4 synthesized-skill divergence, where the served frontmatter differs from the bytes the content hash frames. The served-versus-hashed check they pin survives under D-13.
4. Change the Python dependents fixture kind to `delegates_to` (`test_client.py:323`, `:332`).

Add no `podium-mcp` cache case, because the `podium-mcp` code is unchanged. The `always-revalidate` 404 on a retired row is a server behavior TEST-4 pins against the real core, and the `offline-first` stale serve is unchanged behavior that `offline_first_test.go` already covers. The revision-mark refusal D-9 describes is the existing §6.5 behavior for a lowered `latest`, which `TestDeliveryFreshness_PersistedMarkRefusesStaleLatest` (`test/e2e/delivery_freshness_test.go:33`, `// Spec: §6.5, §4.7.10`) pins end to end: a mark recorded at a later revision refuses a fresh `latest` answer for the earlier version with `materialize.stale_resolution`, and after `podium cache reset-revisions` the earlier version loads. `TestFreshness_StaleLatestRefusedAndWritesNothing` (`cmd/podium-mcp/delivery_freshness_test.go:344`) pins the refusal in process. A retirement lowers `latest` the way those tests do, so a retirement-specific case would repeat them, and S102 step 7 exercises the sequence on a real retired row. The CHANGELOG documents the remedy (DOC-3).

### TEST-7. End-to-end CLI tests

Targets: `test/e2e/standalone_server_test.go`, `test/e2e/filesystem_sync_test.go`, `test/e2e/cli_reference_test.go`, and `cmd/podium/artifact_scaffold_test.go`. Record subprocess coverage with `GOCOVERDIR` as `.claude/rules/test-coverage.md` describes, and run with `-v` and grep for `SKIP`.

- Sync: delete `TestFilesystemSync_SandboxProfileConflictDropsChild`, and update `assertCollisionReported` (`filesystem_sync_test.go:947-957`) to assert the rename-or-remove remedy in place of "declare extends:". TEST-3 owns the rewrite of `TestFilesystemSync_ExecutableInheritanceDropsChild`.
- Lint (`// Spec: §4.6, §7.3.1`): add `TestLint_ExtendsRefusalBesideLintFailure` to `test/e2e/standalone_server_test.go`, beside `TestStandaloneServer_LintValidInvalid`. Its layer holds `ops/c`, a context `ARTIFACT.md` that declares `extends: ops/base`, `ops/d`, a second one that declares `extends: ops/base`, and `invalid`, a context `ARTIFACT.md` that omits `version`, which the linter rejects. Assert exit 1, that standard error holds `rejected: ops/c (ingest.invalid_artifact): ` followed by `layer.ExtendsRemovedReason` and the same line for `ops/d`, in that order, and that standard output holds an error diagnostic naming `invalid`. In a second run over a layer whose only artifact declares `extends:`, assert exit 1, the `rejected:` line, and no `lint: no issues.` line. These cases pin CODE-4 step 8: an aborting walk names only `ops/c` and no diagnostic of `invalid`. In a separate `podium lint` run over a layer whose only artifact is a skill whose `SKILL.md` alone carries `extends:`, assert exit 0 and no `lint.skill_podium_only_field` line (`// Spec: §4.3.4, §4.6`).
- Scaffold (`// Spec: §4.6`):
  - Rewrite `cli_reference_test.go:2264-2276` so that `--extends x` exits 2 for both `--type skill` and `--type context`, writes no directory, and reports the undefined flag on standard error.
  - In `cmd/podium/artifact_scaffold_test.go`, drop `--extends` from the `:131` and `:141` refusal case and its expected message, and delete the `:919-958` extends tests.
- Standalone (`// Spec: §4.6, §7.3.1`):
  - `podium serve --standalone --layer-path` on a layer that holds an `extends:` artifact boots, and the artifact is absent from search and `load_artifact`.
  - `podium layer reingest` then exits non-zero and prints `rejected: <id> (ingest.invalid_artifact): ` followed by `layer.ExtendsRemovedReason`. Do not assert the `extends_removed` token, because the CLI does not print `details`. TEST-2 asserts `details.constraint` over HTTP.
  - Rewrite `TestStandaloneServer_ImpactAnalysis` to build its edge with an agent that declares `delegates_to: [parent]`.

### TEST-8. Delete or rewrite the existing extends tests

Rationale: tests of removed behavior are deleted. Tests that reference removed identifiers (`ManifestRecord.ExtendsPin`, `Artifact.Extends`, `dependency.EdgeExtends`, the `"extends"` edge kind, `layer.ExtendsOverlays`, and the merge, executable, and sandbox helpers) are rewritten so they compile and pin the surviving behavior. speccov-drift fails on a citation to a section that no longer exists (`tools/speccov/main.go:132`). The spec edits remove no numbered section, so drift is satisfied as long as no surviving test cites a removed section. Every section and §6.10 matrix cell the deleted files cite is already cited by a surviving test, so no assertion has to move to keep a citation.

The targets below need a disposition that the gate at the end of this section does not imply: a helper move, a compile-order constraint, an assertion move that keeps a speccov-drift citation, or a rewrite whose target another section cites. The IMPLEMENTOR'S CHOICE after the list covers every other file the gate lists.

- `pkg/layer/collision_test.go`: delete `TestExtendsOverlays`, which calls `ExtendsOverlays`. TEST-1 rewrites the rest of the file at S9.
- `pkg/registry/core/artifact_revision_test.go`: `TestLoadArtifact_CarriesServedArtifactRevision` builds `arRegistry` from an `extends:` child with `epFile` (`extends_pin_test.go:42`), `parentArtifact` (`extends_test.go:17`), and `childArtifact` (`extends_test.go:27`), all in files this deliverable deletes, and ingest would now refuse the child, so its `res.Accepted != len(in.files)` check fails (`artifact_revision_test.go:30-36`). Rewrite `arRegistry` over two plain artifacts with distinct ingest times: `shared/parent@1.2.0` in L1 at `parentAt` and `finance/child@2.0.0`, declaring no `extends:`, in L2 at `childAt`. Inline the `ARTIFACT.md` bodies and a local `fstest.MapFile` helper in the file, so it compiles after the deletions. Rename the "latest extends child" case to "latest", keep the pinned, session, parent, and revalidation cases, rewrite the `arRegistry` and test doc comments without the merged-child clause, and keep the `// Spec: §4.7.10, §4.7.6` citation.
- `pkg/registry/filesystem/walk_test.go`: this step deletes `TestExtendsOf` (`:805`, which calls `extendsOf`), `TestWalk_DropPolicyDroppedRecordIsNotAParent` (`:464`, which sets `ResolveExtends`), and `TestWalk_ExtendsChildInheritsPlainSkillParentDescription` (`:964`, whose `extends: ops/base` child asserts the removed description inheritance). It removes the `ResolveExtends` option from the surviving plain-skill tests (`:876`, `:916`, and `:945`), so the package compiles at S8.
- `test/e2e/artifact_extends_test.go`: delete it. In the same commit, move `exLoadResp` (`artifact_extends_test.go:48-56`) into `test/e2e/republish_helpers_test.go` under the name `loadArtifactResp`, with a doc comment naming the `load_artifact` fields the end-to-end tests assert, and rename its users: `git_source_journey_test.go:318`, `:325`; `multilayer_journeys_test.go:323`, `:470`, `:485`, `:553`, `:555`; `republish_helpers_test.go:193`, `:200`; and `republish_primitive_test.go:142`. `exParentID`, `extendsBoot`, and `exLoad` have no surviving user once the `frontmatter_schema_test.go` merge tests below are deleted, so they go with the file.
- The doc-claim end-to-end tests, which this step owns so that the suite passes from S15 (DOC-2 (g) only reruns them):
  - `test/e2e/frontmatter_schema_test.go:748-826`: delete the cross-layer merge comment block and `TestFrontmatter_MergeChildDescriptionWins`, `TestFrontmatter_MergeTagsUnion`, `TestFrontmatter_MergeSensitivityMostRestrictive`, `TestFrontmatter_MergeSearchVisibilityMostRestrictive`, `TestFrontmatter_MergeTypeMustMatch`, and `TestFrontmatter_ExtendsCycleDetected`, which call `extendsBoot` and `exLoad`. Drop the file's remaining extends claims, including the one at `:749`.
  - `test/e2e/core_concepts_test.go:155-212`: rewrite `TestCoreConcept_LayerPrecedenceOverride` as `TestCoreConcept_LayerCollisionRejected`, in which the high-layer copy of `shared/glossary` declares no `extends:`, the sync exits 1, standard error carries `rejected: shared/glossary (ingest.collision): `, and the target holds `low-description`. Rewrite `TestCoreConcept_LayerOrderAlphabeticalDefault` with a plain `b-layer` copy of `shared/item`: the sync exits 1 and drops the `b-layer` copy, which proves that `b-layer` sorts after `a-layer`, and the target holds `from-a`. Each keeps or gains `// Spec: §4.6, §13.11.3`.
  - `test/e2e/readme_claims_test.go:891-906` (`TestReadme_Impact`) and `test/e2e/deployment_modes_test.go:722-736` (`TestDeployment_Dependents`): the dependent `team/finance/child` becomes a `type: agent` that declares `delegates_to: [shared/parent]`, and the comments and failure messages name the `delegates_to` edge.
- `test/integration/license_change_advisory_test.go`: delete it. In the same commit, move `lcaWriteArtifact` and `lcaReingest` (`license_change_advisory_test.go:16-41`) into `test/integration/lint_caps_unlisted_test.go` as `lintWriteArtifact` and `lintReingest`, and rename their users at `lint_caps_unlisted_test.go:36`, `:58`, `:65`, `:106`, and `:111`.
- `pkg/layer/composer_test.go:256-287`: delete `TestMostRestrictiveSensitivity` and `TestAppendUniqueTags`, whose functions CODE-2 deletes.
- `test/integration/dependents_visibility_test.go:43-46`: replace the hidden-parent extends case, whose row sets `ExtendsPin`, with a `delegates_to` edge. TEST-4 adds the all-retired source case at S11.
- `test/integration/delivery_attestation_test.go`: before deleting its extends-based `daFixture` rows and tests, move each non-extends assertion to a plain or §4.3.4 synthesized-skill fixture, so speccov-drift keeps the §4.7.10 and §13.12 citations. The moved assertions include the synthesized-skill record, the large-manifest `manifest_body_url` fetch, and the `/objects` key access.
- `internal/serverboot/store_retention_test.go`: delete `TestRunStoreRetentionOnce_ProtectsExtendsPinnedParent` (`:86` sets `ExtendsPin`).
- `test/integration/plain_skill_ingest_test.go`: delete `psOverlay` and its comment (`:392-394`) only in the commit that removes its last user, and keep it while `TestPlainSkillIngest_TwoLayersShareTheAssignedSequence` or `TestPlainSkillIngest_ThirdLayerCollisionAndDuplicate` still uses it, so the package compiles at S8. CODE-7 drops the file's `ExtendsPin` references at S3.
- `test/integration/mcp_materialize_pipeline_test.go:157-205`, `TestPodiumMCP_MergedManifestDeliveryHashVerifies`, pins the §4.7.10 merged-manifest framing that SPEC-2 deletes. Rewrite it over the §4.3.4 synthesized-skill divergence, as TEST-6 item 3 does for the `cmd/podium-mcp` copy: a skill with no `ARTIFACT.md` whose served frontmatter is the synthesized document while its content hash frames a zero-length `ARTIFACT.md`. The consistent record materializes the served `ARTIFACT.md`, and the record with one served byte altered is rejected with `materialize.content_hash_mismatch` before any write. Keep its `// Spec: §6.6 step 2 / §4.7.10` citation.

**IMPLEMENTOR'S CHOICE:** how each file the completeness gate below lists, other than the files the bullets above dispose of, is rewritten or deleted — every surviving test passes at S8, or at the step of S9 to S15 that adds or rewrites it (allowlist category 6), with `-race` where it ran with `-race` before; no test asserts removed behavior; a moved assertion keeps its `// Spec:` citation so speccov-drift passes; a test of the purge, retention, or dependents paths asserts the post-D-7 and post-D-10 outcome (retired rows absent from reads, legacy `extends` edges neither returned nor counted); `go vet ./...` and the gate pass.

**Completeness gate.** The list above is not complete, and a grep replaces a complete per-file list. After this step, `grep -rlE 'extends|Extends' --include='*_test.go' pkg cmd internal test` lists only allowlisted files, and the S8 command runs it. A file is allowlisted when every remaining match in it does one of these:

1. asserts the §4.6 no-inheritance refusal, including the `--extends` usage error, or asserts that a message does not contain "extends";
2. asserts that a `SKILL.md` `extends` key is an ordinary key;
3. asserts that a workspace overlay artifact that carries the key is served as authored (D-6);
4. reads the shared `test/vectors/` cases, or pins the same mistyped-`extends_pin` rule (TEST-5 item 4);
5. builds a retired-row fixture, or builds a legacy `extends` edge fixture whose assertion is that the edge is neither returned nor counted;
6. sits in a test that a later step of S9 to S15 adds or rewrites, as that step's deliverable names it.

Remove every other match: delete the removed-behavior test, rewrite the surviving test, or drop the stale comment. Record each listed file and its category in the S8 commit message. The closing step reruns the grep and allows categories 1 to 5 only. The grep does not find a test that calls a deleted identifier without the word, such as `TestMostRestrictiveSensitivity`, and `go vet ./...` in the S8 command catches those.

CODE-12 owns `web/ui/src/surfaces.test.tsx` and `web/ui/src/propertytable.test.tsx`, and CODE-7 owns the compile-only edits to the integration files it lists, all of `test/integration/auth_cross_tenant_quota_test.go`, and `TestMemory_DependencyIndex` in `pkg/store/store_test.go`. Before deleting any test file, list its top-level declarations and grep its package for other users; move each helper a surviving test uses in the same commit, as the bullets above do for the helpers known today, because a surviving user that never mentions extends (`git_source_journey_test.go`, `republish_primitive_test.go`, `lint_caps_unlisted_test.go`) is outside the gate's grep. Keep `pkg/version/served_test.go:138` unchanged. Delete removed-behavior tests outright. Before deleting a file, check with `grep` that each surviving behavior it asserts is already pinned by a non-extends test, and add one only where none exists. The same-ID collision rejection is pinned by `reingest_collision_reason_test.go` and `sync_collision_test.go`, and `audit_redact` by `audit_redact_test.go` and `audit_redact_target_test.go`.

### Commands per step

- S1: `make coverage-gate`.
- S2: `go test ./test/conformance/ ./test/integration/ -run 'Reference|SyncEquivalence|SyncServerSource|ManifestBody'`, before S3. S8 runs it again.
- S3: `gofmt -l pkg cmd internal` prints nothing, then `go build ./...`.
- S4: `go test ./pkg/dependency/ ./pkg/version/ ./pkg/sign/`.
- S5: `go build ./cmd/podium/`.
- S6: `cd sdks/podium-ts && npm test`, and `cd sdks/podium-py && pytest`.
- S7: `cd web/ui && npm test && npm run build`, then `git status web/bundle` shows the rebuilt bundle staged.
- S8: `go vet ./...`, then `grep -rlE 'extends|Extends' --include='*_test.go' pkg cmd internal test`, with each listed file checked against the TEST-8 allowlist categories 1 to 6, then the S2 command against the new code, then `go test` over the tests TEST-8 rewrites, selected with `-run`, then `PODIUM_POSTGRES_DSN=<a reachable Postgres DSN> go test -v ./test/integration/ -run 'AuthCrossTenantQuota_NonInterferenceOverSharedPostgres|StoreRetention'` with a grep for `SKIP`, because the Postgres-backed test skips when the variable is unset or the database is unreachable, then `go test -v ./test/e2e/ -run 'Frontmatter|CoreConcept|Readme_Impact|Deployment_Dependents'` with a grep for `SKIP`.
- S9 to S13: each step runs `go test` over the packages its deliverable targets, with `-run` selecting the tests it adds or rewrites.
- S11, in addition: `go test -race ./pkg/registry/core/ -run 'ServableRows|Retired'`. No Makefile target or CI workflow runs the race detector, so this command is the only check on the memo's locking.
- S14: `go test ./cmd/podium-mcp/`, `pytest sdks/podium-py/tests`, and `npm test` in `sdks/podium-ts`. The command runs no `pkg/sync` test, because TEST-6 owns none, and the `pkg/sync` collision tests that fail after S3 pass once TEST-3 rewrites them at S10.
- S15: `GOCOVERDIR=$(mktemp -d) go test -v ./test/e2e/ -run 'FilesystemSync|Lint|Scaffold|StandaloneServer|Retired|BootRehash'`, grep the output for `SKIP`, then `go tool covdata textfmt` and `go tool cover -func`.
- S16: run S102 by hand.
- S17: `cd site && npm run build`, the site vitest suite, and `go test -v ./test/e2e/ -run 'Frontmatter|CoreConcept|Readme_Impact|Deployment_Dependents'` with a grep for `SKIP`. The pattern is the S8 pattern, because the test functions are named `TestFrontmatter_*`, `TestCoreConcept_*`, `TestReadme_*`, and `TestDeployment_*`, and a pattern that matches no function prints `no tests to run` and exits 0.
- Closing step: with `PODIUM_POSTGRES_DSN` set to a reachable Postgres, so that the Postgres store and the Postgres-backed integration tests run rather than skip, `go test -coverpkg=./... -coverprofile=cover.out ./pkg/manifest/ ./pkg/layer/ ./pkg/registry/... ./pkg/sync/ ./pkg/store/... ./internal/serverboot/ ./test/integration/`, then `go tool cover -func=cover.out`, and confirm that `DeclaresExtends`, `ExtendsRemovedDrop`, `refuseExtends`, `retired`, `assignVersion`, `servableRows`, `vectorRanks`, `validatorFor`, and the store `DependentsOf` and `DependencyInDegree` reach 85%. Then rerun the TEST-8 grep and confirm that each listed file falls in allowlist categories 1 to 5. Then `make lint` and `make coverage-gate`.

## Manual validation

**MV-1.** `test/manual-validation.md`: delete, rewrite, and add scenarios.

**Deletions.** Delete S37, S38, S39, S40, S42, S70, S71, S72, S91, S92, S93, S94, S95, S99, and S100, together with their index rows. Do not renumber, and do not reuse their IDs. Cross-references to the deleted IDs occur only inside other deleted scenarios.

**Rewrites.**

- **S05.** The expected reason ends with `rename this artifact or remove the other copy first`.
- **S77.** Rewrite the Goal and Covers so the overlay exception reads "a workspace overlay artifact replaces the registry-side artifact silently and is served as authored". Steps 1 to 4 change only the reason text in step 2's Expect. Replace step 5 and step 6:

  ````markdown
  5. Make the team copy declare `extends: shared/clash` and sync the
     filesystem target again.

     ```bash
     printf -- '---\ntype: context\nversion: 1.1.0\ndescription: from-team\nextends: shared/clash\n---\n\nfrom-team\n' \
       > "$WORK/reg/team-finance/shared/clash/ARTIFACT.md"
     podium sync --registry "$WORK/reg" --target "$WORK/fs" --harness none 2> "$WORK/err5.txt"; echo "exit=$?"
     cat "$WORK/err5.txt"
     cat "$WORK/fs/shared/clash/ARTIFACT.md"
     ```

     **Expect.** `exit=1`. `$WORK/err5.txt` holds one line,
     `rejected: shared/clash (ingest.invalid_artifact): extends: is not supported; remove the extends: key and declare every field this artifact needs in its own frontmatter`,
     and no `ingest.collision` line. The materialized file holds `from-org`.
     `exit=0` or `from-team` means the build still sanctions a same-ID
     overlay.

  6. Remove the team copy, add a workspace overlay artifact that carries
     `extends:` on the same ID, and sync with it.

     ```bash
     rm -rf "$WORK/reg/team-finance/shared/clash"
     mkdir -p "$WORK/overlay/shared/clash"
     printf -- '---\ntype: context\nversion: 9.0.0\ndescription: from-overlay\nextends: shared/clash\n---\n\nfrom-overlay\n' \
       > "$WORK/overlay/shared/clash/ARTIFACT.md"
     podium sync --registry "$WORK/reg" --target "$WORK/fs" --harness none \
       --overlay "$WORK/overlay" 2> "$WORK/err6.txt"; echo "exit=$?"
     cat "$WORK/err6.txt"
     cmp "$WORK/overlay/shared/clash/ARTIFACT.md" "$WORK/fs/shared/clash/ARTIFACT.md" && echo same
     ```

     **Expect.** `exit=0`, an empty `$WORK/err6.txt`, and `same`: the
     materialized file is byte-for-byte the overlay's authored `ARTIFACT.md`,
     including its `extends: shared/clash` line. A `rejected:` line, a
     warning, or a non-zero exit means the §6.4 exception applies an ingest
     rule to the overlay.
  ````

- **S10.** Give the team skill a distinct ID (`team-greet`), and delete the `ARTIFACT.md` overlay and the `sed` step. Change Covers to multiple Git-source layers and layer ordering. Expect both layers' skills to be served. S05 covers the collision.
- **S26.** Delete `deploy-pro`, its `ARTIFACT.md`, and the `sed` line. Change the Goal to "through `delegates_to` edges". `impact deploy-base` lists `release-agent` alone.
- **S41.** Retitle it "manifest-declared `audit_redact` over a forwarded audit stream" in the heading and the index row. Declare `audit_redact` and `x_account` on the loaded artifact itself, drop §4.6 from Covers, and change step 5's control text to "by the directive" in place of "by inheritance".
- **S51, S52, and S53.** Remove the `extends:` line from `deploy-runbook-strict` (`test/manual-validation.md:5877`) and keep the artifact. The S51 sentence that introduces the fixture (`:5867-5868`) states that the second runbook declares `tags` that omit the `runbook` tag the first carries and no longer says it extends the first. In S52 step 1, the third line's Expect (`:6030`) becomes `1` with `deploy-runbook`, and the explanatory paragraph (`:6035-6038`) states that `deploy-runbook-strict` declares `tags: [deploy, platform]` and does not match, because it declares no `runbook` tag. In S52 step 6, the `tag:runbook deploy` Expect (`:6074`) reads `Showing 1 of 1 match` for `eng/platform/deploy-runbook`. That line holds no "extends", so a sweep for extends wording misses it. S53 drops the "links an artifact to the one it extends" goal, the §4.3 merging Covers entry, and the inherited-tag and link expectations; the property table shows the two declared tags.

**Addition.** Add S102 after S101, and its index row after the S101 row:

```markdown
| S102 | An ARTIFACT.md that declares extends: is refused, and a stored extends: child stops serving after an upgrade | solo then standalone | none | none | none |
```

The scenario builds the `v0.5.2` binary itself, because MV-1 deletes S95, which held that block. The scenario number S102 and port 8207 are unused by other scenarios today. If another proposal takes them first, take the next free number and port and use them in the index row, the heading, and every reference.

````markdown
## S102: An ARTIFACT.md that declares extends: is refused, and a stored extends: child stops serving after an upgrade

**Goal.** Validate that `podium lint` and a filesystem-source `podium sync`
refuse an `ARTIFACT.md` that declares `extends:`, that a reingest refuses it
with `details.constraint: "extends_removed"`, that a child an earlier release
stored stops serving after the upgrade while its earlier version and the
lower layer's copy of a same-ID overlay serve, and that a `podium-mcp` in
`offline-first` that cached the merged child from the earlier registry keeps
serving it until `podium cache prune` clears the cache, after which its
revision mark refuses the older version until `podium cache reset-revisions`
clears the mark.

**Covers.** The §4.6 no-inheritance rule and collision rule, §7.3.1 ingest
outcome, §13.11.3 removed inheritance, §7.2 entity tag, and the §6.5 content
cache and revision marks, through the compiled binaries.

**Why by hand.** An operator reads the `rejected:` lines, the served version,
and the HTTP status directly and gates CI on `$?`. A served `2.0.0` for
`ops/c` after the upgrade, a 304 for the retired version, an `ingest.collision`
when the lower layer publishes `ops/x`, or `exit=0` from the filesystem sync is
the defect this scenario catches. The cache loads confirm the upgrade note the
CHANGELOG gives operators.

**Prerequisites.** Built `podium` and `podium-mcp` binaries on `PATH`, a Go
toolchain, and a checkout whose history holds the `v0.5.2` tag, a release that
still serves `extends:`.

**Steps.**

1. Run the isolation block from "Per-scenario isolation" above, then build
   the `v0.5.2` `podium` binary into `$WORK/old`.

   ```bash
   mkdir -p "$WORK/src" "$WORK/old"
   git -C "$PODIUM_REPO" archive v0.5.2 | tar -x -C "$WORK/src"
   go -C "$WORK/src" build -o "$WORK/old/" ./cmd/podium
   "$WORK/old/podium" version
   ```

   **Expect.** `which podium` prints `$PODIUM_BIN/podium`, and the old binary
   prints `podium 0.5.2`.

2. Build a two-layer registry. `org` holds `ops/base` and `ops/x` 1.0.0.
   `team` holds a plain `ops/c` 1.0.0.

   ```bash
   mkdir -p "$WORK/reg/org/ops/base" "$WORK/reg/org/ops/x" "$WORK/reg/team/ops/c"
   printf 'multi_layer: true\nlayer_order:\n  - org\n  - team\n' > "$WORK/reg/.registry-config"
   for L in org team; do
     printf 'visibility:\n  public: true\n' > "$WORK/reg/$L/.layer-config"
   done
   printf -- '---\ntype: context\nversion: 1.0.0\ndescription: Org base.\ntags: [base]\n---\n\nOrg base.\n' \
     > "$WORK/reg/org/ops/base/ARTIFACT.md"
   printf -- '---\ntype: context\nversion: 1.0.0\ndescription: Org x.\n---\n\nOrg x.\n' \
     > "$WORK/reg/org/ops/x/ARTIFACT.md"
   printf -- '---\ntype: context\nversion: 1.0.0\ndescription: Team c.\n---\n\nTeam c.\n' \
     > "$WORK/reg/team/ops/c/ARTIFACT.md"
   ```

   **Expect.** The three `ARTIFACT.md` files exist.

3. Serve the directory with the old binary. Then make `ops/c` 2.0.0 extend
   `ops/base`, add a same-ID overlay `ops/x` 2.0.0 in `team`, reingest `team`,
   and load both. Warm a `podium-mcp` cache in `offline-first` with `ops/c`,
   and stop the server.

   ```bash
   "$WORK/old/podium" serve --standalone --no-embeddings --layer-path "$WORK/reg" \
     --bind 127.0.0.1:8207 > "$WORK/old.log" 2>&1 &
   SRV=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8207/healthz
   server_alive "$SRV" "$WORK/old.log"
   export URL=http://127.0.0.1:8207
   printf -- '---\ntype: context\nversion: 2.0.0\ndescription: Team c.\nextends: ops/base\n---\n\nTeam c two.\n' \
     > "$WORK/reg/team/ops/c/ARTIFACT.md"
   mkdir -p "$WORK/reg/team/ops/x"
   printf -- '---\ntype: context\nversion: 2.0.0\ndescription: Team x.\nextends: ops/x\n---\n\nTeam x.\n' \
     > "$WORK/reg/team/ops/x/ARTIFACT.md"
   "$WORK/old/podium" layer reingest --registry "$URL" team; echo "exit=$?"
   for ID in ops/c ops/x; do
     curl -s "$URL/v1/load_artifact?id=$ID" \
       | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d["id"], d["version"], d.get("extends_pin"))'
   done
   INIT='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"m","version":"0"}}}'
   LOAD='{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"load_artifact","arguments":{"id":"ops/c"}}}'
   mcp_load() {
     printf '%s\n%s\n' "$INIT" "$LOAD" | PODIUM_REGISTRY="$URL" PODIUM_CACHE_DIR="$WORK/mcp" \
       PODIUM_CACHE_MODE=offline-first PODIUM_HARNESS=none PODIUM_MATERIALIZE_ROOT="$1" \
       podium-mcp > "$1.out" 2> "$1.err"
     grep -e '^version:' -e '^tags:' -e '- base$' "$1/ops/c/ARTIFACT.md"
   }
   mcp_load "$WORK/out-old"
   kill "$SRV"; wait "$SRV" 2>/dev/null
   ```

   **Expect.** The reingest prints no `rejected:` line and `exit=0`. The loads
   print `ops/c 2.0.0 ops/base@1.0.0` and `ops/x 2.0.0 ops/x@1.0.0`: the old
   release merged both. `mcp_load` prints `version: 2.0.0`, a `tags:` line,
   and a `- base` entry, the merged body: the old registry writes the merged
   `tags` as a block sequence, so the tag sits on its own line. The bridge
   also records a revision mark for `ops/c` at the 2.0.0 row's ingest time.
   A `rejected:` line here means the old
   binary already refuses `extends:`, and the later steps prove nothing. A
   missing `ARTIFACT.md` means the load failed; read `$WORK/out-old.err`.

4. Lint and sync the directory with the build under test.

   ```bash
   podium lint --registry "$WORK/reg" 2> "$WORK/lint.txt"; echo "lint exit=$?"
   cat "$WORK/lint.txt"
   podium sync --registry "$WORK/reg" --target "$WORK/fs" --harness none 2> "$WORK/err.txt"; echo "sync exit=$?"
   cat "$WORK/err.txt"
   cat "$WORK/fs/ops/x/ARTIFACT.md"; ls "$WORK/fs/ops"
   ```

   **Expect.** `lint exit=1`, and `$WORK/lint.txt` holds the same two
   `rejected:` lines the sync prints below, `ops/c` first, so lint names
   every refused artifact rather than stopping at the first. `sync exit=1`,
   and `$WORK/err.txt` holds two lines, `ops/c` first:
   `rejected: ops/c (ingest.invalid_artifact): extends: is not supported; remove the extends: key and declare every field this artifact needs in its own frontmatter`,
   then the same line for `ops/x`, and no `ingest.collision` line. The
   materialized `ops/x` holds `Org x.`, and `ls` prints `base` and `x`.

5. Start the build under test on the same store and directory, reingest the
   unchanged `team` layer, and read every path for the retired rows.

   ```bash
   podium serve --standalone --no-embeddings --layer-path "$WORK/reg" \
     --bind 127.0.0.1:8207 > "$WORK/srv.log" 2>&1 &
   SRV=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8207/healthz
   server_alive "$SRV" "$WORK/srv.log"
   grep 'ingested layer' "$WORK/srv.log"
   podium layer reingest --registry "$URL" team 2> "$WORK/re.txt"; echo "exit=$?"
   cat "$WORK/re.txt"
   for ID in ops/c ops/x; do
     curl -s "$URL/v1/load_artifact?id=$ID" \
       | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d["id"], d["version"], "extends_pin" in d)'
   done
   curl -s "$URL/v1/load_artifact?id=ops/c&version=2.0.0" | grep -o 'registry.not_found'
   curl -s -o /dev/null -w 'head %{http_code}\n' -I "$URL/v1/load_artifact?id=ops/c&version=2.0.0"
   curl -s "$URL/v1/search_artifacts?query=Team" \
     | python3 -c 'import json,sys; print(sorted((r["id"], r["version"]) for r in json.load(sys.stdin)["results"]))'
   podium impact --registry "$URL" ops/base
   curl -s -D - -o /dev/null "$URL/v1/load_artifact?id=ops/c" | grep -i '^etag'
   ```

   **Expect.** The `ingested layer` line for `team` reports `rejected=2`. The
   reingest prints `exit=1` and the two `rejected:` lines of step 4.
   The loads print `ops/c 1.0.0 False` and `ops/x 1.0.0 False`. The exact load
   prints `registry.not_found`, and the HEAD prints `head 404`. The search
   lists `('ops/c', '1.0.0')` and no `2.0.0`. `impact ops/base` lists no
   dependent. The `ETag` header is the quoted content hash of `ops/c` 1.0.0
   with nothing folded in. `2.0.0` from any path, `head 304`, or an `ops/c`
   dependent of `ops/base` means a retired row is still served.

6. Read the reingest body over HTTP, then publish a new `ops/x` from the
   lower layer.

   ```bash
   curl -s -X POST "$URL/v1/layers/reingest?id=team" > "$WORK/body.json"
   python3 -c 'import json,sys; print(json.dumps(sorted(json.load(open(sys.argv[1]))["rejected"], key=lambda r: r["artifact_id"])))' "$WORK/body.json"
   printf -- '---\ntype: context\nversion: 1.1.0\ndescription: Org x.\n---\n\nOrg x one.\n' \
     > "$WORK/reg/org/ops/x/ARTIFACT.md"
   podium layer reingest --registry "$URL" org; echo "exit=$?"
   curl -s "$URL/v1/load_artifact?id=ops/x" | python3 -c 'import json,sys; print(json.load(sys.stdin)["version"])'
   ```

   **Expect.** Each `rejected` entry carries `"code": "ingest.invalid_artifact"`
   and `"details": {"constraint": "extends_removed"}` and no other `details`
   member. The `org` reingest prints no `rejected:` line and `exit=0`, and the
   load prints `1.1.0`. An `ingest.collision` here means the retired `team`
   row still counts as a collision.

7. Load `ops/c` through the warm `offline-first` cache, prune it, load
   again, reset the revision mark, and load a third time.

   ```bash
   mcp_load "$WORK/out-warm"
   podium cache prune --dir "$WORK/mcp" --days 0
   mcp_load "$WORK/out-pruned"
   grep -o 'materialize.stale_resolution' "$WORK/out-pruned.out"
   podium cache reset-revisions --dir "$WORK/mcp" ops/c; echo "exit=$?"
   mcp_load "$WORK/out-reset"
   ```

   **Expect.** The warm load prints `version: 2.0.0`, a `tags:` line, and a
   `- base` entry: `offline-first` serves the record it cached in step 3
   without calling the registry, as D-9 accepts and the CHANGELOG states.
   After the prune the bridge finds no cached content and calls the
   registry, which serves `ops/c` 1.0.0 at an ingest time below the mark
   step 3 recorded. The bridge refuses the load and writes no file, so the
   pruned load's `grep` reports a missing `ARTIFACT.md`, and
   `out-pruned.out` holds `materialize.stale_resolution`. The reset prints
   `cache: reset 1 revision mark(s)` and `exit=0`, and the third load
   prints `version: 1.0.0` with no `tags:` line and no `- base` entry.
   `version: 2.0.0` after the prune means the prune left the merged record
   in place. `version: 1.0.0` before the reset means the bridge did not
   compare the answer with its revision mark. A cache filled by a `v0.5.2`
   bridge is a miss under the 0.6.0 cache layout and does not reproduce the
   warm load.

8. Clean up.

   ```bash
   kill "$SRV"; rm -rf "$WORK"
   ```
````

## Documentation changes

### DOC-1. Delete the extends page and diagram after moving their surviving content

Targets: `docs/authoring/extends.md`, `docs/assets/diagrams/extends-inheritance.svg`, `site/test/fixtures/published-routes.json:11`, `:33`, `docs/deployment/layers.md` (new destination sections), `docs/consuming/custom-via-sdk.md:133`, `docs/deployment/local.md:107`, `docs/authoring/index.md:21`, `docs/authoring/your-first-skill.md:180`, `docs/authoring/your-first-agent.md:184`, `docs/authoring/frontmatter-reference.md:241`, `:316`, and `docs/getting-started/concepts.md:135`, `.claude/workflows/docs-audit-pass-2.js:46`, `:48`, and `.claude/workflows/docs-review-double-verified.js:32`.

Most of the page documents the removed feature. Two sections state rules that survive, and three pages cite one of them by anchor. `## Workspace overlay` (`extends.md:220-224`) holds the overlay exception to the collision rule, which SPEC-3 keeps. `## Replacing instead of extending` (`:212-216`) holds the remove-or-rename replacement procedure, which D-2 makes the only remedy.

1. Add a `## Workspace local overlay` section to `docs/deployment/layers.md`. It states that an overlay artifact replaces the registry-side artifact of the same ID in `podium-mcp`, `podium sync`, and the SDKs, that no consumer reports the replacement, and that the consumer applies no ingest rule and serves the overlay artifact as authored, including an `ARTIFACT.md` that carries an `extends` key, which has no effect. It states that copying the artifact into a shared layer subjects it to the collision rule and the no-inheritance rule, and that `podium lint` reports an `ARTIFACT.md` that the no-inheritance rule refuses. It makes no broader lint claim, because `podium lint` contacts no registry (SPEC-3 Edit 2).
2. Fold the replacement procedure from `## Replacing instead of extending` into the `layers.md` collision paragraph that DOC-2 rewrites: remove or rename the lower copy first, then ingest the replacement.
3. Retarget the three `../authoring/extends#workspace-overlay` links (`custom-via-sdk.md:133`, `local.md:107`, `layers.md:17`) to the new `layers.md` anchor.
4. Delete the `[Extends](extends)` row in `docs/authoring/index.md:21` and the extends bullets and sentences at `your-first-skill.md:180`, `your-first-agent.md:184`, `frontmatter-reference.md:241` and `:316`, and `concepts.md:135`. The merge-table links at `frontmatter-reference.md:306`, `:312` and `frontmatter-schema.md:190`, `:196` go with the sections that DOC-2 deletes.
5. Delete `docs/authoring/extends.md` and `docs/assets/diagrams/extends-inheritance.svg`. In the same commit, remove the page from the audit partitions that name it: `docs/authoring/extends.md` from the list at `.claude/workflows/docs-audit-pass-2.js:46` and `extends` from the page list at `.claude/workflows/docs-review-double-verified.js:32`. Rewrite the `:48` focus text of `docs-audit-pass-2.js` to drop "what extends inherits and what it overrides".
6. Regenerate the fixture with `node site/test/fixtures/capture-routes.mjs docs > site/test/fixtures/published-routes.json`, and confirm that the only diff is the removal of `/authoring/extends.html` and `/assets/diagrams/extends-inheritance.svg`.
7. Run `cd site && npm run build` and the site vitest suite (`routes.test.ts`: no diagnostics, every corpus-level check passes, every page reachable from nav). Then confirm that `grep -rn "authoring/extends" docs README.md site deploy web/DESIGN.md web/design/README.md .claude --exclude-dir=node_modules --exclude-dir=dist` returns nothing.

`extends.md` has only markdown and YAML fences, which `tools/doccov/scan.go` does not classify as runnable, and it has no doccov entry, so `tools/doccov/manifest.yaml` is unchanged.

### DOC-2. Rewrite the doc pages and diagrams that mention extends:

Targets: `README.md`; `docs/overview.md`; `docs/getting-started/{concepts,how-it-works,why-podium}.md`; `docs/authoring/{index,frontmatter-reference,your-first-agent,your-first-skill}.md`; `docs/consuming/{browsing-the-catalog,custom-via-sdk}.md`; `docs/deployment/{clustered,extending,index,layers,local,single-node}.md`; `docs/reference/{cli,error-codes,frontmatter-schema,http-api}.md`; `docs/consuming/configure-your-harness.md`; `docs/assets/diagrams/{layer-composition,registry-layout,identity-visibility-flow}.svg`; `deploy/runbook.md`; `web/DESIGN.md`; `web/design/README.md`; and `.claude/workflows/change-proposal.js:810`.

The docs describe the merge table, `extends_pin`, the extends ETag, the executable and sandbox rejections, the scaffold flag, and the extends collision remedy. Remove each extends sentence, and make these specific edits:

- (a) `layer-composition.svg`: replace the text block at `:144-146` with a two-line card body, "Layers the caller cannot see are silently excluded." Replace both footers at `:148` and `:149` with "Collisions between registry-side layers are rejected at ingest." Keep the `:150` footer. Update the ASCII fallback in `docs/getting-started/concepts.md` near `:152` to match, removing "hidden parents" and the extends line, and rewrite the `identity-visibility-flow` fallback at `concepts.md:206` the same way. Render both themes and the standalone file per `.claude/rules/doc-diagram-style.md`.
- (b) `frontmatter-schema.md`: delete the `:69` row and the whole artifact-merge portion of `## Cross-layer merge`, from `:177` through `:198`, including the `TypeProvider` sentence. Keep the `DOMAIN.md` merge text under a heading that names it, such as `## DOMAIN.md merge across layers`.
- (c) `deployment/extending.md`: the `:30` `LayerComposer` row reads "Resolves the caller's effective view from the configured layer list and applies the collision rule." The `:33` `TypeProvider` row drops "+ field-merge semantics". These mirror SPEC-6 Edit 4.
- (d) `http-api.md`: rewrite the ETag sentences at `:210` as "The entity tag is the content-hash entity tag on a full response, a HEAD, and a 304 alike," and delete the per-identity sentence. At `:249`, drop ", whether or not the artifact declares `extends:`". At `:664`, replace the merged-document clauses with "a skill's `SKILL.md`, or another type's stored `ARTIFACT.md`", which mirrors SPEC-5 Edit 13. Replace the executable-inheritance `details` example with one sentence: "An `ARTIFACT.md` that declares `extends:` is rejected with `details: {\"constraint\": \"extends_removed\"}`." In the reingest paragraph at `:528`, after "and lint failures only as the `lint_failures` count", add: "The no-inheritance refusal runs before lint, so an `ARTIFACT.md` that declares `extends:` appears in `rejected` and is never counted in `lint_failures`, whatever lint rule it also fails." This mirrors SPEC-4 Edit 7. In the `dependents` section, after "Each edge carries `from`, `to`, and `kind`." (`:363`), add: "An edge's `kind` is `delegates_to` or `mcpServers`. A stored edge of any other kind is never returned and never counts toward the search ranking signal." This mirrors SPEC-2 Edit 6 and the CODE-7 kind filter on `DependentsOf` and `DependencyInDegree`.
- (e) `layers.md`: delete the `:203` hidden-parent bullet and the sentence "A dropped copy cannot serve as an `extends:` parent." DOC-1 re-homes the overlay paragraph and the replacement procedure, and repoints the links at `local.md:107` and `custom-via-sdk.md:133`.
- (f) Prose that a search for "extends" misses: rewrite `concepts.md:133-135` ("inherits and refines") as an unconditional collision statement. Replace "deterministic merge, explicit precedence, and `extends:`" with "deterministic merge and explicit precedence" in `README.md:111`, `docs/overview.md:58`, `docs/getting-started/why-podium.md:81` and `:213`, the `docs/deployment/layers.md:4` frontmatter description, and `docs/deployment/index.md:41`. In the `why-podium.md` comparison table, which stays aligned with the §1.5 table that SPEC-6 Edit 10 rewrites, change the Podium row's cell "Ordered layers with `extends:`" (`:146`) to "Ordered layers", and in the *Where Podium applies* paragraph (`:156`) change "when ordered layers and `extends:` compose several sources" to "when ordered layers compose several sources". The `DOMAIN.md` merge keeps "deterministic merge" accurate, which matches SPEC-6 Edit 1.
- (g) The doc-claim end-to-end tests that pin the rewritten claims (`test/e2e/readme_claims_test.go`, `core_concepts_test.go`, `frontmatter_schema_test.go`, and `deployment_modes_test.go`) test behavior and read no doc text, and TEST-8 rewrites them at S8. This step changes no test file. It reruns them against the rewritten docs (the S17 command) and confirms that the doccov slugs in `tools/doccov/manifest.yaml` still resolve.
- (h) After editing, run `grep -rniE 'extends|hidden parent|merge semantics|field-merge|inherit' README.md docs/ deploy/ web/DESIGN.md web/design/README.md .claude/`. The text DOC-1 and DOC-2 add matches the pattern, so a remaining hit is acceptable when it falls in one of these categories:
  1. it states the §4.6 no-inheritance refusal of an `ARTIFACT.md` that declares `extends:`, its `extends_removed` constraint, or its drop, `rejected:` line, or `lint.prose_reference` consequence, as the DOC-2 (d), `cli.md`, `error-codes.md`, and (i) edits do;
  2. it states that an `extends` key in a skill's `SKILL.md` is an ordinary key, as the (i) section does;
  3. it states that a workspace overlay artifact that carries the key is served as authored, as the DOC-1 item 1 section does;
  4. it states the outcome for a row the no-inheritance rule retires, including the `## Inheritance` heading and section of `layers.md` and the "retired under the no-inheritance rule" and "other than a row the no-inheritance rule retires" wording of the `error-codes.md`, `configure-your-harness.md`, and `frontmatter-reference.md:103` edits;
  5. it is unrelated to the removed feature, such as "inherit the artifact's sensitivity", the "inherit the session model" sentences in `.claude/skills/`, the "merge semantics" focus text at `.claude/workflows/docs-review-double-verified.js:34`, and the code comments at `.claude/workflows/change-proposal.js:1813` and `:2049`.

  Remove every other hit. Record each remaining hit, by file, line, and category, in the S17 commit message. The exported design canvases `web/design/*.dc.html` and the JavaScript `class ... extends` keyword in `web/design/support.js` are outside the search (Non-goals). The `web/DESIGN.md:290` hit, "a search result whose inherited extension block could not be rewritten", is related and must be gone after the `web/DESIGN.md` edit below. The pattern does not match `web/DESIGN.md:266`, "§13.10 also requires links to extending or dependent artifacts", so confirm separately that `grep -n 'extending or dependent' web/DESIGN.md spec/13-deployment.md` returns nothing after the `web/DESIGN.md` edit and SPEC-5 Edit 8. The pattern omits "extending", because it matches the unrelated *Extending* page and its links throughout `docs/`. Then read each edge-case row that records a user-visible change from 0.5.2, and confirm that the DOC-3 Removed or Changed entries or the DOC-2 (i) section states its outcome or the rule the outcome follows from. Run `cd site && npm run build` with no broken links.
- (i) `layers.md`: add a `## Inheritance` section after the collision section, stating:
  - No artifact inherits from another. Ingest rejects an `ARTIFACT.md` whose frontmatter carries a top-level `extends` key, whatever its value, with `ingest.invalid_artifact` and `details.constraint: "extends_removed"`, before lint. A filesystem-source `podium sync` drops it and exits 1, and `podium lint` reports it as an error.
  - Another artifact of the same reingest whose prose links to a refused artifact fails `lint.prose_reference`.
  - An `extends` key in a skill's `SKILL.md` is an ordinary key and is not refused.
  - A row an earlier release stored from such an `ARTIFACT.md` is served by no read path. `latest` resolves to the artifact's most recent other version, a same-ID overlay serves the lower layer's copy when no other layer holds a served row of the ID, and an artifact with no other version answers `registry.not_found`.
  - Such a stored row is not a collision. When only one layer holds a served row of the ID, that layer ingests new versions of the artifact ID without `ingest.collision`, and an ID with no served row is ingested by any layer without it. The retired row still holds its `(id, version)`, so other bytes at that version are rejected with `ingest.immutable_violation`.
  - An earlier release also admitted a plain copy of the ID from a further layer while an overlay row was stored. When served rows of the ID remain in two or more layers, `latest` serves the most recently ingested of them, and every later ingest of the ID from those layers is rejected with `ingest.collision`, including a re-ingest of unchanged bytes. Unregister all but one of those layers (`podium layer unregister`), which soft-deletes every row of each unregistered layer, so its other artifacts stop serving too. Register a removed source again, without the artifact, only after the recovery window ends and the purge removes its soft-deleted rows. Before the purge, the soft-deleted rows still hold their artifacts' versions, so a registration under any layer ID serves none of the source's artifacts at an unchanged version, even when the ingest reports them accepted, and rejects a skill with no declared version as `auto_version_duplicate_content`. Remove a layer's declaration from the registry configuration before unregistering it, because a declared layer is re-seeded at each start, and declare it again only after the purge.
  - A retired version's `delegates_to` and `mcpServers` references stay in impact analysis while another version of the artifact is served.
  - A refused artifact collides with no other artifact, so the other copy of its ID is materialized or ingested, and no `ingest.collision` line is printed for it.
  - Retiring an artifact's newest rows lowers the version `latest` resolves to, so a `podium-mcp` that loaded a retired version as `latest` refuses the older one with `materialize.stale_resolution`. Stop every MCP server that uses the cache directory and run `podium cache reset-revisions <id>`. A `podium-mcp` in `offline-first` or `offline-only` that cached a merged artifact also needs `podium cache prune --days 0`, which leaves the revision marks in place.
- `error-codes.md`: the `ingest.invalid_artifact` row names the `extends_removed` constraint and the matching filesystem-sync drop, and drops the executable-inheritance and sandbox-profile clauses. The `ingest.collision` row gives the rename-or-remove remedy. In the `materialize.stale_resolution` row (`:127`), change "or after the newest version is purged, erased, or leaves the caller's view" to "or after the newest version is purged, erased, retired under the no-inheritance rule, or leaves the caller's view", which mirrors SPEC-3 Edit 3.
- `configure-your-harness.md:44`: change "or after the newest version is purged, erased, or leaves the caller's view" to "or after the newest version is purged, erased, retired under the no-inheritance rule, or leaves the caller's view".
- `cli.md`: the `podium sync` drop paragraphs at `:240` and about `:288` name the `extends_removed` drop beside the collision drop, the `podium lint` entry (`:186`) states that lint prints a `rejected: <id> (ingest.invalid_artifact): <reason>` line on standard error for every `ARTIFACT.md` that declares `extends:`, lints the remaining artifacts, and exits 1 (CODE-4 step 8), and the scaffold entry drops `--extends`. The `:240` collision sentence drops "where the higher-precedence copy declares no `extends:`". The `:242` executable and sandbox paragraph, including its recompose sentence, is replaced by: "An `ARTIFACT.md` that declares `extends:` is dropped before the collision rule and reported as `rejected: <id> (ingest.invalid_artifact): <reason>`. A dropped artifact collides with no other artifact, so the other copy of its ID is materialized and no `ingest.collision` line is printed for it." This mirrors SPEC-5 Edit 12.
- `deploy/runbook.md:71-84`, the object-storage outage *Impact* paragraph: admission reads the object-held bundled-resource bodies of the requested row only. "every row in the requested artifact's `extends:` chain, parents included" becomes "the requested row", "an artifact whose chain holds any object-held bundled-resource body" becomes "an artifact whose row holds any object-held bundled-resource body", and "Only a load whose whole chain holds inline bundled resources" becomes "Only a load whose row holds inline bundled resources". This mirrors SPEC-5 Edits 1 and 3.
- `docs/authoring/frontmatter-reference.md:103`: the first registry-assigned-version bullet becomes "Bytes that the layer's most recently ingested non-deleted row of the artifact, other than a row the no-inheritance rule retires, holds reuse that row's version, and the ingest is a no-op." The bullet picks the reuse candidate after it leaves retired rows out, the way SPEC-4 Edit 9 does, so when a newer retired `0.0.0-auto.1` follows a stored `auto.0`, a revert to the `auto.0` bytes is unchanged at `auto.0` and the `:105` bullet does not reject it. The bullets at `:104` and `:105` are unchanged, because a retired row stays stored and still counts toward them.
- `web/DESIGN.md:266`: "§13.10 also requires links to extending or dependent artifacts, so an artifact is a node in a graph" becomes "§13.10 also requires links to dependent artifacts (§4.7.3), so an artifact is a node in a graph", which mirrors SPEC-5 Edit 8 and the CODE-12 dependents-only rail. The rest of the paragraph is unchanged. `web/DESIGN.md:231-233`: the classification label is the row's own declared sensitivity (`ArtifactDescriptor.Sensitivity`, §4.7.4), with the values in spec §4.3; drop "resolved to the most restrictive value across an extends chain". `web/DESIGN.md:385-390`: every same-ID collision between registry-side layers is rejected at ingest (spec §4.6, "Collisions"), and the order sets precedence for the collision rule and for `DOMAIN.md` merging; delete the sentence about an extending artifact merging with its parent, and keep the reason the control shows the resulting order. `web/DESIGN.md:289-293`: delete the search-result producer, "a search result whose inherited extension block could not be rewritten, which carries no frontmatter block at all", because CODE-5 deletes the `FrontmatterHidingParent` search-descriptor path that produces it. The sentence then reads "The producer known today is a non-skill artifact response whose frontmatter is cleared because the manifest arrives by link instead of inline." The following sentence becomes "The response structs in `pkg/registry/server/server.go` own it.", and its clause that `docs/reference/http-api.md` carries the search-result case under `search_artifacts` is deleted, because DOC-2 removes that extends paragraph at `http-api.md:176`.
- `web/design/README.md:141`: RELATIONS lists dependent artifacts ("delegated to by" and "referenced by", fetched on a second request via `handleDependents`), and its absent state reads "Nothing depends on this artifact", matching CODE-12.
- `.claude/workflows/change-proposal.js:810`: change "layer composition (`extends:`) that resolves the wrong effective view" to "layer composition that resolves the wrong effective view".

### DOC-3. CHANGELOG [Unreleased] reconciliation

Targets: `CHANGELOG.md:16`, `:17`, `:21`, and the `### Changed` and `### Removed` lists of `[Unreleased]`.

1. Delete the entries at `:16` (0057) and `:17` (0063).
2. In `:21`, change "`--tags`, `--when-to-use`, `--extends`, or `--sensitivity`" to "`--tags`, `--when-to-use`, or `--sensitivity`".
3. Under `### Changed`, add:

```markdown
- **Every same-ID collision between registry-side layers is rejected** (§4.6, §13.11.3): a higher-precedence artifact can no longer overlay a lower one under the same canonical ID by declaring `extends:` on it. Ingest rejects the incoming copy with `ingest.collision`, and a filesystem-source `podium sync` drops the higher-precedence copy and exits 1. The reason now ends `rename this artifact or remove the other copy first`. The workspace overlay still replaces a registry-side artifact of the same ID and serves the overlay artifact as authored.
- **`pkg/registry/filesystem` reports walk drops through `WalkOptions.OnDrop func(layer.Drop)`** (§13.11.3): it replaces `WalkOptions.OnCollision func(layer.Collision)` and reports both a cross-layer collision and an `ARTIFACT.md` that declares `extends:`. A walk under `CollisionPolicyDrop` without `OnDrop` fails. This is a backward-incompatible change to the library API and lands in a MINOR bump.
```

4. Under `### Removed`, add:

```markdown
- **`extends:` is removed** (§4.6, §4.7.3, §4.7.6, §4.7.10, §7.2, §13.4, §13.11.3): no artifact inherits frontmatter from another. Ingest rejects an `ARTIFACT.md` whose frontmatter carries a top-level `extends` key, whatever its value, with `ingest.invalid_artifact` and `details.constraint: "extends_removed"` before lint runs, a filesystem-source `podium sync` drops it and exits 1, and `podium lint` reports it as an error. A row an earlier release stored from such an `ARTIFACT.md` is served by no read path: `latest` resolves to the artifact's most recent other version, a same-ID overlay serves the lower layer's copy when no other layer holds a served row of the ID, and an artifact with no other version answers `registry.not_found`. Such a row is not a collision, so when only one layer holds a served row of the ID, that layer ingests new versions of it without `ingest.collision`, and the row still holds its `(id, version)`. An earlier release also admitted a plain copy of an ID from a further layer while an overlay row of the ID was stored. When served rows of such an ID remain in two or more layers, `latest` serves the most recently ingested of them, and every later ingest of the ID from those layers, including a re-ingest of unchanged bytes, is rejected with `ingest.collision`; unregister all but one of those layers, removing a declared layer from the registry configuration first, which also stops serving each unregistered layer's other artifacts, and register a removed source again without the artifact only after the recovery window ends and the purge removes its soft-deleted rows, because before the purge those rows still hold its artifacts' versions and a registration under any layer ID serves none of them at an unchanged version. The registry rewrites, deletes, and re-signs no stored row, and content hashes and signatures are unchanged. The `load_artifact` response no longer carries `extends_pin`, and its entity tag is the quoted content hash, so a validator cached from a response that carried a pin no longer matches. The field-merge table, the hidden-parent rules, the `executable_inheritance` and `sandbox_profile_conflict` constraints, the `lint.license_changed_across_layers` advisory, the `podium artifact scaffold --extends` flag (passing it exits 2), the `extends` dependency edge kind, and the web viewer's extends links are removed. In the Go library, `layer.ExtendsOverlays` and `filesystem.WalkOptions.ResolveExtends` are removed. Upgrade the registry before the consumers. A `podium-mcp` that loaded a now-retired version as `latest` refuses the older version the registry now serves with `materialize.stale_resolution`, whatever the roll order and in every cache mode: stop every MCP server that uses the cache directory and run `podium cache reset-revisions <id>`, or run it with no ID to clear every mark. A `podium-mcp` in `offline-first` or `offline-only` that cached a merged artifact from an earlier registry keeps serving it; also run `podium cache prune --days 0` against its cache directory, which leaves the revision marks in place, and refill an `offline-only` cache in `always-revalidate` or `offline-first` once. This is a backward-incompatible change and lands in a MINOR bump. No flag, environment variable, or configuration key restores the inheritance. Existing databases keep the `extends_pin` column, unused, and new databases do not create it. A 0.5.x binary rolled back onto the database reads an empty `extends_pin` for every row 0.6.0 wrote.
```

5. Leave the released sections and the other `[Unreleased]` entries unedited.

### PROP-1. Withdraw draft proposal 0062

Target: `proposals/0062-resolve-a-same-id-extends-overlay-s-parent-from-the-nearest-.md` (its Status line, and the file's first commit).

0062 changes how a same-ID `extends:` overlay resolves its parent. D-1 and D-2 remove both `extends:` and the same-ID overlay, so 0062 has nothing left to change. The file has never been committed, and committed proposal 0063 cites it three times (`proposals/0063-refuse-an-extends-child-whose-sandbox-profile-conflicts-with.md:36`, `:46`, `:133`). Committing it with a withdrawn status gives those citations a target and stops anyone from approving and implementing it later; `implement-proposal` checks only for an "Approved" Status line.

- Change `- Status: Draft` to `- Status: Withdrawn (<date 0064 lands>). Moot: proposal 0064 removes extends: and the same-ID overlay this proposal would change. It was never approved or implemented.` "Withdrawn" is a new status value, used here for a record.
- Leave the body unchanged.
- `git add` the file in the same commit as the 0064 status record, following the `docs(proposals): record ...` practice of commit d0340680. Do not delete the file.
- Leave the bodies of implemented proposals 0057, 0058, and 0063 unchanged.

## Open questions

- **Q-1. Fallback for a retired different-ID child.** Resolved at sign-off (2026-11-10): accept the fallback. A retired row is treated as never stored, `latest` resolves to the most recent non-retired version, and an ID with no non-retired row answers `registry.not_found`, as D-7 states. No dedicated error code is added.
- **Q-2. The unused `extends_pin` column.** Resolved at sign-off (2026-11-10): keep the column as staged (D-10). Existing databases keep it unused, and new databases omit it.

## Non-goals

- A compatibility flag, legacy parser path, dual resolver, or migration command for `extends:`.
- A rewrite, deletion, soft-delete, or re-signing of stored rows, or a new §13.4 first-start rewrite clause. Stored content hashes and signatures stay unchanged.
- Dropping the `extends_pin` column from existing databases, or deleting stored `extends` dependency edges.
- A new §6.10 error code, lint code, environment variable, meta-tool, SPI, or matrix cell.
- A change to how `delegates_to:` and `mcpServers:` references are stored or resolved. Their pins stay grammar-only, as `pkg/registry/ingest/ingest.go:1555-1557` stores them today.
- A change to the workspace overlay. It applies no ingest rule and serves an overlay artifact as authored, including one whose `ARTIFACT.md` carries an `extends` key.
- Omitting or warning about an overlay `ARTIFACT.md` that carries an `extends` key in `podium-mcp`, `podium sync`, or the SDKs. The SDK hand parsers read only top-level key lines and cannot apply YAML merge keys or full flow-mapping decoding, so the consumers would disagree on the same file, and no consumer reads the key.
- A change to the §4.3.4 synthesized skill document or the content-hash framing. The §7.3.1 registry-assigned version changes only in its reuse rule, which leaves a retired row out of the reuse candidate (SPEC-4 Edit 9).
- A `podium-mcp` content-cache layout version or other invalidation mechanism beyond the 0.6.0 layout change already in `[Unreleased]`.
- A change to the §4.7.9 verification policy, which reads no sensitivity. Only its extends-based comments change.
- Policing an `extends` key in `SKILL.md`. It becomes an ordinary non-Podium `SKILL.md` key.
- Edits to released CHANGELOG sections or to the bodies of implemented proposals 0057, 0058, and 0063.
- Edits to the exported design canvases `web/design/Podium App.dc.html` and `web/design/Podium UI Inventory.dc.html`. `web/design/README.md` calls them static design references rather than production code, they show mockup frontmatter with an `extends:` row, and the binary embeds only `web/bundle` (`web/web.go:20`). The spec wins where they disagree (`web/design/README.md:7`). `web/design/README.md`, the prose specification beside them, is rewritten (DOC-2).
- Filtering retired rows out of the quota read, reembedding, or the ingest helpers `tenantHasArtifact` and `tenantHasNonDeprecatedVersion`; see D-7.

## Resolved in adversarial review

Review rounds populate this section. Each bullet records a finding and where its fix landed.

### Draft reconciliation (2026-11-10)

- The SPEC-3 revision keeps overlay artifacts served as authored, while the draft's D-6, CODE-8, CODE-11, TEST-6, the SPEC-6 overlay bullet, MV-1, DOC-1, DOC-2, and DOC-3 still described an overlay omission with warnings. All of them now follow SPEC-3. CODE-4 also needed a change: a refusal under every policy fails the overlay walk, which uses `CollisionPolicyHighestWins` with no `OnDrop`. CODE-4 now skips the refusal under that policy alone, D-5 and D-6 state it, TEST-3 pins both sides, and the omission design is recorded under Non-goals.
- TEST-3 and TEST-7 both rewrote `TestFilesystemSync_ExecutableInheritanceDropsChild`. TEST-3 owns the rewrite and carries TEST-7's fixture details; TEST-7 keeps the sandbox-test deletion and the `assertCollisionReported` update.
- TEST-5 and CODE-6 both deleted `TestLoadArtifactETag_FoldsTheServedExtendsPin`. CODE-6 owns it, because the package must compile at S3, and TEST-5 confirms it is gone.
- TEST-8 and CODE-12 both listed the web UI test files. CODE-12 owns them, because the bundle rebuild lands with them.
- DOC-1 and DOC-2 (e) both re-homed the overlay and replacement content into `layers.md`. DOC-1 owns it, and the two land in one docs step.
- CODE-3 (a) and TEST-2 both named a case (h). The revert case is (h), the N-derivation case is (i) and is required, because a test must pin D-7's "counts toward the registry-assigned-version rules". CODE-3 (b)'s prose-reference cascade gained case (j) and a staged §7.3.1 sentence.
- S102 cited the S95 build block, which MV-1 deletes. S102 now carries its own build step.
- S102's last step expected `registry.not_found` after the prune. On a cache miss, `cachedOrRefetch` returns no record and the `latest` load falls through to the registry (`cmd/podium-mcp/main.go:1350-1366`, `:1574-1578`), which serves C@1.0.0. The Expect now says so.
- The SPEC-6 equivalence sentence now names the lower-layer `extends:` copy under an ID the higher layer contributes plainly, matching the TEST-3 fixture that pins the refusal-before-dedupe order.
- §9.4 (`spec/09-extensibility.md:58`) names `extends:` resolution and was in no change. SPEC-6 Edit 5 covers it.
- D-15 placed every consumer change in one code step. The scaffold flag, the SDK edge kinds, and the web UI do not affect mode equivalence, so they follow as their own steps, and D-15 says so.
- CODE-5's memo was keyed per row. A purge followed by a re-ingest can store other bytes under a reused `(id, version)`, so the key is the content hash.
- TEST-4 asserted that an all-retired R is absent from "the dependency in-degree". The ranking signal counts tenant-wide by design, so the assertion is narrowed to `dependents_of`, and fixture (d) pins the visible-edge case CODE-5 (c) describes.
- TEST-1 named the new test file `removed_test.go`, while CODE-2 (c) names the source file `invalid.go`. The test file is `invalid_test.go`, and `ExtendsRemovedDrop` is kept because TEST-1 and TEST-3 assert it.

### Pass 1 (2026-11-10, automated)

- Surviving tests that S3 breaks had no disposition. TEST-8 now rewrites `pkg/registry/core/artifact_revision_test.go` over two plain artifacts with inlined fixtures, TEST-2 removes the extends line, the parent ingest, and the extends case from `TestIngest_PopulatesDependencyEdges`, and CODE-7 is the one owner of `TestMemory_DependencyIndex`, which keeps its legacy extends edge and expects 1 `delegates_to` edge. The duplicate `pkg/store/store_test.go:62` bullet is gone from TEST-8.
- TEST-8 deleted files whose helpers surviving tests use. TEST-8 now moves `exLoadResp` into `test/e2e/republish_helpers_test.go` as `loadArtifactResp`, moves `lcaWriteArtifact` and `lcaReingest` into `lint_caps_unlisted_test.go` as `lintWriteArtifact` and `lintReingest`, replaces the core `epFile`, `parentArtifact`, and `childArtifact` users, and requires a declaration check before every file deletion. The Watch-out bullet names the helpers.
- The doc-claim end-to-end tests had two owners. TEST-8 now owns `readme_claims_test.go`, `core_concepts_test.go`, `frontmatter_schema_test.go` (including the merge tests that call `extendsBoot`), and `deployment_modes_test.go` at S8, with a disposition for each. DOC-2 (g) only reruns them, the S17 ordering constraint gives the new reason, the S8 checklist line names them, and the S8 command runs them.
- SPEC-3 promised that `podium lint` reports what ingest would refuse, although lint contacts no registry. SPEC-3 Edit 2 and DOC-1 item 1 now claim only that lint reports an `ARTIFACT.md` the no-inheritance rule refuses, with the `cmd/podium/main.go:1054-1089` evidence.
- §1.5's comparison table still advertised `extends:`. SPEC-6 Edit 10 rewrites the cell, and DOC-2 (f) rewrites `why-podium.md:146` and `:156` so the tables stay aligned.
- The unchanged §7.3.1 reuse rule picked a retired row as the reuse candidate, contradicting CODE-3 (a) and TEST-2 (h). SPEC-4 Edit 9 excludes a retired row from the reuse candidate, SPEC-1 Edit 4 says a retired row joins the duplicate-content and N rules and never supplies a reused version, and D-7, CODE-3 (a), and the edge-case row cite the new text.
- `deploy/runbook.md`, `web/DESIGN.md`, `web/design/README.md`, and the `.claude/workflows` audit partitions kept extends text outside every edit list. DOC-2 rewrites the runbook, the web design documents, and `change-proposal.js:810`, DOC-1 removes the deleted page from the audit partitions that name it, and the DOC-1 and DOC-2 (h) greps now cover `deploy/`, `web/DESIGN.md`, `web/design/README.md`, and `.claude/`. The exported design canvases are recorded under Non-goals.
- `pkg/layer` kept `MostRestrictiveSensitivity` and `AppendUniqueTags`, whose only callers are tests citing the deleted §4.6 field semantics. CODE-2 deletes both and rewrites the `Composed` comment, and TEST-8 deletes their tests.
- The `DeclaresExtends` prefilter skipped a key spelled with a YAML escape, so a merged 0.5.x child with such a key would be served unmerged. D-3, the Watch-out bullet, and CODE-1 now skip the decode only when the frontmatter block holds neither `extends` nor a backslash. TEST-1 adds the escaped and continuation true cases and a backslash false case, and TEST-4 adds the escaped stored-row fixture (e).
- The CODE-5 memo is written on concurrent read paths with no `-race` test. TEST-4 adds `TestServableRows_ConcurrentListingsAndIngest`, and the S11 commands run it with `-race`.
- Retiring the newest row lowers `latest`, so a `podium-mcp` revision mark refuses the fallback with `materialize.stale_resolution`, and `podium cache prune` keeps the marks. D-9, its Summary line, a new Watch-out bullet, the current-state cache section, the edge-case rows, the DOC-3 Removed entry, TEST-6, and S102's Goal, Covers, step 3, and step 7 now state the refusal and the `podium cache reset-revisions` remedy. TEST-6 names the existing tests that pin the mark refusal and the reset (`test/e2e/delivery_freshness_test.go:33`, `cmd/podium-mcp/delivery_freshness_test.go:344`) rather than adding a duplicate, because the `podium-mcp` code is unchanged.
- The §6.5 reset-revisions triggers omitted the retirement. SPEC-3 Edit 3 adds it, and DOC-2 adds it to `docs/reference/error-codes.md:127`, `docs/consuming/configure-your-harness.md:44`, and the `layers.md` *Inheritance* section.
- The frontmatter panel's served note in `web/ui/src/components/PropertyTable.tsx` was in no edit list. CODE-12 deletes `ServedNote` and its renders, deletes the code-chip test and the `(key withheld)` assertions, adds a no-note case, and D-12 names it.
- S102 expected the merged tags on the `^tags` line, but the old registry writes a block sequence. `mcp_load` now also matches `- base$`, and the step 3 and step 7 Expects name the `tags:` line and the `- base` entry.
- Edge-case rows cited docs edits that did not state their outcome. DOC-2 (d) adds the before-lint sentence to the `http-api.md` reingest paragraph, and DOC-2 (i) and the `cli.md` bullet state that a refused artifact collides with nothing and replace the `cli.md:242` recompose paragraph.
- The S17 command ran `-run 'ReadmeClaims|CoreConcepts|FrontmatterSchema|DeploymentModes'`, which matches no test function, so `go test` printed `no tests to run`, exited 0, and DOC-2 (g)'s rerun checked nothing. The S17 command now uses the S8 pattern, `-run 'Frontmatter|CoreConcept|Readme_Impact|Deployment_Dependents'`, with a grep for `SKIP`.
- S8 runs the doc-claim tests under `test/e2e` and moves helpers there, but its Levels line named unit and integration only. The S8 Levels line now reads "unit, integration, e2e".
- `docs/authoring/frontmatter-reference.md:103` mirrors the §7.3.1 reuse rule without the retired-row exception SPEC-4 Edit 9 adds, and the bullet holds no "extends", so neither the general DOC-2 instruction nor the DOC-2 (h) grep reached it. DOC-2 now rewrites the bullet to say a retired row never supplies the reused version.
- `web/DESIGN.md:289-293` named the hidden-parent search result as a producer of an empty frontmatter view, a path CODE-5 deletes, and its "inherited" wording read like the unrelated hits DOC-2 (h) leaves. The DOC-2 `web/DESIGN.md` bullet now deletes that producer and the `http-api.md` search-result clause, and DOC-2 (h) names `web/DESIGN.md:290` as a related hit that must be gone.

### Pass 2 (2026-11-10, automated)

- TEST-1 expected `DeclaresExtends` to return true for an implicit double-quoted key split across two lines, which `gopkg.in/yaml.v3` v3.0.1 refuses because an implicit key is restricted to one line (`scannerc.go:888-905`), and D-3 and the CODE-1 doc comment claimed the 0.5.x decoder merged such a child. TEST-1 now writes the continuation true case in explicit `?` key form and adds the implicit two-line key as a false case, and D-3 and the CODE-1 doc comment name the explicit-key form and state that the implicit form decodes nowhere. D-3's `types.go` citation is corrected to `:155`.
- The prefilter skipped a `!!binary`-tagged key (`!!binary ZXh0ZW5kcw==: a`), which decodes to `extends` with neither the substring nor a backslash and which the 0.5.x typed decoder resolved to `Extends`. D-3, the Watch-out bullet, the CODE-1 doc comment, and CODE-1 step 2 now skip the decode only when the frontmatter block holds none of `extends`, `\`, and `!`, and the CODE-1 scratch-check paragraph records the tagged and explicit-key results. TEST-1 adds the tagged true cases in implicit, explicit-key, and verbose form and a `description: !!str x` false case, TEST-4 adds the tagged stored-row fixture (f) to the absence assertion and to the concurrency test, and the edge-case row names escaped and tagged spellings.
- `web/DESIGN.md:266` still quoted §13.10 as requiring links to extending artifacts, and the DOC-2 (h) pattern cannot match "extending". DOC-2 now rewrites the sentence to "links to dependent artifacts (§4.7.3)", matching SPEC-5 Edit 8, and DOC-2 (h) adds a separate `grep -n 'extending or dependent'` check rather than adding "extending" to the pattern, which would match the unrelated *Extending* page throughout `docs/`.
- S8 ran `go vet` over `pkg/layer` and `pkg/registry/filesystem` while `TestExtendsOverlays`, `TestExtendsOf`, and `TestWalk_DropPolicyDroppedRecordIsNotAParent` still called identifiers S3 deletes, and TEST-1 and TEST-3 removed them only at S9 and S10. TEST-8 now deletes all three and the other `ResolveExtends` uses in `walk_test.go` at S8, and TEST-1 and TEST-3 point at it.
- `test/integration` did not compile until S12 and `internal/serverboot` until S11, because the `ExtendsPin` rows of `delivery_attestation_test.go`, `stored_row_admission_test.go`, `dependents_visibility_test.go`, and `store_retention_test.go` were fixed by TEST-4 and TEST-5. TEST-8 now owns those compile-affecting rewrites and deletions, TEST-4 keeps only the new all-retired dependents case, and TEST-5 keeps only the server-package edits. The ordering constraints, the Watch-out bullet, the S2 interleave line, the S8 and S11 checklist lines, FIX-1, and the S2 and S8 commands now say that every test package compiles and `go vet ./...` passes from S8, and that S8 reruns the S2 suites against the new code. Each of S9 to S13 runs only its own tests with `-run`, because a package it edits holds tests a later step rewrites, and the combined coverage run moves to the closing step.
- TEST-8 removed only the `ResolveExtends` option at `walk_test.go:972`, which sits inside `TestWalk_ExtendsChildInheritsPlainSkillParentDescription` (`:964`). That test's child declares `extends: ops/base` and asserts the inherited SKILL.md description, so under CODE-4 the walk fails on the refused child and the test reaches `t.Fatalf`. TEST-8 now deletes the test by name and limits the option removal to the surviving plain-skill tests at `:876`, `:916`, and `:945`.
- The Watch-out bullet, the ordering constraints, and the S8 checklist line said TEST-8 carries every compile-only test removal, which contradicted CODE-6 (`TestLoadArtifactETag_FoldsTheServedExtendsPin`) and CODE-7 (the `ExtendsPin` edits outside `pkg/store`), which carry their own compile-only test edits at S3. All three now say that CODE-6 and CODE-7 carry the compile-only test edits they list at S3 and that TEST-8 carries every other compile-only test removal.

### Pass 3 (2026-11-10, automated)

- SPEC-3 Edit 3 added the upgrade retirement to the §6.5 reset-revisions triggers, while SPEC-2 Edit 18 left the rest of the §4.7.10 attestation-scope paragraph unchanged, including its list of honest `latest` decreases (`spec/04-artifact-model.md:1019`) and the sentence that defers to §6.5 for "these". SPEC-2 Edit 18 now also replaces "after a purge or erasure removes the newest versions (§8.4, §8.5), when the caller's view loses them," with a version that adds "after an upgrade retires them under the §4.6 no-inheritance rule", and its closing line excepts that sentence. SPEC-3 Edit 3, D-9, and the Summary name both lists.
- SPEC-1 Edit 4 and D-7 listed the §4.7.3 reverse index and search among the paths that treat a retired row as absent, which contradicted the SPEC-2 Edit 7 sentence, the edge-case row, TEST-4's D→T assertion, and DOC-2 (i), all of which keep a retired version's edges while its ID has another served row. SPEC-1 Edit 4 now removes the reverse index from that list, narrows "search" to "search results", states that the reverse index is keyed by artifact ID and admits no edge whose source ID has only retired rows, and states that the tenant-wide ranking signal counts a retired row's `delegates_to:` and `mcpServers:` edges, including those of an all-retired source ID. D-7 carries the same rule with its code citations (`pkg/registry/ingest/ingest.go:866-871`, `pkg/registry/core/dependents.go:38-53`, `pkg/store/store.go:446-451`), and the "Edges carry no version" Watch-out bullet and the edge-case row cite the new §4.6 sentence.
- CODE-12 rewrote the Layer panel lead copy but gave no disposition to the existing case that asserts the current lead sentence (`web/ui/src/surfaces.test.tsx:8492`, assertions at `:8501-8513`). The CODE-12 targets now list that case, its Tests bullet rewrites it to assert the rename-or-remove copy, keeps the "the higher precedence wins" negative assertion, adds a negative assertion for "extends", and changes its citation to `// Spec: §4.6 ("Collisions")`, and the `LayerPanel.tsx` bullet moves the component comment to the same citation.

### Pass 4 (2026-11-10, automated)

- A retired row's vector still put its artifact ID in `search_artifacts` results, because `vectorRanks` keeps a match by artifact ID and never compares its version (`pkg/registry/core/core.go:1381-1405`), `reorderScored` adds each vector-only hit at the served version (`:1435-1450`), and `Reembed` keeps a vector for every `(id, version)` row (`pkg/registry/core/reembed.go:68`, `:93-96`). CODE-5 gains (d): `vectorRanks` takes the servable visible rows from its one caller and keeps a match only when its `(id, version)` names one of them, which leaves the vectors of older non-retired versions in effect. The CODE-5 targets and choke-point paragraph, D-7, the Summary, a new Watch-out bullet, the storage-and-reembedding edge-case row, and the reembedding Non-goal now describe that filter, TEST-4 adds `TestSearchArtifacts_RetiredRowVectorsDoNotSurface`, and the closing coverage step names `vectorRanks`.
- Removing `extends` from the `SKILL.md` Podium-only list changes lint and ingest outcomes (`pkg/lint/skill_rules.go:36-50`, registered at `pkg/lint/lint.go:120`), but no test asserted the new behavior. CODE-1 now adds a negative `IsPodiumOnlySkillField("extends")` and `PodiumOnlySkillFields()` assertion to `TestPodiumOnlySkillFieldClassification`, TEST-1 adds `TestLint_SkillMDExtendsKeyIsNotPodiumOnly`, TEST-2 adds case (k) for ingest and serving with and without an `ARTIFACT.md` overlay, TEST-3 adds the matching walk case, TEST-7 adds a `podium lint` exit-0 run, and the edge-case row states the lint, ingest, serve, and sync outcomes and cites SPEC-2 Edit 4.

### Pass 5 (2026-11-10, automated)

- MV-1 named only S52's `--tags runbook` CLI line, while S52 step 6 checks the same filter in the web UI and still expected `tag:runbook deploy` to report `Showing 2 of 2 matches` (`test/manual-validation.md:6072-6075`). Without the `extends:` line, `deploy-runbook-strict` (`tags: [deploy, platform]`, `:5878`) matches no `runbook` filter. The MV-1 S51, S52, and S53 bullet now also rewrites the S51 fixture introduction (`:5867-5868`), the S52 step 1 third-line Expect (`:6030`) and explanatory paragraph (`:6035-6038`), and the S52 step 6 Expect, which reads `Showing 1 of 1 match` for `eng/platform/deploy-runbook`.
- The S52 line citations in the MV-1 S51, S52, and S53 bullet and in the bullet above were off by one. The step 1 third-line Expect starts at `test/manual-validation.md:6030`, because `:6029` is the blank line after the code fence. The step 6 `tag:runbook deploy` text is at `:6074`, because `:6075` holds the trailing sentence. Both bullets now cite `:6030`, the MV-1 bullet cites `:6074` for the step 6 Expect, and the bullet above cites `:6072-6075`, which starts at the line that holds `**Expect.**`.

### Pass 6 (2026-11-10, automated)

- `podium lint` walked with no `OnDrop` and returns 1 on a walk error before `linter.Lint` runs (`cmd/podium/main.go:1083-1089`), so under CODE-4 step 2 it would have named only the first refused artifact and no lint diagnostic of the rest, which broke the unchanged §7.3.1 sentence that `podium lint` against the source names the artifacts behind a reingest's lint count (`spec/07-external-integration.md:159`), and CODE-9's comment ("every `ARTIFACT.md`") contradicted CODE-4 and the edge-case row ("the first refused artifact"). CODE-4 gains step 8: `lintCmd` passes a collecting `OnDrop` under `CollisionPolicyDefault`, prints each drop through the existing `reportDropped` (`cmd/podium/layer.go:617-622`), lints the remaining records, and exits 1 on any drop or error diagnostic, and the `reportDropped` and `reportDrops` comments are rewritten. CODE-4 step 2 states that a refusal reports through `OnDrop` under every refusing policy and the walk continues. D-5, its Summary line, the Summary's library bullet, the CODE-4 caller paragraph, the CODE-9 lint comment, the edge-case row, S102 step 4's Expect (both `rejected:` lines), and the DOC-2 `cli.md` bullet now say the same, and TEST-7 adds `TestLint_ExtendsRefusalBesideLintFailure`, which asserts both refused IDs and the diagnostic of the lint-failing artifact.
- The edge-case row for a retired same-ID overlay cited `CHANGELOG.md` for "the lower layer ingests a new X version with no `ingest.collision`", but neither the DOC-3 Removed entry nor DOC-2 (i) said that a retired row is not a collision, and the surviving `layers.md:17` and `error-codes.md:115` text predicted a rejection. DOC-2 (i) adds a bullet stating that a retired row is not a collision and still holds its `(id, version)`, so other bytes at that version are rejected with `ingest.immutable_violation` (the store key is `(tenant_id, artifact_id, version)`, `pkg/store/sqlite.go:101`). The DOC-3 Removed entry adds the same sentence, and the row's docs column now names `docs/deployment/layers.md` *Inheritance* (DOC-2 (i)) beside the CHANGELOG.
- The edge-case row for a retired row presented with other bytes still gave its docs column as "no docs page states it", although the DOC-2 (i) bullet added above states that other bytes at a retired row's `(id, version)` are rejected with `ingest.immutable_violation`. The row now names `docs/deployment/layers.md` *Inheritance* (DOC-2 (i)). It does not cite `CHANGELOG.md`, because the DOC-3 Removed entry says that the row still holds its `(id, version)` and does not name `ingest.immutable_violation`.

### Pass 7 (2026-11-10, automated)

- The DOC-2 `docs/authoring/frontmatter-reference.md:103` bullet kept "the layer's most recently ingested non-deleted row" as the reuse candidate and added only that a retired row never supplies the reused version. A retired row is not deleted (D-7), so the candidate stayed the newer retired `0.0.0-auto.1`, the bullet gave no reuse, and the unchanged `:105` bullet then rejected a revert to the served `auto.0` bytes as `auto_version_duplicate_content`, which contradicted SPEC-4 Edit 9 and TEST-2 (h). The bullet now reads "Bytes that the layer's most recently ingested non-deleted row of the artifact, other than a row the no-inheritance rule retires, holds reuse that row's version, and the ingest is a no-op.", which applies the SPEC-4 Edit 9 predicate before the candidate is chosen, and the separate "never supplies" sentence is dropped. The edge-case row for a no-version skill whose ID holds a retired `0.0.0-auto.1` now names this bullet in its docs column in place of "no docs page states it".

### Pass 8 (2026-11-10, automated)

- SPEC-6 Edit 4 assigned the §4.5.4 `DOMAIN.md` merge to the §9.1 `LayerComposer` row, which DOC-2 (c) claimed to mirror while naming only the collision rule. `pkg/layer` holds no `DOMAIN.md` code (`pkg/layer/composer.go:1`), `pkg/domain.MergeAcrossLayers` implements the merge (`pkg/domain/domain.go:1`, `:11`) and is called directly (`pkg/registry/core/domain_load.go:144`, `pkg/overlay/overlay.go:105`), §10 builds the merge in phase 8 after the phase-7 `LayerComposer` (`spec/10-mvp-build-sequence.md:21-22`), and the SDKs apply the overlay part of the merge without the Go module (`spec/04-artifact-model.md:506`, `spec/02-architecture.md:105`). SPEC-6 Edit 4 now changes the row to "applies the §4.6 collision rule" and records why the merge stays under the §2.2 "merge logic", so DOC-2 (c) mirrors it exactly.
- The `DeclaresExtends` prefilter skipped a frontmatter block that starts with a UTF-16 byte order mark, which `gopkg.in/yaml.v3` decodes as UTF-16 (`readerc.go:41-42`, `:59-66` in v3.0.1) and in which the key `extends` holds no contiguous substring, no backslash, and no `!`, although the 0.5.x typed decoder filled `Extends` from it. A scratch check confirmed both byte orders. The prefilter now also decodes whenever the block holds a NUL byte, which every UTF-16 block that carries the ASCII key holds and which a UTF-8 block's decode refuses (`readerc.go:379`). D-3, the Watch-out bullet, the CODE-1 doc comment, step 2, and scratch-check paragraph, and the edge-case row name the new term. TEST-1 adds UTF-16LE and UTF-16BE true cases and a UTF-16 false case that exercises the decode, and its prefilter-path false case names the NUL term. TEST-4 adds the UTF-16LE stored-row fixture (g) to the absence assertion and to the concurrency test.

### Prune after pass 8 (2026-11-10, automated)

- The `DeclaresExtends` byte prefilter drew a confirmed soundness defect in three passes (the escape, `!!binary`, and UTF-16 spellings), and its specification and soundness argument were restated in D-3, the Watch-out bullet, the CODE-1 doc comment, CODE-1 step 2, the CODE-1 scratch-check paragraph, and TEST-1. The prefilter is an optimization that the CODE-5 (b) per-content-hash memo already makes redundant, so its specification is deleted from each of those places. D-3 now carries an IMPLEMENTOR'S CHOICE on whether the predicate takes any fast path, constrained to return exactly what `SplitFrontmatter`, the `map[string]yaml.Node` decode, and the top-level `extends` lookup return for every input, with no fast path as the recommended default. The blank delegates only the implementation strategy: the predicate's output, which is a fail-closed input to ingest and to retirement, stays fully specified. The Watch-out bullet keeps only the frontmatter-block-only rule, CODE-1's steps are split, decode, and key check, and its doc comment keeps the merge-key and frontmatter-block rationales. TEST-1 keeps every true case, including the escaped, continuation, tagged, explicit-key, and UTF-16 cases as regression tests of the decode, and the nested-key, `SKILL.md`, and two-line implicit-key false cases, and it drops the false cases that existed only to separate the prefilter path from the decode path. An implementor who adds a fast path also adds a property test that it agrees with the decode. TEST-4 keeps fixtures (e) to (g), and its absence assertion now names the predicate in place of the prefilter.
- The Non-goal that excluded any change to the registry-assigned version contradicted SPEC-4 Edit 9, which changes the reuse rule. It now excludes changes to the §4.3.4 synthesized document and the content-hash framing and states that the registry-assigned version changes only in its reuse rule.
- The ingest helpers `tenantHasArtifact` and `tenantHasNonDeprecatedVersion` (`pkg/registry/ingest/ingest.go:283-311`) read every stored row, retired rows included, and no section named them. A new Non-goal beside the quota Non-goal records both as accepted unfiltered readers and states the artifact-count-quota and §7.3.2 deprecation-flip consequences.

### Pass 9 (2026-11-10, automated)

- Two edge-case rows cited docs that did not state their outcome after the staged edits. The `dependents` section of `docs/reference/http-api.md` names no edge kinds, and its only kind-specific sentence (`:366`, the `extends` visibility sentence) is removed, so nothing stated that a stored `extends` edge is never returned or counted. The DOC-3 entries did not mention the `extends_pin` column. DOC-2 (d) now adds, after "Each edge carries `from`, `to`, and `kind`." (`:363`), the sentence "An edge's `kind` is `delegates_to` or `mcpServers`. A stored edge of any other kind is never returned and never counts toward the search ranking signal.", which mirrors SPEC-2 Edit 6 and the CODE-7 kind filter (the kinds are `pkg/dependency/graph.go:14-15`). The DOC-3 Removed entry now ends with the sentences that existing databases keep the `extends_pin` column, unused, that new databases do not create it, and that a 0.5.x binary rolled back onto the database reads an empty `extends_pin` for every row 0.6.0 wrote (the column carries `NOT NULL DEFAULT ''`, `pkg/store/sqlite.go:95`, `pkg/store/postgres.go:86`, and has been in the `CREATE TABLE` statement of both stores since v0.1.0; v0.1.3 added its additive-column entry in `pkg/store/schema_migrate.go`). The two rows' docs columns now name `docs/reference/http-api.md` `dependents` (DOC-2 (d)) and `CHANGELOG.md` *Removed* (DOC-3 item 4).
- Correction to this pass: the bullet above dated the `extends_pin` column to v0.1.3. The column is in the `CREATE TABLE manifests` statement of `pkg/store/sqlite.go` and `pkg/store/postgres.go` at v0.1.0 (`git show v0.1.0:pkg/store/sqlite.go`, line 74), and it comes from commit 87511f26, which `git tag --contains` first places in v0.1.0. `pkg/store/schema_migrate.go` does not exist at v0.1.2, and v0.1.3 added the `{"manifests", "extends_pin TEXT NOT NULL DEFAULT ''", ...}` entry (`schema_migrate.go:65` at that tag). The bullet now states both facts. The rollback conclusion is unchanged, because every 0.5.x binary creates and reads the column.

### Pass 10 (2026-11-10, automated)

- The current-state section said a same-ID collision was accepted only when the higher-precedence artifact declares `extends:` on the ID, but the 0.5.x server sanction also admits any incoming copy while any stored cross-layer row's pin names the ID, with no precedence test (`pkg/registry/ingest/ingest.go:761-779`; `pkg/layer/collision.go:40-48`; `test/integration/plain_skill_ingest_test.go:439-443`). After retirement such a store keeps servable rows of one ID in two registry-side layers, an outcome the proposal never defined, and the D-7, edge-case, DOC-2 (i), and DOC-3 statements that the lower layer serves and ingests freely were false for it. The current-state paragraph now states the §4.6 form, the walk's form, and the wider server form with their citations. D-7 gains *Two-layer IDs*: those rows keep serving with `latest` the most recently ingested (`spec/04-artifact-model.md:924`), every later ingest of the ID from those layers, unchanged bytes included, is rejected with `ingest.collision` because the collision check precedes idempotency (`ingest.go:766-779`, `:784`), and the remedy is to unregister all but one of the layers, removing a declared layer's declaration first, which soft-deletes their rows (§8.4; `pkg/store/store.go:466-470`). No precedence rule, exemption, or rewrite is added. SPEC-1 Edit 2 now says that neither ingest nor a filesystem sync admits a silent shadow and points at Edit 4, and Edit 4 gains a paragraph stating the outcome. The Summary's D-7 line, a new Watch-out bullet, D-9, Q-1, the existing same-ID overlay edge-case row (now qualified to a sole served lower layer), a new edge-case row, DOC-2 (i), and the DOC-3 Removed entry carry the same predicate. TEST-2 adds case (l), which seeds the three-layer state through the store and asserts the `latest` row, the `ingest.collision` on both unchanged re-ingests, and the idempotent re-ingest after `DeleteLayerConfig`.
- `TestPlainSkillIngest_ThirdLayerCollisionAndDuplicate` (`test/integration/plain_skill_ingest_test.go:439-467`) had no disposition, although both of its later assertions fail under CODE-3, and TEST-8's `:394-420` range for `TwoLayersShareTheAssignedSequence` started at the `psOverlay` constant that this test still uses, so deleting the range broke compilation at S8. The TEST-8 bullet now gives each test its own range (`:396-437` and `:439-467`), rewrites the third-layer test to keep its first collision assertion and re-seed the duplicate-content refusal through a row soft-deleted by `DeleteLayerConfig` (`pkg/registry/ingest/autoversion.go:58-68`, `:83-85`), and deletes `psOverlay` (`:392-394`) only with its last user.

### Prune after pass 10 (2026-11-10, automated)

- Each retired-row and cache fix had been copied into the Summary, the Watch-out list, the Decisions, the edge-case table, the open questions, and the deliverables, and those copies were where later contradictions came from. The Summary's D-7 and D-9 lines are now one-line index entries that point at the decisions, and a new sentence under the Fixed decisions fixes the constraint: each rule has one normative statement in its D-n and one staged statement in its SPEC or DOC deliverable, and every other mention cites the D-n without paraphrasing it. The Watch-out bullets that restated a decision (the retired-row key, the vector ranker, the two-layer IDs, the reuse candidate, the refusal before lint, the walk refusal before dedupe, the retired row's edges, the edge-kind filter, `test/vectors/`, the 0057 entry, proposal 0062, the revision marks, and speccov-drift) are now one-line pointers. The traps no decision states stay in full: the overlay walk exemption, the frontmatter-block-only decode, the purge protection with the column, the web bundle rebuild, the compile ordering, the `SKIP` grep, and line drift. The scenario-and-port fallback moved into the MV-1 S102 preamble.
- D-7 and SPEC-1 Edit 4 enumerated each consumer of a retired row separately. Both now state the rule as a delta from the existing soft-delete semantics: a retired row is treated as a soft-deleted row by the ingest collision check, the registry-assigned-version rules, and every read path, including the vector ranker, and it differs in that no layer restore revives it. A check of `pkg/store/sqlite.go` showed that `DependencyInDegree` counts every stored edge and that `DeleteLayerConfig` leaves the dependency table untouched, so the ranking signal already counts a soft-deleted row's edges. The edge behavior is therefore stated as the soft-delete behavior with its ranking consequence. SPEC-1 Edit 4 now defers the registry-assigned-version clause to §7.3.1, which SPEC-4 Edit 9 already stages, and defers the reverse-index clause to §4.7.3, which SPEC-2 Edit 7 already stages. The edge-case rows that quoted the old sentences quote the new ones. The proposed IMPLEMENTOR'S CHOICE on where the retired check runs (the core choke points, a store-level filter beside `DeletedAt`, or both) was not added. The convention bars a blank on which component performs an action. A store-level filter would also change the store SPI, its conformance suite, and the names `servableRows` and `retired` that CODE-3, CODE-5, and the closing coverage step cite. The proposed constraint that every path excluding a soft-deleted row also excludes a retired row contradicts the Non-goal that keeps `tenantHasArtifact` and `tenantHasNonDeprecatedVersion` as readers of retired rows, because both helpers read `ListManifests`, which excludes soft-deleted rows. D-7 therefore keeps the core choke points and states the constraint for read paths only.
- The edge-case table's docs column drew citation findings in passes 6, 7, and 9. The column is deleted. A paragraph above the table and a new DOC-2 (h) step require every row that records a user-visible change from 0.5.2 to appear in the DOC-3 Removed or Changed entries or in DOC-2 (i), and the rows cite no doc sentence.
- Q-1 and D-9 restated the two-layer fallback. Both now cite D-7 *Two-layer IDs*.
- TEST-8's hand-completed deletion list missed tests that S3 breaks. Examples are `TestPodiumMCP_MergedManifestDeliveryHashVerifies` (`test/integration/mcp_materialize_pipeline_test.go:157-205`), which pins the deleted §4.7.10 merged-manifest framing, the `extends: executable` reason fixture in `pkg/registry/server/reingest_pipeline_test.go:241` and `:266`, and the stale comments at `cmd/podium/interactive_test.go:290` and `pkg/sync/server_auth_overlay_test.go:79-80`. Today `grep -rlE 'extends|Extends' --include='*_test.go' pkg cmd internal test` lists 88 files. TEST-8 keeps the helper-move bullets, the compile-order bullets (`psOverlay`, `exLoadResp`, `lcaWriteArtifact`, and the `ExtendsPin` rows), and the bullets whose rewrite target another section cites or a reader cannot infer. It drops the bullets that only named files to delete or said "whatever X leaves", gives each of the gaps above its own disposition, and adds a completeness gate. Under the gate, that grep lists only files in six allowlist categories, the S8 command runs it, and the closing step reruns it without the category for tests that a later step rewrites. The S8 checklist line, the S8 command, and the closing step name the gate. `go vet ./...` catches the deleted-identifier tests that the word does not match, such as `TestMostRestrictiveSensitivity`.

### Pass 11 (2026-11-10, automated)

- The D-7 *Two-layer IDs* remedy, DOC-2 (i), and the DOC-3 Removed entry told the operator to register the unregistered source again under a new layer ID without the artifact. Unregistering soft-deletes every row of the layer (`pkg/store/sqlite.go:920-923`), and the tombstoned rows keep their layer-free `(tenant, artifact_id, version)` keys (`:101`) until the §8.4 purge, so a registration before the purge serves none of the source's other artifacts at unchanged versions: `GetManifest` hides the tombstoned row (`:509-515`), `PutManifest` returns nil on the equal hash without writing (`:323-363`), and ingest counts the artifact accepted (`pkg/registry/ingest/ingest.go:959-993`, `:801-802`), while a skill with no declared version is rejected with `auto_version_duplicate_content` (`pkg/registry/ingest/autoversion.go:75-87`, `:103-105`; `spec/07-external-integration.md:106`). D-7 now states that unregistering also stops serving the layer's other artifacts, that the operator registers the source again only after the purge, what a registration before the purge does, and that `RestoreLayerConfig` is no remedy because it revives the colliding row (`pkg/store/sqlite.go:948-951`). DOC-2 (i) and the DOC-3 Removed entry give the same order. The stale re-seed citation `spec/07-external-integration.md:141` is corrected to `:155`. TEST-2 (l) seeds `ops/z` and a version-less `ops/s` in `other` and pins the sequence through `LoadArtifact`: both are unserved after the unregistration, a pre-purge ingest into a new layer `other2` rejects `ops/s` as duplicate content and still serves neither, and after `PurgeExpiredLayerDeletions` the same ingest serves both. The case does not assert the pre-purge `Accepted` count. No spec edit changes, because SPEC-1 Edit 4 states only that unregistering soft-deletes the rows.
- The DOC-2 (h) gate required every remaining `extends|hidden parent|merge semantics|field-merge|inherit` hit to be unrelated, while DOC-1 item 1, DOC-2 (d), the `cli.md`, `error-codes.md`, `configure-your-harness.md`, and `frontmatter-reference.md:103` bullets, and the (i) `## Inheritance` section add text that matches the pattern and describes the removed feature. DOC-2 (h) now lists allowlist categories, as the TEST-8 gate does: the no-inheritance refusal and its consequences, the ordinary `SKILL.md` key, the overlay artifact served as authored, the retired-row outcome including the `## Inheritance` section and the no-inheritance-rule wording, and the unrelated phrases it already named. Every other hit is removed, and the S17 commit records each remaining hit with its category.
- SPEC-6 Edit 9 makes §11 name a skill with no `ARTIFACT.md` in the overlay leg of the content-hash agreement, while FIX-1 deleted `TestSyncServerSource_OverlayExtendsChildAgreesWithIngest`, the only test of that leg against the digest ingest stored (`test/integration/sync_manifest_body_test.go:140-145`), and no other test covers a `SKILL.md`-only overlay package (`pkg/sync/overlay_test.go:173-215`; `cmd/podium-mcp/overlay_load_test.go:31-58`, `:156-204`). FIX-1 now rewrites it in place as `TestSyncServerSource_OverlayPlainSkillAgreesWithIngest` over the reference fixture's `plain-skills/unversioned/SKILL.md`, asserting that the overlay lock hash equals the served digest and the zero-length-`ARTIFACT.md` framing and differs from the digest over `manifest.SynthesizedSkillDocument("0.0.0-auto.0")`. The S2 and S8 `-run` pattern already selects it.

### Pass 12 (2026-11-10, automated)

- CODE-7 said `test/integration/auth_cross_tenant_quota_test.go` only drops its `ExtendsPin` references, while its `mustPutEdge` stores `Kind: "extends"` edges (`:150-155`) and its `xtqHasEdge` assertions (`:216`, `:225`) expect each org's `/v1/dependents` answer to contain that edge, which the D-10 kind filter withholds. The test skips without `PODIUM_POSTGRES_DSN` (`:104-106`), so only CI (`.github/workflows/test.yml:108`) would show the failure. CODE-7 now owns the whole file: the edges become `delegates_to`, `mustPut` loses its `extendsPin` parameter, and the fixture comment at `:135-139` is rewritten. TEST-8 allowlist category 5 now admits a legacy `extends` edge fixture only when it asserts that the edge is neither returned nor counted, the S8 command runs the test with `PODIUM_POSTGRES_DSN` set and a grep for `SKIP`, and the closing coverage run sets the variable.
- TEST-8 said to delete one assertion at `test/integration/store_retention_objects_test.go:110-111`, which is the fixture of the `extends:` child C. Under D-7 C is retired, so the pre-sweep load of C (`:120-122`) is fatal, and without the purge protection the parent is purged too, which breaks the `purged != 1` count (`:135-137`), the parent check (`:145-147`), and the post-sweep load (`:151-153`). The bullet now rewrites the test as the plain §8.4 deprecated-purge test `TestStoreRetention_PurgesDeprecatedVersionKeepsSharedBytes`, deleting the P and C fixtures, both loads of C, and the parent checks, and keeping the A purge, the A@2.0.0 survival, the count of 1, and the shared-bytes check. The TEST-8 ownership paragraph now gives CODE-7 the compile-only edits and leaves this rewrite to the TEST-8 bullet.

### Pass 13 (2026-11-10, automated)

- SPEC-1 Edit 4 said the registry treats a retired row as a soft-deleted row with the single exception of a layer restore, while the quota read (`pkg/registry/core/quota.go:32-38`, reported as `storage_bytes` at `pkg/registry/server/quota.go:38`) and the ingest helpers `tenantHasArtifact` and `tenantHasNonDeprecatedVersion` (`pkg/registry/ingest/ingest.go:284-311`) read `ListManifests`, which excludes soft-deleted rows (`pkg/store/sqlite.go:610-611`; `pkg/store/store.go:141-145`) and includes retired ones. As staged, the spec required retired rows to be left out of storage usage and out of the deprecation-flip test, which contradicts the Non-goals, and neither outcome landed in spec text. SPEC-1 Edit 4 now scopes the soft-delete treatment to the ingest collision check, the registry-assigned-version rules, and every read path, and states the exceptions: a layer restore does not serve the row again, the §4.7.8 quota read counts it toward storage usage, and ingest counts it as a stored earlier version when it decides whether a new version flipped `deprecated: true` (§7.3.2; `pkg/registry/ingest/ingest.go:811`, `:826-829`). D-7 names the quota read as that exception with the same citations. The edge-case row, renamed to cover the deprecation flip, quotes the new sentence, the restore row quotes the reworded restore clause, both quota Non-goals point at SPEC-1 Edit 4, and TEST-4 asserts that `Quota` counts the bytes of fixture R. No code changes, because neither reader changes.

### Prune after pass 13 (2026-11-10, automated)

- TEST-8's per-file bullets kept drawing test-disposition findings that an implementor finds by running the tests at S8. The bullets whose only content was that a named test fails under the new code and how to rewrite it are deleted: `dependency_rank_test.go:39`, `autoversion_test.go:355`, `sync_test.go:263-284`, the `store_retention_objects_test.go` rewrite, `stored_row_admission_test.go`, `reingest_pipeline_test.go`, the two `plain_skill_ingest_test.go` test rewrites, and the comment-only edits. The `delivery_attestation_test.go` bullet keeps only its speccov-drift constraint, and the `plain_skill_ingest_test.go` bullet keeps only the `psOverlay` deletion timing. The helper moves, the compile-order bullets, the doc-claim end-to-end rewrites, and the completeness gate stay. An IMPLEMENTOR'S CHOICE after the list now governs every other file the gate lists. Its constraint requires each surviving test to pass at S8, or at the step of S9 to S15 that rewrites it, which the ordering constraints require because those steps rewrite tests that fail at S8. The S8 Postgres selector now reads `StoreRetention`, because the deleted bullet named the rewritten test.
- The retired-row exceptions were stated in D-7, in two Non-goals, and in an edge-case row, and the copies disagreed: D-7 named the quota read as "the exception" while SPEC-1 Edit 4 stages three, and the `ArtifactCountQuota` consequence appeared only in a Non-goal. D-7 now holds the one exception list, *Exceptions*, in the three items SPEC-1 Edit 4 stages: a layer restore does not revive the row, the §4.7.8 quota read and reembedding count it, and ingest's deprecation-flip test and artifact-count check read it, each with its code citations. The two Non-goals are one line that points at D-7, the storage-quota edge-case row cites D-7 *Exceptions* and SPEC-1 Edit 4 without restating the outcomes, and the two-layer edge-case row cites D-7 *Two-layer IDs*. SPEC-1 Edit 4 is unchanged.

### Pass 14 (2026-11-10, automated)

- The S14 command ran `go test ./pkg/sync/` over the whole package, while TEST-6 owns no `pkg/sync` test and S14 depended only on S6 and S8. After S3 the `pkg/sync` collision tests fail until TEST-3 rewrites them at S10: `assertOneCollision` requires the reason to contain `"declare extends: "+collisionID` (`pkg/sync/sync_collision_test.go:81`), and `TestRun_FilesystemCollisionResolvedByExtends` (`pkg/sync/sync_collision_test.go:187`) expects the sanctioned overlay. S15 also depended only on S5, S8, and S10, which did not support the ordering constraint that the full suite passes from S15. The S14 command now reads `go test ./cmd/podium-mcp/` with the SDK suites and states that it runs no `pkg/sync` test, S15 now depends on S5 and S8 to S14, and the ordering constraint states that S15 depends on every step from S8 to S14.
