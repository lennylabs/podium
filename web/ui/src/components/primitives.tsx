// The shared primitives every surface renders: one badge that takes a tone,
// one feedback container, one empty state, and one loading state. Absent is a
// designed state rather than blank space, so a surface reaches for these
// rather than rendering nothing.

import type { ReactNode } from 'react';

import { ApiError } from '../api';

export type Tone = 'neutral' | 'accent' | 'danger' | 'quiet';

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
