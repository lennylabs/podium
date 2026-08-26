// The one-time webhook-secret reveal. A git layer's HMAC secret is returned
// on registration and on a rotation and nowhere else, so both entry points
// present it through this component and the wording that it is unrecoverable
// is stated once.

import { useState } from 'react';

import { Banner, CopyField } from '../components/primitives';
import type { LayerSecretResult } from '../api';

/**
 * revealsSecret reports whether a response carries a credential the reader
 * has to take away. The register and rotate flows ask before they present
 * the reveal, because a response that carries no secret leaves nothing to
 * acknowledge and the dialog around it stays ordinarily dismissible.
 */
export function revealsSecret(result: LayerSecretResult): boolean {
  return result.webhook_secret !== undefined && result.webhook_secret !== '';
}

/**
 * SecretReveal draws the credential the registry returned once. A response
 * that carries no secret (a local-path source, or an update with no rotation)
 * carries no reveal, so the component renders the outcome banner instead and
 * the caller does not have to branch on the response itself.
 */
export function SecretReveal({
  result,
  outcome,
  onDone,
}: {
  result: LayerSecretResult;
  outcome: string;
  onDone: () => void;
}) {
  const [acknowledged, setAcknowledged] = useState(false);
  if (!revealsSecret(result)) {
    return <Banner tone="accent">{outcome}</Banner>;
  }
  return (
    <div className="secret-reveal" aria-label="Webhook secret">
      <p className="label">Shown once</p>
      <p>
        The webhook URL is permanent. The secret is returned here and on a rotation, and the registry stores only a
        hash of it.
      </p>
      {/* Both values are here to be taken away, so each carries its own
          copy control. The secret is never served again, so a reader who
          leaves without it has to rotate the secret to get another. */}
      <CopyField label="Webhook URL" value={result.webhook_url ?? ''} />
      <CopyField label="Webhook secret" value={result.webhook_secret} />
      <label>
        <input
          type="checkbox"
          checked={acknowledged}
          onChange={(event) => {
            setAcknowledged(event.target.checked);
          }}
        />
        I have stored the secret.
      </label>
      <button type="button" disabled={!acknowledged} onClick={onDone}>
        Done
      </button>
    </div>
  );
}
