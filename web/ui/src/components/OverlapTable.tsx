// The table of artifacts one artifact overlaps, and the comparison a row
// opens. Descriptions, types, and layers arrive with the report, so nothing
// here is fetched per row.

import type { OverlapArtifact } from '../api';
import { artifactLeaf } from '../route';
import type { PeerRow } from '../similarity';
import { formatSimilarity, formatWordOverlap } from '../similarity';
import { since } from '../time';
import { TypeBadge } from './primitives';
import { ScoreBar } from './ScoreBar';

/** OverlapTable lists one row per overlapping artifact with the similarity
 * and the word overlap in separate columns. Where the read carried no
 * semantic check, the similarity column is not drawn.
 *
 * Given `onSelect`, the table is the viewer's: framed, and each row is a
 * button that selects the peer for the comparison drawn under the table.
 * Without it the table is the similarity page's, unframed, and each peer
 * identifier links to the artifact.
 *
 * Spec: §13.10 */
export function OverlapTable({
  rows,
  showSimilarity,
  peerHref,
  selected,
  onSelect,
}: {
  rows: PeerRow[];
  showSimilarity: boolean;
  peerHref: (id: string) => string;
  selected?: string;
  onSelect?: (id: string) => void;
}) {
  const columns = showSimilarity ? 'overlap-grid overlap-grid-both' : 'overlap-grid overlap-grid-word';
  const framed = onSelect !== undefined;
  return (
    <div className={framed ? 'overlap-table overlap-table-framed' : 'overlap-table'} data-testid="overlap-table">
      <div className={`${columns} overlap-head label`} aria-hidden="true">
        <span>Overlaps with</span>
        <span>Layer</span>
        {showSimilarity && <span>Similarity</span>}
        <span>Word overlap</span>
      </div>
      {rows.map((row) => {
        const id = row.peer.artifact_id;
        const cells = (
          <>
            <span className="overlap-peer">
              <span className="overlap-peer-name">
                {framed ? (
                  <span className="mono overlap-id">{id}</span>
                ) : (
                  <a className="mono overlap-id" href={peerHref(id)}>
                    {id}
                  </a>
                )}
                <TypeBadge type={row.peer.type} />
              </span>
              <span className="overlap-description">{row.peer.description}</span>
            </span>
            <span className="mono overlap-layer">{row.peer.layer}</span>
            {showSimilarity && (
              <span className="overlap-score">
                <ScoreBar kind="similarity" value={row.similarity} />
              </span>
            )}
            <span className="overlap-score">
              <ScoreBar kind="word" value={row.wordOverlap} />
            </span>
          </>
        );
        if (!framed) {
          return (
            <div key={id} className={`${columns} overlap-row`} data-testid="overlap-row">
              {cells}
            </div>
          );
        }
        const pressed = selected === id;
        return (
          <button
            key={id}
            type="button"
            aria-pressed={pressed}
            className={pressed ? `${columns} overlap-row overlap-row-selected` : `${columns} overlap-row`}
            data-testid="overlap-row"
            onClick={() => {
              onSelect(id);
            }}
          >
            {cells}
          </button>
        );
      })}
    </div>
  );
}

/** DescriptionCompare sets the artifact on screen beside the peer selected in
 * the table: a heading naming both, the scores the row carries and when the
 * semantic one was computed, a link to the peer, and the two descriptions
 * side by side. The heading names each artifact by its leaf and carries the
 * whole identifier as its title, because the table above already states the
 * full identifiers.
 *
 * Spec: §13.10 */
export function DescriptionCompare({
  subject,
  row,
  showSimilarity,
  href,
  now,
}: {
  subject: OverlapArtifact;
  row: PeerRow;
  showSimilarity: boolean;
  href: string;
  now: number;
}) {
  const subjectName = artifactLeaf(subject.artifact_id);
  const peerName = artifactLeaf(row.peer.artifact_id);
  const facts: string[] = [];
  if (showSimilarity && row.similarity !== undefined) {
    facts.push(`similarity ${formatSimilarity(row.similarity)}`);
  }
  if (row.wordOverlap !== undefined) {
    facts.push(`word overlap ${formatWordOverlap(row.wordOverlap)}`);
  }
  if (showSimilarity && row.computedAt !== undefined) {
    facts.push(`computed ${since(row.computedAt, now)}`);
  }
  return (
    <section className="compare" aria-label={`${subject.artifact_id} compared with ${row.peer.artifact_id}`}>
      <div className="compare-head">
        <p className="mono compare-title">
          <span title={subject.artifact_id}>{subjectName}</span> <span className="compare-vs">vs.</span>{' '}
          <span title={row.peer.artifact_id}>{peerName}</span>
        </p>
        {facts.length > 0 && <p className="mono compare-facts">{facts.join(' · ')}</p>}
        <a className="compare-open" href={href}>
          Open {peerName} →
        </a>
      </div>
      <div className="compare-cards">
        <CompareCard name={subjectName} id={subject.artifact_id} tag="this artifact" artifact={subject} />
        <CompareCard name={peerName} id={row.peer.artifact_id} tag={row.peer.type} artifact={row.peer} />
      </div>
    </section>
  );
}

function CompareCard({
  name,
  id,
  tag,
  artifact,
}: {
  name: string;
  id: string;
  tag: string;
  artifact: OverlapArtifact;
}) {
  return (
    <div className="compare-card">
      <p className="compare-card-head">
        <span className="mono compare-card-name" title={id}>
          {name}
        </span>
        <span className="mono compare-card-meta">
          {tag} · {artifact.layer}
        </span>
      </p>
      <p className="compare-card-description">
        {artifact.description === '' ? <span className="quiet">No description.</span> : artifact.description}
      </p>
    </div>
  );
}
