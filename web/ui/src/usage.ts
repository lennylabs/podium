// The usage report's figures as the UI states them: the counts of a list's
// artifacts read in one request, the names of the clients and harnesses,
// and the days and numbers the surfaces print.

import { useMemo } from 'react';

import type { UsageCounts, UsagePeriod, UsageReport } from './api';
import { usageReport } from './api';
import { useAsync } from './useAsync';

/** usageListDays is the window every artifact's usage line and rail section
 * covers (§13.10). */
export const usageListDays = 30;

/** The names the UI gives the report's counts, and the definitions their
 * tooltips state. The report's loads are on-demand loads: an agent, an SDK
 * program, a CLI command, or the web UI fetched the artifact when it needed
 * it. Its syncs
 * are preloads: podium sync wrote the artifact into a workspace ahead of
 * time, and the harness then reads the local copy, which the registry never
 * sees. The API, the CLI, and the docs keep the names loads and syncs. */
export const usageHints = {
  loads: 'Fetched from the registry when an agent, an SDK program, a CLI command, or the web UI needed it.',
  syncs:
    'Copied into a workspace ahead of time by podium sync. Each sync run counts once per artifact, and later use of the local copy is not counted.',
  searchHits: 'Appeared in the first 10 results of a search with a query.',
  allLoads: 'On-demand loads and preloads together.',
  lastLoaded: 'The last day an agent, an SDK program, a CLI command, or the web UI loaded the artifact on demand.',
} as const;

/** usageMaxArtifacts is the most artifacts one usage read names (§8.7); a
 * list that shows more states usage for the first ones. */
export const usageMaxArtifacts = 500;

/** usagePageMax is the largest page the report returns (§8.7). */
const usagePageMax = 200;

/** UsageCell is the usage one row states: placeholders while the list's read
 * is in flight, or the counts it answered. A read that failed has no cell,
 * and the row leaves the line out (§13.10). */
export type UsageCell = { state: 'loading' } | { state: 'ready'; counts: UsageCounts };

const noCounts: UsageCounts = { loads: 0, syncs: 0, search_hits: 0 };

/** readArtifactUsage reads the last 30 days of each named artifact's counts.
 * It names at most usageMaxArtifacts artifacts and follows the report's
 * cursor until the list is complete, because a page holds at most 200 entries.
 * An artifact the report leaves out, which is how it answers one the caller
 * cannot see, is given no counts, so a hidden artifact and an unused one read
 * the same.
 *
 * Spec: §8.7, §13.10 */
export async function readArtifactUsage(ids: readonly string[]): Promise<Map<string, UsageCounts>> {
  const named = ids.slice(0, usageMaxArtifacts);
  const out = new Map<string, UsageCounts>();
  if (named.length === 0) {
    return out;
  }
  let cursor: string | undefined;
  // Five hundred artifacts fill at most three pages, so the loop stops there
  // even if a response kept answering a cursor.
  for (let pages = 0; pages < Math.ceil(usageMaxArtifacts / usagePageMax); pages++) {
    const page: UsageReport = await usageReport({
      days: usageListDays,
      artifacts: named,
      limit: usagePageMax,
      cursor,
    });
    for (const artifact of page.artifacts) {
      out.set(artifact.artifact_id, {
        loads: artifact.loads,
        syncs: artifact.syncs,
        search_hits: artifact.search_hits,
      });
    }
    if (page.next_cursor === null) {
      break;
    }
    cursor = page.next_cursor;
  }
  return out;
}

/** useArtifactUsage reads the usage of the artifacts a list shows, in one
 * read issued after the list rendered, and answers each row's cell. The read
 * is keyed on the identifiers, so a list that re-renders with the same rows
 * does not read again.
 *
 * Spec: §13.10 */
export function useArtifactUsage(ids: readonly string[]): (id: string) => UsageCell | undefined {
  const key = ids.slice(0, usageMaxArtifacts).join('\n');
  const read = useAsync(() => readArtifactUsage(key === '' ? [] : key.split('\n')), [key]);
  return useMemo(() => {
    if (read.loading) {
      return () => ({ state: 'loading' });
    }
    const counts = read.value;
    if (counts === null) {
      return () => undefined;
    }
    return (id: string) => ({ state: 'ready', counts: counts.get(id) ?? noCounts });
  }, [read.loading, read.value]);
}

/** RailUsage is what the artifact rail's usage section draws: placeholders
 * while its read is in flight, or the counts, the daily series, and the last
 * days once it answered. A failed read has no value, and the section is not
 * rendered (§13.10). */
export type RailUsage =
  | { state: 'loading' }
  | {
      state: 'ready';
      counts: UsageCounts;
      series: UsagePeriod[];
      lastLoaded: string | null;
      lastSynced: string | null;
      today: string;
    };

/** useRailUsage reads one artifact's last 30 days, by day, on a request of
 * its own, which the page issues beside the artifact read rather than before
 * it. An artifact the caller cannot see comes back as no entry, which reads as
 * no activity, the same as an artifact nobody used.
 *
 * Spec: §13.10 */
export function useRailUsage(id: string): RailUsage | undefined {
  const read = useAsync(
    () => usageReport({ days: usageListDays, artifacts: [id], granularity: 'day', limit: 1 }),
    [id],
  );
  return useMemo((): RailUsage | undefined => {
    if (read.loading) {
      return { state: 'loading' };
    }
    const report = read.value;
    if (report === null) {
      return undefined;
    }
    const entry = report.artifacts.find((artifact) => artifact.artifact_id === id);
    return {
      state: 'ready',
      counts: entry === undefined ? noCounts : entry,
      series: entry?.series ?? [],
      lastLoaded: entry?.last_loaded ?? null,
      lastSynced: entry?.last_synced ?? null,
      today: report.to,
    };
  }, [read.loading, read.value, id]);
}

/** formatCount prints a count with thousands separators. */
export function formatCount(n: number): string {
  return n.toLocaleString('en-US');
}

/** relativeDay states a UTC day relative to today: "today", "yesterday", "N
 * days ago" within a month, and the date itself beyond, so the figure stays
 * readable without a calendar. A null day is an em dash. */
export function relativeDay(day: string | null, today: string): string {
  if (day === null || day === '') {
    return '—';
  }
  const ms = Date.parse(`${today}T00:00:00Z`) - Date.parse(`${day}T00:00:00Z`);
  const days = Math.round(ms / 86_400_000);
  if (Number.isNaN(days) || days < 0) {
    return day;
  }
  if (days === 0) {
    return 'today';
  }
  if (days === 1) {
    return 'yesterday';
  }
  return days < 31 ? `${String(days)} days ago` : day;
}

/** BreakdownPart is one named share of a breakdown bar. */
export interface BreakdownPart {
  name: string;
  count: number;
}

/** breakdownParts turns a breakdown into the parts a bar draws: each client's
 * or harness's loads plus syncs, largest first and named as the report names
 * it (§13.10), with the `other` part and then the `unknown` part last
 * whatever their size, and parts with nothing dropped. */
export function breakdownParts(breakdown: Record<string, UsageCounts>): BreakdownPart[] {
  const parts = Object.entries(breakdown)
    .map(([name, counts]) => ({ name, count: counts.loads + counts.syncs }))
    .filter((part) => part.count > 0)
    .sort((a, b) => b.count - a.count || a.name.localeCompare(b.name));
  const rank = (name: string) => (name === 'unknown' ? 2 : name === 'other' ? 1 : 0);
  return [...parts].sort((a, b) => rank(a.name) - rank(b.name));
}
