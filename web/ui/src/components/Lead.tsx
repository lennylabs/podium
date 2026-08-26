// The description an artifact's header states under its title. A description
// is author-controlled and carries no length bound, and the header sits above
// the version picker, the tabs, and the body, so a description running to
// several hundred words pushes every one of them below the fold and the
// artifact reads as empty until the reader scrolls. The header therefore
// clips the field to the same three lines a listing row reads at (§13.10) and
// carries a control that opens the rest in place.

import { ClampedText } from './ClampedText';

/** Lead renders an artifact's description under its title, clipped to three
 * lines until the reader opens it. The header carries one such control, so
 * the button reads as its own label. */
export function Lead({ text }: { text: string }) {
  return <ClampedText text={text} className="lead" testID="artifact-lead" />;
}
