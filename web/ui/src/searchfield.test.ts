// The search-field case set. A field declared `type="search"` gets a native
// clear control from Chrome, drawn as a saturated blue cross that no token in
// the set carries, and it sits at the right edge of the field beside whatever
// the design put there. The cases pin the reset that removes it from every
// search field the UI draws.
//
// jsdom matches no pseudo-element, so a case matches the element part of the
// reset's selector and reads the declaration out of the rule's text.

import { afterEach, describe, expect, it } from "vitest";

import "./index.css";

const mounted: HTMLElement[] = [];

afterEach(() => {
  while (mounted.length > 0) {
    mounted.pop()?.remove();
  }
});

/** field attaches a search input carrying the given class and returns it. */
function field(className: string): HTMLInputElement {
  const element = document.createElement("input");
  element.type = "search";
  element.className = className;
  document.body.appendChild(element);
  mounted.push(element);
  return element;
}

/** cancelAppearance returns the `appearance` the stylesheet's last matching
 * rule gives the native clear control of element, or the empty string when no
 * rule reaches it. */
function cancelAppearance(element: HTMLElement): string {
  let value = "";
  for (const sheet of Array.from(document.styleSheets)) {
    for (const rule of Array.from(sheet.cssRules)) {
      if (!(rule instanceof CSSStyleRule)) {
        continue;
      }
      const [base, pseudo] = rule.selectorText.split("::");
      if (pseudo !== "-webkit-search-cancel-button") {
        continue;
      }
      if (!element.matches(base)) {
        continue;
      }
      const found = /[;{]\s*appearance:\s*([^;}]+)/.exec(rule.cssText);
      if (found !== null) {
        value = found[1].trim();
      }
    }
  }
  return value;
}

describe("search field", () => {
  it("removes the browser's clear control from the palette query", () => {
    expect(cancelAppearance(field("palette-input"))).toBe("none");
  });

  it("removes it from the search surface's query field", () => {
    expect(cancelAppearance(field("search-input"))).toBe("none");
  });

  it("removes it from a domain filter field", () => {
    expect(cancelAppearance(field("filter-field"))).toBe("none");
  });
});
