# Proposal 0016: Authorize local-source layer registration, confine what a local layer's ingest may read, and report the caller's layer capabilities so the panel offers only what that caller can take

- Issue: (to be filed)
- Status: Verified (2026-09-01). Converged after 12 adversarial review rounds (67
  findings fixed); awaiting sign-off.
- Date: 2026-09-01

This document stages the proposed spec, code, test, and documentation changes.
It does not modify any spec, code, or doc file. Apply the changes in the staged
sections after sign-off.

## Summary

**What changes.**

- §7.3.1 gains a local-source authorization paragraph beside its existing layer
  write authorization and layer read visibility paragraphs. Registering a layer
  that names a filesystem path on the registry host, patching that path,
  restoring such a layer, and reingesting one are authorized to a tenant admin
  holding the §4.7.2 admin role, and any other caller is refused with
  `403 auth.forbidden` carrying `details.constraint: "local_source"`. A `git`
  repository string that resolves to go-git's file transport names such a path
  and takes the same arm. A second paragraph states the local-source ingest
  confinement: an ingest it covers reads only within the directory the layer's
  configured path resolves to, whichever caller declared it, and a read the
  ingest requires and cannot satisfy fails that layer's ingest. SPEC-2's staged
  paragraph states which ingests it covers and which arm it leaves to the
  authorization rule, and no other site in this document restates that.
- `pkg/layer/source` resolves every read of a local layer's tree through
  `os.OpenInRoot`, so a symbolic link stored inside the tree cannot be read
  through to a target outside it, and a link whose target is written as an
  absolute path is refused whatever it names. The three other sites that build a local
  layer's tree themselves (`internal/serverboot` twice and
  `pkg/registry/server`) are routed through the same constructor, so the
  confinement binds every deployment mode rather than the API-registered layer
  alone. `pkg/registry/ingest`'s `SKILL.md` read gains one branch so a refusal
  there keeps the sentinel that classifies it.
- `pkg/registry/server` gains one file carrying the local-source authorization
  rule, the host-path classifier it reads, and the capability evaluator. The
  rule and the evaluator both read the `authAdmin` callback the write gate
  already holds, and `register`, `update`, `restore`, `reingest`, and the
  webhook ingest call the rule.
- §7.3.4's posture read gains `layer_capabilities`, carrying `manage_any_layer`,
  and the sentences bounding its body list are amended so neither states a rule
  the new field breaks: the opening pair stops saying a carried credential is
  verified only to report `subject`, and the closing sentence keeps the body
  closed against every other disclosure. `pkg/registry/server`'s posture
  handler serializes it from a seam that reports every member false where the
  read was not wired, and `internal/serverboot` passes the layer endpoint's own
  evaluator into it, so the value a client renders on and the gate the registry
  applies are one expression.
- The `--local` usage strings on `podium layer register` and `podium layer
  update` name the constraint the registry now enforces.
- The web client gains one predicate, `mayTake(op, target, caps, subject)`,
  which every control the layer panel renders for a §7.3.1 layer write
  consults, and the shell derives the capability object once and threads it
  beside the subject the surfaces already take, together with a
  `postureAnswered` boolean, which reports whether the read settled anything and
  is read by the two empty states alone. The client constructs one register
  target, `newLayerTarget(subject)`, so the register control and the two copies
  that instruct a reader to press it read one call rather than three statements
  of what it reduces to. The predicate reads the
  posture read's `manage_any_layer` and `subject` and the four fields the two
  server gates read on the target of that operation, where the target is the
  stored record, the patch, or the registration according to what the
  operation names, and it reads nothing else. A control the predicate refuses
  is absent, a control it admits is then disabled by the §13.2.1 read-only
  marker as before, and the refused-write rendering stays drawn because a
  prediction can go stale. §13.10's staged text states that one rule, its two
  exceptions, and its one boundary rather than enumerating the controls: a
  reordering affordance is disabled rather than removed and is settled over
  every layer in the block a move would reorder; a control naming a value the
  registry resolves away rather than refuses, such as a registration's class,
  is withheld on the same reading; a control whose request the panel narrows to
  the layers the rule admits is rendered where the rule admits the caller on at
  least one of them and acts on that subset alone; a refusal the target's own
  fields do not settle, such as a `git` repository string that resolves to the
  file transport, is offered and answered by the registry; and a control whose
  availability turns on the layer record alone, with no dependence on the
  caller, is outside the rule.
- §4.6, §4.7.2, §13.10, the reference and deployment pages, the hand-run
  scenarios in `test/manual-validation.md`, the web design documents and their
  drawn boards, and a `CHANGELOG.md` entry follow.

**Fixed decisions.**

- The local-source rule has one arm. A caller the §4.7.2 admin arm admits may
  name a filesystem path on the registry host, and every other caller is
  refused. There is no allowlist of permitted roots, no path resolution, and no
  prefix comparison in the authorization decision, and no configuration key is
  added.
- A registry started with no identity provider configured, or one started in
  public mode, authenticates no caller, so no caller can hold the admin role and
  every operation the rule governs is admitted there, a webhook-triggered
  reingest included. This proposal adds no branch on that deployment, for the
  reasons D9 gives, and the residual goes to the reviewer as OQ-1.
- The rule reads the request's or the stored layer's source type, filesystem
  path, and git repository string. A repository string go-git resolves to its
  file transport is a filesystem path on the registry host, so it takes the same
  arm; a repository string naming a network endpoint does not. A `git` source is
  classified on its repository string alone, so a filesystem path stored beside
  one is not classified, because the Git transport never reads it and refusing
  such a layer would end its webhook deliveries while confining nothing. The
  classifier
  calls go-git's own endpoint parser rather than restating it, fails closed on a
  string that parser rejects, and classifies an empty repository string as
  nothing, so a patch that names no path is admitted.
- The refusal reuses `auth.forbidden` with `details.constraint: "local_source"`.
  No §6.10 code is added and no matrix axis entry is added.
- The confinement and the authorization rule are separate controls and both are
  needed, for the reasons D3 gives. The confinement is not configurable, and
  SPEC-2's staged paragraph states which ingests it covers.
- `register`, `update`, `restore`, `reingest`, and the webhook-triggered ingest
  are the guarded operations. `unregister` and `reorder` name no path and re-read
  none.
- The posture read reports a capability object rather than a role name or an
  `admin` boolean, and the object's one member is named for the gate
  (`manage_any_layer`) rather than for the role.
- The posture handler's capability seam defaults closed. `NewLayerEndpoint`
  installs an admitting `authAdmin` by default
  (`pkg/registry/server/layers.go:190`), and a reporting surface must default
  the other way.
- The client predicts and the registry authorizes. One predicate,
  `mayTake(op, target, caps, subject)`, decides whether a control that would
  take a §7.3.1 layer write is rendered, and no other expression in the client
  decides presence. The present controls are then disabled by `readOnly`.
- A control whose availability turns on the layer record alone, with no
  dependence on the caller, is outside the rule and is decided where it is
  decided today. The webhook-rotation checkbox and `editableVisibility` are the
  two such controls, and neither changes.
- The registration-class rule is not in this proposal. `register` keeps
  resolving the class server-side, `LayerRegisterRequest.UserDefined` stays a
  plain `bool`, and `update` keeps ignoring a visibility patch on a
  user-defined layer. Non-goals records why.
- The account cluster's raw-subject rendering is not in this proposal. The
  posture read gains no `name` and no `email` field, and `pkg/identity` and
  `pkg/layer` gain no field. Non-goals records why.
- Podium is pre-1.0. No flag, key, or dual code path restores an unconfined
  non-admin local registration, and `SessionPosture` grows its field with no
  version negotiation.

**Watch out for.**

- **`os.DirFS` confines a path string and does not confine resolution.** All
  three ingest walks discriminate on `d.IsDir()` and on file base names before
  calling `fs.ReadFile` (`pkg/registry/ingest/ingest.go:992-1015`, `:1033-1055`,
  `:1151-1176`), so a symbolic link is treated as an ordinary bundled file and
  read through. Skipping `fs.ModeSymlink` entries in the walks is not a
  substitute: it scatters enforcement over three walks and two direct reads, it
  is lexical rather than race-free, and it bans links inside the root that the
  confinement admits.
- **`os.Root` refuses an absolute symbolic link that resolves inside the root.**
  The refusal is unconditional on the target
  (`$(go env GOROOT)/src/os/root.go:43`, `:301-306`), so a link the previous
  `os.DirFS` tree read through now fails the layer's whole ingest. CODE-1 states
  the arm, the staged §7.3.1 sentence distinguishes the relative link from the
  absolute one, and TEST-1 pins both.
- **Go names no sentinel for a confinement refusal.** `os.OpenInRoot` returns an
  `*fs.PathError` wrapping the unexported `errPathEscapes`, which satisfies
  neither `fs.ErrNotExist`, nor `fs.ErrPermission`, nor `os.ErrInvalid`, so no
  `errors.Is` test isolates the escape. The confined tree therefore classifies by
  the inverse: `fs.ErrNotExist` passes through unchanged and every other failure
  is wrapped in `ErrSourceUnreachable`. Wrapping every failure would reclassify
  an absent `SKILL.md`, and wrapping only the escape needs a match on an
  unexported standard-library message.
- **`source.Local` is not the only door into the ingest.**
  `internal/serverboot/serverboot.go:455` and `:616` and
  `pkg/registry/server/server.go:338` each build the tree themselves and never
  call the provider. Confining only the provider leaves the same directory
  unconfined when it is bootstrapped, which is a deployment-mode divergence
  rather than the §11 equivalence.
- **A symbolic link as the layer root is legitimate and stays legitimate.**
  `os.OpenRoot` resolves its own argument normally and confines only paths
  beneath it, so a declared path that runs through a link keeps working.
  `pkg/materialize/atomic_treeatomic_test.go:105` records that as correct.
- **`Snapshot` is an SPI return value.** §9.3 forbids a `func` type on it, so
  the confinement must not add a `Close func() error` field. Resolving each open
  through `os.OpenInRoot` holds no descriptor and needs no such field.
- **The endpoint's constructor default admits.** A test harness that never wires
  `WithAdminAuth` takes the admin arm on every operation, so a fixture that
  wires nothing does not exercise the refusal. The refusal binds only where boot
  wires a denying arm.
- **CODE-4 flips green cells in an existing spec-annotated table.**
  `pkg/registry/server/layer_write_auth_test.go`'s `seedLayer` defaults to a
  `local` source with `LocalPath: "/tmp/seed"` (`:105-117`), and its owner
  `restore` and owner `reingest` cells assert `200` under a denying admin arm
  (`:136`, `:142`). Its register table (`:340`) and
  `TestLayerRegister_RecoveryWindowSequence` (`:461`) post local paths as a
  non-admin. Those cells must be moved onto a `git` source, or the change does
  not land green. Moving the cells alone is not enough: the helper's
  `LocalPath` default is not conditioned on the source type, so every `git` cell
  arrives carrying `/tmp/seed` and the new stored-`git`-with-a-path cell would
  assert nothing the default does not already produce. The helper's default is
  narrowed to a `local` source in the same step.
- **The integration lane carries the same seeded-local-source pattern.**
  `test/integration/layer_write_authorization_test.go` wires `WithAdminAuth`
  the way boot does on an identity-provider deployment (`:33-36`), grants admin
  to `ops@acme.com` alone (`:88`), seeds `alice-personal` and `org` as
  `SourceType: "local"` with a real `LocalPath` (`:99-100`), asserts that alice,
  a non-admin owner, reingests `alice-personal` at `http.StatusOK` (`:122`) and
  that `last_ingested_at` is stamped (`:143-145`), and re-registers a `local`
  source as a non-admin (`:160-162`). It is the only integration-level test of
  the write rule and a `// Spec: §7.3.1` citation site, so it moves in the same
  step as the unit file. Its fixture drives `localReingestRunner(st, nil)`
  (`:37`) over a real on-disk tree, so a cell moved onto a `git` source needs a
  reachable repository or a split into a non-local cell for the write rule plus
  a local cell asserting the new refusal.
- **`auth.forbidden` has no `errorCodeRegistry` entry**
  (`pkg/registry/server/error_envelope.go:24-104`), so its envelope reports
  `retryable: false` and an empty `suggested_action`. That is the value today
  for every `auth.forbidden` arm; this change does not add one, and the tests
  assert `retryable: false` rather than a hint.
- **The design corpus is two documents that must be rewritten together.**
  `web/design/README.md:27` states that the mockups are correct where the two
  disagree on a number, and `:7` states that the spec wins over the whole
  handoff. Neither line makes a board authoritative over the spec, so the ground
  for rewriting the boards is SPEC-7 itself: a board drawing a control the panel
  no longer renders contradicts the rendering rule §13.10 states. Commit
  63f7186 is the precedent that edits prose and boards in one commit.
- **The e2e `oidc-jwt` stack skips on darwin**
  (`test/e2e/auth_oidc_jwt_test.go:58-63`). The authenticated end-to-end cases
  use the `injected-session-token` harness
  (`test/e2e/authserver_harness_test.go:136-174`) instead, which needs one
  change: it hardcodes `serve --standalone` with no `--web-ui`, so the posture
  case needs the harness to accept extra serve arguments.
- **The withheld `Register layer` control leaves a dangling empty state.** The
  panel's empty state instructs the reader to press a control the rule now
  withholds from a caller who resolved no subject
  (`web/ui/src/surfaces/LayerPanel.tsx:599-600`), and that caller also reads an
  empty list (`pkg/registry/server/layers.go:252-268`), so the two states arrive
  together. Both that line and the sidebar's own "Register a layer to fill it."
  (`web/ui/src/App.tsx:463-469`) gain an arm keyed on the read having answered
  and on the same register call the control is gated on, rather than on the
  control's absence, because the control is also withheld where the read did not
  answer (D11) and because a registry that authenticates no caller reports no
  subject while still admitting the registration. The capability object
  and the subject report the same values in those two states, so the shell
  derives a `postureAnswered` boolean and threads it into the panel for those
  two arms alone.

## Implementation checklist

- [ ] **S1 · spec** — SPEC-2. §7.3.1 gains the local-source authorization
      paragraph and the ingest-confinement paragraph, and its `**Errors.**`
      paragraph gains the new arm. The file is
      `spec/07-external-integration.md`.
      Levels: —. Depends on: —
- [ ] **S2 · spec** — SPEC-3. §4.6's `local` source bullet and §4.7.2's
      authoring-rights paragraph each gain one pointer clause to the §7.3.1
      rule. The file is `spec/04-artifact-model.md`.
      Levels: —. Depends on: S1
- [ ] **S3 · spec** — SPEC-5. §7.3.4's posture read gains the
      `layer_capabilities` bullet, its opening sentence pair names it and states
      what a carried credential is verified for, and its closing sentence is
      amended. The file is `spec/07-external-integration.md`.
      Levels: —. Depends on: —
- [ ] **S4 · spec** — SPEC-7. §13.10's layer-panel bullet records the rendering
      commitment. The file is `spec/13-deployment.md`.
      Levels: —. Depends on: S3
- [ ] **S5 · code** — CODE-1, TEST-1, TEST-2. The confined tree constructor in
      `pkg/layer/source`, the three bootstrap call sites routed through it, the
      `SKILL.md` read's classification branch and the `Request.Files` comment in
      `pkg/registry/ingest`, and
      the provider-level and ingest-level cases that pin the refusal and its
      classification, including the `NewFromFilesystem` case that pins the
      `pkg/registry/server` site, which no invocation of the binary reaches. The
      pins on the two `internal/serverboot` sites land in S10, which is the level
      that reaches them.
      Levels: unit. Depends on: S1
- [ ] **S6 · code** — CODE-4, TEST-4. The local-source authorization rule, the
      host-path classifier, and the capability evaluator in one new file, the
      five call sites, the cases that pin all five, and the amendments to
      `pkg/registry/server/layer_write_auth_test.go` and
      `test/integration/layer_write_authorization_test.go`, whose seeded local
      paths the rule would otherwise refuse.
      Levels: unit, integration. Depends on: S1
- [ ] **S7 · code** — CODE-5, TEST-5, TEST-6. The posture read's capability
      seam, its default-closed disposition, the boot wiring that passes the
      endpoint's own evaluator into it, the body cases, and the case pinning
      that the reported capability and the enforced gate are one expression.
      Levels: unit, integration. Depends on: S3, S6
- [ ] **S8 · code** — CLI-1. The `--local` usage strings on `podium layer
      register` and `podium layer update` name the new constraint.
      Levels: —. Depends on: S6
- [ ] **S9 · code** — UI-1, UI-2, TEST-8. The posture type, the capability
      accessor, the `mayTake` predicate module, the register target constructor, the shell
      that derives the capability object and the `postureAnswered` flag once,
      threads them, and applies the register call to its own `catalogBare` line
      (`web/ui/src/App.tsx`), the panel
      and its empty-state arm, the deleted-layers surface, the register form,
      the update form, the regenerated bundle, and the client-side cases.
      Levels: unit. Depends on: S4, S7
- [ ] **S10 · test** — TEST-10. The boot wiring, the CLI refusal, the posture
      body, and the bootstrap ingest's confinement through the compiled binary on
      both the `--layer-path` arm and the declared-layer arm, plus the bundle
      assertion.
      Levels: e2e. Depends on: S5, S9
- [ ] **S11 · docs** — DOC-1. The local-source rule and the posture field on the
      HTTP API, error-codes, CLI, layers, and access-control pages, and the
      §4.7.2 disclaimer restatements on the concepts, clustered, and local
      pages.
      Levels: —. Depends on: S6, S7
- [ ] **S12 · docs** — DOC-2. The hand-run scenarios: S44's bootstrap-admin
      note, S47 steps 1 and 3 and its Covers line, S48, and S50's steps amended,
      S50's Goal paragraph, Covers line, and "Why by hand" paragraph rewritten,
      S55 to S57 added, and the scenario index rows.
      Levels: —. Depends on: S9
- [ ] **S13 · docs** — DOC-3. The `CHANGELOG.md` entry.
      Levels: —. Depends on: S9
- [ ] **S14 · docs** — DOC-4. Every site the claim, board, and prescription
      sweeps return takes its R1 to R5 rewrite or falls in a named exclusion
      class, across the brief, the inventory, the boards, and the client's
      comments, and the brief's domain browser section gains the one statement
      DOC-4 adds, which is the rule the shell's catalog line's new arm follows.
      Done when the three sweeps are re-run over the edited tree and
      every returned line is a line this commit edited or a member of an
      exclusion class.
      Levels: —. Depends on: S9

## Current state and the gap

**An authenticated non-admin can make the registry read a filesystem path of
their choosing and serve its contents back to them.**

No authorization governs the filesystem path a layer names. `register` copies
the request body's path into the persisted config with no validation
(`pkg/registry/server/layers.go:839`, `LocalPath: req.LocalPath`), after
checking only that `id` and `source_type` are non-empty and that the force-push
policy is valid (`:767-776`). `update` patches the same field on any non-empty
value (`:697-698`). A repository-wide search finds no allowlist, root
confinement, absoluteness check, or symlink resolution on that field anywhere in
`pkg/`, `cmd/`, or `internal/`. The stored value flows unmodified into
`source.LayerConfig.Path` (`pkg/registry/ingest/orchestrator.go:112`) and then
into `os.DirFS(cfg.Path)` (`pkg/layer/source/local.go:35`).

An authenticated non-admin reaches that write. On a previously-unused layer ID,
`register`'s only refusal is for a caller who resolves no verified subject
(`pkg/registry/server/layers.go:804-809`); such a caller is then resolved to a
user-defined layer with `cfg.Owner = caller.Sub` and
`cfg.Users = []string{cfg.Owner}` (`:845-867`). `reingest` is gated by
`authorizeLayerWrite` alone (`:1219`), which admits a user-defined layer's own
owner (`:321-328`), and `internal/serverboot/reingest.go:105-114` dispatches
`source.Local` for that layer. The artifact read then passes §4.6 because the
caller is in the layer's `Users` list (`pkg/layer/composer.go:83`, `:93-97`). No
§4.6 rule is violated at any step. The defect is that the caller used the
registry as a reader for a directory they were never granted.

**Confining the registered path alone would not close it.** `os.DirFS` does not
confine resolution. All three ingest walks discriminate solely on `d.IsDir()`
and on file base names and then call `fs.ReadFile`
(`pkg/registry/ingest/ingest.go:992-1015` `walkLayer`, `:1033-1055`
`walkDomains`, `:1151-1176` the bundled-resource walk), so a symbolic link
inside the tree is treated as an ordinary bundled file and read through. No
`os.Root` or `os.OpenRoot` use exists anywhere in the repository, and the
toolchain is go 1.26.3 (`go.mod:3`), where both are available. Enforcement
therefore belongs at the ingest, in the local provider and in the three other
sites that build the same tree.

**The spec is silent rather than contradicted.** §7.3.1 says nothing about where
a `local` layer's path may point; §4.6 describes a `local` source as "a
filesystem path readable by the registry process"
(`spec/04-artifact-model.md:596`); and §4.6 and §4.7.2 both close with
"Authoring rights are out of Podium's scope" (`:619`, `:796`), which answers who
may write content into a source the registry already reads rather than who may
make the registry read a path.

**The severity is stated precisely.** The read is bounded by the registry
process's uid. It does not let a caller mint an identity:
`PODIUM_RUNTIME_KEYS_PATH` carries each runtime's issuer, algorithm, and a PKIX
public key (`spec/06-mcp-server.md:78`,
`pkg/identity/runtime_persist.go:49-56`,
`pkg/identity/parse_pem.go:18-51`), and the trusted issuer names are already
printed to the startup log (`internal/serverboot/serverboot.go:1151`). What the
uid does reach on a standard deployment is the configuration carrying
`PODIUM_POSTGRES_DSN` (`spec/13-deployment.md:394`, `:551`), an environment file
carrying `PODIUM_TRUSTED_PROXY_SECRET`, and host TLS material. The primitive is
an arbitrary uid-bounded read, which is the defect regardless of which file is
named.

**Nothing reports the caller's role or write capability to a client.** §7.3.4
fixes the posture body to `identity_provider_configured`, `public_mode`,
`browser_auth`, and `subject` and closes it: "The response carries no other
field" (`spec/07-external-integration.md:188`). `SessionPosture` emits exactly
those (`pkg/registry/server/webui_session.go:20-59`), the TypeScript mirror
carries the same set (`web/ui/src/session.ts:11-24`), and no whoami or
capability route exists (`pkg/registry/server/webui_auth.go:17-20`). The write
decision lives only inside `authorizeLayerWrite`, which returns an error and
annotates no response.

**The panel therefore renders every write affordance on every row, gated on the
§13.2.1 read-only marker alone.** `Edit` (`web/ui/src/surfaces/LayerPanel.tsx:1239`)
and `Unregister` (`:1248`) carry `disabled: readOnly`; `ReingestControl` is
`disabled={readOnly || held === true || …}`
(`web/ui/src/surfaces/ReingestControl.tsx:135`); `DeletedLayers`'s `Restore` is
gated on `readOnly` alone and its props carry no identity
(`web/ui/src/surfaces/DeletedLayers.tsx:50-54`, `:251-255`, `:315-324`); and the
register form offers the admin-defined class
(`web/ui/src/surfaces/RegisterLayerForm.tsx:277-286`) and the `Local folder`
source (`:624-631`) to every caller. `readOnly` originates from the
`X-Podium-Read-Only` response header (`web/ui/src/api.ts:91`, `:125-131`), so it
carries no information about the caller. The server rules for editing a layer
you do not own and for reingesting an admin-defined layer are already correct
and already specified. Only the rendering is wrong.

That rendering is a recorded design rule, conditional on a fact this proposal
changes. `web/design/README.md:154` states: "No response reports that the caller
holds the administrator role, so the panel predicts no outcome: it renders its
write operations on every row and presents whatever refusal a write receives
rather than reading it as a failure of the page." The same clause appears at
`web/design/README.md:91`, `:93`, and `:146`, at `web/DESIGN.md:47`, `:52`,
`:292`, and `:489`, and in comments at
`web/ui/src/surfaces/LayerPanel.tsx:6-8`, `web/ui/src/session.ts:116-118`,
`web/ui/src/surfaces/RegisterLayerForm.tsx:58-63`, and
`web/ui/src/App.tsx:1268-1274`. Once the posture read reports the capability,
the rule's premise no longer holds and every restatement is rewritten with it.

Adjacent surfaces carry the same primitive. `update` patches `local_path` behind
the owner-admitting write gate (`:671`, `:697-698`), so a register-only rule is
defeated by one patch. `restore` re-authorizes against the tombstoned layer's
stored class and owner (`:1036`), so an admin's removal of a hostile layer is
undone by its owner inside the §8.4 window. A stored path is inert until
something re-reads it, and the reachable disclosure is the reingest.

## Decisions

**D1. The local-source rule has one arm, and no configuration key is added.** A
key naming permitted roots would be unset by default and would authorize nobody
by default, so it would ship dead in every default deployment and exist solely
to widen. The deployment it would serve is a non-admin registering a `local`
layer on a registry that configures an identity provider, which is the
deployment §4.6 says `local` is not for: "Intended for standalone and small-team
installations where the registry runs alongside the author"
(`spec/04-artifact-model.md:596`). On standalone and filesystem-registry modes
the admin arm already admits every caller, because a registry started with no
identity provider configured, or one started in public mode, authenticates no
caller (`spec/07-external-integration.md:97`), and `PODIUM_LAYER_PATH` bootstrap
layers are registered by the server rather than by a caller
(`spec/13-deployment.md:519`). An admin-arm-only rule therefore loses no
documented use case. If a deployment later turns up that needs a non-admin to
name a host path, the key is an additive MINOR change against a concrete case.

**D2. The refusal reuses `auth.forbidden` with a `details` discriminator.**
§6.10 states the house rule for this situation: the tenant-management endpoints
cover an authorization axis genuinely distinct from the per-tenant admin gate
and deliberately decline a new code, because "A request without operator
authorization is rejected with `auth.forbidden`, the code the per-tenant admin
endpoints already return" (`spec/06-mcp-server.md:476`). A new code would oblige
a §6.10 prose block and envelope, a `tools/matrix/matrices.go:83-115` axis entry
with an annotated test, a `docs/reference/error-codes.md` row, and an
`errorCodeRegistry` entry (`pkg/registry/server/error_envelope.go:112-123`)
without which the envelope reports an empty `suggested_action` by accident.
`writeErrorDetails` already carries a details map
(`pkg/registry/server/server.go:1454`) and is already used for machine-readable
discriminators (`pkg/registry/server/layers.go:902`, `:1303`). No client
branches on the code: the register form decides what to render from
`layer_capabilities` before any request is sent, the CLI dumps the raw body, and
the SDKs surface `code` as an opaque string.

**D3. The confinement and the authorization rule are both required, and neither
substitutes for the other.** The confinement does not close the disclosure,
because the caller names the root: a layer rooted at `/etc` reads `/etc`
legitimately. The authorization rule does not close the read of a path the
admin arm did admit, because `os.DirFS` reads through a link planted inside it,
including one planted after the layer was registered. The confinement is therefore stated in §7.3.1 rather than left to
configuration, over the ingests the staged confinement paragraph covers. Which
ingests those are, and which caller and which deployment modes the paragraph
binds, are that paragraph's to state, and D3 restates neither.

**D4. The confinement resolves each open through `os.OpenInRoot` and holds no
descriptor.** `Snapshot` is the `LayerSourceProvider` return value
(`pkg/layer/source/source.go:29-42`, `:53`), and §9.3 forbids "no Go channels,
no closures, no `func` types" on an SPI argument or return value
(`spec/09-extensibility.md:43`). A `Close func() error` field on `Snapshot` is
exactly that construct. Resolving per open needs no field, no orchestrator
change, and no close helper.

**D5. The confinement binds the three bootstrap sites as well as the provider.**
`internal/serverboot/serverboot.go:455` (filesystem-registry bootstrap),
`internal/serverboot/serverboot.go:616` (declared `local` layers from
`registry.yaml`), and `pkg/registry/server/server.go:338` each construct the
tree with `os.DirFS` and never call the provider. Confining the provider alone
would leave the same directory unconfined when it is bootstrapped, which is the
deployment-mode divergence `code-best-practices.md` forbids.
`pkg/registry/server/filesystem_resources.go:9-12` says in its own comment that
`dirFS` exists "so future changes (root-relative path normalization, symlink
restrictions) are applied consistently", so routing it through the shared
constructor is what that comment already asks for.

**D6. An `os.Root` refusal at ingest is reported with the existing
`ingest.source_unreachable`.** `pkg/layer/source/local.go:26-29` already records
that a permission failure is the same condition as a missing directory, and a
confinement refusal is a permission failure at the openat boundary. That the
code under-describes the condition is recorded as an accepted failure mode
below, with the evidence a later split would need, including that
`pkg/layer/source/source.go:89-91` documents the sentinel as "a transient fetch
failure" and sets `Retryable: true` on the SPI envelope while a confinement
refusal is permanent.

**D7. The posture read reports a per-capability object rather than a bare
`admin` boolean or a role string.** A boolean named for the role forces every
reader to reconcile two true statements: the installed callback admits
unconditionally when `cfg.publicMode || cfg.identityProvider == ""`
(`internal/serverboot/serverboot.go:1253-1262`) while `core.AdminAuthorize`
would refuse the same caller for holding no identity
(`pkg/registry/core/admin.go:22`). A role enumeration additionally puts the
§4.7.1 operator grant and the §4.7.2 tenant-admin grant into one namespace on a
read whose scope is one deployment's layer surface. The object carries one
member today, `manage_any_layer`, named for the gate.

**D8. Per-row `can_write` flags on the layer record were rejected.**
`store.LayerConfig` is the wire record for the CLI, the SDKs, and `podium sync`;
the flags would have to be recomputed on the soft-deleted arm and on the reorder
response body, which is the filter-every-arm footgun proposal 0015 closed twice;
and they do not help the register form, which has no row. The per-row question
is `manage_any_layer || (row.UserDefined && row.Owner === subject)`, which
mirrors `authorizeLayerWrite` clause for clause and which the client already
computes half of (`web/ui/src/surfaces/LayerPanel.tsx:1670-1673`).

**D9. Public mode takes the admin arm and this proposal adds no branch on it.**
The closure returns nil for every caller when
`cfg.publicMode || cfg.identityProvider == ""`
(`internal/serverboot/serverboot.go:1253-1262`, duplicated at `:2349-2358`), and
public mode is network-reachable via `--allow-public-bind` (`:1574-1577`). A
second expression of a deployment condition that has one canonical expression is
the drift proposal 0015 settled the same question to avoid. What bounds the exposure there is the confinement, which needs no
configuration, on the ingests the staged confinement paragraph covers, and the
registry process's own rights everywhere else. The
residual goes to the reviewer as OQ-1.

**D10. The capability evaluator and the write gate are one expression by
construction.** `Capabilities` is a method on `*LayerEndpoint` reading
`e.authAdmin`, the same field `authorizeLayerWrite` reads (`:327`) and the same
field `readableBy` reads (`:254`). `internal/serverboot` passes the method value
into the posture literal, which binds it to the endpoint holding the one closure
`WithAdminAuth` installed. No local variable or second closure is introduced.

**D11. Where the posture read did not answer, the client holds no capability and
renders no layer write control.** The layer endpoint's constructor default
admits (`pkg/registry/server/layers.go:190`), so the posture read's seam defaults
the other way: an unwired gate on a reporting surface withholds.

**D12. The client predicts and the registry authorizes.** A control whose
availability depends on the caller is absent where the posture read and the
target settle that this caller does not obtain its outcome, and the reordering
affordance is the one such control presented disabled instead, because it also
states the row's position (D13). A control the §13.2.1 read-only mode suspends
for every caller stays present and is disabled, with the state named once in the
read-only banner. A control whose availability turns on the layer record alone,
with no dependence on the caller, is decided where it is decided today and is
untouched. Presence is
decided by capability, and the present controls are then disabled by `readOnly`.
The refused-write state stays drawn, because a prediction can go stale.

The client mirrors every predicate except `isFileTransportRepo`. A second
implementation of a fail-closed classifier in another language drifts from the
Go one, and drifting toward absence hides a control from a caller who holds it,
so `namesHostPath` drops the `Repo` disjunct and the predicate admits wherever
the refusal turns on it. That is one exception with two arms, because the server
classifies `Repo` on `register`, `restore`, and `reingest`: a `git` row whose
stored repository resolves to a host path keeps its reingest and restore
controls, and the register dialog keeps offering the `git` source to every
caller whatever repository string they type. Both take the registry's refusal,
drawn on the row for the first arm and in the dialog for the second. SPEC-7's
staged §13.10 text names this as a refusal the target's own fields do not settle
rather than leaving it as an undeclared exception, and the presentation site
follows from "which the panel presents where the operation was attempted".

**D13. The reordering affordance is the rule's disabled exception, and its
target is the block rather than the row.** It is one of the two exceptions
SPEC-7's staged text names, and it is stated there once rather than per site.
The panel already carries a disabled reordering arm: the drag
handle reads `disabled={readOnly}` and `draggable={!readOnly}` with an
`aria-label` ternary naming the read-only reason
(`web/ui/src/surfaces/LayerPanel.tsx:1150-1159`), the footer's `reorderable`
local gates the reorder note (`:758`, `:769-776`), and the `precedence-label`
carries the matching arm (`:584-589`). UI-2 extends all three.

The predicate is evaluated over the layers a move from that row would reorder,
which is that row's own class block rather than the whole visible set. The panel
sends `movedOrder(blockOf(rows, from), from, onto)` on the pointer path
(`web/ui/src/surfaces/LayerPanel.tsx:478`) and reaches the same `blockOf` on the
keyboard path (`:489-500`), and `blockOf` filters `rows` to the run sharing the
dragged row's `UserDefined` class (`:846-854`), which the panel states to the
reader at `:592-595`. The reorder handler authorizes every id the request names
and refuses the whole call on the first failure
(`pkg/registry/server/layers.go:1124-1136`, spelled with its path because the
bare-colon citations in this paragraph name
`web/ui/src/surfaces/LayerPanel.tsx`, whose own `:1123-1136` is the row's
drag-and-drop event wiring), so the client predicate and the server enforcement
agree only when both range over the block.

Reading the predicate over the whole visible set would withhold an affordance the
caller can exercise. A user-defined layer carries implicit `users: [<registrant>]`
visibility (`spec/07-external-integration.md:95`), so the user-defined block a
non-admin sees holds that caller's own layers alone and `authorizeLayerWrite`
admits the owner on each (`pkg/registry/server/layers.go:321-329`); that caller
reorders their own block today, and one admin-defined row elsewhere in the list
must not take the handle away from them. The disabled arm is the block the caller
cannot wholly write, which for a non-admin is the admin-defined block: the caller
could reorder such a set if they held the admin arm, so this is a control they
could take rather than one they can never take.

**D14. Podium is pre-1.0.** No flag, key, or dual code path restores an
unconfined non-admin local registration, and `SessionPosture` grows its field
with no version negotiation.

## Spec amendment: §7.3.1 local-source authorization

**SPEC-2.** Anchor: `spec/07-external-integration.md`, §7.3.1. Two paragraphs
land immediately after the paragraph beginning `**Layer read visibility.**`
(`spec/07-external-integration.md:99`) and immediately before the paragraph
beginning `**Errors.**` (`:101`). The layer write authorization paragraph, the
layer read visibility paragraph, the user-defined-layer paragraph, the command
list, and the ingestion-trigger material are untouched.

The inserted paragraphs:

> **Local-source authorization.** Registering a layer whose source type is
> `local` or whose registration names a filesystem path on the registry host,
> patching a stored layer's filesystem path, restoring a stored layer that names
> one, and reingesting one are authorized to a tenant admin holding the §4.7.2
> admin role. A registry started with no identity provider configured, or one
> started in public mode (§13.10), authenticates no caller, so no caller can hold
> the admin role and these operations are admitted there, on the same reading the
> layer write authorization rule above states for its own arms. Any other caller
> is refused with `403 auth.forbidden` (§6.10) carrying
> `details.constraint: "local_source"`, and the refusal names no filesystem path.
> The rule applies to a user-defined and to an admin-defined layer alike, and it
> is evaluated on each of those operations rather than against the stored layer
> list, so a layer stored before this rule was in force is refused at its next
> such operation rather than at startup. An inbound webhook delivery triggers a
> reingest and is governed by this rule on the same arm. The delivery carries the
> per-layer secret rather than a caller the registry can place on the admin arm,
> so on a registry that authenticates its callers a webhook-triggered reingest of
> a layer that names a filesystem path is refused; on a registry started with no
> identity provider configured, or one started in public mode, the layer
> endpoints admit the request there as above. The operations
> `unregister` and
> `reorder` name no filesystem path and re-read none, so the rule does not reach
> them. A `git` source whose repository string names a network endpoint is
> fetched through a network transport and yields tree objects rather than host
> files, so the rule does not reach it. A `git` source is placed on this rule's
> arm by its repository string alone: the Git transport reads the repository
> string, the ref, and the root, so a filesystem path stored beside a `git`
> source is never read and does not place a restore, a reingest, or a
> webhook-triggered reingest on the arm. A patch is classified on the fields it
> carries, so a patch naming a filesystem path is on the arm whatever the stored
> layer's source type.
> A `git` source whose repository string
> resolves to the Git file transport names a repository path on the registry host
> and is governed by this rule on the same arm, and a repository string the
> registry cannot place as a network endpoint is treated as naming a host path.
>
> **Local-source ingest confinement.** Whichever caller declared it, an ingest
> that reads a layer's configured filesystem path as a directory reads only
> within the directory that path resolves to. A path that leaves that directory, including one reached through a
> symbolic link stored inside it, is not read. A read the ingest requires and
> cannot satisfy fails that layer's ingest, which reports the layer as
> unreachable (`ingest.source_unreachable`, §6.10). No artifact and no bundled
> resource from that cycle is accepted, so the artifacts served before the
> refusal stay in place until the layer is restructured. A `DOMAIN.md` the
> refused cycle read before the failing read is persisted, because the domain
> composition is committed ahead of the artifact walk, and it emits its §8.1
> `domain.published` event only where that `DOMAIN.md` was added or changed
> since the previous ingest, so a cycle the confinement refuses repeatedly over
> an unchanged domain emits no further event. A relative symbolic link that resolves within the directory
> is read. A symbolic link whose target is an absolute path is not read, whatever
> that target names, because an absolute target names a location from outside the
> directory the confinement bounds.
> The confinement binds every deployment mode (§13.11), including a layer the
> operator declares in the registry's own configuration, and it is not
> configurable. A `git` source whose repository string resolves to the Git file
> transport is fetched through that transport rather than read as a directory,
> so this confinement is not engaged on that arm and the local-source
> authorization rule above is the control that governs it.

The paragraph above is the only statement in this proposal of which ingests the
confinement covers and which it does not. Every other site in this document
refers to "the ingests the staged confinement paragraph covers" and states only
the outcome that site needs. No other site enumerates the covered ingests, and
no other site carries the `git` file-transport sentence that states the
carve-out. Narrowing or widening the population is an edit to this paragraph and
to nothing else. The rule reaches the population and the carve-out alone: a site
may still name the caller the confinement binds or the deployment modes it
covers where its own outcome needs one, which is what the summary's §7.3.1
bullet does for the caller, and what the summary's CODE-1 bullet, D5, and
CODE-1's routing paragraph do for the deployment modes, because each of those
three is about which call sites the constructor is routed through. The staged
content for shipped files is outside this convention: `docs/deployment/layers.md`
in DOC-1 and the hand-run scenarios in DOC-2 are reader-facing mirrors and state
the rule in the spec's own words, because a citation of a proposal paragraph is
meaningless in a shipped file.

SPEC-2 stages one further edit, on §7.3.1's `**Errors.**` paragraph
(`spec/07-external-integration.md:101`). The paragraph ends today with "and
layer writes attempted by a caller whom the layer write authorization rule above
authorizes on neither arm (`auth.forbidden`)". The replacement ends:

> …and layer writes attempted by a caller whom the layer write authorization
> rule above authorizes on neither arm (`auth.forbidden`), and a registration, a
> filesystem-path patch, a restore, or a reingest of a layer that names a
> filesystem path on the registry host attempted by a caller the local-source
> authorization rule above does not authorize (`auth.forbidden`, carrying
> `details.constraint: "local_source"`).

No §6.10 code is added. The refusal is an authorization outcome, and §6.10
already states that an authorization outcome on a distinct axis reuses
`auth.forbidden` (`spec/06-mcp-server.md:476`). The confinement paragraph names
`ingest.source_unreachable`, which §6.10 already carries.

## Spec amendment: §4.6 and §4.7.2 point at the local-source rule

**SPEC-3.** Anchors: `spec/04-artifact-model.md:596`, the `local` bullet in
§4.6's source-type list, and `spec/04-artifact-model.md:796`, the paragraph in
§4.7.2 beginning "**Authoring rights are out of Podium's scope.**". Neither
paragraph states a rule; each gains a pointer to the one normative home in
§7.3.1. The §4.6 Visibility paragraph at `:619` is not edited: it closes a
read-side subsection, and a reader seeking an authorization answer lands in
§4.7.2.

The `local` bullet at `:596` ends today with "Intended for standalone and
small-team installations where the registry runs alongside the author." One
sentence is appended:

> Which caller may declare a layer that names such a path is governed by the
> §7.3.1 local-source authorization rule.

The §4.7.2 paragraph at `:796` ends today with "Podium reads no in-repo
permission files." Two sentences are appended:

> That scope statement is about writing content into a source the registry
> already reads. Which caller may declare a layer that makes the registry read a
> given filesystem path is governed by the §7.3.1 local-source authorization
> rule, because the registry process reads that path with its own rights rather
> than with the registrant's.

## Spec amendment: §7.3.4 posture read reports the caller's layer capabilities

**SPEC-5.** Anchor: `spec/07-external-integration.md`, §7.3.4. The edits are the
opening sentence pair at `:181`, one bullet appended to the list at `:183-186`,
and the closing paragraph at `:188`.

The paragraph at `:181` opens today with "`GET /v1/ui/session` reports the
deployment's identity posture and the caller's own resolved subject. It requires
no credential and refuses no request for lack of one; a request that carries one
has it verified only so the response can report `subject`, and a request that
resolves no subject is answered `200` with `subject` absent." Both sentences are
replaced, because the second states that a carried credential is verified for
reporting `subject` alone, and the capability the same amendment adds is the
second thing that credential decides: `Capabilities` reads the `authAdmin`
callback, which on a deployment that configures an identity provider is
`registry.AdminAuthorize(r.Context(), layerIdentity(r))`
(`internal/serverboot/serverboot.go:1253-1262`), over the same verifying resolver
the posture read passes as `Identity` (`:1324`). The replacement:

> `GET /v1/ui/session` reports the deployment's identity posture, the caller's
> own resolved subject, and what that caller may do on the §7.3.1 layer
> operations. It requires no credential and refuses no request for lack of one;
> a request that carries one has it verified so the response can report
> `subject` and evaluate `layer_capabilities`, and for no other purpose, and a
> request that resolves no subject is answered `200` with `subject` absent.

The bullet lands after the `subject` bullet, last in the list:

> - `layer_capabilities`: an object reporting what the requesting caller may do
>   on the §7.3.1 layer operations on this deployment. It carries
>   `manage_any_layer`, a boolean reporting whether this deployment's layer
>   endpoints admit this caller on the §4.7.2 admin arm, which is the arm that
>   decides a write on a layer the caller does not own and every operation the
>   §7.3.1 local-source authorization rule governs. On a registry started with no
>   identity provider configured, or one started in public mode (§13.10), those
>   endpoints admit every caller on that arm, so the member is true there,
>   including on a request that resolves no subject. The object and its member
>   are always present. Where the deployment determines no capability for the
>   request, the member is false, which is the closed default a reporting surface
>   takes. The object reports a snapshot taken when the read was answered: an
>   operation a client offers on the strength of it can still be refused, and the
>   §6.10 envelope the operation's own endpoint returns remains the authority.

The closing sentence today reads "The response carries no other field, and in
particular no issuer, client identifier, endpoint, or other configuration
value." The replacement:

> The response carries no other field, and in particular no issuer, client
> identifier, endpoint, filesystem path, or other configuration value, and no
> subject or authorization belonging to any caller other than the one that asked.

The remainder of that paragraph, which states what the read discloses and where
it is registered, is unchanged.

## Spec amendment: §13.10 layer panel rendering commitment

**SPEC-7.** Anchor: `spec/13-deployment.md:168`, the `**Layer panel**` bullet in
§13.10's surface list. The bullet today ends "The UI is a thin client over the
same `podium layer …` HTTP endpoints." The following sentences are appended:

> The panel renders a control that would take one of the §7.3.1 layer write
> operations only where the §7.3.4 posture read and the fields carried by that
> operation's target settle that the §7.3.1 rules admit this caller on it. The
> target is the layer as the operation would name it: the stored layer for
> `unregister`, `restore`, `reingest`, and `reorder`; for `update` the stored
> layer's class and owner together with the fields the patch would carry; and
> for `register` the registration the dialog would build, whose class is the
> class that dialog asks for and whose owner is the registrant. The panel reads
> the target's class, its stored owner, its source type, and its filesystem
> path, and it reads no other field and predicts no other rule. A control that
> names a value the registry resolves away rather than refuses, such as the
> class of a registration, is withheld on the same reading, because the caller
> is not admitted to the operation as that control would name it. A control
> whose request the panel narrows to the layers the rule admits is rendered
> where the rule admits the caller on at least one of them, and it acts on that
> subset alone. The rule governs every such control the panel renders,
> including the controls inside its registration and update dialogs and the
> controls on its recovery table, and the §13.2.1 read-only marker then
> disables whatever the rule leaves present.
> A control whose availability turns on the layer record alone,
> with no dependence on the caller, is outside this rule. Two arms are
> exceptions. A reordering affordance is presented disabled with its reason
> named rather than removed, and it is settled over every layer a move from the
> row carrying it would reorder rather than over that row alone, because the
> request names all of them and the registry refuses it whole. A refusal the
> target's own fields do not settle, such as a `git` repository string that
> resolves to the Git file transport, is presented and answered by the
> registry. The registry's refusal remains authoritative: the panel predicts
> and authorizes nothing, and an offered operation can still be refused
> (§7.3.4), which the panel presents where the operation was attempted.

The staleness explanation itself is stated once, in §7.3.4's
`layer_capabilities` bullet, and the appended text cross-references it.

The rule branches on the posture read's `manage_any_layer`, on whether that
read resolved a subject, on the target's class, on its stored owner against
that subject, on whether the target names a filesystem path, and on the
operation the control would take. The reordering exception branches
additionally on whether every layer in the block a move would reorder passes
the same rule, and the unsettleable-refusal exception on whether the target's
own fields settle the refusal. Under §11's web UI verification matrix those
are the variables the statement's own text branches on, so the layer panel
unit's Render cell is driven at the values of each rather than at a list of
surfaces. No control the panel renders carries a condition point of its own,
and a control added later takes its points from this list unchanged, which is
what §11 means by a variable generating its points whether or not it is named
there (`spec/11-verification.md:75`). The registration dialog, the update
dialog, and the recovery table are part of the layer panel unit, which is the
surface the brief names (`web/DESIGN.md:286`, `:335-373`). A posture read that
did not answer is not a separate point for the controls: UI-1's accessor
collapses it onto `manage_any_layer: false` (D11), and TEST-8 pins that collapse
once on the accessor. It is a point for two empty-state copies, which the staged text governs in
neither case and which UI-2 keys on the `postureAnswered` flag on both. The
first is the panel's own empty state, whose arms are points of the layer panel
unit's Render cell. The second is the shell's catalog line
(`web/ui/src/App.tsx:463-469`), which belongs to the domain browser rather than
to the panel, the surface the brief names at `web/DESIGN.md:127`, so its arms
are points of the domain-browser unit's Render cell. That unit's statement is
its section in the brief, and DOC-4 states the rule there in the root-state
paragraph, so the register call and the `postureAnswered` flag are variables
that section's own text branches on and generate their points for that cell
(`spec/11-verification.md:75`). TEST-8 pins both arms of each copy and supplies
both cells.

## Proposed solution

### CODE-1: confine a local layer's tree to its own directory

`pkg/layer/source` gains one exported constructor and one unexported type. The
type resolves each open through `os.OpenInRoot`, which refuses at the openat
boundary rather than by comparing path strings, so a symbolic link planted after
registration is refused as well. It holds no descriptor, so `Snapshot` gains no
field, `pkg/registry/ingest/orchestrator.go` is untouched, and §9.3's constraint
on SPI return values is not engaged.

```go
// ConfinedFS returns a read-only tree rooted at root that refuses any path
// resolving outside it. os.DirFS validates a path string and confines no
// resolution, so a symbolic link stored inside the tree reads its target
// wherever that target lives; every ingest walk treats such an entry as an
// ordinary bundled file and reads it through. A relative link resolving inside
// the tree still reads; os.Root refuses a link whose target is absolute
// whatever that target names, because it resolves a target from the root
// rather than from the filesystem, so an absolute link inside the tree stops
// being readable here. Every failure other than
// fs.ErrNotExist is returned wrapping ErrSourceUnreachable, which is what
// classifies a refusal by the time it reaches the reingest endpoint; an absent
// path keeps fs.ErrNotExist unchanged, because the ingest's SKILL.md read
// distinguishes a file that is not there from one it may not read.
//
// Spec: §7.3.1 (local-source ingest confinement)
func ConfinedFS(root string) fs.FS { return rootFS{root: root} }

// rootFS satisfies fs.StatFS, fs.ReadDirFS, and fs.ReadFileFS, which are the
// interfaces the ingest walks take. A symlinked root is admitted: os.OpenInRoot
// resolves its own root argument normally and confines only what lies beneath.
type rootFS struct{ root string }
```

`Open`, `Stat`, `ReadDir`, and `ReadFile` each call `os.OpenInRoot(f.root, name)`
and operate on the returned file. `Open` returns a value satisfying
`fs.ReadDirFile` for a directory, which is what `fs.WalkDir` needs.

The confinement refuses one link the layer's own directory holds legitimately,
and the refusal is stated rather than worked around. `os.Root` follows a relative
symbolic link that resolves inside the root and refuses one whose target is
absolute, whatever that target names: the documentation states "Symbolic links
must not be absolute" (`$(go env GOROOT)/src/os/root.go:43`), and
`splitPathInRoot` returns `errPathEscapes` on a target beginning with a path
separator before the target is compared against the root (`src/os/root.go:301-306`,
called on every followed link target from `src/os/root_openat.go:363`). A link at
`pkg/abs.txt` naming the root's own `shared/inside.txt` by absolute path is
therefore answered `openat pkg/abs.txt: path escapes from parent` on go1.26.3,
while the same link written relative reads through. Under the classifier below
that refusal is not `fs.ErrNotExist`, so it is wrapped in
`source.ErrSourceUnreachable` and fails the layer's whole ingest on the same path
as an escaping link. Absolute intra-tree links are ordinary in directories a
build or a package manager produced, so this is a behavior change for an existing
operator: the edge-case table carries its row, TEST-1 pins it, `CHANGELOG.md`
names it in the operator action, and `docs/deployment/layers.md` states that such
a layer is restructured to relative links. Admitting an absolute target that
happens to resolve inside the root would mean resolving it outside `os.Root` and
comparing the result against the root, which is the string comparison this change
exists to stop making.

The tree classifies every `os.OpenInRoot` failure, because the ingest reads
through `fs.FS` and has no other way to tell a refusal from an ordinary read
failure. Go exposes no sentinel for a confinement refusal: `os.OpenInRoot`
returns an `*fs.PathError` wrapping the unexported `errPathEscapes`
(`$(go env GOROOT)/src/os/file.go:421`, returned at `src/os/root.go:306` and
`src/os/root_openat.go:320`), which satisfies neither `fs.ErrNotExist`, nor
`fs.ErrPermission`, nor `os.ErrInvalid`, so no `errors.Is` test isolates it and
the only direct test would be a match on an unexported standard-library message
that carries no compatibility promise. The classifier therefore discriminates on
the one condition that is exported and that the ingest already depends on: where
the error satisfies `errors.Is(err, fs.ErrNotExist)` it is returned unchanged,
and every other failure is returned wrapped in `source.ErrSourceUnreachable`,
with a message naming the refused path relative to the root and no host path. A
confinement refusal and an in-root permission failure are classified alike, which
is the disposition `pkg/layer/source/local.go:26-31` already states for the root
itself: a permission failure is the same condition as a missing directory. An
absent in-root file keeps `fs.ErrNotExist`, which is what lets `loadOne` keep its
`missing SKILL.md` message on the absent-file case that TEST-2's second arm pins.
The walks and the
`ARTIFACT.md` read pass the error they receive up unchanged
(`pkg/registry/ingest/ingest.go:1011-1013`, `:1122-1124`, `:1170-1173`), and
`writeReingestError` matches the sentinel and answers
`502 ingest.source_unreachable` (`pkg/registry/server/layers.go:1361-1362`).

`pkg/registry/ingest` takes two edits. The first is on the read the wrap would
otherwise not survive. The second is the `Request.Files` comment at
`:198-200`, which names `os.DirFS` as what the Local `LayerSourceProvider`
produces the snapshot from and is false once `pkg/layer/source/local.go:35`
returns `source.ConfinedFS`. `loadOne`'s `SKILL.md` read, required for a `type: skill` artifact,
discards the returned error and substitutes a bare message that wraps nothing
(`pkg/registry/ingest/ingest.go:1139-1142`,
`fmt.Errorf("%s: type: skill missing SKILL.md", id)`), so a confinement refusal
on a `SKILL.md` reached through an escaping link would lose the sentinel, fall
to `writeReingestError`'s `default` arm, and be answered
`500 registry.unavailable` with `retryable: true`
(`pkg/registry/server/layers.go:1365-1366`,
`pkg/registry/server/error_envelope.go:118`), which is a permanent condition
reported as retryable and the exact misclassification the wrap exists to
prevent. The read gains one branch: where the error satisfies
`errors.Is(err, source.ErrSourceUnreachable)`, which is the sentinel the same
function already names on its walk callback (`:1151-1156`), it is returned wrapped
with `%w` and with the artifact id and `SKILL.md` named; every other error, an
absent `SKILL.md` included, keeps the existing
`"%s: type: skill missing SKILL.md"` message verbatim.

That message is duplicated with no shared constant in the independent
filesystem walker at `pkg/registry/filesystem/walk.go:195`, which this change
does not touch. The walker is the producer the `podium lint --registry` path
reaches (`cmd/podium/main.go:941`, `:953`), so
`test/e2e/artifact_types_test.go:634-640`,
`test/e2e/frontmatter_schema_test.go:100-104`,
`test/e2e/quickstart_flow_test.go:159`, `:166`, `:169`, and
`pkg/registry/filesystem/walk_test.go:102-105` all assert the walker's copy and
stay green whichever branch the ingest copy takes. The ingest copy at `:1141` is
asserted by nothing today, and TEST-2's second arm is what newly pins it. The
package appears in step S5 for those two edits alone. Without the wrap the
refusal reaches the handler unclassified and is coded `registry.unavailable` at
HTTP 500 with `retryable: true`
(`pkg/registry/server/error_envelope.go:26-29`), which is the failure TEST-2
pins against.

`walkDomains` discards any `DOMAIN.md` read error and continues
(`pkg/registry/ingest/ingest.go:1050-1053`), which it already does for a
permission failure. A `DOMAIN.md` reached only through an escaping link is
therefore absent from the ingest rather than failing it, which is why the staged
confinement paragraph scopes the unreachable report to a read the ingest
requires. The edge-case table carries the row.

`pkg/layer/source/local.go:22-38` keeps its existing reachability guard, so an
absent or unreadable root still returns `ErrSourceUnreachable` and the §6.10
disposition at `:26-29` is preserved, and returns `Files: ConfinedFS(cfg.Path)`
in place of `os.DirFS(cfg.Path)`.

Three further sites build the same tree themselves and are routed through the
same constructor, so the confinement binds every deployment mode:

- `internal/serverboot/serverboot.go:455`, the filesystem-registry bootstrap.
- `internal/serverboot/serverboot.go:616`, a `local` layer declared in
  `registry.yaml`.
- `pkg/registry/server/server.go:338`, through `newDirFS` in
  `pkg/registry/server/filesystem_resources.go:9-19`, whose own comment says it
  exists so symlink restrictions are applied consistently. `dirFS` becomes a
  thin wrapper over `source.ConfinedFS` rather than a second implementation.
  `dirFS.Open` is a bare `os.Open(filepath.Join(d.root, filepath.FromSlash(name)))`
  (`pkg/registry/server/filesystem_resources.go:18-19`), so it validates no path
  string at all and is weaker than the `os.DirFS` the other three sites use.

**IMPLEMENTOR'S CHOICE:** whether `dirFS` keeps its own type or becomes an alias
for what `source.ConfinedFS` returns. Any answer must leave exactly one
confinement implementation in the module, so a later change to it cannot reach
one caller and miss another.

The covered population is a call-site set rather than a runtime predicate.
Nothing in `rootFS` reads a source type, a request field, or a configuration
key, so there is no state to set, none to clear, and no arm on which the
confinement fails to fire. After this change `source.ConfinedFS` is the module's
only constructor of an `fs.FS` over a host directory and has exactly four
callers: `pkg/layer/source/local.go:35`, `internal/serverboot/serverboot.go:455`,
`internal/serverboot/serverboot.go:616`, and
`pkg/registry/server/filesystem_resources.go:15`, reached from
`pkg/registry/server/server.go:338`. The only other `fs.FS` that reaches
`ingest.Request.Files` (`pkg/registry/ingest/orchestrator.go:148`) is
`pkg/layer/source/git.go:93`'s `gitTreeFS`, which walks go-git tree objects out
of an in-memory storer (`git.go:47-56`) and opens no host path on any repository
string. Losing the confinement therefore takes adding a fifth producer of a
host-directory tree, and the review-time check against that is
`grep -rn "os.DirFS" --include='*.go' pkg cmd internal` returning no call
outside a test. Three comment hits stand beside the calls today, and one of
them states what this change falsifies: `pkg/registry/ingest/ingest.go:198-200`
reads "The Local LayerSourceProvider produces this from os.DirFS", which is
false once `pkg/layer/source/local.go:35` returns `source.ConfinedFS`, so
CODE-1 rewrites that clause to name `source.ConfinedFS` and leaves the
comment's sentence about the Git provider exposing the checked-out tree the
same way as it is. What the expression returns after this change is the two
`pkg/registry/server` comments, `filesystem_resources.go:9-10` and
`filesystem_resources_test.go:9`, each describing that package's own `dirFS`
against `os.DirFS` and neither a call. The four sites are pinned
behaviorally by TEST-1's `TestLocal_BootstrapTreeIsConfined`, TEST-10's two
arms, and TEST-2's `TestNewFromFilesystem_IngestIsConfined`, so no invariant
test is added.

The retryability of the refusal is recorded rather than changed.
`pkg/layer/source/source.go:89-91` documents `ErrSourceUnreachable` as "a
transient fetch failure" and sets `Retryable: true` on the SPI envelope. A
confinement refusal is permanent. CODE-1 amends that comment to name the
permanent arm and leaves the flag alone; the HTTP envelope is unaffected, because
`ingest.source_unreachable` carries no `errorCodeRegistry` entry
(`pkg/registry/server/error_envelope.go:24`) and the envelope therefore reports
`retryable: false` today.

### CODE-4: the local-source authorization rule and the capability evaluator

One new file, `pkg/registry/server/layer_capabilities.go`, because `layers.go` is
already long and this is one concern. It carries three things.

```go
// LayerCapabilities reports what a caller may do on the §7.3.1 layer
// operations on this deployment. It is reported by the §7.3.4 posture read and
// is a prediction: the endpoint that runs the operation authorizes it.
//
// Spec: §7.3.4
type LayerCapabilities struct {
    ManageAnyLayer bool `json:"manage_any_layer"`
}

// Capabilities evaluates the caller's layer capabilities from the same
// authAdmin callback authorizeLayerWrite takes its admin arm from, so the value
// a client renders on and the gate this endpoint applies are one expression.
//
// Spec: §7.3.4
func (e *LayerEndpoint) Capabilities(r *http.Request) LayerCapabilities {
    return LayerCapabilities{ManageAnyLayer: e.authAdmin(r) == nil}
}

// namesHostPath reports whether an operation names or re-reads a filesystem
// path on the registry host. A stored layer of a custom §9.1 source type that
// carries a path is included, because the orchestrator hands that path to the
// provider whatever the source type says.
//
// A "git" source is classified on its repository string alone. Git.Snapshot
// reads Repo, Ref, and Root and never the configured path
// (pkg/layer/source/git.go:39-97), while the orchestrator copies LocalPath into
// the source config for every source type
// (pkg/registry/ingest/orchestrator.go:112), so a stored git layer carrying a
// path reads none of it. Such a layer is producible today, because register
// copies req.LocalPath into the config with no source-type condition
// (layers.go:835-839) and update assigns cfg.LocalPath on any layer
// (layers.go:697-698). Refusing it would confine nothing and would answer every
// webhook delivery for that layer 403 permanently, with no self-service
// recovery, because update treats an empty local_path as "leave unchanged".
//
// Spec: §7.3.1 (local-source authorization)
func namesHostPath(sourceType, localPath, repo string) bool {
    return sourceType == "local" ||
        (sourceType != "git" && localPath != "") ||
        isFileTransportRepo(repo)
}

// isFileTransportRepo reports whether a git repository string resolves to
// go-git's file transport rather than to a network transport. go-git's default
// protocol map registers "file" alongside http, https, ssh, and git
// (go-git/v5 plumbing/transport/client), and its file client runs
// git-upload-pack against the named path, so "/srv/other-tenant" clones a host
// directory. Git.Snapshot validates nothing about cfg.Repo
// (pkg/layer/source/git.go:39-97), so this is where the classification lives.
//
// It asks go-git rather than restating go-git's parser: transport.NewEndpoint
// is the same disambiguation Git.Snapshot's clone reaches, so a string this
// classifier admits is a string go-git does not resolve to a host path. A
// string go-git cannot parse at all is treated as a host path, and an empty
// string is classified as nothing, because an empty repo names no path and the
// caller's other fields decide the arm.
//
// Spec: §7.3.1 (local-source authorization)
func isFileTransportRepo(repo string) bool {
    if repo == "" {
        return false
    }
    ep, err := transport.NewEndpoint(repo)
    if err != nil {
        return true
    }
    return ep.Protocol == "file"
}
```

`transport` is `github.com/go-git/go-git/v5/plumbing/transport`, already a module
dependency through `pkg/layer/source/git.go`, so no dependency is added.

The classifier mirrors no prose rule of its own, because a prose rule drifts
from the transport it is meant to predict. go-git's `NewEndpoint` tries the
scp-like form, then the local-path form, then a URL parse
(`plumbing/transport/common.go:209-216`), and the scp-like arm makes the user
prefix optional and rejects the scp reading whenever the segment before the
first `:` carries a `/`
(`internal/url/url.go:13`, `:37`), which sends `/srv/repos@h:x` to `parseFile`
and to `Protocol: "file"` (`plumbing/transport/common.go:302-316`). A literal
predicate over `user@host:path` classifies that same string as a network
endpoint and admits it, while `file.DefaultClient` runs `git-upload-pack`
against the host path (`plumbing/transport/client/client.go:21`,
`plumbing/transport/file/client.go:18-35`), which is a bypass on the axis
§7.3.1 names as safe. Calling the parser removes the divergence in both
directions: `host:path` with no `user@` is ssh to go-git and is admitted here
too.

A scheme go-git parses but does not resolve, such as `s3://bucket/x`, is
admitted by this classifier and refused by go-git's client registry before any
path is opened, so it reads no host file. A string `NewEndpoint` rejects
outright is refused here, which is the fail-closed arm.

An empty `repo` is not classified, so a patch that names no repository and no
filesystem path is not refused by this rule. A `git` registration that names an
empty `repo` is refused on the ingest path by `Git.Snapshot`'s own
`ErrInvalidConfig`, which is where that validation already lives.

The rule itself refuses with the existing code and the discriminator:

```go
// authorizeLocalSource refuses a caller the §4.7.2 admin arm does not admit on
// any operation that names or re-reads a filesystem path on the registry host.
// It runs after the write gate on update, restore, and reingest. On a register
// whose ID names no stored layer it is the only refusal for an authenticated
// caller, because the coarse gate there refuses only a caller who resolves no
// verified subject, so this is the arm that closes the arbitrary read.
//
// Spec: §7.3.1
func (e *LayerEndpoint) authorizeLocalSource(w http.ResponseWriter, r *http.Request, sourceType, localPath, repo string) bool
```

On refusal it writes
`writeErrorDetails(w, http.StatusForbidden, "auth.forbidden", msg, map[string]any{"constraint": "local_source"})`
and returns false. The message names the constraint and the remedy in prose and
names no filesystem path.

The five call sites:

| Operation | Where | Values the predicate reads |
|:--|:--|:--|
| `register` | `layers.go`, before `cfg` is built at `:832-841`, after the existing gate at `:804-809` | the request's `source_type`, `local_path`, and `repo` |
| `update` | `layers.go`, after the write gate at `:671`, before the `local_path` patch at `:697-698` | the patch's `local_path` alone; neither its source type nor its repository string is classified |
| `restore` | `layers.go`, after the write gate at `:1036` | the stored config's `SourceType`, `LocalPath`, and `Repo` |
| `reingest` | `layers.go`, after the write gate at `:1219` | the stored config's `SourceType`, `LocalPath`, and `Repo` |
| webhook ingest | `webhook_ingest.go`, in `handleWebhook` after the signature verifies and before `e.runIngestAndRespond(w, r, cfg, nil)` at `:79` | the stored config's `SourceType`, `LocalPath`, and `Repo` |

`update` passes an empty `source_type` and an empty `repo`, because the handler
decodes the patch into `LayerRegisterRequest` and applies neither field: only
`ForcePushPolicy` (`:687-689`), `Ref` (`:691-693`), `Root` (`:694-696`), and
`LocalPath` (`:697-698`) are assigned, and `patch.SourceType` appears nowhere in
the handler. A patch therefore cannot change a layer's source type or where a
`git` layer is fetched from, and classifying either value would refuse a field
the endpoint drops, on a request that patches no filesystem path. That refusal
would also exceed the staged §7.3.1 sentence, which scopes the update arm to
"patching a stored layer's filesystem path": a patch of
`{"source_type":"local","ref":"main"}` on a stored `git` layer patches the ref
and nothing else. An empty repository string is not
classified, and neither is the patch's source type, so a patch carrying no
`local_path` is admitted for whatever caller the write gate already admitted:
`podium layer update --ref release` sends `{"ref": "release"}` alone
(`cmd/podium/layer.go:95-97`) and the panel's Edit control sends `{ root }` plus
the visibility axes on a caller who may not name a filesystem path. `update`
reads the patch rather than the stored config, which is what keeps that caller's
Root-only edit admitted while the same caller's `local_path` patch is refused.
TEST-4 pins the admitted arm, the admitted arm of a patch that echoes a stored
record's `source_type` back, and the refused arm. A stored layer whose `Repo` already resolves to the file
transport is reached on its `restore` and its `reingest`, which are the
operations that re-read it.

`unregister` and `reorder` gain no call, because they name no path and re-read
none.

The webhook ingest is the fifth site because `handleWebhook` refuses a non-`git`
layer (`webhook_ingest.go:55-56`) and then drives the same
`runIngestAndRespond` the guarded `reingest` drives
(`webhook_ingest.go:79`, `layers.go:1223`), while consulting `authAdmin`
nowhere. A stored `git` layer whose `Repo` resolves to the file transport is
exactly the grandfathered record §7.3.1's staged paragraph says is refused at its
next such operation, and without this call site the holder of the per-layer
secret re-reads that host path with the registry's own rights. The webhook
delivery carries the per-layer secret rather than a session, so on a deployment
whose `authAdmin` closure inspects the caller the delivery resolves no admin and
takes the refusing arm. On a registry started with no identity provider
configured or in public mode the installed closure returns nil for every request
without inspecting one (`internal/serverboot/serverboot.go:1253-1262`), so the
delivery is admitted there, which is the outcome every call site of this rule
has on those deployments and which the staged §7.3.1 paragraph states once.
A `git` layer whose repository string names a network endpoint is unaffected on
every deployment, including one whose stored config also carries a `local_path`,
because `namesHostPath` classifies a `git` source on its repository string
alone. Every existing webhook for such a layer keeps working. That carve-out is
what the predicate's `sourceType != "git"` term buys: the shipped API stores a
`local_path` on a `git` layer without complaint (`layers.go:835-839`,
`:697-698`), the git provider never reads it, and refusing the layer would end
its webhook deliveries and its owner's reingest permanently while confining
nothing.
The refusal reuses the same `403 auth.forbidden` envelope and the same
`details.constraint: "local_source"`, so the sender reads one rule.

The refusal on a file-transport `repo` carries the same
`details.constraint: "local_source"`, because the constraint is that the
operation makes the registry read a path on its own host and the client renders
one rule rather than two. The register form cannot predict a per-value refusal
on a repository string it has not yet been given, so it keeps offering the `git`
source to every caller and draws the refusal the registry returns, which is the
stale-prediction arm D12 keeps drawn.

### CODE-5: the posture read reports the capabilities

`pkg/registry/server/webui_session.go` gains one seam on `SessionPosture`:

```go
// Capabilities reports the requesting caller's §7.3.4 layer capabilities. A nil
// seam reports every member false: NewLayerEndpoint defaults authAdmin to a
// callback that admits (layers.go:190), so a reporting surface must default the
// other way, and an unwired posture read withholds rather than promising.
//
// Spec: §7.3.4
Capabilities func(*http.Request) LayerCapabilities
```

The handler writes `layer_capabilities` unconditionally, with no `omitempty` on
the object or on its member, so the key set is fixed. The nil seam is handled in
the handler rather than at each call site.

`SessionPosture`'s own doc comment (`pkg/registry/server/webui_session.go:9-15`)
states that the read reports the posture and the caller's resolved subject "and
nothing else", and its next sentence says a carried credential "has it verified
only so the response can report `subject`". The seam falsifies both, and they are
the mirror of the two §7.3.4 sentences SPEC-5 replaces. The comment is rewritten
in the same edit to state one rule with the spec: it names the third thing the
read reports, it says the carried credential is verified so the response can
report `subject` and evaluate the capabilities and for no other purpose, and it
keeps the negative half of no issuer, client identifier, endpoint, filesystem
path, or other configuration value, and no subject or authorization belonging to
any caller other than the one that asked.
Leaving it is the same leftover DOC-4 removes from the design corpus, on the
declaration the change amends.

`internal/serverboot/serverboot.go`'s `SessionPosture` literal at `:1320-1325`
gains `Capabilities: layers.Capabilities`, with a comment recording that the
method value binds the endpoint's own `authAdmin`, which is the closure
`WithAdminAuth` installed at `:1253-1262`, so the reported capability and the
enforced gate are the same expression by construction. The closure itself stays
inline and is not hoisted into a variable; the method value already provides the
coupling.

### CLI-1: the `--local` usage strings name the constraint

`cmd/podium/layer.go:186` declares `register`'s `--local` as "filesystem path
(for local source)" and becomes "filesystem path (for local source; requires the
administrator role)". `cmd/podium/layer.go:79` declares `update`'s `--local` as
"filesystem path" and becomes "filesystem path (requires the administrator
role)". Line `:185` declares `register`'s `--root` and is not edited. The
binary's own `--help` is a surface DOC-1 does not
reach, and after CODE-4 the shipped string promises an operation the registry
refuses for a caller the admin arm does not admit. No flag is added or removed,
and no request body changes.

### UI-1: the posture type, the capability accessor, and one predicate module

`web/ui/src/session.ts`:

- The `SessionPosture` interface gains
  `layer_capabilities?: LayerCapabilities`, with `LayerCapabilities` carrying
  `manage_any_layer: boolean`. The field is optional on the type because a
  response from an older registry carries none, and the accessor below is what
  turns that into a closed default. The interface's own doc comment (`:8-10`),
  which says the read reports the posture and the caller's subject "and nothing
  else", is rewritten with the same sentence CODE-5 gives the Go wire type, so
  the two mirrors document one body.
- A new `capabilitiesOf(posture: SessionPosture | null): LayerCapabilities`
  exported beside `authControl` and `catalogScope`, returning every member false
  where the read did not answer or carried no object. Every surface passes the
  nullable posture it already holds, which is the convention the other rules in
  this file follow (`:52-118`) and which stops a call site forgetting the closed
  default. The accessor deliberately collapses an unanswered read into the same
  value an answered read that resolved no caller produces, because both callers
  are refused the same operations. The two empty states are the only places the
  two states read differently, and UI-2 derives a separate `postureAnswered`
  boolean for them rather than widening this return.
- The comment at `:116-118` ("The administrator role is reported by nothing, so
  no rule here predicts it") is rewritten to name what is now reported and what
  is still not: the posture read reports whether this deployment's layer
  endpoints admit this caller on the §4.7.2 admin arm, and it reports no role
  name and no grant table.

`web/ui/src/surfaces/layerrights.ts` (new), one concern per file as
`layerfacts.ts`, `members.ts`, `recovery.ts`, and `correction.ts` already are:

```ts
// What the caller may take on a layer. One predicate, because the server
// composes one: authorizeLayerWrite's two arms
// (pkg/registry/server/layers.go:321-329) and authorizeLocalSource's admin arm,
// which CODE-4's call-site table applies to register, update, restore, and
// reingest and to neither unregister nor reorder. Its Go mirror is
// TestLayerWriteAuth_UserDefinedOwnerOrAdmin
// (pkg/registry/server/layer_write_auth_test.go:154), and the two tables are
// meant to be diffable by eye.

/** LayerOp is the §7.3.1 write operation a control would take. */
export type LayerOp =
  | 'register' | 'update' | 'restore' | 'reingest' | 'unregister' | 'reorder';

/** LayerTarget is the layer as the operation would name it: the stored record
 * for unregister, restore, reingest, and reorder; for update the stored
 * record's UserDefined and Owner with SourceType omitted and LocalPath taken
 * from the patch; and for register the registration the dialog would build,
 * carrying the class it asks for and the registrant as Owner.
 * LayerRecord satisfies it structurally, so a caller passes a row directly. */
export interface LayerTarget {
  UserDefined?: boolean;
  Owner?: string;
  SourceType?: string;
  LocalPath?: string;
}

export function ownedByCaller(target: LayerTarget, subject: string): boolean
export function namesHostPath(target: LayerTarget): boolean
/** newLayerTarget is the registration the dialog would build under an unused
 * ID: user-defined, owned by the registrant, naming no filesystem path. Every
 * reader of the register prediction shares it, so the control that takes the
 * operation and the copy that instructs a reader to press it read one value
 * rather than two statements of the same reduction. The dialog's own
 * layer-class control and its Local folder option predict a different
 * registration and build their own target. */
export function newLayerTarget(subject: string): LayerTarget
export function mayTake(
  op: LayerOp, target: LayerTarget, caps: LayerCapabilities, subject: string,
): boolean
```

`mayTake` is
`authorized && (!reads(op) || !namesHostPath(target) || caps.manage_any_layer)`.
`authorized` is `caps.manage_any_layer || ownedByCaller(target, subject)` on
every operation, and the target's own fields decide which term carries it.
`ownedByCaller` is `target.UserDefined === true && subject !== '' &&
target.Owner === subject`. `newLayerTarget(subject)` returns `{ UserDefined: true, Owner: subject }`, which
is the owner the server derives from the credential
(`pkg/registry/server/layers.go:853-854`), and it carries no path key, so
`namesHostPath` is false on it. This is the one place this target's reduction is
derived and the only place it is stated: on that target `mayTake` is
`caps.manage_any_layer || ownedByCaller(target, subject)`, and `ownedByCaller`'s
own `subject !== ''` guard is what makes an empty subject fall through to the
admin arm, so the call decides the arm `pkg/registry/server/layers.go:804-809`
decides, which refuses only where `authAdmin` fails and the caller is
unauthenticated or resolves an empty subject. Every other reader of this
prediction cites the call rather than restating what it reduces to. The
register dialog's layer-class control and its `Local folder` option predict a
different registration, and the control table states each of those targets. An admin-defined registration carries `UserDefined: false` and reduces
`authorized` to `caps.manage_any_layer`, which is what withholds the class
control from an authenticated non-admin. That second reduction predicts no
§7.3.1 refusal: `pkg/registry/server/layers.go:821-830` resolves such a
registration down to a user-defined layer owned by the caller rather than
refusing it, and the staged §13.10 text carries the clause for a control naming
a value the registry resolves away, so the class control's absence is stated in
the rule rather than in a predicate of its own. `reads(op)` is true on
`register`, `update`, `restore`, and `reingest`, which is CODE-4's call-site
table and nothing beyond it, so `unregister` and `reorder` never reach the path
disjunct. `ownedByCaller` moves here from
`web/ui/src/surfaces/LayerPanel.tsx:1670-1673` because `mayTake` consumes it
and the panel's "yours" marker at `:1182` still reads it, and its existing
comment about an admin-defined row carrying no ownership marker is preserved
verbatim.

`namesHostPath` is
`target.SourceType === 'local' || (target.SourceType !== 'git' &&
(target.LocalPath ?? '') !== '')`, which is the server predicate with its `Repo`
disjunct dropped and its `git` carve-out kept. The carve-out is mirrored rather
than dropped because dropping it drifts toward absence: a stored `git` row
carrying a `local_path` is admitted by the server, and a client predicate
without the term would hide `Reingest` and `Restore` from the non-admin owner
who holds them. On an `update` target the path term decides, because that target
carries the patch's fields for the path and the patch names no source type, so
`target.SourceType` is undefined there and the server passes an empty source
type on the same operation. That is CODE-4's rule
that `update` classifies the patch's `local_path` alone. That is what keeps
`Edit` present on a non-admin owner's own `local` row while the `Local path`
field inside the form is withheld: the two controls take the same operation on
two different targets. The record does carry `Repo`
(`web/ui/src/api.ts:322-345`), and the client deliberately does not mirror
`isFileTransportRepo`: a second implementation of a fail-closed classifier in
another language is the drift this proposal otherwise avoids, and mirroring it
wrongly would hide a control from a caller who holds it. A target whose stored
or typed repository resolves to a host path is therefore offered the operation
and answered by the registry, on a row for `restore` and `reingest` and in the
register dialog for `register`. That is the unsettleable-refusal exception the
staged §13.10 text names, and D12 keeps it drawn.

A block is settled at the call site with
`block.every((row) => mayTake('reorder', row, caps, subject))`, because the
reorder request names the moved row's own class block
(`web/ui/src/surfaces/LayerPanel.tsx:478`, `:846-854`) and the handler
authorizes the ids that request names and refuses the whole call on the first
failure (`pkg/registry/server/layers.go:1124-1136`). It carries no condition on
the block's length: a block holding one row is a block with nothing to move
rather than a permission refusal, which the panel already reports through
`blockEdgeNote`.

No `canWrite`, `canReingestOrRestore`, `canReorderBlock`, `canNameHostPath`, or
`canRegisterAdminDefined` is added. Each is `mayTake` at a fixed operation, and
the last two are both `caps.manage_any_layer` read under a second name. One
predicate keyed on the operation is what keeps a control added later from
needing a name, a rendering sentence, and a §11 condition point of its own,
which is the cost the per-surface enumeration was accruing.

`newLayerTarget` is a target constructor rather than a second predicate, and it
is the exception the rule needs: a registration under an unused ID is the one
target no caller holds a record for, so without it each of the three readers of
the register prediction builds the object literal by hand and the reduction is
restated once per site. The register dialog's layer-class control and its
`Local folder` option name registrations of their own, which the control table
states and which this constructor does not build.

### UI-2: the panel, the deleted-layers surface, and the register form

Presence is decided by capability, and the present controls are then disabled by
`readOnly`.

- `web/ui/src/App.tsx` is where the capability object is derived, once. Beside
  `const subject = posture?.subject ?? '';` (`:322`) the shell derives
  `const caps = capabilitiesOf(posture);`, and `caps` is threaded to the surfaces
  the way `subject` already is: through `Surface`'s props (`:526-539`) and its
  call site (`:511-517`), then into `<LayerPanel>` (`:555`) and into
  `<DeletedLayers>` (`:553`), which also gains `subject`. `Surface` is an
  intermediate component that holds no posture of its own, so the props are the
  only route. Deriving it at this one site is what applies the closed default
  once rather than leaving each surface to remember it, and both prop changes are
  breaking interface changes whose only call sites are these two lines.
- The same site derives `const postureAnswered = posture !== null;` and threads
  it along the same route into `<LayerPanel>` alone, where the panel's signature
  (`web/ui/src/surfaces/LayerPanel.tsx:160-177`) gains it as a boolean prop
  beside `subject`, and reads it itself on the shell's own catalog line. The two
  empty-state arms below are its only readers, and no other expression in
  the client reads it. It reports whether the read settled anything and nothing
  about authorization, which the register call owns. The flag is needed because
  `caps` and `subject` cannot tell the two states apart: a read that answered and resolved
  no subject and a read that did not answer both give
  `{manage_any_layer: false}` from `capabilitiesOf` and both give an empty
  `subject`, since the shell holds `null` for a failed read
  (`web/ui/src/App.tsx:219`) and derives the subject from it at `:322`. The
  state the flag reports is the shell's own `posture`, which is initialized to
  `null` (`:77`), set to the answered object at `:209`, and set back to `null`
  at `:219`; those are its only writers, and the shell renders no surface until
  `postureLoaded` is true (`:309`), so the flag is never read on an outstanding
  read. Where the flag is dropped, the panel renders the register instruction
  for a caller the registry resolved none of, which is the case TEST-8 pins on
  both arms. The comment in the same rejection handler (`:214-217`) is rewritten
  by DOC-4, which owns every restatement of the unanswered-read rule.

Every control below is rendered only where `mayTake` admits its operation on its
target. The table is the panel's controls today; UI-1's rule is what a control
added later reads, so no row here states a condition of its own.

| Control | Site | Operation | Target |
|:--|:--|:--|:--|
| `Register layer` | `LayerPanel.tsx:528-537` | `register` | `newLayerTarget(subject)` |
| `Reingest all` | `LayerPanel.tsx:538-547` | `reingest` | each visible row; the control is present where at least one row is admitted |
| Drag handle | `LayerPanel.tsx:1150-1159` | `reorder` | every row of `blockOf(rows, layer.ID)` |
| Row reingest | `ReingestControl.tsx:135`, called at `LayerPanel.tsx:1203-1213` | `reingest` | the row's stored record |
| `Edit` | `LayerPanel.tsx:1239` | `update` | the row's class and owner with the patch's fields for the rest, which names no source type and no filesystem path where the form's `Local path` field is absent |
| `Unregister` | `LayerPanel.tsx:1248` | `unregister` | the row's stored record |
| Overflow trigger | `LayerPanel.tsx:1215-1226` | — | present where its item array is non-empty |
| `Restore` | `DeletedLayers.tsx:315-324` | `restore` | the tombstoned record |
| Layer class control | `RegisterLayerForm.tsx:275-286` | `register` | a registration whose class is admin-defined, which carries no owner the caller matches, so the row reduces to `caps.manage_any_layer` |
| `Local folder` source option | `RegisterLayerForm.tsx:624-631`, called at `:298` | `register` | a user-defined registration owned by the caller, carrying `SourceType: 'local'` and naming a filesystem path, which is the registration that option builds |
| `Local path` field | `UpdateLayerForm.tsx:178-187` | `update` | the row's class and owner with a patch carrying a filesystem path |

`readOnly` keeps its independent mute at every site that reads it today, over
whatever the table leaves present. The consequences that are more than a
presence decision:

- The empty state below the header (`:599-600`) instructs the reader to press
  `Register layer`, so the panel evaluates the register prediction once,
  `const mayRegister = mayTake('register', newLayerTarget(subject), caps, subject);`,
  and both the control's presence and this copy read it. The arm is
  `postureAnswered && !mayRegister`, whose only hand-written term is about the
  posture read rather than about authorization: where the read answered and the
  call refuses, the state reads "The registry resolved no caller for this page,
  so no layer can be registered from it." rather than "Register a layer to bring
  its artifacts into the catalog." For that caller the two states arrive
  together, because `readableBy` returns nothing for the same caller `:804-809`
  refuses (`pkg/registry/server/layers.go:252-268`). The two deployments that
  would break a hand-written condition are decided by the call itself and need
  no clause here: a registry that authenticates nobody reports no `subject`
  (`pkg/registry/server/webui_session.go:54-58`) while its installed admin
  closure admits every request (`internal/serverboot/serverboot.go:1253-1262`),
  so `mayRegister` is true, the control is drawn and the instruction stands; and
  a posture read that did not answer gives `postureAnswered` false, so the
  instruction stands there too even though the control is withheld, because an
  unanswered read settles nothing about whether the registry resolved a caller
  and the remedy DOC-4 states for that read is reloading the document.
- The sidebar states the same instruction on every route.
  `web/ui/src/App.tsx:463-469` renders "The catalog holds no domains." followed
  by "Register a layer to fill it." where `catalogBare` (`:336`) holds. Both
  arms are decided by the shell's own catalog read, `loadDomain('', treeDepth)`
  at `:182`, rather than by the layer list: the caller at issue presents no
  credential the registry rejects, so that read answers rather than refusing and
  `isIdentityRefusal` (`web/ui/src/api.ts:64-78`) matches no code on it, which
  leaves `refused` false (`:313`) and puts a registry whose root read returns no
  subdomain and no notable entry on the `catalogEmpty` (`:329`) and
  `catalogBare` arms. The shell derives `caps`,
  `subject`, and `postureAnswered` at `:322` and renders this line itself, so it
  reads the same register call the panel does, evaluated once beside them, and
  the `catalogBare` arm drops its second sentence where
  `postureAnswered && !mayRegister`. The `catalogEmpty`-but-not-bare sentence is
  untouched: it states where the artifacts sit and instructs nobody. This line
  is rendered copy, so UI-2 owns it and DOC-4's sweep does not reach it. The
  rule the arm follows is stated for this surface in the brief's domain browser
  section, which DOC-4 amends, and SPEC-7's §11 derivation records the arm as a
  condition point of the domain-browser unit's Render cell rather than of the
  layer panel's.
- `reingestAll`'s target list at `:376-377` narrows from `rows.map((row) => row.ID)`
  to the rows the same call admits, so a caller who qualifies on one row does
  not collect an `auth.forbidden` outcome for every other row (`:398-400`), and
  the run report names only the rows the run attempted.
- The drag handle is the reordering exception: it stays rendered and is
  disabled where its call refuses, `draggable` at `:1159` follows the same
  value, and the `aria-label` ternary at `:1153-1157` gains a third arm naming
  the reason so a disabled handle does not announce "press the up or down arrow
  key". `LayerRow` holds one `layer` and no `rows`, so the panel evaluates the
  block where it already maps its rows and threads the result in as one boolean
  prop, the way `subject` is threaded today. The panel-level restatements follow
  the same call over the whole list: `reorderable` at `:758`, which the footer's
  early return and its reorder note at `:769-776` read, and the
  `precedence-label` arms at `:584-589`, which gain the matching third arm.
  Evaluating per block is what keeps the handle live on a non-admin's own
  user-defined rows while an admin-defined row sits in the same list.
- The class control's row is the rule's resolves-away clause rather than a
  refusal prediction: `pkg/registry/server/layers.go:821-830` resolves an
  admin-defined registration from an authenticated non-admin down to a
  user-defined layer owned by that caller. The target the row names carries
  `UserDefined: false` and no owner the caller matches, so `mayTake` reduces to
  `caps.manage_any_layer` there, which is the condition the control was gated on
  before the consolidation.
- Where the class control is absent the form submits the user-defined class as
  it does today, and the note that follows it (`:288-292`) is not gated: it
  describes the class the caller is about to get, `userDefined` opens on
  `subject !== ''` (`:64`), and withholding it would falsify the hand-run Expect
  at `test/manual-validation.md:4777-4779`, which S48 keeps. The source control
  itself keeps rendering, so the `git` option stays available to every caller,
  which is the unsettleable-refusal exception on the register arm: a repository
  string that resolves to the file transport is refused by the registry and
  drawn in the dialog. `sourceType` already opens on `'git'` (`:50`), so a
  caller offered one option opens on the option they hold.
- Where the `Local path` field is absent the update patch omits `local_path` and
  carries `{ root }` plus whatever visibility axes `editableVisibility` already
  allows (`:73-75`), which is what keeps the Root-only edit admitted for the
  same caller `Edit` stays present for. `web/design/README.md:172` already
  requires the form to offer no control for a value it cannot change.
- `DeletedLayers.tsx`'s props gain the capabilities and the caller's subject
  (`:50-54`), which `DeletedRow` (`:249-257`) also takes. The operation is
  `restore` rather than a write-only test, because CODE-4 guards `restore` on
  the stored config's source type and path: a non-admin who owns a tombstoned
  local layer is admitted by the write arm and refused on the path arm, which is
  exactly the population the confirmed defect created. The deleted arm is served
  by the same list handler through the same read narrowing, so such a caller
  does see tombstones they cannot restore.
- Two controls are outside the rule and are unchanged, because their
  availability turns on the layer record alone. The webhook-rotation checkbox is
  `disabled={!git}` with a comment stating it deliberately stays drawn on a
  layer that carries no secret rather than disappearing between source types
  (`web/ui/src/surfaces/UpdateLayerForm.tsx:265-278`), and `editableVisibility`
  withholds the visibility axes on a user-defined layer because the registry
  discards them (`:35`, `:76`, `:199`). Both refuse the same control to every
  caller, including one the admin arm admits, so neither is a prediction about
  the caller and neither takes the rule's absent-by-default disposition.
- The client's comments that state the rules this change reverses are rewritten
  by DOC-4, which enumerates them by search rather than by line: the file header
  at `LayerPanel.tsx:1-13`, the register form's class comment at
  `RegisterLayerForm.tsx:58-63`, the shell's failed-read handler at
  `App.tsx:214-217`, the nav comment at `App.tsx:430-432`, and the account
  cluster at `App.tsx:1268-1274`. DOC-4 owns comments and design prose, and UI-2
  owns rendered copy.

**IMPLEMENTOR'S CHOICE:** whether `blockOf` moves into `layerrights.ts` beside
the reorder call or stays in `LayerPanel.tsx` and is called there. Any answer
must leave one definition of the class block in the client, because the same
function decides what the reorder request names and what the handle predicts,
and two definitions would let those two drift apart.
- `web/bundle` is regenerated. `.github/workflows/test.yml` runs `npm run build`
  and then `git diff --exit-code`, so the source edit and the rebuilt bundle
  ship in the same commit.

## Edge cases and accepted failure modes

| Case | Observable outcome | Where it is stated |
|:--|:--|:--|
| A caller the §4.7.2 admin arm admits registers, patches, restores, or reingests a layer naming a filesystem path | Admitted, unchanged from today. The ingest is confined where the staged confinement paragraph covers it, and bounded by the registry process's own rights everywhere else | §7.3.1's staged local-source paragraphs; `docs/deployment/layers.md` |
| An authenticated non-admin registers a layer naming a filesystem path | `403 auth.forbidden` with `details.constraint: "local_source"`. Nothing is stored | §7.3.1's staged paragraph and its amended `**Errors.**` paragraph; `docs/reference/error-codes.md` `auth.forbidden` |
| An authenticated non-admin patches `local_path` on a layer they own | `403 auth.forbidden` with the same discriminator, and the stored config is unchanged. The other patch fields on the same request are not applied | §7.3.1's staged paragraph; `docs/reference/http-api.md` `### Update a layer` |
| An authenticated non-admin restores or reingests a stored layer that names a filesystem path, including one registered before this rule was in force | `403 auth.forbidden` with the same discriminator. This is the arm that closes the confirmed exfiltration for a layer stored earlier, and an operator upgrading with such layers reads the change in `CHANGELOG.md` rather than in a boot log | §7.3.1's staged paragraph, "evaluated on each of those operations rather than against the stored layer list"; `docs/deployment/layers.md` |
| A stored layer of a custom §9.1 source type carrying a filesystem path, reingested by its non-admin owner | `403 auth.forbidden` with the same discriminator. The orchestrator hands that path to the provider whatever the source type names, so the predicate reads the path rather than the type alone | §7.3.1's staged paragraph, "or whose registration names a filesystem path"; TEST-4's custom-source-type case |
| An authenticated non-admin owner edits a local-source layer they own from the panel | The update form renders no `Local path` field and its patch names no `local_path`, so the Root and visibility patch is admitted and the stored path is unchanged | UI-2's control table and its `Local path` consequence; CODE-4's `update` paragraph; TEST-4's `update`-guard case; TEST-8's update-form case |
| An authenticated non-admin registers a `git`-source layer whose repository string names a network endpoint | Admitted, unchanged. The network transport yields tree objects rather than host files | §7.3.1's staged paragraph's `git` sentences |
| An authenticated non-admin registers a `git`-source layer whose repository string resolves to go-git's file transport, such as `/srv/other-tenant` or a `file://` URL | `403 auth.forbidden` with `details.constraint: "local_source"`. go-git's default protocol map registers `file` and its client runs git-upload-pack against the named path, so the registration reads a host directory with the registry process's rights | §7.3.1's staged paragraph's `git` sentences; CODE-4's `isFileTransportRepo`; TEST-4's repository-string table |
| An authenticated non-admin registers a `git`-source layer whose repository string go-git's endpoint parser rejects | Refused on the host-path arm, because the classifier fails closed on a string the transport cannot parse. A caller who meant a network endpoint spells it with its scheme | CODE-4's `isFileTransportRepo`; TEST-4's repository-string table |
| An authenticated non-admin patches a stored `git` layer they own with a `ref` or a `root` and no filesystem path, including a patch that echoes a `source_type` back | Admitted. `update` classifies the patch's `local_path` alone, and the handler applies neither `SourceType` nor `Repo`, so a patch carrying no `local_path` patches no filesystem path and the rule does not reach it | CODE-4's `update` paragraph; TEST-4's `update`-guard case |
| A stored `git` layer whose config also carries a `local_path`, reingested by its non-admin owner, by its webhook delivery, or restored by that owner | Admitted, and its webhook deliveries keep succeeding. The predicate classifies a `git` source on its repository string alone, because `Git.Snapshot` reads `Repo`, `Ref`, and `Root` and never the configured path (`pkg/layer/source/git.go:39-97`) while the orchestrator copies `LocalPath` into the source config for every source type (`pkg/registry/ingest/orchestrator.go:112`). Such a layer is producible today, because `register` copies `local_path` with no source-type condition (`pkg/registry/server/layers.go:835-839`) and `update` assigns it on any layer (`:697-698`) | §7.3.1's staged paragraph's `git` sentences; CODE-4's `namesHostPath`; TEST-4's stored-`git`-with-a-path cells; TEST-8's stored-`git`-with-a-path row |
| The panel offers a reingest on a `git` row whose stored repository resolves to a host path | The control is offered and the registry refuses it, and the panel draws the refusal on the row. The client does not mirror the repository classifier, so it does not predict this arm | UI-1's `namesHostPath` paragraph; D12; §13.10's staged unsettleable-refusal exception; TEST-8's file-transport rows |
| A caller types a `git` repository string that resolves to the file transport into the register dialog | The dialog offers the `git` source to every caller and the registry refuses the registration with `details.constraint: "local_source"`, which the dialog draws. This is the register arm of the same exception | D12; §13.10's staged unsettleable-refusal exception; CODE-4's `isFileTransportRepo`; TEST-8's file-transport rows |
| A caller who resolves no verified subject on a registry that configures an identity provider opens the panel | `Register layer` is absent, because `mayTake('register', newLayerTarget(subject), caps, subject)` refuses, which is the arm `pkg/registry/server/layers.go:804-809` already refuses. The caller also reads no rows, so the panel stands on its empty state, which states that the registry resolved no caller rather than instructing the reader to register one, and the sidebar's catalog-empty line drops "Register a layer to fill it." on the same call | UI-1's `authorized` paragraph; UI-2's control table and its empty-state consequence; §13.10's staged rule; TEST-8's no-subject case; S47 step 3's amended Expect |
| An authenticated non-admin whose visible rows are all admin-defined, or whose own layer names a filesystem path, opens the panel | `Reingest all` is absent. Where some visible row is admitted, the control is present and its run targets those rows alone, so the run report names no row it did not attempt | §13.10's staged narrowed-request sentence; UI-2's control table and its `reingestAll` consequence; TEST-8's `Reingest all` case |
| A caller on a registry started with no identity provider, or in public mode | Admitted on every local operation, including a webhook-triggered reingest, because such a registry authenticates no caller and the layer endpoints admit the request there. The standalone author's own workflow is unchanged, and the confinement still binds the ingests the staged confinement paragraph covers, while everywhere else the bound is the registry process's own rights. The posture read reports `manage_any_layer: true` on that deployment, including for a request that resolves no subject, so the panel draws `Register layer` and its empty state keeps "Register a layer to bring its artifacts into the catalog." | §7.3.1's staged paragraph; §7.3.4's staged bullet; §13.10; UI-2's empty-state consequence; TEST-8's authenticate-nobody case; OQ-1 |
| An ingest reads a required file through a symbolic link inside the layer directory that resolves outside it | The file is not read and the layer's whole ingest fails with `ingest.source_unreachable` at HTTP 502. `loadOne` returns the refusal to its walk callback, `walkLayer` returns it, and `Ingest` returns before persisting any artifact (`pkg/registry/ingest/ingest.go:1011-1013`, `:1018-1019`, `:508-510`), so no artifact and no bundled resource from that layer is accepted for that cycle and the artifacts served before the refusal stay in place until the layer is restructured. One escaping link fails the whole snapshot rather than skipping one resource, so a first ingest lands the layer nowhere and a reingest leaves the previous snapshot serving, which is what `CHANGELOG.md` tells the operator | §7.3.1's staged confinement paragraph; TEST-2 |
| The same refused cycle had already reached a `DOMAIN.md` | Accepted rather than closed. `Ingest` persists each domain record unconditionally (`pkg/registry/ingest/ingest.go:476`) and emits its `domain.published` event on the change-event seam and the audit sink only where that `DOMAIN.md` is new or its stored source changed (`:479-494`), both ahead of the `walkLayer` call (`:508`), so a refusal inside the artifact walk leaves the layer's domain composition updated from the refused snapshot while no artifact from it is accepted, and on the reingest path those events reach real receivers (`internal/serverboot/reingest.go:53`, `:81`). A layer the confinement refuses on every cycle therefore emits no further `domain.published` for a domain that did not change, which is what keeps a watch loop or a webhook delivery from fanning one event per domain per cycle to §7.3.2 receivers and the §8 audit sink. Deferring `PutDomain` until after the artifact walk succeeds would reorder every ingest path in the module, which is a separate change with its own blast radius. The staged §7.3.1 sentence states the outcome rather than promising an all-or-nothing cycle | §7.3.1's staged confinement paragraph; TEST-2's `DOMAIN.md` assertion |
| An ingest reaches a `DOMAIN.md` only through an escaping symbolic link | The domain is absent from the ingest and the ingest does not fail. `walkDomains` discards any read error and continues (`pkg/registry/ingest/ingest.go:1050-1053`), which it already does for a permission failure, and the staged confinement paragraph scopes the unreachable report to a read the ingest requires. Accepted rather than closed: changing it means giving `walkDomains` an error return, which reaches every ingest path | CODE-1's `walkDomains` paragraph; §7.3.1's staged confinement paragraph |
| An ingest reads a relative symbolic link inside the layer directory that resolves inside it | Read through, unchanged. The control is confinement rather than a symbolic-link ban | §7.3.1's staged confinement paragraph, "A relative symbolic link that resolves within the directory is read"; TEST-1 |
| An ingest reads a symbolic link inside the layer directory whose target is an absolute path resolving inside that same directory | Not read, and the layer's whole ingest fails with `ingest.source_unreachable`, on the same path as an escaping link. This is a behavior change: `os.DirFS` read it through. `os.Root` refuses an absolute target before comparing it against the root (`$(go env GOROOT)/src/os/root.go:43`, `:301-306`), and admitting it would mean resolving the target outside the confinement and comparing path strings. An operator whose layer holds such a link rewrites it relative, which `CHANGELOG.md` and `docs/deployment/layers.md` state | CODE-1's absolute-link paragraph; §7.3.1's staged confinement paragraph; TEST-1's absolute-link case |
| An ingest requires a file that is simply absent inside the layer directory, such as a `type: skill` artifact with no `SKILL.md` | Unchanged. The confined tree returns `fs.ErrNotExist` as `os.OpenInRoot` produced it, so `loadOne` keeps its `"%s: type: skill missing SKILL.md"` message and the refusal classification does not fire. Every other read failure, a confinement refusal and an in-root permission failure alike, is wrapped in `ErrSourceUnreachable`, because Go exposes no sentinel that isolates the escape | CODE-1's classification paragraph; TEST-1's `TestLocal_ReadClassifiesAbsentAndUnreadable`; TEST-2's second `SKILL.md` arm |
| The layer's declared path is itself a symbolic link | Admitted, unchanged. `os.OpenRoot` resolves its own root argument normally and confines only what lies beneath, and `pkg/materialize/atomic_treeatomic_test.go:105` records a symlinked root as legitimate | TEST-1's root-is-a-link case |
| A confinement refusal is reported as `ingest.source_unreachable`, whose SPI sentinel is documented as transient and carries `Retryable: true` | Accepted. The code under-describes the condition: `pkg/layer/source/local.go:26-29` already records that a permission failure is the same condition as a missing directory, and a confinement refusal is a permission failure at the openat boundary. The HTTP envelope is unaffected, because `ingest.source_unreachable` carries no `errorCodeRegistry` entry and reports `retryable: false` today. A later split into a distinct code is a separate change, and CODE-1 amends the sentinel's comment to name the permanent arm | D6; `spec/06-mcp-server.md` §6.10 `ingest.source_unreachable`; `docs/reference/error-codes.md` |
| `auth.forbidden` carries no `suggested_action` | Accepted and unchanged. The code has no `errorCodeRegistry` entry (`pkg/registry/server/error_envelope.go:24-104`), so every `auth.forbidden` arm reports an empty hint and `retryable: false`, which is what the panel's refusal band already renders. Giving the code a hint would have to serve every arm it covers and is not staged | `docs/reference/error-codes.md` `auth.forbidden`; TEST-4's envelope assertions |
| A public-mode registry reachable beyond loopback via `--allow-public-bind` | A caller the operator never identified takes the admin arm and may name a filesystem path. The confinement still bounds what the ingest reads to that directory over the ingests the staged confinement paragraph covers, and the registry process's own rights are the bound everywhere else. Recorded as OQ-1 rather than branched on | D9; OQ-1 |
| A tenant admin who is not the host operator names a filesystem path | Admitted. On a multi-tenant registry the two are different people, and the rule grants the tenant-admin arm a read of the host filesystem bounded by the process uid, and bounded by the named directory as well over the ingests the staged confinement paragraph covers. Everywhere else the process uid is the only bound. Recorded rather than closed, because closing it needs a third grant table beside §4.7.1 and §4.7.2 | Non-goals, "No third grant table"; OQ-1 |
| The posture read is served by a deployment that did not wire the capability seam | `layer_capabilities` is present with `manage_any_layer` false, and the client renders no layer write control | §7.3.4's staged bullet, "always present"; D11; TEST-5's nil-seam case |
| The posture read answers, the caller acts, and the deployment's grant changes in between | The offered operation is refused by the endpoint with its own §6.10 envelope, and the panel presents that refusal on the row it was attempted from | §7.3.4's staged bullet, "reports a snapshot"; §13.10's staged rule; TEST-8's stale-prediction case |
| A non-admin caller opens the register dialog | The class control and the `Local folder` source option are absent, the note describing what a layer of your own means stays rendered on its existing `userDefined` condition, and the form registers a user-defined layer from a `git` source as it does today | §13.10's staged rule and its resolves-away clause; `docs/deployment/layers.md` |
| A non-admin caller holding their own user-defined layers beside admin-defined ones opens the panel | The handle is live on the caller's own rows, because a move from one of them names that block alone and the caller writes every row in it, and it is rendered and disabled on an admin-defined row, with a title naming the reason. The caller could reorder such a set if they held the admin arm, so the control is one they could take rather than one they can never take | D13; §13.10's staged reordering exception; UI-2's control table and its drag-handle consequence |
| A non-admin caller reads the soft-deleted arm | Tombstones their §4.6 view admits are listed, and `Restore` is absent on any row `mayTake('restore', …)` refuses, including a tombstoned layer the caller owns that names a filesystem path | UI-2's control table and its `DeletedLayers.tsx` consequence; §13.10's staged rule; TEST-8's tombstone cases |
| `register` on a previously-unused ID by a caller who resolves no verified subject | `403 auth.forbidden` from the pre-existing gate at `pkg/registry/server/layers.go:804-809`, unchanged, and the local-source rule is not reached | §7.3.1's layer write authorization paragraph, unchanged |
| A registration that asks for an admin-defined layer from a caller the admin arm does not admit | Unchanged: the class is resolved server-side to a user-defined layer owned by the caller, the caller-supplied visibility axes are discarded, and the `201` body reports the resolved record. The panel no longer offers the class control to that caller, so the case arises through a hand-written request | Non-goals, "The registration-class rule"; `spec/07-external-integration.md:95`, `:97` |
| `podium layer register --public` by a non-admin | Unchanged: exit `0` with the resolved record printed, having applied no visibility. Recorded rather than fixed | Non-goals, "The registration-class rule" |
| The account cluster renders a raw OIDC subject | Unchanged. The posture read gains no `name` and no `email` field in this proposal | Non-goals, "The account cluster's raw-subject rendering"; `web/design/README.md:91`, `:200` |

## Testing

**TEST-1: the local provider refuses a read that leaves the layer's directory
(unit).** New `pkg/layer/source/local_confinement_test.go`, each case carrying
`// Spec: §4.6` for the local source's read surface and `// Spec: §7.3.1` for
the confinement rule, so the file maps to the section its siblings map to
(`pkg/layer/source/source_test.go:14`) as well as to the new rule. Written
against the snapshot rather than against the ingest, so it fails if anyone
reverts to `os.DirFS`.

- `TestLocal_ReadRefusesEscapingSymlink`. Build a layer directory holding
  `pkg/ARTIFACT.md` and `pkg/leak.txt` symlinked to a file two levels above the
  root. Assert `fs.ReadFile(snap.Files, "pkg/leak.txt")` returns an error and
  zero bytes of the target, that the error satisfies
  `errors.Is(err, source.ErrSourceUnreachable)` and names the refused path
  relative to the root and no host path, and that `fs.WalkDir` over the snapshot
  still reaches `pkg/ARTIFACT.md`, so the tree refuses the escaping path rather
  than failing every read. This is an `fs.FS`-level property. What the ingest
  does with the refusal is TEST-2's, and the answer there is that the layer's
  ingest fails as a whole.
- `TestLocal_ReadAdmitsSymlinkInsideRoot`. A relative link from `pkg/inside.txt`
  to `../shared/inside.txt` under the same root reads through, which pins that
  the fix is confinement rather than a symbolic-link ban. The link is created
  with a relative target explicitly, because the sibling case below pins that an
  absolute one is refused, and a test that let `os.Symlink` take either form
  would assert whichever the author happened to write.
- `TestLocal_ReadRefusesAbsoluteSymlinkInsideRoot`. A link from `pkg/abs.txt` to
  the absolute path of the same root's `shared/inside.txt` returns an error
  satisfying `errors.Is(err, source.ErrSourceUnreachable)` and zero bytes of the
  target, which pins the arm the staged §7.3.1 sentence and the edge-case row
  name: `os.Root` refuses an absolute target whatever it resolves to, and that
  refusal is classified as unreachable rather than as absent.
- `TestLocal_ReadClassifiesAbsentAndUnreadable`. This case pins the
  discriminator CODE-1 states, so a toolchain change that alters what
  `os.OpenInRoot` returns fails loudly rather than silently reclassifying a
  permanent refusal as a retryable one. In its first arm a read of a name that
  does not exist inside the root returns an error satisfying
  `errors.Is(err, fs.ErrNotExist)` and not
  `errors.Is(err, source.ErrSourceUnreachable)`, which is what keeps `loadOne`'s
  absent-`SKILL.md` message. In its second arm a file inside the root whose mode
  is `0` returns an error satisfying `errors.Is(err, source.ErrSourceUnreachable)`
  and not `errors.Is(err, fs.ErrNotExist)`, which is the disposition
  `pkg/layer/source/local.go:26-31` already takes for the root itself. The second
  arm skips where `os.Geteuid() == 0`, because the mode bits do not bind that
  caller; the first arm skips on nothing.
- `TestLocal_SnapshotRootMaySelfBeASymlink`. The configured path is itself a
  symbolic link to the layer directory. `Snapshot` succeeds and `ARTIFACT.md`
  reads, which preserves the behavior the reachability guard gives today.
- `TestLocal_BootstrapTreeIsConfined`. The tree constructor the two
  `internal/serverboot` sites and `pkg/registry/server` call refuses the same
  escaping link the provider refuses. This case asserts the constructor and
  cannot observe whether those three sites call it, because it lives in
  `pkg/layer/source`. TEST-10's two arms pin the two `internal/serverboot` calls,
  and TEST-2's `TestNewFromFilesystem_IngestIsConfined` pins the
  `pkg/registry/server` call, which no
  end-to-end invocation reaches.

No platform skip on the symbolic-link cases, whose euid-conditional sibling is
named in the bullet above. Create the links with `os.Symlink` and `t.Fatalf` on error, so
a platform or filesystem that cannot make one fails the run rather than
reporting a pass on nothing. Every test job in `.github/workflows/` runs on
`ubuntu-latest`, and a silent skip on a security regression test is the failure
mode this repository has already been bitten by.

There is no snapshot-close case, because the confinement holds no descriptor and
`Snapshot` gains no `Close`.

**TEST-2: the ingest fails the layer and classifies the refusal (unit).** The
cases sit in the packages that can observe each half.

- `TestSourceIngest_EscapingResourceFailsTheLayer`, in `pkg/registry/ingest`
  beside the existing source-ingest cases, carrying `// Spec: §7.3.1, §6.10`.
  Ingest a local layer holding one `DOMAIN.md`, one artifact, and one bundled
  resource symlinked outside the root. Assert the ingest returns an error
  satisfying `errors.Is(err, source.ErrSourceUnreachable)` whose message names
  the refused path relative to the layer root and no host path, that no artifact
  from the layer is persisted, and that `ListDomains` reports the layer's domain
  record, which is the granularity the edge-case table states: the domain walk
  commits ahead of `walkLayer` (`pkg/registry/ingest/ingest.go:472-494`,
  `:508-510`), so the domain arm and the artifact arm are asserted separately
  rather than as one all-or-nothing claim. A second refused cycle over the same
  unchanged tree asserts that `ListDomains` still reports the record while the
  `PublishEvent` seam and the audit sink receive no further `domain.published`,
  which is the emission guard at `:479` and the claim the staged §7.3.1 sentence
  makes about a repeatedly refused cycle. A third arm ingests the same tree
  with the escaping link removed and asserts the artifact lands, so the case
  fails if the confinement refuses a legitimate read.
- `TestSourceIngest_EscapingSkillFileClassifies`, in the same file carrying
  `// Spec: §7.3.1, §6.10`. Ingest a layer holding a `type: skill` artifact whose
  `SKILL.md` is a symbolic link resolving outside the root, and assert the error
  satisfies `errors.Is(err, source.ErrSourceUnreachable)`. Without CODE-1's
  branch this read substitutes an unwrapped message and the same tree is
  answered `500 registry.unavailable` with `retryable: true`. A second arm keeps
  a `type: skill` artifact with no `SKILL.md` at all and asserts the existing
  `missing SKILL.md` message, so the branch does not reclassify the absent-file
  case.
- `TestReingest_EscapingResourceEnvelope`, in `pkg/registry/server` carrying
  `// Spec: §6.10`. Drive `reingest` over the same tree and assert the envelope
  reports `ingest.source_unreachable` at HTTP 502 with `retryable: false`, which
  is the arm D6 records as accepted, and that no host path appears in the body.
  An unwrapped error there is coded `registry.unavailable` at HTTP 500 with
  `retryable: true` (`pkg/registry/server/layers.go:1361-1366`,
  `pkg/registry/server/error_envelope.go:26-29`), which tells the reader the
  registry did not answer when the registry answered and the source is the
  problem. The envelope is only observable from this package, which is why the
  assertion does not live beside the ingest case.
- `TestNewFromFilesystem_IngestIsConfined`, in `pkg/registry/server` carrying
  `// Spec: §7.3.1`, in two arms. `NewFromFilesystem` returns the ingest error
  rather than skipping the layer (`pkg/registry/server/server.go:337-350`), so
  the poisoned tree's arm asserts that the call returns an error satisfying
  `errors.Is(err, source.ErrSourceUnreachable)` and no server, and the control
  arm opens the same tree with the escaping link removed and asserts the server
  is constructed and serves the in-root artifact. This is what pins the third
  bootstrap site (`server.go:338`)
  on the shared constructor. It lives here rather than in TEST-10 because
  `NewFromFilesystem`'s only non-test caller in the module is
  `internal/testharness/registryharness`, so no `podium serve` invocation reaches
  that line and no end-to-end case can observe it.

**TEST-4: the local-source rule on all five operations, and the capability truth
table (unit).** New `pkg/registry/server/layer_local_source_test.go`, each case
carrying `// Spec: §7.3.1`, plus an amendment to
`pkg/registry/server/layer_write_auth_test.go`.

Cases in the new file:

- Admin arm admits, on each of `register`, `update`, `restore`, and `reingest`
  against a layer naming a filesystem path.
- Denying admin arm refuses, on each of the same four, with `403`, code
  `auth.forbidden`, `details.constraint: "local_source"`, `retryable: false`,
  and no filesystem path anywhere in the body. The `register` case additionally
  asserts `GetLayerConfig` reports not found afterwards, and the `update` case
  asserts the stored config is unchanged.
- The webhook ingest, on `newWebhookEndpoint`
  (`pkg/registry/server/webhook_ingest_test.go:16`), which is the fixture that
  seeds a `git` layer with a known secret and returns the `*LayerEndpoint`
  `handleWebhook` hangs off. `pkg/registry/server/webhooks_test.go` is
  `package server_test` and covers the §7.3.2 outbound receivers, so it is not
  the site. The fixture is extended to install a `WithAdminAuth` arm on the
  endpoint it returns, because it takes the constructor's admitting default
  today (`pkg/registry/server/layers.go:190`) and would otherwise exercise no
  refusal. A stored `git` layer whose `Repo` is `/srv/other-tenant`, delivered
  with a correctly signed body under a denying admin arm, is refused with `403`,
  code `auth.forbidden` and `details.constraint: "local_source"`, and no ingest
  runs. The same layer under the constructor's admitting default reaches the
  ingest, which is the open-deployment half the staged §7.3.1 sentence states.
  A layer whose `Repo` is `https://github.com/acme/x.git` reaches the ingest
  under the denying arm, so the case fails if the new call site refuses the
  population the rule does not reach. A third layer, whose `Repo` is
  `https://github.com/acme/x.git` and whose stored `LocalPath` is `/srv/stray`,
  reaches the ingest under the denying arm as well, which pins the predicate's
  `git` carve-out on the delivery path: without it every webhook for a stored
  `git` layer carrying a path answers `403` permanently. Each admitted arm
  wires a runner through `WithReingestRunner` and asserts it ran, because
  `runIngestAndRespond` with no runner answers `200` with a queued record and
  runs nothing (`pkg/registry/server/layers.go:1229-1235`).
- An unwired endpoint. The admin arm is left at the constructor default that
  admits (`pkg/registry/server/layers.go:190`) and every local operation is
  admitted, with a comment recording that the rule binds only where boot wires a
  denying arm and that a reporting surface therefore defaults the other way
  (D11).
- A stored layer of a non-builtin `source_type` carrying a non-empty
  `local_path`, reingested by its non-admin owner: refused. This is the case the
  `SourceType == "local"` test alone would miss. Its companion is a stored layer
  of the same non-builtin `source_type` carrying no `local_path` and no `repo`,
  reingested by its non-admin owner under a denying admin arm: admitted, so the
  predicate is pinned to the path rather than to the type being unrecognized.
- A stored `git` layer whose `Repo` is `https://github.com/acme/x.git` and whose
  `LocalPath` is `/srv/stray`, reingested and restored by its non-admin owner
  under a denying admin arm: admitted on both, because a `git` source is
  classified on its repository string alone. The cell names `LocalPath`
  explicitly rather than taking it from `seedLayer`'s default, so the case fails
  if the `sourceType != "git"` term is dropped. It is the unit-level companion
  of the webhook cell above, and together they pin the population whose webhook
  deliveries and owner reingests the rule must not end.
- The `update` guard reads the patch's `local_path` alone. Under a denying admin
  arm, on a stored `local` layer owned by the caller, a patch of
  `{"root": "docs"}` carrying no `local_path` is admitted at `200`, the stored
  `LocalPath` is unchanged, and `Root` is updated; the same caller's patch
  carrying `local_path` is refused. A third cell drives a stored `git` layer
  owned by the same caller with `{"source_type":"local","ref":"main"}`: it is
  admitted at `200`, the stored source type is still `git`, and `Ref` is updated,
  which pins that the guard classifies no field the handler drops. Together they
  pin that the guard reads the patch
  rather than the stored config, which is what keeps the panel's Edit control
  honest for the caller UI-2 keeps it for.
- A repository-string table over `isFileTransportRepo` and over `register` under
  a denying admin arm: `https://github.com/acme/x.git`, `http://…`, `git://…`,
  `ssh://git@host/x.git`, `git@github.com:acme/x.git`, and `host:path` carrying
  no `user@` are admitted; `/srv/other-tenant`, `./x`,
  `file:///srv/other-tenant`, `/srv/repos@h:x`, and a string
  `transport.NewEndpoint` rejects are refused with `403 auth.forbidden` and
  `details.constraint: "local_source"`. `/srv/repos@h:x` is the row that fails
  against a hand-written `user@host:path` predicate and passes against go-git's
  parser, which routes it to the file transport. An empty `repo` on a `git`
  registration is admitted by this rule and reaches `Git.Snapshot`'s own
  `ErrInvalidConfig` on the ingest path. The same table runs on `reingest`
  against a stored `git` layer carrying each value. Without this case the gate
  has a bypass on the axis §7.3.1 names as safe.
- `unregister` and `reorder` on the same seeded local layer under a denying admin
  arm: the outcome the write rule alone gives, so the rule's scope is pinned by a
  negative as well as by its five positives.
- The capability truth table, asserted directly against
  `(*LayerEndpoint).Capabilities` rather than through an HTTP body: an admitting
  admin arm, a refusing one, and one returning a wrapped `core.ErrUnavailable`,
  which takes the non-admin arm rather than reporting an error.
- `TestLayerEndpoint_CapabilityMatchesGate`. For the same three arms, the value
  `Capabilities(r).ManageAnyLayer` reports and the outcome of a write on an
  admin-defined layer through the handler agree, and so does the outcome of a
  local-source operation. This is the anti-drift assertion, run against the real
  evaluator rather than against a fixture mirror.

Two existing files are amended, and neither amendment is optional.

The unit-level one, and it changes the helper as well as the cells. `seedLayer`
(`:105-117`) applies its two defaults independently: it sets `SourceType` to
`local` only where the caller left it empty (`:108-110`), and it sets
`LocalPath` to `/tmp/seed` whenever `LocalPath` is empty (`:111-113`), whatever
the source type says. A cell that merely names `SourceType: "git"` therefore
arrives at the endpoint carrying a stray `/tmp/seed` the git provider never
reads, and no value the caller can pass clears it. The helper's `LocalPath`
default is therefore conditioned on `cfg.SourceType == "local"`, so a
`git`-source cell reaches the endpoint with an empty `LocalPath` unless the cell
names one. Without the narrowing the new stored-`git`-with-a-path cell above
cannot be written, because every `git` cell would carry a path by default and
the cell would assert nothing the default does not already produce. Every other
caller of the helper seeds a `local` layer and is unaffected by the narrowing.

With the helper corrected, the cells move.
`TestLayerWriteAuth_UserDefinedOwnerOrAdmin` asserts the owner `restore` and
owner `reingest` cells at `200` under a denying admin arm (`:136`, `:142`,
`:162`); the register table posts `source_type: local` as a non-admin (`:340`);
and `TestLayerRegister_RecoveryWindowSequence` registers and restores a local
path as a non-admin (`:467`, `:485-497`). Each moves onto a `git` source with a
network repository string and no `local_path`, so the cells keep asserting the
write rule they exist for rather than incidentally taking the new refusal, with
each comment noting why the source changed. The `restore` and `reingest` cells
take their source from `layerWriteOp` rather than from the shared seed, because
`layerWriteOps` (`:127-146`) is one table driven by a single `seedLayer` call
(`:172`): the struct gains the seed the cell needs, and the `update`,
`unregister`, and `reorder` cells keep the
seeded `local` source, since `update` classifies the patch, which carries `ref` alone
(`:133-135`), and the other two are outside the rule. Keeping them local is what
leaves a cell in which a non-admin owner writes a stored local layer and is
admitted.

**IMPLEMENTOR'S CHOICE:** whether `layerWriteOp` carries a whole
`store.LayerConfig` or the source fields alone. Any answer must let the
`restore` and `reingest` cells reach the endpoint with a `git` source and an
empty `LocalPath` while the other cells keep the `local` seed, so that the
stored-`git`-with-a-path cell names its path explicitly and fails if the
`sourceType != "git"` term is dropped.

The integration-level one, in `test/integration/layer_write_authorization_test.go`.
The fixture wires `WithAdminAuth` from `reg.AdminAuthorize` (`:33-36`), grants
admin to `ops@acme.com` alone (`:88`), and seeds `alice-personal` and `org` as
`SourceType: "local"` with real directories (`:99-100`). Its cell asserting that
alice, a non-admin owner, reingests `alice-personal` at `http.StatusOK` (`:122`)
and stamps `last_ingested_at` (`:143-145`) returns `403` after CODE-4, and its
re-registration case posts `"source_type": "local"` as a non-admin (`:160-162`).
Each cell either moves onto a source the local-source rule does not reach, so it
keeps asserting the write rule it exists for, or splits into a non-local cell for
the write rule plus a local cell asserting `403 auth.forbidden` with
`details.constraint: "local_source"`. The fixture drives
`localReingestRunner(st, nil)` (`:37`) over an on-disk tree, so a cell that must
stay reachable keeps a local source under the `ops@acme.com` caller rather than
under alice. Each amended cell carries a comment recording why the source or the
caller changed. The remaining `WithAdminAuth` fixtures are swept for the same
seeded-local-source pattern in the same step.

**TEST-5: the posture read's body, including the nil seam (unit).**
`pkg/registry/server/webui_session_test.go`, carrying `// Spec: §7.3.4`.

- `TestSessionPosture_Body` keeps its exact top-level key-count assertion at
  `:79` unchanged and each `want` map gains `layer_capabilities`. The sub-map
  branch at `:84-93`, which already asserts an exact sub-key count, is extended
  to cover it, which is what pins "always present with its member present, no
  `omitempty`". That exactness is the only guard on §7.3.4's closed body and is
  extended rather than loosened.
- A case constructing `server.SessionPosture{}` with a nil `Capabilities` seam:
  `layer_capabilities` is present and `manage_any_layer` is false, with a
  comment citing D11 and naming `NewLayerEndpoint`'s admitting default as the
  asymmetry it guards. No other test constructs the seam unwired.
- A case with a stub seam returning true, to pin that the handler serializes the
  seam's value rather than recomputing it.

The capability truth table is not asserted here. The handler computes nothing,
so those cases live in TEST-4 where the evaluator is constructed.

**TEST-6: the fixture's posture read reports the wired endpoint's capability
(integration).** `internal/serverboot/webui_auth_integration_test.go`. The
fixture's `SessionPosture` literal (`:276-280`) gains
`Capabilities: layerEndpoint.Capabilities`, and the capability keys are asserted
in `TestBrowserFlow_PostureRead` (`:706`) and in
`TestBrowserFlow_ExpiredSessionAcrossSurfaces` (`:752`), each carrying
`// Spec: §7.3.4`. No deployment-posture fields are added to `stackOpts`: the
fixture hand-assembles the endpoint and the mux and does not call `run()`, so a
posture it simulates would assert the test author's own wiring. The boot-level
guarantee is pinned by TEST-10, which is the only level at which the real
closure is reached.

**TEST-8: the web client's predicates and rendering (unit).**

- New `web/ui/src/layerrights.test.ts`, importing `./surfaces/layerrights`,
  which is where this package puts a module's unit test
  (`web/ui/src/correction.test.ts:3`). `TestMayTake_MirrorsTheServerArms` is one
  table over `mayTake`, keyed on the six variables the rule branches on rather
  than on a control: `manage_any_layer`, a resolved and an unresolved subject,
  the target's class, its stored owner against that subject, whether the target
  names a filesystem path, and each of the six `LayerOp` values. It is written
  to be diffable against `TestLayerWriteAuth_UserDefinedOwnerOrAdmin`
  (`pkg/registry/server/layer_write_auth_test.go:154`) and against CODE-4's
  call-site table, with the empty-subject, missing-`Owner`, and
  missing-`UserDefined` rows present. The rows that carry the design: a
  user-defined local layer owned by the caller, which `unregister` and `reorder`
  admit and `reingest`, `restore`, and a `local_path` patch refuse; the same row
  under an `update` target that carries the record's class and owner, no source
  type, and no filesystem path, which is admitted, so the `Edit` control and the
  `Local path` field are pinned to differ on one row; a `register` target
  carrying `UserDefined: false` for an authenticated non-admin, which is
  refused, and the same target with `UserDefined: true` and the caller as owner,
  which is admitted, so the layer-class control and the `Register layer` control
  are pinned to differ for one caller; a `git` layer whose `Repo` is a
  filesystem path, for which `namesHostPath` reports false and
  `mayTake('reingest', …)` reports true for its non-admin owner, which pins the
  unsettleable-refusal exception; a `git` row carrying a stored `LocalPath` and
  a network `Repo`, for which `namesHostPath` reports false and
  `mayTake('reingest', …)` and `mayTake('restore', …)` report true for its
  non-admin owner, which pins the client's `git` carve-out against the server
  cell that admits the same layer, so dropping the term on one side fails a test
  on that side; `newLayerTarget('')` with no admin arm, which is refused,
  `newLayerTarget('')` with `manage_any_layer` true, which is admitted because
  that is the deployment authenticating none, and `newLayerTarget('alice')` for
  `alice` with no admin arm, which is admitted, so the constructor is pinned to
  reduce to the admin arm on an empty subject and to the owner arm otherwise; and `capabilitiesOf(null)` reporting every member false,
  which pins D11 once. The block predicate is exercised as `block.every(…)` over
  a block of the caller's own user-defined layers, which it admits, a block
  holding a row the caller cannot write, which it refuses, and a block of one
  row the caller owns, which it admits, so it is pinned to authorization rather
  than to the block's length.
- `web/ui/src/surfaces.test.tsx`: the `posture()` factory (`:142-149`) derives
  `layer_capabilities` from the fields it already carries, using the predicate
  §7.3.4 gives the server, so a fixture cannot state a posture body the registry
  could not emit:
  `const open = overrides.public_mode === true || overrides.identity_provider_configured === false;`
  yielding `{ manage_any_layer: open }`, still overridable by an explicit
  `layer_capabilities`. The factory's comment records why the derivation exists
  and that an authenticated fixture is closed unless its call site says
  otherwise. The 244 public-mode and 12 no-identity call sites need no edit,
  because the derived value is the one the server reports for them; the
  authenticated-subject call sites that render a write control state the
  capability that makes it render.
- `web/ui/src/surfaces.test.tsx:1901-1904` is the existing unanswered-read case.
  Its comment and its name state that such a read renders "the layer panel with
  its write operations", which D11 reverses, and its assertions are replaced by
  the `postureAnswered` false case below. DOC-4's claim sweep returns the site
  and assigns the rewrite to TEST-8, because this file is TEST-8's.
- Component cases drive the rendered panel at the rule's condition points
  rather than once per control, and each case asserts every control the table
  in UI-2 lists, so a control added to that table without a rule fails here. An
  authenticated non-admin on a row they do not own sees no `Edit`,
  `Unregister`, reingest control, or overflow trigger, and sees all of them on
  their own user-defined `git` row; on their own user-defined local row the
  reingest control is absent while `Edit` and `Unregister` stay present; over a
  mixed visible set the drag handle is live and draggable on the caller's own
  rows and is present, disabled, and not draggable on an admin-defined row,
  with an accessible name stating the reason rather than instructing the reader
  to press an arrow key, which is the case that fails if the block target is
  read as the whole visible set; `Reingest all` is absent where no visible row
  is admitted and its run targets only the rows that are; `Restore` is absent
  both on a tombstone the caller cannot write and on a tombstoned local layer
  the caller owns; the update form on a local layer the caller owns renders no
  `Local path` field and submits a patch carrying no `local_path`, and renders
  the field for a caller holding `manage_any_layer`; the register dialog offers
  no class control and no `Local folder` source while its source control still
  renders and keeps its `Git repository` option; `Register layer` is absent for a caller who resolves no subject on a deployment
  that configures an identity provider, and both empty states that caller reads,
  the panel's and the sidebar's `catalog-empty` line, carry the no-caller arm
  rather than the instruction to register. Each of the three register cases
  asserts the control and both copies together, which is what fails if a site
  restates the reduction instead of reading the call. The panel's assertions
  supply the layer panel unit's Render cell and the sidebar's supply the
  domain-browser unit's, which is the attribution SPEC-7's §11 derivation
  carries; on a posture read that did not
  answer, which the case drives by rendering the panel with `postureAnswered`
  false and the same empty subject and closed capability object the
  answered-anonymous case uses, over a registry holding no visible row,
  `Register layer` is absent
  and both empty states still carry the instruction to register, which is the
  case that fails if the arm is keyed on the control's absence or on the
  subject alone rather than on the read having answered; a caller on a registry
  that authenticates none keeps every control; and that same caller, driven
  with `postureAnswered` true, an empty subject, `manage_any_layer` true, and
  no visible row, reads `Register layer` present and both empty states still
  carrying the instruction to register, which is the case that fails if a site
  restates the reduction rather than reading the call. Two cases pin the boundary the rule draws: for a caller
  holding `manage_any_layer`, the webhook-rotation checkbox is still rendered
  disabled on a local row and the visibility axes are still withheld on a
  user-defined row, neither of which the rule reaches. One case pins the stale
  prediction: a row rendered with the control present, whose write returns
  `auth.forbidden`, still draws the refusal band on the row.

**TEST-10: the boot wiring, the CLI, the posture read, and the bootstrap
ingest's confinement through the compiled binary (e2e).** The boot wiring and the
CLI are only observable in the spawned process, and the default coverage profile
does not move for them.

- `TestWebUISession_ReportsLayerCapabilities`, in
  `test/e2e/webui_session_posture_test.go`, carrying `// Spec: §7.3.4`. Boot the
  `injected-session-token` stack
  (`test/e2e/authserver_harness_test.go:136-174`) with
  `PODIUM_BOOTSTRAP_ADMINS=alice@acme.com` and `--web-ui`, and for the
  bootstrap-admin token and for a verified non-admin token assert both
  `GET /v1/ui/session`'s `layer_capabilities` and the outcome of a layer write on
  the admin arm in the same test. That pairing is what pins that `serverboot`
  passes one predicate to both surfaces. The file's closed-struct assertion gains
  the field. The `oidc-jwt` stack is not used, because
  `test/e2e/auth_oidc_jwt_test.go:58-63` skips it on darwin.
  **IMPLEMENTOR'S CHOICE:** how `startAuthServer` takes the extra serve
  arguments. Any answer must leave the existing callers unchanged and must let
  this case pass `--web-ui`, because the harness hardcodes
  `serve --standalone` today.
- `TestLayerCLI_LocalRefusedForNonAdmin`, carrying `// Spec: §7.3.1`. On the
  same stack, `podium layer register --local` under a verified non-admin token
  exits non-zero and prints `auth.forbidden` with the `local_source`
  constraint, and the same invocation under the bootstrap-admin token succeeds.
  A third arm on a standalone stack with no identity provider configured
  succeeds for every caller, which pins the admin-arm carve-out through the
  binary.
- `TestBootstrapLayer_IngestIsConfined`, carrying `// Spec: §7.3.1`, in two
  arms over the same tree: a directory holding one artifact and one bundled
  resource symlinked to a file outside the root. The first arm boots the binary
  with `--layer-path` over it, which is the invocation that reaches
  `bootstrapLayerPath` and its tree at
  `internal/serverboot/serverboot.go:455`; the function returns immediately on an
  empty `layerPath` (`:428-431`), so no other invocation reaches it. The second
  arm boots with a `--config registry.yaml` whose `layers:` block declares the
  same directory as a `local` source, which is the only invocation that reaches
  `bootstrapDeclaredLayers` and its tree at `:616`, because that function returns
  immediately on an empty `cfg.declaredLayers` (`:596-598`). A bootstrap ingest
  failure aborts startup: `bootstrapLayerPath` and `bootstrapDeclaredLayers`
  wrap the error and return it to `run()`
  (`internal/serverboot/serverboot.go:484-487`, `:636-637`, `:873-889`), and the
  doc comment records that this happens before any HTTP listener is bound
  (`:413-415`). Each arm therefore asserts that the boot exits non-zero, that
  the failure names the layer id and the sentinel's rendered text
  `source: unreachable`, and that no listener is
  bound, and each carries a control arm over the same tree with the escaping
  link removed that boots and serves the in-root artifact with none of the
  escaping bytes in any response body. Without both arms the deployment-mode
  divergence D5 names is untested on one side, and
  `TestLocal_BootstrapTreeIsConfined` keeps passing if either site is reverted
  to `os.DirFS`. Reverting either site changes the boot from a refusal to a
  successful boot serving the escaping bytes, which both halves of each arm
  catch.
  The third site, `pkg/registry/server/server.go:338`, is not reachable from the
  binary at all: it is inside `NewFromFilesystem` (`:305`), whose only
  non-test caller in the module is `internal/testharness/registryharness`. It is
  pinned at package level by TEST-2's `TestNewFromFilesystem_IngestIsConfined`
  instead.
  **IMPLEMENTOR'S CHOICE:** whether the control arm reads the served artifact
  through the catalog or through the boot log's ingest outcome, and whether the
  refusal arm reads the process's exit status, its combined output, or both. Any
  answer must distinguish the confinement refusal from a boot that failed for an
  unrelated reason by matching the layer id and the string `source: unreachable`
  in the failure, which is what `%w` on `source.ErrSourceUnreachable` renders
  through both bootstrap wraps (`pkg/layer/source/source.go:91`,
  `pkg/spi/errors.go:33`, `internal/serverboot/serverboot.go:485`, `:637`) and
  what `cmd/podium/serve.go:105-106` prints; the §6.10 code string
  `ingest.source_unreachable` is written only by the HTTP envelope
  (`pkg/registry/server/layers.go:1362`), which the bootstrap path does not
  reach, so an assertion on it would fail against a correct implementation. Any
  answer must fail if the escaping file's bytes reach a response body on the
  control arm, and must run both arms rather than reusing one arm's stack for
  both.
- `TestRegistryConfig_LocalSourceRefusalReachesTheCLI` is not added; the
  configuration lane has no new key.
- `test/e2e/web_ui_surfaces_test.go` greps the served bundle for
  `layer_capabilities`, in the established form
  (`test/e2e/web_ui_surfaces_test.go:65-87`), so a bundle built before UI-1 is
  caught.

## Manual validation

The hand-run scenarios that this change makes wrong or incomplete are S47 steps
1 and 3 with its Covers line, S48 steps 2 and 5, and S50's Goal paragraph,
Covers line, "Why by hand" paragraph, and steps 3 and 4, all on the
Keycloak-backed `oidc-jwt` stack S44 stands up. S55, S56, and S57 are added on
the same stack, and S44 gains a bootstrap-admin note the last two of them read.
Each is re-run by hand before it is committed.

On this stack the `admin` Keycloak user holds no Podium tenant-admin grant, so
every signed-in caller here is an authenticated non-admin and the new rendering
applies to all of them. That is what makes the stack the right place to read it.

**S47 step 1** (`test/manual-validation.md:4667-4680`) reads the posture body and
its Expect enumerates the keys. The Expect gains one sentence:

**Expect.** … and `layer_capabilities` reporting `manage_any_layer: false`,
because this stack configures an identity provider and seeds no admin grant, so
the caller the read reports on holds no tenant-admin role. The object is present
on every answer, including this one, which carries no `subject`. A missing
`layer_capabilities` key means the registry is serving a build from before the
posture read reported capabilities, and the panel on that build renders every
write control on every row.

**S47 step 3** (`test/manual-validation.md:4687-4697`) opens
`http://127.0.0.1:8153/app/` in a private window and clicks the sign-in
control. The step gains a reading of the panel before the click, because this is
the one hand-run place a caller who resolves no subject reads the layer panel,
and the register control and the empty state both change for that caller. The
instruction's address becomes the layers route, `#/layers`
(`web/ui/src/route.ts:149`), because an empty hash resolves to the catalog route
(`web/ui/src/route.ts:38-41`) and a reader who opens `/app/` alone is looking at
the domain browser rather than at the panel. The amended instruction reads "Open
`http://127.0.0.1:8153/app/#/layers` in a private browser window, read the layer
panel, and click the sign-in control", and the rest of the step is unchanged:
the sign-in control is the shell's, the callback returns the browser to
`http://127.0.0.1:8153/app/`, and the existing Expect reads that address as it
does today. The Expect gains:

**Expect.** … and, before the click, the panel header draws no `Register layer`
control and the empty state reads that the registry resolved no caller for this
page rather than instructing the reader to register a layer. A `Register layer`
control drawn here offers a registration
`pkg/registry/server/layers.go:804-809` refuses, and the old empty-state line
here instructs the reader to press a control the panel no longer draws. The list
is empty rather than refused, because `readableBy` narrows to nothing for a
caller with no subject (`pkg/registry/server/layers.go:252-268`). The sidebar's
own catalog line reads "The catalog holds no domains. Its artifacts sit at the
top of the hierarchy." here, which is the arm this change leaves alone. S44's
public layer puts its one artifact at the layer root
(`test/manual-validation.md:4112-4113`, `:4123-4125`), so the root catalog read
returns no subdomain and one notable entry, and `catalogBare`
(`web/ui/src/App.tsx:336`) is false. The arm UI-2 changes, which is the same
line's "Register a layer to fill it." for a caller who resolves no subject,
needs a registry whose root read returns neither a subdomain nor a notable
entry, which no hand-run stack stands up. TEST-8's component cases are what
cover it, and reading this line here is what pins the arm they do not change.

S47's `**Covers.**` line (`test/manual-validation.md:4650-4652`) names no §13.10
today, while the amended step asserts that section's rendering rule. Its closing
`and §4.6 group-scoped visibility.` becomes
`§4.6 group-scoped visibility, and the §13.10 layer panel.`, which is the
spelling S48's own Covers line uses (`test/manual-validation.md:4751`).

**S48 step 2** (`:4772-4783`) instructs the reader to "Choose 'Your own layer' as
the class" and to give `$WORK/own-repo` as the repository. Under the new
rendering the class control is absent for this caller, and the repository value
is an absolute filesystem path on the registry host, which
`isFileTransportRepo` places on the host-path arm, so the registration this
signed-in non-admin issues would be refused with `403 auth.forbidden` and S48
steps 3 and 4, S49, and S50 would all lose the `own-release` layer they read.
Two amendments keep the scenario runnable: the instruction drops the class
choice, and the repository value becomes
`https://git.acme.internal/alice/own-release.git`. `register` stores the
repository string and mints the webhook secret without fetching anything
(`pkg/registry/server/layers.go:942-966`), and no step in S48, S49, or S50
reingests the layer, so an unreachable network URL is the right fixture and
nothing downstream changes. Step 1's local repository stays as it is, and the
step gains a sentence saying it stands for the repository the reader would push
to that URL. The Expect gains:

**Expect.** … The form offers no layer-class control and no `Local folder`
source option, because this caller holds no tenant-admin role and the registry
would refuse both. A class control present here means the panel is predicting
from something other than the posture read, and a `Local folder` option present
here offers a registration the registry answers with `auth.forbidden`. A
filesystem path given as the repository is refused for the same reason, because
a repository string that resolves to the Git file transport names a path on the
registry host.

**S48 step 5** (`:4820-4832`) posts the same `$WORK/own-repo` string from the
terminal as its negative control. Its repository value becomes the same
`https://git.acme.internal/alice/own-release.git`, so the step keeps testing
what it exists to test. Its Expect is unchanged: the request carries no
credential, so the pre-existing gate for a caller who resolves no verified
subject answers first (`pkg/registry/server/layers.go:804-809`) and the
local-source rule is not reached.

S49's and S50's Prerequisites name `own-release` and the S48 run that registers
it (`test/manual-validation.md:4856-4857`, `:4924-4925`, `:4977-4978`). They are
unchanged, because S48 still registers that layer under the same owner.

**S50** (`:4906-5083`) drives the non-owner refusal from the `public-handbook`
row's `Unregister` control. Bob no longer sees that control, so step 3's panel
arm and step 4's network-panel confirmation both press and read something that
does not exist. The two steps are replaced by one panel step and one terminal
step, and `BOB_TOKEN` is exported in step 1 where the same password-grant POST
already runs (`:4944-4949`) rather than being minted a second time in step 5.

New step 3, replacing `:4988-5000`:

3. Read the `public-handbook` row in bob's panel.

   **Expect.** The row is listed, because bob's §4.6 view admits it, and it
   carries no write control at all: no Edit, no Unregister, no reingest control,
   and no overflow trigger. The actions cell is empty and holds the same width as
   a row that carries controls, so the list does not reflow. An Unregister
   control present here means the panel is still offering every write on every
   row, which is what this scenario now exists to catch.

New step 4, replacing `:5002-5008`:

4. Confirm the registry refuses the same operation when it is named directly, so
   the absent control is a prediction of the server rule rather than a
   substitute for it.

   ```bash
   curl -sS -X DELETE "http://127.0.0.1:8153/v1/layers?id=public-handbook" \
     -H "Authorization: Bearer $BOB_TOKEN" \
     -w '\nstatus=%{http_code}\n'
   ```

   **Expect.** `auth.forbidden` at HTTP 403. A `200` means the server let a
   non-owner delete another caller's layer, which is the failure this scenario
   exists to catch. The panel hiding the control and the registry refusing the
   request are two independent statements, and this step is the second one.

Step 5's `BOB_TOKEN` export is dropped, because step 1 now carries it. Steps 6
and 7 are unchanged.

**S50's Goal paragraph** (`:4908-4910`) states that the scenario validates "that
the panel reports the refusal on the row without claiming to know why". No
remaining step attempts a write from the panel, so that clause is replaced:

**Goal.** Validate that the registry refuses a layer write from a signed-in
caller who neither owns the layer nor is a tenant admin, that the panel offers
no write control on a row that caller cannot write, and that a layer outside the
caller's view is absent from that caller's list and still refused when named
directly.

The refusal band on a row it was attempted from is validated by S57, which
presses a control the caller was offered and then lost.

**S50's Covers line** (`:4914-4916`) is the second place the scenario states
what it validates, and it names the same clause the Goal paragraph drops: "and
the §13.10 panel's treatment of a refused write". That clause becomes the
rendering rule new step 3 asserts:

**Covers.** The §7.3.1 layer read visibility rule, the §7.3.1 layer-write
authorization rule, `auth.forbidden`, and the §13.10 rule that the panel offers
a layer operation only where the caller may take it.

**S50's "Why by hand" paragraph** (`:4918-4922`) is premised on the same
refusal, both in its statement of the assertion and in its claim that the
refusal's rendering is what no Go test reads. It is amended on the same terms:

**Why by hand.** The assertion is that a second person, signed in through the
same UI, is offered no write control on a row that person can see, is refused
that same write when it is named directly from the terminal, and reads a list
that omits the layer that person cannot see. The absent control is the part no
Go test reads, and the panel presenting per-owner scoping as server-enforced
while the server failed open is the defect this closes. The rendering of a
refusal the panel does receive is read by S57.

S55, S56, and S57 are added after S54, each written in the file's convention with
a Goal, a Covers line, a "Why by hand" line, a Prerequisites block, numbered
steps carrying runnable blocks, and an Expect block per step. Each carries an
explicit prerequisite line, because S50's cleanup tears the S44 stack down and
S51 to S54 run on a different stack.

The S44 stack seeds no admin grant (`test/manual-validation.md:4810`) and sets no
`PODIUM_BOOTSTRAP_ADMINS`, and `POST /v1/admin/grants` runs `requireAdmin` ->
`core.AdminAuthorize` against an empty grant table
(`pkg/registry/server/admin.go:22-25`, `pkg/registry/core/admin.go:28-31`), so no
caller on that stack can issue the first grant. S56 and S57 need one, so their
prerequisite re-stands the stack with a bootstrap admin who is not the signed-in
caller:

**Prerequisites (S55).** The S44 stack, re-stood by running S44's Prerequisites
and steps 1 to 6, with a signed-in session per S47 steps 1 to 3. `TOKEN` and
`SUBJECT` are the values S44's Prerequisites and step 1 export, and they belong
to the signed-in caller, who holds no tenant-admin grant on this stack. When
Keycloak or the `mkcert` CA is unavailable, skip and record the skip.

**Prerequisites (S56 and S57).** The S55 prerequisite, with two amendments to
S44's steps, both staged in DOC-2 as an S44 note the two scenarios point at:

- Before step 3's `podium serve`, create a second Keycloak user `carol` the way
  S50 step 1 creates `bob` (`test/manual-validation.md:4936-4937`), read her
  `sub` into `CAROL_SUBJECT` and her token into `CAROL_TOKEN` by the same
  password grant (`:4944-4949`), and export
  `PODIUM_BOOTSTRAP_ADMINS="$CAROL_SUBJECT"`.
- Carol is the bootstrap operator and never signs in to the UI. She exists so
  the first grant has an issuer, which `PODIUM_BOOTSTRAP_ADMINS` is the only
  route to (`internal/serverboot/serverboot.go:787-793`, `:2084`).

**S55: the register dialog offers only what the caller can take.**

**Goal.** Validate that the register dialog offers no layer class and no source
the registry will refuse for this caller, and that the registry refuses the same
registration when it is named directly.

**Covers.** §7.3.1 local-source authorization, §7.3.4 `layer_capabilities`,
§13.10 layer panel.

**Why by hand.** The wrong output is a dialog offering a class and a source the
registry then refuses, which reads to the operator as a product that lost their
input, and only a human reading the dialog sees that.

1. Open the layer panel as the signed-in caller and press `Register layer`. Read
   the class control and the source control.

   **Expect.** The dialog carries no layer-class control and its source control
   offers no `Local folder` option. A class control present here means the form
   is predicting from something other than the posture read. A `Local folder`
   option present here offers a registration step 2 shows the registry refusing.

2. Register a `local`-source layer from the terminal with the same caller's
   token.

   ```bash
   curl -sS -X POST "http://127.0.0.1:8153/v1/layers" \
     -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
     -d '{"id":"s55-local","source_type":"local","local_path":"/etc","user_defined":true}' \
     -w '\nstatus=%{http_code}\n'
   ```

   **Expect.** `auth.forbidden` at HTTP 403, with `"constraint": "local_source"`
   in the envelope's `details` and no filesystem path anywhere in the body. A
   `201` means the local-source rule is not wired on `register`, which is the
   confirmed defect this change closes.

**S56: the panel presents per-row only what the caller may take.**

**Goal.** Validate that the panel renders a layer operation only where the
caller may take it, and that a caller who holds the tenant-admin role gets every
control back, so a registry that hid every control from everyone does not pass.

**Covers.** §7.3.1 layer write authorization and local-source authorization,
§7.3.4 `layer_capabilities`, §13.10 layer panel.

**Why by hand.** The row actions are a rendering, and the failure this catches is
a control drawn for a caller who can never take it or withheld from a caller who
can.

1. Grant the tenant-admin role to the signed-in caller, using carol's bootstrap
   token, and register a user-defined local layer as that caller while the grant
   is in force.

   ```bash
   curl -sS -X POST "http://127.0.0.1:8153/v1/admin/grants" \
     -H "Authorization: Bearer $CAROL_TOKEN" -H 'Content-Type: application/json' \
     -d "{\"user_id\":\"$SUBJECT\"}" -w '\nstatus=%{http_code}\n'
   mkdir -p "$WORK/notes" && printf -- '---\nid: note\ntype: context\n---\nnote\n' \
     > "$WORK/notes/ARTIFACT.md"
   PODIUM_SESSION_TOKEN="$TOKEN" podium layer register \
     --registry http://127.0.0.1:8153 \
     --id s56-notes --local "$WORK/notes" --user-defined
   ```

   **Expect.** The grant answers `201` with `{"user_id": "<the subject>"}`, and
   the registration exits `0` with the stored record printed, carrying
   `"UserDefined": true` and this caller's subject as `Owner`. A `403` on the
   grant means `PODIUM_BOOTSTRAP_ADMINS` did not name carol's `sub`, and the
   prerequisite is re-run before continuing. A `403` on the registration means
   the grant did not take effect, because a non-admin may not name a filesystem
   path.

   The grant body carries `user_id` alone (`pkg/registry/server/admin.go:35-55`),
   and the grant lands in the caller's own tenant. `register` derives its source
   type from `--local` and declares no `--source` flag
   (`cmd/podium/layer.go:180-193`), and its flag set parses with
   `flag.ContinueOnError`, so an unknown flag exits non-zero before any request.
   `PODIUM_SESSION_TOKEN` is the credential `doJSON` attaches
   (`cmd/podium/layer.go:535`, `cmd/podium/main.go:1419-1439`), so the CLI acts
   as the same caller the browser session holds; S44's Prerequisites unset that
   variable (`test/manual-validation.md:51`), so exporting it on the command line
   is what makes the invocation authenticated at all.

2. Revoke the grant, reload the panel, and read the `s56-notes` row and the
   `public-handbook` row.

   ```bash
   curl -sS -X DELETE "http://127.0.0.1:8153/v1/admin/grants?user_id=$SUBJECT" \
     -H "Authorization: Bearer $CAROL_TOKEN" -w '\nstatus=%{http_code}\n'
   ```

   **Expect.** The revoke answers `204`. `s56-notes` carries `Edit` and
   `Unregister` behind its overflow
   control, because the caller owns it, and carries no reingest control, because
   it names a filesystem path and this caller is no longer a tenant admin.
   `public-handbook` is admin-defined, so it carries no write control at all and
   an empty actions cell of the same width. The drag handle on `public-handbook`
   is present, disabled, not draggable, and its accessible name states that the
   caller cannot reorder that set rather than instructing the reader to press an
   arrow key, while the handle on `s56-notes` stays live, because a move from it
   names the user-defined block, which holds this caller's own layers alone. A
   disabled handle on `s56-notes` means the client is reading the reorder
   predicate over the whole visible list rather than over the block the request
   names. A reingest control on `s56-notes` means the client is calling `mayTake` at
   `unregister` or `reorder` rather than at `reingest`, so it is predicting the
   write arm alone where the server also applies the local-source rule.

3. Re-grant the role with the same command as step 1's first block, reload, and
   read the same two rows.

   **Expect.** Both rows carry every write control, the reingest control is back
   on `s56-notes`, and the drag handle is live on both rows, including
   `public-handbook`, which the admin arm now lets this caller write. Without
   this step a registry that hid every control from everyone would pass step 2
   for the wrong reason.

**S57: a stale prediction still refuses, and the panel says so.**

**Goal.** Validate that the panel treats its prediction as a prediction: an
operation it offered and the registry then refuses draws the refusal on the row
rather than reading as a failure of the page.

**Covers.** §7.3.4 `layer_capabilities` snapshot semantics, §13.10 layer panel,
§6.10 `auth.forbidden`.

**Why by hand.** Only a human sees whether the page keeps working around the
refused row.

1. With the tenant-admin grant in place from S56 step 3, open the layer panel and
   leave it loaded without reloading it.

   **Expect.** `public-handbook` carries its full set of write controls.

2. Revoke the grant out of band, leaving the page loaded.

   ```bash
   curl -sS -X DELETE "http://127.0.0.1:8153/v1/admin/grants?user_id=$SUBJECT" \
     -H "Authorization: Bearer $CAROL_TOKEN" -w '\nstatus=%{http_code}\n'
   ```

   **Expect.** `204`. The loaded page does not change, because it holds the
   posture read it took before the revocation.

3. Press `Unregister` on the `public-handbook` row from the still-loaded page and
   confirm the dialog.

   **Expect.** The write is refused, the row draws the refusal band naming
   `auth.forbidden`, the band offers Dismiss alone and reads
   `Retrying does not clear this condition.`, and the rest of the page keeps
   working. A page that blanks, signs the caller out, or reports a transport
   failure means the client is reading a refusal as a failure of the page rather
   than as the registry's answer.

**Teardown.** Revoke any remaining grant, delete `s56-notes`, and run S44's
teardown.

The scenario index table gains a row for each of S55, S56, and S57, carrying the
same Deployment, Embeddings, Vector backend, and Live infrastructure values S47
to S50 carry, which is Keycloak in Docker with the `mkcert` CA. No scenario count
is edited, because the document states none: its title is
"# Manual validation scenarios" and neither the introduction nor the
"## Scenario index" heading names a number.

## Documentation changes

**DOC-1: the reference and deployment pages follow the new rules.** The pages
state the layer write rules and the posture body today and would contradict the
spec after SPEC-2, SPEC-3, SPEC-5, and SPEC-7. Each section is written to add no
new runnable block, because `doccov-check` fails on a new runnable example
without its `tools/doccov/manifest.yaml` entry and its executing end-to-end test.

- `docs/reference/http-api.md`: the layer write authorization block gains the
  local-source rule in the spec's own words; the `### Register a layer` body
  records that a `local` `source_type`, a `local_path` on a registration whose
  source type is not `git`, and a `repo` resolving to the Git file transport
  each put the registration on the local-source arm, and that a `git`
  registration is placed by its repository string alone;
  the `### Update a layer` body records that a patch carrying `local_path` puts
  the patch on that arm and that a patch carrying none is not reached by the
  rule, because the handler applies neither `source_type` nor a repository
  string there (`source_type` is already documented as immutable and no `repo`
  field is patchable, `docs/reference/http-api.md:381`), which is the
  distinction CODE-4's `update` row and TEST-4's admitted echoed-`source_type`
  patch pin; and the `/v1/ui/session` block
  gains `layer_capabilities`, its JSON example, the amended "carries no other
  field" sentence, and the statement that the object predicts a server decision
  rather than reporting a grant. That block's opening prose at `:62` is the
  page's copy of the §7.3.4 sentence pair SPEC-5 replaces, carrying "a request
  that carries one has it verified only so the response can report `subject`"
  verbatim, so it is rewritten to SPEC-5's wording: the page states that the read
  also reports what the caller may do on the §7.3.1 layer operations, and that a
  carried credential is verified so the response can report `subject` and
  evaluate `layer_capabilities`, and for no other purpose. The other mirrors of
  that pair are the Go declaration comment CODE-5 rewrites
  (`pkg/registry/server/webui_session.go:9-15`) and the TypeScript comment UI-1
  rewrites (`web/ui/src/session.ts:8-10`), and leaving this one unstaged would
  have left the reference page contradicting the spec on a security-relevant
  disclosure statement. The page also restates the authorization rule
  per operation, and both restatements become the superseded rule once
  local-source layers are guarded, so `### Reingest` (`:354-360`, "Reingesting an
  admin-defined layer is authorized to a tenant admin, and reingesting a
  user-defined layer to its owner or a tenant admin") and `### List soft-deleted
  layers and restore` (`:391-398`, the same sentence for restore) each gain the
  local-source arm: on a layer that names a filesystem path the operation
  additionally requires a tenant admin, and any other caller is refused
  `403 auth.forbidden` with `details.constraint: "local_source"`.
- `docs/reference/error-codes.md`: the `auth.forbidden` row gains the
  local-source arm and its `details.constraint` value, covering a `local` source
  type, a `local_path` outside a `git` source, and a `repo` resolving to the Git
  file transport, and
  states the difference from `ingest.source_unreachable`, which reports a source
  the registry tried to read and could not.
- `docs/reference/cli.md`: `podium layer register` and `podium layer update`
  record that `--local` requires the administrator role. The page's existing
  description of `--local` is corrected wherever it promises an unqualified
  registration.
- `docs/deployment/layers.md`: the local-source section states who may register a
  `local` layer, that the rule is evaluated per operation so a layer registered
  earlier is refused at its next reingest, and that an ingest reads only within
  the layer's own directory so a layer that relied on a symbolic link leaving its
  root must be restructured. The same sentence states that a symbolic link inside
  the layer whose target is written as an absolute path is refused as well, even
  where that target lies inside the layer, and that such a link is rewritten with
  a relative target. The disclaimer at `:150` restating "authoring rights
  are out of Podium's scope" gains the same qualification §4.7.2 gains, and so do
  its restatements at `docs/getting-started/concepts.md:211` and
  `docs/deployment/clustered.md:32`. The same disclaimer at
  `docs/deployment/local.md:168` does not gain it. That page documents the tier
  that runs no registry process, no database, and no identity provider
  (`docs/deployment/local.md:9`, `:15`), so there is no caller for a §7.3.1
  authorization rule to place on the §4.7.2 admin arm and stating the rule there
  would contradict the page. Its bullet gains the scope half alone, that the
  disclaimer is about writing content into a catalog directory the CLI already
  reads, with a cross-link to `docs/deployment/layers.md` for who may declare a
  layer naming a host path on a server-backed tier.
- `docs/deployment/access-control.md`: the local-source rule beside the layer
  write rule the page already describes, one table of what a caller without the
  administrator role may and may not do with layers, and the note that the
  posture read reports the caller's own capabilities to the web UI.

A documentation example that now returns `403` is a documentation defect to fix
rather than a test to add.

**DOC-2: `test/manual-validation.md`.** Staged in full in Manual validation
above.

**DOC-3: `CHANGELOG.md`, under `Unreleased`.** Added: the posture read's
`layer_capabilities`. Changed: the local-source authorization rule, naming the
closed default and the operator action for a deployment where a non-admin
registers `local` layers today, and naming the `git` arm, because a repository
string that resolves to go-git's file transport is now a filesystem path for
this rule and a non-admin registering one is refused; the panel's controls,
which are now present by capability; and the `--local` flag help. Fixed: the
unconfined local path, and the ingest that no longer reads through a symbolic
link leaving the layer root, with the note that one escaping link fails the whole
layer's ingest, so no artifact from that snapshot is accepted until the layer is
restructured while the artifacts served before the refusal stay in place. The
operator action names the second link the ingest stops reading: a link inside the
layer whose target is written as an absolute path is refused whatever it resolves
to, including a target inside the same layer, and is rewritten with a relative
target. The entry also states
that a `DOMAIN.md` the refused cycle already read is still committed, and that it
emits `domain.published` where it was added or changed since the previous ingest
so a repeatedly refused cycle over an unchanged domain stays quiet. The entry states that the change is a MINOR bump and
that no flag or key restores the prior behavior.

**DOC-4: the design corpus's "no response reports the role" rule.** The rule is
conditional on a stated API fact that SPEC-5 changes, so the rewrite follows the
same reasoning to a different conclusion rather than reversing a judgement.
Leaving any restatement behind puts two contradictory rules in one corpus.

The restatement sites are enumerated by search rather than by line number, for
the reason the board paragraph below gives for the same file: a hand-typed set
of line numbers into a corpus this commit edits is stale as soon as the first
edit lands, and a site added later is invisible to it. Three sweeps return every
site, and the rewrite each returned site takes is stated once below rather than
per site.

**The claim sweep.** Run over `web/DESIGN.md`, `web/design/README.md`, both
`web/design/*.dc.html`, and every tracked file under `web/ui/src/`, one file at
a time, with the file read as one string:

```sh
for f in web/DESIGN.md web/design/README.md web/design/*.dc.html \
         $(git ls-files web/ui/src); do
  perl -0777 -ne '
BEGIN { @t = ("administrator role","caller.s role","role badge","admin state",
  "holds this role","predicts no outcome","is not reported","reported by nothing",
  "write operations","write control","whatever refusal","keeps its write controls",
  "marker rather than a gate");
  $re = join "|", map { my $s = $_; $s =~ s{ }{\\s+(?:[*\#>/]+\\s*)?}g; $s } @t; }
while (/($re)/gi) { my $n = (substr($_,0,pos $_) =~ tr/\n//)+1; print "$ARGV:$n\n" }
' "$f"; done | uniq
```

The space in each term matches across a line break and across a comment
continuation marker, because the corpus hard-wraps prose and one site carries
its claim across one: `web/ui/src/App.tsx:216-217` reads "the layer panel with
its write" then "// operations.", and a line-oriented expression for that phrase
returns nothing.

**The board sweep.** The expression the board paragraph below states, run over
both `web/design/*.dc.html`.

**The prescription sweep.** Run over `web/DESIGN.md`, `web/design/README.md`,
and `web/design/Podium UI Inventory.dc.html`:

```sh
grep -nE 'Register layer|Reingest|Restore|Unregister|\bEdit\b|Local folder|[Ll]ocal path|local_path|Drag to reorder|⋮⋮' \
  web/DESIGN.md web/design/README.md 'web/design/Podium UI Inventory.dc.html'
```

The terms are the control names as markdown prose spells them rather than as
the boards spell them. The board sweep's control terms are HTML-delimited
(`>Restore<`, `>Unregister<`, `>Edit<`) and match nothing in a markdown file,
which would drop the recovery table's bare "and Restore" at
`web/design/README.md:157`; and the brief writes the update form's path field
as `local_path` (`:172`) rather than as the boards' `LOCAL PATH`. The claim
sweep does not reach a line that prescribes a control without stating a claim
about the caller, which is the class the brief's header, recovery-table,
register-dialog, update-form, and interaction-table lines fall in. Run at
`0a8ec93` the expression returns `web/design/README.md:148`, `:157`, `:161`,
`:170`, `:172`, `:174`, `:186`, `:214`, `:246`, and `:252`, `web/DESIGN.md:352`,
`:359`, and `:362`, the inventory's button, segmented-control, and
`LayerRow` sections, and four further inventory prose lines outside those
sections, `web/design/Podium UI Inventory.dc.html:1047` and `:1235` under the
`StatCard` and `OutcomeRow` headings (`:1045`, `:1233`) and `:1842` and
`:2130`, each of which describes what a reingest returns rather than which
caller a control is drawn for and so falls in E7. The lines outside R4 fall in
E3, E6, and E7.

**The rewrite a returned site takes.** A site carries one or more of these
claims and takes that claim's rewrite. Each is stated here once, and no site
below restates it.

- **R1, the reporting claim.** "No response reports that the caller holds the
  administrator role", "the administrator state is not reported", "it carries no
  role badge". SPEC-5 changes the fact this rests on. Rewrite: no response
  reports the caller's role, and the §7.3.4 posture read reports per §7.3.1
  operation whether this deployment's layer endpoints would admit this caller,
  which predicts a server decision rather than reporting a grant.
- **R2, the rendering claim.** "Renders its write operations on every row",
  "presents whatever refusal a write receives", "ownership is a marker rather
  than a gate", "a row the caller does not own keeps its write controls".
  Rewrite: the panel renders a §7.3.1 layer write control only where the §7.3.4
  posture read and the target's own class, stored owner, source type, and stored
  filesystem path admit this caller; the §13.2.1 read-only marker then mutes
  whatever remains present; and a refusal an offered write receives is still
  drawn on the row it was attempted from, because the posture read reports a
  snapshot.
- **R3, the unanswered-read claim.** "Where the posture read does not answer, the
  page renders neither authentication control, and the layer panel with its
  write operations." D11 reverses the second half. Rewrite: such a read holds
  `capabilitiesOf(null)`, every member false, so the page renders neither
  authentication control and no layer write control, and a reader recovers the
  controls by reloading the document.
- **R4, the unconditional prescription.** A line prescribing `Register layer`,
  `Reingest all`, `Restore`, `Edit`, `Unregister`, the `Local folder` segment,
  the `Local path` field, the layer-class control, or the drag handle with no
  condition. Rewrite: the line names §13.10's rendering rule as the source of the
  control's presence and states no per-control predicate of its own, so a later
  control changes the panel and not the brief.
- **R5, the empty-state arm.** "A caller who resolves no subject stands on the
  panel's empty state with no row to mark and no write to attempt." Rewrite: the
  sentence gains the arm UI-2's empty-state consequence states, which is that on
  a deployment configuring an identity provider the empty state reports that the
  registry resolved no caller for this page, while a deployment that
  authenticates none keeps "Register a layer to bring its artifacts into the
  catalog."

Where a claim appears in two files, both are rewritten. Rewriting one leaves the
rule and its negation over one population in one corpus, which is the outcome
the sweep exists to prevent, and it is the divergence pass 9 found at
`web/DESIGN.md:426-449` against `web/design/README.md:154`.

**The exclusion classes.** A returned line that falls in one of these is not
edited, and a line returned by a later re-run that falls in one of them is not
edited either.

- **E1, the read-only marker's own rule.** A line stating that a read-only
  registry mutes every write control at once, such as `web/DESIGN.md:637`,
  `web/design/README.md:225`, `web/ui/src/api.ts:98`,
  `web/ui/src/surfaces/LayerPanel.tsx:552`, and the read-only lines at
  `web/ui/src/surfaces.test.tsx:8662`, `:8667`, `:8776`, and `:8820`. The rule
  is unchanged: the marker mutes whatever the capability rule leaves present.
- **E2, a deployment that authenticates none.** A case or a board drawn for that
  deployment keeps every control, because the capability is true there.
  `web/ui/src/surfaces.test.tsx:7404-7409` is the standalone case.
- **E3, a control whose availability turns on the layer record alone.** The
  unregister confirmation at `web/design/README.md:170`, the column set at
  `:150`, the webhook-rotation checkbox, and `editableVisibility`. Neither
  predicts anything about the caller.
- **E4, a claim about something other than the caller's layer writes.** Which
  version or which layer a response reports
  (`web/ui/src/surfaces/ArtifactViewer.tsx:88`, `:383`), the layer quota's
  deployment default, and the domain-browser paging case at
  `web/ui/src/surfaces.test.tsx:16148`, which the term `is not reported` returns
  from "An artifact past the returned edge is not reported as absent".
- **E5, the undesigned-states list** at `web/design/README.md:258`. It records
  what no board draws, and this change draws no board for those states.
- **E6, a component swatch carrying a control label as sample text.** The
  inventory draws each component on its own rather than drawing the panel, so a
  swatch labelled with a control name prescribes nothing about that control's
  presence on a row. The button section (`web/design/Podium UI Inventory.dc.html:192`,
  `:196`, `:200`, `:202`, `:208`, `:212`, `:216`, `:218`) and the segmented
  control (`:361`, `:369`) are that class. The `LayerRow` entry is not: it draws
  the row itself, and this commit rewrites its prose, its signature, and its
  drawn states.
- **E7, a line naming an operation, an error code, or a glyph rather than
  prescribing a control.** `web/DESIGN.md:352`, `:359`, and `:362` describe what
  the reingest and unregister endpoints do; `web/design/README.md:174` is a
  section heading, `:186` is the reingest error-code list, `:246` states that a
  reingest result resolves into a modal, and `:252` is the inline-SVG glyph
  inventory. The inventory's prose lines outside its button, segmented-control,
  and `LayerRow` sections are the same class:
  `web/design/Podium UI Inventory.dc.html:1047` and `:1235` state what a
  reingest's counts and its rejection detail carry, `:1842` states that a
  reingest fans out one request per layer, and `:2130` states that its result
  resolves into a modal. None states whether a control is drawn for a caller.

**Snapshot of what the sweeps return, at `0a8ec93` (2026-09-01).** The line
numbers are a reading aid and are not the enumeration; the sweeps are. A
disagreement between this table and a re-run is resolved in the sweep's favour.

| Site | Claims | Disposition |
|:--|:--|:--|
| `web/DESIGN.md:47-52` | R1, R2 | rewritten |
| `web/DESIGN.md:290-293` | R1 | rewritten |
| `web/DESIGN.md:426-449` | R2, R5 | rewritten; twin of `web/design/README.md:154` |
| `web/DESIGN.md:464-471` | R1, R3 | rewritten |
| `web/DESIGN.md:489-494` | R1, R2 | rewritten |
| `web/design/README.md:91` | R1 | rewritten |
| `web/design/README.md:93` | R1, R2 | rewritten; twin of `web/ui/src/App.tsx:430-432` |
| `web/design/README.md:146` | R1 | rewritten |
| `web/design/README.md:148` | R4 (`Register layer`, `Reingest all`) | rewritten |
| `web/design/README.md:154` | R1, R2, R5 | rewritten |
| `web/design/README.md:157` | R4 (`Restore`) | rewritten |
| `web/design/README.md:161` | R4 (`Local folder`, `Local path`) | rewritten. Leaving it makes the brief prescribe for the register dialog the control the boards below stop drawing on that dialog |
| `web/design/README.md:172` | R4 (the update form's `Local path`) | rewritten; the rule the line already states about owner and visibility is unchanged |
| `web/design/README.md:200` | R1 | rewritten. Its code twin `web/ui/src/App.tsx:1268-1274` states the same clause about the same cluster, and rewriting one leaves the corpus disagreeing with itself. The account menu's rendering is unchanged |
| `web/design/README.md:214` | R4 (the drag handle) | rewritten: the interaction row gains the arm that a row the caller may not reorder carries a handle that is present, disabled, and does not lift, with an accessible name stating the reason |
| `web/design/README.md:223` | R1 | rewritten |
| `web/design/Podium UI Inventory.dc.html:779-780` | R2 | rewritten by the paragraph below, which also states the signature change |
| `web/design/Podium App.dc.html:4628`, `:4750` | R2, R5 | rewritten. These are board 14i's label and its in-board caption, and the board paragraph below states the treatment |
| `web/ui/src/App.tsx:214-217` | R3 | rewritten, beside the `capabilitiesOf(posture)` derivation |
| `web/ui/src/App.tsx:430-432` | R1, R2 | rewritten |
| `web/ui/src/App.tsx:1268-1274` | R1 | rewritten |
| `web/ui/src/session.ts:116-118` | R1 | rewritten |
| `web/ui/src/surfaces/LayerPanel.tsx:1-13` | R1, R2 | rewritten |
| `web/ui/src/surfaces/RegisterLayerForm.tsx:58-63` | R1 | rewritten |
| `web/ui/src/surfaces.test.tsx:1901-1904` | R3 | rewritten, staged in TEST-8, which owns this file |

Two further code comments restate the posture read's closed body on the
declaration each change amends, and they are staged with the change rather than
here: `pkg/registry/server/webui_session.go:9-15` in CODE-5 and
`web/ui/src/session.ts:8-10` in UI-1. Rendered copy is UI-2's rather than
DOC-4's, which is what keeps `web/ui/src/App.tsx:463-469` out of this table.
The rule that copy's new arm follows is design prose, so DOC-4 states it, in
the one place §11 admits for that surface.

**The one statement DOC-4 adds rather than rewrites.** UI-2 gives the shell's
catalog line an arm on the register prediction and on `postureAnswered` (the
bullet on the shell's own register instruction). That line is the domain
browser's, and under §11 the domain-browser unit's statement is its section in
the brief, which today states nothing about when the root's empty state
instructs the reader to register a layer. Append to the root-state paragraph at
`web/DESIGN.md:150-153`, after "it has to read as the top of the hierarchy
rather than as an empty domain.":

> A root whose read returns no subdomain and no artifact is the one state in
> which this surface instructs the reader to register a layer, and that
> instruction is a claim about the caller reading it. The browser states it
> only where the same §7.3.1 prediction the layer panel reads admits this
> caller on a registration, and it states it unchanged where the identity
> posture read settled nothing, because an unanswered read settles nothing
> about whether the registry resolved a caller.

This site enters DOC-4 by name rather than by search, because a sweep returns a
site that restates a rule and this is a rule the corpus states nowhere. It is
the only addition, and every other site in this deliverable is a returned line.
SPEC-7's §11 derivation cites this paragraph for the domain-browser unit's
Render cell.

DOC-4 adds no test. The rules this corpus restates are pinned where they live:
R1 by CODE-5's posture-body test and the §11 matrix cell citing SPEC-5, R2 and
R4 by TEST-8's component cases over UI-2's control table, R3 by TEST-8's
`capabilitiesOf(null)` row and its `postureAnswered` false case, and R5 by
TEST-8's empty-state cases. The statement added to the brief is pinned the same
way, by TEST-8's cases over the sidebar's `catalog-empty` line, which supply the
domain-browser unit's Render cell. Nothing mechanical checks this corpus:
`grep -rn "DESIGN.md\|design/README" tools/ test/ Makefile web/web_test.go`
returns nothing and `make coverage-gate` does not reach it, so the re-run in S14
is what observes a missed site.

`web/design/Podium UI Inventory.dc.html:780` carries the `LayerRow` component's
own prose statement of the rendering rule, and that statement is what UI-2
reverses: "Ownership is a marker rather than a gate: a row the caller does not
own keeps its write controls and presents whatever refusal the write receives,
and only the read-only mode mutes the controls." It is rewritten to state the new
rule, which is that a control's presence is decided by the §7.3.4 capability and
the row's class, stored owner, source type, and stored filesystem path, and that
the §13.2.1 read-only marker then mutes whatever remains present. The remainder
of the paragraph, which prescribes the three-valued `ownership` prop and the
refusal state, is unchanged, because the marker's own rule is not what changes.
The component signature at `:779`, `<LayerRow layer ownership readOnly
dragging>`, gains the capabilities the row reads and the per-row reordering
boolean UI-2 threads into it beside the `subject` the row already takes
(`web/ui/src/surfaces/LayerPanel.tsx:968-986`), so the drawn states below it and
the signature name the same inputs. Leaving the paragraph
puts the rendering rule and its negation on one component page.

Drawn boards rewritten in the same commit, because a board drawing a control the
panel no longer renders contradicts §13.10's staged rendering sentence, and the
corpus subordinates itself to the spec (`web/design/README.md:7`). Its own
authority sentence at `:27` is narrower than that: the mockups win over the
README on a disagreement over a number. Commit 63f7186 is the precedent, which
edited prose and boards in one commit for this class of divergence.

- `web/design/Podium App.dc.html`. The boards are enumerated mechanically rather
  than by line number, because the file is one generated document whose line
  numbers shift under any edit: every drawing site is returned by
  `grep -n 'Reingest all\|>Reingest<\|>Restore<\|>Unregister<\|>Edit<\|Local folder\|⋮⋮'`,
  and each site belongs to the board whose preceding
  `<div class="dv-opt" id="…">` names it. The `Local folder` segment and the
  drag handle are enumerated with the row controls, because the rule now gives
  each a withheld or disabled state. Each added term is spelled as the boards
  spell it: the handle is drawn as the glyph `⋮⋮` in an inline-styled span
  carrying no class attribute (`web/design/Podium App.dc.html:933`), so a
  `drag-handle` term returns nothing, and the register dialog's local-folder arm
  spells its field label `LOCAL PATH` (`:1919`), which the `Local folder` term
  already reaches through the same board (`17b`, which opens at `:1793` and
  heads its modal `Register a layer` at `:1909`). No board draws the update
  dialog at all, because `web/design/README.md:172` records that form as not yet
  mocked. The added terms return no board the old expression missed: every board
  drawing a handle is already returned through its own `Reingest all` header
  line, the four `Local folder` sites (`:1747`, `:1916`, `:2695`, `:2882`) fall
  in boards `17a`, `17b`, `16a`, and `16b`, and `Register layer` occurs only on
  the header lines `Reingest all` already matches. They are added so a reader
  amending a board reads the control they are amending rather than inferring it
  from the header, and no board draws a layer-class control today, so the
  register-dialog board gains one rather than being found by the sweep. The boards that draw those controls today are the topic-18 ingest boards
  `18a` (`:890`) through `18e` (`:1488`), the topic-17 boards `17a` (`:1624`)
  through `17g` (`:2492`), the topic-16 boards `16a` (`:2572`) and `16b`
  (`:2759`), `15f` (`:3303`), and the topic-14 layer-panel boards `14d`
  (`:4104`), `14e` (`:4234`), `14h` (`:4487`), and `14i` (`:4627`). Each keeps
  its controls and gains a caption in its `dv-olabel` naming the caller it draws,
  which is what makes the drawing consistent with the staged §13.10 rule: a
  board that draws every control is a board drawn for a caller the admin arm
  admits, or for a caller on a deployment that authenticates none. A board's own
  prose captions are corpus prose and are returned by the claim sweep rather
  than by the control expression, which is how board 14i's caption at `:4750`
  reaches this deliverable. That caption states R2 and R5 over "a registry where
  the rule is live", where UI-2 puts a caller who resolves no subject on the
  empty state with no write control, so it takes their rewrites, and its half
  about the deployment that authenticates none stays. The board's label at
  `:4628` says the board is drawn on a deployment that runs no browser sign-in,
  which is a narrower condition than authenticating no caller, so the label is
  amended to name the deployment the drawing depends on. `17g`
  (`:2492`) draws no reingest control and is in scope for its `Restore` control,
  which UI-2 gates on `mayTake('restore', …)`. Board 14i draws a
  caller who resolves no subject on a deployment that authenticates none and
  keeps every control, because the capability is true there. A new or amended
  board draws the panel for an authenticated non-admin: a row the caller does not
  own carries an empty actions cell of the same width, no overflow control, and a
  disabled drag handle, while the caller's own user-defined row carries all of
  them. The register-dialog board drops the class control and the `Local folder`
  source for that caller.
- `web/design/Podium UI Inventory.dc.html`: the `LayerRow` drawn states gain the
  no-write-controls state, beside the prose and signature rewrite the paragraph
  above stages on the same component entry.

## Open questions

**OQ-1. Does the local-source rule need an arm below the §4.7.2 admin arm, or one
above it?** The staged rule authorizes the admin arm and refuses everyone else,
with no configuration. Two readings pull in opposite directions and both are the
reviewer's to settle.

The looser reading says a deployment exists where a non-admin should name a host
path, and it would need a key naming permitted roots. D1 rejects it: no page in
`spec/` or `docs/` describes that deployment, the key would be unset and
authorize nobody by default, and it can be added later as an additive MINOR
change against a concrete case.

The stricter reading says a tenant admin is not the host operator, and on a
multi-tenant registry the two are different people, so the admin arm should not
by itself authorize a read of the host filesystem. Public mode is the sharpest
instance: it is network-reachable via `--allow-public-bind`
(`internal/serverboot/serverboot.go:1574-1577`) and its admin closure admits
every caller (`:1253-1262`). D9 adds no branch there, because a second expression
of a deployment condition that has one canonical expression is the drift
proposal 0015 settled the same question to avoid, and because the confinement binds public mode like every other deployment over
the ingests the staged confinement paragraph covers, while everywhere else the
bound is the admin gate and the registry process's own rights. The branch is one line if the
maintainer wants it, and refusing a `local` registration outright in public mode
is the smallest form of the stricter reading.

## Non-goals

- **The registration-class rule.** `register` keeps resolving the class
  server-side, `LayerRegisterRequest.UserDefined` stays a plain `bool`, and
  `update` keeps ignoring a visibility patch on a user-defined layer. The
  outcome is already disclosed: `register` returns
  `LayerRegisterResponse{Layer: cfg}` carrying the persisted class, owner, and
  visibility (`pkg/registry/server/layers.go:961-966`), `update` returns the
  stored `cfg` after the skip (`:746`), and `podium layer register` prints that
  JSON verbatim (`cmd/podium/layer.go:245-247`). The behavior is what §7.3.1
  prescribes today (`spec/07-external-integration.md:95`, `:97`), what
  `spec/14-common-scenarios.md:130` documents, and what
  `pkg/registry/server/layer_register_class_test.go:36` pins. It grants nothing:
  the resolution can only narrow, the body's `public`, `organization`, `groups`,
  and `users` are read only on the admin-defined arm (`:868-873`), a body-supplied
  owner is overwritten by `caller.Sub` (`:860-862`), and an anonymous caller
  asserting `user_defined: true` is already refused (`:804-809`). Nothing in the
  confirmed defect runs through it, and no UI requirement needs it: the register
  form hides the class control from `layer_capabilities` before any request is
  sent. Refusing instead would promote an undocumented request field into a
  normative tri-state contract, change a wire type across two operations, invent
  a `details.constraint` discriminator for the class axis, rewrite the pinning
  test and two cells of `pkg/registry/server/layer_write_auth_test.go`, and
  change `pkg/registry/server/layer_visibility_test.go:34` from "200 and ignore"
  to a refusal, all inside a security change's blast radius. Two smaller items
  are recorded rather than staged: `update` emits an audit event for a patch that
  changed no field (`:745`), and `podium layer register --public` by a non-admin
  exits `0` having applied nothing (`cmd/podium/layer.go:115-125`, `:230-240`).
  Either is its own proposal with its own §7.3.1 sentence.
- **The account cluster's raw-subject rendering.** `web/design/README.md:91` and
  `:200` specify the identity chip and the account menu as carrying a name and an
  email while `web/ui/src/App.tsx:1300-1313` renders the raw subject. The gap
  predates this defect and is fixable without any part of this change. Closing it
  here would add a `name` claim read in `pkg/identity/runtime.go`, a `Name` field
  on `pkg/identity.Identity` and on `pkg/layer.Identity`, three copy sites in
  `internal/serverboot/identity_verify.go`, and an amendment to §6.3.3, which is
  a security-critical verification paragraph, for a display string. `layer.Identity`
  is the struct `VisibleWith` matches on (`pkg/layer/composer.go:83`, `:94`), so a
  display-only field there needs a comment to keep it out of an authorization
  path, and not adding the field is the stronger guarantee. The name is also
  absent on the §6.3.3 trusted-headers path and on any token that omits it, and
  it depends on a scope an operator may narrow (`spec/06-mcp-server.md:137`,
  `:145`), so it would need a configurable claim name on the
  `PODIUM_OAUTH_SUBJECT_CLAIM` precedent (`:104`). Reporting `email` alone would
  close the rendering on every provider that carries one with no §6.3.3
  amendment, and it is the smaller first step whenever this lands; it is left out
  here so the authorization change carries no presentation payload. It lands as
  its own proposal, amending the same §7.3.4 closing sentence a second time.
- **A configuration key naming permitted local-layer roots.** D1 records why.
  With no key there is no path comparison to specify, no startup validation, no
  effective-config row, no prefix-boundary or unresolvable-path edge cases, and
  no `details.allowed_roots` disclosing configured host paths to any
  authenticated caller.
- **A new §6.10 error code for the local-source refusal.** D2 records why. The
  refusal reuses `auth.forbidden` with `details.constraint`, so no §6.10 block,
  no `tools/matrix/matrices.go` axis entry, no `errorCodeRegistry` entry, and no
  `docs/reference/error-codes.md` row is added.
- **A boot-time scan of stored layers.** No background pass rewrites, disables,
  or deletes stored layers, and no boot log counts the layers the rule would
  refuse. `ListLayerConfigs` is per-tenant (`pkg/store/store.go:415`) and a
  multi-tenant boot binds `multiTenantUnrouted`
  (`internal/serverboot/serverboot.go:895-897`), so such a count would report
  zero on the deployment with the most layers to act on. The rule is evaluated
  per operation, and `CHANGELOG.md` carries the operator action.
- **`GET /v1/whoami` or a general capability endpoint.** The posture read serves
  the web UI and is registered with it
  (`pkg/registry/server/webui_auth.go:17-20`), so a CLI or SDK caller learns its
  refusals from the responses alone.
- **A pre-flight authorization check in the CLI.** Adding a posture read to
  `podium layer` would make it depend on a route registered only with the web UI.
- **A third grant table beside §4.7.1 and §4.7.2.** A `local` layer is a
  statement about the registry host's filesystem, and the §4.7.2 admin arm is the
  grant the layer endpoint already reads. §4.7.1 operator authorization is
  unchanged: `PODIUM_OPERATOR_ADMINS` authorizes `/v1/admin/tenants`, and
  `layer_capabilities` reports the §4.7.2 grant.
- **Any change to `authorizeLayerWrite`, its arms, or the read filter proposal
  0015 landed.** This proposal reports those rules and adds a rule beside them.
- **Per-artifact or per-path authorization inside a layer.** §4.6 keeps
  visibility at the layer.
- **Sandboxing the registry process, dropping privilege, or reading a path as the
  registrant's uid.** Those are deployment controls and belong in operational
  guidance.
- **Path or content controls inside a `git`-source layer's tree.** The git
  provider reads go-git tree objects, where a symbolic link blob's content is the
  link target string rather than the target's bytes, and a `git` tree read is
  not among the ingests the staged confinement paragraph covers. Which caller
  may name a repository the file transport resolves
  to a host path is governed by the local-source rule and is staged in CODE-4,
  because that registration reads a host directory with the registry process's
  rights and is otherwise a bypass of the gate on the exact axis §7.3.1 names as
  safe.
- **The per-layer inbound webhook's authorization model.**
  `pkg/registry/server/webhook_ingest.go` keeps authorizing the delivery on the
  per-layer secret alone, and no session, role, or subject is introduced there.
  What this proposal adds on that path is the local-source rule's fifth call
  site, staged in CODE-4, because a stored `git` layer whose repository string
  resolves to the file transport names a host path and the handler drives the
  same `runIngestAndRespond` the guarded `reingest` drives.
- **Formatting the §6.10 envelope in the CLI.** Every layer command dumps the raw
  body today, and the envelope here is written so the raw dump is readable.
- **Group, scope, or quota reporting on the posture read.** That is a different
  disclosure argument, and `web/design/README.md:200` already rules group
  memberships out of the account menu.
- **A role badge in the account cluster.** The capabilities decide whether a
  control is rendered.
- **A scoped reorder inside a block the caller writes only part of.** The reorder
  handler authorizes every layer the request names and refuses the whole call on
  the first failure (`pkg/registry/server/layers.go:1124-1136`), and the panel
  names the moved row's whole class block, so narrowing the request to the rows
  the caller writes needs a server change to keep the positional assignment
  sound. The handle on such a block is rendered disabled instead (D13), and a
  scoped reorder is a separate proposal.
- **A fix for the reingest existence oracle.** `reingest` answers
  `404 registry.not_found` for an unknown ID and `403` for a layer the caller
  cannot write, so a caller can distinguish "no such layer" from "a layer I
  cannot see" by naming IDs. It is orthogonal to every requirement here and
  closing it means changing a refusal code on a path this proposal does not
  otherwise touch. It is recorded rather than fixed silently.
- **A `§13.12` row for `PODIUM_MAX_USER_LAYERS`**, which exists in
  `internal/serverboot` and appears nowhere in `spec/` under that name. The gap
  is named rather than closed here.
- **Any compatibility shim, dual code path, deprecation period, or flag
  restoring the prior behavior.** Podium is pre-1.0 and this is a MINOR bump.

## Resolved in adversarial review

Review rounds populate this section.

### Pass 1 (2026-09-01, automated)

The draft's first challenge pass, whose corrections are folded into the text
above.

- **The registration-class rule was dropped in full.** It fixed nothing the
  confirmed defect needed, its `update` premise ("answering `200` reports a
  change that did not land") was falsified by the same response-body disclosure
  the draft used to acquit `register`, and it promoted an undocumented wire field
  into a normative tri-state contract. Its spec amendment, its code change, its
  CLI owner guard, its test, its error discriminator, and its documentation text
  went with it, and Non-goals records the two smaller items it noticed.
- **The permitted-roots configuration key was dropped in full.** It was unset by
  default, authorized nobody by default, and served a deployment §4.6 says the
  `local` source is not for. Its spec row, its `internal/serverboot` reader and
  startup validation, its effective-config row, its `details.allowed_roots`
  disclosure, its five edge-case tests, and its boot-time grandfathered-layer
  count went with it, and the capability object collapsed to one member.
- **The new §6.10 code was dropped.** §6.10's own tenant-management precedent
  declines a new code for a distinct authorization axis, and the draft's reason
  for keeping it, that the register form could not otherwise decide whether to
  render the source control, was falsified by the draft's own client design,
  which decides from the posture read before any request is sent.
- **The `name` claim and the account cluster were dropped.** Reporting `email`
  closes the same rendering with no §6.3.3 amendment, and the account cluster
  reads no capability at all, so it does not belong in an authorization change.
  The UI challenge went further and carved the cluster out whole, which drops the
  `email` field from this proposal as well; the cluster lands as its own proposal
  with its own claim-configurability decision. This is the one place where two
  challenge sketches disagreed, and the narrower one was applied.
- **The confinement changed mechanism and grew scope.** A `Close func() error`
  on `Snapshot` is the construct §9.3 forbids on an SPI return value, so the
  confinement resolves each open through `os.OpenInRoot` and holds no descriptor,
  which removes `pkg/layer/source/source.go`, the orchestrator, and the close
  helper from the target list. The three bootstrap sites that build the same tree
  with `os.DirFS` were added, without which the change confined an API-registered
  layer and left the same directory unconfined under `PODIUM_LAYER_PATH`. The
  claim that `os.Stat` following links justified the change was removed: a
  symlinked root is admitted by `os.OpenRoot` too, and is legitimate.
- **The local-source predicate was made consistent across the four call sites.**
  The draft guarded `reingest` on `SourceType == "local"` alone, which misses a
  stored layer of a custom §9.1 source type carrying a path that the orchestrator
  still hands to the provider. One predicate now reads both values at all four
  sites, and TEST-4 carries the case.
- **The `layerAdmin` hoist in `internal/serverboot` was dropped.** The
  single-expression guarantee comes from `Capabilities` being a method reading
  `e.authAdmin`, which the posture literal binds by method value, so naming the
  closure in a local variable adds no coupling that already exists.
- **The blast radius into existing tests was recorded rather than discovered
  during implementation.** `pkg/registry/server/layer_write_auth_test.go` seeds a
  `local` source with `/tmp/seed` and asserts `200` on the owner `restore` and
  owner `reingest` cells under a denying admin arm, so those cells move onto a
  `git` source in the same step.
- **The fixture-level anti-drift test was replaced.** `newBrowserStack` does not
  call the boot path, so a six-posture matrix there would assert the test author's
  own wiring. The substantive assertion moved to a same-package unit case against
  the real evaluator, and the boot-level guarantee moved to the end-to-end case,
  which is the only level that reaches the real closure.
- **The web fixture default was derived rather than closed.** A blanket closed
  default would have forced an edit at 256 public-mode and no-identity call sites
  and would have let a fixture state a posture body the registry can never emit.
  The factory now derives the capability from the posture fields it already
  carries.
- **The drag handle stays rendered and disabled.** Removing it cost a non-admin
  the affordance on any tenant carrying one public or organization-scoped
  admin-defined layer, and the panel already carries a disabled reordering arm
  with a titled explanation. The corresponding open question was closed.
- **The design boards were added to the documentation change.** Rewriting only
  the prose left a corpus whose boards draw controls the panel no longer renders.
  The corresponding open question was closed, on the precedent of commit
  63f7186.
- **The hand-run scenarios were re-placed and re-premised.** The new scenarios
  sat after S54 on a stack S50's cleanup tears down, so each now carries an
  explicit prerequisite line; the account-cluster assertions were dropped with
  the account cluster; S50's replacement step uses the bearer token the scenario
  already mints rather than a cookie extracted from a browser profile; and the
  two curl-only scenarios were dropped as a third copy of coverage TEST-4 and
  TEST-10 already carry through the compiled binary.

### Pass 2 (2026-09-01, automated)

- **The update form was added to the client change.**
  `web/ui/src/surfaces/UpdateLayerForm.tsx` built its patch as
  `{ local_path: localPath, root }` on every non-`git` layer (`:73-75`), so
  after CODE-4 a non-admin owner of a local layer was refused on every patch the
  form could send, including a Root-only one, while UI-2 deliberately kept
  `Edit` for that caller. The `Local path` field is now rendered only where the
  caller may name a filesystem path, and the patch omits `local_path` otherwise.
- **`Restore` was moved off `canWrite`.** CODE-4 guards `restore` on the stored
  config's source type and path, so a non-admin owning a tombstoned local layer
  passed `canWrite`, saw `Restore`, and was refused on every press. One predicate
  now covers both guarded per-row operations.
- **The client predicates were renamed and consolidated.** `canReingest` and the
  missing restore predicate collapsed into `canReingestOrRestore`, and
  `canRegisterLocalSource` became `canNameHostPath`, which the register form and
  the update form both read. `namesHostPath` and `canNameHostPath` are exported
  so the two surfaces state one rule.
- **`Reingest all` and the drag handle were re-anchored to their real sites.**
  The `Reingest all` bullet pointed at the per-row drag handle's `aria-label`
  (`:1153-1159`) rather than at the header control (`:538-547`), and the
  drag-handle bullet pointed at the footer's `reorderable` local (`:758`) and the
  `precedence-label` (`:584-589`) rather than at the handle. Both now name every
  site, and `reingestAll`'s target list narrows so a qualifying caller does not
  collect a refusal for every other row.
- **The confinement's error classification was specified.** The staged §7.3.1
  sentence promised `ingest.source_unreachable`, but the only existing wrap is on
  the walk callback's directory error; a refused `fs.ReadFile` reached the
  handler unwrapped and was coded `registry.unavailable` at HTTP 500 with
  `retryable: true`. `ConfinedFS` now returns the refusal wrapped in
  `source.ErrSourceUnreachable`, so the classifier is the tree and
  `pkg/registry/ingest` needs no edit. TEST-2 split into an ingest case and a
  `pkg/registry/server` envelope case, because the envelope is observable only
  from the latter.
- **The ingest-failure granularity was corrected.** The edge-case table claimed
  the walk did not abort. `loadOne` returns the refusal to its walk callback,
  `walkLayer` returns it, and `Ingest` returns before persisting, so one escaping
  read fails the layer's whole ingest. The staged confinement paragraph, the
  edge-case row, TEST-1's wording, and the `CHANGELOG.md` entry now say that, and
  a second row records that `walkDomains` discards a `DOMAIN.md` read failure and
  continues.
- **The bootstrap confinement gained a test that can observe it.**
  `TestLocal_BootstrapTreeIsConfined` asserts the constructor and cannot see
  whether the three bootstrap sites call it, so TEST-10 gained an end-to-end case
  that boots the binary over a `--layer-path` tree holding an escaping link, and
  S10 depends on S5.
- **`test/integration/layer_write_authorization_test.go` was added to the blast
  radius.** It wires a denying admin arm, seeds `alice-personal` as a `local`
  source, and asserts that its non-admin owner reingests it at `200`, which
  CODE-4 turns into a `403`. It is the only integration-level test of the write
  rule, so S6 now reaches the integration level.
- **The `git` carve-out was closed.** `register` validates nothing about `repo`
  and `Git.Snapshot` passes it straight to `git.CloneContext`, whose default
  protocol map registers `file`, so a caller refused on `source_type: local`
  could register `repo: /srv/<victim>` and read the same directory. The rule now
  reads the repository string through a fail-closed classifier, and the staged
  §7.3.1 sentence states the file-transport arm instead of asserting the whole
  source type is safe.
- **The public-mode framing was aligned with the existing spec.** §7.3.1 and
  §4.7.1 both say such a registry authenticates no caller so no caller can hold
  the admin role and the endpoint admits there. The staged paragraph said the
  admin arm admits every caller there, which contradicted both. SPEC-5's bullet
  said the capability is false where no subject resolves, which contradicted the
  proposal's own edge case, its fixture derivation, and board 14i; it now reports
  what the deployment's endpoints do and reserves false for a capability the
  deployment did not determine.
- **The design-corpus authority claim was re-anchored.**
  `web/design/README.md:29` is the fixed-width and non-interactivity caveat.
  The authority sentence is at `:27` and covers a disagreement over a number, and
  `:7` subordinates the whole handoff to the spec. The board rewrite now rests on
  SPEC-7. `web/DESIGN.md:464-471` was added to DOC-4, because it states both the
  rule SPEC-5 falsifies and the unanswered-read rule D11 reverses.
- **The CLI usage-string citations were corrected.** `cmd/podium/layer.go:185`
  declares `--root`; `register`'s `--local` is at `:186`, and `update`'s `--local`
  at `:79` reads "filesystem path" with no parenthetical, so each site now
  carries its own current string and its own replacement.
- **`docs/reference/http-api.md`'s per-operation restatements were staged.** The
  page states the authorization rule again under `### Reingest` and under
  `### List soft-deleted layers and restore`, and both would have been left
  stating the superseded rule.
- **The added hand-run scenarios were made executable.** The S44 stack seeds no
  admin grant and sets no `PODIUM_BOOTSTRAP_ADMINS`, and the grant endpoint needs
  an existing admin, so S56's grant and all of S57 could not be run. The two
  scenarios now re-stand the stack with a bootstrap operator who is not the
  signed-in caller, register the caller's own user-defined layer while the grant
  is in force, and issue the grant and the revoke as runnable requests. All three
  added scenarios were rewritten in the file's convention.
- **Two stale documentation edits were corrected.** The staged correction to a
  scenario count in `test/manual-validation.md` was dropped, because the document
  states no count anywhere, and S50's Goal paragraph was staged, because no
  remaining step performs the panel refusal it claims to validate.
- **The webhook ingest was added as the rule's fifth call site.** Pass 2 put a
  file-transport `git` repository string on the host-path arm, which made
  `handleWebhook` an unguarded door onto the same path: it refuses a non-`git`
  layer (`pkg/registry/server/webhook_ingest.go:55-56`) and then drives the same
  `runIngestAndRespond` the guarded `reingest` drives (`:79`,
  `pkg/registry/server/layers.go:1223`), consulting `authAdmin` nowhere. A
  stored layer whose `Repo` is `/srv/<victim>`, which is the grandfathered
  record §7.3.1 says is refused at its next such operation, was reingested by
  anyone holding the per-layer secret. CODE-4's table, the staged §7.3.1
  paragraph, TEST-4, and the Non-goals bullet that still justified the omission
  now agree that the rule runs there too, and that a repository string naming a
  network endpoint is unaffected.
- **S56 step 1's registration command was made runnable.** It passed
  `--source local`, which `layerRegister` does not declare
  (`cmd/podium/layer.go:180-193`) and which `flag.ContinueOnError` turns into a
  non-zero exit before any request, and it exported `PODIUM_TOKEN`, which only
  `readPublishToken` reads. `doJSON` attaches `readCLIToken`
  (`cmd/podium/layer.go:535`, `cmd/podium/main.go:1419-1439`), so with S44's
  Prerequisites unsetting `PODIUM_SESSION_TOKEN` (`test/manual-validation.md:51`)
  the CLI would have reached the registry anonymously and been refused for
  resolving no verified subject rather than for the reason the Expect names. The
  step now uses `--local` alone and `PODIUM_SESSION_TOKEN`, which is the file's
  own convention.
- **`web/ui/src/App.tsx` was added to the client change.** The new capability
  prop on `LayerPanel` and the new capability and `subject` props on
  `DeletedLayers` are breaking interface changes whose only call sites are
  `App.tsx:555` and `:553`, inside a `Surface` component that holds no posture of
  its own (`:526-539`). The shell now derives `capabilitiesOf(posture)` once
  beside `subject` (`:322`) and threads it, so the closed default is applied at
  one site and the enumerated client change compiles.
- **TEST-10's bootstrap claim was narrowed and split.** A `--layer-path` boot
  reaches only `internal/serverboot/serverboot.go:455`, because
  `bootstrapDeclaredLayers` returns immediately on an empty `cfg.declaredLayers`
  (`:596-598`) and `NewFromFilesystem` has no non-test caller outside
  `internal/testharness/registryharness`. The case now carries a second arm that
  boots with a `registry.yaml` declaring a `local` layer, which is what reaches
  `:616`, and TEST-2 gained `TestNewFromFilesystem_IngestIsConfined` for
  `pkg/registry/server/server.go:338`, which no invocation of the binary reaches.
  S5 and S10 were corrected to match.
- **CODE-1's wording pin dropped a citation that pins nothing.**
  `test/e2e/quickstart_flow_test.go:165` is the second line of a source comment
  quoting the message, and the test asserts only that the output contains
  `error` and the artifact id (`:166`) and mentions `SKILL.md`
  case-insensitively (`:169`), all of which the wrapped branch still satisfies.
  The verbatim-message constraint was moved onto
  `test/e2e/artifact_types_test.go:639` and
  `test/e2e/frontmatter_schema_test.go:103`, which both match the literal
  `missing SKILL.md`. Pass 4 found those two to be the wrong component as well
  and re-anchored the constraint.

### Pass 3 (2026-09-01, automated)

- **The repository classifier was replaced by a call to go-git's own parser.**
  The stated `user@host:path` predicate constrained only the host segment, while
  `MatchesScpLike` makes the user prefix optional and rejects the scp reading
  whenever the segment before the first `:` carries a `/`
  (`internal/url/url.go:13`, `:37`). `/srv/repos@h:x` was therefore classified
  as a network endpoint and admitted for a non-admin while go-git resolved it to
  `parseFile` and ran `git-upload-pack` against the host path, which is a bypass
  on the axis §7.3.1 names as safe. `isFileTransportRepo` now calls
  `transport.NewEndpoint` and tests `Protocol == "file"`, and TEST-4's table
  gained the bypass row and the `host:path` row the literal predicate
  over-refused.
- **An empty repository string is no longer classified as a host path.** By the
  previous rule `isFileTransportRepo("")` was true, so `namesHostPath` was true
  for every `update` patch and the guard refused every non-admin patch, including
  the Root-only edit UI-2 deliberately keeps the Edit control for, and refused a
  custom-source layer carrying no path on `register`, `restore`, and `reingest`.
  The classifier returns false on an empty string, CODE-4's `update` row states
  that no repository string is classified there, and the edge-case table gained
  the admitted row.
- **The `update` guard's admitted arm gained a test.** Every listed case was a
  refusal, and the amendment that moves `layer_write_auth_test.go`'s cells onto a
  `git` source would have removed the last cell in which a non-admin owner writes
  a stored local layer. TEST-4 now pins that a `{"root": "docs"}` patch from that
  caller is admitted at `200` with the stored path unchanged, and the amendment
  records that the `update`, `unregister`, and `reorder` cells keep their `local`
  seed.
- **The webhook rule was stated on the arm rather than as an absolute.** The
  staged §7.3.1 sentence and CODE-4's rationale both asserted that a
  webhook-triggered reingest of a layer naming a filesystem path is refused,
  while the installed admin closure returns nil for every request on a registry
  with no identity provider or in public mode
  (`internal/serverboot/serverboot.go:1253-1262`), which the same paragraph says
  three sentences earlier. Both now state the mode-dependent outcome, and TEST-4
  gained the admitting arm.
- **TEST-4's webhook case was re-anchored.**
  `pkg/registry/server/webhooks_test.go` is `package server_test` and covers the
  §7.3.2 outbound receivers. The inbound fixture is `newWebhookEndpoint`
  (`pkg/registry/server/webhook_ingest_test.go:16`), which takes the admitting
  constructor default and is extended to install a denying arm, and the admitted
  arms wire a runner because `runIngestAndRespond` with no runner records the
  intent alone.
- **The confinement's granularity was corrected a second time.** `Ingest`
  persists each `DOMAIN.md` and emits its `domain.published` event
  (`pkg/registry/ingest/ingest.go:472-494`) before it calls `walkLayer`
  (`:508`), so "accepts nothing from that cycle" was a normative claim the
  implementation does not satisfy. The staged §7.3.1 sentence now scopes the
  refusal to artifacts and bundled resources and states the domain outcome, the
  edge-case table carries the partial-domain row with the reason a deferral is a
  separate change, and TEST-2's fixture gained a `DOMAIN.md` and a `ListDomains`
  assertion.
- **The `SKILL.md` read was brought into CODE-1's scope.** The read discards its
  error and substitutes a message wrapping nothing
  (`pkg/registry/ingest/ingest.go:1139-1142`), so a confinement refusal on a
  `SKILL.md` reached through an escaping link lost the sentinel and was answered
  `500 registry.unavailable` with `retryable: true`. The read gains one branch
  that preserves the sentinel and keeps the existing `missing SKILL.md` wording
  on every other error, `pkg/registry/ingest` joins step S5, and TEST-2 gained
  the case. Pass 4 re-anchored which tests that wording is pinned by.
- **The two bootstrap confinement cases were restated as refusals.** Both
  asserted that the in-root artifact lands beside the refused resource, which the
  abort-on-ingest-failure path makes unreachable: `NewFromFilesystem` returns the
  error and no server, and the two `internal/serverboot` sites wrap it and abort
  startup before any listener binds. Each case now carries a poisoned arm
  asserting the refusal and a clean control arm asserting the serve, and TEST-10's
  IMPLEMENTOR'S CHOICE constraint names the expected boot failure.
- **§13.10's staged rendering sentence was narrowed to the commitment the panel
  keeps.** As written it forbade both the disabled drag handle D13 keeps and the
  reingest control D12 renders on a `git` row whose repository resolves to the
  file transport, so §11's Render cells would have derived from a rule the
  implementation does not keep. The sentence now states the withholding rule, the
  disabled-reordering exception, and the unsettleable-refusal exception, the §11
  derivation lists the added condition points, and TEST-8 gained the
  file-transport row.
- **Two anchors in the client change were corrected.** `RegisterLayerForm.tsx:298`
  is the `<SourceChoice>` call site rather than a second class-control site, so
  wrapping it in `canRegisterAdminDefined` would have removed the source control
  from every authenticated non-admin and contradicted three other statements in
  this proposal. The class control is `:275-286` with its note at `:288-292`, and
  the source control keeps rendering while `SourceChoice` gains one boolean prop
  its option array reads.
- **The design-board bullet was re-anchored.** Its named boards and its cited
  lines were disjoint sets: every cited line lies inside the topic-18 ingest
  boards, while the named boards begin far below them, and `17g` draws no
  reingest control at all. The bullet now enumerates the boards mechanically with
  a grep over the control strings, names every board that draws them today, and
  says what `17g` is edited for.
- **The posture read's two code-level restatements were staged.**
  `pkg/registry/server/webui_session.go:9-13` and `web/ui/src/session.ts:8-10`
  each say the read reports the posture and the subject "and nothing else", on
  the declaration each change amends. They are rewritten with CODE-5's and
  UI-1's edits, and DOC-4 points at them.
- **S48 was kept runnable.** Its registration names `$WORK/own-repo`, an
  absolute filesystem path, which the staged rule refuses for the authenticated
  non-admin the S44 stack produces, and the refusal would have taken S48 steps 3
  and 4, S49, and S50 with it. The repository value in step 2 and in step 5's
  terminal body becomes an `https://` URL, which is sound because `register`
  stores the string without fetching it and no step in the three scenarios
  reingests the layer.

### Pass 4 (2026-09-01, automated)

- **CODE-1's confinement classifier was given a discriminator it can actually
  compute.** The staged rule wrapped "a resolution refusal" in
  `ErrSourceUnreachable` and returned every other failure unchanged, while Go
  exposes nothing that isolates a refusal: `os.OpenInRoot` returns an
  `*fs.PathError` wrapping the unexported `errPathEscapes`
  (`$(go env GOROOT)/src/os/file.go:421`, returned at `src/os/root.go:306` and
  `src/os/root_openat.go:320`), which satisfies neither `fs.ErrNotExist`, nor
  `fs.ErrPermission`, nor `os.ErrInvalid`. An unclassified refusal is coded
  `500 registry.unavailable` with `retryable: true`
  (`pkg/registry/server/layers.go:1365-1366`,
  `pkg/registry/server/error_envelope.go:26-29`), so a permanent refusal would
  have been reported as a condition that clears on retry. The classifier is now
  the inverse: `fs.ErrNotExist` passes through unchanged and every other failure
  is wrapped, which also matches `pkg/layer/source/local.go:26-31`'s existing
  disposition that a permission failure is the same condition as a missing
  directory, and which leaves `loadOne`'s absent-`SKILL.md` message intact.
  `ConfinedFS`'s doc comment, the Summary's watch-out list, TEST-1's new
  `TestLocal_ReadClassifiesAbsentAndUnreadable`, and an edge-case row carry the
  same predicate.
- **CODE-1's retained `missing SKILL.md` wording was re-anchored.** The three
  end-to-end tests it cited run `podium lint --registry`, which reaches
  `filesystem.Open` and `reg.Walk` (`cmd/podium/main.go:941`, `:953`) and
  produces the independent copy of that message at
  `pkg/registry/filesystem/walk.go:195`, a line this change does not touch. The
  copy CODE-1 branches on is `pkg/registry/ingest/ingest.go:1141`, which no test
  asserts today. CODE-1 now says the walker's copy is what those tests pin, names
  `pkg/registry/filesystem/walk_test.go:102-105` beside them, records that the
  message is duplicated with no shared constant, and states that TEST-2's second
  arm is what newly pins the ingest copy.
- **D13's reorder-authorization citation was spelled with its path.** The
  paragraph's other bare-colon citations name
  `web/ui/src/surfaces/LayerPanel.tsx`, whose `:1123-1136` is the row's
  drag-and-drop event wiring rather than any authorization. The behavior D13
  relies on is `pkg/registry/server/layers.go:1123-1136`, and the citation now
  carries that path and says why.
- **SPEC-5's first edit was widened to the whole opening sentence pair of
  §7.3.4.** The second sentence at `spec/07-external-integration.md:181` says a
  carried credential "has it verified only so the response can report `subject`",
  which the capability the same amendment adds falsifies: `Capabilities` reads
  the `authAdmin` callback, which on an identity-provider deployment is
  `registry.AdminAuthorize(r.Context(), layerIdentity(r))` over the same
  verifying resolver the read passes as `Identity`
  (`internal/serverboot/serverboot.go:1253-1262`, `:1324`). Both sentences are
  now replaced, CODE-5 rewrites the same clause on the Go mirror
  (`pkg/registry/server/webui_session.go:9-15`), and the checklist's S3 and the
  Summary name the widened edit.
- **The shell's failed-posture-read comment was staged.**
  `web/ui/src/App.tsx:214-217`, inside `readSession()`'s rejection handler,
  states that an unanswered read leaves the page rendering "the layer panel with
  its write operations", which D11 reverses. DOC-4 now enumerates it beside the
  other client comments and says what it is rewritten to.
- **The user-defined note stays rendered for every caller.** UI-2 gated
  `RegisterLayerForm.tsx:288-292` on `canRegisterAdminDefined`, while S48 step 2's
  Expect asserts that note for an authenticated non-admin
  (`test/manual-validation.md:4777-4779`), so a correct implementation would have
  failed the hand-run step. The note describes the class the caller is about to
  get rather than a choice they cannot make, and `userDefined` opens on
  `subject !== ''` (`:64`), so only the class control at `:275-286` is gated. The
  edge-case row for the register dialog carries the same rule.
- **The design brief's register-dialog prescription was staged.**
  `web/design/README.md:161` states the source segmented control unconditionally,
  while UI-2 renders `Local folder` only where `canNameHostPath` and DOC-4
  already edits the boards that draw the same dialog. The line joins DOC-4's
  prose list with the rewrite it takes.
- **The Pass 3 subsection was moved into "Resolved in adversarial review".** It
  sat at the end of "Documentation changes", where a reader following the pass
  numbering could not find it.

### Pass 5 (2026-09-01, automated)

- **The reorder predicate was moved to the granularity of the request.**
  `canReorder` was evaluated over the whole visible set, while the panel sends
  `movedOrder(blockOf(rows, from), from, onto)` (`:478`) and the handler
  authorizes the ids that request names (`pkg/registry/server/layers.go:1124-1136`),
  so one admin-defined row anywhere in the list disabled the handle on a
  non-admin's own user-defined rows, which is the affordance D13 exists to keep.
  The predicate is now `canReorderBlock(block, caps, subject)` over
  `blockOf(rows, layer.ID)`, the panel-level `reorderable` and `precedence-label`
  arms read whether any block qualifies, D13's premise is corrected, SPEC-7's
  staged sentence names the layers a move from that row would reorder, and
  TEST-8's mixed-set case, the edge-case row, and S56 steps 2 and 3 assert a live
  handle on the caller's own block beside a disabled one on the admin-defined
  block.
- **The confinement's symbolic-link arm was split into the relative and the
  absolute case.** `os.Root` refuses a symbolic link whose target is absolute
  before comparing that target against the root
  (`$(go env GOROOT)/src/os/root.go:43`, `:301-306`, reached from
  `src/os/root_openat.go:363`), which was confirmed on go1.26.3, so the staged
  sentence "A symbolic link that resolves within the directory is read" asserted
  a behavior the staged implementation does not have and hid a regression for
  layers holding absolute intra-tree links. The staged §7.3.1 sentence, the
  `ConfinedFS` comment, a new edge-case row, a new
  `TestLocal_ReadRefusesAbsoluteSymlinkInsideRoot` arm, the `CHANGELOG.md`
  operator action, and `docs/deployment/layers.md` now carry the split, and
  `TestLocal_ReadAdmitsSymlinkInsideRoot` states its relative target explicitly.
- **`update` stopped classifying a field the handler drops.** The call-site table
  read the patch's `source_type`, while `update` assigns only `ForcePushPolicy`,
  `Ref`, `Root`, and `LocalPath` (`pkg/registry/server/layers.go:687-698`) and
  never reads `patch.SourceType`, so a patch of
  `{"source_type":"local","ref":"main"}` would have been refused while patching
  no filesystem path, which the staged §7.3.1 sentence does not authorize. The
  row now reads the patch's `local_path` alone on the reasoning the row already
  gave for `repo`, and TEST-4's `update`-guard case gained the admitted cell.
- **The `layer_write_auth_test.go` amendment gained the helper change it
  needs.** `seedLayer` sets `LocalPath` to `/tmp/seed` whenever the field is
  empty, independently of the source type (`:105-117`), so a cell moved onto a
  `git` source still reached the endpoint carrying a filesystem path and still
  took the new refusal on the path disjunct. The helper's default is now
  conditioned on a `local` source type, the `layerWriteOp` table's single shared
  seed is named as the reason the `restore` and `reingest` cells carry their own
  source, and the Summary watch-out records both.
- **§13.10 gained the registration surface it was being cited for.** The staged
  rendering sentence covered per-row controls alone, while UI-2 withholds the
  register dialog's class control and its `Local folder` source option, which
  belong to no row, and three sites cited that sentence for them. SPEC-7 now
  states the dialog's rule, its §11 derivation ranges over the dialog's condition
  points, and the client half of the maintainer's local-source requirement has a
  spec statement.
- **The design corpus's remaining unconditional prescriptions were staged.**
  `web/design/Podium UI Inventory.dc.html:780` states that a row the caller does
  not own keeps its write controls, which UI-2 reverses, and
  `web/design/README.md:148` and `:157` prescribe `Reingest all` and `Restore`
  unconditionally, which UI-2 makes conditional. All three join DOC-4's prose
  list with their rewrites, and the inventory's `LayerRow` signature at `:779`
  gains the inputs the row now reads.
- **TEST-4's two anchors into `layer_write_auth_test.go` were corrected.** The
  amendment cited `:161` for the owner caller row and `:173` for the table's
  shared seed. `pkg/registry/server/layer_write_auth_test.go:161` closes the
  `callers` struct literal and `:173` is the `if op.tombstoned {` guard; the
  owner row asserting `http.StatusOK` is `:162` and the single `seedLayer` call
  the amendment turns on is `:172`. Both anchors now name those lines.

### Pass 6 (2026-09-01, automated)

- **DOC-1 gained the reference page's copy of the §7.3.4 sentence pair SPEC-5
  replaces.** `docs/reference/http-api.md:62` carries "a request that carries one
  has it verified only so the response can report `subject`" verbatim, which is
  the same clause SPEC-5 replaces in `spec/07-external-integration.md:181`
  because the capability the amendment adds is the second thing that credential
  decides. The bullet enumerated the `/v1/ui/session` edits without it, so after
  the change the spec would have said the credential is verified "so the response
  can report `subject` and evaluate `layer_capabilities`, and for no other
  purpose" while the reference page still said "only so the response can report
  `subject`", a contradiction on a security-relevant disclosure statement. The
  bullet now stages that opening prose for rewriting to SPEC-5's wording and
  names it beside the other mirrors of the pair, the Go declaration comment
  CODE-5 rewrites (`pkg/registry/server/webui_session.go:9-15`) and the
  TypeScript comment UI-1 rewrites (`web/ui/src/session.ts:8-10`). The checklist's
  S11 already names the page rather than its individual edits, so it is unchanged.

### Redesign 1 (2026-09-01, automated)

The client-rendering area was redesigned as a whole. It had accumulated one
predicate per control, one rendering sentence per surface in §13.10's staged
text, one §11 condition point per surface situation, and one UI-2 bullet per
file, so each control added later would have needed a name, a spec sentence, a
condition point, and a bullet of its own. The redesign replaces that enumeration
with one predicate, `mayTake(op, target, caps, subject)`, keyed on the §7.3.1
operation a control would take and on the target that operation would name,
which is the stored record for `unregister`, `restore`, `reingest`, and
`reorder`, the stored record's class and owner with the patch's fields for
`update`, and the registration the dialog would build for `register`. The
sections rewritten are the Summary's web-client bullet and its fixed decisions,
D12, D13's heading, SPEC-7's staged §13.10 text and its §11 derivation, UI-1,
UI-2, the edge-case table's rendering rows, TEST-8, the S47 and S56 hand-run
amendments, and DOC-4's brief and board paragraphs.

Deleted outright:

- `canWrite`, `canReingestOrRestore`, `canReorderBlock`, `canNameHostPath`, and
  `canRegisterAdminDefined` from UI-1's module. Each is `mayTake` at a fixed
  operation, and the last two are `caps.manage_any_layer` read under two names.
  No `canRegister` is added in their place.
- SPEC-7's surface-shaped sentences in the staged §13.10 text, which stated the
  per-row rule, the registration dialog's rule, and the two exceptions
  separately. One rule with its narrowed-request and resolves-away clauses, two
  exceptions, and one boundary replace them.
- SPEC-7's enumerated list of §11 condition points, which named nine surface
  situations. §11 derives cells from the variables the statement branches on,
  and the rewritten paragraph names those variables directly.
- The per-control conditions in UI-2's four surface bullets, replaced by one
  table whose rows carry a site, an operation, and a target, and by a
  consequence list for the effects that are more than a presence decision.

Added by the same pass: the rule's boundary for a control whose availability
turns on the layer record alone, which covers the webhook-rotation checkbox and
`editableVisibility`; the `Register layer` control, which the rule withholds
from a caller who resolved no subject on a registry that authenticates its
callers; the empty state's matching arm and the S47 step 3 hand-run reading of
it; and the register arm of the unsettleable-refusal exception, since the
register dialog keeps offering the `git` source whatever repository string the
caller types.

Open decisions the redesign recorded, each applied at its stated default:

- **Operation-keyed predicate against per-control predicates.** Applied the
  operation-keyed predicate, which deletes five exports where the alternative
  adds a sixth. The alternative keeps each call site reading a predicate named
  for what it authorizes, at the cost of an export, a rendering sentence, and a
  condition point per control.
- **The empty state's no-caller arm.** Applied the rewrite. The arm is keyed on
  the posture read having answered and resolved no subject rather than on the
  register control's absence, because that control is also withheld where the
  read did not answer, and the layer list is a separate request that still
  carries the caller's credential.
- **The adversarial-review pass log.** Left verbatim. Passes 1 to 6 keep naming
  the predicates this redesign deletes, because the log records what each pass
  decided at the time.
- **Where the boundary is stated.** Stated in the §13.10 text, in UI-2, and in
  TEST-8, so a reader does not take `editableVisibility` for an unstated
  instance of the rule.

One staged edit was relocated. The redesign anchored S47's `**Covers.**`
amendment on text in `test/manual-validation.md` rather than on text in this
proposal, so the amendment is staged as prose in the Manual validation section
beside the S47 step 3 amendment.

### Pass 7 (2026-09-01, automated)

- **The panel could not tell an unanswered posture read from an answered one
  that resolved no caller.** UI-2 staged two empty-state arms and TEST-8 staged
  a case for each, while the only values threaded into the panel were the
  capability object and the subject. Those two values are identical in the two
  states: `capabilitiesOf` reports every member false for both (UI-1), the
  shell holds `null` for a failed read (`web/ui/src/App.tsx:219`) and derives
  the subject from it at `:322`, and the discriminator lives in the shell alone
  (`:77-78`, `:309`). The panel's props carry neither
  (`web/ui/src/App.tsx:555`, `web/ui/src/surfaces/LayerPanel.tsx:160-177`). The
  shell now derives `postureAnswered` beside `caps` and threads it along the
  same route into `<LayerPanel>`, the panel's signature takes it, the
  empty-state arm is keyed on the read having answered and the subject being
  empty (Pass 8 added the capability conjunct that arm also carries), and TEST-8
  drives the unanswered case by rendering with the flag false and the same
  subject and capability object the answered-anonymous case uses. UI-1 records
  that the accessor collapses the two states deliberately and that the flag is
  the one place they read apart, SPEC-7's §11 derivation records that the flag
  is a point for the empty-state copy rather than for the control rule, the
  Summary's trap list and web-client bullet name it, and S9 names it.
- **The staged §7.3.1 confinement paragraph made `domain.published` fire on
  every refused cycle.** `Ingest` persists each domain record unconditionally
  (`pkg/registry/ingest/ingest.go:476`) and emits the event only where the
  `DOMAIN.md` is new or its stored source changed (`:479-494`), which is what
  §8.1 (`spec/08-audit-and-observability.md:16`) and §7.3.2
  (`spec/07-external-integration.md:109`) already state. As written, a layer the
  confinement refuses on every cycle would have emitted one event per domain per
  cycle to §7.3.2 receivers and the §8 audit sink, which a watch loop or a
  webhook delivery re-drives indefinitely. The staged sentence now qualifies the
  emission on the added-or-changed condition, the edge-case row states the
  persist and the emission separately with their own anchors, DOC-3's
  `CHANGELOG.md` entry carries the same qualification, and TEST-2's
  `TestSourceIngest_EscapingResourceFailsTheLayer` gains a second refused cycle
  over the same unchanged tree asserting that the record stays while no further
  event reaches either seam.
- **DOC-1 staged the §4.7.2 registry qualification onto a page whose tier runs
  no registry.** `docs/deployment/local.md` documents the tier with no server
  process, no database, and no identity provider (`:9`, `:15`), so a §7.3.1
  authorization rule keyed on the §4.7.2 admin grant has no caller to place on
  either arm there. That site is removed from the restatement list, which keeps
  `docs/getting-started/concepts.md:211` and `docs/deployment/clustered.md:32`,
  and the local page's bullet takes the scope half alone with a cross-link to
  `docs/deployment/layers.md`.
- **TEST-10's bootstrap-confinement assertion matched a string the boot never
  prints.** The sentinel renders as its message alone,
  `source: unreachable` (`pkg/layer/source/source.go:91`,
  `pkg/spi/errors.go:33`), and both bootstrap wraps
  (`internal/serverboot/serverboot.go:485`, `:637`) reach the operator through
  `cmd/podium/serve.go:105-106`. The §6.10 code string
  `ingest.source_unreachable` is written only by the HTTP envelope
  (`pkg/registry/server/layers.go:1362`), which the bootstrap path does not
  reach, so the staged assertion would have failed against a correct
  implementation. Both the assertion and the IMPLEMENTOR'S CHOICE constraint now
  name the layer id and the rendered sentinel text.

### Pass 8 (2026-09-01, automated)

- **The empty state's no-caller arm fired on a registry that authenticates no
  caller, where the register control is drawn and the registration succeeds.**
  The arm was keyed on `postureAnswered && subject === ''`, and a registry
  started with no identity provider or in public mode satisfies both: the
  posture read writes `subject` only for an authenticated caller
  (`pkg/registry/server/webui_session.go:54-58`) while the installed admin
  closure admits every request (`internal/serverboot/serverboot.go:1253-1262`),
  so `manage_any_layer` is true, `Register layer` is rendered, and
  `pkg/registry/server/layers.go` admits the registration. The first run of
  `podium serve --standalone --web-ui` would have drawn the no-caller line above
  a working control. The arm now carries the capability conjunct,
  `postureAnswered && !caps.manage_any_layer && subject === ''`, which is the
  predicate `mayTake('register', …)` reduces to on the target UI-2's control
  table names for that control. UI-2 states the conjunct and why it is
  load-bearing, the Summary's web-client bullet names the same three-part
  predicate, the edge-case row for a registry started with no identity provider
  or in public mode now records that the panel keeps `Register layer` and the
  register instruction, TEST-8 gains the case that drives `postureAnswered`
  true, an empty subject, `manage_any_layer` true, and no visible row and
  asserts both, and the Pass 7 record's restatement points at this pass for the
  added conjunct.
- **DOC-1 staged a false sentence onto `### Update a layer`.** The bullet
  directed the writer to record `local_path`, a `local` source type, and a
  `repo` as carrying the local-source authorization in the register **and**
  update bodies. The update arm is scoped to a patched filesystem path: the
  staged §7.3.1 sentence says "patching a stored layer's filesystem path",
  CODE-4's call-site table classifies "the patch's `local_path` alone", and
  TEST-4 pins a patch echoing `{"source_type":"local","ref":"main"}` back as
  `200`. The page also declares `source_type` immutable and accepts no `repo`
  patch field (`docs/reference/http-api.md:381`). The bullet is now split by
  operation: the register body records the three carriers, and the update body
  records that a patch carrying `local_path` is on the arm and a patch carrying
  none is not reached by the rule.

### Pass 9 (2026-09-01, automated)

- **The staged ingest-confinement paragraph promised a bound on the
  file-transport `git` arm that nothing implements.** The paragraph above it
  brings a `git` repository string resolving to the Git file transport onto the
  local-source authorization arm, and the confinement paragraph then took "a
  layer that names a filesystem path" as its subject, which reads that class in.
  CODE-1 confines `pkg/layer/source/local.go` and the three bootstrap sites
  alone; `pkg/layer/source/git.go` clones through `git.CloneContext` with no
  root and no path check (`pkg/layer/source/git.go:48-56`), and Non-goals
  already states that the confinement is not engaged for a `git` source. The
  confinement paragraph's subject is now "an ingest that reads a layer's
  configured filesystem path as a directory", and the paragraph closes by
  stating that a repository string resolving to the file transport is fetched
  through that transport rather than read as a directory, so the authorization
  rule is the control on that arm. The same predicate now stands in the
  Summary's §7.3.1 bullet and in the fixed decision on the two controls, and the
  residual-risk statements that credited the confinement with a bound it does
  not provide are corrected: D9 and OQ-1 say that on the file-transport arm the
  bound is the admin gate and the registry process's own rights, the
  public-bind edge-case row says the same, and the tenant-admin edge-case row
  now states that the process uid is the bound there with the named directory
  added only on the arm the confinement covers.
- **DOC-4 left `web/DESIGN.md`'s layer-panel restatement of the rendering rule
  behind while staging its verbatim twin.** `web/DESIGN.md:426-449` closes with
  the same sentence as the passage inside `web/design/README.md:154` that DOC-4
  does rewrite, and that sentence states the panel-renders-on-every-row rule
  over §7.3.1's read arms, which UI-2 reverses. The list's ranges reach the
  paragraph's neighbours in the same file and skipped it, so the staged edits
  would have left the rule and its negation over one population in one corpus.
  `web/DESIGN.md:426-449` is added to the "Prose restatements rewritten" list
  and takes the rewrite its twin takes, including UI-2's empty-state arm on the
  closing sentence.
- **S50's Covers line still claimed the panel-refusal coverage the amendment
  moves to S57.** The Goal paragraph was rewritten because no remaining step
  attempts a write from the panel, while the Covers line
  (`test/manual-validation.md:4914-4916`) kept naming "the §13.10 panel's
  treatment of a refused write", and the "Why by hand" paragraph
  (`:4918-4922`) stayed premised on the same refusal. Both are now staged: the
  Covers line names the §13.10 rule that the panel offers a layer operation only
  where the caller may take it, which is what new step 3 asserts, and the "Why
  by hand" paragraph states the offered-nothing assertion, the terminal refusal,
  and that S57 reads the refusal rendering. The S12 checklist step names the two
  added sites.
- **S47 step 3's amended instruction read the layer panel at the catalog
  route.** The staged sentence opened `http://127.0.0.1:8153/app/`, where an
  empty hash resolves to the catalog route (`web/ui/src/route.ts:38-41`), so the
  one hand-run reading of the anonymous-caller panel could not be performed as
  written. The instruction now opens `http://127.0.0.1:8153/app/#/layers`
  (`web/ui/src/route.ts:149`), which is the address every other panel-reading
  scenario in the file names, and the sign-in click and the existing Expect are
  unchanged because the callback returns the browser to `/app/`.
- **Two edge-case rows still credited the confinement with binding every ingest
  that names a filesystem path.** Narrowing the confinement's subject corrected
  D9, OQ-1, and the public-bind and tenant-admin rows, and left the admin-arm
  row and the no-identity-provider row asserting the withdrawn bound over
  populations that include the file-transport `git` arm, which
  `pkg/layer/source/git.go:48-56` fetches through `git.CloneContext` with no
  root and no path check. The admin-arm row now confines the ingest where the
  path is read as a directory and names the registry process's own rights as
  the bound on a repository string that resolves to the file transport, and the
  no-identity-provider row carries the same two clauses.

### Redesign 2 (2026-09-01, automated)

Three areas were redesigned as wholes, each because successive passes were
rewording the same fact in a different site rather than changing what the
proposal commits to.

**The confinement's covered population.** Which ingests the confinement covers
was restated in the Summary's §7.3.1 bullet, in the fixed decision on the two
controls, in D3, in D9, in OQ-1, in the Non-goals `git`-tree bullet, and in four
edge-case rows, each with its own paraphrase of the `git` file-transport
carve-out. SPEC-2's staged confinement paragraph is now the single statement of
the covered population and of that carve-out, and every other site cites "the
ingests the staged confinement paragraph covers" and states only its own
outcome. The covered population is a call-site set rather than a runtime
predicate, which CODE-1 now states: `source.ConfinedFS` is the module's only
constructor of an `fs.FS` over a host directory, it has exactly four callers,
and the review-time check is that `grep -rn "os.DirFS" --include='*.go' pkg cmd
internal` returns no call outside a test. That grep's expected output was
re-run against the working tree, which is what surfaced the comment at
`pkg/registry/ingest/ingest.go:198-200` attributing the snapshot to `os.DirFS`
by name; CODE-1 stages its rewrite, so `pkg/registry/ingest` takes two edits and
S5 names both.

**The register prediction.** The `Register layer` control, the panel's
empty-state copy, and the sidebar's `catalogBare` line all predict the same
registration, and the copy arms were carrying a hand-written three-term
reduction of what the control's own call decides. UI-1 gains one target
constructor, `newLayerTarget(subject)`, and all three readers evaluate
`mayTake('register', newLayerTarget(subject), caps, subject)`. The empty-state
arm is `postureAnswered && !mayRegister`, whose only hand-written term is about
whether the read settled anything. The redesign also closed a gap the proposal
did not mention: `web/ui/src/App.tsx:463-469` renders a second register
instruction on every route, derived from the shell's own catalog read, which
prints the same false instruction the panel's empty state was corrected for in
pass 8. That line belongs to the domain browser, so SPEC-7's §11 derivation
attributes its condition points to that unit's Render cell, DOC-4 states the
rule in the brief's domain browser section, and TEST-8 asserts the control and
both copies together in each of the three register cases.

**DOC-4's restatement sites.** The deliverable was a hand-typed list of line
numbers into a corpus the same commit edits, with one rewrite paragraph per
site and one hand-typed exclusion. Sites are now enumerated by three stated
search predicates, a claim sweep, the board sweep the proposal already had, and
a prescription sweep, and every returned line takes one of five rewrites stated
once each or falls in one of seven exclusion classes stated once each. The line
numbers survive as a snapshot table whose columns are the claims and the
disposition, and a disagreement between the table and a re-run is resolved in
the sweep's favour. S14's completion condition is that the three sweeps are
re-run over the edited tree and every returned line is a line the commit edited
or a member of an exclusion class, which is what observes a miss, because
nothing mechanical reaches this corpus.

Deleted outright:

- The restatements of the confinement's covered population outside SPEC-2's
  paragraph, listed above. What survives outside that paragraph is the caller
  clause in the Summary's §7.3.1 bullet and the deployment scope in the
  Summary's CODE-1 bullet, in D5, and in CODE-1's routing paragraph, each of
  which is about which call sites the constructor is routed through.
- UI-1's English restatement of the register reduction, replaced by
  `newLayerTarget`, and UI-2's English description of the same target in the
  control table's `Register layer` row. The layer-class row and the
  `Local folder` row keep the targets they state, because those predict a
  different registration.
- UI-2's hand-written three-term empty-state condition and the two paragraphs of
  deployment caveats that existed to defend it. The register call decides both
  arms.
- DOC-4's five per-site rewrite paragraphs and its hand-typed line-number list,
  and its hand-typed exclusion of `web/design/README.md:200`, which contradicted
  the staged rewrite of that line's code twin at `web/ui/src/App.tsx:1268-1274`.
  That site is now rewritten; the account menu's own rendering is unchanged.

Nothing in the staged code shrinks. All of the above is proposal prose and one
client expression.

Open decisions the redesign recorded, each applied at its stated default:

- **Board 14i's deployment.** Its label says the board is drawn for a caller who
  resolves no subject "on a deployment that runs no browser sign-in", while the
  board paragraph reads it as the deployment that authenticates none, and a
  registry can configure an identity provider and run no browser flow. Applied
  the default: amend the label and the caption to pin the board to a deployment
  that authenticates none and keep every control, which leaves the
  identity-provider anonymous panel on the undesigned-states list. The
  alternative redraws 14i for that caller and removes the only drawn evidence of
  the authenticates-none panel.
- **How far the state-once convention binds.** Applied the default: it binds this
  proposal's own prose, and staged content for shipped files states the rule in
  the spec's own words, because a citation of a proposal paragraph is meaningless
  in a shipped file. A later narrowing then edits SPEC-2's paragraph and DOC-1's
  mirror sentence. The alternative binds staged content as well and moves one
  site on a narrowing, at the cost of a reference-page sentence citing a
  proposal.
- **What the sidebar's `catalogBare` line says to a caller who resolves none.**
  Applied the default: drop "Register a layer to fill it.", leaving "The catalog
  holds no domains." The sidebar reports the catalog's state and the panel states
  the reason once, in place. The alternative repeats the panel's no-caller
  sentence on every route, including routes with no panel.

Two conflicts between the parallel specifications were settled against the
repository rather than by preference: `newDirFS` is at
`pkg/registry/server/filesystem_resources.go:15` with its `os.Open` at `:18-19`,
and `layerFS := newDirFS(l.Path)` is at `pkg/registry/server/server.go:338`.

### Pass 10 (2026-09-01, automated)

- **CODE-4's webhook compatibility claim was falsified by its own predicate.**
  `namesHostPath` returned true on any non-empty `local_path`, so a stored `git`
  layer that also carries one was refused on `restore`, on `reingest`, and on
  every webhook redelivery, while the paragraph claimed such a layer keeps
  working. That population is producible through the shipped API, because
  `register` copies `req.LocalPath` into the config with no source-type
  condition (`pkg/registry/server/layers.go:835-839`) and `update` assigns
  `cfg.LocalPath` on any layer (`:697-698`), and the refusal bought no
  confinement, because `Git.Snapshot` reads `Repo`, `Ref`, and `Root` and never
  the configured path (`pkg/layer/source/git.go:39-97`) even though the
  orchestrator copies `LocalPath` into the source config for every source type
  (`pkg/registry/ingest/orchestrator.go:112`). The owner could not clear the
  field either, because `update` treats an empty `local_path` as "leave
  unchanged". The predicate now classifies a `git` source on its repository
  string alone, `sourceType == "local" || (sourceType != "git" && localPath !=
  "") || isFileTransportRepo(repo)`, which keeps the custom-§9.1-source-type
  arm the doc comment exists for and leaves every network-repository `git` layer
  admitted. The same carve-out is stated in SPEC-2's staged §7.3.1 paragraph, in
  the Summary's fixed decision on what the rule reads, in the client's
  `namesHostPath` in UI-1, in a new edge-case row, in TEST-4's webhook cell and
  a new unit cell over `reingest` and `restore`, in a new TEST-8 row, and in
  DOC-1's `http-api` and `error-codes` bullets. The client mirrors the carve-out
  rather than dropping it, because a client predicate without the term drifts
  toward absence and hides `Reingest` and `Restore` from a non-admin owner the
  server admits.
- **The `seedLayer` amendment's rationale was restated on the corrected
  predicate.** The paragraph justified conditioning the `/tmp/seed` default on
  the source type by saying a `git` cell would otherwise take the new refusal,
  which the carve-out makes false. The narrowing stays, because without it every
  `git` cell carries a path by default and the new stored-`git`-with-a-path cell
  would assert nothing the default does not already produce.
- **The staged §7.3.1 sentence exempted a path supplied beside a `git` source,
  which the `update` arm refuses.** `update` passes an empty `source_type` and
  an empty `repo` and classifies the patch's `local_path` alone, so the
  `(sourceType != "git" && localPath != "")` term fires on a patch that names a
  path whatever the stored layer's source type. The staged sentence now exempts
  a stored path on a restore, a reingest, and a webhook-triggered reingest, and
  states that a patch is classified on the fields it carries, which is what
  CODE-4's call-site table, the edge-case row, and DOC-1's `http-api.md` bullet
  already said.
- **Two rationales still asserted that a `git` cell carrying `/tmp/seed` takes
  the refusal.** The Summary's watch-out on the moved cells and TEST-4's
  IMPLEMENTOR'S CHOICE were left on the pre-carve-out reason, and the second
  read as forbidding the stored-`git`-with-a-path cell TEST-4 adds. Both now
  give the reason the `seedLayer` paragraph gives: the default is narrowed so a
  `git` cell reaches the endpoint with an empty `LocalPath` unless the cell
  names one, and the new cell names its path explicitly.
- **The `Git.Snapshot` citation was widened to the function body.**
  `pkg/layer/source/git.go:38-56` ended inside the clone-options block and
  carried the `Repo` and `Ref` guards alone, while the sentence claims the
  function reads `Root` (`:72-77`) and never the configured path. All four
  occurrences now cite `pkg/layer/source/git.go:39-97`.
