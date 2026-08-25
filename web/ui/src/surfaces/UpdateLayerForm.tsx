// Updating a layer. The update is a partial patch, so a field left as the
// stored value is sent unchanged and the registry keeps it. The visibility
// axes are offered on an admin-defined layer, which is the class the endpoint
// applies them to. §4.6 fixes a user-defined layer's visibility at
// registration, and the registry ignores an owner or a visibility patch there
// and still answers success, so that class displays its visibility rather
// than editing it.

import type { FormEvent } from 'react';
import { useState } from 'react';

import { SecretReveal } from './SecretReveal';
import { members } from './members';
import { Badge, ErrorState } from '../components/primitives';
import type { LayerRecord, LayerSecretResult, LayerUpdate } from '../api';
import { updateLayer } from '../api';

export function UpdateLayerForm({
  layer,
  readOnly,
  onUpdated,
  onClose,
}: {
  layer: LayerRecord;
  readOnly: boolean;
  onUpdated: () => void;
  onClose: () => void;
}) {
  const git = layer.SourceType === 'git';
  const [ref, setRef] = useState(layer.Ref ?? '');
  const [root, setRoot] = useState(layer.Root ?? '');
  const [localPath, setLocalPath] = useState(layer.LocalPath ?? '');
  const [policy, setPolicy] = useState(layer.force_push_policy ?? 'tolerant');
  const [rotate, setRotate] = useState(false);
  const editableVisibility = layer.UserDefined !== true;
  const [isPublic, setPublic] = useState(layer.Public === true);
  const [organization, setOrganization] = useState(layer.Organization === true);
  const [groups, setGroups] = useState((layer.Groups ?? []).join(', '));
  const [users, setUsers] = useState((layer.Users ?? []).join(', '));
  const [result, setResult] = useState<LayerSecretResult | null>(null);
  const [refusal, setRefusal] = useState<unknown>(null);

  // send is held apart from the form's submit handler so a refused patch can
  // be re-issued from the refusal itself, which is the treatment every other
  // refused write on the panel carries.
  const send = () => {
    const patch: LayerUpdate = git
      ? { ref, root, force_push_policy: policy, rotate_webhook_secret: rotate }
      : { local_path: localPath, root };
    if (editableVisibility) {
      // Each axis the patch carries grants, and an axis it omits keeps its
      // stored value, so the form sends an axis the reader turned on and a
      // member list they named.
      patch.public = isPublic;
      patch.organization = organization;
      patch.groups = members(groups);
      patch.users = members(users);
    }
    updateLayer(layer.ID, patch).then(
      (next) => {
        setRefusal(null);
        setResult(next);
        onUpdated();
      },
      (err: unknown) => {
        setResult(null);
        setRefusal(err);
      },
    );
  };

  const submit = (event: FormEvent) => {
    event.preventDefault();
    send();
  };

  if (result !== null) {
    // A rotation returns the fresh secret once, on the same terms as
    // registration, so the reveal is the one the register flow uses.
    return (
      <SecretReveal
        result={result}
        outcome={`Layer ${layer.ID} is updated.`}
        onDone={() => {
          setResult(null);
          onClose();
        }}
      />
    );
  }

  return (
    <form className="register-form" aria-label={`Update ${layer.ID}`} onSubmit={submit}>
      {git ? (
        <>
          <label className="field">
            <span className="label">Ref</span>
            <input
              type="text"
              value={ref}
              onChange={(event) => {
                setRef(event.target.value);
              }}
            />
          </label>
          <label className="field">
            <span className="label">Force-push policy</span>
            <select
              value={policy}
              onChange={(event) => {
                setPolicy(event.target.value);
              }}
            >
              <option value="tolerant">Tolerant: ingest a rewritten history</option>
              <option value="strict">Strict: reject a rewritten history</option>
            </select>
          </label>
        </>
      ) : (
        <label className="field">
          <span className="label">Local path</span>
          <input
            type="text"
            value={localPath}
            onChange={(event) => {
              setLocalPath(event.target.value);
            }}
          />
        </label>
      )}
      <label className="field">
        <span className="label">Root</span>
        <input
          type="text"
          value={root}
          onChange={(event) => {
            setRoot(event.target.value);
          }}
        />
      </label>
      {editableVisibility ? (
        <fieldset className="field">
          <legend className="label">Visibility</legend>
          <label>
            <input
              type="checkbox"
              checked={isPublic}
              disabled={layer.Public === true}
              onChange={(event) => {
                setPublic(event.target.checked);
              }}
            />
            Public
          </label>
          <label>
            <input
              type="checkbox"
              checked={organization}
              disabled={layer.Organization === true}
              onChange={(event) => {
                setOrganization(event.target.checked);
              }}
            />
            Organization
          </label>
          <label className="field">
            <span className="label">Group names, separated by commas</span>
            <input
              type="text"
              value={groups}
              onChange={(event) => {
                setGroups(event.target.value);
              }}
            />
          </label>
          <label className="field">
            <span className="label">User identifiers, separated by commas</span>
            <input
              type="text"
              value={users}
              onChange={(event) => {
                setUsers(event.target.value);
              }}
            />
          </label>
          {/* The endpoint grants on each axis and revokes on none, so a grant
              already stored cannot be taken back here and its control says so
              rather than offering a change the registry would answer success
              to without making. */}
          <p className="quiet">An axis already granted stays granted. Unregister the layer to withdraw it.</p>
        </fieldset>
      ) : (
        <div className="field">
          <span className="label">Visibility</span>
          <Badge tone="quiet">you alone</Badge>
          <p className="quiet">A layer of your own is fixed to you at registration and cannot be widened.</p>
        </div>
      )}
      {/* Only a git source carries a webhook secret, so the control stays on
          the row of a layer that has none and reports why it cannot be
          taken rather than disappearing between source types. */}
      <label>
        <input
          type="checkbox"
          checked={rotate}
          disabled={!git}
          onChange={(event) => {
            setRotate(event.target.checked);
          }}
        />
        Rotate the webhook secret
      </label>
      {!git && <p className="quiet">Only a git layer carries a webhook secret.</p>}
      {rotate && <p className="quiet">The new secret is shown once. The old one stops working immediately.</p>}
      <button type="submit" disabled={readOnly}>
        Save changes
      </button>
      <button type="button" onClick={onClose}>
        Cancel
      </button>
      {refusal !== null && (
        <>
          <ErrorState error={refusal} onRetry={send} />
          <button
            type="button"
            onClick={() => {
              setRefusal(null);
            }}
          >
            Dismiss
          </button>
        </>
      )}
    </form>
  );
}
