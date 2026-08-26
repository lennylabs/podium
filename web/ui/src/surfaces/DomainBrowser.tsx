// The domain browser: the entry point and the primary navigation over the
// §4.2 domain hierarchy. It renders the caller's position in that hierarchy
// at every level, and it states nothing about what a response does not carry.

import { ArtifactTable, SubdomainTiles } from './DomainAtScale';
import { ArtifactRow } from '../components/ArtifactRow';
import { Breadcrumb } from '../components/Breadcrumb';
import { Badge, EmptyState, ErrorPage, Loading } from '../components/primitives';
import type { DomainDescriptor } from '../api';
import { catalogArtifactIDs, loadDomain } from '../api';
import {
  artifactCountLabel,
  artifactCounts,
  directArtifactCount,
  domainLabel,
  subdomainCountLabel,
} from '../domain';
import { domainHref, searchHref } from '../route';
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

/** leafName is what the page is titled. The breadcrumb above the title already
 * carries the ancestry, so repeating the whole slash-separated path in the h1
 * states the reader's position twice and runs the title off the content column
 * on a deep domain. The registry root has no leaf and is named instead. */
function leafName(path: string): string {
  if (path === '') {
    return 'Registry root';
  }
  const segments = path.split('/');
  return segments[segments.length - 1];
}

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

  return (
    <section className="surface" aria-label="Domain browser">
      <Breadcrumb path={body.path} />
      <div className="domain-head">
        <h1>{leafName(body.path)}</h1>
        <div className="domain-counts">
          {/* The badge is the domain's own count rather than the listing's,
              so a trimmed listing is not presented as the whole domain. The
              two agree wherever nothing was trimmed, and the listing wins
              where it carries entries the catalog does not count as direct
              children, which is the folded group. */}
          <CountBadge count={Math.max(body.notable.length, total ?? 0)} noun="artifact" />
          <CountBadge count={body.subdomains.length} noun="subdomain" />
          {trimmed && <Badge tone="accent">listing trimmed</Badge>}
        </div>
      </div>
      {body.description !== undefined && body.description !== '' ? (
        <p className="lead">{body.description}</p>
      ) : (
        <p className="quiet lead">This domain carries no description.</p>
      )}
      {body.keywords !== undefined && body.keywords.length > 0 && (
        <ul className="tag-list">
          {body.keywords.map((keyword) => (
            <li key={keyword} className="tag">
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
      {compact && body.subdomains.length > 0 ? (
        <SubdomainTiles subdomains={body.subdomains} parent={body.path} catalog={catalog.value} />
      ) : (
        <>
          <h2 className="label">Subdomains</h2>
          {body.subdomains.length === 0 ? (
            <EmptyState>This domain has no subdomains.</EmptyState>
          ) : (
            <SubdomainGrid subdomains={body.subdomains} parent={body.path} catalog={catalog.value} />
          )}
        </>
      )}

      {compact && direct.length > 0 ? (
        <ArtifactTable artifacts={direct} />
      ) : (
        <>
          <h2 className="label">Artifacts in this domain</h2>
          {direct.length === 0 ? (
            <EmptyState>This domain lists no artifacts.</EmptyState>
          ) : (
            <ul className="artifact-list">
              {direct.map((artifact) => (
                <ArtifactRow key={artifact.id} artifact={artifact} />
              ))}
            </ul>
          )}
        </>
      )}
      {/* The trimmed listing is continued at the end of the list rather than
          announced above it: the reader meets it where the returned edge is. */}
      {trimmed && (
        <TrimmedListing scope={body.path} shown={direct.length} total={total} note={body.note ?? ''} />
      )}

      {folded.length > 0 && (
        <section className="folded">
          <h3 className="label">Lifted from sparse subdomains</h3>
          <p className="quiet">These artifacts are not direct children of this domain.</p>
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
  return <Badge tone="quiet">{`${String(count)} ${label}`.toUpperCase()}</Badge>;
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
 * §4.5.5 budget loop pops entries off the list. */
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
    <div className="listing-continuation" role="status" data-testid="listing-continuation">
      <p className="quiet">
        {total === null ? `${String(shown)} artifacts shown.` : `${String(shown)} of ${String(total)} artifacts shown.`}{' '}
        {note}
      </p>
      <a className="button" data-testid="listing-continue" href={scopedSearchHref(scope)}>
        Load the rest
      </a>
    </div>
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
          <a className="subdomain-name mono" href={domainHref(child.path)}>
            <span>{domainLabel(child.path, parent)}</span>
            <Chevron />
          </a>
          {child.description !== undefined && child.description !== '' ? (
            <p className="quiet">{child.description}</p>
          ) : (
            <p className="quiet">No description.</p>
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

function Chevron() {
  return (
    <svg className="chevron" viewBox="0 0 10 10" aria-hidden="true">
      <path d="M3 1.5L7 5l-4 3.5" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
    </svg>
  );
}

