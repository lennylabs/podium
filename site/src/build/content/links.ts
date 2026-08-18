import { posix } from "node:path";

import type { BuildDiagnostic } from "../types";

/** Everything the resolver needs to turn a written link into a published URL. */
export type RouteIndex = {
  /** Every emitted route, including static files. */
  routes: Set<string>;
  /** Heading ids per page route, for anchor validation. */
  anchors: Map<string, Set<string>>;
  /** docs-relative source path to route, which permalinks make non-identical. */
  routeBySource: Map<string, string>;
  /** Routes excluded from navigation and search but still published. */
  hidden: Set<string>;
};

export type LinkContext = {
  /** docs-relative path of the file holding the link, e.g. "deployment/oidc/index.md". */
  fromSource: string;
  /** Route of the page holding the link, for same-page anchors. */
  fromRoute: string;
  file: string;
  line: number | null;
  column: number | null;
};

const SCHEME = /^[a-zA-Z][a-zA-Z0-9+.-]*:/;

export function isExternal(href: string): boolean {
  return SCHEME.test(href) || href.startsWith("//");
}

/**
 * Maps a docs-relative source path to the route it publishes at, applying the
 * same extension rules the build uses.
 */
export function routeForSource(
  source: string,
  routeBySource: Map<string, string>,
): string | null {
  return routeBySource.get(source) ?? null;
}

/**
 * Resolves an internal markdown link to a published URL.
 *
 * Accepts the three forms the corpus uses: an extensionless relative path, an
 * explicit .md path, and a directory path ending in "/". Returns null and
 * records a diagnostic when the target does not exist, so a broken link stops
 * the build instead of shipping.
 */
export function resolveLink(
  href: string,
  index: RouteIndex,
  ctx: LinkContext,
  basePath: string,
  diagnostics: BuildDiagnostic[],
): string | null {
  if (href === "") return null;
  if (isExternal(href)) return href;
  // A fragment, a query, or a root-relative asset path is left as written; the
  // first two are same-page and the third already names a published location.
  if (href.startsWith("#")) {
    const anchor = href.slice(1);
    const available = index.anchors.get(ctx.fromRoute);
    if (available && !available.has(anchor)) {
      diagnostics.push({
        file: ctx.file,
        line: ctx.line,
        column: ctx.column,
        message: `link to "#${anchor}" matches no heading on this page`,
      });
      return null;
    }
    return href;
  }

  const [rawPath = "", rawAnchor] = splitAnchor(href);
  const targetSource = toSourcePath(rawPath, ctx.fromSource);
  if (targetSource === null) {
    diagnostics.push({
      file: ctx.file,
      line: ctx.line,
      column: ctx.column,
      message: `link "${href}" points outside the documentation tree`,
    });
    return null;
  }

  const route = resolveToRoute(targetSource, index);
  if (route === null) {
    diagnostics.push({
      file: ctx.file,
      line: ctx.line,
      column: ctx.column,
      message: `link "${href}" resolves to "${targetSource}", which is not published`,
    });
    return null;
  }

  if (rawAnchor !== undefined) {
    const available = index.anchors.get(route);
    if (available && !available.has(rawAnchor)) {
      diagnostics.push({
        file: ctx.file,
        line: ctx.line,
        column: ctx.column,
        message: `link "${href}" points at "#${rawAnchor}", which matches no heading on that page`,
      });
      return null;
    }
  }

  return `${basePath}${route}${rawAnchor === undefined ? "" : `#${rawAnchor}`}`;
}

function splitAnchor(href: string): [string, string | undefined] {
  const hash = href.indexOf("#");
  if (hash === -1) return [href, undefined];
  return [href.slice(0, hash), href.slice(hash + 1)];
}

/**
 * Turns a written relative path into the docs-relative source path it names.
 * Returns null when it escapes the documentation tree.
 */
function toSourcePath(rawPath: string, fromSource: string): string | null {
  const fromDir = posix.dirname(fromSource);
  const joined = rawPath.startsWith("/")
    ? rawPath.slice(1)
    : posix.normalize(posix.join(fromDir === "." ? "" : fromDir, rawPath));

  if (joined.startsWith("..")) return null;
  return joined;
}

/**
 * Applies the three accepted link forms in turn. A directory link and an
 * extensionless link both fall back to the section's index page.
 */
function resolveToRoute(targetSource: string, index: RouteIndex): string | null {
  const candidates: string[] = [];

  if (targetSource.endsWith("/")) {
    candidates.push(`${targetSource}index.md`);
  } else if (targetSource.endsWith(".md")) {
    candidates.push(targetSource);
  } else if (posix.extname(targetSource) === "") {
    candidates.push(`${targetSource}.md`, `${targetSource}/index.md`);
  } else {
    // A path with another extension names a static file, such as a diagram.
    const staticRoute = `/${targetSource}`;
    return index.routes.has(staticRoute) ? staticRoute : null;
  }

  for (const candidate of candidates) {
    const route = index.routeBySource.get(candidate);
    if (route !== undefined) return route;
  }
  // An empty path means the current directory's index page.
  if (targetSource === "" || targetSource === ".") {
    const route = index.routeBySource.get("index.md");
    if (route !== undefined) return route;
  }
  return null;
}
