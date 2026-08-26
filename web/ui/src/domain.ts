// Labelling a §4.2 domain the catalog reports.

import type { DomainDescriptor } from './api';

/** domainLabel is the label an entry under `parent` carries. A §4.5.5 sparse
 * chain is collapsed by the server into one entry whose path holds every
 * segment it crossed while its name holds only the last one, so a label drawn
 * from the name puts `finance/ap` on screen as `ap` under the root: it states
 * a position in the hierarchy that domain does not hold, and two domains that
 * end in the same segment become indistinguishable. The label is the whole
 * stretch of path the entry navigates across, which leaves an unfolded entry
 * on its own segment. */
export function domainLabel(path: string, parent: string): string {
  const prefix = parent === '' ? '' : `${parent}/`;
  return path.startsWith(prefix) ? path.slice(prefix.length) : path;
}

/** marksCurrentDomain reports whether the tree entry at `path` under `parent`
 * is the row that holds the domain the reader is on. A §4.5.5 sparse chain is
 * collapsed by the server into one entry, so the levels between `parent` and
 * `path` have no row of their own: a route onto one of them belongs to the
 * chain entry that swallowed it, which is the only row on screen that states
 * where the reader is. Without this the sidebar marks nothing at all on those
 * levels while the chain's own endpoint marks correctly. */
export function marksCurrentDomain(path: string, parent: string, current: string | null): boolean {
  if (current === null) {
    return false;
  }
  if (current === path) {
    return true;
  }
  const prefix = parent === '' ? '' : `${parent}/`;
  return path.startsWith(`${current}/`) && current.startsWith(prefix) && current !== parent;
}

/** subdomainCountLabel states what a response reported below a child. An entry
 * with an empty subtree carries no line at all, because a card that reads
 * "0 subdomains" claims a fact the descriptor omits at the deepest returned
 * level rather than one it reports. */
export function subdomainCountLabel(count: number): string | null {
  if (count === 0) {
    return null;
  }
  return `${String(count)} ${count === 1 ? 'subdomain' : 'subdomains'}`;
}

/** artifactCountLabel states how many artifacts a catalog read found under a
 * child. A zero is reported rather than dropped: the catalog is the whole
 * visible set under the scope, so an empty child is a fact the read returned
 * rather than one it left out. */
export function artifactCountLabel(count: number): string {
  return `${String(count)} ${count === 1 ? 'artifact' : 'artifacts'}`;
}

/** artifactCounts attributes every catalog ID under `parent` to the child it
 * falls beneath, so one catalog read counts the whole grid. A count covers the
 * child's entire subtree, because a tile that counted only the artifacts
 * directly inside the child would read as zero for a domain that carries all
 * of its artifacts one level down.
 *
 * The walk descends the ID's own segments and stops at the first child path it
 * matches, which is what makes a §4.5.5 collapsed chain countable: such a child
 * carries every segment it crossed in its path, and no ancestor of it appears
 * in the grid. The last segment of an ID names the artifact rather than a
 * domain, so it is never matched. */
export function artifactCounts(ids: string[], children: string[], parent: string): Map<string, number> {
  const target = new Set(children);
  const counts = new Map<string, number>();
  for (const child of children) {
    counts.set(child, 0);
  }
  const prefix = parent === '' ? '' : `${parent}/`;
  for (const id of ids) {
    if (!id.startsWith(prefix)) {
      continue;
    }
    const segments = id.slice(prefix.length).split('/');
    let held = parent;
    for (const segment of segments.slice(0, -1)) {
      held = held === '' ? segment : `${held}/${segment}`;
      if (target.has(held)) {
        counts.set(held, (counts.get(held) ?? 0) + 1);
        break;
      }
    }
  }
  return counts;
}

/** directArtifactCount is how many artifacts a §4.5.2 catalog read found
 * directly inside `path`, rather than anywhere below it. The catalog is the
 * whole visible set under the scope and the registry does not truncate it, so
 * this is what the domain holds against what a `load_domain` listing returned.
 *
 * The count is of direct children alone, because the listing it is compared
 * against is: an artifact one level down reaches the page as a folded entry
 * with its own group, or not at all. */
export function directArtifactCount(ids: string[], path: string): number {
  let count = 0;
  for (const id of ids) {
    const cut = id.lastIndexOf('/');
    if ((cut === -1 ? '' : id.slice(0, cut)) === path) {
      count += 1;
    }
  }
  return count;
}

/** scopePaths expands the subtree a load_domain response returned under
 * `parent` into every domain path a scope filter can name. Two structures put
 * a domain somewhere other than an entry's own `path`. A §4.5.5 sparse chain
 * arrives folded into one entry, so a list drawn from the entry paths alone
 * offers `finance/ap` and never `finance`; and an entry that expanded carries
 * its children in its own `subdomains`, so a list drawn from the top level
 * alone offers `edge` and never `edge/child-one`. Both are domains the browser
 * navigates to and the search matches by prefix, so the walk descends into
 * every entry's subtree and each entry contributes its intermediate segments
 * as well as itself, in root-to-leaf order and without repeating a path two
 * entries share. */
export function scopePaths(
  subdomains: DomainDescriptor[],
  parent: string,
): string[] {
  const prefix = parent === '' ? '' : `${parent}/`;
  const seen = new Set<string>();
  const options: string[] = [];
  const walk = (entries: DomainDescriptor[]): void => {
    for (const entry of entries) {
      if (entry.path.startsWith(prefix)) {
        let held = parent;
        for (const segment of entry.path.slice(prefix.length).split('/')) {
          if (segment === '') {
            continue;
          }
          held = held === '' ? segment : `${held}/${segment}`;
          if (!seen.has(held)) {
            seen.add(held);
            options.push(held);
          }
        }
      }
      walk(entry.subdomains ?? []);
    }
  };
  walk(subdomains);
  return options;
}
