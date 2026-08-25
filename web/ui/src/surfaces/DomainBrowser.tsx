// The domain browser: the entry point and the primary navigation over the
// §4.2 domain hierarchy. It renders the caller's position in that hierarchy
// at every level, and it states nothing about what a response does not carry.

import { useState } from 'react';

import { ArtifactTable, SubdomainTiles } from './DomainAtScale';
import { ArtifactRow } from '../components/ArtifactRow';
import { Badge, EmptyState, ErrorState, Loading } from '../components/primitives';
import type { DomainDescriptor } from '../api';
import { loadDomain, searchArtifacts } from '../api';
import { domainHref } from '../route';
import { useAsync, useErrorReport } from '../useAsync';

// The browser renders two levels of the returned tree at once and follows a
// link for the rest, which is the depth the tree reads at without turning the
// page into the whole hierarchy.
const renderedDepth = 2;

/** atScale is the child count past which the compact treatment takes over.
 * A card grid stops being a map of the domain somewhere around here, so the
 * subdomains become count tiles and the artifacts a sortable table. */
const atScale = 20;

export function DomainBrowser({ path, onError }: { path: string; onError: (err: unknown) => void }) {
  // The response is trimmed to fit the response budget, and the depth is the
  // one argument the read takes, so a reader who continues past the returned
  // edge re-reads the same domain deeper.
  const [depth, setDepth] = useState(renderedDepth);
  const domain = useAsync(() => loadDomain(path, depth), [path, depth]);
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
      <h1>{body.path === '' ? 'Registry root' : body.path}</h1>
      <div className="artifact-meta">
        <Badge tone="quiet">{body.subdomains.length} subdomains</Badge>
        <Badge tone="quiet">{body.notable.length} artifacts</Badge>
        {trimmed && <Badge tone="accent">listing trimmed</Badge>}
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

      <h2>Subdomains</h2>
      {body.subdomains.length === 0 && <EmptyState>This domain has no subdomains.</EmptyState>}
      {body.subdomains.length > 0 &&
        (compact ? (
          <SubdomainTiles subdomains={body.subdomains} />
        ) : (
          <SubdomainList subdomains={body.subdomains} depth={renderedDepth} />
        ))}

      <h2>Artifacts</h2>
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
          announced above it: the reader meets it where the returned edge is,
          and the control re-reads the same domain deeper. */}
      {trimmed && (
        <TrimmedListing
          scope={body.path}
          shown={direct.length}
          note={body.note ?? ''}
          onLoadMore={() => {
            setDepth((held) => held + 1);
          }}
        />
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

/** TrimmedListing is how a reader continues past the returned edge. The
 * response reports that it was trimmed and carries no total, so the total is
 * the match count an unfiltered search under this domain reports, which the
 * registry takes before it truncates its own result set. A count the search
 * did not return leaves the line stating what is on the page, because the
 * line reports what the reads returned rather than a figure derived from
 * neither. */
function TrimmedListing({
  scope,
  shown,
  note,
  onLoadMore,
}: {
  scope: string;
  shown: number;
  note: string;
  onLoadMore: () => void;
}) {
  const total = useAsync(() => searchArtifacts({ query: '', type: '', scope, tags: [] }, 1), [scope]);
  const matched = total.value?.total_matched ?? 0;
  return (
    <div className="listing-continuation" role="status" data-testid="listing-continuation">
      <p className="quiet">
        {matched > shown ? `${String(shown)} of ${String(matched)} artifacts shown.` : `${String(shown)} artifacts shown.`}{' '}
        {note}
      </p>
      <button type="button" onClick={onLoadMore}>
        Load the rest
      </button>
    </div>
  );
}

/** SubdomainList renders the returned subtree recursively down to the depth
 * the surface renders at once, and links past that edge rather than
 * flattening a tree the response did not return whole. */
function SubdomainList({ subdomains, depth }: { subdomains: DomainDescriptor[]; depth: number }) {
  return (
    <ul className="subdomain-list">
      {subdomains.map((child) => (
        <li key={child.path} className="subdomain">
          <a className="mono" href={domainHref(child.path)}>
            {child.name}
          </a>
          {child.description !== undefined && child.description !== '' ? (
            <p className="quiet">{child.description}</p>
          ) : (
            <p className="quiet">No description.</p>
          )}
          {child.subdomains !== undefined && child.subdomains.length > 0 && depth > 1 && (
            <SubdomainList subdomains={child.subdomains} depth={depth - 1} />
          )}
        </li>
      ))}
    </ul>
  );
}

function Breadcrumb({ path }: { path: string }) {
  const segments = path === '' ? [] : path.split('/');
  return (
    <nav className="breadcrumb" aria-label="Breadcrumb">
      <a href={domainHref('')}>root</a>
      {segments.map((segment, index) => (
        <a key={segment + String(index)} href={domainHref(segments.slice(0, index + 1).join('/'))}>
          {segment}
        </a>
      ))}
    </nav>
  );
}
