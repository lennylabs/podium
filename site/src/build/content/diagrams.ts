import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";

import type { Element, Root as HastRoot } from "hast";
import { visit } from "unist-util-visit";

import type { BuildDiagnostic, SiteConfig } from "../types";

/** Diagrams live in one directory, which is what makes them recognisable here. */
const DIAGRAM_DIR = "assets/diagrams/";

/**
 * Replaces every reference to a checked-in diagram with the SVG markup itself.
 *
 * An SVG loaded through an img tag renders in its own document. It cannot read
 * the page's custom properties and it cannot use the page's webfonts, so a
 * diagram embedded that way is stuck with whatever it hardcodes. Inlining the
 * markup puts it in the page's document instead, where the design tokens
 * resolve and the theme toggle drives them.
 *
 * The markdown keeps the ordinary image reference, so a reader on github.com
 * still gets the file. Each diagram declares fallback values next to every
 * token it reads, which is what that reader sees.
 */
export function inlineDiagrams(
  tree: HastRoot,
  config: SiteConfig,
  file: string,
  diagnostics: BuildDiagnostic[],
): void {
  replaceImages(tree, config, file, diagnostics);
  unwrapParagraphs(tree);
}

function replaceImages(
  tree: HastRoot,
  config: SiteConfig,
  file: string,
  diagnostics: BuildDiagnostic[],
): void {
  visit(tree, "element", (node: Element, index, parent) => {
    if (node.tagName !== "img" || parent === undefined || index === undefined) return;

    const src = node.properties?.["src"];
    if (typeof src !== "string") return;

    const relative = diagramPath(src, config.basePath);
    if (relative === null) return;

    const onDisk = resolve(config.docsDir, relative);
    if (!existsSync(onDisk)) {
      diagnostics.push({
        file,
        line: node.position?.start.line ?? null,
        column: node.position?.start.column ?? null,
        message: `diagram "${relative}" is missing from docs/`,
      });
      return;
    }

    const alt = node.properties?.["alt"];
    parent.children[index] = {
      type: "element",
      tagName: "podium-diagram",
      properties: {
        "data-alt": typeof alt === "string" ? alt : "",
        "data-markup": readFileSync(onDisk, "utf8"),
      },
      children: [],
      position: node.position,
    };
  });
}

/**
 * Lifts a diagram out of the paragraph the markdown image produced.
 *
 * An image alone on a line parses as a paragraph wrapping it. The replacement
 * is a block element, and a block element inside a paragraph is markup a
 * browser splits apart, so the paragraph goes and the diagram takes its place.
 */
function unwrapParagraphs(tree: HastRoot): void {
  visit(tree, "element", (node: Element, index, parent) => {
    if (node.tagName !== "p" || parent === undefined || index === undefined) return;

    const meaningful = node.children.filter(
      (child) => child.type !== "text" || child.value.trim() !== "",
    );
    const only = meaningful[0];
    if (
      meaningful.length !== 1 ||
      only === undefined ||
      only.type !== "element" ||
      only.tagName !== "podium-diagram"
    ) {
      return;
    }

    parent.children[index] = only;
  });
}

/** The docs-relative path of a diagram, or null when the src names something else. */
function diagramPath(src: string, basePath: string): string | null {
  const withoutBase =
    basePath !== "" && src.startsWith(`${basePath}/`) ? src.slice(basePath.length) : src;
  const trimmed = withoutBase.replace(/^\//, "");
  if (!trimmed.startsWith(DIAGRAM_DIR) || !trimmed.endsWith(".svg")) return null;
  // A path that climbs out of the directory is not one of ours.
  if (trimmed.includes("..")) return null;
  return trimmed;
}
