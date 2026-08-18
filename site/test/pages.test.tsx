import type { Root as HastRoot } from "hast";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import { loadConfig } from "../src/build/config";
import { buildNav } from "../src/build/nav";
import { Footer } from "../src/components/layout/Footer";
import { Header, withBase } from "../src/components/layout/Header";
import { Sidebar } from "../src/components/layout/Sidebar";
import { TableOfContents } from "../src/components/layout/TableOfContents";
import { Doc } from "../src/pages/Doc";
import { Landing } from "../src/pages/Landing";
import { NotFound } from "../src/pages/NotFound";
import type { NavNode, PageModel } from "../src/build/types";
import { FIXTURE_CORPUS } from "./support/corpus";

const CONFIG = loadConfig({
  repoRoot: FIXTURE_CORPUS,
  docsDir: FIXTURE_CORPUS,
  siteUrl: "https://example.test",
  basePath: "/base",
  version: "1.2.3",
});

const EMPTY_BODY: HastRoot = { type: "root", children: [] };

function model(overrides: Partial<PageModel> & { route: string }): PageModel {
  return {
    sourcePath: `docs${overrides.route.replace(/\.html$/, ".md")}`,
    title: "Install",
    displayTitle: "Installing Podium",
    navTitle: null,
    description: "Install the fixture tool.",
    navOrder: null,
    hidden: false,
    sectionRoute: null,
    headings: [],
    actions: [],
    body: EMPTY_BODY,
    islands: [],
    editUrl: "https://github.com/lennylabs/podium/edit/main/docs/guide/install.md",
    updatedAt: "",
    text: "",
    ...overrides,
  };
}

const NAV: NavNode[] = buildNav([
  model({ route: "/overview.html", title: "Overview", navOrder: 0, sourcePath: "docs/overview.md" }),
  model({
    route: "/guide/index.html",
    title: "Guide",
    navOrder: 1,
    sourcePath: "docs/guide/index.md",
  }),
  model({
    route: "/guide/install.html",
    title: "Install",
    navOrder: 1,
    sourcePath: "docs/guide/install.md",
  }),
  model({
    route: "/guide/tabs.html",
    title: "Tab strip",
    navOrder: 2,
    sourcePath: "docs/guide/tabs.md",
  }),
]);

/** A three-level tree, which is what the deployment section looks like. */
const NESTED: NavNode[] = buildNav([
  model({
    route: "/deployment/index.html",
    title: "Deployment",
    navOrder: 1,
    sourcePath: "docs/deployment/index.md",
  }),
  model({
    route: "/deployment/oidc/index.html",
    title: "OIDC",
    navOrder: 1,
    sourcePath: "docs/deployment/oidc/index.md",
  }),
  model({
    route: "/deployment/oidc/okta.html",
    title: "Okta",
    navOrder: 1,
    sourcePath: "docs/deployment/oidc/okta.md",
  }),
]);

function markup(element: Parameters<typeof renderToStaticMarkup>[0]): string {
  return renderToStaticMarkup(element);
}

describe("withBase", () => {
  const cases: Array<[string, string, string]> = [
    ["/base", "/guide/install.html", "/base/guide/install.html"],
    ["/base", "guide/install.html", "/base/guide/install.html"],
    ["", "/guide/install.html", "/guide/install.html"],
    ["/base", "", ""],
    ["/base", "#top", "#top"],
    ["/base", "https://example.com/a", "https://example.com/a"],
    ["/base", "mailto:alice@acme.com", "mailto:alice@acme.com"],
  ];

  for (const [basePath, route, expected] of cases) {
    it(`joins ${JSON.stringify(basePath)} with ${JSON.stringify(route)}`, () => {
      expect(withBase(basePath, route)).toBe(expected);
    });
  }
});

describe("Header", () => {
  it("renders one tab per top-level section and marks the current one", () => {
    const html = markup(
      <Header config={CONFIG} nav={NAV} activeRoute="/guide/install.html" />,
    );

    expect(html).toContain('href="/base/guide/index.html" aria-current="page"');
    expect(html).toContain(">Overview</a>");
    expect(html).toContain('class="d-tab is-active"');
  });

  it("marks a section current when the page is the section index itself", () => {
    const html = markup(
      <Header config={CONFIG} nav={NAV} activeRoute="/guide/index.html" />,
    );

    expect(html).toContain('class="d-tab is-active"');
  });

  it("marks no section current on a page outside the tree", () => {
    const html = markup(<Header config={CONFIG} nav={NAV} activeRoute="" />);

    expect(html).not.toContain("is-active");
  });

  it("shows the version the config carries", () => {
    expect(markup(<Header config={CONFIG} nav={NAV} activeRoute="" />)).toContain(
      '<span class="d-version">v1.2.3</span>',
    );
  });

  it("carries the search, theme, and sidebar hooks the client script binds", () => {
    const html = markup(<Header config={CONFIG} nav={NAV} activeRoute="" />);

    expect(html).toContain("data-search-input");
    expect(html).toContain("data-theme-toggle");
    expect(html).toContain("data-sidebar-toggle");
  });
});

describe("Sidebar", () => {
  it("renders each top-level node as a group and its descendants as a list", () => {
    const html = markup(
      <Sidebar nav={NAV} activeRoute="/guide/install.html" basePath="/base" />,
    );

    expect(html).toContain('class="d-nav-group-label"');
    expect(html).toContain('href="/base/guide/install.html" aria-current="page"');
    expect(html).toContain('class="d-nav-link is-active"');
    expect(html).toContain('id="d-nav-guide-index-html"');
  });

  it("ships every group expanded, so the tree is complete without JavaScript", () => {
    const html = markup(<Sidebar nav={NAV} activeRoute="" basePath="/base" />);

    expect(html).toContain('aria-expanded="true"');
    expect(html).not.toContain('aria-expanded="false"');
  });

  it("marks the group itself current when the page is the section index", () => {
    const html = markup(
      <Sidebar nav={NAV} activeRoute="/guide/index.html" basePath="/base" />,
    );

    expect(html).toContain('class="d-nav-group-label is-active"');
  });

  it("renders a nested section as an indented sublist", () => {
    const html = markup(
      <Sidebar nav={NESTED} activeRoute="/deployment/oidc/okta.html" basePath="/base" />,
    );

    expect(html).toContain('class="d-nav-sublist"');
    expect(html).toContain('href="/base/deployment/oidc/index.html"');
    expect(html).toContain('href="/base/deployment/oidc/okta.html" aria-current="page"');
  });

  it("names a group with no route so the id is still valid", () => {
    const nav = buildNav([
      model({
        route: "/loose/page.html",
        title: "Loose",
        sourcePath: "docs/loose/page.md",
      }),
    ]);

    expect(markup(<Sidebar nav={nav} activeRoute="" basePath="" />)).toContain(
      'id="d-nav-root"',
    );
  });
});

describe("TableOfContents", () => {
  const headings = [
    { depth: 1, id: "install", text: "Install" },
    { depth: 2, id: "requirements", text: "Requirements" },
    { depth: 3, id: "macos", text: "macOS" },
    { depth: 4, id: "detail", text: "Detail" },
  ];

  it("lists the h2 and h3 headings and skips the rest", () => {
    const html = markup(
      <TableOfContents headings={headings} editUrl="https://edit" issueUrl="https://issue" />,
    );

    expect(html).toContain('data-toc-link="requirements"');
    expect(html).toContain('data-toc-link="macos"');
    expect(html).not.toContain('data-toc-link="install"');
    expect(html).not.toContain('data-toc-link="detail"');
  });

  it("indents each entry by its depth", () => {
    const html = markup(
      <TableOfContents headings={headings} editUrl="https://edit" issueUrl="https://issue" />,
    );

    expect(html).toContain('class="d-toc-item d-toc-item--2"');
    expect(html).toContain('class="d-toc-item d-toc-item--3"');
  });

  it("omits the rail's list for a page with no headings in range", () => {
    const html = markup(
      <TableOfContents headings={[]} editUrl="https://edit" issueUrl="https://issue" />,
    );

    expect(html).not.toContain("On this page");
    expect(html).toContain('href="https://edit"');
    expect(html).toContain('href="https://issue"');
  });
});

describe("Footer", () => {
  it("shows the licence note and the repository slug", () => {
    const html = markup(<Footer config={CONFIG} />);

    expect(html).toContain("MIT licensed");
    expect(html).toContain(">lennylabs/podium</a>");
  });

  it("links the project pages under the base path", () => {
    const html = markup(<Footer config={CONFIG} />);

    expect(html).toContain('href="/base/about/governance.html"');
    expect(html).toContain('href="/base/about/contributing.html"');
    expect(html).toContain('href="/base/about/changelog.html"');
  });

  it("shows a bare repository name when the URL carries no owner", () => {
    const html = markup(<Footer config={{ ...CONFIG, repoUrl: "podium" }} />);

    expect(html).toContain(">podium</a>");
  });
});

describe("Doc", () => {
  const page = model({
    route: "/guide/install.html",
    sectionRoute: "/guide/index.html",
    headings: [{ depth: 2, id: "requirements", text: "Requirements" }],
  });

  it("renders the display title, the summary, and the body", () => {
    const html = markup(
      <Doc config={CONFIG} page={page} nav={NAV} prev={null} next={null}>
        <p>Body prose.</p>
      </Doc>,
    );

    expect(html).toContain('<h1 class="d-title">Installing Podium</h1>');
    expect(html).toContain('<p class="d-lede">Install the fixture tool.</p>');
    expect(html).toContain('<div class="prose"><p>Body prose.</p></div>');
  });

  it("renders the breadcrumb trail down to the current page", () => {
    const html = markup(
      <Doc config={CONFIG} page={page} nav={NAV} prev={null} next={null}>
        <p>Body.</p>
      </Doc>,
    );

    expect(html).toContain('href="/base/guide/index.html"');
    expect(html).toContain('<span class="d-crumb-current" aria-current="page">Install</span>');
  });

  it("renders the previous and next links when the tree has neighbours", () => {
    const html = markup(
      <Doc
        config={CONFIG}
        page={page}
        nav={NAV}
        prev={{ title: "Guide", route: "/guide/index.html" }}
        next={{ title: "Tab strip", route: "/guide/tabs.html" }}
      >
        <p>Body.</p>
      </Doc>,
    );

    expect(html).toContain('rel="prev"');
    expect(html).toContain('rel="next"');
    expect(html).toContain(">Tab strip</span>");
  });

  it("omits the pager on a page with no neighbours", () => {
    const html = markup(
      <Doc config={CONFIG} page={page} nav={NAV} prev={null} next={null}>
        <p>Body.</p>
      </Doc>,
    );

    expect(html).not.toContain("d-pager");
  });

  it("omits the summary for a page that declares none", () => {
    const html = markup(
      <Doc config={CONFIG} page={{ ...page, description: "" }} nav={NAV} prev={null} next={null}>
        <p>Body.</p>
      </Doc>,
    );

    expect(html).not.toContain("d-lede");
  });

  it("prefills a new issue with the page title and its source file", () => {
    const html = markup(
      <Doc config={CONFIG} page={page} nav={NAV} prev={null} next={null}>
        <p>Body.</p>
      </Doc>,
    );

    expect(html).toContain("issues/new?title=Docs%3A%20Install");
    expect(html).toContain("Source%3A%20docs%2Fguide%2Finstall.md");
  });

  it("renders a skip link and a main landmark", () => {
    const html = markup(
      <Doc config={CONFIG} page={page} nav={NAV} prev={null} next={null}>
        <p>Body.</p>
      </Doc>,
    );

    expect(html).toContain('<a class="skip-link" href="#main">Skip to content</a>');
    expect(html).toContain('id="main"');
  });

  it("renders the whole trail for a page nested two sections deep", () => {
    const html = markup(
      <Doc
        config={CONFIG}
        page={model({
          route: "/deployment/oidc/okta.html",
          title: "Okta",
          sourcePath: "docs/deployment/oidc/okta.md",
          sectionRoute: "/deployment/oidc/index.html",
        })}
        nav={NESTED}
        prev={null}
        next={null}
      >
        <p>Body.</p>
      </Doc>,
    );

    expect(html).toContain('href="/base/deployment/index.html"');
    expect(html).toContain('href="/base/deployment/oidc/index.html"');
  });

  it("renders a top-level page with no breadcrumb trail above it", () => {
    const html = markup(
      <Doc
        config={CONFIG}
        page={model({ route: "/overview.html", title: "Overview", sourcePath: "docs/overview.md" })}
        nav={NAV}
        prev={null}
        next={null}
      >
        <p>Body.</p>
      </Doc>,
    );

    expect(html).toContain('<ol class="d-crumb-list">');
    expect(html).not.toContain('class="d-crumb-link"');
  });
});

describe("NotFound", () => {
  it("states what happened and lists the documentation sections", () => {
    const html = markup(<NotFound config={CONFIG} nav={NAV} />);

    expect(html).toContain(">404</p>");
    expect(html).toContain("Page not found");
    expect(html).toContain('href="/base/"');
    expect(html).toContain('href="/base/guide/index.html"');
    expect(html).toContain(">Overview</a>");
  });

  it("keeps the top bar and the footer, so a reader can restart", () => {
    const html = markup(<NotFound config={CONFIG} nav={NAV} />);

    expect(html).toContain("d-topbar");
    expect(html).toContain("d-footer");
  });
});

describe("Landing", () => {
  it("renders the hero, the adapter strip, the feature grid, and the footer", () => {
    const html = markup(<Landing config={CONFIG} />);

    expect(html).toContain("l-topbar");
    expect(html).toContain("l-hero");
    expect(html).toContain("l-features");
    expect(html).toContain("l-footer");
  });

  it("resolves documentation links against the base path", () => {
    const html = markup(<Landing config={CONFIG} />);

    expect(html).toContain('href="/base/');
    expect(html).not.toContain('href="/getting-started');
  });

  it("resolves repository links against the repository URL", () => {
    expect(markup(<Landing config={CONFIG} />)).toContain(
      'href="https://github.com/lennylabs/podium',
    );
  });

  it("shows the version the config carries", () => {
    expect(markup(<Landing config={CONFIG} />)).toContain("1.2.3");
  });

  it("carries the copy hook the client script binds to the install command", () => {
    expect(markup(<Landing config={CONFIG} />)).toContain("data-copy");
  });
});
