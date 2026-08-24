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
  `GET /v1/ui/session`, which reports the deployment's posture and the caller's
  own resolved subject. That read is what the UI's sign-in control and its
  rendering rules key on. "The browser session" below specifies both, and "The
  posture read" there states the body.
- The layer write handlers gain owner-or-admin authorization, stated once as the
  layer-write authorization rule under "The layer-ownership defect" and staged
  into §7.3.1 by S6. Today any caller can delete or rewrite another user's
  user-defined layer, and a re-registration under an existing layer's ID
  overwrites it without any owner comparison. §7 states an owner rule today only
  for reingest and reorder, and the code implements it for neither.
- The built React bundle is committed to the tree so `go build` and `go install`
  keep working from a clean clone with only a Go toolchain, with a CI check that
  rebuilding produces no diff.
- The §6.10 catalog gains `auth.csrf_invalid` and `auth.exchange_failed`.
  `auth.csrf_invalid` (`403`) is the one code for the §6.3.4 browser-origin
  gate's refusal and for the callback transaction's, in the scope "The CSRF
  position" states. `auth.exchange_failed` (`502`) covers a callback whose code
  exchange fails, in the scope the exchange-failure rule under "The browser
  session" states. "The CSRF position"
  below is the single statement of the gate predicate, including the evidence it
  reads, which routes it excludes, and why, and of what `auth.csrf_invalid`
  covers. `auth.forbidden` is broadened by S6. `auth.token_expired` and
  `auth.untrusted_token` keep their scopes and gain no replacement code; a
  session cookie carries a token the registry itself obtained rather than one a
  gateway forwarded, so what changes on them is the shipped text that assumes a
  gateway forwarded the token, as the unchanged-scope statement under "The
  browser session" states. The credential-location rule under "The browser
  session" is the single statement of which text that is.

**Fixed decisions.**

- **Authentication is registry-mediated, and the registry keeps no session
  state.** No credential is reachable from JavaScript. The alternative of a
  browser-held token under a pure-SPA flow is withdrawn: this proposal newly
  renders author-controlled markdown on the same origin, and a token reachable
  from JavaScript on that origin is the combination the chosen route removes. The
  cookie carries the credential §6.3.3 already accepts rather than one the
  registry mints. The no-session-state rule under "The browser session" is the
  single statement of what that leaves the registry holding, and "Revocation is
  expiry" under the same section is the single statement of the session's
  revocation and renewal model.
- **The layer-ownership gap is closed here rather than filed separately**, because
  the layer panel is the surface that exposes those operations to a browser and
  a panel must not present per-owner scoping as server-enforced while the server
  fails open.
- **The build output is committed.** The bundle is committed rather than
  generated at release, so a clean clone keeps building with only a Go toolchain,
  which is a property the project has today and would otherwise lose. "Build and
  embedding" states the committed-bundle constraints. The staleness risk the
  commitment creates is closed by the rebuild-is-clean CI check, which is part of
  the deliverable rather than a follow-up.
- **The UI gains no privileged access.** It is a client of the HTTP API and
  reads the catalog and the layer list through the same endpoints an SDK would,
  as §13.10 states. The one read it adds is `GET /v1/ui/session`, and "The
  posture read" under "The browser session" is the single statement of what that
  read requires, what it discloses, and what it adds.
- **The implementor does not design the UI.** `web/DESIGN.md` is the design
  brief; a design pass against it produces the layouts and the state treatments.
- **The browser flow is one enablement key with one guard and one mount site.**
  The key is `--web-ui-auth` / `PODIUM_WEB_UI_AUTH`, the guard is
  `StartupConfig.Validate` (`pkg/registry/server/config_validate.go:87`), and the
  mount is a nested check inside the block that already serves `/ui/`
  (`internal/serverboot/serverboot.go:1229`). The startup guard under "The
  browser session" is the single statement of the conjuncts the guard requires,
  the `config.web_ui_auth_unconfigured` refusal it emits, its ordering against
  the shipped public-mode exclusion, and why no shipped guard covers the
  combination. The mount predicate stated under "The browser session" is
  the single statement of what the guard's web-UI conjunct buys at the route
  mount. This bullet adds nothing to either.
- Artifacts stay authored in git. The UI is a reader and a layer manager.

**Watch out for.**

- **CSRF is an obligation this panel creates and the proposal specifies it.**
  A credential the browser attaches by itself authenticates any request the
  browser is induced to make, so every layer write becomes forgeable across
  origins. The session cookie is not the only such credential the deployments
  §13.10 blesses put in a browser's hands. The prior review of this proposal
  never produced a finding on it across eight rounds while treating it as
  acknowledged prose, which is how a known gap stays open. "The CSRF position"
  below is the single statement of the gate predicate, and the gate reads the
  request rather than the credential.
- **Closing the ownership gap changes the authorization behavior of every layer
  write handler**, including the ones the panel does not call. It does not change
  the behavior of a registry that authenticates no caller, and it refuses a
  caller who resolves no subject wherever the gate is live. The deployment
  carve-out under "The layer-ownership defect" states when the gate is live and
  what follows where it is not, and the permissive `NewLayerEndpoint` defaults
  stated in the same section are why
  `test/integration/reingest_pipeline_test.go:87` posts to reingest with no
  credential and keeps passing. The surface that
  regresses is a user-defined layer driven by a non-owner identity on a registry
  that has an identity provider, and that case has no test today.
- **The key-placement rule is stated once**, under "Where configuration keys go".
  It is easy to restate divergently, because §6.3, §13.10, and §13.12 each look
  like the right home and only one of them is.
- **`GET /v1/layers` is unfiltered.** The unfiltered-list rule under "The
  layer-ownership defect" is the single statement of what the read does and what
  follows from it. G1's correction to the design brief and the Non-goals
  exclusion both rest on it.
- **An unverifiable session reports a different result on each surface.** The
  expiry-signal rule under "The browser session" is the single statement of what
  each surface returns, and G1 lands it in the design brief. The hazard is that a
  design or a panel built on one expiry signal reads a layer write's `403` as an
  ownership decision and never learns that the session ended.
- **The read-only classification is stated once**, under "The browser session".
  Sign-in, the callback, sign-out, and the posture read all sit outside the
  §13.2.1 write set, and a read-only registry serves each of them unchanged, so
  an operator sees no authentication outage during a primary outage. It is easy
  to restate divergently, because each edit site looks like the place to derive
  the classification for itself, and a derivation that keys on reads rather than
  on mutation reaches the right answer from a premise §13.2.1 does not require.
  That none of these surfaces writes registry state is a conjunct of the
  no-session-state rule under the same section.

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
      `oidc-jwt` credential, the header-wins precedence rule under "The browser
      session" stated there in spec voice, the narrowed no-token-is-anonymous
      sentence at `spec/06-mcp-server.md:96`, and the `spec/` half of the set the
      `spec/06-mcp-server.md:92` row under "The browser session" names, meaning
      the restated opening clause and sends-no-credential sentence at
      `spec/06-mcp-server.md:92` and the §2.2 mirror at
      `spec/02-architecture.md:101`. The code, documentation, and test halves of
      that set land under C2, D1, and T1.
      Levels: —. Depends on: S2
- [ ] **S4 · spec** — SPEC-4. §7's sign-in, callback, and sign-out routes with
      their methods, their paths,
      their cookies, and their mount predicate, and the posture read
      `GET /v1/ui/session` with its body, its unauthenticated status, and its
      web-UI mount predicate, together with the one §13.2.1 classification
      covering all of them, per the read-only classification under "The browser
      session", which leaves §13.2.1's own text unchanged.
      Levels: —. Depends on: S2
- [ ] **S5 · spec** — SPEC-5. §11's verification entry for the UI, covering the
      matrix the generating rule under "Verification matrix" below produces.
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
- [ ] **C1 · code** — CODE-1. The layer-write authorization rule under "The
      layer-ownership defect", implemented on the layer write handlers with the
      tests "Testing" enumerates.
      Levels: unit, integration, e2e. Depends on: S6, S7
- [ ] **C2 · code** — CODE-2. The browser-flow configuration surface, meaning the
      `Config` and `StartupConfig` fields for every key the key-placement rule
      under "Where configuration keys go" lists, the `podium serve` flags that
      rule places, and the `PODIUM_*` reads beside
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
      including the `oidc-jwt` startup log line, which C2 restates under the
      boot-log adjacency constraint in "restated remediation and message
      strings"; `test/e2e/auth_oidc_jwt_test.go` joins C2's write set only if
      that restatement breaks the constraint. That
      set spans `pkg/registry/server`, `internal/serverboot`, and
      `pkg/identity`, and the rule together with its recorded command is what
      determines it; none of the `pkg/identity` edits changes behavior or a
      signature, because the cookie branch lives in `serverboot` and
      `OIDCVerifier.Verify` receives a raw token with no knowledge of its
      origin.
      Levels: unit, integration, e2e. Depends on: S1, S2, S3, S4, S7
- [ ] **C3 · code** — CODE-3. The web-UI authentication configuration guard in
      `StartupConfig.Validate`, implementing the startup guard under "The browser
      session" over the fields C2 adds, appended after the shipped public-mode
      exclusion so the ordering that statement records holds; the bind-guard
      rationale restatements in the same file
      (`pkg/registry/server/config_validate.go:29` and `:99-101`), which the
      §13.10 bind-guard edit site names; and the route mount at
      `internal/serverboot/serverboot.go:1229`, per the mount predicate stated
      under "The browser session".
      Levels: unit, e2e. Depends on: S1, C2
- [ ] **B1 · code** — BUILD-1. The React toolchain, the committed bundle, the
      `go:embed` change, `web/web_test.go`, the served-bundle end-to-end
      assertion, the rebuild-is-clean CI check, and the
      `dangerouslySetInnerHTML` check under "Rendering untrusted content".
      Levels: unit, e2e. Depends on: —
- [ ] **U1 · code** — UI-1. The UI surfaces built against `web/DESIGN.md`,
      including the sanitized markdown rendering path and its sanitizer cases,
      the posture read on load together with the sign-in and sign-out
      affordances G1's sign-in control table gates on it and the rest of the
      posture-keyed rendering rules G1 states,
      and the client half of the CSRF gate as the wire contract under "The
      CSRF position" states it, which is a deliverable only where the mechanism
      C2 lands carries a request-side value.
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

**What lands.** This paragraph, together with the paragraphs that follow it in
this section through the deployment carve-out below, is the single statement of
the layer-write authorization rule, covering the gated operations, every arm of
the rule, the code each refusal returns, where in each handler the gate runs, and
the conditions under which it is live. Every other site in this proposal cites
the rule by name and states only what is local to that site, including the §7.3.1
edit site that stages its spec text.

`register`, `unregister`, `update`, `restore`, `reorder`, and `reingest` are
gated. On a stored layer that is user-defined, the operation is authorized to
that layer's stored `Owner` or to a tenant admin holding the §4.7.2 admin grant.
On a stored layer that is admin-defined, the operation is authorized to a tenant
admin alone, whatever that layer's stored `Owner` field names. The qualifier on
the owner arm carries weight: an admin-defined layer's `Owner` is assigned from
the request body on the admin-defined branch of `register`
(`pkg/registry/server/layers.go:659`) and patched on the admin-defined branch of
`update` (`:547-549`, `docs/reference/http-api.md:329`), so it is a
caller-supplied field and names no authorized subject. On `register` the gate is
conditional on the request's ID naming a layer that already exists in the tenant,
and the arm taken is decided by the stored layer's class and stored owner rather
than by anything in the request body. A `register` whose ID names no stored layer
is admitted for any authenticated caller. A layer that is soft-deleted and still
inside its §8.4 recovery window (`spec/08-audit-and-observability.md:52`) is a
layer that exists for this rule, so a `register` under its ID is authorized
against its stored owner and its user-defined flag on the same terms, and the
recovery window is not a window in which an ID can be taken over.

A caller authorized by neither arm is refused with `403` `auth.forbidden`,
whether that caller resolves a different subject or resolves none at all, and the
refusal is `403` rather than `404`. The comparison runs against the verified
subject rather than against a client-supplied field, and the admin path stays
able to act on any layer in the tenant. A `register` whose existence lookup fails
is refused with `500` `registry.unavailable` and writes nothing, because a store
failure establishes neither that no layer holds the ID nor who owns the layer
that does. The staged §7.3.1 sentence names no code for that refusal, because
§6.10 carries no prose entry for `registry.unavailable` even though the code sits
on the §6.10 matrix axis (`tools/matrix/matrices.go:109`); the code is stated
here, documented at `docs/reference/error-codes.md:158` as `500`, and asserted by
C1's tests.

Where the gate runs inside each handler is part of the rule. On `reingest`, which
today runs no authorization at all (`pkg/registry/server/layers.go:946-991`), the
gate runs after the layer is loaded, which is what supplies the owner to compare
against, and before `runIngestAndRespond`, so a refused caller triggers neither a
Git fetch nor the break-glass freeze bypass. On `register` the existence lookup
and the owner comparison both run ahead of the `req.UserDefined` short-circuit at
`pkg/registry/server/layers.go:610-611`, so a request body that asserts
`user_defined` cannot skip the gate the way it skips `authAdmin` today.

The admin-defined collapse is a rule S6 introduces rather than one §7 states
today. The manual-reingest trigger row reads "(admin or layer owner)" with no
layer-class qualifier (`spec/07-external-integration.md:65`), so as written it
admits an admin-defined layer's stored non-admin owner, and S6 restates its
parenthetical the way `:87` already scopes reorder. The §0 quickstart carries the
same unqualified rule for the same operation over an admin-defined layer
(`spec/00-quickstart.md:46-47`), so S6 restates that comment as well. The §7.3.1
edit site stages both restatements.

The recovery-window arm of the rule is load-bearing rather than incidental.
`PutLayerConfig` upserts on `(tenant_id, id)` and writes
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
tombstoned. That is the takeover the rule closes, in the window where it is
unrecoverable. No shipped handler determines existence over both sets:
`restore` reads a tombstoned layer's `Owner` and `UserDefined` through a
`ListDeletedLayerConfigs` scan alone (`pkg/registry/server/layers.go:799-816`),
because its target is tombstoned by definition.

**IMPLEMENTOR'S CHOICE:** the store call sequence that implements the existence
lookup on `register`. Any answer satisfies the layer-write authorization rule
above and adds nothing to it: it determines existence over both live layers and
soft-deleted layers still inside the §8.4 recovery window, it treats a failed
call as the refusal the rule states rather than as evidence that the ID is
unused, and it runs where the rule places it. The idiom to follow is the one
`unregister` and `reingest` already use for the same discrimination
(`pkg/registry/server/layers.go:847-855`) together with `restore`'s scan arm
(`:802-806`), rather than `update`'s collapse of every `GetLayerConfig` failure
into `404` (`:487-491`), which is safe there only because not-found refuses on
`update` and admits on `register`. An arm the rule above does not carry is a
defect in the implementation.

**IMPLEMENTOR'S CHOICE:** whether the gate needs an atomicity guarantee beyond
what `PutLayerConfig` gives today, for two registrations racing under the same ID
between the existence lookup and the upsert. Any answer is stated once, in this
section, and if it is that the shipped upsert's behavior is accepted unchanged,
that answer is recorded here as a decision with its reason, so a later reviewer
does not rediscover it as a further arm of the lookup.

**The deployment carve-out.** This paragraph is the single statement of when the
owner gate is live and of what follows where it is not. Every other site in this
proposal cites it by name and states only what is local to that site.

The gate is live only where an identity provider is configured and public mode is
off. The condition is read from the registry's configuration rather than from the
request, so it holds identically for every caller and for every layer write
handler, including the ones the panel does not call. It is the same short-circuit
the admin gate already takes (`internal/serverboot/serverboot.go:1213`). No
registry starts with both an identity provider and public mode set, because
§6.3.3 makes `oidc-jwt` and `trusted-headers` mutually exclusive with public mode
and refuses that startup with `config.public_mode_with_idp`
(`spec/06-mcp-server.md:96`), and those are the registry-process providers. A
running registry that reports a configured identity provider therefore has public
mode off, which is what lets a surface holding only that one flag decide the
gate's state.

On a registry that authenticates no caller, which is the default standalone and
public-mode posture and the posture §13.10's own web UI targets
(`spec/13-deployment.md:170`), every layer write is admitted and closing the
ownership gap changes nothing. That is what keeps §13's statement that "the
layer-management and erase endpoints admit any request"
(`spec/13-deployment.md:33`) true, keeps standalone and standard behaving
identically on the same handler, and keeps a layer registered with
`podium layer register --user-defined --owner alice` manageable by the local
operator. That invocation is the one that produces a user-defined layer with a
stored owner on such a registry: the CLI sends `owner` only inside the
`--user-defined` branch (`cmd/podium/layer.go:224-227`), and the handler falls
back to the request body's `owner` when no authenticated identity resolves
(`pkg/registry/server/layers.go:643-646`, `docs/reference/cli.md:423`). The erase
endpoint reaches the same short-circuit through the same admin hook, which is why
§13 states the two endpoints together and why no restatement of this carve-out
claims it is specific to layer writes.

Where the gate is live, a caller who resolves no subject is refused with `403`
`auth.forbidden` like any non-owner. That arm matters because §6.3.3 makes a
request anonymous rather than rejected while the issuer JWKS is unreachable
(`spec/06-mcp-server.md:98`), so a caller resolving no subject is a routine
runtime state on a registry that authenticates callers.

The spec renders the carve-out the way §4 already renders the parallel carve-out
for the re-embed endpoint: "Configuring an identity provider makes the gate live,
whether or not the registry verifies callers itself"
(`spec/04-artifact-model.md:760`). §4 qualifies its own exclusivity with "does
not extend to the other admin-gated endpoints, whose posture is defined in §4.7.2
and §7.3.2", and the layer write gate is neither admin-only nor specified in
either section, so that spec sentence stands as written and only unqualified
restatements of it move.

**The bare constructor's permissive defaults.** This paragraph is the single
statement of what an endpoint built with the bare `NewLayerEndpoint` authorizes,
and every other site cites it by name. `NewLayerEndpoint` installs a default
`authAdmin` that returns nil for every caller and a default identity resolver
that returns `layer.Identity{IsPublic: true}`, which resolves no subject
(`pkg/registry/server/layers.go:174-175`). Such an endpoint therefore admits
every request on the admin arm of the gate this section adds, whatever the
layer's kind, and names no owner. The construction is test-only. A deployment
wires `pkg/registry/core.AdminAuthorize` into `authAdmin`
(`pkg/registry/server/layers.go:1156`) and the serverboot resolver into the
identity hook, so the permissive default is never the posture a registry serves.
Two consequences follow. `test/integration/reingest_pipeline_test.go:87` posts to
reingest with no credential and keeps passing unchanged once the gate lands,
because the default `authAdmin` admits its caller on the admin arm and that arm
acts on any layer in the tenant; the layer the test registers is admin-defined,
which is a property of the fixture rather than a condition of the pass. A test
that asserts a refusal overrides both defaults, installing a denying
`WithAdminAuth` and a `WithIdentityResolver` that resolves a non-owner or no
subject, because overriding one leaves the other default admitting the request.
The surface that regresses is a user-defined layer driven by a non-owner identity
on a registry that has an identity provider, and that case has no test today.

**IMPLEMENTOR'S CHOICE:** whether the owner comparison reads the caller's subject
through the same helper the cap count uses or through the request-identity
accessor the admin gate uses. Any answer satisfies the layer-write authorization
rule above and adds nothing to it.

**The unfiltered-list rule.** This paragraph is the single statement of what the
layer surface's read does and what follows from it. Every other site in this
proposal names the rule, cites it here, and states no condition, caller, or value
the statement here does not carry.

`GET /v1/layers` runs no authorization function and evaluates no visibility or
owner predicate. The `GET` arm dispatches straight to the list handler
(`pkg/registry/server/layers.go:336-343`), which reads the endpoint's single
tenant and returns every layer config stored under it (`:762-777`), so the tenant
is the only scoping the list carries. `GET /v1/layers?deleted=true` returns that
tenant's soft-deleted layers on the same terms (`:763-770`). The response body
carries the layer configs alone and echoes no caller, so it reports neither who
asked nor that anything was withheld. An anonymous caller, an authenticated
non-owner, an admin, and a caller whose credential fails verification each
receive the same list, because the list handler consults no identity and the
layer endpoint resolves the caller through a resolver that discards the
verification error and returns the anonymous-public identity
(`internal/serverboot/identity_verify.go:55-63`).

The catalog endpoints behind `load_domain`, `search_artifacts`, and
`load_artifact` are the contrast. Each evaluates the §4.6 visibility predicate
against the resolved caller, and that predicate admits every layer in public mode
and on a registry that configures no identity provider
(`pkg/layer/composer.go:65`, `spec/04-artifact-model.md:615`,
`spec/13-deployment.md:33`). The layer list evaluates no predicate on any
deployment.

The gate C1 adds is on the write handlers this section names, and it changes no
read: an expired or untrusted credential changes a write's disposition and leaves
the list read as it is. This proposal adds no server-side filtering to the read,
which the Non-goals section records as an exclusion. The panel's role split is
therefore presentation over a list the server hands it whole, and a design brief,
a documentation page, or a site in this proposal that describes the layer list as
scoped to the caller states something false and is corrected rather than
accommodated. G1 corrects the one such sentence in `web/DESIGN.md`.

## The browser session

The session cookie carries the token §6.3.3 already accepts. What that leaves the
registry holding is the no-session-state rule below, which is the single
statement of it.

This section is the single statement of the pre-authorization transaction
contract: the HTTP method each route answers on, what the sign-in redirect
carries, what `__Host-podium_auth` holds, what the callback compares and in what
order, what each outcome sets and clears, and which code each refusal returns.
Every other site in this proposal cites the contract by name and states only
what is local to that site.

**The route methods.** This paragraph is the single statement of the HTTP method
each authentication route answers on. Every other site in this proposal cites it
by name and states only what is local to that site.

Sign-in and the callback answer on `GET`. Each is a top-level navigation, and a
`SameSite=Lax` cookie is delivered on a cross-site request only when that request
is a top-level navigation with a safe method, which is what makes
`__Host-podium_auth` reach the callback on the identity provider's redirect back.
Neither route can carry a request-side CSRF value a page set, which is why "The
CSRF position" excludes both by name as well as by method.

Sign-out answers on `POST`. It is a non-navigation call the page issues, which is
what lets it carry the `X-Podium-CSRF` header where the landed mechanism carries
a request-side value, and a top-level navigation could not. `POST` is also what
places sign-out inside the §6.3.4 gate under that section's method predicate, so
the gate reads sign-out as state-changing.

The methods are fixed here rather than left to the implementor, because the mux
registration, the Authentication section of `docs/reference/http-api.md`, the §7
entry, the S45 step-4 rewrite, and the new sign-in scenario all have to spell
them identically, and because the sign-out method decides whether the §6.3.4 gate
covers sign-out at all.

**What the cookie holds.** The callback exchanges the authorization code
server-side for an access token whose `aud` is `PODIUM_OAUTH_AUDIENCE`, which is
the token a device-code CLI also presents, and returns it in the
`__Host-podium_session` cookie. The cookie therefore adds no credential to
§6.3.3, per the no-session-state rule below: it carries the same IdP-signed JWT
the `oidc-jwt` provider already verifies on every request
(`spec/06-mcp-server.md:96-100`), which is why the access token is what the
cookie carries rather than the ID token. A deployment whose IdP issues opaque
access tokens cannot use the browser flow, which is the constraint the shipped
`oidc-jwt` path already imposes on a gateway-forwarded token.

**The cookie table.** The browser flow sets the cookies below and no others. This
table is the single statement of the cookie contract. Every other site in this
proposal cites it by name and states only what is local to that site.

| Cookie | Prefix | `HttpOnly` | `Secure` | `Path` | `SameSite` | `Max-Age` | Set by | Cleared by |
|:--|:--|:--|:--|:--|:--|:--|:--|:--|
| `__Host-podium_session` | `__Host-` | yes | yes | `/` | `Lax` | absent | the callback | sign-out |
| `__Host-podium_auth` | `__Host-` | yes | yes | `/` | `Lax` | the configured transaction TTL | sign-in | the callback; sign-out |
| `__Host-podium_csrf`, present only where the mechanism C2 lands carries a request-side value per the wire contract under "The CSRF position" | `__Host-` | no | yes | `/` | `Lax` | absent | the callback | sign-out |

The `__Host-` prefix is the browser-enforced binding control: it forbids a
`Domain` attribute and forces `Secure` and `Path=/`, so no sibling host can plant
any of these cookies, and it is why none of them needs a server-side signing key.
The prefix places no constraint on `HttpOnly`, so the page-readable CSRF row
keeps that anti-planting property while the page reads the value. `Secure` is
unconditional on every row, including the page-readable CSRF row. What that
requires of the registry's browser-facing origin is stated by the redirect-URI
conjunct under "The browser session", which the startup guard enforces.
`SameSite=Lax` rather than `Strict` is
forced by `__Host-podium_auth`, which has to survive the IdP's cross-site
redirect back to the callback.

- `__Host-podium_auth` holds the pre-authorization transaction: the `state`, the
  `nonce`, and the PKCE `code_verifier` the sign-in route mints. Its `Max-Age`
  bounds the sign-in window at 10 minutes by default, tunable by
  `--web-ui-auth-transaction-ttl` / `PODIUM_WEB_UI_AUTH_TRANSACTION_TTL` per the
  key-placement rule under "Where configuration keys go". When and how the
  callback and sign-out clear it is stated under "The callback order and
  outcomes".
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

**The callback order and outcomes.** The callback reads `__Host-podium_auth` and
compares the returned `state` against it before inspecting anything else in the
query, so a callback whose `state` is absent or unequal is refused whatever else
that query carries. It then branches on the IdP's `error` parameter: a query
carrying that parameter runs no exchange, whatever else that query carries, and a
query carrying no `error` parameter is exchanged server-side at
`PODIUM_WEB_UI_OAUTH_TOKEN_ENDPOINT` with the `code` the query carries, the
`code_verifier` the cookie holds, and the configured client credential. The two
arms partition the query, so a query carrying both parameters takes the first arm
and a query carrying neither takes the second, presenting no `code` to the token
endpoint, which refuses it at the OAuth protocol level and so reaches the arm the
exchange-failure rule below gives that refusal. The `nonce` of the ID token
that exchange returns is compared against the cookie's `nonce` after the
exchange, because the ID token does not exist until the token endpoint answers.

This paragraph is the single statement of what the callback sets and clears on
each outcome. Every other site in this proposal cites it by name and states only
what is local to that site. It states the rule that generates the outcomes rather
than a list of them, so a site that reaches an outcome derives what that outcome
sets and clears.

On success the callback returns the access token in `__Host-podium_session`.
Clearing `__Host-podium_auth` is a property of the route rather than of the
outcome a request reaches: the callback consumes the pre-authorization
transaction on the request that delivers it, so every response the callback emits
carries an explicit clearing `Set-Cookie` for `__Host-podium_auth`. That covers
the success response, every refusal the callback returns, and the declined-consent
redirect that takes no error code, and it holds on a request that delivered no
cookie for the callback to consume.

The outcomes the rule ranges over are generated by the order stated above
together with the exchange-failure rule below, and a reader derives one instead
of looking it up: the `state` comparison against the cookie, then the branch on
the query's `error` parameter, then, for the exchanged arm, whether the token
endpoint answered
the exchange and whether that answer refused it, and then, for an answer carrying
tokens, the `nonce` comparison against the cookie. The first comparison that
refuses ends the cascade, and the clearing obligation holds at every point of it,
so an outcome added to the cascade later is covered without an edit here.

Leaving the cookie to reach its `Max-Age` does not satisfy this, because that
`Max-Age` is the operator-tunable transaction TTL and an uncleared cookie stays
replayable for the rest of that window. The clearing is what makes the
transaction single-use: the `state`, the `nonce`, and the PKCE `code_verifier`
are each usable for one callback, a replayed or misdelivered callback finds no
cookie and is refused, and the recovery from every refusal is re-running sign-in
rather than reloading the callback URL. On every outcome other
than success the callback sets no session cookie and emits no `Set-Cookie` for
`__Host-podium_session` at all, so a session cookie the browser already holds is
neither replaced nor cleared. Sign-out is the only route that clears
`__Host-podium_session`, and it clears `__Host-podium_auth` as well so that a
sign-out mid-transaction leaves no cookie behind; the cookie table's clearing
column carries both. Which code each refusal returns is stated under "What
happens when it does not fire" below.

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
behavior. That parameter carries the enablement condition of the header-wins
precedence rule below, and that rule decides when the raw token is read from
`__Host-podium_session`. This site states no second version of it. Everything
after that read is unchanged: `verifier.Verify`, the §6.3.1 `IdpGroupMapping`,
the `ErrKeySetUnavailable` fail-closed-to-anonymous arm, and the `layer.Identity`
construction. The function's doc comment
(`internal/serverboot/identity_verify.go:193-200`) restates the shipped §6.3.3
rule as "A request carrying no token is anonymous and sees public visibility
only", which the second accepted location falsifies as worded, so C2 restates it
from the anonymity rule below in the same edit that adds the parameter. That one
function is installed as the server's identity verifier
(`internal/serverboot/serverboot.go:1136`, `pkg/registry/server/server.go:202`),
reused as the §7.3.1 layer endpoint's resolver (`:1198`, `:1207`), read by the
admin-gate closure (`:1208-1216`), and read by the tenant router (`:1170`), so
one edit reaches every consumer and there is no second resolution site.

**The expiry-signal rule.** This block is the single statement of what an
unverifiable session reports on each surface. Every other site cites it and
restates none of it.

The consumers differ in what they do with a verification error rather than in how
they resolve one, and the difference belongs to the consumer rather than to the
credential or to the deployment.

- The meta-tool identity middleware maps the error to a status and a §6.10 code
  (`pkg/registry/server/identity_verify.go:39-55`, `:87-94`). A token past its
  `exp` returns `401` `auth.token_expired`, and one that fails signature, `iss`,
  or `aud` returns `401` `auth.untrusted_token`. The routes it wraps are the
  paths the boot mux hands to the catch-all
  (`internal/serverboot/serverboot.go:1239`,
  `pkg/registry/server/server.go:429`), and the catalog reads the panel issues
  are among them.
- The §7.3.1 layer endpoints are mounted ahead of that catch-all
  (`internal/serverboot/serverboot.go:1220-1221`) and resolve the caller through
  `layerIdentityResolver`, which discards the error and returns the
  anonymous-public caller (`internal/serverboot/identity_verify.go:55-63`),
  because `WithIdentityResolver` carries no error channel
  (`pkg/registry/server/layers.go:187`). A write the C1 owner gate covers is
  refused with `403` `auth.forbidden`, and the layer read answers as the
  unfiltered-list rule under "The layer-ownership defect" states, so it reports
  nothing about the verification failure.
- The posture read resolves through that same `layerIdentity`
  (`internal/serverboot/serverboot.go:1198`), so it answers `200` and omits
  `subject`.
- While the JWKS is unreachable, verification returns `ErrKeySetUnavailable` and
  the shipped arm resolves anonymous with no error at all
  (`internal/serverboot/identity_verify.go:207-212`), so the meta-tool route
  answers anonymously as well and no surface reports expiry.

The rule carries no deployment qualifier, because every registry that can hold a
session cookie has the owner gate live: the C3 guard admits the browser flow only
on an `oidc-jwt` registry with public mode off, and the gate short-circuits only
on public mode or no identity provider
(`internal/serverboot/serverboot.go:1209-1215`). It also carries no credential
qualifier: the discard predates the browser flow and governs every credential the
layer endpoint accepts, so a gateway-forwarded header token past its `exp` reads
exactly the same way.

This proposal leaves `layerIdentityResolver` as it is. Surfacing the error there
would re-code the refusal a gateway-forwarded caller receives, which is a change
to a shipped surface this proposal does not otherwise touch.

Two consequences follow for the UI. The catalog read is the panel's expiry
signal. A write's `403` `auth.forbidden` carries no expiry information and is not
an ownership decision. G1 lands both in the design brief, whose session-expiry
transition names the transition without naming the signal
(`web/DESIGN.md:163-164`).

The rule adds no error code and re-scopes no envelope. `auth.token_expired` and
`auth.untrusted_token` already cover the case as §6.3.3 states them, and their
gateway-assuming text is restated under the credential-location rule, which
decides every site and names the step that owns it.

**The header-wins precedence rule.** This paragraph is the single statement of
how the registry chooses between the two accepted locations of the `oidc-jwt`
credential. Every other site in this proposal cites it by name and states only
what is local to that site. The registry reads the configured token header first,
and a bearer credential found there decides the request's identity. It reads
`__Host-podium_session` only when both of two conditions hold: the configured
token header carries no bearer credential, meaning the header is absent, its
value is empty, or its value does not begin with the `Bearer` prefix that §6.3.3
requires (`spec/06-mcp-server.md:96`), and the browser flow is enabled on that
registry. A registry with the browser flow disabled reads no cookie at all. The
two locations are never merged, and no request draws part of its
identity from each. A gateway that authenticated the request is the authority in
that deployment, and a registry-set cookie must not displace a gateway-forwarded
identity. The cookie carries the same credential the header carries, so both
locations verify through the same `OIDCVerifier` against the issuer JWKS for the
same `aud`, the resolved subject is JWKS-verified either way, and this rule adds
no verification step and no refusal. Where this precedence leaves a request with
no credential in either location, the anonymity rule below states what the
request resolves as.

**The anonymity rule.** This paragraph is the single statement of when a request
resolves anonymous under `oidc-jwt`. Every other site in this proposal cites it
by name and states only what is local to that site. Under `oidc-jwt` a request
whose configured token header carries no bearer credential is anonymous rather
than rejected, and it sees public visibility only (§4.6). An omitted header, a
header value without the case-insensitively matched `Bearer ` prefix, and a
`Bearer ` value that trims to empty are one condition and not three
(`internal/serverboot/identity_verify.go:181-191`). Where the browser flow is
enabled, such a request is anonymous only when it also presents no valid
`__Host-podium_session` cookie, meaning a cookie whose token verifies under
§6.3.3 against the issuer JWKS for the same `aud`; the configured token header is
still read first, per the header-wins precedence rule above. Where the flow is
disabled the sentence applies as written. The conjunct is required rather than
decorative, because the cookie branch is gated on the browser-flow enablement
field alone, so a registry that boots with the flow disabled reads no cookie and
a stale `__Host-podium_session` sent to it resolves anonymous. Enablement is the
only condition that branch carries. The §13.10 guard already requires `oidc-jwt`,
the web UI, and public mode off before the flow can be enabled
(`pkg/registry/server/config_validate.go:87`), so a `trusted-headers` or an
`injected-session-token` registry reads no cookie, and no site states the
provider and the enablement as two conditions. The separate `trusted-headers`
anonymity rule at `spec/06-mcp-server.md:108` is a different rule and is
unchanged. The conjunct is a necessary condition rather than a definition of
anonymity: a cookie that fails verification does not resolve anonymous on the
routes the meta-tool identity middleware wraps, where the middleware returns the
§6.10 code, and the expiry-signal rule above states what each consumer does with
that error. §6.3.3's fail-closed rule is untouched and applies in either accepted
location, so while the issuer JWKS is unreachable the request is anonymous
whether its token arrived in the header or in the cookie.

**Sign-out** clears every cookie whose row in the cookie table names sign-out as
its clearer, and there is nothing else to clear.

**Enablement, guard, and mount.** The browser flow's configuration keys, their
flag or environment forms, and their startup-read semantics are fixed by the
key-placement rule under "Where configuration keys go", and this section states
no second version of them. `PODIUM_WEB_UI_AUTH` is the key the guard and the
mount below both read.

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
`cmd/podium/login.go:35`).

**The device-code key.** `PODIUM_OAUTH_AUTHORIZATION_ENDPOINT` is the
device-authorization endpoint of the §6.3 `oauth-device-code` flow. It is a
consumer-side acquisition option, carried on that provider's `Options:` list
(`spec/06-mcp-server.md:42`) and read by `podium login` as the default for
`--issuer` (`cmd/podium/login.go:35`), by the MCP bridge
(`cmd/podium-mcp/main.go:277`), and by both SDKs
(`sdks/podium-py/podium/client.py:810`, `sdks/podium-ts/src/index.ts:895`). This
paragraph is the single statement of what the browser flow does with that key,
and every other site in this proposal cites the device-code-key rule by name and
states only what is local to that site.

The browser flow does not read it. The authorization endpoint the sign-in route
redirects to is `PODIUM_WEB_UI_OAUTH_AUTHORIZATION_ENDPOINT`, which is the only
key the flow reads for that purpose. `StartupConfig.Validate` does not accept the
device-code key in place of it: with `PODIUM_OAUTH_AUTHORIZATION_ENDPOINT` set
and `PODIUM_WEB_UI_OAUTH_AUTHORIZATION_ENDPOINT` empty, the
authorization-endpoint conjunct fails and startup fails with
`config.web_ui_auth_unconfigured` naming the key the flow needs, so an operator
who sets only the device-code key gets a startup refusal rather than a redirect
to nowhere. The rule therefore has a startup half and a runtime half, and the
testing plan pins one at each: the Guard case pins the refusal, and the Routes
case pins the endpoint the sign-in redirect carries.

**The startup guard.** The paragraphs below are the single statement of the
browser flow's startup guard. Every other site in this proposal cites this guard
by name and states only what is local to that site.

`StartupConfig.Validate` (`pkg/registry/server/config_validate.go:87`) requires,
when `PODIUM_WEB_UI_AUTH` is on, that `PODIUM_WEB_UI` is on, that
`PODIUM_IDENTITY_PROVIDER` is `oidc-jwt`, that public mode is off, that every
acquisition value is non-empty, and that `PODIUM_WEB_UI_REDIRECT_URI` satisfies
the redirect-URI conjunct below. Those conjuncts are the whole guard. A
configuration that enables the flow and fails one of them fails startup with
`config.web_ui_auth_unconfigured` naming the failed conjunct. A configuration
that enables no browser flow reaches no conjunct and starts, which includes the
shipped web-UI-only configuration, meaning `--web-ui` alone
(`cmd/podium/serve.go:38`, `internal/serverboot/serverboot.go:1826`).

`PODIUM_WEB_UI_AUTH` is a key of its own rather than a `PODIUM_IDENTITY_PROVIDER`
value, so the registry's accepted provider values stay as §13.12 records them
(`spec/13-deployment.md:468`), and no shipped guard covers the combination. The
public-mode exclusion is keyed on `PODIUM_IDENTITY_PROVIDER` alone
(`spec/13-deployment.md:484`, `pkg/registry/server/config_validate.go:88-91`) and
reads no web-UI key. The web-UI bind guard does read `PODIUM_WEB_UI` and
`PODIUM_WEB_UI_ALLOW_PUBLIC_BIND` and already requires a configured identity
provider, and it constrains the bind address rather than the flow's
configuration.

The acquisition values are the rows the key-placement rule marks as carrying a
variable and no flag, and §6.3.4 marks the same set required, additional to the
issuer and audience `oidc-jwt` already requires (`spec/06-mcp-server.md:106`).
The device-code key above states what the guard does with
`PODIUM_OAUTH_AUTHORIZATION_ENDPOINT`.

**The redirect-URI conjunct.** This paragraph is the single statement of the
secure-origin requirement. Every other site in this proposal cites it by name and
states only what is local to that site. `PODIUM_WEB_UI_REDIRECT_URI` is an
`https` URL or an `http` URL whose host is a loopback address, and a value that
is neither fails startup with `config.web_ui_auth_unconfigured` naming this
conjunct. The requirement follows from the cookie contract: every row of the
cookie table carries the `__Host-` prefix, the prefix forces `Secure`
unconditionally, including on the page-readable CSRF row, and a browser neither
stores nor returns a `Secure` cookie on a non-secure origin. A loopback `http`
address is admitted because a browser treats it as a secure context and stores
and returns a `Secure` cookie set there.

The conjunct constrains the registry's own browser-facing origin, which the
redirect URI names because the callback is a registry route. An `https` redirect
URI is satisfied whether the registry is reached over TLS directly or through a
gateway that terminates TLS and forwards plain HTTP to the registry's listener
(`spec/06-mcp-server.md:112`), so the gateway-fronted deployment §13.10 describes
passes the conjunct on a non-loopback plain-HTTP bind. The guard reads the
configured value rather than the deployment property because the registry cannot
observe its own browser-facing scheme: it builds a plain `http.Server` and calls
`ListenAndServe` (`internal/serverboot/serverboot.go:1444`), the `Host` header
carries no scheme, and nothing in the tree reads `X-Forwarded-Proto`. The
redirect URI is the one browser-facing origin startup configuration carries.

Nothing already in the tree implies the conjunct. The shipped `oidc-jwt` issuer
guard (`internal/serverboot/identity_verify.go:268`) constrains
`PODIUM_OAUTH_ISSUER`, which is the IdP's discovery URL rather than the origin
the browser reaches, and it admits a loopback issuer. §13.10's web-UI bind guard
admits a non-loopback bind once `--web-ui-allow-public-bind` and an identity
provider are set (`spec/13-deployment.md:172`), and §6.3.3 blesses a directly
reachable `oidc-jwt` registry (`spec/06-mcp-server.md:106`), which is a registry
serving plain HTTP.

Absent the conjunct, a deployment configured with a plain `http` non-loopback
redirect URI would set the pre-authorization cookie, receive no cookie back on
the callback, and refuse every sign-in with `403` `auth.csrf_invalid` and no
diagnosis.

**The guard's ordering.** This paragraph is the single statement of how the
browser-flow guard orders against the shipped public-mode exclusion, and every
other site cites it instead of restating it. The shipped exclusion is the first
check `Validate` runs (`pkg/registry/server/config_validate.go:87-90`), and the
browser-flow guard runs after it rather than ahead of it, so the shipped check's
predicate, its error, and its message are unchanged and a registry configured for
public mode with `oidc-jwt` keeps failing with `config.public_mode_with_idp` as
it does today. The browser-flow guard therefore reaches its public-mode conjunct
only in a configuration where no identity provider is configured, and in that
configuration the `oidc-jwt` conjunct is the one that fails and the one the error
names. No configuration reaches `config.web_ui_auth_unconfigured` naming the
public-mode conjunct. The guard carries the public-mode conjunct even so, because
it states the full set of conditions the flow requires and the shipped exclusion
is keyed on `PODIUM_IDENTITY_PROVIDER` alone (`spec/13-deployment.md:484`), so
that exclusion does not fire on the new enablement key.

**The mount is stated here and nowhere else.** The authentication routes are
registered inside the existing `if cfg.webUI` block that already mounts `/ui/`
(`internal/serverboot/serverboot.go:1229`), under one nested check on the
enablement field C2 adds. The enclosing block supplies the web-UI conjunct, so
the mount adds exactly one condition, and no other conjunct of the guard is
restated there: `validate()` runs before the wiring, and no booted process can
falsify the `oidc-jwt`, public-mode, acquisition-value, or redirect-URI
conjuncts. Making the web UI a conjunct of the guard rather than a second
enablement axis is what collapses the mount onto that one field: a registry that
enables the flow without the UI does not boot, so "browser flow on, web UI off"
is a startup refusal rather than an unregistered route, and no configuration
leaves the routes absent for a reason the guard has not already stated. The
predicate reads startup configuration alone, set once at boot from the flags and
`PODIUM_*` variables and never changed at runtime, so a registry that boots
without the flow never acquires the routes and one that boots with it never loses
them. The posture read `GET /v1/ui/session` is registered in the same block
beside `/ui/` but outside the nested check, gated on the web UI alone, because a
registry serving the UI with no browser flow is the deployment whose page has to
learn not to offer sign-in. Every path this predicate registers is mounted from
the boot mux, and the handlers' package home is the choice recorded under
"IMPLEMENTOR'S CHOICE (package home)". A path the predicate leaves unregistered
falls through to the catch-all and is answered as any unregistered path is
answered on that deployment; "The status an unregistered path receives" states
the rule that fixes which stage answers and what the status is.

**The posture read.** `GET /v1/ui/session` reports the deployment's identity
posture and the caller's own resolved subject. This paragraph is the single
statement of what the read requires, what it discloses, and what it adds. Every
other site in this proposal cites it by name and states only what is local to
that site.

The read requires no credential and refuses no request for lack of one. A request
that does carry one has it verified only so the response can report `subject`,
through the same `layerIdentity` resolver the layer endpoint uses
(`internal/serverboot/serverboot.go:1198`).

The read carries no privilege. It discloses the deployment's identity
configuration and the requesting caller's own subject, and it discloses no
artifact, layer, tenant, or other caller's data, so the UI gains no privileged
access through it. Every other call the UI makes is the call an SDK would make
against the same endpoint, which is §13.10's own account of the UI: it "talks to
the registry's HTTP API as any other consumer would"
(`spec/13-deployment.md:163`).

The read adds no surface beyond itself. It adds no credential, cookie, error
code, SPI method, or store method, and it changes no existing endpoint. Its
handler reads no store and writes none, so it sits outside the §13.2.1 write set
under the read-only classification below. It is mounted on the web UI alone
rather than on the browser flow, and "Where it mounts" below states that predicate and
what a registry serving no UI answers instead.

The read exists because the browser can observe neither of the two things it
reports. The session cookie is `HttpOnly`, so the page cannot tell whether it
holds one; `GET /v1/layers` reports neither the caller nor any scoping under the
unfiltered-list rule, so it separates no posture
(`pkg/registry/server/layers.go:770-777`); the catalog responses carry no caller
identity; `/healthz` reports the §13.2.1 mode banner alone
(`pkg/registry/server/server.go:656-673`); and `/v1/quota` reports tenant,
limits, and usage alone (`pkg/registry/server/quota.go:9-26`). Without the read
the UI has no source for whether to offer sign-in, and each rendering rule G1
states would key on server configuration the browser cannot see.

- **The body.** This bullet is the single statement of the response body. Every
  other site in this proposal cites it by name and states only what is local to
  that site, and states no field, condition, or value the statement here does not
  carry. The body reports the deployment's posture and the caller's own resolved
  subject, and it reports nothing about any other caller.
  `identity_provider_configured` and `public_mode` are booleans:
  `identity_provider_configured` reports whether an identity provider is
  configured and never names which one, and `public_mode` reports whether public
  mode is engaged. `browser_auth` is an object carrying `enabled`, which reports
  whether the browser flow is enabled on this deployment, and, when `enabled` is
  true, `sign_in_path` and `sign_out_path`, which are the paths the mux
  registers, so no authentication route path is spelled inside the bundle. When
  `enabled` is false both path fields are absent, because the flow's routes are
  not registered on that deployment and there is no registered path to report.
  `subject` is the verified subject of the request that asked, present only when
  one resolves and absent otherwise; the bullet below states which requests
  resolve one. The response carries no other field, and in particular no issuer,
  client identifier, endpoint, or other configuration value. These field names
  are the vocabulary the posture-keyed rendering rules are written in, which G1
  states.
- **The state it reads, and where that state is set.**
  `identity_provider_configured` and `public_mode` read the shipped
  `identityProvider` and `publicMode` fields, and `browser_auth` reads the
  enablement field C2 adds. Each is set once at boot from
  the flags and `PODIUM_*` variables (`internal/serverboot/serverboot.go:1826`)
  and never changed at runtime, and the browser-flow fields are the ones the C3
  guard validates before the wiring runs. `subject` reads the per-request
  identity that `layerIdentity` resolves
  (`internal/serverboot/serverboot.go:1198`), which is the resolver the layer
  endpoint already uses, so what this read reports for an unverifiable session is
  the arm the expiry-signal rule above gives it. Nothing sets or clears state on
  this path: the handler reads no store and writes none, which is what places it
  outside the §13.2.1 write set under the read-only classification below.
- **Where it mounts.** Per the mount predicate stated under "Enablement, guard,
  and mount", which registers it beside `/ui/` on the web UI alone. A registry
  started without `--web-ui` serves no UI, and the path is never registered, so a
  request for it is answered as any path the registry does not register is
  answered on that deployment, per "The status an unregistered path receives"
  below. The S45 stack configures no identity provider, so on that stack the read
  answers `404`.
- **Its callers.** The UI, on load. No CLI, SDK, or MCP caller reads it.
- **What happens when it does not fire.** When the read fails or answers `404`,
  the UI renders its anonymous presentation: no sign-in control, no sign-out
  control, and the layer panel rendered with its write operations, where a
  refused write returns `403` `auth.forbidden` and the panel presents the
  not-permitted state. The Surfaces case under U1 drives that, and the
  end-to-end case under C2 asserts the body on a registry with the flow disabled
  and on one with it enabled.

**The no-session-state rule.** Every other site in this proposal cites this rule
by name and states only what is local to it. The registry keeps no session state,
which is the following conjuncts.

- The registry mints no credential that authenticates a request.
  `__Host-podium_session` carries the access token the IdP issued, which is the
  credential §6.3.3 already accepts, so the browser flow adds no credential to
  that section. The values the registry does mint are the `state`, the `nonce`,
  and the PKCE `code_verifier` the sign-in route puts in `__Host-podium_auth`,
  and the request-side CSRF value the callback sets where the chosen mechanism
  carries one. None of them authenticates any request: each is compared against
  something the IdP returns, or is echoed back by the page that read it.
- The registry keeps no session record, mints no session identifier, and holds no
  session key. The `__Host-` prefix is the browser-enforced binding control the
  cookie table names, which is why no cookie in this flow needs a server-side
  signing key.
- The registry chooses no lifetime for the session. The token's own `exp`, set by
  the IdP, bounds it server-side, and the session row of the cookie table carries
  no `Max-Age` for that reason. The one lifetime the registry chooses is the
  pre-authorization transaction TTL, which bounds the sign-in window and holds no
  session.
- No state is shared across replicas. §13.1's reference topology is a "Stateless
  front-end: 3+ replicas behind a load balancer" (`spec/13-deployment.md:5`). A
  store-backed pre-authorization transaction would force the sign-in and the
  callback onto the same replica or onto a shared write. The cookie travels with
  the browser, so any replica serves the callback.
- No authentication route and no posture read mutates registry state. What each
  of them does instead, and how §13.2.1 classifies it, is the read-only
  classification below.
- This proposal adds no persistence surface. It adds no `store.Store` method
  (`pkg/store/store.go:345`), no table in `Memory`, `Postgres`, or `SQLite`, no
  `additiveColumns` row (`pkg/store/schema_migrate.go:47`), no
  `pkg/store/storetest` conformance case, no retention sweep, no §9.1
  `RegistryStore` row, and no §13.1 topology component.

The rule is a requirement of the reference topology and also the outcome of
costing the alternative. The only thing a stored session record would buy is
revocation before `exp`, which "Revocation is expiry" below states the registry
offers for no credential it accepts. Adding a record for the browser credential
alone would make it the one revocable credential the registry serves, with no
spec basis, at the cost of every item in the last conjunct above and a §13.2.1
read-only classification for whichever routes wrote it.

**The read-only classification.** This paragraph is the single statement of how
§13.2.1 classifies the surfaces this proposal adds. Every other site in this
proposal cites it by name and states only what is local to that site.

Sign-in, the callback, sign-out, and the posture read mutate no registry state.
Sign-in sets the pre-authorization cookie, the callback exchanges the code with
the IdP and sets the cookies the cookie table above names it as setting, sign-out
clears the cookies that table names it as clearing, and the posture read answers
from configuration fields set at boot and from the per-request resolved identity.
None of them reads registry state either. §13.2.1's rule keys on mutation
(`spec/13-deployment.md:41`), so it is the absence of mutation that places each
of them outside the write set. Nothing here joins that set, and the section's
named examples, meaning ingest webhooks, layer admin operations, freeze toggles,
admin grants, and tenant management, stay as they stand.

That rule is per-endpoint and per-mutation, and §13.2.1 says each endpoint's own
section states its classification. §7's entry therefore carries one
classification covering sign-in, the callback, sign-out, and the posture read
together, rather than one for the authentication routes and a second for the
posture read. This is an application of the section's existing rule, so §13.2.1's
own text gains nothing, no carve-out is written, and the §6.3.1 SCIM-receiver
precedent is not invoked.

The posture read is not an authentication route. It mounts on the web UI alone
rather than on the browser flow, so a site whose subject is the authentication
routes covers sign-in, the callback, and sign-out, and a site whose subject
reaches the posture read names it as well.

A read-only registry serves each of these surfaces exactly as it serves it
otherwise, on every deployment that mounts it, and none of them returns
`registry.read_only`. A Postgres primary outage therefore causes no
authentication outage: an operator signs in, signs out, and keeps reading the
catalog on an established session while the registry serves from the replica. The
Read-only case under C2 pins this.

**Revocation is expiry.** This paragraph is the single statement of the session's
revocation and renewal model. Every other site in this proposal cites it by name
and states only what is local to that site.

The browser flow offers no revocation before the token's own `exp`. Sign-out
clears the cookies whose rows in the cookie table name it as their clearer, so
the browser stops presenting the credential, and the token stays valid at the IdP
until it expires. That is the model §6.3.3 already states for the credential the
registry verifies: the verification paragraph checks the signature, `iss`, `aud`,
and the `exp`/`nbf` window and consults no revocation list
(`spec/06-mcp-server.md:98`), and `OIDCVerifier.Verify` implements those checks
and no others (`pkg/identity/oidc_jwt.go:183`). The registry consults no
revocation list for that token in either accepted location under the
credential-location rule, and it gains none here.

Both token-carrying providers the registry accepts, `oidc-jwt` and
`injected-session-token`, are valid until they expire. The third verified
provider, `trusted-headers` (`internal/serverboot/identity_verify.go:89`),
carries no token and no `exp`, because the gateway withdraws identity by ceasing
to inject the identity headers. A stored session record would therefore make the
browser credential revocable before `exp` while every other credential the
registry accepts stays valid until it expires, and the spec gives no basis for
that difference.

There is no silent refresh. An expired session re-runs the sign-in redirect,
which completes without a prompt while the IdP session is live.

**What happens when it does not fire.** With the flow disabled the authentication
routes are never registered, so a request for one of their paths reaches the
catch-all
(`internal/serverboot/serverboot.go:1239`) and is answered by the meta-tool
handler rather than by an authentication route, and `oidcJWTVerifier` ignores
the cookie, so a stale cookie resolves anonymous-public rather than
authenticating anyone.

**The status an unregistered path receives.** This paragraph is the single
statement of the status a request for a path the registry does not register
receives, and every other site in this proposal cites it by name and states only
what is local to that site. The status is the deployment's rather than the
route's.

The request passes two stages in order, and the first stage that answers fixes
the status. Identity verification is the first stage: the meta-tool identity
middleware runs ahead of the inner mux (`pkg/registry/server/server.go:429`,
`pkg/registry/server/identity_verify.go:44-51`) and exempts only `/healthz`,
`/readyz`, and paths under `/scim/` (`:73-80`), so on every other path, including
the authentication routes, `/ui/`, and the posture read, verification runs before
route matching. Route matching is the second stage, and a path the mux does not
register falls through to the catch-all
(`internal/serverboot/serverboot.go:1239`).

Verification answers a request when a verifier is installed, the path is not
exempt, and the configured verifier refuses that request. Where it does, the
middleware answers before any path is matched, and the status is the §6.10
refusal the verification failure maps to
(`pkg/registry/server/identity_verify.go:118-119`). In every other combination
verification does not answer, route matching does, and the status is the one that
deployment returns for any path it does not register. The shipped catch-all
answers `404`, and the mount-predicate and posture-read cases under Testing
assert that status on the stacks they run. A reader derives the disposition of
any configuration from the two facts the rule ranges over, which are whether a
verifier is installed and which requests it refuses. Both are closed.

A booted registry either installs a request-time verifier for one of the
providers this build verifies, which are `injected-session-token`, `oidc-jwt`,
and `trusted-headers` (`internal/serverboot/identity_verify.go:89`), or installs
none, in which case the middleware is a pass-through and nothing can refuse ahead
of route matching (`pkg/registry/server/identity_verify.go:40-41`). No third
state boots. A registry that selects a provider carrying no verifier is refused
at startup by `identityVisibilityGuard`
(`internal/serverboot/identity_verify.go:99-104`), and public mode alongside any
selected provider is refused by `config.public_mode_with_idp`
(`pkg/registry/server/config_validate.go:16`), so the no-verifier value covers
exactly a registry running the standalone default, one naming a label the
identity registry does not carry, and one running in public mode.

Each installed verifier refuses the set of requests its §6.3 contract fixes, and
resolves a caller, authenticated or anonymous, for every request outside that
set. `injected-session-token` requires a verifiable runtime-signed token on every
verified path, so it refuses every request that does not carry one
(`internal/serverboot/identity_verify.go:27-29`,
`pkg/identity/runtime.go:137-138`), and a request carrying none is refused with
`401` `auth.untrusted_runtime`. `oidc-jwt` treats the forwarded token as optional
(`internal/serverboot/identity_verify.go:204-205`) and fails closed to the
anonymous caller while the issuer key set is unavailable, which is §6.3.3's
disposition for that condition rather than a rejection (`:209-213`), so it
refuses exactly a request that presents a token which fails verification while
the key set is available, and a request carrying an invalid bearer token is
refused with `401` `auth.untrusted_token`. `trusted-headers` returns no error for
any request (`internal/serverboot/identity_verify.go:234-236`), so its refusal
set is empty. A provider that gains a request-time verifier in `serverboot` joins
the list above in the same change and carries the rule once its refusal set is
stated.

The §13.10 guard requires `oidc-jwt` for the browser flow, so every
`trusted-headers` and every `injected-session-token` registry is one whose
authentication routes are unregistered. The unmounted route is unreachable under
every disposition the rule produces, because verification answers before route
matching and route matching finds no route, so the mount predicate holds
whichever disposition applies. Every site in this proposal that asserts a status
on a path the registry does not register names the stack it runs on and the
identity configuration of that stack.

With the flow enabled, the callback's own refusals are the following, and each
sets and clears the cookies "The callback order and outcomes" states. A callback
whose `__Host-podium_auth` cookie is absent, expired, or does not match the
returned `state`, and a callback whose exchanged ID token carries a `nonce` other
than the one that cookie holds, are each refused with `403` `auth.csrf_invalid`
in the scope "The CSRF position" states. The `nonce`
comparison is a separate check from the `state` comparison: `state` binds the
callback to the browser that started the transaction, and `nonce` binds the ID
token to that same transaction.

A callback whose `__Host-podium_auth` cookie and `state` validate but whose query
carries the IdP's `error` parameter rather than a `code`, which is what the
authorization endpoint returns when the user declines the consent prompt or the
IdP refuses the authorization request, runs no exchange and returns the browser
to the web UI root at `/ui/` without establishing or replacing a session. A
cancelled first sign-in therefore lands at `/ui/` anonymous, and a cancelled
re-sign-in lands there still signed in under the earlier session that "The
callback order and outcomes" leaves intact. Re-running sign-in is the recovery
for that condition, so it takes no error code and in particular not
`auth.exchange_failed`, whose `retryable: false` envelope and client-credential
remediation would report a user decision as an operator misconfiguration and
would emit a `5xx` on the most common outcome of a sign-in attempt. The `state`
comparison runs first, so an error redirect that carries no matching
pre-authorization cookie is refused with `403` `auth.csrf_invalid` like any other
callback that fails that comparison.

A session cookie the verifier refuses, whether for `exp`, for signature, `iss`,
or `aud`, or because the key set is unavailable, is answered on every surface by
the expiry-signal rule under "The browser session", and this section adds
nothing to it.

**The exchange-failure rule.** This paragraph is the single statement of how the
callback's code exchange fails and which code each failure returns. Every other
site in this proposal cites this rule by name and states only what is local to
it. The discriminator is whether the IdP refused the exchange at the OAuth
protocol level. An IdP the registry cannot reach for the code exchange, and one
whose token endpoint answers with a `5xx`, are both transient failures against a
dependency the registry called, so each is refused with `500`
`registry.unavailable`, whose shipped scope and `retryable: true` envelope cover
exactly that case (`pkg/registry/server/error_envelope.go:26`,
`docs/reference/error-codes.md:158`), and neither re-scopes that entry. An IdP
the registry reaches, whose token endpoint answers the exchange and refuses it
with an OAuth error such as `invalid_grant`, or refuses it because
`PODIUM_WEB_UI_OAUTH_CLIENT_SECRET` is wrong, is a permanent failure for that
request, so `registry.unavailable` is not available to it: every retry fails
identically and the `retryable: true` envelope names no useful action. It is
refused with `502` `auth.exchange_failed`, which S7 stages as a new non-retryable
code whose envelope carries `retryable: false` and a `suggested_action` naming
the client credential and the registered redirect URI as what an operator checks.
A callback whose query carries the IdP's `error` parameter rather than a `code`
runs no exchange and reaches neither arm, so it takes no error code, as the
declined-consent outcome above states. What the callback sets and clears on each
of these outcomes is stated under "The callback order and outcomes" above. No
error code is added beyond `auth.csrf_invalid` and `auth.exchange_failed`, and no
shipped envelope entry is re-scoped.

**What the mechanism does not change.** The credential is unchanged, so §6.3.3's
anonymous-while-JWKS-unreachable rule keeps applying. The rule immediately below
states what the mechanism does to tenant derivation, and the one after it states
what it does to `auth.token_expired` and `auth.untrusted_token`. What moves is
text: every shipped sentence that says this credential arrives forwarded by a
gateway is false for one of its two accepted locations, or names a remediation a
browser cannot perform. The credential-location rule below is the single
statement of which text that is.

**The tenant-derivation rule.** This paragraph is the single statement of what
the browser flow does to tenant derivation, and every other site in this proposal
cites it by name rather than restating any part of it. The flow adds no
credential. A `__Host-podium_session` cookie carries the same IdP-signed token
the configured token header would carry, and the registry verifies it against the
same issuer JWKS for the same audience, so the token's claims are identical in
either accepted location. Derivation reads the token's claims rather than the
request's transport, so the accepted location does not enter it: §6.3.1
per-request tenant selection (`spec/06-mcp-server.md:64`) keeps selecting a
request's tenant from the verified `org_id` claim, and the sentences that restate
it, `spec/06-mcp-server.md:94` and the derivation clause at `:98`, keep describing
what the registry does. Every site that states how the registry derives a
request's tenant therefore stands unedited. Widening such a site is a defect of
the applied change, and a more serious one than widening any other standing site,
because the tenant selector bounds §4.7 isolation. `auth.tenant_unknown` keeps
its scope and keeps populating `details.token_org_id`, because a token that
arrives in the cookie and whose `org_id` resolves to no provisioned tenant is the
failure the shipped entry already describes (`spec/06-mcp-server.md:378`,
`docs/reference/error-codes.md:58`), neither of which names the credential's
location.

**The unchanged-scope statement.** This paragraph is the single statement of what
the browser session does to `auth.token_expired` and `auth.untrusted_token`.
Every other site in this proposal cites it by name and states only what is local
to it. Both codes are authentication failures on the same credential, so both
keep their scopes, neither §6.10 entry is re-scoped, no shipped envelope entry is
re-scoped (`pkg/registry/server/error_envelope.go:67-72`), and no error code is
added beyond `auth.csrf_invalid` and `auth.exchange_failed`. A browser session
presents an `oidc-jwt` token, and §6.10 already scopes `auth.token_expired`
(`spec/06-mcp-server.md:355-364`) to an expired token and `auth.untrusted_token`
(`:366-376`) to one that fails signature, `iss`, or `aud` verification, which is
the verification §6.3.3 specifies and the §6.9 failure-mode table restates at
`:329`. The cookie reaches both codes through the shipped verifier path rather
than a new one: `oidcJWTVerifier` calls `verifier.Verify` and returns the error
(`internal/serverboot/identity_verify.go:201-215`), and `writeIdentityError` maps
that error to the two codes (`pkg/registry/server/identity_verify.go:88-100`).
That path is also why the change inventory reaches `pkg/identity`, which declares
the error the amended `:366` defines. What changes on the two entries is their
text, because a session cookie carries a token the registry itself obtained
through the §6.3.4 exchange rather than one a gateway forwarded, and a directly
reachable `oidc-jwt` registry has no gateway at all. The moved text is, for each
code, whichever of its scope sentence, its §6.9 row, its `suggested_action`, and
its canonical `message` the credential-location rule reaches;
`auth.token_expired`'s canonical `message` (`spec/06-mcp-server.md:360`) is
provider-neutral and stands. The credential-location rule decides every one of
those sites and names the step that owns it, and S7 stages the `spec/` text it
moves for these two codes.

**The credential-location rule.** This paragraph is the single statement of which
shipped text the second accepted credential location moves and which text stands.
Every other site in this proposal cites it by name and states only what is local
to that site.

A site moves when a registry running the browser flow receives a request whose
token arrives in `__Host-podium_session` and the site's text is then false, is
narrowed to less than what the registry accepts, or names an action a browser
cannot take. The categories that produce that condition are a site stating what
the registry accepts as a credential, how a caller obtains that credential, what
a request carrying no bearer credential in the configured token header resolves
to, or that a client sends no credential of its own. A site stating the scope or
the emitted text of an `auth.*` error code moves as well, even where the claims
it names are verified identically in either location, because the sentence
describes the credential by the single location it was written about while the
code covers both. The codes keep their scopes: the verifier path, the failures it
reports, and the §6.10 entries for `auth.token_expired` and
`auth.untrusted_token` are unchanged, and what moves is the scope sentence, the
remediation, and the emitted message. `spec/06-mcp-server.md:386` and its mirror
`pkg/registry/server/error_envelope.go:74` are the single named exception to that
clause, for the reason recorded below, and the rule names no other.

A site stands when its own words already limit it to the configured token header
or to a gateway-fronted deployment, because the browser flow removes neither and
the header-wins precedence rule keeps the gateway account true where the text is
labelled as such. A site that presents that arrangement as the only one a
provider serves is not so limited and moves. A site stands when it describes how
one provider's own mechanism works. A site stands when it documents the
verification configuration, meaning the issuer key, its code mirror, and the
audience startup guard, because the registry verifies the same `iss` and `aud`
claims on the token in either accepted location. A site stands when it states how
the registry derives a request's tenant, for the reason the tenant-derivation
rule above gives.

The rule reaches prose, Go comments and doc comments, and emitted strings alike,
across the corpus the command below searches. A restatement narrows the falsified
claim to the location it was written about rather than deleting it, keeps the two
existing credentials' behavior unchanged, and leaves every standing site
untouched. Widening a standing site is a defect, because it asserts a change this
proposal does not make. The rule produces no inventory of affected sites and this
document fixes no moved set. The moved set is every hit of the command below to
which the rule applies, dispositioned at implementation time, and the applied
change is complete when re-running the command and applying the rule leaves no
hit undispositioned.

Sites are worked through below, one for each restatement pattern the rule
produces, so a disposition can be checked against an example. They illustrate the
rule rather than bound it.

| Site | What it says today | Staged by |
|:--|:--|:--|
| `spec/06-mcp-server.md:92` (the opening clause and the sends-no-credential sentence) | This row is the single statement of that line's disposition and of the set that moves with it. Every other site names this row and adds only what is local to it. The line opens by calling both providers "registry-process identity providers for a deployment that runs the registry behind a gateway that has already authenticated the caller", which is false for a directly reachable `oidc-jwt` registry running the browser flow. The clause is restated per provider: `oidc-jwt` is a registry-process provider for a deployment where the registry verifies the caller's token itself, whether a gateway forwarded it or the registry obtained it through the §6.3.4 exchange, and `trusted-headers` alone keeps the fronting-gateway requirement. The split follows the shipped text, which already separates the two predicates: `:108` makes the gateway the source of truth for `trusted-headers` and `:112` records that `oidc-jwt` "verifies every token regardless of the network path, carries no bind restriction". Dropping the gateway predicate from both providers would assert a change to `trusted-headers` this proposal does not make. The same line closes "A Podium client behind such a gateway sends no credential of its own", and the restatement leaves "such a gateway" without an `oidc-jwt` antecedent, so that sentence is scoped in the same edit to a client behind a gateway under either provider. The clause's mirrors move with it in the applied change, each under the step that owns its tree: the §2.2 component-map bullet (`spec/02-architecture.md:101`) under S3 with `:92` itself, the `pkg/identity/registry.go:69-70` comment under C2, `docs/deployment/gateway-delegated-identity.md:9` and `:11` under D1, where `:9` carries the gateway-fronted framing and `:11` the sends-no-credential sentence whose antecedent `:9` sets, and the `test/manual-validation.md:2482-2484` preamble under T1. Landing one without the rest leaves the applied tree describing `oidc-jwt` as gateway-scoped at the site left behind, which is the defect the co-movement exists to prevent. This row's edit is scoped to `:92` and does not reach the adjacent `:94`, which stands per the tenant-derivation rule | S3, with C2, D1, and T1 |
| `spec/06-mcp-server.md:366` | `auth.untrusted_token`'s scope: "a forwarded `oidc-jwt` token". The sentence states the code's scope rather than one provider's verification path, so it moves even though the claims it names are verified identically in either location | S7 |
| `docs/deployment/integrations.md:85` | a closed acquisition enumeration for the directly reachable arrangement the browser flow runs in: "Callers obtain that token by completing the CLI's device-code flow". Restated so a CLI, an SDK, or another API client obtains the token through the device-code flow and, on a registry that enables the browser flow, a browser obtains it through the §6.3.4 exchange, which the registry returns in `__Host-podium_session` | D1 |
| `docs/deployment/progressive-adoption.md:57` | the no-token-is-anonymous rule, scoped to the provider: "Under `oidc-jwt` a request carrying no token is anonymous rather than rejected, so it resolves to public visibility only". It is narrowed to the anonymity rule above, rendered in the page's voice, which scopes the predicate to the configured token header and adds the browser-flow conjunct while keeping the visibility clause. The row's local point is that the narrowing leaves the surrounding exit criterion true: an unauthenticated caller still sees an empty catalog once no layer is public | D1 |

**IMPLEMENTOR'S CHOICE:** which sites the sweep moves. Any answer is the set the
credential-location rule above selects from the hits of the command below. The
rule states the test, the categories that stand, the defect a widened site
introduces, and the criterion for a complete application, and this blank adds
nothing to it.

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
not left without one. The entry's scope and its `details.token_org_id` field are
covered by the tenant-derivation rule, so §6.10's `auth.tenant_unknown` and this
string are both untouched.

**IMPLEMENTOR'S CHOICE (package home):** none of the above. The package home is
`pkg/registry/server`, alongside the layer endpoint it resembles. Where and under
what condition the boot mux registers the handlers is the mount predicate stated
under "The browser session". The route paths remain the choice recorded
under "The edit sites".

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

**The key-placement rule.** The table below is the single statement of which
browser-flow configuration key carries a `podium serve` flag and which is
environment-only, and every other site cites it rather than restating any part of
it.

| Key | Forms | What it carries |
|:--|:--|:--|
| `--web-ui-auth` / `PODIUM_WEB_UI_AUTH` | flag and variable | one boolean, which enables the browser flow |
| `--web-ui-auth-transaction-ttl` / `PODIUM_WEB_UI_AUTH_TRANSACTION_TTL` | flag and variable | the sign-in window, carried as `__Host-podium_auth`'s `Max-Age` by the cookie table under "The browser session" |
| `PODIUM_WEB_UI_OAUTH_CLIENT_ID` | variable | the OAuth client identifier the sign-in redirect sends |
| `PODIUM_WEB_UI_OAUTH_CLIENT_SECRET` | variable | the client credential the callback's token request sends |
| `PODIUM_WEB_UI_REDIRECT_URI` | variable | the callback URL the IdP returns the browser to |
| `PODIUM_WEB_UI_OAUTH_AUTHORIZATION_ENDPOINT` | variable | the IdP endpoint the sign-in route redirects to |
| `PODIUM_WEB_UI_OAUTH_TOKEN_ENDPOINT` | variable | the IdP endpoint the callback exchanges the code at |

The rows carrying a variable and no flag are the acquisition values, and a site
that names the acquisition set names those rows.

The enablement boolean and the transaction TTL carry both forms, following
`PODIUM_WEB_UI` (`cmd/podium/serve.go:38-39`,
`internal/serverboot/serverboot.go:1826-1827`). The acquisition values carry a
variable and no flag, following `PODIUM_TRUSTED_PROXY_SECRET`: one of them is a
client credential, and a credential passed on the command line is readable from
the process table, so the whole acquisition set is kept off it rather than split
by sensitivity.

A flag and its variable are one value read once. `podium serve` writes a flag it
parsed into the matching variable before the boot read
(`cmd/podium/serve.go:65-72`), and `LoadConfig` reads the variable beside
`internal/serverboot/serverboot.go:1826-1827`. A flag that is set therefore
overrides the variable, and a flag that is not set leaves the variable's value,
which is the precedence `docs/reference/cli.md:139` states for every shipped
`podium serve` flag. Every key in the table is startup configuration, read at boot
and never changed at runtime, so a guard or a mount predicate that reads one reads
a value the process cannot change afterwards.

The documentation surfaces follow from the table. The `docs/reference/cli.md`
synopsis and flag table carry the enablement boolean and the transaction TTL
alone, the `docs/reference/cli.md` environment-variable table carries the
acquisition values, and the §13.10 key list documents every key in the table,
flagged or not.

### The second-location sweep

The registry today accepts two credentials, and §6.3.3 and its mirrors state
that in prose rather than in one enumeration. This proposal adds no third
credential. It adds a second accepted location for the `oidc-jwt` credential, so
every sentence written on the assumption that the credential always arrives
forwarded by a gateway in the configured header is falsified or narrowed.
Successive review rounds each found one such site, one round at a time, because
the disposition was argued per site rather than decided by a rule.

**The rule.** The credential-location rule under "The browser session" decides
every site, and this section states no version of it. The reproducing command and
the worked examples sit beside the rule there. The reason no list of affected
sites appears here is local history: every round of review that read such a list
found one more site the list had missed or misplaced, while the rule and the
command stood unchallenged.

**IMPLEMENTOR'S CHOICE:** the wording each affected site takes. The
credential-location rule under "The browser session" states what any wording
holds to.

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
  configuration guard, stated beside the bind-guard sentence. The staged sentence
  states the guard as the startup guard under "The browser session" states it,
  carrying each conjunct and the `config.web_ui_auth_unconfigured` refusal that
  names the failed conjunct, in spec voice and with no clause added or dropped.
  §13.10 also states the guard's ordering against the shipped public-mode
  exclusion, and against which conjunct a public-mode configuration fails, in the
  terms fixed under "The browser session". This bullet adds nothing to
  that statement.
  What is local to this site is the placement and §6.3.4's half of it: §6.3.4's
  `Options:` list names the same acquisition set the guard requires, and is the
  spec home of the device-code-key rule under "The browser session", stating both
  of its halves, that the browser flow does not read
  `PODIUM_OAUTH_AUTHORIZATION_ENDPOINT` (`spec/06-mcp-server.md:42`) and that the
  guard does not accept it in place of
  `PODIUM_WEB_UI_OAUTH_AUTHORIZATION_ENDPOINT`, so the two sections carry one
  list between them.
  §13.10 requires that redirect URI to be an `https` URL or a loopback `http`
  URL, per the redirect-URI conjunct under "The browser session", which
  is the single statement of the rule and of its reason. §13.10 states the rule
  and the reason in its own prose, because spec text cites no proposal, and it
  states them beside the bind-guard sentence together with the bind-guard
  non-implication that paragraph gives, because a reader of §13.10 meets the bind
  guard first.
  The web-UI conjunct is carried in this guard because the mount predicate stated
  under "The browser session" depends on it. The guard's code mirror is
  `StartupConfig.Validate` (`pkg/registry/server/config_validate.go:87`), which
  C3 extends with the enablement and acquisition fields alongside the existing
  `WebUI` and `WebUIAllowPublicBind` fields (`:67-72`).
- **§6.3, a new §6.3.4** stating the browser acquisition flow, placed after
  §6.3.3, which ends at `spec/06-mcp-server.md:114` immediately before §6.4 at
  `:116`, with a pointer from the §6.3 introduction at `:40`. It is not a fourth
  sub-bullet under the `oauth-device-code` bullet's list (`:44-47`), which is
  scoped to the device-code flow. §6.3.4 is also the spec home of the gate
  predicate "The CSRF position" states. It carries that predicate in full and in
  the same terms: what counts as state-changing, what counts as cross-site
  browser-origin evidence, the refusal and its code, the omitted scheme term and
  its reason, the evidence scoping rather than credential scoping, the admitted
  no-evidence case, the deployment independence, sign-out's place inside the
  gate, and the sign-in and callback exclusion with its reason. It states no
  conjunct that section does not carry and drops none that it does, so a
  divergence between the two is a defect in this edit site. Without a spec home
  the requirement would live only in this proposal, and the test that pins it
  would have no section to cite.
- **§6.3.3 (`spec/06-mcp-server.md:92-112`)** — today it enumerates two accepted
  credentials, the gateway-forwarded `Bearer <token>` under `oidc-jwt` (`:96`)
  and the injected `X-Podium-User-*` headers under `trusted-headers` (`:108`).
  The browser flow adds no third credential. The `oidc-jwt` entry gains a second
  accepted location for the same credential: where the browser flow is enabled,
  a token the registry itself obtained through the §6.3.4 exchange may arrive in
  the `__Host-podium_session` cookie instead of the configured token header, and
  is verified identically against the issuer JWKS for the same `aud`. Because it
  is the same credential, the section adds no verification rule and no new
  refusal. It states the header-wins precedence rule under "The browser session"
  in spec voice, and states no condition that rule does not carry, so §6.3.3
  becomes the spec home of the rule. The `oidc-jwt` paragraph closes with an
  unqualified anonymity rule, "A header value without the prefix carries no
  token, so the request is anonymous and sees public visibility only (§4.6)"
  (`spec/06-mcp-server.md:96`), which names the state that rule hands to the
  cookie, so that sentence narrows to the anonymity rule under "The browser
  session", rendered in the section's voice, rather than standing unchanged. This
  is the same narrowing the mirror table stages on
  `docs/deployment/gateway-delegated-identity.md:58`, which restates this rule,
  and the same page's web-UI account at `:105-107` restates it for a directly
  reachable registry, so the authoring source and both shipped mirrors move
  together and carry identical conjuncts.
  The section's opening paragraph carries the same assumption. The
  `spec/06-mcp-server.md:92` row of the worked-example table under "The browser
  session" states that line's disposition and names every mirror that moves with
  it, and S3 owns the `spec/` half of that set, meaning the `:92` restatement
  here and the §2.2 bullet below. `:92` and `:96` are the only sentences in
  `:92-112` that this amendment touches. `:94` stands as written, per the
  tenant-derivation rule under "The browser session". The separate
  `trusted-headers` anonymity rule at `:108` stands unchanged, because the cookie
  branch cannot run under that provider; the anonymity rule under "The browser
  session" states why.
- **§2.2 (`spec/02-architecture.md:101`)** — the component map's
  `IdentityProvider` bullet mirrors the §6.3.3 opening clause as
  "Registry-process built-ins for a gateway-fronted deployment: `oidc-jwt` and
  `trusted-headers`", stating one gateway predicate over both providers. S3
  restates it in the same edit as its authoring source, on the disposition the
  `spec/06-mcp-server.md:92` row under "The browser session" states, so the
  bullet carries the predicate per provider and the applied spec does not
  describe `oidc-jwt` as gateway-scoped in §2.2 while §6.3.3 says otherwise. The
  bullet's client-side built-ins and its `IdentityProvider`
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
  session" gives each row.
  The section states that sign-in and the callback are outside the §6.3.4
  browser-origin gate, for the reason §6.3.4 gives, and records the two
  consequences that are visible at these routes: a callback presenting a session
  cookie from an earlier sign-in completes and replaces that cookie rather than
  being refused, and a sign-in presenting one starts a fresh transaction rather
  than being refused.
  It states that sign-out clears the cookies the cookie table names it as
  clearing on every request it serves, and that a sign-out the §6.3.4
  browser-origin gate refuses is refused before the handler runs and clears
  nothing, so a forged cross-origin sign-out cannot log an operator out. The
  gate's predicate is §6.3.4's and this entry restates none of it.
  The section also states the mount predicate in spec terms: the authentication
  routes are registered only where the browser flow is enabled, which the startup
  guard under "The browser session" states in full, including what enablement
  implies about the rest of the configuration, so a registry that boots with the
  flow disabled serves none of them and a request for one of those paths is
  answered as any path the registry does not register is answered on that
  deployment. The staged sentence therefore fixes no status code: the status
  belongs to the deployment rather than to the route, per "The status an
  unregistered path receives" under "The browser session", which §7 does not
  restate. The wiring that satisfies the staged sentence is the mount predicate
  stated under "The browser session", which also places the posture read
  `GET /v1/ui/session` this same §7 entry states. This keeps a
  deployment that wants no browser flow, including the shipped web-UI-only
  configuration, free of the routes.
  The same §7 entry states the posture read `GET /v1/ui/session`. Its body is the
  body "The posture read" states, and the §7 text carries no field, condition, or
  value that statement does not. "The browser session" gives the rest: its
  unauthenticated status and its mount on the web UI alone rather than on the
  browser flow. It is stated here rather than in §13.10 because it is an
  HTTP endpoint and §7 is where the registry's endpoints are specified, and it
  carries no acquisition option, so the key-placement rule does not reach it.
  The entry states the §13.2.1 classification once, in a single sentence covering
  sign-in, the callback, sign-out, and the posture read, as the read-only
  classification under "The browser session" gives it. §13.2.1 delegates the
  classification to each endpoint's own section, and §7 is that section for all of
  them, so two sentences here would be two §7 statements answering one question.
- **§7.3.1 (`spec/07-external-integration.md:95`), with the reingest trigger row
  at `:65` and the quickstart reingest comment at `spec/00-quickstart.md:46`** —
  the user-defined-layer
  paragraph states no owner rule for the write handlers; the only per-handler
  statements are the reorder comment at `:87` and the reingest row at `:65`.
  The paragraph gains the layer-write authorization rule under "The
  layer-ownership defect", which is the single statement of that rule and of
  every arm of it. The staged sentence says what the rule says and nothing the
  rule does not carry, so a clause present here and absent there is a defect in
  this edit site rather than an extension of the rule. Two conjuncts of the rule
  have no spec surface and stay out of the staged sentence: the `500`
  `registry.unavailable` refusal on a failed existence lookup, because §6.10
  carries no prose entry for that code even though it sits on the §6.10 matrix
  axis (`tools/matrix/matrices.go:109`), and where in each handler the gate runs.
  Both are stated in the rule and asserted by C1's tests.
  The staged sentence also carries the liveness condition, rendered the way §4
  renders the parallel re-embed carve-out (`spec/04-artifact-model.md:760`): the
  rule is live only where an identity provider is configured and public mode is
  off, so a registry that authenticates no caller keeps admitting the request
  (`spec/13-deployment.md:33`). The deployment carve-out under "The
  layer-ownership defect" is the statement this sentence renders, and it carries
  the code citations and the boot-wiring evidence that spec prose does not. A
  clause the carve-out carries and this sentence does not is a rendering choice
  for spec prose rather than a narrowing of the rule. This is the spec basis
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
  non-admin". After S6 the code also reports a caller the layer-write
  authorization rule under "The layer-ownership defect" refuses, which on a
  user-defined layer is expressly not an admin-only operation
  (`docs/reference/cli.md:440`), so the sentence is broadened to name what that
  rule refuses and nothing further.
- **§6.10 and §6.9** — the new codes. The `auth.csrf_invalid` entry names both
  refusals it covers and their `403`, in the scope "The CSRF position" states,
  and defers to §6.3.4 for each predicate rather than restating either, since S7
  stages §6.3.4 as the home of the browser acquisition flow and of the gate
  predicate alike.
  `auth.exchange_failed` covers a callback whose code exchange the IdP answered
  and refused, refused with `502` and carrying `retryable: false`, with a
  `suggested_action` naming the client credential and the registered redirect URI
  as what an operator checks. The entry defers to the exchange-failure rule under
  "The browser session" for the boundary against `registry.unavailable` rather
  than restating it, and the staged §6.10 text states what that rule states.
  `registry.unavailable` is unedited by this amendment. Both
  codes take a §6.9 row, an entry in the
  `errorCodeRegistry` at `pkg/registry/server/error_envelope.go:24`, a row in the
  `auth.*` table of `docs/reference/error-codes.md`, and a cell on the §6.10 axis
  in `tools/matrix/matrices.go:78-115`.
  `auth.token_expired` (`:355-364`) and `auth.untrusted_token` (`:366-376`) are
  amended in place rather than re-scoped. The unchanged-scope statement under
  "The browser session" is the single statement of what changes on them and of
  what does not, and S7 stages the `spec/` text the credential-location rule
  moves for these two entries, including the §6.9 row at `:329`. That rule also
  records why `auth.tenant_unknown` (`spec/06-mcp-server.md:378-388`) and its
  mirror `pkg/registry/server/error_envelope.go:73-75` stand unedited.
  The §6.10 axis in `tools/matrix/matrices.go:78-115`
  is hand-maintained rather than derived from `spec/` or from the envelope
  registry, which is why `auth.tenant_unknown` and `auth.untrusted_token` are
  shipped codes with no cell on it, and `matrix-audit` reports only cells the axis
  registers. Adding the two entries is what makes the
  `// Matrix: §6.10 (auth.csrf_invalid)` and
  `// Matrix: §6.10 (auth.exchange_failed)` annotations on the tests
  load-bearing; without them an annotation names no cell and the gate stays green
  whether or not the test exists.
- **§13.2.1 (`spec/13-deployment.md:41`)** — no edit. The read-only
  classification under "The browser session" derives the classification from this
  section's existing per-endpoint, per-mutation rule and lands it in §7's entry,
  so this section's text stands as written.
- **§11** — the verification entry, covering the matrix the generating rule
  under "Verification matrix" below produces.

**IMPLEMENTOR'S CHOICE:** the path of each authentication route. The method is
not part of this blank, because the route methods under "The browser session" fix
it. Any answer
places the paths under the existing `/v1/` prefix, uses one path per route, and
appears identically in the §7 entry, in the Authentication section of
`docs/reference/http-api.md`, in the mux registration, in the S45 step-4
rewrite, and in the new sign-in scenario, so every path those scenarios probe
matches the mux. The posture read reports the same values at runtime in
`sign_in_path` and `sign_out_path`, which is where the UI reads them, so the
bundle spells no authentication route path; "The posture read" states those
fields and when they are present. The posture read's own path is
`/v1/ui/session` and is not part of this blank, because the UI has to request it
before it has read anything.

**Shipped documentation mirrors.** Each restates spec text this amendment
changes, so each moves with it.

| Mirror | What it restates |
|:--|:--|
| `docs/deployment/gateway-delegated-identity.md:105-107` | the §13.10 web-UI account; 0012 recorded this page as this proposal's obligation |
| `docs/deployment/gateway-delegated-identity.md:58` | §6.3.3's anonymity rule, "A request carrying no token is anonymous and sees public visibility only", inside the page's `## oidc-jwt` section. It is restated from the anonymity rule under "The browser session", in the page's voice and with the same conjuncts the amended `spec/06-mcp-server.md:96` carries, which keeps it true for the gateway-fronted deployment this page describes, where no browser flow is enabled and no cookie is read. The rest of the paragraph stands: the `auth.untrusted_token` and `auth.token_expired` sentence ahead of it, and the JWKS fail-closed sentence after it. The page's web-UI account at `:105-107` restates the same rule for a directly reachable registry, so the two rewrites on this page carry identical conjuncts and give one request one answer |
| `docs/reference/error-codes.md:57` | `auth.untrusted_token`, restated under the credential-location rule so the row and its remediation match the amended `spec/06-mcp-server.md:366` and `:374` |
| `docs/reference/error-codes.md:59` | `auth.token_expired`, whose scope sentence stands and whose remediation clause is restated under the same rule |
| `docs/reference/error-codes.md:60` | `auth.forbidden`'s "When" text, "An admin-only operation attempted by a non-admin caller", is a shipped restatement of the §7 enumeration at `spec/07-external-integration.md:97` and moves with it, parallel to `docs/reference/cli.md:440` below. It is restated to name what the layer-write authorization rule under "The layer-ownership defect" refuses, saying what that rule says and no more. The `auth.*` table also gains the `auth.csrf_invalid` and `auth.exchange_failed` rows |
| `docs/reference/error-codes.md:69` | the bind guard's `config.web_ui_public_bind_refused`, which the amended §13.10 bind-guard sentence restates; the `config.*` table also gains a `config.web_ui_auth_unconfigured` row stating the browser-flow guard's predicate |
| `docs/reference/http-api.md:13-27` | the Authentication section: the header table, and the account of the accepted registry-process credentials at `:21-27`, which gains the browser session under `oidc-jwt`, together with the header and cookie names the wire contract under "The CSRF position" fixes and the request-side-value condition that contract states. It states the §6.3.4 browser-origin gate and the sign-in and callback exclusion as §6.3.4 states them, which is the predicate "The CSRF position" states, and scopes nothing of its own accord. In particular it does not describe a CSRF requirement every state-changing request carries: a request carrying no browser-origin evidence and no CSRF cookie is admitted, which is what the CLI and the SDKs send. It is also the new home of the authentication routes, each documented by the method and path the route methods under "The browser session" and the path blank above fix, and of the posture read `GET /v1/ui/session`, whose body it states as "The posture read" states it, carrying no field, condition, or value that statement does not carry, and whose unauthenticated status and web-UI mount predicate it states as "The browser session" gives them; there is no route list there today |
| `docs/reference/cli.md:131-138` | the `podium serve` synopsis, a closed usage line carrying `--web-ui` and `--web-ui-allow-public-bind`. It gains a token for each flag the key-placement rule under "Where configuration keys go" places on `podium serve`, and none for the keys that rule makes environment-only |
| `docs/reference/cli.md:142-155` | the `podium serve` flag table, which gains a row for each flag the key-placement rule places, written in the shipped table's voice as overriding the matching `PODIUM_*` variable, and whose `--web-ui-allow-public-bind` row (`:155`) is restated from the amended §13.10 bind-guard sentence |
| `docs/reference/cli.md:747` | the environment-variable table row that pairs `PODIUM_OAUTH_AUDIENCE` with `PODIUM_OAUTH_AUTHORIZATION_ENDPOINT` under one "OAuth provider config" label, which is what makes the two key sets read as one. It gains the environment-only browser-flow acquisition keys, per the key-placement rule, and states the device-code-key rule under "The browser session" as the amended §6.3.4 states it, scoping it no further of its own accord |
| `docs/reference/http-api.md:265-346` | the Layer management section, whose entries state the pre-S6 authorization rule: `:329` says a user-defined-layer update "still answers `200 OK`", `:320` gives the reorder rule as admin-only on an admin-defined layer, `:286` documents register's `201 Created` with no refusal, and unregister, restore, and reingest document no authorization at all, while every other gated route in the reference does (`:538`). The section gains one statement at its head, and the layer-write authorization rule under "The layer-ownership defect" is what that statement says. `:329`'s `200 OK` clause is scoped to the owner, `:320` is restated so the admin-defined sentence no longer reads as the whole rule, and `:286`'s register entry and the unregister, restore, and reingest entries carry the authorization they document none of today. This page names the error codes the refusals return where the staged spec text does not, because it is a code-level reference and `docs/reference/error-codes.md:158` already carries the generic `registry.unavailable` row.<br><br>**IMPLEMENTOR'S CHOICE:** the wording of the head statement. Any answer says what that rule says and nothing it does not carry, rendered in the reference page's voice with the codes named; a clause present here and absent there is a defect in this row rather than an extension of the rule |
| `docs/reference/http-api.md:457` | the Reembed entry's closing sentence, "The exception is specific to re-embed." It is true of the page as it ships, because the Layer management section documents no authorization today, and the head statement the row above adds is what makes it false: after S6 the layer write endpoints carry the deployment carve-out stated under "The layer-ownership defect". This row is a page-internal reconciliation rather than a mirror moving with its source, because the authoring sentence at `spec/04-artifact-model.md:760` qualifies its exclusivity with "does not extend to the other admin-gated endpoints, whose posture is defined in §4.7.2 and §7.3.2" and the layer write gate is neither admin-only nor specified in either of those sections, so the spec sentence stands as written and only the unqualified shipped restatement moves. The first sentence of the paragraph stands. The closing sentence is restated to record that the layer write endpoints admit a request on the same registries for the reason the Layer management head statement gives, and the restatement makes no exclusivity claim, because the erase endpoint documented at `:459-465` is covered by the same carve-out |
| `docs/reference/cli.md:440` | the `podium layer reorder` entry, whose "Reordering a user-defined layer requires no admin role" gives the pre-S6 rule as complete. Its user-defined sentence is restated from the layer-write authorization rule under "The layer-ownership defect", keeping the layer-class scope the entry already carries on its admin-defined sentence, and carrying the rule's liveness condition. It states nothing that rule does not carry |
| `docs/reference/http-api.md:290` | the register-response example, which prints snake_case keys for a response emitting Go field names |

## The CSRF position

A credential the browser attaches automatically authenticates any request the
browser can be induced to make, so every layer write this proposal exposes
becomes forgeable across origins.

The position is specified here rather than left to the implementor, because the
prior review treated it as acknowledged prose for eight rounds and never
produced a finding on it.

This section is the single statement of the gate predicate, including the
evidence it reads, which routes it excludes, and why. Every other site in this
proposal cites it by name and states only what is local to that site. §6.3.4 is
its spec home and carries it in the applied spec, so a conjunct present here and
absent there is a defect in that edit site rather than a narrowing of the gate.

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

- **What counts as state-changing.** A request is state-changing for this gate
  when its HTTP method is other than
  `GET`, `HEAD`, or `OPTIONS`. The predicate is the method rather than the
  handler's effect, because the gate runs before the handler and has nothing
  else to read, and because the safe methods are the ones a browser issues as an
  ordinary navigation or subresource load. Applied to the routes this proposal
  adds, whose methods the route methods under "The browser session" fix, the
  predicate covers sign-out and leaves sign-in and the callback outside the gate,
  which the named exclusion below states as well.
- **The refusal.** Every state-changing request, other than the sign-in and
  callback routes, that
  carries cross-site browser-origin evidence is refused before the handler runs
  with `403` `auth.csrf_invalid`, whatever credential authenticated it.
- **What counts as cross-site evidence.**
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
  nothing: the redirect-URI conjunct under "The browser session" leaves
  a browser no session credential to present on such an origin.
  Comparing against `Host` is what keeps the gate free of a new configuration
  key for the registry's public origin. OD-9 records the deployment that
  comparison does not serve, a gateway that rewrites `Host` to an upstream
  service name.
- **What is admitted.** A state-changing request carrying neither header carries
  no such evidence and
  is admitted, which is what a CLI, an SDK, or any other non-browser client
  sends. This gate is triggered by evidence and never requires a proof of same
  origin, so a request that proves nothing is admitted rather than refused. A
  gate that swept those requests in would break every non-browser writer on a
  registry that enables the browser flow. That admitted case is the gate's
  residual: a browser that sent neither header would be indistinguishable from a
  non-browser client. Every browser that can reach a `/ui/` deployment sends
  `Sec-Fetch-Site` on a cross-site request and `Origin` on a cross-origin form
  POST, and where the chosen mechanism also carries a request-side value the
  residual is closed for a session-authenticated write as well. The Testing
  section pins the refusal, the header-authenticated cross-site refusal, and both
  admitting halves.
- **The gate is not conditional on the browser flow.** It reads the request rather
  than the deployment, so it runs on every state-changing request the registry
  serves, including on a registry that enables no browser flow and on one that
  serves no web UI. A second enablement axis would leave the gateway-fronted
  `trusted-headers` deployment, where the browser flow cannot be enabled at all,
  outside a control its own forgery case needs, and it would cost a non-browser
  client nothing either way, because such a client carries no browser-origin
  evidence. §6.3.4 is where the predicate is stated because the browser flow is
  what makes a browser-borne credential reachable in the first place; the
  predicate itself names no deployment.
- **Sign-out is inside the gate.** The gate's coverage of sign-out is
  load-bearing, because a forged sign-out is a denial of service against a
  signed-in operator. A sign-out refused for CSRF returns `403`
  `auth.csrf_invalid` before the handler runs and clears no cookie. Read-only
  mode does not enter into it: sign-out is outside the §13.2.1 write set, per the
  read-only classification under "The browser session", so the CSRF refusal is
  the only refusal on this path.
- **Sign-in and the callback are outside the gate.** They answer on `GET`, so the
  method predicate leaves them outside it, and the exclusion is also stated by
  name so that an
  implementation that widens the method predicate does not silently pull them
  in. Each is a top-level navigation that carries no
  request-side value a page could have set, and a browser that already holds
  `__Host-podium_session` from an earlier sign-in sends that cookie on both, so
  under an unqualified predicate every re-sign-in would be refused with
  `auth.csrf_invalid`, no session would ever be established for that browser, and
  no recovery would remain, since re-running sign-in is the only recovery an
  expired session has. A callback presenting a session cookie from an earlier
  sign-in therefore completes and replaces that cookie, and a sign-in presenting
  one starts a fresh transaction. What binds both routes is the single-use
  pre-authorization transaction carrying `state`, `nonce`, and the PKCE verifier
  in `__Host-podium_auth`, whose contract under "The browser session" refuses
  exactly the forged and replayed callbacks a same-origin check would. A forced
  cross-origin sign-in can do no more than
  replace the victim's own `__Host-podium_auth` cookie with a transaction the
  registry mints for that same browser, which the victim's own IdP session then
  completes, so the transaction the attacker started is not one the attacker can
  finish in the victim's browser.
- **Cookies.** The gate adds no cookie outside the cookie table under "The
  browser session", and a CSRF mechanism that carries its own request-side value
  takes that table's CSRF row.
  `SameSite` is a defense in depth here rather than the control, which is why the
  evidence check above does not rest on it. Dropping the `__Host-`
  prefix from the CSRF row would let any host under the registry's registrable
  domain plant that cookie with a `Domain` attribute and then forge a
  state-changing request that echoes the planted value, which the session cookie
  would authenticate.
- **This bullet is the single statement of what `auth.csrf_invalid` covers.** The
  code is added to the §6.10 catalog by this proposal, it answers `403`, and it
  covers two refusals on one axis. The first is a state-changing request the
  §6.3.4 browser-origin gate above refuses, which the registry answers before the
  handler runs and whatever credential authenticated the request. The second is a
  callback the single-use pre-authorization transaction refuses, on any of the
  conditions its contract under "The browser session" fixes, which are a
  `__Host-podium_auth` cookie that is absent, expired, or carries a `state` other
  than the returned one, and an exchanged ID token whose `nonce` differs from the
  one that cookie holds. Those conditions, the order they are evaluated in, and
  the cookies each refusal sets and clears are stated there and are not restated
  here. The two refusals are disjoint by route, because sign-in and the callback
  are outside the gate for the reason the exclusion bullet above gives, so the
  transaction is what refuses a forged, replayed, or misdelivered callback. It is
  the same control on the same axis, so the callback reuses this code and no
  second code is added for it. No shipped code covers either refusal, because
  `auth.forbidden` reports an authorization decision about the caller and both of
  these refusals are about the request. The §6.10 entry names both refusals and
  defers to §6.3.4 for each predicate rather than restating either, which S7
  stages as the home of the browser acquisition flow and of the gate predicate
  alike, and §6.3.4 carries the predicate this section states.
- A session cookie the verifier refuses is answered by the expiry-signal rule
  under "The browser session", which this section does not restate. What is local
  to the CSRF position is that the rule adds no error code and re-scopes no
  envelope, so the codes this section introduces remain `auth.csrf_invalid` and
  `auth.exchange_failed` alone, and the unchanged-scope statement under "The
  browser session" is what `auth.token_expired` and `auth.untrusted_token` do and
  do not gain.

**The wire contract, where the mechanism carries a request-side value.** This
paragraph is the single statement of the wire contract, covering both its server
half and its client half. Every other site in this proposal cites it by name and
states only what is local to that site. The names are fixed here rather than left
open, because the UI, the server, the shipped documentation, and the tests all
have to spell them identically. The request-side value is carried by the
`__Host-podium_csrf` cookie, whose attributes, the route that sets it, and the
route that clears it are the cookie table's CSRF row under "The browser session".
That row carries no `HttpOnly`, which is what lets the panel read the value, and
its `__Host-` prefix is what closes the sibling-host forgery in which a host
under the registry's registrable domain plants the cookie and echoes the planted
value; a stateless double submit carries nothing a server-side comparison could
distinguish from a value the registry issued. Every state-changing call the panel
issues echoes that cookie's value in the `X-Podium-CSRF` header, including the
panel's own sign-out `POST`, which the method predicate above makes
state-changing. The server compares the header against the cookie and refuses a
state-changing request that the `__Host-podium_session` cookie authenticates and
whose header is absent or unequal, before the handler runs, with `403`
`auth.csrf_invalid`. The requirement is keyed to authentication by the session
cookie rather than to the presence of the CSRF cookie, which decides two cases
the keying would otherwise answer differently: a request the session cookie does
not authenticate carries no header requirement, which is what a CLI, an SDK, and
a gateway-fronted browser request all send, and a browser holding a live session
cookie and no CSRF cookie is refused, which is the outcome both rows' absent
`Max-Age` exists to prevent. Where this half lands it also closes the gate's
residual for a session-authenticated write, because such a write is refused
whether or not it carries browser-origin evidence. U1 owns the client half and C2
owns the server half. A mechanism that carries no request-side value, meaning the
browser-origin evidence check alone, sets no cookie and requires no header, and
the cookie table's CSRF row is then absent. When the client half is missing while
the server half is present,
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

**The sanitization rule.** This rule is the single statement of how the UI
renders an untrusted artifact body: what is sanitized, what the sanitizer takes
as its input, where it is applied, which URL schemes survive it, and what falls
outside it. Every other site in this proposal cites it by name and states only
what is local to that site.

The UI renders an artifact body through one rendering path in the web UI's own
source tree, and that path sanitizes what it renders. The sanitizer runs on the
rendered output rather than on the markdown source, so a markdown construct that
the renderer emits as raw HTML is neutralized rather than carried through, and a
construct that survives the markdown renderer cannot bypass the sanitizer. No
executable node and no event-handler attribute survives sanitization. The
sanitizer carries an allowlist that admits no URL scheme other than `http`,
`https`, and `mailto` on any attribute it keeps, so a URL bearing any other
scheme, including `javascript:` and `data:`, does not survive on a link or on any
other attribute. That rendering path is the one the `dangerouslySetInnerHTML`
check scopes, so a second path cannot render an artifact body outside this rule.
Frontmatter does not reach this path. It is rendered as a property table with
values escaped as text, and it is not markdown and is not rendered as such.

- **The `dangerouslySetInnerHTML` check.** This bullet is the single statement of
  that check. Every other site in this proposal cites it by name and states only
  what is local to that site. A mechanical check reports every occurrence of
  `dangerouslySetInnerHTML` in the web UI's own source tree. The single sanitized
  rendering path carries the one permitted occurrence, because React admits
  already-sanitized markup only through that attribute and the rendering path
  above is the place this proposal puts it, and the check fails on every other
  occurrence. The check is mechanical rather than a review obligation, and it
  runs in the CI job that also runs the rebuild-is-clean check, so a tree that
  adds an occurrence outside the rendering path fails that job before review. B1
  owns the check, because B1 owns the CI lane. The check is scoped to the web
  UI's tree rather than to the repository, because
  `site/` already uses the attribute
  (`site/src/components/content/Tabs.tsx:78`,
  `site/src/components/layout/Lockup.tsx:31` and `:38`,
  `site/src/build/render.ts:132`). The documentation site renders build-time
  authored content into a published static page and serves no registry artifact
  body on the registry's origin, so it is outside this control. A
  repository-wide check would fail on the current tree before any web-UI code
  exists, and would then be deleted or silently rescoped.

**IMPLEMENTOR'S CHOICE:** which sanitizer implementation the rendering path uses.
Any answer satisfies the sanitization rule above in full and the Testing
section's sanitizer cases verbatim, and states no scheme, attribute, or path
condition the rule does not carry.

## Build and embedding

The React bundle is committed to the tree.

`go:embed` is a compile-time directive: `web/web.go:12` names the files, and a
missing path is a build error rather than a runtime one. Today the three source
files are committed, so a clean clone builds with only a Go toolchain.
Generating the bundle at release instead would break the Go-only build stated by
the committed-bundle constraints below, on every path those constraints name.

`site/` is not a precedent for the alternative. Its `dist/` is gitignored, but
site output is published rather than embedded, so nothing in `go build` depends
on it.

**The committed-bundle constraints.** This block is the single statement of what
the committed bundle must satisfy. Every other site in this proposal cites it by
name and restates none of its content.

- **Go-only build.** A clean clone builds with only a Go toolchain present.
  `go build ./...`, `go install` from source, `make build` (`Makefile:316`), the
  `go` CI job, which carries `actions/setup-go` and installs no other toolchain
  (`.github/workflows/test.yml:22`), and the release cross-compile matrix
  (`.github/workflows/release.yml:298`) all succeed with no Node toolchain and no
  other non-Go tool installed. `go:embed` is resolved at compile time
  (`web/web.go:12`), so a bundle generated at release rather than committed is a
  build error in every one of those paths.
- **Served root.** `web/web.go`'s embed directive names the built bundle, and
  `web.Assets()` returns a file system rooted at the served bundle, through
  `fs.Sub` when the bundler emits into a subdirectory.
  `internal/serverboot/serverboot.go:1230` mounts that file system directly at
  `/ui/`, under the `cfg.webUI` guard at `:1229`, so a bundle whose `index.html`
  is not at the returned root stops serving the UI and `/ui/` stops returning
  `index.html`.
- **Title.** The built `index.html` carries `<title>Podium</title>`, which today
  comes from `web/index.html:5`. A bundler's scaffolded `index.html` carries the
  tool's own title, so the title is a constraint on the bundle rather than an
  incidental property. Three shipped assertions read that literal out of the
  served or embedded index (`web/web_test.go:19`,
  `cmd/podium/serve_ui_test.go:51`, `test/e2e/server_flag_behavior_test.go:30`),
  and holding it leaves all three standing unchanged.
- **Asset references.** Every asset the built `index.html` references resolves
  under the `/ui/` mount, and `web.Assets()` serves every referenced path. The UI
  is served through `http.StripPrefix("/ui/", …)`, and the outer mux routes every
  other path to the meta-tool handler, which registers no `/assets/` route
  (`internal/serverboot/serverboot.go:1230`, `:1239`,
  `pkg/registry/server/server.go:389-419`). A bundle built with the common
  default public base of `/` emits `<script src="/assets/index-<hash>.js">`, the
  browser requests `/assets/…`, the outer mux returns `404`, and `/ui/` serves a
  blank page while the rebuild check and the title assertion both still pass.
  Today's hand-written SPA avoids this only because its references are relative
  (`web/index.html:7`, `:18`). Either the bundler's public base is set to `/ui/`
  or the emitted references are relative.
- **Deterministic output.** Rebuilding the bundle from unchanged source produces
  an unchanged working tree, which is what makes the rebuild-is-clean check a
  stable gate, and the built bundle is the only generated artifact the change
  adds to the tree.

What lands, in addition to the committed-bundle constraints above:

- The bundle is committed at a path that escapes the bare `dist/` entry at
  `.gitignore:18`, either by negation or by a directory name that does not match
  it.
- `web/web.go`'s `go:embed` directive is repointed at the built bundle, and its
  package and `Assets` doc comments stop describing three hand-written files. The
  served-root entry in the committed-bundle constraints governs the root
  `web.Assets()` returns.
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
- A CI step rebuilds the bundle and fails if the working tree differs, which is
  what makes the committed output trustworthy rather than merely present. This is
  part of the deliverable.
- A `.gitattributes` entry marks the bundle generated so review diffs collapse.
  The repository has none today.

**IMPLEMENTOR'S CHOICE:** the bundler and the output path. Any answer satisfies
the committed-bundle constraints above.

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
authentication route creates. The brief is corrected before the design pass reads
it.

**Which claims the correction reaches.** The design pass relies on every claim the
brief makes about what an endpoint returns and about what a caller sees on a given
deployment. A claim is reached when the authority that settles it contradicts it,
when that authority settles it only under conditions the brief leaves unstated,
or when the design pass needs the claim and the brief carries none. A claim that
holds against its authority on every response and every deployment stands as
written. The design questions the brief names above are not claims about the API,
and G1 does not reach them.

**The authority fixes the correction.** Every claim in reach answers to the wire
contract, to the visibility and deployment semantics, or to a surface this
proposal adds, and which of those it answers to determines the form the
correction takes.

- **A claim the wire contract settles is replaced by a citation.** The brief owns
  no wire fact: it states no field name, type, wire key, or status code of its
  own. It cites `docs/reference/http-api.md` where the reference carries the
  field, and it cites the response struct where the reference does not. The design
  pass reads the cited source for any field it renders or gates on. This is what
  corrects the field-level errors the brief carries today, which are the layer
  list's field inventory (`web/DESIGN.md:126-129`) and the type of `frontmatter`,
  without leaving the brief a second copy of the wire contract to drift from. The
  reference carries `frontmatter` on a search result at
  `docs/reference/http-api.md:120`, already correct on the shipped page. It
  carries neither the `load_artifact` response's `frontmatter` nor the layer
  surface: it documents no `GET /v1/layers` response body (`:296-300`) and its
  register example elides every key past the first two (`:290`), so the cited
  source for a layer's fields is `store.LayerConfig` (`pkg/store/store.go:258`),
  which the register response embeds under `layer` (`LayerRegisterResponse`,
  `pkg/registry/server/layers.go:328-332`) and the list response returns under
  `layers` (`pkg/registry/server/layers.go:762-777`).

  **IMPLEMENTOR'S CHOICE:** which layer fields the brief names. `web/DESIGN.md`
  names no field, type, or wire key of its own and cites the response struct for
  the layer surface, so the design pass reads that struct for any field it renders
  or gates on, including that field's marshalled key. A field name appearing in
  this proposal or in the brief is illustrative rather than contractual. Nothing
  mechanical catches a brief that restates a field, because the brief has no
  compiler or test behind it.

- **A claim the visibility and deployment semantics settle is rewritten onto the
  signal the page reads.** What a caller sees is settled by the §4.6 visibility
  predicate and by the deployment's identity posture, and a brief sentence that
  states one deployment's outcome unconditionally reads to the design pass as
  universal. The correction rewrites the claim in the vocabulary of the signal the
  page actually has. Where the claim keys on the deployment, that signal is the
  posture read, and the rewritten claim names the fields the read returns
  (`identity_provider_configured`, `public_mode`, `browser_auth.enabled`, and
  `subject`). Where no field the read returns distinguishes an arm, the rewritten
  claim takes the catalog response as the signal for that arm, which is the source
  the expiry-signal rule under "The browser session" already uses. A claim of this
  kind that another section of this proposal owns as a rule is rewritten to name
  that rule and to carry no condition the rule does not state, which is how
  `web/DESIGN.md:20-22` is scoped: everything the UI displays comes from the
  endpoints an SDK would call, filtered by the caller's identity, holds of the
  catalog endpoints and is false of `GET /v1/layers`, so the layer-panel section
  (`web/DESIGN.md:120-147`) states that the layer list arrives unfiltered and that
  the panel's role split is presentation over it, naming the unfiltered-list rule
  under "The layer-ownership defect".

- **A claim a surface this proposal adds settles is added to the brief section
  that owns the surface.** The brief predates the browser session, so a claim
  about its routes, its cookies, its posture read, or its refusals is either
  absent from the brief or written against a §13.10 that ran no acquisition flow.
  The correction adds the claim to the brief section that owns the surface it
  appears on, in the brief's own vocabulary, carrying no status, code, condition,
  resolver, or surface the owning rule in this proposal does not state. A clause
  in the brief and absent from the owning rule is a defect in the brief. The
  session expiring mid-use, which `web/DESIGN.md:163-164` names as a transition
  without naming the signal the panel receives, gains the expiry-signal rule under
  "The browser session" on those terms, and that rule remains the rule's owner.

**G1 is the single statement of the posture-keyed rendering rules.** The
panel-visibility rule, the catalog-scope rule, and the sign-in control table are
each stated once here, in the vocabulary the posture read returns, so that no
other site translates a prose scoping into a field test. Every other site in this
proposal names a rule and cites G1, and states no condition, field, or value the
statement here does not carry.

- **The panel-visibility rule.** `web/DESIGN.md:145-147` ends the role split with
  "an anonymous caller sees no panel at all". On a registry that configures no
  identity provider every caller is unauthenticated, so under that rule the panel
  renders for nobody exactly where the server admits every layer write. The
  deployment carve-out under "The layer-ownership defect" states when the gate is
  live and what follows where it is not. The sentence is rewritten to key on the
  posture read's `identity_provider_configured`, and that single flag is
  sufficient for the reason the carve-out gives: when the read reports it true, a
  caller for whom the read returns no `subject` sees no panel; when the read
  reports it false, the panel renders with the full set of write operations for
  every caller, because the server admits them there. The administrator arm of the
  role split stays a server decision the page does not predict: the panel renders
  its write operations and presents the not-permitted state on a `403`
  `auth.forbidden`, because no response reports the caller's admin role. This is
  also what corrects `web/DESIGN.md:153-154`, which says the UI has three identity
  states and "cannot always tell them apart from the client side": the anonymous
  and authenticated states are distinguished by whether the read returns a
  `subject`, and the administrator state is not reported at all.

- **The catalog-scope rule.** `web/DESIGN.md:156-158` describes the anonymous
  state as one in which "The catalog renders, filtered to public artifacts". On a
  registry that configures no identity provider, and in public mode, the
  visibility evaluator short-circuits to true for every layer
  (`pkg/layer/composer.go:53`, `:65`, `spec/04-artifact-model.md:615`,
  `spec/13-deployment.md:33`), so the anonymous view is the full catalog rather
  than a public subset. One further deployment class has no anonymous view at all:
  under `injected-session-token`, which is a registry-process provider a web-UI
  registry can run (`spec/13-deployment.md:468`), the meta-tool identity
  middleware verifies before the handler runs and an absent token is a
  verification failure, so every catalog call from a browser holding no
  runtime-signed token returns `401` `auth.untrusted_runtime`
  (`pkg/registry/server/identity_verify.go:44-52`, `:118`,
  `pkg/identity/runtime.go:137-138`). Those two booleans do not distinguish it
  from the `oidc-jwt` and `trusted-headers` case, and the response the read
  returns carries no provider name, so the rule takes the refusal as an arm rather
  than gaining a posture field. The sentence is rewritten to key on the posture
  read's `identity_provider_configured` and `public_mode`, and on whether the
  catalog read answers: where a catalog read is refused with `401`, there is no
  anonymous view and the page renders the refused state rather than an empty or a
  filtered catalog, and where a caller who had a `subject` sees that refusal it is
  the expiry transition the expiry-signal rule names; where the catalog read
  answers, the anonymous view is the public subset when the read reports
  `identity_provider_configured` true and `public_mode` false, and is the whole
  catalog on every other combination of the two.

- **The sign-in control rule.** The brief has no authentication affordance.
  `web/DESIGN.md:163-164` names signing in and signing out as transitions, and it
  was written while §13.10 said the UI "runs no acquisition flow of its own", so
  nothing in the brief's surfaces (`web/DESIGN.md:48`), in "What the design pass
  should produce" (`:194-200`), or in "Out of scope" (`:187-192`) gives the design
  pass a control a human clicks. With the brief unamended and the implementor
  barred from designing the UI, U1 would have no source for the surface the new
  sign-in manual scenario requires a human to use. The brief's state-matrix
  section gains it, as a control in the application shell rather than as another
  entry in the surface list, so the surface list and its heading stand. The table
  below is the sign-in control rule, keyed on the posture read's
  `browser_auth.enabled` and `subject`.

  | `browser_auth.enabled` | `subject` | Control rendered |
  |:--|:--|:--|
  | true | absent | sign-in, as a top-level navigation to the read's `sign_in_path` |
  | true | present | sign-out, as a `POST` from the page carrying the same proof the panel's writes carry, after which the page navigates |
  | false | absent or present | neither control |

  The sign-out row renders a `POST` from the page rather than a link, because that
  is the method the route methods under "The browser session" fix and a control
  the human clicks has to issue the request the route answers. Both conjuncts are
  required on each of the first two rows, because the read carries `sign_in_path`
  and `sign_out_path` only when `enabled` is true and each route is registered
  only where the flow is enabled, so rendering a control on any other combination
  would send the browser to a path the mux does not serve, whose response is
  whatever that deployment answers for an unregistered path rather than anything
  the page can present. "The status an unregistered path receives" under "The
  browser session" states what that response is. The third row covers the shipped
  web-UI-only posture, the default standalone one, and the gateway-fronted §13.10
  deployment, where a subject does resolve: the gateway authenticates the request
  and the registry resolves the caller's identity from the forwarded token or the
  injected headers (`spec/13-deployment.md:170`), which is the identity
  `layerIdentity` returns to the posture read
  (`internal/serverboot/serverboot.go:1198`), while the browser flow is off and
  under `trusted-headers` cannot be enabled at all. Clearing a Podium cookie would
  not end the gateway's own session there.

**Corrections the rule above does not reach.** Each is a design instruction
rather than a claim about the API, so each is named here.

- **A layer that matches on more than one visibility axis.** Visibility is a union
  of the independent fields `Public`, `Organization`, `Groups`, and `Users`
  (`pkg/store/store.go:270-273`), and "Multiple fields combine as a union; a
  caller sees the layer if any condition matches"
  (`spec/04-artifact-model.md:611`), which is why the §4.6 matrix enumerates every
  non-empty subset (`tools/matrix/matrices.go:124-140`). Replacing the brief's
  restated field list with a citation leaves the design pass without a display
  treatment, so the layer-panel section states one for a layer that matches on
  more than one axis, because a single-valued label cannot render a layer that is
  both public and group-scoped.

- **A response with no frontmatter pairs to render.** The brief gives the design
  pass a treatment for it, covering every response the API returns with no pairs.
  The API produces that state on the paths the API tests establish, and the
  producers known today are these. A search result carries no `frontmatter` key
  when the child's `extends:` block cannot be rewritten, which the reference
  states (`docs/reference/http-api.md:120`) and which the struct's `omitempty` tag
  produces (`pkg/registry/server/server.go:557`). A `load_artifact` response for a
  non-skill artifact whose `manifest_body_url` is set carries an empty
  `frontmatter`, because the registry clears it alongside the inline
  `manifest_body` and the consumer fetches the document from the URL
  (`pkg/registry/server/server.go:582`, `:1235-1240`). The reference's
  `manifest_body_url` sentence (`:172`) states the clearing for the body alone,
  and its `load_artifact` field list (`:156-169`) names no `frontmatter` field, so
  the cited source for that half is the response struct. The property table is
  produced in the client from the value those sites describe. The staged surfaces
  that rest on this treatment are §13.10's frontmatter property table and the
  escaping control under "Rendering untrusted content", whose sanitizer case
  asserts that a markup-carrying frontmatter value renders as literal text in that
  table.

## Verification matrix

§11 requires nothing of the UI today. S5 states the obligation, and this section
states the rule that generates what S5 covers, so coverage is checked per unit of
stated behavior rather than per test that happens to be written. The rule is
stated here rather than an enumeration of surfaces, because a surface list is
assembled by noticing combinations and is complete only when noticing stops,
while the rule below makes a missing case a missing statement or a missing
condition point, which the reader can derive.

**The generating rule.** This block is the single statement of what the
verification matrix contains. Every other site in this proposal cites it by name
and states only what is local to that site. The matrix carries one cell per unit,
obligation kind, and driver, and a cell is present exactly when the unit carries
an obligation of that kind on that driver.

- **The units.** A unit is a surface `web/DESIGN.md` names under its surfaces
  heading, or a block of this proposal that is the single statement of what a
  request the browser issues receives or of what the page renders. A surface's
  statement is that surface's section in the brief as G1 corrects it. Both sets
  are enumerable from their documents: the brief fixes its surfaces, and this
  proposal states every behavior it fixes once, in a block that forbids
  restatement elsewhere. A browser-observable behavior with no unit is therefore
  a behavior with no single statement, and a statement added later carries its
  cells with it.
- **The obligation kinds.** Read is a request that leaves registry state as it
  found it, Write is a request that changes it, Error is the refusal or failure
  arm of either, and Render is what the human is shown. The units predicate
  admits a statement of what a request the browser issues receives or of what the
  page renders, so an obligation is one or the other, and Render is the second. A
  request either changes registry state or does not, which separates Read from
  Write, and it takes either the success arm its statement gives it or the
  refusal or failure arm, which separates Error from both, so the kinds cover
  every obligation a unit carries. A cell is absent where the unit carries no
  obligation of that kind.
- **The drivers.** A request is server-driven, meaning the test constructs it, or
  browser-driven, meaning the built bundle's own client code issues it. Which one
  a cell takes is decided by the half of the cited statement it exercises: C1 and
  C2 own the server half, U1 owns the client half, and a statement carrying both
  halves is driven both ways. The wire contract under "The CSRF position" is the
  case that requires both, because a missing client half leaves every
  server-driven case passing while every panel write is refused in the browser.
- **The condition points.** A cell is driven at every point the cited statement
  ranges over. The points are read off the statement rather than off a fixed
  list: a statement's condition points are the values of each variable its own
  text branches on, and a statement is the single statement of its behavior, so
  it carries its own branches and no other site adds one. A variable a statement
  does not branch on generates no point for that statement's cells, and a
  variable a statement branches on generates its points whether or not the
  variable appears below. The variables that recur across statements today are
  the location the credential arrives in, which the credential-location rule
  fixes at the configured token header, `__Host-podium_session`, both, and
  neither; the browser flow enabled and disabled; the identity posture, meaning
  the identity provider the registry configures together with public mode, of
  which the C3 guard admits the browser flow on `oidc-jwt` with public mode off
  alone; the caller's standing, whose arms the layer-write authorization rule
  under "The layer-ownership defect" states; the registry's §13.2.1 write mode;
  the browser-origin evidence and the request-side value the CSRF gate predicate
  branches on, together with the refusal the wire contract under "The CSRF
  position" produces from them; and, on an Error cell whose statement turns on a
  dependency the registry called, whether that dependency answered. That
  enumeration is worked here for the reader and decides nothing.
- **What a cell may say.** A cell names its unit's statement, cites the
  deliverable that owns it, and gives the condition points it is driven at. It
  states no condition, field, status, code, or value the statement does not
  carry, so a clause present in a cell and absent in the statement is a defect in
  the cell rather than an extension of the statement. Where a statement leaves a
  value to the deployment or to the implementation, the cell states the
  obligation over the condition points and the test establishes the value. The
  status a request for an unregistered path receives is the case this settles:
  that status is the deployment's under "The status an unregistered path
  receives" rather than any route's, so a cell carries the obligation and names
  no status. The HTTP method a route answers on is settled the other way, by "The
  route methods", so a cell cites that paragraph and restates no method.

**IMPLEMENTOR'S CHOICE:** how each cell is worded, and whether the matrix is
rendered as a table keyed on unit and obligation kind or as a list per unit. Any
answer applies the rule above, covers every cell the rule generates, and adds no
obligation the cited statements do not carry.

The rule excludes the build and embedding statements, meaning the
committed-bundle constraints and the served bundle, whose verification is B1's
and whose cases the Testing section carries. It does not reach the startup guard,
its ordering, the redirect-URI conjunct, or the key-placement rule, because each
states what the process does before it serves anything and no request the browser
issues observes it; C2 and C3 own those cases in the Testing section. One
obligation is the composition of two units and so stays named: a sign-out refused
for CSRF clears nothing, which follows from the cookie table's clearing column
together with the gate answering ahead of the handler, and which is asserted on
the cookie table's Write cell at the CSRF-refusal condition point.

One further obligation is driven at a condition point no statement branches on
and so stays named here: the search unit's Render cell is driven with the
optional result fields, the relevance score and the sensitivity label, both
present and absent. The brief leaves the treatment of those two fields to the
design pass, so the search statement branches on neither and the rule generates
no such point. This is the one cell that carries a condition point its statement
does not carry, and the exception is stated here for that reason.

The rule decides which units the matrix carries, and the units it admits today
are the domain browser, search, the artifact viewer, and the layer panel from the
brief, and, from this proposal, the unfiltered-list rule, the layer-write
authorization rule, the route methods, the cookie table, the callback order and
outcomes, the expiry-signal rule, the exchange-failure rule, the header-wins
precedence rule, the anonymity rule, the no-session-state rule, the posture read
and its body, the status an unregistered path receives, the read-only
classification, the credential-location rule for the remediation and message
strings it moves, the CSRF gate predicate and its wire contract, the sanitization
rule, and the posture-keyed rendering rules G1 states. That derivation is worked
here for the reader and decides nothing. A unit the predicate admits is a unit
whether or not it is named here, and a name here the predicate does not admit is
a defect in this paragraph rather than a unit.

## Testing

- **Owner authorization (C1).** The cases below assert the layer-write
  authorization rule under "The layer-ownership defect" and assert nothing that
  rule does not state; a case asserting a clause absent there is a defect in the
  case rather than an extension of the rule. On a registry with an identity
  provider configured, a caller who is neither the owner nor an admin, and
  separately a caller who resolves no subject at all, each receive the refusal
  the rule states from `unregister`, `update`, `restore`, `reorder`, and
  `reingest` against a `UserDefined: true` layer; the owner succeeds; an admin
  succeeds on any layer. The no-subject case is the one §6.3.3 makes reachable by
  treating a request as anonymous during a JWKS outage. A separate case covers
  `reingest` against an admin-defined layer, where the rule authorizes a tenant
  admin alone and the handler runs no authorization today at all
  (`pkg/registry/server/layers.go:946-991`): an authenticated non-admin, a caller
  the stored `Owner` field names, and a caller resolving no subject each receive
  the refusal and no ingest runs, and an admin succeeds. The same case is
  repeated with a break-glass body, because the rule places the gate ahead of
  `runIngestAndRespond`, so a break-glass reingest is refused for the same caller
  and bypasses no freeze. The refusal cases install both overrides the permissive
  `NewLayerEndpoint` defaults stated under "The layer-ownership defect" require:
  a denying `WithAdminAuth`, and a `WithIdentityResolver` that resolves a
  non-owner or no subject.
- **Registration takeover (C1).** On the same registry, these cases assert the
  layer-write authorization rule under "The layer-ownership defect" as `register`
  applies it, and assert nothing that rule does not state. The case set is the
  product of the dimensions below, and the outcome rule decides every point of it,
  so a reader derives a case rather than looking one up.

  The dimensions are the caller's relation to the stored layer, which is the
  subject the stored `Owner` names, a different verified subject, or no subject at
  all; the stored layer's class, which is `UserDefined: true` or
  `UserDefined: false`; the state of the posted ID in the store, which is a live
  layer, a layer soft-deleted and still inside its §8.4 recovery window, or no
  layer; and the existence lookup's health, which is a lookup that answers, a
  lookup whose read of the live set fails with an error that is not
  `store.ErrNotFound`, or a lookup whose read of the tenant's tombstoned layers
  fails the same way. Each point is driven with a request body carrying the ID
  alone and again with a body that also asserts `{"user_defined": true}` and an
  `owner` naming the caller.

  The outcome rule. A lookup that fails is refused with `500`
  `registry.unavailable`. An ID naming no stored layer is admitted for an
  authenticated caller. An ID naming a layer, live or tombstoned alike, takes the
  arm that layer's class selects: a user-defined layer admits the subject its
  stored `Owner` names and refuses every other caller, and an admin-defined layer
  refuses every non-admin caller whatever its stored `Owner` names. A caller
  authorized by neither arm is refused with `403` `auth.forbidden`. Every refusal
  runs no write, so the stored layer's owner, source, `UserDefined`, and
  visibility are unchanged and a tombstone the ID named is still in place. What
  the request body asserts changes no outcome at any point.

  The points that discriminate this rule from a weaker one are these. The caller a
  stored admin-defined layer's `Owner` names is refused, because that field is
  assigned from the request body (`pkg/registry/server/layers.go:659`) and
  patchable (`:547-549`) and so names no authorized subject, which separates the
  qualified owner arm from an unqualified one. A caller resolving no subject is
  refused rather than admitted, which is the state §6.3.3 makes routine during a
  JWKS outage. A tombstoned ID is refused rather than admitted, which a
  `GetLayerConfig`-only lookup gets wrong. A failed lookup is refused rather than
  treated as an unused ID, which pins the refusal arm rather than the
  names-no-stored-layer arm. The asserting body is refused on the same terms as
  the plain one, which pins the gate ahead of the `req.UserDefined` short-circuit
  at `pkg/registry/server/layers.go:610-611`: `register` is the only gated handler
  with a branch that never reaches `authAdmin`, so an owner comparison placed
  after `authAdmin` passes every other point and fails this one. Every refusal
  point installs both overrides the permissive `NewLayerEndpoint` defaults stated
  under "The layer-ownership defect" require.

  The product does not reach the recovery window's second half, so that case is
  driven as a sequence: alice registers a user-defined layer and unregisters it,
  bob re-registers that ID and is refused, and alice's subsequent `restore` still
  succeeds, which asserts the tombstone the refusal preserves as well as the
  refusal itself. The product does generate a caller who resolves no subject
  posting an ID that names no stored layer, and that point is not driven, because
  the rule states the admitted arm for an authenticated caller and settles no
  outcome there.

  The test establishes what this bullet does not. Which store call the fault
  injection targets to fail each set follows from the lookup sequence the
  implementor chooses under the IMPLEMENTOR'S CHOICE above, and whichever sequence
  that is, both failures are reachable, because the lookup covers both sets. How a
  caller who resolves no subject is produced, and how the stored layer is read
  back to assert that it is unchanged, are the test's to fix as well.
- **Owner authorization, no identity provider (C1, e2e).** A standalone registry
  unregisters a user-defined layer that was registered with
  `podium layer register --user-defined --owner alice`, which is the admitted
  case the deployment carve-out under "The layer-ownership defect" describes. The
  test first reads the stored layer back and asserts its `UserDefined` is true and its
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
- **Expired session across surfaces (C1 and C2, integration).** On a registry
  with the browser flow enabled, a request carrying a session cookie past the
  token's `exp` is driven against each surface the expiry-signal rule names, and
  each response is asserted against that rule: a meta-tool route, a layer write
  against a layer the cookie's subject owns, `GET /v1/layers`, and the posture
  read. A further case runs with the JWKS unreachable and asserts the anonymous
  arm. This pins the split the rule states, so a design or a panel built on a
  single expiry signal fails here rather than in the browser.
- **Routes (C2, integration).** Driven over HTTP against a stub IdP whose token
  endpoint the fixture controls. The rule below generates the cases, and the
  blank at the end of the bullet carries what the rule leaves to the implementor.

  **The generating rule.** A case is one run of the flow with one value chosen on
  each axis below, and it asserts the outcome the pre-authorization transaction
  contract under "The browser session" fixes for that combination together with
  the cookie effects "The callback order and outcomes" states for that outcome.
  A binding case fixes one element that the flow mints at an observable point and
  consumes at a later one, and asserts that the value the consume point receives
  is the value the mint point emitted; the case is required because an
  implementation can satisfy either point alone while the two disagree. An
  outcome case fixes the transaction cookie the callback receives, the session
  cookie the browser already holds, what the stub returns, and what
  browser-origin evidence the request carries, and asserts the status, the
  redirect or the error envelope, and the cookie effects the contract gives that
  combination. The implementations the axes generate are, one per axis, the one
  that ignores a coordinate the contract reads, meaning it takes an outcome
  without consulting that axis, and the one that reads a coordinate in an order
  the contract does not, meaning it consults that axis ahead of a coordinate the
  contract compares first. A cell is written here when it names one of those
  implementations and that implementation passes every assertion the contract's
  per-element statements produce on their own. Every other cell is carried by the
  blank.

  **The axes.**
  - *The element that crosses a stage boundary.* Its values are the `state`, the
    `nonce`, the PKCE `code_verifier` together with the challenge derived from
    it, each value the configuration supplies to the sign-in redirect
    (`client_id`, `redirect_uri`, and the authorization endpoint), and the token
    the exchange returns. The cookie table's `__Host-podium_auth` bullet states
    what the transaction holds, "The sign-in redirect" states what the redirect
    carries, and "What the cookie holds" states what the callback returns, so
    those statements close the axis and fix each element's mint point and consume
    point. Naming the element names the pair of points a case observes.
  - *The configuration source the sign-in redirect reads.* Its values are the
    browser-flow key, the device-code key of the same name, and the issuer's
    discovery document, which are the places a running registry holds the value
    at the moment the redirect is built. The device-code-key rule under "The
    browser session" is what makes the browser-flow key the one the redirect
    reads.
  - *The transaction cookie the callback receives.* Its values are absent,
    expired, present carrying a `state` other than the returned one, and present
    and matching. The refusal conditions the contract fixes are the first three,
    and a matching cookie is their complement.
  - *The session cookie the browser already holds.* Its values are absent and
    present and verifiable. A session cookie the verifier refuses is driven by
    the expired-session bullet above.
  - *What the stub returns to the callback.* Its values are a query carrying the
    IdP's `error` parameter, whatever else that query carries; and a query
    carrying no `error` parameter, taken once per exchange outcome, which are a
    `code` the stub exchanges for an ID token whose `nonce` matches the
    transaction, a `code` the stub exchanges for an ID token whose `nonce`
    differs, a `code` the stub refuses with an OAuth error such as
    `invalid_grant`, and a `code` whose exchange the stub leaves unanswered or
    answers with a `5xx`. The callback branches on the `error` parameter, then on
    the exchange outcome, whose discriminator the exchange-failure rule fixes as
    whether the IdP refused at the OAuth protocol level, then on the `nonce`
    comparison, and "The callback order and outcomes" names no further branch.
  - *The browser-origin evidence the request carries.* Its values are none, a
    `Sec-Fetch-Site` header whose value is other than `same-origin` or `none`,
    and an `Origin` header whose host and port differ from the host and port the
    request's own `Host` header names. "What counts as cross-site evidence" under
    "The CSRF position" defines those values and defines no other.

  **What every case asserts.** Each case asserts the cookie effects "The callback
  order and outcomes" states for its outcome, and asserts no attribute, field, or
  condition the contract or the cookie table does not carry. A refusal cell
  asserts the code the contract fixes for its condition. The `502`
  `auth.exchange_failed` cell also asserts that the envelope carries
  `retryable: false` and a non-empty `suggested_action`, which discriminates the
  staged `errorCodeRegistry` entry from its absence, because `enrichEnvelope`
  returns immediately for an unregistered code
  (`pkg/registry/server/error_envelope.go:88-92`) and leaves exactly the body a
  code-only assertion would accept, and that cell carries the
  `// Matrix: §6.10 (auth.exchange_failed)` annotation. The cells on the
  `error`-parameter value of the stub axis that the transaction cookie does not
  refuse assert that the response carries neither `auth.csrf_invalid` nor
  `auth.exchange_failed`.

  **The stub's fixture contract.** The stub issues an ID token whose `aud` is the
  OAuth client identifier and an access token whose `aud` is
  `PODIUM_OAUTH_AUDIENCE`, and its token endpoint refuses an exchange presenting
  a `code_verifier` other than the one matching the PKCE challenge the sign-in
  redirect carried. Both are what make the binding cases discriminating: without
  the differing audiences an implementation that puts the ID token in the session
  cookie passes, and without the endpoint-side check an implementation that
  stores a verifier and never sends it passes.

  **What each cell refuses to admit.**
  - The `error`-parameter value of the stub axis, taken on the absent and
    differing-`state` values of the transaction-cookie axis, is what pins the
    order "The callback order and outcomes" states, that the `state` comparison
    runs before the `error` branch. An implementation that inspects `error` first
    answers the `/ui/` redirect on those cells, which lets any third party who
    can make the victim's browser issue a request to the callback path with
    `?error=` destroy the in-flight pre-authorization transaction with no
    refusal. Each condition is a separate cell and each is driven on its own run.
  - The same value of the stub axis on a matching transaction cookie is the
    declined-consent outcome "The browser session" states takes no error code,
    and driving it on both values of the session-cookie axis is what shows the
    prior session surviving.
  - The `nonce` element, whose mint point is the `__Host-podium_auth` cookie the
    sign-in response sets and whose consume point is the `Location` header of
    that same response, fails an implementation that mints a nonce, stores it in
    the cookie, and omits it from the redirect. Against a production identity
    provider the stored nonce would be compared against an absent claim and the
    check would be either permanently failing or vacuous. The differing-`nonce`
    value of the stub axis does not reach that implementation, because the stub
    issues the ID token the fixture asks for.
  - The configuration-source axis, taken on the configured elements, fails an
    implementation that reads the device-code key, or derives the endpoint from
    the discovery document, or omits or hardcodes `client_id` or `redirect_uri`,
    and it fails it here rather than in a browser against a real IdP. A run holds
    a different value in `PODIUM_OAUTH_AUTHORIZATION_ENDPOINT` and a third in the
    issuer's discovery document, so the sources are distinguishable in one
    observation. This is the runtime half of the device-code-key rule under "The
    browser session", and the Guard bullet below pins the startup half.
  - The PKCE element fails an implementation that stores a verifier and never
    sends it, because its consume point is the exchange request the stub
    receives.
  - The OAuth-refusal value of the stub axis is the permanent arm of the
    exchange-failure rule under "The browser session", and a single
    `registry.unavailable` arm would collapse it into the transient one.
  - The token element fails an implementation that puts the ID token in the
    session cookie, and one that exchanges for an access token minted for another
    audience, each of which would otherwise fail silently in every browser. Its
    consume point is a subsequent request driven through the installed
    `oidcJWTVerifier`, which resolves the subject the stub issued rather than
    anonymous.
  - The `Sec-Fetch-Site` value of the evidence axis, taken on a matching
    transaction cookie, a successful exchange, and a session cookie already
    present, is the re-sign-in cell: the exchange completes and the response
    carries a `Set-Cookie` replacing that session cookie rather than a `403`
    `auth.csrf_invalid`. It fails an implementation that installs the evidence
    check over the callback, which is the implementation an operator hits first
    because every browser sign-in ends in that request. A redirect from the
    identity provider is a top-level navigation from that provider's origin, so a
    browser sends exactly that header value, and it is the value the CSRF
    predicate refuses. The none value of the evidence axis pins nothing on this
    route, because a request carrying neither header is admitted under the
    predicate whether or not the gate covers the route.

  **IMPLEMENTOR'S CHOICE:** which cells the rule generates are grouped into one
  run and which are driven separately, where the cases this Testing section
  describes live, and what each is named. Any answer asserts every element the
  pre-authorization transaction contract under "The browser session" states,
  together with every attribute and clearing rule the cookie table gives each
  row, including the transaction TTL at its default and at an overridden value.
  Each case is discriminating, meaning it names the implementation it fails. An
  element asserted in a case and absent from the contract or the cookie table is
  a defect in the case rather than an extension of either. Each case lives in the
  package that owns the function under test and asserts the cookie contract as
  the cookie table states it rather than restating an attribute in the test's own
  prose.
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
  needs.
  The other emitted string the rule moves is the `oidc-jwt` startup log line
  (`internal/serverboot/serverboot.go:1142`), which C2 restates. This paragraph
  is the single statement of the boot-log adjacency constraint, and every other
  site cites it by name. The restated line keeps the phrase `accepted issuers `,
  including its trailing space, immediately followed by the comma-joined
  accepted-issuer values, whose first element is the configured issuer
  (`pkg/identity/oidc_jwt.go:279-286`). Two assertions read that line.
  `test/e2e/auth_oidc_jwt_test.go:202` asserts the concatenation
  `"accepted issuers "+idp.srv.URL`, so a restatement that keeps the phrase but
  separates it from the joined list, or that moves the configured issuer out of
  first position, fails there. `:277` asserts the bare substring
  `accepted issuers` and the issuer value as independent entries, so it passes
  under any restatement that keeps the phrase. A restatement that holds the
  constraint leaves both assertions passing and requires no edit to that file.
  C2 restates the line, holds the constraint, and owns both assertions and the
  file edit they require if it does not. The manual expectations that quote the
  line verbatim are restaged under "Two expectations quote the startup log
  line". Every other site the rule moves is a comment or prose that emits
  nothing, so no test can pin it, and those are held by review against the rule,
  by the §6.10 mirror obligation, and by re-running the recorded command.
- **Any replica serves the callback (C2, integration).** Sign-in runs against one
  endpoint instance and the callback against a second that shares no state, and
  the exchange completes. This is the property the cookie-carried transaction
  buys and the one a store-backed design would have to buy back, so it is
  asserted rather than assumed.
- **Read-only (C2, integration).** With the registry in read-only mode, sign-in,
  the callback, sign-out, and the posture read all behave as they do outside it,
  an established session keeps reading, and none of them returns
  `registry.read_only`. This pins the read-only classification under "The browser
  session", which the amended §7 entry states.
- **CSRF (C2).** The cases instantiate the predicate "The CSRF position" states and
  assert nothing it does not carry. This bullet states the rule that generates
  them.

  **What a case fixes.** A case is one request driven at the gate. Its route is a
  state-changing route the gate covers that has no cookie effect of its own,
  meaning a layer write; the covered state-changing route that has one, meaning
  sign-out; or a route the exclusion names, meaning sign-in. Those are all of
  them, because the gate runs before the handler and reads nothing route-specific,
  so a route enters a case only through whether the method predicate covers it and
  through what the route itself sets or clears, and the route methods under "The
  browser session" fix the method of each route this proposal adds. Its credential
  is the `__Host-podium_session` cookie, a token in the configured token header,
  or none, which are the credentials a registry running the browser flow accepts
  and the absence of one. Its browser-origin evidence is a `Sec-Fetch-Site` value
  other than `same-origin` or `none`, an `Origin` whose host or port differs from
  the request's own `Host`, an `Origin` whose host and port match `Host` and whose
  scheme differs, `Sec-Fetch-Site: same-origin` or `none`, or neither header.
  Those are all of them, because the predicate reads two headers and each is
  absent, carries a value the predicate reads as cross-site, or carries one it
  does not, and because the scheme is the one component of an `Origin` the
  predicate does not compare. Its request-side value, where the landed mechanism
  carries one, equals `__Host-podium_csrf`, is unequal to it, is absent, or has no
  cookie to equal while the session cookie authenticates the request, which are
  the values the wire contract's server half distinguishes. Its registry has the
  browser flow enabled or disabled, and the session cookie, the CSRF cookie, and
  the request-side value exist only where it is enabled, which is what makes a
  flow-disabled case a header-authenticated one. The listener is not a coordinate:
  the registry builds a plain `http.Server` on every deployment, so `r.TLS` is nil
  in every case and the gateway-fronted arrangement is the `Origin` value above
  that differs only in scheme.

  **The outcome any point takes.** A request on an excluded route is admitted
  whatever its other coordinates, and the case asserts the route's own outcome,
  which for sign-in is the authorization redirect and a fresh `__Host-podium_auth`
  cookie. A state-changing request on a covered route is refused before the
  handler runs with `403` `auth.csrf_invalid` when its evidence is cross-site, and
  refused with the same status and code when the session cookie authenticates it
  and its request-side value is absent, unequal, or has no cookie to equal. Every
  other point is admitted and takes the route's own success. A refusal asserts
  that the handler did not run, which on sign-out is no clearing `Set-Cookie` and
  a session that still authenticates the browser on a subsequent request, and that
  is what pins the sign-out half of the §7 and CSRF predicate. The credential and
  the enablement do not appear in the outcome.

  **Which points are written.** A point is written when a case at it names an
  implementation that passes every other point and fails there. A point that names
  no such implementation is illustrative and is not required, which is what closes
  this set. The implementations the axes generate are the one that reads a
  coordinate the rule ignores, meaning a gate scoped to the session cookie, a gate
  conditional on the browser flow, a gate that compares the `Origin` scheme, and a
  gate installed over sign-in; and the one that ignores a coordinate the rule
  reads, meaning no gate, a gate reading `Sec-Fetch-Site` alone, a gate reading
  `Origin` alone, and a gate that omits the request-side comparison. Two of these
  compose, so each cross-site evidence value is driven under the session cookie
  and again under the token header, because a gate that both scopes to the cookie
  and reads one header alone passes every point that ranges only one of the two.
  Enablement composes with nothing further, so one refusing point with the flow
  disabled discharges it. On the admitting side the same criterion writes the
  session-authenticated layer write carrying the proof the mechanism requires,
  meaning a same-origin `Origin` or the `X-Podium-CSRF` header matching
  `__Host-podium_csrf`; the same write carrying an `Origin` that differs from
  `Host` only in scheme; and the token-header write carrying no `Origin`, no
  `Sec-Fetch-Site`, and no CSRF cookie, whose refusal would break every CLI and
  SDK writer once the flow is enabled. A gate with no admitting point is
  indistinguishable from one that refuses everything. The scheme point constructs
  its `Origin` explicitly, because an in-process listener makes the request scheme
  and the `Origin` scheme agree by themselves; against a scheme-comparing
  implementation it returns `403` `auth.csrf_invalid`, which is what every panel
  write on an `https` deployment would do. The admitting write builds its request
  itself, so it pins that the server gate admits a correctly proved write and
  asserts nothing about whether the panel sends the proof; the client half is
  pinned by the U1 Surfaces bullet below. The sign-in point is driven at each
  cross-site evidence value, because a sign-in carrying neither would be admitted
  whether or not the gate covers the route and would pin nothing. An
  implementation that installs the evidence check over sign-in still admits a
  sign-in entered by direct navigation or navigated from the panel's own origin,
  which carry `Sec-Fetch-Site: none` and `same-origin`, so the recovery an expired
  session takes remains open. The unrecoverable outcome is the callback's, and the
  Routes case pins it there.

  **What the rule does not reach.** The callback is an excluded route whose
  refusals come from the single-use pre-authorization transaction rather than from
  the gate, so its points are the Routes bullet's. The sibling-host forgery is not
  a point in this product: a host under the registry's registrable domain that
  plants the CSRF cookie and echoes the planted value sends a request the server
  cannot distinguish from a value it issued, because a stateless double submit
  carries nothing to distinguish, and the control is that cookie's `__Host-`
  prefix. Its case asserts the CSRF cookie's `Set-Cookie` against its row in the
  cookie table under "The browser session", and that assertion is what pins the
  control. The forged sign-out point, meaning a cross-site `POST` to the sign-out
  route, carries the `// Matrix: §6.10 (auth.csrf_invalid)` annotation, and it is
  the point that fails against the pre-fix design, so it is required rather than
  optional.
- **Posture read (C2, integration and e2e).** The integration cases drive
  `GET /v1/ui/session` with no credential against a registry that configures no
  identity provider, one in public mode, and one under `oidc-jwt` with the
  browser flow enabled, and assert the whole body on each against "The posture
  read", including that no field that statement does not carry is present. Two
  further cases drive the enabled deployment with a valid session cookie and with
  a session cookie past the token's `exp`, which are the subject-present and
  subject-absent arms of the same statement, the second being the resolver's
  fail-closed arm. On the enabled deployment `sign_in_path` and `sign_out_path`
  are asserted against the paths the mux registers rather than against literals,
  which is what keeps the UI from spelling a path the mux does not serve. One
  end-to-end case asserts through the binary that a registry
  started with `--web-ui` and no browser flow answers the read with
  `browser_auth.enabled` false, and that a registry started without `--web-ui`
  answers `404` on the path. Both binaries configure no identity provider, which
  is the stack fact that fixes the status, per "The status an unregistered path
  receives" under "The browser session". Without these an implementation that
  omits the read passes every other case here while leaving the UI unable to
  offer sign-in.
- **Guard (C3, unit + e2e).** A table with one refused case per conjunct of the
  startup guard under "The browser session": the flow enabled with the web UI
  disabled; under `oidc-jwt` with each
  acquisition value in turn left empty, which is the client identifier, the
  client credential, the redirect URI, the authorization endpoint, and the token
  endpoint; with `PODIUM_OAUTH_AUTHORIZATION_ENDPOINT` set and
  `PODIUM_WEB_UI_OAUTH_AUTHORIZATION_ENDPOINT` empty, which fails on the same
  conjunct as the empty-authorization-endpoint row above, under the
  device-code-key rule under "The browser session"; with a
  redirect URI that is neither an
  `https` URL nor a loopback `http` URL, which the redirect-URI conjunct under
  "The browser session" refuses; alongside `trusted-headers`; alongside
  `injected-session-token`; and with no identity provider selected, which is also
  the public-mode-with-no-provider case and fails on the `oidc-jwt` conjunct.
  Each asserts `config.web_ui_auth_unconfigured` naming the failed conjunct.
  One further case pins the ordering rather than the new code: the flow enabled
  alongside public mode and `oidc-jwt` fails with `config.public_mode_with_idp`.
  The ordering it pins is stated by the guard's ordering under "The browser
  session", and the
  case exists so that an implementation placing the new guard ahead of the
  shipped exclusion fails here. The table carries no case in which the guard
  names the public-mode conjunct, because no configuration produces one. The
  accepting cases are the flow enabled with every conjunct satisfied, once with
  an `https` redirect URI and once with a loopback `http` one, which are the two
  forms that conjunct admits. The combinations that
  enable no browser flow all pass, including the shipped web-UI-only
  configuration, meaning `--web-ui` alone (`cmd/podium/serve.go:38`,
  `internal/serverboot/serverboot.go:1826`). One representative refusal
  runs through the binary for the exit code and the error envelope.
- **Route mount predicate and configuration surface (C2 and C3, e2e).**
  `TestServe_WebUIAuthRouteMount` in
  `test/e2e/server_flag_behavior_test.go`, beside the existing web-UI cases. The
  cases are the points of a product, and the rule below generates them.

  Each case starts one binary at one point of three axes. The first is
  browser-flow enablement, off or on, with the web UI enabled throughout: the
  mount is a nested check inside the block that already serves `/ui/`
  (`cmd/podium/serve.go:38`, `internal/serverboot/serverboot.go:1229`,
  `internal/serverboot/serverboot.go:1826`), and the flow enabled with the web UI
  disabled does not boot, per the mount predicate and the startup guard under
  "The browser session". The second axis is the carrier of the two keys that
  carry both forms, the enablement boolean and the transaction TTL, which is the
  environment variable or the `podium serve` flag, per the key-placement rule
  under "Where configuration keys go"; the acquisition values carry a variable
  and no flag, so the axis does not range over them and every point supplies them
  as variables. The third axis is the transaction TTL, left at its default or set
  to a value other than the default. The identity provider follows from the
  first axis rather than forming one of its own: a point with the flow on
  configures `oidc-jwt`, which the guard requires, and a point with the flow off
  configures no identity provider, which is the stack fact that fixes the status
  of a path the registry does not register, per "The status an unregistered path
  receives" under "The browser session".

  Each point is observed on three things: whether any authentication route path
  answers, driven by the method the route methods under "The browser session"
  fix; the sign-in `Set-Cookie` `Max-Age` where the sign-in route answers, which
  the cookie table under "The browser session" fixes as the configured
  transaction TTL; and, at a point with the flow off, what a stale
  `__Host-podium_session` cookie resolves to, which is anonymous rather than an
  authenticated subject. A point is written as a case when it is the first to
  establish the value of an observation. At a flow-off point the carrier and TTL
  axes carry no observation, because neither key is set there, so that arm
  collapses to one binary, which is the shipped posture. On the flow-on arm the
  two carriers are separate points because each reaches the field through
  different code: the flag point pins flag registration and the flag-to-field
  assignment in `podium serve`, which no environment point reaches.

  A case at a flow-off point asserts that no authentication route answers, and
  that assertion holds on every stack the flow-off arm runs. A case at a flow-on
  point asserts that the sign-in route answers, per the first observation above.
  A case that asserts a status names the identity configuration of the
  stack it runs on and asserts the status that paragraph gives that stack, which
  for a stack configuring no identity provider is `404`. The shipped `podium
  serve` flag assertion is a fixed literal list
  (`test/e2e/cli_reference_test.go:261`) and reaches neither new flag, so no
  point exercises it.

  The product is closed by the guard. The configuration that would discriminate a
  conjunction from a disjunction is the flow enabled with the web UI disabled, it
  does not boot, and the Guard case covers that refusal. That is what the mount
  predicate stated under "The browser session" buys. This is the predicate S45
  step 4 depends on, and it lives in the boot wiring, so every point is asserted
  through the binary, which is the level `.claude/rules/test-coverage.md`
  requires for a CLI and boot-path change.
- **Build (B1).** Rebuilding the bundle produces no working-tree diff. `go build
  ./...` succeeds with no Node toolchain present.
- **Served bundle (B1, e2e).** A binary started with `PODIUM_WEB_UI=true`
  returns the bundle's `index.html` from `GET /ui/`, and every script and
  stylesheet URL that index references returns `200` from the same running
  binary. This is the level B1 declares and the one that catches an asset base
  path the mount does not serve, which no file-system read of the embedded set
  can catch.
- **Sanitization (U1).** These cases verify the sanitization rule under
  "Rendering untrusted content", and that rule fixes the case set. Each clause of
  it that admits a payload carries one case: the smallest construct the clause
  forbids, delivered through the surface the clause governs, asserting that the
  forbidden form does not survive rendering. The clause admitting no executable
  node takes an element the browser would execute, and the clause admitting no
  event-handler attribute takes such an attribute on an element. The allowlist
  clause takes a link carrying a scheme the rule names as refused, which is one
  case for `javascript:` and one for `data:`. The clause fixing the sanitizer's
  input takes a markdown construct the renderer emits as raw HTML, and which
  construct that is follows from the renderer the implementation adopts. Every
  one of those payloads is delivered as an artifact body and renders through the
  sanitized rendering path. The clause holding frontmatter outside that path
  takes a frontmatter value carrying markup, which renders as literal text in the
  property table rather than as an element. Each case asserts the absence its
  clause states, and what the sanitizer leaves in place of a removed node,
  attribute, or URL is the implementation's, because the sanitizer is an
  IMPLEMENTOR'S CHOICE. The case for the sanitizer's input discriminates a
  sanitizer wired to the rendered output from one wired to the markdown source,
  because a source-wired sanitizer passes every other case here and fails that
  one. The clause scoping the `dangerouslySetInnerHTML` check carries no payload,
  because the bullet below verifies it mechanically in CI. These are the deny
  paths of a fail-closed control this change introduces, so they are required
  rather than covered by the well-formed rendering a Render cell of the
  generating rule under "Verification matrix" asserts.
- **No unsanitized markup (B1).** The `dangerouslySetInnerHTML` check under
  "Rendering untrusted content" is the verification. It runs as a step in the CI
  job that also runs the rebuild-is-clean check, so this item adds no case to the
  test suite and the suite asserts nothing about the attribute.
- **Surfaces (U1).** Per the generating rule under "Verification matrix" above,
  covering the browser-driven cells it produces, driven through the UI's own API calls
  rather than through the CLI. Where the mechanism C2 lands carries a
  request-side value, the layer panel's Write cell is issued by the panel's own
  client code and asserts that the outgoing request carries the header the wire
  contract under "The CSRF position" fixes, read from the cookie that contract
  names, so a UI that omits the header fails this case rather
  than only failing in a browser. This is the client half of the gate, and no
  C2 case reaches it, because every C2 case constructs its own request.
  The posture read's cells are driven at this level as well, one case per row of G1's
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
and its bind stays `127.0.0.1:8153`, which is a loopback `http` origin and is
admitted by the redirect-URI conjunct under "The browser session".

**S45 step 2's negative clause moves.** Step 2 greps
`docs/reference/http-api.md` and `deploy/runbook.md` for the read-only write set,
expects both to enumerate ingest webhooks, layer admin operations, freeze
toggles, admin grants, and tenant management, and expects neither to name "token
issuance, a login endpoint, or a session table"
(`test/manual-validation.md:4197-4203`). The write-set enumeration is unaffected:
the read-only classification under "The browser session" adds no member to the
§13.2.1 write set, so no write joins the set and the Expect block's positive list
stays complete. The negative clause
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
Expect block asserts. A stack that does mount them would not return
`registry.read_only` either, per the read-only classification under "The browser
session".

**S45 step 4 moves.** It probes `/v1/login`, `/v1/auth/token`, and `/v1/token`
and expects 404 "because the registry registers no auth, login, or token route".
The new routes falsify the stated reason, and an implementor who mounts one of
the probed paths turns the step into a failure. It is rewritten to probe the
registry's authentication route paths and to expect `404` on each with the
reason the mount predicate and the stack give: this stack enables neither the web
UI nor the browser flow, so the registry registers none of those routes, and it
configures no identity provider, so nothing refuses the probe ahead of route
matching. Both conjuncts are stated, per "The status an unregistered path
receives" under "The browser session". It keeps what
stays true: the clause struck by proposal 0012 named a write endpoint the
registry does not serve.

**Two expectations quote the startup log line.** S36
(`test/manual-validation.md:2642-2643`) and S44 step 4 (`:4085-4086`) quote the
`oidc-jwt` startup log line verbatim, and C2 restates that line under the
credential-location rule under "The browser session", subject to the boot-log
adjacency constraint in "restated remediation and message strings". Both
expectations are restaged to quote the restated line, and T1 carries them.

**S36's §6.3.3 restatement moves with them.** The scenario's preamble
(`test/manual-validation.md:2482-2484`) restates the §6.3.3 sends-no-credential
sentence without the "behind such a gateway" qualifier its authoring source
carries, so it reads as a claim about every `oidc-jwt` registry, and it closes
the acquisition path in the same sentence. S3 restates the source and D1 restates
the shipped mirrors, so T1 restates this one on the same axis, under the
credential-location rule under "The browser session". The scenario's own
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
  access. The UI reads those endpoints as an SDK would, and the only read this
  proposal adds for it is `GET /v1/ui/session`, which "The posture read" under
  "The browser session" specifies. The sign-in, callback, and sign-out routes
  this proposal adds are navigation targets of the browser flow rather than
  endpoints the UI reads.
- Server-side filtering of `GET /v1/layers`. The unfiltered-list rule under "The
  layer-ownership defect" states the read this proposal leaves as it is.
- A server-side session record, a session table, or a session store. The
  no-session-state rule under "The browser session" is the single statement of
  what the registry holds instead and why.
- Silent token refresh, and revocation before the token's `exp`. "Revocation is
  expiry" under "The browser session" states the model.
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

- The registry keeps no session state. The draft carried a session store, a
  §13.1 topology entry, shared session state, and cross-replica revocation. The
  cookie carries the credential §6.3.3 already accepts rather than one the
  registry mints, and the no-session-state rule under "The browser session" is
  the single statement of what that left.
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
seams leak on every pass that touches them. The cookie contract, the
credential-location rule, and the no-session-state rule were each given a single
normative statement and went quiet afterwards. The remaining duplication is
recorded in "Watch out for".

## Relationship to proposal 0012

0012 corrected §13's account of what the registry accepts and does. Its decision
3 verified that the shipped SPA attaches no credential, narrowed the web-UI
paragraph to state that plainly, and routed in-browser authentication here. The
sentence S1 amends is the one 0012 wrote, and the page
`docs/deployment/gateway-delegated-identity.md` names as this proposal's
obligation is the one 0012 recorded.
