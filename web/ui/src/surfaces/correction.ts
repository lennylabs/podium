// The spelling correction the palette offers a reader whose query matched
// nothing. No registry endpoint answers "did you mean", so the correction is
// derived here from the §4.5.2 catalog: the canonical ID of every artifact the
// caller can see is the only untruncated list of what the registry holds, and
// the words those IDs spell are the vocabulary a typo was aimed at.

import { formatQueryLine, parseQueryLine } from '../query';

/** editCap is the largest edit distance a correction may cross, measured
 * against the length of the word the reader typed. A fixed cap of two rewrites
 * a four-letter word into an unrelated one, so a short word gets one edit and
 * a longer word gets two. */
function editCap(word: string): number {
  return word.length <= 4 ? 1 : 2;
}

/** vocabularyOf collects the words a set of artifact IDs spells. A path
 * segment enters as itself and as the words the hyphens and underscores inside
 * it separate, so `pay-invoice` corrects a whole-token typo and `invoce` on
 * its own alike. */
export function vocabularyOf(ids: string[]): string[] {
  const terms = new Set<string>();
  for (const id of ids) {
    for (const segment of id.toLowerCase().split('/')) {
      if (segment === '') {
        continue;
      }
      terms.add(segment);
      for (const word of segment.split(/[-_.]/)) {
        if (word !== '') {
          terms.add(word);
        }
      }
    }
  }
  return [...terms];
}

/** distance is the Levenshtein distance between two words, over one row of the
 * matrix because only the last row is read. */
function distance(a: string, b: string): number {
  let row = Array.from({ length: b.length + 1 }, (_, at) => at);
  for (let i = 1; i <= a.length; i += 1) {
    const next = [i];
    for (let j = 1; j <= b.length; j += 1) {
      const substitute = row[j - 1] + (a[i - 1] === b[j - 1] ? 0 : 1);
      next.push(Math.min(substitute, row[j] + 1, next[j - 1] + 1));
    }
    row = next;
  }
  return row[b.length];
}

/** nearestTerm is the vocabulary word closest to one typed word, or null when
 * nothing is within the word's edit cap. A word the catalog already spells is
 * not a misspelling, however little it matched, so it is left alone. Ties
 * settle on the alphabetically first candidate so the same query always offers
 * the same correction. */
function nearestTerm(word: string, vocabulary: string[]): string | null {
  if (vocabulary.includes(word)) {
    return null;
  }
  const cap = editCap(word);
  let best: string | null = null;
  let bestAt = cap + 1;
  for (const term of vocabulary) {
    if (term === word || Math.abs(term.length - word.length) > cap) {
      continue;
    }
    const at = distance(word, term);
    if (at > cap) {
      continue;
    }
    if (at < bestAt || (at === bestAt && best !== null && term < best)) {
      best = term;
      bestAt = at;
    }
  }
  return best;
}

/**
 * correctQueryLine rewrites the free text of a query line to the nearest
 * spelling the catalog holds, and returns null when the line is already
 * spelled the way the catalog spells it or when nothing is near enough to
 * offer. The inline filters are carried through untouched: a correction
 * answers a misspelled word, and rewriting `type:` or `tag:` alongside it
 * would change what the reader asked for rather than how they spelled it.
 */
export function correctQueryLine(line: string, ids: string[]): string | null {
  const filters = parseQueryLine(line);
  if (filters.query === '') {
    return null;
  }
  const vocabulary = vocabularyOf(ids);
  let corrected = false;
  const words = filters.query.split(/\s+/).map((word) => {
    const near = nearestTerm(word.toLowerCase(), vocabulary);
    if (near === null) {
      return word;
    }
    corrected = true;
    return near;
  });
  if (!corrected) {
    return null;
  }
  return formatQueryLine({ ...filters, query: words.join(' ') });
}
