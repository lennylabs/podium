// The one-time webhook-secret reveal. A git layer's HMAC secret is returned
// on registration and on a rotation and nowhere else, so both entry points
// present it through this component and the wording that it is unrecoverable
// is stated once.

import { useState } from 'react';
import type { ReactNode } from 'react';

import { Banner, CopyField } from '../components/primitives';
import type { LayerSecretResult } from '../api';

/**
 * revealsSecret reports whether a response carries a credential the reader
 * has to take away. The register and rotate flows ask before they present
 * the reveal, because a response that carries no secret leaves nothing to
 * acknowledge and the dialog around it stays ordinarily dismissible.
 */
export function revealsSecret(
  result: LayerSecretResult,
): result is LayerSecretResult & { webhook_secret: string } {
  return result.webhook_secret !== undefined && result.webhook_secret !== '';
}

/**
 * useSecretAcknowledgement holds whether the reader has stated they stored the
 * secret, and reports whether the dialog around the reveal may offer its
 * ordinary dismissal routes.
 *
 * The hold on Escape, the scrim, and the close control exists so a reader
 * cannot leave an unrecoverable credential behind without saying they took it.
 * Once the acknowledgement is given that reason is spent, so the dialog goes
 * back to closing the way every other dialog does and the keyboard is a route
 * out again. A response that carries no secret is dismissible throughout,
 * because it leaves nothing to acknowledge.
 */
export function useSecretAcknowledgement() {
  const [acknowledged, setAcknowledged] = useState(false);
  const dismissible = (result: LayerSecretResult | null) =>
    result === null || !revealsSecret(result) || acknowledged;
  return { acknowledged, setAcknowledged, dismissible };
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
 *
 * The component renders the dialog's scrolling body and its footer, because
 * the acknowledgement gates the only control that closes the dialog and a
 * footer has to be the body's sibling to sit below it. A caller passes any
 * further body content as children.
 *
 * The acknowledgement is held by the caller through useSecretAcknowledgement,
 * because it also decides whether the dialog around the reveal offers its
 * ordinary dismissal routes.
 */
export function SecretReveal({
  result,
  outcome,
  acknowledged,
  onAcknowledge,
  onDone,
  children,
}: {
  result: LayerSecretResult;
  outcome: string;
  acknowledged: boolean;
  onAcknowledge: (acknowledged: boolean) => void;
  onDone: () => void;
  children?: ReactNode;
}) {
  if (!revealsSecret(result)) {
    return (
      <div className="modal-body">
        <Banner tone="accent">{outcome}</Banner>
        {children}
      </div>
    );
  }
  return (
    <>
      <div className="modal-body">
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
        </div>
        {/* The acknowledgement is the reader's own statement and sits outside
            the dashed block, which frames the credential the block says is
            unrecoverable. */}
        <label className="secret-ack">
          <input
            type="checkbox"
            checked={acknowledged}
            onChange={(event) => {
              onAcknowledge(event.target.checked);
            }}
          />
          I have stored the secret.
        </label>
        {children}
      </div>
      {/* Until the acknowledgement is given this control is the dialog's only
          way out, so it is the footer's primary on the same terms as every
          other dialog's submit, and the note beside it names the way back for
          a reader who did not store the value. */}
      <div className="modal-foot">
        <span className="quiet modal-foot-note">You can rotate the secret later if you need to.</span>
        <button type="button" className="button primary" disabled={!acknowledged} onClick={onDone}>
          Done
        </button>
      </div>
    </>
  );
}
