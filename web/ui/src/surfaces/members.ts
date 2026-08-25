// The member-list parser both layer forms read. A visibility axis names its
// members as a comma-separated list in the form and as an array on the wire.

/** members splits a comma-separated member list into the identifiers a layer
 * request carries, dropping the empty runs a trailing separator or a stray
 * space leaves behind. */
export function members(raw: string): string[] {
  return raw
    .split(',')
    .map((member) => member.trim())
    .filter((member) => member !== '');
}
