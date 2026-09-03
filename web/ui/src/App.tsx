// The application shell. It takes the §7.3.4 posture read on load, renders
// the authentication control that read's posture calls for, and hosts the
// §13.10 surfaces: the domain browser, search, the artifact viewer, and the
// layer panel.

import { useCallback, useEffect, useId, useRef, useState } from 'react';

import type { ReactNode, RefObject } from 'react';

import {
  Banner,
  Chevron,
  ErrorState,
  Loading,
  Magnifier,
  PageBanner,
  RailPathLabel,
} from './components/primitives';
import type { DomainDescriptor } from './api';
import {
  ApiError,
  catalogArtifactIDs,
  invalidateDomainReads,
  isIdentityRefusal,
  listLayers,
  loadDomain,
  readQuota,
  signOut,
  subscribeReadOnly,
} from './api';
import type { LayerCapabilities, SessionPosture } from './session';
import { authControl, capabilitiesOf, catalogScope, expiryControl, isSignedIn, readSession } from './session';
import { mayTake, newLayerTarget } from './surfaces/layerrights';
import { catalogDepth, domainLabel, marksCurrentDomain } from './domain';
import { useModalOpen, usePopupDismiss } from './components/focus';
import {
  ReportFailureTitle,
  artifactDomain,
  domainHref,
  layersHref,
  routeKey,
  searchHref,
  useDocumentTitle,
  useEnteredSurface,
  useRoute,
  useTopOfNewRoute,
} from './route';
import { since } from './time';
import type { ThemePreference } from './theme';
import { useTheme } from './theme';
import { useAsync } from './useAsync';
import { CommandPalette } from './surfaces/CommandPalette';
import { DomainBrowser } from './surfaces/DomainBrowser';
import { SearchSurface } from './surfaces/SearchSurface';
import { ArtifactViewer } from './surfaces/ArtifactViewer';
import { LayerPanel } from './surfaces/LayerPanel';
import { DeletedLayers } from './surfaces/DeletedLayers';

/** treeDepth is how many levels of the domain hierarchy the sidebar tree
 * resolves eagerly. A level below that edge is read when the reader expands
 * the node it hangs under, so the shell holds the top of the hierarchy
 * without reading the whole of it. */
const treeDepth = 2;

/** siblingCap is how many domains one level of the sidebar tree lists before
 * the rest fold behind a remainder row. It is the tree's counterpart to the
 * domain page's tile cap: a level that draws every child of a wide domain
 * fills the sidebar with one level and pushes the pinned footer below the
 * fold, so the tree keeps a level to a screen's worth of rows and states how
 * many it is holding back. */
const siblingCap = 8;

/** contentID names the content region the skip link jumps to. */
const contentID = 'main-content';

export function App() {
  const route = useRoute();
  const [posture, setPosture] = useState<SessionPosture | null>(null);
  const [postureLoaded, setPostureLoaded] = useState(false);
  const [catalogError, setCatalogError] = useState<unknown>(null);
  const [readOnly, setReadOnly] = useState(false);
  // catalogNonce re-issues the shell's own catalog read. The expiry treatment
  // and the refused arm both offer a retry of that read, and a retry that
  // reloaded the document would lose the posture the page already holds.
  const [catalogNonce, setCatalogNonce] = useState(0);
  // reachNonce marks a read that reached the registry after one that did not.
  // The tree's deeper levels are read per node and each node holds its own
  // failure, which the shell's root read never touches: that read answered,
  // so nothing about it moves when a surface beside it recovers. The bump is
  // what re-issues those node reads, so one retry clears the sidebar the same
  // outage marked rather than leaving the reader to collapse the row and
  // expand it again.
  const [reachNonce, setReachNonce] = useState(0);
  // The palette is reachable from every surface, so the shell owns whether it
  // is open and the whole page carries the accelerator that opens it.
  const [paletteOpen, setPaletteOpen] = useState(false);
  // The two places the palette hands focus back to: the header control that
  // advertises it, and the content region a result opened from it lands on.
  const searchTrigger = useRef<HTMLButtonElement>(null);
  const content = useRef<HTMLElement>(null);
  const [theme, setTheme] = useTheme();
  // A modal dialog owns the keyboard while it is open, and the accelerator
  // below is one of the keys it owns.
  const modalOpen = useModalOpen();

  // A surface entered from a link is drawn into the document the reader was
  // already scrolled inside, so the shell puts the window back at the top.
  useTopOfNewRoute();

  // One document draws every surface, so the tab and the history entry are
  // named here rather than by the surface that happens to be mounted. A
  // surface whose read resolved nothing reports the failure it renders, and
  // the report carries the route it was made on: a read that failed stays
  // failed while the next route's read is in flight, so a report the reader
  // has already left is dropped rather than naming the surface they are on.
  const entered = routeKey(route);
  const [failure, setFailure] = useState<{ key: string; name: string } | null>(null);
  const reportFailure = useCallback(
    (name: string | null) => {
      setFailure(name === null ? null : { key: entered, name });
    },
    [entered],
  );
  useDocumentTitle(route, failure !== null && failure.key === entered ? failure.name : null);

  // A surface swapped into the same document announces nothing on its own,
  // and the link that was followed unmounts under the reader's focus.
  const entering = useEnteredSurface(route, content);

  useEffect(() => subscribeReadOnly(setReadOnly), []);

  useEffect(() => {
    // The palette mounts as a second modal surface underneath the dialog on
    // top, which covers it while it takes focus into a search field the
    // reader cannot see, and the one Escape that follows unmounts both and
    // discards the dialog and everything typed into it. A dialog the reader
    // can only leave by acknowledging it loses content that is gone once it
    // unmounts. The accelerator is withheld for as long as any modal dialog
    // is on the page.
    if (modalOpen) {
      return;
    }
    const onKey = (event: KeyboardEvent) => {
      // ⌘K on a Mac and ctrl-K elsewhere are the one accelerator, and the
      // browser binds neither to anything the page would be preventing.
      if (event.key.toLowerCase() === 'k' && (event.metaKey || event.ctrlKey)) {
        event.preventDefault();
        setPaletteOpen((open) => !open);
      }
    };
    window.addEventListener('keydown', onKey);
    return () => {
      window.removeEventListener('keydown', onKey);
    };
  }, [modalOpen]);

  // Entering a surface closes the panel, which is what opening a result from
  // it already does. A route change the panel did not issue leaves it
  // covering a surface the reader deliberately entered, still listing the
  // previous query: the browser's back step is one such change, and a history
  // step also moves focus out of the dialog, which takes the panel's own
  // Escape handler with it and leaves the scrim as the only way out.
  useEffect(() => {
    setPaletteOpen(false);
  }, [entered]);

  // The held domain answers cover the route the reader is on, and they are
  // dropped here rather than in an effect: an effect runs after the surfaces
  // below have mounted and issued their reads, so it would drop the answer
  // the sidebar tree is about to share and restore the second round trip the
  // map exists to remove.
  const readRoute = useRef<string | null>(null);
  if (readRoute.current !== entered) {
    readRoute.current = entered;
    invalidateDomainReads();
  }

  // The sidebar tree is the shell's own catalog read, re-issued on each route
  // the reader enters. On the layers route it is also the panel's expiry
  // signal: a layer write's refusal carries no session information, so without
  // a catalog read the panel would present each refusal as the only signal and
  // would never learn that the session ended.
  const tree = useAsync(() => loadDomain('', treeDepth), [route.name, catalogNonce]);
  useEffect(() => {
    if (route.name !== 'layers' || tree.loading) {
      return;
    }
    setCatalogError(tree.error);
  }, [route.name, tree.loading, tree.error]);

  // The footer counts, which also carry the depth marker beside the CATALOG
  // label. The layer list carries the layer count and the last ingest each
  // layer reports, and the catalog's artifact count is the length of the
  // unscoped catalog listing, which the registry does not truncate. They are
  // re-read on the same two signals the tree above them is: a layer write from
  // this tab bumps the nonce, because a register or an unregister moves the
  // very figures the footer states, and entering a route re-reads both. The
  // route matters even though the counts do not depend on where the reader is,
  // because a catalog change made outside this tab — another operator, a
  // webhook ingest, a CLI register — reaches the tree on the next route the
  // reader enters, and counts left behind then state totals that contradict
  // the tree directly above them and the domain header beside it.
  const counts = useAsync(() => readCounts(), [route.name, catalogNonce]);

  useEffect(() => {
    let live = true;
    readSession().then(
      (next) => {
        if (live) {
          setPosture(next);
          setPostureLoaded(true);
        }
      },
      () => {
        // A read that does not answer leaves the page holding no value for
        // either key. It holds capabilitiesOf(null), every member false, so
        // the page renders neither authentication control and no layer write
        // control, and a reader recovers the controls by reloading the
        // document.
        if (live) {
          setPosture(null);
          setPostureLoaded(true);
        }
      },
    );
    return () => {
      live = false;
    };
  }, []);

  const onCatalogOutcome = useCallback((err: unknown) => {
    setCatalogError(err);
  }, []);

  // The sidebar tree and the footer counts are the shell's own reads on every
  // route, so re-issuing them is a bump of the nonce wherever the reader is.
  const reloadCatalog = useCallback(() => {
    // A layer write moves the catalog under the held domain answers, so they
    // are dropped before the re-read rather than answering it.
    invalidateDomainReads();
    setCatalogNonce((nonce) => nonce + 1);
  }, []);

  // Whether the shell's own catalog read is standing failed for a reason
  // other than identity. The refused arm carries its own retry and a re-read
  // answers the same refusal, so it is left where it is.
  const shellReadFailed = !tree.loading && tree.error !== null && !isIdentityRefusal(tree.error);
  // surfaceReach counts the reads a layers surface reported as answered. The
  // layers surfaces report reachability rather than the catalog outcome the
  // other surfaces report: a layer read that was refused carries an identity
  // outcome of its own, which the surface reports through its error rather
  // than through this count. What a read that answered does say is that the
  // registry is reachable.
  const [surfaceReach, setSurfaceReach] = useState(0);
  const onReach = useCallback(() => {
    setSurfaceReach((n) => n + 1);
  }, []);
  // The shell re-issues its own read on a surface read that answered while
  // that read is standing failed, so the sidebar and the footer stop stating
  // an outage the surface between them has already come back from. Which of
  // the two arrives first is not fixed, so the re-issue keys on both rather
  // than on the report alone. Each report is acted on once, so a re-issue
  // that fails again is left stated instead of running in a loop.
  // Spec: §13.10.
  const reissued = useRef(0);
  useEffect(() => {
    if (!shellReadFailed || surfaceReach === 0 || reissued.current === surfaceReach) {
      return;
    }
    reissued.current = surfaceReach;
    reloadCatalog();
  }, [shellReadFailed, surfaceReach, reloadCatalog]);

  // A surface read that answers after one that did not is the reader's retry
  // reaching the registry, which is the same condition the sidebar reported
  // when its own read failed. The shell re-issues that read on the
  // transition, so one retry recovers the page and the shell around it rather
  // than leaving a second retry to press. The transition is what it keys on
  // rather than a successful outcome alone, so a re-issue that fails again is
  // left stated instead of being re-issued in a loop.
  const priorOutcome = useRef<unknown>(null);
  useEffect(() => {
    const recovered = priorOutcome.current !== null && catalogError === null;
    priorOutcome.current = catalogError;
    // The layers route's surface reports no read of its own: the shell's read
    // is what the panel keys on, and it carries its own retry there.
    if (!recovered || route.name === 'layers') {
      return;
    }
    // The tree's nodes are re-read whether or not the root read failed,
    // because a node level that did not load is the shell's own failed read
    // and the retry that just answered says the registry is reachable again.
    setReachNonce((nonce) => nonce + 1);
    if (tree.loading || tree.error === null) {
      return;
    }
    reloadCatalog();
  }, [catalogError, route.name, tree.loading, tree.error, reloadCatalog]);

  const retryCatalog = useCallback(() => {
    // The shell owns the catalog read on the layers route, so the retry
    // re-issues it in place. Every other route's surface owns the read that
    // was refused, and reloading the document is what re-issues that one.
    if (route.name === 'layers') {
      reloadCatalog();
      return;
    }
    window.location.reload();
  }, [route.name, reloadCatalog]);

  if (!postureLoaded) {
    return <Loading label="Loading." />;
  }

  const refused = isIdentityRefusal(catalogError);
  // The catalog-scope rule splits the refused arm on a key the posture read
  // supplies. Every refused caller gets the refused state, and a caller whose
  // read resolved a subject additionally gets the expiry transition, because
  // that caller is the one whose session ended. A caller who never held a
  // subject reaches the same refusal on a registry whose verifier admits no
  // browser, and telling that caller a session ended states something false.
  const expired = refused && isSignedIn(posture);
  const scope = catalogScope(posture, refused);
  const subject = posture?.subject ?? '';
  // The capability object is derived once, here, so the closed default the
  // accessor applies is applied once rather than at each surface. It is
  // threaded beside the subject the surfaces already take.
  const caps = capabilitiesOf(posture);
  // Whether the posture read settled anything. It reports nothing about
  // authorization, which the register call below owns, and it is read by the
  // two empty states alone: a read that answered and resolved no caller and a
  // read that did not answer give the same capabilities and the same empty
  // subject, and those two states instruct the reader differently.
  const postureAnswered = posture !== null;
  // The register prediction the panel's control and both empty-state
  // instructions read. It is one call rather than a hand-written condition,
  // so a registry that authenticates nobody keeps the instruction and a
  // caller the registry resolved none of loses it.
  const mayRegister = mayTake('register', newLayerTarget(subject), caps, subject);
  const recovery = <AuthRecovery posture={posture} onRetry={retryCatalog} />;
  const catalogNodes = refused ? [] : (tree.value?.subdomains ?? []);
  // A catalog read that came back holding no domain is a state of its own,
  // distinct from the refused arm and from the read still being in flight.
  // Both of those also render no node, so the empty line is gated on a read
  // that returned rather than on the node list alone.
  const catalogEmpty = !refused && !tree.loading && tree.error === null && catalogNodes.length === 0;
  // A layer whose artifacts sit at its root contributes no domain, so the
  // tree is empty on a registry that is registered, ingested, and serving.
  // The advisory is therefore gated on the same read reporting no artifact at
  // the top either: telling a reader to register a layer while the pane beside
  // the message lists that layer's artifacts names a remedy already applied.
  // Spec: §13.10.
  const catalogBare = catalogEmpty && (tree.value?.notable ?? []).length === 0;
  // A read that failed for a reason other than identity leaves the sidebar
  // with nothing to render and nothing the reader can act on. Left unmarked
  // it reads as a catalog holding no domain, while the footer figures an
  // earlier read returned keep standing as the registry's current state.
  const catalogFailed = !refused && !tree.loading && tree.error !== null;

  // The public-subset arm of the catalog-scope rule carries two pieces. The
  // sidebar footer states that the caller is not signed in, and this banner
  // states the same across the page. Neither claims anything about content
  // beyond what the read returned, and the banner carries no control of its
  // own, because the authentication control belongs to the shell.
  //
  // Both pieces key on the arm rather than on the posture read having
  // answered. A catalog read that answers while the posture read does not
  // lands on the public-subset arm, and that arm carries this presentation:
  // the page states that no subject resolved for it and claims nothing about
  // content beyond what was returned. The authentication controls stay keyed
  // on the read, so that arm renders neither of them.
  const anonymous = scope === 'public-subset' && subject === '';

  // The sidebar states where the page sits in the §4.2 hierarchy, and an
  // artifact sits under the domain that holds it. Without this the artifact
  // viewer draws a sidebar carrying no marked row at all, so nothing on the
  // left says where the open artifact lives.
  const treePath =
    route.name === 'domain'
      ? route.path
      : route.name === 'artifact'
        ? artifactDomain(route.id)
        : '';
  const browseState: SectionState =
    route.name === 'domain' ? 'page' : route.name === 'artifact' ? 'containing' : false;

  return (
    <div className="app">
      {/* The sidebar tree stands between the top bar and the content on every
          route, so a keyboard reader otherwise tabs through the whole
          hierarchy to reach the page. Routing owns `location.hash`, which an
          `href="#main"` skip link would overwrite, so the control moves focus
          itself. */}
      <button
        type="button"
        className="skip-link"
        data-testid="skip-link"
        onClick={() => {
          document.getElementById(contentID)?.focus();
        }}
      >
        Skip to content
      </button>
      {/* The sentence a reader who cannot see the swap is told the surface
          they entered by. It is polite, so it waits for whatever a surface is
          already announcing rather than cutting it off. */}
      <p className="assistive-only" role="status" aria-live="polite" data-testid="route-announcement">
        {entering}
      </p>
      <TopBar
        posture={posture}
        theme={theme}
        onTheme={setTheme}
        searchTrigger={searchTrigger}
        onOpenPalette={() => {
          setPaletteOpen(true);
        }}
      />
      <CommandPalette
        open={paletteOpen}
        onClose={() => {
          setPaletteOpen(false);
        }}
        trigger={searchTrigger}
        content={content}
        // The recovery page is a route of its own under the panel, so the
        // handoff still moves the reader off it and is offered there.
        atLayers={route.name === 'layers' && !route.deleted}
      />
      {anonymous && (
        <PageBanner testID="anonymous-banner">
          You are not signed in. This page shows what the registry served.
        </PageBanner>
      )}
      <div className="app-body">
        <nav className="sidebar" aria-label="Sections">
          {/* An artifact is reached from the domain browser and hangs inside
              the hierarchy that section navigates, so its route keeps the
              Browse row filled. The row carries the containing marker there
              rather than the page marker, because the page is the artifact. */}
          <SectionLink href={domainHref('')} current={browseState}>
            Browse
          </SectionLink>
          <SectionLink href={searchHref('')} current={route.name === 'search' ? 'page' : false}>
            Search
          </SectionLink>
          {/* The layer panel is reachable for every caller on every
              deployment. The nav reads no posture field and states nothing
              about the caller: the panel decides which of its own write
              controls it renders, from the posture read's per-operation
              prediction and each target's own fields. */}
          <SectionLink href={layersHref} current={route.name === 'layers' ? 'page' : false}>
            Layers
          </SectionLink>
          {/* The label carries the depth of the catalog beside it. The figure
              is read from the untruncated §4.5.2 catalog listing the footer
              already reads, so it states how deep this hierarchy runs. The
              sidebar once printed the prefetch depth here instead, which is a
              constant of this navigation and never varies with the catalog it
              sits over, so a hierarchy running six levels deep read
              "2 levels" beside a tree holding the deeper node. */}
          <p className="catalog-label">
            <span className="label">Catalog</span>
            <CatalogDepth counts={refused || catalogFailed ? null : counts.value} />
          </p>
          {/* The refused arm has no catalog to navigate, so the tree and the
              counts are empty rather than absent. */}
          <CatalogTree
            nodes={catalogNodes}
            parent=""
            current={treePath === '' ? null : treePath}
            currentIsPage={route.name === 'domain'}
            onOutcome={onCatalogOutcome}
            reach={reachNonce}
            // The eager read runs at treeDepth, so it answers for the levels
            // of the top-level domains it returns.
            covered={treeDepth > 1}
            // The same read carried the level under each top-level domain, so
            // the tree draws both levels at once.
            eagerOpen={treeDepth > 1}
          />
          {catalogEmpty && (
            <p className="quiet catalog-empty" data-testid="catalog-empty">
              The catalog holds no domains.
              {/* The instruction to register is dropped where the read
                  answered and the register call refuses, because the caller
                  it names cannot take the remedy it names. An unanswered read
                  settles nothing about whether the registry resolved a caller,
                  so the instruction stands there. */}
              {catalogBare
                ? postureAnswered && !mayRegister
                  ? ''
                  : ' Register a layer to fill it.'
                : ' Its artifacts sit at the top of the hierarchy.'}
            </p>
          )}
          {/* The failed read is the shell's own, and the surface beside it
              retries only the read the surface owns. So the retry that clears
              this state sits here, where the state it clears is stated. */}
          {catalogFailed && (
            <div className="catalog-empty" data-testid="catalog-failed">
              <p className="quiet">The catalog could not be read.</p>
              {/* A surface can carry a retry of its own at the same moment,
                  so this one names the read it re-issues. The name opens with
                  the visible label, which is what a voice control matches
                  on. */}
              <button
                type="button"
                className="catalog-retry"
                data-testid="catalog-retry"
                aria-label="Try again reading the catalog"
                onClick={reloadCatalog}
              >
                Try again
              </button>
            </div>
          )}
          <div className="sidebar-footer">
            <CatalogCounts counts={refused || catalogFailed ? null : counts.value} unavailable={catalogFailed} />
            {anonymous && <p className="quiet">Not signed in</p>}
          </div>
        </nav>
        <main className="content" id={contentID} ref={content} tabIndex={-1}>
          {/* The expiry transition is rendered over the page the caller was
              on, which is kept rather than cleared, so it sits above the
              surface on every route and the surface stays mounted under it.
              The refused arm that stands in place of the catalog is the one
              reached by a caller who held no subject, where there is no page
              to keep. */}
          {expired && <SessionEnded recovery={recovery} />}
          {refused && !expired && route.name === 'layers' && <RefusedRead onRetry={retryCatalog} />}
          {refused && !expired && route.name !== 'layers' ? (
            <RefusedCatalog error={catalogError} recovery={recovery} />
          ) : (
            <ReportFailureTitle.Provider value={reportFailure}>
              <Surface
                route={route}
                subject={subject}
                caps={caps}
                postureAnswered={postureAnswered}
                readOnly={readOnly}
                onCatalogOutcome={onCatalogOutcome}
                onCatalogChange={reloadCatalog}
                onReach={onReach}
              />
            </ReportFailureTitle.Provider>
          )}
        </main>
      </div>
    </div>
  );
}

function Surface({
  route,
  subject,
  caps,
  postureAnswered,
  readOnly,
  onCatalogOutcome,
  onCatalogChange,
  onReach,
}: {
  route: ReturnType<typeof useRoute>;
  subject: string;
  /** caps is what the layer endpoints admit this caller on. Surface holds no
   * posture of its own, so the props are the only route to the surfaces that
   * predict a write. */
  caps: LayerCapabilities;
  /** postureAnswered reports whether the posture read settled anything. The
   * layer panel's empty state is its only reader below this point. */
  postureAnswered: boolean;
  readOnly: boolean;
  onCatalogOutcome: (err: unknown) => void;
  onCatalogChange: () => void;
  onReach: () => void;
}) {
  switch (route.name) {
    case 'search':
      // The surface seeds its filter state from the query, so a fresh query
      // arriving from the palette while the surface is already open remounts
      // it rather than leaving the prior query's pills standing.
      return <SearchSurface key={route.query} query={route.query} onError={onCatalogOutcome} />;
    case 'artifact':
      return <ArtifactViewer id={route.id} viewing={route.version} onError={onCatalogOutcome} />;
    case 'layers':
      // A restore moves the same figures the sidebar footer states, so the
      // recovery surface reports it the way every other layer write does.
      return route.deleted ? (
        <DeletedLayers
          subject={subject}
          caps={caps}
          onRestored={onCatalogChange}
          readOnly={readOnly}
          onReach={onReach}
        />
      ) : (
        <LayerPanel
          subject={subject}
          caps={caps}
          postureAnswered={postureAnswered}
          readOnly={readOnly}
          onCatalogChange={onCatalogChange}
          onReach={onReach}
        />
      );
    case 'domain':
      return <DomainBrowser path={route.path} onError={onCatalogOutcome} />;
  }
}

/** CatalogTotals is what the sidebar states about the catalog as a whole: how
 * many layers the tenant carries, how many artifacts its catalog matches, how
 * deep the hierarchy runs, and when a layer was last ingested. The counts and
 * the ingest line stand in the footer, and the depth stands beside the catalog
 * label, because all four come from the same pair of reads. */
interface CatalogTotals {
  layers: number;
  artifacts: number;
  depth: number;
  lastIngest: string;
}

async function readCounts(): Promise<CatalogTotals> {
  const [layers, ids] = await Promise.all([
    listLayers(),
    // The catalog answers one canonical ID per artifact over the whole
    // tenant, which is the figure the footer states. An unfiltered search
    // does not answer it: its match count is one row per artifact version,
    // so an artifact republished four times counts four times and the footer
    // contradicts the tree beside it. Spec: §4.5.2.
    catalogArtifactIDs(''),
  ]);
  return {
    layers: layers.length,
    artifacts: ids.length,
    depth: catalogDepth(ids),
    lastIngest: layers.reduce((latest, layer) => {
      const at = layer.last_ingested_at ?? '';
      return at > latest ? at : latest;
    }, ''),
  };
}

/** CatalogDepth is the marker beside the catalog label. It states how many
 * levels of §4.2 domain the catalog runs to, which is what the tree below it
 * navigates. It follows the footer's discipline: a read that has not
 * answered, a read that failed, and the refused arm leave the marker off
 * rather than standing a figure beside a tree no response described. */
function CatalogDepth({ counts }: { counts: CatalogTotals | null }) {
  if (counts === null) {
    return null;
  }
  return (
    <span className="mono quiet catalog-depth" data-testid="catalog-depth">
      {counts.depth} {counts.depth === 1 ? 'level' : 'levels'}
    </span>
  );
}

/** CatalogCounts is the footer pinned to the bottom of the sidebar. It states
 * what the reads returned and nothing else: a read that has not answered, and
 * the refused arm, leave it standing with no counts in it rather than
 * reporting a figure no response carried. A catalog read that failed
 * withdraws the figures and says so, because the next read of them is a route
 * or a layer write away and a figure left standing over a registry that
 * stopped answering is presented as its current state. */
function CatalogCounts({ counts, unavailable = false }: { counts: CatalogTotals | null; unavailable?: boolean }) {
  if (unavailable) {
    return (
      <p className="mono quiet" data-testid="catalog-counts">
        Counts unavailable
      </p>
    );
  }
  if (counts === null) {
    return <p className="mono quiet" data-testid="catalog-counts" />;
  }
  return (
    <>
      <p className="mono quiet" data-testid="catalog-counts">
        {counts.layers} {counts.layers === 1 ? 'layer' : 'layers'} · {counts.artifacts}{' '}
        {counts.artifacts === 1 ? 'artifact' : 'artifacts'}
      </p>
      <p className="mono quiet" data-testid="catalog-ingest">
        {counts.lastIngest === '' ? 'never ingested' : `ingested ${since(counts.lastIngest, Date.now())}`}
      </p>
    </>
  );
}

/** SectionState is how a section row stands to the page. `page` is the row
 * whose surface the page is, `containing` is the row whose surface holds the
 * page without being it, which is what the artifact viewer stands to the
 * domain browser, and `false` is every other row. */
type SectionState = 'page' | 'containing' | false;

/** SectionLink is one row of the sidebar's section navigation. The row for
 * the surface the reader is on is filled and carries `aria-current`, so the
 * shell states which of the §13.10 surfaces the page belongs to, in the
 * sidebar's own terms rather than only in the content beside it. */
function SectionLink({
  href,
  current,
  children,
}: {
  href: string;
  current: SectionState;
  children: ReactNode;
}) {
  return (
    <a
      href={href}
      className={current === false ? 'section-link' : 'section-link section-link-current'}
      // The containing row is not the page, so it takes the set-membership
      // marker rather than the page marker. A row claiming to be the page
      // while the reader is on an artifact states something false to a
      // screen reader the fill says nothing to.
      aria-current={current === 'page' ? 'page' : current === 'containing' ? true : undefined}
    >
      {children}
    </a>
  );
}

/** onCurrentPath reports whether `path` is the domain the reader is on or an
 * ancestor of it. A §4.2 domain path is `/`-separated, and a sparse chain is
 * collapsed into one entry by the server, so a node's path is compared whole
 * rather than segment by segment. */
function onCurrentPath(path: string, current: string | null): boolean {
  if (current === null || path === '') {
    return false;
  }
  return current === path || current.startsWith(`${path}/`);
}

/** CatalogTree is the sidebar's navigation over the §4.2 domain hierarchy.
 * The levels the eager read returned are rendered at once and a deeper level
 * is read when the reader expands the node it hangs under.
 *
 * `current` is the domain the page sits at: the one it shows on a domain
 * route, and the one holding the open artifact on an artifact route. The tree
 * resolves the ancestry down to it and marks it, so a reader who arrived by a
 * link or a breadcrumb sees where in the hierarchy the page sits instead of a
 * row of collapsed roots. `currentIsPage` separates the two: an artifact's
 * domain is where the page sits without being the page.
 *
 * A level wider than the cap keeps the remainder behind one row. A domain
 * that carries a couple of dozen children otherwise draws a couple of dozen
 * rows, which runs the sidebar past the viewport and takes the pinned footer
 * counts with it, so the levels above it and the footer under it both leave
 * the screen to list one domain's children. */
function CatalogTree({
  nodes,
  parent,
  current,
  currentIsPage,
  onOutcome,
  reach,
  covered,
  eagerOpen,
}: {
  nodes: DomainDescriptor[];
  parent: string;
  current: string | null;
  currentIsPage: boolean;
  onOutcome: (err: unknown) => void;
  reach: number;
  /** covered reports whether the read that produced these nodes also carried
   * their own children. A read of depth 2 answers for the level it returns
   * and for the level under it, so a node in the first of those two carries
   * `subdomains` when it has any and omits the field when it has none: the
   * omission is an answer. A node in the second is at the read's edge, where
   * the same omission says only that the read stopped there. */
  covered: boolean;
  /** eagerOpen renders the level under each of these nodes without waiting
   * for the reader to expand it. It is set on the tree's top level, whose
   * children the eager read already carried, so a reader landing on the shell
   * sees two levels of the hierarchy instead of a row of collapsed roots. A
   * level below that comes from a read the reader's own expansion issued, so
   * it opens no further on its own. */
  eagerOpen: boolean;
}) {
  const [all, setAll] = useState(false);
  // The reader's own position is never one of the folded rows: a level whose
  // current domain sits past the cap is drawn whole, because the row that
  // marks where the page sits is what the tree is for.
  //
  // foldable separates the level that has a remainder to hold back from the
  // state it is currently in, so the row stays mounted across the expansion
  // and keeps the focus a keyboard reader put on it. A row that unmounted on
  // the click it handled would drop that reader onto the document body, with
  // the whole shell to tab back through.
  const foldable =
    nodes.length > siblingCap &&
    !nodes.slice(siblingCap).some((node) => onCurrentPath(node.path, current));
  const folded = foldable && !all;
  const shown = folded ? nodes.slice(0, siblingCap) : nodes;

  return (
    // Only the outermost list names the navigation. A nested level is a
    // subtree of the same navigation, and repeating the name there gives a
    // reader several controls called "Catalog" that are one navigation.
    <ul className="catalog-tree" aria-label={parent === '' ? 'Catalog' : undefined}>
      {shown.map((node) => (
        <TreeNode
          key={node.path}
          node={node}
          parent={parent}
          current={current}
          currentIsPage={currentIsPage}
          onOutcome={onOutcome}
          reach={reach}
          covered={covered}
          eagerOpen={eagerOpen}
        />
      ))}
      {foldable && (
        <li className="catalog-node">
          {/* The row states how many domains it is holding back and opens
              them in place, so the level is reachable from the tree rather
              than only from the domain page's subdomain list. Opened, the
              same row folds the remainder back, which is what keeps it in
              the DOM and keeps focus on it. */}
          <button
            type="button"
            className="catalog-more mono"
            aria-expanded={!folded}
            onClick={() => {
              setAll(folded);
            }}
          >
            {folded
              ? `+ ${String(nodes.length - siblingCap)} more`
              : '− show fewer'}
          </button>
        </li>
      )}
    </ul>
  );
}

/** TreeNode is one domain in the sidebar tree. A node whose children came
 * with the eager read renders them from it, and a node at the read's edge
 * reads its own level when it is expanded.
 *
 * That deeper read is a catalog read, so its failure is split three ways. A
 * refusal for an unverifiable identity is handed to the shell, because the
 * catalog read is the expiry signal and a caller whose session ends while the
 * page is open is owed the transition rather than a relabelled node. An
 * authorization refusal leaves the domain listed and not enterable, which is
 * what the reader is owed there: the domain is in the hierarchy and this
 * caller cannot open it. Every other failure is the surface's own error
 * state, so the domain stays enterable, the node states that the level did
 * not load, and the read is re-issued by a later expansion or by the shell
 * reaching the registry again. */
function TreeNode({
  node,
  parent,
  current,
  currentIsPage,
  onOutcome,
  reach,
  covered,
  eagerOpen,
}: {
  node: DomainDescriptor;
  parent: string;
  current: string | null;
  currentIsPage: boolean;
  onOutcome: (err: unknown) => void;
  /** reach counts the reads that reached the registry after one that did
   * not. A bump re-issues this node's level, so a node whose read failed
   * during an outage clears when a retry elsewhere on the page answers. */
  reach: number;
  /** covered reports whether the read that produced this node also answered
   * for its children. See CatalogTree. */
  covered: boolean;
  /** eagerOpen draws this node's own level without a press. See CatalogTree. */
  eagerOpen: boolean;
}) {
  const ancestor = onCurrentPath(node.path, current);
  const eager = node.subdomains;
  // The level the eager read carried is drawn with the node rather than held
  // behind its toggle, so the shell opens on two levels of the hierarchy. The
  // open state is still the reader's from the first press onward: a node they
  // close stays closed. Only a node the read answered for opens this way, so
  // no row opens onto a level that is not in hand and the tree issues no read
  // the reader did not ask for.
  const [open, setOpen] = useState(ancestor || (eagerOpen && (eager?.length ?? 0) > 0));
  const [loaded, setLoaded] = useState<DomainDescriptor[] | null>(null);
  const [restricted, setRestricted] = useState(false);
  const [failed, setFailed] = useState(false);
  const label = domainLabel(node.path, parent);
  const children = eager ?? loaded;
  // The row is marked for the domain the reader is on and for every level a
  // collapsed chain swallowed into it, because those levels have no row of
  // their own to carry the marker.
  const isCurrent = marksCurrentDomain(node.path, parent, current);
  const isCurrentPath = node.path === current;
  // A node the eager read already reported empty is a leaf, so the row draws
  // the blank marker in the toggle's slot and keeps the label aligned with
  // its siblings. The reader never had a toggle there to press.
  //
  // The read reports an empty level by omitting `subdomains` as well as by
  // returning it empty, so a covered node with no field is the same leaf.
  // Drawing it with a disclosure instead puts a control on every leaf in the
  // catalog that reveals nothing when it is pressed, and leaves the tree
  // rendered differently depending on which rows the reader has pressed.
  const leaf = covered ? (eager?.length ?? 0) === 0 : eager !== undefined && eager.length === 0;
  // A node whose own level came back empty is a leaf the reader discovered by
  // pressing the toggle, and that press is why the row keeps a control in the
  // slot rather than dropping it. Unmounting the button the reader is
  // standing on drops keyboard focus to the document body, which loses their
  // place in the tree, and with a pointer the triangle vanishes with no
  // stated outcome. The control stays in place, marked unavailable, and its
  // name states that the level holds no subdomains.
  const emptied = !leaf && loaded !== null && loaded.length === 0;

  // A route that moves onto this node's ancestry opens it. The tree is not
  // remounted when the reader follows a link, so the ancestry has to reach an
  // already-mounted node rather than only its initial state. Opening is all
  // this does: a node the reader closed by hand off the current path stays
  // closed.
  useEffect(() => {
    if (ancestor) {
      setOpen(true);
    }
  }, [ancestor]);

  // An open node reads its own level when the eager read did not carry it,
  // whether the reader expanded it or the route did. Only the authorization
  // refusal latches: a level that did not load for any other reason is read
  // again the next time the node opens, and again when a read elsewhere on
  // the page reaches a registry that had stopped answering.
  useEffect(() => {
    if (!open || eager !== undefined || loaded !== null || restricted) {
      return;
    }
    let live = true;
    setFailed(false);
    loadDomain(node.path, treeDepth).then(
      (level) => {
        if (live) {
          setLoaded(level.subdomains);
        }
      },
      (err: unknown) => {
        if (!live) {
          return;
        }
        if (isIdentityRefusal(err)) {
          onOutcome(err);
          return;
        }
        if (err instanceof ApiError && err.status === 403) {
          setRestricted(true);
          return;
        }
        setFailed(true);
      },
    );
    return () => {
      live = false;
    };
  }, [open, eager, loaded, restricted, node.path, onOutcome, reach]);

  return (
    <li className="catalog-node">
      {/* The row is its own element so the current domain's fill stops at the
          row rather than running down the nested level under it. */}
      <div className={isCurrent ? 'catalog-row catalog-row-current' : 'catalog-row'}>
        {leaf ? (
          <span className="tree-leaf" aria-hidden="true" />
        ) : (
          // The marker is the same drawing on every row, so the toggle takes
          // its name from the domain it opens. Without it a reader arriving by
          // keyboard or screen reader meets a run of identically named buttons
          // and cannot tell which level each one expands.
          //
          // The emptied node keeps the same button element, so React updates
          // it in place and the focus the reader put on it survives the level
          // resolving to nothing. It carries aria-disabled rather than
          // disabled for the same reason: a disabled control is removed from
          // the focus order, and the browser drops focus to the body.
          //
          // It leaves the tab order instead, through tabindex. A browser keeps
          // focus where it is when the focused element takes tabindex="-1", so
          // the reader who pressed the toggle stays on it and is told the
          // outcome, while every other reader tabs from the row above straight
          // to the next row rather than through a control that does nothing.
          // A domain the route opened reaches the same state with no press at
          // all, so leaving the row in the tab order costs a stop per leaf.
          <button
            type="button"
            className={emptied ? 'tree-toggle tree-toggle-empty' : 'tree-toggle'}
            aria-expanded={emptied ? undefined : open}
            aria-disabled={emptied ? true : undefined}
            tabIndex={emptied ? -1 : undefined}
            aria-label={
              emptied ? `${label} has no subdomains` : `${open ? 'Collapse' : 'Expand'} ${label}`
            }
            onClick={() => {
              if (!emptied) {
                setOpen(!open);
              }
            }}
          >
            {/* The open and closed states are one chevron that the row turns,
                which is the same indicator the subdomain cards on the domain
                page draw. A filled triangle here would put two unrelated
                disclosure marks in one view. The spent node keeps a typed
                marker, because it opens nothing and a chevron would read as
                something still to expand. */}
            {emptied ? '·' : <Chevron />}
          </button>
        )}
        {/* The label is the whole folded stretch of path the entry navigates
            across, and the row clips it to the sidebar's width, so it carries
            the label as its title for a reader whose row is too narrow. The
            clipping falls on the ancestry rather than on the segment naming
            the domain the row opens. */}
        {restricted ? (
          <>
            <span className="mono" title={label}>
              <RailPathLabel path={label} />
            </span>
            <span className="catalog-marker" title="restricted" data-testid="restricted-domain">
              restricted
            </span>
          </>
        ) : (
          <a
            className="mono"
            href={domainHref(node.path)}
            title={label}
            // The label is drawn in two boxes so the row clips the ancestry
            // rather than the name, and the name computed from the two runs
            // of text carries a break between them. The link states its own
            // name instead, so a reader hearing the row hears the path.
            aria-label={label}
            // The domain holding an open artifact is where the page sits in
            // the hierarchy without being the page, so it takes the location
            // marker there and the page marker on a domain route. A chain
            // entry standing in for a level it swallowed takes the location
            // marker too: the link navigates to the chain's endpoint rather
            // than to the domain the reader is on.
            aria-current={
              isCurrent ? (currentIsPage && isCurrentPath ? 'page' : 'location') : undefined
            }
          >
            <RailPathLabel path={label} />
          </a>
        )}
        {/* The failed arm states that this level did not load and claims
            nothing about what the caller may see. Expanding the node again
            retries it, and so does a retry beside it that reaches the
            registry. */}
        {failed && (
          <span className="catalog-marker" title="did not load" data-testid="unavailable-domain">
            did not load
          </span>
        )}
        {/* The outcome of an expansion that resolved to nothing is carried by
            the toggle's own name rather than drawn beside it. The row's
            right-aligned slot is narrow enough that "no subdomains" clips to a
            fragment beside any name longer than a few characters, and the
            sentence it clips to says nothing; the visible outcome the reader
            gets is the toggle's spent glyph and the domain page's own line.
            The reader who pressed the toggle and cannot see the row keeps
            focus on it, and the label it renames itself to states the outcome.

            The row publishes no live region of its own. A per-row description
            is static text that holds for as long as the row is drawn, so a
            role="status" span here re-announces the same sentence on every
            re-render the tree takes for a layer write, a reingest, or a
            catalog refresh, and it competes with the result-set and
            write-outcome announcements the surfaces publish. */}
      </div>
      {open && children !== null && children.length > 0 && (
        <CatalogTree
          nodes={children}
          parent={node.path}
          current={current}
          currentIsPage={currentIsPage}
          onOutcome={onOutcome}
          reach={reach}
          // Children that came with this node sit at the edge of the read
          // that carried them, so nothing is known about their own levels.
          // Children this node read itself came from a read of the same
          // depth as the eager one, which answered for their levels too.
          covered={eager === undefined && treeDepth > 1}
          // A level below the tree's top two is drawn because the reader
          // expanded the node above it, so it opens no further on its own.
          eagerOpen={false}
        />
      )}
    </li>
  );
}

/** SessionEnded is the expiry transition. It is rendered for a caller whose
 * posture read resolved a subject and whose catalog read was then refused,
 * because that pair is what marks a session that ended while the page was
 * open. The page underneath is kept, and the control beside the sentence is
 * whatever the deployment's posture licenses. */
function SessionEnded({ recovery }: { recovery: ReactNode }) {
  return (
    <div className="banner banner-danger" role="alert" data-testid="session-ended">
      <p className="banner-title">Your session has expired. Please log in again.</p>
      {recovery}
    </div>
  );
}

/** RefusedRead is the refused arm on a surface the page keeps: it states that
 * the registry served no catalog for this request and claims nothing about a
 * session, which is what a caller whose read resolved no subject is owed. Such
 * a caller reaches the refusal on a registry whose verifier admits no browser
 * at all, where an instruction to sign in again names no flow the registry
 * runs. */
function RefusedRead({ onRetry }: { onRetry: () => void }) {
  return (
    <div className="banner banner-danger" role="alert" data-testid="refused-read">
      <p className="banner-title">The registry served no catalog for this request.</p>
      <p>It verified no identity for the read, so it returned none.</p>
      <button type="button" onClick={onRetry}>
        Try again
      </button>
    </div>
  );
}

/** RefusedCatalog is the arm of the catalog-scope rule the page renders where
 * a catalog read was refused because the caller's identity could not be
 * verified. Such a caller has no anonymous view of the catalog, so the page
 * renders this in place of the catalog rather than an empty or a filtered
 * one. It states that the registry did not serve this catalog to this caller
 * and says nothing about what the catalog holds. It is rendered only where
 * the expiry treatment is not, so the page offers one recovery control rather
 * than two. */
function RefusedCatalog({ error, recovery }: { error: unknown; recovery: ReactNode }) {
  return (
    <section className="surface" aria-label="Catalog refused">
      <h1>This catalog was not served to you</h1>
      <p>The registry did not verify an identity for this request, so it served no catalog.</p>
      {recovery}
      <ErrorState
        error={error}
        onRetry={() => {
          window.location.reload();
        }}
      />
    </section>
  );
}

/** AuthRecovery is the control the refused-catalog arm and the expiry
 * treatment offer the caller. What it may be is bounded by the sign-in
 * control rule's third row: a deployment reporting the browser flow disabled
 * renders no authentication control on any value of subject, so on such a
 * deployment this offers a retry of the refused read as its only control and
 * states that as what stands in place of sign-in.
 *
 * The arm where the posture read did not answer is separate. It is reachable
 * on any deployment, because a transient failure of that read leaves the page
 * holding no posture whatever the registry runs, so the recovery states
 * nothing about whether a browser sign-in exists and offers the retry alone.
 * Printing the disabled-deployment sentence there would assert a deployment
 * property no read reported. */
function AuthRecovery({ posture, onRetry }: { posture: SessionPosture | null; onRetry: () => void }) {
  const control = expiryControl(posture);
  if (control.kind === 'sign-in') {
    return (
      <a className="button primary" data-testid="expiry-sign-in" href={control.path}>
        Sign in
      </a>
    );
  }
  return (
    <>
      <p className="quiet">
        {posture === null
          ? 'The registry did not report its authentication posture for this page, so there is nothing to offer ' +
            'beyond the read itself.'
          : 'This registry runs no browser sign-in. Retry the read once the credential it reads is in place again, ' +
            'or ask the operator who runs it.'}
      </p>
      <button type="button" data-testid="expiry-retry" onClick={onRetry}>
        Try again
      </button>
    </>
  );
}

/** docsHref is where the shell's Docs link goes. The documentation is a site
 * of its own rather than anything the registry serves, so the link leaves the
 * origin and says so. */
const docsHref = 'https://lennylabs.github.io/podium';

/** TopBar is the shell's one header: the wordmark, the registry this page is
 * served from, the trigger that opens the palette, the documentation link,
 * and the identity cluster.
 *
 * The cluster carries the one authentication control. The sign-in control
 * rule keys it on the posture read's browser_auth.enabled and subject, and
 * both conjuncts are required on each control: a deployment running no
 * browser flow renders neither on any value of subject, which covers the
 * gateway-fronted deployment where a subject resolves because the gateway
 * authenticated the request. Each path comes from the read rather than from a
 * literal in this bundle. */
function TopBar({
  posture,
  theme,
  onTheme,
  searchTrigger,
  onOpenPalette,
}: {
  posture: SessionPosture | null;
  theme: ThemePreference;
  onTheme: (next: ThemePreference) => void;
  searchTrigger: RefObject<HTMLButtonElement | null>;
  onOpenPalette: () => void;
}) {
  const control = authControl(posture);
  const subject = posture?.subject ?? '';
  // The cluster names the reader by their email where the read carries one,
  // because a provider-chosen subject is often an opaque identifier. The
  // fallback covers both arms that carry none: a provider that recorded no
  // email for this caller, and an older registry whose posture read reports
  // no such key.
  const display = posture?.email || subject;
  return (
    <header className="topbar">
      <Wordmark />
      {/* The registry the page is served from. The bundle is served by the
          registry itself, so the origin names it and no response has to. */}
      <span className="mono topbar-host" data-testid="registry-host">
        {window.location.host}
      </span>
      <span className="spacer" />
      <button
        type="button"
        className="search-trigger"
        data-testid="search-trigger"
        ref={searchTrigger}
        onClick={onOpenPalette}
      >
        <Magnifier />
        Search artifacts
        <span className="mono key-hint">⌘K</span>
      </button>
      <span className="topbar-divider" aria-hidden="true" />
      <a className="docs-link" href={docsHref} target="_blank" rel="noreferrer">
        Docs <span aria-hidden="true">↗</span>
      </a>
      <span className="topbar-divider" aria-hidden="true" />
      {control.kind === 'sign-in' && (
        <a className="button primary" data-testid="sign-in" href={control.path}>
          Sign in
        </a>
      )}
      {/* The cluster stands wherever a subject resolved, and the sign-out
          entry point inside it is what the sign-in control rule gates: a
          subject that resolved on a deployment running no browser flow gets
          the menu without it. Where no subject resolves, which is the default
          standalone deployment, the appearance preference stands on its own
          instead: it is the client's own state, it predicts no server
          outcome, and leaving it inside the identity cluster would pin every
          reader on that deployment to prefers-color-scheme. */}
      {subject !== '' ? (
        <AccountMenu
          display={display}
          theme={theme}
          onTheme={onTheme}
          signOutPath={control.kind === 'sign-out' ? control.path : null}
        />
      ) : (
        <AppearanceMenu theme={theme} onTheme={onTheme} />
      )}
    </header>
  );
}

/** useTopbarMenu holds one topbar popover open and gives it the dismissal
 * paths every transient overlay in this shell owes a reader: Escape closes it
 * and hands focus back to the trigger, a press or a focus move outside it
 * closes it, and entering another surface closes it. Without the last one the
 * menu stands over a surface the reader deliberately entered, which is the
 * same leak the palette closes on a route change.
 *
 * The hook also mints the popover's id, so a trigger can point aria-controls
 * at the element it owns and every topbar popover carries the same wiring.
 * Whether the popover is one of the kinds aria-haspopup names differs between
 * the two triggers, so that attribute stands on each trigger rather than here.
 *
 * Spec: §13.10
 */
function useTopbarMenu() {
  const [open, setOpen] = useState(false);
  const menuId = useId();
  const trigger = useRef<HTMLButtonElement>(null);
  const menu = usePopupDismiss<HTMLDivElement>(
    open,
    () => {
      setOpen(false);
    },
    trigger,
  );
  const entered = routeKey(useRoute());
  useEffect(() => {
    setOpen(false);
  }, [entered]);
  return {
    open,
    menuId,
    trigger,
    menu,
    toggle: () => {
      setOpen((prior) => !prior);
    },
  };
}

/** AccountMenu is the identity cluster and the menu behind it. It carries the
 * caller's own identity, the appearance preference, the layer quota, and the
 * sign-out entry point where the deployment runs one. It carries no role
 * badge, no capability report, and no group membership: no response reports
 * the caller's role, the posture read's per-operation prediction is read by
 * the layer panel alone, and no response enumerates the caller's groups. */
function AccountMenu({
  display,
  theme,
  onTheme,
  signOutPath,
}: {
  display: string;
  theme: ThemePreference;
  onTheme: (next: ThemePreference) => void;
  signOutPath: string | null;
}) {
  const { open, menuId, trigger, menu, toggle } = useTopbarMenu();
  return (
    <div className="account">
      <button
        type="button"
        className="account-trigger"
        data-testid="account-trigger"
        aria-haspopup="menu"
        aria-controls={menuId}
        aria-expanded={open}
        ref={trigger}
        onClick={toggle}
      >
        <span className="mono avatar" aria-hidden="true">
          {initialsOf(display)}
        </span>
        <span className="mono subject">{display}</span>
      </button>
      {open && (
        <div
          id={menuId}
          className="account-menu"
          role="menu"
          aria-label="Account"
          data-testid="account-menu"
          ref={menu}
        >
          <p className="mono quiet">{display}</p>
          <AppearanceSwitch theme={theme} onTheme={onTheme} />
          <LayerQuota />
          {signOutPath !== null && <SignOutButton path={signOutPath} />}
        </div>
      )}
    </div>
  );
}

/** themeChoices is the appearance control's options in the order they are
 * offered, each with the sentence-case label the design names. The stored
 * preference stays lowercase because it is the value stamped on the root
 * element, so the label is carried beside it rather than derived from it. */
const themeChoices: { value: ThemePreference; label: string }[] = [
  { value: 'system', label: 'System' },
  { value: 'light', label: 'Light' },
  { value: 'dark', label: 'Dark' },
];

/** AppearanceSwitch is the segmented control that pins the appearance
 * preference. It is the same control wherever it stands, so the identity
 * cluster and the shell's own appearance menu carry one implementation. */
function AppearanceSwitch({
  theme,
  onTheme,
}: {
  theme: ThemePreference;
  onTheme: (next: ThemePreference) => void;
}) {
  return (
    <>
      <p className="label">Appearance</p>
      <div className="segmented" role="group" aria-label="Appearance">
        {themeChoices.map((choice) => (
          <button
            key={choice.value}
            type="button"
            className={theme === choice.value ? 'segment segment-on' : 'segment'}
            aria-pressed={theme === choice.value}
            onClick={() => {
              onTheme(choice.value);
            }}
          >
            {choice.label}
          </button>
        ))}
      </div>
    </>
  );
}

/** AppearanceMenu is where the appearance preference stands on a deployment
 * that resolves no subject. The preference is held in the browser and applied
 * by stamping the root element, so the control reads no posture field and
 * predicts no server outcome, which is why the shell renders it on every
 * deployment that renders no identity cluster.
 *
 * The popover holds the segmented control and nothing else, so it declares no
 * role of its own, and the trigger carries no aria-haspopup. A role="menu"
 * whose children are ordinary toggle buttons rather than menu items is
 * announced as a menu holding no items, and the pinned preference then reads
 * as a pressed toggle instead of the selected member of its group. The
 * unqualified aria-haspopup="true" is defined as equivalent to "menu", so a
 * trigger that kept it would promise the arrow-key item navigation the group
 * does not provide. aria-expanded and aria-controls state that the trigger
 * owns something disclosed and where it stands, which is what a labelled
 * group of toggle buttons is. The group and its label stand on the control
 * itself.
 *
 * Spec: §13.10
 */
function AppearanceMenu({
  theme,
  onTheme,
}: {
  theme: ThemePreference;
  onTheme: (next: ThemePreference) => void;
}) {
  const { open, menuId, trigger, menu, toggle } = useTopbarMenu();
  return (
    <div className="account">
      <button
        type="button"
        className="account-trigger appearance-trigger"
        data-testid="appearance-trigger"
        aria-controls={menuId}
        aria-expanded={open}
        aria-label="Appearance"
        ref={trigger}
        onClick={toggle}
      >
        {/* The disc stands alone, with no visible label beside it. The icon is
            aria-hidden, so the aria-label above is the button's whole
            accessible name and dropping it would leave a nameless control that
            a screen reader announces as "button". */}
        <ContrastDisc />
      </button>
      {open && (
        <div id={menuId} className="account-menu" data-testid="appearance-menu" ref={menu}>
          <AppearanceSwitch theme={theme} onTheme={onTheme} />
        </div>
      )}
    </div>
  );
}

/** ContrastDisc is the appearance trigger's icon: a circle with one half
 * filled, drawn as inline SVG so it takes its colour from the label beside
 * it. */
function ContrastDisc() {
  return (
    <svg className="contrast-disc" width="13" height="13" viewBox="0 0 14 14" aria-hidden="true" focusable="false">
      <circle cx="7" cy="7" r="5.8" fill="none" stroke="currentColor" strokeWidth="1.4" />
      <path d="M7 1.2A5.8 5.8 0 0 1 7 12.8z" fill="currentColor" />
    </svg>
  );
}

/** LayerQuota is the menu's quota entry: the §7.3.1 cap on how many
 * user-defined layers one identity may hold, read from the §4.7.8 quota
 * endpoint. That read is gated on no role, so it is a call an SDK would make
 * against the same endpoint and the menu gains no privileged access.
 *
 * The entry is rendered only where the read reports a figure. A read that
 * fails, and a tenant carrying no cap of its own, both leave the menu with no
 * quota entry rather than a number the response did not carry: the value zero
 * selects the deployment default, and no response reports what that default
 * resolved to. A negative value disables the cap, which the entry states. */
function LayerQuota() {
  const quota = useAsync(() => readQuota(), []);
  const cap = quota.value?.limits?.MaxUserLayers;
  if (cap === undefined || cap === 0) {
    return null;
  }
  return (
    <>
      <p className="label">Layer quota</p>
      <p className="mono quiet" data-testid="layer-quota">
        {cap < 0 ? 'no cap on your layers' : `${cap} user-defined layers`}
      </p>
    </>
  );
}

/** initialsOf is the avatar's mono label. A subject is an identifier rather
 * than a person's name, so the initials come off the identifier's own parts
 * and a subject that carries none falls back to its first character. The
 * label comes off whichever of the caller's email and subject the identity
 * cluster selected, and the rule holds for both. */
export function initialsOf(subject: string): string {
  const local = subject.split('@')[0];
  const parts = local.split(/[.\-_]/).filter((part) => part !== '');
  if (parts.length === 0) {
    return subject.slice(0, 1).toUpperCase();
  }
  return parts
    .slice(0, 2)
    .map((part) => part.slice(0, 1).toUpperCase())
    .join('');
}

/** Wordmark is the mark the design pass fixed, drawn inline: a filled disc
 * over a bar, beside the name. It ships no image asset, so it resolves from
 * the bundle like every other part of the page. */
function Wordmark() {
  return (
    <a className="wordmark" href={domainHref('')} aria-label="Podium">
      <svg className="wordmark-mark" viewBox="4 13 64 46" aria-hidden="true">
        <circle className="wordmark-mark-disc" cx="36" cy="28" r="15" />
        <rect className="wordmark-mark-bar" x="4" y="54" width="64" height="5" />
      </svg>
      <span className="wordmark-text">Podium</span>
    </a>
  );
}

function SignOutButton({ path }: { path: string }) {
  const [failed, setFailed] = useState(false);
  return (
    <>
      <button
        type="button"
        data-testid="sign-out"
        onClick={() => {
          // Sign-out is issued as a POST because that is the method the route
          // answers, and the page navigates once it returns.
          signOut(path).then(
            () => {
              window.location.assign('/app/');
            },
            () => {
              setFailed(true);
            },
          );
        }}
      >
        Sign out
      </button>
      {failed && <Banner tone="danger">The registry refused the sign-out and the session is unchanged.</Banner>}
    </>
  );
}
