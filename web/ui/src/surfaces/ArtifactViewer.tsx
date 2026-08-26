// The artifact viewer. It renders the manifest body as a document through the
// sanitized rendering path, presents the frontmatter as a property table
// whose values are text, and links to the artifacts that extend or depend on
// this one, which §13.10 requires.
//
// The layout is two columns: the content column carries the header, the tabs,
// and whichever tab is open, and the rail beside it carries the metadata that
// arrived with the registry's response. The tabs are Rendered, Frontmatter,
// Authored source, and Resources, and each one disappears where the artifact
// carries nothing for it rather than standing an empty panel in the layout.

import { useState } from 'react';

import type { KeyboardEvent } from 'react';

import { ArtifactBody } from '../components/ArtifactBody';
import { Breadcrumb } from '../components/Breadcrumb';
import {
  Badge,
  CopyButton,
  EmptyState,
  ErrorState,
  Loading,
  SensitivityBadge,
  TypeBadge,
  VersionBadge,
} from '../components/primitives';
import { PropertyTable } from '../components/PropertyTable';
import { parseFrontmatter, splitDocument } from '../frontmatter';
import type { DependencyEdge, LargeResourceLink, LoadArtifactResponse } from '../api';
import { dependentsOf, loadArtifact } from '../api';
import { artifactHref } from '../route';
import { useAsync, useErrorReport } from '../useAsync';

type TabName = 'rendered' | 'frontmatter' | 'source' | 'resources';

/** fetchedDelivery is the delivery a file retrieved from object storage
 * carries. The rail groups on it and the resource table states it in its own
 * column. */
const fetchedDelivery = 'fetched on demand';

export function ArtifactViewer({ id, onError }: { id: string; onError: (err: unknown) => void }) {
  // An empty version is the default read, which the registry answers with the
  // latest version. The picker sets one, and the notice keys on the pair
  // rather than on the picker alone, because a reader who picks the version
  // the registry already served is looking at the latest one.
  const [viewing, setViewing] = useState('');
  const [latest, setLatest] = useState('');
  // The response the page is standing on. A version the registry cannot
  // resolve is refused, and the picker that asked for it lives on this page,
  // so the last response that did resolve is held and keeps the page drawn
  // while the refusal is presented beside the control that caused it. Without
  // it the reader loses the picker along with the rest of the surface, and
  // the route still names this artifact, so nothing is left to recover with.
  const [held, setHeld] = useState<LoadArtifactResponse | null>(null);
  const artifact = useAsync(() => loadArtifact(id, viewing === '' ? undefined : viewing), [id, viewing]);
  useErrorReport(artifact.error, onError);
  if (artifact.value !== null && artifact.value !== held) {
    setHeld(artifact.value);
  }
  // A held response belongs to the identifier it was read for, so navigating
  // to another artifact starts over rather than standing the previous one's
  // document under the new route.
  const body = artifact.value ?? (held !== null && held.id === id ? held : null);
  // A manifest above the inline cutoff arrives as a presigned URL with the
  // inline body empty, so the document is fetched here and both columns read
  // the result: the fetch's own loading and failure states are confined to
  // the content column, because the rail's metadata came with the registry's
  // own response and is already there.
  const link = body?.manifest_body_url;
  const fetched = useAsync(async () => (link === undefined ? '' : fetchText(link)), [link?.presigned_url ?? '']);

  if (body === null) {
    if (artifact.error !== null) {
      return <ErrorState error={artifact.error} onRetry={artifact.reload} />;
    }
    return artifact.loading ? <Loading label="Loading the artifact." /> : null;
  }
  if (viewing === '' && latest !== body.version) {
    setLatest(body.version);
  }
  const older = latest !== '' && body.version !== latest;
  // The presigned channel delivers the canonical manifest document rather
  // than a body, and the response cleared the field that document
  // duplicates, so the client reconstitutes both halves from it: the body
  // reaches the rendering path and the frontmatter block reaches the
  // property table. The fences reach neither, and the response's own
  // frontmatter wins where it survived, which is the skill case, where the
  // fetched document is the authored skill file rather than the manifest.
  const split = splitDocument(fetched.value ?? '');
  const document: ManifestHalves =
    body.manifest_body_url === undefined
      ? { body: body.manifest_body, frontmatter: body.frontmatter }
      : { body: split.body, frontmatter: body.frontmatter === '' ? split.frontmatter : body.frontmatter };

  const description = descriptionOf(document.frontmatter);

  return (
    <section className="surface artifact-viewer" aria-label="Artifact viewer">
      <div className="artifact-content">
        <Breadcrumb path={domainOf(body.id)} />
        <div className="page-title">
          <h1>{artifactName(body.id)}</h1>
          <TypeBadge type={body.type} />
          <VersionBadge version={body.version} />
          <SensitivityBadge sensitivity={body.sensitivity} />
        </div>
        <p className="mono quiet artifact-id-line">{body.id}</p>
        {description !== '' && <p className="lead">{description}</p>}
        <div className="artifact-meta">
          <VersionPicker
            key={viewing}
            viewing={viewing}
            onView={(version) => {
              setViewing(version);
            }}
          />
          {artifact.loading && <Loading label="Loading the artifact." />}
        </div>
        {artifact.error !== null && (
          <ErrorState
            error={artifact.error}
            title="The registry did not serve that version."
            testID="version-refused"
          >
            <p className="quiet">Still showing version {body.version}.</p>
            <button
              type="button"
              onClick={() => {
                setViewing('');
              }}
            >
              Show latest
            </button>
          </ErrorState>
        )}
        {older && (
          <div className="banner banner-accent" role="status" data-testid="older-version">
            <p className="banner-title">You are reading version {body.version}.</p>
            <button
              type="button"
              onClick={() => {
                setViewing('');
              }}
            >
              Go to {latest}
            </button>
          </div>
        )}
        {body.manifest_body_url !== undefined && fetched.loading && <Loading label="Fetching the artifact." />}
        {body.manifest_body_url !== undefined && fetched.error !== null && (
          <ErrorState error={fetched.error} onRetry={fetched.reload} />
        )}
        {(body.manifest_body_url === undefined || (!fetched.loading && fetched.error === null)) && (
          <Manifest artifact={body} document={document} />
        )}
      </div>
      <ArtifactRail artifact={body} frontmatter={document.frontmatter} />
    </section>
  );
}

/** artifactName is the page title: the last segment of the artifact's §4.2
 * path, which is what names the artifact itself. The domains above it are
 * the breadcrumb's, and the whole identifier stands under the title. */
function artifactName(id: string): string {
  const cut = id.lastIndexOf('/');
  return cut < 0 ? id : id.slice(cut + 1);
}

/** domainOf is the domain the artifact sits in, which is where the
 * breadcrumb above the title leads. An identifier carrying no separator sits
 * at the registry root. */
function domainOf(id: string): string {
  const cut = id.lastIndexOf('/');
  return cut < 0 ? '' : id.slice(0, cut);
}

/** descriptionOf is the artifact's own description, which the header states
 * under the title. load_artifact reports no description field of its own, so
 * it is read from the frontmatter the response carries, and a block that
 * declares none yields nothing rather than a placeholder. */
function descriptionOf(frontmatter: string): string {
  const found = parseFrontmatter(frontmatter).properties.find((property) => property.key === 'description');
  return found?.value ?? '';
}

/** ManifestHalves is the manifest document split into the half the rendering
 * path renders and the half the property table renders. */
interface ManifestHalves {
  body: string;
  frontmatter: string;
}

/** VersionPicker takes the version the reader wants. load_artifact defaults
 * to the latest version and takes any other, and no response reports which
 * versions exist, so the picker takes one rather than listing a set the
 * registry does not serve. */
function VersionPicker({ viewing, onView }: { viewing: string; onView: (version: string) => void }) {
  const [typed, setTyped] = useState(viewing);
  return (
    <span className="version-picker">
      <label className="label" htmlFor="version-picker-input">
        Version
      </label>
      <input
        id="version-picker-input"
        type="text"
        value={typed}
        placeholder="latest"
        onChange={(event) => {
          setTyped(event.target.value);
        }}
      />
      <button
        type="button"
        onClick={() => {
          onView(typed.trim());
        }}
      >
        View
      </button>
    </span>
  );
}

/** Manifest is the content column's tabbed body: the manifest rendered as a
 * document, its frontmatter as a full-width property table, the authored
 * skill file where the artifact carries one, and the bundled files. A tab
 * whose artifact carries nothing is not drawn, so the set can shrink without
 * leaving a hole. */
function Manifest({ artifact, document }: { artifact: LoadArtifactResponse; document: ManifestHalves }) {
  const [tab, setTab] = useState<TabName>('rendered');
  const { body, frontmatter } = document;
  const skillRaw = artifact.skill_raw ?? '';
  const resources = resourceRows(artifact);
  // The frontmatter block is parsed here rather than inside the panel,
  // because the tab badge reports the parse failure and the tab is drawn
  // before the panel it opens.
  const invalid = parseFrontmatter(frontmatter).error !== '';
  const tabs: { name: TabName; label: string; badge: string }[] = [
    { name: 'rendered', label: 'Rendered', badge: '' },
    { name: 'frontmatter', label: 'Frontmatter', badge: invalid ? '!' : '' },
    ...(skillRaw === '' ? [] : [{ name: 'source' as TabName, label: 'Authored source', badge: '' }]),
    ...(resources.length === 0
      ? []
      : [{ name: 'resources' as TabName, label: 'Resources', badge: String(resources.length) }]),
  ];
  // A tab can disappear between renders, which is what happens when the
  // manifest arrives by link and the authored source it carried is cleared.
  const open = tabs.some((entry) => entry.name === tab) ? tab : 'rendered';

  // A `role="tablist"` is one stop in the Tab order, and the arrows move
  // between the tabs inside it. Without this the widget announces itself as a
  // tab set and then behaves as a row of buttons, which is the state a
  // screen-reader user is left to reconcile.
  const onArrow = (event: KeyboardEvent<HTMLDivElement>) => {
    const at = tabs.findIndex((entry) => entry.name === open);
    let next = at;
    switch (event.key) {
      case 'ArrowRight':
        next = (at + 1) % tabs.length;
        break;
      case 'ArrowLeft':
        next = (at + tabs.length - 1) % tabs.length;
        break;
      case 'Home':
        next = 0;
        break;
      case 'End':
        next = tabs.length - 1;
        break;
      default:
        return;
    }
    event.preventDefault();
    setTab(tabs[next].name);
    // Selection follows focus, so the focus moves with the selection rather
    // than staying on the tab the reader has already left.
    event.currentTarget.querySelectorAll<HTMLButtonElement>('[role="tab"]')[next]?.focus();
  };

  return (
    <>
      <div className="tabs" role="tablist" aria-label="Artifact views" onKeyDown={onArrow}>
        {tabs.map((entry) => (
          <button
            key={entry.name}
            type="button"
            role="tab"
            id={`tab-${entry.name}`}
            aria-selected={open === entry.name}
            aria-controls={`panel-${entry.name}`}
            // The roving tabindex: the tab set is one Tab stop, and the open
            // tab is the one it lands on.
            tabIndex={open === entry.name ? 0 : -1}
            className={open === entry.name ? 'tab tab-open' : 'tab'}
            onClick={() => {
              setTab(entry.name);
            }}
          >
            {entry.label}
            {entry.badge !== '' && <Badge tone={entry.badge === '!' ? 'danger' : 'quiet'}>{entry.badge}</Badge>}
          </button>
        ))}
      </div>
      <div role="tabpanel" id={`panel-${open}`} aria-labelledby={`tab-${open}`}>
        {open === 'rendered' && <ArtifactBody body={body} />}
        {open === 'frontmatter' && <PropertyTable raw={frontmatter} offerRaw />}
        {open === 'source' && <AuthoredSource name="SKILL.md" value={skillRaw} />}
        {open === 'resources' && <ResourceTable rows={resources} />}
      </div>
    </>
  );
}

/** AuthoredSource is the authored file laid out as a file view: a line
 * stating what the tab holds with the take-away controls beside it, then a
 * bordered block whose header names the file and its extent, over a numbered
 * gutter. The reader reaches this tab to quote a line or to take the file
 * away, so the numbering and the download are what the panel is for. */
function AuthoredSource({ name, value }: { name: string; value: string }) {
  // A file ends with a newline, and splitting on it yields a trailing empty
  // element that is not a line. One trailing newline is dropped so the count
  // and the gutter agree with what an editor reports.
  const lines = value.replace(/\n$/, '').split('\n');
  const bytes = new TextEncoder().encode(value).length;
  return (
    <section className="source-pane">
      <div className="source-actions">
        <span className="source-lede">
          The authored file, byte for byte. The Rendered tab shows the parsed body.
        </span>
        <CopyButton value={value} />
        <button
          type="button"
          onClick={() => {
            downloadFile(inlineHref(value, false), name);
          }}
        >
          Download {name}
        </button>
      </div>
      <div className="source-block">
        <div className="source-head mono">
          <span>{name}</span>
          <span className="quiet">
            {lines.length} lines · {formatSize(bytes)}
          </span>
        </div>
        <div className="source-lines">
          {/* The gutter is decorative for a reader who is listening rather
              than looking: it repeats no content, and a screen reader that
              read it would interleave numbers with the file's own text. */}
          <div className="source-gutter mono" aria-hidden="true">
            {lines.map((_, index) => (
              <div key={index}>{index + 1}</div>
            ))}
          </div>
          {/* The joined lines rather than the value, because a trailing
              newline draws a line the gutter does not number and pulls the
              two columns out of register. Copy and Download carry the
              value itself. */}
          <pre className="mono source-code">{lines.join('\n')}</pre>
        </div>
      </div>
    </section>
  );
}

async function fetchText(link: LargeResourceLink): Promise<string> {
  const response = await fetch(link.presigned_url);
  if (!response.ok) {
    throw new Error(`the manifest body fetch answered ${response.status}`);
  }
  return response.text();
}

/** ArtifactRail carries what came with the registry's response: where the
 * artifact came from, its frontmatter, what depends on it, and what it
 * bundles. Each section has an absent state, and the frontmatter section is
 * the exception the design fixes: where the response yields no pairs the
 * section drops its header along with its table, so the rail reads as
 * provenance followed directly by relations. */
function ArtifactRail({ artifact, frontmatter }: { artifact: LoadArtifactResponse; frontmatter: string }) {
  const pairs = parseFrontmatter(frontmatter);
  const hasFrontmatter = pairs.error !== '' || pairs.properties.length > 0;
  const resources = resourceRows(artifact);
  return (
    <aside className="artifact-rail" aria-label="Artifact details">
      <section aria-label="Provenance">
        <p className="label">Provenance</p>
        {/* Provenance is a property table like the frontmatter below it, so
            the two read as one column of labelled values rather than a
            sentence fragment followed by a bare string. The hash is
            abbreviated because it is 71 characters against a rail that is
            far narrower, and the full value stays on the row's title so it
            is still recoverable. */}
        <table className="data-table rail-properties" data-testid="rail-provenance-table">
          <tbody>
            <tr>
              <th scope="row" className="mono">
                layer
              </th>
              <td>{layerName(artifact)}</td>
            </tr>
            <tr>
              <th scope="row" className="mono">
                hash
              </th>
              <td className="mono" title={artifact.content_hash}>
                {abbreviateHash(artifact.content_hash)}
              </td>
            </tr>
          </tbody>
        </table>
      </section>
      {hasFrontmatter && (
        <section aria-label="Frontmatter">
          <p className="label">Frontmatter</p>
          <PropertyTable raw={frontmatter} testID="rail-frontmatter-table" />
        </section>
      )}
      <Relations artifact={artifact} frontmatter={frontmatter} />
      <section aria-label="Bundled resources">
        <p className="label">Resources</p>
        {resources.length === 0 ? (
          <EmptyState>This artifact bundles no files.</EmptyState>
        ) : (
          <>
            {/* The rail splits the two deliveries, because a file that
                arrived with the response and one that is fetched on demand
                cost the reader different things to open. The tab keeps them
                as one list under a delivery column. */}
            <RailResourceGroup
              label="Inline"
              rows={resources.filter((row) => row.delivery !== fetchedDelivery)}
              absent="No file arrived with the response."
            />
            <RailResourceGroup
              label="Fetched on demand"
              rows={resources.filter((row) => row.delivery === fetchedDelivery)}
              absent="No file is fetched on demand."
            />
          </>
        )}
      </section>
    </aside>
  );
}

/** RailResourceGroup is one delivery's files in the rail. An empty group
 * states its absence rather than disappearing, because the two groups
 * together are what tell the reader how this artifact's files arrive. */
function RailResourceGroup({ label, rows, absent }: { label: string; rows: ResourceRow[]; absent: string }) {
  return (
    <div className="rail-group">
      <p className="label quiet">{label}</p>
      {rows.length === 0 ? (
        <EmptyState>{absent}</EmptyState>
      ) : (
        <ul className="rail-list">
          {rows.map((row) => (
            <li key={row.name} className="mono">
              {row.name}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

/** layerName is the layer the artifact was served from. The field is absent
 * on a response that reports none, and the rail states that rather than
 * standing an empty value in the section. */
function layerName(artifact: LoadArtifactResponse): string {
  return artifact.layer === undefined || artifact.layer === '' ? 'unreported' : artifact.layer;
}

/** abbreviateHash keeps a content hash to the width of the rail. The
 * algorithm prefix identifies what was hashed and the ends of the digest are
 * what a reader compares against another copy, so both survive and the
 * middle is elided. A digest short enough to stand whole is left alone. */
function abbreviateHash(hash: string): string {
  const separator = hash.indexOf(':');
  const algorithm = separator === -1 ? '' : hash.slice(0, separator + 1);
  const digest = hash.slice(separator + 1);
  if (digest.length <= 12) {
    return hash;
  }
  return `${algorithm}${digest.slice(0, 4)}…${digest.slice(-4)}`;
}

/** inboundLabel names an edge from the perspective of the artifact it ends
 * at. Every edge the dependents endpoint serves arrives at this artifact
 * (§4.7.3: the reverse index holds the edges whose `to` is this artifact,
 * and `from` is the artifact that declared the relation), so the raw edge
 * kind beside the `from` link would state the relationship backwards: it
 * would read as this artifact extending the one that extends it. Each label
 * therefore reads in the passive direction, and an edge kind this UI does
 * not know still reads inbound rather than inverted. */
function inboundLabel(kind: string): string {
  switch (kind) {
    case 'extends':
      return 'extended by';
    case 'delegates_to':
      return 'delegated to by';
    case 'mcpServers':
      return 'referenced by';
    default:
      return `${kind} by`;
  }
}

/** RelationChip is one artifact named by a relation group: the reference as
 * the manifest or the edge states it, and the viewer route it opens. */
interface RelationChip {
  href: string;
  text: string;
}

/** RailRelationGroup draws one direction of the relation graph. The two
 * directions are separate groups because a single merged list leaves the
 * reader unable to tell whether this artifact extends the one named or is
 * extended by it, and each group states its own absence so a direction with
 * no members reads as empty rather than as missing. The count stands beside
 * the label once a group holds more than one member, where a single chip
 * already counts itself. */
function RailRelationGroup({ label, chips, absent }: { label: string; chips: RelationChip[]; absent: string }) {
  return (
    <div className="rail-group">
      <p className="label quiet">{chips.length > 1 ? `${label} · ${chips.length}` : label}</p>
      {chips.length === 0 ? (
        <EmptyState>{absent}</EmptyState>
      ) : (
        <ul className="relation-list">
          {chips.map((chip) => (
            <li key={chip.text} className="relation-chip">
              <a className="mono" href={chip.href}>
                {chip.text}
              </a>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

/** referenceID strips the version constraint from an `extends:` reference
 * (§4.4). The constraint can be a range rather than a stored version, so the
 * link opens the artifact the reference names and the chip keeps the
 * reference the author wrote. */
function referenceID(reference: string): string {
  const at = reference.indexOf('@');
  return at === -1 ? reference : reference.slice(0, at);
}

/** declaredExtends reads the artifact's own outbound `extends:` reference.
 * The dependents endpoint serves the reverse index alone (§4.7.3), so this
 * direction reaches the rail only from the manifest. A merged response
 * re-serializes the manifest with the hidden parent stripped (§4.6) and
 * carries the pre-merge document beside it, which is where the authored
 * reference survives. */
function declaredExtends(artifact: LoadArtifactResponse, frontmatter: string): string {
  const raw = artifact.raw_frontmatter ?? '';
  const source = raw === '' ? frontmatter : raw;
  const found = parseFrontmatter(source).properties.find((property) => property.key === 'extends');
  return found === undefined ? '' : found.value.trim();
}

/** inboundGroups splits the reverse-index edges into one group per relation,
 * in the order the registry served them. The extends group is always drawn,
 * because §13.10 puts the extending artifacts on this surface and a reader
 * has to be told when there are none; a relation nobody declared stands no
 * group of its own. */
function inboundGroups(edges: DependencyEdge[]): { label: string; chips: RelationChip[]; absent: string }[] {
  const groups = new Map<string, RelationChip[]>([['extended by', []]]);
  for (const edge of edges) {
    const label = inboundLabel(edge.kind);
    const chips = groups.get(label) ?? [];
    chips.push({ href: artifactHref(edge.from), text: edge.from });
    groups.set(label, chips);
  }
  return [...groups].map(([label, chips]) => ({
    label,
    chips,
    absent: label === 'extended by' ? 'Nothing extends this artifact.' : `Nothing is ${label} this artifact.`,
  }));
}

/** Relations lists the artifacts this one extends and the artifacts that
 * extend or otherwise depend on it. The reverse-index edges arrive on their
 * own request, so an artifact with no edges is a state of that group rather
 * than of the page, and the outbound group comes from the manifest already
 * in hand and renders while the request is in flight. */
function Relations({ artifact, frontmatter }: { artifact: LoadArtifactResponse; frontmatter: string }) {
  const edges = useAsync(() => dependentsOf(artifact.id), [artifact.id]);
  const declared = declaredExtends(artifact, frontmatter);
  const outbound = declared === '' ? [] : [{ href: artifactHref(referenceID(declared)), text: declared }];
  return (
    <section aria-label="Relations">
      <p className="label">Relations</p>
      <RailRelationGroup label="extends" chips={outbound} absent="This artifact extends nothing." />
      {edges.loading && <Loading label="Loading relations." />}
      {edges.error !== null && <ErrorState error={edges.error} onRetry={edges.reload} />}
      {edges.value !== null &&
        inboundGroups(edges.value).map((group) => (
          <RailRelationGroup key={group.label} label={group.label} chips={group.chips} absent={group.absent} />
        ))}
    </section>
  );
}

/** ResourceRow is one bundled file. The registry splits them one by one, so a
 * single artifact can carry inline files beside fetched ones, and they are
 * one list distinguished by the delivery column rather than two lists. Every
 * row is retrievable: nothing is previewed, so the row's own action is the
 * only path to the file. */
interface ResourceRow {
  name: string;
  format: string;
  delivery: string;
  size: number;
  href: string;
}

function resourceRows(artifact: LoadArtifactResponse): ResourceRow[] {
  const base64 = artifact.resources_base64 === true;
  const inline = Object.entries(artifact.resources ?? {}).map(([name, value]) => ({
    name,
    format: formatOf(name),
    delivery: base64 ? 'inline, base64' : 'inline',
    // One binary file puts the whole inline set into base64, so the size the
    // row states is the file's own byte count rather than the length of the
    // encoding it arrived in.
    size: base64 ? base64Bytes(value) : new TextEncoder().encode(value).length,
    href: inlineHref(value, base64),
  }));
  const fetched = Object.entries(artifact.large_resources ?? {}).map(([name, link]) => ({
    name,
    format: formatOf(name, link.content_type),
    delivery: fetchedDelivery,
    size: link.size,
    href: link.presigned_url,
  }));
  return [...inline, ...fetched];
}

/** inlineHref is what the row's download action retrieves for a file that
 * arrived in the response. The bytes are already in the page, so the action
 * points at a data URL carrying them: it needs no second request and, unlike
 * an object URL, no revocation once the reader leaves the row. */
function inlineHref(value: string, base64: boolean): string {
  if (base64) {
    return `data:application/octet-stream;base64,${value}`;
  }
  return `data:text/plain;charset=utf-8,${encodeURIComponent(value)}`;
}

/** base64Bytes is how many bytes a base64 string decodes to. It is derived
 * from the encoding's own arithmetic rather than by decoding, because the row
 * states a size and has no other use for the bytes. */
function base64Bytes(value: string): number {
  const encoded = value.replace(/[\s=]+$/, '');
  return Math.floor((encoded.length * 3) / 4);
}

/** formatOf is the row's format column. A fetched file carries the content
 * type the registry recorded; an inline one carries none, so the file's own
 * extension is what the row has to state. */
function formatOf(name: string, contentType?: string): string {
  if (contentType !== undefined && contentType !== '') {
    return contentType;
  }
  const dot = name.lastIndexOf('.');
  return dot <= 0 ? 'unknown' : name.slice(dot + 1);
}

/** ResourceTable lists every bundled file as one set distinguished by its
 * delivery column. Nothing is previewed, so the row's action is the only path
 * to the file, and the control above the table takes the whole set at once.
 * Selecting a row opens the detail card under the table. */
function ResourceTable({ rows }: { rows: ResourceRow[] }) {
  const [selected, setSelected] = useState('');
  const detail = rows.find((row) => row.name === selected) ?? null;
  const total = rows.reduce((sum, row) => sum + row.size, 0);
  return (
    <>
      <button
        type="button"
        className="download-all"
        data-testid="download-all"
        onClick={() => {
          for (const row of rows) {
            downloadFile(row.href, row.name);
          }
        }}
      >
        Download all ↓ {formatSize(total)}
      </button>
      <table className="data-table" aria-label="Resources">
        <thead>
          <tr>
            <th>File</th>
            <th>Format</th>
            <th>Size</th>
            <th>Delivery</th>
            <th>Action</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <tr
              key={row.name}
              className={row.name === selected ? 'row-selected' : ''}
              onClick={() => {
                setSelected(row.name);
              }}
            >
              <td className="mono">{row.name}</td>
              <td className="mono">{row.format}</td>
              <td className="mono">{row.size} bytes</td>
              <td>{row.delivery}</td>
              <td>
                <a href={row.href} download={row.name}>
                  Download
                </a>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {detail !== null && (
        <div className="resource-detail" data-testid="resource-detail">
          <p className="label">Selected</p>
          <p className="mono">{detail.name}</p>
          <p className="quiet mono">
            {detail.format} · {formatSize(detail.size)} · {detail.delivery}
          </p>
          <a className="button" href={detail.href} download={detail.name}>
            Download
          </a>
        </div>
      )}
    </>
  );
}

/** downloadFile retrieves one file without leaving the page. An inline file
 * is already in the document as a data URL and a fetched one is a presigned
 * URL, so both are taken by the same anchor. */
function downloadFile(href: string, name: string): void {
  const anchor = window.document.createElement('a');
  anchor.href = href;
  anchor.download = name;
  anchor.click();
}

/** formatSize states a byte count the way the control above the table does.
 * The unit is chosen from the count so a bundle of a few kilobytes and one of
 * a few hundred megabytes read the same way. */
export function formatSize(bytes: number): string {
  if (bytes < 1024) {
    return `${String(bytes)} B`;
  }
  if (bytes < 1024 * 1024) {
    return `${(bytes / 1024).toFixed(1)} KB`;
  }
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}
