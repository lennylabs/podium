// The command palette. It opens on ⌘K from anywhere in the shell and searches
// artifacts alone, because domain navigation is the sidebar tree's job. The
// query carries the inline filter syntax the search surface exposes as pills,
// so the same filter set §13.10 fixes reaches the same endpoint from here.

import { useState } from 'react';

import type { KeyboardEvent, ReactNode } from 'react';

import { useDialogFocus } from '../components/focus';
import { EmptyState, ErrorState, Loading, Magnifier, TypeBadge, formatVersion } from '../components/primitives';
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

/** listboxID names the result list the query field owns, and optionID names
 * one row inside it. The field points at the highlighted row through
 * `aria-activedescendant`, so the ids are the panel's own contract rather
 * than anything derived from an artifact ID. */
const listboxID = 'palette-listbox';

/** filterExamples is one example per filter the search surface exposes as a
 * pill, drawn in the palette as the chips a reader can type. Each is a
 * separate chip because the three are three things to type rather than one
 * sentence about filtering. */
const filterExamples = ['type:skill', 'tag:review', 'scope:platform'];
const optionID = (at: number) => `palette-option-${at}`;

export function CommandPalette({ open, onClose }: { open: boolean; onClose: () => void }) {
  // The queries this page has already run outlive one opening of the panel,
  // so they are held out here where closing it cannot discard them.
  const [recents, setRecents] = useState<string[]>([]);
  const remember = (query: string) => {
    if (query === '') {
      return;
    }
    setRecents((held) => [query, ...held.filter((prior) => prior !== query)].slice(0, recentCap));
  };

  // The panel is mounted only while it is open, which is what discards the
  // query with it. A panel that stays mounted reopens on the line the reader
  // last typed with the caret at its end, so the next keystroke appends to a
  // query they had already finished with and the just-opened state, which is
  // where the recent queries and the filter syntax are, is never reached
  // again.
  if (!open) {
    return null;
  }
  return <PalettePanel onClose={onClose} recents={recents} onRun={remember} />;
}

/** PalettePanel is one opening of the panel: the query the reader types into
 * it, the row the arrows hold, and the results that line matched. */
function PalettePanel({
  onClose,
  recents,
  onRun,
}: {
  onClose: () => void;
  recents: string[];
  onRun: (query: string) => void;
}) {
  const [line, setLine] = useState('');
  const [index, setIndex] = useState(0);
  // The panel covers the shell, so it owns focus while it is open and hands
  // it back to whatever the reader was on when they pressed the shortcut.
  const dialog = useDialogFocus<HTMLDivElement>();

  const typed = line.trim();
  const results = useAsync<SearchResponse>(
    async () =>
      typed === '' ? { total_matched: 0, results: [] } : searchArtifacts(parseQueryLine(typed), paletteCap),
    [typed],
  );
  const rows = results.value?.results ?? [];
  // The result list is drawn only on the arm PaletteResults reaches with rows
  // in hand. `aria-expanded` and `aria-activedescendant` state that same arm,
  // because a field that points at an option no page holds is worse than one
  // that points at nothing.
  const listed = typed !== '' && !results.loading && results.error === null && rows.length > 0;
  const at = listed ? Math.min(index, rows.length - 1) : -1;

  const openRow = (id: string) => {
    onRun(typed);
    window.location.hash = artifactHref(id);
    onClose();
  };

  const openSearch = () => {
    onRun(typed);
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
        // Both arms navigate and close the panel, and closing it hands focus
        // back to the control it was opened from. That control is the header's
        // search trigger, and an uncancelled ⏎ activates whatever holds focus
        // when the browser applies the key's default action: the panel reopens
        // over the surface the reader just navigated to. The panel consumes
        // ⏎, so it cancels it.
        //
        // ⌘⏎ hands the same query to the search surface, which is the one
        // place the whole result set is listed.
        if (event.metaKey || event.ctrlKey) {
          event.preventDefault();
          openSearch();
          return;
        }
        if (rows.length > 0) {
          event.preventDefault();
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
      {/* aria-modal matches the focus trap below: while the palette is open,
          focus cannot leave it, so a reader is told the rest of the page is
          inert rather than being invited to walk into it. */}
      <div
        ref={dialog}
        className="palette"
        role="dialog"
        aria-modal="true"
        aria-label="Command palette"
        data-testid="palette"
        onKeyDown={onKeyDown}
      >
        {/* The field row. The magnifier names the row as the query field, and
            the match count sits at the row's right edge, which is the edge
            the type and version columns below it also hold. */}
        <div className="palette-field">
          <span className="palette-magnifier">
            <Magnifier size={16} />
          </span>
          <input
            className="palette-input"
            type="search"
            // The field and the result list are a combobox over a listbox:
            // the arrows move a highlight the field owns, and the field names
            // the highlighted row so a reader who cannot see it is told which
            // row Enter opens.
            role="combobox"
            aria-label="Search artifacts"
            aria-autocomplete="list"
            aria-controls={listboxID}
            aria-expanded={listed}
            aria-activedescendant={at < 0 ? undefined : optionID(at)}
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
        {/* The panel is full-bleed, so the header divider and the footer reach
            both of its edges and everything between them carries its own
            inset. */}
        <div className="palette-body">
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
        </div>
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
        <EmptyState scope="inline">No query has been run on this page yet.</EmptyState>
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
      <div className="palette-syntax" data-testid="palette-syntax">
        <span className="quiet">Filter inline:</span>
        {filterExamples.map((filter) => (
          <span key={filter} className="mono palette-syntax-chip">
            {filter}
          </span>
        ))}
      </div>
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
        <EmptyState scope="inline">Nothing matched {typed}. Check the spelling, or drop a filter from the line.</EmptyState>
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
      <ul className="palette-rows" id={listboxID} role="listbox" aria-label="Artifact results">
        {rows.map((row, at) => (
          // The listbox owns its options directly, so the list item carries no
          // role of its own and the row it wraps is the option.
          <li key={row.id} role="presentation">
            <button
              type="button"
              role="option"
              id={optionID(at)}
              aria-selected={at === Math.min(index, rows.length - 1)}
              // The query field keeps focus while the arrows move the
              // highlight, so no row is a Tab stop.
              tabIndex={-1}
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
                <TypeBadge type={row.type} />
                {row.version !== undefined && row.version !== '' && (
                  <span className="mono quiet palette-row-version">{formatVersion(row.version)}</span>
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
