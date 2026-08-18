import { createElement } from "react";
import { createRoot, hydrateRoot, type Root } from "react-dom/client";

import { readPanels } from "../components/content/tab-data";
import { registry } from "../components/islands/registry";

/**
 * Every root this module created, keyed by the element it owns. The client
 * router replaces the article on a navigation, so a root has to be released
 * before the element it renders into leaves the document.
 */
const roots = new Map<HTMLElement, Root>();

/**
 * Attaches every island the page declares.
 *
 * This module is loaded only when a page carries an island, which keeps React
 * out of the bundle for the pages that do not. Every documentation page is
 * complete HTML on its own, so most readers never fetch it.
 *
 * The two kinds attach differently, because React requires a hydrated tree to
 * produce the markup the server already wrote. A container island rebuilds its
 * input by reading the DOM and hydrates over matching markup. A leaf island
 * shows a static fallback whose markup is deliberately not the component's, so
 * it mounts a fresh root over it instead.
 */
export async function mountIslands(nodes: Iterable<HTMLElement>): Promise<void> {
  const reduced = window.matchMedia("(prefers-reduced-motion: reduce)").matches;

  for (const node of nodes) {
    if (roots.has(node)) continue;
    const name = node.getAttribute("data-island") ?? "";
    const mount = node.getAttribute("data-island-mount") ?? "replace";
    const entry = registry[name];
    if (entry?.load === undefined) continue;

    // A leaf island is where an animated variant lives. Reduced motion keeps
    // the static fallback exactly as rendered.
    if (mount === "replace" && reduced) continue;

    // A container island renders from data the browser reads back out of the
    // DOM, so both renders work from identical input.
    if (name === "tabs") {
      const module = await entry.load();
      roots.set(
        node,
        hydrateRoot(
          node,
          createElement(module.default as never, { panels: readPanels(node) } as never),
        ),
      );
      continue;
    }

    let props: Record<string, unknown> = {};
    const raw = node.getAttribute("data-island-props");
    if (raw !== null) {
      try {
        props = JSON.parse(raw) as Record<string, unknown>;
      } catch {
        continue;
      }
    }

    const module = await entry.load();
    const element = createElement(module.default as never, props as never);
    if (mount === "hydrate") {
      roots.set(node, hydrateRoot(node, element));
    } else {
      const root = createRoot(node);
      roots.set(node, root);
      root.render(element);
    }
  }
}

/**
 * Releases the roots rendering inside a region before that region leaves the
 * document. React holds the DOM it rendered, so detaching the markup without
 * unmounting leaves the component tree scheduled against nodes nothing points
 * at.
 */
export function unmountIslands(within: Node): void {
  for (const [node, root] of [...roots]) {
    if (!within.contains(node)) continue;
    roots.delete(node);
    root.unmount();
  }
}
