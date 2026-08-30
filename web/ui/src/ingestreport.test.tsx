// The finished-reingest report. The cases render the control directly rather
// than driving a reingest through the panel, because the elapsed time and the
// finished stamp are the caller's own clock and a case that pins them has to
// fix it. The panel's wiring of that clock is driven through the UI's API
// calls in surfaces.test.tsx.

import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import type { IngestSummary } from './api';
import { ApiError } from './api';
import type { ReingestOutcome } from './surfaces/ReingestControl';
import { ReingestRunReport, ReingestStatus, runText, summaryText } from './surfaces/ReingestControl';
import { unregisteredOn } from './surfaces/recovery';
import { clock, elapsed, stopwatch } from './time';

import './index.css';

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
      onStopWaiting={() => undefined}
    />,
  );
  return screen.getByLabelText('Reingest result for acme/platform-artifacts');
}

afterEach(() => {
  cleanup();
});

/** specificity scores a compound selector as (ids, classes, elements), which
 * is enough for the flat selectors this sheet carries. */
function specificity(selector: string): number {
  const ids = selector.match(/#[\w-]+/g)?.length ?? 0;
  const classes = selector.match(/[.:[][\w-]+/g)?.length ?? 0;
  const elements = selector.match(/(^|[\s>+~])[a-z]+/g)?.length ?? 0;
  return ids * 10000 + classes * 100 + elements;
}

/** cascaded resolves what the sheet gives property for element, by
 * specificity and then by source order. jsdom's getComputedStyle does not
 * apply an author sheet, so the case reads the rules itself. The `font`
 * shorthand is expanded, because a button reset states its type that way. */
function cascaded(element: Element, property: string): string {
  let winner = '';
  let best = -1;
  for (const sheet of Array.from(document.styleSheets)) {
    for (const rule of Array.from(sheet.cssRules)) {
      if (!(rule instanceof CSSStyleRule)) {
        continue;
      }
      for (const selector of rule.selectorText.split(',')) {
        const trimmed = selector.trim();
        if (!element.matches(trimmed)) {
          continue;
        }
        const value = rule.style.getPropertyValue(property) || rule.style.getPropertyValue('font');
        if (value === '') {
          continue;
        }
        const score = specificity(trimmed);
        if (score >= best) {
          best = score;
          winner = value;
        }
      }
    }
  }
  return winner;
}

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
    // A snapshot that accepted artifacts is the one outcome the report reads
    // as good, so the accepted count carries the success tone. This response
    // itemised no artifacts, so the count opens nothing.
    expect(within(counts).getByText('accepted').parentElement?.className).toContain('stat-ok');
  });

  // Every count is drawn at one size and one weight, and the tone alone says
  // which one needs acting on. A count the response itemises is a button, and
  // a button states its own type, so the case pins the two counts the reader
  // most needs against the counts beside them.
  it('draws an itemised count in the same type as the counts beside it', () => {
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
    const type = (label: string) => {
      const element = within(counts).getByText(label).previousSibling as Element;
      return ['font-family', 'font-size', 'font-weight'].map((property) => cascaded(element, property)).join(' ');
    };
    const plain = type('unchanged');
    expect(plain).toBe('var(--font-mono) 22px 700');
    for (const label of ['accepted', 'rejected', 'conflicts', 'lint failures']) {
      expect(type(label)).toBe(plain);
    }
  });

  // The response itemises the pairs the snapshot left in the layer and marks
  // which of them it newly stored, so the accepted count opens its own list
  // rather than the unchanged ones alongside it.
  it('opens the accepted count onto the artifacts the snapshot newly stored', () => {
    report({
      accepted: 2,
      idempotent: 1,
      artifacts: [
        { id: 'platform/deploy', version: '2.0.0', status: 'accepted' },
        { id: 'platform/lint', version: '1.4.0', status: 'unchanged' },
        { id: 'platform/release', version: '3.1.0', status: 'accepted' },
      ],
    });
    const counts = screen.getByLabelText('Ingest counts');
    fireEvent.click(within(counts).getByRole('button', { name: '2' }));
    const listed = within(screen.getByLabelText('Accepted artifacts')).getAllByRole('listitem');
    expect(listed.map((item) => item.textContent)).toEqual(['platform/deploy@2.0.0', 'platform/release@3.1.0']);
  });

  // Nothing accepted is nothing to open, and nothing to tone as an outcome
  // the reader can read as good.
  it('leaves an accepted count of zero inert and untoned', () => {
    report({
      accepted: 0,
      idempotent: 1,
      artifacts: [{ id: 'platform/lint', version: '1.4.0', status: 'unchanged' }],
    });
    const counts = screen.getByLabelText('Ingest counts');
    expect(within(counts).getByText('accepted').parentElement?.className).toContain('stat-neutral');
    expect(within(counts).queryAllByRole('button').length).toBe(0);
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
    fireEvent.click(screen.getByRole('button', { name: 'Back to summary' }));
    fireEvent.click(screen.getByRole('button', { name: '1 immutability conflict' }));
    expect(screen.getByText('platform/lint@1.0.0')).toBeTruthy();
  });

  // The response itemises four independent lists over one run, and a reader
  // who arrived at one of them compares it with the others. The lists are
  // therefore a tab set over one panel, and only a list the response carries
  // gets a tab.
  it('draws the itemised lists as a tab set carrying each list’s count', () => {
    report({
      accepted: 2,
      idempotent: 0,
      artifacts: [{ id: 'platform/deploy', version: '2.0.0', status: 'accepted' }],
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
    fireEvent.click(screen.getByRole('button', { name: '1 immutability conflict' }));
    const tabs = within(screen.getByRole('tablist')).getAllByRole('tab');
    expect(tabs.map((tab) => tab.textContent)).toEqual(['Accepted 1', 'Rejected 1', 'Conflicts 1']);
    // The list the reader opened is the selected tab, and the panel under the
    // strip is that list.
    expect(screen.getByRole('tab', { name: /Conflicts/ }).getAttribute('aria-selected')).toBe('true');
    expect(within(screen.getByRole('tabpanel')).getByLabelText('Immutability conflicts')).toBeTruthy();
    // Another list is reached from the strip rather than by leaving the
    // itemised half and opening a count again.
    fireEvent.click(screen.getByRole('tab', { name: /Rejected/ }));
    expect(within(screen.getByRole('tabpanel')).getByText('above the floor')).toBeTruthy();
    expect(screen.queryByLabelText('Immutability conflicts')).toBeNull();
  });

  // An entry carries an identifier, its §6.10 code, and a message of no
  // bounded length, so it is a bordered card. The browser's default disc
  // marker sets it as a bulleted line instead.
  it('draws each itemised entry as a card rather than a bulleted line', () => {
    report({
      accepted: 0,
      idempotent: 0,
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
    fireEvent.click(screen.getByRole('button', { name: '1 immutability conflict' }));
    const conflicts = screen.getByLabelText('Immutability conflicts');
    expect(conflicts.querySelector('ul')?.className).toContain('ingest-entries');
    const entry = within(conflicts).getByRole('listitem');
    expect(entry.className).toContain('ingest-entry');
    // Both hashes are labelled, because the reader compares two long runs
    // that differ somewhere in the middle.
    expect(within(entry).getByText('stored').nextElementSibling?.textContent).toBe('sha256:aaa');
    expect(within(entry).getByText('incoming').nextElementSibling?.textContent).toBe('sha256:bbb');
  });

  // A real §6.4 digest is 64 characters. Set whole it is a run of hex that
  // wraps across lines and states nothing the reader can hold, so it is
  // elided the way every other hash in this UI is, with the whole value kept
  // on the title for a reader who has to copy it.
  it('elides the middle of each conflicting hash and keeps the whole one on the title', () => {
    const stored = `sha256:${'9c1f4e02'}${'0'.repeat(52)}a04b`;
    const incoming = `sha256:${'2b77af51'}${'0'.repeat(52)}e39c`;
    report({
      accepted: 0,
      idempotent: 0,
      conflicts: [
        {
          artifact_id: 'platform/lint',
          version: '2.3.0',
          old_hash: stored,
          new_hash: incoming,
          code: 'ingest.immutable_violation',
        },
      ],
    });
    fireEvent.click(screen.getByRole('button', { name: '1 immutability conflict' }));
    const entry = within(screen.getByLabelText('Immutability conflicts')).getByRole('listitem');
    const storedCell = within(entry).getByText('stored').nextElementSibling;
    const incomingCell = within(entry).getByText('incoming').nextElementSibling;
    expect(storedCell?.textContent).toBe('sha256:9c1f4e02…a04b');
    expect(incomingCell?.textContent).toBe('sha256:2b77af51…e39c');
    expect(storedCell?.getAttribute('title')).toBe(stored);
    expect(incomingCell?.getAttribute('title')).toBe(incoming);
  });

  // The way back is a footer control beside Done, so the two ways out of the
  // itemised half sit together rather than one of them standing over the
  // content it leaves.
  it('returns to the counts from a footer control beside Done', () => {
    report({
      accepted: 0,
      idempotent: 0,
      rejected: [{ artifact_id: 'platform/deploy', code: 'ingest.sensitivity_floor', reason: 'above the floor' }],
    });
    fireEvent.click(screen.getByRole('button', { name: '1 artifact rejected' }));
    expect(screen.queryByLabelText('Ingest counts')).toBeNull();
    const back = screen.getByRole('button', { name: 'Back to summary' });
    const foot = back.closest('.modal-foot');
    expect(foot).toBeTruthy();
    expect(within(foot as HTMLElement).getAllByRole('button').at(-1)?.textContent).toBe('Done');
    fireEvent.click(back);
    expect(screen.getByLabelText('Ingest counts')).toBeTruthy();
    expect(screen.queryByRole('tablist')).toBeNull();
  });

  // The count is a control and the remedy is prose, so an em dash separates
  // them. Abutting the two runs them together into one broken sentence.
  it('separates each attention count from its remedy with an em dash', () => {
    report({
      accepted: 0,
      idempotent: 1,
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
    const rows = attention.querySelectorAll('.attention-row');
    expect(rows.length).toBe(2);
    expect(rows[0].textContent).toBe(
      '!1 artifact rejected — each carries its code and its reason.',
    );
    expect(rows[1].textContent).toBe(
      '⇄1 immutability conflict — a published version was republished with different content. Bump the version and reingest.',
    );
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
    expect(within(screen.getByLabelText('Advisories')).getByText('WARN')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'See all 14' }));
    expect(listed().length).toBe(14);
  });

  // The badge is a fixed narrow marker beside a full-width artifact id, and
  // the pipeline names its severities in full. The badge therefore reads WARN
  // rather than the WARNING the response carries, and a severity with no
  // abbreviation is drawn as it stands.
  it('abbreviates the warning severity badge and leaves the rest as they stand', () => {
    report({ accepted: 1, idempotent: 0, advisories: advisories(2) });
    const list = within(screen.getByLabelText('Advisories'));
    expect(list.getByText('WARN')).toBeTruthy();
    expect(list.queryByText('WARNING')).toBeNull();
    expect(list.getByText('INFO')).toBeTruthy();
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
    expect(screen.getByText(`finished ${clock(finishedAt)}`)).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Copy summary' })).toBeTruthy();
  });

  // The footer holds two presses. Done closes the report and carries the
  // primary fill; the copy control stays outlined beside it, so the dialog
  // says which control dismisses it.
  it('fills Done as the primary action and leaves the copy control outlined', () => {
    report({ accepted: 184, idempotent: 97, lint_failures: 3 });
    const done = screen.getByRole('button', { name: 'Done' });
    expect(done.className.split(/\s+/)).toContain('primary');
    expect(screen.getByRole('button', { name: 'Copy summary' }).className.split(/\s+/)).not.toContain('primary');
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
    expect(text).toContain(`Reingest acme/platform-artifacts finished ${clock(finishedAt)}`);
    expect(text).toContain('184 accepted, 97 unchanged, 1 rejected, 0 conflicts, 3 lint failures, 1 advisories');
    expect(text).toContain('rejected platform/deploy ingest.sensitivity_floor: above the floor');
    expect(text).toContain('warning platform/ci/check-0 lint.thin_description: advisory number 0');
  });
});

describe('the wait a reingest opens', () => {
  // The pipeline runs inside the request, so the press can stay open for
  // minutes and the registry reports nothing while it does. The wait
  // therefore states how long it has been open and offers the reader a way
  // off it, rather than leaving one sentence under the row.
  it('states the running pipeline, the clock, and the way off the wait', () => {
    vi.useFakeTimers();
    try {
      const openedAt = Date.now() - 47_000;
      render(
        <ReingestStatus
          layerID="acme/platform-artifacts"
          state={{ kind: 'running', startedAt: openedAt, watching: true }}
          onStart={() => undefined}
          onDismiss={() => undefined}
          onStopWaiting={() => undefined}
        />,
      );
      const dialog = screen.getByRole('dialog', { name: 'Reingesting acme/platform-artifacts' });
      expect(dialog.textContent).toContain('Running the ingest pipeline');
      expect(dialog.textContent).toContain('The registry reports nothing until the request returns');
      expect(dialog.textContent).toContain('Elapsed 0:47');
      expect(dialog.textContent).toContain(`started ${clock(openedAt)}`);
      expect(within(dialog).getByRole('button', { name: 'Stop waiting' })).toBeTruthy();
    } finally {
      vi.useRealTimers();
    }
  });

  // The clock is the one part of the wait that changes while the reader
  // watches, so it advances on its own rather than reporting the moment the
  // dialog mounted for as long as the request stays open.
  it('advances the clock while the request stays open', () => {
    vi.useFakeTimers();
    try {
      const openedAt = Date.now();
      render(
        <ReingestStatus
          layerID="acme/platform-artifacts"
          state={{ kind: 'running', startedAt: openedAt, watching: true }}
          onStart={() => undefined}
          onDismiss={() => undefined}
          onStopWaiting={() => undefined}
        />,
      );
      const dialog = screen.getByRole('dialog', { name: 'Reingesting acme/platform-artifacts' });
      expect(dialog.textContent).toContain('Elapsed 0:00');
      act(() => {
        vi.advanceTimersByTime(65_000);
      });
      expect(dialog.textContent).toContain('Elapsed 1:05');
    } finally {
      vi.useRealTimers();
    }
  });

  // Stopping the wait closes the dialog alone. The request is with the
  // registry, so the row keeps saying its reingest is running.
  it('keeps the row running once the reader stops waiting', () => {
    vi.useFakeTimers();
    try {
      const openedAt = Date.now() - 47_000;
      render(
        <ReingestStatus
          layerID="acme/platform-artifacts"
          state={{ kind: 'running', startedAt: openedAt, watching: false }}
          onStart={() => undefined}
          onDismiss={() => undefined}
          onStopWaiting={() => undefined}
        />,
      );
      expect(screen.queryByRole('dialog')).toBeNull();
      const running = screen.getByTestId('reingest-running-acme/platform-artifacts');
      expect(running.textContent).toContain('Reingesting acme/platform-artifacts for 0:47');
    } finally {
      vi.useRealTimers();
    }
  });

  it('states a running wait as a ticking counter', () => {
    expect(stopwatch(0)).toBe('0:00');
    expect(stopwatch(47_000)).toBe('0:47');
    expect(stopwatch(65_400)).toBe('1:05');
    expect(stopwatch(3_723_000)).toBe('1:02:03');
    expect(stopwatch(-5_000)).toBe('0:00');
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

  // The layer panel states two absolute times: the stamp a finished run
  // carries, and the time of day the recovery table gives a same-day
  // unregister. They were read off two different clocks, one rendered in UTC
  // and labelled and one rendered in the reader's zone and unlabelled, so an
  // operator comparing an unregistration against a reingest read a gap the
  // width of their offset from UTC. Both are the reader's own clock now, and
  // both name the zone.
  it("states both of the panel's absolute times on the reader's clock, zoned", () => {
    // The stamps are read through the platform's local-time getters, which
    // Node resolves from TZ on each call, so the case fixes a zone west of
    // UTC rather than depending on the one the suite happens to run in.
    vi.stubEnv('TZ', 'America/Los_Angeles');
    try {
      const at = Date.UTC(2026, 7, 26, 18, 54, 2);
      expect(clock(at)).toBe('11:54:02 PDT');
      expect(unregisteredOn(new Date(at), at)).toBe('today, 11:54 PDT');
    } finally {
      vi.unstubAllEnvs();
    }
  });
});


/** runOutcomes is a fan-out over three layers: two the registry ran and one
 * it refused. */
const runOutcomes: ReingestOutcome[] = [
  {
    layerID: 'acme/platform-artifacts',
    kind: 'summary',
    summary: { accepted: 12, idempotent: 3, lint_failures: 2, advisories: advisories(3) },
  },
  {
    layerID: 'acme/finance',
    kind: 'summary',
    summary: {
      accepted: 4,
      idempotent: 1,
      rejected: [{ artifact_id: 'finance/pay', code: 'ingest.sensitivity_floor', reason: 'above the floor' }],
    },
  },
  {
    layerID: 'acme/ops',
    kind: 'refused',
    error: new ApiError(422, 'registry.invalid_config', 'source: invalid_config: git source requires ref', false, ''),
  },
];

describe('the finished fan-out report', () => {
  // The fan-out is one press, so it answers with one surface: the run's own
  // counts, added up across the layers that answered with a summary.
  it('states the run and adds the counts up across every layer', () => {
    render(
      <ReingestRunReport outcomes={runOutcomes} startedAt={startedAt} finishedAt={finishedAt} onDone={() => undefined} />,
    );
    const dialog = screen.getByRole('dialog', { name: /Reingest all finished/ });
    expect(dialog.textContent).toContain('3 layers');
    expect(dialog.textContent).toContain(elapsed(finishedAt - startedAt));
    const counts = within(dialog).getByLabelText('Ingest counts across the run');
    expect(within(counts).getByText('16')).toBeTruthy();
    expect(within(counts).getByText('4')).toBeTruthy();
    expect(within(counts).getByText('2')).toBeTruthy();
    // Each layer states what its own response carried, including the lint
    // failures the run's aggregate count is made of.
    const layers = within(dialog).getByLabelText('What each layer returned');
    expect(layers.textContent).toContain('12 accepted · 3 unchanged · 0 rejected · 0 conflicts · 2 lint failures');
    expect(layers.textContent).toContain('4 accepted · 1 unchanged · 1 rejected · 0 conflicts · 0 lint failures');
  });

  // The fan-out is client-side, so each layer's itemised rows are already in
  // hand. The run report states what needs attention on the same terms the
  // single-layer report states it, and opens the lists behind the counts it
  // itemises.
  it('names what needs attention across the run and opens the lists behind it', () => {
    render(
      <ReingestRunReport outcomes={runOutcomes} startedAt={startedAt} finishedAt={finishedAt} onDone={() => undefined} />,
    );
    const attention = screen.getByLabelText('Needs attention');
    expect(attention.textContent).toContain('1 artifact rejected');
    expect(attention.textContent).toContain('2 lint failures');
    fireEvent.click(within(attention).getByRole('button', { name: '1 artifact rejected' }));
    const rejected = screen.getByLabelText('Rejected artifacts');
    expect(rejected.textContent).toContain('finance/pay');
    expect(rejected.textContent).toContain('ingest.sensitivity_floor');
  });

  // The advisories every layer raised are listed under the same cap the
  // single-layer report applies, with the rest behind a count of them.
  it('lists the advisories the run raised and holds the rest behind see-all', () => {
    render(
      <ReingestRunReport outcomes={runOutcomes} startedAt={startedAt} finishedAt={finishedAt} onDone={() => undefined} />,
    );
    const listed = screen.getByLabelText('Advisories');
    expect(listed.textContent).toContain('Advisories · non-blocking · 3');
    expect(within(listed).getAllByRole('listitem')).toHaveLength(2);
    fireEvent.click(within(listed).getByRole('button', { name: 'See all 3' }));
    expect(within(screen.getByLabelText('Advisories')).getAllByRole('listitem')).toHaveLength(3);
  });

  // One run reads the same whichever button started it: the fan-out's counts
  // carry the tones the single-layer report gives them, so an accepted count
  // is not drawn like the unchanged count beside it.
  it('tones the run counts the way the single-layer report tones them', () => {
    render(
      <ReingestRunReport outcomes={runOutcomes} startedAt={startedAt} finishedAt={finishedAt} onDone={() => undefined} />,
    );
    const counts = within(screen.getByRole('dialog', { name: /Reingest all finished/ })).getByLabelText(
      'Ingest counts across the run',
    );
    expect(within(counts).getByText('accepted').parentElement?.className).toContain('stat-ok');
    expect(within(counts).getByText('unchanged').parentElement?.className).toContain('stat-neutral');
    expect(within(counts).getByText('rejected').parentElement?.className).toContain('stat-danger');
  });

  // A refused layer is part of the run's result rather than a banner behind
  // it, and it is named with the code and the message its envelope carried.
  it('names the refused layers with their code and message', () => {
    render(
      <ReingestRunReport outcomes={runOutcomes} startedAt={startedAt} finishedAt={finishedAt} onDone={() => undefined} />,
    );
    const refused = screen.getByLabelText('Refused layers');
    expect(refused.textContent).toContain('acme/ops');
    expect(refused.textContent).toContain('registry.invalid_config');
    expect(refused.textContent).toContain('git source requires ref');
  });

  // The layer id in that row is what the operator types back into the
  // unregister gate, so it is drawn as a column that keeps its own text width
  // rather than as a flex item the message beside it squeezes down to the
  // longest unbreakable run, which broke `hr-layer` across two lines at its
  // hyphen. jsdom performs no layout, so the case pins the class that carries
  // the declaration; the rendered row is checked against a browser.
  it('keeps the refused layer id on one line', () => {
    render(
      <ReingestRunReport outcomes={runOutcomes} startedAt={startedAt} finishedAt={finishedAt} onDone={() => undefined} />,
    );
    const id = within(screen.getByLabelText('Refused layers')).getByText('acme/ops');
    expect(id.className).toContain('attention-id');
  });

  // The message beside that id was a bare text node in the row's flex
  // container, so a path in it had no element to wrap within and ran past the
  // card's right edge. It is drawn as the same wrapping prose every other
  // attention row uses.
  it('draws the refusal message as prose that wraps within the card', () => {
    render(
      <ReingestRunReport outcomes={runOutcomes} startedAt={startedAt} finishedAt={finishedAt} onDone={() => undefined} />,
    );
    const message = within(screen.getByLabelText('Refused layers')).getByText(/git source requires ref/);
    expect(message.className).toContain('attention-text');
  });

  // The message an `ingest.*` refusal carries has no bounded length, and set
  // on the same line as the layer id and the code badge it was squeezed into
  // the width they left, wrapping into a narrow column against the card's
  // right edge. The id and the badge take a head line of their own and the
  // message takes the card's full width beneath them, which is how the
  // itemised ingest entries beside it are drawn.
  it('sets the refusal message on its own line under the layer id and its code', () => {
    render(
      <ReingestRunReport outcomes={runOutcomes} startedAt={startedAt} finishedAt={finishedAt} onDone={() => undefined} />,
    );
    const refused = screen.getByLabelText('Refused layers');
    const id = within(refused).getByText('acme/ops');
    const message = within(refused).getByText(/git source requires ref/);
    const head = id.parentElement;
    expect(head?.className).toContain('attention-head');
    expect(within(head as HTMLElement).getByText('registry.invalid_config')).toBeTruthy();
    expect(message.parentElement).not.toBe(head);
    expect(message.parentElement?.className).toContain('attention-stack');
  });

  it('copies the whole run out, layer by layer', () => {
    const text = runText(runOutcomes, finishedAt);
    expect(text).toContain(`Reingest all finished ${clock(finishedAt)}: 3 layers`);
    expect(text).toContain('16 accepted, 4 unchanged, 1 rejected, 0 conflicts, 2 lint failures');
    expect(text).toContain('refused acme/ops registry.invalid_config: source: invalid_config: git source requires ref');
    expect(text).toContain('Reingest acme/finance finished');
  });
});

// Both reports lay a row of five stat cards across the dialog body, and the
// standard dialog width leaves each card too narrow for "LINT FAILURES" on
// one line. Both ask for the wide dialog the board draws them at.
describe('the report dialog width', () => {
  it('asks for the wide dialog on the single-layer report', () => {
    report({ layer: 'acme/platform-artifacts', accepted: 4, idempotent: 1, lint_failures: 0 });
    expect(screen.getByRole('dialog', { name: /Reingest finished/ }).className).toContain('modal-wide');
  });

  it('asks for the wide dialog on the fan-out report', () => {
    render(
      <ReingestRunReport outcomes={runOutcomes} startedAt={startedAt} finishedAt={finishedAt} onDone={() => undefined} />,
    );
    expect(screen.getByRole('dialog', { name: /Reingest all finished/ }).className).toContain('modal-wide');
  });
});
