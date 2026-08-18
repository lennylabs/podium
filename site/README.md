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
  client/         the browser bundle
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

The diagrams under `docs/assets/diagrams/` keep their own palette and render on
a white card in both themes.
