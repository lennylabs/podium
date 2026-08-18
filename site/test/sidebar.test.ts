// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from "vitest";

import { DESKTOP_QUERY, startSidebar } from "../src/client/sidebar";

type MediaListener = (event: { matches: boolean }) => void;

const listeners: MediaListener[] = [];

/**
 * jsdom has no media queries, so matchMedia is stood up here. The list of
 * listeners is what a test uses to say the viewport crossed the breakpoint.
 */
function stubMatchMedia(matches = false): void {
  listeners.length = 0;
  window.matchMedia = ((query: string) => ({
    matches,
    media: query,
    addEventListener: (_: string, fn: MediaListener) => {
      listeners.push(fn);
    },
    removeEventListener: () => undefined,
    addListener: (fn: MediaListener) => {
      listeners.push(fn);
    },
    removeListener: () => undefined,
    onchange: null,
    dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
}

function render(): { toggle: HTMLElement; sidebar: HTMLElement } {
  document.body.innerHTML = `
    <button data-sidebar-toggle aria-expanded="false">Menu</button>
    <nav data-sidebar data-open="false">
      <div data-nav-section data-collapsed="false">
        <button data-nav-group aria-expanded="true">Getting started</button>
        <a href="/base/getting-started/quickstart.html">Quickstart</a>
      </div>
    </nav>`;
  document.body.removeAttribute("data-drawer-open");
  return {
    toggle: document.querySelector("[data-sidebar-toggle]") as HTMLElement,
    sidebar: document.querySelector("[data-sidebar]") as HTMLElement,
  };
}

describe("startSidebar", () => {
  beforeEach(() => {
    stubMatchMedia();
  });

  it("opens and closes the drawer, and marks the body while it is open", () => {
    const { toggle, sidebar } = render();
    startSidebar();

    toggle.click();
    expect(sidebar.getAttribute("data-open")).toBe("true");
    expect(toggle.getAttribute("aria-expanded")).toBe("true");
    expect(document.body.hasAttribute("data-drawer-open")).toBe(true);

    toggle.click();
    expect(sidebar.getAttribute("data-open")).toBe("false");
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    expect(document.body.hasAttribute("data-drawer-open")).toBe(false);
  });

  it("closes when a link inside it is followed", () => {
    const { toggle, sidebar } = render();
    startSidebar();
    toggle.click();

    (sidebar.querySelector("a") as HTMLElement).click();

    expect(sidebar.getAttribute("data-open")).toBe("false");
    expect(document.body.hasAttribute("data-drawer-open")).toBe(false);
  });

  it("stays open when the reader clicks the panel itself", () => {
    const { toggle, sidebar } = render();
    startSidebar();
    toggle.click();

    (sidebar.querySelector("[data-nav-section]") as HTMLElement).click();

    expect(sidebar.getAttribute("data-open")).toBe("true");
  });

  it("closes on Escape and returns focus to the button", () => {
    const { toggle, sidebar } = render();
    const focus = vi.spyOn(toggle, "focus");
    startSidebar();
    toggle.click();

    document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));

    expect(sidebar.getAttribute("data-open")).toBe("false");
    expect(focus).toHaveBeenCalled();
  });

  it("ignores Escape when the drawer is already closed", () => {
    const { toggle } = render();
    const focus = vi.spyOn(toggle, "focus");
    startSidebar();

    document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));

    expect(focus).not.toHaveBeenCalled();
  });

  it("releases the drawer when the viewport reaches the desktop width", () => {
    const { toggle, sidebar } = render();
    startSidebar();
    toggle.click();

    for (const listener of listeners) listener({ matches: true });

    expect(sidebar.getAttribute("data-open")).toBe("false");
    expect(document.body.hasAttribute("data-drawer-open")).toBe(false);
  });

  it("leaves an open drawer alone while the viewport stays narrow", () => {
    const { toggle, sidebar } = render();
    startSidebar();
    toggle.click();

    for (const listener of listeners) listener({ matches: false });

    expect(sidebar.getAttribute("data-open")).toBe("true");
  });

  it("watches the width the stylesheet returns the tree to the layout at", () => {
    render();
    const spy = vi.spyOn(window, "matchMedia");
    startSidebar();

    expect(spy).toHaveBeenCalledWith(DESKTOP_QUERY);
  });

  it("collapses and expands a navigation group", () => {
    render();
    startSidebar();
    const header = document.querySelector("[data-nav-group]") as HTMLElement;
    const section = document.querySelector("[data-nav-section]") as HTMLElement;

    header.click();
    expect(section.getAttribute("data-collapsed")).toBe("true");
    expect(header.getAttribute("aria-expanded")).toBe("false");

    header.click();
    expect(section.getAttribute("data-collapsed")).toBe("false");
    expect(header.getAttribute("aria-expanded")).toBe("true");
  });

  it("binds nothing when the page carries no tree, such as the 404 page", () => {
    document.body.innerHTML = `<button data-sidebar-toggle aria-expanded="false"></button>`;

    expect(() => startSidebar()).not.toThrow();
  });
});
