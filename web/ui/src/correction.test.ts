import { describe, expect, it } from 'vitest';

import { correctQueryLine, vocabularyOf } from './surfaces/correction';

// The correction the palette offers when a query matched nothing. It is
// derived from the catalog of canonical artifact IDs, because no registry
// endpoint answers "did you mean".
describe('correctQueryLine', () => {
  const ids = ['platform/span-coverage', 'finance/ap/pay-invoice', 'eng/deploy'];

  // A path segment enters the vocabulary as itself and as the words its
  // hyphens separate, so a whole-token typo and a one-word typo both find a
  // spelling to land on.
  it('spells the vocabulary from the segments and the words inside them', () => {
    const terms = vocabularyOf(['platform/span-coverage']);
    expect(terms.sort()).toEqual(['coverage', 'platform', 'span', 'span-coverage']);
  });

  it('corrects a misspelled word to the nearest spelling the catalog holds', () => {
    expect(correctQueryLine('span covrage', ids)).toBe('span coverage');
    expect(correctQueryLine('pay-invoce', ids)).toBe('pay-invoice');
  });

  // A word the catalog already spells is not a misspelling, however little it
  // matched, and a word nothing is near is left alone rather than rewritten
  // into an unrelated one.
  it('offers nothing for a spelling the catalog holds or for a word nothing is near', () => {
    expect(correctQueryLine('deploy', ids)).toBeNull();
    expect(correctQueryLine('zzqqxx', ids)).toBeNull();
    expect(correctQueryLine('span coverage', ids)).toBeNull();
  });

  // The inline filters are what the reader asked for rather than how they
  // spelled it, so the correction carries them through and rewrites the free
  // text alone.
  it('carries the inline filters through and corrects the free text', () => {
    expect(correctQueryLine('type:skill covrage', ids)).toBe('type:skill coverage');
    expect(correctQueryLine('type:skil', ids)).toBeNull();
  });

  // An empty catalog and a line with no free text both leave the arm as it
  // was.
  it('offers nothing without a vocabulary or without free text', () => {
    expect(correctQueryLine('covrage', [])).toBeNull();
    expect(correctQueryLine('tag:review', ids)).toBeNull();
  });
});
