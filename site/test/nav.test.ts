import type { Root as HastRoot } from "hast";
import { describe, expect, it } from "vitest";

import { buildNav, flattenNav, neighbours } from "../src/build/nav";
import type { PageModel } from "../src/build/types";

type PageOverrides = Partial<PageModel> & { sourcePath: string; title: string };

const EMPTY_BODY: HastRoot = { type: "root", children: [] };

function page(overrides: PageOverrides): PageModel {
  const source = overrides.sourcePath.replace(/^docs\//, "");
  return {
    route: `/${source.replace(/\.md$/, ".html")}`,
    displayTitle: overrides.title,
    navTitle: null,
    description: `About ${overrides.title}.`,
    navOrder: null,
    hidden: false,
    sectionRoute: null,
    headings: [],
    actions: [],
    body: EMPTY_BODY,
    islands: [],
    editUrl: "",
    updatedAt: "",
    text: "",
    ...overrides,
  };
}

function titles(pages: PageModel[]): string[] {
  return buildNav(pages).map((node) => node.title);
}

describe("buildNav", () => {
  it("orders siblings by nav_order", () => {
    const nav = buildNav([
      page({ sourcePath: "docs/c.md", title: "C", navOrder: 3 }),
      page({ sourcePath: "docs/a.md", title: "A", navOrder: 1 }),
      page({ sourcePath: "docs/b.md", title: "B", navOrder: 2 }),
    ]);

    expect(nav.map((node) => node.title)).toEqual(["A", "B", "C"]);
  });

  it("sorts a page without nav_order after every ordered page", () => {
    expect(
      titles([
        page({ sourcePath: "docs/aardvark.md", title: "Aardvark" }),
        page({ sourcePath: "docs/zebra.md", title: "Zebra", navOrder: 9 }),
      ]),
    ).toEqual(["Zebra", "Aardvark"]);
  });

  it("falls back to the title when two pages share a nav_order", () => {
    expect(
      titles([
        page({ sourcePath: "docs/beta.md", title: "Beta", navOrder: 2 }),
        page({ sourcePath: "docs/alpha.md", title: "Alpha", navOrder: 2 }),
      ]),
    ).toEqual(["Alpha", "Beta"]);
  });

  it("orders two unordered pages by title", () => {
    expect(
      titles([
        page({ sourcePath: "docs/beta.md", title: "Beta" }),
        page({ sourcePath: "docs/alpha.md", title: "Alpha" }),
      ]),
    ).toEqual(["Alpha", "Beta"]);
  });

  it("labels a node with nav_title when the page sets one", () => {
    expect(
      titles([page({ sourcePath: "docs/a.md", title: "Install", navTitle: "Installing" })]),
    ).toEqual(["Installing"]);
  });

  it("builds a section from a directory's index page", () => {
    const nav = buildNav([
      page({ sourcePath: "docs/guide/index.md", title: "Guide", navOrder: 1 }),
      page({ sourcePath: "docs/guide/install.md", title: "Install", navOrder: 1 }),
      page({ sourcePath: "docs/guide/upgrade.md", title: "Upgrade", navOrder: 2 }),
    ]);

    expect(nav).toHaveLength(1);
    expect(nav[0]).toMatchObject({
      title: "Guide",
      route: "/guide/index.html",
      navOrder: 1,
    });
    expect(nav[0]?.children.map((child) => child.title)).toEqual(["Install", "Upgrade"]);
  });

  it("nests a section inside the section that holds it", () => {
    const nav = buildNav([
      page({ sourcePath: "docs/deployment/index.md", title: "Deployment", navOrder: 1 }),
      page({ sourcePath: "docs/deployment/oidc/index.md", title: "OIDC", navOrder: 2 }),
      page({ sourcePath: "docs/deployment/oidc/okta.md", title: "Okta", navOrder: 1 }),
    ]);

    expect(nav[0]?.children.map((child) => child.title)).toEqual(["OIDC"]);
    expect(nav[0]?.children[0]?.children.map((child) => child.title)).toEqual(["Okta"]);
  });

  it("labels a directory with no index page from its directory name", () => {
    const nav = buildNav([
      page({ sourcePath: "docs/getting-started/quickstart.md", title: "Quickstart" }),
    ]);

    expect(nav[0]).toMatchObject({
      title: "Getting Started",
      route: "",
      navOrder: null,
    });
    expect(nav[0]?.children.map((child) => child.title)).toEqual(["Quickstart"]);
  });

  it("excludes a hidden page from the tree", () => {
    const nav = buildNav([
      page({ sourcePath: "docs/guide/index.md", title: "Guide" }),
      page({ sourcePath: "docs/guide/install.md", title: "Install" }),
      page({ sourcePath: "docs/guide/secret.md", title: "Secret", hidden: true }),
    ]);

    expect(nav[0]?.children.map((child) => child.title)).toEqual(["Install"]);
  });

  it("drops a directory whose only pages are hidden", () => {
    expect(
      buildNav([
        page({ sourcePath: "docs/overview.md", title: "Overview" }),
        page({ sourcePath: "docs/internal/draft.md", title: "Draft", hidden: true }),
      ]).map((node) => node.title),
    ).toEqual(["Overview"]);
  });
});

describe("flattenNav", () => {
  it("returns the tree in reading order and omits nodes with no route", () => {
    const nav = buildNav([
      page({ sourcePath: "docs/overview.md", title: "Overview", navOrder: 0 }),
      page({ sourcePath: "docs/guide/index.md", title: "Guide", navOrder: 1 }),
      page({ sourcePath: "docs/guide/install.md", title: "Install", navOrder: 1 }),
      page({ sourcePath: "docs/loose/page.md", title: "Loose", navOrder: 2 }),
    ]);

    expect(flattenNav(nav)).toEqual([
      { title: "Overview", route: "/overview.html" },
      { title: "Guide", route: "/guide/index.html" },
      { title: "Install", route: "/guide/install.html" },
      { title: "Loose", route: "/loose/page.html" },
    ]);
  });
});

describe("neighbours", () => {
  const nav = buildNav([
    page({ sourcePath: "docs/overview.md", title: "Overview", navOrder: 0 }),
    page({ sourcePath: "docs/guide/index.md", title: "Guide", navOrder: 1 }),
    page({ sourcePath: "docs/guide/install.md", title: "Install", navOrder: 1 }),
    page({ sourcePath: "docs/guide/upgrade.md", title: "Upgrade", navOrder: 2 }),
  ]);

  it("returns the pages either side of a route in reading order", () => {
    expect(neighbours(nav, "/guide/install.html")).toEqual({
      prev: { title: "Guide", route: "/guide/index.html" },
      next: { title: "Upgrade", route: "/guide/upgrade.html" },
    });
  });

  it("returns no previous page for the first entry", () => {
    expect(neighbours(nav, "/overview.html").prev).toBeNull();
  });

  it("returns no next page for the last entry", () => {
    expect(neighbours(nav, "/guide/upgrade.html").next).toBeNull();
  });

  it("returns neither for a route the tree does not reach", () => {
    expect(neighbours(nav, "/guide/hidden.html")).toEqual({ prev: null, next: null });
  });
});
