import type { ReactElement, ReactNode } from "react";

import { Header, withBase } from "../components/layout/Header";
import { Sidebar } from "../components/layout/Sidebar";
import { TableOfContents } from "../components/layout/TableOfContents";
import type { NavNode, PageModel, SiteConfig } from "../build/types";

type PageLink = { title: string; route: string };

/** The chain of nav nodes leading to a route, outermost section first. */
function navTrail(nodes: NavNode[], route: string): NavNode[] | null {
  for (const node of nodes) {
    if (node.route === route) {
      return [node];
    }
    const rest = navTrail(node.children, route);
    if (rest !== null) {
      return [node, ...rest];
    }
  }
  return null;
}

/** Prefills a new issue with the page title and the source file it came from. */
function issueUrlFor(config: SiteConfig, page: PageModel): string {
  const title = encodeURIComponent(`Docs: ${page.title}`);
  const body = encodeURIComponent(`Page: ${page.route}\nSource: ${page.sourcePath}\n\n`);
  return `${config.repoUrl}/issues/new?title=${title}&body=${body}`;
}

function Pager(props: {
  basePath: string;
  prev: PageLink | null;
  next: PageLink | null;
}): ReactElement {
  const { basePath, prev, next } = props;
  return (
    <nav className="d-pager" aria-label="Page navigation">
      {prev !== null && (
        <a className="d-pager-card d-pager-card--prev" href={withBase(basePath, prev.route)} rel="prev">
          <span className="d-pager-label">
            <span aria-hidden="true">←</span> Previous
          </span>
          <span className="d-pager-title">{prev.title}</span>
        </a>
      )}
      {next !== null && (
        <a className="d-pager-card d-pager-card--next" href={withBase(basePath, next.route)} rel="next">
          <span className="d-pager-label">
            Next <span aria-hidden="true">→</span>
          </span>
          <span className="d-pager-title">{next.title}</span>
        </a>
      )}
    </nav>
  );
}

/**
 * A documentation page: top bar, navigation tree, article column, and the
 * on-this-page rail.
 *
 * The article body arrives as children, already rendered from the markdown, and
 * sits inside the prose container. The headings on the page model feed the
 * right rail only.
 */
export function Doc(props: {
  config: SiteConfig;
  page: PageModel;
  nav: NavNode[];
  prev: PageLink | null;
  next: PageLink | null;
  children: ReactNode;
}): ReactElement {
  const { config, page, nav, prev, next, children } = props;
  const trail = page.sectionRoute === null ? null : navTrail(nav, page.sectionRoute);

  return (
    <>
      <a className="skip-link" href="#main">
        Skip to content
      </a>
      <Header config={config} activeRoute={page.route} />
      <div className="d-shell">
        <Sidebar nav={nav} activeRoute={page.route} basePath={config.basePath} />
        <main className="d-main" id="main">
          <article className="d-article" data-toc-root="">
            <nav className="d-crumbs" aria-label="Breadcrumb">
              <ol className="d-crumb-list">
                {trail?.map((node) => (
                  <li key={node.route} className="d-crumb">
                    <a className="d-crumb-link" href={withBase(config.basePath, node.route)}>
                      {node.title}
                    </a>
                  </li>
                ))}
                <li className="d-crumb">
                  <span className="d-crumb-current" aria-current="page">
                    {page.title}
                  </span>
                </li>
              </ol>
            </nav>

            <h1 className="d-title">{page.displayTitle}</h1>
            {page.description !== "" && <p className="d-lede">{page.description}</p>}

            <div className="prose">{children}</div>

            {(prev !== null || next !== null) && (
              <Pager basePath={config.basePath} prev={prev} next={next} />
            )}
          </article>
        </main>
        <TableOfContents
          headings={page.headings}
          editUrl={page.editUrl}
          issueUrl={issueUrlFor(config, page)}
        />
      </div>
    </>
  );
}
