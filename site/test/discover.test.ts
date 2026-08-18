import { writeFileSync } from "node:fs";
import { join } from "node:path";

import { describe, expect, it } from "vitest";

import { discover, lastModified, routeForPage } from "../src/build/discover";
import { REPO_ROOT, loadConfig } from "../src/build/config";
import { FIXTURE_CORPUS, configFor, makeCorpus } from "./support/corpus";

describe("routeForPage", () => {
  const cases: Array<{ source: string; permalink: string | null; expected: string }> = [
    { source: "overview.md", permalink: null, expected: "/overview.html" },
    { source: "guide/install.md", permalink: null, expected: "/guide/install.html" },
    { source: "guide/index.md", permalink: null, expected: "/guide/index.html" },
    { source: "a.md", permalink: "/", expected: "/index.html" },
    { source: "a.md", permalink: "/guide/", expected: "/guide/index.html" },
    { source: "a.md", permalink: "/named.html", expected: "/named.html" },
  ];

  for (const entry of cases) {
    it(`maps ${entry.source} with permalink ${String(entry.permalink)} to ${entry.expected}`, () => {
      expect(routeForPage(entry.source, entry.permalink)).toBe(entry.expected);
    });
  }
});

describe("discover", () => {
  it("classifies the fixture corpus into pages and static files", () => {
    const result = discover(configFor(FIXTURE_CORPUS, FIXTURE_CORPUS));

    expect(result.diagnostics).toEqual([]);
    expect(result.pages.map((page) => page.source).sort()).toEqual([
      "guide/hidden.md",
      "guide/index.md",
      "guide/install.md",
      "guide/tabs.md",
      "overview.md",
      "reference/cli.md",
      "reference/index.md",
    ]);
    expect(result.statics.map((file) => file.route).sort()).toEqual([
      "/assets/diagrams/sample.svg",
      "/notes.md",
    ]);
  });

  it("records the line the body starts on so a diagnostic names the file's own line", () => {
    const result = discover(configFor(FIXTURE_CORPUS, FIXTURE_CORPUS));
    const overview = result.pages.find((page) => page.source === "overview.md");

    expect(overview?.bodyStartLine).toBe(11);
    expect(overview?.body.startsWith("\n# Overview")).toBe(true);
  });

  it("reports a page whose frontmatter is rejected and keeps it out of the corpus", () => {
    const corpus = makeCorpus({ "a.md": "---\ntitle: A\n---\n\n# A\n" });

    try {
      const result = discover(corpus.config);

      expect(result.pages).toEqual([]);
      expect(result.diagnostics[0]?.message).toContain('"description" is required');
    } finally {
      corpus.dispose();
    }
  });

  it("walks a corpus in sorted order so the output is reproducible", () => {
    const corpus = makeCorpus({
      "b.md": "---\ntitle: B\ndescription: B\n---\n\n# B\n",
      "a.md": "---\ntitle: A\ndescription: A\n---\n\n# A\n",
      "z/c.md": "---\ntitle: C\ndescription: C\n---\n\n# C\n",
    });

    try {
      expect(discover(corpus.config).pages.map((page) => page.source)).toEqual([
        "a.md",
        "b.md",
        "z/c.md",
      ]);
    } finally {
      corpus.dispose();
    }
  });
});

describe("lastModified", () => {
  it("reads a commit date for every tracked file under docs", () => {
    const dates = lastModified(loadConfig({ repoRoot: REPO_ROOT, version: "0.0.0-test" }));

    expect(dates.size).toBeGreaterThan(0);
    for (const value of dates.values()) {
      expect(value).toMatch(/^\d{4}-\d{2}-\d{2}T/);
    }
  });

  it("returns an empty map when the repository has no history to read", () => {
    const config = configFor("/nonexistent-repository-root", "/nonexistent-repository-root/docs");

    expect(lastModified(config)).toEqual(new Map());
  });
});

describe("the include frontmatter key", () => {
  const page = [
    "---",
    "title: Changelog",
    "description: Release history.",
    "include: CHANGELOG.md",
    "---",
    "",
    "# Changelog",
    "",
    "Lead paragraph.",
    "",
  ].join("\n");

  it("appends the named repo-root file to the page body", () => {
    const corpus = makeCorpus({ "about/changelog.md": page });
    writeFileSync(
      join(corpus.repoRoot, "CHANGELOG.md"),
      "# Changelog\n\nPreamble.\n\n## [0.1.0] - 2026-05-11\n\nFirst release.\n",
    );

    const { pages, diagnostics } = discover(corpus.config);
    const body = pages.find((entry) => entry.source === "about/changelog.md")?.body ?? "";

    expect(diagnostics).toEqual([]);
    expect(body).toContain("Lead paragraph.");
    expect(body).toContain("## [0.1.0] - 2026-05-11");
    expect(body).toContain("First release.");
    corpus.dispose();
  });

  it("drops the included file's own title so the page keeps one h1", () => {
    const corpus = makeCorpus({ "about/changelog.md": page });
    writeFileSync(join(corpus.repoRoot, "CHANGELOG.md"), "# Changelog\n\nPreamble.\n");

    const { pages } = discover(corpus.config);
    const body = pages.find((entry) => entry.source === "about/changelog.md")?.body ?? "";

    expect(body.match(/^# /gm)).toHaveLength(1);
    expect(body).toContain("Preamble.");
    corpus.dispose();
  });

  it("reports a missing include against the page that declared it", () => {
    const corpus = makeCorpus({ "about/changelog.md": page });

    const { diagnostics } = discover(corpus.config);

    expect(diagnostics).toHaveLength(1);
    expect(diagnostics[0]?.message).toContain("CHANGELOG.md");
    expect(diagnostics[0]?.file).toContain("changelog.md");
    corpus.dispose();
  });

  it("refuses a path that escapes the repository root", () => {
    const corpus = makeCorpus({
      "about/changelog.md": page.replace("include: CHANGELOG.md", "include: ../../etc/passwd"),
    });

    const { diagnostics } = discover(corpus.config);

    expect(diagnostics.some((entry) => entry.message.includes('"include"'))).toBe(true);
    corpus.dispose();
  });
});
