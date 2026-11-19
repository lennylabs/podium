// The usage report's components: the 30-day line on a list row, the artifact
// rail's usage section, the usage page's stat tiles, and the bars that state
// a breakdown or a layer's share. Each draws figures it is handed; the
// surfaces own the reads.

import type { UsagePeriod } from '../api';
import { usageHref } from '../route';
import type { BreakdownPart, RailUsage, UsageCell } from '../usage';
import { formatCount, relativeDay, usageHints } from '../usage';

/** UsageLine is the 30-day line under an artifact in the search results and
 * the domain browser: on-demand loads, preloads, and search hits, with placeholders while
 * the list's read is in flight. A row whose read failed passes no cell and the
 * line is left out, with no dash and no retry (§13.10). ruled draws the dashed
 * rule above the line that separates it on a search result card.
 *
 * Spec: §13.10 */
export function UsageLine({ cell, ruled = false }: { cell: UsageCell | undefined; ruled?: boolean }) {
  if (cell === undefined) {
    return null;
  }
  const className = ruled ? 'usage-line usage-line-ruled' : 'usage-line';
  if (cell.state === 'loading') {
    return (
      <div className={className} aria-busy="true" data-testid="usage-line">
        <span className="usage-label">30 DAYS</span>
        <span className="usage-placeholder" aria-hidden="true" style={{ width: 64 }} />
        <span className="usage-placeholder" aria-hidden="true" style={{ width: 56 }} />
        <span className="usage-placeholder" aria-hidden="true" style={{ width: 84 }} />
      </div>
    );
  }
  const { loads, syncs, search_hits } = cell.counts;
  return (
    <div className={className} data-testid="usage-line">
      <span className="usage-label">30 DAYS</span>
      <span className="usage-figures">
        <UsageFigure value={loads} unit="on-demand loads" hint={usageHints.loads} />
        <UsageFigure value={syncs} unit="preloads" hint={usageHints.syncs} />
        <UsageFigure value={search_hits} unit="search hits" hint={usageHints.searchHits} />
      </span>
    </div>
  );
}

function UsageFigure({ value, unit, hint }: { value: number; unit: string; hint: string }) {
  return (
    <span className="usage-figure" title={hint}>
      <span className="usage-value">{formatCount(value)}</span>
      <span className="usage-unit">{unit}</span>
    </span>
  );
}

/** UsageSummary is the artifact rail's USAGE section. An artifact with no
 * counts and one the caller cannot see read the same, because the report
 * answers both alike (§13.10).
 *
 * Spec: §13.10 */
export function UsageSummary({ id, usage }: { id: string; usage: RailUsage | undefined }) {
  if (usage === undefined) {
    return null;
  }
  return (
    <section className="usage-summary" aria-label="Usage" data-testid="rail-usage">
      <div className="rail-section-head">
        <p className="label">Usage</p>
        <p className="label rail-section-count">last 30 days</p>
      </div>
      {usage.state === 'loading' ? (
        <div className="usage-summary-loading" aria-busy="true">
          <span className="usage-placeholder" aria-hidden="true" style={{ width: 200, height: 30 }} />
          <span className="usage-placeholder" aria-hidden="true" style={{ width: 150 }} />
          <span className="usage-placeholder" aria-hidden="true" style={{ width: 170 }} />
          <span className="usage-placeholder" aria-hidden="true" style={{ width: 120 }} />
        </div>
      ) : usage.counts.loads + usage.counts.syncs + usage.counts.search_hits === 0 ? (
        <p className="usage-summary-empty">No on-demand loads, preloads, or search hits in the last 30 days.</p>
      ) : (
        <>
          <LoadBars series={usage.series} />
          <dl className="usage-summary-figures">
            <UsageRow label="on-demand loads" hint={usageHints.loads} value={formatCount(usage.counts.loads)} />
            <UsageRow label="preloads" hint={usageHints.syncs} value={formatCount(usage.counts.syncs)} />
            <UsageRow label="search hits" hint={usageHints.searchHits} value={formatCount(usage.counts.search_hits)} />
            <UsageRow label="last loaded on demand" value={relativeDay(usage.lastLoaded, usage.today)} />
            <UsageRow label="last preloaded" value={relativeDay(usage.lastSynced, usage.today)} />
          </dl>
          <a className="usage-summary-link" href={usageHref(id)}>
            Open in Usage →
          </a>
        </>
      )}
    </section>
  );
}

function UsageRow({ label, value, hint }: { label: string; value: string; hint?: string }) {
  return (
    <div className="usage-summary-row">
      <dt title={hint}>{label}</dt>
      <dd>{value}</dd>
    </div>
  );
}

/** LoadBars draws one bar per day of the series' on-demand loads, scaled to
 * the busiest day, with today's bar, the last, in the accent. A day with no loads keeps a
 * one-pixel stub so the run of days stays legible. */
function LoadBars({ series }: { series: UsagePeriod[] }) {
  const peak = Math.max(1, ...series.map((period) => period.loads));
  return (
    <div className="load-bars" role="img" aria-label={`Daily on-demand loads over the last ${String(series.length)} days`}>
      {series.map((period, i) => (
        <span
          key={period.start}
          className={i === series.length - 1 ? 'load-bar load-bar-today' : 'load-bar'}
          style={{ height: Math.max(1, Math.round((period.loads / peak) * 30)) }}
        />
      ))}
    </div>
  );
}

/** StatTile is one of the usage page's two totals with its daily sparkline.
 * hint is the definition the label's tooltip states. */
export function StatTile({
  label,
  hint,
  total,
  values,
}: {
  label: string;
  hint: string;
  total: number;
  values: number[];
}) {
  return (
    <div className="stat-tile" data-testid="stat-tile">
      <div className="stat-tile-head">
        <h2 className="stat-tile-label" title={hint}>
          {label}
        </h2>
      </div>
      <div className="stat-tile-body">
        <span className="stat-tile-total">{formatCount(total)}</span>
        <Sparkline values={values} />
      </div>
    </div>
  );
}

const sparkWidth = 130;
const sparkHeight = 34;

/** Sparkline draws the series as a line over a faint fill, scaled to its own
 * peak. It is decoration beside the total, so it carries no accessible name. */
export function Sparkline({ values }: { values: number[] }) {
  if (values.length < 2) {
    return null;
  }
  const peak = Math.max(1, ...values);
  const step = (sparkWidth - 2) / (values.length - 1);
  const points = values.map((v, i) => {
    const x = 1 + i * step;
    const y = sparkHeight - 4 - (v / peak) * (sparkHeight - 8);
    return `${x.toFixed(1)},${y.toFixed(1)}`;
  });
  const area = `M1.0,${String(sparkHeight)} L${points.join(' L')} L${(1 + (values.length - 1) * step).toFixed(1)},${String(sparkHeight)} Z`;
  return (
    <svg
      className="sparkline"
      width={sparkWidth}
      height={sparkHeight}
      viewBox={`0 0 ${String(sparkWidth)} ${String(sparkHeight)}`}
      aria-hidden="true"
    >
      <path className="sparkline-area" d={area} />
      <polyline className="sparkline-line" points={points.join(' ')} />
    </svg>
  );
}

/** partTone is the tone slot a part takes: the unknown part always takes the
 * lightest, last slot, so it reads as the remainder however large it is, and
 * the named parts take the others in order, sharing the fourth past it. */
function partTone(part: BreakdownPart, index: number): number {
  return part.name === 'unknown' ? 4 : Math.min(index, 3);
}

/** BreakdownBar is one stacked bar and a legend with each part's count, the
 * parts in the order they are handed. */
export function BreakdownBar({ title, parts }: { title: string; parts: BreakdownPart[] }) {
  const total = parts.reduce((sum, part) => sum + part.count, 0);
  return (
    <div className="breakdown" data-testid="breakdown">
      <h3 className="breakdown-title" title={usageHints.allLoads}>
        {title}
      </h3>
      {total === 0 ? (
        <p className="breakdown-empty">No on-demand loads or preloads in this window.</p>
      ) : (
        <>
          <div className="breakdown-bar" aria-hidden="true">
            {parts.map((part, i) => (
              <span
                key={part.name}
                className={`breakdown-part breakdown-part-${String(partTone(part, i))}`}
                style={{ width: `${((part.count / total) * 100).toFixed(1)}%` }}
              />
            ))}
          </div>
          <ul className="breakdown-legend">
            {parts.map((part, i) => (
              <li key={part.name} className="breakdown-key">
                <span className={`breakdown-swatch breakdown-part-${String(partTone(part, i))}`} aria-hidden="true" />
                {part.name} <span className="breakdown-count">{formatCount(part.count)}</span>
              </li>
            ))}
          </ul>
        </>
      )}
    </div>
  );
}

/** ShareBar is a layer's share of loads and syncs against the largest layer,
 * which is drawn in the accent and the rest muted. */
export function ShareBar({ value, max, top }: { value: number; max: number; top: boolean }) {
  const width = max === 0 ? 0 : Math.round((value / max) * 100);
  return (
    <span className="share-bar" aria-hidden="true">
      <span className={top ? 'share-fill share-fill-top' : 'share-fill'} style={{ width: `${String(width)}%` }} />
    </span>
  );
}
