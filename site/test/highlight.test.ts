import type { Element, Root as HastRoot } from "hast";
import remarkParse from "remark-parse";
import remarkRehype from "remark-rehype";
import { unified } from "unified";
import { afterAll, describe, expect, it } from "vitest";

import {
  LANGUAGES,
  disposeHighlighter,
  highlightTree,
  parseHighlightedLines,
  remarkCodeMeta,
} from "../src/build/content/highlight";
import type { BuildDiagnostic } from "../src/build/types";

function toHast(source: string): HastRoot {
  const processor = unified()
    .use(remarkParse)
    .use(remarkCodeMeta)
    .use(remarkRehype, { allowDangerousHtml: false });
  return processor.runSync(processor.parse(source), source) as HastRoot;
}

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

/** Classes under either key, which is what a browser sees as the class attribute. */
function classesOf(node: Element): string[] {
  const raw: unknown = node.properties?.["className"] ?? node.properties?.["class"];
  if (Array.isArray(raw)) return raw.map(String);
  if (typeof raw === "string") return raw.split(/\s+/);
  return [];
}

async function highlight(
  source: string,
): Promise<{ tree: HastRoot; diagnostics: BuildDiagnostic[]; pre: Element | undefined }> {
  const tree = toHast(source);
  const diagnostics: BuildDiagnostic[] = [];
  await highlightTree(tree, "docs/page.md", 0, diagnostics);
  const pre = elements(tree).find((node) => node.tagName === "pre");
  return { tree, diagnostics, pre };
}

afterAll(() => {
  disposeHighlighter();
});

describe("parseHighlightedLines", () => {
  const cases: Array<{ meta: string | undefined; expected: number[] }> = [
    { meta: undefined, expected: [] },
    { meta: "", expected: [] },
    { meta: "title=x", expected: [] },
    { meta: "{2}", expected: [2] },
    { meta: "{2,5}", expected: [2, 5] },
    { meta: "{5-7}", expected: [5, 6, 7] },
    { meta: "{1,3-5}", expected: [1, 3, 4, 5] },
    { meta: "{ 2 , 4 }", expected: [2, 4] },
    { meta: "{}", expected: [] },
  ];

  for (const entry of cases) {
    it(`reads ${JSON.stringify(entry.meta)} as ${JSON.stringify(entry.expected)}`, () => {
      expect([...parseHighlightedLines(entry.meta)].sort((a, b) => a - b)).toEqual(
        entry.expected,
      );
    });
  }
});

describe("remarkCodeMeta", () => {
  it("carries the fence language and metadata onto the emitted element", () => {
    const code = elements(toHast("```bash {2}\na\nb\n```\n")).find(
      (node) => node.tagName === "code",
    );

    expect(code?.properties).toMatchObject({
      "data-language": "bash",
      "data-meta": "{2}",
    });
  });

  it("writes no language for an untagged fence", () => {
    const code = elements(toHast("```\na\n```\n")).find((node) => node.tagName === "code");

    expect(code?.properties?.["data-language"]).toBeUndefined();
  });
});

describe("highlightTree", () => {
  it("highlights a block written in a supported language", async () => {
    const { pre, diagnostics } = await highlight("```go\npackage main\n```\n");

    expect(diagnostics).toEqual([]);
    expect(pre?.properties).toMatchObject({
      "data-language": "go",
      "data-highlighted": "true",
    });
  });

  const aliases: Array<[string, string]> = [
    ["sh", "bash"],
    ["shell", "bash"],
    ["zsh", "bash"],
    ["js", "javascript"],
    ["ts", "typescript"],
    ["yml", "yaml"],
    ["md", "markdown"],
    ["ps1", "powershell"],
    ["golang", "go"],
  ];

  for (const [alias, canonical] of aliases) {
    it(`normalises the "${alias}" tag to ${canonical}`, async () => {
      const { pre, diagnostics } = await highlight("```" + alias + "\nx\n```\n");

      expect(diagnostics).toEqual([]);
      expect(pre?.properties?.["data-language"]).toBe(canonical);
    });
  }

  const plain: string[] = ["text", "plaintext", "txt", "console", "output"];

  for (const tag of plain) {
    it(`leaves a "${tag}" block unhighlighted`, async () => {
      const { pre, diagnostics } = await highlight("```" + tag + "\nx\n```\n");

      expect(diagnostics).toEqual([]);
      expect(pre?.properties).toMatchObject({
        "data-language": tag,
        "data-highlighted": "false",
      });
    });
  }

  it("labels an untagged block as text and leaves it unhighlighted", async () => {
    const { pre } = await highlight("```\nx\n```\n");

    expect(pre?.properties).toMatchObject({
      "data-language": "text",
      "data-highlighted": "false",
    });
  });

  it("rejects a language it does not load", async () => {
    const { diagnostics } = await highlight("Prose.\n\n```terraform\nx\n```\n");

    expect(diagnostics).toEqual([
      {
        file: "docs/page.md",
        line: 3,
        column: 1,
        message: `unknown code fence language "terraform". Supported languages are ${LANGUAGES.join(", ")}, or tag the block "text" to leave it unhighlighted`,
      },
    ]);
  });

  it("marks the lines the fence metadata names", async () => {
    const { pre } = await highlight("```bash {2}\nfirst\nsecond\nthird\n```\n");

    const lines = elements({ type: "root", children: [pre!] })
      .map((node) => classesOf(node))
      .filter((classes) => classes.includes("line"));

    expect(lines).toHaveLength(3);
    expect(lines[0]).not.toContain("line-highlighted");
    expect(lines[1]).toContain("line-highlighted");
    expect(lines[2]).not.toContain("line-highlighted");
  });

  it("marks no lines when the fence names none", async () => {
    const { pre } = await highlight("```bash\nfirst\nsecond\n```\n");

    expect(JSON.stringify(pre)).not.toContain("line-highlighted");
  });

  it("returns without loading a highlighter for a body with no fences", async () => {
    const tree = toHast("Prose only.\n");
    const diagnostics: BuildDiagnostic[] = [];

    await highlightTree(tree, "docs/page.md", 0, diagnostics);

    expect(diagnostics).toEqual([]);
    expect(elements(tree).map((node) => node.tagName)).toEqual(["p"]);
  });

  it("leaves a pre element that holds no code element alone", async () => {
    const tree: HastRoot = {
      type: "root",
      children: [
        { type: "element", tagName: "pre", properties: {}, children: [] },
      ],
    };
    const diagnostics: BuildDiagnostic[] = [];

    await highlightTree(tree, "docs/page.md", 0, diagnostics);

    expect(diagnostics).toEqual([]);
    expect(tree.children).toHaveLength(1);
  });

  it("reads the language from the class when no data attribute is present", async () => {
    const tree: HastRoot = {
      type: "root",
      children: [
        {
          type: "element",
          tagName: "pre",
          properties: {},
          children: [
            {
              type: "element",
              tagName: "code",
              properties: { className: "language-json" } as unknown as Element["properties"],
              children: [{ type: "text", value: '{"a":1}' }],
            },
          ],
        },
      ],
    };
    const diagnostics: BuildDiagnostic[] = [];

    await highlightTree(tree, "docs/page.md", 0, diagnostics);

    expect(diagnostics).toEqual([]);
    const pre = tree.children[0];
    expect(pre?.type === "element" ? pre.properties : {}).toMatchObject({
      "data-language": "json",
      "data-highlighted": "true",
    });
  });
});
