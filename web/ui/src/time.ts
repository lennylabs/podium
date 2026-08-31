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

/** stopwatch renders a wait that is still running, as the counter a reader
 * watches tick. The finished report states its duration in words, because it
 * is read once and quoted; a clock that advances every second is read at a
 * glance and is therefore digits, minutes and seconds, with an hour part only
 * once the wait has one. */
export function stopwatch(ms: number): string {
  const seconds = Math.max(Math.floor(ms / 1000), 0);
  const hours = Math.floor(seconds / 3600);
  const minutes = Math.floor(seconds / 60) % 60;
  const rest = seconds % 60;
  if (hours === 0) {
    return `${String(minutes)}:${pad(rest)}`;
  }
  return `${String(hours)}:${pad(minutes)}:${pad(rest)}`;
}

/** zone names the reader's own time zone, as the short name the platform
 * gives it (`PDT`, `CEST`, `UTC`) or the GMT offset where it has no short
 * name. Every absolute time this UI states is stated in the reader's zone and
 * carries this name, so a stamp read off one surface can be compared against
 * a stamp read off another and can be quoted into an issue without the reader
 * having to say which clock it came from. */
export function zone(at: Date): string {
  const named = new Intl.DateTimeFormat('en-US', { timeZoneName: 'short' })
    .formatToParts(at)
    .find((part) => part.type === 'timeZoneName');
  // Every implementation that honours timeZoneName emits the part, so the
  // offset below is a stand-in for one that does not rather than a branch a
  // browser reaches.
  return named ? named.value : gmtOffset(at);
}

function gmtOffset(at: Date): string {
  const minutes = -at.getTimezoneOffset();
  const sign = minutes < 0 ? '-' : '+';
  const size = Math.abs(minutes);
  return `GMT${sign}${pad(Math.floor(size / 60))}:${pad(size % 60)}`;
}

/** clock renders a wall-clock stamp in the reader's own zone and names that
 * zone. The layer panel states the time a layer was unregistered on the same
 * clock, so a reader comparing a finished run against an unregistration reads
 * one convention rather than two. The zone is named rather than left implicit,
 * because the stamp is quoted into an issue or a chat message alongside the
 * registry's own logs. */
export function clock(at: number): string {
  const when = new Date(at);
  return `${pad(when.getHours())}:${pad(when.getMinutes())}:${pad(when.getSeconds())} ${zone(when)}`;
}

function pad(value: number): string {
  return String(value).padStart(2, '0');
}
