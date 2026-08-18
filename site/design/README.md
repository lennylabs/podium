# Handoff: Podium website — landing page + documentation shell (light & dark)

## Overview

A marketing landing page and a documentation page template for **Podium**, an open source catalog for reusable AI agent artifacts (skills, agents, commands, rules, hooks, contexts, MCP server registrations) with tools that translate them into harness-specific formats. The current site is a stock Jekyll/Just-the-Docs theme; this design replaces it with an original identity that covers both the marketing entry point and the docs reading experience, in light and dark themes.

Two page types, two themes, four mockups total.

## About the design files

`podium-design-reference.html` in this folder is a **design reference created in HTML** — a static prototype showing intended look and layout, not production code to copy. Open it in a browser to inspect every value with dev tools.

The task is to **recreate these designs in the target codebase's environment** using its established patterns. For Podium that likely means a static site generator with a custom theme (the existing docs are Jekyll/Just-the-Docs; Astro Starlight or a custom Jekyll layout are both reasonable targets). If no environment is settled, pick the one that best fits a docs-heavy open source site and implement there. Do not ship the reference HTML.

There is no JavaScript in the reference beyond a CSS blink animation. All layout is inline-styled for inspection convenience; production should use the codebase's normal styling approach (CSS modules, utility classes, theme tokens, etc.).

## Fidelity

**High fidelity.** Colors, typography, spacing, and radii are final and should be reproduced exactly. Interaction states (hover, focus, copy-to-clipboard feedback, search overlay) are specified below in words but are *not* built in the reference — implement them per the descriptions.

---

## Design tokens

### Typography

| Role | Family | Notes |
| --- | --- | --- |
| UI + headings | **Space Grotesk** | weights 400, 500, 600, 700 |
| Code, labels, metadata | **JetBrains Mono** | weights 400, 500, 700 |
| Wordmark only | **Anton** | uppercase, `letter-spacing: .03em`, 22px in nav (19px in docs nav) |

Both text families are on Google Fonts. Anton is used *only* for the logotype — never for body or headings.

Type scale actually used:

| Element | Size / line-height / tracking / weight |
| --- | --- |
| Landing h1 | 64px / 1.01 / −0.042em / 700 |
| Landing subtitle | 19px / 1.5 / — / 400 |
| Section h2 ("What you get") | 30px / — / −0.025em / 600 |
| Feature card h3 | 17.5px / — / — / 600 |
| Feature card body | 14.5px / 1.55 |
| Docs h1 | 42px / 1.03 / −0.035em / 700 |
| Docs lede | 16.5px / 1.6 |
| Docs body | 15px / 1.6 |
| Docs step heading | 17px / — / −0.015em / 700, preceded by an 11.5px mono number chip |
| Sidebar nav | 13px / 1.95 |
| Sidebar section label | 10px mono 600, `letter-spacing: .1em` |
| On-this-page | 12.5px / 2.05 |
| Code blocks | 13.5–14px / 1.75–1.85 mono |
| Inline code | 14–14.5px mono, tinted background chip, 4px radius |
| Mono metadata / eyebrows | 11–12.5px, `letter-spacing: .08–.1em` |

### Color — light theme

| Token | Hex | Use |
| --- | --- | --- |
| `page` | `#fbf9f5` | page background |
| `surface` | `#ffffff` | cards, code blocks, nav bar (as a `#fff → #fdfdf8` vertical gradient) |
| `surface-2` | `#fdfdf8` | gradient end / subtle panel |
| `surface-3` | `#f5f3e9` | code block tab strip gradient end |
| `ink` | `#14130f` | headings, primary text, text on accent |
| `body` | `#33322a` | paragraph text |
| `secondary` | `#43413a` | docs body text |
| `meta` | `#63604f` | mono metadata, breadcrumbs |
| `faint` | `#6f6c5e` | `$` prompts, de-emphasized mono |
| `border` | `#d9d5c6` | card and panel borders |
| `border-2` | `#e4e0d2` | sidebar tree hairline |
| `border-3` | `#ddd9c9` | kbd chip border |
| `chip` | `#eae7d9` | inline code background, version pill |
| **`accent`** | **`#ffa424`** | highlighter, active nav rail, active tab underline, CTA button, highlighted feature card |
| `accent-deep` | `#f2861a` | logo middle bar, status dots, callout bar, accent gradient end |
| `accent-tint` | `#ffe0b8` | highlighted file paths inside terminal output |
| `accent-wash` | `#fff3e2` | active sidebar item background (fades to transparent) |
| `accent-strip` | `#ffeeda` | adapter strip background gradient start |
| `callout-border` | `#f5cd9b` | release-status callout border |
| **`link`** | **`#a8480a`** | link text (the accent itself never carries small text on light backgrounds) |

Gradients (light): hero wash `radial-gradient(900px 420px at 88% 8%, rgba(255,164,36,.15), transparent 62%)` over `linear-gradient(180deg,#fdfdf8,#fbf9f5 70%)`; terminal glow `linear-gradient(140deg, rgba(255,164,36,.5), rgba(255,164,36,0) 58%)` with `filter: blur(10px)` on a `-14px` inset behind the card; primary button `linear-gradient(180deg,#232219,#14130f)`; CTA band `linear-gradient(120deg,#14130f 0%,#231a12 55%,#3a2110 100%)`; highlighted feature card `linear-gradient(160deg,#ffa424,#f2861a)`.

### Color — dark theme

| Token | Hex | Use |
| --- | --- | --- |
| `page` | `#10131c` | page background (blue-black, not neutral) |
| `nav` | `#141824` | sidebar background |
| `surface` | `#171b28` | cards, code blocks |
| `raised` | `#1b2030` | code tab strip, terminal title bar |
| `raised-2` | `#1e2434` | tab strip gradient start |
| `raised-3` | `#212739` | inline code background, version pill |
| `border` | `#2b3145` | card and panel borders |
| `border-2` | `#3a4159` | control borders, badge outlines |
| `border-3` | `#333a50` | terminal card border |
| `text` | `#eaeef7` | headings, primary text |
| `body` | `#c4cbdb` | docs paragraph text |
| `code` | `#d9dfec` | code block text |
| `muted` | `#9aa3ba` | secondary UI text |
| `faint` | `#7a8399` | `$` prompts, de-emphasized mono |
| **`accent`** | **`#ffa424`** | same accent as light; carries text safely on dark |
| `accent-deep` | `#f2861a` | accent gradient end |
| `accent-soft` | `#ffcf7a` | second stop of the headline gradient text |
| `callout-border` | `#4a3a22` | release-status callout border |

Gradients (dark): hero wash `radial-gradient(820px 400px at 86% 4%, rgba(255,164,36,.07), transparent 60%)` over `linear-gradient(180deg,#141824,#10131c 65%)`; terminal glow `linear-gradient(140deg, rgba(255,164,36,.2), rgba(255,164,36,0) 60%)`, `blur(14px)`, `-16px` inset; primary button `linear-gradient(180deg,#ffb84d,#f59410)` with `#14130f` text and `box-shadow: 0 8px 22px -12px rgba(255,164,36,.6)`; headline accent `linear-gradient(90deg,#ffa424,#ffcf7a)` as `background-clip: text`; CTA band `linear-gradient(120deg,#1b2030 0%,#181c28 55%,#3a2410 100%)`.

### The accent rule (important)

The orange is a **surface** color carrying dark text, or **text on the dark ground**. It is never small text on a light background. Light mode therefore has a separate link tone, `#a8480a` (≈6.5:1 on `#fbf9f5`). Dark mode uses `#ffa424` directly for links. Keep this rule when adding new components.

### Radii, spacing, shadows

- Radii: cards and feature grid `13px`; code blocks and doc panels `11px`; terminal card `13px`; buttons and inputs `9px`; small chips and kbd `4–5px`; status pills `100px`; sidebar active rail `3px` left border.
- Page gutters: `40px` landing, `22px` docs top bar, `46px` docs article horizontal.
- Nav height: `66px` landing, `58px` docs.
- Grid gaps: hero columns `56px`; feature grid is a 3-column grid with a `1px` gap over a border-colored background (hairline effect), not per-card borders.
- Shadows: terminal card light `0 24px 50px -30px rgba(20,19,15,.5)`; dark `0 26px 54px -32px rgba(0,0,0,.9)`; light primary button `0 6px 16px -8px rgba(20,19,15,.6)`.

### Logo

Three rectangles on a 34×22 viewBox — a podium/rostrum read:

```
<svg viewBox="0 0 34 22">
  <rect x="0"  y="8"  width="10" height="14" fill="{ink | text}"/>
  <rect x="12" y="0"  width="10" height="22" fill="{accent-deep light | accent dark}"/>
  <rect x="24" y="12" width="10" height="10" fill="{ink | text}"/>
</svg>
```

Rendered 28×18 in the landing nav, 25×16 in the docs nav, beside the Anton uppercase wordmark "PODIUM" with an 11px gap. No other imagery is used anywhere in the design.

---

## Screens

### 1. Landing page (light: `8a`, dark: `8b`) — 1280px content width

**Purpose:** convert a developer arriving from GitHub or a link into someone who runs the install command or opens the quickstart.

Vertical order:

1. **Top bar** (66px, bottom border, subtle vertical gradient). Logo + wordmark at left. Right side: nav links `Docs · Reference · Deployment · RFCs · GitHub ↗` at 14px, then a mono version pill `v0.1.7` on the chip background.
2. **Hero** (2-column grid, `1fr 1fr`, 56px gap, `76px 40px 66px` padding, accent wash in the top right).
   - Left: status pill (`0.1.x — early release`, 6px accent dot with a soft ring, mono 12px, 100px radius, 1px border); h1 `One catalog. / Every harness.` with "Every harness." highlighted — light: `linear-gradient(180deg,transparent 62%,#ffa424 62%)` behind the text; dark: accent gradient as clipped text; subtitle 19px with `AI skills and other artifacts` given the same highlighter treatment at 60%; an install field (`$ brew install lennylabs/tap/podium` + divider + `copy`, mono 14.5px, bordered, 10px radius); a button row: primary `Quickstart`, secondary `Concepts` (bordered), tertiary `Fit & comparisons` (text only).
   - Right: terminal card — title bar with three 9px dots (first is accent, the other two neutral) and the working directory in mono, then a `podium init` / `podium sync` transcript. Materialized file paths are the accent-tinted (light) or accent-colored (dark) fragments. A blinking block cursor (8×16px, `blink 1.1s step-end infinite`, 50% duty) ends the transcript.
3. **Adapter strip** (14px vertical padding, hairline top and bottom, horizontal gradient from the accent strip tint to transparent at 45%): mono label `ADAPTERS` then Claude Code, Claude Desktop, Cursor, Codex, Gemini CLI, OpenCode, Pi, Hermes, `+ custom`.
4. **Feature section** (`64px 40px 72px`): heading `What you get` at 30px with a mono note `SERVER-ONLY FEATURES MARKED`; then a 3×2 grid, 1px gaps over the border color, 13px outer radius. Cards 01–06 are: Cross-harness delivery, Domains and subdomains, Selective materialization, **Layered composition** (the accent-filled card), Per-layer visibility, Progressive discovery. Cards 04 and 05 carry a mono `SERVER` badge; card 06 carries `MCP / SDK`. Each card: mono number, 17.5px title, 14.5px description.
5. **CTA band** (46px padding, dark gradient in both themes): `Two ways to run it` + a paragraph on filesystem vs registry server, and an accent button `Compare deployment setups`.
6. **Footer**: 13px, `MIT licensed · lennylabs/podium` at left, `Docs · Governance · Contributing · Changelog` at right.

Exact copy is in the reference file; it is drawn from the project's own README and docs and should not be rewritten without the maintainer's sign-off.

### 2. Documentation page (light: `8c`, dark: `8d`) — 1340px, three panes

**Purpose:** read a guide page, navigate the catalog of docs, and jump within the page.

- **Top bar** (58px): logo + wordmark; primary tabs `Docs · Reference · RFCs · Changelog` with the active tab underlined by a 3px accent bar; a 300px search field (magnifier glyph, placeholder "Search artifacts, pages, CLI flags", `⌘K` kbd chip) pushed right; version pill; theme toggle showing the *opposite* theme's name.
- **Grid**: `252px / 1fr / 214px`, minimum height 760px.
  - **Left sidebar** — grouped tree. Group labels are 10px mono uppercase with tracking; items sit in a 13px list indented 10px behind a hairline. The active item pulls left, gains a 3px accent left border and a horizontal accent wash fading to transparent, and goes 600 weight. Groups shown: Getting Started, Authoring, Consuming, Deployment, Reference.
  - **Article** — max-width 780px, `34px 46px 60px` padding. Breadcrumb in mono; h1; lede; a release-status callout (10px radius, tinted background, 4px accent bar at left, tinted border); then numbered steps. **Each step heading is an 11.5px mono number chip on the accent (dark text, 5px radius) followed by a 17px Space Grotesk title, sitting under an 18px-padded hairline rule.** Steps contain: a tabbed code block (Homebrew / Scoop / Binary / Source; the active tab is a raised panel with a 2px accent inset at the top), plain code blocks, a two-up `SKILL.md` / `ARTIFACT.md` comparison, and a final `podium sync` output block with the materialized path highlighted. The article ends with previous/next cards (`← PREVIOUS Why Podium`, `NEXT → Concepts`).
  - **Right rail** — `ON THIS PAGE` label, then 12.5px links at 2.05 line-height; the active heading is 600 weight with a 3px accent inset on its left edge. Below a hairline: `Edit this page on GitHub`, `Report an issue`, both in the link tone.

---

## Interactions & behavior

Not built in the reference — implement these:

- **Theme toggle** in the docs top bar switches light/dark. Persist the choice (localStorage) and respect `prefers-color-scheme` on first visit. The landing page should follow the same stored preference.
- **Copy button** on the install field and on every code block: copies to clipboard, swaps the label to `copied` for ~1.5s.
- **Install tabs** (Homebrew / Scoop / Binary / Source) switch the code block contents; remember the last choice across pages.
- **Search**: `⌘K` / `Ctrl+K` opens a search overlay; the field in the top bar is the same entry point. Client-side index over page titles, headings, and artifact IDs is enough at this size.
- **Sidebar**: groups collapse and expand; the current page's group is expanded on load. The active item state is the accent rail described above.
- **On-this-page** highlights the heading currently in view (IntersectionObserver), scroll-linked.
- **Hover states** (unspecified in the reference, so define them consistently): nav links and sidebar items go to full `ink`/`text`; bordered buttons darken their border one step; the primary button lifts its gradient ~4% lighter; cards in the feature grid raise their background to pure white (light) / `#1b2030` (dark). Transitions 120–160ms `ease-out`.
- **Focus**: every interactive element needs a visible focus ring — 2px `accent` outline with 2px offset works on both themes.
- **Motion**: the only ambient animation is the terminal cursor blink. Respect `prefers-reduced-motion` by freezing it.
- **Responsive** (not mocked): below ~1100px drop the docs right rail; below ~860px collapse the sidebar behind a menu button and stack the landing hero to one column, with the terminal card below the copy.

## State

- `theme`: `'light' | 'dark'`, persisted.
- `installChannel`: `'brew' | 'scoop' | 'binary' | 'source'`, persisted.
- `searchOpen`: boolean; `searchQuery`: string.
- `activeHeading`: string, derived from scroll position.
- `sidebarOpen` (small screens), `expandedGroups`: string[].
- No data fetching beyond the search index.

## Accessibility

- The accent is a surface color, or text on the dark ground — never small text on light. Use `#a8480a` for light-mode link text.
- Verified ratios: ink on light paper ≈ 17:1; light body text ≈ 9:1; light link ≈ 6.5:1; dark text on page ≈ 15:1; dark muted ≈ 6:1; ink on the accent ≈ 9:1.
- Don't rely on the accent alone to mark the active nav item — the weight change and the rail carry it too.

## Assets

None. The logo is the three-rectangle SVG above; there are no images, icon fonts, or illustrations. The two magnifier/search glyphs are inline SVG primitives (a circle plus a rotated rect). Fonts load from Google Fonts — self-host them if the site should work offline.

## Files

- `podium-design-reference.html` — all four mockups (landing light, landing dark, docs light, docs dark), stacked and labeled. This is the source of truth for any value not listed above.
