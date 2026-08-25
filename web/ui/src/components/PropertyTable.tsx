// The frontmatter property table. Frontmatter is not markdown and does not
// reach the sanitized rendering path: every value here is rendered as text,
// which React escapes, so a value carrying markup reads as the characters the
// author wrote.
//
// The heading belongs to the caller. The viewer stands one over the full
// table and the rail drops its section header along with the table where the
// response yields no pairs, so this renders the table and its two absent
// states and nothing around them.

import { parseFrontmatter, splitDocument } from '../frontmatter';

export function PropertyTable({ raw, testID = 'frontmatter-table' }: { raw: string; testID?: string }) {
  // The value the response carries is a whole manifest document on the
  // load path and a bare block on the search path, so the block is taken
  // from it before either the parser or the raw view sees it.
  const block = splitDocument(raw).frontmatter;
  const parsed = parseFrontmatter(block);

  if (parsed.error !== '') {
    return (
      <>
        <div className="banner banner-danger" role="alert">
          <p className="banner-title">Invalid syntax</p>
          <p>{parsed.error}</p>
        </div>
        <pre className="mono raw-frontmatter">{block}</pre>
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
    <table className="data-table" data-testid={testID}>
      <tbody>
        {parsed.properties.map((property) => (
          <tr key={property.key}>
            <th scope="row" className="mono">
              {property.key}
            </th>
            <td>{property.value}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
