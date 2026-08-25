// Reingesting a layer. The registry runs the whole §7.3.1 ingest pipeline
// inside the request, so the call can stay open for a long time and answers
// with a summary of what the snapshot accepted, what it rejected, and what it
// conflicted on. The control therefore carries three states the row does not:
// a pending state while the request is open, the summary the reader dismisses
// once they have read it, and the whole-snapshot rejection the registry
// answers with 409 ingest.immutable_violation.
//
// Every other failure is a refusal of the action rather than a result of it,
// so it is handed back to the row, which presents it the way it presents the
// refusal of any other write.

import { useState } from 'react';

import { Badge } from '../components/primitives';
import type { IngestAdvisory, IngestConflict, IngestRejection, IngestSummary } from '../api';
import { ApiError, reingestLayer } from '../api';

/** immutableViolation is the §6.10 code the registry answers with when every
 * artifact in the snapshot collided with a published version. Nothing was
 * accepted and the layer is unchanged, so the reader is told that rather than
 * being handed a summary of zeroes. */
const immutableViolation = 'ingest.immutable_violation';

type State =
  | { kind: 'idle' }
  | { kind: 'running' }
  | { kind: 'summary'; summary: IngestSummary }
  | { kind: 'rejected'; error: ApiError };

export function ReingestControl({
  layerID,
  readOnly,
  onIngested,
  onRefusal,
}: {
  layerID: string;
  readOnly: boolean;
  onIngested: () => void;
  onRefusal: (err: unknown) => void;
}) {
  const [state, setState] = useState<State>({ kind: 'idle' });

  const start = () => {
    setState({ kind: 'running' });
    reingestLayer(layerID).then(
      (summary) => {
        setState({ kind: 'summary', summary });
        onIngested();
      },
      (err: unknown) => {
        if (err instanceof ApiError && err.code === immutableViolation) {
          setState({ kind: 'rejected', error: err });
          return;
        }
        setState({ kind: 'idle' });
        onRefusal(err);
      },
    );
  };

  const dismiss = () => {
    setState({ kind: 'idle' });
  };

  return (
    <>
      <button type="button" disabled={readOnly || state.kind === 'running'} onClick={start}>
        Reingest
      </button>
      {state.kind === 'running' && (
        <p className="loading" role="status" data-testid="reingest-running">
          <span className="spinner" aria-hidden="true" />
          Reingesting {layerID}. The registry runs the whole pipeline before it answers.
        </p>
      )}
      {state.kind === 'summary' && <IngestReport layerID={layerID} summary={state.summary} onDone={dismiss} />}
      {state.kind === 'rejected' && <SnapshotRejected error={state.error} onDone={dismiss} />}
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
    <div className="ingest-report" role="dialog" aria-label="Reingest result">
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
