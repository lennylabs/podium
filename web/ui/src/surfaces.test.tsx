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
      const stub = stubs[path] ?? { status: 404, body: { code: 'registry.not_found', message: 'no stub' } };
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
            { id: 'platform/meaning', type: 'skill', score: 0 },
          ],
        },
      },
    });
    goTo('#/search/review');
    render(<App />);
    expect((await screen.findByTestId('result-count')).textContent).toBe('Showing 2 of 143');
    expect(screen.getByText('internal')).toBeTruthy();
    expect(screen.getByText('score 8.50')).toBeTruthy();
    expect(screen.getByText('matched by meaning')).toBeTruthy();
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
  it('offers no sign-in control where the deployment runs no browser flow', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ subject: 'alice@acme.com' }) },
      '/v1/load_domain': { status: 401, body: { code: 'auth.token_expired', message: 'expired' } },
    });
    render(<App />);
    await screen.findByLabelText('Catalog refused');
    expect(screen.queryByTestId('expiry-sign-in')).toBeNull();
    expect(screen.getByText(/runs no browser sign-in/)).toBeTruthy();
  });
});

describe('read-only mode', () => {
  // §13.2.1 marks a read-only registry on its read responses, so the panel
  // presents the state once and makes every write control unavailable at the
  // same time. A panel that keeps its controls live collects one refusal per
  // button press instead, which is the presentation the brief forbids.
  it('presents the state once and makes every write control unavailable', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ subject: 'alice@acme.com' }) },
      '/v1/layers': { body: { layers: [adminLayer(), userLayer()] }, headers: { 'X-Podium-Read-Only': 'true' } },
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
      '/v1/layers': { body: { layers: [userLayer()] } },
    });
    goTo('#/layers');
    render(<App />);
    await screen.findByLabelText('Layer panel');
    expect(screen.queryByTestId('read-only-banner')).toBeNull();
    expect(screen.getByRole('button', { name: 'Reingest' }).hasAttribute('disabled')).toBe(false);
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

  // §4.6 defines visibility as independent grants that combine as a union,
  // and a user-defined layer's visibility is fixed at registration, so an
  // axis the form does not offer is unreachable from the UI entirely.
  it('registers a layer on every visibility axis', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ subject: 'alice@acme.com' }) },
      '/v1/layers': { body: { layer: { ID: 'alice-personal', SourceType: 'local', Order: 1, UserDefined: true } } },
    });
    goTo('#/layers');
    render(<App />);
    await screen.findByLabelText('Layer panel');
    fireEvent.click(screen.getByRole('button', { name: 'Register layer' }));
    fireEvent.change(screen.getByLabelText('Layer ID'), { target: { value: 'alice-personal' } });
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

  it('sends the resulting order when a row changes precedence', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ subject: 'alice@acme.com' }) },
      '/v1/layers': { body: { layers: [adminLayer(), userLayer()] } },
      '/v1/layers/reorder': { body: { layers: [] } },
    });
    goTo('#/layers');
    render(<App />);
    await screen.findByLabelText('Layer panel');
    fireEvent.click(screen.getAllByRole('button', { name: 'Raise precedence' })[0]);
    await waitFor(() => {
      expect(requests.some((r) => r.url === '/v1/layers/reorder' && r.method === 'POST')).toBe(true);
    });
    expect(bodies.at(-1)).toBe(JSON.stringify({ order: ['alice-personal', 'company'] }));
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
