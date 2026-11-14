/*
 * The index layout the build writes and the browser reads. Both import it from
 * here, because MiniSearch reloads a serialized index only with the options it
 * was built with.
 *
 * A page contributes one entry for the page and one per section. The page
 * title and the section name are the same for every entry of a page, so they
 * are stored on the page entry alone, and a section hit reads them from there
 * by its route. Storing them on every entry repeated both strings once per
 * section, which was the largest avoidable cost in the serialized index.
 */

/** The fields MiniSearch stores. Any of them is absent where it would be empty. */
export type StoredFields = {
  /** Page title, on the page entry only. */
  title?: string;
  /** Heading text, on a section entry only. */
  heading?: string;
  /** Title of the page's section index, on the page entry only. */
  section?: string;
};

export const SEARCH_FIELDS = ["page", "heading", "text"];
export const STORED_FIELDS = ["title", "heading", "section"];
export const SEARCH_OPTIONS = { boost: { page: 3, heading: 2 }, prefix: true, fuzzy: 0.2 };

/**
 * The label and the context line a result shows. A section hit takes the page
 * title and the section name from its page's entry, which `lookup` returns for
 * a route.
 */
export function describeHit(
  hit: StoredFields & { id: string },
  lookup: (route: string) => StoredFields | undefined,
): { label: string; context: string } {
  const route = hit.id.split("#")[0] ?? hit.id;
  const owner = hit.id.includes("#") ? (lookup(route) ?? {}) : hit;
  const title = owner.title ?? "";
  const heading = hit.heading ?? "";
  return {
    label: heading === "" ? title : heading,
    context: [owner.section ?? "", title].filter((part) => part !== "").join(" · "),
  };
}
