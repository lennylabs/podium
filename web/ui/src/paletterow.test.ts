// The command palette's row geometry. The arrows move the selection down a
// list, so the selected row is drawn as the row it is: tinted across the whole
// panel behind an accent bar at its leading edge. Drawn instead with the
// button border and radius the base rule gives every button, and inset by a
// padded panel body, the selection reads as a box that shrinks out of the list
// rather than as a highlight that moved down it.
//
// jsdom performs no layout and does not resolve a custom property, so a case
// asserts the declaration that reaches the element: a literal length through
// the computed style, and a token-valued property through the rules the sheet
// matches.

import { afterEach, describe, expect, it } from "vitest";

import "./index.css";

const mounted: HTMLElement[] = [];

afterEach(() => {
  while (mounted.length > 0) {
    mounted.pop()?.remove();
  }
});

/** row attaches a palette row inside a palette body and returns it. */
function row(selected: boolean): HTMLElement {
  const body = document.createElement("div");
  body.className = "palette-body";
  const list = document.createElement("ul");
  list.className = "palette-rows";
  const item = document.createElement("li");
  const button = document.createElement("button");
  button.className = selected ? "palette-row palette-row-selected" : "palette-row";
  item.appendChild(button);
  list.appendChild(item);
  body.appendChild(list);
  document.body.appendChild(body);
  mounted.push(body);
  return button;
}

/** styled attaches an element carrying the given classes and returns the style
 * the stylesheet computes for it. */
function styled(tag: string, className: string): CSSStyleDeclaration {
  const element = document.createElement(tag);
  element.className = className;
  document.body.appendChild(element);
  mounted.push(element);
  return window.getComputedStyle(element);
}

/** declared returns the value the sheet's last matching rule gives property
 * for element, or the empty string when no rule sets it. jsdom neither expands
 * a shorthand into its longhands nor resolves a custom property in a computed
 * style, so a case pinning either reads the matching rules instead. */
function declared(element: HTMLElement, property: string): string {
  let value = "";
  for (const sheet of Array.from(document.styleSheets)) {
    for (const rule of Array.from(sheet.cssRules)) {
      if (!(rule instanceof CSSStyleRule)) {
        continue;
      }
      if (!element.matches(rule.selectorText)) {
        continue;
      }
      const found = rule.style.getPropertyValue(property);
      if (found !== "") {
        value = found;
      }
    }
  }
  return value;
}

describe("command palette rows", () => {
  it("squares the row off and takes its border away", () => {
    const plain = row(false);
    expect(declared(plain, "border-radius")).toBe("0");
    expect(declared(plain, "border")).toBe("0");
  });

  it("gives the row the panel's own inset so it reaches both edges", () => {
    const style = window.getComputedStyle(row(false));
    expect(style.paddingLeft).toBe("16px");
    expect(style.paddingRight).toBe("16px");
    const body = styled("div", "palette-body");
    expect(body.paddingLeft).toBe("0px");
    expect(body.paddingRight).toBe("0px");
  });

  it("marks the selection with the accent bar alone", () => {
    const selected = row(true);
    expect(declared(selected, "box-shadow")).toBe("inset 2px 0 var(--acc)");
    expect(declared(selected, "border-color")).toBe("");
  });
});
