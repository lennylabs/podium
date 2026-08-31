# Proposal 0014: Serve the web UI at /app/ and redirect the root to it

- Issue: (to be filed)
- Status: Implemented (2026-08-31). Signed off by the maintainer for
  implementation, whole, with every step in the checklist in scope. Converged
  after 4 adversarial review rounds (6 findings fixed); "Resolved in adversarial
  review" records what each pass changed.
- Date: 2026-08-31

This document stages the proposed spec, code, test, and documentation changes.
It does not modify any spec, code, or doc file. Apply the changes in the staged
sections after sign-off.

## Summary

**What changes.**

- §13.10's mount sentence moves the served single-page UI from `/ui/` to
  `/app/`, and gains one sentence stating that `GET /` redirects the browser to
  the UI on a process that mounts it. §7.3.4's callback sentence moves the
  declined-consent return target to `/app/`, and §6.3.4's illustrative browser
  origin follows so the spec names one UI root.
- `internal/serverboot` mounts the bundle at `/app/` and registers the root
  redirect inside the existing `--web-ui` block, so the redirect exists exactly
  where the UI exists.
- The Vite public base moves to `/app/` and the committed bundle under
  `web/bundle` is regenerated, because the emitted asset references are absolute
  and are resolved at compile time by `//go:embed`.
- The two runtime navigations to the literal follow: the callback's redirect
  constant in `pkg/registry/server` and the SPA's post-sign-out navigation in
  `web/ui/src`.
- The assertions follow the mount: every existing pin on the old literal moves
  to `/app/`, end-to-end cases pin the root redirect, its conditionality on
  `--web-ui`, and the absence of any answer at `/ui/`, and a component
  assertion pins the SPA's post-sign-out destination.
- The operator-facing text follows: the `--web-ui` flag help in `cmd/podium`,
  the two `docs/reference` pages, the hand-run scenarios in
  `test/manual-validation.md`, and a `CHANGELOG.md` breaking-change entry.

**Fixed decisions.**

- The browser-flow routes keep their paths: `/v1/ui/auth/sign-in`,
  `/v1/ui/auth/callback`, `/v1/ui/auth/sign-out`, and `/v1/ui/session`.
- `/ui/` stops serving outright. No alias, no redirect from it, no legacy flag,
  and no configuration key restores it.
- `GET /` redirects only on a process started with `--web-ui`. A process without
  the flag keeps the answer it gives today.
- The redirect registers as the exact-root pattern `GET /{$}` inside the
  existing `if cfg.webUI` block, never by editing the meta-tool catch-all.
- The mount path stays fixed by the spec. §13.10's web-UI configuration keys
  table gains no base-path variable.
- The Vite base and the committed bundle move in the same commit as the Go
  mount, and the rebuild that regenerates the bundle runs after every `web/ui`
  source edit.
- The change adds no §6.10 error code and no matrix cell, so `matrix-audit` and
  `speccov-drift` gain no obligation.

**Watch out for.**

- **A Go-only move ships a blank page.** `web/bundle/index.html` references
  `/ui/assets/index-rSUh_J9i.js` and `/ui/assets/index-DBtk1G5x.css`, and the
  bundle CSS references three `/ui/assets/*.woff2` faces. `//go:embed all:bundle`
  resolves those bytes at compile time, so nothing rewrites them at runtime.
- **The CI rebuild gate makes the bundle non-optional.**
  `.github/workflows/test.yml` runs `npm ci && npm run build` and then
  `git diff --exit-code` plus `git status --porcelain`. A `vite.config.ts` edit
  or a `web/ui/src` edit that ships without the regenerated bundle fails that
  gate, which is why the source edits and the rebuild land together and why the
  rebuild is the last action of S4: the emitted JS carries the sign-out literal
  CODE-4 moves, so a build taken before that edit commits a bundle that no longer
  matches its source.
- **A second `mux.Handle("/", …)` panics at registration.** The meta-tool
  catch-all already holds `"/"`. Editing it in place would swallow the catch-all's
  answer for every unmatched path, and a redirect handler on the bare `"/"`
  pattern would also redirect `/ui/` to `/app/`, which is the compatibility shim
  the pre-1.0 rule in `.claude/rules/code-best-practices.md` forbids.
- **Two tests look like pins and are not.**
  `pkg/registry/server/security_headers_test.go:22` passes `/ui/` as an arbitrary
  target into a synthetic handler with no routing, and the middleware never reads
  `r.URL`, so it stays green either way. `web/ui/src/markdown.test.tsx:211-213`
  asserts that an absolute same-origin link is preserved as authored, which holds
  for any absolute path. Both move for accuracy rather than for coverage.
- **One test fails open rather than closed.**
  `test/e2e/web_ui_bundle_test.go:231` requests `/ui/` and asserts the security
  headers. `SecurityHeaders` wraps the whole mux and ignores the path, so an
  unmoved literal there would assert the headers against the catch-all's 404 and
  stay green while covering nothing about the UI document.
- **`cmd/podium/serve_ui_test.go` runs in-process rather than as a subprocess.** Its
  poll loop breaks on any successful HTTP exchange, including a 404. Its
  fail-closed property comes solely from the `<title>Podium</title>` body check.
- **The e2e helpers follow redirects.** `getRaw` and `getStatus` set no
  `CheckRedirect`, so a root-redirect assertion built on them reads the followed
  `200` from `/app/` and cannot distinguish a redirect from a direct serve. A
  no-follow client already exists in the same file.
- **An in-process twin in `internal/serverboot` would prove nothing.**
  `internal/serverboot/webui_auth_integration_test.go` hand-assembles its own
  mux and re-registers the routes, so a redirect case placed there would assert
  against the test's own registration rather than against the boot path's.

## Implementation checklist

- [x] **S1 · spec** — SPEC-1. §13.10's mount sentence moves to `/app/` and gains
      the root-redirect sentence and its unmounted case.
      Levels: —. Depends on: —
- [x] **S2 · spec** — SPEC-2. §7.3.4's declined-consent return target moves to
      `/app/`, leaving the four route paths verbatim.
      Levels: —. Depends on: S1
- [x] **S3 · spec** — SPEC-3. §6.3.4's illustrative browser origin moves to
      `/app/` so the spec names one UI root.
      Levels: —. Depends on: S1
- [x] **S4 · code** — CODE-1, CODE-2, CODE-3, CODE-4, CODE-5, CODE-6, TEST-1,
      TEST-3. The whole path move: the mount and the root redirect, the Vite
      base, the callback redirect constant, the SPA's post-sign-out navigation
      and the assertion that pins it, the flag help, every existing assertion on
      the old literal, and finally CODE-6's bundle rebuild, which runs last so it
      compiles sources that have all moved. These are one commit
      because the tree is red at every intermediate point: the bundle's absolute
      references, the Go mount, and roughly thirty assertions all pin the same
      literal, and the CI rebuild gate fails a source edit that ships without the
      regenerated bundle.
      Levels: unit, integration, e2e. Depends on: S1, S2, S3
- [x] **S5 · test** — TEST-2. The end-to-end cases that pin the root redirect,
      its conditionality on `--web-ui`, and the absence of any answer at `/ui/`.
      Levels: e2e. Depends on: S4
- [x] **S6 · docs** — DOCS-1. The `--web-ui` row in the CLI reference and the
      callback paragraph in the HTTP API reference.
      Levels: —. Depends on: S4
- [x] **S7 · docs** — DOCS-2. The eight hand-run scenario URLs in
      `test/manual-validation.md`.
      Levels: —. Depends on: S4
- [x] **S8 · docs** — DOCS-3. The `CHANGELOG.md` breaking-change entry.
      Levels: —. Depends on: S4

## Current state and the gap

The §13.10 web UI is served at `/ui/` and `GET /` answers `404`. The maintainer
wants the UI at `/app/` and the root redirecting to it on any process that mounts
the UI.

The path is fixed in the spec at three sites. §13.10 carries the only normative
mount sentence, "the same process exposes a single-page web UI at
`http://<bind>/ui/`". §7.3.4 carries the only normative statement of where the
callback returns the browser, "returns the browser to the web UI root at `/ui/`".
§6.3.4 carries an illustrative browser origin, `https://registry.acme.com/ui/`,
inside the cross-site-evidence bullet. Those are the three occurrences of the
path in `spec/`; the `/v1/ui/…` route names are a different prefix and stay.

Nothing in `spec/` states what `GET /` answers, and §6.10 assigns no code to an
unmatched path. Today's `404` is an incidental product of the meta-tool catch-all
at `internal/serverboot/serverboot.go:1320`, so the root redirect is a new spec
statement rather than an amendment to an existing one, and its conditionality on
`--web-ui` needs stating because no section describes the root under either
configuration.

In code the change is wider than the single mount line at
`internal/serverboot/serverboot.go:1269`. `web/ui/vite.config.ts:14` sets
`base: '/ui/'`, and the committed bundle emits absolute references
(`/ui/assets/index-rSUh_J9i.js`, `/ui/assets/index-DBtk1G5x.css`, and three
`/ui/assets/*.woff2` faces inside the CSS), all resolved at compile time by
`//go:embed all:bundle` in `web/web.go`. A Go-only move therefore serves an index
whose every asset returns `404` and whose `<div id="root">` stays empty. Two
runtime sites navigate a live browser to the literal: `const webUIRoot = "/ui/"`
in `pkg/registry/server/webui_auth.go`, used on the declined-consent arm and on
the success arm of the callback, and `window.location.assign('/ui/')` in
`web/ui/src/App.tsx`, the success continuation of the sign-out control.

`/app` and `/app/` are unclaimed. The outer mux registers `/v1/layers`,
`/v1/layers/`, `/v1/ingest/webhook/`, `/v1/admin/erase`, the web-UI block, a
conditional `/metrics`, and the `"/"` catch-all; the inner registry handler
registers `/healthz`, `/readyz`, the `/v1/*` meta-tool routes, and conditionally
`/scim/v2/` and `/objects/`. Registering `/app/` takes the path away from the
catch-all rather than filling an unrouted hole.

## Decisions

**The browser-flow routes stay verbatim.** `/v1/ui/auth/sign-in`,
`/v1/ui/auth/callback`, `/v1/ui/auth/sign-out`, and `/v1/ui/session` keep their
paths and their constants. They are versioned API routes, and
`PODIUM_WEB_UI_REDIRECT_URI` names the callback as the URI an operator registers
with the identity provider, which §6.3.4 requires byte-identical on both legs of
the exchange. The accepted consequence is that a deployment serves its UI at
`/app/` while the browser flow's API stays under `/v1/ui/`.

**Keeping those routes does not exempt §7.3.4 from edit.** The same subsection
fixes the callback's return destination as the web UI root, mirrored by
`webUIRoot` in `pkg/registry/server/webui_auth.go`. That literal and its mirror
move together, per the mirrored-surfaces rule.

**`/ui/` stops serving outright.** Podium is pre-1.0 and the code rules forbid
compatibility shims, dual code paths, and legacy flags. §13.10's web-UI
configuration keys table exposes no mount-path key, so no configuration restores
the old path, and the cost is
carried by the changelog and a MINOR bump.

**`GET /` redirects only where the UI is mounted.** A registry started without
`--web-ui` keeps the root's current answer, because redirecting to a path that
would itself return `404` turns a clear refusal into a confusing one. The
conditionality comes free from registering the redirect inside the existing
`if cfg.webUI` block.

**The redirect registers as `GET /{$}`.** A second `mux.Handle("/", …)` conflicts
with the catch-all and panics at registration, so the process would not boot.
Editing the catch-all in place would swallow the meta-tool handler's answer for
every unmatched path. A redirect handler matching the whole `"/"` subtree would
also redirect `/ui/`, which is the shim the decision above forbids. `go.mod`
declares `go 1.26.3`, so `{$}` is available, and `"GET /{$}"` matches a strict
subset of `"/"` and therefore wins for `GET /` while every other method and every
other path stays on the catch-all. Go's `ServeMux` matches a `GET` pattern for
`HEAD` as well, so the redirect also answers `HEAD /`.

**The Vite public base and the committed bundle move with the mount.** The
bundle is resolved by `//go:embed` at compile time, so a Go-only change serves an
index whose assets return `404`. Both content hashes change on rebuild, because
the CSS carries the font references and the JS carries the sign-out navigation
literal.

**`http.ServeMux` supplies `/app` to `/app/` automatically.** It does so exactly
as it does for `/ui` today, so the trailing-slash redirect needs no registration.

**The change adds no §6.10 error code and no matrix cell.** The current `404` at
`/` is unspecified behavior produced by the catch-all, and the redirect is a
`3xx` rather than an error envelope, so `matrix-audit` gains no obligation.
`speccov-drift` gains none either: it works at section granularity and §13.10,
§7.3.4, and §6.3.4 each already carry citing tests.

**The SPA needs no path change beyond the sign-out navigation.** Its API client
spells absolute `/v1/…` paths and its routing is hash-based, so the bundle is
mount-agnostic once the build base moves.

**Both middlewares admit the redirect, for different reasons.** `SecurityHeaders`
(`pkg/registry/server/security_headers.go:56`) wraps the whole mux, reads nothing
from the request, and sets its headers on every response, so the redirect
inherits them. `BrowserOriginGate`
(`pkg/registry/server/browser_origin_gate.go:28-41`) is method-first: its opening
predicate is `!stateChanging(r.Method) || gateExcluded(r.URL.Path)`, and `GET`
and `HEAD` are non-state-changing, so the gate admits the redirect before the
path term is evaluated. The gate does read `r.URL.Path`, for the §6.3.4 by-name
exclusion of `/v1/ui/auth/sign-in` and `/v1/ui/auth/callback`
(`browser_origin_gate.go:60-62`), and neither of those paths moves, so the mount
move requires no gate change.

## Spec amendment: §13.10 the served UI path and the root redirect

**SPEC-1.** Anchor: `spec/13-deployment.md`, §13.10, the paragraph beginning
`**Web UI.**`, which today opens "When `podium serve` (standalone or standard) is
started with `--web-ui` (or `PODIUM_WEB_UI=true`), the same process exposes a
single-page web UI at `http://<bind>/ui/`." Replace the paragraph's opening
sentence and append one sentence after it. The surfaces list that follows the
paragraph, the authentication paragraph, the bind-guard paragraph, the
browser-flow configuration guard, and the web-UI configuration keys table are
untouched.

The paragraph's opening becomes:

> **Web UI.** When `podium serve` (standalone or standard) is started with
> `--web-ui` (or `PODIUM_WEB_UI=true`), the same process exposes a single-page
> web UI at `http://<bind>/app/`. On that process `GET /` redirects the browser
> to `/app/`. A process started without the flag mounts neither the UI nor that
> redirect, and a request for `/` on it is answered as any path the registry does
> not register is answered on that deployment. The UI is a static SPA bundled
> into the binary; it talks to the registry's HTTP API as any other consumer
> would. What it surfaces:

The closing clause reuses the formulation §7.3.4 already uses for its
unregistered routes, so the spec states the unmounted case in the words it
already has for it.

## Spec amendment: §7.3.4 the callback's declined-consent return target

**SPEC-2.** Anchor: `spec/07-external-integration.md`, §7.3.4, the paragraph
describing `GET /v1/ui/auth/callback`, in the sentence that begins "A query
carrying that parameter runs no exchange". One literal changes:

> A query carrying that parameter runs no exchange, returns the browser to the
> web UI root at `/app/` without establishing or replacing a session, and takes
> no error code.

The route paths in the same subsection are unchanged. The success path's redirect
is not spelled in §7.3.4 prose, which states only that the callback returns the
access token in the `__Host-podium_session` cookie, so the success target moves
in code alone.

## Spec amendment: §6.3.4 the illustrative browser origin

**SPEC-3.** Anchor: `spec/06-mcp-server.md`, §6.3.4, the bullet titled "**What
counts as cross-site evidence.**", in the clause "where a browser on
`https://registry.acme.com/ui/` sends `Origin: https://registry.acme.com` to a
registry whose own request scheme is `http`". The path becomes
`https://registry.acme.com/app/`.

Nothing else in the bullet changes. The gate predicate, the scheme argument, and
the §13.10 redirect-URI reference are unaffected, because the gate reads `Origin`
and `Host` and never the path. Without this edit the spec names two different UI
roots in two sections, and the illustration describes a browser position no
deployment produces.

## Proposed solution

### CODE-1: the mount and the root redirect

`internal/serverboot/serverboot.go`, the `if cfg.webUI` block that today holds
the single `mux.Handle("/ui/", …)` registration and its log line:

```go
if cfg.webUI {
    mux.Handle("/app/", http.StripPrefix("/app/", http.FileServer(http.FS(web.Assets()))))
    // §13.10: the root redirects to the UI, and only on a process that
    // mounts it, because a redirect to a path that would itself 404 turns
    // a clear refusal into a confusing one. The pattern is the exact-root
    // form: a bare "/" conflicts with the meta-tool catch-all registered
    // below and panics at registration, and a handler matching every path
    // under "/" would redirect the retired /ui/ as well. The GET pattern
    // also answers HEAD, which is what a browser preflighting the root
    // sends; every other method at "/" stays on the catch-all.
    mux.Handle("GET /{$}", http.RedirectHandler("/app/", http.StatusFound))
    log.Printf("web UI mounted at /app/")
    ...
}
```

The nested `if cfg.webUIAuth` block that mounts the §7.3.4 routes is unchanged.
The `webUI` field comment on the config struct, which today reads "mounts the
§13.10 single-page web UI at /ui/", names `/app/`.

The registration order and pattern set were exercised on an `http.ServeMux` to
confirm the behavior rather than inferred: registration does not panic, `GET /`
answers `302` to `/app/`, `HEAD /` answers the same, `POST /` reaches the
catch-all, `GET /nope` reaches the catch-all, `GET /app` takes the mux's
automatic `307` to `/app/`, and `GET /ui/` reaches the catch-all.

### CODE-2: the Vite public base

`web/ui/vite.config.ts`, the header comment and the `base` value:

```ts
// The registry serves the bundle at /app/ behind http.StripPrefix, and the
// outer mux routes every other path to the meta-tool handler, so an asset
// reference rooted at / returns 404 and the page renders blank. Setting the
// public base to /app/ makes every emitted reference resolve under the mount.
…
  base: '/app/',
```

The rebuild that regenerates `web/bundle` is not part of this deliverable. It
runs once, as the last action of S4, after every `web/ui` source edit has landed,
and it is staged as CODE-6 below. Compiling the bundle here would emit a JS chunk
still carrying CODE-4's unmoved sign-out literal, and the CI rebuild gate fails
the resulting mismatch.

`web/web.go`, the package doc comment ("mount it at /ui/") and the `Assets` doc
comment ("serve the UI at /ui/"), name `/app/`.

### CODE-3: the callback's redirect constant

`pkg/registry/server/webui_auth.go`:

```go
// webUIRoot is where the callback returns the browser on success and on a
// declined consent prompt (§7.3.4).
const webUIRoot = "/app/"
```

The two `http.Redirect(w, r, webUIRoot, http.StatusFound)` call sites are
unchanged, and so are the four route constants. Because the auth routes register
only inside the `cfg.webUI` block, and the §13.10 guard makes web-UI enablement a
conjunct of the browser flow, `webUIRoot` can never resolve on a process that did
not mount the UI.

### CODE-4: the SPA's post-sign-out navigation

`web/ui/src/App.tsx`, the success continuation of `signOut(path).then(…)` inside
`SignOutButton`, becomes `window.location.assign('/app/')`. This is the only
non-hash `window.location` navigation to a literal path in the SPA; the remaining
uses are `.reload()`, `.host`, and `.hash`.

The prose that names the mount follows: the comment in `web/ui/src/markdown.ts`,
the font note in `web/ui/src/fonts/README.md`, the matched font note in
`web/ui/src/index.css` (the CSS minifier strips it, so the emitted bytes and the
content hashes are unaffected), the comment in `web/ui/src/surfaces.test.tsx`, and
the two fixture strings in `web/ui/src/markdown.test.tsx` at `:325` and `:355`.
The fixture at `web/ui/src/markdown.test.tsx:211-213` moves for the same reason;
its assertion holds for any absolute same-origin path and is not a pin on the
mount.

The behavioral edit is pinned by a new assertion, staged under Testing.

Two mount-agnostic alternatives were considered and rejected. `assign('/')` adds
a redirect hop to every sign-out and couples the SPA to a redirect that exists
only under `--web-ui`. A relative `assign('./')` depends on the current hash
route. The explicit literal matches what the build base and the flag help do.

### CODE-5: the flag help

`cmd/podium/serve.go`:

```go
// §13.10 Web UI: opt-in single-page UI at /app/, with GET / redirecting to
// it. The non-loopback bind is refused unless --web-ui-allow-public-bind is
// also set and an identity provider is configured (serverboot validates this).
webUI := fs.Bool("web-ui", false, "mount the bundled web UI at /app/ (overrides PODIUM_WEB_UI)")
```

The other web-UI flags carry no path and are unchanged.

### CODE-6: the bundle rebuild

This is the last action of S4, after CODE-2's `base` value and every CODE-4 edit
under `web/ui/src` are in the working tree. In `web/ui`, run
`npm ci && npm run build`, which rewrites `web/bundle/index.html` and
`web/bundle/assets/` under new content hashes, and commit the result in the same
commit as the source edits. Do not hand-edit `web/bundle`.

The order is load-bearing in one direction only. The emitted JS chunk carries the
sign-out navigation literal CODE-4 moves, which `grep -c "/ui/"
web/bundle/assets/index-rSUh_J9i.js` reports today, so a build taken before
CODE-4 lands produces a committed bundle that no longer matches its source, and
`.github/workflows/test.yml` fails the tree on the rebuild gate. A build taken
after every source edit is correct whatever order the source edits themselves
took.

## Edge cases and accepted failure modes

| Case | Observable outcome | Where it is stated |
|:--|:--|:--|
| `GET /` on a process started with `--web-ui` | `302 Found` with `Location: /app/` | §13.10, "On that process `GET /` redirects the browser to `/app/`"; `docs/reference/cli.md` `--web-ui` row |
| `HEAD /` on the same process | The same `302`, because Go's mux matches a `GET` pattern for `HEAD` | §13.10 names `GET /`, and states no `HEAD` requirement; `http.ServeMux` answers `HEAD` from the `GET` pattern, so the same handler answers the browser's other safe navigation method. Pinned by `TestServe_WebUI_RootRedirect` |
| `POST /` and every other method at `/` | Unchanged: the meta-tool catch-all's answer | §13.10 names `GET /` and nothing else; nothing in §6 or §7 registers a root route for another method. Pinned by `TestServe_WebUI_RootRedirect` |
| `GET /` on a process started without `--web-ui` | Unchanged `404` from the catch-all | §13.10, "a request for `/` on it is answered as any path the registry does not register is answered on that deployment" |
| `GET /ui/` on any process after the change | The catch-all's answer, with no redirect and no alias | §13.10 names one mount; the retirement is recorded in `CHANGELOG.md` |
| `GET /app` without the trailing slash | `307` to `/app/`, supplied by `http.ServeMux` | Accepted framework behavior, identical to what `/ui` does today |
| An operator's reverse-proxy rule or bookmark naming `/ui/` | Breaks at upgrade, with no configuration that restores it | `CHANGELOG.md` breaking-change entry; §13.10's web-UI configuration keys table exposes no mount-path key |
| A registered OAuth redirect URI | Unaffected. `PODIUM_WEB_UI_REDIRECT_URI` names `/v1/ui/auth/callback`, which contains `/ui/` as a substring and does not move | §7.3.4's route paths are unchanged; `docs/reference/http-api.md` route names are unchanged |
| A deployment upgraded without the regenerated bundle | Not reachable through a merged commit: the CI rebuild gate fails a source edit that ships without it | `.github/workflows/test.yml` runs the build and then `git diff --exit-code` |
| The browser-origin gate on the redirect | Admitted, because the redirect answers on `GET` and the gate refuses only state-changing methods carrying cross-site evidence | §6.3.4, "What counts as state-changing" |

## Testing

**TEST-1: move the existing assertions to `/app/`.** These are the fail-closed
pins on the current mount, and they move with it.

- `test/e2e/server_flag_behavior_test.go` at `:20`, `:29-31`, `:38`, `:45-46`,
  and `:209`. Moving the pair at `:38-46` to `/app/` means it no longer says
  anything about `/ui/`, so the "the old mount is retired" assertion passes to
  TEST-2's table. The two land together.
- `test/e2e/web_ui_bundle_test.go` at `:5`, `:19`, `:22`, `:32-34`, `:38`, `:48`,
  `:58-59`, `:64`, `:66`, `:92-94`, `:146`, `:155-157`, `:176`, `:197`, and
  `:231`. The last is the one that fails open rather than closed: it asserts the
  security headers on a request to the mount, and the middleware wraps the whole
  mux and ignores the path, so an unmoved literal would assert those headers
  against the catch-all's `404` and stay green while covering nothing about the
  UI document. `:64-66` (`bundleAssetURL`'s `strings.TrimPrefix(ref, "./")` and
  `strings.HasPrefix(ref, "/ui/")`) fails only once the bundle is regenerated,
  which is what makes the rebuild load-bearing rather than additive.
- `test/e2e/web_ui_surfaces_test.go` at `:35-37`, `:73-75`, `:145-147`,
  `:182-184`, and `:226`.
- `cmd/podium/serve_ui_test.go` at `:16` and `:30`. This test runs the server
  in-process, and its poll loop breaks on any successful exchange including a
  `404`, so its fail-closed property comes solely from the
  `<title>Podium</title>` body check. Do not weaken that check believing the
  status carries it.
- `pkg/registry/server/webui_auth_test.go` at `:203-204` and `:315-316`, which
  pin `Location: /ui/` for the success and declined-consent callback arms and
  follow CODE-3.
- `web/web_test.go` at `:15`, `:27-28`, `:43`, `:88`, and `:94`. The last is
  `bundlePath`'s `strings.CutPrefix(ref, "/ui/")`, the embed-side counterpart of
  the served-side helper above, and it fails on the same rebuild.
- `pkg/registry/server/security_headers_test.go:22`. This is an accuracy-only
  edit outside the fail-closed set: the literal is an arbitrary
  `httptest.NewRequest` target with no routing behind it, and the middleware never
  reads the path.

**TEST-2: pin the root redirect (e2e).** The change reaches the compiled binary's
boot path, so the level is end-to-end. In
`test/e2e/server_flag_behavior_test.go`, beside the existing `--web-ui` pair, add
a file-local no-follow client modeled on the one already in that file, because
`getRaw` and `getStatus` set no `CheckRedirect` and would read the followed `200`
from `/app/`:

```go
// requestNoFollow issues one request without following the redirect, so a
// case distinguishes a 302 at / from a direct serve at /app/. It carries a
// method parameter because the staged pattern is method-scoped and both
// halves of "GET /{$}" have to be pinned.
func requestNoFollow(t *testing.T, method, url string) (int, string)

// Spec: §13.10 — with the UI mounted, GET / redirects the browser to it,
// and every other unmatched path stays on the meta-tool catch-all. The
// /ui/ row is the pin on the no-shim decision: a redirect registered on a
// bare "/" pattern would bounce the retired mount and resurrect the alias.
// The HEAD and POST rows pin the method half of the pattern: a bare
// "/{$}" registration passes every path row while answering 302 to POST.
func TestServe_WebUI_RootRedirect(t *testing.T) {
    srv := startServerArgs(t, ..., "serve", "--standalone", "--web-ui", "--layer-path", reg)
    // GET  "/":     302 with Location: /app/
    // HEAD "/":     302 with Location: /app/
    // POST "/":     no 3xx and no Location, the same answer POST /nope gives
    // GET  "/ui/":  no 3xx and no 200
    // GET  "/nope": no 3xx and no 200
}
```

`/ui/` and `/nope` are one assertion at two paths, because after CODE-1 both are
unmatched paths on the same catch-all. Keeping the `/ui/` row is what catches a
redirect registered on the bare `"/"` pattern. The `HEAD` and `POST` rows are the
matching pin on the pattern's method half: both outcomes are behavior this change
introduces at a path that answers uniformly today, and without them an
implementor who registers the bare `"/{$}"` passes the whole table while `POST /`
redirects, contradicting the edge-case row above. The `POST` row asserts the
catch-all's answer by comparing against `POST /nope` on the same process, so it
pins the delegation rather than a status the catch-all is free to change.

The conditionality case needs no new function.
`TestServerFlags_NoWebUIByDefault` already starts exactly the required
no-web-UI process, so it gains one assertion:

```go
// Spec: §13.10 — the redirect mounts with the UI, so this process answers
// / as it answers any path it does not register.
if st, _ := requestNoFollow(t, http.MethodGet, srv.BaseURL+"/"); st/100 == 3 { t.Errorf(...) }
```

No in-process twin is added in `internal/serverboot`. The integration test file
there hand-assembles its own mux and re-registers the routes, so a case placed
beside it would assert against the test file's own registration rather than
against the boot path's conditional block. The coverage rules already designate
the end-to-end test as the correct level for boot-path code and direct keeping it
even where the default coverage profile does not move.

**TEST-3: pin the post-sign-out destination (unit, component).** Extend the
existing case in `web/ui/src/surfaces.test.tsx` that asserts sign-out is issued
as a POST to the read's `sign_out_path`, with a stub over
`window.location.assign` and one assertion that it was called with `/app/`.
Nothing in the tree pins this navigation today, so without it CODE-4's only
behavioral edit is unverified in both directions: nothing fails if the literal is
left at `/ui/`, and nothing fails if it later regresses. This test is part of
CODE-4's commit.

## Manual validation

**DOCS-2.** `test/manual-validation.md` carries eight operator instructions to
open the mount in a browser, and each UI scenario fails at step one if left
unchanged. The surface a human reads directly is the browser's address bar and
the rendered page; the wrong output each substitution catches is a `404` from the
meta-tool catch-all where the scenario expects the UI document.

The substitutions preserve the port and the hash route:

| Line | Scenario | Change |
|:--|:--|:--|
| 4223 | S44 | `http://127.0.0.1:8153/ui/` becomes `.../app/` |
| 4687 | S47 | `http://127.0.0.1:8153/ui/` becomes `.../app/` |
| 4692 | S47 | The post-login return sentence names `http://127.0.0.1:8153/app/`, mirroring CODE-3 |
| 4935 | S50 | `http://127.0.0.1:8153/ui/` becomes `.../app/` |
| 5141 | S51 | `http://127.0.0.1:8462/ui/` becomes `.../app/` |
| 5239 | S52 | `http://127.0.0.1:8462/ui/` becomes `.../app/` |
| 5297 | S53 | `http://127.0.0.1:8462/app/#/artifact/eng%2Fplatform%2Fdeploy-runbook` |
| 5435 | S54 | `http://127.0.0.1:8462/app/#/layers` |

Every `/v1/ui/…` occurrence in the file is left untouched, at lines 4004, 4156,
4198, 4380, 4667, 4672-4673, and 4947.

No scenario is added. S47's sign-out step asserts the anonymous view and names no
URL, so CODE-4 creates no further obligation in this file, and a hand-run
duplicate of the root redirect would repeat what TEST-2 pins automatically.

## Documentation changes

**DOCS-1.** Two pages carry the path. `grep -rn "/ui" docs/ --include='*.md'`
excluding `/v1/ui/` returns exactly them.

`docs/reference/cli.md`, the `--web-ui` row:

> | `--web-ui` | Mount the bundled web UI at `/app/`, and redirect `GET /` to it. Overrides `PODIUM_WEB_UI`. |

`docs/reference/http-api.md`, the callback paragraph: "returns the browser to
`/ui/` without establishing or replacing a session" becomes "returns the browser
to `/app/` …". The `/v1/ui/auth/*` and `/v1/ui/session` route names on that page
are unchanged.

`tools/doccov/manifest.yaml` names no example that exercises the UI path, so no
doccov entry and no new runnable example is created.

**DOCS-3.** `CHANGELOG.md` gains a `### Changed` block above the existing
`### Fixed` in `## [Unreleased]`:

> ### Changed
>
> - **Web UI path**: the bundled web UI is served at `/app/` instead of `/ui/`,
>   and a registry started with `--web-ui` redirects `GET /` to it. `/ui/` is no
>   longer served and no alias replaces it, so a reverse-proxy rule or a bookmark
>   naming the old path must be updated. The browser-flow routes are unchanged:
>   `/v1/ui/auth/sign-in`, `/v1/ui/auth/callback`, `/v1/ui/auth/sign-out`, and
>   `/v1/ui/session` keep their paths, so no identity-provider client
>   configuration and no registered redirect URI changes. A registry started
>   without `--web-ui` answers `GET /` exactly as before.

The release itself follows the release process as a MINOR bump, which is what the
pre-1.0 rule assigns to a backward-incompatible change.

## Open questions

- **Redirect status code.** The staged code uses `302 Found`, matching the status
  the callback already uses and leaving nothing cacheable behind on a deployment
  that later drops `--web-ui`. `308 Permanent Redirect` would let a browser cache
  the hop, at the cost of a stale redirect surviving a configuration change on
  the same origin. Recommendation: `302`.
- **Whether the redirect answers methods other than GET.** The staged pattern is
  `"GET /{$}"`, which Go also matches for `HEAD`, and leaves `POST`, `PUT`, and
  the rest at `/` on the catch-all where they land today. The alternative, a bare
  `"/{$}"`, would redirect every method at the root and change one more behavior
  than the problem asks for. Recommendation: `GET`, and the `HEAD` it implies.

## Non-goals

- Moving the browser-flow routes. `/v1/ui/auth/sign-in`, `/v1/ui/auth/callback`,
  `/v1/ui/auth/sign-out`, and `/v1/ui/session` keep their paths and their
  constants.
- Any compatibility route at `/ui/`: no alias mount, no redirect, no legacy flag,
  and no configuration key that restores the old path.
- A configurable UI mount path. §13.10's web-UI configuration keys table gains no
  base-path variable; `/app/` is fixed by the spec the way `/ui/` was.
- Redirecting `GET /` on a registry started without `--web-ui`. That process
  keeps the answer it gives today.
- Changing what any path other than `/` and `/ui/` answers. `/nope`, `/api`, and
  every other unmatched path stay on the meta-tool catch-all.
- Changing the SPA's hash routing, its `/v1/…` API paths, the browser-origin
  gate, the security headers, or the content-security policy.
- Adding a §6.10 error code, a meta-tool, an SPI, an environment variable, or a
  matrix cell.
- Rewriting §13.10's authentication paragraph, its bind guard, its browser-flow
  configuration guard, or its web-UI configuration keys table. Their predicates,
  errors, messages, and key rows are untouched.

## Resolved in adversarial review

Review rounds populate this section. The draft has been through one challenge
pass, which confirmed every anchor and produced the corrections that are already
folded into the text above: the root-pattern behavior was verified by running the
pattern set on an `http.ServeMux` rather than reasoned about, the `HEAD` clause in
CODE-1's comment was corrected, `web/ui/src/index.css` was added to CODE-4,
`web/bundle` test line `:231` was added to TEST-1, the `cmd/podium/serve_ui_test.go`
caution was corrected to name the body check as the fail-closed mechanism, the
in-process twin was dropped from TEST-2 as proving nothing about the boot path,
the claim that the §13.10 amendment creates a `speccov-drift` obligation was
withdrawn, and the optional extra manual-validation step was dropped as a
duplicate of an automated assertion.

### Pass 2 (2026-08-31, automated)

- The Decisions bullet claiming that `SecurityHeaders` and `BrowserOriginGate`
  both "never read `r.URL`" was false for the gate, which dispatches on
  `!stateChanging(r.Method) || gateExcluded(r.URL.Path)`
  (`pkg/registry/server/browser_origin_gate.go:30`) and compares the path by name
  against the §6.3.4 exclusions (`:60-62`). The bullet now states the two
  mechanisms separately: `SecurityHeaders` reads nothing from the request, and
  the gate is method-first and admits `GET` and `HEAD` before the path term is
  evaluated. The conclusion that the mount move needs no gate change is
  unchanged, and it now rests on the predicate that actually carries it.
- The `HEAD /` edge-case row justified itself with "§13.10's redirect sentence
  names no method", which contradicts SPEC-1's staged sentence "On that process
  `GET /` redirects the browser to `/app/`" and contradicted the adjacent `POST /`
  row. SPEC-1 keeps `GET /`, which is what `docs/reference/cli.md` and the
  `CHANGELOG.md` entry also state, and both rows now derive their outcome from
  that one wording: the spec names `GET /` and states no `HEAD` requirement, and
  `http.ServeMux` answers `HEAD` from a `GET` pattern.
- TEST-2's assertions pinned only the path half of the `"GET /{$}"` pattern. Both
  method-scoped outcomes the change introduces are now pinned in the same
  end-to-end function: `HEAD /` asserts the `302` to `/app/`, and `POST /` asserts
  the catch-all's answer by comparison with `POST /nope`. The helper is restated
  as `requestNoFollow(t, method, url)` so both rows and the conditionality case in
  `TestServerFlags_NoWebUIByDefault` share it.
- CODE-2 staged the `npm run build` before CODE-4 edited the `web/ui/src` sources
  the build compiles, which would commit a bundle whose sign-out literal no longer
  matched its source and fail the rebuild gate in
  `.github/workflows/test.yml`. The rebuild is now its own deliverable, CODE-6,
  stated as the last action of S4 after every `web/ui` source edit, and the
  checklist and the "Watch out for" note carry the same ordering.

### Pass 3 (2026-08-31, automated)

- TEST-1's `test/e2e/web_ui_bundle_test.go` bullet named
  `strings.CutPrefix(ref, "/ui/")` at `:64-66`, where no such call appears. Those
  lines are `bundleAssetURL`, which uses `strings.TrimPrefix(ref, "./")` at `:64`
  and `strings.HasPrefix(ref, "/ui/")` at `:66`
  (`test/e2e/web_ui_bundle_test.go:62-68`). The `CutPrefix` call is in a
  different package and a different helper, `bundlePath` at `web/web_test.go:94`.
  The bullet now names the served-side helper at its own anchor, and the
  `web/web_test.go` bullet names the embed-side helper at `:94`, so the two
  packages an implementor edits separately are identified separately. The claim
  that the assertion fails only once the bundle is regenerated is unchanged,
  because it holds for both helpers.
- The Summary, the Decisions section, the edge-case table, and Non-goals all
  attributed the web-UI configuration keys to §13.12, which carries none:
  `spec/13-deployment.md:376` opens §13.12 as the backend configuration
  reference, scoped at `:378` to storage, vector, embedding, and identity
  backends. The keys are in §13.10's web-UI configuration keys table
  (`spec/13-deployment.md:181-192`), which is the only place a mount-path key
  could be added or shown to be absent. All four statements now cite that table.
  SPEC-1's untouched list and the matching Non-goals bullet name the table too,
  so the section this proposal edits states which of its parts stay as they are.
