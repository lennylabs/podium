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

import { useState } from 'react';

import type { Tone } from '../components/primitives';
import { Badge, CopyButton, Modal } from '../components/primitives';
import type { BreakGlass, IngestAdvisory, IngestConflict, IngestRejection, IngestSummary } from '../api';
import { ApiError } from '../api';
import { clock, elapsed } from '../time';

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
  | { kind: 'running' }
  // The summary arm carries when the request opened and when it returned:
  // the pipeline runs inside the request, so how long the reader waited and
  // when the run finished are facts only the caller holds.
  | { kind: 'summary'; summary: IngestSummary; startedAt: number; finishedAt: number }
  | { kind: 'rejected'; error: ApiError }
  | { kind: 'frozen'; error: ApiError }
  | { kind: 'refused'; error: unknown };

export const idleReingest: ReingestState = { kind: 'idle' };

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
  onStart,
}: {
  state: ReingestState;
  readOnly: boolean;
  onStart: (breakGlass?: BreakGlass) => void;
}) {
  return (
    <button
      type="button"
      disabled={readOnly || state.kind === 'running'}
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
}: {
  layerID: string;
  state: ReingestState;
  onStart: (breakGlass?: BreakGlass) => void;
  onDismiss: () => void;
}) {
  return (
    <>
      {state.kind === 'running' && (
        <p className="loading" role="status" data-testid={`reingest-running-${layerID}`}>
          <span className="spinner" aria-hidden="true" />
          Reingesting {layerID}. The registry runs the whole pipeline before it answers.
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

/** advisoryPreview is how many advisories the report lists before it holds
 * the rest behind "See all". A snapshot raises an advisory per artifact, so
 * the list is unbounded and an uncapped one buries the counts and the
 * rejections under it. */
const advisoryPreview = 2;

/** IngestDetail is the itemised list the reader opened. The report presents
 * the counts first, and only the counts the response itemises open anything:
 * `lint_failures` arrives as a bare number, so it is captioned and left
 * un-openable rather than offered as a list that does not exist. */
type IngestDetail = 'rejected' | 'conflicts' | 'advisories';

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
  // A registry with no ingest runner wired records the request and answers
  // with the intent alone, so there is no summary to present.
  const recorded = summary.accepted === undefined;
  const [detail, setDetail] = useState<IngestDetail | null>(null);
  return (
    <Modal
      title="Reingest finished"
      description={`${layerID} · ${elapsed(finishedAt - startedAt)}`}
      onClose={onDone}
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
              <Stat label="accepted" count={summary.accepted ?? 0} />
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
          <>
            <button
              type="button"
              onClick={() => {
                setDetail(null);
              }}
            >
              Back to the counts
            </button>
            {detail === 'rejected' && <RejectionList rejections={rejected} />}
            {detail === 'conflicts' && <ConflictList conflicts={conflicts} />}
            {detail === 'advisories' && <AdvisoryList advisories={advisories} total={advisories.length} />}
          </>
        )}
      </section>
      <div className="modal-foot">
        <span className="modal-foot-note mono quiet">finished {clock(finishedAt)}</span>
        {!recorded && <CopyButton value={summaryText(layerID, summary, finishedAt)} label="Copy summary" />}
        <button type="button" onClick={onDone}>
          Done
        </button>
      </div>
    </Modal>
  );
}

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
  tone?: Tone;
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
 * numbers. Nothing is drawn when the snapshot needs nothing. */
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
          <button
            type="button"
            className="stat-open"
            onClick={() => {
              onOpen('rejected');
            }}
          >
            {rejected} {rejected === 1 ? 'artifact' : 'artifacts'} rejected
          </button>{' '}
          Each carries its code and its reason.
        </div>
      )}
      {conflicts > 0 && (
        <div className="attention-row attention-accent">
          <button
            type="button"
            className="stat-open"
            onClick={() => {
              onOpen('conflicts');
            }}
          >
            {conflicts} immutability {conflicts === 1 ? 'conflict' : 'conflicts'}
          </button>{' '}
          A published version was republished with different content. Bump the version and reingest.
        </div>
      )}
      {lintFailures > 0 && (
        <div className="attention-row">
          <strong>
            {lintFailures} lint {lintFailures === 1 ? 'failure' : 'failures'}.
          </strong>{' '}
          The response carries the count alone. The ingest log names them.
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

function RejectionList({ rejections }: { rejections: IngestRejection[] }) {
  return (
    <section aria-label="Rejected artifacts">
      <p className="label">Rejected · {rejections.length}</p>
      <ul>
        {rejections.map((rejection) => (
          <li key={`${rejection.artifact_id}:${rejection.code}`}>
            <span className="mono">{rejection.artifact_id}</span> <Badge tone="quiet">{rejection.code}</Badge>{' '}
            {rejection.reason}
          </li>
        ))}
      </ul>
    </section>
  );
}

/** ConflictList names the artifact, the version that collided, and both
 * hashes, because a published version is immutable and the author's next
 * action is to bump it. */
function ConflictList({ conflicts }: { conflicts: IngestConflict[] }) {
  return (
    <section aria-label="Immutability conflicts">
      <p className="label">Conflicts · {conflicts.length}</p>
      <ul>
        {conflicts.map((conflict) => (
          <li key={`${conflict.artifact_id}:${conflict.version}`}>
            <span className="mono">
              {conflict.artifact_id}@{conflict.version}
            </span>{' '}
            <Badge tone="quiet">{conflict.code}</Badge> stored <span className="mono">{conflict.old_hash}</span>,
            incoming <span className="mono">{conflict.new_hash}</span>
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
}: {
  advisories: IngestAdvisory[];
  total: number;
  onSeeAll?: () => void;
}) {
  return (
    <section className="advisories" aria-label="Advisories">
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
      <ul>
        {advisories.map((advisory) => (
          <li key={`${advisory.artifact_id}:${advisory.code}`}>
            <p className="advisory-head">
              <Badge tone={severityTone(advisory.severity)}>{advisory.severity.toUpperCase()}</Badge>{' '}
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
 * them, and it offers a retry where the envelope says the condition clears. */
function ReingestRefused({ error, onRetry, onDone }: { error: unknown; onRetry: () => void; onDone: () => void }) {
  const envelope = error instanceof ApiError ? error : null;
  return (
    <div className="banner banner-danger" role="alert" aria-label="Reingest refused">
      <p className="banner-title">The registry refused this reingest and the layer is unchanged.</p>
      <p className="mono banner-code">{envelope?.code ?? 'registry.unavailable'}</p>
      <p>{envelope !== null ? envelope.message : String(error)}</p>
      {envelope !== null && envelope.suggestedAction !== '' && <p className="quiet">{envelope.suggestedAction}</p>}
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
      <button type="button" onClick={onDone}>
        Dismiss
      </button>
    </div>
  );
}
