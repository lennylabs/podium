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

/** elapsed renders how long a request was open, in the words the reingest
 * report states it in. The registry runs the whole §7.3.1 pipeline inside
 * the request, so a reader who waited minutes for the answer is told how
 * long that was rather than being left to guess. */
export function elapsed(ms: number): string {
  const seconds = Math.max(Math.round(ms / 1000), 0);
  const minutes = Math.floor(seconds / 60);
  const rest = seconds % 60;
  const secondPart = `${rest} ${rest === 1 ? 'second' : 'seconds'}`;
  if (minutes === 0) {
    return secondPart;
  }
  const minutePart = `${minutes} ${minutes === 1 ? 'minute' : 'minutes'}`;
  return rest === 0 ? minutePart : `${minutePart} ${secondPart}`;
}

/** clock renders a wall-clock stamp in UTC, which is what a report of a
 * finished run states. The zone is named rather than left to the reader's
 * locale, because the stamp is quoted into an issue or a chat message
 * alongside the registry's own logs. */
export function clock(at: number): string {
  const when = new Date(at);
  const pad = (value: number) => String(value).padStart(2, '0');
  return `${pad(when.getUTCHours())}:${pad(when.getUTCMinutes())}:${pad(when.getUTCSeconds())} UTC`;
}
