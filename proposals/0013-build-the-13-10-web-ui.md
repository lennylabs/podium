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
  server-side session state. "The browser session" below specifies it.
- The layer write handlers gain owner-or-admin authorization. §7 states an owner
  rule today only for reingest and reorder, S6 adds one to §7.3.1 for
  `register`, `unregister`, `update`, and `restore`, and the code implements it
  for none of them. Today any caller can delete or rewrite another user's
  user-defined layer, and a re-registration under an existing layer's ID
  overwrites it without any owner comparison.
- The built React bundle is committed to the tree so `go build` and `go install`
  keep working from a clean clone with only a Go toolchain, with a CI check that
  rebuilding produces no diff.
- The §6.10 catalog gains `auth.csrf_invalid`, for a state-changing
  session-authenticated request that carries no valid proof of same-origin
  intent. `auth.forbidden` is broadened by S6, and `auth.token_expired` keeps its
  scope and has only its remediation string restated, because "the gateway
  forwards a new token" names no action a browser can take.

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
- **The UI gains no privileged access.** It is a client of the existing HTTP API
  and calls the same endpoints an SDK would, as §13.10 states.
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
  `pkg/registry/server/config_validate.go:87-91`) and no shipped guard reads a
  web-UI key. Because the guard makes the web UI a precondition rather than a
  second enablement axis, "browser flow on, web UI off" is a startup refusal
  rather than a route that returns `404`, and the routes mount on one validated
  field inside the block that already mounts `/ui/`
  (`internal/serverboot/serverboot.go:1229`).
- Artifacts stay authored in git. The UI is a reader and a layer manager.

**Watch out for.**

- **CSRF is an obligation this route creates and the proposal specifies it.**
  A cookie authenticates automatically on any request the browser is induced to
  make, so every layer write becomes forgeable across origins in a way a Bearer
  token never was. The prior review of this proposal never produced a finding on
  it across eight rounds while treating it as acknowledged prose, which is how a
  known gap stays open. "The CSRF position" below specifies it.
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
  callback exchanges a code with the IdP and sets a cookie, and sign-out clears
  both. None of the three mutates the store, so all three sit outside the
  §13.2.1 write set under that section's existing rule, with no carve-out and no
  precedent invoked. A read-only registry serves all three unchanged, so an
  operator sees no authentication outage during a primary outage.

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. §13.10's authentication paragraph, bind-guard
      rationale, web-UI configuration keys, and the browser-flow configuration
      guard, per "The edit sites".
      Levels: —. Depends on: —
- [ ] **S2 · spec** — SPEC-2. A new §6.3.4 stating the browser acquisition flow,
      with its pointer from the §6.3 introduction.
      Levels: —. Depends on: S1
- [ ] **S3 · spec** — SPEC-3. §6.3.3's second accepted location for the
      `oidc-jwt` credential, and its header-wins precedence rule.
      Levels: —. Depends on: S2
- [ ] **S4 · spec** — SPEC-4. §7's sign-in, callback, and sign-out routes with
      their cookies, their mount predicate, and their §13.2.1 classification,
      which leaves §13.2.1's own text unchanged.
      Levels: —. Depends on: S2
- [ ] **S5 · spec** — SPEC-5. §11's verification entry for the UI, covering the
      surface-by-obligation matrix below.
      Levels: —. Depends on: S1, S2, S3, S4, S6, S7
- [ ] **S6 · spec** — SPEC-6. §7.3.1's owner-or-admin authorization for the layer
      write handlers, and §7's `auth.forbidden` error enumeration.
      Levels: —. Depends on: —
- [ ] **S7 · spec** — SPEC-7. The new `auth.csrf_invalid` §6.10 and §6.9
      entries, its `tools/matrix/matrices.go` axis entry, and the
      `auth.token_expired` remediation restatement in the §6.10 envelope and the
      §6.9 row. The `auth.forbidden` broadening is S6's.
      Levels: —. Depends on: S3, S4, S6
- [ ] **G1 · docs** — DESIGN-1. The `web/DESIGN.md` corrections in "The design
      handout".
      Levels: —. Depends on: —
- [ ] **C1 · code** — CODE-1. Owner authorization on the layer write handlers,
      with its `403` tests and its no-identity-provider case.
      Levels: unit, integration, e2e. Depends on: S6, S7
- [ ] **C2 · code** — CODE-2. The sign-in, callback, and sign-out routes and
      their two cookies, the `oidcJWTVerifier` cookie branch
      (`internal/serverboot/identity_verify.go:201`) together with the twelve
      `internal/serverboot` test call sites its new parameter moves
      (`identity_gateway_integration_test.go`, `identity_gateway_test.go`, and
      `multitenant_integration_test.go`), the CSRF position below, and the
      `auth.csrf_invalid` envelope entry.
      Levels: unit, integration, e2e. Depends on: S3, S4, S7
- [ ] **C3 · code** — CODE-3. The web-UI authentication configuration guard in
      `StartupConfig.Validate`, including its web-UI, `oidc-jwt`, and
      public-mode conjuncts, its flags and `PODIUM_*` reads, and the nested route
      mount at `internal/serverboot/serverboot.go:1229`.
      Levels: unit, e2e. Depends on: S1, C2
- [ ] **B1 · code** — BUILD-1. The React toolchain, the committed bundle, the
      `go:embed` change, `web/web_test.go`, the served-bundle end-to-end
      assertion, and the rebuild-is-clean CI check.
      Levels: unit, e2e. Depends on: —
- [ ] **U1 · code** — UI-1. The UI surfaces built against `web/DESIGN.md`.
      Levels: unit, e2e. Depends on: B1, C1, C2, G1
- [ ] **D1 · docs** — DOC-1. Every shipped mirror named in "The edit sites".
      Levels: —. Depends on: S1, S2, S3, S4, S6, S7
- [ ] **T1 · test** — TEST-1. The manual scenarios, including the S44 rewrite and
      the S45 step-2 and step-4 rewrites.
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
break-glass freeze bypass. Against an admin-defined layer the rule collapses to
admin-only, which is what §7 already states
(`spec/07-external-integration.md:65`). On `register` the gate is conditional on the request
naming an existing layer in the tenant: a registration whose ID names no stored
layer creates one as it does today, and a registration whose ID names a stored
layer is authorized to that layer's owner or to a tenant admin and is otherwise
refused with `403` `auth.forbidden` rather than upserting. The existence lookup
runs on the same `(tenant_id, id)` key `PutLayerConfig` writes, and both the
lookup and the owner comparison run ahead of the `req.UserDefined` short-circuit
at `pkg/registry/server/layers.go:610-611`, so a request body that asserts
`user_defined` cannot skip the gate the way it skips `authAdmin` today.

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

**Two cookies.** Both carry the `__Host-` prefix, `HttpOnly`, `Secure`, `Path=/`,
and `SameSite=Lax`. The prefix is the browser-enforced binding control: it
forbids a `Domain` attribute and forces `Secure` and `Path=/`, so no sibling host
can plant either cookie, and it is why neither needs a server-side signing key.
`Secure` is unconditional, so the browser flow requires the registry to be
reached over an `https` origin, whether directly or through the gateway that
terminates TLS (`spec/06-mcp-server.md:112`), or over a loopback address. That
is a property of the registry's own origin rather than of the issuer URL, so the
startup guard below carries it as its own conjunct. A CSRF mechanism that needs a
cookie the page can read adds a third cookie. That cookie carries the `__Host-`
prefix as well, and differs from the two above only in omitting `HttpOnly`: the
prefix constrains `Secure`, the absence of a `Domain` attribute, and `Path=/`,
and it places no constraint on `HttpOnly`, so a page-readable cookie keeps the
anti-planting property the paragraph above rests on. The two cookies above are
the session mechanism and carry no CSRF role.

- `__Host-podium_auth` holds the pre-authorization transaction: the `state`, the
  `nonce`, and the PKCE `code_verifier` the sign-in route mints. `SameSite=Lax`
  rather than `Strict`, because the callback is a top-level cross-site
  navigation from the IdP. Its `Max-Age` bounds the sign-in window at 10 minutes
  by default, tunable by `--web-ui-auth-transaction-ttl` /
  `PODIUM_WEB_UI_AUTH_TRANSACTION_TTL` per the key-placement rule under "Where
  configuration keys go". The callback clears it on every outcome, success or
  refusal, which is what makes the transaction single-use.
- `__Host-podium_session` holds the access token and carries no `Max-Age`. Its
  lifetime is bounded server-side by the token's own `exp`, set by the IdP, so
  the registry chooses no second lifetime. Sign-out clears it.

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

**Sign-out** clears both cookies and there is nothing else to clear.

**Enablement, guard, and mount.** `--web-ui-auth` / `PODIUM_WEB_UI_AUTH` is one
boolean, and `PODIUM_WEB_UI_OAUTH_CLIENT_ID`,
`PODIUM_WEB_UI_OAUTH_CLIENT_SECRET`, and `PODIUM_WEB_UI_REDIRECT_URI` are the
acquisition values. `--web-ui-auth-transaction-ttl` /
`PODIUM_WEB_UI_AUTH_TRANSACTION_TTL` carries the sign-in window. All are startup
configuration, read once beside
`internal/serverboot/serverboot.go:1826-1827` and never changed at runtime.
`StartupConfig.Validate` (`pkg/registry/server/config_validate.go:87`) requires,
when the flow is enabled, that the web UI is on, the identity provider is
`oidc-jwt`, public mode is off, the three acquisition values are non-empty, and
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
are never registered, so the catch-all
(`internal/serverboot/serverboot.go:1239`) routes them to the meta-tool handler
and each returns `404`, and `oidcJWTVerifier` ignores the cookie, so a stale
cookie resolves anonymous-public rather than authenticating anyone. A callback
whose `__Host-podium_auth` cookie is absent, expired, or does not match the
returned `state`, and a callback whose exchanged ID token carries a `nonce` other
than the one that cookie holds, are each refused with `403` `auth.csrf_invalid`,
set no session cookie, and clear the pre-authorization cookie. The `nonce`
comparison is a separate check from the `state` comparison: `state` binds the
callback to the browser that started the transaction, and `nonce` binds the ID
token to that same transaction. A cookie carrying an expired
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
identically and the `retryable: true` envelope names no useful action. Its
disposition is open decision OD-5. On every one of these outcomes the callback
sets no session cookie
and still emits the clearing `Set-Cookie` for `__Host-podium_auth`. No error code
is added beyond the `auth.csrf_invalid` the CSRF position already owns and
whatever OD-5 resolves to, and no shipped envelope entry is re-scoped.

**What the mechanism does not change.** The credential is unchanged, so §6.3.1
tenant selection keeps reading the verified `org_id` claim,
`auth.tenant_unknown` keeps populating `details.token_org_id`,
`auth.token_expired` and `auth.untrusted_token` keep their scopes, and §6.3.3's
anonymous-while-JWKS-unreachable rule keeps applying. The only text that moves on
those codes is `auth.token_expired`'s remediation string.

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

Every new registry-process key is a flag and a `PODIUM_*` environment variable
with no config-file key, which is what `PODIUM_WEB_UI` and
`PODIUM_WEB_UI_ALLOW_PUBLIC_BIND` already do
(`internal/serverboot/serverboot.go:1826-1827`), and which §13.12 already records
for `PODIUM_TRUSTED_PROXY_SECRET` and `PODIUM_RUNTIME_KEYS_PATH` as "Environment
only; no config-file key" (`spec/13-deployment.md:479-480`).

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
  This sentence is the source the code comment and the docs mirrors below follow.
- **§13.10** — the web-UI configuration keys, per the rule above, and the
  configuration guard, stated beside the bind-guard sentence. The guard is that
  enabling the browser flow requires `PODIUM_WEB_UI` on,
  `PODIUM_IDENTITY_PROVIDER=oidc-jwt`, public mode off, and the acquisition
  options §6.3.4 marks required, which are the OAuth client identifier, the
  client credential the server-side exchange presents, and the redirect URI
  registered with the IdP, additional to the issuer and audience `oidc-jwt`
  already requires (`spec/06-mcp-server.md:106`). It also requires that redirect
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
  predicate below: it states that a state-changing request authenticated by a
  browser session carries proof of same-origin intent that the registry verifies
  before the handler runs, and that a request without one is refused with `403`
  `auth.csrf_invalid`. Without a spec home the requirement would live only in
  this proposal, and the test that pins it would have no section to cite.
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
  are never merged. Its own restatement at `:94` is inside that range. A
  `trusted-headers` or `injected-session-token` registry reads no cookie, because
  the browser flow cannot be enabled under either and the cookie branch is gated
  on the same enablement field. "The browser session" above states the mechanism.
- **§7** — the sign-in, callback, and sign-out routes, alongside the
  operator-level endpoints §7.3.3 enumerates
  (`spec/07-external-integration.md:152`). Sign-in mints the state, nonce, and
  PKCE verifier, returns them in the `__Host-podium_auth` cookie, and redirects
  to the IdP; the callback reads that cookie, validates the returned `state` and
  the ID token's `nonce` against it, exchanges the code server-side, clears the
  pre-authorization cookie, and returns the access token in the
  `__Host-podium_session` cookie; sign-out clears both cookies. The cookie
  attributes and lifetimes are the ones "The browser session" states. None of the
  three reads or writes registry state, so §13.2.1 classifies all three outside
  the write set and a read-only registry serves them unchanged. The section
  states that sign-out clears the cookies on every request that carries one and
  passes the §6.3.4 same-origin check, and that a sign-out failing that check is
  refused before the handler runs and clears nothing; that is the same predicate
  "The CSRF position" states, and the two sites carry it identically so a forged
  cross-origin sign-out cannot log an operator out.
  The section also states the mount predicate: the three routes are registered
  only where the browser flow is enabled, which the §13.10 guard already makes
  imply the web UI, `oidc-jwt`, public mode off, and the acquisition options, so
  a registry that boots with the flow disabled serves none of them and each path
  returns `404` from the handler the outer mux routes unmatched paths to. The
  routes are registered inside the block that already mounts `/ui/`
  (`internal/serverboot/serverboot.go:1229`), so the deployment predicate is
  written once. The predicate reads startup configuration that is set at boot and
  not changed at runtime, so a registry that boots without the flow never
  acquires the routes and one that boots with it never loses them. This keeps a
  deployment that wants no browser flow, including the shipped web-UI-only
  configuration, free of the routes.
- **§7.3.1 (`spec/07-external-integration.md:95`)** — the user-defined-layer
  paragraph states no owner rule for the write handlers; the only per-handler
  statements are the reorder comment at `:87` and the reingest row at `:65`. It
  gains the general rule: `unregister`, `update`, `restore`,
  `reorder`, and `reingest` on a user-defined layer, and a `register` whose ID
  names a layer that already exists in the tenant, are authorized to that
  layer's owner or to a tenant admin, and a caller who is neither is refused with
  `403` `auth.forbidden`, whether that caller resolves a different subject or
  none at all. The rule is live only where an identity provider is configured and public
  mode is off, so a registry that authenticates no caller keeps admitting the
  request, per `spec/13-deployment.md:33`. The sentence follows the wording §4
  already uses for the parallel re-embed carve-out
  (`spec/04-artifact-model.md:760`). This is the spec basis
  C1 implements, and it is why C1 depends on S6.
- **§7 errors (`spec/07-external-integration.md:97`)** — the closing error
  enumeration scopes `auth.forbidden` to "admin-only operations attempted by a
  non-admin". After S6 the code also reports a caller who is neither an admin nor
  the owner of the user-defined layer being mutated, which is expressly not an
  admin-only operation (`docs/reference/cli.md:440`), so the sentence is
  broadened.
- **§6.10 and §6.9** — a new code `auth.csrf_invalid` for a state-changing
  session-authenticated request that carries no valid proof of same-origin
  intent and for a callback whose pre-authorization cookie does not validate,
  refused with `403`. No existing code covers it: `auth.forbidden` reports an
  authorization decision about the caller, and this refusal is about the request.
  `auth.tenant_unknown` (`spec/06-mcp-server.md:378-388`) is untouched, because a
  session-authenticated request carries a verified `org_id` claim, so the entry's
  scope and its `details.token_org_id` field both stay accurate, and
  `pkg/registry/server/error_envelope.go:73-75` is not edited.
  `auth.token_expired` (`:355-364`) keeps its scope, because a browser session
  presents an `oidc-jwt` token past its `exp` and the entry already covers that.
  Only its remediation moves: the `suggested_action` at `:362` reads "Refresh the
  token. For 'injected-session-token' the runtime reissues it; for 'oidc-jwt' the
  gateway forwards a new token", which is no action a browser can take, so it is
  restated to name re-running sign-in; the §6.9 row at `:327` carries the same
  clause and moves with it, and the code mirror
  `pkg/registry/server/error_envelope.go:67-69` carries the same string and moves
  with it. The mirror also gains the `auth.csrf_invalid` entry. The §6.10 axis in
  `tools/matrix/matrices.go:78-115` gains an `auth.csrf_invalid` entry. That axis
  is hand-maintained rather than derived from `spec/` or from the envelope
  registry, which is why `auth.tenant_unknown` and `auth.untrusted_token` are
  shipped codes with no cell on it, and `matrix-audit` reports only cells the axis
  registers. Adding the entry is what makes the
  `// Matrix: §6.10 (auth.csrf_invalid)` annotation on the CSRF test
  load-bearing; without it the annotation names no cell and the gate stays green
  whether or not the test exists.
- **§13.2.1 (`spec/13-deployment.md:41`)** — the section's rule is per-endpoint
  and per-mutation, and it says each endpoint's own section states its
  classification, so §7's entry states one covering all three routes: sign-in,
  the callback, and sign-out read and write no registry state, so all three are
  outside the write set and a read-only registry serves them unchanged. This is
  an application of the section's existing rule rather than a carve-out, so
  §13.2.1's own text gains nothing and the SCIM-receiver precedent is not
  invoked.
- **§11** — the verification entry, covering the matrix below.

**IMPLEMENTOR'S CHOICE:** the path of each authentication route. Any answer
places them under the existing `/v1/` prefix, uses one path per route, and
appears identically in the §7 entry, in the Authentication section of
`docs/reference/http-api.md`, in the mux registration, in the S45 step-4
rewrite, and in the new sign-in scenario, so every path those scenarios probe
matches the mux.

**Shipped documentation mirrors.** Each restates spec text this amendment
changes, so each moves with it.

| Mirror | What it restates |
|:--|:--|
| `docs/deployment/gateway-delegated-identity.md:105-107` | the §13.10 web-UI account; 0012 recorded this page as this proposal's obligation |
| `docs/deployment/gateway-delegated-identity.md:58` | §6.3.3's "a request carrying no token is anonymous" rule, inside the page's `## oidc-jwt` section. Under `oidc-jwt` a request carrying neither a token in the configured header nor a `__Host-podium_session` cookie is the anonymous case, so the sentence is scoped to both locations, matching the rewritten `:107` on the same page |
| `docs/reference/error-codes.md:59` | `auth.token_expired`, whose remediation clause names a refresh path per provider and has no browser case; the scope sentence stands and the remediation clause gains one |
| `docs/reference/error-codes.md:60` | `auth.forbidden`, scoped to an admin-only operation attempted by a non-admin; the `auth.*` table also gains the `auth.csrf_invalid` row |
| `docs/reference/error-codes.md:69` | the bind guard's `config.web_ui_public_bind_refused`, which the amended §13.10 bind-guard sentence restates; the `config.*` table also gains a `config.web_ui_auth_unconfigured` row stating the browser-flow guard's predicate |
| `docs/reference/http-api.md:13-27` | the Authentication section: the header table, and the account of the accepted registry-process credentials at `:21-27`, which gains the browser session under `oidc-jwt` and the CSRF requirement a state-changing session-authenticated request carries. It is also the new home of the authentication route paths; there is no route list there today |
| `docs/reference/cli.md:131-138` | the `podium serve` synopsis, a closed usage line carrying `--web-ui` and `--web-ui-allow-public-bind`, which gains a token per new web-UI flag |
| `docs/reference/cli.md:142-155` | the `podium serve` flag table, which gains a row per new web-UI flag naming its `PODIUM_*` override, and whose `--web-ui-allow-public-bind` row (`:155`) is restated from the amended §13.10 bind-guard sentence |
| `docs/reference/http-api.md:290` | the register-response example, which prints snake_case keys for a response emitting Go field names |

## The CSRF position

A session cookie authenticates automatically on any request the browser can be
induced to make, so every layer write this proposal exposes becomes forgeable
across origins. A Bearer token was not forgeable that way, so the risk arrives
with the route rather than with the panel.

The position is specified here rather than left to the implementor, because the
prior review treated it as acknowledged prose for eight rounds and never
produced a finding on it.

- Every state-changing request authenticated by a session cookie carries proof of
  same-origin intent that the server verifies, and a request without valid proof
  is refused before the handler runs with `403` `auth.csrf_invalid`.
- Both cookies are `__Host-` prefixed, `HttpOnly`, `Secure`, `Path=/`, and
  `SameSite=Lax`. `Lax` rather than `Strict` is forced by the
  pre-authorization cookie, which has to survive the IdP's cross-site redirect
  back to the callback. `SameSite` is a defense in depth here rather than the
  control, which is why the same-origin proof required above does not rest on it.
  A CSRF mechanism that carries its own cookie adds a third one. It keeps the
  `__Host-` prefix and omits only `HttpOnly`, because the page has to read it and
  the prefix constrains `Secure`, the absence of a `Domain` attribute, and
  `Path=/` rather than `HttpOnly`. Dropping the prefix would let any host under
  the registry's registrable domain plant the CSRF cookie with a `Domain`
  attribute and then forge a state-changing request that echoes the planted
  value, which the session cookie would authenticate.
- The pre-authorization transaction, meaning the state, nonce, and PKCE verifier
  the sign-in route mints, is bound to the browser and single-use, so a callback
  replayed or delivered to a different browser is refused with the same `403`
  `auth.csrf_invalid`. It is the same control on the same axis, which is why it
  reuses the code rather than adding a second one.
- A request whose session cookie carries a token past its `exp` is refused with
  `401` `auth.token_expired`, and one whose token fails signature, `iss`, or
  `aud` with `401` `auth.untrusted_token`, both by the shipped verifier path, on
  the routes that path wraps. A layer write carrying such a cookie resolves
  anonymous instead and is refused `403` `auth.forbidden`, for the reason "The
  browser session" gives.
  Both codes already cover the case as §6.3.3 states them, so neither is
  re-scoped and no code is added. S7 restates only `auth.token_expired`'s
  remediation text, which names no action a browser can take.
- Sign-out is itself state-changing and carries the same protection, because a
  forged sign-out is a denial of service against a signed-in operator. A sign-out
  refused for CSRF returns `403` `auth.csrf_invalid` and clears no cookie.
  Read-only mode does not enter into it: no authentication route is in the
  §13.2.1 write set, so a read-only registry serves sign-out exactly as it serves
  it otherwise.

**IMPLEMENTOR'S CHOICE:** the CSRF mechanism, whether an origin or
`Sec-Fetch-Site` check, a double-submit cookie, or both. A synchronizer token is
not available, because it is a per-session credential the registry would have to
mint and store, and "The browser session" states that the registry mints no
credential and keeps no session record. A double-submit cookie is a third cookie
that the page reads, so it omits `HttpOnly` and keeps the `__Host-` prefix, and
the two session cookies keep both. Any answer refuses a state-changing
request that carries a session cookie and no valid proof of same-origin intent,
is verified before the handler runs rather than inside it, returns the status and
code stated above rather than a mechanism-specific one, and is asserted by a test
that forges the request. An answer that carries a cookie gives that cookie the
`__Host-` prefix, `Secure`, and `Path=/`, and omits only `HttpOnly`, because a
double-submit comparison against a cookie a sibling host can plant admits the
forged request it exists to refuse. The wire contract is fixed here because it appears in
the handler, in §6.10, in `docs/reference/error-codes.md`, and in the test.

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
  The check is scoped to that tree rather than to the repository, because
  `site/` already uses the attribute in several components
  (`site/src/components/content/Tabs.tsx:78`,
  `site/src/components/layout/Lockup.tsx:31` and `:38`,
  `site/src/build/render.ts:132`). The documentation site renders build-time
  authored content into a published static page and serves no registry artifact
  body on the registry's origin, so it is outside this control. A
  repository-wide check would fail on the current tree before any web-UI code
  exists, and would then be deleted or silently rescoped.

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

**The brief itself is corrected first (G1).** The statements listed below are
wrong about the API the design pass would be designing against, so a design
produced from them would be wrong in the same way.

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
- `web/DESIGN.md:20-22` states that everything the UI displays comes "from the
  same endpoints an SDK would call, filtered by the caller's identity". That is
  true of the catalog endpoints behind `load_domain`, `search_artifacts`, and
  `load_artifact`, and false of `GET /v1/layers`, which returns every layer
  config in the tenant to any caller with no visibility or owner predicate
  (`pkg/registry/server/layers.go:770-777`). The sentence is scoped to the
  catalog endpoints, and the layer-panel section (`web/DESIGN.md:120-147`) states
  that the layer list arrives unfiltered and that the panel's role split is
  presentation over it, which is the same position the Non-goals section takes.
- `web/DESIGN.md:145-147` ends the role split with "an anonymous caller sees no
  panel at all". On a registry that configures no identity provider every caller is
  unauthenticated, so under that rule the panel renders for nobody exactly where
  the server admits every layer write. That is the default standalone and
  public-mode posture and the posture §13.10's own web UI targets
  (`spec/13-deployment.md:170`), and it is where the layer writes are admitted
  (`internal/serverboot/serverboot.go:1209-1215`). The sentence is rewritten to
  scope the no-panel rule to a registry that configures an identity provider,
  and to state that on a registry which authenticates no caller the panel renders
  with the full set of write operations, because the server admits them there.
- `web/DESIGN.md:156-158` describes the anonymous state as one in which "The
  catalog renders, filtered to public artifacts". On the deployment the bullet
  above names, a registry that configures no identity provider, and in public
  mode, the visibility evaluator short-circuits to true for every layer
  (`pkg/layer/composer.go:53`, `:65`, `spec/04-artifact-model.md:615`,
  `spec/13-deployment.md:33`), so the anonymous view is the full catalog rather
  than a public subset. The sentence is scoped to a registry that enforces
  visibility, meaning one that configures an identity provider and is not in
  public mode, and states that elsewhere the anonymous view is the whole
  catalog.
- `web/DESIGN.md:163-164` names "a session expiring mid-use while a page is
  already rendered" as a transition the design handles, without naming the
  signal the panel receives. The registry gives a different signal per surface: a
  catalog read returns `401` `auth.token_expired`, and a layer write returns `403`
  `auth.forbidden` because the layer endpoint's resolver discards the
  verification error (`internal/serverboot/identity_verify.go:55-63`). The
  sentence gains that, so the design pass treats the catalog read as the expiry
  signal and does not read a write's `403` as an ownership decision.

## Verification matrix

§11 requires nothing of the UI today. S5 states the obligation, and this matrix
is what it enumerates, so coverage is checked per surface rather than per test
that happens to be written.

| Surface | Read | Write | Error | Render |
|:--|:--|:--|:--|:--|
| Domain browser | anonymous and authenticated views differ | — | unreachable registry | nested and folded entries |
| Search | filters reach the endpoint | — | no results | score and sensitivity present and absent |
| Artifact viewer | authenticated sees what anonymous does not | — | not-found for invisible artifact | sanitized markdown, property table, related links |
| Layer panel | list is unfiltered by the server | owner gate refuses `403` | `registry.read_only` across the panel | one-time secret, destructive confirmation |
| Session | a cookie-carried token resolves the same identity the header does | sign-in and the callback set cookies and write no registry state; sign-out clears them | on a meta-tool route a cookie past the token's `exp` returns `auth.token_expired`; on a layer write the same cookie resolves anonymous and the owner gate returns `auth.forbidden` | — |
| Session CSRF | — | forged state-changing request refused with `403` `auth.csrf_invalid`, including a request whose request value does not match its CSRF cookie | replayed or misdelivered callback refused with the same code | — |
| Session cookies | both session cookies carry `__Host-`, `HttpOnly`, `Secure`, `Path=/`, and `SameSite=Lax`, a CSRF cookie carries the same attributes without `HttpOnly`, and the pre-authorization cookie's `Max-Age` is the configured transaction TTL | the callback clears the pre-authorization cookie on every outcome; sign-out clears both; a sign-out refused for CSRF clears nothing | — | — |
| Credential precedence | a token in the configured header wins; the cookie is read only when the header carries none | — | — | — |

## Testing

- **Owner authorization (C1).** On a registry with an identity provider
  configured, a caller who is neither the owner nor an admin receives `403`
  `auth.forbidden` from `unregister`, `update`, `restore`, `reorder`, and
  `reingest` against a `UserDefined: true` layer; a caller who resolves no subject
  at all receives the same refusal, which is the case §6.3.3 makes reachable by
  treating a request as anonymous during a JWKS outage; the owner succeeds; an
  admin succeeds on any
  layer. A separate case covers `reingest` against an admin-defined layer, where
  the rule collapses to admin-only (`spec/07-external-integration.md:65`) and the
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
  the owner re-registering their own layer still succeeds; and a registration
  whose ID names no stored layer still succeeds for any authenticated caller.
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
- **Cookie fallback (C2, unit).** `TestOIDCJWTVerifier_SessionCookie` in
  `internal/serverboot/identity_verify_test.go`, which owns `oidcJWTVerifier`.
  The same token presented in the configured token header and in
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
- **Routes (C2, integration).** In a new `pkg/registry/server` test driven over
  HTTP against a stub IdP token endpoint: a callback with no pre-authorization
  cookie and a callback whose `state` does not match are each refused with `403`
  `auth.csrf_invalid` and set no session cookie; a callback whose `state` matches
  the pre-authorization cookie but whose stub-issued ID token carries a different
  `nonce` is refused with `403` `auth.csrf_invalid` and sets no session cookie,
  which is the check that makes the unsigned pre-authorization cookie sound and
  is independent of the `state` comparison; the stub token endpoint asserts that
  the exchange presents the `code_verifier` matching the PKCE challenge the
  sign-in redirect carried, and fails the exchange otherwise, so an
  implementation that stores a verifier and never sends it fails the test; a
  callback whose pre-authorization cookie validates but whose exchange the stub
  token endpoint cannot answer, because it is unreachable or returns `5xx`, is
  refused with `registry.unavailable` (the OAuth-error refusal carries whatever
  code OD-5 resolves to and is asserted with it), sets no session cookie, and
  still emits the clearing `Set-Cookie` for `__Host-podium_auth`; the callback
  emits that clearing `Set-Cookie` on every refusal above and on the success as
  well, which is the
  every-outcome clearing rule "The browser session" states; sign-out clears both
  cookies; and the two session cookies' `Set-Cookie` headers
  carry the `__Host-` prefix, `HttpOnly`, `Secure`, `Path=/`, and `SameSite=Lax`,
  with no `Max-Age` on the session cookie. The sign-in route's
  `__Host-podium_auth` `Set-Cookie` carries a `Max-Age` equal to the configured
  transaction TTL, asserted once at the default and once with the endpoint
  constructed with a different TTL, so the default and the override are
  distinguished. Where the chosen CSRF mechanism carries a cookie, that
  cookie's `Set-Cookie` carries the `__Host-` prefix, `Secure`, `Path=/`, and
  no `HttpOnly`.
- **Any replica serves the callback (C2, integration).** Sign-in runs against one
  endpoint instance and the callback against a second that shares no state, and
  the exchange completes. This is the property the cookie-carried transaction
  buys and the one a store-backed design would have to buy back, so it is
  asserted rather than assumed.
- **Read-only (C2, integration).** With the registry in read-only mode, sign-in,
  the callback, and sign-out all behave as they do outside it, and an established
  session keeps reading. None of the three returns `registry.read_only`. This
  pins the §13.2.1 classification the amended §7 entry states.
- **CSRF (C2).** A forged state-changing request carrying a valid session cookie
  and no valid proof is refused before the handler runs with `403`
  `auth.csrf_invalid`. Where the chosen mechanism carries a cookie, a request
  presenting a valid session cookie, a CSRF cookie, and a request value that
  does not match that cookie is refused with the same status and code. The
  sibling-host forgery, in which a host under the registry's registrable domain
  plants the CSRF cookie and echoes the planted value, is closed by the cookie's
  `__Host-` prefix rather than by a server-side comparison, because a stateless
  double-submit carries nothing the server can distinguish from a value it
  issued; the Routes bullet above asserts that prefix, and that assertion is
  what pins the control. A forged sign-out is refused the same way, carries no
  clearing `Set-Cookie`, and leaves the session still authenticating the browser
  on a subsequent request, which pins the sign-out half of the §7 and CSRF
  predicate. It carries the `// Matrix: §6.10 (auth.csrf_invalid)`
  annotation. This test is the one that would fail against the pre-fix design, so
  it is required rather than optional.
- **Guard (C3, unit + e2e).** `TestStartupConfig_WebUIAuthUnconfigured` in
  `pkg/registry/server/config_validate_test.go`, a table over each refused
  conjunct: the flow enabled with the web UI disabled; under `oidc-jwt` without
  each of the three acquisition options; with a redirect URI that is neither an
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
  `oidc-jwt`, public mode off, and all three options set, once with an `https`
  redirect URI and once with a loopback `http` one. The combinations that
  enable no browser flow all pass, including the shipped web-UI-only
  configuration (`test/manual-validation.md:4070`). One representative refusal
  runs through the binary for the exit code and the error envelope.
- **Route mount predicate (C3, e2e).** `TestServe_WebUIAuthRouteMount` in
  `test/e2e/server_flag_behavior_test.go`, beside the existing web-UI cases. A
  binary started with the web UI enabled and the browser flow disabled, which is
  the shipped posture (`internal/serverboot/serverboot.go:1229`,
  `test/manual-validation.md:4070`), returns `404` on each authentication route
  path, and a stale `__Host-podium_session` cookie sent to it resolves anonymous
  rather than authenticating. A binary started with the browser flow enabled and
  configured serves the sign-in route, and a binary started the same way with
  `PODIUM_WEB_UI_AUTH_TRANSACTION_TTL` set to a value other than the default
  returns a sign-in `Set-Cookie` whose `Max-Age` is that value, which is what
  pins the flag and the environment read through the boot path. No further
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
- **Surfaces (U1).** Per the matrix above, driven through the UI's own API calls
  rather than through the CLI.

## Manual validation

**S44 moves, and it is the test of a convention.** `test/manual-validation.md`
S44 pins the current anonymous behavior and carries a "Known gap this records"
paragraph stating that in-browser authentication is deferred to its own proposal,
written so a later change to the UI has to move that text. This is that change.
S44 is rewritten to assert the authenticated behavior, and its known-gap
paragraph is struck rather than left asserting a deferral that has happened.

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
browser flow, so the registry registers none of those routes. It keeps what
stays true: the clause struck by proposal 0012 named a write endpoint the
registry does not serve.

**New scenarios.** Each runs on a stack that enables `--web-ui` and configures
`oidc-jwt` with the browser flow, as S44 does, which is the deployment on which
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
- Changing the endpoints the UI calls, or giving it privileged access.
- Server-side filtering of `GET /v1/layers`. The panel's role split is
  presentation over an unfiltered list; the gate this proposal adds is on writes.
- A server-side session record, a session table, or a session store. The session
  is the IdP token in a cookie, and "The browser session" states why.
- Silent token refresh, and revocation before the token's `exp`. An expired
  session re-runs the sign-in redirect.
- The SDK half of the `DeviceCodeRequired` gap, a separate §6.3 client surface.
- Any change to `oauth-device-code`, `injected-session-token`, or the startup
  identity guard.

## Resolved in adversarial review

### Pass 1 (2026-08-22, automated)

- **The design brief describes layer visibility as a single value.** `web/DESIGN.md`
  is added to the staged edits as G1, and "The design handout" states the
  correction: visibility is a union of `Public`, `Organization`, `Groups`, and
  `Users`, and the layer-panel section states the treatment for a layer that
  matches on more than one axis.
- **The design brief says everything displayed is identity-filtered.** The same
  G1 entry scopes that sentence to the catalog endpoints and states that
  `GET /v1/layers` arrives unfiltered, matching the Non-goals position.
- **The `podium serve` flag reference was in no edit list.** The mirror table
  gains `docs/reference/cli.md:131-138` and `:142-155`, so each new web-UI flag
  gets a synopsis token and a table row naming its `PODIUM_*` override, and the
  `--web-ui-allow-public-bind` row is restated from the amended bind-guard
  sentence.
- **`auth.forbidden`'s definitions are admin-only.** S6 adds the §7.3.1
  authorization sentence C1 implements and broadens the §7 error enumeration at
  `spec/07-external-integration.md:97`; the mirror table gains
  `docs/reference/error-codes.md:60`; C1 now depends on S6.
- **The owner gate was unspecified where no caller is authenticated.** "What
  lands" states that the gate is live only where an identity provider is
  configured and public mode is off, mirroring the admin gate's short-circuit and
  §4's re-embed carve-out, and Testing gains an e2e case for a
  standalone registry unregistering a layer registered with `--owner`.
- **The named reingest integration test does not fail.** The claim is struck in
  the Summary, in "What lands", and in Testing, and replaced with the reason it
  keeps passing and with the surface that does regress.
- **The CSRF and session refusals named no §6.10 error code.** A new code
  `auth.csrf_invalid` (`403`) covers the forged state-changing request and the
  replayed or misdelivered callback; `auth.token_expired` (`401`) is restated to
  cover an expired or unknown session. S7 stages the §6.10 and §6.9 entries, the
  `docs/reference/error-codes.md` rows, and the
  `pkg/registry/server/error_envelope.go` entries.
- **The CSRF requirement landed in no spec edit site and no matrix cell.** §6.3.4
  is named as its spec home, the verification matrix gains a Session CSRF row,
  and the staged `docs/reference/http-api.md` Authentication edit carries the
  client-visible requirement.
- **Precedence between a session cookie and a forwarded credential was
  unstated.** The §6.3.3 edit states that the session cookie is read and the
  forwarded token or injected headers are ignored rather than merged, with a
  matrix row and a test in which the two name different subjects.
- **`auth.tenant_unknown` was re-scoped only in the docs mirror.** S7 stages
  `spec/06-mcp-server.md:378-388`, the §6.9 row at `:330`, and the envelope
  remediation text, so the source and the mirror agree.
- **Sign-out was classified as a read-only-rejected write.** §13.2.1 places
  sign-out outside the write set on the SCIM-receiver precedent, §7 states that
  the cookie is cleared on every sign-out that carries one, the Summary states
  both halves and the residual, and the Read-only test asserts what sign-out
  returns.
- **No test pinned the cookie attributes.** Testing gains a Session cookie case
  asserting `HttpOnly`, `Secure`, and the chosen `SameSite` on the callback's
  `Set-Cookie` and on the sign-out clearing, with a matching matrix row.
- **The repository-wide `dangerouslySetInnerHTML` check is false today.** The
  control is scoped to the web UI's own source tree, and the `site/` occurrences
  are named with the reason they are outside it.
- **`web/web_test.go` was in no edit list.** B1 gains it with its disposition,
  the build blank gains the constraint that `web.Assets()` is rooted at the
  served bundle, and "The gap" is corrected to name the §13.10 citations that
  exist.
- **S45 step 2 was falsified by the write-set mirror edits.** The manual-validation
  section stages the step-2 Expect rewrite, the "Why by hand" revision, and the
  step-3 probe addition, and T1 names them.
- **The `http-api.md` mirror range stopped short of the credential account.** The
  row is widened to `:13-27` and the claim of an existing route list is dropped.

Corrections to this pass, from the review of its own edits:

- **The new `auth.csrf_invalid` had no matrix cell.** The §6.10 axis in
  `tools/matrix/matrices.go:78-115` is hand-maintained rather than derived, and
  `matrix-audit` reports only the cells it registers, so the annotation was inert.
  S7 and the §6.10/§6.9 edit site now stage the axis entry, and the claim that
  `matrix-audit` checks the code is conditioned on it.
- **The owner-gate predicate was per-request rather than per-deployment.** As
  written it admitted an anonymous caller on a registry that has an identity
  provider, which is the hole the finding was raised about and which §6.3.3 makes
  reachable during a JWKS outage. "What lands", the staged §7.3.1 sentence, and
  the "Watch out for" entry now key the gate on the deployment, matching
  `internal/serverboot/serverboot.go:1213` and `spec/04-artifact-model.md:760`,
  and the C1 test bullet gains the unauthenticated-caller refusal.
- **The Summary still credited §7 with the owner authorization.** It now states
  what the body states: §7 has the rule for reingest and reorder only, S6 adds it
  to §7.3.1 for the rest, and the code implements none of it.
- **S7 claimed an `auth.forbidden` restatement §6.10 has no entry for.** §6.10
  mentions the code only inside the §7.3.3 paragraph, and the broadening is staged
  under S6 in `spec/07-external-integration.md:97` with its
  `docs/reference/error-codes.md:60` mirror. S7's description drops it.

### Pass 2 (2026-08-22, automated)

- **The design brief hid the layer panel on the deployment §13.10 targets.**
  G1 gains a correction for `web/DESIGN.md:145-147`, which ended the role split
  with "an anonymous caller sees no panel at all" while the server admits every
  layer write on a registry that configures no identity provider. The no-panel
  rule is scoped to a registry that configures one, and the manual-validation
  section names the stack the new layer-panel scenarios run on.
- **`register` was outside the ownership gate.** `POST /v1/layers` upserts on
  `(tenant_id, id)` with no owner comparison, so an authenticated non-admin
  could overwrite another user's layer or convert an admin-defined one.
  "The layer-ownership defect", "What lands", the staged §7.3.1 sentence, and
  the Summary now cover it, gated on the request naming an existing layer, and
  Testing gains a registration-takeover case.
- **The §13.12 `PODIUM_MULTI_TENANT` row restated the tenant-rejection rule.**
  `spec/13-deployment.md:504` is added to the edit sites under S3, so §13.12
  stops stating a narrower rejection rule than the amended §6.3.1 and §6.10.
- **The served bundle's title and asset base path were unconstrained.** B1 names
  `cmd/podium/serve_ui_test.go:51` and `test/e2e/server_flag_behavior_test.go:30`
  alongside `web/web_test.go`, constrains the built `index.html` to keep
  `<title>Podium</title>` so all three stand unchanged, requires the bundle's
  asset references to resolve under the `/ui/` mount, and gains an end-to-end
  case that fetches `/ui/` and every asset it references.
- **The authentication routes had no mount predicate, so the staged S45 edits
  were undetermined.** The §7 edit site states that the routes are registered
  only when both the web UI and the browser flow are enabled, following the
  predicate the `/ui/` mount already uses. The step-3 probe addition staged in
  pass 1 is withdrawn, because S45's stack enables neither and a sign-in probe
  would return `404` rather than `registry.read_only`; step 4 is rewritten to
  expect `404` with the mount predicate as its reason; the served routes are
  exercised by the new sign-in scenario instead; and C3 gains an end-to-end case
  pinning the predicate.
- **The callback carried no §13.2.1 classification.** The §13.2.1 edit site now
  classifies all three routes: sign-in and the callback are inside the write set
  and a read-only registry refuses each with `registry.read_only`, sign-out stays
  outside it, the write-set mirror row and the Summary state the same split, and
  Testing gains the mid-flow callback case.
- **`docs/deployment/gateway-delegated-identity.md:58` was in no edit list.** It
  restates §6.3.3's "a request carrying no token is anonymous" rule, which the
  amendment falsifies for a session-authenticated browser request, so the mirror
  table gains the row.
- **§13.1's docs mirror was in no edit list.** The mirror table gains
  `docs/deployment/clustered.md:15-24`, so the session store lands in the
  reference topology a clustered operator sizes the deployment from.
- **The route-path constraint named S45 step 2, which carries no route path.**
  Step 2 is two greps over shipped documents. The constraint now names the step-4
  rewrite and the new sign-in scenario.
- **`auth.token_expired`'s remediation text was left unstaged.** The spec range
  widens to `spec/06-mcp-server.md:355-364` so the envelope's `suggested_action`
  moves with the prose, and the code mirror
  `pkg/registry/server/error_envelope.go:67-69` is staged alongside `:73-75` on
  the reasoning already given for `auth.tenant_unknown`.

### Pass 3 (2026-08-22, automated)

- **The §7 sign-out sentence and the CSRF position stated incompatible
  cookie-clearing predicates.** §7 directed the applied spec to clear the cookie
  on every sign-out request carrying one, while the CSRF position refused a
  cross-origin sign-out and cleared nothing, so the same request had two
  answers. The §7 edit site now carries the same predicate the CSRF position
  states: the cookie is cleared on every sign-out that carries one and passes
  the §6.3.4 same-origin check, and a sign-out that fails that check is refused
  before the handler runs and clears nothing. The verification matrix's Session
  cookie row, the Summary's §13.2.1 entry, and the Session cookie test bullet
  carry the same qualifier, and the CSRF test bullet gains the forged sign-out
  with its absent clearing `Set-Cookie`.
- **The browser flow's enablement was bound to no identity provider.** As
  written, the flow could be enabled alongside public mode, `trusted-headers`,
  `injected-session-token`, or no provider, because the shipped exclusion is
  keyed on `PODIUM_IDENTITY_PROVIDER` alone
  (`pkg/registry/server/config_validate.go:87-91`,
  `spec/13-deployment.md:484`) and no guard read a web-UI key. The fixed
  decision, the §13.10 edit site, the §6.3.3 edit site, the §7 mount predicate,
  and the C3 test bullet now state one predicate: the browser flow requires
  `oidc-jwt` with public mode off and the acquisition options §6.3.4 marks
  required, every other combination fails startup with
  `config.web_ui_auth_unconfigured` in `StartupConfig.Validate`, the session
  credential is accepted only under `oidc-jwt`, and the routes mount only when
  the web UI, the browser flow, and `oidc-jwt` are all in force. The mirror
  table's `docs/reference/error-codes.md` row gains the new `config.*` entry,
  and the checklist records C3's new dependency on S1.
- **The mount-predicate test exercised only the two configurations a
  conjunction and a disjunction agree on.** The Route mount predicate bullet
  gains the discriminating cases: the web UI enabled with the browser flow
  disabled, which is the shipped posture
  (`internal/serverboot/serverboot.go:1229`, `test/manual-validation.md:4070`),
  and the browser flow enabled while the web UI is disabled, each returning
  `404` on every authentication route path.

### Pass 4 (2026-08-22, automated)

- **The no-identity-provider e2e case used an invocation that creates no owned
  user-defined layer.** `podium layer register` sends `owner` only inside the
  `--user-defined` branch (`cmd/podium/layer.go:224-227`), so the bare
  `--owner alice` form registered an admin-defined layer with an empty owner and
  exercised the pre-existing admin branch instead of the carve-out. "What lands"
  and the e2e test bullet now name `--user-defined --owner alice`, and the test
  asserts the stored layer's `UserDefined` and `Owner` before unregistering it.
- **Correction to this pass: the read-back assertion was spelled in
  snake_case.** `store.LayerConfig` carries no JSON tags on `UserDefined` or
  `Owner` (`pkg/store/store.go:267-268`), so the list and register responses
  emit Go field names, which is the same fact the mirror table records when it
  stages `docs/reference/http-api.md:290`. The e2e bullet now names the fields as
  the response emits them. The request body in the registration-takeover bullet
  stays snake_case, because `LayerRegisterRequest` is tagged
  (`pkg/registry/server/layers.go:297-298`).
- **The registration-takeover test omitted the unauthenticated overwrite.**
  `register` skips `authAdmin` whenever the request body asserts `user_defined`
  (`pkg/registry/server/layers.go:610-611`), which is the one branch a caller
  resolving no subject controls and the branch the staged §7.3.1 sentence covers
  with "or none at all". The test bullet gains that case for an existing
  user-defined layer's ID and for an existing admin-defined layer's ID, and
  "What lands" states that the existence lookup and the owner comparison run
  ahead of the `req.UserDefined` short-circuit.
- **B1 attributed the preserved §13.10 annotations to a check the gate does not
  run.** `make coverage-gate` runs `speccov drift`, which fails only on a
  citation naming a section that no longer exists
  (`tools/speccov/main.go:132-133`, `Makefile:284`); `speccov uncovered`
  (`tools/speccov/main.go:112-113`) is not in the gate. The bullet now keeps the
  annotations as an explicit deliverable and states that no gate enforces them.

### Redesign 1 (2026-08-22, automated)

The redesigned area is the browser-session mechanism: what the credential is,
where it lives, who reads it, how the routes are enabled and mounted, and what
the §13.2.1 classification is. It was redesigned because the proposal named a
session store in five places and designed it in none, specified the session a
facet at a time across six spec sections, and carried a §13.2.1 split that needed
an outage residual to stay coherent. "The browser session" is the new single home
of the mechanism, and every edit site points at it rather than restating it.

- **The session store was named in five places and designed in none.** It carried
  a cross-replica revocation guarantee with no storage home, no lifetime, no key,
  and no §9.1 SPI edit, in a repository whose rules treat the SPI table as a
  mirrored surface. The store is deleted. The session is the IdP access token in
  a `__Host-` prefixed `HttpOnly` cookie, the pre-authorization transaction is a
  second such cookie, and the registry keeps no record. Revocation is the token's
  `exp`, which is the model §6.3.3's verification paragraph already carries: it
  checks the signature, `iss`, `aud`, and the `exp`/`nbf` window and consults no
  revocation list (`spec/06-mcp-server.md:98`).
- **The session was specified a facet at a time across six sections.** The
  §6.3.1, §13.12, §13.1, and `auth.tenant_unknown` edit sites are deleted,
  because a cookie-carried `oidc-jwt` token satisfies every one of those
  sentences as written. This reverses pass 1's `auth.tenant_unknown` entry, pass
  2's §13.12 and §13.1 entries, and the mirror rows those entries added.
  `auth.token_expired` keeps its scope and loses only its remediation string.
- **Precedence ran the wrong way.** Pass 1 resolved that the session cookie is
  read and a forwarded token ignored. Under a cookie that carries the same
  `oidc-jwt` credential there is nothing to arbitrate between two credential
  kinds, and a gateway that authenticated the request is the authority in that
  deployment, so the header wins and the cookie is read only when the header
  carries none.
- **The §13.2.1 classification split the three routes and needed a residual.**
  None of them writes registry state, so all three are outside the write set
  under the section's existing rule, the SCIM precedent is not invoked, the
  outage residual disappears, and S45 step 2's write-set enumeration stays as
  written. That step's negative clause still moves, because the route paths land
  in `docs/reference/http-api.md`. This reverses pass 2's
  callback-classification entry and pass 1's sign-out entry.
- **The mount predicate restated conjuncts the guard already enforces.** The
  §13.10 guard gains `PODIUM_WEB_UI` as a conjunct, so the routes mount on one
  validated field nested inside the block that already mounts `/ui/`, and the
  configuration pass 3 added to the mount-predicate test as a discriminating
  case is now a startup refusal. This reverses the second half of pass 3's
  mount-predicate entry.

**What the redesign deletes outright.** The session store in every site that
named it, and with it cross-replica sign-out revocation and the outage residual;
the §13.1 topology edit site and its `docs/deployment/clustered.md:15-24` mirror
row; the §6.3.1 edit site, the §13.12 `PODIUM_MULTI_TENANT` edit site, and the
`auth.tenant_unknown` restatement in §6.10, §6.9, and
`pkg/registry/server/error_envelope.go:73-75`, with the mirror rows
`docs/deployment/gateway-delegated-identity.md:97`,
`docs/deployment/oidc/index.md:67`, and `docs/reference/error-codes.md:58`; the
write-set mirror row covering `docs/reference/http-api.md:633`,
`docs/deployment/operator-guide.md:132`, and `deploy/runbook.md:19`; the
read-only refusal of sign-in and of the callback and the mid-flow callback test
case; the rule that a session cookie wins over a forwarded token; the
synchronizer token as a permitted CSRF answer and the wire-level requirement that
the CSRF proof be a token; the third case in the mount-predicate test; and the
Tenancy test bullet, which asserted a §6.3.1 sentence the proposal no longer
stages.

**Open decisions this redesign records.**

- **OD-1. The session cookie's `Max-Age`.** No `Max-Age`, so the token's `exp`
  bounds the session server-side, or a `Max-Age` computed from the token's
  remaining `exp`, so the browser drops the cookie at the same instant. With no
  `Max-Age` a long-lived browser keeps presenting an expired token and receives
  `401` `auth.token_expired` on the meta-tool routes, and `403` `auth.forbidden`
  on a layer write, until sign-in re-runs; with a computed one the
  request resolves anonymous instead, at the cost of computing the value and
  asserting it. Default: no `Max-Age`.
- **OD-2. Whether the verifier's cookie read is gated on the enablement field.**
  Gating leaves every deployment that does not enable the flow behaving as it
  does today and makes the stale-cookie assertion meaningful, at the cost of one
  parameter at one production call site plus the twelve `internal/serverboot`
  test call sites that construct the function directly. Reading the cookie
  unconditionally under `oidc-jwt` removes the parameter and widens the accepted
  credential transport for deployments that never opted in. Default: gate it.
- **OD-3. The oversized-token failure.** Say nothing, leaving an access token too
  large for one cookie to the implementation, or classify it, which means a
  non-transient error code with its §6.10 entry, its §6.9 row, its
  `error_envelope.go` entry, its `docs/reference/error-codes.md` row, its
  `tools/matrix/matrices.go` axis cell, and a test. `registry.unavailable` is not
  an option, because its envelope carries `retryable: true` and every retry fails
  identically. Default: say nothing.
- **OD-4. The route paths.** Unchanged, and still recorded as IMPLEMENTOR'S
  CHOICE above. No edit site carries a literal path, so a later pass that fixes
  the paths substitutes into the §7 entry, the `docs/reference/http-api.md`
  Authentication section, the mux registration, the S45 step-4 rewrite, and the
  new sign-in scenario.
- **OD-5. The code for an exchange the IdP refuses.** An unreachable IdP or a
  `5xx` from its token endpoint is transient and takes `registry.unavailable`. An
  IdP that answers and refuses the exchange with an OAuth error such as
  `invalid_grant`, or because the configured client credential is wrong, is
  permanent for that request, so `registry.unavailable` is disqualified for the
  same reason it is disqualified for the oversized token. The options are staging
  a new non-transient code with its full mirror set, or re-scoping
  `registry.unavailable` across those mirrors to cover a permanent dependency
  refusal. Default: stage a new non-transient code, because re-scoping a shipped
  retryable entry changes what existing callers are told. Naming the code is the
  decision.

### Pass 5 (2026-08-22, automated)

- **The CSRF cookie was barred from the `__Host-` prefix on a false premise.**
  The prefix constrains `Secure`, the absence of a `Domain` attribute, and
  `Path=/`, and says nothing about `HttpOnly`, so a page-readable cookie can
  carry it. Without it any host under the registry's registrable domain can
  plant the CSRF cookie and forge a request that echoes the planted value, which
  defeats the gate. "The browser session", the CSRF position bullet, and the
  IMPLEMENTOR'S CHOICE constraint now state that a CSRF cookie keeps the prefix
  and omits only `HttpOnly`; the verification matrix's Session cookies row and
  the Routes test bullet assert that cookie's attributes; and the CSRF test
  bullet adds the mismatched-value case and names the prefix assertion as what
  closes the sibling-host forgery, because a stateless double-submit gives the
  server nothing to compare against.
- **An expired session cookie cannot return `401` on a layer write.** The layer
  endpoints are mounted ahead of the catch-all the meta-tool identity middleware
  wraps (`internal/serverboot/serverboot.go:1220-1221`, `:1239`) and resolve the
  caller through `layerIdentityResolver`, which discards the verification error
  (`internal/serverboot/identity_verify.go:55-63`). "The browser session", the
  CSRF position bullet, and the verification matrix's Session row now scope the
  `401` to the wrapped routes and state that a layer write with an expired or
  untrusted cookie resolves anonymous and is refused `403` `auth.forbidden`. The
  resolver is left as it is, because its discard governs every credential the
  layer endpoint accepts and re-coding it would change what a gateway-forwarded
  caller receives. The "one edit reaches every consumer" and "owns the whole
  credential contract" sentences are corrected, G1 gains the
  `web/DESIGN.md:163-164` session-expiry correction, Testing gains an
  expired-session integration case, and the Summary records the split.
- **No test pinned the new refusal on `reingest` against an admin-defined
  layer.** That handler runs no authorization today
  (`pkg/registry/server/layers.go:946-991`), and against an admin-defined layer
  the §7 rule collapses to admin-only
  (`spec/07-external-integration.md:65`), so C1 turns a `200` for every caller
  into a `403`. "What lands" states that the gate runs after the layer is loaded
  and before `runIngestAndRespond`, and the C1 test bullet gains the
  admin-defined case for an authenticated non-admin, for a caller resolving no
  subject, for an admin, and with a break-glass body.
- **G1 left the anonymous state asserting visibility filtering.**
  `web/DESIGN.md:156-158` says the anonymous catalog is "filtered to public
  artifacts", which is false on a registry that configures no identity provider
  and in public mode, where the evaluator short-circuits
  (`pkg/layer/composer.go:53`, `:65`, `spec/04-artifact-model.md:615`,
  `spec/13-deployment.md:33`). G1 gains that sentence, scoped to a registry that
  enforces visibility.
- **The transaction TTL was pinned by no test.** The Routes test bullet asserts
  that the pre-authorization cookie's `Max-Age` equals the configured TTL, at the
  default and at an override, the Route mount predicate end-to-end bullet adds a
  binary started with `PODIUM_WEB_UI_AUTH_TRANSACTION_TTL` set, and the
  verification matrix's Session cookies row carries the same obligation.
- **Correction to this pass.** Adding the TTL binary to the Route mount predicate
  bullet left its closing sentence reading "There is no third case" directly after
  three started binaries. The sentence now reads "No further mount-predicate case
  is needed", which states the count over the mount predicate's configuration
  space rather than over the binaries the bullet starts.

## Relationship to proposal 0012

0012 corrected §13's account of what the registry accepts and does. Its decision
3 verified that the shipped SPA attaches no credential, narrowed the web-UI
paragraph to state that plainly, and routed in-browser authentication here. The
sentence S1 amends is the one 0012 wrote, and the page
`docs/deployment/gateway-delegated-identity.md` names as this proposal's
obligation is the one 0012 recorded.
