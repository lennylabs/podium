// The disabled-control case set. Two states in the layer panel depend on a
// control looking unavailable before it is pressed: a read-only registry
// mutes every write control at once, and a gated dialog holds its confirm
// until the gate is satisfied.
//
// jsdom performs no layout and does not resolve a custom property, so a
// computed colour reads back as the initial value whatever the stylesheet
// says. A case therefore asserts the declaration that wins for the element:
// `cursor` through the computed style, and a token-valued property through
// the last matching rule in the sheet. The two rules that set a disabled
// fill are ordered so the more specific one comes later, so document order
// and specificity agree and the last match is the winner.

import { afterEach, describe, expect, it } from "vitest";

import "./index.css";

const mounted: HTMLElement[] = [];

afterEach(() => {
  while (mounted.length > 0) {
    mounted.pop()?.remove();
  }
});

/** control attaches a disabled control carrying the given classes and returns
 * it. */
function control(
  tag: "button" | "input",
  className = "",
  type?: string,
): HTMLElement {
  const element = document.createElement(tag);
  element.className = className;
  if (type !== undefined && element instanceof HTMLInputElement) {
    element.type = type;
  }
  (element as HTMLButtonElement).disabled = true;
  document.body.appendChild(element);
  mounted.push(element);
  return element;
}

/** declared returns the value the sheet's last matching rule gives property
 * for element, or the empty string when no rule sets it. The rule's
 * declarations are read out of its serialized text, because jsdom's style
 * object does not hand back a value that is a custom-property reference. */
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
      const body = rule.cssText.slice(
        rule.cssText.indexOf("{") + 1,
        rule.cssText.lastIndexOf("}"),
      );
      for (const declaration of body.split(";")) {
        const at = declaration.indexOf(":");
        if (at === -1) {
          continue;
        }
        if (declaration.slice(0, at).trim() !== property) {
          continue;
        }
        value = declaration.slice(at + 1).trim();
      }
    }
  }
  return value;
}

describe("disabled controls", () => {
  it("mutes a plain button", () => {
    const button = control("button");
    expect(getComputedStyle(button).cursor).toBe("not-allowed");
    expect(declared(button, "background")).toBe("var(--chip)");
    expect(declared(button, "color")).toBe("var(--faint)");
  });

  it("drops the accent fill from a disabled primary button", () => {
    const button = control("button", "button primary");
    expect(getComputedStyle(button).cursor).toBe("not-allowed");
    expect(declared(button, "background")).toBe("var(--chip)");
    expect(declared(button, "color")).toBe("var(--faint)");
  });

  it("keeps the destructive confirm in its own quiet tone", () => {
    const button = control("button", "button danger");
    expect(getComputedStyle(button).cursor).toBe("not-allowed");
    expect(declared(button, "background")).toBe("var(--danger-bg)");
    expect(declared(button, "color")).toBe("var(--danger)");
  });

  it("mutes a disabled text field", () => {
    const field = control("input", "", "text");
    expect(getComputedStyle(field).cursor).toBe("not-allowed");
    expect(declared(field, "background")).toBe("var(--chip)");
  });

  it("leaves a disabled checkbox its own box and takes the pointer", () => {
    const box = control("input", "", "checkbox");
    expect(getComputedStyle(box).cursor).toBe("not-allowed");
    expect(declared(box, "background")).toBe("");
  });

  it("leaves an enabled button pressable", () => {
    const button = document.createElement("button");
    button.className = "button primary";
    document.body.appendChild(button);
    mounted.push(button);
    expect(getComputedStyle(button).cursor).toBe("pointer");
    expect(declared(button, "background")).toBe("var(--acc)");
  });
});
