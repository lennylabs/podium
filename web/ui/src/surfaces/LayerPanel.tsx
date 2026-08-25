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
import { Badge, EmptyState, ErrorState, Loading } from '../components/primitives';
import type { LayerRecord } from '../api';
import { ApiError, listLayers, reingestLayer, reorderLayers, unregisterLayer } from '../api';
import { useAsync } from '../useAsync';

export function LayerPanel({ subject }: { subject: string }) {
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
  const order = rows.map((layer) => layer.ID);

  return (
    <section className="surface" aria-label="Layer panel">
      <h1>Layers</h1>
      <div className="panel-actions">
        <button
          type="button"
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
      {registering && <RegisterLayerForm onRegistered={layers.reload} />}
      {showingDeleted && <DeletedLayers onRestored={layers.reload} />}
      <p className="label quiet">Precedence: the last row wins</p>
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
            {rows.map((layer, index) => (
              <LayerRow
                key={layer.ID}
                layer={layer}
                subject={subject}
                onWrite={layers.reload}
                order={order}
                index={index}
              />
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}

/** listOrdered reads the layer list in precedence order. The order value sets
 * precedence within the tenant and the read does not promise an order, so the
 * panel sorts by it rather than by arrival. */
async function listOrdered(): Promise<LayerRecord[]> {
  const layers = await listLayers();
  return [...layers].sort((a, b) => a.Order - b.Order);
}

function LayerRow({
  layer,
  subject,
  onWrite,
  order,
  index,
}: {
  layer: LayerRecord;
  subject: string;
  onWrite: () => void;
  order: string[];
  index: number;
}) {
  const [refusal, setRefusal] = useState<unknown>(null);

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
      <td className="mono quiet">{layer.LastIngestedAt ?? 'never'}</td>
      <td>
        <button
          type="button"
          disabled={index === order.length - 1}
          onClick={() => {
            attempt(() => reorderLayers(moved(order, index, index + 1)));
          }}
        >
          Raise precedence
        </button>
        <button
          type="button"
          onClick={() => {
            attempt(() => reingestLayer(layer.ID));
          }}
        >
          Reingest
        </button>
        <button
          type="button"
          onClick={() => {
            attempt(() => unregisterLayer(layer.ID));
          }}
        >
          Unregister
        </button>
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

/** moved returns the whole order with one layer at a new position, which is
 * what the reorder call takes. */
function moved(order: string[], from: number, to: number): string[] {
  const next = [...order];
  const [layer] = next.splice(from, 1);
  next.splice(to, 0, layer);
  return next;
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
