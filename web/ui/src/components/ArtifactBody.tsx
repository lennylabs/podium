// The rendered artifact body. This is the one component that hands markup to
// the browser as markup, and the markup it hands over has been through the
// sanitizer in markdown.ts. Every other surface renders text, which React
// escapes.

import { useMemo } from 'react';

import type { MouseEvent } from 'react';

import { renderArtifactBody, resourceReferenceAttribute } from '../markdown';

/** noResources is the default bundle, held as one value so a caller that
 * passes none does not invalidate the memo below on every render. */
const noResources: readonly string[] = [];

export function ArtifactBody({
  body,
  resources = noResources,
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
  // The bundled file names decide whether a relative §4.4 prose reference
  // names one of them or another artifact, and the two are resolved
  // differently.
  const markup = useMemo(() => renderArtifactBody(body, resources), [body, resources]);
  if (markup.trim() === '') {
    // A manifest carrying frontmatter and nothing else is a finished
    // document, and so is one whose only content the sanitizer removed. The
    // viewer's loading and failure states are settled before this panel is
    // drawn, so an empty panel would read as a load that failed silently.
    // The line stands in for the document the way the property table's line
    // stands in for an artifact that declares no frontmatter.
    return (
      <p className="quiet" data-testid="artifact-body-empty">
        This artifact has no body.
      </p>
    );
  }
  // The reference controls are inside markup this component inserts whole, so
  // the press is taken on the container rather than bound to each one.
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
    <div
      className="prose"
      data-testid="artifact-body"
      onClick={onClick}
      dangerouslySetInnerHTML={{ __html: markup }}
    />
  );
}
