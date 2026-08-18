/**
 * Client-side navigation between documentation pages.
 *
 * Every page is complete HTML on the server. Without JavaScript a click is an
 * ordinary navigation to a page that already exists, and this module layers a
 * swap on top of that. It fetches the target page, takes the article region and
 * the on-this-page rail out of the response, and puts them in place of the
 * current ones. The top bar and the navigation tree stay in the document, so
 * their listeners, their scroll position, and the collapsed state of each group
 * survive the navigation.
 *
 * Any condition the swap cannot handle falls back to a full navigation: a
 * network failure, a response that is not 200, and a document carrying no
 * article region. A reader is never left on a page that did not change.
 */

const ARTICLE_SELECTOR = "[data-doc-article]";
const TOC_SELECTOR = "[data-doc-toc]";
const TABS_SELECTOR = "[data-topbar-tabs]";
const SIDEBAR_SELECTOR = "#d-sidebar";

/** Delay before the progress bar appears, so a fast swap shows nothing. */
const PROGRESS_DELAY_MS = 200;

/** Head elements whose value differs from page to page. */
const HEAD_FIELDS: Array<{ selector: string; attribute: string }> = [
  { selector: 'link[rel="canonical"]', attribute: "href" },
  { selector: 'meta[name="description"]', attribute: "content" },
  { selector: 'meta[property="og:title"]', attribute: "content" },
  { selector: 'meta[property="og:description"]', attribute: "content" },
  { selector: 'meta[property="og:url"]', attribute: "content" },
];

/**
 * Dispatched on `document` when the router takes over a click. Anything holding
 * page-level state, such as the search overlay, listens for it and closes.
 */
export const NAVIGATE_EVENT = "podium:navigate";

/** Dispatched on `document` once the new article is in the document. */
export const NAVIGATED_EVENT = "podium:navigated";

export type RouterHooks = {
  /** Path prefix every emitted URL carries, "" or "/podium". */
  basePath: string;
  /** Releases whatever is bound inside the article that is leaving. */
  teardown: (article: HTMLElement) => Promise<void>;
  /** Binds the per-page behaviour to the article now in the document. */
  activate: (article: HTMLElement) => void;
};

type NavigationMode = "push" | "pop";

type ScrollState = { podiumScrollY?: number };

/**
 * Starts intercepting navigation and returns the function that stops it again.
 * Nothing is bound on a page that carries no article region, which is what the
 * landing page and the 404 page look like.
 */
export function startRouter(hooks: RouterHooks): () => void {
  if (document.querySelector<HTMLElement>(ARTICLE_SELECTOR) === null) {
    return () => undefined;
  }

  const announcer = appendAnnouncer();
  const progress = appendProgress();
  const listeners = new AbortController();

  let renderedPath = window.location.pathname;
  let generation = 0;

  if ("scrollRestoration" in window.history) {
    // The router puts the reader back where they were, so the browser must not
    // also restore a position against content it has not seen yet.
    window.history.scrollRestoration = "manual";
  }

  const isDocumentRoute = (url: URL): boolean => {
    if (url.origin !== window.location.origin) return false;
    if (url.search !== "") return false;
    const { basePath } = hooks;
    if (basePath !== "" && !url.pathname.startsWith(`${basePath}/`)) return false;
    const route = url.pathname.slice(basePath.length);
    if (!route.endsWith(".html")) return false;
    // The landing page and the 404 page carry a different layout, so they are
    // reached by a full navigation.
    return route !== "/index.html" && route !== "/404.html";
  };

  const navigate = async (url: URL, mode: NavigationMode): Promise<void> => {
    const token = ++generation;
    document.dispatchEvent(new CustomEvent(NAVIGATE_EVENT, { detail: { url: url.href } }));
    const stopProgress = startProgress(progress);

    const next = await loadDocument(url);
    stopProgress();
    if (token !== generation) return;

    const incoming = next?.querySelector<HTMLElement>(ARTICLE_SELECTOR) ?? null;
    const outgoing = document.querySelector<HTMLElement>(ARTICLE_SELECTOR);
    if (next === null || incoming === null || outgoing === null) {
      leave(url);
      return;
    }

    await hooks.teardown(outgoing);
    if (token !== generation) return;

    const article = document.importNode(incoming, true);
    outgoing.replaceWith(article);
    replaceRegion(next, TOC_SELECTOR);
    replaceChildren(next, TABS_SELECTOR);
    copyHead(next);

    if (mode === "push") {
      window.history.pushState({ podiumScrollY: 0 } satisfies ScrollState, "", url.href);
    }
    renderedPath = url.pathname;

    markSidebar(url.pathname);
    hooks.activate(article);

    article.focus({ preventScroll: true });
    restoreScroll(url, mode);
    announcer.textContent = document.title;
    document.dispatchEvent(new CustomEvent(NAVIGATED_EVENT, { detail: { url: url.href } }));
  };

  const onClick = (event: MouseEvent): void => {
    if (event.defaultPrevented) return;
    if (event.button !== 0) return;
    if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;

    const target = event.target;
    if (!(target instanceof Element)) return;
    const anchor = target.closest("a");
    if (anchor === null) return;
    if (anchor.hasAttribute("download")) return;

    const frame = anchor.getAttribute("target");
    if (frame !== null && frame !== "" && frame !== "_self") return;

    const href = anchor.getAttribute("href");
    if (href === null || href === "" || href.startsWith("#")) return;

    const url = new URL(anchor.href, window.location.href);
    if (!isDocumentRoute(url)) return;
    // A link into the current page is a fragment jump the browser already does.
    if (url.pathname === window.location.pathname) return;

    event.preventDefault();
    window.history.replaceState({ podiumScrollY: window.scrollY } satisfies ScrollState, "");
    void navigate(url, "push");
  };

  const onPopState = (): void => {
    const url = new URL(window.location.href);
    // A fragment entry on the page already rendered needs no swap.
    if (url.pathname === renderedPath) return;
    if (!isDocumentRoute(url)) {
      leave(url);
      return;
    }
    void navigate(url, "pop");
  };

  document.addEventListener("click", onClick, { signal: listeners.signal });
  window.addEventListener("popstate", onPopState, { signal: listeners.signal });

  return () => {
    generation += 1;
    listeners.abort();
    announcer.remove();
    progress.remove();
  };
}

/** Fetches a page and parses it, returning null for anything unusable. */
async function loadDocument(url: URL): Promise<Document | null> {
  try {
    const response = await fetch(url.href, {
      credentials: "same-origin",
      headers: { accept: "text/html" },
    });
    if (!response.ok) return null;
    const parsed = new DOMParser().parseFromString(await response.text(), "text/html");
    return parsed.querySelector(ARTICLE_SELECTOR) === null ? null : parsed;
  } catch {
    return null;
  }
}

/** Hands the navigation back to the browser. */
function leave(url: URL): void {
  if (url.href === window.location.href) window.location.reload();
  else window.location.assign(url.href);
}

function replaceRegion(next: Document, selector: string): void {
  const incoming = next.querySelector(selector);
  const current = document.querySelector(selector);
  if (incoming === null || current === null) return;
  current.replaceWith(document.importNode(incoming, true));
}

/**
 * Replaces the contents of a region while keeping the element itself. The top
 * bar's section tabs are updated this way, so the search field and the theme
 * toggle beside them keep the listeners they were given on first load.
 */
function replaceChildren(next: Document, selector: string): void {
  const incoming = next.querySelector(selector);
  const current = document.querySelector(selector);
  if (incoming === null || current === null) return;
  const children = [...incoming.childNodes].map((node) => document.importNode(node, true));
  current.replaceChildren(...children);
}

function copyHead(next: Document): void {
  document.title = next.title;
  for (const field of HEAD_FIELDS) {
    const incoming = next.querySelector(field.selector);
    const current = document.querySelector(field.selector);
    if (incoming === null || current === null) continue;
    const value = incoming.getAttribute(field.attribute);
    if (value !== null) current.setAttribute(field.attribute, value);
  }
}

/**
 * Moves the current marker in the navigation tree. The tree is matched by href
 * rather than rebuilt, so a collapsed group stays collapsed across a
 * navigation.
 */
function markSidebar(pathname: string): void {
  const sidebar = document.querySelector(SIDEBAR_SELECTOR);
  if (sidebar === null) return;
  for (const link of sidebar.querySelectorAll<HTMLAnchorElement>("a[href]")) {
    const current = link.pathname === pathname;
    link.classList.toggle("is-active", current);
    if (current) link.setAttribute("aria-current", "page");
    else link.removeAttribute("aria-current");
  }
}

/**
 * Puts the reader where a full navigation would have put them. The move is
 * instant in both branches, because a swap is a page change rather than a jump
 * within a page the reader is already looking at, and an animated scroll across
 * content nobody has seen reads as motion for its own sake. This also keeps the
 * behaviour identical for a reader who has asked for reduced motion.
 */
function restoreScroll(url: URL, mode: NavigationMode): void {
  if (url.hash !== "") {
    const target = document.getElementById(decodeURIComponent(url.hash.slice(1)));
    if (target !== null) {
      target.scrollIntoView({ behavior: "instant", block: "start" });
      return;
    }
  }
  const state = window.history.state as ScrollState | null;
  const offset =
    mode === "pop" && typeof state?.podiumScrollY === "number" ? state.podiumScrollY : 0;
  window.scrollTo({ top: offset, left: 0, behavior: "instant" });
}

/**
 * The live region that names the page a navigation landed on. Focus moves to
 * the article as well, so a screen reader announces the page and then resumes
 * reading at the top of the new content.
 */
function appendAnnouncer(): HTMLElement {
  const existing = document.querySelector<HTMLElement>("[data-route-announcer]");
  if (existing !== null) return existing;

  const announcer = document.createElement("div");
  announcer.className = "sr-only";
  announcer.setAttribute("data-route-announcer", "");
  announcer.setAttribute("role", "status");
  announcer.setAttribute("aria-live", "polite");
  document.body.append(announcer);
  return announcer;
}

/**
 * The bar shown while a page is on its way. It is created here rather than in
 * the markup, because a reader without JavaScript never navigates this way.
 */
function appendProgress(): HTMLElement {
  const existing = document.querySelector<HTMLElement>("[data-route-progress]");
  if (existing !== null) return existing;

  const progress = document.createElement("div");
  progress.className = "route-progress";
  progress.setAttribute("data-route-progress", "");
  progress.setAttribute("aria-hidden", "true");
  progress.hidden = true;
  document.body.append(progress);
  return progress;
}

function startProgress(progress: HTMLElement): () => void {
  const timer = window.setTimeout(() => {
    progress.hidden = false;
  }, PROGRESS_DELAY_MS);

  return () => {
    window.clearTimeout(timer);
    progress.hidden = true;
  };
}
