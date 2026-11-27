// The warnings a layer write returns. A registry that verifies no caller
// applies no layer visibility, so its register, update, and restore responses
// carry a warning for a layer that is not public (§7.3.1). The write itself
// succeeded, and the outcome banner says so; without this block the reader
// would take a layer restricted to a group or to its owner as hidden from
// everyone else, when every caller sees it.

import { Banner } from './primitives';

/**
 * LayerWarnings renders the warnings member of a layer write response in the
 * danger tone, each warning in the registry's own words. A response that
 * carries none renders nothing, so a caller passes the member unconditionally.
 *
 * A caller that mounts the block at the moment the warnings arrive, outside a
 * dialog that takes focus, passes live={false} and announces the text through
 * a live region it keeps mounted, because a status region mounted with its
 * text is not announced.
 */
export function LayerWarnings({ warnings, live = true }: { warnings?: string[]; live?: boolean }) {
  if (warnings === undefined || warnings.length === 0) {
    return null;
  }
  return (
    <div data-testid="layer-warnings">
      <Banner tone="danger" glyph="!" live={live}>
        <p className="banner-title">Visibility is not applied on this registry.</p>
        {warnings.map((warning) => (
          <p key={warning}>{warning}</p>
        ))}
      </Banner>
    </div>
  );
}
