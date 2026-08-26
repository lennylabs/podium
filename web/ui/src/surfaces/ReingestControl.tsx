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

import { Badge } from '../components/primitives';
import type { BreakGlass, IngestAdvisory, IngestConflict, IngestRejection, IngestSummary } from '../api';
import { ApiError } from '../api';

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
  | { kind: 'summary'; summary: IngestSummary }
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
 * separately: the button sits in the bar and ReingestStatus draws underneath
 * the row's controls, where a report has the width to be read. */
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
      {state.kind === 'summary' && <IngestReport layerID={layerID} summary={state.summary} onDone={onDismiss} />}
      {state.kind === 'rejected' && <SnapshotRejected error={state.error} onDone={onDismiss} />}
      {state.kind === 'frozen' && (
        <BreakGlassConfirmation layerID={layerID} error={state.error} onOverride={onStart} onCancel={onDismiss} />
      )}
      {state.kind === 'refused' && <ReingestRefused error={state.error} onRetry={onStart} onDone={onDismiss} />}
    </>
  );
}

/** IngestReport presents what the snapshot did. The counts come first because
 * they say whether anything needs acting on, and the itemised lists follow,
 * because a rejection carries a code and a reason and a conflict names the
 * version to bump. */
function IngestReport({ layerID, summary, onDone }: { layerID: string; summary: IngestSummary; onDone: () => void }) {
  const rejected = summary.rejected ?? [];
  const conflicts = summary.conflicts ?? [];
  const advisories = summary.advisories ?? [];
  return (
    <div className="ingest-report" role="dialog" aria-label={`Reingest result for ${layerID}`}>
      <p className="banner-title">Reingest finished</p>
      <p className="mono quiet">{layerID}</p>
      {summary.accepted === undefined ? (
        // A registry with no ingest runner wired records the request and
        // answers with the intent alone, so there is no summary to wait for.
        <p data-testid="reingest-recorded">
          The request was recorded. This registry runs no ingest pipeline inside the request, so there is no result to
          read.
        </p>
      ) : (
        <ul className="ingest-counts">
          <li>{summary.accepted} accepted</li>
          <li>{summary.idempotent ?? 0} unchanged</li>
          <li>{rejected.length} rejected</li>
          <li>{conflicts.length} conflicts</li>
          <li>{summary.lint_failures ?? 0} lint failures</li>
        </ul>
      )}
      {rejected.length > 0 && <RejectionList rejections={rejected} />}
      {conflicts.length > 0 && <ConflictList conflicts={conflicts} />}
      {advisories.length > 0 && <AdvisoryList advisories={advisories} />}
      <button type="button" onClick={onDone}>
        Done
      </button>
    </div>
  );
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
 * snapshot, each with its severity, so a reader can tell an advisory apart
 * from a rejection. */
function AdvisoryList({ advisories }: { advisories: IngestAdvisory[] }) {
  return (
    <section aria-label="Advisories">
      <p className="label">Advisories · {advisories.length}</p>
      <ul>
        {advisories.map((advisory) => (
          <li key={`${advisory.artifact_id}:${advisory.code}`}>
            <Badge tone="quiet">{advisory.severity}</Badge> <span className="mono">{advisory.artifact_id}</span>{' '}
            <span className="mono">{advisory.code}</span> {advisory.message}
          </li>
        ))}
      </ul>
    </section>
  );
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
