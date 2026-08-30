// The frontmatter property table's row banding. The full-width panel stands
// the key and the value at opposite ends of the content column, so the rows
// alternate their fill and the reader keeps the line across that gap. The rail
// holds the same pairs in a 270px column on the inset tone and keeps one fill.
//
// jsdom performs no layout and does not resolve a custom property in a
// computed style, so a case reads the declaration the sheet's matching rules
// give the row rather than a resolved colour.

import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';

import { PropertyTable } from './components/PropertyTable';
import './index.css';

afterEach(cleanup);

const raw = ['---', 'type: skill', 'version: 0.1.0', 'owner: platform-eng', '---'].join('\n');

/** declared returns the value the sheet's last matching rule gives property
 * for element, or the empty string when no rule sets it. */
function declared(element: Element, property: string): string {
  let value = '';
  for (const sheet of Array.from(document.styleSheets)) {
    for (const rule of Array.from(sheet.cssRules)) {
      if (!(rule instanceof CSSStyleRule)) {
        continue;
      }
      if (!element.matches(rule.selectorText)) {
        continue;
      }
      const found = rule.style.getPropertyValue(property);
      if (found !== '') {
        value = found;
      }
    }
  }
  return value;
}

/** ruleValue returns the value the rule with exactly this selector gives
 * property, or the empty string when the sheet carries no such rule. A
 * pseudo-element selector cannot be matched against an element, so a case that
 * reads the drawn separator asks the sheet for the rule by name. */
function ruleValue(selector: string, property: string): string {
  for (const sheet of Array.from(document.styleSheets)) {
    for (const rule of Array.from(sheet.cssRules)) {
      if (rule instanceof CSSStyleRule && rule.selectorText === selector) {
        return rule.style.getPropertyValue(property);
      }
    }
  }
  return '';
}

/** fills returns the background each row of the named table is given. */
function fills(testID: string): string[] {
  const rows = Array.from(screen.getByTestId(testID).querySelectorAll('tbody tr'));
  return rows.map((row) => declared(row, 'background-color'));
}

describe('frontmatter property table', () => {
  it('alternates the row fill across the full-width panel', () => {
    render(<PropertyTable raw={raw} offerRaw />);
    expect(screen.getByTestId('frontmatter-table').className.split(' ')).toContain(
      'property-table-panel',
    );
    expect(fills('frontmatter-table')).toEqual(['', 'var(--surf2)', '']);
  });

  it('holds the rail to one fill', () => {
    render(<PropertyTable raw={raw} testID="rail-frontmatter-table" clampValues />);
    expect(screen.getByTestId('rail-frontmatter-table').className.split(' ')).not.toContain(
      'property-table-panel',
    );
    expect(fills('rail-frontmatter-table')).toEqual(['', '', '']);
  });

  // A sequence takes the same one-line row every other key takes, which is what
  // the design draws for `tags`. Stacked one entry per line, a twelve-tag
  // sequence turns one row into a block of bullets and pushes the keys under it
  // down the page. The entries stay list items and the sheet flows them inline
  // with a drawn separator, so the row reads as one line without the markup
  // losing where an entry ends.
  // Spec: §13.10
  it('flows a sequence value onto one line in the full-width panel', () => {
    const sequence = ['---', 'tags:', '  - tracing', '  - review', '  - otel', '---'].join('\n');
    render(<PropertyTable raw={sequence} offerRaw />);
    const value = screen.getByTestId('property-value-tags');
    expect(Array.from(value.querySelectorAll('li')).map((item) => item.textContent)).toEqual([
      'tracing',
      'review',
      'otel',
    ]);
    expect(declared(value.querySelectorAll('li')[0], 'display')).toBe('inline');
    // No marker and no per-entry stacking: the row is one line high.
    expect(declared(value, 'list-style')).toBe('none');
    expect(ruleValue('.property-items li + li', 'margin-top')).toBe('');
    // The separator is drawn between the entries and de-emphasized, so an entry
    // ending in a full stop is still told apart from the comma after it.
    // The sheet keeps the quote character it was authored with, so the case
    // reads the string the separator is rather than the way it was quoted.
    expect(ruleValue('.property-items li + li::before', 'content').replace(/'/g, '"')).toBe('", "');
    expect(ruleValue('.property-items li + li::before', 'color')).toBe('var(--faint)');
  });

  // The note under the panel quotes a frontmatter key inside a sentence. Left
  // as bare mono it reads as a slip in the typeface rather than as a quoted
  // identifier, and the same element in the rendered body on the tab beside it
  // carries the chip fill, so one viewer would draw one token two ways.
  // Spec: §13.10
  it('draws the served note key as a code chip', () => {
    render(<PropertyTable raw={raw} offerRaw />);
    const token = screen.getByTestId('frontmatter-served-note').querySelector('code');
    expect(token?.textContent).toBe('extends');
    expect(declared(token as Element, 'background')).toBe('var(--chip)');
    expect(declared(token as Element, 'border-radius')).toBe('4px');
  });
});
