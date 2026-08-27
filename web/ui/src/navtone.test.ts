// The shell's two chrome planes. The top bar is raised and the sidebar is
// inset beneath it, which the dark theme carries in the ground each one is
// painted: the bar takes `surf` and the sidebar takes one step below it. The
// light theme places both on the same white. Painting the sidebar `surf` in
// both themes merges the two planes wherever the dark theme applies.
//
// jsdom performs no layout and does not resolve a custom property, so a case
// reads the declaration that reaches the element and the token values each
// theme block writes.

import { afterEach, describe, expect, it } from "vitest";

import "./index.css";

const mounted: HTMLElement[] = [];

afterEach(() => {
  while (mounted.length > 0) {
    mounted.pop()?.remove();
  }
});

/** declared returns the value the sheet's last top-level matching rule gives
 * property for an element carrying the given classes. jsdom does not expand a
 * shorthand into its longhands, so a case pinning `background` reads the
 * matching rules rather than the computed style. */
function declared(className: string, property: string): string {
  const element = document.createElement("div");
  element.className = className;
  document.body.appendChild(element);
  mounted.push(element);
  let value = "";
  for (const sheet of Array.from(document.styleSheets)) {
    for (const rule of Array.from(sheet.cssRules)) {
      if (!(rule instanceof CSSStyleRule) || !element.matches(rule.selectorText)) {
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

/** tokens returns the custom properties the rule for exactly the given
 * selector declares, looking inside the given media condition when one is
 * named and at the top level otherwise. jsdom applies no media query to a
 * computed style, so a case pinning a theme block reads the rules that block
 * holds. */
function tokens(selector: string, condition?: string): CSSStyleDeclaration | undefined {
  let found: CSSStyleDeclaration | undefined;
  const match = (rules: CSSRuleList) => {
    for (const rule of Array.from(rules)) {
      if (rule instanceof CSSStyleRule && rule.selectorText.replace(/\s+/g, " ") === selector) {
        found = rule.style;
      }
    }
  };
  for (const sheet of Array.from(document.styleSheets)) {
    if (condition === undefined) {
      match(sheet.cssRules);
      continue;
    }
    for (const rule of Array.from(sheet.cssRules)) {
      if (rule instanceof CSSMediaRule && rule.conditionText === condition) {
        match(rule.cssRules);
      }
    }
  }
  return found;
}

describe("shell chrome tones", () => {
  it("paints the sidebar from its own token rather than the top bar's", () => {
    expect(declared("sidebar", "background")).toBe("var(--nav-surf)");
  });

  it("keeps the sidebar level with the top bar in the light theme", () => {
    const light = tokens(':root, :root[data-theme="light"]');
    expect(light?.getPropertyValue("--nav-surf").trim()).toBe("#ffffff");
    expect(light?.getPropertyValue("--surf").trim()).toBe("#ffffff");
  });

  it("insets the sidebar below the top bar in both dark theme blocks", () => {
    const preference = tokens(":root", "(prefers-color-scheme: dark)");
    expect(preference?.getPropertyValue("--surf").trim()).toBe("#171b28");
    expect(preference?.getPropertyValue("--nav-surf").trim()).toBe("#141824");

    const stamped = tokens(':root[data-theme="dark"]');
    expect(stamped?.getPropertyValue("--surf").trim()).toBe("#171b28");
    expect(stamped?.getPropertyValue("--nav-surf").trim()).toBe("#141824");
  });
});
