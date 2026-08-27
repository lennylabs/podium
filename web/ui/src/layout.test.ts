// The shell layout case set. A grid track written as `1fr` takes its
// min-content width as an automatic minimum, so a wide table or a long
// identifier grows the column past the viewport, scrolls the whole document
// sideways, and clips the card at the right edge. The cases pin the zero
// minimum on the columns that hold page content. jsdom performs no layout, so
// what a case asserts is the declaration that reaches the element; the
// rendered result is checked against a browser at a narrow viewport.

import { afterEach, describe, expect, it } from "vitest";

import "./index.css";

const mounted: HTMLElement[] = [];

afterEach(() => {
  while (mounted.length > 0) {
    mounted.pop()?.remove();
  }
});

/** styled attaches an element carrying the given classes to the document and
 * returns the style the stylesheet computes for it. */
function styled(className: string): CSSStyleDeclaration {
  const element = document.createElement("div");
  element.className = className;
  document.body.appendChild(element);
  mounted.push(element);
  return window.getComputedStyle(element);
}

/** descendantStyle attaches an element of the given tag inside a container
 * carrying the given classes and returns the style the stylesheet computes
 * for the descendant. */
function descendantStyle(className: string, tag: string): CSSStyleDeclaration {
  const container = document.createElement("div");
  container.className = className;
  const child = document.createElement(tag);
  container.appendChild(child);
  document.body.appendChild(container);
  mounted.push(container);
  return window.getComputedStyle(child);
}

/** mediaBlock returns the text of every rule the stylesheet declares under the
 * given media condition. jsdom applies no media query to a computed style, so
 * a case pinning a breakpoint reads the rules the condition holds. */
function mediaBlock(condition: string): string {
  const rules: string[] = [];
  for (const sheet of Array.from(document.styleSheets)) {
    for (const rule of Array.from(sheet.cssRules)) {
      if (rule instanceof CSSMediaRule && rule.conditionText === condition) {
        rules.push(...Array.from(rule.cssRules, (inner) => inner.cssText));
      }
    }
  }
  return rules.join("\n");
}

/** mediaRule returns the declarations the stylesheet writes for one selector
 * under the given media condition, and the empty string when the condition
 * declares nothing for it. */
function mediaRule(condition: string, selector: string): string {
  for (const sheet of Array.from(document.styleSheets)) {
    for (const rule of Array.from(sheet.cssRules)) {
      if (!(rule instanceof CSSMediaRule) || rule.conditionText !== condition) {
        continue;
      }
      for (const inner of Array.from(rule.cssRules)) {
        if (inner instanceof CSSStyleRule && inner.selectorText === selector) {
          return inner.style.cssText;
        }
      }
    }
  }
  return "";
}

describe("shell layout", () => {
  it("gives the content column a zero minimum", () => {
    expect(styled("app-body").gridTemplateColumns).toBe("268px minmax(0, 1fr)");
  });

  it("lets the content element shrink below its content width", () => {
    expect(styled("content").minWidth).toBe("0");
  });

  // The sidebar is a fixed 268px, so under about 900px of viewport the column
  // beside it holds less than 600px and a surface's own header runs out of it:
  // the layer panel's actions were cut off at the right edge. Below that width
  // the shell is one column and the sidebar is a band above the content. jsdom
  // evaluates no media query, so the case reads the declaration out of the
  // stylesheet the browser applies at that width.
  it("drops the sidebar out of the grid below a narrow viewport", () => {
    const narrow = mediaBlock("(max-width: 900px)");
    expect(narrow).toContain(".app-body");
    expect(narrow).toContain("minmax(0, 1fr)");
    expect(narrow).not.toContain("268px");
  });

  // The top bar's items are about 790px wide together on one row, so a narrow
  // viewport carried the account cluster past the right edge: the appearance
  // control sat entirely off screen and was reachable only by scrolling the
  // document sideways, and the squeezed search trigger wrapped its label onto
  // the key hint. Below the shell's narrow breakpoint the bar wraps and the
  // search trigger takes the second row on its own.
  it("wraps the top bar so no control leaves a narrow viewport", () => {
    const bar = mediaRule("(max-width: 900px)", ".topbar");
    expect(bar).toContain("flex-wrap: wrap");
    expect(bar).toContain("height: auto");

    const trigger = mediaRule("(max-width: 900px)", ".search-trigger");
    expect(trigger).toContain("order: 1");
    expect(trigger).toContain("flex: 1 1 100%");
    expect(trigger).toContain("max-width: none");
  });

  // A flex item does not shrink below its min-content width on its own, so
  // the registry host held the top bar's first row open at its full width.
  it("truncates the registry host rather than holding the top bar open", () => {
    const host = styled("topbar-host");
    expect(host.minWidth).toBe("0");
    expect(host.overflow).toBe("hidden");
    expect(host.textOverflow).toBe("ellipsis");
  });

  it("wraps a panel head the column is too narrow to hold on one line", () => {
    expect(styled("panel-head").flexWrap).toBe("wrap");
  });

  // The domain title and its counts are one row, and it wraps so a long leaf
  // name does not push the counts off the content column.
  it("lays the domain title and its counts out on one wrapping row", () => {
    const head = styled("domain-head");
    expect(head.display).toBe("flex");
    expect(head.flexWrap).toBe("wrap");
    expect(descendantStyle("domain-head", "h1").marginTop).toBe("0px");
  });

  // A folded sparse chain gives a subdomain card a long slash-separated title,
  // and a slash offers no break opportunity. Without a zero minimum on the
  // title the card's grid track takes that string's width and the whole
  // document scrolls sideways.
  it("breaks a subdomain title the card is too narrow to hold", () => {
    const name = descendantStyle("subdomain-name", "span");
    expect(name.minWidth).toBe("0");
    expect(name.overflowWrap).toBe("anywhere");
  });

  it("gives the artifact viewer's prose column a zero minimum", () => {
    expect(styled("artifact-viewer").gridTemplateColumns).toBe(
      "minmax(0, 1fr) 316px",
    );
  });

  // The rail is a column of the page rather than a card, so it runs the whole
  // height of the content area: the viewer stretches to the area's height and
  // the content element drops the inset the other surfaces sit within, which
  // leaves the rail against the right edge with its divider on its left.
  it("runs the artifact rail the full height of the content area", () => {
    const container = document.createElement("div");
    container.className = "content";
    const viewer = document.createElement("section");
    viewer.className = "surface artifact-viewer";
    container.appendChild(viewer);
    document.body.appendChild(container);
    mounted.push(container);

    const content = window.getComputedStyle(container);
    expect(content.paddingTop).toBe("0px");
    expect(content.paddingRight).toBe("0px");
    expect(content.maxWidth).toBe("none");
    expect(window.getComputedStyle(viewer).minHeight).toBe("100%");
  });

  // The inset the content element gave up moves onto the viewer's own two
  // columns, so the prose and the rail keep their margins.
  it("moves the inset onto the artifact viewer's columns", () => {
    expect(styled("artifact-content").paddingLeft).toBe("30px");
    expect(styled("artifact-rail").paddingLeft).toBe("22px");
  });
});

// The artifact body is author-controlled markdown, and a token or a table
// wider than the prose column would otherwise grow the column and scroll the
// whole shell sideways, carrying the top bar and the sidebar off screen. The
// cases pin the declarations that keep wide content inside the column.
describe("rendered artifact body", () => {
  it("breaks a token the prose column is too narrow to hold", () => {
    const prose = styled("prose");
    expect(prose.overflowWrap).toBe("anywhere");
    expect(prose.minWidth).toBe("0");
  });

  // The rendering path wraps a body table in the scroll container, so the
  // container carries the scrolling and the table keeps its own semantics.
  it("scrolls a wide table inside the container it is wrapped in", () => {
    const prose = document.createElement("div");
    prose.className = "prose";
    const wrapper = document.createElement("div");
    wrapper.className = "table-scroll";
    wrapper.appendChild(document.createElement("table"));
    prose.appendChild(wrapper);
    document.body.appendChild(prose);
    mounted.push(prose);

    const scroller = window.getComputedStyle(wrapper);
    expect(scroller.overflowX).toBe("auto");
    expect(scroller.maxWidth).toBe("100%");
  });

  it("keeps a table cell's break from squeezing its column", () => {
    expect(descendantStyle("prose", "th").overflowWrap).toBe("break-word");
    expect(descendantStyle("prose", "td").overflowWrap).toBe("break-word");
  });

  it("scales an oversized image down to the prose column", () => {
    expect(descendantStyle("prose", "img").maxWidth).toBe("100%");
  });

  // The page title above the body is 29px/700. A body heading that inherits
  // the global scale draws an h1 at exactly that size and weight, so the
  // document's structure competes with the page's. The cases pin a scale
  // whose every level is lighter than the page title and no larger than the
  // 22px the top body level takes.
  it("sets a body heading below the page title in size and weight", () => {
    const title = descendantStyle("page-title", "h1");
    expect(title.fontSize).toBe("29px");

    for (const tag of ["h1", "h2", "h3", "h4", "h5", "h6"]) {
      const heading = descendantStyle("prose", tag);
      expect(heading.fontWeight).toBe("600");
      const size = Number.parseFloat(heading.fontSize);
      expect(size).toBeGreaterThan(0);
      expect(size).toBeLessThanOrEqual(22);
    }
  });

  // The levels step down, so a document that uses several of them reads as a
  // hierarchy rather than as one repeated size.
  it("steps the body heading levels down", () => {
    const sizes = ["h1", "h2", "h3", "h4"].map((tag) =>
      Number.parseFloat(descendantStyle("prose", tag).fontSize),
    );
    expect(sizes).toEqual([22, 20, 17, 15]);
  });

  // The body's leading heading sits directly under the tab strip, so it drops
  // the top margin the levels below it carry.
  it("drops the top margin on the body's leading heading", () => {
    const container = document.createElement("div");
    container.className = "prose";
    const first = document.createElement("h2");
    const second = document.createElement("h2");
    container.append(first, second);
    document.body.appendChild(container);
    mounted.push(container);

    expect(window.getComputedStyle(first).marginTop).toBe("0px");
    expect(window.getComputedStyle(second).marginTop).toBe("26px");
  });

  // Left at the user-agent default a blockquote draws at body ink with a 40px
  // symmetric indent and no rule, so a quoted aside reads as a paragraph the
  // author indented for no reason. The case pins the rule and the quieter ink
  // that mark it as a quotation.
  it("marks a body blockquote with a left rule and quieter ink", () => {
    const container = document.createElement("div");
    container.className = "prose";
    const quote = document.createElement("blockquote");
    container.appendChild(quote);
    document.body.appendChild(container);
    mounted.push(container);

    expect(declaredFor(quote, "border-left")).toBe("2px solid var(--b2)");
    expect(declaredFor(quote, "color")).toBe("var(--sec)");
    expect(declaredFor(quote, "padding")).toBe("4px 0 4px 16px");
    expect(declaredFor(quote, "margin")).toBe("16px 0");
  });
});

// A listing row carries an author-controlled description of no bounded
// length. Without a clip, one artifact whose description runs to several
// hundred words takes the height of a screen and pushes the rest of the
// listing below the fold. jsdom performs no layout, so the case pins the
// declarations that clip it; the rendered height is checked in a browser.
describe("artifact row description", () => {
  it("clips a row description to three lines over a 720px measure", () => {
    const description = styled("artifact-description");
    expect(description.getPropertyValue("-webkit-line-clamp")).toBe("3");
    expect(description.display).toBe("-webkit-box");
    expect(description.overflow).toBe("hidden");
    expect(description.maxWidth).toBe("720px");
  });

  // The artifact header states the same field above the version picker, the
  // tabs, and the body, so it reads at the same clip until the reader opens
  // it.
  it("clips the header description to the same three lines", () => {
    const lead = styled("lead clamped");
    expect(lead.getPropertyValue("-webkit-line-clamp")).toBe("3");
    expect(lead.display).toBe("-webkit-box");
    expect(lead.overflow).toBe("hidden");
    expect(lead.maxWidth).toBe("720px");
  });

  // The lead is the sentence a page states under its title: a domain's
  // description, an artifact's, and the layer panel's. Set at the primary ink
  // it carries the same weight as the heading above it and the body below it,
  // and the header block reads as one flat slab, so it takes the secondary
  // tone the row description beside it takes. jsdom resolves no custom
  // property, so the tone is pinned through the rules the sheet matches.
  it("sets the page lead in the secondary tone", () => {
    const lead = document.createElement("p");
    lead.className = "lead";
    document.body.appendChild(lead);
    mounted.push(lead);

    expect(declaredFor(lead, "color")).toBe("var(--sec)");
  });

  // The absent placeholder keeps the quiet tone the listing row and the
  // subdomain card state their own absence in.
  it("keeps a lead that states an absent description in the quiet tone", () => {
    const lead = document.createElement("p");
    lead.className = "lead quiet absent-description";
    document.body.appendChild(lead);
    mounted.push(lead);

    expect(declaredFor(lead, "color")).toBe("var(--meta)");
  });

  // The register dialog's consequence line names every granted member, so a
  // registration granting a couple of dozen users wrapped to eight lines,
  // pushed the neutral note and the dialog footer down, and made the body
  // scroll. The members are already listed as tokens above the line, so it
  // clips at two rows.
  it("clips the register consequence line to two lines", () => {
    const line = styled("consequence-text");
    expect(line.getPropertyValue("-webkit-line-clamp")).toBe("2");
    expect(line.display).toBe("-webkit-box");
    expect(line.overflow).toBe("hidden");
    // The clip sits on the text rather than on the padded box around it: a
    // clipped box shows a sliced third row inside its own bottom padding,
    // because overflow clips at the padding edge.
    const box = styled("consequence");
    expect(box.getPropertyValue("-webkit-line-clamp")).toBe("");
    expect(box.padding).toBe("12px 14px");
  });

  // The property table is rows of key and value. A value states what the
  // author wrote in full, so it takes none of the clip the header reads at:
  // a clipped cell hides part of the frontmatter and stands taller than the
  // rows around it.
  it("states a property value whole rather than clipping it", () => {
    const value = styled("property-value");
    expect(value.getPropertyValue("-webkit-line-clamp")).toBe("");
    expect(value.display).not.toBe("-webkit-box");
    expect(value.overflow).not.toBe("hidden");
    expect(value.getPropertyValue("overflow-wrap")).toBe("anywhere");
  });

  // A frontmatter value can be authored across several lines: a YAML block
  // scalar, or a nested mapping the table shows as its own source. Collapsing
  // its line breaks runs those lines into one and contradicts the panel's own
  // line saying the values are shown verbatim.
  it("keeps the line breaks of a value the author wrote across several lines", () => {
    const value = styled("property-value");
    expect(value.getPropertyValue("white-space")).toBe("pre-wrap");
  });
});

// A keyword is an identifier the API round-trips as a filter value, and the
// row already states its identifiers in the mono face at the quiet tone. Set
// in the body sans at body ink, a row of a dozen pills outweighed the mono
// path directly above it and read as the row's second most prominent element
// after the artifact name. jsdom resolves no custom property, so the tone and
// the face are pinned through the rules the sheet matches.
describe("artifact row keyword pill", () => {
  it("sets a keyword in the small quiet mono the identifiers around it use", () => {
    const pill = document.createElement("li");
    pill.className = "tag";
    document.body.appendChild(pill);
    mounted.push(pill);

    expect(window.getComputedStyle(pill).fontSize).toBe("10.5px");
    expect(declaredFor(pill, "font-family")).toBe("var(--font-mono)");
    expect(declaredFor(pill, "color")).toBe("var(--meta)");
  });
});

// A sidebar tree label is the whole folded stretch of path a §4.5.5 sparse
// chain collapsed into one entry, so it runs wider than the 268px sidebar. A
// wrapping row breaks the toggle away from the label, drops the label onto the
// next line at the sidebar's left padding, and cuts it mid-segment against the
// sidebar's border. The cases pin the declarations that keep the row on one
// line and take the shortfall out of the label; the rendered row is checked
// against a browser.
describe("sidebar tree row", () => {
  /** rowChild attaches an element carrying the given tag and classes inside a
   * tree row and returns the style the stylesheet computes for it. */
  function rowChild(tag: string, className: string): CSSStyleDeclaration {
    const row = document.createElement("div");
    row.className = "catalog-row";
    const child = document.createElement(tag);
    child.className = className;
    row.appendChild(child);
    document.body.appendChild(row);
    mounted.push(row);
    return window.getComputedStyle(child);
  }

  it("keeps the row on one line", () => {
    expect(styled("catalog-row").flexWrap).toBe("nowrap");
  });

  it("clips a label wider than the row to an ellipsis inside it", () => {
    const label = rowChild("a", "mono");
    expect(label.minWidth).toBe("0");
    expect(label.overflow).toBe("hidden");
    expect(label.textOverflow).toBe("ellipsis");
    expect(label.whiteSpace).toBe("nowrap");
  });

  it("keeps the toggle at its own width", () => {
    expect(rowChild("button", "tree-toggle").flex).toBe("0 0 auto");
  });

  // The marker annotating a row competes with the name for the same 268px.
  // Set in the section-label type it took enough of the row that
  // `legal/contracts` rendered as `legal/cont…` beside `NO SUBDOMAINS`, so it
  // is drawn in the smaller quiet type the design pass gives the slot.
  it("draws a row marker smaller and quieter than a section label", () => {
    const marker = rowChild("span", "catalog-marker");
    const label = rowChild("span", "label");
    expect(Number.parseFloat(marker.fontSize)).toBeLessThan(
      Number.parseFloat(label.fontSize),
    );
    expect(marker.textTransform).not.toBe("uppercase");
    expect(marker.letterSpacing).not.toBe(label.letterSpacing);
  });

  // The name is what the row is for, so the marker is the item the row takes
  // its shortfall out of.
  it("takes the row's shortfall out of the marker before the name", () => {
    const marker = rowChild("span", "catalog-marker");
    const name = rowChild("a", "mono");
    expect(Number.parseFloat(marker.flexShrink)).toBeGreaterThan(
      Number.parseFloat(name.flexShrink || "1"),
    );
    expect(marker.minWidth).toBe("0");
    expect(marker.overflow).toBe("hidden");
    expect(marker.textOverflow).toBe("ellipsis");
    expect(marker.whiteSpace).toBe("nowrap");
  });

  // The sidebar's anchor rule sets every link in the column at the primary
  // ink. Board 14a draws the tree as a quiet list with one strong entry, so a
  // tree left at that tone separates the current domain from its siblings by
  // weight alone. The case pins the two tones against each other.
  it("draws the tree quiet and the current row at the primary ink", () => {
    /** treeLabel attaches a tree row inside a sidebar and returns its name. */
    function treeLabel(tag: string, current: boolean): Element {
      const sidebar = document.createElement("div");
      sidebar.className = "sidebar";
      const row = document.createElement("div");
      row.className = current ? "catalog-row catalog-row-current" : "catalog-row";
      const name = document.createElement(tag);
      name.className = "mono";
      row.appendChild(name);
      sidebar.appendChild(row);
      document.body.appendChild(sidebar);
      mounted.push(sidebar);
      return name;
    }

    expect(declaredFor(treeLabel("a", false), "color")).toBe("var(--sec)");
    expect(declaredFor(treeLabel("span", false), "color")).toBe("var(--sec)");
    expect(declaredFor(treeLabel("a", true), "color")).toBe("var(--ink)");
    expect(declaredFor(treeLabel("span", true), "color")).toBe("var(--ink)");
  });

  // Board 14a draws a 27px tree row: a 13px name between 6px of padding on
  // each side. The sidebar's anchor rule sets every link in the column at
  // 13.5px inside 6px of its own padding, and a label left at that boxed the
  // row out to 38px on the page's 1.6 reading leading, which put the tree 40
  // percent past the fold it was drawn to fit inside. The case sums the row's
  // vertical box, because jsdom performs no layout; the rendered row is
  // checked against a browser.
  it("draws a row at the design's height", () => {
    const sidebar = document.createElement("div");
    sidebar.className = "sidebar";
    const node = document.createElement("li");
    node.className = "catalog-node";
    const row = document.createElement("div");
    row.className = "catalog-row";
    const name = document.createElement("a");
    name.className = "mono";
    row.appendChild(name);
    node.appendChild(row);
    sidebar.appendChild(node);
    document.body.appendChild(sidebar);
    mounted.push(sidebar);

    const rowStyle = window.getComputedStyle(row);
    const nameStyle = window.getComputedStyle(name);
    // The label carries neither a size nor a box of its own, so the row's
    // metrics are the row's. jsdom resolves neither the inherited size nor
    // the `inherit` keyword, so the size is read from the node that declares
    // it and the label's own declarations are read from the stylesheet.
    expect(declaredFor(name, "font-size")).toBe("inherit");
    expect(declaredFor(name, "padding")).toBe("0");
    const fontSize = Number.parseFloat(window.getComputedStyle(node).fontSize);
    expect(fontSize).toBe(13);

    /** leading resolves the row's line-height, which the stylesheet writes as
     * a unitless factor of the font size. */
    const declared = rowStyle.lineHeight;
    const leading = declared.endsWith("px")
      ? Number.parseFloat(declared)
      : Number.parseFloat(declared) * fontSize;

    const labelPadding = Number.parseFloat(nameStyle.paddingTop) || 0;
    const height =
      leading + 2 * Number.parseFloat(rowStyle.paddingTop) + 2 * labelPadding;
    expect(height).toBeGreaterThan(24);
    expect(height).toBeLessThanOrEqual(28);
  });
});

// A domain's artifacts and a search's results are one bordered container with
// a hairline between rows, per boards 14a, 14b, and 20a of the design pass. A
// border on each row instead draws the listing as a stack of loose boxes. The
// cases pin the border to the list and the divider to the rows after the
// first; the rendered listing is checked against a browser.
/** declaredFor returns the last value the stylesheet declares for the
 * property on a rule the element matches. jsdom drops a shorthand whose value
 * carries a custom property, so the computed style reports nothing at all and
 * the value is read from the rule instead. */
function declaredFor(element: Element, property: string): string {
  let value = "";
  for (const sheet of Array.from(document.styleSheets)) {
    for (const rule of Array.from(sheet.cssRules)) {
      const styleRule = rule as CSSStyleRule;
      if (typeof styleRule.selectorText !== "string") continue;
      if (!element.matches(styleRule.selectorText)) continue;
      const declared = styleRule.style.getPropertyValue(property);
      if (declared !== "") value = declared;
    }
  }
  return value;
}

describe("artifact listing", () => {
  /** listRows attaches a listing of the given row count and returns its
   * container and its rows. */
  function listRows(count: number): { list: HTMLElement; rows: Element[] } {
    const list = document.createElement("ul");
    list.className = "artifact-list";
    for (let i = 0; i < count; i++) {
      const row = document.createElement("li");
      row.className = "artifact-row";
      list.appendChild(row);
    }
    document.body.appendChild(list);
    mounted.push(list);
    return { list, rows: Array.from(list.children) };
  }

  it("draws one border around the whole listing", () => {
    const { list } = listRows(2);
    expect(declaredFor(list, "border")).toBe("1px solid var(--bd)");
    expect(declaredFor(list, "border-radius")).toBe("9px");
    expect(window.getComputedStyle(list).overflow).toBe("hidden");
  });

  it("gives a row no border and no gap of its own", () => {
    const { rows } = listRows(2);
    expect(declaredFor(rows[0], "border")).toBe("");
    expect(declaredFor(rows[0], "border-top")).toBe("");
    expect(window.getComputedStyle(rows[0]).marginBottom).toBe("");
  });

  it("separates the rows with a hairline instead of a gap", () => {
    const { rows } = listRows(3);
    for (const row of rows.slice(1)) {
      expect(declaredFor(row, "border")).toBe("");
      expect(declaredFor(row, "border-top")).toBe("1px solid var(--b2)");
    }
  });
});

// The listings are ul elements. The user-agent indent that a ul carries puts
// the content of a listing 40px right of the section label above it and runs
// the subdomain grid past the right edge of the content column, so board 14a's
// flush left edge holds only once each listing resets it.
describe("listing indent", () => {
  /** listing attaches a ul carrying the given class and returns it. */
  function listing(className: string): HTMLElement {
    const list = document.createElement("ul");
    list.className = className;
    document.body.appendChild(list);
    mounted.push(list);
    return list;
  }

  for (const className of ["artifact-list", "subdomain-grid", "relation-list"]) {
    it(`drops the user-agent list indent and marker on .${className}`, () => {
      const list = listing(className);
      expect(window.getComputedStyle(list).paddingLeft).toBe("0px");
      // jsdom does not expand the list-style shorthand, so the marker is read
      // from the rule rather than from the computed style.
      expect(declaredFor(list, "list-style")).toBe("none");
    });
  }
});

// The command palette's selection is moved by the arrow keys alone, so board
// 19a of the design pass marks the selected row with an accent bar at its
// leading edge over the wash tint, and sets every row's artifact name in the
// link colour. A tint on its own reads as the hover state a keyboard reader
// cannot produce.
// The palette's query line is the panel's header rather than a form field
// inside it. A bordered, filled box around the query, or a focus ring drawn
// around that box, fences off the one control the panel exists for and makes
// the panel read as a dialog holding a form. The row states itself with a
// divider beneath it and an accent caret in the query instead.
describe("command palette header", () => {
  /** paletteField attaches the palette's query row and returns it beside its
   * input. */
  function paletteField(): { field: Element; input: Element } {
    const field = document.createElement("div");
    field.className = "palette-field";
    const input = document.createElement("input");
    input.className = "palette-input";
    field.appendChild(input);
    document.body.appendChild(field);
    mounted.push(field);
    return { field, input };
  }

  it("draws the query line on the panel rather than in a box", () => {
    const { field } = paletteField();
    expect(declaredFor(field, "border")).toBe("");
    expect(declaredFor(field, "background")).toBe("");
    expect(declaredFor(field, "border-radius")).toBe("");
  });

  it("separates the query line from its results with a divider", () => {
    expect(declaredFor(paletteField().field, "border-bottom")).toBe(
      "1px solid var(--b2)",
    );
  });

  it("carries no focus ring on the query line or its field", () => {
    const { field, input } = paletteField();
    expect(declaredFor(field, "box-shadow")).toBe("");
    // The focus-within rule is a state no attached element matches, so it is
    // read from the sheet rather than from an element.
    expect(selectors()).not.toContain(".palette-field:focus-within");
    expect(declaredFor(input, "caret-color")).toBe("var(--acc)");
  });
});

// A native select takes its width from its widest option, so the scope filter
// stretched to the deepest domain path the catalog holds. The pill draws the
// closed label itself and takes the select out of flow, so the width comes
// from the label and the option list contributes none of it.
describe("filter dropdown", () => {
  /** filterSelect attaches the closed filter pill and returns it beside its
   * select. */
  function filterSelect(): { pill: HTMLElement; select: Element } {
    const pill = document.createElement("span");
    pill.className = "pill pill-select";
    const select = document.createElement("select");
    pill.appendChild(select);
    document.body.appendChild(pill);
    mounted.push(pill);
    return { pill, select };
  }

  it("takes the option list out of the pill's width", () => {
    const { pill, select } = filterSelect();
    expect(window.getComputedStyle(select).position).toBe("absolute");
    expect(window.getComputedStyle(select).opacity).toBe("0");
    expect(window.getComputedStyle(pill).position).toBe("relative");
  });

  it("carries the focus ring on the pill, because the select is transparent", () => {
    expect(selectors()).toContain(".pill-select:has(select:focus-visible)");
  });
});

/** selectors is every selector the stylesheet carries, which is how a case
 * asserts the absence of a rule for a state no attached element matches. */
function selectors(): string[] {
  const found: string[] = [];
  for (const sheet of Array.from(document.styleSheets)) {
    for (const rule of Array.from(sheet.cssRules)) {
      const styleRule = rule as CSSStyleRule;
      if (typeof styleRule.selectorText === "string") {
        found.push(styleRule.selectorText);
      }
    }
  }
  return found;
}

// The inline filter syntax is drawn as chips inside a box of its own, so each
// filter reads as a thing to type rather than as one line of prose about
// filtering.
describe("command palette filter syntax", () => {
  it("boxes the filter syntax off from the rest of the panel", () => {
    const box = document.createElement("div");
    box.className = "palette-syntax";
    document.body.appendChild(box);
    mounted.push(box);
    expect(declaredFor(box, "border")).toBe("1px solid var(--b2)");
    expect(window.getComputedStyle(box).display).toBe("flex");
    expect(window.getComputedStyle(box).flexWrap).toBe("wrap");
  });

  it("draws each filter as its own chip", () => {
    const chip = document.createElement("span");
    chip.className = "mono palette-syntax-chip";
    document.body.appendChild(chip);
    mounted.push(chip);
    expect(declaredFor(chip, "background")).toBe("var(--chip)");
    expect(declaredFor(chip, "border-radius")).toBe("5px");
  });
});

describe("command palette row", () => {
  /** paletteRow attaches a palette row carrying the given classes and returns
   * it beside its name element. */
  function paletteRow(className: string): { row: Element; name: Element } {
    const row = document.createElement("button");
    row.className = className;
    const name = document.createElement("span");
    name.className = "mono palette-row-name";
    row.appendChild(name);
    document.body.appendChild(row);
    mounted.push(row);
    return { row, name };
  }

  it("marks the selected row with an accent bar over the wash tint", () => {
    const { row } = paletteRow("palette-row palette-row-selected");
    expect(declaredFor(row, "background")).toBe("var(--wash)");
    expect(declaredFor(row, "box-shadow")).toBe("inset 2px 0 var(--acc)");
  });

  it("leaves an unselected row unmarked", () => {
    const { row } = paletteRow("palette-row");
    expect(declaredFor(row, "background")).toBe("transparent");
    expect(declaredFor(row, "box-shadow")).toBe("");
  });

  it("sets every row's artifact name in the link colour", () => {
    for (const className of ["palette-row", "palette-row palette-row-selected"]) {
      const { name } = paletteRow(className);
      expect(declaredFor(name, "color")).toBe("var(--link)");
    }
  });

  // A long leaf name used to hold the name column at its intrinsic width, which
  // left the path column zero pixels wide. A zero-width box renders no
  // ellipsis, so the row showed no location at all while the row above it did.
  // Both columns clip to an ellipsis and both keep a floor to clip inside.
  it("clips the name and the path to an ellipsis over a floor so every row states a location", () => {
    const { name } = paletteRow("palette-row");
    const path = document.createElement("span");
    path.className = "mono quiet palette-row-path";
    document.body.appendChild(path);
    mounted.push(path);

    for (const column of [name, path]) {
      expect(declaredFor(column, "flex")).toBe("");
      expect(declaredFor(column, "min-width")).toBe("9ch");
      expect(declaredFor(column, "overflow")).toBe("hidden");
      expect(declaredFor(column, "text-overflow")).toBe("ellipsis");
      expect(declaredFor(column, "white-space")).toBe("nowrap");
    }
  });
});

// The layer table's identifier cell holds the layer name and the marker
// qualifying it. A badge declares a trailing margin and no leading one, so the
// cell's own row supplies the space before the marker; without it the name and
// the marker touch.
// The badge carries two neutral weights. The outline tone is bordered, and
// the soft tone is a filled chip with no visible edge, which is how a
// secondary fact beside a badge reads as the lighter of the two. Given one
// treatment for both, every neutral badge in a row renders alike and the
// weight the design separates them by is lost.
describe("badge tones", () => {
  it("draws the soft tone as a filled chip with no visible edge", () => {
    const soft = styled("badge badge-soft");
    expect(soft.background).toBe("var(--chip)");
    expect(soft.borderTopColor).toBe("rgba(0, 0, 0, 0)");
  });

  it("keeps the soft tone distinct from the outlined badge beside it", () => {
    const outline = styled("badge");
    const soft = styled("badge badge-soft");
    expect(soft.background).not.toBe(outline.background);
    expect(soft.borderTopColor).not.toBe(outline.borderTopColor);
  });

  // A bare figure stated inside a control takes the count tone: a filled pill
  // with no edge, set tighter and heavier than the informational badges.
  // Drawn as the outlined badge, the layer panel's recoverable figure read as
  // a boxed input parked beside the link's label rather than as its count.
  it("draws the count tone as a filled pill with no edge", () => {
    const count = styled("badge badge-count");
    expect(count.background).toBe("var(--chip)");
    expect(count.borderTopColor).toBe("rgba(0, 0, 0, 0)");
    expect(count.borderRadius).toBe("100px");
    expect(count.fontWeight).toBe("600");
    expect(count.fontSize).toBe("10.5px");
    const outline = styled("badge");
    expect(count.borderRadius).not.toBe(outline.borderRadius);
    expect(count.fontSize).not.toBe(outline.fontSize);
  });
});

describe("layer identifier cell", () => {
  it("lays the layer name and its markers out on one wrapping row with a gap", () => {
    const cell = styled("layer-id-cell");
    expect(cell.display).toBe("flex");
    expect(cell.flexWrap).toBe("wrap");
    expect(cell.getPropertyValue("gap")).toBe("7px");
  });
});

// The layer table's source cell holds an absolute path or a repository URL,
// either of which can be several times the column's width. Wrapping one broke
// it between characters over three or four lines and left the rows of one
// table at unequal heights, so a detail line is clipped to one line instead.
// The clip falls on the leading directories, because the final segment is
// what tells two rows under one parent apart. The column asks the table for
// nothing of its own, so the clip narrows with the viewport rather than
// pushing the columns beside it off the card.
describe("layer source cell", () => {
  it("clips a source detail line instead of breaking it mid-token", () => {
    const detail = styled("source-detail");
    expect(detail.whiteSpace).toBe("nowrap");
    expect(detail.overflow).toBe("hidden");
    expect(detail.overflowWrap).not.toBe("anywhere");
  });

  // The head absorbs the whole clip while it has width to give, so the tail
  // is drawn complete on every line whose head still fits. The tail is not
  // held out of the shrink altogether: a final segment wider than the cell
  // then overflowed the line's clip and was sliced with no elision marker,
  // and the row stated a repository name that was not the layer's. It elides
  // at its own start once the head has collapsed.
  it("takes the clip out of the head and elides the tail only after it", () => {
    const detail = styled("source-detail");
    expect(detail.display).toBe("flex");
    const head = styled("source-detail-head");
    expect(head.overflow).toBe("hidden");
    expect(head.textOverflow).toBe("ellipsis");
    expect(head.minWidth).toBe("0");
    const tail = styled("source-detail-tail");
    expect(tail.getPropertyValue("flex")).toBe("0 1 auto");
    expect(tail.overflow).toBe("hidden");
    expect(tail.textOverflow).toBe("ellipsis");
    expect(tail.direction).toBe("rtl");
    expect(tail.minWidth).toBe("0");
    // The negative free space goes in proportion to the scaled shrink
    // factors, so the head's factor drives it to zero and freezes it there
    // before the tail gives up a pixel.
    expect(Number(head.flexShrink)).toBeGreaterThan(1000);
  });

  // The head keeps the box the flex layout gives it, so eliding it at its end
  // put the ellipsis wherever the last whole character fitted and left up to a
  // character of slack before the tail, which drew one path as two values with
  // a space between them. The elision falls at the head's start instead, where
  // what the head keeps ends flush against the tail.
  it("elides the head at its start so the line carries no gap", () => {
    expect(styled("source-detail-head").direction).toBe("rtl");
    expect(descendantStyle("source-detail-head", "bdi").direction).toBe("ltr");
  });

  // A tail wider than the cell on its own has nowhere to shrink to. The
  // automatic margin holds a line that fits against the left edge, and where
  // one does not fit the end alignment runs it off the left edge instead of
  // clipping the segment the reader is scanning for.
  it("runs a line the tail alone overflows off the left edge", () => {
    expect(styled("source-detail").justifyContent).toBe("flex-end");
    expect(styled("source-detail-tail").marginRight).toBe("auto");
  });

  it("lets the source column take the width the other columns leave", () => {
    const column = styled("source-col");
    expect(column.maxWidth).toBe("0");
    expect(column.width).toBe("100%");
  });

  it("drops that claim where the layer table fixes its own columns", () => {
    const { cell } = layerTable();
    expect(window.getComputedStyle(cell).width).toBe("auto");
    expect(window.getComputedStyle(cell).maxWidth).toBe("none");
  });

  it("clips an unknown type's field values on the same terms", () => {
    const value = descendantStyle("source-fields", "dd");
    expect(value.whiteSpace).toBe("nowrap");
    expect(value.overflow).toBe("hidden");
    expect(value.textOverflow).toBe("ellipsis");
  });
});

/** layerTable attaches the layer panel's table with one header row and one
 * source cell, and returns the table, its body, its header cells in column
 * order, and the source cell. */
function layerTable(): {
  table: HTMLTableElement;
  body: HTMLTableSectionElement;
  headers: HTMLTableCellElement[];
  cell: HTMLTableCellElement;
} {
  const table = document.createElement("table");
  table.className = "data-table layer-table";
  const head = document.createElement("thead");
  const headRow = document.createElement("tr");
  const headers: HTMLTableCellElement[] = [];
  for (const label of ["Move", "Layer", "Source", "Visibility", "Last ingest", ""]) {
    const header = document.createElement("th");
    header.textContent = label;
    if (label === "Move") {
      header.className = "drag-cell";
    }
    headRow.appendChild(header);
    headers.push(header);
  }
  head.appendChild(headRow);
  table.appendChild(head);
  const body = document.createElement("tbody");
  const row = document.createElement("tr");
  const cell = document.createElement("td");
  cell.className = "source-col";
  row.appendChild(cell);
  body.appendChild(row);
  table.appendChild(body);
  document.body.appendChild(table);
  mounted.push(table);
  return { table, body, headers, cell };
}

// The layer table's columns. The row is identified by its layer name, so that
// column is the widest content column in the table. Sized from its content the
// source column claimed every spare pixel and the identifier column collapsed
// to min-content, which wrapped an ordinary layer name over four lines.
describe("layer table columns", () => {
  it("lays the columns out to fixed proportions rather than to the content", () => {
    expect(window.getComputedStyle(layerTable().table).tableLayout).toBe("fixed");
  });

  it("gives the layer column more width than any other content column", () => {
    const [, layer, source, visibility, ingest] = layerTable().headers;
    const width = (header: HTMLTableCellElement) =>
      Number.parseFloat(window.getComputedStyle(header).width);
    expect(width(layer)).toBeGreaterThan(width(source));
    expect(width(layer)).toBeGreaterThan(width(visibility));
    expect(width(layer)).toBeGreaterThan(width(ingest));
  });

  it("breaks a name longer than its column inside the cell", () => {
    expect(styled("layer-id-cell").overflowWrap).toBe("anywhere");
  });

  // The proportions are read off the table's own width, so a container
  // narrower than the widths they add up to drives every column below the
  // min-content of the token inside it: at a 700px viewport the layer
  // identifier rendered one character to the line and at 900px the visibility
  // markers clipped part-way through "organization". The floor plus the
  // sideways scroll of the container keep every cell on one line at every
  // viewport.
  it("floors the table at the width its columns are drawn at", () => {
    const floor = Number.parseFloat(
      window.getComputedStyle(layerTable().table).minWidth,
    );
    expect(floor).toBeGreaterThanOrEqual(860);
  });

  it("scrolls the table sideways inside its own container", () => {
    expect(styled("table-scroll").overflowX).toBe("auto");
  });

  // A layer that matches on more than one visibility axis wraps its markers
  // over several lines and sets the row's height. Under the table default the
  // rest of the row stayed at the top of that height with an empty band below
  // it, so the row read as a short row with a stack of markers hanging off it
  // rather than as one tier.
  it("centres a body cell against the height the widest cell sets", () => {
    const { body } = layerTable();
    const row = document.createElement("tr");
    for (const className of ["drag-cell", "mono", "source-col", "", "mono", "row-actions"]) {
      const cell = document.createElement("td");
      cell.className = className;
      row.appendChild(cell);
    }
    body.appendChild(row);
    for (const cell of Array.from(row.cells)) {
      expect(window.getComputedStyle(cell).verticalAlign).toBe("middle");
    }
  });

  // The panel a row control opens is a full-width cell in a row of its own, so
  // it is laid out from its top edge.
  it("lays the detail row's panel out from the top", () => {
    const { body } = layerTable();
    const row = document.createElement("tr");
    row.className = "row-detail";
    const cell = document.createElement("td");
    cell.colSpan = 6;
    row.appendChild(cell);
    body.appendChild(row);
    expect(window.getComputedStyle(cell).verticalAlign).toBe("top");
  });
});

/** restoreTable attaches the recently-unregistered table with one header row,
 * one source cell, and one identifier cell, and returns the table, its header
 * cells in column order, and both cells. */
function restoreTable(): {
  table: HTMLTableElement;
  headers: HTMLTableCellElement[];
  source: HTMLTableCellElement;
  id: HTMLTableCellElement;
  count: HTMLTableCellElement;
} {
  const table = document.createElement("table");
  table.className = "data-table restore-table";
  const head = document.createElement("thead");
  const headRow = document.createElement("tr");
  const headers: HTMLTableCellElement[] = [];
  for (const label of [
    "Layer",
    "Source",
    "Artifacts",
    "Unregistered",
    "Erased on",
    "Actions",
  ]) {
    const header = document.createElement("th");
    header.textContent = label;
    headRow.appendChild(header);
    headers.push(header);
  }
  head.appendChild(headRow);
  table.appendChild(head);
  const body = document.createElement("tbody");
  const row = document.createElement("tr");
  const id = document.createElement("td");
  id.className = "mono";
  const source = document.createElement("td");
  source.className = "source-col";
  const count = document.createElement("td");
  count.className = "mono quiet";
  row.appendChild(id);
  row.appendChild(source);
  row.appendChild(count);
  body.appendChild(row);
  table.appendChild(body);
  document.body.appendChild(table);
  mounted.push(table);
  return { table, headers, source, id, count };
}

// The restore table's columns. The row is identified by its layer name, and
// sized from its content the source column claimed every spare pixel, which
// left the identifier column narrow enough to break `gamma-layer` mid-token
// over two lines and to wrap the erase countdown beside it.
describe("restore table columns", () => {
  it("lays the columns out to fixed proportions rather than to the content", () => {
    expect(window.getComputedStyle(restoreTable().table).tableLayout).toBe("fixed");
  });

  it("gives the layer column more width than any other content column", () => {
    const [layer, source, artifacts, unregistered, erased] =
      restoreTable().headers;
    const width = (header: HTMLTableCellElement) =>
      Number.parseFloat(window.getComputedStyle(header).width);
    expect(width(layer)).toBeGreaterThan(width(source));
    expect(width(layer)).toBeGreaterThan(width(artifacts));
    expect(width(layer)).toBeGreaterThan(width(unregistered));
    expect(width(layer)).toBeGreaterThan(width(erased));
  });

  it("drops the source cell's width claim where the table fixes its columns", () => {
    const { source } = restoreTable();
    expect(window.getComputedStyle(source).width).toBe("auto");
    expect(window.getComputedStyle(source).maxWidth).toBe("none");
  });

  it("breaks a name longer than its column inside the cell", () => {
    expect(window.getComputedStyle(restoreTable().id).overflowWrap).toBe("anywhere");
  });

  // The artifact-count cell states that the count is unreported, and that
  // marker is longer than the count it stands in for. Wrapped, it broke over
  // two lines and took the row's height with it.
  it("keeps the artifact-count cell on one line", () => {
    expect(window.getComputedStyle(restoreTable().count).whiteSpace).toBe("nowrap");
  });

  // The proportions are read off the table's own width, so a container
  // narrower than the widths they add up to drives every column below the
  // min-content of the label inside it: at a 1000px viewport the
  // "Unregistered" header ran out of its cell and abutted "Erased on", and the
  // header row read as one token. The floor plus the sideways scroll of the
  // container keep every header and every cell on one line at every viewport.
  it("floors the table at the width its columns are drawn at", () => {
    const floor = Number.parseFloat(
      window.getComputedStyle(restoreTable().table).minWidth,
    );
    expect(floor).toBeGreaterThanOrEqual(960);
  });
});

// The erase clock. The date, the depleting bar, and the count left are one
// row: drawn as a block under the date, the bar sat directly beneath it and
// read as an underline of the date rather than as a gauge of the window.
describe("erase clock", () => {
  it("lays the date, the bar, and the count out on one row", () => {
    const clock = styled("erase-clock");
    expect(clock.display).toBe("flex");
    expect(clock.alignItems).toBe("center");
  });

  it("keeps the depleting bar inline at its drawn width", () => {
    const bar = styled("depleting");
    expect(bar.flex).toBe("0 0 auto");
    expect(bar.width).toBe("54px");
    expect(bar.marginTop).not.toBe("4px");
  });
});

// Every data table separates its header row from the listing by drawing the
// header on the inset tone. The layer panel and the restore table took the
// tone from the at-scale table's column-label class, which they do not carry,
// so their headers painted the same fill as the rows under them.
describe("data table header row", () => {
  it("draws the layer panel's header row on the inset tone", () => {
    const { headers, table } = layerTable();
    const body = table.querySelector("td") as HTMLTableCellElement;
    expect(window.getComputedStyle(headers[1]).backgroundColor).toBe("var(--surf2)");
    expect(window.getComputedStyle(body).backgroundColor).not.toBe("var(--surf2)");
  });

  it("draws the restore table's header row on the inset tone", () => {
    const { headers, id } = restoreTable();
    expect(window.getComputedStyle(headers[0]).backgroundColor).toBe("var(--surf2)");
    expect(window.getComputedStyle(id).backgroundColor).not.toBe("var(--surf2)");
  });

  it("draws the at-scale artifact table's header row on the same tone", () => {
    const header = document.createElement("th");
    header.className = "column-label";
    const table = document.createElement("table");
    table.className = "data-table";
    const head = document.createElement("thead");
    const headRow = document.createElement("tr");
    headRow.appendChild(header);
    head.appendChild(headRow);
    table.appendChild(head);
    document.body.appendChild(table);
    mounted.push(table);
    expect(window.getComputedStyle(header).backgroundColor).toBe("var(--surf2)");
  });

  // A `th scope="row"` labels its own row from inside the body, so it belongs
  // to the listing and takes no header fill.
  it("leaves a row header inside the body on the listing tone", () => {
    const key = descendantStyle("data-table property-table", "th");
    expect(key.backgroundColor).not.toBe("var(--surf2)");
  });
});

// A frontmatter value is authored, so it can be one unbroken token such as a
// serialised nested map. A table takes its min-content width as an automatic
// minimum, so a cell that cannot break sets the property table wider than the
// rail that holds it and scrolls the whole document sideways. Both cells break
// wherever they have to, which puts the table's minimum back at one character.
describe("frontmatter property table", () => {
  it("breaks a key longer than its column inside the cell", () => {
    expect(descendantStyle("property-table", "th").overflowWrap).toBe("anywhere");
  });

  it("breaks a value longer than its column inside the cell", () => {
    expect(descendantStyle("property-table", "td").overflowWrap).toBe("anywhere");
  });

  // Automatic table layout hands a column whatever surplus the table has left,
  // so a key column stated as a share of the table grows with the table: the
  // frontmatter tab stands this table across the full main column, where the
  // share resolved to over 300px and stranded `type` and `version` a third of
  // the page from the value they label. The value asks for the whole table,
  // which sends the surplus there instead.
  it("sends the table's surplus width to the value column", () => {
    expect(descendantStyle("data-table property-table", "td").width).toBe("100%");
  });

  // The design fixes the key column at 180px. A preference would lose to the
  // value's claim on the whole table, so the width is stated as the floor the
  // value cannot squeeze past.
  it("holds the key column at the design's fixed width", () => {
    expect(descendantStyle("property-table", "th").minWidth).toBe("180px");
  });

  // The rail is a 316px column, so its table is about 270px wide and a 180px
  // key column would leave the value less than a third of the row. The rail's
  // own floor is stated in `ch` of the key's face, so it holds a key of
  // ordinary length on one line at whatever size that face is set at.
  it("narrows the key column to an ordinary key's width inside the rail", () => {
    const key = railKeyStyle();
    expect(key.minWidth).toBe("16ch");
    expect(Number.parseInt(key.minWidth, 10)).toBeGreaterThanOrEqual(
      "review_cadence".length,
    );
  });

  // The key labels the row and the value carries the content, so the key is
  // the quiet half. A user agent renders a `th` bold and in the body colour,
  // which reverses that and runs the reader's eye down the field names.
  it("sets the key quieter and smaller than the value beside it", () => {
    const key = descendantStyle("data-table property-table", "th");
    const value = descendantStyle("data-table property-table", "td");
    expect(key.fontWeight).toBe("400");
    expect(key.color).toBe("var(--faint)");
    expect(key.fontSize).toBe("11.5px");
    expect(value.fontSize).toBe("13.5px");
  });

  // The design draws the property table on one surface in both themes and
  // separates the pairs with the row rule alone. Filling every other row turns
  // the rail table and the frontmatter tab into a zebra-striped grid, which is
  // a treatment the design does not carry (§13.10).
  it("draws every row on the same surface", () => {
    const rows = propertyRows(4);
    const first = getComputedStyle(rows[0]).backgroundColor;
    for (const row of rows) {
      expect(getComputedStyle(row).backgroundColor).toBe(first);
      expect(getComputedStyle(row).backgroundColor).not.toBe("var(--surf2)");
    }
  });
});

/** railKeyStyle attaches a property table inside the artifact rail and returns
 * the computed style of its key cell, which is what a case reading the rail's
 * narrower floor needs. */
function railKeyStyle(): CSSStyleDeclaration {
  const rail = document.createElement("aside");
  rail.className = "artifact-rail";
  const table = document.createElement("table");
  table.className = "data-table property-table";
  const key = document.createElement("th");
  table.appendChild(key);
  rail.appendChild(table);
  document.body.appendChild(rail);
  mounted.push(rail);
  return window.getComputedStyle(key);
}

/** propertyRows attaches a property table holding count rows and returns them
 * in document order, which is what a case reading an `nth-child` rule needs. */
function propertyRows(count: number): HTMLTableRowElement[] {
  const table = document.createElement("table");
  table.className = "data-table property-table";
  const body = document.createElement("tbody");
  const rows: HTMLTableRowElement[] = [];
  for (let index = 0; index < count; index += 1) {
    const row = document.createElement("tr");
    body.appendChild(row);
    rows.push(row);
  }
  table.appendChild(body);
  document.body.appendChild(table);
  mounted.push(table);
  return rows;
}


// A subdomain card and a compact tile are each drawn as one target: a
// bordered box holding a name, a description and what stands under it, with a
// chevron stating that it opens. The anchor inside is the name alone, so the
// box is only aimable where the overlay covers it, and the box only
// establishes the containing block that overlay is measured against where it
// is positioned. Without both the reader who clicks the description or the
// count hits nothing.
describe("subdomain click target", () => {
  it("covers its positioned card with the name's overlay", () => {
    expect(ruleText(".stretched-link::after")).toContain("position: absolute");
    expect(ruleText(".stretched-link::after")).toContain("inset: 0");
  });

  it("positions the card and the tile the overlay is measured against", () => {
    expect(styled("subdomain").position).toBe("relative");
    expect(styled("tile").position).toBe("relative");
  });

  it("aims the pointer at the whole card and the whole tile", () => {
    expect(styled("subdomain").cursor).toBe("pointer");
    expect(styled("tile").cursor).toBe("pointer");
  });

  // The pointer anywhere over the box is over the anchor, so the name answers
  // for the box rather than for the line the pointer is on.
  it("underlines the name from a hover anywhere over the box", () => {
    expect(ruleText(".subdomain:hover .subdomain-name > span")).toContain(
      "underline",
    );
    expect(ruleText(".tile:hover .tile-name")).toContain("underline");
  });

  // The focus ring follows the hit area for the same reason: drawn around the
  // name alone it rings a fraction of what the keyboard is about to follow.
  it("rings the whole box while the link holds focus", () => {
    const ring = ruleText(
      '.subdomain:has(.stretched-link:focus-visible), .tile:has(.stretched-link:focus-visible)',
    );
    expect(ring).toContain("box-shadow");
    // The shell's own ring on the anchor is dropped, so the box carries one
    // ring rather than a ring inside a ring.
    expect(ruleText(".stretched-link:focus-visible")).toContain(
      "box-shadow: none",
    );
  });
});

/** ruleText returns the declarations the stylesheet holds for the given
 * selector. jsdom computes no pseudo-element style and matches no `:hover` or
 * `:focus-visible` state, so a case pinning one of those reads the rule the
 * browser applies instead of a computed value. */
function ruleText(selector: string): string {
  const normalize = (text: string) => text.replace(/\s+/g, " ").trim();
  for (const sheet of Array.from(document.styleSheets)) {
    for (const rule of Array.from(sheet.cssRules)) {
      if (
        rule instanceof CSSStyleRule &&
        normalize(rule.selectorText) === normalize(selector)
      ) {
        return rule.cssText;
      }
    }
  }
  return "";
}

// The reingest summary's refused row states a layer, the §6.10 code its
// refusal carried, and a message of no bounded length. The code is drawn as a
// badge, and under the flex container's default stretch that badge took the
// height of the wrapped message and rendered as an empty bordered rectangle
// with its text against the top edge. jsdom performs no layout, so the case
// pins the alignment that sizes each item to its own text; the rendered row is
// checked against a browser.
describe("attention row", () => {
  it("sizes each item in the row to its own text", () => {
    expect(styled("attention-row").alignItems).toBe("baseline");
  });

  // The identifier that leads the row is a flex item, and a flex item takes
  // min-content as its automatic minimum, so beside a long message it shrank
  // to the longest unbreakable run of the id and wrapped `hr-layer` at its
  // hyphen. The column keeps its own text width instead.
  it("keeps an identifier column at the width of its text", () => {
    expect(ruleText(".attention-id")).toContain("flex: none");
  });

  // A refusal message carries whatever the §6.10 envelope wrote, and a
  // filesystem path offers no break opportunity, so the row overflowed its
  // card and the tail of the message was clipped at the modal edge. The
  // message breaks inside such a run instead. jsdom performs no layout, so the
  // case pins the declaration; the rendered row is checked against a browser.
  it("breaks a message inside an unbreakable run rather than overflowing the card", () => {
    expect(styled("attention-text").overflowWrap).toBe("anywhere");
  });

  // The badge itself stays an inline box, so a badge sitting in a baseline-
  // aligned header keeps the alignment its container asks for.
  it("leaves the badge an inline box", () => {
    expect(styled("badge").display).toBe("inline-block");
    expect(styled("badge").alignSelf).toBe("");
  });
});

// A §4.5.5 lifted entry is not a child of the domain it is listed under, and
// the group that holds those entries carries that on its own container: a
// dashed box whose head names the group, with the rows inside it. Drawn with
// the solid border and the fill the direct listing takes, the group is
// indistinguishable from the domain's own artifacts. jsdom performs no layout,
// so the cases pin the declarations that reach the elements.
describe("lifted artifact group", () => {
  it("draws the group as a dashed container", () => {
    // The border is declared as a shorthand naming a token, which jsdom does
    // not expand into a computed longhand, so the case reads the rule.
    expect(ruleText(".folded")).toContain("border: 1px dashed var(--bd)");
    const group = styled("folded");
    expect(group.borderRadius).toBe("11px");
    expect(group.padding).toBe("14px 16px");
  });

  it("sits the group caption on the label's own line", () => {
    const head = styled("folded-head");
    expect(head.display).toBe("flex");
    expect(head.alignItems).toBe("baseline");
  });

  // The marker on a lifted row states provenance, and the tag pills two lines
  // below it state topics. Drawn as another filled chip the marker reads as one
  // more tag, so it takes the dashed edge the group's own container carries and
  // drops the fill.
  it("draws the lifted marker on a dashed edge with no fill", () => {
    const marker = styled("badge badge-folded");
    expect(marker.borderStyle).toBe("dashed");
    expect(ruleText(".badge-folded")).toContain("background: none");
    expect(styled("tag").borderStyle).not.toBe("dashed");
  });

  // The container is the group's border, so the listing inside gives its own
  // up and keeps a hairline over the first row as the head's divider.
  it("gives the listing inside the group up to the container", () => {
    const inner = ruleText(".folded > .artifact-list");
    expect(inner).toContain("border: 0");
    expect(inner).toContain("border-top: 1px solid var(--b2)");
    expect(inner).toContain("border-radius: 0");
  });
});

// The layer row's overflow menu. Drawn in the flow of the fixed-width actions
// cell it stretched its row to the height of the menu, emptied every other
// cell in that row over that height, and pushed every row below it down the
// page. The menu is drawn into the document instead and placed against its
// trigger in viewport coordinates, because the table scrolls sideways inside
// a container that clips what overflows it. jsdom performs no layout, so the
// case pins the declarations that take the menu out of the table; the
// rendered result is checked against a browser.
describe("layer row overflow menu", () => {
  it("takes the menu out of the table's flow and draws it over the rows", () => {
    const menu = styled("row-menu");
    expect(menu.position).toBe("fixed");
    expect(menu.zIndex).toBe("20");
  });
});

// Every dialog footer puts its controls at the trailing edge. The alignment
// came from the `flex: 1` on the footer's note, so a footer that carries no
// note drew Cancel and its primary against the leading edge instead. jsdom
// performs no layout, so the case pins the declaration on the footer itself;
// the rendered result is checked against a browser.
describe("dialog footer", () => {
  it("aligns the footer controls to the trailing edge without a note", () => {
    expect(styled("modal-foot").justifyContent).toBe("flex-end");
  });

  it("keeps the note leading the controls when the footer carries one", () => {
    expect(styled("modal-foot-note").flexGrow).toBe("1");
  });
});
