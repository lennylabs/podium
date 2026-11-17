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
  vi.unstubAllGlobals();
  Object.defineProperty(navigator, "connection", { value: undefined, configurable: true });
  document.body.innerHTML = "";
});

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
    vi.useFakeTimers();
    try {
      const mountSearch = await freshMountSearch();
      mountSearch("/base");
      expect(fetchMock).not.toHaveBeenCalled();
      vi.advanceTimersByTime(2000);
      expect(fetchMock).toHaveBeenCalledTimes(1);
    } finally {
      vi.useRealTimers();
    }
  });
});
