import type { ReactElement } from "react";

import type { Heading } from "../../build/types";

/** Depth range the rail lists. The page title renders the h1, so depth 1 is skipped. */
const MIN_DEPTH = 2;
const MAX_DEPTH = 3;

/**
 * The 214px right rail: the headings of the current page, then the repository
 * links.
 *
 * Each link carries data-toc-link with its heading id. The client script marks
 * the heading currently in view by adding the is-active class, which the
 * stylesheet renders as a heavier weight plus an accent inset. Without
 * JavaScript the list is a plain set of in-page anchors.
 *
 * The rail carries data-doc-toc, so the client router replaces it together with
 * the article it describes.
 */
export function TableOfContents(props: {
  headings: Heading[];
  editUrl: string;
  issueUrl: string;
}): ReactElement {
  const { headings, editUrl, issueUrl } = props;
  const items = headings.filter(
    (heading) => heading.depth >= MIN_DEPTH && heading.depth <= MAX_DEPTH,
  );

  return (
    <aside className="d-toc" data-doc-toc="">
      {items.length > 0 && (
        <nav className="d-toc-nav" aria-labelledby="d-toc-title">
          <p className="d-toc-title" id="d-toc-title">
            On this page
          </p>
          <ul className="d-toc-list">
            {items.map((heading) => (
              <li key={heading.id} className={`d-toc-item d-toc-item--${heading.depth}`}>
                <a className="d-toc-link" href={`#${heading.id}`} data-toc-link={heading.id}>
                  {heading.text}
                </a>
              </li>
            ))}
          </ul>
        </nav>
      )}
      <div className="d-toc-actions">
        <a className="d-toc-action" href={editUrl}>
          Edit this page on GitHub
        </a>
        <a className="d-toc-action" href={issueUrl}>
          Report an issue
        </a>
      </div>
    </aside>
  );
}
