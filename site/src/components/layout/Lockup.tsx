import type { ReactElement } from "react";

import { LOCKUPS } from "../../build/logo";

/**
 * The Podium logotype: the mark and the wordmark as one unit.
 *
 * Both theme variants are inlined and CSS shows one, rather than one copy whose
 * fills are rewritten from tokens. The design files carry their own colours, and
 * reproducing them by mapping elements to variables would mean guessing at any
 * element a future revision adds. Two inline copies cost a few hundred bytes and
 * no extra request, and they stay exactly what the designer drew.
 *
 * The markup is read from docs/assets/logo at build time, so editing a lockup
 * file changes the site on the next build.
 *
 * This module reaches the filesystem and is therefore build-only. It must not
 * be imported from anything under src/client.
 */
export function Lockup(props: { className?: string; label?: string }): ReactElement {
  const label = props.label ?? "Podium";
  const extra = props.className === undefined ? "" : ` ${props.className}`;

  return (
    <span className={`lockup${extra}`} role="img" aria-label={label}>
      <svg
        className="lockup-art lockup-art-light"
        viewBox={LOCKUPS.light.viewBox}
        aria-hidden="true"
        focusable="false"
        dangerouslySetInnerHTML={{ __html: LOCKUPS.light.inner }}
      />
      <svg
        className="lockup-art lockup-art-dark"
        viewBox={LOCKUPS.dark.viewBox}
        aria-hidden="true"
        focusable="false"
        dangerouslySetInnerHTML={{ __html: LOCKUPS.dark.inner }}
      />
    </span>
  );
}
