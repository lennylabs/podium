// The frontmatter property table. Frontmatter is not markdown and does not
// reach the sanitized rendering path: every value here is rendered as text,
// which React escapes, so a value carrying markup reads as the characters the
// author wrote.

import { parseFrontmatter, splitDocument } from '../frontmatter';

export function PropertyTable({ raw }: { raw: string }) {
  // The value the response carries is a whole manifest document on the
  // load path and a bare block on the search path, so the block is taken
  // from it before either the parser or the raw view sees it.
  const block = splitDocument(raw).frontmatter;
  const parsed = parseFrontmatter(block);

  if (parsed.error !== '') {
    return (
      <section aria-label="Frontmatter">
        <h2>Frontmatter</h2>
        <div className="banner banner-danger" role="alert">
          <p className="banner-title">Invalid syntax</p>
          <p>{parsed.error}</p>
        </div>
        <pre className="mono raw-frontmatter">{block}</pre>
      </section>
    );
  }
  if (parsed.properties.length === 0) {
    // A response can yield no pairs at all, and that is a finished document.
    // The table and its header are both omitted, so nothing stands over an
    // empty table and no placeholder row is rendered.
    return <p className="quiet">No frontmatter on this artifact.</p>;
  }
  return (
    <section aria-label="Frontmatter">
      <h2>Frontmatter</h2>
      <table className="data-table" data-testid="frontmatter-table">
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
    </section>
  );
}
