/*
 * The term filter the search index is built with and queried with. The build
 * and the browser must apply the same filter, because MiniSearch matches query
 * terms against the terms the index stored, so both import it from here.
 *
 * Common English function words carry a posting for nearly every section of
 * the documentation and never decide a match, so they are left out of the
 * index. Dropping them keeps the serialized index within its size budget as
 * the documentation grows. Single characters are dropped for the same reason.
 */

const STOP_WORDS = new Set(
  (
    "a all also an and any are as at be been but by can could do does each either for from " +
    "has have how if in into is it its may more must no nor not of on one only or other our " +
    "same should so some such than that the their them then there these they this those " +
    "through to under until upon via was we were what when where whether which while who " +
    "will with within without would"
  ).split(" "),
);

/** Lower-cases a term, or returns null to leave it out of the index and the query. */
export function processTerm(term: string): string | null {
  const lower = term.toLowerCase();
  if (lower.length < 2 || STOP_WORDS.has(lower)) return null;
  return lower;
}
