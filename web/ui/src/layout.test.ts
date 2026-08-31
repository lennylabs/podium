// The shell layout case set. A grid track written as `1fr` takes its
// min-content width as an automatic minimum, so a wide table or a long
// identifier grows the column past the viewport, scrolls the whole document
// sideways, and clips the card at the right edge. The cases pin the zero
// minimum on the columns that hold page content. jsdom performs no layout, so
// what a case asserts is the declaration that reaches the element; the
// rendered result is checked against a browser at a narrow viewport.
//
// The file also holds the stylesheet-wide invariants that no single surface
// owns, because they are read out of the same injected stylesheet.

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

/** lastMediaRule returns the declarations of the last rule the stylesheet
 * writes for one selector under the given media condition. A media condition
 * adds no specificity, so where a breakpoint is declared in more than one
 * block it is the last of them that the browser applies. */
function lastMediaRule(condition: string, selector: string): string {
  let found = "";
  for (const sheet of Array.from(document.styleSheets)) {
    for (const rule of Array.from(sheet.cssRules)) {
      if (!(rule instanceof CSSMediaRule) || rule.conditionText !== condition) {
        continue;
      }
      for (const inner of Array.from(rule.cssRules)) {
        if (inner instanceof CSSStyleRule && inner.selectorText === selector) {
          found = inner.style.cssText;
        }
      }
    }
  }
  return found;
}

describe("shell layout", () => {
  it("gives the content column a zero minimum", () => {
    expect(styled("app-body").gridTemplateColumns).toBe("268px minmax(0, 1fr)");
  });

  // Neither the bar nor the sidebar held its place while the page scrolled, so
  // at the foot of a listing taller than the viewport the wordmark, the search
  // trigger, the appearance control and the Browse, Search and Layers rows had
  // all gone by, leaving nothing visible to navigate by. Both are stuck to the
  // viewport now, the sidebar under the 52px bar.
  // Spec: §13.10
  it("keeps the top bar on screen while the page scrolls", () => {
    const bar = styled("topbar");
    expect(bar.position).toBe("sticky");
    expect(bar.top).toBe("0px");
  });

  it("keeps the sidebar navigation on screen under the top bar", () => {
    const sidebar = styled("sidebar");
    expect(sidebar.position).toBe("sticky");
    expect(sidebar.top).toBe("52px");
    // A grid item stretched to the row's height has no room to move within its
    // container and never sticks, so the column starts its row and takes the
    // viewport height the bar leaves.
    expect(sidebar.alignSelf).toBe("start");
    expect(sidebar.height).toBe("calc(100vh - 52px)");
    // The stylesheet declares no global border box, so without this the
    // column's padding stands outside that height, overhangs the grid row, and
    // the sticky clamp drags the section rows under the bar at the foot of the
    // page.
    expect(sidebar.boxSizing).toBe("border-box");
  });

  // The column is bounded by the viewport, so a catalog longer than the space
  // under the section rows scrolls inside the tree rather than pushing the
  // pinned footer past the bottom of the column.
  it("scrolls a long catalog tree inside the bounded sidebar", () => {
    const tree = styled("catalog-tree");
    expect(tree.overflowY).toBe("auto");
    expect(tree.minHeight).toBe("0");
    expect(tree.flexGrow).toBe("1");
  });

  // Below the narrow breakpoint the sidebar is a band above the content, and a
  // sticky column of viewport height there would hold the whole screen.
  it("returns the sidebar band to the flow at a narrow viewport", () => {
    const sidebar = lastMediaRule("(max-width: 900px)", ".sidebar");
    expect(sidebar).toContain("position: static");
    expect(sidebar).toContain("height: auto");
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

  // The code panel the Authored source tab and the Frontmatter tab's raw view
  // share runs the width of the content column. The tab strip, the lede, and
  // the take-away controls above it all run that width, so a cap on the panel
  // would leave its right edge inside them and clip a long line while the
  // column beside it stayed empty.
  // Spec: §13.10
  it("runs the code panel the width of the content column", () => {
    // jsdom reports the empty string for a property no rule declares, which is
    // what an element that takes its column's width reports here.
    const pane = styled("source-pane").maxWidth;
    const actions = styled("source-actions").maxWidth;
    expect(pane).toBe("");
    expect(actions).toBe("");
    expect(styled("source-block").maxWidth).toBe(pane);
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

  // The chip fill and the 4px radius mark one token quoted inside a sentence.
  // A fenced block is quoted whole and the `pre` card is what marks it, so
  // the `code` a fence nests inside gives that treatment back. Left inherited,
  // each line paints its own band to the width of its text and the block reads
  // as ragged bars floating inside the card.
  it("draws a fenced block on the card's own ground", () => {
    const pre = document.createElement("pre");
    const code = document.createElement("code");
    pre.appendChild(code);
    document.body.appendChild(pre);
    mounted.push(pre);

    const fenced = window.getComputedStyle(code);
    expect(fenced.backgroundColor).toBe("rgba(0, 0, 0, 0)");
    expect(fenced.padding).toBe("0px");
    expect(fenced.borderRadius).toBe("0");
    expect(fenced.display).toBe("block");

    // The inline token keeps the chip it is marked with.
    const inline = document.createElement("code");
    document.body.appendChild(inline);
    mounted.push(inline);
    const quoted = window.getComputedStyle(inline);
    expect(quoted.background).toBe("var(--chip)");
    expect(quoted.padding).toBe("1px 5px");
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

  // The heading carries the primary ink and the prose under it carries the
  // secondary, which is the body-text tone every other reading surface here
  // uses. Without a colour of its own the body inherits the primary ink from
  // the page and a paragraph draws at the weight of its own heading.
  it("draws the body prose in the secondary ink under a primary-ink heading", () => {
    expect(descendantStyle("prose", "p").color).toBe("var(--sec)");
    expect(descendantStyle("prose", "li").color).toBe("var(--sec)");
    expect(descendantStyle("prose", "h2").color).not.toBe("var(--sec)");
  });

  // A full-width document column opens wider than the global leading.
  it("opens the body paragraph's leading past the global line-height", () => {
    expect(descendantStyle("prose", "p").lineHeight).toBe("1.65");
    expect(window.getComputedStyle(document.body).lineHeight).toBe("1.6");
  });

  // Left to the user agent a list carries a 40px indent and the global 1.6
  // leading, so a numbered procedure sits inset from the paragraph column
  // above it and reads tighter than the prose around it. The case pins the
  // 22px indent that keeps the marker in the prose column and the leading
  // the design pass fixes for a list.
  it("indents a body list into the prose column and opens its leading", () => {
    for (const tag of ["ol", "ul"]) {
      const list = descendantStyle("prose", tag);
      expect(list.paddingInlineStart).toBe("22px");
      expect(list.lineHeight).toBe("1.75");
      expect(list.marginBottom).toBe("16px");
    }
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

// The search filter row states what is narrowing the result set. Set at
// regular weight inside 2px of vertical padding, the applied filters sat
// shorter and lighter than the outlined controls beside them and read as
// annotations rather than as the accent chips carrying the query. The cases
// pin the padding every pill in the row shares and the weight the applied one
// adds; jsdom performs no layout, so the rendered row is checked in a browser.
describe("search filter pill", () => {
  it("gives every pill in the row the filter row's padding", () => {
    const style = styled("pill");

    expect(style.paddingTop).toBe("6px");
    expect(style.paddingBottom).toBe("6px");
    expect(style.paddingLeft).toBe("11px");
    expect(style.paddingRight).toBe("11px");
  });

  it("sets an applied filter in the weight that reads as an accent chip", () => {
    expect(styled("pill pill-active").fontWeight).toBe("600");
    expect(styled("pill").fontWeight).not.toBe("600");
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
    expect(label.display).toBe("flex");
    expect(label.minWidth).toBe("0");
    expect(label.overflow).toBe("hidden");
    expect(label.whiteSpace).toBe("nowrap");
  });

  // The label of a collapsed chain is a whole stretch of path, and a row that
  // clipped it as one string dropped its last segment, which is the only part
  // naming the domain the row opens: the rail rendered
  // `finance/accounting/payables/reconciliation/supplier/ledger` as
  // `finance/accounting/payabl…`. The ancestry is the box that yields.
  it("takes the row's shortfall out of the ancestry and keeps the name", () => {
    const lead = rowChild("span", "catalog-lead");
    const name = rowChild("span", "catalog-name");
    expect(Number.parseFloat(lead.flexShrink)).toBeGreaterThan(1);
    expect(lead.minWidth).toBe("0");
    expect(lead.overflow).toBe("hidden");
    expect(lead.textOverflow).toBe("ellipsis");
    expect(Number.parseFloat(name.flexShrink)).toBe(0);
    // A single segment wider than the sidebar is capped by the row rather than
    // drawn past its border.
    expect(name.maxWidth).toBe("100%");
    expect(name.overflow).toBe("hidden");
    expect(name.textOverflow).toBe("ellipsis");
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

  // Board 14a sets the row identifier at 600 13.5px mono and the description
  // under it at 13.5px, which is the interface default rather than the 15px
  // body prose the document inherits. Left without a size the description
  // inherited the body size and became the largest text in the row, larger
  // than the subdomain card description in the same page, so the row read
  // description-first. The case pins the identifier at or above the
  // description's size and both at the interface default.
  it("sets the identifier above the description in the row's type scale", () => {
    const row = document.createElement("li");
    row.className = "artifact-row";
    const id = document.createElement("a");
    id.className = "mono artifact-id";
    const description = document.createElement("p");
    description.className = "artifact-description";
    row.append(id, description);
    document.body.appendChild(row);
    mounted.push(row);

    const idSize = Number.parseFloat(window.getComputedStyle(id).fontSize);
    const descriptionSize = Number.parseFloat(
      window.getComputedStyle(description).fontSize,
    );
    expect(descriptionSize).toBe(13.5);
    expect(idSize).toBe(13.5);
    expect(idSize).toBeGreaterThanOrEqual(descriptionSize);
    expect(window.getComputedStyle(id).fontWeight).toBe("600");
    // The subdomain card description sits directly above the listing in the
    // same page, so the two descriptions read at one size.
    const subdomain = document.createElement("p");
    subdomain.className = "subdomain-description";
    document.body.appendChild(subdomain);
    mounted.push(subdomain);
    expect(Number.parseFloat(window.getComputedStyle(subdomain).fontSize)).toBe(
      descriptionSize,
    );
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

// The resource table's selected row is one tinted band with a single accent
// bar at its leading edge, per board 20f. Declared on every cell, the inset
// bar repeats at each column boundary and the row reads as one accent-outlined
// box per column.
describe("resource table selected row", () => {
  /** resourceRow attaches a resource table carrying a selected row and returns
   * that row's cells. */
  function resourceRow(): Element[] {
    const table = document.createElement("table");
    table.className = "data-table";
    const body = document.createElement("tbody");
    const row = document.createElement("tr");
    row.className = "row-selected";
    for (let i = 0; i < 5; i++) {
      row.appendChild(document.createElement("td"));
    }
    body.appendChild(row);
    table.appendChild(body);
    document.body.appendChild(table);
    mounted.push(table);
    return Array.from(row.children);
  }

  it("tints every cell so the wash runs unbroken across the row", () => {
    for (const cell of resourceRow()) {
      expect(declaredFor(cell, "background")).toBe("var(--wash)");
    }
  });

  it("draws the accent bar only at the row's leading edge", () => {
    const cells = resourceRow();
    expect(declaredFor(cells[0], "box-shadow")).toBe("inset 2px 0 0 var(--acc)");
    for (const cell of cells.slice(1)) {
      expect(declaredFor(cell, "box-shadow")).toBe("");
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

  // The version affordance in the artifact header is the one pressable thing
  // in a row of informational badges, so it takes the bordered control the
  // design draws: the page surface behind body ink inside a visible edge.
  // Given the soft badge tone it sat in the lightest fill and the quietest
  // text in that row, which read as one more piece of metadata.
  it("draws the version picker as a bordered control rather than a soft badge", () => {
    const picker = document.createElement("button");
    picker.className = "badge version-picker-open";
    document.body.appendChild(picker);
    mounted.push(picker);
    expect(declaredFor(picker, "background")).toBe("var(--surf)");
    expect(declaredFor(picker, "border-color")).toBe("var(--bd)");
    expect(declaredFor(picker, "color")).toBe("var(--ink)");
    const soft = document.createElement("span");
    soft.className = "badge badge-soft";
    document.body.appendChild(soft);
    mounted.push(soft);
    expect(declaredFor(picker, "background")).not.toBe(
      declaredFor(soft, "background"),
    );
    expect(declaredFor(picker, "border-color")).not.toBe(
      declaredFor(soft, "border-color"),
    );
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

// The identifier is the name of the row on the layer panel and on the restore
// table. Drawn at the plain mono weight it read as one more mono value beside
// the precedence line under it and the source path next to it, so it carries
// the weight an artifact row's identifier carries.
describe("layer name", () => {
  it("draws the identifier at the weight an artifact row's identifier carries", () => {
    expect(styled("layer-name").fontWeight).toBe("600");
    expect(styled("layer-name").fontWeight).toBe(styled("artifact-id").fontWeight);
  });
});

// The provenance rail's content-hash row holds a 71-character digest against
// a rail several times narrower. The row carries the whole value, so a reader
// checking it against a build or a signature can select it, copy it, or have
// it read out, and the rail clips it visually on the same terms as the layer
// table's source detail line.
describe("provenance content hash", () => {
  it("clips the digest in the container rather than in its text", () => {
    const row = styled("rail-hash");
    expect(row.display).toBe("flex");
    expect(row.whiteSpace).toBe("nowrap");
    expect(row.overflow).toBe("hidden");
    // The lead elides at its own end rather than pushing the row's controls
    // past the rail's edge.
    const lead = styled("rail-hash-lead");
    expect(lead.minWidth).toBe("0");
    expect(lead.textOverflow).toBe("ellipsis");
    const middle = styled("rail-hash-middle");
    expect(middle.overflow).toBe("hidden");
    expect(middle.textOverflow).toBe("ellipsis");
    expect(middle.minWidth).toBe("0");
    // The middle absorbs the whole clip before the trailing digest characters
    // give up a pixel, so the end the reader compares stays drawn.
    expect(Number(middle.flexShrink)).toBeGreaterThan(1000);
    const tail = styled("rail-hash-tail");
    expect(tail.getPropertyValue("flex")).toBe("0 0.0001 auto");
    expect(tail.textOverflow).toBe("ellipsis");
    // The trailing characters are the last run to give a pixel up. The rail
    // is narrower than the lead, the tail, and the row's controls together,
    // so a lead held rigid takes the whole width and leaves the tail a sliver
    // narrower than its own ellipsis: the digest then ends mid-character with
    // nothing saying it was cut. The lead shortens ahead of the tail instead,
    // and its own ellipsis marks the cut.
    expect(Number(lead.flexShrink)).toBeGreaterThan(Number(tail.flexShrink));
    expect(Number(middle.flexShrink)).toBeGreaterThan(Number(lead.flexShrink));
  });

  // The clip falls at the middle run's start, so the ellipsis stands against
  // the lead and the row reads as one elided digest. The element inside
  // restores the reading order, so the run's characters keep their own.
  it("elides the clipped run at its start", () => {
    expect(styled("rail-hash-middle").direction).toBe("rtl");
    expect(descendantStyle("rail-hash-middle", "bdi").direction).toBe("ltr");
  });

  // The row reserves the copy confirmation's place from the first render, so
  // the digest runs beside it are already collapsed by the time the copy
  // lands. Shrinkable, the confirmation was cut mid-word against the row's
  // clip and reported an outcome the reader could not read.
  it("holds the copy confirmation out of the row's clip", () => {
    const row = document.createElement("dd");
    row.className = "mono rail-hash";
    const confirmation = document.createElement("span");
    confirmation.className = "quiet copy-confirmation";
    row.appendChild(confirmation);
    document.body.appendChild(row);
    mounted.push(row);
    const button = document.createElement("button");
    row.appendChild(button);
    const style = window.getComputedStyle(confirmation);
    expect(style.getPropertyValue("flex")).toBe("0 0 auto");
    // The report is set at the button's size, because the width it holds is
    // width the digest gives up.
    expect(style.fontSize).toBe(window.getComputedStyle(button).fontSize);
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
    const tail = styled("source-detail-tail");
    expect(tail.getPropertyValue("flex")).toBe("0 1 auto");
    expect(tail.overflow).toBe("hidden");
    expect(tail.textOverflow).toBe("ellipsis");
    expect(tail.direction).toBe("rtl");
    expect(tail.minWidth).toBe("0");
    // The negative free space goes in proportion to the scaled shrink
    // factors, so the head's factor drives it to its reserve and freezes it
    // there before the tail gives up a pixel.
    expect(Number(head.flexShrink)).toBeGreaterThan(1000);
  });

  // The head stops shrinking while it can still draw its marker. Allowed to
  // shrink to nothing it was left a sliver a fraction of a character wide on
  // the rows whose path is most truncated, the browser painted no ellipsis at
  // that width, and the cell stated a bare final directory name that reads as
  // the layer's whole source path. The reserve is the marker and the
  // separator that closes the run, so what the head keeps reads as a
  // directory prefix. The component caps the reserve at the head's own
  // length, because a head shorter than the reserve drew the spare width as a
  // gap before the tail; two characters is what a head with directories of its
  // own holds, and the fallback covers a head the component leaves unmeasured.
  it("reserves the head enough width to draw its own ellipsis", () => {
    const head = styled("source-detail-head");
    expect(head.minWidth).toBe("var(--head-reserve, 2ch)");
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
  const labels = ["", "Layer", "Source", "Visibility", "Last ingest", ""];
  for (const [index, label] of labels.entries()) {
    const header = document.createElement("th");
    header.textContent = label;
    if (index === 0) {
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

  // The design draws the handle column at 34px across the whole column. A
  // table cell sizes content-box by default, so the shared cell padding sat
  // outside the declared width and the column came out at 58px.
  it("counts the cell padding inside the handle column's declared width", () => {
    const [handle] = layerTable().headers;
    const style = window.getComputedStyle(handle);
    expect(style.boxSizing).toBe("border-box");
    expect(style.width).toBe("34px");
    const padding =
      Number.parseFloat(style.paddingLeft) + Number.parseFloat(style.paddingRight);
    expect(padding).toBeLessThan(34);
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

  // An absolutely positioned descendant resolves against the nearest
  // positioned ancestor, so inside a static scroll container it is laid out
  // against the page and escapes the clip the overflow declares. The
  // assistive-only spans in the restore table sit past the table's floor, and
  // from outside the clip they set the document's scroll width wider than the
  // viewport, which scrolled the whole page sideways at 520px and carried the
  // fixed top bar and the sidebar out from under the content.
  it("contains what it scrolls rather than letting it reach the document", () => {
    expect(styled("table-scroll").position).toBe("relative");
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

  // The floor is wider than the container at an ordinary laptop viewport, so
  // the column that falls outside it first is the last one, and that column
  // carries the only route to reingest, edit, and unregister a layer: at a
  // 1000px viewport the whole actions cell was drawn past the container's
  // right edge with nothing on screen saying the table continued. The column
  // is pinned to the trailing edge of the scroll container, so it is drawn
  // inside the visible column at every scroll position. jsdom performs no
  // layout, so the cases pin the declarations; the drawn column is checked in
  // a browser.
  it("pins the actions column to the trailing edge of the scroll container", () => {
    const { body, headers } = layerTable();
    const row = document.createElement("tr");
    for (const className of ["drag-cell", "mono", "source-col", "", "mono", "row-actions"]) {
      const cell = document.createElement("td");
      cell.className = className;
      row.appendChild(cell);
    }
    body.appendChild(row);
    for (const cell of [headers[headers.length - 1], row.cells[5]]) {
      const style = window.getComputedStyle(cell);
      expect(style.position).toBe("sticky");
      expect(style.right).toBe("0px");
    }
  });

  // A pinned cell is painted over the cells that scroll beneath it, so while
  // the table is wider than its container the actions cell carries the ground
  // its row is drawn on and the rule that separates it from the column it
  // covers. Left transparent the scrolled source and visibility cells read
  // through the row's controls.
  it("gives the pinned actions cell its own ground while the table scrolls", () => {
    // The scroll container names itself so the cells inside it can ask how
    // wide it is.
    expect(ruleText(".table-scroll")).toContain("container: table-scroll / inline-size");
    const block = containerBlock("table-scroll", "860px");
    expect(block).toContain("border-left: 1px solid var(--b2)");
    expect(block).toContain("background-color: var(--surf)");
    expect(block).toContain("background-color: var(--chip)");
  });

  // The table's floor is 860px, so a container at least that wide holds every
  // column and the pinned cell covers nothing. Drawn there as well, the rule
  // ran the full height of a table that does not scroll and the actions cells
  // sat on a ground of their own, so the table read as boxed off one column
  // early. The design draws all rows on one grid separated by horizontal
  // hairlines.
  // Spec: §13.10
  it("draws no rule or ground on the actions column while the table fits", () => {
    const pinned = ruleText(".layer-table th:last-child, .layer-table td.row-actions");
    expect(pinned).toContain("position: sticky");
    expect(pinned).not.toContain("border-left");
    expect(ruleText(".layer-table td.row-actions")).toBe("");
    expect(
      ruleText(".layer-table tbody > tr:not(.row-detail):hover > td.row-actions"),
    ).toBe("");
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
  action: HTMLTableCellElement;
  row: HTMLTableRowElement;
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
    "",
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
  const action = document.createElement("td");
  action.appendChild(document.createElement("button"));
  row.appendChild(id);
  row.appendChild(source);
  row.appendChild(count);
  row.appendChild(action);
  body.appendChild(row);
  table.appendChild(body);
  document.body.appendChild(table);
  mounted.push(table);
  return { table, headers, source, id, count, action, row };
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

  // The design draws the handle column at 34px across the whole column. A
  // table cell sizes content-box by default, so the shared cell padding sat
  // outside the declared width and the column came out at 58px.
  it("counts the cell padding inside the handle column's declared width", () => {
    const [handle] = layerTable().headers;
    const style = window.getComputedStyle(handle);
    expect(style.boxSizing).toBe("border-box");
    expect(style.width).toBe("34px");
    const padding =
      Number.parseFloat(style.paddingLeft) + Number.parseFloat(style.paddingRight);
    expect(padding).toBeLessThan(34);
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

  // That floor is wider than the column the surface is laid out in at a 1280px
  // viewport, and the overflow falls on the action column, which carries the
  // row's only control. Left in the scroll the button was cut through its own
  // label, and scrolling far enough to read it took the layer identifier off
  // the left edge. The column is pinned to the container's right edge, and it
  // paints its own ground because the cells scrolling under it show through a
  // sticky cell that declares none.
  it("pins the action column to the container's right edge", () => {
    const { action, headers } = restoreTable();
    const cell = window.getComputedStyle(action);
    expect(cell.position).toBe("sticky");
    expect(cell.right).toBe("0px");
    expect(cell.backgroundColor).toBe("var(--surf)");
    const header = window.getComputedStyle(headers[headers.length - 1]);
    expect(header.position).toBe("sticky");
    expect(header.right).toBe("0px");
    expect(header.backgroundColor).toBe("var(--surf2)");
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
// minimum, so a value that cannot break sets the property table wider than the
// rail that holds it and scrolls the whole document sideways. The value breaks
// wherever it has to, which puts the table's minimum back at one character.
describe("frontmatter property table", () => {
  // A key is an identifier, and a break inside it renders a name the author
  // did not write: with `anywhere` the rail drew `review_cycle` as
  // `review_cycl` over a lone `e` while the value stood beside the first half.
  // `anywhere` also counts the break in the cell's min-content width, which is
  // what let the value's claim on the table hold the key at its floor with the
  // surplus spent elsewhere. `break-word` keeps the key whole in that width,
  // so the column sizes to the longest key the table carries, and it still
  // breaks a key too wide for the capped column.
  it("keeps a key whole in the column's own width", () => {
    const key = descendantStyle("property-table", "th");
    expect(key.overflowWrap).toBe("break-word");
    expect(key.overflowWrap).not.toBe("anywhere");
  });

  it("breaks a value longer than its column inside the cell", () => {
    expect(descendantStyle("property-table", "td").overflowWrap).toBe("anywhere");
  });

  // The key column now grows to its content, so an authored key long enough to
  // outrun the table would push it past the content column and scroll the
  // document sideways, which is the failure the break above covers. The cap
  // bounds the column at twice the design's key width in the panel and at the
  // rail's half of its narrower table.
  it("caps how wide a long key can push the key column", () => {
    expect(descendantStyle("property-table", "th").maxWidth).toBe("360px");
    const rail = railKeyStyle();
    const cap = Number.parseInt(rail.maxWidth, 10);
    expect(rail.maxWidth).toBe(`${cap}ch`);
    expect(cap).toBeGreaterThan(Number.parseInt(rail.minWidth, 10));
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
  // key column would leave the value less than a third of the row. The design
  // fixes the rail's key at the width of `sensitivity`, the longest key it
  // draws, and gives the value the rest. A wider floor spends the rail's
  // remaining width on the key: at 16ch the key cell measured 135px of a 271px
  // table and the value wrapped a description to one word a line before a
  // disclosure control clipped it. The floor is stated in `ch` of the key's own
  // mono face, so it tracks whatever size that face is set at, and a table
  // whose keys are all longer sizes the column to them instead.
  it("holds the rail's key column to the design's longest drawn key", () => {
    const key = railKeyStyle();
    const floor = Number.parseInt(key.minWidth, 10);
    expect(key.minWidth).toBe(`${floor}ch`);
    expect(floor).toBe("sensitivity".length);
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

/** containerBlock returns the text of the container query block whose prelude
 * names the given container and condition. jsdom evaluates no container
 * query, so a case pinning one reads the rules the condition holds. */
function containerBlock(name: string, condition: string): string {
  const blocks: string[] = [];
  for (const sheet of Array.from(document.styleSheets)) {
    for (const rule of Array.from(sheet.cssRules)) {
      const text = rule.cssText;
      const prelude = text.slice(0, text.indexOf("{"));
      if (
        prelude.startsWith("@container") &&
        prelude.includes(name) &&
        prelude.includes(condition)
      ) {
        blocks.push(text);
      }
    }
  }
  return blocks.join("\n");
}

/** scrollbarRule returns the text of the rule declaring the given scrollbar
 * part. The dialog body and the sideways-scrolling table container share one
 * declaration, so the part is looked up by the group the two are named in. */
function scrollbarRule(part: string): string {
  return ruleText(
    `.modal-body::-webkit-scrollbar${part}, .table-scroll::-webkit-scrollbar${part}`,
  );
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

  // A row carrying a message of no bounded length beside an identifier and a
  // code badge stacks instead: the message is a block of its own under them
  // rather than a flex item sized by what they leave. jsdom performs no
  // layout, so the case pins the declaration; the rendered row is checked
  // against a browser.
  it("gives a stacked row's message the full width of the card", () => {
    expect(styled("attention-row attention-stack").display).toBe("block");
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

  // The menu is one panel holding rows. Carrying the ordinary control tone
  // each item outlined itself in the fill the panel already carries, which
  // drew an outline inside an outline and a doubled rule between the items,
  // and the menu read as a stack of buttons in a box rather than as a menu.
  it("draws each item flat inside the panel", () => {
    const item = descendantStyle("row-menu", "button");
    expect(item.borderWidth).toBe("0px");
    expect(item.borderRadius).toBe("0");
    expect(item.backgroundColor).toBe("rgba(0, 0, 0, 0)");
  });

  // The rows are told apart by the panel's own hairline rather than by a
  // border each item draws for itself.
  it("separates the items with a single hairline", () => {
    expect(ruleText(".row-menu button + button")).toContain(
      "border-top: 1px solid var(--b2)",
    );
  });

  // A row runs the width of the panel, and the panel clips what overflows it,
  // so the shell's ring is cut off at the row's edges where it stands outside
  // the element.
  it("draws the focus ring inside the focused row", () => {
    expect(ruleText(".row-menu button:focus-visible")).toContain(
      "box-shadow: inset 0 0 0 2px var(--acc)",
    );
  });
});

// The action a refused write was attempted from carries the refusal with the
// row it belongs to. Left in the ordinary tone it is pixel-identical to every
// other row action in the stack, and which control produced the banner under
// the row can only be read off the banner's position.
describe("refused row action", () => {
  /** action attaches a row action button carrying the given classes and
   * returns it. The tone is declared on `button.action-refused`, so a div
   * matches neither the base control rule nor the refusal rule. */
  function action(className: string): HTMLElement {
    const button = document.createElement("button");
    button.className = className;
    document.body.appendChild(button);
    mounted.push(button);
    return button;
  }

  it("draws the attempted action in the danger tone", () => {
    const refused = action("action-refused");
    expect(declaredFor(refused, "border-color")).toBe("var(--danger-bd)");
    expect(declaredFor(refused, "background")).toBe("var(--danger-bg)");
    expect(declaredFor(refused, "color")).toBe("var(--danger)");
  });

  it("keeps it distinct from the untouched action beside it", () => {
    const plain = action("");
    expect(declaredFor(plain, "background")).toBe("var(--surf2)");
    expect(declaredFor(plain, "color")).toBe("var(--ink)");
  });

  // A read-only registry mutes every write control, and the muted tone is
  // declared after the base control rule. A refusal already drawn on a
  // control keeps its own tone over that.
  it("keeps the refusal tone on a control the registry has muted", () => {
    const refused = action("action-refused");
    refused.setAttribute("disabled", "");
    expect(declaredFor(refused, "color")).toBe("var(--danger)");
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

// The reingest report divides its body between a row of five stat cards. At
// the standard dialog width each card is about 118px, which is narrower than
// "LINT FAILURES" needs on one line, so that label broke in two and the row
// carried one two-line card beside four single-line ones. The wide modifier
// is the width the board draws that dialog at. jsdom performs no layout, so
// the case pins the declared width; the rendered row is checked in a browser.
describe("dialog width", () => {
  it("draws a standard dialog at the narrower of the two board widths", () => {
    expect(styled("modal").width).toBe("680px");
  });

  it("widens a dialog whose body carries a row of columns", () => {
    expect(styled("modal modal-wide").width).toBe("760px");
  });
});

// A dialog body taller than the viewport scrolls inside the dialog, which
// keeps the footer and its submit on screen. The Register form is taller than
// an ordinary laptop viewport, so the line stating the consequence of the
// visibility selection and the note on when visibility can be changed stand
// below the fold, and the platform's overlay scrollbar paints nothing until a
// scroll begins: a half-cut field row was the only sign that anything
// followed. Declaring the bar's parts opts the body onto a bar that is drawn
// for as long as the body overflows. jsdom computes no pseudo-element style,
// so the cases read the rules the browser applies; the drawn bar is checked
// in a browser.
describe("dialog body scroll", () => {
  it("draws a scrollbar on a dialog body rather than the overlay bar", () => {
    expect(scrollbarRule("")).toContain("width: 10px");
    expect(scrollbarRule("-thumb")).toContain("background: var(--bd)");
  });

  it("reserves the bar's gutter so an overflowing body does not reflow", () => {
    expect(ruleText(".modal-body")).toContain("scrollbar-gutter: stable");
  });

  // Either standard property takes precedence over the parts above and
  // selects the overlay bar again, which is the bar the parts exist to leave.
  it("leaves the standard scrollbar properties off the dialog body", () => {
    expect(ruleText(".modal-body")).not.toContain("scrollbar-width");
    expect(ruleText(".modal-body")).not.toContain("scrollbar-color");
  });
});

// A table that keeps its designed column widths below its floor is clipped at
// its container's edge with the table's own right border drawn there, so the
// last column still on screen reads as the table's last column. On the layer
// panel at a 1000px viewport the source and the visibility columns were cut
// and the panel a refusal opens ran past the edge with its message and its
// Dismiss outside it, and the overlay bar painted nothing until a scroll was
// already in progress, so nothing marked the clipping. The container takes the
// same bar the dialog body does, on the other axis.
describe("table scroll mark", () => {
  it("draws a scrollbar on a sideways-scrolling table rather than the overlay bar", () => {
    expect(scrollbarRule("")).toContain(".table-scroll::-webkit-scrollbar");
    expect(scrollbarRule("")).toContain("height: 10px");
    expect(scrollbarRule("-thumb")).toContain("background: var(--bd)");
  });

  // Either standard property takes precedence over the parts above and selects
  // the overlay bar again.
  it("leaves the standard scrollbar properties off the container", () => {
    expect(ruleText(".table-scroll")).not.toContain("scrollbar-width");
    expect(ruleText(".table-scroll")).not.toContain("scrollbar-color");
  });
});

// The sidebar is banded: a navigation block, the catalog tree, and the
// counts footer. The band boundaries are hairlines, and the catalog label
// once carried margin alone, so the tree floated under the navigation rows
// with no boundary while the footer below it drew one. jsdom performs no
// layout, so the cases pin the declarations that draw the two rules; the
// rendered sidebar is checked in a browser.
describe("sidebar bands", () => {
  /** band attaches an element carrying the given classes and returns it. */
  function band(className: string): HTMLElement {
    const element = document.createElement("div");
    element.className = className;
    document.body.appendChild(element);
    mounted.push(element);
    return element;
  }

  it("rules the catalog section off from the navigation block above it", () => {
    expect(declaredFor(band("catalog-label"), "border-top")).toBe(
      "1px solid var(--b2)",
    );
  });

  it("draws the catalog rule in the same hairline the footer draws", () => {
    expect(declaredFor(band("catalog-label"), "border-top")).toBe(
      declaredFor(band("sidebar-footer"), "border-top"),
    );
  });
});

// The copy confirmation reports an outcome beside the control that produced
// it. Drawn only after the copy lands, it took its width from the value in
// the row, which rewrapped and moved the acknowledgement and the Done button
// of the secret reveal down between the two clicks that flow asks for. The
// element is on the page from the first render and the copy reveals it, so
// the row's geometry is the same before and after. jsdom performs no layout,
// so the cases pin the declarations that hold the place; the rendered dialog
// is checked in a browser.
describe("copy confirmation", () => {
  /** confirmation attaches a copy confirmation, optionally in its copied
   * state, and returns it. */
  function confirmation(copied: boolean): HTMLElement {
    const element = document.createElement("span");
    element.className = "quiet copy-confirmation";
    if (copied) element.setAttribute("data-copied", "");
    document.body.appendChild(element);
    mounted.push(element);
    return element;
  }

  it("keeps the confirmation's place while hiding it", () => {
    expect(declaredFor(confirmation(false), "visibility")).toBe("hidden");
    expect(declaredFor(confirmation(false), "display")).toBe("");
  });

  it("reveals the same element once the copy lands", () => {
    expect(declaredFor(confirmation(true), "visibility")).toBe("visible");
  });
});

// The monospace surfaces are verbatim: the authored source tab shows the file
// byte for byte, the raw frontmatter view shows the block the registry serves,
// and an artifact ID, a version constraint, or a hash is a value a reader
// copies by eye. JetBrains Mono ligates operator sequences by default, which
// draws `>=` as `≥`, `!=` as `≠`, and `->` as `→`, so a reader was shown
// characters the author never wrote.
describe("mono surfaces", () => {
  /** styleRules returns every style rule the stylesheet declares, including
   * those nested inside a media condition. */
  function styleRules(): CSSStyleRule[] {
    const collected: CSSStyleRule[] = [];
    const visit = (rules: CSSRuleList) => {
      for (const rule of Array.from(rules)) {
        if (rule instanceof CSSStyleRule) {
          collected.push(rule);
        } else if (rule instanceof CSSMediaRule) {
          visit(rule.cssRules);
        }
      }
    };
    for (const sheet of Array.from(document.styleSheets)) {
      visit(sheet.cssRules);
    }
    return collected;
  }

  /** selectorsDeclaring returns the individual selectors of every rule writing
   * the given declaration, split out of each rule's selector list. */
  function selectorsDeclaring(property: string, value: string): Set<string> {
    const found = new Set<string>();
    for (const rule of styleRules()) {
      if (rule.style.getPropertyValue(property).trim() !== value) {
        continue;
      }
      for (const selector of rule.selectorText.split(",")) {
        found.add(selector.trim());
      }
    }
    return found;
  }

  it("renders the served characters rather than ligating an operator", () => {
    expect(descendantStyle("markdown", "code").fontVariantLigatures).toBe(
      "none",
    );
    expect(descendantStyle("markdown", "pre").fontVariantLigatures).toBe(
      "none",
    );
  });

  it("suppresses ligatures on every selector that takes the mono face", () => {
    const mono = selectorsDeclaring("font-family", "var(--font-mono)");
    const suppressed = selectorsDeclaring("font-variant-ligatures", "none");

    expect(mono.size).toBeGreaterThan(0);
    expect([...mono].filter((selector) => !suppressed.has(selector))).toEqual(
      [],
    );
  });
});
