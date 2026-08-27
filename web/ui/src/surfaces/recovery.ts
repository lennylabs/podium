// The §8.4 recovery window, shared by the surface that unregisters a layer
// and the surface that lists what is still restorable. Both state the window
// as the calendar date it runs out on rather than as a duration the reader
// has to add up, so both derive that date the same way.

/** recoveryDays is how long an unregistered layer stays restorable (§8.4). */
export const recoveryDays = 30;

/** accentDays is the remaining window the recovery surface accents. Below it
 * the layer is close enough to erasure that the row says so. */
export const accentDays = 3;

/** calendarDate renders an instant as the calendar date it fell on in the
 * reader's own zone, formatted as `23 Sep 2026`. A recovery date is a deadline
 * the reader checks against the calendar on their wall, so a UTC calendar day
 * would tell a reader west of UTC in the evening that a layer they
 * unregistered moments ago went tomorrow, and would put the erase deadline a
 * day off their own calendar. The date carries no zone name because a bare
 * calendar date already reads as the reader's own.
 *
 * The month is named rather than numbered, because the deadline is read at a
 * glance beside a depleting bar and an ISO date in that position reads as a
 * serial number. The month names are fixed here rather than taken from the
 * platform formatter, so the date does not change spelling with the browser's
 * locale while the surface around it stays in one language. */
export function calendarDate(at: Date): string {
  return `${String(at.getDate())} ${months[at.getMonth()]} ${String(at.getFullYear())}`;
}

const months = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

/** unregisteredOn renders when a layer was unregistered. A layer unregistered
 * earlier the same day is stated as the time of day, because the reader who
 * is looking for it is looking for something they did minutes ago and the
 * day's own date tells them nothing. */
export function unregisteredOn(at: Date, now: number): string {
  const today = new Date(now);
  const sameDay =
    at.getFullYear() === today.getFullYear() &&
    at.getMonth() === today.getMonth() &&
    at.getDate() === today.getDate();
  if (!sameDay) {
    return calendarDate(at);
  }
  const pad = (value: number) => String(value).padStart(2, '0');
  return `today, ${pad(at.getHours())}:${pad(at.getMinutes())}`;
}

/** erasesOn returns the date the recovery window runs out for a layer
 * unregistered at the given time, as a calendar date in the reader's zone. */
export function erasesOn(unregisteredAt: Date): string {
  return calendarDate(new Date(unregisteredAt.getTime() + recoveryDays * dayMillis));
}

/** daysLeft returns whole days remaining before erasure, floored at zero. The
 * count rounds a part-day up, so a layer unregistered moments ago reports the
 * full window the unregister confirmation promised rather than one day less,
 * and the count agrees with the erase date the same row states. A layer past
 * its window is one the purge job has not reached yet, and the surface reports
 * no time left rather than a negative count. */
export function daysLeft(unregisteredAt: Date, now: number): number {
  const remaining = unregisteredAt.getTime() + recoveryDays * dayMillis - now;
  return remaining <= 0 ? 0 : Math.ceil(remaining / dayMillis);
}

const dayMillis = 24 * 60 * 60 * 1000;
