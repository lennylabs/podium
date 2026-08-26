// The domain browser's at-scale treatment. A domain that carries a few
// children reads as cards and a list; one that carries dozens does not, so
// past the threshold the subdomains become count tiles with their own filter
// and the artifacts become a sortable table. The data is the same
// load_domain response either way: this module changes only how much of it a
// screen can hold at once.

import { useState } from 'react';

import type { ArtifactDescriptor, DomainDescriptor } from '../api';
import { domainLabel, subdomainCountLabel } from '../domain';
import { artifactHref, domainHref } from '../route';

/** tileCap is how many tiles the grid shows before the reader asks for the
 * rest, which keeps a domain with dozens of children to one screen. */
const tileCap = 12;

/** SubdomainTiles is the compact treatment: the section label carrying the
 * child count, a filter over the names and a grid-or-list toggle on the same
 * row, and one tile per child.
 *
 * The tile states what the response reported below the child. A load_domain
 * descriptor (`pkg/registry/server/server.go`, `DomainDescriptor`) carries the
 * nested subtree and no artifact count, so the tile counts subdomains, and a
 * child whose subtree came back empty carries no count line rather than
 * claiming a zero the descriptor omits at the deepest returned level. */
export function SubdomainTiles({ subdomains, parent }: { subdomains: DomainDescriptor[]; parent: string }) {
  const [filter, setFilter] = useState('');
  const [view, setView] = useState<'grid' | 'list'>('grid');
  const [all, setAll] = useState(false);

  const needle = filter.trim().toLowerCase();
  // The filter runs over the label the tile carries, so a reader who types
  // what is on screen matches the tile they can see.
  const matched =
    needle === ''
      ? subdomains
      : subdomains.filter((child) => domainLabel(child.path, parent).toLowerCase().includes(needle));
  const shown = all ? matched : matched.slice(0, tileCap);

  return (
    <div className="subdomain-tiles">
      <div className="section-head">
        <h2 className="label">Subdomains</h2>
        <span className="mono quiet section-count">{subdomains.length}</span>
        <input
          className="filter-field"
          type="search"
          aria-label="Filter subdomains"
          placeholder="Filter subdomains"
          value={filter}
          onChange={(event) => {
            setFilter(event.target.value);
          }}
        />
        <div className="segmented" role="group" aria-label="Subdomain view">
          {(['grid', 'list'] as const).map((choice) => (
            <button
              key={choice}
              type="button"
              className={view === choice ? 'segment segment-on' : 'segment'}
              aria-pressed={view === choice}
              onClick={() => {
                setView(choice);
              }}
            >
              {choice}
            </button>
          ))}
        </div>
      </div>
      <ul className={view === 'grid' ? 'tile-grid' : 'tile-list'} aria-label="Subdomains">
        {shown.map((child) => {
          const count = subdomainCountLabel((child.subdomains ?? []).length);
          return (
            <li key={child.path} className="tile">
              <a className="tile-name mono" href={domainHref(child.path)}>
                {domainLabel(child.path, parent)}
              </a>
              {count !== null && <span className="mono quiet tile-count">{count}</span>}
            </li>
          );
        })}
      </ul>
      {!all && matched.length > shown.length && (
        <button
          type="button"
          data-testid="show-all-subdomains"
          onClick={() => {
            setAll(true);
          }}
        >
          Show all {matched.length} subdomains
        </button>
      )}
    </div>
  );
}

/** ArtifactColumn is what the table sorts on. Every value is present on every
 * descriptor or rendered as absent, so a sort never reorders on a field half
 * the rows lack. */
type ArtifactColumn = 'id' | 'type' | 'version';

const sortOptions: { key: ArtifactColumn; label: string }[] = [
  { key: 'id', label: 'artifact' },
  { key: 'type', label: 'type' },
  { key: 'version', label: 'version' },
];

/** ArtifactTable is the at-scale artifact treatment: a filter over the domain's
 * own listing, a type chip per returned type beside an All chip, a sort
 * control, and the author's own picks in their own block above the rest. Each
 * description is clipped to one line, because at this count the table is a map
 * rather than a reading surface.
 *
 * The author's picks stand above the rest under every ordering. The sort
 * control chooses what orders the rows inside each block, so it names the
 * column it sorts on rather than the arrangement of the blocks. */
export function ArtifactTable({ artifacts }: { artifacts: ArtifactDescriptor[] }) {
  const [type, setType] = useState('');
  const [filter, setFilter] = useState('');
  const [column, setColumn] = useState<ArtifactColumn>('id');

  const types = [...new Set(artifacts.map((artifact) => artifact.type))].sort();
  const needle = filter.trim().toLowerCase();
  // The filter runs over the identifier the first column carries, for the
  // reason the subdomain filter runs over the tile's own label.
  const matched = artifacts.filter(
    (artifact) =>
      (type === '' || artifact.type === type) && (needle === '' || artifact.id.toLowerCase().includes(needle)),
  );
  const curated = sorted(
    matched.filter((artifact) => artifact.source === 'featured'),
    column,
  );
  const rest = sorted(
    matched.filter((artifact) => artifact.source !== 'featured'),
    column,
  );

  return (
    <div className="artifact-table">
      <div className="section-head">
        <h2 className="label">Artifacts</h2>
        <input
          className="filter-field"
          type="search"
          aria-label="Filter in this domain"
          placeholder="Filter in this domain"
          value={filter}
          onChange={(event) => {
            setFilter(event.target.value);
          }}
        />
        {/* The All chip is the unfiltered set stated as a chip of its own, so
            the row always carries the state it is in rather than leaving the
            reader to read it from the absence of an active chip. */}
        <div className="chip-row" role="group" aria-label="Type">
          <button
            type="button"
            className={type === '' ? 'pill pill-active' : 'pill'}
            aria-pressed={type === ''}
            onClick={() => {
              setType('');
            }}
          >
            All
          </button>
          {types.map((name) => (
            <button
              key={name}
              type="button"
              className={type === name ? 'pill pill-active' : 'pill'}
              aria-pressed={type === name}
              onClick={() => {
                setType(name);
              }}
            >
              {name}
            </button>
          ))}
        </div>
        <label className="sort-control">
          Sort:
          <select
            aria-label="Sort artifacts"
            value={column}
            onChange={(event) => {
              setColumn(event.target.value as ArtifactColumn);
            }}
          >
            {sortOptions.map((option) => (
              <option key={option.key} value={option.key}>
                {option.label}
              </option>
            ))}
          </select>
        </label>
      </div>
      {curated.length > 0 && (
        <div className="curated-block">
          <div className="curated-head">
            <span aria-hidden="true">★</span>
            <span className="label">Curated by the domain author</span>
            <span className="mono">{curated.length}</span>
          </div>
          <ArtifactRows rows={curated} />
        </div>
      )}
      {/* The rest carries no heading of its own. The picks above it are the
          block that is titled, and a second title over everything the domain
          returned names the listing the page is already about. */}
      {rest.length > 0 && <ArtifactRows rows={rest} />}
    </div>
  );
}

/** ArtifactRows draws one block of the table. The column labels are quiet
 * markers over the columns they name: the sort control above the table is
 * where an ordering is chosen, so a header carries no control of its own. */
function ArtifactRows({ rows }: { rows: ArtifactDescriptor[] }) {
  return (
    <table className="data-table" aria-label="Artifacts">
      <thead>
        <tr>
          <th className="column-label">Artifact</th>
          <th className="column-label">Type</th>
          <th className="column-label">Version</th>
          <th className="column-label">Tags</th>
          <th className="column-label">Description</th>
        </tr>
      </thead>
      <tbody>
        {rows.map((artifact) => (
          <tr key={artifact.id}>
            <td className="mono">
              <a href={artifactHref(artifact.id)}>{artifact.id}</a>
            </td>
            <td>{artifact.type}</td>
            <td className="mono quiet">{artifact.version ?? 'unversioned'}</td>
            <td>
              <span className="tag-list">
                {(artifact.tags ?? []).map((tag) => (
                  <span key={tag} className="tag">
                    {tag}
                  </span>
                ))}
              </span>
            </td>
            <td className="clipped quiet">{artifact.description ?? 'No description.'}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

/** sorted orders a row set by the chosen column. A descriptor omits a version
 * where it carries none, and an absent value sorts as the empty string rather
 * than dropping the row. */
function sorted(rows: ArtifactDescriptor[], column: ArtifactColumn): ArtifactDescriptor[] {
  return [...rows].sort((a, b) => valueOf(a, column).localeCompare(valueOf(b, column)));
}

function valueOf(artifact: ArtifactDescriptor, column: ArtifactColumn): string {
  switch (column) {
    case 'type':
      return artifact.type;
    case 'version':
      return artifact.version ?? '';
    case 'id':
      return artifact.id;
  }
}
