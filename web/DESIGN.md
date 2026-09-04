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
user defined. The list read hands the panel the layers the caller may read on the
terms the §7.3.1 layer read visibility rule sets, which are the tenant's whole
list for a tenant admin and for every caller on a registry that authenticates
none, and the layers §4.6 admits otherwise. No response reports the caller's
role. The §7.3.4 posture read reports, per §7.3.1 operation, whether this
deployment's layer endpoints would admit this caller, which predicts a server
decision rather than reporting a grant. The panel therefore renders a §7.3.1
layer write control only where that read and the target's own class, stored
owner, source type, and stored filesystem path admit this caller, the §13.2.1
read-only marker then mutes whatever remains present, and a refusal an offered
write receives is still drawn on the row it was attempted from, because the
posture read reports a snapshot. The one marker it carries is
the ownership marker the layer-panel section below defines, which is a property
of a user-defined row rather than a rendering of the caller's role.

The registry process serves the UI on the same origin as the API it calls, from
the mount point §13.10 names under "Web UI" and
`internal/serverboot/serverboot.go` registers on the same mux as the `/v1`
routes. The mount is opt-in behind the `--web-ui` flag §13.10 describes, so a
design must not assume the UI is reachable on every registry. The UI has no
privileged access. §13.10 states that it talks to the registry's HTTP API as any
other consumer would, so a screen shows only what the caller is already entitled
to see, and a smaller catalog is a normal result rather than an error. §13.10
scopes its acquisition clause to the deployment where the browser flow is
disabled, and there the UI runs no acquisition flow of its own and resolves
identity solely from what the request carries. Where the browser flow is enabled
the registry runs the acquisition itself, and what that flow does is owned by
"The browser session" in `proposals/0013-build-the-13-10-web-ui.md`, which the
sign-in control rule in the state matrix below keys on.

## Current state

The UI had no design. A vanilla-JavaScript placeholder rendered the domain
browser, the search box, and the artifact viewer as unstyled text, and it carried
no layer panel. That placeholder was removed together with the markup and the
stylesheet it shipped with, and nothing was carried forward from it. Treat this
brief as a first design rather than a redesign.

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
  `docs/reference/http-api.md`). No screen may key on whether a gateway fronts
  the registry. One endpoint does report the deployment's identity posture and
  the caller's own resolved subject, and it is the posture read, whose fields are
  owned by "The posture read" in
  `proposals/0013-build-the-13-10-web-ui.md`. The application shell keys its
  authentication control on that read, under the sign-in control rule in the
  state matrix below. The catalog and the per-surface screens do not: each has to
  work without knowing how the caller was authenticated, and each reads the same
  whether the caller resolves to a person or to anonymous, with the scope of the
  catalog itself decided by the catalog-scope rule in the state matrix below.
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
to read as the top of the hierarchy rather than as an empty domain. A root whose
read returns no subdomain and no artifact is the one state in which this surface
instructs the reader to register a layer, and that instruction is a claim about
the caller reading it. The browser states it only where the same §7.3.1
prediction the layer panel reads admits this caller on a registration, and it
states it unchanged where the identity posture read settled nothing, because an
unanswered read settles nothing about whether the registry resolved a caller.

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

Three cases the design must handle explicitly. The first is an artifact whose body
arrives as a presigned URL with the inline body empty, which the registry does for
a document above the inline cutoff (`attachManifestBody` in
`pkg/registry/server/server.go`, described under `load_artifact` in
`docs/reference/http-api.md`). The viewer has nothing to render until that fetch
completes, so it needs a loading state and a failure state for the body itself.
The second is an artifact carrying resources of both kinds at once. The registry
splits the bundled files one by one (`attachResources` in the same file), so a
single artifact can present inline files beside fetched files, and the resource
list has to read as one list rather than as two.

The third is a response that yields no frontmatter pairs to render. More than one
path through the API produces it, so the treatment covers the state rather than
branching per producer: the property table is omitted entirely, with no header
standing over an empty table and no placeholder row, and the rest of the viewer
reads as a finished document rather than as a partial load. That treatment is
distinct from the one for a frontmatter block that fails to parse, which is a
defect the reader is told about. The producers known today are a search result
whose inherited extension block could not be rewritten, which carries no
frontmatter block at all, and a non-skill artifact response whose frontmatter is
cleared because the manifest arrives by link instead of inline. The response
structs in `pkg/registry/server/server.go` own both, and
`docs/reference/http-api.md` carries the search-result case under
`search_artifacts`.

### 4. Layer panel

The catalog is assembled from layers, and spec §4.6 defines what a layer is: one
source and one visibility declaration, composed in precedence order. This is the
only surface with write operations. Its rows are the rows the caller may read
under §7.3.1, and its per-row rendering differs by layer class and by ownership
rather than by the caller's role, because no response reports the caller's role.
The §7.3.4 posture read reports instead, per §7.3.1 operation, whether this
deployment's layer endpoints would admit this caller, which predicts a server
decision rather than reporting a grant, and every write control on a row is
rendered from that prediction together with the row's own fields.

A layer record identifies its source, records when it was last ingested, carries
an order value that sets its precedence, and declares who can see it. Spec §7.3.1
fixes the layer object's member names, and §7.2.1 makes them lower snake_case, so
the response example in `docs/reference/http-api.md` and the JSON tags on
`store.LayerConfig` (`pkg/store/store.go`) agree. The struct is the one
enumeration of the field list, and the design reads it there rather than from a
copy in this brief. The webhook secret and the tenant identifier are both absent
from the object. Three of the record's properties drive design work.

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
  a treatment for a layer with no grants at all. A layer matching on more than one
  axis is displayed as one marker per matching axis, rendered together in a fixed
  axis order rather than collapsed into a single-valued label, so two layers
  carrying the same grants read identically and no axis is dropped to make room.
  Where an axis names more members than the row can hold, the treatment summarises
  the overflow within that axis rather than hiding the axis. Spec §4.6 fixes a user-defined
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
panel §13.10 describes, because a tenant admin is authorized on both layer
classes under the layer-write authorization rule stated in "The layer-ownership
defect" in `proposals/0013-build-the-13-10-web-ui.md`. An ordinary user manages
their own user-defined layers and is capped at a configurable number of them
(spec §7.3.1, with the enforced default in `pkg/registry/server/layers.go`). The
registry refuses a registration past the cap with an error carrying the limit and
the caller's current count, so the panel renders that refusal where the user
creates a layer rather than as a generic failure.

Two properties of the shipped API decide how far the panel can lean on that role
split. The first is the read. The layer list reports what the caller may read: a
tenant admin and every caller on a registry that authenticates none receive the
tenant's whole list, any other caller who resolves a verified subject receives
the layers §4.6 admits, and a caller who resolves no subject receives none. The
panel's role split is presentation over whichever list the server returns. That
statement is owned by the §7.3.1 layer read visibility rule.

A caller whose credential fails verification is refused rather than narrowed. The
read answers the §6.10 envelope that credential already receives elsewhere, and
the panel renders its refusal band in place of its table, naming the code and the
envelope's `suggested_action`. The band offers no retry, because the registry
marks none of those codes retryable. The way back belongs to the shell: the same
credential refuses the shell's own catalog read, so the layers route renders the
refused-read banner with its retry, or the ended-session banner with its recovery
control where the posture read resolved a subject, above the panel it keeps
mounted.

The second is the write. Each write is authorized per layer, under the
layer-write authorization rule stated under "The layer-ownership defect" in
`proposals/0013-build-the-13-10-web-ui.md`: an operation
on a user-defined layer is authorized to that layer's owner or to a tenant
admin, an operation on an admin-defined layer is authorized to a tenant admin
alone, and the rule covers register, unregister, update, restore, reorder, and
reingest. The panel therefore treats creating a layer under an unused ID and
re-registering an existing ID as different authorization cases, which that rule
decides. The rule is live only where the deployment both configures an identity
provider and does not run in public mode, which is the liveness condition that
rule carries. A registry missing either conjunct admits
every one of the panel's writes and the role split is presentation there as
well, which covers both the standalone registry that authenticates no caller and
a registry that engages public mode. Where the rule is live, a registration under
one of the recoverable IDs the panel's restore surface lists is authorized on the
same terms as any other write against that stored layer, so the recovery window
is not a window in which an unauthorized caller can take the ID over, while a
caller that rule does authorize on the stored layer may register under it. The
design consequence is that the panel can now receive a refusal
from a write it would otherwise have assumed would succeed, including on a layer
its own role split presented as the caller's to manage. It presents that refusal
rather than treating it as a failure of the page.

The panel's ownership marker carries only what that rule makes an ownership
fact, which is why it applies to a user-defined row alone. On a user-defined row
the marker is a comparison of the row's stored owner against the caller's own
subject, and the posture read reports a subject only where one resolves. Where
it resolves none the comparison has no left-hand side, so such a row carries no
ownership marker. On an admin-defined row the same rule authorizes a tenant
admin alone, whatever the stored owner names, because that owner is supplied by
the caller who registered or patched the layer and names no authorized subject.
An admin-defined row therefore carries no ownership marker on any value of that
field, and the panel presents the stored owner as the field it is rather than as
a statement about who may write it. Whether the panel's
writes are admitted is decided on a different axis: the liveness condition above
is a property of the deployment's configuration rather than of whether this
caller resolved a subject. Where the registry configures no identity provider or
runs in public mode, the write rule is not live, the panel holds the tenant's
whole list, no row carries an ownership marker because no subject resolves, and
no write is refused. Where the registry configures an identity provider and does
not run in public mode, the rule is live and the read has three arms under
§7.3.1: a caller holding the tenant `admin` role reads the tenant's whole list,
any other caller who resolves a verified subject reads the layers §4.6 admits,
and a caller who resolves no subject reads no layers. A caller who resolves a
subject is offered a write control on a row it can see only where the §7.3.4
posture read and that row's own class, stored owner, source type, and stored
filesystem path admit it, and an offered write that comes back refused is drawn
on the row it was attempted from, because the posture read reports a snapshot. A
caller who resolves no subject stands on the panel's empty state with no row to
mark and no write to attempt. On a deployment configuring an identity provider
that empty state reports that the registry resolved no caller for this page,
while a deployment that authenticates none keeps "Register a layer to bring its
artifacts into the catalog."

Whether an anonymous caller sees the panel is a UI decision, while what the panel
holds is decided by the read: the tenant's whole list for a tenant admin and on a
registry that authenticates no caller, the layers §4.6 admits for any other
caller who resolves a verified subject, and no rows for a caller who resolves
none. A standalone deployment configures no identity provider, treats the local
operator as the administrator (§13.10, with the admin-authorization wiring in
`internal/serverboot/serverboot.go`), and keeps the panel available holding the
tenant's whole list, where nobody authenticates and the panel is the point.

## The state matrix

Most of the design work is here rather than in the happy path.

**Identity states.** The UI has these. The anonymous state and the authenticated
state are told apart by whether the posture read resolves a subject for the
caller. No response reports the caller's role. The §7.3.4 posture read reports
per §7.3.1 operation whether this deployment's layer endpoints would admit this
caller, which predicts a server decision rather than reporting a grant, and the
page renders its layer write controls from that prediction. A read that does not
answer holds every member of that prediction false, so the page renders neither
authentication control and no layer write control, and a reader recovers the
controls by reloading the document.

1. **Anonymous.** The posture read resolves no subject, so the caller browses
   without an identity (§13.10 on web-UI authentication, with the visibility
   grants in §4.6). How much of the catalog that caller sees, and whether the
   catalog answers at all, are set by the catalog-scope rule below. Where the
   catalog read answers, nothing is broken and no error has occurred, so that
   view must not read as a failure: it is a legitimate browsing mode. Where the
   catalog read is refused because the caller's identity could not be verified,
   the refused arm of the catalog-scope rule governs instead.
2. **Authenticated user.** The effective view is the composition of every layer
   whose visibility declaration matches the caller's identity, which always
   includes the public layers (spec §4.6). The same user manages their own
   user-defined layers under §13.10 and §7.3.1, on the terms the layer-write
   authorization rule sets, which the layer panel above states.
3. **Administrator.** Manages every layer in the tenant, including the
   admin-defined layers an ordinary user cannot modify or unregister. Destructive
   operations are not exclusive to this role, since a user can unregister a layer
   they own. No response reports that the caller holds this role. The §7.3.4
   posture read reports whether this deployment's layer endpoints would admit
   this caller on the §4.7.2 admin arm, which is a prediction of a server
   decision rather than a report of a grant, and the list read hands the panel
   the layers the caller may read on the terms the §7.3.1 layer read visibility
   rule sets, which are the tenant's whole list for this role and for every
   caller on a registry that authenticates none. The panel renders a write
   control over the list it received only where that prediction and the row's
   own class, stored owner, source type, and stored filesystem path admit this
   caller, and it draws a refusal an offered write receives on the row the write
   was attempted from.

**The catalog-scope rule.** How much of the catalog an anonymous caller sees is a
property of the deployment, and the page reads it from the posture read rather
than assuming a public subset. The rule keys on that read's
`identity_provider_configured` and `public_mode`, both owned by "The posture
read" in `proposals/0013-build-the-13-10-web-ui.md`, and on whether the catalog
read answers at all.

- Where a catalog read is refused because the caller's identity could not be
  verified, that caller has no anonymous view of the catalog, and the page
  renders the refused state rather than an empty catalog or a filtered one.
  What a catalog read returns for a session the registry cannot verify is owned
  by the expiry-signal rule under "The browser session" in
  `proposals/0013-build-the-13-10-web-ui.md`, and the page reads it there rather
  than from this brief. A second deployment class reaches this same arm with no
  session involved. A registry whose identity provider verifies a runtime-signed
  token refuses every catalog call from a browser that holds none, because that
  verification runs ahead of the handler and a caller carrying no token fails it
  (`pkg/registry/server/identity_verify.go` and `pkg/identity/runtime.go` own
  that behaviour). A caller who never held a subject therefore reaches this arm
  as readily as one who did, and the posture read carries no field that separates
  such a deployment from one whose catalog read answers.
  This arm is ordered ahead of the two below, whether or not the
  posture read answered, so neither of them applies to such a refusal. Where a
  caller who had a subject sees that refusal, the transition it marks is the
  session expiry the expiry-signal rule under "The browser session" in the same
  proposal names, and that rule owns that transition alone. A catalog read that
  fails for any other reason, such as an unavailable registry or a server
  failure, is outside this rule and takes the surface's own error state under
  "Per-surface states" below.
- Where the catalog read answers, the anonymous view is the public subset when
  the posture read reports `identity_provider_configured` true and `public_mode`
  false. On every other combination of the two it is the whole catalog.
- Where the catalog read answers and the posture read does not, the page holds
  neither key. It presents what the catalog read returned, under the constraint
  the public-subset arm carries below, and it renders the anonymous presentation
  "The posture read" states for that arm.

That keying carries one named exception, and it is the only one. A registry
configured with an identity provider whose label the process does not recognise
installs no verifier, resolves every caller the same way, and serves its whole
catalog to all of them, while the posture read places it on the public-subset arm
because the read reports the configured setting rather than an installed
verifier. The read carries no field that separates that deployment from a
verifying one, and the page cannot separate it either. The rule therefore
constrains what the arm licenses the page to state: on the public-subset arm the
page presents the catalog the read returned and states nothing that would be
false on that deployment, asserting neither that artifacts were withheld nor that
hidden artifacts exist. That is the same constraint the empty-versus-filtered
question below already carries. The design pass drives no stub combination of its
own for this deployment, because the read reports it identically to a verifying
registry.

**The sign-in control rule.** The UI carries one authentication control, and it
belongs to the application shell rather than to any surface in the list above.
The rule keys on the posture read's `browser_auth.enabled` and `subject`, both
owned by "The posture read" in `proposals/0013-build-the-13-10-web-ui.md`.

| `browser_auth.enabled` | `subject` | Control rendered |
|:--|:--|:--|
| true | absent | sign-in, as a top-level navigation to the sign-in path the read reports |
| true | present | sign-out, issued as a `POST` from the page carrying the same proof the panel's writes carry, after which the page navigates |
| false | absent or present | neither control |

Sign-out is issued as a `POST` rather than followed as a link because that is the
method the route answers, which "The route methods" in the same proposal owns,
and a control a human clicks has to issue the request the route answers. Both
conjuncts are required on each of the first two rows. The read reports the two
paths only when the flow is enabled, and each route is registered only where the
flow is enabled, so a control rendered on any other combination sends the browser
to a path the registry does not serve, and what comes back is the deployment's
answer for an unregistered path rather than anything the page can present ("The
status an unregistered path receives", in the same proposal). The third row
covers the deployments that run no browser flow, including the gateway-fronted
§13.10 deployment where a subject does resolve because the gateway authenticated
the request; clearing a Podium cookie would not end the gateway's own session
there. A read that does not answer leaves the page holding no value for either
key, so the table decides nothing for that case. It is owned by "The posture
read" in the same proposal, and the identity-states preamble above states the
presentation it fixes.

The control is keyed on the posture read the page takes when it loads. A session
that ends while the page is already rendered is signalled by the catalog read
rather than by the shell, on the terms the expiry-signal rule under "The browser
session" in the same proposal gives it, and the treatment for that transition
carries its own control. What that control may be is bounded by the third row:
where the read reports the browser flow disabled the page renders no
authentication control on any value of `subject`, so the expiry treatment on
such a deployment offers no sign-in affordance and has to state what it offers
in its place.

The transitions matter as much as the states: signing in, signing out, and a
session expiring mid-use while a page is already rendered. The signal for that
last transition is fixed by the expiry-signal rule under "The browser session" in
`proposals/0013-build-the-13-10-web-ui.md`. The catalog read is the panel's
expiry signal. The refusal a write receives when the caller's session can no
longer be verified carries no expiry information and is not an ownership
decision, so a design that reads that refusal as an ended session, or as a
statement about who owns the layer, reaches the wrong state on both counts. The
panel receives one refusal on that path and cannot tell a caller the owner gate
turned away from a caller whose session can no longer be verified, because both
arrive the same way and no field separates them. What the refusal means for
authorization is owned by the layer-write authorization rule, which the layer
panel above states, and it carries no session information.

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
