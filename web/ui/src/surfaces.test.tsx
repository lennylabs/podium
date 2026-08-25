// The Surfaces case set. It covers the browser-driven cells of the §11
// verification matrix: each case drives a surface through the UI's own API
// calls against a stubbed registry rather than through a constructed request,
// and asserts what the page renders.
//
// The posture read's cells are driven here as well, one case per row of the
// sign-in control table plus a case for a read that fails. Two further cases
// pin the posture-keyed rendering rules the design brief states: the layer
// panel renders for a caller who resolves no subject, and the anonymous view
// under public mode is the whole catalog rather than a filtered one.

import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { App } from './App';
import type { SessionPosture } from './session';

/** Stub is one registry response: the status and the JSON body a path
 * answers with. */
interface Stub {
  status?: number;
  body?: unknown;
  /** text is the response for a path that answers with a document rather
   * than with JSON, which is what the presigned manifest-body URL returns. */
  text?: string;
  /** headers are the response headers the page reads, which is where the
   * §13.2.1 read-only marker arrives. */
  headers?: Record<string, string>;
}

interface Recorded {
  url: string;
  method: string;
}

const requests: Recorded[] = [];
/** bodies records the request bodies the page sent, so a case can assert what
 * a write carried rather than only that it fired. */
const bodies: string[] = [];

/** stubRegistry installs the registry the page reads. A path with no stub
 * answers 404, so a case that drives a call it did not stub fails on the
 * surface's own error state rather than on a silent empty response. */
function stubRegistry(stubs: Record<string, Stub>): void {
  vi.stubGlobal(
    'fetch',
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      requests.push({ url, method });
      if (typeof init?.body === 'string') {
        bodies.push(init.body);
      }
      const path = url.split('?')[0];
      // A path a surface both reads and writes takes a method-qualified key
      // where the two answer differently, and the bare path otherwise.
      const stub = stubs[`${method} ${path}`] ??
        stubs[path] ?? { status: 404, body: { code: 'registry.not_found', message: 'no stub' } };
      const status = stub.status ?? 200;
      return Promise.resolve(
        new Response(stub.text ?? JSON.stringify(stub.body ?? {}), {
          status,
          headers: {
            'content-type': stub.text === undefined ? 'application/json' : 'text/markdown',
            ...stub.headers,
          },
        }),
      );
    }),
  );
}

function posture(overrides: Partial<SessionPosture> = {}): SessionPosture {
  return {
    identity_provider_configured: true,
    public_mode: false,
    browser_auth: { enabled: false },
    ...overrides,
  };
}

/** manifestDoc is what load_artifact returns under its frontmatter field:
 * the ARTIFACT.md document, delimiter fences and all. */
const manifestDoc = '---\nname: review\ntags:\n  - security\n---\n';

const emptyDomain = {
  path: '',
  subdomains: [],
  notable: [],
};

function goTo(hash: string): void {
  window.location.hash = hash;
}

beforeEach(() => {
  requests.length = 0;
  bodies.length = 0;
  goTo('#/');
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('the sign-in control', () => {
  // Row one of the sign-in control table: the flow enabled with no subject
  // renders a sign-in navigation to the path the read reports.
  it('navigates to the read’s sign_in_path where the flow is enabled and no subject resolves', async () => {
    stubRegistry({
      '/v1/ui/session': {
        body: posture({
          browser_auth: { enabled: true, sign_in_path: '/v1/ui/auth/sign-in', sign_out_path: '/v1/ui/auth/sign-out' },
        }),
      },
      '/v1/load_domain': { body: emptyDomain },
    });
    render(<App />);
    const control = await screen.findByTestId('sign-in');
    expect(control.getAttribute('href')).toBe('/v1/ui/auth/sign-in');
    expect(screen.queryByTestId('sign-out')).toBeNull();
  });

  // Row two: the flow enabled with a subject renders sign-out, issued as a
  // POST to the path the read reports.
  it('issues sign-out as a POST to the read’s sign_out_path where a subject resolves', async () => {
    stubRegistry({
      '/v1/ui/session': {
        body: posture({
          subject: 'alice@acme.com',
          browser_auth: { enabled: true, sign_in_path: '/v1/ui/auth/sign-in', sign_out_path: '/v1/ui/auth/sign-out' },
        }),
      },
      '/v1/load_domain': { body: emptyDomain },
      '/v1/ui/auth/sign-out': { body: {} },
    });
    render(<App />);
    const control = await screen.findByTestId('sign-out');
    expect(screen.queryByTestId('sign-in')).toBeNull();
    fireEvent.click(control);
    await waitFor(() => {
      expect(requests).toContainEqual({ url: '/v1/ui/auth/sign-out', method: 'POST' });
    });
  });

  // Row three, driven with a subject present, which is the gateway-fronted
  // arrangement: a deployment running no browser flow renders neither
  // control on any value of subject. Clearing a Podium cookie would not end
  // the gateway's own session there.
  it('renders neither control where the flow is disabled and a subject resolves', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ subject: 'alice@acme.com' }) },
      '/v1/load_domain': { body: emptyDomain },
    });
    render(<App />);
    await screen.findByLabelText('Domain browser');
    expect(screen.queryByTestId('sign-in')).toBeNull();
    expect(screen.queryByTestId('sign-out')).toBeNull();
  });

  // A read that does not answer leaves the page holding no value for either
  // key, so it renders the anonymous presentation: neither control, and the
  // layer panel with its write operations.
  it('renders the anonymous presentation where the posture read fails', async () => {
    stubRegistry({
      '/v1/ui/session': { status: 503, body: { code: 'registry.unavailable', message: 'down' } },
      '/v1/layers': { body: { layers: [userLayer()] } },
    });
    goTo('#/layers');
    render(<App />);
    await screen.findByLabelText('Layer panel');
    expect(screen.queryByTestId('sign-in')).toBeNull();
    expect(screen.queryByTestId('sign-out')).toBeNull();
    expect(screen.getAllByRole('button', { name: 'Reingest' }).length).toBe(1);
    expect(screen.queryByText('yours')).toBeNull();
  });
});

describe('the catalog-scope rule', () => {
  // The whole-catalog arm. A registry engaging public mode serves its whole
  // catalog to a caller who resolves no subject, so the page presents what
  // the read returned and carries no public-view framing. An implementation
  // that filtered the anonymous view to public artifacts, or framed it as
  // one, fails here.
  it('presents the whole catalog under public mode with no public-view framing', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ public_mode: true }) },
      '/v1/load_domain': {
        body: {
          path: '',
          subdomains: [],
          notable: [{ id: 'security/internal-review', type: 'skill', version: '1.0.0' }],
        },
      },
    });
    render(<App />);
    expect(await screen.findByText('security/internal-review')).toBeTruthy();
    expect(screen.queryByText('Not signed in')).toBeNull();
  });

  // The public-subset arm carries its framing and states nothing about what
  // was withheld.
  it('frames the anonymous view without asserting that anything was withheld', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture() },
      '/v1/load_domain': { body: emptyDomain },
    });
    render(<App />);
    expect(await screen.findByText('Not signed in')).toBeTruthy();
    expect(screen.queryByText(/hidden/i)).toBeNull();
    expect(screen.queryByText(/withheld/i)).toBeNull();
  });

  // The refused arm, ordered ahead of the other two. A registry whose
  // identity provider verifies a runtime-signed token refuses every catalog
  // call from a browser that holds none, and that caller has no anonymous
  // view of the catalog at all.
  it('renders the refused state rather than an empty or filtered catalog', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture() },
      '/v1/load_domain': { status: 401, body: { code: 'auth.untrusted_token', message: 'not verified' } },
    });
    render(<App />);
    await screen.findByLabelText('Catalog refused');
    expect(screen.queryByLabelText('Domain browser')).toBeNull();
    expect(screen.getByText('auth.untrusted_token')).toBeTruthy();
  });
});

describe('the domain browser', () => {
  it('renders the subdomains, the direct artifacts, and the lifted ones apart', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ public_mode: true, subject: 'alice@acme.com' }) },
      '/v1/load_domain': {
        body: {
          path: 'platform',
          description: 'Platform engineering.',
          keywords: ['infra'],
          subdomains: [{ path: 'platform/ci', name: 'ci', description: 'Pipelines.' }],
          notable: [
            { id: 'platform/deploy', type: 'skill', version: '2.0.0', source: 'featured' },
            { id: 'platform/ci/lint', type: 'skill', folded_from: 'ci' },
          ],
          note: 'The listing was trimmed to fit the response budget.',
        },
      },
    });
    goTo('#/domain/platform');
    render(<App />);
    await screen.findByLabelText('Domain browser');
    expect(screen.getByText('ci')).toBeTruthy();
    expect(screen.getByText('platform/deploy')).toBeTruthy();
    expect(screen.getByText('curated')).toBeTruthy();
    expect(screen.getByText('Lifted from sparse subdomains')).toBeTruthy();
    expect(screen.getByText('The listing was trimmed to fit the response budget.')).toBeTruthy();
  });

  // The §6.10 envelope says whether the condition clears on its own. Where it
  // says the condition does not, offering a retry sends the reader round a
  // loop that ends the same way, so the state says so instead.
  it('offers no retry of a read the envelope reports as not clearing on its own', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ public_mode: true }) },
      '/v1/load_domain': {
        status: 400,
        body: { code: 'registry.invalid_argument', message: 'no such domain', retryable: false },
      },
    });
    render(<App />);
    expect(await screen.findByTestId('not-retryable')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Try again' })).toBeNull();
    expect(screen.getByText('registry.invalid_argument')).toBeTruthy();
  });

  it('renders a domain that carries neither subdomains nor artifacts as a finished page', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ public_mode: true }) },
      '/v1/load_domain': { body: emptyDomain },
    });
    render(<App />);
    await screen.findByLabelText('Domain browser');
    expect(screen.getByText('This domain has no subdomains.')).toBeTruthy();
    expect(screen.getByText('This domain lists no artifacts.')).toBeTruthy();
  });
});

describe('search', () => {
  it('carries the type, scope, and tag filters on the request it issues', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ public_mode: true }) },
      '/v1/search_artifacts': { body: { total_matched: 0 } },
    });
    goTo('#/search/review');
    render(<App />);
    await screen.findByLabelText('Search');
    fireEvent.change(screen.getByLabelText('Type'), { target: { value: 'skill' } });
    fireEvent.change(screen.getByLabelText('Scope'), { target: { value: 'platform' } });
    fireEvent.change(screen.getByLabelText('Tags'), { target: { value: 'review, security' } });
    await waitFor(() => {
      const last = requests.filter((r) => r.url.startsWith('/v1/search_artifacts')).at(-1)?.url ?? '';
      const query = new URLSearchParams(last.split('?')[1] ?? '');
      expect(query.get('query')).toBe('review');
      expect(query.get('type')).toBe('skill');
      expect(query.get('scope')).toBe('platform');
      expect(query.get('tags')).toBe('review,security');
    });
  });

  // The match count is taken before the cap truncates the list, so fewer
  // results than matches is the ordinary outcome and reads as one. The two
  // optional result fields are driven present and absent in the same case.
  it('reports the returned count against the match count and labels a vector-only match', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ public_mode: true }) },
      '/v1/search_artifacts': {
        body: {
          query: 'review',
          total_matched: 143,
          results: [
            { id: 'platform/review', type: 'skill', score: 8.5, sensitivity: 'internal' },
            { id: 'platform/weaker', type: 'skill', score: 2.1 },
            { id: 'platform/meaning', type: 'skill', score: 0 },
          ],
        },
      },
    });
    goTo('#/search/review');
    render(<App />);
    expect((await screen.findByTestId('result-count')).textContent).toBe('Showing 3 of 143');
    expect(screen.getByText('internal')).toBeTruthy();
    expect(screen.getByText('matched by meaning')).toBeTruthy();
    // Relevance is drawn as bars ranked against the strongest score in the
    // set, and no row states a score. The vector-only row draws no bars and
    // still occupies the column, so the rows stay aligned.
    const indicators = screen.getAllByTestId('relevance-bars');
    expect(indicators.map((el) => el.getAttribute('data-filled'))).toEqual(['4', '1', '0']);
    expect(indicators[2].childElementCount).toBe(0);
    expect(screen.queryByText(/score 8/)).toBeNull();
  });

  it('renders a search that matched nothing as an empty result rather than a failure', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ public_mode: true }) },
      '/v1/search_artifacts': { body: { total_matched: 0 } },
    });
    goTo('#/search/nothing');
    render(<App />);
    expect(await screen.findByText('Nothing matched. Widen the query or clear a filter.')).toBeTruthy();
  });
});

describe('the artifact viewer', () => {
  it('renders the body as a document, the frontmatter as a property table, and the graph edges as links', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ public_mode: true }) },
      '/v1/load_artifact': {
        body: {
          id: 'platform/review',
          type: 'skill',
          version: '1.2.0',
          content_hash: 'sha256:abc',
          manifest_body: '# Review\n\nRun the checklist.\n',
          // The frontmatter field carries the whole ARTIFACT.md document,
          // fences and prose body included, which is what the endpoint
          // returns. A viewer that hands the field straight to the YAML
          // parser reaches the invalid-syntax arm on every real artifact.
          frontmatter: manifestDoc,
          skill_raw: `${manifestDoc}\nAuthored skill body.\n`,
          layer: 'platform',
        },
      },
      '/v1/dependents': { body: { edges: [{ from: 'platform/review-strict', to: 'platform/review', kind: 'extends' }] } },
    });
    goTo('#/artifact/platform%2Freview');
    render(<App />);
    await screen.findByLabelText('Artifact viewer');
    expect(screen.getByTestId('artifact-body').querySelector('h1')?.textContent).toBe('Review');
    const table = screen.getByTestId('frontmatter-table');
    expect(table.textContent).toContain('name');
    expect(table.textContent).toContain('security');
    expect(screen.queryByText('Invalid syntax')).toBeNull();
    // The authored skill file is populated on a skill artifact, so the
    // viewer carries it.
    expect(screen.getByLabelText('Authored source').textContent).toContain('Authored skill body.');
    const relation = await screen.findByText('platform/review-strict');
    expect(relation.getAttribute('href')).toBe('#/artifact/platform%2Freview-strict');
  });

  // The presigned channel delivers the canonical manifest document rather
  // than a body, and the response clears the field that document
  // duplicates. A viewer that hands the fetched document to the rendering
  // path renders the frontmatter as markdown, where the fences become rules
  // and the keys become prose, while the property table reports no pairs.
  it('reconstitutes a manifest delivered by presigned URL rather than rendering the document', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ public_mode: true }) },
      '/v1/load_artifact': {
        body: {
          id: 'platform/review',
          type: 'context',
          version: '1.0.0',
          content_hash: 'sha256:abc',
          manifest_body: '',
          frontmatter: '',
          manifest_body_url: { presigned_url: 'https://objects.acme.com/abc', content_hash: 'sha256:abc', size: 900 },
        },
      },
      'https://objects.acme.com/abc': { text: `${manifestDoc}\n# Review\n\nRun the checklist.\n` },
      '/v1/dependents': { body: { edges: [] } },
    });
    goTo('#/artifact/platform%2Freview');
    render(<App />);
    await screen.findByLabelText('Artifact viewer');
    const rendered = await screen.findByTestId('artifact-body');
    expect(rendered.querySelector('h1')?.textContent).toBe('Review');
    expect(rendered.querySelector('hr')).toBeNull();
    expect(rendered.textContent).not.toContain('name: review');
    const table = screen.getByTestId('frontmatter-table');
    expect(table.textContent).toContain('name');
    expect(table.textContent).toContain('security');
    expect(screen.queryByText('No frontmatter on this artifact.')).toBeNull();
  });

  it('carries no authored-source view on an artifact that has none', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ public_mode: true }) },
      '/v1/load_artifact': {
        body: {
          id: 'platform/notes',
          type: 'context',
          version: '1.0.0',
          content_hash: 'sha256:abc',
          manifest_body: 'Body.\n',
          frontmatter: manifestDoc,
        },
      },
      '/v1/dependents': { body: { edges: [] } },
    });
    goTo('#/artifact/platform%2Fnotes');
    render(<App />);
    await screen.findByLabelText('Artifact viewer');
    expect(screen.queryByLabelText('Authored source')).toBeNull();
  });

  it('omits the property table where the response yields no frontmatter pairs', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ public_mode: true }) },
      '/v1/load_artifact': {
        body: {
          id: 'platform/review',
          type: 'context',
          version: '1.0.0',
          content_hash: 'sha256:abc',
          manifest_body: 'Body.\n',
          frontmatter: '',
        },
      },
      '/v1/dependents': { body: { edges: [] } },
    });
    goTo('#/artifact/platform%2Freview');
    render(<App />);
    await screen.findByLabelText('Artifact viewer');
    expect(screen.queryByTestId('frontmatter-table')).toBeNull();
    expect(screen.getByText('No frontmatter on this artifact.')).toBeTruthy();
    expect(await screen.findByText('Nothing extends or depends on this artifact.')).toBeTruthy();
  });

  it('reports a frontmatter block that does not parse without affecting the rest of the viewer', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ public_mode: true }) },
      '/v1/load_artifact': {
        body: {
          id: 'platform/review',
          type: 'skill',
          version: '1.0.0',
          content_hash: 'sha256:abc',
          manifest_body: '# Review\n',
          frontmatter: 'name: [unterminated\n',
        },
      },
      '/v1/dependents': { body: { edges: [] } },
    });
    goTo('#/artifact/platform%2Freview');
    render(<App />);
    await screen.findByLabelText('Artifact viewer');
    expect(screen.getByText('Invalid syntax')).toBeTruthy();
    expect(screen.getByTestId('artifact-body').querySelector('h1')?.textContent).toBe('Review');
  });
});

describe('the layer panel', () => {
  // The panel renders for a caller who resolves no subject on a registry
  // that configures no identity provider, which is the standalone deployment
  // where nobody authenticates and the panel is the point. An
  // implementation that hides the panel from an anonymous caller fails here.
  it('renders its write operations for a caller who resolves no subject', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ identity_provider_configured: false }) },
      '/v1/layers': { body: { layers: [adminLayer(), userLayer()] } },
    });
    goTo('#/layers');
    render(<App />);
    await screen.findByLabelText('Layer panel');
    expect(screen.getAllByRole('button', { name: 'Reingest' }).length).toBe(2);
    expect(screen.getAllByRole('button', { name: 'Unregister' }).length).toBe(2);
    expect(screen.queryByText('yours')).toBeNull();
  });

  // The ownership marker is a property of a user-defined row alone. An
  // admin-defined row carries none on any value of its stored owner, because
  // that owner is a caller-supplied field naming no authorized subject.
  it('marks a user-defined row the caller owns and marks no admin-defined row', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ subject: 'alice@acme.com' }) },
      '/v1/layers': { body: { layers: [adminLayer('alice@acme.com'), userLayer('alice@acme.com')] } },
    });
    goTo('#/layers');
    render(<App />);
    await screen.findByLabelText('Layer panel');
    expect(screen.getAllByText('yours').length).toBe(1);
    expect(screen.getByText('admin-defined')).toBeTruthy();
  });

  // A write can come back refused, including on a row the panel presented as
  // the caller's to manage. The refusal is drawn on the row, says only that
  // the action was refused and nothing changed, and leaves every other
  // control live.
  it('presents a refused write on the row without reporting ownership or session state', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ subject: 'alice@acme.com' }) },
      '/v1/layers': { body: { layers: [userLayer('bob@acme.com')] } },
      '/v1/layers/reingest': { status: 403, body: { code: 'auth.forbidden', message: 'not permitted' } },
    });
    goTo('#/layers');
    render(<App />);
    await screen.findByLabelText('Layer panel');
    fireEvent.click(screen.getByRole('button', { name: 'Reingest' }));
    expect(await screen.findByText(/nothing changed/)).toBeTruthy();
    expect(screen.getByText('auth.forbidden')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Unregister' }).hasAttribute('disabled')).toBe(false);
  });

  it('renders one marker per matching visibility axis and summarises an axis that overflows', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ public_mode: true }) },
      '/v1/layers': {
        body: {
          layers: [
            {
              ID: 'shared',
              SourceType: 'git',
              Repo: 'git@github.com:acme/shared.git',
              Ref: 'main',
              Order: 1,
              Public: true,
              Organization: true,
              Groups: ['secops', 'appsec', 'platform', 'data'],
              Users: ['carol@acme.com'],
            },
          ],
        },
      },
    });
    goTo('#/layers');
    render(<App />);
    await screen.findByLabelText('Layer panel');
    expect(screen.getByText('public')).toBeTruthy();
    expect(screen.getByText('organization')).toBeTruthy();
    expect(screen.getByText('groups: secops · appsec +2')).toBeTruthy();
    expect(screen.getByText('users: carol@acme.com')).toBeTruthy();
  });
});

describe('the session-expiry transition', () => {
  // The catalog read is the expiry signal, and the treatment's control is
  // bounded by the sign-in control table: on a deployment running the
  // browser flow it is a navigation to the read's own sign_in_path. The
  // caller held a subject when the page loaded, which is what makes this the
  // expiry transition rather than the anonymous refused arm.
  it('offers the read’s sign-in path where a catalog read is refused mid-session', async () => {
    stubRegistry({
      '/v1/ui/session': {
        body: posture({
          subject: 'alice@acme.com',
          browser_auth: { enabled: true, sign_in_path: '/v1/ui/auth/sign-in', sign_out_path: '/v1/ui/auth/sign-out' },
        }),
      },
      '/v1/load_domain': { status: 401, body: { code: 'auth.token_expired', message: 'expired' } },
    });
    render(<App />);
    await screen.findByLabelText('Catalog refused');
    expect((await screen.findByTestId('expiry-sign-in')).getAttribute('href')).toBe('/v1/ui/auth/sign-in');
  });

  // The layers route issues no catalog read of its own, so the panel would
  // receive the ended session on no path at all unless the shell takes one.
  // The panel is kept underneath the treatment.
  it('presents the ended session on the layer panel and keeps the panel underneath', async () => {
    stubRegistry({
      '/v1/ui/session': {
        body: posture({
          subject: 'alice@acme.com',
          browser_auth: { enabled: true, sign_in_path: '/v1/ui/auth/sign-in', sign_out_path: '/v1/ui/auth/sign-out' },
        }),
      },
      '/v1/load_domain': { status: 401, body: { code: 'auth.token_expired', message: 'expired' } },
      '/v1/layers': { body: { layers: [userLayer()] } },
    });
    goTo('#/layers');
    render(<App />);
    await screen.findByLabelText('Layer panel');
    await screen.findByTestId('session-ended');
    expect((await screen.findByTestId('expiry-sign-in')).getAttribute('href')).toBe('/v1/ui/auth/sign-in');
    expect(screen.getByText('alice-personal')).toBeTruthy();
  });

  // The third row of the sign-in control table bounds what the treatment may
  // offer: a deployment running no browser flow renders no authentication
  // control, so the treatment states what it offers in its place.
  it('offers a retry of the refused read where the deployment runs no browser flow', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ subject: 'alice@acme.com' }) },
      '/v1/load_domain': { status: 401, body: { code: 'auth.token_expired', message: 'expired' } },
    });
    render(<App />);
    await screen.findByLabelText('Catalog refused');
    expect(screen.queryByTestId('expiry-sign-in')).toBeNull();
    expect(screen.getByText(/runs no browser sign-in/)).toBeTruthy();
    // The third row renders no authentication control, so the treatment has to
    // state what it offers in its place, and a retry of the refused read is
    // that control.
    expect(screen.getByTestId('expiry-retry')).toBeTruthy();
  });

  // The refused arm is reached by a caller who never held a subject as well,
  // on a registry whose verifier refuses a browser that carries no token. The
  // expiry transition belongs to the caller whose read resolved a subject, so
  // this caller is told what the read returned and nothing about a session.
  it('claims no ended session where the posture read resolved no subject', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ browser_auth: { enabled: false } }) },
      '/v1/load_domain': { status: 401, body: { code: 'auth.untrusted_token', message: 'no identity' } },
      '/v1/layers': { body: { layers: [userLayer()] } },
    });
    goTo('#/layers');
    render(<App />);
    await screen.findByLabelText('Layer panel');
    await screen.findByTestId('refused-read');
    expect(screen.queryByTestId('session-ended')).toBeNull();
    expect(screen.queryByTestId('expiry-sign-in')).toBeNull();
  });

  // The refused arm belongs to a read the registry could not verify an
  // identity for, and the codes that carry it are the ones the identity
  // middleware writes. The tenant router answers auth.tenant_unknown with the
  // same status for a caller whose token verified, so a page keying on the
  // status alone would tell that caller their session ended while it is
  // intact. That failure takes the surface's own error state.
  it('claims no ended session where a verified caller names an unprovisioned tenant', async () => {
    const unknownTenant = {
      status: 401,
      body: {
        code: 'auth.tenant_unknown',
        message: "Verified token names organization 'globex' which is not a provisioned tenant.",
        details: { token_org_id: 'globex' },
      },
    };
    stubRegistry({
      '/v1/ui/session': {
        body: posture({
          subject: 'alice@acme.com',
          browser_auth: { enabled: true, sign_in_path: '/v1/ui/auth/sign-in', sign_out_path: '/v1/ui/auth/sign-out' },
        }),
      },
      '/v1/load_domain': unknownTenant,
      '/v1/layers': unknownTenant,
    });
    goTo('#/layers');
    render(<App />);
    await screen.findByText('auth.tenant_unknown');
    expect(screen.queryByTestId('session-ended')).toBeNull();
    expect(screen.queryByTestId('refused-read')).toBeNull();
  });
});

describe('read-only mode', () => {
  // §13.2.1 marks a read-only registry on its read responses, so the panel
  // presents the state once and makes every write control unavailable at the
  // same time. A panel that keeps its controls live collects one refusal per
  // button press instead, which is the presentation the brief forbids. The
  // marker arrives on a catalog read, because the middleware that sets it
  // wraps the meta-tool mux and the layer endpoints are mounted beside it.
  it('presents the state once and makes every write control unavailable', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ subject: 'alice@acme.com' }) },
      '/v1/load_domain': { body: emptyDomain, headers: { 'X-Podium-Read-Only': 'true' } },
      '/v1/layers': { body: { layers: [adminLayer(), userLayer()] } },
    });
    goTo('#/layers');
    render(<App />);
    await screen.findByLabelText('Layer panel');
    await screen.findByTestId('read-only-banner');
    for (const name of ['Register layer', 'Reingest', 'Unregister', 'Raise precedence']) {
      for (const control of screen.getAllByRole('button', { name })) {
        expect(control.hasAttribute('disabled')).toBe(true);
      }
    }
  });

  it('keeps every write control live where the registry serves writes', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ subject: 'alice@acme.com' }) },
      '/v1/load_domain': { body: emptyDomain },
      '/v1/layers': { body: { layers: [userLayer()] } },
    });
    goTo('#/layers');
    render(<App />);
    await screen.findByLabelText('Layer panel');
    expect(screen.queryByTestId('read-only-banner')).toBeNull();
    expect(screen.getByRole('button', { name: 'Reingest' }).hasAttribute('disabled')).toBe(false);
  });

  // The layer endpoints are outside the §13.2.1 middleware, so a response from
  // one of them carries the marker on no mode. A panel that read that absence
  // as "the registry serves writes" would clear the banner on its own list
  // read and on every reload after it.
  it('keeps the banner where a layer read carries no marker', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ subject: 'alice@acme.com' }) },
      '/v1/load_domain': { body: emptyDomain, headers: { 'X-Podium-Read-Only': 'true' } },
      '/v1/layers': { body: { layers: [userLayer()] } },
    });
    goTo('#/layers');
    render(<App />);
    await screen.findByLabelText('Layer panel');
    await screen.findByTestId('read-only-banner');
    fireEvent.click(screen.getByRole('button', { name: 'Recently unregistered' }));
    await screen.findByLabelText('Recently unregistered');
    expect(screen.getByTestId('read-only-banner')).toBeTruthy();
  });

  // The middleware that sets the marker wraps the meta-tool mux from inside
  // the identity verification and the tenant router, so a refusal from either
  // is written before that middleware runs and carries no marker whatever the
  // mode is. A page that read that absence as "the registry serves writes"
  // would clear the banner and make every write control live again the moment
  // the session expired on a registry that still refuses every write.
  it('keeps the banner where a catalog read is refused', async () => {
    stubRegistry({
      '/v1/ui/session': {
        body: posture({
          subject: 'alice@acme.com',
          browser_auth: { enabled: true, sign_in_path: '/v1/ui/auth/sign-in', sign_out_path: '/v1/ui/auth/sign-out' },
        }),
      },
      '/v1/load_domain': { body: emptyDomain, headers: { 'X-Podium-Read-Only': 'true' } },
      '/v1/layers': { body: { layers: [userLayer()] } },
    });
    goTo('#/layers');
    render(<App />);
    await screen.findByLabelText('Layer panel');
    await screen.findByTestId('read-only-banner');
    // The session ends, so the shell's next catalog read is refused before it
    // reaches the marker middleware. Re-entering the route re-issues it.
    stubRegistry({
      '/v1/ui/session': {
        body: posture({
          subject: 'alice@acme.com',
          browser_auth: { enabled: true, sign_in_path: '/v1/ui/auth/sign-in', sign_out_path: '/v1/ui/auth/sign-out' },
        }),
      },
      '/v1/load_domain': { status: 401, body: { code: 'auth.token_expired', message: 'expired' } },
      '/v1/layers': { body: { layers: [userLayer()] } },
    });
    goTo('#/');
    await screen.findByLabelText('Catalog refused');
    goTo('#/layers');
    await screen.findByTestId('session-ended');
    await screen.findByLabelText('Layer panel');
    expect(screen.getByTestId('read-only-banner')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Reingest' }).hasAttribute('disabled')).toBe(true);
  });
});

describe('the layer write flows', () => {
  // Unregistering removes the layer's artifacts from every caller's view, so
  // the write is issued only after a confirmation stating both halves of
  // what it does and only once the layer's own ID has been typed.
  it('holds the unregister write until the confirmation is completed', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ subject: 'alice@acme.com' }) },
      '/v1/layers': { body: { layers: [userLayer()] } },
    });
    goTo('#/layers');
    render(<App />);
    await screen.findByLabelText('Layer panel');
    fireEvent.click(screen.getByRole('button', { name: 'Unregister' }));
    const dialog = await screen.findByLabelText('Unregister a layer');
    expect(dialog.textContent).toContain('every caller');
    expect(dialog.textContent).toContain('recoverable for 30 days');
    expect(requests.some((r) => r.method === 'DELETE')).toBe(false);
    const confirm = screen.getByRole('button', { name: 'Unregister layer' });
    expect(confirm.hasAttribute('disabled')).toBe(true);
    fireEvent.change(screen.getByLabelText('Type the layer ID to confirm'), { target: { value: 'alice-personal' } });
    fireEvent.click(screen.getByRole('button', { name: 'Unregister layer' }));
    await waitFor(() => {
      expect(requests.some((r) => r.url.startsWith('/v1/layers?') && r.method === 'DELETE')).toBe(true);
    });
  });

  // §13.10 makes the panel the surface a user manages their own user-defined
  // layers on, which is the class §7.3.1 caps per user and authorizes its
  // owner on, so that is the class the form registers by default. The
  // registry fixes such a layer's visibility to the registrant and discards
  // what the request carries, so the axes are absent on that class.
  it('registers the caller’s own layer as user-defined and offers it no visibility axes', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ subject: 'alice@acme.com' }) },
      '/v1/layers': { body: { layer: { ID: 'alice-personal', SourceType: 'local', Order: 1, UserDefined: true } } },
    });
    goTo('#/layers');
    render(<App />);
    await screen.findByLabelText('Layer panel');
    fireEvent.click(screen.getByRole('button', { name: 'Register layer' }));
    fireEvent.change(screen.getByLabelText('Layer ID'), { target: { value: 'alice-personal' } });
    expect(screen.queryByLabelText('Organization')).toBeNull();
    expect(screen.queryByLabelText('Public')).toBeNull();
    fireEvent.submit(screen.getByLabelText('Register a layer'));
    await waitFor(() => {
      expect(requests.some((r) => r.url === '/v1/layers' && r.method === 'POST')).toBe(true);
    });
    const sent = JSON.parse(bodies.at(-1) ?? '{}') as Record<string, unknown>;
    expect(sent.user_defined).toBe(true);
    expect(sent.public).toBeUndefined();
    expect(sent.organization).toBeUndefined();
    expect(sent.groups).toBeUndefined();
    expect(sent.users).toBeUndefined();
  });

  // A user-defined layer's owner is derived from the caller's own subject and
  // the registry refuses the registration where none resolves, so a caller
  // holding no subject, which is every caller of a standalone registry, opens
  // on the tenant's class instead of on a registration that cannot succeed.
  it('opens on the tenant’s class where the posture read resolved no subject', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ identity_provider_configured: false }) },
      '/v1/layers': { body: { layer: { ID: 'company', SourceType: 'local', Order: 1 } } },
    });
    goTo('#/layers');
    render(<App />);
    await screen.findByLabelText('Layer panel');
    fireEvent.click(screen.getByRole('button', { name: 'Register layer' }));
    fireEvent.change(screen.getByLabelText('Layer ID'), { target: { value: 'company' } });
    expect(screen.getByLabelText('Organization')).toBeTruthy();
    fireEvent.submit(screen.getByLabelText('Register a layer'));
    await waitFor(() => {
      expect(requests.some((r) => r.url === '/v1/layers' && r.method === 'POST')).toBe(true);
    });
    const sent = JSON.parse(bodies.at(-1) ?? '{}') as Record<string, unknown>;
    expect(sent.user_defined).toBe(false);
  });

  // §4.6 defines visibility as independent grants that combine as a union.
  // They are honoured on an admin-defined layer, which is the class the form
  // offers them on.
  it('registers an admin-defined layer on every visibility axis', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ subject: 'alice@acme.com' }) },
      '/v1/layers': { body: { layer: { ID: 'company', SourceType: 'local', Order: 1 } } },
    });
    goTo('#/layers');
    render(<App />);
    await screen.findByLabelText('Layer panel');
    fireEvent.click(screen.getByRole('button', { name: 'Register layer' }));
    fireEvent.change(screen.getByLabelText('Layer ID'), { target: { value: 'company' } });
    fireEvent.change(screen.getByLabelText('Layer class'), { target: { value: 'admin' } });
    fireEvent.click(screen.getByLabelText('Organization'));
    fireEvent.click(screen.getByLabelText('Groups'));
    // An axis selected with no member named registers a grant admitting
    // nobody, so the write is held until each selected axis carries one.
    expect(screen.getByRole('button', { name: 'Register' }).hasAttribute('disabled')).toBe(true);
    fireEvent.change(screen.getByLabelText('Group names, separated by commas'), {
      target: { value: 'secops, appsec' },
    });
    fireEvent.click(screen.getByLabelText('Specific users'));
    fireEvent.change(screen.getByLabelText('User identifiers, separated by commas'), {
      target: { value: 'carol@acme.com' },
    });
    fireEvent.submit(screen.getByLabelText('Register a layer'));
    await waitFor(() => {
      expect(requests.some((r) => r.url === '/v1/layers' && r.method === 'POST')).toBe(true);
    });
    const sent = JSON.parse(bodies.at(-1) ?? '{}') as Record<string, unknown>;
    expect(sent.user_defined).toBe(false);
    expect(sent.organization).toBe(true);
    expect(sent.groups).toEqual(['secops', 'appsec']);
    expect(sent.users).toEqual(['carol@acme.com']);
  });


  it('reveals a git layer’s webhook secret once and holds the reveal until it is acknowledged', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ subject: 'alice@acme.com' }) },
      '/v1/layers': {
        body: {
          layer: { ID: 'alice-personal', SourceType: 'git', Order: 1, UserDefined: true },
          webhook_url: 'https://registry.acme.com/v1/ingest/webhook/alice-personal',
          webhook_secret: 'whsec-abc',
        },
      },
    });
    goTo('#/layers');
    render(<App />);
    await screen.findByLabelText('Layer panel');
    fireEvent.click(screen.getByRole('button', { name: 'Register layer' }));
    fireEvent.change(screen.getByLabelText('Layer ID'), { target: { value: 'alice-personal' } });
    fireEvent.submit(screen.getByLabelText('Register a layer'));
    await screen.findByLabelText('Webhook secret');
    expect(screen.getByText('whsec-abc')).toBeTruthy();
    // The secret is served here and nowhere else, so it carries an explicit
    // copy control rather than leaving the reader to select it. The URL
    // carries one too.
    expect(screen.getAllByRole('button', { name: 'Copy' }).length).toBe(2);
    const done = screen.getByRole('button', { name: 'Done' });
    expect(done.hasAttribute('disabled')).toBe(true);
    fireEvent.click(screen.getByLabelText('I have stored the secret.'));
    expect(screen.getByRole('button', { name: 'Done' }).hasAttribute('disabled')).toBe(false);
  });

  // The update is a partial patch and a rotation returns the fresh secret
  // once, on the same terms as registration, so the rotation runs through the
  // same reveal rather than through a second treatment.
  it('patches a git layer and reveals the rotated secret once', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ subject: 'alice@acme.com' }) },
      '/v1/layers': { body: { layers: [adminLayer()] } },
      'PUT /v1/layers/update': {
        body: {
          layer: { ID: 'company', SourceType: 'git', Order: 1 },
          webhook_url: 'https://registry.acme.com/v1/ingest/webhook/company',
          webhook_secret: 'whsec-rotated',
        },
      },
    });
    goTo('#/layers');
    render(<App />);
    await screen.findByLabelText('Layer panel');
    fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
    const form = await screen.findByLabelText('Update company');
    // The endpoint applies a visibility patch on an admin-defined layer, so
    // the form carries the axes and the patch carries what they name. It
    // grants on each axis and revokes on none, so a stored grant is displayed
    // as unavailable rather than offered as a change the registry answers
    // success to without making.
    expect(screen.getByLabelText('Organization').hasAttribute('disabled')).toBe(true);
    fireEvent.click(screen.getByLabelText('Public'));
    fireEvent.change(screen.getByLabelText('Group names, separated by commas'), { target: { value: 'secops' } });
    fireEvent.change(screen.getByLabelText('Ref'), { target: { value: 'release' } });
    fireEvent.change(screen.getByLabelText('Force-push policy'), { target: { value: 'strict' } });
    fireEvent.click(screen.getByLabelText('Rotate the webhook secret'));
    fireEvent.submit(form);
    await waitFor(() => {
      expect(requests.some((r) => r.url === '/v1/layers/update?id=company' && r.method === 'PUT')).toBe(true);
    });
    const sent = JSON.parse(bodies.at(-1) ?? '{}') as Record<string, unknown>;
    expect(sent.ref).toBe('release');
    expect(sent.force_push_policy).toBe('strict');
    expect(sent.rotate_webhook_secret).toBe(true);
    expect(sent.public).toBe(true);
    expect(sent.groups).toEqual(['secops']);
    await screen.findByLabelText('Webhook secret');
    expect(screen.getByText('whsec-rotated')).toBeTruthy();
  });

  // Only a git source carries a webhook secret, and the registry refuses a
  // rotation on any other source, so the control is unavailable on a
  // local-path layer and says why.
  it('offers no rotation on a local-path layer and patches its source details', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ subject: 'alice@acme.com' }) },
      '/v1/layers': { body: { layers: [userLayer()] } },
      'PUT /v1/layers/update': { body: { layer: userLayer() } },
    });
    goTo('#/layers');
    render(<App />);
    await screen.findByLabelText('Layer panel');
    fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
    const form = await screen.findByLabelText('Update alice-personal');
    const rotate = screen.getByLabelText('Rotate the webhook secret');
    expect(rotate.hasAttribute('disabled')).toBe(true);
    // §4.6 fixes a user-defined layer's visibility at registration, and the
    // registry ignores a visibility patch there and still answers success, so
    // that class displays its visibility rather than editing it.
    expect(screen.queryByLabelText('Organization')).toBeNull();
    expect(form.textContent).toContain('fixed to you at registration');
    expect(form.textContent).toContain('Only a git layer carries a webhook secret.');
    fireEvent.change(screen.getByLabelText('Local path'), { target: { value: '/Users/alice/moved' } });
    fireEvent.submit(form);
    await waitFor(() => {
      expect(requests.some((r) => r.url === '/v1/layers/update?id=alice-personal' && r.method === 'PUT')).toBe(true);
    });
    const sent = JSON.parse(bodies.at(-1) ?? '{}') as Record<string, unknown>;
    expect(sent.local_path).toBe('/Users/alice/moved');
    expect(sent.rotate_webhook_secret).toBeUndefined();
    expect(sent.public).toBeUndefined();
    expect(sent.groups).toBeUndefined();
    // A patch that rotates nothing carries no secret, so the reveal is
    // replaced by the outcome the update reports.
    expect((await screen.findByText('Layer alice-personal is updated.')).textContent).toBeTruthy();
  });

  // §4.6 composes every user-defined layer above every admin-defined one
  // whatever the stored order values are, so a move runs inside the moving
  // layer's own class and the request names that class block. The endpoint
  // rewrites the order value of every layer the request names from that
  // layer's position in the request, so a request naming the traded pair
  // alone would stamp the block's first two order values onto the pair and
  // leave the rest of the block holding stored values that tie or invert
  // against them.
  it('sends the resulting order of the moving layer class block', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ subject: 'alice@acme.com' }) },
      '/v1/layers': {
        body: { layers: [adminLayer(), userLayer(), scratchLayer(), bobLayer()] },
      },
      '/v1/layers/reorder': { body: { layers: [] } },
    });
    goTo('#/layers');
    render(<App />);
    await screen.findByLabelText('Layer panel');
    const controls = screen.getAllByRole('button', { name: 'Raise precedence' });
    // The admin layer is alone in its class, so its control has nothing to
    // move past and is held.
    expect(controls[0].hasAttribute('disabled')).toBe(true);
    fireEvent.click(controls[1]);
    await waitFor(() => {
      expect(requests.some((r) => r.url === '/v1/layers/reorder' && r.method === 'POST')).toBe(true);
    });
    // The user-defined block is alice-personal, alice-scratch, bob-personal
    // in stored order, and the move trades the first two. The whole block is
    // named, bob-personal included, so its rewritten order value keeps it
    // below the pair rather than colliding with them; the registry authorizes
    // each named layer on its own and the panel presents whatever it refuses.
    expect(bodies.at(-1)).toBe(JSON.stringify({ order: ['alice-scratch', 'alice-personal', 'bob-personal'] }));
  });

  // The reingest call runs the whole pipeline inside the request and answers
  // with a summary the reader has to act on, so the control presents the
  // counts and the itemised rejections and conflicts rather than returning
  // the row to rest.
  it('presents what the reingest snapshot accepted, rejected, and conflicted on', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ subject: 'alice@acme.com' }) },
      '/v1/layers': { body: { layers: [userLayer()] } },
      '/v1/layers/reingest': {
        body: {
          layer: 'alice-personal',
          accepted: 4,
          idempotent: 2,
          lint_failures: 1,
          rejected: [{ artifact_id: 'platform/deploy', code: 'ingest.sensitivity_floor', reason: 'above the floor' }],
          conflicts: [
            {
              artifact_id: 'platform/lint',
              version: '1.0.0',
              old_hash: 'sha256:aaa',
              new_hash: 'sha256:bbb',
              code: 'ingest.immutable_violation',
            },
          ],
          advisories: [
            { artifact_id: 'platform/ci', code: 'license.changed', severity: 'warning', message: 'license changed' },
          ],
        },
      },
    });
    goTo('#/layers');
    render(<App />);
    await screen.findByLabelText('Layer panel');
    fireEvent.click(screen.getByRole('button', { name: 'Reingest' }));
    await screen.findByLabelText('Reingest result');
    expect(screen.getByText('4 accepted')).toBeTruthy();
    expect(screen.getByText('2 unchanged')).toBeTruthy();
    expect(screen.getByText('1 lint failures')).toBeTruthy();
    expect(screen.getByLabelText('Rejected artifacts')).toBeTruthy();
    expect(screen.getByText('platform/lint@1.0.0')).toBeTruthy();
    expect(screen.getByLabelText('Advisories')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Done' }));
    expect(screen.queryByLabelText('Reingest result')).toBeNull();
  });

  // A registry with no ingest runner wired records the intent and answers
  // with no summary, so the control says the request was recorded rather than
  // presenting a summary of zeroes.
  it('reports a recorded reingest where the registry runs no pipeline in the request', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ subject: 'alice@acme.com' }) },
      '/v1/layers': { body: { layers: [userLayer()] } },
      '/v1/layers/reingest': { body: { queued: 'alice-personal', queued_at: '2026-08-25T00:00:00Z' } },
    });
    goTo('#/layers');
    render(<App />);
    await screen.findByLabelText('Layer panel');
    fireEvent.click(screen.getByRole('button', { name: 'Reingest' }));
    expect(await screen.findByTestId('reingest-recorded')).toBeTruthy();
  });

  // A snapshot whose every artifact collided with a published version is
  // refused whole with 409 ingest.immutable_violation. Nothing was accepted
  // and the layer is unchanged, and bumping the versions is the only thing
  // that clears it, so the arm names the colliding versions and offers no
  // retry.
  it('names the colliding versions where the whole snapshot was refused', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ subject: 'alice@acme.com' }) },
      '/v1/layers': { body: { layers: [userLayer()] } },
      '/v1/layers/reingest': {
        status: 409,
        body: {
          code: 'ingest.immutable_violation',
          message: 'same-version content conflict: platform/lint@1.0.0 already exists with different content',
          retryable: false,
          details: {
            conflicts: [
              {
                artifact_id: 'platform/lint',
                version: '1.0.0',
                old_hash: 'sha256:aaa',
                new_hash: 'sha256:bbb',
                code: 'ingest.immutable_violation',
              },
            ],
          },
        },
      },
    });
    goTo('#/layers');
    render(<App />);
    await screen.findByLabelText('Layer panel');
    fireEvent.click(screen.getByRole('button', { name: 'Reingest' }));
    await screen.findByLabelText('Reingest rejected');
    expect(screen.getByText('Nothing was ingested')).toBeTruthy();
    expect(screen.getByText('platform/lint@1.0.0')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Try again' })).toBeNull();
  });

  // The cap refusal carries the limit and the caller's current count, and
  // this is where the user created the layer, so the count is rendered here
  // rather than arriving as the generic failure every other refusal gets.
  it('renders the layer limit and the current count where a registration exceeds the cap', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ subject: 'alice@acme.com' }) },
      '/v1/layers': { body: { layers: [userLayer()] } },
      'POST /v1/layers': {
        status: 429,
        body: {
          code: 'quota.layer_count_exceeded',
          message: 'user-defined layer cap of 3 reached for alice@acme.com',
          details: { limit: 3, current: 3 },
        },
      },
    });
    goTo('#/layers');
    render(<App />);
    await screen.findByLabelText('Layer panel');
    fireEvent.click(screen.getByRole('button', { name: 'Register layer' }));
    fireEvent.change(screen.getByLabelText('Layer ID'), { target: { value: 'alice-extra' } });
    fireEvent.submit(screen.getByLabelText('Register a layer'));
    const refusal = await screen.findByLabelText('Layer limit reached');
    expect(refusal.textContent).toContain('3 of 3');
    expect(screen.getByText('quota.layer_count_exceeded')).toBeTruthy();
  });

  it('lists what is still recoverable and restores it', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ subject: 'alice@acme.com' }) },
      '/v1/layers': { body: { layers: [] } },
      '/v1/layers/restore': { body: {} },
    });
    goTo('#/layers');
    render(<App />);
    await screen.findByLabelText('Layer panel');
    // The deleted read answers on the same path with a query argument, so
    // the stub is swapped once the panel is up.
    stubRegistry({
      '/v1/ui/session': { body: posture({ subject: 'alice@acme.com' }) },
      '/v1/layers': { body: { layers: [userLayer()] } },
      '/v1/layers/restore': { body: {} },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Recently unregistered' }));
    await screen.findByLabelText('Recently unregistered');
    fireEvent.click(await screen.findByRole('button', { name: 'Restore' }));
    await waitFor(() => {
      expect(requests.some((r) => r.url.startsWith('/v1/layers/restore') && r.method === 'POST')).toBe(true);
    });
  });
});

function adminLayer(owner = ''): Record<string, unknown> {
  return {
    ID: 'company',
    SourceType: 'git',
    Repo: 'git@github.com:acme/company.git',
    Ref: 'main',
    Order: 1,
    UserDefined: false,
    Owner: owner,
    Organization: true,
  };
}

function userLayer(owner = 'alice@acme.com'): Record<string, unknown> {
  return {
    ID: 'alice-personal',
    SourceType: 'local',
    LocalPath: '/Users/alice/registry',
    Order: 2,
    UserDefined: true,
    Owner: owner,
  };
}

/** bobLayer is a user-defined layer another subject owns. The list read is
 * unfiltered, so it reaches the panel alongside the caller's own, and a
 * reorder that named it would be refused whole. */
function bobLayer(): Record<string, unknown> {
  return {
    ID: 'bob-personal',
    SourceType: 'local',
    LocalPath: '/Users/bob/registry',
    Order: 4,
    UserDefined: true,
    Owner: 'bob@acme.com',
  };
}

/** scratchLayer is a second user-defined layer, which a reorder case needs so
 * the moving layer has a sibling inside its own class. */
function scratchLayer(owner = 'alice@acme.com'): Record<string, unknown> {
  return {
    ID: 'alice-scratch',
    SourceType: 'local',
    LocalPath: '/Users/alice/scratch',
    Order: 3,
    UserDefined: true,
    Owner: owner,
  };
}
