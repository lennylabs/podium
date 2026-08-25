// Search over the catalog. §13.10 fixes the filter set to the ones the SDK
// and the CLI carry, which are type, scope, and tags, so this surface offers
// those and no others. Every argument is optional, so a request with no query
// text is a browse over the filters.

import { useState } from 'react';

import { ArtifactRow } from '../components/ArtifactRow';
import { EmptyState, ErrorState, Loading } from '../components/primitives';
import type { SearchFilters, SearchResponse } from '../api';
import { searchArtifacts } from '../api';
import type { Async } from '../useAsync';
import { useAsync, useErrorReport } from '../useAsync';

const resultCap = 10;

export function SearchSurface({ query, onError }: { query: string; onError: (err: unknown) => void }) {
  const [type, setType] = useState('');
  const [scope, setScope] = useState('');
  const [tags, setTags] = useState('');
  const [text, setText] = useState(query);

  const filters: SearchFilters = {
    query: text,
    type,
    scope,
    tags: tags
      .split(',')
      .map((tag) => tag.trim())
      .filter((tag) => tag !== ''),
  };
  const key = JSON.stringify(filters);
  const search = useAsync(() => searchArtifacts(filters, resultCap), [key]);
  useErrorReport(search.error, onError);

  return (
    <section className="surface" aria-label="Search">
      <h1>Search</h1>
      <label className="field">
        <span className="label">Query</span>
        <input
          type="search"
          value={text}
          placeholder="Search artifacts"
          onChange={(event) => {
            setText(event.target.value);
          }}
        />
      </label>
      <div className="filters">
        <label className="field">
          <span className="label">Type</span>
          <input
            type="text"
            value={type}
            onChange={(event) => {
              setType(event.target.value);
            }}
          />
        </label>
        <label className="field">
          <span className="label">Scope</span>
          <input
            type="text"
            value={scope}
            onChange={(event) => {
              setScope(event.target.value);
            }}
          />
        </label>
        <label className="field">
          <span className="label">Tags</span>
          <input
            type="text"
            value={tags}
            placeholder="comma separated"
            onChange={(event) => {
              setTags(event.target.value);
            }}
          />
        </label>
      </div>
      <SearchResults search={search} />
    </section>
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
  return (
    <>
      {/* The match count is taken before the cap truncates the list, so
          fewer results than matches is the ordinary outcome. */}
      <p className="quiet mono" data-testid="result-count">
        Showing {results.length} of {body.total_matched}
      </p>
      {results.length < body.total_matched && (
        <p className="quiet">
          Narrow the result set with a filter, drill into a subdomain, or run a more specific query.
        </p>
      )}
      <ul className="artifact-list">
        {results.map((artifact) => (
          <ArtifactRow key={artifact.id} artifact={artifact} />
        ))}
      </ul>
    </>
  );
}
