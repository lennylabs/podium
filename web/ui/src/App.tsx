// The application shell. It takes the §7.3.4 posture read on load, renders
// the authentication control that read's posture calls for, and hosts the
// §13.10 surfaces: the domain browser, search, the artifact viewer, and the
// layer panel.

import { useCallback, useEffect, useState } from 'react';

import type { ReactNode } from 'react';

import { Banner, ErrorState, Loading, PageBanner } from './components/primitives';
import type { DomainDescriptor } from './api';
import {
  ApiError,
  isIdentityRefusal,
  listLayers,
  loadDomain,
  readQuota,
  searchArtifacts,
  signOut,
  subscribeReadOnly,
} from './api';
import type { SessionPosture } from './session';
import { authControl, catalogScope, expiryControl, isSignedIn, readSession } from './session';
import { domainLabel } from './domain';
import { domainHref, layersHref, searchHref, useRoute } from './route';
import { since } from './time';
import type { ThemePreference } from './theme';
import { useTheme } from './theme';
import { useAsync } from './useAsync';
import { CommandPalette } from './surfaces/CommandPalette';
import { DomainBrowser } from './surfaces/DomainBrowser';
import { SearchSurface } from './surfaces/SearchSurface';
import { ArtifactViewer } from './surfaces/ArtifactViewer';
import { LayerPanel } from './surfaces/LayerPanel';

/** treeDepth is how many levels of the domain hierarchy the sidebar tree
 * resolves eagerly. A level below that edge is read when the reader expands
 * the node it hangs under, so the shell holds the top of the hierarchy
 * without reading the whole of it. */
const treeDepth = 2;

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
  // The palette is reachable from every surface, so the shell owns whether it
  // is open and the whole page carries the accelerator that opens it.
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [theme, setTheme] = useTheme();

  useEffect(() => subscribeReadOnly(setReadOnly), []);

  useEffect(() => {
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
  }, []);

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
  // ingest each layer reports, and the catalog's artifact count is the match
  // count an unfiltered search reports, which the registry takes before it
  // truncates the result set. Neither depends on where the reader is, so the
  // route does not re-read them; a layer write does, through the panel's
  // catalog-change signal, because a register or an unregister moves the very
  // figures the footer states.
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

  const retryCatalog = useCallback(() => {
    // The shell owns the catalog read on the layers route, so the retry
    // re-issues it in place. Every other route's surface owns the read that
    // was refused, and reloading the document is what re-issues that one.
    if (route.name === 'layers') {
      setCatalogNonce((nonce) => nonce + 1);
      return;
    }
    window.location.reload();
  }, [route.name]);

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
          <a href={domainHref('')}>Browse</a>
          <a href={searchHref('')}>Search</a>
          {/* The layer panel is reachable for every caller on every
              deployment. The nav reads no posture field and predicts no
              outcome the server decides. */}
          <a href={layersHref}>Layers</a>
          <p className="catalog-label">
            <span className="label">Catalog</span>
            {/* The depth marker names how deep the sidebar resolves the tree
                rather than how deep the catalog runs, which no response
                reports. It is kept on the refused arm, because it states a
                property of this navigation rather than anything about what
                the catalog holds. It is dropped where the read returned no
                domain, because there the marker stands over nothing and
                describes a descent the reader cannot make. */}
            {!catalogEmpty && (
              <span className="label" data-testid="catalog-depth">
                {treeDepth} levels
              </span>
            )}
          </p>
          {/* The refused arm has no catalog to navigate, so the tree and the
              counts are empty rather than absent. */}
          <CatalogTree
            nodes={catalogNodes}
            parent=""
            current={route.name === 'domain' && route.path !== '' ? route.path : null}
            onOutcome={onCatalogOutcome}
          />
          {catalogEmpty && (
            <p className="quiet catalog-empty" data-testid="catalog-empty">
              The catalog holds no domains. Register a layer to fill it.
            </p>
          )}
          <div className="sidebar-footer">
            <CatalogCounts counts={refused ? null : counts.value} />
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
              onCatalogChange={counts.reload}
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
      return <LayerPanel subject={subject} readOnly={readOnly} onCatalogChange={onCatalogChange} />;
    case 'domain':
      return <DomainBrowser path={route.path} onError={onCatalogOutcome} />;
  }
}

/** CatalogTotals is what the sidebar footer states: how many layers the
 * tenant carries, how many artifacts its catalog matches, and when a layer
 * was last ingested. */
interface CatalogTotals {
  layers: number;
  artifacts: number;
  lastIngest: string;
}

async function readCounts(): Promise<CatalogTotals> {
  const [layers, search] = await Promise.all([
    listLayers(),
    // The match count is taken before the result set is truncated, so a
    // search carrying no query and no filter reports the catalog's own
    // artifact count and one result is enough to ask for.
    searchArtifacts({ query: '', type: '', scope: '', tags: [] }, 1),
  ]);
  return {
    layers: layers.length,
    artifacts: search.total_matched,
    lastIngest: layers.reduce((latest, layer) => {
      const at = layer.last_ingested_at ?? '';
      return at > latest ? at : latest;
    }, ''),
  };
}

/** CatalogCounts is the footer pinned to the bottom of the sidebar. It states
 * what the reads returned and nothing else: a read that has not answered, and
 * the refused arm, leave it standing with no counts in it rather than
 * reporting a figure no response carried. */
function CatalogCounts({ counts }: { counts: CatalogTotals | null }) {
  if (counts === null) {
    return <p className="mono quiet" data-testid="catalog-counts" />;
  }
  return (
    <>
      <p className="mono quiet" data-testid="catalog-counts">
        {counts.layers} layers · {counts.artifacts} artifacts
      </p>
      <p className="mono quiet" data-testid="catalog-ingest">
        {counts.lastIngest === '' ? 'never ingested' : `ingested ${since(counts.lastIngest, Date.now())}`}
      </p>
    </>
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
 * `current` is the domain the page is showing, or null on a route that is not
 * a domain. The tree resolves the ancestry down to it and marks it, so a
 * reader who arrived by a link or a breadcrumb sees where in the hierarchy
 * the page sits instead of a row of collapsed roots. */
function CatalogTree({
  nodes,
  parent,
  current,
  onOutcome,
}: {
  nodes: DomainDescriptor[];
  parent: string;
  current: string | null;
  onOutcome: (err: unknown) => void;
}) {
  return (
    <ul className="catalog-tree" aria-label="Catalog">
      {nodes.map((node) => (
        <TreeNode key={node.path} node={node} parent={parent} current={current} onOutcome={onOutcome} />
      ))}
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
 * not load, and a later expansion re-issues the read. */
function TreeNode({
  node,
  parent,
  current,
  onOutcome,
}: {
  node: DomainDescriptor;
  parent: string;
  current: string | null;
  onOutcome: (err: unknown) => void;
}) {
  const ancestor = onCurrentPath(node.path, current);
  const [open, setOpen] = useState(ancestor);
  const [loaded, setLoaded] = useState<DomainDescriptor[] | null>(null);
  const [restricted, setRestricted] = useState(false);
  const [failed, setFailed] = useState(false);
  const eager = node.subdomains;
  const label = domainLabel(node.path, parent);
  const children = eager ?? loaded;
  const isCurrent = node.path === current;

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
  // again the next time the node opens.
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
  }, [open, eager, loaded, restricted, node.path, onOutcome]);

  return (
    <li className="catalog-node">
      {/* The row is its own element so the current domain's fill stops at the
          row rather than running down the nested level under it. */}
      <div className={isCurrent ? 'catalog-row catalog-row-current' : 'catalog-row'}>
        <button type="button" className="tree-toggle" aria-expanded={open} onClick={() => setOpen(!open)}>
          {open ? '▾' : '▸'}
        </button>
        {restricted ? (
          <>
            <span className="mono">{label}</span>
            <span className="label" data-testid="restricted-domain">
              restricted
            </span>
          </>
        ) : (
          <a className="mono" href={domainHref(node.path)} aria-current={isCurrent ? 'page' : undefined}>
            {label}
          </a>
        )}
        {/* The failed arm states that this level did not load and claims
            nothing about what the caller may see. Expanding the node again is
            what retries it. */}
        {failed && (
          <span className="label" data-testid="unavailable-domain">
            did not load
          </span>
        )}
      </div>
      {open && children !== null && children.length > 0 && (
        <CatalogTree nodes={children} parent={node.path} current={current} onOutcome={onOutcome} />
      )}
      {/* A node the reader expanded onto an empty level states that the level
          is empty. Drawing nothing there leaves the press with no outcome on
          screen and reads as an expansion that failed, which is the one thing
          the row is not. */}
      {open && children !== null && children.length === 0 && (
        <p className="catalog-empty quiet" data-testid="empty-domain">
          No subdomains.
        </p>
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
        <span aria-hidden="true">⌕</span>
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
          the menu without it. */}
      {subject !== '' && (
        <AccountMenu
          subject={subject}
          theme={theme}
          onTheme={onTheme}
          signOutPath={control.kind === 'sign-out' ? control.path : null}
        />
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
          <p className="label">Appearance</p>
          <div className="view-toggle" role="group" aria-label="Appearance">
            {(['system', 'light', 'dark'] as ThemePreference[]).map((choice) => (
              <button
                key={choice}
                type="button"
                className={theme === choice ? 'toggle toggle-open' : 'toggle'}
                aria-pressed={theme === choice}
                onClick={() => {
                  onTheme(choice);
                }}
              >
                {choice}
              </button>
            ))}
          </div>
          <LayerQuota />
          {signOutPath !== null && <SignOutButton path={signOutPath} />}
        </div>
      )}
    </div>
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
