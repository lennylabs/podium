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

import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { App } from './App';
import { parseQueryLine } from './query';
import { searchHref } from './route';
import type { SessionPosture } from './session';
// The stylesheet is imported for its own sake: the wrapping rule the rail
// depends on is asserted from the computed style it produces.
import './index.css';

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
  /** deferred holds the response until a later macrotask, which is what a
   * network round-trip does. A stub that answers within the same batch of
   * React updates as the call that issued it hides every intermediate state
   * the surface renders while the request is in flight. */
  deferred?: boolean;
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
      // A path whose query argument selects a different response takes the
      // whole URL as its key, which is how the deleted-layer read is told
      // apart from the layer list it shares a path with.
      const stub = (url === path ? undefined : stubs[url]) ??
        stubs[`${method} ${path}`] ??
        stubs[path] ?? { status: 404, body: { code: 'registry.not_found', message: 'no stub' } };
      const status = stub.status ?? 200;
      const answer = () =>
        new Response(stub.text ?? JSON.stringify(stub.body ?? {}), {
          status,
          headers: {
            'content-type': stub.text === undefined ? 'application/json' : 'text/markdown',
            ...stub.headers,
          },
        });
      if (stub.deferred === true) {
        return new Promise<Response>((resolve) => {
          setTimeout(() => {
            resolve(answer());
          }, 0);
        });
      }
      return Promise.resolve(answer());
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

/** lastSearch is the query string of the most recent search the page issued,
 * which is what a case asserting a filter reads. */
function lastSearch(): URLSearchParams {
  const last = requests.filter((r) => r.url.startsWith('/v1/search_artifacts')).at(-1)?.url ?? '';
  return new URLSearchParams(last.split('?')[1] ?? '');
}

/** addToken drives the filter row's token entry, which is how a tag, a scope,
 * and a type the row does not offer as a pill are added. */
function addToken(label: string, value: string): void {
  fireEvent.click(screen.getByRole('button', { name: `+ ${label}` }));
  fireEvent.change(screen.getByLabelText(`Add a ${label} filter`), { target: { value } });
  fireEvent.click(screen.getByRole('button', { name: 'Add' }));
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

describe('the application shell', () => {
  const catalog = {
    path: '',
    subdomains: [
      {
        path: 'platform',
        name: 'platform',
        subdomains: [{ path: 'platform/ci', name: 'ci' }],
      },
    ],
    notable: [],
  };

  // The shell is one layout on every screen: the nav, the catalog label with
  // its depth marker, the tree, and the counts footer pinned under it. The
  // tree is eager to two levels and reads a deeper level when the reader
  // expands the node it hangs under.
  it('renders the catalog tree, reads a deeper level on expand, and states the counts', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ public_mode: true }) },
      '/v1/load_domain': { body: catalog },
      '/v1/search_artifacts': { body: { total_matched: 312 } },
      '/v1/layers': {
        body: { layers: [adminLayer(), { ...userLayer(), last_ingested_at: new Date().toISOString() }] },
      },
    });
    render(<App />);
    const tree = await screen.findByLabelText('Catalog');
    expect(screen.getByTestId('catalog-depth').textContent).toBe('2 levels');
    // Both eager levels are in the response, so the second one is rendered
    // from it rather than read again.
    fireEvent.click(within(tree).getAllByRole('button', { expanded: false })[0]);
    expect(within(tree).getByText('ci')).toBeTruthy();
    expect(await screen.findByTestId('catalog-counts')).toBeTruthy();
    await waitFor(() => {
      expect(screen.getByTestId('catalog-counts').textContent).toBe('2 layers · 312 artifacts');
    });
    expect(screen.getByTestId('catalog-ingest').textContent).toBe('ingested 0m ago');
    // The level below the eager edge is unknown until the reader asks for
    // it, and asking is what reads it.
    fireEvent.click(within(tree).getAllByRole('button', { expanded: false })[0]);
    await waitFor(() => {
      expect(requests.some((r) => r.url.includes('path=platform%2Fci'))).toBe(true);
    });
  });

  // The wordmark is the mark the design pass fixed, drawn inline beside the
  // name, so it resolves from the bundle like the rest of the page.
  it('renders the wordmark as an inline mark beside the name', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ public_mode: true }) },
      '/v1/load_domain': { body: emptyDomain },
      '/v1/search_artifacts': { body: { total_matched: 0 } },
      '/v1/layers': { body: { layers: [] } },
    });
    render(<App />);
    const wordmark = await screen.findByLabelText('Podium');
    expect(wordmark.querySelector('svg')).toBeTruthy();
    expect(wordmark.textContent).toBe('Podium');
  });

  // A domain the registry refuses to open stays in the hierarchy and is not
  // enterable, which is what the reader is owed: the tree lists it and the
  // link is gone.
  it('lists a domain whose level the registry refuses and makes it unenterable', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ public_mode: true }) },
      '/v1/load_domain': { body: catalog },
      '/v1/search_artifacts': { body: { total_matched: 0 } },
      '/v1/layers': { body: { layers: [] } },
    });
    render(<App />);
    const tree = await screen.findByLabelText('Catalog');
    fireEvent.click(within(tree).getAllByRole('button', { expanded: false })[0]);
    stubRegistry({
      '/v1/ui/session': { body: posture({ public_mode: true }) },
      '/v1/load_domain': { status: 403, body: { code: 'auth.forbidden', message: 'not permitted' } },
      '/v1/search_artifacts': { body: { total_matched: 0 } },
      '/v1/layers': { body: { layers: [] } },
    });
    fireEvent.click(within(tree).getAllByRole('button', { expanded: false })[0]);
    expect(await screen.findByTestId('restricted-domain')).toBeTruthy();
    expect(within(tree).queryByRole('link', { name: 'ci' })).toBeNull();
  });

  // The deeper read is a catalog read, so a refusal for an unverifiable
  // identity is the expiry signal rather than a permission property of the
  // domain. A caller whose session ends while the page is open is owed the
  // expiry transition, which is what the shell renders from the outcome the
  // node hands it.
  it('renders the expiry transition where a deeper level is refused for an unverifiable identity', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ subject: 'alice@acme.com' }) },
      '/v1/load_domain': { body: catalog },
      '/v1/search_artifacts': { body: { total_matched: 0 } },
      '/v1/layers': { body: { layers: [] } },
    });
    render(<App />);
    const tree = await screen.findByLabelText('Catalog');
    fireEvent.click(within(tree).getAllByRole('button', { expanded: false })[0]);
    stubRegistry({
      '/v1/load_domain': { status: 401, body: { code: 'auth.token_expired', message: 'expired' } },
    });
    fireEvent.click(within(tree).getAllByRole('button', { expanded: false })[0]);
    expect(await screen.findByTestId('session-ended')).toBeTruthy();
    expect(screen.queryByTestId('restricted-domain')).toBeNull();
  });

  // A level that did not load for any other reason states that and nothing
  // more. The domain stays enterable, because no response reported that this
  // caller may not open it, and expanding the node again re-issues the read
  // rather than latching the failure for the life of the page.
  it('keeps a domain enterable where its deeper read failed and re-reads it on the next expansion', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ public_mode: true }) },
      '/v1/load_domain': { body: catalog },
      '/v1/search_artifacts': { body: { total_matched: 0 } },
      '/v1/layers': { body: { layers: [] } },
    });
    render(<App />);
    const tree = await screen.findByLabelText('Catalog');
    fireEvent.click(within(tree).getAllByRole('button', { expanded: false })[0]);
    stubRegistry({
      '/v1/load_domain': { status: 503, body: { code: 'registry.unavailable', message: 'down' } },
    });
    const toggle = within(tree).getAllByRole('button', { expanded: false })[0];
    fireEvent.click(toggle);
    expect(await screen.findByTestId('unavailable-domain')).toBeTruthy();
    expect(within(tree).getByRole('link', { name: 'ci' })).toBeTruthy();
    expect(screen.queryByTestId('restricted-domain')).toBeNull();
    // The failure cleared, and the next expansion is what re-issues the read.
    stubRegistry({
      '/v1/load_domain': { body: { path: 'platform/ci', subdomains: [{ path: 'platform/ci/lint', name: 'lint' }] } },
    });
    fireEvent.click(toggle);
    fireEvent.click(toggle);
    expect(await within(tree).findByText('lint')).toBeTruthy();
    expect(screen.queryByTestId('unavailable-domain')).toBeNull();
  });

  // The refused arm has no catalog to navigate. The tree and the counts are
  // emptied rather than left standing with what an earlier read returned,
  // and the depth marker is kept, because it states a property of this
  // navigation rather than of the catalog.
  it('empties the tree and the counts where the catalog read is refused', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture() },
      '/v1/load_domain': { status: 401, body: { code: 'auth.untrusted_token', message: 'not verified' } },
      '/v1/search_artifacts': { body: { total_matched: 312 } },
      '/v1/layers': { body: { layers: [adminLayer()] } },
    });
    render(<App />);
    await screen.findByLabelText('Catalog refused');
    expect(within(screen.getByLabelText('Catalog')).queryAllByRole('listitem')).toEqual([]);
    expect(screen.getByTestId('catalog-counts').textContent).toBe('');
    expect(screen.queryByTestId('catalog-ingest')).toBeNull();
    expect(screen.getByTestId('catalog-depth').textContent).toBe('2 levels');
  });
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
    // The sign-out entry point is the one the account menu carries, so the
    // cluster is opened first.
    fireEvent.click(await screen.findByTestId('account-trigger'));
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
    expect(screen.queryByTestId('anonymous-banner')).toBeNull();
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
    // The arm carries two pieces: the sidebar footer note and the page
    // banner. The banner carries no control of its own, because the
    // authentication control belongs to the shell.
    const banner = screen.getByTestId('anonymous-banner');
    expect(banner.textContent).toContain('not signed in');
    expect(banner.querySelector('button')).toBeNull();
    expect(banner.querySelector('a')).toBeNull();
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
    const browser = await screen.findByLabelText('Domain browser');
    // The sidebar tree lists the same domain, so the browser's own listing is
    // read off the surface rather than off the page.
    expect(within(browser).getByText('ci')).toBeTruthy();
    expect(screen.getByText('platform/deploy')).toBeTruthy();
    expect(screen.getByText('curated')).toBeTruthy();
    expect(screen.getByText('Lifted from sparse subdomains')).toBeTruthy();
    // The note reaches the reader at the returned edge rather than above the
    // description, beside the count and the control that continues past it.
    const continuation = await screen.findByTestId('listing-continuation');
    expect(continuation.textContent).toContain('The listing was trimmed to fit the response budget.');
  });

  // The subdomains are a card grid over the immediate children. Each card
  // states what the response reported below that child, and the grandchildren
  // the two-level read returned are counted rather than drawn, so the page
  // stays one level deep however deep the tree runs.
  it('lists the immediate subdomains as counted cards without nesting the level below', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ public_mode: true }) },
      '/v1/load_domain': {
        body: {
          path: '',
          subdomains: [
            {
              path: 'platform',
              name: 'platform',
              description: 'Platform engineering.',
              subdomains: [
                { path: 'platform/ci', name: 'ci' },
                { path: 'platform/deploy', name: 'deploy' },
              ],
            },
            { path: 'finance', name: 'finance' },
          ],
          notable: [],
        },
      },
    });
    render(<App />);
    const browser = await screen.findByLabelText('Domain browser');
    const grid = within(browser).getByRole('list', { name: 'Subdomains' });
    const cards = within(grid).getAllByRole('listitem');
    expect(cards.map((card) => within(card).getByRole('link').textContent)).toEqual(['platform', 'finance']);
    // The grandchildren are the count on their parent's card and appear
    // nowhere on the page as cards of their own.
    expect(within(cards[0]).getByText('2 subdomains')).toBeTruthy();
    expect(within(grid).queryByText('ci')).toBeNull();
    expect(within(grid).queryByText('deploy')).toBeNull();
    // A child the response reported nothing under claims no count.
    expect(within(cards[1]).queryByText(/subdomains?$/)).toBeNull();
    expect(within(cards[1]).getByText('No description.')).toBeTruthy();
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
    // The filter row is pills rather than text boxes: a type is selected from
    // the offered set, and a scope and a tag are added through the row's
    // token entry.
    fireEvent.click(within(screen.getByRole('group', { name: 'Type' })).getByRole('button', { name: 'skill' }));
    addToken('scope', 'platform');
    addToken('tag', 'review');
    addToken('tag', 'security');
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
            // The registry marshals the score with omitempty, so the zero
            // score a fused-in vector-only candidate carries never reaches
            // the wire. The row arrives with no score key at all, which is
            // what a surface reading the field's presence would render as an
            // unranked row.
            { id: 'platform/meaning', type: 'skill' },
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

  // An active filter carries the control that removes it, which is what
  // returns the row to the unfiltered read.
  it('drops a filter from the request when its pill is removed', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ public_mode: true }) },
      '/v1/search_artifacts': { body: { total_matched: 0 } },
    });
    goTo('#/search/review');
    render(<App />);
    await screen.findByLabelText('Search');
    addToken('tag', 'security');
    await waitFor(() => {
      expect(lastSearch().get('tags')).toBe('security');
    });
    fireEvent.click(screen.getByRole('button', { name: 'Remove the security filter' }));
    await waitFor(() => {
      expect(lastSearch().get('tags')).toBeNull();
    });
  });

  // A search is addressable: the query and the active filters live in the
  // route, so the address bar names what is on screen and the reader can
  // reload it or send it to someone else.
  it('carries the typed query and the active filters in the location hash', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ public_mode: true }) },
      '/v1/search_artifacts': { body: { total_matched: 0 } },
    });
    goTo(searchHref(''));
    render(<App />);
    await screen.findByLabelText('Search');
    fireEvent.change(screen.getByLabelText('Search artifacts'), { target: { value: 'deploy' } });
    await waitFor(() => {
      expect(window.location.hash).toBe(searchHref('deploy'));
    });
    fireEvent.click(within(screen.getByRole('group', { name: 'Type' })).getByRole('button', { name: 'skill' }));
    addToken('tag', 'security');
    await waitFor(() => {
      expect(window.location.hash).toBe(searchHref('type:skill tag:security deploy'));
    });
    // The hash the surface writes is the one the surface reads, so a reload
    // of it stands the same query and the same pills back up.
    const restored = parseQueryLine(decodeURIComponent(window.location.hash.replace('#/search/', '')));
    expect(restored).toEqual({ query: 'deploy', type: 'skill', scope: '', tags: ['security'] });
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
    // The rendered tab is the one the viewer opens on, and the rail carries
    // the frontmatter beside it.
    expect(screen.getByTestId('artifact-body').querySelector('h1')?.textContent).toBe('Review');
    const rail = screen.getByTestId('rail-frontmatter-table');
    expect(rail.textContent).toContain('name');
    expect(rail.textContent).toContain('security');
    expect(screen.queryByText('Invalid syntax')).toBeNull();
    fireEvent.click(screen.getByRole('tab', { name: /Frontmatter/ }));
    const table = screen.getByTestId('frontmatter-table');
    expect(table.textContent).toContain('security');
    // The authored skill file is populated on a skill artifact, so the
    // viewer carries its tab.
    fireEvent.click(screen.getByRole('tab', { name: 'Authored source' }));
    expect(screen.getByText(/Authored skill body\./)).toBeTruthy();
    const relation = await screen.findByText('platform/review-strict');
    expect(relation.getAttribute('href')).toBe('#/artifact/platform%2Freview-strict');
  });

  // The header names the artifact. The heading carries the artifact's own
  // name at the page-title role, the badges qualifying it sit beside it on
  // the same line, and the breadcrumb above it leads back through the
  // domains. A heading set at the mono-body role leaves the markdown body's
  // own first heading as the largest text on the page.
  it('names the artifact in a page title with the badges beside it', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ public_mode: true }) },
      '/v1/load_artifact': {
        body: {
          id: 'finance/accounts-payable/pay-invoice',
          type: 'skill',
          version: '2.3.0',
          content_hash: 'sha256:abc',
          manifest_body: '# Pay an invoice\n',
          frontmatter: '---\nname: pay-invoice\ndescription: Pay a supplier invoice.\n---\n',
        },
      },
      '/v1/dependents': { body: { edges: [] } },
    });
    goTo('#/artifact/finance%2Faccounts-payable%2Fpay-invoice');
    render(<App />);
    await screen.findByLabelText('Artifact viewer');
    // The markdown body carries a heading of its own, so the assertion is
    // that the first one on the page is the artifact's name.
    const headings = within(screen.getByLabelText('Artifact viewer')).getAllByRole('heading', { level: 1 });
    const heading = headings[0];
    expect(heading.textContent).toBe('pay-invoice');
    const style = window.getComputedStyle(heading);
    expect(style.fontSize).toBe('29px');
    // The badges are siblings of the heading rather than a row below it.
    const title = heading.parentElement;
    expect(within(title as HTMLElement).getByText('skill')).toBeTruthy();
    expect(within(title as HTMLElement).getByText('2.3.0')).toBeTruthy();
    // The whole identifier stands under the title, and the breadcrumb leads
    // back through the domains above it.
    const content = heading.closest('.artifact-content') as HTMLElement;
    expect(within(content).getByText('finance/accounts-payable/pay-invoice')).toBeTruthy();
    const trail = screen.getByLabelText('Breadcrumb');
    expect(within(trail).getByText('accounts-payable').getAttribute('href')).toBe(
      '#/domain/finance%2Faccounts-payable',
    );
    expect(within(content).getByText('Pay a supplier invoice.')).toBeTruthy();
  });

  // Spec: §13.10 — the viewer links to extending or dependent artifacts.
  // Every edge the dependents endpoint serves ends at the artifact on the
  // page, so the label reads in the passive direction. Labelling the row
  // with the raw edge kind states the relationship backwards.
  it('labels each graph edge as inbound rather than inverting the relationship', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ public_mode: true }) },
      '/v1/load_artifact': {
        body: {
          id: 'finance/ap/pay-invoice',
          type: 'skill',
          version: '1.0.0',
          content_hash: 'sha256:abc',
          manifest_body: '# Pay invoice\n',
          frontmatter: manifestDoc,
        },
      },
      '/v1/dependents': {
        body: {
          edges: [
            { from: 'finance/ap/reconcile-ledger', to: 'finance/ap/pay-invoice', kind: 'extends' },
            { from: 'finance/ap/close-books', to: 'finance/ap/pay-invoice', kind: 'delegates_to' },
          ],
        },
      },
    });
    goTo('#/artifact/finance%2Fap%2Fpay-invoice');
    render(<App />);
    const relations = await screen.findByLabelText('Relations');
    const rows = relations.querySelectorAll('li');
    expect(rows.length).toBe(2);
    expect(rows[0].textContent).toBe('extended by finance/ap/reconcile-ledger');
    expect(rows[1].textContent).toBe('delegated to by finance/ap/close-books');
    // The bare edge kind beside the link is the inverted reading.
    expect(relations.querySelector('.label.quiet')?.textContent).not.toBe('extends');
  });

  // The rail is a fixed-width column and a content hash is long by
  // construction, so the value has to wrap inside the rail. Without a
  // wrapping rule it runs past the panel edge and is clipped mid-string,
  // which leaves the reader no way to read the value at all.
  it('wraps the provenance content hash inside the rail rather than running it past the edge', async () => {
    const contentHash = 'sha256:ab7469fdce70f0beb8c3b4e696da5e0080f95f75a9d8b3c2e1f0a94d6c7b8e5f';
    stubRegistry({
      '/v1/ui/session': { body: posture({ public_mode: true }) },
      '/v1/load_artifact': {
        body: {
          id: 'finance/ap/pay-invoice',
          type: 'skill',
          version: '1.0.0',
          content_hash: contentHash,
          manifest_body: '# Pay invoice\n',
          frontmatter: manifestDoc,
        },
      },
      '/v1/dependents': { body: { edges: [] } },
    });
    goTo('#/artifact/finance%2Fap%2Fpay-invoice');
    render(<App />);
    const provenance = await screen.findByLabelText('Provenance');
    const value = within(provenance).getByText(contentHash);
    const style = window.getComputedStyle(value);
    expect(`${style.overflowWrap} ${style.wordBreak}`).toMatch(/anywhere|break-word|break-all/);
  });

  // The viewer is two columns with a tab set over the content one. The
  // resource tab carries the count of what the artifact bundles, and a tab
  // whose artifact carries nothing for it is not drawn at all rather than
  // opening on an empty panel.
  it('draws the tab set with the resource count and drops a tab the artifact carries nothing for', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ public_mode: true }) },
      '/v1/load_artifact': {
        body: {
          id: 'platform/review',
          type: 'context',
          version: '1.0.0',
          content_hash: 'sha256:abc',
          manifest_body: '# Review\n',
          frontmatter: manifestDoc,
          resources: { 'checklist.md': 'body', 'rubric.md': 'body' },
        },
      },
      '/v1/dependents': { body: { edges: [] } },
    });
    goTo('#/artifact/platform%2Freview');
    render(<App />);
    await screen.findByLabelText('Artifact viewer');
    expect(screen.getByRole('tab', { name: /Resources/ }).textContent).toContain('2');
    expect(screen.queryByRole('tab', { name: 'Authored source' })).toBeNull();
    fireEvent.click(screen.getByRole('tab', { name: /Resources/ }));
    expect(screen.getByLabelText('Resources').textContent).toContain('checklist.md');
  });

  // Every bundled file is retrievable from its own row: nothing is
  // previewed, so the row's action is the only path to the file. One binary
  // file puts the whole inline set into base64, and that row's action carries
  // the decoded bytes while its size column states the file's own byte count
  // rather than the length of the encoding.
  it('gives every resource row a format, a byte size, and a download action', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ public_mode: true }) },
      '/v1/load_artifact': {
        body: {
          id: 'platform/review',
          type: 'context',
          version: '1.0.0',
          content_hash: 'sha256:abc',
          manifest_body: '# Review\n',
          frontmatter: '',
          resources: { 'logo.png': 'AAECAw==' },
          resources_base64: true,
          large_resources: {
            'corpus.bin': {
              presigned_url: 'https://objects.acme.com/corpus',
              content_hash: 'sha256:def',
              size: 168000000,
              content_type: 'application/octet-stream',
            },
          },
        },
      },
      '/v1/dependents': { body: { edges: [] } },
    });
    goTo('#/artifact/platform%2Freview');
    render(<App />);
    await screen.findByLabelText('Artifact viewer');
    fireEvent.click(screen.getByRole('tab', { name: /Resources/ }));
    const rows = within(screen.getByLabelText('Resources')).getAllByRole('row').slice(1);
    const inline = within(rows[0]).getAllByRole('cell').map((cell) => cell.textContent);
    expect(inline.slice(0, 4)).toEqual(['logo.png', 'png', '4 bytes', 'inline, base64']);
    const download = within(rows[0]).getByRole('link', { name: 'Download' });
    expect(download.getAttribute('href')).toBe('data:application/octet-stream;base64,AAECAw==');
    expect(download.getAttribute('download')).toBe('logo.png');
    const fetched = within(rows[1]).getAllByRole('cell').map((cell) => cell.textContent);
    expect(fetched.slice(0, 4)).toEqual([
      'corpus.bin',
      'application/octet-stream',
      '168000000 bytes',
      'fetched on demand',
    ]);
    expect(within(rows[1]).getByRole('link', { name: 'Download' }).getAttribute('href')).toBe(
      'https://objects.acme.com/corpus',
    );
  });

  // The frontmatter panel offers both readings of the block: the property
  // table and the YAML the author wrote.
  it('reads a well-formed frontmatter block as a table or as raw YAML', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ public_mode: true }) },
      '/v1/load_artifact': {
        body: {
          id: 'platform/review',
          type: 'context',
          version: '1.0.0',
          content_hash: 'sha256:abc',
          manifest_body: '# Review\n',
          frontmatter: manifestDoc,
        },
      },
      '/v1/dependents': { body: { edges: [] } },
    });
    goTo('#/artifact/platform%2Freview');
    render(<App />);
    await screen.findByLabelText('Artifact viewer');
    fireEvent.click(screen.getByRole('tab', { name: /Frontmatter/ }));
    expect(screen.getByTestId('frontmatter-table')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Raw YAML' }));
    expect(screen.queryByTestId('frontmatter-table')).toBeNull();
    expect(screen.getByTestId('raw-frontmatter').textContent).toContain('name: review');
    // Nothing marks a line on a block that parsed.
    expect(screen.queryByTestId('offending-line')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Table' }));
    expect(screen.getByTestId('frontmatter-table')).toBeTruthy();
  });

  // load_artifact defaults to the latest version and takes any other, so a
  // reader who picks one is told which version they are reading and is given
  // the way back to the latest.
  it('reads the version the picker names and marks it as an older one', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ public_mode: true }) },
      '/v1/load_artifact': {
        body: {
          id: 'platform/review',
          type: 'context',
          version: '2.3.0',
          content_hash: 'sha256:abc',
          manifest_body: '# Review\n',
          frontmatter: '',
        },
      },
      '/v1/dependents': { body: { edges: [] } },
    });
    goTo('#/artifact/platform%2Freview');
    render(<App />);
    await screen.findByLabelText('Artifact viewer');
    expect(screen.queryByTestId('older-version')).toBeNull();
    stubRegistry({
      '/v1/ui/session': { body: posture({ public_mode: true }) },
      '/v1/load_artifact': {
        body: {
          id: 'platform/review',
          type: 'context',
          version: '1.0.0',
          content_hash: 'sha256:old',
          manifest_body: '# Review\n',
          frontmatter: '',
        },
      },
      '/v1/dependents': { body: { edges: [] } },
    });
    fireEvent.change(screen.getByLabelText('Version'), { target: { value: '1.0.0' } });
    fireEvent.click(screen.getByRole('button', { name: 'View' }));
    const notice = await screen.findByTestId('older-version');
    expect(notice.textContent).toContain('1.0.0');
    expect(screen.getByRole('button', { name: 'Go to 2.3.0' })).toBeTruthy();
    expect(requests.some((r) => r.url.includes('version=1.0.0'))).toBe(true);
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
    const table = screen.getByTestId('rail-frontmatter-table');
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

  it('drops the rail’s frontmatter section where the response yields no pairs', async () => {
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
    // The rail reads as provenance followed directly by relations: the
    // section header goes with the table rather than standing over an empty
    // one, and the tab carries no parse-failure badge.
    expect(screen.queryByTestId('rail-frontmatter-table')).toBeNull();
    expect(screen.queryByLabelText('Frontmatter')).toBeNull();
    expect(screen.getByRole('tab', { name: /Frontmatter/ }).textContent).not.toContain('!');
    fireEvent.click(screen.getByRole('tab', { name: /Frontmatter/ }));
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
          frontmatter: 'name: review\n\tbad: tab\n',
        },
      },
      '/v1/dependents': { body: { edges: [] } },
    });
    goTo('#/artifact/platform%2Freview');
    render(<App />);
    await screen.findByLabelText('Artifact viewer');
    // The parse failure is reported on the tab that opens the block, so a
    // reader on another tab is told the block did not parse.
    expect(screen.getByRole('tab', { name: /Frontmatter/ }).textContent).toContain('!');
    expect(screen.getByTestId('artifact-body').querySelector('h1')?.textContent).toBe('Review');
    fireEvent.click(screen.getByRole('tab', { name: /Frontmatter/ }));
    expect(screen.getAllByText('Invalid syntax').length).toBeGreaterThan(0);
    // The banner carries the parser's own position and the raw block below
    // it marks the line that position names.
    expect(screen.getAllByText(/line 2, column/).length).toBeGreaterThan(0);
    expect(screen.getAllByTestId('offending-line')[0].textContent).toContain('bad: tab');
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
    openRowActions('company');
    openRowActions();
    expect(screen.getAllByRole('button', { name: 'Unregister' }).length).toBe(2);
    expect(screen.queryByText('yours')).toBeNull();
  });

  // Every row shares one grid and the actions column is fixed width, so the
  // row's controls stay on one line: one action plus an overflow control.
  // Rendering Edit, Reingest, and Unregister side by side stacked them and
  // tripled the height of every row.
  it('keeps the row to one action and an overflow control', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ subject: 'alice@acme.com' }) },
      '/v1/layers': { body: { layers: [userLayer()] } },
    });
    goTo('#/layers');
    render(<App />);
    await screen.findByLabelText('Layer panel');
    const cell = screen.getByRole('button', { name: 'Reingest' }).closest('td') as HTMLElement;
    expect(within(cell).getAllByRole('button').map((button) => button.textContent)).toEqual(['Reingest', '⋯']);
    expect(screen.queryByRole('button', { name: 'Edit' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Unregister' })).toBeNull();
    openRowActions();
    expect(screen.getByRole('button', { name: 'Edit' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Unregister' })).toBeTruthy();
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
    // The admin-defined row still shows its stored owner, as the field it
    // is. It carries no ownership language and none of the marker's
    // styling, because the write rule authorizes a tenant admin there and
    // that field names no authorized subject.
    const stored = screen.getByTestId('stored-owner');
    expect(stored.textContent).toBe('owner: alice@acme.com');
    expect(stored.className).not.toContain('badge');
  });

  it('states an unset stored owner on an admin-defined row rather than omitting the field', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ subject: 'alice@acme.com' }) },
      '/v1/layers': { body: { layers: [adminLayer()] } },
    });
    goTo('#/layers');
    render(<App />);
    await screen.findByLabelText('Layer panel');
    expect(screen.getByTestId('stored-owner').textContent).toBe('owner: unset');
    expect(screen.queryByText('yours')).toBeNull();
  });

  // A write can come back refused, including on a row the panel presented as
  // the caller's to manage. The refusal is drawn on the row, says only that
  // the action was refused and nothing changed, and leaves every other
  // control live.
  it('presents a refused write on the row without reporting ownership or session state', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ subject: 'alice@acme.com' }) },
      '/v1/layers': { body: { layers: [userLayer('bob@acme.com')] } },
      'DELETE /v1/layers': { status: 403, body: { code: 'auth.forbidden', message: 'not permitted' } },
    });
    goTo('#/layers');
    render(<App />);
    await screen.findByLabelText('Layer panel');
    openRowActions();
    fireEvent.click(screen.getByRole('button', { name: 'Unregister' }));
    fireEvent.change(screen.getByLabelText('Type the layer ID to confirm'), { target: { value: 'alice-personal' } });
    fireEvent.click(screen.getByRole('button', { name: 'Unregister layer' }));
    expect(await screen.findByText(/nothing changed/)).toBeTruthy();
    expect(screen.getByText('auth.forbidden')).toBeTruthy();
    openRowActions();
    expect(screen.getByRole('button', { name: 'Edit' }).hasAttribute('disabled')).toBe(false);
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
    await screen.findByTestId('session-ended');
    expect((await screen.findByTestId('expiry-sign-in')).getAttribute('href')).toBe('/v1/ui/auth/sign-in');
    // The transition stands over the page the caller was on. The refused
    // screen that stands in place of the catalog belongs to the caller who
    // held no subject, so it is not what this caller gets.
    expect(screen.queryByLabelText('Catalog refused')).toBeNull();
  });

  // The expiry arm keeps the page underneath. A caller reading a domain whose
  // sidebar expansion is then refused keeps the domain surface, with the one
  // sentence over it.
  it('keeps the domain the caller was reading under the transition', async () => {
    stubRegistry({
      '/v1/ui/session': {
        body: posture({
          subject: 'alice@acme.com',
          browser_auth: { enabled: true, sign_in_path: '/v1/ui/auth/sign-in', sign_out_path: '/v1/ui/auth/sign-out' },
        }),
      },
      '/v1/load_domain': { body: { path: 'platform', subdomains: [{ path: 'platform/ci', name: 'ci' }], notable: [] } },
      '/v1/search_artifacts': { body: { total_matched: 0 } },
      '/v1/layers': { body: { layers: [] } },
    });
    goTo('#/domain/platform');
    render(<App />);
    await screen.findByLabelText('Domain browser');
    const tree = await screen.findByLabelText('Catalog');
    stubRegistry({
      '/v1/load_domain': { status: 401, body: { code: 'auth.token_expired', message: 'expired' } },
    });
    fireEvent.click(within(tree).getAllByRole('button', { expanded: false })[0]);
    await screen.findByTestId('session-ended');
    expect(screen.getByLabelText('Domain browser')).toBeTruthy();
    expect(screen.queryByLabelText('Catalog refused')).toBeNull();
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
    await screen.findByTestId('session-ended');
    expect(screen.queryByTestId('expiry-sign-in')).toBeNull();
    expect(screen.getByText(/runs no browser sign-in/)).toBeTruthy();
    // The third row renders no authentication control, so the treatment has to
    // state what it offers in its place, and a retry of the refused read is
    // that control.
    expect(screen.getByTestId('expiry-retry')).toBeTruthy();
  });

  // A posture read that did not answer is a different arm from a deployment
  // that reported the browser flow disabled. It is reachable on any
  // deployment, so the recovery claims nothing about whether a browser
  // sign-in exists and offers the retry alone.
  it('claims no deployment property where the posture read did not answer', async () => {
    stubRegistry({
      '/v1/ui/session': { status: 503, body: { code: 'registry.unavailable', message: 'no posture' } },
      '/v1/load_domain': { status: 401, body: { code: 'auth.untrusted_token', message: 'no identity' } },
    });
    render(<App />);
    await screen.findByLabelText('Catalog refused');
    expect(screen.queryByText(/runs no browser sign-in/)).toBeNull();
    expect(screen.queryByTestId('expiry-sign-in')).toBeNull();
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
    openRowActions('company');
    openRowActions();
    for (const name of ['Register layer', 'Reingest all', 'Reingest', 'Unregister', 'Edit']) {
      for (const control of screen.getAllByRole('button', { name })) {
        expect(control.hasAttribute('disabled')).toBe(true);
      }
    }
    // Reordering is a write too, so the rows carry no drag on a read-only
    // registry rather than committing a move the registry would refuse.
    for (const row of screen.getAllByLabelText(/Drag .* to reorder/)) {
      expect(row.closest('tr')?.getAttribute('draggable')).toBe('false');
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
    fireEvent.click(screen.getByTestId('recoverable-link'));
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
    await screen.findByTestId('session-ended');
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
    openRowActions();
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

  // A browser draws a select and a checkbox from the operating system
  // palette unless the page overrides it, which left the register form with
  // a white select and a white checkbox on a dark surface. The design brief
  // requires every surface to read in both themes off one token set, so the
  // select carries the same border treatment as the text input beside it and
  // the checkbox takes its tick from the accent token.
  it('draws the register form’s select and checkboxes off the token set rather than as native widgets', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ subject: 'alice@acme.com' }) },
      '/v1/layers': { body: { layer: { ID: 'company', SourceType: 'local', Order: 1 } } },
    });
    goTo('#/layers');
    render(<App />);
    await screen.findByLabelText('Layer panel');
    fireEvent.click(screen.getByRole('button', { name: 'Register layer' }));
    fireEvent.change(screen.getByLabelText('Layer class'), { target: { value: 'admin' } });
    const text = window.getComputedStyle(screen.getByLabelText('Layer ID'));
    const select = window.getComputedStyle(screen.getByLabelText('Layer class'));
    expect(select.borderRadius).toBe(text.borderRadius);
    expect(select.borderTopWidth).toBe(text.borderTopWidth);
    expect(select.appearance).toBe('none');
    const box = window.getComputedStyle(screen.getByLabelText('Organization'));
    expect(box.accentColor).toBe('var(--acc)');
    // The text-input rule pads and fills the control, which is the wrong
    // treatment for a checkbox and is what it used to inherit here.
    expect(box.padding).not.toBe(text.padding);
    expect(box.width).toBe('15px');
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


  // The registration reloads the list, and the reload answers over the
  // network rather than within the batch that issued it, so the list read is
  // deferred here. The panel must hold the reveal across a reload that
  // reports loading, because the secret is served once and a panel that
  // remounted the form in its place would leave the reader with no copy.
  it('reveals a git layer’s webhook secret once and holds the reveal until it is acknowledged', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ subject: 'alice@acme.com' }) },
      'GET /v1/layers': { body: { layers: [] }, deferred: true },
      'POST /v1/layers': {
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
    // The reload the registration triggered lands after the reveal paints,
    // and the reveal is still there once it has.
    await waitFor(() => {
      expect(requests.filter((r) => r.url === '/v1/layers' && r.method === 'GET').length).toBeGreaterThan(1);
    });
    expect(screen.getByLabelText('Webhook secret')).toBeTruthy();
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
      '/v1/layers': { body: { layers: [adminLayer()] }, deferred: true },
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
    openRowActions('company');
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
    openRowActions();
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
    // The move commits on drop: the dragged row lands where the row it was
    // dropped onto stood.
    dragRowOnto('alice-personal', 'alice-scratch');
    await waitFor(() => {
      expect(requests.some((r) => r.url === '/v1/layers/reorder' && r.method === 'POST')).toBe(true);
    });
    // The user-defined block is alice-personal, alice-scratch, bob-personal
    // in stored order, and the move puts the first row where the second
    // stood. The whole block is named, bob-personal included, so its
    // rewritten order value keeps it below the pair rather than colliding
    // with them; the registry authorizes each named layer on its own and the
    // panel presents whatever it refuses.
    expect(bodies.at(-1)).toBe(JSON.stringify({ order: ['alice-scratch', 'alice-personal', 'bob-personal'] }));
  });

  // §4.6 composes every user-defined layer above every admin-defined one
  // whatever the stored order values are, so a drop across the class boundary
  // names a move no composition would make and the panel sends nothing.
  it('sends no reorder where the drop crosses the layer-class boundary', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ subject: 'alice@acme.com' }) },
      '/v1/layers': { body: { layers: [adminLayer(), userLayer(), scratchLayer()] } },
      '/v1/layers/reorder': { body: { layers: [] } },
    });
    goTo('#/layers');
    render(<App />);
    await screen.findByLabelText('Layer panel');
    dragRowOnto('alice-personal', 'company');
    await waitFor(() => {
      expect(screen.getByLabelText('Layer panel')).toBeTruthy();
    });
    expect(requests.some((r) => r.url === '/v1/layers/reorder')).toBe(false);
  });

  // The fan-out issues one request per layer in sequence. A row changes only
  // when its own request returns, so the panel fabricates no progress and a
  // row shows what its own response carried.
  it('reingests every layer in sequence and moves each row on its own response', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ subject: 'alice@acme.com' }) },
      '/v1/layers': { body: { layers: [adminLayer(), userLayer()] } },
      '/v1/layers/reingest': { body: { accepted: 1, idempotent: 0 } },
    });
    goTo('#/layers');
    render(<App />);
    await screen.findByLabelText('Layer panel');
    fireEvent.click(screen.getByRole('button', { name: 'Reingest all' }));
    await waitFor(() => {
      expect(screen.getByLabelText('Reingest result for company')).toBeTruthy();
      expect(screen.getByLabelText('Reingest result for alice-personal')).toBeTruthy();
    });
    const reingests = requests.filter((r) => r.url.startsWith('/v1/layers/reingest'));
    expect(reingests.length).toBe(2);
    expect(reingests[0].url).toContain('id=company');
    expect(reingests[1].url).toContain('id=alice-personal');
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
    await screen.findByLabelText('Reingest result for alice-personal');
    expect(screen.getByText('4 accepted')).toBeTruthy();
    expect(screen.getByText('2 unchanged')).toBeTruthy();
    expect(screen.getByText('1 lint failures')).toBeTruthy();
    expect(screen.getByLabelText('Rejected artifacts')).toBeTruthy();
    expect(screen.getByText('platform/lint@1.0.0')).toBeTruthy();
    expect(screen.getByLabelText('Advisories')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Done' }));
    expect(screen.queryByLabelText('Reingest result for alice-personal')).toBeNull();
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

  // A reingest inside a §4.7.2 freeze window is refused with ingest.frozen,
  // and the same endpoint takes the break-glass override, so the arm offers
  // it rather than leaving the reader with a refusal and no next action. The
  // registry requires a justification and the freeze rule two distinct
  // approvers, so the override carries all three.
  it('offers the break-glass override where a freeze window refused the reingest', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ subject: 'alice@acme.com' }) },
      '/v1/layers': { body: { layers: [userLayer()] } },
      '/v1/layers/reingest': {
        status: 409,
        body: { code: 'ingest.frozen', message: 'a freeze window is active', retryable: false },
      },
    });
    goTo('#/layers');
    render(<App />);
    await screen.findByLabelText('Layer panel');
    fireEvent.click(screen.getByRole('button', { name: 'Reingest' }));
    await screen.findByLabelText('Reingest alice-personal during a freeze window');
    const override = screen.getByRole('button', { name: 'Reingest during the freeze' });
    // The override stays held until the justification and two distinct
    // approvers are in place, because the registry refuses it without them.
    expect(override.hasAttribute('disabled')).toBe(true);
    fireEvent.change(screen.getByLabelText('Justification'), { target: { value: 'incident 7' } });
    fireEvent.change(screen.getByLabelText('First approver'), { target: { value: 'alice@acme.com' } });
    expect(override.hasAttribute('disabled')).toBe(true);
    fireEvent.change(screen.getByLabelText('Second approver'), { target: { value: 'bob@acme.com' } });
    fireEvent.click(override);
    await waitFor(() => {
      expect(bodies.at(-1)).toBe(
        JSON.stringify({
          break_glass: true,
          justification: 'incident 7',
          approvers: ['alice@acme.com', 'bob@acme.com'],
        }),
      );
    });
  });

  // Every other reingest refusal carries its own remediation in the
  // envelope, and the codes the pipeline answers with have different next
  // actions, so the arm presents the envelope's message and suggested action
  // rather than one line that fits none of them.
  it('presents a refused reingest with the envelope’s own message and remediation', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ subject: 'alice@acme.com' }) },
      '/v1/layers': { body: { layers: [userLayer()] } },
      '/v1/layers/reingest': {
        status: 422,
        body: {
          code: 'ingest.lint_failed',
          message: '3 artifacts failed the lint gate',
          retryable: false,
          suggested_action: 'Fix the reported manifests and reingest.',
        },
      },
    });
    goTo('#/layers');
    render(<App />);
    await screen.findByLabelText('Layer panel');
    fireEvent.click(screen.getByRole('button', { name: 'Reingest' }));
    const refused = await screen.findByLabelText('Reingest refused');
    expect(refused.textContent).toContain('3 artifacts failed the lint gate');
    expect(refused.textContent).toContain('Fix the reported manifests and reingest.');
    expect(refused.textContent).toContain('ingest.lint_failed');
    // The envelope reports that the condition does not clear on its own, so
    // the arm offers no retry.
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

  // The recovery surface answers how long is left before erasure, so every
  // row states when the layer was unregistered, the date it is erased on,
  // and how much of the §8.4 window remains. A row inside the accent window
  // says so, because that is the row to act on today.
  it('lists what is still recoverable with its erase date and restores it', async () => {
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
    const unregisteredAt = new Date(Date.now() - 28 * 24 * 60 * 60 * 1000);
    stubRegistry({
      '/v1/ui/session': { body: posture({ subject: 'alice@acme.com' }) },
      '/v1/layers': { body: { layers: [{ ...userLayer(), DeletedAt: unregisteredAt.toISOString() }] } },
      '/v1/layers/restore': { body: {} },
    });
    fireEvent.click(screen.getByTestId('recoverable-link'));
    const surface = await screen.findByLabelText('Recently unregistered');
    const erasesOn = new Date(unregisteredAt.getTime() + 30 * 24 * 60 * 60 * 1000).toISOString().slice(0, 10);
    expect(surface.textContent).toContain(unregisteredAt.toISOString().slice(0, 10));
    expect(surface.textContent).toContain(erasesOn);
    const left = screen.getByTestId('days-left-alice-personal');
    expect(left.textContent).toBe('1 days left');
    expect(left.className).toContain('accent');
    // The source is on the same record, so the row names where the layer
    // came from rather than its identifier alone.
    expect(surface.textContent).toContain('/Users/alice/registry');
    fireEvent.click(await screen.findByRole('button', { name: 'Restore' }));
    await waitFor(() => {
      expect(requests.some((r) => r.url.startsWith('/v1/layers/restore') && r.method === 'POST')).toBe(true);
    });
  });

  // A record carrying no tombstone time states that rather than computing a
  // date from a value it does not hold.
  it('states no erase date where the record carries no unregistered time', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ subject: 'alice@acme.com' }) },
      '/v1/layers': { body: { layers: [{ ...userLayer(), DeletedAt: null }] } },
    });
    goTo('#/layers');
    render(<App />);
    await screen.findByLabelText('Layer panel');
    fireEvent.click(screen.getByTestId('recoverable-link'));
    const surface = await screen.findByLabelText('Recently unregistered');
    expect(surface.textContent).toContain('The registry reported no erase date.');
  });
});

/** dragRowOnto drives the panel's drag-to-reorder: the row is picked up by
 * its handle and dropped onto another row, and the move commits on the drop.
 */
function dragRowOnto(from: string, onto: string): void {
  const source = layerRow(from);
  const target = layerRow(onto);
  fireEvent.dragStart(source);
  fireEvent.dragOver(target);
  fireEvent.drop(target);
}

function layerRow(id: string): HTMLElement {
  const row = screen.getByLabelText(`Drag ${id} to reorder`).closest('tr');
  if (row === null) {
    throw new Error(`no layer row for ${id}`);
  }
  return row;
}

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

/** openRowActions opens one row's overflow control, which is where Edit and
 * Unregister live so that every row keeps to a single line. */
function openRowActions(layerID = 'alice-personal'): void {
  fireEvent.click(screen.getByRole('button', { name: `More actions for ${layerID}` }));
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

describe('the command palette', () => {
  const artifact = {
    id: 'platform/review',
    type: 'skill',
    version: '1.2.0',
    content_hash: 'sha256:abc',
    manifest_body: '# Review\n',
    frontmatter: manifestDoc,
  };

  function palettePage(results: Record<string, unknown>[], total = results.length): void {
    stubRegistry({
      '/v1/ui/session': { body: posture({ public_mode: true }) },
      '/v1/load_domain': { body: emptyDomain },
      '/v1/search_artifacts': { body: { total_matched: total, results } },
      '/v1/load_artifact': { body: artifact },
      '/v1/dependents': { body: { edges: [] } },
      '/v1/layers': { body: { layers: [] } },
    });
  }

  // The palette is reachable from anywhere: the shell's search trigger opens
  // it and so does the accelerator, and it lists artifacts alone, because
  // domain navigation is the sidebar tree's.
  it('opens from the trigger and from ⌘K, lists what matched, and opens a row', async () => {
    palettePage([{ id: 'platform/review', type: 'skill', version: '1.2.0' }], 4);
    render(<App />);
    fireEvent.click(await screen.findByTestId('search-trigger'));
    const panel = screen.getByTestId('palette');
    fireEvent.change(within(panel).getByLabelText('Search artifacts'), { target: { value: 'review' } });
    expect((await screen.findByTestId('palette-heading')).textContent).toBe('Artifacts · 1 of 4');
    expect(within(panel).getByText('review')).toBeTruthy();
    fireEvent.keyDown(panel, { key: 'Enter' });
    expect(window.location.hash).toBe('#/artifact/platform%2Freview');
    expect(screen.queryByTestId('palette')).toBeNull();
    // The accelerator opens the same panel from the surface the navigation
    // landed on.
    fireEvent.keyDown(window, { key: 'k', metaKey: true });
    expect(screen.getByTestId('palette')).toBeTruthy();
  });

  // The inline filter syntax is the palette's form of the pills the search
  // surface renders, and it reaches the same endpoint arguments.
  it('carries the inline filter syntax into the search request', async () => {
    palettePage([]);
    render(<App />);
    fireEvent.click(await screen.findByTestId('search-trigger'));
    fireEvent.change(within(screen.getByTestId('palette')).getByLabelText('Search artifacts'), {
      target: { value: 'type:skill tag:review scope:platform lint' },
    });
    await waitFor(() => {
      expect(lastSearch().get('query')).toBe('lint');
    });
    expect(lastSearch().get('type')).toBe('skill');
    expect(lastSearch().get('tags')).toBe('review');
    expect(lastSearch().get('scope')).toBe('platform');
  });

  // ⌘⏎ hands the query to the search surface, which is the one place the
  // whole result set is listed, and esc closes the panel over the page it
  // was opened from.
  it('hands the query to the search surface on ⌘⏎ and closes on esc', async () => {
    palettePage([{ id: 'platform/review', type: 'skill' }]);
    render(<App />);
    fireEvent.click(await screen.findByTestId('search-trigger'));
    const panel = screen.getByTestId('palette');
    fireEvent.change(within(panel).getByLabelText('Search artifacts'), { target: { value: 'review' } });
    fireEvent.keyDown(panel, { key: 'Enter', metaKey: true });
    expect(window.location.hash).toBe('#/search/review');
    await screen.findByLabelText('Search');
    fireEvent.keyDown(window, { key: 'k', metaKey: true });
    fireEvent.keyDown(screen.getByTestId('palette'), { key: 'Escape' });
    expect(screen.queryByTestId('palette')).toBeNull();
  });

  // The handoff carries the filters the palette parsed rather than the line
  // read back as free text: the search surface issues the request the palette
  // issued and renders the filters as the pills the syntax teaches.
  it('reproduces the palette’s filters and result set on the search surface', async () => {
    palettePage([{ id: 'platform/review', type: 'skill' }], 3);
    render(<App />);
    fireEvent.click(await screen.findByTestId('search-trigger'));
    const panel = screen.getByTestId('palette');
    fireEvent.change(within(panel).getByLabelText('Search artifacts'), {
      target: { value: 'type:skill tag:review scope:platform lint' },
    });
    await waitFor(() => {
      expect(lastSearch().get('type')).toBe('skill');
    });
    fireEvent.keyDown(panel, { key: 'Enter', metaKey: true });
    await screen.findByLabelText('Search');
    await waitFor(() => {
      expect(lastSearch().get('query')).toBe('lint');
    });
    expect(lastSearch().get('type')).toBe('skill');
    expect(lastSearch().get('tags')).toBe('review');
    expect(lastSearch().get('scope')).toBe('platform');
    // The parsed filters are the pills the surface opens with, so the reader
    // can drop one from the row the palette's syntax taught.
    expect(within(screen.getByLabelText('Scope')).getByText('platform')).toBeTruthy();
    expect(screen.getByLabelText('Remove the review filter')).toBeTruthy();
    expect(screen.getByLabelText('Remove the skill filter')).toBeTruthy();
  });

  // A query that matched nothing offers the recovery path and says nothing
  // about what a different caller would have seen.
  it('states no match without hinting that anything is hidden', async () => {
    palettePage([], 0);
    render(<App />);
    fireEvent.click(await screen.findByTestId('search-trigger'));
    const panel = screen.getByTestId('palette');
    // The just-opened panel teaches the filter syntax before a query is run.
    expect(screen.getByTestId('palette-syntax').textContent).toContain('type:skill');
    fireEvent.change(within(panel).getByLabelText('Search artifacts'), { target: { value: 'nothingmatches' } });
    expect(await screen.findByText(/Nothing matched nothingmatches/)).toBeTruthy();
    expect(within(panel).queryByText(/hidden/i)).toBeNull();
    expect(within(panel).queryByText(/permission/i)).toBeNull();
  });
});

describe('the shell’s identity cluster', () => {
  beforeEach(() => {
    window.localStorage.clear();
    window.document.documentElement.removeAttribute('data-theme');
  });

  // The shell names the registry the page is served from, links the
  // documentation, and carries the trigger that opens the palette.
  it('names the registry, links the docs, and carries the search trigger', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ public_mode: true }) },
      '/v1/load_domain': { body: emptyDomain },
    });
    render(<App />);
    expect((await screen.findByTestId('registry-host')).textContent).toBe(window.location.host);
    expect(screen.getByRole('link', { name: /Docs/ }).getAttribute('href')).toContain('https://');
    expect(screen.getByTestId('search-trigger').textContent).toContain('⌘K');
  });

  // The appearance preference is the client's own state, and it is applied by
  // stamping data-theme on the root element, which is what overrides the
  // visitor's prefers-color-scheme in both directions.
  it('pins a theme onto the root element and returns it to the system setting', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ subject: 'alice@acme.com' }) },
      '/v1/load_domain': { body: emptyDomain },
    });
    render(<App />);
    const cluster = await screen.findByTestId('account-trigger');
    expect(cluster.textContent).toContain('alice@acme.com');
    fireEvent.click(cluster);
    const menu = screen.getByTestId('account-menu');
    fireEvent.click(within(menu).getByRole('button', { name: 'dark' }));
    expect(window.document.documentElement.getAttribute('data-theme')).toBe('dark');
    expect(window.localStorage.getItem('podium.theme')).toBe('dark');
    fireEvent.click(within(menu).getByRole('button', { name: 'system' }));
    expect(window.document.documentElement.hasAttribute('data-theme')).toBe(false);
  });

  // The menu carries the layer quota, read from the §4.7.8 endpoint the
  // registry gates on no role, so the caller sees the cap on how many layers
  // of their own they may hold.
  it('states the layer quota the registry reports', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ subject: 'alice@acme.com' }) },
      '/v1/load_domain': { body: emptyDomain },
      '/v1/quota': { body: { tenant_id: 'acme', limits: { MaxUserLayers: 3 } } },
    });
    render(<App />);
    fireEvent.click(await screen.findByTestId('account-trigger'));
    expect((await screen.findByTestId('layer-quota')).textContent).toBe('3 user-defined layers');
  });

  // A quota read that fails leaves the menu with no quota entry rather than a
  // figure no response carried.
  it('drops the quota entry where the read does not answer', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ subject: 'alice@acme.com' }) },
      '/v1/load_domain': { body: emptyDomain },
      '/v1/quota': { status: 503, body: { code: 'registry.unavailable', message: 'down' } },
    });
    render(<App />);
    fireEvent.click(await screen.findByTestId('account-trigger'));
    await screen.findByTestId('account-menu');
    await waitFor(() => {
      expect(requests.some((r) => r.url === '/v1/quota')).toBe(true);
    });
    expect(screen.queryByTestId('layer-quota')).toBeNull();
  });

  // A tenant whose quota disables the cap holds any number of layers, which
  // the entry states rather than reporting the negative value.
  it('states a disabled cap as no limit', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ subject: 'alice@acme.com' }) },
      '/v1/load_domain': { body: emptyDomain },
      '/v1/quota': { body: { limits: { MaxUserLayers: -1 } } },
    });
    render(<App />);
    fireEvent.click(await screen.findByTestId('account-trigger'));
    expect((await screen.findByTestId('layer-quota')).textContent).toBe('no cap on your layers');
  });
});

describe('the trimmed listing', () => {
  const trimmed = {
    path: 'platform',
    subdomains: [],
    notable: [
      { id: 'platform/deploy', type: 'skill' },
      { id: 'platform/lint', type: 'skill' },
    ],
    note: 'The listing was trimmed to fit the response budget.',
  };

  // The trimmed case is a pill among the header badges and a line at the end
  // of the list stating what is on the page against the match count, with a
  // control that continues past the returned edge. The continuation is the
  // scoped search the line takes its total from, because load_domain offers
  // no lever over the notable list.
  it('states the shown count against the total and continues into the scoped search', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ public_mode: true }) },
      '/v1/load_domain': { body: trimmed },
      '/v1/search_artifacts': { body: { total_matched: 21 } },
    });
    goTo('#/domain/platform');
    render(<App />);
    await screen.findByLabelText('Domain browser');
    expect(screen.getByText('listing trimmed')).toBeTruthy();
    const line = await screen.findByTestId('listing-continuation');
    await waitFor(() => {
      expect(line.textContent).toContain('2 of 21 artifacts shown.');
    });
    const cont = within(line).getByRole('link', { name: 'Load the rest' });
    expect(cont.getAttribute('href')).toBe(searchHref('scope:platform'));
    goTo(searchHref('scope:platform'));
    await screen.findByLabelText('Search');
    await waitFor(() => {
      expect(requests.some((r) => r.url.startsWith('/v1/search_artifacts') && r.url.includes('scope=platform'))).toBe(
        true,
      );
    });
    // Raising the subtree depth is what the control must not do: the notable
    // list is capped independently of it, so a deeper read returns no
    // artifact the reader does not already hold.
    expect(requests.some((r) => r.url.startsWith('/v1/load_domain') && r.url.includes('depth=3'))).toBe(false);
  });

  // A domain with dozens of children is a map rather than a card grid, so the
  // subdomains become count tiles under a filter and the artifacts a sortable
  // table.
  it('switches to tiles and a sortable table past the at-scale threshold', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ public_mode: true }) },
      '/v1/load_domain': {
        body: {
          path: 'platform',
          subdomains: Array.from({ length: 24 }, (_, i) => ({ path: `platform/d${String(i)}`, name: `d${String(i)}` })),
          notable: [
            { id: 'platform/deploy', type: 'skill', version: '2.0.0', source: 'featured' },
            { id: 'platform/lint', type: 'rule', version: '1.0.0' },
          ],
        },
      },
    });
    goTo('#/domain/platform');
    render(<App />);
    const browser = await screen.findByLabelText('Domain browser');
    expect(within(browser).getByTestId('show-all-subdomains').textContent).toBe('Show all 24 subdomains');
    fireEvent.change(within(browser).getByLabelText('Filter subdomains'), { target: { value: 'd1' } });
    expect(within(browser).queryByRole('link', { name: 'd2' })).toBeNull();
    // The author's own picks keep their own heading, and the table sorts on
    // the column the header names.
    expect(within(browser).getByText('Curated by the domain author')).toBeTruthy();
    const tables = within(browser).getAllByLabelText('Artifacts');
    expect(within(tables[0]).getByRole('link', { name: 'platform/deploy' })).toBeTruthy();
  });
});

describe('the anonymous framing', () => {
  // Where the catalog read answers and the posture read does not, the page is
  // on the public-subset arm and takes that arm's treatment: it presents what
  // the catalog read returned, states that no subject resolved, and claims
  // nothing about content beyond what was returned. The authentication
  // controls key on the posture read, so a read that did not answer renders
  // neither of them.
  it('takes the public-subset treatment where the posture read did not answer', async () => {
    stubRegistry({
      '/v1/ui/session': { status: 503, body: { code: 'registry.unavailable', message: 'down' } },
      '/v1/load_domain': {
        body: { path: '', subdomains: [], notable: [{ id: 'platform/deploy', type: 'skill' }] },
      },
    });
    render(<App />);
    expect(await screen.findByText('platform/deploy')).toBeTruthy();
    expect(screen.getByTestId('anonymous-banner')).toBeTruthy();
    expect(screen.getByText('Not signed in')).toBeTruthy();
    expect(screen.queryByTestId('sign-in')).toBeNull();
    expect(screen.queryByTestId('sign-out')).toBeNull();
    // The arm says nothing about content having been withheld.
    expect(screen.queryByText(/hidden/i)).toBeNull();
    expect(screen.queryByText(/withheld/i)).toBeNull();
  });
});

describe('the artifact viewer’s resources', () => {
  function resourcePage(): void {
    stubRegistry({
      '/v1/ui/session': { body: posture({ public_mode: true }) },
      '/v1/load_artifact': {
        body: {
          id: 'platform/review',
          type: 'context',
          version: '1.0.0',
          content_hash: 'sha256:abc',
          manifest_body: '# Review\n',
          frontmatter: '',
          resources: { 'checklist.md': 'body' },
          large_resources: {
            'corpus.bin': {
              presigned_url: 'https://objects.acme.com/corpus',
              content_hash: 'sha256:def',
              size: 2 * 1024 * 1024,
              content_type: 'application/octet-stream',
            },
          },
        },
      },
      '/v1/dependents': { body: { edges: [] } },
    });
    goTo('#/artifact/platform%2Freview');
  }

  // The rail splits the two deliveries, because a file that arrived with the
  // response and one that is fetched on demand cost the reader different
  // things to open.
  it('splits the rail into the inline files and the ones fetched on demand', async () => {
    resourcePage();
    render(<App />);
    await screen.findByLabelText('Artifact viewer');
    const section = screen.getByLabelText('Bundled resources');
    const groups = section.querySelectorAll('.rail-group');
    expect(groups.length).toBe(2);
    expect(groups[0].textContent).toContain('Inline');
    expect(groups[0].textContent).toContain('checklist.md');
    expect(groups[1].textContent).toContain('Fetched on demand');
    expect(groups[1].textContent).toContain('corpus.bin');
  });

  // The tab keeps the two deliveries as one list, takes the whole set at
  // once from the control above the table, and opens the selected row's
  // detail card under it.
  it('offers the whole set above the table and details the selected row under it', async () => {
    resourcePage();
    render(<App />);
    await screen.findByLabelText('Artifact viewer');
    fireEvent.click(screen.getByRole('tab', { name: /Resources/ }));
    // The total is the two files together: four inline bytes and two
    // megabytes fetched on demand.
    expect(screen.getByTestId('download-all').textContent).toBe('Download all ↓ 2.0 MB');
    expect(screen.queryByTestId('resource-detail')).toBeNull();
    const rows = within(screen.getByLabelText('Resources')).getAllByRole('row').slice(1);
    fireEvent.click(rows[1]);
    const detail = screen.getByTestId('resource-detail');
    expect(detail.textContent).toContain('corpus.bin');
    expect(detail.textContent).toContain('fetched on demand');
    expect(rows[1].className).toContain('row-selected');
  });
});

describe('a refused layer write', () => {
  function refusedPage(): void {
    stubRegistry({
      '/v1/ui/session': { body: posture({ subject: 'alice@acme.com' }) },
      '/v1/layers': { body: { layers: [userLayer('bob@acme.com')] } },
      '/v1/layers?deleted=true': { body: { layers: [] } },
      'DELETE /v1/layers': { status: 403, body: { code: 'auth.forbidden', message: 'not permitted' } },
    });
    goTo('#/layers');
  }

  async function refuseAnUnregister(): Promise<void> {
    await screen.findByLabelText('Layer panel');
    openRowActions();
    fireEvent.click(screen.getByRole('button', { name: 'Unregister' }));
    fireEvent.change(screen.getByLabelText('Type the layer ID to confirm'), { target: { value: 'alice-personal' } });
    fireEvent.click(screen.getByRole('button', { name: 'Unregister layer' }));
    await screen.findByText(/nothing changed/);
  }

  // The refusal is drawn on the row with a Try again beside it, and the
  // control re-issues the write that was refused rather than a fresh guess
  // at it.
  it('re-issues the refused write from the row', async () => {
    refusedPage();
    render(<App />);
    await refuseAnUnregister();
    const sent = requests.filter((r) => r.method === 'DELETE').length;
    fireEvent.click(within(screen.getByRole('alert')).getByRole('button', { name: 'Try again' }));
    await waitFor(() => {
      expect(requests.filter((r) => r.method === 'DELETE').length).toBe(sent + 1);
    });
  });

  // Dismiss clears the row's refusal without driving another write, which is
  // the only other way out of the state.
  it('clears the refusal on dismiss and drives no write', async () => {
    refusedPage();
    render(<App />);
    await refuseAnUnregister();
    const sent = requests.filter((r) => r.method === 'DELETE').length;
    fireEvent.click(within(screen.getByRole('alert')).getByRole('button', { name: 'Dismiss' }));
    await waitFor(() => {
      expect(screen.queryByText(/nothing changed/)).toBeNull();
    });
    expect(requests.filter((r) => r.method === 'DELETE').length).toBe(sent);
    // Every other control on the row stayed live throughout.
    openRowActions();
    expect(screen.getByRole('button', { name: 'Edit' }).hasAttribute('disabled')).toBe(false);
  });

  // The recoverable link leads the action row and states how much is still
  // restorable, which is the one piece of panel state naming something on
  // its way to being erased.
  it('states the recoverable count on the panel’s first action', async () => {
    stubRegistry({
      '/v1/ui/session': { body: posture({ subject: 'alice@acme.com' }) },
      '/v1/layers': { body: { layers: [userLayer()] } },
      '/v1/layers?deleted=true': {
        body: { layers: [{ ...userLayer(), ID: 'alice-old', DeletedAt: new Date().toISOString() }] },
      },
    });
    goTo('#/layers');
    render(<App />);
    await screen.findByLabelText('Layer panel');
    const link = screen.getByTestId('recoverable-link');
    await waitFor(() => {
      expect(link.textContent).toBe('↺ Recently unregistered · 1');
    });
    // It is the first control in the action row, ahead of the primary
    // Register layer and the secondary Reingest all.
    const actions = within(link.parentElement as HTMLElement).getAllByRole('button');
    expect(actions.map((button) => button.textContent?.slice(0, 8))).toEqual(['↺ Recent', 'Register', 'Reingest']);
  });
});
