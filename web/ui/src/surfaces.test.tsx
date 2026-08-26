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
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { App } from "./App";
import { parseQueryLine } from "./query";
import { domainHref, searchHref } from "./route";
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

beforeEach(() => {
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

  // The shell is one layout on every screen: the nav, the catalog label with
  // its depth marker, the tree, and the counts footer pinned under it. The
  // tree is eager to two levels and reads a deeper level when the reader
  // expands the node it hangs under.
  it("renders the catalog tree, reads a deeper level on expand, and states the counts", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": { body: catalog },
      "/v1/search_artifacts": { body: { total_matched: 312 } },
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
    const depth = screen.getByTestId("catalog-depth");
    expect(depth.textContent).toBe("2 levels");
    // The marker annotates the Catalog label rather than repeating it, so it
    // carries its own class and not the uppercased, tracked section label.
    expect(depth.className).toBe("catalog-depth");
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
      "/v1/load_domain?path=platform%2Fci&depth=2": {
        body: {
          path: "platform/ci",
          subdomains: [{ path: "platform/ci/lint", name: "lint" }],
          notable: [],
        },
      },
      // The current domain carries a level of its own, so its node keeps the
      // toggle the assertion below counts. A domain whose own read comes back
      // empty is a leaf, and a leaf drops its toggle.
      "/v1/load_domain?path=platform%2Fci%2Flint&depth=2": {
        body: {
          path: "platform/ci/lint",
          subdomains: [{ path: "platform/ci/lint/rules", name: "rules" }],
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

  // A node whose level came back empty is a leaf. The tree draws the leaf
  // state for it, which is the dropped toggle, and it writes no sentence
  // inside the tree about the empty level.
  it("turns a node whose level came back empty into a leaf", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": {
        body: {
          path: "",
          subdomains: [{ path: "finance", name: "finance" }],
          notable: [],
        },
      },
      // The expanded node is at the eager read's edge, so it reads its own
      // level and the registry reports nothing under it.
      "/v1/load_domain?path=finance&depth=2": {
        body: { path: "finance", subdomains: [], notable: [] },
      },
      "/v1/search_artifacts": { body: { total_matched: 0 } },
      "/v1/layers": { body: { layers: [] } },
    });
    render(<App />);
    const tree = await screen.findByLabelText("Catalog");
    fireEvent.click(
      within(tree).getAllByRole("button", { expanded: false })[0],
    );
    // The toggle goes once the level resolves to nothing, so the row is a
    // leaf and the tree holds no prose row under it.
    await waitFor(() => {
      expect(within(tree).queryAllByRole("button")).toHaveLength(0);
    });
    expect(within(tree).getByRole("link", { name: "finance" })).toBeTruthy();
    expect(within(tree).queryByText(/No subdomains/)).toBeNull();
    expect(tree.querySelectorAll("p")).toHaveLength(0);
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

  // The refused arm has no catalog to navigate. The tree and the counts are
  // emptied rather than left standing with what an earlier read returned,
  // and the depth marker is kept, because it states a property of this
  // navigation rather than of the catalog.
  it("empties the tree and the counts where the catalog read is refused", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture() },
      "/v1/load_domain": {
        status: 401,
        body: { code: "auth.untrusted_token", message: "not verified" },
      },
      "/v1/search_artifacts": { body: { total_matched: 312 } },
      "/v1/layers": { body: { layers: [adminLayer()] } },
    });
    render(<App />);
    await screen.findByLabelText("Catalog refused");
    expect(
      within(screen.getByLabelText("Catalog")).queryAllByRole("listitem"),
    ).toEqual([]);
    expect(screen.getByTestId("catalog-counts").textContent).toBe("");
    expect(screen.queryByTestId("catalog-ingest")).toBeNull();
    expect(screen.getByTestId("catalog-depth").textContent).toBe("2 levels");
  });

  // A catalog read that failed for a reason other than identity leaves the
  // sidebar with no tree. The tree region says the read failed rather than
  // rendering blank, and the footer figures and the depth marker are
  // withdrawn, because the counts are read once for the page and a figure
  // left standing over a registry that stopped answering states it as
  // current.
  it("says the catalog read failed and withdraws the counts and the depth marker", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/load_domain": {
        status: 503,
        body: { code: "registry.unavailable", message: "down" },
      },
      "/v1/search_artifacts": { body: { total_matched: 312 } },
      "/v1/layers": {
        body: {
          layers: [{ ...adminLayer(), last_ingested_at: new Date().toISOString() }],
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
    expect(screen.queryByTestId("catalog-depth")).toBeNull();
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
      "/v1/search_artifacts": { body: { total_matched: 312 } },
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
      "1 layers · 312 artifacts",
    );
    expect(screen.getByTestId("catalog-depth").textContent).toBe("2 levels");
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
      "/v1/search_artifacts": { body: { total_matched: 312 } },
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
        "1 layers · 312 artifacts",
      );
    });
  });

  // A read that returned a catalog holding no domain gets a line saying so.
  // The depth marker goes with the tree it describes, because a descent
  // stated over an empty sidebar reads as a tree that failed to render.
  it("states that the catalog holds no domains and drops the depth marker", async () => {
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
    expect(screen.queryByTestId("catalog-depth")).toBeNull();
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
    expect(screen.getByText("Lifted from sparse subdomains")).toBeTruthy();
    // The note reaches the reader at the returned edge rather than above the
    // description, beside the count and the control that continues past it.
    const continuation = await screen.findByTestId("listing-continuation");
    expect(continuation.textContent).toContain(
      "The listing was trimmed to fit the response budget.",
    );
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
            { id: "platform/deploy", type: "context", description: "Deploy runbook." },
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
            { path: "finance/ap", name: "ap", description: "Accounts payable." },
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

  it("titles the registry root, which has no leaf segment", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": { body: emptyDomain },
    });
    render(<App />);
    const browser = await screen.findByLabelText("Domain browser");
    expect(within(browser).getByRole("heading", { level: 1 }).textContent).toBe(
      "Registry root",
    );
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
    const head = within(browser).getByRole("heading", { level: 1 }).parentElement;
    expect(head?.textContent).toBe("Registry root1 SUBDOMAIN");
  });

  it("renders a domain that carries neither subdomains nor artifacts as a finished page", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": { body: emptyDomain },
    });
    render(<App />);
    await screen.findByLabelText("Domain browser");
    expect(screen.getByText("This domain has no subdomains.")).toBeTruthy();
    expect(screen.getByText("This domain lists no artifacts.")).toBeTruthy();
  });
});

describe("search", () => {
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
    expect((await screen.findByTestId("result-count")).textContent).toBe(
      "Showing 3 of 143",
    );
    // A result set spans domains, so a ranked row leads with the whole
    // identifier and keeps its type and version beside it rather than in the
    // listing's right-hand column.
    expect(screen.queryByTestId("artifact-row-aside")).toBeNull();
    const first = screen.getByRole("link", { name: "platform/review" });
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
      expect(
        (column.nextElementSibling as HTMLElement).className,
      ).toBe("artifact-row-body");
      expect(indicator.closest(".artifact-row-head")).toBeNull();
    }
    expect(screen.queryByText(/score 8/)).toBeNull();
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
            { id: "eng/deploy", type: "context", description: "Deploy runbook" },
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
      await screen.findByText(
        "Nothing matched. Widen the query or clear a filter.",
      ),
    ).toBeTruthy();
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
    expect(within(trail).queryByRole("link", { name: "pay-invoice" })).toBeNull();
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
    expect([...groups].map((group) => group.querySelector("p")?.textContent))
      .toEqual(["extends", "extended by", "delegated to by"]);
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
  // pre-merge document beside it, which is where the reference survives.
  it("splits the rail's relations into the artifact's own extends and the artifacts extending it", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
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
    expect(rows).toEqual([
      ["layer", "acme-platform"],
      ["hash", "sha256:ab74…8e5f"],
    ]);
    // The section carries no table and no bordered container of its own, so
    // it does not read as a second copy of the frontmatter table below it.
    expect(provenance.querySelector("table")).toBeNull();
    expect(provenance.querySelector(".data-table")).toBeNull();
    // The abbreviation is a display, so the whole value is still on the row.
    expect(within(provenance).queryByText(contentHash)).toBeNull();
    expect(facts.querySelectorAll("dd")[1].getAttribute("title")).toBe(
      contentHash,
    );
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
  // rather than the length of the encoding.
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
      "4 bytes",
      "inline, base64",
    ]);
    const download = within(rows[0]).getByRole("link", { name: "Download" });
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
      "168000000 bytes",
      "fetched on demand",
    ]);
    expect(
      within(rows[1])
        .getByRole("link", { name: "Download" })
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
    fireEvent.change(screen.getByLabelText("Version"), {
      target: { value: "1.0.0" },
    });
    fireEvent.click(screen.getByRole("button", { name: "View" }));
    const notice = await screen.findByTestId("older-version");
    expect(notice.textContent).toContain("1.0.0");
    expect(screen.getByRole("button", { name: "Go to 2.3.0" })).toBeTruthy();
    expect(requests.some((r) => r.url.includes("version=1.0.0"))).toBe(true);
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
    fireEvent.change(screen.getByLabelText("Version"), {
      target: { value: "9.9.9" },
    });
    fireEvent.click(screen.getByRole("button", { name: "View" }));
    const refusal = await screen.findByTestId("version-refused");
    expect(refusal.textContent).toContain("invalid pin");
    // The surface the picker sits on is still drawn, and so is the picker.
    expect(screen.getByLabelText("Artifact viewer")).toBeTruthy();
    expect(screen.getByLabelText("Version")).toBeTruthy();
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
    expect((screen.getByLabelText("Version") as HTMLInputElement).value).toBe(
      "",
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
    fireEvent.change(screen.getByLabelText("Version"), {
      target: { value: "9.9.9" },
    });
    fireEvent.click(screen.getByRole("button", { name: "View" }));
    const refusal = await screen.findByTestId("version-refused");
    const occurrences = (refusal.textContent ?? "").split(
      "registry.not_found",
    ).length - 1;
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
    expect(view.getByRole("button", { name: "Table" }).className).toBe("segment segment-on");
    expect(view.getByRole("button", { name: "Raw YAML" }).className).toBe("segment");
    fireEvent.click(view.getByRole("button", { name: "Raw YAML" }));
    expect(view.getByRole("button", { name: "Raw YAML" }).className).toBe("segment segment-on");
    expect(view.getByRole("button", { name: "Table" }).className).toBe("segment");
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
      expect(
        screen.getByRole("button", { name: "Show less" }),
      ).toBeTruthy();

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
      // The rail states the same field, and its own control goes with it.
      expect(
        screen.queryByRole("button", {
          name: "Show the whole description value",
        }),
      ).toBeNull();
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

    // The rail states the same field again, and the relation links §13.10
    // requires stand under it in the same scrolling column, so an unclipped
    // value there pushes those links off the page.
    // Spec: §13.10
    it("clips the same description in the rail's property table", async () => {
      stubHeights(900);
      stubViewer("The invoice approval path routes each document.");
      await screen.findByLabelText("Artifact viewer");
      const value = screen.getByTestId("property-value-description");
      expect(value.classList.contains("clamped")).toBe(true);
      // The rail's control names the property it opens, because a reader
      // running down the rows meets it away from the value it belongs to.
      const more = screen.getByRole("button", {
        name: "Show the whole description value",
      });
      expect(more.getAttribute("aria-expanded")).toBe("false");
      fireEvent.click(more);
      expect(
        screen
          .getByTestId("property-value-description")
          .classList.contains("clamped"),
      ).toBe(false);
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
    openRowActions("company");
    openRowActions();
    expect(screen.getAllByRole("button", { name: "Unregister" }).length).toBe(
      2,
    );
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
      headers.map((header) => header.querySelector(".label")?.textContent ?? ""),
    ).toEqual(["Move", "Layer", "Source", "Visibility", "Last ingest", ""]);
    expect(headers[5].textContent).toBe("");
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
    expect(screen.queryByRole("button", { name: "Edit" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Unregister" })).toBeNull();
    openRowActions();
    expect(screen.getByRole("button", { name: "Edit" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Unregister" })).toBeTruthy();
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
    fireEvent.click(screen.getByRole("button", { name: "Unregister" }));
    fireEvent.change(screen.getByLabelText("Type the layer ID to confirm"), {
      target: { value: "alice-personal" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Unregister layer" }));
    expect(await screen.findByText(/nothing changed/)).toBeTruthy();
    expect(screen.getByText("auth.forbidden")).toBeTruthy();
    openRowActions();
    expect(
      screen.getByRole("button", { name: "Edit" }).hasAttribute("disabled"),
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
    expect(git.getByText("git@github.com:acme/registry.git")).toBeTruthy();
    expect(git.getByText("catalog/")).toBeTruthy();
    const local = within(layerRow("alice-personal"));
    expect(local.getByText("local")).toBeTruthy();
    expect(local.getByText("/Users/alice/registry")).toBeTruthy();
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
    expect(style.textOverflow).toBe("ellipsis");
    expect(style.overflow).toBe("hidden");
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
          subdomains: [{ path: "platform/ci", name: "ci" }],
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
    fireEvent.click(
      within(tree).getAllByRole("button", { expanded: false })[0],
    );
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
    for (const name of [
      "Register layer",
      "Reingest all",
      "Reingest",
      "Unregister",
      "Edit",
    ]) {
      for (const control of screen.getAllByRole("button", { name })) {
        expect(control.hasAttribute("disabled")).toBe(true);
      }
    }
    // Reordering is a write too, so the rows carry no drag and the handles
    // take no key on a read-only registry rather than committing a move the
    // registry would refuse.
    for (const handle of screen.getAllByLabelText(/^Move .*arrow key$/)) {
      expect(handle.closest("tr")?.getAttribute("draggable")).toBe("false");
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
    fireEvent.click(screen.getByRole("button", { name: "Unregister" }));
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
    fireEvent.click(screen.getByRole("button", { name: "Unregister" }));
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
      "/v1/search_artifacts": { body: { total_matched: 312 } },
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
      "/v1/search_artifacts": { body: { total_matched: 200 } },
      "DELETE /v1/layers": { body: {} },
      "/v1/layers?deleted=true": { body: { layers: [userLayer()] } },
      "/v1/layers": { body: { layers: [adminLayer()] } },
    });
    openRowActions();
    fireEvent.click(screen.getByRole("button", { name: "Unregister" }));
    await screen.findByLabelText("Unregister alice-personal");
    fireEvent.change(screen.getByLabelText("Type the layer ID to confirm"), {
      target: { value: "alice-personal" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Unregister layer" }));
    await waitFor(() => {
      expect(screen.getByTestId("catalog-counts").textContent).toBe(
        "1 layers · 200 artifacts",
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
        body: { path: "", subdomains: [{ path: "eng", name: "eng" }], notable: [] },
      },
      "/v1/search_artifacts": { body: { total_matched: 9 } },
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
      "/v1/search_artifacts": { body: { total_matched: 10 } },
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
      "1 layers · 10 artifacts",
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
    fireEvent.click(screen.getByRole("button", { name: "Unregister" }));
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
    fireEvent.click(screen.getByRole("button", { name: "Unregister" }));
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
    fireEvent.click(screen.getByRole("button", { name: "Unregister" }));
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
      screen
        .getByRole("button", { name: "Register" })
        .hasAttribute("disabled"),
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
    // A local source reads no ref, so the hold does not reach it.
    fireEvent.click(screen.getByRole("radio", { name: "Local folder" }));
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
    expect(screen.getByTestId("visibility-note").textContent).toBe(
      "Visibility is fixed at registration.",
    );
    expect(
      within(dialog).getByRole("button", { name: "Register" })
        .className,
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
    expect(screen.getByTestId("visibility-consequence").textContent).toBe(
      "No grants — only you will see this layer.",
    );
    fireEvent.click(screen.getByLabelText("Organization"));
    fireEvent.click(screen.getByLabelText("Groups"));
    fireEvent.change(
      screen.getByLabelText("Group names, separated by commas"),
      {
        target: { value: "secops, appsec" },
      },
    );
    expect(screen.getByTestId("visibility-consequence").textContent).toBe(
      "Everyone in this tenant will see this layer — the organization grant already covers secops and appsec.",
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
    // Nothing typed yet, so every known name is on offer.
    expect(screen.getByTestId("group-picker-count").textContent).toBe(
      "4 of 4 match",
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
    fireEvent.click(within(picker).getByRole("button", { name: "platform-eng" }));
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
    expect(screen.queryByRole("button", { name: "Remove platform-eng" })).toBeNull();
    expect(screen.getByRole("button", { name: "Remove platfrom" })).toBeTruthy();
  });

  // The registration reloads the list, and the reload answers over the
  // network rather than within the batch that issued it, so the list read is
  // deferred here. The panel must hold the reveal across a reload that
  // reports loading, because the secret is served once and a panel that
  // remounted the form in its place would leave the reader with no copy.
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
    expect(
      requests.some((r) => r.url.startsWith("/v1/layers/reingest")),
    ).toBe(false);
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
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
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
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
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
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
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
    const region = screen.getByTestId("reorder-announcement");
    expect(region.getAttribute("aria-live")).toBe("polite");
    expect(region.textContent).toBe("");
    fireEvent.keyDown(screen.getByLabelText(moveHandleLabel("alice-personal")), {
      key: "ArrowDown",
    });
    await waitFor(() => {
      expect(screen.getByTestId("reorder-announcement").textContent).toBe(
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
    fireEvent.keyDown(screen.getByLabelText(moveHandleLabel("alice-personal")), {
      key: "ArrowUp",
    });
    await waitFor(() => {
      expect(screen.getByLabelText("Layer panel")).toBeTruthy();
    });
    expect(requests.some((r) => r.url === "/v1/layers/reorder")).toBe(false);
  });

  // The fan-out issues one request per layer in sequence, and the press is
  // one press, so the run answers with one report: the combined counts, a row
  // per layer, and no dialog naming a single layer.
  it("reingests every layer in sequence and reports the run once", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [adminLayer(), userLayer()] } },
      "/v1/layers/reingest?id=company": { body: { accepted: 3, idempotent: 1 } },
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

  // A layer the registry refused is part of the run's result. Reported on the
  // row alone it sat behind the reports of every other layer, so the roll-up
  // names it with the code and the message its envelope carried.
  it("names a refused layer in the run report", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [adminLayer(), userLayer()] } },
      "/v1/layers/reingest?id=company": { body: { accepted: 3, idempotent: 1 } },
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
    expect(within(counts).getByText("accepted").previousSibling?.textContent).toBe("4");
    expect(within(counts).getByText("unchanged").previousSibling?.textContent).toBe("2");
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
    fireEvent.click(screen.getByRole("button", { name: "1 artifact rejected" }));
    expect(screen.getByLabelText("Rejected artifacts")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Back to the counts" }));
    fireEvent.click(
      screen.getByRole("button", { name: "1 immutability conflict" }),
    );
    expect(screen.getByText("platform/lint@1.0.0")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Back to the counts" }));
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
    // A dialog opened to be read can be left without acting on it.
    fireEvent.keyDown(document, { key: "Escape" });
    expect(
      screen.queryByLabelText("Reingest result for alice-personal"),
    ).toBeNull();
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
    const erasesOn = new Date(
      unregisteredAt.getTime() + 30 * 24 * 60 * 60 * 1000,
    )
      .toISOString()
      .slice(0, 10);
    expect(surface.textContent).toContain(
      unregisteredAt.toISOString().slice(0, 10),
    );
    expect(surface.textContent).toContain(erasesOn);
    const left = screen.getByTestId("days-left-alice-personal");
    expect(left.textContent).toBe("1 days left");
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
    expect(screen.getByTestId("days-left-alice-roomy").textContent).toBe(
      "29 days left",
    );
    expect(roomy.className).not.toContain("depleting-urgent");

    expect(screen.getByTestId("days-left-alice-expiring").textContent).toBe(
      "1 days left",
    );
    expect(expiring.className).toContain("depleting-urgent");
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
      expect(link.textContent).toBe("\u21ba Recently unregistered \u00b7 1");
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
      headers.map((header) => header.querySelector(".label")?.textContent ?? ""),
    ).toEqual(["Layer", "Source", "Unregistered", "Erased on", "Actions"]);
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
 */
function dragRowOnto(from: string, onto: string): void {
  const source = layerRow(from);
  const target = layerRow(onto);
  fireEvent.dragStart(source);
  fireEvent.dragOver(target);
  fireEvent.drop(target);
}

function layerRow(id: string): HTMLElement {
  const row = screen.getByLabelText(moveHandleLabel(id)).closest("tr");
  if (row === null) {
    throw new Error(`no layer row for ${id}`);
  }
  return row;
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
    fireEvent.keyDown(panel, { key: "Escape" });
    expect(screen.queryByTestId("palette")).toBeNull();
    expect(document.activeElement).toBe(trigger);
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
      await screen.findByText(/Nothing matched nothingmatches/),
    ).toBeTruthy();
    expect(within(panel).queryByText(/hidden/i)).toBeNull();
    expect(within(panel).queryByText(/permission/i)).toBeNull();
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
    expect(within(appearance).getByRole("button", { name: "system" }).className).toBe(
      "segment segment-on",
    );
    fireEvent.click(within(menu).getByRole("button", { name: "dark" }));
    expect(within(appearance).getByRole("button", { name: "dark" }).className).toBe(
      "segment segment-on",
    );
    expect(window.document.documentElement.getAttribute("data-theme")).toBe(
      "dark",
    );
    expect(window.localStorage.getItem("podium.theme")).toBe("dark");
    fireEvent.click(within(menu).getByRole("button", { name: "system" }));
    expect(window.document.documentElement.hasAttribute("data-theme")).toBe(
      false,
    );
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

  // The trimmed case is a pill among the header badges and a line at the end
  // of the list stating what is on the page against the match count, with a
  // control that continues past the returned edge. The continuation is the
  // scoped search the line takes its total from, because load_domain offers
  // no lever over the notable list.
  it("states the shown count against the total and continues into the scoped search", async () => {
    stubRegistry({
      "/v1/ui/session": { body: posture({ public_mode: true }) },
      "/v1/load_domain": { body: trimmed },
      "/v1/search_artifacts": { body: { total_matched: 21 } },
    });
    goTo("#/domain/platform");
    render(<App />);
    await screen.findByLabelText("Domain browser");
    expect(screen.getByText("listing trimmed")).toBeTruthy();
    const line = await screen.findByTestId("listing-continuation");
    await waitFor(() => {
      expect(line.textContent).toContain("2 of 21 artifacts shown.");
    });
    const cont = within(line).getByRole("link", { name: "Load the rest" });
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
    expect(within(browser).getByTestId("show-all-subdomains").textContent).toBe(
      "Show all 24 subdomains",
    );
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
      within(tables[0]).getByRole("link", { name: "platform/deploy" }),
    ).toBeTruthy();
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
    // the one filled with the track colour.
    const viewSwitch = subrow.getByRole("group", { name: "Subdomain view" });
    expect(viewSwitch.className.split(" ")).toContain("segmented");
    expect(within(viewSwitch).getByRole("button", { name: "grid" }).className).toBe(
      "segment segment-on",
    );
    expect(within(viewSwitch).getByRole("button", { name: "list" }).className).toBe("segment");
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
    const arthead = within(browser).getByRole("heading", { name: "Artifacts" })
      .parentElement;
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
      within(browser).queryByRole("link", { name: "platform/notes" }),
    ).toBeNull();
    fireEvent.click(all);
    expect(
      within(browser).getByRole("link", { name: "platform/notes" }),
    ).toBeTruthy();

    // The in-domain filter runs over the identifier the first column carries.
    fireEvent.change(artrow.getByLabelText("Filter in this domain"), {
      target: { value: "notes" },
    });
    expect(
      within(browser).queryByRole("link", { name: "platform/lint" }),
    ).toBeNull();
    fireEvent.change(artrow.getByLabelText("Filter in this domain"), {
      target: { value: "" },
    });

    // The picks stand in their own block under a header carrying their count,
    // and the rest of the listing carries no heading of its own.
    const curated = within(browser).getByText("Curated by the domain author")
      .parentElement;
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
  function resourcePage(): void {
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
    expect(screen.getByTestId("download-all").textContent).toBe(
      "Download all ↓ 2.0 MB",
    );
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
});

describe("a refused layer write", () => {
  function refusedPage(): void {
    stubRegistry({
      "/v1/ui/session": { body: posture({ subject: "alice@acme.com" }) },
      "/v1/layers": { body: { layers: [userLayer("bob@acme.com")] } },
      "/v1/layers?deleted=true": { body: { layers: [] } },
      "DELETE /v1/layers": {
        status: 403,
        body: { code: "auth.forbidden", message: "not permitted" },
      },
    });
    goTo("#/layers");
  }

  async function refuseAnUnregister(): Promise<void> {
    await screen.findByLabelText("Layer panel");
    openRowActions();
    fireEvent.click(screen.getByRole("button", { name: "Unregister" }));
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
    refusedPage();
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
      screen.getByRole("button", { name: "Edit" }).hasAttribute("disabled"),
    ).toBe(false);
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
      expect(link.textContent).toBe("↺ Recently unregistered · 1");
    });
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
      "No layers are registered under this tenant.",
    );
    expect(panel.textContent).not.toContain("Precedence");
    expect(panel.textContent).not.toContain("composes above");
    expect(panel.textContent).not.toContain("Reordering takes effect");
    // The recoverable read answers on the same stub, so it reports nothing
    // recoverable and the link states no figure beside itself.
    const link = screen.getByTestId("recoverable-link");
    await waitFor(() => {
      expect(link.textContent).toBe("↺ Recently unregistered");
    });
    expect(
      screen
        .getByRole("button", { name: "Reingest all" })
        .hasAttribute("disabled"),
    ).toBe(true);
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
    expect(screen.getByRole("tab", { name: /Frontmatter/ }).getAttribute("aria-selected")).toBe("true");
    expect(document.activeElement).toBe(
      screen.getByRole("tab", { name: /Frontmatter/ }),
    );
    expect(screen.getByTestId("frontmatter-table")).toBeTruthy();
    // End lands on the last tab, and the arrows wrap rather than stopping at
    // the edge.
    fireEvent.keyDown(list, { key: "End" });
    expect(
      screen.getByRole("tab", { name: "Authored source" }).getAttribute("aria-selected"),
    ).toBe("true");
    fireEvent.keyDown(list, { key: "ArrowRight" });
    expect(screen.getByRole("tab", { name: "Rendered" }).getAttribute("aria-selected")).toBe("true");
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
