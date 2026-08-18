/** One panel's label and its already-rendered markup. */
export type TabPanelData = { label: string; html: string };

/**
 * Reads panel data back out of a rendered tab strip.
 *
 * The client calls this before hydrating so it can render the component from
 * the same data the build used, which is what gives hydration matching markup
 * to attach to. It lives apart from the component so the client can read the
 * DOM without pulling the component into the entry chunk.
 */
export function readPanels(root: Element): TabPanelData[] {
  const panels: TabPanelData[] = [];
  for (const node of root.querySelectorAll("[data-tab-label]")) {
    panels.push({
      label: node.getAttribute("data-tab-label") ?? "",
      html: node.innerHTML,
    });
  }
  return panels;
}
