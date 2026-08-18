# Podium documentation site

The generator and the web application for <https://lennylabs.github.io/podium>.

Markdown under `docs/` is the source of every documentation page. This package
reads that markdown, builds one model per page, and renders each model to static
HTML with React. Nothing here writes to `docs/`.

## Commands

Run these from `site/`, or use the wrapper targets from the repository root.

| Command | Root target | Behavior |
| --- | --- | --- |
| `npm run build` | `make docs-build` | Full build into `site/dist/` |
| `npm run dev` | `make docs-dev` | Rebuilds on change and serves on port 4321 |
| `npm run check` | `make docs-check` | Validation gate, plus the test suite from the root target |
| `npm test` | | Vitest unit, integration, and rendering tests |
| `npm run coverage` | | Line coverage over `src/` |
| `npm run typecheck` | | `tsc --noEmit` |

Node 20 or later is required. `npm ci` installs the toolchain.

## Layout

```
src/
  build/          the generator: discovery, the content pipeline, and output
    content/      frontmatter, links, alerts, directives, headings, highlighting
  components/     React components, grouped by role
    islands/      the island registry and its prop validator
  pages/          Landing, Doc, and NotFound
  client/         the browser bundle: theme, copy, search, islands, and the router
  styles/         design tokens and stylesheets
design/           the design handoff: tokens, screens, and four mockups
test/             fixtures and the test suite
```

## The page model

Discovery walks `docs/` and classifies every file. A markdown file carrying
frontmatter is a page. A markdown file without frontmatter is published verbatim
at its source path, and so is everything under `docs/assets/`.

Each page produces a `PageModel` (`src/build/types.ts`) holding its route,
title, description, heading outline, action links, island references, and a
hast body. The body is JSON-serializable, so the model can be inspected in a
test and reused by the search indexer without re-parsing.

The build runs three passes, because each needs the whole corpus before the
next can run. Discovery assigns every route, so a link has something to resolve
against. Parsing produces each tree and its heading ids, so an anchor can be
checked. Finishing rewrites links and highlights code against the complete
index.

## Frontmatter

Any key outside this set is a build error, so a typo fails the build rather than
being ignored.

| Key | Required | Purpose |
| --- | --- | --- |
| `title` | yes | Page title, navigation label, and `<title>` element |
| `description` | yes | Meta description and search result summary |
| `nav_order` | no | Order among siblings. Pages without it sort after ordered pages, alphabetically by title |
| `nav_title` | no | Navigation label when it should differ from the page title |
| `permalink` | no | Explicit route when a page's route must differ from its source path |
| `actions` | no | Call-to-action links, the first rendered as primary |
| `hidden` | no | Excludes the page from navigation and search while still publishing it |
| `include` | no | Repo-root-relative file whose markdown is appended to the page body |

`include` publishes a file that is maintained outside `docs/`. The changelog
page names `CHANGELOG.md`, which the release process edits at the repository
root, so the page follows it with no second copy to keep in step. The appended
markdown runs through the rest of the pipeline like any other body, so its
headings, links, and fenced blocks are checked. An included file's opening h1 is
dropped, because the including page already carries a title.

A file that needs adjusting before it sits inside a page takes the options form:

```yaml
include:
  file: CHANGELOG.md
  skip_sections: [Unreleased]
  demote: 1
```

`skip_sections` drops a heading and everything under it, matched on the
heading's text with any link brackets removed. `demote` pushes every included
heading down that many levels, which is how a standalone file's outline nests
under the including page's own headings. It also decides how much of the file
reaches the rail on the right, which lists h2 and h3: the changelog demotes by
one so its version headings land at h3 and their `Added` and `Changed`
subheadings at h4, leaving the rail one entry per release. The page supplies the
h2 those versions sit under, since a body that jumps from h1 to h3 fails the
heading-order check.

Navigation comes from the directory tree. A directory is a section, its
`index.md` is that section's page, and `nav_order` orders siblings. No page
names its parent.

## Callouts

Callouts use GitHub's alert syntax, so a page reads correctly both here and on
github.com.

```markdown
> [!NOTE]
> Podium is at 0.1.x, an early release.
```

`NOTE`, `TIP`, `IMPORTANT`, `WARNING`, and `CAUTION` are recognized. A
blockquote with no marker stays a blockquote.

## Action links

```yaml
---
title: Overview
description: ...
actions:
  - label: Quickstart
    href: getting-started/quickstart
---
```

`href` goes through the link resolver that checks body links, so a broken action
link fails the build. An `href` carrying a scheme is external and is emitted as
written.

## Components

A component occupies a block and is written as a directive. The directive name
is the registry key, so one lookup produces both the component and its prop
schema.

A leaf directive names a component and its props:

```markdown
::diagram{name="sync-watch" alt="How the watch loop materializes files"}
```

A container directive wraps content the component arranges. Nesting follows the
fence convention: the outer container carries more colons than the inner one.

```markdown
::::tabs
:::tab{label="Homebrew"}
...markdown...
:::
:::tab{label="Scoop"}
...markdown...
:::
::::
```

There is no inline form. Prose containing colon patterns such as `17:00` or
`1:1` parses as an inline directive, and anything that does not name a
registered component is restored to the text as written.

### Adding a component

Add an entry to `src/components/islands/registry.ts`:

```ts
"sync-playground": defineIsland<{ artifact: string }>({
  props: { artifact: { kind: "string", required: true } },
  children: { kind: "none" },
  fallback: { from: "prop", name: "artifact" },
  load: async () => ({
    default: (await import("../content/SyncPlayground")).SyncPlayground,
  }),
}),
```

The entry declares three things.

**Props.** Directive attributes arrive as strings, and the validator coerces
each to its declared kind. `string` accepts an optional `oneOf`. `route` is
resolved by the link resolver. `asset` names a file that must exist, and
`within` plus `extension` let an entry accept a bare stem. An undeclared
attribute, a missing required prop, and a value that fails coercion each fail
the build with the file, the line, and the column.

**Children.** `none` is a leaf. `markdown` is a container whose content the
component arranges. `directives` is a container whose direct children must all
be a named child directive, with a minimum count.

**A fallback.** Every component names one, either a required prop holding a
path or the container's own content. The fallback is what a reader without
JavaScript, a crawler, and a reader who has asked for reduced motion are left
with, so it has to carry the page's meaning on its own. The registry itself is
validated before any markdown is read, so a fallback naming an undeclared or
optional prop fails immediately.

### How a component reaches the browser

The two kinds attach differently, because React requires a hydrated tree to
produce the markup the server already wrote.

A container island is rendered on the server by the same component that runs in
the browser, from data rather than from React children. The client reads that
data back out of the DOM and hydrates over markup that matches.

A leaf island renders its static fallback on the server. The component's output
is deliberately different from that fallback, so the client mounts a fresh root
over it rather than hydrating.

Runnable commands stay in the markdown body. `tools/doccov` classifies pages by
their fenced blocks and the end-to-end suite executes them, so a component
accompanies the commands rather than replacing them.

## Responsive layout

Three layouts, from the design's breakpoints.

| Width | Documentation | Landing |
| --- | --- | --- |
| ≥ 1240px | Tree, article, and the on-this-page rail | Full layout |
| 1100–1239px | Tree and article; the rail is dropped | Full layout |
| 721–1099px | Article alone; the tree becomes a slide-over | Single column |
| ≤ 720px | Article alone; the tree becomes a full-width drawer | Mobile layout |

At mobile widths the documentation bar keeps the menu button, the brand, the
version, and a search icon. The search field, the section tabs, and the theme
toggle move into the drawer, which is the only place they fit; both copies are
in the markup and the breakpoint decides which one shows, so neither is
duplicated on screen. Touch targets are 44px, pulled back by the difference to
the gutter so a glyph still lines up with the text below it.

`src/client/sidebar.ts` records whether the reader has asked for the drawer, as
`data-open` on the tree and `data-drawer-open` on the body. Whether the tree is
a drawer at all is the stylesheet's decision: at a width where it is part of the
layout those attributes select nothing, so one set of markup serves every width.
The module closes the drawer when a link inside it is followed, on Escape, and
when the viewport reaches the width at which the tree rejoins the layout.

The landing page's nav links move behind a `<details>` disclosure rather than a
scripted panel, so the menu opens before the bundle loads.

## Client navigation

Every page is complete HTML, and the browser bundle adds a navigation layer over
it. A click on a link to another documentation page is intercepted, the target
page is fetched, and its article region and on-this-page rail replace the ones on
screen. The top bar and the navigation tree stay in the document, so a navigation
keeps the tree's scroll position, its collapsed groups, and the listeners bound
on first load.

`src/client/router.ts` holds the layer. The build side is a set of attributes:
`data-doc-article` on the article element in `src/pages/Doc.tsx`, `data-doc-toc`
on the rail in `TableOfContents`, and `data-topbar-tabs` on the section tabs in
`Header`. Rendering stays on the server, and the router moves markup the build
already produced.

Alongside the swap the router updates the address bar through `pushState`, the
document title, the canonical link, the Open Graph fields, the current marker in
the navigation tree, and the section tab in the top bar. The breadcrumb and the
pager arrive with the article. `popstate` drives back and forward, and each
entry's scroll position is recorded in its history state.

### Links the router leaves alone

A link is handed back to the browser when it points outside the site, carries a
`download` attribute, opens in another frame, is a bare fragment, carries a query
string, or is clicked with a modifier key or a middle click. The landing page and
the 404 page carry a different layout, so they are reached by a full navigation
as well.

A fetch that fails, answers anything other than 200, or returns a document with
no article region ends in `location.assign`, which loads the target the ordinary
way.

Without JavaScript nothing is intercepted, and every link is an ordinary
navigation to a page that already exists in the output.

### After a swap

The per-page initialisers run again over the incoming article. The copy buttons
are bound, the on-this-page observer is disconnected and built again over the new
headings, and any island the new content declares is mounted. The roots of the
outgoing article are released first, through `unmountIslands` in
`src/client/islands.ts`, because React holds the DOM it rendered.

Focus moves to the article, which is why it carries `tabindex="-1"`, and a polite
live region names the page that loaded. Scrolling is instant in both the
top-of-page and the fragment case, so a swap lands the way a page load does. A
bar appears at the top of the viewport when a fetch runs longer than 200ms. Its
sweep runs only for a reader who has not asked for reduced motion, and the bar
itself is visible either way.

The router dispatches `podium:navigate` on `document` when it takes a click and
`podium:navigated` once the new article is in place. The search overlay listens
for the first one and closes; anything else that holds page-level state can use
the same pair.

## Caching

Every file the browser fetches by a fixed name is a file the browser can serve
after it changes. The stylesheet, the client bundle, and the search index are
written under a name containing a hash of their contents, so each is immutable
and a page can reference the new one the moment it is built. The browser icons
keep fixed paths because a favicon is referenced by convention, so their links
carry a version query taken from the icon bytes.

Pages themselves are served with a short max-age. The router fetches them with
`cache: "no-cache"`, which revalidates against the server and costs a 304 when
nothing changed, so a client navigation never renders a page the reader has
already been told is stale.

## Checks

`npm run check` fails on an unknown frontmatter key, a missing required key, an
unresolvable link, a link to a missing anchor, an unknown directive, a bad
directive prop or container, a duplicate route, a page no navigation entry
reaches, an image with no alt text, a heading outline that skips a level, an
unknown code fence language, a search index over its size limit, and a text
token that misses the WCAG AA contrast ratio on a surface it renders on.

It also resolves every absolute `https://lennylabs.github.io/podium/...` URL in
`README.md` and `CONTRIBUTING.md` against the emitted routes, because a
trailing-slash URL does not match the published `<page>.html` output.

## Design

`design/README.md` holds the tokens, the type scale, the screen compositions,
and the interaction states. `design/podium-design-reference.html` holds four
mockups covering the landing page and a documentation page in a light and a dark
theme, and it is the source of truth for any value the README does not list.

Components read the CSS variables in `src/styles/tokens.css` and never inline a
color. The accent is a surface color carrying dark text, or text on the dark
ground; it never sets small text on a light background, which is why the light
theme carries a separate link tone.

The diagrams under `docs/assets/diagrams/` are written into the page rather than
loaded through an img tag, so they read the same tokens the page does and the
theme toggle drives them. Each one binds the tokens it uses to local `--dg-*`
properties with a fallback beside every reference, which is what a reader on
github.com sees. `.claude/rules/doc-diagram-style.md` holds the conventions.
