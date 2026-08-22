# Proposal 0013: Build the §13.10 web UI

- Issue: (to be filed)
- Status: Draft
- Date: 2026-08-22

This document stages the proposed spec, code, test, and documentation changes.
It does not modify any spec, code, or doc file. Apply the changes in the edit
sites and testing sections after sign-off.

## Summary

**What changes.**

- The web UI is rewritten in React and gains the three §13.10 surfaces it does
  not have: search filters, an artifact viewer that renders markdown and a
  frontmatter property table with links to related artifacts, and a layer panel.
- The browser signs in through the registry. The registry performs the OAuth
  code exchange server-side and issues an `HttpOnly` session cookie, so no token
  is reachable from JavaScript. This adds sign-in, callback, and sign-out routes,
  a third accepted credential, and shared session state.
- The layer write handlers gain the owner authorization §7 already specifies and
  the code does not implement. Today any caller can delete or rewrite another
  user's user-defined layer.
- The built React bundle is committed to the tree so `go build` and `go install`
  keep working from a clean clone with only a Go toolchain, with a CI check that
  rebuilding produces no diff.

**Fixed decisions.**

- **Authentication is registry-mediated.** The browser never holds a token. The
  alternative of a browser-held token under a pure-SPA flow is withdrawn: this
  proposal newly renders author-controlled markdown on the same origin, and a
  token reachable from JavaScript on that origin is the combination the chosen
  route removes.
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
- **The browser flow is not a new `PODIUM_IDENTITY_PROVIDER` value.** It is a
  browser acquisition path available under `oidc-jwt`, gated by its own
  enablement key, which leaves the registry's accepted provider values as §13.12
  records them (`spec/13-deployment.md:468`).
- Artifacts stay authored in git. The UI is a reader and a layer manager.

**Watch out for.**

- **CSRF is a first-class obligation of this route, not a detail.** A cookie
  authenticates automatically on any request the browser is induced to make, so
  every layer write becomes forgeable across origins in a way a Bearer token
  never was. The prior review of this proposal never produced a finding on it
  across eight rounds while treating it as acknowledged prose, which is how a
  known gap stays open. "The CSRF position" below specifies it.
- **Closing the ownership gap changes the authorization behavior of every layer
  write handler**, not only the ones the panel calls.
  `test/integration/reingest_pipeline_test.go:87` posts to reingest with no
  credential today and passes, so it fails once reingest authorizes. That test is
  part of the deliverable.
- **The key-placement rule is stated once**, under "Where configuration keys go".
  It is easy to restate divergently, because §6.3, §13.10, and §13.12 each look
  like the right home and only one of them is.
- **`GET /v1/layers` is unfiltered.** The panel's role split is presentation over
  a list the server does not scope, so a design that implies otherwise
  misrepresents it. The server-side gate this proposal adds is on writes.
- **A session that ends is a write to shared state**, so a read-only registry
  rejects it. Sign-in and sign-out therefore have a §13.2.1 classification, and
  the operator-facing consequence is that an established session keeps reading
  while a new sign-in is refused.

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1. §13.10's authentication paragraph, bind-guard
      rationale, and web-UI configuration keys, per "The edit sites".
      Levels: —. Depends on: —
- [ ] **S2 · spec** — SPEC-2. A new §6.3.4 stating the browser acquisition flow,
      with its pointer from the §6.3 introduction.
      Levels: —. Depends on: S1
- [ ] **S3 · spec** — SPEC-3. §6.3.3's third accepted credential and §6.3.1's
      browser-session organization source.
      Levels: —. Depends on: S2
- [ ] **S4 · spec** — SPEC-4. §7's sign-in, callback, and sign-out routes;
      §13.2.1's classification of the session writes; §13.1's topology entry for
      the session store.
      Levels: —. Depends on: S2
- [ ] **S5 · spec** — SPEC-5. §11's verification entry for the UI, covering the
      surface-by-obligation matrix below.
      Levels: —. Depends on: S1, S2, S3, S4
- [ ] **C1 · code** — CODE-1. Owner authorization on the layer write handlers,
      with its `403` tests and the reingest integration-test credential.
      Levels: unit, integration. Depends on: —
- [ ] **C2 · code** — CODE-2. The session store, the sign-in, callback, and
      sign-out routes, and the CSRF position below.
      Levels: unit, integration, e2e. Depends on: S3, S4
- [ ] **C3 · code** — CODE-3. The web-UI authentication configuration guard.
      Levels: unit, e2e. Depends on: C2
- [ ] **B1 · code** — BUILD-1. The React toolchain, the committed bundle, the
      `go:embed` change, and the rebuild-is-clean CI check.
      Levels: unit, e2e. Depends on: —
- [ ] **U1 · code** — UI-1. The four surfaces built against `web/DESIGN.md`.
      Levels: unit, e2e. Depends on: B1, C1, C2
- [ ] **D1 · docs** — DOC-1. Every shipped mirror named in "The edit sites".
      Levels: —. Depends on: S1, S2, S3, S4
- [ ] **T1 · test** — TEST-1. The manual scenarios, including the S44 rewrite and
      the S45 step-4 rewrite.
      Levels: —. Depends on: U1

## The gap

§13.10 specifies four web-UI surfaces (`spec/13-deployment.md:164-168`). The
implementation provides roughly one and a half.

| Specified | Built |
|:--|:--|
| Domain browser matching `load_domain`'s structure | yes |
| Search with the same `type` / `scope` / `tags` filters as the SDK and CLI | free-text query only |
| Artifact viewer: body as markdown, frontmatter as a property table, links to extending or dependent artifacts | no; both rendered as raw `<pre>`, no links |
| Layer panel: layers with source, visibility, and `last_ingested_at`; admins register, reingest, and unregister; users manage their own layers under the §7.3.1 cap | absent |

The SPA is 162 lines: `web/app.js` 129, `web/index.html` 20, `web/style.css` 13.
It is vanilla JavaScript with no build step, embedded with `go:embed`
(`web/web.go:12`) and served at `/ui/` by a plain `http.FileServer` with no
middleware (`internal/serverboot/serverboot.go:1229`).

The UI appears in neither §10's build sequence nor §11's verification list, and
no test cites §13.10 for a UI surface. That is how a four-surface specification
and a one-and-a-half-surface implementation coexisted without anything failing,
and it is why S5 creates the verification obligation as well as satisfying it.

## The layer-ownership defect

This is a fail-open divergence from spec, and it is closed here because the layer
panel is the surface that exposes these operations to a browser.

§7 specifies owner authorization: manual reingest is "(admin or layer owner)"
(`spec/07-external-integration.md:65`). The handlers implement neither half.

`unregister`, `update`, `restore`, and `reorder` gate on `!cfg.UserDefined`
alone (`pkg/registry/server/layers.go:856`, `:494`, `:819`, `:905`), so
authorization runs only for admin-defined layers. When the layer is
user-defined, no check runs and any caller receives `200`. The comment above one
of them states the intended rule, "a user-defined layer belongs to its
registrant (§4.7.2)", and the code does not implement it. `reingest` calls no
authorization function at all.

The only owner comparisons in the file are the §7.3.1 cap count (`:680`) and the
§8.5 erase filter (`:419`). Neither authorizes a write against the caller.

**What lands.** An owner gate on `unregister`, `update`, `restore`, and
`reorder`, and an admin-or-owner gate on `reingest`, each returning `403`
`auth.forbidden`, with tests asserting the refusal.
`test/integration/reingest_pipeline_test.go:87` posts to reingest with no
credential and passes today, so it moves with the change.

**IMPLEMENTOR'S CHOICE:** whether the owner comparison reads the caller's subject
through the same helper the cap count uses or through the request-identity
accessor the admin gate uses. Any answer compares against the verified subject
rather than a client-supplied field, returns `403` `auth.forbidden` rather than
`404`, and leaves the admin path able to act on any layer in the tenant.

## The spec amendment

Three of the four surfaces need no spec change: §13.10 specifies the domain
browser, the search filters, and the artifact viewer in enough detail to build
against, and their endpoints exist. The amendment is the authentication story,
and it is larger than a single sentence.

### Where configuration keys go

Stated once here; every edit site below refers to this rule rather than
restating it.

§6.3 documents options per identity provider, not per consumer: the `Options:`
list sits on the provider bullet (`spec/06-mcp-server.md:42`) and the CLI
sub-bullet at `:46` carries none of its own. So the browser flow's **acquisition**
options go on the new §6.3.4 entry's own `Options:` list.

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
  anonymous. Rewritten to state what the UI now does. The first half stays
  literally true of the device-code flow, because this is an authorization-code
  flow; what changes is that it is no longer a complete account.
- **§13.10, `spec/13-deployment.md:172`** — the bind guard, whose stated
  rationale is "preventing accidental exposure of an unauthenticated UI".
  Restated to match what the guard achieves once the UI can authenticate. This
  sentence is the source the code comment and the docs mirrors below follow.
- **§13.10** — the web-UI configuration keys, per the rule above, and a sentence
  stating the configuration guard below, beside the bind-guard sentence.
- **§6.3, a new §6.3.4** stating the browser acquisition flow, placed after
  §6.3.3, which ends at `spec/06-mcp-server.md:114` immediately before §6.4 at
  `:116`, with a pointer from the §6.3 introduction at `:40`. It is not a fourth
  sub-bullet under the `oauth-device-code` bullet's list (`:44-47`), which is
  scoped to the device-code flow.
- **§6.3.3 (`spec/06-mcp-server.md:92-112`)** — today it enumerates two accepted
  credentials, the gateway-forwarded `Bearer <token>` under `oidc-jwt` (`:96`)
  and the injected `X-Podium-User-*` headers under `trusted-headers` (`:108`).
  The registry now accepts a third, a browser session, so the section states what
  that session is and what the registry verifies about it. Its own restatement at
  `:94` is inside that range.
- **§6.3.1 (`spec/06-mcp-server.md:52-65`)** — per-request tenant selection at
  `:64` carries the same closed enumeration on the axis that decides which tenant
  serves a request. A session-authenticated browser request carries neither the
  verified `org_id` claim nor `X-Podium-User-Org`, so the sentence states where a
  browser session's organization value comes from and what happens when it
  resolves to no tenant.
- **§7** — the sign-in, callback, and sign-out routes, alongside the
  operator-level endpoints §7.3.3 enumerates
  (`spec/07-external-integration.md:152`). Sign-in mints and persists the state,
  nonce, and PKCE verifier and redirects to the IdP; the callback reads them back
  and exchanges the code server-side; sign-out ends the session in shared state
  so every replica refuses it, and clears the `HttpOnly` cookie the browser
  cannot clear itself.
- **§13.2.1 (`spec/13-deployment.md:41`)** — ending a session writes shared
  registry state, so a read-only registry rejects it with `registry.read_only`.
  The section states the classification of sign-in and of sign-out.
- **§13.1 (`spec/13-deployment.md:5`)** — the session store as a topology
  component.
- **§11** — the verification entry, covering the matrix below.

**IMPLEMENTOR'S CHOICE:** the path of each authentication route. Any answer
places them under the existing `/v1/` prefix, uses one path per route, and
appears identically in the §7 entry, in `docs/reference/http-api.md:13-19`, in
the mux registration, and in the S45 step-4 rewrite, so the paths that scenario
probes match the mux.

**Shipped documentation mirrors.** Each restates spec text this amendment
changes, so each moves with it.

| Mirror | What it restates |
|:--|:--|
| `docs/deployment/gateway-delegated-identity.md:105-107` | the §13.10 web-UI account; 0012 recorded this page as this proposal's obligation |
| `docs/deployment/gateway-delegated-identity.md:97` | §6.3.1's tenant-routing enumeration, verbatim in scope |
| `docs/deployment/oidc/index.md:67` | the same axis for `oidc-jwt` tokens |
| `docs/reference/error-codes.md:58` | `auth.tenant_unknown`, scoped to a verified `oidc-jwt` token's `org_id` |
| `docs/reference/error-codes.md:69` | the guard's neighbour, `config.web_ui_public_bind_refused` |
| `docs/reference/http-api.md:13-19` | the Authentication section and its route list |
| `docs/reference/http-api.md:290` | the register-response example, which prints snake_case keys for a response emitting Go field names |
| `docs/reference/http-api.md:633`, `docs/deployment/operator-guide.md:132`, `deploy/runbook.md:19` | the §13.2.1 write set, each as a closed parenthetical the session write joins |

## The CSRF position

A session cookie authenticates automatically on any request the browser can be
induced to make, so every layer write this proposal exposes becomes forgeable
across origins. A Bearer token was not, which is why the risk arrives with the
route rather than with the panel.

The position is specified here rather than left to the implementor, because the
prior review treated it as acknowledged prose for eight rounds and never
produced a finding on it.

- Every state-changing request authenticated by a session cookie carries a CSRF
  token the server verifies, and a request without a valid one is refused before
  the handler runs.
- The cookie is `HttpOnly`, `Secure`, and `SameSite` at the strictest setting the
  sign-in redirect permits. `SameSite` is a defence in depth here rather than the
  control, because the OAuth callback is itself a cross-site navigation.
- The pre-authorization transaction, meaning the state, nonce, and PKCE verifier
  the sign-in route mints, is bound to the browser and single-use, so a callback
  replayed or delivered to a different browser is refused.
- Sign-out is itself state-changing and carries the same protection, because a
  forged sign-out is a denial of service against a signed-in operator.

**IMPLEMENTOR'S CHOICE:** the CSRF mechanism, whether a synchroniser token, a
double-submit cookie, or an origin check paired with one of them. Any answer
refuses a state-changing request that carries a session cookie and no valid
proof of same-origin intent, is verified before the handler runs rather than
inside it, and is asserted by a test that forges the request.

## Rendering untrusted content

Artifact bodies are markdown authored by whoever can write to a layer's source,
and the UI now renders them rather than showing them as preformatted text. That
turns author-controlled content into markup on the registry's own origin, which
is the origin the session cookie is scoped to.

- Rendered markdown is sanitised, and the sanitiser runs on the rendered output
  rather than on the source, so a construct that survives the markdown renderer
  cannot bypass it.
- Frontmatter is rendered as a property table with values escaped as text. It is
  not markdown and is not rendered as such.
- The repository carries no `dangerouslySetInnerHTML` outside the single
  sanitised rendering path, checked mechanically rather than by review.

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
- `web/web.go`'s embed directive names the built bundle.
- A CI step rebuilds the bundle and fails if the working tree differs, which is
  what makes the committed output trustworthy rather than merely present. This is
  part of the deliverable.
- A `.gitattributes` entry marks the bundle generated so review diffs collapse.
  The repository has none today.

**IMPLEMENTOR'S CHOICE:** the bundler and the output path. Any answer produces
deterministic output so the rebuild check is stable, keeps `go build ./...`
working with no Node toolchain present, and leaves the built bundle the only
generated artifact in the tree.

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

## Verification matrix

§11 requires nothing of the UI today. S5 states the obligation, and this matrix
is what it enumerates, so coverage is checked per surface rather than per test
that happens to be written.

| Surface | Read | Write | Error | Render |
|:--|:--|:--|:--|:--|
| Domain browser | anonymous and authenticated views differ | — | unreachable registry | nested and folded entries |
| Search | filters reach the endpoint | — | no results | score and sensitivity present and absent |
| Artifact viewer | authenticated sees what anonymous does not | — | not-found for invisible artifact | sanitised markdown, property table, related links |
| Layer panel | list is unfiltered by the server | owner gate refuses `403` | `registry.read_only` across the panel | one-time secret, destructive confirmation |
| Session | established session reads | sign-in and sign-out write | expiry mid-page | — |

## Testing

- **Owner authorization (C1).** A non-owner receives `403` `auth.forbidden` from
  `unregister`, `update`, `restore`, and `reorder`; an owner succeeds; an admin
  succeeds on any layer. Reingest refuses an unauthenticated caller, which is the
  assertion `test/integration/reingest_pipeline_test.go:87` currently contradicts.
- **Session (C2).** Sign-in issues a session; the callback refuses a replayed or
  cross-browser state; sign-out ends the session so a second replica refuses it;
  an expired session is refused mid-request.
- **CSRF (C2).** A forged state-changing request carrying a valid session cookie
  and no valid proof is refused before the handler runs. This test is the one
  that would fail against the pre-fix design, so it is required rather than
  optional.
- **Tenancy (C2).** A browser session on a multi-tenant registry resolves the
  organization the staged §6.3.1 sentence names, and a session naming no
  provisioned tenant is refused with the code that sentence states.
- **Read-only (C2).** With the registry in read-only mode, an established session
  keeps reading and a new sign-in is refused with `registry.read_only`.
- **Guard (C3).** Enabling the UI and the browser flow without its configuration
  fails startup with `config.web_ui_auth_unconfigured`, and the unaffected
  combinations still start.
- **Build (B1).** Rebuilding the bundle produces no working-tree diff. `go build
  ./...` succeeds with no Node toolchain present.
- **Surfaces (U1).** Per the matrix above, driven through the UI's own API calls
  rather than through the CLI.

## Manual validation

**S44 moves, and it is the test of a convention.** `test/manual-validation.md`
S44 pins the current anonymous behavior and carries a "Known gap this records"
paragraph stating that in-browser authentication is deferred to its own proposal,
written so a later change to the UI has to move that text. This is that change.
S44 is rewritten to assert the authenticated behavior, and its known-gap
paragraph is struck rather than left asserting a deferral that has happened.

**S45 step 4 moves.** It probes `/v1/login`, `/v1/auth/token`, and `/v1/token`
and expects 404 "because the registry registers no auth, login, or token route".
The new routes falsify the stated reason, and an implementor who mounts one of
the probed paths turns the step into a failure. It is rewritten to probe the
paths the registry now serves and to state what each returns, keeping what stays
true: the clause struck by proposal 0012 named a write endpoint the registry does
not serve.

**New scenarios.** A sign-in through the UI that yields a view an anonymous
caller does not get; the layer panel's register flow including the one-time
secret; an unregister with its confirmation; and a non-owner attempting a
destructive operation and being refused. Each names what a human reads on screen,
which is the class no Go test covers, as S44 already established for the
anonymous case.

## Non-goals

- Authoring or editing artifacts through the UI.
- Any admin surface beyond the layer panel.
- Changing the endpoints the UI calls, or giving it privileged access.
- Server-side filtering of `GET /v1/layers`. The panel's role split is
  presentation over an unfiltered list; the gate this proposal adds is on writes.
- The SDK half of the `DeviceCodeRequired` gap, a separate §6.3 client surface.
- Any change to `oauth-device-code`, `injected-session-token`, or the startup
  identity guard.

## Relationship to proposal 0012

0012 corrected §13's account of what the registry accepts and does. Its decision
3 verified that the shipped SPA attaches no credential, narrowed the web-UI
paragraph to state that plainly, and routed in-browser authentication here. The
sentence S1 amends is the one 0012 wrote, and the page
`docs/deployment/gateway-delegated-identity.md` names as this proposal's
obligation is the one 0012 recorded.
