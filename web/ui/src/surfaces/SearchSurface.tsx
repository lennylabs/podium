// Search over the catalog. §13.10 fixes the filter set to the ones the SDK
// and the CLI carry, which are type, scope, and tags, so this surface offers
// those and no others. Every argument is optional, so a request with no query
// text is a browse over the filters.
//
// The filters are drawn as the design pass fixed them: an active filter is a
// filled pill carrying its own remove control, an inactive one is outlined,
// and a filter whose values cannot be enumerated is added through a token
// entry. The result count sits at the right of the same row.

import { useState } from 'react';

import { ArtifactRow } from '../components/ArtifactRow';
import { EmptyState, ErrorState, Loading } from '../components/primitives';
import type { SearchFilters, SearchResponse } from '../api';
import { searchArtifacts } from '../api';
import { parseQueryLine } from '../query';
import type { Async } from '../useAsync';
import { useAsync, useErrorReport } from '../useAsync';

const resultCap = 10;

/** firstClassTypes are the §4.3 types every registry carries, which is what
 * the row can offer as pills. An extension type registers through the
 * TypeProvider SPI and no response enumerates the registered set, so the row
 * also takes a typed value rather than confining the filter to these. */
const firstClassTypes = ['skill', 'agent', 'context', 'command', 'rule', 'hook', 'mcp-server'];

export function SearchSurface({ query, onError }: { query: string; onError: (err: unknown) => void }) {
  // The route query carries the same line the palette types, so the surface
  // runs the palette's own parse over it. A query arriving as
  // "type:skill auth" opens with the skill pill filled and "auth" in the
  // field, which is the request the palette issued and the result set it
  // listed. The shell remounts the surface on each query, so the parse seeds
  // the state once per query and the reader's later edits stand.
  const seed = parseQueryLine(query);
  const [type, setType] = useState(seed.type);
  const [scope, setScope] = useState(seed.scope);
  const [tags, setTags] = useState<string[]>(seed.tags);
  const [text, setText] = useState(seed.query);

  const filters: SearchFilters = { query: text, type, scope, tags };
  const key = JSON.stringify(filters);
  const search = useAsync(() => searchArtifacts(filters, resultCap), [key]);
  useErrorReport(search.error, onError);
  const body = search.value;

  return (
    <section className="surface" aria-label="Search">
      <h1>Search</h1>
      <input
        className="search-field"
        type="search"
        aria-label="Search artifacts"
        value={text}
        placeholder="Search artifacts"
        onChange={(event) => {
          setText(event.target.value);
        }}
      />
      <div className="filter-row">
        <div className="filter-group" role="group" aria-label="Type">
          {firstClassTypes.map((name) => (
            <FilterPill
              key={name}
              label={name}
              active={type === name}
              onSelect={() => {
                setType(name);
              }}
              onRemove={() => {
                setType('');
              }}
            />
          ))}
          {type !== '' && !firstClassTypes.includes(type) && (
            <FilterPill
              label={type}
              active
              onRemove={() => {
                setType('');
              }}
            />
          )}
          <TokenEntry label="type" onAdd={setType} />
        </div>
        <div className="filter-group" role="group" aria-label="Scope">
          {scope === '' ? (
            <TokenEntry label="scope" onAdd={setScope} />
          ) : (
            <FilterPill
              label={scope}
              active
              onRemove={() => {
                setScope('');
              }}
            />
          )}
        </div>
        <div className="filter-group" role="group" aria-label="Tags">
          {tags.map((tag) => (
            <FilterPill
              key={tag}
              label={tag}
              active
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
        </div>
        {/* The match count is taken before the cap truncates the list, so
            fewer results than matches is the ordinary outcome. */}
        {body !== null && (
          <p className="result-count quiet mono" data-testid="result-count">
            Showing {(body.results ?? []).length} of {body.total_matched}
          </p>
        )}
      </div>
      <SearchResults search={search} />
    </section>
  );
}

/** FilterPill is one filter value. An active value is filled and carries the
 * control that removes it; an inactive one is outlined and selects on a
 * press, so both states are one press away from the other. */
function FilterPill({
  label,
  active,
  onSelect,
  onRemove,
}: {
  label: string;
  active: boolean;
  /** onSelect activates an inactive value. An active pill offers removal
   * alone, so it carries none. */
  onSelect?: () => void;
  onRemove: () => void;
}) {
  if (!active) {
    return (
      <button type="button" className="pill" onClick={onSelect}>
        {label}
      </button>
    );
  }
  return (
    <span className="pill pill-active">
      {label}
      <button type="button" className="pill-remove" aria-label={`Remove the ${label} filter`} onClick={onRemove}>
        ✕
      </button>
    </span>
  );
}

/** TokenEntry adds a filter value the row cannot offer as a pill, which is
 * every tag, every scope, and a type the registry carries through the
 * TypeProvider SPI. It opens on a press, takes one value, and closes. */
function TokenEntry({ label, onAdd }: { label: string; onAdd: (value: string) => void }) {
  const [open, setOpen] = useState(false);
  const [typed, setTyped] = useState('');

  const commit = () => {
    const value = typed.trim();
    if (value !== '') {
      onAdd(value);
    }
    setTyped('');
    setOpen(false);
  };

  if (!open) {
    return (
      <button
        type="button"
        className="pill pill-add"
        onClick={() => {
          setOpen(true);
        }}
      >
        + {label}
      </button>
    );
  }
  return (
    <span className="pill pill-entry">
      <input
        type="text"
        aria-label={`Add a ${label} filter`}
        value={typed}
        autoFocus
        onChange={(event) => {
          setTyped(event.target.value);
        }}
        onKeyDown={(event) => {
          if (event.key === 'Enter') {
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

function SearchResults({ search }: { search: Async<SearchResponse> }) {
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
    return <EmptyState>Nothing matched. Widen the query or clear a filter.</EmptyState>;
  }
  // A lexical score is comparable only inside the result set it came back in,
  // so the relevance indicator ranks each row against the strongest score
  // here rather than against a fixed scale.
  const topScore = results.reduce((top, artifact) => Math.max(top, artifact.score ?? 0), 0);
  return (
    <>
      {results.length < body.total_matched && (
        <p className="quiet">
          Narrow the result set with a filter, drill into a subdomain, or run a more specific query.
        </p>
      )}
      <ul className="artifact-list">
        {results.map((artifact) => (
          <ArtifactRow key={artifact.id} artifact={artifact} ranked topScore={topScore} />
        ))}
      </ul>
    </>
  );
}
