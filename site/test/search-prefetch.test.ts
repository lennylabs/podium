// @vitest-environment jsdom
import MiniSearch from "minisearch";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { SEARCH_FIELDS, STORED_FIELDS, SEARCH_OPTIONS } from "../src/client/search-fields";
import { processTerm } from "../src/client/search-terms";

const INDEX_URL = "/base/assets/search-index-test.json";

/** A serialized index holding one page, in the form the build writes. */
function serializedIndex(): string {
  const index = new MiniSearch({
    fields: SEARCH_FIELDS,
    storeFields: STORED_FIELDS,
    processTerm,
    searchOptions: SEARCH_OPTIONS,
  });
  index.add({ id: "/install.html", page: "Install", title: "Install", text: "Install the podium binary." });
  return JSON.stringify(index.toJSON());
}

/** Loads a fresh copy of the module, so its index and loading state start empty. */
async function freshMountSearch(): Promise<(basePath: string) => void> {
  vi.resetModules();
  return (await import("../src/client/search")).mountSearch;
}

let idle: Array<() => void>;
let fetchMock: ReturnType<typeof vi.fn>;

beforeEach(() => {
  document.body.innerHTML = `<input data-search-input>`;
  document.body.dataset["searchIndex"] = INDEX_URL;
  idle = [];
  vi.stubGlobal("requestIdleCallback", (task: () => void) => {
    idle.push(task);
    return idle.length;
  });
  fetchMock = vi.fn(async () => new Response(serializedIndex(), { status: 200 }));
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  // Restores the prototype getter after a test that overrides the ready state.
  Reflect.deleteProperty(document, "readyState");
  Object.defineProperty(navigator, "connection", { value: undefined, configurable: true });
  document.body.innerHTML = "";
});

/** Opens the search overlay the way a reader's focus on the field does. */
function openSearch(): void {
  document.querySelector<HTMLInputElement>("[data-search-input]")?.dispatchEvent(new Event("focus"));
}

/** Types a query into the open overlay. */
function typeQuery(query: string): void {
  const input = document.querySelector<HTMLInputElement>("[data-search-overlay] input");
  expect(input).not.toBeNull();
  input!.value = query;
  input!.dispatchEvent(new Event("input"));
}

function resultsText(): string {
  return document.querySelector("[data-search-results]")?.textContent ?? "";
}

/** A response holding the one-page index. */
function indexResponse(): Response {
  return new Response(serializedIndex(), { status: 200 });
}

/** A fetch that never answers and rejects only when its signal aborts. */
function hangingFetch(_url: string, init?: RequestInit): Promise<Response> {
  return new Promise<Response>((_resolve, reject) => {
    init?.signal?.addEventListener("abort", () =>
      reject(new DOMException("The operation was aborted.", "AbortError")),
    );
  });
}

/** Runs every queued idle task and lets the loads they start settle. */
async function runIdle(): Promise<void> {
  for (const task of idle.splice(0)) task();
  await vi.waitFor(() => expect(fetchMock).toHaveBeenCalled());
  await new Promise((resolve) => setTimeout(resolve, 0));
}

describe("search index prefetch", () => {
  it("downloads the index once the page is idle, before any interaction", async () => {
    const mountSearch = await freshMountSearch();
    mountSearch("/base");
    expect(fetchMock).not.toHaveBeenCalled();
    expect(idle).toHaveLength(1);
    await runIdle();
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(fetchMock.mock.calls[0]?.[0]).toBe(INDEX_URL);
  });

  it("does not download the index again when the reader then opens search", async () => {
    const mountSearch = await freshMountSearch();
    mountSearch("/base");
    await runIdle();
    document.querySelector<HTMLInputElement>("[data-search-input]")?.dispatchEvent(new Event("focus"));
    const input = document.querySelector<HTMLInputElement>("[data-search-overlay] input");
    expect(input).not.toBeNull();
    input!.value = "install";
    input!.dispatchEvent(new Event("input"));
    await vi.waitFor(() =>
      expect(document.querySelector("[data-search-results]")?.textContent).toContain("Install"),
    );
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("retries on the next interaction after a failed prefetch", async () => {
    fetchMock.mockImplementationOnce(async () => new Response("missing", { status: 404 }));
    const mountSearch = await freshMountSearch();
    mountSearch("/base");
    await runIdle();
    expect(fetchMock).toHaveBeenCalledTimes(1);
    document.querySelector<HTMLInputElement>("[data-search-input]")?.dispatchEvent(new Event("focus"));
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
  });

  it("shares the in-flight prefetch when the reader opens search before it finishes", async () => {
    let release: () => void = () => undefined;
    fetchMock.mockImplementationOnce(
      () =>
        new Promise<Response>((resolve) => {
          release = () => resolve(indexResponse());
        }),
    );
    const mountSearch = await freshMountSearch();
    mountSearch("/base");
    for (const task of idle.splice(0)) task();
    expect(fetchMock).toHaveBeenCalledTimes(1);

    openSearch();
    typeQuery("install");
    expect(fetchMock).toHaveBeenCalledTimes(1);

    release();
    await vi.waitFor(() => expect(resultsText()).toContain("Install"));
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("waits for the load event when the page is still loading", async () => {
    Object.defineProperty(document, "readyState", { value: "loading", configurable: true });
    const mountSearch = await freshMountSearch();
    mountSearch("/base");
    expect(idle).toHaveLength(0);

    Reflect.deleteProperty(document, "readyState");
    window.dispatchEvent(new Event("load"));
    expect(idle).toHaveLength(1);
    // The listener is registered once, so a second load event schedules nothing.
    window.dispatchEvent(new Event("load"));
    expect(idle).toHaveLength(1);

    await runIdle();
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("shows a message after a failed load and retries when the reader types", async () => {
    fetchMock
      .mockImplementationOnce(async () => new Response("missing", { status: 404 }))
      .mockImplementationOnce(async () => new Response("missing", { status: 503 }));
    const mountSearch = await freshMountSearch();
    mountSearch("/base");
    await runIdle();
    expect(fetchMock).toHaveBeenCalledTimes(1);

    openSearch();
    await vi.waitFor(() => expect(resultsText()).toContain("Search could not load"));
    expect(fetchMock).toHaveBeenCalledTimes(2);

    typeQuery("install");
    await vi.waitFor(() => expect(resultsText()).toContain("Install"));
    expect(fetchMock).toHaveBeenCalledTimes(3);
    expect(resultsText()).not.toContain("Search could not load");
  });

  it("abandons a hung index request after its timeout and retries on the next query", async () => {
    fetchMock.mockImplementationOnce(hangingFetch);
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
    const mountSearch = await freshMountSearch();
    mountSearch("/base");
    openSearch();
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const signal = (fetchMock.mock.calls[0]?.[1] as RequestInit | undefined)?.signal;
    expect(signal).toBeDefined();

    await vi.advanceTimersByTimeAsync(14_999);
    expect(signal?.aborted).toBe(false);
    await vi.advanceTimersByTimeAsync(1);
    expect(signal?.aborted).toBe(true);

    vi.useRealTimers();
    await vi.waitFor(() => expect(resultsText()).toContain("Search could not load"));

    typeQuery("install");
    await vi.waitFor(() => expect(resultsText()).toContain("Install"));
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("keeps the on-demand fetch when the browser asks to save data", async () => {
    Object.defineProperty(navigator, "connection", { value: { saveData: true }, configurable: true });
    const mountSearch = await freshMountSearch();
    mountSearch("/base");
    expect(idle).toHaveLength(0);
    expect(fetchMock).not.toHaveBeenCalled();
    document.querySelector<HTMLInputElement>("[data-search-input]")?.dispatchEvent(new Event("focus"));
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
  });

  it("waits a fixed delay when the browser has no requestIdleCallback", async () => {
    vi.stubGlobal("requestIdleCallback", undefined);
    // Only the timer functions are faked. The default set also installs a fake
    // requestIdleCallback, which would put the idle path back in place and
    // leave the fallback untested.
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
    try {
      const mountSearch = await freshMountSearch();
      mountSearch("/base");
      expect(fetchMock).not.toHaveBeenCalled();
      vi.advanceTimersByTime(1999);
      expect(fetchMock).not.toHaveBeenCalled();
      vi.advanceTimersByTime(1);
      expect(fetchMock).toHaveBeenCalledTimes(1);
    } finally {
      vi.useRealTimers();
    }
  });
});
