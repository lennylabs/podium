# Proposal 0013: Build the §13.10 web UI

- Issue: (to be filed)
- Status: Draft
- Date: 2026-08-22

This document stages no changes yet. It records what §13.10 specifies, what the
implementation provides, and the one spec amendment building the rest requires,
so a review run stages them rather than rediscovering the analysis. It has not
been through the adversarial review loop.

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

## The spec amendment, and why exactly one is needed

Three of the four surfaces need no spec change. §13.10 specifies the domain
browser, the search filters, and the artifact viewer in enough detail to build
against, the endpoints exist, and this proposal implements existing spec.

The authentication story is different, in three parts.

**The current sentence contradicts the build.** `spec/13-deployment.md:170`
states that the UI "runs no acquisition flow of its own and resolves identity
solely from what the request carries", so a direct request "resolves as
anonymous, and sees public visibility only". That sentence is correct today and
becomes false the moment the UI can authenticate. It was written by proposal
0012, which verified the behavior and deliberately routed the question here.

**No browser acquisition flow is specified anywhere.** §6.3 enumerates
acquisition per consumer: the MCP server through elicitation, the CLI through
stderr and a browser open, the SDK by raising `DeviceCodeRequired`. There is no
browser entry. A sweep of `spec/` for `PKCE`, `authorization_code`,
`redirect_uri`, `sessionStorage`, and `cookie` returns nothing.

**The registry serves no auth route.** `/v1/login`, `/v1/auth/token`, and
`/v1/token` all return 404, and the mux registers no auth, login, or token route
(`internal/serverboot/serverboot.go:1220-1239`). Whether one is added is
decision 1.

## Decision 1: how the browser authenticates

Two viable routes. The maintainer leans toward the second.

**Route A1, pure-SPA authorization code with PKCE.** The browser talks to the
IdP directly and the registry is untouched: it keeps verifying Bearer tokens
under `oidc-jwt` exactly as it does for the CLI. The spec change is a §6.3
acquisition bullet plus the §13.10 rewrite. Costs: the token lives in the
browser, the operator must register a public client at the IdP with the
registry's `/ui/` origin as a redirect URI, and the IdP must allow CORS on its
token endpoint.

**Route A2, registry-mediated.** The registry becomes the OAuth client, performs
the code exchange server-side, and hands the browser a session cookie. No token
is reachable from JavaScript, which removes the class of attack where a script
steals it. Costs: a callback endpoint under §7, a session concept the registry
does not currently have, an OAuth client secret to configure, and the
corresponding §6.3 and §13.12 entries.

A2's cost is proportionate here in a way it would not be for a read-only viewer.
The layer panel performs destructive operations as the caller, so this proposal
is the case where a stronger posture earns its complexity. A2 also needs a
position on CSRF, which A1 does not, because a cookie-authenticated write is
forgeable across origins and a Bearer-authenticated one is not.

Whichever route is taken, decision 1 settles: where the session or token lives,
what happens when it expires mid-page, and whether the registry gains state that
§2.2's shared-library framing has to accommodate.

## Decision 2: the React rewrite and the build step

The SPA is to be rewritten in React, which the current no-build-step vanilla
bundle cannot accommodate. The repository already builds JavaScript for the
documentation site under `site/`, so a toolchain exists as precedent, but the
web UI's bundle is embedded into the Go binary and the site's is not.

Open: whether the built assets are committed so `go build` alone still produces
a working binary, or generated during the release build so the tree carries no
build output. The first keeps `go build ./...` self-contained and puts generated
files in review diffs; the second keeps the tree clean and makes the binary
depend on a Node toolchain being present. §13.10 says the UI is "bundled into
the binary", which both satisfy.

This is the decision with the widest blast radius: it touches `web/web.go`, the
Makefile, the release workflow, and CI.

## Decision 3: scope for standalone

§13.10 offers `--web-ui` on both standalone and standard. Standalone's posture
is deliberately no-auth on a loopback bind (§13.10), so a browser sign-in flow
may be out of place there. Whether the authenticated UI is standard-only, with
standalone keeping the current open-on-loopback behavior, is open and affects
what the layer panel does when no identity provider is configured.

## The design handout

**The implementor does not design the UI.** `web/DESIGN.md` is the design brief,
and a design pass against it produces the layouts, the state treatments, and the
component inventory. The implementation builds what that pass produces.

The brief carries the four surfaces with their real response shapes, the identity
and per-surface state matrix, and the design questions this proposal does not
answer: how much domain depth to render at once, whether to expose the relevance
score, how to treat the sensitivity label, and how to distinguish an empty domain
from a filtered one without disclosing that hidden artifacts exist.

Two items in the brief are design problems rather than implementation details
and must not be settled by whoever writes the React:

- The webhook secret is returned once on register and on rotation
  (`LayerRegisterResponse`, `pkg/registry/server/layers.go:328`). It has to be
  copyable, unmistakably unrecoverable, and not readable as persistent content.
- Unregistering a layer removes its artifacts from every caller's view. It needs
  a confirmation treatment proportionate to that.

## Non-goals

- Authoring or editing artifacts through the UI. Artifacts are authored in git,
  and §13.10 describes a reader and a layer manager.
- Any admin surface beyond the layer panel.
- Changing the endpoints the UI calls. It is a client of the existing HTTP API
  and gains no privileged access, which §13.10 states as "a thin client over the
  same `podium layer …` HTTP endpoints".
- The SDK half of the `DeviceCodeRequired` gap, which is a separate §6.3 client
  surface tracked on its own.

## Testing

§11 currently requires nothing of the UI and no test cites §13.10 for a UI
surface, so this proposal creates the verification obligation as well as
satisfying it. A §11 entry is part of the spec amendment.

At minimum: unit coverage for the auth flow's token or session handling
including expiry; an end-to-end test that an authenticated caller sees an
artifact an anonymous one does not, driven through the UI's own API calls rather
than through the CLI; an end-to-end test that the layer panel's write operations
reach the same endpoints the CLI uses and are refused for a non-admin; and
coverage of the read-only mode presentation, since the registry can reject every
write with `registry.read_only` while reads continue.

## Manual validation

**S44 has to move, and it is the reason this proposal cannot land quietly.**
`test/manual-validation.md` S44 currently pins the anonymous behavior: it
asserts that a directly reachable UI under `oidc-jwt` shows public artifacts
only, and it carries a "Known gap this records" paragraph stating that
in-browser authentication is deferred to its own proposal. That paragraph exists
so a later change to the UI has to move this text with it. This is that change.

New scenarios: a sign-in through the UI that yields a view an anonymous caller
does not get; the layer panel's register flow including the one-time secret; and
an unregister with its confirmation. Each names what a human reads on screen,
which is the class no Go test covers, as S44 already established for the
anonymous case.

## Relationship to proposal 0012

0012 corrected §13's account of what the registry accepts and does. Its decision
3 verified that the shipped SPA attaches no credential, narrowed the web-UI
paragraph to state that plainly, and routed in-browser authentication here. The
sentence this proposal amends is the one 0012 wrote.
