// Scroll locking for the overlays the shell opens. A dialog that declares
// itself modal covers the page and keeps focus, but the wheel still reaches
// the document underneath it: a scroll anywhere, including over the scrim,
// slides the surface around behind a dialog that stays pinned, and a reader
// who came to a one-time credential loses the place they were reading.

import { useEffect } from 'react';

// locked counts the overlays holding the page still, because one dialog can
// open over another (a modal over the palette, or a form replaced by the
// outcome of its own submission) and the second one's release must not undo
// the first one's lock. The saved values are the document's own, read once
// when the count goes from none to one and written back when it returns to
// none, so a page that sets its own overflow keeps it.
let locked = 0;
let saved: { overflow: string; paddingRight: string } | null = null;

function hold(): void {
  locked += 1;
  if (locked > 1) {
    return;
  }
  const root = document.documentElement;
  saved = { overflow: root.style.overflow, paddingRight: document.body.style.paddingRight };
  // The viewport's scrollbar leaves with the overflow, and the surface behind
  // the scrim widens into the space it held unless the width is given back as
  // padding. A platform drawing an overlay scrollbar reports zero here and
  // gets no padding. A document that has not been laid out reports a client
  // width of zero, which would read as a gutter the width of the viewport, so
  // that arm gets no padding either.
  const gutter = root.clientWidth === 0 ? 0 : window.innerWidth - root.clientWidth;
  root.style.overflow = 'hidden';
  if (gutter > 0) {
    document.body.style.paddingRight = `${gutter}px`;
  }
}

function release(): void {
  locked = Math.max(0, locked - 1);
  if (locked > 0 || saved === null) {
    return;
  }
  document.documentElement.style.overflow = saved.overflow;
  document.body.style.paddingRight = saved.paddingRight;
  saved = null;
}

/**
 * useScrollLock holds the document still for as long as `active` holds, and
 * gives it back when the overlay closes. The scroll position is kept, because
 * hiding the viewport's overflow stops the wheel without moving what is
 * already scrolled to, so a reader who dismisses the dialog resumes where the
 * page was.
 */
export function useScrollLock(active = true): void {
  useEffect(() => {
    if (!active) {
      return;
    }
    hold();
    return release;
  }, [active]);
}
