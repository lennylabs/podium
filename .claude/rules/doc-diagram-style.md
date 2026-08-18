# Diagram style

Project-wide style rules for SVG documentation diagrams. These rules apply to every `.svg` file in `docs/assets/diagrams/`, to the markdown that embeds them, and to the ASCII fallback blocks that accompany them.

The companion rules in `doc-style.md` apply to any prose that appears in or around a diagram (titles, captions, footers, and alt text).

## Top-level principle

Diagrams are flat, geometric, and readable at thumbnail size. They use straight lines, the documentation site's design tokens, and the site's typefaces. They convey one concept per diagram. If the diagram cannot be described in one declarative sentence in the page caption, it is doing too much.

## Colour comes from the design tokens

`site/src/styles/tokens.css` holds the site's colours. A diagram never names a hex value in its drawing. It binds each token it uses to a local `--dg-*` custom property in its `<style>` block, and every drawn element reads one of the `dg-` classes built on those properties.

Use this role-to-token mapping. The token name is the contract; its current value is whatever `tokens.css` says today.

| Role in the drawing | Token | Class |
|:--|:--|:--|
| Diagram ground | `--surface` | `.dg-bg` |
| Grouping frame that holds other boxes | `--surface-2` | `.dg-panel` |
| Default box fill | `--surface-3` | `.dg-card` |
| Second neutral box fill | `--chip` | `.dg-card-strong` |
| The box the diagram is about | `--accent-wash` | `.dg-card-primary` |
| Weak accent fill for pills and event payloads | `--accent-strip` | `.dg-chip`, `.dg-card-event` |
| Primary text, primary box outline, connector | `--ink` | `.dg-title`, `.dg-name`, `.dg-mono`, `.dg-line` |
| Secondary text, de-emphasized outline | `--meta` | `.dg-lead`, `.dg-sub`, `.dg-note`, `.dg-card-quiet` |
| Footer text | `--faint` | `.dg-foot` |
| Hairline, tree guide, divider, lifeline | `--border` | `.dg-guide`, `.dg-divider` |
| Accent outline and accent connector | `--accent-deep` | `.dg-card-primary`, `.dg-card-strong`, `.dg-line-accent`, `.dg-outline` |
| Accent-coloured text | `--link` | `.dg-label`, `.dg-mono-accent` |
| Text face | `--font-ui` | `.dg` on the root element |
| Code, paths, and identifiers | `--font-mono` | `.dg-mono`, `.dg-mono-accent`, `.dg-mono-quiet` |

The mapping carries these constraints.

- Accent-coloured **text** takes `--link`, never `--accent`. The accent is a surface colour carrying dark text. It does not set small text on light paper, which is why the light theme carries a separate `--link` tone.
- The token set carries one accent and no error colour. A category cannot be encoded in hue. An error state, an event payload, or a rejected row is drawn with the accent when it needs attention and with `--meta` when it is de-emphasized, and the label says which it is.

## Both themes, and both embeddings

The markdown embeds a diagram as an ordinary image: `![alt](../assets/diagrams/name.svg)`. The documentation site reads that reference and writes the SVG markup into the page, so the site's custom properties resolve inside the drawing and the theme toggle drives it. A reader on github.com gets the file itself, where no page CSS applies.

Both readings work because every token reference carries a fallback and the file switches on the operating system setting:

```css
:root {
  --dg-ink: var(--ink, #14130f);
}

@media (prefers-color-scheme: dark) {
  :root {
    --dg-ink: var(--ink, #eaeef7);
  }
}
```

Inlined, `--ink` is defined and the fallback never applies, so the toggle wins in both branches. Standing alone, `:root` is the `<svg>` element itself, the fallback applies, and the media query selects the theme.

Both branches must name the **same token** and differ only in the fallback. Naming `--accent-deep` in one branch and `--accent` in the other makes the inlined colour depend on the operating system, which is a bug.

The `<style>` block is byte-identical in every diagram. Copy it from an existing file rather than writing a new one, and when a role or a token changes, change all the files together:

```bash
grep -c "dg-card-primary" docs/assets/diagrams/*.svg   # every file, same count
```

Element ids are not shared. An id appears once per document, and several diagrams can land on one page, so suffix every marker id with the file stem: `dg-arrow-sync-watch`, `dg-arrow-accent-sync-watch`.

## Visual style

### Canvas

- The root element carries an explicit `viewBox` and `class="dg"`, and no `font-family` attribute.
- The first drawn element is `<rect class="dg-bg" width="W" height="H"/>`.
- Leave a 20–40 px outer margin on all sides.

### Lines and corners

- All lines are straight or orthogonal.
- Rounded corners: `rx=6` for narrow cards (40–60 px tall), and `rx=8` to `rx=10` for wider primary cards.
- Stroke widths come from the classes. Vary geometry rather than redefining a class.
- No SVG filters. The `feTurbulence` and `feDisplacementMap` wobble is retired, and a single purely vertical path inside a filter group has a degenerate bounding box that can drop the rendering entirely.

### Type

The scale lives in the shared style block:

| Role | Class | Size |
|:--|:--|:--|
| Diagram title | `.dg-title` | 22 px |
| Card title | `.dg-name` | 17 px |
| Subtitle under the title | `.dg-lead` | 15 px |
| Body line inside a card | `.dg-sub` | 14 px |
| Section label, tag | `.dg-label`, `.dg-label-quiet` | 13 px |
| Annotation beside an arrow | `.dg-note` | 13 px |
| Footer | `.dg-foot` | 13 px |
| Code and identifiers | `.dg-mono` | 13 px |

Pick one class per role and keep the same role at the same size across the diagram. When a row needs a different size, add a size modifier (`.dg-t-xs` through `.dg-t-3xl`) to the role class rather than declaring a new class.

## Arrows

### Markers

Use `markerUnits="userSpaceOnUse"` on every marker so the head size is proportional to the canvas rather than to the stroke width. Stroke-width scaling produces an arrowhead that engulfs the line on short spans.

```xml
<marker id="dg-arrow-<stem>" viewBox="0 0 12 12" refX="10" refY="6"
        markerWidth="10" markerHeight="10"
        markerUnits="userSpaceOnUse" orient="auto">
  <path class="dg-arrow-head" d="M 0 0 L 11 6 L 0 12 z"/>
</marker>
```

The head takes its fill from a class, so it tracks the theme with the line it terminates. An accent line uses `.dg-arrow-head-accent` on a second marker. A dark arrowhead on an accent line reads as a separate element rather than as the arrow's tip.

### Geometry

- Leave a 4–8 px gap between the path endpoint and the target box edge so the arrowhead terminates at the box rather than overlapping it.
- Arrows between rows of boxes end at the top edge of the target box. A floating arrowhead suggests the arrow leads nowhere.
- T-junctions: one source feeding several targets. The trunk path carries no `marker-end`, and only the branch paths carry arrowheads. A stray arrowhead in the middle of the trunk is the most common rendering bug.
- Sequence-diagram messages end 4–6 px before the target lifeline.

## Boxes and layout

- Group related boxes in a row with consistent height and corner radius. A row reads as one tier.
- Cards have 16–20 px of internal padding on the left. A section label sits 22–26 px from the top, and the card title 22–30 px below that.
- Multiple text lines inside a box sit 18–22 px apart.
- Sibling tiers all have outlined boxes or all do not. Mixing outlined cards with bare text in a parallel row reads as a missing outline rather than a deliberate choice.

## Content

### Example identifiers

Use the cryptography convention from `doc-style.md`: `alice`, `bob`, `carol`, and so on for human users, and `acme` (`acme.com`, `Acme Corp`) for the example tenant. Do not use the real names of project people, customers, or contributors.

### Names and references

- No spec section references (`§4.6`, `§13.5`) in diagram text. Diagrams are reused across the documentation and the spec, and section numbers age out.
- No historical content. Former names, deprecated synonyms, and rename notes stay out of the drawing.
- Non-exhaustive lists use `etc.` followed by a period. An exhaustive list uses a proper conjunction (`a, b, and c`).

### Text fit

Text must not overflow its containing box at the rendered size. The site's `--font-ui` is wider than a system sans at the same pixel size, so a line that fitted before a typeface change may not fit after one. When a string does not fit:

1. Add the next size modifier down.
2. Widen the box.
3. Split into two lines.
4. Rephrase to a shorter equivalent.

Apply one option per row, and do not mix sizes within a row.

### Captions and alt text

The markdown `![alt](svg)` alt text describes what the diagram shows in prose terms and follows `doc-style.md`.

## ASCII fallbacks

Every diagram has an HTML-comment ASCII fallback immediately after the image so machine consumers and screen-reader-friendly source readers can recover the structure when the SVG is not rendered.

### Hazards inside HTML comments

XML comments cannot contain the substring `--`. The most common trap is an ASCII arrow that contains `-->`: the first occurrence closes the comment early, and everything after it renders as visible content.

Replacements:

- `-->` between boxes becomes `==>` or `===>`.
- `--(label)-->` becomes `===(label)==>`.
- `--->|` and `<---` in sequence diagrams become `===>|` and `<===`.
- Avoid `--watch` and similar flag spellings inside comments. Write "the watch flag" or "the long-running form" instead.

The check is mechanical: every `-->` inside an HTML comment that has non-whitespace content on the same line after it is a bug.

### Structure

- Open with a short label: `ASCII fallback for the diagram above (<name>):`.
- Use `+`, `-`, `|`, backtick, and forward slash for the structural skeleton. Unicode box-drawing characters (`├`, `└`, `│`) are not portable across the renderers this project cares about, and they misalign in markdown previews.
- Match the SVG's identifiers (alice, acme) and field names.
- Mirror the SVG's structure faithfully. The ASCII is the same diagram in another medium.

## Where these rules apply

- All `.svg` files in `docs/assets/diagrams/`.
- All markdown that embeds a diagram via `![alt](...svg)`.
- ASCII fallback blocks adjacent to those images.
- The prose paragraphs immediately before and after the diagram, where the content rules from `doc-style.md` apply.

## How to apply when editing

1. Build the site and render the diagram in both themes. `cd site && npm run build` writes `dist/`, and serving it lets a headless browser screenshot the page with `data-theme="light"` and `data-theme="dark"` on the root element. Read the images; do not trust a string substitution.
2. Render the file on its own as well, with `--blink-settings=preferredColorScheme=1` and again with `=0`, which is what a reader on github.com gets.
3. Check for text that has become illegible against its ground, labels that overflow their boxes, and arrowheads or strokes that vanished.
4. Sweep the source for the known hazards:
   - `grep -n "#[0-9a-fA-F]\{3,6\}" <file>` — a hex value outside the token bindings in the `<style>` block.
   - `grep -n "—" <file>` — em-dashes in alt text or captions. Re-check that each is a genuine aside.
   - `grep -n " shape\b" <file>` — `shape` as a generic noun.
   - `grep -n "§\|spec/" <file>` — spec section references.
   - For HTML-commented ASCII: scan every line inside `<!-- ... -->` for `-->` followed by non-whitespace, and rewrite those arrows.
5. After a move or a restructure, re-render and re-scan. Visual regressions from layout shifts are common.

## Escape hatches

- A pictogram inside a diagram (a brand logo, a stylized icon) is exempt from the font and palette rules within its own bounding box.
- Diagrams imported from external standards (OAuth flow, MCP protocol) may preserve their canonical styling when the source is authoritative and the diagram is a faithful reproduction.
- A single em-dash is acceptable as a genuine aside in a diagram footer. Decorative em-dash beats are not.

## Maintenance

When a new visual failure surfaces in review, add a specific, actionable rule above. Keep the file actionable and do not let it grow into a style thesaurus. When the site gains a token that a diagram role needs, add the row to the role-to-token table and use the class name everywhere.
