// The similarity page. It reads the §7.3.1 catalog overlap report with the
// caller's identity and lists each artifact that has overlap at the read's
// threshold, one entry per artifact, beside the selected entry's detail. A
// description-length diagnostic is the viewer's concern and is not listed
// here, and the page carries no control for the overlap rebuild endpoint
// (§13.10).

import { useEffect, useState } from 'react';

import { ClusterList } from '../components/ClusterList';
import { MessageBox, OverlapEmpty } from '../components/MessageBox';
import { OverlapTable } from '../components/OverlapTable';
import { Badge, ErrorState, Loading, TypeBadge } from '../components/primitives';
import { RangeSlider } from '../components/RangeSlider';
import { artifactHref, replaceRoute, similarityHref } from '../route';
import type { ClusterEntry } from '../similarity';
import {
  clusterEntries,
  formatThreshold,
  plural,
  semanticOn,
  thresholdMax,
  thresholdMin,
  thresholdStep,
} from '../similarity';
import { useErrorReport } from '../useAsync';
import { useOverlapRead } from './overlapread';

/** SimilaritySurface is the similarity page. `selected` is the artifact the
 * route names, which the list opens on where the report carries an entry for
 * it; the first entry is selected otherwise. Selecting another entry rewrites
 * the address in place, so the page can be linked at an entry without each
 * selection adding a history step.
 *
 * Spec: §13.10 */
export function SimilaritySurface({
  selected,
  onError,
  threshold,
  onThreshold,
}: {
  selected: string;
  onError: (err: unknown) => void;
  /** threshold is the threshold the reader chose, held by the shell so the
   * sidebar's count reads at it; null until the reader moves the control. */
  threshold: number | null;
  onThreshold: (value: number | null) => void;
}) {
  const read = useOverlapRead(undefined, { chosen: threshold, setChosen: onThreshold });
  // The report is a catalog read taken with the caller's identity, so its
  // outcome goes to the shell, where the catalog-scope rule decides what an
  // identity refusal renders.
  useErrorReport(read.error, onError);
  const report = read.report;
  const entries = report === null ? [] : clusterEntries(report);
  const on = report !== null && semanticOn(report);
  // The selection is held here and written to the address in place. A
  // selection is not a navigation: it adds no history step and enters no new
  // surface, so the shell neither announces it nor moves the reader's focus.
  const [picked, setPicked] = useState(selected);
  const [routed, setRouted] = useState(selected);
  if (routed !== selected) {
    setRouted(selected);
    setPicked(selected);
  }
  const current = entries.find((entry) => entry.artifact.artifact_id === picked) ?? entries[0] ?? null;
  const select = (id: string) => {
    setPicked(id);
    replaceRoute(similarityHref(id));
  };
  // The address names the entry on screen. A route naming no entry, or one
  // the report no longer carries at the chosen threshold, is corrected to
  // the entry the list fell back to, so a copied link opens what was shown.
  const shownID = current?.artifact.artifact_id ?? '';
  useEffect(() => {
    if (shownID !== '') {
      replaceRoute(similarityHref(shownID));
    }
  }, [shownID]);

  return (
    <section className="surface similarity" aria-label="Similarity">
      <div className="similarity-head">
        <h1>Similarity analysis</h1>
        {report !== null && (
          <span className="similarity-head-badges">
            <Badge tone="soft">
              {entries.length} {plural(entries.length, 'CLUSTER', 'CLUSTERS')}
            </Badge>
          </span>
        )}
        {on && read.threshold !== null && (
          <span className="similarity-head-control">
            <RangeSlider
              label="SIMILARITY"
              min={thresholdMin}
              max={thresholdMax}
              step={thresholdStep}
              value={read.threshold}
              onChange={read.setThreshold}
            />
          </span>
        )}
      </div>
      <p className="lead similarity-lead">
        Artifacts whose descriptions may be too close for search and load_domain to tell apart.
      </p>
      {read.error !== null && (
        <ErrorState error={read.error} title="The overlap report did not load." onRetry={read.reload} />
      )}
      {report === null && read.error === null && <Loading label="Loading the overlap report." />}
      {report !== null && entries.length === 0 && (
        <OverlapEmpty testID="similarity-empty">
          {on && read.threshold !== null ? `No clusters at ${formatThreshold(read.threshold)}` : 'No clusters'}
        </OverlapEmpty>
      )}
      {report !== null && current !== null && (
        <div className="similarity-panel">
          <ClusterList
            entries={entries}
            selected={current.artifact.artifact_id}
            onSelect={select}
            showSimilarity={on}
          />
          <ClusterDetail entry={current} showSimilarity={on} />
        </div>
      )}
    </section>
  );
}

/** ClusterDetail is the selected entry: its identifier, type, and layer, its
 * description, the registry's messages, and the artifacts it overlaps. It
 * scrolls apart from the list beside it. */
function ClusterDetail({ entry, showSimilarity }: { entry: ClusterEntry; showSimilarity: boolean }) {
  const { artifact } = entry;
  return (
    <div className="cluster-detail" data-testid="cluster-detail">
      <p className="cluster-detail-head">
        <a className="mono cluster-detail-id" href={artifactHref(artifact.artifact_id)}>
          {artifact.artifact_id}
        </a>
        <TypeBadge type={artifact.type} />
        <span className="mono cluster-detail-layer">{artifact.layer}</span>
      </p>
      <p className="cluster-detail-description">
        {artifact.description === '' ? <span className="quiet">No description.</span> : artifact.description}
      </p>
      <MessageBox diagnostics={entry.diagnostics} />
      <OverlapTable rows={entry.peers} showSimilarity={showSimilarity} peerHref={(id) => artifactHref(id)} />
    </div>
  );
}
