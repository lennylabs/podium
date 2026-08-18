import { startRouter } from "./router";
import { mountSearch } from "./search";
import { startSidebar } from "./sidebar";

const THEME_KEY = "podium-theme";
const basePath = document.body.dataset["basePath"] ?? "";

/* ------------------------------------------------------------------ theme */

function currentTheme(): "light" | "dark" {
  const explicit = document.documentElement.getAttribute("data-theme");
  if (explicit === "light" || explicit === "dark") return explicit;
  return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}

function applyTheme(theme: "light" | "dark"): void {
  document.documentElement.setAttribute("data-theme", theme);
  try {
    window.localStorage.setItem(THEME_KEY, theme);
  } catch {
    // A blocked write means the choice lasts for this page only.
  }
  for (const toggle of document.querySelectorAll<HTMLElement>("[data-theme-toggle]")) {
    const next = theme === "dark" ? "light" : "dark";
    // The control names the theme it switches to, so its label is a promise
    // rather than a status.
    toggle.textContent = next;
    toggle.setAttribute("aria-label", `Switch to the ${next} theme`);
  }
}

function initTheme(): void {
  applyTheme(currentTheme());
  for (const toggle of document.querySelectorAll<HTMLElement>("[data-theme-toggle]")) {
    toggle.addEventListener("click", () => {
      applyTheme(currentTheme() === "dark" ? "light" : "dark");
    });
  }
}

/* ------------------------------------------------------------------- copy */

/**
 * Binds the copy buttons inside a region. The router swaps the article on a
 * navigation, so the buttons that arrive with it are bound against the region
 * they came in rather than against the whole document.
 */
function initCopy(root: ParentNode): void {
  const buttons = root.querySelectorAll<HTMLButtonElement>(
    "[data-copy], [data-copy-target]",
  );

  for (const button of buttons) {
    button.addEventListener("click", () => {
      const explicit = button.getAttribute("data-copy-target");
      const source = explicit
        ? document.querySelector(explicit)
        : button.closest(".code-block")?.querySelector("pre");
      const text = source?.textContent ?? "";
      if (text === "") return;

      const idle = button.getAttribute("data-copy-label") ?? button.textContent ?? "copy";
      const done = button.getAttribute("data-copy-done-label") ?? "copied";

      void navigator.clipboard.writeText(text).then(() => {
        button.textContent = done;
        button.setAttribute("data-copied", "true");
        window.setTimeout(() => {
          button.textContent = idle;
          button.removeAttribute("data-copied");
        }, 1500);
      });
    });
  }
}

/* ---------------------------------------------------------- on this page */

/**
 * The observer watching the current article's headings. A navigation replaces
 * both the article and the rail, so the observer is disconnected and built
 * again against the headings that are now in the document.
 */
let spy: IntersectionObserver | null = null;

function initScrollSpy(): void {
  spy?.disconnect();
  spy = null;

  const links = new Map<string, HTMLElement>();
  for (const link of document.querySelectorAll<HTMLElement>("[data-toc-link]")) {
    const id = link.getAttribute("data-toc-link");
    if (id !== null) links.set(id, link);
  }
  if (links.size === 0) return;

  const article = document.querySelector("[data-toc-root]");
  if (article === null) return;

  const headings = [...article.querySelectorAll<HTMLElement>("h2[id], h3[id], h4[id]")];
  if (headings.length === 0) return;

  let active: HTMLElement | null = null;
  const mark = (id: string): void => {
    const link = links.get(id);
    if (link === undefined || link === active) return;
    active?.removeAttribute("data-active");
    link.setAttribute("data-active", "true");
    active = link;
  };

  const observer = new IntersectionObserver(
    (entries) => {
      const visible = entries
        .filter((entry) => entry.isIntersecting)
        .sort((a, b) => a.boundingClientRect.top - b.boundingClientRect.top);
      const first = visible[0]?.target.id;
      if (first !== undefined) mark(first);
    },
    { rootMargin: "0px 0px -70% 0px", threshold: 0 },
  );

  for (const heading of headings) observer.observe(heading);
  spy = observer;
}

/* ---------------------------------------------------------------- islands */

/**
 * Loads the island runtime only when the page has something to mount.
 *
 * React is the largest thing the browser could be asked to download, and a page
 * that declares no island needs none of it. The promise is kept so a later
 * navigation reuses the module it already fetched, and so an article leaving
 * the document can release the roots the module created.
 */
let islandRuntime: Promise<typeof import("./islands")> | null = null;

function mountIslandsIn(root: ParentNode): void {
  const nodes = root.querySelectorAll<HTMLElement>("[data-island]");
  if (nodes.length === 0) return;
  islandRuntime ??= import("./islands");
  void islandRuntime.then((module) => module.mountIslands(nodes));
}

async function unmountIslandsIn(root: Node): Promise<void> {
  if (islandRuntime === null) return;
  const module = await islandRuntime;
  module.unmountIslands(root);
}

/* ------------------------------------------------------------------- boot */

initTheme();
initCopy(document);
startSidebar();
initScrollSpy();
mountSearch(basePath);
mountIslandsIn(document);

startRouter({
  basePath,
  teardown: unmountIslandsIn,
  activate: (article) => {
    initCopy(article);
    initScrollSpy();
    mountIslandsIn(article);
  },
});
