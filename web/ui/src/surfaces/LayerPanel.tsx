// The layer panel: the only surface with write operations. The list read
// hands the panel every layer stored under the tenant and no response reports
// that the caller holds the administrator role, so the panel predicts no
// outcome. It renders its write operations on every row and presents whatever
// refusal a write receives.
//
// The panel is rendered for every caller on every deployment, including a
// caller who resolves no subject. A standalone registry authenticates nobody
// and treats the local operator as the administrator, and the panel is the
// point of that deployment.

import { useState } from 'react';

import { DeletedLayers } from './DeletedLayers';
import { erasesOn, recoveryDays } from './recovery';
import { RegisterLayerForm } from './RegisterLayerForm';
import type { ReingestState } from './ReingestControl';
import { idleReingest, ReingestButton, ReingestStatus, reingestRefusal } from './ReingestControl';
import { UpdateLayerForm } from './UpdateLayerForm';
import { Badge, Banner, EmptyState, ErrorState, Loading } from '../components/primitives';
import { SourceCell } from '../components/SourceCell';
import type { BreakGlass, LayerRecord } from '../api';
import { ApiError, listDeletedLayers, listLayers, reingestLayer, reorderLayers, unregisterLayer } from '../api';
import { useAsync } from '../useAsync';

/** Refusal is a write the registry refused, held with the write itself so the
 * row can re-issue exactly what was attempted. */
interface Refusal {
  error: unknown;
  retry: () => void;
}

export function LayerPanel({ subject, readOnly }: { subject: string; readOnly: boolean }) {
  const layers = useAsync(() => listOrdered(), []);
  // The recoverable list is read for its count alone here. The section it
  // opens reads the list itself, so the panel holds no copy of what that
  // section renders.
  const recoverable = useAsync(() => listDeletedLayers(), []);
  const [registering, setRegistering] = useState(false);
  const [showingDeleted, setShowingDeleted] = useState(false);
  // A write's refusal is drawn on the row it was attempted on. The panel
  // holds the map because a reorder is committed by a drop on another row
  // and its refusal belongs to the row that moved.
  const [refusals, setRefusals] = useState<Record<string, Refusal | null>>({});
  // Each row's reingest state is driven by that row's own response, which is
  // what lets the fan-out leave every row it has not heard from untouched.
  const [reingest, setReingest] = useState<Record<string, ReingestState>>({});
  const [dragging, setDragging] = useState<string | null>(null);
  const [over, setOver] = useState<string | null>(null);

  // The loading state stands in for the panel on the first read alone. A
  // write reloads the list, and the reload reports loading again, so swapping
  // the whole panel out here would unmount the form that issued the write and
  // discard the one-time webhook secret its response carried. The panel holds
  // the rows it already has until the reload answers.
  if (layers.loading && layers.value === null) {
    return <Loading label="Loading the layers." />;
  }
  if (layers.error !== null) {
    return <ErrorState error={layers.error} onRetry={layers.reload} />;
  }
  const rows = layers.value ?? [];

  const recordRefusal = (id: string, err: unknown, retry: () => void) => {
    setRefusals((prev) => ({ ...prev, [id]: { error: err, retry } }));
  };
  const clearRefusal = (id: string) => {
    setRefusals((prev) => ({ ...prev, [id]: null }));
  };
  const setRowReingest = (id: string, state: ReingestState) => {
    setReingest((prev) => ({ ...prev, [id]: state }));
  };

  /** runReingest drives one layer's reingest and moves that layer's row
   * alone. The pipeline runs inside the request, so nothing is reported
   * until it returns and the row shows what its own response carried. */
  const runReingest = async (id: string, breakGlass?: BreakGlass): Promise<void> => {
    setRowReingest(id, { kind: 'running' });
    try {
      const summary = await reingestLayer(id, breakGlass);
      setRowReingest(id, { kind: 'summary', summary });
      clearRefusal(id);
      layers.reload();
    } catch (err: unknown) {
      setRowReingest(id, reingestRefusal(err));
    }
  };

  /** reingestAll is the fan-out. It issues one request per layer in
   * sequence, so a row changes only when its own request returns and no row
   * shows progress the registry has not reported. */
  const reingestAll = async (): Promise<void> => {
    for (const row of rows) {
      await runReingest(row.ID);
    }
  };

  const commitMove = (from: string, onto: string) => {
    setDragging(null);
    setOver(null);
    const order = movedOrder(blockOf(rows, from), from, onto);
    if (order === null) {
      return;
    }
    const send = () => {
      reorderLayers(order).then(
        () => {
          clearRefusal(from);
          layers.reload();
        },
        (err: unknown) => {
          recordRefusal(from, err, send);
        },
      );
    };
    send();
  };

  return (
    <section className="surface" aria-label="Layer panel">
      <h1>Layers</h1>
      {/* §13.2.1 marks a read-only registry on its read responses, so the
          state is presented once here and every write control is unavailable
          at once rather than each one failing when it is pressed. */}
      {readOnly && (
        <Banner tone="danger">
          <span data-testid="read-only-banner">
            Something went wrong — the registry is temporarily read-only. Browsing and search still work.
          </span>
        </Banner>
      )}
      <div className="panel-actions">
        {/* The recoverable link leads the row and states how much is still
            restorable, because that count is the one piece of panel state
            naming something on its way to being erased. */}
        <button
          type="button"
          className="link-action"
          data-testid="recoverable-link"
          onClick={() => {
            setShowingDeleted((open) => !open);
          }}
        >
          ↺ Recently unregistered
          {recoverable.value === null ? '' : ` · ${String(recoverable.value.length)}`}
        </button>
        <button
          type="button"
          className="button primary"
          disabled={readOnly}
          onClick={() => {
            setRegistering((open) => !open);
          }}
        >
          Register layer
        </button>
        <button
          type="button"
          disabled={readOnly || rows.length === 0}
          onClick={() => {
            void reingestAll();
          }}
        >
          Reingest all
        </button>
      </div>
      {registering && (
        <RegisterLayerForm
          subject={subject}
          onRegistered={layers.reload}
          onClose={() => {
            setRegistering(false);
          }}
          readOnly={readOnly}
        />
      )}
      {showingDeleted && (
        <DeletedLayers
          onRestored={() => {
            layers.reload();
            recoverable.reload();
          }}
          readOnly={readOnly}
        />
      )}
      <p className="label quiet">Precedence — drag to reorder</p>
      <p className="quiet">
        The last row wins. Every user-defined layer composes above every admin-defined layer, so a row moves within its
        own block.
      </p>
      {rows.length === 0 ? (
        <EmptyState>No layers are registered under this tenant.</EmptyState>
      ) : (
        <table className="data-table layer-table">
          <thead>
            <tr>
              <th className="drag-cell">
                <span className="label">Move</span>
              </th>
              <th>Layer</th>
              <th>Source</th>
              <th>Visibility</th>
              <th>Last ingest</th>
              <th>Actions</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((layer) => (
              <LayerRow
                key={layer.ID}
                layer={layer}
                subject={subject}
                readOnly={readOnly}
                refusal={refusals[layer.ID] ?? null}
                reingest={reingest[layer.ID] ?? idleReingest}
                dragging={dragging === layer.ID}
                over={over === layer.ID}
                onDragStart={() => {
                  setDragging(layer.ID);
                }}
                onDragOver={() => {
                  setOver(layer.ID);
                }}
                onDrop={() => {
                  if (dragging !== null) {
                    commitMove(dragging, layer.ID);
                  }
                }}
                onDragEnd={() => {
                  setDragging(null);
                  setOver(null);
                }}
                onReingest={(breakGlass) => {
                  void runReingest(layer.ID, breakGlass);
                }}
                onDismissReingest={() => {
                  setRowReingest(layer.ID, idleReingest);
                }}
                onWrite={() => {
                  clearRefusal(layer.ID);
                  layers.reload();
                  // An unregister moves the layer into the recoverable list,
                  // so the count the header states is re-read on every write
                  // rather than only on the one that reopens the section.
                  recoverable.reload();
                }}
                onRefusal={(err, retry) => {
                  recordRefusal(layer.ID, err, retry);
                }}
                onDismissRefusal={() => {
                  clearRefusal(layer.ID);
                }}
              />
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}

/** listOrdered reads the layer list in the order §4.6 composes it in. The
 * order value sets precedence within a class, and the composition rule places
 * every user-defined layer above every admin-defined layer whatever the stored
 * order values are, so the rows are grouped by class first and sorted by order
 * inside each group. Sorting the whole list by order alone would name the
 * wrong winning row on any tenant whose most recently registered layer is
 * admin-defined, because registration hands each new layer the highest
 * existing order. */
async function listOrdered(): Promise<LayerRecord[]> {
  const layers = await listLayers();
  const byOrder = (a: LayerRecord, b: LayerRecord) => a.Order - b.Order;
  const admin = layers.filter((layer) => layer.UserDefined !== true).sort(byOrder);
  const user = layers.filter((layer) => layer.UserDefined === true).sort(byOrder);
  return [...admin, ...user];
}

/** blockOf returns the contiguous run of rows a layer shares its class with,
 * which is the run a reorder may move it inside. A move across the class
 * boundary changes no composition order, because §4.6 composes every
 * user-defined layer above every admin-defined one whatever the stored order
 * values are, so the block bounds both where the control can take a row and
 * what the request names. */
function blockOf(rows: LayerRecord[], id: string): LayerRecord[] {
  const moving = rows.find((row) => row.ID === id);
  if (moving === undefined) {
    return [];
  }
  return rows.filter((row) => (row.UserDefined === true) === (moving.UserDefined === true));
}

/** movedOrder returns the block's resulting order after the dragged row is
 * dropped onto another row of the same class, or null where the drop names no
 * move the block can make.
 *
 * The reorder endpoint assigns each layer the request names an absolute order
 * value taken from its position in the request rather than swapping two
 * stored values. A request naming the moved pair alone therefore stamps the
 * block's first order values onto that pair and leaves every other row of the
 * block holding the value it already had, which ties or inverts rows the move
 * was not meant to touch. The request names the whole block so the endpoint's
 * positional assignment reproduces the order the panel displayed.
 *
 * Every layer the request names is authorized on its own under the §7.3.1
 * layer-write rule, and the list read is unfiltered, so a block holding a
 * layer this caller may not write has its move refused whole. The panel
 * presents that refusal on the row rather than predicting it. */
function movedOrder(block: LayerRecord[], from: string, onto: string): string[] | null {
  const order = block.map((row) => row.ID);
  const at = order.indexOf(from);
  const target = order.indexOf(onto);
  if (at < 0 || target < 0 || at === target) {
    return null;
  }
  order.splice(at, 1);
  order.splice(target, 0, from);
  return order;
}

function LayerRow({
  layer,
  subject,
  readOnly,
  refusal,
  reingest,
  dragging,
  over,
  onDragStart,
  onDragOver,
  onDrop,
  onDragEnd,
  onReingest,
  onDismissReingest,
  onWrite,
  onRefusal,
  onDismissRefusal,
}: {
  layer: LayerRecord;
  subject: string;
  readOnly: boolean;
  refusal: Refusal | null;
  reingest: ReingestState;
  dragging: boolean;
  over: boolean;
  onDragStart: () => void;
  onDragOver: () => void;
  onDrop: () => void;
  onDragEnd: () => void;
  onReingest: (breakGlass?: BreakGlass) => void;
  onDismissReingest: () => void;
  onWrite: () => void;
  onRefusal: (err: unknown, retry: () => void) => void;
  onDismissRefusal: () => void;
}) {
  const [confirming, setConfirming] = useState(false);
  const [editing, setEditing] = useState(false);
  const [overflowOpen, setOverflowOpen] = useState(false);

  // A write the panel sends can come back refused, including on a row the
  // panel presented as this caller's to manage. The refusal is drawn on the
  // row and says only that the registry refused that action and that nothing
  // changed. It reports neither who owns the layer nor the state of the
  // session, because the refusal carries neither.
  // The refusal carries the write beside it, so Try again re-issues exactly
  // the action that was refused rather than a fresh guess at it.
  const attempt = (run: () => Promise<unknown>) => {
    run().then(onWrite, (err: unknown) => {
      onRefusal(err, () => {
        attempt(run);
      });
    });
  };

  const rowClass = [dragging ? 'row-dragging' : '', over ? 'row-drop-target' : ''].filter((name) => name !== '');

  return (
    <tr
      className={rowClass.join(' ')}
      draggable={!readOnly}
      onDragStart={onDragStart}
      onDragOver={(event) => {
        // A row that does not cancel the drag-over event is not a drop
        // target, so the drop never fires and the move is silently lost.
        event.preventDefault();
        onDragOver();
      }}
      onDrop={(event) => {
        event.preventDefault();
        onDrop();
      }}
      onDragEnd={onDragEnd}
    >
      <td className="drag-cell">
        <span className="drag-handle" aria-label={`Drag ${layer.ID} to reorder`} role="img">
          ⋮⋮
        </span>
      </td>
      <td className="mono">
        {layer.ID}
        {ownedByCaller(layer, subject) && <Badge tone="accent">yours</Badge>}
        {layer.UserDefined !== true && (
          <>
            <Badge tone="quiet">admin-defined</Badge>
            {/* The stored owner of an admin-defined layer is a
                caller-supplied field naming no authorized subject, so the row
                states it as the field it is: no ownership language and none
                of the marker's styling. */}
            <span className="quiet stored-owner" data-testid="stored-owner">
              owner: {layer.Owner === undefined || layer.Owner === '' ? 'unset' : layer.Owner}
            </span>
          </>
        )}
      </td>
      <td>
        <SourceCell layer={layer} />
      </td>
      <td>
        <VisibilityCell layer={layer} />
      </td>
      <td className="mono quiet">{layer.last_ingested_at ?? 'never'}</td>
      <td className="row-actions">
        {/* The actions column is fixed width and every row shares one grid,
            so the bar carries the one action a reader reaches for on a row,
            and the rest sit behind the overflow control. Stacking all three
            controls tripled the height of every row. */}
        <div className="row-action-bar">
          <ReingestButton state={reingest} readOnly={readOnly} onStart={onReingest} />
          <button
            type="button"
            className="row-overflow"
            aria-label={`More actions for ${layer.ID}`}
            aria-expanded={overflowOpen}
            onClick={() => {
              setOverflowOpen((open) => !open);
            }}
          >
            ⋯
          </button>
        </div>
        {overflowOpen && (
          <div className="row-menu" aria-label={`More actions for ${layer.ID}`}>
            <button
              type="button"
              disabled={readOnly}
              onClick={() => {
                setOverflowOpen(false);
                setEditing((open) => !open);
              }}
            >
              Edit
            </button>
            <button
              type="button"
              disabled={readOnly}
              onClick={() => {
                setOverflowOpen(false);
                setConfirming(true);
              }}
            >
              Unregister
            </button>
          </div>
        )}
        <ReingestStatus layerID={layer.ID} state={reingest} onStart={onReingest} onDismiss={onDismissReingest} />
        {editing && (
          <UpdateLayerForm
            layer={layer}
            readOnly={readOnly}
            onUpdated={onWrite}
            onClose={() => {
              setEditing(false);
            }}
          />
        )}
        {confirming && (
          <UnregisterConfirmation
            layer={layer}
            onCancel={() => {
              setConfirming(false);
            }}
            onConfirm={() => {
              setConfirming(false);
              attempt(() => unregisterLayer(layer.ID));
            }}
          />
        )}
        {refusal !== null && (
          <div className="row-refusal" role="alert">
            <p>
              The registry refused that action and nothing changed.{' '}
              <span className="mono">
                {refusal.error instanceof ApiError ? refusal.error.code : 'registry.unavailable'}
              </span>
            </p>
            {/* The refusal is cleared by re-issuing the write or by
                dismissing it. Every other control on the row stays live. */}
            <button type="button" onClick={refusal.retry}>
              Try again
            </button>
            <button type="button" onClick={onDismissRefusal}>
              Dismiss
            </button>
          </div>
        )}
      </td>
    </tr>
  );
}

/**
 * UnregisterConfirmation gates the one write whose effect reaches callers
 * who never touched this panel. It states both halves of what unregistering
 * does: the layer's artifacts leave every caller's view at the next sync,
 * and the layer stays restorable until the recovery window runs out. The
 * write is issued only once the reader has typed the layer's own ID, so the
 * action cannot be taken by a single press on the row it sits in.
 */
function UnregisterConfirmation({
  layer,
  onCancel,
  onConfirm,
}: {
  layer: LayerRecord;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const [typed, setTyped] = useState('');
  return (
    <div className="confirm" role="dialog" aria-label="Unregister a layer">
      <p className="banner-title">Unregister {layer.ID}?</p>
      <p>Its artifacts disappear from every caller&rsquo;s view the next time they sync.</p>
      <p>
        The layer is recoverable for {recoveryDays} days, until {erasesOn(new Date())}, after which it is erased.
      </p>
      <label className="field">
        <span className="label">Type the layer ID to confirm</span>
        <input
          type="text"
          value={typed}
          onChange={(event) => {
            setTyped(event.target.value);
          }}
        />
      </label>
      <button type="button" disabled={typed !== layer.ID} onClick={onConfirm}>
        Unregister layer
      </button>
      <button type="button" onClick={onCancel}>
        Cancel
      </button>
    </div>
  );
}

/** ownedByCaller is the panel's ownership marker. It is a property of a
 * user-defined row alone: on such a row it compares the row's stored owner
 * against the caller's own subject, and the posture read reports a subject
 * only where one resolves, so a caller with no subject carries no marker on
 * any row. An admin-defined row carries no marker on any value of its stored
 * owner, because the write rule authorizes a tenant admin alone there and
 * that owner names no authorized subject. */
function ownedByCaller(layer: LayerRecord, subject: string): boolean {
  return layer.UserDefined === true && subject !== '' && layer.Owner === subject;
}

/** VisibilityCell renders one marker per matching axis, in the fixed order
 * public, organization, groups, then users, because §4.6 defines visibility
 * as independent grants that combine as a union. Two layers carrying the same
 * grants therefore read identically, and no axis is dropped. */
function VisibilityCell({ layer }: { layer: LayerRecord }) {
  const groups = layer.Groups ?? [];
  const users = layer.Users ?? [];
  const markers = [
    layer.Public === true ? 'public' : '',
    layer.Organization === true ? 'organization' : '',
    groups.length > 0 ? `groups: ${summarize(groups)}` : '',
    users.length > 0 ? `users: ${summarize(users)}` : '',
  ].filter((marker) => marker !== '');
  if (markers.length === 0) {
    return <span className="quiet">no grants — only you</span>;
  }
  return (
    <>
      {markers.map((marker) => (
        <Badge key={marker} tone="quiet">
          {marker}
        </Badge>
      ))}
    </>
  );
}

/** summarize keeps an axis that names more members than the row can hold
 * inside its own marker, so the axis stays visible and the count is not
 * dropped to make room. */
function summarize(members: string[]): string {
  const shown = members.slice(0, 2).join(' · ');
  return members.length > 2 ? `${shown} +${String(members.length - 2)}` : shown;
}
