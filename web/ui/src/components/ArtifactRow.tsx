// One row per catalog entity. The domain browser and the search surface
// receive the same descriptor, so both render this row and neither formats
// the same field twice.

import { Badge, SensitivityBadge } from './primitives';
import type { ArtifactDescriptor } from '../api';
import { artifactHref, artifactLeaf } from '../route';

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
 * own. A result matched only by vector similarity is fused in with a zero
 * score, which is a property of how it matched rather than a weak match, so
 * it draws no bars and carries a label instead. The bar column holds its
 * width on that row so the rows stay aligned.
 *
 * The indicator is drawn on a ranked row alone, which is a property of the
 * surface rather than of the descriptor: the registry marshals the score with
 * omitempty, so a zero score and an unscored descriptor are indistinguishable
 * on the wire. A search row therefore reads an absent score as the vector-only
 * arm, and an unranked listing such as the domain browser draws no indicator
 * at all. */
function Relevance({ ranked, score, topScore }: { ranked: boolean; score?: number; topScore: number }) {
  if (!ranked) {
    return null;
  }
  const filled = filledBars(score ?? 0, topScore);
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

/** ArtifactRow draws one entry. ranked marks a row that arrived from a ranked
 * result set, which is what decides whether the row carries a relevance
 * indicator, and topScore is the strongest score in that set, which the
 * indicator ranks against. */
export function ArtifactRow({
  artifact,
  ranked = false,
  topScore = 0,
}: {
  artifact: ArtifactDescriptor;
  ranked?: boolean;
  topScore?: number;
}) {
  const version = artifact.version !== undefined && artifact.version !== '' ? artifact.version : '';
  return (
    <li className="artifact-row">
      <div className="artifact-row-body">
        {/* The identifying line. A listing row names the artifact and states
            its full path beside it, because the domain is already the page;
            a ranked row leads with the whole identifier, because a result
            set spans domains and the path is what distinguishes two rows. */}
        <div className="artifact-row-head">
          <a className="mono artifact-id" href={artifactHref(artifact.id)}>
            {ranked ? artifact.id : artifactLeaf(artifact.id)}
          </a>
          {!ranked && <span className="mono quiet artifact-path">{artifact.id}</span>}
          {/* A ranked row keeps its type and version inline, beside the
              identifier its relevance is measured on. A listing row moves
              them to the column at the row's right edge. */}
          {ranked && (
            <>
              <Badge>{artifact.type}</Badge>
              {version !== '' && <Badge tone="quiet">{version}</Badge>}
            </>
          )}
          <SensitivityBadge sensitivity={artifact.sensitivity} />
          {artifact.source === 'featured' && <Badge tone="accent">curated</Badge>}
          {artifact.source === 'signal' && <span className="quiet label">surfaced by usage</span>}
          {artifact.folded_from !== undefined && artifact.folded_from !== '' && (
            <Badge tone="quiet">from {artifact.folded_from}</Badge>
          )}
          <Relevance ranked={ranked} score={artifact.score} topScore={topScore} />
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
      </div>
      {/* The right-hand column. Type and version sit at a fixed edge down
          the listing, so the reader scans one column instead of reading
          across each row's second line. */}
      {!ranked && (
        <div className="artifact-row-aside" data-testid="artifact-row-aside">
          <Badge>{artifact.type}</Badge>
          {version !== '' && <span className="mono quiet artifact-version">{version}</span>}
        </div>
      )}
    </li>
  );
}
