// One source cell for both layer tables: the panel's own rows and the rows
// waiting to be erased. A source type is pluggable, so a type neither table
// has seen renders as its name rather than as a broken row.

import { Badge } from './primitives';
import type { LayerRecord } from '../api';

export function SourceCell({ layer }: { layer: LayerRecord }) {
  if (layer.SourceType === 'git') {
    return (
      <span className="mono">
        {layer.Repo}
        {layer.Ref !== undefined && layer.Ref !== '' ? `@${layer.Ref}` : ''}
      </span>
    );
  }
  if (layer.SourceType === 'local') {
    return <span className="mono">{layer.LocalPath}</span>;
  }
  return <Badge tone="quiet">{layer.SourceType}</Badge>;
}
