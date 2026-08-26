// The description an artifact's header states under its title. A description
// is author-controlled and carries no length bound, and the header sits above
// the version picker, the tabs, and the body, so a description running to
// several hundred words pushes every one of them below the fold and the
// artifact reads as empty until the reader scrolls. The header therefore
// clips the field to the same three lines a listing row reads at (§13.10) and
// carries a control that opens the rest in place.
//
// Description is optional in an artifact's frontmatter, so the header states
// its absence in the same italic placeholder the listing row and the
// subdomain card state theirs in. Collapsing the line away instead would put
// the title straight onto the tab strip and read as a rendering gap rather
// than as an artifact that declares no description.

import { ClampedText } from './ClampedText';

/** Lead renders an artifact's description under its title, clipped to three
 * lines until the reader opens it. The header carries one such control, so
 * the button reads as its own label. An empty description renders the absent
 * placeholder, which carries no clip because it is one short line. */
export function Lead({ text }: { text: string }) {
  if (text === '') {
    return (
      <p className="lead quiet absent-description" data-testid="artifact-lead">
        No description.
      </p>
    );
  }
  return <ClampedText text={text} className="lead" testID="artifact-lead" />;
}
