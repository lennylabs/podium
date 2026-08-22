# Web UI design brief

Input for a UI design pass on Podium's web UI. It describes what the UI must
surface, the data behind each surface, and the states each surface has to
handle. It does not prescribe layout, visual treatment, or component structure,
which are the design pass's output.

The authority for what the UI does is `spec/13-deployment.md` §13.10. Where this
brief and the spec disagree, the spec wins and this file is the defect.

## What the UI is for

A registry serves a catalog of artifacts: skills, contexts, and the other
first-class types. The CLI and the SDKs read that catalog for developers. The
web UI is the path for people who do not use a terminal, named in §13.10 as
analysts, prompt authors, and reviewers, and it is also where an administrator
manages the layer list.

The UI is served by the registry process itself at `/ui/`, on the same origin as
the API it calls. It is a client of the public HTTP API and has no privileged
access: everything it displays comes from the same endpoints an SDK would call,
filtered by the caller's identity.

## Current state

The existing implementation is a 129-line vanilla-JS file covering roughly one
and a half of the four surfaces below. It is a placeholder, not a baseline to
preserve. Treat this brief as a first design rather than a redesign, and do not
carry forward the current structure.

## Constraints

- **React.** The SPA is being rewritten in React. A build step is therefore
  expected, and the built bundle is embedded into the Go binary and served as
  static files. Assume no server-side rendering.
- **Same origin, no proxy.** Every API call goes to the same host that served
  the page. There is no separate API domain and no CORS.
- **The registry may be reached directly or through a gateway.** Both are
  supported deployments and the UI cannot tell which it is in.
- **Design tokens already exist.** `site/src/styles/tokens.css` defines the
  palette, type, and spacing the documentation site and the diagrams use. The UI
  should inherit that vocabulary rather than introduce a second one. It defines
  light and dark values, so the UI is expected to support both.
- **Content is user-authored and unbounded.** Artifact bodies are markdown of
  arbitrary length, descriptions can be long, and identifiers are
  slash-separated paths that can be deep. Nothing should assume short strings.

## The four surfaces

### 1. Domain browser

The catalog is a hierarchy of domains, each holding subdomains and artifacts.
This surface is the entry point and the primary navigation.

`GET /v1/load_domain?path=<domain-path>` returns:

| Field | Type | Notes |
|:--|:--|:--|
| `path` | string | the requested domain; empty string is the root |
| `description` | string | optional |
| `keywords` | string[] | optional |
| `subdomains` | object[] | each: `path`, `name`, `description`, and optionally a nested `subdomains` array when more than one level is expanded |
| `notable` | object[] | artifact descriptors, see below |
| `note` | string | optional; a rendering note from the server |

A `notable` entry carries `id`, `type`, `version`, `description`, `tags`,
`summary`, and two fields that matter to the design:

- `source` is `"featured"` for an author-curated entry or `"signal"` otherwise.
  The distinction is meaningful to a reader and is currently invisible.
- `folded_from` is set when an artifact was lifted into this domain from a
  sparse subdomain below it. Such an entry is not a direct child, and showing it
  as one misrepresents the hierarchy.

Subdomains can nest arbitrarily deep, and `subdomains` may itself carry a nested
tree, so the design needs a position on how much depth to render at once.

### 2. Search

`GET /v1/search_artifacts?query=<text>` with optional `type`, `scope`, and
`tags` filters, which §13.10 requires to match what the SDK and CLI offer.
Returns `query`, `total_matched`, and `results`, each an artifact descriptor
carrying `id`, `type`, `version`, `description`, `tags`, `score`, `sensitivity`,
and `frontmatter`.

Two fields drive design decisions:

- `score` is a relevance score. Whether to expose it, and how, is open.
- `sensitivity` is a classification label. It is absent on unclassified
  artifacts and needs a treatment that reads as a property of the artifact
  rather than as an alert.

`total_matched` can exceed the number of returned results, so the design needs a
way to express "showing N of M".

### 3. Artifact viewer

`GET /v1/load_artifact?id=<id>` returns the full artifact:

| Field | Notes |
|:--|:--|
| `id`, `type`, `version` | identity |
| `content_hash` | the artifact's hash; useful for provenance display |
| `manifest_body` | the artifact body, **markdown**, to be rendered rather than shown as source |
| `frontmatter` | structured metadata, to be shown as a **property table** rather than as raw text |
| `skill_raw` | for skills only, the verbatim authored file |
| `layer` | which layer served it |
| `sensitivity` | optional classification |
| `resources` | map of filename to inline content |
| `large_resources` | map of filename to a link the client fetches separately |

§13.10 also requires **links to extending or dependent artifacts**, so an
artifact is a node in a graph and the viewer is where that graph becomes
navigable.

Two cases the design must handle explicitly: an artifact whose body is delivered
by URL rather than inline because it exceeded a size cutoff, and an artifact
carrying resources of both kinds at once.

### 4. Layer panel

The catalog is assembled from layers, each pointing at a git repository or a
local path, each with its own visibility. This is the only surface with write
operations, and the only one whose contents differ by role.

A layer carries `id`, `source_type` (`git` or `local`), `repo`, `ref`, `root`,
`local_path`, `order` (precedence, lower is lower), `user_defined`, `owner`,
`last_ingested_at`, `last_ingested_ref`, and a visibility that is one of public,
organization-wide, group-scoped, or user-scoped.

Operations, all over the same `/v1/layers` endpoints the CLI uses:

- **Register** a layer, which returns a webhook URL and a secret **shown once**.
  That one-time reveal is a specific design problem: it must be copyable, it
  must be clearly unrecoverable, and it must not be mistaken for persistent
  content.
- **Update** a layer, including rotating its webhook secret, which triggers the
  same one-time reveal.
- **Reingest** a layer, which is a long-running operation.
- **Unregister** a layer, which removes its artifacts from every caller's view
  and is destructive.
- **Reorder** layers, since `order` determines precedence when two layers carry
  the same artifact ID.

Roles differ: an administrator manages every layer in the tenant; an ordinary
user manages only their own user-defined layers and is subject to a cap on how
many they may create; an anonymous caller sees no panel at all.

## The state matrix

Most of the design work is here rather than in the happy path.

**Identity states.** The UI has three, and cannot always tell them apart from
the client side:

1. **Anonymous.** No credential. The catalog renders, filtered to public
   artifacts. Nothing is broken and no error has occurred, so an anonymous view
   must not read as a failure. This is a legitimate browsing mode.
2. **Authenticated user.** Sees public artifacts plus those their layer
   visibility admits, and manages their own user-defined layers.
3. **Administrator.** Sees the full layer panel with destructive operations.

The transitions matter as much as the states: signing in, signing out, and a
session expiring mid-use while a page is already rendered.

**Per-surface states.** Each of the four surfaces needs: loading, empty,
error, and forbidden. Two deserve specific attention:

- **Empty versus filtered.** A domain with no visible artifacts looks identical
  to an anonymous caller and to a user whose visibility excludes everything in
  it. Whether the UI distinguishes these, and how it does so without disclosing
  that hidden artifacts exist, is a real design question with a privacy
  constraint attached.
- **Not-found versus not-permitted.** The API returns `registry.not_found` for
  an artifact the caller may not see, deliberately, so that invisibility
  discloses nothing. The UI must not undo that by implying the artifact exists.

**Errors** arrive as a JSON body with a `code`, a `message`, and a `retryable`
flag. The code is a stable identifier such as `registry.not_found` or
`registry.read_only`; the message is prose. The design should assume error text
is not always suitable to show verbatim.

**Read-only mode.** The registry can enter a degraded mode where reads work and
every write is rejected with `registry.read_only`. The layer panel needs a
coherent presentation of that state rather than a failure per button press.

## Out of scope

- Authoring or editing artifacts. The UI is a reader and a layer manager;
  artifacts are authored in git.
- Any admin surface beyond the layer panel.
- Any visual identity beyond what the existing token set already establishes.

## What the design pass should produce

1. Layouts for the four surfaces, including the nested and deep cases rather
   than only a shallow example.
2. The state treatments named above, in particular anonymous browsing, the
   one-time secret reveal, and the destructive-operation confirmation.
3. A component inventory sufficient to build from, given React.
4. A position on the open questions this brief names: how much domain depth to
   render at once, whether to expose relevance score, how to treat sensitivity,
   and how to distinguish empty from filtered without disclosing hidden content.
