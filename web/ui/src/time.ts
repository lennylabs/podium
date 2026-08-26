// Relative time, shared by every surface that states when a layer was last
// ingested. The sidebar footer and the layer panel report the same fact, so
// they render it through one function rather than each stating it its own
// way.

/** since renders an ingest timestamp as the age a surface states. A stamp
 * the browser cannot parse is reported as the stamp itself, because the
 * surface states what the response carried rather than a computed age it
 * could not derive. */
export function since(stamp: string, now: number): string {
  const at = Date.parse(stamp);
  if (Number.isNaN(at)) {
    return stamp;
  }
  const minutes = Math.max(Math.floor((now - at) / 60000), 0);
  if (minutes < 60) {
    return `${minutes}m ago`;
  }
  const hours = Math.floor(minutes / 60);
  if (hours < 24) {
    return `${hours}h ago`;
  }
  return `${Math.floor(hours / 24)}d ago`;
}
