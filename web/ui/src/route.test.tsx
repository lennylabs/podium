// The hash-routing case set. The address bar is the only record of where the
// reader is, so a hash the router cannot read must not leave the address
// saying one thing while the page draws another: the reader copies the link,
// bookmarks it, or steps back, and each of those carries the broken route on.
//
// Spec: §13.10

import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import {
  artifactHref,
  domainHref,
  layersHref,
  parseRoute,
  routeKey,
  replaceRoute,
  searchHref,
  useRoute,
  useTopOfNewRoute,
} from "./route";

afterEach(() => {
  cleanup();
  window.location.hash = "";
});

/** Probe renders the key of the route `useRoute` reports, which is what a case
 * reads to learn which surface the shell would draw. */
function Probe(): React.ReactElement {
  return <div data-testid="route">{routeKey(useRoute())}</div>;
}

function drawnRoute(): string {
  return screen.getByTestId("route").textContent ?? "";
}

describe("parseRoute", () => {
  it("reads the registry root from the catalog address", () => {
    expect(parseRoute("#/")).toEqual({ name: "domain", path: "" });
    expect(parseRoute("")).toEqual({ name: "domain", path: "" });
  });

  it("reads each addressable surface", () => {
    expect(parseRoute(domainHref("finance/ap"))).toEqual({
      name: "domain",
      path: "finance/ap",
    });
    expect(parseRoute(artifactHref("finance/ap/pay"))).toEqual({
      name: "artifact",
      id: "finance/ap/pay",
    });
    expect(parseRoute(searchHref("deploy"))).toEqual({
      name: "search",
      query: "deploy",
    });
    expect(parseRoute(layersHref)).toEqual({ name: "layers", deleted: false });
  });

  it("answers null for a hash that names no surface", () => {
    expect(parseRoute("#/totally-unknown-route")).toBeNull();
    expect(parseRoute("#/layer")).toBeNull();
    expect(parseRoute("#//finance")).toBeNull();
  });
});

describe("useRoute", () => {
  it("corrects an unrecognized hash the reader arrives on", () => {
    window.location.hash = "#/totally-unknown-route";
    render(<Probe />);
    expect(drawnRoute()).toBe("domain/");
    expect(window.location.hash).toBe(domainHref(""));
  });

  it("corrects an unrecognized hash the reader navigates to", () => {
    window.location.hash = artifactHref("finance/ap/pay");
    render(<Probe />);
    expect(drawnRoute()).toBe("artifact/finance/ap/pay");

    act(() => {
      window.location.hash = "#/nope";
      window.dispatchEvent(new HashChangeEvent("hashchange"));
    });
    expect(drawnRoute()).toBe("domain/");
    expect(window.location.hash).toBe(domainHref(""));
  });

  it("leaves a recognized hash alone", () => {
    window.location.hash = searchHref("deploy");
    render(<Probe />);
    expect(drawnRoute()).toBe("search/deploy");
    expect(window.location.hash).toBe(searchHref("deploy"));
  });
});

/** Reader draws the route the way `Probe` does and installs the scroll reset
 * beside it, which is how the shell holds the two. */
function Reader(): React.ReactElement {
  useTopOfNewRoute();
  return <Probe />;
}

/** navigate is a link followed: the browser pushes an entry the shell has
 * never been on and fires the hash change. */
function navigate(href: string): void {
  act(() => {
    window.history.pushState(null, "", href);
    window.dispatchEvent(new HashChangeEvent("hashchange"));
  });
}

/** stepBack is a history step: the entry the window lands on is one the shell
 * has already drawn a surface on, so it still carries its state. */
function stepBack(href: string, state: unknown): void {
  act(() => {
    window.history.replaceState(state, "", href);
    window.dispatchEvent(new HashChangeEvent("hashchange"));
  });
}

// A surface entered from a link is drawn into the document the reader was
// already scrolled inside, so an artifact opened from the relations rail
// would otherwise arrive with its title, breadcrumb, and property table above
// the viewport. Chrome fires `popstate` for a fragment push as well as for a
// history traversal, so the two are told apart by the mark the shell leaves
// on an entry it has been on.
//
// Spec: §13.10
describe("useTopOfNewRoute", () => {
  /** watchScroll replaces `window.scrollTo`, which jsdom does not implement,
   * with a recorder the cases read. */
  function watchScroll(): ReturnType<typeof vi.fn> {
    const scrollTo = vi.fn();
    Object.defineProperty(window, "scrollTo", {
      configurable: true,
      writable: true,
      value: scrollTo,
    });
    return scrollTo;
  }

  it("puts the reader at the top of a surface reached by a link", () => {
    window.location.hash = artifactHref("finance/ap/pay-invoice");
    const scrollTo = watchScroll();
    render(<Reader />);

    navigate(artifactHref("finance/ap/pay-invoice-eu"));
    expect(drawnRoute()).toBe("artifact/finance/ap/pay-invoice-eu");
    expect(scrollTo).toHaveBeenCalledWith(0, 0);
  });

  // The browser restores the offset a history entry was left at, and moving
  // the reader to the top of a page they stepped back to loses the place they
  // came from.
  it("leaves a history step where the browser puts it", () => {
    window.location.hash = artifactHref("finance/ap/pay-invoice");
    const scrollTo = watchScroll();
    render(<Reader />);
    const entered = window.history.state;

    navigate(artifactHref("finance/ap/pay-invoice-eu"));
    scrollTo.mockClear();

    stepBack(artifactHref("finance/ap/pay-invoice"), entered);
    expect(drawnRoute()).toBe("artifact/finance/ap/pay-invoice");
    expect(scrollTo).not.toHaveBeenCalled();
  });

  // A link followed after a history step is a new entry again, so the step
  // must not leave the reset switched off behind it.
  it("puts the reader at the top of a link followed after a history step", () => {
    window.location.hash = artifactHref("finance/ap/pay-invoice");
    const scrollTo = watchScroll();
    render(<Reader />);
    const entered = window.history.state;

    navigate(artifactHref("finance/ap/pay-invoice-eu"));
    stepBack(artifactHref("finance/ap/pay-invoice"), entered);
    scrollTo.mockClear();

    navigate(searchHref("deploy"));
    expect(scrollTo).toHaveBeenCalledTimes(1);
    expect(scrollTo).toHaveBeenCalledWith(0, 0);
  });

  // The search surface rewrites its own entry as the reader types, and an
  // entry that lost its mark would read as one the shell has never been on.
  it("keeps the entry's mark across a route the surface rewrote", () => {
    window.location.hash = searchHref("dep");
    const scrollTo = watchScroll();
    render(<Reader />);

    act(() => {
      replaceRoute(searchHref("deploy"));
    });
    // What the entry holds after the rewrite is what a step back to it
    // restores.
    stepBack(searchHref("deploy"), window.history.state);
    expect(scrollTo).not.toHaveBeenCalled();
  });
});
