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

describe("shell layout", () => {
  it("gives the content column a zero minimum", () => {
    expect(styled("app-body").gridTemplateColumns).toBe("268px minmax(0, 1fr)");
  });

  it("lets the content element shrink below its content width", () => {
    expect(styled("content").minWidth).toBe("0");
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

  it("scrolls a wide table inside its own box", () => {
    const table = descendantStyle("prose", "table");
    expect(table.overflowX).toBe("auto");
    expect(table.display).toBe("block");
    expect(table.maxWidth).toBe("100%");
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

  // The rail states the same field again in its property table, above the
  // relation links, so an unclipped value there pushes the links off the
  // page even though the header beside it reads clipped.
  it("clips a rail property value to the same three lines", () => {
    const value = styled("property-value clamped");
    expect(value.getPropertyValue("-webkit-line-clamp")).toBe("3");
    expect(value.display).toBe("-webkit-box");
    expect(value.overflow).toBe("hidden");
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

  it("keeps the toggle and the state marker at their own width", () => {
    expect(rowChild("button", "tree-toggle").flex).toBe("0 0 auto");
    expect(rowChild("span", "label").flex).toBe("0 0 auto");
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
});

// The layer table's identifier cell holds the layer name and the marker
// qualifying it. A badge declares a trailing margin and no leading one, so the
// cell's own row supplies the space before the marker; without it the name and
// the marker touch.
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
// The column asks the table for nothing of its own, so the clip narrows with
// the viewport rather than pushing the columns beside it off the card.
describe("layer source cell", () => {
  it("clips a source detail line instead of breaking it mid-token", () => {
    const detail = styled("source-detail");
    expect(detail.whiteSpace).toBe("nowrap");
    expect(detail.overflow).toBe("hidden");
    expect(detail.textOverflow).toBe("ellipsis");
    expect(detail.overflowWrap).not.toBe("anywhere");
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
 * source cell, and returns the table, its header cells in column order, and
 * the source cell. */
function layerTable(): {
  table: HTMLTableElement;
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
  return { table, headers, cell };
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

  // A breakable value contributes nothing to the table's minimum, so without a
  // claim of its own the key column collapses toward one character per line
  // beside a value that asks for the whole table.
  it("keeps a share of the table for the key column", () => {
    expect(descendantStyle("property-table", "th").width).toBe("40%");
  });
});
