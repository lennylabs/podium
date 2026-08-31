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
import { CodeBlock, codeLines } from './CodeBlock';
import { CopyButton } from './primitives';

export function PropertyTable({
  raw,
  testID = 'frontmatter-table',
  offerRaw = false,
}: {
  raw: string;
  testID?: string;
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
            Parsed from the frontmatter the registry serves for this artifact. Unknown keys are
            preserved.
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
        <>
          <RawBlock block={block} offending={0} />
          <ServedNote />
        </>
      ) : (
        // The panel's table is banded and the rail's is not, so the class the
        // banding hangs on is put on here beside the toggle that marks the
        // panel rather than being inferred from an ancestor in the sheet.
        <table
          className={
            offerRaw ? 'data-table property-table property-table-panel' : 'data-table property-table'
          }
          data-testid={testID}
        >
          <tbody>
            {parsed.properties.map((property) => (
              <tr key={property.key}>
                <th scope="row" className="mono">
                  {property.key}
                </th>
                <td>
                  <PropertyValue property={property} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {offerRaw && !rawView && (
        <>
          <p className="quiet property-note">
            Values are shown verbatim. A long description wraps rather than being clipped.
          </p>
          <ServedNote />
        </>
      )}
    </>
  );
}

/** ServedNote accounts for the difference between this block and the file the
 * author wrote. The registry re-serializes an extends manifest with the
 * parent stripped (§4.6), so a reader who compares the panel with the
 * EXTENDS chip the rail draws beside it finds a reference on one half of the
 * viewer and no row for it on the other. The panel cannot close the gap by
 * republishing the pre-merge document, because that is the disclosure §4.6
 * withholds, so it names the removal instead, in the register the rendered
 * body already uses for a stripped link (§13.10). */
function ServedNote() {
  return (
    <p className="quiet property-note" data-testid="frontmatter-served-note">
      The block is the one the registry serves. A key the registry withholds is absent from it (key
      withheld), so an artifact can carry no <code className="mono">extends</code> row here and
      still name a parent under Relations.
    </p>
  );
}

/** PropertyValue is the content of one value cell. A value wraps whole on both
 * surfaces, which is what the design draws for the rail and what the panel's
 * own line under the table states: the value is the author's text and the cell
 * shows all of it. Clipping the rail's cells cut a description and a tag list
 * mid-word behind a control of their own, which contradicted that line and
 * appears in neither design reference (§13.10). */
function PropertyValue({ property }: { property: Property }) {
  if (property.items.length > 0) {
    // Both surfaces run the entries onto one line, which is the row the design
    // draws and the height the rows around it take. Each entry is still a list
    // item, so a wrapped entry stays one entry and a screen reader counts them;
    // the sheet flows the items inline and draws the separator between them in
    // the de-emphasized tone, which is what tells an entry ending in a full
    // stop apart from the comma that follows it (§13.10).
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

/** RawBlock is the block as the registry serves it. It takes the same file view
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
        name="served frontmatter"
        lines={lines}
        offending={offending}
        label="Frontmatter, as served"
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
