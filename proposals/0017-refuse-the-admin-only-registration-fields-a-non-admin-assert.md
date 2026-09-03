# Proposal 0017: Refuse the admin-only registration fields a non-admin asserts, and report the caller's own email in the posture read

- Issue: (to be filed)
- Status: Implemented (2026-09-02). Signed off by the maintainer for
  implementation, whole, with every step in the checklist in scope. Converged
  after 9 adversarial review rounds (21 findings fixed); "Resolved in
  adversarial review" records what each pass changed.
- Date: 2026-09-02

This document stages the proposed spec, code, test, and documentation changes.
It does not modify any spec, code, or doc file. Apply the changes in the staged
sections after sign-off.

## Summary

**What changes.**

- §7.3.1 gains an admin-only registration fields paragraph beside its existing
  layer write authorization, layer read visibility, and local-source
  authorization paragraphs. A `register` request from a caller the §4.7.2 admin
  arm does not admit that asserts `owner`, `public`, `organization`, `groups`,
  or `users` is refused with `403 auth.forbidden` carrying
  `details.constraint: "admin_only_fields"` rather than having the assertion
  discarded and answered `201`. The §7.3.1 `**Errors.**` paragraph gains the
  arm.
- `pkg/registry/server`'s `register` handler hoists its admin-arm evaluation,
  computes the asserted admin-only fields from their values, and refuses after
  the local-source rule has run, so the `local_source` refusal keeps its
  precedence and the layer write refusal keeps its own.
- §7.3.4's posture read gains `email`, the requesting caller's own email where
  the configured identity provider recorded one, and the closing sentence that
  bounds the body is amended a second time so it stays closed against every
  other disclosure and against any other caller's data.
  `pkg/registry/server`'s posture handler serializes it from the identity it
  already holds.
- The web shell's account cluster renders that email where the read carries one
  and the subject where it does not, so a deployment whose subject is an
  opaque provider identifier no longer draws a UUID as the reader's own
  identity.
- The `--public`, `--organization`, `--group`, and `--user` usage strings on
  `podium layer register` name the administrator role, the reference and
  deployment pages follow, `test/manual-validation.md` gains the hand-run
  readings for both surfaces, and `CHANGELOG.md` records the change.

**Fixed decisions.**

- The server-side registration class resolution stays. §7.3.1 requires it and
  §14.9's invocation carries no class flag, so a bare non-admin registration
  keeps answering `201` with the implicit `users: [<registrant>]` visibility.
  The new rule refuses assertions of the ownership and visibility fields and
  refuses nothing else.
- `LayerRegisterRequest.UserDefined` stays a plain `bool`. `user_defined` is
  not an admin-only field under this rule, and no request type changes.
- The refusal keys on the caller's §4.7.2 admin arm rather than on the resolved
  layer class. It therefore reaches a re-registration of a stored layer the
  caller owns on the same terms, and a registry started with no identity
  provider configured, or one started in public mode, admits every caller on
  that arm and refuses nothing.
- The refusal keys on values rather than on key presence. `public: false`,
  `organization: false`, an empty `groups` or `users`, an empty `owner`, and an
  `owner` naming the caller's own verified subject assert nothing.
- The refusal reuses `auth.forbidden` with a second `details.constraint` value,
  `admin_only_fields`. No §6.10 code and no matrix axis entry is added, and the
  `details` object carries `constraint` alone. The asserted field names are
  named in the message.
- Ordering is fixed: `authorizeLayerWrite`, then `authorizeLocalSource`, then
  the admin-only fields rule. Both earlier refusals keep the envelopes already
  pinned on disk.
- The posture read reports `email` and no display name. `layer.Identity` gains
  no field, `pkg/identity` reads no new claim, and §6.3.3 is not amended.
- The CLI's register body construction is untouched. Only the four visibility
  flags' usage strings change, and `--owner` keeps its string because it is the
  documented mechanism on a no-identity standalone deployment.
- Podium is pre-1.0. No flag, key, or dual code path preserves the discard. The
  server's `SessionPosture` gains no field, because the handler writes the body
  from the identity it already holds, and the client's `SessionPosture` type
  gains an optional `email` with no version negotiation.

**Watch out for.**

- **The 201 body already reports the discard, so the defect is the status and
  the exit code.** `register` answers `writeJSON(w, http.StatusCreated, resp)`
  over `resp := LayerRegisterResponse{Layer: cfg}`, and `store.LayerConfig`
  carries no JSON
  tags except `WebhookSecret`, so the response reports `UserDefined: true`,
  `Public: false`, and the derived `Users` in Go-cased keys. The operator-facing
  defect is that `podium layer register --public` exits `0` on a registration
  that applied none of it, rather than the outcome going unreported.
- **The web register form asserts nothing on its user-defined arm.**
  `RegisterLayerForm` sends `public`, `organization`, `groups`, and `users` as
  `undefined` there (`web/ui/src/surfaces/RegisterLayerForm.tsx:211-218`) and
  sends no `owner` at all, so no form submission is newly refused. It does send
  `user_defined` unconditionally, which is exactly why `user_defined` is not on
  the refusal's field list.
- **The account cluster is not fed from the shell's `subject`.** `TopBar`
  derives its own `const subject = posture?.subject ?? ''`
  (`web/ui/src/App.tsx:1226`, inside `TopBar` which opens at `:1212`) and passes
  it to `AccountMenu` (`:1265-1271`). App's `subject` at `:324` feeds
  `mayRegister` (`:339`) and `anonymous` (`:372`) and must stay the raw subject.
  The change belongs at `:1226` alone.
- **The cluster's render gate stays keyed on the subject.** `{subject !== '' ?
  ...}` at `:1265` is what keeps a body carrying an email with no subject from
  raising the cluster.
- **`initialsOf` splits on `@` and then on `[.\-_]`**
  (`web/ui/src/App.tsx:1512-1513`) and contains no whitespace. That rule is
  correct for an email and for a subject, which are the only two values that
  reach it under this change. It would be wrong for a display name, which is
  one more reason the name is deferred.
- **The existing server posture test masks the defect.**
  `pkg/registry/server/webui_session_test.go:163` resolves an email-shaped
  subject (`alice@acme.com`), so it reads correctly today whatever the handler
  serializes. The new case has to resolve a subject that is not an email.
- **The Keycloak users S44 creates carry no email.** `$KC create users`
  (`test/manual-validation.md:3941`) sets `username` and `enabled` alone, so on
  that stack the posture read reports no `email` and the cluster renders the
  fallback. DOCS-2 therefore stages both the `-s email=` argument on that
  `create users` invocation and the `$KC update` command that gives the realm
  user S47 signs in as an email, without which the hand-run reading validates
  the fallback and nothing else.
- **`test/e2e/cli_reference_test.go` cannot host the refusal case.** Its
  `startServer(t, "")` configures no identity provider, so the admin arm admits
  every caller and the refusal never fires. The authenticated harness is
  `startAuthServer`, and `test/e2e/local_source_authorization_test.go` is the
  sibling file that already drives a non-admin CLI refusal through it.

## Implementation checklist

- [x] **S1 · spec** — SPEC-1. §7.3.1 gains the admin-only registration fields
      paragraph, and its `**Errors.**` paragraph gains the new arm. The file is
      `spec/07-external-integration.md`.
      Levels: —. Depends on: —
- [x] **S2 · spec** — SPEC-2. §7.3.4's posture read gains the `email` bullet,
      its opening sentence names it, and its closing sentence is amended. The
      file is `spec/07-external-integration.md`.
      Levels: —. Depends on: —
- [x] **S3 · code** — CODE-1, TEST-1. The hoisted admin arm, the value-keyed
      asserted-fields helper, and the refusal after the local-source call in
      `pkg/registry/server/layers.go`; the server cases in
      `pkg/registry/server/layer_register_class_test.go`; the amended
      `asserts-user-defined` body and owner expectation in
      `pkg/registry/server/layer_write_auth_test.go`; the two ordering cells in
      `test/integration/layer_write_authorization_test.go`; and the end-to-end
      CLI case in `test/e2e/local_source_authorization_test.go`.
      Levels: unit, integration, e2e. Depends on: S1
- [x] **S4 · code** — CODE-3, TEST-2. The posture read serializes the
      requesting caller's own email under its existing subject guard, its
      declaration comment states what the body is closed against, and the
      handler cases pin the present and the absent arm.
      Levels: unit. Depends on: S2
- [x] **S5 · code** — UI-1, TEST-3. The `email` field on `SessionPosture`, the
      display derivation in `TopBar`, the `display` prop on `AccountMenu` and
      its three render sites, the client cases, and the regenerated
      `web/bundle` (`web/bundle/index.html` and the content-hashed files under
      `web/bundle/assets/`), produced by `npm ci && npm run build` in `web/ui`.
      Levels: unit. Depends on: S4
- [x] **S6 · code** — CLI-1. The `--public`, `--organization`, `--group`, and
      `--user` usage strings on `podium layer register` name the administrator
      role.
      Levels: —. Depends on: S1
- [x] **S7 · docs** — DOCS-1. The refusal and the posture field on the HTTP API,
      error-codes, CLI, access-control, and layers pages.
      Levels: —. Depends on: S3, S4, S6
- [x] **S8 · docs** — DOCS-2. The hand-run scenarios: S44's realm-user email,
      S47 step 1, step 3, and step 6, and S55's new
      terminal step with its `**Covers.**` line.
      Levels: —. Depends on: S3, S5
- [x] **S9 · docs** — DOCS-3. The `CHANGELOG.md` entry, which records both the
      refusal and the posture field.
      Levels: —. Depends on: S3, S5

## Current state and the gap

**`POST /v1/layers` discards the admin-only fields a non-admin asserts and
answers `201`.**

`register` resolves the registration class server-side. The handler reads the
caller, takes the admin arm, and on failure resolves the registration to the
user-defined class where a subject resolves
(`pkg/registry/server/layers.go:828-839`). On that arm it overwrites
`cfg.Owner` with the caller's subject, forces `cfg.Users = []string{cfg.Owner}`,
and never reads `req.Public`, `req.Organization`, `req.Groups`, or `req.Users`,
which are assigned only on the admin-defined arm (`:864-893`, with the comment
"Discard any caller-supplied public/organization/groups." at `:871`). It builds
`resp := LayerRegisterResponse{Layer: cfg}` (`:980`) and answers
`writeJSON(w, http.StatusCreated, resp)` (`:985`).

Nothing between the discard and the `201` reports it as a refusal.
`LayerRegisterResponse` carries `Layer`, `WebhookURL`, and `WebhookSecret`
(`pkg/registry/server/layers.go:503-508`), there is no warning field, no
advisory header, and no distinct status. The stored configuration is echoed
whole, so the body does report `UserDefined: true`, `Public: false`, and the
derived `Users`. The CLI errors only on `status >= 400`
(`cmd/podium/layer.go:243-248`), so `podium layer register --id X --repo
<network-url> --ref main --public` as an authenticated non-admin exits `0` and
prints a success record for a layer that is not public. After proposal 0016 the
same caller gets a clean `403 auth.forbidden` with `details.constraint:
"local_source"` for `--local` and this silent divergence for `--public`.

`--public`, `--organization`, `--group`, and `--user` are written in top-level
blocks independent of `--user-defined` (`cmd/podium/layer.go:230-241`), which is
what makes the visibility half reachable from the shipped CLI. `body["owner"] =
*owner` is written unconditionally inside the `--user-defined` block (`:225`)
with `owner` defaulting to the empty string (`:188`), so a bare `podium layer
register --user-defined` transmits `"owner": ""` and a rule keyed on key
presence would refuse a working invocation.

A non-admin who asserts nothing must still register their own layer. §7.3.1
states that authenticated users register their own layers via `podium layer
register` with implicit visibility `users: [<registrant>]`
(`spec/07-external-integration.md:95`), the write-authorization paragraph states
that where such a registration resolves to a user-defined layer and a subject
resolves the stored owner is that subject (`:97`), and §14.9's invocation
carries no class flag (`spec/14-common-scenarios.md:130`). The class resolution
stays; what the new rule refuses is the assertion the resolution will not honor.

**The posture read reports no email, so the account cluster renders a raw OIDC
subject.**

`GET /v1/ui/session` writes `identity_provider_configured`, `public_mode`,
`browser_auth`, `layer_capabilities`, and `subject`
(`pkg/registry/server/webui_session.go:56-77`). The shell reads `const subject =
posture?.subject ?? ''` (`web/ui/src/App.tsx:1226`) and renders
`{initialsOf(subject)}` in the avatar with `{subject}` beside it (`:1350`,
`:1352`) and `{subject}` again as the account menu's only identity line
(`:1363`). Signed in against a provider whose `sub` is a UUID, that cluster reads
a UUID, and `initialsOf` derives the avatar label from the UUID's parts
(`:1508-1521`).

The design specifies otherwise. The top bar's identity is "a 24px circular
`chip` avatar with mono initials and the email at 12.5px"
(`web/design/README.md:91`). Because `sub` is provider-chosen, the same build
renders a readable identifier on one deployment and an opaque one on another.

The registry already carries the value. §6.3.3 enumerates the token claims and
states that the registry records the caller's subject and `email` under
`oidc-jwt` and records `sub` and `email` from the headers under
`trusted-headers` (`spec/06-mcp-server.md:102`, `:112`). `claimIdentity` parses
it (`pkg/identity/runtime.go:249-251`), `IdentityFromTrustedHeaders` reads
`X-Podium-User-Email` into it (`pkg/identity/trusted_headers.go:31`, `:67`),
`layer.Identity` carries it (`pkg/layer/composer.go:23`), and all three
request-time verifier constructions copy it
(`internal/serverboot/identity_verify.go:38`, `:259`, `:286`). The posture
handler already holds the whole `layer.Identity` through its `Identity` seam
(`pkg/registry/server/webui_session.go:73-77`) and copies only `Sub` out of it.

The blocker is §7.3.4's closing sentence, which carries "The response carries no
other field, and in particular no issuer, client identifier, endpoint,
filesystem path, or other configuration value, and no subject or authorization
belonging to any caller other than the one that asked"
(`spec/07-external-integration.md:193`). Proposal 0016 amended that sentence once
to admit `layer_capabilities`. Reporting the requesting caller's own email is on
the same footing and breaches only the blanket clause.

## Decisions

**D1. Both defects are staged here.** They touch different routes and could be
landed separately. The reviewer may sequence S3 and S4 to S5 as separate
commits; the proposal is signed off whole.

**D2. The refusal keys on the caller's admin arm rather than on the layer
class.** The handler computes `adminErr := e.authAdmin(r)` once and refuses when
that error is non-nil and the request asserts at least one admin-only field. A
caller the admin arm admits is unaffected. The existing class-resolution cases
in `pkg/registry/server/layer_register_class_test.go` stay green for two
different reasons. The cases on `newClassHarness` (`:36-61`, `:66-77`) carry no
admin-only field in their bodies, so the guard computes an empty slice. The
cases on `newLayerHarness` (`:83-99`, `:105-124`, `:128-152`) stay green because
that harness's bare `NewLayerEndpoint` installs the admitting `authAdmin`
(`pkg/registry/server/layers.go:190`), so `adminErr` is nil and CODE-1's guard
is never entered, even though two of those bodies do assert an admin-only field
under D3 and D4: `"owner": "alice@acme.com"` from a caller the default resolver
gives no subject (`:112`) and `"organization": true` (`:137`). Any later change
to the field list or to those harnesses has to re-check those two cases. The
rule also keeps a no-identity
standalone deployment and a
public-mode deployment admitting every registration, on the same reading §7.3.1
already states for the local-source rule. `WithAdminAuth` returns nil in both
cases (`internal/serverboot/serverboot.go:1254-1263`), so the §2.2 single
behavioral surface is not breached.

**D3. The refusal keys on values rather than on key presence.** An asserted
field is `public: true`, `organization: true`, a non-empty `groups`, a non-empty
`users`, or an `owner` that is non-empty after trimming and differs from the
caller's verified subject. Value-keying is forced by the decode:
`LayerRegisterRequest` carries plain `string`, `bool`, and `[]string` fields
(`pkg/registry/server/layers.go:466-489`), so `encoding/json` cannot distinguish
an absent key from a zero value without converting five fields to pointers.
It also keeps `podium layer register --user-defined` working, which transmits
`"owner": ""` unconditionally, and it leaves an older CLI working against a
newer registry.

**D4. An `owner` naming the caller's own subject asserts nothing.** §7.3.1
already mandates that the stored owner of such a registration is the caller's
subject, so a caller echoing its own subject asks for what the resolution
performs. `podium layer register --user-defined --owner <own sub>` is a working
invocation today and stays one.

**D5. `user_defined` is not on the field list, and
`LayerRegisterRequest.UserDefined` stays a plain `bool`.** Refusing an asserted
`user_defined: false` would refuse exactly the request
`spec/07-external-integration.md:97` mandates resolving, and it is unreachable
as a distinct harm: a non-admin asserting it either resolves no subject and is
already refused (`pkg/registry/server/layers.go:834-836`) or resolves one and
receives the class §7.3.1 requires. It is also unsafe against always-serializing
clients. The repo's own web client sends the key literally on every submission
(`web/ui/src/surfaces/RegisterLayerForm.tsx:211`) with a class state computed
once at mount from a prop (`:74`), so an omitted-versus-false distinction would
turn a failed or slow posture read into an authorization outcome for a caller
whose registration succeeds today. It would additionally invalidate the
documented rationale for the form's withheld class control (`:87-95`), which
states that the registry resolves such a registration down to a user-defined
layer rather than refusing it. The residual is recorded as a non-goal, and
OQ-1 puts the alternative to the reviewer.

**D6. The refusal reuses `auth.forbidden` with a second `details.constraint`
value.** `admin_only_fields` sits beside the `local_source` value proposal 0016
established (`pkg/registry/server/layer_capabilities.go:99-101`, pinned at
`test/integration/layer_write_authorization_test.go:245-250`). §6.10 already
states that an authorization outcome on a distinct axis reuses
`auth.forbidden`, and `details` is a free-form object there, so no matrix axis
entry and no `// Matrix:` obligation is created.

**D7. `details` carries `constraint` alone.** The established form carries that
key and nothing else, the CLI prints the raw body (`cmd/podium/layer.go:245`),
and no client in this change reads a field list. The refusal message names the
asserted fields, which is the local-source message's own form.

**D8. The two earlier refusals keep their precedence.** The new check runs after
`e.authorizeLocalSource(...)` (`pkg/registry/server/layers.go:847-849`), which
itself runs after `authorizeLayerWrite` (`:808-812`). A non-admin registering
`--local --owner bob` still gets `details.constraint: "local_source"`, and a
non-admin re-registering another caller's stored layer with an asserted `owner`
still gets the bare layer-write refusal with no constraint. Neither ordering is
pinned on disk today: the two cells at
`test/integration/layer_write_authorization_test.go:208-222` and `:234-250`
each post an `owner` naming the posting caller's own subject
(`:145`), which asserts nothing under D4, so both stay green wherever the
refusal is placed. TEST-1 stages the two integration cells that do assert a
field and therefore pin the two orderings.

**D9. The rule applies on the re-registration arm.** A non-admin re-registering
a layer they own with `--public` fails the same admin arm and is refused on the
same terms. `authorizeLayerWrite` still runs first, so a caller neither arm
authorizes keeps its existing refusal.

**D10. The posture read gains `email` and no display name.** `email` is already
a first-class §6.3.3 identity attribute under both providers, already carried on
the exact seam the handler holds, and the change is a serialization plus the
§7.3.4 amendment. Nothing in `spec/` mandates a display name, no `name` claim is
read anywhere in `pkg/identity` or `internal/serverboot`, and neither
`identity.Identity` nor `layer.Identity` carries such a field, so adding one
would be code leading spec. A name would also reproduce the defect it is meant
to fix: `trusted-headers` names no display-name header
(`pkg/identity/trusted_headers.go:26-41`), and a provider issuing
`preferred_username` and no `name` resolves none either. The design's account
menu name line (`web/design/README.md:200`) is left unrendered and recorded as a
non-goal.

**D11. The client renders a fallback rather than a required field.** The
cluster renders the email where the read carries one and the subject where it
does not, so a registry serving an older bundle, a deployment whose provider
records no email, and a `trusted-headers` gateway that injects none all keep a
readable cluster. The avatar initials come off whichever value the fallback
selected. Nothing in the cluster reports a role or a capability, which
`web/design/README.md:200` forbids.

**D12. The CLI's register body construction is untouched.** The server rule is
value-keyed and needs nothing from the CLI. Hoisting `--owner` out of the
`--user-defined` block would newly route it to the admin-defined arm, where the
handler assigns `cfg.Owner = req.Owner` verbatim
(`pkg/registry/server/layers.go:888`), which is a register behavior no spec
change here mandates and which contradicts `docs/reference/cli.md` and the
rationale recorded at `test/e2e/standalone_layer_test.go:73-75`. Only the four
visibility flags' usage strings change.

**D13. `CODE-2` is absent from the change list.** It staged the `Name` field,
the claim read, and the three copy sites, and adversarial review dropped it
whole. The identifier is left unused rather than reassigned so the review record
stays readable.

## Spec amendment: §7.3.1 admin-only registration fields

**SPEC-1.** Anchor: `spec/07-external-integration.md`, §7.3.1. One paragraph
lands immediately after the paragraph beginning `**Local-source ingest
confinement.**` (`spec/07-external-integration.md:103`) and immediately before
the paragraph beginning `**Errors.**` (`:105`). The user-defined-layer
paragraph, the layer write authorization paragraph, the layer read visibility
paragraph, the two local-source paragraphs, and the command list are untouched.

The inserted paragraph:

> **Admin-only registration fields.** A `register` request carries fields that
> only a tenant admin's registration is read for: `owner`, `public`,
> `organization`, `groups`, and `users`. A caller the §4.7.2 admin arm does not
> admit registers a user-defined layer whose owner is that caller's verified
> subject and whose visibility is the implicit `users: [<registrant>]`, so a
> request from such a caller that asserts any of those fields is refused with
> `403 auth.forbidden` (§6.10) carrying
> `details.constraint: "admin_only_fields"`, rather than having the assertion
> discarded and answered `201`. The refusal names the asserted fields in its
> message. A field is asserted by its value rather than by its presence:
> `public` or `organization` set to true, a non-empty `groups` or `users`, and
> an `owner` that names a subject other than the caller's own. A field carrying
> a false boolean, an empty array, or an empty string asserts nothing, and an
> `owner` naming the caller's own subject asserts nothing, because it names what
> the rule above already stores. A registration that asserts none of them is
> unaffected, which is the invocation §7.3.1 and §14.9 document, and the class
> resolution above continues to resolve it, including a request that names the
> registration class explicitly. The rule is evaluated on the same arm as the
> layer write authorization rule above, so it reaches a re-registration of a
> stored layer the caller owns on the same terms, and a registry started with no
> identity provider configured, or one started in public mode (§13.10),
> authenticates no caller and admits every caller on the admin arm, so the rule
> refuses nothing there. Where a registration is on both this rule's arm and the
> local-source authorization rule's arm, the local-source refusal is the one
> returned.

SPEC-1 stages one further edit, on §7.3.1's `**Errors.**` paragraph
(`spec/07-external-integration.md:105`). The paragraph ends today with "and a
registration, a filesystem-path patch, a restore, or a reingest of a layer that
names a filesystem path on the registry host attempted by a caller the
local-source authorization rule above does not authorize (`auth.forbidden`,
carrying `details.constraint: "local_source"`)". The replacement ends:

> …and a registration, a filesystem-path patch, a restore, or a reingest of a
> layer that names a filesystem path on the registry host attempted by a caller
> the local-source authorization rule above does not authorize
> (`auth.forbidden`, carrying `details.constraint: "local_source"`), and a
> registration asserting an admin-only registration field attempted by a caller
> the admin-only registration fields rule above does not admit
> (`auth.forbidden`, carrying `details.constraint: "admin_only_fields"`).

No §6.10 code is added. The refusal is an authorization outcome on a distinct
axis, which §6.10 already places on `auth.forbidden`
(`spec/06-mcp-server.md:476`).

## Spec amendment: §7.3.4 posture read reports the caller's own email

**SPEC-2.** Anchor: `spec/07-external-integration.md`, §7.3.4. The edits are the
opening sentence pair at `:185`, one bullet inserted into the list at
`:187-191`, and the closing paragraph at `:193`.

The opening pair reads today "…a request that carries one has it verified so the
response can report `subject` and evaluate `layer_capabilities`, and for no
other purpose…". The clause is extended:

> …a request that carries one has it verified so the response can report
> `subject` and `email` and evaluate `layer_capabilities`, and for no other
> purpose…

One bullet lands immediately after the `subject` bullet (`:190`) and immediately
before the `layer_capabilities` bullet (`:191`):

> - `email`: the requesting caller's own email as the configured identity
>   provider recorded it (§6.3.3), present only where one resolves and is
>   non-empty, and absent otherwise. It belongs to the caller that asked and to
>   no other caller.

The closing paragraph reads today "The response carries no other field, and in
particular no issuer, client identifier, endpoint, filesystem path, or other
configuration value, and no subject or authorization belonging to any caller
other than the one that asked. The read discloses the deployment's identity
configuration and the requesting caller's own subject, and it discloses no
artifact, layer, tenant, or other caller's data." Both sentences are replaced:

> The response carries no other field, and in particular no issuer, client
> identifier, endpoint, filesystem path, or other configuration value, and no
> subject, email, or authorization belonging to any caller other than the one
> that asked. The read discloses the deployment's identity configuration and the
> requesting caller's own subject and email, and it discloses no artifact,
> layer, tenant, or other caller's data.

The remainder of that paragraph, which states where the read is registered, is
unchanged.

## Proposed solution

### CODE-1: `register` refuses the admin-only fields a non-admin asserts

Anchors: `pkg/registry/server/layers.go`, the exists/admin gate at `:803-818`,
the class resolution at `:828-839`, and the local-source call at `:847-849`.
`pkg/registry/server/layer_capabilities.go:99-101` is the form the refusal
matches.

**1. Hoist the admin arm.** `e.authAdmin(r)` is evaluated twice on this path
today, once inside the `else if` at `:813` and once inside the class resolution
at `:831`. Compute it once immediately before the `if exists` block and read the
result in all three places:

```go
// spec: §7.3.1 — the admin arm decides three things on this path: whether a
// registration under an unused ID from a caller with no verified subject is
// refused, whether the registration resolves to the admin-defined class, and
// whether an asserted admin-only field is refused below. Evaluate it once so
// the three read one answer.
adminErr := e.authAdmin(r)
```

The `else if` becomes `} else if adminErr != nil {`, and the class resolution's
inner `if err := e.authAdmin(r); err != nil {` becomes `if adminErr != nil {`
with the existing arms unchanged. `req.UserDefined` is still read as a plain
`bool`.

**2. The value-keyed helper.** It lands in `pkg/registry/server/layers.go`
beside the request type:

```go
// adminOnlyRegistrationFields reports the §7.3.1 admin-only registration
// fields the request asserts, in sorted order. A field is asserted by its
// value rather than by its presence: LayerRegisterRequest decodes into plain
// string, bool, and []string fields, so an absent key and a zero value are one
// thing, and the shipped CLI writes body["owner"] unconditionally inside its
// --user-defined block. An owner naming the caller's own subject asserts
// nothing, because it names what the class resolution already stores.
//
// Spec: §7.3.1
func adminOnlyRegistrationFields(req LayerRegisterRequest, sub string) []string {
	var asserted []string
	if len(req.Groups) > 0 {
		asserted = append(asserted, "groups")
	}
	if req.Organization {
		asserted = append(asserted, "organization")
	}
	if owner := strings.TrimSpace(req.Owner); owner != "" && owner != sub {
		asserted = append(asserted, "owner")
	}
	if req.Public {
		asserted = append(asserted, "public")
	}
	if len(req.Users) > 0 {
		asserted = append(asserted, "users")
	}
	return asserted
}
```

The arms are written in alphabetical order, so the returned slice is sorted by
construction and the refusal message is stable for a test to assert.

**3. The refusal.** It lands immediately after the `authorizeLocalSource` call
at `:847-849` and before `cfg` is built, so both earlier refusals keep their
precedence:

```go
// spec: §7.3.1 — the admin-only registration fields rule. A caller the admin
// arm admits has these fields read on the admin-defined arm below; every other
// caller has them resolved away, so an assertion is refused here rather than
// discarded and answered 201.
if adminErr != nil {
	if asserted := adminOnlyRegistrationFields(req, caller.Sub); len(asserted) > 0 {
		writeErrorDetails(w, http.StatusForbidden, "auth.forbidden",
			fmt.Sprintf("the registration fields %s are read on a tenant admin's registration alone; this registration resolves to a layer owned by the caller with visibility users:[<registrant>], so re-send it without them or ask an administrator to run it",
				strings.Join(asserted, ", ")),
			map[string]any{"constraint": "admin_only_fields"})
		return
	}
}
```

`caller` is already in scope from the class resolution at `:828`. Nothing else
in the handler changes: the class resolution, the two arms, the quota check, and
the `201` response are untouched.

### CODE-3: the posture read serializes the caller's own email

Anchor: `pkg/registry/server/webui_session.go`, the declaration comment at
`:9-22` and the body construction at `:73-77`.

The serialization lands inside the existing subject guard, so the two fields
resolve from one identity read and neither can report another caller's value:

```go
// §7.3.4: the response reports the requesting caller's own subject and its
// own email, each present only where it resolves non-empty, and nothing
// belonging to any other caller.
if p.Identity != nil {
	if id := p.Identity(r); id.IsAuthenticated && id.Sub != "" {
		body["subject"] = id.Sub
		if id.Email != "" {
			body["email"] = id.Email
		}
	}
}
```

The declaration comment at `:9-22` states today that the read reports the
deployment's identity posture, the caller's own resolved subject, and the
caller's layer capabilities. It gains the email in both of its sentences, in
SPEC-2's wording: the read reports the caller's own subject and email, and a
carried credential is verified so the response can report `subject` and `email`
and evaluate `layer_capabilities`, and for no other purpose.

`SessionPosture` gains no field. `Identity` already yields the whole
`layer.Identity`.

### UI-1: the account cluster renders the email the design specifies

Anchors: `web/ui/src/session.ts` (`SessionPosture` at `:22-40`),
`web/ui/src/App.tsx` (`TopBar`'s derivation at `:1226`, the cluster's render
gate and the `AccountMenu` call at `:1265-1271`, `AccountMenu`'s trigger at
`:1339-1353`, its menu identity line at `:1363`, and `initialsOf` at
`:1508-1521`).

**1. The type.** `SessionPosture` gains one optional field beside `subject`:

```ts
  /** Present only where the identity provider recorded one for this caller.
   * The account cluster renders it in place of the subject, which is a
   * provider-chosen identifier and is a UUID on some deployments. */
  email?: string;
```

No accessor is added to `session.ts`. With the display name deferred, the
fallback is a single expression at a single site, and `capabilitiesOf` exists
because it turns an absent field into a closed authorization default, which this
field has no analogue of.

**2. The derivation.** `TopBar` keeps `const subject = posture?.subject ?? ''`
at `:1226`, because the cluster's render gate at `:1265` must stay keyed on the
subject so a body carrying an email with no subject cannot raise the cluster. It
gains one line beside it:

```ts
  // The design draws the reader's own email here. The read reports the key
  // only where the provider recorded a non-empty value, and the subject is the
  // fallback, so a deployment whose provider records no email and an older
  // registry that reports no such key both keep a readable cluster.
  const display = posture?.email || subject;
```

Nothing at `web/ui/src/App.tsx:324` changes. App's `subject` there feeds
`mayRegister` (`:339`) and `anonymous` (`:372`) and must stay the raw subject,
and `TopBar` already receives the posture (`:410-411`), so no prop is added to
it.

**3. The render sites.** `AccountMenu` takes `display` beside `subject` and
renders it at every site the cluster draws it: `initialsOf(display)` in the
avatar (`:1350`), `{display}` on the trigger line (`:1352`), and `{display}` as
the menu's identity line (`:1363`). The gate at `:1265-1271` passes both and
stays keyed on `subject !== ''`.

**4. `initialsOf` is unchanged.** Its `@`-then-`[.\-_]` split
(`web/ui/src/App.tsx:1512-1513`) is correct for an email and for a subject,
which are the only two values that reach it. Its doc comment gains one sentence
saying the label now comes off whichever of the two the cluster selected.

**5. The bundle.** `web/bundle` is regenerated in the same commit by running
`npm ci && npm run build` in `web/ui`, which vite emits to `../bundle`
(`web/ui/vite.config.ts:17`) and which `web/web.go`'s `//go:embed all:bundle`
(`web/web.go:19-20`) ships inside the binary. The regenerated artifacts are
`web/bundle/index.html` and the content-hashed files under
`web/bundle/assets/`. `web/bundle` is never hand-edited. There is no
`web/ui/dist` and no build writes one: `.gitignore:25` carries a bare `dist/`
entry, which is why the output goes to `web/bundle` and why that path carries
its own negation (`web/ui/vite.config.ts:9-12`, `.gitignore:26-29`).
`.github/workflows/test.yml:207-220` fails the build when rebuilding the bundle
leaves the working tree changed, so a commit that changes `web/ui/src` and
leaves `web/bundle` at its previous content fails CI, and a registry built from
it serves a page that never reads the new `email` key while TEST-3 passes from
source.

**IMPLEMENTOR'S CHOICE:** whether `AccountMenu` takes `display` as a second prop
or takes the posture and derives it. Any answer keeps the cluster's render gate
keyed on the subject, keeps the three render sites reading one expression rather
than three, and adds no second place where the fallback is stated.

### CLI-1: the visibility flags name the administrator role

Anchor: `cmd/podium/layer.go:189-194`, the four visibility flag declarations on
`podium layer register`.

Each usage string gains the same clause `--local` already carries
(`cmd/podium/layer.go:186`, landed in commit 31a9402):

- `--public`: `"visibility: public (requires the administrator role)"`
- `--organization`: `"visibility: organization-wide (requires the administrator role)"`
- `--group`: `"OIDC group with visibility (repeatable; requires the administrator role)"`
- `--user`: `"OIDC subject or email with visibility (repeatable; requires the administrator role)"`

`--user-defined` and `--owner` keep their strings. `--owner` is the documented
mechanism on a no-identity standalone deployment, where the admin arm admits
every caller and the refusal never fires, so an administrator-role clause there
would be false. No body construction changes.

## Edge cases and accepted failure modes

| Case | Observable outcome | Where it is stated |
|:--|:--|:--|
| An authenticated non-admin registers with no admin-only field | `201`, a user-defined layer owned by the caller with visibility `users: [<registrant>]`, unchanged from today | §7.3.1's staged paragraph, "A registration that asserts none of them is unaffected"; `spec/07-external-integration.md:95`, `:97` |
| An authenticated non-admin registers with `--public`, `--organization`, `--group`, or `--user` | `403 auth.forbidden` with `details.constraint: "admin_only_fields"`, the message naming the asserted fields, and nothing stored | §7.3.1's staged paragraph and its amended `**Errors.**` paragraph; `docs/reference/error-codes.md` `auth.forbidden` |
| An authenticated non-admin registers with `--user-defined` and no `--owner`, so the body carries `"owner": ""` | `201`. The empty string asserts nothing | §7.3.1's staged paragraph, "A field carrying a false boolean, an empty array, or an empty string asserts nothing"; TEST-1's assert-nothing table |
| An authenticated non-admin registers with `--user-defined --owner <their own subject>` | `201`, owner is that subject. The value names what the class resolution already stores | §7.3.1's staged paragraph, "an `owner` naming the caller's own subject asserts nothing"; TEST-1's own-subject case |
| An authenticated non-admin registers with an `owner` naming a different subject | `403 auth.forbidden` with `details.constraint: "admin_only_fields"` | §7.3.1's staged paragraph, "an `owner` that names a subject other than the caller's own"; TEST-1's other-subject case |
| A client sends `public: false`, `organization: false`, `groups: []`, or `users: []` | `201`. None of them is an assertion | §7.3.1's staged paragraph; TEST-1's assert-nothing table |
| A raw HTTP client sends `user_defined: false` as a non-admin with a resolved subject | `201`, and the class is resolved down to user-defined. The outcome is machine-visible: the response echoes the stored configuration, reporting `UserDefined: true`, `Public: false`, and the derived `Users` | Accepted residual, recorded in Non-goals and put to the reviewer as OQ-1; `spec/07-external-integration.md:97` mandates the resolution |
| An authenticated non-admin registers `--local` together with `--public` | `403 auth.forbidden` with `details.constraint: "local_source"`. The local-source rule answers first | §7.3.1's staged paragraph, "the local-source refusal is the one returned"; TEST-1's second staged integration cell |
| An authenticated non-admin re-registers another caller's stored layer with an asserted `public` | `403 auth.forbidden` with no `details.constraint`. The layer write authorization rule answers first | `spec/07-external-integration.md:97`; TEST-1's first staged integration cell |
| An authenticated non-admin re-registers their own stored layer with `--public` | `403 auth.forbidden` with `details.constraint: "admin_only_fields"`, and the stored layer is unchanged | §7.3.1's staged paragraph, "it reaches a re-registration of a stored layer the caller owns on the same terms" |
| A tenant admin registers with every admin-only field set | `201`, unchanged from today. The admin arm admits the caller and the admin-defined arm reads every field | §7.3.1's staged paragraph, "A caller the §4.7.2 admin arm does not admit…" |
| A registry started with no identity provider configured, or one in public mode, receives any registration | Admitted. No caller can hold the admin role, so the admin arm admits every caller and the rule refuses nothing | §7.3.1's staged paragraph; `internal/serverboot/serverboot.go:1254-1263`; `docs/deployment/access-control.md` |
| The web register form submits on its user-defined arm | Admitted. It sends `public`, `organization`, `groups`, and `users` as `undefined` and no `owner` | `web/ui/src/surfaces/RegisterLayerForm.tsx:211-218`; the existing client pin at `web/ui/src/surfaces.test.tsx:9721-9732`, which submits the user-defined arm and asserts `user_defined: true` with `public`, `organization`, `groups`, and `users` all undefined |
| The posture read answers a request that resolves no subject | No `subject` and no `email`. The client renders no account cluster | §7.3.4's `subject` bullet and the staged `email` bullet; `web/ui/src/App.tsx:1265` |
| The posture read answers a caller whose provider recorded no email | `subject` present, `email` absent, and the cluster renders the subject | §7.3.4's staged bullet, "present only where one resolves and is non-empty"; TEST-2's absent arm |
| A `trusted-headers` deployment whose gateway injects `X-Podium-User-Email` | `email` present and rendered. The header set is unchanged and gains no display-name header | `spec/06-mcp-server.md:112`; `pkg/identity/trusted_headers.go:31`, `:67` |
| A registry serving a bundle built before this change | The bundle reads no `email` key and renders the subject, unchanged from today. The field is additive | `web/ui/src/session.ts`'s optional field; D11 |
| The account menu's name line the design draws | Not rendered. No shipped identity surface carries a display name | Non-goals, "A display name in the account cluster"; `web/design/README.md:200` |

## Testing

**TEST-1: the refusal, its boundary, and its precedence.**

- `TestLayerRegister_AdminOnlyFieldsRefused`, in
  `pkg/registry/server/layer_register_class_test.go` beside the existing
  class-resolution cases at `:36-152`, carrying `// Spec: §7.3.1`. One
  table-driven test over the asserted bodies: `{"public": true}`,
  `{"organization": true}`, `{"groups": ["acme-finance"]}`, `{"users":
  ["bob@acme.com"]}`, `{"owner": "bob@acme.com"}` from a caller whose subject is
  `alice@acme.com`, and one body asserting several at once. Each case asserts
  `403`, the code `auth.forbidden`, `details.constraint == "admin_only_fields"`,
  that the message names each asserted field, and that
  `GetLayerConfig` reports no stored layer. It runs on `newClassHarness`, whose
  admin authorizer denies every caller
  (`pkg/registry/server/layer_register_class_test.go:14-16`, `:25`) and whose
  identity resolver returns the given identity, so no extra wiring is needed.
- `TestLayerRegister_AdminOnlyFieldsAssertNothing`, in the same file on the same
  `newClassHarness`, carrying
  `// Spec: §7.3.1`. The boundary table: a bare registration, `{"user_defined":
  true, "owner": ""}`, `{"public": false}`, `{"organization": false}`,
  `{"groups": []}`, `{"users": []}`, and `{"user_defined": true, "owner":
  "alice@acme.com"}` from the caller whose subject is `alice@acme.com`. Each
  asserts `201`, and the last asserts the stored owner is that subject. This is
  the case that fails if the rule is ever re-keyed on presence, which would
  break the shipped `podium layer register --user-defined` invocation.
- `TestLayerRegister_AdminArmAdmitsEveryField`, in the same file carrying
  `// Spec: §7.3.1`. It cannot run on `newClassHarness`, which denies every
  caller, so it builds its endpoint the way `newLayerHarness`
  (`pkg/registry/server/layers_test.go:23-32`) does, from the bare
  `NewLayerEndpoint` whose `authAdmin` admits every caller
  (`pkg/registry/server/layers.go:190`), with an identity resolver installed so
  the caller resolves a subject. That caller posts every admin-only field and
  the case asserts `201` with the stored layer carrying `Public: true`, the
  supplied owner, and the supplied groups, so the rule cannot creep onto the
  admin arm.
- `TestLayerRegister_AdminOnlyFieldsOnReRegistration`, in the same file on
  `newClassHarness`, carrying
  `// Spec: §7.3.1`. A non-admin re-registers a stored layer they own with
  `{"public": true}`, and the case asserts the same refusal and that the stored
  configuration is unchanged.
- `pkg/registry/server/layer_write_auth_test.go`'s
  `TestLayerRegister_TakeoverProduct` is amended, because its
  `asserts-user-defined` body carries `"owner": "bob"` (`:343`) against a caller
  set that includes alice (`:316`, `:64`), so under CODE-1 alice's admitted
  cells would flip from `201` to `403`. The body's `owner` becomes the posting
  caller's own subject rather than the literal `"bob"`, which asserts nothing
  under D4, and `assertRegisteredOwner`'s `want := "bob"` seed
  (`:471-477`) becomes that same subject. The comment above the request
  construction gains a sentence naming why, in the form the file already uses
  for the git source and the local-source rule (`:365-368`): the §7.3.1
  admin-only registration fields rule refuses an `owner` naming another
  subject, and this table asserts the layer-write rule alone. No cell changes
  status under that amendment, so `wantRegisterOutcome`'s stated invariant that
  the request body changes no outcome at any point (`:393-397`) stays true and
  is not rewritten. The `// Spec:` line on `TestLayerRegister_TakeoverProduct`
  already cites §7.3.1 and is unchanged.
- Two integration cells are added to
  `test/integration/layer_write_authorization_test.go`, after the two register
  cells at `:208-250`, carrying `// Spec: §7.3.1`, and they are what pin
  CODE-1's two ordering decisions. The first posts, as non-admin bob under
  alice's stored layer ID `alice-personal`, a git-source body carrying
  `"public": true`, and asserts `403`, `auth.forbidden`,
  `details.constraint == ""`, and that alice's stored layer keeps its owner and
  its source, which pins that `authorizeLayerWrite` precedes the new check. The
  second posts, as the same caller, a `local`-source registration under an
  unused ID carrying `"public": true`, and asserts `403`, `auth.forbidden`,
  `details.constraint == "local_source"`, and nothing stored, which pins that
  `authorizeLocalSource` precedes it. The two existing cells at `:208-222` and
  `:234-250` are unchanged and pin neither ordering, because the only
  admin-only field either carries is an `owner` naming bob's own subject
  (`:145`), which asserts nothing under D4.
- `TestLayerCLI_AdminOnlyFieldsRefusedForNonAdmin`, in
  `test/e2e/local_source_authorization_test.go` beside
  `TestLayerCLI_LocalRefusedForNonAdmin`, carrying `// Spec: §7.3.1`. It drives
  `podium layer register --id bob-public --repo <network-url> --ref main
  --public` as a verified non-admin through `startAuthServer` and asserts a
  non-zero exit with `403`, `auth.forbidden`, and `admin_only_fields` in stderr,
  which is the operator-facing defect: today that invocation exits `0`. A second
  arm runs the same invocation as the bootstrap admin and asserts exit `0`.
  `test/e2e/cli_reference_test.go` cannot host either arm, because its
  `startServer(t, "")` configures no identity provider and its admin arm admits
  every caller.

**TEST-2: the posture read reports the caller's own email.**

- `TestSessionPosture_Email`, in `pkg/registry/server/webui_session_test.go`
  beside `TestSessionPosture_Subject` at `:151-176`, carrying
  `// Spec: §7.3.4`, in three arms. An identity resolving `Sub:
  "3f2a…-uuid", Email: "alice@acme.com"` reports both keys with distinct
  values, which is the arm the existing subject case cannot observe because it
  resolves an email-shaped subject. An identity resolving a subject and an empty
  email reports `subject` and no `email` key. An anonymous identity reports
  neither. A fourth assertion enumerates the body's keys and fails on any key
  beyond the six §7.3.4 names, which is the closed-body claim SPEC-2's amended
  sentence makes.

**TEST-3: the account cluster renders the display value.**

- Two cases extend the existing `describe("the shell's identity cluster")` at
  `web/ui/src/surfaces.test.tsx:15376`. The first stubs a posture carrying a
  UUID subject and `email: "alice@acme.com"` and asserts the account trigger and
  the opened menu both carry the email and neither carries the UUID, and that
  the avatar label is `A` rather than initials taken off the UUID's parts.
  `initialsOf` is unchanged, and it returns `A` for that address: the local part
  `alice` carries none of the `[.\-_]` separators it splits on, so the label is
  one character (`web/ui/src/App.tsx:1508-1521`). A UUID subject carries `-`, so
  the two labels differ and the assertion discriminates. The
  second stubs a posture carrying a UUID subject and no `email` and asserts the
  trigger carries the subject, which is the fallback arm.
- The existing case `it("pins a theme onto the root element and returns it to
  the system setting")` at `:15426-15455`, which feeds `posture({ subject:
  "alice@acme.com" })` (`:15428`) and asserts the trigger's text contains
  `alice@acme.com` (`:15433`), stays green under the fallback, because that
  posture carries no `email` and `display` resolves to the subject. It is the
  subject-only pin, so no separate fallback case is added there.
  `it("offers the appearance preference where no subject resolves")` at
  `:15457-15476`, which asserts `queryByTestId("account-trigger")` is null
  (`:15468`), stays unchanged and is what pins that the render gate stayed keyed
  on the subject.
- No `web/ui/src/session.ts` unit case is added, because UI-1 adds no accessor
  there. `web/ui/src/layerrights.test.ts` is unchanged.

## Manual validation

The change alters two surfaces a human reads directly: the CLI's exit status and
stderr on a refused registration, and the account cluster the web UI draws. Both
are read on the Keycloak-backed `oidc-jwt` stack S44 stands up, where every
signed-in caller holds no tenant-admin grant. Each amended scenario is re-run by
hand before it is committed.

**S44's realm users gain an email.** `$KC create users -r master -s
username=carol -s enabled=true` (`test/manual-validation.md:3941`) and the realm
`admin` user carry no email, so the posture read reports none and the account
cluster renders the fallback. The bootstrap-admin block's `create users`
invocation gains `-s email=carol@acme.com`, and S44's Prerequisites gain one
command setting an email on the user S47 signs in as:

```bash
$KC update "users/$($KC get users -r master -q username=admin --fields id --format csv --noquotes)" \
  -r master -s email=alice@acme.com -s emailVerified=true
```

A sentence beside it states why: the account cluster renders the caller's own
email, the `profile email` scopes the client already requests carry the claim,
and a realm user with no email address exercises the fallback rather than the
rendering.

**S47's Prerequisites** (`test/manual-validation.md:4686-4688`) are unchanged.
They read "run S44's Prerequisites and steps 1 to 4", and item 5 of S44's
Prerequisites already exports `TOKEN`, minting it with the password grant for
realm user `admin` (`test/manual-validation.md:4087-4095`). That is the user S47
step 3 signs in as in the browser (`:4722-4724`) and the user S44's
Prerequisites' `$KC update` gives an email to, so the credentialed posture
reading below and the browser rendering report the same caller. S44 step 1 says
the same of the variable's origin: "`ISSUER` and `TOKEN` come from the
Prerequisites above and are already exported" (`:4124-4126`).

**S47 step 1** (`test/manual-validation.md:4692-4710`) reads the posture
anonymously. Its Expect gains one sentence, and the step gains a second command
reading the same route with the caller's own credential.

**Expect.** … and there is no `email` key, for the same reason there is no
`subject` key: this request carries no credential.

```bash
curl -sS -H "Authorization: Bearer $TOKEN" "http://127.0.0.1:8153/v1/ui/session"; echo
```

**Expect.** HTTP 200 with `subject` naming the caller's Keycloak `sub`, which is
a UUID, and `email` reporting `alice@acme.com`. The two keys carry different
values, which is what the account cluster reads in step 3. An answer carrying
`subject` and no `email` means the realm user has no email address set, so the
S44 Prerequisites' `$KC update` did not take and step 3 will read the fallback
rendering instead.

**S47 step 3's Expect** (`:4732-4744`) states today that after the login "the
top bar carries the caller's own subject instead of a Sign in button". That
sentence is replaced:

… and the account cluster stands where the sign-in control was: the top bar
carries the caller's own email, `alice@acme.com`, with an avatar reading `A`,
which is what `initialsOf` derives from a local part carrying no `.`, `-`, or
`_`, and the sign-out control sits inside the menu that cluster opens. A top bar
carrying the UUID from step 1 here means the posture read reported no `email`
key or the bundle is a build from before the cluster read it, and the reader is
looking at a provider-chosen identifier where the design draws their own
address.

**S47 step 6's instruction** (`:4782`) reads today "Open the account cluster
from the subject in the top bar". It becomes "Open the account cluster from the
email in the top bar", and its Expect is otherwise unchanged.

**S55 gains a third step**, after its existing `local_source` terminal check at
`:5691-5705` and before the Cleanup paragraph. The scenario already stands up a
signed-in non-admin with `TOKEN` exported, and the analogous local-source
refusal is a step inside it rather than a scenario of its own.

3. Register a layer with a visibility flag from the terminal with the same
   caller's token.

   ```bash
   curl -sS -X POST "http://127.0.0.1:8153/v1/layers" \
     -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
     -d '{"id":"s55-public","source_type":"git","repo":"https://git.acme.internal/alice/x.git","ref":"main","public":true}' \
     -w '\nstatus=%{http_code}\n'
   ```

   **Expect.** `auth.forbidden` at HTTP 403, with `"constraint":
   "admin_only_fields"` in the envelope's `details` and the message naming
   `public`. A `201` means the registration was accepted with its visibility
   discarded, which is the defect this change closes: the caller reads a success
   record for a layer that is not public. Re-sending the same body without
   `"public": true` answers `201`, and the stored layer reports `UserDefined:
   true` with the caller alone in `Users`.

S55's `**Covers.**` line (`:5667-5668`) names §7.3.1 local-source authorization,
§7.3.4 `layer_capabilities`, and the §13.10 layer panel. It gains
`§7.3.1 admin-only registration fields`.

## Documentation changes

**DOCS-1: the reference and deployment pages follow the new rules.** Each
section is written to add no new runnable block, because `doccov-check` fails on
a new runnable example without its `tools/doccov/manifest.yaml` entry and its
executing end-to-end test.

Every page below carries the deployment-mode carve-out, and its wording is
SPEC-1's own: a registry started with no identity provider configured, or one
started in public mode (§13.10), authenticates no caller and admits every caller
on the admin arm, so the rule refuses nothing there. Each bullet names the
page-local sentence the clause attaches to and does not restate it, so the five
pages take one wording and cannot drift apart from each other or from SPEC-1.

- `docs/reference/http-api.md`: the `### Register a layer` body at `:345-347`
  gains a paragraph beside the local-source paragraph, stating that `owner`,
  `public`, `organization`, `groups`, and `users` are read on a tenant admin's
  registration alone, that a caller without the `admin` role asserting any of
  them is refused with `403 auth.forbidden` carrying `details.constraint:
  "admin_only_fields"`, that a field is asserted by its value so a false
  boolean, an empty array, an empty `owner`, and an `owner` naming the caller's
  own subject assert nothing, and that a registration asserting none of them is
  resolved to a user-defined layer as before. The paragraph carries the
  carve-out clause above beside `:322` and `:324`, which state the same reading
  for their own rules. The `### Session posture` block
  at `:56-84` gains `"email": "alice@acme.com"` in the sample body, its field
  paragraph at `:80` gains a sentence describing `email` in SPEC-2's wording,
  its opening prose at `:62` gains the same "report `subject` and `email`"
  clause, and the closed-against sentence at `:84`, which is the page's mirror
  of §7.3.4's closing sentence, takes SPEC-2's replacement. `:82`, the
  `layer_capabilities` paragraph, is untouched.
- `docs/reference/error-codes.md`: the `auth.forbidden` row at `:60` gains the
  admin-only registration fields arm and its `details.constraint` value, states
  the value-keyed reading, and states that the class field is resolved rather
  than refused. The new arm carries the carve-out clause above inside that row,
  where both existing arms already carry it.
- `docs/reference/cli.md`: the `podium layer register` section's `--user-defined`
  bullet at `:428` says today that `--public`, `--organization`, `--group`, and
  `--user` "are ignored on this form and `--owner` is honored only when no
  authenticated identity resolves". Both halves stop being true for a caller a
  registry authenticates. The bullet is rewritten to state that the registry
  derives a user-defined layer's owner from the authenticated caller and sets
  its visibility to that owner alone; that on a registry that authenticates
  callers, a caller without the `admin` role who sends any of `--public`,
  `--organization`, `--group`, `--user`, or an `--owner` naming another subject
  is rejected with `auth.forbidden` carrying `details.constraint:
  "admin_only_fields"`; and to carry the carve-out clause above, extended with
  the consequence that `--owner` is the mechanism on such a registry and no flag
  is refused there. Its closing sentence about
  `podium layer update` stays, and the visibility bullets at `:429-431` gain the
  same qualification. The paragraph lands beside the `--local` paragraph at
  `:422`, which is the precedent form.
- `docs/deployment/access-control.md`: the layer rules table at `:90-97` gains
  one row, "Register a layer asserting an owner, `public`, `organization`,
  `groups`, or `users`" against "Refused with `auth.forbidden`,
  `details.constraint: "admin_only_fields"`. A registration asserting none of
  them is permitted and resolves to a user-defined layer owned by the caller."
  The new row is placed immediately after the existing register row at `:92`,
  so the general rule and its carve-out read together the way `:95` and `:96`
  already do. That existing row states without qualification that registering a
  `git` layer on a network repository is permitted for any caller who resolves a
  verified subject, which the new row contradicts, so it is qualified in the
  page's own carve-out form: "Permitted for a caller who resolves a verified
  subject where the registration asserts none of `owner`, `public`,
  `organization`, `groups`, and `users`; see the row below. The registry
  resolves it to a user-defined layer owned by that subject. A caller who
  resolves no subject is refused with `auth.forbidden`." The carve-out clause
  above attaches at `:99`, the paragraph that already states both existing rules
  admit every request on a registry authenticating no caller, which gains this
  rule.
- The session-posture paragraph at `docs/deployment/access-control.md:101`
  states that the read reports `layer_capabilities.manage_any_layer` for the
  requesting caller alone. It gains one clause naming `email` on the same
  footing.
- `docs/deployment/layers.md` is the operator-facing account of runtime layer
  registration, and two of its statements go false. The sentence at `:100`
  states unconditionally that an authenticated caller without the tenant
  `admin` role registers a user-defined layer whether or not `--user-defined`
  is passed; it is qualified to cover a registration asserting none of `owner`,
  `public`, `organization`, `groups`, and `users`, and to name the
  `auth.forbidden` refusal carrying `details.constraint: "admin_only_fields"`
  otherwise. A paragraph stating the rule, the value-keyed reading, and the
  carve-out clause above lands in a new section beside the page's `### Who may
  register a local-source layer` section at `:102-104`, which is the precedent
  form proposal 0016 established on this page. The `--organization` invocation
  at `:85-87` gains the same in-fence
  role note the `--local` invocation carries at `:89-90`, naming the
  administrator role the visibility flags now require. The `--group` at `:92`
  sits on that local-source invocation, which already carries the note, so it
  needs no second one. The edit is confined to prose and in-fence comments, so
  the page's runnable block set is unchanged and `doccov-check` stays green on
  its `D-cli` slug (`tools/doccov/manifest.yaml:51-52`).

`docs/deployment/gateway-delegated-identity.md` is not edited. Its header table
at `:74-80` already documents `X-Podium-User-Email`, and no header, no
`IdentityFromTrustedHeaders` behavior, and no statement on that page changes.

**DOCS-2: the hand-run scenarios.** The amendments are staged in full in
Manual validation: S44's realm-user email, S47 steps 1, 3, and 6, S55's new step
3, and S55's `**Covers.**` line.

**DOCS-3: the changelog.** `CHANGELOG.md`'s `Unreleased` section gains, under
`Changed`, that `POST /v1/layers` now refuses a registration asserting an
admin-only field from a caller without the tenant-admin role rather than
discarding it and answering `201`, naming the `admin_only_fields` constraint and
the value-keyed reading; and, under `Added`, that the session posture read
reports the requesting caller's own `email` and the web UI's account cluster
renders it. An operator whose automation sends `--public` as a non-admin reads
the change there, because the registration answered `201` before and answers
`403` now.

## Open questions

**OQ-1. Should an asserted registration class be refused as well?** The staged
rule refuses the ownership and visibility fields and leaves `user_defined:
false` from a non-admin resolved down to the user-defined class, which is what
`spec/07-external-integration.md:97` mandates and what the `201` body reports
back through the echoed configuration. D5 gives the grounds: the assertion is
unreachable as a distinct harm, and the repo's own web client transmits the key
unconditionally with a class state computed once at mount, so an
omitted-versus-false distinction would turn a slow or failed posture read into
an authorization refusal for a registration that succeeds today.

Refusing it anyway is the reviewer's to take, and it is not a one-line addition.
It requires `LayerRegisterRequest.UserDefined` to become a `*bool`, because a
plain `bool` cannot distinguish an asserted false from an omitted key; it
requires `web/ui/src/surfaces/RegisterLayerForm.tsx:211` to send the key only
when true, or to derive the class at submit time from the current subject rather
than from the mount-time `useState` at `:74`; it requires the two pinned client
cases whose `expect(sent.user_defined).toBe(false)` assertions sit at
`web/ui/src/surfaces.test.tsx:9763` and `:9858` to move with it; and it
requires the withholding rationale comment at `RegisterLayerForm.tsx:87-95` to
be rewritten, because it justifies the hidden control on the registry resolving
that value rather than refusing it. None of that is staged here.

## Non-goals

- **A tri-state on `user_defined`, and any refusal of an asserted registration
  class.** D5 gives the grounds and OQ-1 puts the alternative to the reviewer.
  `LayerRegisterRequest.UserDefined` stays a plain `bool`, the class resolution
  is unchanged, and a raw HTTP client sending `user_defined: false` still has
  its class resolved down to user-defined, which the `201` body reports back
  through the echoed configuration.
- **Any change to the server-side class resolution for a registration that
  asserts nothing.** §7.3.1 requires it and §14.9's invocation carries no class
  flag, so a bare non-admin registration keeps answering `201` with the implicit
  `users: [<registrant>]` visibility.
- **A display name in the account cluster.** `layer.Identity` carries no name,
  `pkg/identity` reads no `name` claim, and `spec/` mandates none: §6.3.3 fixes
  what the registry records from a token and from the gateway headers, and a
  display name is in neither list. Adding one would need a §6.3.3 amendment
  naming the claim before any code reads one, and it would render
  per-deployment-divergent output, because `trusted-headers` carries no such
  header and a provider issuing `preferred_username` and no `name` resolves
  none. The browser session credential is an access token
  (`pkg/registry/server/webui_auth.go:144-147`), on which the OIDC `name` claim
  is not guaranteed, so the claim's source would have to be settled as well. The
  design's name line (`web/design/README.md:200`) is left unrendered, and a
  separate proposal can add it.
- **A new `X-Podium-User-Name` trusted-headers request header.** The header set
  is a fixed §6.3.3 wire contract, the gateway already injects
  `X-Podium-User-Email`, and the client's fallback renders that email, so the
  account cluster is correct there without it.
- **A new §6.10 error code, a new matrix cell, a new scope, and a new
  environment variable or configuration key.** The refusal reuses
  `auth.forbidden` with a second `details.constraint` value, and the email
  reuses the claim the default scope set already carries.
- **A `details.fields` key, a warnings field, an advisory header, or a
  partial-success status on the register response.** `details` carries
  `constraint` alone, which is the shipped form, and the asserted field names
  are named in the message. The response type is unchanged.
- **Any change to the CLI's register body construction.** D12 gives the grounds:
  hoisting `--owner` out of the `--user-defined` block would newly route it to
  the admin-defined arm, contradicting `docs/reference/cli.md` and the rationale
  recorded at `test/e2e/standalone_layer_test.go:73-75`.
- **A role badge, a capability report, or a group list in the account cluster**,
  which `web/design/README.md:200` rules out.
- **The `update` route's discard of `owner`, `public`, `organization`, `groups`,
  and `users` on a user-defined layer** (`pkg/registry/server/layers.go:726-747`).
  It is a different rule with a different key: the guard reads the layer's
  stored, immutable §4.6 class rather than a class resolved from the caller, so
  the discard is caller-independent and silences a tenant admin's patch exactly
  as it silences the owner's. `authorizeLayerWrite` already confines a
  non-admin to layers they own, `patch.UserDefined` is never read so there is no
  class assertion to refuse, and the CLI sends those fields only when the
  operator set them (`cmd/podium/layer.go:112-126`), so the value-versus-presence
  hazard does not arise. Its honest fix is a §4.6 immutability refusal binding
  every caller, and it is recorded here for a separate proposal.
- **Backfilling the email onto layers, audit entries, or any surface other than
  the §7.3.4 posture read.** No existing consumer of `Identity.Email` changes.

## Resolved in adversarial review

Review rounds populate this section.

### Pass 1 (2026-09-02, automated)

The draft's first challenge pass, whose corrections are folded into the text
above.

- **The `user_defined` limb of the refusal was cut, and the `*bool` request-type
  change with it.** The draft justified both on the premise that `user_defined:
  false` "is today inexpressible by any shipped client and indistinguishable
  from omission on the wire". The web UI is a shipped client and transmits it
  unconditionally (`web/ui/src/api.ts:501`,
  `web/ui/src/surfaces/RegisterLayerForm.tsx:211`), with the false arm pinned by
  two existing cases (`web/ui/src/surfaces.test.tsx:9763`, `:9858`) and the class
  state computed once at mount (`RegisterLayerForm.tsx:74`), so an authenticated
  non-admin whose posture read failed would have been refused where the
  registration succeeds today. The limb also refused the request
  `spec/07-external-integration.md:97` mandates resolving, and it inverted the
  rationale the shipped form documents for withholding its own class control
  (`RegisterLayerForm.tsx:87-95`). The residual is recorded as a non-goal and
  put to the reviewer as OQ-1.
- **`details.fields` was cut.** It was a new wire key with no consumer: the
  shipped form carries `constraint` alone
  (`pkg/registry/server/layer_capabilities.go:99-101`), the CLI prints the raw
  body, and no client change in the draft read it. The asserted field names are
  named in the message instead.
- **The `owner` limb was narrowed** to refuse only an owner naming a subject
  other than the caller's own, because a caller echoing its own subject asks for
  what §7.3.1 already mandates storing, and `--user-defined --owner <own sub>`
  is a working invocation today.
- **`CODE-2` was dropped whole, and the `name` half of SPEC-2, CODE-3, UI-1,
  TEST-1, and DOCS-1 with it.** No §6.10 or §6.3.3 text mandates a display name,
  no `name` claim is read anywhere in the tree, and neither `identity.Identity`
  nor `layer.Identity` carries such a field, so the draft would have landed a
  claim read and two struct fields with no spec section behind them. It also
  defeated the defect it was written to fix, since `trusted-headers` names no
  such header and a `preferred_username`-only provider resolves none. Email is
  already a first-class §6.3.3 attribute carried on the seam the handler holds,
  and it satisfies the top-bar design rule (`web/design/README.md:91`) exactly.
  OQ-1 and OQ-2 of the draft were both residuals of the `name` field and were
  deleted; the surviving OQ-1 is the class question.
- **UI-1's wiring was corrected.** The draft derived the display object in the
  shell beside `caps` (`web/ui/src/App.tsx:324-330`) and threaded it into the
  cluster. The cluster is not fed from there: `TopBar` derives its own subject at
  `:1226`, and App's `subject` at `:324` feeds `mayRegister` and `anonymous`.
  The change is at `:1226` alone, the render gate at `:1265` stays keyed on the
  subject, and the stale citations for the trigger, the menu identity line, and
  `initialsOf` were corrected to `:1339-1353`, `:1363`, and `:1508-1521`. The
  draft's claim that `initialsOf` "keeps its splitting rule" was correct only
  because the name was cut; its character class carries no whitespace and would
  have returned `A` for "Alice Example".
- **The exported `displayIdentity` accessor was cut.** With the name gone the
  fallback is one expression at one site, and `capabilitiesOf`'s precedent is a
  closed authorization default rather than a rendering fallback.
- **CLI-1 lost its code half.** The draft's rationale, that the CLI's
  unconditional empty-owner send "is what forces the refusal to key on values
  rather than on presence", is false: presence-keying was never available,
  because `LayerRegisterRequest` decodes into plain fields. The staged body
  change would have hoisted `--owner` out of the `--user-defined` block, newly
  routing it to the admin-defined arm and contradicting `docs/reference/cli.md`
  and `test/e2e/standalone_layer_test.go:73-75`. `--owner` was also removed from
  the flags whose help names the administrator role, because it is the
  documented mechanism on a no-identity deployment where the refusal never
  fires.
- **TEST-1's targets were corrected.** `web/ui/src/session.test.ts` does not
  exist; `session.ts` is unit-tested from `web/ui/src/layerrights.test.ts`, and
  with the accessor cut no case lands there at all. The proposed integration
  cell duplicated `test/integration/layer_write_authorization_test.go:234-250`
  verbatim, so it became an assert-unchanged statement, and the cell at
  `:208-222` was added as the second regression pin, which is the one CODE-1's
  ordering most endangers and which the draft did not name. The six single-field
  cases were folded into two table-driven tests.
- **The end-to-end file was corrected to
  `test/e2e/local_source_authorization_test.go`.** The revision named
  `test/e2e/cli_reference_test.go`, whose `startServer(t, "")` configures no
  identity provider, so its admin arm admits every caller and the refusal cannot
  fire there. The named file already drives a non-admin CLI refusal through
  `startAuthServer`, which is the harness this case needs.
- **DOCS-1's anchors were corrected.** `docs/reference/cli.md:428` is the
  `--user-defined` bullet whose two claims go false, and it fell outside the
  draft's stated range. `docs/reference/http-api.md:84` is the closed-against
  sentence SPEC-2 amends, where the draft named `:82`, the `layer_capabilities`
  paragraph. `docs/deployment/gateway-delegated-identity.md` was dropped, since
  nothing on it becomes false.
- **The manual scenario was folded into S55** rather than added after S57,
  following the precedent that the analogous local-source refusal is a step
  inside S55. The `$KC` realm-user email was added after checking
  `test/manual-validation.md:3941`: the stack's users carry no email, so without
  it the hand-run reading would validate the fallback and nothing else.
- **Two statements of fact in the problem restatement were corrected.** The
  `201` response echoes `store.LayerConfig`, which carries no JSON tags except
  `WebhookSecret`, so the field names are Go-cased and the discard is visible in
  the body. The defect is the `201` status and the zero exit code rather than
  the divergence being unreported.

### Pass 2 (2026-09-02, automated)

- **`pkg/registry/server/layer_write_auth_test.go` was added to TEST-1 and to
  S3.** `TestLayerRegister_TakeoverProduct` posts the body `{"user_defined":
  true, "owner": "bob"}` (`:343`) against a caller set that includes alice
  (`:316`, `:64`), so under CODE-1 alice's admitted cells would have flipped
  from `201` to `403` and the package would have been left red with no recorded
  disposition. The staged correction makes the body's `owner` the posting
  caller's own subject, which asserts nothing under D4, and moves
  `assertRegisteredOwner`'s `want := "bob"` seed with it. No cell changes
  status, so the table's documented invariant that the request body changes no
  outcome stays true.
- **The claim that the two existing integration cells pin CODE-1's ordering was
  withdrawn, and two cells that do pin it were staged.** Both cells post as
  bob, whose subject is `bob@acme.com` (`:145`), a body whose only admin-only
  field is `"owner": "bob@acme.com"`, which asserts nothing under D4, so
  neither is ordering-sensitive. "Watch out for", D8, TEST-1, and the two
  edge-case rows were corrected, and TEST-1 now stages a cell posting `"public":
  true` under alice's stored layer ID (pinning `authorizeLayerWrite` first) and
  a cell posting a `local` source with `"public": true` (pinning
  `authorizeLocalSource` first).
- **The `newClassHarness` description was inverted in D2 and TEST-1 and was
  corrected.** That harness denies every caller
  (`pkg/registry/server/layer_register_class_test.go:14-16`, `:25`), so the
  three refusal cases take it unchanged, and
  `TestLayerRegister_AdminArmAdmitsEveryField` builds an admitting endpoint the
  way `newLayerHarness` does (`pkg/registry/server/layers_test.go:23-32`,
  `pkg/registry/server/layers.go:190`) with an identity resolver installed. D2's
  reason the existing class cases stay green was restated as their bodies
  asserting no admin-only field.
- **The staged avatar label `AA` was corrected to `A`.** `initialsOf` splits the
  local part on `[.\-_]` (`web/ui/src/App.tsx:1508-1521`), and `alice` carries
  none of them, so the shipped function returns one character for
  `alice@acme.com`. TEST-3 and S47 step 3's Expect now name `A` and state why,
  and the assertion still discriminates against a UUID subject, whose parts
  split on `-`.
- **`docs/deployment/layers.md` was added to DOCS-1 and to S7.** Its sentence at
  `:100` states unconditionally that an authenticated non-admin registers a
  user-defined layer, and its runtime register block at `:85-92` shows an
  `--organization` invocation at `:85-87` with no role note beside a `--local`
  invocation at `:89-92` that carries one, so both go false. The page already carries the
  `### Who may register a local-source layer` section proposal 0016 added
  (`:102-104`), which is the precedent form, and it is doccov-tracked
  (`tools/doccov/manifest.yaml:51-52`), so the edit is confined to prose and
  in-fence comments.
- **Four unresolvable anchors were retargeted.** `docs/reference/http-api.md:60`
  is a closing fence and the opening prose is `:62`;
  `docs/deployment/access-control.md:100` and `:102` are blank lines and the
  paragraphs are `:99` and `:101`; `test/manual-validation.md:4778` is inside
  S47 step 5 and the instruction is `:4782`; and `pkg/layer/composer.go:22` is
  the `Sub` field, with `Email` at `:23`.
- **The widening of S47's Prerequisites to S44's steps 1 to 6 was withdrawn.**
  It rested on a misread anchor. `test/manual-validation.md:4087-4095` is item 5
  of S44's **Prerequisites** list, whose heading is at `:3956`, and not S44 step
  5, which is at `:4238` and consumes `$TOKEN` for a negative control rather than
  exporting it. S44's steps begin at `:4122`, and step 1 states that "`ISSUER`
  and `TOKEN` come from the Prerequisites above and are already exported"
  (`:4124-4126`). S47's existing "run S44's Prerequisites and steps 1 to 4"
  (`:4686`) therefore already brings `TOKEN` into scope, and the staged
  credentialed posture reading works unchanged. The S55 precedent sentence was
  dropped for the same reason: `:5676-5677` attributes the value to S44's
  Prerequisites, so S55 is no precedent for a step range carrying it. S47's
  Prerequisites stay at steps 1 to 4, and the paragraph now states where `TOKEN`
  comes from. The S8 checklist entry, the DOCS-2 paragraph, and the
  requirement-coverage row dropped their reference to an S47 Prerequisites edit.

### Pass 3 (2026-09-02, automated)

- **UI-1's bundle item named a path that does not exist.** It staged
  regenerating `web/ui/dist`, which no build writes and which git would refuse
  to track, because `.gitignore:25` carries a bare `dist/` entry and vite
  therefore emits to `../bundle` (`web/ui/vite.config.ts:9-12`, `:17`) where
  `web/web.go:19-20` embeds it. An implementor following it would have left
  `web/bundle` at its previous content, shipped a page that never reads the new
  `email` key, and failed the staleness gate at
  `.github/workflows/test.yml:207-220`. The item now names `web/bundle`, the
  `npm ci && npm run build` command in `web/ui` that produces it, and the
  concrete artifacts `web/bundle/index.html` and the content-hashed files under
  `web/bundle/assets/`, and S5's file list and the requirement-coverage row name
  the same path.
- **DOCS-1 left `docs/deployment/access-control.md:92` unqualified.** That row
  states without qualification that registering a `git` layer on a network
  repository is permitted for any caller who resolves a verified subject, so the
  new refusal row would have contradicted it inside one operator-facing
  authorization table. The bullet now stages the qualification on `:92` in the
  page's own carve-out form, the one `:95` already uses, and places the new row
  immediately after it. The table's anchor was corrected to `:90-97`, its actual
  extent.
- **D2's stated reason the existing class-resolution cases stay green was
  false for two of them.** `TestLayerEndpoint_UserDefinedBodyOwnerHonoredWithoutIdentity`
  posts `"owner": "alice@acme.com"` from a caller with no resolved subject
  (`pkg/registry/server/layer_register_class_test.go:112`) and
  `TestLayerEndpoint_AdminPlainRegisterStaysAdminDefined` posts
  `"organization": true` (`:137`), both of which are assertions under D3 and D4.
  What keeps them green is `newLayerHarness`'s bare `NewLayerEndpoint`, whose
  admitting `authAdmin` (`pkg/registry/server/layers.go:190`) leaves `adminErr`
  nil so CODE-1's guard is never entered. D2 now gives the per-harness reason and
  names the two bodies, so a later change to the field list or to those harnesses
  re-checks them.

### Pass 4 (2026-09-02, automated)

- **The register-handler anchors resolved to the wrong lines.** The "Watch out
  for" bullet and CODE-1 step 1 both named `:832` for the second
  `e.authAdmin(r)` call, which sits at `:831` while `:832` is the subject test
  inside it; CODE-1 step 3 named `:826` for the `caller` declaration, which is
  at `:828` while `:826` is a comment line; and D12 named `:890` for
  `cfg.Owner = req.Owner`, which is at `:888` while `:890` assigns
  `cfg.Organization`, so the citation carrying that fixed decision's reasoning
  resolved to an unrelated statement. All three are retargeted, and the two
  adjacent ranges that began on a comment or a blank line are tightened: the
  local-source call is `:847-849` and the user-defined arm's `req.Owner`
  fallback is `:872-876`. The class-resolution range is `:828-839` wherever it
  appears.
- **The two reference-page bullets dropped the no-identity-provider arm.**
  SPEC-1's staged paragraph states that a registry started with no identity
  provider configured, or one started in public mode, authenticates no caller
  and admits every caller on the admin arm, so the rule refuses nothing there,
  and the `cli.md`, `access-control.md`, and `layers.md` bullets each stage that
  clause. Without it the `http-api.md` register paragraph and the
  `error-codes.md` `auth.forbidden` row would have told a standalone or
  public-mode operator that their working `--public` registration is refused,
  which both pages already contradict for their existing rules at
  `docs/reference/http-api.md:322` and `:324` and twice in
  `docs/reference/error-codes.md:60`. Both bullets now stage the same clause.

### Pass 5 (2026-09-02, automated)

- **DOCS-1's `layers.md` fence edit named a `--group` invocation that does not
  exist.** The runtime register block at `docs/deployment/layers.md:83-92`
  carries `--group` once, at `:92`, inside the local-source invocation whose
  role note is already at `:89-90`, so the instruction asked the implementor to
  add a note that invocation already carries. The bullet now names the one
  invocation that lacks a note, the `--organization` registration at `:85-87`,
  and states that the `--group` at `:92` needs none. The pass-2 record that
  introduced the claim is corrected to the same reading.
- **D7's citation for the CLI printing the raw body pointed at the `return`
  statement.** `cmd/podium/layer.go:246` is `return 1`; the print is
  `fmt.Fprintf(os.Stderr, "register failed: HTTP %d\n%s\n", status, out)` at
  `:245`. D7 now cites `:245`, which agrees with the `:243-248` range the
  current-state section already uses for the same fact.
- **The `201` answer was quoted as code that does not exist.** The
  current-state section quoted `writeJSON(w, http.StatusCreated,
  LayerRegisterResponse{Layer: cfg})` at `:980-984`, an expression absent from
  the tree, and the cited range covers the response construction and the
  git-source webhook branch rather than the status write. It now reads that
  `register` builds `resp := LayerRegisterResponse{Layer: cfg}` (`:980`) and
  answers `writeJSON(w, http.StatusCreated, resp)` (`:985`).

### Pass 6 (2026-09-02, pruning)

- **The restatements that fed the review's own defect rate were removed, so each
  fact has one home.** Two of the fifteen findings across passes 1 to 5 were
  defects the document's redundancy introduced, and the record names four-site
  and two-site corrections for single facts. Nine "Watch out for" bullets were
  deleted, because each restated a constraint already binding in a Decision, in
  CODE-1, or in TEST-1: the ordering the two existing integration cells do not
  pin (D8, TEST-1), the `TestLayerRegister_TakeoverProduct` correction (TEST-1),
  `newClassHarness`'s denying authorizer (D2, TEST-1), the unavailability of
  presence-keying and the CLI's unconditional `body["owner"]` (D3), the
  non-hoisting of `--owner` and its exclusion from the administrator-role
  wording (D12, CLI-1), and the twice-called `e.authAdmin(r)` (CODE-1 step 1).
  The bullets carrying a fact stated nowhere else are unchanged. In DOCS-1, the
  five per-page restatements of the no-identity-provider and public-mode
  carve-out, which is the drift pass 4 caught, were replaced by one statement of
  the clause in SPEC-1's wording at the head of DOCS-1, and each bullet now
  names only the page-local sentence the clause attaches to. No requirement was
  delegated or dropped, because every deleted sentence duplicated a statement
  that remains binding elsewhere, so the checklist, the test plan, and the
  requirement-coverage table are unchanged.

### Pass 7 (2026-09-02, automated)

- **The register-form edge-case row now cites the client case that pins the
  submitted body.** The row's "Where it is stated" column named TEST-3, which
  stages only the account-cluster cases extending `describe("the shell's
  identity cluster")` at `web/ui/src/surfaces.test.tsx:15376` and renders no
  register form, so a reviewer checking that the form's user-defined arm is
  still admitted landed on tests that cannot establish it. The column now names
  the existing case at `web/ui/src/surfaces.test.tsx:9721-9732`, which submits
  the form on its user-defined arm and asserts `public`, `organization`,
  `groups`, and `users` are all `undefined`. That is the pin the row's outcome
  rests on, and it is unchanged by this proposal.

### Pass 8 (2026-09-02, automated)

- **Every `web/ui/src/App.tsx` anchor was retargeted to the line that carries
  the code.** The cited numbers came from a commit on a sibling branch and sat
  about a dozen lines below the working tree's, so `TopBar`'s derivation, the
  cluster's render gate, `AccountMenu`'s trigger and identity line, `initialsOf`
  and its character class, App's `anonymous`, and the `<TopBar>` call site all
  resolved to unrelated code, including a closing brace where the render gate
  was named as the authority for the fail-closed rule. The Summary bullets, the
  current-state paragraph, UI-1's anchors and steps, the edge-case row, and
  the pass-1 record now cite `:1226` (in `TopBar`, which opens at `:1212`),
  `:1265-1271`, `:1339-1353` with the avatar at `:1350` and the trigger subject
  line at `:1352`, `:1363`, `:1508-1521` with the split at `:1512-1513`, `:372`,
  and `:410-411`. App's `subject` at `:324` and `mayRegister` at `:339` were
  already correct and are unchanged.
- **TEST-3's client-test anchors were retargeted, and the two cases it leans on
  are now named by their titles.** The `describe` block is at
  `web/ui/src/surfaces.test.tsx:15376`. The subject-only pin is `it("pins a
  theme onto the root element and returns it to the system setting")` at
  `:15426-15455`, whose posture stub is at `:15428` and whose trigger assertion
  is at `:15433`. The pin that the render gate raises no cluster without a
  subject is `it("offers the appearance preference where no subject resolves")`
  at `:15457-15476`, whose null assertion is at `:15468`. The previously cited
  ranges landed on a case asserting the opposite of the subject-only pin and on
  an accessible-name test for the appearance trigger.
- **Correction to the first bullet: D11 was removed from the list of retargeted
  sections.** D11 names no `web/ui/src/App.tsx` line and cites only
  `web/design/README.md:200`, so it took no anchor edit and listing it recorded
  a change that was never made.
- **The register-form pins now cite the submitted-body assertions.** The
  edge-case row cites `web/ui/src/surfaces.test.tsx:9721-9732`, which runs from
  the `fireEvent.submit` through `expect(sent.user_defined).toBe(true)` and the
  four `toBeUndefined` assertions. OQ-1 and the pass-1 record cite `:9763` and
  `:9858`, which are the two `expect(sent.user_defined).toBe(false)` assertions.
  The earlier numbers pointed at a computed-style assertion, a closing paren,
  and the comment block of a token-set styling test whose fixture sets
  `manage_any_layer: true`.

## Requirement coverage

The change carries two mandatory defects. Both are staged here, and neither is a
non-goal, an open decision, or a deferral.

| Defect | Deliverables that satisfy it |
|:--|:--|
| **Defect one.** `POST /v1/layers` discards the admin-only registration fields a non-admin asserts and answers `201`, and `podium layer register --public` exits `0` on a registration that applied none of it. | SPEC-1 (§7.3.1's admin-only registration fields paragraph and its `**Errors.**` arm); CODE-1 (the hoisted admin arm, `adminOnlyRegistrationFields`, and the refusal after the local-source call in `pkg/registry/server/layers.go`); CLI-1 (the four visibility flags' usage strings); TEST-1 (`TestLayerRegister_AdminOnlyFieldsRefused`, `…AssertNothing`, `…OnReRegistration`, `TestLayerRegister_AdminArmAdmitsEveryField`, the amended `TestLayerRegister_TakeoverProduct`, the two ordering cells in `test/integration/layer_write_authorization_test.go`, and `TestLayerCLI_AdminOnlyFieldsRefusedForNonAdmin`); DOCS-1's `docs/reference/http-api.md`, `docs/reference/error-codes.md`, `docs/reference/cli.md`, `docs/deployment/access-control.md`, and `docs/deployment/layers.md` edits; DOCS-2's S55 step 3 and `**Covers.**` line; DOCS-3's `Changed` entry. Checklist steps S1, S3, S6, S7, S8, and S9. |
| **Defect two.** The §7.3.4 posture read reports no email, so the web shell's account cluster draws a provider-chosen subject, which is a UUID on an `oidc-jwt` deployment. | SPEC-2 (§7.3.4's `email` bullet, its opening sentence, and its closing paragraph); CODE-3 (the posture handler's serialization under its existing subject guard and its declaration comment); UI-1 (`SessionPosture.email`, `TopBar`'s `display` derivation, `AccountMenu`'s render sites, and the regenerated `web/bundle`); TEST-2 (`TestSessionPosture_Email` and its closed-body assertion); TEST-3 (the two client cases on the shell's identity cluster); DOCS-1's session-posture edits on `docs/reference/http-api.md` and `docs/deployment/access-control.md`; DOCS-2's S44 realm-user email and S47 steps 1, 3, and 6; DOCS-3's `Added` entry. Checklist steps S2, S4, S5, S7, S8, and S9. |
