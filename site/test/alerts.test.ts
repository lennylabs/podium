import type { Element, Root as HastRoot } from "hast";
import remarkParse from "remark-parse";
import remarkRehype from "remark-rehype";
import { unified } from "unified";
import { describe, expect, it } from "vitest";

import { ALERT_TYPES, remarkAlerts } from "../src/build/content/alerts";
import { textContent } from "./support/directives";

function transform(source: string): HastRoot {
  const processor = unified()
    .use(remarkParse)
    .use(remarkAlerts)
    .use(remarkRehype, { allowDangerousHtml: false });
  return processor.runSync(processor.parse(source), source) as HastRoot;
}

function firstElement(tree: HastRoot): Element {
  const node = tree.children.find((child): child is Element => child.type === "element");
  if (node === undefined) throw new Error("the tree carries no element");
  return node;
}

describe("remarkAlerts", () => {
  for (const type of ALERT_TYPES) {
    it(`turns a [!${type.toUpperCase()}] blockquote into a ${type} callout`, () => {
      const node = firstElement(
        transform(`> [!${type.toUpperCase()}]\n> Deployment needs an operator.\n`),
      );

      expect(node.tagName).toBe("podium-callout");
      expect(node.properties?.["type"]).toBe(type);
    });
  }

  it("strips the marker from the callout text", () => {
    const node = firstElement(transform("> [!WARNING]\n> Back up the registry first.\n"));

    expect(textContent(node).trim()).toBe("Back up the registry first.");
    expect(textContent(node)).not.toContain("[!WARNING]");
  });

  it("keeps text written on the marker line", () => {
    const node = firstElement(transform("> [!TIP] Run the check first.\n"));

    expect(node.properties?.["type"]).toBe("tip");
    expect(textContent(node).trim()).toBe("Run the check first.");
  });

  it("keeps every paragraph of a multi-paragraph alert", () => {
    const node = firstElement(
      transform("> [!NOTE]\n> First paragraph.\n>\n> Second paragraph.\n"),
    );

    const paragraphs = node.children.filter(
      (child): child is Element => child.type === "element" && child.tagName === "p",
    );
    expect(paragraphs).toHaveLength(2);
    expect(textContent(paragraphs[0])).toBe("First paragraph.");
    expect(textContent(paragraphs[1])).toBe("Second paragraph.");
  });

  it("drops the leading paragraph when the marker stands alone", () => {
    const node = firstElement(transform("> [!IMPORTANT]\n>\n> The registry must be up.\n"));

    const paragraphs = node.children.filter(
      (child): child is Element => child.type === "element" && child.tagName === "p",
    );
    expect(paragraphs).toHaveLength(1);
    expect(textContent(paragraphs[0])).toBe("The registry must be up.");
  });

  it("leaves an unmarked blockquote as a blockquote", () => {
    const node = firstElement(transform("> An ordinary quotation.\n"));

    expect(node.tagName).toBe("blockquote");
    expect(node.properties?.["type"]).toBeUndefined();
  });

  it("leaves a blockquote whose marker names no alert type as a blockquote", () => {
    expect(firstElement(transform("> [!DANGER]\n> Not an alert type.\n")).tagName).toBe(
      "blockquote",
    );
  });

  it("leaves a blockquote opening with a list rather than a paragraph alone", () => {
    expect(firstElement(transform("> - [!NOTE]\n> - item\n")).tagName).toBe("blockquote");
  });

  it("leaves a blockquote whose first paragraph opens with emphasis alone", () => {
    expect(firstElement(transform("> *[!NOTE]* text\n")).tagName).toBe("blockquote");
  });
});
