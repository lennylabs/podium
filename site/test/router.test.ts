// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { NAVIGATED_EVENT, NAVIGATE_EVENT, startRouter } from "../src/client/router";

const BASE = "/base";
const ORIGIN = "http://localhost:3000";

type PageSpec = {
  title: string;
  route: string;
  heading: string;
  description?: string;
  body?: string;
  toc?: string;
  tab?: "docs" | "changelog";
};

/**
 * The parts of an emitted documentation page the router reads: the head fields,
 * the top-bar tabs, the navigation tree, the article region, and the
 * on-this-page rail.
 */
function pageHtml(spec: PageSpec): string {
  const tab = spec.tab ?? "docs";
  return `<!doctype html>
<html lang="en">
  <head>
    <title>${spec.title} · Podium</title>
    <meta name="description" content="${spec.description ?? spec.title} summary">
    <link rel="canonical" href="https://example.test${BASE}${spec.route}">
    <meta property="og:title" content="${spec.title} · Podium">
    <meta property="og:description" content="${spec.description ?? spec.title} summary">
    <meta property="og:url" content="https://example.test${BASE}${spec.route}">
  </head>
  <body class="docs" data-base-path="${BASE}">
    <header class="d-topbar">
      <nav class="d-tabs" data-topbar-tabs=""
        ><a class="d-tab${tab === "docs" ? " is-active" : ""}" href="${BASE}/overview.html">Docs</a
        ><a class="d-tab${tab === "changelog" ? " is-active" : ""}" href="${BASE}/about/changelog.html">Changelog</a
      ></nav>
      <button type="button" data-theme-toggle="">dark</button>
    </header>
    <div class="d-shell">
      <nav id="d-sidebar" class="d-sidebar">
        <a class="d-nav-link" href="${BASE}/overview.html">Overview</a>
        <a class="d-nav-link" href="${BASE}/guide/install.html">Install</a>
      </nav>
      <main class="d-main" id="main">
        <article class="d-article" id="doc-article" data-doc-article="" data-toc-root="" tabindex="-1">
          <h1 class="d-title">${spec.heading}</h1>
          ${spec.body ?? `<p>Body of ${spec.heading}.</p>`}
        </article>
      </main>
      <aside class="d-toc" data-doc-toc="">${spec.toc ?? `<p>Rail of ${spec.heading}.</p>`}</aside>
    </div>
  </body>
</html>`;
}

const PAGES: Record<string, string> = {
  [`${BASE}/overview.html`]: pageHtml({
    title: "Overview",
    route: "/overview.html",
    heading: "Overview",
  }),
  [`${BASE}/guide/install.html`]: pageHtml({
    title: "Install",
    route: "/guide/install.html",
    heading: "Installing Podium",
    description: "Install",
    body: '<p>Install prose.</p><h2 id="requirements">Requirements</h2>',
    toc: '<a class="d-toc-link" href="#requirements" data-toc-link="requirements">Requirements</a>',
  }),
  [`${BASE}/about/changelog.html`]: pageHtml({
    title: "Changelog",
    route: "/about/changelog.html",
    heading: "Changelog",
    tab: "changelog",
  }),
};

let fetched: string[] = [];
let status = 200;
/** Pages the fake server answers with something the router cannot swap. */
let unusable = new Set<string>();
let networkFailure = new Set<string>();

function installFetch(): void {
  vi.stubGlobal("fetch", async (input: string) => {
    const url = new URL(String(input), ORIGIN);
    fetched.push(url.pathname);
    if (networkFailure.has(url.pathname)) throw new Error("the network is down");
    const body = PAGES[url.pathname];
    if (body === undefined || status !== 200) {
      return new Response("not found", { status: status === 200 ? 404 : status });
    }
    if (unusable.has(url.pathname)) {
      return new Response("<html><body><p>No article here.</p></body></html>", { status: 200 });
    }
    return new Response(body, { status: 200, headers: { "content-type": "text/html" } });
  });
}

/** Loads a page into the jsdom document the way a full navigation would. */
function loadPage(route: string): void {
  window.history.replaceState(null, "", `${BASE}${route}`);
  const parsed = new DOMParser().parseFromString(
    PAGES[`${BASE}${route}`] ?? "",
    "text/html",
  );
  document.documentElement.innerHTML = parsed.documentElement.innerHTML;
}

let stopRouter: (() => void) | null = null;
let anchorDefaults: AbortController | null = null;
/** True when the router cancelled the last click it saw. */
let intercepted = false;

function start(): { activated: string[]; torndown: string[] } {
  const activated: string[] = [];
  const torndown: string[] = [];
  stopRouter = startRouter({
    basePath: BASE,
    teardown: async (article) => {
      torndown.push(article.querySelector("h1")?.textContent ?? "");
    },
    activate: (article) => {
      activated.push(article.querySelector("h1")?.textContent ?? "");
    },
  });

  // jsdom implements no navigation, so the anchor default is cancelled once the
  // router has had its turn. This listener bubbles on the same node as the
  // router's, and it is registered second, so the router still sees an
  // uncancelled event and its own decision is readable here.
  anchorDefaults = new AbortController();
  document.addEventListener(
    "click",
    (event) => {
      intercepted = event.defaultPrevented;
      event.preventDefault();
    },
    { signal: anchorDefaults.signal },
  );

  return { activated, torndown };
}

/** Clicks a link and reports whether the router took the click. */
function clickLink(selector: string, init: MouseEventInit = {}): boolean {
  const link = document.querySelector<HTMLAnchorElement>(selector);
  if (link === null) throw new Error(`no link matched ${selector}`);
  intercepted = false;
  link.dispatchEvent(
    new MouseEvent("click", { bubbles: true, cancelable: true, button: 0, ...init }),
  );
  return intercepted;
}

/** Resolves after the router's fetch, parse, and swap have all settled. */
async function settle(): Promise<void> {
  for (let round = 0; round < 8; round += 1) {
    await Promise.resolve();
    await new Promise((resolve) => setTimeout(resolve, 0));
  }
}

beforeEach(() => {
  fetched = [];
  status = 200;
  unusable = new Set();
  networkFailure = new Set();
  installFetch();
  vi.stubGlobal("scrollTo", () => undefined);
  Element.prototype.scrollIntoView = (): void => undefined;
  loadPage("/overview.html");
});

afterEach(() => {
  // The document outlives a test, so a router left running would answer the
  // next test's clicks as well.
  stopRouter?.();
  stopRouter = null;
  anchorDefaults?.abort();
  anchorDefaults = null;
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("startRouter", () => {
  it("swaps the article without touching the top bar or the tree", async () => {
    const topbar = document.querySelector(".d-topbar");
    const sidebar = document.querySelector("#d-sidebar");
    start();

    expect(clickLink('a[href="/base/guide/install.html"]')).toBe(true);
    await settle();

    expect(document.querySelector("h1")?.textContent).toBe("Installing Podium");
    expect(document.querySelector(".d-topbar")).toBe(topbar);
    expect(document.querySelector("#d-sidebar")).toBe(sidebar);
  });

  it("fetches the page it swaps in exactly once", async () => {
    start();

    clickLink('a[href="/base/guide/install.html"]');
    await settle();

    expect(fetched).toEqual(["/base/guide/install.html"]);
  });

  it("swaps the on-this-page rail with the article", async () => {
    start();

    clickLink('a[href="/base/guide/install.html"]');
    await settle();

    expect(document.querySelector("[data-doc-toc]")?.textContent).toContain("Requirements");
  });

  it("updates the address bar and the history stack", async () => {
    start();

    clickLink('a[href="/base/guide/install.html"]');
    await settle();

    expect(window.location.pathname).toBe("/base/guide/install.html");
  });

  it("updates the title, the canonical link, and the page metadata", async () => {
    start();

    clickLink('a[href="/base/guide/install.html"]');
    await settle();

    expect(document.title).toBe("Install · Podium");
    expect(document.querySelector('link[rel="canonical"]')?.getAttribute("href")).toBe(
      "https://example.test/base/guide/install.html",
    );
    expect(document.querySelector('meta[name="description"]')?.getAttribute("content")).toBe(
      "Install summary",
    );
    expect(document.querySelector('meta[property="og:url"]')?.getAttribute("content")).toBe(
      "https://example.test/base/guide/install.html",
    );
  });

  it("moves the current marker in the navigation tree", async () => {
    start();

    clickLink('a[href="/base/guide/install.html"]');
    await settle();

    const install = document.querySelector('#d-sidebar a[href="/base/guide/install.html"]');
    const overview = document.querySelector('#d-sidebar a[href="/base/overview.html"]');
    expect(install?.getAttribute("aria-current")).toBe("page");
    expect(install?.classList.contains("is-active")).toBe(true);
    expect(overview?.hasAttribute("aria-current")).toBe(false);
  });

  it("updates the top-bar tabs in place, keeping the controls beside them", async () => {
    start();
    const toggle = document.querySelector("[data-theme-toggle]");
    const tabs = document.querySelector("[data-topbar-tabs]");

    clickLink('a[href="/base/about/changelog.html"]');
    await settle();

    expect(document.querySelector("[data-topbar-tabs]")).toBe(tabs);
    expect(document.querySelector("[data-theme-toggle]")).toBe(toggle);
    expect(
      document.querySelector('.d-tabs a[href="/base/about/changelog.html"]')?.className,
    ).toContain("is-active");
    expect(
      document.querySelector('.d-tabs a[href="/base/overview.html"]')?.className,
    ).not.toContain("is-active");
  });

  it("moves focus to the new article and announces the page", async () => {
    start();

    clickLink('a[href="/base/guide/install.html"]');
    await settle();

    expect(document.activeElement).toBe(document.querySelector("[data-doc-article]"));
    expect(document.querySelector("[data-route-announcer]")?.textContent).toBe(
      "Install · Podium",
    );
  });

  it("gives the live region a polite status role", () => {
    start();

    const announcer = document.querySelector("[data-route-announcer]");
    expect(announcer?.getAttribute("role")).toBe("status");
    expect(announcer?.getAttribute("aria-live")).toBe("polite");
  });

  it("runs the teardown and the activation hooks around the swap", async () => {
    const { activated, torndown } = start();

    clickLink('a[href="/base/guide/install.html"]');
    await settle();

    expect(torndown).toEqual(["Overview"]);
    expect(activated).toEqual(["Installing Podium"]);
  });

  it("announces that a navigation started, then that it finished", async () => {
    start();
    const seen: string[] = [];
    document.addEventListener(NAVIGATE_EVENT, () => seen.push("start"));
    document.addEventListener(NAVIGATED_EVENT, () => seen.push("end"));

    clickLink('a[href="/base/guide/install.html"]');
    await settle();

    expect(seen).toEqual(["start", "end"]);
  });

  it("restores the previous page on a back step", async () => {
    start();
    clickLink('a[href="/base/guide/install.html"]');
    await settle();

    window.history.back();
    await settle();

    expect(window.location.pathname).toBe("/base/overview.html");
    expect(document.querySelector("h1")?.textContent).toBe("Overview");
  });

  it("restores the later page on a forward step", async () => {
    start();
    clickLink('a[href="/base/guide/install.html"]');
    await settle();
    window.history.back();
    await settle();

    window.history.forward();
    await settle();

    expect(window.location.pathname).toBe("/base/guide/install.html");
    expect(document.querySelector("h1")?.textContent).toBe("Installing Podium");
  });

  it("scrolls to the fragment a link names", async () => {
    const scrolled: Element[] = [];
    Element.prototype.scrollIntoView = function scrollIntoView(this: Element): void {
      scrolled.push(this);
    };
    start();
    const link = document.querySelector<HTMLAnchorElement>(
      'a[href="/base/guide/install.html"]',
    );
    link?.setAttribute("href", "/base/guide/install.html#requirements");

    clickLink('a[href="/base/guide/install.html#requirements"]');
    await settle();

    expect(scrolled.map((element) => element.id)).toEqual(["requirements"]);
  });

  it("shows nothing on a page that carries no article region", () => {
    document.body.innerHTML = "<p>Landing.</p>";

    start();

    expect(document.querySelector("[data-route-announcer]")).toBeNull();
    expect(document.querySelector("[data-route-progress]")).toBeNull();
  });

  it("hides the progress bar until a fetch is under way", async () => {
    start();

    const progress = document.querySelector<HTMLElement>("[data-route-progress]");
    expect(progress?.hidden).toBe(true);

    clickLink('a[href="/base/guide/install.html"]');
    await settle();

    expect(progress?.hidden).toBe(true);
  });
});

describe("the links startRouter leaves to the browser", () => {
  const cases: Array<{ name: string; html: string; selector: string; init?: MouseEventInit }> = [
    {
      name: "an external link",
      html: '<a id="probe" href="https://example.com/a.html">External</a>',
      selector: "#probe",
    },
    {
      name: "a download link",
      html: '<a id="probe" href="/base/guide/install.html" download>Download</a>',
      selector: "#probe",
    },
    {
      name: "a link opening in a new tab",
      html: '<a id="probe" href="/base/guide/install.html" target="_blank">New tab</a>',
      selector: "#probe",
    },
    {
      name: "a fragment link",
      html: '<a id="probe" href="#requirements">Requirements</a>',
      selector: "#probe",
    },
    {
      name: "a link to the landing page",
      html: `<a id="probe" href="${BASE}/index.html">Home</a>`,
      selector: "#probe",
    },
    {
      name: "a link to a published asset",
      html: `<a id="probe" href="${BASE}/assets/diagrams/sample.svg">Diagram</a>`,
      selector: "#probe",
    },
    {
      name: "a link outside the base path",
      html: '<a id="probe" href="/elsewhere/page.html">Elsewhere</a>',
      selector: "#probe",
    },
    {
      name: "a link carrying a query string",
      html: `<a id="probe" href="${BASE}/guide/install.html?q=1">Query</a>`,
      selector: "#probe",
    },
    {
      name: "a link into the page already open",
      html: `<a id="probe" href="${BASE}/overview.html">This page</a>`,
      selector: "#probe",
    },
  ];

  for (const probe of cases) {
    it(`leaves ${probe.name} alone`, async () => {
      start();
      document.querySelector("[data-doc-article]")?.insertAdjacentHTML("beforeend", probe.html);

      expect(clickLink(probe.selector, probe.init ?? {})).toBe(false);
      await settle();

      expect(fetched).toEqual([]);
    });
  }

  const modifiers: Array<[string, MouseEventInit]> = [
    ["the command key", { metaKey: true }],
    ["the control key", { ctrlKey: true }],
    ["the shift key", { shiftKey: true }],
    ["the alt key", { altKey: true }],
    ["a middle click", { button: 1 }],
  ];

  for (const [name, init] of modifiers) {
    it(`leaves a click with ${name} alone`, async () => {
      start();

      expect(clickLink('a[href="/base/guide/install.html"]', init)).toBe(false);
      await settle();

      expect(fetched).toEqual([]);
    });
  }

  it("leaves a click another handler already took alone", async () => {
    start();
    const earlier = new AbortController();
    document.addEventListener("click", (event) => event.preventDefault(), {
      capture: true,
      signal: earlier.signal,
    });

    try {
      clickLink('a[href="/base/guide/install.html"]');
      await settle();

      expect(fetched).toEqual([]);
    } finally {
      earlier.abort();
    }
  });

  it("leaves a fragment entry on the page already open alone", async () => {
    start();
    clickLink('a[href="/base/guide/install.html"]');
    await settle();
    fetched = [];

    window.history.pushState(null, "", "/base/guide/install.html#requirements");
    window.dispatchEvent(new PopStateEvent("popstate"));
    await settle();

    expect(fetched).toEqual([]);
  });
});

describe("startRouter when a page cannot be swapped", () => {
  /**
   * jsdom implements no navigation, so a fallback is observed by the article
   * staying put and the address bar keeping the page the reader is on.
   */
  function assertStranded(): void {
    expect(document.querySelector("h1")?.textContent).toBe("Overview");
    expect(window.location.pathname).toBe("/base/overview.html");
  }

  beforeEach(() => {
    vi.spyOn(console, "error").mockImplementation(() => undefined);
  });

  it("hands a non-200 response back to the browser", async () => {
    status = 500;
    start();

    clickLink('a[href="/base/guide/install.html"]');
    await settle();

    expect(fetched).toEqual(["/base/guide/install.html"]);
    assertStranded();
  });

  it("hands a failed request back to the browser", async () => {
    networkFailure.add("/base/guide/install.html");
    start();

    clickLink('a[href="/base/guide/install.html"]');
    await settle();

    assertStranded();
  });

  it("hands a response with no article region back to the browser", async () => {
    unusable.add("/base/guide/install.html");
    start();

    clickLink('a[href="/base/guide/install.html"]');
    await settle();

    assertStranded();
  });
});
