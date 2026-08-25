# Web UI design brief

Input for a UI design pass on Podium's web UI. It describes what the UI must
surface, the data behind each surface, and the states each surface has to
handle. It does not prescribe layout, visual treatment, or component structure,
which are the design pass's output.

The authority for what the UI does is the **Web UI** part of
`spec/13-deployment.md` §13.10, which is the section titled Standalone
Deployment and which covers the UI for both the standalone and the standard
topology. Where this brief and the spec disagree, the spec wins and this file is
the defect.

## How this brief sources its claims

This brief states design intent in its own words: which surfaces exist, what a
reader is trying to do, which states a screen has to handle, and what makes a
treatment right or wrong. It states no field name, field type, status code,
endpoint path, or response body of its own. For each of those it names the
authority that owns the fact and leaves the fact there.

The reason is that the brief does not own the API. A brief that restates a
surface it does not control goes stale as soon as that surface changes, and
review cannot keep it right, because nothing mechanical checks prose against a
response struct. Citing the owner removes the copy that can drift.

The authorities, in order of precedence: the Go source is what runs, meaning the
response structs under `pkg/registry/server/` and `pkg/store/`;
`docs/reference/http-api.md` is the client-facing reference; and the spec under
`spec/` states required behaviour. Where the Go source and a document disagree,
the Go source is what the UI receives, and this brief records the disagreement
rather than choosing between them.

## What the UI is for

A registry serves a catalog of artifacts: skills, contexts, and the other
first-class types. The CLI and the SDKs read that catalog for developers. The
web UI is the path for people who do not use a terminal. The Web UI part of
§13.10 names that audience as non-developer users, and it names them concretely
as analysts, prompt authors, and reviewers who want to browse the catalog without
installing the SDK or learning the CLI. It is also where layers are managed. The
layer-panel paragraph of §13.10 states who may do what there: an administrator
manages the registered layer list, and an ordinary user manages the layers that
user defined. The panel therefore has to read differently for those
two roles rather than presenting one list of uniformly actionable rows.

The registry process serves the UI on the same origin as the API it calls, from
the mount point §13.10 names under "Web UI" and
`internal/serverboot/serverboot.go` registers on the same mux as the `/v1`
routes. The mount is opt-in behind the `--web-ui` flag §13.10 describes, so a
design must not assume the UI is reachable on every registry. The UI has no
privileged access. §13.10 states that it talks to the registry's HTTP API as any
other consumer would and that it resolves identity solely from what the request
carries, so a screen shows only what the caller is already entitled to see, and a
smaller catalog is a normal result rather than an error.

## Current state

The existing implementation is the vanilla-JS file at `web/app.js`. It renders
the domain browser, the search box, and the artifact viewer as unstyled text, and
it has no layer panel. It is a placeholder rather than a baseline to preserve.
Treat this brief as a first design rather than a redesign, and do not carry
forward the current structure.

## Constraints

- **React.** The SPA is being rewritten in React, so a build step is expected.
  `web/web.go` embeds the `web/` assets into the binary and
  `internal/serverboot/serverboot.go` serves them through a static file server,
  so the build output ships inside the binary and every screen is rendered in the
  browser. Assume no server-side rendering, which makes first paint, loading, and
  empty states the design's problem rather than the server's.
- **Same origin.** `internal/serverboot/serverboot.go` mounts the UI and the API
  on one mux, and the request chain in `pkg/registry/server/server.go` carries no
  cross-origin middleware. Every API call goes to the host that served the page,
  so the design can assume relative paths and browser-carried credentials, and it
  needs no configurable API base URL and no cross-origin fallback.
- **The registry may be reached directly or through a gateway.** §13.10 describes
  both deployments and resolves identity the same way in each, and the registry
  exposes no endpoint that reports which one is in front of it. The liveness and
  readiness responses report the serving state and nothing about the path the
  caller took (`HealthResponse` and `ReadyResponse` in
  `pkg/registry/server/server.go`, documented under Health in
  `docs/reference/http-api.md`). Every screen therefore has to work
  without knowing how the caller was authenticated, and it has to read the same
  whether the caller resolves to a person or to anonymous.
- **Design tokens already exist.** `site/src/styles/tokens.css` is the token file
  the documentation site uses, and `.claude/rules/doc-diagram-style.md` binds
  every diagram class to the same file. The UI should inherit that vocabulary
  rather than introduce a second one. Read the file for what it carries: colours
  for surfaces, text, lines, and the accent, the font families, corner radii, and
  the site's page widths and gutters. It declares no type scale and no general
  spacing scale, so the design pass supplies those.
- **Both themes.** The token file resolves a theme in the order its header
  comment states: the light values are the default, an unconfigured visitor's
  `prefers-color-scheme` switches the palette, and a `data-theme` attribute on the
  root element overrides both. Every surface has to read in both themes, and a
  treatment that works on only one ground is wrong.
- **Content is user-authored and long.** The prose body of a manifest is markdown
  returned inline by `load_artifact`, and an artifact ID is its directory path
  under the registry root in a hierarchy that nests to arbitrary depth (spec §4.2
  in `spec/04-artifact-model.md`). The only bounds are the ingest-time size caps
  in §4.1, which are far above anything a layout can lean on. No layout should
  assume a short string, a short body, or a shallow identifier.

## The surfaces

### 1. Domain browser

The catalog is the domain hierarchy described in spec §4.2: directories are
domain paths and the leaves are artifact packages. This surface is the entry
point and the primary navigation, so it has to make a reader's position in that
hierarchy obvious at every level.

The surface is backed by the `load_domain` call documented in
`docs/reference/http-api.md` under Discovery, which takes the domain path and an
optional depth. The response schema is `LoadDomainResponse` in
`pkg/registry/server/server.go`, mirrored for clients in the same reference
section. Read the request and the response there; this brief does not restate
them. What each part of the response gives the design:

| Part of the response | What it gives the design |
|:--|:--|
| the requested domain, echoed back | It always carries a value, so breadcrumbs can be rendered from the response alone. |
| the domain description | It is optional, so every domain header and every subdomain card needs a treatment for a domain that arrives without one, and that treatment cannot be a blank gap where the text would have been. |
| the domain's keywords | Author-supplied and optional, so the header has to read as finished when a domain declares none. |
| the child domains | Always present and possibly empty, so a domain with no children still needs a treatment. |
| the domain's artifacts | Always present and possibly empty, so a domain that is pure navigation needs its own state. |
| the rendering note | One short sentence, present only when the server reduced the response in a way the caller did not ask for, such as trimming the depth or the artifact list to fit the response budget (§4.5.5, "Rendering note"). The reader is seeing an incomplete map when it appears, so it needs a place that reads as a system message about the listing rather than as domain content or as an error. |

The registry root is addressed by the empty path, and §4.5.5 records that the
root carries no description and no author-curated entries. The browser therefore
needs a root state whose header cannot lean on a domain description, and it has
to read as the top of the hierarchy rather than as an empty domain.

Each child entry is a `DomainDescriptor` (`pkg/registry/server/server.go`), which
is the same structure the search surface ranks, so a subdomain row and a domain
search result can share one component. Its comment records that an entry has a
path, a display name, a description that may be absent, and, when the call
expands more than one level, its own nested child tree. The row component
therefore has to render recursively rather than assume a flat list.

An artifact entry is an `ArtifactDescriptor` (`pkg/registry/server/server.go`),
the same structure a search result uses, so one card component serves both
surfaces. Which of its members are always present and which are omitted when
empty is recorded by the struct's tags, and most of them are omitted, so the card
has to hold together when a version, a description, a tag list, or a summary is
missing. The struct comments record which members populate on which call, and two
of them carry design consequences:

- An entry records whether it was curated by the domain's author or selected by
  ranking. The permitted values, the precedence when an artifact qualifies for
  both, and the cases where the field is absent are defined by
  `ArtifactDescriptor.Source` in `pkg/registry/core/core.go` and by the
  notable-selection rules in §4.5.5. The distinction is meaningful to a reader and
  is currently invisible.
- An entry can record the subpath it was folded up from, which the server sets
  when a sparse subdomain is collapsed into this domain's leaf set
  (`ArtifactDescriptor.FoldedFrom` in `pkg/registry/server/server.go`, and the
  folding rules in §4.5.5). Such an entry is not a direct child of the requested
  domain, and showing it as one misrepresents the hierarchy.

The directory hierarchy nests to arbitrary depth, and one response does not. The
rendered subtree is capped by the configured depth ceiling and trimmed further to
fit the response budget, and a caller-supplied depth is capped at the same ceiling
(§4.5.5, with the rendering in `pkg/registry/core/core.go`). The design needs a
position on how much of the returned tree to render at once and on how a reader
continues past the returned edge.

### 2. Search

Search calls the registry's `search_artifacts` endpoint, whose path and full
argument list are documented in `docs/reference/http-api.md` and registered in
`pkg/registry/server/server.go`. §13.10 requires the UI's filters to be the same
ones the SDK and CLI offer, and it names them as type, scope, and tags. The
filter set is therefore fixed by the spec, and the design decides only how many
of them are visible at once and which are folded behind a control. Every argument is optional, so a request with no query text is a browse
over the filters, and the design needs a state for that as well as for a text
query. The endpoint also takes a result-count argument, which is what a "show
more" control would drive.

The response envelope is `SearchResponse` in `pkg/registry/server/server.go`,
with a worked example under `search_artifacts` in `docs/reference/http-api.md`.
It carries the echoed query, a total match count, and the ranked results. Only
the match count is always present: the echoed query, the result list, and most
descriptor members are omitted when empty, so a search that matched nothing
returns no result list at all, and a browse call that passes filters without a
query gets no echo of what was asked. Every element the design leans on needs an
absent state as well as a populated one.

Each result is an `ArtifactDescriptor` (`pkg/registry/server/server.go`), the
same structure the domain browser's artifact entries use, filled for search by
`descriptorOf` in the same file. It gives the design the artifact's identity, a
short description, tags, a relevance score, an optional classification label, and
the artifact's frontmatter. Two of those drive design decisions:

- The relevance score is the lexical rank, and `pkg/registry/core/core.go` leaves
  it at zero for a result matched only by vector similarity, so some results
  arrive without one. Whether to expose the score, and how to treat a result that
  has none, is open.
- The classification label is omitted when the artifact declares none and is
  resolved to the most restrictive value across an extends chain
  (`ArtifactDescriptor.Sensitivity`, with the values and their informational
  meaning in spec §4.3 and §4.6). It needs a treatment that reads as a property of
  the artifact rather than as an alert, and every screen showing it needs an
  unclassified state.

The match count is taken before the result cap truncates the list
(`SearchArtifacts` in `pkg/registry/core/core.go`). The cap and its default are
documented under `search_artifacts` in `docs/reference/http-api.md`, and its
maximum is fixed by the `search_artifacts` description in spec §5 of
`spec/05-meta-tools.md`. A search therefore commonly returns fewer results than
it matched. The design needs a way to express "showing N of M", and it needs the
recovery path §5 describes for the truncated case: narrowing with filters,
drilling into a subdomain, or running a more specific query.

### 3. Artifact viewer

The viewer reads the `load_artifact` endpoint, whose route and arguments are
registered in `pkg/registry/server/server.go` and documented in
`docs/reference/http-api.md`. The response is `LoadArtifactResponse` in the same
Go file. The parts of it that carry a design consequence:

| Part of the response | What it gives the design |
|:--|:--|
| identity | The artifact's ID, type, and version, always present. |
| provenance | A content hash of the loaded artifact, available for a provenance display. |
| the manifest body | Markdown, which spec §4.1 fixes for the manifest, rendered as a document rather than shown as source. The viewer therefore needs a rendered-markdown treatment covering headings, lists, code blocks, tables, and links. |
| the manifest frontmatter | A raw YAML block carried as text, per `LoadArtifactResponse`, which is the only authority that carries it for this call. The viewer presents it as a property table, which means the client parses that block first, so the design needs a treatment for a block that fails to parse and for an artifact that carries no frontmatter at all. |
| the authored skill file | Populated for a skill artifact and empty for every other type, and cleared when the manifest arrives by link instead of inline (`SkillRaw` for the first condition and `ManifestBodyURL` for the second, both in `pkg/registry/server/server.go`, with the clearing applied by `attachManifestBody` in the same file). An authored-file view is therefore available on some artifacts and absent on others, so it belongs in a treatment that can disappear without leaving a hole in the layout. |
| the serving layer | Naming the layer that served the artifact ties the viewer back to the layer panel, so the two surfaces use the same label for the same layer. |
| the sensitivity classification | Absent on unclassified artifacts and informational rather than enforced (spec §4.3), so the treatment reads as a property of the artifact. The search result carries the same classification, so both surfaces use one treatment. |
| the inline resources | The bundled files delivered with the response. The values are not always readable text: one binary file forces the whole inline set into base64 and the response says so through a companion flag, so the viewer needs a treatment for a resource it cannot render as text. |
| the fetched resources | The bundled files the client retrieves from object storage instead. Each entry is a reference object rather than a bare link, carrying the size and the content type alongside the URL (`LargeResourceLink` in `pkg/registry/server/server.go`, whose comment records that the URL's authentication model differs by object-store backend), so the viewer can show a file's size and kind before any fetch and needs a state for a fetch that fails. |

§13.10 also requires links to extending or dependent artifacts, so an artifact is
a node in a graph and the viewer is where that graph becomes navigable. Those
edges are served by their own endpoint rather than by the artifact response
(`handleDependents` in `pkg/registry/server/server.go`), so the graph arrives on a
second request and the design needs a state for an artifact that has no edges.

Two cases the design must handle explicitly. The first is an artifact whose body
arrives as a presigned URL with the inline body empty, which the registry does for
a document above the inline cutoff (`attachManifestBody` in
`pkg/registry/server/server.go`, described under `load_artifact` in
`docs/reference/http-api.md`). The viewer has nothing to render until that fetch
completes, so it needs a loading state and a failure state for the body itself.
The second is an artifact carrying resources of both kinds at once. The registry
splits the bundled files one by one (`attachResources` in the same file), so a
single artifact can present inline files beside fetched files, and the resource
list has to read as one list rather than as two.

**The sanitization rule.** An artifact body is markdown authored by whoever can
write to a layer's source, and the viewer renders it as a document on the
registry's own origin, which is the origin the session cookie is scoped to. This
rule is the single statement of how the UI renders an untrusted artifact body:
what is sanitized, what the sanitizer takes as its input, where it is applied,
which URL schemes survive it, and what falls outside it. Every other site that
mentions the rule cites it by name and states only what is local to that site.

The viewer renders an artifact body through one rendering path in the web UI's
own source tree, and that path sanitizes what it renders. The sanitizer runs on
the rendered output rather than on the markdown source, so a markdown construct
that the renderer emits as raw HTML is neutralized rather than carried through,
and a construct that survives the markdown renderer cannot bypass the sanitizer.
No executable node and no event-handler attribute survives sanitization. The
sanitizer carries an allowlist that admits no URL scheme other than `http`,
`https`, and `mailto` on any attribute it keeps, so a URL bearing any other
scheme, including `javascript:` and `data:`, does not survive on a link or on any
other attribute. Frontmatter does not reach this path. It is rendered as a
property table with values escaped as text, and it is not markdown and is not
rendered as such. Which sanitizer implementation the rendering path uses is the
implementor's choice, and any answer satisfies this rule in full and states no
scheme, attribute, or path condition the rule does not carry.

### 4. Layer panel

The catalog is assembled from layers, and spec §4.6 defines what a layer is: one
source and one visibility declaration, composed in precedence order. This is the
only surface with write operations, and the only one whose contents differ by
role.

A layer record identifies its source, records when it was last ingested, carries
an order value that sets its precedence, and declares who can see it. The registry
marshals `store.LayerConfig` (`pkg/store/store.go`) directly into every layer
response, so that struct owns the field names, their casing, and their types, and
the design reads it there rather than from a copy in this brief. Read the struct
before naming a field in a mock: the layer JSON is not uniformly snake_case, the
response example in `docs/reference/http-api.md` disagrees with the struct on that
point, and the webhook secret is deliberately absent from it. Three of the
record's properties drive design work.

- **Source.** The built-in source types are a git repository and a local
  filesystem path, listed under "Source types" in spec §4.6, and each carries a
  different set of source details to display. The type is pluggable through the
  `LayerSourceProvider` extension point, so the panel must render a source type
  the design has not seen without breaking.
- **Precedence.** The order value sets a layer's precedence within the tenant, and
  the direction is fixed by `store.LayerConfig.Order` with the composition rule in
  spec §4.6. Direction is the design risk: a list that reads top to bottom does
  not by itself tell a user which end wins, so the panel has to label the winning
  end rather than rely on position.
- **Visibility.** Visibility is not a single choice. Spec §4.6 defines it as a set
  of independent grants that combine as a union, and `store.LayerConfig` mirrors
  them as separate members, so a layer can be organization-wide and group-scoped
  at the same time. The panel renders a combination rather than a single badge, it
  stays readable when a layer names several groups or several users, and it needs
  a treatment for a layer with no grants at all. Spec §4.6 fixes a user-defined
  layer's visibility at registration and forbids widening it, so that case is
  displayed rather than edited.

Every operation below runs against the layer-management endpoints the CLI already
drives, registered in `LayerEndpoint.Handler` (`pkg/registry/server/layers.go`)
and documented under "Layer management" in `docs/reference/http-api.md`. The
consequence for the design is that the web UI adds no capability the CLI lacks, so
any state the CLI can reach is a state the panel must render.

- **Register** a layer. Registering a git-backed layer returns a webhook URL and
  an HMAC secret, and that is the only response that carries the secret:
  `store.LayerConfig` redacts it everywhere else, and the register response
  includes it only on registration and on a secret rotation
  (`pkg/registry/server/layers.go`). Registering a local-path layer returns
  neither, so the reveal is a conditional part of the flow. The one-time reveal is
  a specific design problem: it must be copyable, it must be clearly
  unrecoverable, and it must not be mistaken for persistent content.
- **Update** a layer. The update is a partial patch, and the fields it accepts are
  listed under "Update a layer" in `docs/reference/http-api.md`, against the
  handler in `pkg/registry/server/layers.go`. Requesting a webhook-secret rotation
  returns the new secret once, on the same terms as registration, so the reveal
  treatment is reused here, and only a git source carries a secret, so the
  rotation control needs an absent or disabled state on a local layer. On a
  user-defined layer the registry ignores the owner and visibility fields and
  still answers success, so the form must not offer controls for values it cannot
  change.
- **Reingest** a layer. The registry runs the whole ingest pipeline inside the
  request and answers with a summary of what the snapshot accepted, what it
  rejected, and what it conflicted on (`POST /v1/layers/reingest` in
  `docs/reference/http-api.md`, served by the reingest handler in
  `pkg/registry/server/layers.go`). The call carries no job identifier and no
  progress channel, so the design problem is a single request that can stay open
  for a long time, followed by a result summary the user has to read and act on.
- **Unregister** a layer. The artifacts leave every caller's effective view
  immediately, and the layer and its artifacts are soft-deleted and recoverable
  for a retention window through the restore endpoint (spec §11 layer-lifecycle
  tests, the §8.4 retention table, and the "Unregister" and "List soft-deleted
  layers and restore" sections of `docs/reference/http-api.md`). The confirmation
  treatment has to convey both halves: the removal is visible to everyone at once,
  and it is recoverable rather than final. The panel also needs a surface for what
  is still recoverable, because an unregistered layer has not been erased.
- **Reorder** layers, which changes the order the effective view is composed in
  (spec §4.6, "Composition order"). Two layers cannot simply carry the same
  artifact ID: that collision is rejected at ingest unless the higher-precedence
  artifact declares `extends:` (spec §4.6, "Merge semantics for collisions").
  Reordering therefore changes how an extending artifact merges with its parent,
  which is why the control has to show the resulting order rather than treat the
  list as unordered.

Roles differ. An administrator manages every layer in the tenant, which is the
panel §13.10 describes and the authorization the layer endpoint applies to
admin-defined layers (`pkg/registry/server/layers.go`). An ordinary user manages
their own user-defined layers and is capped at a configurable number of them
(spec §7.3.1, with the enforced default in `pkg/registry/server/layers.go`). The
registry refuses a registration past the cap with an error carrying the limit and
the caller's current count, so the panel renders that refusal where the user
creates a layer rather than as a generic failure.

Two properties of the shipped API constrain how far the panel can lean on that
role split. The layer list is not scoped to the caller: the list handler consults
no identity and returns every layer stored under the tenant, so the panel's role
split is presentation over a list the server hands it whole. Ownership scoping is
also not enforced today on the write path: spec §13.10 and §7.3.1 describe a user
acting on that user's own layers, while the update, unregister, reorder, and
restore handlers in `pkg/registry/server/layers.go` gate only on whether a layer
is admin-defined. That divergence between the spec and the Go source is reported
separately, and the panel design follows the spec's scoping.

Whether an anonymous caller sees the panel is a design decision rather than one
the API makes. Listing layers carries no authorization check, and a standalone
deployment with no identity provider treats the local operator as the
administrator (§13.10, with the admin-authorization wiring in
`internal/serverboot/serverboot.go`). Hiding the panel from an unauthenticated
caller is therefore a UI choice, and it has to keep the panel available in the
standalone deployment, where nobody authenticates and the panel is the point.

## The state matrix

Most of the design work is here rather than in the happy path.

**Identity states.** The UI has these, and cannot always tell them apart from the
client side:

1. **Anonymous.** The request carries no credential, so the caller resolves as
   anonymous and sees public visibility only (§13.10 on web-UI authentication,
   with the visibility grants in §4.6). The catalog still renders. Nothing is
   broken and no error has occurred, so an anonymous view must not read as a
   failure. This is a legitimate browsing mode.
2. **Authenticated user.** The effective view is the composition of every layer
   whose visibility declaration matches the caller's identity, which always
   includes the public layers (spec §4.6). The same user manages their own
   user-defined layers under §13.10 and §7.3.1, subject to the enforcement gap
   noted in the layer panel above.
3. **Administrator.** Manages every layer in the tenant, including the
   admin-defined layers an ordinary user cannot modify or unregister. Destructive
   operations are not exclusive to this role, since a user can unregister a layer
   they own, so the panel differs by which layers it exposes rather than by
   whether it offers destruction at all.

The transitions matter as much as the states: signing in, signing out, and a
session expiring mid-use while a page is already rendered.

**Per-surface states.** Each surface needs loading, empty, error, and forbidden.
Two deserve specific attention:

- **Empty versus filtered.** A domain response is assembled entirely from the
  caller's visible set (`LoadDomain` in `pkg/registry/core/core.go`) and carries
  no field reporting that visibility filtering removed anything, so a domain with
  no visible artifacts looks identical to an anonymous caller and to a user whose
  visibility excludes everything in it. The same filtering produces a second case:
  when nothing of the domain itself is visible the path does not resolve and the
  call fails as not found, so one URL can render for one caller and read as
  missing for another. Whether the UI distinguishes empty from filtered, and how
  it does so without disclosing that hidden artifacts exist, is a design question
  with a privacy constraint attached.
- **Not-found versus not-permitted.** A single-artifact load for an artifact the
  caller may not see returns the same not-found error as an artifact that does not
  exist, deliberately, so that invisibility discloses nothing
  (`pkg/registry/core/core.go`, which records the denial only in the audit log).
  The batch-load path reports its per-item denial under a distinct code, and
  `docs/reference/error-codes.md` catalogues both codes and records that the
  denial code's response mirrors a not-found result. The UI must not undo the
  concealment by implying the artifact exists.

**Errors** arrive as the structured envelope spec §6.10 defines, implemented as
`ErrorResponse` in `pkg/registry/server/server.go` and catalogued for clients in
`docs/reference/error-codes.md`. The design needs a treatment for a
machine-readable code the UI branches on, a prose message, a retry signal that
tells the user whether the condition clears on its own, and an optional
remediation hint that is present for some codes and absent for others. A design
that shows only the message discards the two fields that tell the user what to do.
The code is a stable namespaced identifier drawn from that catalogue, and the
design should assume the message is prose that is not always suitable to show
verbatim.

**Read-only mode.** Spec §13.2.1 defines a degraded mode in which reads continue
to serve and every mutating endpoint is rejected, which
`pkg/registry/server/readonly.go` enforces at a single choke point with one
dedicated error code. §13.2.1 also puts a read-only marker on read responses, so
the UI can present the state before a write is attempted. Every write control on
the layer panel is unavailable at once, so the panel needs a coherent presentation
of that state rather than a failure per button press.

## Out of scope

- Authoring or editing artifacts. The UI is a reader and a layer manager;
  artifacts are authored in git.
- Any admin surface beyond the layer panel.
- Any visual identity beyond what the existing token set already establishes.

## What the design pass should produce

1. Layouts for each surface, including the nested and deep cases rather than only
   a shallow example.
2. The state treatments named above, in particular anonymous browsing, the
   one-time secret reveal, and the destructive-operation confirmation.
3. A component inventory sufficient to build from, given React.
4. A position on the open questions this brief names: how much domain depth to
   render at once, whether to expose relevance score, how to treat sensitivity,
   how to distinguish empty from filtered without disclosing hidden content, and
   whether an unauthenticated caller sees the layer panel given that it has to
   stay available in the standalone deployment.
