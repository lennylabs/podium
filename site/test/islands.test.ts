// @vitest-environment jsdom
import { act, createElement } from "react";
import { renderToString } from "react-dom/server";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { readPanels } from "../src/components/content/tab-data";
import { Tabs } from "../src/components/content/Tabs";

/**
 * A test registry: one leaf island whose component is a fixture, and the tab
 * strip the site ships. The client mounts against whatever the registry names,
 * so a fixture entry exercises the same paths a real island takes.
 */
vi.mock("../src/components/islands/registry", async () => {
  const react = await import("react");
  const tabs = await import("../src/components/content/Tabs");

  return {
    registry: {
      sparkle: {
        props: { label: { kind: "string", required: true } },
        children: { kind: "none" },
        fallback: { from: "prop", name: "label" },
        load: async () => ({
          default: (props: { label: string }) =>
            react.createElement("span", { className: "sparkle" }, `mounted ${props.label}`),
        }),
      },
      quiet: {
        props: {},
        children: { kind: "none" },
        fallback: { from: "prop", name: "label" },
      },
      tabs: {
        props: {},
        children: { kind: "directives", name: "tab", min: 2 },
        fallback: { from: "children" },
        load: async () => ({ default: tabs.Tabs }),
      },
    },
  };
});

const { mountIslands, unmountIslands } = await import("../src/client/islands");

const PANELS = [
  { label: "npm", html: "<p>Install with <strong>npm</strong>.</p>" },
  { label: "Homebrew", html: "<p>Install with <em>Homebrew</em>.</p>" },
];

function setReducedMotion(reduced: boolean): void {
  window.matchMedia = ((query: string) => ({
    matches: query.includes("prefers-reduced-motion") ? reduced : false,
    media: query,
    onchange: null,
    addListener: () => undefined,
    removeListener: () => undefined,
    addEventListener: () => undefined,
    removeEventListener: () => undefined,
    dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
}

async function mount(): Promise<void> {
  await act(async () => {
    await mountIslands(document.querySelectorAll<HTMLElement>("[data-island]"));
  });
}

beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  setReducedMotion(false);
  window.localStorage.clear();
  document.body.innerHTML = "";
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("mountIslands", () => {
  function leafMarkup(name = "sparkle", props = '{"label":"the catalog"}'): string {
    return (
      `<div class="island" data-island="${name}" data-island-id="island-0" ` +
      `data-island-mount="replace" data-island-props='${props}'>` +
      `<img class="diagram" src="/base/assets/diagrams/sample.svg" alt="A sample diagram">` +
      `</div>`
    );
  }

  it("mounts a leaf island's component over its static fallback", async () => {
    document.body.innerHTML = leafMarkup();

    await mount();

    const island = document.querySelector('[data-island="sparkle"]');
    expect(island?.querySelector(".sparkle")?.textContent).toBe("mounted the catalog");
    expect(island?.querySelector("img")).toBeNull();
  });

  it("keeps the leaf fallback in place when the reader asks for reduced motion", async () => {
    setReducedMotion(true);
    document.body.innerHTML = leafMarkup();

    await mount();

    const island = document.querySelector('[data-island="sparkle"]');
    expect(island?.querySelector("img")?.getAttribute("src")).toBe(
      "/base/assets/diagrams/sample.svg",
    );
    expect(island?.querySelector(".sparkle")).toBeNull();
  });

  it("leaves an island the registry does not define alone", async () => {
    document.body.innerHTML = leafMarkup("gallery");

    await mount();

    expect(document.querySelector('[data-island="gallery"] img')).not.toBeNull();
  });

  it("leaves an island whose registry entry loads no component alone", async () => {
    document.body.innerHTML = leafMarkup("quiet");

    await mount();

    expect(document.querySelector('[data-island="quiet"] img')).not.toBeNull();
  });

  it("leaves an island whose props are not valid JSON alone", async () => {
    document.body.innerHTML = leafMarkup("sparkle", "{not json}");

    await mount();

    expect(document.querySelector('[data-island="sparkle"] img')).not.toBeNull();
    expect(document.querySelector(".sparkle")).toBeNull();
  });

  it("hydrates a container island over the markup the build wrote", async () => {
    const server = renderToString(createElement(Tabs, { panels: PANELS }));
    document.body.innerHTML =
      `<div class="island" data-island="tabs" data-island-id="island-0" ` +
      `data-island-mount="hydrate" data-island-props="{}">${server}</div>`;

    await mount();

    const strip = document.querySelector("[data-tabs]");
    expect(strip?.getAttribute("data-mounted")).toBe("true");

    const panels = [...document.querySelectorAll<HTMLElement>("[data-tab-label]")];
    expect(panels.map((panel) => panel.getAttribute("data-tab-label"))).toEqual([
      "npm",
      "Homebrew",
    ]);
    expect(panels[0]?.hidden).toBe(false);
    expect(panels[1]?.hidden).toBe(true);
    expect(document.querySelectorAll('[role="tab"]')).toHaveLength(2);
  });

  it("restores the panel the reader chose on an earlier page", async () => {
    window.localStorage.setItem("podium-tab-choice", "Homebrew");
    const server = renderToString(createElement(Tabs, { panels: PANELS }));
    document.body.innerHTML =
      `<div class="island" data-island="tabs" data-island-mount="hydrate">${server}</div>`;

    await mount();

    const panels = [...document.querySelectorAll<HTMLElement>("[data-tab-label]")];
    expect(panels[0]?.hidden).toBe(true);
    expect(panels[1]?.hidden).toBe(false);
  });

  it("remembers the panel the reader clicks", async () => {
    const server = renderToString(createElement(Tabs, { panels: PANELS }));
    document.body.innerHTML =
      `<div class="island" data-island="tabs" data-island-mount="hydrate">${server}</div>`;
    await mount();

    const buttons = document.querySelectorAll<HTMLButtonElement>(".tabs-tab");
    await act(async () => {
      buttons[1]?.click();
    });

    expect(window.localStorage.getItem("podium-tab-choice")).toBe("Homebrew");
    const panels = [...document.querySelectorAll<HTMLElement>("[data-tab-label]")];
    expect(panels[1]?.hidden).toBe(false);
  });

  it("selects the first panel when storage cannot be read", async () => {
    vi.spyOn(window.localStorage, "getItem").mockImplementation(() => {
      throw new Error("storage is blocked");
    });
    const server = renderToString(createElement(Tabs, { panels: PANELS }));
    document.body.innerHTML =
      `<div class="island" data-island="tabs" data-island-mount="hydrate">${server}</div>`;

    await mount();

    const panels = [...document.querySelectorAll<HTMLElement>("[data-tab-label]")];
    expect(panels[0]?.hidden).toBe(false);
    expect(panels[1]?.hidden).toBe(true);
  });

  it("switches panels even when storage cannot be written", async () => {
    const server = renderToString(createElement(Tabs, { panels: PANELS }));
    document.body.innerHTML =
      `<div class="island" data-island="tabs" data-island-mount="hydrate">${server}</div>`;
    await mount();
    vi.spyOn(window.localStorage, "setItem").mockImplementation(() => {
      throw new Error("storage is blocked");
    });

    const buttons = document.querySelectorAll<HTMLButtonElement>(".tabs-tab");
    await act(async () => {
      buttons[1]?.click();
    });

    const panels = [...document.querySelectorAll<HTMLElement>("[data-tab-label]")];
    expect(panels[1]?.hidden).toBe(false);
  });

  it("keeps the first panel when the stored choice names no panel", async () => {
    window.localStorage.setItem("podium-tab-choice", "Chocolatey");
    const server = renderToString(createElement(Tabs, { panels: PANELS }));
    document.body.innerHTML =
      `<div class="island" data-island="tabs" data-island-mount="hydrate">${server}</div>`;

    await mount();

    const panels = [...document.querySelectorAll<HTMLElement>("[data-tab-label]")];
    expect(panels[0]?.hidden).toBe(false);
  });

  it("does nothing for a page carrying no islands", async () => {
    document.body.innerHTML = "<p>Prose only.</p>";

    await mount();

    expect(document.body.innerHTML).toBe("<p>Prose only.</p>");
  });

  it("mounts an island once, so a second pass over it changes nothing", async () => {
    document.body.innerHTML = leafMarkup();
    await mount();
    const mounted = document.querySelector(".sparkle");

    await mount();

    expect(document.querySelector(".sparkle")).toBe(mounted);
  });
});

describe("unmountIslands", () => {
  /**
   * The client router replaces the article on a navigation, so the roots
   * rendering inside it are released first.
   */
  it("releases the roots inside a region and leaves the markup empty", async () => {
    document.body.innerHTML =
      '<article id="doc-article"><div class="island" data-island="sparkle" ' +
      `data-island-mount="replace" data-island-props='{"label":"the catalog"}'></div></article>`;
    await act(async () => {
      await mountIslands(document.querySelectorAll<HTMLElement>("[data-island]"));
    });
    expect(document.querySelector(".sparkle")).not.toBeNull();

    const article = document.querySelector("#doc-article") as HTMLElement;
    await act(async () => {
      unmountIslands(article);
    });

    expect(document.querySelector(".sparkle")).toBeNull();
  });

  it("leaves a root outside the region alone", async () => {
    document.body.innerHTML =
      '<article id="doc-article"></article><div class="island" data-island="sparkle" ' +
      `data-island-mount="replace" data-island-props='{"label":"the catalog"}'></div>`;
    await act(async () => {
      await mountIslands(document.querySelectorAll<HTMLElement>("[data-island]"));
    });

    const article = document.querySelector("#doc-article") as HTMLElement;
    await act(async () => {
      unmountIslands(article);
    });

    expect(document.querySelector(".sparkle")).not.toBeNull();
  });

  it("mounts a region again after its roots were released", async () => {
    document.body.innerHTML =
      '<article id="doc-article"><div class="island" data-island="sparkle" ' +
      `data-island-mount="replace" data-island-props='{"label":"the catalog"}'></div></article>`;
    const article = document.querySelector("#doc-article") as HTMLElement;
    await act(async () => {
      await mountIslands(document.querySelectorAll<HTMLElement>("[data-island]"));
    });
    await act(async () => {
      unmountIslands(article);
    });

    await act(async () => {
      await mountIslands(document.querySelectorAll<HTMLElement>("[data-island]"));
    });

    expect(document.querySelector(".sparkle")?.textContent).toBe("mounted the catalog");
  });
});

describe("DiagramIsland", () => {
  it("renders the registered variant once it loads", async () => {
    const { diagramVariants } = await import("../src/components/diagrams");
    const { DiagramIsland } = await import("../src/components/content/DiagramIsland");
    const { createRoot } = await import("react-dom/client");

    diagramVariants["sample"] = async () => ({
      default: (props: { name: string; alt: string }) =>
        createElement("p", { className: "animated" }, props.alt),
    });

    try {
      const host = document.createElement("div");
      document.body.append(host);

      await act(async () => {
        createRoot(host).render(
          createElement(DiagramIsland, {
            name: "/base/assets/diagrams/sample.svg",
            alt: "A sample diagram",
          }),
        );
      });

      expect(host.querySelector(".animated")?.textContent).toBe("A sample diagram");
    } finally {
      delete diagramVariants["sample"];
    }
  });

  it("renders the checked-in SVG when the stem has no variant", async () => {
    const { DiagramIsland } = await import("../src/components/content/DiagramIsland");
    const { createRoot } = await import("react-dom/client");

    const host = document.createElement("div");
    document.body.append(host);

    await act(async () => {
      createRoot(host).render(
        createElement(DiagramIsland, {
          name: "/base/assets/diagrams/sample.svg",
          alt: "A sample diagram",
        }),
      );
    });

    expect(host.querySelector("img.diagram")?.getAttribute("src")).toBe(
      "/base/assets/diagrams/sample.svg",
    );
  });
});

describe("readPanels", () => {
  it("reads every panel in source order with its markup intact", () => {
    const root = document.createElement("div");
    root.innerHTML = renderToString(createElement(Tabs, { panels: PANELS }));

    expect(readPanels(root)).toEqual(PANELS);
  });

  it("reads an empty label for a panel that carries none", () => {
    const root = document.createElement("div");
    root.innerHTML = '<div data-tab-label><p>Body.</p></div>';

    expect(readPanels(root)).toEqual([{ label: "", html: "<p>Body.</p>" }]);
  });

  it("reads nothing from markup carrying no panels", () => {
    const root = document.createElement("div");
    root.innerHTML = "<p>Prose only.</p>";

    expect(readPanels(root)).toEqual([]);
  });
});
