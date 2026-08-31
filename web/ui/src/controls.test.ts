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
    // The text-field fill would swallow the box whole. It keeps the empty
    // square the checkbox draws for itself and mutes only its border.
    expect(declared(box, "background")).toBe("transparent");
    expect(declared(box, "border-color")).toBe("var(--b2)");
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

// Chrome derives a native checkbox's whole appearance from accent-color
// rather than from color-scheme, so an accent-coloured box came back as a
// light-appearance control whatever the theme said: on the dark surface an
// unchecked visibility grant and the secret reveal's acknowledgement both
// rendered as a solid pale square, which reads as a control already set. The
// box paints both of its own states off the token set instead.
describe("the checkbox's own paint", () => {
  /** box attaches an enabled checkbox in the given checked state. */
  function box(checked: boolean): HTMLInputElement {
    const input = document.createElement("input");
    input.type = "checkbox";
    input.checked = checked;
    document.body.appendChild(input);
    mounted.push(input);
    return input;
  }

  /** ruleFor returns the serialized declarations of the sheet rule carrying
   * the given selector, which is how a pseudo-element rule is read: it
   * matches no element, so `declared` cannot reach it. */
  function ruleFor(selector: string): string {
    for (const sheet of Array.from(document.styleSheets)) {
      for (const rule of Array.from(sheet.cssRules)) {
        if (rule instanceof CSSStyleRule && rule.selectorText === selector) {
          return rule.cssText;
        }
      }
    }
    return "";
  }

  it("draws an unchecked box as an empty square behind a hairline border", () => {
    const unchecked = box(false);
    expect(declared(unchecked, "appearance")).toBe("none");
    expect(declared(unchecked, "background")).toBe("transparent");
    expect(declared(unchecked, "border")).toBe("1.5px solid var(--bd)");
    // The browser paints nothing of its own now, so the accent no longer
    // decides the control's appearance.
    expect(declared(unchecked, "accent-color")).toBe("");
  });

  it("draws a checked box as the accent fill behind a dark tick", () => {
    const checked = box(true);
    expect(declared(checked, "background")).toBe("var(--acc)");
    expect(declared(checked, "border-color")).toBe("var(--acc)");
    expect(ruleFor('input[type="checkbox"]:checked::after')).toContain(
      "var(--on-acc)",
    );
  });
});
