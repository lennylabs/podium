// A table an author wrote in the artifact body takes the product's one table
// treatment: an outer card, a header row on the inset tone, and horizontal
// dividers between rows with no vertical rule between columns. Before this,
// the rendered body was the one table in the UI drawn as a full cell grid.
//
// jsdom performs no layout and does not resolve a custom property in a
// computed style, so a case reads the declaration the sheet's matching rules
// give the element rather than a resolved colour.

import { render } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { ArtifactBody } from './components/ArtifactBody';
import './index.css';

const body = [
  '| Step | Owner |',
  '| --- | --- |',
  '| Freeze the release branch | platform |',
  '| Run the migration | data |',
  '',
].join('\n');

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

function renderTable(): { table: Element; head: Element; cells: Element[] } {
  const container = render(<ArtifactBody body={body} resources={[]} />).container;
  const table = container.querySelector('table');
  if (table === null) {
    throw new Error('the body rendered no table');
  }
  const head = table.querySelector('thead th');
  if (head === null) {
    throw new Error('the table rendered no header cell');
  }
  return { table, head, cells: Array.from(table.querySelectorAll('th, td')) };
}

describe('a table in the rendered artifact body', () => {
  it('is drawn as a card with a header band', () => {
    const { table, head } = renderTable();
    expect(declared(table, 'border')).toBe('1px solid var(--bd)');
    expect(declared(table, 'border-radius')).toBe('9px');
    expect(declared(head, 'background-color')).toBe('var(--surf2)');
  });

  it('separates rows with a horizontal divider and no vertical cell rule', () => {
    const { cells } = renderTable();
    expect(cells.length).toBeGreaterThan(0);
    for (const cell of cells) {
      expect(declared(cell, 'border-bottom')).toBe('1px solid var(--b2)');
      expect(declared(cell, 'border')).toBe('');
      expect(declared(cell, 'border-left')).toBe('');
      expect(declared(cell, 'border-right')).toBe('');
      expect(declared(cell, 'border-top')).toBe('');
    }
  });
});
