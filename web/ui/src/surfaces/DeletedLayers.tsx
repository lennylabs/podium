// What is still recoverable. Unregistering a layer removes its artifacts from
// every caller's effective view at once and soft-deletes the layer for a
// retention window, so the panel carries a surface for the layers that have
// not been erased. It is a page of its own rather than a section inside the
// panel, because it carries a table and the panel carries another: rendered
// together, the reader gets two stacked tables and the precedence label and
// the layer rows are pushed down by the height of this one.
//
// The question this surface answers is how long is left, so every row states
// the date it was unregistered, the date it is erased on, and how much of the
// window remains. A row inside the accent window says so, because that is the
// row a reader has to act on today.

import { useState } from 'react';

import { accentDays, daysLeft, erasesOn, recoveryDays } from './recovery';
import { EmptyState, ErrorState, Loading } from '../components/primitives';
import { layersHref } from '../route';
import { SourceCell } from '../components/SourceCell';
import type { LayerRecord } from '../api';
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
  const restore = (id: string) => {
    restoreLayer(id).then(
      () => {
        setRefusal(null);
        deleted.reload();
        onRestored();
      },
      (err: unknown) => {
        setRefusal(err);
      },
    );
  };

  return (
    <section className="surface" aria-label="Recently unregistered">
      {/* The trail leads back to the panel, which is where the reader came
          from and where the layer this surface restores is listed again. */}
      <nav className="breadcrumb" aria-label="Breadcrumb">
        <a href={layersHref}>Layers</a>
        <span className="breadcrumb-sep" aria-hidden="true">
          /
        </span>
        <span className="breadcrumb-here" aria-current="page">
          Recently unregistered
        </span>
      </nav>
      <h1>Recently unregistered</h1>
      <p className="lead">A layer stays restorable for {recoveryDays} days, after which it is erased.</p>
      {rows.length === 0 ? (
        <EmptyState>Nothing is waiting to be erased.</EmptyState>
      ) : (
        <table className="data-table">
          <thead>
            <tr>
              <th>Layer</th>
              <th>Source</th>
              <th>Unregistered</th>
              <th>Erased</th>
              <th>Actions</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((layer) => (
              <DeletedRow key={layer.ID} layer={layer} readOnly={readOnly} onRestore={restore} />
            ))}
          </tbody>
        </table>
      )}
      {refusal !== null && (
        <p className="row-refusal" role="alert">
          The registry refused that action and nothing changed.{' '}
          <span className="mono">{refusal instanceof ApiError ? refusal.code : 'registry.unavailable'}</span>
        </p>
      )}
      {/* What a restore does, stated where the reader decides to press it.
          The precedence it returns to and the ID collision that refuses it
          are both outcomes the button alone does not name. */}
      <p className="quiet">
        Restoring puts the layer back at its previous precedence. Where an artifact ID it carries now exists in another
        layer, the restore is refused and names it.
      </p>
    </section>
  );
}

function DeletedRow({
  layer,
  readOnly,
  onRestore,
}: {
  layer: LayerRecord;
  readOnly: boolean;
  onRestore: (id: string) => void;
}) {
  const window = recoveryWindow(layer);
  return (
    <tr>
      <td className="mono">{layer.ID}</td>
      <td className="source-col">
        <SourceCell layer={layer} />
      </td>
      <td className="mono quiet">{window === null ? 'unreported' : window.unregistered}</td>
      <td>
        {window === null ? (
          // The record carries no tombstone time, so the row states that
          // rather than computing a date from a value it does not hold.
          <span className="quiet">The registry reported no erase date.</span>
        ) : (
          <>
            {/* The date, the count, and the bar carry the same urgency, so a
                row about to be erased reads as one accented unit rather than
                as a bar that is accent on every row. */}
            <span className={window.urgent ? 'mono accent' : 'mono'}>{window.erases}</span>{' '}
            <span className={window.urgent ? 'accent' : 'quiet'} data-testid={`days-left-${layer.ID}`}>
              {window.left} days left
            </span>
            <span
              className={window.urgent ? 'depleting depleting-urgent' : 'depleting'}
              role="presentation"
              style={{ ['--remaining' as string]: `${String(window.remaining)}%` }}
            />
          </>
        )}
      </td>
      <td>
        <button
          type="button"
          disabled={readOnly}
          onClick={() => {
            onRestore(layer.ID);
          }}
        >
          Restore
        </button>
      </td>
    </tr>
  );
}

interface RecoveryWindow {
  unregistered: string;
  erases: string;
  left: number;
  /** remaining is how much of the window is left, as a percentage, which is
   * what the depleting bar draws. */
  remaining: number;
  /** urgent is whether the window is inside the accent threshold, which the
   * date, the count, and the bar all read to decide their tone. */
  urgent: boolean;
}

/** recoveryWindow derives the row's dates from the tombstone the record
 * carries, or null where it carries none. */
function recoveryWindow(layer: LayerRecord): RecoveryWindow | null {
  if (layer.DeletedAt === undefined || layer.DeletedAt === null || layer.DeletedAt === '') {
    return null;
  }
  const at = new Date(layer.DeletedAt);
  if (Number.isNaN(at.getTime())) {
    return null;
  }
  const left = daysLeft(at, Date.now());
  return {
    unregistered: at.toISOString().slice(0, 10),
    erases: erasesOn(at),
    left,
    remaining: Math.round((left / recoveryDays) * 100),
    urgent: left <= accentDays,
  };
}
