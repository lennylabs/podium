// The link-treatment case set. Most anchors in this interface are structural:
// a subdomain card, a listing row, a crumb. The user-agent underline drawn
// under each one turns a page of navigation into a page of link text, and a
// card title drawn in the link tone reads as an inline link inside the card
// rather than as the card's name. The cases pin the declarations that decide
// it. jsdom performs no layout, so what a case asserts is the declaration that
// reaches the element; the rendered result is checked against a browser.

import { afterEach, describe, expect, it } from "vitest";

import "./index.css";

const mounted: HTMLElement[] = [];

afterEach(() => {
  while (mounted.length > 0) {
    mounted.pop()?.remove();
  }
});

/** anchorStyle attaches an anchor carrying the given classes, inside a
 * container carrying the given container classes, and returns the style the
 * stylesheet computes for it. */
function anchorStyle(className: string, containerClass = ""): CSSStyleDeclaration {
  const container = document.createElement("div");
  container.className = containerClass;
  const anchor = document.createElement("a");
  anchor.className = className;
  anchor.href = "#/";
  container.appendChild(anchor);
  document.body.appendChild(container);
  mounted.push(container);
  return window.getComputedStyle(anchor);
}

describe("link treatment", () => {
  it("drops the user-agent underline on an anchor by default", () => {
    expect(anchorStyle("").textDecoration).toBe("none");
  });

  // The card title is the card's name. The chevron at the card's right edge is
  // what states that the card opens the domain, so the title carries neither
  // the underline nor the link tone.
  it("draws the subdomain card title in the body ink without an underline", () => {
    const name = anchorStyle("subdomain-name mono");
    expect(name.textDecoration).toBe("none");
    expect(name.color).toBe("var(--ink)");
  });

  // The identifier keeps the link tone, which is what sets it apart from the
  // path and the badges beside it on the row, and gives up the underline that
  // would otherwise stand under every row of the listing.
  it("draws the artifact row identifier in the link tone without an underline", () => {
    const id = anchorStyle("mono artifact-id");
    expect(id.textDecoration).toBe("none");
    expect(id.color).toBe("var(--link)");
  });

  // Rendered markdown is running prose, where nothing else in the paragraph
  // marks where a link starts and ends, so the underline stays.
  it("keeps the underline on a link inside rendered markdown", () => {
    expect(anchorStyle("", "prose").textDecoration).toBe("underline");
  });

  // The sanitizer strips an anchor's destination when it points somewhere the
  // viewer must not follow, and the result draws as body text.
  it("draws a stripped markdown link as body text", () => {
    expect(anchorStyle("link-stripped", "prose").textDecoration).toBe("none");
  });

  // The sanitizer strips an image's source when it names a foreign host, and
  // the note left in its place draws in the secondary tone.
  it("draws the note left by a stripped markdown image as secondary text", () => {
    const container = document.createElement("div");
    container.className = "prose";
    const note = document.createElement("span");
    note.className = "image-stripped";
    container.appendChild(note);
    document.body.appendChild(container);
    mounted.push(container);
    expect(window.getComputedStyle(note).color).toBe("var(--meta)");
  });
});
