// Author-controlled content a surface clips to a fixed number of lines until
// the reader opens it. Neither a description nor a tag sequence carries a
// length bound, and a long one pushes everything under it off the fold: in the
// artifact header it buries the body, and in the rail's frontmatter table it
// buries the relation links §13.10 requires the viewer to carry. Every such
// site therefore clips the value and offers the rest in place.
//
// Spec: §13.10

import { useId, useLayoutEffect, useRef, useState, type RefObject } from 'react';

/** Clamp is the state one clipped value carries: whether it is open, whether
 * the clip actually cuts anything, and the id that ties the control to the
 * region it opens.
 *
 * The control appears only where the content actually overruns the clip,
 * because whether three lines hold a given value is a layout fact that depends
 * on the rendered width and the typeface: it is measured against the clipped
 * element rather than guessed at from the value's length. The measurement runs
 * while the element is clipped, so expanding it does not read as the overrun
 * having gone away. */
interface Clamp<T extends HTMLElement> {
  /** ref goes on the element the clip class is applied to, because the
   * measurement compares that element's scroll height with its own box. */
  ref: RefObject<T | null>;
  /** region is the id the clipped element takes and the control points at, so
   * a screen reader announces the open state over a named region. It comes
   * from useId, so the rail, which renders one clip per property row, does not
   * repeat an id across the document. */
  region: string;
  expanded: boolean;
  overrun: boolean;
  toggle: () => void;
}

/** useClamp carries the clip state for one value. `key` is the content the
 * measurement belongs to: a route change swaps a new value into the same
 * component instance rather than remounting it, so the state measured against
 * the old content has to be dropped when the key changes. Without the reset,
 * an opened value leaves its control standing over the next artifact's short
 * one, where it collapses nothing, because the measurement below returns early
 * while expanded and the overrun is never recomputed. React re-renders on this
 * before it commits, so the stale control never reaches the screen. */
export function useClamp<T extends HTMLElement>(key: string): Clamp<T> {
  const region = useId();
  const [expanded, setExpanded] = useState(false);
  const [overrun, setOverrun] = useState(false);
  const element = useRef<T>(null);
  const [measured, setMeasured] = useState(key);
  if (measured !== key) {
    setMeasured(key);
    setExpanded(false);
    setOverrun(false);
  }

  useLayoutEffect(() => {
    const node = element.current;
    if (node === null || expanded) {
      return;
    }
    setOverrun(node.scrollHeight > node.clientHeight);
  }, [key, expanded]);

  return {
    ref: element,
    region,
    expanded,
    overrun,
    toggle: () => {
      setExpanded(!expanded);
    },
  };
}

/** ClampMore is the control that opens one clipped value. It renders nothing
 * where the clip cuts nothing, so a value that already fits carries no control.
 *
 * label names the control for a reader who meets it out of the surrounding
 * text, which is what a screen reader running down a rail of property rows
 * does. A site carrying one control leaves it unset and the button reads as its
 * own label. */
export function ClampMore<T extends HTMLElement>({
  clamp,
  label,
}: {
  clamp: Clamp<T>;
  label?: string;
}) {
  if (!clamp.overrun) {
    return null;
  }
  return (
    <button
      type="button"
      className="clamp-more"
      aria-expanded={clamp.expanded}
      aria-controls={clamp.region}
      aria-label={label}
      onClick={clamp.toggle}
    >
      {clamp.expanded ? 'Show less' : 'Show more'}
    </button>
  );
}

/** ClampedText renders a scalar under the shared three-line clip and carries
 * the control that opens it. */
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
  moreLabel?: string;
}) {
  const clamp = useClamp<HTMLParagraphElement>(text);
  return (
    <>
      <p
        ref={clamp.ref}
        id={clamp.region}
        className={clamp.expanded ? className : `${className} clamped`}
        data-testid={testID}
      >
        {text}
      </p>
      <ClampMore clamp={clamp} label={moreLabel} />
    </>
  );
}
