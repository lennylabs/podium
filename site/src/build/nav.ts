import { posix } from "node:path";

import type { NavNode, PageModel } from "./types";

type Draft = {
  page: PageModel | null;
  dir: string;
  children: Map<string, Draft>;
  pages: PageModel[];
};

function emptyDraft(dir: string): Draft {
  return { page: null, dir, children: new Map(), pages: [] };
}

function labelFor(page: PageModel): string {
  return page.navTitle ?? page.title;
}

/**
 * Orders siblings by nav_order, then alphabetically by label.
 *
 * A page without nav_order sorts after every ordered page, and two pages
 * sharing a value fall back to their labels, which is the order the previous
 * theme produced.
 */
function compare(a: NavNode, b: NavNode): number {
  const left = a.navOrder;
  const right = b.navOrder;
  if (left !== null && right !== null && left !== right) return left - right;
  if (left !== null && right === null) return -1;
  if (left === null && right !== null) return 1;
  return a.title.localeCompare(b.title);
}

/**
 * Derives the navigation tree from the directory layout.
 *
 * A directory is a section and its index.md is the section's page, so the tree
 * carries the hierarchy and no page has to name its parent. Hidden pages are
 * left out entirely.
 */
export function buildNav(pages: PageModel[]): NavNode[] {
  const root = emptyDraft(".");

  const visible = pages.filter((page) => !page.hidden);

  for (const page of visible) {
    const source = page.sourcePath.replace(/^docs\//, "");
    const dir = posix.dirname(source);
    const segments = dir === "." ? [] : dir.split("/");

    let node = root;
    let walked = "";
    for (const segment of segments) {
      walked = walked === "" ? segment : `${walked}/${segment}`;
      let child = node.children.get(segment);
      if (child === undefined) {
        child = emptyDraft(walked);
        node.children.set(segment, child);
      }
      node = child;
    }

    if (posix.basename(source) === "index.md") node.page = page;
    else node.pages.push(page);
  }

  return toNodes(root).children;
}

function toNodes(draft: Draft): NavNode {
  const children: NavNode[] = [];

  for (const page of draft.pages) {
    children.push({
      title: labelFor(page),
      route: page.route,
      navOrder: page.navOrder,
      children: [],
    });
  }

  for (const child of draft.children.values()) {
    const node = toNodes(child);
    // A directory with no index.md still groups its pages; it takes the
    // directory name as a label so the section is reachable.
    if (child.page === null && node.children.length === 0) continue;
    children.push(node);
  }

  children.sort(compare);

  const page = draft.page;
  return {
    title: page === null ? titleFromDir(draft.dir) : labelFor(page),
    route: page === null ? "" : page.route,
    navOrder: page === null ? null : page.navOrder,
    children,
  };
}

function titleFromDir(dir: string): string {
  const base = posix.basename(dir);
  return base
    .split("-")
    .map((part) => (part === "" ? part : part[0]!.toUpperCase() + part.slice(1)))
    .join(" ");
}

/** Flattens the tree into reading order, which drives the previous and next links. */
export function flattenNav(nodes: NavNode[]): Array<{ title: string; route: string }> {
  const out: Array<{ title: string; route: string }> = [];
  const walk = (list: NavNode[]): void => {
    for (const node of list) {
      if (node.route !== "") out.push({ title: node.title, route: node.route });
      walk(node.children);
    }
  };
  walk(nodes);
  return out;
}

/** Finds the pages either side of a route in reading order. */
export function neighbours(
  nodes: NavNode[],
  route: string,
): {
  prev: { title: string; route: string } | null;
  next: { title: string; route: string } | null;
} {
  const flat = flattenNav(nodes);
  const position = flat.findIndex((entry) => entry.route === route);
  if (position === -1) return { prev: null, next: null };
  return {
    prev: flat[position - 1] ?? null,
    next: flat[position + 1] ?? null,
  };
}
