// The domain browser's at-scale treatment. A domain that carries a few
// children reads as cards and a list; one that carries dozens does not, so
// past the threshold the subdomains become count tiles with their own filter
// and the artifacts become a sortable table. The data is the same
// load_domain response either way: this module changes only how much of it a
// screen can hold at once.

import type { ReactNode } from "react";
import { useState } from "react";

import type { ArtifactDescriptor, DomainDescriptor } from "../api";
import {
  EmptyState,
  Magnifier,
  TypeBadge,
  formatVersion,
} from "../components/primitives";
import {
  artifactCountLabel,
  artifactCounts,
  domainLabel,
  subdomainCountLabel,
} from "../domain";
import { formatQueryLine } from "../query";
import { artifactHref, domainHref, pathUnder, searchHref } from "../route";

/** tileCap is how many tiles the grid shows before the reader asks for the
 * rest, which keeps a domain with dozens of children to one screen. */
const tileCap = 12;

/** subdomainViews pairs each view with the label its segment carries. The
 * state key is lowercase because it names a CSS class, and a segment label is
 * sentence-case prose the way every other segmented control in the build reads
 * (§13.10). */
const subdomainViews = [
  { view: "grid", label: "Grid" },
  { view: "list", label: "List" },
] as const;

/** FilterField is the narrow filter both at-scale sections close their header
 * row with. It is a row rather than a bare input, so the magnifier sits inside
 * the border the way it does in the top bar's search trigger and on the search
 * surface, which is what marks the control as a filter (§13.10). */
function FilterField({
  label,
  value,
  onTyped,
}: {
  label: string;
  value: string;
  onTyped: (text: string) => void;
}) {
  return (
    <div className="filter-field">
      <Magnifier size={13} />
      <input
        className="filter-input"
        type="search"
        aria-label={label}
        placeholder={label}
        value={value}
        onChange={(event) => {
          onTyped(event.target.value);
        }}
      />
    </div>
  );
}

/** SubdomainTiles is the compact treatment: the section label carrying the
 * child count, a filter over the names and a grid-or-list toggle on the same
 * row, and one tile per child.
 *
 * The tile states how many artifacts stand under the child, which is what puts
 * the reader on the busiest one first: the grid is ordered by that count, and
 * the caption under it says so. A load_domain descriptor
 * (`pkg/registry/server/server.go`, `DomainDescriptor`) carries the nested
 * subtree and no artifact count, so the count comes from the §4.5.2 catalog
 * read the browser issues for the page rather than from a scoped search behind
 * every tile.
 *
 * A catalog read that failed arrives as a null and leaves the grid in the order
 * the response returned, with the tile stating what that response reported
 * below the child, which is its subdomain count where it carries one. */
export function SubdomainTiles({
  subdomains,
  parent,
  catalog,
}: {
  subdomains: DomainDescriptor[];
  parent: string;
  catalog: string[] | null;
}) {
  const [filter, setFilter] = useState("");
  const [view, setView] = useState<"grid" | "list">("grid");
  const [all, setAll] = useState(false);

  const counts =
    catalog === null
      ? null
      : artifactCounts(
          catalog,
          subdomains.map((child) => child.path),
          parent,
        );
  // The busiest child leads, and children that hold the same number keep the
  // path order the response returned them in.
  const ordered =
    counts === null
      ? subdomains
      : [...subdomains].sort(
          (a, b) => (counts.get(b.path) ?? 0) - (counts.get(a.path) ?? 0),
        );

  const needle = filter.trim().toLowerCase();
  // The filter runs over the label the tile carries, so a reader who types
  // what is on screen matches the tile they can see.
  const matched =
    needle === ""
      ? ordered
      : ordered.filter((child) =>
          domainLabel(child.path, parent).toLowerCase().includes(needle),
        );
  const shown = all ? matched : matched.slice(0, tileCap);

  return (
    <div className="subdomain-tiles">
      <div className="section-head">
        <h2 className="label">Subdomains</h2>
        {/* The count states the listing under it rather than the domain's
            own total, so a filter that narrows the grid narrows the figure
            with it. The header badge beside the domain name is where the
            unfiltered total is read. */}
        <span className="mono quiet section-count">{matched.length}</span>
        <FilterField label="Filter subdomains" value={filter} onTyped={setFilter} />
        <div className="segmented" role="group" aria-label="Subdomain view">
          {subdomainViews.map((choice) => (
            <button
              key={choice.view}
              type="button"
              className={
                view === choice.view ? "segment segment-on" : "segment"
              }
              aria-pressed={view === choice.view}
              onClick={() => {
                setView(choice.view);
              }}
            >
              {choice.label}
            </button>
          ))}
        </div>
      </div>
      {/* A filter that matches nothing states the outcome. Dropping the grid
          and leaving the controls over blank space reads as a listing that
          failed to load (§13.10). */}
      {matched.length === 0 ? (
        <EmptyState title="Nothing matched">
          Clear the filter to see every subdomain.
        </EmptyState>
      ) : (
        <ul
          className={view === "grid" ? "tile-grid" : "tile-list"}
          aria-label="Subdomains"
        >
          {shown.map((child) => {
            const count =
              counts === null
                ? subdomainCountLabel((child.subdomains ?? []).length)
                : artifactCountLabel(counts.get(child.path) ?? 0);
            return (
              <li
                key={child.path}
                className={view === "grid" ? "tile" : "tile tile-row"}
              >
                {/* The tile is one target, on both arms. The name carries the
                  overlay that makes the whole tile follow the link
                  (`index.css`, `.stretched-link`), so the row's description
                  and its count are aimable too. */}
                <a
                  className="tile-name mono stretched-link"
                  href={domainHref(child.path)}
                >
                  {domainLabel(child.path, parent)}
                </a>
                {/* The row has the width for what the tile has no room to
                  carry, so the list arm states the child's description on the
                  same line. The grid arm leaves it out: a six-column tile
                  clips it to a few characters. */}
                {view === "list" && (
                  <span
                    className={`quiet clipped tile-description${
                      child.description === undefined ||
                      child.description === ""
                        ? " absent-description"
                        : ""
                    }`}
                  >
                    {child.description === undefined || child.description === ""
                      ? "No description."
                      : child.description}
                  </span>
                )}
                {count !== null && (
                  <span className="mono quiet tile-count">{count}</span>
                )}
              </li>
            );
          })}
        </ul>
      )}
      {/* The filter rewrites the grid and the count in the heading above it,
          and neither is a change a reader who cannot see the page is told
          about. The region is rendered on every state of the section, empty
          until a filter is typed: a region mounted at the moment its text
          arrives is not in the accessibility tree when the change happens,
          and the announcement is dropped (§13.10). */}
      <p
        className="assistive-only"
        role="status"
        aria-live="polite"
        data-testid="subdomain-filter-announcement"
      >
        {filterAnnouncement(
          needle !== "",
          matched.length,
          ordered.length,
          "subdomain",
        )}
      </p>
      <div className="tile-foot">
        {!all && matched.length > shown.length && (
          <button
            type="button"
            className="tile-more"
            data-testid="show-all-subdomains"
            onClick={() => {
              setAll(true);
            }}
          >
            Show all {matched.length} subdomains
          </button>
        )}
        {counts !== null && matched.length > 0 && (
          <span className="quiet tile-order">Sorted by artifact count.</span>
        )}
      </div>
    </div>
  );
}

/** ArtifactColumn is what the table sorts on. Every value is present on every
 * descriptor or rendered as absent, so a sort never reorders on a field half
 * the rows lack. */
type ArtifactColumn = "id" | "type" | "version";

const sortOptions: { key: ArtifactColumn; label: string }[] = [
  { key: "id", label: "artifact" },
  { key: "type", label: "type" },
  { key: "version", label: "version" },
];

/** ArtifactTable is the at-scale artifact treatment: a filter over the domain's
 * own listing, a type chip per returned type beside an All chip, a sort
 * control, and the author's own picks in their own block above the rest. Each
 * description is clipped to one line, because at this count the table is a map
 * rather than a reading surface.
 *
 * The author's picks stand above the rest under every ordering. The sort
 * control chooses what orders the rows inside each block, so it names the
 * column it sorts on rather than the arrangement of the blocks.
 *
 * The filter runs over the rows the response carried, and §4.5.5 caps that
 * listing at the configured notable_count. Where the cap trimmed it, a filter
 * that matches nothing has established nothing about the domain, so the table
 * states the reach of the filter and continues into the scoped §4.5.3 search
 * carrying the same words and the same type. Reporting the artifact as absent
 * and offering a cleared filter as the recovery denies an artifact the domain
 * holds, and neither clearing the filter nor changing the type loads the rows
 * the response withheld (§13.10).
 *
 * A sort over that same trimmed listing ranks the returned rows alone, and a
 * ranking reads as a statement about the domain: the top row of a version
 * sort looks like the domain's highest version when it is the highest of the
 * rows the page loaded. The sort therefore states its reach on the same line
 * and offers the same continuation the filter does. */
export function ArtifactTable({
  artifacts,
  scope,
  trimmed,
  withheld,
  tail,
}: {
  artifacts: ArtifactDescriptor[];
  /** scope is the domain the listing belongs to, which bounds the search the
   * continuation runs. The registry root carries no scope filter. */
  scope: string;
  /** trimmed reports that the listing is a partial view of the domain. */
  trimmed: boolean;
  /** withheld is how many artifacts the domain holds beyond the listing, and
   * null where the response reported the reduction without a count. */
  withheld: number | null;
  /** tail is the continuation row that states how much of the domain the
   * listing carries. The table owns where it is drawn, because the table is
   * what knows whether the rows under it are still the ones the tail counts. */
  tail?: ReactNode;
}) {
  const [type, setType] = useState("");
  const [filter, setFilter] = useState("");
  const [column, setColumn] = useState<ArtifactColumn>("id");

  const types = [...new Set(artifacts.map((artifact) => artifact.type))].sort();
  const needle = filter.trim().toLowerCase();
  // The filter runs over the identifier the first column carries, for the
  // reason the subdomain filter runs over the tile's own label. That column
  // states the path under the current domain, so a filter run over the whole
  // identifier would keep rows on a stretch of prefix no row prints.
  const matched = artifacts.filter(
    (artifact) =>
      (type === "" || artifact.type === type) &&
      (needle === "" ||
        pathUnder(artifact.id, scope).toLowerCase().includes(needle)),
  );
  const filtering = needle !== "" || type !== "";
  // A column other than the identifier ranks the rows, and a ranking over a
  // trimmed listing is the reading that goes wrong silently: the top row of a
  // version sort is the highest version the page loaded, and the domain's
  // highest can be among the rows the response withheld. Identifier order is
  // the arrangement the listing already stands in, so it makes no claim the
  // continuation row below the table does not already carry.
  const ranking = column !== "id";
  // A filter, a type chip, or a ranking over a trimmed listing answers for the
  // returned rows alone, which is what the reach line exists to say.
  const narrowed = trimmed && (filtering || ranking);
  const curated = sorted(
    matched.filter((artifact) => artifact.source === "featured"),
    column,
  );
  const rest = sorted(
    matched.filter((artifact) => artifact.source !== "featured"),
    column,
  );

  return (
    <div className="artifact-table">
      <div className="section-head">
        <h2 className="label">Artifacts</h2>
        <FilterField
          label="Filter in this domain"
          value={filter}
          onTyped={setFilter}
        />
        {/* The All chip is the unfiltered set stated as a chip of its own, so
            the row always carries the state it is in rather than leaving the
            reader to read it from the absence of an active chip. */}
        <div className="chip-row" role="group" aria-label="Type">
          <button
            type="button"
            className={type === "" ? "pill pill-active" : "pill"}
            aria-pressed={type === ""}
            onClick={() => {
              setType("");
            }}
          >
            All
          </button>
          {types.map((name) => (
            <button
              key={name}
              type="button"
              className={type === name ? "pill pill-active" : "pill"}
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
      {/* Same reason as the tiles above: the filters and the type chips stay
          on screen, so the region under them says why it holds no rows. A
          trimmed listing states the trim instead, because the returned rows
          are not the whole domain and the recovery lies past their edge. */}
      {matched.length === 0 && !narrowed && (
        <EmptyState title="Nothing matched">
          Clear the filter or pick another type.
        </EmptyState>
      )}
      {narrowed && (
        <ListingReach
          scope={scope}
          subject={filtering ? "filter" : "sort"}
          shown={artifacts.length}
          empty={filtering && matched.length === 0}
          withheld={withheld}
          query={filter.trim()}
          type={type}
        />
      )}
      {curated.length > 0 && (
        <div className="curated-block">
          <div className="curated-head">
            <span aria-hidden="true">★</span>
            <span className="label">Curated by the domain author</span>
            <span className="mono">{curated.length}</span>
          </div>
          <ArtifactRows
            rows={curated}
            scope={scope}
            region="Curated artifacts"
          />
        </div>
      )}
      {/* The rest carries no heading of its own. The picks above it are the
          block that is titled, and a second title over everything the domain
          returned names the listing the page is already about. */}
      {rest.length > 0 && (
        <ArtifactRows
          rows={rest}
          scope={scope}
          region="Artifacts in this domain"
        />
      )}
      {/* The tail counts the rows the response returned, and a filter narrows
          what the table draws without loading any more of them. It is
          therefore drawn over the listing no control has acted on: under a
          filter it states a count the reader cannot see, and beneath an empty
          table it asserts rows are on screen. The reach line above states the
          same edge for the listing the reader is looking at, and it carries
          the continuation past it, so the two never stand together. Both rows
          carry role="status", so the contradiction is read out as well as
          drawn (§13.10). */}
      {!narrowed && !filtering && tail}
      {/* The filter and the type chips rewrite the table body, and the counts
          they change live in the rows themselves. The region states the new
          one for the reader who cannot see the table, on the same terms the
          search surface and the command palette state theirs (§13.10). */}
      <p
        className="assistive-only"
        role="status"
        aria-live="polite"
        data-testid="artifact-filter-announcement"
      >
        {filterAnnouncement(
          filtering,
          matched.length,
          artifacts.length,
          "artifact",
        )}
      </p>
    </div>
  );
}

/** filterAnnouncement is what a client-side filter says to a reader who
 * cannot see the listing it rewrote. It is empty while no filter is applied,
 * so the listing is announced when the reader narrows it rather than on the
 * first paint, and it counts against the rows the surface holds rather than
 * against the domain: both filters run over what the response returned, and
 * the reach line beside the table is what continues past that edge. */
function filterAnnouncement(
  filtering: boolean,
  matched: number,
  total: number,
  noun: string,
): string {
  if (!filtering) {
    return "";
  }
  if (matched === 0) {
    return `No ${noun} matched.`;
  }
  return `${String(matched)} of ${String(total)} ${total === 1 ? noun : `${noun}s`} matched.`;
}

/** ListingReach states how far the control the reader just used reached and
 * continues past it. It is drawn where a filter, a type chip, or a sort acts
 * on a listing the response trimmed: a match found among the returned rows is
 * still no answer about the rows that were withheld, a filter that matched
 * nothing has established nothing at all, and a ranking of the returned rows
 * is not the domain's ranking.
 *
 * The continuation is the §4.5.3 search bounded to this domain, carrying the
 * typed words and the chosen type, so the reader lands on the same question
 * asked of the whole domain rather than on an unfiltered search they have to
 * retype. */
function ListingReach({
  scope,
  subject,
  shown,
  empty,
  withheld,
  query,
  type,
}: {
  scope: string;
  /** subject names the control the line answers for, which is the word the
   * sentence opens on. */
  subject: "filter" | "sort";
  shown: number;
  /** empty reports that the filter matched none of the returned rows. */
  empty: boolean;
  withheld: number | null;
  query: string;
  type: string;
}) {
  const rest =
    withheld === null
      ? "The response returned fewer artifacts than the domain holds."
      : `${String(withheld)} more ${withheld === 1 ? "artifact stands" : "artifacts stand"} under this domain.`;
  return (
    <div className="listing-reach" role="status" data-testid="listing-reach">
      <span className="listing-tail-mark" aria-hidden="true" />
      <div className="listing-tail-body">
        <p className="listing-tail-line">
          {empty && "Nothing on this page matched. "}
          {`The ${subject} covers the ${String(shown)} ${shown === 1 ? "artifact" : "artifacts"} this page loaded. ${rest}`}
        </p>
        <a
          className="button"
          data-testid="listing-reach-continue"
          href={reachHref(scope, query, type)}
        >
          Search the whole domain
        </a>
      </div>
    </div>
  );
}

/** reachHref addresses the scoped search the filter continues into. The
 * registry root carries no scope filter, because the empty path bounds
 * nothing. */
function reachHref(scope: string, query: string, type: string): string {
  return searchHref(formatQueryLine({ query, type, scope, tags: [] }));
}

/** ArtifactRows draws one block of the table. The column labels are quiet
 * markers over the columns they name: the sort control above the table is
 * where an ordering is chosen, so a header carries no control of its own.
 *
 * The table keeps its designed column widths down to a floor and scrolls
 * sideways inside its own container below that, the way the layer panel's
 * table does. Left to grow with its content it rendered past the right edge
 * of the window at a 1024px viewport and scrolled the whole shell sideways,
 * carrying the top bar and the sidebar with it. The container is focusable
 * and carries a name of its own, because a region that scrolls must be
 * reachable from the keyboard (§13.10). */
function ArtifactRows({
  rows,
  scope,
  region,
}: {
  rows: ArtifactDescriptor[];
  /** scope is the domain the page stands on, which the identifier column
   * states each row relative to. */
  scope: string;
  region: string;
}) {
  return (
    <div
      className="table-scroll"
      tabIndex={0}
      role="region"
      aria-label={region}
    >
      <table className="data-table artifact-rows" aria-label="Artifacts">
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
              {/* The cell states the path under the domain the page is on,
                the way the design's own table does. The heading above the
                table already names that domain, so a cell carrying the whole
                identifier restates it on every row and pushes the segment
                that tells the rows apart to the right of a prefix they all
                share. The link still addresses the whole identifier, and a
                title carries it for a reader who needs it. */}
              <td className="mono">
                <a href={artifactHref(artifact.id)} title={artifact.id}>
                  {pathUnder(artifact.id, scope)}
                </a>
              </td>
              {/* The type and the version are the same two markers the compact
                listing and the viewer carry, so the cell renders the shared
                badge and the shared `v` prefix rather than the raw field: a
                table that prints them bare reads as a different component
                for the same pair of values. */}
              <td>
                <TypeBadge type={artifact.type} />
              </td>
              <td className="mono quiet">
                {artifact.version === undefined || artifact.version === ""
                  ? "unversioned"
                  : formatVersion(artifact.version)}
              </td>
              <td>
                <span className="tag-list">
                  {(artifact.tags ?? []).map((tag) => (
                    <span key={tag} className="tag">
                      {tag}
                    </span>
                  ))}
                </span>
              </td>
              {/* The clip sits on an inner element rather than on the cell.
                A `display: block` cell leaves the row's cell layout, which
                broke the rule under the description column away from the rule
                under the columns beside it and dropped the row's tag chips
                below their neighbours' baseline. */}
              <td className="quiet">
                <span
                  className={`clipped${artifact.description === undefined ? " absent-description" : ""}`}
                >
                  {artifact.description ?? "No description."}
                </span>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

/** sorted orders a row set by the chosen column. A descriptor omits a version
 * where it carries none, and an absent value sorts as the empty string rather
 * than dropping the row. */
function sorted(
  rows: ArtifactDescriptor[],
  column: ArtifactColumn,
): ArtifactDescriptor[] {
  return [...rows].sort((a, b) =>
    valueOf(a, column).localeCompare(valueOf(b, column)),
  );
}

function valueOf(artifact: ArtifactDescriptor, column: ArtifactColumn): string {
  switch (column) {
    case "type":
      return artifact.type;
    case "version":
      return artifact.version ?? "";
    case "id":
      return artifact.id;
  }
}
