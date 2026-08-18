import { describe, expect, it } from "vitest";

import {
  checkHeadingOrder,
  checkImageAlt,
  collectHeadings,
  collectText,
} from "../src/build/content/headings";
import type { BuildDiagnostic, Heading } from "../src/build/types";
import { toHast } from "./support/hast";

describe("collectHeadings", () => {
  it("collects every heading with its depth, id, and text", () => {
    expect(collectHeadings(toHast("# Install\n\n## Requirements\n\n### macOS\n"))).toEqual([
      { depth: 1, id: "install", text: "Install" },
      { depth: 2, id: "requirements", text: "Requirements" },
      { depth: 3, id: "macos", text: "macOS" },
    ]);
  });

  it("excludes the anchor link text the autolink transform appended", () => {
    const [heading] = collectHeadings(toHast("## podium sync\n"));

    expect(heading?.text).toBe("podium sync");
  });

  it("keeps inline markup as text", () => {
    const [heading] = collectHeadings(toHast("## The `podium` binary\n"));

    expect(heading?.text).toBe("The podium binary");
  });

  it("skips a heading that carries no id", () => {
    const tree = toHast("## A\n");
    const heading = tree.children.find(
      (node) => node.type === "element" && node.tagName === "h2",
    );
    if (heading?.type === "element") heading.properties = {};

    expect(collectHeadings(tree)).toEqual([]);
  });

  it("returns nothing for a body with no headings", () => {
    expect(collectHeadings(toHast("Prose only.\n"))).toEqual([]);
  });
});

describe("collectText", () => {
  it("flattens prose into one whitespace-normalised string", () => {
    expect(collectText(toHast("# A\n\nFirst line\nsecond line.\n"))).toBe(
      "A First line second line.",
    );
  });

  it("excludes fenced code", () => {
    expect(collectText(toHast("Prose.\n\n```bash\npodium sync\n```\n"))).not.toContain(
      "podium sync",
    );
  });

  it("excludes island markup", () => {
    const tree = toHast("Prose.\n");
    tree.children.push({
      type: "element",
      tagName: "podium-island",
      properties: {},
      children: [{ type: "text", value: "hidden from the index" }],
    });

    expect(collectText(tree)).toBe("Prose.");
  });

  it("returns an empty string for an empty body", () => {
    expect(collectText(toHast(""))).toBe("");
  });
});

describe("checkHeadingOrder", () => {
  function messages(headings: Heading[]): string[] {
    const diagnostics: BuildDiagnostic[] = [];
    checkHeadingOrder(headings, "docs/page.md", diagnostics);
    return diagnostics.map((diagnostic) => diagnostic.message);
  }

  function outline(...depths: number[]): Heading[] {
    return depths.map((depth, position) => ({
      depth,
      id: `h${position}`,
      text: `Heading ${position}`,
    }));
  }

  it("accepts an outline that descends one level at a time", () => {
    expect(messages(outline(2, 3, 4, 3, 2))).toEqual([]);
  });

  it("accepts a body that opens at h2, because the template renders the h1", () => {
    expect(messages(outline(2))).toEqual([]);
  });

  it("rejects a body that opens at h3", () => {
    expect(messages(outline(3))).toEqual([
      'heading "Heading 0" is an h3 directly below an h1, which skips a level',
    ]);
  });

  it("rejects a jump from h2 to h4", () => {
    expect(messages(outline(2, 4))).toEqual([
      'heading "Heading 1" is an h4 directly below an h2, which skips a level',
    ]);
  });

  it("locates the diagnostic at the file rather than a line", () => {
    const diagnostics: BuildDiagnostic[] = [];
    checkHeadingOrder(outline(4), "docs/page.md", diagnostics);

    expect(diagnostics[0]).toMatchObject({
      file: "docs/page.md",
      line: null,
      column: null,
    });
  });
});

describe("checkImageAlt", () => {
  function messages(source: string): string[] {
    const diagnostics: BuildDiagnostic[] = [];
    checkImageAlt(toHast(source), "docs/page.md", 0, diagnostics);
    return diagnostics.map((diagnostic) => diagnostic.message);
  }

  it("accepts an image carrying alt text", () => {
    expect(messages("![The layout](a.svg)\n")).toEqual([]);
  });

  it("rejects an image with no alt text", () => {
    expect(messages("![](a.svg)\n")).toEqual(['image "a.svg" has no alt text']);
  });

  it("rejects an image whose alt text is whitespace", () => {
    expect(messages("![ ](a.svg)\n")).toEqual(['image "a.svg" has no alt text']);
  });

  it("locates the diagnostic at the line the image was written on", () => {
    const diagnostics: BuildDiagnostic[] = [];
    checkImageAlt(toHast("Prose.\n\n![](a.svg)\n"), "docs/page.md", 0, diagnostics);

    expect(diagnostics[0]).toMatchObject({ file: "docs/page.md", line: 3, column: 1 });
  });

  it("offsets the line past the frontmatter block", () => {
    const diagnostics: BuildDiagnostic[] = [];
    // A page whose frontmatter occupies six lines: the image on body line 3 is
    // on file line 9, which is the line an author opens the file to.
    checkImageAlt(toHast("Prose.\n\n![](a.svg)\n"), "docs/page.md", 6, diagnostics);

    expect(diagnostics[0]).toMatchObject({ line: 9, column: 1 });
  });
});
