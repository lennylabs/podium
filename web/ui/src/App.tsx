// The application shell. It takes the §7.3.4 posture read on load, renders
// the authentication control that read's posture calls for, and hosts the
// §13.10 surfaces: the domain browser, search, the artifact viewer, and the
// layer panel.

import { useCallback, useEffect, useState } from 'react';

import { Banner, ErrorState, Loading } from './components/primitives';
import { isIdentityRefusal, loadDomain, signOut, subscribeReadOnly } from './api';
import type { SessionPosture } from './session';
import { authControl, catalogScope, expiryControl, readSession } from './session';
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
  }, [route.name]);

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

  if (!postureLoaded) {
    return <Loading label="Loading." />;
  }

  const refused = isIdentityRefusal(catalogError);
  const scope = catalogScope(posture, refused);
  const subject = posture?.subject ?? '';

  return (
    <div className="app">
      <TopBar posture={posture} />
      <div className="app-body">
        <nav className="sidebar" aria-label="Sections">
          <a href={domainHref('')}>Browse</a>
          <a href={searchHref('')}>Search</a>
          {/* The layer panel is reachable for every caller on every
              deployment. The nav reads no posture field and predicts no
              outcome the server decides. */}
          <a href={layersHref}>Layers</a>
          {scope === 'public-subset' && subject === '' && <p className="quiet footer-note">Not signed in</p>}
        </nav>
        <main className="content">
          {refused && route.name !== 'layers' ? (
            <RefusedCatalog error={catalogError} posture={posture} />
          ) : (
            <Surface
              route={route}
              subject={subject}
              posture={posture}
              readOnly={readOnly}
              sessionEnded={refused}
              onCatalogOutcome={onCatalogOutcome}
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
  posture,
  readOnly,
  sessionEnded,
  onCatalogOutcome,
}: {
  route: ReturnType<typeof useRoute>;
  subject: string;
  posture: SessionPosture | null;
  readOnly: boolean;
  sessionEnded: boolean;
  onCatalogOutcome: (err: unknown) => void;
}) {
  switch (route.name) {
    case 'search':
      return <SearchSurface query={route.query} onError={onCatalogOutcome} />;
    case 'artifact':
      return <ArtifactViewer id={route.id} onError={onCatalogOutcome} />;
    case 'layers':
      return (
        <LayerPanel
          subject={subject}
          readOnly={readOnly}
          sessionEnded={sessionEnded}
          recovery={<AuthRecovery posture={posture} />}
        />
      );
    case 'domain':
      return <DomainBrowser path={route.path} onError={onCatalogOutcome} />;
  }
}

/** RefusedCatalog is the arm of the catalog-scope rule the page renders where
 * a catalog read was refused because the caller's identity could not be
 * verified. Such a caller has no anonymous view of the catalog, so the page
 * renders this in place of the catalog rather than an empty or a filtered
 * one. It states that the registry did not serve this catalog to this caller
 * and says nothing about what the catalog holds. */
function RefusedCatalog({ error, posture }: { error: unknown; posture: SessionPosture | null }) {
  return (
    <section className="surface" aria-label="Catalog refused">
      <h1>This catalog was not served to you</h1>
      <p>The registry did not verify an identity for this request, so it served no catalog.</p>
      <AuthRecovery posture={posture} />
      <ErrorState
        error={error}
        onRetry={() => {
          window.location.reload();
        }}
      />
    </section>
  );
}

/** AuthRecovery is the control the refused-catalog arm and the panel's
 * session-ended treatment offer the caller. What it may be is bounded by the
 * sign-in control rule's third row: a deployment reporting the browser flow
 * disabled renders no authentication control on any value of subject, so on
 * such a deployment this states what the caller has in its place instead of
 * offering a sign-in the mux does not serve. */
function AuthRecovery({ posture }: { posture: SessionPosture | null }) {
  const control = expiryControl(posture);
  if (control.kind !== 'sign-in') {
    return (
      <p className="quiet">
        This registry runs no browser sign-in. Retry the read once the credential it reads is in place again, or ask
        the operator who runs it.
      </p>
    );
  }
  return (
    <a className="button primary" data-testid="expiry-sign-in" href={control.path}>
      Sign in
    </a>
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
