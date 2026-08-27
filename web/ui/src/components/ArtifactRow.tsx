// One row per catalog entity. The domain browser and the search surface
// receive the same descriptor, so both render this row and neither formats
// the same field twice.

import { CuratedBadge, FoldedFromBadge, SensitivityBadge, TypeBadge, formatVersion } from './primitives';
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

/** RelevanceBars renders the lexical rank as bars rather than as a number,
 * which is what the row has to say about a score whose scale means nothing on
 * its own. A result matched only by vector similarity is fused in with a zero
 * score, which is a property of how it matched rather than a weak match, so it
 * draws no bars and the row carries a label instead. The element holds its
 * width on that row so the bars stay on one x position down the result set.
 *
 * The indicator is drawn on a ranked row alone, which is a property of the
 * surface rather than of the descriptor: the registry marshals the score with
 * omitempty, so a zero score and an unscored descriptor are indistinguishable
 * on the wire. An unranked listing such as the domain browser draws no
 * indicator at all.
 *
 * A ranked row reads an absent score as the vector-only arm only where the
 * result set carries a score at all. A set where no row does is not a lexical
 * ranking with vector-only rows fused into it: an empty query returns every
 * match at score zero (`pkg/registry/core/core.go`), and a registry serving
 * BM25 alone (§13.10 `--no-embeddings`) runs no vector retrieval to fuse in.
 * Reading that set as semantic matching would label every row for a match the
 * deployment never performed, so an unscored set draws no indicator and no
 * label. */
function RelevanceBars({ filled }: { filled: number }) {
  if (filled === 0) {
    return <span className="relevance" data-testid="relevance-bars" data-filled="0" aria-hidden="true" />;
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
 * indicator and where it places its type and version, and topScore is the
 * strongest score in that set, which the indicator ranks against. A topScore of zero is a set nothing was scored in, and such a set
 * carries no relevance indicator. */
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
  const scored = ranked && topScore > 0;
  const filled = scored ? filledBars(artifact.score ?? 0, topScore) : 0;
  /* The type and the version are drawn once here and placed twice below, so a
     row states them the same way wherever the surface puts them. The type is
     one of a closed vocabulary and carries the badge outline; the version is a
     number the reader reads off the row rather than scans for, so it stays
     bare mono meta. Boxing it too gives a row three equal-weight pills and
     makes the version read as another classification. */
  const marks = (
    <>
      <TypeBadge type={artifact.type} />
      {version !== '' && <span className="mono quiet artifact-version">{formatVersion(version)}</span>}
    </>
  );
  return (
    <li className="artifact-row">
      {/* The relevance column. The indicator leads the row rather than
          trailing the badges, because a badge row is as wide as the values it
          happens to carry: drawn after it, the bars land on a different x
          position on every row and stop reading as a column. */}
      {scored && (
        <div className="artifact-row-relevance" data-testid="artifact-row-relevance">
          <RelevanceBars filled={filled} />
        </div>
      )}
      <div className="artifact-row-body">
        {/* The identifying line, and what it states depends on the context the
            surface has already given the reader. A listing row hangs under a
            domain heading that names every level above it, so the row leads
            with the leaf in the link tone and carries the full path beside it
            in quiet mono, which is what tells two rows of the same name apart
            across domains. A ranked result set spans the whole catalog and
            supplies no such heading, so the row's link carries the whole
            identifier: splitting it there prints the leaf twice on one line
            and leaves the reader reading the same name in two tones. */}
        <div className="artifact-row-head">
          <a className="mono artifact-id" href={artifactHref(artifact.id)}>
            {ranked ? artifact.id : artifactLeaf(artifact.id)}
          </a>
          {!ranked && <span className="mono quiet artifact-path">{artifact.id}</span>}
          {/* A ranked row keeps its type and version inline, beside the
              identifier its relevance is measured on. A listing row moves
              them to the column at the row's right edge. */}
          {ranked && marks}
          <SensitivityBadge sensitivity={artifact.sensitivity} />
          {/* The notable source is drawn on its "featured" arm alone. The
              registry tags every entry the domain's featured: list does not
              name as "signal" (§4.5.5), whether or not any usage signal
              contributed to it, so a "surfaced by usage" marker lands on every
              row of a registry that has served no traffic and states a reason
              the response does not report. The row therefore distinguishes
              what the response distinguishes, which is featured against the
              rest. */}
          {artifact.source === 'featured' && <CuratedBadge />}
          <FoldedFromBadge foldedFrom={artifact.folded_from} />
          {scored && filled === 0 && <span className="quiet label">matched by meaning</span>}
        </div>
        {/* A manifest carries no required description, and the row's aside
            column holds the row's height whether the line is drawn or not, so
            omitting it leaves blank space under the identifier that reads as a
            description that failed to render. The absent case therefore states
            itself, in the quiet tone the subdomain card and the compact
            listing already state it in. */}
        {artifact.description !== undefined && artifact.description !== '' ? (
          <p className="artifact-description">{artifact.description}</p>
        ) : (
          <p className="artifact-description quiet absent-description">No description.</p>
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
          {marks}
        </div>
      )}
    </li>
  );
}
