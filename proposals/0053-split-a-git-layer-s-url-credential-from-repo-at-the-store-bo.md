# Proposal 0053: Split a git layer's URL credential from `repo` at the store boundary, with the stored value unchanged (step 1 of 2)

- Issue: (to be filed)
- Status: Implemented (2026-10-09). Signed off as staged, with the open questions settled as recorded in "Resolved decisions" (RD-1 through RD-3). Verified on 2026-10-09 after 4 adversarial review rounds (6 findings fixed).
- Date: 2026-10-09

This document stages the proposed code, test, and documentation changes. It does not modify any spec, code, or doc file. Apply the changes in the staged sections after sign-off. The proposal stages no spec edit (D-14).

## Summary

**What changes.**

- A new go-git-free package, `internal/repourl`, holds the single classifier for a layer `repo`: the redaction that `source.RedactRepo` performs today, the fail-closed test, and a byte-exact split and join of URL userinfo. `pkg/layer/source/redact.go` delegates to it and keeps its exported names, signatures, and outputs (CODE-1).
- `store.LayerConfig` gains the field `RegisteredRepo`, tagged `json:"-"`, of the new named string type `store.RegisteredRepo`, and the method `CloneRepo()`. `pkg/store/layer_repo.go` (new) holds the read rule `SplitLayerRepo`, the write rule `LayerRepoColumn`, and the sentinel `ErrRepoCredentialMismatch`. `layerConfigEqual` in `pkg/registry/server/layers.go` names the new field (CODE-2).
- The ingest orchestrator passes `cfg.CloneRepo()` to the source provider in place of `cfg.Repo`, and the stored-layer callers of the local-source gate `authorizeLocalSource` (restore, reingest, and the inbound webhook) pass the same value, so the gate classifies the string the clone receives (CODE-4).
- A new unexported helper, `reportedRepo`, in `pkg/registry/server/layers.go` returns `source.RedactRepo(cfg.CloneRepo())`. `wireLayer` and the re-registration guard predicate `redactedRepoResubmitted` both read it, and the predicate is restated so that it holds for a layer the store has split (CODE-5).
- The memory, SQLite, and Postgres stores apply the read rule on every layer read and the write rule in `PutLayerConfig`. The `layer_configs` table gains the nullable column `repo_userinfo`, which this release reads and never fills (CODE-3).
- Unit, conformance, handler, orchestrator, CLI, and end-to-end tests follow, and the changelog gains one entry (TEST-1 through TEST-4, DOC-1).

**Fixed decisions.**

- This is step 1 of a two-release plan. The `repo` column keeps the registered bytes, userinfo included. No row is migrated or rewritten. A 0.5.2 binary runs against a database this release has written to. This is a user decision and is closed (D-1).
- Encryption at rest is out of scope. This is a user decision and is closed (D-2).
- Nothing proposal 0052 built is removed or weakened. Every response, log line, clone error, and refusal carries the same bytes as 0.5.2 (D-3).
- The split class is a `repo` that parses as a URL with a non-empty scheme and with userinfo, and that is outside the fail-closed classes. Every other value stays in `Repo` unsplit (D-4).
- For a split layer, the in-memory `Repo` equals `source.RedactRepo(registered)` byte for byte (D-5).
- The field is `LayerConfig.RegisteredRepo` and holds the whole registered URL. The type is a named string whose `String` and `GoString` methods return `[redacted]`. The names `RegisteredRepo`, `CloneRepo`, `SplitLayerRepo`, `LayerRepoColumn`, and `ErrRepoCredentialMismatch` are fixed (D-6, D-8).
- The write rule runs inside `PutLayerConfig` on every backend. A config whose `RegisteredRepo` is outside the split class or does not redact to its `Repo` returns `ErrRepoCredentialMismatch` and writes nothing. The read split and the mismatch error are part of the `RegistryStore` conformance contract (D-8).
- The classifier lives in `internal/repourl`. `pkg/store` does not import `pkg/layer/source` (D-7).
- `pkg/layer/source/git.go` is unchanged. go-git receives the same URL bytes as in 0.5.2. `source.LayerConfig` gains no field (D-9).
- The reported value of any `LayerConfig` is `source.RedactRepo(cfg.CloneRepo())`. The guard fires when the stored layer has a `RegisteredRepo` or its reported value differs from its `Repo`, and the request's `repo` equals the reported value (D-5, D-10).
- The local-source gate classifies `cfg.CloneRepo()` on a stored layer and `req.Repo` at registration. `authorizeLocalSource`, `namesHostPath`, and `isFileTransportRepo` are not edited (D-16).
- `repo_userinfo` is bound to NULL on every `PutLayerConfig`. This release never writes a credential to it (D-11). The column is staged in this step (RD-2).
- A non-NULL `repo_userinfo` is the raw registered userinfo substring, and `repo` then holds the registered bytes without it. The read ignores the column when `repo` is already in the split class or the recombined value does not split back into the same two parts (D-12).
- Once step 2 writes `repo_userinfo`, the lowest binary that can run against the database is this release (D-13).
- No spec text, §6.10 error code, `PODIUM_*` variable, `registry.yaml` key, HTTP member, CLI flag, SDK surface, or Web UI source changes (D-14).
- The release vehicle is PATCH 0.5.3 (D-15, RD-1).

**Watch out for.**

- **The orchestrator edit lands before the store split.** Once a store read returns a userinfo-free `Repo`, a clone that reads `cfg.Repo` sends no credential. `CloneRepo()` returns `Repo` while `RegisteredRepo` is empty, so CODE-4 is safe to land first, and the checklist orders it that way.
- **`RedactRepo` is not idempotent.** `source.RedactRepo("https://tok@host/a%40b c.git")` is `https://host/a@b%20c.git`, and `RedactRepo` of that result is `[redacted]`. A store read already returns the redacted form in `Repo` for a split layer, so no site calls `RedactRepo` on a store-read `Repo`. A response and the guard compute the reported value with `reportedRepo(cfg)`, which redacts the registered bytes once (CODE-5).
- **The local-source gate reads `cfg.CloneRepo()` on a stored layer.** Redaction can change the transport go-git resolves: `file://tok@?/srv/x` is a file-transport URL and its reported form `file:?/srv/x` reads as scp-like ssh, while `https://tok@` is an https URL and its reported form `https:` reads as a file path. A gate that classified the reported `Repo` would admit the first and refuse the second. The three stored-layer call sites change in CODE-4, in the same step as the orchestrator line.
- **A fresh `LayerConfig` still carries userinfo in `Repo`.** Registration (`pkg/registry/server/layers.go:1461-1472`) and the declared-layer boot seed (`internal/serverboot/serverboot.go:635-645`, `:680-681`) build a struct and keep using it after `PutLayerConfig`. `wireLayer` and the boot-log `RedactRepo` call remain required on those paths. Do not remove either on the ground that the store now splits.
- **`source.RedactRepo` is moved, and its body is unchanged.** Do not re-derive it from `Split`. The value `//tok@host/x://y` matches the scheme pattern, parses with an empty scheme and with userinfo, and is outside the split class. A `Redact` written as "unsafe, then split, then the input" returns that value with its credential, where 0.5.2 reports `//host/x://y`.
- **The Postgres upsert leaves an unnamed column untouched.** `repo_userinfo = EXCLUDED.repo_userinfo` must appear in the `DO UPDATE SET` list. Without it, a later step-2 row that is rewritten with a credential-free `repo` would be recombined with a stale credential. The SQLite tests cannot detect the omission, because `INSERT OR REPLACE` resets the column either way. The Postgres case of TEST-2(e) is the test that fails without the assignment.
- **`TestWakesWatchers_EveryField` enumerates `store.LayerConfig` by reflection.** It fails the moment the field exists, until the table names the field and `layerConfigEqual` compares it. Both edits land with the field, in step S2.
- **`pkg/store/storetest` must not import `pkg/layer/source`.** The conformance package has no go-git package in its dependency closure. Write expected values as string literals.
- **`pkg/store/postgres_test.go` is package `store_test`.** It cannot reach the per-organization schema scoping in `p.org`. Raw-column tests for Postgres go in an internal-package file.
- **An assertion that a clone's request URL carries no userinfo proves nothing.** An HTTP request line never carries userinfo, so a server-side check of `r.URL.User` passes on 0.5.2. The end-to-end test of TEST-4(d) asserts the pair the remote records from `r.BasicAuth()`.
- **Existing 0052 tests assert the stored `Repo` equals the full credential URL.** They fail once the store splits. TEST-3 lists each site, and it lands in the same step as CODE-3.
- **An existing additive-migration fixture has no `repo` column.** `TestSQLite_AdditiveMigration_BackfillsLayerConfigColumns` builds a four-column legacy table and cannot show a stored credential being split. TEST-2 adds a fixture with the 0.5.2 column set.
- **A conformance case in `storetest` lowers `codecov/patch`.** Assertion branches in the suite count as uncovered lines. Use `must()` and one combined assertion per step.
- **Parts of `test/e2e` skip silently on macOS.** Run the new end-to-end test with `-v` and confirm it does not report `SKIP`.

## Implementation checklist

- [x] **S1 · code** — CODE-1. `internal/repourl` with `Redact`, `Unsafe`, `Split`, and `Join`, and `pkg/layer/source/redact.go` delegating to it.
      Levels: unit. Depends on: —
- [x] **S2 · code** — CODE-2. `store.RegisteredRepo`, `LayerConfig.RegisteredRepo`, `CloneRepo()`, `SplitLayerRepo`, `LayerRepoColumn`, and `ErrRepoCredentialMismatch`, with the `layerConfigEqual` line and the `TestWakesWatchers_EveryField` row.
      Levels: unit. Depends on: S1
- [x] **S3 · code** — CODE-4. `pkg/registry/ingest/orchestrator.go` passes `cfg.CloneRepo()` to the provider, and the restore, reingest, and inbound-webhook handlers pass `cfg.CloneRepo()` to `authorizeLocalSource`.
      Levels: unit, integration. Depends on: S2
- [x] **S4 · code** — CODE-5. `reportedRepo`, with `wireLayer` and `redactedRepoResubmitted` reading it, so that the reported value and the guard hold for a split layer.
      Levels: unit, integration. Depends on: S2
- [x] **S5 · code** — CODE-3, TEST-3. The memory, SQLite, and Postgres backends split on read and recompose on write, `layer_configs` gains `repo_userinfo`, and the existing stored-value assertions follow. The two are bundled because the listed assertions fail from the commit that makes the store split.
      Levels: unit, conformance, integration, e2e. Depends on: S3, S4
- [x] **S6 · test** — TEST-1. Unit tests for `internal/repourl` and the added `TestRedactRepo` row.
      Levels: unit. Depends on: S1
- [x] **S7 · test** — TEST-2. The `LayerRepoCredential` conformance case, the raw-column tests, the 0.5.2 rollback test, the migration test, the forward-read rows with their SQLite and Postgres raw-SQL tests, the print and marshal test, and the write-rule rows.
      Levels: unit, conformance, integration. Depends on: S5
- [x] **S8 · test** — TEST-4. New handler subtests, the `reportedRepo` unit rows, the local-source gate rows, the orchestrator test, the `podium admin migrate` case, and the end-to-end restart test.
      Levels: unit, integration, e2e. Depends on: S5
- [x] **S9 · docs** — DOC-1. The changelog entry.
      Levels: —. Depends on: S5

**Ordering constraints.** S3 and S4 precede S5: each is behavior-neutral while no store sets `RegisteredRepo`, and each is required from the commit that sets it. S6 is independent of S2 through S5. Land S1 through S8 in one pull request, so the 85% coverage bar on the new lines is met when the pull request is measured.

## Current state and the gap

### One field carries the credential and the reportable URL

At v0.5.2 a git layer's URL credential and its reportable URL share `store.LayerConfig.Repo` (`pkg/store/store.go:282`, tagged `json:"repo"`). Every backend returns it verbatim. The memory store keeps the struct as given (`pkg/store/memory.go:464-481`). SQLite and Postgres bind `cfg.Repo` to the `repo` column and scan it back (`pkg/store/sqlite.go:842-852`, `:1000-1011`, `pkg/store/postgres.go:1283-1313`).

The §7.3.1 "Repository credentials" protection is applied after the read, at these call sites:

- `wireLayer` (`pkg/registry/server/layers.go:348-351`) for every layer response.
- The declared-layer boot log (`internal/serverboot/serverboot.go:680-681`).
- `RedactCloneError` (`pkg/layer/source/git.go:60`).
- The guard predicate `redactedRepoResubmitted` (`pkg/registry/server/layers.go:755-758`).

A new code path that marshals, logs, or wraps a `LayerConfig` discloses the credential unless its author adds one of these calls. This proposal moves the credential out of `Repo` at the point where the store hands a layer to the rest of the process.

### Facts the design absorbs

1. **The reported value is a canonical rendering.** `RedactRepo` returns `u.String()` (`pkg/layer/source/redact.go:49-50`), which lowercases the scheme, re-escapes userinfo and path, and drops an empty fragment. 0.5.2 reports that form (`pkg/layer/source/redact_test.go:50`), and the guard compares against it byte for byte. The in-memory `Repo` must equal `RedactRepo(stored)`, and the registered string cannot be rebuilt from that value plus parsed userinfo. Every `PutLayerConfig` rewrites the whole row, so the store carries the registered bytes through each read-modify-write.
2. **Empty userinfo counts.** `u.User != nil` holds for `https://@host/x.git` and `https://:@host/x.git`. 0.5.2 strips these and the guard fires on them, so the split needs a presence signal that does not depend on a non-empty credential.
3. **The clone input is filled field by field.** The clone receives `source.LayerConfig` (`pkg/layer/source/source.go:58-75`), built at `pkg/registry/ingest/orchestrator.go:107-114`, so the registered URL crosses that seam explicitly.
4. **Fresh-struct writers pass an unsplit `Repo`.** These are registration (`pkg/registry/server/layers.go:1461-1472`), the declared-layer boot seed that rewrites each `registry.yaml` git layer on every start (`internal/serverboot/serverboot.go:635-645`), and the bootstrap local-layer seed (`internal/serverboot/serverboot.go:529-544`). `PutLayerConfig` accepts an unsplit `Repo` on every backend.
5. **A network-path value sits in the userinfo arm.** The scheme pattern `^[^:]+://` matches `//tok@host/x://y`. `url.Parse` reads it with an empty scheme, userinfo `tok`, and host `host`, and 0.5.2 reports it as `//host/x://y`. The text after its first `://` is `y`, so a byte rule that looks for the authority there finds no userinfo.
6. **`RedactRepo` is not idempotent.** `u.String()` writes the path from its decoded form whenever the registered escaping is not the canonical one, so a percent-encoded `@` (`%40`) in the path or fragment, beside a byte such as a space that makes the escaping non-canonical, is written as a literal `@`. The registered value has no literal `@` after its authority, so it is outside the fail-closed class and 0.5.2 reports the stripped URL. The stripped URL is an `https` value with `@` after its host, which is the fail-closed class (`pkg/layer/source/redact.go:97-100`). Measured with a copy of `RedactRepo`: `https://ghp_tok3n@git.acme.com/acme/a%40b c.git` gives `https://git.acme.com/acme/a@b%20c.git`, and that value gives `[redacted]`. 0.5.2 redacts the registered bytes once per response. A site that redacts a store-read `Repo` after the split would redact twice.
7. **Redaction can change the transport go-git resolves.** `u.String()` writes `//` only when the host, the path, or the userinfo is non-empty, so a hostless, pathless URL loses its `//` with its userinfo. `isFileTransportRepo` asks `transport.NewEndpoint`, on the documented ground that it is "the same disambiguation Git.Snapshot's clone reaches" (`pkg/registry/server/layer_capabilities.go:62-64`, `:75-85`). Measured with go-git v5.19.2: `file://tok@?/srv/x` resolves to protocol `file`, and its redaction `file:?/srv/x` resolves to `ssh` with host `file`. `https://tok@` resolves to `https`, and its redaction `https:` resolves to `file`. In 0.5.2 the gate and the clone read the same string. The stored-layer gate sites are restore and reingest (`pkg/registry/server/layers.go:1687`, `:1898`) and the inbound webhook (`pkg/registry/server/webhook_ingest.go:81`).

### Paths that need no edit

The read-modify-write paths carry the whole struct in process and never through JSON, so a `json:"-"` field survives them: update, reorder (`pkg/registry/server/layers.go:1798`), the post-ingest stamp (`pkg/registry/ingest/orchestrator.go:200`), and `podium admin migrate` (`cmd/podium/admin_migrate.go:320`, `:378`). No Web UI, SDK, CLI, or HTTP member depends on userinfo in `repo`. The client references are `web/ui/src/components/SourceCell.tsx:45` and `:175`, which display the reported value, and `cmd/podium/layer.go:282`, which sends `force_repo_overwrite`. The stored-layer callers of the local-source gate are outside this set, because they classify the string and change in CODE-4 (fact 7).

## Decisions

- **D-1. Two-release plan (user decision, closed).** This proposal is step 1. The `repo` column keeps the full URL exactly as registered. There is no migration and no rewrite of existing rows. A 0.5.2 binary pointed at a database this release has written to behaves as it does today.
- **D-2. No encryption (user decision, closed).** Encryption at rest is out of scope and is not proposed.
- **D-3. The 0052 mechanisms stay.** Registration and update never refuse, warn on, or alter URL userinfo. `RedactRepo`, `wireLayer`, `RedactCloneError`, the `[redacted]` class, the boot-log redaction, `details.constraint: "redacted_repo"`, `force_repo_overwrite`, and `--force-repo-overwrite` all stay, and every response reports the same bytes as 0.5.2. That holds because each response redacts the registered bytes exactly once, as 0.5.2 does (D-5, CODE-5). The local-source refusals are unchanged because the gate classifies the registered bytes on every path (D-16).
- **D-4. The split class.** A value is split when it takes the arm of `parseRepo` (`pkg/layer/source/redact.go:86-102`) that returns a parsed URL, that URL has `u.User != nil` and a non-empty parsed scheme, and the registered bytes begin with that scheme, compared without regard to case, followed by `://`. Scheme-less values (scp-like remotes and paths), the network-path form of fact 5, and the fail-closed classes stay in `Repo` unsplit, covered by the 0052 mechanisms.
- **D-5. The in-memory `Repo` of a split layer is `RedactRepo(registered)`.** It is the canonical `u.User = nil; u.String()` rendering. Removing the userinfo by byte surgery is rejected for this value: it would report `HTTPS://host/a.git` where 0.5.2 reports `https://host/a.git`, and it would change the value the guard matches. The equality with the 0.5.2 reported value holds by construction, because the store computes `Repo` from the registered bytes. It does not survive a second `RedactRepo` call (fact 6), so the reported value of any `LayerConfig` is `source.RedactRepo(cfg.CloneRepo())`, computed by `reportedRepo` (CODE-5), and no site applies `RedactRepo` to a store-read `Repo`.
- **D-6. The field holds the registered URL.** `LayerConfig.RegisteredRepo` is tagged `json:"-"` and has the named string type `store.RegisteredRepo`.
  - Holding the registered string, in place of parsed userinfo, makes the write byte-exact for a non-canonical URL. A non-empty value is the presence signal for empty userinfo.
  - The type's `String` and `GoString` methods return `[redacted]` for a non-empty value. A `LayerConfig` returned by a store read, held directly or under exported fields, therefore prints no credential under `%v`, `%+v`, `%#v`, `%s`, and `%q`. A `LayerConfig` under an unexported struct field, and any numeric verb, prints the raw value, because `fmt` cannot call the methods there. That nesting exists at `cmd/podium/admin_migrate.go:277`, and no current `Printf` prints it.
  - A named string is chosen over a struct with an unexported member. `RegistryStore` is a §9.1 SPI, and §9.3 requires SPI inputs and outputs to be serializable with no opaque in-process state. A custom store, and any test, builds the value by conversion.
- **D-7. One classifier, in `internal/repourl`.** `pkg/store` has no go-git package in its dependency closure and `pkg/layer/source` has many, so `pkg/store` importing `pkg/layer/source` is rejected. The reverse import would pull the SQL drivers into the source package. A package under `pkg/` would become module public API with no exported signature that needs it, so the package is internal. `source.RedactRepo`, `source.RedactedRepo`, and `source.RedactCloneError` keep their names, signatures, and outputs, and no caller changes.
- **D-8. The write rule lives inside `PutLayerConfig` on every backend.**
  - A config with an empty `RegisteredRepo` persists `Repo` verbatim. This covers the fresh-struct writers and every unsplit value.
  - A config whose `RegisteredRepo` is in the split class and redacts to its `Repo` persists the registered URL.
  - Any other combination returns `ErrRepoCredentialMismatch` and writes nothing, because the store cannot tell which half the caller meant. No current caller produces that combination: the only non-test assignments to a stored config's `Repo` are the `wireLayer` copy and the two fresh structs, update never assigns `Repo`, and restore makes no put.
  - The rule functions are exported so that a custom `RegistryStore` can apply them. The conformance case in TEST-2 makes the read split and the mismatch error part of the `RegistryStore` conformance contract for every backend that runs the suite (`pkg/store/storetest/storetest.go:1-8`).
- **D-9. The provider input is unchanged from 0.5.2.** The orchestrator passes the full registered URL in `source.LayerConfig.Repo`. `Git.Snapshot` keeps `URL: cfg.Repo` and `RedactCloneError(cfg.Repo, err)`, so go-git receives the same bytes and derives the same `Authorization` header as today. No field is added to the provider input, and a compiled-in custom provider sees the same input as before. Residual: the full URL exists in the transient `source.LayerConfig` value for the duration of the `Snapshot` call. That struct has no JSON tags and is not marshalled or logged anywhere under `pkg/`, `cmd/`, or `internal/`.
- **D-10. The guard predicate.** The stored layer holds a credential, and the request's `repo` equals the reported value. The reported value is `reportedRepo(stored)`, which is `source.RedactRepo(stored.CloneRepo())`. A stored layer holds a credential when `RegisteredRepo` is present or when the reported value differs from `stored.Repo`. For a layer with an empty `RegisteredRepo`, `CloneRepo()` is `Repo`, so the second disjunct is the 0.5.2 predicate. It covers the `[redacted]` class, the network-path form, and any store that returns an unsplit strippable `Repo`. External behavior is identical, including for empty userinfo.
- **D-11. The forward read is staged as one nullable column, `layer_configs.repo_userinfo`.**
  - Adding it is safe for a 0.5.2 binary. 0.5.2 names its columns explicitly in every INSERT and SELECT (`pkg/store/sqlite.go:843-852`, `:859-864`, `pkg/store/postgres.go:1283-1313`, `:1325-1330`), the column is nullable, and the 0.5.2 additive migration never drops a column (`pkg/store/schema_migrate.go`).
  - This release reads the column when it is non-NULL and binds NULL to it on every `PutLayerConfig`.
  - Clearing on write is required on Postgres, where the upsert otherwise leaves the column untouched. On SQLite `INSERT OR REPLACE` resets it regardless.
- **D-12. The column format this release commits to reading.** A NULL `repo_userinfo` means no separately stored credential. A non-NULL value, the empty string included, is the raw registered userinfo substring, and `repo` then holds the registered bytes with that substring and its `@` removed. Reinsertion is string concatenation after the first `://`, which is byte-exact. When `repo` itself is in the split class, or the recombined value is outside it, or splitting the recombined value does not return the same two parts, the column is ignored and the row reads as its `repo` value alone. That direction presents no unexpected credential.
- **D-13. Rollback floor.** Once step 2 writes `repo_userinfo`, the lowest binary that can run against the database is this release. A 0.5.2 binary never reads the column and would clone without the credential. On SQLite it would also erase the column at the next layer write, and on Postgres it would leave it stale against a `repo` it rewrote.
- **D-14. No other surface.** The proposal stages no spec edit. §7.3.1 states that a registration "stores it unchanged, and the ingest clones with the stored value", and both halves stay literally true: the `repo` column holds the registered bytes, and `CloneRepo()` hands those bytes to the provider. No §6.10 error code, `PODIUM_*` variable, `registry.yaml` key, SPI method, `LayerSourceProvider` input, HTTP member, CLI flag, SDK surface, or Web UI source changes. At the spec level the change is a refactor, and the existing `// Spec: §7.3.1` citations and tests continue to hold.
- **D-15. Release vehicle.** The release is PATCH 0.5.3 (RD-1).
- **D-16. The local-source gate classifies the string the clone receives.** Restore, reingest, and the inbound webhook pass `cfg.CloneRepo()` to `authorizeLocalSource`, which is the value the orchestrator hands the provider for the same layer. Registration keeps passing `req.Repo`, which is the unsplit request value (`pkg/registry/server/layers.go:1433`). The gate therefore reads the same bytes as in 0.5.2 on every path, and the §7.3.1 local-source rule is neither widened nor narrowed. Classifying the reported `Repo` is rejected because it disagrees with go-git in both directions (fact 7): it admits a non-admin restore, reingest, or webhook delivery for a file-transport layer, and it refuses the non-admin owner of a hostless network URL. `authorizeLocalSource`, `namesHostPath`, and `isFileTransportRepo` are not edited.

## Proposed solution

### CODE-1. `internal/repourl` (new) and `pkg/layer/source/redact.go:41-102`

The package imports `net/url`, `regexp`, and `strings`. It is the one place that classifies a layer `repo`.

```go
// Package repourl classifies a layer's repo value: which part of it is a URL
// credential, and whether any part of it may be reported. The store and the
// git source share it so the class the store splits is the class a response
// strips.
//
// Spec: §7.3.1 (Repository credentials)
package repourl

// Redacted is what a response reports for a repo whose credential-bearing
// part cannot be identified.
const Redacted = "[redacted]"

// Parts is a repo in the split class, taken apart.
type Parts struct {
	// Clean is the value a response reports: Redact of the registered value.
	Clean string
	// Bare is the registered bytes with the raw userinfo and its '@' removed.
	Bare string
	// RawUserinfo is the registered userinfo substring, unescaped and
	// possibly empty.
	RawUserinfo string
}

func Redact(repo string) string
func Unsafe(repo string) bool
func Split(repo string) (Parts, bool)
func Join(bare, rawUserinfo string) (string, bool)
```

- **Move without rewriting.** `schemeRE` and `parseRepo` move from `pkg/layer/source/redact.go` unchanged. `Redact` is the current `RedactRepo` body, byte for byte: an unsafe value returns `Redacted`, `u == nil || u.User == nil` returns the input, and any other value returns `u.String()` after `u.User = nil`. `Unsafe` is the current `repoUnsafeToEcho` body.
- **`pkg/layer/source/redact.go`.** `RedactedRepo` becomes `repourl.Redacted`. `RedactRepo` returns `repourl.Redact(repo)`. `RedactCloneError` calls `repourl.Unsafe`. `cloneErrorWithheld` and `urlUserinfoRE` stay in `pkg/layer/source`. Doc comments and `// Spec:` citations on the exported names stay.
- **`Split`.** It returns true only when all of these hold: `parseRepo` returns a URL and reports the value safe, `u.User != nil`, `u.Scheme != ""`, and the registered bytes begin with the scheme, compared without regard to case, followed by `://`. The authority is then the text after that `://` up to the first `/`, `?`, or `#`, and the raw userinfo is the authority up to its last `@`. `Bare` is the input with the raw userinfo and that `@` removed, and `Clean` is `Redact(repo)`. An authority with no `@` returns false. That branch cannot run for a value that passed the earlier conditions, and it carries a comment saying so.
- **`Join`.** It finds the first `://` in `bare` and returns `bare` with `rawUserinfo` and `@` inserted after it. A `bare` with no `://` returns false.
- **Invariants, pinned by TEST-1 for every value where `Split` is true.** `Join(p.Bare, p.RawUserinfo)` returns the input and true. `p.Clean == Redact(repo)`. `url.Parse(p.Bare)` has a nil `User`.

`Bare`, `RawUserinfo`, and `Join` exist for the D-11 forward read. If the open question on the column resolves to dropping it from this step, remove all three, and `Split` returns `Clean` and the flag.

**IMPLEMENTOR'S CHOICE:** whether `Redact`, `Unsafe`, and `Split` share one `url.Parse` call — the observable results must equal the rules above for every row of the TEST-1 tables and every existing row of `TestRedactRepo`.

### CODE-2. `pkg/store/store.go`, `pkg/store/layer_repo.go` (new), and `pkg/registry/server/layers.go:591-612`

In `LayerConfig`, replace the `Repo` line and add the field after it:

```go
// Repo is the git remote in the form a response may report. A LayerConfig
// returned by a Store read carries no URL userinfo here for a repo in the
// split class (see SplitLayerRepo). A config built by a caller and not yet
// read back, such as the one a registration or the boot seed writes, may
// still carry userinfo, so a response and a log line pass it through
// source.RedactRepo.
Repo string `json:"repo"` // git source
// RegisteredRepo is the git remote exactly as registered, userinfo
// included. A Store read sets it for a repo in the split class and leaves
// it empty otherwise. It is withheld from every response by the json:"-"
// tag, on the model of WebhookSecret. The clone reads it through CloneRepo.
//
// Spec: §7.3.1 (Repository credentials)
RegisteredRepo RegisteredRepo `json:"-"`
```

`pkg/store/layer_repo.go`, package `store`, importing `database/sql`, `errors`, and `internal/repourl`:

```go
// RegisteredRepo is a git remote as registered, which may carry a URL
// credential. String and GoString return a fixed text, so %v, %+v, %#v, %s,
// and %q of a LayerConfig held directly or under exported fields print no
// credential. A LayerConfig under an unexported struct field, and a numeric
// verb, print the raw value, because fmt cannot call the methods there.
type RegisteredRepo string

func (r RegisteredRepo) Present() bool    // r != ""
func (r RegisteredRepo) String() string   // "[redacted]" when present, "" otherwise
func (r RegisteredRepo) GoString() string // the same

// CloneRepo returns the remote a clone uses: the registered value when the
// store split one off, and Repo otherwise.
func (c LayerConfig) CloneRepo() string

// ErrRepoCredentialMismatch reports a LayerConfig whose RegisteredRepo does
// not redact to its Repo. The store writes nothing, because it cannot tell
// which of the two the caller meant to store.
var ErrRepoCredentialMismatch = errors.New("store: layer repo and registered repo disagree")

// SplitLayerRepo is the read rule every RegistryStore applies to a layer row.
func SplitLayerRepo(repo string, userinfo sql.NullString) (string, RegisteredRepo)

// LayerRepoColumn is the write rule every RegistryStore applies in
// PutLayerConfig. It returns the value of the repo column.
func LayerRepoColumn(cfg LayerConfig) (string, error)
```

`SplitLayerRepo(repo, userinfo)`:

1. Let `registered` be `repo`.
2. When `userinfo.Valid`, `repourl.Split(repo)` is false, `repourl.Join(repo, userinfo.String)` succeeds with value `j`, and `repourl.Split(j)` is true with `Bare == repo` and `RawUserinfo == userinfo.String`, let `registered` be `j`. In every other case the column value is ignored (D-12).
3. When `repourl.Split(registered)` is true, return its `Clean` and `RegisteredRepo(registered)`.
4. Otherwise return `registered` and an empty `RegisteredRepo`.

`LayerRepoColumn(cfg)`:

1. When `cfg.RegisteredRepo` is empty, return `cfg.Repo`.
2. When `repourl.Split(string(cfg.RegisteredRepo))` is true and its `Clean` equals `cfg.Repo`, return `string(cfg.RegisteredRepo)`.
3. Otherwise return `ErrRepoCredentialMismatch`.

The mismatch sentinel is a plain error. No handler branches on it, and it adds no §6.10 code.

`layerConfigEqual` (`pkg/registry/server/layers.go:591-612`) gains `a.RegisteredRepo == b.RegisteredRepo &&` after the `Repo` line. It is called only on the update path (`pkg/registry/server/layers.go:1313`), which never assigns `Repo`, so its result there does not change. `wakesWatchers` is not edited: registration passes a zero `before`, so a credential-only re-registration wakes watchers and records its §8.1 event as in 0.5.2.

`TestWakesWatchers_EveryField` (`pkg/registry/server/layer_change_predicates_test.go:45`) gains one row in the same step, because its reflection check fails from the commit that adds the field:

```go
{"RegisteredRepo", func(c *store.LayerConfig) {
	c.RegisteredRepo = store.RegisteredRepo("https://alice-user:s3cr3tpw@git.acme.com/acme/x.git")
}, false},
```

### CODE-3. `pkg/store/memory.go`, `pkg/store/sqlite.go`, `pkg/store/postgres.go`, and `pkg/store/schema_migrate.go:76-93`

- **Schema.** Add `repo_userinfo TEXT`, nullable with no default, to both `CREATE TABLE layer_configs` statements (`pkg/store/sqlite.go:126-148`, `pkg/store/postgres.go:113`). Add one `additiveColumns` row: `{"layer_configs", "repo_userinfo TEXT", "repo_userinfo TEXT"}`.
- **SQLite and Postgres `PutLayerConfig`.** Call `LayerRepoColumn(cfg)` first and return its error before any statement. Bind its result where `cfg.Repo` is bound today. Append `repo_userinfo` to the column list, bound to NULL. On Postgres, add `repo_userinfo = EXCLUDED.repo_userinfo` to the `DO UPDATE SET` list (D-11).
- **SQLite and Postgres reads.** Append `repo_userinfo` to the SELECT list of the get, the list, and the deleted list on each backend (`pkg/store/sqlite.go:860`, `:875`, `:950`, `pkg/store/postgres.go:1326`, `:1346`, `:1436`). `scanLayerConfig` and `scanLayerConfigPG` scan `repo` into a local string and `repo_userinfo` into a `sql.NullString`, then assign `cfg.Repo` and `cfg.RegisteredRepo` from `SplitLayerRepo`.
- **Memory.** `PutLayerConfig` calls `LayerRepoColumn(cfg)`, returns its error before touching the map, and stores the config with `Repo` set to the result and an empty `RegisteredRepo`, so the map holds what a SQL row holds. `GetLayerConfig`, `ListLayerConfigs`, and `ListDeletedLayerConfigs` call `SplitLayerRepo` with an invalid `sql.NullString` on each returned copy.

The soft-delete, restore, and erase statements touch no `repo` column and are unchanged.

### CODE-4. `pkg/registry/ingest/orchestrator.go:107-114`, `pkg/registry/server/layers.go:1687` and `:1898`, and `pkg/registry/server/webhook_ingest.go:81`

These are the sites that hand a stored layer's repository string to go-git or classify it as go-git would. Each reads `cfg.CloneRepo()`. `CloneRepo()` returns `Repo` while `RegisteredRepo` is empty, so every edit in this section is behavior-neutral until a store splits.

In `pkg/registry/ingest/orchestrator.go`, at line 109, replace `Repo: cfg.Repo,` with:

```go
// Spec: §7.3.1 (Repository credentials). The ingest clones with the stored
// value; cfg.Repo is the reportable form.
Repo: cfg.CloneRepo(),
```

The post-ingest stamp at line 200 writes the `cfg` it was given, which still carries `RegisteredRepo`, and needs no edit. `pkg/layer/source/git.go` is unchanged.

In the restore handler (`pkg/registry/server/layers.go:1687`), the reingest handler (`pkg/registry/server/layers.go:1898`), and the inbound-webhook handler (`pkg/registry/server/webhook_ingest.go:81`), replace the last argument of the `authorizeLocalSource` call, `cfg.Repo`, with `cfg.CloneRepo()`:

```go
if !e.authorizeLocalSource(w, r, cfg.SourceType, cfg.LocalPath, cfg.CloneRepo()) {
```

Each of the three holds a config read from the store, and after CODE-3 its `Repo` is the reported form, which go-git can resolve to a different transport than the registered bytes (fact 7, D-16). The registration call (`pkg/registry/server/layers.go:1433`) passes `req.Repo` and the update call (`pkg/registry/server/layers.go:1243`) passes an empty repository string, and neither changes. `authorizeLocalSource`, `namesHostPath`, and `isFileTransportRepo` (`pkg/registry/server/layer_capabilities.go:49-108`) are not edited. The gate rows of TEST-4(a) pin each of the three sites. A site left on `cfg.Repo` fails the file-transport row in one direction or the hostless row in the other.

### CODE-5. `pkg/registry/server/layers.go:344-351` and `:750-758`

```go
// reportedRepo returns the repo a response reports for cfg. It redacts the
// registered bytes exactly once: the registered value for a layer the store
// split, and Repo for every other config. source.RedactRepo is not
// idempotent, so it is never applied to a Repo the store already redacted.
//
// Spec: §7.3.1 (Repository credentials)
func reportedRepo(cfg store.LayerConfig) string {
	return source.RedactRepo(cfg.CloneRepo())
}

func wireLayer(cfg store.LayerConfig) store.LayerConfig {
	cfg.Repo = reportedRepo(cfg)
	return cfg
}

func redactedRepoResubmitted(stored store.LayerConfig, repo string) bool {
	reported := reportedRepo(stored)
	holdsCredential := stored.RegisteredRepo.Present() || reported != stored.Repo
	return holdsCredential && repo == reported
}
```

- **`reportedRepo`.** It is unexported, lives beside `wireLayer`, and has these two callers and no other. It reads `cfg.Repo` and `cfg.RegisteredRepo` through `CloneRepo()` and holds no state of its own. `RegisteredRepo` is set by a store read (`SplitLayerRepo`, CODE-2 and CODE-3) and is empty on every fresh struct, so for a split layer the result is `RedactRepo(registered)`, and for every other config it is `RedactRepo(cfg.Repo)`, which is the 0.5.2 expression. Both are the 0.5.2 reported value. For a split layer the result also equals `stored.Repo` by D-5. The helper does not depend on that equality: a config whose `Repo` disagreed with its `RegisteredRepo` would still report the redaction of the registered bytes, with no userinfo.
- **Why `source.RedactRepo(stored.Repo)` is replaced.** `RedactRepo` is not idempotent (fact 6). Applied to the store-read `Repo` of a split layer whose reported form carries `@` after its host, it returns `[redacted]`. The list, update, and reorder responses (`pkg/registry/server/layers.go:326`, `:338`, `:1314`, `:1324`) would then report `[redacted]` where 0.5.2 and the register response (`pkg/registry/server/layers.go:1591`, a fresh struct) report the stripped URL, and the guard would stop refusing the 0.5.2 reported value. A branch on `RegisteredRepo.Present()` that returns `cfg.Repo` unredacted was considered. The single expression is chosen because it has no branch and does not trust `Repo`.
- **The guard.** Without the first disjunct the refusal would never fire for a split layer, because the reported value equals its `Repo`. The call site at `pkg/registry/server/layers.go:1454` and its envelope are untouched. Extend the function's doc comment with one sentence naming the two ways a stored layer holds a credential.
- **`wireLayer`.** Its signature, its callers, and its doc comment stay. Reword the `readableBy` comment (`pkg/registry/server/layers.go:316-319`): a projection written back to the store would store `[redacted]` for a fail-closed value and would drop the credential of an unsplit one.
- **The boot log** (`internal/serverboot/serverboot.go:680-681`) holds the fresh struct it built, whose `RegisteredRepo` is empty, and keeps `source.RedactRepo(lc.Repo)`.

These sites need no edit, and TEST-3 or TEST-4 confirms each: registration (`pkg/registry/server/layers.go:1461-1472`), the boot seed and boot log (`internal/serverboot/serverboot.go:635-645`, `:680-681`), reorder, update, and `podium admin migrate`.

## Edge cases and accepted failure modes

No spec text changes, so each row cites the existing §7.3.1 sentence that governs it. "Private remotes" is the subsection of `docs/deployment/layers.md`, and "layer object" is the table in `docs/reference/http-api.md`.

| Case | Outcome | Governing text; documented in |
|:--|:--|:--|
| `https://alice-user:s3cr3tpw@git.acme.com/acme/x.git` read from any store | `Repo` is `https://git.acme.com/acme/x.git`, `RegisteredRepo` holds the registered URL, and every response reports the same bytes as 0.5.2 | §7.3.1 "with its userinfo removed"; layer object |
| The same layer is ingested | The provider receives the registered URL, and the remote receives the same basic-auth pair as in 0.5.2 | §7.3.1 "the ingest clones with the stored value"; Private remotes |
| Update, reorder, secret rotation, or post-ingest stamp on a split layer | The `repo` column holds the registered bytes after the write | §7.3.1 "An update does not change `repo`"; `docs/reference/http-api.md` Update a layer |
| Non-canonical registered URL, such as `HTTPS://t@host/a.git` or one with an unescaped space in its path | Reported in canonical form, as in 0.5.2. The `repo` column keeps the registered bytes across every write | §7.3.1 "stores it unchanged"; layer object |
| Registered URL whose reported form carries `@` after its host, such as `https://ghp_tok3n@git.acme.com/acme/a%40b c.git` | Split. Every response reports `https://git.acme.com/acme/a@b%20c.git`, and the guard refuses that value on re-registration, as in 0.5.2. No response reports `[redacted]` for it, because the registered bytes are redacted once (D-5) | §7.3.1 "with its userinfo removed"; layer object |
| Stored file-transport URL with userinfo, such as `file://tok@/srv/x` or `file://tok@?/srv/x` | Split. A restore, a reingest, or a webhook delivery that the admin arm does not admit is refused with `403 auth.forbidden` and `details.constraint: "local_source"`, as in 0.5.2, because the gate classifies the registered bytes (D-16) | §7.3.1 "Local-source authorization"; `docs/reference/http-api.md` Layer management, "Local-source authorization" |
| Stored hostless network URL with userinfo, such as `https://tok@` | Split, and reported as `https:` as in 0.5.2. The gate classifies the registered bytes as `https`, so the non-admin owner's reingest is admitted and reaches the clone, as in 0.5.2 (D-16) | §7.3.1 "Local-source authorization"; no docs page, a degenerate value |
| Empty userinfo, `https://@host/x.git` or `https://:@host/x.git` | Split. Reported as `https://host/x.git`, and the guard refuses that value on re-registration, as in 0.5.2 | §7.3.1 "Re-registration with a reported `repo`"; `docs/reference/http-api.md` Register a layer |
| `ssh://git@host/x.git` | Split. Reported as `ssh://host/x.git`, and the clone receives the registered URL with its connection user | §7.3.1 "with its userinfo removed"; layer object |
| scp-like remote, filesystem path, or empty `repo` | Unsplit. `RegisteredRepo` is empty and `Repo` is the stored value | §7.3.1 "is reported as stored"; layer object |
| Fail-closed value (a scheme that does not parse, or `http`/`https` with `@` after the host) | Unsplit. `Repo` holds the stored value in process, a response reports `[redacted]`, and the guard fires on `[redacted]` through the second disjunct of D-10 | §7.3.1 "is reported as the fixed string `[redacted]`"; layer object |
| Network-path value `//tok@host/x://y` | Unsplit. Reported as `//host/x://y` as in 0.5.2, and covered by the second disjunct of D-10 | §7.3.1 "with its userinfo removed"; layer object |
| Re-registration of a split layer with its reported `repo` | `400 registry.invalid_argument` with `details.constraint: "redacted_repo"`, as in 0.5.2 | §7.3.1 "Re-registration with a reported `repo`"; Private remotes |
| The same registration with `force_repo_overwrite` | `201`. The fresh struct has no `RegisteredRepo`, so the stored `repo` is the value sent, with no credential | Same |
| Fresh `LayerConfig` printed or marshalled before a store read | `Repo` still carries userinfo. `wireLayer` and the boot-log `RedactRepo` cover the two paths that hold one (accepted, D-6) | §7.3.1 "Every response that carries the layer object"; no docs page, internal |
| `LayerConfig` printed under an unexported struct field or with a numeric verb | Prints the raw registered URL (accepted, D-6). No current code does this | No client-observable outcome; D-6 |
| Config whose `RegisteredRepo` does not redact to its `Repo` | `PutLayerConfig` returns `ErrRepoCredentialMismatch` and writes nothing. No current caller produces it | No client-observable outcome; D-8 |
| Custom `RegistryStore` that does not apply the rules | Its layers read unsplit. `wireLayer`, the guard's second disjunct, and `CloneRepo()` give 0.5.2 behavior. It fails the `LayerRepoCredential` conformance case | §7.3.1 as above; D-8 |
| 0.5.2 binary started against a database this release wrote | Runs as before. Its statements name no `repo_userinfo`, which stays NULL | DOC-1 changelog entry |
| Row with a non-NULL `repo_userinfo` (a step-2 row, or a hand edit) | Read recombines it with `repo` when the result is in the split class. The next write stores the registered bytes in `repo` and NULL in the column | D-12; DOC-1 changelog entry |
| Row whose `repo` is in the split class and whose `repo_userinfo` is also non-NULL, or whose recombined value is outside the split class | The column is ignored and the row reads as its `repo` alone | D-12 |
| 0.5.2 binary started against a database step 2 wrote | Clones without the credential (deferred to step 2, which documents the floor) | D-13; Non-goals |
| Post-ingest stamp writes the config read before a long ingest began | A registration made during the ingest is overwritten, `RegisteredRepo` included, as the whole struct is in 0.5.2 (pre-existing) | Non-goals |
| Stored `repo` at rest | Unencrypted, as today (out of scope, D-2) | Non-goals |

## Testing

Every new test carries `// Spec: §7.3.1 (Repository credentials)`, and the guard tests carry `// Spec: §7.3.1 (Re-registration with a reported repo)`. No §6.10 code is added, so no `// Matrix:` annotation is owed. Credential fixtures are distinctive strings (`alice-user`, `s3cr3tpw`, `ghp_tok3n`).

### TEST-1. Unit tests for the classifier

Targets: `internal/repourl/repourl_test.go` (new) and `pkg/layer/source/redact_test.go`.

- (a) Positive table for `Split`. Each row carries a literal expected `Clean`. Do not compute the expectation by calling `Redact` or `source.RedactRepo`.

  | Input | Expected `Clean` | Expected `RawUserinfo` |
  |:--|:--|:--|
  | `https://alice-user:s3cr3tpw@git.acme.com/acme/x.git` | `https://git.acme.com/acme/x.git` | `alice-user:s3cr3tpw` |
  | `https://ghp_tok3n@git.acme.com/acme/x.git` | `https://git.acme.com/acme/x.git` | `ghp_tok3n` |
  | `https://:s3cr3tpw@git.acme.com/acme/x.git` | `https://git.acme.com/acme/x.git` | `:s3cr3tpw` |
  | `HTTPS://t@host/a.git` | `https://host/a.git` | `t` |
  | `https://u:p%2fq@host/a b.git` | `https://host/a%20b.git` | `u:p%2fq` |
  | `https://a@b:c@host/x.git` | `https://host/x.git` | `a@b:c` |
  | `https://tok@host` | `https://host` | `tok` |
  | `https://@host/x.git` | `https://host/x.git` | empty |
  | `https://:@host/x.git` | `https://host/x.git` | `:` |
  | `https://tok@host/x.git#` | `https://host/x.git` | `tok` |
  | `https://tok@host/x.git?` | `https://host/x.git?` | `tok` |
  | `ssh://git@host/x.git` | `ssh://host/x.git` | `git` |
  | `file://tok@/srv/x` | `file:///srv/x` | `tok` |
  | `file://tok@?/srv/x` | `file:?/srv/x` | `tok` |
  | `https://tok@` | `https:` | `tok` |
  | `https://ghp_tok3n@git.acme.com/acme/a%40b c.git` | `https://git.acme.com/acme/a@b%20c.git` | `ghp_tok3n` |

  For each row assert that `Split` is true, `Clean` equals the literal, `Join(Bare, RawUserinfo)` returns the input byte for byte, `url.Parse(Bare)` has a nil `User`, and `Redact(input)` equals `Clean`. The `a@b:c` row exercises the cut at the last `@` of the authority. The `file://tok@?/srv/x` and `https://tok@` rows pin the reported forms that lose their `//`, which are the forms go-git resolves to a different transport than the registered value (fact 7). The gate rows of TEST-4(a) assert that classification, because this package does not import go-git.

  One further assertion documents that `Redact` is not idempotent (fact 6): `Redact("https://git.acme.com/acme/a@b%20c.git")` returns `Redacted`. Its comment states that this is why no caller redacts a `Clean` value, and that the assertion is a record of the 0.5.2 behavior and is not a requirement on it.
- (b) Negative table.
  - `git@github.com:acme/x.git`, `/srv/git/acme.git`, the empty string, and `https://git.acme.com/acme/x.git` return `Split` false and `Unsafe` false, and `Redact` returns the input.
  - `//tok@host/x://y` returns `Split` false and `Unsafe` false, and `Redact` returns `//host/x://y`.
  - `https://alice-user:s3cr3t\x7fpw@host/x`, `https://ghp_tok/3n@host/x.git`, `https://git.acme.com/acme/x.git?ref=a@b`, and `https://bob@ghp_tok/3n@host/x.git` return `Split` false and `Unsafe` true. The last row parses with a non-nil `User`, so it fails a `Split` that omits the unsafe check.
  - `Join` on a value with no `://` returns false.
- (c) `TestRedactRepo` in `pkg/layer/source/redact_test.go` gains the row `//tok@host/x://y` with expected `//host/x://y`. The existing table has no network-path case. Every existing row, `TestRedactCloneError`, and `TestGit_CloneErrorCarriesNoCredential` pass with no other edit.

### TEST-2. Store conformance, column bytes, forward read, and rollback

- (a) Conformance case `LayerRepoCredential` in `pkg/store/storetest/storetest.go`, registered in `Suite` beside `LayerConfigCRUD`. Expected values are string literals, and the file gains no import of `pkg/layer/source`. Each step uses `must()` and one combined assertion.
  - Put a fresh config with `Repo: "https://alice-user:s3cr3tpw@git.acme.com/acme/a b.git"`. `GetLayerConfig` and `ListLayerConfigs` return `Repo == "https://git.acme.com/acme/a%20b.git"`, a present `RegisteredRepo`, and `CloneRepo()` equal to the registered string.
  - Read-modify-write: set `LastIngestedRef` on the config a read returned and put it back. A second read returns the same `Repo` and `CloneRepo()`.
  - Soft-delete the layer. `ListDeletedLayerConfigs` returns the same split.
  - `https://@git.acme.com/x.git` reads back with `Repo == "https://git.acme.com/x.git"` and a present `RegisteredRepo`.
  - `https://ghp_tok3n@git.acme.com/acme/a%40b c.git` reads back with `Repo == "https://git.acme.com/acme/a@b%20c.git"`, a present `RegisteredRepo`, and `CloneRepo()` equal to the registered string, from `GetLayerConfig` and from `ListLayerConfigs`. This row fails on a backend that redacts the value twice, which would return `[redacted]`.
  - `git@github.com:acme/x.git`, `/srv/git/acme.git`, and `https://ghp_tok/3n@host/x.git` read back with `Repo` equal to the input and an empty `RegisteredRepo`.
  - Error path: a config read from the store, with `Repo` then set to `https://git.acme.com/acme/other.git`, returns an error for which `errors.Is(err, store.ErrRepoCredentialMismatch)` holds, and a following read returns the row unchanged.
- (b) Raw-column tests, in internal-package files (package `store`): `pkg/store/sqlite_test.go` for SQLite, and an internal-package file for Postgres gated on `PODIUM_POSTGRES_DSN` with the existing skip pattern (`pkg/store/schema_migrate_test.go:224-226`). After a put and a read-modify-write of a credential-bearing layer, `SELECT repo, repo_userinfo FROM layer_configs` returns the registered bytes and NULL.

  **IMPLEMENTOR'S CHOICE:** whether the Postgres raw-column, rollback, and forward-read tests go in `pkg/store/schema_migrate_test.go` or a new `pkg/store/layer_repo_test.go` — the file must be package `store`, and each test must skip cleanly when `PODIUM_POSTGRES_DSN` is unset.
- (c) Rollback test, SQLite and Postgres. Against a database this release created and wrote, execute the literal 0.5.2 writer: the `INSERT OR REPLACE` from `git show v0.5.2:pkg/store/sqlite.go` and the upsert from `git show v0.5.2:pkg/store/postgres.go`, neither of which names `repo_userinfo`. Assert that the statement succeeds and that a following `GetLayerConfig` returns the split value with `CloneRepo()` equal to the inserted URL. The test fails if `repo_userinfo` is ever declared `NOT NULL` without a default. A comment states that the statement is a frozen copy of the 0.5.2 writer and that the test is deleted when step 2 raises the rollback floor (D-13).
- (d) Migration test, SQLite, in `pkg/store/schema_migrate_test.go`. Create a legacy `layer_configs` table with the 0.5.2 column set and one row whose `repo` carries userinfo. `OpenSQLite` adds `repo_userinfo`, and `GetLayerConfig` returns the clean `Repo`, a present `RegisteredRepo`, and the registered `CloneRepo()`. The "column is added" assertion is already made by `TestSQLite_AdditiveColumnsMatchSchema` and the loop in `TestPostgres_AdditiveMigration`, which iterate `additiveColumns`.
- (e) Forward-read rows, conditional on the open question about the column. A table test of `SplitLayerRepo` in package `store`, plus one SQLite test and one Postgres test that each write the row with raw SQL:

  | `repo` | `repo_userinfo` | Expected `Repo` | Expected `CloneRepo()` |
  |:--|:--|:--|:--|
  | `https://git.acme.com/x.git` | `ghp_tok3n` | `https://git.acme.com/x.git` | `https://ghp_tok3n@git.acme.com/x.git` |
  | `https://git.acme.com/x.git` | empty string, non-NULL | `https://git.acme.com/x.git` | `https://@git.acme.com/x.git` |
  | `https://alice-user@git.acme.com/x.git` | `ghp_tok3n` | `https://git.acme.com/x.git` | `https://alice-user@git.acme.com/x.git` (column ignored) |
  | `git@github.com:acme/x.git` | `ghp_tok3n` | `git@github.com:acme/x.git` | the same (column ignored) |
  | `https://git.acme.com/x.git` | `ghp_tok/3n` | `https://git.acme.com/x.git` | the same (column ignored; the recombined value is fail-closed) |
  | `ssh://git.acme.com/x.git` | `tok@evil.com/y` | `ssh://git.acme.com/x.git` | the same (column ignored; the recombined value splits into different parts) |
  | `https://git.acme.com/x.git` | NULL | `https://git.acme.com/x.git` | the same |

  The `ssh` row pins the comparison in step 2 of `SplitLayerRepo`, `Bare == repo` and `RawUserinfo == userinfo.String`, which no other row reaches. `Join` returns `ssh://tok@evil.com/y@git.acme.com/x.git`. The `@`-after-authority check of `parseRepo` applies to `http` and `https` alone (`pkg/layer/source/redact.go:94-100`), so the value is in the split class, and `Split` returns `RawUserinfo` `tok` and `Bare` `ssh://evil.com/y@git.acme.com/x.git`. Neither equals its input, so the column is ignored. A `SplitLayerRepo` that omits the comparison fails this row, because it returns `Repo == "ssh://evil.com/y@git.acme.com/x.git"` and a `CloneRepo()` whose host is `evil.com`. The table test's comment states this.

  The SQLite test then puts the first row's config back and asserts that `repo` holds `https://ghp_tok3n@git.acme.com/x.git` and `repo_userinfo` is NULL. It pins the SQLite scan path and the recomposed `repo`. It cannot pin the clearing, because `INSERT OR REPLACE` resets `repo_userinfo` whether or not the statement binds it.

  The Postgres test is the one that pins the clearing and the Postgres scan path. It is in the internal-package file of (b), gated on `PODIUM_POSTGRES_DSN`, and runs these steps:
  1. Put a layer with `Repo: "https://git.acme.com/x.git"` through `PutLayerConfig`, so the row exists and the next put takes the `ON CONFLICT` arm.
  2. Set the column with raw SQL: `UPDATE layer_configs SET repo_userinfo = 'ghp_tok3n'` for that tenant and layer.
  3. Assert that `GetLayerConfig` and `ListLayerConfigs` each return `Repo == "https://git.acme.com/x.git"` and `CloneRepo() == "https://ghp_tok3n@git.acme.com/x.git"`. Soft-delete the layer, assert the same pair from `ListDeletedLayerConfigs`, and restore it. These cover `scanLayerConfigPG` with a non-NULL column on the get, the list, and the deleted list.
  4. Put back the config the get returned.
  5. Assert that `SELECT repo, repo_userinfo FROM layer_configs` for the row returns `https://ghp_tok3n@git.acme.com/x.git` and NULL.

  Step 5 fails when `repo_userinfo = EXCLUDED.repo_userinfo` is absent from the `DO UPDATE SET` list, because the column then keeps `ghp_tok3n` beside a `repo` that already carries it. A read after step 4 cannot stand in for the raw `SELECT`: a `repo` in the split class makes the read ignore the column (D-12), so the stale value is invisible to `GetLayerConfig`. The test's comment states both facts.
- (f) Print and marshal test in `pkg/store/store_test.go`. The subject is a config returned by a memory-store read of a credential-bearing layer. `fmt.Sprintf` with `%v`, `%+v`, `%#v`, `%s`, and `%q`, and `json.Marshal`, each produce output that contains neither `alice-user`, `s3cr3tpw`, nor the registered URL. `LayerRepoColumn` and the `RegisteredRepo` methods get direct unit rows for the empty value. The test makes no claim about a fresh, unsplit struct.
- (g) Write-rule rows, a table test of `LayerRepoColumn` in package `store`, beside the `SplitLayerRepo` table. The conformance error path in (a) reaches only the `Clean != Repo` half of the rule, with a registered value inside the split class. These rows reach the split-class half, and an implementation that compares `repourl.Redact(registered)` with `Repo` alone fails the first three.

  | `RegisteredRepo` | `Repo` | Expected |
  |:--|:--|:--|
  | `https://ghp_tok/3n@host/x.git` | `[redacted]` | `ErrRepoCredentialMismatch` (the registered value is fail-closed) |
  | `git@github.com:acme/x.git` | `git@github.com:acme/x.git` | `ErrRepoCredentialMismatch` (scp-like, outside the split class) |
  | `//tok@host/x://y` | `//host/x://y` | `ErrRepoCredentialMismatch` (network-path form, outside the split class) |
  | `https://git.acme.com/x.git` | `https://git.acme.com/x.git` | `ErrRepoCredentialMismatch` (no userinfo) |
  | `https://ghp_tok3n@git.acme.com/x.git` | `https://git.acme.com/other.git` | `ErrRepoCredentialMismatch` (split class, `Clean` differs) |
  | `https://ghp_tok3n@git.acme.com/x.git` | `https://git.acme.com/x.git` | returns `https://ghp_tok3n@git.acme.com/x.git` and a nil error |
  | `https://ghp_tok3n@git.acme.com/acme/a%40b c.git` | `https://git.acme.com/acme/a@b%20c.git` | returns the registered value and a nil error |

  Each refusing row asserts `errors.Is(err, store.ErrRepoCredentialMismatch)` and an empty returned string. The last row fails a rule that redacts `Repo` before comparing.

### TEST-3. Existing assertions that follow the split

These land with CODE-3. Each assertion that the stored `Repo` equals the full credential URL changes to assert both that `CloneRepo()` equals the registered URL and that `Repo` equals the reported value.

- `pkg/registry/server/layer_repo_credential_test.go`: `assertStoredRepo` (line 39) and the stored-value assertions at lines 118, 192, 419, 516, 576, and 584.
- `internal/serverboot/declared_layers_test.go:301`.
- `test/integration/layer_repo_credential_test.go`: the comment at lines 203-204 is reworded to say the clone reads the registered value. `TestLayerRepoCredential_CloneFailureReportsNoCredential` passes with no assertion change, and it is the SQLite check that the clone still sends the credential.

No assertion on a response body, a log line, or an envelope changes. With these edits the 0052 register, update, list, and reorder response tests and the refuse, force, fail-closed-class, and soft-deleted guard subtests are the regression net for D-3. `TestDeclarativeLayers_RepoCredentialNotReported` and `TestLayerRegister_RedactedRepoGuard_CLI` pass unmodified.

### TEST-4. New handler, orchestrator, migrate, and end-to-end tests

- (a) Handler subtests in `pkg/registry/server/layer_repo_credential_test.go`, on the existing memory-store guard harness.
  - **Empty userinfo.** A layer stored with `https://@git.acme.com/x.git` and re-registered with `https://git.acme.com/x.git` returns `400 registry.invalid_argument` with `details.constraint: "redacted_repo"`. The same body with `force_repo_overwrite` returns `201`.
  - **Read-modify-write.** After an update, a reorder, and an update with `rotate_webhook_secret`, `GetLayerConfig(...).CloneRepo()` is the registered URL.
  - **A store that does not split.** A wrapper that embeds `store.Store` and returns an unsplit, strippable `Repo` with an empty `RegisteredRepo` from `GetLayerConfig` and `ListLayerConfigs`, on the pattern of `countingStore` (`pkg/registry/server/events_test.go:141`). The guard refuses the reported value, and the list response reports the stripped value. This is the only test of the second disjunct of D-10 for a strippable value.
  - **A reported value that does not survive a second redaction.** Register a layer with `https://ghp_tok3n@git.acme.com/acme/a%40b c.git`. The register response, the list response, an update response, and the reorder response each report `repo` as `https://git.acme.com/acme/a@b%20c.git`. A re-registration with that value returns `400 registry.invalid_argument` with `details.constraint: "redacted_repo"`, and the same body with `force_repo_overwrite` returns `201`. The subtest fails when `wireLayer` or the guard applies `RedactRepo` to the store-read `Repo`, which reports `[redacted]` on the list, update, and reorder responses and admits the re-registration.
  - **`reportedRepo` unit rows**, in the same file. A config with an empty `RegisteredRepo` and `Repo` `https://ghp_tok3n@host/x.git` reports `https://host/x.git`. A config with an empty `RegisteredRepo` and `Repo` `https://ghp_tok/3n@host/x.git` reports `[redacted]`. A config with `RegisteredRepo` `https://ghp_tok3n@host/x.git` and a disagreeing `Repo` `https://alice-user:s3cr3tpw@host/y.git` reports `https://host/x.git`, which pins that the helper reads the registered bytes and never returns `Repo` unredacted.
  - **Local-source gate rows**, in `pkg/registry/server/layer_local_source_test.go`, carrying `// Spec: §7.3.1 (local-source authorization)`. They pin the three CODE-4 gate sites on a store that splits.
    - `repoCases()` (line 389) gains `file://tok@/srv/x` and `file://tok@?/srv/x` with `file: true`, and `https://tok@` and `https://ghp_tok3n@git.acme.com/x.git` with `file: false`. `TestLocalSource_RepositoryStrings` then runs each through the classifier, a non-admin registration, and a non-admin reingest of a stored layer. The reingest of `file://tok@?/srv/x` answers `403 auth.forbidden` with `details.constraint: "local_source"`, and the reingest of `https://tok@` by its owner answers `200` with no `local_source` refusal.
    - The same test gains a direct classifier assertion on the reported forms, with a comment saying they are the values the gate must not read: `isFileTransportRepo("file:?/srv/x")` is false and `isFileTransportRepo("https:")` is true.
    - A restore subtest on the existing tombstone pattern (the `restore` rows at lines 156 and 299): a soft-deleted git layer stored with `file://tok@?/srv/x` is refused with the `local_source` envelope for a non-admin, and one stored with `https://tok@` is restored by its non-admin owner.
    - `TestLocalSource_WebhookIngest` (line 591) gains the rows `file://tok@/srv/x` and `file://tok@?/srv/x`, each refused with `403` and the `local_source` envelope when the admin arm denies, with the ingest runner not called, and the rows `https://tok@` and `https://ghp_tok3n@git.acme.com/x.git`, each admitted with `200`.
    - For each added row, the stored-layer assertion first reads the layer back and asserts a present `RegisteredRepo`, so the row cannot pass on a store that did not split.
- (b) Orchestrator, `pkg/registry/ingest/orchestrator_test.go`. `fakeProvider` (line 20) records `cfg.Repo`. One test puts a credential-bearing layer in a memory store, reads it back, runs `SourceIngest`, and asserts that the provider saw the registered URL, that the stored config has `LastIngestedRef` set, and that its `CloneRepo()` is unchanged. A second row with an scp-like remote asserts the provider saw `Repo` as stored.
- (c) `podium admin migrate`, `cmd/podium/admin_migrate_test.go`. `TestAdminMigrateToStandard_PumpsMetadataAndObjects` (line 105) seeds a second layer with URL userinfo and asserts that the target's `CloneRepo()` equals the source's and that the captured command output contains neither credential part.
- (d) End to end, `test/e2e/layer_repo_credential_test.go`, `TestLayerRepoCredential_SurvivesRestart_CLI`, on the restart pattern of `TestDeclarativeLayers_DeclaredGitProviderSurvivesRestart` (`test/e2e/declarative_layers_test.go:196`).
  1. Start the server with `PODIUM_SQLITE_PATH` set.
  2. Register a layer through the CLI against an in-test `httptest` remote that records `r.BasicAuth()` and answers 500.
  3. Stop the server and start it again on the same file.
  4. Assert that the listed `repo` carries no userinfo.
  5. Assert that a registration of the listed value exits non-zero with `redacted_repo`.
  6. Run `podium layer reingest` and assert that the remote recorded `alice-user` and `s3cr3tpw`.
  7. Assert that the server log carries neither credential part.

  The test catches a split that drops the credential across a process restart, which no in-process test reaches.

**Regression check on the manual suite.** The change alters nothing an operator reads, so no manual scenario is added. Scenario S89 in `test/manual-validation.md` is re-run unmodified: its checks are the `Basic ` line in the remote's log and the absence of `s89-token`, and both are unaffected.

**Closing step.** Run `go test -race -coverpkg=./... -coverprofile=cover.out` over `./internal/repourl/...`, `./pkg/layer/source/...`, `./pkg/store/...`, `./pkg/registry/...`, `./internal/serverboot/...`, `./cmd/podium/...`, and `./test/integration/...`, and confirm the new lines reach 85%. Run the Postgres tests once with `PODIUM_POSTGRES_DSN` set. Run `go list -deps ./pkg/store ./pkg/store/storetest | grep go-git` and confirm it prints nothing. Then run `make coverage-gate`.

## Resolved decisions

The human reviewer settled these on 2026-10-09.

**RD-1. Release vehicle (was OQ-1).** The release is PATCH 0.5.3. No client-observable behavior and no provider input changes, and the database stays usable by 0.5.2. `main` carries no unreleased work beyond 0.5.2, so the change merges to `main` and the release is tagged there, as 0.5.2 was. The MINOR 0.6.0 alternative is not taken.

**RD-2. The step-2 column is staged now (was OQ-2; D-11, D-12).** This release adds `layer_configs.repo_userinfo` and its read. The step-2 storage format is therefore fixed as D-12 states it. The alternative of an in-memory split alone, with the reader in a third release, is not taken.

**RD-3. `RegisteredRepo` holds the whole registered URL (D-6).** A credential-only field is deferred to step 2, where the storage format carries the userinfo in its own column. In this step the field keeps the registered bytes so that the write is byte-exact.

## Documentation changes

**DOC-1.** It lands in step S9. `CHANGELOG.md`, under `## [Unreleased]`, add a `### Changed` group with:

> - **The metadata store gains the nullable column `layer_configs.repo_userinfo`** (§7.3.1): this release adds the column and leaves it empty. The stored `repo` and every layer response are unchanged, and a 0.5.2 binary runs against the upgraded database.

No page under `docs/` changes. `docs/deployment/layers.md:115` says "The registry stores the URL as given and clones with it", and `docs/reference/http-api.md:418` says the registry "stores the value as given and clones with it". Both stay literally accurate: the `repo` column holds the registered bytes, and go-git receives them. The CLI reference and the layer object table describe responses, which are unchanged.

## Non-goals

- Step 2 of the plan: writing the credential to `repo_userinfo` and a userinfo-free value to `repo`. It is a later one-way release. This release already reads that format, so a rollback from step 2 stops at this release and cannot reach 0.5.2 (D-13).
- Encryption at rest of the `repo` column, the `repo_userinfo` column, or any other stored secret.
- Any migration, backfill, or rewrite of existing `layer_configs` rows.
- Refusing, warning on, or altering URL userinfo at registration, at update, or in `registry.yaml`.
- A dedicated credential member on the register request, a credential upload path, an SSH key store, or a secret-reference syntax.
- Splitting scp-like remotes, filesystem paths, the network-path form, or the fail-closed `[redacted]` classes. They stay in `Repo` unsplit under the 0052 mechanisms.
- A credential field on `source.LayerConfig`, or any other change to the `LayerSourceProvider` input.
- Removing or narrowing `RedactRepo`, `wireLayer`, `RedactCloneError`, the boot-log redaction, or the re-registration guard with its `force_repo_overwrite` member and `--force-repo-overwrite` flag.
- Any Web UI, SDK, CLI, HTTP API, §6.10 error code, environment variable, or `registry.yaml` change.
- **Removing userinfo from the URL handed to go-git.** The draft derived a userinfo-free `CloneOptions.URL` and an explicit `BasicAuth` inside `Git.Snapshot`. It is deferred to step 2 or a separate proposal, for these reasons.
  - go-git already builds `BasicAuth` from the endpoint userinfo, and net/http sets the same header for a password-only URL, so the `Authorization` header a remote receives is the same either way.
  - The full URL would still sit in `source.LayerConfig.Repo` in the same call frame, and the clone uses an in-memory storer local to `Snapshot`, so the remote config is never persisted.
  - The edit re-implements a dependency's userinfo handling, and it changes one wire behavior: with explicit `BasicAuth`, a password-only URL's header is also copied on a same-host redirect.
  - The problem this proposal addresses is the store boundary, which CODE-2 and CODE-3 cover.
- **A §7.3.1 spec edit restating what the ingest clones with (dropped alternative SPEC-1).** The draft staged one sentence saying the source presents the credential in the `Authorization` header and that the request URL carries no userinfo. It is dropped for these reasons.
  - The existing sentence, "the ingest clones with the stored value", already covers this step: the column keeps the registered bytes and the orchestrator hands them to the provider.
  - Presenting the credential in the `Authorization` header is 0.5.2 behavior, so the sentence would record no change.
  - An HTTP request line never carries userinfo, so "the request URL carries no userinfo" is unobservable.
  - The sentence would be false for empty userinfo, where no header is sent.
  - §9.1 leaves snapshot mechanics to the provider, and no other part of §7.3.1 names a transport header. A new behavioral sentence would create a test obligation and constrain step 2 for a property no client observes.
  - If step 2 changes what is stored, "stores it unchanged" is the sentence to revisit, in that proposal.
- The lost-update window in which the post-ingest stamp writes the config read before a long ingest started, and the boot seed writing a declared layer without its `WebhookSecret`. Both pre-date this proposal and are unchanged by it.

## Resolved in adversarial review

Review rounds populate this section. Each bullet records a finding and where its fix landed.

### Draft reconciliation (2026-10-09)

The draft's per-change challenge revisions were applied. Where they disagreed with the draft or with each other, the proposal resolves them as follows.

- **Decision numbering.** The draft's D-10 (explicit `BasicAuth` clone target) and D-11 (the password-only same-host redirect difference) are withdrawn with the `git.go` half of CODE-4. The draft's D-12 through D-17 are D-10 through D-15 here.
- **Package location.** The classifier is `internal/repourl`. The draft placed it at `pkg/layer/source/repourl`, which would have added module public API that no exported signature needs.
- **`Redact` is moved, and `Split` is narrowed.** The draft rebuilt `RedactRepo` on `Split`. For the network-path value `//tok@host/x://y` that design either broke the join invariant or returned the credential. `Redact` is now the 0.5.2 body moved unchanged, `Split` requires a non-empty parsed scheme, and D-4 states the narrower class. TEST-1 carries the row in both test files.
- **Type and field name.** The draft's `RepoCredential` struct with an unexported member is replaced by the named string type `RegisteredRepo` on the field of the same name, and the rule functions are exported. The struct could not be built by a custom `RegistryStore`, which §9.3 and the `storetest` package comment require. The sentinel keeps the name `ErrRepoCredentialMismatch`, and the conformance case keeps the name `LayerRepoCredential`.
- **`Parts` members.** The CODE-1 revision listed `User` and `Scheme` among the members, and the CODE-4 revision removed their only consumer. `Parts` carries `Clean`, `Bare`, and `RawUserinfo`. The last two serve the forward read and go with the column under OQ-2.
- **The `git.go` half of CODE-4 is withdrawn.** `pkg/layer/source/git.go` is untouched, which left the orchestrator line as the draft's remaining CODE-4 edit. Pass 1 later added the stored-layer `authorizeLocalSource` call sites in the restore, reingest, and inbound-webhook handlers, and the CODE-4 section states the current scope. The header, redirect, and `cloneTarget` cases of the draft's TEST-1 are removed with it, as is the request-URL assertion, which was vacuous. The draft's open questions on SPEC-1 and on the redirect difference are removed.
- **SPEC-1 is dropped.** The proposal stages no spec edit, the checklist has no spec step, and DOC-1 no longer needs to reconcile the docs wording with a spec rewording.
- **DOC-1.** The changelog bullet states the column and the downgrade fact. The draft's sentence on the `Authorization` header announced a non-change and is removed, and the redirect sentence is removed with the withdrawn D-11.
- **The `TestWakesWatchers_EveryField` row and the `layerConfigEqual` line move to CODE-2.** The draft placed them in TEST-3 and CODE-5. The reflection check fails from the commit that adds the field, so they land with it. With a named string type the row's mutator builds its value by conversion, and the draft's read-through-a-memory-store workaround is gone.
- **TEST-3 is divided.** Edits to existing assertions are TEST-3 and land with CODE-3 in S5, so that commit is green. New tests are TEST-4. The duplicate handler matrix over SQLite is not added, because the conformance case pins backend parity and the integration test covers the SQLite handler path.
- **The write rule checks the split class.** `LayerRepoColumn` accepts a `RegisteredRepo` only when `Split` holds for it and its `Clean` equals `Repo`. The draft compared `Redact` output alone, which would also have accepted a fail-closed registered value paired with `[redacted]`.
- **Manual validation.** No scenario is staged, because no operator-visible output changes. The S89 re-run is recorded under Testing.

### Pass 1 (2026-10-09, automated)

- **The stored-layer local-source gate classified the reported `Repo` while the clone used the registered bytes.** After the store split, restore, reingest, and the inbound webhook would have passed the redacted `Repo` to `authorizeLocalSource`, and go-git resolves that string to a different transport than the registered value for a hostless URL, in both directions. CODE-4 now changes the three call sites to `cfg.CloneRepo()`, D-16 records the decision, fact 7 records the measurement, `authorizeLocalSource` leaves the CODE-5 list of unedited sites, and the Summary, the checklist step S3, the edge-case table, the TEST-1(a) table, and the TEST-4(a) gate rows follow.
- **A second `RedactRepo` call changed the reported value to `[redacted]`.** `RedactRepo` is not idempotent for a URL whose canonical form gains `@` after the host, so `wireLayer` and the guard applying it to a store-read `Repo` would have changed the list, update, and reorder responses and disabled the guard for such a layer. CODE-5 now adds `reportedRepo`, which returns `source.RedactRepo(cfg.CloneRepo())`, and routes `wireLayer` and `redactedRepoResubmitted` through it. D-3, D-5, and D-10 state the rule, fact 6 records the measurement, and TEST-1(a), TEST-2(a), TEST-2(g), and TEST-4(a) carry the fixture `https://ghp_tok3n@git.acme.com/acme/a%40b c.git`. The finding suggested a branch on `RegisteredRepo.Present()`. The single expression is used because it redacts the registered bytes once on every path and does not trust `Repo`.
- **No test failed when the Postgres upsert omitted `repo_userinfo = EXCLUDED.repo_userinfo`, and the Postgres forward read was untested.** TEST-2(e) gains a Postgres test that sets the column with raw SQL, reads the row through the get, the list, and the deleted list, puts the config back, and asserts the raw column is NULL. The Summary's Postgres bullet names it.
- **The split-class half of the write rule had no test.** TEST-2(g) adds a `LayerRepoColumn` table with a registered value in each class outside the split class, the `Clean` mismatch, and the accepting rows.
- **The draft-reconciliation bullet on CODE-4 still stated that CODE-4 was the orchestrator line alone.** The first bullet of this pass extended CODE-4 to the three stored-layer `authorizeLocalSource` call sites (`pkg/registry/server/layers.go:1687` and `:1898`, and `pkg/registry/server/webhook_ingest.go:81`) and left that parallel statement unchanged. The bullet now records only the withdrawal of the `git.go` half and points to the CODE-4 section for the current scope.

### Pass 2 (2026-10-09, automated)

- **The round-trip comparison of the forward read had no test.** No TEST-2(e) row had `Join` succeed and `Split` hold for the recombined value with parts that differ from the inputs, so a `SplitLayerRepo` without the `Bare == repo` and `RawUserinfo == userinfo.String` comparison passed every listed test and returned a clone URL on another host. TEST-2(e) gains the row `ssh://git.acme.com/x.git` with `repo_userinfo` `tok@evil.com/y`, whose expected `Repo` and `CloneRepo()` are both `ssh://git.acme.com/x.git`, and a paragraph that states what the row pins.
