import { parse as parseYaml } from "yaml";

import type { BuildDiagnostic } from "../types";

/** The complete accepted key set. Anything else stops the build. */
const ALLOWED_KEYS = new Set([
  "title",
  "description",
  "nav_order",
  "nav_title",
  "permalink",
  "actions",
  "hidden",
  "include",
]);

export type RawAction = { label: string; href: string };

/** A repo-root file published as part of a page body. */
export type Include = {
  /** Repo-root-relative path, such as "CHANGELOG.md". */
  file: string;
  /** Sections dropped before inclusion, matched on heading text. */
  skipSections: string[];
  /** Levels every included heading is pushed down by. */
  demote: number;
};

export type Frontmatter = {
  title: string;
  description: string;
  navOrder: number | null;
  navTitle: string | null;
  permalink: string | null;
  actions: RawAction[];
  hidden: boolean;
  /** File whose markdown is appended to the page body. */
  include: Include | null;
};

export type SplitSource = {
  /** Parsed YAML block, or null when the file carries no frontmatter. */
  frontmatter: string | null;
  body: string;
  /** 1-based line the body starts on, so diagnostics point at the right place. */
  bodyStartLine: number;
};

/**
 * Splits a leading `---` delimited YAML block off a markdown source. A file
 * with no such block is not a page; the caller publishes it verbatim.
 */
export function splitFrontmatter(source: string): SplitSource {
  if (!source.startsWith("---")) {
    return { frontmatter: null, body: source, bodyStartLine: 1 };
  }
  const end = source.indexOf("\n---", 3);
  if (end === -1) {
    return { frontmatter: null, body: source, bodyStartLine: 1 };
  }
  const block = source.slice(3, end);
  const after = source.indexOf("\n", end + 1);
  const body = after === -1 ? "" : source.slice(after + 1);
  const bodyStartLine = source.slice(0, after + 1).split("\n").length;
  return { frontmatter: block, body, bodyStartLine };
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/**
 * Validates a frontmatter block against the accepted key set and coerces it to
 * the typed form. Every problem is collected rather than thrown one at a time,
 * so one build run reports every bad page.
 */
export function parseFrontmatter(
  block: string,
  file: string,
  diagnostics: BuildDiagnostic[],
): Frontmatter | null {
  let parsed: unknown;
  try {
    parsed = parseYaml(block);
  } catch (error) {
    diagnostics.push({
      file,
      line: 1,
      column: 1,
      message: `frontmatter is not valid YAML: ${(error as Error).message}`,
    });
    return null;
  }

  if (!isPlainObject(parsed)) {
    diagnostics.push({
      file,
      line: 1,
      column: 1,
      message: "frontmatter must be a mapping of keys to values",
    });
    return null;
  }

  const before = diagnostics.length;

  for (const key of Object.keys(parsed)) {
    if (!ALLOWED_KEYS.has(key)) {
      diagnostics.push({
        file,
        line: 1,
        column: 1,
        message: `unknown frontmatter key "${key}". Accepted keys are ${[...ALLOWED_KEYS]
          .sort()
          .join(", ")}`,
      });
    }
  }

  const title = parsed["title"];
  if (typeof title !== "string" || title.trim() === "") {
    diagnostics.push({
      file,
      line: 1,
      column: 1,
      message: 'frontmatter key "title" is required and must be a non-empty string',
    });
  }

  const description = parsed["description"];
  if (typeof description !== "string" || description.trim() === "") {
    diagnostics.push({
      file,
      line: 1,
      column: 1,
      message:
        'frontmatter key "description" is required and must be a non-empty string',
    });
  }

  const navOrderRaw = parsed["nav_order"];
  let navOrder: number | null = null;
  if (navOrderRaw !== undefined) {
    if (typeof navOrderRaw !== "number" || !Number.isFinite(navOrderRaw)) {
      diagnostics.push({
        file,
        line: 1,
        column: 1,
        message: '"nav_order" must be a number',
      });
    } else {
      navOrder = navOrderRaw;
    }
  }

  const navTitleRaw = parsed["nav_title"];
  let navTitle: string | null = null;
  if (navTitleRaw !== undefined) {
    if (typeof navTitleRaw !== "string") {
      diagnostics.push({
        file,
        line: 1,
        column: 1,
        message: '"nav_title" must be a string',
      });
    } else {
      navTitle = navTitleRaw;
    }
  }

  const include = parseInclude(parsed["include"], file, diagnostics);

  const permalinkRaw = parsed["permalink"];
  let permalink: string | null = null;
  if (permalinkRaw !== undefined) {
    if (typeof permalinkRaw !== "string" || !permalinkRaw.startsWith("/")) {
      diagnostics.push({
        file,
        line: 1,
        column: 1,
        message: '"permalink" must be a string beginning with "/"',
      });
    } else {
      permalink = permalinkRaw;
    }
  }

  const hiddenRaw = parsed["hidden"];
  let hidden = false;
  if (hiddenRaw !== undefined) {
    if (typeof hiddenRaw !== "boolean") {
      diagnostics.push({
        file,
        line: 1,
        column: 1,
        message: '"hidden" must be true or false',
      });
    } else {
      hidden = hiddenRaw;
    }
  }

  const actions = parseActions(parsed["actions"], file, diagnostics);

  if (diagnostics.length !== before) return null;

  return {
    title: title as string,
    description: description as string,
    navOrder,
    navTitle,
    permalink,
    include,
    actions,
    hidden,
  };
}

function parseActions(
  raw: unknown,
  file: string,
  diagnostics: BuildDiagnostic[],
): RawAction[] {
  if (raw === undefined) return [];
  if (!Array.isArray(raw)) {
    diagnostics.push({
      file,
      line: 1,
      column: 1,
      message: '"actions" must be a list',
    });
    return [];
  }

  const actions: RawAction[] = [];
  raw.forEach((entry, index) => {
    if (!isPlainObject(entry)) {
      diagnostics.push({
        file,
        line: 1,
        column: 1,
        message: `actions[${index}] must be a mapping with "label" and "href"`,
      });
      return;
    }
    for (const key of Object.keys(entry)) {
      if (key !== "label" && key !== "href") {
        diagnostics.push({
          file,
          line: 1,
          column: 1,
          message: `actions[${index}] has unknown key "${key}"`,
        });
      }
    }
    const label = entry["label"];
    const href = entry["href"];
    if (typeof label !== "string" || label.trim() === "") {
      diagnostics.push({
        file,
        line: 1,
        column: 1,
        message: `actions[${index}].label is required and must be a non-empty string`,
      });
      return;
    }
    if (typeof href !== "string" || href.trim() === "") {
      diagnostics.push({
        file,
        line: 1,
        column: 1,
        message: `actions[${index}].href is required and must be a non-empty string`,
      });
      return;
    }
    actions.push({ label, href });
  });
  return actions;
}

/**
 * Reads the include, in either the plain-path form or the options form.
 *
 * A page that only needs the file names it directly. The options form is for a
 * file whose own structure has to be adjusted before it sits inside a page: a
 * section that does not belong on the site, or headings that have to nest under
 * the including page's own.
 */
function parseInclude(
  raw: unknown,
  file: string,
  diagnostics: BuildDiagnostic[],
): Include | null {
  if (raw === undefined) return null;

  const reject = (message: string): null => {
    diagnostics.push({ file, line: null, column: null, message });
    return null;
  };

  const source = typeof raw === "string" ? { file: raw } : raw;
  if (typeof source !== "object" || source === null || Array.isArray(source)) {
    return reject('"include" must be a path or a block naming "file"');
  }

  const record = source as Record<string, unknown>;
  for (const key of Object.keys(record)) {
    if (!["file", "skip_sections", "demote"].includes(key)) {
      return reject(`unknown "include" key "${key}". Accepted: demote, file, skip_sections`);
    }
  }

  const path = record["file"];
  if (typeof path !== "string" || path === "" || path.startsWith("/") || path.includes("..")) {
    return reject('"include.file" must be a repo-root-relative path, such as "CHANGELOG.md"');
  }

  const skipRaw = record["skip_sections"];
  let skipSections: string[] = [];
  if (skipRaw !== undefined) {
    if (!Array.isArray(skipRaw) || skipRaw.some((entry) => typeof entry !== "string")) {
      return reject('"include.skip_sections" must be a list of heading titles');
    }
    skipSections = skipRaw as string[];
  }

  const demoteRaw = record["demote"];
  let demote = 0;
  if (demoteRaw !== undefined) {
    if (typeof demoteRaw !== "number" || !Number.isInteger(demoteRaw) || demoteRaw < 0 || demoteRaw > 4) {
      return reject('"include.demote" must be a whole number between 0 and 4');
    }
    demote = demoteRaw;
  }

  return { file: path, skipSections, demote };
}
