/**
 * The navigation tree: collapsing its groups, and opening it as a drawer.
 *
 * The tree and the top bar stay in the document across a client navigation, so
 * these listeners are bound once and never rebound.
 *
 * Below the desktop breakpoint the tree leaves the layout and becomes a panel
 * over the article. Which state it is in is CSS's decision; this module only
 * records whether the reader has asked for it, as `data-open` on the tree and
 * `data-drawer-open` on the body. At a width where the tree is part of the
 * layout those attributes select nothing, so the same markup serves both.
 */
export function startSidebar(): void {
  for (const header of document.querySelectorAll<HTMLElement>("[data-nav-group]")) {
    header.addEventListener("click", () => {
      const group = header.closest("[data-nav-section]");
      if (group === null) return;
      const collapsed = group.getAttribute("data-collapsed") === "true";
      group.setAttribute("data-collapsed", collapsed ? "false" : "true");
      header.setAttribute("aria-expanded", collapsed ? "true" : "false");
    });
  }

  const toggle = document.querySelector<HTMLElement>("[data-sidebar-toggle]");
  const sidebar = document.querySelector<HTMLElement>("[data-sidebar]");
  if (toggle === null || sidebar === null) return;

  const setOpen = (open: boolean): void => {
    sidebar.setAttribute("data-open", open ? "true" : "false");
    toggle.setAttribute("aria-expanded", open ? "true" : "false");
    // The drawer covers the article, so the page behind it must not scroll
    // under the reader's finger.
    document.body.toggleAttribute("data-drawer-open", open);
  };

  toggle.addEventListener("click", () => {
    setOpen(sidebar.getAttribute("data-open") !== "true");
  });

  // Following a link inside the drawer navigates, which has to close it. The
  // router swaps the article underneath, and the drawer would otherwise be left
  // covering the page the reader just asked for.
  sidebar.addEventListener("click", (event) => {
    if ((event.target as Element | null)?.closest("a") !== null) setOpen(false);
  });

  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && sidebar.getAttribute("data-open") === "true") {
      setOpen(false);
      toggle.focus();
    }
  });

  // A viewport that grows past the drawer breakpoint puts the tree back in the
  // layout, where the open state means nothing and the body must not stay
  // locked. Older Safari exposes the listener under addListener alone.
  const wide = window.matchMedia(DESKTOP_QUERY);
  const release = (event: { matches: boolean }): void => {
    if (event.matches) setOpen(false);
  };
  if (typeof wide.addEventListener === "function") wide.addEventListener("change", release);
  else if (typeof wide.addListener === "function") wide.addListener(release);
}

/** The width at which the tree rejoins the layout. Mirrors docs.css. */
export const DESKTOP_QUERY = "(min-width: 1100px)";
