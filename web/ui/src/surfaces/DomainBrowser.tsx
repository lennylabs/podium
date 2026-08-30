// The domain browser: the entry point and the primary navigation over the
// §4.2 domain hierarchy. It renders the caller's position in that hierarchy
// at every level, and it states nothing about what a response does not carry.

import { ArtifactTable, SubdomainTiles } from './DomainAtScale';
import { ArtifactRow } from '../components/ArtifactRow';
import { Breadcrumb } from '../components/Breadcrumb';
import { Badge, Chevron, EmptyState, ErrorPage, Loading, PathLabel } from '../components/primitives';
import type { DomainDescriptor } from '../api';
import { catalogArtifactIDs, loadDomain } from '../api';
import {
  artifactCountLabel,
  artifactCounts,
  directArtifactCount,
  domainLabel,
  subdomainCountLabel,
} from '../domain';
import { domainHref, domainTitle, searchHref } from '../route';
import { useAsync, useErrorReport } from '../useAsync';

// The browser lists the immediate children and follows a link for the rest,
// and it reads the second returned level for the child's own subdomain count
// rather than drawing it. Nesting the grandchildren inside their parent's card
// turns one screen into the whole hierarchy, so the request stays two levels
// deep and the page stays one.
const renderedDepth = 2;

/** atScale is the child count past which the compact treatment takes over.
 * A card grid stops being a map of the domain somewhere around here, so the
 * subdomains become count tiles and the artifacts a sortable table. */
const atScale = 20;

export function DomainBrowser({ path, onError }: { path: string; onError: (err: unknown) => void }) {
  const domain = useAsync(() => loadDomain(path, renderedDepth), [path]);
  // §4.5.5 caps the notable list at the configured notable_count and surfaces
  // a rendering note only for the two reductions the spec names, the response
  // budget and the depth ceiling. A domain the cap trimmed therefore returns a
  // short listing and no note, so the note alone cannot tell the page what the
  // domain holds. The §4.5.2 catalog read can: it returns every visible ID
  // under the scope and the registry does not truncate it. One read per domain
  // page serves both the header count and the at-scale tile counts.
  const catalog = useAsync(() => catalogArtifactIDs(path), [path]);
  useErrorReport(domain.error, onError);

  if (domain.loading) {
    return <Loading label="Loading the domain." />;
  }
  if (domain.error !== null) {
    // The browser has no header, no listing, and no breadcrumb left standing
    // around a banner, so a failed read is the whole surface.
    return (
      <ErrorPage
        error={domain.error}
        title="No such domain"
        subject={path === '' ? undefined : path}
        onRetry={domain.reload}
        testID="domain-failed"
      />
    );
  }
  const body = domain.value;
  if (body === null) {
    return null;
  }
  const direct = body.notable.filter((a) => a.folded_from === undefined || a.folded_from === '');
  const folded = body.notable.filter((a) => a.folded_from !== undefined && a.folded_from !== '');
  // held is what the catalog reports the domain holds directly, and total is
  // that figure once it exceeds the listing. A catalog read that failed leaves
  // both unknown, and the page states what load_domain returned.
  const held = catalog.value === null ? null : directArtifactCount(catalog.value, body.path);
  const total = held !== null && held > direct.length ? held : null;
  const trimmed = total !== null || (body.note !== undefined && body.note !== '');
  const compact = body.subdomains.length > atScale;
  const root = body.path === '';
  // §4.5.5 collapses a sparse subdomain into its parent's leaf set, and a
  // domain whose every entry arrived that way returns an empty subdomain list
  // and an empty direct listing while still carrying the entries the header
  // counts. Stating both absences above the lifted group contradicts that
  // count and spends the first screen saying nothing is there, so where the
  // folded group is the whole of what the domain holds, the two empty panels
  // stand down and the group is what the reader meets.
  const foldedOnly = folded.length > 0 && body.subdomains.length === 0 && direct.length === 0;
  // The artifact badge counts the whole subtree rather than what the domain
  // holds directly. §4.5.2 returns every visible ID under the scope, which is
  // the figure the subdomain tiles below the header already sum to, so a badge
  // drawn from the direct listing reads smaller than the counts the same
  // screen prints and reads as zero on a domain that carries everything one
  // level down. The figure stays out of `total`, which drives the
  // trimmed-listing line: the listing is not a truncated view of the artifacts
  // under the subdomains, they sit under the links the page already draws.
  //
  // A catalog read that failed leaves the subtree unknown, and the page falls
  // back to what `load_domain` returned.
  const subtreeHeld = catalog.value === null ? null : catalog.value.length;
  // The trimmed listing is continued at the end of the list rather than
  // announced above it: the reader meets it where the returned edge is. It is
  // the list's own last row, so a listing that carries rows takes it inside
  // the card, and a table or an empty listing carries it in a card of its own
  // rather than as a note detached under the page.
  const tail = trimmed ? (
    <TrimmedListing scope={body.path} shown={direct.length} total={total} note={body.note ?? ''} />
  ) : null;
  const tailList = tail === null ? null : <ul className="artifact-list">{tail}</ul>;

  return (
    <section className="surface" aria-label="Domain browser">
      <Breadcrumb path={body.path} />
      <div className="domain-head">
        <h1>{domainTitle(body.path)}</h1>
        <div className="domain-counts">
          {/* The badge is what the catalog holds under the domain rather than
              what the listing returned, so a trimmed listing is not presented
              as the whole domain and the header agrees with the tiles under
              it. Where the catalog read failed the listing stands in, and the
              trimmed total wins over it where the two disagree. */}
          <CountBadge
            count={subtreeHeld ?? Math.max(body.notable.length, total ?? 0)}
            noun="artifact"
          />
          {/* Nothing is a subdomain of the root, so the entry screen counts
              the top-level domains by that name. */}
          <CountBadge count={body.subdomains.length} noun={root ? 'domain' : 'subdomain'} />
          {/* The marker states that the listing below is a truncated view,
              which is a property of the response rather than of the domain.
              It therefore takes the marker tone: the same filled chip as the
              counts beside it, in the metadata colour, carrying an accent dot.
              An accent fill or edge would read as a warning, and a trimmed
              listing is neither content nor error. */}
          {trimmed && <Badge tone="marker">listing trimmed</Badge>}
        </div>
      </div>
      {/* A domain with no description of its own is told it carries none. The
          root is told what it is instead: §4.5.5 fixes that the root has no
          description to carry, so reporting the absence states a defect where
          there is none. */}
      {body.description !== undefined && body.description !== '' ? (
        <p className="lead">{body.description}</p>
      ) : root ? (
        <p className="quiet lead">This is the top of the domain hierarchy, and every domain the registry holds sits below it.</p>
      ) : (
        <p className="quiet lead">This domain carries no description.</p>
      )}
      {body.keywords !== undefined && body.keywords.length > 0 && (
        <ul className="tag-list">
          {body.keywords.map((keyword) => (
            <li key={keyword} className="keyword">
              {keyword}
            </li>
          ))}
        </ul>
      )}

      {/* The two listings are labelled rather than titled. A heading at the
          h2 display size competes with the domain name above it and reads as
          a third peer section, so both carry the section-label role the
          design pass fixed for a quiet divider over a list.

          The compact treatments carry their own label, because at this count
          the label shares its row with the controls over the listing. */}
      {foldedOnly ? null : compact && body.subdomains.length > 0 ? (
        <SubdomainTiles subdomains={body.subdomains} parent={body.path} catalog={catalog.value} />
      ) : (
        <>
          <h2 className="label">Subdomains</h2>
          {body.subdomains.length === 0 ? (
            <EmptyState title="No subdomains">Domains nested under this one appear here.</EmptyState>
          ) : (
            <SubdomainGrid subdomains={body.subdomains} parent={body.path} catalog={catalog.value} />
          )}
        </>
      )}

      {foldedOnly ? (
        // The listing is gone, and a rendering note the response carried is
        // not: it reports what the fold left out, which is the one thing this
        // screen would otherwise fail to state.
        tailList
      ) : compact && direct.length > 0 ? (
        // The table filters the rows the response carried, so it is told
        // whether those rows are the whole domain: a filter over a trimmed
        // listing continues into the scoped search rather than reporting an
        // artifact the listing withheld as absent. It is handed the
        // continuation row as well, because it is what knows whether the rows
        // drawn under it are still the ones that row counts.
        <ArtifactTable
          artifacts={direct}
          scope={body.path}
          trimmed={trimmed}
          withheld={total === null ? null : total - direct.length}
          tail={tailList}
        />
      ) : (
        <>
          <h2 className="label">Artifacts in this domain</h2>
          {direct.length === 0 ? (
            <>
              <EmptyState title="No artifacts here">
                Artifacts published directly to this domain appear here.
              </EmptyState>
              {tailList}
            </>
          ) : (
            <ul className="artifact-list">
              {direct.map((artifact) => (
                <ArtifactRow key={artifact.id} artifact={artifact} />
              ))}
              {tail}
            </ul>
          )}
        </>
      )}

      {/* A §4.5.5 lifted entry is not a child of this domain, so the group is
          drawn as its own dashed container rather than as a second section
          styled like the direct listing above it. The container is what says
          the rows belong to somewhere else; a bare label over an identical
          bordered list leaves the distinction to the reader's memory of the
          heading. The caption sits on the label's own line, because the
          container has already made the point a full sentence was carrying. */}
      {folded.length > 0 && (
        <section className="folded">
          <div className="folded-head">
            <h3 className="label">Lifted from sparse subdomains</h3>
            <span className="quiet">Not direct children</span>
          </div>
          <ul className="artifact-list">
            {folded.map((artifact) => (
              <ArtifactRow key={artifact.id} artifact={artifact} />
            ))}
          </ul>
        </section>
      )}
    </section>
  );
}

/** CountBadge states one of the two figures the domain response reports, as a
 * marker beside the domain name rather than as a sentence under it. The count
 * is set in caps for the same reason a type is (`components/primitives.tsx`,
 * `TypeBadge`), in the text rather than in the stylesheet.
 *
 * A zero draws nothing. The listing below the header already states an empty
 * one in prose, so a "0 ARTIFACTS" marker beside the title repeats it in the
 * position the page reserves for what the domain holds. */
function CountBadge({ count, noun }: { count: number; noun: string }) {
  if (count === 0) {
    return null;
  }
  const label = count === 1 ? noun : `${noun}s`;
  return <Badge tone="soft">{`${String(count)} ${label}`.toUpperCase()}</Badge>;
}

/** TrimmedListing is how a reader continues past the returned edge. The
 * response reports what it returned and no total, so the total is the count
 * the catalog read established. A total the read did not establish leaves the
 * line stating what is on the page, because the line reports what the reads
 * returned rather than a figure derived from neither.
 *
 * The continuation is a scoped search rather than a deeper read of this
 * domain. `load_domain` takes the subtree depth as its only argument, and the
 * notable list is capped independently of it, so raising the depth returns no
 * artifact the reader does not already hold and can return fewer once the
 * §4.5.5 budget loop pops entries off the list.
 *
 * The control names that handoff rather than promising the withheld artifacts.
 * The search surface opens at its own cap and cannot serve a `top_k` above the
 * §5 ceiling, so a domain holding more than the ceiling has no listing that
 * carries the rest, and a control labelled as one that loads them delivers
 * neither the rest nor a single artifact past what the reader already holds. */
function TrimmedListing({
  scope,
  shown,
  total,
  note,
}: {
  scope: string;
  shown: number;
  total: number | null;
  note: string;
}) {
  return (
    <li className="listing-tail" role="status" data-testid="listing-continuation">
      {/* The dot marks the row as the listing's edge. It carries no text of
          its own, so it is hidden from the reader who is read the row. */}
      <span className="listing-tail-mark" aria-hidden="true" />
      <div className="listing-tail-body">
        <p className="listing-tail-line">
          {total === null ? `${String(shown)} artifacts shown.` : `${String(shown)} of ${String(total)} artifacts shown.`}{' '}
          {note}
        </p>
        <a className="button" data-testid="listing-continue" href={scopedSearchHref(scope)}>
          {scope === '' ? 'Search the catalog' : 'Search this domain'}
        </a>
      </div>
    </li>
  );
}

/** scopedSearchHref addresses the search this domain's listing continues
 * into. The registry root carries no scope filter, because the empty path
 * bounds nothing. */
function scopedSearchHref(scope: string): string {
  return searchHref(scope === '' ? '' : `scope:${scope}`);
}

/** SubdomainGrid lays the immediate children out as a card grid: the name
 * with a chevron pointing at the domain it opens, the description, and what
 * the response reports below the child. Each card is one drill-down step, so
 * the grandchildren the request returned are counted here and drawn on the
 * page they belong to.
 *
 * The line carries how many artifacts stand under the child and how many
 * subdomains the response reported below it. A load_domain descriptor
 * (`pkg/registry/server/server.go`, `DomainDescriptor`) carries the nested
 * subtree and no artifact count, so the artifact figure comes from the §4.5.2
 * catalog read the browser already issues for the page rather than from a
 * scoped search behind every card, which is the read the compact tile counts
 * from as well.
 *
 * A catalog read that failed arrives as a null and leaves the card stating
 * what the response reported below the child alone. */
function SubdomainGrid({
  subdomains,
  parent,
  catalog,
}: {
  subdomains: DomainDescriptor[];
  parent: string;
  catalog: string[] | null;
}) {
  const counts =
    catalog === null
      ? null
      : artifactCounts(
          catalog,
          subdomains.map((child) => child.path),
          parent,
        );
  return (
    <ul className="subdomain-grid" aria-label="Subdomains">
      {subdomains.map((child) => (
        <li key={child.path} className="subdomain">
          {/* The card is one target. The name carries the overlay that makes
              the whole card follow the link (`index.css`, `.stretched-link`),
              so the description and the counts are aimable too. */}
          <a className="subdomain-name mono stretched-link" href={domainHref(child.path)}>
            <span>
              <PathLabel path={domainLabel(child.path, parent)} />
            </span>
            <Chevron />
          </a>
          {child.description !== undefined && child.description !== '' ? (
            <p className="subdomain-description">{child.description}</p>
          ) : (
            <p className="subdomain-description quiet absent-description">No description.</p>
          )}
          <SubdomainCounts
            subdomains={child.subdomains ?? []}
            artifacts={counts === null ? null : (counts.get(child.path) ?? 0)}
          />
        </li>
      ))}
    </ul>
  );
}

/** SubdomainCounts states what stands under a child on the card treatment: the
 * artifact count the catalog read established, then the subdomain count the
 * response reported. The compact tile states the same two figures. A card that
 * establishes neither figure carries no line at all. */
function SubdomainCounts({
  subdomains,
  artifacts,
}: {
  subdomains: DomainDescriptor[];
  artifacts: number | null;
}) {
  const nested = subdomainCountLabel(subdomains.length);
  if (artifacts === null && nested === null) {
    return null;
  }
  return (
    <div className="subdomain-counts mono quiet">
      {artifacts !== null && <span>{artifactCountLabel(artifacts)}</span>}
      {nested !== null && <span>{nested}</span>}
    </div>
  );
}
