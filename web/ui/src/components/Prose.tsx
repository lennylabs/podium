// The rendered markdown document. This is the one component that hands markup
// to the browser as markup, and the markup it hands over has been through the
// sanitizer in markdown.ts. Every other surface renders text, which React
// escapes.
//
// Two bodies reach it. An artifact's manifest body (§4.4) is the document the
// viewer is built around, and a domain's prose body (§4.5.5) is the long-form
// context `load_domain` returns for the requested path. Both are markdown
// authored by whoever can write to a layer's source, so both take the same
// rendering and the same sanitizer.

import { useMemo } from 'react';

import type { MouseEvent, ReactNode } from 'react';

import { renderProse } from '../markdown';

/** noResources is the default bundle, held as one value so a caller that
 * passes none does not invalidate the memo below on every render. */
const noResources: readonly string[] = [];

export function Prose({
  body,
  resources = noResources,
  className = 'prose',
  testID,
  empty = null,
  onClick,
}: {
  body: string;
  resources?: readonly string[];
  className?: string;
  testID?: string;
  /** empty stands in for the document when the body renders to nothing, which
   * a body carrying only frontmatter and a body whose every construct the
   * sanitizer removed both do. */
  empty?: ReactNode;
  onClick?: (event: MouseEvent<HTMLDivElement>) => void;
}) {
  // The bundled file names decide whether a relative §4.4 prose reference
  // names one of them or another artifact, and the two are resolved
  // differently.
  const markup = useMemo(() => renderProse(body, resources), [body, resources]);
  if (markup.trim() === '') {
    return <>{empty}</>;
  }
  return (
    <div
      className={className}
      data-testid={testID}
      onClick={onClick}
      dangerouslySetInnerHTML={{ __html: markup }}
    />
  );
}
