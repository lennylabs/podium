// The rendered artifact body. This is the one component that hands markup to
// the browser as markup, and the markup it hands over has been through the
// sanitizer in markdown.ts. Every other surface renders text, which React
// escapes.

import { useMemo } from 'react';

import { renderArtifactBody } from '../markdown';

/** noResources is the default bundle, held as one value so a caller that
 * passes none does not invalidate the memo below on every render. */
const noResources: readonly string[] = [];

export function ArtifactBody({ body, resources = noResources }: { body: string; resources?: readonly string[] }) {
  // The bundled file names decide whether a relative §4.4 prose reference
  // names one of them or another artifact, and only the second is routed.
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
  return <div className="prose" data-testid="artifact-body" dangerouslySetInnerHTML={{ __html: markup }} />;
}
