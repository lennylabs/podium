import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";

import { parse as parseYaml } from "yaml";

import { escapeHtml } from "./render";
import { resolveLink, type RouteIndex } from "./content/links";
import type { BuildDiagnostic, SiteConfig } from "./types";

/**
 * Pages that moved keep their old URLs working. GitHub Pages serves no
 * redirects of its own, so the build writes a small page at each old route
 * that forwards the reader to the new one.
 *
 * A page that was split across several pages cannot forward every reader to
 * one place, so an entry also maps the old page's anchors to wherever each
 * section now lives. The forwarding page reads the fragment the reader arrived
 * with and picks the matching target.
 */
export const REDIRECTS_FILE = "_redirects.yaml";

export type Redirect = {
  /** Route of the old page, e.g. "/deployment/operator-guide.html". */
  route: string;
  /** Published URL the old page forwards to, with the base path applied. */
  target: string;
  /** Old anchor to published URL, for sections that moved to another page. */
  anchors: Record<string, string>;
};

type RawEntry = { from?: unknown; to?: unknown; anchors?: unknown };

/** Maps a docs-relative written path to the route it would publish at. */
function routeOf(from: string): string {
  const trimmed = from.replace(/^\/+/, "").replace(/\.md$/, "");
  if (trimmed.endsWith("/")) return `/${trimmed}index.html`;
  return `/${trimmed}.html`;
}

/**
 * Reads the redirect list and resolves every target against the finished
 * route index, so a redirect to a page or a heading that does not exist stops
 * the build the same way a broken link does.
 */
export function loadRedirects(
  config: SiteConfig,
  index: RouteIndex,
  diagnostics: BuildDiagnostic[],
): Redirect[] {
  const path = join(config.docsDir, REDIRECTS_FILE);
  if (!existsSync(path)) return [];

  const file = `docs/${REDIRECTS_FILE}`;
  const report = (message: string): void => {
    diagnostics.push({ file, line: null, column: null, message });
  };

  let raw: unknown;
  try {
    raw = parseYaml(readFileSync(path, "utf8"));
  } catch (error) {
    report(`could not parse: ${(error as Error).message}`);
    return [];
  }
  if (raw === null || raw === undefined) return [];
  if (!Array.isArray(raw)) {
    report("expected a list of redirect entries");
    return [];
  }

  // Targets are written docs-relative, which is how a link from a page at the
  // root of the tree reads.
  const resolve = (href: string): string | null =>
    resolveLink(
      href,
      index,
      { fromSource: "index.md", fromRoute: "/index.html", file, line: null, column: null },
      config.basePath,
      diagnostics,
    );

  const published = new Set(index.routeBySource.values());
  const seen = new Set<string>();
  const redirects: Redirect[] = [];
  for (const entry of raw as RawEntry[]) {
    if (typeof entry?.from !== "string" || typeof entry.to !== "string") {
      report("every entry needs a string from and a string to");
      continue;
    }
    const route = routeOf(entry.from);
    if (published.has(route)) {
      report(`"${entry.from}" is still a published page, so it cannot redirect`);
      continue;
    }
    if (seen.has(route)) {
      report(`"${entry.from}" is listed more than once`);
      continue;
    }
    seen.add(route);

    const target = resolve(entry.to);
    if (target === null) continue;

    const anchors: Record<string, string> = {};
    if (entry.anchors !== undefined) {
      if (typeof entry.anchors !== "object" || entry.anchors === null || Array.isArray(entry.anchors)) {
        report(`the anchors of "${entry.from}" must be a mapping`);
        continue;
      }
      for (const [anchor, href] of Object.entries(entry.anchors as Record<string, unknown>)) {
        if (typeof href !== "string") {
          report(`anchor "${anchor}" of "${entry.from}" needs a string target`);
          continue;
        }
        const resolved = resolve(href);
        if (resolved !== null) anchors[anchor] = resolved;
      }
    }

    redirects.push({ route, target, anchors });
  }
  return redirects;
}

/**
 * The forwarding page. The script runs before the refresh fires, and picks the
 * anchor's new home when the reader arrived with a fragment that moved. The
 * refresh and the link cover a reader without JavaScript.
 */
export function renderRedirect(redirect: Redirect): string {
  const target = escapeHtml(redirect.target);
  const map = JSON.stringify(redirect.anchors).replace(/</g, "\\u003c");
  const fallback = JSON.stringify(redirect.target).replace(/</g, "\\u003c");
  return [
    "<!doctype html>",
    '<html lang="en">',
    "<head>",
    '<meta charset="utf-8">',
    "<title>Page moved · Podium</title>",
    '<meta name="robots" content="noindex">',
    `<link rel="canonical" href="${target}">`,
    `<script>(function(){var m=${map};var h=location.hash.slice(1);location.replace(h&&m[h]?m[h]:${fallback}+(h?"#"+h:""));})();</script>`,
    `<meta http-equiv="refresh" content="0; url=${target}">`,
    "</head>",
    "<body>",
    `<p>This page moved to <a href="${target}">${target}</a>.</p>`,
    "</body>",
    "</html>",
    "",
  ].join("\n");
}
