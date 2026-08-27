// Author-controlled text a surface clips to a fixed number of lines until the
// reader opens it. A description carries no length bound, and a long one
// pushes everything under it off the fold: in the artifact header it buries
// the body, and in the rail's frontmatter table it buries the relation links
// §13.10 requires the viewer to carry. Both sites therefore clip the field
// and offer the rest in place.
//
// Spec: §13.10

import { useId, useLayoutEffect, useRef, useState } from 'react';

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
  // The control reports its state with aria-expanded, which is only half of
  // what a screen reader needs: without aria-controls the state is announced
  // over no named region. The paragraph therefore carries a generated id the
  // button points at. The id comes from useId so the rail, which renders one
  // clip per property row, does not repeat an id across the document.
  const region = useId();
  const [expanded, setExpanded] = useState(false);
  const [overrun, setOverrun] = useState(false);
  const paragraph = useRef<HTMLParagraphElement>(null);
  // A route change swaps the text into the same component instance rather
  // than remounting it, so the state measured against the old string has to
  // be dropped here. Without the reset, an opened description leaves its
  // control standing over the next artifact's short line, where it collapses
  // nothing: the measurement below returns early while expanded, so the
  // overrun is never recomputed for the new text. React re-renders on this
  // before it commits, so the stale control never reaches the screen.
  const [measured, setMeasured] = useState(text);
  if (measured !== text) {
    setMeasured(text);
    setExpanded(false);
    setOverrun(false);
  }

  useLayoutEffect(() => {
    const node = paragraph.current;
    if (node === null || expanded) {
      return;
    }
    setOverrun(node.scrollHeight > node.clientHeight);
  }, [text, expanded]);

  return (
    <>
      <p
        ref={paragraph}
        id={region}
        className={expanded ? className : `${className} clamped`}
        data-testid={testID}
      >
        {text}
      </p>
      {overrun && (
        <button
          type="button"
          className="clamp-more"
          aria-expanded={expanded}
          aria-controls={region}
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
