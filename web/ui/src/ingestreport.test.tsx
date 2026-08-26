// The finished-reingest report. The cases render the control directly rather
// than driving a reingest through the panel, because the elapsed time and the
// finished stamp are the caller's own clock and a case that pins them has to
// fix it. The panel's wiring of that clock is driven through the UI's API
// calls in surfaces.test.tsx.

import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';

import type { IngestSummary } from './api';
import { ReingestStatus, summaryText } from './surfaces/ReingestControl';
import { clock, elapsed } from './time';

const startedAt = Date.UTC(2026, 7, 26, 14, 3, 34);
const finishedAt = Date.UTC(2026, 7, 26, 14, 6, 22);

function advisories(count: number) {
  return Array.from({ length: count }, (_, index) => ({
    artifact_id: `platform/ci/check-${String(index)}`,
    code: 'lint.thin_description',
    severity: index === 0 ? 'warning' : 'info',
    message: `advisory number ${String(index)}`,
  }));
}

function report(summary: IngestSummary) {
  render(
    <ReingestStatus
      layerID="acme/platform-artifacts"
      state={{ kind: 'summary', summary, startedAt, finishedAt }}
      onStart={() => undefined}
      onDismiss={() => undefined}
    />,
  );
  return screen.getByLabelText('Reingest result for acme/platform-artifacts');
}

afterEach(() => {
  cleanup();
});

describe('the finished reingest report', () => {
  // The counts are the first thing the reader acts on, so each is a card
  // carrying the number and its name. A count the response itemises opens
  // that list; lint_failures arrives as a bare number, so it says so and
  // opens nothing.
  it('states each count as a card, and opens only the counts the response itemises', () => {
    report({
      layer: 'acme/platform-artifacts',
      accepted: 184,
      idempotent: 97,
      lint_failures: 3,
      rejected: [{ artifact_id: 'platform/deploy', code: 'ingest.sensitivity_floor', reason: 'above the floor' }],
      conflicts: [
        {
          artifact_id: 'platform/lint',
          version: '1.0.0',
          old_hash: 'sha256:aaa',
          new_hash: 'sha256:bbb',
          code: 'ingest.immutable_violation',
        },
      ],
    });
    const counts = screen.getByLabelText('Ingest counts');
    for (const [label, count] of [
      ['accepted', '184'],
      ['unchanged', '97'],
      ['rejected', '1'],
      ['conflicts', '1'],
      ['lint failures', '3'],
    ]) {
      expect(within(counts).getByText(label).previousSibling?.textContent).toBe(count);
    }
    expect(within(counts).getByText('count only')).toBeTruthy();
    expect(within(counts).getAllByRole('button').length).toBe(2);
    // A non-zero rejected and conflicts count is toned, because those are the
    // two the reader has to act on.
    expect(within(counts).getByText('rejected').parentElement?.className).toContain('stat-danger');
    expect(within(counts).getByText('conflicts').parentElement?.className).toContain('stat-accent');
    expect(within(counts).getByText('accepted').parentElement?.className).toContain('stat-neutral');
  });

  // The counts say how many; the block beside them says what to do about it,
  // and each itemised count opens its list from there.
  it('names what needs attention and opens the list behind each itemised count', () => {
    report({
      accepted: 0,
      idempotent: 1,
      lint_failures: 3,
      rejected: [{ artifact_id: 'platform/deploy', code: 'ingest.sensitivity_floor', reason: 'above the floor' }],
      conflicts: [
        {
          artifact_id: 'platform/lint',
          version: '1.0.0',
          old_hash: 'sha256:aaa',
          new_hash: 'sha256:bbb',
          code: 'ingest.immutable_violation',
        },
      ],
    });
    const attention = screen.getByLabelText('Needs attention');
    expect(within(attention).getByText(/3 lint failures/)).toBeTruthy();
    fireEvent.click(within(attention).getByRole('button', { name: '1 artifact rejected' }));
    expect(within(screen.getByLabelText('Rejected artifacts')).getByText('above the floor')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Back to the counts' }));
    fireEvent.click(screen.getByRole('button', { name: '1 immutability conflict' }));
    expect(screen.getByText('platform/lint@1.0.0')).toBeTruthy();
  });

  // A clean snapshot needs nothing, so nothing is drawn asking for it.
  it('draws no attention block where the snapshot rejected and conflicted on nothing', () => {
    report({ accepted: 2, idempotent: 0, lint_failures: 0 });
    expect(screen.queryByLabelText('Needs attention')).toBeNull();
    expect(screen.getByLabelText('Ingest counts')).toBeTruthy();
  });

  // A snapshot raises an advisory per artifact, so an uncapped list buries
  // the counts under it. The report shows the first of them and holds the
  // rest behind a control that states how many there are.
  it('caps the advisory list and offers the rest behind a count of them', () => {
    report({ accepted: 1, idempotent: 0, advisories: advisories(14) });
    const listed = () => within(screen.getByLabelText('Advisories')).getAllByRole('listitem');
    expect(listed().length).toBe(2);
    // The severity leads each row, so an advisory is not read as a rejection.
    expect(within(screen.getByLabelText('Advisories')).getByText('WARNING')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'See all 14' }));
    expect(listed().length).toBe(14);
  });

  // A list that fits needs no control offering the rest of it.
  it('offers no see-all control where every advisory is listed', () => {
    report({ accepted: 1, idempotent: 0, advisories: advisories(2) });
    expect(screen.queryByRole('button', { name: /See all/ })).toBeNull();
    expect(within(screen.getByLabelText('Advisories')).getAllByRole('listitem').length).toBe(2);
  });

  // The pipeline runs inside the request, so the reader waited for it. The
  // report states how long that was and when the run finished, and it hands
  // over the whole outcome as text.
  it('states how long the run took, when it finished, and copies the outcome out', () => {
    report({ accepted: 184, idempotent: 97, lint_failures: 3 });
    expect(screen.getByText(/acme\/platform-artifacts · 2 minutes 48 seconds/)).toBeTruthy();
    expect(screen.getByText('finished 14:06:22 UTC')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Copy summary' })).toBeTruthy();
  });

  // A registry with no ingest runner wired answers with the intent alone.
  // There is no outcome to card up and none to copy.
  it('presents no counts where the registry ran no pipeline inside the request', () => {
    report({ queued: 'acme/platform-artifacts' });
    expect(screen.getByTestId('reingest-recorded')).toBeTruthy();
    expect(screen.queryByLabelText('Ingest counts')).toBeNull();
    expect(screen.queryByRole('button', { name: 'Copy summary' })).toBeNull();
  });
});

describe('the copied summary', () => {
  // A reader carries the outcome into an issue or a chat message, so the
  // copied text states the counts and itemises what the response itemised.
  it('states the counts and every itemised row', () => {
    const text = summaryText(
      'acme/platform-artifacts',
      {
        accepted: 184,
        idempotent: 97,
        lint_failures: 3,
        rejected: [{ artifact_id: 'platform/deploy', code: 'ingest.sensitivity_floor', reason: 'above the floor' }],
        advisories: advisories(1),
      },
      finishedAt,
    );
    expect(text).toContain('Reingest acme/platform-artifacts finished 14:06:22 UTC');
    expect(text).toContain('184 accepted, 97 unchanged, 1 rejected, 0 conflicts, 3 lint failures, 1 advisories');
    expect(text).toContain('rejected platform/deploy ingest.sensitivity_floor: above the floor');
    expect(text).toContain('warning platform/ci/check-0 lint.thin_description: advisory number 0');
  });
});

describe('the report clock', () => {
  it('states a run under a minute in seconds alone', () => {
    expect(elapsed(48_000)).toBe('48 seconds');
    expect(elapsed(1_000)).toBe('1 second');
  });

  it('states a whole number of minutes without a seconds part', () => {
    expect(elapsed(120_000)).toBe('2 minutes');
    expect(elapsed(60_000)).toBe('1 minute');
    expect(elapsed(168_000)).toBe('2 minutes 48 seconds');
  });

  // A clock that ran backwards between the two reads reports no time at all
  // rather than a negative one.
  it('reports a negative interval as none', () => {
    expect(elapsed(-5_000)).toBe('0 seconds');
  });

  it('states the wall clock in UTC', () => {
    expect(clock(Date.UTC(2026, 7, 26, 4, 6, 2))).toBe('04:06:02 UTC');
  });
});
