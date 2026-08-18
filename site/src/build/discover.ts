import { execFileSync } from "node:child_process";
import { readFileSync, readdirSync, statSync } from "node:fs";
import { join, posix, relative, sep } from "node:path";

import {
  parseFrontmatter,
  splitFrontmatter,
  type Frontmatter,
} from "./content/frontmatter";
import type { BuildDiagnostic, SiteConfig, StaticFile } from "./types";

/** A markdown file carrying frontmatter, before its body has been parsed. */
export type DiscoveredPage = {
  /** docs-relative source path, e.g. "getting-started/quickstart.md". */
  source: string;
  /** Repository-relative path, used in diagnostics. */
  file: string;
  route: string;
  body: string;
  bodyStartLine: number;
  frontmatter: Frontmatter;
};

export type Discovery = {
  pages: DiscoveredPage[];
  statics: StaticFile[];
  diagnostics: BuildDiagnostic[];
};

/**
 * Jekyll excluded dotfiles and underscore-prefixed entries, and the published
 * site never carried them. Keeping that rule avoids publishing editor droppings
 * and build caches.
 */
function isSkipped(entry: string): boolean {
  return entry.startsWith(".") || entry.startsWith("_");
}

function walk(dir: string, acc: string[] = []): string[] {
  for (const entry of readdirSync(dir).sort()) {
    if (isSkipped(entry)) continue;
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) walk(full, acc);
    else acc.push(full);
  }
  return acc;
}

/** Maps a docs-relative source path to its published route. */
export function routeForPage(source: string, permalink: string | null): string {
  if (permalink !== null) {
    if (permalink === "/") return "/index.html";
    return permalink.endsWith("/") ? `${permalink}index.html` : permalink;
  }
  return `/${source.replace(/\.md$/, ".html")}`;
}

/**
 * Reads the corpus and classifies every file.
 *
 * A markdown file with frontmatter is a page. A markdown file without it is not
 * a page: it is published verbatim at its source path, which is what the
 * previous build did, and it gets no navigation entry and no link rewriting.
 */
export function discover(config: SiteConfig): Discovery {
  const diagnostics: BuildDiagnostic[] = [];
  const pages: DiscoveredPage[] = [];
  const statics: StaticFile[] = [];

  for (const absolute of walk(config.docsDir)) {
    const source = relative(config.docsDir, absolute).split(sep).join(posix.sep);
    const file = relative(config.repoRoot, absolute).split(sep).join(posix.sep);

    if (!source.endsWith(".md")) {
      statics.push({ route: `/${source}`, sourcePath: file });
      continue;
    }

    const raw = readFileSync(absolute, "utf8");
    const split = splitFrontmatter(raw);
    if (split.frontmatter === null) {
      statics.push({ route: `/${source}`, sourcePath: file });
      continue;
    }

    const frontmatter = parseFrontmatter(split.frontmatter, file, diagnostics);
    if (frontmatter === null) continue;

    pages.push({
      source,
      file,
      route: routeForPage(source, frontmatter.permalink),
      body: split.body,
      bodyStartLine: split.bodyStartLine,
      frontmatter,
    });
  }

  reportDuplicateRoutes(pages, diagnostics);
  return { pages, statics, diagnostics };
}

function reportDuplicateRoutes(
  pages: DiscoveredPage[],
  diagnostics: BuildDiagnostic[],
): void {
  const seen = new Map<string, string>();
  for (const page of pages) {
    const previous = seen.get(page.route);
    if (previous !== undefined) {
      diagnostics.push({
        file: page.file,
        line: 1,
        column: 1,
        message: `route "${page.route}" is already published by ${previous}`,
      });
      continue;
    }
    seen.set(page.route, page.file);
  }
}

/**
 * Reads the last commit date for every file under docs/ in one pass.
 *
 * A shallow clone carries no history, so this returns an empty map and pages
 * render without a date rather than failing the build.
 */
export function lastModified(config: SiteConfig): Map<string, string> {
  const dates = new Map<string, string>();
  let output: string;
  try {
    output = execFileSync(
      "git",
      ["log", "--pretty=format:%cI", "--name-only", "--no-merges", "--", "docs"],
      { cwd: config.repoRoot, encoding: "utf8", maxBuffer: 32 * 1024 * 1024 },
    );
  } catch {
    return dates;
  }

  let current = "";
  for (const line of output.split("\n")) {
    if (line === "") continue;
    if (/^\d{4}-\d{2}-\d{2}T/.test(line)) {
      current = line;
      continue;
    }
    // Commits arrive newest first, so the first mention of a path is its most
    // recent change.
    if (!dates.has(line)) dates.set(line, current);
  }
  return dates;
}
