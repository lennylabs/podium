// The description an artifact's header states under its title. A description
// is author-controlled and carries no length bound, and the header sits above
// the version picker, the tabs, and the body, so a description running to
// several hundred words pushes every one of them below the fold and the
// artifact reads as empty until the reader scrolls. The header therefore
// clips the field to the same three lines a listing row reads at (§13.10) and
// carries a control that opens the rest in place.

import { useLayoutEffect, useRef, useState } from 'react';

/** Lead renders an artifact's description under its title, clipped to three
 * lines until the reader opens it. The control appears only where the text
 * actually overruns the clip, because whether three lines hold a given string
 * is a layout fact that depends on the rendered width and the typeface: it is
 * measured against the clipped element rather than guessed at from the
 * string's length. The measurement runs while the element is clipped, so
 * expanding it does not read as the overrun having gone away. */
export function Lead({ text }: { text: string }) {
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
      <p ref={paragraph} className={expanded ? 'lead' : 'lead lead-clamped'} data-testid="artifact-lead">
        {text}
      </p>
      {overrun && (
        <button
          type="button"
          className="lead-more"
          aria-expanded={expanded}
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
