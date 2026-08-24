# Proposal 0013: Build the §13.10 web UI

- Issue: (to be filed)
- Status: Draft
- Date: 2026-08-22

This document stages the proposed spec, code, test, and documentation changes.
It does not modify any spec, code, or doc file. Apply the changes in the edit
sites and testing sections after sign-off.

## Summary

**What changes.**

- The web UI is rewritten in React and gains the §13.10 surfaces it does not
  have: search filters, an artifact viewer that renders markdown and a
  frontmatter property table with links to related artifacts, and a layer panel.
- The browser signs in through the registry. The registry performs the OAuth
  code exchange server-side and returns the resulting token, which is the same
  IdP-signed registry-audience token a device-code CLI obtains, in an
  `HttpOnly` cookie, so no token is reachable from JavaScript. This adds
  sign-in, callback, and sign-out routes and a second location the existing
  `oidc-jwt` verifier reads a token from. It adds no new credential kind and no
  server-side session state. Because the cookie is `HttpOnly` and no shipped
  response echoes the caller, it also adds one unauthenticated read,
  `GET /v1/ui/session`, which reports whether an identity provider is
  configured, whether public mode is engaged, whether the browser flow is
  enabled and at which paths, and the subject the request resolves to. That read
  is what the UI's sign-in control and its rendering rules key on. "The browser
  session" below specifies both.
- The layer write handlers gain owner-or-admin authorization. §7 states an owner
  rule today only for reingest and reorder, S6 adds one to §7.3.1 for
  `register`, `unregister`, `update`, and `restore`, and the code implements it
  for none of them. Today any caller can delete or rewrite another user's
  user-defined layer, and a re-registration under an existing layer's ID
  overwrites it without any owner comparison.
- The built React bundle is committed to the tree so `go build` and `go install`
  keep working from a clean clone with only a Go toolchain, with a CI check that
  rebuilding produces no diff.
- The §6.10 catalog gains two codes. `auth.csrf_invalid` (`403`) covers a
  state-changing request the same-origin gate refuses and a callback whose
  pre-authorization cookie does not validate. `auth.exchange_failed` (`502`)
  covers a callback whose code exchange the IdP answered and refused, which is
  permanent for that request and therefore outside the retryable
  `registry.unavailable`. "The CSRF position" below is the single statement of
  the gate predicate, including which routes it excludes and why.
  `auth.forbidden` is broadened by S6.
  `auth.token_expired` and `auth.untrusted_token` keep their scopes. A session
  cookie carries a token the registry itself obtained rather than one a gateway
  forwarded, so the shipped text that describes either code as reporting a
  forwarded token is restated. The credential-location rule under "The browser
  session" is the single statement of which text that is.

**Fixed decisions.**

- **Authentication is registry-mediated, and the registry stores nothing.** No
  credential is reachable from JavaScript. The alternative of a browser-held
  token under a pure-SPA flow is withdrawn: this proposal newly renders
  author-controlled markdown on the same origin, and a token reachable from
  JavaScript on that origin is the combination the chosen route removes. The
  cookie carries the credential §6.3.3 already accepts rather than one the
  registry mints, so there is no session record, no session lifetime to choose,
  no session key to manage, and no cross-replica state. Revocation before the
  token's own `exp` is not offered, which is the property every credential the
  registry accepts today already has.
- **The layer-ownership gap is closed here rather than filed separately**, because
  the layer panel is the surface that exposes those operations to a browser and
  a panel must not present per-owner scoping as server-enforced while the server
  fails open.
- **The build output is committed.** `go build ./...` and `go install` work from
  a clean clone with no Node toolchain, which is a property the project has today
  and would otherwise lose. The staleness risk this creates is closed by the
  rebuild-is-clean CI check, which is part of the deliverable rather than a
  follow-up.
- **The UI gains no privileged access.** It is a client of the HTTP API and
  reads the catalog and the layer list through the same endpoints an SDK would,
  as §13.10 states. The posture read it adds is unauthenticated and carries no
  privilege, and it exists because the browser can observe neither the
  deployment's identity posture nor its own resolved subject.
- **The implementor does not design the UI.** `web/DESIGN.md` is the design
  brief; a design pass against it produces the layouts and the state treatments.
- **The browser flow is one enablement key with one guard and one mount site.**
  `PODIUM_WEB_UI_AUTH` is not a `PODIUM_IDENTITY_PROVIDER` value, so the
  registry's accepted provider values stay as §13.12 records them
  (`spec/13-deployment.md:468`). Enabling it requires `PODIUM_WEB_UI` on,
  `PODIUM_IDENTITY_PROVIDER=oidc-jwt`, public mode off, and the acquisition
  options §6.3.4 marks required; every other combination fails startup with
  `config.web_ui_auth_unconfigured` from `StartupConfig.Validate`
  (`pkg/registry/server/config_validate.go:87`). The existing public-mode
  exclusion cannot cover this, because it is keyed on
  `PODIUM_IDENTITY_PROVIDER` alone (`spec/13-deployment.md:484`,
  `pkg/registry/server/config_validate.go:88-91`) and reads no web-UI key. The
  shipped web-UI guard does read two of them,
  `PODIUM_WEB_UI` and `PODIUM_WEB_UI_ALLOW_PUBLIC_BIND`, and already requires a
  configured identity provider
  (`pkg/registry/server/config_validate.go:103-108`), so it is the in-file
  precedent C3's conjunct extends. It does not cover browser-flow enablement
  either: it fires only on a non-loopback bind, and it accepts any provider
  value rather than `oidc-jwt`. No shipped guard reads a browser-flow key.
  Because the guard makes the web UI a precondition rather than a
  second enablement axis, "browser flow on, web UI off" is a startup refusal
  rather than an unregistered route, and the routes mount on one validated
  field inside the block that already mounts `/ui/`
  (`internal/serverboot/serverboot.go:1229`).
- Artifacts stay authored in git. The UI is a reader and a layer manager.

**Watch out for.**

- **CSRF is an obligation this panel creates and the proposal specifies it.**
  A credential the browser attaches by itself authenticates any request the
  browser is induced to make, so every layer write becomes forgeable across
  origins. The session cookie is not the only such credential the deployments
  §13.10 blesses put in a browser's hands. The prior review of this proposal
  never produced a finding on it across eight rounds while treating it as
  acknowledged prose, which is how a known gap stays open. "The CSRF position"
  below is the single statement of the gate predicate.
- **Closing the ownership gap changes the authorization behavior of every layer
  write handler**, including the ones the panel does not call. It does not change
  the behavior of a registry that authenticates no caller: the gate is live only
  when an identity provider is configured and public mode is off, which is the
  same deployment-keyed short-circuit the admin gate already takes
  (`internal/serverboot/serverboot.go:1209-1215`). On a live gate an
  unauthenticated caller is refused like any non-owner.
  `test/integration/reingest_pipeline_test.go:87` posts to reingest with no
  credential and keeps passing, because `NewLayerEndpoint` installs a permissive
  default `authAdmin` (`pkg/registry/server/layers.go:174`) and its layer is
  admin-defined, so the admin path authorizes the request. The surface that
  regresses is a user-defined layer driven by a non-owner identity on a registry
  that has an identity provider, and that case has no test today.
- **The key-placement rule is stated once**, under "Where configuration keys go".
  It is easy to restate divergently, because §6.3, §13.10, and §13.12 each look
  like the right home and only one of them is.
- **`GET /v1/layers` is unfiltered.** The panel's role split is presentation over
  a list the server does not scope, so a design that implies otherwise
  misrepresents it. The server-side gate this proposal adds is on writes.
- **An expired session reads differently on the two surfaces.** The meta-tool
  routes return `401` `auth.token_expired`, and the layer endpoints return `403`
  `auth.forbidden`, because they resolve the caller through a resolver that
  discards the verification error
  (`internal/serverboot/identity_verify.go:55-63`). The panel takes the catalog
  read as its expiry signal. "The browser session" states it and G1 corrects the
  design brief to match.
- **No authentication route writes registry state.** Sign-in sets a cookie, the
  callback exchanges a code with the IdP and sets cookies, and sign-out clears
  the cookies the cookie table names it as clearing. None of the three mutates
  the store, and neither does the posture read, so each of them sits outside the
  §13.2.1 write set under that section's existing rule, with no carve-out and no
  precedent invoked. A read-only registry serves them unchanged, so an
  operator sees no authentication outage during a primary outage.

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. §13.10's authentication paragraph, bind-guard
      rationale, web-UI configuration keys, and the browser-flow configuration
      guard, per "The edit sites". The §13.10 authentication paragraph is one of
      the sites the credential-location rule under "The browser session" moves,
      and this step owns it.
      Levels: —. Depends on: —
- [ ] **S2 · spec** — SPEC-2. A new §6.3.4 stating the browser acquisition flow,
      with its pointer from the §6.3 introduction.
      Levels: —. Depends on: S1
- [ ] **S3 · spec** — SPEC-3. §6.3.3's second accepted location for the
      `oidc-jwt` credential, its header-wins precedence rule, the narrowed
      no-token-is-anonymous sentence at `spec/06-mcp-server.md:96`, the restated
      opening clause and sends-no-credential sentence at
      `spec/06-mcp-server.md:92`, and the §2.2 restatement of that clause at
      `spec/02-architecture.md:101`. Each is a site the credential-location rule
      under "The browser session" moves, and this step owns the `spec/` half of
      §6.3.3 and §2.2.
      Levels: —. Depends on: S2
- [ ] **S4 · spec** — SPEC-4. §7's sign-in, callback, and sign-out routes with
      their methods, their paths,
      their cookies, their mount predicate, and their §13.2.1 classification,
      which leaves §13.2.1's own text unchanged, and the posture read
      `GET /v1/ui/session` with its body, its unauthenticated status, its
      web-UI mount predicate, and the same classification.
      Levels: —. Depends on: S2
- [ ] **S5 · spec** — SPEC-5. §11's verification entry for the UI, covering the
      surface-by-obligation matrix below.
      Levels: —. Depends on: S1, S2, S3, S4, S6, S7
- [ ] **S6 · spec** — SPEC-6. §7.3.1's owner-or-admin authorization for the layer
      write handlers, the rescoped manual-reingest trigger row at
      `spec/07-external-integration.md:65`, the rescoped quickstart reingest
      comment at `spec/00-quickstart.md:46`, which carries the same unqualified
      rule over an admin-defined layer, and §7's `auth.forbidden` error
      enumeration.
      Levels: —. Depends on: —
- [ ] **S7 · spec** — SPEC-7. The new `auth.csrf_invalid` and
      `auth.exchange_failed` §6.10 and §6.9
      entries, their `tools/matrix/matrices.go` axis entries, and the §6.10 and
      §6.9 text that the credential-location rule under "The browser session"
      moves for `auth.token_expired` and `auth.untrusted_token`, meaning, for
      each code, whichever of its scope sentence, its §6.9 row, its
      `suggested_action`, and its canonical `message` the rule reaches.
      `auth.token_expired`'s canonical `message` (`spec/06-mcp-server.md:360`)
      is provider-neutral and stands, as the §6.10 and §6.9 edit site records.
      The rule's other `spec/` sites belong to S1 and S3,
      which own them. The `auth.forbidden` broadening is S6's.
      Levels: —. Depends on: S3, S4, S6
- [ ] **G1 · docs** — DESIGN-1. The `web/DESIGN.md` corrections in "The design
      handout".
      Levels: —. Depends on: —
- [ ] **C1 · code** — CODE-1. Owner authorization on the layer write handlers,
      with its `403` tests, its no-identity-provider case, and the
      `registry.unavailable` refusal on a `register` whose existence lookup
      fails.
      Levels: unit, integration, e2e. Depends on: S6, S7
- [ ] **C2 · code** — CODE-2. The browser-flow configuration surface, meaning the
      `Config` and `StartupConfig` fields for the enablement boolean, the
      transaction TTL, and the acquisition values, the `--web-ui-auth` and
      `--web-ui-auth-transaction-ttl` flags on `podium serve`,
      and the `PODIUM_*` reads beside
      `internal/serverboot/serverboot.go:1826-1827`; the sign-in, callback, and
      sign-out routes and
      their two cookies; the posture read `GET /v1/ui/session` and its mount on
      the web UI alone, per "The posture read"; the `oidcJWTVerifier` cookie branch
      (`internal/serverboot/identity_verify.go:201`) together with the twelve
      `internal/serverboot` test call sites its new parameter moves
      (`identity_gateway_integration_test.go`, `identity_gateway_test.go`, and
      `multitenant_integration_test.go`), the CSRF position below, the
      `auth.csrf_invalid` and `auth.exchange_failed` entries in
      `errorCodeRegistry`, and the Go comments, doc comments, and emitted strings
      that the credential-location rule under "The browser session" moves,
      together with `test/e2e/auth_oidc_jwt_test.go:202` whenever the restated
      boot log line breaks the `accepted issuers ` adjacency that assertion
      requires. That
      set spans `pkg/registry/server`, `internal/serverboot`, and
      `pkg/identity`, and the rule together with its recorded command is what
      determines it; none of the `pkg/identity` edits changes behavior or a
      signature, because the cookie branch lives in `serverboot` and
      `OIDCVerifier.Verify` receives a raw token with no knowledge of its
      origin.
      Levels: unit, integration, e2e. Depends on: S1, S2, S3, S4, S7
- [ ] **C3 · code** — CODE-3. The web-UI authentication configuration guard in
      `StartupConfig.Validate`, including its web-UI, `oidc-jwt`, public-mode,
      acquisition-value, and redirect-URI conjuncts, over the fields C2 adds;
      the bind-guard rationale restatements in the same file
      (`pkg/registry/server/config_validate.go:29` and `:99-101`), which the
      §13.10 bind-guard edit site names; and the nested route mount at
      `internal/serverboot/serverboot.go:1229`.
      Levels: unit, e2e. Depends on: S1, C2
- [ ] **B1 · code** — BUILD-1. The React toolchain, the committed bundle, the
      `go:embed` change, `web/web_test.go`, the served-bundle end-to-end
      assertion, the rebuild-is-clean CI check, and the mechanical
      `dangerouslySetInnerHTML` check over the web UI's source tree in the same
      CI job.
      Levels: unit, e2e. Depends on: —
- [ ] **U1 · code** — UI-1. The UI surfaces built against `web/DESIGN.md`,
      including the sanitized markdown rendering path and its sanitizer cases,
      the posture read on load together with the sign-in and sign-out
      affordances G1's sign-in control table gates on it and the rest of the
      posture-keyed rendering rules G1 states,
      and the client half of the CSRF gate, meaning the `X-Podium-CSRF` header
      read from the `__Host-podium_csrf` cookie on every state-changing call the
      panel issues, where the mechanism C2 lands carries a request-side value.
      Levels: unit, e2e. Depends on: B1, C1, C2, G1
- [ ] **D1 · docs** — DOC-1. Every shipped mirror named in "The edit sites" and
      every site under `docs/` that the credential-location rule under "The
      browser session" moves, which is the whole documentation half of "The
      second-location sweep". A site the rule leaves standing is left untouched.
      Levels: —. Depends on: S1, S2, S3, S4, S6, S7
- [ ] **T1 · test** — TEST-1. The manual scenarios, including the S44 rewrite,
      the S44 stack restaging (its Keycloak client registration, its
      password-grant token mint, and its serve invocation), the S45 step-2
      and step-4 rewrites, and the sites under `test/` that the
      credential-location rule under "The browser session" moves, which covers
      both the startup-log text S36 and S44 quote verbatim and S36's restatement
      of the §6.3.3 sends-no-credential sentence.
      Levels: —. Depends on: U1

## The gap

§13.10 specifies the web-UI surfaces (`spec/13-deployment.md:164-168`). The
implementation provides roughly one and a half of them.

| Specified | Built |
|:--|:--|
| Domain browser matching `load_domain`'s structure | yes |
| Search with the same `type` / `scope` / `tags` filters as the SDK and CLI | free-text query only |
| Artifact viewer: body as markdown, frontmatter as a property table, links to extending or dependent artifacts | no; both rendered as raw `<pre>`, no links |
| Layer panel: layers with source, visibility, and `last_ingested_at`; admins register, reingest, and unregister; users manage their own layers under the §7.3.1 cap | absent |

The SPA is 162 lines: `web/app.js` 129, `web/index.html` 20, and `web/style.css`
13. It is vanilla JavaScript with no build step, embedded with `go:embed`
(`web/web.go:12`) and served at `/ui/` by a plain `http.FileServer` with no
middleware (`internal/serverboot/serverboot.go:1229`).

The UI appears in neither §10's build sequence nor §11's verification list. The
tests that cite §13.10 for the UI cover its packaging rather than its surfaces:
`web/web_test.go:11` and `:24` assert that the embedded set carries
`index.html`, `app.js`, and `style.css`, and `cmd/podium/serve_ui_test.go:16`
asserts the mount by requiring the served body to contain
`<title>Podium</title>`, as `test/e2e/server_flag_behavior_test.go:30` does
through the binary. Nothing asserts what any of the specified surfaces in the
table above renders. That is how the specified surfaces and the partial implementation
coexisted without anything failing, and it is why S5 creates the verification
obligation as well as satisfying it.

## The layer-ownership defect

This is a fail-open divergence from spec, and it is closed here because the layer
panel is the surface that exposes these operations to a browser.

§7 specifies owner authorization on some of the write handlers. Manual reingest is
"(admin or layer owner)" (`spec/07-external-integration.md:65`), and reorder is
scoped to "admin-defined layers (admin auth) or your own user-defined layers"
(`:87`). The handlers implement neither half. For `register`, `unregister`,
`update`, and
`restore` no spec sentence states an owner rule at all, so S6 adds one to §7.3.1
before C1 implements it; the only statement of the intended rule today is a code
comment.

`unregister`, `update`, `restore`, and `reorder` gate on `!cfg.UserDefined`
alone (`pkg/registry/server/layers.go:856`, `:494`, `:819`, `:905`), so
authorization runs only for admin-defined layers. When the layer is
user-defined, no check runs and any caller receives `200`. The comment above one
of them states the intended rule, "a user-defined layer belongs to its
registrant (§4.7.2)", and the code does not implement it. `reingest` calls no
authorization function at all.

`register` is the same defect on a path that reads as a creation rather than a
write. `POST /v1/layers` (`pkg/registry/server/layers.go:582`) never loads the
stored layer and never compares an owner: it builds a fresh `LayerConfig` from
the request body and calls `PutLayerConfig` (`:742`), which is an upsert keyed on
`(tenant_id, id)` in every backend (`pkg/store/postgres.go:1195`,
`pkg/store/memory.go:410`). An authenticated non-admin bob who posts
`{"id":"alice-personal", …}` fails `authAdmin`, is promoted to `userDefined`
because he is authenticated (`:611-618`), has `cfg.Owner` set to himself
(`:643-645`), passes the §7.3.1 cap because the count excludes the same ID
(`:680`), and overwrites alice's layer. The same request against an
admin-defined layer ID converts it into bob's user-defined layer with visibility
`users:[bob]`, which removes it from every other caller's view.

The only owner comparisons in the file are the §7.3.1 cap count (`:680`) and the
§8.5 erase filter (`:419`). Neither authorizes a write against the caller.

**What lands.** An owner-or-admin gate on `register`, `unregister`, `update`,
`restore`, `reorder`, and `reingest`, each returning `403` `auth.forbidden`, with
tests asserting the refusal. On `reingest`, which today runs no authorization at
all (`pkg/registry/server/layers.go:946-991`), the gate runs after the layer is
loaded, which is what supplies the owner to compare against, and before
`runIngestAndRespond`, so a refused caller triggers neither a Git fetch nor the
break-glass freeze bypass. The owner arm of the gate reads the stored `Owner`
only where the stored layer is user-defined. Against an admin-defined layer the
rule collapses to admin-only. That collapse is a rule S6 introduces rather than
one §7 states today: the manual-reingest trigger row reads "(admin or layer
owner)" with no layer-class qualifier
(`spec/07-external-integration.md:65`), so as written it admits an
admin-defined layer's stored non-admin owner, and S6 restates its parenthetical
the way `:87` already scopes reorder. The §0 quickstart carries the same
unqualified rule for the same operation over an admin-defined layer
(`spec/00-quickstart.md:46-47`), so S6 restates that comment as well; the
§7.3.1 edit site states both restatements. The qualifier carries weight rather
than restating the obvious: an admin-defined layer carries a stored `Owner` as
well, assigned from the request body on the admin-defined branch of `register`
(`pkg/registry/server/layers.go:659`) and patchable on the same branch of
`update` (`:547-549`, `docs/reference/http-api.md:329`), so an owner arm written
without the qualifier would admit whichever non-admin subject that field names.
On `register` the gate is conditional on the request naming an existing layer in
the tenant. The §7.3.1 edit site states the arms of that rule, and this section
adds nothing to them.

The existence lookup covers soft-deleted layers, and that is load-bearing rather
than incidental. `PutLayerConfig` upserts on `(tenant_id, id)` and writes
`deleted_at` from the config it is given, so a registration under a tombstoned
layer's ID occupies that key and clears the tombstone
(`pkg/store/postgres.go:1195`, `:1213`, `pkg/store/sqlite.go:750`,
`pkg/store/memory.go:406-411`), while every backend's `GetLayerConfig` filters
`deleted_at` out (`pkg/store/postgres.go:1236`, `pkg/store/sqlite.go:771`,
`pkg/store/memory.go:417-421`). A gate that looked only through `GetLayerConfig`
would therefore take the names-no-stored-layer arm for the whole §8.4 recovery
window (`spec/08-audit-and-observability.md:52`), and bob re-registering alice's
unregistered layer ID would become its owner, clear the tombstone, and leave
alice's `restore` answering `404` "no recoverable layer" with her artifacts still
tombstoned. That is the takeover the gate exists to close, in the window where it
is unrecoverable. No shipped handler determines existence over both sets:
`restore` reads a tombstoned layer's `Owner` and `UserDefined` through a
`ListDeletedLayerConfigs` scan alone (`pkg/registry/server/layers.go:799-816`),
because its target is tombstoned by definition.

**IMPLEMENTOR'S CHOICE:** the store call sequence that implements the existence
lookup on `register`. Any answer determines existence over both live layers and
soft-deleted layers still inside the §8.4 recovery window
(`spec/08-audit-and-observability.md:52`); a call that errors refuses the
registration with `500` `registry.unavailable` and writes nothing, because a
store failure establishes neither that no layer holds the ID nor who owns the
layer that does, following the idiom `unregister` and `reingest` already use for
the same discrimination (`pkg/registry/server/layers.go:847-855`) and `restore`'s
scan arm (`:802-806`) rather than `update`'s collapse of every `GetLayerConfig`
failure into `404` (`:487-491`), which is safe there only because not-found
refuses on `update` and admits on `register`; the lookup and the owner comparison
both run ahead of the `req.UserDefined` short-circuit at
`pkg/registry/server/layers.go:610-611`, so a request body that asserts
`user_defined` cannot skip the gate the way it skips `authAdmin` today; and the
arms the lookup feeds are exactly those the §7.3.1 edit site states, so an arm
that site does not carry is a defect in the implementation.

**IMPLEMENTOR'S CHOICE:** whether the gate needs an atomicity guarantee beyond
what `PutLayerConfig` gives today, for two registrations racing under the same ID
between the existence lookup and the upsert. Any answer is stated once, in this
section, and if it is that the shipped upsert's behavior is accepted unchanged,
that answer is recorded here as a decision with its reason, so a later reviewer
does not rediscover it as a further arm of the lookup.

The gate is live whenever an identity provider is configured and public mode is
off. On a registry that authenticates no caller, which is the default standalone
and public-mode posture and the posture §13.10's own web UI targets, the write is
admitted. That is the same deployment-keyed short-circuit the admin gate already
takes (`internal/serverboot/serverboot.go:1213`), and it is stated the way §4
states the parallel carve-out for the re-embed endpoint: "Configuring an identity
provider makes the gate live, whether or not the registry verifies callers
itself" (`spec/04-artifact-model.md:760`). On a live gate a caller who resolves no
subject is refused with `403` `auth.forbidden` like any non-owner, which matters
because §6.3.3 makes a request anonymous rather than rejected while the issuer
JWKS is unreachable (`spec/06-mcp-server.md:98`). The carve-out is what keeps
§13's statement that "the layer-management and erase endpoints admit any request"
(`spec/13-deployment.md:33`) true, keeps standalone and standard behaving
identically on the same handler, and keeps a layer registered with
`podium layer register --user-defined --owner alice` manageable by the local
operator. That invocation is the one that produces a user-defined layer with a
stored owner on a registry that authenticates no caller: the CLI sends `owner`
only inside the `--user-defined` branch (`cmd/podium/layer.go:224-227`), and the
handler falls back to the request body's `owner` when no authenticated identity
resolves (`pkg/registry/server/layers.go:643-646`,
`docs/reference/cli.md:423`).

`test/integration/reingest_pipeline_test.go:87` keeps passing unchanged. It
builds the endpoint with the bare `NewLayerEndpoint`, whose default `authAdmin`
returns nil for every caller (`pkg/registry/server/layers.go:174`), and its layer
is admin-defined, so the admin path authorizes the request.

**IMPLEMENTOR'S CHOICE:** whether the owner comparison reads the caller's subject
through the same helper the cap count uses or through the request-identity
accessor the admin gate uses. Any answer compares against the verified subject
rather than a client-supplied field, returns `403` `auth.forbidden` rather than
`404`, and leaves the admin path able to act on any layer in the tenant.

## The browser session

The registry mints no credential and keeps no session record. The session cookie
carries the token §6.3.3 already accepts.

This section is the single statement of the pre-authorization transaction
contract: the HTTP method each route answers on, what the sign-in redirect
carries, what `__Host-podium_auth` holds, what the callback compares and in what
order, what each outcome sets and clears, and which code each refusal returns.
Every other site in this proposal cites the contract by name and states only
what is local to that site.

**The route methods.** Sign-in and the callback answer on `GET`. Each is a
top-level navigation, which is what makes `SameSite=Lax` deliver
`__Host-podium_auth` on the identity provider's redirect back to the callback,
and neither can carry a request-side CSRF value a page set, which is why "The
CSRF position" excludes both by name as well as by method. Sign-out answers on
`POST`. It is a non-navigation call the page issues, which is what lets it carry
the `X-Podium-CSRF` header where the landed mechanism requires one, and `POST`
is what puts it inside the §6.3.4 gate under that section's method predicate.
The methods are fixed here rather than left to the implementor because the mux
registration, the `docs/reference/http-api.md` Authentication section, the §7
entry, the S45 step-4 rewrite, and the new sign-in scenario all have to spell
them identically, and because the sign-out method decides whether the §6.3.4
gate covers sign-out at all.

**What the cookie holds.** The callback exchanges the authorization code
server-side for an access token whose `aud` is `PODIUM_OAUTH_AUDIENCE`, which is
the token a device-code CLI also presents, and returns it in the
`__Host-podium_session` cookie. The registry keeps no session record, mints no
session identifier, and holds no session key. This adds no credential to §6.3.3:
the cookie carries the same IdP-signed JWT the `oidc-jwt` provider already
verifies on every request (`spec/06-mcp-server.md:96-100`), which is why the
access token is what the cookie carries rather than the ID token. A deployment
whose IdP issues opaque access tokens cannot use the browser flow, which is the
constraint the shipped `oidc-jwt` path already imposes on a gateway-forwarded
token.

**The cookie table.** The browser flow sets the cookies below and no others. This
table is the single statement of the cookie contract. Every other site in this
proposal cites it by name and states only what is local to that site.

| Cookie | Prefix | `HttpOnly` | `Secure` | `Path` | `SameSite` | `Max-Age` | Set by | Cleared by |
|:--|:--|:--|:--|:--|:--|:--|:--|:--|
| `__Host-podium_session` | `__Host-` | yes | yes | `/` | `Lax` | absent | the callback | sign-out |
| `__Host-podium_auth` | `__Host-` | yes | yes | `/` | `Lax` | the configured transaction TTL | sign-in | the callback, on every outcome; sign-out |
| `__Host-podium_csrf`, present only where the chosen mechanism carries a request-side value | `__Host-` | no | yes | `/` | `Lax` | absent | the callback | sign-out |

The `__Host-` prefix is the browser-enforced binding control: it forbids a
`Domain` attribute and forces `Secure` and `Path=/`, so no sibling host can plant
any of these cookies, and it is why none of them needs a server-side signing key.
The prefix places no constraint on `HttpOnly`, so the page-readable CSRF row
keeps that anti-planting property while the page reads the value. `Secure` is
unconditional, so the browser flow requires the registry to be reached over an
`https` origin, whether directly or through the gateway that terminates TLS
(`spec/06-mcp-server.md:112`), or over a loopback address. That is a property of
the registry's own origin rather than of the issuer URL, so the startup guard
below carries it as its own conjunct. `SameSite=Lax` rather than `Strict` is
forced by `__Host-podium_auth`, which has to survive the IdP's cross-site
redirect back to the callback.

- `__Host-podium_auth` holds the pre-authorization transaction: the `state`, the
  `nonce`, and the PKCE `code_verifier` the sign-in route mints. Its `Max-Age`
  bounds the sign-in window at 10 minutes by default, tunable by
  `--web-ui-auth-transaction-ttl` / `PODIUM_WEB_UI_AUTH_TRANSACTION_TTL` per the
  key-placement rule under "Where configuration keys go". The callback clears it
  on every outcome, success or refusal, which is what makes the transaction
  single-use, and sign-out clears it as well so a sign-out mid-transaction leaves
  no cookie behind.
- `__Host-podium_session` holds the access token. Its lifetime is bounded
  server-side by the token's own `exp`, set by the IdP, so the registry chooses
  no second lifetime and the row carries no `Max-Age`. `__Host-podium_csrf`
  carries no `Max-Age` for the same reason and it is load-bearing: the callback
  is the only route that sets it, so a CSRF cookie that expires before the
  session cookie leaves a browser holding a live session and no proof, every
  panel write returning `403` `auth.csrf_invalid` with no recovery short of
  re-running sign-in, and the panel unable to detect the condition because the
  catalog read that reports expiry still succeeds. Both cookies therefore end
  with the browser session, and both are cleared together by sign-out.
- `__Host-podium_session` and `__Host-podium_auth` are the session mechanism and carry no CSRF
  role.

**The sign-in redirect.** The sign-in route mints the `state`, the `nonce`, and
the PKCE `code_verifier`, returns the three in `__Host-podium_auth`, and
redirects the browser to `PODIUM_WEB_UI_OAUTH_AUTHORIZATION_ENDPOINT` with
`client_id` set to `PODIUM_WEB_UI_OAUTH_CLIENT_ID`, `redirect_uri` set to
`PODIUM_WEB_UI_REDIRECT_URI`, `response_type=code`, a scope set containing
`openid`, and the `state`, the `nonce`, and the PKCE challenge derived from that
verifier. The `nonce` travels in the authorization request because an ID token
carries a `nonce` claim only when the request sent one, which is what makes the
callback's binding check reachable.

**The callback order.** The callback reads `__Host-podium_auth` and compares the
returned `state` against it before inspecting anything else in the query, so a
callback whose `state` is absent or unequal is refused whatever else that query
carries. It then branches: a query carrying the IdP's `error` parameter runs no
exchange, and a query carrying a `code` is exchanged server-side at
`PODIUM_WEB_UI_OAUTH_TOKEN_ENDPOINT` with the `code_verifier` the cookie holds
and the configured client credential. The `nonce` of the ID token that exchange
returns is compared against the cookie's `nonce` after the exchange, because the
ID token does not exist until the token endpoint answers. On success the callback
returns the access token in `__Host-podium_session`. It clears
`__Host-podium_auth` on every outcome, and "What happens when it does not fire"
below states what each outcome sets and clears and which code each refusal
returns.

Neither value is signed or encrypted by the registry, because each is either
compared against something the IdP returns (`state` against the callback query
parameter, `nonce` against the ID token claim, and `code_verifier` against the
PKCE challenge the IdP validates) or is itself a JWT the issuer signed.
Tampering breaks only the tamperer's own flow.

**Where the cookie is read.** `oidcJWTVerifier`
(`internal/serverboot/identity_verify.go:201`) gains a `sessionCookie bool`
parameter, passed the browser-flow enablement field at its one production call
site (`internal/serverboot/serverboot.go:1135`); the `internal/serverboot` tests
that construct the function directly pass `false` and keep their current
behavior. When the configured token header
carries no bearer credential and that parameter is true, the raw token is read
from `__Host-podium_session`. Everything after that is unchanged:
`verifier.Verify`, the §6.3.1 `IdpGroupMapping`, the `ErrKeySetUnavailable`
fail-closed-to-anonymous arm, and the `layer.Identity` construction. That one
function is installed as the server's identity verifier
(`internal/serverboot/serverboot.go:1136`, `pkg/registry/server/server.go:202`),
reused as the §7.3.1 layer endpoint's resolver (`:1198`, `:1207`), read by the
admin-gate closure (`:1208-1216`), and read by the tenant router (`:1170`), so
one edit reaches every consumer and there is no second resolution site. The
consumers differ in what they do with a verification error rather than in how
they resolve one: the meta-tool identity middleware maps the error to a status
and a §6.10 code (`pkg/registry/server/identity_verify.go:39-55`, `:87-94`),
while `layerIdentityResolver` discards it and returns the anonymous-public
caller (`internal/serverboot/identity_verify.go:55-63`), because
`WithIdentityResolver` carries no error channel
(`pkg/registry/server/layers.go:187`). "What happens when it does not fire"
states what each surface returns.

**Precedence is branch order.** The configured token header is read first, and
the cookie only when that header carries none. The two are never merged. A
gateway that authenticated the request is the authority in that deployment, and
a registry-set cookie must not override it. Both paths verify through the same
`OIDCVerifier`, so the resolved subject is JWKS-verified either way.

**Sign-out** clears every cookie whose row in the cookie table names sign-out as
its clearer, and there is nothing else to clear.

**Enablement, guard, and mount.** `--web-ui-auth` / `PODIUM_WEB_UI_AUTH` is one
boolean. The acquisition values are `PODIUM_WEB_UI_OAUTH_CLIENT_ID`,
`PODIUM_WEB_UI_OAUTH_CLIENT_SECRET`, `PODIUM_WEB_UI_REDIRECT_URI`,
`PODIUM_WEB_UI_OAUTH_AUTHORIZATION_ENDPOINT`, and
`PODIUM_WEB_UI_OAUTH_TOKEN_ENDPOINT`.
`--web-ui-auth-transaction-ttl` /
`PODIUM_WEB_UI_AUTH_TRANSACTION_TTL` carries the sign-in window. All are startup
configuration, read once beside
`internal/serverboot/serverboot.go:1826-1827` and never changed at runtime.

`PODIUM_WEB_UI_OAUTH_AUTHORIZATION_ENDPOINT` and
`PODIUM_WEB_UI_OAUTH_TOKEN_ENDPOINT` are the IdP URLs the flow calls: the
sign-in route redirects the
browser to the authorization endpoint, and the callback posts the code, the
`code_verifier`, and the client credential to the token endpoint. Neither is
derivable from what the registry reads today. `PODIUM_OAUTH_ISSUER` yields
neither, because the registry's discovery read parses only `jwks_uri` and
`access_token_issuer` from the discovery document
(`pkg/identity/oidc_jwt.go:360-376`). Extending that read would add no outbound
call: `serverboot` already primes the verifier at boot and refuses to start when
the discovery document or the JWKS is unreachable
(`internal/serverboot/serverboot.go:1132`, `pkg/identity/oidc_jwt.go:172`). The
objection is that the parsed field set is a shipped `pkg/identity` contract this
proposal does not otherwise touch, and that an IdP whose discovery document omits
`authorization_endpoint` or `token_endpoint` would become a new class of startup
refusal for every `oidc-jwt` registry rather than only for one that enables the
browser flow. Configuring the two
endpoints explicitly is also the shipped pattern for the device-code client,
which takes `--issuer` / `PODIUM_OAUTH_AUTHORIZATION_ENDPOINT` and `--token-url`
/ `PODIUM_OAUTH_TOKEN_URL` (`docs/reference/cli.md:112-113`,
`cmd/podium/login.go:35`). `PODIUM_OAUTH_AUTHORIZATION_ENDPOINT` itself is the
device-code endpoint, read by the CLI and the MCP bridge
(`cmd/podium/login.go:35`, `cmd/podium-mcp/main.go:277`) and carried on §6.3's
`oauth-device-code` options list (`spec/06-mcp-server.md:42`). The browser flow
does not read it, and the guard does not accept it in place of
`PODIUM_WEB_UI_OAUTH_AUTHORIZATION_ENDPOINT`, so an operator who sets only the
device-code key gets a startup refusal naming the key the flow needs rather than
a redirect to nowhere.

`StartupConfig.Validate` (`pkg/registry/server/config_validate.go:87`) requires,
when the flow is enabled, that the web UI is on, the identity provider is
`oidc-jwt`, public mode is off, every acquisition value is non-empty, and
`PODIUM_WEB_UI_REDIRECT_URI` is an `https` URL or a loopback `http` URL, and
otherwise fails startup with `config.web_ui_auth_unconfigured` naming the failed
conjunct. The public-mode conjunct runs after the shipped public-mode exclusion
rather than ahead of it, so a registry configured for public mode with
`oidc-jwt` keeps failing with `config.public_mode_with_idp` as it does today
(`pkg/registry/server/config_validate.go:87-90`); the combination the new guard
refuses is public mode with no identity provider, and there it is the `oidc-jwt`
conjunct that fails. The redirect-URI conjunct is the secure-origin requirement the `__Host-`
prefix imposes: a browser neither stores nor returns a `Secure` cookie on a
non-secure origin, so a registry reached over plain `http` on a non-loopback
address would set the pre-authorization cookie, receive no cookie back on the
callback, and refuse every sign-in with `403` `auth.csrf_invalid` and no
diagnosis. The shipped `oidc-jwt` issuer guard
(`internal/serverboot/identity_verify.go:268`) does not cover this, because it
constrains `PODIUM_OAUTH_ISSUER`, which is the IdP's discovery URL rather than
the origin the browser reaches. The routes mount inside the existing
`if cfg.webUI` block
(`internal/serverboot/serverboot.go:1229`) under a nested enablement check. The
nesting is the web-UI conjunct; the `oidc-jwt` conjunct is not restated at the
mount, because `validate()` runs before the wiring and no booted process can
falsify it.

**The posture read.** `GET /v1/ui/session` is an unauthenticated read that
reports the deployment posture and the caller's own resolved subject. The UI
needs both and can observe neither: the session cookie is `HttpOnly`, so the
page cannot tell whether it holds one; `GET /v1/layers` is unfiltered and echoes
no caller (`pkg/registry/server/layers.go:770-777`); the catalog responses carry
no caller identity; `/healthz` reports the §13.2.1 mode banner alone
(`pkg/registry/server/server.go:656-673`); and `/v1/quota` reports tenant,
limits, and usage alone (`pkg/registry/server/quota.go:9-26`). Without the read
the UI has no source for whether to offer sign-in, and each rendering rule G1
states would key on server configuration the browser cannot see.

- **The body.** `identity_provider_configured` and `public_mode` are booleans.
  `browser_auth` is an object carrying `enabled`, and, when `enabled` is true,
  `sign_in_path` and `sign_out_path`, which are the paths the mux registers, so
  no authentication route path is spelled inside the bundle. `subject` is the
  verified subject of the request that asked, present only when one resolves.
  The response carries no other field, and in particular no issuer, client
  identifier, endpoint, or other configuration value.
- **The state it reads, and where that state is set.**
  `identity_provider_configured` and `public_mode` read the shipped
  `identityProvider` and `publicMode` fields, and `browser_auth` reads the
  enablement field C2 adds. Each is set once at boot from
  the flags and `PODIUM_*` variables (`internal/serverboot/serverboot.go:1826`)
  and never changed at runtime, and the browser-flow fields are the ones the C3
  guard validates before the wiring runs. `subject` reads the per-request identity that
  `layerIdentity` resolves (`internal/serverboot/serverboot.go:1198`), which is
  the resolver the layer endpoint already uses and which returns the
  anonymous-public caller when verification fails, so an expired or untrusted
  session cookie reports no subject and the panel's expiry signal stays the
  catalog read. Nothing sets or clears state on this path: the handler reads no
  store and writes none, so §13.2.1 leaves it outside the write set for the same
  reason the authentication routes sit outside it, and a read-only registry
  serves it unchanged.
- **Where it mounts.** Inside the `if cfg.webUI` block beside `/ui/`
  (`internal/serverboot/serverboot.go:1229`), gated on the web UI alone rather
  than on the browser flow, because a registry serving the UI with no browser
  flow is exactly the deployment whose page has to learn not to offer sign-in.
  A registry started without `--web-ui` serves no UI, and the path is never
  registered, so a request for it falls through to the catch-all and is answered
  as any unregistered path is on that deployment, per "What happens when it does
  not fire" below. The S45 stack configures no identity provider, so what it
  sees is `404`.
- **Its callers.** The UI, on load. No CLI, SDK, or MCP caller reads it, it
  changes no existing endpoint, and it adds no credential, cookie, error code,
  SPI method, or store method. It is an unauthenticated read, so the UI gains no
  privileged access and every other call it makes is still the call an SDK would
  make.
- **What happens when it does not fire.** When the read fails or answers `404`,
  the UI renders its anonymous presentation: no sign-in control, no sign-out
  control, and the layer panel rendered with its write operations, where a
  refused write returns `403` `auth.forbidden` and the panel presents the
  not-permitted state. The Surfaces case under U1 drives that, and the
  end-to-end case under C2 asserts the body on a registry with the flow disabled
  and on one with it enabled.

**No shared state, and that is a requirement rather than a saving.** §13.1's
reference topology is a "Stateless front-end: 3+ replicas behind a load balancer"
(`spec/13-deployment.md:5`). A store-backed pre-authorization transaction forces
the sign-in and the callback onto the same replica or onto a shared write; the
cookie travels with the browser, so any replica serves the callback. Nothing in
this proposal adds a `store.Store` method (`pkg/store/store.go:345`), a table in
`Memory`, `Postgres`, or `SQLite`, an `additiveColumns` row
(`pkg/store/schema_migrate.go:47`), a `pkg/store/storetest` conformance case, a
retention sweep, or a §9.1 `RegistryStore` row.

**Revocation is expiry.** Sign-out clears the cookies, so the browser stops
presenting the credential, and the token stays valid at the IdP until it expires.
That is the model §6.3.3 already states for the credential the registry verifies:
the verification paragraph checks the signature, `iss`, `aud`, and the
`exp`/`nbf` window and consults no revocation list
(`spec/06-mcp-server.md:98`). The registry operates no revocation list for a
forwarded token today and gains none here. There is no silent refresh: an expired
session re-runs the sign-in redirect, which completes without a prompt while the
IdP session is live.

**Why there is no session store.** The only thing a stored record would buy is
revocation before `exp`, which the registry offers for no credential today. Both
`oidc-jwt` and `injected-session-token` tokens are valid until they expire, with
no revocation list. Adding it for the browser credential alone would make it the
one revocable credential, with no spec basis, at the cost of a `store.Store`
method set across `pkg/store/memory.go`, `sqlite.go`, `postgres.go`,
`schema_migrate.go`, and the `pkg/store/storetest` conformance suite, a §9.1
`RegistryStore` row on a mirrored surface, a §13.1 topology component, and a
read-only classification for two more routes.

**What happens when it does not fire.** With the flow disabled the three paths
are never registered, so a request for one of them reaches the catch-all
(`internal/serverboot/serverboot.go:1239`) and is answered by the meta-tool
handler rather than by an authentication route, and `oidcJWTVerifier` ignores
the cookie, so a stale cookie resolves anonymous-public rather than
authenticating anyone.

The status that request receives is the deployment's rather than the route's,
and this paragraph is the single statement of it. The meta-tool identity
middleware runs ahead of the inner mux (`pkg/registry/server/server.go:429`,
`pkg/registry/server/identity_verify.go:44-51`) and exempts only `/healthz`,
`/readyz`, and `/scim/` (`:73-80`). Where the configured verifier refuses the
request, the middleware answers before any path is matched: under
`injected-session-token` a request carrying no runtime-signed token fails
verification (`internal/serverboot/identity_verify.go:27-29`,
`pkg/identity/runtime.go:137-138`) and the middleware returns `401`
`auth.untrusted_runtime` (`pkg/registry/server/identity_verify.go:118-119`), and
under `oidc-jwt` a request carrying an invalid bearer token returns `401`
`auth.untrusted_token`. Where no verifier refuses the request, which is a
registry that configures no identity provider and an `oidc-jwt` registry whose
request carries no bearer credential
(`internal/serverboot/identity_verify.go:204-205`), the response is `404`. The
unmounted route is unreachable under every one of those dispositions, so the
mount predicate holds either way, and every site in this proposal that asserts
`404` on an unmounted authentication route names the stack it runs on and that
stack configures no identity provider.

With the flow enabled, the callback's own refusals are the following. A callback
whose `__Host-podium_auth` cookie is absent, expired, or does not match the
returned `state`, and a callback whose exchanged ID token carries a `nonce` other
than the one that cookie holds, are each refused with `403` `auth.csrf_invalid`,
set no session cookie, and clear the pre-authorization cookie. The `nonce`
comparison is a separate check from the `state` comparison: `state` binds the
callback to the browser that started the transaction, and `nonce` binds the ID
token to that same transaction. A callback whose `__Host-podium_auth` cookie and
`state` validate but whose query carries the IdP's `error` parameter rather than
a `code`, which is what the authorization endpoint returns when the user declines
the consent prompt or the IdP refuses the authorization request, runs no
exchange: it clears the pre-authorization cookie, sets no session cookie, and
returns the browser to the web UI root at `/ui/` without establishing or
replacing a session. It leaves any `__Host-podium_session` cookie the browser
already holds intact, which the cookie table's clearing column already states,
so a cancelled first sign-in lands at `/ui/` anonymous and a cancelled
re-sign-in lands there still signed in under the earlier session. Re-running
sign-in is the recovery for that condition, so it takes no error code and in
particular not `auth.exchange_failed`, whose `retryable: false` envelope and
client-credential remediation would report a user decision as an operator
misconfiguration and would emit a `5xx` on the most common outcome of a sign-in
attempt. The `state` comparison runs first, so an error redirect that carries no
matching pre-authorization cookie is refused with `403` `auth.csrf_invalid` like
any other callback that fails that comparison. A cookie carrying an expired
token is refused with `401` `auth.token_expired`, and one failing signature,
`iss`, or `aud` with `401` `auth.untrusted_token`, both from the shipped verifier
path, on the routes the meta-tool identity middleware wraps, which are the paths
the boot mux hands to the catch-all
(`internal/serverboot/serverboot.go:1239`, `pkg/registry/server/server.go:429`).
The §7.3.1 layer endpoints are mounted ahead of that catch-all
(`internal/serverboot/serverboot.go:1220-1221`) and resolve the caller through
`layerIdentityResolver`, which discards the verification error and returns the
anonymous-public caller (`internal/serverboot/identity_verify.go:55-63`), so a
layer write carrying an expired or untrusted session cookie resolves no subject
and is refused with `403` `auth.forbidden` by the owner gate C1 adds, while
`GET /v1/layers` still returns the unfiltered list. This proposal leaves that
resolver as it is. Its discard predates the browser flow and governs every
credential the layer endpoint accepts, so surfacing the error there would re-code
the refusal a gateway-forwarded caller receives as well, which is a change to a
shipped surface this proposal does not otherwise touch. The panel therefore
learns that a session has expired from a catalog read, which returns `401`
`auth.token_expired`, rather than from a write, and G1 corrects the brief's
session-expiry transition to state that. While the JWKS is unreachable the
request is anonymous. An IdP the registry
cannot reach for the code exchange, or one that answers the token endpoint with a
`5xx`, is a transient failure against a dependency the registry called, so it is
refused with `registry.unavailable`, whose shipped scope and retryable framing
cover exactly that case (`pkg/registry/server/error_envelope.go:26`,
`docs/reference/error-codes.md:158`). An IdP that reaches the registry and
refuses the exchange with an OAuth error such as `invalid_grant`, or refuses it
because `PODIUM_WEB_UI_OAUTH_CLIENT_SECRET` is wrong, is a permanent failure for
that request, so `registry.unavailable` is not available to it: every retry fails
identically and the `retryable: true` envelope names no useful action. It is
refused with `502` `auth.exchange_failed`, which S7 stages as a non-retryable
code. On every one of these outcomes the callback sets no session cookie
and still emits the clearing `Set-Cookie` for `__Host-podium_auth`. No error code
is added beyond `auth.csrf_invalid` and `auth.exchange_failed`, and no shipped
envelope entry is re-scoped.

**What the mechanism does not change.** The credential is unchanged, so §6.3.1
tenant selection keeps reading the verified `org_id` claim,
`auth.tenant_unknown` keeps populating `details.token_org_id`,
`auth.token_expired` and `auth.untrusted_token` keep their scopes as
authentication failures on the same credential, and §6.3.3's
anonymous-while-JWKS-unreachable rule keeps applying. No error code is re-scoped
and none is added. What moves is text: every shipped sentence that says this
credential arrives forwarded by a gateway is false for one of its two accepted
locations, or names a remediation a browser cannot perform. The rule below is
the single statement of which text that is.

**The credential-location rule.** Every other site in this proposal cites this
rule by name and states only what is local to it. A site is restated when a
request whose token arrives in `__Host-podium_session` reaches it and the text is
then false or names an action a browser cannot take. A site scoped to the header
location or to the gateway deployment stands as written, because the browser flow
removes neither and the header-wins precedence rule keeps the gateway account
true where it is labelled as such. A site that documents the verification
configuration itself, meaning the issuer key, its code mirror, and the audience
startup guard, also stands, because the registry verifies the same `iss` and
`aud` claims on the token in either accepted location. A site that states the
scope or the emitted text of an `auth.*` error code moves even when it names
those same claims, because the code's scope is what the amendment widens. A site
that states how the registry derives a request's tenant also stands, because the
cookie carries the same token and the tenant still comes from that token's
verified `org_id` claim.

Four sites are worked through below, one for each restatement pattern the rule
produces, so a disposition can be checked against an example. They illustrate the
rule rather than bound it.

| Site | What it says today | Staged by |
|:--|:--|:--|
| `spec/06-mcp-server.md:92` (the opening clause and the sends-no-credential sentence) | both providers are "registry-process identity providers for a deployment that runs the registry behind a gateway that has already authenticated the caller", which is false for a directly reachable `oidc-jwt` registry running the browser flow, and the same line closes "A Podium client behind such a gateway sends no credential of its own", whose antecedent the restatement removes for `oidc-jwt`. It is the authoring source of the `pkg/identity/registry.go:69-70` comment and of `docs/deployment/gateway-delegated-identity.md:11`, so the three move together. Restated so `oidc-jwt` is described as a registry-process provider for a deployment where the registry verifies the caller's token itself, whether a gateway forwarded it or the registry obtained it through the §6.3.4 exchange, with `trusted-headers` alone keeping the fronting-gateway requirement and the sends-no-credential sentence scoped to a client behind a gateway under either provider. `:94`, on tenant derivation, stands | S3 |
| `spec/06-mcp-server.md:366` | `auth.untrusted_token`'s scope: "a forwarded `oidc-jwt` token". The sentence states the code's scope rather than one provider's verification path, so it moves even though the claims it names are verified identically in either location | S7 |
| `docs/deployment/integrations.md:85` | a closed acquisition enumeration for the directly reachable arrangement the browser flow runs in: "Callers obtain that token by completing the CLI's device-code flow". Restated so a CLI, an SDK, or another API client obtains the token through the device-code flow and, on a registry that enables the browser flow, a browser obtains it through the §6.3.4 exchange, which the registry returns in `__Host-podium_session` | D1 |
| `docs/deployment/progressive-adoption.md:57` | the no-token-is-anonymous rule, scoped to the provider: "Under `oidc-jwt` a request carrying no token is anonymous rather than rejected, so it resolves to public visibility only". Narrowed with the same browser-flow conjunct the `spec/06-mcp-server.md:96` authoring source gains: under `oidc-jwt` a request carrying no bearer credential in the configured token header is anonymous, and where the browser flow is enabled it is anonymous only when it also presents no valid `__Host-podium_session` cookie. The conjunct is required rather than decorative, because a registry with the flow disabled reads no cookie, so a stale `__Host-podium_session` sent to it resolves anonymous | D1 |

**IMPLEMENTOR'S CHOICE:** which sites the sweep moves. The moved set is every hit
of the recorded command below to which the rule above applies, determined at
implementation time rather than fixed here. A site describing one provider's own
mechanism, the verification configuration, or tenant derivation stands. A site
stating the scope or the emitted text of an `auth.*` code moves. Widening a
standing site is a defect, because it asserts a change this proposal does not
make, and a widened tenant-derivation site is worse still, because the tenant
selector bounds §4.7 isolation. The applied change is complete when re-running
the command and applying the rule leaves no hit undispositioned.

The sweep is reproduced rather than re-derived. It is the hits of

    grep -rniE "forward|gateway|anonymous|obtain (that|the) (access )?token|acquires a token|acquires? and caches|acquired itself|device-code flow|authenticates? (once )?(with|through)" \
      spec pkg/identity pkg/registry/server internal/serverboot docs/reference \
      docs/deployment docs/getting-started/how-it-works.md \
      test/manual-validation.md

filtered by the rule above, discarding the hits that use the word for something
else: forward compatibility, forward slashes, a schema migrated forward in place,
the audit `X-Forwarded-User` attribute, and the MCP bridge forwarding meta-tool
calls (`spec/06-mcp-server.md:5`). The pattern carries `anonymous`, the
acquisition verbs, and the device-code and authentication phrasings beside the
two gateway words, because a site can state the no-token-is-anonymous rule or
close the token-acquisition path without using either gateway word, and the
acquisition sentence is worded differently on each page that carries it. The spec
scope is the whole of `spec/` rather than §6 alone, because
`spec/02-architecture.md:101` restates the §6.3.3 clause outside it. The command
returns more hits than the rule moves, so the rule is what disposes of the
remainder.

`spec/06-mcp-server.md:386` and its mirror
`pkg/registry/server/error_envelope.go:74` stand as a named exception to the
rule's error-code clause, and the rule names no other. That
`auth.tenant_unknown` remediation reads "Provision the organization as a tenant,
or forward a token whose org_id claim names an existing tenant." Unlike
`auth.token_expired`'s remediation it does not enumerate a path per provider, and
its first clause names the action every deployment takes, so a browser session is
not left without one. The entry's scope and its `details.token_org_id` field stay
accurate for a session-authenticated request, so §6.10's `auth.tenant_unknown`
and this string are both untouched.

**IMPLEMENTOR'S CHOICE:** none of the above. The package home is
`pkg/registry/server`, alongside the layer endpoint it resembles, mounted from the
boot mux beside `/ui/` (`internal/serverboot/serverboot.go:1229`). The route paths
remain the choice recorded under "The edit sites".

## The spec amendment

The domain browser, the search filters, and the artifact viewer need no spec
change: §13.10 specifies them in enough detail to build against, and their
endpoints exist. The amendment is the authentication story, and it is larger than
a single sentence.

### Where configuration keys go

This rule is stated once here, and every edit site below refers to it rather than
restating it.

§6.3 documents options per identity provider rather than per consumer: the
`Options:` list sits on the provider bullet (`spec/06-mcp-server.md:42`) and the
CLI sub-bullet at `:46` carries none of its own. The browser flow's
**acquisition** options therefore go on the new §6.3.4 entry's own `Options:`
list.

The **registry-process** keys go in §13.10 beside `PODIUM_WEB_UI`
(`spec/13-deployment.md:163`), which is where the shipped web-UI keys are
documented and the only place `PODIUM_WEB_UI` appears in `spec/`.

They do **not** go in the §13.12 identity table. Its introducing sentence scopes
it to the registry-process variables the gateway-delegated and
`injected-session-token` providers introduce (`spec/13-deployment.md:470`), and
its closing sentence enumerates the `identity_provider:` config-file object
(`spec/13-deployment.md:482`). A web-UI key is introduced by neither provider, so
a row there makes the first sentence false and a config-file form makes the
second false.

No new registry-process key has a config-file form. Whether a key also carries a
`podium serve` flag is a separate choice, because the two shipped patterns
differ: `PODIUM_WEB_UI` and `PODIUM_WEB_UI_ALLOW_PUBLIC_BIND` carry both a flag
and an environment read (`cmd/podium/serve.go:38-39`,
`internal/serverboot/serverboot.go:1826-1827`), while `PODIUM_TRUSTED_PROXY_SECRET`
and `PODIUM_RUNTIME_KEYS_PATH` carry an environment read and no flag at all and
are recorded in §13.12 as "Environment only; no config-file key"
(`spec/13-deployment.md:479-480`).

The enablement boolean and the transaction TTL carry a flag and a `PODIUM_*`
variable, following `PODIUM_WEB_UI`. The acquisition values, enumerated under
"Enablement, guard, and mount", are environment-only with no flag, following
`PODIUM_TRUSTED_PROXY_SECRET`: one of them is a client credential, and a
credential passed on the command line is readable from the process table, so the
whole acquisition set is kept off it rather than split by sensitivity. The
`docs/reference/cli.md` synopsis and flag-table rows below therefore cover the
enablement boolean and the transaction TTL only, and the §13.10 key list is where
every new key, flagged or not, is documented.

### The second-location sweep

The registry today accepts two credentials, and §6.3.3 and its mirrors state
that in prose rather than in one enumeration. This proposal adds no third
credential. It adds a second accepted location for the `oidc-jwt` credential, so
every sentence written on the assumption that the credential always arrives
forwarded by a gateway in the configured header is falsified or narrowed.
Successive review rounds each found one such site, one round at a time, because
the disposition was argued per site rather than decided by a rule.

**The rule.** The credential-location rule under "The browser session" decides
every site, and this section states no second version of it. A site is affected
when it states what the registry accepts as a credential, how a caller obtains
that credential, what a request lacking a Bearer token resolves to, or that a
client sends no credential of its own. A site is unaffected when it describes how
one provider's own mechanism works, the verification configuration, or how the
registry derives a request's tenant, none of which change.

The sweep produces no inventory of affected sites. The rule is stated once, with
the reproducing command beside it and four worked examples. A list here would be
a projection of that rule onto the corpus, and a projection drifts from the rule
that generates it: each round of review that read such a list found one more site
the list had missed or misplaced, while the rule and the command stood
unchallenged.

**IMPLEMENTOR'S CHOICE:** the wording each affected site takes. Any answer
narrows the falsified claim to the location it was written about rather than
deleting it, keeps the two existing credentials' behaviour unchanged, and leaves
every site the rule leaves standing untouched.

### The edit sites

**Spec.**

- **§13.10, `spec/13-deployment.md:170`** — the sentence proposal 0012 landed,
  which states the UI "runs no acquisition flow of its own" and resolves
  anonymous. It is rewritten to state what the UI now does. The first half stays
  literally true of the device-code flow, because this is an authorization-code
  flow; what changes is that it is no longer a complete account.
- **§13.10, `spec/13-deployment.md:172`** — the bind guard, whose stated
  rationale is "preventing accidental exposure of an unauthenticated UI".
  It is restated to match what the guard achieves once the UI can authenticate.
  This sentence is the source the code comment and the docs mirrors below follow,
  and the clause it carries exists at exactly three sites in the tree: this
  sentence, `docs/reference/error-codes.md:69`, which the mirror table stages
  under D1, and `ErrWebUIPublicBindRefused`'s doc comment at
  `pkg/registry/server/config_validate.go:29`, which C3 stages together with the
  inline rationale on the guard itself at
  `pkg/registry/server/config_validate.go:99-101` ("the web UI is open on its
  bind address (no auth in a no-identity standalone)"), narrowed the same way.
  The guard's predicate, its error, and its message are unchanged; what moves is
  the rationale each site states.
- **§13.10** — the web-UI configuration keys, per the rule above, and the
  configuration guard, stated beside the bind-guard sentence. The guard is that
  enabling the browser flow requires `PODIUM_WEB_UI` on,
  `PODIUM_IDENTITY_PROVIDER=oidc-jwt`, public mode off, and the acquisition
  options §6.3.4 marks required, which are the OAuth client identifier, the
  client credential the server-side exchange presents, the redirect URI
  registered with the IdP, and the IdP's authorization and token endpoints,
  additional to the issuer and audience `oidc-jwt`
  already requires (`spec/06-mcp-server.md:106`). §6.3.4's `Options:` list names
  the same set, and states that the device-code option
  `PODIUM_OAUTH_AUTHORIZATION_ENDPOINT` (`spec/06-mcp-server.md:42`) is not read
  by the browser flow. It also requires that redirect
  URI to be an `https` URL or a loopback `http` URL, because the session cookies
  carry the `__Host-` prefix and therefore `Secure`, and a browser neither stores
  nor returns a `Secure` cookie on a non-secure origin. That conjunct is about
  the origin the browser reaches rather than the issuer URL the `oidc-jwt` scheme
  guard already constrains. The new guard runs after the shipped public-mode
  exclusion, so public mode combined with `oidc-jwt` keeps failing with
  `config.public_mode_with_idp`, which the existing check emits first
  (`pkg/registry/server/config_validate.go:87-90`), and the browser-flow guard
  reaches the public-mode conjunct only where no identity provider is
  configured, in which case the `oidc-jwt` conjunct is the one it names.
  §13.10's bind guard does not imply the redirect-URI conjunct: it admits
  a non-loopback bind once `--web-ui-allow-public-bind` and an identity provider
  are set (`spec/13-deployment.md:172`), and §6.3.3 blesses a directly reachable
  `oidc-jwt` registry (`spec/06-mcp-server.md:106`), which is a registry serving
  plain HTTP. Every other combination fails
  startup with `config.web_ui_auth_unconfigured`, naming the condition that
  failed. Making the web UI a conjunct of the guard rather than a second
  enablement axis is what lets the routes mount on one validated field: a
  registry that enables the flow without the UI does not boot, so no
  configuration leaves the routes absent for a reason the guard has not already
  stated. The provider and public-mode conjuncts are stated here because the
  shipped exclusion is keyed on `PODIUM_IDENTITY_PROVIDER` alone
  (`spec/13-deployment.md:484`) and does not fire on a new enablement key. The
  guard reads only startup configuration, which is set once at boot from the
  flags and `PODIUM_*` variables and never changed at runtime, and its code
  mirror is `StartupConfig.Validate` (`pkg/registry/server/config_validate.go:87`),
  which C3 extends with the enablement and acquisition fields alongside the
  existing `WebUI` and `WebUIAllowPublicBind` fields (`:67-72`).
- **§6.3, a new §6.3.4** stating the browser acquisition flow, placed after
  §6.3.3, which ends at `spec/06-mcp-server.md:114` immediately before §6.4 at
  `:116`, with a pointer from the §6.3 introduction at `:40`. It is not a fourth
  sub-bullet under the `oauth-device-code` bullet's list (`:44-47`), which is
  scoped to the device-code flow. §6.3.4 is also the spec home of the CSRF
  predicate below: it defines a state-changing request as one whose HTTP method
  is other than `GET`, `HEAD`, or `OPTIONS`, and it states that a state-changing request, other than the
  sign-in and callback routes, that carries cross-site browser-origin evidence is
  refused with `403` `auth.csrf_invalid` before the handler runs, whatever
  credential authenticated it, and it names that evidence as a `Sec-Fetch-Site`
  header whose value is other than `same-origin` or `none` or an `Origin` header
  whose host and port differ from the host and port the request's own `Host`
  header names. It states that the scheme is not compared, because the `Host`
  header carries no scheme and §6.3.3 already records that the registry serves
  HTTP while TLS terminates upstream (`spec/06-mcp-server.md:112`), so a
  registry behind a gateway cannot observe the browser-facing scheme; "The CSRF
  position" carries the same predicate and the reason the omission is safe. It
  states that the predicate is scoped by the evidence rather than by the
  credential, because §13.10 serves the UI behind a fronting gateway that
  converts the browser's own session into the configured token header
  (`spec/13-deployment.md:170`), so a credential-scoped gate would leave that
  deployment's panel writes forgeable. It states that a state-changing request
  carrying no such evidence is admitted, which is what a CLI, an SDK, or any
  other non-browser client sends. It states that the predicate names no
  deployment, so it applies whether or not the browser flow is enabled. It states
  the two routes'
  exclusion and its reason in the same place: each is a top-level navigation that
  carries no same-origin proof and may carry a session cookie from an earlier
  sign-in, and each is bound instead by the single-use `state`, `nonce`, and PKCE
  verifier in the pre-authorization cookie. Without a spec home the requirement
  would live only in this proposal, and the test that pins it would have no
  section to cite.
- **§6.3.3 (`spec/06-mcp-server.md:92-112`)** — today it enumerates two accepted
  credentials, the gateway-forwarded `Bearer <token>` under `oidc-jwt` (`:96`)
  and the injected `X-Podium-User-*` headers under `trusted-headers` (`:108`).
  The browser flow adds no third credential. The `oidc-jwt` entry gains a second
  accepted location for the same credential: where the browser flow is enabled,
  a token the registry itself obtained through the §6.3.4 exchange may arrive in
  the `__Host-podium_session` cookie instead of the configured token header, and
  is verified identically against the issuer JWKS for the same `aud`. Because it
  is the same credential, the section adds no verification rule and no new
  refusal. It states the precedence: the configured token header is read first
  and the cookie only when that header carries no bearer credential, so a
  registry-set cookie cannot displace a gateway-forwarded identity, and the two
  are never merged. The `oidc-jwt` paragraph closes with an unqualified
  anonymity rule, "A header value without the prefix carries no token, so the
  request is anonymous and sees public visibility only (§4.6)"
  (`spec/06-mcp-server.md:96`), which names the same state the precedence rule
  hands to the cookie, so that sentence narrows with the amendment rather than
  standing unchanged: where the browser flow is enabled, a request whose
  configured token header carries no bearer credential is anonymous only when it
  also presents no valid `__Host-podium_session` cookie, and elsewhere the
  sentence applies as written. This is the same narrowing the mirror table stages
  on `docs/deployment/gateway-delegated-identity.md:58`, which restates this
  rule, so the authoring source and its shipped mirror move together. The
  section's opening paragraph carries the same assumption. Its first clause
  (`spec/06-mcp-server.md:92`) describes both providers as serving a deployment
  that runs the registry behind a gateway, which is false for a directly
  reachable `oidc-jwt` registry running the browser flow, so S3 restates it under
  the credential-location rule in "The browser session", which works that site
  through as an example. The same
  line closes "A Podium client behind such a gateway sends no credential of its
  own", whose "such a gateway" the restatement leaves without an `oidc-jwt`
  antecedent, so S3 scopes that sentence to a client behind a gateway under
  either provider in the same edit. The `pkg/identity/registry.go:69-70` comment
  C2 moves and the `docs/deployment/gateway-delegated-identity.md:11` sentence D1
  moves are that line's code and documentation mirrors, and the three land
  together. `:94` stands as written, because the
  amendment adds no credential and changes no tenant derivation: the cookie
  carries the same IdP-signed token and the registry still reads the
  organization from that token's verified `org_id` claim. `:92` and `:96` are
  the only sentences in `:92-112` that this amendment touches, and the separate
  `trusted-headers` anonymity rule at `:108` stands unchanged for the reason the
  next sentence gives. A
  `trusted-headers` or `injected-session-token` registry reads no cookie, because
  the browser flow cannot be enabled under either and the cookie branch is gated
  on the same enablement field. "The browser session" above states the mechanism.
- **§2.2 (`spec/02-architecture.md:101`)** — the component map's
  `IdentityProvider` bullet restates the §6.3.3 clause above as "Registry-process
  built-ins for a gateway-fronted deployment: `oidc-jwt` and `trusted-headers`".
  S3 restates it in the same edit as its authoring source, so the applied spec
  does not describe `oidc-jwt` as gateway-scoped in §2.2 while §6.3.3 says
  otherwise. The bullet's client-side built-ins and its `IdentityProvider`
  description are unchanged, and no §9.1 SPI row moves, because the browser flow
  adds no provider value.
- **§7** — the sign-in, callback, and sign-out routes and the posture read,
  alongside the
  operator-level endpoints §7.3.3 enumerates
  (`spec/07-external-integration.md:152`). The section states each route's
  method, path, and outcomes as the pre-authorization transaction contract under
  "The browser session" gives them, meaning what the sign-in redirect carries,
  what the callback compares and in what order, what each outcome sets and
  clears, and which code each refusal returns. It states no element that contract
  does not carry, so a clause present here and absent there is a defect in this
  edit site rather than an extension of the contract. The cookie
  attributes and lifetimes are the ones the cookie table under "The browser
  session" gives each row. None of the
  three reads or writes registry state, so §13.2.1 classifies all three outside
  the write set and a read-only registry serves them unchanged. The section
  states that sign-in and the callback are outside the §6.3.4 same-origin gate,
  for the reason §6.3.4 gives, that a callback presenting a session cookie from
  an earlier sign-in completes and replaces that cookie rather than being
  refused, and that a sign-in presenting one starts a fresh transaction rather
  than being refused. It states
  that sign-out clears the cookies on every request that carries one and
  passes the §6.3.4 same-origin check, and that a sign-out failing that check is
  refused before the handler runs and clears nothing; that is the same predicate
  "The CSRF position" states, and the two sites carry it identically so a forged
  cross-origin sign-out cannot log an operator out.
  The section also states the mount predicate: the three routes are registered
  only where the browser flow is enabled, which the §13.10 guard already makes
  imply the web UI, `oidc-jwt`, public mode off, and the acquisition options, so
  a registry that boots with the flow disabled serves none of them and a request
  for one of those paths is answered as any path the registry does not register
  is answered on that deployment. The staged sentence fixes no status code,
  because the identity verification §6.3.3 configures runs ahead of route
  matching and a deployment whose provider refuses an unverifiable request
  answers that refusal first; "What happens when it does not fire" under "The
  browser session" records the dispositions. The
  routes are registered inside the block that already mounts `/ui/`
  (`internal/serverboot/serverboot.go:1229`), so the deployment predicate is
  written once. The predicate reads startup configuration that is set at boot and
  not changed at runtime, so a registry that boots without the flow never
  acquires the routes and one that boots with it never loses them. This keeps a
  deployment that wants no browser flow, including the shipped web-UI-only
  configuration, free of the routes.
  The same §7 entry states the posture read `GET /v1/ui/session`, which "The
  browser session" specifies: its body, its unauthenticated status, its mount on
  the web UI alone rather than on the browser flow, and that it reads and writes
  no registry state, so §13.2.1 leaves it outside the write set beside the three
  authentication routes. It is stated here rather than in §13.10 because it is an
  HTTP endpoint and §7 is where the registry's endpoints are specified, and it
  carries no acquisition option, so the key-placement rule does not reach it.
- **§7.3.1 (`spec/07-external-integration.md:95`), with the reingest trigger row
  at `:65` and the quickstart reingest comment at `spec/00-quickstart.md:46`** —
  the user-defined-layer
  paragraph states no owner rule for the write handlers; the only per-handler
  statements are the reorder comment at `:87` and the reingest row at `:65`.
  This edit site is the single statement of the layer-write authorization rule,
  including every arm of it and the conditions under which it is live. Every
  other site in this proposal cites it by name and states only what is local to
  that site. It
  gains the general rule: `unregister`, `update`, `restore`,
  `reorder`, and `reingest` on a user-defined layer, and a `register` whose ID
  names a user-defined layer that already exists in the tenant, are authorized to
  that layer's owner or to a tenant admin. The same operations against an
  admin-defined layer, including a `register` whose ID names one, are authorized
  to a tenant admin alone, whatever that layer's stored `owner` field names,
  because that field is set from the request body on the admin-defined branch of
  `register` and patched on the admin-defined branch of `update`
  (`pkg/registry/server/layers.go:659`, `:547-549`) and therefore names no
  authorized subject. A caller authorized by neither arm is refused with
  `403` `auth.forbidden`, whether that caller resolves a different subject or
  none at all. A layer that is soft-deleted and still inside its §8.4 recovery
  window is a layer that exists for this rule: a `register` under its ID is
  authorized against its stored owner and its user-defined flag on the same
  terms, so the recovery window is not a window in which its ID can be taken
  over. A registry that cannot determine whether the ID names an existing layer
  refuses the request rather than treating the ID as unused, so a degraded store
  denies the registration rather than admitting it. The staged sentence names no
  error code, because §6.10 carries no prose entry for `registry.unavailable`
  even though the code is on the §6.10 matrix axis
  (`tools/matrix/matrices.go:109`); the code the refusal returns is stated in
  "The layer-ownership defect" and asserted by C1's tests.
  The rule is live only where an identity provider is configured and public
  mode is off, so a registry that authenticates no caller keeps admitting the
  request, per `spec/13-deployment.md:33`. The sentence follows the wording §4
  already uses for the parallel re-embed carve-out
  (`spec/04-artifact-model.md:760`). This is the spec basis
  C1 implements, and it is why C1 depends on S6.
  The reingest trigger row at `:65` is restated in the same edit, because it
  reads "(admin or layer owner)" for every layer class and would otherwise admit
  an admin-defined layer's stored non-admin owner while the amended §7.3.1
  refuses that caller, leaving two §7 sentences deciding the same request
  oppositely. Its parenthetical is rescoped the way `:87` already scopes
  reorder, naming a tenant admin, or the layer's owner on a user-defined layer.
  The quickstart's reingest comment at `spec/00-quickstart.md:46` reads "an
  admin (or the layer owner) can reingest manually" over `org-defaults`
  (`spec/00-quickstart.md:47`), which is an organization-visible admin-defined
  config layer wherever the corpus declares it
  (`docs/deployment/access-control.md:35-39`,
  `docs/getting-started/concepts.md:122`), so after S6 its owner arm names no
  authorized subject and the sentence decides that request the opposite way the
  amended §7.3.1 does. The same edit restates it as a tenant admin reingesting
  manually, dropping the owner arm rather than qualifying it, because the layer
  the example reingests is admin-defined.
- **§7 errors (`spec/07-external-integration.md:97`)** — the closing error
  enumeration scopes `auth.forbidden` to "admin-only operations attempted by a
  non-admin". After S6 the code also reports a caller who is neither an admin nor
  the owner of the user-defined layer being mutated, which is expressly not an
  admin-only operation (`docs/reference/cli.md:440`), so the sentence is
  broadened.
- **§6.10 and §6.9** — two new codes. `auth.csrf_invalid` covers a state-changing
  request the §6.3.4 gate refuses and a callback whose pre-authorization cookie
  does not validate, refused with `403`. The entry defers to §6.3.4 for the
  predicate rather than restating it, and §6.3.4 states the predicate "The CSRF
  position" states. No existing code covers it: `auth.forbidden` reports an
  authorization decision about the caller, and this refusal is about the request.
  `auth.exchange_failed` covers a callback whose code exchange the IdP answered
  and refused, refused with `502` and carrying `retryable: false`, with a
  `suggested_action` naming the client credential and the registered redirect URI
  as what an operator checks. It is separate from `registry.unavailable`, whose
  envelope carries `retryable: true` (`pkg/registry/server/error_envelope.go:26`)
  and whose remediation names a retry that fails identically every time. Both
  codes take a §6.9 row, an entry in the
  `errorCodeRegistry` at `pkg/registry/server/error_envelope.go:24`, a row in the
  `auth.*` table of `docs/reference/error-codes.md`, and a cell on the §6.10 axis
  in `tools/matrix/matrices.go:78-115`.
  `auth.token_expired` (`:355-364`) and `auth.untrusted_token` (`:366-376`) keep
  their scopes, because a browser session presents an `oidc-jwt` token and the
  two entries already cover an expired one and one that fails signature, `iss`,
  or `aud`. Neither is re-scoped and no code is added. The cookie reaches them
  through the shipped verifier path rather than a new one: `oidcJWTVerifier`
  calls `verifier.Verify` and returns the error
  (`internal/serverboot/identity_verify.go:201-215`), which `writeIdentityError`
  maps to the two codes (`pkg/registry/server/identity_verify.go:88-100`). That
  path is also why the inventory reaches `pkg/identity`, which declares the error
  the amended `:366` defines. What moves on each entry is the text that assumes a
  gateway forwarded the token, because the cookie carries a token the registry
  itself obtained through the §6.3.4 exchange and a directly reachable `oidc-jwt`
  registry has no gateway at all. S7 stages the `spec/` text the
  credential-location rule under "The browser session" moves for these two codes;
  `auth.token_expired`'s canonical `message` (`spec/06-mcp-server.md:360`) is
  provider-neutral and is not among it. That rule also records why
  `auth.tenant_unknown` (`spec/06-mcp-server.md:378-388`) and its mirror
  `pkg/registry/server/error_envelope.go:73-75` stand unedited.
  The §6.10 axis in `tools/matrix/matrices.go:78-115`
  is hand-maintained rather than derived from `spec/` or from the envelope
  registry, which is why `auth.tenant_unknown` and `auth.untrusted_token` are
  shipped codes with no cell on it, and `matrix-audit` reports only cells the axis
  registers. Adding the two entries is what makes the
  `// Matrix: §6.10 (auth.csrf_invalid)` and
  `// Matrix: §6.10 (auth.exchange_failed)` annotations on the tests
  load-bearing; without them an annotation names no cell and the gate stays green
  whether or not the test exists.
- **§13.2.1 (`spec/13-deployment.md:41`)** — the section's rule is per-endpoint
  and per-mutation, and it says each endpoint's own section states its
  classification, so §7's entry states one covering every route this proposal
  adds: sign-in, the callback, sign-out, and the posture read all read and write
  no registry state, so each is
  outside the write set and a read-only registry serves them unchanged. This is
  an application of the section's existing rule rather than a carve-out, so
  §13.2.1's own text gains nothing and the SCIM-receiver precedent is not
  invoked.
- **§11** — the verification entry, covering the matrix below.

**IMPLEMENTOR'S CHOICE:** the path of each authentication route. The method is
not part of this blank: "The browser session" fixes sign-in and the callback on
`GET` and sign-out on `POST`. Any answer
places the paths under the existing `/v1/` prefix, uses one path per route, and
appears identically in the §7 entry, in the Authentication section of
`docs/reference/http-api.md`, in the mux registration, in the S45 step-4
rewrite, and in the new sign-in scenario, so every path those scenarios probe
matches the mux. The posture read's `sign_in_path` and `sign_out_path` carry the
same values at runtime, which is where the UI reads them, so the bundle spells
no authentication route path. The posture read's own path is `/v1/ui/session`
and is not part of this blank, because the UI has to request it before it has
read anything.

**Shipped documentation mirrors.** Each restates spec text this amendment
changes, so each moves with it.

| Mirror | What it restates |
|:--|:--|
| `docs/deployment/gateway-delegated-identity.md:105-107` | the §13.10 web-UI account; 0012 recorded this page as this proposal's obligation |
| `docs/deployment/gateway-delegated-identity.md:58` | §6.3.3's "a request carrying no token is anonymous" rule, inside the page's `## oidc-jwt` section. It is restated with the same browser-flow conjunct the `spec/06-mcp-server.md:96` authoring source gains: a request carrying no token in the configured header is anonymous, and where the browser flow is enabled it is anonymous only when it also presents no valid `__Host-podium_session` cookie. The conjunct keeps the sentence true for the gateway-fronted deployment this page describes, which enables no browser flow and reads no cookie, and it matches the rewritten `:107` on the same page |
| `docs/reference/error-codes.md:57` | `auth.untrusted_token`, restated under the credential-location rule so the row and its remediation match the amended `spec/06-mcp-server.md:366` and `:374` |
| `docs/reference/error-codes.md:59` | `auth.token_expired`, whose scope sentence stands and whose remediation clause is restated under the same rule |
| `docs/reference/error-codes.md:60` | `auth.forbidden`'s "When" text, "An admin-only operation attempted by a non-admin caller", is restated as owner-or-admin, parallel to `docs/reference/cli.md:440` below: a layer write on a user-defined layer attempted by a caller who is neither the owner nor an admin joins the admin-only case the entry already names. The `auth.*` table also gains the `auth.csrf_invalid` and `auth.exchange_failed` rows |
| `docs/reference/error-codes.md:69` | the bind guard's `config.web_ui_public_bind_refused`, which the amended §13.10 bind-guard sentence restates; the `config.*` table also gains a `config.web_ui_auth_unconfigured` row stating the browser-flow guard's predicate |
| `docs/reference/http-api.md:13-27` | the Authentication section: the header table, and the account of the accepted registry-process credentials at `:21-27`, which gains the browser session under `oidc-jwt` and the CSRF requirement every state-changing request carries, other than the sign-in and callback routes, together with the `X-Podium-CSRF` header and `__Host-podium_csrf` cookie where the landed mechanism carries a request-side value. It states the predicate and the sign-in and callback exclusion as §6.3.4 states them, which is the predicate "The CSRF position" states, and scopes neither of its own accord. It is also the new home of the authentication routes, each documented by the method and path the route methods under "The browser session" and the path blank above fix, and of the posture read `GET /v1/ui/session`, whose body, unauthenticated status, and web-UI mount predicate it states as "The browser session" gives them; there is no route list there today |
| `docs/reference/cli.md:131-138` | the `podium serve` synopsis, a closed usage line carrying `--web-ui` and `--web-ui-allow-public-bind`, which gains a token for `--web-ui-auth` and one for `--web-ui-auth-transaction-ttl` and none for the environment-only acquisition values, per the key-placement rule |
| `docs/reference/cli.md:142-155` | the `podium serve` flag table, which gains a row for each of those two flags naming its `PODIUM_*` override, and whose `--web-ui-allow-public-bind` row (`:155`) is restated from the amended §13.10 bind-guard sentence |
| `docs/reference/cli.md:747` | the environment-variable table row that pairs `PODIUM_OAUTH_AUDIENCE` with `PODIUM_OAUTH_AUTHORIZATION_ENDPOINT` as "OAuth provider config", which gains the environment-only browser-flow acquisition keys and states that the device-code endpoint is not one of them |
| `docs/reference/http-api.md:265-346` | the Layer management section, whose entries state the pre-S6 authorization rule: `:329` says a user-defined-layer update "still answers `200 OK`", `:320` gives the reorder rule as admin-only on an admin-defined layer, `:286` documents register's `201 Created` with no refusal, and unregister, restore, and reingest document no authorization at all, while every other gated route in the reference does (`:538`). The section gains one statement at its head, and the amended §7.3.1 is what that statement says. `:329`'s `200 OK` clause is scoped to the owner, `:320` is restated so the admin-defined sentence no longer reads as the whole rule, and `:286`'s register entry and the unregister, restore, and reingest entries carry the authorization they document none of today. This page names the error codes the refusals return where the staged spec text does not, because it is a code-level reference and `docs/reference/error-codes.md:158` already carries the generic `registry.unavailable` row.<br><br>**IMPLEMENTOR'S CHOICE:** the wording of the head statement. Any answer says what the amended §7.3.1 says and nothing the §7.3.1 edit site does not carry, rendered in the reference page's voice with the codes named; a clause present here and absent there is a defect in this row rather than an extension of the rule |
| `docs/reference/http-api.md:457` | the Reembed entry's closing sentence, "The exception is specific to re-embed." It is true of the page as it ships, because the Layer management section documents no authorization today, and the head statement the row above adds is what makes it false: after S6 the layer write endpoints carry the same deployment-keyed carve-out on the same page. This row is a page-internal reconciliation rather than a mirror moving with its source, because the authoring sentence at `spec/04-artifact-model.md:760` qualifies its exclusivity with "does not extend to the other admin-gated endpoints, whose posture is defined in §4.7.2 and §7.3.2" and the layer write gate is neither admin-only nor specified in either of those sections, so the spec sentence stands as written and only the unqualified shipped restatement moves. The first sentence of the paragraph stands. The closing sentence is restated to record that the layer write endpoints admit a request on the same registries for the reason the Layer management head statement gives. The restatement makes no exclusivity claim, because the erase endpoint documented at `:459-465` takes the same short-circuit through the shared admin hook (`internal/serverboot/serverboot.go:1213`) and §13 states the two together: "the layer-management and erase endpoints admit any request" (`spec/13-deployment.md:33`) |
| `docs/reference/cli.md:440` | the `podium layer reorder` entry, which states "Reordering a user-defined layer requires no admin role" as the complete rule; it is restated as owner-or-admin with the same deployment carve-out |
| `docs/reference/http-api.md:290` | the register-response example, which prints snake_case keys for a response emitting Go field names |

## The CSRF position

A credential the browser attaches automatically authenticates any request the
browser can be induced to make, so every layer write this proposal exposes
becomes forgeable across origins.

The position is specified here rather than left to the implementor, because the
prior review treated it as acknowledged prose for eight rounds and never
produced a finding on it.

This section is the single statement of the gate predicate, including which
routes it excludes and why. Every other site in this proposal cites it by name
and states only what is local to that site.

**The gate is scoped by the evidence the request carries rather than by which
credential authenticated it.** The session cookie is not the only credential a
browser attaches by itself. Where a gateway fronts the registry under `oidc-jwt`
or `trusted-headers`, §13.10 serves the UI from the same registry process behind
the same gateway and the gateway "authenticates the request and the registry
resolves the caller's identity from the forwarded token or the injected headers,
exactly as for any other API request" (`spec/13-deployment.md:170`,
mirrored at `docs/deployment/gateway-delegated-identity.md:107`). There the
browser's credential is the gateway's own ambient session, which the gateway
converts into the configured token header on every request the browser can be
induced to make, including a cross-origin form POST. The layer write handlers
decode the body with no `Content-Type` check
(`pkg/registry/server/layers.go:586-587`), so such a POST is CORS-simple and
takes no preflight. A gate scoped to the session cookie would therefore leave
the panel's own writes forgeable on the deployment §13.10 blesses, which is why
the predicate below reads the request rather than the credential.

- A request is state-changing for this gate when its HTTP method is other than
  `GET`, `HEAD`, or `OPTIONS`. The predicate is the method rather than the
  handler's effect, because the gate runs before the handler and has nothing
  else to read, and because the safe methods are the ones a browser issues as an
  ordinary navigation or subresource load. Sign-out answers on `POST` per the
  route methods under "The browser session", so it is state-changing by this
  predicate and the gate covers it; sign-in and the callback answer on `GET`, so
  the method predicate leaves them outside the gate as well as the named
  exclusion below.
- Every state-changing request, other than the sign-in and callback routes, that
  carries cross-site browser-origin evidence is refused before the handler runs
  with `403` `auth.csrf_invalid`, whatever credential authenticated it.
  Browser-origin evidence is cross-site when the request carries a
  `Sec-Fetch-Site` header whose value is other than `same-origin` or `none`, or
  an `Origin` header whose host and port differ from the host and port the
  request's own `Host` header names. The scheme is not compared, and that is
  forced rather than chosen: an HTTP `Host` header is `uri-host [":" port]` and
  carries no scheme, and a registry behind a TLS-terminating gateway cannot
  observe the browser-facing one. The registry builds a plain `http.Server` and
  calls `ListenAndServe` (`internal/serverboot/serverboot.go:1422`, `:1444`),
  §6.3.3 records that "the registry serves HTTP and TLS terminates upstream"
  (`spec/06-mcp-server.md:112`), and nothing in the tree reads
  `X-Forwarded-Proto`. A predicate that compared the scheme would refuse every
  panel write on the deployment §13.10 blesses and on the §13.1 topology, where
  a browser on `https://registry.acme.com/ui/` sends
  `Origin: https://registry.acme.com` to a registry whose own request scheme is
  `http` (`spec/13-deployment.md:5`, `:170`). Omitting the scheme term admits a
  downgrade origin such as `http://<host>` as same-origin, which costs this gate
  nothing: every session cookie carries `Secure` per the cookie table, so a
  browser presents no session credential on a non-secure origin, and the C3
  redirect-URI conjunct already requires an `https` or loopback origin.
  Comparing against `Host` is what keeps the gate free of a new configuration
  key for the registry's public origin. OD-9 records the deployment that
  comparison does not serve, a gateway that rewrites `Host` to an upstream
  service name.
- A state-changing request carrying neither header carries no such evidence and
  is admitted, which is what a CLI, an SDK, or any other non-browser client
  sends. A gate that swept them in would break every non-browser writer on a
  registry that enables the browser flow. That admitted case is the gate's
  residual: a browser that sent neither header would be indistinguishable from a
  non-browser client. Every browser that can reach a `/ui/` deployment sends
  `Sec-Fetch-Site` on a cross-site request and `Origin` on a cross-origin form
  POST, and where the chosen mechanism also carries a request-side value the
  residual is closed for a session-authenticated write as well. The Testing
  section pins the refusal, the header-authenticated cross-site refusal, and both
  admitting halves.
- The gate is not conditional on the browser flow. It reads the request rather
  than the deployment, so it runs on every state-changing request the registry
  serves, including on a registry that enables no browser flow and on one that
  serves no web UI. A second enablement axis would leave the gateway-fronted
  `trusted-headers` deployment, where the browser flow cannot be enabled at all,
  outside a control its own forgery case needs, and it would cost a non-browser
  client nothing either way, because such a client carries no browser-origin
  evidence. §6.3.4 is where the predicate is stated because the browser flow is
  what makes a browser-borne credential reachable in the first place; the
  predicate itself names no deployment.
- Sign-in and the callback are outside the gate, and the exclusion is stated by
  name rather than left to follow from their `GET` method, so that an
  implementation that widens the method predicate does not silently pull them
  in. Each is a top-level navigation that carries no
  request-side value a page could have set, and a browser that already holds
  `__Host-podium_session` from an earlier sign-in sends that cookie on both, so
  under an unqualified predicate every re-sign-in would be refused with
  `auth.csrf_invalid`, no session would ever be established for that browser, and
  no recovery would remain, since re-running sign-in is the only recovery an
  expired session has. What binds both routes is the single-use pre-authorization
  transaction, whose contract under "The browser session" refuses exactly the
  forged and replayed callbacks a same-origin check would. A forced cross-origin
  sign-in can do no more than
  replace the victim's own `__Host-podium_auth` cookie with a transaction the
  registry mints for that same browser, which the victim's own IdP session then
  completes, so the transaction the attacker started is not one the attacker can
  finish in the victim's browser.
- The cookies are the ones the cookie table under "The browser session" lists,
  and a CSRF mechanism that carries its own cookie takes that table's CSRF row.
  `SameSite` is a defense in depth here rather than the control, which is why the
  same-origin proof required above does not rest on it. Dropping the `__Host-`
  prefix from the CSRF row would let any host under the registry's registrable
  domain plant that cookie with a `Domain` attribute and then forge a
  state-changing request that echoes the planted value, which the session cookie
  would authenticate.
- The pre-authorization transaction refuses a replayed or misdelivered callback
  with the same `403` `auth.csrf_invalid`, as its contract under "The browser
  session" states. It is the same control on the same axis, which is why it
  reuses the code rather than adding a second one.
- A request whose session cookie carries a token past its `exp` is refused with
  `401` `auth.token_expired`, and one whose token fails signature, `iss`, or
  `aud` with `401` `auth.untrusted_token`, both by the shipped verifier path, on
  the routes that path wraps. A layer write carrying such a cookie resolves
  anonymous instead and is refused `403` `auth.forbidden`, for the reason "The
  browser session" gives.
  Both codes already cover the case as §6.3.3 states them, so neither is
  re-scoped and no code is added. Their gateway-assuming text is restated under
  the credential-location rule in "The browser session", which decides every site
  and names the step that owns it.
- Sign-out answers on `POST` and is therefore state-changing by the method
  predicate above, so it carries the same protection, because a forged sign-out
  is a denial of service against a signed-in operator. A sign-out refused for
  CSRF returns `403` `auth.csrf_invalid` and clears no cookie.
  Read-only mode does not enter into it: no authentication route is in the
  §13.2.1 write set, so a read-only registry serves sign-out exactly as it serves
  it otherwise.

**The wire contract, where the mechanism carries a request-side value.** The
names are fixed here rather than left open, because the UI and the server both
have to spell them and a test asserts them. The value is the
`__Host-podium_csrf` cookie, which the callback sets alongside the session
cookie and sign-out clears, and the request echoes it in the `X-Podium-CSRF`
header. The server compares the header against the cookie and refuses a
session-authenticated request whose header is absent or unequal, and requires no
header of a request that carries no CSRF cookie, which is what a CLI, an SDK,
and a gateway-fronted browser request all send. U1 owns the client half: every
state-changing call the panel issues sends that header, read from that cookie. A
mechanism that carries no request-side value, meaning the browser-origin
evidence check alone, sets no cookie and requires no header, and the cookie
table's CSRF row is then absent. When the client half is missing while the server half is present,
every panel write returns `403` `auth.csrf_invalid` in the browser while every
server-driven case still passes, which is why the U1 Surfaces bullet in the
Testing section drives a state-changing call from the panel itself and asserts
that it carries the header.

**IMPLEMENTOR'S CHOICE:** whether the gated endpoints also carry the
`__Host-podium_csrf` and `X-Podium-CSRF` double submit above, on top of the
browser-origin evidence check the predicate requires. The evidence check is not
itself a choice, because it is the only half that reaches a request a fronting
gateway authenticated, where the browser holds no CSRF cookie and requiring one
would refuse every CLI and SDK write. Any answer satisfies the predicate the
bullets above state and the Testing section's CSRF cases verbatim, adds no
cookie outside the cookie table's CSRF row, and mints no server-stored token.

## Rendering untrusted content

Artifact bodies are markdown authored by whoever can write to a layer's source,
and the UI now renders them rather than showing them as preformatted text. That
turns author-controlled content into markup on the registry's own origin, which
is the origin the session cookie is scoped to.

- Rendered markdown is sanitized, and the sanitizer runs on the rendered output
  rather than on the source, so a construct that survives the markdown renderer
  cannot bypass it.
- Frontmatter is rendered as a property table with values escaped as text. It is
  not markdown and is not rendered as such.
- The web UI's own source tree carries no `dangerouslySetInnerHTML` outside the
  single sanitized rendering path, checked mechanically rather than by review.
  B1 owns that check, because B1 owns the CI lane, and it runs in the same CI job
  as the rebuild-is-clean check so a tree that reintroduces the attribute fails
  before review. The controls above are pinned by the sanitizer cases in the
  Testing section.
  The check is scoped to that tree rather than to the repository, because
  `site/` already uses the attribute in several components
  (`site/src/components/content/Tabs.tsx:78`,
  `site/src/components/layout/Lockup.tsx:31` and `:38`,
  `site/src/build/render.ts:132`). The documentation site renders build-time
  authored content into a published static page and serves no registry artifact
  body on the registry's origin, so it is outside this control. A
  repository-wide check would fail on the current tree before any web-UI code
  exists, and would then be deleted or silently rescoped.

**IMPLEMENTOR'S CHOICE:** which sanitizer the rendering path uses. Any answer
runs on the rendered output rather than on the markdown source, is applied at the
single rendering path the `dangerouslySetInnerHTML` check scopes, and carries an
allowlist that admits no URL scheme other than `http`, `https`, and `mailto` on
any attribute it keeps.

## Build and embedding

The React bundle is committed to the tree.

`go:embed` is a compile-time directive: `web/web.go:12` names the files, and a
missing path is a build error rather than a runtime one. Today the three source
files are committed, so `go build ./...` works on a clean clone with only a Go
toolchain. Generating the bundle at release instead would break that, and with
it `make build` (`Makefile:316`), the `go` CI job, which carries `setup-go` and
no Node (`.github/workflows/test.yml:22`), the release cross-compile matrix
(`.github/workflows/release.yml:298`), and `go install` from source entirely.

`site/` is not a precedent for the alternative. Its `dist/` is gitignored, but
site output is published rather than embedded, so nothing in `go build` depends
on it.

What lands:

- The bundle is committed at a path that escapes the bare `dist/` entry at
  `.gitignore:18`, either by negation or by a directory name that does not match
  it.
- `web/web.go`'s embed directive names the built bundle, and `web.Assets()`
  returns a file system rooted at the served bundle, through `fs.Sub` when the
  bundler emits into a subdirectory. `internal/serverboot/serverboot.go:1229`
  mounts that file system directly at `/ui/`, so a bundle whose `index.html` is
  not at the returned root stops serving the UI.
- `web/web_test.go` moves with the directive. Its assertions read
  `index.html`, `app.js`, and `style.css` out of the embedded set by exact name
  at its root (`:13-21`, `:24-32`), and the vanilla `app.js` and `style.css`
  cease to exist. They are rewritten against the bundle's entry points at the
  root `web.Assets()` returns, and both `// Spec: §13.10` annotations
  (`web/web_test.go:11`, `:24`) are preserved so the rewritten tests stay
  attributable to the section they verify. No gate enforces that, which is why it
  is stated as part of the deliverable: `make coverage-gate` runs `speccov drift`
  (`Makefile:284`, `:247-248`), which fails only on a citation naming a spec
  section that no longer exists (`tools/speccov/main.go:132-133`). The command
  that fails on a section with no citing test is `speccov uncovered`
  (`tools/speccov/main.go:112-113`), and the gate does not invoke it. Many other
  tests across the repository also cite §13.10, so the section does not lose its
  last citation either way.
- The built `index.html` keeps `<title>Podium</title>`, which today comes from
  `web/index.html:5`. Three shipped assertions read that literal out of the
  served or embedded index: `web/web_test.go:19`,
  `cmd/podium/serve_ui_test.go:51`, and
  `test/e2e/server_flag_behavior_test.go:30`. A bundler's scaffolded
  `index.html` carries the tool's own title, so the title is a constraint on the
  bundle rather than an incidental property, and holding it leaves all three
  assertions standing unchanged.
- The bundle's asset references resolve under the `/ui/` mount. The UI is served
  through `http.StripPrefix("/ui/", …)` and the outer mux routes every other
  path to the meta-tool handler, which registers no `/assets/` route
  (`internal/serverboot/serverboot.go:1230`, `:1239`,
  `pkg/registry/server/server.go:389-419`). A bundle built with the common
  default public base of `/` emits `<script src="/assets/index-<hash>.js">`, the
  browser requests `/assets/…`, and the outer mux returns `404`, so `/ui/` serves
  a blank page while the rebuild check and the title assertion both still pass.
  Today's hand-written SPA avoids this only because its references are relative
  (`web/index.html:7`, `:18`). Either the bundler's public base is set to `/ui/`
  or the emitted references are relative, and `web.Assets()` serves every
  referenced path.
- A CI step rebuilds the bundle and fails if the working tree differs, which is
  what makes the committed output trustworthy rather than merely present. This is
  part of the deliverable.
- A `.gitattributes` entry marks the bundle generated so review diffs collapse.
  The repository has none today.

**IMPLEMENTOR'S CHOICE:** the bundler and the output path. Any answer produces
deterministic output so the rebuild check is stable, keeps `go build ./...`
working with no Node toolchain present, leaves `web.Assets()` rooted at the
served bundle so `/ui/` keeps returning `index.html`, emits that `index.html`
with `<title>Podium</title>` and with asset references that resolve under the
`/ui/` mount, and leaves the built bundle the only generated artifact in the
tree.

## The design handout

`web/DESIGN.md` is the design brief. A design pass against it produces the
layouts, the state treatments, and the component inventory, and the
implementation builds what that pass produces. The implementor does not design
the UI.

Two items in the brief are design problems rather than implementation details
and must not be settled by whoever writes the React:

- The webhook secret is returned once on register and on rotation
  (`LayerRegisterResponse`, `pkg/registry/server/layers.go:328`). It must be
  copyable, unmistakably unrecoverable, and not readable as persistent content.
- Unregistering a layer removes its artifacts from every caller's view and needs
  a confirmation proportionate to that.

The brief also names the design questions this proposal does not answer: how much
domain depth to render at once, whether to expose the relevance score, how to
treat the sensitivity label, and how to distinguish an empty domain from a
filtered one without disclosing that hidden artifacts exist.

**The brief itself is corrected first (G1).** The brief is wrong about the API
the design pass would be designing against, so a design produced from it would be
wrong in the same way, and it is missing the surface this proposal's
authentication route creates. The corrections below cover both, and one of them
is a standing constraint rather than a single sentence. Each correction that keys a
rendering rule on the deployment names the posture read as the signal, because
"The browser session" adds it for exactly that purpose and the brief predates it.

This section is the single statement of the posture-keyed rendering rules. The
panel-visibility rule, the catalog-scope rule, the expiry-signal rule, and the
sign-in control table are each stated once here, in the vocabulary the posture
read returns (`identity_provider_configured`, `public_mode`,
`browser_auth.enabled`, and `subject`), so that no other site translates a prose
scoping into a field test. Every other site in this proposal names a rule and
cites G1, and states no condition, field, or value the statement here does not
carry.

- `web/DESIGN.md:126-129` describes a layer as carrying "a visibility that is one
  of public, organization-wide, group-scoped, or user-scoped". Visibility is a
  union of the independent fields `Public`, `Organization`, `Groups`, and
  `Users` (`pkg/store/store.go:270-273`), and "Multiple fields combine as a
  union; a caller sees the layer if any condition matches"
  (`spec/04-artifact-model.md:611`), which is why the §4.6 matrix enumerates
  every non-empty subset (`tools/matrix/matrices.go:124-140`). The sentence is
  rewritten to describe the union, and the layer-panel section states the display
  treatment for a layer that matches on more than one axis, because a
  single-valued label cannot render a layer that is both public and
  group-scoped.
- **The brief owns no wire fact.** `web/DESIGN.md` states no field name, type, or
  status code of its own. It cites `docs/reference/http-api.md` and the response
  structs for each, and the design pass reads the cited source for any field it
  needs to render. That is what corrects the two field-level errors the brief
  carries today, the layer list's field inventory and the type of `frontmatter`,
  without leaving the brief a second copy of the wire contract to drift from. The
  brief cites the reference where the reference carries the field, which is
  `docs/reference/http-api.md:120` for `frontmatter` on a search result, already
  correct on the shipped page. It cites the response struct where the reference
  does not, which is the `load_artifact` response's `frontmatter`, and also the
  whole layer surface: the reference documents no `GET /v1/layers` response body
  (`docs/reference/http-api.md:296-300`) and its register example elides every
  key past the first two (`:290`), so the cited source for a layer's fields is
  `store.LayerConfig` (`pkg/store/store.go:258`), which the register response
  embeds under `layer` (`LayerRegisterResponse`,
  `pkg/registry/server/layers.go:328-332`) and the list response returns under
  `layers` (`pkg/registry/server/layers.go:762-777`).

  **IMPLEMENTOR'S CHOICE:** which layer fields the brief names. `web/DESIGN.md`
  names no field, type, or wire key of its own and cites the response struct for
  the layer surface, so the design pass reads that struct for any field it
  renders or gates on, including that field's marshalled key. A field name
  appearing in this proposal or in the brief is illustrative rather than
  contractual. Nothing mechanical catches a brief that restates a field, because
  the brief has no compiler or test behind it.
- The brief gives the design pass a treatment for a response that has no
  frontmatter pairs to render. It reaches that state on the paths below. A search
  result carries no `frontmatter` key when the child's `extends:` block cannot be
  rewritten, which the reference states (`docs/reference/http-api.md:120`) and
  which the struct's `omitempty` tag produces
  (`pkg/registry/server/server.go:557`). A `load_artifact` response for a
  non-skill artifact whose `manifest_body_url` is set carries an empty
  `frontmatter`, because the registry clears it alongside the inline
  `manifest_body` and the consumer fetches the document from the URL
  (`pkg/registry/server/server.go:582`, `:1235-1240`); the reference's
  `manifest_body_url` sentence (`:172`) states the clearing for the body alone,
  and its `load_artifact` field list (`:156-169`) names no `frontmatter` field,
  so the brief cites the response struct for this half rather than the reference.
  The property table is produced in the client from the value those sites
  describe. The staged surfaces that
  rest on this treatment are §13.10's frontmatter property table and the escaping
  control under "Rendering untrusted content", whose sanitizer case asserts that
  a markup-carrying frontmatter value renders as literal text in that table.
- `web/DESIGN.md:20-22` states that everything the UI displays comes "from the
  same endpoints an SDK would call, filtered by the caller's identity". That is
  true of the catalog endpoints behind `load_domain`, `search_artifacts`, and
  `load_artifact`, and false of `GET /v1/layers`, which returns every layer
  config in the tenant to any caller with no visibility or owner predicate
  (`pkg/registry/server/layers.go:770-777`). The sentence is scoped to the
  catalog endpoints, and the layer-panel section (`web/DESIGN.md:120-147`) states
  that the layer list arrives unfiltered and that the panel's role split is
  presentation over it, which is the same position the Non-goals section takes.
- **The panel-visibility rule.** `web/DESIGN.md:145-147` ends the role split with
  "an anonymous caller sees no
  panel at all". On a registry that configures no identity provider every caller is
  unauthenticated, so under that rule the panel renders for nobody exactly where
  the server admits every layer write. That is the default standalone and
  public-mode posture and the posture §13.10's own web UI targets
  (`spec/13-deployment.md:170`), and it is where the layer writes are admitted
  (`internal/serverboot/serverboot.go:1209-1215`). The sentence is rewritten to
  key on the posture read's `identity_provider_configured`, which it names: when
  the read reports it true, a caller for whom the read returns no `subject` sees
  no panel; when the read reports it false, the panel renders with the full set
  of write operations for every caller, because the server admits them there.
  The administrator arm of the role split stays a
  server decision the page does not predict: the panel renders its write
  operations and presents the not-permitted state on a `403` `auth.forbidden`,
  because no response reports the caller's admin role.
- **The catalog-scope rule.** `web/DESIGN.md:156-158` describes the anonymous
  state as one in which "The
  catalog renders, filtered to public artifacts". On the deployment the bullet
  above names, a registry that configures no identity provider, and in public
  mode, the visibility evaluator short-circuits to true for every layer
  (`pkg/layer/composer.go:53`, `:65`, `spec/04-artifact-model.md:615`,
  `spec/13-deployment.md:33`), so the anonymous view is the full catalog rather
  than a public subset. One further deployment class has no anonymous view at
  all: under
  `injected-session-token`, which is a registry-process provider a web-UI
  registry can run (`spec/13-deployment.md:468`), the meta-tool identity
  middleware verifies before the handler runs and an absent token is a
  verification failure, so every catalog call from a browser holding no
  runtime-signed token returns `401` `auth.untrusted_runtime`
  (`pkg/registry/server/identity_verify.go:44-52`, `:118`,
  `pkg/identity/runtime.go:137-138`). Those two booleans do not distinguish it
  from the `oidc-jwt` and `trusted-headers` case, and the response the read
  returns carries no provider name, so the rule takes the refusal as an arm
  rather than gaining a posture field: the page learns from the catalog response,
  which is the same source the expiry-signal rule below already uses. The
  sentence is rewritten to key on the posture read's
  `identity_provider_configured` and `public_mode`, which it names, and on
  whether the catalog read answers: where a catalog read is refused with `401`,
  there is no anonymous view and the page renders the refused state rather than
  an empty or a filtered catalog, and where a caller who had a `subject` sees
  that refusal it is the expiry transition the expiry-signal rule names; where
  the catalog read answers, the anonymous view is the public subset when the
  read reports `identity_provider_configured` true and `public_mode` false, and
  is the whole catalog on every other combination of the two.
- **The expiry-signal rule.** `web/DESIGN.md:163-164` names "a session expiring
  mid-use while a page is
  already rendered" as a transition the design handles, without naming the
  signal the panel receives. The registry gives a different signal per surface: a
  catalog read returns `401` `auth.token_expired`, and a layer write returns `403`
  `auth.forbidden` because the layer endpoint's resolver discards the
  verification error (`internal/serverboot/identity_verify.go:55-63`). The
  sentence gains that, so the design pass treats the catalog read as the expiry
  signal and does not read a write's `403` as an ownership decision.
- The brief has no authentication affordance. `web/DESIGN.md:163-164` names
  signing in and signing out as transitions, and it was written while §13.10 said
  the UI "runs no acquisition flow of its own", so nothing in the brief's
  surfaces (`web/DESIGN.md:48`), in "What the design pass should produce"
  (`:194-200`), or in "Out of scope" (`:187-192`) gives the design pass a control
  a human clicks. With the brief unamended and the implementor barred from
  designing the UI, U1 would have no source for the surface the new sign-in
  manual scenario requires a human to use. The brief's state-matrix section gains
  it, as a control in the application shell rather than as another entry in the
  surface list, so the surface list and its heading stand. The table below is the
  sign-in control rule, keyed on the posture read's `browser_auth.enabled` and
  `subject`.

  | `browser_auth.enabled` | `subject` | Control rendered |
  |:--|:--|:--|
  | true | absent | sign-in, as a top-level navigation to the read's `sign_in_path` |
  | true | present | sign-out, as a `POST` from the page carrying the same proof the panel's writes carry, after which the page navigates |
  | false | absent or present | neither control |

  Sign-out is a `POST` from the page rather than a navigation because a
  top-level navigation cannot carry the `X-Podium-CSRF` header where the landed
  mechanism requires one, and because `POST` is what the §6.3.4 method predicate
  reads as state-changing. Both conjuncts are required on each of the first two
  rows,
  because the read carries `sign_in_path` and `sign_out_path` only when `enabled`
  is true and each route is registered only where the flow is enabled, so
  rendering a control on any other combination would send the browser to a path
  the mux does not serve, whose response is whatever that deployment answers for
  an unregistered path rather than anything the page can present. "What happens
  when it does not fire" under "The browser session" states what that response
  is. The third row covers the shipped web-UI-only posture, the default
  standalone one, and the gateway-fronted §13.10 deployment, where a subject does
  resolve: the gateway authenticates the request and the registry resolves the
  caller's identity from the forwarded token or the injected headers
  (`spec/13-deployment.md:170`), which is the identity `layerIdentity` returns to
  the posture read (`internal/serverboot/serverboot.go:1198`), while the browser
  flow is off and under `trusted-headers` cannot be enabled at all. Clearing a
  Podium cookie would not end the gateway's own session there.
- `web/DESIGN.md:153-154` says the UI has three identity states and "cannot
  always tell them apart from the client side", which the corrections above now
  depend on being false for two of the three. The sentence is rewritten to state
  what the posture read settles and what it does not: the anonymous and
  authenticated states are distinguished by whether the read returns a `subject`,
  and the administrator state is not reported at all, so the design treats it as
  a server decision surfaced by a refused write rather than as a state the page
  knows before it acts.

## Verification matrix

§11 requires nothing of the UI today. S5 states the obligation, and this matrix
is what it enumerates, so coverage is checked per surface rather than per test
that happens to be written.

**IMPLEMENTOR'S CHOICE:** how a Render cell names a rule that G1 owns. A cell
names the rule and cites G1, and states no condition, field, or value that G1's
statement does not carry, so a clause present in a cell and absent in G1 is a
defect in the cell rather than an extension of the rule.

| Surface | Read | Write | Error | Render |
|:--|:--|:--|:--|:--|
| Domain browser | anonymous and authenticated views differ | — | unreachable registry | nested and folded entries |
| Search | filters reach the endpoint | — | no results | score and sensitivity present and absent |
| Artifact viewer | authenticated sees what anonymous does not | — | not-found for invisible artifact | sanitized markdown, property table, related links; a hostile body and a markup-carrying frontmatter value both render inert |
| Layer panel | list is unfiltered by the server | owner gate refuses `403`; an owner's same-origin write carrying the session cookie and the required proof succeeds, driven server-side by C2 and issued by the panel's own client code in U1 | `registry.read_only` across the panel; a registration whose existence lookup fails is refused with `registry.unavailable` rather than admitted | one-time secret, destructive confirmation; the panel-visibility rule G1 states |
| Session | a cookie-carried token resolves the same identity the header does, and the session cookie a successful callback returns resolves the IdP-issued subject rather than anonymous | sign-in and the callback set cookies and write no registry state; sign-out clears them | on a meta-tool route a cookie past the token's `exp` returns `auth.token_expired`; on a layer write the same cookie resolves anonymous and the owner gate returns `auth.forbidden`; a callback whose exchange the IdP answers and refuses returns `auth.exchange_failed`, an unreachable token endpoint returns `registry.unavailable`, and a callback carrying the IdP's `error` parameter rather than a `code` runs no exchange, sets no session cookie, leaves an existing one intact, and returns the browser to `/ui/` | — |
| Session CSRF | — | every refusal and every admission "The CSRF position" states, driven against each credential the registry accepts and with the browser flow both enabled and disabled | replayed or misdelivered callback refused with `403` `auth.csrf_invalid` | — |
| Posture read | the body reports the deployment's identity provider, public mode, browser-flow enablement, and route paths, and the caller's own subject when one resolves | — | the read is absent on a registry serving no web UI, and the UI renders its anonymous presentation when the read fails | the control G1's sign-in control table gives for each row, including the disabled-with-a-subject row, which is the gateway-fronted arrangement; the catalog-scope rule G1 states |
| Session cookies | every `Set-Cookie` the flow emits carries the attributes its row in the cookie table gives it | the clearing behavior each row states; a sign-out refused for CSRF clears nothing | — | — |
| Credential precedence | a token in the configured header wins; the cookie is read only when the header carries none | — | — | — |
| Session remediation text | — | — | the `auth.token_expired` and `auth.untrusted_token` envelopes name an action a browser session can take, asserted verbatim rather than for non-emptiness | — |

## Testing

- **Owner authorization (C1).** On a registry with an identity provider
  configured, a caller who is neither the owner nor an admin receives `403`
  `auth.forbidden` from `unregister`, `update`, `restore`, `reorder`, and
  `reingest` against a `UserDefined: true` layer; a caller who resolves no subject
  at all receives the same refusal, which is the case §6.3.3 makes reachable by
  treating a request as anonymous during a JWKS outage; the owner succeeds; an
  admin succeeds on any
  layer. A separate case covers `reingest` against an admin-defined layer, where
  the rule S6 states collapses to admin-only, including for a caller the stored
  `Owner` field names, and the
  handler runs no authorization today at all
  (`pkg/registry/server/layers.go:946-991`): an authenticated non-admin and a
  caller resolving no subject each receive `403` `auth.forbidden` and no ingest
  runs, and an admin succeeds. The same case is repeated with a break-glass body,
  because the gate runs ahead of `runIngestAndRespond`, so a break-glass reingest
  is refused for the same caller and bypasses no freeze. The refusal cases install a denying `WithAdminAuth` and a non-owner or
  empty `WithIdentityResolver`, because the bare `NewLayerEndpoint` authorizes
  every caller as admin (`pkg/registry/server/layers.go:174`).
- **Registration takeover (C1).** On the same registry, an authenticated
  non-owner who re-registers an existing user-defined layer's ID receives `403`
  `auth.forbidden` and the stored layer's owner, source, and visibility are
  unchanged; a non-admin who re-registers an existing admin-defined layer's ID
  receives the same refusal rather than converting it to a user-defined layer;
  a non-admin who is the recorded `Owner` of an existing admin-defined layer and
  re-registers that ID with `{"user_defined": true}` receives `403`
  `auth.forbidden`, with the stored layer still carrying `UserDefined: false` and
  its visibility unchanged, which is the case that discriminates the qualified
  owner arm from an unqualified one, because an admin-defined layer's `Owner` is
  set from the request body (`pkg/registry/server/layers.go:659`) and patchable
  (`:547-549`) and so names no authorized subject;
  the owner re-registering their own layer still succeeds; and a registration
  whose ID names no stored layer still succeeds for any authenticated caller.
  One case covers the recovery window, and it is the case a `GetLayerConfig`-only
  lookup fails: alice registers a user-defined layer and unregisters it, bob
  re-registers that ID and receives `403` `auth.forbidden`, and alice's
  subsequent `restore` still succeeds, which asserts both the refusal and the
  tombstone the refusal preserves.
  Two cases cover the degraded store, one against a store whose read of a live
  layer fails with an error that is not `store.ErrNotFound`, and one against a
  store that reports no live layer under the ID while its read of the tenant's
  tombstoned layers fails. The first posts a registration naming a live
  user-defined layer's ID and the second one naming a tombstoned layer's ID, each
  from a non-owner, and each asserts `500` `registry.unavailable` and that the
  stored layer or its tombstone is unchanged, which pins the refusal arm rather
  than the names-no-stored-layer arm. Whichever store calls the implementor's
  lookup makes, both failures are reachable, because the lookup covers both sets.
  The case that pins the implementation is the unauthenticated one: a caller that
  resolves no subject and posts
  `{"id": <an existing user-defined layer's id>, "user_defined": true, "owner": <any value>}`
  receives `403` `auth.forbidden` and the stored layer's owner, source, and
  visibility are unchanged, and the same holds when the posted ID names an
  existing admin-defined layer. `register` is the only gated handler with a
  branch that never reaches `authAdmin`, because the admin check runs only when
  the request body does not already assert `user_defined`
  (`pkg/registry/server/layers.go:610-611`), so an owner comparison placed only
  after `authAdmin` leaves this path open while every other listed case passes.
- **Owner authorization, no identity provider (C1, e2e).** A standalone registry
  unregisters a user-defined layer that was registered with
  `podium layer register --user-defined --owner alice`, because a registry that
  configures no identity provider does not make the gate live. The test first
  reads the stored layer back and asserts its `UserDefined` is true and its
  `Owner` is `alice`, spelled as the list response emits them, because that
  response marshals `store.LayerConfig`, whose fields carry no JSON tags
  (`pkg/store/store.go:267-268`), so the unregister reaches the user-defined branch the carve-out
  governs rather than the admin-defined branch that is already permissive.
  `--user-defined` is required in the invocation, because the CLI sends `owner`
  only inside that branch (`cmd/podium/layer.go:224-227`) and a bare `--owner`
  would register an admin-defined layer with an empty owner. This is the case
  whose carve-out lives in the boot wiring, so it is asserted through the binary.
- **Cookie fallback (C2, unit).** Beside the shipped `oidcJWTVerifier`
  anonymous-resolution cases, in the file C2's call-site enumeration names as
  their home. The same token presented in the configured token header and in
  `__Host-podium_session` resolves an identical `layer.Identity`, including
  `OrgID` and mapped groups. A request carrying both, naming different subjects,
  resolves the header's. A cookie past the token's `exp` returns
  `identity.ErrTokenExpired`, which the meta-tool identity middleware maps to
  `401` `auth.token_expired` (`pkg/registry/server/identity_verify.go:39-55`,
  `:87-94`). A cookie
  request resolves anonymous while the JWKS is unavailable, and a cookie on a
  verifier built with the flow disabled resolves anonymous-public. This bullet
  owns the resolution contract, because that one function is what the
  meta-tool middleware and the layer endpoint both use
  (`internal/serverboot/serverboot.go:1136`, `:1198`). It does not own what each
  consumer does with the returned error; the bullet below pins that.
- **Expired session across surfaces (C1 and C2, integration).** On a registry with the
  browser flow enabled and the owner gate live, a request carrying a session
  cookie past the token's `exp` returns `401` `auth.token_expired` from a
  meta-tool route, and the same cookie on a layer write against a layer the
  cookie's subject owns returns `403` `auth.forbidden`, because
  `layerIdentityResolver` discards the verification error
  (`internal/serverboot/identity_verify.go:55-63`). This pins the split "The
  browser session" states, so a design or a panel built on a single expiry
  signal fails here rather than in the browser.
- **Routes (C2, integration).** Driven over HTTP against a stub IdP token
  endpoint. The cases below are the ones that name an implementation the
  pre-authorization transaction contract does not by itself exclude; the blank
  at the end of the bullet carries the rest.
  - A callback carrying `?error=access_denied` together with either no
    `__Host-podium_auth` cookie or a cookie whose `state` differs from the
    `state` query parameter is refused with `403` `auth.csrf_invalid`, sets no
    session cookie, and emits no `Set-Cookie` for `__Host-podium_session`, so a
    `__Host-podium_session` cookie the browser already holds survives. The case
    runs once per condition, and each run asserts the `403` and the untouched
    session cookie. This is what pins the ordering "The browser session" states,
    that the `state` comparison runs before the `error` branch: an
    implementation that inspects `error` first answers the `/ui/` redirect here
    instead, which lets any third party who can make the victim's browser issue
    a request to the callback path with `?error=` destroy the in-flight
    pre-authorization transaction with no refusal.
  - A callback carrying `?error=access_denied` and no `code`, whose
    `__Host-podium_auth` cookie and `state` both validate, runs no exchange: it
    clears `__Host-podium_auth`, sets no session cookie, and redirects to `/ui/`.
    The case runs once with no `__Host-podium_session` cookie present, asserting
    the redirect and that no `Set-Cookie` for `__Host-podium_session` is emitted,
    and once with a valid `__Host-podium_session` cookie already present,
    asserting that its `Set-Cookie` is not reissued, so the prior session
    survives. Both runs assert the response carries neither `auth.csrf_invalid`
    nor `auth.exchange_failed`. This is the declined-consent outcome the two
    neighboring cases each stop short of: the case above is scoped to the
    mismatched-state and missing-cookie variants, and the `auth.exchange_failed`
    case below requires a `code`, so neither exercises the valid-state,
    IdP-declined path "The browser session" states takes no error code.
  - The sign-in route's `Location` header carries a `nonce` matching the
    `__Host-podium_auth` cookie the same response sets. An ID token carries a
    `nonce` claim only when the authorization request sent one, so an
    implementation that mints a nonce, stores it in the cookie, and omits it from
    the redirect fails here, whereas against a production identity provider the
    stored nonce would be compared against an absent claim and the check would be
    either permanently failing or vacuous. A nonce-mismatch case cannot
    catch that, because the stub issues the ID token the fixture asks for.
    The same case runs with
    `PODIUM_OAUTH_AUTHORIZATION_ENDPOINT` set to a different value and with an
    issuer whose discovery document names a third, so an implementation that
    reads the device-code key, or derives the endpoint from the discovery
    document, or omits or hardcodes `client_id` or `redirect_uri`, fails here
    rather than in a browser against a real IdP. This is what pins the runtime
    half of the §6.3.4 sentence that the device-code option is not read by the
    browser flow; the Guard bullet pins only the startup refusal.
  - The stub token endpoint asserts that the exchange presents the
    `code_verifier` matching the PKCE challenge the sign-in redirect carried, and
    fails the exchange otherwise, so an implementation that stores a verifier and
    never sends it fails here.
  - A callback whose exchange the stub answers with an OAuth error such as
    `invalid_grant` is refused with `502` `auth.exchange_failed`, and the
    response envelope carries `retryable: false` and a
    non-empty `suggested_action`. The refusal discriminates the permanent
    failure from the transient one, which a single
    `registry.unavailable` arm would collapse, and the envelope assertion
    discriminates the staged `errorCodeRegistry` entry from its absence, because
    `enrichEnvelope` returns immediately for an unregistered code
    (`pkg/registry/server/error_envelope.go:88-92`) and leaves exactly the body a
    code-only assertion would accept. It carries the
    `// Matrix: §6.10 (auth.exchange_failed)` annotation.
  - The `__Host-podium_session` cookie a successful callback returns, replayed on
    a subsequent request through the installed `oidcJWTVerifier`, resolves the
    subject the stub IdP issued rather than anonymous. The stub issues an ID
    token whose `aud` is the OAuth client identifier and an access token whose
    `aud` is `PODIUM_OAUTH_AUDIENCE`, so an implementation that puts the ID token
    in the session cookie, or one that exchanges for an access token minted for
    another audience, fails here instead of failing silently in every browser.
  - A valid callback delivered with `Sec-Fetch-Site: cross-site` while a valid
    `__Host-podium_session` cookie is already present, which is the re-sign-in
    case, completes the exchange and returns a `Set-Cookie` replacing that
    session cookie rather than a `403` `auth.csrf_invalid`. The header is what
    makes the case discriminating: a redirect from the identity provider is a
    top-level navigation from that provider's origin, so a browser sends exactly
    that value, and it is the value the CSRF predicate refuses. A callback
    carrying neither `Sec-Fetch-Site` nor `Origin` would be admitted under the
    predicate whether or not the gate covers this route, so it would pin
    nothing. The case fails against an implementation that installs the
    evidence check over the callback, and that implementation is the one an
    operator hits first, because every browser sign-in ends in that request.

  **IMPLEMENTOR'S CHOICE:** which further cases are written, where the cases this
  Testing section describes live, and what each is named. Any answer asserts
  every element the pre-authorization transaction contract under "The browser
  session" states, together with every attribute and clearing rule the cookie
  table gives each row, including the transaction TTL at its default and at an
  overridden value. Each case is discriminating, meaning it names the
  implementation it fails. An element asserted in a case and absent from the
  contract or the cookie table is a defect in the case rather than an extension
  of either. Each case lives in the package that owns the function under test and
  asserts the cookie contract as the cookie table states it rather than restating
  an attribute in the test's own prose.
- **Error envelope entries (C2, unit).** The two new codes join the shipped
  per-code envelope tables in `pkg/registry/server/error_envelope_test.go`:
  `auth.csrf_invalid` and `auth.exchange_failed` are added to
  `TestEnrichEnvelope_RetryableByCode`'s table with `false`
  (`pkg/registry/server/error_envelope_test.go:52-72`) and to
  `TestEnrichEnvelope_SuggestedActionCoverage`'s `withHint` list, which fails on
  an empty `suggested_action` (`:89-110`). This is what makes the
  `errorCodeRegistry` entries S7 stages load-bearing: without these cases an
  implementation that lands the §6.10 entries, the §6.9 rows, and the
  `docs/reference/error-codes.md` rows while omitting the registry entries
  emits an envelope whose `retryable` is false by default and whose
  `suggested_action` is empty, and every route-level case still passes.
- **The restated remediation and message strings (C2, unit).** Nothing pins
  these today: `TestEnrichEnvelope_SuggestedActionCoverage` lists neither
  `auth.token_expired` nor `auth.untrusted_token`, and where it does list a code
  it asserts only that the remediation is non-empty
  (`pkg/registry/server/error_envelope_test.go:89-110`); the route-level cases
  assert `env.Code` and never `env.Message`
  (`internal/serverboot/identity_gateway_integration_test.go:228`, `:243`,
  `:255`, `:380`). An implementation that lands every §6.10 entry, §6.9 row, and
  `docs/reference/error-codes.md` row while leaving the four client-visible Go
  strings naming a gateway passes the whole suite. Both codes join
  `TestEnrichEnvelope_SuggestedActionCoverage`'s `withHint` list, which the
  bullet above extends for the two new codes, so the two additions land as one
  change to that test. Each of the two restated codes also gains
  a case asserting its `suggestedAction` verbatim against the amended spec
  string, in the style the same file already uses for `auth.untrusted_runtime`
  (`pkg/registry/server/error_envelope_test.go:76-84`). A case in
  `pkg/registry/server` drives `writeIdentityError` with an
  `*identity.UntrustedTokenError` carrying an issuer and with one carrying none,
  and asserts each `message` verbatim against the amended
  `spec/06-mcp-server.md:371` and its issuerless fallback. Each case carries
  `// Spec: §6.10`. This is the only new test the credential-location rule
  needs. The other emitted string the rule moves is the boot log line,
  matched by the substring "accepted issuers" (`test/e2e/auth_oidc_jwt_test.go:202`,
  `:277`) and quoted verbatim by the S36 and S44 expectations that T1 restages.
  Every other site the rule moves is a comment or prose that emits nothing, so no
  test can pin it, and those are held by review against the rule, by the §6.10
  mirror obligation, and by re-running the recorded command.
  `test/e2e/auth_oidc_jwt_test.go:202` matches `"accepted issuers "+idp.srv.URL`
  rather than the bare substring, so a restatement that keeps the phrase but
  separates it from the joined issuer list fails that assertion; C2 keeps the
  adjacency, and it owns the assertion if it does not.
- **Any replica serves the callback (C2, integration).** Sign-in runs against one
  endpoint instance and the callback against a second that shares no state, and
  the exchange completes. This is the property the cookie-carried transaction
  buys and the one a store-backed design would have to buy back, so it is
  asserted rather than assumed.
- **Read-only (C2, integration).** With the registry in read-only mode, sign-in,
  the callback, sign-out, and the posture read all behave as they do outside it,
  and an established
  session keeps reading. None of them returns `registry.read_only`. This
  pins the §13.2.1 classification the amended §7 entry states.
- **CSRF (C2).** A forged state-changing request carrying a valid session cookie
  and cross-site browser-origin evidence, meaning `Sec-Fetch-Site: cross-site`
  in one case and an `Origin` naming another host in another, is refused before
  the handler runs with `403` `auth.csrf_invalid`. The same two forgeries are
  driven again with the request authenticated by a `Bearer` token in the
  configured token header and no session cookie, and are refused identically.
  That pair is the gateway-fronted forgery: §13.10 serves the UI behind a
  gateway that converts the browser's own session into that header
  (`spec/13-deployment.md:170`), so a gate scoped to the session cookie passes
  every other case here and admits the forged panel write on the deployment
  §13.10 blesses. One of that pair runs on a registry with the browser flow
  disabled, which pins that the gate reads the request rather than the
  deployment and is not conditional on the flow. Where the chosen mechanism
  carries a cookie, a request
  presenting a valid session cookie, a CSRF cookie, and a request value that
  does not match that cookie is refused with the same status and code. The
  sibling-host forgery, in which a host under the registry's registrable domain
  plants the CSRF cookie and echoes the planted value, is closed by that cookie's
  `__Host-` prefix rather than by a server-side comparison, because a stateless
  double-submit carries nothing the server can distinguish from a value it
  issued; this test asserts the CSRF cookie's `Set-Cookie` against its row in the
  cookie table, and that assertion is what pins the control. A forged sign-out, meaning a cross-site `POST` to the sign-out route, is refused the same way, carries no
  clearing `Set-Cookie`, and leaves the session still authenticating the browser
  on a subsequent request, which pins the sign-out half of the §7 and CSRF
  predicate. It carries the `// Matrix: §6.10 (auth.csrf_invalid)`
  annotation. This test is the one that would fail against the pre-fix design, so
  it is required rather than optional.

  The admitting cases run in the same bullet, because a gate with no admitting
  case is indistinguishable from a gate that refuses everything. A layer write
  carrying a valid session cookie and the proof the chosen mechanism requires,
  meaning a same-origin `Origin` or the `X-Podium-CSRF` header matching
  `__Host-podium_csrf`, succeeds rather than returning `403`. This case builds
  the request itself, so it pins that the server gate admits a correctly proved
  write and asserts nothing about whether the panel sends the proof; the client
  half is pinned by the U1 Surfaces bullet below. It runs a second time in the
  gateway-fronted arrangement, against the same plain-HTTP listener with
  `r.TLS` nil: the request carries `Origin: https://<the value of its own Host
  header>` and `Sec-Fetch-Site: same-origin`, and it is admitted. That run is
  what discriminates the landed predicate from one that compares the scheme,
  which every other case here fails to reach, because an in-process listener
  makes the request scheme and the `Origin` scheme agree. Against a
  scheme-comparing implementation it returns `403` `auth.csrf_invalid`, which is
  what every panel write on an `https` deployment would do. A layer write on
  the same registry authenticated by a `Bearer` token in the configured token
  header, carrying no `Origin`, no `Sec-Fetch-Site`, and no CSRF cookie,
  succeeds, which discriminates the evidence-scoped predicate from an
  unconditional gate that would refuse every CLI and SDK writer once the browser
  flow is enabled. Sign-in driven from a browser that already holds a valid
  `__Host-podium_session` returns the authorization redirect and a fresh
  `__Host-podium_auth` cookie rather than `403` `auth.csrf_invalid`. That case
  runs once carrying `Sec-Fetch-Site: cross-site` and once carrying an `Origin`
  naming another host, because those are the two values the predicate reads: a
  sign-in carrying neither would be admitted whether or not the gate covers the
  route, so it would pin nothing. Both runs fail against an implementation that
  installs the evidence check over the sign-in route, which is the
  implementation that refuses a sign-in reached by a link from any other origin.
  Such an implementation still admits a sign-in entered by direct navigation or
  navigated from the panel's own origin, because those carry
  `Sec-Fetch-Site: none` and `same-origin`, so the recovery an expired session
  takes remains open. The unrecoverable outcome is the callback's, and the
  Routes case pins it there.
- **Posture read (C2, integration and e2e).** The integration cases drive
  `GET /v1/ui/session` with no credential and assert the body per deployment: on
  a registry that configures no identity provider it reports
  `identity_provider_configured` false, `browser_auth.enabled` false, and no
  `subject`; in public mode it reports `public_mode` true; with `oidc-jwt` and
  the browser flow enabled it reports both booleans and a `browser_auth` whose
  `sign_in_path` and `sign_out_path` are the paths the mux registers, which is
  what keeps the UI from spelling a path the mux does not serve; the same request
  carrying a valid session cookie reports that token's subject, and one carrying
  a session cookie past the token's `exp` reports none, which is the resolver's
  fail-closed arm. One end-to-end case asserts through the binary that a registry
  started with `--web-ui` and no browser flow answers the read with
  `browser_auth.enabled` false, and that a registry started without `--web-ui`
  answers `404` on the path. Both binaries configure no identity provider, which
  is what makes `404` the disposition rather than an identity refusal, per "What
  happens when it does not fire" under "The browser session". Without these an implementation that omits the read
  passes every other case here while leaving the UI unable to offer sign-in.
- **Guard (C3, unit + e2e).** A table over each refused
  conjunct: the flow enabled with the web UI disabled; under `oidc-jwt` with each
  acquisition value in turn left empty, which is the client identifier, the
  client credential, the redirect URI, the authorization endpoint, and the token
  endpoint; with `PODIUM_OAUTH_AUTHORIZATION_ENDPOINT` set and
  `PODIUM_WEB_UI_OAUTH_AUTHORIZATION_ENDPOINT` empty, which fails on the same
  conjunct, because the device-code key is not read by the browser flow; with a
  redirect URI that is neither an
  `https` URL nor a loopback `http` URL; alongside `trusted-headers`; alongside
  `injected-session-token`; and with no identity provider selected, which is also
  the public-mode-with-no-provider case and fails on the `oidc-jwt` conjunct.
  Each fails with `config.web_ui_auth_unconfigured` naming the failed conjunct.
  One further case pins the ordering rather than the new code: the flow enabled
  alongside public mode and `oidc-jwt` fails with `config.public_mode_with_idp`,
  because the shipped exclusion is the first check `Validate` runs
  (`pkg/registry/server/config_validate.go:87-90`) and the new guard does not
  displace it, so no configuration reaches
  `config.web_ui_auth_unconfigured` naming the public-mode conjunct. The
  accepting cases are the flow enabled with the web UI on,
  `oidc-jwt`, public mode off, and every acquisition value set, once with an
  `https` redirect URI and once with a loopback `http` one. The combinations that
  enable no browser flow all pass, including the shipped web-UI-only
  configuration, meaning `--web-ui` alone (`cmd/podium/serve.go:38`,
  `internal/serverboot/serverboot.go:1826`). One representative refusal
  runs through the binary for the exit code and the error envelope.
- **Route mount predicate and configuration surface (C2 and C3, e2e).**
  `TestServe_WebUIAuthRouteMount` in
  `test/e2e/server_flag_behavior_test.go`, beside the existing web-UI cases. A
  binary started with the web UI enabled and the browser flow disabled, which is
  the shipped posture (`cmd/podium/serve.go:38`,
  `internal/serverboot/serverboot.go:1229`,
  `internal/serverboot/serverboot.go:1826`), returns `404` on each authentication route
  path, and a stale `__Host-podium_session` cookie sent to it resolves anonymous
  rather than authenticating. That binary configures no identity provider, which
  is what makes `404` the disposition rather than an identity refusal, per "What
  happens when it does not fire" under "The browser session"; the case asserts
  the status on that stack alone and asserts on every stack that no
  authentication route answers. A binary started with the browser flow enabled and
  configured serves the sign-in route, and a binary started the same way with
  `PODIUM_WEB_UI_AUTH_TRANSACTION_TTL` set to a value other than the default
  returns a sign-in `Set-Cookie` whose `Max-Age` is that value, which pins the
  environment read through the boot path. A third binary is started with
  `--web-ui-auth` and `--web-ui-auth-transaction-ttl=<value>` on the command line
  instead, with both environment forms unset, and returns the same two outcomes:
  the sign-in route answers rather than returning `404`, and the sign-in
  `Set-Cookie` `Max-Age` equals the flag's value. That case pins flag
  registration and the flag-to-field assignment in `podium serve`, which the
  environment case does not reach, and it is the level
  `.claude/rules/test-coverage.md` requires for a CLI and boot-path change. The
  shipped `podium serve` flag assertion is a fixed literal list
  (`test/e2e/cli_reference_test.go:261`) and reaches neither new flag. No further
  mount-predicate case is needed: the configuration
  that would discriminate a conjunction from a disjunction, the flow enabled with
  the web UI disabled, is a startup refusal the guard test covers, which is what
  collapsing the predicate onto one validated field buys. This is the predicate
  S45 step 4 depends on, and it lives in the boot wiring, so it is asserted
  through the binary.
- **Build (B1).** Rebuilding the bundle produces no working-tree diff. `go build
  ./...` succeeds with no Node toolchain present.
- **Served bundle (B1, e2e).** A binary started with `PODIUM_WEB_UI=true`
  returns the bundle's `index.html` from `GET /ui/`, and every script and
  stylesheet URL that index references returns `200` from the same running
  binary. This is the level B1 declares and the one that catches an asset base
  path the mount does not serve, which no file-system read of the embedded set
  can catch.
- **Sanitization (U1).** An artifact body carrying a `<script>` element, an
  `<img onerror=…>` attribute, and an `[x](javascript:…)` link renders with no
  executable node and no surviving `javascript:` URL for any of the three. A
  frontmatter value carrying markup renders as literal text in the property
  table rather than as an element. A markdown construct the renderer emits as
  raw HTML is still neutralized, which is the case that pins that the sanitizer
  runs on the rendered output rather than on the source; a sanitizer wired to the
  source passes every other case here and fails this one. A link whose scheme is
  outside the allowlist, such as `data:text/html,…`, renders with no surviving
  URL, which pins the allowlist the blank under "Rendering untrusted content"
  constrains. These are the deny
  paths of a fail-closed control this change introduces, so they are required
  rather than covered by the Render column's well-formed case.
- **No unsanitized markup (B1).** The mechanical check over the web UI's own
  source tree reports no `dangerouslySetInnerHTML` outside the single sanitized
  rendering path, and it runs in the CI job that also runs the rebuild-is-clean
  check. A tree that adds a second occurrence fails that job.
- **Surfaces (U1).** Per the matrix above, driven through the UI's own API calls
  rather than through the CLI. Where the mechanism C2 lands carries a
  request-side value, the Layer panel Write case is issued by the panel's own
  client code and asserts that the outgoing request carries `X-Podium-CSRF` read
  from `__Host-podium_csrf`, so a UI that omits the header fails this case rather
  than only failing in a browser. This is the client half of the gate, and no
  C2 case reaches it, because every C2 case constructs its own request.
  The Posture read row is driven at this level as well, one case per row of G1's
  rendering table against a stubbed read, plus one case for a read that fails.
  The `browser_auth.enabled` false row is stubbed with a `subject` present, which
  is the gateway-fronted arrangement and the case that discriminates a
  one-conjunct sign-out rule from the landed one. The sign-in row asserts that
  the control's target is the read's `sign_in_path`. The failing read renders the
  anonymous presentation with neither control, which pins the failure behavior
  "The posture read" states and which no server-side case reaches.
  Two further cases pin the rendering rules G1 rewrites into the brief, which
  G1's sign-in control table does not carry and which no server-side case
  reaches. The first drives the layer panel against a stubbed read and fails an
  implementation that carries the brief's uncorrected "an anonymous caller sees
  no panel at all" rule forward. The second drives the catalog against a stubbed
  read and a stubbed catalog response, which is what reaches the refused arm,
  and fails an implementation that carries the brief's uncorrected "filtered to
  public artifacts" rule forward.

  **IMPLEMENTOR'S CHOICE:** the stub combinations each of the two cases drives.
  The set covers every arm of the rule as G1 states it, including the arm where
  the panel renders on a registry that configures no identity provider, the arm
  where the anonymous view is the whole catalog under public mode, and the arm
  where the catalog read is refused with `401` and the page renders the refused
  state rather than an empty or a filtered catalog, which is what an
  `injected-session-token` registry returns to a browser, and a
  combination this bullet drives that G1's statement does not distinguish is a
  defect in the bullet rather than an extension of the rule.

## Manual validation

**S44 moves, and it is the test of a convention.** `test/manual-validation.md`
S44 pins the current anonymous behavior and carries a "Known gap this records"
paragraph stating that in-browser authentication is deferred to its own proposal,
written so a later change to the UI has to move that text. This is that change.
S44 is rewritten to assert the authenticated behavior, and its known-gap
paragraph is struck rather than left asserting a deferral that has happened.

S44's stack has to change with it, and that change is staged here rather than
left to whoever rewrites the steps. Prerequisite 4 registers the Keycloak client
as `publicClient=true` with `standardFlowEnabled=false`
(`test/manual-validation.md:3977-3981`), which is a public client with no secret
and the authorization-code flow disabled, and it registers no redirect URI. The
browser flow cannot run against it, and the startup guard refuses to boot without
a client credential, so a human would stop before reaching anything the scenario
asserts. Prerequisite 4 is restaged to create the client with
`publicClient=false` and `standardFlowEnabled=true`, to read the generated secret
back with `kcadm`, and to set `redirectUris` covering the registry's callback
path on `http://127.0.0.1:8153`. It keeps `directAccessGrantsEnabled=true`, the
audience mapper, and the lightweight-token and lifespan attributes, because step
5's password-grant negative control still needs them.

Prerequisite 5 is restaged with it. It mints that control's token with a
direct-access-grant `curl` that presents `client_id=podium` and no client secret
(`test/manual-validation.md:3999-4002`), and a client registered
`publicClient=false` requires client authentication on the token endpoint, so
that request would answer `invalid_client`, `curl -fsS` would exit non-zero, and
`$TOKEN` would be empty before the registry is ever started. The `curl` gains
`-d client_secret="$KC_SECRET"`, reading the secret prerequisite 4 now reads back
with `kcadm`, which is the only change that prerequisite needs. Step 3's serve invocation
(`test/manual-validation.md:4069-4070`) gains `PODIUM_WEB_UI_AUTH`, the
acquisition values including the IdP endpoint keys, and the transaction TTL,
and its bind stays `127.0.0.1:8153`, which is the loopback `http` origin the
redirect-URI conjunct admits.

**S45 step 2's negative clause moves.** Step 2 greps
`docs/reference/http-api.md` and `deploy/runbook.md` for the read-only write set,
expects both to enumerate ingest webhooks, layer admin operations, freeze
toggles, admin grants, and tenant management, and expects neither to name "token
issuance, a login endpoint, or a session table"
(`test/manual-validation.md:4197-4203`). The write-set enumeration is unaffected:
none of the three authentication routes writes registry state, so no write joins
the set and the Expect block's positive list stays complete. The negative clause
is falsified, because the mirror table stages
`docs/reference/http-api.md:13-27` as the new home of the authentication route
paths and the sign-in route is a login endpoint, so a human running the step
after that edit lands reads a document that names one and records a failure. The
clause is narrowed to what stays true: neither document's write-set list names
token issuance or a session table, and the authentication route paths
`docs/reference/http-api.md` now documents sit outside the write set. The "Why by
hand" paragraph is revised on the same axis.

Step 3's probe loop is left alone. S45 runs on the S21 standard-deployment stack
(`test/manual-validation.md:4180-4188`), which serves in strict mode with
Postgres and S3 and enables neither the web UI nor an identity provider
(`:1441-1443`, `:917`). The authentication routes are not mounted there, so a
sign-in probe would return `404` rather than the `registry.read_only` the step's
Expect block asserts. No authentication route is refused in read-only mode on any
stack, because none of the three is in the §13.2.1 write set; the Read-only test
under C2 pins that.

**S45 step 4 moves.** It probes `/v1/login`, `/v1/auth/token`, and `/v1/token`
and expects 404 "because the registry registers no auth, login, or token route".
The new routes falsify the stated reason, and an implementor who mounts one of
the probed paths turns the step into a failure. It is rewritten to probe the
registry's authentication route paths and to expect `404` on each with the
reason the mount predicate gives: this stack enables neither the web UI nor the
browser flow, so the registry registers none of those routes, and it configures
no identity provider, so nothing refuses the probe ahead of route matching. Both
conjuncts are stated, because the status a probe of an unregistered path
receives is the deployment's rather than the route's, per "What happens when it
does not fire" under "The browser session". It keeps what
stays true: the clause struck by proposal 0012 named a write endpoint the
registry does not serve.

**Two expectations quote the startup log line.** S36
(`test/manual-validation.md:2642-2643`) and S44 step 4 (`:4085-4086`) quote the
`oidc-jwt` startup log line verbatim, and C2 restates that line under the
credential-location rule in "The browser session". Both expectations are
restaged to quote the restated line, and T1 carries them.
The restatement keeps "accepted issuers " immediately followed by the joined
issuer list, because `test/e2e/auth_oidc_jwt_test.go:202` matches
`"accepted issuers "+idp.srv.URL` and so requires that adjacency, while `:277`
matches the bare substring. A restatement that holds the adjacency leaves both
assertions passing, and C2 owns them if it breaks the adjacency.

**S36's §6.3.3 restatement moves with them.** The scenario's preamble
(`test/manual-validation.md:2482-2484`) restates the §6.3.3 sends-no-credential
sentence without the "behind such a gateway" qualifier its authoring source
carries, so it reads as a claim about every `oidc-jwt` registry, and it closes
the acquisition path in the same sentence. S3 restates the source and D1 restates
the shipped mirrors, so T1 restates this one on the same axis, under the
credential-location rule in "The browser session". The scenario's own
device-code steps are unchanged, because S36 runs against a registry with no web
UI and no browser flow.

**New scenarios.** Each runs on the S44 stack as this section restages it,
meaning the confidential Keycloak client with the authorization-code flow and a
registered redirect URI, and a registry started with `--web-ui` and the
browser-flow configuration. That is the deployment on which
the routes are mounted and the panel's authenticated role split is reachable.
The scenarios are a sign-in through the UI that yields a view an anonymous caller
does not get, which is where the served authentication routes are exercised; the
layer panel's register flow including the one-time secret; an unregister with its
confirmation; and a non-owner attempting a destructive operation and being
refused. Each names what a human reads on screen, which is the class no Go test
covers, as S44 already established for the anonymous case.

## Non-goals

- Authoring or editing artifacts through the UI.
- Any admin surface beyond the layer panel.
- Changing the catalog and layer endpoints the UI calls, or giving it privileged
  access. The one endpoint this proposal adds for the UI is the unauthenticated
  posture read `GET /v1/ui/session`, which reports deployment posture and the
  caller's own resolved subject and carries no privilege; "The browser session"
  states why the browser can observe neither otherwise.
- Server-side filtering of `GET /v1/layers`. The panel's role split is
  presentation over an unfiltered list; the gate this proposal adds is on writes.
- A server-side session record, a session table, or a session store. The session
  is the IdP token in a cookie, and "The browser session" states why.
- Silent token refresh, and revocation before the token's `exp`. An expired
  session re-runs the sign-in redirect.
- The SDK half of the `DeviceCodeRequired` gap, a separate §6.3 client surface.
- Any behavioral change to `oauth-device-code`, `injected-session-token`, or the
  startup identity guard. The guard's predicate, its refusal, and its error code
  are unchanged, and its doc comment and its startup message stand as written
  under the credential-location rule's verification-configuration clause, because
  the registry verifies the same `aud` claim on the token in either accepted
  location.

## Resolved in adversarial review
Two review runs, both adversarial, neither reaching a clean sweep. The per-pass
detail lived here and was compacted into this summary once it reached a quarter
of the document: every lens reads this section on every round, and it carries no
design obligation. The full pass-by-pass record is in this file's git history.

**Run 1.** Eight rounds, forty-nine findings. Halted by its own introspection
rather than converging, on the reading that the falling finding rate measured
the lens rather than the document and that two thirds of the findings were
conditional on a route decision the maintainer had not settled. It returned
three questions: the authentication route, whether the layer-ownership gap
belonged here, and whether the React bundle is committed or generated.

**The re-draft.** All three were settled, and the proposal was rewritten from
the decided position rather than patched, 1477 lines down to 451.

**Run 2.** Twenty-one rounds, fifty-four findings, five full sweeps, two
redesigns, and five prunes. Halted on two scope questions rather than on a text
defect.

**What the runs changed in the design, rather than in its wording.**

- The registry stores nothing. The draft carried a session store, a §13.1
  topology entry, shared session state, and cross-replica revocation. The cookie
  carries the credential §6.3.3 already accepts rather than one the registry
  mints, so there is no session record, no lifetime to choose, and no new
  persistence surface.
- The layer-ownership gap moved from a code-only fix into §7.3.1, because §7
  already specifies owner authorization and the defect is that the code does not
  implement it.
- Two error codes were named that the draft had specified refusals for without
  naming: `auth.csrf_invalid` and `auth.exchange_failed`.
- The credential-site inventory was added after four consecutive rounds each
  found one site falsified by the third credential. Its own audit then found it
  incomplete in both directions, which is what the inventory exists to make
  possible.
- `web/DESIGN.md` became a second finding source, having never been reviewed.
  Its corrections are staged as G1.

**What the runs established about themselves, and what remains open.** Both
introspections converged on one diagnosis: a mechanism stated in several places
is kept consistent by propagation rather than by having one statement, and the
seams leak on every pass that touches them. The cookie contract and the
credential-location rule were each given a single normative statement and went
quiet afterwards. The remaining duplication is recorded in "Watch out for".

## Relationship to proposal 0012

0012 corrected §13's account of what the registry accepts and does. Its decision
3 verified that the shipped SPA attaches no credential, narrowed the web-UI
paragraph to state that plainly, and routed in-browser authentication here. The
sentence S1 amends is the one 0012 wrote, and the page
`docs/deployment/gateway-delegated-identity.md` names as this proposal's
obligation is the one 0012 recorded.
