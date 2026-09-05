// What a layer record states about itself, derived once for every surface
// that reports it. The layer panel's rows, the unregister confirmation, and
// the artifact viewer's provenance rail all name the audience a layer grants
// and the run it last ingested, so they derive both here and one layer reads
// the same way wherever it is reported.

import type { LayerRecord } from '../api';

/** VisibilityMarker is one axis's marker: the axis and the members it names,
 * and the count of members it does not. */
export interface VisibilityMarker {
  named: string;
  extra: number;
}

/** visibilityMarkers returns one marker per matching axis, in the fixed order
 * public, organization, groups, then users, because §4.6 defines visibility
 * as independent grants that combine as a union. Two layers carrying the same
 * grants therefore read identically, and no axis is dropped. */
export function visibilityMarkers(layer: LayerRecord): VisibilityMarker[] {
  const groups = layer.groups ?? [];
  const users = layer.users ?? [];
  return [
    layer.public === true ? { named: 'public', extra: 0 } : null,
    layer.organization === true ? { named: 'organization', extra: 0 } : null,
    groups.length > 0 ? summarize('group', groups) : null,
    users.length > 0 ? summarize('user', users) : null,
  ].filter((marker): marker is VisibilityMarker => marker !== null);
}

/** markerText states a marker as one string, which is what a surface that
 * reads visibility as a line rather than as a row of badges renders. */
export function markerText(marker: VisibilityMarker): string {
  return marker.extra > 0 ? `${marker.named} +${String(marker.extra)}` : marker.named;
}

/** noGrants is what a surface states for a record that sets no visibility
 * field. It states the record's own fact and asserts nothing about who reads
 * the layer, because who reads it depends on the deployment rather than on
 * the record: §4.6 matches no condition on such a record, and the bypasses in
 * the same section admit every caller on a registry running in public mode or
 * configuring no identity provider. The Edit dialog withdraws each axis, so
 * the state is one an operator reaches deliberately, and the earlier copy
 * naming a grant the registrant retains stated an access the record does not
 * carry. A surface that holds the §7.3.4 posture read is where a reachability
 * reading belongs. */
export const noGrants = 'no grants';

/** visibilitySummary is every axis a layer grants on, as one line. A layer
 * that grants on no axis states that rather than rendering an empty line,
 * because the absence of grants is the fact the reader needs. */
export function visibilitySummary(layer: LayerRecord): string {
  const markers = visibilityMarkers(layer);
  return markers.length === 0 ? noGrants : markers.map(markerText).join(', ');
}

/** memberBudget is how many characters of member names one marker states
 * before the rest become a count. The visibility column is the narrowest of
 * the layer row's text columns, and a marker wider than this was clipped by
 * the cell part-way through a name, which took the remainder count with it.
 * Two short group names fit the budget; two addresses do not, so an axis of
 * addresses names one and counts the rest. */
const memberBudget = 24;

/** summarize keeps an axis that names more members than the row can hold
 * inside its own marker, so the axis stays visible and the count is not
 * dropped to make room. It names as many members as the budget holds, and at
 * least one however long that one is. */
function summarize(axis: string, members: string[]): VisibilityMarker {
  const named: string[] = [];
  let width = 0;
  for (const member of members) {
    const cost = named.length === 0 ? member.length : member.length + 3;
    if (named.length > 0 && width + cost > memberBudget) {
      break;
    }
    named.push(member);
    width += cost;
  }
  return {
    named: `${axis}: ${named.join(' · ')}`,
    extra: members.length - named.length,
  };
}

/** ingestRef is the reference a surface displays beside the ingest age. A git
 * source records the commit the run landed on, which is the fact a reader
 * compares across runs. A local source records the directory it read, which
 * the surfaces already state elsewhere and which wraps over several lines in
 * the narrow columns that report it, so a non-git layer displays no
 * reference. */
export function ingestRef(layer: LayerRecord): string {
  if (layer.source_type !== 'git') {
    return '';
  }
  return layer.last_ingested_ref ?? '';
}

/** shortRef abbreviates an ingest reference to what a reader compares. A
 * commit SHA is stated at its short length, and a reference that is not a
 * SHA, such as a branch name, is stated whole. */
export function shortRef(ref: string): string {
  return /^[0-9a-f]{12,}$/i.test(ref) ? ref.slice(0, 7) : ref;
}

/** ingestedRef pairs the branch the layer tracks with the commit its last run
 * landed on, in the `main@4f2a1c9` form the rail states. The commit alone does
 * not say what it was reached through, and a layer that has ingested no commit
 * still tracks a branch, so each half stands where the other is absent. */
export function ingestedRef(layer: LayerRecord): string {
  const commit = ingestRef(layer);
  const branch = layer.source_type === 'git' ? (layer.ref ?? '') : '';
  if (commit === '') {
    return branch;
  }
  return branch === '' ? shortRef(commit) : `${branch}@${shortRef(commit)}`;
}
