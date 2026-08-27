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
 * carries no reveal, so the component renders the outcome alone and the caller
 * does not have to branch on the response itself.
 *
 * The outcome is stated on both branches. The reveal is where naming the layer
 * matters most: the credential is unrecoverable, and a reader who is handed a
 * secret with no outcome line is not told that the write landed or which layer
 * the secret belongs to.
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
    <>
      <Banner tone="accent">{outcome}</Banner>
      {/* The URL is stored on the layer and the panel serves it again on
          demand, so it sits outside the block below. Inside it, the block's
          dashed edge would state that the URL is unrecoverable too, which is
          the one thing a reader must not believe about the value they have to
          configure their repository with. */}
      {result.webhook_url !== undefined && result.webhook_url !== '' && (
        <>
          <CopyField label="Webhook URL" value={result.webhook_url} />
          <p className="quiet">Stored on the layer. You can look this up again any time.</p>
        </>
      )}
      <div className="secret-reveal" aria-label="Webhook secret">
        {/* The badge sits on the secret's own label, so the shown-once
            condition names the one value it applies to. */}
        <CopyField label="Webhook secret" badge="SHOWN ONCE" value={result.webhook_secret} />
        <p>
          The registry stores a hash of the secret rather than the secret. Once this dialog closes it cannot be shown
          again, and a replacement takes a rotation.
        </p>
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
    </>
  );
}
