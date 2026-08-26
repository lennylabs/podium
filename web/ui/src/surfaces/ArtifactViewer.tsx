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

import { ArtifactBody } from '../components/ArtifactBody';
import { Breadcrumb } from '../components/Breadcrumb';
import { Badge, CopyField, EmptyState, ErrorState, Loading } from '../components/primitives';
import { PropertyTable } from '../components/PropertyTable';
import { parseFrontmatter, splitDocument } from '../frontmatter';
import type { LargeResourceLink, LoadArtifactResponse } from '../api';
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
  const artifact = useAsync(() => loadArtifact(id, viewing === '' ? undefined : viewing), [id, viewing]);
  useErrorReport(artifact.error, onError);
  // A manifest above the inline cutoff arrives as a presigned URL with the
  // inline body empty, so the document is fetched here and both columns read
  // the result: the fetch's own loading and failure states are confined to
  // the content column, because the rail's metadata came with the registry's
  // own response and is already there.
  const link = artifact.value?.manifest_body_url;
  const fetched = useAsync(async () => (link === undefined ? '' : fetchText(link)), [link?.presigned_url ?? '']);

  if (artifact.loading) {
    return <Loading label="Loading the artifact." />;
  }
  if (artifact.error !== null) {
    return <ErrorState error={artifact.error} onRetry={artifact.reload} />;
  }
  const body = artifact.value;
  if (body === null) {
    return null;
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
          <Badge>{body.type}</Badge>
          <Badge tone="quiet">{body.version}</Badge>
          {body.sensitivity !== undefined && body.sensitivity !== '' && <Badge tone="quiet">{body.sensitivity}</Badge>}
        </div>
        <p className="mono quiet artifact-id-line">{body.id}</p>
        {description !== '' && <p className="lead">{description}</p>}
        <div className="artifact-meta">
          <VersionPicker
            viewing={viewing}
            onView={(version) => {
              setViewing(version);
            }}
          />
        </div>
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

  return (
    <>
      <div className="tabs" role="tablist" aria-label="Artifact views">
        {tabs.map((entry) => (
          <button
            key={entry.name}
            type="button"
            role="tab"
            id={`tab-${entry.name}`}
            aria-selected={open === entry.name}
            aria-controls={`panel-${entry.name}`}
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
        {open === 'source' && <CopyField label="SKILL.md" value={skillRaw} block />}
        {open === 'resources' && <ResourceTable rows={resources} />}
      </div>
    </>
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
      <Relations id={artifact.id} />
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

/** Relations lists the artifacts that extend or otherwise depend on this
 * one. The edges arrive on their own request, so an artifact with no edges
 * is a state of this section rather than of the page. */
function Relations({ id }: { id: string }) {
  const edges = useAsync(() => dependentsOf(id), [id]);
  return (
    <section aria-label="Relations">
      <p className="label">Relations</p>
      {edges.loading && <Loading label="Loading relations." />}
      {edges.error !== null && <ErrorState error={edges.error} onRetry={edges.reload} />}
      {edges.value !== null &&
        (edges.value.length === 0 ? (
          <EmptyState>Nothing extends or depends on this artifact.</EmptyState>
        ) : (
          <ul className="relation-list">
            {edges.value.map((edge) => (
              <li key={edge.kind + edge.from}>
                <span className="label quiet">{inboundLabel(edge.kind)}</span>{' '}
                <a className="mono" href={artifactHref(edge.from)}>
                  {edge.from}
                </a>
              </li>
            ))}
          </ul>
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
