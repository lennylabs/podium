// One source cell for both layer tables: the panel's own rows and the rows
// waiting to be erased. Every source type leads with a chip naming the type,
// so two rows of different types are told apart by the chip rather than by
// the syntax of the string beside it. The identifying reference sits on the
// first line and the location details follow on quiet lines, because a raw
// repository URL or an absolute filesystem path is long enough to wrap over
// the whole cell and bury the one fact the reader is scanning for. A detail
// line longer than the cell is clipped to one line and carries its whole value
// in the title attribute, so every row of the table keeps the same height.
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
          <Badge tone="soft">git</Badge>
          {/* §4.6: the git source resolves its tree at the ref and has no
              default, so a row carrying none is refused on every ingest with
              "git source requires ref". Reading the empty ref as a default
              branch asserts a fallback the registry does not implement, and
              the reader is left to work out from the refusal why a layer that
              registered cleanly serves nothing. The row names the missing ref
              instead. */}
          {present(layer.Ref) ? (
            <span className="mono">{layer.Ref}</span>
          ) : (
            <Badge tone="danger">no ref</Badge>
          )}
        </div>
        <Detail value={layer.Repo ?? ''} />
        {present(layer.Root) && <Detail value={`${layer.Root}/`} />}
      </div>
    );
  }
  if (layer.SourceType === 'local') {
    return (
      <div className="source-cell">
        <div className="source-ref">
          <Badge tone="soft">local</Badge>
        </div>
        <Detail value={layer.LocalPath ?? ''} />
      </div>
    );
  }
  const fields = sourceFields(layer);
  return (
    <div className="source-cell">
      <div className="source-ref">
        <Badge tone="soft">{layer.SourceType}</Badge>
      </div>
      {fields.length > 0 && (
        <details className="source-fields">
          <summary className="quiet">
            {fields.length} source {fields.length === 1 ? 'field' : 'fields'}
          </summary>
          <dl className="mono quiet">
            {fields.map(([name, value]) => (
              <div key={name}>
                <dt>{name}</dt>
                <dd title={value}>{value}</dd>
              </div>
            ))}
          </dl>
        </details>
      )}
    </div>
  );
}

/** Detail is one quiet location line under the source reference. The line is
 * split so that the clip falls on the leading directories and the identifying
 * final segment is always drawn: several layers under one parent share every
 * leading directory, and clipping from the right rendered them as the same
 * string, which stops the column telling the rows apart. The head gives up
 * its own leading characters to the clip, so what it keeps ends against the
 * tail and the line reads as one path. Where the tail alone is wider than the
 * cell, the head has already collapsed to the width of its own ellipsis and
 * the tail takes the same leading elision, so the line always states that it
 * is truncated. The title
 * attribute repeats the whole value because the head is still clipped where
 * the column is narrower than it. */
function Detail({ value }: { value: string }) {
  const { head, tail } = splitDetail(value);
  return (
    <div className="mono quiet source-detail" title={value}>
      {/* The head is elided at its start, so its own bidirectional
          isolation keeps a leading separator at the left of the run rather
          than reordered to its end. The run is drawn only where the value
          carries leading directories, because it reserves the width of its
          own ellipsis and an empty run then indented a value such as
          `artifacts/` by two characters for a prefix it does not have. */}
      {head !== '' && (
        <span className="source-detail-head">
          <bdi>{head}</bdi>
        </span>
      )}
      {/* The tail is elided at its start for the same reason, and isolates
          its own text so a separator is not reordered to the far end. */}
      <span className="source-detail-tail">
        <bdi>{tail}</bdi>
      </span>
    </div>
  );
}

/** splitDetail divides a location line into the head the cell may clip and
 * the tail it holds out of the clip. The tail is the value's final path
 * segment, and a trailing separator travels with it so a root such as
 * `artifacts/` reads whole. */
export function splitDetail(value: string): { head: string; tail: string } {
  const stripped = value.endsWith('/') ? value.slice(0, -1) : value;
  const cut = stripped.lastIndexOf('/');
  return { head: value.slice(0, cut + 1), tail: value.slice(cut + 1) };
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
