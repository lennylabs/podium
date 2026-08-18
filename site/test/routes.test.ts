import { readFileSync } from "node:fs";
import { resolve } from "node:path";

import { afterAll, beforeAll, describe, expect, it } from "vitest";

import { REPO_ROOT, SITE_DIR, loadConfig } from "../src/build/config";
import { disposeHighlighter } from "../src/build/content/highlight";
import { buildCorpus, type BuiltCorpus } from "../src/build/content/pipeline";
import { buildNav, flattenNav } from "../src/build/nav";
import { runChecks } from "../src/build/check";
import { buildSearchIndex } from "../src/build/search";
import { validateRegistry } from "../src/components/islands/props";
import { registry } from "../src/components/islands/registry";
import type { NavNode, SiteConfig } from "../src/build/types";

/**
 * Every route the corpus publishes. Regenerate with
 * test/fixtures/capture-routes.mjs after a deliberate rename, so an
 * unintended one fails here instead of shipping.
 */
const PUBLISHED: string[] = JSON.parse(
  readFileSync(resolve(SITE_DIR, "test/fixtures/published-routes.json"), "utf8"),
) as string[];

describe("the repository documentation tree", () => {
  let config: SiteConfig;
  let corpus: BuiltCorpus;
  let nav: NavNode[];

  beforeAll(async () => {
    config = loadConfig({
      repoRoot: REPO_ROOT,
      // The published site lives under this prefix, and the README links the
      // check reads are written against it.
      basePath: "/podium",
      version: "0.0.0-test",
    });
    corpus = await buildCorpus(config, registry);
    nav = buildNav(corpus.pages);
  }, 60_000);

  afterAll(() => {
    disposeHighlighter();
  });

  it("builds with no diagnostics", () => {
    expect(corpus.diagnostics).toEqual([]);
  });

  it("passes the registry validation the build runs first", () => {
    expect(validateRegistry(registry)).toEqual([]);
  });

  it("passes every corpus-level check", () => {
    const sectionTitles = new Map(corpus.pages.map((page) => [page.route, page.title]));
    const search = buildSearchIndex(corpus.pages, sectionTitles);

    expect(
      runChecks({
        config,
        pages: corpus.pages,
        nav,
        index: corpus.index,
        searchIndexBytes: Buffer.byteLength(search.serialized),
      }),
    ).toEqual([]);
  });

  it("still emits every route the previous build published", () => {
    const missing = PUBLISHED.filter((route) => !corpus.index.routes.has(route));

    expect(missing).toEqual([]);
  });

  it("emits the landing page and the new overview page", () => {
    expect(corpus.index.routes.has("/index.html")).toBe(true);
    expect(corpus.index.routes.has("/overview.html")).toBe(true);
  });

  it("reaches every published page from the navigation tree", () => {
    const reachable = new Set(flattenNav(nav).map((entry) => entry.route));
    const unreachable = corpus.pages
      .filter((page) => !page.hidden && !reachable.has(page.route))
      .map((page) => page.route);

    expect(unreachable).toEqual([]);
  });

  it("keeps the serialized search index inside the declared limit", () => {
    const sectionTitles = new Map(corpus.pages.map((page) => [page.route, page.title]));
    const search = buildSearchIndex(corpus.pages, sectionTitles);

    expect(Buffer.byteLength(search.serialized)).toBeLessThanOrEqual(
      config.searchIndexLimitBytes,
    );
  });
});
