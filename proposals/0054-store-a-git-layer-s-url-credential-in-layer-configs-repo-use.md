# Proposal 0054: Store a git layer's URL credential in `layer_configs.repo_userinfo` and a userinfo-free `repo` (step 2 of 2)

- Issue: (to be filed)
- Status: Approved (2026-11-08). Signed off as staged, with the open questions settled as recorded in "Resolved decisions" (RD-1 through RD-4). Verified on 2026-10-10 after 3 adversarial review rounds (2 findings fixed). Implementation waits for the step-1 release tag (D-5).
- Date: 2026-10-10

This document stages the proposed spec, code, test, and documentation changes. It does not modify any spec, code, or doc file. Apply the changes in the staged sections after sign-off.

The version placeholders `<step-1>` and `<step-2>` appear throughout. `<step-1>` is the released version that carries proposal 0053, planned as 0.5.3 and not yet tagged. `<step-2>` is the release that carries this proposal, the next MINOR, which is 0.6.0 when step 1 ships as 0.5.3 (RD-2). The implementation substitutes the literal version numbers once the step-1 tag exists (D-5, RD-4).

## Summary

**What changes.**

- §7.3.1 "Repository credentials" is reworded: a registration retains the registered value unchanged, and the SQLite and Postgres metadata stores write the userinfo of a URL with a scheme apart from the remainder of the value (SPEC-1).
- §13.4 gains one paragraph for a release that changes only the form in which the store holds a value, where an earlier version already reads both forms. Such a release rewrites no row and names a version floor in its changelog (SPEC-2, RD-3).
- The store write rule in `pkg/store/layer_repo.go` returns two values. `LayerRepoColumn` is replaced by `LayerRepoColumns`, which returns the bare `repo` and the userinfo for a value in the split class, and the value whole with a NULL userinfo for every other value (CODE-1).
- The SQLite and Postgres `PutLayerConfig` statements bind `repo_userinfo` from the write rule in place of a literal NULL, so every put writes the new form (D-8, RD-1). The memory backend keeps the registered bytes in its map (CODE-2).
- Store tests pin the two-column contents and the read of a row in the earlier form, and the tests frozen to the 0.5.2 writers are deleted. Handler, ingest, `admin migrate`, and end-to-end tests read the raw row over SQLite. One manual scenario is staged (TEST-1, TEST-2, TEST-3).
- The changelog names the rollback floor, the operator guide gains a subsection on the floor and on the effects of running 0.5.2 against a written database, `docs/deployment/clustered.md` and the `podium admin migrate-to-standard` reference gain one pointer each, and the layer pages change "stores" to "keeps" (DOC-1, DOC-2, DOC-3).
- The release vehicle is the next MINOR, which is 0.6.0 when step 1 ships as 0.5.3 (D-16, RD-2).

**Fixed decisions.**

- No migration, backfill, or startup rewrite runs. A row that holds the credential inside `repo` keeps working, and the read rule `SplitLayerRepo` is unchanged (D-1, user decision, closed).
- No client-observable byte changes, and nothing proposal 0052 built is removed or weakened (D-2, user decision, closed).
- Encryption at rest is out of scope (D-3, user decision, closed).
- A value outside the split class of proposal 0053 is written to `repo` whole with a NULL `repo_userinfo` (D-4, user decision, closed).
- This step ships in a release after `<step-1>`. The code is not merged to `main` until the step-1 tag exists (D-5, user decision, closed).
- For a split-class value, `repo` receives `Parts.Bare` and `repo_userinfo` receives `Parts.RawUserinfo` as a non-NULL string, the empty string included (D-6).
- The write splits `string(cfg.RegisteredRepo)` when it is present and valid, and `cfg.Repo` otherwise (D-7).
- `LayerConfig.RegisteredRepo` keeps holding the whole registered URL. `CloneRepo`, `reportedRepo`, `layerConfigEqual`, the guard predicate, and the `LayerRepoCredential` conformance case are unchanged (D-9).
- `pkg/layer/source/git.go` is unchanged and go-git receives the same bytes (D-10).
- The memory backend does not take the two-part form (D-11).
- The names `LayerRepoColumns` and `registeredLayerRepo` are fixed, and no alias for `LayerRepoColumn` is kept (D-12).
- The write returns the two-part form only when the read rule recombines it into the registered bytes (D-13).
- The §7.3.1 edit names no table or column, and the guard paragraph of §7.3.1 is not edited (D-14).
- The changelog entry and the operator documentation state the rollback floor `<step-1>`, the stop-all rule for an upgrade that skips `<step-1>`, and the mixed-version behavior (D-17).
- Rollback to 0.5.2 is unsupported. Its effects are documented and no code mitigates them (D-18).
- The tests frozen to the 0.5.2 writers are deleted, and no frozen step-1 writer replaces them (D-19).
- No §6.10 error code, `PODIUM_*` variable, `registry.yaml` key, HTTP member, CLI flag, SDK surface, SPI method, or schema statement changes (D-20).

**Watch out for.**

- **The handler and orchestrator suites run on the memory store.** `pkg/registry/server/layers_test.go:25`, `pkg/registry/server/layer_repo_credential_test.go:326`, and `pkg/registry/ingest/orchestrator_test.go:116` use `store.NewMemory()`, whose format this proposal does not change. Their passing unmodified says nothing about this change. The evidence over the changed backends is listed in TEST-2.
- **A NULL bind for empty userinfo changes the cloned bytes.** The read tests `userinfo.Valid` (`pkg/store/layer_repo.go:72`). `https://@git.acme.com/x.git` must store `repo_userinfo = ''`. A NULL there reads back as `https://git.acme.com/x.git`, with an empty `RegisteredRepo`, and the guard stops firing for the layer.
- **The write must split `cfg.Repo` when `RegisteredRepo` is absent.** Registration (`pkg/registry/server/layers.go:1475-1479`, `:1596`) and the declared-layer boot seed (`internal/serverboot/serverboot.go:645`, `:746`) pass the credential in `Repo` alone. A rule that splits only a present `RegisteredRepo` leaves every new registration in the earlier form until its first completed ingest.
- **The existing final assertion of the Postgres column test stops detecting a missing `repo_userinfo = EXCLUDED.repo_userinfo`.** After this change the put-back writes the same `ghp_tok3n` the test planted. TEST-1(b) adds a put with a userinfo-free `Repo` and asserts a NULL column, which is the assertion that fails without the clause.
- **`rawLayerRepo` is unexported in package `store`'s tests.** Tests outside `pkg/store` read the row with their own `sql.Open("sqlite3", path)` query, on the precedent of `test/e2e/stored_row_admission_test.go:45`.
- **A soft delete and a restore write the row without converting it.** `DeleteLayerConfig` and `RestoreLayerConfig` update `deleted_at` alone (`pkg/store/sqlite.go:916`, `:937`). No spec sentence, changelog sentence, or test may say that every write of a row converts it.
- **An update that restates what the layer holds writes nothing.** The update handler returns before `PutLayerConfig` when `layerConfigEqual(before, cfg)` holds (`pkg/registry/server/layers.go:1327`), so such an update does not convert the row.
- **The changelog entry is copied verbatim into the GitHub Release body.** A repo-relative markdown link does not resolve there. DOC-1 names the docs page as a backticked path.
- **The changelog entry must not use the phrase "migration of the values stored under the schema".** `docs/deployment/operator-guide.md:155-162` ties that phrase to the maintenance-window and backup-restore procedure, which does not apply here.
- **The 0.5.2 frozen writers are deleted and no step-1 frozen writer replaces them.** Their own comments direct the deletion. The step-1 statement differs from the new one only in binding NULL, so a raw `UPDATE` reaches the same row state (TEST-1(c)).
- **Postgres tests skip without `PODIUM_POSTGRES_DSN`, and parts of `test/e2e` skip silently on macOS.** Run both with `-v` and confirm that no changed test reports `SKIP`.
- **`pkg/store/storetest/storetest.go` is not edited.** Its `empty-userinfo` row (`:707`, `:740-760`) already fails on a SQL backend that binds NULL for empty userinfo, and assertion branches in that file lower `codecov/patch`.

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. The §7.3.1 "Repository credentials" sentence is replaced with the retained-value and store-write wording.
      Levels: —. Depends on: —
- [ ] **S2 · spec** — SPEC-2. §13.4 gains the paragraph for a change of stored form with a version floor.
      Levels: —. Depends on: —
- [ ] **S3 · code** — CODE-1, CODE-2, TEST-1. `LayerRepoColumns` and `registeredLayerRepo` replace `LayerRepoColumn`, the SQLite and Postgres writers bind both columns, the memory writer calls `registeredLayerRepo`, and the store tests follow. The three are bundled because the rename does not compile without its callers and the existing raw-column assertions fail from the commit that changes the write.
      Levels: unit, conformance, integration. Depends on: S1, and S2 when it is not skipped
- [ ] **S4 · test** — TEST-2. The SQLite handler test, the post-ingest stamp test, the `admin migrate` raw-row assertion, and the raw read between stop and restart in the end-to-end test.
      Levels: unit, integration, e2e. Depends on: S3
- [ ] **S5 · docs** — DOC-2. The operator guide subsection "Rollback floor for the layer credential columns", the pointer sentences, and the `clustered.md` and `cli.md` sentences. The version literals are substituted only after the step-1 tag exists.
      Levels: —. Depends on: S3
- [ ] **S6 · docs** — DOC-3. "stores" becomes "keeps" on the layer pages, with the link to the DOC-2 subsection.
      Levels: —. Depends on: S5
- [ ] **S7 · docs** — DOC-1. The changelog entry, added only after the step-1 tag exists and with the released version substituted for `<step-1>`.
      Levels: —. Depends on: S5
- [ ] **S8 · test** — TEST-3. Scenario S90 in `test/manual-validation.md` and its index row.
      Levels: manual. Depends on: S3

**Ordering constraints.** S1 and S2 land as their own commits, with `make coverage-gate` run before any code change. S3 through S8 land in one pull request, so the 85% coverage bar on the new lines is met when the pull request is measured. The pull request is not merged to `main` until `git tag -l` shows the step-1 version (D-5).

## Current state and the gap

### The credential remains inside the `repo` column

Proposal 0053 is implemented on the branch `proposal/layer-repo-credential-columns` (HEAD `754ebcc0`). It is planned as 0.5.3 and is not released: `git tag -l` ends at `v0.5.2`. It split a git layer's URL credential at the store boundary only.

- A store read returns `LayerConfig.Repo` without userinfo and the registered URL in `LayerConfig.RegisteredRepo` (`pkg/store/layer_repo.go:55-62`).
- The write rule persists the whole registered URL in the `repo` column. `LayerRepoColumn` returns the registered bytes, or `cfg.Repo` unchanged when `RegisteredRepo` is absent (`pkg/store/layer_repo.go:98-108`).
- SQLite binds `repo_userinfo` to a literal NULL (`pkg/store/sqlite.go:849-855`). Postgres binds NULL and assigns `repo_userinfo = EXCLUDED.repo_userinfo` (`pkg/store/postgres.go:1290-1317`).

The credential therefore remains at rest inside `repo`. A reader of that one column, such as a SQL report, a support dump, or a later `SELECT`, receives the credential together with the reportable URL.

### The read already accepts the target format

`recombineLayerRepo` (`pkg/store/layer_repo.go:71-87`) honors a non-NULL `repo_userinfo`, the empty string included, when `repo` is outside the split class and `repourl.Join(repo, userinfo)` splits back into the same `Bare` and `RawUserinfo` (`internal/repourl/repourl.go:76-118`). For every split-class value X, `Split(X).Bare` is outside the split class and `Join(Bare, RawUserinfo)` equals X byte for byte. That was confirmed by reading the classifier and by an exhaustive enumeration of 1,768,735 split-class values, which found no violation.

The SQL reads select the column on the get, the list, and the deleted list (`pkg/store/sqlite.go:871`, `:887`, `:963`; `pkg/store/postgres.go:1338`, `:1359`, `:1450`). No production code outside the two SQL backends reads `layer_configs.repo`. Consumers use `CloneRepo()` (`pkg/registry/ingest/orchestrator.go:111`; `pkg/registry/server/layers.go:1701`, `:1912`; `pkg/registry/server/webhook_ingest.go:81`) or `reportedRepo` (`pkg/registry/server/layers.go:350-351`).

### The remaining change is the write

These input forms reach `PutLayerConfig`.

- **A caller-built config** carries the credentialed URL in `Repo` and no `RegisteredRepo`. The writers are registration (`pkg/registry/server/layers.go:1475-1479`, `:1596`) and the declared-layer boot seed (`internal/serverboot/serverboot.go:645`, `:746`).
- **A store-returned config** carries a clean `Repo` and a `RegisteredRepo`. The writers are update (`pkg/registry/server/layers.go:1331`), reorder (`:1812`), the post-ingest stamp (`pkg/registry/ingest/orchestrator.go:202`), and `podium admin migrate` (`cmd/podium/admin_migrate.go:378`).

The write rule must convert both. Restore does not write the repo columns: it runs `RestoreLayerConfig` (`pkg/registry/server/layers.go:1704`), which clears `deleted_at` alone.

### The change is one-way below the step-1 release

v0.5.2 never names the column. It clones with `URL: cfg.Repo` and no `Auth` (`pkg/layer/source/git.go:49` at the `v0.5.2` tag), resets the column on SQLite through `INSERT OR REPLACE`, and leaves it untouched on Postgres.

These spec sentences were satisfied literally in step 1 and need a decision in this step:

- §7.3.1: "A registration accepts that value and stores it unchanged, and the ingest clones with the stored value."
- §13.4: "A release that changes how a stored value is computed rewrites the affected rows in place".

Docs pages say the registry stores the URL "as given" (`docs/deployment/layers.md:115`, `docs/reference/http-api.md:418`, `docs/reference/cli.md:455`). `docs/deployment/operator-guide.md:157` and `:162` state a general coexistence and rollback rule with no floor for this change.

## Decisions

- **D-1. No rewrite of existing rows (user decision, closed).** No migration, backfill, or startup rewrite runs. A row that holds the credential inside `repo` keeps working. The step-1 read rule, `SplitLayerRepo` and `recombineLayerRepo` (`pkg/store/layer_repo.go:55-87`), is unchanged and reads both formats.
- **D-2. No client-observable change (user decision, closed).** Nothing proposal 0052 built is removed or weakened. Registration and update accept URL userinfo unchanged, every response reports the same `repo`, and the clone presents the same credential. The `[redacted]` class, `RedactCloneError`, the boot-log redaction, and the re-registration guard with `details.constraint: "redacted_repo"`, `force_repo_overwrite`, and `--force-repo-overwrite` stay as they are.
- **D-3. No encryption (user decision, closed).** Encryption at rest is out of scope.
- **D-4. Values outside the split class stay whole (user decision, closed).** An scp-like remote, a filesystem path, the network-path form such as `//tok@host/x://y`, and a fail-closed `[redacted]` value are written to `repo` whole with a NULL `repo_userinfo`, as in step 1.
- **D-5. A separate, later release (user decision, closed).** Step 2 ships in a release after the one that carries step 1 and never in the same version. Implementation precondition: the step-2 code is not merged to `main` until the step-1 tag exists, so the two changelog entries cannot land in one release section. The step-2 changelog entry and the operator documentation state the rollback floor.
- **D-6. Write format.** For a registered value in the split class, `repo` receives `Parts.Bare` and `repo_userinfo` receives `Parts.RawUserinfo` as a non-NULL string, the empty string included. `https://@host/x.git` stores `repo_userinfo = ''`. Binding NULL for empty userinfo would change the cloned bytes, because the read tests `userinfo.Valid`. Both columns are bound in the one existing statement per backend, so no interleaving of writers leaves a pair that is half in the earlier form and half in the new one.
- **D-7. The value the write splits.** It is `string(cfg.RegisteredRepo)` when that field is present, after the existing split-class and `Clean == Repo` validation, which still returns `ErrRepoCredentialMismatch`. It is `cfg.Repo` otherwise. Splitting `cfg.Repo` is required because registration and the declared-layer boot seed pass the credential in `Repo` with no `RegisteredRepo`.
- **D-8. Every `PutLayerConfig` writes the new format (RD-1).** No signal crosses the `RegistryStore` interface, and no backend reads before it writes.
  - An existing row is never rewritten by an upgrade step. It keeps working unconverted for as long as nothing writes its repo columns.
  - It converts at the next ordinary write of its layer: the post-ingest stamp after a completed ingest, an update that changes the layer, a reorder (which puts every listed layer), a re-registration, the declared-layer boot seed (every start, so a declared git layer converts at the first start of the `<step-2>` binary), or `podium admin migrate` into a target.
  - A soft delete and a restore convert nothing.
  - The registration-only alternative is rejected. A `LayerConfig` signal field changes the SPI data type and the conformance suite. A store-side rule keyed on insert versus conflict adds a read-dependent write to each backend and cannot tell a same-URL re-registration from a stamp. Both leave every existing credential inside `repo` indefinitely, which is the gap this step closes.
- **D-9. `RegisteredRepo` keeps the whole registered URL.** A credential-only field would change the `RegistryStore` SPI data contract, the `LayerRepoCredential` conformance case (`pkg/store/storetest/storetest.go:696-791`), `layerConfigEqual` (`pkg/registry/server/layers.go:606`), `CloneRepo`, `reportedRepo`, and the guard predicate, and it would move the byte-exact join out of the store into every consumer. The at-rest change needs none of that, because the read recombines the two columns into the same `RegisteredRepo`.
- **D-10. go-git input is unchanged.** Passing the credential to go-git through `Auth` with a userinfo-free URL stays out. `pkg/layer/source/git.go` is unchanged. The reasons in the Non-goals of proposal 0053 hold: go-git derives no `Auth` for a password-only URL (the header comes from net/http), so explicit `BasicAuth` changes same-host redirect behavior, and the change is separable from the stored format.
- **D-11. The memory backend's behavior does not change.** It has no column, lives for the process lifetime only, is reachable only through `PODIUM_REGISTRY_STORE=memory` (an undocumented test affordance, `internal/serverboot/serverboot.go:2664-2669`), and its map is never serialized. It keeps the registered bytes in its map and reads with `SplitLayerRepo(cfg.Repo, sql.NullString{})`. Its source changes by one call (D-12) and one comment (`pkg/store/memory.go:464-465` says the map holds what a SQL row holds, which becomes false).
- **D-12. One write rule with two results.** The exported `LayerRepoColumn` is replaced by `LayerRepoColumns(cfg LayerConfig) (repo string, userinfo sql.NullString, err error)`. It is built on the unexported `registeredLayerRepo(cfg) (string, error)`, which holds the present validation and returns the registered bytes. The SQL backends call `LayerRepoColumns`, and the memory backend calls `registeredLayerRepo`. No compatibility alias is kept, because the project is pre-1.0 and every caller is updated.
- **D-13. The write degrades to the earlier form.** `LayerRepoColumns` returns the two-part form only when `recombineLayerRepo(parts.Bare, userinfo)` equals the registered bytes. Otherwise it returns the registered bytes whole with an invalid `NullString`. That branch cannot run for the current classifier, and it carries a comment that says so. It exists so that a later classifier change writes the step-1 format and never stores a row that reads back without its credential.
- **D-14. The §7.3.1 edit is minimal.** "stores it unchanged" is reworded to state that the registered value is retained unchanged and that the SQLite and Postgres stores write the userinfo of a split-class value apart from the rest. The spec names no table or column today (a grep of `spec/` and `docs/` for `layer_configs`, `repo_userinfo`, and `RegisteredRepo` returns nothing), and the edit names none. The §7.3.1 guard paragraph already uses "stored `repo`" in the logical sense, since the guard would never fire if the phrase meant a userinfo-free column. That paragraph is not edited, and the new text states the reading explicitly.
- **D-15. §13.4 gains one paragraph (RD-3).** The current text requires an in-place rewrite and a stop of every process on the previous version for "a release that changes how a stored value is computed". Step 2 changes the form in which a value is held. The value the registry computes with is unchanged, the release before it reads both forms, and D-1 forbids the rewrite. Without the paragraph a reader can hold step 2 to the rewrite rule.
- **D-16. Release vehicle: the next MINOR (RD-2).** No client-observable behavior changes, and a rollback of one release, to `<step-1>`, works. That is what `docs/deployment/operator-guide.md:162` promises for "the previous version's binary". The release nevertheless ends compatibility with 0.5.2 in these operator-visible ways: a 0.5.2 binary can no longer run against the database, and a rolling upgrade directly from 0.5.2 is not permitted. Under the pre-1.0 rule that places a backward-incompatible change in a MINOR bump, the vehicle is 0.6.0 if step 1 ships as 0.5.3. PATCH was considered on the client-facing reading and not chosen.
- **D-17. Operator-facing statements.** The changelog entry and the docs carry these.
  1. The lowest version that runs against a database this release has written to is `<step-1>`, for rollback and for mixed-version replicas alike.
  2. An upgrade from 0.5.2 or earlier that skips `<step-1>` stops every replica first, and its rollback target is `<step-1>` and no lower version.
  3. `<step-1>` and `<step-2>` replicas that share one Postgres read and clone identically. A `<step-1>` replica writes the earlier format on every layer write it makes, so a row can alternate formats during the roll. The at-rest property holds for a row from its first write after the last `<step-1>` replica is gone.
  4. The stored-value-migration procedure of the operator guide (maintenance window, backup restore, no canary) does not apply.
  5. A SQL dump of `layer_configs` needs both columns to reproduce a layer's remote, and `repo_userinfo` holds a secret.
  6. `podium admin migrate-to-standard` copies through the store interface, so the target receives the format of the binary that runs the command.
  7. The effects of a 0.5.2 binary on a written row (D-18).
- **D-18. Rollback to 0.5.2 is unsupported, and its effects are documented.** They were verified against the `v0.5.2` tag.
  - An admin-registered or user-registered git layer whose row is in the new format clones without its credential, so a private remote fails with `ingest.source_unreachable`. The layer is reported with the bare `repo`, which differs from the earlier reported form for a non-canonical URL, and the `redacted_repo` guard does not fire for it.
  - A layer declared in the registry configuration is reseeded from the configured URL at each start and clones as before.
  - On SQLite, any 0.5.2 `PutLayerConfig` for the row (an update, a re-registration, a reorder, the boot seed, or a stamp after a clone that succeeded) resets `repo_userinfo`, and the credential is then gone from the store. A failed clone, a soft delete, and a restore leave it.
  - On Postgres, the 0.5.2 upsert leaves the column, so the credential survives a roll-forward when 0.5.2 did not change `repo`. If a re-registration under 0.5.2 replaced the layer's `repo` with a different userinfo-free URL, the first read after roll-forward joins the earlier credential to the new remote. The operator re-registers that layer after rolling forward, before it is ingested. A re-registration is the only 0.5.2 write that produces this row for a layer registered through the API or the CLI, because the update handler never assigns `repo` (`pkg/registry/server/layers.go:1252-1254`, and the same comment at `v0.5.2:pkg/registry/server/layers.go:1238-1239`; `spec/07-external-integration.md:121`). The 0.5.2 boot seed also replaces `repo` for a declared layer whose configured URL changed (`v0.5.2:internal/serverboot/serverboot.go:746`), and that row needs no operator action: every start of `<step-1>` or later writes the declared layer before the server serves any request, because the `bootstrapDeclaredLayers` call (`internal/serverboot/serverboot.go:1085`, whose put is `:645`) precedes `serveUntilShutdown` (`:1827`), which holds the only `srv.Serve` call (`:1839`). The upsert assigns both columns (`pkg/store/postgres.go:1317`).
- **D-19. Tests frozen to 0.5.2 are deleted.** Their own comments direct it (`pkg/store/sqlite_test.go:203-207`, `pkg/store/layer_repo_test.go:128-132`, `:321-332`). No frozen copy of a step-1 writer replaces them. One test per backend sets a row to the earlier form with raw SQL and pins that the row reads to the same `Repo` and `CloneRepo()` and that a put restores the two-part form (TEST-1(c)).
- **D-20. No new surface.** The proposal adds no §6.10 error code, `PODIUM_*` variable, `registry.yaml` key, HTTP member, CLI flag, SDK change, SPI method, schema column, or `LayerSourceProvider` input. The column already exists as nullable TEXT (`pkg/store/sqlite.go:148`, `pkg/store/postgres.go:135`, `pkg/store/schema_migrate.go:94`), and `applySchema` is untouched.

## Spec amendment: §7.3.1 Repository credentials (SPEC-1)

**Anchor.** `spec/07-external-integration.md`, §7.3.1, the paragraph that begins "**Repository credentials.**". Replace its second sentence:

> A registration accepts that value and stores it unchanged, and the ingest clones with the stored value.

with:

> A registration accepts that value and retains it unchanged, and the ingest clones with the registered value. Where the registered value parses as a URL with a non-empty scheme and with userinfo, an empty userinfo included, and is not a value this paragraph reports as `[redacted]`, the SQLite and Postgres metadata stores write the userinfo apart from the remainder of the value, and the registry joins the two, byte for byte, to obtain the registered value. Those stores write every other `repo` whole, and a `repo` a store holds whole is read as held. In this section the stored `repo` is the registered value, in whichever form the store holds it.

No other sentence of the paragraph changes, and the paragraph that begins "**Re-registration with a reported `repo`.**" is not edited.

Notes on the wording, for the implementor and the reviewer:

- The rule is stated as what the stores write and what a read accepts. It stays true for a row that no write has converted, for a soft delete and a restore, and during a mixed-version roll.
- The clause "a `repo` a store holds whole is read as held" is the spec basis for existing rows (D-1). The text carries no release history and no statement of when a row converts. The version floor belongs to §13.4 (SPEC-2), the changelog, and the operator guide.
- The rule is scoped to the SQLite and Postgres stores, so it binds neither the memory backend (D-11) nor the `RegistryStore` conformance suite (D-9).
- Empty userinfo is named because D-6 depends on it.

The edit lands on its own commit, and `make coverage-gate` runs before any code change.

## Spec amendment: §13.4 Migrations, a change of stored form (SPEC-2)

**Anchor.** `spec/13-deployment.md`, §13.4. Insert one paragraph immediately before the last paragraph of the section, which begins "Type definitions are compiled into the binary and evolve with it."

> The rewrite rule this section states for a release that changes how a stored value is computed does not govern a release that changes only the form in which the store holds a value, where an earlier version already reads both forms and returns the same value from each. Such a release rewrites no row at start and records no completion. Its changelog names the earliest version that reads the new form. A process on that version or a later one coexists with a process on the new release. No process on a version below it runs against the store once the new release has written to it, and an upgrade from such a version stops every process on it before the first process on the new release starts. The stored form of a `git` layer's `repo` (§7.3.1) is such a value: §7.3.1 states which values the SQLite and Postgres stores write in two parts and that a value held whole is read as held, and a process on an earlier version that reads both forms writes the whole form.

The existing sentence "The release's changelog names the change and the order in which the registry and the consumers roll." is left as it is, and no existing paragraph of §13.4 changes.

The draft anchored the paragraph directly after the paragraph that begins "The additive guarantee covers the schema and not the values stored under it." That position puts it between the rewrite rule and the sentence "The §4.7.6 content hash is such a value.", which would then read as placing the content hash in the new class. The paragraph therefore lands after the content-hash and admission paragraphs, and its opening words name the rule it excepts in place of "The rewrite rule above".

Notes on the wording:

- Every rule is keyed to the floor version the changelog names. "The release before it" is avoided, because a patch release between step 1 and step 2 would make that phrase name the wrong version.
- The paragraph makes no per-row conversion claim. §7.3.1 as edited by SPEC-1 owns the write rule, so the conversion scope (RD-1) touches one spec site.
- "Writes the whole form" is stated as a fact of this instance and is not a condition of the class.
- The "without downtime" sentence of §13.4 covers an upgrade "that changes only the schema". An upgrade from 0.5.2 to `<step-2>` is not schema-only, so the stop-all rule for it does not conflict.

## Proposed solution

### CODE-1. `pkg/store/layer_repo.go`

Replace `LayerRepoColumn` with the following. The read rule, `recombineLayerRepo`, and `ErrRepoCredentialMismatch` are untouched.

```go
// registeredLayerRepo returns the registered remote a config carries: its
// RegisteredRepo when present and valid, and its Repo otherwise.
func registeredLayerRepo(cfg LayerConfig) (string, error) {
	if !cfg.RegisteredRepo.Present() {
		return cfg.Repo, nil
	}
	registered := string(cfg.RegisteredRepo)
	parts, ok := repourl.Split(registered)
	if !ok || parts.Clean != cfg.Repo {
		return "", ErrRepoCredentialMismatch
	}
	return registered, nil
}

// LayerRepoColumns is the write rule every SQL RegistryStore applies in
// PutLayerConfig. It returns the values of the repo and repo_userinfo columns.
//
// A registered value in the split class is returned as its bare form and a
// valid userinfo, the empty string included. Every other value is returned
// whole with an invalid NullString. A config whose RegisteredRepo is outside
// the split class or does not redact to its Repo returns
// ErrRepoCredentialMismatch.
//
// Spec: §7.3.1 (Repository credentials)
func LayerRepoColumns(cfg LayerConfig) (string, sql.NullString, error) {
	registered, err := registeredLayerRepo(cfg)
	if err != nil {
		return "", sql.NullString{}, err
	}
	parts, ok := repourl.Split(registered)
	if !ok {
		return registered, sql.NullString{}, nil
	}
	userinfo := sql.NullString{String: parts.RawUserinfo, Valid: true}
	// This branch cannot run for the current classifier: Join(Bare,
	// RawUserinfo) returns the registered bytes for every split-class value.
	// It keeps the registered bytes whole if a classifier change breaks that,
	// so a row is never written in a form that reads back without its
	// credential.
	if recombineLayerRepo(parts.Bare, userinfo) != registered {
		return registered, sql.NullString{}, nil
	}
	return parts.Bare, userinfo, nil
}
```

Update the `SplitLayerRepo` doc comment only where it refers to the write rule by name.

### CODE-2. `pkg/store/sqlite.go`, `pkg/store/postgres.go`, and `pkg/store/memory.go`

- **SQLite `PutLayerConfig` (`pkg/store/sqlite.go:837-860`).** Call `repo, userinfo, err := LayerRepoColumns(cfg)`. The VALUES list ends `?, ?)` for the last two columns in place of `?, NULL)`, which gives 22 placeholders, and `userinfo` is appended as the last argument.
- **Postgres `PutLayerConfig` (`pkg/store/postgres.go:1273-1325`).** Make the same call. The VALUES list becomes `($1, ..., $21, $22)`, and `userinfo` is appended as the last argument. The `ON CONFLICT` list is unchanged. It already assigns `repo_userinfo = EXCLUDED.repo_userinfo`, which also clears the column when a later write carries no userinfo, as a `force_repo_overwrite` registration does.
- Both keep the comment that the write rule runs before any statement, so a refused config writes nothing.
- **Memory `PutLayerConfig` (`pkg/store/memory.go:464-478`).** Call `repo, err := registeredLayerRepo(cfg)` in place of `LayerRepoColumn(cfg)`. The doc comment becomes: "The map holds the registered bytes in Repo and no RegisteredRepo. The memory store has no repo_userinfo slot and nothing reads its map but this file, so it does not take the two-column form the SQL stores write."

No `SELECT`, scan function, schema statement, or `schema_migrate.go` entry changes. No file under `pkg/registry`, `pkg/layer/source`, `internal/serverboot`, or `cmd/` changes.

## Edge cases and accepted failure modes

"Rollback floor" is the subsection DOC-2 adds to `docs/deployment/operator-guide.md`. "Layer object" is the table in `docs/reference/http-api.md`. Spec quotations are from §7.3.1 as SPEC-1 amends it and from §13.4 as SPEC-2 amends it.

| Case | Outcome | Governing text; documented in |
|:--|:--|:--|
| `https://alice-user:s3cr3tpw@git.acme.com/acme/x.git` registered on SQLite or Postgres | `repo` holds `https://git.acme.com/acme/x.git` and `repo_userinfo` holds `alice-user:s3cr3tpw`. Every response and the clone are as before | §7.3.1 "write the userinfo apart from the remainder of the value"; Rollback floor |
| Empty userinfo, `https://@host/x.git` or `https://:@host/x.git` | `repo_userinfo` holds `''` or `:` as a non-NULL value. The read returns the registered bytes, and the guard fires as before | §7.3.1 "an empty userinfo included"; Rollback floor |
| Non-canonical registered URL, such as `HTTPS://t@host/a b.git` | `repo` holds the registered bytes with the userinfo and its `@` cut out, with no re-escaping. The read joins them byte for byte | §7.3.1 "joins the two, byte for byte"; layer object |
| scp-like remote, filesystem path, network-path value, userinfo-free URL, or empty `repo` | `repo` holds the value whole and `repo_userinfo` is NULL | §7.3.1 "Those stores write every other `repo` whole"; Rollback floor |
| Fail-closed value, such as `https://ghp_tok/3n@host/x.git` | Held whole in `repo`, credential included, and reported as `[redacted]` (accepted, D-4) | §7.3.1 "is not a value this paragraph reports as `[redacted]`"; Rollback floor |
| Row written before `<step-2>`, never written since | Keeps the credential inside `repo` and reads, reports, and clones as before (accepted, D-1) | §7.3.1 "a `repo` a store holds whole is read as held"; Rollback floor, DOC-1 |
| Same row after a completed ingest, an update that changes the layer, a reorder, a re-registration, or a start for a declared layer | Takes the two-part form | §7.3.1 "write the userinfo apart"; DOC-1, Rollback floor |
| Same row after a soft delete, a restore, a failed ingest, or an update that changes nothing | Unchanged, in the earlier form | §7.3.1 "read as held"; Rollback floor |
| Re-registration of a credentialed layer with `force_repo_overwrite` and the reported URL | `repo` holds the reported URL and `repo_userinfo` is NULL | §7.3.1 "stores the `repo` it carries"; `docs/deployment/layers.md` Private remotes |
| Memory backend | Holds the registered bytes in its map. No at-rest surface exists (accepted, D-11) | §7.3.1 "the SQLite and Postgres metadata stores"; no docs page, a test affordance |
| Custom `RegistryStore` | Bound by the `LayerRepoCredential` conformance case as before, and by no at-rest rule | §7.3.1 as above; D-9 |
| Config whose `RegisteredRepo` does not redact to its `Repo` | `PutLayerConfig` returns `ErrRepoCredentialMismatch` and writes nothing, as in step 1 | No client-observable outcome; D-7 |
| Classifier change that breaks the join invariant | The write returns the registered bytes whole with a NULL userinfo (D-13) | §7.3.1 "read as held"; no docs page, unreachable today |
| `<step-1>` and `<step-2>` replicas on one Postgres | Both read and clone identically. A `<step-1>` replica writes the whole form, so a row can alternate until the last `<step-1>` replica is gone (accepted) | §13.4 "a process on an earlier version that reads both forms writes the whole form"; Rollback floor |
| Rollback from `<step-2>` to `<step-1>` | Works. Rows keep their form until `<step-1>` next writes them, and then hold the whole form | §13.4 "A process on that version or a later one coexists"; Rollback floor, DOC-1 |
| Rolling upgrade from 0.5.2 directly to `<step-2>` | Not permitted. Every replica stops first | §13.4 "an upgrade from such a version stops every process on it"; Rollback floor, DOC-1 |
| 0.5.2 binary started against a database `<step-2>` wrote | Unsupported. A non-declared layer in the new form clones without its credential and fails with `ingest.source_unreachable`, and the `redacted_repo` guard does not fire for it | §13.4 "No process on a version below it runs against the store"; Rollback floor |
| The same, on SQLite, after any 0.5.2 put of the row | `repo_userinfo` is reset and the credential is gone from the store. The layer needs a registration with the full URL (accepted, D-18) | Rollback floor |
| The same, on Postgres, after a 0.5.2 re-registration replaced `repo` with a different userinfo-free URL | After roll-forward the read joins the earlier credential to the new remote. The operator re-registers the layer before it is ingested. No code mitigates it (deferred, Non-goals). A declared layer whose configured URL changed under 0.5.2 is rewritten in both columns at the next start on `<step-1>` or later and needs no action (D-18) | Rollback floor |
| `podium admin migrate-to-standard` | The target holds the form of the binary that runs the command | `docs/reference/cli.md` `podium admin migrate-to-standard` |
| SQL dump or report that reads `repo` alone | Receives no credential for a converted row and cannot reproduce the remote without `repo_userinfo` | Rollback floor |
| `repo_userinfo` at rest | Unencrypted (out of scope, D-3) | Rollback floor |
| Post-ingest stamp writes the config read before a long ingest began | A registration made during the ingest is overwritten, both columns together, as the whole row is today (pre-existing) | Non-goals |

## Testing

Every new or changed test carries `// Spec: §7.3.1 (Repository credentials)`. The earlier-form tests of TEST-1(c) also carry `// Spec: §13.4` when SPEC-2 lands. No §6.10 code is added, so no `// Matrix:` annotation is owed. Credential fixtures are the distinctive strings already in use (`alice-user`, `s3cr3tpw`, `ghp_tok3n`).

### TEST-1. Store tests

Targets: `pkg/store/layer_repo_test.go`, `pkg/store/sqlite_test.go`, and `pkg/store/store_test.go`. `pkg/store/schema_migrate_test.go` and `pkg/store/storetest/storetest.go` stay unmodified and must pass as they are. `TestSQLite_AdditiveMigration_SplitsLegacyLayerRepo` (`pkg/store/schema_migrate_test.go:252-256`) already asserts that the raw columns of a legacy row are unchanged after open, which pins D-1 at the open path.

- **Write-rule table.** Rewrite `TestLayerRepoColumn` (`pkg/store/layer_repo_test.go:73`) as `TestLayerRepoColumns`. The refusing rows are kept and assert `errors.Is(err, ErrRepoCredentialMismatch)`, an empty string, and an invalid `NullString`. The accepting rows assert a literal `(repo, userinfo)` pair. Add these rows:

  | Input | Expected `repo` | Expected userinfo |
  |:--|:--|:--|
  | `Repo: https://ghp_tok3n@git.acme.com/x.git`, no `RegisteredRepo` | `https://git.acme.com/x.git` | `ghp_tok3n`, valid |
  | `https://@git.acme.com/x.git` | `https://git.acme.com/x.git` | empty string, valid |
  | `https://:@host/x` | `https://host/x` | `:`, valid |
  | `https://a@b@host/x` | `https://host/x` | `a@b`, valid |
  | `HTTPS://t@host/a.git` | `HTTPS://host/a.git` | `t`, valid |
  | `ssh://u:p@h:22/x?y#z@w` | `ssh://h:22/x?y#z@w` | `u:p`, valid |
  | `git@github.com:acme/x.git` | the input | invalid |
  | `/srv/git/acme.git` | the input | invalid |
  | `//tok@host/x://y` | the input | invalid |
  | `https://ghp_tok/3n@host/x.git` | the input | invalid |
  | `https://git.acme.com/x.git` | the input | invalid |

- **Round-trip property test.** Over a generated corpus built from scheme prefixes and an adversarial token alphabet, assert for every input x that `SplitLayerRepo(LayerRepoColumns(LayerConfig{Repo: x}))` equals `SplitLayerRepo(x, sql.NullString{})`. This pins the invariant the unreachable branch of D-13 guards.

  **IMPLEMENTOR'S CHOICE:** the corpus alphabet and length bound — the corpus must contain `@`, `:`, `/`, `?`, `#`, `%`, a space, an uppercase scheme, an empty userinfo, and at least one value in each class outside the split class, and the test must finish within the package's normal unit-test time.

- **`pkg/store/store_test.go:240-250`.** The loop asserts that the column equals `Repo` for a credentialed value in `Repo` alone. It changes to the `LayerRepoColumns` pair, and the credentialed row expects the bare `repo` and a valid userinfo.
- **(a) Raw columns after a put.** Rename `TestSQLite_LayerRepoColumnHoldsRegisteredBytes` and `TestPostgres_LayerRepoColumnHoldsRegisteredBytes` to `...LayerRepoColumnsHoldBareAndUserinfo`. Assert the raw pair `{bare, {userinfo, true}}` after a caller-built put and after a Get-then-Put. For the existing fixture `https://alice-user:s3cr3tpw@git.acme.com/acme/a b.git` the bare form is `https://git.acme.com/acme/a b.git`, because `Parts.Bare` is cut from the registered bytes. Add table rows for empty userinfo (`{bare, {"", true}}`) and for the scp-like, path, network-path, and fail-closed values (`{value, NULL}`). The NULL rows also pin that the column stays nullable.
- **(b) Column read and replacement.** In `TestSQLite_LayerRepoUserinfoColumnRead` and the Postgres counterpart, the put-back assertions become `{bare, {"ghp_tok3n", true}}`. The Postgres test gains a put of the same layer with a userinfo-free `Repo` and no `RegisteredRepo`, asserts a NULL column afterward, and is renamed `TestPostgres_LayerRepoUserinfoColumnReadAndReplaced`. The comment at `pkg/store/sqlite_test.go:251-255` says the trailing NULL proves nothing about the bind. It becomes stale and is rewritten to describe what the test now asserts.
- **(c) A row in the earlier form.** One test per backend, `TestSQLite_LayerRepoEarlierFormRow` and `TestPostgres_LayerRepoEarlierFormRow`:
  1. This release puts a credentialed layer, and the raw row is `{bare, userinfo}`.
  2. A raw `UPDATE layer_configs SET repo = <registered>, repo_userinfo = NULL` puts the row in the form a `<step-1>` replica or a database that was never written by this release holds.
  3. The get, the list, and the deleted list return the same `Repo` and `CloneRepo()` as before the update.
  4. The raw columns are unchanged after `DeleteLayerConfig` and `RestoreLayerConfig`.
  5. A Get-then-Put restores `{bare, userinfo}` with the same store-boundary read.

  The test covers D-1 and the alternation of D-17 item 3 with no frozen statement.
- **(d) Deletions.** Delete `legacySQLitePutLayerConfig`, `legacyPostgresPutLayerConfig`, both `...LayerRepoRollbackWriter` tests, and `TestLegacyLayerWriters_NameNoUserinfoColumn`. Add no frozen step-1 statement and no guard test.
- **(e) Postgres evidence.** Record in the pull request that the Postgres tests ran in CI and locally against the standard stack, since they skip without `PODIUM_POSTGRES_DSN`.

The conformance suite is the concurrent-path and backend-parity evidence: `LayerRepoCredential` runs unmodified on the memory, SQLite, and Postgres backends, and its `empty-userinfo` row fails on a SQL backend that binds NULL for empty userinfo. Both columns travel in one statement, so the change adds no concurrent path of its own.

### TEST-2. Handler, ingest, migrate, and end-to-end tests

Targets: `pkg/registry/server/layer_repo_credential_test.go`, `pkg/registry/ingest/orchestrator_test.go`, `cmd/podium/admin_migrate_test.go`, and `test/e2e/layer_repo_credential_test.go`.

**Suites that must pass unmodified over the changed backends.** `TestLayerRepoCredential_CloneFailureReportsNoCredential` in `test/integration/layer_repo_credential_test.go` (SQLite, line 124), the two end-to-end tests in `test/e2e/layer_repo_credential_test.go` apart from the addition in (d), the existing boundary assertions of the admin-migrate test, and the `storetest` conformance case. These are the regression evidence for D-2. The handler and orchestrator suites on the memory store are not.

No test above `pkg/store` reads a raw column today. Each addition reads the row with one `sql.Open("sqlite3", path)` query and one combined struct comparison per step.

- **(a) Handler over SQLite.** One test in `pkg/registry/server/layer_repo_credential_test.go`, built with the existing `newGuardHarnessOn(t, guardAdmin, st)`, where `st` is `store.OpenSQLite` on a temp file. No new harness is added.
  1. Register `credRepoFull` through `POST /v1/layers`. The raw row is `{credRepoReported, {"alice-user:s3cr3tpw", true}}`, and the register and list responses report `credRepoReported`.
  2. Register the reported value. The response is the `redacted_repo` refusal, asserted through `assertRedactedRepoRefusal`, and the raw row is unchanged.
  3. Register with `force_repo_overwrite: true`. The raw row is `{credRepoReported, NULL}`.
- **(b) Post-ingest stamp.** Extend `TestSourceIngest_ProviderReceivesCloneRepo` or add a sibling, with the existing `fakeProvider` over `store.OpenSQLite`. No bare repository or git remote is added.
  1. Put the layer, then overwrite its row with raw SQL to the earlier form (`repo` is the registered URL and `repo_userinfo` is NULL).
  2. Get the layer, run `ingest.SourceIngest`, and assert that `provider.calledWithRepo` is the registered URL.
  3. Assert that the raw row is `{bare, {userinfo, true}}` with `last_ingested_ref` set.

  This pins the statement of D-8 and DOC-1 that a completed ingest converts a row.

  **IMPLEMENTOR'S CHOICE:** extending `TestSourceIngest_ProviderReceivesCloneRepo` or adding a sibling test — the existing memory-store assertions of that test must still run, and the new assertions must run over `store.OpenSQLite`.
- **(c) `admin migrate`.** In `TestAdminMigrateToStandard_PumpsMetadataAndObjects`, after the pump, read the raw row of `team-private` from `dstDB` and assert the two-part form. The existing boundary assertions stay.
- **(d) End to end.** Extend `TestLayerRepoCredential_SurvivesRestart_CLI`. Between `stopProc(srv.cmd)` and the second start, open the SQLite file and assert that the `private` row's `repo` equals `reportedWant` and its `repo_userinfo` equals `alice-user:s3cr3tpw`. The existing assertions after the restart (the reported repo, the refusal, the basic-auth pair at the remote, and the logs) stay unmodified and cover the clone. No successful reingest is added, because the fixture remote answers 500. The file has no macOS skip. Confirm with `go test -v` output that the test ran.
- **(e) Coverage.** Run `go test -coverpkg=./... -coverprofile=cover.out ./pkg/store/... ./pkg/registry/... ./cmd/podium/... ./test/integration/...`. Confirm that `LayerRepoColumns` and `registeredLayerRepo` reach 85%, with the D-13 branch documented as unreachable by its comment. Then run `make coverage-gate`.

## Manual validation

### TEST-3. Scenario S90

Add this row to the scenario index of `test/manual-validation.md`, after S89:

```markdown
| S90 | A git layer's URL credential is held apart from `repo` in the metadata store, and a row in the earlier form keeps working | standalone | none | none | none |
```

Add the scenario after S89. It binds ports 8191 and 8192, which no other scenario binds.

````markdown
## S90: A git layer's URL credential is held apart from `repo` in the metadata store, and a row in the earlier form keeps working

**Goal.** Validate that a `git` layer registered with a credential in its URL
userinfo is written with the userinfo in `layer_configs.repo_userinfo` and the
rest of the URL in `layer_configs.repo`, that a row holding the whole URL in
`repo` is read, reported, and cloned as before and is left as it is by a
restart, and that the next registration of that layer writes the two-part form.

**Covers.** The §7.3.1 repository credentials rule for the SQLite store and
the §13.4 rule that a change of stored form rewrites no row at start, through
the compiled binary, the `podium layer` commands, and the store file.

**Why by hand.** An operator reads the raw `layer_configs` row with `sqlite3`,
as a SQL report or a support dump does. The string `s90-token` in the `repo`
column after step 2 or step 5 is the defect this scenario catches. An empty
`repo_userinfo` beside a `repo` with no credential after step 2 is the
opposite defect: the credential was lost. A row that changes across the
restart in step 4 is a startup rewrite, which the release does not run.

**Steps.**

1. Run the isolation block. Start a stub remote that answers every request
   with 500 and logs the `Authorization` header, then serve a standalone
   registry.

   ```bash
   cat > "$WORK/remote.py" <<'EOF'
   import http.server, sys
   class H(http.server.BaseHTTPRequestHandler):
       def do_GET(self):
           open(sys.argv[1], "a").write((self.headers.get("Authorization") or "none") + "\n")
           self.send_response(500); self.end_headers()
       def log_message(self, *a): pass
   http.server.HTTPServer(("127.0.0.1", 8192), H).serve_forever()
   EOF
   python3 "$WORK/remote.py" "$WORK/remote.log" &
   REMOTE=$!
   podium serve --standalone --no-embeddings --bind 127.0.0.1:8191 > "$WORK/srv.log" 2>&1 &
   SRV=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8191/healthz
   server_alive "$SRV" "$WORK/srv.log"
   export PODIUM_REGISTRY=http://127.0.0.1:8191
   ```

   **Expect.** `server_alive` reports the server running.

2. Register a layer whose remote carries a credential, and read its row.

   ```bash
   podium layer register --registry "$PODIUM_REGISTRY" --id private --public \
     --repo "http://x-access-token:s90-token@127.0.0.1:8192/acme/private.git" --ref main > "$WORK/register.out"
   sqlite3 "$PODIUM_SQLITE_PATH" \
     "select repo, repo_userinfo, repo_userinfo is null from layer_configs where id = 'private';"
   ```

   **Expect.** The registration succeeds. The query prints
   `http://127.0.0.1:8192/acme/private.git|x-access-token:s90-token|0`. A
   `repo` that contains `s90-token` is a defect, and so is a last column of
   `1`.

3. Put the row in the form an earlier release wrote, then list and reingest
   the layer.

   ```bash
   sqlite3 "$PODIUM_SQLITE_PATH" \
     "update layer_configs set repo = 'http://x-access-token:s90-token@127.0.0.1:8192/acme/private.git', repo_userinfo = null where id = 'private';"
   podium layer list --registry "$PODIUM_REGISTRY" | tee "$WORK/list.out"
   grep -c "s90-token\|x-access-token" "$WORK/list.out"
   podium layer reingest --registry "$PODIUM_REGISTRY" private 2>&1 | tee "$WORK/reingest.out"
   grep -c "s90-token\|x-access-token" "$WORK/reingest.out"
   cat "$WORK/remote.log"
   ```

   **Expect.** The `private` layer's listed `repo` is
   `http://127.0.0.1:8192/acme/private.git`, and both counts are `0`. The
   reingest reports `ingest.source_unreachable` with status `500`.
   `remote.log` holds at least one line beginning `Basic `, which shows the
   clone sent the credential from the earlier-form row. A line reading `none`
   on every request means the row was read without its credential.

4. Restart the server on the same store and read the row again.

   ```bash
   kill "$SRV"; wait "$SRV" 2>/dev/null
   podium serve --standalone --no-embeddings --bind 127.0.0.1:8191 > "$WORK/srv2.log" 2>&1 &
   SRV=$!
   curl -s --retry 40 --retry-delay 1 --retry-all-errors -o /dev/null http://127.0.0.1:8191/healthz
   server_alive "$SRV" "$WORK/srv2.log"
   sqlite3 "$PODIUM_SQLITE_PATH" \
     "select repo, repo_userinfo is null from layer_configs where id = 'private';"
   ```

   **Expect.** The query prints
   `http://x-access-token:s90-token@127.0.0.1:8192/acme/private.git|1`. The
   start rewrote nothing, and the failed reingest of step 3 wrote nothing. A
   row in the two-part form here means a start or a failed ingest converted
   it.

5. Re-register the layer with its full remote and read the row.

   ```bash
   podium layer register --registry "$PODIUM_REGISTRY" --id private --public \
     --repo "http://x-access-token:s90-token@127.0.0.1:8192/acme/private.git" --ref main > "$WORK/reregister.out"
   sqlite3 "$PODIUM_SQLITE_PATH" \
     "select repo, repo_userinfo from layer_configs where id = 'private';"
   ```

   **Expect.** The registration succeeds, and the query prints
   `http://127.0.0.1:8192/acme/private.git|x-access-token:s90-token`.

6. Register the listed remote with the override flag and read the row.

   ```bash
   podium layer register --registry "$PODIUM_REGISTRY" --id private --public --force-repo-overwrite \
     --repo "http://127.0.0.1:8192/acme/private.git" --ref main > "$WORK/force.out"
   sqlite3 "$PODIUM_SQLITE_PATH" \
     "select repo, repo_userinfo is null from layer_configs where id = 'private';"
   grep -c "s90-token" "$WORK/srv.log" "$WORK/srv2.log"
   ```

   **Expect.** The query prints `http://127.0.0.1:8192/acme/private.git|1`,
   so the credential is gone from both columns. Both log counts are `0`.

**Cleanup.** Stop the stub with `kill "$REMOTE"`, stop the server with
`kill "$SRV"`, and `rm -rf "$WORK"`.

---
````

**Regression check on the manual suite.** Scenario S89 is re-run unmodified. Its checks are the `Basic ` line in the remote's log and the absence of `s89-token`, and both are unaffected.

## Resolved decisions

The reviewer resolved the open questions of the converged draft on 2026-10-10.

- **RD-1. Conversion scope (D-8).** Every `PutLayerConfig` writes the new format. Existing rows are untouched by the upgrade and convert at their next ordinary write: a completed ingest, an update that changes the layer, a reorder, a re-registration, each start for a declared git layer, or `admin migrate`. An active existing layer therefore converts at its next ingest without a re-registration. The registration-only rule was rejected, because it costs a signal through the `RegistryStore` data contract or a read-before-write in each backend, and it leaves existing credentials inside `repo` indefinitely.
- **RD-2. Release vehicle (D-16).** The next MINOR, which is 0.6.0 when step 1 ships as 0.5.3. The release ends the ability to run a 0.5.2 binary against the database and forbids a rolling upgrade directly from 0.5.2.
- **RD-3. Spec scope (D-15).** SPEC-2 lands. §13.4 gains the paragraph for a change of stored form with a version floor.
- **RD-4. Version literals in the docs.** The implementation waits for the step-1 tag and substitutes the released version number, as D-5 requires. It does not merge with a placeholder.

## Documentation changes

### DOC-1. `CHANGELOG.md`

**Precondition.** The entry is added only after the step-1 tag exists, with the literal released version substituted for `<step-1>`. At that point `## [Unreleased]` has no `### Changed` heading, and the entry creates it. The entry never shares a release section with the step-1 entry at `CHANGELOG.md:11`, which is accurate for step 1 and is not edited.

Under `## [Unreleased]`, add a `### Changed` group with:

> - **A `git` layer's URL credential is stored apart from its `repo`** (§7.3.1, §13.4): for a `repo` URL that carries userinfo, the metadata store now writes the userinfo to `layer_configs.repo_userinfo` and the rest of the URL to `layer_configs.repo`. No row is rewritten at upgrade. A layer registered earlier keeps working and takes the new form at its next write, which is its next completed ingest, an update that changes the layer, a reorder, or a re-registration, or the next start for a layer declared in the registry configuration. Registration, every layer response, and the clone are unchanged. **The lowest version that runs against a database this release has written to is `<step-1>`.** Roll back to `<step-1>` and no further, and upgrade from 0.5.2 or earlier by stopping every replica first or by upgrading through `<step-1>`. Replicas on `<step-1>` and on this release run together during a rolling upgrade. The rollback floor section of `docs/deployment/operator-guide.md` gives the mixed-version rule and the effects of running 0.5.2 against a written database.

The citation is "(§7.3.1, §13.4)". The entry does not use the phrase "migration of the values stored under the schema".

**IMPLEMENTOR'S CHOICE:** whether the entry also carries the sentence "`podium admin migrate-to-standard` writes the new form to its target." — the entry must keep every sentence quoted above, and the added sentence must sit before the bolded floor sentence.

### DOC-2. `docs/deployment/operator-guide.md`, `docs/deployment/clustered.md`, and `docs/reference/cli.md`

**`docs/deployment/operator-guide.md`, "Upgrade procedure", step 3 ("Roll").** Append:

> A release whose changelog names a rollback floor permits mixed-version replicas at or above that floor. [Rollback floor for the layer credential columns](#rollback-floor-for-the-layer-credential-columns) describes such a release.

**The same page, the paragraph that begins "Roll back by reverting the binary."** Append:

> Where a changelog entry names a rollback floor, the reverted binary is at or above that version, including when the upgrade started from a version below it.

**The same page, a new subsection at the end of "Upgrade procedure", before the horizontal rule that precedes "Read-only mode".**

> ### Rollback floor for the layer credential columns
>
> From `<step-2>`, the SQLite and Postgres metadata stores write a `git` layer's URL credential apart from the rest of its remote. For a `repo` that is a URL with a scheme and userinfo, and that a layer read does not report as `[redacted]`, `layer_configs.repo_userinfo` receives the userinfo and `layer_configs.repo` receives the rest of the URL. Every other `repo`, which covers an scp-like remote, a filesystem path, and a value reported as `[redacted]`, is held whole in `repo`. A row written before `<step-2>` keeps the whole URL in `repo` until the registry next writes that layer: after a completed ingest, an update that changes the layer, a reorder, or a re-registration, or at the next start for a layer declared in the registry configuration. A soft delete, a restore, and a failed ingest leave the row as it is. `repo_userinfo` holds the credential unencrypted. A SQL export of `layer_configs` needs both columns to reproduce a layer's remote, and it carries a secret when it includes `repo_userinfo`.
>
> The upgrade rewrites no row, and it is a change of stored form that `<step-1>` already reads. The maintenance-window procedure of step 2 does not apply: the canary phase runs as usual, and the store needs no backup restore to roll back. The lowest version that runs against a database `<step-2>` has written to is `<step-1>`, for a rollback and for mixed-version replicas alike.
>
> Replicas on `<step-1>` and on `<step-2>` that share a Postgres store read and clone identically. A `<step-1>` replica writes the whole URL to `repo` on every layer write it makes, so a row can change form in either direction during the roll. A row holds its credential apart from its first write after the last `<step-1>` replica has stopped.
>
> An upgrade from 0.5.2 or earlier directly to `<step-2>` stops every replica before the first `<step-2>` replica starts, and its rollback target is `<step-1>`. On a Helm chart deployment, an upgrade from 0.5.2 or earlier directly to `<step-2>` passes neither `--rollback-on-failure` nor `--atomic`, because an automatic rollback reinstalls a binary below the floor. An upgrade through `<step-1>` has neither restriction.
>
> Running 0.5.2 against a database `<step-2>` has written to is unsupported. A 0.5.2 process does not read `repo_userinfo`. A layer registered through the API or the CLI whose row holds its credential apart is cloned without the credential, so a private remote fails with `ingest.source_unreachable`, and a registration that carries the layer's listed remote is not refused. A layer declared in the registry configuration is written from the configured URL at each start and clones as before. On SQLite, a 0.5.2 write of the layer (an update, a reorder, a re-registration, a start for a declared layer, or a completed ingest) clears `repo_userinfo`, and the layer then needs a registration with the full URL. On Postgres, a 0.5.2 write leaves the column as it is. If a re-registration under 0.5.2 replaced the layer's `repo` with a different URL that carries no userinfo, the column still holds the earlier credential, and `<step-1>` or later joins it to the new remote when it reads the row. Before that layer is ingested on `<step-1>` or later, re-register it with the full URL of the new remote, or with the userinfo-free URL and `--force-repo-overwrite` (`force_repo_overwrite: true`). A re-registration with the userinfo-free URL and no override is refused with `details.constraint: "redacted_repo"`. An update does not change `repo`, so a layer that was only updated under 0.5.2 needs no re-registration. A layer declared in the registry configuration needs none either, because each start on `<step-1>` or later writes both columns from the configured URL.

**`docs/deployment/clustered.md`, "Later upgrades".** After the sentence "A release whose changelog names a new stored-value migration follows this procedure again.", add:

> A release whose changelog names a rollback floor rolls with the default rolling update from a version at or above the floor. From a version below the floor, scale the Deployment to zero replicas before the upgrade, and pass neither `--rollback-on-failure` nor `--atomic`. [Rollback floor for the layer credential columns](operator-guide#rollback-floor-for-the-layer-credential-columns) states the floor for `<step-2>`.

**`docs/reference/cli.md`, `podium admin migrate-to-standard`.** After the sentence that begins "The command copies each row as the source stores it", add:

> A `git` layer's remote is the exception. The command reads and writes layers through the store, so the target holds each layer's URL credential in the form the release that runs the command writes ([Rollback floor for the layer credential columns](../deployment/operator-guide#rollback-floor-for-the-layer-credential-columns)).

The version literals are substituted only after the step-1 tag exists (D-5, RD-4). No `tools/doccov/manifest.yaml` entry is needed, because no runnable example is added.

### DOC-3. The layer pages

The sentences below describe client-facing behavior, which is unchanged. The edit changes the verb to match SPEC-1 and states the storage form once, by link.

- `docs/reference/http-api.md:418`: "The registry stores the value as given and clones with it." becomes "The registry keeps the value as given and clones with it." No sentence about the store is added.
- `docs/reference/cli.md:455`: "The registry stores it as given, and `podium layer list` reports the remote without it" becomes "The registry keeps it as given, and `podium layer list` reports the remote without it". No clause about the store is added.
- `docs/deployment/layers.md:115`: "The registry stores the URL as given and clones with it." becomes "The registry keeps the URL as given and clones with it. The [operator guide](operator-guide#rollback-floor-for-the-layer-credential-columns) states how the metadata store holds the credential."

The `force_repo_overwrite` sentences ("stores the `repo` as sent", "stores the remote as given, without the credential") and the phrases "stored remote" and "stored `repo`" stay unedited. They read under the SPEC-1 definition of the stored `repo`. Run `make coverage-gate` to confirm that the new anchor link resolves.

## Non-goals

- Encryption at rest of `repo_userinfo`, `repo`, `webhook_secret`, or any other stored secret (user decision).
- Any migration, backfill, startup rewrite, completion record, or operator command that converts existing `layer_configs` rows. Rows convert only through the writes the registry already makes.
- Refusing, warning on, or altering URL userinfo at registration, at update, or in `registry.yaml`, and any dedicated credential member, upload path, SSH key store, or secret-reference syntax.
- Splitting scp-like remotes, filesystem paths, the network-path form, or the fail-closed `[redacted]` classes. They stay whole in `repo` with a NULL `repo_userinfo`.
- Replacing `LayerConfig.RegisteredRepo` with a credential-only field, or any change to `CloneRepo`, `reportedRepo`, `layerConfigEqual`, the guard predicate, the `RegistryStore` method set, or the `LayerRepoCredential` conformance expectations (D-9).
- Passing the credential to go-git through `Auth` with a userinfo-free `CloneOptions.URL`, or any other change to `pkg/layer/source/git.go` or the `LayerSourceProvider` input (D-10). A separate proposal can take it up with the same-host redirect difference for a password-only URL.
- Changing the read rule `SplitLayerRepo` or the D-12 format of proposal 0053, including the stale-userinfo case after a 0.5.2 write on Postgres. That case is documented (D-18) and is not mitigated in code, because a mitigation would need a format `<step-1>` does not read.
- A caller signal on `PutLayerConfig`, or a store-side rule keyed on the existing row, to restrict conversion to registrations (the rejected alternative of D-8, RD-1).
- Giving the memory backend a userinfo slot or the two-part form (D-11).
- Removing or narrowing anything proposal 0052 built: `RedactRepo`, `wireLayer`, `RedactCloneError`, the boot-log redaction, the `[redacted]` class, and the re-registration guard with `force_repo_overwrite` and `--force-repo-overwrite`.
- Any §6.10 error code, `PODIUM_*` variable, `registry.yaml` key, HTTP member, CLI flag, SDK, Web UI, or schema change.
- Cutting the release, bumping `buildinfo.Version` or `Chart.yaml`, or editing the step-1 changelog entry. The user stated that no release is cut now.
- The pre-existing whole-row last-writer-wins window between a long ingest's stamp and a concurrent layer write, and the boot seed writing a declared layer without its `WebhookSecret`. Both columns are always written together, so the format change widens neither.

## Resolved in adversarial review

Review rounds populate this section. Each bullet records a finding and where its fix landed.

### Draft reconciliation (2026-10-10)

The draft's per-change challenge revisions were applied. Where they disagreed with the draft or with each other, the proposal resolves them as follows.

- **The transitional sentence of SPEC-1.** The SPEC-1 revision deletes the sentence about a layer held whole before the rule took effect. The SPEC-2 revision asks SPEC-1 to keep that sentence with a narrower trigger list, and its own paragraph says §7.3.1 states "when a layer held whole takes that form". The proposal follows the SPEC-1 revision, because it keeps release history out of §7.3.1 and makes SPEC-1 independent of SPEC-2. The last sentence of the SPEC-2 paragraph is adjusted to match: it says §7.3.1 states which values the SQLite and Postgres stores write in two parts and that a value held whole is read as held. The conversion triggers are listed in DOC-1 and DOC-2.
- **D-19 and the frozen step-1 writers.** The draft's D-19 replaced the 0.5.2 frozen writers with frozen copies of the step-1 writers. The TEST-1 revision adds none, because the step-1 statement differs only in binding NULL and its source tag does not exist yet. D-19 is restated to match, and TEST-1(c) sets the earlier form with a raw `UPDATE`.
- **The §13.4 test citations.** The SPEC-2 revision assigns `// Spec: §13.4` to TEST-1(d) and (e) of the draft, which the TEST-1 revision removed. The citation goes on the two `...LayerRepoEarlierFormRow` tests of TEST-1(c). The no-rewrite-at-open assertion stays in the unmodified `TestSQLite_AdditiveMigration_SplitsLegacyLayerRepo`.
- **`pkg/store/schema_migrate_test.go` and `pkg/store/storetest/storetest.go` leave the TEST-1 targets.** Both stay unmodified. The `empty-userinfo` conformance row already pins D-6 on the SQL backends.
- **The TEST-2 rationale.** The draft called the step-1 handler and orchestrator suites the regression evidence. They run on the memory store, so the proposal names the SQLite-backed suites instead and adds raw-row reads at the handler, the stamp, `admin migrate`, and the compiled binary.
- **The DOC-2 subsection heading.** The DOC-3 revision links to the anchor `rollback-floor-for-the-layer-credential-columns`, which fixes the heading. DOC-1 refers to the same subsection by a backticked path. The conditional description of which values are held in two parts is carried in that subsection alone.
- **The DOC-2 text.** The draft carried the DOC-2 corrections and not the full sketch they correct. The subsection, the step 3 sentence, and the `clustered.md` and `cli.md` sentences are written here from D-17 and D-18, with the four corrections applied as given.
- **Checklist bundling.** CODE-1, CODE-2, and TEST-1 share step S3. The rename does not compile without its callers, and the existing raw-column assertions fail from the commit that changes the write.
- **Manual validation.** The draft staged no manual scenario. The change alters what an operator reads in the store file and adds a rollback floor, so scenario S90 is staged as TEST-3.
- **The placement of the SPEC-2 paragraph.** The SPEC-2 revision anchors the paragraph directly after the "additive guarantee" paragraph and opens it with "The rewrite rule above". There it would precede the sentence "The §4.7.6 content hash is such a value.", whose "such a value" would then point at the new class. The proposal inserts the paragraph before the section's last paragraph and opens it with "The rewrite rule this section states for a release that changes how a stored value is computed". The rest of the revised text is unchanged apart from the last sentence, as the first bullet records.

### Pass 1 (2026-10-10, automated)

- **The stale-userinfo trigger on Postgres named an update.** The DOC-2 subsection said a 0.5.2 process could replace a layer's `repo` "through a re-registration or an update". The update handler never assigns `repo`, at HEAD (`pkg/registry/server/layers.go:1252-1254`) or at the tag (`v0.5.2:pkg/registry/server/layers.go:1238-1239`), and §7.3.1 states that an update does not change `repo` (`spec/07-external-integration.md:121`). The DOC-2 paragraph, D-18, and the matching row of the edge-case table now name a re-registration as the trigger. The other 0.5.2 writer that replaces `repo` is the declared-layer boot seed (`v0.5.2:internal/serverboot/serverboot.go:746`). The same three places now state that such a layer needs no re-registration, because each start on `<step-1>` or later writes both columns from the configured URL before the server serves any request (`internal/serverboot/serverboot.go:1085` and `:645` for the seed, `:1827` for the serve call, and `pkg/store/postgres.go:1317` for the upsert). This wording was chosen over naming the declared layer as a second trigger, because the row never reaches an ingest on `<step-1>` or later in the stale state.
- **The boot-seed ordering named the listener bind.** The first version of the bullet above, and D-18, said the declared-layer seed runs "before the listener binds" and cited the put alone (`internal/serverboot/serverboot.go:645`). `run` opens the listener at `internal/serverboot/serverboot.go:852` and calls `bootstrapDeclaredLayers` afterwards, at `:1085`. The guarantee the argument needs is that no request is served before the seed, and it holds because `serveUntilShutdown` is called at `:1827` and holds the only `srv.Serve` call (`:1839`). Both places now say "before the server serves any request" and cite the seed call and the serve call. The DOC-2 text and the edge-case row never carried the phrase and are unchanged.
