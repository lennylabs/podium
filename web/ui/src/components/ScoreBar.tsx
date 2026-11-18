// The two overlap scores and their marks. A semantic similarity is a cosine
// of two embeddings and a word overlap is the share of words two descriptions
// hold in common, so the scales are not comparable: each has its own colour
// and its own number format, and no column or sort carries both (§13.10).

import { formatSimilarity, formatWordOverlap } from '../similarity';

/** ScoreKind names which of the two overlap scores a mark carries. */
export type ScoreKind = 'similarity' | 'word';

/** ScoreBar draws one score as a short bar filled to the value beside the
 * figure: the accent bar and a decimal for a similarity, the grey bar and a
 * percentage for a word overlap. A score the check did not produce is drawn
 * as an em dash, because a blank cell reads as a row that failed to render
 * and a zero reads as a measured score.
 *
 * Spec: §13.10 */
export function ScoreBar({ kind, value }: { kind: ScoreKind; value?: number }) {
  if (value === undefined) {
    return <ScoreAbsent />;
  }
  const fill = `${String(Math.round(Math.max(0, Math.min(1, value)) * 100))}%`;
  return (
    <span className={`score score-${kind}`}>
      <span className="score-track" aria-hidden="true">
        <span className="score-fill" style={{ width: fill }} />
      </span>
      {kind === 'similarity' ? formatSimilarity(value) : formatWordOverlap(value)}
    </span>
  );
}

/** ScoreAbsent is the em dash a missing score is drawn as. The dash is
 * decoration to a screen reader, so the cell names the absence in words. */
export function ScoreAbsent() {
  return (
    <span className="score-absent">
      <span aria-hidden="true">—</span>
      <span className="assistive-only">none</span>
    </span>
  );
}
