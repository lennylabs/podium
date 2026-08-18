import { existsSync } from "node:fs";
import { posix, resolve } from "node:path";

import type { Element, Root as HastRoot } from "hast";
import rehypeAutolinkHeadings from "rehype-autolink-headings";
import rehypeSlug from "rehype-slug";
import remarkDirective from "remark-directive";
import remarkGfm from "remark-gfm";
import remarkParse from "remark-parse";
import remarkRehype from "remark-rehype";
import { unified } from "unified";
import { visit } from "unist-util-visit";

import { diagramVariants } from "../../components/diagrams";
import type { IslandRegistry } from "../../components/islands/props";
import type { DiscoveredPage } from "../discover";
import { discover, lastModified } from "../discover";
import type {
  ActionLink,
  BuildDiagnostic,
  IslandRef,
  PageModel,
  SiteConfig,
  StaticFile,
} from "../types";
import { remarkAlerts } from "./alerts";
import { remarkDirectives } from "./directives";
import { checkHeadingOrder, checkImageAlt, collectHeadings, collectText } from "./headings";
import { highlightTree, remarkCodeMeta } from "./highlight";
import { isExternal, resolveLink, type RouteIndex } from "./links";

export type BuiltCorpus = {
  pages: PageModel[];
  statics: StaticFile[];
  index: RouteIndex;
  diagnostics: BuildDiagnostic[];
};

type Staged = {
  page: DiscoveredPage;
  tree: HastRoot;
  islands: IslandRef[];
  displayTitle: string;
};

/**
 * Reads the corpus and produces one model per page.
 *
 * Three passes, because each needs the whole corpus before the next can run:
 *
 *   1. discovery assigns every route, so a link has something to resolve against
 *   2. parsing produces each tree and its heading ids, so an anchor can be checked
 *   3. finishing rewrites links and highlights code against the complete index
 */
export async function buildCorpus(
  config: SiteConfig,
  registry: IslandRegistry,
): Promise<BuiltCorpus> {
  const { pages: discovered, statics, diagnostics } = discover(config);

  const routeBySource = new Map<string, string>();
  const routes = new Set<string>();
  for (const page of discovered) {
    routeBySource.set(page.source, page.route);
    routes.add(page.route);
  }
  for (const file of statics) routes.add(file.route);
  // Emitted by the build rather than discovered in the corpus, but still
  // published, so a link may point at either.
  routes.add("/index.html");
  routes.add("/404.html");

  const index: RouteIndex = {
    routes,
    anchors: new Map(),
    routeBySource,
    hidden: new Set(
      discovered.filter((page) => page.frontmatter.hidden).map((page) => page.route),
    ),
  };

  const variants = new Set(Object.keys(diagramVariants));
  const staged: Staged[] = [];

  for (const page of discovered) {
    const islands: IslandRef[] = [];
    const tree = parseBody(page, config, registry, index, variants, islands, diagnostics);
    // The opening heading is removed before the anchor set is collected. Doing
    // it the other way round leaves its slug in the set, so a link to that
    // fragment passes the check and then finds nothing on the page.
    const displayTitle = dropLeadingTitle(tree) ?? page.frontmatter.title;
    index.anchors.set(
      page.route,
      new Set(collectHeadings(tree).map((heading) => heading.id)),
    );
    staged.push({ page, tree, islands, displayTitle });
  }

  const dates = lastModified(config);
  const models: PageModel[] = [];

  for (const entry of staged) {
    const offset = entry.page.bodyStartLine - 1;
    rewriteLinks(entry.tree, entry.page, index, config, diagnostics);
    await highlightTree(entry.tree, entry.page.file, offset, diagnostics);

    const headings = collectHeadings(entry.tree);
    checkHeadingOrder(headings, entry.page.file, diagnostics);
    checkImageAlt(entry.tree, entry.page.file, offset, diagnostics);

    models.push({
      route: entry.page.route,
      sourcePath: entry.page.file,
      title: entry.page.frontmatter.title,
      displayTitle: entry.displayTitle,
      navTitle: entry.page.frontmatter.navTitle,
      description: entry.page.frontmatter.description,
      navOrder: entry.page.frontmatter.navOrder,
      hidden: entry.page.frontmatter.hidden,
      sectionRoute: sectionRouteFor(entry.page.source, routeBySource),
      headings,
      actions: resolveActions(entry.page, index, config, diagnostics),
      body: entry.tree,
      islands: entry.islands,
      editUrl: `${config.editBase}/${entry.page.file}`,
      updatedAt: dates.get(entry.page.file) ?? "",
      text: collectText(entry.tree),
    });
  }

  return { pages: models, statics, index, diagnostics };
}

/** Runs the markdown transform for one page, up to but not including links. */
function parseBody(
  page: DiscoveredPage,
  config: SiteConfig,
  registry: IslandRegistry,
  index: RouteIndex,
  diagramStems: ReadonlySet<string>,
  islands: IslandRef[],
  diagnostics: BuildDiagnostic[],
): HastRoot {
  const processor = unified()
    .use(remarkParse)
    .use(remarkGfm)
    .use(remarkDirective)
    .use(remarkAlerts)
    .use(remarkDirectives, {
      registry,
      file: page.file,
      diagnostics,
      islands,
      diagramVariants: diagramStems,
      source: page.body,
      lineOffset: page.bodyStartLine - 1,
      resolveRoute: (value) =>
        resolveLink(
          value,
          index,
          {
            fromSource: page.source,
            fromRoute: page.route,
            file: page.file,
            line: null,
            column: null,
          },
          config.basePath,
          [],
        ),
      resolveAsset: (spec, value) => {
        const relativePath =
          spec.within === undefined
            ? posix.join(posix.dirname(page.source), value)
            : posix.join(spec.within, `${value}${spec.extension ?? ""}`);
        const onDisk = resolve(config.docsDir, relativePath);
        return existsSync(onDisk) ? `${config.basePath}/${relativePath}` : null;
      },
    })
    .use(remarkCodeMeta)
    .use(remarkRehype, { allowDangerousHtml: false })
    .use(rehypeSlug)
    .use(rehypeAutolinkHeadings, {
      behavior: "append",
      properties: { className: ["heading-anchor"], ariaHidden: "true", tabIndex: -1 },
    });

  const mdast = processor.parse(page.body);
  return processor.runSync(mdast, page.body) as HastRoot;
}

/**
 * Removes the body's opening h1 and returns its text.
 *
 * Pages carry a top-level heading because that is what makes them read
 * correctly on github.com, where there is no template to supply one. The
 * template renders the heading itself, so keeping both would show the title
 * twice and put two h1 elements in the document.
 *
 * The heading's text becomes the page's display title, which lets a page use a
 * short navigation label and a fuller heading: "Consuming" in the tree, and
 * "Consuming artifacts" at the top of the page.
 */
function dropLeadingTitle(tree: HastRoot): string | null {
  const first = tree.children.find(
    (node): node is Element => node.type === "element",
  );
  if (first === undefined || first.tagName !== "h1") return null;

  const text = headingText(first).trim();
  tree.children.splice(tree.children.indexOf(first), 1);
  return text === "" ? null : text;
}

/** Heading text without the anchor link rehype-autolink-headings appends. */
function headingText(node: Element): string {
  let out = "";
  visit(node, "text", (child, _index, parent) => {
    const owner = parent as Element | undefined;
    if (owner?.tagName === "a") return;
    out += child.value;
  });
  return out;
}

/**
 * Rewrites every internal href and src to its published URL. Positions are
 * offset by the frontmatter block so a diagnostic points at the source line.
 */
function rewriteLinks(
  tree: HastRoot,
  page: DiscoveredPage,
  index: RouteIndex,
  config: SiteConfig,
  diagnostics: BuildDiagnostic[],
): void {
  const offset = page.bodyStartLine - 1;

  visit(tree, "element", (node: Element) => {
    const attribute = node.tagName === "a" ? "href" : node.tagName === "img" ? "src" : null;
    if (attribute === null) return;

    const raw = node.properties?.[attribute];
    if (typeof raw !== "string" || raw === "") return;
    if (isExternal(raw)) return;
    // Already absolute against the published site, which the island transform
    // produces for a resolved asset prop.
    if (config.basePath !== "" && raw.startsWith(`${config.basePath}/`)) return;

    const line = node.position?.start.line;
    const resolved = resolveLink(
      raw,
      index,
      {
        fromSource: page.source,
        fromRoute: page.route,
        file: page.file,
        line: line === undefined ? null : line + offset,
        column: node.position?.start.column ?? null,
      },
      config.basePath,
      diagnostics,
    );
    if (resolved === null) return;
    node.properties = { ...node.properties, [attribute]: resolved };
  });
}

/** Resolves the frontmatter action links, marking the first as primary. */
function resolveActions(
  page: DiscoveredPage,
  index: RouteIndex,
  config: SiteConfig,
  diagnostics: BuildDiagnostic[],
): ActionLink[] {
  return page.frontmatter.actions.flatMap((action, position) => {
    const external = isExternal(action.href);
    const href = external
      ? action.href
      : resolveLink(
          action.href,
          index,
          {
            fromSource: page.source,
            fromRoute: page.route,
            file: page.file,
            line: 1,
            column: 1,
          },
          config.basePath,
          diagnostics,
        );
    if (href === null) return [];
    return [
      {
        label: action.label,
        href,
        variant: position === 0 ? ("primary" as const) : ("outline" as const),
        external,
      },
    ];
  });
}

/**
 * The section a page belongs to is its directory's index page. A page at the
 * top level, and a section index itself, belong to no section.
 */
function sectionRouteFor(
  source: string,
  routeBySource: Map<string, string>,
): string | null {
  const dir = posix.dirname(source);
  if (dir === "." || source === `${dir}/index.md`) return null;
  return routeBySource.get(`${dir}/index.md`) ?? null;
}
