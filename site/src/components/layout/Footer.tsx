import type { ReactElement } from "react";

import type { SiteConfig } from "../../build/types";
import { withBase } from "./Header";

/** Repository slug shown beside the licence note, derived from the repo URL. */
function repoSlug(repoUrl: string): string {
  const parts = repoUrl.replace(/\/+$/, "").split("/");
  const name = parts.pop() ?? "";
  const owner = parts.pop() ?? "";
  return owner === "" ? name : `${owner}/${name}`;
}

/**
 * The site footer: the licence note and repository link at the left, the
 * project pages at the right.
 */
export function Footer(props: { config: SiteConfig }): ReactElement {
  const { config } = props;
  const links: Array<{ label: string; href: string }> = [
    { label: "Docs", href: withBase(config.basePath, "/") },
    { label: "Governance", href: withBase(config.basePath, "/about/governance.html") },
    { label: "Contributing", href: withBase(config.basePath, "/about/contributing.html") },
    { label: "Changelog", href: withBase(config.basePath, "/about/changelog.html") },
  ];

  return (
    <footer className="d-footer">
      <div className="d-footer-inner">
        <p className="d-footer-note">
          MIT licensed <span aria-hidden="true">·</span>{" "}
          <a className="d-footer-link" href={config.repoUrl}>
            {repoSlug(config.repoUrl)}
          </a>
        </p>
        <nav className="d-footer-nav" aria-label="Project">
          {links.map((link) => (
            <a key={link.href} className="d-footer-link" href={link.href}>
              {link.label}
            </a>
          ))}
        </nav>
      </div>
    </footer>
  );
}
