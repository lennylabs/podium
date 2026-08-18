import type { Root as HastRoot } from "hast";
import remarkDirective from "remark-directive";
import remarkGfm from "remark-gfm";
import remarkParse from "remark-parse";
import remarkRehype from "remark-rehype";
import { unified } from "unified";

import { remarkDirectives } from "../../src/build/content/directives";
import type {
  AssetResolver,
  IslandRegistry,
  RouteResolver,
} from "../../src/components/islands/props";
import { registry as siteRegistry } from "../../src/components/islands/registry";
import type { BuildDiagnostic, IslandRef } from "../../src/build/types";

export type DirectiveRunOptions = {
  registry?: IslandRegistry;
  file?: string;
  /** Lines the frontmatter block occupies, mirroring what the pipeline passes. */
  lineOffset?: number;
  diagramVariants?: ReadonlySet<string>;
  resolveRoute?: RouteResolver;
  resolveAsset?: AssetResolver;
};

export type DirectiveRun = {
  tree: HastRoot;
  diagnostics: BuildDiagnostic[];
  islands: IslandRef[];
};

/**
 * Runs the directive transform over a markdown body with the same plugin order
 * the page pipeline uses, so a diagnostic carries the position it would carry
 * during a build.
 */
export function runDirectives(
  source: string,
  options: DirectiveRunOptions = {},
): DirectiveRun {
  const diagnostics: BuildDiagnostic[] = [];
  const islands: IslandRef[] = [];

  const processor = unified()
    .use(remarkParse)
    .use(remarkGfm)
    .use(remarkDirective)
    .use(remarkDirectives, {
      registry: options.registry ?? siteRegistry,
      file: options.file ?? "docs/page.md",
      diagnostics,
      islands,
      diagramVariants: options.diagramVariants ?? new Set<string>(),
      source,
      lineOffset: options.lineOffset ?? 0,
      resolveRoute: options.resolveRoute ?? (() => null),
      resolveAsset: options.resolveAsset ?? (() => null),
    })
    .use(remarkRehype, { allowDangerousHtml: false });

  const tree = processor.runSync(processor.parse(source), source) as HastRoot;
  return { tree, diagnostics, islands };
}

/** Concatenates every text node in a hast tree, which is what a reader sees. */
export function textContent(node: unknown): string {
  const current = node as {
    type?: string;
    value?: string;
    children?: unknown[];
  };
  if (current.type === "text") return String(current.value ?? "");
  return (current.children ?? []).map(textContent).join("");
}
