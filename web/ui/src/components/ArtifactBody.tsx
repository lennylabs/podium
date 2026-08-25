// The rendered artifact body. This is the one component that hands markup to
// the browser as markup, and the markup it hands over has been through the
// sanitizer in markdown.ts. Every other surface renders text, which React
// escapes.

import { useMemo } from 'react';

import { renderArtifactBody } from '../markdown';

export function ArtifactBody({ body }: { body: string }) {
  const markup = useMemo(() => renderArtifactBody(body), [body]);
  return <div className="prose" data-testid="artifact-body" dangerouslySetInnerHTML={{ __html: markup }} />;
}
