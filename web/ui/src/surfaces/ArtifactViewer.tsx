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

import { useEffect, useMemo, useRef, useState } from 'react';

import type { RefObject } from 'react';

import { ArtifactBody } from '../components/ArtifactBody';
import { Breadcrumb } from '../components/Breadcrumb';
import { usePopupDismiss } from '../components/focus';
import { Lead } from '../components/Lead';
import type { BadgeTone } from '../components/primitives';
import {
  CopyButton,
  DeprecatedBadge,
  EmptyState,
  ErrorPage,
  ErrorState,
  Loading,
  SensitivityBadge,
  TabStrip,
  TypeBadge,
  formatVersion,
} from '../components/primitives';
import { PropertyTable } from '../components/PropertyTable';
import { parseFrontmatter, splitDocument } from '../frontmatter';
import { abbreviateHash } from '../hash';
import type { DependencyEdge, LargeResourceLink, LayerRecord, LoadArtifactResponse } from '../api';
import { dependentsOf, listLayers, loadArtifact } from '../api';
import { artifactHref } from '../route';
import { since } from '../time';
import { useAsync, useErrorReport } from '../useAsync';
import { ingestedRef, visibilitySummary } from './layerfacts';

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
  // The open tab lives here rather than in the tab set, because the rail
  // beside it reads it: the Frontmatter tab already stands the same pairs
  // full width in the content column, and the rail's own copy of them would
  // put the table on the screen twice.
  const [tab, setTab] = useState<TabName>('rendered');
  const [latest, setLatest] = useState('');
  // The response the page is standing on. A version the registry cannot
  // resolve is refused, and the picker that asked for it lives on this page,
  // so the last response that did resolve is held and keeps the page drawn
  // while the refusal is presented beside the control that caused it. Without
  // it the reader loses the picker along with the rest of the surface, and
  // the route still names this artifact, so nothing is left to recover with.
  const [held, setHeld] = useState<LoadArtifactResponse | null>(null);
  // The version the picker named belongs to the artifact it was named for. A
  // route change from one viewer to another reuses this component rather than
  // remounting it, so without this the next artifact is read at the previous
  // one's pinned version, the registry answers registry.not_found, and the
  // viewer reports an artifact that exists as missing. The pin and the latest
  // version it is compared against both start over, the way the held response
  // already does.
  const [viewed, setViewed] = useState(id);
  if (viewed !== id) {
    setViewed(id);
    setViewing('');
    setLatest('');
  }
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
  // The rail states who the artifact's layer is visible to and the run that
  // last ingested it, and load_artifact answers the layer's identifier alone,
  // so the layer list is read beside the artifact. Its outcome is not reported
  // to the shell: it is metadata standing beside the document rather than the
  // catalog read this surface is built on, so a refusal leaves those rows
  // unreported instead of replacing the artifact with a failure. Spec: §13.10.
  const layers = useAsync(listLayers, []);
  // The controls that return the reader to the latest version remove
  // themselves as they do it: the refusal banner and the older-version notice
  // both disappear once the pin is dropped, and the browser leaves focus on
  // the document body, so a keyboard reader is dumped at the top of the page.
  // The header's version control is what they were recovering from, it is on
  // the row the banner referred to, and it survives the read, so it takes the
  // focus back. The picker is keyed on the pin and remounts with it, which is
  // why the handover is an effect: the button the ref names is the one the
  // next render mounts.
  const versionTrigger = useRef<HTMLButtonElement>(null);
  const owedFocus = useRef(false);
  const showLatest = () => {
    owedFocus.current = true;
    setViewing('');
  };
  useEffect(() => {
    if (!owedFocus.current) {
      return;
    }
    owedFocus.current = false;
    versionTrigger.current?.focus();
  }, [viewing]);

  if (body === null) {
    if (artifact.error !== null) {
      // Nothing of this surface loaded, so the failure is the page rather
      // than a banner over a page that is still there.
      return (
        <ErrorPage
          error={artifact.error}
          title="No such artifact"
          subject={id}
          onRetry={artifact.reload}
          testID="artifact-failed"
        />
      );
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

  const description = descriptionOf(document.frontmatter, body.skill_raw ?? '');

  return (
    <section className="surface artifact-viewer" aria-label="Artifact viewer">
      <div className="artifact-content">
        <Breadcrumb path={domainOf(body.id)} current={artifactName(body.id)} />
        <div className="page-title">
          <h1>{artifactName(body.id)}</h1>
          <TypeBadge type={body.type} />
          <VersionPicker
            key={viewing}
            trigger={versionTrigger}
            current={body.version}
            viewing={viewing}
            onView={(version) => {
              setViewing(version);
            }}
          />
          <SensitivityBadge sensitivity={body.sensitivity} />
          <DeprecatedBadge deprecated={body.deprecated} />
        </div>
        <Lead text={description} />
        <DeprecationNotice artifact={body} />
        {artifact.loading && <Loading label="Loading the artifact." />}
        {artifact.error !== null && (
          <ErrorState
            error={artifact.error}
            title="The registry did not serve that version."
            testID="version-refused"
          >
            <p className="quiet">Still showing version {body.version}.</p>
            <button type="button" onClick={showLatest}>
              Show latest
            </button>
          </ErrorState>
        )}
        {older && (
          <div className="banner banner-accent version-notice" role="status" data-testid="older-version">
            <span className="version-notice-pin">Viewing {formatVersion(body.version)}</span>
            <span className="quiet">— not the latest.</span>
            <button type="button" className="version-notice-latest" onClick={showLatest}>
              Go to {formatVersion(latest)}
            </button>
          </div>
        )}
        {body.manifest_body_url !== undefined && fetched.loading && <Loading label="Fetching the artifact." />}
        {body.manifest_body_url !== undefined && fetched.error !== null && (
          <ErrorState error={fetched.error} onRetry={fetched.reload} />
        )}
        {(body.manifest_body_url === undefined || (!fetched.loading && fetched.error === null)) && (
          <Manifest artifact={body} document={document} tab={tab} onTab={setTab} />
        )}
      </div>
      <ArtifactRail
        artifact={body}
        layer={layerOf(layers.value, body)}
        frontmatter={document.frontmatter}
        showFrontmatter={tab !== 'frontmatter'}
      />
    </section>
  );
}

/** DeprecationNotice states the §4.7.4 lifecycle warning the registry serves
 * beside the artifact's bytes, and links the upgrade target the warning names.
 * The frontmatter table carries the same pair as text, which leaves the reader
 * to notice a row among a dozen and to retype the identifier it names, so the
 * notice stands under the header and the target is the link that opens it. An
 * artifact naming no replacement still gets the notice, because the state the
 * reader has to act on is the deprecation rather than the target. */
function DeprecationNotice({ artifact }: { artifact: LoadArtifactResponse }) {
  if (artifact.deprecated !== true) {
    return null;
  }
  const replacement = artifact.replaced_by ?? '';
  return (
    <div className="banner banner-accent" role="status" data-testid="deprecated-notice">
      <p className="banner-title">This artifact is deprecated.</p>
      {replacement === '' ? (
        <p className="quiet">The registry still serves it, and its manifest names no replacement.</p>
      ) : (
        <p className="quiet">
          The registry still serves it. Its replacement is{' '}
          <a className="mono" href={artifactHref(replacement)}>
            {replacement}
          </a>
          .
        </p>
      )}
    </div>
  );
}

/** artifactName is the page title: the last segment of the artifact's §4.2
 * path, which is what names the artifact itself. The domains above it are
 * the breadcrumb's, which is the one place the header states the path: a
 * second line spelling the whole identifier repeats the breadcrumb three
 * lines above it. */
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
 * it is read from the frontmatter the response carries. A skill omits the
 * field from its manifest and declares it in the authored SKILL.md instead
 * (§4.3.4), which the response carries separately, so that block is read
 * where the manifest declares none. An artifact declaring it in neither
 * yields nothing rather than a placeholder. */
function descriptionOf(frontmatter: string, skillRaw: string): string {
  const declared = declaredDescription(frontmatter);
  return declared === '' ? declaredDescription(skillRaw) : declared;
}

/** declaredDescription is the description a manifest document's frontmatter
 * block declares, or nothing where it declares none. */
function declaredDescription(document: string): string {
  const found = parseFrontmatter(document).properties.find((property) => property.key === 'description');
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
 * registry does not serve.
 *
 * The closed control is the version badge in the header. Standing the field
 * and its button on a row of their own put a form under every artifact's
 * description, including the artifacts that carry one published version,
 * which is the common case and the one that has nothing to pick. The entry
 * field is disclosed from the badge instead, so the header states the version
 * and the reader who wants another one asks for it.
 *
 * Spec: §13.10
 */
function VersionPicker({
  trigger,
  current,
  viewing,
  onView,
}: {
  trigger: RefObject<HTMLButtonElement | null>;
  current: string;
  viewing: string;
  onView: (version: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const [typed, setTyped] = useState(viewing);
  // The field is a transient popup, so it carries the dismissal paths the
  // other popups in this shell carry: Escape closes it and hands focus back to
  // the badge it was disclosed from, and a press or a focus move outside it
  // closes it. A reader who abandoned it on Escape was otherwise left on the
  // document body, at the top of the page, with the header row they were
  // reading gone from under them.
  const field = usePopupDismiss<HTMLSpanElement>(
    open,
    () => {
      setOpen(false);
    },
    trigger,
  );
  const view = () => {
    setOpen(false);
    onView(typed.trim());
  };
  const label = formatVersion(current);
  return (
    <span className="version-picker">
      <button
        type="button"
        ref={trigger}
        className="badge badge-soft version-picker-open"
        aria-expanded={open}
        aria-label={`Version ${label === '' ? 'unstated' : label}. Read another version.`}
        onClick={() => {
          setOpen(!open);
        }}
      >
        {label === '' ? 'version' : label}
        <span aria-hidden="true" className="version-picker-caret">
          ▾
        </span>
      </button>
      {open && (
        <span className="version-picker-field" ref={field}>
          <label className="label" htmlFor="version-picker-input">
            Version
          </label>
          <input
            id="version-picker-input"
            type="text"
            value={typed}
            placeholder="latest"
            autoFocus
            onChange={(event) => {
              setTyped(event.target.value);
            }}
            // A single-field entry control takes Enter as its commit, because a
            // reader who typed a version reaches for the return key before the
            // adjacent button. Escape is the disclosure's own dismissal and is
            // handled with the rest of them.
            onKeyDown={(event) => {
              if (event.key === 'Enter') {
                view();
              }
            }}
          />
          <button type="button" onClick={view}>
            View
          </button>
        </span>
      )}
    </span>
  );
}

/** Manifest is the content column's tabbed body: the manifest rendered as a
 * document, its frontmatter as a full-width property table, the authored
 * skill file where the artifact carries one, and the bundled files. A tab
 * whose artifact carries nothing is not drawn, so the set can shrink without
 * leaving a hole. */
function Manifest({
  artifact,
  document,
  tab,
  onTab,
}: {
  artifact: LoadArtifactResponse;
  document: ManifestHalves;
  tab: TabName;
  onTab: (tab: TabName) => void;
}) {
  const { body, frontmatter } = document;
  const skillRaw = artifact.skill_raw ?? '';
  // Both memos hold across a re-render that leaves the response alone, so the
  // rendered body is not re-sanitized every time a tab changes.
  const resources = useMemo(() => resourceRows(artifact), [artifact]);
  const resourceNames = useMemo(() => resources.map((row) => row.name), [resources]);
  // The frontmatter block is parsed here rather than inside the panel,
  // because the tab badge reports the parse failure and the tab is drawn
  // before the panel it opens.
  const invalid = parseFrontmatter(frontmatter).error !== '';
  const tabs: { name: TabName; label: string; badge: string; badgeTone?: BadgeTone }[] = [
    { name: 'rendered', label: 'Rendered', badge: '' },
    { name: 'frontmatter', label: 'Frontmatter', badge: invalid ? '!' : '', badgeTone: invalid ? 'danger' : 'quiet' },
    ...(skillRaw === '' ? [] : [{ name: 'source' as TabName, label: 'Authored source', badge: '' }]),
    ...(resources.length === 0
      ? []
      : [{ name: 'resources' as TabName, label: 'Resources', badge: String(resources.length) }]),
  ];
  // A tab can disappear between renders, which is what happens when the
  // manifest arrives by link and the authored source it carried is cleared.
  const open = tabs.some((entry) => entry.name === tab) ? tab : 'rendered';

  return (
    <TabStrip label="Artifact views" tabs={tabs} open={open} onOpen={onTab}>
      <>
        {open === 'rendered' && <ArtifactBody body={body} resources={resourceNames} />}
        {open === 'frontmatter' && <PropertyTable raw={frontmatter} offerRaw />}
        {open === 'source' && <AuthoredSource name="SKILL.md" value={skillRaw} />}
        {open === 'resources' && <ResourceTable rows={resources} />}
      </>
    </TabStrip>
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
 * the exception the design fixes: where the response yields no pairs, or
 * where the Frontmatter tab is already showing the same pairs full width in
 * the content column, the section drops its header along with its table, so
 * the rail reads as provenance followed directly by relations. */
function ArtifactRail({
  artifact,
  layer,
  frontmatter,
  showFrontmatter,
}: {
  artifact: LoadArtifactResponse;
  layer: LayerRecord | null;
  frontmatter: string;
  showFrontmatter: boolean;
}) {
  const pairs = parseFrontmatter(frontmatter);
  const hasFrontmatter = showFrontmatter && (pairs.error !== '' || pairs.properties.length > 0);
  const resources = resourceRows(artifact);
  return (
    <aside className="artifact-rail" aria-label="Artifact details">
      <section aria-label="Provenance">
        <p className="label">Provenance</p>
        {/* Provenance is a borderless label and value list, so the bordered
            containers below it carry the sections the reader opens and
            closes. Drawing it as a table like the frontmatter beneath makes
            the two sections read as the same kind of object and flattens the
            rail. The hash is abbreviated because it is 71 characters against
            a rail that is far narrower, and the full value stays on the
            row's title so it is still recoverable. */}
        <dl className="rail-facts" data-testid="rail-provenance">
          <div className="rail-fact">
            <dt className="mono">layer</dt>
            <dd>{layerName(artifact)}</dd>
          </div>
          <div className="rail-fact">
            <dt className="mono">visibility</dt>
            <dd>{layer === null ? 'unreported' : visibilitySummary(layer)}</dd>
          </div>
          <div className="rail-fact">
            <dt className="mono">ingested</dt>
            <dd title={layer?.last_ingested_at ?? undefined}>{ingestedLine(layer)}</dd>
          </div>
          <div className="rail-fact">
            <dt className="mono">hash</dt>
            <dd className="mono" title={artifact.content_hash}>
              {abbreviateHash(artifact.content_hash)}
            </dd>
          </div>
        </dl>
      </section>
      {hasFrontmatter && (
        <section aria-label="Frontmatter">
          <p className="label">Frontmatter</p>
          {/* The rail clips a scalar value, because the relation links stand
              under this table in the same scrolling column and a long
              description would otherwise push them far below the fold. */}
          <PropertyTable raw={frontmatter} testID="rail-frontmatter-table" clampValues />
        </section>
      )}
      <Relations artifact={artifact} frontmatter={frontmatter} />
      <section aria-label="Bundled resources">
        <p className="label">Resources</p>
        {resources.length === 0 ? (
          <EmptyState scope="inline">This artifact bundles no files.</EmptyState>
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
        <EmptyState scope="inline">{absent}</EmptyState>
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

/** layerOf is the record for the layer the artifact was served from. The
 * layer list is a second read, so it is absent while that read is in flight
 * and after one that failed, and a layer the caller cannot see is absent from
 * a list that did answer. Each case leaves the rows it feeds unreported. */
function layerOf(layers: LayerRecord[] | null, artifact: LoadArtifactResponse): LayerRecord | null {
  const id = artifact.layer ?? '';
  if (layers === null || id === '') {
    return null;
  }
  return layers.find((record) => record.ID === id) ?? null;
}

/** ingestedLine states when the artifact's layer was last ingested and the
 * reference the run landed on, which is the pair that says which revision of
 * the source the bytes above came from. The age is what the row displays and
 * the exact stamp stays on the row's title, the same discipline the layer
 * panel's ingest column and the abbreviated hash below follow. */
function ingestedLine(layer: LayerRecord | null): string {
  if (layer === null) {
    return 'unreported';
  }
  const at = layer.last_ingested_at ?? '';
  const age = at === '' ? 'never' : since(at, Date.now());
  const ref = ingestedRef(layer);
  return ref === '' ? age : `${age} · ${ref}`;
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
 * already counts itself. Each chip carries a leading dot toned by direction,
 * accent on the edge this artifact declares and meta on the edges that end
 * here, so the two directions stay apart once the group labels have scrolled
 * out of the reader's eye. */
function RailRelationGroup({
  label,
  chips,
  absent,
  direction,
}: {
  label: string;
  chips: RelationChip[];
  absent: string;
  direction: 'outbound' | 'inbound';
}) {
  return (
    <div className="rail-group">
      <p className="label quiet">{chips.length > 1 ? `${label} · ${chips.length}` : label}</p>
      {chips.length === 0 ? (
        <EmptyState scope="inline">{absent}</EmptyState>
      ) : (
        <ul className="relation-list">
          {chips.map((chip) => (
            <li key={chip.text} className="relation-chip">
              <span className={`relation-dot ${direction}`} aria-hidden="true" />
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
      <RailRelationGroup
        label="extends"
        chips={outbound}
        absent="This artifact extends nothing."
        direction="outbound"
      />
      {edges.loading && <Loading label="Loading relations." />}
      {edges.error !== null && <ErrorState error={edges.error} onRetry={edges.reload} />}
      {edges.value !== null &&
        inboundGroups(edges.value).map((group) => (
          <RailRelationGroup
            key={group.label}
            label={group.label}
            chips={group.chips}
            absent={group.absent}
            direction="inbound"
          />
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
            <th className="column-label">File</th>
            <th className="column-label">Format</th>
            <th className="column-label">Size</th>
            <th className="column-label">Delivery</th>
            <th className="column-label">Action</th>
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
              <td className="mono">{formatSize(row.size)}</td>
              <td>
                <span className="badge badge-quiet">{row.delivery}</span>
              </td>
              <td>
                <a className="button" href={row.href} download={row.name}>
                  Download ↓
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
