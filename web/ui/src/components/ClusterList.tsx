// The similarity page's list of artifacts with overlap. It scrolls on its own
// under a header that stays put, so a long catalog does not push the detail
// beside it off the screen.

import type { ClusterEntry } from '../similarity';
import { formatStrongest, formatWordOverlap, plural } from '../similarity';

/** ClusterList is one row per artifact with overlap: its identifier, its
 * layer, and the strongest score of each kind it carries, stated as
 * `0.91 · 80%`. A kind the artifact has no peer for is left out of the
 * figure, and the similarity is left out wherever the read carried no
 * semantic check. The selected row is tinted and marked.
 *
 * Spec: §13.10 */
export function ClusterList({
  entries,
  selected,
  onSelect,
  showSimilarity,
}: {
  entries: ClusterEntry[];
  selected: string;
  onSelect: (id: string) => void;
  showSimilarity: boolean;
}) {
  return (
    <div className="cluster-list" data-testid="cluster-list">
      <p className="cluster-list-head label">
        {entries.length} {plural(entries.length, 'artifact', 'artifacts')}
        <span className="cluster-list-strongest">Strongest</span>
      </p>
      <ul className="cluster-list-rows" aria-label="Artifacts with overlap">
        {entries.map((entry) => {
          const id = entry.artifact.artifact_id;
          const pressed = selected === id;
          return (
            <li key={id}>
              <button
                type="button"
                aria-pressed={pressed}
                className={pressed ? 'cluster-row cluster-row-selected' : 'cluster-row'}
                onClick={() => {
                  onSelect(id);
                }}
              >
                <span className="mono cluster-row-id">{id}</span>
                <span className="mono cluster-row-meta">
                  {entry.artifact.layer}
                  <span className="cluster-row-score">{strongestFigure(entry, showSimilarity)}</span>
                </span>
              </button>
            </li>
          );
        })}
      </ul>
    </div>
  );
}

/** strongestFigure states an entry's strongest scores, the similarity first
 * and the word overlap after it, each in its own format. */
export function strongestFigure(entry: ClusterEntry, showSimilarity: boolean): string {
  const parts: string[] = [];
  if (showSimilarity && entry.strongestSimilarity !== undefined) {
    parts.push(formatStrongest(entry.strongestSimilarity));
  }
  if (entry.strongestWordOverlap !== undefined) {
    parts.push(formatWordOverlap(entry.strongestWordOverlap));
  }
  return parts.join(' · ');
}
