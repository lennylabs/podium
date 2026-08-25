// The domain browser's at-scale treatment. A domain that carries a few
// children reads as cards and a list; one that carries dozens does not, so
// past the threshold the subdomains become count tiles with their own filter
// and the artifacts become a sortable table. The data is the same
// load_domain response either way: this module changes only how much of it a
// screen can hold at once.

import { useState } from 'react';

import type { ArtifactDescriptor, DomainDescriptor } from '../api';
import { artifactHref, domainHref } from '../route';

/** tileCap is how many tiles the grid shows before the reader asks for the
 * rest, which keeps a domain with dozens of children to one screen. */
const tileCap = 12;

/** SubdomainTiles is the compact treatment: a filter over the names, a
 * grid-or-list toggle, and one tile per child carrying the number of children
 * the response reported under it. */
export function SubdomainTiles({ subdomains }: { subdomains: DomainDescriptor[] }) {
  const [filter, setFilter] = useState('');
  const [view, setView] = useState<'grid' | 'list'>('grid');
  const [all, setAll] = useState(false);

  const needle = filter.trim().toLowerCase();
  const matched = needle === '' ? subdomains : subdomains.filter((child) => child.name.toLowerCase().includes(needle));
  const shown = all ? matched : matched.slice(0, tileCap);

  return (
    <div className="subdomain-tiles">
      <div className="filter-row">
        <label className="field">
          <span className="label">Filter subdomains</span>
          <input
            type="search"
            value={filter}
            onChange={(event) => {
              setFilter(event.target.value);
            }}
          />
        </label>
        <div className="view-toggle" role="group" aria-label="Subdomain view">
          {(['grid', 'list'] as const).map((choice) => (
            <button
              key={choice}
              type="button"
              className={view === choice ? 'toggle toggle-open' : 'toggle'}
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
        {shown.map((child) => (
          <li key={child.path} className="tile">
            <a className="mono" href={domainHref(child.path)}>
              {child.name}
            </a>
            <span className="mono quiet">{(child.subdomains ?? []).length} below</span>
          </li>
        ))}
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

/** ArtifactTable is the at-scale artifact treatment: a type filter over the
 * returned set, a sortable column header, and the author's own picks under
 * their own heading. Each description is clipped to one line, because at this
 * count the table is a map rather than a reading surface. */
export function ArtifactTable({ artifacts }: { artifacts: ArtifactDescriptor[] }) {
  const [type, setType] = useState('');
  const [column, setColumn] = useState<ArtifactColumn>('id');

  const types = [...new Set(artifacts.map((artifact) => artifact.type))].sort();
  const matched = type === '' ? artifacts : artifacts.filter((artifact) => artifact.type === type);
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
      <div className="filter-row" role="group" aria-label="Type">
        {types.map((name) => (
          <button
            key={name}
            type="button"
            className={type === name ? 'pill pill-active' : 'pill'}
            onClick={() => {
              setType(type === name ? '' : name);
            }}
          >
            {name}
          </button>
        ))}
      </div>
      {curated.length > 0 && (
        <>
          <h3 className="label">Curated by the domain author</h3>
          <ArtifactRows rows={curated} column={column} onSort={setColumn} />
        </>
      )}
      {rest.length > 0 && (
        <>
          <h3 className="label">Everything else</h3>
          <ArtifactRows rows={rest} column={column} onSort={setColumn} />
        </>
      )}
    </div>
  );
}

function ArtifactRows({
  rows,
  column,
  onSort,
}: {
  rows: ArtifactDescriptor[];
  column: ArtifactColumn;
  onSort: (next: ArtifactColumn) => void;
}) {
  const headers: { key: ArtifactColumn; label: string }[] = [
    { key: 'id', label: 'Artifact' },
    { key: 'type', label: 'Type' },
    { key: 'version', label: 'Version' },
  ];
  return (
    <table className="data-table" aria-label="Artifacts">
      <thead>
        <tr>
          {headers.map((header) => (
            <th key={header.key}>
              <button
                type="button"
                aria-pressed={column === header.key}
                onClick={() => {
                  onSort(header.key);
                }}
              >
                {header.label}
              </button>
            </th>
          ))}
          <th>Description</th>
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
