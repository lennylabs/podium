import { render } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import type { Async } from './useAsync';
import { useAsync } from './useAsync';

// Every render's view of the hook, in order, with the input it rendered for.
type Seen = { input: string; value: string | null; loading: boolean };

function Probe({ input, seen }: { input: string; seen: Seen[] }) {
  const read: Async<string> = useAsync(async () => `answer for ${input}`, [input]);
  seen.push({ input, value: read.value, loading: read.loading });
  return null;
}

describe('useAsync', () => {
  // A caller reading a settled value treats it as the answer to the inputs it
  // rendered with. On the render where the inputs change, the hook still holds
  // the previous read's value, so it must report that render as loading
  // rather than as the settled answer to the new inputs.
  it('reports the render where the inputs change as loading', async () => {
    const seen: Seen[] = [];
    const view = render(<Probe input="first" seen={seen} />);
    await expect.poll(() => seen.at(-1)?.value).toBe('answer for first');
    expect(seen.at(-1)?.loading).toBe(false);

    seen.length = 0;
    view.rerender(<Probe input="second" seen={seen} />);
    await expect.poll(() => seen.at(-1)?.value).toBe('answer for second');

    // No render for the new input reported the previous answer as settled.
    const misreported = seen.filter(
      (s) => s.input === 'second' && !s.loading && s.value !== 'answer for second',
    );
    expect(misreported).toEqual([]);
    expect(seen[0]).toEqual({ input: 'second', value: 'answer for first', loading: true });
  });

  it('reports a settled read with unchanged inputs as not loading', async () => {
    const seen: Seen[] = [];
    const view = render(<Probe input="same" seen={seen} />);
    await expect.poll(() => seen.at(-1)?.value).toBe('answer for same');
    seen.length = 0;
    view.rerender(<Probe input="same" seen={seen} />);
    expect(seen).toEqual([{ input: 'same', value: 'answer for same', loading: false }]);
  });
});
