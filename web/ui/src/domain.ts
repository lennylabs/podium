// Labelling a §4.2 domain the catalog reports.

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
