import {
  Children,
  createElement,
  Fragment,
  isValidElement,
  type ComponentType,
  type ReactNode,
} from "react";
import { renderToStaticMarkup, renderToString } from "react-dom/server";
import { toJsxRuntime } from "hast-util-to-jsx-runtime";
import { jsx, jsxs } from "react/jsx-runtime";

import { ActionLinks } from "../components/content/ActionLinks";
import { Callout } from "../components/content/Callout";
import { CodeBlock } from "../components/content/CodeBlock";
import type { IslandRegistry } from "../components/islands/props";
import type { IslandProps, IslandRef, PageModel, SiteConfig } from "./types";

const IMAGE_EXTENSIONS = [".svg", ".png", ".jpg", ".jpeg", ".webp", ".avif"];

/**
 * Loads the component for every island a page declares.
 *
 * Container islands are rendered on the server by the same component that
 * hydrates in the browser, which is what gives hydration matching markup to
 * attach to.
 */
export async function resolveIslandComponents(
  registry: IslandRegistry,
  islands: IslandRef[],
): Promise<Map<string, ComponentType<Record<string, unknown>>>> {
  const resolved = new Map<string, ComponentType<Record<string, unknown>>>();
  for (const island of islands) {
    if (island.mount !== "hydrate" || resolved.has(island.component)) continue;
    const entry = registry[island.component];
    if (entry?.load === undefined) continue;
    const module = await entry.load();
    resolved.set(
      island.component,
      module.default as ComponentType<Record<string, unknown>>,
    );
  }
  return resolved;
}

type IslandPlaceholderProps = {
  "data-island"?: string;
  "data-island-id"?: string;
  "data-island-mount"?: string;
  "data-island-props"?: string;
  children?: ReactNode;
};

/**
 * Turns a page body into React elements.
 *
 * The component table is where the pipeline's renamed nodes become components:
 * a callout node becomes Callout, a fence becomes CodeBlock, and an island node
 * becomes its placeholder.
 */
export function renderBody(
  page: PageModel,
  components: Map<string, ComponentType<Record<string, unknown>>>,
): ReactNode {
  const Island = (props: IslandPlaceholderProps): ReactNode => {
    const name = props["data-island"] ?? "";
    const mount = props["data-island-mount"] ?? "replace";
    const parsed = parseProps(props["data-island-props"]);

    let inner: ReactNode = props.children ?? null;
    if (mount === "hydrate") {
      const Component = components.get(name);
      if (Component !== undefined) {
        // A container island renders from data rather than from React children,
        // so the browser can rebuild the same input by reading the DOM and
        // hydrate against markup that matches.
        inner =
          name === "tabs"
            ? createElement(Component, { panels: extractPanels(props.children) })
            : createElement(Component, parsed, props.children);
      }
    } else {
      inner = leafFallback(name, parsed) ?? inner;
    }

    return createElement(
      "div",
      {
        "data-island": name,
        "data-island-id": props["data-island-id"],
        "data-island-mount": mount,
        "data-island-props": props["data-island-props"],
        className: "island",
      },
      inner,
    );
  };

  return toJsxRuntime(page.body, {
    Fragment,
    jsx: jsx as never,
    jsxs: jsxs as never,
    components: {
      "podium-callout": Callout as never,
      "podium-island": Island as never,
      pre: CodeBlock as never,
    },
    passKeys: true,
  });
}

/**
 * Turns the child markers a container directive produced into the data its
 * component renders from. Each panel's markup is rendered once here and travels
 * in the page, which is what the browser reads back before hydrating.
 */
function extractPanels(children: ReactNode): Array<{ label: string; html: string }> {
  const panels: Array<{ label: string; html: string }> = [];

  for (const child of Children.toArray(children)) {
    if (!isValidElement(child)) continue;
    const childProps = child.props as {
      "data-child-props"?: string;
      children?: ReactNode;
    };
    const raw = childProps["data-child-props"];
    if (typeof raw !== "string") continue;

    let label = "";
    try {
      const parsed = JSON.parse(raw) as { label?: unknown };
      if (typeof parsed.label === "string") label = parsed.label;
    } catch {
      // The build validated this payload, so a parse failure is unreachable.
    }

    panels.push({
      label,
      html: renderToStaticMarkup(childProps.children as never),
    });
  }

  return panels;
}

function parseProps(raw: string | undefined): Record<string, unknown> {
  if (raw === undefined) return {};
  try {
    return JSON.parse(raw) as Record<string, unknown>;
  } catch {
    return {};
  }
}

/**
 * The static content a leaf island shows before it mounts. It is what a reader
 * without JavaScript, a crawler, and a reader who has asked for reduced motion
 * are left with, so it carries the page's meaning on its own.
 */
function leafFallback(name: string, props: Record<string, unknown>): ReactNode {
  if (name !== "diagram") return null;
  const src = typeof props["name"] === "string" ? props["name"] : "";
  const alt = typeof props["alt"] === "string" ? props["alt"] : "";
  if (!IMAGE_EXTENSIONS.some((extension) => src.endsWith(extension))) return null;
  return createElement("img", { className: "diagram", src, alt });
}

/** Browser icons, published from docs/assets/logo like any other asset. */
export const ICONS = {
  light: "/assets/logo/podium-mark-light.svg",
  dark: "/assets/logo/podium-mark-dark.svg",
  tile: "/assets/logo/podium-tile.svg",
} as const;

/**
 * The icon set a browser chooses from.
 *
 * The two marks are drawn for the background they sit on: the light one carries
 * dark ink and the dark one carries paper ink, so each stays legible against a
 * tab strip of that colour. Support for `media` on an icon link varies, so the
 * tile is declared last with no condition. It is square and carries its own
 * background, which makes it the one icon that reads correctly anywhere, and a
 * browser that ignores the conditions lands on it.
 */
function iconLinks(basePath: string): string[] {
  return [
    `<link rel="icon" type="image/svg+xml" media="(prefers-color-scheme: light)" href="${escapeHtml(basePath + ICONS.light)}">`,
    `<link rel="icon" type="image/svg+xml" media="(prefers-color-scheme: dark)" href="${escapeHtml(basePath + ICONS.dark)}">`,
    `<link rel="icon" type="image/svg+xml" href="${escapeHtml(basePath + ICONS.tile)}">`,
    `<link rel="apple-touch-icon" href="${escapeHtml(basePath + ICONS.tile)}">`,
  ];
}

export type DocumentOptions = {
  config: SiteConfig;
  title: string;
  description: string;
  route: string;
  bodyClass: string;
  /** Emitted client entry, or null when the page needs no JavaScript. */
  scriptSrc: string | null;
  stylesheets: string[];
  /** True when the page carries islands and its markup must be hydratable. */
  hydratable: boolean;
  children: ReactNode;
};

/**
 * Wraps rendered content in a complete HTML document.
 *
 * A page with no islands renders through renderToStaticMarkup, whose output
 * carries no hydration metadata and is never hydrated. A page with islands
 * renders through renderToString so the client has something to attach to.
 */
export function renderDocument(options: DocumentOptions): string {
  const { config } = options;
  const canonical = `${config.siteUrl}${config.basePath}${options.route}`;
  const markup = options.hydratable
    ? renderToString(options.children as never)
    : renderToStaticMarkup(options.children as never);

  const head = [
    '<meta charset="utf-8">',
    '<meta name="viewport" content="width=device-width, initial-scale=1">',
    `<title>${escapeHtml(options.title)}</title>`,
    `<meta name="description" content="${escapeHtml(options.description)}">`,
    `<link rel="canonical" href="${escapeHtml(canonical)}">`,
    `<meta property="og:title" content="${escapeHtml(options.title)}">`,
    `<meta property="og:description" content="${escapeHtml(options.description)}">`,
    `<meta property="og:url" content="${escapeHtml(canonical)}">`,
    '<meta property="og:type" content="website">',
    `<meta property="og:image" content="${escapeHtml(`${config.siteUrl}${config.basePath}${ICONS.tile}`)}">`,
    ...iconLinks(config.basePath),
    ...options.stylesheets.map(
      (href) => `<link rel="stylesheet" href="${escapeHtml(href)}">`,
    ),
    // Applies the stored theme before first paint, so a reader who chose dark
    // never sees a light flash on navigation.
    `<script>${THEME_BOOTSTRAP}</script>`,
  ].join("\n    ");

  const script =
    options.scriptSrc === null
      ? ""
      : `\n    <script type="module" src="${escapeHtml(options.scriptSrc)}"></script>`;

  return `<!doctype html>
<html lang="en">
  <head>
    ${head}
  </head>
  <body class="${escapeHtml(options.bodyClass)}" data-base-path="${escapeHtml(config.basePath)}">
    ${markup}${script}
  </body>
</html>
`;
}

const THEME_BOOTSTRAP =
  `try{var t=localStorage.getItem("podium-theme");` +
  `if(t==="dark"||t==="light")document.documentElement.setAttribute("data-theme",t)}catch(e){}`;

export function escapeHtml(value: string): string {
  return value
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

export { ActionLinks };
export type { IslandProps };
