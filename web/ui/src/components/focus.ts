// Focus management for the dialogs the shell opens. A dialog that covers the
// page has to take the reader's focus with it. Focus left behind on the
// surface underneath puts a keyboard reader on controls the scrim has hidden,
// and a dialog that closes without handing focus back drops the reader at the
// top of the document with no way to resume where they were.

import { useEffect, useRef } from 'react';
import type { RefObject } from 'react';

/**
 * dismissAttribute marks a control whose only job is to close the dialog. A
 * dialog opens with focus on the first control that does its work, so the
 * dismissal ✕ that leads the header is skipped when focus moves in: landing
 * there makes the reader's first Enter close the dialog they just opened.
 * The control stays in the Tab cycle.
 */
export const dismissAttribute = 'data-dialog-dismiss';

/** focusableStops matches the controls a dialog can hand focus to. */
const focusableStops = [
  'a[href]',
  'button:not([disabled])',
  'input:not([disabled])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  '[tabindex]:not([tabindex="-1"])',
].join(', ');

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
 */
export function useDialogFocus<T extends HTMLElement>(active = true): RefObject<T | null> {
  const container = useRef<T>(null);
  useEffect(() => {
    if (!active) {
      return;
    }
    const dialog = container.current;
    if (dialog === null) {
      return;
    }
    const held = document.activeElement;
    const opener = held instanceof HTMLElement && held !== document.body ? held : null;
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
      if (opener !== null && opener.isConnected) {
        opener.focus();
      }
    };
  }, [active]);
  return container;
}
