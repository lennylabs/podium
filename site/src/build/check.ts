import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";

import { SITE_DIR } from "./config";
import type { RouteIndex } from "./content/links";
import { flattenNav } from "./nav";
import { ICONS } from "./render";
import type { BuildDiagnostic, NavNode, PageModel, SiteConfig } from "./types";

/**
 * Text tokens and the surfaces they render on. Each pair must clear the WCAG AA
 * ratio for body text in both themes.
 *
 * The accent is a surface colour carrying dark text, or text on the dark
 * ground. It never sets small text on a light background, which is why the
 * light theme carries a separate link tone and why --accent appears here only
 * as a background.
 */
const CONTRAST_PAIRS: Array<{ text: string; background: string }> = [
  { text: "--body", background: "--page" },
  { text: "--body", background: "--surface" },
  { text: "--ink", background: "--page" },
  { text: "--ink", background: "--surface" },
  { text: "--meta", background: "--page" },
  { text: "--faint", background: "--page" },
  { text: "--link", background: "--page" },
  { text: "--link", background: "--surface" },
  { text: "--on-accent", background: "--accent" },
];

const AA_NORMAL = 4.5;

export type CheckInput = {
  config: SiteConfig;
  pages: PageModel[];
  nav: NavNode[];
  index: RouteIndex;
  searchIndexBytes: number;
};

/**
 * Rules that need the finished corpus rather than a single page. Page-level
 * problems are already reported by the pipeline that produced them.
 */
export function runChecks(input: CheckInput): BuildDiagnostic[] {
  const diagnostics: BuildDiagnostic[] = [];

  checkOrphans(input, diagnostics);
  checkSiteOriginLinks(input, diagnostics);
  checkSearchIndexSize(input, diagnostics);
  checkIcons(input, diagnostics);
  diagnostics.push(...checkContrast());

  return diagnostics;
}

/**
 * The browser icons are referenced from every page's head but appear in no
 * page's body, so nothing else would notice a rename. A missing icon is
 * invisible until someone looks at a tab.
 */
function checkIcons(input: CheckInput, diagnostics: BuildDiagnostic[]): void {
  for (const [role, route] of Object.entries(ICONS)) {
    if (input.index.routes.has(route)) continue;
    diagnostics.push({
      file: "site/src/build/render.ts",
      line: null,
      column: null,
      message: `the ${role} browser icon names "docs${route}", which does not exist`,
    });
  }
}

/** A published page that no navigation entry reaches is unreachable by reading. */
function checkOrphans(input: CheckInput, diagnostics: BuildDiagnostic[]): void {
  const reachable = new Set(flattenNav(input.nav).map((entry) => entry.route));
  for (const page of input.pages) {
    if (page.hidden) continue;
    if (reachable.has(page.route)) continue;
    diagnostics.push({
      file: page.sourcePath,
      line: 1,
      column: 1,
      message: `page is published at "${page.route}" but no navigation entry reaches it. Set "hidden: true" if that is deliberate`,
    });
  }
}

/**
 * Absolute links back into the published site, wherever they are written.
 *
 * README.md is read here because it is the page most likely to carry one and it
 * is not part of the corpus the pipeline walks. A URL that matches no route is
 * the failure that shipped two live 404s.
 */
function checkSiteOriginLinks(input: CheckInput, diagnostics: BuildDiagnostic[]): void {
  const origin = `${input.config.siteUrl}${input.config.basePath}`;
  const files = ["README.md", "CONTRIBUTING.md"];

  for (const relativePath of files) {
    const absolute = resolve(input.config.repoRoot, relativePath);
    if (!existsSync(absolute)) continue;
    const source = readFileSync(absolute, "utf8");

    source.split("\n").forEach((line, position) => {
      const pattern = new RegExp(`${escapeRegExp(origin)}([^\\s)"'\\]]*)`, "g");
      for (const match of line.matchAll(pattern)) {
        const path = (match[1] ?? "").replace(/[.,;]$/, "");
        if (resolvesToRoute(path, input.index)) continue;
        diagnostics.push({
          file: relativePath,
          line: position + 1,
          column: (match.index ?? 0) + 1,
          message: `"${origin}${path}" matches no published route`,
        });
      }
    });
  }
}

/**
 * Applies the same forms the link resolver accepts, so a trailing-slash URL is
 * judged the way the server will serve it.
 */
function resolvesToRoute(path: string, index: RouteIndex): boolean {
  const clean = path.split("#")[0] ?? "";
  const candidates =
    clean === "" || clean === "/"
      ? ["/index.html"]
      : clean.endsWith("/")
        ? [`${clean}index.html`]
        : clean.endsWith(".html") || clean.includes(".")
          ? [clean]
          : [`${clean}.html`, `${clean}/index.html`];
  return candidates.some((candidate) => index.routes.has(candidate));
}

function checkSearchIndexSize(input: CheckInput, diagnostics: BuildDiagnostic[]): void {
  if (input.searchIndexBytes <= input.config.searchIndexLimitBytes) return;
  diagnostics.push({
    file: "site/src/build/search.ts",
    line: null,
    column: null,
    message: `the search index is ${Math.round(input.searchIndexBytes / 1024)}kB, over the ${Math.round(
      input.config.searchIndexLimitBytes / 1024,
    )}kB limit. Split it per section, or raise the limit deliberately`,
  });
}

/** Reads the declared token values for both themes out of the stylesheet. */
export function readThemes(
  cssPath = resolve(SITE_DIR, "src/styles/tokens.css"),
): { light: Map<string, string>; dark: Map<string, string> } {
  const css = readFileSync(cssPath, "utf8");
  const light = new Map<string, string>();
  const dark = new Map<string, string>();

  const rootBlock = css.match(/:root\s*\{([\s\S]*?)\n\}/);
  if (rootBlock?.[1]) collectVariables(rootBlock[1], light);

  const darkBlock = css.match(/:root\[data-theme="dark"\]\s*\{([\s\S]*?)\n\}/);
  if (darkBlock?.[1]) {
    collectVariables(darkBlock[1], dark);
    for (const [name, value] of light) if (!dark.has(name)) dark.set(name, value);
  }

  return { light, dark };
}

function collectVariables(block: string, into: Map<string, string>): void {
  for (const match of block.matchAll(/(--[\w-]+)\s*:\s*([^;]+);/g)) {
    const name = match[1];
    const value = (match[2] ?? "").trim();
    if (name && /^#[0-9a-fA-F]{3,8}$/.test(value)) into.set(name, value);
  }
}

/** Fails any text token that does not clear AA on a surface it renders on. */
export function checkContrast(): BuildDiagnostic[] {
  const diagnostics: BuildDiagnostic[] = [];
  const themes = readThemes();

  for (const [themeName, tokens] of Object.entries(themes)) {
    for (const pair of CONTRAST_PAIRS) {
      const text = tokens.get(pair.text);
      const background = tokens.get(pair.background);
      if (text === undefined || background === undefined) continue;
      const ratio = contrastRatio(text, background);
      if (ratio >= AA_NORMAL) continue;
      diagnostics.push({
        file: "site/src/styles/tokens.css",
        line: null,
        column: null,
        message: `${themeName} theme: ${pair.text} (${text}) on ${pair.background} (${background}) is ${ratio.toFixed(2)}:1, below the ${AA_NORMAL}:1 minimum`,
      });
    }
  }

  return diagnostics;
}

export function contrastRatio(foreground: string, background: string): number {
  const a = relativeLuminance(foreground);
  const b = relativeLuminance(background);
  const lighter = Math.max(a, b);
  const darker = Math.min(a, b);
  return (lighter + 0.05) / (darker + 0.05);
}

function relativeLuminance(hex: string): number {
  const [r, g, b] = parseHex(hex);
  const channel = (value: number): number => {
    const scaled = value / 255;
    return scaled <= 0.03928
      ? scaled / 12.92
      : Math.pow((scaled + 0.055) / 1.055, 2.4);
  };
  return 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b);
}

function parseHex(hex: string): [number, number, number] {
  let value = hex.replace("#", "");
  if (value.length === 3) {
    value = value
      .split("")
      .map((character) => character + character)
      .join("");
  }
  return [
    Number.parseInt(value.slice(0, 2), 16),
    Number.parseInt(value.slice(2, 4), 16),
    Number.parseInt(value.slice(4, 6), 16),
  ];
}

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}
