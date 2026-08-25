// One row per catalog entity. The domain browser and the search surface
// receive the same descriptor, so both render this row and neither formats
// the same field twice.

import { Badge } from './primitives';
import type { ArtifactDescriptor } from '../api';
import { artifactHref } from '../route';

/** relevanceBars is how many bars the indicator draws. */
const relevanceBars = 4;

/** filledBars is how many of the bars a score fills. A lexical score has no
 * fixed upper bound, so it carries no absolute strength and the indicator
 * ranks it against the strongest score in the same result set, which is the
 * set the reader compares rows inside. A score above zero fills at least one
 * bar, so a ranked row never reads as an unranked one. */
function filledBars(score: number, topScore: number): number {
  if (score <= 0 || topScore <= 0) {
    return 0;
  }
  const filled = Math.ceil((score / topScore) * relevanceBars);
  return Math.min(Math.max(filled, 1), relevanceBars);
}

/** Relevance renders the lexical rank as bars rather than as a number, which
 * is what the row has to say about a score whose scale means nothing on its
 * own. A result matched only by vector similarity arrives with a zero score,
 * which is a property of how it matched rather than a weak match, so it draws
 * no bars and carries a label instead. The bar column holds its width on that
 * row so the rows stay aligned. A descriptor carrying no score at all, which
 * is every domain-browser entry, renders nothing. */
function Relevance({ score, topScore }: { score?: number; topScore: number }) {
  if (score === undefined) {
    return null;
  }
  const filled = filledBars(score, topScore);
  if (filled === 0) {
    return (
      <>
        <span className="relevance" data-testid="relevance-bars" data-filled="0" aria-hidden="true" />
        <span className="quiet label">matched by meaning</span>
      </>
    );
  }
  return (
    <span
      className="relevance"
      data-testid="relevance-bars"
      data-filled={String(filled)}
      role="img"
      aria-label={`relevance ${String(filled)} of ${String(relevanceBars)}`}
    >
      {Array.from({ length: relevanceBars }, (_, i) => (
        <span key={i} className={i < filled ? 'relevance-bar relevance-bar-filled' : 'relevance-bar'} />
      ))}
    </span>
  );
}

/** ArtifactRow draws one entry. topScore is the strongest score in the result
 * set the entry arrived in, which the relevance indicator ranks against; a
 * listing that carries no scores passes zero and draws no indicator. */
export function ArtifactRow({ artifact, topScore = 0 }: { artifact: ArtifactDescriptor; topScore?: number }) {
  return (
    <li className="artifact-row">
      <a className="mono artifact-id" href={artifactHref(artifact.id)}>
        {artifact.id}
      </a>
      <div className="artifact-meta">
        <Badge>{artifact.type}</Badge>
        {artifact.version !== undefined && artifact.version !== '' && <Badge tone="quiet">{artifact.version}</Badge>}
        {artifact.sensitivity !== undefined && artifact.sensitivity !== '' && (
          <Badge tone="quiet">{artifact.sensitivity}</Badge>
        )}
        {artifact.source === 'featured' && <Badge tone="accent">curated</Badge>}
        {artifact.source === 'signal' && <span className="quiet label">surfaced by usage</span>}
        {artifact.folded_from !== undefined && artifact.folded_from !== '' && (
          <Badge tone="quiet">from {artifact.folded_from}</Badge>
        )}
        <Relevance score={artifact.score} topScore={topScore} />
      </div>
      {artifact.description !== undefined && artifact.description !== '' && (
        <p className="artifact-description">{artifact.description}</p>
      )}
      {artifact.tags !== undefined && artifact.tags.length > 0 && (
        <ul className="tag-list">
          {artifact.tags.map((tag) => (
            <li key={tag} className="tag">
              {tag}
            </li>
          ))}
        </ul>
      )}
    </li>
  );
}
