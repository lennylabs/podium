// The shared primitives every surface renders: one badge that takes a tone,
// one feedback container, one empty state, and one loading state. Absent is a
// designed state rather than blank space, so a surface reaches for these
// rather than rendering nothing.

import type { ReactNode } from 'react';
import { useState } from 'react';

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
  const [copied, setCopied] = useState(false);
  return (
    <div className="copy-field">
      <span className="label quiet">{label}</span>
      {block ? <pre className="mono copy-value">{value}</pre> : <span className="mono copy-value">{value}</span>}
      <button
        type="button"
        onClick={() => {
          // A browser that exposes no clipboard leaves the value on the page
          // to be selected, so the control reports what happened rather than
          // claiming a copy that did not occur.
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
    </div>
  );
}

export function Badge({ tone = 'neutral', children }: { tone?: Tone; children: ReactNode }) {
  return <span className={`badge badge-${tone}`}>{children}</span>;
}

export function Banner({ tone = 'neutral', children }: { tone?: Tone; children: ReactNode }) {
  return (
    <div className={`banner banner-${tone}`} role="status">
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
 * prose message, and the remediation hint where the code carries one. A
 * retryable condition gets the retry the envelope says clears on its own.
 */
export function ErrorState({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  const envelope = error instanceof ApiError ? error : null;
  return (
    <div className="banner banner-danger" role="alert">
      <p className="banner-title">The registry did not answer this request.</p>
      {envelope !== null && <p className="mono banner-code">{envelope.code}</p>}
      <p>{envelope !== null ? envelope.message : String(error)}</p>
      {envelope !== null && envelope.suggestedAction !== '' && <p className="quiet">{envelope.suggestedAction}</p>}
      {onRetry !== undefined && (
        <button type="button" onClick={onRetry}>
          Try again
        </button>
      )}
    </div>
  );
}
