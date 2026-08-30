// Reingesting a layer. The registry runs the whole §7.3.1 ingest pipeline
// inside the request, so the call can stay open for a long time and answers
// with a summary of what the snapshot accepted, what it rejected, and what it
// conflicted on. The control therefore carries states the row does not: a
// pending state while the request is open, the summary the reader dismisses
// once they have read it, the whole-snapshot rejection the registry answers
// with 409 ingest.immutable_violation, the freeze window the caller may
// override, and every other refusal, which is presented with the envelope's
// own message and remediation.
//
// The state lives on the panel rather than here, because the panel runs the
// fan-out across every layer and a row changes only when its own request
// returns.

import { useEffect, useState } from 'react';
import type { RefObject } from 'react';

import type { TabCountTone, Tone } from '../components/primitives';
import { Badge, CopyButton, Modal, TabStrip } from '../components/primitives';
import type {
  BreakGlass,
  IngestAdvisory,
  IngestConflict,
  IngestedArtifact,
  IngestRejection,
  IngestSummary,
} from '../api';
import { ApiError } from '../api';
import { abbreviateHash } from '../hash';
import { clock, elapsed, stopwatch } from '../time';

/** immutableViolation is the §6.10 code the registry answers with when every
 * artifact in the snapshot collided with a published version. Nothing was
 * accepted and the layer is unchanged, so the reader is told that rather than
 * being handed a summary of zeroes. */
const immutableViolation = 'ingest.immutable_violation';

/** frozen is the §6.10 code an active §4.7.2 freeze window answers with. The
 * endpoint takes a break-glass override on the same request, so this arm
 * offers that override rather than only reporting the refusal. */
const frozen = 'ingest.frozen';

/** ReingestState is what one row's reingest is doing. Every arm is reached
 * from that row's own response. */
export type ReingestState =
  | { kind: 'idle' }
  // The running arm carries when the request opened, because the pipeline
  // runs inside it and the caller's own clock is the only thing that can say
  // how long the reader has been waiting. `watching` is whether the wait is
  // held open over the page: a row's own press opens it, "Stop waiting"
  // closes it, and the fan-out never opens it, because the run reports every
  // layer at once rather than one dialog per layer.
  | { kind: 'running'; startedAt: number; watching: boolean }
  // The summary arm carries when the request opened and when it returned:
  // the pipeline runs inside the request, so how long the reader waited and
  // when the run finished are facts only the caller holds.
  | { kind: 'summary'; summary: IngestSummary; startedAt: number; finishedAt: number }
  | { kind: 'rejected'; error: ApiError }
  | { kind: 'frozen'; error: ApiError }
  | { kind: 'refused'; error: unknown };

export const idleReingest: ReingestState = { kind: 'idle' };

/** refusedReingest is whether the state is a refusal the row draws in danger
 * tokens. The panel stacks one Reingest button per layer and draws the
 * refusal banner in a full-width row under the layer it belongs to, so with
 * the trigger left in its ordinary tone nothing on the row says which control
 * produced the banner. The freeze window is not one of these: it answers with
 * a confirmation that offers the override rather than with a refusal the
 * reader can only dismiss. */
export function refusedReingest(state: ReingestState): boolean {
  return state.kind === 'refused' || state.kind === 'rejected';
}

/** reingestRefusal classifies what a reingest request failed with. The
 * whole-snapshot rejection and the freeze window each have a treatment of
 * their own, and every other code the pipeline answers with, including
 * ingest.history_rewritten, ingest.lint_failed, the quota codes,
 * ingest.public_mode_rejects_sensitive, ingest.source_unreachable, and
 * registry.unavailable, is presented with the envelope's own message and
 * remediation rather than a fixed line. */
export function reingestRefusal(err: unknown): ReingestState {
  if (err instanceof ApiError && err.code === immutableViolation) {
    return { kind: 'rejected', error: err };
  }
  if (err instanceof ApiError && err.code === frozen) {
    return { kind: 'frozen', error: err };
  }
  return { kind: 'refused', error: err };
}

/** ReingestButton is the trigger alone. The row's action bar holds a fixed
 * pair of controls, so the trigger and the states it opens are rendered
 * separately: the button sits in the bar, and ReingestStatus draws the states
 * it opens, either under the row's controls or, for the finished report, over
 * the page. */
export function ReingestButton({
  state,
  readOnly,
  held,
  onStart,
  buttonRef,
}: {
  state: ReingestState;
  readOnly: boolean;
  /** held is the panel's own in-flight guard: a fan-out across every layer is
   * open, and this row is one of the layers it reingests. Without it the row
   * trigger and the fan-out see only their own request, and a second POST for
   * the same layer runs the registry's whole ingest pipeline twice over one
   * source at once. */
  held?: boolean;
  onStart: (breakGlass?: BreakGlass) => void;
  /** buttonRef is how the row reaches the trigger it has to hand focus back
   * to. The button disables itself while its request is open, which blurs
   * it, and the banners the request resolves into take their dismissal
   * controls with them when they close. */
  buttonRef?: RefObject<HTMLButtonElement | null>;
}) {
  return (
    <button
      ref={buttonRef}
      type="button"
      // The refusal is drawn on the action that was attempted as well as on
      // the row, so the trigger carries the danger tone for as long as its
      // own refusal stands and clears it when the refusal is dismissed or
      // the next attempt opens.
      className={refusedReingest(state) ? 'action-refused' : undefined}
      disabled={readOnly || held === true || state.kind === 'running'}
      onClick={() => {
        onStart();
      }}
    >
      Reingest
    </button>
  );
}

export function ReingestStatus({
  layerID,
  state,
  onStart,
  onDismiss,
  onStopWaiting,
}: {
  layerID: string;
  state: ReingestState;
  onStart: (breakGlass?: BreakGlass) => void;
  onDismiss: () => void;
  /** onStopWaiting closes the wait the press opened. The request stays open,
   * so the row keeps its running annotation and its held trigger. */
  onStopWaiting: () => void;
}) {
  return (
    <>
      {state.kind === 'running' && state.watching && (
        <ReingestWait layerID={layerID} startedAt={state.startedAt} onStopWaiting={onStopWaiting} />
      )}
      {state.kind === 'running' && !state.watching && (
        <p className="loading" role="status" data-testid={`reingest-running-${layerID}`}>
          <span className="spinner" aria-hidden="true" />
          Reingesting {layerID} for <RunningClock startedAt={state.startedAt} />. The registry runs the whole pipeline
          before it answers.
        </p>
      )}
      {state.kind === 'summary' && (
        <IngestReport
          layerID={layerID}
          summary={state.summary}
          startedAt={state.startedAt}
          finishedAt={state.finishedAt}
          onDone={onDismiss}
        />
      )}
      {state.kind === 'rejected' && <SnapshotRejected error={state.error} onDone={onDismiss} />}
      {state.kind === 'frozen' && (
        <BreakGlassConfirmation layerID={layerID} error={state.error} onOverride={onStart} onCancel={onDismiss} />
      )}
      {state.kind === 'refused' && <ReingestRefused error={state.error} onRetry={onStart} onDone={onDismiss} />}
    </>
  );
}

/** useTick re-renders once a second for as long as it is mounted. Every
 * running clock on this surface reads the caller's own start stamp, so the
 * clock advances only when something asks the component to render again. */
function useTick(): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = setInterval(() => {
      setNow(Date.now());
    }, 1000);
    return () => {
      clearInterval(timer);
    };
  }, []);
  return now;
}

/** RunningClock is how long the open request has been running, ticking. */
function RunningClock({ startedAt }: { startedAt: number }) {
  const now = useTick();
  return <span className="mono">{stopwatch(now - startedAt)}</span>;
}

/** ReingestWait is the wait itself. The registry runs the whole §7.3.1
 * pipeline inside the request and reports nothing until it returns, so a
 * press that can stay open for minutes is drawn as the surface the reader is
 * left on rather than as one line under a row: the spinner says the request
 * is open, the clock says how long it has been open and when it started, and
 * "Stop waiting" gives the reader a way off the dialog.
 *
 * Stopping the wait abandons the wait alone. The request is already with the
 * registry, which runs the pipeline to its end whatever this tab does, so the
 * row keeps its running annotation and its held trigger and still resolves
 * into the report when the response arrives. */
function ReingestWait({
  layerID,
  startedAt,
  onStopWaiting,
}: {
  layerID: string;
  startedAt: number;
  onStopWaiting: () => void;
}) {
  return (
    <Modal
      title={`Reingesting ${layerID}`}
      description="One request, held open until the pipeline finishes. Keep this tab open. Stopping the wait abandons the wait rather than the ingest."
      onClose={onStopWaiting}
    >
      <div className="reingest-wait modal-body" role="status" data-testid={`reingest-running-${layerID}`}>
        <span className="spinner spinner-large" aria-hidden="true" />
        <p className="wait-title">Running the ingest pipeline</p>
        <p className="wait-note">
          The registry reports nothing until the request returns. A large layer can take several minutes.
        </p>
        <p className="wait-clock mono">
          Elapsed <RunningClock startedAt={startedAt} />
          <span className="wait-clock-rule" aria-hidden="true" />
          started {clock(startedAt)}
        </p>
      </div>
      <div className="modal-foot">
        <span className="modal-foot-note quiet">Triggered from the layer&apos;s row.</span>
        <button type="button" className="button-plain" onClick={onStopWaiting}>
          Stop waiting
        </button>
      </div>
    </Modal>
  );
}

/** advisoryPreview is how many advisories the report lists before it holds
 * the rest behind "See all". A snapshot raises an advisory per artifact, so
 * the list is unbounded and an uncapped one buries the counts and the
 * rejections under it. */
const advisoryPreview = 2;

/** IngestDetail is the itemised list the reader opened. The report presents
 * the counts first, and only the counts the response itemises open anything:
 * `lint_failures` arrives as a bare number, so it is captioned and left
 * un-openable rather than offered as a list that does not exist. */
type IngestDetail = 'accepted' | 'rejected' | 'conflicts' | 'advisories';

/** IngestReport presents what the snapshot did. The counts come first
 * because they say whether anything needs acting on, and each count the
 * response itemises opens its own list, because a rejection carries a code
 * and a reason and a conflict names the version to bump.
 *
 * It resolves into a Modal because the report is a result the reader has to
 * read: an artifact id, a rejection reason, and an advisory message are all
 * full-width prose, and the row's actions cell is a fixed narrow column in a
 * grid every other row shares. Rendered into that cell the report widened the
 * table past its section, collapsed the source column, and clipped the
 * advisory text off the right edge. */
function IngestReport({
  layerID,
  summary,
  startedAt,
  finishedAt,
  onDone,
}: {
  layerID: string;
  summary: IngestSummary;
  startedAt: number;
  finishedAt: number;
  onDone: () => void;
}) {
  const rejected = summary.rejected ?? [];
  const conflicts = summary.conflicts ?? [];
  const advisories = summary.advisories ?? [];
  const lintFailures = summary.lint_failures ?? 0;
  // The response itemises the pairs the snapshot left in the layer, carrying
  // both counts the report presents. Only the newly accepted ones back the
  // accepted count, so the rest are dropped rather than shown under a number
  // they are not part of.
  const accepted = (summary.artifacts ?? []).filter((a) => a.status === 'accepted');
  // A registry with no ingest runner wired records the request and answers
  // with the intent alone, so there is no summary to present.
  const recorded = summary.accepted === undefined;
  const [detail, setDetail] = useState<IngestDetail | null>(null);
  return (
    <Modal
      title="Reingest finished"
      description={`${layerID} · ${elapsed(finishedAt - startedAt)}`}
      onClose={onDone}
      wide
    >
      <section className="ingest-report modal-body" aria-label={`Reingest result for ${layerID}`}>
        {recorded && (
          <p data-testid="reingest-recorded">
            The request was recorded. This registry runs no ingest pipeline inside the request, so there is no result to
            read.
          </p>
        )}
        {!recorded && detail === null && (
          <>
            <div className="stats" aria-label="Ingest counts">
              <Stat
                label="accepted"
                count={summary.accepted ?? 0}
                tone="ok"
                onOpen={
                  accepted.length > 0
                    ? () => {
                        setDetail('accepted');
                      }
                    : undefined
                }
              />
              <Stat label="unchanged" count={summary.idempotent ?? 0} />
              <Stat
                label="rejected"
                count={rejected.length}
                tone="danger"
                onOpen={() => {
                  setDetail('rejected');
                }}
              />
              <Stat
                label="conflicts"
                count={conflicts.length}
                tone="accent"
                onOpen={() => {
                  setDetail('conflicts');
                }}
              />
              <Stat label="lint failures" count={lintFailures} caption="count only" />
            </div>
            <NeedsAttention
              rejected={rejected.length}
              conflicts={conflicts.length}
              lintFailures={lintFailures}
              onOpen={setDetail}
            />
            {advisories.length > 0 && (
              <AdvisoryList
                advisories={advisories.slice(0, advisoryPreview)}
                total={advisories.length}
                onSeeAll={
                  advisories.length > advisoryPreview
                    ? () => {
                        setDetail('advisories');
                      }
                    : undefined
                }
              />
            )}
          </>
        )}
        {detail !== null && (
          <IngestDetailTabs
            open={detail}
            onOpen={setDetail}
            accepted={accepted}
            rejected={rejected}
            conflicts={conflicts}
            advisories={advisories}
          />
        )}
      </section>
      <div className="modal-foot">
        <span className="modal-foot-note mono quiet">finished {clock(finishedAt)}</span>
        {!recorded && <CopyButton value={summaryText(layerID, summary, finishedAt)} label="Copy summary" />}
        {detail !== null && <BackToSummary onBack={setDetail} />}
        {/* Done is the press that closes the report, so it carries the primary
            fill. Beside an identically outlined copy control neither button
            reads as the one that dismisses the dialog. */}
        <button type="button" className="button primary" onClick={onDone}>
          Done
        </button>
      </div>
    </Modal>
  );
}

/** StatTone is the tone a count carries. It extends the shared tones with
 * the success tone, which only a count has a use for: a badge marks a state
 * that needs reading, and a snapshot that accepted artifacts needs none. */
type StatTone = Tone | 'ok';

/** Stat is one count. A count the response itemises carries a control that
 * opens that list; a count it does not carries a caption saying so, because
 * a number that looks like every other number and opens nothing reads as a
 * dead control. A tone is applied only where the count is non-zero, so a
 * clean snapshot is not tinted as though it needed acting on. */
function Stat({
  label,
  count,
  tone = 'neutral',
  caption,
  onOpen,
}: {
  label: string;
  count: number;
  tone?: StatTone;
  caption?: string;
  onOpen?: () => void;
}) {
  const applied = count > 0 ? tone : 'neutral';
  return (
    <div className={`stat stat-${applied}`}>
      {onOpen !== undefined && count > 0 ? (
        <button type="button" className="stat-count stat-open" onClick={onOpen}>
          {count}
        </button>
      ) : (
        <span className="stat-count">{count}</span>
      )}
      <span className="stat-label">{label}</span>
      {caption !== undefined && <span className="stat-caption">{caption}</span>}
    </div>
  );
}

/** NeedsAttention states what the counts mean for the reader's next action.
 * A rejection and a conflict have different remedies, and lint failures have
 * no list to open, so each is named rather than left as a number in a row of
 * numbers. Nothing is drawn when the snapshot needs nothing. Each row leads
 * with a glyph in its own gutter and separates the count from the remedy with
 * an em dash, because the two clauses otherwise abut and read as one broken
 * sentence. */
function NeedsAttention({
  rejected,
  conflicts,
  lintFailures,
  onOpen,
}: {
  rejected: number;
  conflicts: number;
  lintFailures: number;
  onOpen: (detail: IngestDetail) => void;
}) {
  if (rejected === 0 && conflicts === 0 && lintFailures === 0) {
    return null;
  }
  return (
    <section className="attention" aria-label="Needs attention">
      <p className="label">Needs attention</p>
      {rejected > 0 && (
        <div className="attention-row attention-danger">
          <span className="attention-glyph" aria-hidden="true">
            !
          </span>
          <p className="attention-text">
            <button
              type="button"
              className="stat-open"
              onClick={() => {
                onOpen('rejected');
              }}
            >
              {rejected} {rejected === 1 ? 'artifact' : 'artifacts'} rejected
            </button>
            {' — '}each carries its code and its reason.
          </p>
        </div>
      )}
      {conflicts > 0 && (
        <div className="attention-row attention-accent">
          <span className="attention-glyph" aria-hidden="true">
            ⇄
          </span>
          <p className="attention-text">
            <button
              type="button"
              className="stat-open"
              onClick={() => {
                onOpen('conflicts');
              }}
            >
              {conflicts} immutability {conflicts === 1 ? 'conflict' : 'conflicts'}
            </button>
            {' — '}a published version was republished with different content. Bump the version
            and reingest.
          </p>
        </div>
      )}
      {lintFailures > 0 && (
        <div className="attention-row">
          <span className="attention-glyph" aria-hidden="true">
            ◎
          </span>
          <p className="attention-text">
            <strong>
              {lintFailures} lint {lintFailures === 1 ? 'failure' : 'failures'}.
            </strong>{' '}
            The response carries the count alone. The ingest log names them.
          </p>
        </div>
      )}
    </section>
  );
}

/** summaryText is the report as plain text, for a reader who carries the
 * outcome into an issue or a chat message. It states the counts and itemises
 * what the response itemised, so the copied text says what the surface says
 * rather than a subset of it. */
export function summaryText(layerID: string, summary: IngestSummary, finishedAt: number): string {
  const rejected = summary.rejected ?? [];
  const conflicts = summary.conflicts ?? [];
  const advisories = summary.advisories ?? [];
  const lines = [
    `Reingest ${layerID} finished ${clock(finishedAt)}`,
    `${summary.accepted ?? 0} accepted, ${summary.idempotent ?? 0} unchanged, ${rejected.length} rejected, ${conflicts.length} conflicts, ${summary.lint_failures ?? 0} lint failures, ${advisories.length} advisories`,
  ];
  for (const rejection of rejected) {
    lines.push(`rejected ${rejection.artifact_id} ${rejection.code}: ${rejection.reason}`);
  }
  for (const conflict of conflicts) {
    lines.push(`conflict ${conflict.artifact_id}@${conflict.version} ${conflict.code}`);
  }
  for (const advisory of advisories) {
    lines.push(`${advisory.severity} ${advisory.artifact_id} ${advisory.code}: ${advisory.message}`);
  }
  return lines.join('\n');
}

/** BackToSummary is the way out of the itemised half. It sits in the footer
 * beside the control that closes the dialog, because both are ways out of
 * what the reader is looking at, and a back control standing over the list it
 * leaves reads as part of that list. */
function BackToSummary({ onBack }: { onBack: (detail: IngestDetail | null) => void }) {
  return (
    <button
      type="button"
      onClick={() => {
        onBack(null);
      }}
    >
      Back to summary
    </button>
  );
}

/** IngestDetailTabs is the itemised half of the report. The response
 * itemises four independent lists over one run, and the reader arrives at one
 * of them from a count and then compares it with the others, so they are
 * drawn as a tab set over the same panel rather than as one list reached by
 * leaving the counts. Only a list the response actually carries gets a tab,
 * because a tab that opens onto nothing is a control that does nothing.
 *
 * Each list opens with a line saying what the entries mean for the layer,
 * because the tab names the list and does not say whether the snapshot
 * applied it.
 *
 * Spec: §7.3.1
 */
function IngestDetailTabs({
  open,
  onOpen,
  accepted,
  rejected,
  conflicts,
  advisories,
}: {
  open: IngestDetail;
  onOpen: (detail: IngestDetail) => void;
  accepted: IngestedArtifact[];
  rejected: IngestRejection[];
  conflicts: IngestConflict[];
  advisories: IngestAdvisory[];
}) {
  type DetailTab = { name: IngestDetail; label: string; count: string; countTone?: TabCountTone };
  const listed = (name: IngestDetail, label: string, count: number, countTone?: TabCountTone): DetailTab[] =>
    count === 0 ? [] : [{ name, label, count: String(count), countTone }];
  const tabs: DetailTab[] = [
    ...listed('accepted', 'Accepted', accepted.length),
    ...listed('rejected', 'Rejected', rejected.length, 'danger'),
    ...listed('conflicts', 'Conflicts', conflicts.length, 'accent'),
    ...listed('advisories', 'Advisories', advisories.length),
  ];
  // A count opens its own tab, so the open one is always among them. The
  // fallback stands for a response whose list is empty behind a count that is
  // not, which leaves the panel on a tab that exists.
  const current = tabs.some((entry) => entry.name === open) ? open : (tabs[0]?.name ?? open);
  if (tabs.length === 0) {
    return null;
  }
  return (
    <TabStrip label="Ingest result lists" tabs={tabs} open={current} onOpen={onOpen}>
      <>
        {current === 'accepted' && <AcceptedList artifacts={accepted} />}
        {current === 'rejected' && <RejectionList rejections={rejected} />}
        {current === 'conflicts' && <ConflictList conflicts={conflicts} />}
        {current === 'advisories' && <AdvisoryList advisories={advisories} total={advisories.length} inTab />}
      </>
    </TabStrip>
  );
}

/** AcceptedList names the (artifact_id, version) pairs the snapshot newly
 * stored. It is what the accepted count opens, because "184 accepted" does
 * not say which versions are now live and the reader's next action is to
 * check one of them. */
function AcceptedList({ artifacts }: { artifacts: IngestedArtifact[] }) {
  return (
    <section className="ingest-detail" aria-label="Accepted artifacts">
      <p className="ingest-detail-lede">Newly stored by the snapshot. Each version is now live in the layer.</p>
      <ul className="ingest-entries">
        {artifacts.map((artifact) => (
          <li className="ingest-entry" key={`${artifact.id}:${artifact.version}`}>
            <p className="ingest-entry-head">
              <span className="mono">
                {artifact.id}@{artifact.version}
              </span>
            </p>
          </li>
        ))}
      </ul>
    </section>
  );
}

/** RejectionList names what the snapshot dropped, with the §6.10 code and the
 * reason the pipeline gave, because the reader's next action is a fix in the
 * source repository and the reason is what names it. */
function RejectionList({ rejections }: { rejections: IngestRejection[] }) {
  return (
    <section className="ingest-detail" aria-label="Rejected artifacts">
      <p className="ingest-detail-lede">Dropped by the snapshot. Everything else in the layer was applied.</p>
      <ul className="ingest-entries">
        {rejections.map((rejection) => (
          <li className="ingest-entry" key={`${rejection.artifact_id}:${rejection.code}`}>
            <p className="ingest-entry-head">
              <span className="mono">{rejection.artifact_id}</span>
              <Badge tone="danger">{rejection.code}</Badge>
            </p>
            <p className="ingest-entry-text">{rejection.reason}</p>
          </li>
        ))}
      </ul>
    </section>
  );
}

/** ConflictList names the artifact, the version that collided, and both
 * hashes, because a published version is immutable and the author's next
 * action is to bump it. The two hashes are set as a labelled pair rather than
 * inline, because they differ in the middle of a long unbreakable run and a
 * reader comparing them has to find where. Each is elided the way every other
 * hash in this UI is, with a wider lead than the artifact rail takes because
 * two hashes read against each other need more digest before they part. The
 * whole hash stays on the title, so a reader who needs to copy it has it. */
function ConflictList({ conflicts }: { conflicts: IngestConflict[] }) {
  return (
    <section className="ingest-detail" aria-label="Immutability conflicts">
      <p className="ingest-detail-lede">
        Each version already exists with different content. A published version is immutable, so bump the version and
        reingest.
      </p>
      <ul className="ingest-entries">
        {conflicts.map((conflict) => (
          <li className="ingest-entry ingest-entry-accent" key={`${conflict.artifact_id}:${conflict.version}`}>
            <p className="ingest-entry-head">
              <span className="mono">
                {conflict.artifact_id}@{conflict.version}
              </span>
              <Badge tone="accent">{conflict.code}</Badge>
            </p>
            <dl className="ingest-hashes mono">
              <dt className="quiet">stored</dt>
              <dd title={conflict.old_hash}>{abbreviateHash(conflict.old_hash ?? '', 8)}</dd>
              <dt className="quiet">incoming</dt>
              <dd title={conflict.new_hash}>{abbreviateHash(conflict.new_hash ?? '', 8)}</dd>
            </dl>
          </li>
        ))}
      </ul>
    </section>
  );
}

/** AdvisoryList carries the flags the pipeline raised without blocking the
 * snapshot. The severity leads each row so a reader can tell an advisory
 * apart from a rejection, the artifact id and the code sit on their own line
 * above the message, and the caller decides how many rows to pass. */
function AdvisoryList({
  advisories,
  total,
  onSeeAll,
  inTab = false,
}: {
  advisories: IngestAdvisory[];
  total: number;
  onSeeAll?: () => void;
  inTab?: boolean;
}) {
  return (
    <section className="advisories" aria-label="Advisories">
      {/* Under its own tab the count is already on the tab, so the heading
          states what the entries mean for the layer instead of repeating it. */}
      {inTab ? (
        <p className="ingest-detail-lede">
          Raised without blocking the snapshot. Everything listed was applied to the layer.
        </p>
      ) : (
        <p className="label">
          Advisories · non-blocking · {total}
          {onSeeAll !== undefined && (
            <>
              {' '}
              <button type="button" className="stat-open" onClick={onSeeAll}>
                See all {total}
              </button>
            </>
          )}
        </p>
      )}
      <ul className="ingest-entries">
        {advisories.map((advisory) => (
          <li className="ingest-entry" key={`${advisory.artifact_id}:${advisory.code}`}>
            <p className="advisory-head">
              <Badge tone={severityTone(advisory.severity)}>{severityLabel(advisory.severity)}</Badge>{' '}
              <span className="mono">{advisory.artifact_id}</span>{' '}
              <span className="mono quiet">{advisory.code}</span>
            </p>
            <p className="advisory-message">{advisory.message}</p>
          </li>
        ))}
      </ul>
    </section>
  );
}

/** severityLabel is what the severity badge reads. The pipeline names its
 * severities in full and the badge is a fixed narrow marker beside a
 * full-width artifact id, so `warning` is abbreviated to the WARN the rest of
 * the surface uses. A severity with no abbreviation is uppercased as it
 * stands, which keeps a severity the linter gains readable without a change
 * here. */
function severityLabel(severity: string): string {
  const abbreviated: Record<string, string> = { warning: 'WARN' };
  return abbreviated[severity.toLowerCase()] ?? severity.toUpperCase();
}

/** severityTone marks a warning apart from the rest. The pipeline raises
 * advisories at more than one severity and only a warning asks the reader to
 * look, so anything else is drawn quiet. */
function severityTone(severity: string): Tone {
  return severity.toLowerCase().startsWith('warn') ? 'accent' : 'quiet';
}

/** SnapshotRejected is the 409 arm: every artifact collided with a published
 * version, so nothing was accepted and the layer is unchanged. The condition
 * does not clear on its own, which is what the envelope's retry signal says,
 * so the surface offers no retry. */
function SnapshotRejected({ error, onDone }: { error: ApiError; onDone: () => void }) {
  const conflicts = (error.details.conflicts ?? []) as IngestConflict[];
  return (
    <div className="banner banner-danger" role="alert" aria-label="Reingest rejected">
      <p className="banner-title">Nothing was ingested</p>
      <p className="mono banner-code">{error.code}</p>
      <p>Every artifact in the snapshot collided with a published version, so the layer is unchanged.</p>
      <p>{error.message}</p>
      {conflicts.length > 0 && <ConflictList conflicts={conflicts} />}
      <p className="quiet">Bump the versions in the source and reingest.</p>
      <button type="button" onClick={onDone}>
        Close
      </button>
    </div>
  );
}

/** BreakGlassConfirmation is the freeze-window arm. §4.7.2 lets an operator
 * run inside a freeze window with a justification and two distinct approvers,
 * and the endpoint reads all three off the same request, so the refusal
 * offers the override rather than leaving the reader with no next action. The
 * override is recorded in the audit log, so the confirmation states that
 * before it is sent. */
function BreakGlassConfirmation({
  layerID,
  error,
  onOverride,
  onCancel,
}: {
  layerID: string;
  error: ApiError;
  onOverride: (breakGlass: BreakGlass) => void;
  onCancel: () => void;
}) {
  const [justification, setJustification] = useState('');
  const [first, setFirst] = useState('');
  const [second, setSecond] = useState('');
  const approvers = [first.trim(), second.trim()].filter((approver) => approver !== '');
  const complete = justification.trim() !== '' && approvers.length === 2 && approvers[0] !== approvers[1];
  return (
    <div className="confirm" role="dialog" aria-label={`Reingest ${layerID} during a freeze window`}>
      <p className="banner-title">A freeze window is open</p>
      <p className="mono banner-code">{error.code}</p>
      <p>{error.message}</p>
      <p>
        Reingesting {layerID} now overrides that window. It takes a justification and two distinct approvers, and the
        registry records the override in the audit log.
      </p>
      <label className="field">
        <span className="label">Justification</span>
        <input
          type="text"
          value={justification}
          onChange={(event) => {
            setJustification(event.target.value);
          }}
        />
      </label>
      <label className="field">
        <span className="label">First approver</span>
        <input
          type="text"
          value={first}
          onChange={(event) => {
            setFirst(event.target.value);
          }}
        />
      </label>
      <label className="field">
        <span className="label">Second approver</span>
        <input
          type="text"
          value={second}
          onChange={(event) => {
            setSecond(event.target.value);
          }}
        />
      </label>
      <button
        type="button"
        disabled={!complete}
        onClick={() => {
          onOverride({ justification: justification.trim(), approvers });
        }}
      >
        Reingest during the freeze
      </button>
      <button type="button" onClick={onCancel}>
        Cancel
      </button>
    </div>
  );
}

/** ReingestRefused presents every other refusal on the terms the envelope
 * states them: its code, its message, and the remediation it carries. The
 * pipeline answers with codes whose next action differs, so the surface
 * carries the envelope's own words rather than one line that fits none of
 * them, and it offers a retry where the envelope says the condition clears.
 *
 * The refusal annotates one row of the panel, so it is drawn as a row
 * annotation: a leading REFUSED marker, the statement and the envelope's
 * words beside it, and the recovery at the band's right edge on the
 * statement's own line. Stacking those under each other gave one row's
 * refusal the height and the weight of a page-level failure. */
function ReingestRefused({ error, onRetry, onDone }: { error: unknown; onRetry: () => void; onDone: () => void }) {
  const envelope = error instanceof ApiError ? error : null;
  return (
    <div className="banner banner-danger banner-annotation" role="alert" aria-label="Reingest refused">
      <Badge tone="danger">REFUSED</Badge>
      <div className="banner-text">
        <p className="banner-title">The registry refused this reingest and the layer is unchanged.</p>
        <p className="banner-detail">
          <span className="mono banner-code">{envelope?.label ?? 'registry.unavailable'}</span>{' '}
          {envelope !== null ? envelope.message : String(error)}
        </p>
        {envelope !== null && envelope.suggestedAction !== '' && (
          <p className="banner-detail">{envelope.suggestedAction}</p>
        )}
      </div>
      <div className="banner-actions">
        {(envelope === null || envelope.retryable) && (
          <button
            type="button"
            onClick={() => {
              onRetry();
            }}
          >
            Try again
          </button>
        )}
        {/* Dismiss closes the band and changes nothing, so it is drawn
            without a border beside the bordered recovery. */}
        <button type="button" className="button-plain" onClick={onDone}>
          Dismiss
        </button>
      </div>
    </div>
  );
}

/** ReingestOutcome is what one layer's request in a fan-out returned. The
 * fan-out reports the whole run at once, so each layer's answer is collected
 * rather than resolved into a report of its own. */
export type ReingestOutcome =
  | { layerID: string; kind: 'summary'; summary: IngestSummary }
  | { layerID: string; kind: 'refused'; error: unknown };

/** counted adds up what the run's summaries carried. A layer the registry
 * only recorded contributes nothing, because it reports no counts. The
 * itemised rows are gathered rather than tallied, so the run report can open
 * the same lists the single-layer report opens instead of presenting a total
 * with nothing behind it. */
function counted(outcomes: ReingestOutcome[]) {
  let accepted = 0;
  let idempotent = 0;
  let lintFailures = 0;
  const rejected: IngestRejection[] = [];
  const conflicts: IngestConflict[] = [];
  const advisories: IngestAdvisory[] = [];
  for (const outcome of outcomes) {
    if (outcome.kind !== 'summary') {
      continue;
    }
    accepted += outcome.summary.accepted ?? 0;
    idempotent += outcome.summary.idempotent ?? 0;
    lintFailures += outcome.summary.lint_failures ?? 0;
    rejected.push(...(outcome.summary.rejected ?? []));
    conflicts.push(...(outcome.summary.conflicts ?? []));
    advisories.push(...(outcome.summary.advisories ?? []));
  }
  return { accepted, idempotent, rejected, conflicts, advisories, lintFailures };
}

/** ReingestRunReport is what "Reingest all" resolves into. The fan-out issues
 * one request per layer, and reporting each one in its own dialog stacked N
 * dialogs over the page, each naming a single layer and none saying which of
 * how many, with a refused layer left as a banner under the stack. The run is
 * one press, so it answers with one surface: the combined counts, a row per
 * layer with what its own response carried, and the layers the registry
 * refused. A refusal that takes a further decision, such as a freeze window,
 * is named here and reingested from its own row, where that decision has its
 * confirmation.
 *
 * The fan-out is client-side, so every layer's rejections, conflicts, and
 * advisories are already in hand. The run report therefore states what needs
 * attention and lists the advisories on the same terms the single-layer
 * report does, and each layer's line carries its own lint failures, so the
 * aggregate lint count is attributable to a layer rather than being a total
 * the reader can only recover from the copied text. */
export function ReingestRunReport({
  outcomes,
  startedAt,
  finishedAt,
  onDone,
}: {
  outcomes: ReingestOutcome[];
  startedAt: number;
  finishedAt: number;
  onDone: () => void;
}) {
  const totals = counted(outcomes);
  const refused = outcomes.filter((outcome) => outcome.kind === 'refused');
  const layerWord = outcomes.length === 1 ? 'layer' : 'layers';
  const [detail, setDetail] = useState<IngestDetail | null>(null);
  return (
    <Modal
      title="Reingest all finished"
      description={`${String(outcomes.length)} ${layerWord} · ${elapsed(finishedAt - startedAt)}`}
      onClose={onDone}
      wide
    >
      <section className="ingest-report modal-body" aria-label="Reingest all result">
        {detail !== null && (
          <IngestDetailTabs
            open={detail}
            onOpen={setDetail}
            accepted={[]}
            rejected={totals.rejected}
            conflicts={totals.conflicts}
            advisories={totals.advisories}
          />
        )}
        {detail === null && (
          <>
            <div className="stats" aria-label="Ingest counts across the run">
              <Stat label="accepted" count={totals.accepted} tone="ok" />
              <Stat label="unchanged" count={totals.idempotent} />
              <Stat
                label="rejected"
                count={totals.rejected.length}
                tone="danger"
                onOpen={() => {
                  setDetail('rejected');
                }}
              />
              <Stat
                label="conflicts"
                count={totals.conflicts.length}
                tone="accent"
                onOpen={() => {
                  setDetail('conflicts');
                }}
              />
              <Stat label="lint failures" count={totals.lintFailures} caption="count only" />
            </div>
            <NeedsAttention
              rejected={totals.rejected.length}
              conflicts={totals.conflicts.length}
              lintFailures={totals.lintFailures}
              onOpen={setDetail}
            />
            {refused.length > 0 && (
              <section className="attention" aria-label="Refused layers">
                <p className="label">Refused · {refused.length}</p>
                {refused.map((outcome) => (
                  <div key={outcome.layerID} className="attention-row attention-stack attention-danger">
                    <p className="attention-head">
                      <span className="mono attention-id">{outcome.layerID}</span>
                      <Badge tone="quiet">{refusalCode(outcome)}</Badge>
                    </p>
                    <p className="attention-text">{refusalMessage(outcome)}</p>
                  </div>
                ))}
                <p className="quiet">
                  Reingest a refused layer from its own row, which carries the remediation its refusal states.
                </p>
              </section>
            )}
            {totals.advisories.length > 0 && (
              <AdvisoryList
                advisories={totals.advisories.slice(0, advisoryPreview)}
                total={totals.advisories.length}
                onSeeAll={
                  totals.advisories.length > advisoryPreview
                    ? () => {
                        setDetail('advisories');
                      }
                    : undefined
                }
              />
            )}
            <section aria-label="What each layer returned">
              <p className="label">Layers · {outcomes.length}</p>
              <ul className="run-layers">
                {outcomes.map((outcome) => (
                  <li key={outcome.layerID}>
                    <span className="mono">{outcome.layerID}</span> <span className="quiet">{layerLine(outcome)}</span>
                  </li>
                ))}
              </ul>
            </section>
          </>
        )}
      </section>
      <div className="modal-foot">
        <span className="modal-foot-note mono quiet">finished {clock(finishedAt)}</span>
        <CopyButton value={runText(outcomes, finishedAt)} label="Copy summary" />
        {detail !== null && <BackToSummary onBack={setDetail} />}
        <button type="button" className="button primary" onClick={onDone}>
          Done
        </button>
      </div>
    </Modal>
  );
}

/** refusalCode and refusalMessage read a refused layer's envelope. A refusal
 * that carries no envelope is a transport failure, which is the code the API
 * surface answers with for one. */
function refusalCode(outcome: ReingestOutcome): string {
  const envelope = outcome.kind === 'refused' && outcome.error instanceof ApiError ? outcome.error : null;
  return envelope?.label ?? 'registry.unavailable';
}

function refusalMessage(outcome: ReingestOutcome): string {
  if (outcome.kind !== 'refused') {
    return '';
  }
  return outcome.error instanceof ApiError ? outcome.error.message : String(outcome.error);
}

/** layerLine states what one layer's response carried, in the same words the
 * single-layer report's cards use. */
function layerLine(outcome: ReingestOutcome): string {
  if (outcome.kind === 'refused') {
    return 'refused';
  }
  if (outcome.summary.accepted === undefined) {
    return 'recorded · this registry runs no pipeline inside the request';
  }
  const summary = outcome.summary;
  return `${String(summary.accepted)} accepted · ${String(summary.idempotent ?? 0)} unchanged · ${String((summary.rejected ?? []).length)} rejected · ${String((summary.conflicts ?? []).length)} conflicts · ${String(summary.lint_failures ?? 0)} lint failures`;
}

/** runText is the whole run as plain text, for a reader who carries the
 * outcome into an issue or a chat message. Each layer contributes what the
 * single-layer copy states, and a refused layer contributes its code and its
 * message. */
export function runText(outcomes: ReingestOutcome[], finishedAt: number): string {
  const totals = counted(outcomes);
  const lines = [
    `Reingest all finished ${clock(finishedAt)}: ${String(outcomes.length)} ${outcomes.length === 1 ? 'layer' : 'layers'}`,
    `${String(totals.accepted)} accepted, ${String(totals.idempotent)} unchanged, ${String(totals.rejected.length)} rejected, ${String(totals.conflicts.length)} conflicts, ${String(totals.lintFailures)} lint failures`,
  ];
  for (const outcome of outcomes) {
    if (outcome.kind === 'refused') {
      lines.push(`refused ${outcome.layerID} ${refusalCode(outcome)}: ${refusalMessage(outcome)}`);
      continue;
    }
    lines.push(summaryText(outcome.layerID, outcome.summary, finishedAt));
  }
  return lines.join('\n');
}
