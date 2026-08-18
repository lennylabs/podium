import type { ReactElement } from "react";

import type { NavNode } from "../../build/types";
import { CHANGELOG_ROUTE, SearchGlyph, TABS, withBase } from "./Header";

/**
 * The controls the top bar surrenders at mobile widths.
 *
 * The bar keeps the menu button, the brand, the version, and the search icon,
 * which is all that fits across 390pt. The search field, the section tabs, and
 * the theme toggle move in here, where the drawer has room for them. The whole
 * block is hidden above the drawer breakpoint, so a wide viewport still reads
 * them from the bar alone and neither copy is duplicated on screen.
 */
function DrawerHead(props: { activeRoute: string; basePath: string }): ReactElement {
  const { activeRoute, basePath } = props;

  return (
    <div className="d-drawer-head">
      <button type="button" className="d-drawer-search" data-search-input="">
        <SearchGlyph />
        <span>Search artifacts, pages, CLI flags</span>
      </button>

      <div className="d-drawer-tabs">
        {TABS.map((tab) => {
          const current =
            tab.route === CHANGELOG_ROUTE
              ? activeRoute === CHANGELOG_ROUTE
              : activeRoute !== "" && activeRoute !== CHANGELOG_ROUTE;
          return (
            <a
              key={tab.route}
              className={current ? "d-drawer-tab is-active" : "d-drawer-tab"}
              href={withBase(basePath, tab.route)}
              aria-current={current ? "page" : undefined}
            >
              {tab.label}
            </a>
          );
        })}

        <button type="button" className="d-drawer-theme" data-theme-toggle="">
          <span className="d-theme-dot" aria-hidden="true" />
          <span className="sr-only">Switch theme to </span>
          <span className="d-theme-label d-theme-label--dark">Dark</span>
          <span className="d-theme-label d-theme-label--light">Light</span>
        </button>
      </div>
    </div>
  );
}

/** Turns a route into a DOM id fragment: "/authoring/index.html" -> "authoring-index-html". */
function routeId(route: string): string {
  const slug = route.replace(/[^a-z0-9]+/gi, "-").replace(/^-+|-+$/g, "");
  return slug === "" ? "root" : slug.toLowerCase();
}

function NavItem(props: {
  node: NavNode;
  activeRoute: string;
  basePath: string;
}): ReactElement {
  const { node, activeRoute, basePath } = props;
  const current = node.route === activeRoute;

  return (
    <li className="d-nav-item">
      <a
        className={current ? "d-nav-link is-active" : "d-nav-link"}
        href={withBase(basePath, node.route)}
        aria-current={current ? "page" : undefined}
      >
        {node.title}
      </a>
      {node.children.length > 0 && (
        <ul className="d-nav-sublist">
          {node.children.map((child) => (
            <NavItem
              key={child.route}
              node={child}
              activeRoute={activeRoute}
              basePath={basePath}
            />
          ))}
        </ul>
      )}
    </li>
  );
}

function NavGroup(props: {
  group: NavNode;
  activeRoute: string;
  basePath: string;
}): ReactElement {
  const { group, activeRoute, basePath } = props;
  const listId = `d-nav-${routeId(group.route)}`;
  const current = group.route === activeRoute;

  return (
    <div className="d-nav-group">
      <div className="d-nav-group-head">
        <a
          className={current ? "d-nav-group-label is-active" : "d-nav-group-label"}
          href={withBase(basePath, group.route)}
          aria-current={current ? "page" : undefined}
        >
          {group.title}
        </a>
        <button
          type="button"
          className="d-nav-group-toggle"
          data-nav-group={group.route}
          aria-controls={listId}
          aria-expanded="true"
        >
          <svg width="10" height="6" viewBox="0 0 10 6" aria-hidden="true" focusable="false">
            <path
              d="M1 1.5 L5 5 L9 1.5"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.6"
              strokeLinecap="round"
              strokeLinejoin="round"
            />
          </svg>
          <span className="sr-only">Toggle the {group.title} section</span>
        </button>
      </div>
      <ul className="d-nav-list" id={listId}>
        {group.children.map((child) => (
          <NavItem
            key={child.route}
            node={child}
            activeRoute={activeRoute}
            basePath={basePath}
          />
        ))}
      </ul>
    </div>
  );
}

/**
 * The 252px navigation tree. Top-level nav nodes render as groups; their
 * descendants render as an indented list behind a hairline.
 *
 * Groups ship expanded so the tree is complete without JavaScript. The toggle
 * button carries the data-nav-group hook and the aria-expanded state the client
 * script updates when a group collapses. Below the small-screen breakpoint the
 * tree stacks above the article, and the skip link jumps a reader past it.
 */
export function Sidebar(props: {
  nav: NavNode[];
  activeRoute: string;
  basePath: string;
}): ReactElement {
  const { nav, activeRoute, basePath } = props;

  return (
    <nav
      id="d-sidebar"
      className="d-sidebar"
      aria-label="Documentation"
      data-sidebar=""
      data-open="false"
    >
      <div className="d-sidebar-inner">
        <DrawerHead activeRoute={activeRoute} basePath={basePath} />
        {nav.map((group) => (
          <NavGroup
            key={group.route}
            group={group}
            activeRoute={activeRoute}
            basePath={basePath}
          />
        ))}
      </div>
    </nav>
  );
}
