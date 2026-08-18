import type { Element, Root as HastRoot, RootContent } from "hast";
import type { Code, Root as MdastRoot } from "mdast";
import type { Plugin } from "unified";
import { visit } from "unist-util-visit";
import { createHighlighter, type Highlighter } from "shiki";

import type { BuildDiagnostic } from "../types";

/**
 * The languages the corpus uses, plus the project's own two. Loading a fixed
 * set keeps the highlighter small and makes an unrecognised tag an error rather
 * than a silent fall back to unhighlighted text.
 */
export const LANGUAGES = [
  "bash",
  "go",
  "javascript",
  "json",
  "markdown",
  "powershell",
  "python",
  "typescript",
  "yaml",
] as const;

/** Tags that mean "do not highlight this block". */
const PLAIN = new Set(["text", "plaintext", "txt", "console", "output", ""]);

const ALIASES: Record<string, string> = {
  sh: "bash",
  shell: "bash",
  zsh: "bash",
  js: "javascript",
  ts: "typescript",
  yml: "yaml",
  md: "markdown",
  ps1: "powershell",
  golang: "go",
};

const LIGHT_THEME = "github-light";
const DARK_THEME = "github-dark";

let highlighterPromise: Promise<Highlighter> | null = null;

export function getHighlighter(): Promise<Highlighter> {
  highlighterPromise ??= createHighlighter({
    themes: [LIGHT_THEME, DARK_THEME],
    langs: [...LANGUAGES],
  });
  return highlighterPromise;
}

/** Releases the shared highlighter, so a test run does not leak workers. */
export function disposeHighlighter(): void {
  const pending = highlighterPromise;
  highlighterPromise = null;
  void pending?.then((h) => h.dispose());
}

/**
 * Carries the fence language and metadata onto the emitted element.
 *
 * remark-rehype writes the language as a class and drops the metadata, so
 * ```bash {2-3} would lose its line selection before the highlighter runs.
 */
export const remarkCodeMeta: Plugin<[], MdastRoot> = () => (tree: MdastRoot) => {
  visit(tree, "code", (node: Code) => {
    const data = node.data ?? (node.data = {});
    const properties: Record<string, string> = {
      ...((data.hProperties ?? {}) as Record<string, string>),
    };
    if (node.lang) properties["data-language"] = node.lang;
    if (node.meta) properties["data-meta"] = node.meta;
    data.hProperties = properties;
  });
};

/** Parses `{2,5-7}` into the set of 1-based line numbers it names. */
export function parseHighlightedLines(meta: string | undefined): Set<number> {
  const lines = new Set<number>();
  if (!meta) return lines;
  const match = meta.match(/\{([\d,\s-]+)\}/);
  if (!match || match[1] === undefined) return lines;
  for (const part of match[1].split(",")) {
    const range = part.trim();
    if (range === "") continue;
    const [startText, endText] = range.split("-");
    const start = Number(startText);
    const end = endText === undefined ? start : Number(endText);
    if (!Number.isFinite(start) || !Number.isFinite(end)) continue;
    for (let line = start; line <= end; line += 1) lines.add(line);
  }
  return lines;
}

function textOf(node: Element): string {
  let out = "";
  visit(node, "text", (text) => {
    out += text.value;
  });
  return out;
}

/**
 * Reads an element's classes under either key. remark-rehype writes className
 * and Shiki writes class, so a reader that knows only one of them misses the
 * markup the other produced.
 */
function classNames(node: Element): string[] {
  const raw: unknown = node.properties?.["className"] ?? node.properties?.["class"];
  if (Array.isArray(raw)) return raw.map(String);
  if (typeof raw === "string") return raw.split(/\s+/);
  return [];
}

/**
 * Replaces every fenced block in the tree with highlighted markup.
 *
 * Shiki emits both themes as CSS variables in one pass, so switching the theme
 * is a CSS change and no highlighter reaches the browser.
 */
export async function highlightTree(
  tree: HastRoot,
  file: string,
  lineOffset: number,
  diagnostics: BuildDiagnostic[],
): Promise<void> {
  type Target = { parent: { children: RootContent[] }; index: number; pre: Element };
  const targets: Target[] = [];

  visit(tree, "element", (node: Element, index, parent) => {
    if (node.tagName !== "pre" || parent === null || parent === undefined) return;
    if (index === null || index === undefined) return;
    const code = node.children.find(
      (child): child is Element => child.type === "element" && child.tagName === "code",
    );
    if (!code) return;
    targets.push({
      parent: parent as { children: RootContent[] },
      index,
      pre: node,
    });
  });

  if (targets.length === 0) return;
  const highlighter = await getHighlighter();

  for (const target of targets) {
    const code = target.pre.children.find(
      (child): child is Element => child.type === "element" && child.tagName === "code",
    );
    if (!code) continue;

    const declared =
      typeof code.properties?.["data-language"] === "string"
        ? (code.properties["data-language"] as string)
        : (classNames(code)
            .find((name) => name.startsWith("language-"))
            ?.slice("language-".length) ?? "");

    const normalised = ALIASES[declared.toLowerCase()] ?? declared.toLowerCase();
    const source = textOf(code).replace(/\n$/, "");
    const meta =
      typeof code.properties?.["data-meta"] === "string"
        ? (code.properties["data-meta"] as string)
        : undefined;

    if (PLAIN.has(normalised)) {
      annotatePlain(target.pre, declared);
      continue;
    }

    if (!(LANGUAGES as readonly string[]).includes(normalised)) {
      const line = target.pre.position?.start.line;
      diagnostics.push({
        file,
        line: line === undefined ? null : line + lineOffset,
        column: target.pre.position?.start.column ?? null,
        message: `unknown code fence language "${declared}". Supported languages are ${LANGUAGES.join(", ")}, or tag the block "text" to leave it unhighlighted`,
      });
      continue;
    }

    const highlighted = highlighter.codeToHast(source, {
      lang: normalised,
      themes: { light: LIGHT_THEME, dark: DARK_THEME },
      defaultColor: false,
    });

    const pre = highlighted.children.find(
      (child): child is Element => child.type === "element" && child.tagName === "pre",
    );
    if (!pre) continue;

    applyLineHighlights(pre, parseHighlightedLines(meta));

    pre.properties = {
      ...pre.properties,
      "data-language": normalised,
      "data-highlighted": "true",
    };

    target.parent.children[target.index] = pre;
  }
}

/** Marks an unhighlighted block so the component still labels and copies it. */
function annotatePlain(pre: Element, declared: string): void {
  pre.properties = {
    ...pre.properties,
    "data-language": declared === "" ? "text" : declared,
    "data-highlighted": "false",
  };
}

function applyLineHighlights(pre: Element, lines: Set<number>): void {
  if (lines.size === 0) return;
  let lineNumber = 0;
  visit(pre, "element", (node: Element) => {
    if (!classNames(node).includes("line")) return;
    lineNumber += 1;
    if (!lines.has(lineNumber)) return;
    const properties: Record<string, unknown> = { ...node.properties };
    // Shiki wrote the classes under "class"; leaving both keys would emit the
    // attribute twice.
    delete properties["class"];
    properties["className"] = [...classNames(node), "line-highlighted"];
    node.properties = properties as Element["properties"];
  });
}
