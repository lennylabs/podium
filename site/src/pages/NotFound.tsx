import type { ReactElement } from "react";

import { Footer } from "../components/layout/Footer";
import { Header, withBase } from "../components/layout/Header";
import type { NavNode, SiteConfig } from "../build/types";

/**
 * The 404 page. It keeps the top bar and the footer, states what happened, and
 * lists the documentation sections so a reader can restart from a known page.
 */
export function NotFound(props: { config: SiteConfig; nav: NavNode[] }): ReactElement {
  const { config, nav } = props;

  return (
    <>
      <a className="skip-link" href="#main">
        Skip to content
      </a>
      <Header config={config} nav={nav} activeRoute="" />
      <main className="d-notfound" id="main">
        <p className="d-notfound-code">404</p>
        <h1 className="d-notfound-title">Page not found</h1>
        <p className="d-notfound-text">
          The page you asked for is not part of this documentation site. It may have moved, or the
          link that brought you here may be out of date. Start from a section below, or search from
          the bar above.
        </p>
        <ul className="d-notfound-list">
          <li className="d-notfound-list-item">
            <a className="d-notfound-link" href={withBase(config.basePath, "/")}>
              Documentation home
            </a>
          </li>
          {nav.map((section) => (
            <li key={section.route} className="d-notfound-list-item">
              <a className="d-notfound-link" href={withBase(config.basePath, section.route)}>
                {section.title}
              </a>
            </li>
          ))}
        </ul>
      </main>
      <Footer config={config} />
    </>
  );
}
