import type { ComponentType } from "react";

import type { BuildDiagnostic, IslandProps } from "../../build/types";

/**
 * Directive attributes arrive as strings. A PropSpec says what the value means,
 * and the validator coerces it before it reaches a component.
 */
export type PropSpec =
  | { kind: "string"; required?: boolean; oneOf?: readonly string[]; default?: string }
  | { kind: "number"; required?: boolean; default?: number }
  | { kind: "boolean"; required?: boolean; default?: boolean }
  /** A link into the corpus, resolved by the same resolver that checks body links. */
  | { kind: "route"; required?: boolean }
  /**
   * A file that must exist under docs/. `within` and `extension` let an entry
   * accept a bare stem, so `name="sync-watch"` can mean
   * docs/assets/diagrams/sync-watch.svg.
   */
  | { kind: "asset"; required?: boolean; within?: string; extension?: string };

export type ChildrenSpec =
  /** Leaf directive: the node carries no content of its own. */
  | { kind: "none" }
  /** Container whose markdown content the component arranges. */
  | { kind: "markdown" }
  /** Container whose direct children must all be a named container directive. */
  | { kind: "directives"; name: string; min: number };

export type FallbackSpec =
  /** The named prop resolves to the static content shown before mounting. */
  | { from: "prop"; name: string }
  /** The container's own markdown is what a reader sees without JavaScript. */
  | { from: "children" };

export type IslandDefinition<
  P extends IslandProps = IslandProps,
  C = P,
> = {
  props: { [K in keyof P]: PropSpec };
  children: ChildrenSpec;
  fallback: FallbackSpec;
  /**
   * Loads the component that mounts in the browser. A child-only entry has
   * none: its parent arranges it, so it is never a mount point of its own.
   */
  load?: () => Promise<{ default: ComponentType<C> }>;
};

/**
 * Holds the prop type at the definition site so a component that reads an
 * undeclared prop fails typechecking rather than reading undefined at runtime.
 */
export function defineIsland<P extends IslandProps, C = P>(
  definition: IslandDefinition<P, C>,
): IslandDefinition<IslandProps> {
  return definition as unknown as IslandDefinition<IslandProps>;
}

export type IslandRegistry = Record<string, IslandDefinition<IslandProps>>;

/**
 * Checks the registry before any markdown is read. A fallback that names a prop
 * the entry does not declare, or names an optional one, would let an island
 * reach a reader with nothing to show, so it fails here rather than in review.
 */
export function validateRegistry(registry: IslandRegistry): BuildDiagnostic[] {
  const diagnostics: BuildDiagnostic[] = [];
  const file = "site/src/components/islands/registry.ts";

  for (const [name, entry] of Object.entries(registry)) {
    if (entry.fallback.from === "prop") {
      const spec = entry.props[entry.fallback.name];
      if (!spec) {
        diagnostics.push({
          file,
          line: null,
          column: null,
          message: `island "${name}" names "${entry.fallback.name}" as its fallback but does not declare that prop`,
        });
      } else if (!("required" in spec) || spec.required !== true) {
        diagnostics.push({
          file,
          line: null,
          column: null,
          message: `island "${name}" names optional prop "${entry.fallback.name}" as its fallback; a fallback prop must be required`,
        });
      }
    }

    if (entry.fallback.from === "children" && entry.children.kind === "none") {
      diagnostics.push({
        file,
        line: null,
        column: null,
        message: `island "${name}" takes its fallback from children but declares no children`,
      });
    }

    if (entry.children.kind === "directives" && entry.children.min < 1) {
      diagnostics.push({
        file,
        line: null,
        column: null,
        message: `island "${name}" declares a child minimum below 1`,
      });
    }
  }

  return diagnostics;
}

export type AssetResolver = (
  spec: Extract<PropSpec, { kind: "asset" }>,
  value: string,
) => string | null;

export type RouteResolver = (value: string) => string | null;

export type CoerceContext = {
  file: string;
  line: number | null;
  column: number | null;
  resolveRoute: RouteResolver;
  resolveAsset: AssetResolver;
};

/**
 * Coerces raw directive attributes against an entry's declared props. Returns
 * null when anything failed, having pushed a located diagnostic for each.
 */
export function coerceProps(
  name: string,
  entry: IslandDefinition<IslandProps>,
  attributes: Record<string, string | null | undefined>,
  ctx: CoerceContext,
  diagnostics: BuildDiagnostic[],
): IslandProps | null {
  const before = diagnostics.length;
  const out: IslandProps = {};
  const at = { file: ctx.file, line: ctx.line, column: ctx.column };

  for (const key of Object.keys(attributes)) {
    if (!(key in entry.props)) {
      const declared = Object.keys(entry.props);
      diagnostics.push({
        ...at,
        message:
          `island "${name}" has no prop "${key}". ` +
          (declared.length === 0
            ? "It declares no props."
            : `It declares ${declared.sort().join(", ")}.`),
      });
    }
  }

  for (const [key, spec] of Object.entries(entry.props)) {
    const raw = attributes[key];
    if (raw === undefined || raw === null) {
      if (spec.required === true) {
        diagnostics.push({
          ...at,
          message: `island "${name}" requires prop "${key}"`,
        });
      } else if ("default" in spec && spec.default !== undefined) {
        out[key] = spec.default;
      }
      continue;
    }

    switch (spec.kind) {
      case "string": {
        if (spec.oneOf && !spec.oneOf.includes(raw)) {
          diagnostics.push({
            ...at,
            message: `island "${name}" prop "${key}" must be one of ${spec.oneOf.join(", ")}, got "${raw}"`,
          });
          break;
        }
        out[key] = raw;
        break;
      }
      case "number": {
        const parsed = Number(raw);
        if (raw.trim() === "" || !Number.isFinite(parsed)) {
          diagnostics.push({
            ...at,
            message: `island "${name}" prop "${key}" must be a number, got "${raw}"`,
          });
          break;
        }
        out[key] = parsed;
        break;
      }
      case "boolean": {
        if (raw === "true" || raw === "") out[key] = true;
        else if (raw === "false") out[key] = false;
        else {
          diagnostics.push({
            ...at,
            message: `island "${name}" prop "${key}" must be true or false, got "${raw}"`,
          });
        }
        break;
      }
      case "route": {
        const resolved = ctx.resolveRoute(raw);
        if (resolved === null) {
          diagnostics.push({
            ...at,
            message: `island "${name}" prop "${key}" does not resolve to a page: "${raw}"`,
          });
          break;
        }
        out[key] = resolved;
        break;
      }
      case "asset": {
        const resolved = ctx.resolveAsset(spec, raw);
        if (resolved === null) {
          diagnostics.push({
            ...at,
            message: `island "${name}" prop "${key}" does not name a file that exists: "${raw}"`,
          });
          break;
        }
        out[key] = resolved;
        break;
      }
    }
  }

  return diagnostics.length === before ? out : null;
}
