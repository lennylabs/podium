// The pointer-feedback case set. Every control that carries the button chrome
// answers a hover with one step of darkening and a press with a 1px drop, so
// a pointer over a control and a press on it are both visible before the write
// they issue returns.
//
// jsdom performs no layout and resolves no custom property, so a case reads
// the declaration the winning rule states rather than a computed colour. A
// hover rule matches no element in jsdom either, because jsdom has no pointer,
// so the case resolves the rule that wins by specificity and document order
// over the element's own selector.

import { afterEach, describe, expect, it } from "vitest";

import "./index.css";

const mounted: HTMLElement[] = [];

afterEach(() => {
  while (mounted.length > 0) {
    mounted.pop()?.remove();
  }
});

/** attach mounts an element of the given tag and classes and returns it. */
function attach(tag: string, className = ""): HTMLElement {
  const element = document.createElement(tag);
  element.className = className;
  document.body.appendChild(element);
  mounted.push(element);
  return element;
}

/** bodyRow mounts a table carrying the given classes and returns one body row,
 * which is the element a listing table's pointer rule is written against. The
 * cell classes name the cells the row holds, so a case can build the row that
 * stands in for an empty table as well as an ordinary one. */
function bodyRow(tableClass: string, ...cells: string[]): HTMLTableRowElement {
  const table = attach("table", tableClass) as HTMLTableElement;
  const body = table.appendChild(document.createElement("tbody"));
  const row = body.appendChild(document.createElement("tr"));
  for (const cell of cells) {
    row.appendChild(document.createElement("td")).className = cell;
  }
  return row;
}

/** button attaches an enabled button carrying the given classes. */
function button(className = ""): HTMLButtonElement {
  const element = document.createElement("button");
  element.className = className;
  document.body.appendChild(element);
  mounted.push(element);
  return element;
}

/** selectors splits a rule's selector list on the commas that separate its
 * selectors, leaving the commas inside a functional pseudo-class such as
 * `:where(button, .button)` where they are. */
function selectors(list: string): string[] {
  const out: string[] = [];
  let depth = 0;
  let current = "";
  for (const character of list) {
    if (character === "(") {
      depth += 1;
    } else if (character === ")") {
      depth -= 1;
    }
    if (character === "," && depth === 0) {
      out.push(current.trim());
      current = "";
      continue;
    }
    current += character;
  }
  out.push(current.trim());
  return out.filter((selector) => selector !== "");
}

/** base strips the pointer state and its disabled guard from a selector,
 * leaving the part an idle element is matched against. */
function base(selector: string): string {
  return selector
    .replace(":where(:not(:disabled))", "")
    .replace(":hover", "")
    .replace(":active", "")
    .trim();
}

/** specificity scores a selector the way the cascade does, counting ids,
 * classes with attributes and pseudo-classes, and element names. `:where()`
 * contributes nothing, which is what keeps the shared hover rule at the
 * specificity of the rule it overrides. */
function specificity(selector: string): number {
  const outside = selector.replace(/:where\((?:[^()]|\([^()]*\))*\)/g, "");
  const ids = outside.match(/#[\w-]+/g)?.length ?? 0;
  const classes =
    (outside.match(/\.[\w-]+/g)?.length ?? 0) +
    (outside.match(/\[[^\]]*\]/g)?.length ?? 0) +
    (outside.match(/:(?!:)[\w-]+/g)?.length ?? 0);
  const elements = outside.match(/(^|[\s>+~(,])[a-z]+/g)?.length ?? 0;
  return ids * 10000 + classes * 100 + elements;
}

/** stateDeclared returns the value the sheet's winning rule gives property for
 * element in the given pointer state, or the empty string when no rule in that
 * state sets it. A rule is a candidate when one of its selectors names the
 * state and the element matches the rest of that selector; the highest
 * specificity wins, and document order breaks a tie. */
function stateDeclared(
  element: HTMLElement,
  state: ":hover" | ":active",
  property: string,
): string {
  let value = "";
  let best = -1;
  for (const sheet of Array.from(document.styleSheets)) {
    for (const rule of Array.from(sheet.cssRules)) {
      if (!(rule instanceof CSSStyleRule)) {
        continue;
      }
      for (const stated of selectors(rule.selectorText)) {
        if (!stated.includes(state)) {
          continue;
        }
        const idle = base(stated);
        if (idle === "" || !element.matches(idle)) {
          continue;
        }
        const score = specificity(stated);
        if (score < best) {
          continue;
        }
        const declared = rule.style.getPropertyValue(property).trim();
        if (declared === "") {
          continue;
        }
        best = score;
        value = declared;
      }
    }
  }
  return value;
}

describe("pointer feedback", () => {
  it("darkens a plain button under the pointer", () => {
    expect(stateDeclared(button(), ":hover", "background")).toBe("var(--chip)");
  });

  it("darkens the chromed button under the pointer", () => {
    expect(stateDeclared(button("button"), ":hover", "background")).toBe(
      "var(--chip)",
    );
  });

  it("darkens the accent fill of the primary button", () => {
    const primary = button("button primary");
    expect(stateDeclared(primary, ":hover", "background")).toBe(
      "var(--acc-hover)",
    );
    expect(stateDeclared(primary, ":hover", "border-color")).toBe(
      "var(--acc-hover)",
    );
  });

  it("darkens the destructive fill of the confirm", () => {
    const danger = button("button danger");
    expect(stateDeclared(danger, ":hover", "background")).toBe(
      "var(--danger-hover)",
    );
    expect(stateDeclared(danger, ":hover", "border-color")).toBe(
      "var(--danger-hover)",
    );
  });

  it("keeps a refused control in its own tone under the pointer", () => {
    const refused = button("action-refused");
    expect(stateDeclared(refused, ":hover", "background")).toBe(
      "var(--danger-bg)",
    );
    expect(stateDeclared(refused, ":hover", "border-color")).toBe(
      "var(--danger)",
    );
  });

  it("washes the quiet accent control, which has no chrome to darken", () => {
    expect(
      stateDeclared(button("button link-action"), ":hover", "background"),
    ).toBe("var(--wash)");
  });

  it("drops every pressable control 1px", () => {
    for (const classes of ["", "button", "button primary", "button danger"]) {
      expect(stateDeclared(button(classes), ":active", "transform")).toBe(
        "translateY(1px)",
      );
    }
  });

  it("leaves a disabled control unmoved by the pointer", () => {
    const disabled = button("button primary");
    disabled.disabled = true;
    // Every pointer rule is guarded by :not(:disabled), so a control the
    // registry would refuse answers neither a hover nor a press.
    for (const sheet of Array.from(document.styleSheets)) {
      for (const rule of Array.from(sheet.cssRules)) {
        if (!(rule instanceof CSSStyleRule)) {
          continue;
        }
        for (const stated of selectors(rule.selectorText)) {
          if (!stated.includes(":hover") && !stated.includes(":active")) {
            continue;
          }
          const idle = base(stated);
          if (idle === "" || !disabled.matches(idle)) {
            continue;
          }
          expect(stated).toContain(":not(:disabled)");
        }
      }
    }
  });

  // The design pass fixes one step toward `chip` for a listing entry under
  // the pointer, so the reader sees which row or card a click will land on.
  it("raises a listing row and a subdomain card under the pointer", () => {
    for (const [tag, className] of [
      ["li", "artifact-row"],
      ["li", "subdomain"],
      ["li", "tile"],
    ] as const) {
      expect(stateDeclared(attach(tag, className), ":hover", "background")).toBe(
        "var(--chip)",
      );
    }
  });

  it("raises a listing table's row under the pointer", () => {
    for (const tableClass of [
      "data-table layer-table",
      "data-table restore-table",
      "data-table artifact-rows",
    ]) {
      expect(
        stateDeclared(bodyRow(tableClass, "mono"), ":hover", "background-color"),
      ).toBe("var(--chip)");
    }
  });

  it("keeps the soft badge's box on a raised entry", () => {
    const row = attach("li", "artifact-row");
    const badge = row.appendChild(document.createElement("span"));
    badge.className = "badge badge-soft";
    expect(stateDeclared(badge, ":hover", "border-color")).toBe("var(--bd)");
    const cell = bodyRow("data-table layer-table", "source-col").cells[0];
    const marker = cell.appendChild(document.createElement("span"));
    marker.className = "badge badge-soft";
    expect(stateDeclared(marker, ":hover", "border-color")).toBe("var(--bd)");
  });

  // A detail panel is a row of its own under the row that opened it, so it
  // does not light up as a second entry.
  it("leaves a detail panel where it is", () => {
    const detail = bodyRow("data-table layer-table", "");
    detail.className = "row-detail";
    expect(stateDeclared(detail, ":hover", "background-color")).toBe("");
  });

  // The message an empty listing carries answers no click, and its cell paints
  // the table's own ground over the row's, whatever the row is drawn in.
  it("keeps the empty-listing message on the table's ground", () => {
    const absent = bodyRow("data-table artifact-rows", "table-empty");
    const cell = absent.querySelector<HTMLElement>(".table-empty");
    expect(cell).not.toBeNull();
    expect(getComputedStyle(cell as HTMLElement).backgroundColor).toBe(
      "var(--surf)",
    );
  });

  // The property table and the resources table list an artifact's own fields,
  // where a row is not a target.
  it("leaves a table of fields unraised", () => {
    for (const tableClass of ["data-table property-table", "data-table"]) {
      expect(
        stateDeclared(bodyRow(tableClass, "mono"), ":hover", "background-color"),
      ).toBe("");
    }
  });

  it("names each pointer token in both themes", () => {
    const declarations = Array.from(document.styleSheets)
      .flatMap((sheet) => Array.from(sheet.cssRules))
      .flatMap((rule) =>
        rule instanceof CSSStyleRule
          ? [rule]
          : rule instanceof CSSMediaRule
            ? Array.from(rule.cssRules).filter(
                (inner): inner is CSSStyleRule => inner instanceof CSSStyleRule,
              )
            : [],
      );
    for (const token of ["--acc-hover", "--danger-hover"]) {
      const values = declarations
        .map((rule) => rule.style.getPropertyValue(token).trim())
        .filter((value) => value !== "");
      // The light default, the dark media query, and the dark override each
      // declare the token, so a theme toggle carries the pointer state with
      // it. The two dark arms state the same value.
      expect(values).toHaveLength(3);
      expect(values[1]).toBe(values[2]);
    }
  });
});
