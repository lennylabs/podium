import { afterEach, describe, expect, it } from "vitest";

import { disposeHighlighter } from "../src/build/content/highlight";
import { buildCorpus } from "../src/build/content/pipeline";
import { renderRedirect } from "../src/build/redirects";
import { registry } from "../src/components/islands/registry";
import { makeCorpus, type TempCorpus } from "./support/corpus";

const PAGES = {
  "guide/new.md": "---\ntitle: New\ndescription: The new page.\n---\n\n# New\n\n## Setup\n\nText.\n\n## Upgrades\n\nText.\n",
  "ops/backup.md": "---\ntitle: Backup\ndescription: Backups.\n---\n\n# Backup\n\n## Restore\n\nText.\n",
};

let corpus: TempCorpus | null = null;

afterEach(() => {
  corpus?.dispose();
  corpus = null;
  disposeHighlighter();
});

async function build(redirects: string) {
  corpus = makeCorpus({ ...PAGES, "_redirects.yaml": redirects });
  return buildCorpus(corpus.config, registry);
}

describe("redirects", () => {
  it("publishes the old route and resolves the target and every moved anchor", async () => {
    const built = await build(
      "- from: guide/old\n  to: guide/new\n  anchors:\n    restore: ops/backup#restore\n",
    );

    expect(built.diagnostics).toEqual([]);
    expect(built.redirects).toEqual([
      {
        route: "/guide/old.html",
        target: "/guide/new.html",
        anchors: { restore: "/ops/backup.html#restore" },
      },
    ]);
    expect(built.index.routes.has("/guide/old.html")).toBe(true);
  });

  it("maps a directory entry to the section's index route", async () => {
    const built = await build("- from: legacy/\n  to: guide/new\n");

    expect(built.redirects[0]?.route).toBe("/legacy/index.html");
  });

  it("reports a target page that is not published", async () => {
    const built = await build("- from: guide/old\n  to: guide/missing\n");

    expect(built.redirects).toEqual([]);
    expect(built.diagnostics.map((d) => d.message)).toEqual([
      'link "guide/missing" resolves to "guide/missing", which is not published',
    ]);
  });

  it("reports an anchor target that matches no heading", async () => {
    const built = await build(
      "- from: guide/old\n  to: guide/new\n  anchors:\n    gone: ops/backup#nowhere\n",
    );

    expect(built.diagnostics.map((d) => d.message)).toEqual([
      'link "ops/backup#nowhere" points at "#nowhere", which matches no heading on that page',
    ]);
  });

  it("refuses to shadow a page that is still published", async () => {
    const built = await build("- from: ops/backup\n  to: guide/new\n");

    expect(built.redirects).toEqual([]);
    expect(built.diagnostics.map((d) => d.message)).toEqual([
      '"ops/backup" is still a published page, so it cannot redirect',
    ]);
  });

  it("refuses a duplicate entry and a malformed one", async () => {
    const built = await build(
      "- from: guide/old\n  to: guide/new\n- from: guide/old\n  to: ops/backup\n- from: 3\n",
    );

    expect(built.redirects).toHaveLength(1);
    expect(built.diagnostics.map((d) => d.message)).toEqual([
      '"guide/old" is listed more than once',
      "every entry needs a string from and a string to",
    ]);
  });

  it("reports a file that is not a list", async () => {
    const built = await build("from: guide/old\n");

    expect(built.diagnostics.map((d) => d.message)).toEqual([
      "expected a list of redirect entries",
    ]);
  });

  it("publishes nothing extra when the corpus has no redirect file", async () => {
    corpus = makeCorpus(PAGES);
    const built = await buildCorpus(corpus.config, registry);

    expect(built.redirects).toEqual([]);
    expect(built.diagnostics).toEqual([]);
  });
});

describe("renderRedirect", () => {
  const html = renderRedirect({
    route: "/guide/old.html",
    target: "/base/guide/new.html",
    anchors: { restore: "/base/ops/backup.html#restore" },
  });

  it("forwards without JavaScript through the refresh and the link", () => {
    expect(html).toContain('<meta http-equiv="refresh" content="0; url=/base/guide/new.html">');
    expect(html).toContain('<a href="/base/guide/new.html">');
    expect(html).toContain('<meta name="robots" content="noindex">');
  });

  function forward(hash: string): string {
    const script = /<script>([\s\S]*?)<\/script>/.exec(html)?.[1] ?? "";
    let replaced = "";
    const location = { hash, replace: (url: string) => (replaced = url) };
    new Function("location", script)(location);
    return replaced;
  }

  it("sends a moved anchor to the page that now holds it", () => {
    expect(forward("#restore")).toBe("/base/ops/backup.html#restore");
  });

  it("keeps an unmapped anchor on the default target", () => {
    expect(forward("#setup")).toBe("/base/guide/new.html#setup");
    expect(forward("")).toBe("/base/guide/new.html");
  });
});
