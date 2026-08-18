import { describe, expect, it } from "vitest";

import {
  isExternal,
  resolveLink,
  routeForSource,
  type LinkContext,
  type RouteIndex,
} from "../src/build/content/links";
import type { BuildDiagnostic } from "../src/build/types";

function makeIndex(): RouteIndex {
  const routeBySource = new Map<string, string>([
    ["index.md", "/index.html"],
    ["overview.md", "/overview.html"],
    ["guide/index.md", "/guide/index.html"],
    ["guide/install.md", "/guide/install.html"],
    ["reference/cli.md", "/reference/cli.html"],
  ]);

  return {
    routeBySource,
    routes: new Set([...routeBySource.values(), "/assets/diagrams/sample.svg"]),
    anchors: new Map([
      ["/guide/install.html", new Set(["requirements", "steps"])],
      ["/overview.html", new Set(["sections"])],
    ]),
    hidden: new Set<string>(),
  };
}

function contextFor(fromSource: string, fromRoute: string): LinkContext {
  return { fromSource, fromRoute, file: `docs/${fromSource}`, line: 12, column: 3 };
}

function resolve(
  href: string,
  options: { from?: [string, string]; basePath?: string } = {},
): { url: string | null; diagnostics: BuildDiagnostic[] } {
  const diagnostics: BuildDiagnostic[] = [];
  const [fromSource, fromRoute] = options.from ?? ["overview.md", "/overview.html"];
  const url = resolveLink(
    href,
    makeIndex(),
    contextFor(fromSource, fromRoute),
    options.basePath ?? "",
    diagnostics,
  );
  return { url, diagnostics };
}

describe("isExternal", () => {
  const cases: Array<[string, boolean]> = [
    ["https://example.com/a", true],
    ["http://example.com", true],
    ["mailto:alice@acme.com", true],
    ["//cdn.example.com/a.js", true],
    ["guide/install", false],
    ["/guide/install.html", false],
    ["#requirements", false],
  ];

  for (const [href, expected] of cases) {
    it(`treats "${href}" as ${expected ? "external" : "internal"}`, () => {
      expect(isExternal(href)).toBe(expected);
    });
  }
});

describe("routeForSource", () => {
  it("maps a docs-relative source to its published route", () => {
    expect(routeForSource("guide/install.md", makeIndex().routeBySource)).toBe(
      "/guide/install.html",
    );
  });

  it("returns null for a source that publishes nothing", () => {
    expect(routeForSource("guide/missing.md", makeIndex().routeBySource)).toBeNull();
  });
});

describe("resolveLink", () => {
  const accepted: Array<{ name: string; href: string; expected: string }> = [
    {
      name: "resolves an extensionless relative path",
      href: "guide/install",
      expected: "/guide/install.html",
    },
    {
      name: "resolves an explicit .md path",
      href: "reference/cli.md",
      expected: "/reference/cli.html",
    },
    {
      name: "resolves a directory path ending in a slash to its index page",
      href: "guide/",
      expected: "/guide/index.html",
    },
    {
      name: "resolves an extensionless path naming a directory to its index page",
      href: "guide",
      expected: "/guide/index.html",
    },
    {
      name: "resolves a path rooted at the documentation tree",
      href: "/reference/cli.md",
      expected: "/reference/cli.html",
    },
    {
      name: "resolves a path with another extension to the static file it names",
      href: "assets/diagrams/sample.svg",
      expected: "/assets/diagrams/sample.svg",
    },
  ];

  for (const entry of accepted) {
    it(entry.name, () => {
      const { url, diagnostics } = resolve(entry.href);

      expect(url).toBe(entry.expected);
      expect(diagnostics).toEqual([]);
    });
  }

  it("resolves a path relative to the directory the link was written in", () => {
    const { url } = resolve("../reference/cli", {
      from: ["guide/install.md", "/guide/install.html"],
    });

    expect(url).toBe("/reference/cli.html");
  });

  it("resolves a link to the current directory to the corpus index page", () => {
    const { url } = resolve(".");

    expect(url).toBe("/index.html");
  });

  it("keeps a same-page anchor that matches a heading", () => {
    const { url, diagnostics } = resolve("#sections");

    expect(url).toBe("#sections");
    expect(diagnostics).toEqual([]);
  });

  it("rejects a same-page anchor that matches no heading", () => {
    const { url, diagnostics } = resolve("#missing-heading");

    expect(url).toBeNull();
    expect(diagnostics).toEqual([
      {
        file: "docs/overview.md",
        line: 12,
        column: 3,
        message: 'link to "#missing-heading" matches no heading on this page',
      },
    ]);
  });

  it("keeps an anchor on another page that matches a heading there", () => {
    const { url, diagnostics } = resolve("guide/install#requirements");

    expect(url).toBe("/guide/install.html#requirements");
    expect(diagnostics).toEqual([]);
  });

  it("rejects an anchor on another page that matches no heading there", () => {
    const { url, diagnostics } = resolve("guide/install#missing");

    expect(url).toBeNull();
    expect(diagnostics[0]?.message).toBe(
      'link "guide/install#missing" points at "#missing", which matches no heading on that page',
    );
  });

  it("rejects a path that resolves to nothing published", () => {
    const { url, diagnostics } = resolve("guide/uninstall");

    expect(url).toBeNull();
    expect(diagnostics).toEqual([
      {
        file: "docs/overview.md",
        line: 12,
        column: 3,
        message:
          'link "guide/uninstall" resolves to "guide/uninstall", which is not published',
      },
    ]);
  });

  it("rejects an asset path that names no published file", () => {
    const { url, diagnostics } = resolve("assets/diagrams/missing.svg");

    expect(url).toBeNull();
    expect(diagnostics[0]?.message).toContain("which is not published");
  });

  it("rejects a path escaping the documentation tree", () => {
    const { url, diagnostics } = resolve("../../README.md", {
      from: ["guide/install.md", "/guide/install.html"],
    });

    expect(url).toBeNull();
    expect(diagnostics).toEqual([
      {
        file: "docs/guide/install.md",
        line: 12,
        column: 3,
        message: 'link "../../README.md" points outside the documentation tree',
      },
    ]);
  });

  it("passes an external URL through untouched", () => {
    const { url, diagnostics } = resolve("https://example.com/a?b=c#d");

    expect(url).toBe("https://example.com/a?b=c#d");
    expect(diagnostics).toEqual([]);
  });

  it("returns null for an empty href without reporting a problem", () => {
    const { url, diagnostics } = resolve("");

    expect(url).toBeNull();
    expect(diagnostics).toEqual([]);
  });

  it("prefixes a resolved route with the site base path", () => {
    const { url } = resolve("guide/install", { basePath: "/podium" });

    expect(url).toBe("/podium/guide/install.html");
  });

  it("prefixes a resolved route with the base path and keeps the anchor", () => {
    const { url } = resolve("guide/install#steps", { basePath: "/podium" });

    expect(url).toBe("/podium/guide/install.html#steps");
  });

  it("leaves an anchor unchecked when the target page records no headings", () => {
    const { url, diagnostics } = resolve("reference/cli#anything");

    expect(url).toBe("/reference/cli.html#anything");
    expect(diagnostics).toEqual([]);
  });
});
