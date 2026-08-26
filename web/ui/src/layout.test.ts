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

  it("gives the artifact viewer's prose column a zero minimum", () => {
    expect(styled("artifact-viewer").gridTemplateColumns).toBe(
      "minmax(0, 1fr) 316px",
    );
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
describe("artifact listing", () => {
  /** declaredFor returns the last value the stylesheet declares for the
   * property on a rule the element matches. jsdom drops a border shorthand
   * whose value carries a custom property, so the computed style reports no
   * border at all and a border is read from the rule instead. */
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

  it("clips an unknown type's field values on the same terms", () => {
    const value = descendantStyle("source-fields", "dd");
    expect(value.whiteSpace).toBe("nowrap");
    expect(value.overflow).toBe("hidden");
    expect(value.textOverflow).toBe("ellipsis");
  });
});
