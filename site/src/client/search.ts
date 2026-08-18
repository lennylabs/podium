import type MiniSearchType from "minisearch";

import { NAVIGATE_EVENT } from "./router";

type Stored = {
  route: string;
  page: string;
  heading: string;
  section: string;
};

let index: MiniSearchType<Stored> | null = null;
let loading: Promise<void> | null = null;

/**
 * Fetches the prebuilt index on the reader's first interaction with the search
 * field, so a page that is only read never pays for it.
 */
async function load(basePath: string): Promise<void> {
  if (index !== null) return;
  // The engine is fetched alongside the index rather than in the page bundle,
  // so a reader who never searches never downloads it.
  // The build writes the index under a hashed name and names it on the body,
  // so the file is immutable and the fetch never returns a stale corpus.
  const url =
    document.body.dataset["searchIndex"] ?? `${basePath}/assets/search-index.json`;
  const [{ default: MiniSearch }, serialized] = await Promise.all([
    import("minisearch"),
    fetch(url).then((response) => response.text()),
  ]);
  index = MiniSearch.loadJSON<Stored>(serialized, {
    fields: ["page", "heading", "text"],
    storeFields: ["route", "page", "heading", "section"],
    searchOptions: { boost: { page: 3, heading: 2 }, prefix: true, fuzzy: 0.2 },
  });
}

function ensureOverlay(): {
  overlay: HTMLElement;
  input: HTMLInputElement;
  results: HTMLElement;
} {
  const existing = document.querySelector<HTMLElement>("[data-search-overlay]");
  if (existing !== null) {
    return {
      overlay: existing,
      input: existing.querySelector("input") as HTMLInputElement,
      results: existing.querySelector("[data-search-results]") as HTMLElement,
    };
  }

  const overlay = document.createElement("div");
  overlay.setAttribute("data-search-overlay", "");
  overlay.setAttribute("role", "dialog");
  overlay.setAttribute("aria-modal", "true");
  overlay.setAttribute("aria-label", "Search the documentation");
  overlay.hidden = true;
  overlay.innerHTML = `
    <div class="search-panel">
      <input type="search" class="search-field" placeholder="Search artifacts, pages, CLI flags" aria-label="Search" autocomplete="off" />
      <ul class="search-results" data-search-results></ul>
    </div>
  `;
  document.body.append(overlay);

  const input = overlay.querySelector("input") as HTMLInputElement;
  const results = overlay.querySelector("[data-search-results]") as HTMLElement;

  overlay.addEventListener("click", (event) => {
    if (event.target === overlay) close(overlay);
  });

  return { overlay, input, results };
}

function close(overlay: HTMLElement): void {
  overlay.hidden = true;
  document.body.removeAttribute("data-search-open");
}

function open(overlay: HTMLElement, input: HTMLInputElement): void {
  overlay.hidden = false;
  document.body.setAttribute("data-search-open", "true");
  input.focus();
  input.select();
}

/**
 * The index stores each route as the site publishes it, without the path prefix
 * the site is served under, so every result href is joined with the base path
 * the page carries.
 */
function render(
  results: HTMLElement,
  basePath: string,
  hits: Array<Stored & { score: number }>,
): void {
  if (hits.length === 0) {
    results.innerHTML = `<li class="search-empty">No matches.</li>`;
    return;
  }
  results.innerHTML = hits
    .slice(0, 12)
    .map((hit) => {
      const context = [hit.section, hit.page].filter((part) => part !== "").join(" · ");
      const label = hit.heading === "" ? hit.page : hit.heading;
      const href = escape(`${basePath}${hit.route}`);
      return `<li class="search-result"><a href="${href}"><span class="search-result-title">${escape(label)}</span><span class="search-result-context">${escape(context)}</span></a></li>`;
    })
    .join("");
}

function escape(value: string): string {
  return value.replace(/[&<>"]/g, (character) => {
    switch (character) {
      case "&":
        return "&amp;";
      case "<":
        return "&lt;";
      case ">":
        return "&gt;";
      default:
        return "&quot;";
    }
  });
}

/**
 * Wires the search field and the keyboard shortcut. Both open the same overlay,
 * which is the single entry point the design specifies.
 */
export function mountSearch(basePath: string): void {
  const fields = document.querySelectorAll<HTMLElement>("[data-search-input]");
  if (fields.length === 0) return;

  const { overlay, input, results } = ensureOverlay();

  const begin = (): void => {
    open(overlay, input);
    loading ??= load(basePath);
    void loading.then(() => run());
  };

  const run = (): void => {
    if (index === null) return;
    const query = input.value.trim();
    if (query === "") {
      results.innerHTML = "";
      return;
    }
    render(
      results,
      basePath,
      index.search(query) as unknown as Array<Stored & { score: number }>,
    );
  };

  for (const field of fields) {
    field.addEventListener("click", begin);
    field.addEventListener("focus", begin);
  }

  input.addEventListener("input", run);

  // A result the router takes over never reloads the page, so the overlay is
  // closed here rather than by the navigation.
  document.addEventListener(NAVIGATE_EVENT, () => close(overlay));

  document.addEventListener("keydown", (event) => {
    if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
      event.preventDefault();
      begin();
      return;
    }
    if (event.key === "Escape" && !overlay.hidden) close(overlay);
  });
}
