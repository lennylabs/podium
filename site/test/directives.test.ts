import { existsSync } from "node:fs";
import { join, resolve } from "node:path";

import type { Element, Root as HastRoot } from "hast";
import { describe, expect, it } from "vitest";

import { childOnlyDirectives } from "../src/build/content/directives";
import {
  coerceProps,
  defineIsland,
  validateRegistry,
  type CoerceContext,
  type IslandDefinition,
  type IslandRegistry,
} from "../src/components/islands/props";
import { registry } from "../src/components/islands/registry";
import type { BuildDiagnostic } from "../src/build/types";
import { FIXTURE_CORPUS } from "./support/corpus";
import { runDirectives, textContent } from "./support/directives";

/** Frontmatter occupies lines 1..6, so a body line 3 is file line 9. */
const OFFSET = 6;

function elements(tree: HastRoot): Element[] {
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

describe("childOnlyDirectives", () => {
  it("derives the child-only names from the registry rather than a hardcoded list", () => {
    expect([...childOnlyDirectives(registry)]).toEqual(["tab"]);
  });

  it("returns nothing for a registry that declares no directive children", () => {
    expect(childOnlyDirectives({})).toEqual(new Set());
  });
});

describe("remarkDirectives failure modes", () => {
  it("rejects a directive name the registry does not define", () => {
    const { diagnostics } = runDirectives("# T\n\n::gallery{name=\"a\"}\n", {
      lineOffset: OFFSET,
    });

    expect(diagnostics).toEqual([
      {
        file: "docs/page.md",
        line: 3 + OFFSET,
        column: 1,
        message: 'unknown directive "gallery". The registry defines diagram, tab, tabs',
      },
    ]);
  });

  it("rejects an attribute the island does not declare", () => {
    const { diagnostics } = runDirectives(
      '# T\n\n::diagram{name="sample" alt="A" caption="B"}\n',
      { lineOffset: OFFSET, resolveAsset: () => "/assets/diagrams/sample.svg" },
    );

    expect(diagnostics).toEqual([
      {
        file: "docs/page.md",
        line: 3 + OFFSET,
        column: 1,
        message: 'island "diagram" has no prop "caption". It declares alt, name.',
      },
    ]);
  });

  it("rejects a directive that omits a required prop", () => {
    const { diagnostics } = runDirectives('# T\n\n::diagram{name="sample"}\n', {
      lineOffset: OFFSET,
      resolveAsset: () => "/assets/diagrams/sample.svg",
    });

    expect(diagnostics).toEqual([
      {
        file: "docs/page.md",
        line: 3 + OFFSET,
        column: 1,
        message: 'island "diagram" requires prop "alt"',
      },
    ]);
  });

  it("rejects a value that fails number coercion", () => {
    const numeric: IslandRegistry = {
      chart: defineIsland<{ height: number }>({
        props: { height: { kind: "number", required: true } },
        children: { kind: "none" },
        fallback: { from: "prop", name: "height" },
      }),
    };

    const { diagnostics } = runDirectives('# T\n\n::chart{height="tall"}\n', {
      registry: numeric,
      lineOffset: OFFSET,
    });

    expect(diagnostics).toEqual([
      {
        file: "docs/page.md",
        line: 3 + OFFSET,
        column: 1,
        message: 'island "chart" prop "height" must be a number, got "tall"',
      },
    ]);
  });

  it("rejects a value outside the declared oneOf set", () => {
    const constrained: IslandRegistry = {
      banner: defineIsland<{ tone: string }>({
        props: {
          tone: { kind: "string", required: true, oneOf: ["calm", "loud"] as const },
        },
        children: { kind: "none" },
        fallback: { from: "prop", name: "tone" },
      }),
    };

    const { diagnostics } = runDirectives('# T\n\n::banner{tone="shouty"}\n', {
      registry: constrained,
      lineOffset: OFFSET,
    });

    expect(diagnostics).toEqual([
      {
        file: "docs/page.md",
        line: 3 + OFFSET,
        column: 1,
        message: 'island "banner" prop "tone" must be one of calm, loud, got "shouty"',
      },
    ]);
  });

  it("rejects an asset prop naming a file that does not exist", () => {
    const { diagnostics } = runDirectives(
      '# T\n\n::diagram{name="missing" alt="A missing diagram"}\n',
      { lineOffset: OFFSET },
    );

    expect(diagnostics).toEqual([
      {
        file: "docs/page.md",
        line: 3 + OFFSET,
        column: 1,
        message: 'island "diagram" prop "name" does not name a file that exists: "missing"',
      },
    ]);
  });

  it("rejects a tab written outside a tabs container", () => {
    const { diagnostics } = runDirectives('# T\n\n:::tab{label="npm"}\nText.\n:::\n', {
      lineOffset: OFFSET,
    });

    expect(diagnostics).toEqual([
      {
        file: "docs/page.md",
        line: 3 + OFFSET,
        column: 1,
        message: '"tab" only appears inside tabs',
      },
    ]);
  });

  it("rejects a tabs container holding prose directly", () => {
    const { diagnostics } = runDirectives(
      "# T\n\n::::tabs\n\nProse that belongs in a tab.\n\n::::\n",
      { lineOffset: OFFSET },
    );

    expect(diagnostics).toEqual([
      {
        file: "docs/page.md",
        line: 5 + OFFSET,
        column: 1,
        message: '"tabs" holds only "tab" containers, and this content is not one',
      },
    ]);
  });

  it("rejects a tabs container below its minimum child count", () => {
    const { diagnostics } = runDirectives(
      '# T\n\n::::tabs\n\n:::tab{label="npm"}\nOnly one.\n:::\n\n::::\n',
      { lineOffset: OFFSET },
    );

    expect(diagnostics[0]).toEqual({
      file: "docs/page.md",
      line: 3 + OFFSET,
      column: 1,
      message: '"tabs" needs at least 2 "tab" children, found 1',
    });
  });

  it("rejects a container written for an island that takes no content", () => {
    const { diagnostics } = runDirectives(
      '# T\n\n:::diagram{name="sample" alt="A"}\nContent.\n:::\n',
      { lineOffset: OFFSET, resolveAsset: () => "/assets/diagrams/sample.svg" },
    );

    expect(diagnostics).toEqual([
      {
        file: "docs/page.md",
        line: 3 + OFFSET,
        column: 1,
        message: '"diagram" takes no content, so write it as a leaf: "::diagram{...}"',
      },
    ]);
  });

  it("rejects a leaf written for an island that wraps content", () => {
    const { diagnostics } = runDirectives("# T\n\n::tabs\n", { lineOffset: OFFSET });

    expect(diagnostics).toEqual([
      {
        file: "docs/page.md",
        line: 3 + OFFSET,
        column: 1,
        message: '"tabs" wraps content, so write it as a container: ":::tabs"',
      },
    ]);
  });

  it("rejects a text directive whose name is a registry entry", () => {
    const { diagnostics } = runDirectives("# T\n\nSee the :tabs above.\n", {
      lineOffset: OFFSET,
    });

    expect(diagnostics).toEqual([
      {
        file: "docs/page.md",
        line: 3 + OFFSET,
        column: 9,
        message:
          'inline directive ":tabs" is not supported. A component occupies a block: write "::tabs{...}" for a leaf or ":::tabs" for a container',
      },
    ]);
  });
});

describe("remarkDirectives prose that only looks like a directive", () => {
  const prose: Array<{ name: string; source: string }> = [
    {
      name: "keeps a clock time written in prose",
      source: 'No ingest from Friday 17:00 to Monday 09:00.\n',
    },
    {
      name: "keeps a one-to-one ratio written in prose",
      source: "The read CLI maps 1:1 to the SDK's read operations.\n",
    },
    {
      name: "keeps a colon-prefixed word that names no component",
      source: "Write :emphasis in prose and it survives.\n",
    },
  ];

  for (const entry of prose) {
    it(entry.name, () => {
      const { tree, diagnostics, islands } = runDirectives(entry.source, {
        lineOffset: OFFSET,
      });

      expect(diagnostics).toEqual([]);
      expect(islands).toEqual([]);
      expect(textContent(tree)).toBe(entry.source.trimEnd());
    });
  }

  it("keeps both patterns intact in one paragraph", () => {
    const source =
      "Freeze from Friday 17:00 to Monday 09:00, and the CLI maps 1:1 to the SDK.\n";
    const { tree, diagnostics } = runDirectives(source);

    expect(diagnostics).toEqual([]);
    expect(textContent(tree)).toContain("17:00");
    expect(textContent(tree)).toContain("1:1");
    expect(textContent(tree)).toBe(source.trimEnd());
  });

  it("restores the text a directive with attributes consumed", () => {
    const source = "Contact alice at :desk{floor=3} today.\n";
    const { tree, diagnostics } = runDirectives(source);

    expect(diagnostics).toEqual([]);
    expect(textContent(tree)).toBe(source.trimEnd());
  });
});

describe("remarkDirectives success paths", () => {
  it("emits a static image for a diagram with no animated variant", () => {
    const { tree, diagnostics, islands } = runDirectives(
      '::diagram{name="sample" alt="A sample fixture diagram"}\n',
      { resolveAsset: () => "/assets/diagrams/sample.svg" },
    );

    expect(diagnostics).toEqual([]);
    expect(islands).toEqual([]);

    const image = elements(tree).find((node) => node.tagName === "img");
    expect(image?.properties).toMatchObject({
      src: "/assets/diagrams/sample.svg",
      alt: "A sample fixture diagram",
      className: ["diagram"],
    });
  });

  it("emits an island for a diagram that has an animated variant", () => {
    const { tree, diagnostics, islands } = runDirectives(
      '::diagram{name="sample" alt="A sample fixture diagram"}\n',
      {
        resolveAsset: () => "/assets/diagrams/sample.svg",
        diagramVariants: new Set(["sample"]),
      },
    );

    expect(diagnostics).toEqual([]);
    expect(islands).toEqual([
      {
        id: "island-0",
        component: "diagram",
        mount: "replace",
        props: { name: "/assets/diagrams/sample.svg", alt: "A sample fixture diagram" },
      },
    ]);

    const placeholder = elements(tree).find((node) => node.tagName === "podium-island");
    expect(placeholder?.properties).toMatchObject({
      "data-island": "diagram",
      "data-island-id": "island-0",
      "data-island-mount": "replace",
    });
  });

  it("emits a hydrating island and a child marker per tab", () => {
    const { tree, diagnostics, islands } = runDirectives(
      '::::tabs\n\n:::tab{label="npm"}\nUse npm.\n:::\n\n:::tab{label="Homebrew"}\nUse brew.\n:::\n\n::::\n',
    );

    expect(diagnostics).toEqual([]);
    expect(islands).toEqual([
      { id: "island-0", component: "tabs", mount: "hydrate", props: {} },
    ]);

    const markers = elements(tree).filter(
      (node) => node.properties?.["data-child-directive"] === "tab",
    );
    expect(markers.map((node) => node.properties?.["data-child-props"])).toEqual([
      '{"label":"npm"}',
      '{"label":"Homebrew"}',
    ]);
  });

  it("numbers islands in source order", () => {
    const { islands } = runDirectives(
      '::diagram{name="a" alt="A"}\n\n::diagram{name="b" alt="B"}\n',
      {
        resolveAsset: (_spec, value) => `/assets/diagrams/${value}.svg`,
        diagramVariants: new Set(["a", "b"]),
      },
    );

    expect(islands.map((island) => island.id)).toEqual(["island-0", "island-1"]);
  });
});

describe("coerceProps", () => {
  const context: CoerceContext = {
    file: "docs/page.md",
    line: 11,
    column: 4,
    resolveRoute: (value) => (value === "guide/install" ? "/guide/install.html" : null),
    resolveAsset: (_spec, value) => (value === "sample" ? "/sample.svg" : null),
  };

  function coerce(
    entry: IslandDefinition,
    attributes: Record<string, string | null | undefined>,
  ): { props: ReturnType<typeof coerceProps>; diagnostics: BuildDiagnostic[] } {
    const diagnostics: BuildDiagnostic[] = [];
    const props = coerceProps("widget", entry, attributes, context, diagnostics);
    return { props, diagnostics };
  }

  const entry = defineIsland<{
    label: string;
    tone: string;
    height: number;
    open: boolean;
    target: string;
    picture: string;
  }>({
    props: {
      label: { kind: "string", required: true },
      tone: { kind: "string", oneOf: ["calm", "loud"] as const, default: "calm" },
      height: { kind: "number", default: 12 },
      open: { kind: "boolean" },
      target: { kind: "route" },
      picture: { kind: "asset" },
    },
    children: { kind: "none" },
    fallback: { from: "prop", name: "label" },
  });

  it("coerces every declared kind and applies the declared defaults", () => {
    const { props, diagnostics } = coerce(entry, {
      label: "Install",
      height: "24",
      open: "true",
      target: "guide/install",
      picture: "sample",
    });

    expect(diagnostics).toEqual([]);
    expect(props).toEqual({
      label: "Install",
      tone: "calm",
      height: 24,
      open: true,
      target: "/guide/install.html",
      picture: "/sample.svg",
    });
  });

  it("reads a valueless attribute as true", () => {
    expect(coerce(entry, { label: "A", open: "" }).props).toMatchObject({ open: true });
  });

  it("reads an explicit false", () => {
    expect(coerce(entry, { label: "A", open: "false" }).props).toMatchObject({
      open: false,
    });
  });

  it("rejects a boolean that is neither true nor false", () => {
    const { props, diagnostics } = coerce(entry, { label: "A", open: "maybe" });

    expect(props).toBeNull();
    expect(diagnostics[0]).toEqual({
      file: "docs/page.md",
      line: 11,
      column: 4,
      message: 'island "widget" prop "open" must be true or false, got "maybe"',
    });
  });

  it("rejects a number written as whitespace", () => {
    expect(coerce(entry, { label: "A", height: "  " }).diagnostics[0]?.message).toBe(
      'island "widget" prop "height" must be a number, got "  "',
    );
  });

  it("rejects a route that resolves to no page", () => {
    expect(coerce(entry, { label: "A", target: "guide/nowhere" }).diagnostics[0]).toEqual({
      file: "docs/page.md",
      line: 11,
      column: 4,
      message: 'island "widget" prop "target" does not resolve to a page: "guide/nowhere"',
    });
  });

  it("omits an optional prop the directive did not write", () => {
    expect(coerce(entry, { label: "A" }).props).toEqual({
      label: "A",
      tone: "calm",
      height: 12,
    });
  });

  it("names the declared props when an island declares none", () => {
    const bare = defineIsland<Record<string, never>>({
      props: {},
      children: { kind: "markdown" },
      fallback: { from: "children" },
    });

    expect(coerce(bare, { label: "A" }).diagnostics[0]?.message).toBe(
      'island "widget" has no prop "label". It declares no props.',
    );
  });

  it("treats an attribute written as null as absent", () => {
    expect(coerce(entry, { label: "A", open: null }).props).toMatchObject({ label: "A" });
  });
});

describe("the registry the site ships", () => {
  it("loads a component for every entry that declares a loader", async () => {
    for (const [name, entry] of Object.entries(registry)) {
      if (entry.load === undefined) continue;
      const module = await entry.load();
      expect(module.default, `${name} loaded no component`).toBeTypeOf("function");
    }
  });

  it("declares no loader for a child-only entry, which its parent arranges", () => {
    expect(registry["tab"]?.load).toBeUndefined();
  });
});

describe("validateRegistry", () => {
  it("accepts the registry the site ships", () => {
    expect(validateRegistry(registry)).toEqual([]);
  });

  it("rejects a fallback naming a prop the island does not declare", () => {
    const diagnostics = validateRegistry({
      widget: defineIsland<{ label: string }>({
        props: { label: { kind: "string", required: true } },
        children: { kind: "none" },
        fallback: { from: "prop", name: "picture" },
      }),
    });

    expect(diagnostics).toEqual([
      {
        file: "site/src/components/islands/registry.ts",
        line: null,
        column: null,
        message:
          'island "widget" names "picture" as its fallback but does not declare that prop',
      },
    ]);
  });

  it("rejects a fallback naming an optional prop", () => {
    const diagnostics = validateRegistry({
      widget: defineIsland<{ label: string }>({
        props: { label: { kind: "string" } },
        children: { kind: "none" },
        fallback: { from: "prop", name: "label" },
      }),
    });

    expect(diagnostics).toEqual([
      {
        file: "site/src/components/islands/registry.ts",
        line: null,
        column: null,
        message:
          'island "widget" names optional prop "label" as its fallback; a fallback prop must be required',
      },
    ]);
  });

  it("rejects an island taking its fallback from children it does not accept", () => {
    const diagnostics = validateRegistry({
      widget: defineIsland<Record<string, never>>({
        props: {},
        children: { kind: "none" },
        fallback: { from: "children" },
      }),
    });

    expect(diagnostics[0]?.message).toBe(
      'island "widget" takes its fallback from children but declares no children',
    );
  });

  it("rejects a child minimum below one", () => {
    const diagnostics = validateRegistry({
      widget: defineIsland<Record<string, never>>({
        props: {},
        children: { kind: "directives", name: "panel", min: 0 },
        fallback: { from: "children" },
      }),
    });

    expect(diagnostics[0]?.message).toBe('island "widget" declares a child minimum below 1');
  });
});

describe("asset resolution against a corpus on disk", () => {
  it("resolves a bare stem to the checked-in file the registry entry names", () => {
    const spec = registry["diagram"]?.props["name"];
    if (spec === undefined || spec.kind !== "asset") {
      throw new Error("the diagram entry no longer declares name as an asset");
    }

    const { diagnostics, tree } = runDirectives(
      '::diagram{name="sample" alt="A sample fixture diagram"}\n',
      {
        resolveAsset: (assetSpec, value) => {
          const relativePath = join(
            assetSpec.within ?? "",
            `${value}${assetSpec.extension ?? ""}`,
          );
          return existsSync(resolve(FIXTURE_CORPUS, relativePath))
            ? `/${relativePath}`
            : null;
        },
      },
    );

    expect(diagnostics).toEqual([]);
    expect(
      elements(tree).find((node) => node.tagName === "img")?.properties?.["src"],
    ).toBe("/assets/diagrams/sample.svg");
  });
});
