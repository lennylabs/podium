// The command palette. It opens on ⌘K from anywhere in the shell and searches
// artifacts alone, because domain navigation is the sidebar tree's job. The
// query carries the inline filter syntax the search surface exposes as pills,
// so the same filter set §13.10 fixes reaches the same endpoint from here.

import { useRef, useState } from 'react';

import type { KeyboardEvent, ReactNode, RefObject } from 'react';

import { useDialogFocus } from '../components/focus';
import { useScrollLock } from '../components/scrolllock';
import { EmptyState, ErrorState, Loading, Magnifier, TypeBadge, formatVersion } from '../components/primitives';
import type { ArtifactDescriptor, SearchResponse } from '../api';
import { catalogArtifactIDs, searchArtifacts } from '../api';
import { correctQueryLine } from './correction';
import { hasFilters, parseQueryLine } from '../query';
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

export function CommandPalette({
  open,
  onClose,
  trigger,
  content,
}: {
  open: boolean;
  onClose: () => void;
  trigger: RefObject<HTMLElement | null>;
  content: RefObject<HTMLElement | null>;
}) {
  // The queries this panel has acted on outlive one opening of it, so they are
  // held out here where closing it cannot discard them. A query joins the list
  // when it opens a result or reaches the search surface, which are the two
  // ways the reader finishes with one. The panel issues a read on every
  // keystroke, so a list of every query a read went out for would hold each
  // prefix of the line as its own entry, and so would the search surface,
  // which rewrites the route on every keystroke too.
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
  return <PalettePanel onClose={onClose} recents={recents} onRun={remember} trigger={trigger} content={content} />;
}

/** PalettePanel is one opening of the panel: the query the reader types into
 * it, the row the arrows hold, and the results that line matched. */
function PalettePanel({
  onClose,
  recents,
  onRun,
  trigger,
  content,
}: {
  onClose: () => void;
  recents: string[];
  onRun: (query: string) => void;
  trigger: RefObject<HTMLElement | null>;
  content: RefObject<HTMLElement | null>;
}) {
  const [line, setLine] = useState('');
  const [index, setIndex] = useState(0);
  // Opening a result replaces the surface under the panel, and the reader
  // resumes on that surface rather than on the header they opened the panel
  // from, so a panel that navigated hands focus to the main landmark.
  const navigated = useRef(false);
  // The panel covers the shell, so it owns focus while it is open and hands
  // it back to whatever the reader was on when they pressed the shortcut. The
  // accelerator opens it from surfaces where nothing holds focus, and focus
  // left on the document there restarts the next Tab at the top of the page,
  // so the header's own trigger stands in for the control there was none of.
  const dialog = useDialogFocus<HTMLDivElement>(true, undefined, (opener) =>
    navigated.current ? content.current : (opener ?? trigger.current),
  );
  // The panel is pinned to the viewport, so the surface it covers is held
  // still under it: a wheel over the scrim would otherwise slide the page
  // around behind a panel that does not move with it.
  useScrollLock();

  const typed = line.trim();
  const results = useAsync<SearchResponse>(
    async () =>
      typed === '' ? { total_matched: 0, results: [] } : searchArtifacts(parseQueryLine(typed), paletteCap),
    [typed],
  );
  const rows = results.value?.results ?? [];
  // A query that matched nothing is offered the nearest spelling the catalog
  // holds, because prose telling a reader to check the spelling leaves them
  // retyping a line they already believe is right. No endpoint answers "did
  // you mean", so the vocabulary is the §4.5.2 catalog of canonical IDs and
  // the correction is computed here. The read is issued only on the arm that
  // has nothing to list, and the arm holds across keystrokes, so a query that
  // matched costs nothing and a reader still typing a miss costs one read. A
  // catalog the caller cannot read leaves the arm as it was: the correction is
  // an offer, and a failure to make one is not a failure to report.
  const noMatch = typed !== '' && !results.loading && results.error === null && rows.length === 0;
  const catalog = useAsync<string[]>(async () => (noMatch ? catalogArtifactIDs('') : []), [noMatch]);
  const correction = noMatch ? correctQueryLine(typed, catalog.value ?? []) : null;
  // The result list is drawn only on the arm PaletteResults reaches with rows
  // in hand. `aria-expanded` and `aria-activedescendant` state the arm that
  // holds a list, because a field that points at an option no page holds is
  // worse than one that points at nothing.
  const listed = typed !== '' && !results.loading && results.error === null && rows.length > 0;
  // The just-opened panel lists the queries this panel has acted on, and the arrows
  // move over them the way they move over results. The two lists never appear
  // together, so one highlight walks whichever list is drawn and the footer
  // names the keys that act on it.
  const listedRecents = typed === '' && recents.length > 0;
  const options = listedRecents ? recents.length : listed ? rows.length : 0;
  const at = options === 0 ? -1 : Math.min(index, options - 1);
  // The no-match arm and the refused arm below both offer one action, the
  // handoff to the search surface, and the footer advertises ⏎ the whole time
  // the panel is open. So ⏎ runs that one action wherever there is no row to
  // open, rather than being a key the legend names and nothing answers. The
  // refused arm needs it as much as the no-match one does: the query still
  // reaches the search surface, which issues the read again on a surface that
  // can list the whole result set.
  const offersSearch = typed !== '' && !results.loading && !listed;
  // Which list the panel is drawing, which is what the footer names its keys
  // against. A read still in flight keeps the legend the settled list will
  // carry rather than flickering through the bare arm on every keystroke.
  const mode: PaletteMode =
    typed === '' ? (listedRecents ? 'recents' : 'bare') : offersSearch ? 'search' : 'rows';
  // What the panel drew is also stated for a reader who cannot see it. The
  // count reaches them as the read settles, and the no-match arm, which
  // replaces the whole listbox with a sentence, reaches them at all:
  // `aria-activedescendant` names a row while there is one to name and says
  // nothing at the moment the list empties.
  const announcement = resultAnnouncement(typed, results, rows);

  const openRow = (id: string) => {
    onRun(typed);
    navigated.current = true;
    window.location.hash = artifactHref(id);
    onClose();
  };

  // Running a recent query puts it back in the field, which is what issues
  // the read again. The highlight returns to the top of the list the read
  // produces.
  const runRecent = (query: string) => {
    setLine(query);
    setIndex(0);
  };

  const openSearch = () => {
    onRun(typed);
    navigated.current = true;
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
        setIndex((at) => (options === 0 ? 0 : (at + 1) % options));
        return;
      case 'ArrowUp':
        event.preventDefault();
        setIndex((at) => (options === 0 ? 0 : (at + options - 1) % options));
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
        //
        // An empty line has no query to hand over and no row to open, so both
        // arms below are refused there rather than carrying nothing to the
        // search surface, which lands on its browse listing and reads as the
        // panel having discarded what the reader asked for. ⏎ runs the
        // highlighted recent query instead, which is what the footer names.
        if (typed === '') {
          if (listedRecents) {
            event.preventDefault();
            runRecent(recents[at]);
          }
          return;
        }
        if (event.metaKey || event.ctrlKey) {
          event.preventDefault();
          openSearch();
          return;
        }
        if (listed) {
          event.preventDefault();
          openRow(rows[Math.min(index, rows.length - 1)].id);
          return;
        }
        if (offersSearch) {
          event.preventDefault();
          openSearch();
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
            aria-expanded={listed || listedRecents}
            aria-activedescendant={at < 0 ? undefined : optionID(at)}
            placeholder="Search artifacts"
            value={line}
            onChange={(event) => {
              setLine(event.target.value);
              setIndex(0);
            }}
          />
          {/* The count is drawn on every settled read, a match and a no match
              alike. A count that appears only when something matched leaves
              the arm that most needs it, the one with an empty panel under
              the field, stating nothing about how many rows it looked at. */}
          {typed !== '' && !results.loading && results.error === null && (
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
            <PaletteHints recents={recents} at={at} onPick={runRecent} />
          ) : (
            <PaletteResults
              results={results}
              rows={rows}
              index={index}
              typed={typed}
              correction={correction}
              onOpen={openRow}
              onSearch={openSearch}
              onCorrect={runRecent}
            />
          )}
        </div>
        {/* The region is rendered on every state of the panel, empty until a
            read settles. A region mounted at the moment its text arrives is
            not in the accessibility tree when the change happens, and the
            announcement is dropped. */}
        <p className="assistive-only" role="status" aria-live="polite" data-testid="palette-announcement">
          {announcement}
        </p>
        <PaletteFooter mode={mode} />
      </div>
    </div>
  );
}

/** resultAnnouncement is what the panel's result state says to a reader who
 * cannot see it. It is empty while nothing has been typed and while a read is
 * in flight, so a settled result is announced once rather than a partial one
 * being announced on the way to it. The no-match line is worded for the region
 * rather than repeated from the panel, the way the copy control's
 * announcement is: the drawn sentence carries advice a reader acts on with
 * the panel in front of them, and the region states the outcome. A refused
 * read carries its own alert from ErrorState and is not restated here. */
function resultAnnouncement(typed: string, results: Async<SearchResponse>, rows: ArtifactDescriptor[]): string {
  if (typed === '' || results.loading || results.error !== null) {
    return '';
  }
  if (rows.length === 0) {
    return `No artifact matched “${typed}”.`;
  }
  const matched = results.value?.total_matched ?? rows.length;
  return `${rows.length} of ${matched} artifact${matched === 1 ? '' : 's'} matched.`;
}

/** KeyCap draws one keystroke as the key it names. The footer states four of
 * them, and a key rendered as running prose reads as part of the sentence
 * beside it rather than as the key to press. */
function KeyCap({ children }: { children: ReactNode }) {
  return <span className="mono key-cap">{children}</span>;
}

/** PaletteMode is which list the panel is drawing, which is what fixes the
 * keys the footer names and the list the arrows walk. */
type PaletteMode = 'rows' | 'recents' | 'bare' | 'search';

/** PaletteFooter is the keyboard legend. Escape sits at the right edge on its
 * own, because it leaves the panel rather than acting inside it. On an arm
 * with no row to open, the legend states the keys that act, because naming a
 * key over a panel nothing answers it on advertises an action the reader
 * cannot take. The just-opened arm names ⏎ as run, because it runs the
 * highlighted recent query, and drops ⌘⏎, because an empty line carries no
 * query to the search surface. */
function PaletteFooter({ mode }: { mode: PaletteMode }) {
  return (
    <p className="palette-footer quiet" data-testid="palette-footer">
      {mode === 'rows' || mode === 'recents' ? (
        <>
          <KeyCap>↑</KeyCap>
          <KeyCap>↓</KeyCap>
          <span>navigate</span>
          <KeyCap>⏎</KeyCap>
          <span>{mode === 'rows' ? 'open' : 'run'}</span>
        </>
      ) : null}
      {mode === 'rows' && (
        <>
          <KeyCap>⌘⏎</KeyCap>
          <span>all results</span>
        </>
      )}
      {mode === 'search' && (
        <>
          <KeyCap>⏎</KeyCap>
          <span>search anyway</span>
        </>
      )}
      <span className="spacer" />
      <KeyCap>esc</KeyCap>
      <span>close</span>
    </p>
  );
}

/** PaletteHints is the just-opened panel: the queries this panel has acted on,
 * and the inline filter syntax, which is what teaches the query language
 * the search surface exposes as pills. The recent queries are the panel's
 * listbox on this arm, so the arrows walk them and ⏎ runs the highlighted one,
 * which is what the footer names here. */
function PaletteHints({
  recents,
  at,
  onPick,
}: {
  recents: string[];
  at: number;
  onPick: (query: string) => void;
}) {
  return (
    <>
      <p className="label">Recent queries</p>
      {recents.length === 0 ? (
        // The copy names what fills the list. It once stated that no query had
        // been run on this page, which the reader can contradict by looking
        // past the panel at the results of one they ran on the search surface
        // behind it.
        <EmptyState scope="inline">
          A query is listed here once it opens a result or reaches the search surface.
        </EmptyState>
      ) : (
        // The list carries the result list's own classes, because the arrows
        // move one highlight and a recent query drawn as an inset button
        // would read as a different kind of row from the ones the same keys
        // walk over a query's matches.
        <ul className="palette-rows" id={listboxID} role="listbox" aria-label="Recent queries">
          {recents.map((query, row) => (
            <li key={query} role="presentation">
              <button
                type="button"
                role="option"
                id={optionID(row)}
                aria-selected={row === at}
                // The query field keeps focus while the arrows move the
                // highlight, so no row is a Tab stop.
                tabIndex={-1}
                className={row === at ? 'palette-row palette-row-selected' : 'palette-row'}
                onClick={() => {
                  onPick(query);
                }}
              >
                <span className="quiet palette-recent-mark" aria-hidden="true">
                  ↩
                </span>
                <span>{query}</span>
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
    </>
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
  correction,
  onOpen,
  onSearch,
  onCorrect,
}: {
  results: Async<SearchResponse>;
  rows: ArtifactDescriptor[];
  index: number;
  typed: string;
  correction: string | null;
  onOpen: (id: string) => void;
  onSearch: () => void;
  onCorrect: (query: string) => void;
}) {
  if (results.loading) {
    return <Loading label="Searching." />;
  }
  if (results.error !== null) {
    // The refused read leaves the panel with no row to open, so it carries
    // the same handoff the no-match arm carries. Without it the panel states
    // a failure and offers only a retry of the read that just failed, while
    // the footer below still advertises ⏎.
    return (
      <ErrorState error={results.error} onRetry={results.reload}>
        <button type="button" onClick={onSearch}>
          Run it on the search surface
        </button>
      </ErrorState>
    );
  }
  if (rows.length === 0) {
    // The arm states the outcome as a heading, then what the query ran
    // against, then the one action left. Drawn as a single quiet sentence it
    // collapsed the panel to a line under the field and named neither how
    // many rows the query reached nor what search looks at, so a reader had
    // no way to tell a misspelling from a term the registry does not index.
    // The query is quoted so a reader can see where it ends, and the advice to
    // drop a filter is drawn only when the line carries one to drop.
    const filtered = hasFilters(parseQueryLine(typed));
    return (
      <div className="palette-empty" data-testid="palette-empty">
        <p className="palette-empty-heading">Nothing matched “{typed}”</p>
        <p className="quiet palette-empty-body">
          {filtered
            ? 'Try fewer words, or drop a filter from the line.'
            : 'Try fewer words, or check the spelling.'}{' '}
          Search covers artifact names, descriptions, and tags.
        </p>
        {/* The correction is the one control that answers a typo, so it sits
            above the handoff, which re-runs the query that just failed. It is
            drawn only when the catalog holds a spelling near the typed one. */}
        {correction !== null && (
          <p className="palette-correction" data-testid="palette-correction">
            <span className="quiet">Did you mean</span>{' '}
            <button
              type="button"
              className="mono palette-correction-chip"
              onClick={() => {
                onCorrect(correction);
              }}
            >
              {correction}
            </button>
          </p>
        )}
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
