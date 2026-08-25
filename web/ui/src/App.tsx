// The application shell. It takes the §7.3.4 posture read on load, renders
// the authentication control that read's posture calls for, and hosts the
// §13.10 surfaces: the domain browser, search, the artifact viewer, and the
// layer panel.

import { useCallback, useEffect, useState } from 'react';

import type { ReactNode } from 'react';

import { Banner, ErrorState, Loading, PageBanner } from './components/primitives';
import { isIdentityRefusal, loadDomain, signOut, subscribeReadOnly } from './api';
import type { SessionPosture } from './session';
import { authControl, catalogScope, expiryControl, isSignedIn, readSession } from './session';
import { domainHref, layersHref, searchHref, useRoute } from './route';
import { DomainBrowser } from './surfaces/DomainBrowser';
import { SearchSurface } from './surfaces/SearchSurface';
import { ArtifactViewer } from './surfaces/ArtifactViewer';
import { LayerPanel } from './surfaces/LayerPanel';

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

  useEffect(() => subscribeReadOnly(setReadOnly), []);

  // The catalog read is the panel's expiry signal, and the layers route
  // issues no catalog read of its own, so the shell takes one while that
  // route is active. A layer write's refusal carries no session information,
  // so without this read the panel would present each refusal as the only
  // signal and would never learn that the session ended.
  useEffect(() => {
    if (route.name !== 'layers') {
      return;
    }
    let live = true;
    loadDomain('').then(
      () => {
        if (live) {
          setCatalogError(null);
        }
      },
      (err: unknown) => {
        if (live) {
          setCatalogError(err);
        }
      },
    );
    return () => {
      live = false;
    };
  }, [route.name, catalogNonce]);

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

  // The public-subset arm of the catalog-scope rule carries two pieces. The
  // sidebar footer states that the caller is not signed in, and this banner
  // states the same across the page. Neither claims anything about content
  // beyond what the read returned, and the banner carries no control of its
  // own, because the authentication control belongs to the shell.
  const anonymous = scope === 'public-subset' && subject === '';

  return (
    <div className="app">
      <TopBar posture={posture} />
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
          {anonymous && <p className="quiet footer-note">Not signed in</p>}
        </nav>
        <main className="content">
          {/* The expiry transition is rendered over the page the caller was
              on, which is kept rather than cleared, so it sits above the
              surface on every route. */}
          {expired && <SessionEnded recovery={recovery} />}
          {refused && !expired && route.name === 'layers' && <RefusedRead onRetry={retryCatalog} />}
          {refused && route.name !== 'layers' ? (
            <RefusedCatalog error={catalogError} recovery={expired ? null : recovery} />
          ) : (
            <Surface route={route} subject={subject} readOnly={readOnly} onCatalogOutcome={onCatalogOutcome} />
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
}: {
  route: ReturnType<typeof useRoute>;
  subject: string;
  readOnly: boolean;
  onCatalogOutcome: (err: unknown) => void;
}) {
  switch (route.name) {
    case 'search':
      return <SearchSurface query={route.query} onError={onCatalogOutcome} />;
    case 'artifact':
      return <ArtifactViewer id={route.id} onError={onCatalogOutcome} />;
    case 'layers':
      return <LayerPanel subject={subject} readOnly={readOnly} />;
    case 'domain':
      return <DomainBrowser path={route.path} onError={onCatalogOutcome} />;
  }
}

/** SessionEnded is the expiry transition. It is rendered for a caller whose
 * posture read resolved a subject and whose catalog read was then refused,
 * because that pair is what marks a session that ended while the page was
 * open. The page underneath is kept, and the control beside the sentence is
 * whatever the deployment's posture licenses. */
function SessionEnded({ recovery }: { recovery: ReactNode }) {
  return (
    <div className="banner banner-danger" role="alert" data-testid="session-ended">
      <p className="banner-title">Your session has ended. Sign in again to continue.</p>
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
 * and says nothing about what the catalog holds. The recovery control is null
 * where the expiry treatment above already carries it, so the page offers one
 * control rather than two. */
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

/** TopBar carries the one authentication control. The sign-in control rule
 * keys it on the posture read's browser_auth.enabled and subject, and both
 * conjuncts are required on each control: a deployment running no browser
 * flow renders neither on any value of subject, which covers the
 * gateway-fronted deployment where a subject resolves because the gateway
 * authenticated the request. Each path comes from the read rather than from a
 * literal in this bundle. */
function TopBar({ posture }: { posture: SessionPosture | null }) {
  const control = authControl(posture);
  return (
    <header className="topbar">
      <span className="wordmark">Podium</span>
      <span className="spacer" />
      {posture?.subject !== undefined && <span className="mono subject">{posture.subject}</span>}
      {control.kind === 'sign-in' && (
        <a className="button primary" data-testid="sign-in" href={control.path}>
          Sign in
        </a>
      )}
      {control.kind === 'sign-out' && <SignOutButton path={control.path} />}
    </header>
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
