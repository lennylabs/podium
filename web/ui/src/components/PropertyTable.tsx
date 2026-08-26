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

import { parseFrontmatter, splitDocument } from '../frontmatter';
import { ClampedText } from './ClampedText';

export function PropertyTable({
  raw,
  testID = 'frontmatter-table',
  offerRaw = false,
  clampValues = false,
}: {
  raw: string;
  testID?: string;
  /** offerRaw stands the Table and Raw YAML views side by side, which the
   * full-width panel offers and the rail does not. It also carries the two
   * lines that state where the pairs came from and how their values are
   * shown, which belong to the panel and not to the rail's narrow column. */
  offerRaw?: boolean;
  /** clampValues clips each value to three lines with a control that opens
   * it, which the rail takes and the full-width panel does not. The rail is
   * a single scrolling column with the relation links under this table, so a
   * description running to a couple of thousand characters puts those links
   * thousands of pixels below the fold (§13.10). The panel has nothing under
   * it to bury and states that its values are shown verbatim, so it keeps
   * them whole. */
  clampValues?: boolean;
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
                  {property.value.trim() === '' ? (
                    <AbsentValue keyName={property.key} />
                  ) : clampValues ? (
                    <ClampedText
                      text={property.value}
                      className="property-value"
                      testID={`property-value-${property.key}`}
                      moreLabel={`Show the whole ${property.key} value`}
                    />
                  ) : (
                    property.value
                  )}
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

/** RawBlock is the block as the author wrote it. The line the parser
 * complained about is marked, so the reader is shown where the failure is
 * rather than only being told its coordinates. A block with no reported
 * position marks nothing. */
function RawBlock({ block, offending }: { block: string; offending: number }) {
  return (
    <pre className="mono raw-frontmatter" data-testid="raw-frontmatter">
      {block.split('\n').map((line, index) => (
        <span
          key={`${String(index)}:${line}`}
          className={index + 1 === offending ? 'raw-line raw-line-offending' : 'raw-line'}
          data-testid={index + 1 === offending ? 'offending-line' : undefined}
        >
          {line}
          {'\n'}
        </span>
      ))}
    </pre>
  );
}
