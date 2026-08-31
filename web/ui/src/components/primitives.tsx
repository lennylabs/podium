// The shared primitives every surface renders: one badge that takes a tone,
// one feedback container, one empty state, and one loading state. Absent is a
// designed state rather than blank space, so a surface reaches for these
// rather than rendering nothing.

import type { KeyboardEvent as ReactKeyboardEvent, ReactNode } from 'react';
import { Fragment, useEffect, useId, useState } from 'react';
import { createPortal } from 'react-dom';

import { ApiError } from '../api';
import { splitDomainLabel } from '../domain';
import { atCatalogRoute, domainHref, useFailureTitle } from '../route';
import { dismissAttribute, registerModal, requestRetryFocus, useDialogFocus, useRetryFocus } from './focus';
import { useScrollLock } from './scrolllock';

export type Tone = 'neutral' | 'accent' | 'danger' | 'quiet';

/** BadgeTone is the tone set a badge takes. It extends the shared tones with
 * `soft`, the filled borderless chip the badge alone draws, `grant`, the
 * filled chip that keeps the badge's outline, `hollow`, the outlined chip that
 * drops the fill, `marker`, the rounded chip that carries an accent dot, and
 * `count`, the filled pill a bare figure takes, and `strong`, the filled
 * accent pill a condition the reader cannot undo takes: the banner tones are
 * full-width containers, where a second neutral fill states nothing. */
export type BadgeTone = Tone | 'soft' | 'grant' | 'hollow' | 'marker' | 'count' | 'strong';

/** TabCountTone is the tone a tab's trailing count or marker takes. The count
 * is quiet mono text rather than a chip, so the tone sets its colour and
 * nothing else. */
export type TabCountTone = 'quiet' | 'danger' | 'accent';

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
 * A field carrying one takes the accent label and the filled accent pill, so
 * the condition reads louder than the label of the permanent value beside it.
 * Drawn in the quiet label and the wash chip the informational badges use, the
 * marker for a value that cannot be recovered carries no more weight than the
 * one for a value that can.
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
      <span className={badge === undefined ? 'label quiet' : 'label label-accent'}>{label}</span>
      {badge !== undefined && <Badge tone="strong">{badge}</Badge>}
      {block ? <pre className="mono copy-value">{value}</pre> : <span className="mono copy-value">{value}</span>}
      <CopyButton value={value} subject={label} />
    </div>
  );
}

/** defaultCopyLabel is the word a copy control carries when its caller names
 * no other. It is also the marker for a control whose name CopyButton may
 * extend with the value's subject. */
const defaultCopyLabel = 'Copy';

/**
 * CopyButton is the explicit copy control CopyField carries, on its own for a
 * surface that lays the value out itself. It reports the outcome beside
 * itself, because a browser that exposes no clipboard leaves the value on the
 * page to be selected and the control must not claim a copy that did not
 * occur.
 *
 * A failure is reported as plainly as a success. `navigator.clipboard` is
 * absent on every non-secure origin, so a registry served over plain HTTP on
 * any host but localhost has no clipboard at all, and a control that stayed
 * at rest on that press is indistinguishable from one that was never
 * pressed.
 * The reader who takes silence for a copy is the operator holding the
 * one-time webhook secret, which the reveal destroys on dismissal, so the
 * failed press says so and sends them to the value on the page.
 *
 * The outcome is also carried by a live region, held on the page from the
 * first render and empty until a copy lands. The visible confirmation alone
 * announces nothing, and the one value that cannot be copied twice is the
 * one-time webhook secret the register dialog destroys on dismissal. A caller
 * that knows what the value is names it in `subject`, so the announcement
 * says which of several values on the surface was taken or missed.
 *
 * `subject` also names the button itself. The register and rotation reveals
 * put two of these side by side, one carrying the permanent webhook URL and
 * one carrying the secret that is shown once, and a reader working from the
 * tab order or a list of controls cannot tell two buttons both called Copy
 * apart. The accessible name keeps the visible word first so that speaking
 * the label still activates the control. A caller that passes its own
 * `label` has already named the control, so that name is left alone.
 *
 * Spec: §13.10
 */
export function CopyButton({
  value,
  label = defaultCopyLabel,
  subject,
}: {
  value: string;
  label?: string;
  subject?: string;
}) {
  const [outcome, setOutcome] = useState<CopyOutcome>('rest');
  const accessibleName = subject !== undefined && label === defaultCopyLabel ? `${label} ${subject}` : undefined;
  return (
    <>
      <button
        type="button"
        aria-label={accessibleName}
        onClick={() => {
          const clipboard: Clipboard | undefined = navigator.clipboard;
          if (clipboard === undefined) {
            setOutcome('failed');
            return;
          }
          void clipboard.writeText(value).then(
            () => {
              setOutcome('copied');
            },
            () => {
              setOutcome('failed');
            },
          );
        }}
      >
        {label}
      </button>
      {/* Both reports hold their place from the first render and one of them
          is revealed by the press, stacked so the row reserves the wider of
          the two once. Inserting a report on the press takes width from the
          value beside it, which rewraps and grows the row, and in the register
          dialog that moves the acknowledgement and the Done button the reader
          clicks next out from under the pointer. */}
      <span className="copy-outcome" aria-hidden="true">
        <span className="quiet copy-confirmation" data-copied={outcome === 'copied' ? '' : undefined}>
          Copied
        </span>
        <span className="copy-failure" data-failed={outcome === 'failed' ? '' : undefined}>
          Not copied
        </span>
      </span>
      <span className="assistive-only" role="status" aria-live="polite" data-testid="copy-announcement">
        {announcement(outcome, subject)}
      </span>
    </>
  );
}

/** CopyOutcome is what the last press of a copy control did: nothing yet,
 * took the value, or reached no clipboard and left it on the page. */
type CopyOutcome = 'rest' | 'copied' | 'failed';

/** announcement is the live region's text for an outcome. The failed press
 * names the value it did not take and says where the value still is, because
 * the reader who cannot see the control's report is the one least able to
 * find the value again. */
function announcement(outcome: CopyOutcome, subject?: string): string {
  if (outcome === 'copied') {
    return subject !== undefined ? `${subject} copied to clipboard.` : 'Copied to clipboard.';
  }
  if (outcome === 'failed') {
    const named = subject !== undefined ? `${subject} was not copied.` : 'The value was not copied.';
    return `${named} The clipboard was not reachable, so select the value on the page and copy it yourself.`;
  }
  return '';
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
 * RailPathLabel draws a domain label on one line of the sidebar tree, where
 * the row is a single line that clips rather than wrapping. A §4.5.5 sparse
 * chain reaches the rail folded into one entry whose label is the whole
 * stretch of path it crosses, and a label clipped as one string loses its
 * tail, which is the segment naming the domain the row opens. The ancestry
 * and the name are drawn in separate boxes so the row takes its shortfall out
 * of the ancestry and the name stays on screen; the row still carries the
 * whole path as its title.
 *
 * Spec: §13.10
 */
export function RailPathLabel({ path }: { path: string }) {
  const { lead, name } = splitDomainLabel(path);
  return (
    <>
      {lead !== '' && <span className="catalog-lead">{lead}</span>}
      <span className="catalog-name">{name}</span>
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
 * SurfacedLabel marks a notable entry the domain author did not feature. The
 * §4.5.5 notable list is drawn from two sources, and a row that carries no
 * mark at all leaves the reader to infer the second source from the absence
 * of the first, which is only legible to a reader who already knows the
 * curated badge exists. Naming both halves states the distinction the
 * response draws.
 *
 * It is a quiet label rather than a badge: the ranked half is the default
 * case and the far larger one, so an outlined pill on every row of a listing
 * competes with the type and the version beside it and the curated star stops
 * standing out.
 *
 * Spec: §4.5.5
 */
export function SurfacedLabel() {
  return <span className="quiet label">SURFACED BY USAGE</span>;
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
 * SensitivityBadge renders the artifact's sensitivity classification in a
 * listing or result row's metadata line. The value alone ("internal",
 * "confidential") does not say which axis it measures, so the badge names the
 * axis and carries the same weight as the type and version badges beside it:
 * the classification is informational and never an alert. The badge is absent
 * on an unclassified artifact. It is a row-level mark, so the artifact
 * viewer's header does not draw it; the rail's frontmatter table states the
 * classification on the artifact's own page.
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
 * envelope, so what it renders, on the error page and in the banner alike, is
 * what an artifact that does not exist renders, down to the code it states.
 * Both failure treatments run it, because a refusal concealed on one surface
 * and stated on another discloses through the difference. The single-artifact
 * read conceals the denial in the registry today (`pkg/registry/core`), and the
 * concealment §13.10 requires is a property of what the UI renders rather than
 * of that invariant: a read route that later answered a refusal directly would
 * otherwise disclose, through the eyebrow, the message, and the code, that an
 * artifact the caller may not see exists.
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
  children,
  testID,
}: {
  error: unknown;
  /** What did not load, in the page's own words: "No such artifact". */
  title: string;
  /** The domain path or artifact ID the route named. The sentence under the
   * title states it, so the reader sees what was asked for. */
  subject?: string;
  onRetry?: () => void;
  /** A recovery control belonging to the route that failed, placed first in
   * the action row. A route that named something narrower than the catalog
   * has somewhere nearer to send the reader than the catalog root, and this
   * is where that control goes. */
  children?: ReactNode;
  testID?: string;
}) {
  const shown = concealRefusal(error);
  const envelope = shown instanceof ApiError ? shown : null;
  const kind = errorPageKind(shown);
  const label = kind === 'notFound' ? 'NOT FOUND' : kind === 'unavailable' ? 'REGISTRY UNREACHABLE' : 'REFUSED';
  // The caller's title states that what the route named is not there ("No
  // such domain"), which only a not-found belongs to. Every other refusal
  // leaves the artifact or the domain where it was: an argument the registry
  // rejected, a quota, or a status written in front of it says nothing about
  // whether the identifier resolves, and the catalog behind the page goes on
  // listing it. Announcing those as absent states something false, so the
  // page reports the refusal and leaves the code line to name it.
  const heading =
    kind === 'unavailable' ? "Can't reach the registry" : kind === 'notFound' ? title : 'The request was refused';
  // The route named something that did not load, so naming the document from
  // the route names a thing this page says is not there. The page names it
  // instead, in the same words it heads itself with.
  useFailureTitle(heading);
  const offerRetry = onRetry !== undefined && (envelope === null || envelope.retryable);
  // A retry that refuses again is rendered as a fresh control, so the control
  // that was pressed takes the focus back rather than leaving the reader on
  // the document body.
  const retryControl = useRetryFocus<HTMLButtonElement>();
  const offerRecovery = children !== undefined && children !== null && children !== false;
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
        {(offerRetry || offerRecovery || offerBack) && (
          <div className="error-actions">
            {offerRetry && (
              <button
                type="button"
                className="button primary"
                ref={retryControl}
                onClick={() => {
                  requestRetryFocus();
                  onRetry();
                }}
              >
                Retry
              </button>
            )}
            {children}
            {offerBack && (
              <a className={offerRetry || offerRecovery ? 'button' : 'button primary'} href={domainHref('')}>
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
 *
 * A read's permission refusal is concealed here as it is on the error page,
 * because §13.10's concealment rule is a property of what the UI renders and
 * not of which of the two failure treatments a surface happened to reach.
 *
 * Spec: §13.10
 */
export function ErrorState({
  error,
  onRetry,
  title = 'The registry did not answer this request.',
  testID,
  children,
  write = false,
}: {
  error: unknown;
  onRetry?: () => void;
  title?: string;
  testID?: string;
  children?: ReactNode;
  /** The banner reports a write's refusal rather than a read's. A write names
   * something the caller already knows is there, so its permission refusal
   * conceals nothing and the caller is told the decision that was made: the
   * layer panel presents the not-permitted state a refused write receives,
   * and a concealed one would read as a layer that had disappeared. */
  write?: boolean;
}) {
  const shown = write ? error : concealRefusal(error);
  const envelope = shown instanceof ApiError ? shown : null;
  const retryable = envelope === null || envelope.retryable;
  const retryControl = useRetryFocus<HTMLButtonElement>();
  return (
    <div className="banner banner-danger" role="alert" data-testid={testID}>
      <p className="banner-title">{title}</p>
      {envelope !== null && <p className="mono banner-code">{envelope.label}</p>}
      <p>{envelope !== null ? envelopeMessage(envelope) : String(shown)}</p>
      {envelope !== null && envelope.suggestedAction !== '' && <p className="quiet">{envelope.suggestedAction}</p>}
      {onRetry !== undefined &&
        (retryable ? (
          <button
            type="button"
            ref={retryControl}
            onClick={() => {
              requestRetryFocus();
              onRetry();
            }}
          >
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
  wide = false,
  handBack,
  children,
}: {
  title: string;
  description?: string;
  onClose: () => void;
  dismissible?: boolean;
  /** wide draws the dialog at the wider of the two widths the boards use. It
   * is for content that divides the body between several columns, where the
   * standard width leaves each column too narrow for its own label. */
  wide?: boolean;
  /** handBack names the control focus returns to when the dialog closes, in
   * place of whatever held focus as it opened. A dialog that replaces
   * another one over the same surface needs it: the dialog it replaces is
   * what held focus, and it is unmounting, so the default hands focus to a
   * detached node and the reader is left on the document body. */
  handBack?: (opener: HTMLElement | null) => HTMLElement | null;
  children: ReactNode;
}) {
  const headingID = useId();
  // The title identifies the dialog: a caller that replaces a form with the
  // outcome of its submission renders a second Modal in the first one's
  // place, and React reuses the element, so the title is what tells the focus
  // move that the dialog behind it is a different one.
  const dialog = useDialogFocus<HTMLDivElement>(true, title, handBack);
  // The dialog is pinned to the viewport, so the surface it covers is held
  // still under it. A wheel over the scrim would otherwise scroll the page
  // behind the dialog, which is worst on the one-time secret, where the
  // reader has no way back to what they were reading.
  useScrollLock();
  // The dialog says page-wide that it is open, so an accelerator in the shell
  // does not open an overlay underneath it. A dialog that withholds every
  // dismissal route says that too, and the shell keeps the address bar on the
  // route it opened on for as long as it stands.
  useEffect(() => registerModal(!dismissible), [dismissible]);
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
      <div
        ref={dialog}
        className={wide ? 'modal modal-wide' : 'modal'}
        role="dialog"
        aria-modal="true"
        aria-labelledby={headingID}
      >
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
 *
 * A tab's count follows its label as quiet mono text after a word space. A
 * bordered chip in that position reads as a control jammed against the label
 * rather than as a figure the label carries, which is why the count takes no
 * border, no fill, and no padding box.
 */
export function TabStrip<Name extends string>({
  label,
  tabs,
  open,
  onOpen,
  children,
}: {
  label: string;
  tabs: { name: Name; label: string; count?: string; countTone?: TabCountTone }[];
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
            {entry.count !== undefined && entry.count !== '' && (
              <>
                {' '}
                <span className={`tab-count tab-count-${entry.countTone ?? 'quiet'}`}>{entry.count}</span>
              </>
            )}
          </button>
        ))}
      </div>
      <div
        role="tabpanel"
        id={`${prefix}panel-${open}`}
        aria-labelledby={`${prefix}tab-${open}`}
        // The panel is its own Tab stop. A panel holding only prose has no
        // focusable descendant, so without this the Tab order steps from the
        // tab strip past the content the strip selects, and a keyboard reader
        // can neither reach nor scroll it.
        tabIndex={0}
      >
        {children}
      </div>
    </>
  );
}
