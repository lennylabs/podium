import type { Root as HastRoot } from "hast";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { afterAll, beforeAll, describe, expect, it } from "vitest";

import { loadConfig } from "../src/build/config";
import { disposeHighlighter } from "../src/build/content/highlight";
import { buildCorpus, type BuiltCorpus } from "../src/build/content/pipeline";
import {
  escapeHtml,
  renderBody,
  renderDocument,
  resolveIslandComponents,
} from "../src/build/render";
import { ActionLinks } from "../src/components/content/ActionLinks";
import { Callout } from "../src/components/content/Callout";
import { CodeBlock } from "../src/components/content/CodeBlock";
import { DiagramIsland } from "../src/components/content/DiagramIsland";
import { Tabs } from "../src/components/content/Tabs";
import { registry } from "../src/components/islands/registry";
import type { PageModel } from "../src/build/types";
import { FIXTURE_CORPUS, configFor } from "./support/corpus";

const CONFIG = loadConfig({
  repoRoot: FIXTURE_CORPUS,
  docsDir: FIXTURE_CORPUS,
  siteUrl: "https://example.test",
  basePath: "/base",
  version: "0.0.0-test",
});

function markup(element: Parameters<typeof renderToStaticMarkup>[0]): string {
  return renderToStaticMarkup(element);
}

describe("Callout", () => {
  const types = ["note", "tip", "important", "warning", "caution"] as const;

  for (const type of types) {
    it(`renders a ${type} callout with its label`, () => {
      const html = markup(
        <Callout type={type}>
          <p>Body text.</p>
        </Callout>,
      );

      expect(html).toContain(`class="callout callout-${type}"`);
      expect(html).toContain('role="note"');
      expect(html).toContain(
        `<p class="callout-label">${type[0]!.toUpperCase()}${type.slice(1)}</p>`,
      );
      expect(html).toContain('<div class="callout-body"><p>Body text.</p></div>');
    });
  }

  it("falls back to the note treatment when no type is given", () => {
    expect(markup(<Callout>Text.</Callout>)).toContain('class="callout callout-note"');
  });

  it("falls back to the note label for a type it does not know", () => {
    const html = markup(<Callout type="danger">Text.</Callout>);

    expect(html).toContain('class="callout callout-danger"');
    expect(html).toContain(">Note</p>");
  });
});

describe("CodeBlock", () => {
  it("labels the block with the short name of its language", () => {
    const html = markup(
      <CodeBlock data-language="bash" data-highlighted="true" className="shiki">
        <code>podium sync</code>
      </CodeBlock>,
    );

    expect(html).toContain('<div class="code-block" data-language="bash">');
    expect(html).toContain('<span class="code-block-language">shell</span>');
    expect(html).toContain('data-highlighted="true"');
    expect(html).toContain("<code>podium sync</code>");
  });

  it("carries the copy hook the client script binds", () => {
    const html = markup(
      <CodeBlock data-language="json">
        <code>{"{}"}</code>
      </CodeBlock>,
    );

    expect(html).toContain('class="code-block-copy" data-copy="true"');
    expect(html).toContain('aria-label="Copy code"');
  });

  it("labels an unlabelled block as text", () => {
    expect(markup(<CodeBlock>{"x"}</CodeBlock>)).toContain(
      '<span class="code-block-language">text</span>',
    );
  });

  it("shows a language it has no short name for as written", () => {
    expect(markup(<CodeBlock data-language="terraform">{"x"}</CodeBlock>)).toContain(
      '<span class="code-block-language">terraform</span>',
    );
  });
});

describe("ActionLinks", () => {
  it("renders the primary action first and the rest as outlines", () => {
    const html = markup(
      <ActionLinks
        actions={[
          { label: "Quickstart", href: "/base/a.html", variant: "primary", external: false },
          { label: "Source", href: "https://example.com", variant: "outline", external: true },
        ]}
      />,
    );

    expect(html).toContain('class="action-link action-link-primary" href="/base/a.html"');
    expect(html).toContain('class="action-link action-link-outline"');
    expect(html).toContain('rel="noreferrer" target="_blank"');
  });

  it("marks only external links as opening in a new tab", () => {
    const html = markup(
      <ActionLinks
        actions={[
          { label: "Quickstart", href: "/base/a.html", variant: "primary", external: false },
        ]}
      />,
    );

    expect(html).not.toContain("target=");
  });

  it("renders nothing when the page declares no actions", () => {
    expect(markup(<ActionLinks actions={[]} />)).toBe("");
  });
});

describe("Tabs", () => {
  const panels = [
    { label: "npm", html: "<p>Use <strong>npm</strong>.</p>" },
    { label: "Homebrew", html: "<p>Use brew.</p>" },
  ];

  it("renders every panel visible before it mounts", () => {
    const html = markup(<Tabs panels={panels} />);

    expect(html).toContain('data-mounted="false"');
    expect(html).not.toContain("hidden");
    expect(html).toContain("<p>Use <strong>npm</strong>.</p>");
    expect(html).toContain("<p>Use brew.</p>");
  });

  it("labels each panel so the browser can read the data back", () => {
    const html = markup(<Tabs panels={panels} />);

    expect(html).toContain('data-tab-label="npm"');
    expect(html).toContain('data-tab-label="Homebrew"');
  });

  it("names each tab button after its panel", () => {
    const html = markup(<Tabs panels={panels} />);

    expect(html).toContain('class="tabs-tab"');
    expect(html).toContain(">npm</button>");
    expect(html).toContain(">Homebrew</button>");
  });

  it("assigns no tab roles before it mounts, so the strip reads as plain content", () => {
    const html = markup(<Tabs panels={panels} />);

    expect(html).not.toContain('role="tab"');
    expect(html).not.toContain('role="tablist"');
  });
});

describe("DiagramIsland", () => {
  it("renders the checked-in SVG when no animated variant is registered", () => {
    const html = markup(
      <DiagramIsland name="/base/assets/diagrams/sample.svg" alt="A sample diagram" />,
    );

    expect(html).toContain(
      '<img class="diagram" src="/base/assets/diagrams/sample.svg" alt="A sample diagram"/>',
    );
  });
});

describe("renderBody leaf islands", () => {
  function pageWithIsland(properties: Record<string, string>): PageModel {
    const body: HastRoot = {
      type: "root",
      children: [
        {
          type: "element",
          tagName: "podium-island",
          properties,
          children: [{ type: "element", tagName: "p", properties: {}, children: [
            { type: "text", value: "Fallback prose." },
          ] }],
        },
      ],
    };

    return {
      route: "/a.html",
      sourcePath: "docs/a.md",
      title: "A",
      displayTitle: "A",
      navTitle: null,
      description: "B",
      navOrder: null,
      hidden: false,
      sectionRoute: null,
      headings: [],
      actions: [],
      body,
      islands: [],
      editUrl: "",
      updatedAt: "",
      text: "",
    };
  }

  it("renders a diagram island's fallback image before it mounts", () => {
    const html = markup(
      renderBody(
        pageWithIsland({
          "data-island": "diagram",
          "data-island-id": "island-0",
          "data-island-mount": "replace",
          "data-island-props":
            '{"name":"/base/assets/diagrams/sample.svg","alt":"A sample diagram"}',
        }),
        new Map(),
      ) as never,
    );

    expect(html).toContain('class="island"');
    expect(html).toContain(
      '<img class="diagram" src="/base/assets/diagrams/sample.svg" alt="A sample diagram"/>',
    );
    expect(html).not.toContain("Fallback prose.");
  });

  it("keeps the written content when the island names no image", () => {
    const html = markup(
      renderBody(
        pageWithIsland({
          "data-island": "diagram",
          "data-island-mount": "replace",
          "data-island-props": '{"name":"sample","alt":"A"}',
        }),
        new Map(),
      ) as never,
    );

    expect(html).toContain("Fallback prose.");
  });

  it("keeps the written content for a leaf island with no fallback of its own", () => {
    const html = markup(
      renderBody(
        pageWithIsland({
          "data-island": "sparkle",
          "data-island-mount": "replace",
          "data-island-props": "{}",
        }),
        new Map(),
      ) as never,
    );

    expect(html).toContain("Fallback prose.");
  });

  it("keeps the written content when the island's props are not valid JSON", () => {
    const html = markup(
      renderBody(
        pageWithIsland({
          "data-island": "diagram",
          "data-island-mount": "replace",
          "data-island-props": "{not json}",
        }),
        new Map(),
      ) as never,
    );

    expect(html).toContain("Fallback prose.");
  });

  it("treats an island with no mount attribute as a leaf", () => {
    const html = markup(
      renderBody(pageWithIsland({ "data-island": "diagram" }), new Map()) as never,
    );

    expect(html).toContain('data-island-mount="replace"');
    expect(html).toContain("Fallback prose.");
  });
});

describe("escapeHtml", () => {
  const cases: Array<[string, string]> = [
    ["Podium & Co", "Podium &amp; Co"],
    ["<script>", "&lt;script&gt;"],
    ['a "quoted" word', "a &quot;quoted&quot; word"],
    ["plain", "plain"],
  ];

  for (const [input, expected] of cases) {
    it(`escapes ${JSON.stringify(input)}`, () => {
      expect(escapeHtml(input)).toBe(expected);
    });
  }
});

describe("renderDocument", () => {
  const options = {
    config: CONFIG,
    title: "Install · Podium",
    description: 'Install the "fixture" tool & more',
    route: "/guide/install.html",
    bodyClass: "docs",
    scriptSrc: "/base/assets/site/entry-abc.js",
    stylesheets: ["/base/assets/site-abc.css"],
    hydratable: false,
    children: createElement("main", null, "Body"),
  };

  it("wraps the markup in a complete document", () => {
    const html = renderDocument(options);

    expect(html.startsWith("<!doctype html>\n<html lang=\"en\">")).toBe(true);
    expect(html).toContain("<title>Install · Podium</title>");
    expect(html).toContain('<body class="docs" data-base-path="/base">');
    expect(html).toContain("<main>Body</main>");
    expect(html.trimEnd().endsWith("</html>")).toBe(true);
  });

  it("writes the canonical URL from the site origin, the base path, and the route", () => {
    expect(renderDocument(options)).toContain(
      '<link rel="canonical" href="https://example.test/base/guide/install.html">',
    );
  });

  it("escapes the metadata it writes into attributes", () => {
    const html = renderDocument(options);

    expect(html).toContain(
      '<meta name="description" content="Install the &quot;fixture&quot; tool &amp; more">',
    );
  });

  it("links each stylesheet and the client entry", () => {
    const html = renderDocument(options);

    expect(html).toContain('<link rel="stylesheet" href="/base/assets/site-abc.css">');
    expect(html).toContain(
      '<script type="module" src="/base/assets/site/entry-abc.js"></script>',
    );
  });

  it("omits the script tag for a page that needs no JavaScript", () => {
    expect(renderDocument({ ...options, scriptSrc: null })).not.toContain(
      'type="module"',
    );
  });

  it("applies the stored theme before first paint", () => {
    expect(renderDocument(options)).toContain('localStorage.getItem("podium-theme")');
  });

  it("emits hydration metadata only for a page carrying islands", () => {
    const plain = renderDocument(options);
    const hydratable = renderDocument({ ...options, hydratable: true });

    expect(plain).not.toContain("<!--$-->");
    expect(hydratable).toContain("<main>Body</main>");
  });
});

describe("renderBody over the fixture corpus", () => {
  let corpus: BuiltCorpus;

  beforeAll(async () => {
    corpus = await buildCorpus(configFor(FIXTURE_CORPUS, FIXTURE_CORPUS, { basePath: "/base" }), registry);
  });

  afterAll(() => {
    disposeHighlighter();
  });

  function pageAt(route: string): PageModel {
    const page = corpus.pages.find((candidate) => candidate.route === route);
    if (page === undefined) throw new Error(`no page at ${route}`);
    return page;
  }

  it("loads only the components a page's hydrating islands need", async () => {
    const components = await resolveIslandComponents(
      registry,
      pageAt("/guide/tabs.html").islands,
    );

    expect([...components.keys()]).toEqual(["tabs"]);
  });

  it("loads nothing for a page with no islands", async () => {
    const components = await resolveIslandComponents(
      registry,
      pageAt("/guide/install.html").islands,
    );

    expect(components.size).toBe(0);
  });

  it("renders a callout, a code block, and an image from one page", async () => {
    const page = pageAt("/guide/install.html");
    const html = markup(renderBody(page, new Map()) as never);

    expect(html).toContain('class="callout callout-note"');
    expect(html).toContain('<div class="code-block" data-language="bash">');
    expect(html).toContain('src="/base/assets/diagrams/sample.svg"');
  });

  it("renders a tabs island as a mount point wrapping the rendered panels", async () => {
    const page = pageAt("/guide/tabs.html");
    const components = await resolveIslandComponents(registry, page.islands);
    const html = markup(renderBody(page, components) as never);

    expect(html).toContain('data-island="tabs"');
    expect(html).toContain('data-island-mount="hydrate"');
    expect(html).toContain('data-tab-label="npm"');
    expect(html).toContain('data-tab-label="Homebrew"');
    expect(html).toContain("Install with <strong>npm</strong>.");
    expect(html).toContain("Install with <strong>Homebrew</strong>.");
  });

  it("keeps prose the parser read as an inline directive in the rendered markup", () => {
    const html = markup(renderBody(pageAt("/overview.html"), new Map()) as never);

    expect(html).toContain("17:00");
    expect(html).toContain("1:1");
  });

  it("renders a page with a hydrating island but no loaded component as its children", () => {
    const page = pageAt("/guide/tabs.html");
    const html = markup(renderBody(page, new Map()) as never);

    expect(html).toContain('data-island="tabs"');
    expect(html).toContain("Install with <strong>npm</strong>.");
    expect(html).not.toContain("tabs-strip");
  });
});
