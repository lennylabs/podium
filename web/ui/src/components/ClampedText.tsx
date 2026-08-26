// Author-controlled text a surface clips to a fixed number of lines until the
// reader opens it. A description carries no length bound, and a long one
// pushes everything under it off the fold: in the artifact header it buries
// the body, and in the rail's frontmatter table it buries the relation links
// §13.10 requires the viewer to carry. Both sites therefore clip the field
// and offer the rest in place.
//
// Spec: §13.10

import { useLayoutEffect, useRef, useState } from 'react';

/** ClampedText renders text under the shared three-line clip and carries the
 * control that opens it. The control appears only where the text actually
 * overruns the clip, because whether three lines hold a given string is a
 * layout fact that depends on the rendered width and the typeface: it is
 * measured against the clipped element rather than guessed at from the
 * string's length. The measurement runs while the element is clipped, so
 * expanding it does not read as the overrun having gone away. */
export function ClampedText({
  text,
  className,
  testID,
  moreLabel,
}: {
  text: string;
  /** className is the site's own class on the paragraph. The clip itself
   * comes from the shared `clamped` class this adds while collapsed. */
  className: string;
  testID?: string;
  /** moreLabel names the control for a reader who meets it out of the
   * surrounding text, which is what a screen reader running down a rail of
   * property rows does. A site carrying one control leaves it unset and the
   * button reads as its own label. */
  moreLabel?: string;
}) {
  const [expanded, setExpanded] = useState(false);
  const [overrun, setOverrun] = useState(false);
  const paragraph = useRef<HTMLParagraphElement>(null);

  useLayoutEffect(() => {
    const node = paragraph.current;
    if (node === null || expanded) {
      return;
    }
    setOverrun(node.scrollHeight > node.clientHeight);
  }, [text, expanded]);

  return (
    <>
      <p ref={paragraph} className={expanded ? className : `${className} clamped`} data-testid={testID}>
        {text}
      </p>
      {overrun && (
        <button
          type="button"
          className="clamp-more"
          aria-expanded={expanded}
          aria-label={moreLabel}
          onClick={() => {
            setExpanded(!expanded);
          }}
        >
          {expanded ? 'Show less' : 'Show more'}
        </button>
      )}
    </>
  );
}
