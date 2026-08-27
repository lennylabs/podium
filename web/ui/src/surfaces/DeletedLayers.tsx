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

import { useRef, useState } from 'react';

import { accentDays, daysLeft, erasesOn, recoveryDays, unregisteredOn } from './recovery';
import { EmptyState, ErrorState, Loading } from '../components/primitives';
import { layersHref } from '../route';
import { takeFocus } from '../components/focus';
import { SourceCell } from '../components/SourceCell';
import type { LayerRecord } from '../api';
import { ApiError, listDeletedLayers, listLayers, restoreLayer } from '../api';
import { useAsync, useReachReport } from '../useAsync';

export function DeletedLayers({
  onRestored,
  readOnly,
  onReach,
}: {
  onRestored: () => void;
  readOnly: boolean;
  /** onReach tells the shell that this read answered, so a shell read that
   * failed during the same outage is re-issued rather than leaving the
   * sidebar stating an outage this table has come back from. */
  onReach: () => void;
}) {
  const deleted = useAsync(() => listDeletedLayers(), []);
  useReachReport(!deleted.loading && deleted.error === null, onReach);
  const [refusal, setRefusal] = useState<unknown>(null);
  // What the last restore did. A restored row leaves the table, and the empty
  // state that follows reports nothing about the write that emptied it, so
  // the outcome is held here and stated the way the panel states a committed
  // reorder.
  const [restored, setRestored] = useState('');
  // The restored row leaves the table with the button that restored it, so
  // the heading is where focus goes. Left where it was, focus falls to the
  // document body and the reader resumes at the top of the page.
  const heading = useRef<HTMLHeadingElement>(null);

  // The loading state stands in for the surface on the first read alone. A
  // restore re-reads the list, and swapping the whole surface out for the
  // reload unmounts the heading the write hands focus to and the region it
  // reports itself in, which drops both. The surface holds the rows it
  // already has until the reload answers.
  if (deleted.loading && deleted.value === null) {
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
        takeFocus(heading.current);
        // The restore response carries the layer ID alone, so the precedence
        // the layer came back at is read from the layer list, which is the
        // same order the panel one link away displays. A list read that
        // fails reports the restore without a position rather than turning a
        // committed write into a refusal.
        listLayers().then(
          (layers) => {
            setRestored(restoredNote(id, layers));
          },
          () => {
            setRestored(restoredNote(id, null));
          },
        );
      },
      (err: unknown) => {
        setRestored('');
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
      <h1 ref={heading}>Recently unregistered</h1>
      <p className="lead">A layer stays restorable for {recoveryDays} days, after which it is erased.</p>
      {rows.length === 0 ? (
        <EmptyState title="Nothing to erase">
          An unregistered layer waits here until its recovery window ends.
        </EmptyState>
      ) : (
        // The table keeps its designed column widths down to a floor and
        // scrolls sideways inside its own container below that, the way the
        // layer panel's table does. Squeezed below that floor, the
        // "Unregistered" header ran out of its cell and abutted "Erased on",
        // and the header row read as one token. The container is focusable so
        // a keyboard reaches the scroll it owns.
        <div className="table-scroll" tabIndex={0} role="region" aria-label="Recoverable layers">
          <table className="data-table restore-table">
            <thead>
              <tr>
                {/* The layer panel this table sits one link away from labels its
                    own columns in the section-label style, so these carry the
                    same treatment. Sentence-case sans headers here put two
                    tables the reader crosses in one step into two type
                    systems. */}
                <th>
                  <span className="label">Layer</span>
                </th>
                <th>
                  <span className="label">Source</span>
                </th>
                {/* How much comes back on a restore is the second question
                    this surface answers, so the count has a column of its
                    own. No layer read carries it, so every cell states that
                    it is unreported rather than the column being left out:
                    a missing column reads as a datum that does not exist,
                    and an unreported cell reads as one the registry did not
                    send. */}
                <th>
                  <span className="label">Artifacts</span>
                </th>
                <th>
                  <span className="label">Unregistered</span>
                </th>
                <th>
                  <span className="label">Erased on</span>
                </th>
                <th>
                  <span className="label">Actions</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {rows.map((layer) => (
                <DeletedRow key={layer.ID} layer={layer} readOnly={readOnly} onRestore={restore} />
              ))}
            </tbody>
          </table>
        </div>
      )}
      {/* The live region is rendered on every state of the surface, empty
          until a restore lands, and it becomes the visible outcome banner
          when it carries text. A region mounted at the moment its text
          arrives is not in the accessibility tree when the change happens,
          and the announcement is dropped. */}
      <p
        className={restored === '' ? 'assistive-only' : 'banner banner-accent'}
        role="status"
        aria-live="polite"
        data-testid="restore-announcement"
      >
        {restored}
      </p>
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

/** restoredNote states what a committed restore did, in the same terms the
 * layer panel states a committed reorder: the layer, and its position counted
 * down the whole table, which is the precedence order the panel is about. The
 * layer list is the order the catalog composes in, so the restored layer's
 * index in it is the precedence it came back at. A list the read did not
 * return, or one the layer is absent from, leaves the position unstated. */
function restoredNote(id: string, layers: LayerRecord[] | null): string {
  if (layers === null) {
    return `${id} is restored.`;
  }
  const at = layers.findIndex((layer) => layer.ID === id);
  if (at < 0) {
    return `${id} is restored.`;
  }
  return `${id} is restored at order ${String(at + 1)} of ${String(layers.length)}.`;
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
      {/* The layer read the surface is built on carries no artifact count on
          an active or a tombstoned layer, so the cell states the datum is
          unreported the way the unregistered date does where the record
          carries no tombstone time. */}
      <td className="mono quiet" data-testid={`artifact-count-${layer.ID}`}>
        unreported
      </td>
      <td className="mono quiet">{window === null ? 'unreported' : window.unregistered}</td>
      <td>
        {window === null ? (
          // The record carries no tombstone time, so the row states that
          // rather than computing a date from a value it does not hold.
          <span className="quiet">The registry reported no erase date.</span>
        ) : (
          // The cell is a clock: the deadline, how much of the window is
          // left drawn between them, and the count. Stacked, the bar sat
          // directly under the date and read as an underline of it rather
          // than as a gauge of anything.
          <span className="erase-clock">
            {/* The date, the count, and the bar carry the same urgency, so a
                row about to be erased reads as one accented unit rather than
                as a bar that is accent on every row. */}
            <span className={window.urgent ? 'mono accent' : 'mono'}>{window.erases}</span>
            <span
              className={window.urgent ? 'depleting depleting-urgent' : 'depleting'}
              role="presentation"
              style={{ ['--remaining' as string]: `${String(window.remaining)}%` }}
            />
            {/* The count is compact enough to sit inside the column beside
                the bar, and the phrase it stands for is carried for a reader
                who hears the row rather than sees the gauge. */}
            <span
              className={window.urgent ? 'mono accent' : 'mono quiet'}
              data-testid={`days-left-${layer.ID}`}
              aria-hidden="true"
            >
              {window.left}d{window.urgent ? ' left' : ''}
            </span>
            <span className="assistive-only">{window.left} days left</span>
          </span>
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
  const now = Date.now();
  const left = daysLeft(at, now);
  return {
    unregistered: unregisteredOn(at, now),
    erases: erasesOn(at),
    left,
    remaining: Math.round((left / recoveryDays) * 100),
    urgent: left <= accentDays,
  };
}
