// The overlap read the two similarity screens share: the similarity page
// reads the whole report and the artifact viewer reads one artifact's. Both
// carry the same threshold control, so the read, the threshold the reader
// chose, and the report held across a re-read live in one hook.

import { useState } from 'react';

import type { OverlapReport } from '../api';
import { overlapReport } from '../api';
import { clampThreshold } from '../similarity';
import { useAsync, useDebounced } from '../useAsync';

/** OverlapRead is the state of the overlap read a screen renders from. */
export interface OverlapRead {
  /** report is the last report that answered for this artifact, held while
   * a re-read at another threshold is in flight, so the threshold control the
   * reader is dragging stays mounted rather than giving way to a spinner. */
  report: OverlapReport | null;
  loading: boolean;
  error: unknown;
  reload: () => void;
  /** threshold is the value the control shows: the reader's choice, or the
   * threshold the report applied until the reader moves the control. It is
   * null where the read carried no semantic check. */
  threshold: number | null;
  setThreshold: (value: number) => void;
}

/** ThresholdChoice holds the threshold a reader chose outside the hook, so
 * a screen whose choice something else also reads keeps it where both can
 * reach it. chosen is null until the reader moves the control. */
export interface ThresholdChoice {
  chosen: number | null;
  setChosen: (value: number | null) => void;
}

/** useOverlapRead reads the §7.3.1 catalog overlap report, restricted to
 * `artifact` where one is named. The first read carries no threshold, so the
 * registry applies its configured one and the control starts there; a value
 * the reader chooses is sent on the re-read and changes which semantic peers
 * the report carries, and nothing else (§13.10). The re-read waits for the
 * control to rest, so a drag across the track costs one request rather than
 * one per step. A screen that passes `choice` keeps the reader's threshold
 * there, which is how the similarity page shares it with the sidebar's count;
 * a screen that passes none keeps it in the hook.
 *
 * Spec: §13.10, §7.3.1 */
export function useOverlapRead(artifact?: string, choice?: ThresholdChoice): OverlapRead {
  const [ownChosen, setOwnChosen] = useState<number | null>(null);
  const chosen = choice === undefined ? ownChosen : choice.chosen;
  const setChosen = choice === undefined ? setOwnChosen : choice.setChosen;
  const [held, setHeld] = useState<{ artifact?: string; report: OverlapReport } | null>(null);
  const [readFor, setReadFor] = useState(artifact);
  if (readFor !== artifact) {
    // The viewer survives the route change from one artifact to the next, so
    // a threshold chosen on one artifact would otherwise be applied to the
    // next one's first read and its control would not start at the
    // registry's threshold. Only the hook's own choice is reset here: a
    // shared choice belongs to a screen whose artifact does not change.
    setReadFor(artifact);
    setOwnChosen(null);
  }
  // A reset is applied at once rather than after the pause, so the next
  // artifact's first read never carries the previous artifact's choice.
  const settled = useDebounced(chosen);
  const asked = chosen === null ? null : settled;
  const read = useAsync(
    () => overlapReport({ threshold: asked ?? undefined, artifact }),
    [artifact, asked],
  );
  // A settled answer is the one held. While a read is in flight the hook
  // still reports the previous answer as its value, which on a route change
  // belongs to the previous artifact, so only a settled one is taken.
  if (!read.loading && read.value !== null && held?.report !== read.value) {
    setHeld({ artifact, report: read.value });
  }
  const report = held !== null && held.artifact === artifact ? held.report : null;
  const applied = report?.semantic_overlap.state === 'on' ? report.semantic_overlap.threshold : null;
  return {
    report,
    loading: read.loading,
    error: read.error,
    reload: read.reload,
    threshold: applied === null ? null : clampThreshold(chosen ?? applied),
    setThreshold: setChosen,
  };
}
