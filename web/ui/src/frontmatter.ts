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

import { isMap, isScalar, isSeq, parseDocument, Scalar, type Node } from 'yaml';

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
  const contents = doc.contents;
  if (!isMap(contents)) {
    return { properties: [], error: 'The frontmatter block is not a mapping.', line: 0 };
  }
  const properties = contents.items.map((pair) => row(raw, pair.key, pair.value as unknown));
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
function row(source: string, key: unknown, value: unknown): Property {
  const name = isScalar(key) ? String(key.value ?? '') : '';
  if (isSeq(value)) {
    return { key: name, value: '', items: value.items.map((item) => valueText(source, item)) };
  }
  return { key: name, value: valueText(source, value), items: [] };
}

/** valueText renders one frontmatter value as the text the table shows. The panel
 * states that its values are shown verbatim, so a scalar is the token the
 * author wrote, line breaks in a block scalar included, and a nested mapping or
 * sequence is the source the author wrote rather than a re-serialization in
 * another notation (§13.10). Trailing blank lines are dropped, because a
 * block scalar chomps to a newline the table would otherwise render as an
 * empty line of its own. */
function valueText(source: string, value: unknown): string {
  if (isScalar(value)) {
    return scalarText(value);
  }
  const range = (value as Node | null | undefined)?.range;
  if (range === null || range === undefined) {
    return '';
  }
  return dedent(source, range[0], source.slice(range[0], range[1]).replace(/\s+$/, ''));
}

/** scalarText is one scalar's authored text. A plain scalar carries the token
 * the author typed in its source, while the parser's resolved value is what the
 * YAML core schema derives from that token: `007` resolves to the number 7 and
 * `1.10` to 1.1, so a table built from resolved values shows a value the author
 * never wrote and contradicts the Raw YAML view of the same block (§13.10). A
 * quoted or block scalar keeps its delimiters and its indentation in the
 * source, and its resolved value is the text inside them. */
function scalarText(value: Scalar): string {
  if (value.type === Scalar.PLAIN && typeof value.source === 'string') {
    return value.source.replace(/\s+$/, '');
  }
  const resolved = value.value;
  if (resolved === null || resolved === undefined) {
    return '';
  }
  return String(resolved).replace(/\s+$/, '');
}

/** dedent strips the indent a nested block sits at from every line after its
 * first. The slice starts at the value's first character, so the opening line
 * carries no indent while the lines under it carry the block's full one, and
 * the value would otherwise read as a staircase rather than as the block the
 * author wrote. */
function dedent(source: string, start: number, block: string): string {
  if (!block.includes('\n')) {
    return block;
  }
  const column = start - (source.lastIndexOf('\n', start - 1) + 1);
  if (column === 0) {
    return block;
  }
  const strip = new RegExp(`^[ ]{0,${String(column)}}`);
  const [first, ...rest] = block.split('\n');
  return [first, ...rest.map((line) => line.replace(strip, ''))].join('\n');
}
