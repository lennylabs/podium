// The similarity surface's reading of the §7.3.1 catalog overlap report. The
// report arrives as one diagnostic per check per artifact, and the surface
// presents it as one entry per artifact and one row per peer, so the grouping
// lives here, beside the score formats, rather than in either screen. Nothing
// here fetches: the screens own the read and hand the report in.

import type { OverlapArtifact, OverlapDiagnostic, OverlapReport } from './api';

/** The diagnostic codes the report carries. The semantic code's score is a
 * cosine similarity of two embeddings and the word code's is a word-set
 * similarity of two descriptions, so the two scales are never compared. */
export const semanticCode = 'lint.semantic_overlap';
export const wordCode = 'lint.colliding_descriptions';
export const thinCode = 'lint.thin_description';

/** The range the threshold control covers and the step it moves in. The
 * range is the one §13.10 fixes for the control and §7.3.1 accepts for the
 * report's threshold parameter. */
export const thresholdMin = 0.5;
export const thresholdMax = 1;
export const thresholdStep = 0.01;

/** PeerRow is one artifact another artifact overlaps, with the score of each
 * check that reached it. A peer reached by both checks is one row carrying
 * both scores; a score a check did not produce is absent rather than zero. */
export interface PeerRow {
  peer: OverlapArtifact;
  similarity?: number;
  wordOverlap?: number;
  computedAt?: string;
}

/** ClusterEntry is one artifact that has overlap at the read's threshold: its
 * fields, its overlap diagnostics, its peers, and the strongest score of each
 * kind across them.
 *
 * Spec: §13.10 */
export interface ClusterEntry {
  artifact: OverlapArtifact;
  diagnostics: OverlapDiagnostic[];
  peers: PeerRow[];
  strongestSimilarity?: number;
  strongestWordOverlap?: number;
}

/** semanticOn reports whether the read included the semantic check. When it
 * did not, the surface shows no threshold control and no semantic score.
 *
 * Spec: §13.10 */
export function semanticOn(report: OverlapReport): boolean {
  return report.semantic_overlap.state === 'on';
}

/** isOverlap reports whether a diagnostic is one of the two overlap codes. */
function isOverlap(d: OverlapDiagnostic): boolean {
  return d.code === semanticCode || d.code === wordCode;
}

/** clusterEntries is the similarity page's list: one entry per artifact that
 * carries an overlap diagnostic, combining that artifact's semantic and word
 * overlap diagnostics, in the report's artifact order. A description-length
 * diagnostic is no overlap and makes no entry.
 *
 * Spec: §13.10 */
export function clusterEntries(report: OverlapReport): ClusterEntry[] {
  const byID = new Map<string, OverlapDiagnostic[]>();
  for (const d of report.diagnostics) {
    if (!isOverlap(d)) {
      continue;
    }
    const held = byID.get(d.artifact_id);
    if (held === undefined) {
      byID.set(d.artifact_id, [d]);
    } else {
      held.push(d);
    }
  }
  return [...byID.values()].map((diagnostics) => entryOf(diagnostics));
}

/** overlapEntry is the entry for one artifact, or null where the report
 * carries no overlap diagnostic for it. The viewer reads its own artifact
 * through this, and a hidden or unknown identifier reads as no overlap
 * because the registry answers it with the same empty report.
 *
 * Spec: §13.10 */
export function overlapEntry(report: OverlapReport, id: string): ClusterEntry | null {
  const diagnostics = report.diagnostics.filter((d) => d.artifact_id === id && isOverlap(d));
  return diagnostics.length === 0 ? null : entryOf(diagnostics);
}

/** thinDiagnostic is the description-length diagnostic the report carries
 * for an artifact, or null where it carries none. */
export function thinDiagnostic(report: OverlapReport, id: string): OverlapDiagnostic | null {
  return report.diagnostics.find((d) => d.artifact_id === id && d.code === thinCode) ?? null;
}

function entryOf(diagnostics: OverlapDiagnostic[]): ClusterEntry {
  // The semantic message is stated first, because its peers lead the table
  // below it; the report orders the two codes alphabetically.
  const ordered = [...diagnostics].sort((a, b) => codeRank(a.code) - codeRank(b.code));
  const peers = peerRows(ordered);
  return {
    artifact: {
      artifact_id: ordered[0].artifact_id,
      description: ordered[0].description,
      type: ordered[0].type,
      layer: ordered[0].layer,
    },
    diagnostics: ordered,
    peers,
    strongestSimilarity: strongest(peers.map((row) => row.similarity)),
    strongestWordOverlap: strongest(peers.map((row) => row.wordOverlap)),
  };
}

function codeRank(code: string): number {
  return code === semanticCode ? 0 : 1;
}

/** peerRows merges the peers of an artifact's overlap diagnostics into one
 * row per peer. The semantic peers come first in the registry's order, which
 * is by descending similarity, and a peer reached only by word overlap
 * follows in the registry's order for that check. The rows are not ranked
 * across the two scores: a word-overlap row never moves above a semantic row
 * on its percentage, because the two scales measure different things.
 *
 * Spec: §13.10 */
function peerRows(diagnostics: OverlapDiagnostic[]): PeerRow[] {
  const rows = new Map<string, PeerRow>();
  for (const d of diagnostics) {
    for (const p of d.peers ?? []) {
      const row = rows.get(p.artifact_id) ?? {
        peer: { artifact_id: p.artifact_id, description: p.description, type: p.type, layer: p.layer },
      };
      if (d.code === semanticCode) {
        row.similarity = p.similarity;
        row.computedAt = p.computed_at;
      } else {
        row.wordOverlap = p.similarity;
      }
      rows.set(p.artifact_id, row);
    }
  }
  return [...rows.values()];
}

function strongest(scores: (number | undefined)[]): number | undefined {
  const present = scores.filter((s): s is number => s !== undefined);
  return present.length === 0 ? undefined : Math.max(...present);
}

/** truncate cuts a score down to `digits` decimals rather than rounding it.
 * A similarity of 0.8496 rounded to two decimals reads 0.85, which is the
 * threshold it fell short of; cut, it reads 0.84. The small slack keeps a
 * value computed as 0.8499999 for 0.85 from printing one step low, the same
 * slack the registry applies to the percentage in its own messages. */
function truncate(value: number, digits: number): string {
  const scale = 10 ** digits;
  return (Math.floor(value * scale + 1e-6) / scale).toFixed(digits);
}

/** formatSimilarity states a semantic similarity as the decimal a table row
 * carries, such as 0.912. */
export function formatSimilarity(value: number): string {
  return truncate(value, 3);
}

/** formatStrongest states a semantic similarity at the two decimals the
 * cluster list's strongest column carries, such as 0.91. */
export function formatStrongest(value: number): string {
  return truncate(value, 2);
}

/** formatWordOverlap states a word overlap as the whole percentage the
 * registry's own message names, such as 72%. */
export function formatWordOverlap(value: number): string {
  return `${String(Math.max(0, Math.min(100, Math.floor(value * 100 + 1e-6))))}%`;
}

/** formatThreshold states a threshold at the two decimals the control steps
 * in, or at the precision the registry reported where its configured value
 * carries more. */
export function formatThreshold(value: number): string {
  const fixed = value.toFixed(2);
  return Number(fixed) === value ? fixed : String(value);
}

/** clampThreshold holds a threshold the registry reported inside the range
 * the control covers. The registry validates its configured value against
 * the same range, so this guards the control rather than the data. */
export function clampThreshold(value: number): number {
  return Math.min(thresholdMax, Math.max(thresholdMin, value));
}

const smallNumbers = ['No', 'One', 'Two', 'Three', 'Four', 'Five', 'Six', 'Seven', 'Eight', 'Nine'];

/** countWord opens a sentence with a count: spelled out below ten, as the
 * design's copy does, and in digits from ten up. */
export function countWord(n: number): string {
  return n < smallNumbers.length ? smallNumbers[n] : String(n);
}

/** plural picks the singular or the plural noun for a count. */
export function plural(n: number, one: string, many: string): string {
  return n === 1 ? one : many;
}
