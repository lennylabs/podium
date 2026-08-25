// Frontmatter arrives as a raw YAML block carried as text, so the client
// parses it before the viewer can present it as a property table. A block
// that fails to parse is a state the reader is told about, and a response
// that yields no pairs at all is a finished document rather than a defect.

import { parseDocument } from 'yaml';

/** Property is one row of the frontmatter property table. The value is text:
 * it is rendered as text and never as markup. */
export interface Property {
  key: string;
  value: string;
}

/** ParsedFrontmatter is either the pairs to render or the parse failure to
 * report, never both. */
export interface ParsedFrontmatter {
  properties: Property[];
  /** error is the parser's complaint, with its position where the parser
   * reports one. Empty when the block parsed. */
  error: string;
}

/** parseFrontmatter parses a raw YAML frontmatter block into property rows.
 * An empty block yields no rows and no error, which is the state the viewer
 * renders by omitting the table. */
export function parseFrontmatter(raw: string): ParsedFrontmatter {
  if (raw.trim() === '') {
    return { properties: [], error: '' };
  }
  const doc = parseDocument(raw);
  if (doc.errors.length > 0) {
    return { properties: [], error: describe(doc.errors[0]) };
  }
  const value: unknown = doc.toJS();
  if (value === null || typeof value !== 'object' || Array.isArray(value)) {
    return { properties: [], error: 'The frontmatter block is not a mapping.' };
  }
  const properties = Object.entries(value as Record<string, unknown>).map(([key, entry]) => ({
    key,
    value: stringify(entry),
  }));
  return { properties, error: '' };
}

function describe(err: { message: string; linePos?: [{ line: number; col: number }, ...unknown[]] }): string {
  const at = err.linePos?.[0];
  if (at === undefined) {
    return err.message;
  }
  return `${err.message} (line ${at.line}, column ${at.col})`;
}

/** stringify renders one frontmatter value as the text the table shows. A
 * nested value is shown as JSON so a reader sees its structure without the
 * table growing a second layout. */
function stringify(value: unknown): string {
  if (value === null || value === undefined) {
    return '';
  }
  if (typeof value === 'string') {
    return value;
  }
  if (typeof value === 'number' || typeof value === 'boolean') {
    return String(value);
  }
  if (Array.isArray(value)) {
    return value.map(stringify).join(', ');
  }
  return JSON.stringify(value);
}
