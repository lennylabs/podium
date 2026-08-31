// One row per catalog entity. The domain browser and the search surface
// receive the same descriptor, so both render this row and neither formats
// the same field twice.

import {
  CuratedBadge,
  FoldedFromBadge,
  SensitivityBadge,
  SurfacedLabel,
  TypeBadge,
  formatVersion,
} from './primitives';
import type { ArtifactDescriptor } from '../api';
import { artifactHref, artifactLeaf } from '../route';

/** relevanceBars is how many bars the indicator draws. */
const relevanceBars = 4;

/** filledBars is how many of the bars the row at rank fills, out of a result
 * set of resultCount rows. The indicator reads the position the registry
 * returned the row in rather than the descriptor's score: the score is the
 * pre-rerank lexical score, and the §12 usage rerank and the §4.7.3
 * dependency rerank reorder the results afterwards without rewriting it
 * (`pkg/registry/core/core.go`), so a score-derived indicator contradicts the
 * order its own list is printed in. The strongest bar therefore always sits
 * at the top of the list and the bars descend with the rows. Every ranked row
 * fills at least one bar, so a ranked row never reads as an unranked one. */
function filledBars(rank: number, resultCount: number): number {
  if (resultCount <= 0) {
    return 0;
  }
  const filled = Math.ceil(((resultCount - rank) / resultCount) * relevanceBars);
  return Math.min(Math.max(filled, 1), relevanceBars);
}

/** RelevanceBars renders the rank as bars rather than as a number, which is
 * what the row has to say about a position whose distance from its neighbours
 * the response does not report. The element holds its width on that row so the
 * bars stay on one x position down the result set.
 *
 * The indicator is drawn on a ranked row alone, which is a property of the
 * surface rather than of the descriptor: an unranked listing such as the
 * domain browser presents its entries in identifier order and draws no
 * indicator at all. */
function RelevanceBars({ filled }: { filled: number }) {
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
 * indicator and where it places its type and version. */
export function ArtifactRow({
  artifact,
  ranked = false,
  rank = 0,
  resultCount = 0,
  matchedByMeaning = false,
  titled = false,
  dense = false,
}: {
  artifact: ArtifactDescriptor;
  ranked?: boolean;
  /** rank is the row's zero-based position in the ranked result set, and
   * resultCount is how many rows that set holds. The relevance indicator is
   * drawn from the two, so it states the ranking the registry returned. A
   * resultCount of zero is a set the registry did not rank by relevance, and
   * such a set carries no indicator. */
  rank?: number;
  resultCount?: number;
  /** matchedByMeaning marks a result the registry fused in from vector
   * retrieval alone, which the surface reads off the descriptor's absent
   * score. It says how the row matched rather than how strongly, so the row
   * keeps its rank indicator and carries the label beside it. */
  matchedByMeaning?: boolean;
  /** titled marks a row that already stands under a head naming what it is.
   * The at-scale surface groups the domain author's picks under such a head,
   * and a curated marker on every row under it states the head again once per
   * row. */
  titled?: boolean;
  /** dense marks a row drawn beside the at-scale artifact table, which clips
   * every description to one line and carries the tags in a column of their
   * own. A row that keeps the listing treatment beside that table draws the
   * same kind of entry at roughly twice the height, so the block above the
   * table pushes it down the page and the surface reads as two listings
   * rather than one (§13.10). */
  dense?: boolean;
}) {
  const version = artifact.version !== undefined && artifact.version !== '' ? artifact.version : '';
  const filled = ranked ? filledBars(rank, resultCount) : 0;
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
    <li className={dense ? 'artifact-row artifact-row-dense' : 'artifact-row'}>
      {/* The relevance column. The indicator leads the row rather than
          trailing the badges, because a badge row is as wide as the values it
          happens to carry: drawn after it, the bars land on a different x
          position on every row and stop reading as a column. */}
      {filled > 0 && (
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
          {/* The marks are one group rather than loose siblings of the
              identifier. An identifier that fills the row otherwise leaves the
              first mark behind on its line and wraps the rest, which draws one
              cluster of metadata as two lines and separates the type from its
              version. Grouped, the cluster wraps as a unit. The group is empty
              on a row that carries no mark at all, and CSS drops it so the
              head's gap does not print after the identifier. */}
          <span className="artifact-row-marks">
            {/* A ranked row keeps its type and version inline, beside the
                identifier its relevance is measured on. A listing row moves
                them to the column at the row's right edge. */}
            {ranked && marks}
            <SensitivityBadge sensitivity={artifact.sensitivity} />
            {/* The §4.5.5 notable source, named on both its arms. The list is
                selected from the domain's featured: entries and from the
                usage-ranked rest, and marking the featured half alone leaves
                the other half stating nothing, which a reader can only read as
                the second source once they have seen a curated row elsewhere
                in the catalog. A row under a head that already names its half
                carries neither mark. */}
            {!titled &&
              (artifact.source === 'featured' ? (
                <CuratedBadge />
              ) : (
                artifact.source === 'signal' && <SurfacedLabel />
              ))}
            <FoldedFromBadge foldedFrom={artifact.folded_from} />
            {matchedByMeaning && <span className="quiet label">matched by meaning</span>}
          </span>
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
        {!dense && artifact.tags !== undefined && artifact.tags.length > 0 && (
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
