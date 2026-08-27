// Search over the catalog. §13.10 fixes the filter set to the ones the SDK
// and the CLI carry, which are type, scope, and tags, so this surface offers
// those and no others. Every argument is optional, so a request with no query
// text is a browse over the filters.
//
// The row is drawn as the design pass fixed it: the label names the row, an
// applied filter is a filled pill carrying its own remove control, a filter
// whose values the registry can enumerate is added from an outlined dropdown,
// and one whose values it cannot is added through a token entry. The result
// count sits at the right of the same row.

import { useEffect, useRef, useState } from "react";

import { ArtifactRow } from "../components/ArtifactRow";
import { usePopupDismiss } from "../components/focus";
import { Chevron, EmptyState, ErrorState, Loading, Magnifier } from "../components/primitives";
import type { SearchFilters, SearchResponse } from "../api";
import { loadDomain, searchArtifacts } from "../api";
import { scopePaths } from "../domain";
import { formatQueryLine, parseQueryLine } from "../query";
import { replaceRoute, searchHref } from "../route";
import type { Async } from "../useAsync";
import { useAsync, useErrorReport } from "../useAsync";

const resultCap = 10;

/** searchPage is how many further results one continuation request asks for,
 * and searchCapMax is the largest `top_k` §5 accepts: a request above it is
 * refused with INVALID_ARGUMENT rather than served, so the cap stops there
 * and the recovery line stands alone for a match count past it. */
const searchPage = 20;
const searchCapMax = 50;

/** scopeDepth is how deep the scope dropdown reads the domain tree. It is the
 * depth the sidebar tree opens at, so every domain the reader can see without
 * expanding a node can also be named as a scope. A depth of 1 returns the
 * top-level entries with empty subtrees, which offered fewer scopes than the
 * tree beside it already listed. */
const scopeDepth = 2;

/** firstClassTypes are the §4.3 types every registry carries, which is what
 * the type dropdown can offer. An extension type registers through the
 * TypeProvider SPI and no response enumerates the registered set, so a filter
 * on one arrives through the palette's `type:` syntax or through the route,
 * and the row renders it as the pill any applied type takes. */
const firstClassTypes = [
  "skill",
  "agent",
  "context",
  "command",
  "rule",
  "hook",
  "mcp-server",
];

export function SearchSurface({
  query,
  onError,
}: {
  query: string;
  onError: (err: unknown) => void;
}) {
  // The route query carries the same line the palette types, so the surface
  // runs the palette's own parse over it. A query arriving as
  // "type:skill auth" opens with the skill pill applied and "auth" in the
  // field, which is the request the palette issued and the result set it
  // listed. The shell remounts the surface on each query, so the parse seeds
  // the state once per query and the reader's later edits stand.
  const seed = parseQueryLine(query);
  const [type, setType] = useState(seed.type);
  const [scope, setScope] = useState(seed.scope);
  const [tags, setTags] = useState<string[]>(seed.tags);
  const [text, setText] = useState(seed.query);

  // A scope is a §4.2 domain path, so the dropdown offers every domain the
  // root read describes: its top-level entries, the subdomains those entries
  // carry, and every segment a §4.5.5 folded chain crossed on the way to one.
  // A scope matches by prefix and the browser navigates to each of those
  // domains, so a list drawn from the top-level entry paths alone would hide a
  // domain that both surfaces answer for. The read is an enhancement to the
  // row rather than the surface's own catalog read: a failure leaves the
  // dropdown offering the unscoped search alone and is neither reported to the
  // shell nor drawn, because the search itself still answers.
  const domains = useAsync(() => loadDomain("", scopeDepth), []);
  const scopeOptions = scopePaths(domains.value?.subdomains ?? [], "");

  const filters: SearchFilters = { query: text, type, scope, tags };
  const key = JSON.stringify(filters);
  // The continuation raises the cap on the same request rather than paging,
  // because §5 search takes a result count and no offset. A new request is a
  // new result set, so the cap drops back to the first page when the query or
  // a filter changes; it is reset during the render that reads the new key so
  // the raised cap never issues a request against the new filters.
  const [cap, setCap] = useState(resultCap);
  const [capKey, setCapKey] = useState(key);
  if (capKey !== key) {
    setCapKey(key);
    setCap(resultCap);
  }
  const search = useAsync(() => searchArtifacts(filters, cap), [key, cap]);
  // A search is a page of the catalog like any other, so the query and the
  // filters live in the route rather than only in component state: the
  // address bar names the search that is on screen, a reload restores it, and
  // the reader can send it to someone else. The entry is replaced rather than
  // pushed because a pushed entry per keystroke would bury whatever the
  // reader was looking at before the search under one step per character.
  const line = formatQueryLine(filters);
  useEffect(() => {
    replaceRoute(searchHref(line));
  }, [line]);
  useErrorReport(search.error, onError);
  const body = search.value;

  return (
    // The content column opens on the query field. A "Search" title over a
    // field already labelled "Search artifacts" restates the field and pushes
    // it and the filter row down the page, so the surface carries none and is
    // named for assistive technology by the landmark label alone. The layer
    // panel does carry a title, because its header also holds a description
    // and its write actions.
    <section className="surface" aria-label="Search">
      {/* The field is a row rather than a bare input: the magnifier names it
          as the query field the way the top bar's trigger and the palette's
          own field do, and the border belongs to the row so the icon sits
          inside it. */}
      <div className="search-field">
        <Magnifier />
        <input
          className="search-input"
          type="search"
          aria-label="Search artifacts"
          value={text}
          placeholder="Search artifacts"
          onChange={(event) => {
            setText(event.target.value);
          }}
        />
      </div>
      <div className="filter-row">
        <span className="filter-label mono">Filters</span>
        {type === "" ? (
          <FilterSelect
            label="type"
            options={firstClassTypes}
            onSelect={setType}
          />
        ) : (
          <FilterPill
            label="type"
            value={type}
            onRemove={() => {
              setType("");
            }}
          />
        )}
        {scope === "" ? (
          <FilterSelect
            label="scope"
            options={scopeOptions}
            onSelect={setScope}
          />
        ) : (
          <FilterPill
            label="scope"
            value={scope}
            onRemove={() => {
              setScope("");
            }}
          />
        )}
        {tags.map((tag) => (
          <FilterPill
            key={tag}
            label="tag"
            value={tag}
            onRemove={() => {
              setTags(tags.filter((held) => held !== tag));
            }}
          />
        ))}
        <TokenEntry
          label="tag"
          onAdd={(tag) => {
            setTags((held) => (held.includes(tag) ? held : [...held, tag]));
          }}
        />
        {/* The match count is taken before the cap truncates the list, so
            fewer results than matches is the ordinary outcome. */}
        {body !== null && (
          <p className="result-count quiet" data-testid="result-count">
            Showing <strong>{(body.results ?? []).length}</strong> of{" "}
            <strong>{body.total_matched}</strong>
          </p>
        )}
      </div>
      <SearchResults
        search={search}
        cap={cap}
        onMore={(step) => {
          setCap((held) => Math.min(held + step, searchCapMax));
        }}
      />
    </section>
  );
}

/** FilterPill is one applied filter. It names the filter it applies as well
 * as the value, so a row carrying several reads as the request it issues, and
 * it carries the control that removes it. */
function FilterPill({
  label,
  value,
  onRemove,
}: {
  label: string;
  value: string;
  onRemove: () => void;
}) {
  return (
    <span className="pill pill-active">
      {label}: {value}
      <button
        type="button"
        className="pill-remove"
        aria-label={`Remove the ${value} filter`}
        onClick={onRemove}
      >
        ✕
      </button>
    </span>
  );
}

/** FilterSelect applies a filter whose values are enumerable, which is the
 * type and the scope. It reads as the unapplied state of the pill that
 * replaces it: the closed control names the filter and the unfiltered read it
 * currently stands for. */
function FilterSelect({
  label,
  options,
  onSelect,
}: {
  label: string;
  options: string[];
  onSelect: (value: string) => void;
}) {
  // A native select is as wide as its widest option, so the scope control
  // stretched to the deepest domain path in the catalog and left a dead gap
  // between its label and its indicator. The pill draws the closed label and
  // the chevron itself, which takes the width from the label, and the select
  // is laid over the pill transparently so the control the reader operates is
  // still the browser's own.
  return (
    <span className="pill pill-select">
      <span className="pill-select-label" aria-hidden="true">
        {label}: all
      </span>
      <Chevron />
      <select
        aria-label={`Filter by ${label}`}
        value=""
        onChange={(event) => {
          onSelect(event.target.value);
        }}
      >
        <option value="">{label}: all</option>
        {options.map((option) => (
          <option key={option} value={option}>
            {label}: {option}
          </option>
        ))}
      </select>
    </span>
  );
}

/** TokenEntry adds a filter value the registry cannot enumerate, which is
 * every tag. It opens on a press, takes one value, and closes. */
function TokenEntry({
  label,
  onAdd,
}: {
  label: string;
  onAdd: (value: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const [typed, setTyped] = useState("");
  const trigger = useRef<HTMLButtonElement>(null);
  // The entry is a transient popup, so it dismisses on Escape and on a press
  // or a focus move outside itself. Without that, a reader who opened it to
  // look loses the add control for the rest of the session, because the
  // field stands in the button's place until a value is submitted.
  const owed = useRef(false);
  const entry = usePopupDismiss<HTMLSpanElement>(
    open,
    () => {
      owed.current = true;
      setTyped("");
      setOpen(false);
    },
    trigger,
  );
  // usePopupDismiss hands focus back to the trigger, but the trigger is
  // unmounted while the field stands in its place, so the focus lands on
  // nothing. Every exit records that the button is owed the focus, and it
  // takes it once it is back. A submitted value closes the field the same way
  // a dismissal does, so without this a reader who applied a tag filter by
  // keyboard was left on the document body with the filter row gone from
  // under them.
  useEffect(() => {
    if (open || !owed.current) {
      return;
    }
    owed.current = false;
    trigger.current?.focus();
  }, [open]);

  const commit = () => {
    const value = typed.trim();
    if (value !== "") {
      onAdd(value);
    }
    owed.current = true;
    setTyped("");
    setOpen(false);
  };

  if (!open) {
    return (
      <button
        type="button"
        className="pill pill-add"
        ref={trigger}
        onClick={() => {
          setOpen(true);
        }}
      >
        + {label}
      </button>
    );
  }
  return (
    <span className="pill pill-entry" ref={entry}>
      <input
        type="text"
        aria-label={`Add a ${label} filter`}
        value={typed}
        autoFocus
        onChange={(event) => {
          setTyped(event.target.value);
        }}
        // The default action is refused because the add control takes the
        // focus back as the field closes, and the browser would then read the
        // same ⏎ as an activation of the button now under it and reopen the
        // field the reader just submitted.
        onKeyDown={(event) => {
          if (event.key === "Enter") {
            event.preventDefault();
            commit();
          }
        }}
      />
      <button type="button" onClick={commit}>
        Add
      </button>
    </span>
  );
}

function SearchResults({
  search,
  cap,
  onMore,
}: {
  search: Async<SearchResponse>;
  cap: number;
  onMore: (step: number) => void;
}) {
  if (search.loading) {
    return <Loading label="Searching." />;
  }
  if (search.error !== null) {
    return <ErrorState error={search.error} onRetry={search.reload} />;
  }
  const body = search.value;
  if (body === null) {
    return null;
  }
  const results = body.results ?? [];
  if (results.length === 0) {
    return (
      <EmptyState>
        Nothing matched. Widen the query or clear a filter.
      </EmptyState>
    );
  }
  // A lexical score is comparable only inside the result set it came back in,
  // so the relevance indicator ranks each row against the strongest score
  // here rather than against a fixed scale.
  const topScore = results.reduce(
    (top, artifact) => Math.max(top, artifact.score ?? 0),
    0,
  );
  const withheld = body.total_matched - results.length;
  // A result the cap withheld is reachable from the foot of the list. The
  // step asks for one page more, bounded by what is still withheld and by the
  // largest count §5 serves, and a step of zero means the cap is spent and
  // narrowing the request is the only way further.
  const step = Math.min(searchPage, withheld, searchCapMax - cap);
  return (
    <>
      {withheld > 0 && (
        <p className="quiet">
          Narrow the result set with a filter, drill into a subdomain, or run a
          more specific query.
        </p>
      )}
      <ul className="artifact-list">
        {results.map((artifact) => (
          <ArtifactRow
            key={artifact.id}
            artifact={artifact}
            ranked
            topScore={topScore}
          />
        ))}
      </ul>
      {step > 0 && (
        <div className="listing-continuation" data-testid="search-continuation">
          <button
            type="button"
            className="button"
            data-testid="search-continue"
            onClick={() => {
              onMore(step);
            }}
          >
            Load {step} more
          </button>
          <p className="quiet">Ranked by relevance.</p>
        </div>
      )}
    </>
  );
}
