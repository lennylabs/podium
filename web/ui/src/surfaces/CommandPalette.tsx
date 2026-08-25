// The command palette. It opens on ⌘K from anywhere in the shell and searches
// artifacts alone, because domain navigation is the sidebar tree's job. The
// query carries the inline filter syntax the search surface exposes as pills,
// so the same filter set §13.10 fixes reaches the same endpoint from here.

import { useEffect, useRef, useState } from 'react';

import type { KeyboardEvent } from 'react';

import { EmptyState, ErrorState, Loading } from '../components/primitives';
import type { ArtifactDescriptor, SearchResponse } from '../api';
import { searchArtifacts } from '../api';
import { parseQueryLine } from '../query';
import { artifactHref, searchHref } from '../route';
import type { Async } from '../useAsync';
import { useAsync } from '../useAsync';

/** paletteCap is how many rows the panel lists. The match count comes back
 * before the cap truncates the list, so the heading states both. */
const paletteCap = 8;

/** recentCap is how many queries the just-opened panel lists. */
const recentCap = 5;

export function CommandPalette({ open, onClose }: { open: boolean; onClose: () => void }) {
  const [line, setLine] = useState('');
  const [index, setIndex] = useState(0);
  const [recents, setRecents] = useState<string[]>([]);
  const input = useRef<HTMLInputElement>(null);

  const typed = line.trim();
  const results = useAsync<SearchResponse>(
    async () =>
      typed === '' ? { total_matched: 0, results: [] } : searchArtifacts(parseQueryLine(typed), paletteCap),
    [typed],
  );
  const rows = results.value?.results ?? [];

  useEffect(() => {
    if (open) {
      input.current?.focus();
    }
  }, [open]);

  if (!open) {
    return null;
  }

  const remember = (query: string) => {
    if (query === '') {
      return;
    }
    setRecents((held) => [query, ...held.filter((prior) => prior !== query)].slice(0, recentCap));
  };

  const openRow = (id: string) => {
    remember(typed);
    window.location.hash = artifactHref(id);
    onClose();
  };

  const openSearch = () => {
    remember(typed);
    // The whole typed line travels, filters and all. The search surface runs
    // the same parse over it, so the request it issues and the pills it
    // renders are the ones this panel typed rather than the line read back as
    // free text.
    window.location.hash = searchHref(typed);
    onClose();
  };

  const onKeyDown = (event: KeyboardEvent) => {
    switch (event.key) {
      case 'Escape':
        onClose();
        return;
      case 'ArrowDown':
        event.preventDefault();
        setIndex((at) => (rows.length === 0 ? 0 : (at + 1) % rows.length));
        return;
      case 'ArrowUp':
        event.preventDefault();
        setIndex((at) => (rows.length === 0 ? 0 : (at + rows.length - 1) % rows.length));
        return;
      case 'Enter':
        // ⌘⏎ hands the same query to the search surface, which is the one
        // place the whole result set is listed.
        if (event.metaKey || event.ctrlKey) {
          openSearch();
          return;
        }
        if (rows.length > 0) {
          openRow(rows[Math.min(index, rows.length - 1)].id);
        }
        return;
      default:
    }
  };

  return (
    <div
      className="palette-scrim"
      role="presentation"
      onClick={(event) => {
        if (event.target === event.currentTarget) {
          onClose();
        }
      }}
    >
      <div className="palette" role="dialog" aria-label="Command palette" data-testid="palette" onKeyDown={onKeyDown}>
        <input
          ref={input}
          className="palette-input"
          type="search"
          aria-label="Search artifacts"
          placeholder="Search artifacts"
          value={line}
          onChange={(event) => {
            setLine(event.target.value);
            setIndex(0);
          }}
        />
        {typed === '' ? (
          <PaletteHints
            recents={recents}
            onPick={(query) => {
              setLine(query);
            }}
          />
        ) : (
          <PaletteResults
            results={results}
            rows={rows}
            index={index}
            typed={typed}
            onOpen={openRow}
            onSearch={openSearch}
          />
        )}
        <p className="palette-footer mono quiet">↑↓ navigate · ⏎ open · ⌘⏎ all results · esc close</p>
      </div>
    </div>
  );
}

/** PaletteHints is the just-opened panel: the queries this page has already
 * run, and the inline filter syntax, which is what teaches the query language
 * the search surface exposes as pills. */
function PaletteHints({ recents, onPick }: { recents: string[]; onPick: (query: string) => void }) {
  return (
    <div className="palette-hints">
      <p className="label">Recent queries</p>
      {recents.length === 0 ? (
        <EmptyState>No query has been run on this page yet.</EmptyState>
      ) : (
        <ul className="palette-recents">
          {recents.map((query) => (
            <li key={query}>
              <button
                type="button"
                onClick={() => {
                  onPick(query);
                }}
              >
                {query}
              </button>
            </li>
          ))}
        </ul>
      )}
      <p className="label">Filter syntax</p>
      <p className="mono quiet" data-testid="palette-syntax">
        type:skill · tag:review · scope:platform
      </p>
    </div>
  );
}

/** PaletteResults lists what the query matched. The heading states the listed
 * count against the match count the registry took before the cap truncated
 * the list, and a query that matched nothing says nothing about what a
 * different caller would have seen. */
function PaletteResults({
  results,
  rows,
  index,
  typed,
  onOpen,
  onSearch,
}: {
  results: Async<SearchResponse>;
  rows: ArtifactDescriptor[];
  index: number;
  typed: string;
  onOpen: (id: string) => void;
  onSearch: () => void;
}) {
  if (results.loading) {
    return <Loading label="Searching." />;
  }
  if (results.error !== null) {
    return <ErrorState error={results.error} onRetry={results.reload} />;
  }
  if (rows.length === 0) {
    return (
      <div className="palette-empty">
        <EmptyState>Nothing matched {typed}. Check the spelling, or drop a filter from the line.</EmptyState>
        <button type="button" onClick={onSearch}>
          Run it on the search surface
        </button>
      </div>
    );
  }
  return (
    <>
      <p className="label" data-testid="palette-heading">
        Artifacts · {rows.length} of {results.value?.total_matched ?? rows.length}
      </p>
      <ul className="palette-rows">
        {rows.map((row, at) => (
          <li key={row.id}>
            <button
              type="button"
              className={at === Math.min(index, rows.length - 1) ? 'palette-row palette-row-selected' : 'palette-row'}
              onClick={() => {
                onOpen(row.id);
              }}
            >
              <span className="mono palette-row-name">{leafOf(row.id)}</span>
              <span className="mono quiet palette-row-path">{row.id}</span>
              <span className="quiet">
                {row.type}
                {row.version === undefined || row.version === '' ? '' : ` · ${row.version}`}
              </span>
            </button>
          </li>
        ))}
      </ul>
    </>
  );
}

/** leafOf is the artifact's own name inside its domain path, which is what
 * the row states above the full path. */
function leafOf(id: string): string {
  const cut = id.lastIndexOf('/');
  return cut < 0 ? id : id.slice(cut + 1);
}
