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

import {
  cleanup,
  createEvent,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { App } from "./App";
import { invalidateDomainReads } from "./api";
import { parseQueryLine } from "./query";
import {
  artifactHref,
  deletedLayersHref,
  domainHref,
  layersHref,
  searchHref,
} from "./route";
import type { SessionPosture } from "./session";
// The stylesheet is imported for its own sake: the wrapping rule the rail
// depends on is asserted from the computed style it produces.
import "./index.css";

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
  /** rejects makes the call fail the way the browser fails a request that
   * never reached the registry: the fetch promise rejects with a TypeError
   * and there is no response to read. */
  rejects?: boolean;
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
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      requests.push({ url, method });
      if (typeof init?.body === "string") {
        bodies.push(init.body);
      }
      const path = url.split("?")[0];
      // A path a surface both reads and writes takes a method-qualified key
      // where the two answer differently, and the bare path otherwise.
      // A path whose query argument selects a different response takes the
      // whole URL as its key, which is how the deleted-layer read is told
      // apart from the layer list it shares a path with.
      const stub = (url === path ? undefined : stubs[url]) ??
        stubs[`${method} ${path}`] ??
        stubs[path] ?? {
          status: 404,
          body: { code: "registry.not_found", message: "no stub" },
        };
      if (stub.rejects === true) {
        return Promise.reject(new TypeError("Failed to fetch"));
      }
      const status = stub.status ?? 200;
      const answer = () =>
        new Response(stub.text ?? JSON.stringify(stub.body ?? {}), {
          status,
          headers: {
            "content-type":
              stub.text === undefined ? "application/json" : "text/markdown",
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

/** catalogOf is a §4.5.2 catalog answer holding `count` distinct artifact
 * IDs, which is the figure the sidebar footer states. The catalog carries one
 * ID per artifact however many versions it holds, so a fixture that stands in
 * for a republished catalog is the same listing at the same length. */
function catalogOf(count: number): { ids: string[] } {
  return {
    ids: Array.from(
      { length: count },
      (_, i) => `platform/svc${String(i + 1)}`,
    ),
  };
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
const manifestDoc = "---\nname: review\ntags:\n  - security\n---\n";

/** ingestedLayer is a layer record carrying the grants and the ingest run the
 * artifact viewer's provenance rail states. */
function ingestedLayer(): Record<string, unknown> {
  return {
    ID: "acme-platform",
    SourceType: "git",
    Repo: "git@github.com:acme/platform.git",
    Ref: "main",
    Order: 1,
    Organization: true,
    last_ingested_at: new Date(Date.now() - 7200000).toISOString(),
    LastIngestedRef: "4f2a1c9de4471b1e8f0c2a5d6e7b8c9a0d1e2f34",
  };
}

/** payInvoice is a load_artifact answer whose layer is the record above. */
function payInvoice(): Record<string, unknown> {
  return {
    id: "finance/ap/pay-invoice",
    type: "skill",
    version: "1.0.0",
    content_hash: "sha256:ab74",
    layer: "acme-platform",
    manifest_body: "# Pay invoice\n",
    frontmatter: manifestDoc,
  };
}

/** railFacts reads the rail's provenance block as label and value pairs. */
function railFacts(provenance: HTMLElement): (string | null | undefined)[][] {
  const facts = within(provenance).getByTestId("rail-provenance");
  return [...facts.querySelectorAll(".rail-fact")].map((row) => [
    row.querySelector("dt")?.textContent,
    row.querySelector("dd")?.textContent,
  ]);
}

const emptyDomain = {
  path: "",
  subdomains: [],
  notable: [],
};

/** rootDomains is the registry root as the filter row reads it: the top-level
 * domains are what the scope dropdown offers. */
const rootDomains = {
  path: "",
  subdomains: [
    { path: "platform", name: "platform" },
    { path: "finance", name: "finance" },
  ],
  notable: [],
};

function goTo(hash: string): void {
  window.location.hash = hash;
}

/** lastSearch is the query string of the most recent search the page issued,
 * which is what a case asserting a filter reads. */
function lastSearch(): URLSearchParams {
  const last =
    requests.filter((r) => r.url.startsWith("/v1/search_artifacts")).at(-1)
      ?.url ?? "";
  return new URLSearchParams(last.split("?")[1] ?? "");
}

/** addToken drives the filter row's token entry, which is how a tag is
 * added. */
function addToken(label: string, value: string): void {
  fireEvent.click(screen.getByRole("button", { name: `+ ${label}` }));
  fireEvent.change(screen.getByLabelText(`Add a ${label} filter`), {
    target: { value },
  });
  fireEvent.click(screen.getByRole("button", { name: "Add" }));
}

/** selectFilter drives the filter row's dropdown, which is how the two
 * enumerable filters, the type and the scope, are applied. */
function selectFilter(label: string, value: string): void {
  fireEvent.change(screen.getByLabelText(`Filter by ${label}`), {
    target: { value },
  });
}

/** scrolledIntoView records what a surface scrolled to. jsdom implements no
 * scrolling and leaves `scrollIntoView` undefined, so the method is installed
 * here and a case that asserts a control was brought into view reads what it
 * was called on. */
const scrolledIntoView: Element[] = [];

/** openVersionPicker discloses the artifact viewer's version field from the
 * badge in the header, which is where the affordance lives. */
function openVersionPicker(): void {
  fireEvent.click(screen.getByRole("button", { name: /^Version / }));
}

/** pinVersion reads the open artifact at another version through the header
 * disclosure. */
function pinVersion(version: string): void {
  openVersionPicker();
  fireEvent.change(screen.getByLabelText("Version"), {
    target: { value: version },
  });
  fireEvent.click(screen.getByRole("button", { name: "View" }));
}

beforeEach(() => {
  // The held load_domain answers outlive a render, so a case starts against a
  // registry it stubbed itself rather than against the previous case's.
  invalidateDomainReads();
  requests.length = 0;
  bodies.length = 0;
  scrolledIntoView.length = 0;
  Element.prototype.scrollIntoView = function record(this: Element) {
    scrolledIntoView.push(this);
  };
  goTo("#/");
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

/** zonedDate is the calendar date an instant fell on in the given zone, in
 * the short form the recovery surface states its dates in. The day, the
 * month, and the year come from the platform formatter rather than from the
 * surface's own arithmetic, so the assertion does not restate what it
 * checks. */
function zonedDate(at: Date, timeZone: string): string {
  const parts = new Intl.DateTimeFormat("en-US", {
    day: "numeric",
    month: "short",
    year: "numeric",
    timeZone,
  }).formatToParts(at);
  const value = (type: string) =>
    parts.find((part) => part.type === type)?.value ?? "";
  return `${value("day")} ${value("month")} ${value("year")}`;
}

/** localDate is the calendar date an instant fell on in the zone the suite
 * runs in, which is the zone the recovery surface states its dates in. */
function localDate(at: Date): string {
  return zonedDate(at, Intl.DateTimeFormat().resolvedOptions().timeZone);
}

describe("the application shell", () => {
  const catalog = {
    path: "",
    subdomains: [
      {
        path: "platform",
        name: "platform",
        subdomains: [{ path: "platform/ci", name: "ci" }],
      },
    ],
    notable: [],
  };

  // The shell is one layout on every screen: the nav, the catalog label, the
  // tree, and the counts footer pinned under it. The tree is eager to two
  // levels and reads a deeper level when the reader expands the node it
  // hangs under.
  it("renders the catalog tree, reads a deeper level on expand, and states the counts", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": { body: catalog },
      "/v1/catalog": { body: catalogOf(312) },
      "/v1/layers": {
        body: {
          layers: [
            adminLayer(),
            { ...userLayer(), last_ingested_at: new Date().toISOString() },
          ],
        },
      },
    });
    render(<App />);
    const tree = await screen.findByLabelText("Catalog");
    // Both eager levels are in the response, so the second one is rendered
    // from it rather than read again.
    fireEvent.click(
      within(tree).getAllByRole("button", { expanded: false })[0],
    );
    expect(within(tree).getByText("ci")).toBeTruthy();
    expect(await screen.findByTestId("catalog-counts")).toBeTruthy();
    await waitFor(() => {
      expect(screen.getByTestId("catalog-counts").textContent).toBe(
        "2 layers · 312 artifacts",
      );
    });
    expect(screen.getByTestId("catalog-ingest").textContent).toBe(
      "ingested 0m ago",
    );
    // The level below the eager edge is unknown until the reader asks for
    // it, and asking is what reads it.
    fireEvent.click(
      within(tree).getAllByRole("button", { expanded: false })[0],
    );
    await waitFor(() => {
      expect(requests.some((r) => r.url.includes("path=platform%2Fci"))).toBe(
        true,
      );
    });
  });

  // The footer states a figure, and a figure of one is stated in the singular
  // the way every other count on the page is. A registry holding one layer
  // reading "1 layers" is the shell mis-stating what it read.
  it("states a count of one in the singular in the counts footer", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": { body: catalog },
      "/v1/catalog": { body: catalogOf(1) },
      "/v1/layers": { body: { layers: [adminLayer()] } },
    });
    render(<App />);
    await waitFor(() => {
      expect(screen.getByTestId("catalog-counts").textContent).toBe(
        "1 layer · 1 artifact",
      );
    });
  });

  // The footer states how many artifacts the catalog holds, and the §4.5.2
  // catalog carries one canonical ID per artifact. An unfiltered search
  // answers a different question: its match count is one row per artifact
  // version, so a catalog of two artifacts one of which was republished four
  // times reported six and the footer contradicted the tree beside it.
  it("counts artifacts rather than versions in the counts footer", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": { body: catalog },
      "/v1/catalog": {
        body: { ids: ["eng/deploy", "finance/ap/pay-invoice"] },
      },
      // Every version of eng/deploy is its own search row.
      "/v1/search_artifacts": { body: { total_matched: 6 } },
      "/v1/layers": { body: { layers: [adminLayer()] } },
    });
    render(<App />);
    await waitFor(() => {
      expect(screen.getByTestId("catalog-counts").textContent).toBe(
        "1 layer · 2 artifacts",
      );
    });
  });

  // The label states how deep the §4.2 hierarchy runs. The figure is read
  // from the untruncated §4.5.2 catalog listing rather than from the tree,
  // whose eager read stops at the prefetch depth: a marker drawn from the
  // tree read "2 levels" beside a catalog holding a chain five domains long.
  it("states the catalog's own depth beside the label", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        body: {
          path: "",
          subdomains: [{ path: "a/b/c/d/e", name: "e" }],
          notable: [],
        },
      },
      "/v1/catalog": { body: { ids: ["a/one", "a/b/c/d/e/deep"] } },
      "/v1/layers": { body: { layers: [adminLayer()] } },
    });
    render(<App />);
    const sidebar = within(await screen.findByLabelText("Sections"));
    await waitFor(() => {
      expect(screen.getByTestId("catalog-depth").textContent).toBe("5 levels");
    });
    expect(sidebar.getByText("a/b/c/d/e")).toBeTruthy();
  });

  // The depth is stated in the singular at one, the way every other figure
  // the sidebar carries is.
  it("states a catalog depth of one in the singular", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": { body: catalog },
      "/v1/catalog": { body: { ids: ["eng/deploy"] } },
      "/v1/layers": { body: { layers: [adminLayer()] } },
    });
    render(<App />);
    await waitFor(() => {
      expect(screen.getByTestId("catalog-depth").textContent).toBe("1 level");
    });
  });

  // A read that did not answer leaves the marker off. A figure standing
  // beside a tree no response described states a hierarchy the registry did
  // not report.
  it("states no catalog depth where the catalog read failed", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": { status: 500, body: { code: "INTERNAL" } },
      "/v1/catalog": { body: { ids: ["a/b/c/deep"] } },
      "/v1/layers": { body: { layers: [adminLayer()] } },
    });
    render(<App />);
    expect(await screen.findByTestId("catalog-failed")).toBeTruthy();
    expect(screen.queryByTestId("catalog-depth")).toBeNull();
  });

  // The tree is the shell's statement of where the reader is in the §4.2
  // hierarchy, so the ancestry of the domain on screen is resolved down to it
  // and that domain is marked. A reader who arrived by a link or a breadcrumb
  // would otherwise face a row of collapsed roots with nothing indicating the
  // page's position.
  it("expands the tree to the domain on screen and marks it as the current page", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      // The eager read stops at platform/ci, so the level holding the current
      // domain is one the tree has to read for itself.
      // The current domain carries a level of its own, so its node keeps the
      // toggle the assertion below counts. The read is two levels deep, so it
      // carries that level with it. A domain the read reports empty is a
      // leaf, and a leaf drops its toggle.
      "/v1/load_domain?path=platform%2Fci&depth=2": {
        body: {
          path: "platform/ci",
          subdomains: [
            {
              path: "platform/ci/lint",
              name: "lint",
              subdomains: [{ path: "platform/ci/lint/rules", name: "rules" }],
            },
          ],
          notable: [],
        },
      },
      "/v1/load_domain": { body: catalog },
      "/v1/search_artifacts": { body: { total_matched: 0 } },
      "/v1/layers": { body: { layers: [] } },
    });
    goTo(domainHref("platform/ci/lint"));
    render(<App />);
    // The whole sidebar is the query root, because the resolved ancestry
    // renders a nested level of the tree under the top one.
    const tree = within(await screen.findByLabelText("Sections"));
    const current = await tree.findByRole("link", { name: "lint" });
    expect(current.getAttribute("aria-current")).toBe("page");
    expect(current.closest(".catalog-row")?.className).toContain(
      "catalog-row-current",
    );
    // The ancestry and the domain itself opened, and no ancestor claims to
    // be the page.
    expect(tree.getAllByRole("button", { expanded: true }).length).toBe(3);
    expect(
      tree.getByRole("link", { name: "platform" }).getAttribute("aria-current"),
    ).toBeNull();
    expect(
      tree.getByRole("link", { name: "ci" }).getAttribute("aria-current"),
    ).toBeNull();
  });

  // The panel and the sidebar tree read the same level of the §4.2 hierarchy
  // on a domain route: the panel reads the domain it renders, and the tree
  // reads that node's level because the eager read stopped above it. The two
  // are the same request, and the page issues it once.
  it("reads each domain level once on a domain route", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain?path=platform%2Fci&depth=2": {
        body: {
          path: "platform/ci",
          subdomains: [{ path: "platform/ci/lint", name: "lint" }],
          notable: [],
        },
      },
      "/v1/load_domain?path=platform%2Fci%2Flint&depth=2": {
        body: {
          path: "platform/ci/lint",
          subdomains: [{ path: "platform/ci/lint/rules", name: "rules" }],
          notable: [],
        },
      },
      "/v1/load_domain": { body: catalog },
      "/v1/catalog": { body: { ids: [] } },
      "/v1/search_artifacts": { body: { total_matched: 0 } },
      "/v1/layers": { body: { layers: [] } },
    });
    goTo(domainHref("platform/ci/lint"));
    render(<App />);
    // Both readers have answered: the panel heads the domain, and the tree
    // renders the level under the node the route opened.
    await screen.findByRole("heading", { name: "lint" });
    const tree = within(await screen.findByLabelText("Sections"));
    await tree.findByRole("link", { name: "rules" });
    const reads = requests
      .filter((r) => r.url.startsWith("/v1/load_domain"))
      .map((r) => r.url);
    expect(reads).toContain("/v1/load_domain?path=platform%2Fci%2Flint&depth=2");
    expect([...new Set(reads)]).toEqual(reads);
  });

  // A §4.5.5 sparse chain is collapsed into one tree entry, so the levels it
  // swallowed have no row of their own. A route onto one of them is marked on
  // the entry that swallowed it, because otherwise the sidebar states no
  // position at all while the chain's own endpoint marks correctly.
  it("marks the collapsed chain entry for a domain inside the chain", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain?path=legal%2Fcontracts%2Fnda%2Ftemplates&depth=2": {
        body: {
          path: "legal/contracts/nda/templates",
          subdomains: [
            { path: "legal/contracts/nda/templates/mutual", name: "mutual" },
          ],
          notable: [],
        },
      },
      "/v1/load_domain": {
        body: {
          path: "",
          subdomains: [
            { path: "eng", name: "eng" },
            {
              path: "legal/contracts/nda/templates/mutual",
              name: "mutual",
            },
          ],
          notable: [],
        },
      },
      "/v1/catalog": { body: { ids: [] } },
      "/v1/search_artifacts": { body: { total_matched: 0 } },
      "/v1/layers": { body: { layers: [] } },
    });
    goTo(domainHref("legal/contracts/nda/templates"));
    render(<App />);
    const tree = within(await screen.findByLabelText("Sections"));
    const chain = await tree.findByRole("link", {
      name: "legal/contracts/nda/templates/mutual",
    });
    expect(chain.closest(".catalog-row")?.className).toContain(
      "catalog-row-current",
    );
    // The entry links to the chain's endpoint rather than to the domain the
    // reader is on, so it states position rather than claiming to be the page.
    expect(chain.getAttribute("aria-current")).toBe("location");
    expect(
      tree.getByRole("link", { name: "eng" }).getAttribute("aria-current"),
    ).toBeNull();
  });

  // The sidebar's section rows are the shell's statement of which §13.10
  // surface the page is. An implementation that renders the three rows
  // identically on every route leaves the reader with nothing in the shell
  // naming where they are, and fails here.
  it("marks the section row of the surface the reader is on", async () => {
    const stubs = {
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": { body: catalog },
      "/v1/search_artifacts": { body: { total_matched: 0 } },
      "/v1/layers": { body: { layers: [] } },
    };
    const rows = (): Record<string, HTMLElement> => {
      const nav = within(screen.getByLabelText("Sections"));
      return {
        Browse: nav.getByRole("link", { name: "Browse" }),
        Search: nav.getByRole("link", { name: "Search" }),
        Layers: nav.getByRole("link", { name: "Layers" }),
      };
    };
    // Each route marks its own row and only its own. The assertion reads the
    // filled class as well as the marker, because the marker alone is
    // invisible and the fill is what the reader sees.
    const cases: { hash: string; landmark: string; current: string }[] = [
      { hash: domainHref(""), landmark: "Domain browser", current: "Browse" },
      { hash: searchHref(""), landmark: "Search", current: "Search" },
      { hash: "#/layers", landmark: "Layer panel", current: "Layers" },
    ];
    for (const { hash, landmark, current } of cases) {
      stubRegistry(stubs);
      goTo(hash);
      render(<App />);
      await screen.findByLabelText(landmark);
      for (const [name, row] of Object.entries(rows())) {
        expect([name, row.getAttribute("aria-current")]).toEqual([
          name,
          name === current ? "page" : null,
        ]);
        expect([name, row.className.includes("section-link-current")]).toEqual([
          name,
          name === current,
        ]);
      }
      cleanup();
    }
  });

  // An artifact is reached from the domain browser and lives inside the
  // hierarchy that section navigates. A shell that marks nothing on an
  // artifact route leaves the reader with a sidebar saying nothing about
  // where the open artifact sits, and fails here.
  it("marks the browse section and the artifact's own domain on an artifact route", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      // The domain holding the artifact sits at the eager read's edge, so the
      // tree reads its own level once the route opens it.
      "/v1/load_domain?path=platform%2Fci&depth=2": {
        body: {
          path: "platform/ci",
          subdomains: [{ path: "platform/ci/rules", name: "rules" }],
          notable: [],
        },
      },
      "/v1/load_domain": { body: catalog },
      "/v1/load_artifact": {
        body: {
          id: "platform/ci/lint",
          type: "skill",
          version: "1.0.0",
          content_hash: "sha256:abc",
          manifest_body: "# Lint\n",
          frontmatter: manifestDoc,
          layer: "platform",
        },
      },
      "/v1/dependents": { body: { edges: [] } },
      "/v1/layers": { body: { layers: [] } },
    });
    goTo(artifactHref("platform/ci/lint"));
    render(<App />);
    await screen.findByLabelText("Artifact viewer");
    const nav = within(screen.getByLabelText("Sections"));
    // Browse is filled, and it carries the containing marker rather than the
    // page marker, because the page is the artifact.
    const browse = nav.getByRole("link", { name: "Browse" });
    expect(browse.className).toContain("section-link-current");
    expect(browse.getAttribute("aria-current")).toBe("true");
    // The tree opened down to the domain holding the artifact and marked it.
    const domain = await nav.findByRole("link", { name: "ci" });
    expect(domain.closest(".catalog-row")?.className).toContain(
      "catalog-row-current",
    );
    expect(domain.getAttribute("aria-current")).toBe("location");
    expect(
      nav.getByRole("link", { name: "platform" }).getAttribute("aria-current"),
    ).toBeNull();
    // The ancestry down to that domain opened, so its own level is on screen
    // rather than folded behind a collapsed root.
    expect(nav.getAllByRole("button", { expanded: true }).length).toBe(2);
    expect(nav.getByRole("link", { name: "rules" })).toBeTruthy();
  });

  // An artifact registered at the registry root has no domain above it, so
  // the tree has no row to mark and the section row carries the position on
  // its own.
  it("marks the browse section alone for an artifact at the registry root", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": { body: catalog },
      "/v1/load_artifact": {
        body: {
          id: "review",
          type: "skill",
          version: "1.0.0",
          content_hash: "sha256:abc",
          manifest_body: "# Review\n",
          frontmatter: manifestDoc,
          layer: "platform",
        },
      },
      "/v1/dependents": { body: { edges: [] } },
      "/v1/layers": { body: { layers: [] } },
    });
    goTo(artifactHref("review"));
    render(<App />);
    await screen.findByLabelText("Artifact viewer");
    const nav = within(screen.getByLabelText("Sections"));
    expect(nav.getByRole("link", { name: "Browse" }).className).toContain(
      "section-link-current",
    );
    expect(
      nav.getByRole("link", { name: "platform" }).getAttribute("aria-current"),
    ).toBeNull();
    expect(document.querySelector(".catalog-row-current")).toBeNull();
  });

  // The wordmark is the mark the design pass fixed, drawn inline beside the
  // name, so it resolves from the bundle like the rest of the page.
  it("renders the wordmark as an inline mark beside the name", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": { body: emptyDomain },
      "/v1/search_artifacts": { body: { total_matched: 0 } },
      "/v1/layers": { body: { layers: [] } },
    });
    render(<App />);
    const wordmark = await screen.findByLabelText("Podium");
    expect(wordmark.querySelector("svg")).toBeTruthy();
    expect(wordmark.textContent).toBe("Podium");
  });

  // A level wider than the cap folds its remainder behind one row. Drawing
  // every child of a wide domain fills the sidebar with one level, pushes the
  // levels beside it and the pinned footer counts off the screen, and leaves
  // the reader scrolling the sidebar to reach what the shell states about the
  // catalog. The row states the count it holds back and opens it in place.
  it("folds a level wider than the cap behind a remainder row", async () => {
    const wide = Array.from({ length: 11 }, (_, i) => ({
      path: `sub${String(i)}`,
      name: `sub${String(i)}`,
      // The level is drawn from what the eager read carried, so a child that
      // came back with no level of its own keeps the case to one tree level.
      subdomains: [],
    }));
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        body: { path: "", subdomains: wide, notable: [] },
      },
      "/v1/search_artifacts": { body: { total_matched: 0 } },
      "/v1/layers": { body: { layers: [] } },
    });
    render(<App />);
    const tree = await screen.findByLabelText("Catalog");
    expect(within(tree).getByText("sub7")).toBeTruthy();
    expect(within(tree).queryByText("sub8")).toBeNull();
    const more = within(tree).getByRole("button", { name: "+ 3 more" });
    fireEvent.click(more);
    expect(within(tree).getByText("sub10")).toBeTruthy();
    expect(within(tree).queryByRole("button", { name: "+ 3 more" })).toBeNull();
  });

  // The remainder row is a disclosure the keyboard reaches, so expanding it
  // leaves focus on it. A row that unmounted on the click it handled would
  // drop that reader onto the document body with the whole shell to tab back
  // through, and the same row folding the level back is what keeps it
  // mounted.
  it("keeps focus on the remainder row across the expansion it performs", async () => {
    const wide = Array.from({ length: 11 }, (_, i) => ({
      path: `sub${String(i)}`,
      name: `sub${String(i)}`,
      subdomains: [],
    }));
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        body: { path: "", subdomains: wide, notable: [] },
      },
      "/v1/search_artifacts": { body: { total_matched: 0 } },
      "/v1/layers": { body: { layers: [] } },
    });
    render(<App />);
    const tree = await screen.findByLabelText("Catalog");
    const more = within(tree).getByRole("button", { name: "+ 3 more" });
    more.focus();
    fireEvent.click(more);
    expect(within(tree).getByText("sub10")).toBeTruthy();
    expect(document.activeElement).toBe(more);
    expect(more.getAttribute("aria-expanded")).toBe("true");
    // The row folds the level back, so the reader who opened it can close it
    // from where the keyboard already is.
    expect(within(tree).getByRole("button", { name: "\u2212 show fewer" })).toBe(
      more,
    );
    fireEvent.click(more);
    expect(within(tree).queryByText("sub10")).toBeNull();
    expect(document.activeElement).toBe(more);
    expect(more.getAttribute("aria-expanded")).toBe("false");
  });

  // The reader's own position is never one of the folded rows. A domain that
  // sits past the cap is what the page is showing, so the level is drawn
  // whole and the row marking the current domain is on screen.
  it("draws a folded level whole when the current domain sits past the cap", async () => {
    const wide = Array.from({ length: 11 }, (_, i) => ({
      path: `sub${String(i)}`,
      name: `sub${String(i)}`,
      // The level is drawn from what the eager read carried, so a child that
      // came back with no level of its own keeps the case to one tree level.
      subdomains: [],
    }));
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        body: { path: "", subdomains: wide, notable: [] },
      },
      "/v1/search_artifacts": { body: { total_matched: 0 } },
      "/v1/layers": { body: { layers: [] } },
    });
    goTo(domainHref("sub9"));
    render(<App />);
    const tree = await screen.findByLabelText("Catalog");
    expect(
      within(tree).getByRole("link", { name: "sub9", current: "page" }),
    ).toBeTruthy();
    expect(within(tree).queryByRole("button", { name: /more$/ })).toBeNull();
  });

  // Every toggle draws the same glyph, so the accessible name is the only
  // thing that separates one row's toggle from the next one's. A reader
  // moving through the tree by keyboard is owed the domain each toggle opens,
  // and the name states whether pressing it opens or closes that level.
  it("names each subtree toggle after the domain it expands", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        body: {
          path: "",
          subdomains: [
            {
              path: "finance",
              name: "finance",
              subdomains: [{ path: "finance/ap", name: "ap" }],
            },
            {
              path: "eng",
              name: "eng",
              subdomains: [{ path: "eng/deploy", name: "deploy" }],
            },
          ],
          notable: [],
        },
      },
      "/v1/search_artifacts": { body: { total_matched: 0 } },
      "/v1/layers": { body: { layers: [] } },
    });
    render(<App />);
    const tree = await screen.findByLabelText("Catalog");
    const finance = within(tree).getByRole("button", {
      name: "Expand finance",
    });
    expect(
      within(tree).getByRole("button", { name: "Expand eng" }),
    ).toBeTruthy();
    fireEvent.click(finance);
    expect(
      within(tree).getByRole("button", { name: "Collapse finance" }),
    ).toBeTruthy();
    // The name is the row's own label, so the level the toggle opened carries
    // named toggles of its own.
    expect(
      within(tree).getByRole("button", { name: "Expand ap" }),
    ).toBeTruthy();
  });

  // The tree and the subdomain cards are two disclosure affordances in one
  // view, so the tree's open and closed marker is the shell's stroked chevron
  // rather than a typed triangle, and the row turns it through aria-expanded.
  it("marks an open and a closed subtree with the shell's chevron", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        body: {
          path: "",
          subdomains: [
            {
              path: "finance",
              name: "finance",
              subdomains: [{ path: "finance/ap", name: "ap" }],
            },
          ],
          notable: [],
        },
      },
      "/v1/search_artifacts": { body: { total_matched: 0 } },
      "/v1/layers": { body: { layers: [] } },
    });
    render(<App />);
    const tree = await screen.findByLabelText("Catalog");
    const closed = within(tree).getByRole("button", { name: "Expand finance" });
    expect(closed.querySelector("svg.chevron")).not.toBeNull();
    expect(closed.textContent).toBe("");
    fireEvent.click(closed);
    const open = within(tree).getByRole("button", {
      name: "Collapse finance",
    });
    expect(open.querySelector("svg.chevron")).not.toBeNull();
    expect(open.textContent).toBe("");
  });

  // A §4.5.5 sparse chain arrives collapsed into one entry whose path holds
  // every segment it crossed and whose name holds only the last one. The row
  // states the stretch of path it navigates across, because a row drawn from
  // the name puts support/escalations on screen as escalations under the root
  // and states a position in the hierarchy that domain does not hold.
  it("names a folded subdomain by the path it navigates to", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        body: {
          path: "",
          subdomains: [
            {
              path: "support/escalations",
              name: "escalations",
              subdomains: [
                { path: "support/escalations/paging", name: "paging" },
              ],
            },
          ],
          notable: [],
        },
      },
      "/v1/search_artifacts": { body: { total_matched: 0 } },
      "/v1/layers": { body: { layers: [] } },
    });
    render(<App />);
    const tree = await screen.findByLabelText("Catalog");
    const folded = within(tree).getByRole("link", {
      name: "support/escalations",
    });
    expect(folded.getAttribute("href")).toBe(domainHref("support/escalations"));
    expect(within(tree).queryByText("escalations")).toBeNull();
    // A row under the folded one carries its own segment alone, because the
    // label is relative to the parent the row hangs under.
    fireEvent.click(
      within(tree).getAllByRole("button", { expanded: false })[0],
    );
    expect(within(tree).getByRole("link", { name: "paging" })).toBeTruthy();
  });

  // A node whose level came back empty keeps the control the reader pressed,
  // marked unavailable, and renames itself to the outcome. The reader who
  // pressed the toggle is left standing on it, and a row that dropped the
  // button instead would take the reader's focus with it.
  it("keeps the toggle in place and names the outcome when a level comes back empty", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        body: {
          path: "",
          subdomains: [
            {
              path: "finance",
              name: "finance",
              subdomains: [{ path: "finance/ap", name: "ap" }],
            },
          ],
          notable: [],
        },
      },
      // The expanded node is at the eager read's edge, so it reads its own
      // level and the registry reports nothing under it.
      "/v1/load_domain?path=finance%2Fap&depth=2": {
        body: { path: "finance/ap", subdomains: [], notable: [] },
      },
      "/v1/search_artifacts": { body: { total_matched: 0 } },
      "/v1/layers": { body: { layers: [] } },
    });
    render(<App />);
    const tree = await screen.findByLabelText("Catalog");
    fireEvent.click(within(tree).getByRole("button", { name: "Expand finance" }));
    const toggle = within(tree).getByRole("button", { name: "Expand ap" });
    toggle.focus();
    fireEvent.click(toggle);
    // The control stays in the row once the level resolves to nothing, and it
    // is the same element, so the focus the reader put on it survives.
    await waitFor(() => {
      expect(
        within(tree).getByRole("button", { name: "ap has no subdomains" }),
      ).toBeTruthy();
    });
    expect(document.activeElement).toBe(toggle);
    expect(toggle.getAttribute("aria-disabled")).toBe("true");
    expect(toggle.getAttribute("aria-expanded")).toBeNull();
    // Keeping the reader's focus is all the control is still there for, so it
    // leaves the tab order and the next reader tabs past the row.
    expect(toggle.tabIndex).toBe(-1);
    // The outcome reaches the accessibility tree through the toggle's name
    // rather than being drawn beside it. The row's right-aligned slot is the
    // design's "restricted" slot and is too narrow for the sentence, which
    // clipped to a fragment beside any name longer than a few characters.
    //
    // The row publishes no live region. A per-row description is static text,
    // so a role="status" span here would re-announce the same sentence on
    // every re-render the tree takes for a layer write, a reingest, or a
    // catalog refresh, competing with the announcements the surfaces publish.
    expect(within(tree).queryByTestId("empty-domain")).toBeNull();
    expect(
      tree.querySelectorAll("[role='status'], [aria-live]"),
    ).toHaveLength(0);
    expect(within(tree).queryByText("no subdomains")).toBeNull();
    expect(within(tree).queryByText("ap has no subdomains")).toBeNull();
    expect(
      tree.querySelectorAll(".catalog-row > .catalog-marker"),
    ).toHaveLength(0);
    expect(within(tree).getByRole("link", { name: "ap" })).toBeTruthy();
    expect(tree.querySelectorAll("p")).toHaveLength(0);
  });

  // Spec: §13.10 — the sidebar stands beside every surface, so a live region
  // it publishes outlives the surface the reader moved on to. An emptied row
  // held its sentence for the rest of the session and read it out again on
  // each of the tree's re-renders, next to the announcement the surface the
  // reader is on publishes about its own results.
  it("leaves the emptied row out of the live regions the surfaces publish", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        body: {
          path: "",
          subdomains: [
            {
              path: "finance",
              name: "finance",
              subdomains: [{ path: "finance/ap", name: "ap" }],
            },
          ],
          notable: [],
        },
      },
      "/v1/load_domain?path=finance%2Fap&depth=2": {
        body: { path: "finance/ap", subdomains: [], notable: [] },
      },
      "/v1/search_artifacts": { body: { total_matched: 0 } },
      "/v1/layers": { body: { layers: [] } },
    });
    render(<App />);
    const tree = await screen.findByLabelText("Catalog");
    fireEvent.click(within(tree).getByRole("button", { name: "Expand finance" }));
    fireEvent.click(within(tree).getByRole("button", { name: "Expand ap" }));
    await waitFor(() => {
      expect(
        within(tree).getByRole("button", { name: "ap has no subdomains" }),
      ).toBeTruthy();
    });
    goTo("#/search/");
    const region = await screen.findByTestId("search-announcement");
    await waitFor(() => {
      expect(region.textContent).toBe("The catalog holds no artifacts.");
    });
    // The surface's own announcement is the only thing the reader hears.
    expect([
      ...document.querySelectorAll("[role='status'], [aria-live]"),
    ]).toEqual([region]);
  });

  // A route onto a leaf domain opens its node and resolves the level to
  // nothing with no press behind it, so nobody's focus is standing on the
  // control. It must not cost the reader a tab stop on the way down the tree.
  it("keeps a level the route emptied out of the tab order", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        body: {
          path: "",
          subdomains: [
            {
              path: "finance",
              name: "finance",
              subdomains: [{ path: "finance/ap", name: "ap" }],
            },
          ],
          notable: [],
        },
      },
      "/v1/load_domain?path=finance%2Fap&depth=2": {
        body: { path: "finance/ap", subdomains: [], notable: [] },
      },
      "/v1/search_artifacts": { body: { total_matched: 0 } },
      "/v1/layers": { body: { layers: [] } },
    });
    goTo("#/domain/finance/ap");
    render(<App />);
    // The whole sidebar is the query root, because the resolved ancestry
    // renders a nested level of the tree under the top one.
    const tree = within(await screen.findByLabelText("Sections"));
    const toggle = await tree.findByRole("button", {
      name: "ap has no subdomains",
    });
    expect(toggle.tabIndex).toBe(-1);
    expect(document.activeElement).not.toBe(toggle);
  });

  // A level the eager read already reported empty never carried a toggle, so
  // the row draws the blank marker and no control. The reader had nothing to
  // press there and is owed no sentence about it.
  it("draws no toggle for a node the eager read reported empty", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        body: {
          path: "",
          subdomains: [{ path: "finance", name: "finance", subdomains: [] }],
          notable: [],
        },
      },
      "/v1/search_artifacts": { body: { total_matched: 0 } },
      "/v1/layers": { body: { layers: [] } },
    });
    render(<App />);
    const tree = await screen.findByLabelText("Catalog");
    expect(within(tree).getByRole("link", { name: "finance" })).toBeTruthy();
    expect(within(tree).queryAllByRole("button")).toHaveLength(0);
    expect(within(tree).queryByTestId("empty-domain")).toBeNull();
  });

  // The registry omits `subdomains` on a descriptor that has none rather than
  // returning it empty, so the eager read reports a leaf by leaving the field
  // out. The read runs two levels deep and answers for the level under each
  // top-level domain, so that omission is an answer and the row is drawn as a
  // leaf on the first paint. Drawing a disclosure there instead gives every
  // leaf in the catalog a control that reveals nothing when it is pressed,
  // and leaves the tree drawn differently depending on which rows the reader
  // has already pressed.
  it("draws a leaf for a top-level domain the eager read left without subdomains", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        body: {
          path: "",
          subdomains: [
            { path: "eng", name: "eng" },
            { path: "finance", name: "finance" },
            // The level under this one came with the read, so this row is the
            // one node in the level that opens.
            {
              path: "sales",
              name: "sales",
              subdomains: [{ path: "sales/emea", name: "emea" }],
            },
          ],
          notable: [],
        },
      },
      "/v1/search_artifacts": { body: { total_matched: 0 } },
      "/v1/layers": { body: { layers: [] } },
    });
    render(<App />);
    const tree = await screen.findByLabelText("Catalog");
    expect(within(tree).getByRole("link", { name: "eng" })).toBeTruthy();
    expect(within(tree).getByRole("link", { name: "finance" })).toBeTruthy();
    expect(tree.querySelectorAll(".tree-leaf")).toHaveLength(2);
    // The domain that holds a level keeps its disclosure, and it is the only
    // control in the tree.
    const toggles = within(tree).getAllByRole("button");
    expect(toggles).toHaveLength(1);
    expect(toggles[0].getAttribute("aria-label")).toBe("Expand sales");
    // A node at the read's edge is a different case: nothing is known about
    // its level, so it keeps a disclosure that reads it.
    fireEvent.click(toggles[0]);
    expect(
      within(tree).getByRole("button", { name: "Expand emea" }),
    ).toBeTruthy();
  });

  // A domain the registry refuses to open stays in the hierarchy and is not
  // enterable, which is what the reader is owed: the tree lists it and the
  // link is gone.
  it("lists a domain whose level the registry refuses and makes it unenterable", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": { body: catalog },
      "/v1/search_artifacts": { body: { total_matched: 0 } },
      "/v1/layers": { body: { layers: [] } },
    });
    render(<App />);
    const tree = await screen.findByLabelText("Catalog");
    fireEvent.click(
      within(tree).getAllByRole("button", { expanded: false })[0],
    );
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        status: 403,
        body: { code: "auth.forbidden", message: "not permitted" },
      },
      "/v1/search_artifacts": { body: { total_matched: 0 } },
      "/v1/layers": { body: { layers: [] } },
    });
    fireEvent.click(
      within(tree).getAllByRole("button", { expanded: false })[0],
    );
    expect(await screen.findByTestId("restricted-domain")).toBeTruthy();
    expect(within(tree).queryByRole("link", { name: "ci" })).toBeNull();
  });

  // The deeper read is a catalog read, so a refusal for an unverifiable
  // identity is the expiry signal rather than a permission property of the
  // domain. A caller whose session ends while the page is open is owed the
  // expiry transition, which is what the shell renders from the outcome the
  // node hands it.
  it("renders the expiry transition where a deeper level is refused for an unverifiable identity", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/load_domain": { body: catalog },
      "/v1/search_artifacts": { body: { total_matched: 0 } },
      "/v1/layers": { body: { layers: [] } },
    });
    render(<App />);
    const tree = await screen.findByLabelText("Catalog");
    fireEvent.click(
      within(tree).getAllByRole("button", { expanded: false })[0],
    );
    stubRegistry({
      "/v1/load_domain": {
        status: 401,
        body: { code: "auth.token_expired", message: "expired" },
      },
    });
    fireEvent.click(
      within(tree).getAllByRole("button", { expanded: false })[0],
    );
    expect(await screen.findByTestId("session-ended")).toBeTruthy();
    expect(screen.queryByTestId("restricted-domain")).toBeNull();
  });

  // A level that did not load for any other reason states that and nothing
  // more. The domain stays enterable, because no response reported that this
  // caller may not open it, and expanding the node again re-issues the read
  // rather than latching the failure for the life of the page.
  it("keeps a domain enterable where its deeper read failed and re-reads it on the next expansion", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": { body: catalog },
      "/v1/search_artifacts": { body: { total_matched: 0 } },
      "/v1/layers": { body: { layers: [] } },
    });
    render(<App />);
    const tree = await screen.findByLabelText("Catalog");
    fireEvent.click(
      within(tree).getAllByRole("button", { expanded: false })[0],
    );
    stubRegistry({
      "/v1/load_domain": {
        status: 503,
        body: { code: "registry.unavailable", message: "down" },
      },
    });
    const toggle = within(tree).getAllByRole("button", { expanded: false })[0];
    fireEvent.click(toggle);
    expect(await screen.findByTestId("unavailable-domain")).toBeTruthy();
    expect(within(tree).getByRole("link", { name: "ci" })).toBeTruthy();
    expect(screen.queryByTestId("restricted-domain")).toBeNull();
    // The failure cleared, and the next expansion is what re-issues the read.
    stubRegistry({
      "/v1/load_domain": {
        body: {
          path: "platform/ci",
          subdomains: [{ path: "platform/ci/lint", name: "lint" }],
        },
      },
    });
    fireEvent.click(toggle);
    fireEvent.click(toggle);
    expect(await within(tree).findByText("lint")).toBeTruthy();
    expect(screen.queryByTestId("unavailable-domain")).toBeNull();
  });

  // The node's failed read is the shell's own, and the reader has one outage
  // in front of them rather than two. A surface retry that reaches the
  // registry re-issues the node level as well, so the row stops saying the
  // level did not load without the reader collapsing it and expanding it
  // again.
  it("clears a node's failed level when a surface retry reaches the registry", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": { body: catalog },
      "/v1/catalog": { body: { ids: [] } },
      "/v1/layers": { body: { layers: [] } },
    });
    render(<App />);
    const tree = await screen.findByLabelText("Catalog");
    fireEvent.click(
      within(tree).getAllByRole("button", { expanded: false })[0],
    );
    // The registry stops answering, so the node's own level does not load and
    // the domain the reader then opens does not either.
    stubRegistry({
      "/v1/load_domain": {
        status: 503,
        body: {
          code: "registry.unavailable",
          message: "down",
          retryable: true,
        },
      },
      "/v1/catalog": { body: { ids: [] } },
    });
    fireEvent.click(
      within(tree).getAllByRole("button", { expanded: false })[0],
    );
    expect(await screen.findByTestId("unavailable-domain")).toBeTruthy();
    goTo(domainHref("platform/ci"));
    const failed = await screen.findByTestId("domain-failed");
    // The registry answers again and the surface's own retry is pressed.
    stubRegistry({
      "/v1/load_domain": {
        body: {
          path: "platform/ci",
          subdomains: [{ path: "platform/ci/lint", name: "lint" }],
          notable: [],
        },
      },
      "/v1/catalog": { body: { ids: [] } },
    });
    fireEvent.click(within(failed).getByRole("button", { name: "Retry" }));
    expect(await within(tree).findByText("lint")).toBeTruthy();
    expect(screen.queryByTestId("unavailable-domain")).toBeNull();
  });

  // The refused arm has no catalog to navigate. The tree and the counts are
  // emptied rather than left standing with what an earlier read returned.
  it("empties the tree and the counts where the catalog read is refused", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture() },
      "/v1/load_domain": {
        status: 401,
        body: { code: "auth.untrusted_token", message: "not verified" },
      },
      "/v1/catalog": { body: catalogOf(312) },
      "/v1/layers": { body: { layers: [adminLayer()] } },
    });
    render(<App />);
    await screen.findByLabelText("Catalog refused");
    expect(
      within(screen.getByLabelText("Catalog")).queryAllByRole("listitem"),
    ).toEqual([]);
    expect(screen.getByTestId("catalog-counts").textContent).toBe("");
    expect(screen.queryByTestId("catalog-ingest")).toBeNull();
  });

  // A catalog read that failed for a reason other than identity leaves the
  // sidebar with no tree. The tree region says the read failed rather than
  // rendering blank, and the footer figures are withdrawn, because the
  // counts are read once for the page and a figure left standing over a
  // registry that stopped answering states it as current.
  it("says the catalog read failed and withdraws the counts", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/load_domain": {
        status: 503,
        body: { code: "registry.unavailable", message: "down" },
      },
      "/v1/catalog": { body: catalogOf(312) },
      "/v1/layers": {
        body: {
          layers: [
            { ...adminLayer(), last_ingested_at: new Date().toISOString() },
          ],
        },
      },
    });
    render(<App />);
    await waitFor(() => {
      expect(screen.getByTestId("catalog-failed").textContent).toContain(
        "The catalog could not be read.",
      );
    });
    expect(
      within(screen.getByLabelText("Catalog")).queryAllByRole("listitem"),
    ).toEqual([]);
    expect(screen.queryByTestId("catalog-empty")).toBeNull();
    expect(screen.getByTestId("catalog-counts").textContent).toBe(
      "Counts unavailable",
    );
    expect(screen.queryByTestId("catalog-ingest")).toBeNull();
  });

  // The failed read is the shell's own. The surface beside it retries only
  // the read the surface owns, so the sidebar carries the retry for the tree
  // and the counts, and running it clears the state it is stated under.
  it("recovers the tree and the counts from the sidebar's own retry", async () => {
    const stubs: Record<string, Stub> = {
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/load_domain": {
        status: 503,
        body: { code: "registry.unavailable", message: "down" },
      },
      "/v1/catalog": { body: catalogOf(312) },
      "/v1/layers": { body: { layers: [adminLayer()] } },
    };
    stubRegistry(stubs);
    render(<App />);
    await screen.findByTestId("catalog-failed");
    expect(screen.getByTestId("catalog-counts").textContent).toBe(
      "Counts unavailable",
    );

    stubs["/v1/load_domain"] = { body: rootDomains };
    // A surface can carry a retry at the same moment, so this one is named
    // for the read it re-issues rather than sharing the bare label.
    fireEvent.click(
      screen.getByRole("button", { name: "Try again reading the catalog" }),
    );

    await waitFor(() => {
      expect(screen.queryByTestId("catalog-failed")).toBeNull();
    });
    expect(
      within(screen.getByLabelText("Catalog")).queryAllByRole("listitem"),
    ).toHaveLength(2);
    expect(screen.getByTestId("catalog-counts").textContent).toBe(
      "1 layer · 312 artifacts",
    );
  });

  // The surface beside the sidebar owns its own read, and that read answering
  // after one that did not is the reader's retry reaching the registry. That
  // is the condition the sidebar reported, so the shell re-issues its own
  // read on it and one retry recovers the whole page.
  it("recovers the tree and the counts from the surface's own retry", async () => {
    const stubs: Record<string, Stub> = {
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      // The registry is not answering at all, which is how it fails when it
      // has gone away: the request never reaches it and there is no envelope.
      "/v1/load_domain": { rejects: true },
      "/v1/catalog": { body: catalogOf(312) },
      "/v1/layers": { body: { layers: [adminLayer()] } },
    };
    stubRegistry(stubs);
    // A domain the recovered catalog does not carry, so no node in the tree
    // is the current one and none of them expands under it.
    goTo(domainHref("eng"));
    render(<App />);
    await screen.findByTestId("domain-failed");
    await screen.findByTestId("catalog-failed");
    expect(screen.getByTestId("catalog-counts").textContent).toBe(
      "Counts unavailable",
    );

    stubs["/v1/load_domain"] = { body: rootDomains };
    // The surface's retry, and not the sidebar's. The sidebar's own control
    // is gone once the state it is stated under clears, so pressing it after
    // this would be pressing a control the reader no longer has.
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));

    await waitFor(() => {
      expect(screen.queryByTestId("catalog-failed")).toBeNull();
    });
    expect(
      within(screen.getByLabelText("Catalog")).queryAllByRole("listitem"),
    ).toHaveLength(2);
    await waitFor(() => {
      expect(screen.getByTestId("catalog-counts").textContent).toBe(
        "1 layer · 312 artifacts",
      );
    });
  });

  // The layers route's surfaces report no catalog outcome, because a layer
  // endpoint answers an unverifiable session anonymously and says nothing
  // about it. A read of theirs that answered does say the registry is
  // reachable, which is the condition the sidebar reported, so the shell
  // re-issues its own read on it. Without that the sidebar states an outage
  // for the rest of the session while the panel beside it lists the layers.
  it("recovers the tree and the counts from the layer panel's retry", async () => {
    const stubs: Record<string, Stub> = {
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/load_domain": { rejects: true },
      "/v1/catalog": { body: catalogOf(312) },
      "/v1/layers": { rejects: true },
    };
    stubRegistry(stubs);
    goTo(layersHref);
    render(<App />);
    await screen.findByTestId("catalog-failed");
    expect(screen.getByTestId("catalog-counts").textContent).toBe(
      "Counts unavailable",
    );

    stubs["/v1/load_domain"] = { body: rootDomains };
    stubs["/v1/layers"] = { body: { layers: [adminLayer()] } };
    // The panel's own retry, whose name is the bare label. The sidebar's
    // control names the read it re-issues, so the two do not collide.
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));

    await waitFor(() => {
      expect(screen.queryByTestId("catalog-failed")).toBeNull();
    });
    expect(
      within(screen.getByLabelText("Catalog")).queryAllByRole("listitem"),
    ).toHaveLength(2);
    await waitFor(() => {
      expect(screen.getByTestId("catalog-counts").textContent).toBe(
        "1 layer · 312 artifacts",
      );
    });
  });

  // The recovery table is the other surface the layers route carries, and it
  // is reached from the panel while the sidebar is still stating the outage.
  // Its read answering recovers the shell the same way.
  it("recovers the tree and the counts from the recovery table's read", async () => {
    const stubs: Record<string, Stub> = {
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/load_domain": { rejects: true },
      "/v1/catalog": { body: catalogOf(312) },
      "/v1/layers": { rejects: true },
    };
    stubRegistry(stubs);
    goTo(layersHref);
    render(<App />);
    await screen.findByTestId("catalog-failed");

    stubs["/v1/load_domain"] = { body: rootDomains };
    stubs["/v1/layers"] = { body: { layers: [] } };
    // The route name does not change between the panel and the recovery
    // table, so the shell's read is not re-issued by the navigation itself.
    goTo(deletedLayersHref);

    await waitFor(() => {
      expect(screen.queryByTestId("catalog-failed")).toBeNull();
    });
    await waitFor(() => {
      expect(screen.getByTestId("catalog-counts").textContent).toBe(
        "0 layers · 312 artifacts",
      );
    });
  });

  // A read that returned a catalog holding no domain gets a line saying so.
  it("states that the catalog holds no domains", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/load_domain": { body: emptyDomain },
      "/v1/search_artifacts": { body: { total_matched: 0 } },
      "/v1/layers": { body: { layers: [] } },
    });
    render(<App />);
    await screen.findByLabelText("Catalog");
    await waitFor(() => {
      expect(screen.getByTestId("catalog-empty").textContent).toContain(
        "The catalog holds no domains.",
      );
    });
    expect(screen.getByTestId("catalog-empty").textContent).toContain(
      "Register a layer to fill it.",
    );
  });

  // A layer whose artifacts sit at its root contributes no domain, so the
  // tree is empty while the registry is registered, ingested, and serving.
  // The advisory names a remedy the reader has already applied there.
  // Spec: §13.10
  it("does not tell a reader to register a layer where the empty tree holds artifacts", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/load_domain": {
        body: {
          path: "",
          subdomains: [],
          notable: [
            { id: "solo", type: "skill", description: "Pay an invoice" },
          ],
        },
      },
      "/v1/search_artifacts": { body: { total_matched: 0 } },
      "/v1/layers": { body: { layers: [] } },
    });
    render(<App />);
    await screen.findByLabelText("Catalog");
    await waitFor(() => {
      expect(screen.getByTestId("catalog-empty").textContent).toContain(
        "The catalog holds no domains.",
      );
    });
    expect(screen.getByTestId("catalog-empty").textContent).not.toContain(
      "Register a layer to fill it.",
    );
    expect(screen.getByTestId("catalog-empty").textContent).toContain(
      "Its artifacts sit at the top of the hierarchy.",
    );
  });
});

describe("the sign-in control", () => {
  // Row one of the sign-in control table: the flow enabled with no subject
  // renders a sign-in navigation to the path the read reports.
  it("navigates to the read’s sign_in_path where the flow is enabled and no subject resolves", async () => {
    stubRegistry({
      "/v1/ui/session": {
        body: posture({
          browser_auth: {
            enabled: true,
            sign_in_path: "/v1/ui/auth/sign-in",
            sign_out_path: "/v1/ui/auth/sign-out",
          },
        }),
      },
      "/v1/load_domain": { body: emptyDomain },
    });
    render(<App />);
    const control = await screen.findByTestId("sign-in");
    expect(control.getAttribute("href")).toBe("/v1/ui/auth/sign-in");
    expect(screen.queryByTestId("sign-out")).toBeNull();
  });

  // Row two: the flow enabled with a subject renders sign-out, issued as a
  // POST to the path the read reports.
  it("issues sign-out as a POST to the read’s sign_out_path where a subject resolves", async () => {
    stubRegistry({
      "/v1/ui/session": {
        body: posture({
          subject: "alice@acme.com",
          browser_auth: {
            enabled: true,
            sign_in_path: "/v1/ui/auth/sign-in",
            sign_out_path: "/v1/ui/auth/sign-out",
          },
        }),
      },
      "/v1/load_domain": { body: emptyDomain },
      "/v1/ui/auth/sign-out": { body: {} },
    });
    render(<App />);
    // The sign-out entry point is the one the account menu carries, so the
    // cluster is opened first.
    fireEvent.click(await screen.findByTestId("account-trigger"));
    const control = await screen.findByTestId("sign-out");
    expect(screen.queryByTestId("sign-in")).toBeNull();
    fireEvent.click(control);
    await waitFor(() => {
      expect(requests).toContainEqual({
        url: "/v1/ui/auth/sign-out",
        method: "POST",
      });
    });
  });

  // Row three, driven with a subject present, which is the gateway-fronted
  // arrangement: a deployment running no browser flow renders neither
  // control on any value of subject. Clearing a Podium cookie would not end
  // the gateway's own session there.
  it("renders neither control where the flow is disabled and a subject resolves", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/load_domain": { body: emptyDomain },
    });
    render(<App />);
    await screen.findByLabelText("Domain browser");
    expect(screen.queryByTestId("sign-in")).toBeNull();
    expect(screen.queryByTestId("sign-out")).toBeNull();
  });

  // A read that does not answer leaves the page holding no value for either
  // key, so it renders the anonymous presentation: neither control, and the
  // layer panel with its write operations.
  it("renders the anonymous presentation where the posture read fails", async () => {
    stubRegistry({
      "/v1/ui/session": {
        status: 503,
        body: { code: "registry.unavailable", message: "down" },
      },
      "/v1/layers": { body: { layers: [userLayer()] } },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    expect(screen.queryByTestId("sign-in")).toBeNull();
    expect(screen.queryByTestId("sign-out")).toBeNull();
    expect(screen.getAllByRole("button", { name: "Reingest" }).length).toBe(1);
    expect(screen.queryByText("yours")).toBeNull();
  });
});

describe("the catalog-scope rule", () => {
  // The whole-catalog arm. A registry engaging public mode serves its whole
  // catalog to a caller who resolves no subject, so the page presents what
  // the read returned and carries no public-view framing. An implementation
  // that filtered the anonymous view to public artifacts, or framed it as
  // one, fails here.
  it("presents the whole catalog under public mode with no public-view framing", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        body: {
          path: "",
          subdomains: [],
          notable: [
            { id: "security/internal-review", type: "skill", version: "1.0.0" },
          ],
        },
      },
    });
    render(<App />);
    expect(await screen.findByText("security/internal-review")).toBeTruthy();
    expect(screen.queryByText("Not signed in")).toBeNull();
    expect(screen.queryByTestId("anonymous-banner")).toBeNull();
  });

  // The public-subset arm carries its framing and states nothing about what
  // was withheld.
  it("frames the anonymous view without asserting that anything was withheld", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture() },
      "/v1/load_domain": { body: emptyDomain },
    });
    render(<App />);
    expect(await screen.findByText("Not signed in")).toBeTruthy();
    expect(screen.queryByText(/hidden/i)).toBeNull();
    expect(screen.queryByText(/withheld/i)).toBeNull();
    // The arm carries two pieces: the sidebar footer note and the page
    // banner. The banner carries no control of its own, because the
    // authentication control belongs to the shell.
    const banner = screen.getByTestId("anonymous-banner");
    expect(banner.textContent).toContain("not signed in");
    expect(banner.querySelector("button")).toBeNull();
    expect(banner.querySelector("a")).toBeNull();
  });

  // The refused arm, ordered ahead of the other two. A registry whose
  // identity provider verifies a runtime-signed token refuses every catalog
  // call from a browser that holds none, and that caller has no anonymous
  // view of the catalog at all.
  it("renders the refused state rather than an empty or filtered catalog", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture() },
      "/v1/load_domain": {
        status: 401,
        body: { code: "auth.untrusted_token", message: "not verified" },
      },
    });
    render(<App />);
    await screen.findByLabelText("Catalog refused");
    expect(screen.queryByLabelText("Domain browser")).toBeNull();
    expect(screen.getByText("auth.untrusted_token")).toBeTruthy();
  });
});

describe("the domain browser", () => {
  it("renders the subdomains, the direct artifacts, and the lifted ones apart", async () => {
    stubRegistry({
      "/v1/ui/session": {
        body: posture({ public_mode: true, subject: "alice@acme.com" }),
      },
      "/v1/load_domain": {
        body: {
          path: "platform",
          description: "Platform engineering.",
          keywords: ["infra"],
          subdomains: [
            { path: "platform/ci", name: "ci", description: "Pipelines." },
          ],
          notable: [
            {
              id: "platform/deploy",
              type: "skill",
              version: "2.0.0",
              source: "featured",
            },
            { id: "platform/ci/lint", type: "skill", folded_from: "ci" },
          ],
          note: "The listing was trimmed to fit the response budget.",
        },
      },
    });
    goTo("#/domain/platform");
    render(<App />);
    const browser = await screen.findByLabelText("Domain browser");
    // The sidebar tree lists the same domain, so the browser's own listing is
    // read off the surface rather than off the page.
    expect(within(browser).getByText("ci")).toBeTruthy();
    expect(screen.getByText("platform/deploy")).toBeTruthy();
    expect(screen.getByText("\u2605 CURATED")).toBeTruthy();
    // The lifted entries sit in their own group, whose head names the group
    // and qualifies it on the same line, and whose container holds the rows.
    // A label with a sentence under it and the listing beside them leaves the
    // group looking like a second listing of the domain's own artifacts.
    const lifted = screen.getByText("Lifted from sparse subdomains");
    const head = lifted.parentElement;
    expect(head?.className).toContain("folded-head");
    expect(within(head as HTMLElement).getByText("Not direct children")).toBeTruthy();
    const group = head?.parentElement;
    expect(group?.className).toContain("folded");
    expect(
      within(group as HTMLElement).getByText("platform/ci/lint"),
    ).toBeTruthy();
    // The lifted row names the subdomain it was raised out of, and it names
    // the relation rather than printing the subpath alone. The marker is drawn
    // on the group's dashed edge so it does not read as one more tag pill.
    const marker = within(group as HTMLElement).getByText("\u2191 FROM ci");
    expect(marker.className).toContain("badge-folded");
    // The note reaches the reader at the returned edge rather than above the
    // description, beside the count and the control that continues past it.
    const continuation = await screen.findByTestId("listing-continuation");
    expect(continuation.textContent).toContain(
      "The listing was trimmed to fit the response budget.",
    );
  });

  // A §4.5.5 sparse chain reaches the grid folded into one card whose title is
  // the whole stretch of path it crosses. A slash carries no break opportunity
  // of its own, so a card too narrow for the title broke it inside a segment
  // and read as broken text. The title declares a break after each separator.
  it("breaks a folded subdomain card title at its path separators", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        body: {
          path: "finance",
          subdomains: [
            {
              path: "finance/accounting/ledgers/reconciliation/quarterly",
              name: "quarterly",
              description: "Quarterly.",
            },
          ],
          notable: [],
        },
      },
    });
    goTo("#/domain/finance");
    render(<App />);
    const browser = await screen.findByLabelText("Domain browser");
    const title = browser.querySelector(".subdomain-name > span");
    expect(title?.textContent).toBe(
      "accounting/ledgers/reconciliation/quarterly",
    );
    // One opportunity per separator, each of them after the slash, so a broken
    // line ends on the separator rather than inside the segment before it.
    const breaks = [...(title?.querySelectorAll("wbr") ?? [])];
    expect(breaks.length).toBe(3);
    for (const opportunity of breaks) {
      expect(opportunity.previousSibling?.textContent).toBe("/");
    }
  });

  // A domain keyword covers the whole subtree and an artifact tag labels one
  // row. Drawn with the same pill they read as one vocabulary, so the header
  // keyword and the listing tag carry different treatments.
  it("draws a domain keyword apart from an artifact tag", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        body: {
          path: "platform",
          keywords: ["infra"],
          subdomains: [],
          notable: [
            {
              id: "platform/deploy",
              type: "skill",
              version: "2.0.0",
              tags: ["tracing"],
            },
          ],
        },
      },
    });
    goTo("#/domain/platform");
    render(<App />);
    const browser = await screen.findByLabelText("Domain browser");
    const keyword = within(browser).getByText("infra");
    const tag = within(browser).getByText("tracing");
    expect(keyword.className).toBe("keyword");
    expect(tag.className).toBe("tag");
  });

  // A listing row is two columns. The reader scanning for a type or a
  // version reads one column at the row's right edge rather than reading
  // across the second line of every row.
  it("puts a listing row type and version in a right-hand column beside the named artifact", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        body: {
          path: "platform",
          subdomains: [],
          notable: [
            {
              id: "platform/deploy",
              type: "skill",
              version: "2.0.0",
              source: "featured",
            },
          ],
        },
      },
    });
    goTo("#/domain/platform");
    render(<App />);
    const browser = await screen.findByLabelText("Domain browser");
    const aside = within(browser).getByTestId("artifact-row-aside");
    expect(aside.textContent).toBe("SKILLv2.0.0");
    // The row names the artifact and states the full path beside it, and the
    // right-hand column holds neither.
    const link = within(browser).getByRole("link", { name: "deploy" });
    expect(link.getAttribute("href")).toBe("#/artifact/platform%2Fdeploy");
    const head = link.parentElement as HTMLElement;
    expect(within(head).getByText("platform/deploy")).toBeTruthy();
    expect(within(head).getByText("\u2605 CURATED")).toBeTruthy();
    expect(within(head).queryByText("v2.0.0")).toBeNull();
  });

  // Spec: §13.10 — the domain browser is the primary navigation, so a card
  // states how much stands behind it before the reader spends a click. The
  // artifact figure comes from the one catalog read the page already issues,
  // so a card for a domain holding artifacts and no subdomain still carries a
  // count line.
  it("states a subdomain card artifact count beside its subdomain count", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/catalog": {
        body: {
          ids: [
            "platform/ci/lint",
            "platform/ci/rules/naming",
            "platform/release/cut",
          ],
        },
      },
      "/v1/load_domain": {
        body: {
          path: "platform",
          subdomains: [
            {
              path: "platform/ci",
              name: "ci",
              description: "Pipelines.",
              subdomains: [{ path: "platform/ci/rules", name: "rules" }],
            },
            { path: "platform/release", name: "release" },
            { path: "platform/idle", name: "idle" },
          ],
          notable: [],
        },
      },
    });
    goTo("#/domain/platform");
    render(<App />);
    const browser = await screen.findByLabelText("Domain browser");
    const grid = within(browser).getByRole("list", { name: "Subdomains" });
    const cards = within(grid).getAllByRole("listitem");
    await waitFor(() => {
      expect(cards[0].textContent).toContain("2 artifacts");
    });
    expect(cards[0].textContent).toContain("1 subdomain");
    // A child holding one artifact and no subdomain carries a count line of
    // its own rather than nothing at all.
    expect(cards[1].textContent).toContain("1 artifact");
    expect(cards[1].textContent).not.toContain("subdomain");
    // A zero is what the catalog read returned under that child, so the card
    // states it.
    expect(cards[2].textContent).toContain("0 artifacts");
  });

  // Spec: §13.10 — the card is drawn as one target, so the description and
  // the count line are aimable as well as the name. The card carries the
  // overlay class the stylesheet stretches over the whole box
  // (`index.css`, `.stretched-link`), which `layout.test.ts` pins.
  it("makes the whole subdomain card follow its link", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/catalog": { body: { ids: ["platform/ci/lint"] } },
      "/v1/load_domain": {
        body: {
          path: "platform",
          subdomains: [
            { path: "platform/ci", name: "ci", description: "Pipelines." },
          ],
          notable: [],
        },
      },
    });
    goTo("#/domain/platform");
    render(<App />);
    const browser = await screen.findByLabelText("Domain browser");
    const grid = within(browser).getByRole("list", { name: "Subdomains" });
    const card = within(grid).getAllByRole("listitem")[0];
    const link = within(card).getByRole("link");
    expect(link.className).toContain("stretched-link");
    expect(link.getAttribute("href")).toBe("#/domain/platform%2Fci");
    // The overlay is measured against the card, so the card holds the link
    // and the description the overlay has to cover.
    expect(card.contains(link)).toBe(true);
    expect(card.textContent).toContain("Pipelines.");
  });

  // A catalog read that failed establishes no artifact count, so the card
  // states what the load_domain response reported below the child and claims
  // nothing the page did not read.
  it("falls back to the subdomain count alone when the catalog read is refused", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/catalog": {
        status: 403,
        body: { error: { code: "auth.untrusted_token", message: "no" } },
      },
      "/v1/load_domain": {
        body: {
          path: "platform",
          subdomains: [
            {
              path: "platform/ci",
              name: "ci",
              subdomains: [{ path: "platform/ci/rules", name: "rules" }],
            },
          ],
          notable: [],
        },
      },
    });
    goTo("#/domain/platform");
    render(<App />);
    const browser = await screen.findByLabelText("Domain browser");
    const grid = within(browser).getByRole("list", { name: "Subdomains" });
    const card = within(grid).getAllByRole("listitem")[0];
    expect(card.textContent).toContain("1 subdomain");
    expect(card.textContent).not.toContain("artifact");
  });

  // Spec: §13.10 — the domain browser marks each entry. The type is a fixed
  // vocabulary word and reads in caps, the version carries the v that names
  // what the number measures, and the curated marker leads with a star so a
  // reader scanning a listing finds the featured rows without reading the
  // label on each badge.
  it("marks a listing row with a capitalised type, a v-prefixed version, and a starred curated badge", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        body: {
          path: "platform",
          subdomains: [],
          notable: [
            {
              id: "platform/deploy",
              type: "context",
              version: "0.1.0",
              source: "featured",
            },
          ],
        },
      },
    });
    goTo("#/domain/platform");
    render(<App />);
    const browser = await screen.findByLabelText("Domain browser");
    const row = within(browser).getByRole("listitem");
    expect(within(row).getByText("CONTEXT")).toBeTruthy();
    expect(within(row).queryByText("context")).toBeNull();
    expect(within(row).getByText("v0.1.0")).toBeTruthy();
    expect(within(row).queryByText("0.1.0")).toBeNull();
    const curated = within(row).getByText("\u2605 CURATED");
    expect(curated.className).toContain("badge-accent");
    expect(within(row).queryByText("curated")).toBeNull();
  });

  // Spec: \u00a74.5.5 \u2014 the registry tags every notable entry the domain's
  // featured: list does not name as "signal", whether or not a usage signal
  // contributed to it. A marker naming usage on those rows lands on every row
  // of a registry that has served no traffic and states a reason the response
  // does not report, so the listing marks the featured rows alone.
  it("marks a non-featured listing row with no source claim at all", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        body: {
          path: "platform",
          subdomains: [],
          notable: [
            {
              id: "platform/deploy",
              type: "skill",
              version: "2.0.0",
              source: "signal",
            },
            {
              id: "platform/build",
              type: "skill",
              version: "1.0.0",
              source: "featured",
            },
          ],
        },
      },
    });
    goTo("#/domain/platform");
    render(<App />);
    const browser = await screen.findByLabelText("Domain browser");
    expect(within(browser).queryByText("surfaced by usage")).toBeNull();
    // The distinction the response does draw survives beside it.
    expect(within(browser).getAllByText("\u2605 CURATED")).toHaveLength(1);
  });

  // Spec: §13.10 — a manifest carries no required description, and the row's
  // right-hand column holds the row's height whether a description line is
  // drawn or not. A row that omits the line therefore reads as a description
  // that failed to render rather than as an artifact that carries none.
  it("states the absent description on a listing row rather than leaving the line blank", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        body: {
          path: "platform",
          subdomains: [],
          notable: [
            {
              id: "platform/deploy",
              type: "context",
              description: "Deploy runbook.",
            },
            { id: "platform/nodesc", type: "context" },
          ],
        },
      },
    });
    goTo("#/domain/platform");
    render(<App />);
    const browser = await screen.findByLabelText("Domain browser");
    const rows = within(browser).getAllByRole("listitem");
    expect(within(rows[0]).getByText("Deploy runbook.")).toBeTruthy();
    const absent = within(rows[1]).getByText("No description.");
    // The placeholder reads in the meta tone, so it does not carry the weight
    // of a description the artifact does not have.
    expect(absent.className).toContain("quiet");
  });

  // Colour alone does not separate the placeholder from a description. Set
  // upright at the size of the rows around it, "No description." reads as a
  // description whose text happens to say that. The placeholder is therefore
  // italic on every surface that draws one: the listing row, the subdomain
  // card, and the compact table.
  it("sets the absent-description placeholder in italic on every surface that draws one", async () => {
    const wide = Array.from({ length: 21 }, (_, i) => ({
      path: `platform/sub${String(i)}`,
      name: `sub${String(i)}`,
      subdomains: [],
    }));
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        body: {
          path: "platform",
          subdomains: [{ path: "platform/ci", name: "ci" }],
          notable: [{ id: "platform/nodesc", type: "context" }],
        },
      },
    });
    goTo("#/domain/platform");
    const comfortable = render(<App />);
    const browser = await screen.findByLabelText("Domain browser");
    // One placeholder stands on the subdomain card and one on the artifact
    // row, and both are asserted.
    const placeholders = within(browser).getAllByText("No description.");
    expect(placeholders.length).toBe(2);
    for (const placeholder of placeholders) {
      expect(window.getComputedStyle(placeholder).fontStyle).toBe("italic");
    }
    comfortable.unmount();

    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        body: {
          path: "platform",
          subdomains: wide,
          notable: [{ id: "platform/nodesc", type: "context" }],
        },
      },
    });
    goTo("#/domain/platform");
    render(<App />);
    const table = await screen.findByRole("table", { name: "Artifacts" });
    expect(
      window.getComputedStyle(within(table).getByText("No description."))
        .fontStyle,
    ).toBe("italic");
  });

  // The listings are divided by labels rather than by titles, so the page
  // carries one heading at title weight and the two dividers stay quiet.
  it("divides the listings with section labels rather than with page-title-weight headings", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        body: {
          path: "platform",
          subdomains: [{ path: "platform/ci", name: "ci" }],
          notable: [{ id: "platform/deploy", type: "skill" }],
        },
      },
    });
    goTo("#/domain/platform");
    render(<App />);
    const browser = await screen.findByLabelText("Domain browser");
    const labels = within(browser)
      .getAllByRole("heading", { level: 2 })
      .map((heading) => [heading.textContent, heading.className]);
    expect(labels).toEqual([
      ["Subdomains", "label"],
      ["Artifacts in this domain", "label"],
    ]);
  });

  // The subdomains are a card grid over the immediate children. Each card
  // states what the response reported below that child, and the grandchildren
  // the two-level read returned are counted rather than drawn, so the page
  // stays one level deep however deep the tree runs.
  it("lists the immediate subdomains as counted cards without nesting the level below", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        body: {
          path: "",
          subdomains: [
            {
              path: "platform",
              name: "platform",
              description: "Platform engineering.",
              subdomains: [
                { path: "platform/ci", name: "ci" },
                { path: "platform/deploy", name: "deploy" },
              ],
            },
            { path: "finance", name: "finance" },
          ],
          notable: [],
        },
      },
    });
    render(<App />);
    const browser = await screen.findByLabelText("Domain browser");
    const grid = within(browser).getByRole("list", { name: "Subdomains" });
    const cards = within(grid).getAllByRole("listitem");
    expect(
      cards.map((card) => within(card).getByRole("link").textContent),
    ).toEqual(["platform", "finance"]);
    // The grandchildren are the count on their parent's card and appear
    // nowhere on the page as cards of their own.
    expect(within(cards[0]).getByText("2 subdomains")).toBeTruthy();
    expect(within(grid).queryByText("ci")).toBeNull();
    expect(within(grid).queryByText("deploy")).toBeNull();
    // A child the response reported nothing under claims no count.
    expect(within(cards[1]).queryByText(/subdomains?$/)).toBeNull();
    expect(within(cards[1]).getByText("No description.")).toBeTruthy();
  });

  // The card reads name, then description, then the two figures. Set at the
  // body size in the quiet tone, the description is the largest and the palest
  // text in the card and reads as a second counts line, so it takes the
  // secondary ink the artifact row's description takes and a size under the
  // card title's, and only the counts stay quiet.
  it("sets the subdomain card description under the title size in the secondary ink", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        body: {
          path: "",
          subdomains: [
            {
              path: "platform",
              name: "platform",
              description: "Platform engineering.",
              subdomains: [{ path: "platform/ci", name: "ci" }],
            },
          ],
          notable: [],
        },
      },
    });
    render(<App />);
    const browser = await screen.findByLabelText("Domain browser");
    const grid = within(browser).getByRole("list", { name: "Subdomains" });
    const card = within(grid).getAllByRole("listitem")[0];
    const title = within(card).getByRole("link");
    const description = within(card).getByText("Platform engineering.");
    const counts = within(card).getByText("1 subdomain");
    expect(parseFloat(window.getComputedStyle(description).fontSize)).toBeLessThan(
      parseFloat(window.getComputedStyle(title).fontSize),
    );
    expect(window.getComputedStyle(description).color).not.toBe(
      window.getComputedStyle(counts.parentElement as HTMLElement).color,
    );
    // The placeholder that stands where a child carries no description keeps
    // the quiet tone, because it is not a description the author wrote.
    expect(description.classList.contains("quiet")).toBe(false);
  });

  // A §4.5.5 sparse chain arrives collapsed into one entry whose path holds
  // every segment it crossed and whose name holds only the last one. A card
  // drawn from the name puts finance/ap on screen as ap under the root, which
  // states a position in the hierarchy that domain does not hold and makes two
  // domains ending in the same segment indistinguishable.
  it("names a folded subdomain card by the stretch of path it navigates across", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        body: {
          path: "",
          subdomains: [
            {
              path: "finance/ap",
              name: "ap",
              description: "Accounts payable.",
            },
            { path: "vendor/ap", name: "ap", description: "Vendor payments." },
          ],
          notable: [],
        },
      },
    });
    render(<App />);
    const browser = await screen.findByLabelText("Domain browser");
    const grid = within(browser).getByRole("list", { name: "Subdomains" });
    const cards = within(grid).getAllByRole("listitem");
    expect(
      cards.map((card) => within(card).getByRole("link").textContent),
    ).toEqual(["finance/ap", "vendor/ap"]);
  });

  // The §6.10 envelope says whether the condition clears on its own. Where it
  // says the condition does not, offering a retry sends the reader round a
  // loop that ends the same way, so the state says so instead.
  it("offers no retry of a read the envelope reports as not clearing on its own", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        status: 400,
        body: {
          code: "registry.invalid_argument",
          message: "no such domain",
          retryable: false,
        },
      },
    });
    render(<App />);
    const page = await screen.findByTestId("domain-failed");
    expect(within(page).queryByRole("button", { name: "Retry" })).toBeNull();
    expect(page.textContent).toContain(
      "registry.invalid_argument · not retryable",
    );
  });

  // The breadcrumb above the title carries the ancestry, so the title names the
  // domain the page is on and nothing above it.
  it("titles a deep domain with its leaf name rather than the whole path", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        body: {
          path: "platform/observability/tracing",
          subdomains: [],
          notable: [],
        },
      },
    });
    goTo("#/domain/platform%2Fobservability%2Ftracing");
    render(<App />);
    const browser = await screen.findByLabelText("Domain browser");
    expect(within(browser).getByRole("heading", { level: 1 }).textContent).toBe(
      "tracing",
    );
    // The ancestry is still reachable, as the trail above the title.
    const trail = within(browser).getByRole("navigation", {
      name: "Breadcrumb",
    });
    expect(
      within(trail).getByRole("link", { name: "observability" }),
    ).toBeTruthy();
  });

  // The trail reads as one path rather than as a row of links: a slash
  // between the segments, the registry root opening it as "catalog", and the
  // domain the reader is already on standing as plain text.
  it("separates the breadcrumb segments and leaves the current one unlinked", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        body: {
          path: "platform/observability/tracing",
          subdomains: [],
          notable: [],
        },
      },
    });
    goTo("#/domain/platform%2Fobservability%2Ftracing");
    render(<App />);
    const browser = await screen.findByLabelText("Domain browser");
    const trail = within(browser).getByRole("navigation", {
      name: "Breadcrumb",
    });
    expect(trail.textContent).toBe("catalog/platform/observability/tracing");
    expect(within(trail).getByRole("link", { name: "catalog" })).toBeTruthy();
    // The page's own segment carries no link, and it is the only one marked
    // as the reader's position.
    expect(within(trail).queryByRole("link", { name: "tracing" })).toBeNull();
    const here = trail.querySelectorAll('[aria-current="page"]');
    expect(here.length).toBe(1);
    expect(here[0].textContent).toBe("tracing");
    // The trail is set in the identifier face, at the weight that separates
    // the reader's position from the ancestry above it.
    expect(window.getComputedStyle(trail).fontFamily).toBe("var(--font-mono)");
    expect(window.getComputedStyle(here[0]).fontWeight).toBe("500");
  });

  // A trail long enough to wrap must not break between a separator and the
  // segment it introduces, or the first line ends on a dangling slash and the
  // path reads as truncated mid-segment. Each separator is grouped with the
  // crumb that follows it so the pair wraps as one unit.
  it("groups each breadcrumb separator with the segment that follows it", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        body: {
          path: "platform/networking/edge/loadbalancing",
          subdomains: [],
          notable: [],
        },
      },
    });
    goTo("#/domain/platform%2Fnetworking%2Fedge%2Floadbalancing");
    render(<App />);
    const browser = await screen.findByLabelText("Domain browser");
    const trail = within(browser).getByRole("navigation", {
      name: "Breadcrumb",
    });
    const labels = [
      "catalog",
      "platform",
      "networking",
      "edge",
      "loadbalancing",
    ];
    const crumbs = Array.from(trail.children);
    expect(crumbs.length).toBe(labels.length);
    crumbs.forEach((crumb, index) => {
      expect(crumb.textContent).toBe(
        index === 0 ? labels[0] : `/${labels[index]}`,
      );
      // No separator stands alone at the wrap boundary: every one of them is
      // inside the group whose segment it introduces.
      const sep = crumb.querySelector(".breadcrumb-sep");
      if (index === 0) {
        expect(sep).toBeNull();
      } else {
        expect(sep).toBeTruthy();
        expect(crumb.firstElementChild).toBe(sep);
      }
    });
    expect(trail.querySelectorAll(".breadcrumb-sep").length).toBe(
      labels.length - 1,
    );
  });

  it("carries the registry root as the single word catalog", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": { body: emptyDomain },
    });
    render(<App />);
    const browser = await screen.findByLabelText("Domain browser");
    const trail = within(browser).getByRole("navigation", {
      name: "Breadcrumb",
    });
    expect(trail.textContent).toBe("catalog");
    expect(within(trail).queryByRole("link")).toBeNull();
  });

  it("titles the registry root for what it holds, having no leaf segment", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": { body: emptyDomain },
    });
    render(<App />);
    const browser = await screen.findByLabelText("Domain browser");
    expect(within(browser).getByRole("heading", { level: 1 }).textContent).toBe(
      "All domains",
    );
  });

  // §4.5.5 fixes that the root carries no description, so the entry screen
  // states what the root is rather than reporting the absence as it would for
  // a domain whose author left one out.
  it("states what the registry root is instead of reporting a missing description", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": { body: emptyDomain },
    });
    render(<App />);
    await screen.findByLabelText("Domain browser");
    expect(
      screen.getByText(
        "This is the top of the domain hierarchy, and every domain the registry holds sits below it.",
      ),
    ).toBeTruthy();
    expect(
      screen.queryByText("This domain carries no description."),
    ).toBeNull();
  });

  // Every visible artifact sits under the root, so the entry screen's own
  // count is the §4.5.2 catalog rather than the artifacts the empty path
  // holds directly, of which a registry organized into domains has none.
  it("counts the whole catalog and the top-level domains in the root header", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/catalog": { body: catalogOf(312) },
      "/v1/load_domain": {
        body: {
          path: "",
          subdomains: [
            { path: "eng", name: "eng" },
            { path: "finance/ap", name: "ap" },
          ],
          notable: [],
        },
      },
    });
    render(<App />);
    const browser = await screen.findByLabelText("Domain browser");
    const head = within(browser).getByRole("heading", {
      level: 1,
    }).parentElement;
    expect(head?.textContent).toBe("All domains312 ARTIFACTS2 DOMAINS");
    // The catalog count heads the page without continuing the listing: those
    // artifacts sit under the subdomains rather than past a trimmed edge.
    expect(screen.queryByTestId("listing-continuation")).toBeNull();
  });

  // The counts qualify the domain name, so they sit on the title's line in
  // the marker casing a badge carries, with what the domain holds read first.
  it("sets the counts beside the domain name in caps, artifacts first", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        body: {
          path: "platform",
          subdomains: [{ path: "platform/ci", name: "ci" }],
          notable: [
            { id: "platform/deploy", type: "skill" },
            { id: "platform/build", type: "skill" },
          ],
        },
      },
    });
    goTo("#/domain/platform");
    render(<App />);
    const browser = await screen.findByLabelText("Domain browser");
    const heading = within(browser).getByRole("heading", { level: 1 });
    const head = heading.parentElement;
    expect(head).not.toBeNull();
    expect(head?.textContent).toBe("platform2 ARTIFACTS1 SUBDOMAIN");
    // A count qualifies the name it sits beside, so it takes the badge's
    // soft tone. Drawn outlined it competes with the type badges the listing
    // below the heading is scanned by.
    for (const count of ["2 ARTIFACTS", "1 SUBDOMAIN"]) {
      expect(
        within(head as HTMLElement).getByText(count).className.split(" "),
      ).toContain("badge-soft");
    }
  });

  // A count of zero states nothing the empty listing below does not already
  // state in prose, so it draws no marker at all.
  it("draws no count badge for a domain that holds none of that thing", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        body: {
          path: "",
          subdomains: [{ path: "eng", name: "eng" }],
          notable: [],
        },
      },
    });
    render(<App />);
    const browser = await screen.findByLabelText("Domain browser");
    const head = within(browser).getByRole("heading", {
      level: 1,
    }).parentElement;
    expect(head?.textContent).toBe("All domains1 DOMAIN");
  });

  it("renders a domain that carries neither subdomains nor artifacts as a finished page", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": { body: emptyDomain },
    });
    render(<App />);
    await screen.findByLabelText("Domain browser");
    expect(screen.getByText("No subdomains")).toBeTruthy();
    expect(
      screen.getByText("Domains nested under this one appear here."),
    ).toBeTruthy();
    expect(screen.getByText("No artifacts here")).toBeTruthy();
    expect(
      screen.getByText(
        "Artifacts published directly to this domain appear here.",
      ),
    ).toBeTruthy();
  });

  // Spec: §13.10 — the page-scope absence is the two-line state: a title in
  // the surface's own ink over the sentence that says what would appear
  // there. Drawn as the sentence alone, the card reads as a caption that lost
  // its content rather than as the state the design draws.
  it("draws a page-scope absence as a title over its sentence", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": { body: emptyDomain },
    });
    render(<App />);
    await screen.findByLabelText("Domain browser");
    const card = screen
      .getByText("Artifacts published directly to this domain appear here.")
      .closest(".empty") as HTMLElement;
    expect(card.className.split(" ")).toContain("empty-page");
    const lines = Array.from(card.children).map((line) => [
      line.className,
      line.textContent,
    ]);
    expect(lines).toEqual([
      ["empty-title", "No artifacts here"],
      [
        "empty-body",
        "Artifacts published directly to this domain appear here.",
      ],
    ]);
    expect(window.getComputedStyle(card.children[0]).color).toBe("var(--ink)");
  });

  // §4.5.5 folding can leave a domain with an empty subdomain list and an
  // empty direct listing while its whole content arrived lifted. The two
  // empty panels would then contradict the header count and stand between it
  // and the entries it counts.
  it("states no absence on a domain whose every entry arrived folded", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        body: {
          path: "finance",
          description: "Finance.",
          subdomains: [],
          notable: [
            { id: "finance/ap/pay-invoice", type: "skill", folded_from: "ap" },
            { id: "finance/ar/send-invoice", type: "skill", folded_from: "ar" },
          ],
        },
      },
    });
    goTo("#/domain/finance");
    render(<App />);
    const browser = await screen.findByLabelText("Domain browser");
    expect(within(browser).queryByText("No subdomains")).toBeNull();
    expect(within(browser).queryByText("No artifacts here")).toBeNull();
    // The header count and the group it refers to are what the screen holds.
    expect(within(browser).getByText("2 ARTIFACTS")).toBeTruthy();
    expect(within(browser).getByText("Lifted from sparse subdomains")).toBeTruthy();
    expect(within(browser).getByText("finance/ap/pay-invoice")).toBeTruthy();
  });
});

describe("search", () => {
  // The design opens the search content column on the query field. A page
  // title over a field already labelled "Search artifacts" restates the field
  // and pushes it and the filter row down, so the surface carries no heading
  // and the landmark label is what names it. The layer panel keeps its title,
  // which is what tells the two apart.
  it("opens on the query field rather than on a page title", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": { body: rootDomains },
      "/v1/search_artifacts": { body: { total_matched: 0 } },
      "/v1/layers": { body: { layers: [] } },
    });
    goTo("#/search/review");
    render(<App />);
    const surface = await screen.findByLabelText("Search");
    expect(within(surface).queryAllByRole("heading")).toEqual([]);
    // The first thing the column draws is the field itself.
    expect(surface.firstElementChild?.className).toBe("search-field");

    // The layer panel is the surface the design does title, so the absence
    // above reads as this surface's own rule rather than as a shell that
    // draws no headings at all.
    goTo("#/layers");
    render(<App />);
    const panel = await screen.findByLabelText("Layer panel");
    expect(within(panel).getByRole("heading", { level: 1 }).textContent).toBe(
      "Layers",
    );
  });

  it("carries the type, scope, and tag filters on the request it issues", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": { body: rootDomains },
      "/v1/search_artifacts": { body: { total_matched: 0 } },
    });
    goTo("#/search/review");
    render(<App />);
    await screen.findByLabelText("Search");
    // The filter row is pills rather than text boxes: the type and the scope
    // are chosen from their dropdowns, and a tag is added through the row's
    // token entry.
    selectFilter("type", "skill");
    selectFilter("scope", "platform");
    addToken("tag", "review");
    addToken("tag", "security");
    await waitFor(() => {
      const last =
        requests.filter((r) => r.url.startsWith("/v1/search_artifacts")).at(-1)
          ?.url ?? "";
      const query = new URLSearchParams(last.split("?")[1] ?? "");
      expect(query.get("query")).toBe("review");
      expect(query.get("type")).toBe("skill");
      expect(query.get("scope")).toBe("platform");
      expect(query.get("tags")).toBe("review,security");
    });
  });

  // The row states what it is and carries one control per filter. It offers
  // no chip per artifact type, because a row that spends its width on the
  // unapplied values of one filter states the filter set less clearly than
  // the label and the dropdown do.
  it("names the filter row and offers one control per filter rather than a chip per type", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": { body: rootDomains },
      "/v1/search_artifacts": { body: { total_matched: 0 } },
    });
    goTo("#/search/review");
    render(<App />);
    await screen.findByLabelText("Search");
    expect(screen.getByText("Filters")).toBeTruthy();
    for (const name of ["skill", "agent", "context", "mcp-server"]) {
      expect(screen.queryByRole("button", { name })).toBeNull();
    }
    expect(screen.queryByRole("button", { name: "+ type" })).toBeNull();
    expect(screen.queryByRole("button", { name: "+ scope" })).toBeNull();
    expect(screen.getByRole("button", { name: "+ tag" })).toBeTruthy();
    // The scope dropdown offers the registry's own top-level domains, since a
    // scope is a domain path rather than a value the reader invents.
    const scope = await screen.findByLabelText("Filter by scope");
    expect(
      within(scope)
        .getAllByRole("option")
        .map((option) => option.textContent),
    ).toEqual(["scope: all", "scope: platform", "scope: finance"]);
    // An applied filter names the filter it applies and carries its own
    // remove control, and the dropdown it replaces is gone.
    selectFilter("type", "skill");
    expect(await screen.findByText("type: skill")).toBeTruthy();
    expect(screen.queryByLabelText("Filter by type")).toBeNull();
    fireEvent.click(
      screen.getByRole("button", { name: "Remove the skill filter" }),
    );
    await waitFor(() => {
      expect(screen.getByLabelText("Filter by type")).toBeTruthy();
    });
  });

  // The tag entry stands in the add control's place while it is open, so a
  // reader who opened it to look and then moved on loses the control for the
  // rest of the session unless it dismisses itself. It carries the paths
  // every other transient popup in the shell carries.
  it("dismisses the tag entry on Escape and on an outside press", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": { body: rootDomains },
      "/v1/search_artifacts": { body: { total_matched: 0 } },
    });
    goTo("#/search/review");
    render(<App />);
    await screen.findByLabelText("Search");

    const open = () => screen.getByRole("button", { name: "+ tag" });
    fireEvent.click(open());
    expect(screen.getByLabelText("Add a tag filter")).toBeTruthy();
    fireEvent.keyDown(document, { key: "Escape" });
    expect(screen.queryByLabelText("Add a tag filter")).toBeNull();
    expect(document.activeElement).toBe(open());

    fireEvent.click(open());
    // Text the reader abandoned does not come back with the field.
    fireEvent.change(screen.getByLabelText("Add a tag filter"), {
      target: { value: "review" },
    });
    fireEvent.pointerDown(document.body);
    expect(screen.queryByLabelText("Add a tag filter")).toBeNull();
    expect(screen.queryByText("tag: review")).toBeNull();
    fireEvent.click(open());
    expect(
      (screen.getByLabelText("Add a tag filter") as HTMLInputElement).value,
    ).toBe("");
  });

  // A submitted value closes the field the same way a dismissal does, and the
  // add control stands back in its place, so it takes the focus. Without it a
  // reader who applied a tag filter from the keyboard is dropped on the
  // document body and has to tab through the whole shell to get back to the
  // filter row they were standing on.
  it("returns focus to the add control when a tag filter is applied", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": { body: rootDomains },
      "/v1/search_artifacts": { body: { total_matched: 0 } },
    });
    goTo("#/search/review");
    render(<App />);
    await screen.findByLabelText("Search");

    const open = () => screen.getByRole("button", { name: "+ tag" });
    fireEvent.click(open());
    const field = screen.getByLabelText("Add a tag filter");
    fireEvent.change(field, { target: { value: "review" } });
    // The ⏎ is consumed, because the add control takes the focus back as the
    // field closes, and the browser would otherwise read the same key as an
    // activation of the button now under it and reopen the field.
    expect(fireEvent.keyDown(field, { key: "Enter" })).toBe(false);
    expect(await screen.findByText("tag: review")).toBeTruthy();
    expect(document.activeElement).toBe(open());
  });

  // A native select is as wide as its widest option, so a catalog carrying a
  // deep domain path stretched the scope control to that path and left a gap
  // between the label and the indicator. The closed pill draws its own label,
  // which is what sets its width, and the option list stays behind it.
  it("draws the closed dropdown's own label so the option list does not set its width", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        body: {
          path: "",
          subdomains: [
            {
              path: "finance/accounts-payable/invoicing/settlement",
              name: "settlement",
            },
          ],
          notable: [],
        },
      },
      "/v1/search_artifacts": { body: { total_matched: 0 } },
    });
    goTo("#/search/");
    render(<App />);
    const scope = await screen.findByLabelText("Filter by scope");
    const pill = scope.closest(".pill-select");
    expect(pill).not.toBeNull();
    const drawn = pill?.querySelector(".pill-select-label");
    expect(drawn?.textContent).toBe("scope: all");
    expect(drawn?.getAttribute("aria-hidden")).toBe("true");
    // The label the pill draws is the closed state of the control, so it is
    // the short one rather than the deepest path the list offers.
    expect(
      within(scope)
        .getAllByRole("option")
        .map((option) => option.textContent),
    ).toContain("scope: finance/accounts-payable/invoicing/settlement");
    expect(pill?.querySelector(".chevron")).not.toBeNull();
  });

  // A §4.5.5 sparse chain reaches the root listing folded into one entry, so
  // the domains it crossed appear in no response field of their own. Each is
  // a page the browser navigates to and a prefix the search matches, so the
  // dropdown offers every segment rather than the folded label alone.
  it("offers each segment of a folded domain chain as a scope", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        body: {
          path: "",
          subdomains: [
            { path: "finance/ap", name: "ap" },
            { path: "finance/ar", name: "ar" },
            { path: "platform", name: "platform" },
          ],
          notable: [],
        },
      },
      "/v1/search_artifacts": { body: { total_matched: 0 } },
    });
    goTo("#/search/review");
    render(<App />);
    await screen.findByLabelText("Search");
    const scope = await screen.findByLabelText("Filter by scope");
    expect(
      within(scope)
        .getAllByRole("option")
        .map((option) => option.textContent),
    ).toEqual([
      "scope: all",
      "scope: finance",
      "scope: finance/ap",
      "scope: finance/ar",
      "scope: platform",
    ]);
    selectFilter("scope", "finance");
    await waitFor(() => {
      expect(lastSearch().get("scope")).toBe("finance");
    });
  });

  // The root read expands more than one level, so an unfolded top-level
  // domain carries its children in its own `subdomains` rather than at the
  // top level. Each child is a page the browser navigates to and a prefix the
  // search matches, so the surface reads the tree at the depth the sidebar
  // opens at and the dropdown offers the whole returned subtree.
  it("offers a nested subdomain the root read returned as a scope", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      // The registry answers a shallower read with the top-level entries
      // alone, the way a depth of 1 does, so a surface that asks for less
      // than the tree it walks is told apart from one that asks for the
      // whole subtree.
      "/v1/load_domain": {
        body: {
          path: "",
          subdomains: [
            { path: "edge", name: "edge" },
            { path: "platform", name: "platform" },
          ],
          notable: [],
        },
      },
      "/v1/load_domain?depth=2": {
        body: {
          path: "",
          subdomains: [
            {
              path: "edge",
              name: "edge",
              subdomains: [
                {
                  path: "edge/child-one",
                  name: "child-one",
                  subdomains: [{ path: "edge/child-one/leaf", name: "leaf" }],
                },
              ],
            },
            { path: "platform", name: "platform" },
          ],
          notable: [],
        },
      },
      "/v1/search_artifacts": { body: { total_matched: 0 } },
    });
    goTo("#/search/review");
    render(<App />);
    await screen.findByLabelText("Search");
    const scope = await screen.findByLabelText("Filter by scope");
    expect(
      within(scope)
        .getAllByRole("option")
        .map((option) => option.textContent),
    ).toEqual([
      "scope: all",
      "scope: edge",
      "scope: edge/child-one",
      "scope: edge/child-one/leaf",
      "scope: platform",
    ]);
    selectFilter("scope", "edge/child-one");
    await waitFor(() => {
      expect(lastSearch().get("scope")).toBe("edge/child-one");
    });
  });

  // The match count is taken before the cap truncates the list, so fewer
  // results than matches is the ordinary outcome and reads as one. The two
  // optional result fields are driven present and absent in the same case.
  it("reports the returned count against the match count and labels a vector-only match", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/search_artifacts": {
        body: {
          query: "review",
          total_matched: 143,
          results: [
            {
              id: "platform/review",
              type: "skill",
              version: "2.0.0",
              score: 8.5,
              sensitivity: "internal",
            },
            { id: "platform/weaker", type: "skill", score: 2.1 },
            // The registry marshals the score with omitempty, so the zero
            // score a fused-in vector-only candidate carries never reaches
            // the wire. The row arrives with no score key at all, which is
            // what a surface reading the field's presence would render as an
            // unranked row.
            { id: "platform/meaning", type: "skill" },
          ],
        },
      },
    });
    goTo("#/search/review");
    render(<App />);
    const count = await screen.findByTestId("result-count");
    expect(count.textContent).toBe("Showing 3 of 143 matches");
    // The inline count is set in the proportional face with the two numerals
    // lifted out of the quiet surrounding words. The mono variant belongs to
    // a count that sits inside a field.
    expect(count.className).not.toContain("mono");
    expect(
      Array.from(count.querySelectorAll("strong")).map(
        (node) => node.textContent,
      ),
    ).toEqual(["3", "143"]);
    // A ranked row keeps its type and version beside the identifier rather
    // than in the listing's right-hand column.
    expect(screen.queryByTestId("artifact-row-aside")).toBeNull();
    // The version reads as meta beside the type rather than as a third badge.
    // The type is one of a closed vocabulary and carries the outline; boxing
    // the version too gives the row three equal-weight pills, and the same
    // artifact in the domain listing draws its version bare, so the two
    // surfaces would state one field two ways.
    expect(screen.getByText("v2.0.0").className).toBe(
      "mono quiet artifact-version",
    );
    // A ranked result set spans the whole catalog and stands under no domain
    // heading, so the row's link carries the whole identifier and the row
    // prints the leaf once. The leaf-plus-path pairing belongs to the domain
    // listing, where the heading already supplies the levels above the row.
    const first = screen.getByRole("link", { name: "platform/review" });
    const head = first.parentElement as HTMLElement;
    expect(first.className).toBe("mono artifact-id");
    expect(head.querySelector(".artifact-path")).toBeNull();
    expect(within(head).queryAllByText("review")).toEqual([]);
    expect(
      within(first.parentElement as HTMLElement).getByText("SKILL"),
    ).toBeTruthy();
    // The classification names its axis on the row as it does in the viewer.
    expect(screen.getByText("sensitivity: internal")).toBeTruthy();
    expect(screen.getByText("matched by meaning")).toBeTruthy();
    // Relevance is drawn as bars ranked against the strongest score in the
    // set, and no row states a score. The vector-only row draws no bars and
    // still occupies the column, so the rows stay aligned.
    const indicators = screen.getAllByTestId("relevance-bars");
    expect(indicators.map((el) => el.getAttribute("data-filled"))).toEqual([
      "4",
      "1",
      "0",
    ]);
    expect(indicators[2].childElementCount).toBe(0);
    // The indicator leads the row instead of trailing the badges. A badge row
    // is only as wide as the values that row happens to carry, so an indicator
    // drawn after it lands on a different x position on every row.
    for (const indicator of indicators) {
      const column = indicator.parentElement as HTMLElement;
      expect(column.className).toBe("artifact-row-relevance");
      expect(column.previousElementSibling).toBeNull();
      expect((column.nextElementSibling as HTMLElement).className).toBe(
        "artifact-row-body",
      );
      expect(indicator.closest(".artifact-row-head")).toBeNull();
    }
    expect(screen.queryByText(/score 8/)).toBeNull();
  });

  // Spec: §13.10 — the count names what it counts. Two bare numerals leave a
  // reader to guess what the second one is, and the domain listing states its
  // own count with the noun, so the two surfaces read alike.
  it("names the counted noun and agrees with a lone match", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/search_artifacts": {
        body: {
          query: "tls",
          total_matched: 1,
          results: [{ id: "platform/tls", type: "skill" }],
        },
      },
    });
    goTo("#/search/tls");
    render(<App />);
    const count = await screen.findByTestId("result-count");
    expect(count.textContent).toBe("Showing 1 of 1 match");
    // The noun stays at the paragraph's quiet tone, so only the numerals lift
    // to ink.
    expect(
      Array.from(count.querySelectorAll("strong")).map(
        (node) => node.textContent,
      ),
    ).toEqual(["1", "1"]);
  });

  // Spec: §13.10 — a truncated result list reaches the results the cap
  // withheld. §5 search takes a result count and no offset, so the
  // continuation raises `top_k` on the same request, and it stops at the
  // largest count the endpoint serves rather than issuing a request §5
  // refuses. Past that the recovery line stands alone.
  it("continues a truncated result list up to the largest count search serves", async () => {
    const hits = (n: number) =>
      Array.from({ length: n }, (_, i) => ({
        id: `platform/svc${String(i + 1)}`,
        type: "skill",
        score: 9 - i * 0.01,
      }));
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/search_artifacts?query=review&top_k=10": {
        body: { total_matched: 143, results: hits(10) },
      },
      "/v1/search_artifacts?query=review&top_k=30": {
        body: { total_matched: 143, results: hits(30) },
      },
      "/v1/search_artifacts?query=review&top_k=50": {
        body: { total_matched: 143, results: hits(50) },
      },
    });
    goTo("#/search/review");
    render(<App />);
    expect((await screen.findByTestId("result-count")).textContent).toBe(
      "Showing 10 of 143 matches",
    );
    const foot = screen.getByTestId("search-continuation");
    // The caption sits with the control, so the reader reads the order the
    // withheld results arrive in beside the control that asks for them.
    expect(within(foot).getByText("Ranked by relevance.")).toBeTruthy();
    fireEvent.click(within(foot).getByRole("button", { name: "Load 20 more" }));
    await waitFor(() => {
      expect(lastSearch().get("top_k")).toBe("30");
    });
    expect((await screen.findByTestId("result-count")).textContent).toBe(
      "Showing 30 of 143 matches",
    );
    fireEvent.click(
      await screen.findByRole("button", { name: "Load 20 more" }),
    );
    await waitFor(() => {
      expect(lastSearch().get("top_k")).toBe("50");
    });
    expect((await screen.findByTestId("result-count")).textContent).toBe(
      "Showing 50 of 143 matches",
    );
    // The cap is spent, so the control is gone and narrowing the request is
    // what the surface offers.
    expect(screen.queryByTestId("search-continuation")).toBeNull();
    expect(
      screen.getByText(
        "Narrow the result set with a filter, drill into a subdomain, or run a more specific query.",
      ),
    ).toBeTruthy();
  });

  // Spec: §13.10 — the reader who continues the list by keyboard stays on the
  // control they pressed. The widened read is the same request at a raised
  // count, so the list it widens stands while it is in flight rather than
  // being replaced by the loading line: replacing it unmounts the button from
  // under the focus, the focus falls to the document body, and the next Tab
  // restarts at the top of the page.
  it("holds the focused continuation control across the widened request", async () => {
    const hits = (n: number) =>
      Array.from({ length: n }, (_, i) => ({
        id: `platform/svc${String(i + 1)}`,
        type: "skill",
        score: 9 - i * 0.01,
      }));
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/search_artifacts?query=review&top_k=10": {
        body: { total_matched: 60, results: hits(10) },
      },
      "/v1/search_artifacts?query=review&top_k=30": {
        body: { total_matched: 60, results: hits(30) },
        deferred: true,
      },
      "/v1/search_artifacts?query=review&top_k=50": {
        body: { total_matched: 60, results: hits(50) },
        deferred: true,
      },
    });
    goTo("#/search/review");
    render(<App />);
    await screen.findByTestId("result-count");
    const control = screen.getByTestId("search-continue");
    control.focus();
    expect(document.activeElement).toBe(control);
    fireEvent.click(control);
    // The request is in flight. The rows the reader already had are still
    // drawn, the control is the same element it was, and the focus is still
    // on it. It refuses a second press while it waits.
    expect(lastSearch().get("top_k")).toBe("30");
    expect(screen.getByTestId("search-continue")).toBe(control);
    expect(document.activeElement).toBe(control);
    expect(control.getAttribute("aria-disabled")).toBe("true");
    expect(screen.getAllByTestId("relevance-bars").length).toBe(10);
    fireEvent.click(control);
    expect(
      requests.filter((r) => r.url.includes("top_k=50")).length,
    ).toBe(0);
    // The results land on the same control, which is now live again and asks
    // for the next page.
    await waitFor(() => {
      expect(screen.getAllByTestId("relevance-bars").length).toBe(30);
    });
    expect(document.activeElement).toBe(control);
    expect(control.getAttribute("aria-disabled")).toBeNull();
    expect(control.textContent).toBe("Load 20 more");
    // The last step is the same: the control the reader is standing on stays
    // mounted while the request that spends the cap is in flight.
    fireEvent.click(control);
    expect(lastSearch().get("top_k")).toBe("50");
    expect(screen.getByTestId("search-continue")).toBe(control);
    expect(document.activeElement).toBe(control);
    // That press spent the cap, so the control it was on is gone once its own
    // results land. The focus goes to the first result the press appended
    // rather than to the document body, which is where the reader was
    // reading.
    await waitFor(() => {
      expect(screen.getAllByTestId("relevance-bars").length).toBe(50);
    });
    expect(screen.queryByTestId("search-continue")).toBeNull();
    expect((document.activeElement as HTMLElement).textContent).toBe(
      "platform/svc31",
    );
  });

  // Spec: §13.10 — the continuation asks for what is still withheld rather
  // than a fixed page, and it is gone once the list is whole. A new request
  // is a new result set, so an edited filter drops the cap back.
  it("asks for the withheld results alone and resets the cap on a new request", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/search_artifacts?query=review&top_k=10": {
        body: {
          total_matched: 12,
          results: Array.from({ length: 10 }, (_, i) => ({
            id: `platform/svc${String(i + 1)}`,
            type: "skill",
          })),
        },
      },
      "/v1/search_artifacts?query=review&top_k=12": {
        body: {
          total_matched: 12,
          results: Array.from({ length: 12 }, (_, i) => ({
            id: `platform/svc${String(i + 1)}`,
            type: "skill",
          })),
        },
      },
      "/v1/search_artifacts?query=review&type=skill&top_k=10": {
        body: {
          total_matched: 4,
          results: [{ id: "platform/svc1", type: "skill" }],
        },
      },
    });
    goTo("#/search/review");
    render(<App />);
    expect((await screen.findByTestId("result-count")).textContent).toBe(
      "Showing 10 of 12 matches",
    );
    fireEvent.click(await screen.findByRole("button", { name: "Load 2 more" }));
    await waitFor(() => {
      expect(lastSearch().get("top_k")).toBe("12");
    });
    expect((await screen.findByTestId("result-count")).textContent).toBe(
      "Showing 12 of 12 matches",
    );
    expect(screen.queryByTestId("search-continuation")).toBeNull();
    // The raised cap belonged to the request that carried it, so the filtered
    // request opens at the first page again.
    selectFilter("type", "skill");
    await waitFor(() => {
      expect(lastSearch().get("type")).toBe("skill");
    });
    expect(lastSearch().get("top_k")).toBe("10");
  });

  // Spec: §13.10 — a standalone registry started with --no-embeddings serves
  // BM25 alone, and an empty query returns every match at score zero, so a
  // whole result set can reach the page unscored. Nothing in such a set was
  // matched by vector similarity, so the surface draws no relevance indicator
  // and claims no semantic match on any row.
  it("draws no relevance indicator when no result in the set carries a score", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/search_artifacts": {
        body: {
          total_matched: 2,
          results: [
            {
              id: "eng/deploy",
              type: "context",
              description: "Deploy runbook",
            },
            { id: "finance/ap/pay-invoice", type: "skill" },
          ],
        },
      },
    });
    goTo("#/search/");
    render(<App />);
    expect(await screen.findByRole("link", { name: "eng/deploy" })).toBeTruthy();
    expect(screen.queryByText("matched by meaning")).toBeNull();
    expect(screen.queryAllByTestId("relevance-bars")).toEqual([]);
    expect(screen.queryByTestId("artifact-row-relevance")).toBeNull();
  });

  // An active filter carries the control that removes it, which is what
  // returns the row to the unfiltered read.
  it("drops a filter from the request when its pill is removed", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/search_artifacts": { body: { total_matched: 0 } },
    });
    goTo("#/search/review");
    render(<App />);
    await screen.findByLabelText("Search");
    addToken("tag", "security");
    await waitFor(() => {
      expect(lastSearch().get("tags")).toBe("security");
    });
    fireEvent.click(
      screen.getByRole("button", { name: "Remove the security filter" }),
    );
    await waitFor(() => {
      expect(lastSearch().get("tags")).toBeNull();
    });
  });

  // A search is addressable: the query and the active filters live in the
  // route, so the address bar names what is on screen and the reader can
  // reload it or send it to someone else.
  it("carries the typed query and the active filters in the location hash", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/search_artifacts": { body: { total_matched: 0 } },
    });
    goTo(searchHref(""));
    render(<App />);
    await screen.findByLabelText("Search");
    fireEvent.change(screen.getByLabelText("Search artifacts"), {
      target: { value: "deploy" },
    });
    await waitFor(() => {
      expect(window.location.hash).toBe(searchHref("deploy"));
    });
    selectFilter("type", "skill");
    addToken("tag", "security");
    await waitFor(() => {
      expect(window.location.hash).toBe(
        searchHref("type:skill tag:security deploy"),
      );
    });
    // The hash the surface writes is the one the surface reads, so a reload
    // of it stands the same query and the same pills back up.
    const restored = parseQueryLine(
      decodeURIComponent(window.location.hash.replace("#/search/", "")),
    );
    expect(restored).toEqual({
      query: "deploy",
      type: "skill",
      scope: "",
      tags: ["security"],
    });
  });

  it("renders a search that matched nothing as an empty result rather than a failure", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/search_artifacts": { body: { total_matched: 0 } },
    });
    goTo("#/search/nothing");
    render(<App />);
    expect(
      await screen.findByText("Widen the query."),
    ).toBeTruthy();
  });

  // Spec: §13.10
  it("names the empty catalog rather than a missed query when neither was issued", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/search_artifacts": { body: { total_matched: 0 } },
    });
    goTo("#/search/");
    render(<App />);
    // The browse carried no query and no filter, so nothing was searched for
    // and there is nothing to widen. The remedy is the one the sidebar tree
    // names for the same registry.
    const empty = await screen.findByText(
      "Register a layer to fill it.",
    );
    expect(empty).toBeTruthy();
    expect(
      screen.queryByText("Widen the query."),
    ).toBeNull();
    // Typing a query makes the empty result the answer to that query, so the
    // no-match remedy returns.
    fireEvent.change(screen.getByLabelText("Search artifacts"), {
      target: { value: "deploy" },
    });
    expect(
      await screen.findByText("Widen the query."),
    ).toBeTruthy();
  });

  // Spec: §13.10
  it("names a filtered browse with no query as a search that matched nothing", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/search_artifacts": { body: { total_matched: 0 } },
    });
    goTo("#/search/");
    render(<App />);
    await screen.findByText(
      "Register a layer to fill it.",
    );
    // A filter applied with no query text is a request the reader issued, and
    // the filter is a control the row carries, so the remedy names it.
    selectFilter("type", "skill");
    expect(
      await screen.findByText(
        "Widen the query or clear a filter.",
      ),
    ).toBeTruthy();
  });

  it("offers clearing a filter only when the row carries one", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/search_artifacts": { body: { total_matched: 0 } },
    });
    goTo("#/search/nothing");
    render(<App />);
    // Nothing is applied, so the row has no filter to clear and the remedy
    // names only the query.
    const plain = await screen.findByText("Widen the query.");
    expect(plain.textContent).not.toContain("filter");
    // Applying a type filter gives the reader a control to undo, and the
    // sentence names it.
    selectFilter("type", "skill");
    expect(
      await screen.findByText(
        "Widen the query or clear a filter.",
      ),
    ).toBeTruthy();
  });

  // Spec: §13.10 — narrowing the search swaps the count and can replace the
  // whole list with a sentence, and neither change moves focus. A reader who
  // cannot see the surface is told the settled count and the moment the list
  // empties through a polite region.
  it("announces the settled result count and the emptied list", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/search_artifacts?query=review&top_k=10": {
        body: {
          total_matched: 2,
          results: [
            { id: "platform/review", type: "skill", score: 8.5 },
            { id: "platform/weaker", type: "skill", score: 2.1 },
          ],
        },
      },
      "/v1/search_artifacts": { body: { total_matched: 0 } },
    });
    goTo("#/search/review");
    render(<App />);
    const region = await screen.findByTestId("search-announcement");
    // The region is polite and mounted before its text arrives, so the change
    // happens inside a node the accessibility tree already holds.
    expect(region.getAttribute("role")).toBe("status");
    expect(region.getAttribute("aria-live")).toBe("polite");
    expect(region.className).toBe("assistive-only");
    await waitFor(() => {
      expect(region.textContent).toBe("2 of 2 artifacts matched.");
    });
    // Narrowing to a query nothing answers empties the list, and the region
    // states the outcome rather than repeating the remedy the page draws.
    fireEvent.change(screen.getByLabelText("Search artifacts"), {
      target: { value: "zzzznotamatch" },
    });
    await screen.findByText("Widen the query.");
    expect(region.textContent).toBe("No artifact matched.");
    // A filter narrowed over a query the row still carries lands on the same
    // outcome, because the count and the list change the same way.
    selectFilter("type", "skill");
    await waitFor(() => {
      expect(lastSearch().get("type")).toBe("skill");
    });
    expect(region.textContent).toBe("No artifact matched.");
  });

  // Spec: §13.10 — a browse that no filter and no query narrowed reports an
  // empty catalog rather than a search that missed.
  it("announces an empty catalog as the browse it answered", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/search_artifacts": { body: { total_matched: 0 } },
    });
    goTo("#/search/");
    render(<App />);
    const region = await screen.findByTestId("search-announcement");
    await waitFor(() => {
      expect(region.textContent).toBe("The catalog holds no artifacts.");
    });
  });

  it("draws the empty result as a card set quieter than a result row", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/search_artifacts": { body: { total_matched: 0 } },
    });
    goTo("#/search/nothing");
    render(<App />);
    const absent = (await screen.findByText("Widen the query.")).closest(
      ".empty",
    ) as HTMLElement;
    expect(absent.className.split(" ")).toContain("empty-page");
    // The card carries the title line over the sentence, so the absence
    // reads as the designed state rather than as a stranded caption.
    expect(within(absent).getByText("Nothing matched").className).toBe(
      "empty-title",
    );
    // The page preset is a bordered card, and the sentence inside it is
    // smaller and quieter than the body text a result would have carried.
    const style = window.getComputedStyle(absent);
    expect(style.fontSize).toBe("13px");
    expect(style.color).toBe("var(--faint)");
    expect(style.borderRadius).toBe("11px");
    expect(style.padding).toBe("26px");
  });
});

describe("the artifact viewer", () => {
  it("renders the body as a document, the frontmatter as a property table, and the graph edges as links", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact": {
        body: {
          id: "platform/review",
          type: "skill",
          version: "1.2.0",
          content_hash: "sha256:abc",
          manifest_body: "# Review\n\nRun the checklist.\n",
          // The frontmatter field carries the whole ARTIFACT.md document,
          // fences and prose body included, which is what the endpoint
          // returns. A viewer that hands the field straight to the YAML
          // parser reaches the invalid-syntax arm on every real artifact.
          frontmatter: manifestDoc,
          skill_raw: `${manifestDoc}\nAuthored skill body.\n`,
          layer: "platform",
        },
      },
      "/v1/dependents": {
        body: {
          edges: [
            {
              from: "platform/review-strict",
              to: "platform/review",
              kind: "extends",
            },
          ],
        },
      },
    });
    goTo("#/artifact/platform%2Freview");
    render(<App />);
    await screen.findByLabelText("Artifact viewer");
    // The rendered tab is the one the viewer opens on, and the rail carries
    // the frontmatter beside it.
    expect(
      screen.getByTestId("artifact-body").querySelector("h1")?.textContent,
    ).toBe("Review");
    const rail = screen.getByTestId("rail-frontmatter-table");
    expect(rail.textContent).toContain("name");
    expect(rail.textContent).toContain("security");
    expect(screen.queryByText("Invalid syntax")).toBeNull();
    fireEvent.click(screen.getByRole("tab", { name: /Frontmatter/ }));
    const table = screen.getByTestId("frontmatter-table");
    expect(table.textContent).toContain("security");
    // The authored skill file is populated on a skill artifact, so the
    // viewer carries its tab.
    fireEvent.click(screen.getByRole("tab", { name: "Authored source" }));
    expect(screen.getByText(/Authored skill body\./)).toBeTruthy();
    const relation = await screen.findByText("platform/review-strict");
    expect(relation.getAttribute("href")).toBe(
      "#/artifact/platform%2Freview-strict",
    );
  });

  // The header names the artifact. The heading carries the artifact's own
  // name at the page-title role, the badges qualifying it sit beside it on
  // the same line, and the breadcrumb above it leads back through the
  // domains. A heading set at the mono-body role leaves the markdown body's
  // own first heading as the largest text on the page.
  it("names the artifact in a page title with the badges beside it", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact": {
        body: {
          id: "finance/accounts-payable/pay-invoice",
          type: "skill",
          version: "2.3.0",
          content_hash: "sha256:abc",
          manifest_body: "# Pay an invoice\n",
          frontmatter:
            "---\nname: pay-invoice\ndescription: Pay a supplier invoice.\n---\n",
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    goTo("#/artifact/finance%2Faccounts-payable%2Fpay-invoice");
    render(<App />);
    await screen.findByLabelText("Artifact viewer");
    // The markdown body carries a heading of its own, so the assertion is
    // that the first one on the page is the artifact's name.
    const headings = within(
      screen.getByLabelText("Artifact viewer"),
    ).getAllByRole("heading", { level: 1 });
    const heading = headings[0];
    expect(heading.textContent).toBe("pay-invoice");
    const style = window.getComputedStyle(heading);
    expect(style.fontSize).toBe("29px");
    // The badges are siblings of the heading rather than a row below it.
    const title = heading.parentElement;
    expect(within(title as HTMLElement).getByText("SKILL")).toBeTruthy();
    expect(within(title as HTMLElement).getByText("v2.3.0")).toBeTruthy();
    // The type and the version are the two badge weights the header holds
    // apart: the type takes the outlined badge, and the version the filled
    // soft one. Drawn in one treatment they read as a row of identical pills
    // and the reader is given no order to read them in.
    const type = within(title as HTMLElement).getByText("SKILL");
    expect(type.className.split(" ")).toContain("badge");
    expect(type.className.split(" ")).not.toContain("badge-soft");
    const version = within(title as HTMLElement)
      .getByText("v2.3.0")
      .closest(".badge");
    expect(version?.className.split(" ")).toContain("badge-soft");
    // The breadcrumb is the one place the header states the path. A mono
    // line spelling the whole identifier under the badges repeats the trail
    // three lines above it, so the content column states the path once.
    const content = heading.closest(".artifact-content") as HTMLElement;
    expect(
      within(content).queryByText("finance/accounts-payable/pay-invoice"),
    ).toBeNull();
    const trail = screen.getByLabelText("Breadcrumb");
    expect(
      within(trail).getByText("accounts-payable").getAttribute("href"),
    ).toBe("#/domain/finance%2Faccounts-payable");
    // The artifact itself ends the trail, as plain text rather than a link
    // back to the page being read.
    expect(trail.textContent).toBe(
      "catalog/finance/accounts-payable/pay-invoice",
    );
    expect(
      within(trail).queryByRole("link", { name: "pay-invoice" }),
    ).toBeNull();
    expect(within(content).getByText("Pay a supplier invoice.")).toBeTruthy();
    // The response carries no classification, so the badge is absent rather
    // than standing empty.
    expect(within(title as HTMLElement).queryByText(/sensitivity/)).toBeNull();
  });

  // A skill omits `description` from ARTIFACT.md and declares it in the
  // authored SKILL.md instead (§4.3.4), which load_artifact returns under
  // skill_raw. A header that reads the manifest frontmatter alone states no
  // description for any skill, while the listing that linked to the page
  // states one.
  //
  // Spec: §4.3.4
  it("states a skill's description from the authored file where the manifest omits it", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact": {
        body: {
          id: "finance/accounts-payable/pay-invoice",
          type: "skill",
          version: "2.3.0",
          content_hash: "sha256:abc",
          manifest_body: "# Pay an invoice\n",
          frontmatter:
            "---\ntype: skill\nversion: 2.3.0\nsensitivity: low\n---\n",
          skill_raw:
            "---\nname: pay-invoice\ndescription: Pay a supplier invoice.\n---\n\n# Pay an invoice\n",
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    goTo("#/artifact/finance%2Faccounts-payable%2Fpay-invoice");
    render(<App />);
    await screen.findByLabelText("Artifact viewer");
    const heading = within(
      screen.getByLabelText("Artifact viewer"),
    ).getAllByRole("heading", { level: 1 })[0];
    const content = heading.closest(".artifact-content") as HTMLElement;
    const lead = within(content).getByText("Pay a supplier invoice.");
    expect(lead.classList.contains("lead")).toBe(true);
    // The description stands between the title row and the version picker,
    // which is where the header states it for every other type.
    const title = heading.parentElement as HTMLElement;
    expect(title.nextElementSibling).toBe(lead);
  });

  // A classification value states a level and never the axis it measures, so
  // "internal" beside the type and the version reads as one more unnamed
  // property of the artifact. The badge names the axis and carries the weight
  // of the badges it sits with, because the classification is informational
  // rather than an alert.
  //
  // Spec: §4.3
  it("names the axis the sensitivity badge measures at the weight of the badges beside it", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact": {
        body: {
          id: "finance/accounts-payable/pay-invoice",
          type: "skill",
          version: "2.3.0",
          sensitivity: "internal",
          content_hash: "sha256:abc",
          manifest_body: "# Pay an invoice\n",
          frontmatter: "---\nname: pay-invoice\nsensitivity: internal\n---\n",
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    goTo("#/artifact/finance%2Faccounts-payable%2Fpay-invoice");
    render(<App />);
    await screen.findByLabelText("Artifact viewer");
    const heading = within(
      screen.getByLabelText("Artifact viewer"),
    ).getAllByRole("heading", { level: 1 })[0];
    const title = heading.parentElement as HTMLElement;
    const classification = within(title).getByText("sensitivity: internal");
    expect(within(title).getByText("SKILL").className).toBe(
      classification.className,
    );
  });

  // Spec: §13.10 — the viewer links to extending or dependent artifacts.
  // Every edge the dependents endpoint serves ends at the artifact on the
  // page, so the label reads in the passive direction. Labelling the row
  // with the raw edge kind states the relationship backwards.
  it("labels each graph edge as inbound rather than inverting the relationship", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact": {
        body: {
          id: "finance/ap/pay-invoice",
          type: "skill",
          version: "1.0.0",
          content_hash: "sha256:abc",
          manifest_body: "# Pay invoice\n",
          frontmatter: manifestDoc,
        },
      },
      "/v1/dependents": {
        body: {
          edges: [
            {
              from: "finance/ap/reconcile-ledger",
              to: "finance/ap/pay-invoice",
              kind: "extends",
            },
            {
              from: "finance/ap/close-books",
              to: "finance/ap/pay-invoice",
              kind: "delegates_to",
            },
          ],
        },
      },
    });
    goTo("#/artifact/finance%2Fap%2Fpay-invoice");
    render(<App />);
    const relations = await screen.findByLabelText("Relations");
    const groups = relations.querySelectorAll(".rail-group");
    // The outbound group leads, then one group per inbound relation, each
    // labelled in the passive direction.
    expect(
      [...groups].map((group) => group.querySelector("p")?.textContent),
    ).toEqual(["extends", "extended by", "delegated to by"]);
    expect(groups[1].querySelector("li")?.textContent).toBe(
      "finance/ap/reconcile-ledger",
    );
    expect(groups[2].querySelector("li")?.textContent).toBe(
      "finance/ap/close-books",
    );
  });

  // Spec: §13.10 — the viewer links to extending or dependent artifacts. The
  // dependents endpoint serves the reverse index alone, so the artifact's own
  // outbound extends reaches the rail from the manifest. A merged response
  // strips the parent from the frontmatter it re-serializes and carries the
  // pre-merge document beside it, which is where the reference survives. The
  // catalog read lists the parent, so this caller may be told it exists.
  it("splits the rail's relations into the artifact's own extends and the artifacts extending it", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/catalog": { body: { ids: ["finance/ap/pay-invoice"] } },
      "/v1/load_artifact": {
        body: {
          id: "finance/ap/three-way-match",
          type: "skill",
          version: "1.0.0",
          content_hash: "sha256:abc",
          manifest_body: "# Three-way match\n",
          frontmatter: "---\ntype: skill\nversion: 1.0.0\n---\n",
          manifest_merged: true,
          raw_frontmatter:
            "---\ntype: skill\nversion: 1.0.0\nextends: finance/ap/pay-invoice@1.2.0\n---\n",
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    goTo("#/artifact/finance%2Fap%2Fthree-way-match");
    render(<App />);
    const relations = await screen.findByLabelText("Relations");
    // The direction the artifact declares is a group of its own, and the
    // chip keeps the authored reference while the link drops the version
    // constraint, which can name a range rather than a stored version.
    const declared = within(relations).getByText(
      "finance/ap/pay-invoice@1.2.0",
    );
    expect(declared.getAttribute("href")).toBe(
      "#/artifact/finance%2Fap%2Fpay-invoice",
    );
    // The other direction has no members, and says so on its own group
    // rather than leaving the reader to read the absence off the first.
    expect(
      within(relations).getByText("Nothing extends this artifact."),
    ).toBeTruthy();
    expect(
      within(relations).queryByText("This artifact extends nothing."),
    ).toBeNull();
  });

  // Spec: §4.6 — when the caller cannot see the layer that contributes a
  // parent, the registry merges it server-side and "the parent's existence
  // and ID are not surfaced to the requester". The pre-merge document travels
  // beside the merged manifest for the content-hash check, so the authored
  // reference is still on the response, and republishing it as a chip would
  // tell the reader an artifact they cannot open exists. The catalog read
  // omits the parent, so the group reads as it does for an artifact that
  // declares none.
  it("withholds the extends chip when the catalog does not list the declared parent", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/catalog": { body: { ids: [] } },
      "/v1/load_artifact": {
        body: {
          id: "pub/child",
          type: "skill",
          version: "0.1.0",
          content_hash: "sha256:abc",
          manifest_body: "# Child\n",
          frontmatter: "---\ntype: skill\nversion: 0.1.0\n---\n",
          manifest_merged: true,
          raw_frontmatter:
            "---\ntype: skill\nversion: 0.1.0\nextends: hidden/parent\n---\n",
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    goTo("#/artifact/pub%2Fchild");
    render(<App />);
    const relations = await screen.findByLabelText("Relations");
    await within(relations).findByText("This artifact extends nothing.");
    // Neither the ID nor a link to it reaches the page.
    expect(screen.queryByText("hidden/parent")).toBeNull();
    expect(
      relations.querySelector('a[href="#/artifact/hidden%2Fparent"]'),
    ).toBeNull();
    // The read that settled it was taken over the parent's own domain.
    expect(
      requests.some((req) => req.url.includes("/v1/catalog?scope=hidden")),
    ).toBe(true);
  });

  // Spec: §4.6 — a concealment rule that cannot be evaluated denies. When the
  // catalog read fails, the rail cannot establish that the caller may see the
  // declared parent, so it withholds the chip rather than falling back to the
  // reference the pre-merge document carries.
  it("withholds the extends chip when the catalog read fails", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/catalog": { rejects: true },
      "/v1/load_artifact": {
        body: {
          id: "pub/child",
          type: "skill",
          version: "0.1.0",
          content_hash: "sha256:abc",
          manifest_body: "# Child\n",
          frontmatter: "---\ntype: skill\nversion: 0.1.0\n---\n",
          manifest_merged: true,
          raw_frontmatter:
            "---\ntype: skill\nversion: 0.1.0\nextends: hidden/parent\n---\n",
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    goTo("#/artifact/pub%2Fchild");
    render(<App />);
    const relations = await screen.findByLabelText("Relations");
    await within(relations).findByText("This artifact extends nothing.");
    expect(screen.queryByText("hidden/parent")).toBeNull();
  });

  // Spec: §13.10 — the viewer links to extending or dependent artifacts. The
  // chips of both directions are the same bordered row, so the leading dot is
  // what separates the edge the artifact declares from the edges that end at
  // it once the group label is out of the reader's eye.
  it("tones each relation chip's leading dot by the direction of its edge", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/catalog": { body: { ids: ["finance/ap/pay-invoice"] } },
      "/v1/load_artifact": {
        body: {
          id: "finance/ap/three-way-match",
          type: "skill",
          version: "1.0.0",
          content_hash: "sha256:abc",
          manifest_body: "# Three-way match\n",
          frontmatter: "---\ntype: skill\nversion: 1.0.0\n---\n",
          manifest_merged: true,
          raw_frontmatter:
            "---\ntype: skill\nversion: 1.0.0\nextends: finance/ap/pay-invoice\n---\n",
        },
      },
      "/v1/dependents": {
        body: {
          edges: [
            {
              from: "finance/ap/close-books",
              to: "finance/ap/three-way-match",
              kind: "extends",
            },
          ],
        },
      },
    });
    goTo("#/artifact/finance%2Fap%2Fthree-way-match");
    render(<App />);
    const relations = await screen.findByLabelText("Relations");
    await screen.findByText("finance/ap/close-books");
    // Every chip carries a dot, and only the outbound group's is accented.
    const chips = [...relations.querySelectorAll(".relation-chip")];
    expect(
      chips.map(
        (chip) => chip.querySelector(".relation-dot")?.className ?? "none",
      ),
    ).toEqual(["relation-dot outbound", "relation-dot inbound"]);
    // The dot stands before the id rather than after it.
    expect(chips[0].firstElementChild?.className).toBe("relation-dot outbound");
  });

  // The rail is a fixed-width column, and provenance is a set of labelled
  // values rather than prose: each one stands in a borderless list under its
  // own section label, so the bordered frontmatter and relations sections
  // beneath it stay the objects the rail is built around. The content hash is
  // 71 characters against a rail far narrower than that, so the row
  // abbreviates it and keeps the whole value on the row's title, where the
  // reader can still recover it.
  it("renders provenance as a borderless labelled list with the content hash abbreviated", async () => {
    const contentHash =
      "sha256:ab7469fdce70f0beb8c3b4e696da5e0080f95f75a9d8b3c2e1f0a94d6c7b8e5f";
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact": {
        body: {
          id: "finance/ap/pay-invoice",
          type: "skill",
          version: "1.0.0",
          content_hash: contentHash,
          layer: "acme-platform",
          manifest_body: "# Pay invoice\n",
          frontmatter: manifestDoc,
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    goTo("#/artifact/finance%2Fap%2Fpay-invoice");
    render(<App />);
    const provenance = await screen.findByLabelText("Provenance");
    const facts = within(provenance).getByTestId("rail-provenance");
    const rows = [...facts.querySelectorAll(".rail-fact")].map((row) => [
      row.querySelector("dt")?.textContent,
      row.querySelector("dd")?.textContent,
    ]);
    // The layer list answered nothing here, so the two rows it feeds state
    // that rather than disappearing out of the block.
    expect(rows).toEqual([
      ["layer", "acme-platform"],
      ["visibility", "unreported"],
      ["ingested", "unreported"],
      ["hash", "sha256:ab74…8e5f"],
    ]);
    // The section carries no table and no bordered container of its own, so
    // it does not read as a second copy of the frontmatter table below it.
    expect(provenance.querySelector("table")).toBeNull();
    expect(provenance.querySelector(".data-table")).toBeNull();
    // The abbreviation is a display, so the whole value is still on the row.
    expect(within(provenance).queryByText(contentHash)).toBeNull();
    expect(facts.querySelectorAll("dd")[3].getAttribute("title")).toBe(
      contentHash,
    );
  });

  // The rail is where a reader learns who else can see the artifact and which
  // revision of the source it came from, which §13.10 puts beside the
  // document rather than behind the layer panel. Neither fact is on the
  // load_artifact response, so the viewer reads the layer list and states the
  // layer's §4.6 grants and the run it last ingested from the record there.
  it("states the layer's visibility and its last ingest in the provenance rail", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/layers": {
        body: {
          layers: [
            {
              ID: "acme-platform",
              SourceType: "git",
              Repo: "git@github.com:acme/platform.git",
              Ref: "main",
              Order: 1,
              Organization: true,
              Groups: ["platform"],
              last_ingested_at: new Date(Date.now() - 7200000).toISOString(),
              LastIngestedRef: "4f2a1c9de4471b1e8f0c2a5d6e7b8c9a0d1e2f34",
            },
          ],
        },
      },
      "/v1/load_artifact": {
        body: {
          id: "finance/ap/pay-invoice",
          type: "skill",
          version: "1.0.0",
          content_hash: "sha256:ab74",
          layer: "acme-platform",
          manifest_body: "# Pay invoice\n",
          frontmatter: manifestDoc,
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    goTo("#/artifact/finance%2Fap%2Fpay-invoice");
    render(<App />);
    const provenance = await screen.findByLabelText("Provenance");
    await waitFor(() => {
      const facts = within(provenance).getByTestId("rail-provenance");
      const rows = [...facts.querySelectorAll(".rail-fact")].map((row) => [
        row.querySelector("dt")?.textContent,
        row.querySelector("dd")?.textContent,
      ]);
      expect(rows).toEqual([
        ["layer", "acme-platform"],
        // Every granted axis, in the union order §4.6 defines.
        ["visibility", "organization, group: platform"],
        // The age the reader scans, with the branch and the short commit the
        // run landed on beside it.
        ["ingested", "2h ago · main@4f2a1c9"],
        ["hash", "sha256:ab74"],
      ]);
    });
  });

  // The rail's layer read is refused by the same outage that refuses the
  // document, and the reader recovers from it with the page's own Retry. That
  // control re-issues both reads, so one press restores the provenance rows
  // along with the document. Spec: §13.10.
  it("recovers the provenance rows from the viewer's own retry", async () => {
    const stubs: Record<string, Stub> = {
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      // The registry has gone away, so neither read reaches it.
      "/v1/layers": { rejects: true },
      "/v1/load_artifact": { rejects: true },
      "/v1/dependents": { body: { edges: [] } },
    };
    stubRegistry(stubs);
    goTo("#/artifact/finance%2Fap%2Fpay-invoice");
    render(<App />);
    await screen.findByTestId("artifact-failed");

    stubs["/v1/layers"] = { body: { layers: [ingestedLayer()] } };
    stubs["/v1/load_artifact"] = { body: payInvoice() };
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));

    const provenance = await screen.findByLabelText("Provenance");
    await waitFor(() => {
      expect(railFacts(provenance)).toEqual([
        ["layer", "acme-platform"],
        ["visibility", "organization"],
        ["ingested", "2h ago · main@4f2a1c9"],
        ["hash", "sha256:ab74"],
      ]);
    });
  });

  // The viewer survives the route change from one artifact to the next, so a
  // layer read that was refused while the registry was away would otherwise
  // stay refused for the rest of the session. The route change re-issues it.
  // Spec: §13.10.
  it("re-issues the refused layer read when the route names another artifact", async () => {
    const stubs: Record<string, Stub> = {
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/layers": { rejects: true },
      "/v1/load_artifact": { body: payInvoice() },
      "/v1/dependents": { body: { edges: [] } },
    };
    stubRegistry(stubs);
    goTo("#/artifact/finance%2Fap%2Fpay-invoice");
    render(<App />);
    const first = await screen.findByLabelText("Provenance");
    await waitFor(() => {
      expect(railFacts(first)).toEqual([
        ["layer", "acme-platform"],
        ["visibility", "unreported"],
        ["ingested", "unreported"],
        ["hash", "sha256:ab74"],
      ]);
    });

    stubs["/v1/layers"] = { body: { layers: [ingestedLayer()] } };
    stubs["/v1/load_artifact"] = {
      body: { ...payInvoice(), id: "eng/deploy", type: "context" },
    };
    goTo("#/artifact/eng%2Fdeploy");

    await waitFor(() => {
      expect(railFacts(screen.getByLabelText("Provenance"))).toEqual([
        ["layer", "acme-platform"],
        ["visibility", "organization"],
        ["ingested", "2h ago · main@4f2a1c9"],
        ["hash", "sha256:ab74"],
      ]);
    });
  });

  // The viewer is two columns with a tab set over the content one. The
  // resource tab carries the count of what the artifact bundles, and a tab
  // whose artifact carries nothing for it is not drawn at all rather than
  // opening on an empty panel.
  it("draws the tab set with the resource count and drops a tab the artifact carries nothing for", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact": {
        body: {
          id: "platform/review",
          type: "context",
          version: "1.0.0",
          content_hash: "sha256:abc",
          manifest_body: "# Review\n",
          frontmatter: manifestDoc,
          resources: { "checklist.md": "body", "rubric.md": "body" },
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    goTo("#/artifact/platform%2Freview");
    render(<App />);
    await screen.findByLabelText("Artifact viewer");
    expect(
      screen.getByRole("tab", { name: /Resources/ }).textContent,
    ).toContain("2");
    expect(screen.queryByRole("tab", { name: "Authored source" })).toBeNull();
    fireEvent.click(screen.getByRole("tab", { name: /Resources/ }));
    expect(screen.getByLabelText("Resources").textContent).toContain(
      "checklist.md",
    );
  });

  // The authored source tab is a file view rather than a bare value beside a
  // control: a header states the file and its extent, a gutter numbers the
  // lines so a reader can quote one, and the file is takeable whole by Copy
  // or by Download.
  it("shows the authored file under a header with a numbered gutter and a download", async () => {
    const skillRaw = "---\nname: review\n---\n\nBody line.\n";
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact": {
        body: {
          id: "platform/review",
          type: "skill",
          version: "1.0.0",
          content_hash: "sha256:abc",
          manifest_body: "# Review\n",
          frontmatter: manifestDoc,
          skill_raw: skillRaw,
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    goTo("#/artifact/platform%2Freview");
    render(<App />);
    await screen.findByLabelText("Artifact viewer");
    fireEvent.click(screen.getByRole("tab", { name: "Authored source" }));
    const pane = screen.getByRole("tabpanel");
    // The header names the file and states its extent. The trailing newline
    // is a byte rather than a line, so the count is the five authored lines.
    expect(pane.querySelector(".source-head")?.textContent).toBe(
      "SKILL.md5 lines · 33 B",
    );
    expect(
      [...pane.querySelectorAll(".source-gutter div")].map(
        (line) => line.textContent,
      ),
    ).toEqual(["1", "2", "3", "4", "5"]);
    expect(pane.querySelector(".source-code")?.textContent).toBe(
      skillRaw.trimEnd(),
    );
    // Download takes the file itself, trailing newline included.
    const click = vi
      .spyOn(HTMLAnchorElement.prototype, "click")
      .mockImplementation(function (this: HTMLAnchorElement) {
        expect(this.getAttribute("download")).toBe("SKILL.md");
        expect(this.getAttribute("href")).toBe(
          `data:text/plain;charset=utf-8,${encodeURIComponent(skillRaw)}`,
        );
      });
    fireEvent.click(
      within(pane).getByRole("button", { name: "Download SKILL.md" }),
    );
    expect(click).toHaveBeenCalledTimes(1);
    click.mockRestore();
    expect(within(pane).getByRole("button", { name: "Copy" })).toBeTruthy();
  });

  // Every bundled file is retrievable from its own row: nothing is
  // previewed, so the row's action is the only path to the file. One binary
  // file puts the whole inline set into base64, and that row's action carries
  // the decoded bytes while its size column states the file's own byte count
  // rather than the length of the encoding. The column carries the same unit
  // the total above the table and the detail card under it use, so one file's
  // size does not read two ways on one screen.
  it("gives every resource row a format, a byte size, and a download action", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact": {
        body: {
          id: "platform/review",
          type: "context",
          version: "1.0.0",
          content_hash: "sha256:abc",
          manifest_body: "# Review\n",
          frontmatter: "",
          resources: { "logo.png": "AAECAw==" },
          resources_base64: true,
          large_resources: {
            "corpus.bin": {
              presigned_url: "https://objects.acme.com/corpus",
              content_hash: "sha256:def",
              size: 168000000,
              content_type: "application/octet-stream",
            },
          },
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    goTo("#/artifact/platform%2Freview");
    render(<App />);
    await screen.findByLabelText("Artifact viewer");
    fireEvent.click(screen.getByRole("tab", { name: /Resources/ }));
    const rows = within(screen.getByLabelText("Resources"))
      .getAllByRole("row")
      .slice(1);
    const inline = within(rows[0])
      .getAllByRole("cell")
      .map((cell) => cell.textContent);
    expect(inline.slice(0, 4)).toEqual([
      "logo.png",
      "png",
      "4 B",
      "inline, base64",
    ]);
    const download = within(rows[0]).getByRole("link", {
      name: "Download ↓",
    });
    expect(download.getAttribute("href")).toBe(
      "data:application/octet-stream;base64,AAECAw==",
    );
    expect(download.getAttribute("download")).toBe("logo.png");
    const fetched = within(rows[1])
      .getAllByRole("cell")
      .map((cell) => cell.textContent);
    expect(fetched.slice(0, 4)).toEqual([
      "corpus.bin",
      "application/octet-stream",
      "160.2 MB",
      "fetched on demand",
    ]);
    expect(
      within(rows[1])
        .getByRole("link", { name: "Download ↓" })
        .getAttribute("href"),
    ).toBe("https://objects.acme.com/corpus");
  });

  // The frontmatter panel offers both readings of the block: the property
  // table and the YAML the author wrote.
  it("reads a well-formed frontmatter block as a table or as raw YAML", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact": {
        body: {
          id: "platform/review",
          type: "context",
          version: "1.0.0",
          content_hash: "sha256:abc",
          manifest_body: "# Review\n",
          frontmatter: manifestDoc,
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    goTo("#/artifact/platform%2Freview");
    render(<App />);
    await screen.findByLabelText("Artifact viewer");
    fireEvent.click(screen.getByRole("tab", { name: /Frontmatter/ }));
    expect(screen.getByTestId("frontmatter-table")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Raw YAML" }));
    expect(screen.queryByTestId("frontmatter-table")).toBeNull();
    expect(screen.getByTestId("raw-frontmatter").textContent).toContain(
      "name: review",
    );
    // Nothing marks a line on a block that parsed.
    expect(screen.queryByTestId("offending-line")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Table" }));
    expect(screen.getByTestId("frontmatter-table")).toBeTruthy();
  });

  // The raw pane scrolls sideways, so it takes the same treatment as the
  // rendered body's tables and code fences: a keyboard-only reader reaches it
  // in the tab order and is told what it holds.
  // Spec: §13.10
  it("names the raw frontmatter pane as a focusable region", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact": {
        body: {
          id: "platform/review",
          type: "context",
          version: "1.0.0",
          content_hash: "sha256:abc",
          manifest_body: "# Review\n",
          frontmatter: manifestDoc,
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    goTo("#/artifact/platform%2Freview");
    render(<App />);
    await screen.findByLabelText("Artifact viewer");
    fireEvent.click(screen.getByRole("tab", { name: /Frontmatter/ }));
    fireEvent.click(screen.getByRole("button", { name: "Raw YAML" }));
    const pane = screen.getByRole("region", {
      name: "Frontmatter, as authored",
    });
    expect(pane.getAttribute("data-testid")).toBe("raw-frontmatter");
    expect(pane.getAttribute("tabindex")).toBe("0");
  });

  // The two code panes on this surface take the same file view. A bare block
  // says neither what it holds nor how far it runs, and a value that runs
  // past the right edge is only reachable by scrolling a pane nothing marks
  // as scrollable, so the raw block carries the header, the numbered gutter,
  // and the explicit Copy the authored source pane carries (§13.10).
  it("gives the raw frontmatter block a header, a gutter, and a Copy control", async () => {
    const written: string[] = [];
    const original = Object.getOwnPropertyDescriptor(navigator, "clipboard");
    Object.defineProperty(navigator, "clipboard", {
      value: {
        writeText: (text: string) => {
          written.push(text);
          return Promise.resolve();
        },
      },
      configurable: true,
    });
    try {
      stubRegistry({
        "/v1/ui/session": { body: posture({ public_mode: true }) },
        "/v1/load_artifact": {
          body: {
            id: "platform/review",
            type: "context",
            version: "1.0.0",
            content_hash: "sha256:abc",
            manifest_body: "# Review\n",
            frontmatter: manifestDoc,
          },
        },
        "/v1/dependents": { body: { edges: [] } },
      });
      goTo("#/artifact/platform%2Freview");
      render(<App />);
      await screen.findByLabelText("Artifact viewer");
      fireEvent.click(screen.getByRole("tab", { name: /Frontmatter/ }));
      fireEvent.click(screen.getByRole("button", { name: "Raw YAML" }));
      const block = screen
        .getByTestId("raw-frontmatter")
        .closest(".source-block") as HTMLElement;
      // The header names the block and states its extent.
      expect(
        (block.querySelector(".source-head") as HTMLElement).textContent,
      ).toBe("raw frontmatter3 lines");
      // The gutter numbers every line of the block and is skipped by a
      // screen reader, which would otherwise interleave the numbers.
      const gutter = block.querySelector(".source-gutter") as HTMLElement;
      expect(gutter.getAttribute("aria-hidden")).toBe("true");
      expect([...gutter.children].map((line) => line.textContent)).toEqual([
        "1",
        "2",
        "3",
      ]);
      const copy = screen.getByRole("button", { name: "Copy raw block" });
      fireEvent.click(copy);
      await waitFor(() => {
        expect(written).toEqual(["name: review\ntags:\n  - security"]);
      });
    } finally {
      if (original !== undefined) {
        Object.defineProperty(navigator, "clipboard", original);
      } else {
        delete (navigator as unknown as { clipboard?: unknown }).clipboard;
      }
    }
  });

  // The gutter carries the parse failure too, so the number of the line the
  // parser named is marked alongside the line itself.
  // Spec: §13.10
  it("marks the offending line in the raw block's gutter", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact": {
        body: {
          id: "platform/broken",
          type: "context",
          version: "1.0.0",
          content_hash: "sha256:abc",
          manifest_body: "# Broken\n",
          frontmatter: "---\nname: broken\n  bad: indent\n---\n",
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    goTo("#/artifact/platform%2Fbroken");
    render(<App />);
    await screen.findByLabelText("Artifact viewer");
    fireEvent.click(screen.getByRole("tab", { name: /Frontmatter/ }));
    await screen.findByText("Invalid syntax");
    const marked = screen.getByTestId("offending-line");
    const block = marked.closest(".source-block") as HTMLElement;
    const gutter = block.querySelector(".source-gutter") as HTMLElement;
    const tinted = [...gutter.children].filter((line) =>
      line.classList.contains("source-gutter-offending"),
    );
    expect(tinted.length).toBe(1);
    // The tinted number is the position of the marked line.
    const lines = [...(marked.parentElement as HTMLElement).children];
    expect(tinted[0].textContent).toBe(String(lines.indexOf(marked) + 1));
  });

  // The version affordance is disclosed from the badge in the header. Most
  // artifacts carry one published version, and standing an entry field and
  // its button between the description and the tabs put a form on the page
  // for a reader who has nothing to pick.
  // Spec: §13.10
  it("states the version in the header and discloses the field from it", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact": {
        body: {
          id: "platform/review",
          type: "context",
          version: "2.3.0",
          content_hash: "sha256:abc",
          manifest_body: "# Review\n",
          frontmatter: "",
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    goTo("#/artifact/platform%2Freview");
    render(<App />);
    await screen.findByLabelText("Artifact viewer");
    // Nothing stands open: no field, no View button, no VERSION label.
    expect(screen.queryByLabelText("Version")).toBeNull();
    expect(screen.queryByRole("button", { name: "View" })).toBeNull();
    // The badge that states the version is what opens the field, and it sits
    // in the title row beside the type badge.
    const trigger = screen.getByRole("button", { name: /^Version v2\.3\.0/ });
    expect(trigger.textContent).toContain("v2.3.0");
    expect(trigger.getAttribute("aria-expanded")).toBe("false");
    expect(trigger.closest(".page-title")).not.toBeNull();
    fireEvent.click(trigger);
    expect(screen.getByLabelText("Version")).toBeTruthy();
    expect(screen.getByRole("button", { name: "View" })).toBeTruthy();
    expect(trigger.getAttribute("aria-expanded")).toBe("true");
    // Escape abandons the disclosure and leaves the header as it was.
    fireEvent.keyDown(screen.getByLabelText("Version"), { key: "Escape" });
    expect(screen.queryByLabelText("Version")).toBeNull();
  });

  // load_artifact defaults to the latest version and takes any other, so a
  // reader who picks one is told which version they are reading and is given
  // the way back to the latest.
  it("reads the version the picker names and marks it as an older one", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact": {
        body: {
          id: "platform/review",
          type: "context",
          version: "2.3.0",
          content_hash: "sha256:abc",
          manifest_body: "# Review\n",
          frontmatter: "",
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    goTo("#/artifact/platform%2Freview");
    render(<App />);
    await screen.findByLabelText("Artifact viewer");
    expect(screen.queryByTestId("older-version")).toBeNull();
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact": {
        body: {
          id: "platform/review",
          type: "context",
          version: "1.0.0",
          content_hash: "sha256:old",
          manifest_body: "# Review\n",
          frontmatter: "",
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    pinVersion("1.0.0");
    const notice = await screen.findByTestId("older-version");
    // The notice names both versions the way the header's badge names one, so
    // the row and the badge above it read as the same fact.
    expect(notice.textContent).toContain("Viewing v1.0.0");
    expect(notice.textContent).toContain("not the latest");
    expect(screen.getByRole("button", { name: "Go to v2.3.0" })).toBeTruthy();
    expect(requests.some((r) => r.url.includes("version=1.0.0"))).toBe(true);
  });

  // Reading an older version is a move between two drawn states of the same
  // artifact, so it goes through the address the way every other move in the
  // shell does. Without it the address states the latest version while the
  // page states an older one: the reader who copies the link hands out the
  // latest version, a reload drops the pin, and a back step leaves the
  // artifact instead of returning to the version they came from.
  //
  // Spec: §13.10
  it("addresses the version the picker names and returns to latest from the notice", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact?id=platform%2Freview": {
        body: {
          id: "platform/review",
          type: "context",
          version: "2.3.0",
          content_hash: "sha256:abc",
          manifest_body: "# Review\n",
          frontmatter: "",
        },
      },
      "/v1/load_artifact?id=platform%2Freview&version=1.0.0": {
        body: {
          id: "platform/review",
          type: "context",
          version: "1.0.0",
          content_hash: "sha256:old",
          manifest_body: "# Review\n",
          frontmatter: "",
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    goTo(artifactHref("platform/review"));
    render(<App />);
    await screen.findByLabelText("Artifact viewer");

    pinVersion("1.0.0");
    await screen.findByTestId("older-version");
    expect(window.location.hash).toBe(artifactHref("platform/review", "1.0.0"));
    // The pinned address is its own history entry, so the reader's back step
    // returns to the latest version rather than leaving the artifact.
    expect(window.location.hash).not.toBe(artifactHref("platform/review"));

    fireEvent.click(screen.getByRole("button", { name: "Go to v2.3.0" }));
    await waitFor(() => {
      expect(screen.queryByTestId("older-version")).toBeNull();
    });
    expect(window.location.hash).toBe(artifactHref("platform/review"));
  });

  // A pinned address is reachable from a pasted link, a bookmark, and a step
  // back, so the viewer reads the version the address names on arrival. The
  // latest version is read beside it, because the reader who arrived this way
  // has never been told which version is current.
  //
  // Spec: §13.10
  it("opens a pinned address at that version and marks it as an older one", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact?id=platform%2Freview": {
        body: {
          id: "platform/review",
          type: "context",
          version: "2.3.0",
          content_hash: "sha256:abc",
          manifest_body: "# Latest review\n",
          frontmatter: "",
        },
      },
      "/v1/load_artifact?id=platform%2Freview&version=1.0.0": {
        body: {
          id: "platform/review",
          type: "context",
          version: "1.0.0",
          content_hash: "sha256:old",
          manifest_body: "# Older review\n",
          frontmatter: "",
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    goTo(artifactHref("platform/review", "1.0.0"));
    render(<App />);
    await screen.findByLabelText("Artifact viewer");

    const notice = await screen.findByTestId("older-version");
    expect(notice.textContent).toContain("Viewing v1.0.0");
    expect(screen.getByRole("button", { name: "Go to v2.3.0" })).toBeTruthy();
    expect(screen.getByRole("button", { name: /^Version v1.0.0/ })).toBeTruthy();
    expect(
      screen.getByLabelText("Artifact viewer").textContent,
    ).toContain("Older review");
  });

  // The registry keeps serving a deprecated artifact and reports the lifecycle
  // state beside the bytes, so the viewer marks the artifact as retired and
  // opens the upgrade target the response names.
  // Spec: §4.7.4
  it("marks a deprecated artifact and links the artifact that replaces it", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact": {
        body: {
          id: "eng/old-deploy",
          type: "context",
          version: "0.1.0",
          content_hash: "sha256:abc",
          manifest_body: "# Superseded\n",
          frontmatter: "deprecated: true\nreplaced_by: eng/deploy\n",
          deprecated: true,
          replaced_by: "eng/deploy",
          deprecation_warning: "artifact is deprecated; replaced_by eng/deploy",
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    goTo("#/artifact/eng%2Fold-deploy");
    render(<App />);
    await screen.findByLabelText("Artifact viewer");
    expect(screen.getByText("DEPRECATED")).toBeTruthy();
    const notice = screen.getByTestId("deprecated-notice");
    const link = within(notice).getByRole("link", { name: "eng/deploy" });
    expect(link.getAttribute("href")).toBe("#/artifact/eng%2Fdeploy");
  });

  // A deprecated artifact whose manifest names no upgrade target still carries
  // the warning, because the state the reader has to act on is the deprecation.
  // Spec: §4.7.4
  it("marks a deprecated artifact that names no replacement", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact": {
        body: {
          id: "eng/old-deploy",
          type: "context",
          version: "0.1.0",
          content_hash: "sha256:abc",
          manifest_body: "# Superseded\n",
          frontmatter: "deprecated: true\n",
          deprecated: true,
          deprecation_warning: "artifact is deprecated",
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    goTo("#/artifact/eng%2Fold-deploy");
    render(<App />);
    await screen.findByLabelText("Artifact viewer");
    const notice = screen.getByTestId("deprecated-notice");
    expect(notice.textContent).toContain("names no replacement");
    expect(within(notice).queryByRole("link")).toBeNull();
  });

  // A live artifact carries neither the badge nor the notice, so the marker
  // means what it says.
  // Spec: §4.7.4
  it("leaves a live artifact unmarked", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact": {
        body: {
          id: "eng/deploy",
          type: "context",
          version: "0.1.0",
          content_hash: "sha256:abc",
          manifest_body: "# Deploy\n",
          frontmatter: "",
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    goTo("#/artifact/eng%2Fdeploy");
    render(<App />);
    await screen.findByLabelText("Artifact viewer");
    expect(screen.queryByTestId("deprecated-notice")).toBeNull();
    expect(screen.queryByText("DEPRECATED")).toBeNull();
  });

  // The picker is a single entry field beside its own button, and Enter in
  // such a field commits it. A reader who types a version and presses return
  // gets the same read the button performs.
  it("reads the version the picker names when Enter commits the field", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact": {
        body: {
          id: "platform/review",
          type: "context",
          version: "2.3.0",
          content_hash: "sha256:abc",
          manifest_body: "# Review\n",
          frontmatter: "",
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    goTo("#/artifact/platform%2Freview");
    render(<App />);
    await screen.findByLabelText("Artifact viewer");
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact": {
        body: {
          id: "platform/review",
          type: "context",
          version: "1.0.0",
          content_hash: "sha256:old",
          manifest_body: "# Review\n",
          frontmatter: "",
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    openVersionPicker();
    fireEvent.change(screen.getByLabelText("Version"), {
      target: { value: "1.0.0" },
    });
    fireEvent.keyDown(screen.getByLabelText("Version"), { key: "Enter" });
    const notice = await screen.findByTestId("older-version");
    expect(notice.textContent).toContain("1.0.0");
    expect(requests.some((r) => r.url.includes("version=1.0.0"))).toBe(true);
  });

  // A version the registry cannot resolve is refused, and the picker that
  // asked for it lives in the viewer's own header. The refusal is presented
  // beside that control and the page it stands on is kept, because the route
  // still names this artifact and a page replaced by a full-width error
  // offers the reader nothing to recover with.
  it("keeps the viewer standing when the registry refuses the version the picker names", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact": {
        body: {
          id: "platform/review",
          type: "context",
          version: "2.3.0",
          content_hash: "sha256:abc",
          manifest_body: "# Review\n",
          frontmatter: "",
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    goTo("#/artifact/platform%2Freview");
    render(<App />);
    await screen.findByLabelText("Artifact viewer");
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact": {
        status: 404,
        body: {
          code: "registry.not_found",
          message: "version: invalid pin: no candidate matches",
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    pinVersion("9.9.9");
    const refusal = await screen.findByTestId("version-refused");
    expect(refusal.textContent).toContain("invalid pin");
    // The surface the picker sits on is still drawn, and so is the picker.
    expect(screen.getByLabelText("Artifact viewer")).toBeTruthy();
    expect(screen.getByRole("button", { name: /^Version / })).toBeTruthy();
    expect(screen.getByRole("heading", { name: "review" })).toBeTruthy();
    expect(refusal.textContent).toContain("2.3.0");
    // The recovery control returns the reader to the version the page held.
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact": {
        body: {
          id: "platform/review",
          type: "context",
          version: "2.3.0",
          content_hash: "sha256:abc",
          manifest_body: "# Review\n",
          frontmatter: "",
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    fireEvent.click(screen.getByRole("button", { name: "Show latest" }));
    await waitFor(() => {
      expect(screen.queryByTestId("version-refused")).toBeNull();
    });
    openVersionPicker();
    expect((screen.getByLabelText("Version") as HTMLInputElement).value).toBe(
      "",
    );
  });

  // The badge toggles aria-expanded, which states that something opened
  // without stating what or where it stands, and the field it discloses is
  // several elements away in the document. The badge therefore points at the
  // popover it owns and names its kind, the same wiring the topbar triggers
  // carry.
  //
  // Spec: §13.10
  it("points the version badge at the popover it owns and names its kind", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact": {
        body: {
          id: "platform/review",
          type: "context",
          version: "2.3.0",
          content_hash: "sha256:abc",
          manifest_body: "# Review\n",
          frontmatter: "",
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    goTo("#/artifact/platform%2Freview");
    render(<App />);
    await screen.findByLabelText("Artifact viewer");
    const badge = screen.getByRole("button", { name: /^Version / });
    // The popover is a labelled entry field with its own submit that takes
    // focus on opening and hands it back on Escape, so it is a non-modal
    // dialog and both ends say so.
    expect(badge.getAttribute("aria-haspopup")).toBe("dialog");
    const controls = badge.getAttribute("aria-controls");
    expect(controls).toBeTruthy();
    openVersionPicker();
    const popover = screen.getByRole("dialog", {
      name: "Read another version",
    });
    expect(popover.id).toBe(controls);
    within(popover).getByLabelText("Version");
    within(popover).getByRole("button", { name: "View" });
  });

  // Both of the picker's transient parts remove themselves: the disclosed
  // field closes on Escape, and the refusal banner disappears with the pin it
  // reports. Each hands the focus to the version badge, which is the control
  // the reader was operating and the one that survives the read. Without it
  // they are left on the document body at the top of the page.
  it("returns focus to the version badge when the picker and the refusal close", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact": {
        body: {
          id: "platform/review",
          type: "context",
          version: "2.3.0",
          content_hash: "sha256:abc",
          manifest_body: "# Review\n",
          frontmatter: "",
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    goTo("#/artifact/platform%2Freview");
    render(<App />);
    await screen.findByLabelText("Artifact viewer");
    const badge = () => screen.getByRole("button", { name: /^Version / });

    openVersionPicker();
    fireEvent.keyDown(screen.getByLabelText("Version"), { key: "Escape" });
    expect(screen.queryByLabelText("Version")).toBeNull();
    expect(document.activeElement).toBe(badge());

    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact": {
        status: 404,
        body: {
          code: "registry.not_found",
          message: "version: invalid pin: no candidate matches",
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    pinVersion("9.9.9");
    await screen.findByTestId("version-refused");
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact": {
        body: {
          id: "platform/review",
          type: "context",
          version: "2.3.0",
          content_hash: "sha256:abc",
          manifest_body: "# Review\n",
          frontmatter: "",
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    fireEvent.click(screen.getByRole("button", { name: "Show latest" }));
    await waitFor(() => {
      expect(screen.queryByTestId("version-refused")).toBeNull();
    });
    expect(document.activeElement).toBe(badge());
  });

  // Committing a pin closes the popover the same way a dismissal does, and it
  // removes the field the reader was typing in. The focus therefore goes back
  // to the version badge on that path too, whether the registry served the
  // pin, refused it, or was asked for the version already on screen. Without
  // it a keyboard reader who pinned a version was left on the document body,
  // above the whole shell, with the refusal banner they need to recover from
  // a full catalog tree away.
  //
  // Spec: §13.10
  it("returns focus to the version badge when the picker commits a pin", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact": {
        body: {
          id: "platform/review",
          type: "context",
          version: "2.3.0",
          content_hash: "sha256:abc",
          manifest_body: "# Review\n",
          frontmatter: "",
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    goTo("#/artifact/platform%2Freview");
    render(<App />);
    await screen.findByLabelText("Artifact viewer");
    const badge = () => screen.getByRole("button", { name: /^Version / });

    // A pin the registry serves.
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact": {
        body: {
          id: "platform/review",
          type: "context",
          version: "1.0.0",
          content_hash: "sha256:old",
          manifest_body: "# Review\n",
          frontmatter: "",
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    pinVersion("1.0.0");
    await screen.findByTestId("older-version");
    expect(document.activeElement).toBe(badge());

    // The version already on screen, which writes the state it already holds
    // and so renders nothing of its own for the handover to follow.
    openVersionPicker();
    fireEvent.click(screen.getByRole("button", { name: "View" }));
    await waitFor(() => {
      expect(screen.queryByLabelText("Version")).toBeNull();
    });
    expect(document.activeElement).toBe(badge());

    // A pin the registry refuses, submitted from the field itself.
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact": {
        status: 404,
        body: {
          code: "registry.not_found",
          message: "version: invalid pin: no candidate matches",
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    openVersionPicker();
    fireEvent.change(screen.getByLabelText("Version"), {
      target: { value: "9.9.9" },
    });
    // The commit refuses the key's default action, because the focus lands on
    // the badge while the press is still being processed and the browser
    // would otherwise carry the same Enter on to that button and disclose the
    // field again.
    const commit = fireEvent.keyDown(screen.getByLabelText("Version"), {
      key: "Enter",
    });
    expect(commit).toBe(false);
    await screen.findByTestId("version-refused");
    expect(document.activeElement).toBe(badge());
    expect(screen.queryByLabelText("Version")).toBeNull();
  });

  // The version the picker names belongs to the artifact it was named for. A
  // route change from one viewer to another reuses the component, so a pin
  // that survived it would read the next artifact at a version that artifact
  // has no candidate for, and the viewer would report an artifact that exists
  // as missing.
  it("drops the version the picker named when the route opens another artifact", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact?id=eng%2Fdeploy": {
        body: {
          id: "eng/deploy",
          type: "context",
          version: "2.3.0",
          content_hash: "sha256:abc",
          manifest_body: "# Deploy\n",
          frontmatter: "",
        },
      },
      "/v1/load_artifact?id=eng%2Fxss": {
        body: {
          id: "eng/xss",
          type: "context",
          version: "0.1.0",
          content_hash: "sha256:def",
          manifest_body: "# Escaping\n",
          frontmatter: "",
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    goTo("#/artifact/eng%2Fdeploy");
    render(<App />);
    await screen.findByLabelText("Artifact viewer");
    // No stub answers this version, so the registry refuses the pin.
    pinVersion("9.9.9");
    await screen.findByTestId("version-refused");

    goTo("#/artifact/eng%2Fxss");
    expect(await screen.findByRole("heading", { name: "xss" })).toBeTruthy();
    expect(screen.queryByTestId("artifact-failed")).toBeNull();
    expect(screen.queryByTestId("version-refused")).toBeNull();
    openVersionPicker();
    expect((screen.getByLabelText("Version") as HTMLInputElement).value).toBe(
      "",
    );
    // The next artifact is read at its latest version rather than at the
    // version the previous one was pinned to.
    expect(requests.some((r) => r.url.includes("id=eng%2Fxss&version="))).toBe(
      false,
    );
  });

  // A pin the registry refused is still the string the field opens on, so
  // reopening the picker on it with the caret at its end let the reader's
  // correction be appended to the refused pin: 0.1.0 typed into a field
  // holding 9.9.9 asks the registry for 9.9.90.1.0, which it refuses again.
  // The field is mounted per opening and its text is selected on mount, so
  // the correction replaces the refused pin and an abandoned edit is
  // discarded with the popover.
  // Spec: §13.10
  it("selects the refused pin when the version picker reopens on it", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact": {
        body: {
          id: "platform/review",
          type: "context",
          version: "2.3.0",
          content_hash: "sha256:abc",
          manifest_body: "# Review\n",
          frontmatter: "",
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    goTo("#/artifact/platform%2Freview");
    render(<App />);
    await screen.findByLabelText("Artifact viewer");
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact": {
        status: 404,
        body: {
          code: "registry.not_found",
          message: "version: invalid pin: no candidate matches",
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    pinVersion("9.9.9");
    await screen.findByTestId("version-refused");

    openVersionPicker();
    const reopened = screen.getByLabelText("Version") as HTMLInputElement;
    expect(reopened.value).toBe("9.9.9");
    // The whole string is selected, so the next keystroke replaces it.
    expect(reopened.selectionStart).toBe(0);
    expect(reopened.selectionEnd).toBe("9.9.9".length);

    // An edit the reader abandons is discarded with the popover rather than
    // waiting in the field the next opening presents.
    fireEvent.change(reopened, { target: { value: "9.9" } });
    fireEvent.keyDown(document, { key: "Escape" });
    openVersionPicker();
    expect((screen.getByLabelText("Version") as HTMLInputElement).value).toBe(
      "9.9.9",
    );
  });

  // The registry prefixes several §6.10 messages with the code they carry, and
  // the banner already states that code on a line of its own. The prose is
  // stripped of the repetition so the reader is told the code once.
  // Spec: §6.10
  it("states the error code once when the registry repeats it in the message", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact": {
        body: {
          id: "platform/review",
          type: "context",
          version: "2.3.0",
          content_hash: "sha256:abc",
          manifest_body: "# Review\n",
          frontmatter: "",
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    goTo("#/artifact/platform%2Freview");
    render(<App />);
    await screen.findByLabelText("Artifact viewer");
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact": {
        status: 404,
        body: {
          code: "registry.not_found",
          message:
            "registry.not_found: version: invalid pin: no candidate matches",
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    pinVersion("9.9.9");
    const refusal = await screen.findByTestId("version-refused");
    const occurrences =
      (refusal.textContent ?? "").split("registry.not_found").length - 1;
    expect(occurrences).toBe(1);
    // The rest of the envelope's prose survives the strip.
    expect(refusal.textContent).toContain(
      "version: invalid pin: no candidate matches",
    );
  });

  // The presigned channel delivers the canonical manifest document rather
  // than a body, and the response clears the field that document
  // duplicates. A viewer that hands the fetched document to the rendering
  // path renders the frontmatter as markdown, where the fences become rules
  // and the keys become prose, while the property table reports no pairs.
  it("reconstitutes a manifest delivered by presigned URL rather than rendering the document", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact": {
        body: {
          id: "platform/review",
          type: "context",
          version: "1.0.0",
          content_hash: "sha256:abc",
          manifest_body: "",
          frontmatter: "",
          manifest_body_url: {
            presigned_url: "https://objects.acme.com/abc",
            content_hash: "sha256:abc",
            size: 900,
          },
        },
      },
      "https://objects.acme.com/abc": {
        text: `${manifestDoc}\n# Review\n\nRun the checklist.\n`,
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    goTo("#/artifact/platform%2Freview");
    render(<App />);
    await screen.findByLabelText("Artifact viewer");
    const rendered = await screen.findByTestId("artifact-body");
    expect(rendered.querySelector("h1")?.textContent).toBe("Review");
    expect(rendered.querySelector("hr")).toBeNull();
    expect(rendered.textContent).not.toContain("name: review");
    const table = screen.getByTestId("rail-frontmatter-table");
    expect(table.textContent).toContain("name");
    expect(table.textContent).toContain("security");
    expect(screen.queryByText("No frontmatter on this artifact.")).toBeNull();
  });

  it("carries no authored-source view on an artifact that has none", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact": {
        body: {
          id: "platform/notes",
          type: "context",
          version: "1.0.0",
          content_hash: "sha256:abc",
          manifest_body: "Body.\n",
          frontmatter: manifestDoc,
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    goTo("#/artifact/platform%2Fnotes");
    render(<App />);
    await screen.findByLabelText("Artifact viewer");
    expect(screen.queryByLabelText("Authored source")).toBeNull();
  });

  it("states the absent body where the manifest carries frontmatter and nothing else", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact": {
        body: {
          id: "platform/bare",
          type: "context",
          version: "1.0.0",
          content_hash: "sha256:abc",
          manifest_body: "",
          frontmatter: manifestDoc,
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    goTo("#/artifact/platform%2Fbare");
    render(<App />);
    await screen.findByLabelText("Artifact viewer");
    // The loading and failure states are settled before the panel is drawn,
    // so the empty panel would otherwise read as a load that failed.
    expect(screen.queryByTestId("artifact-body")).toBeNull();
    expect(screen.getByText("This artifact has no body.")).toBeTruthy();
    // The rest of the viewer still reads as a finished document.
    expect(screen.getByTestId("rail-frontmatter-table").textContent).toContain(
      "name",
    );
  });

  // Frontmatter is authored, so a value can be one unbroken token such as a
  // serialised nested map. A table takes its min-content width as an automatic
  // minimum, so a cell that cannot break widens the rail's table past the rail
  // and scrolls the whole document sideways. The class carrying the break rule
  // stands on the table itself, so both cells inherit it.
  it("marks the property table as one whose cells break inside a long token", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact": {
        body: {
          id: "edge/odd-frontmatter",
          type: "skill",
          version: "0.1.0",
          content_hash: "sha256:abc",
          manifest_body: "Body.\n",
          frontmatter:
            '---\ntype: skill\ncustom_unknown_key: {"nested":{"deeper":[1,2,3]}}\n---\n',
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    goTo("#/artifact/edge%2Fodd-frontmatter");
    render(<App />);
    await screen.findByLabelText("Artifact viewer");
    const table = await screen.findByTestId("rail-frontmatter-table");
    expect(table.className.split(" ")).toContain("property-table");
    expect(table.textContent).toContain('{"nested":{"deeper":[1,2,3]}}');
  });

  // The panel states that its values are shown verbatim, so a value the
  // author wrote as a YAML block keeps that block: a nested mapping reads as
  // the key and value lines under it, and a literal block scalar keeps the
  // line breaks that are the whole reason it was written as one. Rendering
  // either as a JSON literal or on one line shows the author a document they
  // did not write (§13.10).
  it("shows a nested mapping and a block scalar as the YAML the author wrote", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact": {
        body: {
          id: "eng/yamly",
          type: "context",
          version: "0.1.0",
          content_hash: "sha256:abc",
          manifest_body: "Body.\n",
          frontmatter:
            "---\ntype: context\nmapping:\n  inner_key: inner value\n  other: 2\nnotes: |\n  A literal block\n  across two lines\n---\n",
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    goTo("#/artifact/eng%2Fyamly");
    render(<App />);
    await screen.findByLabelText("Artifact viewer");
    fireEvent.click(screen.getByRole("tab", { name: /Frontmatter/ }));
    expect(screen.getByTestId("property-value-mapping").textContent).toBe(
      "inner_key: inner value\nother: 2",
    );
    expect(screen.getByTestId("property-value-notes").textContent).toBe(
      "A literal block\nacross two lines",
    );
  });

  // The two views of one block must agree. A plain scalar's authored token is
  // not its parsed value: the YAML core schema resolves `007` to the number 7
  // and `1.10` to 1.1, so a table built from parsed values shows a key whose
  // value the author never wrote while Raw YAML beside it shows the token
  // (§13.10). A quoted scalar still drops its delimiters, which is the text
  // inside them.
  it("shows a scalar as the token the author wrote rather than as its parsed value", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact": {
        body: {
          id: "eng/scalars",
          type: "context",
          version: "0.1.0",
          content_hash: "sha256:abc",
          manifest_body: "Body.\n",
          frontmatter:
            '---\ntype: context\nnum: 007\nrelease: 1.10\nlabel: "quoted text"\n---\n',
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    goTo("#/artifact/eng%2Fscalars");
    render(<App />);
    await screen.findByLabelText("Artifact viewer");
    fireEvent.click(screen.getByRole("tab", { name: /Frontmatter/ }));
    expect(screen.getByTestId("property-value-num").textContent).toBe("007");
    expect(screen.getByTestId("property-value-release").textContent).toBe(
      "1.10",
    );
    expect(screen.getByTestId("property-value-label").textContent).toBe(
      "quoted text",
    );
    // The same tab's other view shows the same tokens.
    fireEvent.click(screen.getByRole("button", { name: "Raw YAML" }));
    const raw = screen.getByTestId("raw-frontmatter").textContent ?? "";
    expect(raw).toContain("num: 007");
    expect(raw).toContain("release: 1.10");
  });

  it("drops the rail’s frontmatter section where the response yields no pairs", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact": {
        body: {
          id: "platform/review",
          type: "context",
          version: "1.0.0",
          content_hash: "sha256:abc",
          manifest_body: "Body.\n",
          frontmatter: "",
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    goTo("#/artifact/platform%2Freview");
    render(<App />);
    await screen.findByLabelText("Artifact viewer");
    // The rail reads as provenance followed directly by relations: the
    // section header goes with the table rather than standing over an empty
    // one, and the tab carries no parse-failure badge.
    expect(screen.queryByTestId("rail-frontmatter-table")).toBeNull();
    expect(screen.queryByLabelText("Frontmatter")).toBeNull();
    expect(
      screen.getByRole("tab", { name: /Frontmatter/ }).textContent,
    ).not.toContain("!");
    fireEvent.click(screen.getByRole("tab", { name: /Frontmatter/ }));
    expect(screen.getByText("No frontmatter on this artifact.")).toBeTruthy();
    expect(
      await screen.findByText("Nothing extends this artifact."),
    ).toBeTruthy();
    expect(screen.getByText("This artifact extends nothing.")).toBeTruthy();
  });

  it("draws a rail section’s absence quieter than the section label over it", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact": {
        body: {
          id: "platform/review",
          type: "context",
          version: "1.0.0",
          content_hash: "sha256:abc",
          manifest_body: "Body.\n",
          frontmatter: "",
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    goTo("#/artifact/platform%2Freview");
    render(<App />);
    await screen.findByLabelText("Artifact viewer");
    // Every absent rail section takes the inline preset, so absence never
    // reads louder than the sections that hold content.
    for (const sentence of [
      "This artifact extends nothing.",
      "Nothing extends this artifact.",
      "This artifact bundles no files.",
    ]) {
      const absent = (await screen.findByText(sentence)).closest(
        ".empty",
      ) as HTMLElement;
      // The rail variant is the title-less single line, so one absent
      // section never outweighs the sections beside it that hold content.
      expect([sentence, absent.textContent]).toEqual([sentence, sentence]);
      expect([sentence, absent.className.split(" ")]).toEqual([
        sentence,
        ["empty", "empty-inline"],
      ]);
      const style = window.getComputedStyle(absent);
      expect([sentence, style.fontSize, style.color]).toEqual([
        sentence,
        "12.5px",
        "var(--faint)",
      ]);
      expect([sentence, style.borderRadius, style.padding]).toEqual([
        sentence,
        "9px",
        "14px",
      ]);
    }
  });

  it("drops the rail’s frontmatter section while the Frontmatter tab stands the same pairs full width", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact": {
        body: {
          id: "platform/review",
          type: "context",
          version: "1.0.0",
          content_hash: "sha256:abc",
          manifest_body: "Body.\n",
          frontmatter: "---\nname: review\nsensitivity: low\n---\n",
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    goTo("#/artifact/platform%2Freview");
    render(<App />);
    await screen.findByLabelText("Artifact viewer");
    // The rendered tab carries no property table of its own, so the rail
    // states the pairs beside it.
    expect(screen.getByTestId("rail-frontmatter-table").textContent).toContain(
      "sensitivity",
    );
    fireEvent.click(screen.getByRole("tab", { name: /Frontmatter/ }));
    // The panel now stands the same pairs full width, so the rail drops its
    // section header along with its table and reads as provenance followed
    // directly by relations.
    expect(screen.getByTestId("frontmatter-table").textContent).toContain(
      "sensitivity",
    );
    expect(screen.queryByTestId("rail-frontmatter-table")).toBeNull();
    expect(screen.queryByRole("region", { name: "Frontmatter" })).toBeNull();
    // The panel opens with the line that states where the pairs came from,
    // and the Table and Raw YAML views stand beside it on the same row.
    expect(
      screen.getByText(/Unknown keys are preserved and shown as authored\./),
    ).toBeTruthy();
    expect(screen.getByText(/Values are shown verbatim\./)).toBeTruthy();
    expect(
      screen
        .getByRole("group", { name: "Frontmatter view" })
        .closest(".source-actions"),
    ).not.toBeNull();
    // Leaving the tab returns the rail's own copy.
    fireEvent.click(screen.getByRole("tab", { name: "Rendered" }));
    expect(screen.getByTestId("rail-frontmatter-table").textContent).toContain(
      "sensitivity",
    );
  });

  // Every exclusive one-row choice in the build is the same segmented control,
  // so the chosen segment is raised onto the surface colour over a chip track.
  // A switch that fills the chosen segment with the track colour instead
  // inverts the control and reads as the unchosen view being the live one.
  it("draws the frontmatter view switch as the shared segmented control", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact": {
        body: {
          id: "platform/review",
          type: "context",
          version: "1.0.0",
          content_hash: "sha256:abc",
          manifest_body: "Body.\n",
          frontmatter: "---\nname: review\n---\n",
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    goTo("#/artifact/platform%2Freview");
    render(<App />);
    await screen.findByLabelText("Artifact viewer");
    fireEvent.click(screen.getByRole("tab", { name: /Frontmatter/ }));
    const group = screen.getByRole("group", { name: "Frontmatter view" });
    expect(group.className.split(" ")).toContain("segmented");
    const view = within(group);
    expect(view.getByRole("button", { name: "Table" }).className).toBe(
      "segment segment-on",
    );
    expect(view.getByRole("button", { name: "Raw YAML" }).className).toBe(
      "segment",
    );
    fireEvent.click(view.getByRole("button", { name: "Raw YAML" }));
    expect(view.getByRole("button", { name: "Raw YAML" }).className).toBe(
      "segment segment-on",
    );
    expect(view.getByRole("button", { name: "Table" }).className).toBe(
      "segment",
    );
  });

  it("reports a frontmatter block that does not parse without affecting the rest of the viewer", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact": {
        body: {
          id: "platform/review",
          type: "skill",
          version: "1.0.0",
          content_hash: "sha256:abc",
          manifest_body: "# Review\n",
          frontmatter: "name: review\n\tbad: tab\n",
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    goTo("#/artifact/platform%2Freview");
    render(<App />);
    await screen.findByLabelText("Artifact viewer");
    // The parse failure is reported on the tab that opens the block, so a
    // reader on another tab is told the block did not parse.
    expect(
      screen.getByRole("tab", { name: /Frontmatter/ }).textContent,
    ).toContain("!");
    expect(
      screen.getByTestId("artifact-body").querySelector("h1")?.textContent,
    ).toBe("Review");
    fireEvent.click(screen.getByRole("tab", { name: /Frontmatter/ }));
    expect(screen.getAllByText("Invalid syntax").length).toBeGreaterThan(0);
    // The banner carries the parser's own position and the raw block below
    // it marks the line that position names.
    expect(screen.getAllByText(/line 2, column/).length).toBeGreaterThan(0);
    expect(screen.getAllByTestId("offending-line")[0].textContent).toContain(
      "bad: tab",
    );
  });

  // A description is author-controlled and carries no length bound, and the
  // header states it above the version picker, the tabs, and the body. Left
  // unclipped, a description of several hundred words pushes all of them
  // below the fold and the artifact reads as empty until the reader scrolls,
  // while the listing row that linked to the page clips the same string.
  // jsdom performs no layout, so the two heights the control reads are
  // stubbed and the clip itself is pinned in the layout case set.
  describe("the header description", () => {
    /** stubHeights makes a clipped paragraph report the overrun a browser
     * would measure on it, and an opened one report none. */
    function stubHeights(scrollHeight: number) {
      const clientHeight = 60;
      Object.defineProperty(HTMLParagraphElement.prototype, "scrollHeight", {
        configurable: true,
        get(this: HTMLParagraphElement) {
          return this.classList.contains("clamped")
            ? scrollHeight
            : clientHeight;
        },
      });
      Object.defineProperty(HTMLParagraphElement.prototype, "clientHeight", {
        configurable: true,
        get: () => clientHeight,
      });
    }

    afterEach(() => {
      delete (HTMLParagraphElement.prototype as { scrollHeight?: number })
        .scrollHeight;
      delete (HTMLParagraphElement.prototype as { clientHeight?: number })
        .clientHeight;
    });

    function stubViewer(description: string) {
      stubRegistry({
        "/v1/ui/session": { body: posture({ public_mode: true }) },
        "/v1/load_artifact": {
          body: {
            id: "edge/many-tags",
            type: "context",
            version: "0.1.0",
            content_hash: "sha256:abc",
            manifest_body: "# Many tags\n",
            frontmatter: `---\nname: many-tags\ndescription: ${description}\n---\n`,
          },
        },
        "/v1/dependents": { body: { edges: [] } },
      });
      goTo("#/artifact/edge%2Fmany-tags");
      render(<App />);
    }

    it("clips a long description and opens it on request", async () => {
      stubHeights(900);
      stubViewer("The invoice approval path routes each document.");
      await screen.findByLabelText("Artifact viewer");
      const lead = screen.getByTestId("artifact-lead");
      expect(lead.classList.contains("clamped")).toBe(true);
      const more = await screen.findByRole("button", { name: "Show more" });
      expect(more.getAttribute("aria-expanded")).toBe("false");
      fireEvent.click(more);
      expect(
        screen.getByTestId("artifact-lead").classList.contains("clamped"),
      ).toBe(false);
      const less = screen.getByRole("button", { name: "Show less" });
      expect(less.getAttribute("aria-expanded")).toBe("true");
      // Collapsing restores the clip, so the control is not a one-way door.
      fireEvent.click(less);
      expect(
        screen.getByTestId("artifact-lead").classList.contains("clamped"),
      ).toBe(true);
    });

    // A route change swaps the description in place rather than remounting the
    // header, so the clip's own state has to follow the new text. Left alone,
    // an opened long description leaves its control standing over the next
    // artifact's single short line, where it collapses nothing.
    it("drops the control when the route changes to a description the clip holds", async () => {
      // Each paragraph overruns the clip in proportion to its own text, so the
      // two artifacts measure differently against one stubbed layout.
      const clientHeight = 60;
      Object.defineProperty(HTMLParagraphElement.prototype, "scrollHeight", {
        configurable: true,
        get(this: HTMLParagraphElement) {
          return this.classList.contains("clamped") &&
            (this.textContent ?? "").length > 40
            ? 900
            : clientHeight;
        },
      });
      Object.defineProperty(HTMLParagraphElement.prototype, "clientHeight", {
        configurable: true,
        get: () => clientHeight,
      });
      function viewed(id: string, description: string) {
        return {
          body: {
            id,
            type: "context",
            version: "0.1.0",
            content_hash: "sha256:abc",
            manifest_body: "# Viewed\n",
            frontmatter: `---\nname: viewed\ndescription: ${description}\n---\n`,
          },
        };
      }
      const stubs: Record<string, Stub> = {
        "/v1/ui/session": { body: posture({ public_mode: true }) },
        "/v1/load_artifact": viewed(
          "edge/many-tags",
          "The invoice approval path routes each document through every approver on it.",
        ),
        "/v1/dependents": { body: { edges: [] } },
      };
      stubRegistry(stubs);
      goTo("#/artifact/edge%2Fmany-tags");
      render(<App />);
      fireEvent.click(await screen.findByRole("button", { name: "Show more" }));
      expect(screen.getByRole("button", { name: "Show less" })).toBeTruthy();

      stubs["/v1/load_artifact"] = viewed("edge/no-body", "No body at all.");
      goTo("#/artifact/edge%2Fno-body");
      await waitFor(() => {
        expect(screen.getByTestId("artifact-lead").textContent).toBe(
          "No body at all.",
        );
      });
      // The short line is stated whole and carries no control of either name.
      expect(
        screen.getByTestId("artifact-lead").classList.contains("clamped"),
      ).toBe(true);
      expect(screen.queryByRole("button", { name: "Show less" })).toBeNull();
      expect(screen.queryByRole("button", { name: "Show more" })).toBeNull();
      // The rail states the same field in its property table, whole and
      // with no control of its own.
      expect(screen.getByTestId("property-value-description").textContent).toBe(
        "No body at all.",
      );
      expect(
        screen.getByTestId("rail-frontmatter-table").querySelector("button"),
      ).toBeNull();
    });

    // A control that reports aria-expanded has to name the region the state
    // belongs to, or assistive technology announces "expanded" over nothing.
    // The header and the rail each clip the same description on one page, so
    // the two regions also have to carry distinct ids.
    // Spec: §13.10
    it("points each clip control at the region it opens", async () => {
      stubHeights(900);
      stubViewer("The invoice approval path routes each document.");
      await screen.findByLabelText("Artifact viewer");
      const lead = screen.getByTestId("artifact-lead");
      const railValue = screen.getByTestId("property-value-description");
      const headerControl = screen.getByRole("button", { name: "Show more" });
      const railControl = screen.getByRole("button", {
        name: "Show the whole description value",
      });
      expect(headerControl.getAttribute("aria-controls")).toBe(lead.id);
      expect(lead.id).not.toBe("");
      expect(railControl.getAttribute("aria-controls")).toBe(railValue.id);
      expect(railValue.id).not.toBe("");
      expect(lead.id).not.toBe(railValue.id);
      // Opening the region keeps the association, so the announced state and
      // the named region stay in step.
      fireEvent.click(headerControl);
      const opened = screen.getByRole("button", { name: "Show less" });
      expect(opened.getAttribute("aria-expanded")).toBe("true");
      expect(opened.getAttribute("aria-controls")).toBe(
        screen.getByTestId("artifact-lead").id,
      );
    });

    it("offers no control for a description the clip already holds", async () => {
      stubHeights(60);
      stubViewer("Pay a supplier invoice.");
      await screen.findByLabelText("Artifact viewer");
      expect(screen.getByTestId("artifact-lead").textContent).toBe(
        "Pay a supplier invoice.",
      );
      expect(screen.queryByRole("button", { name: "Show more" })).toBeNull();
    });

    // Description is optional in an artifact's frontmatter. The listing row
    // and the subdomain card both state its absence in an italic placeholder,
    // and the header states it in the same one: collapsing the line away puts
    // the title straight onto the tab strip and reads as a rendering gap.
    // Spec: §13.10
    it("states an absent description in the header", async () => {
      stubHeights(60);
      stubRegistry({
        "/v1/ui/session": { body: posture({ public_mode: true }) },
        "/v1/load_artifact": {
          body: {
            id: "edge/no-description",
            type: "context",
            version: "0.1.0",
            content_hash: "sha256:abc",
            manifest_body: "# No description\n",
            frontmatter: "---\nname: no-description\n---\n",
          },
        },
        "/v1/dependents": { body: { edges: [] } },
      });
      goTo("#/artifact/edge%2Fno-description");
      render(<App />);
      await screen.findByLabelText("Artifact viewer");
      const lead = screen.getByTestId("artifact-lead");
      expect(lead.textContent).toBe("No description.");
      expect(lead.classList.contains("absent-description")).toBe(true);
      // The placeholder is one short line, so it carries no clip and no
      // control to open.
      expect(lead.classList.contains("clamped")).toBe(false);
      expect(screen.queryByRole("button", { name: "Show more" })).toBeNull();
    });

    // The rail is a summary column, and the relation links §13.10 requires
    // the viewer to carry stand under this table in the same scrolling
    // column. A description of several hundred words rendered whole makes one
    // row many screens tall and pushes those links off the fold, so the rail
    // clips a scalar value at the three lines the header's own description
    // reads at and offers the rest in place.
    // Spec: §13.10
    it("clips a long property value in the rail's table and opens it on request", async () => {
      stubHeights(900);
      stubViewer("The invoice approval path routes each document.");
      await screen.findByLabelText("Artifact viewer");
      const value = screen.getByTestId("property-value-description");
      expect(value.textContent).toBe(
        "The invoice approval path routes each document.",
      );
      expect(value.classList.contains("clamped")).toBe(true);
      // The control names its own row, so it is distinguishable from the
      // header's control and from the other rows' controls.
      const more = await screen.findByRole("button", {
        name: "Show the whole description value",
      });
      expect(more.getAttribute("aria-expanded")).toBe("false");
      fireEvent.click(more);
      expect(
        screen
          .getByTestId("property-value-description")
          .classList.contains("clamped"),
      ).toBe(false);
      // Collapsing restores the clip, so the control is not a one-way door.
      fireEvent.click(
        screen.getByRole("button", {
          name: "Show the whole description value",
        }),
      );
      expect(
        screen
          .getByTestId("property-value-description")
          .classList.contains("clamped"),
      ).toBe(true);
    });

    // A sequence one entry per line is unbounded in the rail's narrow column:
    // a dozen tags run the frontmatter table past 700px on their own and push
    // RELATIONS and RESOURCES off the fold, which is the state the clip
    // exists to prevent. The rail runs the entries together on one line and
    // clips that line like any other value, and the control opens the rest in
    // place.
    // Spec: §13.10
    it("clips a sequence value in the rail's table and opens it on request", async () => {
      stubHeights(900);
      stubRegistry({
        "/v1/ui/session": { body: posture({ public_mode: true }) },
        "/v1/load_artifact": {
          body: {
            id: "edge/many-tags",
            type: "context",
            version: "0.1.0",
            content_hash: "sha256:abc",
            manifest_body: "# Many tags\n",
            frontmatter:
              "---\nname: many-tags\ntags: [alpha, bravo, charlie, delta, echo," +
              " foxtrot, golf, hotel, india, juliet]\n---\n",
          },
        },
        "/v1/dependents": { body: { edges: [] } },
      });
      goTo("#/artifact/edge%2Fmany-tags");
      render(<App />);
      await screen.findByLabelText("Artifact viewer");
      const tags = screen.getByTestId("property-value-tags");
      // No entry is dropped: the whole sequence is in the cell, on one line.
      expect(tags.textContent).toBe(
        "alpha, bravo, charlie, delta, echo, foxtrot, golf, hotel, india, juliet",
      );
      expect(tags.querySelectorAll("li")).toHaveLength(0);
      expect(tags.classList.contains("clamped")).toBe(true);
      // The rest is one control away rather than gone.
      const more = await screen.findByRole("button", {
        name: "Show the whole tags value",
      });
      fireEvent.click(more);
      expect(
        screen.getByTestId("property-value-tags").classList.contains("clamped"),
      ).toBe(false);
    });

    // The full-width Frontmatter panel states that its values are shown
    // verbatim, and it carries nothing under the table to bury, so it keeps
    // them whole.
    it("leaves the full-width frontmatter panel unclipped", async () => {
      stubHeights(900);
      stubViewer("The invoice approval path routes each document.");
      await screen.findByLabelText("Artifact viewer");
      fireEvent.click(screen.getByRole("tab", { name: /Frontmatter/ }));
      const panel = screen.getByTestId("frontmatter-table");
      expect(panel.querySelector(".clamped")).toBeNull();
    });

    // A key the author wrote with no value is a pair the block carries, so
    // the row stands. Rendered as a blank cell it reads as the table having
    // failed rather than as the value being absent, so the cell states the
    // absence with an em dash in both the rail and the full-width panel.
    // Spec: §13.10
    it("states an empty frontmatter value as an em dash", async () => {
      stubHeights(60);
      stubViewer('""');
      await screen.findByLabelText("Artifact viewer");
      const railValue = screen.getByTestId("property-absent-description");
      expect(railValue.textContent).toBe("—");
      // The dash is decoration to a screen reader, so the cell names which
      // key it belongs to and what the dash stands for.
      expect(railValue.getAttribute("aria-label")).toBe(
        "description has no value",
      );
      // The em dash stands in place of the value, so the cell carries no
      // value element beside it.
      expect(screen.queryByTestId("property-value-description")).toBeNull();
      // The neighbouring pair still states its own value.
      expect(
        screen.getByTestId("rail-frontmatter-table").textContent,
      ).toContain("many-tags");

      fireEvent.click(screen.getByRole("tab", { name: /Frontmatter/ }));
      expect(
        screen
          .getByTestId("frontmatter-table")
          .querySelector(".property-absent")?.textContent,
      ).toBe("—");
    });

    // An authored null is an absence the author wrote out. Printing the token
    // makes `null` read as a value that was set, and it splits the two
    // authored empties apart: `tags: []` takes the em dash while
    // `license: null` prints a word. Every YAML null token takes the same em
    // dash, and a quoted "null" is the string the author asked for.
    // Spec: §13.10
    it("states an authored null frontmatter value as an em dash", async () => {
      stubHeights(60);
      stubRegistry({
        "/v1/ui/session": { body: posture({ public_mode: true }) },
        "/v1/load_artifact": {
          body: {
            id: "eng/odd",
            type: "context",
            version: "0.1.0",
            content_hash: "sha256:abc",
            manifest_body: "# Odd\n",
            frontmatter:
              '---\nname: odd\ntags: []\nlicense: null\nretired: ~\nverdict: "null"\n---\n',
          },
        },
        "/v1/dependents": { body: { edges: [] } },
      });
      goTo("#/artifact/eng%2Fodd");
      render(<App />);
      await screen.findByLabelText("Artifact viewer");

      // The empty sequence and the two null tokens reach the same marker.
      for (const key of ["tags", "license", "retired"]) {
        const absent = screen.getByTestId(`property-absent-${key}`);
        expect(absent.textContent).toBe("—");
        expect(absent.getAttribute("aria-label")).toBe(`${key} has no value`);
        expect(screen.queryByTestId(`property-value-${key}`)).toBeNull();
      }
      // A quoted null is a string, so the cell states the word the author
      // wrote.
      expect(screen.getByTestId("property-value-verdict").textContent).toBe(
        "null",
      );

      // The full-width panel states the same absences.
      fireEvent.click(screen.getByRole("tab", { name: /Frontmatter/ }));
      const panel = screen.getByTestId("frontmatter-table");
      expect(panel.querySelectorAll(".property-absent")).toHaveLength(3);
      expect(panel.textContent).not.toContain("~");
    });

    // A sequence value is several entries, and both surfaces run them onto one
    // line: the rail joins the text and clips it, and the panel keeps the
    // entries as list items the sheet flows inline. The markup is what carries
    // the entry boundary either way, so no entry is dropped and no entry is
    // merged into its neighbour.
    // Spec: §13.10
    it("carries every entry of a sequence value on both surfaces", async () => {
      stubHeights(60);
      stubRegistry({
        "/v1/ui/session": { body: posture({ public_mode: true }) },
        "/v1/load_artifact": {
          body: {
            id: "finance/pay-invoice",
            type: "context",
            version: "0.1.0",
            content_hash: "sha256:abc",
            manifest_body: "# Pay invoice\n",
            frontmatter:
              "---\nname: pay-invoice\ntags: [finance, ap]\nwhen_to_use:\n" +
              "  - The user asks to pay a vendor invoice.\n" +
              "  - A purchase order needs matching against a received invoice.\n---\n",
          },
        },
        "/v1/dependents": { body: { edges: [] } },
      });
      goTo("#/artifact/finance%2Fpay-invoice");
      render(<App />);
      await screen.findByLabelText("Artifact viewer");

      // The rail runs the entries together, so its cell carries no list.
      expect(
        screen
          .getByTestId("property-value-tags")
          .querySelectorAll("li"),
      ).toHaveLength(0);
      expect(screen.getByTestId("property-value-tags").textContent).toBe(
        "finance, ap",
      );

      fireEvent.click(screen.getByRole("tab", { name: /Frontmatter/ }));
      const panel = screen.getByTestId("frontmatter-table");
      const uses = panel.querySelector<HTMLElement>(
        '[data-testid="property-value-when_to_use"]',
      );
      expect(
        Array.from(uses?.querySelectorAll("li") ?? []).map(
          (item) => item.textContent,
        ),
      ).toEqual([
        "The user asks to pay a vendor invoice.",
        "A purchase order needs matching against a received invoice.",
      ]);
      // The separator is drawn by the sheet rather than authored into the
      // text, so the entry a reader copies is the entry the author wrote.
      expect(uses?.textContent).not.toContain("invoice., A");
      expect(
        Array.from(
          panel.querySelectorAll('[data-testid="property-value-tags"] li'),
        ).map((item) => item.textContent),
      ).toEqual(["finance", "ap"]);
    });
  });
});

describe("the layer panel", () => {
  // The panel renders for a caller who resolves no subject on a registry
  // that configures no identity provider, which is the standalone deployment
  // where nobody authenticates and the panel is the point. An
  // implementation that hides the panel from an anonymous caller fails here.
  it("renders its write operations for a caller who resolves no subject", async () => {
    stubRegistry({
      "/v1/ui/session": {
        body: posture({ identity_provider_configured: false }),
      },
      "/v1/layers": { body: { layers: [adminLayer(), userLayer()] } },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    expect(screen.getAllByRole("button", { name: "Reingest" }).length).toBe(2);
    // One row's actions are open at a time, so each row's menu is read on its
    // own turn.
    openRowActions("company");
    expect(screen.getByRole("menuitem", { name: "Unregister" })).toBeTruthy();
    openRowActions();
    expect(screen.getByRole("menuitem", { name: "Unregister" })).toBeTruthy();
    expect(screen.queryByText("yours")).toBeNull();
  });

  // The header row is drawn entirely in the section-label style, so the
  // column names read as one row rather than as mono handles beside
  // sentence-case sans text. The actions column names no data and carries
  // no header at all.
  it("draws every column header in the section-label style", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [userLayer()] } },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    const headers = Array.from(
      document.querySelectorAll("table.layer-table thead th"),
    );
    expect(
      headers.map(
        (header) => header.querySelector(".label")?.textContent ?? "",
      ),
    ).toEqual(["", "Layer", "Source", "Visibility", "Last ingest", ""]);
    // The handle column and the actions column each carry a control that
    // names itself, so neither takes a column title.
    expect(headers[0].textContent).toBe("");
    expect(headers[5].textContent).toBe("");
  });

  // The table's columns are fixed proportions floored at the width they are
  // drawn at, so a viewport too narrow to hold them scrolls the table
  // sideways rather than squeezing a cell into one character to the line. The
  // container is a focusable region so a keyboard reaches the scroll.
  it("puts the table in a container that scrolls sideways", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [userLayer()] } },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    const table = document.querySelector("table.layer-table") as HTMLElement;
    const container = table.parentElement as HTMLElement;
    expect(container.classList.contains("table-scroll")).toBe(true);
    expect(container.tabIndex).toBe(0);
    expect(container.getAttribute("aria-label")).toBe("Layers");
  });

  // Every row shares one grid and the actions column is fixed width, so the
  // row's controls stay on one line: one action plus an overflow control.
  // Rendering Edit, Reingest, and Unregister side by side stacked them and
  // tripled the height of every row.
  it("keeps the row to one action and an overflow control", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [userLayer()] } },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    const cell = screen
      .getByRole("button", { name: "Reingest" })
      .closest("td") as HTMLElement;
    expect(
      within(cell)
        .getAllByRole("button")
        .map((button) => button.textContent),
    ).toEqual(["Reingest", "⋯"]);
    expect(screen.queryByRole("menuitem", { name: "Edit" })).toBeNull();
    expect(screen.queryByRole("menuitem", { name: "Unregister" })).toBeNull();
    openRowActions();
    expect(screen.getByRole("menuitem", { name: "Edit" })).toBeTruthy();
    expect(screen.getByRole("menuitem", { name: "Unregister" })).toBeTruthy();
  });

  // The ownership marker is a property of a user-defined row alone. An
  // admin-defined row carries none on any value of its stored owner, because
  // that owner is a caller-supplied field naming no authorized subject.
  it("marks a user-defined row the caller owns and marks no admin-defined row", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": {
        body: {
          layers: [adminLayer("alice@acme.com"), userLayer("alice@acme.com")],
        },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    expect(screen.getAllByText("yours").length).toBe(1);
    // The admin-defined row still shows its stored owner, as the field it
    // is. It carries no ownership language and none of the marker's
    // styling, because the write rule authorizes a tenant admin there and
    // that field names no authorized subject.
    const stored = screen.getAllByTestId("layer-order")[0];
    expect(stored.textContent).toBe("order 1 · owner alice@acme.com");
    expect(stored.className).not.toContain("badge");
  });

  // The panel's subject is precedence, so every row states its own place in
  // it. The position counts down the table, which is sorted the way §4.6
  // composes the catalog, because the stored order values set precedence
  // within a class alone and are neither contiguous nor comparable across the
  // two blocks.
  // Spec: §13.10
  it("states each row's place in the precedence order", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": {
        body: {
          layers: [
            { ...adminLayer("platform-eng"), Order: 11 },
            { ...userLayer("alice@acme.com"), Order: 21 },
          ],
        },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    expect(
      screen.getAllByTestId("layer-order").map((note) => note.textContent),
    ).toEqual([
      "order 1 · owner platform-eng",
      "order 2 · owner alice@acme.com",
    ]);
  });

  // A badge sets a trailing margin and no leading one, so a name written
  // straight into the cell before one reads as a single run of text. The row
  // that holds them supplies the gap.
  it("sets the ownership marker off from the layer name it qualifies", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [userLayer("alice@acme.com")] } },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    const marker = screen.getByText("yours");
    const row = marker.parentElement;
    expect(row).not.toBeNull();
    expect(row?.textContent).toContain("alice-personal");
    const style = window.getComputedStyle(row as HTMLElement);
    expect(style.display).toBe("flex");
    expect(style.getPropertyValue("gap")).toBe("7px");
  });

  // The row states the last ingest as an age, the way the sidebar footer
  // states the same fact, with the ingest reference beneath it. The stored
  // stamp is a microsecond ISO-8601 string that wraps over two lines in this
  // column, so it is carried on the cell's title and nowhere in its text.
  it("states the last ingest as an age over the short ingest ref", async () => {
    const at = new Date(Date.now() - 2 * 60 * 60 * 1000).toISOString();
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": {
        body: {
          layers: [
            {
              ...adminLayer(),
              last_ingested_at: at,
              LastIngestedRef: "4f2a1c9d8e7b6a5c4d3e2f1a0b9c8d7e6f5a4b3c",
            },
            userLayer(),
          ],
        },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    const ingested = lastIngestCell("company");
    expect(ingested.textContent).toBe("2h ago4f2a1c9");
    expect(ingested.textContent).not.toContain(at);
    expect(within(ingested).getByTitle(at)).toBeTruthy();
    // A layer no ingest has run against says so, and a source that carries
    // no reference keeps the row the same height as one that does.
    expect(lastIngestCell("alice-personal").textContent).toBe("never—");
  });

  // A local source records the directory it read as its ingest reference, so
  // after a reingest the stored reference is the layer's own path. The Source
  // column two cells to the left already states that path, and repeating it
  // here wraps over several lines, so a non-git layer displays no reference.
  it("displays no ingest ref on a local layer whose stored ref is its own path", async () => {
    const at = new Date(Date.now() - 4 * 60 * 1000).toISOString();
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": {
        body: {
          layers: [
            {
              ...userLayer(),
              last_ingested_at: at,
              LastIngestedRef: "/Users/alice/registry",
            },
          ],
        },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    const ingested = lastIngestCell("alice-personal");
    expect(ingested.textContent).toBe("4m ago—");
    expect(ingested.textContent).not.toContain("/Users/alice/registry");
  });

  // The identifier names the row on both tables the reader crosses, so it is
  // marked as the row's name and drawn heavier than the precedence line under
  // it and the source path beside it (§13.10).
  it("marks the layer identifier as the name of the row on the layer panel", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [userLayer()] } },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    const name = screen.getByText("alice-personal");
    expect(name.classList.contains("layer-name")).toBe(true);
  });

  // A layer carrying no stored owner states its position alone. Appending an
  // empty owner clause would read as a field the reader could set from here.
  it("states the position alone on a row whose stored owner is unset", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [adminLayer()] } },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    expect(screen.getByTestId("layer-order").textContent).toBe("order 1");
    expect(screen.queryByText("yours")).toBeNull();
  });

  // A write can come back refused, including on a row the panel presented as
  // the caller's to manage. The refusal is drawn on the row, says only that
  // the action was refused and nothing changed, and leaves every other
  // control live.
  it("presents a refused write on the row without reporting ownership or session state", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [userLayer("bob@acme.com")] } },
      "DELETE /v1/layers": {
        status: 403,
        body: { code: "auth.forbidden", message: "not permitted" },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    openRowActions();
    fireEvent.click(screen.getByRole("menuitem", { name: "Unregister" }));
    fireEvent.change(screen.getByLabelText("Type the layer ID to confirm"), {
      target: { value: "alice-personal" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Unregister layer" }));
    expect(await screen.findByText(/nothing changed/)).toBeTruthy();
    expect(screen.getByText("auth.forbidden")).toBeTruthy();
    openRowActions();
    expect(
      screen.getByRole("menuitem", { name: "Edit" }).hasAttribute("disabled"),
    ).toBe(false);
  });

  // The refusal is full-width prose and the actions column is a fixed narrow
  // column in a grid every row shares, so the card is drawn in a row of its
  // own under the layer rather than inside that cell. Drawn inside it the
  // card stretched the layer's row to its own height, emptied every other
  // cell over that height, and pushed the row's controls apart.
  it("draws a refusal under the row rather than inside the actions cell", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [userLayer()] } },
      "/v1/layers/reingest": {
        status: 403,
        body: {
          code: "auth.forbidden",
          message: "not permitted",
          retryable: false,
        },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Reingest" }));
    const card = await screen.findByLabelText("Reingest refused");
    const cell = card.closest("td");
    expect(cell).not.toBeNull();
    expect(cell?.className).not.toContain("row-actions");
    // The card spans the table in a row of its own, which is what leaves the
    // layer's own cells at their own height.
    expect((cell as HTMLTableCellElement).colSpan).toBe(6);
    const detail = cell?.closest("tr");
    expect(detail?.className).toContain("row-detail");
    // The layer's own row keeps its cells and its action cluster.
    const row = layerRow("alice-personal");
    expect(row.querySelectorAll("td").length).toBe(6);
    expect(row.contains(card)).toBe(false);
    const bar = row.querySelector(".row-action-bar");
    expect(bar?.querySelectorAll("button").length).toBe(2);
  });

  // A refusal is drawn on the row and on the action that was attempted. The
  // panel stacks one Reingest button per layer and draws the banner in a row
  // under the layer it belongs to, so a trigger left in its ordinary tone
  // leaves the reader inferring which control produced the banner from where
  // the banner sits.
  it("tints the Reingest button a refusal was attempted from and no other", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [adminLayer(), userLayer()] } },
      "/v1/layers/reingest?id=company": {
        status: 503,
        body: {
          code: "registry.unavailable",
          message: "the store is unreachable",
          retryable: true,
        },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    const refusedTrigger = reingestTrigger("company");
    const untouched = reingestTrigger("alice-personal");
    expect(refusedTrigger.className).not.toContain("action-refused");
    fireEvent.click(refusedTrigger);
    await screen.findByLabelText("Reingest refused");
    expect(refusedTrigger.className).toContain("action-refused");
    // Every other row's action stays in its ordinary tone.
    expect(untouched.className).not.toContain("action-refused");
    // Dismissing the refusal clears the tone with it.
    fireEvent.click(screen.getByRole("button", { name: "Dismiss" }));
    expect(refusedTrigger.className).not.toContain("action-refused");
  });

  it("renders one marker per matching visibility axis and summarises an axis that overflows", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/layers": {
        body: {
          layers: [
            {
              ID: "shared",
              SourceType: "git",
              Repo: "git@github.com:acme/shared.git",
              Ref: "main",
              Order: 1,
              Public: true,
              Organization: true,
              Groups: ["secops", "appsec", "platform", "data"],
              Users: ["carol@acme.com"],
            },
          ],
        },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    expect(screen.getByText("public")).toBeTruthy();
    expect(screen.getByText("organization")).toBeTruthy();
    expect(screen.getByText("group: secops · appsec")).toBeTruthy();
    expect(screen.getByText("+2")).toBeTruthy();
    expect(screen.getByText("user: carol@acme.com")).toBeTruthy();
    // The markers sit beside each other in one wrapping cell, so a row that
    // grants on four axes is the height of a row that grants on one.
    const cell = screen.getByText("public").closest(".visibility-markers");
    expect(cell).toBeTruthy();
    expect(cell?.querySelectorAll(".badge").length).toBe(4);
    // A marker states the grant the row is listed under, which is the fact
    // the column exists to carry, so it keeps the badge's outline and its
    // body tone. The soft tone is the source chip beside it, and a marker
    // that took it would read at the source chip's weight.
    expect(cell?.querySelectorAll(".badge-grant").length).toBe(4);
    expect(cell?.querySelectorAll(".badge-soft").length).toBe(0);
    // Asserted against the stylesheet the bundle ships rather than against
    // the class name, because the two treatments differ only in the edge and
    // the text tone: the soft chip declares its border away and drops to the
    // metadata tone, and a marker that did the same would be indistinguishable
    // from the source chip in the column beside it.
    const marker = screen.getByText("public").closest(".badge");
    const markerStyle = getComputedStyle(marker as Element);
    expect(markerStyle.borderColor).not.toBe("transparent");
    expect(markerStyle.color).toBe("var(--sec)");
    for (const marker of [
      "organization",
      "group: secops · appsec",
      "user: carol@acme.com",
    ]) {
      expect(cell?.contains(screen.getByText(marker))).toBe(true);
    }
  });

  // The remainder count is the part of an overflowing marker the reader
  // cannot reconstruct from anything else on the row, so it is drawn in its
  // own element outside the clipping run rather than at the end of the
  // member names, where the cell's ellipsis cut it off.
  it("counts the members an overflowing visibility axis does not name", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/layers": {
        body: {
          layers: [
            {
              ID: "shared",
              SourceType: "git",
              Repo: "git@github.com:acme/shared.git",
              Ref: "main",
              Order: 1,
              Groups: [
                "secops",
                "appsec",
                "platform-eng",
                "data-platform",
                "design-ops",
              ],
              Users: ["alice@acme.com", "bob@acme.com", "carol@acme.com"],
            },
          ],
        },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    const cell = screen
      .getByText("group: secops · appsec")
      .closest(".visibility-markers");
    expect(cell).toBeTruthy();
    // An axis of addresses names one member and counts the other two, because
    // two addresses are wider than the column holds.
    expect(screen.getByText("user: alice@acme.com")).toBeTruthy();
    const counts = [...(cell?.querySelectorAll(".marker-extra") ?? [])].map(
      (node) => node.textContent?.trim(),
    );
    expect(counts).toEqual(["+3", "+2"]);
  });

  // A row granting on no axis states that as a marker of the same size and
  // geometry as a granted axis, so the column reads as one row of markers
  // whatever the grant state. Set as plain body text the statement read as a
  // cell that had failed to render its marker.
  it("states an absent grant as an outlined marker in the visibility cell", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/layers": {
        body: {
          layers: [
            {
              ID: "alice-personal",
              SourceType: "local",
              Path: "/Users/alice/registry",
              Order: 1,
            },
            {
              ID: "shared",
              SourceType: "local",
              Path: "/srv/registry",
              Order: 2,
              Organization: true,
            },
          ],
        },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    const marker = screen.getByText("no grants — only you");
    expect(marker.className).toContain("badge");
    // The marker sits in the cell a granted row uses, so the two rows put
    // their markers at the same place in the column.
    expect(marker.closest(".visibility-markers")).toBeTruthy();
    // Asserted against the stylesheet the bundle ships: the marker takes the
    // padding and the mono face a granted axis takes, so the column holds one
    // row of markers of one size, and it separates itself by dropping the
    // fill the granted marker carries.
    const style = getComputedStyle(marker);
    const granted = getComputedStyle(
      screen.getByText("organization").closest(".badge") as Element,
    );
    expect(style.padding).toBe(granted.padding);
    expect(style.fontFamily).toBe(granted.fontFamily);
    expect(style.fontSize).toBe(granted.fontSize);
    expect(style.backgroundColor).toBe("rgba(0, 0, 0, 0)");
    expect(granted.background).not.toBe(style.background);
    expect(style.color).toBe("var(--meta)");
  });

  // The source cell names the type it is showing and states every source
  // field the layer was registered with. A git layer's configured root is
  // part of where the layer's artifacts come from, so a row that omits it
  // describes a different source than the one the registry stored.
  it("names the source type and states the git root and the local path", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/layers": {
        body: {
          layers: [
            {
              ID: "acme-git-main",
              SourceType: "git",
              Repo: "git@github.com:acme/registry.git",
              Ref: "main",
              Root: "catalog",
              Order: 1,
            },
            userLayer(),
          ],
        },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    const git = within(layerRow("acme-git-main"));
    expect(git.getByText("git")).toBeTruthy();
    expect(git.getByText("main")).toBeTruthy();
    // A detail line is split into the run the cell may clip and the run it
    // always draws, so the value is read off the line rather than matched as
    // one text node.
    expect(details(layerRow("acme-git-main"))).toEqual([
      "git@github.com:acme/registry.git",
      "catalog/",
    ]);
    const local = within(layerRow("alice-personal"));
    expect(local.getByText("local")).toBeTruthy();
    expect(details(layerRow("alice-personal"))).toEqual([
      "/Users/alice/registry",
    ]);
    // The chip names the source type at the weight of a qualifier, so it
    // takes the badge's soft tone rather than the outline the layer's own
    // markers carry.
    for (const chip of [git.getByText("git"), local.getByText("local")]) {
      expect(chip.className.split(" ")).toContain("badge-soft");
    }
  });

  // A local path or a repository URL can be far longer than the source
  // column. Wrapping it broke the string between characters and stacked one
  // row over three or four lines, so a detail line stays on one line and
  // repeats its whole value in the title attribute, where a reader who needs
  // the tail of the path can still read it.
  it("keeps a long source path on one line and states it whole in the title", async () => {
    const longPath =
      "/var/folders/q_/df6ygvl10fj4g162_ld1tkvw0000gn/T/tmp.8UKSGzgrdh/reg";
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/layers": {
        body: {
          layers: [
            {
              ID: "scratch",
              SourceType: "local",
              LocalPath: longPath,
              Order: 1,
            },
          ],
        },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    const detail = layerRow("scratch").querySelector(".source-detail");
    expect(detail?.textContent).toBe(longPath);
    expect(detail?.getAttribute("title")).toBe(longPath);
    const style = window.getComputedStyle(detail as Element);
    expect(style.whiteSpace).toBe("nowrap");
    expect(style.overflow).toBe("hidden");
    const head = window.getComputedStyle(
      detail?.querySelector(".source-detail-head") as Element,
    );
    expect(head.textOverflow).toBe("ellipsis");
  });

  // Layers registered under one parent directory share every leading
  // directory of their path, so a line clipped from the right rendered them
  // as the same string and the source column stopped telling the rows apart.
  // The segment that identifies the row is held out of the clip.
  it("holds each source path's last segment out of the clip", async () => {
    const parent =
      "/var/folders/q_/df6ygvl10fj4g162_ld1tkvw0000gn/T/registries";
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/layers": {
        body: {
          layers: [
            {
              ID: "finance",
              SourceType: "local",
              LocalPath: `${parent}/finance-shared`,
              Order: 1,
            },
            {
              ID: "eng",
              SourceType: "local",
              LocalPath: `${parent}/eng-shared`,
              Order: 2,
            },
          ],
        },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    const tail = (id: string) =>
      layerRow(id).querySelector(".source-detail-tail")?.textContent;
    expect(tail("finance")).toBe("finance-shared");
    expect(tail("eng")).toBe("eng-shared");
    const head = (id: string) =>
      layerRow(id).querySelector(".source-detail-head")?.textContent;
    expect(head("finance")).toBe(`${parent}/`);
    // The head fills the width the flex layout leaves it, so an elision at
    // its end stopped at the last whole character that fitted and left up to
    // a character of slack before the tail, which read as a space between two
    // values. The elision falls at the head's start, where what it keeps ends
    // flush against the tail, and the head isolates its own text so a leading
    // separator is not reordered to the far end of the run.
    const line = layerRow("finance").querySelector(".source-detail-head");
    expect(window.getComputedStyle(line as Element).direction).toBe("rtl");
    const isolated = line?.querySelector("bdi");
    expect(isolated?.textContent).toBe(`${parent}/`);
    expect(window.getComputedStyle(isolated as Element).direction).toBe("ltr");
  });

  // A final segment wider than the column would be clipped at its own end by
  // an ordinary clip, which loses exactly the characters the reader is
  // scanning for, so the clip falls at the segment's start instead. Held out
  // of the shrink entirely, the segment overflowed the line's clip and was
  // sliced with no marker, and the row read as a whole value beside sibling
  // rows that all carried a leading ellipsis.
  it("elides a source path's last segment at its start where it fills the cell", async () => {
    const longSegment = "a".repeat(60);
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/layers": {
        body: {
          layers: [
            {
              ID: "wide",
              SourceType: "local",
              LocalPath: `/srv/${longSegment}`,
              Order: 1,
            },
          ],
        },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    const row = layerRow("wide");
    const tail = row.querySelector(".source-detail-tail") as Element;
    expect(tail.textContent).toBe(longSegment);
    const tailStyle = window.getComputedStyle(tail);
    expect(tailStyle.textOverflow).toBe("ellipsis");
    expect(tailStyle.overflow).toBe("hidden");
    expect(tailStyle.direction).toBe("rtl");
    expect(tailStyle.flexShrink).toBe("1");
    const isolated = tail.querySelector("bdi");
    expect(isolated?.textContent).toBe(longSegment);
    expect(window.getComputedStyle(isolated as Element).direction).toBe("ltr");
    // The head absorbs the whole shrink before the tail gives up a pixel, so
    // a line whose head still fits keeps its final segment complete.
    const headShrink = window.getComputedStyle(
      row.querySelector(".source-detail-head") as Element,
    ).flexShrink;
    expect(Number(headShrink)).toBeGreaterThan(1000);
    const line = window.getComputedStyle(
      row.querySelector(".source-detail") as Element,
    );
    expect(line.justifyContent).toBe("flex-end");
    expect(row.querySelector(".source-detail")?.getAttribute("title")).toBe(
      `/srv/${longSegment}`,
    );
  });

  // A source type is pluggable, so a type the panel has never seen still
  // renders: the chip carries its name and its fields sit behind a
  // disclosure.
  it("renders an unknown source type as a chip with its fields behind a disclosure", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/layers": {
        body: {
          layers: [
            {
              ID: "acme-vault",
              SourceType: "vault-kv",
              Repo: "acme/vault-artifacts",
              Root: "artifacts",
              Order: 1,
            },
          ],
        },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    const row = within(layerRow("acme-vault"));
    expect(row.getByText("vault-kv")).toBeTruthy();
    fireEvent.click(row.getByText("2 source fields"));
    expect(row.getByText("acme/vault-artifacts")).toBeTruthy();
    expect(row.getByText("artifacts")).toBeTruthy();
  });

  // The panel states what a layer is under its title, names the winning end
  // of the order on the precedence label itself, and closes with the caller's
  // holding against the §7.3.1 cap beside what a reorder does. Left to
  // position alone, a table sorted the other way round reads the same, and
  // the cap is stated nowhere else on the surface.
  it("states its description, the winning end of the order, and the cap on the caller layers", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [adminLayer(), userLayer()] } },
      "/v1/quota": { body: { limits: { MaxUserLayers: 3 } } },
    });
    goTo("#/layers");
    render(<App />);
    const panel = within(await screen.findByLabelText("Layer panel"));
    expect(
      panel.getByText(/Sources the catalog is composed from/).textContent,
    ).toContain(
      "When two layers carry the same artifact ID, the higher precedence wins.",
    );
    // The qualifier sits on the label's own line rather than in a paragraph
    // below the label.
    const label = panel.getByText(
      "Precedence — drag or press the arrow keys on a handle to reorder",
    ).parentElement as HTMLElement;
    expect(within(label).getByText("lower row wins")).toBeTruthy();
    expect(
      (await screen.findByTestId("personal-layer-count")).textContent,
    ).toBe("You have 1 of 3 personal layers.");
    expect(
      panel.getByText(/Reordering takes effect on the next read/).textContent,
    ).toContain("it does not trigger a reingest");
  });

  // The denominator is stated only where the quota read reports a positive
  // cap. Zero selects the deployment default and no response reports what
  // that default resolved to, so the footer states the holding alone rather
  // than a limit no response carried.
  it("states the caller holding alone where the quota read reports no cap", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [adminLayer(), userLayer()] } },
      "/v1/quota": { body: { limits: { MaxUserLayers: 0 } } },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    await waitFor(() => {
      expect(requests.some((r) => r.url === "/v1/quota")).toBe(true);
    });
    expect(screen.getByTestId("personal-layer-count").textContent).toBe(
      "You have 1 personal layer.",
    );
  });

  // A caller who resolves no subject owns no row the panel can recognize as
  // theirs, so the footer carries the reordering note by itself.
  it("states no personal holding for a caller who resolves no subject", async () => {
    stubRegistry({
      "/v1/ui/session": {
        body: posture({ identity_provider_configured: false }),
      },
      "/v1/layers": { body: { layers: [adminLayer(), userLayer()] } },
      "/v1/quota": { body: { limits: { MaxUserLayers: 3 } } },
    });
    goTo("#/layers");
    render(<App />);
    const panel = within(await screen.findByLabelText("Layer panel"));
    expect(screen.queryByTestId("personal-layer-count")).toBeNull();
    expect(
      panel.getByText(/Reordering takes effect on the next read/),
    ).toBeTruthy();
  });
});

describe("the session-expiry transition", () => {
  // The catalog read is the expiry signal, and the treatment's control is
  // bounded by the sign-in control table: on a deployment running the
  // browser flow it is a navigation to the read's own sign_in_path. The
  // caller held a subject when the page loaded, which is what makes this the
  // expiry transition rather than the anonymous refused arm.
  it("offers the read’s sign-in path where a catalog read is refused mid-session", async () => {
    stubRegistry({
      "/v1/ui/session": {
        body: posture({
          subject: "alice@acme.com",
          browser_auth: {
            enabled: true,
            sign_in_path: "/v1/ui/auth/sign-in",
            sign_out_path: "/v1/ui/auth/sign-out",
          },
        }),
      },
      "/v1/load_domain": {
        status: 401,
        body: { code: "auth.token_expired", message: "expired" },
      },
    });
    render(<App />);
    await screen.findByTestId("session-ended");
    expect(
      (await screen.findByTestId("expiry-sign-in")).getAttribute("href"),
    ).toBe("/v1/ui/auth/sign-in");
    // The transition stands over the page the caller was on. The refused
    // screen that stands in place of the catalog belongs to the caller who
    // held no subject, so it is not what this caller gets.
    expect(screen.queryByLabelText("Catalog refused")).toBeNull();
  });

  // The expiry arm keeps the page underneath. A caller reading a domain whose
  // sidebar expansion is then refused keeps the domain surface, with the one
  // sentence over it.
  it("keeps the domain the caller was reading under the transition", async () => {
    stubRegistry({
      "/v1/ui/session": {
        body: posture({
          subject: "alice@acme.com",
          browser_auth: {
            enabled: true,
            sign_in_path: "/v1/ui/auth/sign-in",
            sign_out_path: "/v1/ui/auth/sign-out",
          },
        }),
      },
      "/v1/load_domain": {
        body: {
          path: "platform",
          subdomains: [
            {
              path: "platform/ci",
              name: "ci",
              subdomains: [{ path: "platform/ci/lint", name: "lint" }],
            },
          ],
          notable: [],
        },
      },
      "/v1/search_artifacts": { body: { total_matched: 0 } },
      "/v1/layers": { body: { layers: [] } },
    });
    goTo("#/domain/platform");
    render(<App />);
    await screen.findByLabelText("Domain browser");
    const tree = await screen.findByLabelText("Catalog");
    stubRegistry({
      "/v1/load_domain": {
        status: 401,
        body: { code: "auth.token_expired", message: "expired" },
      },
    });
    // The node at the eager read's edge is the one that reads for itself, and
    // that read is the refusal the transition hangs on.
    fireEvent.click(
      within(tree).getByRole("button", { name: "Expand platform/ci" }),
    );
    fireEvent.click(within(tree).getByRole("button", { name: "Expand lint" }));
    await screen.findByTestId("session-ended");
    expect(screen.getByLabelText("Domain browser")).toBeTruthy();
    expect(screen.queryByLabelText("Catalog refused")).toBeNull();
  });

  // The layers route issues no catalog read of its own, so the panel would
  // receive the ended session on no path at all unless the shell takes one.
  // The panel is kept underneath the treatment.
  it("presents the ended session on the layer panel and keeps the panel underneath", async () => {
    stubRegistry({
      "/v1/ui/session": {
        body: posture({
          subject: "alice@acme.com",
          browser_auth: {
            enabled: true,
            sign_in_path: "/v1/ui/auth/sign-in",
            sign_out_path: "/v1/ui/auth/sign-out",
          },
        }),
      },
      "/v1/load_domain": {
        status: 401,
        body: { code: "auth.token_expired", message: "expired" },
      },
      "/v1/layers": { body: { layers: [userLayer()] } },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    await screen.findByTestId("session-ended");
    expect(
      (await screen.findByTestId("expiry-sign-in")).getAttribute("href"),
    ).toBe("/v1/ui/auth/sign-in");
    expect(screen.getByText("alice-personal")).toBeTruthy();
  });

  // The third row of the sign-in control table bounds what the treatment may
  // offer: a deployment running no browser flow renders no authentication
  // control, so the treatment states what it offers in its place.
  it("offers a retry of the refused read where the deployment runs no browser flow", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/load_domain": {
        status: 401,
        body: { code: "auth.token_expired", message: "expired" },
      },
    });
    render(<App />);
    await screen.findByTestId("session-ended");
    expect(screen.queryByTestId("expiry-sign-in")).toBeNull();
    expect(screen.getByText(/runs no browser sign-in/)).toBeTruthy();
    // The third row renders no authentication control, so the treatment has to
    // state what it offers in its place, and a retry of the refused read is
    // that control.
    expect(screen.getByTestId("expiry-retry")).toBeTruthy();
  });

  // A posture read that did not answer is a different arm from a deployment
  // that reported the browser flow disabled. It is reachable on any
  // deployment, so the recovery claims nothing about whether a browser
  // sign-in exists and offers the retry alone.
  it("claims no deployment property where the posture read did not answer", async () => {
    stubRegistry({
      "/v1/ui/session": {
        status: 503,
        body: { code: "registry.unavailable", message: "no posture" },
      },
      "/v1/load_domain": {
        status: 401,
        body: { code: "auth.untrusted_token", message: "no identity" },
      },
    });
    render(<App />);
    await screen.findByLabelText("Catalog refused");
    expect(screen.queryByText(/runs no browser sign-in/)).toBeNull();
    expect(screen.queryByTestId("expiry-sign-in")).toBeNull();
    expect(screen.getByTestId("expiry-retry")).toBeTruthy();
  });

  // The refused arm is reached by a caller who never held a subject as well,
  // on a registry whose verifier refuses a browser that carries no token. The
  // expiry transition belongs to the caller whose read resolved a subject, so
  // this caller is told what the read returned and nothing about a session.
  it("claims no ended session where the posture read resolved no subject", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ browser_auth: { enabled: false } }) },
      "/v1/load_domain": {
        status: 401,
        body: { code: "auth.untrusted_token", message: "no identity" },
      },
      "/v1/layers": { body: { layers: [userLayer()] } },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    await screen.findByTestId("refused-read");
    expect(screen.queryByTestId("session-ended")).toBeNull();
    expect(screen.queryByTestId("expiry-sign-in")).toBeNull();
  });

  // The refused arm belongs to a read the registry could not verify an
  // identity for, and the codes that carry it are the ones the identity
  // middleware writes. The tenant router answers auth.tenant_unknown with the
  // same status for a caller whose token verified, so a page keying on the
  // status alone would tell that caller their session ended while it is
  // intact. That failure takes the surface's own error state.
  it("claims no ended session where a verified caller names an unprovisioned tenant", async () => {
    const unknownTenant = {
      status: 401,
      body: {
        code: "auth.tenant_unknown",
        message:
          "Verified token names organization 'globex' which is not a provisioned tenant.",
        details: { token_org_id: "globex" },
      },
    };
    stubRegistry({
      "/v1/ui/session": {
        body: posture({
          subject: "alice@acme.com",
          browser_auth: {
            enabled: true,
            sign_in_path: "/v1/ui/auth/sign-in",
            sign_out_path: "/v1/ui/auth/sign-out",
          },
        }),
      },
      "/v1/load_domain": unknownTenant,
      "/v1/layers": unknownTenant,
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByText("auth.tenant_unknown");
    expect(screen.queryByTestId("session-ended")).toBeNull();
    expect(screen.queryByTestId("refused-read")).toBeNull();
  });
});

describe("read-only mode", () => {
  // §13.2.1 marks a read-only registry on its read responses, so the panel
  // presents the state once and makes every write control unavailable at the
  // same time. A panel that keeps its controls live collects one refusal per
  // button press instead, which is the presentation the brief forbids. The
  // marker arrives on a catalog read, because the middleware that sets it
  // wraps the meta-tool mux and the layer endpoints are mounted beside it.
  it("presents the state once and makes every write control unavailable", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/load_domain": {
        body: emptyDomain,
        headers: { "X-Podium-Read-Only": "true" },
      },
      "/v1/layers": { body: { layers: [adminLayer(), userLayer()] } },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    await screen.findByTestId("read-only-banner");
    openRowActions("company");
    openRowActions();
    for (const name of ["Register layer", "Reingest all", "Reingest"]) {
      for (const control of screen.getAllByRole("button", { name })) {
        expect(control.hasAttribute("disabled")).toBe(true);
      }
    }
    for (const name of ["Unregister", "Edit"]) {
      for (const control of screen.getAllByRole("menuitem", { name })) {
        expect(control.hasAttribute("disabled")).toBe(true);
      }
    }
    // Reordering is a write too, so the rows carry no drag and the handles
    // take no key on a read-only registry rather than committing a move the
    // registry would refuse.
    for (const handle of screen.getAllByLabelText(/^Move .*arrow key$/)) {
      expect(handle.getAttribute("draggable")).toBe("false");
      expect(handle.hasAttribute("disabled")).toBe(true);
    }
  });

  it("keeps every write control live where the registry serves writes", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/load_domain": { body: emptyDomain },
      "/v1/layers": { body: { layers: [userLayer()] } },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    expect(screen.queryByTestId("read-only-banner")).toBeNull();
    expect(
      screen.getByRole("button", { name: "Reingest" }).hasAttribute("disabled"),
    ).toBe(false);
  });

  // The layer endpoints are outside the §13.2.1 middleware, so a response from
  // one of them carries the marker on no mode. A panel that read that absence
  // as "the registry serves writes" would clear the banner on its own list
  // read and on every reload after it.
  it("keeps the banner where a layer read carries no marker", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/load_domain": {
        body: emptyDomain,
        headers: { "X-Podium-Read-Only": "true" },
      },
      "/v1/layers": { body: { layers: [userLayer()] } },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    await screen.findByTestId("read-only-banner");
    // The panel's own recoverable count is a second layer read, and it
    // carries the marker on no mode either.
    await waitFor(() => {
      expect(requests.some((r) => r.url.includes("deleted=true"))).toBe(true);
    });
    expect(screen.getByTestId("read-only-banner")).toBeTruthy();
  });

  // The middleware that sets the marker wraps the meta-tool mux from inside
  // the identity verification and the tenant router, so a refusal from either
  // is written before that middleware runs and carries no marker whatever the
  // mode is. A page that read that absence as "the registry serves writes"
  // would clear the banner and make every write control live again the moment
  // the session expired on a registry that still refuses every write.
  it("keeps the banner where a catalog read is refused", async () => {
    stubRegistry({
      "/v1/ui/session": {
        body: posture({
          subject: "alice@acme.com",
          browser_auth: {
            enabled: true,
            sign_in_path: "/v1/ui/auth/sign-in",
            sign_out_path: "/v1/ui/auth/sign-out",
          },
        }),
      },
      "/v1/load_domain": {
        body: emptyDomain,
        headers: { "X-Podium-Read-Only": "true" },
      },
      "/v1/layers": { body: { layers: [userLayer()] } },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    await screen.findByTestId("read-only-banner");
    // The session ends, so the shell's next catalog read is refused before it
    // reaches the marker middleware. Re-entering the route re-issues it.
    stubRegistry({
      "/v1/ui/session": {
        body: posture({
          subject: "alice@acme.com",
          browser_auth: {
            enabled: true,
            sign_in_path: "/v1/ui/auth/sign-in",
            sign_out_path: "/v1/ui/auth/sign-out",
          },
        }),
      },
      "/v1/load_domain": {
        status: 401,
        body: { code: "auth.token_expired", message: "expired" },
      },
      "/v1/layers": { body: { layers: [userLayer()] } },
    });
    goTo("#/");
    await screen.findByTestId("session-ended");
    goTo("#/layers");
    await screen.findByTestId("session-ended");
    await screen.findByLabelText("Layer panel");
    expect(screen.getByTestId("read-only-banner")).toBeTruthy();
    expect(
      screen.getByRole("button", { name: "Reingest" }).hasAttribute("disabled"),
    ).toBe(true);
  });
});

describe("the layer write flows", () => {
  // Unregistering removes the layer's artifacts from every caller's view, so
  // the write is issued only after a confirmation stating both halves of
  // what it does and only once the layer's own ID has been typed.
  it("holds the unregister write until the confirmation is completed", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [userLayer()] } },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    openRowActions();
    fireEvent.click(screen.getByRole("menuitem", { name: "Unregister" }));
    const dialog = await screen.findByLabelText("Unregister alice-personal");
    expect(dialog.textContent).toContain("every caller");
    expect(dialog.textContent).toContain("Recoverable for 30 days");
    expect(requests.some((r) => r.method === "DELETE")).toBe(false);
    const confirm = screen.getByRole("button", { name: "Unregister layer" });
    expect(confirm.hasAttribute("disabled")).toBe(true);
    fireEvent.change(screen.getByLabelText("Type the layer ID to confirm"), {
      target: { value: "alice-personal" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Unregister layer" }));
    await waitFor(() => {
      expect(
        requests.some(
          (r) => r.url.startsWith("/v1/layers?") && r.method === "DELETE",
        ),
      ).toBe(true);
    });
  });

  // Every other held write in the panel names what is holding it, so the
  // unregister confirmation states its hold in the footer and points the
  // disabled control and the field at that sentence. The confirmation opens
  // with the field empty, so the sentence stands only once what the reader
  // typed does not match, and it clears once the typed ID does match, which is
  // when the control becomes pressable.
  it("names the hold on the unregister confirmation until the typed ID matches", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [userLayer()] } },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    openRowActions();
    fireEvent.click(screen.getByRole("menuitem", { name: "Unregister" }));
    await screen.findByLabelText("Unregister alice-personal");
    const field = screen.getByLabelText("Type the layer ID to confirm");
    const confirm = screen.getByRole("button", { name: "Unregister layer" });
    // Nothing has been typed, so the confirmation opens on no sentence at all
    // rather than on one in the refusal colour. The field's own label carries
    // the instruction until then.
    expect(screen.queryByTestId("unregister-foot-note")).toBeNull();
    expect(confirm.getAttribute("aria-describedby")).toBeNull();
    expect(field.getAttribute("aria-describedby")).toBeNull();
    expect(confirm.hasAttribute("disabled")).toBe(true);
    // A near miss is still held, and the sentence states the hold.
    fireEvent.change(field, { target: { value: "alice-persona" } });
    const note = screen.getByTestId("unregister-foot-note");
    expect(note.textContent).toBe(
      "Type the layer ID to confirm the unregistration.",
    );
    expect(note.className).toContain("modal-foot-hold");
    expect(confirm.getAttribute("aria-describedby")).toBe(note.id);
    expect(field.getAttribute("aria-describedby")).toBe(note.id);
    expect(confirm.hasAttribute("disabled")).toBe(true);
    fireEvent.change(field, { target: { value: "alice-personal" } });
    expect(screen.queryByTestId("unregister-foot-note")).toBeNull();
    expect(confirm.getAttribute("aria-describedby")).toBeNull();
    expect(confirm.hasAttribute("disabled")).toBe(false);
  });

  // The destructive half and the recoverable half of the confirmation sit
  // side by side and differ only in their fill, so each leads with its own
  // glyph in a fixed gutter, the way the register form's consequence and note
  // do. The glyph carries no text of its own and stays out of the
  // accessibility tree.
  it("leads each unregister consequence block with its own glyph", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [userLayer()] } },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    openRowActions();
    fireEvent.click(screen.getByRole("menuitem", { name: "Unregister" }));
    const dialog = await screen.findByLabelText("Unregister alice-personal");
    const banners = Array.from(dialog.querySelectorAll(".banner"));
    expect(banners.length).toBe(2);
    for (const banner of banners) {
      const style = window.getComputedStyle(banner);
      expect(style.display).toBe("flex");
      expect(style.gap).toBe("11px");
      const glyph = banner.firstElementChild as HTMLElement;
      expect(glyph.className).toBe("banner-glyph");
      expect(glyph.getAttribute("aria-hidden")).toBe("true");
      expect(glyph.textContent?.trim()).toBeTruthy();
      expect(window.getComputedStyle(glyph).width).toBe("11px");
    }
    // The danger block's mark is not the neutral block's, so the two halves
    // are told apart by more than their fill.
    expect(banners[0].classList.contains("banner-danger")).toBe(true);
    expect(banners[0].firstElementChild?.textContent?.trim()).not.toBe(
      banners[1].firstElementChild?.textContent?.trim(),
    );
  });

  // The confirmation's single field commits on Enter, the way the version
  // picker's does, so the reader who has typed the ID does not have to reach
  // for the pointer. A half-typed ID leaves Enter inert, on the same match
  // the confirm button gates on.
  it("submits the unregister on Enter once the typed ID matches", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [userLayer()] } },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    openRowActions();
    fireEvent.click(screen.getByRole("menuitem", { name: "Unregister" }));
    await screen.findByLabelText("Unregister alice-personal");
    const field = screen.getByLabelText("Type the layer ID to confirm");
    fireEvent.change(field, { target: { value: "alice-pers" } });
    fireEvent.keyDown(field, { key: "Enter" });
    expect(requests.some((r) => r.method === "DELETE")).toBe(false);
    fireEvent.change(field, { target: { value: "alice-personal" } });
    fireEvent.keyDown(field, { key: "Enter" });
    await waitFor(() => {
      expect(
        requests.some(
          (r) => r.url.startsWith("/v1/layers?") && r.method === "DELETE",
        ),
      ).toBe(true);
    });
    await waitFor(() => {
      expect(screen.queryByLabelText("Unregister alice-personal")).toBe(null);
    });
  });

  // The sidebar footer states how many layers the tenant carries and how many
  // artifacts its catalog matches, and a layer write moves both. Read once
  // for the page, the footer kept the figures the reader arrived with for the
  // rest of the session, so a register or an unregister left it stating a
  // count no response carried.
  it("re-reads the sidebar counts after a layer write", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/load_domain": { body: emptyDomain },
      "/v1/catalog": { body: catalogOf(312) },
      "/v1/layers": { body: { layers: [adminLayer(), userLayer()] } },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    await waitFor(() => {
      expect(screen.getByTestId("catalog-counts").textContent).toBe(
        "2 layers · 312 artifacts",
      );
    });
    // The write lands, and the registry answers every read after it with the
    // catalog the write left behind.
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/load_domain": { body: emptyDomain },
      "/v1/catalog": { body: catalogOf(200) },
      "DELETE /v1/layers": { body: {} },
      "/v1/layers?deleted=true": { body: { layers: [userLayer()] } },
      "/v1/layers": { body: { layers: [adminLayer()] } },
    });
    openRowActions();
    fireEvent.click(screen.getByRole("menuitem", { name: "Unregister" }));
    await screen.findByLabelText("Unregister alice-personal");
    fireEvent.change(screen.getByLabelText("Type the layer ID to confirm"), {
      target: { value: "alice-personal" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Unregister layer" }));
    await waitFor(() => {
      expect(screen.getByTestId("catalog-counts").textContent).toBe(
        "1 layer · 200 artifacts",
      );
    });
  });

  // A layer write moves the catalog itself, so the sidebar tree is re-read
  // from the same signal as the footer counts. Refreshing only the counts
  // left the tree standing on the hierarchy the reader arrived with, and a
  // domain the write had just added appeared only after a page reload.
  it("re-reads the sidebar tree after a layer write", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/load_domain": {
        body: {
          path: "",
          subdomains: [{ path: "eng", name: "eng" }],
          notable: [],
        },
      },
      "/v1/catalog": { body: catalogOf(9) },
      "/v1/layers": { body: { layers: [userLayer()] } },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    await waitFor(() => {
      expect(
        within(screen.getByLabelText("Catalog")).getByText("eng"),
      ).toBeTruthy();
    });
    expect(
      within(screen.getByLabelText("Catalog")).queryByText("hr"),
    ).toBeNull();
    // The reingest lands artifacts under a domain the catalog did not carry,
    // and every read after it sees what the write left behind.
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/load_domain": {
        body: {
          path: "",
          subdomains: [
            { path: "eng", name: "eng" },
            { path: "hr", name: "hr" },
          ],
          notable: [],
        },
      },
      "/v1/catalog": { body: catalogOf(10) },
      "/v1/layers": { body: { layers: [userLayer()] } },
      "/v1/layers/reingest": { body: { layer: "alice-personal", accepted: 1 } },
    });
    fireEvent.click(screen.getByRole("button", { name: "Reingest" }));
    await screen.findByLabelText("Reingest result for alice-personal");
    await waitFor(() => {
      expect(
        within(screen.getByLabelText("Catalog")).getByText("hr"),
      ).toBeTruthy();
    });
    expect(screen.getByTestId("catalog-counts").textContent).toBe(
      "1 layer · 10 artifacts",
    );
  });

  // The confirmation is a dialog over a scrim rather than a panel inside the
  // row's actions cell. Rendered into the cell it took the column's width and
  // grew the row by several hundred pixels, which pushed every row below it
  // down the page while the reader was deciding.
  it("opens the unregister confirmation as a dialog over a scrim rather than inside the row", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [adminLayer(), userLayer()] } },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    openRowActions();
    fireEvent.click(screen.getByRole("menuitem", { name: "Unregister" }));
    const dialog = await screen.findByLabelText("Unregister alice-personal");
    expect(screen.getByTestId("modal-scrim").contains(dialog)).toBe(true);
    expect(dialog.closest("tr")).toBeNull();
    // The audience the write takes the layer from is stated beside the ID
    // the reader types, so the confirmation names more than the ID.
    expect(screen.getByTestId("unregister-properties").textContent).toContain(
      "no grants",
    );
    // Cancel leads the footer and the destructive control carries the danger
    // tone, so the press that reaches every caller is the one to aim for.
    const confirm = screen.getByRole("button", { name: "Unregister layer" });
    const foot = confirm.parentElement as HTMLElement;
    expect(
      within(foot)
        .getAllByRole("button")
        .map((button) => button.textContent),
    ).toEqual(["Cancel", "Unregister layer"]);
    expect(confirm.className).toContain("danger");
  });

  // Every other key-and-value pair in the build — the artifact rail's
  // provenance and the resource detail — is a borderless list whose key is a
  // quiet lowercase mono label. Drawn as a single-row table the confirmation's
  // visibility pair took the user agent's bold `th` in the UI face and read as
  // a table header standing over the dialog rather than as a label beside the
  // grants it names.
  it("states the visibility in the unregister confirmation as a quiet mono-keyed borderless pair", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [adminLayer(), userLayer()] } },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    openRowActions();
    fireEvent.click(screen.getByRole("menuitem", { name: "Unregister" }));
    await screen.findByLabelText("Unregister alice-personal");
    const properties = screen.getByTestId("unregister-properties");
    // The pair is a labelled list, so it carries no table and no bordered
    // table container.
    expect(properties.tagName).toBe("DL");
    expect(properties.closest("table")).toBeNull();
    expect(properties.className.split(" ")).toContain("rail-facts");
    const rows = [...properties.querySelectorAll(".rail-fact")].map((row) => [
      row.querySelector("dt")?.textContent,
      row.querySelector("dd")?.textContent,
    ]);
    expect(rows).toEqual([["visibility", "no grants — only you"]]);
    // The key takes the mono face the rail's keys take, which is what
    // separates a label from a heading here.
    const key = properties.querySelector("dt") as HTMLElement;
    expect(key.className.split(" ")).toContain("mono");
  });

  // A dialog that leaves focus on the surface it covers puts a keyboard
  // reader on controls the scrim has hidden, and one that closes without
  // handing focus back drops them at the top of the document. The row's
  // overflow trigger is what the reader gets back, because the menu item that
  // opened the dialog has left the document by then.
  it("takes focus into the unregister confirmation, cycles Tab inside it, and hands focus back on cancel", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [adminLayer(), userLayer()] } },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    openRowActions();
    fireEvent.click(screen.getByRole("menuitem", { name: "Unregister" }));
    const dialog = await screen.findByLabelText("Unregister alice-personal");
    expect(dialog.contains(document.activeElement)).toBe(true);
    const stops = Array.from(
      dialog.querySelectorAll<HTMLElement>(
        "button:not([disabled]), input:not([disabled])",
      ),
    );
    stops[stops.length - 1].focus();
    fireEvent.keyDown(document, { key: "Tab" });
    expect(document.activeElement).toBe(stops[0]);
    fireEvent.keyDown(document, { key: "Tab", shiftKey: true });
    expect(document.activeElement).toBe(stops[stops.length - 1]);
    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(screen.queryByLabelText("Unregister alice-personal")).toBeNull();
    expect(document.activeElement).toBe(
      screen.getByRole("button", { name: "More actions for alice-personal" }),
    );
  });

  // The write that removes the control it was started from is the one that
  // strands a keyboard reader: the dialog closes, the row it was opened from
  // leaves the table, and focus falls to the document body with nothing said
  // about what happened. Focus lands on the panel heading, and the panel
  // states the outcome the vanished row can no longer state.
  it("hands focus to the panel heading and states the outcome when an unregister takes the row away", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "DELETE /v1/layers": { body: {} },
      "/v1/layers?deleted=true": { body: { layers: [] } },
      "/v1/layers": { body: { layers: [adminLayer(), userLayer()] } },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    openRowActions();
    fireEvent.click(screen.getByRole("menuitem", { name: "Unregister" }));
    await screen.findByLabelText("Unregister alice-personal");
    fireEvent.change(screen.getByLabelText("Type the layer ID to confirm"), {
      target: { value: "alice-personal" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Unregister layer" }));
    await waitFor(() => {
      expect(document.activeElement).toBe(
        screen.getByRole("heading", { name: "Layers" }),
      );
    });
    expect(screen.getByTestId("panel-announcement").textContent).toContain(
      "alice-personal is unregistered.",
    );
  });

  // The row's overflow menu is a transient popup, and every other overlay in
  // the shell leaves on Escape. A popup whose only exit is its own trigger
  // strands a reader who opened it to look, and one that survives a press
  // elsewhere leaves stale menus stacked over rows the reader has moved on
  // from.
  it("dismisses the row actions on Escape, on an outside press, and when another row's actions open", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [adminLayer(), userLayer()] } },
    });
    goTo("#/layers");
    render(<App />);
    const panel = await screen.findByLabelText("Layer panel");
    const trigger = screen.getByRole("button", {
      name: "More actions for alice-personal",
    });
    // The trigger carries the same label as the menu it opens, so the open
    // menus are read off the popups themselves.
    const openMenus = () =>
      Array.from(document.querySelectorAll(".row-menu")).map((menu) =>
        menu.getAttribute("aria-label"),
      );

    openRowActions();
    expect(trigger.getAttribute("aria-expanded")).toBe("true");
    fireEvent.keyDown(document, { key: "Escape" });
    expect(openMenus()).toEqual([]);
    expect(document.activeElement).toBe(trigger);

    openRowActions();
    fireEvent.pointerDown(panel);
    expect(openMenus()).toEqual([]);

    // Only one row's actions are open at a time: the press that opens the
    // second row's menu is a press outside the first.
    openRowActions();
    const other = screen.getByRole("button", {
      name: "More actions for company",
    });
    fireEvent.pointerDown(other);
    fireEvent.click(other);
    expect(openMenus()).toEqual(["More actions for company"]);
  });

  // A control that reports aria-expanded has to say what it expands, and the
  // popup behind it has to carry the semantics that make its label readable.
  // A bare div holding two plain buttons announces as neither a menu nor a
  // set of items, drops the label it states, and leaves a forward Tab as the
  // only route into a popup the reader has just opened.
  it("opens the row actions as a menu, with focus on the first item and the arrows moving between them", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [userLayer()] } },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    const trigger = screen.getByRole("button", {
      name: "More actions for alice-personal",
    });
    expect(trigger.getAttribute("aria-haspopup")).toBe("menu");

    openRowActions();
    const menu = screen.getByRole("menu", {
      name: "More actions for alice-personal",
    });
    const items = within(menu).getAllByRole("menuitem");
    expect(items.map((item) => item.textContent)).toEqual([
      "Edit",
      "Unregister",
    ]);
    // The menu is one Tab stop and opens inside itself.
    expect(document.activeElement).toBe(items[0]);
    expect(items.map((item) => item.getAttribute("tabindex"))).toEqual([
      "0",
      "-1",
    ]);

    fireEvent.keyDown(menu, { key: "ArrowDown" });
    expect(document.activeElement).toBe(items[1]);
    expect(items.map((item) => item.getAttribute("tabindex"))).toEqual([
      "-1",
      "0",
    ]);
    // The arrows wrap, so neither end of the menu is a dead stop.
    fireEvent.keyDown(menu, { key: "ArrowDown" });
    expect(document.activeElement).toBe(items[0]);
    fireEvent.keyDown(menu, { key: "ArrowUp" });
    expect(document.activeElement).toBe(items[1]);
    fireEvent.keyDown(menu, { key: "Home" });
    expect(document.activeElement).toBe(items[0]);
    fireEvent.keyDown(menu, { key: "End" });
    expect(document.activeElement).toBe(items[1]);
  });

  // The menu overlays the table rather than taking space in it. Drawn in the
  // flow of the row's fixed-width actions cell it stretched that row to the
  // height of the menu, emptied every other cell in the row over that height,
  // and pushed every row below it down the page, so a reader who opened a
  // menu lost the row they were reading. jsdom performs no layout, so the
  // case reads where the menu is drawn: outside the table, which is what
  // leaves the table's geometry unchanged while a menu is open.
  it("draws the row actions outside the table so the rows below stay where they were", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [adminLayer(), userLayer()] } },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    openRowActions();
    const menu = screen.getByRole("menu", {
      name: "More actions for alice-personal",
    });
    const table = document.querySelector(".layer-table");
    expect(table).not.toBeNull();
    expect(table?.contains(menu)).toBe(false);
    // The row it belongs to still holds every cell the table draws.
    const row = screen
      .getByRole("button", { name: "More actions for alice-personal" })
      .closest("tr");
    expect(row?.querySelectorAll("td").length).toBe(6);
    expect(row?.contains(menu)).toBe(false);
  });

  // A dialog opens with focus on the field the reader has to fill in. Opening
  // focus on the dismissal ✕ made the first Enter close the dialog the reader
  // had just opened, and put the destructive confirmation's opening focus on
  // a second way to cancel.
  it("opens each dialog with focus on its first field rather than on the dismissal control", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [adminLayer(), userLayer()] } },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Register layer" }));
    await screen.findByTestId("register-form");
    expect(document.activeElement).toBe(screen.getByLabelText("Layer ID"));
    // The dismissal stays reachable by Tab from there.
    const close = screen.getByRole("button", { name: "Close" });
    expect(close.hasAttribute("disabled")).toBe(false);
    fireEvent.click(close);
    openRowActions();
    fireEvent.click(screen.getByRole("menuitem", { name: "Unregister" }));
    const dialog = await screen.findByLabelText("Unregister alice-personal");
    expect(document.activeElement).toBe(
      within(dialog).getByLabelText("Type the layer ID to confirm"),
    );
  });

  // §13.10 makes the panel the surface a user manages their own user-defined
  // layers on, which is the class §7.3.1 caps per user and authorizes its
  // owner on, so that is the class the form registers by default. The
  // registry fixes such a layer's visibility to the registrant and discards
  // what the request carries, so the axes are absent on that class.
  it("registers the caller’s own layer as user-defined and offers it no visibility axes", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": {
        body: {
          layer: {
            ID: "alice-personal",
            SourceType: "local",
            Order: 1,
            UserDefined: true,
          },
        },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Register layer" }));
    fireEvent.change(screen.getByLabelText("Layer ID"), {
      target: { value: "alice-personal" },
    });
    expect(screen.queryByLabelText("Organization")).toBeNull();
    expect(screen.queryByLabelText("Public")).toBeNull();
    fireEvent.submit(screen.getByTestId("register-form"));
    await waitFor(() => {
      expect(
        requests.some((r) => r.url === "/v1/layers" && r.method === "POST"),
      ).toBe(true);
    });
    const sent = JSON.parse(bodies.at(-1) ?? "{}") as Record<string, unknown>;
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
  it("opens on the tenant’s class where the posture read resolved no subject", async () => {
    stubRegistry({
      "/v1/ui/session": {
        body: posture({ identity_provider_configured: false }),
      },
      "/v1/layers": {
        body: { layer: { ID: "company", SourceType: "local", Order: 1 } },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Register layer" }));
    fireEvent.change(screen.getByLabelText("Layer ID"), {
      target: { value: "company" },
    });
    expect(screen.getByLabelText("Organization")).toBeTruthy();
    fireEvent.submit(screen.getByTestId("register-form"));
    await waitFor(() => {
      expect(
        requests.some((r) => r.url === "/v1/layers" && r.method === "POST"),
      ).toBe(true);
    });
    const sent = JSON.parse(bodies.at(-1) ?? "{}") as Record<string, unknown>;
    expect(sent.user_defined).toBe(false);
  });

  // A browser draws a select and a checkbox from the operating system
  // palette unless the page overrides it, which left the register form with
  // a white select and a white checkbox on a dark surface. The design brief
  // requires every surface to read in both themes off one token set, so the
  // select carries the same border treatment as the text input beside it and
  // the checkbox takes its tick from the accent token.
  it("draws the register form’s select and checkboxes off the token set rather than as native widgets", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": {
        body: { layer: { ID: "company", SourceType: "local", Order: 1 } },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Register layer" }));
    fireEvent.change(screen.getByLabelText("Layer class"), {
      target: { value: "admin" },
    });
    const text = window.getComputedStyle(screen.getByLabelText("Layer ID"));
    const select = window.getComputedStyle(
      screen.getByLabelText("Layer class"),
    );
    expect(select.borderRadius).toBe(text.borderRadius);
    expect(select.borderTopWidth).toBe(text.borderTopWidth);
    expect(select.appearance).toBe("none");
    const box = window.getComputedStyle(screen.getByLabelText("Organization"));
    expect(box.accentColor).toBe("var(--acc)");
    // The text-input rule pads and fills the control, which is the wrong
    // treatment for a checkbox and is what it used to inherit here.
    expect(box.padding).not.toBe(text.padding);
    expect(box.width).toBe("15px");
  });

  // §4.6 defines visibility as independent grants that combine as a union.
  // They are honoured on an admin-defined layer, which is the class the form
  // offers them on.
  it("registers an admin-defined layer on every visibility axis", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": {
        body: { layer: { ID: "company", SourceType: "local", Order: 1 } },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Register layer" }));
    fireEvent.change(screen.getByLabelText("Layer ID"), {
      target: { value: "company" },
    });
    fireEvent.change(screen.getByLabelText("Layer class"), {
      target: { value: "admin" },
    });
    fireEvent.click(screen.getByLabelText("Organization"));
    fireEvent.click(screen.getByLabelText("Groups"));
    // An axis selected with no member named registers a grant admitting
    // nobody, so the write is held until each selected axis carries one.
    expect(
      screen.getByRole("button", { name: "Register" }).hasAttribute("disabled"),
    ).toBe(true);
    fireEvent.change(
      screen.getByLabelText("Group names, separated by commas"),
      {
        target: { value: "secops, appsec" },
      },
    );
    fireEvent.click(screen.getByLabelText("Specific users"));
    fireEvent.change(
      screen.getByLabelText("User identifiers, separated by commas"),
      {
        target: { value: "carol@acme.com" },
      },
    );
    fireEvent.submit(screen.getByTestId("register-form"));
    await waitFor(() => {
      expect(
        requests.some((r) => r.url === "/v1/layers" && r.method === "POST"),
      ).toBe(true);
    });
    const sent = JSON.parse(bodies.at(-1) ?? "{}") as Record<string, unknown>;
    expect(sent.user_defined).toBe(false);
    expect(sent.organization).toBe(true);
    expect(sent.groups).toEqual(["secops", "appsec"]);
    expect(sent.users).toEqual(["carol@acme.com"]);
  });

  // §13.10: a registration that names no grant is not stored ungranted. The
  // registry stamps the deployment's default visibility, which is public on a
  // standalone with no identity provider, so the dialog must not promise the
  // reader an owner-only layer before the row underneath reports `public`.
  it("names the stamped default where an admin-defined registration grants nobody", async () => {
    stubRegistry({
      "/v1/ui/session": {
        body: posture({ identity_provider_configured: false }),
      },
      "/v1/layers": {
        body: { layer: { ID: "ops", SourceType: "local", Order: 1 } },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Register layer" }));
    const line = screen.getByTestId("visibility-consequence");
    expect(line.querySelector(".consequence-text")?.textContent).toBe(
      "No grants — the registry stamps this deployment's default visibility, " +
        "which is public on a standalone with no identity provider. The registered row states what it applied.",
    );
    expect(line.textContent).not.toContain("only you");
    // The line describes the write the form is about to send: no axis is
    // carried, which is the request the registry applies the default to.
    fireEvent.change(screen.getByLabelText("Layer ID"), {
      target: { value: "ops" },
    });
    fireEvent.click(
      within(screen.getByRole("radiogroup", { name: "Source" })).getByRole(
        "radio",
        { name: "Local folder" },
      ),
    );
    fireEvent.change(screen.getByLabelText("Local path"), {
      target: { value: "/srv/ops" },
    });
    fireEvent.submit(screen.getByTestId("register-form"));
    await waitFor(() => {
      expect(
        requests.some((r) => r.url === "/v1/layers" && r.method === "POST"),
      ).toBe(true);
    });
    const sent = JSON.parse(bodies.at(-1) ?? "{}") as Record<string, unknown>;
    expect(sent.public).toBe(false);
    expect(sent.organization).toBe(false);
    expect(sent.groups).toBeUndefined();
    expect(sent.users).toBeUndefined();
  });

  // A git layer whose artifacts live under a subdirectory is registered by
  // naming that subtree as the root, which is the field the git source reads
  // to scope the fetch. Without the control on the form such a repository
  // cannot be registered from the browser at all.
  it("registers a git layer under the subtree the root names", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": {
        body: { layer: { ID: "catalog", SourceType: "git", Order: 1 } },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Register layer" }));
    fireEvent.change(screen.getByLabelText("Layer ID"), {
      target: { value: "catalog" },
    });
    fireEvent.change(screen.getByLabelText("Repository"), {
      target: { value: "git@github.com:acme/catalog.git" },
    });
    fireEvent.change(screen.getByLabelText("Ref"), {
      target: { value: "main" },
    });
    fireEvent.change(screen.getByLabelText("Root"), {
      target: { value: "artifacts" },
    });
    fireEvent.submit(screen.getByTestId("register-form"));
    await waitFor(() => {
      expect(
        requests.some((r) => r.url === "/v1/layers" && r.method === "POST"),
      ).toBe(true);
    });
    const sent = JSON.parse(bodies.at(-1) ?? "{}") as Record<string, unknown>;
    expect(sent.repo).toBe("git@github.com:acme/catalog.git");
    expect(sent.ref).toBe("main");
    expect(sent.root).toBe("artifacts");
  });

  // The root qualifies a git repository alone, so a local source offers no
  // such field and sends none.
  it("offers no root on a local source", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": {
        body: {
          layer: { ID: "alice-personal", SourceType: "local", Order: 1 },
        },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Register layer" }));
    fireEvent.click(screen.getByRole("radio", { name: "Local folder" }));
    expect(screen.queryByLabelText("Root")).toBeNull();
    fireEvent.change(screen.getByLabelText("Layer ID"), {
      target: { value: "alice-personal" },
    });
    fireEvent.change(screen.getByLabelText("Local path"), {
      target: { value: "/Users/alice/reg" },
    });
    fireEvent.submit(screen.getByTestId("register-form"));
    await waitFor(() => {
      expect(
        requests.some((r) => r.url === "/v1/layers" && r.method === "POST"),
      ).toBe(true);
    });
    const sent = JSON.parse(bodies.at(-1) ?? "{}") as Record<string, unknown>;
    expect(sent.root).toBeUndefined();
  });

  // §4.6: a git layer reads its tree from a repository, and the registry's
  // source validator refuses one without it on every ingest with
  // "git source requires repo". The registration itself is accepted, so a form
  // that sends it leaves a layer that can never ingest, and the repository is
  // held on exactly as the ref beside it is.
  it("holds the register submit on a git source with no repository", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": {
        body: { layer: { ID: "ops", SourceType: "git", Order: 1 } },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Register layer" }));
    const dialog = screen.getByRole("dialog", { name: "Register a layer" });
    fireEvent.change(screen.getByLabelText("Layer ID"), {
      target: { value: "git-layer" },
    });
    fireEvent.change(screen.getByLabelText("Ref"), {
      target: { value: "main" },
    });
    // The repository carries the same visible requirement marker the ref does.
    expect(screen.getByTestId("register-repo-required").textContent).toBe(
      "required",
    );
    expect(
      screen.getByLabelText("Repository").getAttribute("aria-required"),
    ).toBe("true");
    const register = within(dialog).getByRole("button", { name: "Register" });
    const note = screen.getByTestId("register-foot-note");
    expect(register.hasAttribute("disabled")).toBe(true);
    expect(note.textContent).toContain(
      "Name the repository before registering.",
    );
    expect(register.getAttribute("aria-describedby")).toBe(note.id);
    // The field the hold stands on reports itself invalid once the reader has
    // been in it and left it empty.
    fireEvent.blur(screen.getByLabelText("Repository"));
    expect(
      screen.getByLabelText("Repository").getAttribute("aria-invalid"),
    ).toBe("true");
    // Naming the repository releases the hold and sends the registration.
    fireEvent.change(screen.getByLabelText("Repository"), {
      target: { value: "https://github.com/alice/catalog.git" },
    });
    expect(register.hasAttribute("disabled")).toBe(false);
    expect(screen.getByTestId("register-foot-note").textContent).toContain(
      "Registers at the end of the order",
    );
  });

  // The dialog opens with every required field empty, so a hold stands before
  // the reader has typed anything. Stating it then would open the form on a
  // sentence in the refusal colour, reading as an error the reader has already
  // caused. The footer keeps its standing note until the reader has begun.
  it("opens the register dialog on its standing note rather than a hold", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": {
        body: { layer: { ID: "ops", SourceType: "git", Order: 1 } },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Register layer" }));
    const dialog = screen.getByRole("dialog", { name: "Register a layer" });
    const register = within(dialog).getByRole("button", { name: "Register" });
    const note = screen.getByTestId("register-foot-note");
    expect(register.hasAttribute("disabled")).toBe(true);
    expect(note.textContent).toContain("Registers at the end of the order");
    expect(note.className).not.toContain("modal-foot-hold");
    expect(register.getAttribute("aria-describedby")).toBe(null);
    const layerID = screen.getByLabelText("Layer ID");
    expect(layerID.getAttribute("aria-describedby")).toBe(null);
    // Once the reader has been in the form, the standing hold is named, in the
    // refusal colour and on the field it stands on.
    fireEvent.change(layerID, { target: { value: "git-layer" } });
    fireEvent.change(layerID, { target: { value: "" } });
    const held = screen.getByTestId("register-foot-note");
    expect(held.textContent).toContain("Name the layer ID before registering.");
    expect(held.className).toContain("modal-foot-hold");
    expect(register.getAttribute("aria-describedby")).toBe(held.id);
    expect(layerID.getAttribute("aria-describedby")).toBe(held.id);
  });

  // A disabled submit reports no reason of its own, and the field the hold is
  // on scrolls out of view once the body is scrolled to the submit row, so the
  // form names the field holding the submit beside the control and marks the
  // field itself as required.
  it("names the field holding the register submit", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": {
        body: { layer: { ID: "ops", SourceType: "git", Order: 1 } },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Register layer" }));
    const dialog = screen.getByRole("dialog", { name: "Register a layer" });
    fireEvent.change(screen.getByLabelText("Layer ID"), {
      target: { value: "git-layer" },
    });
    fireEvent.change(screen.getByLabelText("Repository"), {
      target: { value: "https://github.com/alice/catalog.git" },
    });
    // The ref carries a visible requirement marker and the input says so to a
    // reader who is not looking at it.
    expect(screen.getByTestId("register-ref-required").textContent).toBe(
      "required",
    );
    expect(screen.getByLabelText("Ref").getAttribute("aria-required")).toBe(
      "true",
    );
    // The footer note names the ref while the submit is held, and the submit
    // points at that note.
    const register = within(dialog).getByRole("button", { name: "Register" });
    const note = screen.getByTestId("register-foot-note");
    expect(register.hasAttribute("disabled")).toBe(true);
    expect(note.textContent).toContain("Name the ref before registering.");
    expect(register.getAttribute("aria-describedby")).toBe(note.id);
    // Naming the ref releases the hold, and the note goes back to stating
    // where the registration lands.
    fireEvent.change(screen.getByLabelText("Ref"), {
      target: { value: "main" },
    });
    expect(register.hasAttribute("disabled")).toBe(false);
    expect(register.getAttribute("aria-describedby")).toBe(null);
    expect(screen.getByTestId("register-foot-note").textContent).toContain(
      "Registers at the end of the order",
    );
    // The local arm holds on its own field and names that one instead.
    fireEvent.click(screen.getByRole("radio", { name: "Local folder" }));
    expect(screen.getByTestId("register-local-path-required")).toBeTruthy();
    expect(
      screen.getByLabelText("Local path").getAttribute("aria-required"),
    ).toBe("true");
    expect(screen.getByTestId("register-foot-note").textContent).toContain(
      "Name the local path before registering.",
    );
  });

  // §4.6 keys a layer on its ID and the registration writes that key, so
  // reusing the ID of a registered layer rewrites it: the stored layer takes a
  // new place at the end of the order and its last ingest is cleared, while
  // the dialog reports plain success. The panel already lists the IDs, so the
  // form holds the submit on a reused one and names the layer it would
  // overwrite.
  it("holds the register submit on a layer ID that is already registered", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [adminLayer(), userLayer()] } },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Register layer" }));
    const dialog = screen.getByRole("dialog", { name: "Register a layer" });
    fireEvent.change(screen.getByLabelText("Layer ID"), {
      target: { value: "company" },
    });
    fireEvent.change(screen.getByLabelText("Repository"), {
      target: { value: "https://github.com/acme/company.git" },
    });
    fireEvent.change(screen.getByLabelText("Ref"), {
      target: { value: "main" },
    });
    const register = within(dialog).getByRole("button", { name: "Register" });
    const note = screen.getByTestId("register-foot-note");
    expect(register.hasAttribute("disabled")).toBe(true);
    expect(note.textContent).toContain("Layer company is already registered.");
    expect(register.getAttribute("aria-describedby")).toBe(note.id);
    // The hold stands on the ID field, which reports itself invalid once the
    // reader has left it.
    fireEvent.blur(screen.getByLabelText("Layer ID"));
    expect(
      screen.getByLabelText("Layer ID").getAttribute("aria-invalid"),
    ).toBe("true");
    // An unused ID releases the hold.
    fireEvent.change(screen.getByLabelText("Layer ID"), {
      target: { value: "company-archive" },
    });
    expect(register.hasAttribute("disabled")).toBe(false);
    expect(screen.getByTestId("register-foot-note").textContent).toContain(
      "Registers at the end of the order",
    );
  });

  // The guidance under the fields is explanatory text about the controls
  // beside it. Left at the body size it is the longest and largest run of
  // text in the dialog, louder than the field labels and the checkbox titles
  // it explains, so it takes the dense size the form's other helper strings
  // are set at.
  it("sets the register form's field guidance at the dense size", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": {
        body: { layer: { ID: "ops", SourceType: "git", Order: 1 } },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Register layer" }));
    const gitNote = screen.getByTestId("register-git-note");
    expect(window.getComputedStyle(gitNote).fontSize).toBe("12.5px");
    // The class note stands on the same footing on the user-defined arm.
    const classNote = screen.getByText(/A layer of your own is visible/);
    expect(window.getComputedStyle(classNote).fontSize).toBe("12.5px");
    // The local arm carries the same guidance about its own field.
    fireEvent.click(screen.getByRole("radio", { name: "Local folder" }));
    const localNote = screen.getByTestId("register-local-note");
    expect(window.getComputedStyle(localNote).fontSize).toBe("12.5px");
  });

  // The sentence stating the hold sits in the footer, and a reader who tabs
  // into the field it names never reaches it. The field the hold stands on
  // therefore reports itself invalid and points at that same sentence, so the
  // refusal arrives on the control it applies to.
  it("marks the field holding the register submit invalid", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": {
        body: { layer: { ID: "ops", SourceType: "git", Order: 1 } },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Register layer" }));
    const note = screen.getByTestId("register-foot-note");
    // The ID and the repository hold the submit first, so both are named
    // before the ref becomes the field the hold stands on.
    fireEvent.change(screen.getByLabelText("Layer ID"), {
      target: { value: "git-layer" },
    });
    fireEvent.change(screen.getByLabelText("Repository"), {
      target: { value: "https://github.com/alice/catalog.git" },
    });
    const ref = screen.getByLabelText("Ref");
    fireEvent.blur(ref);
    expect(ref.getAttribute("aria-invalid")).toBe("true");
    expect(ref.getAttribute("aria-describedby")).toBe(note.id);
    // Naming the ref releases the hold, and the field stops reporting itself
    // as the one refused.
    fireEvent.change(ref, { target: { value: "main" } });
    expect(ref.getAttribute("aria-invalid")).toBe(null);
    expect(ref.getAttribute("aria-describedby")).toBe(null);
    // The local arm carries the same association on its own field.
    fireEvent.click(screen.getByRole("radio", { name: "Local folder" }));
    const localPath = screen.getByLabelText("Local path");
    fireEvent.blur(localPath);
    expect(localPath.getAttribute("aria-invalid")).toBe("true");
    expect(localPath.getAttribute("aria-describedby")).toBe(
      screen.getByTestId("register-foot-note").id,
    );
    // A selected visibility axis with no member named holds the submit on its
    // member field, and that field carries the association too.
    fireEvent.change(screen.getByLabelText("Local path"), {
      target: { value: "/Users/alice/reg" },
    });
    fireEvent.change(screen.getByLabelText("Layer class"), {
      target: { value: "admin" },
    });
    fireEvent.click(screen.getByRole("checkbox", { name: "Groups" }));
    const groupField = screen.getByLabelText(
      "Group names, separated by commas",
    );
    fireEvent.blur(groupField);
    expect(groupField.getAttribute("aria-invalid")).toBe("true");
    expect(groupField.getAttribute("aria-describedby")).toBe(
      screen.getByTestId("register-foot-note").id,
    );
  });

  // The dialog opens with every required field empty, and the ID takes focus.
  // A field that reported itself invalid from the hold alone would announce a
  // refusal on a form the reader has not begun to fill in. The mark waits
  // until the reader has been in the field and left it empty.
  it("leaves a pristine required field unmarked until it is left empty", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": {
        body: { layer: { ID: "ops", SourceType: "git", Order: 1 } },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Register layer" }));
    const layerID = screen.getByLabelText("Layer ID");
    const note = screen.getByTestId("register-foot-note");
    // The requirement reaches the pristine field through the marker and
    // `aria-required`, and neither the field nor the footer announces a
    // refusal on a form the reader has not begun to fill in.
    expect(note.textContent).toContain("Registers at the end of the order");
    expect(layerID.getAttribute("aria-required")).toBe("true");
    expect(layerID.getAttribute("aria-invalid")).toBe(null);
    // Leaving the field still empty is what marks it, and the footer names the
    // hold from the same point.
    fireEvent.blur(layerID);
    expect(layerID.getAttribute("aria-invalid")).toBe("true");
    expect(screen.getByTestId("register-foot-note").textContent).toContain(
      "Name the layer ID before registering.",
    );
    // A pristine ref carries no mark either, and it is the field the hold
    // moves to once the ID is named.
    fireEvent.change(layerID, { target: { value: "alice-personal" } });
    expect(layerID.getAttribute("aria-invalid")).toBe(null);
    expect(screen.getByLabelText("Ref").getAttribute("aria-invalid")).toBe(
      null,
    );
  });

  // A layer is addressed by its ID, and a registration without one is refused
  // by the registry with a message naming `source_type`, a field the form
  // never draws. The ID therefore carries the same requirement marker and the
  // same hold the ref and the local path carry, on every source type.
  it("holds a registration until the layer ID is named", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": {
        body: { layer: { ID: "ops", SourceType: "git", Order: 1 } },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Register layer" }));
    const dialog = screen.getByRole("dialog", { name: "Register a layer" });
    const register = within(dialog).getByRole("button", { name: "Register" });
    const note = screen.getByTestId("register-foot-note");
    const layerID = screen.getByLabelText("Layer ID");
    expect(screen.getByTestId("register-id-required").textContent).toBe(
      "required",
    );
    expect(layerID.getAttribute("aria-required")).toBe("true");
    // The local arm's own field filled, the submit is still held, and the
    // footer and the ID field both name the ID as what is holding it.
    fireEvent.click(screen.getByRole("radio", { name: "Local folder" }));
    fireEvent.change(screen.getByLabelText("Local path"), {
      target: { value: "/Users/alice/reg" },
    });
    expect(register.hasAttribute("disabled")).toBe(true);
    expect(note.textContent).toContain("Name the layer ID before registering.");
    expect(register.getAttribute("aria-describedby")).toBe(note.id);
    fireEvent.blur(layerID);
    expect(layerID.getAttribute("aria-invalid")).toBe("true");
    expect(layerID.getAttribute("aria-describedby")).toBe(note.id);
    // Naming the ID releases the hold on every source type.
    fireEvent.change(layerID, { target: { value: "alice-personal" } });
    expect(register.hasAttribute("disabled")).toBe(false);
    expect(layerID.getAttribute("aria-invalid")).toBe(null);
    expect(screen.getByTestId("register-foot-note").textContent).toContain(
      "Registers at the end of the order",
    );
    // The git arm holds on the ID just the same, and whitespace alone does
    // not name one.
    fireEvent.click(screen.getByRole("radio", { name: "Git repository" }));
    fireEvent.change(screen.getByLabelText("Ref"), {
      target: { value: "main" },
    });
    fireEvent.change(layerID, { target: { value: "   " } });
    expect(register.hasAttribute("disabled")).toBe(true);
    expect(screen.getByTestId("register-foot-note").textContent).toContain(
      "Name the layer ID before registering.",
    );
  });

  // §4.6: the git source resolves its tree at the ref and has no default, so
  // a git layer registered with the ref blank is accepted, issues its
  // one-time secret, takes a place in the order, and is then refused on
  // every ingest with "git source requires ref". The form holds the write
  // until the ref is named, and a local source is unaffected by the hold.
  it("holds a git registration until the ref is named", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": {
        body: { layer: { ID: "ops", SourceType: "git", Order: 1 } },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Register layer" }));
    const dialog = screen.getByRole("dialog", { name: "Register a layer" });
    fireEvent.change(screen.getByLabelText("Layer ID"), {
      target: { value: "ops" },
    });
    fireEvent.change(screen.getByLabelText("Repository"), {
      target: { value: "git@github.com:acme/ops.git" },
    });
    const register = within(dialog).getByRole("button", { name: "Register" });
    expect(register.hasAttribute("disabled")).toBe(true);
    // Whitespace names no ref either.
    fireEvent.change(screen.getByLabelText("Ref"), { target: { value: "  " } });
    expect(register.hasAttribute("disabled")).toBe(true);
    // A local source reads no ref, so the ref the git arm holds on does not
    // travel with the reader; the local arm holds on its own field instead.
    fireEvent.click(screen.getByRole("radio", { name: "Local folder" }));
    fireEvent.change(screen.getByLabelText("Local path"), {
      target: { value: "/Users/alice/reg" },
    });
    expect(register.hasAttribute("disabled")).toBe(false);
    fireEvent.click(screen.getByRole("radio", { name: "Git repository" }));
    expect(register.hasAttribute("disabled")).toBe(true);
    fireEvent.change(screen.getByLabelText("Ref"), {
      target: { value: "main" },
    });
    expect(register.hasAttribute("disabled")).toBe(false);
    fireEvent.submit(screen.getByTestId("register-form"));
    await waitFor(() => {
      expect(
        requests.some((r) => r.url === "/v1/layers" && r.method === "POST"),
      ).toBe(true);
    });
    const sent = JSON.parse(bodies.at(-1) ?? "{}") as Record<string, unknown>;
    expect(sent.ref).toBe("main");
  });

  // §4.6: a local source reads its tree from the named directory and has no
  // default, so a local layer registered with the path blank is accepted,
  // takes a place in the order, and is then refused on every ingest with
  // "local source requires path". The form holds the write until the path is
  // named, on the same terms as the git arm's ref.
  it("holds a local registration until the path is named", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": {
        body: { layer: { ID: "ops", SourceType: "local", Order: 1 } },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Register layer" }));
    const dialog = screen.getByRole("dialog", { name: "Register a layer" });
    fireEvent.change(screen.getByLabelText("Layer ID"), {
      target: { value: "ops" },
    });
    fireEvent.click(screen.getByRole("radio", { name: "Local folder" }));
    const register = within(dialog).getByRole("button", { name: "Register" });
    expect(register.hasAttribute("disabled")).toBe(true);
    // Whitespace names no directory either.
    fireEvent.change(screen.getByLabelText("Local path"), {
      target: { value: "  " },
    });
    expect(register.hasAttribute("disabled")).toBe(true);
    fireEvent.change(screen.getByLabelText("Local path"), {
      target: { value: "/Users/alice/reg" },
    });
    expect(register.hasAttribute("disabled")).toBe(false);
    fireEvent.submit(screen.getByTestId("register-form"));
    await waitFor(() => {
      expect(
        requests.some((r) => r.url === "/v1/layers" && r.method === "POST"),
      ).toBe(true);
    });
    const sent = JSON.parse(bodies.at(-1) ?? "{}") as Record<string, unknown>;
    expect(sent.local_path).toBe("/Users/alice/reg");
  });

  // A git row carrying no ref cannot ingest at all, so the source cell names
  // the missing ref rather than reading it as a default branch the registry
  // does not implement.
  it("names a git row's missing ref rather than asserting a default branch", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/layers": {
        body: {
          layers: [
            {
              ID: "ops",
              SourceType: "git",
              Repo: "git@github.com:acme/ops.git",
              Ref: "",
              Order: 1,
              Public: true,
            },
          ],
        },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    const cell = layerRow("ops").querySelector(".source-cell");
    expect(cell?.textContent).not.toContain("default branch");
    expect(cell?.textContent).toContain("no ref");
  });

  // §13.10 puts the layer panel's writes on the panel, and a registration is
  // reviewed before it is sent, so the form is a dialog over a scrim with the
  // panel underneath keeping its position. The inline panel it replaced named
  // itself nowhere, offered no way out but submitting, and left every grant
  // as a bare word.
  it("opens the registration as a dialog over a scrim that names itself and can be left", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [] } },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Register layer" }));
    const dialog = screen.getByRole("dialog", { name: "Register a layer" });
    expect(dialog.getAttribute("aria-modal")).toBe("true");
    expect(screen.getByTestId("modal-scrim")).toBeTruthy();
    expect(
      within(dialog).getByText(/A layer points at a source Podium ingests/),
    ).toBeTruthy();
    // A user-defined layer's visibility is fixed at registration, which is
    // the class this caller opens on.
    expect(
      screen.getByTestId("visibility-note").querySelector(".note-text")
        ?.textContent,
    ).toBe("Visibility is fixed at registration.");
    expect(
      within(dialog).getByRole("button", { name: "Register" }).className,
    ).toContain("primary");
    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(
      screen.queryByRole("dialog", { name: "Register a layer" }),
    ).toBeNull();
  });

  // The source types are two exclusive choices that fit on one row, and the
  // grants are terms a reader cannot act on from the axis name alone, so the
  // source is a segmented control and each grant is a card stating who it
  // admits with the consequence of the whole selection stated once.
  it("draws the source as a segmented control and each grant as a described card", async () => {
    stubRegistry({
      "/v1/ui/session": {
        body: posture({ identity_provider_configured: false }),
      },
      "/v1/layers": { body: { layers: [] } },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Register layer" }));
    const source = screen.getByRole("radiogroup", { name: "Source" });
    const git = within(source).getByRole("radio", { name: "Git repository" });
    const local = within(source).getByRole("radio", { name: "Local folder" });
    expect(git.getAttribute("aria-checked")).toBe("true");
    fireEvent.click(local);
    expect(local.getAttribute("aria-checked")).toBe("true");
    expect(screen.getByLabelText("Local path")).toBeTruthy();
    for (const [axis, description] of [
      ["Public", "Anyone, signed in or not."],
      ["Organization", "Everyone in this tenant."],
      ["Groups", "Members of the OIDC groups you name."],
      ["Specific users", "Named individuals, by email."],
    ]) {
      const box = screen.getByLabelText(axis);
      const describedBy = box.getAttribute("aria-describedby") ?? "";
      expect(document.getElementById(describedBy)?.textContent).toBe(
        description,
      );
    }
    // The consequence of the whole selection, stated in the reviewer's terms.
    expect(
      screen
        .getByTestId("visibility-consequence")
        .querySelector(".consequence-text")?.textContent,
    ).toBe(
      "No grants — the registry stamps this deployment's default visibility, " +
        "which is public on a standalone with no identity provider. The registered row states what it applied.",
    );
    fireEvent.click(screen.getByLabelText("Organization"));
    fireEvent.click(screen.getByLabelText("Groups"));
    fireEvent.change(
      screen.getByLabelText("Group names, separated by commas"),
      {
        target: { value: "secops, appsec" },
      },
    );
    expect(
      screen
        .getByTestId("visibility-consequence")
        .querySelector(".consequence-text")?.textContent,
    ).toBe(
      "Everyone in this tenant will see this layer — the organization grant already covers secops and appsec.",
    );
  });

  // A registration can name a couple of dozen users, and the consequence
  // states every one of them. Unclipped, the line wrapped to eight rows,
  // pushed the neutral note and the footer down, and made the dialog body
  // scroll. The names are already listed as tokens above the line, so the
  // line clips at two rows and the dialog keeps its size.
  it("clips the consequence line of a long user grant to two lines", async () => {
    stubRegistry({
      "/v1/ui/session": {
        body: posture({ identity_provider_configured: false }),
      },
      "/v1/layers": { body: { layers: [] } },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Register layer" }));
    fireEvent.click(screen.getByLabelText("Specific users"));
    const named = Array.from(
      { length: 25 },
      (_, index) => `user${index + 1}@acme.com`,
    );
    fireEvent.change(
      screen.getByLabelText("User identifiers, separated by commas"),
      { target: { value: named.join(", ") } },
    );
    const line = screen.getByTestId("visibility-consequence");
    expect(line.textContent).toContain("user25@acme.com");
    const clipped = line.querySelector(".consequence-text");
    expect(line.textContent).toContain(clipped?.textContent ?? "");
    const style = window.getComputedStyle(clipped as Element);
    expect(style.getPropertyValue("-webkit-line-clamp")).toBe("2");
    expect(style.display).toBe("-webkit-box");
    expect(style.overflow).toBe("hidden");
  });

  // The consequence and the neutral note sit next to each other at the foot
  // of the form and are told apart only by their fill, which does not say
  // which one states what the selection admits and which one is an aside.
  // Each leads with its own glyph in a gutter of its own width, so the two
  // read as different kinds of feedback and a wrapped message keeps its left
  // edge. The glyph carries no text of its own, so it is hidden from the
  // accessibility tree and the message stays the readable content.
  it("leads the consequence and the neutral note with a glyph in their own gutter", async () => {
    stubRegistry({
      "/v1/ui/session": {
        body: posture({ identity_provider_configured: false }),
      },
      "/v1/layers": { body: { layers: [] } },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Register layer" }));
    const consequence = screen.getByTestId("visibility-consequence");
    const note = screen.getByTestId("visibility-note");
    for (const block of [consequence, note]) {
      const style = window.getComputedStyle(block);
      expect(style.display).toBe("flex");
      expect(style.gap).toBe("11px");
      const glyph = block.firstElementChild as HTMLElement;
      expect(glyph.className).toBe("note-glyph");
      expect(glyph.getAttribute("aria-hidden")).toBe("true");
      expect(glyph.textContent?.trim()).toBeTruthy();
      expect(window.getComputedStyle(glyph).width).toBe("11px");
    }
    // The two glyphs differ, so the accent block and the aside are not the
    // same mark in two fills.
    expect(consequence.firstElementChild?.textContent?.trim()).not.toBe(
      note.firstElementChild?.textContent?.trim(),
    );
  });

  // §4.6 grants to a group name the identity provider supplies and the
  // registry accepts any string, so a mistyped name registers a layer that
  // silently admits nobody and no refusal ever names it. No response
  // enumerates the provider's groups, so the check the form can make is
  // against the names already granted on the layers the caller can see: the
  // group axis narrows them as the reader types, states how many of them
  // match, and enters one on a click. Each entered member removes itself.
  it("offers the group names already granted, with a match count, and lets an entered group be removed", async () => {
    stubRegistry({
      "/v1/ui/session": {
        body: posture({ identity_provider_configured: false }),
      },
      "/v1/layers": {
        body: {
          layers: [
            {
              ...adminLayer(),
              // The blank and the repeat are what the panel actually holds:
              // the names come from several layers' grants, so the list is
              // deduplicated and an empty entry is dropped rather than
              // offered as a name.
              Groups: ["platform-oncall", "secops", " "],
            },
            {
              ...adminLayer(),
              ID: "compliance",
              Order: 2,
              Groups: ["secops", "appsec", "platform-eng"],
            },
          ],
        },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Register layer" }));
    fireEvent.click(screen.getByLabelText("Groups"));
    const field = screen.getByLabelText("Group names, separated by commas");
    const picker = screen.getByTestId("group-picker");
    // Nothing typed yet, so every known name is on offer. Four names is one
    // more than the box holds, so the header says the rest scroll.
    expect(screen.getByTestId("group-picker-count").textContent).toBe(
      "4 of 4 match · scroll for more",
    );
    expect(
      within(picker)
        .getAllByRole("button")
        .map((row) => row.textContent),
    ).toEqual(["appsec", "platform-eng", "platform-oncall", "secops"]);
    fireEvent.change(field, { target: { value: "plat" } });
    expect(screen.getByTestId("group-picker-count").textContent).toBe(
      "2 of 4 match",
    );
    // Picking a row enters that name rather than leaving the reader to
    // finish typing it, which is what makes the list a check on the spelling.
    fireEvent.click(
      within(picker).getByRole("button", { name: "platform-eng" }),
    );
    expect((field as HTMLInputElement).value).toBe("platform-eng, ");
    expect(screen.getByTestId("group-picker-count").textContent).toBe(
      "3 of 4 match",
    );
    // The caret returns to the line the pick extended, so naming a second
    // group is typing rather than a click back into the field.
    expect(document.activeElement).toBe(field);
    // A name matching nothing known is drawn as such, because the registry
    // will accept it without complaint.
    fireEvent.change(field, {
      target: { value: "platform-eng, platfrom" },
    });
    expect(screen.getByTestId("group-picker-empty").textContent).toBe(
      "No group granted elsewhere matches “platfrom”.",
    );
    // Each member is a token that drops itself from the line.
    fireEvent.click(
      screen.getByRole("button", { name: "Remove platform-eng" }),
    );
    expect((field as HTMLInputElement).value).toBe("platfrom");
    expect(
      screen.queryByRole("button", { name: "Remove platform-eng" }),
    ).toBeNull();
    expect(
      screen.getByRole("button", { name: "Remove platfrom" }),
    ).toBeTruthy();
  });

  // The picker is a typeahead under the field the reader is typing into, so
  // it is reached with the arrows rather than by tabbing out of the line and
  // back into it. The footer states the keys, because nothing about a list
  // drawn under a text field says the arrows reach it. ⏎ enters the
  // highlighted name, and the field sits in a form, so it has to be consumed:
  // an uncancelled ⏎ there submits the registration instead.
  it("moves the group picker with the arrow keys and enters the highlighted name on return", async () => {
    stubRegistry({
      "/v1/ui/session": {
        body: posture({ identity_provider_configured: false }),
      },
      "/v1/layers": {
        body: {
          layers: [
            {
              ...adminLayer(),
              Groups: ["appsec", "platform-eng", "platform-oncall", "secops"],
            },
          ],
        },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Register layer" }));
    fireEvent.click(screen.getByLabelText("Groups"));
    const field = screen.getByLabelText("Group names, separated by commas");
    const picker = screen.getByTestId("group-picker");
    expect(screen.getByTestId("group-picker-keys").textContent).toBe(
      "Type to narrow. \u2191\u2193 to move, \u23ce to select.",
    );
    const highlighted = () =>
      within(picker)
        .getAllByRole("button")
        .filter((row) => row.className.split(" ").includes("picker-row-on"))
        .map((row) => row.textContent);
    // The top row is highlighted before a key is pressed, so ⏎ enters a name
    // without an arrow first.
    expect(highlighted()).toEqual(["appsec"]);
    fireEvent.keyDown(field, { key: "ArrowDown" });
    expect(highlighted()).toEqual(["platform-eng"]);
    fireEvent.keyDown(field, { key: "ArrowUp" });
    fireEvent.keyDown(field, { key: "ArrowUp" });
    // The movement wraps, so the last row is one key from the first.
    expect(highlighted()).toEqual(["secops"]);
    fireEvent.keyDown(field, { key: "Enter" });
    expect((field as HTMLInputElement).value).toBe("secops, ");
    // The entered name leaves the list and the highlight returns to the top
    // of what is left rather than to whatever slid into its index.
    expect(highlighted()).toEqual(["appsec"]);
    // ⏎ entered a name rather than sending the registration.
    expect(requests.some((r) => r.method === "POST")).toBe(false);
    expect(screen.queryByTestId("group-picker")).toBeTruthy();
    // Typing narrows the list under the highlight, so an index past the end
    // of what is left lands on a drawn row instead of on nothing.
    fireEvent.keyDown(field, { key: "ArrowDown" });
    fireEvent.keyDown(field, { key: "ArrowDown" });
    expect(highlighted()).toEqual(["platform-oncall"]);
    fireEvent.change(field, { target: { value: "secops, app" } });
    expect(highlighted()).toEqual(["appsec"]);
  });

  // The box bounds the dialog at three whole rows, so a longer list is cut
  // off. A cut with nothing marking it contradicts the count in the header,
  // which tells the reader six matched while three are drawn. The header says
  // the rest scroll and the last visible row is faded, and both go away once
  // the typed fragment narrows the list to what the box holds.
  it("marks the group list as scrolling while it is longer than the picker holds", async () => {
    stubRegistry({
      "/v1/ui/session": {
        body: posture({ identity_provider_configured: false }),
      },
      "/v1/layers": {
        body: {
          layers: [
            {
              ...adminLayer(),
              Groups: [
                "appsec",
                "data",
                "infra",
                "platform-eng",
                "platform-oncall",
                "secops",
              ],
            },
          ],
        },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Register layer" }));
    fireEvent.click(screen.getByLabelText("Groups"));
    const field = screen.getByLabelText("Group names, separated by commas");
    expect(screen.getByTestId("group-picker-count").textContent).toBe(
      "6 of 6 match · scroll for more",
    );
    expect(
      screen.getByTestId("group-picker-rows").className.split(" "),
    ).toContain("picker-rows-scrolls");
    // Narrowed to what the box holds, nothing is hidden and neither cue is
    // drawn.
    fireEvent.change(field, { target: { value: "plat" } });
    expect(screen.getByTestId("group-picker-count").textContent).toBe(
      "2 of 6 match",
    );
    expect(
      screen.getByTestId("group-picker-rows").className.split(" "),
    ).not.toContain("picker-rows-scrolls");
  });

  // The registration reloads the list, and the reload answers over the
  // network rather than within the batch that issued it, so the list read is
  // deferred here. The panel must hold the reveal across a reload that
  // reports loading, because the secret is served once and a panel that
  // remounted the form in its place would leave the reader with no copy.
  // A local-path source is issued no webhook and therefore no secret, so the
  // response carries neither field. The dialog has nothing unrecoverable to
  // hand over: it states the outcome and stays ordinarily dismissible, with
  // no shown-once block and no acknowledgement gating the close. Every other
  // registration test supplies a secret, so this is the arm that fixes the
  // no-secret contract revealsSecret exists to express.
  it("registers a local layer without presenting a secret to acknowledge", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "GET /v1/layers": { body: { layers: [] } },
      "POST /v1/layers": {
        body: {
          layer: {
            ID: "alice-local",
            SourceType: "local",
            Order: 1,
            UserDefined: true,
          },
        },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Register layer" }));
    fireEvent.change(screen.getByLabelText("Layer ID"), {
      target: { value: "alice-local" },
    });
    fireEvent.submit(screen.getByTestId("register-form"));
    await screen.findByText("Layer alice-local is registered.");
    expect(screen.queryByLabelText("Webhook secret")).toBeNull();
    expect(screen.queryByText("SHOWN ONCE")).toBeNull();
    expect(screen.queryByLabelText("I have stored the secret.")).toBeNull();
  });

  it("reveals a git layer’s webhook secret once and holds the reveal until it is acknowledged", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "GET /v1/layers": { body: { layers: [] }, deferred: true },
      "POST /v1/layers": {
        body: {
          layer: {
            ID: "alice-personal",
            SourceType: "git",
            Order: 1,
            UserDefined: true,
          },
          webhook_url:
            "https://registry.acme.com/v1/ingest/webhook/alice-personal",
          webhook_secret: "whsec-abc",
        },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Register layer" }));
    fireEvent.change(screen.getByLabelText("Layer ID"), {
      target: { value: "alice-personal" },
    });
    fireEvent.submit(screen.getByTestId("register-form"));
    await screen.findByLabelText("Webhook secret");
    expect(screen.getByText("whsec-abc")).toBeTruthy();
    // The reload the registration triggered lands after the reveal paints,
    // and the reveal is still there once it has.
    await waitFor(() => {
      expect(
        requests.filter((r) => r.url === "/v1/layers" && r.method === "GET")
          .length,
      ).toBeGreaterThan(1);
    });
    expect(screen.getByLabelText("Webhook secret")).toBeTruthy();
    expect(screen.getByText("whsec-abc")).toBeTruthy();
    // The secret is served here and nowhere else, so it carries an explicit
    // copy control rather than leaving the reader to select it. The URL
    // carries one too.
    expect(screen.getAllByRole("button", { name: "Copy" }).length).toBe(2);
    const done = screen.getByRole("button", { name: "Done" });
    expect(done.hasAttribute("disabled")).toBe(true);
    fireEvent.click(screen.getByLabelText("I have stored the secret."));
    expect(
      screen.getByRole("button", { name: "Done" }).hasAttribute("disabled"),
    ).toBe(false);
  });

  // The URL is stored on the layer and can be read again; the secret cannot.
  // The shown-once treatment therefore covers the secret alone, because a
  // dashed block holding both values tells the reader that the URL is
  // unrecoverable as well.
  //
  // Spec: §13.10
  it("marks only the secret as shown once and leaves the webhook URL outside the block", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [] } },
      "POST /v1/layers": {
        body: {
          layer: {
            ID: "alice-personal",
            SourceType: "git",
            Order: 1,
            UserDefined: true,
          },
          webhook_url:
            "https://registry.acme.com/v1/ingest/webhook/alice-personal",
          webhook_secret: "whsec-abc",
        },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Register layer" }));
    fireEvent.change(screen.getByLabelText("Layer ID"), {
      target: { value: "alice-personal" },
    });
    fireEvent.submit(screen.getByTestId("register-form"));
    const block = await screen.findByLabelText("Webhook secret");
    // The secret and its badge are inside the block.
    expect(within(block as HTMLElement).getByText("whsec-abc")).toBeTruthy();
    expect(within(block as HTMLElement).getByText("SHOWN ONCE")).toBeTruthy();
    // The URL is outside it, with the line saying it can be read again.
    const url = screen.getByText(
      "https://registry.acme.com/v1/ingest/webhook/alice-personal",
    );
    expect(block.contains(url)).toBe(false);
    expect(
      screen.getByText("Stored on the layer. You can look this up again any time."),
    ).toBeTruthy();
    // The badge names the secret's field rather than heading both fields.
    const secretRow = within(block as HTMLElement)
      .getByText("whsec-abc")
      .closest(".copy-field") as HTMLElement;
    expect(within(secretRow).getByText("SHOWN ONCE")).toBeTruthy();
    expect(within(secretRow).getByText("Webhook secret")).toBeTruthy();
  });

  // The acknowledgement is the reader's own statement rather than part of the
  // credential the dashed block frames, and the control it gates closes the
  // dialog, so it belongs in the dialog's footer beside the note that names
  // the way back.
  //
  // Spec: §13.10
  it("puts the acknowledgement on its own row and Done in the modal footer", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [] } },
      "POST /v1/layers": {
        body: {
          layer: {
            ID: "alice-personal",
            SourceType: "git",
            Order: 1,
            UserDefined: true,
          },
          webhook_url:
            "https://registry.acme.com/v1/ingest/webhook/alice-personal",
          webhook_secret: "whsec-abc",
        },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Register layer" }));
    fireEvent.change(screen.getByLabelText("Layer ID"), {
      target: { value: "alice-personal" },
    });
    fireEvent.submit(screen.getByTestId("register-form"));
    const block = await screen.findByLabelText("Webhook secret");
    // The checkbox sits outside the dashed block that frames the credential.
    const ack = screen.getByLabelText("I have stored the secret.");
    expect(block.contains(ack)).toBe(false);
    // Done is the footer's primary, and the footer carries the rotation note.
    const done = screen.getByRole("button", { name: "Done" });
    const foot = done.closest(".modal-foot") as HTMLElement | null;
    expect(foot).not.toBeNull();
    expect(done.className).toContain("primary");
    expect(foot!.contains(ack)).toBe(false);
    expect(
      within(foot!).getByText("You can rotate the secret later if you need to."),
    ).toBeTruthy();
    // The footer sits below the dialog's scrolling body rather than inside it.
    expect(
      (foot!.previousElementSibling as HTMLElement | null)?.className,
    ).toContain("modal-body");
  });

  // The reveal is the branch where naming the layer matters most: the
  // credential is unrecoverable, and a dialog that presents it without the
  // outcome never confirms that the layer was created or which layer the
  // secret belongs to.
  it("states the registration outcome beside the revealed secret", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [] } },
      "POST /v1/layers": {
        body: {
          layer: {
            ID: "alice-personal",
            SourceType: "git",
            Order: 1,
            UserDefined: true,
          },
          webhook_url:
            "https://registry.acme.com/v1/ingest/webhook/alice-personal",
          webhook_secret: "whsec-abc",
        },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Register layer" }));
    fireEvent.change(screen.getByLabelText("Layer ID"), {
      target: { value: "alice-personal" },
    });
    fireEvent.submit(screen.getByTestId("register-form"));
    await screen.findByLabelText("Webhook secret");
    expect(screen.getByText("Layer alice-personal is registered.")).toBeTruthy();
  });

  // A rotation reveals the fresh secret on the same terms, so it states its
  // own outcome beside it.
  it("states the update outcome beside a rotated secret", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [adminLayer()] } },
      "PUT /v1/layers/update": {
        body: {
          layer: adminLayer(),
          webhook_url: "https://registry.acme.com/v1/ingest/webhook/company",
          webhook_secret: "whsec-rotated",
        },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    openRowActions("company");
    fireEvent.click(screen.getByRole("menuitem", { name: "Edit" }));
    const form = await screen.findByLabelText("Update company");
    fireEvent.click(screen.getByLabelText("Rotate the webhook secret"));
    fireEvent.submit(form);
    await screen.findByLabelText("Webhook secret");
    expect(screen.getByText("Layer company is updated.")).toBeTruthy();
  });

  // A registration is an upsert on the layer ID, so a second one sent while
  // the first is still open rewrites the layer the first one created and
  // issues a fresh webhook secret. The submit holds itself closed while its
  // own write is open, the way the row's Reingest control does.
  it("sends one registration however many times the submit is activated", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [] } },
      "POST /v1/layers": {
        deferred: true,
        body: {
          layer: {
            ID: "alice-personal",
            SourceType: "local",
            Order: 1,
            UserDefined: true,
          },
        },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Register layer" }));
    const dialog = screen.getByRole("dialog", { name: "Register a layer" });
    fireEvent.click(screen.getByRole("radio", { name: "Local folder" }));
    fireEvent.change(screen.getByLabelText("Layer ID"), {
      target: { value: "alice-personal" },
    });
    fireEvent.change(screen.getByLabelText("Local path"), {
      target: { value: "/Users/alice/reg" },
    });
    const submit = within(dialog).getByRole("button", { name: "Register" });
    fireEvent.click(submit);
    expect(submit.hasAttribute("disabled")).toBe(true);
    expect(submit.getAttribute("aria-busy")).toBe("true");
    fireEvent.click(submit);
    fireEvent.click(submit);
    // Enter from a field in the form submits it without going through the
    // control, so the form is driven that way as well.
    fireEvent.submit(screen.getByTestId("register-form"));
    await screen.findByText("Layer alice-personal is registered.");
    expect(
      requests.filter((r) => r.url === "/v1/layers" && r.method === "POST")
        .length,
    ).toBe(1);
  });

  // Every patch carrying a rotation issues a fresh secret, so a second Save
  // changes while the first is open rotates again and replaces the value the
  // reveal is presenting as shown once.
  it("sends one rotation however many times Save changes is activated", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [adminLayer()] } },
      "PUT /v1/layers/update": {
        deferred: true,
        body: {
          layer: adminLayer(),
          webhook_url: "https://registry.acme.com/v1/ingest/webhook/company",
          webhook_secret: "whsec-rotated",
        },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    openRowActions("company");
    fireEvent.click(screen.getByRole("menuitem", { name: "Edit" }));
    const form = await screen.findByLabelText("Update company");
    fireEvent.click(screen.getByLabelText("Rotate the webhook secret"));
    const save = within(form).getByRole("button", { name: "Save changes" });
    fireEvent.click(save);
    expect(save.hasAttribute("disabled")).toBe(true);
    expect(save.getAttribute("aria-busy")).toBe("true");
    fireEvent.click(save);
    fireEvent.submit(form);
    await screen.findByLabelText("Webhook secret");
    expect(
      requests.filter(
        (r) => r.url.startsWith("/v1/layers/update") && r.method === "PUT",
      ).length,
    ).toBe(1);
    expect(screen.getByText("whsec-rotated")).toBeTruthy();
  });

  // The secret is served once, so the copy is the one action in the panel a
  // reader cannot repeat. A confirmation that only paints beside the control
  // reaches nobody driving the panel by screen reader, so the outcome is
  // carried by a live region that is on the page before the copy lands.
  it("announces the one-time secret copy through a live region", async () => {
    const written: string[] = [];
    const clipboard = {
      writeText: (text: string) => {
        written.push(text);
        return Promise.resolve();
      },
    };
    const original = Object.getOwnPropertyDescriptor(navigator, "clipboard");
    Object.defineProperty(navigator, "clipboard", {
      value: clipboard,
      configurable: true,
    });
    try {
      stubRegistry({
        "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
        "/v1/layers": { body: { layers: [] } },
        "POST /v1/layers": {
          body: {
            layer: {
              ID: "alice-personal",
              SourceType: "git",
              Order: 1,
              UserDefined: true,
            },
            webhook_url:
              "https://registry.acme.com/v1/ingest/webhook/alice-personal",
            webhook_secret: "whsec-abc",
          },
        },
      });
      goTo("#/layers");
      render(<App />);
      await screen.findByLabelText("Layer panel");
      fireEvent.click(screen.getByRole("button", { name: "Register layer" }));
      fireEvent.change(screen.getByLabelText("Layer ID"), {
        target: { value: "alice-personal" },
      });
      fireEvent.submit(screen.getByTestId("register-form"));
      await screen.findByLabelText("Webhook secret");
      // The region is mounted and empty before the copy, so the text arrives
      // as a change to a region already in the accessibility tree.
      const regions = screen.getAllByTestId("copy-announcement");
      expect(regions.length).toBe(2);
      for (const region of regions) {
        expect(region.getAttribute("aria-live")).toBe("polite");
        expect(region.textContent).toBe("");
      }
      const secretRow = screen.getByText("whsec-abc").closest(".copy-field");
      const copy = within(secretRow as HTMLElement).getByRole("button", {
        name: "Copy",
      });
      fireEvent.click(copy);
      await waitFor(() => {
        expect(
          within(secretRow as HTMLElement).getByTestId("copy-announcement")
            .textContent,
        ).toBe("Webhook secret copied to clipboard.");
      });
      expect(written).toEqual(["whsec-abc"]);
      // The visible confirmation is not read a second time beside the region.
      expect(
        within(secretRow as HTMLElement)
          .getByText("Copied")
          .getAttribute("aria-hidden"),
      ).toBe("true");
    } finally {
      if (original) {
        Object.defineProperty(navigator, "clipboard", original);
      } else {
        delete (navigator as unknown as { clipboard?: unknown }).clipboard;
      }
    }
  });

  // The secret is served once and is unrecoverable, so the reveal gates its
  // own dismissal behind an acknowledgement. A dialog that also closed on
  // Escape or on a scrim click would discard the credential around that
  // gate, and the reader's only way back would be a rotation.
  it("holds the secret reveal against Escape, the scrim, and a close control", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [] } },
      "POST /v1/layers": {
        body: {
          layer: {
            ID: "alice-personal",
            SourceType: "git",
            Order: 1,
            UserDefined: true,
          },
          webhook_url:
            "https://registry.acme.com/v1/ingest/webhook/alice-personal",
          webhook_secret: "whsec-abc",
        },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Register layer" }));
    fireEvent.change(screen.getByLabelText("Layer ID"), {
      target: { value: "alice-personal" },
    });
    fireEvent.submit(screen.getByTestId("register-form"));
    await screen.findByLabelText("Webhook secret");
    // The dialog offers no close control while the secret is unacknowledged,
    // so the acknowledgement is the only route out.
    expect(screen.queryByRole("button", { name: "Close" })).toBeNull();
    fireEvent.keyDown(document, { key: "Escape" });
    expect(screen.getByText("whsec-abc")).toBeTruthy();
    fireEvent.click(screen.getByTestId("modal-scrim"));
    expect(screen.getByText("whsec-abc")).toBeTruthy();
    fireEvent.click(screen.getByLabelText("I have stored the secret."));
    fireEvent.click(screen.getByRole("button", { name: "Done" }));
    await waitFor(() => {
      expect(screen.queryByLabelText("Webhook secret")).toBeNull();
    });
  });

  // The browser's Back gesture is the dismissal route the dialog cannot see:
  // it fires no key and no press, and leaving the layers route unmounts the
  // panel the reveal is rendered from. It discards the same credential Escape
  // and the scrim are refused for, so the shell refuses it the same way and
  // pins the route the reveal opened on back onto the address bar.
  it("holds the secret reveal against a history step and restores the route", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [] } },
      "POST /v1/layers": {
        body: {
          layer: {
            ID: "alice-personal",
            SourceType: "git",
            Order: 1,
            UserDefined: true,
          },
          webhook_url:
            "https://registry.acme.com/v1/ingest/webhook/alice-personal",
          webhook_secret: "whsec-abc",
        },
      },
    });
    goTo(layersHref);
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Register layer" }));
    fireEvent.change(screen.getByLabelText("Layer ID"), {
      target: { value: "alice-personal" },
    });
    fireEvent.submit(screen.getByTestId("register-form"));
    await screen.findByLabelText("Webhook secret");
    // The step the reader's Back gesture lands on: the hash moves off the
    // layers route and the shell is told about it.
    window.location.hash = searchHref("zzqqnomatch");
    fireEvent(window, new HashChangeEvent("hashchange"));
    expect(screen.getByText("whsec-abc")).toBeTruthy();
    expect(window.location.hash).toBe(layersHref);
    // The acknowledgement remains the way out, and the shell follows a route
    // change again once the reveal is gone.
    fireEvent.click(screen.getByLabelText("I have stored the secret."));
    fireEvent.click(screen.getByRole("button", { name: "Done" }));
    await waitFor(() => {
      expect(screen.queryByLabelText("Webhook secret")).toBeNull();
    });
    window.location.hash = searchHref("zzqqnomatch");
    fireEvent(window, new HashChangeEvent("hashchange"));
    await waitFor(() => {
      expect(screen.queryByLabelText("Layer panel")).toBeNull();
    });
  });

  // The reveal refuses Escape, the scrim, and every control behind it because
  // the secret is unrecoverable, so the accelerator that opens the command
  // palette has to refuse it too: a palette over the reveal takes focus, and
  // opening any result navigates the shell and unmounts the secret.
  it("refuses the command-palette accelerator while the secret reveal is open", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [] } },
      "POST /v1/layers": {
        body: {
          layer: {
            ID: "alice-personal",
            SourceType: "git",
            Order: 1,
            UserDefined: true,
          },
          webhook_url:
            "https://registry.acme.com/v1/ingest/webhook/alice-personal",
          webhook_secret: "whsec-abc",
        },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Register layer" }));
    fireEvent.change(screen.getByLabelText("Layer ID"), {
      target: { value: "alice-personal" },
    });
    fireEvent.submit(screen.getByTestId("register-form"));
    await screen.findByLabelText("Webhook secret");
    fireEvent.keyDown(window, { key: "k", metaKey: true });
    expect(screen.queryByTestId("palette")).toBeNull();
    expect(screen.getByText("whsec-abc")).toBeTruthy();
    // The acknowledgement is still the way out, and the accelerator works
    // again once the reveal is gone.
    fireEvent.click(screen.getByLabelText("I have stored the secret."));
    fireEvent.click(screen.getByRole("button", { name: "Done" }));
    await waitFor(() => {
      expect(screen.queryByLabelText("Webhook secret")).toBeNull();
    });
    fireEvent.keyDown(window, { key: "k", metaKey: true });
    expect(screen.getByTestId("palette")).toBeTruthy();
  });

  // A local source returns no secret, so the registration outcome carries
  // nothing the reader has to take away and the dialog dismisses the way
  // every other dialog does.
  it("leaves a secretless registration outcome dismissible", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [] } },
      "POST /v1/layers": {
        body: {
          layer: {
            ID: "alice-personal",
            SourceType: "local",
            Order: 1,
            UserDefined: true,
          },
        },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Register layer" }));
    fireEvent.click(screen.getByRole("radio", { name: "Local folder" }));
    fireEvent.change(screen.getByLabelText("Layer ID"), {
      target: { value: "alice-personal" },
    });
    fireEvent.change(screen.getByLabelText("Local path"), {
      target: { value: "/Users/alice/reg" },
    });
    fireEvent.submit(screen.getByTestId("register-form"));
    await screen.findByText("Layer alice-personal is registered.");
    expect(screen.getByRole("button", { name: "Close" })).toBeTruthy();
    fireEvent.keyDown(document, { key: "Escape" });
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });
  });

  // §7.3.1 runs no ingest on a registration: a git source stays at its
  // initial commit until a webhook delivery or the first manual reingest,
  // and a local source is read at that reingest too. The submit therefore
  // promises the registration alone, and the outcome names the ingest as the
  // next thing to run so the row it just added, which reads "never", is not
  // left as a layer the reader believes carries its artifacts.
  it("promises the registration alone and names the ingest as the next step", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [] } },
      "POST /v1/layers": {
        body: {
          layer: {
            ID: "alice-personal",
            SourceType: "local",
            Order: 1,
            UserDefined: true,
          },
        },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Register layer" }));
    const dialog = screen.getByRole("dialog", { name: "Register a layer" });
    expect(
      within(dialog).queryByRole("button", { name: /ingest/i }),
    ).toBeNull();
    fireEvent.click(screen.getByRole("radio", { name: "Local folder" }));
    fireEvent.change(screen.getByLabelText("Layer ID"), {
      target: { value: "alice-personal" },
    });
    fireEvent.change(screen.getByLabelText("Local path"), {
      target: { value: "/Users/alice/reg" },
    });
    fireEvent.click(within(dialog).getByRole("button", { name: "Register" }));
    await screen.findByText("Layer alice-personal is registered.");
    // The registration sends the register call and nothing else, so a reader
    // who acts on the outcome is the one who runs the ingest.
    expect(
      requests.filter((r) => r.url === "/v1/layers" && r.method === "POST")
        .length,
    ).toBe(1);
    expect(requests.some((r) => r.url.startsWith("/v1/layers/reingest"))).toBe(
      false,
    );
    expect(screen.getByTestId("register-ingest-note").textContent).toContain(
      "Reingest",
    );
  });

  // The outcome is a second dialog rendered in the form's place, so it takes
  // the reader's focus the way the form did. The submit control that held
  // focus unmounts with the form, which leaves focus on the document, and a
  // keyboard reader is then given no sign that the registration finished or
  // that a dialog covers the page.
  it("moves focus into the registration outcome dialog", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [] } },
      "POST /v1/layers": {
        body: {
          layer: {
            ID: "alice-personal",
            SourceType: "local",
            Order: 1,
            UserDefined: true,
          },
        },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    // A pointer press focuses the control it lands on, which jsdom leaves to
    // the caller, and the control focus returns to is read from there.
    const trigger = screen.getByRole("button", { name: "Register layer" });
    trigger.focus();
    fireEvent.click(trigger);
    const form = screen.getByRole("dialog", { name: "Register a layer" });
    fireEvent.click(screen.getByRole("radio", { name: "Local folder" }));
    fireEvent.change(screen.getByLabelText("Layer ID"), {
      target: { value: "alice-personal" },
    });
    fireEvent.change(screen.getByLabelText("Local path"), {
      target: { value: "/Users/alice/reg" },
    });
    fireEvent.click(within(form).getByRole("button", { name: "Register" }));
    await screen.findByText("Layer alice-personal is registered.");
    const outcome = screen.getByRole("dialog", { name: "Layer registered" });
    await waitFor(() => {
      expect(outcome.contains(document.activeElement)).toBe(true);
    });
    // Leaving the outcome still hands focus back to the control the
    // registration was started from.
    fireEvent.keyDown(document, { key: "Escape" });
    await waitFor(() => {
      expect(document.activeElement).toBe(trigger);
    });
  });

  // The update is a partial patch and a rotation returns the fresh secret
  // once, on the same terms as registration, so the rotation runs through the
  // same reveal rather than through a second treatment.
  it("patches a git layer and reveals the rotated secret once", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [adminLayer()] }, deferred: true },
      "PUT /v1/layers/update": {
        body: {
          layer: { ID: "company", SourceType: "git", Order: 1 },
          webhook_url: "https://registry.acme.com/v1/ingest/webhook/company",
          webhook_secret: "whsec-rotated",
        },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    openRowActions("company");
    fireEvent.click(screen.getByRole("menuitem", { name: "Edit" }));
    const form = await screen.findByLabelText("Update company");
    // The endpoint applies a visibility patch on an admin-defined layer, so
    // the form carries the axes and the patch carries what they name. It
    // grants on each axis and revokes on none, so a stored grant is displayed
    // as unavailable rather than offered as a change the registry answers
    // success to without making.
    expect(screen.getByLabelText("Organization").hasAttribute("disabled")).toBe(
      true,
    );
    fireEvent.click(screen.getByLabelText("Public"));
    fireEvent.change(
      screen.getByLabelText("Group names, separated by commas"),
      { target: { value: "secops" } },
    );
    fireEvent.change(screen.getByLabelText("Ref"), {
      target: { value: "release" },
    });
    fireEvent.change(screen.getByLabelText("Force-push policy"), {
      target: { value: "strict" },
    });
    fireEvent.click(screen.getByLabelText("Rotate the webhook secret"));
    fireEvent.submit(form);
    await waitFor(() => {
      expect(
        requests.some(
          (r) => r.url === "/v1/layers/update?id=company" && r.method === "PUT",
        ),
      ).toBe(true);
    });
    const sent = JSON.parse(bodies.at(-1) ?? "{}") as Record<string, unknown>;
    expect(sent.ref).toBe("release");
    expect(sent.force_push_policy).toBe("strict");
    expect(sent.rotate_webhook_secret).toBe(true);
    expect(sent.public).toBe(true);
    expect(sent.groups).toEqual(["secops"]);
    await screen.findByLabelText("Webhook secret");
    expect(screen.getByText("whsec-rotated")).toBeTruthy();
  });

  // Only a git source carries a webhook secret, and the registry refuses a
  // rotation on any other source, so the control is unavailable on a
  // local-path layer and says why.
  it("offers no rotation on a local-path layer and patches its source details", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [userLayer()] } },
      "PUT /v1/layers/update": { body: { layer: userLayer() } },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    openRowActions();
    fireEvent.click(screen.getByRole("menuitem", { name: "Edit" }));
    const form = await screen.findByLabelText("Update alice-personal");
    const rotate = screen.getByLabelText("Rotate the webhook secret");
    expect(rotate.hasAttribute("disabled")).toBe(true);
    // §4.6 fixes a user-defined layer's visibility at registration, and the
    // registry ignores a visibility patch there and still answers success, so
    // that class displays its visibility rather than editing it.
    expect(screen.queryByLabelText("Organization")).toBeNull();
    expect(form.textContent).toContain("fixed to you at registration");
    expect(form.textContent).toContain(
      "Only a git layer carries a webhook secret.",
    );
    fireEvent.change(screen.getByLabelText("Local path"), {
      target: { value: "/Users/alice/moved" },
    });
    fireEvent.submit(form);
    await waitFor(() => {
      expect(
        requests.some(
          (r) =>
            r.url === "/v1/layers/update?id=alice-personal" &&
            r.method === "PUT",
        ),
      ).toBe(true);
    });
    const sent = JSON.parse(bodies.at(-1) ?? "{}") as Record<string, unknown>;
    expect(sent.local_path).toBe("/Users/alice/moved");
    expect(sent.rotate_webhook_secret).toBeUndefined();
    expect(sent.public).toBeUndefined();
    expect(sent.groups).toBeUndefined();
    // A patch that rotates nothing carries no secret, so the reveal is
    // replaced by the outcome the update reports.
    expect(
      (await screen.findByText("Layer alice-personal is updated.")).textContent,
    ).toBeTruthy();
  });

  // The update is reviewed before it is sent, on the same terms as the
  // register and the unregister writes, so it opens over the panel. Left
  // inside the row's actions cell it was laid out by that fixed-width column,
  // which is too narrow for a filesystem path, and it grew the row enough to
  // reflow its neighbours.
  it("opens the edit form as a dialog over the panel", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [adminLayer(), userLayer()] } },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    openRowActions();
    fireEvent.click(screen.getByRole("menuitem", { name: "Edit" }));
    const form = await screen.findByLabelText("Update alice-personal");
    const dialog = form.closest('[role="dialog"]');
    expect(dialog).toBeTruthy();
    expect(dialog?.getAttribute("aria-modal")).toBe("true");
    expect(screen.getByTestId("modal-scrim")).toBeTruthy();
    // The dialog stands at the end of the document rather than inside the
    // cell that opened it, so no ancestor of the panel's table lays it out.
    expect(form.closest("td")).toBeNull();
    expect(form.closest("table")).toBeNull();
    // The scrim dismisses it, which is the treatment the sibling dialogs
    // carry, so a reader who opened the form to look can leave it.
    fireEvent.click(screen.getByTestId("modal-scrim"));
    await waitFor(() => {
      expect(screen.queryByLabelText("Update alice-personal")).toBeNull();
    });
  });

  // §4.6 composes every user-defined layer above every admin-defined one
  // whatever the stored order values are, so a move runs inside the moving
  // layer's own class and the request names that class block. The endpoint
  // rewrites the order value of every layer the request names from that
  // layer's position in the request, so a request naming the traded pair
  // alone would stamp the block's first two order values onto the pair and
  // leave the rest of the block holding stored values that tie or invert
  // against them.
  it("sends the resulting order of the moving layer class block", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": {
        body: {
          layers: [adminLayer(), userLayer(), scratchLayer(), bobLayer()],
        },
      },
      "/v1/layers/reorder": { body: { layers: [] } },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    // The move commits on drop: the dragged row lands where the row it was
    // dropped onto stood.
    dragRowOnto("alice-personal", "alice-scratch");
    await waitFor(() => {
      expect(
        requests.some(
          (r) => r.url === "/v1/layers/reorder" && r.method === "POST",
        ),
      ).toBe(true);
    });
    // The user-defined block is alice-personal, alice-scratch, bob-personal
    // in stored order, and the move puts the first row where the second
    // stood. The whole block is named, bob-personal included, so its
    // rewritten order value keeps it below the pair rather than colliding
    // with them; the registry authorizes each named layer on its own and the
    // panel presents whatever it refuses.
    expect(bodies.at(-1)).toBe(
      JSON.stringify({
        order: ["alice-scratch", "alice-personal", "bob-personal"],
      }),
    );
  });

  // A dragstart that writes nothing to the drag data store is a cancelled
  // drag under the HTML drag-and-drop model, and a browser that enforces
  // that fires neither dragover nor drop, which takes the pointer reorder
  // away and leaves only the keyboard handle.
  it("carries the dragged layer on the drag data store", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": {
        body: { layers: [adminLayer(), userLayer(), scratchLayer()] },
      },
      "/v1/layers/reorder": { body: { layers: [] } },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    const store = dragRowOnto("alice-personal", "alice-scratch");
    expect(store.getData("text/plain")).toBe("alice-personal");
    // The drop moves the row rather than copying it.
    expect(store.effectAllowed).toBe("move");
  });

  // The panel's instruction names the handle as what a drag starts from, and
  // the row carries text a reader selects with the mouse. A draggable row
  // started a reorder from a drag begun over the layer name or the source
  // path, and it took that text out of the selection model.
  it("makes only the handle draggable, and leaves the rest of the row alone", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": {
        body: { layers: [adminLayer(), userLayer(), scratchLayer()] },
      },
      "/v1/layers/reorder": { body: { layers: [] } },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    expect(layerHandle("alice-personal").getAttribute("draggable")).toBe(
      "true",
    );
    for (const id of ["company", "alice-personal", "alice-scratch"]) {
      expect(layerRow(id).getAttribute("draggable")).toBeNull();
    }
  });

  // The dragged row takes the slot of the row it is dropped onto, so it lands
  // above that row when it moves up the table and below it when it moves
  // down. The indicator marks the edge the row will land on, because an
  // indicator fixed to the top edge promises the slot above the target on a
  // downward drag and the row arrives one place further down than that.
  it("marks the edge the dragged row will land on", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": {
        body: {
          layers: [adminLayer(), userLayer(), scratchLayer(), bobLayer()],
        },
      },
      "/v1/layers/reorder": { body: { layers: [] } },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    // Downward: alice-personal onto alice-scratch lands below alice-scratch.
    dragRowOver("alice-personal", "alice-scratch");
    expect(layerRow("alice-scratch").className).toContain("row-drop-below");
    expect(layerRow("alice-scratch").className).not.toContain("row-drop-above");
    fireEvent.dragEnd(layerRow("alice-personal"));
    // Upward: bob-personal onto alice-personal lands above alice-personal.
    dragRowOver("bob-personal", "alice-personal");
    expect(layerRow("alice-personal").className).toContain("row-drop-above");
    expect(layerRow("alice-personal").className).not.toContain(
      "row-drop-below",
    );
  });

  // §4.6 composes every user-defined layer above every admin-defined one
  // whatever the stored order values are, so a drop across the class boundary
  // names a move no composition would make and the panel sends nothing.
  it("sends no reorder where the drop crosses the layer-class boundary", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": {
        body: { layers: [adminLayer(), userLayer(), scratchLayer()] },
      },
      "/v1/layers/reorder": { body: { layers: [] } },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    dragRowOnto("alice-personal", "company");
    await waitFor(() => {
      expect(screen.getByLabelText("Layer panel")).toBeTruthy();
    });
    expect(requests.some((r) => r.url === "/v1/layers/reorder")).toBe(false);
  });

  // Precedence is the panel's one ordering write, and a pointer drag is the
  // only way to issue it unless the handle is a control the keyboard can
  // reach. The handle takes focus and the arrow keys walk the row through its
  // block, sending the request a drop sends.
  it("reorders from the keyboard when the handle takes an arrow key", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": {
        body: {
          layers: [adminLayer(), userLayer(), scratchLayer(), bobLayer()],
        },
      },
      "/v1/layers/reorder": { body: { layers: [] } },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    const handle = screen.getByLabelText(
      moveHandleLabel("alice-personal"),
    ) as HTMLButtonElement;
    handle.focus();
    expect(document.activeElement).toBe(handle);
    fireEvent.keyDown(handle, { key: "ArrowDown" });
    await waitFor(() => {
      expect(
        requests.some(
          (r) => r.url === "/v1/layers/reorder" && r.method === "POST",
        ),
      ).toBe(true);
    });
    // A step down the block is the move a drop onto the next row commits, and
    // the request names the whole block for the same reason.
    expect(bodies.at(-1)).toBe(
      JSON.stringify({
        order: ["alice-scratch", "alice-personal", "bob-personal"],
      }),
    );
  });

  // The keyboard path is there for an operator who cannot see the rows swap,
  // so the move states where the layer landed in a polite live region rather
  // than reporting itself by the swap alone.
  it("announces where a committed move left the layer", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": {
        body: {
          layers: [adminLayer(), userLayer(), scratchLayer(), bobLayer()],
        },
      },
      "/v1/layers/reorder": { body: { layers: [] } },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    const region = screen.getByTestId("panel-announcement");
    expect(region.getAttribute("aria-live")).toBe("polite");
    expect(region.textContent).toBe("");
    fireEvent.keyDown(
      screen.getByLabelText(moveHandleLabel("alice-personal")),
      {
        key: "ArrowDown",
      },
    );
    await waitFor(() => {
      expect(screen.getByTestId("panel-announcement").textContent).toBe(
        "alice-personal moved to order 3 of 4.",
      );
    });
  });

  // A step off the end of the block names no move §4.6 would compose, so the
  // key does what a drop across the class boundary does and sends nothing.
  it("sends no reorder where an arrow key steps off the end of the block", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": {
        body: { layers: [adminLayer(), userLayer(), scratchLayer()] },
      },
      "/v1/layers/reorder": { body: { layers: [] } },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.keyDown(
      screen.getByLabelText(moveHandleLabel("alice-personal")),
      {
        key: "ArrowUp",
      },
    );
    await waitFor(() => {
      expect(screen.getByLabelText("Layer panel")).toBeTruthy();
    });
    expect(requests.some((r) => r.url === "/v1/layers/reorder")).toBe(false);
  });

  // A refused step is a no-op the keyboard reader cannot see, so it states
  // its refusal in the live region the committed move states its outcome in.
  // Leaving the region alone leaves the previous move's confirmation standing
  // as the answer to a press that moved nothing.
  it("announces that an arrow key stepped off the end of the block", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": {
        body: { layers: [adminLayer(), userLayer(), scratchLayer()] },
      },
      "/v1/layers/reorder": { body: { layers: [] } },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.keyDown(
      screen.getByLabelText(moveHandleLabel("alice-personal")),
      { key: "ArrowDown" },
    );
    await waitFor(() => {
      expect(screen.getByTestId("panel-announcement").textContent).toBe(
        "alice-personal moved to order 3 of 3.",
      );
    });
    fireEvent.keyDown(
      screen.getByLabelText(moveHandleLabel("alice-personal")),
      { key: "ArrowUp" },
    );
    await waitFor(() => {
      expect(screen.getByTestId("panel-announcement").textContent).toBe(
        "alice-personal is already first among the user-defined layers; it did not move.",
      );
    });
  });

  // The fan-out issues one request per layer in sequence, and the press is
  // one press, so the run answers with one report: the combined counts, a row
  // per layer, and no dialog naming a single layer.
  it("reingests every layer in sequence and reports the run once", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [adminLayer(), userLayer()] } },
      "/v1/layers/reingest?id=company": {
        body: { accepted: 3, idempotent: 1 },
      },
      "/v1/layers/reingest?id=alice-personal": {
        body: { accepted: 2, idempotent: 6 },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Reingest all" }));
    const report = await screen.findByLabelText("Reingest all result");
    const reingests = requests.filter((r) =>
      r.url.startsWith("/v1/layers/reingest"),
    );
    expect(reingests.length).toBe(2);
    expect(reingests[0].url).toContain("id=company");
    expect(reingests[1].url).toContain("id=alice-personal");
    // One dialog for the whole run, naming how many layers it covered.
    expect(screen.getAllByRole("dialog").length).toBe(1);
    expect(
      screen.getByRole("dialog", { name: /Reingest all finished/ }).textContent,
    ).toContain("2 layers");
    expect(screen.queryByLabelText("Reingest result for company")).toBeNull();
    expect(
      screen.queryByLabelText("Reingest result for alice-personal"),
    ).toBeNull();
    // The counts are the run's, not one layer's.
    const counts = within(report).getByLabelText(
      "Ingest counts across the run",
    );
    expect(within(counts).getByText("5")).toBeTruthy();
    expect(within(counts).getByText("accepted")).toBeTruthy();
    expect(within(counts).getByText("unchanged")).toBeTruthy();
    expect(report.textContent).toContain("company");
    expect(report.textContent).toContain("alice-personal");
  });

  // The registry runs the whole ingest pipeline inside the request, so a
  // layer already reingesting must not be handed a second concurrent request.
  // The row trigger and the fan-out are one guard: while a row's own reingest
  // is open, "Reingest all" is held, and the row keeps the result it waited
  // for rather than having it overwritten by a run that started after it.
  it("holds Reingest all while a row's own reingest is open", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [adminLayer(), userLayer()] } },
      "/v1/layers/reingest?id=company": {
        body: { accepted: 3, idempotent: 1 },
        deferred: true,
      },
      "/v1/layers/reingest?id=alice-personal": {
        body: { accepted: 2, idempotent: 6 },
        deferred: true,
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getAllByRole("button", { name: "Reingest" })[0]);
    const all = screen.getByRole("button", { name: "Reingest all" });
    expect(all.hasAttribute("disabled")).toBe(true);
    fireEvent.click(all);
    // The row's own reingest answers, and its result is what the row reports.
    await screen.findByLabelText("Reingest result for company");
    const reingests = requests.filter((r) =>
      r.url.startsWith("/v1/layers/reingest"),
    );
    expect(reingests.map((r) => r.url)).toEqual([
      "/v1/layers/reingest?id=company",
    ]);
    // The guard lifts once nothing is in flight.
    await waitFor(() => {
      expect(
        screen
          .getByRole("button", { name: "Reingest all" })
          .hasAttribute("disabled"),
      ).toBe(false);
    });
  });

  // The same guard from the other side: the fan-out reingests every layer, so
  // the row triggers are held for as long as it runs.
  it("holds the row triggers while the fan-out is running", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [adminLayer(), userLayer()] } },
      "/v1/layers/reingest?id=company": {
        body: { accepted: 3, idempotent: 1 },
        deferred: true,
      },
      "/v1/layers/reingest?id=alice-personal": {
        body: { accepted: 2, idempotent: 6 },
        deferred: true,
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Reingest all" }));
    for (const trigger of screen.getAllByRole("button", { name: "Reingest" })) {
      expect(trigger.hasAttribute("disabled")).toBe(true);
    }
    await screen.findByLabelText("Reingest all result");
    // Two layers, one request each: the fan-out issued nothing twice.
    expect(
      requests.filter((r) => r.url.startsWith("/v1/layers/reingest")).length,
    ).toBe(2);
  });

  // A layer the registry refused is part of the run's result. Reported on the
  // row alone it sat behind the reports of every other layer, so the roll-up
  // names it with the code and the message its envelope carried.
  it("names a refused layer in the run report", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [adminLayer(), userLayer()] } },
      "/v1/layers/reingest?id=company": {
        body: { accepted: 3, idempotent: 1 },
      },
      "/v1/layers/reingest?id=alice-personal": {
        status: 422,
        body: {
          code: "registry.invalid_config",
          message: "source: invalid_config: git source requires ref",
        },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Reingest all" }));
    await screen.findByLabelText("Reingest all result");
    const refused = screen.getByLabelText("Refused layers");
    expect(refused.textContent).toContain("alice-personal");
    expect(refused.textContent).toContain("registry.invalid_config");
    expect(refused.textContent).toContain("git source requires ref");
    // The refusal is in the run's report rather than under the row, where the
    // report stack hid it.
    expect(screen.queryByLabelText("Reingest refused")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Done" }));
    await waitFor(() => {
      expect(screen.queryByLabelText("Reingest all result")).toBeNull();
    });
  });

  // The run ends by re-reading the layer list, and an outage that began while
  // the fan-out was running refuses that read. The press issued a request per
  // layer, so what they answered is reported beside the outage rather than
  // being dropped by the panel's error state.
  it("reports the run when the reload that ends it is refused", async () => {
    const stubs: Record<string, Stub> = {
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [adminLayer(), userLayer()] } },
      "/v1/layers/reingest?id=company": {
        body: { accepted: 3, idempotent: 1 },
      },
      "/v1/layers/reingest?id=alice-personal": {
        status: 503,
        body: { code: "registry.unavailable", message: "down" },
      },
    };
    stubRegistry(stubs);
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    // The outage begins after the panel loaded, so the fan-out's first layer
    // answers and the reload that follows the run does not.
    stubs["/v1/layers"] = {
      status: 503,
      body: { code: "registry.unavailable", message: "down" },
    };
    fireEvent.click(screen.getByRole("button", { name: "Reingest all" }));
    // The refused reload lands last, and the report is still on the page once
    // it has: the outage state stands in for the table alone.
    await screen.findByText("The registry did not answer this request.");
    const report = screen.getByLabelText("Reingest all result");
    // Both layers are in the report: the one that answered before the outage
    // and the one the outage refused.
    expect(report.textContent).toContain("company");
    expect(report.textContent).toContain("alice-personal");
    expect(screen.getByLabelText("Refused layers").textContent).toContain(
      "registry.unavailable",
    );
  });

  // The reingest call runs the whole pipeline inside the request and answers
  // with a summary the reader has to act on, so the control presents the
  // counts and, behind the count that carries the list, the itemised
  // rejections and conflicts rather than returning the row to rest.
  it("presents what the reingest snapshot accepted, rejected, and conflicted on", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [userLayer()] } },
      "/v1/layers/reingest": {
        body: {
          layer: "alice-personal",
          accepted: 4,
          idempotent: 2,
          lint_failures: 1,
          rejected: [
            {
              artifact_id: "platform/deploy",
              code: "ingest.sensitivity_floor",
              reason: "above the floor",
            },
          ],
          conflicts: [
            {
              artifact_id: "platform/lint",
              version: "1.0.0",
              old_hash: "sha256:aaa",
              new_hash: "sha256:bbb",
              code: "ingest.immutable_violation",
            },
          ],
          advisories: [
            {
              artifact_id: "platform/ci",
              code: "license.changed",
              severity: "warning",
              message: "license changed",
            },
          ],
        },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Reingest" }));
    await screen.findByLabelText("Reingest result for alice-personal");
    const counts = screen.getByLabelText("Ingest counts");
    expect(
      within(counts).getByText("accepted").previousSibling?.textContent,
    ).toBe("4");
    expect(
      within(counts).getByText("unchanged").previousSibling?.textContent,
    ).toBe("2");
    // lint_failures arrives as a bare number, so the card says the count is
    // all the response carried and opens nothing.
    expect(within(counts).getByText("count only")).toBeTruthy();
    // The pipeline ran inside the request, so the panel's own clock is what
    // says how long the reader waited and when the run finished.
    expect(
      screen.getByText(/^alice-personal · \d+ (second|minute)/),
    ).toBeTruthy();
    expect(screen.getByText(/^finished \d\d:\d\d:\d\d UTC$/)).toBeTruthy();
    expect(screen.getByLabelText("Advisories")).toBeTruthy();
    // The itemised lists sit behind the counts that carry them.
    expect(screen.queryByLabelText("Rejected artifacts")).toBeNull();
    // Only the counts the response itemises are controls: rejected and
    // conflicts open, accepted, unchanged, and lint failures do not.
    expect(within(counts).getAllByRole("button").length).toBe(2);
    fireEvent.click(
      screen.getByRole("button", { name: "1 artifact rejected" }),
    );
    expect(screen.getByLabelText("Rejected artifacts")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Back to summary" }));
    fireEvent.click(
      screen.getByRole("button", { name: "1 immutability conflict" }),
    );
    expect(screen.getByText("platform/lint@1.0.0")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Back to summary" }));
    fireEvent.click(screen.getByRole("button", { name: "Done" }));
    expect(
      screen.queryByLabelText("Reingest result for alice-personal"),
    ).toBeNull();
  });

  // A finished reingest is a result the reader has to read: an artifact id, a
  // rejection reason, and an advisory message are all full-width prose. The
  // layer row's actions cell is a fixed narrow column in a grid every row
  // shares, so the report drawn into that cell widened the table past its
  // section, collapsed the source column, and clipped the advisory text off
  // the right edge. It resolves over the page instead.
  it("resolves the finished reingest over the page rather than into the layer row", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [userLayer()] } },
      "/v1/layers/reingest": {
        body: {
          layer: "alice-personal",
          accepted: 0,
          idempotent: 1,
          advisories: [
            {
              artifact_id:
                "platform/infrastructure/kubernetes/clusters/production-cluster-runbook",
              code: "lint.thin_description",
              severity: "warning",
              message: "description is thin (6 chars, 2 words)",
            },
          ],
        },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Reingest" }));
    const report = await screen.findByLabelText(
      "Reingest result for alice-personal",
    );
    const dialog = screen.getByRole("dialog", { name: /Reingest finished/ });
    expect(dialog.getAttribute("aria-modal")).toBe("true");
    expect(screen.getByTestId("modal-scrim")).toBeTruthy();
    // The report is carried by the dialog rather than by the row's actions
    // cell, which is what keeps the table's columns at their own widths.
    expect(report.closest(".modal")).toBe(dialog);
    expect(within(dialog).getByText(/production-cluster-runbook/)).toBeTruthy();
    // The dialog is pinned to the viewport, so the panel under it is held
    // still while it is open rather than sliding around behind it.
    expect(document.documentElement.style.overflow).toBe("hidden");
    // A dialog opened to be read can be left without acting on it.
    fireEvent.keyDown(document, { key: "Escape" });
    expect(
      screen.queryByLabelText("Reingest result for alice-personal"),
    ).toBeNull();
    expect(document.documentElement.style.overflow).toBe("");
  });

  // A registry with no ingest runner wired records the intent and answers
  // with no summary, so the control says the request was recorded rather than
  // presenting a summary of zeroes.
  it("reports a recorded reingest where the registry runs no pipeline in the request", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [userLayer()] } },
      "/v1/layers/reingest": {
        body: { queued: "alice-personal", queued_at: "2026-08-25T00:00:00Z" },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Reingest" }));
    expect(await screen.findByTestId("reingest-recorded")).toBeTruthy();
  });

  // A snapshot whose every artifact collided with a published version is
  // refused whole with 409 ingest.immutable_violation. Nothing was accepted
  // and the layer is unchanged, and bumping the versions is the only thing
  // that clears it, so the arm names the colliding versions and offers no
  // retry.
  it("names the colliding versions where the whole snapshot was refused", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [userLayer()] } },
      "/v1/layers/reingest": {
        status: 409,
        body: {
          code: "ingest.immutable_violation",
          message:
            "same-version content conflict: platform/lint@1.0.0 already exists with different content",
          retryable: false,
          details: {
            conflicts: [
              {
                artifact_id: "platform/lint",
                version: "1.0.0",
                old_hash: "sha256:aaa",
                new_hash: "sha256:bbb",
                code: "ingest.immutable_violation",
              },
            ],
          },
        },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Reingest" }));
    await screen.findByLabelText("Reingest rejected");
    expect(screen.getByText("Nothing was ingested")).toBeTruthy();
    expect(screen.getByText("platform/lint@1.0.0")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Try again" })).toBeNull();
  });

  // A reingest inside a §4.7.2 freeze window is refused with ingest.frozen,
  // and the same endpoint takes the break-glass override, so the arm offers
  // it rather than leaving the reader with a refusal and no next action. The
  // registry requires a justification and the freeze rule two distinct
  // approvers, so the override carries all three.
  it("offers the break-glass override where a freeze window refused the reingest", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [userLayer()] } },
      "/v1/layers/reingest": {
        status: 409,
        body: {
          code: "ingest.frozen",
          message: "a freeze window is active",
          retryable: false,
        },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Reingest" }));
    await screen.findByLabelText(
      "Reingest alice-personal during a freeze window",
    );
    const override = screen.getByRole("button", {
      name: "Reingest during the freeze",
    });
    // The override stays held until the justification and two distinct
    // approvers are in place, because the registry refuses it without them.
    expect(override.hasAttribute("disabled")).toBe(true);
    fireEvent.change(screen.getByLabelText("Justification"), {
      target: { value: "incident 7" },
    });
    fireEvent.change(screen.getByLabelText("First approver"), {
      target: { value: "alice@acme.com" },
    });
    expect(override.hasAttribute("disabled")).toBe(true);
    fireEvent.change(screen.getByLabelText("Second approver"), {
      target: { value: "bob@acme.com" },
    });
    fireEvent.click(override);
    await waitFor(() => {
      expect(bodies.at(-1)).toBe(
        JSON.stringify({
          break_glass: true,
          justification: "incident 7",
          approvers: ["alice@acme.com", "bob@acme.com"],
        }),
      );
    });
  });

  // The refusal annotates one row of the panel, so it is drawn as a row
  // annotation rather than as a page-level failure block: a leading REFUSED
  // marker, the statement beside it, and the recovery at the band's right
  // edge on the statement's own line.
  it("draws a refused reingest as a row annotation with its marker leading and its controls at the right edge", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [userLayer()] } },
      "/v1/layers/reingest": {
        status: 503,
        body: {
          code: "registry.unavailable",
          message: "the store is unreachable",
          retryable: true,
        },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Reingest" }));
    const refused = await screen.findByLabelText("Reingest refused");
    expect(refused.className).toContain("banner-annotation");
    // The marker leads the band.
    const marker = refused.firstElementChild as HTMLElement;
    expect(marker.className).toContain("badge-danger");
    expect(marker.textContent).toBe("REFUSED");
    // Both controls sit together in the band's own action cluster rather than
    // stacked under the statement.
    const actions = refused.querySelector(".banner-actions");
    expect(actions).not.toBeNull();
    const retry = within(refused).getByRole("button", { name: "Try again" });
    const dismiss = within(refused).getByRole("button", { name: "Dismiss" });
    expect(actions?.contains(retry)).toBe(true);
    expect(actions?.contains(dismiss)).toBe(true);
    // Try again is the bordered recovery and Dismiss is drawn plain beside it.
    expect(retry.className).not.toContain("button-plain");
    expect(dismiss.className).toContain("button-plain");
  });

  // Every other reingest refusal carries its own remediation in the
  // envelope, and the codes the pipeline answers with have different next
  // actions, so the arm presents the envelope's message and suggested action
  // rather than one line that fits none of them.
  it("presents a refused reingest with the envelope’s own message and remediation", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [userLayer()] } },
      "/v1/layers/reingest": {
        status: 422,
        body: {
          code: "ingest.lint_failed",
          message: "3 artifacts failed the lint gate",
          retryable: false,
          suggested_action: "Fix the reported manifests and reingest.",
        },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Reingest" }));
    const refused = await screen.findByLabelText("Reingest refused");
    expect(refused.textContent).toContain("3 artifacts failed the lint gate");
    expect(refused.textContent).toContain(
      "Fix the reported manifests and reingest.",
    );
    expect(refused.textContent).toContain("ingest.lint_failed");
    // The envelope reports that the condition does not clear on its own, so
    // the arm offers no retry.
    expect(screen.queryByRole("button", { name: "Try again" })).toBeNull();
  });

  // §13.10 supports serving the registry behind a gateway, and a refusal
  // written by that gateway rather than by the registry carries a status and
  // no §6.10 envelope. The code is the machine-readable fact the panel puts
  // in front of an operator, so a response that carried none is reported by
  // its status rather than given a code the registry never sent.
  it("reports a refusal that carried no error envelope by its status alone", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [userLayer()] } },
      "/v1/layers/reingest": {
        status: 403,
        text: "<html><body>403 Forbidden</body></html>",
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Reingest" }));
    const refused = await screen.findByLabelText("Reingest refused");
    expect(refused.textContent).toContain("HTTP 403");
    expect(refused.textContent).not.toContain("registry.unavailable");
    // A 403 is a decision, so the band states no recovery that repeats it.
    expect(screen.queryByRole("button", { name: "Try again" })).toBeNull();
  });

  // The same refusal at a server-side status is transient, and nothing in the
  // response says so, so the status is what the arm reads. Reporting it as
  // permanent withholds the retry that clears it.
  it("offers the retry on a codeless refusal whose status is server-side", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [userLayer()] } },
      "/v1/layers/reingest": {
        status: 503,
        text: "<html><body>503 Service Unavailable</body></html>",
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Reingest" }));
    const refused = await screen.findByLabelText("Reingest refused");
    expect(refused.textContent).toContain("HTTP 503");
    expect(
      within(refused).getByRole("button", { name: "Try again" }),
    ).toBeTruthy();
  });

  // The trigger disables itself for as long as its request is open, which
  // takes focus off it, and the banner the request settles into takes its own
  // controls away when it is dismissed. Focus left on the document body puts
  // the reader back at the top of the page, so the row hands it back to the
  // control the reingest was started from.
  it("returns focus to the row’s Reingest control when the request settles and when the refusal is dismissed", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [userLayer()] } },
      "/v1/layers/reingest": {
        status: 502,
        body: {
          code: "ingest.source_unreachable",
          message: "the source could not be reached",
          retryable: true,
        },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    const trigger = screen.getByRole("button", { name: "Reingest" });
    trigger.focus();
    fireEvent.click(trigger);
    // Disabling the focused control is what the browser does while the
    // request is open, and it leaves focus on the document body.
    (document.activeElement as HTMLElement | null)?.blur();
    const refused = await screen.findByLabelText("Reingest refused");
    await waitFor(() => {
      expect(document.activeElement).toBe(trigger);
    });
    // Dismissing the banner unmounts the control the press was made on, and
    // the row hands focus back to the same trigger.
    const dismiss = within(refused).getByRole("button", { name: "Dismiss" });
    dismiss.focus();
    fireEvent.click(dismiss);
    await waitFor(() => {
      expect(screen.queryByLabelText("Reingest refused")).toBeNull();
    });
    await waitFor(() => {
      expect(document.activeElement).toBe(
        screen.getByRole("button", { name: "Reingest" }),
      );
    });
  });

  // The cap refusal carries the limit and the caller's current count, and
  // this is where the user created the layer, so the count is rendered here
  // rather than arriving as the generic failure every other refusal gets.
  it("renders the layer limit and the current count where a registration exceeds the cap", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [userLayer()] } },
      "POST /v1/layers": {
        status: 429,
        body: {
          code: "quota.layer_count_exceeded",
          message: "user-defined layer cap of 3 reached for alice@acme.com",
          details: { limit: 3, current: 3 },
        },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Register layer" }));
    fireEvent.change(screen.getByLabelText("Layer ID"), {
      target: { value: "alice-extra" },
    });
    fireEvent.submit(screen.getByTestId("register-form"));
    const refusal = await screen.findByLabelText("Layer limit reached");
    expect(refusal.textContent).toContain("3 of 3");
    expect(screen.getByText("quota.layer_count_exceeded")).toBeTruthy();
  });

  // The register form is taller than the dialog and its body scrolls, so a
  // refusal drawn under the last field lands below the fold and the submit
  // reads as a control that did nothing. The refusal is drawn ahead of the
  // fields, is scrolled to, and takes focus.
  it("puts a refused registration ahead of the form fields, scrolls to it, and gives it focus", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [userLayer()] } },
      "POST /v1/layers": {
        status: 400,
        body: {
          code: "registry.invalid_argument",
          message: "id and source_type are required",
        },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Register layer" }));
    fireEvent.submit(screen.getByTestId("register-form"));
    const refusal = await screen.findByTestId("register-refusal");
    expect(refusal.textContent).toContain("registry.invalid_argument");
    // DOCUMENT_POSITION_FOLLOWING: the field comes after the refusal, so the
    // refusal is above the fields rather than under them.
    expect(
      refusal.compareDocumentPosition(screen.getByLabelText("Layer ID")) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    expect(scrolledIntoView).toContain(refusal);
    expect(document.activeElement).toBe(refusal);
  });

  // A refusal carrying a §6.10 envelope is an answer from the registry, so
  // the banner heads it as a refusal and names what was not created. The
  // heading for a failure carrying no envelope stays with the transport
  // failure, which is the one where the registry really did not answer.
  it("heads a refused registration as a refusal and a transport failure as no answer", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [userLayer()] } },
      "POST /v1/layers": {
        status: 400,
        body: {
          code: "registry.invalid_argument",
          message: "id and source_type are required",
        },
      },
    });
    goTo("#/layers");
    const refused = render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Register layer" }));
    fireEvent.submit(screen.getByTestId("register-form"));
    const refusal = await screen.findByTestId("register-refusal");
    expect(refusal.textContent).toContain(
      "The registry refused this registration and no layer was created.",
    );
    expect(refusal.textContent).not.toContain("did not answer");
    refused.unmount();

    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [userLayer()] } },
      "POST /v1/layers": { rejects: true },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Register layer" }));
    fireEvent.submit(screen.getByTestId("register-form"));
    const unreachable = await screen.findByTestId("register-refusal");
    expect(unreachable.textContent).toContain(
      "The registry did not answer this request.",
    );
  });

  // A refusal names the request that was sent. Editing the form invalidates
  // it, so the banner is dropped on the next change rather than standing
  // over fields the reader has since corrected.
  it("drops a refused registration when the reader edits the form", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [userLayer()] } },
      "POST /v1/layers": {
        status: 400,
        body: {
          code: "registry.invalid_argument",
          message: "id and source_type are required",
        },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    fireEvent.click(screen.getByRole("button", { name: "Register layer" }));
    fireEvent.submit(screen.getByTestId("register-form"));
    await screen.findByTestId("register-refusal");
    // A field the refusal named, corrected.
    fireEvent.change(screen.getByLabelText("Layer ID"), {
      target: { value: "a-new-id" },
    });
    expect(screen.queryByTestId("register-refusal")).toBeNull();

    // The source segments are buttons rather than inputs and emit no change
    // event, so they clear the refusal on their own.
    fireEvent.submit(screen.getByTestId("register-form"));
    await screen.findByTestId("register-refusal");
    fireEvent.click(screen.getByRole("radio", { name: "Local folder" }));
    expect(screen.queryByTestId("register-refusal")).toBeNull();
  });

  // The recovery surface answers how long is left before erasure, so every
  // row states when the layer was unregistered, the date it is erased on,
  // and how much of the §8.4 window remains. A row inside the accent window
  // says so, because that is the row to act on today.
  it("lists what is still recoverable with its erase date and restores it", async () => {
    const unregisteredAt = new Date(Date.now() - 28 * 24 * 60 * 60 * 1000);
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": {
        body: {
          layers: [{ ...userLayer(), DeletedAt: unregisteredAt.toISOString() }],
        },
      },
      "/v1/layers/restore": { body: {} },
    });
    goTo("#/layers/deleted");
    render(<App />);
    const surface = await screen.findByLabelText("Recently unregistered");
    const erasesOn = localDate(
      new Date(unregisteredAt.getTime() + 30 * 24 * 60 * 60 * 1000),
    );
    expect(surface.textContent).toContain(localDate(unregisteredAt));
    expect(surface.textContent).toContain(erasesOn);
    const left = screen.getByTestId("days-left-alice-personal");
    expect(left.textContent).toBe("2d left");
    expect(left.className).toContain("accent");
    // The source is on the same record, so the row names where the layer
    // came from rather than its identifier alone.
    expect(surface.textContent).toContain("/Users/alice/registry");
    fireEvent.click(await screen.findByRole("button", { name: "Restore" }));
    await waitFor(() => {
      expect(
        requests.some(
          (r) => r.url.startsWith("/v1/layers/restore") && r.method === "POST",
        ),
      ).toBe(true);
    });
  });

  // The restore table's columns are fixed proportions floored at the width
  // they are drawn at, so a content column narrower than its header ran the
  // "Unregistered" label out of its cell and into "Erased on". The table
  // scrolls sideways inside its own container the way the layer panel's does.
  it("puts the restore table in a container that scrolls sideways", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [] } },
      "/v1/layers?deleted=true": {
        body: {
          layers: [{ ...userLayer(), DeletedAt: new Date().toISOString() }],
        },
      },
    });
    goTo("#/layers/deleted");
    render(<App />);
    await screen.findByLabelText("Recently unregistered");
    const table = document.querySelector("table.restore-table") as HTMLElement;
    const container = table.parentElement as HTMLElement;
    expect(container.classList.contains("table-scroll")).toBe(true);
    expect(container.tabIndex).toBe(0);
    expect(container.getAttribute("aria-label")).toBe("Recoverable layers");
  });

  // The restore table names its rows the same way the layer panel does, so
  // the identifier carries the row-name treatment here too (§13.10).
  it("marks the layer identifier as the name of the row on the restore table", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [] } },
      "/v1/layers?deleted=true": {
        body: {
          layers: [{ ...userLayer(), DeletedAt: new Date().toISOString() }],
        },
      },
    });
    goTo("#/layers/deleted");
    render(<App />);
    await screen.findByLabelText("Recently unregistered");
    const name = screen.getByText("alice-personal");
    expect(name.tagName).toBe("TD");
    expect(name.classList.contains("layer-name")).toBe(true);
  });

  // A restore is a write like every other write in the panel, so it reports
  // what it did. The restored row leaves the table and the empty state that
  // replaces it names no layer, so the outcome is stated in a live region
  // that names the layer and the precedence it came back at.
  it("reports a committed restore with the precedence it returned to", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      // The list read answers with the restored layer back in it, which is
      // where the announced precedence is read from.
      "/v1/layers": { body: { layers: [userLayer(), adminLayer()] } },
      "/v1/layers?deleted=true": {
        body: {
          layers: [{ ...userLayer(), DeletedAt: new Date().toISOString() }],
        },
      },
      "/v1/layers/restore": { body: { restored: "alice-personal" } },
    });
    goTo("#/layers/deleted");
    render(<App />);
    await screen.findByLabelText("Recently unregistered");
    // The region is mounted before the write lands, so the announcement is
    // in the accessibility tree at the moment its text arrives.
    const region = screen.getByTestId("restore-announcement");
    expect(region.getAttribute("role")).toBe("status");
    expect(region.textContent).toBe("");

    fireEvent.click(await screen.findByRole("button", { name: "Restore" }));
    await waitFor(() => {
      expect(screen.getByTestId("restore-announcement").textContent).toBe(
        "alice-personal is restored at order 1 of 2.",
      );
    });
    // Reported on the page as well, rather than to assistive technology
    // alone.
    expect(screen.getByTestId("restore-announcement").className).toContain(
      "banner",
    );
  });

  // The Restore button leaves the table with the row it restored, so a
  // keyboard reader who pressed it is left on the document body. Focus lands
  // on the heading, beside the live region the restore reports itself in.
  it("hands focus to the heading when a restore takes the row away", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [userLayer(), adminLayer()] } },
      "/v1/layers?deleted=true": {
        body: {
          layers: [{ ...userLayer(), DeletedAt: new Date().toISOString() }],
        },
      },
      "/v1/layers/restore": { body: { restored: "alice-personal" } },
    });
    goTo("#/layers/deleted");
    render(<App />);
    await screen.findByLabelText("Recently unregistered");
    fireEvent.click(await screen.findByRole("button", { name: "Restore" }));
    // Focus is asserted once both reads the restore issues have answered,
    // because a surface that swaps itself out for its own loading state
    // takes the heading with it and drops focus again on the way through.
    await waitFor(() => {
      expect(screen.getByTestId("restore-announcement").textContent).toContain(
        "is restored",
      );
    });
    expect(document.activeElement).toBe(
      screen.getByRole("heading", { name: "Recently unregistered" }),
    );
  });

  // Restore is the only action this surface carries, so the state it reaches
  // when nothing is recoverable names the missing layer rather than the erase
  // the reader never performs.
  it("names the absent restorable layer when nothing is recoverable", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [userLayer()] } },
      "/v1/layers?deleted=true": { body: { layers: [] } },
    });
    goTo("#/layers/deleted");
    render(<App />);
    const surface = await screen.findByLabelText("Recently unregistered");
    const title = surface.querySelector(".empty-title") as HTMLElement;
    expect(title.textContent).toBe("No layers to restore");
    expect(surface.textContent).not.toContain("Nothing to erase");
  });

  // A refused restore reports the refusal alone. Where a successful restore
  // came first, its outcome is dropped rather than left standing beside a
  // refusal of the next one.
  it("drops the restore outcome when a later restore is refused", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [userLayer()] } },
      "/v1/layers?deleted=true": {
        body: {
          layers: [{ ...userLayer(), DeletedAt: new Date().toISOString() }],
        },
      },
      "/v1/layers/restore": {
        status: 409,
        body: {
          code: "registry.conflict",
          message: "artifact ID already exists",
        },
      },
    });
    goTo("#/layers/deleted");
    render(<App />);
    await screen.findByLabelText("Recently unregistered");
    fireEvent.click(await screen.findByRole("button", { name: "Restore" }));
    const refusal = await screen.findByRole("alert");
    expect(refusal.textContent).toContain("registry.conflict");
    expect(screen.getByTestId("restore-announcement").textContent).toBe("");
  });

  // The accent is what the surface reserves for a layer about to be erased,
  // so a row with most of its window left draws its bar in the neutral tone
  // and only a row inside the threshold turns the date, the count, and the
  // bar accent together.
  it("accents the erase clock only inside the threshold", async () => {
    const day = 24 * 60 * 60 * 1000;
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": {
        body: {
          layers: [
            {
              ...userLayer(),
              ID: "alice-roomy",
              DeletedAt: new Date(Date.now() - day / 2).toISOString(),
            },
            {
              ...userLayer(),
              ID: "alice-expiring",
              DeletedAt: new Date(Date.now() - 28 * day).toISOString(),
            },
          ],
        },
      },
    });
    goTo("#/layers/deleted");
    render(<App />);
    await screen.findByLabelText("Recently unregistered");

    const [roomy, expiring] = Array.from(
      document.querySelectorAll(".depleting"),
    );
    expect(screen.getByTestId("days-left-alice-roomy").textContent).toBe("30d");
    expect(roomy.className).not.toContain("depleting-urgent");

    expect(screen.getByTestId("days-left-alice-expiring").textContent).toBe(
      "2d left",
    );
    expect(expiring.className).toContain("depleting-urgent");

    // The date is the third element of the cell's tone, so off the threshold
    // it is quiet like the count beside it and only the urgent row sets it in
    // the accent. A date in ink on every row is the loudest thing in the cell
    // and leaves the accent nothing to contrast against.
    const dates = Array.from(
      document.querySelectorAll(".erase-clock > :first-child"),
    );
    expect(dates[0].className).toBe("mono quiet");
    expect(screen.getByTestId("days-left-alice-roomy").className).toBe(
      "mono quiet",
    );
    expect(dates[1].className).toBe("mono accent");
  });

  // The erase deadline is a clock the reader reads at a glance: the date, how
  // much of the window is left drawn between them, and the count. Drawn as a
  // block under the date, the bar sat directly beneath it and read as an
  // underline of the date rather than as a gauge, and an ISO date in that
  // position read as a serial number rather than as a day on the calendar.
  it("draws the erase deadline as a dated clock on one row", async () => {
    const unregisteredAt = new Date(Date.now() - 22 * 24 * 60 * 60 * 1000);
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": {
        body: {
          layers: [{ ...userLayer(), DeletedAt: unregisteredAt.toISOString() }],
        },
      },
    });
    goTo("#/layers/deleted");
    render(<App />);
    const surface = await screen.findByLabelText("Recently unregistered");
    const erases = new Date(
      unregisteredAt.getTime() + 30 * 24 * 60 * 60 * 1000,
    );
    // The date names its month rather than numbering it, and the ISO date it
    // was rendered as is nowhere in the cell.
    const clock = surface.querySelector(".erase-clock") as HTMLElement;
    expect(clock.textContent).toContain(localDate(erases));
    expect(clock.textContent).not.toContain(erases.toISOString().slice(0, 10));
    expect(clock.textContent).not.toContain(
      `${String(erases.getFullYear())}-`,
    );
    // The bar is between the date and the count, so the row reads as a clock
    // rather than as a date with a rule under it.
    const cells = Array.from(clock.children);
    expect(cells[0].textContent).toBe(localDate(erases));
    expect(cells[1].className).toContain("depleting");
    expect(cells[2]).toBe(screen.getByTestId("days-left-alice-personal"));
    expect(cells[2].textContent).toBe("8d");
    // The compact count is a gauge label, so the phrase it stands for is
    // still what a reader who hears the row is told.
    expect(cells[3].className).toContain("assistive-only");
    expect(cells[3].textContent).toBe("8 days left");
  });

  // A layer unregistered earlier the same day is looked for by someone who
  // did it minutes ago, so the row states the time of day. The date of a day
  // the reader is already on tells them nothing.
  it("states a same-day unregister as the time of day", async () => {
    const unregisteredAt = new Date(Date.now() - 60 * 1000);
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": {
        body: {
          layers: [{ ...userLayer(), DeletedAt: unregisteredAt.toISOString() }],
        },
      },
    });
    goTo("#/layers/deleted");
    render(<App />);
    const surface = await screen.findByLabelText("Recently unregistered");
    const pad = (value: number) => String(value).padStart(2, "0");
    expect(surface.textContent).toContain(
      `today, ${pad(unregisteredAt.getHours())}:${pad(unregisteredAt.getMinutes())}`,
    );
  });

  // The unregister confirmation promises the full §8.4 window and names the
  // erase date. A layer unregistered moments ago has a fraction of a day
  // already spent, so a count that dropped the part-day reported one day less
  // than both the promise and the erase date in its own row.
  it("reports the whole window on the day the layer is unregistered", async () => {
    const unregisteredAt = new Date(Date.now() - 30 * 1000);
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": {
        body: {
          layers: [{ ...userLayer(), DeletedAt: unregisteredAt.toISOString() }],
        },
      },
    });
    goTo("#/layers/deleted");
    render(<App />);
    const surface = await screen.findByLabelText("Recently unregistered");
    expect(screen.getByTestId("days-left-alice-personal").textContent).toBe(
      "30d",
    );
    expect(surface.textContent).toContain(
      localDate(new Date(unregisteredAt.getTime() + 30 * 24 * 60 * 60 * 1000)),
    );
  });

  // Both recovery dates are calendar days, and a calendar day only means
  // anything in a zone. A reader west of UTC late in the evening is on the
  // day before the UTC one, so a UTC calendar day told them a layer they had
  // just unregistered went tomorrow and put the erase deadline a day off the
  // calendar they read the row against. The dates are the reader's own.
  it("dates the recovery window on the reader's calendar rather than UTC", async () => {
    const zone = "America/Los_Angeles";
    // The surface reads the zone through the platform's local-time getters,
    // which Node resolves from TZ on each call, so the case fixes a zone west
    // of UTC rather than depending on the one the suite happens to run in.
    vi.stubEnv("TZ", zone);
    try {
      // 06:00 UTC is 23:00 the previous day in the zone, so the instant's
      // calendar day differs between the two whatever day the suite runs on.
      const evening = new Date();
      evening.setUTCHours(6, 0, 0, 0);
      const unregisteredAt = new Date(
        evening.getTime() - 28 * 24 * 60 * 60 * 1000,
      );
      const erases = new Date(
        unregisteredAt.getTime() + 30 * 24 * 60 * 60 * 1000,
      );
      stubRegistry({
        "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
        "/v1/layers": {
          body: {
            layers: [{ ...userLayer(), DeletedAt: unregisteredAt.toISOString() }],
          },
        },
      });
      goTo("#/layers/deleted");
      render(<App />);
      const surface = await screen.findByLabelText("Recently unregistered");
      expect(surface.textContent).toContain(zonedDate(unregisteredAt, zone));
      expect(surface.textContent).toContain(zonedDate(erases, zone));
      // The UTC calendar day is a day ahead of both, and neither is stated.
      expect(surface.textContent).not.toContain(
        unregisteredAt.toISOString().slice(0, 10),
      );
      expect(surface.textContent).not.toContain(
        erases.toISOString().slice(0, 10),
      );
    } finally {
      vi.unstubAllEnvs();
    }
  });

  // The recovery surface is a page of its own under the panel. It carries a
  // table and the panel carries another, so rendered together the reader gets
  // two stacked tables and the precedence label and the layer rows are pushed
  // down by the height of this one. The panel's link is what leads there, and
  // the trail above the surface leads back.
  it("gives the recovery surface a page of its own under the panel", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [userLayer()] } },
      "/v1/layers?deleted=true": {
        body: {
          layers: [
            {
              ...userLayer(),
              ID: "alice-old",
              DeletedAt: new Date().toISOString(),
            },
          ],
        },
      },
    });
    goTo("#/layers");
    render(<App />);
    const panel = await screen.findByLabelText("Layer panel");
    const link = screen.getByTestId("recoverable-link");
    await waitFor(() => {
      expect(link.querySelector(".badge")?.textContent).toBe("1");
    });
    // The link leads to the surface rather than opening it inside the panel,
    // so the panel carries its own table alone.
    expect(link.getAttribute("href")).toBe("#/layers/deleted");
    expect(screen.queryByLabelText("Recently unregistered")).toBe(null);
    expect(within(panel).getAllByRole("table")).toHaveLength(1);

    goTo("#/layers/deleted");
    const surface = await screen.findByLabelText("Recently unregistered");
    // The layer table is not on this screen.
    expect(screen.queryByLabelText("Layer panel")).toBe(null);
    expect(document.body.textContent).not.toContain(
      "drag or press the arrow keys",
    );
    expect(
      within(surface).getByRole("heading", { name: "Recently unregistered" })
        .tagName,
    ).toBe("H1");
    const trail = within(surface).getByLabelText("Breadcrumb");
    expect(trail.textContent).toBe("Layers/Recently unregistered");
    expect(
      within(trail).getByRole("link", { name: "Layers" }).getAttribute("href"),
    ).toBe("#/layers");
    // What a restore does closes the page, because the button alone names
    // neither the precedence it returns to nor the refusal it can meet.
    expect(surface.textContent).toContain(
      "Restoring puts the layer back at its previous precedence.",
    );
  });

  // The restore table and the layer table are one link apart, so this table's
  // column names read in the same section-label style rather than as
  // sentence-case sans beside the panel's mono headers.
  it("draws every column header in the section-label style", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [userLayer()] } },
    });
    goTo("#/layers/deleted");
    render(<App />);
    const surface = await screen.findByLabelText("Recently unregistered");
    const headers = Array.from(
      within(surface).getByRole("table").querySelectorAll("thead th"),
    );
    expect(
      headers.map(
        (header) => header.querySelector(".label")?.textContent ?? "",
      ),
    ).toEqual([
      "Layer",
      "Source",
      "Artifacts",
      "Unregistered",
      "Erased on",
      "",
    ]);
    // The restore column carries a control that names itself, so it takes no
    // column title.
    expect(headers[5].textContent).toBe("");
  });

  // How much comes back on a restore is the second question this surface
  // answers, so the artifact count has a column of its own. No layer read
  // carries the count, so the column states it is unreported on every row
  // rather than being left out: a column that is not drawn reads as a datum
  // that does not exist.
  it("carries an artifact-count column that states the count is unreported", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [userLayer()] } },
    });
    goTo("#/layers/deleted");
    render(<App />);
    const surface = await screen.findByLabelText("Recently unregistered");
    const headers = Array.from(
      within(surface).getByRole("table").querySelectorAll("thead th"),
    ).map((header) => header.querySelector(".label")?.textContent ?? "");
    const at = headers.indexOf("Artifacts");
    expect(at).toBeGreaterThan(-1);
    const row = within(surface).getByRole("table").querySelector("tbody tr");
    const cells = Array.from(row?.querySelectorAll("td") ?? []);
    expect(cells).toHaveLength(headers.length);
    expect(cells[at].textContent).toBe("unreported");
  });

  // A record carrying no tombstone time states that rather than computing a
  // date from a value it does not hold.
  it("states no erase date where the record carries no unregistered time", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [{ ...userLayer(), DeletedAt: null }] } },
    });
    goTo("#/layers/deleted");
    render(<App />);
    const surface = await screen.findByLabelText("Recently unregistered");
    expect(surface.textContent).toContain(
      "The registry reported no erase date.",
    );
  });
});

/** moveHandleLabel is the accessible name of one row's reorder handle. */
function moveHandleLabel(id: string): string {
  return `Move ${id}: press the up or down arrow key`;
}

/** dragRowOnto drives the panel's drag-to-reorder: the row is picked up by
 * its handle and dropped onto another row, and the move commits on the drop.
 * It returns the drag data store the drag was carried on, which a browser
 * supplies on every drag event and jsdom supplies on none.
 */
/** dragRowOver picks a row up and holds it over another row without dropping
 * it, which is the state the drop indicator is drawn in. */
function dragRowOver(from: string, onto: string): void {
  const dataTransfer = dragStore();
  fireEvent.dragStart(layerHandle(from), { dataTransfer });
  fireEvent.dragOver(layerRow(onto), { dataTransfer });
}

function dragRowOnto(from: string, onto: string): DragStore {
  const source = layerHandle(from);
  const target = layerRow(onto);
  const dataTransfer = dragStore();
  fireEvent.dragStart(source, { dataTransfer });
  fireEvent.dragOver(target, { dataTransfer });
  fireEvent.drop(target, { dataTransfer });
  return dataTransfer;
}

/** DragStore records what a dragstart writes to the drag data store. */
interface DragStore {
  effectAllowed: string;
  data: Record<string, string>;
  setData: (format: string, value: string) => void;
  getData: (format: string) => string;
}

function dragStore(): DragStore {
  const store: DragStore = {
    effectAllowed: "uninitialized",
    data: {},
    setData: (format, value) => {
      store.data[format] = value;
    },
    getData: (format) => store.data[format] ?? "",
  };
  return store;
}

/** layerHandle is one row's reorder handle, which is the only part of the row
 * a pointer drag picks up. */
function layerHandle(id: string): HTMLElement {
  return screen.getByLabelText(moveHandleLabel(id));
}

function layerRow(id: string): HTMLElement {
  const row = layerHandle(id).closest("tr");
  if (row === null) {
    throw new Error(`no layer row for ${id}`);
  }
  return row;
}

/** reingestTrigger is one row's Reingest button, which the action bar draws
 * before the overflow control. It is read off the row rather than by name,
 * because every row carries a button with the same name. */
function reingestTrigger(id: string): HTMLButtonElement {
  const button = layerRow(id).querySelector(".row-action-bar button");
  if (button === null) {
    throw new Error(`no Reingest trigger for ${id}`);
  }
  return button as HTMLButtonElement;
}

/** details is the source cell's location lines on one row, each read whole
 * across the run the cell may clip and the run it always draws. */
function details(row: HTMLElement): string[] {
  return Array.from(
    row.querySelectorAll(".source-detail"),
    (line) => line.textContent ?? "",
  );
}

/** lastIngestCell is the Last ingest cell of one row, which the table
 * positions after the drag handle, the layer, the source, and the
 * visibility. */
function lastIngestCell(id: string): HTMLElement {
  return layerRow(id).querySelectorAll("td")[4] as HTMLElement;
}

function adminLayer(owner = ""): Record<string, unknown> {
  return {
    ID: "company",
    SourceType: "git",
    Repo: "git@github.com:acme/company.git",
    Ref: "main",
    Order: 1,
    UserDefined: false,
    Owner: owner,
    Organization: true,
  };
}

function userLayer(owner = "alice@acme.com"): Record<string, unknown> {
  return {
    ID: "alice-personal",
    SourceType: "local",
    LocalPath: "/Users/alice/registry",
    Order: 2,
    UserDefined: true,
    Owner: owner,
  };
}

/** openRowActions opens one row's overflow control, which is where Edit and
 * Unregister live so that every row keeps to a single line. */
function openRowActions(layerID = "alice-personal"): void {
  fireEvent.click(
    screen.getByRole("button", { name: `More actions for ${layerID}` }),
  );
}

/** bobLayer is a user-defined layer another subject owns. The list read is
 * unfiltered, so it reaches the panel alongside the caller's own, and a
 * reorder that named it would be refused whole. */
function bobLayer(): Record<string, unknown> {
  return {
    ID: "bob-personal",
    SourceType: "local",
    LocalPath: "/Users/bob/registry",
    Order: 4,
    UserDefined: true,
    Owner: "bob@acme.com",
  };
}

/** scratchLayer is a second user-defined layer, which a reorder case needs so
 * the moving layer has a sibling inside its own class. */
function scratchLayer(owner = "alice@acme.com"): Record<string, unknown> {
  return {
    ID: "alice-scratch",
    SourceType: "local",
    LocalPath: "/Users/alice/scratch",
    Order: 3,
    UserDefined: true,
    Owner: owner,
  };
}

describe("the command palette", () => {
  const artifact = {
    id: "platform/review",
    type: "skill",
    version: "1.2.0",
    content_hash: "sha256:abc",
    manifest_body: "# Review\n",
    frontmatter: manifestDoc,
  };

  function palettePage(
    results: Record<string, unknown>[],
    total = results.length,
  ): void {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": { body: emptyDomain },
      "/v1/search_artifacts": { body: { total_matched: total, results } },
      "/v1/load_artifact": { body: artifact },
      "/v1/dependents": { body: { edges: [] } },
      "/v1/layers": { body: { layers: [] } },
    });
  }

  // The palette is reachable from anywhere: the shell's search trigger opens
  // it and so does the accelerator, and it lists artifacts alone, because
  // domain navigation is the sidebar tree's.
  it("opens from the trigger and from ⌘K, lists what matched, and opens a row", async () => {
    palettePage(
      [{ id: "platform/review", type: "skill", version: "1.2.0" }],
      4,
    );
    render(<App />);
    fireEvent.click(await screen.findByTestId("search-trigger"));
    const panel = screen.getByTestId("palette");
    fireEvent.change(within(panel).getByLabelText("Search artifacts"), {
      target: { value: "review" },
    });
    expect((await screen.findByTestId("palette-heading")).textContent).toBe(
      "Artifacts · 1 of 4",
    );
    expect(within(panel).getByText("review")).toBeTruthy();
    fireEvent.keyDown(panel, { key: "Enter" });
    expect(window.location.hash).toBe("#/artifact/platform%2Freview");
    expect(screen.queryByTestId("palette")).toBeNull();
    // The accelerator opens the same panel from the surface the navigation
    // landed on.
    fireEvent.keyDown(window, { key: "k", metaKey: true });
    expect(screen.getByTestId("palette")).toBeTruthy();
  });

  // The type and the version hold the row's right edge rather than running on
  // as prose after the path, the field row states the count on that same edge,
  // and each keystroke in the footer is drawn as the key it names.
  it("aligns the type and version on the row's right edge and counts the matches in the field", async () => {
    palettePage(
      [{ id: "platform/review", type: "skill", version: "1.2.0" }],
      4,
    );
    render(<App />);
    fireEvent.click(await screen.findByTestId("search-trigger"));
    const panel = screen.getByTestId("palette");
    fireEvent.change(within(panel).getByLabelText("Search artifacts"), {
      target: { value: "review" },
    });
    const aside = await within(panel).findByTestId("palette-row-aside");
    // The aside is the row's last element, so nothing follows the column that
    // holds the edge.
    expect(aside.parentElement?.lastElementChild).toBe(aside);
    expect(within(aside).getByText("SKILL").className).toContain("badge");
    expect(within(aside).getByText("v1.2.0")).toBeTruthy();
    // The count the heading states also sits in the field row.
    expect(screen.getByTestId("palette-count").textContent).toBe("1 of 4");
    const footer = screen.getByTestId("palette-footer");
    expect(
      within(footer)
        .getAllByText(/^(↑|↓|⏎|⌘⏎|esc)$/)
        .every((key) => key.className.includes("key-cap")),
    ).toBe(true);
  });

  // The just-opened panel draws each filter as its own chip under a label
  // naming what they are for. Run together as one line of prose, the three
  // read as a sentence about filtering rather than as three things a reader
  // can type into the query above them.
  it("draws each inline filter as its own chip", async () => {
    palettePage([]);
    render(<App />);
    fireEvent.click(await screen.findByTestId("search-trigger"));
    const syntax = screen.getByTestId("palette-syntax");
    expect(syntax.textContent).toContain("Filter inline:");
    const chips = Array.from(syntax.querySelectorAll(".palette-syntax-chip"));
    expect(chips.map((chip) => chip.textContent)).toEqual([
      "type:skill",
      "tag:review",
      "scope:platform",
    ]);
    // A separator between the filters is what a single run-together line
    // needs and a set of chips does not.
    expect(syntax.textContent).not.toContain("·");
  });

  // The panel covers the shell, so Tab has to cycle inside it. Its result
  // rows carry tabIndex -1, because the arrows move the highlight while the
  // query field keeps focus, and a trap that counted them as Tab stops never
  // saw focus reach the stop it treats as the last one: Tab from the field
  // walked out onto the sidebar behind the scrim.
  it("keeps Tab and Shift+Tab inside the panel when the rows hold no Tab stop", async () => {
    palettePage([
      { id: "platform/review", type: "skill", version: "1.2.0" },
      { id: "platform/lint", type: "skill", version: "1.0.0" },
    ]);
    render(<App />);
    fireEvent.click(await screen.findByTestId("search-trigger"));
    const panel = screen.getByTestId("palette");
    const field = within(panel).getByLabelText("Search artifacts");
    fireEvent.change(field, { target: { value: "platform" } });
    await within(panel).findByTestId("palette-heading");
    field.focus();
    for (const shiftKey of [false, true]) {
      const tab = createEvent.keyDown(document, { key: "Tab", shiftKey });
      fireEvent(document, tab);
      // The panel cancels the key, so the browser's own Tab order never runs
      // and focus stays on the field the panel wrapped it back to.
      expect(tab.defaultPrevented).toBe(true);
      expect(document.activeElement).toBe(field);
    }
  });

  // The inline filter syntax is the palette's form of the pills the search
  // surface renders, and it reaches the same endpoint arguments.
  it("carries the inline filter syntax into the search request", async () => {
    palettePage([]);
    render(<App />);
    fireEvent.click(await screen.findByTestId("search-trigger"));
    fireEvent.change(
      within(screen.getByTestId("palette")).getByLabelText("Search artifacts"),
      {
        target: { value: "type:skill tag:review scope:platform lint" },
      },
    );
    await waitFor(() => {
      expect(lastSearch().get("query")).toBe("lint");
    });
    expect(lastSearch().get("type")).toBe("skill");
    expect(lastSearch().get("tags")).toBe("review");
    expect(lastSearch().get("scope")).toBe("platform");
  });

  // ⌘⏎ hands the query to the search surface, which is the one place the
  // whole result set is listed, and esc closes the panel over the page it
  // was opened from.
  it("hands the query to the search surface on ⌘⏎ and closes on esc", async () => {
    palettePage([{ id: "platform/review", type: "skill" }]);
    render(<App />);
    fireEvent.click(await screen.findByTestId("search-trigger"));
    const panel = screen.getByTestId("palette");
    fireEvent.change(within(panel).getByLabelText("Search artifacts"), {
      target: { value: "review" },
    });
    fireEvent.keyDown(panel, { key: "Enter", metaKey: true });
    expect(window.location.hash).toBe("#/search/review");
    await screen.findByLabelText("Search");
    fireEvent.keyDown(window, { key: "k", metaKey: true });
    fireEvent.keyDown(screen.getByTestId("palette"), { key: "Escape" });
    expect(screen.queryByTestId("palette")).toBeNull();
  });

  // The reader's edits to the search surface's own field rewrite the hash
  // without a navigation, so the route the shell last parsed trails the
  // address bar. A palette handoff back to that trailing query still has to
  // stand the surface up on it: otherwise the field, the result list, and the
  // address bar name two different searches, and the link the reader copies
  // answers with results the page never drew (§13.10).
  it("stands the search surface up on a palette query the shell last parsed", async () => {
    palettePage([{ id: "platform/review", type: "skill" }]);
    render(<App />);
    await screen.findByTestId("search-trigger");
    // The route is entered from the catalog rather than landed on, so the
    // surface is standing on the query the reader then edits.
    goTo(searchHref("review"));
    const field = await screen.findByLabelText("Search artifacts");
    fireEvent.change(field, { target: { value: "lint" } });
    await waitFor(() => {
      expect(window.location.hash).toBe(searchHref("lint"));
    });
    fireEvent.keyDown(window, { key: "k", metaKey: true });
    const panel = screen.getByTestId("palette");
    fireEvent.change(within(panel).getByLabelText("Search artifacts"), {
      target: { value: "review" },
    });
    fireEvent.keyDown(panel, { key: "Enter", metaKey: true });
    expect(window.location.hash).toBe(searchHref("review"));
    await waitFor(() => {
      const surfaceField = screen.getByLabelText(
        "Search artifacts",
      ) as HTMLInputElement;
      expect(surfaceField.value).toBe("review");
    });
    await waitFor(() => {
      expect(lastSearch().get("query")).toBe("review");
    });
  });

  // Closing the panel hands focus back to the header's search trigger, and a
  // ⏎ the panel leaves uncancelled then activates that trigger as the
  // browser's default action for the key: the panel reopens over the artifact
  // or the search surface it just navigated to. Both arms consume the key, so
  // both cancel it.
  it("cancels the ⏎ it consumes so the trigger it returns focus to is not activated", async () => {
    palettePage([{ id: "platform/review", type: "skill" }]);
    render(<App />);
    const trigger = await screen.findByTestId("search-trigger");
    trigger.focus();
    fireEvent.click(trigger);
    const panel = screen.getByTestId("palette");
    fireEvent.change(within(panel).getByLabelText("Search artifacts"), {
      target: { value: "review" },
    });
    await screen.findByTestId("palette-heading");
    const open = createEvent.keyDown(panel, { key: "Enter" });
    fireEvent(panel, open);
    expect(open.defaultPrevented).toBe(true);
    expect(window.location.hash).toBe("#/artifact/platform%2Freview");
    expect(screen.queryByTestId("palette")).toBeNull();
    // ⌘⏎ leaves through the same trigger, so it cancels the key as well.
    fireEvent.click(screen.getByTestId("search-trigger"));
    const reopened = screen.getByTestId("palette");
    fireEvent.change(within(reopened).getByLabelText("Search artifacts"), {
      target: { value: "review" },
    });
    const all = createEvent.keyDown(reopened, { key: "Enter", metaKey: true });
    fireEvent(reopened, all);
    expect(all.defaultPrevented).toBe(true);
    expect(window.location.hash).toBe("#/search/review");
    expect(screen.queryByTestId("palette")).toBeNull();
  });

  // The panel covers the shell, so it owns focus while it is open and returns
  // it to the trigger when it closes. A reader who opened it on the keyboard
  // otherwise resumes at the top of the document.
  it("takes focus into the query field and returns it to the trigger on esc", async () => {
    palettePage([]);
    render(<App />);
    const trigger = await screen.findByTestId("search-trigger");
    trigger.focus();
    fireEvent.click(trigger);
    const panel = screen.getByTestId("palette");
    expect(document.activeElement).toBe(
      within(panel).getByLabelText("Search artifacts"),
    );
    // The trap is announced as well as implemented: a reader is told the shell
    // behind the panel is inert, which is what the layer modals declare too.
    expect(panel.getAttribute("role")).toBe("dialog");
    expect(panel.getAttribute("aria-modal")).toBe("true");
    fireEvent.keyDown(panel, { key: "Escape" });
    expect(screen.queryByTestId("palette")).toBeNull();
    expect(document.activeElement).toBe(trigger);
  });

  // The panel is pinned to the viewport, so the surface under it is held
  // still while it is open: a wheel over the scrim otherwise slides the page
  // around behind a panel that does not move with it.
  it("holds the page still while the panel is open and gives it back on close", async () => {
    palettePage([]);
    render(<App />);
    const trigger = await screen.findByTestId("search-trigger");
    expect(document.documentElement.style.overflow).toBe("");
    fireEvent.click(trigger);
    const panel = screen.getByTestId("palette");
    expect(document.documentElement.style.overflow).toBe("hidden");
    fireEvent.keyDown(panel, { key: "Escape" });
    expect(document.documentElement.style.overflow).toBe("");
  });

  // The accelerator opens the panel from a surface where nothing holds focus,
  // and the panel has no opening control to hand focus back to there. Focus
  // left on the document restarts the next Tab at the top of the page, so the
  // header's own trigger stands in. Opening a result is the other case: the
  // reader resumes on the surface the panel navigated to rather than on the
  // header, so focus lands on the content region.
  it("returns focus to the trigger after ⌘K and to the content region after a result opens", async () => {
    palettePage([{ id: "platform/review", type: "skill" }]);
    render(<App />);
    const trigger = await screen.findByTestId("search-trigger");
    // Nothing holds focus, which is where the accelerator is pressed from.
    (document.activeElement as HTMLElement | null)?.blur();
    fireEvent.keyDown(window, { key: "k", metaKey: true });
    const panel = screen.getByTestId("palette");
    expect(document.activeElement).toBe(
      within(panel).getByLabelText("Search artifacts"),
    );
    fireEvent.keyDown(panel, { key: "Escape" });
    expect(screen.queryByTestId("palette")).toBeNull();
    expect(document.activeElement).toBe(trigger);
    // A result opened from the panel replaces the surface underneath it, and
    // the content region is where the reader resumes reading it.
    fireEvent.click(trigger);
    const reopened = screen.getByTestId("palette");
    fireEvent.change(within(reopened).getByLabelText("Search artifacts"), {
      target: { value: "review" },
    });
    await screen.findByTestId("palette-heading");
    fireEvent.keyDown(reopened, { key: "Enter" });
    expect(window.location.hash).toBe("#/artifact/platform%2Freview");
    expect(document.activeElement).toBe(document.getElementById("main-content"));
  });

  // The handoff carries the filters the palette parsed rather than the line
  // read back as free text: the search surface issues the request the palette
  // issued and renders the filters as the pills the syntax teaches.
  it("reproduces the palette’s filters and result set on the search surface", async () => {
    palettePage([{ id: "platform/review", type: "skill" }], 3);
    render(<App />);
    fireEvent.click(await screen.findByTestId("search-trigger"));
    const panel = screen.getByTestId("palette");
    fireEvent.change(within(panel).getByLabelText("Search artifacts"), {
      target: { value: "type:skill tag:review scope:platform lint" },
    });
    await waitFor(() => {
      expect(lastSearch().get("type")).toBe("skill");
    });
    fireEvent.keyDown(panel, { key: "Enter", metaKey: true });
    await screen.findByLabelText("Search");
    await waitFor(() => {
      expect(lastSearch().get("query")).toBe("lint");
    });
    expect(lastSearch().get("type")).toBe("skill");
    expect(lastSearch().get("tags")).toBe("review");
    expect(lastSearch().get("scope")).toBe("platform");
    // The parsed filters are the pills the surface opens with, so the reader
    // can drop one from the row the palette's syntax taught.
    expect(screen.getByText("scope: platform")).toBeTruthy();
    expect(screen.getByLabelText("Remove the review filter")).toBeTruthy();
    expect(screen.getByLabelText("Remove the skill filter")).toBeTruthy();
  });

  // A query that matched nothing offers the recovery path and says nothing
  // about what a different caller would have seen.
  it("states no match without hinting that anything is hidden", async () => {
    palettePage([], 0);
    render(<App />);
    fireEvent.click(await screen.findByTestId("search-trigger"));
    const panel = screen.getByTestId("palette");
    // The just-opened panel teaches the filter syntax before a query is run.
    expect(screen.getByTestId("palette-syntax").textContent).toContain(
      "type:skill",
    );
    fireEvent.change(within(panel).getByLabelText("Search artifacts"), {
      target: { value: "nothingmatches" },
    });
    expect(
      await screen.findByText(/Nothing matched \u201cnothingmatches\u201d/),
    ).toBeTruthy();
    expect(within(panel).queryByText(/hidden/i)).toBeNull();
    expect(within(panel).queryByText(/permission/i)).toBeNull();
  });

  // A reader who cannot see the panel is told what the query settled on. The
  // count reaches them as the read lands, and the region is mounted before
  // the query is typed so the text arrives in a region the accessibility tree
  // already holds.
  it("announces the settled result count", async () => {
    palettePage(
      [{ id: "platform/review", type: "skill", version: "1.2.0" }],
      4,
    );
    render(<App />);
    fireEvent.click(await screen.findByTestId("search-trigger"));
    const panel = screen.getByTestId("palette");
    const region = within(panel).getByTestId("palette-announcement");
    expect(region.getAttribute("role")).toBe("status");
    expect(region.getAttribute("aria-live")).toBe("polite");
    expect(region.className).toContain("assistive-only");
    expect(region.textContent).toBe("");
    fireEvent.change(within(panel).getByLabelText("Search artifacts"), {
      target: { value: "review" },
    });
    await within(panel).findByTestId("palette-heading");
    expect(region.textContent).toBe("1 of 4 artifacts matched.");
  });

  // The no-match arm replaces the whole listbox with a sentence, which is what
  // the field's aria-activedescendant pointed into, so it is announced rather
  // than left to a list that is no longer drawn.
  it("announces the no-match arm when the list empties", async () => {
    palettePage([], 0);
    render(<App />);
    fireEvent.click(await screen.findByTestId("search-trigger"));
    const panel = screen.getByTestId("palette");
    const region = within(panel).getByTestId("palette-announcement");
    fireEvent.change(within(panel).getByLabelText("Search artifacts"), {
      target: { value: "zzzznotathing" },
    });
    await within(panel).findByText(/Nothing matched “zzzznotathing”/);
    expect(region.textContent).toBe(
      "No artifact matched \u201czzzznotathing\u201d.",
    );
  });

  // The footer advertises ⏎ for as long as the panel is open, so the arm with
  // no row to open answers it with the one action it does offer, which is the
  // handoff the visible button performs.
  it("runs the query on the search surface when ⏎ has no row to open", async () => {
    palettePage([], 0);
    render(<App />);
    fireEvent.click(await screen.findByTestId("search-trigger"));
    const panel = screen.getByTestId("palette");
    fireEvent.change(within(panel).getByLabelText("Search artifacts"), {
      target: { value: "nothingmatches" },
    });
    await screen.findByText(/Nothing matched “nothingmatches”/);
    fireEvent.keyDown(panel, { key: "Enter" });
    expect(screen.queryByTestId("palette")).toBeNull();
    expect(window.location.hash).toBe("#/search/nothingmatches");
  });

  // A refused read leaves the panel with no row to open either, so it carries
  // the same handoff the no-match arm carries and answers ⏎ with it. A panel
  // that offered only a retry of the read that just failed made ⏎ a key the
  // footer names and nothing answers.
  it("offers the search surface, on the button and on ⏎, when the read is refused", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": { body: emptyDomain },
      "/v1/search_artifacts": { rejects: true },
      "/v1/layers": { body: { layers: [] } },
    });
    render(<App />);
    fireEvent.click(await screen.findByTestId("search-trigger"));
    const panel = screen.getByTestId("palette");
    fireEvent.change(within(panel).getByLabelText("Search artifacts"), {
      target: { value: "deploy" },
    });
    await within(panel).findByText("The registry did not answer this request.");
    expect(
      within(panel).getByRole("button", { name: "Run it on the search surface" }),
    ).toBeTruthy();
    // The retry of the refused read stays beside it: the two are different
    // recoveries, and the handoff does not replace the retry.
    expect(within(panel).getByRole("button", { name: "Try again" })).toBeTruthy();
    fireEvent.keyDown(panel, { key: "Enter" });
    expect(screen.queryByTestId("palette")).toBeNull();
    expect(window.location.hash).toBe("#/search/deploy");
  });

  // The no-match line quotes the query so a reader can see where it ends, and
  // it advises dropping a filter only when the line carries one to drop.
  it("quotes the query and withholds filter advice on a line with no filter", async () => {
    palettePage([], 0);
    render(<App />);
    fireEvent.click(await screen.findByTestId("search-trigger"));
    const panel = screen.getByTestId("palette");
    const field = within(panel).getByLabelText("Search artifacts");
    fireEvent.change(field, { target: { value: "the" } });
    const plain = await screen.findByText(/Nothing matched \u201cthe\u201d/);
    expect(plain.textContent).toBe("Nothing matched \u201cthe\u201d");
    expect(
      within(panel).getByText(/Try fewer words/).textContent,
    ).toBe(
      "Try fewer words, or check the spelling. Search covers artifact names, descriptions, and tags.",
    );
    // The same line with a filter on it gains the advice to drop one.
    fireEvent.change(field, { target: { value: "type:skill the" } });
    const filtered = await screen.findByText(/drop a filter from the line/);
    expect(filtered.textContent).toBe(
      "Try fewer words, or drop a filter from the line. Search covers artifact names, descriptions, and tags.",
    );
  });

  // A query that matched nothing is a result the panel states as fully as a
  // query that matched something: the field carries "0 of 0", the arm names
  // the query at heading weight, and a quiet line says what search looked at.
  // Drawn as one sentence with no count, the panel collapsed under the field
  // and told a reader neither how many rows the query reached nor why.
  it("counts, names, and explains the query that matched nothing", async () => {
    palettePage([], 0);
    render(<App />);
    fireEvent.click(await screen.findByTestId("search-trigger"));
    const panel = screen.getByTestId("palette");
    fireEvent.change(within(panel).getByLabelText("Search artifacts"), {
      target: { value: "span covrage" },
    });
    const empty = await within(panel).findByTestId("palette-empty");
    // The count sits on the field's right edge, the same edge it holds when
    // rows came back.
    expect(within(panel).getByTestId("palette-count").textContent).toBe(
      "0 of 0",
    );
    const heading = within(empty).getByText(
      "Nothing matched \u201cspan covrage\u201d",
    );
    expect(heading.className).toContain("palette-empty-heading");
    expect(
      within(empty).getByText(/Search covers artifact names/).className,
    ).toContain("palette-empty-body");
    // \u2191\u2193 and \u23ce-to-open name keys no row answers here, so the legend reduces
    // to the handoff and the way out.
    const footer = within(panel).getByTestId("palette-footer");
    expect(footer.textContent).toBe("\u23cesearch anywayescclose");
  });

  // A reopened panel is a fresh one. A panel that held the line the reader
  // last typed puts the caret at its end with nothing selected, so the
  // "open and type" gesture appends to a finished query and searches for the
  // two run together, and the just-opened state is never reached again.
  it("opens on an empty query and shows the just-opened state again", async () => {
    palettePage([{ id: "platform/review", type: "skill" }], 1);
    render(<App />);
    fireEvent.click(await screen.findByTestId("search-trigger"));
    const field = () =>
      within(screen.getByTestId("palette")).getByLabelText(
        "Search artifacts",
      ) as HTMLInputElement;
    fireEvent.change(field(), { target: { value: "review" } });
    await screen.findByTestId("palette-heading");
    fireEvent.keyDown(screen.getByTestId("palette"), { key: "Escape" });

    fireEvent.keyDown(window, { key: "k", metaKey: true });
    expect(field().value).toBe("");
    expect(screen.getByTestId("palette-syntax")).toBeTruthy();
    // Typing into the reopened panel searches for what was typed rather than
    // for it appended to the discarded line.
    fireEvent.change(field(), { target: { value: "lint" } });
    await waitFor(() => {
      expect(lastSearch().get("query")).toBe("lint");
    });
  });

  // The empty list is fed by the panel's own handoffs, so it says what fills
  // it. Stating that no query has been run on this page is contradicted by the
  // search surface behind the scrim, which is listing the results of one the
  // reader just ran there.
  it("names what fills the recent queries rather than denying a query the page ran", async () => {
    palettePage([{ id: "eng/deploy", type: "context" }], 1);
    goTo(searchHref(""));
    render(<App />);
    const surface = await screen.findByLabelText("Search");
    fireEvent.change(within(surface).getByLabelText("Search artifacts"), {
      target: { value: "deploy" },
    });
    await waitFor(() => {
      expect(lastSearch().get("query")).toBe("deploy");
    });

    fireEvent.keyDown(window, { key: "k", metaKey: true });
    const panel = screen.getByTestId("palette");
    expect(panel.textContent).not.toContain("No query has been run");
    expect(
      within(panel).getByText(
        "A query is listed here once it opens a result or reaches the search surface.",
      ),
    ).toBeTruthy();
  });

  // The queries the panel has acted on outlive the opening that ran them,
  // because they are what the just-opened state lists.
  it("lists a query it ran among the recent queries of a later opening", async () => {
    palettePage([{ id: "platform/review", type: "skill" }], 1);
    render(<App />);
    fireEvent.click(await screen.findByTestId("search-trigger"));
    fireEvent.change(
      within(screen.getByTestId("palette")).getByLabelText("Search artifacts"),
      { target: { value: "review" } },
    );
    await screen.findByTestId("palette-heading");
    fireEvent.keyDown(screen.getByTestId("palette"), { key: "Enter" });
    expect(window.location.hash).toBe("#/artifact/platform%2Freview");

    fireEvent.keyDown(window, { key: "k", metaKey: true });
    const panel = screen.getByTestId("palette");
    const recent = within(panel).getByRole("option", { name: "review" });
    // Picking one fills the field with it, which is the point of listing it.
    fireEvent.click(recent);
    expect(
      (within(panel).getByLabelText("Search artifacts") as HTMLInputElement)
        .value,
    ).toBe("review");
  });

  // The just-opened panel has no row to open and no query to hand to the
  // search surface, so the footer names neither. It names the keys that act:
  // the arrows walk the recent queries and ⏎ runs the highlighted one. A
  // legend carried over from the typed state advertised ⏎ open and ⌘⏎ all
  // results over a panel where ⌘⏎ landed on the search surface's browse
  // listing, which is the results of nothing the reader asked for.
  it("names the keys the just-opened panel answers and runs the highlighted recent query", async () => {
    palettePage([{ id: "platform/review", type: "skill" }], 1);
    render(<App />);
    fireEvent.click(await screen.findByTestId("search-trigger"));
    const type = (query: string) => {
      fireEvent.change(
        within(screen.getByTestId("palette")).getByLabelText(
          "Search artifacts",
        ),
        { target: { value: query } },
      );
    };
    // Two queries run from this page, so the reopened panel lists both.
    for (const query of ["review", "lint"]) {
      type(query);
      await screen.findByTestId("palette-heading");
      // ⏎ opens the highlighted row, which is what records the query. The
      // panel is reopened once the surface it opened has settled, because
      // entering a surface closes whatever panel covers it.
      fireEvent.keyDown(screen.getByTestId("palette"), { key: "Enter" });
      await screen.findByLabelText("Artifact viewer");
      fireEvent.keyDown(window, { key: "k", metaKey: true });
    }

    const panel = screen.getByTestId("palette");
    const footer = within(panel).getByTestId("palette-footer");
    expect(footer.textContent).toBe("↑↓navigate⏎runescclose");
    expect(footer.textContent).not.toContain("⌘⏎");

    // The arrows walk the recent queries, and the field names the one they
    // hold for a reader who cannot see the highlight.
    const field = within(panel).getByLabelText("Search artifacts");
    const list = within(panel).getByRole("listbox", { name: "Recent queries" });
    const held = () =>
      within(list)
        .getAllByRole("option")
        .findIndex((row) => row.getAttribute("aria-selected") === "true");
    expect(held()).toBe(0);
    fireEvent.keyDown(panel, { key: "ArrowDown" });
    expect(held()).toBe(1);
    expect(field.getAttribute("aria-activedescendant")).toBe(
      within(list).getAllByRole("option")[1].id,
    );

    // ⏎ runs the highlighted one, which puts it back in the field and issues
    // the read again.
    fireEvent.keyDown(panel, { key: "Enter" });
    expect((field as HTMLInputElement).value).toBe("review");
    await waitFor(() => {
      expect(lastSearch().get("query")).toBe("review");
    });
    expect(window.location.hash).not.toContain("#/search/");
  });

  // Nothing typed is nothing to carry, so ⌘⏎ is refused rather than landing
  // on the search surface's browse listing with the panel closed behind it.
  it("refuses the search handoff while the line is empty", async () => {
    palettePage([], 0);
    render(<App />);
    fireEvent.click(await screen.findByTestId("search-trigger"));
    const panel = screen.getByTestId("palette");
    for (const metaKey of [true, false]) {
      fireEvent.keyDown(panel, { key: "Enter", metaKey });
    }
    expect(window.location.hash).not.toContain("#/search/");
    expect(screen.getByTestId("palette")).toBeTruthy();
    // With no query run on this page there is no recent to walk either, so
    // the footer names the one key that acts.
    expect(within(panel).getByTestId("palette-footer").textContent).toBe(
      "escclose",
    );
  });

  // A route change the panel did not issue is the reader entering a surface
  // they mean to read: the browser's back step, an address-bar edit, or a
  // link under the scrim. The panel closes on it the way it closes on the
  // result it opens, rather than covering the entered surface with the
  // previous query's matches.
  it("closes when a route change it did not issue enters another surface", async () => {
    palettePage([{ id: "platform/review", type: "skill" }], 1);
    goTo(artifactHref("platform/review"));
    render(<App />);
    await screen.findByTestId("search-trigger");
    fireEvent.keyDown(window, { key: "k", metaKey: true });
    fireEvent.change(
      within(screen.getByTestId("palette")).getByLabelText("Search artifacts"),
      { target: { value: "review" } },
    );
    await screen.findByTestId("palette-heading");
    goTo(layersHref);
    await waitFor(() => {
      expect(screen.queryByTestId("palette")).toBeNull();
    });
  });
});

describe("the shell’s identity cluster", () => {
  beforeEach(() => {
    window.localStorage.clear();
    window.document.documentElement.removeAttribute("data-theme");
  });

  // The shell names the registry the page is served from, links the
  // documentation, and carries the trigger that opens the palette.
  it("names the registry, links the docs, and carries the search trigger", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": { body: emptyDomain },
    });
    render(<App />);
    expect((await screen.findByTestId("registry-host")).textContent).toBe(
      window.location.host,
    );
    expect(
      screen.getByRole("link", { name: /Docs/ }).getAttribute("href"),
    ).toContain("https://");
    expect(screen.getByTestId("search-trigger").textContent).toContain("⌘K");
  });

  // Every place the shell says "search" draws the magnifier as inline SVG.
  // The Unicode magnifier it replaces sets at a fraction of its nominal size
  // and reads as a stray mark, so a text glyph in any of the three is the
  // defect this pins.
  it("draws the magnifier as an icon in the trigger, the palette, and the search field", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/search_artifacts": { body: { total_matched: 0 } },
    });
    goTo("#/search/");
    render(<App />);
    const surface = await screen.findByLabelText("Search");
    expect(surface.querySelectorAll("svg.magnifier").length).toBe(1);

    const trigger = screen.getByTestId("search-trigger");
    expect(trigger.querySelectorAll("svg.magnifier").length).toBe(1);

    fireEvent.click(trigger);
    const panel = await screen.findByTestId("palette");
    expect(panel.querySelectorAll("svg.magnifier").length).toBe(1);

    expect(window.document.body.textContent).not.toContain("⌕");
  });

  // The appearance preference is the client's own state, and it is applied by
  // stamping data-theme on the root element, which is what overrides the
  // visitor's prefers-color-scheme in both directions.
  it("pins a theme onto the root element and returns it to the system setting", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/load_domain": { body: emptyDomain },
    });
    render(<App />);
    const cluster = await screen.findByTestId("account-trigger");
    expect(cluster.textContent).toContain("alice@acme.com");
    fireEvent.click(cluster);
    const menu = screen.getByTestId("account-menu");
    // The appearance switch is that same segmented control, so the pinned
    // preference is the segment raised onto the surface colour.
    const appearance = within(menu).getByRole("group", { name: "Appearance" });
    expect(appearance.className.split(" ")).toContain("segmented");
    expect(
      within(appearance).getByRole("button", { name: "System" }).className,
    ).toBe("segment segment-on");
    fireEvent.click(within(menu).getByRole("button", { name: "Dark" }));
    expect(
      within(appearance).getByRole("button", { name: "Dark" }).className,
    ).toBe("segment segment-on");
    expect(window.document.documentElement.getAttribute("data-theme")).toBe(
      "dark",
    );
    expect(window.localStorage.getItem("podium.theme")).toBe("dark");
    fireEvent.click(within(menu).getByRole("button", { name: "System" }));
    expect(window.document.documentElement.hasAttribute("data-theme")).toBe(
      false,
    );
  });

  // A deployment that resolves no subject renders no identity cluster, and
  // the appearance preference is client state that predicts no server
  // outcome, so the shell stands it on its own there. Without it the reader
  // on a standalone registry is pinned to prefers-color-scheme.
  it("offers the appearance preference where no subject resolves", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture() },
      "/v1/load_domain": { body: emptyDomain },
    });
    render(<App />);
    expect(await screen.findByTestId("registry-host")).toBeTruthy();
    expect(screen.queryByTestId("account-trigger")).toBeNull();

    fireEvent.click(screen.getByTestId("appearance-trigger"));
    const menu = screen.getByTestId("appearance-menu");
    // The popover carries the segmented control and nothing else. A
    // role="menu" over three toggle buttons is announced as a menu holding no
    // items, so the popover claims no role and the group stands on the
    // control, which is the pattern the rest of the shell uses.
    expect(menu.getAttribute("role")).toBeNull();
    const appearance = within(menu).getByRole("group", { name: "Appearance" });
    expect(appearance.className.split(" ")).toContain("segmented");
    fireEvent.click(within(menu).getByRole("button", { name: "Light" }));
    expect(window.document.documentElement.getAttribute("data-theme")).toBe(
      "light",
    );
    expect(window.localStorage.getItem("podium.theme")).toBe("light");
  });

  // A trigger that toggles aria-expanded and stops there tells a reader that
  // something opened without saying where it stands. Both topbar triggers
  // point at the element they own, the same wiring the layer table's overflow
  // control carries.
  //
  // Spec: §13.10
  it("points the appearance trigger at the popover it owns and claims no menu", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture() },
      "/v1/load_domain": { body: emptyDomain },
    });
    render(<App />);
    const trigger = await screen.findByTestId("appearance-trigger");
    // The popover holds a labelled group of toggle buttons rather than menu
    // items. aria-haspopup names the kind of popup a trigger opens and its
    // unqualified "true" is defined as equivalent to "menu", so a trigger
    // carrying either value promises arrow-key item navigation and
    // menuitemradio announcement that the group does not provide. The
    // appearance trigger therefore carries no aria-haspopup at all.
    expect(trigger.hasAttribute("aria-haspopup")).toBe(false);
    const controls = trigger.getAttribute("aria-controls");
    expect(controls).toBeTruthy();
    fireEvent.click(trigger);
    const popover = screen.getByTestId("appearance-menu");
    expect(popover.id).toBe(controls);
    expect(popover.getAttribute("role")).toBeNull();
    within(popover).getByRole("group", { name: "Appearance" });
  });

  // The identity cluster's trigger carries the same wiring, and its popover
  // is a menu, so it names that kind.
  //
  // Spec: §13.10
  it("names the menu the identity cluster owns", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/load_domain": { body: emptyDomain },
    });
    render(<App />);
    const trigger = await screen.findByTestId("account-trigger");
    expect(trigger.getAttribute("aria-haspopup")).toBe("menu");
    const controls = trigger.getAttribute("aria-controls");
    expect(controls).toBeTruthy();
    fireEvent.click(trigger);
    expect(screen.getByTestId("account-menu").id).toBe(controls);
  });

  // Every label the shell writes for a reader is sentence case, and the
  // appearance options are labels rather than identifiers. The stored
  // preference stays lowercase because it is the value stamped on the root
  // element, so the control must carry its own labels.
  //
  // Spec: §13.10
  it("labels the appearance options in sentence case", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture() },
      "/v1/load_domain": { body: emptyDomain },
    });
    render(<App />);
    fireEvent.click(await screen.findByTestId("appearance-trigger"));
    const appearance = within(
      screen.getByTestId("appearance-menu"),
    ).getByRole("group", { name: "Appearance" });
    expect(
      within(appearance)
        .getAllByRole("button")
        .map((option) => option.textContent),
    ).toEqual(["System", "Light", "Dark"]);
  });

  // The topbar menus are transient popovers, and every other overlay in the
  // shell leaves on Escape and on a press outside it. One whose only exit is
  // its own trigger stands over the surface for the rest of the session, and
  // one that survives the reader entering another surface covers a surface
  // they deliberately opened.
  it("dismisses the appearance menu on Escape, on an outside press, and on a route change", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture() },
      "/v1/load_domain": { body: emptyDomain },
      "/v1/search_artifacts": { body: { total_matched: 0 } },
    });
    render(<App />);
    const trigger = await screen.findByTestId("appearance-trigger");

    fireEvent.click(trigger);
    expect(trigger.getAttribute("aria-expanded")).toBe("true");
    fireEvent.keyDown(document, { key: "Escape" });
    expect(screen.queryByTestId("appearance-menu")).toBeNull();
    expect(trigger.getAttribute("aria-expanded")).toBe("false");
    expect(document.activeElement).toBe(trigger);

    fireEvent.click(trigger);
    fireEvent.pointerDown(document.body);
    expect(screen.queryByTestId("appearance-menu")).toBeNull();

    fireEvent.click(trigger);
    expect(screen.getByTestId("appearance-menu")).toBeTruthy();
    goTo(searchHref("deploy"));
    await waitFor(() => {
      expect(screen.queryByTestId("appearance-menu")).toBeNull();
    });
  });

  // The identity cluster's menu is the same popover behind a different
  // trigger, so it carries the same dismissal paths.
  it("dismisses the account menu on Escape", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/load_domain": { body: emptyDomain },
    });
    render(<App />);
    const trigger = await screen.findByTestId("account-trigger");
    fireEvent.click(trigger);
    expect(screen.getByTestId("account-menu")).toBeTruthy();
    fireEvent.keyDown(document, { key: "Escape" });
    expect(screen.queryByTestId("account-menu")).toBeNull();
    expect(document.activeElement).toBe(trigger);
  });

  // The menu carries the layer quota, read from the §4.7.8 endpoint the
  // registry gates on no role, so the caller sees the cap on how many layers
  // of their own they may hold.
  it("states the layer quota the registry reports", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/load_domain": { body: emptyDomain },
      "/v1/quota": {
        body: { tenant_id: "acme", limits: { MaxUserLayers: 3 } },
      },
    });
    render(<App />);
    fireEvent.click(await screen.findByTestId("account-trigger"));
    expect((await screen.findByTestId("layer-quota")).textContent).toBe(
      "3 user-defined layers",
    );
  });

  // A quota read that fails leaves the menu with no quota entry rather than a
  // figure no response carried.
  it("drops the quota entry where the read does not answer", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/load_domain": { body: emptyDomain },
      "/v1/quota": {
        status: 503,
        body: { code: "registry.unavailable", message: "down" },
      },
    });
    render(<App />);
    fireEvent.click(await screen.findByTestId("account-trigger"));
    await screen.findByTestId("account-menu");
    await waitFor(() => {
      expect(requests.some((r) => r.url === "/v1/quota")).toBe(true);
    });
    expect(screen.queryByTestId("layer-quota")).toBeNull();
  });

  // A tenant whose quota disables the cap holds any number of layers, which
  // the entry states rather than reporting the negative value.
  it("states a disabled cap as no limit", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/load_domain": { body: emptyDomain },
      "/v1/quota": { body: { limits: { MaxUserLayers: -1 } } },
    });
    render(<App />);
    fireEvent.click(await screen.findByTestId("account-trigger"));
    expect((await screen.findByTestId("layer-quota")).textContent).toBe(
      "no cap on your layers",
    );
  });
});

describe("the trimmed listing", () => {
  const trimmed = {
    path: "platform",
    subdomains: [],
    notable: [
      { id: "platform/deploy", type: "skill" },
      { id: "platform/lint", type: "skill" },
    ],
    note: "The listing was trimmed to fit the response budget.",
  };

  /** heldBy is the catalog a domain holding `count` artifacts answers with. */
  function heldBy(path: string, count: number): string[] {
    return Array.from(
      { length: count },
      (_, i) => `${path}/svc${String(i + 1)}`,
    );
  }

  // The §4.5.5 notable_count cap trims the listing without a rendering note,
  // because the note covers the budget and depth reductions alone. The page
  // reads what the domain holds off the untruncated §4.5.2 catalog instead, so
  // a listing the cap trimmed is not presented as the whole domain.
  it("states the domain's own count where the cap trimmed the listing and the response carries no note", async () => {
    const capped = {
      path: "platform",
      subdomains: [],
      notable: heldBy("platform", 10).map((id) => ({ id, type: "skill" })),
    };
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": { body: capped },
      "/v1/catalog": { body: { ids: heldBy("platform", 24) } },
    });
    goTo("#/domain/platform");
    render(<App />);
    await screen.findByLabelText("Domain browser");
    expect(await screen.findByText("listing trimmed")).toBeTruthy();
    expect(screen.getByText("24 ARTIFACTS")).toBeTruthy();
    const line = await screen.findByTestId("listing-continuation");
    expect(line.textContent).toContain("10 of 24 artifacts shown.");
    expect(
      within(line)
        .getByRole("link", { name: "Search this domain" })
        .getAttribute("href"),
    ).toBe(searchHref("scope:platform"));
  });

  // A listing that carries everything the domain holds is not a trimmed one,
  // and the catalog read that establishes it draws neither the pill nor the
  // continuation line.
  it("draws no trimmed treatment where the catalog counts what the listing carries", async () => {
    const whole = {
      path: "platform",
      subdomains: [],
      notable: heldBy("platform", 3).map((id) => ({ id, type: "skill" })),
    };
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": { body: whole },
      "/v1/catalog": { body: { ids: heldBy("platform", 3) } },
    });
    goTo("#/domain/platform");
    render(<App />);
    await screen.findByLabelText("Domain browser");
    expect(await screen.findByText("3 ARTIFACTS")).toBeTruthy();
    await waitFor(() => {
      expect(requests.some((r) => r.url.startsWith("/v1/catalog"))).toBe(true);
    });
    expect(screen.queryByText("listing trimmed")).toBeNull();
    expect(screen.queryByTestId("listing-continuation")).toBeNull();
  });

  // The trimmed case is a pill among the header badges and a line at the end
  // of the list stating what is on the page against the domain's own count,
  // with a control that continues past the returned edge. The continuation is
  // a scoped search, because load_domain offers no lever over the notable
  // list.
  it("states the shown count against the total and continues into the scoped search", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": { body: trimmed },
      "/v1/catalog": { body: { ids: heldBy("platform", 21) } },
      "/v1/search_artifacts": { body: { total_matched: 21 } },
    });
    goTo("#/domain/platform");
    render(<App />);
    await screen.findByLabelText("Domain browser");
    // The marker reads as neither content nor error, so it takes the filled
    // chip in the metadata tone the counts beside it take rather than the
    // accent-outlined badge, which reads as a warning.
    const pill = screen.getByText("listing trimmed");
    expect(pill.className.split(" ")).toContain("badge-marker");
    expect(pill.className).not.toContain("badge-accent");
    const line = await screen.findByTestId("listing-continuation");
    await waitFor(() => {
      expect(line.textContent).toContain("2 of 21 artifacts shown.");
    });
    const cont = within(line).getByRole("link", { name: "Search this domain" });
    expect(cont.getAttribute("href")).toBe(searchHref("scope:platform"));
    goTo(searchHref("scope:platform"));
    await screen.findByLabelText("Search");
    await waitFor(() => {
      expect(
        requests.some(
          (r) =>
            r.url.startsWith("/v1/search_artifacts") &&
            r.url.includes("scope=platform"),
        ),
      ).toBe(true);
    });
    // Raising the subtree depth is what the control must not do: the notable
    // list is capped independently of it, so a deeper read returns no
    // artifact the reader does not already hold.
    expect(
      requests.some(
        (r) => r.url.startsWith("/v1/load_domain") && r.url.includes("depth=3"),
      ),
    ).toBe(false);
  });

  // The continuation hands the reader to the scoped search, which opens at
  // its own cap and cannot ask §5 for a top_k above the ceiling. A domain
  // holding more than that ceiling therefore has no listing that carries the
  // withheld artifacts, so the control names the search it opens instead of
  // promising a load that arrives with nothing more than the reader holds.
  it("names the scoped search it hands off to rather than promising the withheld artifacts", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        body: {
          path: "scale",
          subdomains: [],
          notable: heldBy("scale", 10).map((id) => ({ id, type: "skill" })),
        },
      },
      "/v1/catalog": { body: { ids: heldBy("scale", 60) } },
    });
    goTo("#/domain/scale");
    render(<App />);
    await screen.findByLabelText("Domain browser");
    const line = await screen.findByTestId("listing-continuation");
    await waitFor(() => {
      expect(line.textContent).toContain("10 of 60 artifacts shown.");
    });
    const cont = within(line).getByTestId("listing-continue");
    expect(cont.textContent).toBe("Search this domain");
    expect(cont.getAttribute("href")).toBe(searchHref("scope:scale"));
    // No wording on the row offers to load, or to produce the rest, because
    // the press loads no artifact and the rest is past what §5 serves.
    expect(line.textContent).not.toMatch(/load/i);
    expect(line.textContent).not.toMatch(/the rest/i);
  });

  // The registry root bounds nothing, so its continuation carries no scope
  // filter and the label names the catalog-wide search it opens.
  it("names the catalog-wide search at the registry root", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        body: {
          path: "",
          subdomains: [],
          notable: heldBy("", 10).map((id) => ({
            id: id.replace(/^\//, ""),
            type: "skill",
          })),
          note: "The listing was trimmed to fit the response budget.",
        },
      },
      "/v1/catalog": { body: { ids: heldBy("root", 60) } },
    });
    goTo("#/");
    render(<App />);
    await screen.findByLabelText("Domain browser");
    const cont = await screen.findByTestId("listing-continue");
    expect(cont.textContent).toBe("Search the catalog");
    expect(cont.getAttribute("href")).toBe(searchHref(""));
  });

  // The continuation is the listing's own last row rather than a note under
  // the page: it closes the same bordered card the artifacts are drawn in, so
  // it reads as the edge of the listing.
  it("closes the artifact list with the continuation as its final row", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": { body: trimmed },
      "/v1/catalog": { body: { ids: heldBy("platform", 21) } },
    });
    goTo("#/domain/platform");
    render(<App />);
    await screen.findByLabelText("Domain browser");
    const line = await screen.findByTestId("listing-continuation");
    const list = line.closest("ul.artifact-list");
    expect(list).not.toBeNull();
    expect(list?.lastElementChild).toBe(line);
    // The card the continuation closes is the one the artifacts are listed
    // in, rather than a second card of its own below them.
    expect(within(list as HTMLElement).getByText("platform/lint")).toBeTruthy();
  });

  // A domain with dozens of children is a map rather than a card grid, so the
  // subdomains become count tiles under a filter and the artifacts a sortable
  // table.
  it("switches to tiles and a sortable table past the at-scale threshold", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        body: {
          path: "platform",
          subdomains: Array.from({ length: 24 }, (_, i) => ({
            path: `platform/d${String(i)}`,
            name: `d${String(i)}`,
          })),
          notable: [
            {
              id: "platform/deploy",
              type: "skill",
              version: "2.0.0",
              source: "featured",
            },
            { id: "platform/lint", type: "rule", version: "1.0.0" },
          ],
        },
      },
    });
    goTo("#/domain/platform");
    render(<App />);
    const browser = await screen.findByLabelText("Domain browser");
    const showAll = within(browser).getByTestId("show-all-subdomains");
    expect(showAll.textContent).toBe("Show all 24 subdomains");
    // The disclosure over the tile grid is drawn quiet, so it does not read as
    // the loudest control on the surface (§13.10).
    expect(showAll.classList.contains("tile-more")).toBe(true);
    fireEvent.change(within(browser).getByLabelText("Filter subdomains"), {
      target: { value: "d1" },
    });
    expect(within(browser).queryByRole("link", { name: "d2" })).toBeNull();
    // The author's own picks keep their own heading, and the table sorts on
    // the column the sort control names.
    expect(
      within(browser).getByText("Curated by the domain author"),
    ).toBeTruthy();
    const tables = within(browser).getAllByLabelText("Artifacts");
    expect(
      within(tables[0]).getByRole("link", { name: "deploy" }),
    ).toBeTruthy();
  });

  // The at-scale table states each identifier under the domain the page is
  // on. The heading names that domain already, so a cell carrying the whole
  // identifier spends the column on a prefix every row shares (§13.10).
  it("states an at-scale artifact identifier under the current domain", async () => {
    const scope = "finance/accounts-payable/invoicing";
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        body: {
          path: scope,
          subdomains: Array.from({ length: 24 }, (_, i) => ({
            path: `${scope}/d${String(i)}`,
            name: `d${String(i)}`,
          })),
          notable: [
            { id: `${scope}/pay-invoice`, type: "skill", version: "2.0.0" },
            {
              id: `${scope}/disputes/hold-payment`,
              type: "rule",
              version: "1.0.0",
            },
          ],
        },
      },
    });
    goTo(`#/domain/${encodeURIComponent(scope)}`);
    render(<App />);
    const browser = await screen.findByLabelText("Domain browser");
    const table = within(browser).getByLabelText("Artifacts");

    // A row directly under the domain states its own name, and a row from a
    // subdomain states the levels between, which is what tells the two apart.
    const leaf = within(table).getByRole("link", { name: "pay-invoice" });
    const nested = within(table).getByRole("link", {
      name: "disputes/hold-payment",
    });
    expect(table.textContent).not.toContain(scope);
    // The link still addresses the whole identifier, and carries it for a
    // reader who needs the row's absolute name.
    expect(leaf.getAttribute("href")).toBe(artifactHref(`${scope}/pay-invoice`));
    expect(leaf.getAttribute("title")).toBe(`${scope}/pay-invoice`);
    expect(nested.getAttribute("href")).toBe(
      artifactHref(`${scope}/disputes/hold-payment`),
    );

    // The filter runs over what the column states, so a word from the shared
    // prefix matches no row rather than every row.
    const arthead = within(browser).getByRole("heading", {
      name: "Artifacts",
    }).parentElement as HTMLElement;
    fireEvent.change(within(arthead).getByLabelText("Filter in this domain"), {
      target: { value: "accounts-payable" },
    });
    expect(
      within(browser).getByText("Clear the filter or pick another type."),
    ).toBeTruthy();
    fireEvent.change(within(arthead).getByLabelText("Filter in this domain"), {
      target: { value: "disputes" },
    });
    expect(
      within(browser).getByRole("link", { name: "disputes/hold-payment" }),
    ).toBeTruthy();
    expect(
      within(browser).queryByRole("link", { name: "pay-invoice" }),
    ).toBeNull();
  });

  // A filter that matches nothing on the at-scale surface states the outcome
  // the way every other zero-result listing in the build does, and the section
  // count reports the filtered listing rather than the unfiltered total.
  it("states the outcome when an at-scale filter matches nothing", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        body: {
          path: "platform",
          subdomains: Array.from({ length: 24 }, (_, i) => ({
            path: `platform/d${String(i)}`,
            name: `d${String(i)}`,
          })),
          notable: [
            { id: "platform/deploy", type: "skill", version: "2.0.0" },
            { id: "platform/lint", type: "rule", version: "1.0.0" },
          ],
        },
      },
    });
    goTo("#/domain/platform");
    render(<App />);
    const browser = await screen.findByLabelText("Domain browser");

    const subhead = within(browser).getByRole("heading", {
      name: "Subdomains",
    }).parentElement as HTMLElement;
    expect(subhead.textContent).toContain("24");
    fireEvent.change(within(subhead).getByLabelText("Filter subdomains"), {
      target: { value: "zzz" },
    });
    expect(
      within(browser).getByText(
        "Clear the filter to see every subdomain.",
      ),
    ).toBeTruthy();
    // The grid is gone, the caption that describes its ordering goes with it,
    // and the section count reports the filtered listing.
    expect(
      within(browser).queryByRole("list", { name: "Subdomains" }),
    ).toBeNull();
    expect(within(browser).queryByText("Sorted by artifact count.")).toBeNull();
    expect(subhead.textContent).not.toContain("24");
    expect(subhead.textContent).toContain("0");

    const arthead = within(browser).getByRole("heading", {
      name: "Artifacts",
    }).parentElement as HTMLElement;
    fireEvent.change(within(arthead).getByLabelText("Filter in this domain"), {
      target: { value: "qqq" },
    });
    expect(
      within(browser).getByText(
        "Clear the filter or pick another type.",
      ),
    ).toBeTruthy();
    expect(within(browser).queryByLabelText("Artifacts")).toBeNull();

    // Clearing the filter restores the listing and the count.
    fireEvent.change(within(subhead).getByLabelText("Filter subdomains"), {
      target: { value: "" },
    });
    expect(
      within(browser).getByRole("list", { name: "Subdomains" }),
    ).toBeTruthy();
    expect(subhead.textContent).toContain("24");
  });

  // Both at-scale filters rewrite a result set whose count is drawn in a
  // heading and in the table body, where a reader who cannot see the page
  // reads neither. Each states its new count in a polite live region, the way
  // the search surface and the command palette state theirs.
  it("announces the at-scale filter counts", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        body: {
          path: "platform",
          subdomains: Array.from({ length: 24 }, (_, i) => ({
            path: `platform/d${String(i)}`,
            name: `d${String(i)}`,
          })),
          notable: [
            { id: "platform/deploy", type: "skill", version: "2.0.0" },
            { id: "platform/lint", type: "rule", version: "1.0.0" },
          ],
        },
      },
    });
    goTo("#/domain/platform");
    render(<App />);
    const browser = await screen.findByLabelText("Domain browser");

    // Both regions are mounted before a filter is typed and say nothing, so
    // the first announcement lands on a region already in the tree.
    const subs = within(browser).getByTestId("subdomain-filter-announcement");
    const arts = within(browser).getByTestId("artifact-filter-announcement");
    for (const region of [subs, arts]) {
      expect(region.getAttribute("role")).toBe("status");
      expect(region.getAttribute("aria-live")).toBe("polite");
      expect(region.textContent).toBe("");
    }

    const subhead = within(browser).getByRole("heading", {
      name: "Subdomains",
    }).parentElement as HTMLElement;
    fireEvent.change(within(subhead).getByLabelText("Filter subdomains"), {
      target: { value: "d1" },
    });
    // d1 and d10 through d19.
    expect(subs.textContent).toBe("11 of 24 subdomains matched.");
    fireEvent.change(within(subhead).getByLabelText("Filter subdomains"), {
      target: { value: "zzz" },
    });
    expect(subs.textContent).toBe("No subdomain matched.");
    fireEvent.change(within(subhead).getByLabelText("Filter subdomains"), {
      target: { value: "" },
    });
    expect(subs.textContent).toBe("");

    const arthead = within(browser).getByRole("heading", {
      name: "Artifacts",
    }).parentElement as HTMLElement;
    fireEvent.change(within(arthead).getByLabelText("Filter in this domain"), {
      target: { value: "lint" },
    });
    expect(arts.textContent).toBe("1 of 2 artifacts matched.");
    // A type chip narrows the same listing, so it reports through the same
    // region as the typed filter.
    fireEvent.change(within(arthead).getByLabelText("Filter in this domain"), {
      target: { value: "" },
    });
    expect(arts.textContent).toBe("");
    fireEvent.click(within(arthead).getByRole("button", { name: "skill" }));
    expect(arts.textContent).toBe("1 of 2 artifacts matched.");
  });

  // §4.5.5 caps the notable list, so the at-scale table filters a partial view
  // of the domain. A filter that matches nothing among the returned rows has
  // established nothing about the artifacts the response withheld, and
  // clearing the filter or changing the type loads none of them, so the table
  // states the reach of the filter and continues into the search bounded to
  // this domain carrying the same words.
  //
  // Spec: §13.10
  it("continues a filter over a trimmed at-scale listing into the scoped search", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        body: {
          path: "platform",
          subdomains: Array.from({ length: 24 }, (_, i) => ({
            path: `platform/d${String(i)}`,
            name: `d${String(i)}`,
          })),
          notable: Array.from({ length: 10 }, (_, i) => ({
            id: `platform/direct-${String(i + 1)}`,
            type: "skill",
          })),
        },
      },
      "/v1/catalog": {
        body: {
          ids: Array.from(
            { length: 25 },
            (_, i) => `platform/direct-${String(i + 1)}`,
          ),
        },
      },
    });
    goTo("#/domain/platform");
    render(<App />);
    const browser = await screen.findByLabelText("Domain browser");
    const arthead = within(browser).getByRole("heading", {
      name: "Artifacts",
    }).parentElement as HTMLElement;

    // An artifact past the returned edge is not reported as absent, and the
    // recovery offered is the one that reaches it.
    fireEvent.change(within(arthead).getByLabelText("Filter in this domain"), {
      target: { value: "direct-25" },
    });
    const reach = await within(browser).findByTestId("listing-reach");
    expect(reach.textContent).toContain("Nothing on this page matched.");
    expect(reach.textContent).toContain(
      "The filter covers the 10 artifacts this page loaded.",
    );
    expect(reach.textContent).toContain(
      "15 more artifacts stand under this domain.",
    );
    expect(
      within(browser).queryByText(
        "Clear the filter or pick another type.",
      ),
    ).toBeNull();
    expect(
      within(reach)
        .getByRole("link", { name: "Search the whole domain" })
        .getAttribute("href"),
    ).toBe(searchHref("scope:platform direct-25"));

    // A match among the returned rows is still an answer about those rows
    // alone, so the continuation stands beside the row it found and the type
    // chip travels with it.
    fireEvent.change(within(arthead).getByLabelText("Filter in this domain"), {
      target: { value: "direct-3" },
    });
    fireEvent.click(within(arthead).getByRole("button", { name: "skill" }));
    expect(
      within(browser).getByRole("link", { name: "direct-3" }),
    ).toBeTruthy();
    const found = within(browser).getByTestId("listing-reach");
    expect(found.textContent).not.toContain("Nothing on this page matched.");
    expect(
      within(found)
        .getByRole("link", { name: "Search the whole domain" })
        .getAttribute("href"),
    ).toBe(searchHref("type:skill scope:platform direct-3"));

    // An unfiltered listing states its edge in the continuation row under the
    // table and carries no filter continuation of its own.
    fireEvent.change(within(arthead).getByLabelText("Filter in this domain"), {
      target: { value: "" },
    });
    fireEvent.click(within(arthead).getByRole("button", { name: "All" }));
    expect(within(browser).queryByTestId("listing-reach")).toBeNull();
    expect(within(browser).getByTestId("listing-continuation")).toBeTruthy();
  });

  // The continuation row counts the rows the response returned, and a filter
  // narrows the table without loading any more of them. Left standing under a
  // filtered table it reports a figure the reader cannot see, and under an
  // empty one it asserts rows are on screen directly beneath the reach line
  // saying nothing matched. Both rows carry role="status", so the reader who
  // is read the page hears the contradiction as well.
  //
  // Spec: §13.10
  it("drops the continuation row while a filter narrows the at-scale table", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        body: {
          path: "platform",
          subdomains: Array.from({ length: 24 }, (_, i) => ({
            path: `platform/d${String(i)}`,
            name: `d${String(i)}`,
          })),
          notable: Array.from({ length: 10 }, (_, i) => ({
            id: `platform/direct-${String(i + 1)}`,
            type: "skill",
          })),
        },
      },
      "/v1/catalog": {
        body: {
          ids: Array.from(
            { length: 25 },
            (_, i) => `platform/direct-${String(i + 1)}`,
          ),
        },
      },
    });
    goTo("#/domain/platform");
    render(<App />);
    const browser = await screen.findByLabelText("Domain browser");
    const arthead = within(browser).getByRole("heading", {
      name: "Artifacts",
    }).parentElement as HTMLElement;
    const filter = within(arthead).getByLabelText("Filter in this domain");
    await waitFor(() => {
      expect(
        within(browser).getByTestId("listing-continuation").textContent,
      ).toContain("10 of 25 artifacts shown.");
    });

    // A filter that matches nothing leaves an empty table, and no row under it
    // claims ten artifacts are shown.
    fireEvent.change(filter, { target: { value: "zzzz" } });
    expect(within(browser).queryAllByRole("row")).toHaveLength(0);
    expect(within(browser).queryByTestId("listing-continuation")).toBeNull();
    expect(within(browser).queryByText(/artifacts shown\./)).toBeNull();

    // A filter that matches one row does not claim ten either.
    fireEvent.change(filter, { target: { value: "direct-1" } });
    expect(
      within(browser).getByRole("link", { name: "direct-1" }),
    ).toBeTruthy();
    expect(within(browser).queryByTestId("listing-continuation")).toBeNull();

    // A type chip narrows the same way a typed filter does.
    fireEvent.change(filter, { target: { value: "" } });
    fireEvent.click(within(arthead).getByRole("button", { name: "skill" }));
    expect(within(browser).queryByTestId("listing-continuation")).toBeNull();

    // Clearing both restores the row, because the table is drawing the rows it
    // counts again.
    fireEvent.click(within(arthead).getByRole("button", { name: "All" }));
    expect(
      within(browser).getByTestId("listing-continuation").textContent,
    ).toContain("10 of 25 artifacts shown.");
  });

  // §4.5.5 caps the notable list, so a sort over the at-scale table ranks the
  // returned rows rather than the domain. A version ranking whose top row is
  // the highest version the page loaded reads as the domain's highest, so the
  // table states the reach of the sort on the terms the filter already uses
  // and continues into the search bounded to this domain.
  //
  // Spec: §13.10
  it("states the reach of a sort over a trimmed at-scale listing", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        body: {
          path: "platform",
          subdomains: Array.from({ length: 24 }, (_, i) => ({
            path: `platform/d${String(i)}`,
            name: `d${String(i)}`,
          })),
          notable: Array.from({ length: 10 }, (_, i) => ({
            id: `platform/direct-${String(i + 1)}`,
            type: "skill",
            version: "0.1.0",
          })),
        },
      },
      "/v1/catalog": {
        body: {
          ids: Array.from(
            { length: 12 },
            (_, i) => `platform/direct-${String(i + 1)}`,
          ),
        },
      },
    });
    goTo("#/domain/platform");
    render(<App />);
    const browser = await screen.findByLabelText("Domain browser");
    const arthead = within(browser).getByRole("heading", {
      name: "Artifacts",
    }).parentElement as HTMLElement;
    await waitFor(() => {
      expect(
        within(browser).getByTestId("listing-continuation").textContent,
      ).toContain("10 of 12 artifacts shown.");
    });

    // The listing's own ordering makes no claim past the rows it draws, so it
    // stands under the continuation row alone.
    expect(within(browser).queryByTestId("listing-reach")).toBeNull();

    // Ranking by a column does, so the reach line states which rows were
    // ranked and how many stand outside them.
    fireEvent.change(within(arthead).getByLabelText("Sort artifacts"), {
      target: { value: "version" },
    });
    const reach = within(browser).getByTestId("listing-reach");
    expect(reach.textContent).toContain(
      "The sort covers the 10 artifacts this page loaded.",
    );
    expect(reach.textContent).toContain(
      "2 more artifacts stand under this domain.",
    );
    expect(reach.textContent).not.toContain("Nothing on this page matched.");
    expect(
      within(reach)
        .getByRole("link", { name: "Search the whole domain" })
        .getAttribute("href"),
    ).toBe(searchHref("scope:platform"));
    // The reach line reports the same edge the continuation row does, so the
    // two do not stand together.
    expect(within(browser).queryByTestId("listing-continuation")).toBeNull();

    // Returning to the listing's own ordering returns the page to the row that
    // states the edge without a control's reach to qualify.
    fireEvent.change(within(arthead).getByLabelText("Sort artifacts"), {
      target: { value: "id" },
    });
    expect(within(browser).queryByTestId("listing-reach")).toBeNull();
    expect(
      within(browser).getByTestId("listing-continuation").textContent,
    ).toContain("10 of 12 artifacts shown.");
  });

  // The compact treatment is the one the design pass fixed for this count: the
  // section label carries the count and the controls over the listing share
  // its row, a tile states what the response reported below the child, and the
  // table's column labels mark the columns while the control above them
  // chooses the ordering.
  it("carries the compact listing's controls on the section label's row", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        body: {
          path: "platform",
          subdomains: Array.from({ length: 24 }, (_, i) => ({
            path: `platform/d${String(i)}`,
            name: `d${String(i)}`,
            // One child came back with a level under it and the rest came
            // back empty, which is the pair of tiles the count line splits.
            subdomains:
              i === 0
                ? [
                    { path: "platform/d0/one", name: "one" },
                    { path: "platform/d0/two", name: "two" },
                  ]
                : [],
          })),
          notable: [
            {
              id: "platform/deploy",
              type: "skill",
              version: "2.0.0",
              tags: ["release"],
              source: "featured",
            },
            { id: "platform/lint", type: "rule", version: "1.0.0" },
            { id: "platform/notes", type: "context", version: "1.2.0" },
          ],
        },
      },
    });
    goTo("#/domain/platform");
    render(<App />);
    const browser = await screen.findByLabelText("Domain browser");

    // The subdomain label states the count, and the filter and the view
    // toggle stand on the label's own row rather than above the grid.
    const subhead = within(browser).getByRole("heading", {
      name: "Subdomains",
    }).parentElement;
    expect(subhead).not.toBeNull();
    const subrow = within(subhead as HTMLElement);
    expect(subrow.getByLabelText("Filter subdomains")).toBeTruthy();
    // The switch is the segmented control the register form uses, so the
    // chosen view is the segment raised onto the surface colour rather than
    // the one filled with the track colour, and each segment is labelled in
    // the sentence case every other segmented control in the build carries.
    const viewSwitch = subrow.getByRole("group", { name: "Subdomain view" });
    expect(viewSwitch.className.split(" ")).toContain("segmented");
    expect(
      within(viewSwitch)
        .getAllByRole("button")
        .map((segment) => segment.textContent),
    ).toEqual(["Grid", "List"]);
    expect(
      within(viewSwitch).getByRole("button", { name: "Grid" }).className,
    ).toBe("segment segment-on");
    expect(
      within(viewSwitch).getByRole("button", { name: "List" }).className,
    ).toBe("segment");
    expect((subhead as HTMLElement).textContent).toContain("24");
    // The grid itself is not in that row.
    expect(subrow.queryByLabelText("Subdomains")).toBeNull();

    // A tile counts what the response reported below the child, and a child
    // whose subtree came back empty carries no count line at all.
    const tiles = within(browser).getByRole("list", { name: "Subdomains" });
    const first = within(tiles).getAllByRole("listitem")[0];
    expect(first.textContent).toBe("d02 subdomains");
    expect(within(tiles).getAllByRole("listitem")[1].textContent).toBe("d1");
    expect(tiles.textContent).not.toContain("below");

    // The artifact label carries the same row: a filter over the domain's own
    // listing, an All chip standing for the unfiltered set, one chip per
    // returned type, and the sort control.
    const arthead = within(browser).getByRole("heading", {
      name: "Artifacts",
    }).parentElement;
    const artrow = within(arthead as HTMLElement);
    expect(artrow.getByLabelText("Filter in this domain")).toBeTruthy();
    expect(artrow.getByLabelText("Sort artifacts")).toBeTruthy();
    const all = artrow.getByRole("button", { name: "All" });
    expect(all.getAttribute("aria-pressed")).toBe("true");
    expect(artrow.getByRole("button", { name: "rule" })).toBeTruthy();

    // A type chip narrows the table to that type and takes the active state
    // off the All chip.
    fireEvent.click(artrow.getByRole("button", { name: "rule" }));
    expect(all.getAttribute("aria-pressed")).toBe("false");
    expect(
      within(browser).queryByRole("link", { name: "notes" }),
    ).toBeNull();
    fireEvent.click(all);
    expect(
      within(browser).getByRole("link", { name: "notes" }),
    ).toBeTruthy();

    // The in-domain filter runs over the identifier the first column carries.
    fireEvent.change(artrow.getByLabelText("Filter in this domain"), {
      target: { value: "notes" },
    });
    expect(
      within(browser).queryByRole("link", { name: "lint" }),
    ).toBeNull();
    fireEvent.change(artrow.getByLabelText("Filter in this domain"), {
      target: { value: "" },
    });

    // The picks stand in their own block under a header carrying their count,
    // and the rest of the listing carries no heading of its own.
    const curated = within(browser).getByText(
      "Curated by the domain author",
    ).parentElement;
    expect((curated as HTMLElement).textContent).toContain("1");
    expect(within(browser).queryByText("Everything else")).toBeNull();

    // The column labels mark the columns and carry no control: the ordering
    // is chosen by the sort control above the table.
    const rest = within(browser).getAllByLabelText("Artifacts")[1];
    const columns = within(rest).getAllByRole("columnheader");
    expect(columns.map((column) => column.textContent)).toEqual([
      "Artifact",
      "Type",
      "Version",
      "Tags",
      "Description",
    ]);
    expect(within(columns[0]).queryByRole("button")).toBeNull();

    // The type and version cells carry the same two markers every other
    // listing carries: the type as the shared badge in caps, and the version
    // with the `v` prefix that names what the number measures.
    const row = within(rest).getAllByRole("row")[1];
    const cells = within(row).getAllByRole("cell");
    const badge = within(cells[1]).getByText("RULE");
    expect(badge.className.split(" ")).toContain("badge");
    expect(cells[2].textContent).toBe("v1.0.0");
  });

  // The list arm is a denser row rather than the grid tile stretched across the
  // container. A full-width tile carrying only a name and a count leaves most
  // of every row empty and costs a card of height per child, so the row states
  // the name, the description and the count on one line.
  it("renders the at-scale list arm as a row carrying the description", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        body: {
          path: "platform",
          subdomains: Array.from({ length: 24 }, (_, i) => ({
            path: `platform/d${String(i)}`,
            name: `d${String(i)}`,
            // One child carries a description and the rest carry none, which
            // is the pair the row's absent-description treatment splits.
            description:
              i === 0 ? "Everything the build pipeline runs." : undefined,
            subdomains: [],
          })),
          notable: [],
        },
      },
    });
    goTo("#/domain/platform");
    render(<App />);
    const browser = await screen.findByLabelText("Domain browser");

    // The grid arm carries the name and the count alone: a six-column tile has
    // no room for a description.
    const tiles = within(browser).getByRole("list", { name: "Subdomains" });
    expect(within(tiles).getAllByRole("listitem")[0].className).toBe("tile");
    expect(tiles.textContent).not.toContain(
      "Everything the build pipeline runs.",
    );

    fireEvent.click(
      within(
        within(browser).getByRole("group", { name: "Subdomain view" }),
      ).getByRole("button", {
        name: "List",
      }),
    );

    // The same tile at row density, with the description on the line.
    const rows = within(browser).getByRole("list", { name: "Subdomains" });
    const first = within(rows).getAllByRole("listitem")[0];
    expect(first.className.split(" ")).toContain("tile-row");
    // The row is one target on both arms: the description and the count sit
    // under the overlay the stylesheet stretches over the tile
    // (`index.css`, `.stretched-link`).
    expect(within(first).getByRole("link").className).toContain(
      "stretched-link",
    );
    expect(first.textContent).toContain("Everything the build pipeline runs.");
    // A child that carries no description states so rather than leaving the
    // row's middle blank.
    const second = within(rows).getAllByRole("listitem")[1];
    expect(second.textContent).toContain("No description.");
    expect(
      within(second).getByText("No description.").className.split(" "),
    ).toContain("absent-description");

    // Going back to the grid drops the row density and the description with it.
    fireEvent.click(
      within(
        within(browser).getByRole("group", { name: "Subdomain view" }),
      ).getByRole("button", {
        name: "Grid",
      }),
    );
    const back = within(browser).getByRole("list", { name: "Subdomains" });
    expect(within(back).getAllByRole("listitem")[0].className).toBe("tile");
    expect(back.textContent).not.toContain("No description.");
  });

  // The compact tile exists to state a count, and the count it states is the
  // artifacts standing under the child. load_domain reports no such figure, so
  // it comes from one catalog read over the domain rather than from a scoped
  // search behind every tile.
  it("counts the artifacts under each at-scale subdomain tile", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        body: {
          path: "platform",
          subdomains: Array.from({ length: 24 }, (_, i) => ({
            path: `platform/d${String(i)}`,
            name: `d${String(i)}`,
          })),
          notable: [],
        },
      },
      "/v1/catalog": {
        body: {
          ids: [
            // d1 holds its artifacts one level down, so a count taken from
            // the direct children alone would read as zero.
            "platform/d1/deep/one",
            "platform/d1/deep/two",
            "platform/d1/three",
            "platform/d0/only",
          ],
        },
      },
    });
    goTo("#/domain/platform");
    render(<App />);
    const browser = await screen.findByLabelText("Domain browser");
    const tiles = await within(browser).findByRole("list", {
      name: "Subdomains",
    });
    await waitFor(() => {
      expect(within(tiles).getAllByRole("listitem")[0].textContent).toBe(
        "d13 artifacts",
      );
    });
    // The busiest child leads, the rest keep the order the response returned,
    // and a child the catalog found nothing under states that.
    const listed = within(tiles)
      .getAllByRole("listitem")
      .map((tile) => tile.textContent);
    expect(listed[1]).toBe("d01 artifact");
    expect(listed[2]).toBe("d20 artifacts");
    // The caption states what ordered the grid.
    expect(within(browser).getByText("Sorted by artifact count.")).toBeTruthy();
    // The count is one read over the whole domain. The shell reads the
    // unscoped catalog for its own footer, so the scoped reads are what this
    // counts.
    expect(
      requests.filter((r) => r.url.startsWith("/v1/catalog?scope=")).length,
    ).toBe(1);
    expect(
      requests.some((r) => r.url.includes(encodeURIComponent("platform/d"))),
    ).toBe(false);
  });

  // A catalog read that fails leaves the grid as the domain response returned
  // it, and the tile falls back to what that response reported below the child.
  it("keeps the response order when the at-scale catalog read fails", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        body: {
          path: "platform",
          subdomains: Array.from({ length: 24 }, (_, i) => ({
            path: `platform/d${String(i)}`,
            name: `d${String(i)}`,
            subdomains:
              i === 0 ? [{ path: "platform/d0/one", name: "one" }] : [],
          })),
          notable: [],
        },
      },
      "/v1/catalog": { rejects: true },
    });
    goTo("#/domain/platform");
    render(<App />);
    const browser = await screen.findByLabelText("Domain browser");
    const tiles = await within(browser).findByRole("list", {
      name: "Subdomains",
    });
    await waitFor(() => {
      expect(within(tiles).getAllByRole("listitem")[0].textContent).toBe(
        "d01 subdomain",
      );
    });
    expect(within(tiles).getAllByRole("listitem")[1].textContent).toBe("d1");
    expect(within(browser).queryByText("Sorted by artifact count.")).toBeNull();
    // The failed count leaves the domain browser standing.
    expect(within(browser).queryByTestId("domain-failed")).toBeNull();
  });

  // A descriptor that carries no version still fills the column, because a
  // blank cell in a sortable table reads as a load that did not finish.
  it("states an absent version in the at-scale table", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        body: {
          path: "platform",
          subdomains: Array.from({ length: 24 }, (_, i) => ({
            path: `platform/d${String(i)}`,
            name: `d${String(i)}`,
          })),
          notable: [{ id: "platform/lint", type: "rule" }],
        },
      },
    });
    goTo("#/domain/platform");
    render(<App />);
    const browser = await screen.findByLabelText("Domain browser");
    const table = within(browser).getByLabelText("Artifacts");
    const cells = within(within(table).getAllByRole("row")[1]).getAllByRole(
      "cell",
    );
    expect(within(cells[1]).getByText("RULE")).toBeTruthy();
    expect(cells[2].textContent).toBe("unversioned");
  });

  // Spec: §13.10 — the at-scale artifact table is a map of the domain, so a
  // row reads as one band: one separator across every column, and the type
  // badge, the version, and the tag chips on a common baseline. Clipping the
  // description to one line needs a block display, and putting that on the
  // cell takes it out of the row's cell layout, which lifted the description
  // column's rule above the rule under the columns beside it. The clip
  // therefore sits on an element inside the cell.
  it("keeps the clipped description cell in the row's cell layout", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        body: {
          path: "platform",
          subdomains: Array.from({ length: 24 }, (_, i) => ({
            path: `platform/d${String(i)}`,
            name: `d${String(i)}`,
          })),
          notable: [
            {
              id: "platform/lint",
              type: "rule",
              description:
                "A lint rule with a description far longer than the column it stands in.",
            },
          ],
        },
      },
    });
    goTo("#/domain/platform");
    render(<App />);
    const browser = await screen.findByLabelText("Domain browser");
    const table = within(browser).getByLabelText("Artifacts");
    const cells = within(within(table).getAllByRole("row")[1]).getAllByRole(
      "cell",
    );
    for (const cell of cells) {
      expect(window.getComputedStyle(cell).display).toBe("table-cell");
    }
    const description = cells[cells.length - 1];
    const clip = description.querySelector(".clipped");
    expect(clip).not.toBeNull();
    expect(clip?.textContent).toBe(
      "A lint rule with a description far longer than the column it stands in.",
    );
    const clipped = window.getComputedStyle(clip as Element);
    expect(clipped.whiteSpace).toBe("nowrap");
    expect(clipped.textOverflow).toBe("ellipsis");
    // The tag chips stand in the band with the text beside them, so the tag
    // list drops the block margin it carries under a card.
    const tags = cells[3].querySelector(".tag-list");
    expect(tags).not.toBeNull();
    expect(window.getComputedStyle(tags as Element).marginTop).toBe("0px");
  });

  // Spec: §13.10 — the table asks for more width than a narrow content column
  // holds, and nothing above it clipped what it asked for: at a 1024px
  // viewport it rendered past the right edge of the window and scrolled the
  // whole shell sideways, carrying the top bar and the sidebar with it. Each
  // block sits in the scroll container the layer panel's table sits in, so
  // the container owns the sideways scroll, and it is focusable and named so
  // a keyboard reaches it.
  it("puts each block of the table in a container that scrolls sideways", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        body: {
          path: "platform",
          subdomains: Array.from({ length: 24 }, (_, i) => ({
            path: `platform/d${String(i)}`,
            name: `d${String(i)}`,
          })),
          notable: [
            {
              id: "platform/deploy",
              type: "skill",
              version: "2.0.0",
              source: "featured",
            },
            { id: "platform/lint", type: "rule", version: "1.0.0" },
          ],
        },
      },
    });
    goTo("#/domain/platform");
    render(<App />);
    const browser = await screen.findByLabelText("Domain browser");
    const tables = within(browser).getAllByLabelText("Artifacts");
    expect(tables).toHaveLength(2);
    expect(
      tables.map((table) => {
        const container = table.parentElement as HTMLElement;
        return {
          scroller: container.classList.contains("table-scroll"),
          overflow: window.getComputedStyle(container).overflowX,
          focusable: container.tabIndex,
          name: container.getAttribute("aria-label"),
        };
      }),
    ).toEqual([
      {
        scroller: true,
        overflow: "auto",
        focusable: 0,
        name: "Curated artifacts",
      },
      {
        scroller: true,
        overflow: "auto",
        focusable: 0,
        name: "Artifacts in this domain",
      },
    ]);
    // The floor is what keeps the columns at the widths the design draws them
    // at inside that container: below it an identifier such as
    // `platform/deploy` broke across two lines.
    const floor = Number.parseFloat(
      window.getComputedStyle(tables[0]).minWidth,
    );
    expect(floor).toBeGreaterThanOrEqual(860);
  });
});

describe("the anonymous framing", () => {
  // Where the catalog read answers and the posture read does not, the page is
  // on the public-subset arm and takes that arm's treatment: it presents what
  // the catalog read returned, states that no subject resolved, and claims
  // nothing about content beyond what was returned. The authentication
  // controls key on the posture read, so a read that did not answer renders
  // neither of them.
  it("takes the public-subset treatment where the posture read did not answer", async () => {
    stubRegistry({
      "/v1/ui/session": {
        status: 503,
        body: { code: "registry.unavailable", message: "down" },
      },
      "/v1/load_domain": {
        body: {
          path: "",
          subdomains: [],
          notable: [{ id: "platform/deploy", type: "skill" }],
        },
      },
    });
    render(<App />);
    expect(await screen.findByText("platform/deploy")).toBeTruthy();
    expect(screen.getByTestId("anonymous-banner")).toBeTruthy();
    expect(screen.getByText("Not signed in")).toBeTruthy();
    expect(screen.queryByTestId("sign-in")).toBeNull();
    expect(screen.queryByTestId("sign-out")).toBeNull();
    // The arm says nothing about content having been withheld.
    expect(screen.queryByText(/hidden/i)).toBeNull();
    expect(screen.queryByText(/withheld/i)).toBeNull();
  });
});

describe("the artifact viewer’s resources", () => {
  function resourcePage(manifestBody = "# Review\n"): void {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact": {
        body: {
          id: "platform/review",
          type: "context",
          version: "1.0.0",
          content_hash: "sha256:abc",
          manifest_body: manifestBody,
          frontmatter: "",
          resources: { "checklist.md": "body" },
          large_resources: {
            "corpus.bin": {
              presigned_url: "https://objects.acme.com/corpus",
              content_hash: "sha256:def",
              size: 2 * 1024 * 1024,
              content_type: "application/octet-stream",
            },
          },
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    goTo("#/artifact/platform%2Freview");
  }

  // The rail splits the two deliveries, because a file that arrived with the
  // response and one that is fetched on demand cost the reader different
  // things to open.
  it("splits the rail into the inline files and the ones fetched on demand", async () => {
    resourcePage();
    render(<App />);
    await screen.findByLabelText("Artifact viewer");
    const section = screen.getByLabelText("Bundled resources");
    const groups = section.querySelectorAll(".rail-group");
    expect(groups.length).toBe(2);
    expect(groups[0].textContent).toContain("Inline");
    expect(groups[0].textContent).toContain("checklist.md");
    expect(groups[1].textContent).toContain("Fetched on demand");
    expect(groups[1].textContent).toContain("corpus.bin");
  });

  // A bundled file in the rail is a row rather than a line of text: it is
  // bordered, and it states its size on the far edge, because a run of bare
  // mono lines says neither where one file ends nor what opening it costs.
  // The section header carries the count of the whole set, and a file
  // fetched on demand takes the retrieval action from its size.
  it("draws each rail resource as a bordered row stating its size", async () => {
    resourcePage();
    render(<App />);
    await screen.findByLabelText("Artifact viewer");
    const section = screen.getByLabelText("Bundled resources");
    expect(screen.getByTestId("rail-resource-count").textContent).toBe("2");
    const rows = [...section.querySelectorAll(".resource-chip")];
    expect(rows.length).toBe(2);
    expect(rows[0].textContent).toBe("checklist.md4 B");
    const first = window.getComputedStyle(rows[0]);
    expect(first.display).toBe("flex");
    expect(first.borderRadius).toBe("8px");
    expect(
      window.getComputedStyle(
        rows[0].querySelector(".resource-size") as Element,
      ).marginLeft,
    ).toBe("auto");
    // The file fetched on demand is not in the page, so its row is outlined
    // and its size is what retrieves it.
    expect(rows[1].textContent).toBe("corpus.bin2.0 MB ↓");
    expect(window.getComputedStyle(rows[1]).borderStyle).toBe("dashed");
    const download = within(rows[1] as HTMLElement).getByRole("link", {
      name: "Download corpus.bin",
    });
    expect(download.getAttribute("href")).toBe(
      "https://objects.acme.com/corpus",
    );
  });

  // The tab keeps the two deliveries as one list, takes the whole set at
  // once from the control above the table, and opens the selected row's
  // detail card under it.
  it("offers the whole set above the table and details the selected row under it", async () => {
    resourcePage();
    render(<App />);
    await screen.findByLabelText("Artifact viewer");
    fireEvent.click(screen.getByRole("tab", { name: /Resources/ }));
    // The total is the two files together: four inline bytes and two
    // megabytes fetched on demand.
    const all = screen.getByTestId("download-all");
    expect(all.textContent).toBe("Download all ↓ 2.0 MB");
    // The control takes the set the row buttons take one file at a time, so
    // it sits at the table's right edge above their column rather than at the
    // left edge, where it reads as a heading over the file column.
    const allStyle = window.getComputedStyle(all);
    expect(allStyle.display).toBe("block");
    expect(allStyle.marginLeft).toBe("auto");
    expect(allStyle.marginRight).toBe("0px");
    expect(screen.queryByTestId("resource-detail")).toBeNull();
    const rows = within(screen.getByLabelText("Resources"))
      .getAllByRole("row")
      .slice(1);
    fireEvent.click(rows[1]);
    const detail = screen.getByTestId("resource-detail");
    expect(detail.textContent).toContain("corpus.bin");
    expect(detail.textContent).toContain("fetched on demand");
    expect(rows[1].className).toContain("row-selected");
  });

  // The detail card states the file's attributes as a labelled property grid
  // rather than as one dot-joined line, and its retrieval action is the
  // primary control on the tab, which the selected row's own action joins.
  // Without this the card read as a caption with a secondary button under it
  // and the reader had to infer which value answered which attribute.
  it("details the selected file as a property grid with a primary download", async () => {
    resourcePage();
    render(<App />);
    await screen.findByLabelText("Artifact viewer");
    fireEvent.click(screen.getByRole("tab", { name: /Resources/ }));
    const rows = within(screen.getByLabelText("Resources"))
      .getAllByRole("row")
      .slice(1);
    fireEvent.click(rows[1]);
    const facts = screen.getByTestId("resource-detail-facts");
    const pairs = [...facts.querySelectorAll(".rail-fact")].map((fact) => [
      fact.querySelector("dt")?.textContent,
      fact.querySelector("dd")?.textContent,
    ]);
    expect(pairs).toEqual([
      ["format", "application/octet-stream"],
      ["size", "2.0 MB"],
      ["delivery", "fetched on demand"],
      ["content type", "application/octet-stream"],
    ]);
    const card = screen.getByTestId("resource-detail");
    const action = within(card).getByRole("link", { name: "Download ↓" });
    expect(action.className).toContain("primary");
    expect(action.getAttribute("href")).toBe("https://objects.acme.com/corpus");
    // The selected row's own action is filled the same way, so the pair reads
    // as one control rather than as a primary card action beside an outlined
    // row action that retrieves the same file.
    expect(
      within(rows[1]).getByRole("link", { name: "Download ↓" }).className,
    ).toContain("primary");
    expect(
      within(rows[0]).getByRole("link", { name: "Download ↓" }).className,
    ).not.toContain("primary");
    // An inline file carries no recorded media type, so the card keeps the
    // row and names the absence rather than dropping it.
    fireEvent.click(rows[0]);
    const inline = [
      ...screen.getByTestId("resource-detail-facts").querySelectorAll(".rail-fact"),
    ];
    expect(inline[3].textContent).toBe("content typenot recorded");
  });

  // A §4.4 prose reference in the body that names a bundled file is followed
  // inside the viewer. The registry serves no per-artifact asset route, so
  // left as authored the reference resolves against the /ui/ mount and the
  // reader leaves the SPA for a plain-text 404; the Resources tab holds the
  // file's only delivery, so the reference opens that tab on it (§13.10).
  it("opens the Resources tab on the file a body reference names", async () => {
    resourcePage("See [the checklist](checklist.md).\n");
    render(<App />);
    await screen.findByLabelText("Artifact viewer");
    const reference = within(screen.getByTestId("artifact-body")).getByRole(
      "button",
      { name: "the checklist" },
    );
    // Nothing in the body navigates out of the shell.
    expect(
      screen.getByTestId("artifact-body").querySelector("a"),
    ).toBeNull();
    fireEvent.click(reference);
    expect(
      screen.getByRole("tab", { name: /Resources/ }).getAttribute(
        "aria-selected",
      ),
    ).toBe("true");
    expect(screen.getByTestId("resource-detail").textContent).toContain(
      "checklist.md",
    );
    expect(screen.getByLabelText("Artifact viewer")).toBeTruthy();
  });

  // The selection drives what the tab shows, so it is operable without a
  // pointer: the file name is a button a keyboard reaches and activates, and
  // it states whether its row is the selected one.
  it("selects a resource row from the keyboard", async () => {
    resourcePage();
    render(<App />);
    await screen.findByLabelText("Artifact viewer");
    fireEvent.click(screen.getByRole("tab", { name: /Resources/ }));
    const table = within(screen.getByLabelText("Resources"));
    const name = table.getByRole("button", { name: "corpus.bin" });
    name.focus();
    expect(document.activeElement).toBe(name);
    expect(name.getAttribute("aria-pressed")).toBe("false");
    // A focused button activates on Enter or Space, which the browser
    // delivers as a click.
    fireEvent.click(name);
    expect(name.getAttribute("aria-pressed")).toBe("true");
    expect(screen.getByTestId("resource-detail").textContent).toContain(
      "corpus.bin",
    );
    expect(
      table.getAllByRole("row").slice(1)[1].className,
    ).toContain("row-selected");
  });

  // One file's size reads the same in every place the tab states it: the
  // total above the table, the row's size column, and the detail card under
  // it all take the same unit.
  it("states a row's size in the same unit as the total and the detail card", async () => {
    resourcePage();
    render(<App />);
    await screen.findByLabelText("Artifact viewer");
    fireEvent.click(screen.getByRole("tab", { name: /Resources/ }));
    const rows = within(screen.getByLabelText("Resources"))
      .getAllByRole("row")
      .slice(1);
    const size = within(rows[1]).getAllByRole("cell")[2].textContent;
    expect(size).toBe("2.0 MB");
    fireEvent.click(rows[1]);
    expect(screen.getByTestId("resource-detail").textContent).toContain(size);
  });

  // The resource table is one of the UI's data tables, so its column headers
  // take the same mono label treatment the at-scale artifact table uses, its
  // delivery value reads as a badge rather than as body text, and its row
  // action reads as a bordered control. Without this the tab drew three
  // treatments the rest of the build does not use.
  it("labels its columns, badges the delivery, and draws the row action as a control", async () => {
    resourcePage();
    render(<App />);
    await screen.findByLabelText("Artifact viewer");
    fireEvent.click(screen.getByRole("tab", { name: /Resources/ }));
    const table = screen.getByLabelText("Resources");
    const headers = within(table).getAllByRole("columnheader");
    // The download column carries no header, so the header row reads as the
    // columns that name data. Every column that does name one is drawn in
    // the section-label style.
    expect(headers.map((header) => header.textContent)).toEqual([
      "File",
      "Format",
      "Size",
      "Delivery",
      "",
    ]);
    for (const header of headers.slice(0, 4)) {
      expect(header.className).toContain("column-label");
    }
    const rows = within(table).getAllByRole("row").slice(1);
    for (const row of rows) {
      const delivery = within(row).getAllByRole("cell")[3];
      expect(delivery.querySelector(".badge")).not.toBeNull();
      expect(
        within(row).getByRole("link", { name: "Download ↓" }).className,
      ).toContain("button");
    }
  });
});

describe("a refused layer write", () => {
  function refusedPage(refusal?: {
    status: number;
    body: Record<string, unknown>;
  }): void {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [userLayer("bob@acme.com")] } },
      "/v1/layers?deleted=true": { body: { layers: [] } },
      "DELETE /v1/layers": refusal ?? {
        status: 403,
        body: { code: "auth.forbidden", message: "not permitted" },
      },
    });
    goTo("#/layers");
  }

  async function refuseAnUnregister(): Promise<void> {
    await screen.findByLabelText("Layer panel");
    openRowActions();
    fireEvent.click(screen.getByRole("menuitem", { name: "Unregister" }));
    fireEvent.change(screen.getByLabelText("Type the layer ID to confirm"), {
      target: { value: "alice-personal" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Unregister layer" }));
    await screen.findByText(/nothing changed/);
  }

  // The refusal is drawn on the row with a Try again beside it, and the
  // control re-issues the write that was refused rather than a fresh guess
  // at it.
  it("re-issues the refused write from the row", async () => {
    // The re-issue is offered where the envelope reports the condition clears
    // on its own, so the write this drives is refused by a transient one.
    refusedPage({
      status: 503,
      body: {
        code: "registry.unavailable",
        message: "the registry did not answer",
        retryable: true,
      },
    });
    render(<App />);
    await refuseAnUnregister();
    const sent = requests.filter((r) => r.method === "DELETE").length;
    fireEvent.click(
      within(screen.getByRole("alert")).getByRole("button", {
        name: "Try again",
      }),
    );
    await waitFor(() => {
      expect(requests.filter((r) => r.method === "DELETE").length).toBe(
        sent + 1,
      );
    });
  });

  // A refused write is presented on the envelope's own terms. Rendering the
  // code alone discards the sentence stating what was refused and the
  // remediation naming the one action that clears it, and it offers a retry
  // the envelope reports cannot succeed: a browser-origin refusal answers an
  // identical re-issue from the same page identically.
  // Spec: §6.10
  it("states the refusal’s message and remediation and withholds a retry that cannot succeed", async () => {
    refusedPage({
      status: 403,
      body: {
        code: "auth.csrf_invalid",
        message:
          "The request was refused because it did not pass the browser-origin check.",
        retryable: false,
        suggested_action:
          "Reload the web UI and retry the operation from it; if the registry is behind a gateway, pass the browser-facing Host header through unrewritten.",
      },
    });
    render(<App />);
    await refuseAnUnregister();
    const banner = screen.getByRole("alert");
    expect(banner.textContent).toContain("auth.csrf_invalid");
    expect(banner.textContent).toContain(
      "did not pass the browser-origin check",
    );
    expect(banner.textContent).toContain("Reload the web UI");
    expect(within(banner).queryByRole("button", { name: "Try again" })).toBeNull();
    expect(within(banner).getByTestId("not-retryable")).toBeTruthy();
    // Dismiss is the way out that remains.
    expect(
      within(banner).getByRole("button", { name: "Dismiss" }),
    ).toBeTruthy();
  });

  // Dismiss clears the row's refusal without driving another write, which is
  // the only other way out of the state.
  it("clears the refusal on dismiss and drives no write", async () => {
    refusedPage();
    render(<App />);
    await refuseAnUnregister();
    const sent = requests.filter((r) => r.method === "DELETE").length;
    fireEvent.click(
      within(screen.getByRole("alert")).getByRole("button", {
        name: "Dismiss",
      }),
    );
    await waitFor(() => {
      expect(screen.queryByText(/nothing changed/)).toBeNull();
    });
    expect(requests.filter((r) => r.method === "DELETE").length).toBe(sent);
    // Every other control on the row stayed live throughout.
    openRowActions();
    expect(
      screen.getByRole("menuitem", { name: "Edit" }).hasAttribute("disabled"),
    ).toBe(false);
  });

  // Dismiss unmounts itself with the banner it closes, so a keyboard operator
  // who presses it is left on the document body and the next Tab restarts at
  // the top of the page. The row's Reingest control is where focus resumes.
  it("returns focus to the row’s Reingest control when the refusal is dismissed", async () => {
    refusedPage();
    render(<App />);
    await refuseAnUnregister();
    const dismiss = within(screen.getByRole("alert")).getByRole("button", {
      name: "Dismiss",
    });
    dismiss.focus();
    fireEvent.click(dismiss);
    await waitFor(() => {
      expect(screen.queryByText(/nothing changed/)).toBeNull();
    });
    await waitFor(() => {
      expect(document.activeElement).toBe(
        screen.getByRole("button", { name: "Reingest" }),
      );
    });
  });

  // The recoverable link leads the action row and states how much is still
  // restorable, which is the one piece of panel state naming something on
  // its way to being erased.
  it("states the recoverable count on the panel’s first action", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [userLayer()] } },
      "/v1/layers?deleted=true": {
        body: {
          layers: [
            {
              ...userLayer(),
              ID: "alice-old",
              DeletedAt: new Date().toISOString(),
            },
          ],
        },
      },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    const link = screen.getByTestId("recoverable-link");
    await waitFor(() => {
      expect(link.querySelector(".badge")?.textContent).toBe("1");
    });
    // The figure is the badge every other count in the UI uses, so it reads
    // as a count rather than as running text appended to the label.
    expect(link.textContent).not.toContain("·");
    // It takes the count tone, the filled pill a bare figure inside a control
    // carries. The outlined badge the tabular markers take draws the same
    // figure as a boxed input parked beside the link's label.
    const badge = link.querySelector(".badge") as HTMLElement;
    const tones = badge.className.split(" ");
    expect(tones).toContain("badge-count");
    expect(tones).not.toContain("badge-quiet");
    // It is the first control in the action row, ahead of the primary
    // Register layer and the secondary Reingest all.
    const actions = Array.from(
      (link.parentElement as HTMLElement).children,
    ) as HTMLElement[];
    expect(actions.map((control) => control.textContent?.slice(0, 8))).toEqual([
      "↺ Recent",
      "Register",
      "Reingest",
    ]);
  });

  // A panel holding no layer carries the empty state alone. The precedence
  // lines, the reordering note, and the recoverable count all describe
  // acting on a row, so each is absent where there is no row, and the empty
  // state is not read as a tree that failed to render under them.
  it("drops the reorder copy and the zero count where no layer is registered", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/load_domain": { body: emptyDomain },
      "/v1/layers": { body: { layers: [] } },
    });
    goTo("#/layers");
    render(<App />);
    const panel = await screen.findByLabelText("Layer panel");
    expect(panel.textContent).toContain(
      "Register a layer to bring its artifacts into the catalog.",
    );
    expect(panel.textContent).not.toContain("Precedence");
    expect(panel.textContent).not.toContain("composes above");
    expect(panel.textContent).not.toContain("Reordering takes effect");
    // The recoverable read answers on the same stub, so it reports nothing
    // recoverable and the link states no figure beside itself.
    const link = screen.getByTestId("recoverable-link");
    await waitFor(() => {
      expect(link.textContent).toBe("↺ Recently unregistered");
      expect(link.querySelector(".badge")).toBe(null);
    });
    expect(
      screen
        .getByRole("button", { name: "Reingest all" })
        .hasAttribute("disabled"),
    ).toBe(true);
  });

  // The panel's retry is the recovery from an outage that refused every read
  // the panel owns, so it re-reads the deleted list beside the layer list. A
  // retry that reloads the rows alone repopulates the table over a count that
  // still holds the failure, and a layer inside its recovery window then
  // reads as nothing to recover for the rest of the session.
  it("re-reads the recoverable count when the panel retry recovers", async () => {
    const deleted = {
      layers: [
        {
          ...userLayer(),
          ID: "alice-old",
          DeletedAt: new Date().toISOString(),
        },
      ],
    };
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { rejects: true },
      "/v1/layers?deleted=true": { rejects: true },
    });
    goTo("#/layers");
    render(<App />);
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("registry.unavailable");
    // The registry answers again, and the reader presses the panel's own
    // recovery control.
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [userLayer()] } },
      "/v1/layers?deleted=true": { body: deleted },
    });
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    await screen.findByLabelText("Layer panel");
    const link = screen.getByTestId("recoverable-link");
    await waitFor(() => {
      expect(link.querySelector(".badge")?.textContent).toBe("1");
    });
  });

  // A deleted-list read that failed holds no layers, and so does a tenant
  // with nothing unregistered. The link states which of the two it has: a
  // failed read is reported rather than drawn as the panel's nothing-to-
  // recover arm.
  it("marks the recoverable count unread when its own read failed", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [userLayer()] } },
      "/v1/layers?deleted=true": { rejects: true },
    });
    goTo("#/layers");
    render(<App />);
    await screen.findByLabelText("Layer panel");
    const link = screen.getByTestId("recoverable-link");
    await waitFor(() => {
      expect(link.querySelector(".badge")?.textContent).toBe("?");
    });
    expect(link.getAttribute("title")).toBe(
      "The recoverable count could not be read.",
    );
    // The unread marker stands where the figure would, so it takes the same
    // count tone the figure takes.
    expect(
      (link.querySelector(".badge") as HTMLElement).className.split(" "),
    ).toContain("badge-count");
    // The rows the panel did read are untouched by the failure beside them.
    expect(screen.getByText("alice-personal")).toBeTruthy();
  });
});

describe("a registry that did not answer", () => {
  // A call the browser never delivered rejects with a JavaScript exception
  // rather than with a §6.10 envelope. The surface presents it on the same
  // terms as every other refusal, with a code and a sentence of its own, so
  // the reader is told the registry is unreachable rather than shown the
  // browser's internal exception text.
  it("states the registry is unreachable and shows no exception text", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": { rejects: true },
    });
    goTo("#/domain/platform");
    render(<App />);
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("registry.unavailable");
    expect(alert.textContent).toContain(
      "The registry could not be reached from this browser.",
    );
    expect(alert.textContent).not.toContain("TypeError");
    expect(alert.textContent).not.toContain("Failed to fetch");
  });

  // The condition clears when the registry answers again, so the state keeps
  // its retry and the retry re-issues the read that failed.
  it("keeps a retry that re-issues the read", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": { rejects: true },
    });
    goTo("#/domain/platform");
    render(<App />);
    const alert = await screen.findByRole("alert");
    const sent = requests.filter((r) =>
      r.url.startsWith("/v1/load_domain"),
    ).length;
    fireEvent.click(within(alert).getByRole("button", { name: "Retry" }));
    await waitFor(() => {
      expect(
        requests.filter((r) => r.url.startsWith("/v1/load_domain")).length,
      ).toBe(sent + 1);
    });
  });
});

// A read that resolved nothing leaves no surface to stand a banner over, so
// §13.10's domain browser and artifact viewer render the failure as the page:
// what kind of failure it was, what did not load, one sentence naming what
// the route asked for, and the way off a dead surface. The code is stated
// once, at the foot.
describe("a whole-surface failure", () => {
  it("draws a failed artifact read as an error page with a way back", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact": {
        status: 404,
        body: {
          code: "registry.not_found",
          message: "registry.not_found: artifact eng/deploy/no-desc",
        },
      },
    });
    goTo("#/artifact/eng%2Fdeploy%2Fno-desc");
    render(<App />);
    const page = await screen.findByTestId("artifact-failed");
    expect(
      within(page).getByRole("heading", { name: "No such artifact" }),
    ).toBeTruthy();
    expect(page.textContent).toContain("NOT FOUND");
    expect(page.textContent).toContain("eng/deploy/no-desc does not resolve.");
    // The way off the dead surface, and the code once rather than twice.
    const back = within(page).getByRole("link", { name: "Back to catalog" });
    expect(back.getAttribute("href")).toBe(domainHref(""));
    expect(page.textContent?.match(/registry\.not_found/g)?.length).toBe(1);
    expect(page.textContent).toContain("registry.not_found · not retryable");
    // The condition does not clear on its own, so no retry is offered.
    expect(within(page).queryByRole("button", { name: "Retry" })).toBeNull();
  });

  it("draws a failed domain read the same way, with a retry where the condition clears", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": { rejects: true },
    });
    goTo("#/domain/platform%2Fci");
    render(<App />);
    const page = await screen.findByTestId("domain-failed");
    expect(
      within(page).getByRole("heading", { name: "Can't reach the registry" }),
    ).toBeTruthy();
    expect(page.textContent).toContain("REGISTRY UNREACHABLE");
    expect(within(page).getByRole("button", { name: "Retry" })).toBeTruthy();
    expect(
      within(page).getByRole("link", { name: "Back to catalog" }),
    ).toBeTruthy();
    expect(page.textContent).toContain("registry.unavailable · retryable");
  });

  // A refusal written by something in front of the registry carries a status
  // and no §6.10 envelope. The page states the status it received rather than
  // a code the registry never sent, it does not read the status as the domain
  // being missing, and a server-side status keeps the retry that clears it.
  it("states the status alone where a whole-surface refusal carried no error code", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        status: 502,
        text: "<html><body>502 Bad Gateway</body></html>",
      },
    });
    goTo("#/domain/platform%2Fci");
    render(<App />);
    const page = await screen.findByTestId("domain-failed");
    expect(page.textContent).toContain("HTTP 502 · retryable");
    expect(page.textContent).not.toContain("registry.unavailable");
    expect(
      within(page).getByRole("heading", { name: "The request was refused" }),
    ).toBeTruthy();
    expect(within(page).getByRole("button", { name: "Retry" })).toBeTruthy();
  });

  // The way off is only a way off where it leads somewhere else. At the
  // registry root the link's target is the route already on screen, so
  // following it would leave the same panel standing and read as a second
  // failed attempt.
  it("omits the way back on the catalog route, leaving the retry alone", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": { rejects: true },
    });
    goTo("#/");
    render(<App />);
    const page = await screen.findByTestId("domain-failed");
    expect(
      within(page).getByRole("heading", { name: "Can't reach the registry" }),
    ).toBeTruthy();
    expect(within(page).getByRole("button", { name: "Retry" })).toBeTruthy();
    expect(
      within(page).queryByRole("link", { name: "Back to catalog" }),
    ).toBeNull();
  });

  // §13.10 requires an artifact the caller may not see to be indistinguishable
  // from one that does not exist. The registry conceals the single-artifact
  // denial today, so the page is the second place that property holds, and it
  // holds without resting on that: a read route that answered a refusal
  // directly renders the not-found page down to the code at its foot.
  it("draws a refused artifact read as the not-found page", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/load_artifact": {
        status: 403,
        body: {
          code: "auth.forbidden",
          message: "The caller is not permitted to read this artifact.",
          retryable: false,
        },
      },
    });
    goTo("#/artifact/eng%2Fdeploy%2Fno-desc");
    render(<App />);
    const page = await screen.findByTestId("artifact-failed");
    expect(
      within(page).getByRole("heading", { name: "No such artifact" }),
    ).toBeTruthy();
    expect(page.textContent).toContain("NOT FOUND");
    expect(page.textContent).not.toContain("REFUSED");
    expect(page.textContent).toContain("eng/deploy/no-desc does not resolve.");
    expect(page.textContent).not.toContain("not permitted");
    expect(page.textContent).toContain("registry.not_found · not retryable");
    expect(page.textContent).not.toContain("auth.forbidden");
  });

  // The batch path reports its per-item visibility denial under its own code,
  // which the error-code reference records as mirroring a not-found result.
  // The page collapses it the same way.
  it("draws a visibility denial as the not-found page", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/load_artifact": {
        status: 403,
        body: {
          code: "visibility.denied",
          message: "visibility.denied: caller lacks visibility",
          retryable: false,
        },
      },
    });
    goTo("#/artifact/eng%2Fdeploy%2Fno-desc");
    render(<App />);
    const page = await screen.findByTestId("artifact-failed");
    expect(page.textContent).toContain("NOT FOUND");
    expect(page.textContent).not.toContain("visibility.denied");
    expect(page.textContent).toContain("registry.not_found · not retryable");
  });
});

// The keyboard contract the announced roles promise. A widget that names
// itself a tab set, a combobox, or a listbox is operated the way the WAI-ARIA
// pattern for that role is operated, and the shell offers a way past the
// sidebar tree that does not run through it.
describe("keyboard semantics", () => {
  // The tab set is one Tab stop with a roving tabindex, and the arrows move
  // the selection inside it.
  it("moves the artifact viewer’s tabs with the arrows over a roving tabindex", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_artifact": {
        body: {
          id: "platform/review",
          type: "skill",
          version: "1.0.0",
          content_hash: "sha256:abc",
          manifest_body: "# Review\n",
          frontmatter: "name: review\n",
          skill_raw: "---\nname: review\n---\n\nAuthored skill body.\n",
        },
      },
      "/v1/dependents": { body: { edges: [] } },
    });
    goTo("#/artifact/platform%2Freview");
    render(<App />);
    await screen.findByLabelText("Artifact viewer");
    const list = screen.getByRole("tablist");
    const tabs = within(list).getAllByRole("tab");
    expect(tabs.map((tab) => tab.getAttribute("tabindex"))).toEqual([
      "0",
      "-1",
      "-1",
    ]);
    tabs[0].focus();
    fireEvent.keyDown(list, { key: "ArrowRight" });
    expect(
      screen
        .getByRole("tab", { name: /Frontmatter/ })
        .getAttribute("aria-selected"),
    ).toBe("true");
    expect(document.activeElement).toBe(
      screen.getByRole("tab", { name: /Frontmatter/ }),
    );
    expect(screen.getByTestId("frontmatter-table")).toBeTruthy();
    // End lands on the last tab, and the arrows wrap rather than stopping at
    // the edge.
    fireEvent.keyDown(list, { key: "End" });
    expect(
      screen
        .getByRole("tab", { name: "Authored source" })
        .getAttribute("aria-selected"),
    ).toBe("true");
    fireEvent.keyDown(list, { key: "ArrowRight" });
    expect(
      screen
        .getByRole("tab", { name: "Rendered" })
        .getAttribute("aria-selected"),
    ).toBe("true");
  });

  // The palette's field and result list are a combobox over a listbox, so the
  // highlight the arrows move is named rather than only drawn.
  it("names the palette’s highlighted row through aria-activedescendant", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": { body: emptyDomain },
      "/v1/search_artifacts": {
        body: {
          total_matched: 2,
          results: [
            { id: "platform/review", type: "skill" },
            { id: "platform/lint", type: "skill" },
          ],
        },
      },
    });
    render(<App />);
    fireEvent.click(await screen.findByTestId("search-trigger"));
    const panel = screen.getByTestId("palette");
    const field = within(panel).getByLabelText("Search artifacts");
    expect(field.getAttribute("role")).toBe("combobox");
    expect(field.getAttribute("aria-expanded")).toBe("false");
    fireEvent.change(field, { target: { value: "review" } });
    await screen.findByTestId("palette-heading");
    const listbox = within(panel).getByRole("listbox");
    expect(field.getAttribute("aria-expanded")).toBe("true");
    expect(field.getAttribute("aria-controls")).toBe(listbox.id);
    const options = within(listbox).getAllByRole("option");
    expect(options.length).toBe(2);
    expect(field.getAttribute("aria-activedescendant")).toBe(options[0].id);
    expect(options[0].getAttribute("aria-selected")).toBe("true");
    fireEvent.keyDown(panel, { key: "ArrowDown" });
    expect(field.getAttribute("aria-activedescendant")).toBe(options[1].id);
    expect(options[1].getAttribute("aria-selected")).toBe("true");
    expect(options[0].getAttribute("aria-selected")).toBe("false");
  });

  // The shell's first Tab stop skips the sidebar tree, which otherwise sits
  // between the top bar and the page on every route.
  it("offers a first-stop skip link that moves focus to the content region", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": { body: emptyDomain },
    });
    goTo("#/");
    render(<App />);
    const skip = await screen.findByTestId("skip-link");
    // It is the document's first focusable element, ahead of the top bar.
    const focusable = Array.from(
      document.querySelectorAll<HTMLElement>(
        "a[href], button, input, [tabindex]:not([tabindex='-1'])",
      ),
    );
    expect(focusable[0]).toBe(skip);
    fireEvent.click(skip);
    expect(document.activeElement).toBe(screen.getByRole("main"));
  });
});
