// The shared primitives every surface renders: one badge that takes a tone,
// one feedback container, one empty state, and one loading state. Absent is a
// designed state rather than blank space, so a surface reaches for these
// rather than rendering nothing.

import type { ReactNode } from 'react';
import { useEffect, useId, useState } from 'react';
import { createPortal } from 'react-dom';

import { ApiError } from '../api';

export type Tone = 'neutral' | 'accent' | 'danger' | 'quiet';

/**
 * CopyField renders a value the reader has to take away with them beside an
 * explicit Copy control. Every such value carries this control, because a
 * value that is copied by clicking it carries no affordance saying so, and a
 * one-time secret is unrecoverable once the reader has moved on. A block
 * field renders the value as preformatted text so its line breaks survive.
 */
export function CopyField({ label, value, block = false }: { label: string; value: string; block?: boolean }) {
  return (
    <div className="copy-field">
      <span className="label quiet">{label}</span>
      {block ? <pre className="mono copy-value">{value}</pre> : <span className="mono copy-value">{value}</span>}
      <CopyButton value={value} />
    </div>
  );
}

/**
 * CopyButton is the explicit copy control CopyField carries, on its own for a
 * surface that lays the value out itself. It reports the outcome beside
 * itself, because a browser that exposes no clipboard leaves the value on the
 * page to be selected and the control must not claim a copy that did not
 * occur.
 */
export function CopyButton({ value }: { value: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <>
      <button
        type="button"
        onClick={() => {
          void navigator.clipboard?.writeText(value).then(
            () => {
              setCopied(true);
            },
            () => {
              setCopied(false);
            },
          );
        }}
      >
        Copy
      </button>
      {copied && <span className="quiet">Copied</span>}
    </>
  );
}

export function Badge({ tone = 'neutral', children }: { tone?: Tone; children: ReactNode }) {
  return <span className={`badge badge-${tone}`}>{children}</span>;
}

/**
 * SensitivityBadge renders the artifact's sensitivity classification. The
 * value alone ("internal", "confidential") does not say which axis it
 * measures, so the badge names the axis and carries the same weight as the
 * type and version badges beside it: the classification is informational and
 * never an alert. The badge is absent on an unclassified artifact.
 *
 * Spec: §4.3
 */
export function SensitivityBadge({ sensitivity }: { sensitivity?: string }) {
  if (sensitivity === undefined || sensitivity === '') {
    return null;
  }
  return <Badge>sensitivity: {sensitivity}</Badge>;
}

export function Banner({ tone = 'neutral', children }: { tone?: Tone; children: ReactNode }) {
  return (
    <div className={`banner banner-${tone}`} role="status">
      {children}
    </div>
  );
}

/**
 * PageBanner is the Banner preset that sits between the top bar and the
 * grid, full width, and speaks about the page rather than about a control on
 * it. It carries no control of its own: where the reader's next action is an
 * authentication one, that control belongs to the shell.
 */
export function PageBanner({
  tone = 'neutral',
  testID,
  children,
}: {
  tone?: Tone;
  testID?: string;
  children: ReactNode;
}) {
  return (
    <div className={`page-banner banner banner-${tone}`} role="status" data-testid={testID}>
      {children}
    </div>
  );
}

export function Loading({ label }: { label: string }) {
  return (
    <p className="loading" role="status">
      <span className="spinner" aria-hidden="true" />
      {label}
    </p>
  );
}

export function EmptyState({ children }: { children: ReactNode }) {
  return <p className="empty">{children}</p>;
}

/**
 * ErrorState presents a §6.10 envelope: the code the page branches on, the
 * prose message, the remediation hint where the code carries one, and the
 * retry signal. The retry control is rendered only where the envelope says
 * the condition clears on its own; where it says the condition does not, the
 * state says so instead, because offering a retry there sends the reader
 * round a loop that ends the same way. A failure carrying no envelope at all
 * is a transport failure, which does clear on its own.
 */
export function ErrorState({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  const envelope = error instanceof ApiError ? error : null;
  const retryable = envelope === null || envelope.retryable;
  return (
    <div className="banner banner-danger" role="alert">
      <p className="banner-title">The registry did not answer this request.</p>
      {envelope !== null && <p className="mono banner-code">{envelope.code}</p>}
      <p>{envelope !== null ? envelope.message : String(error)}</p>
      {envelope !== null && envelope.suggestedAction !== '' && <p className="quiet">{envelope.suggestedAction}</p>}
      {onRetry !== undefined &&
        (retryable ? (
          <button type="button" onClick={onRetry}>
            Try again
          </button>
        ) : (
          <p className="quiet" data-testid="not-retryable">
            Retrying does not clear this condition.
          </p>
        ))}
    </div>
  );
}

/**
 * Modal is a dialog over a scrim. A write the reader has to review before it
 * is sent is presented over the surface that opened it rather than pushed
 * into it, so the surface underneath keeps its position and the dialog owns
 * the reader's attention while it is open. The scrim, Escape, and the close
 * control all dismiss it, because a dialog that can only be left by
 * completing the write traps a reader who opened it to look.
 *
 * It renders through a portal, so the dialog stands at the end of the
 * document however deep the control that opened it sits. A dialog left in
 * place is laid out by whatever contains that control: opened from a table
 * cell it takes the column's width and grows the row, and a fixed position
 * resolves against any ancestor carrying a transform rather than against the
 * viewport.
 */
export function Modal({
  title,
  description,
  onClose,
  children,
}: {
  title: string;
  description?: string;
  onClose: () => void;
  children: ReactNode;
}) {
  const headingID = useId();
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        onClose();
      }
    };
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('keydown', onKey);
    };
  }, [onClose]);
  return createPortal(
    <div
      className="modal-scrim"
      role="presentation"
      data-testid="modal-scrim"
      onClick={(event) => {
        if (event.target === event.currentTarget) {
          onClose();
        }
      }}
    >
      <div className="modal" role="dialog" aria-modal="true" aria-labelledby={headingID}>
        <header className="modal-head">
          <div className="modal-title-row">
            <h2 id={headingID}>{title}</h2>
            <button type="button" className="modal-close" aria-label="Close" onClick={onClose}>
              ✕
            </button>
          </div>
          {description !== undefined && <p className="modal-lead">{description}</p>}
        </header>
        {children}
      </div>
    </div>,
    document.body,
  );
}
