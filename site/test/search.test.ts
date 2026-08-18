import { describe, expect, it } from "vitest";

import { buildSearchIndex, documentsFor } from "../src/build/search";
import type { PageModel } from "../src/build/types";
import { toHast } from "./support/hast";

type PageOverrides = Omit<Partial<PageModel>, "body"> & { route: string; body: string };

/** A page model whose body is written as markdown, which the helper parses. */
function page(overrides: PageOverrides): PageModel {
  const { body, ...rest } = overrides;
  return {
    sourcePath: `docs${overrides.route.replace(/\.html$/, ".md")}`,
    title: "Install",
    displayTitle: "Install",
    navTitle: null,
    description: "Install the fixture tool.",
    navOrder: null,
    hidden: false,
    sectionRoute: null,
    headings: [],
    actions: [],
    islands: [],
    editUrl: "",
    updatedAt: "",
    text: "",
    ...rest,
    body: toHast(body),
  };
}

describe("documentsFor", () => {
  it("emits one page entry and one entry per section", () => {
    const docs = documentsFor(
      page({
        route: "/guide/install.html",
        body: "Lead prose.\n\n## Requirements\n\nA machine.\n\n## Steps\n\nRun the command.\n",
      }),
      "Guide",
    );

    expect(docs.map((doc) => doc.id)).toEqual([
      "/guide/install.html",
      "/guide/install.html#requirements",
      "/guide/install.html#steps",
    ]);
    expect(docs.map((doc) => doc.heading)).toEqual(["", "Requirements", "Steps"]);
    expect(docs.every((doc) => doc.section === "Guide")).toBe(true);
  });

  it("keeps prose before the first heading on the page entry", () => {
    const [pageDoc] = documentsFor(
      page({ route: "/a.html", body: "Lead prose.\n\n## Requirements\n\nA machine.\n" }),
      "",
    );

    expect(pageDoc?.text).toBe("Install the fixture tool. Lead prose.");
  });

  it("carries only the description when the body opens with a heading", () => {
    const [pageDoc] = documentsFor(
      page({ route: "/a.html", body: "## Requirements\n\nA machine.\n" }),
      "",
    );

    expect(pageDoc?.text).toBe("Install the fixture tool.");
  });

  it("opens a hit at the heading that carries the answer", () => {
    const docs = documentsFor(
      page({ route: "/a.html", body: "## Requirements\n\nA machine running Linux.\n" }),
      "",
    );

    expect(docs[1]).toMatchObject({
      route: "/a.html#requirements",
      text: "A machine running Linux.",
    });
  });

  it("excludes fenced code from a section entry", () => {
    const docs = documentsFor(
      page({ route: "/a.html", body: "## Steps\n\n```bash\npodium sync\n```\n" }),
      "",
    );

    expect(docs[1]?.text).toBe("");
  });

  it("starts no section for a heading with no id", () => {
    const model = page({ route: "/a.html", body: "## Requirements\n\nA machine.\n" });
    const heading = model.body.children.find(
      (node) => node.type === "element" && node.tagName === "h2",
    );
    if (heading?.type === "element") heading.properties = {};

    expect(documentsFor(model, "")).toHaveLength(1);
  });

  it("emits one page entry for a body with no headings", () => {
    const docs = documentsFor(page({ route: "/a.html", body: "Prose only.\n" }), "");

    expect(docs).toHaveLength(1);
    expect(docs[0]?.text).toBe("Install the fixture tool. Prose only.");
  });
});

describe("buildSearchIndex", () => {
  const pages = [
    page({
      route: "/guide/index.html",
      title: "Guide",
      body: "The guide covers installation.\n",
    }),
    page({
      route: "/guide/install.html",
      title: "Install",
      sectionRoute: "/guide/index.html",
      body: "## Requirements\n\nA machine running Linux.\n",
    }),
    page({
      route: "/guide/secret.html",
      title: "Secret",
      hidden: true,
      body: "## Hidden\n\nNever indexed.\n",
    }),
  ];

  const sectionTitles = new Map([["/guide/index.html", "Guide"]]);

  it("indexes every visible page and excludes hidden ones", () => {
    const index = buildSearchIndex(pages, sectionTitles);

    expect(index.documentCount).toBe(3);
    expect(index.serialized).not.toContain("Secret");
  });

  it("serializes to JSON the browser can load", () => {
    const parsed = JSON.parse(buildSearchIndex(pages, sectionTitles).serialized) as {
      documentCount: number;
    };

    expect(parsed.documentCount).toBe(3);
  });

  it("names the section each entry belongs to", () => {
    const serialized = buildSearchIndex(pages, sectionTitles).serialized;

    expect(serialized).toContain("Guide");
  });

  it("leaves the section empty for a page whose section title is unknown", () => {
    const orphan = page({
      route: "/loose.html",
      sectionRoute: "/missing/index.html",
      body: "Prose.\n",
    });

    expect(documentsFor(orphan, "")[0]?.section).toBe("");
    expect(buildSearchIndex([orphan], sectionTitles).documentCount).toBe(1);
  });

  it("indexes nothing for an empty corpus", () => {
    expect(buildSearchIndex([], sectionTitles).documentCount).toBe(0);
  });
});
