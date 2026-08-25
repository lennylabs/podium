# Handoff: Podium registry web UI

## Overview

The web UI for a Podium registry — the read surface for people who do not use the CLI or SDK, plus the layer-management surface for administrators and layer owners. Four core surfaces (domain browser, search, artifact viewer, layer panel), a ⌘K palette, and the write flows around layer registration and reingest.

The authority for what the UI does is `spec/13-deployment.md` §13.10 and the design brief derived from it. Where this handoff and the spec disagree, the spec wins.

## About the design files

The two HTML files in this bundle are **design references**, not production code. They are static prototypes showing intended look, structure, and states. Recreate them in the target codebase's environment using its established patterns.

The brief states the SPA is being rewritten in **React**, served from `web/` and embedded into the binary by `web/web.go`. Assume no server-side rendering: first paint, loading, and empty states are the client's problem.

Do not copy the inline styles out of these files. They are inline because of how the prototypes were authored, not because that is the recommended approach.

| File | What it holds |
|:--|:--|
| `Podium App.dc.html` | The screen mockups, grouped in numbered turns. Open it and read the board ids (14a, 17c, 20f…) — this README refers to them. |
| `Podium UI Inventory.dc.html` | The components, every state, in both themes, plus a "what uses what" dependency table. Build from this file. |
| `support.js` | Runtime for the two HTML files. Not part of the design. |

Open either file directly in a browser. No build step, no server.

## Fidelity

**High fidelity.** Colors, type, spacing, and copy are final. Recreate them faithfully. Where the mockups and this README disagree on a number, the mockups are correct.

Two caveats: the boards are drawn at a fixed 1440px width and have no responsive behaviour, and nothing in them is interactive — hover and focus states are documented below rather than demonstrated.

---

## Design tokens

Two sets, one name per role. **Components must never hard-code a hex.** The dark theme is the same component with a different token set — no `isDark` branches in component code.

| Token | Light | Dark | Role |
|:--|:--|:--|:--|
| `page` | `#f4f1ea` | `#0c0f16` | app background behind the shell |
| `surf` | `#ffffff` | `#171b28` | cards, rails, raised surfaces |
| `surf2` | `#fcfbf6` | `#141824` | inset areas, table headers, footers |
| `ink` | `#14130f` | `#eaeef7` | primary text |
| `sec` | `#43413a` | `#c4cbdb` | body text |
| `meta` | `#63604f` | `#9aa3ba` | secondary text |
| `faint` | `#6f6c5e` | `#7a8399` | tertiary text, labels |
| `bd` | `#d9d5c6` | `#2b3145` | borders |
| `b2` | `#e4e0d2` | `#232838` | dividers, inner rules |
| `chip` | `#eae7d9` | `#212739` | filled chips, inactive segments |
| `acc` | `#ffa424` | `#ffa424` | accent — identical in both themes |
| `accD` | `#f2861a` | `#ffa424` | accent, logo mark only |
| `onAcc` | `#14130f` | `#14130f` | text on accent |
| `link` | `#a8480a` | `#ffa424` | links, artifact ids |
| `wash` | `#fff3e2` | `rgba(255,164,36,.1)` | accent background |
| `cbd` | `#f5cd9b` | `#4a3a22` | accent border |
| `danger` | `#b3341c` | `#ff6b4a` | destructive, rejections |
| `dangerBg` | `#fdeee9` | `rgba(255,107,74,.12)` | destructive background |
| `dangerBd` | `#f0c4b8` | `#5c2a1e` | destructive border |
| `ok` | `#3f8f66` | `#5fbf8f` | success |
| `scrim` | `rgba(20,19,15,.42)` | `rgba(6,8,13,.62)` | behind dialogs |

Shadows: card `0 1px 2px rgba(20,19,15,.06)` light / `0 1px 2px rgba(0,0,0,.4)` dark. Dialog `0 30px 60px -24px rgba(20,19,15,.45)` light / `0 30px 60px -24px rgba(0,0,0,.8)` dark.

Radii: 5px badges · 6–8px inputs and buttons · 9–11px cards and panels · 14px dialogs · 100px pills.

### Type

**Space Grotesk** 400/500/600/700 for prose and UI. **JetBrains Mono** 400/500/700 for anything an implementer or the API would treat as an identifier — artifact IDs, domain paths, versions, hashes, error codes, counts, timestamps, keyboard hints, section labels. **Anton** only in the wordmark.

| Role | Size / weight / spacing |
|:--|:--|
| Page title | 29–30px / 700 / `-.025em` |
| Section title | 21px / 700 / `-.02em` |
| Subsection | 19–20px / 600 / `-.02em` |
| Body | 15px / 400 / 1.6 |
| UI default | 13.5px / 400–500 |
| Dense UI | 12.5px / 400 |
| Mono body | 12.5–13px |
| Mono meta | 10.5–11.5px |
| Section label | 10–11px / 600 mono / `.07–.12em` / uppercase |

Spacing runs on a 4px grid; the common steps are 6, 8, 10, 12, 14, 16, 18, 22, 26, 30, 40.

---

## Layout shell

Every screen is the same shell.

- **TopBar** — 52px tall, `surf`, 1px `bd` bottom border, 16px horizontal padding, 16px gap. Left to right: wordmark (24px mark + Anton 17–18px uppercase, `.03em`); registry hostname in 11px mono `faint` behind a 12px left padding and a 1px `b2` rule; flexible spacer; search trigger; 1px × 22px divider; Docs link (12.5px, `link`, external arrow, `white-space:nowrap`); divider; identity cluster.
  - The **search trigger** is 32px tall, `flex: 1 1 300px; max-width: 300px; min-width: 150px`, 1px `bd`, 8px radius, `surf2`, holding a magnifier, "Search artifacts", and a ⌘K key hint. It is a button that opens the palette, not an input.
  - **Identity** is a 24px circular `chip` avatar with mono initials and the email at 12.5px. It carries no role badge: no response reports that the caller holds the administrator role, so the shell renders nothing that predicts it. A caller with no subject replaces the cluster with a primary "Sign in", and the sign-in control rule in the brief decides whether that control is rendered at all. The inventory draws the four states that rule produces: a subject with a sign-out entry point, a caller with no subject and a sign-in control, and, on a deployment running no browser flow, a caller with no subject carrying no control and a resolved subject with no sign-out entry point.
- **Sidebar** — 268px fixed, `surf`, 1px `bd` right border, full height, flex column. Nav items (Browse / Search / Layers) at 13.5px in 7px-radius rows, active row filled `chip` at weight 600. Then a `CATALOG` section label with the depth marker ("3 levels") right-aligned. Then the tree. A footer pinned to the bottom by `margin-top:auto` states "4 layers · 312 artifacts" and "ingested 6m ago" in 10.5px mono.
  - The **Layers** nav item is rendered for every caller, on every deployment. This is a presentation decision and it reads no posture field: the panel renders its write operations and presents whatever refusal a write receives, so the nav predicts no outcome the server decides, and the panel stays available on the standalone deployment, where nobody authenticates and the panel is the point. Boards 14f and 14g carry it beside the anonymous and the refused catalog, and board 14i carries the panel itself for a caller who resolves no subject.
  - The footer copy for a caller with no subject follows the brief's catalog-scope rule. On the arm where the catalog read answers a public subset the footer states "Not signed in" and claims nothing about content beyond what was returned, and the sign-in control rule decides whether the shell renders a sign-in control at all. On the arms where the whole catalog is returned there is no public-view framing at all: the footer keeps its layer and artifact counts. Where the catalog read answers and the posture read does not, the footer takes the treatment that rule's third bullet gives that arm: it presents what the catalog read returned under the constraint the public-subset arm carries, so it states "Not signed in" and claims nothing about content beyond what was returned. Where the catalog read is refused there is no anonymous view to frame, and the refused-state screen stands in place of the catalog. The depth marker is kept in every case.
- **Main** — `display: grid; grid-template-columns: 268px 1fr`. Content padding is `26px 30px 40px`, max width 1120–1180px.
- A **PageBanner**, when present, sits between the TopBar and the grid, full width.

---

## Screens

Board ids refer to `Podium App.dc.html`.

### 1. Domain browser — 14a (light), 15a (at scale), 20a (trimmed), 14f (anonymous), 14g (refused catalog)

**Purpose:** the entry point and primary navigation. Backed by `load_domain`.

Breadcrumb, then an h1 with count badges beside it, then the description at 15px/1.6 with `text-wrap: pretty` capped at 720px, then keyword pills. Subdomains follow as a 3-column card grid, then artifacts as a bordered list.

Behaviour the API forces:

- **Curated versus surfaced.** `ArtifactDescriptor.Source` distinguishes an author's pick from a ranked one. Curated gets a `★ CURATED` badge in accent; surfaced gets the quiet label "SURFACED BY USAGE". The field can be absent.
- **Folded artifacts.** An entry with `FoldedFrom` is not a child of this domain. It goes in a separate dashed group titled "LIFTED FROM SPARSE SUBDOMAINS / Not direct children", each row carrying an `↑ FROM <subdomain>` badge.
- **Rendering note.** When the server trimmed the listing, show both a `listing trimmed` pill among the header badges and a line at the end of the list: "4 of 21 artifacts shown." with a "Load the rest" button. It must read as neither content nor error.
- **At scale (15a).** Past roughly twenty subdomains, switch to compact count tiles in a 6-column grid with a filter field, a grid/list toggle, and a "Show all 24 subdomains" control. Artifacts become a sortable DataTable with type filters and a curated section header; descriptions drop to one clipped line.
- **Anonymous (14f).** The board draws one arm of the catalog-scope rule: a deployment that authenticates callers, a caller who resolves no subject, and a catalog read that answered. It assumes the browser flow is enabled, so the shell carries the single sign-in control the sign-in control rule renders on that arm; on a deployment running no browser flow the same board is drawn without it. A neutral PageBanner states that the caller is not signed in and carries no control of its own, because the authentication control belongs to the shell. Nothing states or implies that artifacts were withheld or that hidden artifacts exist. The Layers nav item stands as it does on every other board. The whole-catalog arms carry no banner, and the refused arm is board 14g.
- **Refused catalog (14g).** The board draws the refused arm of the catalog-scope rule, on that rule's own keys: wherever a catalog read is refused because the caller's identity could not be verified, the caller has no anonymous view at all and the refused-state screen stands in place of the catalog. The arm carries no deployment qualifier and does not depend on whether the caller held a subject earlier in the page session. The screen states that the registry did not serve this catalog to this caller, says nothing about what the catalog holds, and offers a retry; the sidebar tree and the footer counts are empty. The authentication affordance beside it is whatever the sign-in control rule renders for the same posture read, so the board as drawn carries none, and the variant on a deployment that runs the browser flow with no subject resolved carries the shell's sign-in control beside the retry rather than retry alone. Session expiry (15k) is the same arm reached later in a page session, and it keeps the page underneath.

### 2. Search — 14b, 20b

Backed by `search_artifacts`. Filters are fixed by the spec to **type, scope, and tags**.

A 46px search field with a blinking accent caret, then a filter row: active filters as filled accent pills with a remove affordance, inactive as outlined, plus a dashed "+ tag". The result count sits at the row's right as "Showing 6 of 143".

- The match count is taken **before** truncation, so N-of-M is normal, not an error. Offer the §5 recovery path: narrow with filters, drill into a subdomain, or run a more specific query.
- **Relevance** renders as four bars, not a number. A vector-only match returns score 0 — render **no bars at all** and put a "matched by meaning" tag in the metadata row. The bar column keeps its width so rows stay aligned.
- **Sensitivity** is an outlined badge in the metadata row, the same weight as type and version. It is informational, never an alert, and is absent on unclassified artifacts.

### 3. Artifact viewer — 14c, 15b, 15c, 20c, 20d, 20e, 20f

Backed by `load_artifact`. Two columns: content at `1fr`, a 316px rail on `surf2` with a 1px `bd` divider.

Header is breadcrumb, h1, type and version badges, description. Then tabs: **Rendered · Frontmatter · Authored source · Resources**, the last with a count badge.

- **Rendered** — markdown as a document. Headings 20px/600, body 15px/1.65, ordered lists 1.75 line-height, inline code in `chip` at 13.5px mono with 4px radius.
- **Frontmatter (15b)** — a full-width property table with a Table / Raw YAML toggle. The block is raw YAML parsed **client-side**, so a parse failure is a real state: the tab badge becomes `!`, an error banner says "Invalid syntax" with the parser's line and column, and the raw block renders below with the offending line highlighted in `dangerBg`. Nothing else on the artifact is affected.
  - **No pairs to render.** A response can yield no frontmatter pairs at all, and that is a finished document rather than a partial load. Omit the property table entirely: no column header standing over an empty table, no placeholder row, and no error styling. The tab carries no `!` badge, and a single quiet line at 12.5px `faint` reads "No frontmatter on this artifact." It must stay visually distinct from the "Invalid syntax" state, which is the only frontmatter state drawn in `danger` tokens.
- **Authored source (15c)** — the file byte for byte, line numbers in a `surf2` gutter, Copy and Download. Populated only for skill artifacts and cleared when the manifest arrives by link; the tab must be able to disappear without leaving a hole.
- **Resources (20f)** — a table of file, format, size, delivery, and a Download action on every row. Inline and fetched files are **one list** distinguished by a `delivery` column, not two lists. No previews: every file downloads, and a "Download all ↓ 168 MB" control sits above the table. The selected row is tinted `wash` with an inset accent bar and drives a SELECTED detail card below it.
- **Body fetched from object storage (20d/20e)** — a large manifest arrives as a presigned URL. While fetching, show skeleton lines and "Fetching the artifact."; the rail is already populated because metadata came with the registry response. On failure, an InlineError with Retry sits **in the body area only** — the rest of the page stays usable.
- **Rail** — PROVENANCE (layer, visibility, ingest ref, content hash), FRONTMATTER as a property table, RELATIONS (extends / extended by, fetched on a second request via `handleDependents`), RESOURCES split into inline and fetched-on-demand. Each section needs an absent state; use the inline EmptyState ("Nothing extends this artifact"). FRONTMATTER is the exception: where the response yields no pairs, the rail drops the section header along with the table rather than standing an empty table under it, so the rail reads as PROVENANCE followed directly by RELATIONS.
- **Versions.** `load_artifact` defaults to latest but takes any version. The picker sits in the header, and viewing an older version puts an accent notice across the page with a "Go to v2.3.0" link.

### 4. Layer panel — 14d, 14e, 14h, 14i, 17f, 17g

The only surface with writes. Its contents differ by layer class rather than by caller role, because the list endpoint hands the panel every layer under the tenant and no response reports the caller's role.

Header with title, description, and actions: "↺ Recently unregistered · 3" as a quiet accent link, then "Register layer" (primary) and "Reingest all" (secondary). Below, a label reading "PRECEDENCE — DRAG TO REORDER / lower row wins" — **the winning end must be labelled**, not implied by position.

The table columns are drag handle (34px), layer, source, visibility, last ingest, actions (120px). All rows share one grid.

- **Visibility is a union of axes.** A layer can be public, organization-wide, group-scoped, and user-scoped at the same time. Render one marker per matching axis, in the fixed order public, organization, groups, then users, in one wrapping cell, so two layers carrying the same grants read identically. Where an axis names more members than the row can hold, that axis's marker summarises the remainder inside itself (`group: secops · appsec +4`), so no axis is dropped to make room and the full membership belongs in the row's detail. A layer with no grants shows "no grants — only you". The LayerRow inventory draws both the multi-axis row and the overflow row.
- **Source types are pluggable.** An unknown type still renders: a generic chip with the type name, its details behind a Disclosure ("▸ 4 source fields"), actions unchanged.
- **Roles.** An administrator manages every layer, and an ordinary user manages the layers that user defined. The layer list endpoint is not scoped by caller, so this split is presentation over a list the server hands you whole. No response reports that the caller holds the administrator role, so the panel predicts no outcome: it renders its write operations on every row, marks a row the caller does not own as owned elsewhere, and presents whatever refusal a write receives rather than reading it as a failure of the page. The marker compares the row against the caller's own subject, and the posture read reports a subject only where one resolves, so a caller with no subject gets no ownership marker on any row. The marker is reserved for a caller whose subject resolved, and whether a write is admitted is decided by the deployment's configuration rather than by whether this caller resolved a subject. Board 14i draws the panel for a caller who resolves no subject: every write control is rendered and no row carries an ownership marker.
- **A refused write (14h).** A write the panel sends can come back refused, including on a row the panel presented as the caller's to manage, so the refusal is a drawn state rather than a failure of the page. It is drawn on the row and on the action that was attempted, in `danger` tokens, with a Try again and a Dismiss beside it; every other control on the panel stays live. It says only that the registry refused that action and that nothing changed. It reports neither who owns the layer nor the state of the session, so it reuses neither the ownership marker nor the 15k expiry dialog, and it is not the read-only banner, which is presented before any write is attempted and mutes every control at once. The LayerRow inventory carries the same state on the component the writes live on.
- **Read-only mode (14e).** `§13.2.1` puts a marker on read responses, so present the state **before** a write is attempted: one banner reading "Something went wrong — the registry is temporarily read-only. Browsing and search still work.", every write control muted at once. Never a failure per button press.
- **Recently unregistered (17g).** Unregister is a soft delete recoverable for 30 days. This table lists layer, source, artifact count, unregistered date, erase date with a depleting bar, and Restore. Accent marks three days or fewer.

### 5. Layer write flows — 17a, 17b, 17c, 16a, 16b, 15f

**Register (17a/17b).** A Modal at 660–680px. First a source segmented control, Git repository / Local folder. Then repository, ref, and root (or local path). Then visibility as four combinable **checkboxes** — Public, Organization, Groups, Specific users — with Public and Organization side by side. Groups holds a nested TokenInput plus a group Combobox.

- The **group picker (16a)** is a typeahead over IdP groups rendered **in flow**, not as an overlay, so the dialog does not have to grow around it. It shows "5 of 214 match", each row's member count, and a "you're a member" tag; three rows visible with a fade, the rest scroll. Register stays disabled until a group is chosen.
- One line states the consequence in the reviewer's terms, clamped to two lines so a long user list cannot push the dialog around.
- A neutral note always reads "Visibility is fixed at registration."
- **Layer cap (17c).** The registry refuses past the per-user limit with an error carrying the limit and current count. Render it where the user creates the layer — "You've reached your layer limit — 3 of 3" plus their existing layers — not as a generic failure.

**Secret reveal (15f).** Registering a git layer returns a webhook URL and an HMAC secret. The URL is permanent; the secret is returned only here and on rotation. Put the secret in a dashed accent block with a `SHOWN ONCE` badge, a Copy action, and a line stating Podium stores a hash. Done stays disabled until an acknowledgement checkbox is ticked. A local-path layer returns neither, so the whole reveal is conditional.

**Unregister (17f).** A confirmation stating **both halves**: "38 artifacts will disappear from every user's view next time they sync" *and* "Recoverable for 30 days" with the date. Requires the layer ID typed to confirm.

**Update.** Not yet mocked. `POST /v1/layers/update` patches visibility, ref, root, local_path, owner, force_push_policy, and rotates the webhook secret. On a user-defined layer the registry ignores owner and visibility and still answers 200 — so the form must not offer controls for values it cannot change. Rotation reuses the 15f reveal and needs a disabled state on a local layer.

### 6. Reingest — 17d, 17e, 18a, 18b, 18c, 18d, 18e

`POST /v1/layers/reingest?id=` runs the whole pipeline **inside the request**. No job id, no progress channel.

- **One layer (17e)** — a single spinner, an elapsed clock, and a plain statement that nothing is reported until the request returns. "Stop waiting" abandons the wait, not the ingest.
- **All layers (17d)** — one request per layer in sequence. A row changes only when its own request returns; do not fabricate progress. Finished rows show what their response actually returned ("61 accepted · 0 rejected").
- **Summary (18a)** — five stat cards from the response: `accepted`, `idempotent` (as "unchanged"), `rejected`, `conflicts`, `lint_failures`. **Only counts the API itemises are clickable** — `artifacts`, `rejected`, `conflicts`, `embedding_failures` and `advisories` come back as lists; `lint_failures` is a bare `len()`, so it is captioned "count only" and links to the ingest log. Non-blocking advisories follow with severity, artifact id, code, and message, capped with a "See all 14".
- **Detail (18b)** — tabs for Accepted, Rejected, Conflicts, Advisories. A rejection carries `artifact_id`, `code`, `reason`. A **conflict is an immutability violation**, not a cross-layer collision: same version, different content, carrying `old_hash` and `new_hash`, and the fix is bumping the version. Cross-layer collision is one of the *rejection* reasons.
- **Whole snapshot rejected (18c)** — when nothing was accepted and nothing was idempotent, the endpoint returns **HTTP 409** `ingest.immutable_violation` with the conflicts in the error details. That is a different screen: "Nothing was ingested", the layer unchanged, not retryable until the content changes.
- **Break-glass (18d)** — reingesting during a freeze window requires `break_glass`, a non-empty `justification`, and **two distinct approvers**. A destructive-tone confirm; the action is recorded in the audit log.
- **Queued (18e)** — with no ingest runner wired the handler answers `queued` with no summary. There is nothing to wait for; say so and point at the layer's last-ingest time.

Error codes to branch on, from `writeReingestError`: `ingest.frozen`, `ingest.history_rewritten`, `ingest.lint_failed`, `quota.storage_exceeded`, `quota.audit_volume_exceeded`, `ingest.public_mode_rejects_sensitive`, `ingest.source_unreachable`, `registry.unavailable`.

### 7. Command palette — 19a, 19b, 19c

⌘K from anywhere. A 660px panel over a scrim, 96px from the top.

**Artifacts only** — domain navigation lives in the tree. Typing shows a group heading with the count, rows of artifact name, path, type and version, and a keyboard footer: ↑↓ navigate, ⏎ open, ⌘⏎ for all results on the Search surface, esc. Just-opened shows recent queries plus the inline filter syntax (`type:skill`, `tag:review`, `scope:platform`), which teaches the query language the Search page exposes as chips. No match offers a spelling correction and says nothing about what might be hidden.

### 8. Authentication — 15k, 15l

Sign in has no screen of its own. It is the shell control the brief's sign-in control rule fixes: a top-level navigation to the sign-in path the posture read reports, after which the identity provider owns the page. Board 14f shows the control in the anonymous top bar. Nothing is drawn as an in-page flow, and no device-code step belongs here: the device grant is the CLI and SDK acquisition path, and the browser flow reads no device-code endpoint.

Sign out is a `POST` the page issues, carrying the same proof the panel's writes carry, after which the page navigates. Render it as a control rather than as a link, because that is the method the route answers. It is conditioned the same way "Sign in" is: the brief's sign-in control rule renders it only where the posture read reports the browser flow enabled and a subject resolved. A subject that resolved on a deployment running no browser flow gets the account menu without it, which is the AccountMenu state the inventory draws beside the signed-in one.

Session expiry (15k) is one sentence — "Your session has expired. Please log in again." — over the page the user was on, which is **kept, not cleared**. The signal is a catalog read refused because the identity could not be verified; a refused write says nothing about the session. The caller reaching this state has no anonymous view of the catalog, so the dialog offers no browsing mode. The control beside the message is whatever the brief's sign-in control rule renders for the deployment's posture. Board 15k is drawn on a deployment that runs the browser sign-in flow, where that control is "Sign in". On a deployment running no browser flow the rule renders no authentication control at all, and expiry is reachable there because the signal carries no credential qualifier, so the dialog states the same sentence and offers a retry of the refused read as its only control. The account menu (15l) carries name, email, appearance (System / Light / Dark), layer quota, API tokens, and Sign out. It carries no role badge, and it does **not** list group memberships; a user can be in many.

---

## Interactions

| Element | Behaviour |
|:--|:--|
| Rows and cards | Hover raises background one step toward `chip`; no transform, no shadow change |
| Buttons | Hover darkens the fill or border one step; active drops 1px |
| Focus | 1px `acc` border plus `0 0 0 3px rgba(255,164,36,.16)` ring, on every focusable |
| Text caret | 1.5px `acc`, `blink 1.1s step-end infinite` |
| Spinner | 2–3px ring, `acc` top, `spin 900ms linear infinite` |
| Tree | Two levels eager; deeper levels load on expand; a restricted domain is listed but not enterable |
| Drag to reorder | Row lifts with an accent border and a 2px accent drop indicator; commits on drop; takes effect on the next read and does not trigger a reingest |
| Dialogs | Open over a scrim on the page they came from; esc and scrim click dismiss; destructive ones require typed confirmation |
| Palette | ⌘K opens, ↑↓ move, ⏎ opens, ⌘⏎ goes to full search, esc closes |
| Copy | Every CopyField has an explicit Copy button; never click-to-copy without an affordance |

Prefers-reduced-motion should stop the caret blink and the spinner rotation; neither carries information the static form lacks.

## State the client owns

- **Identity** — read from the posture read: whether the browser flow is enabled, whether a subject resolved, and the deployment's identity posture. The administrator role is not reported, so the client holds no admin state. Never inferred from what a domain returns.
- **Theme** — system / light / dark, persisted; `data-theme` on the root overrides `prefers-color-scheme`.
- **Read-only** — read from the marker on read responses, applied to every write control at once.
- **Per-surface** — loading, empty, error, forbidden. Every list and every rail section needs all four.
- **Palette** — open state, query, selected index, recent queries.
- **In-flight reingest** — per-layer status for the fan-out, since a page navigation abandons it.

Two rules with teeth:

1. **Empty and filtered must be indistinguishable.** A domain assembled from your visible set carries no field saying filtering removed anything. Never hint that hidden artifacts exist.
2. **Not-found and not-permitted render identically.** A single-artifact load for something you may not see returns the same not-found error as something that does not exist, deliberately. Do not undo that.

## Components

`Podium UI Inventory.dc.html` holds the components, in both themes, with every state. Section 07 of that file is the dependency table: build primitives first, then composites, then surfaces.

Rules the set depends on:

- One row component per entity. ArtifactRow and LayerRow cover the catalog entities; PaletteRow is ArtifactRow at palette density. A DataTable's other rows are plain cells over the shared column grid.
- Badges take a tone, not a domain concept. Type, version, visibility, provenance, and error codes all reuse one badge.
- One feedback container. Banner carries tone and scope; PageBanner and InlineError are presets over it.
- Density is a prop, not a component.
- Every "N of M" is one ResultCount. Four surfaces report the same query differently the moment each formats its own.
- A result you must read is not a toast. Reingest returns rejections and conflicts that need action, so it resolves into a Modal.
- Nothing fetches. Components take data as props; a screen owns the request.
- Absent is a designed state, not blank space — description, keywords, version, score, sensitivity, relations, resources.

## Assets

No image assets. The wordmark is inline SVG: a filled circle at `cx=36 cy=28 r=15` in `accD` over a `4,54 64×5` bar in `ink`, viewBox `4 13 64 46`, beside "PODIUM" in Anton, uppercase, `.03em`. Icons are inline SVG at 9–16px with 1.5–1.6px strokes: magnifier, chevron, arrows, plus a few text glyphs (✓ ✕ ↺ ⋮⋮ ▸ ↗ ↓).

Fonts load from Google Fonts: Space Grotesk 400–700, JetBrains Mono 400–700, Anton 400. Self-host them for an air-gapped registry.

## Not yet designed

Named so they are not mistaken for oversights: the update-layer form; empty states for an empty domain, a search with no results, a registry with no layers, and an empty restore list; not-found and forbidden pages (the ErrorPage component exists, the screens do not); loading states for the browser, search, and layer list; the non-admin layer panel; the standalone deployment with no identity provider; browse-mode search with filters and no query; a surface over `GET /v1/scope/preview`; and any responsive behaviour below 1440px.
