// The usage page: how the catalog the caller can see is loaded, synced, and
// found over a window the reader chooses. Two reads back it. The summary read
// carries the totals, their daily series, the client and harness breakdowns,
// and the layers; the list read carries one page of artifacts at a time under
// the reader's filters and sort. They are separate because the report's layer
// filter narrows the totals, and the table's layer menu must not move the
// tiles above it (§8.7).

import { useEffect, useRef, useState } from 'react';

import { FilterField } from './DomainAtScale';
import { firstClassTypes } from './SearchSurface';
import type { UsageArtifact, UsageQuery, UsageReport } from '../api';
import { usageReport } from '../api';
import { BreakdownBar, ShareBar, StatTile } from '../components/Usage';
import { EmptyState, ErrorState, Loading, TypeBadge } from '../components/primitives';
import { artifactHref, replaceRoute, usageHref } from '../route';
import { plural } from '../similarity';
import { breakdownParts, formatCount, relativeDay, usageHints } from '../usage';
import { useAsync, useDebounced, useErrorReport } from '../useAsync';

/** UsageWindow is the window the page reads: a preset number of days, or a
 * custom range of UTC days. */
type UsageWindow = { days: number } | { from: string; to: string };

const presets = [7, 30, 90] as const;

/** listPage is how many artifacts one page of the table carries. */
const listPage = 50;

type SortKey = NonNullable<UsageQuery['sort']>;

/** columns are the table's sortable columns in the order they are drawn,
 * after the artifact and layer columns. */
const columns: { key: SortKey; label: string; hint: string }[] = [
  { key: 'loads', label: 'ON-DEMAND LOADS', hint: usageHints.loads },
  { key: 'syncs', label: 'PRELOADS', hint: usageHints.syncs },
  { key: 'search_hits', label: 'SEARCH HITS', hint: usageHints.searchHits },
  { key: 'last_loaded', label: 'LAST LOADED', hint: usageHints.lastLoaded },
];

function windowParams(w: UsageWindow): Pick<UsageQuery, 'days' | 'from' | 'to'> {
  return 'days' in w ? { days: w.days } : { from: w.from, to: w.to };
}

/** UsageSurface is the usage page. `query` is the artifact ID text the list
 * opens filtered by, which the artifact viewer's link sets.
 *
 * Spec: §13.10, §8.7 */
export function UsageSurface({ query, onError }: { query: string; onError: (err: unknown) => void }) {
  const [range, setRange] = useState<UsageWindow>({ days: 30 });
  const summary = useAsync(
    () => usageReport({ ...windowParams(range), granularity: 'day', limit: 1 }),
    [JSON.stringify(range)],
  );
  // The summary is a catalog read taken with the caller's identity, so its
  // outcome goes to the shell, where the catalog-scope rule decides what an
  // identity refusal renders.
  useErrorReport(summary.error, onError);
  const report = summary.value;
  return (
    <section className="surface usage" aria-label="Usage">
      <div className="usage-head">
        <h1>Usage</h1>
        <WindowControl
          value={range}
          shown={report === null ? null : { from: report.from, to: report.to }}
          onChange={setRange}
        />
      </div>
      <p className="lead usage-lead">
        On-demand loads, preloads, and search hits for the layers you can see. Counted per UTC day.
      </p>
      {summary.error !== null && (
        <ErrorState error={summary.error} title="The usage report did not load." onRetry={summary.reload} />
      )}
      {report === null && summary.error === null && <Loading label="Loading the usage report." />}
      {report !== null && (
        <>
          <Summary report={report} />
          <LayerUsage report={report} />
          <ArtifactUsage
            range={range}
            layers={report.layers.map((l) => l.layer)}
            initialQuery={query}
            today={'days' in range ? report.to : new Date().toISOString().slice(0, 10)}
          />
        </>
      )}
    </section>
  );
}

/** presetRange returns the first and last UTC day of a preset window that
 * ends today, the range the registry reads for `days` (§8.7). */
function presetRange(days: number, now: Date = new Date()): { from: string; to: string } {
  const day = 24 * 60 * 60 * 1000;
  const iso = (t: number) => new Date(t).toISOString().slice(0, 10);
  return { from: iso(now.getTime() - (days - 1) * day), to: iso(now.getTime()) };
}

/** WindowControl is the 7d / 30d / 90d / Custom… choice. Custom opens two
 * date fields filled with the range on display, `shown` (the report's from
 * and to) or, before the report arrives, the preset's own days, so To starts
 * at today and From at the start of the preset window. The page reads the
 * range whenever both fields hold a date and From is not after To; the fields
 * bound each other and stop at today, so a reversed or future range, which
 * the registry refuses, is not sent. */
function WindowControl({
  value,
  shown,
  onChange,
}: {
  value: UsageWindow;
  shown: { from: string; to: string } | null;
  onChange: (w: UsageWindow) => void;
}) {
  const [custom, setCustom] = useState(!('days' in value));
  const [from, setFrom] = useState('days' in value ? '' : value.from);
  const [to, setTo] = useState('days' in value ? '' : value.to);
  const today = new Date().toISOString().slice(0, 10);
  const read = (f: string, t: string) => {
    if (f !== '' && t !== '' && f <= t) {
      onChange({ from: f, to: t });
    }
  };
  return (
    <div className="usage-head-control">
      <div className="segmented usage-range" role="group" aria-label="Time range">
        {presets.map((days) => {
          const on = !custom && 'days' in value && value.days === days;
          return (
            <button
              key={days}
              type="button"
              className={on ? 'segment segment-on' : 'segment'}
              aria-pressed={on}
              onClick={() => {
                setCustom(false);
                onChange({ days });
              }}
            >
              {`${String(days)}d`}
            </button>
          );
        })}
        <button
          type="button"
          className={custom ? 'segment segment-on' : 'segment'}
          aria-pressed={custom}
          onClick={() => {
            if (custom) {
              return;
            }
            const start = shown ?? ('days' in value ? presetRange(value.days) : value);
            setCustom(true);
            setFrom(start.from);
            setTo(start.to);
            onChange({ from: start.from, to: start.to });
          }}
        >
          Custom…
        </button>
      </div>
      {custom && (
        <div className="usage-custom-range">
          <label>
            From
            <input
              type="date"
              value={from}
              max={to === '' ? today : to}
              onChange={(event) => {
                setFrom(event.target.value);
                read(event.target.value, to);
              }}
            />
          </label>
          <label>
            To
            <input
              type="date"
              value={to}
              min={from === '' ? undefined : from}
              max={today}
              onChange={(event) => {
                setTo(event.target.value);
                read(from, event.target.value);
              }}
            />
          </label>
        </div>
      )}
    </div>
  );
}

/** Summary is the two stat tiles and the breakdown card. */
function Summary({ report }: { report: UsageReport }) {
  const series = report.totals.series ?? [];
  return (
    <>
      <div className="usage-tiles">
        <StatTile
          label="ON-DEMAND LOADS"
          hint={usageHints.loads}
          total={report.totals.loads}
          values={series.map((p) => p.loads)}
        />
        <StatTile
          label="PRELOADS"
          hint={usageHints.syncs}
          total={report.totals.syncs}
          values={series.map((p) => p.syncs)}
        />
      </div>
      <div className="usage-card usage-breakdowns">
        <BreakdownBar title="ALL LOADS BY CLIENT" parts={breakdownParts(report.totals.by_client)} />
        <BreakdownBar title="ALL LOADS BY HARNESS" parts={breakdownParts(report.totals.by_harness)} />
      </div>
    </>
  );
}

/** LayerUsage is the By layer disclosure, collapsed by default and naming the
 * layer count and the layer with the most on-demand loads while closed. */
function LayerUsage({ report }: { report: UsageReport }) {
  const [open, setOpen] = useState(false);
  const layers = report.layers;
  const shares = layers.map((l) => l.loads + l.syncs);
  const max = Math.max(0, ...shares);
  const total = shares.reduce((sum, share) => sum + share, 0);
  const leader = layers.reduce<(typeof layers)[number] | null>(
    (best, l) => (best === null || l.loads > best.loads ? l : best),
    null,
  );
  const count = `${String(layers.length)} ${plural(layers.length, 'layer', 'layers')}`;
  const summary =
    !open && leader !== null && leader.loads > 0 ? `${count} · ${leader.layer} has the most on-demand loads` : count;
  return (
    <div className={open ? 'usage-card usage-layers usage-layers-open' : 'usage-card usage-layers'}>
      <button
        type="button"
        className="usage-disclosure"
        aria-expanded={open}
        onClick={() => {
          setOpen(!open);
        }}
      >
        <svg className="usage-disclosure-chevron" width="9" height="9" viewBox="0 0 10 10" aria-hidden="true">
          <path d="M3 1.5L7 5l-4 3.5" fill="none" strokeWidth="1.5" strokeLinecap="round" />
        </svg>
        <span className="usage-disclosure-title">By layer</span>
        <span className="usage-disclosure-summary">{summary}</span>
      </button>
      {open && (
        <div className="usage-table usage-layer-table" role="table" aria-label="Usage by layer">
          <div className="usage-row usage-row-head" role="row">
            <span role="columnheader">LAYER</span>
            <span role="columnheader" title={usageHints.allLoads}>
              SHARE OF ALL LOADS
            </span>
            <span role="columnheader" className="num" title={usageHints.loads}>
              ON-DEMAND LOADS
            </span>
            <span role="columnheader" className="num" title={usageHints.syncs}>
              PRELOADS
            </span>
            <span role="columnheader" className="num" title={usageHints.searchHits}>
              SEARCH HITS
            </span>
          </div>
          {layers.map((l, i) => (
            <div key={l.layer} className="usage-row" role="row">
              <span role="cell" className="mono usage-layer-name">
                {l.layer}
              </span>
              <span role="cell">
                <ShareBar value={shares[i]} total={total} top={max > 0 && shares[i] === max} />
              </span>
              <span role="cell" className="num strong">
                {formatCount(l.loads)}
              </span>
              <span role="cell" className="num">
                {formatCount(l.syncs)}
              </span>
              <span role="cell" className="num">
                {formatCount(l.search_hits)}
              </span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

/** ArtifactUsage is the By artifact section: the ID filter, the type and
 * layer menus, the count, and the sortable table that pages with the
 * report's cursor. Every change to a filter or the sort reads the list from
 * its first page, because a cursor belongs to the order it was issued in
 * (§8.7). */
function ArtifactUsage({
  range,
  layers,
  initialQuery,
  today,
}: {
  range: UsageWindow;
  layers: string[];
  initialQuery: string;
  today: string;
}) {
  const [filter, setFilter] = useState(initialQuery);
  const [type, setType] = useState('');
  const [layer, setLayer] = useState('');
  const [sort, setSort] = useState<SortKey>('loads');
  const [order, setOrder] = useState<'desc' | 'asc'>('desc');
  const [more, setMore] = useState<UsageArtifact[]>([]);
  const [next, setNext] = useState<string | null>(null);
  const [total, setTotal] = useState(0);
  const [generation, setGeneration] = useState(0);
  const [loadingMore, setLoadingMore] = useState(false);
  const [moreFailed, setMoreFailed] = useState(false);
  const sentinel = useRef<HTMLDivElement | null>(null);
  const typed = useDebounced(filter.trim());
  // The address carries the filter text, so a copied link opens the list the
  // reader filtered.
  useEffect(() => {
    replaceRoute(usageHref(typed));
  }, [typed]);
  const params: UsageQuery = {
    ...windowParams(range),
    query: typed === '' ? undefined : typed,
    types: type === '' ? undefined : [type],
    layers: layer === '' ? undefined : [layer],
    sort,
    order,
    limit: listPage,
  };
  const key = JSON.stringify(params);
  const list = useAsync(() => usageReport(params), [key]);
  const first = list.value;
  // A change of filters or sort, and each first page that answers, starts the
  // list over: the appended pages are discarded and the next cursor is the
  // one the first page carries. Until the new first page answers there is no
  // cursor, because the one on screen belongs to the previous order (§8.7).
  // Each restart bumps the generation, which is what a next page compares on
  // its return. The filters alone cannot tell lists apart: a reader who
  // changes a filter and changes it back is under the same filters again,
  // and a page asked for before the change would be appended beside the
  // pages the new list reads.
  const [listedFor, setListedFor] = useState({ key, first });
  if (listedFor.key !== key || listedFor.first !== first) {
    setListedFor({ key, first });
    setMore([]);
    setNext(listedFor.key === key ? (first?.next_cursor ?? null) : null);
    setTotal(first?.artifact_count ?? 0);
    setGeneration((g) => g + 1);
    setLoadingMore(false);
    setMoreFailed(false);
  }
  const generationRef = useRef(generation);
  generationRef.current = generation;
  const rows = first === null ? [] : [...first.artifacts, ...more];
  const peak: Peaks = {
    loads: Math.max(0, ...rows.map((r) => r.loads)),
    syncs: Math.max(0, ...rows.map((r) => r.syncs)),
    search_hits: Math.max(0, ...rows.map((r) => r.search_hits)),
  };

  const sortBy = (column: SortKey) => {
    if (column === sort) {
      setOrder(order === 'desc' ? 'asc' : 'desc');
    } else {
      setSort(column);
      setOrder('desc');
    }
  };
  const showMore = () => {
    if (next === null || loadingMore || list.loading) {
      return;
    }
    // A page answers the list it was asked for. One that arrives after the
    // list started over belongs to a list no longer on screen, so it is
    // dropped rather than appended under the new order.
    const askedIn = generation;
    const listed = rows;
    const readWith = next;
    setLoadingMore(true);
    setMoreFailed(false);
    usageReport({ ...params, cursor: readWith }).then(
      (page) => {
        if (generationRef.current !== askedIn) {
          return;
        }
        setMore((prior) => [...prior, ...unlisted(listed, page.artifacts)]);
        // The page's cursor replaces the one it was read with, whether or not
        // the page added rows. A count that changed between reads moves
        // artifacts across the cursor (§8.7), so the last page can come back
        // empty with no cursor, and a page before it can repeat only rows
        // already listed while unseen artifacts remain after it. Each cursor
        // the report issues lies past the one it was read with, so following
        // it always advances. A cursor equal to the one just read would read
        // the same page again in a loop, so it ends the list.
        setNext(page.next_cursor === readWith ? null : page.next_cursor);
        setTotal(page.artifact_count);
        setLoadingMore(false);
      },
      () => {
        if (generationRef.current !== askedIn) {
          return;
        }
        setLoadingMore(false);
        setMoreFailed(true);
      },
    );
  };
  // The sentinel below the table reads the next page as it comes within
  // 400px of the viewport. The observer is rebuilt with each page, so a page
  // too short to push the sentinel out of view reads the one after it too. A
  // failed read stops the observer, because a rebuilt observer reports the
  // sentinel still in view at once and would ask for the failed page again
  // in a loop; the page states the failure and the button retries it.
  const observe = typeof IntersectionObserver !== 'undefined';
  const showMoreRef = useRef(showMore);
  showMoreRef.current = showMore;
  useEffect(() => {
    const target = sentinel.current;
    if (!observe || target === null || next === null || loadingMore || moreFailed) {
      return;
    }
    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((entry) => entry.isIntersecting)) {
          showMoreRef.current();
        }
      },
      { rootMargin: '400px 0px' },
    );
    observer.observe(target);
    return () => {
      observer.disconnect();
    };
  }, [observe, next, loadingMore, moreFailed, rows.length]);

  return (
    <section className="usage-artifacts" aria-label="By artifact">
      <h2 className="usage-section-title">By artifact</h2>
      <div className="usage-filters">
        <FilterField label="Filter by artifact id" value={filter} onTyped={setFilter} />
        <label className="usage-menu">
          Type:
          <select
            aria-label="Type"
            value={type}
            onChange={(event) => {
              setType(event.target.value);
            }}
          >
            <option value="">all</option>
            {firstClassTypes.map((name) => (
              <option key={name} value={name}>
                {name}
              </option>
            ))}
          </select>
        </label>
        <label className="usage-menu">
          Layer:
          <select
            aria-label="Layer"
            value={layer}
            onChange={(event) => {
              setLayer(event.target.value);
            }}
          >
            <option value="">all</option>
            {layers.map((name) => (
              <option key={name} value={name}>
                {name}
              </option>
            ))}
          </select>
        </label>
        {first !== null && (
          <span className="usage-count" data-testid="usage-count">
            Showing <strong>{formatCount(rows.length)}</strong> of <strong>{formatCount(Math.max(total, rows.length))}</strong>
          </span>
        )}
      </div>
      {list.error !== null && (
        <ErrorState error={list.error} title="The artifact list did not load." onRetry={list.reload} />
      )}
      {list.loading && first === null && list.error === null && <Loading label="Loading the artifact list." />}
      {first !== null && (
        <div className="usage-card usage-table usage-artifact-table" role="table" aria-label="Usage by artifact">
          <div className="usage-row usage-row-head" role="row">
            <span role="columnheader">ARTIFACT</span>
            <span role="columnheader">LAYER</span>
            {columns.map((column) => (
              <span
                key={column.key}
                role="columnheader"
                className="num"
                aria-sort={column.key === sort ? (order === 'desc' ? 'descending' : 'ascending') : 'none'}
              >
                <button
                  type="button"
                  className={column.key === sort ? 'usage-sort usage-sort-on' : 'usage-sort'}
                  title={column.hint}
                  onClick={() => {
                    sortBy(column.key);
                  }}
                >
                  {column.label}
                  {column.key === sort ? (order === 'desc' ? ' ↓' : ' ↑') : ''}
                </button>
              </span>
            ))}
          </div>
          {rows.length === 0 ? (
            <EmptyState scope="inline">No artifacts match.</EmptyState>
          ) : (
            rows.map((row) => <ArtifactUsageRow key={row.artifact_id} row={row} peak={peak} today={today} />)
          )}
        </div>
      )}
      {next !== null && first !== null && (
        <div className="usage-more" ref={sentinel}>
          {loadingMore ? (
            <span className="usage-more-status" role="status">
              Loading more artifacts…
            </span>
          ) : moreFailed ? (
            <>
              <span className="usage-more-status" role="alert">
                The next artifacts did not load.
              </span>
              <button type="button" className="button" onClick={showMore}>
                Try again
              </button>
            </>
          ) : (
            <button type="button" className="button" onClick={showMore}>
              {showMoreLabel(total - rows.length)}
            </button>
          )}
        </div>
      )}
    </section>
  );
}

/** unlisted returns the rows of a page whose artifact is not already listed.
 * A count that changes between reads can move an artifact across the cursor
 * and onto a second page (§8.7); the table lists each artifact once, where it
 * first appeared, and a repeated row would also repeat a React key.
 *
 * Spec: §8.7 */
function unlisted(listed: UsageArtifact[], page: UsageArtifact[]): UsageArtifact[] {
  const seen = new Set(listed.map((row) => row.artifact_id));
  return page.filter((row) => {
    if (seen.has(row.artifact_id)) {
      return false;
    }
    seen.add(row.artifact_id);
    return true;
  });
}

/** showMoreLabel names the next page on the button. `remaining` comes from
 * the latest page's artifact count, which a count change or a retired
 * artifact can bring to zero or below while the report still issues a
 * cursor; the label then names no number. */
function showMoreLabel(remaining: number): string {
  return remaining > 0 ? `Show ${String(Math.min(listPage, remaining))} more` : 'Show more';
}

/** Peaks holds the largest value of each barred column among the rows
 * shown, which each row's bars are drawn against. */
type Peaks = Record<'loads' | 'syncs' | 'search_hits', number>;

/** CountCell is a count with a small bar relative to its column's peak. */
function CountCell({ value, peak, strong }: { value: number; peak: number; strong?: boolean }) {
  const width = peak === 0 ? 0 : Math.round((value / peak) * 100);
  const tone = [strong === true ? 'strong' : '', value === 0 ? 'quiet' : ''].filter(Boolean).join(' ');
  return (
    <span role="cell" className="num usage-count-cell">
      <span className="usage-count-bar" aria-hidden="true">
        <span className="usage-count-fill" style={{ width: `${String(width)}%` }} />
      </span>
      <span className={tone === '' ? undefined : tone}>{formatCount(value)}</span>
    </span>
  );
}

function ArtifactUsageRow({ row, peak, today }: { row: UsageArtifact; peak: Peaks; today: string }) {
  return (
    <div className="usage-row" role="row">
      <span role="cell" className="usage-artifact">
        <a className="mono usage-artifact-id" href={artifactHref(row.artifact_id)}>
          {row.artifact_id}
        </a>
        <TypeBadge type={row.type} />
      </span>
      <span role="cell" className="mono usage-layer-cell">
        {row.layer}
      </span>
      <CountCell value={row.loads} peak={peak.loads} strong />
      <CountCell value={row.syncs} peak={peak.syncs} />
      <CountCell value={row.search_hits} peak={peak.search_hits} />
      <span role="cell" className="num usage-last">
        {relativeDay(row.last_loaded, today)}
      </span>
    </div>
  );
}
