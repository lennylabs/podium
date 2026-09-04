// The member-list parser both layer forms read. A visibility axis names its
// members as a comma-separated list in the form and as an array on the wire.
// The rest of the file backs the group typeahead the register form draws over
// that list.

import type { LayerRecord } from '../api';

/** members splits a comma-separated member list into the identifiers a layer
 * request carries, dropping the empty runs a trailing separator or a stray
 * space leaves behind. */
export function members(raw: string): string[] {
  return raw
    .split(',')
    .map((member) => member.trim())
    .filter((member) => member !== '');
}

/** grantedGroups is every group name already granted on the layers the caller
 * can see, deduplicated and ordered.
 *
 * §4.6 grants to a group name the identity provider supplies, and no registry
 * response enumerates that provider's groups, so the names already in use are
 * the only list of known-good group names a form can offer. A group typed here
 * and absent from that list is either the first grant to it or a typo, and the
 * reader is the one who can tell the two apart. */
export function grantedGroups(layers: readonly LayerRecord[]): string[] {
  const names = new Set<string>();
  for (const layer of layers) {
    for (const group of layer.groups ?? []) {
      const name = group.trim();
      if (name !== '') {
        names.add(name);
      }
    }
  }
  return [...names].sort((left, right) => left.localeCompare(right));
}

/** fragment is the member the reader is part-way through typing: whatever
 * follows the last separator. A typeahead narrows on it rather than on the
 * whole line, so the members already entered do not narrow the list to
 * nothing. */
export function fragment(raw: string): string {
  return raw.slice(raw.lastIndexOf(',') + 1).trim();
}

/** matchGroups narrows the known names to the ones the part-way member
 * matches, case-insensitively, dropping the names the line already carries so
 * a picked name cannot be picked twice. */
export function matchGroups(known: readonly string[], raw: string): string[] {
  const entered = new Set(members(raw).map((member) => member.toLowerCase()));
  const query = fragment(raw).toLowerCase();
  return known.filter((name) => {
    const folded = name.toLowerCase();
    return !entered.has(folded) && folded.includes(query);
  });
}

/** replaceFragment swaps the part-way member for the name the reader picked
 * and leaves a separator behind it, so picking one name and typing the next is
 * one continuous action. */
export function replaceFragment(raw: string, picked: string): string {
  const kept = members(raw.slice(0, raw.lastIndexOf(',') + 1)).filter(
    (member) => member.toLowerCase() !== picked.toLowerCase(),
  );
  return `${[...kept, picked].join(', ')}, `;
}

/** without drops one member from a list, which is what removing a token does
 * to the line behind it. */
export function without(list: readonly string[], dropped: string): string[] {
  return list.filter((member) => member !== dropped);
}

/** merge adds the members a form names to the ones a layer already carries,
 * keeping the stored list intact and dropping a name that repeats one of them.
 * §4.6 grants on an axis and withdraws on none, so a member list a form sends
 * is the stored grant plus what the reader added rather than a replacement. */
export function merge(stored: readonly string[], added: readonly string[]): string[] {
  const held = new Set(stored);
  return [...stored, ...added.filter((member) => !held.has(member))];
}
