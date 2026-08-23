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
- The §6.10 catalog gains two codes. `auth.csrf_invalid` (`403`) covers a
  state-changing session-authenticated request that carries no valid proof of
  same-origin intent, and a callback whose pre-authorization cookie does not
  validate. `auth.exchange_failed` (`502`) covers a callback whose code exchange
  the IdP answered and refused, which is permanent for that request and therefore
  outside the retryable `registry.unavailable`.
  The callback route is outside the same-origin gate, because it is a cross-site
  top-level navigation that may already carry a session cookie; gating it would
  refuse every re-sign-in. `auth.forbidden` is broadened by S6.
  `auth.token_expired` and `auth.untrusted_token` keep their scopes. A session
  cookie carries a token the registry itself obtained rather than one a gateway
  forwarded, so the shipped text that describes either code as reporting a
  forwarded token is restated. The credential-location table under "The browser
  session" is the single inventory of that text and names every site.

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
      guard, per "The edit sites". The §13.10 authentication paragraph is the row
      of the credential-location table under "The browser session" whose Staged by
      column names S1.
      Levels: —. Depends on: —
- [ ] **S2 · spec** — SPEC-2. A new §6.3.4 stating the browser acquisition flow,
      with its pointer from the §6.3 introduction.
      Levels: —. Depends on: S1
- [ ] **S3 · spec** — SPEC-3. §6.3.3's second accepted location for the
      `oidc-jwt` credential, its header-wins precedence rule, the narrowed
      no-token-is-anonymous sentence at `spec/06-mcp-server.md:96`, and the
      restated opening clause at `spec/06-mcp-server.md:92`, which is the row of
      the credential-location table under "The browser session" whose Staged by
      column names S3 alongside `:96`.
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
- [ ] **S7 · spec** — SPEC-7. The new `auth.csrf_invalid` and
      `auth.exchange_failed` §6.10 and §6.9
      entries, their `tools/matrix/matrices.go` axis entries, and every row of
      the credential-location table under "The browser session" whose Staged by
      column names S7, which covers the `auth.token_expired` and
      `auth.untrusted_token` restatements in their §6.10 entries and their §6.9
      rows. The table's other `spec/` rows belong to S1 and S3, which own them.
      The `auth.forbidden` broadening is S6's.
      Levels: —. Depends on: S3, S4, S6
- [ ] **G1 · docs** — DESIGN-1. The `web/DESIGN.md` corrections in "The design
      handout".
      Levels: —. Depends on: —
- [ ] **C1 · code** — CODE-1. Owner authorization on the layer write handlers,
      with its `403` tests and its no-identity-provider case.
      Levels: unit, integration, e2e. Depends on: S6, S7
- [ ] **C2 · code** — CODE-2. The browser-flow configuration surface, meaning the
      `Config` and `StartupConfig` fields for the enablement boolean, the
      transaction TTL, and the acquisition values, the `--web-ui-auth` and
      `--web-ui-auth-transaction-ttl` flags on `podium serve`,
      and the `PODIUM_*` reads beside
      `internal/serverboot/serverboot.go:1826-1827`; the sign-in, callback, and
      sign-out routes and
      their two cookies, the `oidcJWTVerifier` cookie branch
      (`internal/serverboot/identity_verify.go:201`) together with the twelve
      `internal/serverboot` test call sites its new parameter moves
      (`identity_gateway_integration_test.go`, `identity_gateway_test.go`, and
      `multitenant_integration_test.go`), the CSRF position below, the
      `auth.csrf_invalid` and `auth.exchange_failed` entries in
      `errorCodeRegistry`, and every row of the credential-location table
      under "The browser session" whose Staged by
      column names C2. That set spans `pkg/registry/server`,
      `internal/serverboot`, and `pkg/identity`, and the table is its complete
      enumeration; none of the `pkg/identity` edits changes behavior or a
      signature, because the cookie branch lives in `serverboot` and
      `OIDCVerifier.Verify` receives a raw token with no knowledge of its
      origin.
      Levels: unit, integration, e2e. Depends on: S1, S2, S3, S4, S7
- [ ] **C3 · code** — CODE-3. The web-UI authentication configuration guard in
      `StartupConfig.Validate`, including its web-UI, `oidc-jwt`, public-mode,
      acquisition-value, and redirect-URI conjuncts, over the fields C2 adds,
      and the nested route mount at
      `internal/serverboot/serverboot.go:1229`.
      Levels: unit, e2e. Depends on: S1, C2
- [ ] **B1 · code** — BUILD-1. The React toolchain, the committed bundle, the
      `go:embed` change, `web/web_test.go`, the served-bundle end-to-end
      assertion, the rebuild-is-clean CI check, and the mechanical
      `dangerouslySetInnerHTML` check over the web UI's source tree in the same
      CI job.
      Levels: unit, e2e. Depends on: —
- [ ] **U1 · code** — UI-1. The UI surfaces built against `web/DESIGN.md`,
      including the sanitized markdown rendering path and its sanitizer cases.
      Levels: unit, e2e. Depends on: B1, C1, C2, G1
- [ ] **D1 · docs** — DOC-1. Every shipped mirror named in "The edit sites", and
      every row of the credential-location table whose Staged by column names D1.
      Levels: —. Depends on: S1, S2, S3, S4, S6, S7
- [ ] **T1 · test** — TEST-1. The manual scenarios, including the S44 rewrite,
      the S44 stack restaging (its Keycloak client registration, its
      password-grant token mint, and its serve invocation), the S45 step-2
      and step-4 rewrites, and every row of the credential-location table under
      "The browser session" whose Staged by column names T1, which is the
      startup-log text S36 and S44 quote verbatim.
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
rule collapses to admin-only, which is what §7 already states
(`spec/07-external-integration.md:65`), and that qualifier carries weight rather
than restating the obvious: an admin-defined layer carries a stored `Owner` as
well, assigned from the request body on the admin-defined branch of `register`
(`pkg/registry/server/layers.go:659`) and patchable on the same branch of
`update` (`:547-549`, `docs/reference/http-api.md:329`), so an owner arm written
without the qualifier would admit whichever non-admin subject that field names.
On `register` the gate is conditional on the request
naming an existing layer in the tenant: a registration whose ID names no stored
layer creates one as it does today, a registration whose ID names a stored
user-defined layer is authorized to that layer's owner or to a tenant admin, a
registration whose ID names a stored admin-defined layer is authorized to a
tenant admin alone whatever that layer's `Owner` field names, and every other
caller is refused with `403` `auth.forbidden` rather than upserting. The existence lookup
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

**The cookie table.** The browser flow sets the cookies below and no others. This
table is the single statement of the cookie contract. Every other site in this
proposal cites it by name and states only what is local to that site.

| Cookie | Prefix | `HttpOnly` | `Secure` | `Path` | `SameSite` | `Max-Age` | Set by | Cleared by |
|:--|:--|:--|:--|:--|:--|:--|:--|:--|
| `__Host-podium_session` | `__Host-` | yes | yes | `/` | `Lax` | absent | the callback | sign-out |
| `__Host-podium_auth` | `__Host-` | yes | yes | `/` | `Lax` | the configured transaction TTL | sign-in | the callback, on every outcome; sign-out |
| the CSRF cookie, present only where the chosen mechanism carries one | `__Host-` | no | yes | `/` | `Lax` | unconstrained | the CSRF mechanism | the CSRF mechanism |

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
  no second lifetime and the row carries no `Max-Age`.
- The two `__Host-podium_*` cookies are the session mechanism and carry no CSRF
  role.

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
locations, or names a remediation a browser cannot perform. The table below is
the single inventory of that text.

**The credential-location table.** Every other site in this proposal cites this
table by name and states only what is local to it. A site is restated when a
request whose token arrives in `__Host-podium_session` reaches it and the text is
then false or names an action a browser cannot take. A site scoped to the header
location or to the gateway deployment stands as written, because the browser flow
removes neither and the header-wins precedence rule keeps the gateway account
true where it is labelled as such.

| Site | What it says today | Staged by |
|:--|:--|:--|
| `spec/06-mcp-server.md:92` (first clause) | both providers are "registry-process identity providers for a deployment that runs the registry behind a gateway that has already authenticated the caller", which is false for a directly reachable `oidc-jwt` registry running the browser flow. It is the authoring source of the `pkg/identity/registry.go:69-70` comment below, so the two move together. Restated so `oidc-jwt` is described as a registry-process provider for a deployment where the registry verifies the caller's token itself, whether a gateway forwarded it or the registry obtained it through the §6.3.4 exchange, with `trusted-headers` alone keeping the fronting-gateway requirement. `:94`, on tenant derivation, stands | S3 |
| `spec/06-mcp-server.md:96` (closing sentence) | a request whose header carries no bearer credential is anonymous | S3, narrowed as the §6.3.3 edit site states |
| `spec/13-deployment.md:170` | the §13.10 account: "unless a gateway forwards the token" and "resolves the caller's identity from the forwarded token" | S1 |
| `spec/06-mcp-server.md:327` | the §6.9 row: "for `oidc-jwt` the gateway forwards a new token" | S7 |
| `spec/06-mcp-server.md:329` | the §6.9 row label "Untrusted forwarded token" | S7 |
| `spec/06-mcp-server.md:355` | "The gateway-delegated providers (§6.3.3) add three `auth.*` codes", which after the amendment also describes a registry with no gateway | S7 |
| `spec/06-mcp-server.md:362` | `auth.token_expired`'s `suggested_action`, carrying the same clause as `:327` | S7 |
| `spec/06-mcp-server.md:366` | `auth.untrusted_token`'s scope: "a forwarded `oidc-jwt` token" | S7 |
| `spec/06-mcp-server.md:371` | the canonical `message`: "Forwarded token from issuer '…' failed verification." | S7 |
| `spec/06-mcp-server.md:374` | `auth.untrusted_token`'s `suggested_action`: "Verify the gateway forwards a token…" | S7 |
| `pkg/registry/server/error_envelope.go:66` | the comment introducing the block C2 edits: "the gateway-delegated oidc-jwt provider's codes" | C2 |
| `pkg/registry/server/error_envelope.go:68` | `auth.token_expired`'s `suggestedAction`, the mirror of `:362` | C2 |
| `pkg/registry/server/error_envelope.go:71` | `auth.untrusted_token`'s `suggestedAction`, the mirror of `:374` | C2 |
| `pkg/registry/server/identity_verify.go:89-91` | the expiry comment: "an expired oidc-jwt token is refreshed by the gateway, not the runtime" | C2 |
| `pkg/registry/server/identity_verify.go:96-98` | the comment on the `errors.As(err, &ult)` arm, defining `auth.untrusted_token` as "a gateway-forwarded oidc-jwt token that fails signature, iss, or aud" | C2 |
| `pkg/registry/server/identity_verify.go:103` | the emitted message, the mirror of `spec/06-mcp-server.md:371` | C2 |
| `pkg/registry/server/identity_verify.go:107` | the issuerless fallback: "The forwarded token could not be verified." | C2 |
| `internal/serverboot/identity_verify.go:193-197` | `oidcJWTVerifier`'s doc comment, on the function that gains the cookie branch: "extracts the forwarded JWT from the configured token header" at `:194`, and "A request carrying no token is anonymous" at `:196-197`, which is the code mirror of the `spec/06-mcp-server.md:96` narrowing S3 stages | C2 |
| `internal/serverboot/identity_verify.go:261` | `oidcJWTConfigGuard`'s doc comment: "oidc-jwt verifies the forwarded token's aud" | C2 |
| `internal/serverboot/identity_verify.go:277` | the `config.oidc_jwt_audience_unset` message: "verified on every forwarded token". The `config.oidc_jwt_audience_unset` prefix every assertion matches (`test/e2e/auth_gateway_test.go:183`, `internal/serverboot/identity_gateway_test.go:51-52`) is unchanged | C2 |
| `internal/serverboot/serverboot.go:1115` | the wiring comment: "verify the gateway-forwarded token against the issuer JWKS" | C2 |
| `internal/serverboot/serverboot.go:1142` | the boot log line "verifying forwarded tokens against accepted issuers", which an operator reads to confirm the provider came up. The restatement keeps the substring "accepted issuers <issuers>", which `test/e2e/auth_oidc_jwt_test.go:202` and `:277` match | C2 |
| `test/manual-validation.md:2642-2643` | S36's expectation, quoting that boot log line verbatim | T1 |
| `test/manual-validation.md:4085-4086` | S44 step 3's **Expect**, quoting the same line verbatim | T1 |
| `pkg/identity/identity.go:22` | `ErrUntrustedToken`'s doc: "A gateway-forwarded oidc-jwt token failed…" | C2 |
| `pkg/identity/oidc_jwt.go:24` | `OIDCJWT`'s doc: "verifies a gateway-forwarded IdP-signed JWT" | C2 |
| `pkg/identity/oidc_jwt.go:44` | `UntrustedTokenError`'s doc: "reports that a gateway-forwarded oidc-jwt token failed verification" | C2 |
| `pkg/identity/oidc_jwt.go:65` | `ErrKeySetUnavailable`'s doc: "so a forwarded token cannot be verified" | C2 |
| `pkg/identity/oidc_jwt.go:86` | `OIDCVerifier`'s doc: "verifies a gateway-forwarded OIDC JWT" | C2 |
| `pkg/identity/registry.go:69-70` | the registration comment: "a gateway fronts the registry and the registry verifies the forwarded token", which asserts a fronting gateway rather than describing one | C2 |
| `docs/reference/error-codes.md:57` | the `auth.untrusted_token` row, opening "A gateway-forwarded `oidc-jwt` token failed…" | D1 |
| `docs/reference/error-codes.md:59` | the `auth.token_expired` remediation clause, "the gateway forwards a new token on `oidc-jwt`"; the row's scope sentence stands | D1 |
| `docs/reference/http-api.md:24` | "`oidc-jwt`: a gateway forwards the caller's token", inside the `:21-27` account the mirror table already stages | D1 |
| `docs/getting-started/how-it-works.md:413-416` | the shipped restatement of `spec/06-mcp-server.md:92`: both providers are "registry-process providers for a deployment that runs the registry behind a gateway that already authenticated the caller", and "`oidc-jwt` verifies the forwarded IdP-signed token on every request". Restated the same way S3 restates its authoring source: `oidc-jwt` is a registry-process provider for a deployment where the registry verifies the caller's token itself, whether a gateway forwarded it in the configured header or the registry obtained it through the §6.3.4 exchange and it arrives in `__Host-podium_session`, with `trusted-headers` alone keeping the fronting-gateway requirement. The `trusted-headers` sentence and the page link stand | D1 |
| `docs/deployment/integrations.md:86` | the `oidc-jwt` row of the registry provider table: "The registry verifies a gateway-forwarded IdP-signed JWT against the issuer's JWKS on every request." Restated so the token's location is either the configured header or `__Host-podium_session`; the row's `[Gateway-delegated identity]` link and the neighbouring `trusted-headers` row stand | D1 |
| `docs/deployment/gateway-delegated-identity.md:58` | the no-token-is-anonymous rule, as the mirror table states | D1 |
| `docs/deployment/gateway-delegated-identity.md:105-107` | the web-UI account, as the mirror table states | D1 |

These sites stand as written, because each is scoped to the header location or to
the gateway deployment: `spec/06-mcp-server.md:108`, `:110`, `:112`, and
`:96`'s opening sentence, which S3 amends by adding the second location beside it
rather than by rewriting it; `spec/13-deployment.md:185`, `:474`, `:475`;
`internal/serverboot/serverboot.go:1505`, `:1509`;
`internal/serverboot/identity_verify.go:175-181`, `bearerTokenFromHeader`, which
still reads headers alone, because the cookie is read by `oidcJWTVerifier`;
`pkg/identity/trusted_headers.go:8-11`, `:42-43`, because `trusted-headers` reads
no cookie and the browser flow cannot be enabled under it;
`docs/deployment/gateway-delegated-identity.md:15`, `:16`, `:18`, `:22`, `:45`,
`:50`; `docs/deployment/oidc/index.md:47`, whose sentence opens "When a gateway
in front of the registry has already authenticated the caller";
`docs/deployment/oidc/auth0.md:96`; and `docs/deployment/clustered.md:171`.

`spec/06-mcp-server.md:386` and its mirror
`pkg/registry/server/error_envelope.go:74` also stand. That
`auth.tenant_unknown` remediation reads "Provision the organization as a tenant,
or forward a token whose org_id claim names an existing tenant." Unlike
`auth.token_expired`'s remediation it does not enumerate a path per provider, and
its first clause names the action every deployment takes, so a browser session is
not left without one. The entry's scope and its `details.token_org_id` field stay
accurate for a session-authenticated request, so §6.10's `auth.tenant_unknown`
and this string are both untouched.

The moved set is reproduced rather than re-derived. It is the hits of

    grep -rniE "forward|gateway" spec/06-mcp-server.md spec/13-deployment.md \
      pkg/identity pkg/registry/server internal/serverboot docs/reference \
      docs/deployment docs/getting-started/how-it-works.md \
      test/manual-validation.md

filtered by the rule above, discarding the hits that use the word for something
else: forward compatibility, forward slashes, a schema migrated forward in place,
the audit `X-Forwarded-User` attribute, and the MCP bridge forwarding meta-tool
calls (`spec/06-mcp-server.md:5`). The sweep returns far more hits than the table
has rows, so the rule is what disposes of the remainder; the not-moved lists above
record only the sites earlier rounds re-found. A reviewer who suspects a missing
site runs that command and applies the rule rather than reading the prose again.

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
  predicate below: it states that a state-changing request authenticated by a
  browser session, other than the callback route, carries proof of same-origin
  intent that the registry verifies
  before the handler runs, and that a request without one is refused with `403`
  `auth.csrf_invalid`. It states the callback's exclusion and its reason in the
  same place: the callback is a top-level cross-site navigation that carries no
  same-origin proof and may carry a session cookie from an earlier sign-in, and
  it is bound instead by the single-use `state` and `nonce` in its
  pre-authorization cookie. Without a spec home the requirement would live only
  in this proposal, and the test that pins it would have no section to cite.
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
  reachable `oidc-jwt` registry running the browser flow, so S3 restates it per
  its row in the credential-location table under "The browser session";
  the `pkg/identity/registry.go:69-70` comment C2 moves is that clause's code
  mirror, and the two land together. `:94` stands as written, because the
  amendment adds no credential and changes no tenant derivation: the cookie
  carries the same IdP-signed token and the registry still reads the
  organization from that token's verified `org_id` claim. `:92` and `:96` are
  the only sentences in `:92-112` that this amendment touches, and the separate
  `trusted-headers` anonymity rule at `:108` stands unchanged for the reason the
  next sentence gives. A
  `trusted-headers` or `injected-session-token` registry reads no cookie, because
  the browser flow cannot be enabled under either and the cookie branch is gated
  on the same enablement field. "The browser session" above states the mechanism.
- **§7** — the sign-in, callback, and sign-out routes, alongside the
  operator-level endpoints §7.3.3 enumerates
  (`spec/07-external-integration.md:152`). Sign-in mints the state, nonce, and
  PKCE verifier, returns them in the `__Host-podium_auth` cookie, and redirects
  to the IdP; the callback reads that cookie, validates the returned `state`
  against it, exchanges the code server-side, validates the `nonce` of the ID
  token that exchange returns against that same cookie, clears the
  pre-authorization cookie, and returns the access token in the
  `__Host-podium_session` cookie; sign-out clears both cookies. The section also
  states the disposition of a callback whose `state` validates but whose query
  carries the IdP's `error` parameter rather than a `code`, which is the redirect
  the authorization endpoint sends when the user declines the consent prompt: the
  callback runs no exchange, clears the pre-authorization cookie, sets no session
  cookie, leaves any `__Host-podium_session` cookie the browser already holds
  intact, and returns the browser to the web UI root at `/ui/` without
  establishing or replacing a session, because re-running sign-in is the
  recovery. That outcome carries no
  error code, and in particular not `auth.exchange_failed`, which is reserved for
  an exchange the IdP answered and refused. The `nonce`
  check follows the exchange because the ID token does not exist until the token
  endpoint answers, which is the order "The browser session" and the Routes test
  state. The cookie
  attributes and lifetimes are the ones the cookie table under "The browser
  session" gives each row. None of the
  three reads or writes registry state, so §13.2.1 classifies all three outside
  the write set and a read-only registry serves them unchanged. The section
  states that the callback is outside the §6.3.4 same-origin gate, for the reason
  §6.3.4 gives, and that a callback presenting a session cookie from an earlier
  sign-in completes and replaces that cookie rather than being refused. It states
  that sign-out clears the cookies on every request that carries one and
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
  names a user-defined layer that already exists in the tenant, are authorized to
  that layer's owner or to a tenant admin. The same operations against an
  admin-defined layer, including a `register` whose ID names one, are authorized
  to a tenant admin alone, whatever that layer's stored `owner` field names,
  because that field is set from the request body on the admin-defined branch of
  `register` and patched on the admin-defined branch of `update`
  (`pkg/registry/server/layers.go:659`, `:547-549`) and therefore names no
  authorized subject. A caller authorized by neither arm is refused with
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
- **§6.10 and §6.9** — two new codes. `auth.csrf_invalid` covers a state-changing
  session-authenticated request that carries no valid proof of same-origin
  intent and a callback whose pre-authorization cookie does not validate,
  refused with `403`. No existing code covers it: `auth.forbidden` reports an
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
  registry has no gateway at all. S7 stages every row of the credential-location
  table under "The browser session" whose Staged by column names S7;
  `auth.token_expired`'s canonical `message` (`spec/06-mcp-server.md:360`) is
  provider-neutral and is not among them. That table also records why
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
| `docs/reference/error-codes.md:57` | `auth.untrusted_token`, restated per its row in the credential-location table so the row and its remediation match the amended `spec/06-mcp-server.md:366` and `:374` |
| `docs/reference/error-codes.md:59` | `auth.token_expired`, whose scope sentence stands and whose remediation clause is restated per its row in the credential-location table |
| `docs/reference/error-codes.md:60` | `auth.forbidden`, scoped to an admin-only operation attempted by a non-admin; the `auth.*` table also gains the `auth.csrf_invalid` and `auth.exchange_failed` rows |
| `docs/reference/error-codes.md:69` | the bind guard's `config.web_ui_public_bind_refused`, which the amended §13.10 bind-guard sentence restates; the `config.*` table also gains a `config.web_ui_auth_unconfigured` row stating the browser-flow guard's predicate |
| `docs/reference/http-api.md:13-27` | the Authentication section: the header table, and the account of the accepted registry-process credentials at `:21-27`, which gains the browser session under `oidc-jwt` and the CSRF requirement a state-changing session-authenticated request carries, other than the callback route. It states the callback's exclusion for the reason §6.3.4 gives: the callback is a cross-site top-level navigation bound instead by the single-use `state` and `nonce`. It is also the new home of the authentication route paths; there is no route list there today |
| `docs/reference/cli.md:131-138` | the `podium serve` synopsis, a closed usage line carrying `--web-ui` and `--web-ui-allow-public-bind`, which gains a token for `--web-ui-auth` and one for `--web-ui-auth-transaction-ttl` and none for the environment-only acquisition values, per the key-placement rule |
| `docs/reference/cli.md:142-155` | the `podium serve` flag table, which gains a row for each of those two flags naming its `PODIUM_*` override, and whose `--web-ui-allow-public-bind` row (`:155`) is restated from the amended §13.10 bind-guard sentence |
| `docs/reference/cli.md:747` | the environment-variable table row that pairs `PODIUM_OAUTH_AUDIENCE` with `PODIUM_OAUTH_AUTHORIZATION_ENDPOINT` as "OAuth provider config", which gains the environment-only browser-flow acquisition keys and states that the device-code endpoint is not one of them |
| `docs/reference/http-api.md:265-346` | the Layer management section, whose entries state the pre-S6 authorization rule: `:329` says a user-defined-layer update "still answers `200 OK`", `:320` gives the reorder rule as admin-only on an admin-defined layer, `:286` documents register's `201 Created` with no refusal, and unregister, restore, and reingest document no authorization at all, while every other gated route in the reference does (`:538`). The section gains one statement at its head, matching the amended §7.3.1: against a user-defined layer, `register` under that layer's ID, `unregister`, `update`, `restore`, `reorder`, and `reingest` are authorized to the layer's owner or to a tenant admin; against an admin-defined layer the same operations are authorized to a tenant admin alone, whatever the `owner` field on that layer names; every other caller receives `403` `auth.forbidden`; and the rule is live only where an identity provider is configured and public mode is off. `:329`'s `200 OK` clause is scoped to the owner, and `:320` is restated so the admin-defined sentence no longer reads as the whole rule |
| `docs/reference/cli.md:440` | the `podium layer reorder` entry, which states "Reordering a user-defined layer requires no admin role" as the complete rule; it is restated as owner-or-admin with the same deployment carve-out |
| `docs/reference/http-api.md:290` | the register-response example, which prints snake_case keys for a response emitting Go field names |

## The CSRF position

A session cookie authenticates automatically on any request the browser can be
induced to make, so every layer write this proposal exposes becomes forgeable
across origins. A Bearer token was not forgeable that way, so the risk arrives
with the route rather than with the panel.

The position is specified here rather than left to the implementor, because the
prior review treated it as acknowledged prose for eight rounds and never
produced a finding on it.

- Every state-changing request authenticated by a session cookie, other than the
  callback route, carries proof of same-origin intent that the server verifies,
  and a request without valid proof is refused before the handler runs with `403`
  `auth.csrf_invalid`.
- The callback is outside that gate, and the exclusion is stated rather than
  implied. The callback arrives as a top-level cross-site navigation from the
  IdP, it carries no request-side value a page could have set, and a browser that
  already holds `__Host-podium_session` from an earlier sign-in sends that cookie
  on it, so under an unqualified predicate every re-sign-in would be refused with
  `auth.csrf_invalid` and no session would ever be established for that browser.
  What protects the callback is the single-use `state` and `nonce` binding the
  pre-authorization cookie carries, which the bullet below states, and which
  refuses exactly the forged and replayed callbacks a same-origin check would.
  A callback that presents a valid session cookie is treated as a re-sign-in: it
  runs the same validation and replaces the session cookie on success.
- The cookies are the ones the cookie table under "The browser session" lists,
  and a CSRF mechanism that carries its own cookie takes that table's CSRF row.
  `SameSite` is a defense in depth here rather than the control, which is why the
  same-origin proof required above does not rest on it. Dropping the `__Host-`
  prefix from the CSRF row would let any host under the registry's registrable
  domain plant that cookie with a `Domain` attribute and then forge a
  state-changing request that echoes the planted value, which the session cookie
  would authenticate.
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
  re-scoped and no code is added. Their gateway-assuming text is restated per the
  credential-location table under "The browser session", which names every site
  and its owner.
- Sign-out is itself state-changing and carries the same protection, because a
  forged sign-out is a denial of service against a signed-in operator. A sign-out
  refused for CSRF returns `403` `auth.csrf_invalid` and clears no cookie.
  Read-only mode does not enter into it: no authentication route is in the
  §13.2.1 write set, so a read-only registry serves sign-out exactly as it serves
  it otherwise.

**IMPLEMENTOR'S CHOICE:** the CSRF mechanism on the gated endpoints, whether an
origin or `Sec-Fetch-Site` check, a double-submit cookie, or both. Any answer
refuses a state-changing non-callback request that carries a session cookie and
no valid proof of same-origin intent, refuses it before the handler runs rather
than inside it, returns `403` `auth.csrf_invalid` rather than a
mechanism-specific status or code, and is asserted by a test that forges the
request. Any cookie the answer introduces takes the CSRF row of the cookie
table. A synchronizer token is not available, because it is a per-session
credential the registry would have to mint and store, and "The browser session"
states that the registry mints no credential and keeps no session record.

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
  group-scoped. The same sentence's field inventory is respelled as the layer
  responses emit it. `GET /v1/layers` marshals `[]store.LayerConfig` directly
  (`pkg/registry/server/layers.go:769`, `:777`) and the register response
  embeds the same struct (`:329`), and that struct carries JSON tags only on
  `force_push_policy`, `last_ingested_at`, and `webhook_secret` (`json:"-"`)
  (`pkg/store/store.go:258-307`), so the wire keys are the Go field names `ID`,
  `SourceType`, `Repo`, `Ref`, `Root`, `LocalPath`, `Order`, `UserDefined`,
  `Owner`, and `LastIngestedRef`, with `last_ingested_at` and
  `force_push_policy` the tagged exceptions. This is the same fact the mirror
  table records when it stages `docs/reference/http-api.md:290`. The brief's
  request-body descriptions stay snake_case, because `LayerRegisterRequest` is
  tagged (`pkg/registry/server/layers.go:290-312`). The brief's catalog sections
  need no such correction, because those responses are tagged snake_case; the
  layer list is the one place the brief diverges from the wire, and the brief has
  no compiler or test behind it.
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
| Artifact viewer | authenticated sees what anonymous does not | — | not-found for invisible artifact | sanitized markdown, property table, related links; a hostile body and a markup-carrying frontmatter value both render inert |
| Layer panel | list is unfiltered by the server | owner gate refuses `403` | `registry.read_only` across the panel | one-time secret, destructive confirmation |
| Session | a cookie-carried token resolves the same identity the header does, and the session cookie a successful callback returns resolves the IdP-issued subject rather than anonymous | sign-in and the callback set cookies and write no registry state; sign-out clears them | on a meta-tool route a cookie past the token's `exp` returns `auth.token_expired`; on a layer write the same cookie resolves anonymous and the owner gate returns `auth.forbidden`; a callback whose exchange the IdP answers and refuses returns `auth.exchange_failed`, an unreachable token endpoint returns `registry.unavailable`, and a callback carrying the IdP's `error` parameter rather than a `code` runs no exchange, sets no session cookie, leaves an existing one intact, and returns the browser to `/ui/` | — |
| Session CSRF | — | forged state-changing request refused with `403` `auth.csrf_invalid`, including a request whose request value does not match its CSRF cookie; a callback carrying a session cookie from an earlier sign-in completes and replaces it, because the callback is outside the same-origin gate | replayed or misdelivered callback refused with the same code | — |
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
  a non-admin who is the recorded `Owner` of an existing admin-defined layer and
  re-registers that ID with `{"user_defined": true}` receives `403`
  `auth.forbidden`, with the stored layer still carrying `UserDefined: false` and
  its visibility unchanged, which is the case that discriminates the qualified
  owner arm from an unqualified one, because an admin-defined layer's `Owner` is
  set from the request body (`pkg/registry/server/layers.go:659`) and patchable
  (`:547-549`) and so names no authorized subject;
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
  endpoint, one case per discriminating condition.
  - A callback carrying no pre-authorization cookie is refused with `403`
    `auth.csrf_invalid` and sets no session cookie.
  - A callback whose returned `state` does not match the pre-authorization
    cookie is refused with `403` `auth.csrf_invalid` and sets no session cookie.
  - A callback whose `state` matches but whose stub-issued ID token carries a
    different `nonce` is refused with `403` `auth.csrf_invalid` and sets no
    session cookie. This is the check that makes the unsigned pre-authorization
    cookie sound, and it is independent of the `state` comparison.
  - A callback carrying a valid pre-authorization cookie and
    `?error=access_denied&state=<the cookie's state>`, which is the redirect the
    IdP sends when the user declines sign-in, leaves the stub token endpoint
    with no request recorded, sets no session cookie, emits the clearing
    `Set-Cookie` for `__Host-podium_auth`, and returns the browser to `/ui/`.
    The case runs twice, once with no session cookie present and once with a
    valid `__Host-podium_session` cookie present, and the second run asserts
    that the response emits no `Set-Cookie` for that cookie, so the cancelled
    re-sign-in leaves the earlier session in place.
    An implementation that falls through to the exchange fails here,
    because it answers `502` `auth.exchange_failed` on the outcome an operator
    hits every time a user cancels.
  - The stub token endpoint asserts that the exchange presents the
    `code_verifier` matching the PKCE challenge the sign-in redirect carried, and
    fails the exchange otherwise, so an implementation that stores a verifier and
    never sends it fails here.
  - A callback whose pre-authorization cookie validates but whose exchange the
    stub cannot answer, because it is unreachable or returns `5xx`, is refused
    with `registry.unavailable` and sets no session cookie.
  - A callback whose exchange the stub answers with an OAuth error such as
    `invalid_grant` is refused with `502` `auth.exchange_failed` and sets no
    session cookie, and the response envelope carries `retryable: false` and a
    non-empty `suggested_action`. This is the case that discriminates the
    permanent refusal from the transient one above, which a single
    `registry.unavailable` arm would collapse, and the envelope assertion is what
    discriminates the staged `errorCodeRegistry` entry from its absence, because
    `enrichEnvelope` returns immediately for an unregistered code
    (`pkg/registry/server/error_envelope.go:88-92`) and leaves exactly the body a
    code-only assertion would accept. It carries the
    `// Matrix: §6.10 (auth.exchange_failed)` annotation.
  - The callback emits the clearing `Set-Cookie` for `__Host-podium_auth` on
    every refusal above and on the success as well, which is the every-outcome
    clearing the cookie table states.
  - The `__Host-podium_session` cookie a successful callback returns, replayed on
    a subsequent request through the installed `oidcJWTVerifier`, resolves the
    subject the stub IdP issued rather than anonymous. The stub issues an ID
    token whose `aud` is the OAuth client identifier and an access token whose
    `aud` is `PODIUM_OAUTH_AUDIENCE`, so an implementation that puts the ID token
    in the session cookie, or one that exchanges for an access token minted for
    another audience, fails here instead of failing silently in every browser.
  - A valid callback delivered while a valid `__Host-podium_session` cookie is
    already present, which is the re-sign-in case, completes the exchange and
    returns a `Set-Cookie` replacing that session cookie rather than a `403`
    `auth.csrf_invalid`. That case fails against an implementation that applies
    the same-origin gate to the callback, and it is the one an operator hits
    first, because the second sign-in of any browser is that request.
  - Sign-out clears both `__Host-podium_*` cookies.
  - Every `Set-Cookie` the three routes emit carries the attributes its row in
    the cookie table gives it, asserted against that table rather than restated
    here. The `__Host-podium_auth` `Max-Age` is asserted once at the default
    transaction TTL and once with the endpoint constructed with a different TTL,
    so the default and the override are distinguished.

  **IMPLEMENTOR'S CHOICE:** where the cases this Testing section describes live
  and what each is named. Any answer places a case in the package that owns the
  function under test, and asserts the cookie contract as the cookie table states
  it rather than restating an attribute in the test's own prose.
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
  `// Spec: §6.10`. This is the only new test the credential-location table
  needs. Two of its other rows are emitted strings that existing checks already
  observe: the `config.oidc_jwt_audience_unset` startup error, matched by its
  code prefix alone (`test/e2e/auth_gateway_test.go:183`,
  `internal/serverboot/identity_gateway_test.go:51-52`), and the boot log line,
  matched by the substring "accepted issuers" (`test/e2e/auth_oidc_jwt_test.go:202`,
  `:277`) and quoted verbatim by the S36 and S44 expectations that T1 restages.
  The rest of the rows are comments and prose that emit nothing, so no test can
  pin them, and they are held by review against the table, by the §6.10 mirror
  obligation, and by the sweep the table states.
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
  plants the CSRF cookie and echoes the planted value, is closed by that cookie's
  `__Host-` prefix rather than by a server-side comparison, because a stateless
  double-submit carries nothing the server can distinguish from a value it
  issued; this test asserts the CSRF cookie's `Set-Cookie` against its row in the
  cookie table, and that assertion is what pins the control. A forged sign-out is refused the same way, carries no
  clearing `Set-Cookie`, and leaves the session still authenticating the browser
  on a subsequent request, which pins the sign-out half of the §7 and CSRF
  predicate. It carries the `// Matrix: §6.10 (auth.csrf_invalid)`
  annotation. This test is the one that would fail against the pre-fix design, so
  it is required rather than optional.
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
  rather than authenticating. A binary started with the browser flow enabled and
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
  source passes every other case here and fails this one. These are the deny
  paths of a fail-closed control this change introduces, so they are required
  rather than covered by the Render column's well-formed case.
- **No unsanitized markup (B1).** The mechanical check over the web UI's own
  source tree reports no `dangerouslySetInnerHTML` outside the single sanitized
  rendering path, and it runs in the CI job that also runs the rebuild-is-clean
  check. A tree that adds a second occurrence fails that job.
- **Surfaces (U1).** Per the matrix above, driven through the UI's own API calls
  rather than through the CLI.

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
browser flow, so the registry registers none of those routes. It keeps what
stays true: the clause struck by proposal 0012 named a write endpoint the
registry does not serve.

**Two expectations quote the startup log line.** S36
(`test/manual-validation.md:2642-2643`) and S44 step 3 (`:4085-4086`) quote the
`oidc-jwt` startup log line verbatim, and C2 restates that line per its row in
the credential-location table under "The browser session". Both expectations are
restaged to quote the restated line, and T1 carries them as the table's T1 rows.
The restatement keeps the substring "accepted issuers", so the end-to-end
assertions that match on it (`test/e2e/auth_oidc_jwt_test.go:202`, `:277`) are
unaffected.

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
- Changing the endpoints the UI calls, or giving it privileged access.
- Server-side filtering of `GET /v1/layers`. The panel's role split is
  presentation over an unfiltered list; the gate this proposal adds is on writes.
- A server-side session record, a session table, or a session store. The session
  is the IdP token in a cookie, and "The browser session" states why.
- Silent token refresh, and revocation before the token's `exp`. An expired
  session re-runs the sign-in redirect.
- The SDK half of the `DeviceCodeRequired` gap, a separate §6.3 client surface.
- Any behavioral change to `oauth-device-code`, `injected-session-token`, or the
  startup identity guard. The guard's predicate, its refusal, and its error code
  are unchanged; C2 restates its doc comment and the tail of its message per
  their rows in the credential-location table under "The browser session",
  because both say the `aud` claim is verified on a forwarded token.

## Resolved in adversarial review

### Passes 1 through 4 and Redesign 1 (2026-08-22, automated)

Redesign 1 rewrote the browser-session mechanism: what the credential is, where
it lives, who reads it, how the routes are enabled and mounted, and what the
§13.2.1 classification is. It was redesigned because the proposal named a session
store in five places and designed it in none, specified the session a facet at a
time across six spec sections, and carried a §13.2.1 split that needed an outage
residual to stay coherent. The store is deleted. The session is the IdP access
token in a cookie, the pre-authorization transaction is a second cookie, the
registry keeps no record, and revocation is the token's `exp`, which is the model
§6.3.3's verification paragraph already carries (`spec/06-mcp-server.md:98`).
"The browser session" is the single home of the mechanism, and every edit site
points at it. Precedence runs header-first, because a gateway that authenticated
the request is the authority in that deployment. All three routes read and write
no registry state, so all three sit outside the §13.2.1 write set under that
section's existing rule. The routes mount on one validated field, because the
§13.10 guard takes `PODIUM_WEB_UI` as a conjunct.

The findings these passes resolved are indexed below. Each is landed in the edit
sites, the implementation checklist, and the Testing section, which carry the
decisions; the paragraph above and the two that follow carry what a later pass
cites.

- P1: the design brief described layer visibility as a single value (G1).
- P1: the design brief said everything displayed is identity-filtered (G1).
- P1: the `podium serve` flag reference was in no edit list.
- P1: `auth.forbidden`'s definitions were admin-only, so S6 was added.
- P1: the owner gate was unspecified where no caller is authenticated.
- P1: the named reingest integration test does not fail, so the claim was struck.
- P1: the CSRF and session refusals named no §6.10 error code.
- P1: the CSRF requirement landed in no spec edit site and no matrix cell.
- P1: precedence between a session cookie and a forwarded credential was unstated.
- P1: `auth.tenant_unknown` was re-scoped only in the docs mirror. Reversed by Redesign 1.
- P1: sign-out was classified as a read-only-rejected write. Reversed by Redesign 1.
- P1: no test pinned the cookie attributes.
- P1: the repository-wide `dangerouslySetInnerHTML` check was false against the shipped tree.
- P1: `web/web_test.go` was in no edit list.
- P1: S45 step 2 was falsified by the write-set mirror edits.
- P1: the `http-api.md` mirror range stopped short of the credential account.
- P1 correction: `auth.csrf_invalid` had no `tools/matrix/matrices.go` axis cell.
- P1 correction: the owner-gate predicate was per-request rather than per-deployment.
- P1 correction: the Summary still credited §7 with the owner authorization.
- P1 correction: S7 claimed an `auth.forbidden` restatement §6.10 has no entry for.
- P2: the design brief hid the layer panel on the deployment §13.10 targets (G1).
- P2: `register` was outside the ownership gate.
- P2: the §13.12 `PODIUM_MULTI_TENANT` row restated the tenant-rejection rule. Reversed by Redesign 1.
- P2: the served bundle's title and asset base path were unconstrained.
- P2: the authentication routes had no mount predicate, so the staged S45 edits were undetermined.
- P2: the callback carried no §13.2.1 classification. Reversed by Redesign 1.
- P2: `docs/deployment/gateway-delegated-identity.md:58` was in no edit list.
- P2: §13.1's docs mirror was in no edit list. Reversed by Redesign 1.
- P2: the route-path constraint named S45 step 2, which carries no route path.
- P2: `auth.token_expired`'s remediation text was left unstaged.
- P3: the §7 sign-out sentence and the CSRF position stated incompatible cookie-clearing predicates.
- P3: the browser flow's enablement was bound to no identity provider. Its citation for the shipped public-mode exclusion is corrected in pass 8 to `pkg/registry/server/config_validate.go:88-91`.
- P3: the mount-predicate test exercised only the configurations a conjunction and a disjunction agree on. Its second half is reversed by Redesign 1.
- P4: the no-identity-provider e2e case used an invocation that creates no owned user-defined layer.
- P4 correction: that case's read-back assertion was spelled in snake_case.
- P4: the registration-takeover test omitted the unauthenticated overwrite.
- P4: B1 attributed the preserved §13.10 annotations to a check the gate does not run.

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
- **OD-5. The code for an exchange the IdP refuses. Resolved by the prune pass
  below.** An unreachable IdP or a `5xx` from its token endpoint is transient and
  takes `registry.unavailable`. An IdP that answers and refuses the exchange with
  an OAuth error such as `invalid_grant`, or because the configured client
  credential is wrong, is permanent for that request, so `registry.unavailable`
  is disqualified for the same reason it is disqualified for the oversized token.
  Re-scoping the shipped retryable entry to cover a permanent dependency refusal
  was rejected, because it changes what existing callers are told. The code is
  `auth.exchange_failed`, refused with `502` and non-retryable, and S7 stages it
  with its §6.10 entry, its §6.9 row, its `error_envelope.go` entry, its
  `docs/reference/error-codes.md` row, and its `tools/matrix/matrices.go` axis
  cell, with the Routes test asserting it.

### Passes 5 through 9 (2026-08-22, automated)

One reversal from this span is kept in full, because a later pass that cannot see
a reversal repeats it. Every other finding is indexed on one line, naming the
deliverable and the section that carries it. The edit sites, the implementation
checklist, the verification matrix, and the Testing section carry the decisions.

**The reversal. The CSRF cookie was barred from the `__Host-` prefix on a false
premise.** The prefix constrains `Secure`, the absence of a `Domain` attribute,
and `Path=/`, and says nothing about `HttpOnly`, so a page-readable cookie can
carry it. Without it any host under the registry's registrable domain can plant
the CSRF cookie and forge a request that echoes the planted value, which defeats
the gate. A CSRF cookie therefore keeps the prefix and omits only `HttpOnly`.
"The CSRF position" and the cookie table under "The browser session" carry that
rule, the verification matrix's Session cookies row and the Routes test bullet
assert the cookie's attributes, and the CSRF test bullet carries the
mismatched-value case, because a stateless double-submit gives the server nothing
to compare against.

- P5: an expired session cookie cannot return `401` on a layer write, because the layer endpoints resolve the caller through `layerIdentityResolver`, which discards the verification error ("The browser session", the verification matrix's Session row, G1, and the expired-session Testing bullet).
- P5: no test pinned the new refusal on `reingest` against an admin-defined layer ("What lands" and the C1 test bullet).
- P5: the design brief's anonymous state asserted visibility filtering on a registry that enforces none (G1).
- P5: the transaction TTL was pinned by no test (the Routes test bullet, the mount-predicate end-to-end bullet, and the verification matrix's Session cookies row).
- P5 correction: the mount-predicate bullet closed with a count over the binaries it starts rather than over the mount predicate's configuration space.
- P6: the browser flow's authorization and token endpoints had no source, so `PODIUM_WEB_UI_OAUTH_AUTHORIZATION_ENDPOINT` and `PODIUM_WEB_UI_OAUTH_TOKEN_ENDPOINT` were added as environment-only acquisition values ("Enablement, guard, and mount", the §13.10 and §6.3.4 edit sites, the `docs/reference/cli.md:747` mirror row, and the guard test).
- P6: the key-placement rule contradicted its own precedent, so the enablement boolean and the transaction TTL carry a flag and the acquisition values are environment-only ("Where configuration keys go" and the `docs/reference/cli.md` mirror rows).
- P6: C2 consumed configuration C3 created while C3 depended on C2, so the fields, the flags, and the `PODIUM_*` reads moved into C2.
- P6: the CSRF predicate swept in the callback and broke re-sign-in, so the callback sits outside the same-origin gate ("The CSRF position", the §6.3.4 and §7 edit sites, the Routes test, and the verification matrix's Session CSRF row).
- P6: the layer-management authorization documentation was in no edit list (the `docs/reference/http-api.md:265-346` and `docs/reference/cli.md:440` mirror rows).
- P6: the design brief's layer field inventory was snake_case against a wire that carries Go field names (G1).
- P6: S44's stack could not run the browser flow, so its Keycloak client registration and its serve invocation are restaged (the manual-validation section and T1).
- P6: the markdown sanitization controls carried no test and the mechanical check no owner (the Sanitization and No-unsanitized-markup Testing bullets, B1, and the verification matrix).
- P6: the new `podium serve` flags were pinned by no test (the mount-predicate end-to-end bullet).
- P6 correction: making the S44 Keycloak client confidential broke the password-grant token mint that feeds its own negative control (the manual-validation section and T1).
- P6 correction: two Testing bullets cited the restaged S44 invocation as the web-UI-only posture.
- P6 correction: the rejection of discovery extension named a cost the registry already pays ("Enablement, guard, and mount").
- P6 correction: C2 absorbed the configuration keys but not the spec edits that name them, so C2 depends on S1 and S2.
- P6 correction: the `docs/reference/http-api.md:13-27` mirror row stated the CSRF requirement without the callback exclusion.
- P7: the staged register gate authorized an admin-defined layer's stored non-admin owner, so the owner arm is scoped to a stored user-defined layer ("What lands", the §7.3.1 edit site, the `docs/reference/http-api.md:265-346` mirror row, and the Registration takeover test bullet).
- P7: no test pinned that the callback's session cookie is a token the registry's own verifier accepts (the Routes test bullet and the verification matrix's Session Read cell).
- P8: the fixed decision claimed no shipped guard reads a web-UI key, which is false of the bind guard at `pkg/registry/server/config_validate.go:103-108`, and the citation for the public-mode exclusion is corrected to `pkg/registry/server/config_validate.go:88-91` (the Summary's fixed decisions).
- P9: the cookie-fallback test was placed in a file that owns no `oidcJWTVerifier` test, so the bullet ties itself to C2's call-site enumeration instead of naming a file of its own (the Cookie fallback Testing bullet).

### Prune 1 (2026-08-22, automated)

- **The cookie contract was stated at six sites and the review log had regrown
  past its yield.** No two statements disagreed, so this pass removed detail
  rather than repairing a mechanism. The cookie attributes now live in one table
  under "The browser session", listing each cookie with its prefix, `HttpOnly`,
  `Secure`, `Path`, `SameSite`, `Max-Age`, setter, and clearer, including the
  conditional CSRF row; "The CSRF position", its IMPLEMENTOR'S CHOICE, the
  verification matrix's Session cookies row, and the Testing section now cite
  that table and state only what is local to each. The Testing section's Routes
  bullet is split into one sub-bullet per discriminating case so a wrong clause is
  findable, and its file-name and line-anchored test inventories, which is where
  pass 9 found a wrong file name, are replaced by an IMPLEMENTOR'S CHOICE blank
  constrained to placing each case in the package that owns the function under
  test and asserting the cookie contract as the table states it. Passes 1 through
  4 and Redesign 1 collapse to the redesign summary, the reversals later passes
  cite, and a one-line index per finding; no deliverable depended on the deleted
  prose, because the edit sites and the checklist carry the decisions. OD-5 is
  resolved in the same pass rather than left as a forward reference a Testing
  bullet asserts against: the OAuth-refusal case takes the new non-retryable code
  `auth.exchange_failed` (`502`), staged under S7 with its §6.10 entry, its §6.9
  row, its `pkg/registry/server/error_envelope.go` entry, its
  `docs/reference/error-codes.md` row, and its `tools/matrix/matrices.go` axis
  cell.

### Pass 10 (2026-08-22, automated)

- **The staged §7 callback sentence ordered the `nonce` check before the
  exchange that produces the ID token.** The sentence read "validates the
  returned `state` and the ID token's `nonce` against it, exchanges the code
  server-side", which no implementation can perform, and it contradicted "The
  browser session" and the Routes test, both of which check the `nonce` on the
  exchanged token. The §7 edit site now validates `state`, exchanges the code,
  and then validates the returned ID token's `nonce`, and states why that order
  is forced.
- **The cookie table did not record sign-out as a clearer of
  `__Host-podium_auth`.** The table is the single statement of the cookie
  contract and the Testing section asserts every `Set-Cookie` against it, while
  the Summary, "The browser session", the §7 edit site, the verification matrix,
  and the Routes test all say sign-out clears both `__Host-podium_*` cookies. The
  row's "Cleared by" cell now reads "the callback, on every outcome; sign-out",
  and the pre-authorization bullet states the same, so the table and the sign-out
  test agree on how many clearing headers sign-out emits.
- **The staged `errorCodeRegistry` entries were pinned by no test.**
  `enrichEnvelope` returns immediately for a code the registry does not carry
  (`pkg/registry/server/error_envelope.go:88-92`), so an implementation that
  omits both entries still emits a `502` body whose `code` is
  `auth.exchange_failed` and passes the only listed assertion, which defeats the
  reason OD-5 created the code rather than reusing `registry.unavailable`. The
  Routes sub-bullet now also asserts `retryable: false` and a non-empty
  `suggested_action`, and a new Error envelope Testing bullet adds both codes to
  the shipped per-code tables `TestEnrichEnvelope_RetryableByCode`
  (`pkg/registry/server/error_envelope_test.go:52-72`) and
  `TestEnrichEnvelope_SuggestedActionCoverage` (`:89-110`).

### Pass 11 (2026-08-22, automated)

- **The callback had no disposition for the IdP's error redirect, so a cancelled
  sign-in fell through to a permanent `502`.** An authorization endpoint answers
  a declined consent prompt or a refused authorization request by redirecting to
  the registered redirect URI with `error` and `state` and no `code`. That
  callback carries a matching `__Host-podium_auth` cookie, so no
  `auth.csrf_invalid` arm fired, and the only remaining enumerated arm was the
  exchange, which the registry would have run with no code and the IdP would have
  refused, taking `502` `auth.exchange_failed` with `retryable: false` and a
  remediation naming the client credential and the redirect URI. That reports the
  most common user-side outcome of a sign-in attempt as an operator
  misconfiguration, tells the client the request cannot be retried when
  re-running sign-in is exactly the recovery, and emits a `5xx` on a routine
  path. The shipped device-code flow already switches on the IdP's `error`
  envelope and maps `access_denied` to its own outcome
  (`pkg/identity/oauth_devicecode.go:85-86`, `:210-217`), so collapsing it was
  not an in-house convention either. "What happens when it does not fire", the §7
  edit site, the verification matrix's Session Error cell, and the Routes test
  now state the disposition: after `state` validates, a callback whose query
  carries `error` rather than `code` runs no exchange, clears the
  pre-authorization cookie, sets no session cookie, and returns the browser to
  `/ui/` without establishing or replacing a session. It takes no error code, so
  the §6.10 catalog
  still gains only `auth.csrf_invalid` and `auth.exchange_failed`, and S7 is
  unchanged. The Routes test gains the case that drives the callback with
  `?error=access_denied` and a matching cookie and asserts that the stub token
  endpoint receives no request.
- **Correction: the error-redirect branch claimed a signed-out browser while
  clearing no session cookie.** The branch clears only `__Host-podium_auth`, and
  the cookie table names sign-out as the only clearer of `__Host-podium_session`,
  so on the case the proposal enumerates elsewhere, a cancelled re-sign-in from a
  browser that already holds a valid session cookie, the browser is still signed
  in when it lands on `/ui/`. The four sites now state the outcome the branch
  actually produces: the callback sets no session cookie, leaves any existing
  `__Host-podium_session` intact, and returns the browser to `/ui/` without
  establishing or replacing a session. The cookie table is unchanged, and the
  Routes case now runs twice, once with no session cookie and once with a valid
  one, and asserts on the second run that the response emits no `Set-Cookie` for
  the session cookie, so the intended reading is pinned by a test.

### Pass 12 (2026-08-22, automated)

- **`auth.untrusted_token`'s scope wording, its §6.9 row, its documentation row,
  and its gateway-naming remediation were left unstaged while the browser flow
  made the code reachable without a gateway.** The proposal asserted that the
  code kept its scope and that the only text moving was
  `auth.token_expired`'s remediation, so no `auth.untrusted_token` site appeared
  in any edit list. The cookie path reaches the code through the shipped verifier
  (`internal/serverboot/identity_verify.go:201-215`,
  `pkg/registry/server/identity_verify.go:88-100`) carrying a token the registry
  itself obtained through the §6.3.4 exchange, and in a directly reachable
  `oidc-jwt` deployment no gateway exists, so `spec/06-mcp-server.md:366` ("a
  forwarded `oidc-jwt` token"), the §6.9 row label at `:329` ("Untrusted
  forwarded token"), `docs/reference/error-codes.md:57` ("A gateway-forwarded
  `oidc-jwt` token"), and the remediation at `:374` and its mirror
  `pkg/registry/server/error_envelope.go:70-71` ("Verify the gateway forwards a
  token") all stopped describing when the code is returned and what to do about
  it, which is the same defect the proposal already stages a fix for on
  `auth.token_expired`. The code now moves with its sibling: the §6.10 and §6.9
  edit site states the rescoping of `:366` and `:329` and the restated
  remediation at `:374`, names the envelope mirror at `:70-71`, the mirror table
  gains a `docs/reference/error-codes.md:57` row beside the existing `:59` and
  `:60` rows, and S7, C2, the Summary, "What the mechanism does not change", and
  "The CSRF position" all state the same predicate: both codes keep their scopes
  and both have their gateway-assuming text restated to cover either accepted
  §6.3.3 location. The spec half is S7's, the documentation half is D1's, and the
  envelope mirror is C2's, which is where the other envelope edits already live.
- **Correction to the above: the same entry's canonical `message` and its two
  code mirrors were still unstaged.** The pass restated the scope sentence, the
  §6.9 row, the documentation row, and the remediation, and it wrote the general
  claim that the text assuming a gateway forwarded the token moves. It left the
  third gateway-assuming string inside the same entry in place:
  `spec/06-mcp-server.md:371` ("Forwarded token from issuer '…' failed
  verification."), emitted by `pkg/registry/server/identity_verify.go:103` from
  `ult.Issuer` with the issuerless fallback at `:107` ("The forwarded token could
  not be verified."), both in the `errors.As(err, &ult)` arm the same bullet
  cites as the path a session cookie reaches. A browser session that fails
  signature, `iss`, or `aud` verification would therefore return a message
  asserting a forwarding that never happened, and touching the spec example
  without the code would put the two out of agreement. The §6.10 and §6.9 edit
  site now names `:371` and its mirrors at `identity_verify.go:103` and `:107`,
  C2's enumeration names the same two code lines beside
  `error_envelope.go:70-71`, and "What the mechanism does not change" and "The
  CSRF position" both name the message alongside the scope wording and the
  remediation.

### Pass 13 (2026-08-22, automated)

- **§6.3.3's unqualified no-token-is-anonymous sentence was left unstaged while
  the precedence rule handed that same state to the cookie.** The §6.3.3 edit
  site described the section as enumerating two accepted credentials and stated
  that the amendment adds no verification rule and no new refusal, naming only
  the credential enumeration at `spec/06-mcp-server.md:96`. That paragraph closes
  with "A header value without the prefix carries no token, so the request is
  anonymous and sees public visibility only (§4.6)", which is precisely the state
  the staged precedence rule assigns to the `__Host-podium_session` cookie, so
  applying the edits as written would leave §6.3.3 calling the same request both
  anonymous and session-authenticated. The mirror table already stages the same
  narrowing on `docs/deployment/gateway-delegated-identity.md:58`, which restates
  this rule, so the authoring source was left behind its shipped mirror. The
  §6.3.3 edit site now names the closing sentence and states its narrowing in the
  same terms as the mirror row: where the browser flow is enabled, a request
  whose configured token header carries no bearer credential is anonymous only
  when it also presents no valid `__Host-podium_session` cookie, and elsewhere
  the sentence applies as written. S3 names the narrowed sentence alongside the
  second accepted location and the precedence rule.

### Pass 14 (2026-08-22, automated)

- **The §6.3.3 edit site cited a line that carries no anonymity rule.** The
  bullet closed with "Its own restatement at `:94` is inside that range", which
  asserted that §6.3.3 states the no-token-is-anonymous rule a second time at
  `spec/06-mcp-server.md:94` and that the staged range already covers it. That
  line is the claim and tenant-derivation paragraph: it matches the caller's
  subject and `email` against `users:` visibility and derives the §4.7.1 tenant
  from the verified `org_id` claim or the `X-Podium-User-Org` header. It says
  nothing about a request that carries no token. The sentence dates from the
  withdrawn design in which the amendment added a third accepted credential and
  that paragraph's enumeration would have had to move. The only other statement
  of the anonymity rule inside `:92-112` is `:108`, which the same bullet
  excludes because the browser flow cannot be enabled under `trusted-headers`.
  The bullet now records that `:92-94` stand as written, because the amendment
  adds no credential and changes no tenant derivation, and that `:96` is the
  only sentence in the range the amendment narrows.

### Redesign 2 (2026-08-22, automated)

Two areas were redesigned: the inventory of shipped text that assumes a gateway
forwarded the `oidc-jwt` credential, and the review log for passes 5 through 9.

**Why the inventory was redesigned.** The proposal carried that inventory as five
differing prose enumerations, in the Summary, in "What the mechanism does not
change", at the §6.10 and §6.9 edit site, in "The CSRF position", and in two rows
of the shipped-documentation-mirror table, with none of them authoritative.
Passes 12, 13, and 14 each added one site, pass 12 needed a correction inside its
own round to finish its own fix, and sites remained unstaged inside the exact
functions C2 rewrites, including every `pkg/identity` doc comment and the four
client-visible Go strings. Adding a site meant editing five places, and a site
added to one of them was invisible to the other four.

**What replaced it.** One table, "The credential-location table", under "The
browser session" beside the cookie table that already serves the same purpose.
Each row names a site by `file:line`, what it says today, and the checklist step
that stages it. The decision rule and the reproducing `grep` sit beside the
table, so a reviewer who suspects a missing site reruns the sweep and applies the
rule rather than reading the prose again. The table is exhaustive for what moves;
the rule disposes of everything else, and the not-moved lists record only the
sites earlier rounds re-found. Every other site in the proposal now cites the
table by name and states only what is local: the Summary, S1, S3, S7, C2, D1, T1,
the §6.3.3 edit site, the §6.10 and §6.9 edit site, "The CSRF position", and the
two mirror-table rows. The mechanism itself is unchanged. No field, type,
interface, error code, or store method is added, and the consolidation is
net-negative in prose.

**What the redesign reverses.** Pass 14 recorded that `spec/06-mcp-server.md:92-94`
stand as written. `:92`'s first clause asserts that both §6.3.3 providers serve a
deployment running the registry behind a gateway, which is false for a directly
reachable `oidc-jwt` registry running the browser flow, and its code mirror
`pkg/identity/registry.go:69-70` was already staged under C2. S3 now stages the
clause and `:94` still stands.

**What the redesign deletes.** The Summary's restatement of why the two codes'
text moves; the enumeration of which strings move inside "What the mechanism does
not change", whose heading and opening sentence survive so the pass 12 references
still resolve; the `auth.tenant_unknown` untouched sentence at the §6.10 and §6.9
edit site, now a not-moved row on the table; the per-string argument for
`auth.token_expired` and `auth.untrusted_token` at that same edit site; the two
mirror-table rows' inline account of what changes in each
`docs/reference/error-codes.md` row; "The CSRF position" restatement of the same
list; and the inline site lists inside the C2 and S7 checklist entries. Nothing
deleted is the sole statement of a deliverable, because every deleted site's
content is carried by a table row whose Staged by column names S1, S3, S7, C2,
D1, or T1.

**Why the review log was pruned, and what it deleted.** Passes 5 through 9 had
regrown past the point where a later pass reads them, which is the condition
Prune 1 already handled for passes 1 through 4. Their full text is replaced by
one index line per finding, naming the deliverable and the section that carries
it. Pass 5's `__Host-` prefix reversal is kept in full, because a later pass that
cannot see a reversal repeats it. No deliverable depended on the deleted prose.

**What the redesign adds to Testing and to the verification matrix.** Nothing
pinned the four client-visible Go strings:
`TestEnrichEnvelope_SuggestedActionCoverage` lists neither code and asserts only
non-emptiness for the codes it does list, and the route-level cases assert
`env.Code` and never `env.Message`, so an implementation that landed every spec
and documentation row while leaving all four strings naming a gateway passed the
whole suite. A unit bullet under C2 asserts each `suggestedAction` and each
`message` verbatim against the amended spec strings, and the verification matrix
gains a Session remediation text row. The rest of the table's rows are comments
and prose that emit nothing, so review against the table holds them.

**Open decisions this redesign records.**

- **OD-6. The level that pins the four client-visible strings.** The unit case in
  `pkg/registry/server` asserts the four strings against the registry table and
  `writeIdentityError`, matches a shipped precedent in the same file, and needs
  no browser-flow fixture, at the cost of not pinning the envelope a browser
  receives. An integration case on a registry with the browser flow enabled would
  pin that envelope, at the cost of a fixture and of asserting by the absence of
  the substrings "forward" and "gateway" rather than against the spec string.
  Default: the unit case, which is what Testing states. The integration case is a
  superset in coverage and can be added later without changing any other site.
- **OD-7. Whether `internal/serverboot/identity_verify.go:261` and `:277` move.**
  They are the audience guard's doc comment and its startup error message, which
  say the `aud` claim is verified on every forwarded token. After the amendment it
  is verified on every token in either accepted location, so the strings are
  narrow rather than false, and the guard itself is unchanged. Default: move them,
  as the table states, because the sweep re-finds them otherwise and every
  assertion on that message matches the `config.oidc_jwt_audience_unset` code
  prefix alone. The alternative records them as not-moved with "narrow but not
  false" as the reason, which is one row either way.
- **OD-8. `spec/06-mcp-server.md:355` and the new-codes edit.** The sentence reads
  "The gateway-delegated providers (§6.3.3) add three `auth.*` codes", and S7 adds
  `auth.csrf_invalid` and `auth.exchange_failed` to the same §6.10 region.
  Default: S7 owns `:355` and restates it so it no longer attributes the codes to
  a gateway, and the new-codes edit decides where the two new entries are
  introduced. If that edit rewrites the paragraph, the table's `:355` row is
  satisfied by that rewrite rather than by a second one.

### Pass 15 (2026-08-22, automated)

- **Two shipped restatements of the §6.3.3 fronting-gateway clause were on the
  not-moved list.** `docs/getting-started/how-it-works.md:413-416` repeats
  `spec/06-mcp-server.md:92` almost verbatim and adds "`oidc-jwt` verifies the
  forwarded IdP-signed token on every request", and
  `docs/deployment/integrations.md:86` states "The registry verifies a
  gateway-forwarded IdP-signed JWT against the issuer's JWKS on every request".
  S3 stages that clause in its authoring source and C2 stages its code mirror
  `pkg/identity/registry.go:69-70`, so the gateway scoping cannot be the
  exemption that keeps the two pages as written: that scoping is the text S3
  declares false. Both satisfy the credential-location table's rule, because a
  request whose token arrives in `__Host-podium_session` reaches both texts and
  neither the gateway framing nor "forwarded ... on every request" is true of it.
  Leaving them behind would repeat the pass 13 defect in the opposite direction,
  where a shipped mirror had been staged and its authoring source left behind.
  Both are now rows of the credential-location table under D1, restated the same
  way S3 restates `spec/06-mcp-server.md:92`. The not-moved list's remaining docs
  entries do satisfy the rule, and `docs/deployment/oidc/index.md:47` now carries
  its opening clause inline so a later reader can check the scoping without
  opening the page.

## Relationship to proposal 0012

0012 corrected §13's account of what the registry accepts and does. Its decision
3 verified that the shipped SPA attaches no credential, narrowed the web-UI
paragraph to state that plainly, and routed in-browser authentication here. The
sentence S1 amends is the one 0012 wrote, and the page
`docs/deployment/gateway-delegated-identity.md` names as this proposal's
obligation is the one 0012 recorded.
