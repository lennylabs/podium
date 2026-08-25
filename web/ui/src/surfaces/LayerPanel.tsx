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
import { RegisterLayerForm } from './RegisterLayerForm';
import { ReingestControl } from './ReingestControl';
import { UpdateLayerForm } from './UpdateLayerForm';
import { Badge, Banner, EmptyState, ErrorState, Loading } from '../components/primitives';
import type { LayerRecord } from '../api';
import { ApiError, listLayers, reorderLayers, unregisterLayer } from '../api';
import { useAsync } from '../useAsync';

/** recoveryDays is the window an unregistered layer stays restorable for
 * (§8.4). The confirmation states it with the date it runs out, because a
 * window with no date leaves the reader to work out what it means. */
const recoveryDays = 30;

export function LayerPanel({ subject, readOnly }: { subject: string; readOnly: boolean }) {
  const layers = useAsync(() => listOrdered(), []);
  const [registering, setRegistering] = useState(false);
  const [showingDeleted, setShowingDeleted] = useState(false);

  if (layers.loading) {
    return <Loading label="Loading the layers." />;
  }
  if (layers.error !== null) {
    return <ErrorState error={layers.error} onRetry={layers.reload} />;
  }
  const rows = layers.value ?? [];

  return (
    <section className="surface" aria-label="Layer panel">
      <h1>Layers</h1>
      {/* §13.2.1 marks a read-only registry on its read responses, so the
          state is presented once here and every write control is unavailable
          at once rather than each one failing when it is pressed. */}
      {readOnly && (
        <Banner tone="danger">
          <span data-testid="read-only-banner">
            The registry is temporarily read-only, so no layer can be changed right now. Browsing and search still
            work.
          </span>
        </Banner>
      )}
      <div className="panel-actions">
        <button
          type="button"
          disabled={readOnly}
          onClick={() => {
            setRegistering((open) => !open);
          }}
        >
          Register layer
        </button>
        <button
          type="button"
          onClick={() => {
            setShowingDeleted((open) => !open);
          }}
        >
          Recently unregistered
        </button>
      </div>
      {registering && <RegisterLayerForm subject={subject} onRegistered={layers.reload} readOnly={readOnly} />}
      {showingDeleted && <DeletedLayers onRestored={layers.reload} readOnly={readOnly} />}
      <p className="label quiet">Precedence: the last row wins</p>
      <p className="quiet">
        Every user-defined layer composes above every admin-defined layer, so a row moves within its own block.
      </p>
      {rows.length === 0 ? (
        <EmptyState>No layers are registered under this tenant.</EmptyState>
      ) : (
        <table className="data-table layer-table">
          <thead>
            <tr>
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
                onWrite={layers.reload}
                block={blockOf(rows, layer)}
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
function blockOf(rows: LayerRecord[], layer: LayerRecord): LayerRecord[] {
  return rows.filter((row) => (row.UserDefined === true) === (layer.UserDefined === true));
}

/** raisedOrder returns the block's resulting order after a row trades places
 * with the row below it, or null where the row is already at the winning end.
 *
 * The reorder endpoint assigns each layer the request names an absolute order
 * value taken from its position in the request rather than swapping two
 * stored values. A request naming the traded pair alone therefore stamps the
 * block's first two order values onto that pair and leaves every other row of
 * the block holding the value it already had, which ties or inverts rows the
 * move was not meant to touch. The request names the whole block so the
 * endpoint's positional assignment reproduces the order the panel displayed.
 *
 * Every layer the request names is authorized on its own under the §7.3.1
 * layer-write rule, and the list read is unfiltered, so a block holding a
 * layer this caller may not write has its move refused whole. The panel
 * presents that refusal on the row rather than predicting it. */
function raisedOrder(block: LayerRecord[], layer: LayerRecord): string[] | null {
  const index = block.findIndex((row) => row.ID === layer.ID);
  if (index < 0 || index + 1 >= block.length) {
    return null;
  }
  const order = block.map((row) => row.ID);
  order[index] = block[index + 1].ID;
  order[index + 1] = layer.ID;
  return order;
}

function LayerRow({
  layer,
  subject,
  readOnly,
  onWrite,
  block,
}: {
  layer: LayerRecord;
  subject: string;
  readOnly: boolean;
  onWrite: () => void;
  block: LayerRecord[];
}) {
  // Raising precedence trades places with the next row of the same class.
  // The request carries the block's resulting order, because the endpoint
  // rewrites the order value of every layer it names from that layer's
  // position in the request.
  const raised = raisedOrder(block, layer);
  const [refusal, setRefusal] = useState<unknown>(null);
  const [confirming, setConfirming] = useState(false);
  const [editing, setEditing] = useState(false);

  // A write the panel sends can come back refused, including on a row the
  // panel presented as this caller's to manage. The refusal is drawn on the
  // row and says only that the registry refused that action and that nothing
  // changed. It reports neither who owns the layer nor the state of the
  // session, because the refusal carries neither.
  const attempt = (run: () => Promise<unknown>) => {
    run().then(
      () => {
        setRefusal(null);
        onWrite();
      },
      (err: unknown) => {
        setRefusal(err);
      },
    );
  };

  return (
    <tr>
      <td className="mono">
        {layer.ID}
        {ownedByCaller(layer, subject) && <Badge tone="accent">yours</Badge>}
        {layer.UserDefined !== true && <Badge tone="quiet">admin-defined</Badge>}
      </td>
      <td>
        <SourceCell layer={layer} />
      </td>
      <td>
        <VisibilityCell layer={layer} />
      </td>
      <td className="mono quiet">{layer.last_ingested_at ?? 'never'}</td>
      <td>
        <button
          type="button"
          disabled={readOnly || raised === null}
          onClick={() => {
            if (raised !== null) {
              attempt(() => reorderLayers(raised));
            }
          }}
        >
          Raise precedence
        </button>
        <button
          type="button"
          disabled={readOnly}
          onClick={() => {
            setEditing((open) => !open);
          }}
        >
          Edit
        </button>
        <ReingestControl
          layerID={layer.ID}
          readOnly={readOnly}
          onIngested={() => {
            setRefusal(null);
            onWrite();
          }}
          onRefusal={setRefusal}
        />
        <button
          type="button"
          disabled={readOnly}
          onClick={() => {
            setConfirming(true);
          }}
        >
          Unregister
        </button>
        {editing && (
          <UpdateLayerForm
            layer={layer}
            readOnly={readOnly}
            onUpdated={() => {
              setRefusal(null);
              onWrite();
            }}
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
          <p className="row-refusal" role="alert">
            The registry refused that action and nothing changed.{' '}
            <span className="mono">{refusal instanceof ApiError ? refusal.code : 'registry.unavailable'}</span>
          </p>
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
        The layer is recoverable for {recoveryDays} days, until {erasesOn()}, after which it is erased.
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

/** erasesOn returns the date the recovery window runs out, as the calendar
 * date the reader reads rather than as a duration they have to add up. */
function erasesOn(): string {
  const at = new Date(Date.now() + recoveryDays * 24 * 60 * 60 * 1000);
  return at.toISOString().slice(0, 10);
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

function SourceCell({ layer }: { layer: LayerRecord }) {
  if (layer.SourceType === 'git') {
    return (
      <span className="mono">
        {layer.Repo}
        {layer.Ref !== undefined && layer.Ref !== '' ? `@${layer.Ref}` : ''}
      </span>
    );
  }
  if (layer.SourceType === 'local') {
    return <span className="mono">{layer.LocalPath}</span>;
  }
  // The source type is pluggable, so a type this panel has not seen renders
  // as its name rather than as a broken row.
  return <Badge tone="quiet">{layer.SourceType}</Badge>;
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
    return <span className="quiet">no grants</span>;
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
