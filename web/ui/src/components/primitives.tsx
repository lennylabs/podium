// The shared primitives every surface renders: one badge that takes a tone,
// one feedback container, one empty state, and one loading state. Absent is a
// designed state rather than blank space, so a surface reaches for these
// rather than rendering nothing.

import type { ReactNode } from 'react';
import { useEffect, useId, useState } from 'react';
import { createPortal } from 'react-dom';

import { ApiError } from '../api';
import { dismissAttribute, useDialogFocus } from './focus';

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
export function CopyButton({ value, label = 'Copy' }: { value: string; label?: string }) {
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
        {label}
      </button>
      {copied && <span className="quiet">Copied</span>}
    </>
  );
}

/**
 * Magnifier is the one search icon the shell draws, rendered as inline SVG so
 * it takes its colour from the text beside it and holds its proportions at
 * any size. It is drawn rather than typed: the Unicode magnifier it replaces
 * (U+2315) sets at a fraction of its nominal size in the shell's typefaces
 * and reads as a stray mark rather than as an icon.
 *
 * Spec: §13.10
 */
export function Magnifier({ size = 13 }: { size?: number }) {
  return (
    <svg className="magnifier" width={size} height={size} viewBox="0 0 14 14" aria-hidden="true" focusable="false">
      <circle cx="5.8" cy="5.8" r="4.6" fill="none" stroke="currentColor" strokeWidth="1.6" />
      <path d="M9.4 9.4L13 13" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" />
    </svg>
  );
}

export function Badge({ tone = 'neutral', children }: { tone?: Tone; children: ReactNode }) {
  return <span className={`badge badge-${tone}`}>{children}</span>;
}

/**
 * TypeBadge names the artifact's first-class type. The type is a fixed
 * vocabulary rather than prose, so it is set in caps to read as a marker
 * beside the artifact's own name. The transform is applied to the text rather
 * than in the stylesheet, so what a reader copies out of the page is what the
 * page shows.
 *
 * Spec: §4.3
 */
export function TypeBadge({ type }: { type: string }) {
  return <Badge>{type.toUpperCase()}</Badge>;
}

/**
 * formatVersion prefixes a manifest version for display. A bare semver beside
 * an artifact name reads as an unlabelled number, and the `v` names what the
 * number measures. A version that already carries the prefix keeps the one it
 * has.
 */
export function formatVersion(version: string): string {
  if (version === '') {
    return '';
  }
  return version.startsWith('v') ? version : `v${version}`;
}

/**
 * VersionBadge renders the manifest version as a quiet badge beside the type.
 */
export function VersionBadge({ version }: { version: string }) {
  const text = formatVersion(version);
  if (text === '') {
    return null;
  }
  return <Badge tone="quiet">{text}</Badge>;
}

/**
 * CuratedBadge marks a row the domain author chose to feature. The star
 * carries the distinction at a glance in a listing where the accent tone
 * alone is one of several, and the label names what the star means.
 *
 * Spec: §13.10
 */
export function CuratedBadge() {
  return <span className="badge badge-accent badge-curated">★ CURATED</span>;
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

/** ErrorPageKind is what a whole-surface failure was: the read resolved
 * nothing, the registry did not answer, or it refused for some other reason.
 * Not-found and not-permitted deliberately land on the same kind, because a
 * page that told them apart would disclose that an artifact the caller may
 * not see exists.
 *
 * Spec: §13.10
 */
type ErrorPageKind = 'notFound' | 'unavailable' | 'failed';

/** errorPageKind reads the kind off the §6.10 envelope. A failure carrying no
 * envelope at all never reached the registry, which is the unreachable case
 * rather than a refusal. */
function errorPageKind(error: unknown): ErrorPageKind {
  if (!(error instanceof ApiError)) {
    return 'unavailable';
  }
  if (error.code.endsWith('.not_found')) {
    return 'notFound';
  }
  return error.code === 'registry.unavailable' ? 'unavailable' : 'failed';
}

/** envelopeMessage is the envelope's prose with a leading repetition of its
 * own code removed. The registry prefixes several messages with the code they
 * carry ("registry.not_found: artifact eng/deploy"), and both the error page
 * and the ErrorState banner state the code once on a line of their own, so
 * the prefix would print it twice.
 *
 * Spec: §6.10 */
function envelopeMessage(error: ApiError): string {
  const prefix = `${error.code}: `;
  return error.message.startsWith(prefix) ? error.message.slice(prefix.length) : error.message;
}

/**
 * ErrorPage is a whole-surface failure, as opposed to the ErrorState banner a
 * surface that is still standing renders inside itself. It is a centered card
 * carrying the kind of failure, what did not load, one sentence naming what
 * the route asked for, and the way on. A dead surface always offers a way off
 * it, because the route still names something that did not load and the
 * reader is otherwise left on a page with nothing on it; the retry sits beside
 * that where the envelope says the condition clears on its own. The code is
 * stated once and quietly at the foot, where whoever has to report it can
 * find it without it being the first thing the page says.
 */
export function ErrorPage({
  error,
  title,
  subject,
  onRetry,
  testID,
}: {
  error: unknown;
  /** What did not load, in the page's own words: "No such artifact". */
  title: string;
  /** The domain path or artifact ID the route named. The sentence under the
   * title states it, so the reader sees what was asked for. */
  subject?: string;
  onRetry?: () => void;
  testID?: string;
}) {
  const envelope = error instanceof ApiError ? error : null;
  const kind = errorPageKind(error);
  const label = kind === 'notFound' ? 'NOT FOUND' : kind === 'unavailable' ? 'REGISTRY UNREACHABLE' : 'REFUSED';
  const heading = kind === 'unavailable' ? "Can't reach the registry" : title;
  const offerRetry = onRetry !== undefined && (envelope === null || envelope.retryable);
  return (
    <section className="surface error-page" role="alert" aria-label="Failed" data-testid={testID}>
      <div className="error-card">
        <p className="mono error-kind">{label}</p>
        <h1 className="error-title">{heading}</h1>
        <p className="error-lead">
          {kind === 'notFound' && subject !== undefined ? (
            <>
              <span className="mono">{subject}</span> does not resolve.
            </>
          ) : envelope !== null ? (
            envelopeMessage(envelope)
          ) : (
            'The page loaded but the registry did not answer. Nothing has changed.'
          )}
        </p>
        {envelope !== null && envelope.suggestedAction !== '' && (
          <p className="error-lead quiet">{envelope.suggestedAction}</p>
        )}
        <div className="error-actions">
          {offerRetry && (
            <button type="button" className="button primary" onClick={onRetry}>
              Retry
            </button>
          )}
          <a className={offerRetry ? 'button' : 'button primary'} href="#/">
            Back to catalog
          </a>
        </div>
        {envelope !== null && (
          <p className="mono error-code">
            {envelope.code} · {envelope.retryable ? 'retryable' : 'not retryable'}
          </p>
        )}
      </div>
    </section>
  );
}

/**
 * ErrorState presents a §6.10 envelope: the code the page branches on, the
 * prose message, the remediation hint where the code carries one, and the
 * retry signal. The retry control is rendered only where the envelope says
 * the condition clears on its own; where it says the condition does not, the
 * state says so instead, because offering a retry there sends the reader
 * round a loop that ends the same way. A failure carrying no envelope at all
 * is a transport failure, which does clear on its own.
 *
 * A caller that keeps its surface standing around the failure states what the
 * refusal was about in `title` and puts the recovery control that belongs to
 * that surface in `children`, so the reader is offered a way on from inside
 * the same banner rather than from a page that is no longer there.
 */
export function ErrorState({
  error,
  onRetry,
  title = 'The registry did not answer this request.',
  testID,
  children,
}: {
  error: unknown;
  onRetry?: () => void;
  title?: string;
  testID?: string;
  children?: ReactNode;
}) {
  const envelope = error instanceof ApiError ? error : null;
  const retryable = envelope === null || envelope.retryable;
  return (
    <div className="banner banner-danger" role="alert" data-testid={testID}>
      <p className="banner-title">{title}</p>
      {envelope !== null && <p className="mono banner-code">{envelope.code}</p>}
      <p>{envelope !== null ? envelopeMessage(envelope) : String(error)}</p>
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
      {children}
    </div>
  );
}

/**
 * Modal is a dialog over a scrim. A write the reader has to review before it
 * is sent is presented over the surface that opened it rather than pushed
 * into it, so the surface underneath keeps its position and the dialog owns
 * the reader's attention while it is open. The scrim, Escape, and the close
 * control all dismiss it, because a dialog that can only be left by
 * completing the write traps a reader who opened it to look. A dialog the
 * caller marks undismissible withholds all three, which is for content the
 * reader cannot get back once it is gone: the one-time webhook secret is
 * shown once and is unrecoverable, so leaving that dialog by any route other
 * than its own acknowledgement discards the credential. Focus moves
 * into the dialog when it opens, cycles within it, and returns to the control
 * that opened it when it closes.
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
  dismissible = true,
  children,
}: {
  title: string;
  description?: string;
  onClose: () => void;
  dismissible?: boolean;
  children: ReactNode;
}) {
  const headingID = useId();
  // The title identifies the dialog: a caller that replaces a form with the
  // outcome of its submission renders a second Modal in the first one's
  // place, and React reuses the element, so the title is what tells the focus
  // move that the dialog behind it is a different one.
  const dialog = useDialogFocus<HTMLDivElement>(true, title);
  useEffect(() => {
    if (!dismissible) {
      return;
    }
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        onClose();
      }
    };
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('keydown', onKey);
    };
  }, [dismissible, onClose]);
  return createPortal(
    <div
      className="modal-scrim"
      role="presentation"
      data-testid="modal-scrim"
      onClick={(event) => {
        if (dismissible && event.target === event.currentTarget) {
          onClose();
        }
      }}
    >
      <div ref={dialog} className="modal" role="dialog" aria-modal="true" aria-labelledby={headingID}>
        <header className="modal-head">
          <div className="modal-title-row">
            <h2 id={headingID}>{title}</h2>
            {dismissible && (
              <button
                type="button"
                className="modal-close"
                aria-label="Close"
                {...{ [dismissAttribute]: '' }}
                onClick={onClose}
              >
                ✕
              </button>
            )}
          </div>
          {description !== undefined && <p className="modal-lead">{description}</p>}
        </header>
        {children}
      </div>
    </div>,
    document.body,
  );
}
