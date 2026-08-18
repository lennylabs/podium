import type { ComponentType } from "react";

/**
 * Animated diagram variants, keyed by the stem of the checked-in SVG.
 *
 * `::diagram{name="sync-watch" alt="..."}` renders the variant registered here
 * when one exists, and the checked-in docs/assets/diagrams/sync-watch.svg when
 * it does not. The static SVG stays the source of the content either way, so a
 * reader without JavaScript and a reader on GitHub both see the diagram.
 *
 * Adding a variant is adding an entry here.
 */
export const diagramVariants: Record<
  string,
  () => Promise<{ default: ComponentType<{ name: string; alt: string }> }>
> = {};
