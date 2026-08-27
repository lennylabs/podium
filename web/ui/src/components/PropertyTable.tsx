// The frontmatter property table. Frontmatter is not markdown and does not
// reach the sanitized rendering path: every value here is rendered as text,
// which React escapes, so a value carrying markup reads as the characters the
// author wrote.
//
// The heading belongs to the caller. The viewer stands one over the full
// table and the rail drops its section header along with the table where the
// response yields no pairs, so this renders the table, the two absent states,
// the em dash a valueless key takes, and the full-width panel's own lines.

import { useState } from 'react';

import { parseFrontmatter, splitDocument, type Property } from '../frontmatter';
import { ClampedText } from './ClampedText';
import { CodeBlock, codeLines } from './CodeBlock';
import { CopyButton } from './primitives';

export function PropertyTable({
  raw,
  testID = 'frontmatter-table',
  offerRaw = false,
  clampValues = false,
}: {
  raw: string;
  testID?: string;
  /** clampValues puts a value under the shared three-line clip with a control
   * of its own, which the rail asks for and the full-width panel does not.
   * Neither a description nor a sequence carries a length bound, and in the
   * rail's narrow column an unclipped one runs for screens and pushes the
   * relation links §13.10 requires the viewer to carry far below the fold. */
  clampValues?: boolean;
  /** offerRaw stands the Table and Raw YAML views side by side, which the
   * full-width panel offers and the rail does not. It also carries the two
   * lines that state where the pairs came from and how their values are
   * shown, which belong to the panel and not to the rail's narrow column. */
  offerRaw?: boolean;
}) {
  // The value the response carries is a whole manifest document on the
  // load path and a bare block on the search path, so the block is taken
  // from it before either the parser or the raw view sees it.
  const block = splitDocument(raw).frontmatter;
  const parsed = parseFrontmatter(block);
  const [rawView, setRawView] = useState(false);

  if (parsed.error !== '') {
    // The block is raw YAML parsed in the client, so a parse failure is a
    // state of this panel: the complaint carries the parser's own position
    // and the raw block stands below it with that line marked.
    return (
      <>
        <div className="banner banner-danger" role="alert">
          <p className="banner-title">Invalid syntax</p>
          <p>{parsed.error}</p>
        </div>
        <RawBlock block={block} offending={parsed.line} />
      </>
    );
  }
  if (parsed.properties.length === 0) {
    // A response can yield no pairs at all, and that is a finished document.
    // The table is omitted, so nothing stands over an empty table and no
    // placeholder row is rendered.
    return <p className="quiet">No frontmatter on this artifact.</p>;
  }
  return (
    <>
      {offerRaw && (
        // The line and the toggle share a row, with the toggle pushed to the
        // right edge of the panel, so the panel opens with one line of prose
        // rather than with a control standing alone.
        <div className="source-actions">
          <span className="source-lede">
            Parsed from the artifact&apos;s manifest. Unknown keys are preserved and shown as authored.
          </span>
          <div className="segmented" role="group" aria-label="Frontmatter view">
            <button
            type="button"
            aria-pressed={!rawView}
            className={rawView ? 'segment' : 'segment segment-on'}
            onClick={() => {
              setRawView(false);
            }}
          >
            Table
          </button>
          <button
            type="button"
            aria-pressed={rawView}
            className={rawView ? 'segment segment-on' : 'segment'}
            onClick={() => {
              setRawView(true);
            }}
          >
            Raw YAML
            </button>
          </div>
        </div>
      )}
      {offerRaw && rawView ? (
        <RawBlock block={block} offending={0} />
      ) : (
        <table className="data-table property-table" data-testid={testID}>
          <tbody>
            {parsed.properties.map((property) => (
              <tr key={property.key}>
                <th scope="row" className="mono">
                  {property.key}
                </th>
                <td>
                  <PropertyValue property={property} clamp={clampValues} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {offerRaw && !rawView && (
        <p className="quiet property-note">
          Values are shown verbatim. A long description wraps rather than being clipped.
        </p>
      )}
    </>
  );
}

/** PropertyValue is the content of one value cell. Every value wraps whole in
 * the full-width panel, which is wide and carries nothing under the table to
 * bury, and is clipped in the rail, where an unbounded row makes the table
 * taller than the rest of the page and pushes the relation links off the fold
 * (§13.10). A sequence is clipped the same way a scalar is: a dozen tags one
 * per line runs the rail's table past 700px on its own, which is the state the
 * clip exists to prevent. */
function PropertyValue({ property, clamp }: { property: Property; clamp: boolean }) {
  if (property.items.length > 0) {
    if (clamp) {
      // The rail runs the entries together on one line and clips the result,
      // which is what the design draws: `tags | tracing, review, otel`. An
      // entry that ends in a full stop then runs into the separator, and the
      // full-width panel below is where such a sequence is read entry by
      // entry. A blank entry takes the em dash the absent state uses, so a
      // key the author left an empty entry in does not read as a doubled
      // separator (§13.10).
      const joined = property.items.map((item) => (item.trim() === '' ? '—' : item)).join(', ');
      return (
        <ClampedText
          text={joined}
          className="property-value"
          testID={`property-value-${property.key}`}
          moreLabel={`Show the whole ${property.key} value`}
        />
      );
    }
    // The full-width panel keeps the entries apart. Joined into one line, an
    // entry that ends in a full stop runs into the separator and reads as
    // `invoice., A purchase order`, where the reader cannot tell the
    // separator from the author's own punctuation. Each entry is a list item,
    // so a wrapped entry stays one entry (§13.10).
    return (
      <ul className="property-items" data-testid={`property-value-${property.key}`}>
        {property.items.map((item, index) => (
          <li key={`${String(index)}:${item}`} className="property-value">
            {item.trim() === '' ? <AbsentValue keyName={`${property.key}-${String(index)}`} /> : item}
          </li>
        ))}
      </ul>
    );
  }
  if (property.value.trim() === '') {
    return <AbsentValue keyName={property.key} />;
  }
  if (clamp) {
    // The control names its own row, because a reader running down the rail's
    // property rows meets it out of the surrounding text and every row would
    // otherwise offer the same unqualified "Show more".
    return (
      <ClampedText
        text={property.value}
        className="property-value"
        testID={`property-value-${property.key}`}
        moreLabel={`Show the whole ${property.key} value`}
      />
    );
  }
  return (
    <span className="property-value" data-testid={`property-value-${property.key}`}>
      {property.value}
    </span>
  );
}

/** AbsentValue is the treatment for a key the author wrote with no value.
 * The pair is present in the block, so the row stays and its value cell
 * carries an em dash: a blank cell reads as the table having failed to render
 * the value rather than as the author having left it empty (§13.10). The dash
 * is decoration to a screen reader, so the cell names the state instead. */
function AbsentValue({ keyName }: { keyName: string }) {
  return (
    <span
      className="property-absent"
      data-testid={`property-absent-${keyName}`}
      role="img"
      aria-label={`${keyName} has no value`}
    >
      —
    </span>
  );
}

/** RawBlock is the block as the author wrote it. It takes the same file view
 * as the viewer's authored source pane, because both panes stand on the same
 * surface and a reader who learns one reads the other: a header naming the
 * block and counting its lines, a numbered gutter, and an explicit Copy
 * control under it (§13.10). The line the parser complained about is marked,
 * so the reader is shown where the failure is rather than only being told its
 * coordinates. A block with no reported position marks nothing.
 *
 * The text column scrolls sideways, so it is a named region in the tab order
 * the same way the rendered body's tables and code fences are. Without it a
 * keyboard-only reader cannot reach the scroll container and cannot read a
 * value that runs past the pane's right edge. */
function RawBlock({ block, offending }: { block: string; offending: number }) {
  const lines = codeLines(block);
  return (
    <section className="source-pane">
      <CodeBlock
        name="raw frontmatter"
        lines={lines}
        offending={offending}
        label="Frontmatter, as authored"
        testID="raw-frontmatter"
      />
      <div className="source-actions source-actions-under">
        {/* The block itself rather than the rendered lines, so the copy is
            the text the parser and the table were built from. */}
        <CopyButton value={block} label="Copy raw block" subject="Raw frontmatter" />
      </div>
    </section>
  );
}
