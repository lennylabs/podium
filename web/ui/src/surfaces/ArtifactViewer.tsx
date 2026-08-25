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
import { Badge, CopyField, EmptyState, ErrorState, Loading } from '../components/primitives';
import { PropertyTable } from '../components/PropertyTable';
import { parseFrontmatter, splitDocument } from '../frontmatter';
import type { LargeResourceLink, LoadArtifactResponse } from '../api';
import { dependentsOf, loadArtifact } from '../api';
import { artifactHref } from '../route';
import { useAsync, useErrorReport } from '../useAsync';

type TabName = 'rendered' | 'frontmatter' | 'source' | 'resources';

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

  return (
    <section className="surface artifact-viewer" aria-label="Artifact viewer">
      <div className="artifact-content">
        <h1 className="mono">{body.id}</h1>
        <div className="artifact-meta">
          <Badge>{body.type}</Badge>
          <Badge tone="quiet">{body.version}</Badge>
          {body.sensitivity !== undefined && body.sensitivity !== '' && <Badge tone="quiet">{body.sensitivity}</Badge>}
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
        {open === 'frontmatter' && <PropertyTable raw={frontmatter} />}
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
        <p className="quiet">
          Layer <span className="mono">{layerName(artifact)}</span>
        </p>
        <p className="mono quiet">{artifact.content_hash}</p>
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
          <ul className="rail-list">
            {resources.map((row) => (
              <li key={row.name} className="mono">
                {row.name} <span className="quiet">{row.delivery}</span>
              </li>
            ))}
          </ul>
        )}
      </section>
    </aside>
  );
}

/** layerName is the layer the artifact was served from. The field is absent
 * on a response that reports none, and the rail states that rather than
 * standing an empty value in the section. */
function layerName(artifact: LoadArtifactResponse): string {
  return artifact.layer === undefined || artifact.layer === '' ? 'unreported' : artifact.layer;
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
                <span className="label quiet">{edge.kind}</span>{' '}
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
 * one list distinguished by the delivery column rather than two lists. */
interface ResourceRow {
  name: string;
  delivery: string;
  size: number;
  href: string;
}

function resourceRows(artifact: LoadArtifactResponse): ResourceRow[] {
  const inline = Object.entries(artifact.resources ?? {}).map(([name, value]) => ({
    name,
    delivery: artifact.resources_base64 === true ? 'inline, base64' : 'inline',
    size: value.length,
    href: '',
  }));
  const fetched = Object.entries(artifact.large_resources ?? {}).map(([name, link]) => ({
    name,
    delivery: 'fetched on demand',
    size: link.size,
    href: link.presigned_url,
  }));
  return [...inline, ...fetched];
}

function ResourceTable({ rows }: { rows: ResourceRow[] }) {
  return (
    <table className="data-table" aria-label="Resources">
      <thead>
        <tr>
          <th>File</th>
          <th>Delivery</th>
          <th>Size</th>
        </tr>
      </thead>
      <tbody>
        {rows.map((row) => (
          <tr key={row.name}>
            <td className="mono">{row.href === '' ? row.name : <a href={row.href}>{row.name}</a>}</td>
            <td>{row.delivery}</td>
            <td className="mono">{row.size} bytes</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
