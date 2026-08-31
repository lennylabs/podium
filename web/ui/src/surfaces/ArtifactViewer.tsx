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

import { Fragment, useEffect, useId, useMemo, useRef, useState } from 'react';

import type { ReactNode, RefObject } from 'react';

import { ArtifactBody } from '../components/ArtifactBody';
import { Breadcrumb } from '../components/Breadcrumb';
import { CodeBlock, codeLines } from '../components/CodeBlock';
import { usePopupDismiss } from '../components/focus';
import { Lead } from '../components/Lead';
import type { TabCountTone } from '../components/primitives';
import {
  CopyButton,
  DeprecatedBadge,
  EmptyState,
  ErrorPage,
  ErrorState,
  Loading,
  TabStrip,
  TypeBadge,
  formatVersion,
} from '../components/primitives';
import { PropertyTable } from '../components/PropertyTable';
import { parseFrontmatter, splitDocument } from '../frontmatter';
import { splitHash } from '../hash';
import type { DependencyEdge, LargeResourceLink, LayerRecord, LoadArtifactResponse } from '../api';
import { catalogArtifactIDs, dependentsOf, listLayers, loadArtifact } from '../api';
import { artifactHref } from '../route';
import { since } from '../time';
import { useAsync, useErrorReport } from '../useAsync';
import { ingestedRef, visibilitySummary } from './layerfacts';

type TabName = 'rendered' | 'frontmatter' | 'source' | 'resources';

/** fetchedDelivery is the delivery a file retrieved from object storage
 * carries. The rail groups on it and the resource table states it in its own
 * column. */
const fetchedDelivery = 'fetched on demand';

export function ArtifactViewer({
  id,
  viewing,
  onError,
}: {
  id: string;
  /** viewing is the version the route pins the read to. An empty version is
   * the default read, which the registry answers with the latest version. The
   * picker navigates to a pinned address rather than writing state here, so
   * the older version can be linked and the reader's back step returns to the
   * version they came from. The notice keys on the pair rather than on the
   * pin alone, because a reader who asks for the version the registry already
   * served is looking at the latest one. */
  viewing: string;
  onError: (err: unknown) => void;
}) {
  // The open tab lives here rather than in the tab set, because the header
  // above it and the rail beside it both read it: the Frontmatter tab already
  // stands the same pairs full width in the content column, so the rail's own
  // copy of them and the header's description paragraph would each put a value
  // the table already carries on the screen twice.
  const [tab, setTab] = useState<TabName>('rendered');
  const [latest, setLatest] = useState('');
  // The response the page is standing on. A version the registry cannot
  // resolve is refused, and the picker that asked for it lives on this page,
  // so the last response that did resolve is held and keeps the page drawn
  // while the refusal is presented beside the control that caused it. Without
  // it the reader loses the picker along with the rest of the surface, and
  // the route still names this artifact, so nothing is left to recover with.
  const [held, setHeld] = useState<LoadArtifactResponse | null>(null);
  // The latest version belongs to the artifact it was read for. A route
  // change from one viewer to another reuses this component rather than
  // remounting it, so without this the next artifact is compared against the
  // previous one's latest version and its own is marked as an older one. The
  // remembered version starts over, the way the held response already does.
  const [viewed, setViewed] = useState(id);
  // The rail states who the artifact's layer is visible to and the run that
  // last ingested it, and load_artifact answers the layer's identifier alone,
  // so the layer list is read beside the artifact. Its outcome is not reported
  // to the shell: it is metadata standing beside the document rather than the
  // catalog read this surface is built on, so a refusal leaves those rows
  // unreported instead of replacing the artifact with a failure. Spec: §13.10.
  const layers = useAsync(listLayers, []);
  if (viewed !== id) {
    setViewed(id);
    setLatest('');
    // The open tab is not in the address, and this component survives the
    // route change from one artifact to the next, so a tab that carried over
    // would make the same artifact address open on a different panel
    // depending on how the reader arrived, and a reload of that address would
    // switch the panel back. Every artifact opens on the body §13.10 names
    // first. Spec: §13.10.
    setTab('rendered');
    // A layer list that was refused leaves the rail's visibility and ingested
    // rows unreported, and this component survives the route change from one
    // artifact to the next, so without re-issuing it here every later
    // artifact inherits a momentary outage for the rest of the session.
    if (layers.error !== null) {
      layers.reload();
    }
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
  // A pinned address is reachable from a pasted link, a bookmark, and a step
  // back, so the reader can arrive on an older version without having been on
  // the latest one. The latest version is then read beside the pinned
  // document, because the notice that marks the document as an older one and
  // the control that leads back both name it. A reader who arrived from the
  // latest version already has it remembered, and no second read is issued.
  const unknownLatest = viewing !== '' && latest === '';
  const latestRead = useAsync(async () => (unknownLatest ? (await loadArtifact(id)).version : ''), [id, unknownLatest]);
  if (latestRead.value !== null && latestRead.value !== '' && latest === '') {
    setLatest(latestRead.value);
  }
  // Every control that reads another version removes itself as it does so:
  // the picker's field is unmounted when it commits a pin, and the refusal
  // banner and the older-version notice both disappear once the pin is
  // dropped. The browser leaves focus on the document body each time, so a
  // keyboard reader is dumped at the top of the page. The header's version
  // control is the one they were operating, it is on the row those banners
  // refer to, and it survives the read, so it takes the focus back. The
  // handover is an effect following a counter rather than the pin itself,
  // because a reader can ask for the version already on screen, and
  // navigating to the address the window is already on renders nothing for
  // the handover to follow.
  const versionTrigger = useRef<HTMLButtonElement>(null);
  const [owedFocus, setOwedFocus] = useState(0);
  const returnFocus = () => {
    setOwedFocus((owed) => owed + 1);
  };
  // Reading another version is a navigation, so it goes through the address
  // and lands in the history stack: the reader's back step returns to the
  // version they came from, and the address they copy while an older version
  // is on screen opens that version.
  const readVersion = (version: string) => {
    returnFocus();
    window.location.hash = artifactHref(id, version);
  };
  const showLatest = () => {
    readVersion('');
  };
  useEffect(() => {
    if (owedFocus === 0) {
      return;
    }
    versionTrigger.current?.focus();
  }, [owedFocus]);

  // The rail's layer read is a second read this surface makes, and the outage
  // that refused it is the one the reader is retrying, so the control that
  // recovers the document re-issues it too. Retrying the document alone
  // returns a page whose provenance rows stay unreported.
  const retry = () => {
    artifact.reload();
    if (layers.error !== null) {
      layers.reload();
    }
  };

  if (body === null) {
    if (artifact.error !== null) {
      // Nothing of this surface loaded, so the failure is the page rather
      // than a banner over a page that is still there.
      //
      // A pinned address arrives cold from a link, a bookmark, or a reload of
      // the address the picker wrote after a pin the registry refused, and
      // there the failure is the pin rather than the artifact: the catalog
      // still holds the artifact at the versions it does have. The registry
      // reports the two conditions apart, so the page does too, names the pin
      // that failed, and offers the unpinned address the in-session refusal
      // already offers. Reporting the artifact absent because one pin missed
      // states something false about the catalog. Spec: §13.10.
      const pinned = viewing !== '';
      return (
        <ErrorPage
          error={artifact.error}
          title={pinned ? 'No such version' : 'No such artifact'}
          subject={pinned ? `${id}@${viewing}` : id}
          onRetry={retry}
          testID="artifact-failed"
        >
          {pinned && (
            <a className="button primary" href={artifactHref(id)} data-testid="pin-show-latest">
              Show latest
            </a>
          )}
        </ErrorPage>
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
  const frontmatterOpen = tab === 'frontmatter';
  // Where the manifest frontmatter declares the description, the Frontmatter
  // tab stands it as the `description` row of its own table, and keeping the
  // header paragraph there would print the same sentence twice within one
  // screen, so the header drops it. A skill declares the field in the
  // authored SKILL.md instead and its manifest must not carry it (§4.3.4),
  // so that table stands nothing in the header's place and dropping the
  // paragraph would take the description off the page entirely. The header
  // keeps its paragraph wherever the table has no row to relocate it into.
  //
  // Spec: §13.10
  const tableStatesDescription = declaredDescription(document.frontmatter) !== '';

  return (
    <section className="surface artifact-viewer" aria-label="Artifact viewer">
      <div className="artifact-content">
        <Breadcrumb path={domainOf(body.id)} current={artifactName(body.id)} />
        {/* The header states the artifact's identity and the version being
            read. It carries no sensitivity badge: the registry stamps a
            classification on every artifact, so a badge here stands on every
            page, states what the rail's frontmatter table states a screen
            away, and pushes the version picker onto a second row under a long
            identifier. A search result keeps the badge, where a classification
            distinguishes one row from the rest. */}
        <div className="page-title">
          <h1>{artifactName(body.id)}</h1>
          <TypeBadge type={body.type} />
          <VersionPicker
            trigger={versionTrigger}
            current={body.version}
            viewing={viewing}
            onView={readVersion}
          />
          <DeprecatedBadge deprecated={body.deprecated} />
        </div>
        {!(frontmatterOpen && tableStatesDescription) && <Lead text={description} />}
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
        showFrontmatter={!frontmatterOpen}
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
  // aria-expanded alone states that something opened without stating what or
  // where, and the popover it discloses stands several elements away in the
  // document. The popover therefore takes a generated id the badge points
  // aria-controls at, and the badge names the kind it opens. The popover is a
  // labelled entry field with its own submit, which takes focus on opening,
  // returns it on Escape, and closes on a press outside it, so it is a
  // non-modal dialog and says so on both ends.
  const fieldId = useId();
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
  const label = formatVersion(current);
  return (
    <span className="version-picker">
      <button
        type="button"
        ref={trigger}
        className="badge version-picker-open"
        aria-expanded={open}
        aria-haspopup="dialog"
        aria-controls={fieldId}
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
        <VersionField
          field={field}
          fieldId={fieldId}
          viewing={viewing}
          onView={(version) => {
            setOpen(false);
            onView(version);
          }}
        />
      )}
    </span>
  );
}

/** VersionField is one opening of the picker's entry field. It is mounted
 * only while the popover is open, so the string the reader last submitted is
 * discarded with it rather than reappearing with the caret at its end. A pin
 * the registry refused stays the version the page asked for, so the field
 * still opens on it, and the seeded text is selected on mount: the next
 * keystroke replaces the refused pin instead of extending it into a longer
 * one the registry refuses again.
 *
 * Spec: §13.10
 */
function VersionField({
  field,
  fieldId,
  viewing,
  onView,
}: {
  field: RefObject<HTMLSpanElement | null>;
  /** fieldId is the id the badge points aria-controls at. */
  fieldId: string;
  viewing: string;
  onView: (version: string) => void;
}) {
  const [typed, setTyped] = useState(viewing);
  const view = () => {
    onView(typed.trim());
  };
  // Selecting on mount rather than from an inline ref callback: React
  // reattaches an inline callback on every render, which would reselect the
  // whole field after each keystroke.
  const input = useRef<HTMLInputElement>(null);
  useEffect(() => {
    input.current?.select();
  }, []);
  return (
    <span className="version-picker-field" id={fieldId} role="dialog" aria-label="Read another version" ref={field}>
      <label className="label" htmlFor="version-picker-input">
        Version
      </label>
      <input
        id="version-picker-input"
        type="text"
        value={typed}
        placeholder="latest"
        ref={input}
        autoFocus
        onChange={(event) => {
          setTyped(event.target.value);
        }}
        // A single-field entry control takes Enter as its commit, because a
        // reader who typed a version reaches for the return key before the
        // adjacent button. Escape is the disclosure's own dismissal and is
        // handled with the rest of them. The commit is the key's whole
        // meaning here, so its default action is refused: the commit hands
        // the focus back to the badge while the press is still being
        // processed, and the browser would otherwise carry the same Enter on
        // to that button and disclose the field again.
        onKeyDown={(event) => {
          if (event.key === 'Enter') {
            event.preventDefault();
            view();
          }
        }}
      />
      <button type="button" onClick={view}>
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
  // The selected file lives here rather than in the table, because a §4.4
  // prose reference in the body opens the Resources tab on the file it names
  // and the table is mounted by that press.
  const [selected, setSelected] = useState('');
  const tabs: { name: TabName; label: string; count: string; countTone?: TabCountTone }[] = [
    { name: 'rendered', label: 'Rendered', count: '' },
    { name: 'frontmatter', label: 'Frontmatter', count: invalid ? '!' : '', countTone: invalid ? 'danger' : 'quiet' },
    ...(skillRaw === '' ? [] : [{ name: 'source' as TabName, label: 'Authored source', count: '' }]),
    ...(resources.length === 0
      ? []
      : [{ name: 'resources' as TabName, label: 'Resources', count: String(resources.length) }]),
  ];
  // A tab can disappear between renders, which is what happens when the
  // manifest arrives by link and the authored source it carried is cleared.
  const open = tabs.some((entry) => entry.name === tab) ? tab : 'rendered';

  return (
    <TabStrip label="Artifact views" tabs={tabs} open={open} onOpen={onTab}>
      <>
        {open === 'rendered' && (
          <ArtifactBody
            body={body}
            resources={resourceNames}
            onResource={(name) => {
              setSelected(name);
              onTab('resources');
            }}
          />
        )}
        {open === 'frontmatter' && <PropertyTable raw={frontmatter} offerRaw />}
        {open === 'source' && <AuthoredSource name="SKILL.md" value={skillRaw} />}
        {open === 'resources' && <ResourceTable rows={resources} selected={selected} onSelect={setSelected} />}
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
  const lines = codeLines(value);
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
      {/* The split lines rather than the value, because a trailing newline
          draws a line the gutter does not number and pulls the two columns
          out of register. Copy and Download carry the value itself. */}
      <CodeBlock name={name} extra={formatSize(bytes)} lines={lines} />
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
            rail. The hash is 71 characters against a rail that is far
            narrower, so the row clips it the way the layer table's source
            column clips a path: the whole digest stays in the document and
            the elision is the container's, so selecting the row, copying it,
            or hearing it read out yields the digest a reader checks against
            a build rather than the ends of it (§13.10). */}
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
            <dd className="mono rail-hash" title={artifact.content_hash}>
              <ContentHash hash={artifact.content_hash} />
              {/* The runs are laid out as separate boxes, so a selection
                  across them carries a line break between each pair and a
                  digest pasted into a comparison does not match. The control
                  takes the value itself, on the terms every other value a
                  reader has to take away with them is copied on. */}
              <CopyButton value={artifact.content_hash} subject="Content hash" />
            </dd>
          </div>
        </dl>
      </section>
      {hasFrontmatter && (
        <section aria-label="Frontmatter">
          <p className="label">Frontmatter</p>
          {/* The rail clips a scalar value, because the relation links stand
              under this table in the same scrolling column and a long
              description otherwise pushes them thousands of pixels below the
              fold. The Frontmatter panel's line about a value wrapping is
              scoped to that panel, which carries no links under it (§13.10). */}
          <PropertyTable raw={frontmatter} testID="rail-frontmatter-table" clampValues />
        </section>
      )}
      <Relations artifact={artifact} frontmatter={frontmatter} />
      <section aria-label="Bundled resources">
        {/* The count stands on the header's far edge, so the extent of the
            section is read off the header rather than by counting rows. */}
        <div className="rail-section-head">
          <p className="label">Resources</p>
          {resources.length > 0 && (
            <p className="label rail-section-count" data-testid="rail-resource-count">
              {resources.length}
            </p>
          )}
        </div>
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
              fetched={false}
            />
            <RailResourceGroup
              label="Fetched on demand"
              rows={resources.filter((row) => row.delivery === fetchedDelivery)}
              absent="No file is fetched on demand."
              fetched
            />
          </>
        )}
      </section>
    </aside>
  );
}

/** ContentHash draws a §6.4 content hash as the three runs the rail elides it
 * through: the lead, which names the algorithm and opens the digest, the
 * middle, which the rail clips, and the last digest characters, which stay
 * drawn because they are the other end a reader compares against another copy.
 * The runs together are the whole hash, so a reader who has to check the
 * digest gets it out of the page by selecting or copying the row. The clipped
 * run isolates its own text, so the right-to-left direction that moves its
 * ellipsis against the lead does not reorder the characters within it. */
function ContentHash({ hash }: { hash: string }) {
  const runs = splitHash(hash);
  return (
    <>
      <span className="rail-hash-lead">
        <bdi>{runs.lead}</bdi>
      </span>
      <span className="rail-hash-middle">
        <bdi>{runs.middle}</bdi>
      </span>
      <span className="rail-hash-tail">
        <bdi>{runs.tail}</bdi>
      </span>
    </>
  );
}

/** RailResourceGroup is one delivery's files in the rail. An empty group
 * states its absence rather than disappearing, because the two groups
 * together are what tell the reader how this artifact's files arrive. Each
 * file stands on its own bordered row carrying its size, the figure that
 * says what opening it costs, and a file fetched on demand carries the
 * retrieval action on that figure because the bytes are not in the page. */
function RailResourceGroup({
  label,
  rows,
  absent,
  fetched,
}: {
  label: string;
  rows: ResourceRow[];
  absent: string;
  fetched: boolean;
}) {
  return (
    <div className="rail-group">
      <p className="label quiet">{label}</p>
      {rows.length === 0 ? (
        <EmptyState scope="inline">{absent}</EmptyState>
      ) : (
        <ul className="rail-list">
          {rows.map((row) => (
            <li key={row.name} className={fetched ? 'resource-chip fetched' : 'resource-chip'}>
              <ResourcePath name={row.name} />
              {fetched ? (
                <a
                  className="mono quiet resource-size"
                  href={row.href}
                  download={row.name}
                  aria-label={`Download ${row.name}`}
                >
                  {formatSize(row.size)} ↓
                </a>
              ) : (
                <span className="mono quiet resource-size">{formatSize(row.size)}</span>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

/** ResourcePath is a bundled file's path in the rail. The rail is narrower
 * than a nested path, so the path is handed to the browser one segment at a
 * time with a break opportunity between them and none inside them: a path
 * that does not fit wraps at a directory boundary and each name stays whole.
 * Left to break wherever it fits, `resources/attributes.json` ends its first
 * line as `resources/attributes.js` and `good-trace.json` splits at its
 * hyphen, both of which read as files that do not exist. */
function ResourcePath({ name }: { name: string }) {
  const segments = name.split('/');
  return (
    <span className="mono">
      {segments.map((segment, index) => (
        // The path is fixed for the row, so the position is the only key a
        // repeated segment can take.
        <Fragment key={index}>
          <span className="resource-segment">{index === 0 ? segment : `/${segment}`}</span>
          {index < segments.length - 1 ? <wbr /> : null}
        </Fragment>
      ))}
    </span>
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
/** RailGroup is one labelled band of the relations rail. It carries the label
 * so a band that reports a state other than its members, such as a read that
 * failed, still names the relation it stands for. */
function RailGroup({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="rail-group">
      <p className="label quiet">{label}</p>
      {children}
    </div>
  );
}

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
    <RailGroup label={chips.length > 1 ? `${label} · ${chips.length}` : label}>
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
    </RailGroup>
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

/** declaredExtends reads the reference the artifact's author wrote, which is
 * a candidate for the rail rather than something the rail may draw. The
 * dependents endpoint serves the reverse index alone (§4.7.3), so this
 * direction reaches the rail only from the manifest, and the registry
 * re-serializes every extends manifest with the parent stripped (§4.6) and
 * carries the pre-merge document beside it for the content-hash check, so
 * the authored reference survives only there. Whether the reader may be told
 * the parent exists is settled by visibleExtends. */
function declaredExtends(artifact: LoadArtifactResponse, frontmatter: string): string {
  const raw = artifact.raw_frontmatter ?? '';
  const source = raw === '' ? frontmatter : raw;
  const found = parseFrontmatter(source).properties.find((property) => property.key === 'extends');
  return found === undefined ? '' : found.value.trim();
}

/** parentScope is the domain a parent artifact ID sits in, which is the scope
 * the catalog read is taken over. A top-level ID sits at the root, whose
 * scope is the empty string. */
function parentScope(id: string): string {
  const cut = id.lastIndexOf('/');
  return cut === -1 ? '' : id.slice(0, cut);
}

/** visibleExtends resolves the authored reference into the chips the rail may
 * draw. §4.6 merges a parent whose layer the caller cannot see and holds that
 * "the parent's existence and ID are not surfaced to the requester", so a
 * concealed parent has to read on this surface exactly as an artifact that
 * extends nothing does. The served frontmatter cannot settle it either way,
 * because the registry strips the parent from every extends response whether
 * or not the caller can see it, and the pre-merge document beside it is a
 * disclosure that does not license the rail to republish the ID.
 *
 * The §4.5.2 catalog read answers with the IDs the caller can see under a
 * scope, so a parent it lists is one the caller could have opened on its own
 * and naming it discloses nothing, and a parent it omits leaves the group
 * indistinguishable from one nobody declared. A catalog read that fails
 * resolves to no chip, because a concealment rule that cannot be evaluated
 * denies. */
async function visibleExtends(declared: string): Promise<RelationChip[]> {
  if (declared === '') {
    return [];
  }
  const id = referenceID(declared);
  const visible = await catalogArtifactIDs(parentScope(id));
  return visible.includes(id) ? [{ href: artifactHref(id), text: declared }] : [];
}

/** inboundGroups splits the reverse-index edges into one group per relation,
 * in the order the registry served them. The extends group is always drawn,
 * because §13.10 puts the extending artifacts on this surface and a reader
 * has to be told when there are none; a relation nobody declared stands no
 * group of its own. */
const extendedBy = 'extended by';

function inboundGroups(edges: DependencyEdge[]): { label: string; chips: RelationChip[]; absent: string }[] {
  const groups = new Map<string, RelationChip[]>([[extendedBy, []]]);
  for (const edge of edges) {
    const label = inboundLabel(edge.kind);
    const chips = groups.get(label) ?? [];
    chips.push({ href: artifactHref(edge.from), text: edge.from });
    groups.set(label, chips);
  }
  return [...groups].map(([label, chips]) => ({
    label,
    chips,
    absent: label === extendedBy ? 'Nothing extends this artifact.' : `Nothing is ${label} this artifact.`,
  }));
}

/** Relations lists the artifacts this one extends and the artifacts that
 * extend or otherwise depend on it. The reverse-index edges arrive on their
 * own request, so an artifact with no edges is a state of that group rather
 * than of the page. The outbound direction takes a read of its own as well,
 * because the reference the manifest carries names a parent the caller may
 * not be allowed to know exists (§4.6). An artifact that declares no parent
 * settles without waiting on anything. */
function Relations({ artifact, frontmatter }: { artifact: LoadArtifactResponse; frontmatter: string }) {
  const edges = useAsync(() => dependentsOf(artifact.id), [artifact.id]);
  const declared = declaredExtends(artifact, frontmatter);
  const parent = useAsync(() => visibleExtends(declared), [artifact.id, declared]);
  return (
    <section aria-label="Relations">
      <p className="label">Relations</p>
      {declared !== '' && parent.loading ? (
        <Loading label="Loading relations." />
      ) : (
        <RailRelationGroup
          label="extends"
          chips={parent.value ?? []}
          absent="This artifact extends nothing."
          direction="outbound"
        />
      )}
      {edges.loading && <Loading label="Loading relations." />}
      {/* A failed reverse-index read keeps the group heading the served
          groups would have carried, for the same reason inboundGroups always
          draws the extends group: the reader has to be told which relation
          went unreported, and a bare failure band between the outbound group
          and the rest of the rail names none. */}
      {edges.error !== null && (
        <RailGroup label={extendedBy}>
          <ErrorState error={edges.error} onRetry={edges.reload} />
        </RailGroup>
      )}
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
  // The media type the registry recorded for the file. Only a fetched file
  // carries one, so an inline file leaves it empty and the detail card states
  // that the registry recorded none rather than dropping the row.
  contentType: string;
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
    contentType: '',
  }));
  const fetched = Object.entries(artifact.large_resources ?? {}).map(([name, link]) => ({
    name,
    format: formatOf(name, link.content_type),
    delivery: fetchedDelivery,
    size: link.size,
    href: link.presigned_url,
    contentType: link.content_type ?? '',
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

/** extensionFormats names the format a filename suffix carries. The suffix
 * itself is already the end of the file cell, so a column that restated it
 * would carry nothing: `checklist.md` is markdown and `otel-dump.tar.gz` is an
 * archive. A suffix with no entry here has no better name than itself. */
const extensionFormats: Record<string, string> = {
  md: 'markdown',
  markdown: 'markdown',
  ndjson: 'ndjson',
  jsonl: 'ndjson',
  yml: 'yaml',
  txt: 'text',
  gz: 'archive',
  tgz: 'archive',
  tar: 'archive',
  zip: 'archive',
  bz2: 'archive',
  xz: 'archive',
  zst: 'archive',
};

/** contentTypeFormats names the format a media type carries, for a fetched
 * file whose suffix names none. The media type itself has its own row in the
 * detail card, so printing it in the format column would state one value
 * twice and name the format nowhere. */
const contentTypeFormats: Record<string, string> = {
  'text/markdown': 'markdown',
  'text/plain': 'text',
  'text/csv': 'csv',
  'application/json': 'json',
  'application/x-ndjson': 'ndjson',
  'application/gzip': 'archive',
  'application/zip': 'archive',
  'application/x-tar': 'archive',
  'application/octet-stream': 'binary',
};

/** formatOf is the row's format column: what kind of file the row is, drawn
 * from the file's suffix and, for a fetched file whose suffix names no known
 * format, from the media type the registry recorded. */
function formatOf(name: string, contentType?: string): string {
  const dot = name.lastIndexOf('.');
  const extension = dot <= 0 ? '' : name.slice(dot + 1).toLowerCase();
  const named = extensionFormats[extension];
  if (named !== undefined) {
    return named;
  }
  const mediaType = (contentType ?? '').split(';')[0].trim().toLowerCase();
  const fromType = contentTypeFormats[mediaType];
  if (fromType !== undefined) {
    return fromType;
  }
  return extension === '' ? 'unknown' : extension;
}

/** ResourceTable lists every bundled file as one set distinguished by its
 * delivery column. Nothing is previewed, so the row's action is the only path
 * to the file, and the control above the table takes the whole set at once.
 * Selecting a row opens the detail card under the table. */
function ResourceTable({
  rows,
  selected,
  onSelect,
}: {
  rows: ResourceRow[];
  selected: string;
  onSelect: (name: string) => void;
}) {
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
            {/* The download column carries no header; the button in it
                names itself. */}
            <th />
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <tr
              key={row.name}
              className={row.name === selected ? 'row-selected' : ''}
              onClick={() => {
                onSelect(row.name);
              }}
            >
              <td className="mono">
                {/* The file name is a button so the selection that drives the
                    detail card has an input path that is not a pointer click
                    on the row. A row carries no role a keyboard reaches, so
                    without the button the card below the table is unreachable
                    without a mouse. The row's own click handler stays for the
                    pointer, so a click anywhere in the row still selects it. */}
                <button
                  type="button"
                  className="resource-name"
                  aria-pressed={row.name === selected}
                  onClick={() => {
                    onSelect(row.name);
                  }}
                >
                  {row.name}
                </button>
              </td>
              <td className="mono">{row.format}</td>
              <td className="mono">{formatSize(row.size)}</td>
              <td>
                <span className="badge badge-quiet">{row.delivery}</span>
              </td>
              <td>
                {/* The selected row's action is the primary one on the page:
                    the detail card below states that file's attributes, so
                    the two controls that retrieve it read as one pair rather
                    than as the card's action and an outlined row control
                    that happens to do the same thing. */}
                <a
                  className={row.name === selected ? 'button primary' : 'button'}
                  href={row.href}
                  download={row.name}
                >
                  Download ↓
                </a>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {detail !== null && (
        <div className="resource-detail" data-testid="resource-detail">
          <div className="resource-detail-head">
            <p className="label">Selected</p>
            <p className="mono">{detail.name}</p>
          </div>
          {/* The file's attributes are a labelled property grid rather than
              one dot-joined line, so each value is read against the name of
              the attribute it answers and the card states the same set for
              every file. The grid reuses the rail's fact list, because both
              present the same kind of label and value pairs. */}
          <div className="resource-detail-body">
            <dl className="rail-facts" data-testid="resource-detail-facts">
              <div className="rail-fact">
                <dt className="mono">format</dt>
                <dd>{detail.format}</dd>
              </div>
              <div className="rail-fact">
                <dt className="mono">size</dt>
                <dd>{formatSize(detail.size)}</dd>
              </div>
              <div className="rail-fact">
                <dt className="mono">delivery</dt>
                <dd>{detail.delivery}</dd>
              </div>
              <div className="rail-fact">
                <dt className="mono">content type</dt>
                <dd className={detail.contentType === '' ? 'quiet' : undefined}>
                  {detail.contentType === '' ? 'not recorded' : detail.contentType}
                </dd>
              </div>
            </dl>
            <a className="button primary" href={detail.href} download={detail.name}>
              Download ↓
            </a>
          </div>
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
