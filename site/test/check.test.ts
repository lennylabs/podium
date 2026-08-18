import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";

import type { Root as HastRoot } from "hast";
import { afterEach, describe, expect, it } from "vitest";

import {
  checkContrast,
  contrastRatio,
  readThemes,
  runChecks,
  type CheckInput,
} from "../src/build/check";
import { SITE_DIR } from "../src/build/config";
import { ICONS } from "../src/build/render";
import type { RouteIndex } from "../src/build/content/links";
import { buildNav } from "../src/build/nav";
import type { NavNode, PageModel, SiteConfig } from "../src/build/types";
import { configFor } from "./support/corpus";

const EMPTY_BODY: HastRoot = { type: "root", children: [] };
const TEMP_ROOT = resolve(SITE_DIR, "test/.tmp");

const disposals: Array<() => void> = [];

afterEach(() => {
  while (disposals.length > 0) disposals.pop()?.();
});

function scratchRepo(files: Record<string, string> = {}): string {
  mkdirSync(TEMP_ROOT, { recursive: true });
  const root = mkdtempSync(join(TEMP_ROOT, "checks-"));
  mkdirSync(join(root, "docs"), { recursive: true });
  for (const [name, contents] of Object.entries(files)) {
    writeFileSync(join(root, name), contents);
  }
  disposals.push(() => rmSync(root, { recursive: true, force: true }));
  return root;
}

function page(overrides: Partial<PageModel> & { route: string }): PageModel {
  return {
    sourcePath: `docs${overrides.route.replace(/\.html$/, ".md")}`,
    title: "A page",
    displayTitle: "A page",
    navTitle: null,
    description: "About a page.",
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

/**
 * A route set always carries the browser icons, because every real corpus
 * publishes them from docs/assets/logo. Including them here keeps each test
 * asserting the rule it names instead of also reporting missing icons.
 */
function indexFor(routes: string[]): RouteIndex {
  return {
    routes: new Set([...Object.values(ICONS), ...routes]),
    anchors: new Map(),
    routeBySource: new Map(),
    hidden: new Set(),
  };
}

function inputFor(overrides: Partial<CheckInput> & { config: SiteConfig }): CheckInput {
  return {
    pages: [],
    nav: [] as NavNode[],
    index: indexFor([]),
    searchIndexBytes: 0,
    ...overrides,
  };
}

describe("checkIcons", () => {
  it("passes when every browser icon is published", () => {
    const diagnostics = runChecks(
      inputFor({ config: configFor(scratchRepo(), "docs") }),
    );

    expect(diagnostics).toEqual([]);
  });

  it("fails for a browser icon that no longer exists", () => {
    const index = indexFor([]);
    index.routes.delete(ICONS.dark);

    const diagnostics = runChecks(
      inputFor({ config: configFor(scratchRepo(), "docs"), index }),
    );

    expect(diagnostics).toEqual([
      {
        file: "site/src/build/render.ts",
        line: null,
        column: null,
        message: `the dark browser icon names "docs${ICONS.dark}", which does not exist`,
      },
    ]);
  });
});

describe("contrastRatio", () => {
  const cases: Array<{ foreground: string; background: string; ratio: number }> = [
    { foreground: "#000000", background: "#ffffff", ratio: 21 },
    { foreground: "#ffffff", background: "#ffffff", ratio: 1 },
    { foreground: "#8a7755", background: "#ffffff", ratio: 4.33 },
    { foreground: "#777777", background: "#ffffff", ratio: 4.48 },
  ];

  for (const entry of cases) {
    it(`reports ${entry.foreground} on ${entry.background} as ${entry.ratio}:1`, () => {
      expect(contrastRatio(entry.foreground, entry.background)).toBeCloseTo(
        entry.ratio,
        2,
      );
    });
  }

  it("clears the AA minimum for the light link tone on the page surface", () => {
    expect(contrastRatio("#a8480a", "#fbf9f5")).toBeGreaterThanOrEqual(4.5);
  });

  it("expands a three-digit hex to its six-digit form", () => {
    expect(contrastRatio("#fff", "#000")).toBeCloseTo(21, 5);
  });

  it("reports the same ratio whichever way round the pair is given", () => {
    expect(contrastRatio("#8a7755", "#ffffff")).toBeCloseTo(
      contrastRatio("#ffffff", "#8a7755"),
      10,
    );
  });
});

describe("readThemes", () => {
  it("reads both themes out of the shipped token file", () => {
    const themes = readThemes();

    expect(themes.light.get("--page")).toBe("#fbf9f5");
    expect(themes.dark.get("--page")).toBe("#10131c");
  });

  it("carries a light token the dark block does not redeclare into the dark theme", () => {
    const themes = readThemes();

    expect(themes.dark.get("--r-card")).toBeUndefined();
    expect(themes.dark.get("--accent-deep")).toBe("#f2861a");
  });

  it("keeps only hex values, so a gradient token is not read as a colour", () => {
    expect(readThemes().light.has("--nav-bg")).toBe(false);
  });
});

describe("checkContrast", () => {
  it("passes on the token file the site ships", () => {
    expect(checkContrast()).toEqual([]);
  });
});

describe("checkSearchIndexSize", () => {
  it("passes when the serialized index is inside the limit", () => {
    const config = configFor(scratchRepo(), "docs", { searchIndexLimitBytes: 1024 });

    expect(runChecks(inputFor({ config, searchIndexBytes: 1024 }))).toEqual([]);
  });

  it("fails when the serialized index is over the limit", () => {
    const config = configFor(scratchRepo(), "docs", {
      searchIndexLimitBytes: 600 * 1024,
    });

    const diagnostics = runChecks(
      inputFor({ config, searchIndexBytes: 700 * 1024 }),
    );

    expect(diagnostics).toEqual([
      {
        file: "site/src/build/search.ts",
        line: null,
        column: null,
        message:
          "the search index is 700kB, over the 600kB limit. Split it per section, or raise the limit deliberately",
      },
    ]);
  });
});

describe("checkOrphans", () => {
  const pages = [
    page({ route: "/guide/index.html", title: "Guide", sourcePath: "docs/guide/index.md" }),
    page({
      route: "/guide/install.html",
      title: "Install",
      sourcePath: "docs/guide/install.md",
    }),
  ];

  it("passes when every published page is reachable from the navigation tree", () => {
    const config = configFor(scratchRepo(), "docs");
    const diagnostics = runChecks(
      inputFor({ config, pages, nav: buildNav(pages) }),
    );

    expect(diagnostics).toEqual([]);
  });

  it("fails for a published page no navigation entry reaches", () => {
    const config = configFor(scratchRepo(), "docs");
    const orphan = page({
      route: "/guide/stray.html",
      title: "Stray",
      sourcePath: "docs/guide/stray.md",
    });

    const diagnostics = runChecks(
      inputFor({ config, pages: [...pages, orphan], nav: buildNav(pages) }),
    );

    expect(diagnostics).toEqual([
      {
        file: "docs/guide/stray.md",
        line: 1,
        column: 1,
        message:
          'page is published at "/guide/stray.html" but no navigation entry reaches it. Set "hidden: true" if that is deliberate',
      },
    ]);
  });

  it("passes for an unreachable page that declares itself hidden", () => {
    const config = configFor(scratchRepo(), "docs");
    const hidden = page({
      route: "/guide/stray.html",
      sourcePath: "docs/guide/stray.md",
      hidden: true,
    });

    expect(
      runChecks(inputFor({ config, pages: [...pages, hidden], nav: buildNav(pages) })),
    ).toEqual([]);
  });
});

describe("checkSiteOriginLinks", () => {
  const routes = ["/index.html", "/guide/index.html", "/guide/install.html"];

  function checkReadme(body: string): ReturnType<typeof runChecks> {
    const repoRoot = scratchRepo({ "README.md": body });
    const config = configFor(repoRoot, join(repoRoot, "docs"), {
      siteUrl: "https://example.test",
      basePath: "/base",
    });
    return runChecks(inputFor({ config, index: indexFor(routes) }));
  }

  const accepted: Array<{ name: string; url: string }> = [
    { name: "an extensionless path", url: "https://example.test/base/guide/install" },
    { name: "an explicit .html path", url: "https://example.test/base/guide/install.html" },
    { name: "a directory path", url: "https://example.test/base/guide/" },
    { name: "the site root with a trailing slash", url: "https://example.test/base/" },
    { name: "the site root with no trailing slash", url: "https://example.test/base" },
    {
      name: "a path carrying a fragment",
      url: "https://example.test/base/guide/install#requirements",
    },
  ];

  for (const entry of accepted) {
    it(`accepts ${entry.name}`, () => {
      expect(checkReadme(`Read [the docs](${entry.url}).\n`)).toEqual([]);
    });
  }

  it("rejects a trailing-slash URL that matches no published route", () => {
    const diagnostics = checkReadme("Read [the guide](https://example.test/base/manual/).\n");

    expect(diagnostics).toEqual([
      {
        file: "README.md",
        line: 1,
        column: 18,
        message: '"https://example.test/base/manual/" matches no published route',
      },
    ]);
  });

  it("rejects an extensionless URL that matches no published route", () => {
    expect(
      checkReadme("See https://example.test/base/guide/uninstall for details.\n")[0]
        ?.message,
    ).toBe('"https://example.test/base/guide/uninstall" matches no published route');
  });

  it("reports the line each bad URL was written on", () => {
    const diagnostics = checkReadme(
      "# Podium\n\nOne: https://example.test/base/a\n\nTwo: https://example.test/base/b\n",
    );

    expect(diagnostics.map((diagnostic) => diagnostic.line)).toEqual([3, 5]);
  });

  it("ignores trailing sentence punctuation when resolving a URL", () => {
    expect(checkReadme("Read https://example.test/base/guide/install.\n")).toEqual([]);
  });

  it("ignores a URL pointing at another origin", () => {
    expect(checkReadme("See https://example.com/base/manual/ for details.\n")).toEqual([]);
  });

  it("checks CONTRIBUTING.md alongside README.md", () => {
    const repoRoot = scratchRepo({
      "CONTRIBUTING.md": "See https://example.test/base/manual/.\n",
    });
    const config = configFor(repoRoot, join(repoRoot, "docs"), {
      siteUrl: "https://example.test",
      basePath: "/base",
    });

    expect(
      runChecks(inputFor({ config, index: indexFor(routes) }))[0]?.file,
    ).toBe("CONTRIBUTING.md");
  });

  it("passes when neither file exists", () => {
    const repoRoot = scratchRepo();
    const config = configFor(repoRoot, join(repoRoot, "docs"), {
      siteUrl: "https://example.test",
      basePath: "/base",
    });

    expect(runChecks(inputFor({ config, index: indexFor(routes) }))).toEqual([]);
  });
});
