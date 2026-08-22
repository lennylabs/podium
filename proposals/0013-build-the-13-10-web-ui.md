# Proposal 0013: Build the §13.10 web UI

- Issue: (to be filed)
- Status: Draft
- Date: 2026-08-22

This document stages no deliverable ids yet. It records what §13.10 specifies,
what the implementation provides, the spec amendment building the rest requires,
and the edit sites that amendment reaches, so a staging run creates the
deliverables rather than rediscovering the analysis. The adversarial review loop
has begun, and the passes it has completed are recorded under "Resolved in
adversarial review".

## Summary

**What changes.**

- The three incomplete §13.10 surfaces are built out in `web/`: the search
  filters (`type`, `scope`, `tags`), the artifact viewer with rendered markdown,
  a frontmatter property table, and links to extending or dependent artifacts,
  and the layer panel, which does not exist today. The domain browser's behavior
  is unchanged, and the React rewrite replaces its implementation, so it gains
  the test it does not have today.
- The artifact viewer renders user-authored markdown, so the build carries the
  sanitization rules stated under "Rendering untrusted content" and the tests
  that pin them.
- The browser gains an authentication flow, by one of the two routes in decision
  1. Route A2 also adds the sign-in initiation, callback, and sign-out routes
  that "The spec amendment" enumerates, and a session concept, to the registry
  under `internal/serverboot` and `pkg/registry/server`.
- Enabling `--web-ui` and the browser sign-in flow without the chosen route's
  remaining browser authentication keys is refused at startup with
  `config.web_ui_auth_unconfigured`, the guard specified under "The spec
  amendment". A gateway-delegated deployment, which enables no browser sign-in
  flow, keeps booting and keeps the §13.10 inherited-identity behavior.
- `spec/` gains the amendment enumerated under "The spec amendment": the §13.10
  sentences at `spec/13-deployment.md:170` and `:172` are rewritten, §6.3 gains
  a new §6.3.4 subsection for the browser acquisition flow carrying the
  browser's acquisition options, §13.10 gains the registry-process keys the
  chosen route introduces beside `PODIUM_WEB_UI` and the configuration-guard
  sentence, and §11 gains a verification entry for the UI. The §13.12 identity
  table gains no row, because its introducing sentence scopes it to the keys the
  gateway-delegated and injected-session-token providers introduce. The route
  decision 1 settles brings further sections with it, which that section
  enumerates per route.
- `docs/` gains the edits that mirror the amended sentences, enumerated under
  "The spec amendment": the "Web UI" section of
  `docs/deployment/gateway-delegated-identity.md:105-107`, a
  `docs/reference/error-codes.md` row for the new configuration code, the
  register-response example at `docs/reference/http-api.md:290`, and, under route
  A2, the Authentication section of `docs/reference/http-api.md:13-19`, the
  multi-tenant routing sentence at
  `docs/deployment/gateway-delegated-identity.md:97` that mirrors §6.3.1, plus
  the read-only write set restated at
  `docs/deployment/operator-guide.md:132`, `docs/reference/http-api.md:633`, and
  `deploy/runbook.md:19`, which moves because every A2 session answer decision 1
  admits keeps the end of a session in shared registry state. The amended bind-guard rationale reaches
  `pkg/registry/server/config_validate.go:25-30` and `:99-102`,
  `docs/reference/error-codes.md:69`, `docs/reference/cli.md:154-155`, and
  `cmd/podium/serve.go:35-37` in the same sweep.
- `web/DESIGN.md` is corrected where it disagrees with what the endpoints emit:
  the layer payload's field names, the layer visibility model, and the brief's
  claim that the layer list is identity-filtered and role-dependent, which
  `GET /v1/layers` is not. The brief states that the spec wins over it
  (`web/DESIGN.md:8-9`).
- The vanilla SPA is rewritten in React, which introduces a build step whose
  output is embedded through `web/web.go` and reaches the Makefile, the release
  workflow, and CI.
- Tests are created where none exist: unit coverage of token or session expiry,
  unit coverage of the sanitization rules, end-to-end coverage of the domain
  browser's navigation and its empty-domain state, end-to-end coverage of the
  visibility difference between an authenticated and an anonymous caller, end-to-end
  coverage of the search filters, end-to-end coverage of the viewer's rendered
  markdown and its frontmatter property table, end-to-end coverage of the
  viewer's dependents call including
  the `500 registry.unavailable` arm, end-to-end coverage of the viewer's
  `/v1/load_artifact` failure arms, where a missing and an invisible artifact
  both present the same not-found state, end-to-end coverage of the layer panel's
  read surface against the field names and the §4.6 visibility union the list
  endpoint emits, end-to-end coverage of the layer panel's writes against the
  admin-defined against user-defined split the registry actually enforces on
  them, end-to-end coverage of the panel's reingest control against the result
  summary, the queue-only response, and the reingest error arms, an end-to-end
  boot test of the new configuration keys and their
  absent-key refusal, a route A2 sign-out test, a route A2 test of the §6.3.1
  tenant selection a browser session performs including its deny arm, and
  coverage of read-only mode.
- `test/manual-validation.md` S44 is rewritten, S45 moves with the route A2
  edits (both its write-set assertion and its step 4, which asserts that the
  registry serves no auth, login, or token route), and scenarios are added for
  sign-in, the register flow with its one-time secret, and unregister with its
  confirmation.

**Fixed decisions.**

- On the data plane the UI is a thin client over the existing catalog and
  `/v1/layers…` HTTP endpoints. It gains no data-plane endpoint of its own and
  no privileged access. Whether the registry gains authentication routes for the
  browser is decision 1 rather than a fixed decision.
- The domain browser, the search filters, and the artifact viewer implement
  existing spec and need no spec change. The amendment covers authentication,
  the configuration that authentication requires, and verification.
- The SPA is rewritten in React.
- Artifacts are not authored or edited through the UI, and no admin surface
  beyond the layer panel is built.
- The implementor does not design the UI. A design pass against `web/DESIGN.md`
  produces the layouts, the state treatments, and the component inventory, and
  the implementation builds what that pass produces.
- The one-time webhook secret treatment and the unregister confirmation are
  outputs of that design pass and are not settled by whoever writes the React.
- Authentication does not land on its own. It lands with the other surfaces,
  and the layer panel is what makes it proportionate.
- The SDK half of the `DeviceCodeRequired` gap is out of scope and tracked
  separately.

**Watch out for.**

- `spec/13-deployment.md:170` states that the UI resolves identity solely from
  what the request carries and sees public visibility only. It is correct today
  and becomes false the moment the UI can authenticate. Proposal 0012 wrote that
  sentence after verifying the behavior, so amending it is part of this change
  rather than a correction of an error.
- `docs/deployment/gateway-delegated-identity.md:107` restates that sentence, and
  proposal 0012 recorded the page as an obligation of this proposal
  (`proposals/0012-the-registry-does-not-accept-oauth-device-code.md:226`). Which
  of its clauses becomes false depends on the route, as "The spec amendment"
  states per route; its device-code clause stays true under both, because both
  routes are authorization-code flows. Amending §13.10 without touching the page
  ships a page that describes a UI with no acquisition flow after the UI has one.
- Rendering the manifest body as markdown removes the control that makes
  artifact content inert today. `web/app.js:29` inserts every server string as a
  text node and `web/app.js:106` puts the body in a `<pre>`, and any
  authenticated caller can register a layer whose artifacts an administrator
  then opens (`spec/07-external-integration.md:95`). "Rendering untrusted
  content" states the rules the renderer has to satisfy.
- Route A2's session and its pre-authorization state have to work on the §13.1
  reference topology, which is a stateless front-end of 3+ replicas behind a
  load balancer with no specified session affinity
  (`spec/13-deployment.md:5`). An in-process map fails there, so decision 1
  carries that constraint.
- `test/manual-validation.md` S44 pins the anonymous behavior and carries a
  "Known gap this records" paragraph placed there so a later UI change has to
  move it. Leaving S44 in place makes the manual suite assert the opposite of
  what the build does.
- §11 requires nothing of the UI and no test cites §13.10 for a UI surface. A
  green suite is therefore not evidence that any UI surface works, which is how
  a four-surface specification and a one-and-a-half surface implementation
  coexisted. This change creates the verification obligation it satisfies.
- Route A2 requires a position on CSRF, because a cookie-authenticated write is
  forgeable across origins and a Bearer-authenticated one is not. Route A1
  requires an IdP-side public client registration and CORS on the IdP token
  endpoint, neither of which is under this repository's control.
- The build-step question has the widest blast radius: `web/web.go`, the
  Makefile, the release workflow, and CI. Whether the built assets are committed
  is open, and the choice decides whether `go build ./...` alone still produces
  a working binary.
- The open UI is conditioned on having no identity provider rather than on the
  deployment mode (`spec/13-deployment.md:170`), and both gateway-delegated
  providers apply on either mode (`spec/06-mcp-server.md:92`). So the sign-in
  flow applies in standalone as well, and what the layer panel does with no
  identity provider configured is what decision 3 leaves open. With no provider
  configured the admin gate admits every caller
  (`internal/serverboot/serverboot.go:1213-1215`), so an open panel is an
  unauthenticated destructive surface on the bind address.
- `GET /v1/layers` returns every active layer in the tenant to every caller,
  anonymous included, with no admin gate and no identity or ownership filter
  (`pkg/registry/server/layers.go:772-777`,
  `internal/serverboot/serverboot.go:1220`). Any per-role scoping the panel shows
  is client-side presentation, so hiding a layer does not withhold it. The
  tenant it lists is the boot default tenant captured when the endpoint is
  constructed (`internal/serverboot/serverboot.go:1199`,
  `pkg/registry/server/layers.go:772`), and the §6.3.1 tenant router is a
  meta-tool-server option the outer-mux layer route never reaches
  (`internal/serverboot/serverboot.go:1165-1170`, `:1220`, `:1239`), which
  serverboot's own comment records at `:1194-1196`. So on a multi-tenant
  registry the panel receives that one tenant's layers for every caller
  regardless of credential, and no browser session changes it.
- The registry enforces no layer-ownership check on any write path, and
  `POST /v1/layers/reingest` enforces no authorization at all. Decision 4 records
  the constraint that follows for the panel.
- Every decision the document records is open. It records a lean toward route A2
  and settles none of the route, the asset-commit question, the no-identity-
  provider scope, or the layer-ownership gap.

## Implementation checklist

This document stages no deliverables. It records the analysis and the edit sites
so a staging run creates the changes, and no `SPEC-n`, `CODE-n`, `DOCS-n`, or
`TEST-n` ids exist yet for a step to name. The checklist stays empty until that
run stages them.

The document does record the ordering constraints those steps have to satisfy:

- The spec edits enumerated under "The spec amendment", including the additional
  sections the route decision 1 settles brings with it, land and are verified
  before the code that depends on them, per
  `.claude/rules/spec-driven-development.md`.
- The `docs/` edits land with the spec sentences they mirror, so no shipped page
  states the pre-amendment behavior after the amendment lands.
- The `web/DESIGN.md` corrections land before the design pass reads the brief.
- The React rewrite and its build step precede the surface work, because the
  surfaces are built in React and the embedding path through `web/web.go`
  changes with it.
- Decision 1 settles before the layer panel is built, because the panel performs
  destructive operations as the caller.
- Decision 4 settles before the layer panel is built, because whether the panel
  may present per-owner scoping as server-enforced depends on it, and because a
  code deliverable closing the gap would land before the panel that exposes those
  operations.
- The design pass against `web/DESIGN.md` precedes the surface work it produces
  layouts for.
- The `test/manual-validation.md` S44 rewrite lands with the authentication
  change rather than after it, and under route A2 the S45 rewrite lands with the
  routes that falsify it, so no scenario in the manual suite asserts the
  pre-change behavior.

## The gap

§13.10 specifies four web-UI surfaces (`spec/13-deployment.md:164-168`). The
implementation provides roughly one and a half of them.

| Specified | Built |
|:--|:--|
| Domain browser matching `load_domain`'s structure | yes |
| Search with the same `type` / `scope` / `tags` filters as the SDK and CLI | free-text query only; no filters |
| Artifact viewer: body as markdown, frontmatter as a property table, links to extending or dependent artifacts | no; both rendered as raw `<pre>` blocks, no links |
| Layer panel: list layers with source, visibility, and `last_ingested_at`; admins register, reingest, and unregister; users manage their own layers under the §7.3.1 cap | absent entirely |

The whole SPA is 162 lines: `web/app.js` 129, `web/index.html` 20,
`web/style.css` 13. It is vanilla JavaScript with no build step, embedded with
`go:embed` (`web/web.go:12-13`) and served at `/ui/` by a plain
`http.FileServer` with no middleware (`internal/serverboot/serverboot.go:1229`).

The UI appears in neither §10's build sequence nor §11's verification list, and
no test cites §13.10 for any UI surface. So it is specified, unscheduled, and
unverified, which is how a four-surface specification and a one-and-a-half
surface implementation coexisted without anything failing.

## What this is not

It is not a defect report about authentication. The web UI attaching no
credential was recorded as a separate finding, and it is a symptom rather than
the problem: it is one gap among four, and the surface that would most need
authentication, the layer panel with its destructive operations, is the one
that does not exist. Building authentication alone would protect a read-only
viewer that has nothing to protect.

## The spec amendment

The domain browser, the search filters, and the artifact viewer need no spec
change. §13.10 specifies them in enough detail to build against, the endpoints
exist, and this proposal implements existing spec.

The authentication story is different, in three parts.

**The current sentence contradicts the build.** `spec/13-deployment.md:170`
states that the UI "runs no acquisition flow of its own and resolves identity
solely from what the request carries", so a direct request "resolves as
anonymous, and sees public visibility only". That sentence is correct today and
becomes false the moment the UI can authenticate. It was written by proposal
0012, which verified the behavior and deliberately routed the question here.

**No browser acquisition flow is specified anywhere.** §6.3's provider list
enumerates `oauth-device-code` (`spec/06-mcp-server.md:42`),
`injected-session-token` (`:49`), and the `(Extensible.)` bullet (`:50`). The
MCP-server, CLI, and SDK entries at `:45-47` are sub-bullets of the
`oauth-device-code` bullet, introduced at `:44` by "How the verification URL
surfaces depends on the consumer", so they state where the device-code
verification URL is displayed rather than forming a per-consumer acquisition
list. No entry anywhere in §6.3 covers a browser. A sweep of `spec/` for `PKCE`,
`authorization_code`, `redirect_uri`, `sessionStorage`, and `cookie` returns
nothing.

**The registry serves no auth route.** `/v1/login`, `/v1/auth/token`, and
`/v1/token` all return 404, and the mux registers no auth, login, or token route
(`internal/serverboot/serverboot.go:1220-1239`). Whether any is added is
decision 1.

### The edit sites

The amendment's size depends on the route decision 1 settles, so the site list
is stated per route rather than as a single count.

Both routes:

- §13.10, the sentence at `spec/13-deployment.md:170`, rewritten to state what
  the UI now does.
- §13.10, the bind-guard sentence at `spec/13-deployment.md:172`, whose stated
  rationale is "preventing accidental exposure of an unauthenticated UI". That
  rationale is the source the code comment and the docs pages below mirror, so
  it is restated to match what the guard achieves once the UI can authenticate.
- §6.3, a new §6.3.4 subsection stating the browser acquisition flow, placed
  after §6.3.3, which ends at `spec/06-mcp-server.md:114` immediately before
  §6.4 at `:116`, with a pointer to it from the §6.3 introduction at
  `spec/06-mcp-server.md:40`. It is not a fourth sub-bullet under the
  `oauth-device-code` bullet's "How the verification URL surfaces" list
  (`spec/06-mcp-server.md:44-47`), because that list is scoped to the
  device-code flow and both candidate routes are authorization-code flows. The
  browser sign-in flow is also not a new `PODIUM_IDENTITY_PROVIDER` value: it is
  a browser acquisition path available under `oidc-jwt`, enabled by the route's
  enablement key, which is what "The web-UI authentication configuration guard"
  states and what leaves the registry's accepted provider values as §13.12
  records them (`spec/13-deployment.md:468`).
- §13.10, a sentence stating the configuration guard specified below, beside the
  bind-guard sentence, and a matching row in `docs/reference/error-codes.md`
  beside `config.web_ui_public_bind_refused` (`docs/reference/error-codes.md:69`).
- §11, a verification entry for the UI.
- §6.3.4 and §13.10, the configuration the chosen route requires, split along the
  line those sections already draw. §6.3 documents options per identity
  provider rather than per consumer: `PODIUM_OAUTH_AUDIENCE`,
  `PODIUM_OAUTH_AUTHORIZATION_ENDPOINT`, and `PODIUM_TOKEN_KEYCHAIN_NAME` are
  the `Options:` list on the `oauth-device-code` bullet
  (`spec/06-mcp-server.md:42`), stated once on the provider bullet rather than
  restated per consumer: the CLI sub-bullet at `:46` carries no option list of
  its own. So the browser flow's acquisition
  options go on the new §6.3.4 entry's own `Options:` list. The registry-process
  keys go in §13.10 beside `PODIUM_WEB_UI` (`spec/13-deployment.md:163`), which
  is where the shipped web-UI keys are documented and which is the only place
  `PODIUM_WEB_UI` appears in `spec/`. They do not go in the §13.12 identity
  table, whose introducing sentence scopes it to "the following registry-process
  variables" that the gateway-delegated providers and the
  `injected-session-token` provider introduce (`spec/13-deployment.md:470`) and
  whose closing sentence enumerates the `identity_provider:` config-file object
  (`spec/13-deployment.md:482`); a web-UI key is introduced by neither provider,
  so a row there would make the first sentence false and a config-file form
  under `identity_provider:` would make the second false. Which keys fall on
  which side depends on the route: A1's browser client id and authorization
  endpoint are acquisition options served to the browser, and A2's OAuth client
  secret and its session keys are registry-process keys. The enablement key
  that turns the browser sign-in flow on is a registry-process key under either
  route, so it goes in §13.10 with its default. Every new registry-process key
  here is a flag and a `PODIUM_*` environment variable with no config-file key,
  which is what `PODIUM_WEB_UI` and `PODIUM_WEB_UI_ALLOW_PUBLIC_BIND` already do
  (`internal/serverboot/serverboot.go:1826-1827`) and which §13.12 already
  records for `PODIUM_TRUSTED_PROXY_SECRET` and `PODIUM_RUNTIME_KEYS_PATH` as
  "Environment only; no config-file key" (`spec/13-deployment.md:479-480`).
- `docs/deployment/gateway-delegated-identity.md:105-107`, the "Web UI" section,
  which today states that the UI "carries no device-code flow of its own" and
  that "a browser request that carries no token resolves as anonymous and sees
  public visibility only". Under both routes the first half stays literally true,
  because both routes are authorization-code flows rather than the §6.3
  `oauth-device-code` flow (`spec/06-mcp-server.md:42`), and it stops being a
  complete account of the page's subject, so the section gains the browser's
  acquisition flow. Under A2 the second half becomes false, because a session
  cookie authenticates a request that carries no bearer token; under A1 it stays
  true, because the registry keeps verifying Bearer tokens and an uncredentialed
  browser request still resolves as anonymous. Proposal 0012 recorded this page
  as this proposal's obligation
  (`proposals/0012-the-registry-does-not-accept-oauth-device-code.md:226`).
- `docs/reference/http-api.md:290`, the register-response example, whose `layer`
  object prints snake_case keys for a response that emits the Go field names.
  The panel reads the emitted keys, so the page is corrected to them, as "The
  design handout" records.

Route A2 additionally:

- §7, A2's browser authentication routes, alongside the operator-level endpoints
  §7.3.3 enumerates (`spec/07-external-integration.md:152`). A2 needs more than
  the callback: a sign-in initiation route that mints and persists the state,
  nonce, and PKCE verifier and redirects to the IdP's authorization endpoint,
  the callback that reads them back and exchanges the code server-side, and a
  sign-out route that ends the session in the shared registry state decision 1's
  answer uses, so every replica refuses it afterwards, and clears the `HttpOnly`
  cookie the browser cannot clear itself. The brief names signing out as a state
  transition the design pass has to treat (`web/DESIGN.md:163-164`). Each of
  those routes is enumerated in §7 and in `docs/reference/http-api.md:13-19`.

  **IMPLEMENTOR'S CHOICE:** the path each of those routes takes. Any answer places them under
  the existing `/v1/` prefix, uses one path per route, appears identically in
  the §7 entry, in `docs/reference/http-api.md:13-19`, and in the mux
  registration, and is carried into the S45 step 4 rewrite below, so the paths
  that scenario probes and the responses it expects match the mux.
- §6.3.3, which today enumerates two registry-accepted credentials, the
  gateway-forwarded `Bearer <token>` under `oidc-jwt`
  (`spec/06-mcp-server.md:96`) and the injected `X-Podium-User-Sub`,
  `X-Podium-User-Email`, `X-Podium-User-Groups`, and `X-Podium-User-Org` headers
  under `trusted-headers` (`spec/06-mcp-server.md:108`). Under A2 the registry
  accepts a third, a browser session, so §6.3.3 states what that session is and
  what the registry verifies about it (`spec/06-mcp-server.md:92-112`). §6.3.3's
  own restatement of the two credentials is at `:94` and is inside that range.
- §6.3.1's per-request tenant selection (`spec/06-mcp-server.md:64`), which
  carries the same closed enumeration on the axis that decides which tenant a
  request is served from: the verified `org_id` claim under `oidc-jwt`, or the
  `X-Podium-User-Org` header under `trusted-headers`. A session-authenticated
  browser request on a multi-tenant registry carries neither, so the sentence
  states where a browser session's organization value comes from. §6.3.1 spans
  `spec/06-mcp-server.md:52-65`, outside the §6.3.3 range above, so it is its
  own edit site.
- `docs/deployment/gateway-delegated-identity.md:97`, which restates §6.3.1's
  enumeration verbatim in scope and content ("A multi-tenant registry routes
  each request to the tenant its organization names: the verified `org_id`
  claim under `oidc-jwt`, or the `X-Podium-User-Org` header under
  `trusted-headers`", with the per-provider deny behavior after it). Once
  §6.3.1 gains a third source for the organization value, that sentence states
  the pre-amendment enumeration as a complete account, so it gains the browser
  session's organization source and its deny behavior as the staged §6.3.1 text
  names them. This is a second site on a page the "Both routes" list already
  opens at `:105-107`. Two neighbouring restatements are checked against the
  staged §6.3.1 sentence in the same sweep and corrected where they diverge:
  `docs/deployment/oidc/index.md:67`, which restates the same axis for
  `oidc-jwt` tokens, and `docs/reference/error-codes.md:58`, whose
  `auth.tenant_unknown` row is scoped to "A verified `oidc-jwt` token's
  `org_id`" and has to admit whichever code the staged §6.3.1 sentence names
  for the browser-session deny arm.
- §13.2.1, because every A2 session answer decision 1 admits keeps the end of a
  session in shared registry state, so a browser authentication path writes and
  a read-only registry rejects that write with `registry.read_only`
  (`spec/13-deployment.md:41`). The section states the classification of sign-in
  and of sign-out.
- §13.1, if the route adds a topology component such as a shared session store
  (`spec/13-deployment.md:5`).
- `docs/reference/http-api.md:13-19`, whose Authentication section states that
  "Every call carries an OAuth-attested identity" and lists `Authorization:
  Bearer <jwt>` as the credential. Under A2 a session cookie is a second
  accepted credential and the sign-in, callback, and sign-out routes are new, so
  the section and its route list move with the spec edit.
- The shipped mirrors of the §13.2.1 write set, each of which restates it as a
  closed parenthetical: `docs/deployment/operator-guide.md:132`,
  `docs/reference/http-api.md:633`, and `deploy/runbook.md:19`. The browser
  authentication path that writes shared session state joins each list, and the
  operator-guide narrative states the observable outcome the chosen answer
  produces: an established session keeps reading, and the authentication write
  that answer performs is rejected with `registry.read_only`. `test/manual-validation.md` S45 compares the
  runbook's write set against what the running registry rejects
  (`test/manual-validation.md:4160`), so its assertion moves with them.
- `test/manual-validation.md` S45 step 4, which is a second and independent
  assertion in the same scenario. It probes `/v1/login`, `/v1/auth/token`, and
  `/v1/token` and expects "404 on each, because the registry registers no auth,
  login, or token route" (`test/manual-validation.md:4254-4266`). A2's sign-in,
  callback, and sign-out routes falsify the stated reason, and an implementor
  who mounts one of the probed paths turns the step's expectation into a
  failure. The step is rewritten to probe the paths A2 actually serves and to
  state the response each returns, and the reason clause is replaced with what
  remains true: the struck clause named a write endpoint the registry does not
  serve. "Manual validation" carries the same item.

### The web-UI authentication configuration guard

The amendment introduces one guard, because enabling `--web-ui` and the browser
sign-in flow without the configuration that flow needs would serve a UI that can
only ever resolve anonymous while the operator believes it authenticates. It is specified here so the
implementor builds it rather than deciding it.

- **What it reads.** `server.StartupConfig` already carries `PublicMode`,
  `IdentityProvider`, `Bind`, `AllowPublicBind`, `WebUI`, and
  `WebUIAllowPublicBind` (`pkg/registry/server/config_validate.go:56-72`), and
  the bind guard reads them at `:103-108`. The route decision 1 settles adds its
  browser authentication keys to that struct and to the `serverboot.Config`
  that populates it (`internal/serverboot/serverboot.go:2052-2060`): under A1 the
  browser client id and authorization endpoint, under A2 the OAuth client secret
  and whatever session keys the answer decision 1 settles requires, such as a
  signing key when the session or the pre-authorization transaction is a signed
  cookie. Both routes add one further key, the enablement
  key that turns the browser sign-in flow on. It is a registry-process key, and
  it is read only under `oidc-jwt`, following the per-provider precedent §6.3
  already sets for `PODIUM_OAUTH_SUBJECT_CLAIM` and its neighbours
  (`spec/06-mcp-server.md:102`).
- **Where that state is set and cleared.** The same places the shipped web-UI
  keys are set: the `podium serve` flag and the `PODIUM_*` environment variable,
  resolved in the §13.12 precedence order with the flag ahead of the env var
  (`spec/13-deployment.md:360`) and assembled into the configuration struct
  `serverboot` validates before it binds a listener. No new key carries a
  `registry.yaml` form, which is what `PODIUM_WEB_UI` and
  `PODIUM_WEB_UI_ALLOW_PUBLIC_BIND` already do
  (`internal/serverboot/serverboot.go:1826-1827`), and which keeps the closed
  `identity_provider:` enumeration at `spec/13-deployment.md:482` as it is.
  There is no runtime setter and no clearing site: the value is read once at
  startup.
- **When it fires.** `WebUI` is true, the browser sign-in flow is enabled by the
  route's enablement key, and any of the route's remaining browser
  authentication keys is unset. Startup then fails with
  the sentinel `ErrWebUIAuthUnconfigured` in
  `pkg/registry/server/config_validate.go`, beside `ErrWebUIPublicBindRefused`
  and carrying `config.web_ui_auth_unconfigured`, which follows that code's
  precedent exactly: a sentinel in that file, a §13.10 sentence stating the
  refusal, and a row in `docs/reference/error-codes.md` beside
  `config.web_ui_public_bind_refused` (`docs/reference/error-codes.md:69`). Both
  of those sites are the "Both routes" entry above.
- **What happens when it does not fire, and what observes that.** With no
  identity provider configured the guard is silent and the UI stays open on its
  bind address, which is what §13.10 specifies and what decision 3 keeps. The
  guard is also silent whenever the browser sign-in flow is not enabled, which
  covers every gateway-delegated deployment. Under `trusted-headers` the
  registry is an OAuth client of nothing, so no browser client id,
  authorization endpoint, client secret, or callback exists to configure, and
  under a gateway-fronted `oidc-jwt` registry the gateway authenticates the
  request and the UI inherits the request's resolved identity
  (`spec/13-deployment.md:170`). Both keep booting with `--web-ui` and no
  browser authentication keys set, which is the deployment the existing bind
  guard admits on a non-loopback address
  (`pkg/registry/server/config_validate.go:103-108`) and which
  `docs/deployment/gateway-delegated-identity.md:107` documents. The
  end-to-end test named under Testing observes all three arms, because a missed
  refusal shows up as a successful boot in the absent-keys condition. The
  existing bind-guard refusal is pinned only at the unit level
  (`pkg/registry/server/config_validate_test.go:173`,
  `internal/serverboot/webui_sign_config_test.go:27`), where each test builds a
  config struct and asserts `errors.Is` against the sentinel. Those tests supply
  the assertion form rather than the level: no test observes a web-UI startup
  refusal through the spawned binary today
  (`test/e2e/server_flag_behavior_test.go:6-8` records that the non-loopback
  refusal is left to those unit tests), so the new test is the first at that
  level.
- **Every caller.** `StartupConfig.Validate`
  (`pkg/registry/server/config_validate.go:87`) is where the check lands, and
  `serverboot.Config.validate` is its sole caller
  (`internal/serverboot/serverboot.go:2050-2062`), so the guard adds no interface
  method and no new type has to satisfy anything.

The bind-guard rationale has one source in `spec/` and a set of mirrors, and
they all move together. The source is `spec/13-deployment.md:172`, listed above.
The mirrors are the doc comment on `ErrWebUIPublicBindRefused`
(`pkg/registry/server/config_validate.go:25-30`), which repeats the sentence
verbatim; the comment on the guard itself
(`pkg/registry/server/config_validate.go:99-102`), which paraphrases the same
rationale ("the web UI is open on its bind address (no auth in a no-identity
standalone)") immediately above the check at `:103`, and which a grep for the
spec sentence does not find; `docs/reference/error-codes.md:69`, which restates
the rationale as "which would expose an unauthenticated UI"; and
`docs/reference/cli.md:154-155`, whose flag rows restate the guard without its
rationale. `cmd/podium/serve.go:35-37` also restates the guard without its
rationale and is in the sweep on the same footing as the `cli.md` rows, so the
two stay consistent with each other. Editing the docs pages alone would leave
the spec sentence and the code comments asserting the pre-amendment rationale,
which is the divergence this sweep exists to prevent.

## Resolved in adversarial review

### Pass 1 (2026-08-22, automated)

- **The docs mirror of the amended sentence was in no edit list.**
  `docs/deployment/gateway-delegated-identity.md:107` restates both halves of
  `spec/13-deployment.md:170`, and proposal 0012 recorded it as this proposal's
  obligation. The Summary, the edit-site list under "The spec amendment", and
  the ordering constraints now carry it, together with
  `docs/reference/http-api.md:13-19` under route A2 and the
  `docs/reference/cli.md:154-155` and `docs/reference/error-codes.md:69` sweep.
- **The brief's layer field names were the request body's.** `GET /v1/layers`
  marshals `store.LayerConfig`, whose untagged fields serialize under their Go
  names. "The design handout" now stages the correction to the emitted keys and
  records why retagging the struct is not staged. Pass 2 corrected the tag set
  this entry stated.
- **The brief modelled layer visibility as an enum.** §4.6 defines it as a union
  of `public`, `organization`, `groups`, and `users`. "The design handout" now
  stages the correction, including the non-wideable implicit
  `users: [<registrant>]` on user-defined layers.
- **"Gains no new endpoint" contradicted route A2.** The fixed decision and the
  non-goal are now scoped to the data plane, and the authentication routes are
  stated as decision 1's to settle. Pass 2 corrected A2's route set, which this
  entry gave as the callback alone.
- **The spec-edit list omitted §7, §13.12, and §6.3.3.** The section is retitled
  "The spec amendment" and now enumerates the sites per route, including §13.12
  for either route's configuration keys and, under A2, §7, §6.3.3, §13.2.1, and
  §13.1. The ordering constraint and the Summary point at that enumeration
  rather than restating a fixed list. Pass 2 split the configuration keys between
  §6.3 and §13.12, and Pass 3 replaced the §13.12 half with §13.10.
- **Registry-side session state was bounded against §2.2.** The constraint is
  §13.1's stateless front-end of 3+ replicas. Decision 1 now states that both
  the pre-authorization transaction and the session must survive a request
  landing on another replica and a rolling restart, names the two answers that
  satisfy it, records the §13.2.1 interaction of a store-backed session, and
  requires the cross-instance test. The decision stays open. Pass 2 added the
  matching test for the pre-authorization transaction, which this entry left
  unobserved.
- **The named layer-panel test asserted a refusal the registry does not
  produce.** An authenticated non-admin's register is reclassified as
  user-defined and succeeds (`pkg/registry/server/layers.go:601-620`). Testing
  now names the cases with their status and §6.10 code. Pass 2 corrected the
  owner-refusal case this entry added, which the registry also does not produce.
- **Rendered markdown removes the control that keeps artifact content inert.**
  The new
  "Rendering untrusted content" section states the rules, the brief carries
  them, and Testing pins them with a hostile-fixture unit test and an end-to-end
  test through a user-defined layer.
- **The search filters and the artifact viewer had no listed test.** Testing now
  names a test that the search call carries `type`, `scope`, and `tags`
  including the no-match case, and a test that the viewer issues `/v1/dependents`
  and handles its responses. Pass 2 corrected the response set this entry named.

Corrections to this pass, from the review of its own edits:

- **The bind-guard sweep staged the two docs mirrors without their source.** The
  rationale "preventing accidental exposure of an unauthenticated UI" originates
  at `spec/13-deployment.md:172` and is repeated verbatim at
  `pkg/registry/server/config_validate.go:29`. Editing only
  `docs/reference/error-codes.md:69` and `docs/reference/cli.md:154-155` would
  leave the spec and the code asserting the pre-amendment rationale. The §13.10
  edit list now carries `spec/13-deployment.md:172` as the source, the closing
  paragraph names the code comment as a site that moves with it, and the
  attribution is corrected: `error-codes.md:69` restates the rationale and
  `cli.md:154-155` restates the guard without it.
- **The first untrusted-content rule had no test that observed it.** Both named
  hostile fixtures put the markup in the manifest body only, so a second
  `dangerouslySetInnerHTML` on a `frontmatter` value, a `resources` name, or a
  `description` passed them unchanged. The unit fixture now carries the same
  markup in those fields, the end-to-end fixture carries it in frontmatter as
  well as the body, and Testing names a repository check that
  `dangerouslySetInnerHTML` appears exactly once. The failure paragraph is
  narrowed to what each check observes.
- **Two line citations were off by one.** `spec/04-artifact-model.md:601` is a
  blank line and the visibility sentence is at `:602`.
  `pkg/registry/server/server.go:851-853` covers `Query`, `Type`, and `Scope`,
  excluding the `Tags` parameter the test exists to pin, which is at `:854`;
  the range is now `:852-854`.

### Pass 2 (2026-08-22, automated)

- **The layer-panel test asserted a 403 and an owner check the registry does not
  have.** `unregister`, `update`, `restore`, and `reorder` gate on
  `!cfg.UserDefined` alone, and `reingest` calls no authorization function at all
  (`pkg/registry/server/layers.go:946-991`), which
  `test/integration/reingest_pipeline_test.go:87` exercises with no credential.
  Testing now states the boundary as the admin-defined against user-defined split
  alone, names the operations on another caller's user-defined layer as
  succeeding, and drops the owner check from what the panel must agree with. New
  decision 4 records whether this proposal stages the §13.10 and §7.3.1 owner
  gate, with the constraint any answer satisfies.
- **The viewer test named a not-found response `/v1/dependents` cannot return.**
  An unknown or invisible id returns `200 {"edges":[]}`, and the only status
  mapped to a missing artifact is `400 registry.invalid_argument` on an absent
  `id` (`pkg/registry/server/server.go:878-881`). Testing now names the
  populated-edges, empty-edges, and missing-id cases, and attributes the
  not-found case to `/v1/load_artifact`. Pass 3 added the second non-200 exit
  this entry overlooked, the `500 registry.unavailable` a store failure produces.
- **The stated tag set on `store.LayerConfig` was incomplete and implied the
  layer list emits the webhook secret.** `WebhookSecret` carries `json:"-"`
  (`pkg/store/store.go:284`) for that reason. "The design handout" now names all
  three tags, gives the full emitted key set including `TenantID`, `GitProvider`,
  `CreatedAt`, and `DeletedAt`, and states that the list payload carries no
  webhook secret.
- **§6.3.3 was described as specifying one accepted credential.** It enumerates
  the `oidc-jwt` bearer token (`spec/06-mcp-server.md:96`) and the
  `trusted-headers` injected `X-Podium-User-*` headers (`:108`). The edit site now
  says so and cites `:92-112`.
- **The §13.12 placement rule contradicted §13.12's own text.** §13.12 documents
  per-provider config in §6.3 (`spec/13-deployment.md:468`) and scopes its table
  to registry-process variables (`:470-480`). The edit site now splits the
  browser's acquisition options from the registry-process keys. Pass 3 moved the
  registry-process half to §13.10, because the §13.12 table's introducing
  sentence attributes every row to a provider the web UI is not.
- **Decision 3 attributed a no-auth posture to standalone as a mode.** §13.10
  conditions it on having no identity provider (`spec/13-deployment.md:170`) and
  §6.3.3 applies both providers on either mode (`spec/06-mcp-server.md:92`), which
  `test/manual-validation.md:3892` exercises on a standalone `oidc-jwt` registry
  with `--web-ui`. Decision 3 is restated on the identity-provider axis, and the
  Summary entry with it. Pass 3 narrowed that axis again to the enablement key,
  which is read only under `oidc-jwt`.
- **Route A2 was scoped to one endpoint while its own mechanism needs more.** The
  §7 edit site now names the sign-in initiation, callback, and sign-out routes,
  marks their paths as the implementor's choice with the constraint that each
  appears in §7, in `docs/reference/http-api.md:13-19`, and in the mux, and the
  Summary and decision 1 carry the same set. Pass 3 added the sign-out test this
  entry left unpinned.
- **The brief's identity-filtered, role-dependent layer list was in no edit
  list.** `GET /v1/layers` returns every active layer in the tenant to every
  caller (`pkg/registry/server/layers.go:772-777`,
  `internal/serverboot/serverboot.go:1220`). "The design handout" stages the
  correction, and the Summary records that per-role scoping in the panel is
  client-side presentation.
- **The claim that retagging would move the API reference example was
  inverted.** `docs/reference/http-api.md:290` already prints snake_case keys for
  a response that emits Go field names, so the page is wrong today. The false
  clause is dropped and the page joins the docs edit list.
- **The §13.2.1 write set has shipped mirrors that were unstaged.** Under the
  same condition as the §13.2.1 entry, `docs/deployment/operator-guide.md:132`,
  `docs/reference/http-api.md:633`, and `deploy/runbook.md:19` gain browser
  sign-in, and `test/manual-validation.md` S45 moves with them.
- **The bind-guard sweep missed the mirror on the enforcing code.**
  `pkg/registry/server/config_validate.go:99-102` paraphrases the pre-amendment
  rationale immediately above the check. It joins the sweep, the enumeration no
  longer states a count, and `cmd/podium/serve.go:35-37` is named as in scope on
  the same footing as the `cli.md` rows.
- **"Both halves become false" did not hold for the gateway-delegated-identity
  page.** Both routes are authorization-code flows, so the device-code clause
  stays true, and the anonymous clause becomes false only under A2. The edit site
  and the Summary now state the per-route accounting.
- **A2's pre-authorization transaction had no test.** Testing now names the
  cross-instance callback case beside the cross-instance session case, including
  the refusal when the transaction cannot be resolved, and decision 1 points at
  both.
- **The new configuration keys had no test and no absent-key behavior.** "The
  spec amendment" now specifies the `config.web_ui_auth_unconfigured` guard whole
  (what it reads, where that state is set, when it fires, what happens when it
  does not, and its sole caller), and Testing names the end-to-end boot test that
  observes its conditions.
- **The guard's first predicate refused every gateway-delegated deployment.**
  Firing on "an identity provider is configured" alone made
  `podium serve --web-ui` unbootable under `trusted-headers`, where the registry
  is an OAuth client of nothing and no browser key exists to set, and refused the
  gateway-fronted `oidc-jwt` registry whose UI inherits the request's resolved
  identity (`spec/13-deployment.md:170`,
  `docs/deployment/gateway-delegated-identity.md:107`) and which the existing
  bind guard admits on a non-loopback address
  (`pkg/registry/server/config_validate.go:103-108`). The guard now fires only
  when the browser sign-in flow is enabled by the route's enablement key and a
  remaining key is unset, the non-firing arm names the gateway-delegated case,
  and Testing carries it as a fourth boot condition.
- **Testing and the Summary routed every new key to §13.12 after the placement
  rule was split.** Under route A1 the amendment adds no §13.12 acquisition
  option, so a "§13.12 default" was a default in a section documenting none.
  Testing now reads the default from the section the edit-site list places the
  key in, and the Summary states the placement rather than a per-route table
  entry. The enablement key is named as a registry-process key under either
  route. Pass 3 changed the registry-process section from §13.12 to §13.10.
- **Decision 2 still counted three decisions.** The count went stale when
  decision 4 was added, and decision 4 carries its own blast radius, so the
  comparison no longer held. The sentence now names what decision 2 touches
  without a count.

### Pass 3 (2026-08-22, automated)

- **The §6.3 browser entry was anchored inside the `oauth-device-code`
  provider's verification-URL sub-list.** The MCP-server, CLI, and SDK entries
  at `spec/06-mcp-server.md:45-47` are sub-bullets of the `oauth-device-code`
  bullet at `:42`, introduced at `:44` by "How the verification URL surfaces
  depends on the consumer", so filing an authorization-code flow beside them
  would document the browser as a consumer of the device-code flow the registry
  refuses (`spec/13-deployment.md:468`). The edit site is now a new §6.3.4
  subsection placed after §6.3.3, and the amendment states that the browser
  sign-in flow is a browser acquisition path available under `oidc-jwt` rather
  than a new `PODIUM_IDENTITY_PROVIDER` value, which is the choice that adds no
  provider value and matches the guard's `oidc-jwt` scoping. The supporting
  paragraph under "The spec amendment" is corrected to describe that sub-list as
  device-code-scoped.
- **"§6.3 carries the acquisition-side options per consumer" misread §6.3.**
  §6.3's option lists are per identity provider: `PODIUM_OAUTH_AUDIENCE`,
  `PODIUM_OAUTH_AUTHORIZATION_ENDPOINT`, and `PODIUM_TOKEN_KEYCHAIN_NAME` are
  the `Options:` list on the `oauth-device-code` bullet
  (`spec/06-mcp-server.md:42`), and the CLI sub-bullet at `:46` carries no
  option list. §6.2 states separately that provider-specific options are passed
  as additional env vars (`spec/06-mcp-server.md:36`). The placement rule and decision
  1's route A1 costing now put the browser flow's acquisition options on the new
  §6.3.4 entry's own `Options:` list.
- **The §13.12 table's scoping sentence and its closed config-file enumeration
  were unstaged.** `spec/13-deployment.md:470` attributes every row of that table
  to the gateway-delegated and `injected-session-token` providers, and `:482`
  enumerates the `identity_provider:` object, so a web-UI row would falsify one
  and a config-file form under `identity_provider:` would falsify the other. The
  registry-process keys now go in §13.10 beside `PODIUM_WEB_UI`
  (`spec/13-deployment.md:163`), where the shipped web-UI keys live, and each is
  a flag and a `PODIUM_*` environment variable with no config-file key, which is
  what `PODIUM_WEB_UI` and `PODIUM_WEB_UI_ALLOW_PUBLIC_BIND` already do
  (`internal/serverboot/serverboot.go:1826-1827`). The Summary, the guard's
  set-and-clear paragraph, Testing, and decision 1 carry the same placement.
- **"`handleDependents` has one error arm" was false.** The handler routes a
  `DependentsOf` failure through `writeCoreError`
  (`pkg/registry/server/server.go:883-887`), whose default arm writes
  `500 registry.unavailable` (`:1405-1406`), and that path is reachable because
  `DependentsOf` propagates the store error and the `visibleManifests`
  `ErrUnavailable` unchanged (`pkg/registry/core/dependents.go:32-39`,
  `pkg/registry/core/core.go:1980`). Testing now names both non-200 exits and
  adds the case that the viewer distinguishes `500 registry.unavailable` from
  `200 {"edges":[]}`, presenting the surface's error state for the links while
  the already-loaded artifact stays rendered.
- **The two tests cited as pinning the web-UI refusal at the end-to-end level
  are unit tests.** `pkg/registry/server/config_validate_test.go:173` and
  `internal/serverboot/webui_sign_config_test.go:27` build a config struct and
  call `Validate` in the test process, and
  `test/e2e/server_flag_behavior_test.go:6-8` records that the non-loopback
  refusal is left to them. The guard section and Testing now state that those
  tests supply the assertion form rather than the level, that no end-to-end test
  observes a web-UI startup refusal today, and that the new
  `config.web_ui_auth_unconfigured` test asserts the spawned binary's exit
  status and the code named on stderr.
- **Decision 3 scoped the sign-in flow to "whenever an identity provider is
  configured".** The registry accepts `oidc-jwt`, `trusted-headers`, and
  `injected-session-token` (`spec/13-deployment.md:468`), and under the latter
  two no browser OAuth client exists, which the guard section already states.
  Decision 3 is restated on the enablement-key axis, which is read only under
  `oidc-jwt`, and names the inherited-identity behavior the other two keep.
- **Route A2's third credential left §6.3.1's per-request tenant selection
  unstaged.** `spec/06-mcp-server.md:64` carries the same closed enumeration on
  the tenant-selection axis, and a session-authenticated browser request carries
  neither an `org_id` claim nor `X-Podium-User-Org`. §6.3.1 spans `:52-65`,
  outside the staged §6.3.3 range, so it joins the route A2 edit-site list.
- **Route A2's sign-out route had no test.** Testing now names an A2 test that,
  after a successful sign-in, the sign-out route clears the session cookie and a
  subsequent layer-panel write carrying the pre-sign-out session is refused,
  asserted across two registry instances so an in-process-only invalidation is
  caught.
- **The layer panel's read surface had no test.** §13.10 specifies the list with
  source, visibility, and `last_ingested_at` (`spec/13-deployment.md:168`), and
  the panel reads Go field names and a §4.6 visibility union rather than the
  snake_case request-body names. Testing now names an end-to-end test of the
  rendered list, including a layer carrying both `Organization` and `Groups`,
  and the empty-tenant state.
- **Correction to this pass: Testing's replacement sentence overstated what
  `test/e2e/server_flag_behavior_test.go` covers.** The file holds six tests,
  and `TestServerFlags_SignRejectsUnknown` (`:116-126`) already refuses an
  unrecognized `--sign` value through the spawned binary and asserts a non-zero
  exit with `config.invalid_sign_mode` on stderr, which is the assertion form
  the new `config.web_ui_auth_unconfigured` test needs. Testing now scopes the
  claim to the web UI, cites the two mount tests (`:18`, `:36`), and names
  `TestServerFlags_SignRejectsUnknown` as the end-to-end precedent to follow.

### Pass 4 (2026-08-22, automated)

- **Decision 1 endorsed a session answer the listed sign-out test refuses.** A
  signed stateless cookie carries no server-side state, so sign-out can only
  emit a clearing `Set-Cookie` and a replayed pre-sign-out cookie still presents
  a valid signature to a second instance, which is exactly the case Testing
  asserts and exactly what the §7 sign-out entry says the route prevents.
  Decision 1 now splits the two pieces of cross-request state: the
  pre-authorization transaction takes either a signed stateless cookie or a
  shared store record, and the session takes a shared `RegistryStore` session
  record or a signed stateless cookie together with a revocation record, because
  sign-out ends a session that authorizes destructive layer operations. Every
  admitted answer therefore keeps the end of a session in shared state, so the
  §13.2.1 edit site and its shipped mirrors become unconditional under A2, the
  §7 sign-out sentence states the cross-replica revocation, the A2 cost list and
  the Summary carry the same predicate, and the key lists no longer name a
  session signing key as A2's only session key.
- **The domain browser had no listed test.** It is a §13.10 surface whose
  implementation the React rewrite replaces, and after Pass 3 pinned the layer
  panel's read surface it was the remaining specified surface nothing observes.
  Testing now names an end-to-end test that the rewritten browser issues
  `/v1/load_domain` for the requested path, renders the `subdomains` and
  `notable` entries, navigates a nested path, and renders the empty state for a
  domain carrying neither. The Summary's test list carries it.

### Pass 5 (2026-08-22, automated)

- **The artifact viewer's specified rendering was the one §13.10 surface no
  listed test observed.** The listed viewer tests were the hostile-fixture
  inertness case and the `/v1/dependents` case, both of which a renderer that
  escapes its whole input and emits the body as literal text satisfies, so
  neither observed the markdown rendering or the frontmatter property table
  (`spec/13-deployment.md:167`) that replaces today's two `<pre>` blocks
  (`web/app.js:101-102`, `:105-106`). Testing now names an end-to-end case that
  a heading, a fenced code block, and a link render as the corresponding
  elements and that a two-key `frontmatter` string
  (`pkg/registry/server/server.go:582`) renders as a property table with one row
  per key, which puts the viewer under the standard Pass 3 and Pass 4 applied to
  the layer panel's read surface and to the domain browser. The Summary's test
  list carries it.
- **Route A2's staged §6.3.1 tenant selection had no test.**
  `spec/06-mcp-server.md:64` closes the tenant-selection axis to the verified
  `org_id` claim and the `X-Podium-User-Org` header and states a deny behavior
  for each, and the A2 edit-site list stages a third credential on that axis
  with nothing pinning it. Testing now names an A2 test that a browser session
  established for a caller in one organization serves that organization's layer
  list and audit stream, and that a session whose organization value resolves to
  no provisioned tenant is refused with the code the staged sentence names
  rather than falling through to another tenant. The Summary's test list carries
  it. Pass 7 narrowed the assertion to the catalog routes the tenant router
  covers, because the layer list this entry named is served from the boot
  default tenant.

### Pass 6 (2026-08-22, automated)

- **The layer panel's reingest operation had no listed test.** §13.10 names
  register, reingest, and unregister as the panel's operations
  (`spec/13-deployment.md:168`), and the write bullet enumerated only register,
  unregister, update, restore, and reorder. Reingest appeared only as background
  for the authorization discussion, where `test/integration/reingest_pipeline_test.go:87`
  pins the endpoint rather than the panel's consumption of it. Testing now names
  an end-to-end case that the panel's reingest control issues
  `POST /v1/layers/reingest?id=<id>` and presents each response the handler
  produces: the §7.3.1 result summary when a runner is wired
  (`pkg/registry/server/layers.go:1090-1101`), the queue-only
  `{"queued", "queued_at"}` body when one is not (`:997-1002`), the
  `404 registry.not_found` and `500 registry.unavailable` arms (`:981-988`) as
  the surface's error state, and the pure-conflict
  `409 ingest.immutable_violation` envelope (`:1056-1065`) as the error state
  carrying that code. This puts the panel's write surface under the standard
  Passes 3, 4, and 5 applied to the panel's read surface, the domain browser,
  and the artifact viewer. The Summary's test list carries it.

### Pass 7 (2026-08-22, automated)

- **`spec/06-mcp-server.md:36` was cited for a proposition it does not state.**
  That line is the closing sentence of §6.2 and says only that
  provider-specific options are passed as additional env vars. It says nothing
  about consumers and is outside §6.3, whose first heading is at
  `spec/06-mcp-server.md:38`. The placement rule and decision 1's route A1
  costing now rest on the citations that carry the claim: the `Options:` list on
  the `oauth-device-code` bullet (`:42`) and the CLI sub-bullet that carries
  none (`:46`). The Pass 3 entry that introduced the citation attributes it to
  §6.2.
- **The route-A2 §6.3.1 test asserted a tenant-scoped layer list the endpoint
  cannot produce.** `GET /v1/layers` performs no per-request tenant selection:
  `NewLayerEndpoint` captures the boot default tenant
  (`internal/serverboot/serverboot.go:1199`) and the list handler reads it
  (`pkg/registry/server/layers.go:772`), while the §6.3.1 tenant router is a
  meta-tool-server option (`internal/serverboot/serverboot.go:1165-1170`) that
  the outer-mux layer route never reaches (`:1220`, `:1239`,
  and the comment at `:1194-1196`). The test now asserts tenant selection and
  its deny arm on the catalog routes the router covers, records that routing
  the layer endpoint through §6.3.1 is a separate change, and the "Watch out
  for" entry on `GET /v1/layers` records the multi-tenant behavior.
- **The route-A2 §6.3.1 edit left its shipped docs mirror unstaged.**
  `docs/deployment/gateway-delegated-identity.md:97` restates §6.3.1's closed
  enumeration and its per-provider deny behavior. It joins the route A2 docs
  list, together with the two neighbouring restatements checked in the same
  sweep, `docs/deployment/oidc/index.md:67` and the `auth.tenant_unknown` row at
  `docs/reference/error-codes.md:58`. The Summary carries the site.
- **S45 step 4 asserts that the registry serves no auth, login, or token
  route.** It probes `/v1/login`, `/v1/auth/token`, and `/v1/token` and expects
  a 404 on each for that stated reason
  (`test/manual-validation.md:4254-4266`), which route A2 falsifies and which an
  implementor who mounts one of those paths turns into a failing step. The route
  A2 edit list stages the step's rewrite, "Manual validation" carries the same
  item, the route-path choice is constrained to appear in that rewrite, and the
  Summary and the ordering constraints name S45 beside S44.
- **The artifact viewer's not-found state had no listed test.** Pass 2 moved the
  case off the `/v1/dependents` bullet without staging it at
  `/v1/load_artifact`. Testing now names an end-to-end case that a missing id
  and an id invisible to the caller both return `404 registry.not_found`
  (`pkg/registry/server/server.go:1403-1404`, reached because `LoadArtifact`
  filters invisible manifests before the not-found return,
  `pkg/registry/core/core.go:1497`) and that the viewer presents the same
  not-found state for both without implying the artifact exists
  (`web/DESIGN.md:174-176`), while `500 registry.unavailable` presents the error
  state instead. The Summary's test list carries it.

## Decisions for the reviewer

1. **How the browser authenticates.** There are two viable routes, and the
   maintainer leans toward the second.

   **Route A1, pure-SPA authorization code with PKCE.** The browser talks to the
   IdP directly and the registry is untouched: it keeps verifying Bearer tokens
   under `oidc-jwt` exactly as it does for the CLI. The spec change is the
   "Both routes" edit-site list alone, with no route A2 additions. Costs: the token lives in the
   browser, the operator must register a public client at the IdP with the
   registry's `/ui/` origin as a redirect URI, and the IdP must allow CORS on
   its token endpoint. The browser client id and the authorization endpoint the
   UI presents are served from the registry and are acquisition options, so they
   go on the new §6.3.4 entry's own `Options:` list, which is the axis §6.3 uses
   for `oauth-device-code`'s options (`spec/06-mcp-server.md:42`, with the CLI
   sub-bullet at `:46` carrying none of its own), rather than in the §13.12
   table.

   **Route A2, registry-mediated.** The registry becomes the OAuth client,
   performs the code exchange server-side, and hands the browser a session
   cookie. No token is reachable from JavaScript, which removes the class of
   attack where a script steals it. Costs: the sign-in initiation, callback, and
   sign-out routes under §7 that the edit-site list above enumerates, a session
   concept the registry does not currently have whose end is recorded in shared
   state, an OAuth client secret to configure, and the §6.3.4, §6.3.1, §6.3.3,
   §13.2.1, and §13.10 entries that same list enumerates.

   A2's cost is proportionate here in a way it would not be for a read-only
   viewer. The layer panel performs destructive operations as the caller, which
   is what justifies the additional cost. A2 also needs a position on CSRF,
   which A1 does not, because a cookie-authenticated write is forgeable across
   origins and a Bearer-authenticated one is not.

   Whichever route is taken, this decision settles where the session or token
   lives and what happens when it expires mid-page. Route A2 has a further
   constraint, and any A2 answer has to satisfy it: the §13.1 reference topology
   is a stateless front-end of 3+ replicas behind a load balancer with no
   specified session affinity (`spec/13-deployment.md:5`), and A2 needs two
   pieces of cross-request state, the pre-authorization transaction (the state,
   nonce, and PKCE verifier written at redirect and read at the callback) and
   the post-authorization session. Both have to survive a request landing on a
   replica other than the one that wrote them, and the session has to survive a
   rolling restart. An in-process map satisfies neither. For the
   pre-authorization transaction, a signed stateless cookie with an
   operator-configured signing key and a stated rotation procedure satisfies the
   constraint, and so does a record in the shared `RegistryStore`. The session
   carries a further requirement, because sign-out ends a session that
   authorizes destructive layer operations and the registry has to refuse an
   ended session on whichever replica the next request lands on. A signed
   stateless cookie on its own cannot meet it: it carries no server-side state,
   so nothing exists for sign-out to end, and a captured cookie replayed after
   sign-out still presents a valid signature and unexpired claims. The session
   answers that satisfy the constraint are therefore a session record in the
   shared `RegistryStore`, or a signed stateless cookie together with a
   revocation record in that store. Both keep the end of a session in shared
   state, so under either the registry writes on a browser authentication path
   and a read-only registry rejects that write with `registry.read_only`
   (§13.2.1). Any A2 answer states which of sign-in and sign-out performs that
   write and what a read-only registry does with a sign-out it cannot record,
   because a sign-out the registry accepts without recording leaves the session
   usable. §2.2 imposes no constraint here: its
   statelessness sentence is about the MCP server (`spec/02-architecture.md:93`)
   and its shared-library paragraph is about a single canonical implementation
   per concern (`spec/02-architecture.md:101-105`).

   Whatever A2 answer is chosen brings its registry-process configuration keys
   into §13.10 with their defaults, its §13.2.1 classification of the browser
   authentication write, its topology component into §13.1 when it adds one, and
   the cross-instance tests Testing names. One covers each piece of
   cross-request state: a sign-in whose redirect is issued by one instance
   completes when the callback is delivered to a second, and a session
   established against one instance authorizes a layer-panel write issued
   against a second. The first is the case an in-process map passes only by
   accident of routing, so without it the constraint above is unobserved. A
   further test covers the revocation the session half requires: after sign-out
   against one instance, a second instance refuses the pre-sign-out session.
2. **The React rewrite and the build step.** The SPA is to be rewritten in
   React, which the current no-build-step vanilla bundle cannot accommodate. The
   repository already builds JavaScript for the documentation site under
   `site/`, so a toolchain exists as precedent, but the web UI's bundle is
   embedded into the Go binary and the site's is not.

   Open: whether the built assets are committed so `go build` alone still
   produces a working binary, or generated during the release build so the tree
   carries no build output. The first keeps `go build ./...` self-contained and
   puts generated files in review diffs; the second keeps the tree clean and
   makes the binary depend on a Node toolchain being present. §13.10 says the UI
   is "bundled into the binary", which both satisfy.

   This decision touches `web/web.go`, the Makefile, the release workflow, and
   CI.
3. **Scope when no identity provider is configured.** §13.10 conditions the open
   UI on the absence of an identity provider rather than on the deployment mode:
   "in standalone deployments without an identity provider, the UI is open on the
   bind address" (`spec/13-deployment.md:170`). §6.3.3 states that `oidc-jwt` and
   `trusted-headers` "both apply on a standalone (§13.10) or a standard (§13.1)
   backend" (`spec/06-mcp-server.md:92`), and a standalone registry under
   `oidc-jwt` with `--web-ui` is a configuration the repository already validates
   by hand (`test/manual-validation.md:3892`, whose command is
   `podium serve --standalone ... --web-ui --bind 127.0.0.1:8153`). Splitting the
   authenticated UI along the standard-against-standalone axis would leave that
   exact registry unable to authenticate its own UI while an identical registry
   started in standard mode can, which is a deployment-mode divergence §2.2 does
   not admit.

   So the sign-in flow applies when the route's enablement key is set, in either
   mode, and that key is read only under `oidc-jwt`, which is the axis "The
   web-UI authentication configuration guard" uses. A `trusted-headers` or
   `injected-session-token` registry has an identity provider configured and
   runs no sign-in flow: it keeps inheriting the request's resolved identity,
   because under `trusted-headers` the registry is an OAuth client of nothing
   and under `injected-session-token` the runtime issues the token
   (`spec/06-mcp-server.md:66`). The open UI applies when no identity provider
   is configured. What remains open is what
   the layer panel does with no identity provider configured. There every caller
   resolves anonymous and the admin gate admits everyone, because the boot wiring
   short-circuits it when no provider is set or public mode is on
   (`internal/serverboot/serverboot.go:1213-1215`), so every layer operation
   including unregister succeeds for any browser that can reach the bind address.
   Whether the panel is hidden, shown read-only, or shown with its write
   operations available is open.
4. **Whether this proposal closes the layer-ownership gap.** §13.10 states that
   "users can manage their own user-defined layers"
   (`spec/13-deployment.md:168`) and §7.3.1 scopes manual reingest to "admin or
   layer owner" (`spec/07-external-integration.md:65`). The handlers implement
   neither. `unregister`, `update`, `restore`, and `reorder` gate on
   `!cfg.UserDefined` alone (`pkg/registry/server/layers.go:856-861`, `:494-499`,
   `:819-824`, `:905-910`), so any caller can delete or rewrite another user's
   user-defined layer and receives `200`. `reingest` calls no authorization
   function at all (`:946-991`), and `test/integration/reingest_pipeline_test.go:87`
   posts to it with no credential today. The only owner comparisons in the file
   are the §7.3.1 cap count (`:680`) and the §8.5 erase filter (`:419`), neither
   of which authorizes a write against the caller.

   This is a fail-open divergence from spec rather than a spec-silent detail, and
   it matters here because the layer panel is the surface that exposes those
   operations to a browser. Any answer satisfies this constraint: the panel does
   not present per-owner scoping on a destructive operation as server-enforced
   unless the server enforces it. Either the proposal stages a code deliverable
   adding the owner gate to `unregister`, `update`, `restore`, and `reorder`, and
   an admin-or-owner gate to `reingest`, with the `403` `auth.forbidden` tests
   attached to it and `test/integration/reingest_pipeline_test.go` updated to
   carry a credential; or the gap is left out of scope, the panel's role split is
   documented as client-side presentation, and the gap is filed separately. The
   proposal takes no position, because closing it changes the authorization
   behavior of every layer write handler named above, which carries its own blast
   radius rather than being a detail of building the UI.

## The design handout

**The implementor does not design the UI.** `web/DESIGN.md` is the design brief,
and a design pass against it produces the layouts, the state treatments, and the
component inventory. The implementation builds what that pass produces.

The brief carries the four surfaces with the response payloads their endpoints
return, the identity and per-surface state matrix, and the design questions this
proposal does not answer: how much domain depth to render at once, whether to
expose the relevance score, how to treat the sensitivity label, and how to
distinguish an empty domain from a filtered one without disclosing that hidden
artifacts exist.

The brief is corrected where it disagrees with what the endpoints emit and with
§4.6, before the design pass reads it. The brief itself states that the spec
wins where the two disagree (`web/DESIGN.md:8-9`).

- **The layer payload's field names.** `web/DESIGN.md:126-129` lists `id`,
  `source_type`, `local_path`, `user_defined`, and the rest in snake_case. Those
  are the names of the `POST /v1/layers` request body
  (`pkg/registry/server/layers.go:289-297`). `GET /v1/layers` marshals
  `store.LayerConfig` directly (`pkg/registry/server/layers.go:772-777`), and
  that struct carries a JSON tag on `ForcePushPolicy`
  (`json:"force_push_policy,omitempty"`, `pkg/store/store.go:302`), on
  `LastIngestedAt` (`json:"last_ingested_at,omitempty"`,
  `pkg/store/store.go:307`), and on `WebhookSecret` (`json:"-"`,
  `pkg/store/store.go:284`), which keeps the HMAC secret out of every response
  that marshals the struct, `GET /v1/layers` included, because that endpoint is
  not admin-gated (`pkg/store/store.go:277-281`). Every remaining field marshals
  under its Go field name, so the emitted keys are `TenantID`, `ID`,
  `SourceType`, `Repo`, `Ref`, `Root`, `LocalPath`, `Order`, `UserDefined`,
  `Owner`, `Public`, `Organization`, `Groups`, `Users`, `GitProvider`,
  `LastIngestedRef`, `CreatedAt`, and `DeletedAt`, alongside the two snake_case
  tagged keys. The register response embeds the same struct
  (`pkg/registry/server/layers.go:327-332`). The brief is corrected to that key
  set, with a note that the register and update request bodies use the snake_case
  names instead and that the layer list payload carries no webhook secret, so the
  one-time reveal is reachable only from the register response
  (`pkg/registry/server/layers.go:751-755`) and the update response that carries
  a rotated secret (`pkg/registry/server/layers.go:571-576`). The response keys
  are pinned by
  `test/e2e/declarative_layers_test.go:67`,
  `test/e2e/filesystem_sync_test.go:804`,
  `test/e2e/standalone_server_test.go:816`,
  `test/e2e/standard_deployment_test.go:1688`, and
  `cmd/podium/serve_layer_path_test.go`, so the panel reads those keys and no
  retagging of `store.LayerConfig` is staged. The shipped API reference is
  already inconsistent with what the endpoint emits: its register-response
  example prints `"layer": { "id": ..., "source_type": ... }`
  (`docs/reference/http-api.md:290`) for a response whose `layer` object carries
  `ID` and `SourceType`. That page is in the docs edit list, and the correction
  is to the emitted Go field names in that example. Correcting the page rather
  than the struct keeps the pinned tests and the served contract as they are.
- **The layer visibility model.** `web/DESIGN.md:128-129` presents visibility as
  one of public, organization-wide, group-scoped, or user-scoped. §4.6 defines
  it as one or more independent declarations that combine as a union, and a
  caller sees the layer if any condition matches
  (`spec/04-artifact-model.md:602`, `:611`). `store.LayerConfig` mirrors that
  with `Public`, `Organization`, `Groups`, and `Users`
  (`pkg/store/store.go:269-273`), all of which the list response emits. The
  brief is corrected to describe the union, so the panel renders the full set
  rather than picking one label, and it records that a user-defined layer
  carries implicit `users: [<registrant>]` that cannot be widened
  (`spec/04-artifact-model.md:611`).
- **The layer list is neither identity-filtered nor role-dependent.** The brief
  tells the design pass that everything the UI displays is "filtered by the
  caller's identity" (`web/DESIGN.md:20-22`), that the layer panel is "the only
  one whose contents differ by role" (`web/DESIGN.md:124`), and that an
  administrator sees every layer while an ordinary user sees only their own
  (`web/DESIGN.md:145-147`). `GET /v1/layers` does none of that. Its handler
  calls `ListLayerConfigs` with the tenant alone and writes the result
  (`pkg/registry/server/layers.go:772-777`), the memory backend filters only on
  tenant and the soft-delete tombstone (`pkg/store/memory.go:428-443`), the route
  is mounted without authenticating middleware
  (`internal/serverboot/serverboot.go:1220`), and the endpoint is not admin-gated
  (`pkg/store/store.go:277-278`). So every caller, anonymous included, receives
  every active layer in the tenant with its repo URL, owner subject, and
  visibility fields. The brief is corrected to say so, and to say that any
  per-role scoping the panel presents is client-side presentation over an
  unscoped payload rather than a server-enforced view. The design pass therefore
  cannot treat hiding a layer as withholding it. Server-side filtering of the
  list is a separate change with its own spec basis and is not staged here.
- **Untrusted content.** The brief's only statement about artifact content is
  about length (`web/DESIGN.md:44-46`). It gains the rules stated under
  "Rendering untrusted content", because the design pass decides how a body,
  frontmatter values, and outbound links are presented and those presentations
  have to be expressible under the rules.

Two items in the brief are design problems rather than implementation details
and must not be settled by whoever writes the React:

- The webhook secret is returned once on register and on rotation
  (`LayerRegisterResponse`, `pkg/registry/server/layers.go:328`). It has to be
  copyable, unmistakably unrecoverable, and not readable as persistent content.
- Unregistering a layer removes its artifacts from every caller's view. It needs
  a confirmation treatment proportionate to that.

## Rendering untrusted content

Today no artifact content can execute in the UI: every server-supplied string
reaches the DOM as a text node (`web/app.js:29`) and the manifest body goes into
a `<pre>` (`web/app.js:106`). Rendering the body as markdown removes that
property. `manifest_body` is user-authored content ingested from a layer, any
authenticated caller can register a layer of their own
(`spec/07-external-integration.md:95`), and the UI shares the API's origin
(`web/DESIGN.md:19-21`), so a low-privileged author would otherwise execute
script in the browser of anyone who opens the artifact, including an
administrator, on an origin that carries the caller's credential and hosts the
layer panel's destructive operations.

The build therefore satisfies these rules, and the brief carries them so the
design pass produces presentations that are expressible under them.

- Server-supplied strings other than the rendered body are inserted as text.
  This covers `frontmatter`, `description`, `id`, `resources` and
  `large_resources` names, every layer field, and every error message. React's
  default interpolation does this; the rule is that no other site uses
  `dangerouslySetInnerHTML`. `frontmatter` is a sibling of `manifest_body` on the
  same response (`pkg/registry/server/server.go:582`) and is equally
  author-controlled, and §13.10 renders it as a property table
  (`spec/13-deployment.md:167`), so it is a live insertion surface. Testing names
  both a repository check that `dangerouslySetInnerHTML` appears exactly once and
  fixture coverage of these fields.
- The rendered markdown body is the single site that inserts HTML. The markdown
  renderer runs with raw HTML disabled, and its output passes through an
  allowlist sanitizer before insertion. The allowlist admits no element that can
  execute or load a subresource (`script`, `style`, `iframe`, `object`,
  `embed`, `form`) and no event-handler attribute.
- `href` and `src` values that survive sanitization are restricted to `http`,
  `https`, `mailto`, and in-page fragment targets. Every other scheme, including
  `javascript:` and `data:`, is dropped.
- A large-resource link the viewer follows is a registry-issued URL from the
  `load_artifact` response and is subject to the same scheme restriction.

When a rule does not hold, artifact content executes on the registry's origin
with whatever credential the chosen route places there. The hostile-fixture
tests named under Testing observe the body rules and the non-body insertion
sites they exercise, the repository check observes the single-insertion-site
rule, and both fail rather than warn.

**IMPLEMENTOR'S CHOICE:** which markdown renderer and sanitizer the bundle uses.
Any answer runs in the browser bundle with no network call, is configured by
allowlist rather than denylist, and is applied to the renderer's output before
insertion rather than to the markdown source.

## Testing

§11 currently requires nothing of the UI and no test cites §13.10 for a UI
surface, so this proposal creates the verification obligation as well as
satisfying it. A §11 entry is part of the spec amendment.

At minimum:

- Unit coverage for the auth flow's token or session handling including expiry.
- Unit coverage of the sanitization rules: a fixture body carrying a `<script>`
  element, an `onerror` attribute, a raw `<iframe>`, and a `javascript:` link
  renders inert, and the fixture is asserted against the rendered output rather
  than against the renderer's configuration. The same fixture carries the same
  hostile markup in a `frontmatter` value, in a `resources` name, and in the
  artifact `description`, and those render inert as literal text, which is what
  observes the first rule at the non-body insertion sites.
- A repository check that `dangerouslySetInnerHTML` appears exactly once in the
  bundle's source, in the viewer module that inserts the rendered body. This is
  the mechanical form of the first rule, and a fixture cannot observe a second
  insertion site the build has not yet added.
- End-to-end coverage of the domain browser, whose behavior §13.10 specifies as
  "hierarchical navigation matching `load_domain`'s structure"
  (`spec/13-deployment.md:165`) and whose implementation the React rewrite
  replaces (`web/app.js:34-45`). The rewritten browser issues `/v1/load_domain`
  for the requested path and renders the `subdomains` and `notable` entries that
  response carries (`pkg/registry/server/server.go:506-513`), a nested path
  navigates to that path rather than re-requesting the root, and a domain whose
  response carries neither a visible subdomain nor a visible notable artifact
  renders the empty state rather than an error.
- An end-to-end test that an authenticated caller sees an artifact an anonymous
  one does not, driven through the UI's own API calls rather than through the
  CLI.
- An end-to-end test that ingesting an artifact whose body and frontmatter carry
  the same hostile markup through a user-defined layer and loading it in the
  viewer executes nothing.
- An end-to-end test that the search surface's call to
  `/v1/search_artifacts` carries `type`, `scope`, and `tags`, which the handler
  already reads (`pkg/registry/server/server.go:852-854`), including the
  no-match case where `total_matched` is 0.
- An end-to-end test that the artifact viewer issues `/v1/dependents`
  (`pkg/registry/server/server.go:399`) for the loaded id, since
  `LoadArtifactResponse` carries no edge field, and that it handles the
  responses that endpoint produces. `handleDependents` has two non-200 exits: a
  `400 registry.invalid_argument` when `id` is absent
  (`pkg/registry/server/server.go:878-881`), and the `writeCoreError` arm that
  surfaces a store or visibility-resolution failure as `500 registry.unavailable`
  (`:883-887` and `:1405-1406`, reached because `DependentsOf` propagates the
  store error unchanged and wraps a `ListManifests` failure as `ErrUnavailable`,
  `pkg/registry/core/dependents.go:32-39`, `pkg/registry/core/core.go:1980`).
  Otherwise it writes `200` with an `edges` array (`:894`).
  `DependentsOf` reads the reverse-dependency
  index and filters by visibility without resolving the artifact
  (`pkg/registry/core/dependents.go:26-50`), and the backends return no edges and
  a nil error for an unmatched `to_artifact` rather than `ErrNotFound`
  (`pkg/store/memory.go:309-319`, `pkg/store/sqlite.go:613-625`), so an unknown,
  deleted, or invisible id yields the same `200 {"edges":[]}` as an artifact with
  no dependents. The cases are therefore `200` with populated edges, `200` with
  `edges: []`, which the viewer presents as an absence of links rather than as an
  error, the `400` when `id` is absent, and `500 registry.unavailable`, which the
  viewer distinguishes from `200 {"edges":[]}`: it presents the surface's error
  state for the links and leaves the already-loaded artifact rendered, since the
  viewer issues `/v1/dependents` as a second call after `/v1/load_artifact` has
  already returned `200` and the brief requires a per-surface error state
  (`web/DESIGN.md:166-167`). That case is what a store failure produces, and
  without it a viewer that reports "no dependents" during an outage passes. A not-found artifact is a
  `/v1/load_artifact` case and surfaces there as `404 registry.not_found`
  (`pkg/registry/server/server.go:1403-1404`), covered by the next case.
- An end-to-end test of the artifact viewer's `/v1/load_artifact` failure arms,
  which are the viewer's primary call. Loading an id that does not exist and
  loading an id that exists but is invisible to the caller both return
  `404 registry.not_found` from the same `writeCoreError` arm
  (`pkg/registry/server/server.go:1403-1404`), and the viewer presents the same
  not-found state for both, with no rendered body, no property table, and no
  wording that implies the artifact exists, which is the disclosure requirement
  the brief states (`web/DESIGN.md:174-176`). A store failure surfaces through
  the default arm as `500 registry.unavailable` (`:1405-1406`), and the viewer
  presents its error state for that rather than the not-found state, so an
  outage does not read as a missing artifact. Without this case a viewer that
  renders an empty or default state for a 404 passes every other listed test
  while implying the artifact exists.
- End-to-end coverage of the artifact viewer's specified rendering, which §13.10
  states as "manifest body rendered as markdown, frontmatter as a property
  table" (`spec/13-deployment.md:167`) and which today is two raw `<pre>` blocks
  (`web/app.js:101-102`, `:105-106`). Loading an artifact whose `manifest_body`
  carries a heading, a fenced code block, and an `[a](https://example.com)` link
  renders each as the corresponding element rather than as escaped literal text,
  and whose `frontmatter` carries two keys renders a property table with one row
  per key. `frontmatter` arrives as a raw string on the `load_artifact` response
  (`pkg/registry/server/server.go:582`), so the table is what the viewer derives
  from that string. This case and the sanitization fixture above observe the two
  directions of the same renderer: without it, a renderer that escapes its whole
  input and emits the body as literal text satisfies every hostile fixture.
- End-to-end coverage of the layer panel's read surface, which §13.10 specifies
  as "list registered layers with their source, visibility, and
  `last_ingested_at`" (`spec/13-deployment.md:168`). With an admin-defined git
  layer and a user-defined layer registered, the panel's `GET /v1/layers` call
  renders each layer's source (`SourceType` with `Repo` or `LocalPath`), its
  `last_ingested_at`, and the full §4.6 visibility union, so a layer carrying
  both `Organization` and `Groups` shows both rather than one label. The
  assertion reads the emitted Go field names rather than the snake_case
  request-body names, which is the trap "The design handout" records, and a
  tenant with no layers renders the empty state rather than an error. The
  existing end-to-end citations under "The design handout" pin what the server
  emits; this test is what pins the panel's consumption of it.
- End-to-end coverage of the layer panel's write operations against the
  authorization boundary the registry enforces, which is the admin-defined
  against user-defined split alone. An authenticated non-admin's register is
  accepted as a user-defined layer with `Owner` and the implicit
  `users: [<registrant>]` set to the caller's subject, which is what
  `pkg/registry/server/layers.go:601-620` implements and what §13.10 states
  ("users can manage their own user-defined layers",
  `spec/13-deployment.md:168`). An anonymous caller's admin-defined register is
  refused with `403` and `auth.forbidden`. Unregister, update, restore, and
  reorder of an admin-defined layer by a non-admin are refused with `403` and
  `auth.forbidden`, which is the whole of the gate each one applies
  (`pkg/registry/server/layers.go:856-861`, `:494-499`, `:819-824`, `:905-910`).
  The same operations on a user-defined layer owned by another caller succeed
  with `200`, because no handler compares `cfg.Owner` to the caller, and
  `POST /v1/layers/reingest` calls no authorization function at all
  (`pkg/registry/server/layers.go:946-991`), which
  `test/integration/reingest_pipeline_test.go:87` already exercises with no
  credential. The panel therefore renders the admin-defined against user-defined
  split, and it does not present per-owner scoping on destructive operations as
  server-enforced. Decision 4 settles whether this proposal closes that gap.
- End-to-end coverage of the layer panel's reingest control, which §13.10 names
  beside register and unregister ("Admins can register, reingest, and unregister
  layers from the UI", `spec/13-deployment.md:168`) and which `web/DESIGN.md:139`
  records as long-running. The control issues
  `POST /v1/layers/reingest?id=<id>` for the selected layer, and the test
  asserts the panel's presentation of each response the handler produces
  (`pkg/registry/server/layers.go:946-991`). With a reingest runner wired the
  response is the §7.3.1 result summary, whose `accepted`, `idempotent`,
  `artifacts`, `conflicts`, `advisories`, `rejected`, `lint_failures`, and
  `embedding_failures` keys the handler writes
  (`pkg/registry/server/layers.go:1090-1101`), and the panel presents the
  outcome rather than the bare acknowledgement. With no runner wired the
  response is the queue-only `{"queued", "queued_at"}` body
  (`pkg/registry/server/layers.go:997-1002`), which the panel presents as an
  accepted request whose result is not yet known. The result summary carries
  `queued` and `queued_at` as well, so the panel distinguishes the two by the
  presence of the summary keys rather than by `queued`. An unknown id returns
  `404 registry.not_found` and a store failure `500 registry.unavailable`
  (`pkg/registry/server/layers.go:981-988`), and the panel presents each as the
  surface's error state rather than as a completed reingest, which is the same
  distinction the `/v1/dependents` case above draws. The pure-conflict
  `409 ingest.immutable_violation` envelope
  (`pkg/registry/server/layers.go:1056-1065`) is an ingest-pipeline outcome
  rather than a panel state, so it is asserted as the error state carrying the
  code. `test/integration/reingest_pipeline_test.go:87` pins what the endpoint
  emits; this test is what pins the panel's consumption of it.
- Coverage of the read-only mode presentation, since the registry can reject
  every write with `registry.read_only` while reads continue.
- An end-to-end test under `test/e2e/` that boots the binary with `--web-ui` and
  the chosen route's configuration keys, since those keys are boot-path behavior
  and `.claude/rules/test-coverage.md` puts configuration validation at that
  level. No existing test observes a web-UI startup refusal through the spawned
  binary: the bind-guard refusal is pinned by two in-process unit tests that
  call `Validate` directly (`pkg/registry/server/config_validate_test.go:173`,
  `internal/serverboot/webui_sign_config_test.go:27`), which supply the
  `errors.Is`-against-the-sentinel assertion form.
  `test/e2e/server_flag_behavior_test.go` covers `--web-ui` only as a mount test
  (`:18`, `:36`) and observes no web-UI startup refusal, while
  `TestServerFlags_SignRejectsUnknown` (`:116`) is the end-to-end form the new
  test follows: a non-zero exit and the code named on stderr. It asserts the
  observable
  startup result in four conditions: with the keys present the UI serves and
  offers sign-in; with `--web-ui` set, the browser sign-in flow enabled, and the
  route's remaining browser authentication keys absent, startup is refused with
  `config.web_ui_auth_unconfigured`, the guard specified under "The spec
  amendment"; with `--web-ui` set, an identity provider configured, no browser
  sign-in flow enabled, and no browser authentication keys set, the binary boots
  and serves the UI that inherits the request's resolved identity, which is the
  gateway-delegated deployment `trusted-headers` and a gateway-fronted
  `oidc-jwt` registry both run; and with an optional key omitted the default
  documented for that key applies, read from the section the edit-site list
  places it in (§6.3.4 for an acquisition option, §13.10 for a registry-process
  key). Each refusal condition is asserted on the spawned binary's observable
  result, which is a non-zero exit and the code named on stderr, rather than on
  a `Validate` call in the test process.
- Under route A2, a test for each of the two pieces of cross-request state
  decision 1 names. A sign-in whose redirect is issued by one registry instance
  completes when the IdP callback carrying the same `state` is delivered to a
  second instance, and a callback whose pre-authorization transaction cannot be
  resolved is refused rather than establishing a session. A session established
  against one registry instance authorizes a layer-panel write issued against a
  second.
- Under route A2, a test of the sign-out route the §7 edit site names. After a
  successful sign-in, a call to the sign-out route clears the session cookie,
  and a subsequent layer-panel write carrying the pre-sign-out session is
  refused rather than authorized. The refusal is asserted across two registry
  instances, so an invalidation that only takes effect in the process that
  served the sign-out is caught. Sign-out is a fail-closed path, because the
  session it ends authorizes destructive layer operations and the browser cannot
  clear an `HttpOnly` cookie itself.
- Under route A2, a test of the §6.3.1 per-request tenant selection the A2
  edit-site list stages, since a session-authenticated request carries neither
  the `org_id` claim nor `X-Podium-User-Org` that
  `spec/06-mcp-server.md:64` enumerates. The test asserts tenant selection on the
  catalog routes the §6.3.1 tenant router covers, which are the ones mounted
  behind the meta-tool server (`internal/serverboot/serverboot.go:1165-1170`,
  `:1239`): on a multi-tenant registry, a browser session established for a
  caller in one organization serves that organization's `/v1/load_domain` and
  `/v1/search_artifacts` view, and a session whose organization value resolves
  to no provisioned tenant is refused with the code the staged §6.3.1 sentence
  names rather than being served against another tenant. The deny arm is what
  makes this fail closed, because a session that falls through to another tenant
  exposes that tenant's catalog. The panel's layer list is outside this test,
  because `GET /v1/layers` performs no per-request tenant selection at all: it
  is mounted on the outer mux ahead of the tenant router
  (`internal/serverboot/serverboot.go:1220`) and every handler reads the boot
  default tenant captured at construction
  (`internal/serverboot/serverboot.go:1199`, `pkg/registry/server/layers.go:772`).
  Routing that endpoint through §6.3.1 resolution is a separate change with its
  own spec basis and is not staged here.

**IMPLEMENTOR'S CHOICE:** the level and file each UI-surface test lands at, which
depends on whether the React rewrite brings a browser test runner into the
repository. Any answer drives the served bundle's own API calls rather than
re-implementing them in the test, and every case above is asserted somewhere the
default `make test` run executes.

## Manual validation

**S44 has to move with this change.** `test/manual-validation.md` S44 currently
pins the anonymous behavior: it asserts that a directly reachable UI under
`oidc-jwt` shows public artifacts only, and it carries a "Known gap this
records" paragraph stating that in-browser authentication is deferred to its own
proposal. That paragraph exists so a later change to the UI has to move this
text with it, and this proposal is that change.

**S45 has to move with route A2.** Beyond the write-set assertion "The spec
amendment" stages, S45 step 4 asserts that `/v1/login`, `/v1/auth/token`, and
`/v1/token` return 404 "because the registry registers no auth, login, or token
route" (`test/manual-validation.md:4254-4266`). A2 adds sign-in, callback, and
sign-out routes, so the step is rewritten to probe the paths A2 serves and to
state the response each returns, with the reason clause narrowed to the write
endpoint the struck clause named. Under route A1 the registry adds no route and
the step stands as written.

New scenarios: a sign-in through the UI that yields a view an anonymous caller
does not get; the layer panel's register flow including the one-time secret; and
an unregister with its confirmation. Each names what a human reads on screen,
which is the class no Go test covers, as S44 already established for the
anonymous case.

## Non-goals

- Authoring or editing artifacts through the UI. Artifacts are authored in git,
  and §13.10 describes a reader and a layer manager.
- Any admin surface beyond the layer panel.
- Changing the catalog and `/v1/layers…` endpoints the UI calls. On the data
  plane it is a client of the existing HTTP API and gains no privileged access,
  which §13.10 states as "a thin client over the same `podium layer …` HTTP
  endpoints". The authentication routes route A2 adds are in scope and are
  settled by decision 1.
- Server-side identity filtering of `GET /v1/layers`, which returns the full
  layer list of the boot default tenant to every caller today, and per-request
  tenant selection on that endpoint, which it does not perform. Whether the proposal closes the
  separate layer-ownership gap on the write paths is decision 4 rather than a
  non-goal.
- The SDK half of the `DeviceCodeRequired` gap, which is a separate §6.3 client
  surface tracked on its own.

## Relationship to proposal 0012

0012 corrected §13's account of what the registry accepts and does. Its decision
3 verified that the shipped SPA attaches no credential, narrowed the web-UI
paragraph to state that plainly, and routed in-browser authentication here. The
sentence this proposal amends is the one 0012 wrote.
