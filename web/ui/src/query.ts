// The query line and the filters it carries. The palette types the filters
// inline and the search surface renders them as pills, and both reach the
// same endpoint with the same arguments, so the parse lives here rather than
// in either surface.

import type { SearchFilters } from './api';

/**
 * parseQueryLine splits a typed line into the filters the search endpoint
 * takes. `type:`, `tag:`, and `scope:` are the inline form of the pills the
 * search surface renders, and whatever is left of the line is the query text.
 */
export function parseQueryLine(line: string): SearchFilters {
  const filters: SearchFilters = { query: '', type: '', scope: '', tags: [] };
  const words: string[] = [];
  for (const word of line.split(/\s+/)) {
    const [head, ...rest] = word.split(':');
    const value = rest.join(':');
    if (value === '') {
      if (word !== '') {
        words.push(word);
      }
      continue;
    }
    switch (head) {
      case 'type':
        filters.type = value;
        break;
      case 'scope':
        filters.scope = value;
        break;
      case 'tag':
        filters.tags.push(value);
        break;
      default:
        words.push(word);
    }
  }
  filters.query = words.join(' ');
  return filters;
}

/**
 * formatQueryLine writes the filters back as the line parseQueryLine reads,
 * so a search the reader assembled from pills and typed text addresses the
 * same route the palette would have issued for it. The two are inverse over
 * every line the surface produces, which is what lets the search surface hold
 * its state in the location hash.
 */
export function formatQueryLine(filters: SearchFilters): string {
  const words: string[] = [];
  if (filters.type !== '') {
    words.push(`type:${filters.type}`);
  }
  if (filters.scope !== '') {
    words.push(`scope:${filters.scope}`);
  }
  for (const tag of filters.tags) {
    words.push(`tag:${tag}`);
  }
  if (filters.query !== '') {
    words.push(filters.query);
  }
  return words.join(' ');
}

/**
 * hasFilters reports whether a parsed line carries any of the filters the
 * search surface exposes as pills. An empty state advises dropping a filter
 * only when the line actually carries one.
 */
export function hasFilters(filters: SearchFilters): boolean {
  return filters.type !== '' || filters.scope !== '' || filters.tags.length > 0;
}
