// The rendered artifact body. It is the §4.4 manifest body drawn through the
// shared prose path, plus the one thing that path does not carry: the bundled
// files a §4.4 prose reference can name.

import type { MouseEvent } from 'react';

import { Prose } from './Prose';
import { resourceReferenceAttribute } from '../markdown';

export function ArtifactBody({
  body,
  resources,
  onResource,
}: {
  body: string;
  resources?: readonly string[];
  /** onResource is called with the bundled file a §4.4 prose reference names,
   * when the reader follows that reference. The rendering path turns such a
   * reference into a control rather than a link, because the file has no
   * address of its own on this origin, and the surface holding the body
   * decides what opening it means. */
  onResource?: (name: string) => void;
}) {
  // The reference controls are inside markup the prose component inserts
  // whole, so the press is taken on the container rather than bound to each
  // one.
  const onClick = (event: MouseEvent<HTMLDivElement>) => {
    if (onResource === undefined) {
      return;
    }
    const target = event.target instanceof Element ? event.target : null;
    const name = target?.closest(`[${resourceReferenceAttribute}]`)?.getAttribute(resourceReferenceAttribute);
    if (name !== null && name !== undefined) {
      onResource(name);
    }
  };
  return (
    <Prose
      body={body}
      resources={resources}
      testID="artifact-body"
      onClick={onClick}
      empty={
        // A manifest carrying frontmatter and nothing else is a finished
        // document, and so is one whose only content the sanitizer removed.
        // The viewer's loading and failure states are settled before this
        // panel is drawn, so an empty panel would read as a load that failed
        // silently. The line stands in for the document the way the property
        // table's line stands in for an artifact that declares no
        // frontmatter.
        <p className="quiet" data-testid="artifact-body-empty">
          This artifact has no body.
        </p>
      }
    />
  );
}
