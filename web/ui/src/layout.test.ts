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
