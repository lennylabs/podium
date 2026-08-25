// What is still recoverable. Unregistering a layer removes its artifacts from
// every caller's effective view at once and soft-deletes the layer for a
// retention window, so the panel carries a surface for the layers that have
// not been erased.

import { useState } from 'react';

import { EmptyState, ErrorState, Loading } from '../components/primitives';
import { ApiError, listDeletedLayers, restoreLayer } from '../api';
import { useAsync } from '../useAsync';

export function DeletedLayers({ onRestored, readOnly }: { onRestored: () => void; readOnly: boolean }) {
  const deleted = useAsync(() => listDeletedLayers(), []);
  const [refusal, setRefusal] = useState<unknown>(null);

  if (deleted.loading) {
    return <Loading label="Loading the recoverable layers." />;
  }
  if (deleted.error !== null) {
    return <ErrorState error={deleted.error} onRetry={deleted.reload} />;
  }
  const rows = deleted.value ?? [];
  return (
    <section aria-label="Recently unregistered">
      <h2>Recently unregistered</h2>
      {rows.length === 0 ? (
        <EmptyState>Nothing is waiting to be erased.</EmptyState>
      ) : (
        <ul className="artifact-list">
          {rows.map((layer) => (
            <li key={layer.ID} className="artifact-row">
              <span className="mono">{layer.ID}</span>
              <button
                type="button"
                disabled={readOnly}
                onClick={() => {
                  restoreLayer(layer.ID).then(
                    () => {
                      setRefusal(null);
                      deleted.reload();
                      onRestored();
                    },
                    (err: unknown) => {
                      setRefusal(err);
                    },
                  );
                }}
              >
                Restore
              </button>
            </li>
          ))}
        </ul>
      )}
      {refusal !== null && (
        <p className="row-refusal" role="alert">
          The registry refused that action and nothing changed.{' '}
          <span className="mono">{refusal instanceof ApiError ? refusal.code : 'registry.unavailable'}</span>
        </p>
      )}
    </section>
  );
}
