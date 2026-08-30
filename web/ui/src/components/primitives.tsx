// The shared primitives every surface renders: one badge that takes a tone,
// one feedback container, one empty state, and one loading state. Absent is a
// designed state rather than blank space, so a surface reaches for these
// rather than rendering nothing.

import type { KeyboardEvent as ReactKeyboardEvent, ReactNode } from 'react';
import { Fragment, useEffect, useId, useState } from 'react';
import { createPortal } from 'react-dom';

import { ApiError } from '../api';
import { atCatalogRoute, domainHref } from '../route';
import { dismissAttribute, holdDismissal, useDialogFocus } from './focus';
import { useScrollLock } from './scrolllock';

export type Tone = 'neutral' | 'accent' | 'danger' | 'quiet';

/** BadgeTone is the tone set a badge takes. It extends the shared tones with
 * `soft`, the filled borderless chip the badge alone draws, `grant`, the
 * filled chip that keeps the badge's outline, `hollow`, the outlined chip that
 * drops the fill, `marker`, the rounded chip that carries an accent dot, and
 * `count`, the filled pill a bare figure takes: the banner tones are
 * full-width containers, where a second neutral fill states nothing. */
export type BadgeTone = Tone | 'soft' | 'grant' | 'hollow' | 'marker' | 'count';

/**
 * CopyField renders a value the reader has to take away with them beside an
 * explicit Copy control. Every such value carries this control, because a
 * value that is copied by clicking it carries no affordance saying so, and a
 * one-time secret is unrecoverable once the reader has moved on. A block
 * field renders the value as preformatted text so its line breaks survive.
 *
 * A badge sits beside the label for a field whose value carries a condition
 * the label alone does not state, which is how the webhook secret is marked
 * as shown once without a heading that would also cover the field above it.
 */
export function CopyField({
  label,
  value,
  block = false,
  badge,
}: {
  label: string;
  value: string;
  block?: boolean;
  badge?: string;
}) {
  return (
    <div className="copy-field">
      <span className="label quiet">{label}</span>
      {badge !== undefined && <Badge tone="accent">{badge}</Badge>}
      {block ? <pre className="mono copy-value">{value}</pre> : <span className="mono copy-value">{value}</span>}
      <CopyButton value={value} subject={label} />
    </div>
  );
}

/**
 * CopyButton is the explicit copy control CopyField carries, on its own for a
 * surface that lays the value out itself. It reports the outcome beside
 * itself, because a browser that exposes no clipboard leaves the value on the
 * page to be selected and the control must not claim a copy that did not
 * occur.
 *
 * The outcome is also carried by a live region, held on the page from the
 * first render and empty until a copy lands. The visible confirmation alone
 * announces nothing, and the one value that cannot be copied twice is the
 * one-time webhook secret the register dialog destroys on dismissal. A caller
 * that knows what the value is names it in `subject`, so the announcement
 * says which of several values on the surface was taken.
 *
 * Spec: §13.10
 */
export function CopyButton({
  value,
  label = 'Copy',
  subject,
}: {
  value: string;
  label?: string;
  subject?: string;
}) {
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
      {copied && (
        <span className="quiet" aria-hidden="true">
          Copied
        </span>
      )}
      <span className="assistive-only" role="status" aria-live="polite" data-testid="copy-announcement">
        {copied ? (subject ? `${subject} copied to clipboard.` : 'Copied to clipboard.') : ''}
      </span>
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

/**
 * Chevron is the small right-pointing indicator the shell draws beside a
 * control that leads somewhere or opens something. It is drawn rather than
 * typed so it takes its colour from the text beside it, and the element it
 * sits in rotates it where it points another way.
 *
 * Spec: §13.10
 */
export function Chevron() {
  return (
    <svg className="chevron" viewBox="0 0 10 10" aria-hidden="true">
      <path d="M3 1.5L7 5l-4 3.5" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
    </svg>
  );
}

/**
 * Badge is the small marker beside a name or a title. The tone selects the
 * treatment: `marker` states a property of the response rather than of the
 * subject, and it is the one tone that draws a dot.
 *
 * Spec: §13.10
 */
export function Badge({ tone = 'neutral', children }: { tone?: BadgeTone; children: ReactNode }) {
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
 * PathLabel draws a slash-separated domain path as a label that breaks at its
 * separators. A §4.5.5 sparse chain reaches a card folded into one entry whose
 * label is the whole stretch of path it crosses, and a slash carries no break
 * opportunity of its own, so a card too narrow for the label breaks it inside
 * a segment and the title reads as broken text rather than as a path. The
 * `<wbr>` after each separator is the opportunity the browser takes first,
 * which keeps the break on a boundary and leaves the CSS break rule as the
 * fallback for a single segment wider than the card.
 *
 * Spec: §13.10
 */
export function PathLabel({ path }: { path: string }) {
  return (
    <>
      {path.split('/').map((segment, index) => (
        <Fragment key={`${String(index)}:${segment}`}>
          {index > 0 && (
            <>
              {'/'}
              <wbr />
            </>
          )}
          {segment}
        </Fragment>
      ))}
    </>
  );
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
 * FoldedFromBadge names the subdomain a §4.5.5 lifted entry was raised out of.
 * The row it sits on is not a child of the domain it is listed under, and the
 * badge is what says so, so it is drawn on the dashed edge the lifted group's
 * own container carries rather than on the outline every informational badge
 * beside it takes. The arrow states the direction the entry travelled, and
 * naming the relation in caps keeps the badge from reading as one more tag on
 * the row. The value is the relative subpath the registry reports, which is
 * what distinguishes two lifted entries of the same name.
 *
 * Spec: §4.5.5
 */
export function FoldedFromBadge({ foldedFrom }: { foldedFrom?: string }) {
  if (foldedFrom === undefined || foldedFrom === '') {
    return null;
  }
  return <span className="badge badge-folded">↑ FROM {foldedFrom}</span>;
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

/**
 * DeprecatedBadge marks an artifact the registry still serves but has retired.
 * The registry reports the lifecycle state on the response rather than leaving
 * it to be read out of the frontmatter, so the badge keys on that field and
 * carries the accent tone: a reader who lands on a retired artifact has to see
 * that it is retired before reading it.
 *
 * Spec: §4.7.4
 */
export function DeprecatedBadge({ deprecated }: { deprecated?: boolean }) {
  if (deprecated !== true) {
    return null;
  }
  return <Badge tone="accent">DEPRECATED</Badge>;
}

/**
 * Banner states one fact about the surface it sits on. Two banners set side
 * by side differ only in their fill, which is not enough to tell a warning
 * from the aside beside it, so a banner that has a neighbour takes a leading
 * glyph in its own gutter the way the register form's consequence and note do.
 */
export function Banner({
  tone = 'neutral',
  glyph,
  children,
}: {
  tone?: Tone;
  glyph?: string;
  children: ReactNode;
}) {
  if (glyph === undefined) {
    return (
      <div className={`banner banner-${tone}`} role="status">
        {children}
      </div>
    );
  }
  return (
    <div className={`banner banner-${tone} banner-lead`} role="status">
      <span className="banner-glyph" aria-hidden="true">
        {glyph}
      </span>
      <div className="banner-text">{children}</div>
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

/** EmptyScope is how much of the surface an absence stands for. A `page`
 * absence replaces a listing and is drawn as a bordered card, and an
 * `inline` one stands for a single section of the artifact rail and is drawn
 * as a smaller dashed box. Both read quieter than the content they replace,
 * because an absence that outweighs the labels around it makes the missing
 * thing the loudest thing on the surface.
 *
 * Spec: §13.10
 */
export type EmptyScope = 'page' | 'inline';

/** EmptyStateProps carries the title only on the `page` scope. The designed
 * page-scope absence is two lines, a short title over the sentence that says
 * what would appear there, because a lone centred sentence in a card the size
 * of the listing it replaced reads as a caption that lost its content. The
 * rail-scope absence is the single quiet line, so the union refuses a title
 * there rather than letting one section of the rail outweigh the others.
 *
 * Spec: §13.10
 */
type EmptyStateProps =
  | { scope?: 'page'; title: string; children: ReactNode }
  | { scope: 'inline'; title?: never; children: ReactNode };

export function EmptyState(props: EmptyStateProps) {
  const scope = props.scope ?? 'page';
  return (
    <div className={`empty empty-${scope}`}>
      {props.title === undefined ? null : <p className="empty-title">{props.title}</p>}
      <p className="empty-body">{props.children}</p>
    </div>
  );
}

/** ErrorPageKind is what a whole-surface failure was: the read resolved
 * nothing, the registry did not answer, or it refused for some other reason.
 * Not-found and not-permitted deliberately land on the same kind, because a
 * page that told them apart would disclose that an artifact the caller may
 * not see exists. `concealRefusal` below is what puts a refusal on this arm.
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

/** permissionCodes are the §6.10 codes that report a permission decision on a
 * read: an authorization refusal, and the batch path's per-item visibility
 * denial, which `docs/reference/error-codes.md` records as mirroring a
 * not-found result. */
const permissionCodes: ReadonlySet<string> = new Set<string>(['auth.forbidden', 'visibility.denied']);

/** concealRefusal replaces a read's permission refusal with the not-found
 * envelope, so the page it renders is the one an artifact that does not exist
 * renders, down to the code at its foot. The single-artifact read conceals the
 * denial in the registry today (`pkg/registry/core`), and the concealment §13.10
 * requires is a property of the page rather than of that invariant: a read route
 * that later answered a refusal directly would otherwise disclose, through the
 * eyebrow, the message, and the code, that an artifact the caller may not see
 * exists.
 *
 * Spec: §13.10 */
function concealRefusal(error: unknown): unknown {
  if (!(error instanceof ApiError) || !permissionCodes.has(error.code)) {
    return error;
  }
  return new ApiError(404, 'registry.not_found', 'The registry has no record of it.', false, '');
}

/** envelopeMessage is the envelope's prose with a leading repetition of its
 * own code removed. The registry prefixes several messages with the code they
 * carry ("registry.not_found: artifact eng/deploy"), and both the error page
 * and the ErrorState banner state the code once on a line of their own, so
 * the prefix would print it twice.
 *
 * Spec: §6.10 */
function envelopeMessage(error: ApiError): string {
  if (error.code === '') {
    return error.message;
  }
  const prefix = `${error.code}: `;
  return error.message.startsWith(prefix) ? error.message.slice(prefix.length) : error.message;
}

/**
 * ErrorPage is a whole-surface failure, as opposed to the ErrorState banner a
 * surface that is still standing renders inside itself. It is a centered card
 * carrying the kind of failure, what did not load, one sentence naming what
 * the route asked for, and the way on. A dead surface offers a way off it
 * wherever there is one, because the route still names something that did not
 * load and the reader is otherwise left on a page with nothing on it; the
 * retry sits beside that where the envelope says the condition clears on its
 * own, and on the catalog route itself the retry is the only action. The code is
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
  const shown = concealRefusal(error);
  const envelope = shown instanceof ApiError ? shown : null;
  const kind = errorPageKind(shown);
  const label = kind === 'notFound' ? 'NOT FOUND' : kind === 'unavailable' ? 'REGISTRY UNREACHABLE' : 'REFUSED';
  // The caller's title states what the route asked for and did not get ("No
  // such domain"), which a refusal carrying a §6.10 code has been classified
  // enough to say. A refusal carrying no code has not: the status alone does
  // not report that the domain is missing, so the page states that the
  // request was refused and leaves the code line to report the status.
  const heading =
    kind === 'unavailable'
      ? "Can't reach the registry"
      : envelope !== null && envelope.code === ''
        ? 'The request was refused'
        : title;
  const offerRetry = onRetry !== undefined && (envelope === null || envelope.retryable);
  // The way off is omitted on the route it leads to. The catalog read can fail
  // at the registry root, and there the link navigates to the route already on
  // screen: the panel stays exactly as it is, which reads as a second failed
  // attempt rather than as a link that had nowhere to go.
  const offerBack = !atCatalogRoute(window.location.hash);
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
        {(offerRetry || offerBack) && (
          <div className="error-actions">
            {offerRetry && (
              <button type="button" className="button primary" onClick={onRetry}>
                Retry
              </button>
            )}
            {offerBack && (
              <a className={offerRetry ? 'button' : 'button primary'} href={domainHref('')}>
                Back to catalog
              </a>
            )}
          </div>
        )}
        {envelope !== null && (
          <p className="mono error-code">
            {envelope.label} · {envelope.retryable ? 'retryable' : 'not retryable'}
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
      {envelope !== null && <p className="mono banner-code">{envelope.label}</p>}
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
  // The dialog is pinned to the viewport, so the surface it covers is held
  // still under it. A wheel over the scrim would otherwise scroll the page
  // behind the dialog, which is worst on the one-time secret, where the
  // reader has no way back to what they were reading.
  useScrollLock();
  // A dialog that withholds every dismissal route says so page-wide, so an
  // accelerator in the shell does not open an overlay over content the reader
  // cannot get back.
  useEffect(() => (dismissible ? undefined : holdDismissal()), [dismissible]);
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

/** TabStrip is a set of exclusive in-place views over one subject, with the
 * open view's content under the strip. A `role="tablist"` is one stop in the
 * Tab order and the arrows move between the tabs inside it, so a widget that
 * announces itself as a tab set behaves as one rather than as a row of
 * buttons. Selection follows focus, which is what a tab set whose panels are
 * already loaded is expected to do.
 *
 * The ids are derived from a per-instance prefix, because two strips can
 * stand on one page and an id serves one element.
 */
export function TabStrip<Name extends string>({
  label,
  tabs,
  open,
  onOpen,
  children,
}: {
  label: string;
  tabs: { name: Name; label: string; badge?: string; badgeTone?: BadgeTone }[];
  open: Name;
  onOpen: (name: Name) => void;
  children: ReactNode;
}) {
  const prefix = useId();
  const onArrow = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    const at = tabs.findIndex((entry) => entry.name === open);
    let next = at;
    switch (event.key) {
      case 'ArrowRight':
        next = (at + 1) % tabs.length;
        break;
      case 'ArrowLeft':
        next = (at + tabs.length - 1) % tabs.length;
        break;
      case 'Home':
        next = 0;
        break;
      case 'End':
        next = tabs.length - 1;
        break;
      default:
        return;
    }
    event.preventDefault();
    onOpen(tabs[next].name);
    // The focus moves with the selection rather than staying on the tab the
    // reader has already left.
    event.currentTarget.querySelectorAll<HTMLButtonElement>('[role="tab"]')[next]?.focus();
  };
  return (
    <>
      <div className="tabs" role="tablist" aria-label={label} onKeyDown={onArrow}>
        {tabs.map((entry) => (
          <button
            key={entry.name}
            type="button"
            role="tab"
            id={`${prefix}tab-${entry.name}`}
            aria-selected={open === entry.name}
            aria-controls={`${prefix}panel-${entry.name}`}
            // The roving tabindex: the tab set is one Tab stop, and the open
            // tab is the one it lands on.
            tabIndex={open === entry.name ? 0 : -1}
            className={open === entry.name ? 'tab tab-open' : 'tab'}
            onClick={() => {
              onOpen(entry.name);
            }}
          >
            {entry.label}
            {entry.badge !== undefined && entry.badge !== '' && (
              <Badge tone={entry.badgeTone ?? 'quiet'}>{entry.badge}</Badge>
            )}
          </button>
        ))}
      </div>
      <div role="tabpanel" id={`${prefix}panel-${open}`} aria-labelledby={`${prefix}tab-${open}`}>
        {children}
      </div>
    </>
  );
}
