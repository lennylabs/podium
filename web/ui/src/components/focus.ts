// Focus management for the dialogs the shell opens. A dialog that covers the
// page has to take the reader's focus with it. Focus left behind on the
// surface underneath puts a keyboard reader on controls the scrim has hidden,
// and a dialog that closes without handing focus back drops the reader at the
// top of the document with no way to resume where they were.

import { useEffect, useRef, useSyncExternalStore } from 'react';
import type { RefObject } from 'react';

/**
 * dismissAttribute marks a control whose only job is to close the dialog. A
 * dialog opens with focus on the first control that does its work, so the
 * dismissal ✕ that leads the header is skipped when focus moves in: landing
 * there makes the reader's first Enter close the dialog they just opened.
 * The control stays in the Tab cycle.
 */
export const dismissAttribute = 'data-dialog-dismiss';

/**
 * focusableStops matches the controls a dialog can hand focus to. Every arm
 * excludes `tabindex="-1"`, because a control carrying it is reachable by
 * script alone and the browser's own Tab order walks past it. A dialog that
 * counted one as a stop, such as a row of a roving-tabindex list, never saw
 * focus reach the stop it treats as the last one, so the wrap below never
 * fired and Tab walked out onto the surface the scrim covers.
 */
const focusableStops = [
  'a[href]',
  'button:not([disabled])',
  'input:not([disabled])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  '[tabindex]',
]
  .map((stop) => `${stop}:not([tabindex="-1"])`)
  .join(', ');

/**
 * takeFocus hands focus to a landmark that never receives it by keyboard,
 * such as a surface heading. A write that removes the control it was started
 * from leaves focus on the document body: the next Tab restarts at the top of
 * the page, and a screen reader is left on nothing while the row it was on
 * disappears. The surface hands focus to its own heading instead, which is a
 * stable place to resume from and is read out as the reader lands there.
 *
 * The element is made programmatically focusable, because a heading is not a
 * tab stop and focusing one the browser does not consider focusable drops
 * focus on the document.
 */
export function takeFocus(target: HTMLElement | null): void {
  if (target === null || !target.isConnected) {
    return;
  }
  target.tabIndex = -1;
  target.focus();
}

/**
 * useDialogFocus gives a dialog the three behaviours a `role="dialog"` owes a
 * keyboard reader, for as long as `active` holds: focus moves to the first
 * control inside it that is not a dismissal when it opens, Tab and Shift+Tab
 * cycle within it rather than walking out onto the covered surface, and focus
 * returns to whatever held it when the dialog opened once the dialog closes.
 *
 * Attach the returned ref to the element carrying `role="dialog"`. The
 * element the dialog was opened from is read at that moment rather than
 * passed in, so a dialog does not have to know what opened it; a control that
 * has left the document by the time the dialog closes is skipped, because
 * focusing a detached node silently drops focus on the document instead.
 *
 * A caller that presents a sequence of dialogs through one element, such as a
 * form the outcome of its own submission replaces, passes an `identity` that
 * changes with the dialog. Without it React reuses the element, the effect
 * does not run again, and the second dialog opens with focus wherever the
 * first left it: on the document, once the submit control that held it
 * unmounted.
 */
export function useDialogFocus<T extends HTMLElement>(active = true, identity?: unknown): RefObject<T | null> {
  const container = useRef<T>(null);
  // The control the dialog was opened from is read once for as long as the
  // dialog is open, and handing focus back is its own effect for that reason:
  // re-reading it when the identity changes would read the document, because
  // the control the reader was on unmounts with the content being replaced.
  useEffect(() => {
    if (!active) {
      return;
    }
    const held = document.activeElement;
    const opener = held instanceof HTMLElement && held !== document.body ? held : null;
    return () => {
      if (opener !== null && opener.isConnected) {
        opener.focus();
      }
    };
  }, [active]);
  useEffect(() => {
    if (!active) {
      return;
    }
    const dialog = container.current;
    if (dialog === null) {
      return;
    }
    const stops = () => Array.from(dialog.querySelectorAll<HTMLElement>(focusableStops));
    const opening = stops().filter((stop) => !stop.hasAttribute(dismissAttribute));
    if (opening.length > 0) {
      opening[0].focus();
    } else {
      // A dialog whose only control dismisses it still takes the reader's
      // focus off the covered surface, so the dialog itself receives it.
      dialog.tabIndex = -1;
      dialog.focus();
    }
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Tab') {
        return;
      }
      const inside = stops();
      if (inside.length === 0) {
        event.preventDefault();
        return;
      }
      const last = inside[inside.length - 1];
      const edge = event.shiftKey ? inside[0] : last;
      // Focus sitting outside the dialog is wrapped back in as well, because
      // the browser parks focus on the document when the control that opened
      // the dialog is removed, and Tab from there walks the covered surface.
      if (document.activeElement === edge || !dialog.contains(document.activeElement)) {
        event.preventDefault();
        (event.shiftKey ? last : inside[0]).focus();
      }
    };
    document.addEventListener('keydown', onKeyDown);
    return () => {
      document.removeEventListener('keydown', onKeyDown);
    };
  }, [active, identity]);
  return container;
}

/**
 * usePopupDismiss gives a transient popup the dismissal paths every overlay
 * in this shell owes a reader: Escape closes it and hands focus back to the
 * control that opened it, a pointer press anywhere outside it closes it, and
 * focus landing outside it closes it. The last two also make the popups
 * exclusive, because opening a second one presses or focuses outside the
 * first. A popup whose only exit is its own trigger strands a reader who
 * opened it to look, and leaves stale menus stacked over the surface.
 *
 * Attach the returned ref to the popup element and pass the trigger's ref.
 * The trigger counts as inside, so the press that toggles the popup shut is
 * not also read as an outside press, which would close and reopen it.
 */
export function usePopupDismiss<T extends HTMLElement>(
  open: boolean,
  onClose: () => void,
  trigger: RefObject<HTMLElement | null>,
): RefObject<T | null> {
  const popup = useRef<T>(null);
  // The close callback is read through a ref so a caller that rebuilds it on
  // every render does not tear the listeners down and set them up again.
  const close = useRef(onClose);
  close.current = onClose;
  useEffect(() => {
    if (!open) {
      return;
    }
    const outside = (target: EventTarget | null) => {
      if (!(target instanceof Node)) {
        return true;
      }
      return !(popup.current?.contains(target) ?? false) && !(trigger.current?.contains(target) ?? false);
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') {
        return;
      }
      close.current();
      trigger.current?.focus();
    };
    const onOutside = (event: Event) => {
      if (outside(event.target)) {
        close.current();
      }
    };
    document.addEventListener('keydown', onKeyDown);
    // Capture, so a press on a control that unmounts itself still reports a
    // target the popup can compare against.
    document.addEventListener('pointerdown', onOutside, true);
    document.addEventListener('focusin', onOutside, true);
    return () => {
      document.removeEventListener('keydown', onKeyDown);
      document.removeEventListener('pointerdown', onOutside, true);
      document.removeEventListener('focusin', onOutside, true);
    };
  }, [open, trigger]);
  return popup;
}

// holds counts the dialogs on the page that refuse every dismissal route. It is
// module state because the accelerators that open an overlay live in the shell,
// above whatever surface opened the dialog, and a dialog does not know what is
// listening for a key it has no way to see.
let holds = 0;
const heldListeners = new Set<() => void>();

function publishHeld(): void {
  for (const listener of heldListeners) {
    listener();
  }
}

/**
 * holdDismissal marks the page as carrying a dialog the reader cannot leave by
 * any route the dialog does not itself offer, and returns the release the
 * caller runs when that dialog goes away. Content shown once and unrecoverable
 * is what this is for: the one-time webhook secret withholds Escape, the scrim,
 * and the close control, and an overlay opened over it from elsewhere would
 * take focus and discard the credential when the reader followed it.
 */
export function holdDismissal(): () => void {
  holds += 1;
  publishHeld();
  let released = false;
  return () => {
    if (released) {
      return;
    }
    released = true;
    holds -= 1;
    publishHeld();
  };
}

/**
 * useDismissalHeld reports whether such a dialog is open. A page-level
 * accelerator reads it and does nothing while it holds, so the acknowledgement
 * the dialog gates on stays the one way out.
 */
export function useDismissalHeld(): boolean {
  return useSyncExternalStore(
    (listener) => {
      heldListeners.add(listener);
      return () => {
        heldListeners.delete(listener);
      };
    },
    () => holds > 0,
    () => false,
  );
}
