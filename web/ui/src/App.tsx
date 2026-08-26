// The application shell. It takes the §7.3.4 posture read on load, renders
// the authentication control that read's posture calls for, and hosts the
// §13.10 surfaces: the domain browser, search, the artifact viewer, and the
// layer panel.

import { useCallback, useEffect, useRef, useState } from 'react';

import type { ReactNode } from 'react';

import { Banner, ErrorState, Loading, Magnifier, PageBanner } from './components/primitives';
import type { DomainDescriptor } from './api';
import {
  ApiError,
  catalogArtifactIDs,
  isIdentityRefusal,
  listLayers,
  loadDomain,
  readQuota,
  signOut,
  subscribeReadOnly,
} from './api';
import type { SessionPosture } from './session';
import { authControl, catalogScope, expiryControl, isSignedIn, readSession } from './session';
import { catalogDepth, domainLabel, marksCurrentDomain } from './domain';
import { useDismissalHeld } from './components/focus';
import { artifactDomain, domainHref, layersHref, searchHref, useRoute } from './route';
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
  const [theme, setTheme] = useTheme();
  // A dialog that refuses every dismissal route holds the page, and the
  // accelerator below is one of the routes it is refusing.
  const dismissalHeld = useDismissalHeld();

  useEffect(() => subscribeReadOnly(setReadOnly), []);

  useEffect(() => {
    // A dialog the reader can only leave by acknowledging it is showing
    // content that is gone once it unmounts, and the palette would cover it,
    // take focus, and navigate away from it on the first result opened. The
    // accelerator is withheld for as long as such a dialog is on the page.
    if (dismissalHeld) {
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
  }, [dismissalHeld]);

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

  // The footer counts. The layer list carries the layer count and the last
  // ingest each layer reports, and the catalog's artifact count is the length
  // of the unscoped catalog listing, which the registry does not truncate.
  // Neither depends on where the reader is, so the
  // route does not re-read them; a layer write does, through the panel's
  // catalog-change signal, because a register or an unregister moves the very
  // figures the footer states. That signal bumps the nonce, so the counts and
  // the sidebar tree are re-read from the one event: a write that adds or
  // removes a domain moves both, and refreshing only the counts left the tree
  // standing on the catalog the reader arrived with until the page reloaded.
  const counts = useAsync(() => readCounts(), [catalogNonce]);

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
        // either key, and the anonymous presentation is what it renders:
        // neither authentication control, and the layer panel with its write
        // operations.
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
    setCatalogNonce((nonce) => nonce + 1);
  }, []);

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
  const recovery = <AuthRecovery posture={posture} onRetry={retryCatalog} />;
  const catalogNodes = refused ? [] : (tree.value?.subdomains ?? []);
  // A catalog read that came back holding no domain is a state of its own,
  // distinct from the refused arm and from the read still being in flight.
  // Both of those also render no node, so the empty line is gated on a read
  // that returned rather than on the node list alone.
  const catalogEmpty = !refused && !tree.loading && tree.error === null && catalogNodes.length === 0;
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
      <TopBar
        posture={posture}
        theme={theme}
        onTheme={setTheme}
        onOpenPalette={() => {
          setPaletteOpen(true);
        }}
      />
      <CommandPalette
        open={paletteOpen}
        onClose={() => {
          setPaletteOpen(false);
        }}
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
              deployment. The nav reads no posture field and predicts no
              outcome the server decides. */}
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
          />
          {catalogEmpty && (
            <p className="quiet catalog-empty" data-testid="catalog-empty">
              The catalog holds no domains. Register a layer to fill it.
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
        <main className="content" id={contentID} tabIndex={-1}>
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
            <Surface
              route={route}
              subject={subject}
              readOnly={readOnly}
              onCatalogOutcome={onCatalogOutcome}
              onCatalogChange={reloadCatalog}
            />
          )}
        </main>
      </div>
    </div>
  );
}

function Surface({
  route,
  subject,
  readOnly,
  onCatalogOutcome,
  onCatalogChange,
}: {
  route: ReturnType<typeof useRoute>;
  subject: string;
  readOnly: boolean;
  onCatalogOutcome: (err: unknown) => void;
  onCatalogChange: () => void;
}) {
  switch (route.name) {
    case 'search':
      // The surface seeds its filter state from the query, so a fresh query
      // arriving from the palette while the surface is already open remounts
      // it rather than leaving the prior query's pills standing.
      return <SearchSurface key={route.query} query={route.query} onError={onCatalogOutcome} />;
    case 'artifact':
      return <ArtifactViewer id={route.id} onError={onCatalogOutcome} />;
    case 'layers':
      // A restore moves the same figures the sidebar footer states, so the
      // recovery surface reports it the way every other layer write does.
      return route.deleted ? (
        <DeletedLayers onRestored={onCatalogChange} readOnly={readOnly} />
      ) : (
        <LayerPanel subject={subject} readOnly={readOnly} onCatalogChange={onCatalogChange} />
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
 * withdraws the figures and says so, because these are read once for the page
 * and a figure left standing over a registry that stopped answering is
 * presented as its current state. */
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
}: {
  nodes: DomainDescriptor[];
  parent: string;
  current: string | null;
  currentIsPage: boolean;
  onOutcome: (err: unknown) => void;
  reach: number;
}) {
  const [all, setAll] = useState(false);
  // The reader's own position is never one of the folded rows: a level whose
  // current domain sits past the cap is drawn whole, because the row that
  // marks where the page sits is what the tree is for.
  const folded =
    !all &&
    nodes.length > siblingCap &&
    !nodes.slice(siblingCap).some((node) => onCurrentPath(node.path, current));
  const shown = folded ? nodes.slice(0, siblingCap) : nodes;

  return (
    <ul className="catalog-tree" aria-label="Catalog">
      {shown.map((node) => (
        <TreeNode
          key={node.path}
          node={node}
          parent={parent}
          current={current}
          currentIsPage={currentIsPage}
          onOutcome={onOutcome}
          reach={reach}
        />
      ))}
      {folded && (
        <li className="catalog-node">
          {/* The row states how many domains it is holding back and opens
              them in place, so the level is reachable from the tree rather
              than only from the domain page's subdomain list. */}
          <button
            type="button"
            className="catalog-more mono"
            onClick={() => {
              setAll(true);
            }}
          >
            + {nodes.length - siblingCap} more
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
}) {
  const ancestor = onCurrentPath(node.path, current);
  const [open, setOpen] = useState(ancestor);
  const [loaded, setLoaded] = useState<DomainDescriptor[] | null>(null);
  const [restricted, setRestricted] = useState(false);
  const [failed, setFailed] = useState(false);
  const eager = node.subdomains;
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
  const leaf = eager !== undefined && eager.length === 0;
  // A node whose own level came back empty is a leaf the reader discovered by
  // pressing the toggle, and that press is why the row keeps a control in the
  // slot rather than dropping it. Unmounting the button the reader is
  // standing on drops keyboard focus to the document body, which loses their
  // place in the tree, and with a pointer the triangle vanishes with no
  // stated outcome. The control stays in place, marked unavailable, and the
  // row states that the level holds no subdomains.
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
          // The glyph is the same character on every row, so the toggle takes
          // its name from the domain it opens. Without it a reader arriving by
          // keyboard or screen reader meets a run of identically named buttons
          // and cannot tell which level each one expands.
          //
          // The emptied node keeps the same button element, so React updates
          // it in place and the focus the reader put on it survives the level
          // resolving to nothing. It carries aria-disabled rather than
          // disabled for the same reason: a disabled control is removed from
          // the focus order, and the browser drops focus to the body.
          <button
            type="button"
            className={emptied ? 'tree-toggle tree-toggle-empty' : 'tree-toggle'}
            aria-expanded={emptied ? undefined : open}
            aria-disabled={emptied ? true : undefined}
            aria-label={
              emptied ? `${label} has no subdomains` : `${open ? 'Collapse' : 'Expand'} ${label}`
            }
            onClick={() => {
              if (!emptied) {
                setOpen(!open);
              }
            }}
          >
            {emptied ? '·' : open ? '▾' : '▸'}
          </button>
        )}
        {/* The label is the whole folded stretch of path the entry navigates
            across, and the row clips it to the sidebar's width, so it carries
            the label as its title for a reader whose row is too narrow. */}
        {restricted ? (
          <>
            <span className="mono" title={label}>
              {label}
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
            {label}
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
        {/* The marker is what an expansion that resolved to nothing produces
            on screen, and it is a status so the outcome reaches a reader who
            pressed the toggle and cannot see the row. */}
        {emptied && (
          <span
            className="catalog-marker"
            title="no subdomains"
            role="status"
            data-testid="empty-domain"
          >
            no subdomains
          </span>
        )}
      </div>
      {open && children !== null && children.length > 0 && (
        <CatalogTree
          nodes={children}
          parent={node.path}
          current={current}
          currentIsPage={currentIsPage}
          onOutcome={onOutcome}
          reach={reach}
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
  onOpenPalette,
}: {
  posture: SessionPosture | null;
  theme: ThemePreference;
  onTheme: (next: ThemePreference) => void;
  onOpenPalette: () => void;
}) {
  const control = authControl(posture);
  const subject = posture?.subject ?? '';
  return (
    <header className="topbar">
      <Wordmark />
      {/* The registry the page is served from. The bundle is served by the
          registry itself, so the origin names it and no response has to. */}
      <span className="mono topbar-host" data-testid="registry-host">
        {window.location.host}
      </span>
      <span className="spacer" />
      <button type="button" className="search-trigger" data-testid="search-trigger" onClick={onOpenPalette}>
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
          subject={subject}
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

/** AccountMenu is the identity cluster and the menu behind it. It carries the
 * caller's own subject, the appearance preference, the layer quota, and the
 * sign-out entry point where the deployment runs one. It carries no role
 * badge and no group membership: no response reports that the caller holds
 * the administrator role, and no response enumerates the caller's groups, so
 * the menu states neither. */
function AccountMenu({
  subject,
  theme,
  onTheme,
  signOutPath,
}: {
  subject: string;
  theme: ThemePreference;
  onTheme: (next: ThemePreference) => void;
  signOutPath: string | null;
}) {
  const [open, setOpen] = useState(false);
  return (
    <div className="account">
      <button
        type="button"
        className="account-trigger"
        data-testid="account-trigger"
        aria-expanded={open}
        onClick={() => {
          setOpen((prior) => !prior);
        }}
      >
        <span className="mono avatar" aria-hidden="true">
          {initialsOf(subject)}
        </span>
        <span className="mono subject">{subject}</span>
      </button>
      {open && (
        <div className="account-menu" role="menu" aria-label="Account" data-testid="account-menu">
          <p className="mono quiet">{subject}</p>
          <AppearanceSwitch theme={theme} onTheme={onTheme} />
          <LayerQuota />
          {signOutPath !== null && <SignOutButton path={signOutPath} />}
        </div>
      )}
    </div>
  );
}

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
        {(['system', 'light', 'dark'] as ThemePreference[]).map((choice) => (
          <button
            key={choice}
            type="button"
            className={theme === choice ? 'segment segment-on' : 'segment'}
            aria-pressed={theme === choice}
            onClick={() => {
              onTheme(choice);
            }}
          >
            {choice}
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
 * Spec: §13.10
 */
function AppearanceMenu({
  theme,
  onTheme,
}: {
  theme: ThemePreference;
  onTheme: (next: ThemePreference) => void;
}) {
  const [open, setOpen] = useState(false);
  return (
    <div className="account">
      <button
        type="button"
        className="account-trigger appearance-trigger"
        data-testid="appearance-trigger"
        aria-expanded={open}
        onClick={() => {
          setOpen((prior) => !prior);
        }}
      >
        <ContrastDisc />
        Appearance
      </button>
      {open && (
        <div className="account-menu" role="menu" aria-label="Appearance" data-testid="appearance-menu">
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
 * and a subject that carries none falls back to its first character. */
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
              window.location.assign('/ui/');
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
