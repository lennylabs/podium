import type { ReactElement } from "react";

import { Lockup } from "./Lockup";

import type { NavNode, SiteConfig } from "../../build/types";

/**
 * Joins the site base path with a page route. Absolute URLs and fragments pass
 * through untouched, so the same helper serves internal and external links.
 */
export function withBase(basePath: string, route: string): string {
  if (route === "" || route.startsWith("#") || /^[a-z][a-z0-9+.-]*:/i.test(route)) {
    return route;
  }
  const suffix = route.startsWith("/") ? route : `/${route}`;
  return `${basePath}${suffix}`;
}

/** Directory prefix of a section route, used to match descendant pages. */
function sectionPrefix(route: string): string {
  return route.replace(/index\.html$/, "");
}

function isCurrentSection(route: string, activeRoute: string): boolean {
  if (activeRoute === "") {
    return false;
  }
  if (route === activeRoute) {
    return true;
  }
  const prefix = sectionPrefix(route);
  return prefix.endsWith("/") && activeRoute.startsWith(prefix);
}


function SearchGlyph(): ReactElement {
  return (
    <svg
      className="d-search-glyph"
      width="12"
      height="12"
      viewBox="0 0 12 12"
      aria-hidden="true"
      focusable="false"
    >
      <circle cx="5" cy="5" r="4" fill="none" stroke="currentColor" strokeWidth="1.4" />
      <rect
        x="7.6"
        y="8.4"
        width="4"
        height="1.4"
        rx="0.7"
        transform="rotate(45 7.6 8.4)"
        fill="currentColor"
      />
    </svg>
  );
}

/**
 * The documentation top bar: wordmark, section tabs, search entry point,
 * version pill, and the theme toggle.
 *
 * Every control is static markup carrying a data hook. The client script wires
 * the search overlay, the theme switch, and the small-screen sidebar drawer.
 * The theme toggle names the theme it switches to, and both labels ship in the
 * markup so the correct one shows without JavaScript.
 */
export function Header(props: {
  config: SiteConfig;
  nav: NavNode[];
  activeRoute: string;
}): ReactElement {
  const { config, nav, activeRoute } = props;

  return (
    <header className="d-topbar">
      <div className="d-topbar-inner">
        <button
          type="button"
          className="d-menu"
          data-sidebar-toggle=""
          aria-controls="d-sidebar"
          aria-expanded="true"
        >
          <svg width="16" height="12" viewBox="0 0 16 12" aria-hidden="true" focusable="false">
            <rect x="0" y="0" width="16" height="2" rx="1" />
            <rect x="0" y="5" width="16" height="2" rx="1" />
            <rect x="0" y="10" width="16" height="2" rx="1" />
          </svg>
          <span className="sr-only">Menu</span>
        </button>

        <a className="d-brand" href={withBase(config.basePath, "/")}>
          <Lockup className="d-lockup" />
        </a>

        <nav className="d-tabs" aria-label="Documentation sections">
          {nav.map((section) => {
            const current = isCurrentSection(section.route, activeRoute);
            return (
              <a
                key={section.route}
                className={current ? "d-tab is-active" : "d-tab"}
                href={withBase(config.basePath, section.route)}
                aria-current={current ? "page" : undefined}
              >
                {section.title}
              </a>
            );
          })}
        </nav>

        <div className="d-topbar-spacer" />

        <label className="d-search">
          <SearchGlyph />
          <span className="sr-only">Search the documentation</span>
          <input
            type="search"
            className="d-search-input"
            data-search-input=""
            placeholder="Search artifacts, pages, CLI flags"
          />
          <kbd className="d-kbd">⌘K</kbd>
        </label>

        <span className="d-version">v{config.version}</span>

        <button type="button" className="d-theme" data-theme-toggle="">
          <span className="d-theme-dot" aria-hidden="true" />
          <span className="sr-only">Switch theme to </span>
          <span className="d-theme-label d-theme-label--dark">Dark</span>
          <span className="d-theme-label d-theme-label--light">Light</span>
        </button>
      </div>
    </header>
  );
}
