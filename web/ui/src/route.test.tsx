// The hash-routing case set. The address bar is the only record of where the
// reader is, so a hash the router cannot read must not leave the address
// saying one thing while the page draws another: the reader copies the link,
// bookmarks it, or steps back, and each of those carries the broken route on.
//
// Spec: §13.10

import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import {
  artifactHref,
  domainHref,
  layersHref,
  parseRoute,
  routeKey,
  searchHref,
  useRoute,
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
