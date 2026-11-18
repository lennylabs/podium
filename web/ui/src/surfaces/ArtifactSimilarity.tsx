// The artifact viewer's similarity parts: the Similarity tab and the rail's
// similarity section. Both render the one overlap read the viewer takes for
// the artifact it displays, restricted with the report's artifact parameter.
// The registry answers an artifact the caller cannot see with the same empty
// report as one that overlaps nothing, so both parts have one state for the
// two, and neither can tell them apart (§13.10).

import { useState } from 'react';

import { MessageBox, OverlapEmpty, ThinNotice } from '../components/MessageBox';
import { DescriptionCompare, OverlapTable } from '../components/OverlapTable';
import { EmptyState, ErrorState, Loading } from '../components/primitives';
import { RangeSlider } from '../components/RangeSlider';
import { artifactHref, similarityHref } from '../route';
import type { ClusterEntry } from '../similarity';
import {
  countWord,
  formatStrongest,
  formatThreshold,
  formatWordOverlap,
  overlapEntry,
  plural,
  semanticOn,
  thinDiagnostic,
  thresholdMax,
  thresholdMin,
  thresholdStep,
} from '../similarity';
import type { OverlapRead } from './overlapread';

/** similarityCount is the figure the Similarity tab carries: how many
 * artifacts overlap the one on screen. It is empty while the read is in
 * flight, where it failed, and where nothing overlaps, so the tab stands
 * without a figure rather than stating a zero. */
export function similarityCount(read: OverlapRead, id: string): string {
  if (read.report === null) {
    return '';
  }
  const entry = overlapEntry(read.report, id);
  return entry === null ? '' : String(entry.peers.length);
}

/** SimilarityTab is the viewer's Similarity tab: a line stating how many
 * artifacts read like this one beside the threshold control, the registry's
 * messages, the table of overlapping artifacts, the comparison a selected row
 * opens, and the state of the description's length.
 *
 * Spec: §13.10 */
export function SimilarityTab({
  read,
  id,
  description,
  now,
}: {
  read: OverlapRead;
  id: string;
  /** description is the artifact's own description as the viewer states it.
   * The description-length check skips an empty description, so the tab
   * reads it to avoid calling an absent description long enough. */
  description: string;
  now: number;
}) {
  const [selected, setSelected] = useState('');
  if (read.report === null) {
    return read.error !== null ? (
      <ErrorState error={read.error} title="The overlap report did not load." onRetry={read.reload} />
    ) : (
      <Loading label="Loading the overlap report." />
    );
  }
  const report = read.report;
  const on = semanticOn(report);
  const entry = overlapEntry(report, id);
  const thin = thinDiagnostic(report, id);
  const row = entry?.peers.find((peer) => peer.peer.artifact_id === selected);
  return (
    <div className="similarity-tab">
      <div className="similarity-intro">
        {entry !== null && <p className="similarity-intro-text">{introSentence(entry)}</p>}
        {on && read.threshold !== null && (
          <RangeSlider
            label="SIMILARITY"
            min={thresholdMin}
            max={thresholdMax}
            step={thresholdStep}
            value={read.threshold}
            onChange={read.setThreshold}
          />
        )}
      </div>
      {read.error !== null && (
        <ErrorState error={read.error} title="The overlap report did not load." onRetry={read.reload} />
      )}
      {thin !== null && <ThinNotice message={thin.message} />}
      {entry === null ? (
        <OverlapEmpty testID="similarity-tab-empty">
          {on && read.threshold !== null
            ? `No overlapping artifacts at ${formatThreshold(read.threshold)}`
            : 'No overlapping artifacts'}
        </OverlapEmpty>
      ) : (
        <>
          <MessageBox diagnostics={entry.diagnostics} />
          <OverlapTable
            rows={entry.peers}
            showSimilarity={on}
            peerHref={(peer) => artifactHref(peer)}
            selected={row?.peer.artifact_id ?? ''}
            onSelect={setSelected}
          />
          {row !== undefined && (
            <DescriptionCompare
              subject={entry.artifact}
              row={row}
              showSimilarity={on}
              href={artifactHref(row.peer.artifact_id)}
              now={now}
            />
          )}
        </>
      )}
      {thin === null && <LengthStatus description={description} />}
    </div>
  );
}

/** introSentence states how many artifacts read like this one, in the words
 * the design gives the tab. */
function introSentence(entry: ClusterEntry): string {
  const n = entry.peers.length;
  return `${countWord(n)} ${plural(n, 'artifact reads', 'artifacts read')} like this one. Search and load_domain may not tell them apart.`;
}

/** LengthStatus states that the description is long enough to tell apart,
 * which is what the report says by carrying no description-length
 * diagnostic for the artifact. An empty description draws no diagnostic
 * either, so it is stated as absent rather than as long enough. */
function LengthStatus({ description }: { description: string }) {
  if (description.trim() === '') {
    return <p className="length-status quiet">This artifact states no description.</p>;
  }
  return (
    <p className="length-status" data-testid="length-status">
      <span className="length-status-mark" aria-hidden="true">
        ✓
      </span>
      The description is long enough to tell apart.
    </p>
  );
}

/** RailSimilarity is the rail's similarity section, drawn on every tab: how
 * many artifacts overlap this one and the strongest similarity, how many
 * share wording and the strongest overlap, whether the description is long
 * enough, and the way to the similarity page. Where the read carried no
 * semantic check the similarity line is not drawn.
 *
 * Spec: §13.10 */
export function RailSimilarity({ read, id, description }: { read: OverlapRead; id: string; description: string }) {
  return (
    <section aria-label="Similarity">
      <p className="label">Similarity</p>
      {read.report === null && read.error === null && <Loading label="Loading the overlap report." />}
      {read.report === null && read.error !== null && <ErrorState error={read.error} onRetry={read.reload} />}
      {read.report !== null && <RailSimilarityFacts read={read} id={id} description={description} />}
    </section>
  );
}

function RailSimilarityFacts({ read, id, description }: { read: OverlapRead; id: string; description: string }) {
  const report = read.report;
  if (report === null) {
    return null;
  }
  const on = semanticOn(report);
  const entry = overlapEntry(report, id);
  const thin = thinDiagnostic(report, id);
  const semantic = on ? (entry?.peers.filter((row) => row.similarity !== undefined) ?? []) : [];
  const word = entry?.peers.filter((row) => row.wordOverlap !== undefined) ?? [];
  return (
    <div className="rail-similarity" data-testid="rail-similarity">
      {semantic.length > 0 && entry?.strongestSimilarity !== undefined && (
        <p className="rail-similarity-line">
          <span className="rail-similarity-mark rail-similarity-mark-similarity" aria-hidden="true" />
          {semantic.length} overlapping {plural(semantic.length, 'artifact', 'artifacts')},{' '}
          {semantic.length === 1 ? 'at' : 'up to'} {formatStrongest(entry.strongestSimilarity)}
        </p>
      )}
      {word.length > 0 && entry?.strongestWordOverlap !== undefined && (
        <p className="rail-similarity-line">
          <span className="rail-similarity-mark rail-similarity-mark-word" aria-hidden="true" />
          {word.length} with {word.length === 1 ? '' : 'up to '}
          {formatWordOverlap(entry.strongestWordOverlap)} word overlap
        </p>
      )}
      {entry === null && <EmptyState scope="inline">No overlapping artifacts</EmptyState>}
      {thin !== null ? (
        <p className="rail-similarity-line" title={thin.message}>
          <span className="message-dot message-dot-similarity rail-similarity-dot" aria-hidden="true" />
          Description is too short to tell apart
        </p>
      ) : description.trim() === '' ? (
        <p className="rail-similarity-line quiet">No description</p>
      ) : (
        <p className="rail-similarity-line">
          <span className="length-status-mark rail-similarity-check" aria-hidden="true">
            ✓
          </span>
          Description length is fine
        </p>
      )}
      <a className="rail-similarity-link" href={similarityHref(entry === null ? '' : id)}>
        See it in Similarity analysis →
      </a>
    </div>
  );
}
