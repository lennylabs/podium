import { join } from "node:path";

import type { Element, Root as HastRoot } from "hast";
import { afterAll, beforeAll, describe, expect, it } from "vitest";

import { disposeHighlighter } from "../src/build/content/highlight";
import { buildCorpus, type BuiltCorpus } from "../src/build/content/pipeline";
import { buildNav, flattenNav } from "../src/build/nav";
import { buildSearchIndex } from "../src/build/search";
import {
  defineIsland,
  type IslandRegistry,
} from "../src/components/islands/props";
import { registry } from "../src/components/islands/registry";
import type { PageModel, SiteConfig } from "../src/build/types";
import { FIXTURE_CORPUS, configFor, makeCorpus } from "./support/corpus";
import { textContent } from "./support/directives";

const BASE = "/base";

/**
 * A registry exercising the prop kinds the shipped entries do not use: a link
 * into the corpus, and a file named relative to the page rather than to a
 * declared directory.
 */
const LINKED: IslandRegistry = {
  linkcard: defineIsland<{ target: string; picture: string }>({
    props: {
      target: { kind: "route", required: true },
      picture: { kind: "asset" },
    },
    children: { kind: "none" },
    fallback: { from: "prop", name: "target" },
  }),
};

function elements(tree: HastRoot | Element): Element[] {
  const found: Element[] = [];
  const walk = (node: { children?: unknown[] }): void => {
    for (const child of (node.children ?? []) as Array<{
      type?: string;
      children?: unknown[];
    }>) {
      if (child.type === "element") found.push(child as Element);
      walk(child);
    }
  };
  walk(tree);
  return found;
}

function pageAt(corpus: BuiltCorpus, route: string): PageModel {
  const page = corpus.pages.find((candidate) => candidate.route === route);
  if (page === undefined) throw new Error(`the corpus published no page at ${route}`);
  return page;
}

describe("buildCorpus over the fixture corpus", () => {
  let config: SiteConfig;
  let corpus: BuiltCorpus;

  beforeAll(async () => {
    // The fixture corpus is its own root, so a source path reads the way a
    // docs-relative path reads during a real build.
    config = configFor(FIXTURE_CORPUS, FIXTURE_CORPUS, { basePath: BASE });
    corpus = await buildCorpus(config, registry);
  });

  afterAll(() => {
    disposeHighlighter();
  });

  it("reports no problems", () => {
    expect(corpus.diagnostics).toEqual([]);
  });

  it("maps every markdown page carrying frontmatter to a route", () => {
    expect(corpus.pages.map((page) => page.route).sort()).toEqual([
      "/guide/hidden.html",
      "/guide/index.html",
      "/guide/install.html",
      "/guide/tabs.html",
      "/overview.html",
      "/reference/cli.html",
      "/reference/index.html",
    ]);
  });

  it("publishes a markdown file with no frontmatter verbatim at its source path", () => {
    expect(corpus.statics).toContainEqual({
      route: "/notes.md",
      sourcePath: "notes.md",
    });
  });

  it("publishes a non-markdown file as a static file", () => {
    expect(corpus.statics).toContainEqual({
      route: "/assets/diagrams/sample.svg",
      sourcePath: "assets/diagrams/sample.svg",
    });
  });

  it("adds the routes the build emits rather than discovers", () => {
    expect(corpus.index.routes.has("/index.html")).toBe(true);
    expect(corpus.index.routes.has("/404.html")).toBe(true);
  });

  it("records the hidden page as published and hidden", () => {
    expect(corpus.index.hidden).toEqual(new Set(["/guide/hidden.html"]));
    expect(pageAt(corpus, "/guide/hidden.html").hidden).toBe(true);
  });

  it("carries the frontmatter onto the page model", () => {
    expect(pageAt(corpus, "/guide/install.html")).toMatchObject({
      sourcePath: "guide/install.md",
      title: "Install",
      navTitle: "Installing",
      description: "Install the fixture tool.",
      navOrder: 1,
      hidden: false,
      sectionRoute: "/guide/index.html",
      editUrl: `${config.editBase}/guide/install.md`,
    });
  });

  it("gives a top-level page and a section index no section of their own", () => {
    expect(pageAt(corpus, "/overview.html").sectionRoute).toBeNull();
    expect(pageAt(corpus, "/guide/index.html").sectionRoute).toBeNull();
  });

  it("resolves the frontmatter actions and marks the first as primary", () => {
    expect(pageAt(corpus, "/overview.html").actions).toEqual([
      {
        label: "Install",
        href: "/base/guide/install.html",
        variant: "primary",
        external: false,
      },
      {
        label: "Source",
        href: "https://example.com/source",
        variant: "outline",
        external: true,
      },
    ]);
  });

  it("collects the heading outline with the ids the anchors use", () => {
    expect(pageAt(corpus, "/reference/cli.html").headings).toEqual([
      { depth: 2, id: "podium-sync", text: "podium sync" },
      { depth: 2, id: "podium-check", text: "podium check" },
    ]);
  });

  it("records every heading id against the page route for anchor checking", () => {
    expect(corpus.index.anchors.get("/guide/install.html")).toContain("requirements");
  });

  it("takes the display title from the body's opening heading and removes it", () => {
    const install = pageAt(corpus, "/guide/install.html");

    expect(install.displayTitle).toBe("Install");
    expect(elements(install.body).some((node) => node.tagName === "h1")).toBe(false);
  });

  it("falls back to the frontmatter title for a body with no opening heading", async () => {
    const corpus = makeCorpus({
      "a.md": "---\ntitle: A\ndescription: B\n---\n\nProse with no heading.\n",
    });

    try {
      const built = await buildCorpus(corpus.config, registry);

      expect(built.pages[0]?.displayTitle).toBe("A");
    } finally {
      corpus.dispose();
    }
  });

  it("rewrites each accepted link form to its published URL under the base path", () => {
    const hrefs = elements(pageAt(corpus, "/overview.html").body)
      .filter((node) => node.tagName === "a")
      .map((node) => node.properties?.["href"])
      .filter((href) => href !== undefined && String(href).startsWith("/base"));

    expect(hrefs).toEqual([
      "/base/guide/index.html",
      "/base/reference/cli.html",
      "/base/guide/install.html#requirements",
      "/base/reference/index.html",
      "/base/guide/install.html",
    ]);
  });

  it("keeps prose that the parser reads as an inline directive", () => {
    const rendered = textContent(pageAt(corpus, "/overview.html").body);

    expect(rendered).toContain("Freeze windows run from Friday 17:00 to Monday 09:00");
    expect(rendered).toContain("the read CLI maps 1:1\nto the SDK");
  });

  it("renames an alert blockquote to a callout carrying its type", () => {
    const callout = elements(pageAt(corpus, "/guide/install.html").body).find(
      (node) => node.tagName === "podium-callout",
    );

    expect(callout?.properties?.["type"]).toBe("note");
  });

  it("highlights a fenced block and labels it with its language", () => {
    const pre = elements(pageAt(corpus, "/guide/install.html").body).find(
      (node) => node.tagName === "pre",
    );

    expect(pre?.properties).toMatchObject({
      "data-language": "bash",
      "data-highlighted": "true",
    });
  });

  it("renders a diagram with no animated variant as the checked-in SVG", () => {
    const images = elements(pageAt(corpus, "/guide/install.html").body).filter(
      (node) => node.tagName === "img",
    );

    expect(images.map((node) => node.properties?.["src"])).toEqual([
      "/base/assets/diagrams/sample.svg",
      "/base/assets/diagrams/sample.svg",
    ]);
    expect(images[1]?.properties?.["alt"]).toBe("A sample fixture diagram");
    expect(pageAt(corpus, "/guide/install.html").islands).toEqual([]);
  });

  it("declares one hydrating island for a tabs container", () => {
    expect(pageAt(corpus, "/guide/tabs.html").islands).toEqual([
      { id: "island-0", component: "tabs", mount: "hydrate", props: {} },
    ]);
  });

  it("renders each tab as a child marker carrying its label", () => {
    const markers = elements(pageAt(corpus, "/guide/tabs.html").body).filter(
      (node) => node.properties?.["data-child-directive"] === "tab",
    );

    expect(markers.map((node) => node.properties?.["data-child-props"])).toEqual([
      '{"label":"npm"}',
      '{"label":"Homebrew"}',
    ]);
  });

  it("excludes island markup from the text kept for the search index", () => {
    const text = pageAt(corpus, "/guide/tabs.html").text;

    expect(text).toContain("Pick the channel that matches the machine.");
    expect(text).not.toContain("Install with npm");
  });

  it("excludes fenced code from the text kept for the search index", () => {
    expect(pageAt(corpus, "/guide/install.html").text).not.toContain("podium sync");
  });

  it("derives the navigation tree from the directory layout", () => {
    const nav = buildNav(corpus.pages);

    expect(nav.map((node) => node.title)).toEqual(["Overview", "Guide", "Reference"]);
    expect(nav[1]?.children.map((node) => node.title)).toEqual(["Installing", "Tab strip"]);
    expect(flattenNav(nav).map((entry) => entry.route)).toEqual([
      "/overview.html",
      "/guide/index.html",
      "/guide/install.html",
      "/guide/tabs.html",
      "/reference/index.html",
      "/reference/cli.html",
    ]);
  });

  it("indexes every visible page and its sections for search", () => {
    const sectionTitles = new Map(corpus.pages.map((page) => [page.route, page.title]));
    const index = buildSearchIndex(corpus.pages, sectionTitles);

    expect(index.documentCount).toBeGreaterThan(corpus.pages.length - 1);
    expect(index.serialized).not.toContain("Hidden note");
  });
});

describe("buildCorpus failure reporting", () => {
  it("reports a page whose body links to nothing published", async () => {
    const corpus = makeCorpus({
      "overview.md": "---\ntitle: A\ndescription: B\n---\n\n# A\n\n[Gone](guide/gone)\n",
    });

    try {
      const built = await buildCorpus(corpus.config, registry);

      expect(built.diagnostics).toEqual([
        {
          file: "docs/overview.md",
          line: 8,
          column: 1,
          message: 'link "guide/gone" resolves to "guide/gone", which is not published',
        },
      ]);
    } finally {
      corpus.dispose();
    }
  });

  it("reports two pages competing for one route", async () => {
    const corpus = makeCorpus({
      "a.md": "---\ntitle: A\ndescription: B\npermalink: /shared.html\n---\n\n# A\n",
      "b.md": "---\ntitle: B\ndescription: C\npermalink: /shared.html\n---\n\n# B\n",
    });

    try {
      const built = await buildCorpus(corpus.config, registry);

      expect(built.diagnostics).toEqual([
        {
          file: "docs/b.md",
          line: 1,
          column: 1,
          message: 'route "/shared.html" is already published by docs/a.md',
        },
      ]);
    } finally {
      corpus.dispose();
    }
  });

  it("reports an image with no alt text", async () => {
    const corpus = makeCorpus({
      "a.md": "---\ntitle: A\ndescription: B\n---\n\n# A\n\n![](x.svg)\n",
      "x.svg": "<svg xmlns='http://www.w3.org/2000/svg'/>",
    });

    try {
      const built = await buildCorpus(corpus.config, registry);

      expect(built.diagnostics.map((diagnostic) => diagnostic.message)).toEqual([
        'image "/x.svg" has no alt text',
      ]);
    } finally {
      corpus.dispose();
    }
  });

  it("reports a heading outline that skips a level", async () => {
    const corpus = makeCorpus({
      "a.md": "---\ntitle: A\ndescription: B\n---\n\n# A\n\n### Deep\n",
    });

    try {
      const built = await buildCorpus(corpus.config, registry);

      expect(built.diagnostics[0]?.message).toBe(
        'heading "Deep" is an h3 directly below an h1, which skips a level',
      );
    } finally {
      corpus.dispose();
    }
  });

  it("reports a frontmatter action that resolves to nothing published", async () => {
    const corpus = makeCorpus({
      "a.md":
        "---\ntitle: A\ndescription: B\nactions:\n  - label: Gone\n    href: nowhere\n---\n\n# A\n",
    });

    try {
      const built = await buildCorpus(corpus.config, registry);

      expect(built.diagnostics[0]?.message).toContain('link "nowhere" resolves to');
      expect(built.pages[0]?.actions).toEqual([]);
    } finally {
      corpus.dispose();
    }
  });

  it("keeps a page out of the corpus when its frontmatter is rejected", async () => {
    const corpus = makeCorpus({
      "a.md": "---\ntitle: A\n---\n\n# A\n",
      "b.md": "---\ntitle: B\ndescription: C\n---\n\n# B\n",
    });

    try {
      const built = await buildCorpus(corpus.config, registry);

      expect(built.pages.map((page) => page.route)).toEqual(["/b.html"]);
      expect(built.diagnostics).toHaveLength(1);
    } finally {
      corpus.dispose();
    }
  });

  it("publishes a permalink at the route it names", async () => {
    const corpus = makeCorpus({
      "a.md": "---\ntitle: A\ndescription: B\npermalink: /\n---\n\n# A\n",
      "guide/b.md":
        "---\ntitle: B\ndescription: C\npermalink: /guide/\n---\n\n# B\n",
      "guide/c.md":
        "---\ntitle: C\ndescription: D\npermalink: /named.html\n---\n\n# C\n",
    });

    try {
      const built = await buildCorpus(corpus.config, registry);

      expect(built.pages.map((page) => page.route).sort()).toEqual([
        "/guide/index.html",
        "/index.html",
        "/named.html",
      ]);
    } finally {
      corpus.dispose();
    }
  });

  it("skips a dotfile and an underscore-prefixed entry", async () => {
    const corpus = makeCorpus({
      "a.md": "---\ntitle: A\ndescription: B\n---\n\n# A\n",
      ".hidden.md": "---\ntitle: Dot\ndescription: B\n---\n\n# Dot\n",
      "_drafts/b.md": "---\ntitle: Draft\ndescription: B\n---\n\n# Draft\n",
    });

    try {
      const built = await buildCorpus(corpus.config, registry);

      expect(built.pages.map((page) => page.route)).toEqual(["/a.html"]);
      expect(built.statics).toEqual([]);
    } finally {
      corpus.dispose();
    }
  });

  it("resolves a route prop and an asset prop written beside the page", async () => {
    const corpus = makeCorpus({
      "a.md":
        '---\ntitle: A\ndescription: B\n---\n\n# A\n\n::linkcard{target="b" picture="pic.svg"}\n',
      "b.md": "---\ntitle: B\ndescription: C\n---\n\n# B\n",
      "pic.svg": "<svg xmlns='http://www.w3.org/2000/svg'/>",
    });

    try {
      const built = await buildCorpus(corpus.config, LINKED);

      expect(built.diagnostics).toEqual([]);
      expect(built.pages[0]?.islands).toEqual([
        {
          id: "island-0",
          component: "linkcard",
          mount: "replace",
          props: { target: "/b.html", picture: "/pic.svg" },
        },
      ]);
    } finally {
      corpus.dispose();
    }
  });

  it("reports a route prop that resolves to nothing published", async () => {
    const corpus = makeCorpus({
      "a.md": '---\ntitle: A\ndescription: B\n---\n\n# A\n\n::linkcard{target="gone"}\n',
    });

    try {
      const built = await buildCorpus(corpus.config, LINKED);

      expect(built.diagnostics).toEqual([
        {
          file: "docs/a.md",
          line: 8,
          column: 1,
          message: 'island "linkcard" prop "target" does not resolve to a page: "gone"',
        },
      ]);
    } finally {
      corpus.dispose();
    }
  });

  it("reports an asset prop naming a file that is not beside the page", async () => {
    const corpus = makeCorpus({
      "a.md":
        '---\ntitle: A\ndescription: B\n---\n\n# A\n\n::linkcard{target="a" picture="gone.svg"}\n',
    });

    try {
      const built = await buildCorpus(corpus.config, LINKED);

      expect(built.diagnostics[0]?.message).toBe(
        'island "linkcard" prop "picture" does not name a file that exists: "gone.svg"',
      );
    } finally {
      corpus.dispose();
    }
  });

  it("leaves the out directory alone, which the writer creates later", async () => {
    const corpus = makeCorpus({
      "a.md": "---\ntitle: A\ndescription: B\n---\n\n# A\n",
    });

    try {
      const built = await buildCorpus(corpus.config, registry);

      expect(built.pages[0]?.editUrl).toBe(`${corpus.config.editBase}/docs/a.md`);
      expect(corpus.config.outDir).toBe(join(corpus.repoRoot, "out"));
    } finally {
      corpus.dispose();
    }
  });
});
