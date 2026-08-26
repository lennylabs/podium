// Frontmatter arrives as text, so the client parses it before the viewer can
// present it as a property table. A block that fails to parse is a state the
// reader is told about, and a response that yields no pairs at all is a
// finished document rather than a defect.
//
// What load_artifact returns under its frontmatter field is the whole
// ARTIFACT.md document, delimiter fences and prose body included, and the
// presigned manifest-body channel delivers the same document as a fetched
// file. Both are split here before either half is used, so the parser is
// handed a YAML mapping and the body is handed to the rendering path.

import { parseDocument } from 'yaml';

/** Property is one row of the frontmatter property table. The value is text:
 * it is rendered as text and never as markup. */
export interface Property {
  key: string;
  /** value is the text a scalar renders as. It is empty on a sequence, whose
   * entries are carried by items. */
  value: string;
  /** items are the entries of a sequence, each rendered on its own line.
   * Joining them into one line collides with an entry's own punctuation: a
   * sequence of sentences reads as `invoice., A purchase order`, where the
   * separator is indistinguishable from the text (§13.10). It is empty on a
   * scalar and on an empty sequence, which the table shows as an absent
   * value. */
  items: string[];
}

/** ParsedFrontmatter is either the pairs to render or the parse failure to
 * report, never both. */
export interface ParsedFrontmatter {
  properties: Property[];
  /** error is the parser's complaint, with its position where the parser
   * reports one. Empty when the block parsed. */
  error: string;
  /** line is the 1-based line of the block the parser complained about,
   * which the raw view marks. It is zero where the parser reported no
   * position and on a block that parsed. */
  line: number;
}

/** SplitDocument is a manifest document separated into the frontmatter block
 * the property table renders and the body the rendering path renders. */
export interface SplitDocument {
  frontmatter: string;
  body: string;
}

/** splitDocument separates a manifest document at its delimiter fences. A
 * document opening with a fence yields the block between the fences and the
 * prose after the closing one. Text carrying no opening fence is a bare
 * frontmatter block, which is what a search result returns, so it yields
 * itself and an empty body. */
export function splitDocument(text: string): SplitDocument {
  const opening = /^﻿?---[ \t]*\r?\n/.exec(text);
  if (opening === null) {
    return { frontmatter: text, body: '' };
  }
  const rest = text.slice(opening[0].length);
  const closing = /(?:^|\r?\n)---[ \t]*(?:\r?\n|$)/.exec(rest);
  if (closing === null) {
    // The document opens a block it never closes. The whole remainder is
    // the block, so the parser reports the syntax the author wrote rather
    // than the viewer silently rendering it as prose.
    return { frontmatter: rest, body: '' };
  }
  return {
    frontmatter: rest.slice(0, closing.index),
    body: rest.slice(closing.index + closing[0].length),
  };
}

/** parseFrontmatter parses a manifest document's frontmatter into property
 * rows. An empty block yields no rows and no error, which is the state the
 * viewer renders by omitting the table. */
export function parseFrontmatter(text: string): ParsedFrontmatter {
  const raw = splitDocument(text).frontmatter;
  if (raw.trim() === '') {
    return { properties: [], error: '', line: 0 };
  }
  const doc = parseDocument(raw);
  if (doc.errors.length > 0) {
    const failure = doc.errors[0];
    return { properties: [], error: describe(failure), line: failure.linePos?.[0].line ?? 0 };
  }
  const value: unknown = doc.toJS();
  if (value === null || typeof value !== 'object' || Array.isArray(value)) {
    return { properties: [], error: 'The frontmatter block is not a mapping.', line: 0 };
  }
  const properties = Object.entries(value as Record<string, unknown>).map(([key, entry]) => row(key, entry));
  return { properties, error: '', line: 0 };
}

function describe(err: { message: string; linePos?: [{ line: number; col: number }, ...unknown[]] }): string {
  const at = err.linePos?.[0];
  if (at === undefined) {
    return err.message;
  }
  return `${err.message} (line ${at.line}, column ${at.col})`;
}

/** row builds one table row from a frontmatter pair. A sequence keeps its
 * entries apart so the table renders them as separate lines, and every other
 * value is one piece of text. */
function row(key: string, entry: unknown): Property {
  if (Array.isArray(entry)) {
    return { key, value: '', items: entry.map(stringify) };
  }
  return { key, value: stringify(entry), items: [] };
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
  // A sequence reaches this only nested inside another value, because a
  // top-level one is kept as separate entries by row above. It is shown as
  // JSON alongside the other nested values rather than flattened into a
  // comma-joined line that reads as text the author wrote.
  return JSON.stringify(value);
}
