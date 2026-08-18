import type { Element, Root as HastRoot } from "hast";
import { visit } from "unist-util-visit";

import type { BuildDiagnostic, Heading } from "../types";

const HEADING_TAGS = new Set(["h1", "h2", "h3", "h4", "h5", "h6"]);

/** Elements whose text is markup rather than prose, excluded from the index. */
const NON_PROSE = new Set(["pre", "script", "style", "podium-island"]);

function textOf(node: Element): string {
  let out = "";
  visit(node, "text", (text) => {
    out += text.value;
  });
  return out.trim();
}

/**
 * Collects the heading outline used by the on-this-page rail and the search
 * index. rehype-slug has already assigned ids, and rehype-autolink-headings has
 * added the anchor link, whose text is excluded here.
 */
export function collectHeadings(tree: HastRoot): Heading[] {
  const headings: Heading[] = [];
  visit(tree, "element", (node: Element) => {
    if (!HEADING_TAGS.has(node.tagName)) return;
    const id = typeof node.properties?.["id"] === "string" ? node.properties["id"] : "";
    if (id === "") return;
    const text = textOf({
      ...node,
      children: node.children.filter(
        (child) =>
          !(
            child.type === "element" &&
            child.tagName === "a" &&
            String(
              Array.isArray(child.properties?.["className"])
                ? child.properties["className"].join(" ")
                : (child.properties?.["className"] ?? ""),
            ).includes("heading-anchor")
          ),
      ),
    });
    headings.push({ depth: Number(node.tagName.slice(1)), id, text });
  });
  return headings;
}

/** Flattens the page's prose for the search index, skipping code and markup. */
export function collectText(tree: HastRoot): string {
  const parts: string[] = [];
  const gather = (node: {
    type: string;
    children?: unknown[];
    tagName?: string;
    value?: string;
  }): void => {
    if (node.type === "text") {
      parts.push(String(node.value ?? ""));
      return;
    }
    if (node.type === "element" && node.tagName && NON_PROSE.has(node.tagName)) return;
    for (const child of (node.children ?? []) as Array<Parameters<typeof gather>[0]>) {
      gather(child);
    }
    // Adjacent text nodes are one run of prose and are joined directly; a space
    // is added at element boundaries so words do not run together. Joining
    // everything with a space would split "17:00" across its restored nodes.
    if (node.type === "element") parts.push(" ");
  };
  gather(tree as unknown as Parameters<typeof gather>[0]);
  return parts.join("").replace(/\s+/g, " ").trim();
}

/**
 * Reports a heading outline that skips a level, which breaks assistive
 * navigation. The page title is an h1 rendered by the template, so a body that
 * opens at h3 has skipped h2.
 */
export function checkHeadingOrder(
  headings: Heading[],
  file: string,
  diagnostics: BuildDiagnostic[],
): void {
  let previous = 1;
  for (const heading of headings) {
    if (heading.depth > previous + 1) {
      diagnostics.push({
        file,
        line: null,
        column: null,
        message: `heading "${heading.text}" is an h${heading.depth} directly below an h${previous}, which skips a level`,
      });
    }
    previous = heading.depth;
  }
}

/** Reports an image with no alt text. */
export function checkImageAlt(
  tree: HastRoot,
  file: string,
  lineOffset: number,
  diagnostics: BuildDiagnostic[],
): void {
  visit(tree, "element", (node: Element) => {
    if (node.tagName !== "img") return;
    const alt = node.properties?.["alt"];
    if (typeof alt === "string" && alt.trim() !== "") return;
    const line = node.position?.start.line;
    diagnostics.push({
      file,
      line: line === undefined ? null : line + lineOffset,
      column: node.position?.start.column ?? null,
      message: `image "${String(node.properties?.["src"] ?? "")}" has no alt text`,
    });
  });
}
