import type { ReactElement } from "react";

import type { NavNode } from "../../build/types";
import { withBase } from "./Header";

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
    <nav id="d-sidebar" className="d-sidebar" aria-label="Documentation">
      <div className="d-sidebar-inner">
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
