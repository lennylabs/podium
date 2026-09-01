# Proposal 0015: Filter GET /v1/layers to the caller's effective view

- Issue: (to be filed)
- Status: Verified (2026-08-31). Converged after 4 adversarial review rounds
  (6 findings fixed), recorded under "Resolved in adversarial review"; awaiting
  sign-off.
- Date: 2026-08-31

This document stages the proposed spec, code, test, and documentation changes.
It does not modify any spec, code, or doc file. Apply the changes in the staged
sections after sign-off.

## Summary

**What changes.**

- §7.3.1 gains a layer read visibility paragraph beside its existing layer write
  authorization paragraph, stating that a tenant admin reads the tenant's whole
  layer list on both the live and the soft-deleted arm, that any other
  authenticated caller reads the layers §4.6 admits for that caller, that a
  caller the registry does not authenticate reads no layers, that a registry
  which authenticates no caller returns the whole list to every caller, and that
  a `reorder` response reports the same set the list read would.
- `pkg/registry/core` exports the one projection from a stored layer config onto
  the §4.6 visibility record, so the endpoint filter and the composed view read
  the same fields.
- `pkg/registry/server` narrows both arms of `GET /v1/layers` and the reorder
  response body through one helper that reads the `authAdmin` callback the write
  gate already holds and, for a caller that callback refuses, the identity
  resolver and a new group-resolver seam beside it.
- `internal/serverboot` passes the SCIM group expander to the layer endpoint as
  well as to the registry. Nothing else in the boot wiring changes: the layer
  endpoint already holds the admin callback and the identity resolver this rule
  reads.
- The layer panel's empty-state heading is scoped to the caller's view, the doc
  comments that assert an unfiltered whole-block list read are corrected, and the
  committed bundle is regenerated.
- The reference pages, the layers deployment page, the hand-run scenarios in
  `test/manual-validation.md`, the web design documents, and a `CHANGELOG.md`
  entry follow the narrowed read.

**Fixed decisions.**

- The list is filtered rather than refused. A layer outside the caller's view is
  absent from a `200` response, and the change adds no §6.10 error code and no
  matrix cell.
- Both arms of the handler are filtered, and so is the reorder response body.
  Filtering the live arm alone leaves the same disclosure reachable through
  `?deleted=true` or through a reorder any layer owner can issue.
- The read has an admin arm, and it is the arm the endpoint already has. A
  caller `authAdmin` admits reads the tenant's whole list, including every
  user-defined layer belonging to another caller, on both the live and the
  soft-deleted arm. It is the same callback the write gate
  (`pkg/registry/server/layers.go:212-220`) and the erase handler (`:457`) read,
  so one deployment condition decides reads and writes.
- The condition "a registry started with no identity provider configured, or one
  started in public mode" needs no separate branch. The callback `serverboot`
  installs admits unconditionally there
  (`internal/serverboot/serverboot.go:1247-1255`), so such a deployment takes the
  admin arm and the standalone layer panel is unchanged
  (`spec/13-deployment.md:144`; `web/design/README.md:93`). A registry naming a
  free-form `PODIUM_IDENTITY_PROVIDER` label is a different deployment: it
  installs no request-time verifier, its admin callback refuses every caller, and
  its read returns no layers to anyone, which is the posture its write gate
  already takes.
- A caller that resolves no verified subject and is not admitted by the admin arm
  reads no layers rather than the public subset. The layer-management surface is
  not an anonymous surface, and withholding a public layer's `repo`,
  `local_path`, and webhook posture from an unauthenticated caller costs this
  rule nothing.
- The filter needs no separate owner test. A user-defined layer is stored with
  its registrant in `users:`, so `layer.VisibleWith` already admits the owner.
- A credential that is presented and fails verification resolves to no verified
  subject, so on a registry that configures an identity provider it takes the
  unauthenticated arm and reads no layers. This needs no change to
  `layerIdentityResolver`: `AdminAuthorize` refuses the public-mode identity that
  resolver returns (`pkg/registry/core/admin.go:22`), and the helper's
  authentication guard runs before `layer.VisibleWith`, whose `IsPublic`
  short-circuit (`pkg/layer/composer.go:64-66`) is therefore never reached on
  that arm.
- The narrowed read answers an empty `200`, and no other spec section is
  rescoped. §6.3.2, §6.3.3, §6.9, and §13.12 keep their unqualified
  credential-rejection statements, so the applied spec carries both an
  unqualified refusal and an empty `200` for the same credential on this route.
  The overlap is accepted because the divergence is pre-existing in code and the
  narrowed body discloses nothing, and open decision 2 carries the alternative of
  restoring the scoping clause.
- The change adds one predicate and one projection. The filter calls
  `layer.VisibleWith` on a `layer.Layer` built from the exported projection in
  `pkg/registry/core`, and copies neither.
- Podium is pre-1.0, so no flag, configuration key, or query parameter restores
  the unfiltered listing, and no dual code path is added.

**Watch out for.**

- **The `authAdmin` default admits, which is why almost no existing test reaches
  a narrowed read.** `NewLayerEndpoint` installs
  `func(*http.Request) error { return nil }`
  (`pkg/registry/server/layers.go:180`), so every harness that does not call
  `WithAdminAuth` takes the admin arm and reads the whole list exactly as today.
  Several tests do install a refusing or conditionally refusing callback
  (`pkg/registry/server/layer_visibility_test.go:116-117`,
  `pkg/registry/server/coverage_gaps_test.go:510-511`,
  `pkg/registry/server/layer_register_class_test.go:25`,
  `pkg/registry/server/erase_test.go:40`,
  `pkg/registry/server/layers_test.go:306`,
  `pkg/registry/server/layer_write_auth_test.go:77-81`, and
  `test/integration/layer_write_authorization_test.go:34`). All but one drive a
  write path and issue no narrowed read. The exception is
  `TestLayerWriteAuth_UserDefinedOwnerOrAdmin`'s `reorder` op
  (`pkg/registry/server/layer_write_auth_test.go:138-139`), which posts
  `POST /v1/layers/reorder` under the refusing callback and so reaches the
  response body `readableBy` narrows. It stays green for a stated reason: the
  case asserts the status code and the error code alone (`:179-184`), and its
  owner arm seeds `Users: ["alice"]` on the only layer (`:171`) with the resolver
  reporting alice, so `readableBy` returns that layer.
- **The browser-stack fixture installs no admin callback.** `newBrowserStack`
  builds the endpoint with `WithIdentityResolver` alone
  (`internal/serverboot/webui_auth_integration_test.go:216-218`), so on that
  fixture every caller takes the admin arm and reads the whole list. A
  per-identity case there installs a `WithAdminAuth` mirroring the boot closure
  on an identity-provider-configured registry, or it asserts nothing.
- **The authentication guard must precede the predicate.** `layer.VisibleWith`
  returns true for `layer.Identity{IsPublic: true}`
  (`pkg/layer/composer.go:64-66`), which is what `layerIdentityResolver` returns
  for a credential that fails verification
  (`internal/serverboot/identity_verify.go:56-65`). Evaluating the predicate
  first would hand a forged credential the whole tenant.
- **The panel lead is left verbatim, and the filter is why it is accurate for the
  caller it was wrong for.** `web/ui/src/surfaces/LayerPanel.tsx:145-150` says
  the panel lists the sources the catalog is composed from. For an authenticated
  non-admin on a registry that authenticates callers, CODE-2 makes the panel's
  row set the set that caller's catalog is composed from. The admin arm splits.
  For a §4.7.2 tenant admin on a registry that authenticates callers, the panel
  deliberately holds the tenant's whole list while the catalog stays composed
  under §4.6, so the two sets differ there. On a registry that authenticates no
  caller they coincide for a different reason: the catalog read paths
  short-circuit on `id.IsPublic` and return every layer's manifests without
  evaluating the §4.6 predicate (`pkg/registry/server/server.go:283-286`,
  `pkg/registry/core/core.go:1997-1998`,
  `pkg/registry/core/domain_load.go:157-158`). The lead is left alone on every
  arm, and with it the two component assertions that pin it
  (`web/ui/src/surfaces.test.tsx:8378`, `:17780`).
- **`surfaces.test.tsx:13756` is a different component.** It pins
  `web/ui/src/surfaces/DeletedLayers.tsx`'s "No layers to restore" heading, and
  it is outside this change.
- **`newBrowserStack` writes no layer config rows.** Its only store writes are
  `CreateTenant` (`internal/serverboot/webui_auth_integration_test.go:191`) and
  `PutManifest` (`:198`). The `pub` and `eng` values at `:194-197` are a
  `[]layer.Layer` slice handed to `core.New` at `:214` as the composed catalog's
  layer list, and `GET /v1/layers` answers from `store.ListLayerConfigs`
  (`pkg/registry/server/layers.go:873`), so on that stack the layer read returns
  an empty list today. Any per-identity disclosure assertion there has to seed
  its own `store.LayerConfig` rows, which TEST-4 stages.
- **`test/integration/runtime_layer_visibility_test.go` cannot reach the boot
  wiring.** It imports no `internal/serverboot` and injects static identities, so
  a case placed there proves nothing about `layerIdentityResolver` or the mount.
- **`internal/serverboot`'s browser stack installs no group resolver.** CODE-4's
  wiring is reachable only from a test that runs the binary, which is
  `test/e2e/auth_scim_visibility_test.go`.
- **The CI rebuild gate makes the bundle non-optional.**
  `.github/workflows/test.yml` runs `npm run build` and then
  `git diff --exit-code`, so CODE-5's source edit ships with the regenerated
  `web/bundle` in the same commit.

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. §7.3.1 gains the layer read visibility paragraph
      beside the write authorization paragraph. The file is
      `spec/07-external-integration.md`.
      Levels: —. Depends on: —
- [ ] **S2 · code** — CODE-1. `pkg/registry/core` exports the projection from a
      stored layer config onto the §4.6 visibility record.
      Levels: unit. Depends on: S1
- [ ] **S3 · code** — CODE-2, TEST-1. The endpoint filters both list arms and the
      reorder response body, with the per-identity endpoint cases that pin it.
      Levels: unit. Depends on: S2
- [ ] **S4 · test** — TEST-3. The browser-flow doc comment that asserts the layer
      read answers unfiltered is corrected.
      Levels: integration. Depends on: S3
- [ ] **S5 · code** — CODE-4. The SCIM group expander is passed to the layer
      endpoint as well as to the registry.
      Levels: e2e. The wiring lives in the builder chain and the browser stack
      installs no group resolver, so the level it reaches is the end-to-end arm
      TEST-4 stages in S6.
      Depends on: S3, which stages the `WithGroupResolver` option this wiring
      calls.
- [ ] **S6 · test** — TEST-4. The `layerConfigs` seam on the browser stack's
      fixture, the per-identity narrowing through the wired browser stack, and
      the SCIM-resolved membership read through the binary.
      Levels: integration, e2e. Depends on: S5
- [ ] **S7 · code** — CODE-5, TEST-5. The panel's empty-state heading, the stale
      panel doc comments including the file header, the regenerated bundle, the
      component assertion that pins the heading, and the `bobLayer` fixture
      comment. The code lane resumes here after S6 because the heading it stages
      describes the behavior S3 lands, and it precedes the documentation steps
      that describe the same surface.
      Levels: unit. Depends on: S3
- [ ] **S8 · docs** — DOC-1. The read rule on the HTTP API reference, the CLI
      reference, and the layers deployment page.
      Levels: —. Depends on: S3
- [ ] **S9 · docs** — DOC-2. The three hand-run scenarios that read the list
      anonymously or assert it is unfiltered.
      Levels: —. Depends on: S3, S7
- [ ] **S10 · docs** — DOC-3. The `CHANGELOG.md` entry.
      Levels: —. Depends on: S3
- [ ] **S11 · docs** — DOC-4. The design documents' unfiltered-read statements,
      in `web/DESIGN.md` and `web/design/README.md`.
      Levels: —. Depends on: S3

## Current state and the gap

`GET /v1/layers` runs no authorization and evaluates no visibility predicate, so
any caller receives every layer configured in the tenant. The `GET` arm
dispatches straight to the list handler (`pkg/registry/server/layers.go:409-410`),
which calls `ListLayerConfigs(ctx, e.tenantID)` and marshals the result wholesale
(`:863-879`). The function never calls `e.identify`, never evaluates a §4.6
predicate, and applies no owner test, even though the endpoint holds the resolver
its write handlers use (`:51-53`, `:212-220`) and the sibling restore handler
gates the same `ListDeletedLayerConfigs` result behind owner-or-admin
authorization (`:900-938`). The `?deleted=true` arm is the same body
(`:864-871`), so the tenant's soft-deleted layers are enumerable on identical
terms.

The disclosure is the serialized store record. `LocalPath`
(`pkg/store/store.go:265`), `Owner` (`:268`), and `Groups` and `Users`
(`:272-273`) are untagged and emitted, so a caller learns that a group-gated
layer exists, where its source lives on the registry host, and which OIDC subject
holds a personal layer. `WebhookSecret` alone is suppressed with `json:"-"`
(`:284`). The endpoint is mounted on the boot mux outside the meta-tool identity
middleware (`internal/serverboot/serverboot.go:1259-1260`), and the only wrapper,
`BrowserOriginGate`, exempts `GET` by method
(`pkg/registry/server/browser_origin_gate.go:30`, `:46-52`), so an anonymous
caller reaches it.

The same disclosure is reachable on a write path. `POST /v1/layers/reorder`
authorizes only the layers the request names and then re-lists the tenant,
returning every stored config (`pkg/registry/server/layers.go:1020-1028`). Since
the write gate admits a user-defined layer's own owner (`:216`), any
authenticated caller who owns one personal layer can reorder that layer and read
the whole tenant's layer list back.

The resolver the endpoint holds does not distinguish a forged credential from a
public-mode caller. `layerIdentityResolver` discards the verifier's error and
returns `layer.Identity{IsPublic: true}`
(`internal/serverboot/identity_verify.go:56-65`), which is the §13.10 public-mode
bypass rather than an anonymous caller, and `layer.VisibleWith` short-circuits to
visible for it (`pkg/layer/composer.go:64-66`). A filter that evaluated the §4.6
predicate on that identity would still hand the whole list to a caller whose
credential is expired or forged, which is why CODE-2 resolves the admin arm and
the authentication guard before it evaluates the predicate. Under `oidc-jwt` and
`trusted-headers` an anonymous caller resolves to the zero identity instead
(`internal/serverboot/identity_verify.go:223-233`, `:257-275`). Today every
caller class receives the same list because the handler consults no identity at
all, rather than because the resolver collapses them.

The spec is silent on this endpoint and explicit on the principle. §7.3.1 states
layer write authorization in detail and says nothing about the read. §4.6 states
that each user-defined layer is visible only to the user who registered it, that
read-side enforcement happens at the registry on every call
(`spec/04-artifact-model.md:613`), and that the composition order takes the
user-defined layers belonging to the caller.
§4.5.5's Unknown-paths rule states that an unlisted path returns the same
`domain.not_found` error as a typo, so an unlisted folder is not detectable
through enumeration probing (`spec/04-artifact-model.md:562`), and §7.5.1 orders
visibility before scope filtering so
include patterns cannot leak the existence of artifacts in invisible layers.
That enforcement sentence is unqualified, and the §4.6 sentence that enumerates
`load_domain`, `search_domains`, `search_artifacts`, and `load_artifact`
governs layer resolution rather than enforcement
(`spec/04-artifact-model.md:589`). What §4.6 does not do is assign a disposition
to the layer-management endpoints, which §7.3.1 owns, and both places that name
`GET /v1/layers` state no gate (`spec/07-external-integration.md:154`,
`spec/13-deployment.md:519`). That is the gap SPEC-1 fills.

No test in the repository issues `GET /v1/layers` under two identities and
compares the results, so nothing fails while the endpoint returns everything, and
one hand-run scenario documents the leak as expected
(`test/manual-validation.md:4956`).

## Decisions

**The list is filtered, and no error code is added.** A layer outside the
caller's effective view is absent from a `200` response. That makes the read
symmetric with the catalog endpoints and with §4.5.5's rule that an unlisted
path is not detectable through enumeration probing. The
handler's existing `registry.unavailable` arms are unchanged, and `matrix-audit`
gains no cell.

**Both arms of the handler are filtered, and so is the reorder response.** The
`?deleted=true` arm shares the handler body and returns the same fields, and the
reorder handler re-lists the tenant after authorizing only the layers the request
names. A filter on the live list alone leaves the identical disclosure reachable
through a query parameter or through a single reorder.

**The read has an admin arm, and it is the callback the endpoint already holds.**
`e.authAdmin` decides every layer write
(`pkg/registry/server/layers.go:212-220`, `:457`), and the read reads the same
callback, so one deployment condition decides both and a caller who can write a
layer can read it. A caller it admits reads the tenant's whole list on both arms.
§4.6's evaluator carries no admin arm (`pkg/layer/composer.go:64-97`) and the
§4.7.2 diagnostic visibility override is a separate opt-in, audited path
(`pkg/registry/core/core.go:1171-1176`, `:2014-2032`); neither is applied here,
and neither is needed, because the arm is resolved before the predicate rather
than inside it. The accepted consequence is that a tenant admin reads every other
caller's user-defined layer, including its owner subject and its local path,
which is the disclosure the admin already holds a write grant over.

**The filter needs no separate owner test.** A user-defined layer is stored with
`Users: []string{cfg.Owner}` at registration
(`pkg/registry/server/layers.go:758`, `:737-739`), so `layer.VisibleWith` already
admits the registrant and a second ownership branch would be a duplicate
predicate.

**The registry that authenticates no caller needs no branch of its own.** A
registry started with no identity provider configured, or one started in public
mode (§13.10), authenticates no caller, which is the condition §7.3.1's write
paragraph already states in those words (`spec/07-external-integration.md:97`)
and which the installed admin callback already reads as
`cfg.publicMode || cfg.identityProvider == ""`
(`internal/serverboot/serverboot.go:1247-1255`). Every caller passes the admin
arm there and reads the whole list, so the read and the write agree with no
second expression to keep in step. A free-form `PODIUM_IDENTITY_PROVIDER` label
is a different deployment: `selectIdentityProvider` returns nil for a value the
`identity.Default` registry does not carry
(`internal/serverboot/identity_verify.go:157-159`) and
`identityVisibilityGuard` lets it start (`:95-97`), so its write gate is live,
its admin callback refuses every caller, and its read returns no layers to
anyone. Public mode and an identity provider are mutually exclusive at startup
(`spec/13-deployment.md:506`; `pkg/registry/server/config_validate.go:17-18`), so
the arms never overlap.

**A failed verification reads no layers, and no resolver changes.** A credential
that fails signature, `iss`, `aud`, configured-subject-claim, or expiry
validation (`pkg/identity/oidc_jwt.go:251-253`), and a runtime-signed token
carrying no registered signing key on a registry started under
`injected-session-token` (`internal/serverboot/serverboot.go:1147`), resolves
through `layerIdentityResolver` onto `layer.Identity{IsPublic: true}`.
`AdminAuthorize` refuses that identity (`pkg/registry/core/admin.go:22`), so such
a caller fails the admin arm, and the authentication guard stops it before
`layer.VisibleWith` is reached. The read answers an empty `200`, which discloses
nothing. §6.3.2's and §6.3.3's refusals are unqualified statements written for
the registry process, and the layer-management endpoints diverge from them today:
they are registered on the mux directly
(`internal/serverboot/serverboot.go:1259-1260`), ahead of the meta-tool handler
that verifies the credential (`:1327`), and a request carrying an expired session
already answers `200` on this route
(`internal/serverboot/webui_auth_integration_test.go:650-655`). This change
narrows that body to empty. It leaves §6.3.2, §6.3.3, §6.9, and §13.12 unscoped,
so the spec states an unqualified refusal for a credential §7.3.1 now answers
with an empty `200`. That overlap is accepted rather than resolved, and open
decision 2 carries the alternative of restoring the scoping clause.

**The filter reuses the existing predicate and the existing group expander.** No
second predicate is written, and the projection from the stored layer config onto
the visibility record is exported from its one existing home in
`pkg/registry/core` rather than copied.

**Podium is pre-1.0.** No flag, configuration key, or query parameter restores
the unfiltered listing, and no dual code path is added.

## Spec amendment: §7.3.1 layer read visibility

**SPEC-1.** Anchor: `spec/07-external-integration.md`, §7.3.1. The new paragraph
lands immediately after the paragraph beginning `**Layer write authorization.**`
(`spec/07-external-integration.md:97`) and immediately before the paragraph
beginning `**Errors.**` (`:99`). Nothing in either neighbouring paragraph changes,
and the command list, the user-defined-layer paragraph, and the ingestion-trigger
material above are untouched.

The inserted paragraph:

> **Layer read visibility.** The layer read operation `list` returns, on both
> its live and its soft-deleted arm, the tenant's whole layer list to a caller
> holding the §4.7.2 admin role, and to every caller on a registry started with
> no identity provider configured or one started in public mode (§13.10), which
> authenticates no caller. To any other authenticated caller it returns the
> layers that caller can see under §4.6, which includes that caller's own
> user-defined layers through their implicit `users: [<registrant>]` visibility.
> To a caller that resolves no verified subject it returns no layers, whether
> that caller presented no credential or presented one that failed verification;
> the layer read raises no authentication error of its own, so the response is an
> empty `200` rather than a refusal. A `reorder` response reports the same set
> the list read would report for that caller. A layer the rule withholds is
> absent from the response rather than refused, so the read discloses no layer
> identifier, source location, owner subject, or visibility declaration for it,
> on the same footing as §4.5.5's rule that a path reachable only under
> `unlisted: true` is not detectable through enumeration probing. A narrowed
> listing is reported through no §6.10 error code.

The rule reads the same authorization the layer write authorization paragraph
above reads, so one deployment condition decides both, and a caller who can write
a layer can read it.

Deliberately absent: a scoping clause on §6.3.2's, §6.3.3's, §6.9's, or
§13.12's credential-rejection statements. Those statements carry no route
qualifier today. §6.3.3 is titled "Server-Side Request Authentication"
(`spec/06-mcp-server.md:90`) and states its rejections for the registry process
rather than for one route, §6.3.2 states `auth.untrusted_runtime` for an
unregistered signing key (`:76`), §6.9 restates both as failure-mode rows
(`:389`, `:391`), and §13.12's `PODIUM_OAUTH_SUBJECT_CLAIM` row restates the
subject-claim refusal (`spec/13-deployment.md:498`). The endpoint has diverged
from them since it was built: the layer-management handlers are registered on the
mux directly (`internal/serverboot/serverboot.go:1259-1260`) and take precedence
over the meta-tool handler at `/` (`:1327`), which is the surface that verifies
the request credential, and a request carrying an expired session already answers
`200` on this route
(`internal/serverboot/webui_auth_integration_test.go:650-655`). The paragraph
above states the disposition that endpoint takes, so from here the spec carries
both an unqualified refusal in §6.3.2 and §6.3.3 and an empty `200` in §7.3.1 for
the same credential on the same request. This proposal accepts that as a known,
narrow overlap rather than resolving it, on the ground that the divergence is
pre-existing and that the narrowed body discloses nothing. Rescoping those
statements is a separate editorial change, and open decision 2 carries it as an
option. The implicit `users: [<registrant>]` visibility of a user-defined layer
is stated two paragraphs above at `spec/07-external-integration.md:95`. The
filesystem-registry bypass is not named, because a filesystem-source registry
serves no HTTP endpoint (`spec/13-deployment.md:331-336`).

A reviewer is most likely to reopen the §6.3.2 and §6.3.3 question here, and it
is the one place where dropping SPEC-2 costs something rather than nothing:
SPEC-1 states a disposition for a request those sections also state a disposition
for. Passes 4 and 5 added SPEC-2's scoping clause for exactly that reason. Open
decision 2 puts the choice to the maintainer rather than deleting it silently.

## Proposed solution

### CODE-1: one projection onto the visibility record

`pkg/registry/core/core.go`, splitting the field copy out of `layerFromConfig`
(`:422-433`) and exporting it:

```go
// VisibilityOf projects a stored layer config onto the §4.6 visibility
// record carried by the layer.Layer the evaluator consumes. It is the one
// projection: layerFromConfig builds the composed view's layers on it, and
// the §7.3.1 layer-list read builds the layer.Layer it filters with on it.
//
// Spec: §4.6
func VisibilityOf(c store.LayerConfig) layer.Visibility {
	return layer.Visibility{
		Public:       c.Public,
		Organization: c.Organization,
		Groups:       c.Groups,
		Users:        c.Users,
	}
}

func layerFromConfig(c store.LayerConfig, precedence int) layer.Layer {
	return layer.Layer{ID: c.ID, Precedence: precedence, Visibility: VisibilityOf(c)}
}
```

No other `pkg/registry/core` call site changes. `layerFromConfig` keeps its
signature and its callers at `core.go:409` and `:414`. The dependency direction
holds: `pkg/registry/server` already imports `pkg/registry/core` for the
`ErrForbidden` mapping in `writeCoreError`, and `core` does not import `server`.

### CODE-2: filter both list arms and the reorder response

`pkg/registry/server/layers.go`. The endpoint gains a group resolver field beside
the identity resolver it already holds (`:48-53`), with a `WithGroupResolver`
option beside the existing ones (`:186-196`), and one helper both read paths
call:

```go
// readableBy narrows a stored layer list to what the caller may read under
// the §7.3.1 layer read rule. It is the read counterpart of
// authorizeLayerWrite and takes its admin arm from the same authAdmin
// callback, so one deployment condition decides both reads and writes.
//
// A caller the callback admits reads the tenant's whole list. That is the
// §4.7.2 tenant admin, and it is also every caller on a registry started
// with no identity provider configured or in public mode, because the
// callback serverboot installs admits unconditionally there
// (internal/serverboot/serverboot.go:1247-1255). That second case is what
// keeps the standalone layer panel populated for a caller with no subject.
//
// Any other caller must resolve a verified subject. The guard runs before
// layer.VisibleWith and is load-bearing rather than defensive: the boot
// resolver maps a credential that fails verification onto
// layer.Identity{IsPublic: true} (internal/serverboot/identity_verify.go:56-65)
// and VisibleWith short-circuits to visible for that identity
// (pkg/layer/composer.go:64-66), so evaluating the predicate first would
// hand a forged credential the whole tenant.
//
// An authAdmin error of any kind, including the store failure
// core.AdminAuthorize reports as ErrUnavailable, takes the non-admin path.
// The read narrows rather than failing, which discloses less rather than
// more, and the kinds are not discriminated.
//
// The result is always non-nil so an empty read marshals as [] rather than
// null.
//
// Spec: §4.6, §7.3.1
func (e *LayerEndpoint) readableBy(r *http.Request, configs []store.LayerConfig) []store.LayerConfig {
	out := make([]store.LayerConfig, 0, len(configs))
	if e.authAdmin(r) == nil {
		return append(out, configs...)
	}
	caller := e.identify(r)
	if !caller.IsAuthenticated || caller.Sub == "" {
		return out
	}
	for _, c := range configs {
		// VisibleWith takes a layer.Layer and reads its Visibility field.
		// Precedence is unused on this read, so it stays at its zero value.
		l := layer.Layer{ID: c.ID, Visibility: core.VisibilityOf(c)}
		if layer.VisibleWith(l, caller, e.resolveGroup) {
			out = append(out, c)
		}
	}
	return out
}
```

`layer.VisibleWith`'s first parameter is a `layer.Layer` and its body reads
`Visibility` off that struct (`pkg/layer/composer.go:64`, `:68`), which is why the
helper wraps the projection rather than passing it as the first argument.
Exporting a `layer.Layer` builder from `pkg/registry/core` instead was considered
and rejected: precedence is a property of the composed view rather than of a
stored row, so such a builder would either take a precedence argument this read
has no value for or hand `layerFromConfig` a `Precedence` it has to overwrite.

`list` (`:860-879`) fetches once, on the arm the query parameter selects, and
returns `e.readableBy(r, configs)`. Both arms pass through the same helper, so the
`?deleted=true` path carries the same filter as the live path.

`reorder` (`:1020-1028`) narrows its response body through the same helper,
sorting before filtering so the order the caller reads is unchanged for the rows
it can see. Its handler doc comment states that the response reports the same set
the list read would report for that caller. Without this the fix
is bypassable: the write gate admits a user-defined layer's owner, so any caller
who owns one personal layer reorders that layer and reads the tenant's whole
list back.

No owner branch is added. A user-defined layer stores its registrant in `Users`
at registration (`:758`), so the §4.6 predicate already admits the owner.

### CODE-3 was dropped

An earlier revision changed `layerIdentityResolver` so a failed verification
produced the zero identity rather than `layer.Identity{IsPublic: true}`. The
three-armed read makes it unnecessary. On a registry that configures an identity
provider, `AdminAuthorize` refuses the public-mode identity outright
(`pkg/registry/core/admin.go:22`), including on the free-form-label deployment
where `layerVerify` is nil (`internal/serverboot/identity_verify.go:157-159`), so
such a caller falls to the non-admin path and is stopped by `readableBy`'s
authentication guard before the predicate runs. `internal/serverboot` is
unchanged apart from CODE-4, and `TestLayerIdentityResolver`
(`internal/serverboot/identity_verify_test.go:212-244`) keeps its assertions
verbatim.

### CODE-4: wire the SCIM group expander into the layer endpoint

`internal/serverboot/serverboot.go`. The SCIM block (`:955-978`) already builds a
group resolver over `scimStore.MembersOf` and passes it to the registry. Hold it
in a local and pass it to both consumers:

```go
var resolveGroup layer.GroupResolver
if scimHandler != nil {
	resolveGroup = func(g string) []string {
		members, err := scimStore.MembersOf(context.Background(), g)
		if err != nil {
			return nil
		}
		return members
	}
	registry = registry.WithGroupResolver(resolveGroup)
}
```

and in the layer endpoint's builder chain (`:1238-1256`):

```go
	WithIdentityResolver(layerIdentity).
	WithGroupResolver(resolveGroup).
```

A nil `resolveGroup` is the JWT-only path, which is what the nil contract in
`pkg/layer` already means (`pkg/layer/composer.go:45-49`). Without this a caller
whose group membership arrives only through SCIM reads that group's artifacts and
does not see the layer that carries them, which is a divergence between two reads
of the same §4.6 predicate.

### CODE-5: scope the layer panel's empty-state heading and its stale comments

`web/ui/src/surfaces/LayerPanel.tsx` takes these string edits:

- The empty state's heading at `:595` becomes
  `<EmptyState title="No layers to show">`. The body sentence, "Register a layer
  to bring its artifacts into the catalog.", stays verbatim: §7.3.1 authorizes a
  user-defined `register` to any caller who resolves a verified subject, so it is
  still the action a caller seeing zero rows can take. The heading matches the
  sibling surface's existing convention and stops the panel from telling a caller
  who can see none of several registered layers that the tenant has none. A body
  naming what the caller cannot see is deliberately not staged, because it would
  volunteer that the tenant holds hidden layers and cut against the
  non-enumerability posture §4.5.5 states and this change rests on.
- One clause is falsified and one word is imprecise, under a single rule:
  `block` means the run within the rows the panel holds (`blockOf`, `:837-849`),
  and after CODE-2 those rows are the caller's §7.3.1 read scope.
  - `movedOrder`'s "and the list read is unfiltered" (`:866`) is falsified and is
    the reason for a consequence that still holds. §4.6 read visibility and the
    §7.3.1 write rule are independent, so a caller can still see a layer it may
    not write. The clause becomes "which is independent of the §7.3.1 read
    visibility the list is scoped by", leaving the sentence's conclusion, that a
    block holding such a layer has its move refused whole, verbatim.
  - "The request names the whole block" (`:862-863`) and "each request names the
    whole block's resulting order" (`:214`) each take the word `visible` before
    `block`, so the two comments read the same way. Nothing else in either
    comment changes: "leaves every other row of the block holding the value it
    already had, which ties or inverts rows the move was not meant to touch"
    (`:859-861`) is already accurate, and the held-press dedupe argument is
    unaffected because the panel already steps every press off the order it is
    displaying.
  - The file-header comment at `:1-5` states the same falsified clause as the
    reason for the panel's whole rendering posture: "The list read hands the
    panel every layer stored under the tenant and no response reports that the
    caller holds the administrator role, so the panel predicts no outcome." Its
    first clause becomes the §7.3.1 read rule: the list read reports what the
    caller may read, which is the tenant's whole list for a §4.7.2 tenant admin
    and for every caller on a registry that authenticates none, the layers §4.6
    admits for any other caller who resolves a verified subject, and no layers
    for a caller who resolves none. The role clause and the conclusion that
    follows it stay verbatim, because no response reports the role and the panel
    still predicts no outcome, and the standalone-deployment paragraph at `:7-10`
    is unchanged.

  No test is owed. These are comments, the endpoint's assignment is unchanged,
  and `TestLayerEndpoint_Reorder` (`pkg/registry/server/layers_test.go:268-297`)
  owns that assignment and gains no case.

The panel's lead at `:145-150` is left verbatim. It says the panel lists the
sources the catalog is composed from and that the order below decides which
artifact an overlay merges onto. For an authenticated non-admin the filter makes
that accurate: the read paths that compose a caller's catalog already apply the
§4.6 predicate over the same resolved layer list
(`pkg/registry/core/core.go:1985-2000`,
`pkg/registry/core/domain_load.go:156-164`), so after CODE-2 that caller's panel
row set equals the set their catalog is composed from, where today, unfiltered,
the panel lists layers that compose into no catalog the reader can read. For a
§4.7.2 tenant admin on a registry that authenticates callers the two sets differ,
because the panel holds the tenant's whole list while the catalog stays composed
under §4.6. On a registry that authenticates no caller they coincide: the
meta-tool server's default resolver returns `layer.Identity{IsPublic: true}`
(`pkg/registry/server/server.go:283-286`) and both catalog read paths
short-circuit on `id.IsPublic` before the §4.6 predicate runs
(`pkg/registry/core/core.go:1997-1998`,
`pkg/registry/core/domain_load.go:157-158`), so that caller's catalog is composed
from the whole list the panel holds. The lead is left alone on every arm: on the
admin arm it is the deliberate posture of the admin panel, and no response
reports the role, so the panel predicts nothing either way.

No further panel edit is staged. `listOrdered` (`:794-806`) partitions
and sorts whatever the read returns, and the "yours" marker and the personal
layer count are computed per row against the subject the posture read reports
(`:753`, `:779-786`).

`web/bundle` is regenerated in the same commit, because a `web/ui/src` string
changed and `.github/workflows/test.yml` runs `npm run build` followed by
`git diff --exit-code`.

## Edge cases and accepted failure modes

| Case | Observable outcome | Where it is stated |
|:--|:--|:--|
| A tenant admin reads either arm | `200` carrying the tenant's whole layer list, including every other caller's user-defined layers. Unchanged from today | §7.3.1's staged paragraph; `docs/reference/http-api.md` `### List layers` |
| An authenticated non-admin reads either arm | `200` carrying the layers §4.6 admits for that caller, including that caller's own user-defined layers, with no field of a withheld layer present and no indication that rows were withheld | §7.3.1's staged paragraph; `docs/reference/http-api.md` `### List layers` |
| A caller resolving no verified subject on a registry that configures an identity provider, whether the caller presented no credential or one that failed signature, `iss`, `aud`, subject-claim, expiry, or runtime-key validation | `200` carrying `"layers": []`, and the panel's "No layers to show" empty state. No §6.10 code reports it | §7.3.1's staged paragraph; `docs/reference/http-api.md` `### List layers`; `CHANGELOG.md` |
| A caller's view admits no layer at all | `200` carrying an empty array rather than `null`, and the panel's "No layers to show" empty state | §7.3.1's staged paragraph; `docs/reference/http-api.md` `### List layers` |
| `GET /v1/layers?deleted=true` | The same rule as the live arm, on every caller class | §7.3.1's staged paragraph, "on both its live and its soft-deleted arm"; `docs/reference/http-api.md` `### List soft-deleted layers and restore` |
| `POST /v1/layers/reorder` issued by a layer owner | `200` whose body reports the same set the list read would report for that caller, which is the tenant's whole list where that caller also holds the §4.7.2 admin role | §7.3.1's staged paragraph, "A `reorder` response reports the same set the list read would report for that caller" |
| The admin check itself fails, because `IsAdmin` returns a store error | The caller takes the non-admin path and reads their §4.6 view, or no layers if they resolve no subject. The read narrows rather than answering `registry.unavailable` | `readableBy`'s doc comment; TEST-1's `admin_check_error` arm |
| A registry started with no identity provider, which includes public mode | Unchanged: the whole tenant layer list | §7.3.1's staged paragraph, "authenticates no caller"; the `authAdmin` closure at `internal/serverboot/serverboot.go:1247-1255`; `docs/reference/http-api.md` `### List layers` |
| A registry naming a free-form `PODIUM_IDENTITY_PROVIDER` label, which installs no request-time verifier and is not public mode | Every caller is refused by the admin callback and authenticates as no one, so every caller reads no layers, which is the posture its write gate already takes | `readableBy`'s admin arm; the `authAdmin` closure at `internal/serverboot/serverboot.go:1247-1255`; TEST-1's arms |
| A filesystem-source registry | Not reachable. There is no server process and no HTTP endpoint in that mode | `spec/13-deployment.md` §13.11; the endpoint is not documented for that mode on any page |
| A non-admin reads the `?deleted=true` arm | The same rule. A tombstoned admin-defined layer their §4.6 view admits is listed even though restoring it requires the admin role, which matches the live arm listing rows they cannot write | §7.3.1's staged paragraph; `docs/reference/http-api.md` `### List soft-deleted layers and restore` |
| A reorder naming a proper subset of a class run | Unchanged, and not this proposal's to change: the endpoint restamps the named layers alone and leaves every unnamed layer at its stored value (`pkg/registry/server/layers.go:1012-1015`), which composition reads over the tenant's whole stored set before visibility is applied (`pkg/registry/core/core.go:394-416`). A CLI or API caller reaches this today by naming a subset (`cmd/podium/layer.go:289-305`), with no read filter involved. The panel adds no new path to it: an admin reads and names the whole run, and a non-admin can name only their own user-defined layers, whose visibility is fixed to the registrant (`spec/04-artifact-model.md:580`) | Non-goals, "Changing how `POST /v1/layers/reorder` assigns absolute order values"; CODE-5's corrected `movedOrder` comment |
| A caller registers a layer whose visibility excludes them | Unreachable under the admin arm, so the reloaded panel always holds the new row. A registration whose visibility can exclude the registrant is admin-defined, and the register handler coerces an authenticated non-admin's registration to user-defined (`pkg/registry/server/layers.go:713-720`) with `Users: []string{cfg.Owner}` fixed at registration (`:758`) and not widenable by `update` (`:610-613`). An admin-defined registration therefore requires the same `authAdmin` callback that returns the tenant's whole list | `readableBy`'s admin arm; §7.3.1's write authorization paragraph |
| A caller probing for a layer ID that exists but is invisible | Absent from the list, and a write against it is refused with `auth.forbidden` as today | §7.3.1's staged paragraph and its existing write authorization paragraph |

## Testing

**TEST-1: pin the filter at the endpoint (unit).** New cases in
`pkg/registry/server/layers_test.go`, beside
`TestLayerEndpoint_ListReturnsRegisteredLayers` (`:196-224`) and
`TestLayerEndpoint_UnregisterSoftDeletesAndRestoreRecovers` (`:56-115`). The
existing harness (`:17-25`) installs no admin
callback, so it runs on the constructor default that admits
(`pkg/registry/server/layers.go:180`) and reads the whole list; its assertions
stay green and still mean what they meant. The new cases need a harness variant
taking a per-request `error` for `WithAdminAuth`, a per-request `layer.Identity`,
and an optional `layer.GroupResolver`. Name the function
`TestLayerEndpoint_ListArmsByCallerRole`, table-driven, each case carrying
`// Spec: §4.6, §7.3.1`. Seed one private admin-defined layer with a `LocalPath`
the assertions search for, one public admin-defined layer, and one user-defined
layer owned by alice.

- **`admin_reads_whole_tenant`.** An admin callback returning nil with a resolver
  reporting bob: all three layers present, including alice's user-defined layer
  and the private layer's local path.
- **`user_reads_effective_view`.** A refusing callback with a resolver reporting
  bob: the public layer alone, and the body contains neither the private layer's
  ID, nor its local path, nor alice's subject. Alice's arm additionally carries
  her own layer.
- **`anonymous_reads_nothing`.** A refusing callback with a resolver returning
  `layer.Identity{}`: `200` whose body is exactly `{"layers":[]}`, marshalled as
  `[]` rather than `null`.
- **`forged_credential_reads_nothing`.** A refusing callback with a resolver
  returning `layer.Identity{IsPublic: true}`, which is what the boot resolver
  produces for a credential that fails verification: `{"layers":[]}`. A helper
  that evaluates `layer.VisibleWith` before the authentication guard returns all
  three layers and fails here.
- **`admin_check_error`.** A callback returning a wrapped `core.ErrUnavailable`
  with an authenticated non-admin resolver: the caller's §4.6 view, and status
  `200`.
- **`group_resolver_seam`**, two arms. A layer carrying
  `groups: [finance-readers]` is returned to an authenticated caller the injected
  resolver names a member of, and withheld from one matching by neither JWT group
  nor resolver. This pins that `readableBy` passes the endpoint's resolver
  through. The predicate's own group semantics are already pinned in `pkg/layer`
  (`pkg/layer/scim_visibility_test.go:13-43`,
  `pkg/layer/group_resolver_failure_test.go:33-133`), so no third arm is added.
- **`deleted_arm_takes_the_same_rule`.** A tombstoned user-defined layer owned by
  alice is present for alice and for the admin arm, and absent for bob and for
  the anonymous arm, with bob's body carrying neither the layer ID nor the owner
  subject.
- **`reorder_response_takes_the_same_rule`.** Alice reorders her own user-defined
  layer under a refusing admin callback, in a tenant that also holds a private
  admin-defined layer, and receives a `200` whose body contains neither that
  layer's ID nor its local path. The same reorder under an admitting callback
  returns the whole list.

**TEST-3: correct the browser-flow doc comment (integration).**
`internal/serverboot/webui_auth_integration_test.go`,
`TestBrowserFlow_ExpiredSessionAcrossSurfaces` (`:629-668`). The comment at
`:632` states that the layer read answers unfiltered, which CODE-2 falsifies,
while the `200` assertion at `:652-653` survives unchanged and would silently
stop describing the behavior. Rewrite the comment to describe each surface after
the fix: the meta-tool route refuses with `auth.token_expired`, the layer read
answers `200` and reports the verification failure through no error code, and the
posture read answers `200` with no subject. State what the layer read's `200` now
holds on this fixture: `newBrowserStack` installs no admin callback
(`internal/serverboot/webui_auth_integration_test.go:216-218`), so the
constructor default admits (`pkg/registry/server/layers.go:180`), the request
takes `readableBy`'s admin arm rather than its no-subject arm, and the body
carries the stack's whole layer list, which this stack seeds as empty. Keep all
three assertions verbatim.

The comment must not describe the no-subject arm, because this fixture cannot
take it. TEST-4's new `adminAuth` field is what a case needing that arm installs,
and TEST-3 does not need it: the surface it documents is the divergence between
the three routes on one expired credential.

This case seeds no layer rows of its own. `newBrowserStack` writes no layer
configs today (its only store writes are `CreateTenant` at `:191` and
`PutManifest` at `:198`), and this case passes no rows through the seam TEST-4
adds, so its layer read returns an empty list and asserts only the status of each
surface. TEST-4 carries the disclosure assertion, in a case that seeds its own
rows through that seam.

**TEST-4: prove the narrowing through the wired stack (integration and e2e).**

- **A layer-config seam on the browser stack's fixture.** `browserStack` boots
  the real JWKS identity provider (`:186`), the real verifier (`:211`), the real
  resolver (`:212`), and the layer endpoint on its own mux outside the meta-tool
  handler (`:217-222`, `:248`), which is the wiring the per-identity case needs.
  It writes no `store.LayerConfig` rows, and `GET /v1/layers` answers from
  `store.ListLayerConfigs` alone, so the `pub` and `eng` values at `:194-197` are
  invisible to that read: they are a `[]layer.Layer` slice handed to `core.New`
  at `:214` as the composed catalog's layer list. Add one field to `stackOpts`:

  ```go
  // layerConfigs seeds GET /v1/layers. newBrowserStack writes each row with
  // st.PutLayerConfig after CreateTenant, before the endpoint is built. The
  // zero value is nil, which writes nothing, so a caller that does not set it
  // reads the empty list the stack returns today. A caller leaves TenantID
  // unset, because the fixture owns the tenant and stamps it on each row
  // before the write. The layers field handed to core.New is the composed
  // catalog's list and does not reach this read.
  layerConfigs []store.LayerConfig
  ```

  A second `stackOpts` field is required, because the fixture installs no admin
  callback (`:216-218`) and the constructor default admits, so every arm of the
  case below would read the whole list and the case would assert nothing:

  ```go
  // adminAuth is installed with WithAdminAuth. The zero value is nil, which
  // leaves the constructor default that admits every caller, matching what
  // the fixture does today. A case that needs the non-admin arms passes the
  // closure serverboot installs on an identity-provider-configured registry,
  // which refuses a caller holding no §4.7.2 grant.
  adminAuth func(*http.Request) error
  ```

  `newBrowserStack` ranges over `opts.layerConfigs` immediately after the
  `CreateTenant` call at `:191`, stamping the fixture's tenant on each row and
  then writing it:

  ```go
  for _, cfg := range opts.layerConfigs {
      cfg.TenantID = tenant
      if err := st.PutLayerConfig(t.Context(), cfg); err != nil {
          t.Fatalf("PutLayerConfig %s: %v", cfg.ID, err)
      }
  }
  ```

  The stamp is required rather than cosmetic: `store.Memory` keys a row on
  `layerKey(cfg.TenantID, cfg.ID)` and `ListLayerConfigs` returns only rows
  whose `TenantID` matches (`pkg/store/memory.go:404`, `:407-411`, `:427-435`),
  while the fixture's `const tenant = "bf-tenant"` at `:190` is local to
  `newBrowserStack` and unreachable from a caller's `stackOpts` literal, so an
  unstamped row is written under the empty tenant and `GET /v1/layers` for
  `bf-tenant` answers `{"layers": []}` for every identity. Failing the test with
  `t.Fatalf` on an error is the fixture's existing convention for a seed write
  (`:192`, `:203`). Nothing clears the
  field: the store is per-stack (`st := store.NewMemory()` at `:189`) and the
  `httptest.Server` is closed by the `t.Cleanup` at `:249`. Every existing
  caller of `newBrowserStack` leaves the field unset and is unaffected, which
  the suite's existing cases pin by continuing to pass unchanged. The case
  below is the only one that sets it, and if the seam does not fire that case
  fails on its own first assertion, because a stack that wrote no rows returns
  the empty list for every identity.
- **Per-identity narrowing through the real resolver, verifier, and mount.** Add
  one case to `internal/serverboot/webui_auth_integration_test.go` that builds a
  stack with `browserAuth: true` and with `layerConfigs` carrying a public row,
  a row declaring
  `Groups: []string{"engineering"}` with a `LocalPath` the assertions search for,
  and a user-defined row whose `Owner` and `Users` name a third subject. Name it
  `TestBrowserFlow_LayerListNarrowsToCaller`, set `adminAuth` to a closure
  refusing every caller, and issue `GET /v1/layers` three times, with a valid
  cookie for a caller in `engineering`, with a valid cookie for a caller outside
  it, and with no cookie, carrying `// Spec: §4.6, §7.3.1`. Assert `200` on all
  three, that the group-gated row is present only for the member, that the public
  row is present for both cookie-carrying arms, that the cookie-less arm's body
  is exactly `{"layers":[]}`, and that the withheld rows' IDs, local path, and
  owner subject appear in neither of the other bodies. A fourth arm leaves
  `adminAuth` nil and asserts the cookie-less caller reads all three rows, which
  pins that the admin arm and the no-identity-provider deployment take the same
  branch. `browserAuth: true` is what makes
  the two cookie-carrying arms resolve a caller at all: `oidcJWTVerifier` reads
  `__Host-podium_session` only when its `sessionCookie` argument is set
  (`internal/serverboot/identity_verify.go:215`, `:218-222`), which
  `newBrowserStack` passes from `opts.browserAuth` (`:211`), so on a default
  stack all three arms would resolve anonymous and the case would assert
  nothing. The expired credential arm needs no
  case here; TEST-3's function already covers that surface.
- **CODE-4's wiring.** `browserStack` installs no group resolver (`:217-218`) and
  the wiring lives in `serverboot.go`'s builder chain, so only a test that runs
  the binary reaches it. Extend the existing push-then-remove case in
  `test/e2e/auth_scim_visibility_test.go`, which boots the real binary with
  `PODIUM_SCIM_TOKENS` (`:33-35`) and carries no darwin skip, with a
  `GET /v1/layers` assertion on the same request that already checks artifact
  visibility: the groups-restricted layer is listed for the SCIM-resolved member
  and absent after the removal.

**TEST-5: pin the panel's empty-state heading (unit, component).** Extend the
LayerPanel empty-state case in `web/ui/src/surfaces.test.tsx` at `:17573-17584`,
which renders the panel with a stubbed empty `/v1/layers`, with one assertion
that the `.empty-title` reads "No layers to show", under
`// Spec: §4.6, §7.3.1`. The title carries no assertion today; the suite's only
`.empty-title` assertions are `:2988`, `:4346`, and `:13756`.

Three assertions look like breaks and are not, and are left unedited:
`:8378-8388` matches the panel lead with an unanchored regex and asserts the
collision sentence CODE-5 preserves; `:17780` asserts the lead literal, which
CODE-5 leaves verbatim; and `:13743-13759` pins `DeletedLayers.tsx:132`, a
different component outside this change.

One fixture comment in the same file is falsified and takes a string edit beside
CODE-5's panel comments. `bobLayer`
(`web/ui/src/surfaces.test.tsx:14267-14269`) reads "The list read is unfiltered,
so it reaches the panel alongside the caller's own", which after CODE-2 is no
longer why another subject's user-defined layer appears in a panel row. The
reason becomes the stub: the suite's `/v1/layers` stub returns the row, and on a
live registry that row reaches a caller the §7.3.1 admin arm admits. The rest of
the comment, including "a reorder that named it would be refused whole", is
unchanged, and the fixture's own assertions are unchanged.

## Manual validation

The hand-run scenarios on the Keycloak-backed `oidc-jwt` stack that contradict
the narrowed read are S48 step 4, S49 step 4, and S50. They are staged here in
`test/manual-validation.md`'s existing conventions, and each is re-run by hand
before it is committed.

**S48 step 4** (`test/manual-validation.md:4794-4808`) issues an anonymous list
read and expects the user-defined layer to appear. The surface a human reads is
the terminal output of the curl beside the panel row; the wrong output it catches
is a personal layer appearing to a caller who does not own it. The command gains
the owner's credential:

```bash
curl -sS "http://127.0.0.1:8153/v1/layers" \
  -H "Authorization: Bearer $TOKEN" | python3 -m json.tool | grep -i -e '"ID"' -e secret
```

The whole Expect block at `:4801-4808` is replaced by the following, which keeps
the secret-redaction rule and the "yours" marker sentence verbatim and drops the
sentence at `:4806-4808` stating that the list read is unfiltered:

**Expect.** `own-release` is listed, and no line carries a secret value. The
secret is returned on registration and on a rotation and is redacted from every
other response, so once the reveal is dismissed it cannot be read back. In the
panel the row carries the "yours" marker, which the panel draws by comparing the
layer's stored owner against the subject the posture read reports. The list read
answers the layers the caller may read, so the same request without the header
returns `{"layers": []}`: this stack configures `oidc-jwt` and seeds no admin
grant, so an unauthenticated caller is neither an admin nor an authenticated
subject and reads no layers at all. The marker remains a rendering of ownership
over the rows the caller can see.

**S49 step 4** (`:4874-4882`) issues the same anonymous read and counts the
tombstoned layer. It gains the same header, so the count is zero because the
layer is tombstoned rather than because the caller is anonymous:

```bash
curl -sS "http://127.0.0.1:8153/v1/layers" \
  -H "Authorization: Bearer $TOKEN" | grep -c own-release || true
```

**Expect.** The live list no longer carries `own-release`, read under the owner's
own credential. The layer is restorable from the panel's recovery control until
the §8.4 window runs out, which is what makes this an unregister rather than an
erasure.

**S50** (`:4889-4986`) states outright that the list read is unfiltered (`:4956`)
and drives the non-owner refusal from the `own-release` panel row. That row no longer
exists in bob's panel once his list omits `own-release`, because panel rows come
solely from the list read (`web/ui/src/surfaces/LayerPanel.tsx:175`, `:308`,
`:796`) and the panel offers no entry by ID. The panel arm moves to a row bob can
still see rather than disappearing, so the scenario keeps the refused-write
rendering it is the only hand-run source of, and it gains a terminal arm for the
layer bob cannot see.

The header blocks are rewritten first. **Goal** (`:4891-4893`) becomes: validate
that the registry refuses a layer write from a signed-in caller who neither owns
the layer nor is a tenant admin, that the panel reports the refusal on the row
without claiming to know why, and that a layer outside the caller's view is
absent from that caller's list and still refused when named directly. **Covers**
(`:4895-4896`) gains the §7.3.1 layer read visibility rule beside the layer-write
authorization rule, `auth.forbidden`, and the §13.10 panel's treatment of a
refused write. **Why by hand** (`:4898-4901`) becomes: the assertion is that a
second person, signed in through the same UI, is refused a write on a row that
person can see and reads a list that omits the layer that person cannot see. The
refusal's rendering is the part no Go test reads, and the panel presenting
per-owner scoping as server-enforced while the server failed open is the defect
this closes.

Step 2's expectation becomes:

**Expect.** The body reports `subject` equal to `$BOB_SUBJECT`, which differs
from `$SUBJECT`. A body whose `subject` equals `$SUBJECT` means the sign-in
reused the admin profile's SSO session, and every later step in this scenario
would then exercise the owner arm of the rule rather than the non-owner arm it
exists to test. The panel lists `public-handbook` alone. `private-comp` and
`comp-readers-policy` are absent because bob's subject is in neither the `users:`
list nor the `comp-readers` group, and `own-release` is absent because a
user-defined layer is visible to its registrant alone. `public-handbook` is
present because bob is authenticated and it is public. A panel listing more than
`public-handbook` means the list read is still unfiltered, which is the failure
this step now catches; an empty panel means bob's sign-in did not resolve a
subject, and step 1 is re-run before continuing.

Steps 3 and 4 keep their panel interaction and their expectations, retargeted
from the `own-release` row to the `public-handbook` row. That layer is
admin-defined, bob holds no admin role, so §7.3.1's write rule refuses him there
on the admin arm and the panel renders the same refusal on a row bob can see.
Step 4's network-panel assertion reads the `DELETE /v1/layers?id=public-handbook`
request instead.

A new step 5 covers the layer bob cannot see, from the terminal. Mint bob's token
the way the prerequisite mints `TOKEN` (`:4064-4067`), with `-d username=bob -d
password=bob`, exported as `BOB_TOKEN`, then:

```bash
curl -sS -X DELETE "http://127.0.0.1:8153/v1/layers?id=own-release" \
  -H "Authorization: Bearer $BOB_TOKEN" \
  -w '\nstatus=%{http_code}\n'
```

The layer ID travels in the `id` query parameter, which is where the handler
reads it (`pkg/registry/server/layers.go:943-947`); a request without it answers
`400 registry.invalid_argument` before any authorization runs, and the handler
reads no request body.

**Expect.** `auth.forbidden` at HTTP 403. A `200` means the server let a
non-owner delete another caller's layer, which is the failure this scenario
exists to catch. The refusal names neither the owner nor the state of the
session, because it carries neither, and it is the same refusal a caller who can
see the layer receives, so the narrowed list discloses nothing further about it.

The existing step 5, the owner arm reached from the admin window's panel row,
becomes step 6 and names its target explicitly. Its instruction reads "press
Unregister on the `own-release` row" rather than "press Unregister on the same
row" (`test/manual-validation.md:4979`), because "the same row" takes its
referent from step 3, and step 3 now acts on `public-handbook`. Left as a
back-reference the step would send the operator at an admin-defined layer, where
this stack's registry refuses the admin as well: the standalone server runs with
`identity_provider.type: oidc-jwt` and no bootstrap admin grant, so the admin
gate falls through to `registry.AdminAuthorize` against an empty grant table
(`internal/serverboot/serverboot.go:1247-1255`) and the write answers `403
auth.forbidden` (`pkg/registry/server/layers.go:957-961`). On `own-release` the
step still succeeds, because `authorizeLayerWrite` admits a user-defined layer's
stored owner before it reaches the admin gate
(`pkg/registry/server/layers.go:212-219`) and the scenario's S48 prerequisite
(`test/manual-validation.md:4903-4904`) registered that layer as the admin. Its heading, its Expect, and its "Without this step" sentence are
otherwise unchanged. The row is present in the admin's window because the
admin's own list still carries `own-release`: the admin registered it, and a
user-defined layer is visible to its registrant.

## Documentation changes

**DOC-1.** The reference pages state the layer write rule in detail and state no
read rule. `docs/deployment/oidc/okta.md:97-98` already claims the filtered
behaviour for a non-admin user, so that page needs no edit and becomes accurate.

`docs/reference/http-api.md`, `### List layers` (`:346-350`), which is a bare code
block today, gains one paragraph stating that a caller holding the tenant `admin`
role receives the tenant's whole layer list, that any other authenticated caller
receives the layers that caller can see under the visibility rules, that a caller
the registry does not authenticate, including one whose credential fails
verification since these endpoints raise no authentication error of their own,
receives an empty list, that a withheld layer is absent from the `200` rather
than refused with an error code, and closing with the page's established bypass
sentence: "A registry started with no identity provider configured, or one
started in public mode, authenticates no caller, so the read returns the tenant's
whole layer list there." No filesystem registry is named, because that mode
serves no HTTP read.

`docs/reference/http-api.md`, `### List soft-deleted layers and restore`
(`:389-396`), gains one sentence saying the same filter applies to the
`?deleted=true` arm.

`docs/reference/cli.md`, `### podium layer list` (`:431-437`): "Lists configured
layers and their current state." becomes a sentence naming the layers visible to
the caller's identity, carrying the same bypass sentence, and stating that a
caller holding the tenant `admin` role sees every layer in the tenant while
another caller sees the layers that caller's identity admits and an
unauthenticated caller sees none.

`docs/deployment/layers.md:101`: the clause describing what `podium layer list`
prints takes the same three arms as the CLI reference: a caller holding the
tenant `admin` role, and every caller on a registry that authenticates none, sees
every layer in the tenant; any other authenticated caller sees the layers that
caller's identity admits; and an unauthenticated caller sees none. Line `:131`
needs no edit, because the `--deleted` arm is filtered on the terms stated at
`:101`.

`docs/deployment/access-control.md:121` needs no edit. The step directs an
operator through `podium admin show-effective`, and an operator holding the
`admin` role reads the whole list from `podium layer list`, so the diagnosis is
unaffected.

**DOC-2** is the manual validation section above.

**DOC-4.** The design documents state that the list read is unscoped, and each
statement is the reason a rendering rule gives for itself. `web/DESIGN.md:44`
("The list read hands the panel every layer stored under the tenant"), `:384`
("returns every layer stored under the tenant, so the panel's role split is
presentation"), `:467` ("read hands the panel every layer stored under the tenant
on the terms the"), `web/design/README.md:146` ("because the list endpoint hands
the panel every layer under the tenant"), and `:154` ("The layer list endpoint is
not scoped by caller, so this split is presentation over a list the server hands
you whole") each take the narrowed read: the list read reports what the caller
may read under §7.3.1, which is the tenant's whole list for a tenant admin and
for every caller on a registry that authenticates none, and the layers §4.6
admits otherwise. The role claim in the same sentences is unchanged: no response
reports that the caller holds the administrator role, and the panel predicts no
outcome.

Further statements in the same sections are falsified by that change and take an
edit with it.

- `web/design/README.md:146` opens the layer-panel section by stating that the
  panel's "contents differ by layer class rather than by caller role", and gives
  the unfiltered read as the reason. Under §7.3.1 the rows the panel holds are
  the rows the caller may read, so the opening clause states instead that the
  panel's contents depend on what the caller may read and that its per-row
  rendering differs by layer class. The clause "no response reports the caller's
  role" in the same sentence stands, on the same terms as the role claim `:154`
  keeps, so the panel's role split remains presentation.
- `web/DESIGN.md:45` and `:467-468` each attribute the read to "the terms the
  unfiltered-list rule sets". Both clauses name the §7.3.1 layer read visibility
  rule this proposal stages instead.
- `web/DESIGN.md:385-388` reads "That statement is owned by the unfiltered-list
  rule under \"The layer-ownership defect\" in
  `proposals/0013-build-the-13-10-web-ui.md`, and the panel carries no condition
  that rule does not state." The narrowed read is a condition that rule does not
  state (`proposals/0013-build-the-13-10-web-ui.md:133`, "**`GET /v1/layers` is
  unfiltered.**"), so the sentence names this proposal's §7.3.1 rule as the owner
  of the statement above it and drops the no-further-condition clause.
- `web/DESIGN.md:288-290` opens the DESIGN.md layer-panel section with the claim
  `web/design/README.md:146` opens with, and gives it as the reason for the same
  row-difference rule: "Its rows differ by layer class and by ownership rather
  than by the caller's role, because the list read is unscoped and no response
  reports that the caller holds the administrator role." It takes the same edit
  `:146` takes: the panel's rows are the rows the caller may read under §7.3.1,
  and its per-row rendering differs by layer class and by ownership. The clause
  "no response reports that the caller holds the administrator role" is kept
  verbatim, as it is at the other sites. Correcting `:44`, `:384`, and `:467` in
  this file while leaving `:289` standing would land the design corpus
  internally inconsistent, which is the ground Pass 8 recorded for `:146`.
- `web/design/README.md:154` closes with "so on a registry where the rule is live
  a caller who resolves no subject carries no ownership marker and still receives
  a refusal the panel presents", and `web/DESIGN.md:422-427` carries the same
  clause in the same structure ("so on a registry where the rule is live a caller
  who resolves no subject carries no marker and still receives the refusal the
  paragraph above tells the panel to present"). Both take one replacement, so the
  two design documents describe one post-change state. The sentence before the
  clause, that whether the write rule is live is a property of the deployment's
  configuration rather than of whether this caller resolved a subject, stands
  unchanged. The clause after "so" is split by the condition it names, because
  running no browser sign-in and authenticating no caller are independent
  postures: the browser flow is gated on `cfg.webUIAuth`
  (`internal/serverboot/serverboot.go:1278`) while whether a caller is
  authenticated is gated on `cfg.publicMode || cfg.identityProvider == ""`
  (`:1252`), and the §13.10 gateway-fronted deployment runs no browser flow while
  a subject does resolve (`spec/13-deployment.md:170`;
  `web/DESIGN.md:543-546`). The replacement states both arms. Where the registry
  configures no identity provider or runs in public mode, the write rule is not
  live (`spec/07-external-integration.md:97`,
  `internal/serverboot/serverboot.go:1252`,
  `pkg/registry/server/layers.go:218`), the panel holds the tenant's whole list,
  no row carries an ownership marker because no subject resolves, and no write is
  refused. Where the registry configures an identity provider and does not run in
  public mode, the rule is live and the read has three arms under §7.3.1: a
  caller holding the tenant `admin` role reads the tenant's whole list, any other
  caller who resolves a verified subject reads the layers §4.6 admits, and a
  caller who resolves no subject reads no layers. A caller who resolves a subject
  receives on any row it can see whatever refusal the rule produces, which the
  panel presents, and a caller who resolves no subject stands on the panel's
  empty state with no row to mark and no write to attempt.
- The board 14i sentences beside that clause take the same split.
  `web/design/README.md:154` describes board 14i as drawing the panel for a
  caller who resolves no subject on a deployment running no browser sign-in, with
  "every write control is rendered and no row carries an ownership marker". That
  state is reachable on the arm where the registry authenticates no caller, and a
  registry that configures no identity provider cannot enable the browser flow
  (`docs/reference/error-codes.md:72`), so the board keeps its rows and its
  controls and the description names that arm rather than the whole
  no-browser-sign-in class. The retained closing sentence, "On a deployment that
  runs the browser flow the same board carries the shell's sign-in control beside
  the panel, which is what the sign-in control rule renders for that posture",
  names a deployment that configures `oidc-jwt` with public mode off, so a caller
  who resolves no subject there reads no layers: the sentence states that the
  same board carries the shell's sign-in control beside the panel's empty state.
  `web/design/README.md:93` needs no edit, because it states that board 14i
  carries the panel itself for that caller, which stays true whether or not the
  panel holds rows.
- `web/DESIGN.md:429-435` gives "Listing layers carries no authorization check"
  as the reason that whether an anonymous caller sees the panel is "a design
  decision rather than one the API makes". Under §7.3.1 the list read is an
  authorization decision, so the paragraph states instead that rendering the
  panel stays a UI decision while what the panel holds is decided by the read:
  the tenant's whole list for a tenant admin and on a registry that authenticates
  no caller, the layers §4.6 admits for any other caller who resolves a verified
  subject, and no rows for a caller who resolves none. The standalone sentence is
  unaffected and is kept: that deployment configures no identity provider, treats
  the local operator as the administrator, and keeps the panel available holding
  the tenant's whole list.

The ownership marker rule itself is otherwise unchanged. These files carry no
runnable example, so `doccov` gains no entry and no test is owed.

**DOC-3.** `CHANGELOG.md` gains one entry in the `### Fixed` block under
`## [Unreleased]`, which is where this repository records user-facing changes in
the feature commit itself. There is precedent for recording a visibility change
that alters what a caller sees on upgrade (`CHANGELOG.md:79`):

> - **Layer list visibility**: `GET /v1/layers` reports what the calling identity
>   may read, on both the live and the `?deleted=true` arm, and the reorder
>   response reports the same set. A caller holding the tenant `admin` role still
>   receives the tenant's whole layer list. Any other authenticated caller
>   receives the layers visible to that identity, and a caller the registry does
>   not authenticate receives an empty list. A layer outside that set is absent
>   from the response rather than refused, and no error code reports the
>   narrowing. A registry started with no identity provider configured, which
>   includes public mode, returns the whole layer list to every caller as before.
>   On upgrade, a signed-in non-admin sees fewer rows in `podium layer list` and
>   in the web UI layer panel, and an unauthenticated caller against a registry
>   that configures an identity provider sees none.

## Open questions

None. The failed-credential question is closed by the rule itself: such a caller
resolves no verified subject and reads no layers, which is the strictest
available answer and discloses nothing, so no divergence with §6.3.2 or §6.3.3
needs resolving. The reorder question is closed by the write gate: a tenant admin
reads the whole run, and a non-admin can name only their own user-defined layers,
so no accepted reorder from the panel restamps a run the caller cannot see whole,
and a subset reorder from the CLI is pre-existing endpoint behaviour this
proposal does not change.

## Open decisions

Two of the three decisions this section carried are settled. They are recorded
here with the reasoning that produced them, because the alternatives were
weighed and the record of why is worth keeping.

**Settled: the layer read carries an admin arm.** A tenant admin reads the
tenant's whole list, including another caller's user-defined layers. The staged
text is written that way throughout and no reviewer may reopen it. The reasoning
below stands as the record.

**Settled: the register form's layer-class control is not in this proposal.** It
is deferred to its own proposal, together with the wider defect it belongs to:
a non-admin can register a `local`-source layer naming any path the registry
process can read, which is a disclosure the class control alone does not close.
Deferring the form does not defer that defect, which is recorded separately.

**1. Does the layer read carry an admin arm?** Settled: yes, keyed on the
`authAdmin` callback the endpoint already holds, which is what this proposal
stages.

- Taking the admin arm: a tenant admin reads every layer in the tenant, including
  another caller's user-defined layers with their owner subject and local path.
  CODE-3, TEST-2, the gateway documentation edits, and both former open questions
  disappear. The scoping of §6.3.2 and §6.3.3 does not follow from this arm
  either way; that is open decision 2. `internal/serverboot` changes only for
  CODE-4. Every list read by a non-admin costs one `store.IsAdmin` call, which is
  what a layer write already costs.
- Refusing the admin arm: §4.6 decides for every caller, an admin holding no
  group and owning no personal layer sees fewer rows than the tenant holds, and
  the failed-credential caller reads the public layers rather than nothing.
  CODE-3 and SPEC-2 come back, and with them the question of what a failed
  credential reads.

SPEC-1's staged paragraph is written for the admin arm, which is the settled
answer above.

**2. Is an empty `200` for a failed credential acceptable on this route, and does
the spec say so twice?** Default: an empty `200`, with no rescoping of §6.3.2,
§6.3.3, §6.9, or §13.12. The route already answers `200` to an expired session
(`internal/serverboot/webui_auth_integration_test.go:650-655`), and after the
change the body discloses nothing. Three arms:

- **Empty `200`, nothing rescoped**, which is what this proposal stages. Its cost
  is stated rather than argued away: §6.3.3 states its rejections for the
  registry process and carries no route qualifier (`spec/06-mcp-server.md:90`,
  `:102`), so after SPEC-1 lands the spec holds both "rejected with
  `auth.untrusted_token`" and an empty `200` for the same credential on
  `GET /v1/layers`. Today the spec assigns that read no disposition at all, so
  this arm converts a code-versus-spec divergence into a spec-versus-spec
  overlap.
- **Empty `200`, with the §6.3.x scoping retained.** SPEC-2 comes back as earlier
  revisions staged it: a scoping clause and a §7.3.1 cross-reference on
  `spec/06-mcp-server.md:76`, `:102`, `:106`, §6.9's rows (`:389`, `:390`,
  `:391`), and `spec/13-deployment.md:498`. It is the only arm under which the
  applied spec carries one disposition per route. Its cost is a three-file spec
  edit and S1 widening back to those files.
- **A `401` on this route.** The two routes agree with no rescoping, at the cost
  of a refusal on a read that is otherwise never refused, and it needs its own
  SPEC-1 sentence and a change to the endpoint's mounting or its handler.

**3. Does the register form's layer-class control belong in this proposal?**
Settled: no. The defect is real and verified: `register` resolves the class
server-side and answers `201` carrying the class it chose
(`pkg/registry/server/layers.go:711-721`, `:736-762`), while the form sends a
class from a `<select>` and the outcome line states only the layer ID
(`web/ui/src/surfaces/RegisterLayerForm.tsx:214`, `:275-286`). Closing it needs a
new §7.3.4 field, a client change, and its own tests, and no deliverable here
depends on it. It belongs in its own proposal. Adding it to this one widens the
change by one spec field, two code sections, three tests, a manual-validation
step, and a design-document pass.

The deferral is on scope alone. The same registration path carries an unfixed
disclosure: `register` copies `local_path` into the stored config with no
validation and no root confinement, so an authenticated non-admin can register a
`local`-source layer naming any path the registry process can read and then read
that path's contents through their own user-defined layer. That is recorded as
its own defect and is not deferred by deferring the form.

## Non-goals

- Redesigning the web UI layer panel for a partial list. The empty-state heading
  and the doc comments CODE-5 enumerates are edited, and the panel lead stays
  verbatim: the filter makes it accurate for an authenticated non-admin, and on
  the admin arm the panel holds the tenant's whole list by design while the
  catalog stays composed under §4.6.
- Explaining, in the panel, why a registered layer does not appear in the
  reloaded list. The case does not arise under the admin arm: a registration
  whose visibility can exclude the registrant is admin-defined and requires the
  admin callback that also returns the tenant's whole list, and a user-defined
  layer's `users: [<registrant>]` is fixed at registration and cannot be patched
  (`pkg/registry/server/layers.go:713-720`, `:758`, `:610-613`).
- Adding a diagnostic override that restores the full list to a non-admin caller,
  in the style of the `as_admin=1` arms at `docs/reference/http-api.md:166` and
  `:198`. A tenant admin already reads the whole list, so no override is needed
  for the case those arms exist for.
- Adding a §6.10 error code. The remedy narrows a `200` collection rather than
  denying a named resource, the handler's existing `registry.unavailable` arms
  are unchanged, and `matrix-audit` gains no cell.
- Changing the §7.3.1 layer write authorization rule, any write handler's
  authorization behaviour, or `layerIdentityResolver`. The read reuses the
  `authAdmin` callback the write gate already installs and adds no branch to it,
  and the resolver's public-mode fallback for a credential that fails
  verification is left as it is, because `readableBy`'s authentication guard
  makes it unreachable on the read.
- Changing how `POST /v1/layers/reorder` assigns absolute order values. Only its
  response body is narrowed.
- Tenant routing on the layer endpoint. It reads a single tenant ID fixed at
  construction (`internal/serverboot/serverboot.go:1238`), and multi-tenant
  routing on this surface is a separate question.
- Emitting an audit event for a narrowed listing, or reporting to the caller that
  rows were withheld.

## Resolved in adversarial review

Review rounds populate this section.

### Pass 1

The draft's first challenge pass, whose corrections are already folded into the
text above: the sentence
attributing the anonymous treatment of a failed verification to §6.3.3 was
removed from the staged spec text, from CODE-3's doc comment, and from TEST-2's
annotation, because §6.3.3 rejects such a token; the implicit-visibility and
filesystem-registry clauses were dropped from the staged spec paragraph as a
restatement and as an inaccuracy; CODE-2 grew the reorder response body, which
made the fix bypassable by a single request without it; CODE-5's panel lead edit
was dropped because the filter repairs that sentence rather than breaking it, and
its empty-state edit was reduced to the heading; TEST-1's group case lost its
JWT-groups arm and its standalone empty-body case as duplicates of assertions
that exist in `pkg/layer` and of an assertion that belongs inside another case;
TEST-3 lost its fixture growth because the browser stack writes no layer rows;
TEST-4 moved off `test/integration`, which reaches none of the boot wiring it
claimed to exercise, onto the browser stack and the SCIM end-to-end test; TEST-5
lost the two literal edits that CODE-5's reduction made unnecessary and gained
the corrected target for the empty-state case; and DOC-1, DOC-3, and the staged
spec text lost the filesystem-registry bypass clause, which names a mode that
serves no HTTP read.

### Pass 2 (2026-08-31, automated)

- **CODE-2's filter passed a `layer.Visibility` where `layer.VisibleWith` takes a
  `layer.Layer`.** The staged helper now wraps the projection in a
  `layer.Layer{ID: c.ID, Visibility: core.VisibilityOf(c)}` at the call site, with
  a comment saying precedence is unused on this read, and CODE-2 records why an
  exported `layer.Layer` builder was rejected instead. CODE-1's doc comment names
  `layer.Layer` as the type the evaluator consumes, and the Summary and the
  Decisions sentence describing the reuse were corrected to match.
- **TEST-4's browser-stack case asserted over layers the fixture never stores.**
  TEST-4 now stages a `layerConfigs` field on `stackOpts`, written with
  `st.PutLayerConfig` after `CreateTenant`, and the per-identity case seeds its
  own public, group-gated, and user-defined rows through it. The `:194-197`
  attribution was corrected to name the composer's layer list, the `CreateTenant`
  citation was corrected from `:189` to `:191`, and TEST-3's closing paragraph and
  the matching "Watch out for" bullet were rewritten to agree with TEST-4.
- **The staged §7.3.1 paragraph attributed the enumeration rule to §4.5.3.** The
  staged text, the current-state prose, the Decisions sentence, and CODE-5's
  reference now cite §4.5.5's Unknown-paths rule, which is where the
  non-enumerability sentence lives (`spec/04-artifact-model.md:562`).
- **S50 step 2's expectation stated that bob sees every `registry.yaml` layer.**
  The replacement Expect names the row set bob actually sees, which is
  `public-handbook` alone, gives the reason each other row is absent, and states
  that a longer list means the read is still unfiltered.
- **The whole-list arm was grounded in "the §4.6 bypasses".** SPEC-1, the
  Decisions paragraph, CODE-3's prose and doc comment, and the edge-case row now
  state the condition as §7.3.1's write paragraph states it, which is a registry
  started with no identity provider configured or one started in public mode, and
  cite §4.6's public-mode bypass for the public-mode arm alone.
- **`docs/deployment/access-control.md`'s troubleshooting step was in no edit
  list.** DOC-1 stages the sentence scoping `podium layer list` to the operator's
  own view and directing the operator to `podium admin show-effective` as the
  authority for whether a layer exists, and the admin edge-case row names the
  page.
- **DOC-2's S48 deletion range did not cover the sentence it named.** The staged
  edit now replaces the whole Expect block at `:4801-4808` with text that keeps
  the secret-redaction and "yours" marker sentences verbatim and drops only the
  unfiltered-list sentence at `:4806-4808`.
- **S50's staged `DELETE` omitted the required `id` query parameter.** The
  command reads `?id=own-release` and carries no body, which is what the handler
  parses (`pkg/registry/server/layers.go:943-947`).
- **S50's Goal, Covers, and Why-by-hand claimed panel coverage the rewrite
  removed.** The header blocks are staged with the step edits, and the panel arm
  is retargeted from `own-release` to the `public-handbook` row bob can still
  see, whose admin-defined class refuses him under the same §7.3.1 write rule, so
  the refused-write rendering stays in the corpus. The terminal write becomes a
  new step covering the layer bob cannot see, and the owner arm moves to step 6.
- **The failed-verification disposition cited a §7.3.1 rule the staged text
  omitted.** SPEC-1's paragraph now carries that sentence, stating that a
  presented credential which fails verification is treated on this read as
  carrying none and naming the §6.3.3 divergence, which reverses Pass 1's
  deferral because deferring left the code comment and the documentation citing a
  rule no section carried. DOC-1 stages the matching sentences on
  `docs/reference/http-api.md` and `docs/deployment/gateway-delegated-identity.md`,
  and open question 1 lists both as changing if the maintainer directs a refusal
  instead.
- **TEST-4's `layerConfigs` seam wrote rows the endpoint could not read.**
  `store.Memory` keys a layer row on `layerKey(cfg.TenantID, cfg.ID)` and
  `ListLayerConfigs` filters by tenant (`pkg/store/memory.go:404`, `:407-411`,
  `:427-435`), while the fixture's tenant constant is local to
  `newBrowserStack`, so an unstamped row landed under the empty tenant and the
  case could never pass. The seed loop is now staged as code that assigns
  `cfg.TenantID = tenant` before each `PutLayerConfig`, the field's doc comment
  says a caller leaves `TenantID` unset, and the per-identity case is staged
  with `browserAuth: true`, because `oidcJWTVerifier` reads
  `__Host-podium_session` only when that option is set
  (`internal/serverboot/identity_verify.go:215`, `:218-222`).
- **The §4.6 public-mode bypass citation pointed at a blank line.** The
  Decisions paragraph now cites `spec/04-artifact-model.md:615`, which is the
  public-mode bypass paragraph; `:614` is blank.
- **Open question 1's enumeration omitted the `CHANGELOG.md` entry.** The
  question now lists DOC-3's second upgrade consequence alongside SPEC-1,
  CODE-3, TEST-3, the edge-case row, and DOC-1, so it agrees with the
  edge-case row and with DOC-3's own contingency sentence.

### Pass 3 (2026-08-31, automated)

- **S50's step 6 was staged "otherwise unchanged" while the row its
  back-reference named was retargeted.** The existing step 5 reads "press
  Unregister on the same row" (`test/manual-validation.md:4979`), whose referent
  step 3 sets, and steps 3 and 4 now act on `public-handbook`. Step 6 is staged
  with an explicit target, `own-release`, and the proposal records why: the
  owner arm succeeds there because `authorizeLayerWrite` admits a user-defined
  layer's stored owner before the admin gate
  (`pkg/registry/server/layers.go:212-219`), while on the admin-defined
  `public-handbook` this stack's empty admin grant table refuses the admin too
  (`internal/serverboot/serverboot.go:1247-1255`,
  `pkg/registry/server/layers.go:957-961`).
- **The partial-block reorder row stated an outcome the endpoint does not
  produce.** The endpoint stamps `(i+1)*10` onto the named layers alone and
  leaves every unnamed layer at its stored value
  (`pkg/registry/server/layers.go:1012-1015`), so an unnamed row can tie with a
  named one or invert relative to it, which the panel's own comment already
  records (`web/ui/src/surfaces/LayerPanel.tsx:859-861`). A later round found the
  treatment was four statements of one fact and that the fact was not this
  proposal's: a subset reorder is reachable today from the CLI
  (`cmd/podium/layer.go:289-305`) with no read filter involved. The open question
  and the duplicated prose are gone; one edge-case row states it, the Non-goals
  line fences it, and CODE-5 corrects the one panel clause the filter falsifies
  (`:866`).
- **CODE-5 cited `core.go:365` for catalog visibility scoping.** That function is
  `effectiveLayerComposition`, which computes the §4.7.5 audit field. The
  citation is now the read paths that scope what a caller reads,
  `pkg/registry/core/core.go:1985-2000` and
  `pkg/registry/core/domain_load.go:156-164`.
- **§6.3.3 states its rejection for the registry process rather than for a
  meta-tool route, and was in no edit list.** SPEC-2 stages the scoping clause on
  `spec/06-mcp-server.md:102` and the cross-reference to §7.3.1, S1 carries both
  spec edits, and the staged §7.3.1 sentence, the Decisions paragraphs, the
  "Watch out for" bullet, the edge-case row, and open question 1 describe §6.3.3
  as the server-side request authentication rule rather than as a route. Open
  question 1 records that a resolution toward refusal drops SPEC-2 whole.
- **A nil request-time verifier is not the condition "authenticates no
  caller".** `selectIdentityProvider` returns nil for a free-form
  `PODIUM_IDENTITY_PROVIDER` label (`internal/serverboot/identity_verify.go:157-159`)
  and startup is allowed (`:95-97`), so that deployment would have kept an
  unfiltered read while its write gate refuses every caller
  (`internal/serverboot/serverboot.go:1252`). CODE-3 now takes the condition as
  a parameter and reads the same expression the write gate reads, hoisted into
  one local; the Summary, the Decisions paragraph, the edge-case table, and
  TEST-2's nil-verifier arms state it on those terms, and the call sites of
  `layerIdentityResolver` are named.
- **Correction to the bullet above: CODE-3 enumerated two call sites and
  asserted the enumeration was complete, while there are four.**
  `layerIdentityResolver` is called from `internal/serverboot/serverboot.go:1237`,
  `internal/serverboot/webui_auth_integration_test.go:212`, and
  `internal/serverboot/identity_verify_test.go:221` and `:241`. The last two are
  the constructions TEST-2 stages, so CODE-3 contradicted TEST-2 inside the same
  proposal, and a reader scoping S4 from CODE-3 alone would have left the
  package uncompilable. CODE-3 now names all four and states that the added
  parameter is a compile-breaking signature change.

### Pass 4 (2026-08-31, automated)

- **§6.9's failure-mode rows restate the unqualified rejection SPEC-2 exists to
  scope, and were in no edit list.** The expired-token row
  (`spec/06-mcp-server.md:389`) and the untrusted-token row (`:391`) state
  "Reject with `auth.token_expired`" and "Reject with `auth.untrusted_token`"
  with no route scope, so scoping §6.3.3:102 alone would have left the spec
  answering a forged or expired token on `GET /v1/layers` two ways. The table is
  not confined to meta-tool calls: its cross-site browser-origin row (`:393`)
  governs a gate that wraps the mux the layer endpoint is mounted on
  (`internal/serverboot/serverboot.go:1509`, `:1259`). SPEC-2 now stages both
  §6.9 behavior cells beside the §6.3.3 sentence, in the same wording and with
  the same cross-reference to §7.3.1, and records that both row keys and both
  error codes are unchanged so §6.10's inventory and its `// Matrix:` annotations
  are unaffected. The Summary, the "Watch out for" bullet, the Decisions
  paragraph, the S1 checklist row, the edge-case row for a credential that fails
  verification, the spec-amendments heading, and open question 1's enumeration of
  what a resolution toward refusal drops now name both sections. §6.9's
  cross-site browser-origin row is deliberately untouched, because its key
  already scopes it to a state-changing request and `BrowserOriginGate` reads the
  method (`pkg/registry/server/browser_origin_gate.go:30`, `:46-52`).

### Pass 5 (2026-08-31, automated)

- **SPEC-2 scoped §6.3.3's signature and expiry rejection and left the
  configured-subject-claim rejection in the same section, and its §13.12 and
  deployment-page mirrors, unqualified.** `PODIUM_OAUTH_SUBJECT_CLAIM` makes a
  token that does not carry the named claim an `auth.untrusted_token` refusal
  (`spec/06-mcp-server.md:106`), and that refusal reaches the layer endpoint
  through the same verifier: `claimIdentity` failing turns into `untrustedToken`
  (`pkg/identity/oidc_jwt.go:251-253`), `oidcJWTVerifier` returns it
  (`internal/serverboot/identity_verify.go:226-234`), and CODE-3 maps it onto the
  zero identity. SPEC-2 now stages the same scoping clause on that sentence, on
  the §13.12 env-var row that mirrors it (`spec/13-deployment.md:498`), and DOC-1
  stages it on the configuration row of the page it already edits
  (`docs/deployment/gateway-delegated-identity.md:47`). S1's row names
  `spec/13-deployment.md` alongside the other two spec files.
- **The staged §7.3.1 failed-verification rule contradicted §6.3.2's and §6.9's
  `auth.untrusted_runtime` rejection on a registry running
  `injected-session-token`.** That registry installs the runtime verifier as
  `layerVerify` (`internal/serverboot/serverboot.go:1147`), so an unregistered
  signing key resolves to the zero identity and reads a narrowed `200` while
  §6.3.2 (`spec/06-mcp-server.md:76`) and §6.9's untrusted-runtime row (`:390`)
  state an unqualified refusal. SPEC-2 now stages both with the same clause and
  the same §7.3.1 cross-reference, SPEC-1's divergence sentence names §6.3.2 and
  `auth.untrusted_runtime` beside the two §6.3.3 codes and enumerates the
  credential classes the rule covers, and the Summary, the "Watch out for"
  bullets, the Decisions paragraph, CODE-3's doc comment, the edge-case row for a
  credential that fails verification, and open question 1 state the same set.
  §6.9's `auth.tenant_unknown` row (`:392`) is recorded as deliberately
  untouched, because that refusal is raised by the tenant middleware
  (`pkg/registry/server/server.go:464-468`) rather than by the verifier the layer
  resolver calls, and the layer endpoint reads a single tenant ID fixed at
  construction.
- **Extending the annotation prohibition to §6.3.2 contradicted TEST-2's staged
  annotation.** The "Watch out for" bullet was rewritten to forbid annotating the
  layer read's anonymous treatment with §6.3.2 or §6.3.3, while TEST-2 stages
  `// Spec: §4.6, §6.3.2, §7.3.1` on `TestLayerIdentityResolver` and justifies
  §6.3.2 there because the test drives the injected-session-token provider's
  verifier. The bullet now forbids citing either section as the authority for the
  anonymous treatment and records that a test may still carry §6.3.2 when it
  drives that provider's verifier, which is what TEST-2 does. CODE-3's staged
  `// Spec: §4.6, §7.3.1` is unchanged and satisfies the narrowed rule.

### Pass 6 (2026-08-31, automated)

- **The gap argument attributed a meta-tool enumeration to §4.6's read-side
  enforcement sentence and then contradicted its own earlier reading.** The
  paragraph asserted both that §4.6 states read-side enforcement on every call
  and that §4.6's enforcement sentence enumerates `load_domain`,
  `search_domains`, `search_artifacts`, and `load_artifact` and so excludes the
  layer-management read. The enforcement sentence
  (`spec/04-artifact-model.md:613`) names no call and excludes nothing, and the
  only §4.6 sentence naming those calls (`:589`) governs layer resolution and
  `extends:` composition inside "The layer list" subsection. The paragraph now
  cites `:613` once, states that it is unqualified, records what `:589` actually
  governs, and names the narrower gap: §4.6 assigns no disposition to the
  layer-management endpoints, which §7.3.1 owns, and both places that name `GET
  /v1/layers` state no gate (`spec/07-external-integration.md:154`,
  `spec/13-deployment.md:519`). SPEC-1's staged paragraph and CODE-2's
  `// Spec: §4.6, §7.3.1` annotation are unchanged, because the corrected
  reading makes §4.6 a broader rather than a narrower authority for them.

### Pass 7 (2026-08-31, automated)

- **The S50 step-6 rationale attributed `own-release`'s registration to S47,
  which registers no layer.** S47 signs in through the UI and reads a layer
  (`test/manual-validation.md:4643`), and `own-release` is registered by S48
  (`test/manual-validation.md:4744`, `:4776`), which S50's own Prerequisites
  name (`test/manual-validation.md:4903-4904`). The sentence explaining why the
  owner arm still succeeds now credits the scenario's S48 prerequisite and cites
  those prerequisite lines. The `pkg/registry/server/layers.go:212-219` citation
  for the owner-before-admin arm is unchanged and still accurate, and the
  neighbouring reference to the window S47 signed in from is correct as written.

### Redesign 1 (2026-08-31, automated)

Three independent redesign specifications were reconciled into one edit list and
applied. The areas redesigned are the read's authorization arms, the spec
amendment, the boot-wiring change, the panel and design-document comment
corrections, and the checklist, edge cases, tests, and documentation that follow
from them.

- **The read gained an admin arm, and CODE-3, TEST-2, and both open questions
  were dropped with it.** The earlier design filtered every caller through §4.6
  and therefore needed `layerIdentityResolver` to stop mapping a failed
  verification onto the public-mode identity. Reading `authAdmin` first removes
  that need: `core.AdminAuthorize` refuses the public-mode identity
  (`pkg/registry/core/admin.go:22`), the authentication guard runs before
  `layer.VisibleWith`, and the response to a forged credential is an empty `200`
  that discloses nothing. The admin arm also absorbs the registry that
  authenticates no caller, because the installed callback admits unconditionally
  there (`internal/serverboot/serverboot.go:1247-1255`). Earlier passes in this
  log argue for CODE-3 as it stood at the time; this entry supersedes them.
- **SPEC-2 was dropped as a scope decision rather than as a consequence.** The
  admin arm does not reconcile §7.3.1's empty `200` with §6.3.2's and §6.3.3's
  unqualified refusals: those statements carry no route qualifier
  (`spec/06-mcp-server.md:90`, `:102`), and SPEC-1 now states a disposition for a
  request they also state a disposition for. Passes 4 and 5 added the scoping
  clause for that reason and their analysis still holds. This revision accepts
  the overlap on the ground that the divergence is pre-existing in code and that
  the narrowed body discloses nothing, and it puts restoring the clause to the
  maintainer as open decision 2 rather than deleting the question.
- **The partial-block reorder treatment shrank to one row.** A subset reorder is
  reachable from the CLI today with no read filter involved
  (`cmd/podium/layer.go:289-305`), and under the admin arm the panel adds no new
  path to it, so the proposal states the pre-existing endpoint behaviour once and
  fences it in Non-goals. Open question 2 is gone.
- **The design documents were in no edit list.** `web/DESIGN.md:44`, `:384`,
  `:467` and `web/design/README.md:154` state that the list read hands the panel
  every layer stored under the tenant, which CODE-2 falsifies. DOC-4 stages the
  correction, together with the two clauses that attribute the read to proposal
  0013's unfiltered-list rule and the `web/design/README.md` sentence that has a
  caller with no subject reading rows and receiving panel refusals on a registry
  where the write rule is live. The role claim in those sentences is intact, and
  S11 schedules the work.
- **Two more statements of the unfiltered read were in no edit list.** The
  panel's file-header comment (`web/ui/src/surfaces/LayerPanel.tsx:1-5`) gives
  the unfiltered read as the reason for the panel's whole rendering posture, and
  the `bobLayer` fixture comment (`web/ui/src/surfaces.test.tsx:14267-14269`)
  gives it as the reason another subject's layer reaches a panel row. CODE-5
  takes the first and TEST-5 takes the second, on the same terms as the comment
  sites already enumerated.

**What the redesign deleted.** CODE-3 in full, with its `layerIdentityResolver`
signature and behaviour change, its four call-site updates, and its doc comment.
SPEC-2 in full, in every section its anchor enumeration named. TEST-2 in full.
Both open questions, replaced by decisions. The two DOC-1 paragraphs editing
`docs/deployment/gateway-delegated-identity.md` and the DOC-1 paragraph editing
`docs/deployment/access-control.md:121`. DOC-3's contingency sentences and the
changelog entry's failed-credential upgrade consequence in its earlier wording.
Five "Watch out for" bullets that existed to defend CODE-3 or SPEC-2. The
Summary's SPEC-2 bullet and its whole-list-arm fixed decision. The staged SPEC-1
clause "The rule carries no admin arm" and the Decisions paragraphs restating it.

**Open decisions recorded.** Whether the read carries an admin arm, whether an
empty `200` for a failed credential leaves §6.3.2, §6.3.3, §6.9, and §13.12
unscoped, and whether the register form's layer-class control belongs here. The
third is excluded on scope grounds: `register` resolves the layer class
server-side and the form neither predicts nor reports it, which is a separate
defect needing its own §7.3.4 field, client change, and tests.

### Pass 8 (2026-08-31, automated)

- **A further statement of the unfiltered read was in no edit list.**
  `web/design/README.md:146` opens the layer-panel section with "Its contents
  differ by layer class rather than by caller role, because the list endpoint
  hands the panel every layer under the tenant and no response reports the
  caller's role", which is the reason the section gives for the panel's whole
  contents rule. CODE-2 falsifies both the opening claim and the causal clause,
  and correcting `:154` eight lines below it while leaving `:146` standing would
  land the design corpus internally inconsistent. DOC-4 now names `:146` in its
  enumeration of the sites that take the narrowed read, and stages the opening
  clause on the §7.3.1 rule: the panel's contents depend on what the caller may
  read, and its per-row rendering differs by layer class. The role claim in that
  sentence stands unchanged, on the same terms as the role claim `:154` keeps.
  This supersedes the Pass 7 entry "The design documents were in no edit list",
  whose enumeration named `web/DESIGN.md:44`, `:384`, `:467`, and
  `web/design/README.md:154` alone. S11 already schedules both files and needs no
  change.

### Pass 9 (2026-08-31, automated)

- **`web/DESIGN.md:289` stated the layer-panel section's premise as the unscoped
  read and was in no edit list.** That sentence is the DESIGN.md twin of
  `web/design/README.md:146`, which Pass 8 added to DOC-4 on the ground that
  correcting one and leaving the other standing lands the design corpus
  internally inconsistent. DOC-4 now stages `web/DESIGN.md:288-290` on the same
  terms: the panel's rows are the rows the caller may read under §7.3.1, its
  per-row rendering differs by layer class and by ownership, and the clause "no
  response reports that the caller holds the administrator role" is kept
  verbatim.
- **`web/DESIGN.md:429-435` gave "Listing layers carries no authorization check"
  as the reason the panel is shown to an anonymous caller, and was in no edit
  list.** CODE-2 makes the list read an authorization decision, so the premise is
  false after the change. DOC-4 now stages that paragraph: rendering the panel
  stays a UI decision, while what the panel holds is decided by the read, which
  is the tenant's whole list for a tenant admin and on a registry that
  authenticates no caller, the layers §4.6 admits for any other caller who
  resolves a verified subject, and no rows for a caller who resolves none. The
  standalone sentence is unaffected and is kept.
- **`web/DESIGN.md:422-427` carried the same no-subject-still-receives-a-refusal
  clause DOC-4 rewrote in `web/design/README.md:154`, and was in no edit list.**
  DOC-4 now stages one replacement for both clauses, so the two design documents
  describe one post-change state for a caller who resolves no subject.
- **DOC-4 rewrote board 14i's design sentence on the false premise that a
  deployment running no browser sign-in authenticates no caller.** The browser
  flow is gated on `cfg.webUIAuth` (`internal/serverboot/serverboot.go:1278`)
  while whether callers are authenticated is gated on `cfg.publicMode ||
  cfg.identityProvider == ""` (`:1252`), and the §13.10 gateway-fronted
  deployment runs no browser flow while a subject does resolve
  (`spec/13-deployment.md:170`; `web/DESIGN.md:543-546`). The staged replacement
  asserted one state for both postures, which contradicted the proposal's own
  edge-case row for a caller resolving no verified subject on a registry that
  configures an identity provider. DOC-4 now splits the clause by the condition
  it names, keeps the refusal case scoped to a caller that resolves a subject on
  a registry where the rule is live, names the arm board 14i is drawn for, and
  reconciles the retained browser-flow sentence with the empty state that arm
  produces. `web/design/README.md:93` is recorded as needing no edit, because it
  states only that the board carries the panel, which stays true whether or not
  the panel holds rows.
- **The live-rule arm of that replacement stated the read as "the layers §4.6
  admits" for every caller who resolves a subject, dropping the tenant-admin
  arm.** Under the staged §7.3.1 paragraph a caller holding the §4.7.2 admin role
  reads the tenant's whole list on a registry that authenticates callers, so the
  clause contradicted the rule this proposal stages and the three arms DOC-4
  states for `web/DESIGN.md:429-435` in the bullet below it. Applying both
  verbatim would land the two paragraphs of the DESIGN.md layer-panel section
  describing different read sets for the same caller. The live-rule arm now
  states all three arms, and the refusal clause stays scoped to rows the caller
  can see.
