# Proposal 0020: Refuse an owner or visibility patch on a user-defined layer

- Issue: (to be filed)
- Status: Approved (2026-09-04). Signed off by the maintainer for implementation, whole, with every step in the checklist in scope. Converged after 4 adversarial review rounds (5 findings fixed). OQ-1 is resolved in favor of the staged position, so the refusal is `400 registry.invalid_argument` carrying `details.constraint: "immutable_visibility"`: no caller is authorized to widen a user-defined layer, so the failure is a property of the request rather than of the caller. OQ-2 is resolved in favor of the staged position, so `owner` is compared against the layer's stored owner, which is what admits a verbatim layer-object round trip for every caller the write rule admits.
- Date: 2026-09-04

This document stages the proposed spec, code, test, and documentation changes. It does not modify any spec, code, or doc file. Apply the changes in the staged sections after sign-off. Every anchor is read against `main` at `7ec5521`, which carries proposal 0019's snake_case conversion and its `git_provider` setter, so every layer JSON member is named in its post-0019 form.

## Summary

**What changes.**

- §7.3.1 gains a paragraph fixing that `POST|PUT /v1/layers/update` refuses a patch asserting `owner`, `public`, `organization`, `groups`, or `users` against a stored user-defined layer, with `400 registry.invalid_argument` carrying `details.constraint: "immutable_visibility"`. §7.3.1's **Errors** paragraph names the refusal, and §4.6's visibility sentence gains a cross-reference to it.
- `LayerEndpoint.update` (`pkg/registry/server/layers.go:709-833`) evaluates the rule ahead of every mutation it performs, including the webhook-secret rotation, and returns before `PutLayerConfig` and before `emitLayerEvent`. The `if !cfg.UserDefined { … }` guard at `:798-818` becomes an unconditional application block, because the refusal above it has removed the user-defined case.
- The rule binds every caller, a tenant admin included, and it binds a registry started with no identity provider and one in public mode. It reads the stored layer's class rather than the requesting caller, which is what distinguishes it from the three neighbouring §7.3.1 rules.
- A field is asserted by its value rather than by its presence, on the reading §7.3.1 already fixes for the register path. A value that restates what the layer already stores asserts nothing, and `owner` and `users` are compared against the stored values, so a client that reads a layer object and sends it back unchanged is admitted. The layer object carries `owner` and `users` on every layer, and a stored user-defined layer's `users` is `[<owner>]`, so that echo is the case the comparison exists for.
- `docs/reference/http-api.md`, `docs/reference/cli.md`, `docs/deployment/layers.md`, `web/design/README.md`, `web/DESIGN.md`, the stale comments under `web/ui/src` the "Watch out for" list names, `test/manual-validation.md`, and `CHANGELOG.md` follow. The web UI needs no behavioural change.

**Fixed decisions.**

- **The refusal is `400 registry.invalid_argument` rather than `403 auth.forbidden`.** No caller is authorized to widen a user-defined layer, so the failure is a property of the request against the stored record rather than of the caller. §4.6 states that the implicit `users: [<registrant>]` visibility "is set automatically and cannot be widened", in the section that defines the layer classes, and names no exempt caller. §4.7.2's admin powers cover admin-defined layers, tenant settings, reingests, re-embedding, and a read-side visibility override for diagnostics that is itself audited; none of them is a write to a user-defined layer's stored declaration. A `403` would tell an owner who holds every right on the layer that they lack one, and would tell a tenant admin the same on a layer the admin arm otherwise admits them on entirely. The symmetry counter-argument is recorded as OQ-1.
- **The refusal carries `details.constraint: "immutable_visibility"`**, joining `local_source` and `admin_only_fields` as a discriminator on an existing code, so a client branches without parsing prose.
- **A field is asserted by a value that differs from what the layer stores.** `public: false`, `organization: false`, an empty `groups` or `users`, an empty `owner`, an `owner` naming the layer's stored owner, and a `users` restating the stored `[<owner>]` assert nothing. This is sound rather than a compromise: `register`'s user-defined arm never reads `req.Public`, `req.Organization`, or `req.Groups` and derives the visibility from the authenticated identity, so a stored user-defined layer is always at `public: false`, `organization: false`, no groups, `users: [owner]`. Every value that would widen is therefore non-zero and differs from the stored value, and the predicate covers the whole widening axis with no gap.
- **The comparison against the stored value is exact.** It is element for element and byte for byte, and it neither trims whitespace nor reorders a slice, because an admitted patch falls through to the application block, which stores the value the patch carried. Admitting a value that is not byte-identical to the stored one would let an `owner` padded with whitespace replace the stored owner, and `cfg.Owner` bounds `authorizeLayerWrite` (`pkg/registry/server/layers.go:324`) and the per-identity user-defined layer cap (`:1008`), both of which compare it exactly. This also settles the `users` comparison as order-sensitive: a client echoing a value it read preserves the order it read, so exactness costs the round-trip nothing.
- **`owner` and `users` are compared against the layer's stored values, and `groups` is not compared at all.** A stored user-defined layer holds `groups` empty, so any non-empty value differs from the stored one by construction and the value alone decides it. `owner` and `users` are the two axes a layer object carries a non-zero value on, so each is compared against what the layer stores, which is what admits a client that reads a layer object and returns it unchanged. The parallel with `adminOnlyRegistrationFields`, which compares `owner` against the caller's subject, is recorded as OQ-2.
- **No pointer fields and no presence-aware decode.** `LayerRegisterRequest` is the decode target for both `register` and `update`, so converting five members to pointers would change the register path's semantics in the same edit and contradict §7.3.1's value-assertion sentence, which landed with proposal 0017.
- **The rule is evaluated above the rotation.** `rotate_webhook_secret` is applied at `:784-796` and the current guard runs at `:802`, so a patch carrying a rotation beside a widening today mints a fresh secret, discards the widening, and answers `200`, leaving the source repository's registered secret stale. The refusal placed above the rotation closes that.
- **The refusal rejects the whole request**, so a body carrying `ref` beside `public` applies neither, mints no credential, writes no record, and emits no §8.1 event. This matches the local-source refusal's stated treatment on the same endpoint.
- **The rule is evaluated after the layer write authorization rule and after the local-source authorization rule**, so a request either of those refuses keeps its own envelope.
- **The widening recourse is re-registration.** `POST /v1/layers` under a stored layer's ID is authorized on the stored layer's admin arm, and with the admin arm admitting the caller the class resolves from `req.UserDefined`, so an admin re-registers the ID as an admin-defined layer with the visibility they declare. `PutLayerConfig` is an upsert on `(tenant_id, id)` that overwrites `user_defined` and the visibility columns. The recourse is documented and exercised end to end rather than asserted.
- No §6.10 error code is added and no matrix cell changes. Podium is pre-1.0; no flag, environment variable, or `registry.yaml` key restores the discard.

**Watch out for.**

- **The rule refuses where every neighbouring rule goes quiet.** The layer write, local-source, and admin-only registration fields rules each state that a registry with no identity provider and one in public mode admit every caller on the admin arm. This rule refuses there too, because it never consults the caller. An implementor copying a neighbouring block will write that carve-out by reflex, and the staged spec text states the opposite explicitly.
- **The rotation runs before the current guard** (`:784-796` against `:802`), which is why the check's position is load-bearing rather than incidental.
- **One shipped test pins the discard.** `TestLayerEndpoint_UpdateCannotWidenUserDefined` (`pkg/registry/server/layer_visibility_test.go:34-57`) asserts `200` with an unchanged record, so it and the handler falsify each other and convert in one commit.
- **The web UI already withholds the controls, so no server-and-client commit is forced here.** `UpdateLayerForm.tsx:46` computes `editableVisibility = layer.user_defined !== true` and the patch builder populates the visibility members only inside that guard; `LayerUpdate` (`web/ui/src/api.ts`) declares no `owner` at all. On a user-defined layer the form renders a static display instead. This is the opposite of proposal 0019's constraint, where `TestWebUI_ServedBundleReadsTheLayerRecordFields` spanned both sides.
- **The CLI cannot send a false boolean or an empty array.** `--public` and `--organization` are bool flags written only when true, and `--group`/`--user` are repeatable slice flags written only when non-empty (`cmd/podium/layer.go:111-125`), so every CLI request that trips the rule is one where the operator asked to widen a user-defined layer.
- **On a deployment with no identity provider, `--owner` at registration is how a user-defined layer's owner is named.** After this change an operator correcting a typo through `podium layer update --owner` reads a `400` rather than a silent `200`. The correction never applied; the `200` said it did. The recourse is re-registration, which that deployment admits.
- **A user-defined layer with an empty stored owner is possible** on a registry that authenticated no caller at registration. Any non-empty `owner` in a patch then differs from the stored value and is refused, which is the correct direction: the field is not patchable on the class.
- **The prose sites that state the discard as current behaviour go false**: `docs/reference/http-api.md:476`, `docs/reference/cli.md:430`, `web/design/README.md:172`, `web/DESIGN.md:361-364`, and the header comment of `web/ui/src/surfaces/UpdateLayerForm.tsx`. `web/DESIGN.md` is a separate file from `web/design/README.md` and states the registry behaviour in the same words, so a sweep that reads only one of them leaves the other asserting the removed behaviour. One further site, `web/ui/src/surfaces.test.tsx:9771-9773`, describes the *register* path's discard, which proposal 0017 already made a refusal, and is corrected in the same sweep so a reader is not left with two accounts of one rule.
- **The audit emission is unpinned.** No test asserts that an update emits a §8.1 event, so a change to it passes the existing suite either way.

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1, SPEC-2, SPEC-3. §7.3.1 gains the immutable-visibility paragraph, its **Errors** paragraph names the refusal, and §4.6's visibility sentence gains its cross-reference. Committed alone and verified before any code.
      Levels: —. Depends on: —
- [ ] **S2 · code** — CODE-1, TEST-1, TEST-2. `assertedImmutableVisibilityFields` beside `adminOnlyRegistrationFields`; the refusal in `update` placed above the rotation and below the local-source gate; the guard at `:798-818` made unconditional; the helper's unit table; and the rewrite of `TestLayerEndpoint_UpdateCannotWidenUserDefined`. Indivisible: the rewritten test asserts `400` and the old one asserts `200`, so either landing alone is a red commit.
      Levels: unit, integration. Depends on: S1
- [ ] **S3 · test** — TEST-3. The end-to-end arms through the built binary: the owner refused, the tenant admin refused identically, the non-owner refused with `auth.forbidden` ahead of this rule, the record read back unchanged, the admin's re-registration recourse widening the layer, and the owner's arm run again against a public-mode registry and refused there.
      Levels: e2e. Depends on: S2
- [ ] **S4 · code** — UI-1, TEST-4. The two stale comments under `web/ui/src`, the added vitest case pinning that the form sends no visibility member on a user-defined layer, and the rebuilt `web/bundle` committed in this same commit whether or not it changed.
      Levels: unit. Depends on: S1
- [ ] **S5 · docs** — DOC-1, DOC-2, DOC-3. `docs/reference/http-api.md:476`, `docs/reference/cli.md:430`, `docs/deployment/layers.md`, `web/design/README.md:172`, and `web/DESIGN.md:361-364`.
      Levels: —. Depends on: S2
- [ ] **S6 · docs** — DOC-4. `test/manual-validation.md` gains S59, and S44's two reusable blocks that scope themselves by enumeration are extended to name it: the stack note at `:4050-4051` and the "Bootstrap admin" amendment's heading at `:4055` and body sentence at `:4058`.
      Levels: —. Depends on: S2, S3, S4
- [ ] **S7 · docs** — DOC-5. The `CHANGELOG.md` entry.
      Levels: —. Depends on: S5

**Ordering constraints.** S1 precedes every other step, per the spec-first rule. S2 is indivisible for the reason stated on its line. S2 and S4 are **not** required to be one commit, which is the finding that determines this sequence: the UI sends nothing this rule refuses, so no test spans the two sides, and the bundle rebuild follows from S4's comment edits alone. The bundle rebuild lands in S4 and nowhere else, because no later step touches `web/ui/src`.

## Current state and the gap

`LayerEndpoint.update` authorizes the write, decodes the patch, validates `force_push_policy` and `git_provider`, evaluates the local-source rule at `:757`, applies `force_push_policy`, `git_provider`, `ref`, `root`, and `local_path`, performs a requested rotation at `:784-796`, and only then reaches:

```go
	// spec: §4.6 — a user-defined layer's owner and implicit
	// users:[owner] visibility are fixed at registration and cannot be
	// widened, so visibility/owner patches are ignored for it. An admin
	// may edit an admin-defined layer's visibility.
	if !cfg.UserDefined {
		if patch.Owner != "" { cfg.Owner = patch.Owner }
		...
	}
```

(`:798-818`.) There is no `else`. Control falls through to `PutLayerConfig` at `:819`, `emitLayerEvent(r, cfg, "update")` at `:825`, and `writeJSON(w, http.StatusOK, resp)` at `:833`. The caller is told the write succeeded, and the record in the same response reports the visibility the patch tried to replace.

Three consequences follow from the guard's position rather than from the discard alone. A patch carrying a visibility field beside `ref` applies `ref` and drops the rest silently. A patch carrying a rotation beside a widening mints a fresh secret the operator must register at the Git host, from a request that changed nothing else. And the unconditional emission writes a §8.1 event reporting a config change that did not occur.

`authorizeLayerWrite` (`:322-329`) already confines a non-admin to a layer they own, so the caller reaching this code on a user-defined layer is that layer's owner or a tenant admin. Both are fully authorized to write the layer, which is why the refusal has to be about the field rather than about the caller.

§4.6 is right about the rule. `spec/04-artifact-model.md:580` states that each user-defined layer "is visible only to the user who registered it", and `:611` states that the implicit `users: [<registrant>]` visibility "is set automatically and cannot be widened". Neither sentence is conditioned on who asks. §7.3.1 already refuses the identical assertion on the identical fields on the register path, stating the reason explicitly: "rather than having the assertion discarded and answered `201`". Proposal 0017 landed that rule, recorded that the update path's honest fix is "a §4.6 immutability refusal binding every caller", and deferred it. This proposal is that fix.

## Spec amendment: §7.3.1 immutable visibility on a user-defined layer

**SPEC-1.** Anchor: `spec/07-external-integration.md`, §7.3.1. One paragraph lands after the paragraph beginning `**Admin-only registration fields.**` and before the paragraph beginning `**Git provider selection.**`.

> **Immutable visibility on a user-defined layer.** §4.6 fixes a user-defined layer's owner and its implicit `users: [<owner>]` visibility at registration, so an `update` request against a stored layer of that class asserting `owner`, `public`, `organization`, `groups`, or `users` is refused with `400 registry.invalid_argument` (§6.10) carrying `details.constraint: "immutable_visibility"`, rather than having the assertion discarded and answered `200`. The refusal names the asserted fields in its message. A field is asserted by its value rather than by its presence, on the reading the admin-only registration fields rule above states, and a value that differs from what the layer stores is what asserts it: `public` or `organization` set to true, a non-empty `groups`, a non-empty `users` differing from the layer's stored `users`, and an `owner` naming a subject other than the layer's stored owner. A false boolean, an empty array, an empty string, and a `groups`, `users`, or `owner` restating what the layer stores assert nothing, so a client that reads the layer object and returns it unchanged is admitted. The comparison against a stored value is exact, element for element and byte for byte, so a value differing from the stored one only in element order or in surrounding whitespace is an assertion and is refused. The rule reads the stored layer's class rather than the requesting caller, so it binds every caller the layer write authorization rule admits, a tenant admin included, and a registry started with no identity provider configured and one started in public mode (§13.10) refuse on the same terms; this is what distinguishes it from the layer write, local-source, and admin-only registration fields rules, each of which admits every caller there. The §4.7.2 admin role overrides visibility on the read side for diagnostics and writes no layer's stored declaration. The rule is evaluated before any field of the patch is applied, before a requested webhook-secret rotation is performed, and before the layer is written, so a refused request leaves the stored configuration unchanged, mints no credential, applies no other field the same patch carries, and emits no §8.1 event. A tenant admin who needs a user's layer visible more widely re-registers its ID through `register` as an admin-defined layer, which the layer write authorization rule authorizes to an admin on a stored user-defined layer and which replaces the stored record with one of a class whose visibility an admin declares; that re-registration resets the layer's order, its registration time, and its ingest history, and mints a new inbound webhook secret for a `git` source. On a stored layer that is admin-defined these fields remain patchable and the rule does not reach it. The rule is evaluated after the layer write authorization rule and after the local-source authorization rule above, so a request either of those refuses keeps its own envelope.

**SPEC-2.** Anchor: `spec/07-external-integration.md`, §7.3.1, the `**Errors.**` paragraph. Append to the closing enumeration, after the `admin_only_fields` clause:

> and an update asserting an owner or a visibility field against a stored user-defined layer, which the immutable visibility rule above refuses (`registry.invalid_argument`, carrying `details.constraint: "immutable_visibility"`).

**SPEC-3.** Anchor: `spec/04-artifact-model.md`, §4.6, the visibility paragraph. Append to the sentence beginning "User-defined layers (§7.3.1) have implicit visibility":

> The layer's owner is fixed on the same terms. Neither is patchable afterwards by any caller, including a tenant admin; §7.3.1 states the refusal the update operation returns and the re-registration that places the layer in the admin-defined class.

`registry.invalid_argument` is an existing §6.10 code with an existing matrix cell, and `details.constraint` is an existing envelope discriminator, so no §6.10 amendment and no matrix cell is added.

## Proposed solution

### CODE-1: the helper and the refusal

Add beside `adminOnlyRegistrationFields` (`pkg/registry/server/layers.go:507`):

```go
// assertedImmutableVisibilityFields reports the §4.6 fields the patch asserts
// against a stored user-defined layer, in sorted order, and nothing against an
// admin-defined one. A field is asserted by its value rather than by its
// presence, for the reason adminOnlyRegistrationFields states: the request
// decodes into plain string, bool, and []string fields, so an absent key and a
// zero value are one thing after decode. The predicate loses nothing by it,
// because register derives a user-defined layer's visibility from the
// authenticated identity and never reads the request's public, organization,
// or groups, so a stored layer of that class is always at public:false,
// organization:false, no groups, users:[owner], and every widening value is
// non-zero.
//
// What asserts is a value that differs from what the layer stores. On the
// three axes the class stores at their zero value, any non-zero value differs
// by construction, so the check is the value alone. Users is the one axis
// carrying a non-zero stored value, so it is compared, and owner is compared
// against the layer's stored owner rather than against the caller's subject,
// so a tenant admin who echoes back the owner they read asserts nothing.
// store.LayerConfig marshals owner and users without omitempty, so a client
// returning a layer object it read carries users:[<owner>] on every
// user-defined layer, and that echo is the case these two comparisons exist
// for.
//
// The comparison is exact and neither trims nor reorders. An admitted patch
// falls through to the application block below, which stores the value the
// patch carried, so admitting an owner that is not byte-identical to the
// stored one would replace the stored owner with a padded string; cfg.Owner
// bounds authorizeLayerWrite and the per-identity user-defined layer cap, and
// both compare it exactly.
//
// Spec: §4.6, §7.3.1
func assertedImmutableVisibilityFields(patch LayerRegisterRequest, cfg store.LayerConfig) []string {
	if !cfg.UserDefined {
		return nil
	}
	var asserted []string
	if len(patch.Groups) > 0 {
		asserted = append(asserted, "groups")
	}
	if patch.Organization {
		asserted = append(asserted, "organization")
	}
	if patch.Owner != "" && patch.Owner != cfg.Owner {
		asserted = append(asserted, "owner")
	}
	if patch.Public {
		asserted = append(asserted, "public")
	}
	if len(patch.Users) > 0 && !slices.Equal(patch.Users, cfg.Users) {
		asserted = append(asserted, "users")
	}
	return asserted
}
```

The helper adds `slices` to the file's imports. It does not reuse
`adminOnlyRegistrationFields`'s `strings.TrimSpace` on `owner`: that helper
compares against the caller's subject to decide whether to refuse a
registration, and nothing downstream of it stores the trimmed value, whereas an
`owner` this helper admits is stored verbatim by the block below.

Call it in `update` immediately after the `authorizeLocalSource` gate at `:757` and **above** the `force_push_policy` application and the rotation, so a refused request mutates nothing and mints nothing:

```go
	// spec: §7.3.1 — the immutable visibility rule. A user-defined layer's
	// owner and its implicit users:[owner] visibility are fixed at
	// registration, so an assertion is refused rather than discarded and
	// answered 200. It is evaluated here, above every mutation this handler
	// performs including the rotation below, so a refused patch mints no
	// webhook secret and applies no other field it carries. It runs after
	// the local-source gate so a patch on both arms keeps that envelope.
	if asserted := assertedImmutableVisibilityFields(patch, cfg); len(asserted) > 0 {
		writeErrorDetails(w, http.StatusBadRequest, "registry.invalid_argument",
			fmt.Sprintf("the fields %s cannot be patched on a user-defined layer; its owner and its users:[<owner>] visibility are fixed at registration, so re-send the patch without them, or ask an administrator to re-register the layer as an admin-defined one",
				strings.Join(asserted, ", ")),
			map[string]any{"constraint": "immutable_visibility"})
		return
	}
```

Replace the guard at `:798-818` with the unconditional application block, whose comment cites the refusal above rather than the discard. Its assignments are unchanged, including `cfg.Owner = patch.Owner` and `cfg.Users = patch.Users`, and they are safe on a user-defined layer because the helper admits an `owner` or a `users` only when it is identical to what is stored, so the block rewrites those values with themselves. `writeErrorDetails` already exists and runs the envelope through `enrichEnvelope`, so `retryable` and `suggested_action` populate as they do for every other refusal.

The `update` doc comment's allowed-mutations list gains the qualification that the visibility fields and `owner` apply to an admin-defined layer and are refused on a user-defined one.

### The presence-versus-value problem

`Public` and `Organization` are plain `bool` and `Groups`/`Users` are plain `[]string`, so `{"public": false}` and a body omitting `public` are one value after decode, and a rule keyed on *naming* a field cannot be implemented against the struct.

It does not need to be. `register`'s user-defined arm derives the owner from the authenticated identity, sets `cfg.Users = []string{cfg.Owner}`, and never reads `req.Public`, `req.Organization`, or `req.Groups`; its comment says so ("Discard any caller-supplied public/organization/groups"). A stored user-defined layer is therefore always at the zero value on three axes and at `users: [owner]` on the fourth, so every value that would widen it is non-zero and the value predicate covers the widening axis exactly.

The residue is bounded and inert: `{"public": false}`, `{"groups": []}`, `{"users": []}`, `{"owner": ""}`, an `owner` restating the stored owner, and a `users` restating the stored `[<owner>]` are admitted and change nothing, and none of the zero-value bodies is reachable from the CLI or the web UI. The two restating bodies are reachable, because the layer object carries `owner` and `users` on every layer (§7.3.1's layer object paragraph, `spec/07-external-integration.md:103`: "Every other member is carried on every layer"), so a client that reads a layer object and writes it back sends both. Comparing them against the stored values is what keeps that read-then-write admitted; a predicate keyed on the value alone would refuse the echo of every user-defined layer, which is the class this rule governs.

The alternatives are rejected on cost. Pointer fields would change the register path's semantics in the same edit, because `LayerRegisterRequest` is the decode target for both handlers, and would contradict the §7.3.1 value-assertion sentence that landed with proposal 0017. A second decode into `map[string]json.RawMessage` requires each handler to buffer `r.Body`, so the change lands at two call sites and any third added later silently gets no presence.

### The recourse

`POST /v1/layers` under a stored layer's ID is a write against that layer, authorized on the stored layer's class, so on a stored user-defined layer the admin arm admits a tenant admin. With the admin arm admitting the caller, the class resolves from `req.UserDefined`, which is false for a body that omits it, and the admin-defined branch assigns `owner`, `public`, `organization`, `groups`, and `users` from the request. `PutLayerConfig` upserts on `(tenant_id, id)` and overwrites `user_defined` along with every other column, so the stored layer becomes admin-defined with the declared visibility.

The owner alone cannot take that path: the class resolution routes a non-admin caller to the user-defined arm whatever the body says, and the admin-only registration fields rule refuses a body asserting the visibility. That asymmetry is intended, because widening a layer beyond its registrant is a tenant-level act.

The recourse has side effects an operator must expect, and the documentation names them: a fresh `created_at`, an `order` recomputed at the tail, emptied `last_ingested_ref` and `last_ingested_at`, a newly minted webhook secret on a `git` source that must be registered at the Git host, and a freed slot against the former owner's user-defined layer cap.

## Edge cases and accepted failure modes

| Case | Observable outcome | Where it is stated |
|:--|:--|:--|
| The owner patches `public: true` on their own layer | `400`, `registry.invalid_argument`, `details.constraint: "immutable_visibility"`, message naming `public` | SPEC-1; TEST-2 |
| A tenant admin sends the identical patch | The identical `400` | SPEC-1; TEST-2, TEST-3 |
| A registry with no identity provider, or in public mode, sends it | The identical `400`. The rule reads the stored class | SPEC-1; TEST-2 |
| A patch carrying `public: true` and `ref` | `400`, and `ref` unchanged in the store | SPEC-1; TEST-2 |
| A patch carrying `rotate_webhook_secret: true` and `groups` on a user-defined git layer | `400`, no secret minted, the stored secret unchanged. Today this mints one and answers `200` | CODE-1's placement; TEST-2 |
| A patch carrying `public: false`, `organization: false`, `groups: []`, `users: []`, or `owner: ""` | `200`, unchanged | SPEC-1; TEST-2 |
| A patch carrying `users` or `owner` restating the stored value exactly | `200`, unchanged | SPEC-1; TEST-1, TEST-2 |
| A client returning a layer object it read, verbatim | `200`, unchanged. Its `owner` and `users` restate the stored values | SPEC-1; TEST-2 |
| A patch carrying an `owner` differing from the stored owner only by surrounding whitespace, or a `users` differing only in element order | `400`, and the stored values unchanged | SPEC-1; TEST-1, TEST-2 |
| A non-owner non-admin patching a user-defined layer's visibility | `403 auth.forbidden` with no constraint, from the write rule evaluated first | SPEC-1; TEST-2 |
| A non-admin patching `local_path` and `public` together | `403 auth.forbidden` with `details.constraint: "local_source"` | SPEC-1; TEST-2 |
| The same five fields on an admin-defined layer | `200`, applied, unchanged from today | SPEC-1; TEST-2 |
| A refused patch | No record written, no §8.1 event, no §7.6 publication | SPEC-1; TEST-2 |
| An admitted no-op patch | Still writes and still emits, unchanged from today | Non-goals |
| An admin re-registers a user's layer ID with `public: true` | `201`, the record becomes admin-defined and public | The recourse; TEST-3 |
| The former owner after that conversion | Authorized on neither write arm; the layer is visible to them through its new declaration | §7.3.1 |
| A user-defined layer with an empty stored owner | Any non-empty `owner` differs and is refused. The recourse is re-registration | Watch out for |
| A layer stored before this rule | Untouched. Nothing is migrated; the rule is evaluated at the next update | SPEC-1 |
| `podium layer update --public` against a user-defined layer | Exit 1, with `HTTP 400` and the envelope on stderr | TEST-3 |
| The web UI's update form on a user-defined row | Unreachable by the refusal; the form renders the visibility as text and sends no visibility member | TEST-4 |
| An SDK or meta-tool caller | None reads or patches a layer record, so none is affected | Proposal 0019 |

## Testing

Every case carries `// Spec: §4.6` and `// Spec: §7.3.1`. `registry.invalid_argument` is an existing §6.10 cell with an envelope test, so the rewritten integration case carries `// Matrix: §6.10 (registry.invalid_argument)` and no cell is added.

**TEST-1: the helper (unit, `pkg/registry/server`).** A table over `assertedImmutableVisibilityFields`: each field alone returns that field; all five return the sorted list; `public: false`, `organization: false`, `groups: []`, `users: []`, `owner: ""`, an `owner` equal to the stored owner, and a `users` equal to the stored `users` each return empty; an `owner` differing from the stored owner only by surrounding whitespace returns `["owner"]`, and against a stored `users` of more than one element a patch differing only in element order returns `["users"]`, which pins the comparison as exact; and every field set against an admin-defined layer returns empty whatever the values.

**TEST-2: the endpoint (integration, `pkg/registry/server`).** `TestLayerEndpoint_UpdateCannotWidenUserDefined` (`layer_visibility_test.go:34-57`) is rewritten in place, keeping its harness and inverting its expectation. **This is the regression test that fails against the current code**, which answers `200` with an unchanged record. Subtests: each field refused with the status, code, constraint, and the field named in the message; all five named in sorted order; the stored record byte-identical after a refused patch that also carried `ref`; a rotation-plus-widening patch refused with no `webhook_secret` in the response and the stored secret unchanged; a refused patch emitting nothing to a recording audit sink; the identical patch from a caller the admin arm admits refused identically; the zero-value and restating bodies admitted with `ref` applied, and the layer object read back from the endpoint replayed verbatim as a patch and admitted, which is the round-trip arm the value comparison exists for; the stored `owner`, `public`, `organization`, `groups`, and `users` byte-identical to the pre-patch record after each admitted patch, which is what falsifies an application block that stores an untrimmed `owner`; a patch carrying an `owner` padded with whitespace refused, with the stored owner unchanged; a non-owner non-admin answered `auth.forbidden` with no constraint; a non-admin `local_path`-plus-`public` patch answered `local_source`; and all five fields applied on an admin-defined layer.

**TEST-3: through the binary (e2e, `test/e2e`).** Against a registry with an identity provider configured: the owner's `podium layer update --id <own> --public` exits non-zero with `registry.invalid_argument` and `immutable_visibility` on stderr; a tenant admin's identical invocation is refused identically, which is the arm a `403` reading would get wrong; a third caller is refused with `auth.forbidden`, pinning the precedence through the binary; `podium layer list` shows the record unchanged; and the admin then re-registers the ID with the wanted visibility and a third caller's `podium layer list` returns it as `user_defined: false`. That last arm is what makes the documented recourse exercised rather than asserted. A `--public-mode` registry runs the first arm again and is refused, pinning the one behaviour that separates this rule from its neighbours.

**TEST-4: the client (vitest, `web/ui`).** A case opening the Edit dialog on a `user_defined: true` row, submitting, and asserting the sent body has `public`, `organization`, `groups`, `users`, and `owner` all absent, alongside the existing assertion that the static visibility display renders. The existing cases pin the rendering and the admin-defined send; none pins the absence of the members on the wire, which is the property that makes the refusal unreachable from the UI.

**Mutation checks before the gate.** Restore the `if !cfg.UserDefined` guard and confirm TEST-2 fails on every arm. Change the code to `auth.forbidden` and confirm TEST-2 fails on the code. Add the no-identity-provider carve-out and confirm TEST-2's admin arm and TEST-3's public-mode arm fail. Move the refusal below the rotation and confirm TEST-2's rotation arm fails. Move it above `authorizeLocalSource` and confirm TEST-2's precedence arm fails. Change the `owner` comparison to the caller's subject and confirm TEST-2's restating arm fails. Drop the `users` comparison and confirm TEST-2's round-trip arm fails. Wrap the `owner` comparison in `strings.TrimSpace` and confirm TEST-1's whitespace row and TEST-2's padded-owner arm fail.

## Manual validation

`test/manual-validation.md` gains **S59: a personal layer's owner and visibility are fixed**, after S58, on the `oidc-jwt` stack S44 builds, taking S44's Prerequisites and steps 1 to 4. S59 also applies S44's "Bootstrap admin" amendment, the one S56 and S57 apply, which creates the realm user `carol` and exports `PODIUM_BOOTSTRAP_ADMINS` naming her `sub` before step 3's `podium serve`: the stack as written seeds no tenant-admin grant, and `POST /v1/admin/grants` is itself admin-gated, so without the amendment S59's tenant-admin refusal arm and its re-registration recourse arm cannot be run. The panel step signs in the way S47 does.

S44 scopes both reusable blocks by enumeration, and both enumerations go stale when S59 joins them, so DOC-4 amends S44 in the same edit. The stack note at `test/manual-validation.md:4050-4051` reads "S47 through S50 and S55 through S57 take their prerequisites and steps 1 to 4 from here" and gains S59. The amendment's heading at `:4055` reads "**Bootstrap admin, for S56 and S57 alone.**" and its body sentence at `:4058` reads "S56 and S57 need one, so a run that reaches them amends step 3 with two additions"; both are rewritten to name S56, S57, and S59. Left as written, the heading tells an operator preparing the stack for S59 that the amendment is not theirs, and a registry started with no bootstrap admin cannot run S59's tenant-admin refusal arm or its re-registration recourse arm at all, which is the failure the amendment exists to prevent. The trailing sentence at `:4076-4077` naming the run that skips the block ("A run of S44 through S50 alone skips this block") stays true and is unchanged.

The steps: register a personal layer as a non-admin and read its class, owner, and visibility; attempt `podium layer update --id <id> --public --group acme-eng` and confirm the non-zero exit, the `400`, the constraint, and both field names in sorted order; confirm `podium layer list` reports the record unchanged; confirm the audit log gained no event from the refused attempt, where before this change it gained one reporting a config change that did not happen; `curl` a `PUT` carrying the layer object read in step 1 verbatim and confirm `200` with an unchanged record, which exercises the comparison against the stored `owner` and `users` that the echo depends on and which an operator would otherwise take on faith; confirm `podium layer update --id <id> --ref release` still applies, so the refusal is scoped to the five fields; as a tenant admin, run the step-2 command and confirm the identical refusal; as that admin, re-register the ID with the wanted visibility and confirm a third identity now reads the layer, noting that the re-registration mints a new webhook secret and resets the order, registration time, and ingest history; and open the layer panel on the row and confirm the Edit dialog renders the visibility as text with no editable axes.

## Documentation changes

`docs/reference/http-api.md:476` replaces its statement of the discard with the refusal, its code and constraint, that the refusal names the fields and rejects the whole request, that a field asserts by value, that the rule binds every caller including an admin and every caller on a deployment that authenticates none, and that widening is reached by re-registration. The precedence paragraph at `:478` gains this rule's position after the local-source rule.

`docs/reference/cli.md:430` replaces its clause with the refusal and its code, keeping the register-path refusal in the same sentence pair distinguishable. The `podium layer update` section gains a sentence stating that `--owner`, `--public`, `--organization`, `--group`, and `--user` apply to an admin-defined layer and are refused on a user-defined one.

`docs/deployment/layers.md` gains a paragraph stating that a user-defined layer's owner and visibility are fixed at registration, that those flags are refused whoever runs them, that an administrator re-registers the ID to widen the layer, and what that re-registration resets. It also names the recourse for an operator on a deployment with no identity provider correcting an `--owner` value.

`web/design/README.md:172` is rewritten to state that the registry refuses such a patch, keeping the design consequence, which is unchanged and now has a stronger reason. `web/DESIGN.md:361-364`, the Update bullet, states the same registry behaviour in the same words and is rewritten on the same terms: the registry refuses an owner or a visibility patch on a user-defined layer with `400 registry.invalid_argument` carrying `details.constraint: "immutable_visibility"`, so the form must not offer controls for values it cannot change. The header comment of `web/ui/src/surfaces/UpdateLayerForm.tsx` is rewritten on the same terms, and the stale register-path comment at `web/ui/src/surfaces.test.tsx:9771-9773` is corrected to match the rule proposal 0017 landed.

`CHANGELOG.md` gains a `Fixed` entry naming the endpoint, the five fields, the code and constraint, that the refusal binds a tenant admin and a deployment that authenticates no caller, that the whole request is refused so a rotation in the same body mints nothing, and that a request which received `200` before receives `400` after. It records the change as backward-incompatible under a MINOR bump.

`docs/deployment/access-control.md` already states the constraint correctly and is unchanged. `web/DESIGN.md`'s other statement of this case, the visibility-treatment paragraph at `:338-340` that says the case is displayed rather than edited, stays true and is unchanged.

## Resolved in adversarial review

### Pass 1 (2026-09-04, automated)

- **The staged predicate refused the round-trip the proposal promised was admitted.** `store.LayerConfig` marshals `users` without `omitempty` (`pkg/store/store.go:287`) and §7.3.1's layer object paragraph states that every member other than `force_push_policy` and `last_ingested_at` is carried on every layer (`spec/07-external-integration.md:103`), so a layer object read from a user-defined layer always carries `users: [<owner>]`. A predicate refusing any non-empty `users` therefore refused the verbatim echo on exactly the class the rule governs, contradicting the Summary, SPEC-1, the edge-case table, TEST-2, and S59. The predicate now compares `users` against the layer's stored value, the same reading `owner` already took, and the Summary, SPEC-1, CODE-1, the edge-case table, TEST-1, TEST-2, and the mutation checks state that one predicate. `groups` keeps its value-only check, because the class stores `groups` empty, so any non-empty value differs from the stored one by construction, and CODE-1's comment says so.
- **The helper trimmed `owner` while the application block stored it untrimmed.** After the guard at `pkg/registry/server/layers.go:798-818` becomes unconditional, an admitted `owner` reaches `cfg.Owner = patch.Owner`, and `cfg.Owner` bounds `authorizeLayerWrite` (`:324`) and the per-identity user-defined layer cap (`:1008`), both of which compare it exactly. A whitespace-padded `owner` admitted by a trimming predicate would have locked the owner out of her own layer and stopped the row counting against the cap. The `strings.TrimSpace` is dropped, so an `owner` that is not byte-identical to the stored owner asserts, an admitted value rewrites the stored value with itself, TEST-1's whitespace row expects `["owner"]`, TEST-2 gains an arm asserting the stored fields are byte-identical after an admitted patch, and the mutation checks reintroduce the trim.
- **`web/DESIGN.md` states the discard as current registry behaviour and was declared unchanged.** Its Update bullet at `:361-364` states that the registry ignores the owner and visibility fields and still answers success, the same sentence the sweep stages at `web/design/README.md:172`, and the file is distinct from that one. It is added to the DOC-3 edit list and to checklist step S5, the "Watch out for" enumeration names it, and the unchanged-documents sentence now points at `web/DESIGN.md:338-340`, the visibility-treatment paragraph that stays true.
- **S59 named the wrong stack and one that seeds no tenant admin.** The `oidc-jwt` stack is built by S44, and S47 takes its Prerequisites and steps 1 to 4 from S44 (`test/manual-validation.md:4825`). That stack seeds no tenant-admin grant and sets no `PODIUM_BOOTSTRAP_ADMINS`, and S56 and S57 amend step 3 to create `carol` and export it (`test/manual-validation.md:4055-4061`). S59's tenant-admin refusal arm and its re-registration recourse arm need that amendment, so the manual-validation section now names the S44 stack, requires the same amendment, and references S47 for the sign-in the panel step needs.

### Pass 2 (2026-09-04, automated)

- **S59 falsified two S44 sentences that scope its reusable blocks by enumeration, and no step edited them.** The amendment's heading at `test/manual-validation.md:4055` says it exists "for S56 and S57 alone" and its body at `:4058` says "S56 and S57 need one, so a run that reaches them amends step 3", while the stack note at `:4050-4051` says "S47 through S50 and S55 through S57 take their prerequisites and steps 1 to 4 from here". Adding an S59 that takes the stack and applies the amendment makes all three false, and an operator reading the heading would start a registry with no bootstrap admin and be unable to run S59's tenant-admin refusal arm or its re-registration recourse arm. DOC-4 and checklist step S6 now stage those S44 edits alongside the new scenario, and the manual-validation section names the exact sentences and states that the "A run of S44 through S50 alone skips this block" sentence at `:4076-4077` stays true.

## Open questions

**OQ-1. `400 registry.invalid_argument` or `403 auth.forbidden`?** This proposal fixes `400`, on the reasoning that no caller is authorized to widen a user-defined layer, so the failure is a property of the request rather than of the caller, and a `403` would tell an owner and a tenant admin they lack a right on a layer they otherwise hold every right over. The counter-argument is symmetry: the same five field names on the sibling `register` endpoint refuse with `403 auth.forbidden` carrying `details.constraint: "admin_only_fields"`, so a client branching on the status class handles two for one family of refusals. The resolution changes SPEC-1's sentence and CODE-1's call and nothing in the sequencing or the inventory.

**OQ-2. Is `owner` compared against the layer's stored owner or against the caller's verified subject?** This proposal compares against the stored owner, so a tenant admin who echoes back the owner they read is admitted and a layer read feeds back into a layer write for every caller the write rule admits. Comparing against the caller's subject is more literally parallel to `adminOnlyRegistrationFields` and would refuse that echo.

## Non-goals

- **Suppressing the §8.1 event on an admitted no-op patch.** `emitLayerEvent` fires on every admitted update, so a patch restating what is stored still emits. Deciding whether §8.1 audits the operation or the delta needs a defined equality over `store.LayerConfig`, a §8.1 sentence, and a decision about the rotation arm, none of which follows from §4.6. This proposal removes the case where a *refused* request emitted an event, which is the one that reported something untrue. The residue is recorded for a separate proposal.
- **A presence-aware decode, pointer fields, or any change to `LayerRegisterRequest`.** The value predicate covers the widening axis exactly, for the reason stated above.
- **Letting a tenant admin narrow an admin-defined layer.** The update path grants on each axis and withdraws on none, so `{"public": false}` and `{"groups": []}` are no-ops on the arm that does execute. That is a defect of the same class on a different arm, and closing it needs the presence-aware decode this proposal declines. It is recorded for a separate proposal.
- **A `user_defined` member on the update body**, or any second setter for a layer's class. `register` performs the conversion and a dual path is forbidden.
- **Recording refused layer writes in the audit stream.** No layer write refusal is audited today under any of the three refusal rules; adding it for one would make the stream report one refusal and not its neighbours.
- **Fixing `register`'s reset of order, registration time, ingest history, and webhook secret on a re-registration.** The recourse inherits that behaviour. It predates this defect and affects every re-registration.
- **An ownership transfer operation**, which §4.6 defines no operation for.
- **Any change to `register`, to the layer write rule, to the local-source rule, or to which layers a caller reads.**
- **Any web UI behavioural change.** The client already offers no control that reaches the refusal.
- **A compatibility shim, dual code path, or configuration key restoring the discard.**
