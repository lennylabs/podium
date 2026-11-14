import type { Element, RootContent } from "hast";
import MiniSearch from "minisearch";

import {
  SEARCH_FIELDS,
  SEARCH_OPTIONS,
  STORED_FIELDS,
  type StoredFields,
} from "../client/search-fields";
import { processTerm } from "../client/search-terms";
import type { PageModel } from "./types";

export type SearchDocument = {
  /**
   * URL to open, including any heading fragment. MiniSearch keeps every
   * document ID in the serialized index, so the route is read back from the ID
   * rather than stored a second time as a field, which would add every route's
   * bytes to the index again.
   */
  id: string;
  /** Page title, shown as the result's context. */
  page: string;
  /** Heading text, empty for a whole-page entry. */
  heading: string;
  section: string;
  text: string;
};

/*
 * Only h2 and h3 start a search entry. A deeper heading folds into the entry of
 * the section above it, heading text included, so a hit still lands within one
 * screen of the answer. Every entry adds its terms' postings to the serialized
 * index, and reference pages carry many short h4 and h5 subsections, so an
 * entry per subsection grows the index with headings rather than with prose.
 */
const HEADING_TAGS = new Set(["h2", "h3"]);
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
 * What the index receives. Every entry is searched by its page title, so a
 * section ranks with its page, but the title and the section name are stored
 * on the page entry alone (search-fields.ts).
 */
type IndexedDocument = { id: string; page: string; text: string } & StoredFields;

function toIndexed(doc: SearchDocument): IndexedDocument {
  const indexed: IndexedDocument = { id: doc.id, page: doc.page, text: doc.text };
  if (doc.id.includes("#")) {
    indexed.heading = doc.heading;
  } else {
    indexed.title = doc.page;
    if (doc.section !== "") indexed.section = doc.section;
  }
  return indexed;
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

  const index = new MiniSearch<IndexedDocument>({
    fields: SEARCH_FIELDS,
    storeFields: STORED_FIELDS,
    processTerm,
    searchOptions: SEARCH_OPTIONS,
  });

  index.addAll(documents.map(toIndexed));

  return {
    serialized: JSON.stringify(index.toJSON()),
    documentCount: documents.length,
  };
}
