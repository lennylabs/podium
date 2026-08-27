// Content-hash presentation, shared by every surface that states one. The
// artifact rail and the ingest report both print a §6.4 content hash, and a
// 64-character digest set whole is a wall of hex that no reader reads, so
// they elide it through one function rather than each choosing its own
// width.

/** abbreviateHash elides the middle of a content hash. The algorithm prefix
 * identifies what was hashed and the ends of the digest are what a reader
 * compares against another copy, so both survive. `lead` is how many digest
 * characters stand before the ellipsis: a hash read on its own needs few,
 * while two hashes set against each other need more before the reader can
 * tell them apart. A digest short enough to stand whole is left alone. */
export function abbreviateHash(hash: string, lead = 4): string {
  const separator = hash.indexOf(':');
  const algorithm = separator === -1 ? '' : hash.slice(0, separator + 1);
  const digest = hash.slice(separator + 1);
  if (digest.length <= lead + 8) {
    return hash;
  }
  return `${algorithm}${digest.slice(0, lead)}…${digest.slice(-4)}`;
}
