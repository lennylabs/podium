// The domain browser: the entry point and the primary navigation over the
// §4.2 domain hierarchy. It renders the caller's position in that hierarchy
// at every level, and it states nothing about what a response does not carry.

import { ArtifactTable, SubdomainTiles } from './DomainAtScale';
import { ArtifactRow } from '../components/ArtifactRow';
import { Breadcrumb } from '../components/Breadcrumb';
import { Badge, EmptyState, ErrorState, Loading } from '../components/primitives';
import type { DomainDescriptor } from '../api';
import { loadDomain, searchArtifacts } from '../api';
import { domainLabel } from '../domain';
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
  useErrorReport(domain.error, onError);

  if (domain.loading) {
    return <Loading label="Loading the domain." />;
  }
  if (domain.error !== null) {
    return <ErrorState error={domain.error} onRetry={domain.reload} />;
  }
  const body = domain.value;
  if (body === null) {
    return null;
  }
  const direct = body.notable.filter((a) => a.folded_from === undefined || a.folded_from === '');
  const folded = body.notable.filter((a) => a.folded_from !== undefined && a.folded_from !== '');
  const trimmed = body.note !== undefined && body.note !== '';
  const compact = body.subdomains.length > atScale;

  return (
    <section className="surface" aria-label="Domain browser">
      <Breadcrumb path={body.path} />
      <div className="domain-head">
        <h1>{leafName(body.path)}</h1>
        <div className="domain-counts">
          <CountBadge count={body.notable.length} noun="artifact" />
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
          design pass fixed for a quiet divider over a list. */}
      <h2 className="label">Subdomains</h2>
      {body.subdomains.length === 0 && <EmptyState>This domain has no subdomains.</EmptyState>}
      {body.subdomains.length > 0 &&
        (compact ? (
          <SubdomainTiles subdomains={body.subdomains} parent={body.path} />
        ) : (
          <SubdomainGrid subdomains={body.subdomains} parent={body.path} />
        ))}

      <h2 className="label">Artifacts in this domain</h2>
      {direct.length === 0 && <EmptyState>This domain lists no artifacts.</EmptyState>}
      {direct.length > 0 &&
        (compact ? (
          <ArtifactTable artifacts={direct} />
        ) : (
          <ul className="artifact-list">
            {direct.map((artifact) => (
              <ArtifactRow key={artifact.id} artifact={artifact} />
            ))}
          </ul>
        ))}
      {/* The trimmed listing is continued at the end of the list rather than
          announced above it: the reader meets it where the returned edge is. */}
      {trimmed && <TrimmedListing scope={body.path} shown={direct.length} note={body.note ?? ''} />}

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
 * response reports that it was trimmed and carries no total, so the total is
 * the match count an unfiltered search under this domain reports, which the
 * registry takes before it truncates its own result set. A count the search
 * did not return leaves the line stating what is on the page, because the
 * line reports what the reads returned rather than a figure derived from
 * neither.
 *
 * The continuation is that same scoped search rather than a deeper read of
 * this domain. `load_domain` takes the subtree depth as its only argument,
 * and the notable list is capped independently of it, so raising the depth
 * returns no artifact the reader does not already hold and can return fewer
 * once the §4.5.5 budget loop pops entries off the list. */
function TrimmedListing({ scope, shown, note }: { scope: string; shown: number; note: string }) {
  const total = useAsync(() => searchArtifacts({ query: '', type: '', scope, tags: [] }, 1), [scope]);
  const matched = total.value?.total_matched ?? 0;
  return (
    <div className="listing-continuation" role="status" data-testid="listing-continuation">
      <p className="quiet">
        {matched > shown ? `${String(shown)} of ${String(matched)} artifacts shown.` : `${String(shown)} artifacts shown.`}{' '}
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
 * The line carries the subdomain count alone. A load_domain descriptor
 * (`pkg/registry/server/server.go`, `DomainDescriptor`) carries the nested
 * subtree and no artifact count, and taking one scoped search per card to
 * derive that count would put a request behind every tile on the page. */
function SubdomainGrid({ subdomains, parent }: { subdomains: DomainDescriptor[]; parent: string }) {
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
          <SubdomainCounts subdomains={child.subdomains ?? []} />
        </li>
      ))}
    </ul>
  );
}

/** SubdomainCounts states what the response reported below a child. An entry
 * with an empty subtree carries no count line, because a card that reads
 * "0 subdomains" claims a fact the descriptor omits at the deepest returned
 * level rather than one it reports. */
function SubdomainCounts({ subdomains }: { subdomains: DomainDescriptor[] }) {
  if (subdomains.length === 0) {
    return null;
  }
  return (
    <div className="subdomain-counts mono quiet">
      <span>
        {subdomains.length} {subdomains.length === 1 ? 'subdomain' : 'subdomains'}
      </span>
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

