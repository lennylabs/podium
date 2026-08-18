# Handoff: Podium website — landing page + documentation shell (light & dark)

## Overview

A marketing landing page and a documentation page template for **Podium**, an open source catalog for reusable AI agent artifacts (skills, agents, commands, rules, hooks, contexts, MCP server registrations) with tools that translate them into harness-specific formats. The current site is a stock Jekyll/Just-the-Docs theme; this design replaces it with an original identity covering both the marketing entry point and the docs reading experience, in light and dark themes.

Two page types, two themes, at two breakpoints: four desktop mockups and five mobile mockups (390pt).

## About the design files

`podium-design-reference.html` is a **design reference created in HTML** — a static prototype showing intended look and layout, not production code to copy. Open it in a browser and inspect any value with dev tools.

Recreate these designs in the target codebase using its established patterns. For Podium that likely means a static site generator with a custom theme (the existing docs are Jekyll/Just-the-Docs; Astro Starlight or a custom Jekyll layout are both reasonable targets). Do not ship the reference HTML.

`logo/` holds the production mark as SVG (see *Assets*).

There is no JavaScript in the reference beyond a CSS blink animation. All layout is inline-styled for inspection convenience; production should use the codebase's normal styling approach.

## Fidelity

**High fidelity.** Colors, typography, spacing, radii, and the six feature diagrams are final and should be reproduced exactly. Interaction states (hover, focus, copy-to-clipboard, search overlay, theme toggle) are specified in words below and are *not* built in the reference.

---

## Design tokens

### Typography

| Role | Family | Notes |
| --- | --- | --- |
| UI + headings | **Space Grotesk** | weights 400, 500, 600, 700 |
| Code, labels, metadata | **JetBrains Mono** | weights 400, 500, 700 |
| Wordmark only | **Anton** | uppercase, `letter-spacing: .03em`, 22px landing nav / 19px docs nav |

Both text families are on Google Fonts. Anton is used *only* for the logotype.

| Element | Size / line-height / tracking / weight |
| --- | --- |
| Landing h1 | 64px / 1.01 / −0.042em / 700 |
| Landing subtitle | 19px / 1.5 |
| Section h2 (Features, Run it three ways, Server-side integrations) | 30px / — / −0.025em / 600 |
| Feature card h3 | 17.5px / 600 |
| Feature card body | 14.5px / 1.55 |
| "Run it three ways" card title | 19px / 600 / −0.015em |
| Deployment label / value | 12px mono `.06em` / 15px |
| Integrations pill | 13.5px / 500 |
| Docs h1 | 42px / 1.03 / −0.035em / 700 |
| Docs lede | 16.5px / 1.6 |
| Docs body | 15px / 1.6 |
| Docs step heading | 17px / 700 / −0.015em, preceded by an 11.5px mono number chip on the accent |
| Sidebar nav | 13px / 1.95; group labels 10px mono 600 `.1em` |
| On-this-page | 12.5px / 2.05 |
| Terminal + code blocks | 12.5px/1.7 (hero terminal), 14px/1.75 (docs) mono |
| Inline code | 14–14.5px mono on a tinted chip, 4px radius |

### Color — light theme

| Token | Hex | Use |
| --- | --- | --- |
| `page` | `#fbf9f5` | page background |
| `surface` | `#ffffff` | cards, code blocks, nav (as `#fff → #fbf9f5` gradient) |
| `surface-2` | `#fdfdf8` | gradient end / subtle panel |
| `surface-3` | `#f5f3e9` | tab strip gradient end |
| `ink` | `#14130f` | headings, primary text, text on accent |
| `body` | `#33322a` | paragraph text, terminal output paths |
| `secondary` | `#43413a` | docs body, feature card body |
| `meta` | `#63604f` | mono metadata, breadcrumbs, diagram muted strokes |
| `faint` | `#6f6c5e` | `$` prompts, de-emphasized mono |
| `border` | `#d9d5c6` | card and panel borders |
| `border-2` | `#e4e0d2` | inner hairlines |
| `border-3` | `#ddd9c9` | secondary button border |
| `chip` | `#eae7d9` | inline code, version pill |
| **`accent`** | **`#ffa424`** | highlighter, active nav rail, active tab, CTA, feature "on" states, integration pills |
| `accent-deep` | `#f2861a` | logo disc, accent gradient end |
| `accent-tint` | `#ffe0b8` | highlight fills |
| `accent-wash` | `#fff3e2` | active sidebar item, callout background |
| `accent-strip` | `#ffeeda` | adapter strip gradient start |
| `callout-border` | `#f5cd9b` | callout / pill borders |
| **`link`** | **`#a8480a`** | link text and terminal flags (accent never carries small text on light) |

Gradients: hero wash `radial-gradient(900px 420px at 88% 8%, rgba(255,164,36,.15), transparent 62%)` over `linear-gradient(180deg,#fdfdf8,#fbf9f5 70%)`; terminal glow `linear-gradient(140deg, rgba(255,164,36,.5), rgba(255,164,36,0) 58%)`, `blur(10px)`, `-14px` inset; primary button `linear-gradient(180deg,#232219,#14130f)`; adapter strip `linear-gradient(90deg,#ffeeda,#fbf9f5 45%)`; feature cards `linear-gradient(180deg,#fff,#fcfbf6)`.

### Color — dark theme

| Token | Hex | Use |
| --- | --- | --- |
| `page` | `#10131c` | page background (blue-black) |
| `nav` | `#141824` | sidebar background, nav gradient start |
| `surface` | `#171b28` | cards, code blocks |
| `raised` | `#1b2030` | terminal title bar, tab strip |
| `raised-2` | `#1e2434` | tab strip gradient start |
| `raised-3` | `#212739` | inline code, version pill |
| `border` | `#2b3145` | card and panel borders |
| `border-2` | `#3a4159` | control borders |
| `border-3` | `#333a50` | terminal card border |
| `text` | `#eaeef7` | headings, primary text, diagram ink |
| `body` | `#c4cbdb` | paragraph text, terminal paths |
| `muted` | `#9aa3ba` | secondary UI text, diagram muted strokes |
| `faint` | `#7a8399` | `$` prompts |
| **`accent`** | **`#ffa424`** | same accent; carries text safely on dark |
| `accent-soft` | `#ffcf7a` | second stop of the headline gradient |
| `callout-border` | `#4a3a22` | callout / pill borders |

Gradients: hero wash `radial-gradient(820px 400px at 86% 4%, rgba(255,164,36,.07), transparent 60%)` over `linear-gradient(180deg,#141824,#10131c 65%)`; terminal glow `rgba(255,164,36,.2)`, `blur(14px)`, `-16px` inset; primary button `linear-gradient(180deg,#ffb84d,#f59410)` with `#14130f` text and `0 8px 22px -12px rgba(255,164,36,.6)`; headline accent `linear-gradient(90deg,#ffa424,#ffcf7a)` via `background-clip: text`; pills `rgba(255,164,36,.12)` on `#4a3a22`.

### The accent rule

The orange is a **surface** color carrying dark text, or **text on the dark ground**. It is never small text on a light background — light mode uses `#a8480a` for link text and terminal flags. Keep this rule when adding components.

### Radii, spacing, shadows

- Radii: cards, feature grid, deployment grid `13px`; code blocks and doc panels `11px`; terminal `13px`; buttons, inputs, callouts `9–10px`; chips and kbd `4–5px`; pills and status `100px`; sidebar active rail `3px` left border.
- Gutters: `40px` landing, `22px` docs top bar, `46px` docs article.
- Nav height: `66px` landing, `58px` docs.
- Section rhythm: hero `76px 40px 66px`; Features `64px 40px 72px`; the two sections below it `0 40px 72px`.
- Hairline grids: feature/deployment grids are CSS grid with a `1px` gap over a border-colored background, not per-card borders.
- Shadows: terminal light `0 24px 50px -30px rgba(20,19,15,.5)`, dark `0 26px 54px -32px rgba(0,0,0,.9)`; light primary button `0 6px 16px -8px rgba(20,19,15,.6)`.

---

## Screens

### 1. Landing page (light `8a`, dark `8b`) — 1280px content width

1. **Top bar** — Beat mark + Anton wordmark "PODIUM"; right side `Docs`, `GitHub ↗` at 14px, then a mono `v0.1.7` pill.
2. **Hero** — 2-column grid, 56px gap, accent wash top right.
   - Left: status pill `0.1.x — early release` (6px accent dot with a soft ring); h1 `One catalog. / Every harness.` with "Every harness." highlighted (light: an accent band behind the text from 62% height; dark: accent gradient as clipped text); subtitle with `AI skills and other artifacts` given the same highlighter at 60%; an install field (`$ brew install lennylabs/tap/podium`, divider, `copy`); buttons `Quickstart` (primary), `Concepts` (bordered), `Fit & comparisons` (text).
   - Right: terminal card titled `~/projects/foo` (three 9px dots, first accent). Transcript shows three `podium sync` invocations — `--harness claude-code`, `--harness cursor`, `--config marketplace.yaml` — flags in the link tone, output paths indented two spaces in body color, and a final `$` prompt with a blinking 8×14px block cursor (`blink 1.1s step-end infinite`, 50% duty). **Emit each transcript line as its own block element**; do not rely on newlines inside `<pre>`.
3. **Adapter strip** — mono `ADAPTERS` label then Claude Code, Claude Desktop, Cursor, Codex, Gemini CLI, OpenCode, Pi, Hermes, `+ custom`, on a fading accent gradient with hairlines above and below.
4. **Features** — h2 `Features`, then a **2 × 3** hairline grid. Each card is a horizontal flex: **diagram at left (200px wide, flex none), text at right** with 22px gap. Text block is mono number, 17.5px title, 14.5px body. No badges, no highlighted card. Content, in order:

   | # | Title | Body |
   | --- | --- | --- |
   | 01 | Cross-harness delivery | Write canonical artifacts once. Automatically translate into the format your runtime expects. |
   | 02 | Domains and subdomains | Easy artifact maintenance and discoverability through the use of domains and sub-domains. |
   | 03 | Selective materialization | Sync a subset of the catalog into a workspace. Define profiles to quickly and seamlessly switch between scopes. |
   | 04 | Progressive discovery | Minimalistic set of tools for agents to traverse domains and find artifacts, materializing artifacts (including bundled files) lazily as they are needed. |
   | 05 | Layered composition | Compose the catalog from multiple independent sources with deterministic merge and explicit precedence. |
   | 06 | Access control | Declare who can see what: public, org-wide, scoped to OIDC groups, or specific users. |

   The six diagrams are inline SVG — copy them verbatim from the reference. Summary of each (see *Feature diagrams* below):
   1. a source document fanning out along three curves, arrowheads, into three boxes badged `Claude Code` (disc), `Cursor` (square), `Codex` (triangle);
   2. an indented domain tree: `catalog` → `Platform` → `ci` (nested) → `Analytics` → `Finance`;
   3. a dashed catalog of eight rows, two ticked in accent, arrow, two matching accent rows at right;
   4. a discovery walk — solid ink path with arrowheads through solid nodes, dotted branches to dashed unvisited nodes;
   5. three overlapping circles filled `ink @ 12%`, solid outlines outside the overlaps and **dotted arcs where they overlap** (via per-circle SVG masks), labelled team / org / personal;
   6. the same construction as 3, but the greyed rows carry lock glyphs and the unlocked pair is what travels to the right.

5. **Run it three ways** — h2, then a 3-column hairline grid. Each card: icon + title row, hairline, three label/value pairs, then a "plus" list with accent bullet dots.

   | | Local | Single node | Clustered |
   | --- | --- | --- | --- |
   | icon | folder outline | database cylinder | six-spoke asterisk |
   | Server-side deployment | None | One binary | Replicas, Postgres, storage |
   | Catalog source | A folder, read from disk | One or more folders or remote Git repos | One or more folders or remote Git repos |
   | Materialization | User-driven sync | User-driven sync, or agent-driven on demand | User-driven sync, or agent-driven on demand |
   | plus label | — | Everything in local, plus | Everything in single node, plus |
   | plus list | Author, lint, sync · Domains and profiles | Discovery via MCP or SDK · Hybrid search · Layers and visibility · One audit log | Multi-tenancy · SCIM group sync · Signing and transparency log · High availability |

   Below the grid, a tinted callout with an accent `→`: *The artifacts never change. They are independent of Podium's deployment model.*

6. **Server-side integrations** — h2, then a three-column table (`210px / 190px / 1fr`) with mono column headers `Out of the box` and `Compatible alternatives`, hairline row separators, and alternating row backgrounds. Alternatives are accent-tinted pills; some rows carry a muted note beneath.

   | Row | Icon | Out of the box | Alternatives | Note |
   | --- | --- | --- | --- | --- |
   | Metadata store | cylinder | SQLite | Postgres | — |
   | Object storage | cube | Local filesystem | S3 or S3-compatible storage | — |
   | Vector index | node triangle | sqlite-vec | pgvector, Pinecone, Weaviate Cloud, Qdrant Cloud | pgvector is the default when you already run Postgres. |
   | Embeddings | sparkle | BM25 only | OpenAI, Voyage, Cohere, Ollama | Or let Pinecone, Weaviate, or Qdrant embed on ingest. Vectors are fused with BM25 hits via reciprocal rank fusion. |
   | Identity | padlock | None | OIDC device code, Gateway-forwarded JWT, SCIM | — |
   | Layer sources | branch | Git and local paths | Custom via SPI | S3 buckets, OCI registries, HTTP archives. |

   Closing line, muted: *Nothing in the right column is required to start. At cluster scale, Postgres and object storage become requirements. Easily re-embed during vector backend migrations.*

7. **Footer** — `MIT licensed · lennylabs/podium` left, `Docs · Governance · Contributing · Changelog` right.

### 2. Documentation page (light `8c`, dark `8d`) — 1340px, three panes

- **Top bar** (58px): Beat mark + wordmark; tabs `Docs`, `Changelog` with a 3px accent underline on the active one; a 300px search field (magnifier, placeholder "Search artifacts, pages, CLI flags", `⌘K` chip); version pill; theme toggle showing the *opposite* theme's name.
- **Grid** `252px / 1fr / 214px`, min-height 760px.
  - **Sidebar** — groups Getting Started, Authoring, Consuming, Deployment, Reference. Group labels are 10px mono uppercase; items indent 10px behind a hairline. The active item pulls left, takes a 3px accent left border, an accent wash fading to transparent, and 600 weight.
  - **Article** — max-width 780px. Breadcrumb (mono), h1, lede, then a release-status callout (4px accent bar, tinted background). Steps 01–04 each use the number-chip heading over a hairline rule: *Install the CLI* (tabbed code block, active tab raised with a 2px accent inset), *Tell Podium where the catalog lives*, *Write your first skill* (two-up `SKILL.md` / `ARTIFACT.md`), *Materialize* (sync output with the materialized path highlighted). Ends with previous/next cards.
  - **Right rail** — `ON THIS PAGE`, active heading at 600 weight with a 3px accent inset; below a hairline, `Edit this page on GitHub` and `Report an issue` in the link tone.

---

## Mobile — 390pt (`13a`–`13e`)

Same tokens, same content, same diagrams. Only structure changes. Design width 390pt; everything below is a **single column with 18px side gutters**. All tap targets are at least 44 × 44pt (icon buttons use a 44pt box with negative margin so the glyph still aligns to the gutter).

### Landing (light `13a`, dark `13b`)

- **Top bar** 56pt: Beat mark (24px) + wordmark, version pill, hamburger. Desktop's inline nav links are gone — they live behind the menu.
- **Hero**: h1 drops to **40px / 1.03 / −0.035em**, subtitle to 16.5px. The same two highlighter treatments apply. The install field is full-width with the command `text-overflow: ellipsis`-clipped and `copy` pinned right. Buttons stack: `Quickstart` full width, then `Concepts` and `Comparisons` (shortened from "Fit & comparisons") side by side at 50%. Each is 14px vertical padding ⇒ ~47pt tall. Hero radial wash shrinks to `520px 300px at 88% 2%`.
- **Terminal**: full-bleed band (no card radius, no glow, no drop shadow — those read as noise at this width), hairline above and below. Type drops to **11px / 1.6**, the strip is `white-space: nowrap; overflow-x: auto`, and the title bar carries a mono `SWIPE →` affordance at its right. Same three-sync transcript, same blinking cursor. One line was dropped per block (the second and third `scripts/` paths) to keep the block from dominating the fold.
- **Adapters**: the horizontal strip becomes a wrapping set of pills (4px 9px, 100px radius, `border-2`), under a mono `ADAPTERS` label, on a vertical accent-tint gradient.
- **Features**: single column, hairline-separated (no outer card radius). Each card stacks **diagram above text**, diagram centered at 232px wide, 16px gap. Titles 17px, body 14.5px.
- **Run it three ways**: three separate rounded cards (13px radius, 1px border) at 12px vertical spacing inside the gutters, each keeping the icon+title row, hairline, label/value pairs and bulleted "plus" list. The arrow callout follows as its own tinted card.
- **Server-side integrations**: the table becomes one hairline-separated block per row — bold row name, then two label/value rows using a 96px mono label column (`OUT OF BOX`, `ALTERNATIVES`), pills wrapping in place, and the note beneath. The closing paragraph is reworded to "Nothing above is required to start…" since there is no right column on mobile.
- **Footer**: stacked — licence line, then links wrapping in a 14px-gap row.

### Docs (light `13c`, dark `13d`, drawer open `13e`)

- **Top bar** 56pt: hamburger (becomes an X when the drawer is open), mark + wordmark, version pill, search icon. The inline search field and the desktop theme toggle move into the drawer / overflow menu.
- **Article**: the three-pane grid collapses to the article alone. h1 32px, lede 15.5px, body 15px. The right rail (on-this-page) is dropped at this width — reinstate it as a collapsible "On this page" disclosure directly under the h1 if you want it back. Code blocks and the install tab strip both scroll horizontally rather than wrapping; the tab strip keeps its 2px accent inset on the active tab. Prev/next cards stack.
- **Nav drawer** (`13e`): full-width panel pushed under the top bar. Contains, in order — a 40pt search field, the section tabs as pills (`Docs` filled accent, `Changelog` outlined), then the full five-group tree with 44pt-tall rows. The active item keeps the desktop treatment: pulled left, 3px accent left border, accent wash fading right, 600 weight. Implement as an overlay panel (with a scrim over the page) or as a push panel; the mockup shows the push variant.

### Breakpoints

Two mockups, three states: mobile ≤ 720px, tablet 721–1099px (not mocked — use the mobile structure with the feature diagrams back to the left of their text, and the docs sidebar as a slide-over), desktop ≥ 1100px. The docs right rail appears at ≥ 1240px.

---

## Feature diagrams

Six inline SVGs, one per feature card, on a 240-unit-wide viewBox (96 tall, except #5 at 116) rendered at 200px wide on desktop and 232px on mobile. Two palettes only: `ink`/`text` for primary shapes, `meta`/`muted` for connectors and de-emphasized shapes, `accent` for "selected/visible" states. Rules they follow — keep these if you redraw anything:

- Every connector starts and ends **at a node's edge**, never inside it.
- Dashed strokes mean *not loaded* / *not permitted*; solid means *walked* / *permitted*.
- Accent appears only in 03 and 06, where it marks the selected artifacts.
- Diagram 05 uses one `<mask>` per circle (unique ids per theme — `lm0..2` light, `dm0..2` dark; the mobile copies in the reference are namespaced `moblm0..2` / `mobdm0..2`) so the overlapping arcs draw dotted and the outer arcs solid. **Mask ids must be unique per rendered instance** — generate them per component instance rather than hard-coding.

## Interactions & behavior

Not built in the reference:

- **Theme toggle** in the docs top bar; persist in localStorage, respect `prefers-color-scheme` on first visit, and have the landing page follow the same stored preference.
- **Copy button** on the install field and every code block: copies, swaps label to `copied` for ~1.5s.
- **Install tabs** (Homebrew / Scoop / Binary / Source) switch block contents; remember the choice across pages.
- **Search**: `⌘K` / `Ctrl+K` opens an overlay; the top-bar field is the same entry point. A client-side index over page titles, headings, and artifact IDs is enough at this size.
- **Sidebar** groups collapse/expand; the current page's group is expanded on load.
- **On-this-page** tracks the heading in view (IntersectionObserver).
- **Hover** (undefined in the reference — define consistently): nav and sidebar items to full ink/text; bordered buttons darken one step; the primary button lifts ~4%; feature cards raise to pure white (light) / `#1b2030` (dark). 120–160ms `ease-out`.
- **Focus**: visible ring on every interactive element — 2px accent outline, 2px offset, works on both themes.
- **Motion**: the terminal cursor blink is the only ambient animation; freeze it under `prefers-reduced-motion`.
- **Responsive**: the mobile layout is mocked — see *Mobile — 390pt* above. The menu button opens the nav drawer (`13e`); the search icon opens the same search overlay as `⌘K`.
- **Horizontal scroll regions** (mobile terminal, code blocks, install tabs) need momentum scrolling and hidden scrollbars, but must stay keyboard-reachable — give each a `tabindex="0"` and an accessible name.

## State

- `theme`: `'light' | 'dark'`, persisted.
- `installChannel`: `'brew' | 'scoop' | 'binary' | 'source'`, persisted.
- `searchOpen`, `searchQuery`.
- `activeHeading` (derived from scroll), `expandedGroups`, `sidebarOpen` (small screens).
- No data fetching beyond the search index.

## Accessibility

- Accent as surface or as text on dark only; `#a8480a` for light-mode link text.
- Ratios: ink on light paper ≈17:1; light body ≈9:1; light link ≈6.5:1; dark text on page ≈15:1; dark muted ≈6:1; ink on accent ≈9:1.
- Don't rely on accent alone for the active nav item — weight and the rail carry it too.
- Diagrams are decorative (`aria-hidden="true"`); the card text carries the meaning.

## Assets

`logo/` — the **Beat** mark: an accent disc held above a rule.

| File | Use |
| --- | --- |
| `podium-mark-light.svg` | On light backgrounds — rule `#14130f`, disc `#f2861a`. |
| `podium-mark-dark.svg` | On dark backgrounds — rule `#fbf9f5`, disc `#ffa424`. |
| `podium-mark-mono-ink.svg` / `-mono-paper.svg` | Single-colour, for print and reversing out of solids. |
| `podium-lockup-light.svg` / `-dark.svg` | Mark + wordmark, cap height matched to the mark (46 units = Anton at 63px). Wordmark is live text — outline it or keep Anton available. |
| `podium-tile.svg` | Square app icon / favicon. |

Mark geometry: viewBox `4 13 64 46`, disc r15 at (36,28), rule 64×5 at y54, 11-unit gap. Holds down to 16px; below 20px thicken the rule to 6 units. Rendered 28×20 in the landing nav, 25×18 in the docs nav.

No other imagery: no photography, illustration, or icon fonts. The nav search glyph, deployment icons, and integration icons are small inline SVG primitives — copy them from the reference. Fonts load from Google Fonts; self-host if the site must work offline.

## Files

- `podium-design-reference.html` — all nine mockups: the four desktop pages stacked, then the five mobile screens side by side. Source of truth for anything not listed above.
- `logo/` — production SVGs.
