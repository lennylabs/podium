// Updating a layer. The update is a partial patch, so a field left as the
// stored value is sent unchanged and the registry keeps it. The form offers
// the source details and the force-push policy and nothing else: the registry
// ignores an owner or a visibility patch on a user-defined layer and still
// answers success, so a control for either would report a change that never
// happened.

import type { FormEvent } from 'react';
import { useState } from 'react';

import { SecretReveal } from './SecretReveal';
import { ErrorState } from '../components/primitives';
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
  const [result, setResult] = useState<LayerSecretResult | null>(null);
  const [refusal, setRefusal] = useState<unknown>(null);

  const submit = (event: FormEvent) => {
    event.preventDefault();
    const patch: LayerUpdate = git
      ? { ref, root, force_push_policy: policy, rotate_webhook_secret: rotate }
      : { local_path: localPath, root };
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
      {refusal !== null && <ErrorState error={refusal} />}
    </form>
  );
}
