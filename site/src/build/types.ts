import type { Root as HastRoot } from "hast";

/** A scalar bag. Island props are always scalars; children travel as markup. */
export type IslandProps = Record<string, string | number | boolean>;

export type IslandRef = {
  /** Stable per-page identifier, used to pair the placeholder with its mount. */
  id: string;
  /** Registry key, which is also the directive name. */
  component: string;
  props: IslandProps;
  /** How the client attaches: over matching markup, or by replacing a fallback. */
  mount: "hydrate" | "replace";
};

export type Heading = {
  depth: number;
  id: string;
  text: string;
};

export type ActionLink = {
  label: string;
  href: string;
  variant: "primary" | "outline";
  external: boolean;
};

export type PageModel = {
  /** Published path, e.g. "/getting-started/quickstart.html". */
  route: string;
  /** Repository-relative source, e.g. "docs/getting-started/quickstart.md". */
  sourcePath: string;
  title: string;
  /** Heading shown on the page, taken from the body's opening h1 when present. */
  displayTitle: string;
  navTitle: string | null;
  description: string;
  navOrder: number | null;
  hidden: boolean;
  /** Route of the section index this page belongs to, null at the top level. */
  sectionRoute: string | null;
  headings: Heading[];
  actions: ActionLink[];
  body: HastRoot;
  islands: IslandRef[];
  editUrl: string;
  /** ISO date of the last commit touching the source file, empty when unknown. */
  updatedAt: string;
  /** Plain prose, retained for the search index. */
  text: string;
};

/** A file published verbatim: assets, and markdown carrying no frontmatter. */
export type StaticFile = {
  route: string;
  sourcePath: string;
};

export type NavNode = {
  title: string;
  route: string;
  navOrder: number | null;
  children: NavNode[];
};

export type SiteConfig = {
  /** Absolute origin, no trailing slash. */
  siteUrl: string;
  /** Path prefix every emitted URL carries, "" or "/podium". */
  basePath: string;
  /** Repository root, absolute. */
  repoRoot: string;
  /** Markdown source directory, absolute. */
  docsDir: string;
  /** Build output directory, absolute. */
  outDir: string;
  /** Base for "edit this page" links, no trailing slash. */
  editBase: string;
  repoUrl: string;
  /** Version string shown in the version pill. */
  version: string;
  /** Failure threshold for the serialized search index, in bytes. */
  searchIndexLimitBytes: number;
};

export type BuildDiagnostic = {
  file: string;
  line: number | null;
  column: number | null;
  message: string;
};

/** Thrown for any condition that must stop a build or a check run. */
export class BuildError extends Error {
  readonly diagnostics: BuildDiagnostic[];

  constructor(diagnostics: BuildDiagnostic[]) {
    const body = diagnostics
      .map((d) => {
        const where =
          d.line === null ? d.file : `${d.file}:${d.line}:${d.column ?? 1}`;
        return `  ${where}  ${d.message}`;
      })
      .join("\n");
    super(`${diagnostics.length} problem(s):\n${body}`);
    this.name = "BuildError";
    this.diagnostics = diagnostics;
  }
}
