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
 * reader's own zone, formatted as `YYYY-MM-DD`. A recovery date is a deadline
 * the reader checks against the calendar on their wall, so a UTC calendar day
 * would tell a reader west of UTC in the evening that a layer they
 * unregistered moments ago went tomorrow, and would put the erase deadline a
 * day off their own calendar. The date carries no zone name because a bare
 * calendar date already reads as the reader's own. */
export function calendarDate(at: Date): string {
  const pad = (value: number) => String(value).padStart(2, '0');
  return `${String(at.getFullYear())}-${pad(at.getMonth() + 1)}-${pad(at.getDate())}`;
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
