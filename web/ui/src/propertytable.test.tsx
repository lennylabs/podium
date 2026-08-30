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
});
