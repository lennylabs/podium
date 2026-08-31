// The text-caret case set. The caret is one of the interaction tokens: every
// field the reader types into marks itself live with the accent caret. Left
// to the browser the caret takes the text colour, which gave the search
// surface's query an ink caret beside a command palette query drawing the
// accent one.
//
// jsdom resolves `caret-color` down to the declaration the stylesheet gives
// the element without substituting the custom property, so a case reads the
// token reference itself. A field no rule reaches resolves to `auto`, which
// is what an unstyled caret is.

import { afterEach, describe, expect, it } from "vitest";

import "./index.css";

const mounted: HTMLElement[] = [];

afterEach(() => {
  while (mounted.length > 0) {
    mounted.pop()?.remove();
  }
});

/** caret attaches an input of the given type carrying className and returns
 * the caret colour the stylesheet resolves for it. */
function caret(className: string, type = "search"): string {
  const element = document.createElement("input");
  element.type = type;
  element.className = className;
  document.body.appendChild(element);
  mounted.push(element);
  return getComputedStyle(element).caretColor;
}

describe("text caret", () => {
  it("draws the accent caret in the search surface's query field", () => {
    expect(caret("search-input")).toBe("var(--acc)");
  });

  it("draws the accent caret in the command palette's query field", () => {
    expect(caret("palette-input")).toBe("var(--acc)");
  });

  it("draws the accent caret in a text field on a form", () => {
    expect(caret("", "text")).toBe("var(--acc)");
  });

  it("leaves the caret of a checkbox, which draws none, alone", () => {
    expect(caret("", "checkbox")).toBe("auto");
  });
});
