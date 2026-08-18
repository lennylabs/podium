import type { Root, RootContent } from "mdast";
import type { Plugin } from "unified";
import { visit } from "unist-util-visit";

import {
  coerceProps,
  type CoerceContext,
  type IslandRegistry,
} from "../../components/islands/props";
import type { BuildDiagnostic, IslandProps, IslandRef } from "../types";

type DirectiveNode = RootContent & {
  type: "containerDirective" | "leafDirective" | "textDirective";
  name: string;
  attributes?: Record<string, string | null | undefined> | null | undefined;
  children: RootContent[];
};

export type DirectiveOptions = {
  registry: IslandRegistry;
  file: string;
  diagnostics: BuildDiagnostic[];
  /** Collected in source order; the renderer pairs each with its placeholder. */
  islands: IslandRef[];
  resolveRoute: CoerceContext["resolveRoute"];
  resolveAsset: CoerceContext["resolveAsset"];
  /** Diagram stems that have an animated variant; the rest stay static. */
  diagramVariants: ReadonlySet<string>;
  /** The markdown body, used to restore text that only looks like a directive. */
  source: string;
  /** Lines occupied by frontmatter, so a diagnostic names the file's own line. */
  lineOffset: number;
};

function isDirective(node: { type: string }): node is DirectiveNode {
  return (
    node.type === "containerDirective" ||
    node.type === "leafDirective" ||
    node.type === "textDirective"
  );
}

function positionOf(node: RootContent): {
  line: number | null;
  column: number | null;
} {
  const start = node.position?.start;
  return { line: start?.line ?? null, column: start?.column ?? null };
}

/**
 * Puts back the exact source text a node consumed.
 *
 * Slicing the original markdown rather than reassembling the syntax keeps
 * whatever the author actually typed, including spacing the parser normalised.
 */
function restoreLiteral(
  node: RootContent,
  parent: { children: RootContent[] } | undefined,
  index: number | undefined,
  source: string,
): void {
  if (parent === undefined || index === undefined) return;
  const start = node.position?.start.offset;
  const end = node.position?.end.offset;
  if (start === undefined || end === undefined) return;
  parent.children[index] = {
    type: "text",
    value: source.slice(start, end),
    position: node.position,
  };
}

/**
 * Names that only ever appear inside a parent container, derived from the
 * registry rather than hardcoded, so `tab` is legal under `tabs` and nowhere
 * else.
 */
export function childOnlyDirectives(registry: IslandRegistry): Set<string> {
  const names = new Set<string>();
  for (const entry of Object.values(registry)) {
    if (entry.children.kind === "directives") names.add(entry.children.name);
  }
  return names;
}

/**
 * Validates every directive against the registry and rewrites it into an island
 * placeholder.
 *
 * remark-rehype carries no handler for directive nodes and an unhandled
 * directive emits nothing at all, so a directive that is not claimed here
 * disappears from the page without an error. Every path through this transform
 * therefore either sets data.hName or records a diagnostic.
 */
export const remarkDirectives: Plugin<[DirectiveOptions], Root> =
  (options: DirectiveOptions) => (tree: Root) => {
    const {
      registry,
      file,
      diagnostics,
      islands,
      resolveRoute,
      resolveAsset,
      diagramVariants,
    } = options;

    const childOnly = childOnlyDirectives(registry);
    // Populated by a parent container before its children are visited, because
    // the traversal reaches a parent first.
    const sanctioned = new Set<RootContent>();

    visit(tree, (node, index, parent) => {
      if (!isDirective(node)) return;

      const raw = positionOf(node);
      const line = raw.line === null ? null : raw.line + options.lineOffset;
      const column = raw.column;

      if (node.type === "textDirective") {
        // Ordinary prose contains colon patterns such as "17:00" and "1:1",
        // which the parser reads as inline directives. An unhandled directive
        // emits nothing, so leaving them alone would silently delete
        // characters from the page. Anything that does not name a component is
        // restored to the text the author wrote.
        if (registry[node.name] === undefined) {
          restoreLiteral(node, parent, index, options.source);
          return "skip";
        }
        diagnostics.push({
          file,
          line,
          column,
          message: `inline directive ":${node.name}" is not supported. A component occupies a block: write "::${node.name}{...}" for a leaf or ":::${node.name}" for a container`,
        });
        return;
      }

      const entry = registry[node.name];
      if (!entry) {
        const known = Object.keys(registry).sort().join(", ");
        diagnostics.push({
          file,
          line,
          column,
          message: `unknown directive "${node.name}". The registry defines ${known}`,
        });
        return;
      }

      if (childOnly.has(node.name) && !sanctioned.has(node)) {
        const parents = Object.entries(registry)
          .filter(
            ([, e]) =>
              e.children.kind === "directives" && e.children.name === node.name,
          )
          .map(([parentName]) => parentName);
        diagnostics.push({
          file,
          line,
          column,
          message: `"${node.name}" only appears inside ${parents.join(" or ")}`,
        });
        return;
      }

      const ctx: CoerceContext = { file, line, column, resolveRoute, resolveAsset };
      const props = coerceProps(
        node.name,
        entry,
        node.attributes ?? {},
        ctx,
        diagnostics,
      );

      if (
        !validateChildren(
          node,
          entry.children,
          file,
          options.lineOffset,
          diagnostics,
          sanctioned,
        )
      ) {
        return;
      }
      if (props === null) return;

      // A diagram with no animated variant is a plain figure. Emitting an
      // island for it would ship a mount point with nothing to mount.
      // The variant lookup uses the stem as written, because the coerced value
      // is the resolved URL of the checked-in SVG.
      if (
        node.name === "diagram" &&
        !diagramVariants.has(String(node.attributes?.["name"] ?? ""))
      ) {
        emitStaticDiagram(node, props);
        return;
      }

      // A sanctioned child is content its parent arranges, not a mount point of
      // its own. It emits a marker carrying its props, and the parent island
      // reads those markers to build the data both renders work from.
      if (childOnly.has(node.name)) {
        const data = node.data ?? (node.data = {});
        data.hName = "div";
        data.hProperties = {
          "data-child-directive": node.name,
          "data-child-props": JSON.stringify(props),
        };
        return;
      }

      const id = `island-${islands.length}`;
      const mount = entry.children.kind === "none" ? "replace" : "hydrate";
      islands.push({ id, component: node.name, props, mount });

      const data = node.data ?? (node.data = {});
      data.hName = "podium-island";
      data.hProperties = {
        "data-island-id": id,
        "data-island": node.name,
        "data-island-mount": mount,
        "data-island-props": JSON.stringify(props),
      };
    });
  };

/** Renders a diagram directive as the checked-in SVG with its required alt text. */
function emitStaticDiagram(node: DirectiveNode, props: IslandProps): void {
  const data = node.data ?? (node.data = {});
  data.hName = "img";
  data.hProperties = {
    src: String(props["name"] ?? ""),
    alt: String(props["alt"] ?? ""),
    className: ["diagram"],
  };
  node.children = [];
}

/**
 * Enforces the entry's ChildrenSpec, and sanctions the child directives a
 * container legitimately holds. Returns false when the content is wrong, which
 * stops the caller emitting an island whose content it cannot arrange.
 */
function validateChildren(
  node: DirectiveNode,
  spec: IslandRegistry[string]["children"],
  file: string,
  lineOffset: number,
  diagnostics: BuildDiagnostic[],
  sanctioned: Set<RootContent>,
): boolean {
  const raw = positionOf(node);
  const line = raw.line === null ? null : raw.line + lineOffset;
  const column = raw.column;

  if (spec.kind === "none") {
    if (node.type === "containerDirective") {
      diagnostics.push({
        file,
        line,
        column,
        message: `"${node.name}" takes no content, so write it as a leaf: "::${node.name}{...}"`,
      });
      return false;
    }
    return true;
  }

  if (node.type !== "containerDirective") {
    diagnostics.push({
      file,
      line,
      column,
      message: `"${node.name}" wraps content, so write it as a container: ":::${node.name}"`,
    });
    return false;
  }

  if (spec.kind === "markdown") return true;

  const named: RootContent[] = [];
  const stray: RootContent[] = [];
  for (const child of node.children) {
    if (isDirective(child) && child.name === spec.name) named.push(child);
    else stray.push(child);
  }

  if (stray.length > 0) {
    const first = stray[0];
    const at = first === undefined ? raw : positionOf(first);
    diagnostics.push({
      file,
      line: at.line === null ? null : at.line + lineOffset,
      column: at.column,
      message: `"${node.name}" holds only "${spec.name}" containers, and this content is not one`,
    });
    return false;
  }

  if (named.length < spec.min) {
    diagnostics.push({
      file,
      line,
      column,
      message: `"${node.name}" needs at least ${spec.min} "${spec.name}" children, found ${named.length}`,
    });
    return false;
  }

  for (const child of named) sanctioned.add(child);
  return true;
}
