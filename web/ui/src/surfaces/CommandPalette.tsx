// The command palette. It opens on ⌘K from anywhere in the shell and searches
// artifacts alone, because domain navigation is the sidebar tree's job. The
// query carries the inline filter syntax the search surface exposes as pills,
// so the same filter set §13.10 fixes reaches the same endpoint from here.

import { useEffect, useRef, useState } from 'react';

import type { KeyboardEvent, ReactNode } from 'react';

import { Badge, EmptyState, ErrorState, Loading } from '../components/primitives';
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
        {/* The field row. The magnifier names the row as the query field, and
            the match count sits at the row's right edge, which is the edge
            the type and version columns below it also hold. */}
        <div className="palette-field">
          <span className="palette-magnifier" aria-hidden="true">
            ⌕
          </span>
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
          {typed !== '' && !results.loading && results.error === null && rows.length > 0 && (
            <span className="mono quiet palette-count" data-testid="palette-count">
              {rows.length} of {results.value?.total_matched ?? rows.length}
            </span>
          )}
        </div>
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
        <PaletteFooter />
      </div>
    </div>
  );
}

/** KeyCap draws one keystroke as the key it names. The footer states four of
 * them, and a key rendered as running prose reads as part of the sentence
 * beside it rather than as the key to press. */
function KeyCap({ children }: { children: ReactNode }) {
  return <span className="mono key-cap">{children}</span>;
}

/** PaletteFooter is the keyboard legend. Escape sits at the right edge on its
 * own, because it leaves the panel rather than acting inside it. */
function PaletteFooter() {
  return (
    <p className="palette-footer quiet" data-testid="palette-footer">
      <KeyCap>↑</KeyCap>
      <KeyCap>↓</KeyCap>
      <span>navigate</span>
      <KeyCap>⏎</KeyCap>
      <span>open</span>
      <KeyCap>⌘⏎</KeyCap>
      <span>all results</span>
      <span className="spacer" />
      <KeyCap>esc</KeyCap>
      <span>close</span>
    </p>
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
              {/* The right-hand column, held at the panel's right edge the way
                  the listing rows hold theirs, so the reader scans one column
                  of types instead of reading to the end of each path. */}
              <span className="palette-row-aside" data-testid="palette-row-aside">
                <Badge>{row.type}</Badge>
                {row.version !== undefined && row.version !== '' && (
                  <span className="mono quiet palette-row-version">{row.version}</span>
                )}
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
