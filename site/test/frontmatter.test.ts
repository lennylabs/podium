import { describe, expect, it } from "vitest";

import {
  parseFrontmatter,
  splitFrontmatter,
} from "../src/build/content/frontmatter";
import type { BuildDiagnostic } from "../src/build/types";

function parse(block: string): {
  result: ReturnType<typeof parseFrontmatter>;
  diagnostics: BuildDiagnostic[];
} {
  const diagnostics: BuildDiagnostic[] = [];
  const result = parseFrontmatter(block, "docs/page.md", diagnostics);
  return { result, diagnostics };
}

function messages(block: string): string[] {
  return parse(block).diagnostics.map((diagnostic) => diagnostic.message);
}

const VALID = `
title: Quickstart
description: Install Podium and materialize a catalog.
nav_order: 2
nav_title: Quick start
permalink: /getting-started/quickstart.html
hidden: false
actions:
  - label: Concepts
    href: getting-started/concepts
`;

describe("splitFrontmatter", () => {
  it("returns the block, the body, and the line the body starts on", () => {
    const split = splitFrontmatter("---\ntitle: A\n---\n\n# A\n\nText.\n");

    expect(split.frontmatter).toBe("\ntitle: A");
    expect(split.body).toBe("\n# A\n\nText.\n");
    expect(split.bodyStartLine).toBe(4);
  });

  it("reports no frontmatter for a file that does not open with a delimiter", () => {
    const source = "# Notes\n\nNo frontmatter here.\n";
    const split = splitFrontmatter(source);

    expect(split.frontmatter).toBeNull();
    expect(split.body).toBe(source);
    expect(split.bodyStartLine).toBe(1);
  });

  it("reports no frontmatter when the opening delimiter is never closed", () => {
    const source = "---\ntitle: A\n\n# A\n";
    const split = splitFrontmatter(source);

    expect(split.frontmatter).toBeNull();
    expect(split.body).toBe(source);
  });

  it("returns an empty body for a file that is only a frontmatter block", () => {
    const split = splitFrontmatter("---\ntitle: A\n---");

    expect(split.frontmatter).toBe("\ntitle: A");
    expect(split.body).toBe("");
  });
});

describe("parseFrontmatter", () => {
  it("accepts a block naming every supported key", () => {
    const { result, diagnostics } = parse(VALID);

    expect(diagnostics).toEqual([]);
    expect(result).toEqual({
      title: "Quickstart",
      description: "Install Podium and materialize a catalog.",
      navOrder: 2,
      navTitle: "Quick start",
      permalink: "/getting-started/quickstart.html",
      hidden: false,
      actions: [{ label: "Concepts", href: "getting-started/concepts" }],
    });
  });

  it("defaults the optional keys when the block names only the required ones", () => {
    const { result } = parse("title: A\ndescription: B\n");

    expect(result).toEqual({
      title: "A",
      description: "B",
      navOrder: null,
      navTitle: null,
      permalink: null,
      hidden: false,
      actions: [],
    });
  });

  const rejections: Array<{ name: string; block: string; expected: RegExp }> = [
    {
      name: "rejects a block with no title",
      block: "description: B\n",
      expected: /"title" is required/,
    },
    {
      name: "rejects a block with an empty title",
      block: 'title: "  "\ndescription: B\n',
      expected: /"title" is required/,
    },
    {
      name: "rejects a block with no description",
      block: "title: A\n",
      expected: /"description" is required/,
    },
    {
      name: "rejects an unknown key",
      block: "title: A\ndescription: B\nlayout: default\n",
      expected: /unknown frontmatter key "layout"/,
    },
    {
      name: "rejects a nav_order that is not a number",
      block: "title: A\ndescription: B\nnav_order: second\n",
      expected: /"nav_order" must be a number/,
    },
    {
      name: "rejects a nav_title that is not a string",
      block: "title: A\ndescription: B\nnav_title: 4\n",
      expected: /"nav_title" must be a string/,
    },
    {
      name: "rejects a permalink that does not begin with a slash",
      block: "title: A\ndescription: B\npermalink: getting-started/\n",
      expected: /"permalink" must be a string beginning with "\/"/,
    },
    {
      name: "rejects a hidden value that is not a boolean",
      block: "title: A\ndescription: B\nhidden: yes please\n",
      expected: /"hidden" must be true or false/,
    },
    {
      name: "rejects actions that are not a list",
      block: "title: A\ndescription: B\nactions: Concepts\n",
      expected: /"actions" must be a list/,
    },
    {
      name: "rejects an action entry that is not a mapping",
      block: "title: A\ndescription: B\nactions:\n  - Concepts\n",
      expected: /actions\[0\] must be a mapping with "label" and "href"/,
    },
    {
      name: "rejects an action entry naming an unknown key",
      block:
        "title: A\ndescription: B\nactions:\n  - label: A\n    href: b\n    variant: primary\n",
      expected: /actions\[0\] has unknown key "variant"/,
    },
    {
      name: "rejects an action entry with no label",
      block: "title: A\ndescription: B\nactions:\n  - href: b\n",
      expected: /actions\[0\]\.label is required/,
    },
    {
      name: "rejects an action entry with no href",
      block: "title: A\ndescription: B\nactions:\n  - label: A\n",
      expected: /actions\[0\]\.href is required/,
    },
    {
      name: "rejects malformed YAML",
      block: "title: A\n  description: [B\n",
      expected: /frontmatter is not valid YAML/,
    },
    {
      name: "rejects a block that is not a mapping",
      block: "- title\n- description\n",
      expected: /frontmatter must be a mapping of keys to values/,
    },
  ];

  for (const rejection of rejections) {
    it(rejection.name, () => {
      const { result, diagnostics } = parse(rejection.block);

      expect(result).toBeNull();
      expect(diagnostics.map((diagnostic) => diagnostic.message)).toEqual(
        expect.arrayContaining([expect.stringMatching(rejection.expected)]),
      );
    });
  }

  it("locates every diagnostic at the head of the file", () => {
    const { diagnostics } = parse("layout: default\n");

    expect(diagnostics.length).toBeGreaterThan(0);
    for (const diagnostic of diagnostics) {
      expect(diagnostic).toMatchObject({ file: "docs/page.md", line: 1, column: 1 });
    }
  });

  it("reports every problem in one pass rather than stopping at the first", () => {
    expect(messages("layout: default\nnav_order: second\n")).toHaveLength(4);
  });

  it("lists the accepted keys in the unknown-key message", () => {
    const [message] = messages("title: A\ndescription: B\nlayout: default\n");

    expect(message).toContain(
      "actions, description, hidden, nav_order, nav_title, permalink, title",
    );
  });

  it("keeps every valid action when one entry in the list is rejected", () => {
    const { diagnostics } = parse(
      "title: A\ndescription: B\nactions:\n  - label: Good\n    href: a\n  - label: Bad\n",
    );

    expect(diagnostics.map((diagnostic) => diagnostic.message)).toEqual([
      'actions[1].href is required and must be a non-empty string',
    ]);
  });
});
