// One source cell for both layer tables: the panel's own rows and the rows
// waiting to be erased. Every source type leads with a chip naming the type,
// so two rows of different types are told apart by the chip rather than by
// the syntax of the string beside it. The identifying reference sits on the
// first line and the location details follow on quiet lines, because a raw
// repository URL or an absolute filesystem path is long enough to wrap over
// the whole cell and bury the one fact the reader is scanning for.
//
// A source type is pluggable, so a type neither table has seen still renders:
// the chip carries its name and whatever source fields the record carries sit
// behind a disclosure, which keeps an unknown row the same height as a known
// one.

import { Badge } from './primitives';
import type { LayerRecord } from '../api';

export function SourceCell({ layer }: { layer: LayerRecord }) {
  if (layer.SourceType === 'git') {
    return (
      <div className="source-cell">
        <div className="source-ref">
          <Badge tone="quiet">git</Badge>
          <span className="mono">{present(layer.Ref) ? layer.Ref : 'default branch'}</span>
        </div>
        <div className="mono quiet source-detail">{layer.Repo}</div>
        {present(layer.Root) && <div className="mono quiet source-detail">{`${layer.Root}/`}</div>}
      </div>
    );
  }
  if (layer.SourceType === 'local') {
    return (
      <div className="source-cell">
        <div className="source-ref">
          <Badge tone="quiet">local</Badge>
        </div>
        <div className="mono quiet source-detail">{layer.LocalPath}</div>
      </div>
    );
  }
  const fields = sourceFields(layer);
  return (
    <div className="source-cell">
      <div className="source-ref">
        <Badge tone="quiet">{layer.SourceType}</Badge>
      </div>
      {fields.length > 0 && (
        <details className="source-fields">
          <summary className="quiet">
            {fields.length} source {fields.length === 1 ? 'field' : 'fields'}
          </summary>
          <dl className="mono quiet source-detail">
            {fields.map(([name, value]) => (
              <div key={name}>
                <dt>{name}</dt>
                <dd>{value}</dd>
              </div>
            ))}
          </dl>
        </details>
      )}
    </div>
  );
}

/** sourceFields is the source-carrying members of the layer record that this
 * record populates. An unknown type states the fields it arrived with rather
 * than a fixed list, so a type that fills only some of them shows only
 * those. */
function sourceFields(layer: LayerRecord): [string, string][] {
  const candidates: [string, string | undefined][] = [
    ['repo', layer.Repo],
    ['ref', layer.Ref],
    ['root', layer.Root],
    ['local_path', layer.LocalPath],
  ];
  return candidates.filter((entry): entry is [string, string] => present(entry[1]));
}

function present(value: string | undefined): boolean {
  return value !== undefined && value !== '';
}
