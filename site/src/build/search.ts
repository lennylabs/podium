import type { Element, RootContent } from "hast";
import MiniSearch from "minisearch";

import type { PageModel } from "./types";

export type SearchDocument = {
  id: string;
  /** URL to open, including any heading fragment. */
  route: string;
  /** Page title, shown as the result's context. */
  page: string;
  /** Heading text, empty for a whole-page entry. */
  heading: string;
  section: string;
  text: string;
};

const HEADING_TAGS = new Set(["h2", "h3", "h4", "h5", "h6"]);
const NON_PROSE = new Set(["pre", "script", "style"]);

function textOf(node: RootContent): string {
  if (node.type === "text") return node.value;
  if (node.type !== "element") return "";
  if (NON_PROSE.has(node.tagName)) return "";
  return node.children.map(textOf).join(" ");
}

function isHeading(node: RootContent): node is Element {
  return node.type === "element" && HEADING_TAGS.has(node.tagName);
}

/**
 * Splits a page into one document per section, so a hit can open at the heading
 * that carries the answer rather than at the top of a long page.
 */
export function documentsFor(page: PageModel, sectionTitle: string): SearchDocument[] {
  // The page-level entry carries the title and the summary only. Its prose is
  // covered by the section entries below, and indexing it twice roughly doubles
  // the index for no gain in what a reader can find.
  const docs: SearchDocument[] = [
    {
      id: page.route,
      route: page.route,
      page: page.title,
      heading: "",
      section: sectionTitle,
      text: page.description,
    },
  ];

  // Prose before the first heading has no section entry to fall into, so it is
  // kept on the page entry.
  const lead: string[] = [];
  for (const node of page.body.children) {
    if (isHeading(node)) break;
    lead.push(textOf(node));
  }
  const leadText = lead.join(" ").replace(/\s+/g, " ").trim();
  if (leadText !== "") docs[0]!.text = `${page.description} ${leadText}`.trim();

  let current: { id: string; heading: string; parts: string[] } | null = null;
  const flush = (): void => {
    if (current === null) return;
    const text = current.parts.join(" ").replace(/\s+/g, " ").trim();
    docs.push({
      id: `${page.route}#${current.id}`,
      route: `${page.route}#${current.id}`,
      page: page.title,
      heading: current.heading,
      section: sectionTitle,
      text,
    });
    current = null;
  };

  for (const node of page.body.children) {
    if (isHeading(node)) {
      flush();
      const id = typeof node.properties?.["id"] === "string" ? node.properties["id"] : "";
      if (id !== "") current = { id, heading: textOf(node).trim(), parts: [] };
      continue;
    }
    if (current !== null) current.parts.push(textOf(node));
  }
  flush();

  return docs;
}

/**
 * Builds the index the browser loads on the reader's first search.
 *
 * Hidden pages are published but excluded here, which is what `hidden: true`
 * means.
 */
export function buildSearchIndex(
  pages: PageModel[],
  sectionTitles: Map<string, string>,
): { serialized: string; documentCount: number } {
  const documents = pages
    .filter((page) => !page.hidden)
    .flatMap((page) =>
      documentsFor(
        page,
        page.sectionRoute === null ? "" : (sectionTitles.get(page.sectionRoute) ?? ""),
      ),
    );

  const index = new MiniSearch<SearchDocument>({
    fields: ["page", "heading", "text"],
    storeFields: ["route", "page", "heading", "section"],
    searchOptions: {
      boost: { page: 3, heading: 2 },
      prefix: true,
      fuzzy: 0.2,
    },
  });

  index.addAll(documents);

  return {
    serialized: JSON.stringify(index.toJSON()),
    documentCount: documents.length,
  };
}
